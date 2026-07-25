package services

// PushService is the Web Push fan-out: it owns the server's VAPID identity, the set
// of browser subscriptions, and the delivery loop. It is intentionally thin — the
// wire crypto lives in webpush.go — so this file is just storage + fan-out + prune.

import (
	"crypto/ecdsa"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// PushSubscription mirrors the browser PushManager subscription JSON the client
// POSTs after the user opts in.
type PushSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	// DeviceID ties this subscription to the browser/device that registered it, so a
	// notification can be sent to only the session's active device. Empty for legacy
	// subscriptions registered before this existed — those will simply never match an
	// active-device filter (no fallback, by design), so re-subscribing refreshes it.
	DeviceID string `json:"deviceId"`
}

// PushMessage is the notification payload the service worker renders. Kept tiny on
// purpose: push services cap the encrypted body (~4KB) and phones show little.
type PushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag,omitempty"` // collapses same-tag notifications
	URL   string `json:"url,omitempty"` // where a tap should navigate
}

type PushService struct {
	db      *sql.DB
	client  *http.Client
	priv    *ecdsa.PrivateKey // VAPID signing key; nil disables push
	pubB64  string            // VAPID public key (applicationServerKey)
	contact string            // VAPID "sub" claim
}

// NewPushService loads (or first-time generates) the VAPID keypair and returns a
// ready service. It never fails hard: if key setup somehow fails, push is simply
// disabled (PublicKey == "", Notify is a no-op) so the rest of the deck runs.
func NewPushService(db *sql.DB, contact string) *PushService {
	priv, pub := loadOrCreateVAPID(db)
	return &PushService{
		db:      db,
		client:  &http.Client{Timeout: 15 * time.Second},
		priv:    priv,
		pubB64:  pub,
		contact: contact,
	}
}

// Enabled reports whether push is usable (a VAPID key is present).
func (s *PushService) Enabled() bool { return s.priv != nil && s.pubB64 != "" }

// PublicKey is the base64url application-server key a browser subscribes with.
func (s *PushService) PublicKey() string { return s.pubB64 }

// Subscribe records (or refreshes) a browser subscription, keyed by endpoint.
func (s *PushService) Subscribe(sub PushSubscription) bool {
	if sub.Endpoint == "" || sub.Keys.P256dh == "" || sub.Keys.Auth == "" {
		return false
	}
	_, err := s.db.Exec(
		`INSERT INTO push_subscriptions(endpoint, p256dh, auth, device_id) VALUES(?, ?, ?, ?)
		 ON CONFLICT(endpoint) DO UPDATE SET p256dh=excluded.p256dh, auth=excluded.auth, device_id=excluded.device_id`,
		sub.Endpoint, sub.Keys.P256dh, sub.Keys.Auth, sub.DeviceID,
	)
	if err != nil {
		log.Printf("push: subscribe failed: %v", err)
		return false
	}
	return true
}

// Unsubscribe drops a subscription — called both from the client (user opts out)
// and automatically when a push service reports the endpoint gone.
func (s *PushService) Unsubscribe(endpoint string) {
	_, _ = s.db.Exec("DELETE FROM push_subscriptions WHERE endpoint = ?", endpoint)
}

// SetActiveDevice records deviceID as the owner of agentID's session — the last
// device to open or reclaim it. A blank deviceID is ignored (legacy client), leaving
// any prior owner in place rather than clearing it.
func (s *PushService) SetActiveDevice(agentID, deviceID string) {
	if agentID == "" || deviceID == "" {
		return
	}
	_, _ = s.db.Exec(
		`INSERT INTO agent_active_device(agent_id, device_id, updated_at) VALUES(?, ?, datetime('now'))
		 ON CONFLICT(agent_id) DO UPDATE SET device_id=excluded.device_id, updated_at=excluded.updated_at`,
		agentID, deviceID,
	)
}

// ActiveDevice returns the device that currently owns agentID's session, or "" if
// none has been recorded.
func (s *PushService) ActiveDevice(agentID string) string {
	var dev string
	_ = s.db.QueryRow("SELECT device_id FROM agent_active_device WHERE agent_id = ?", agentID).Scan(&dev)
	return dev
}

// NotifyAgent sends msg to ONLY the device that currently owns agentID's session, so
// notifications land on the one device you're actually using instead of every device
// at once. If the agent has no recorded active device, or that device has no push
// subscription, nothing is sent — by design there is no broadcast fallback.
func (s *PushService) NotifyAgent(agentID string, msg PushMessage) {
	dev := s.ActiveDevice(agentID)
	if dev == "" {
		log.Printf("push: skip %q tag=%s → no active device for agent %s", msg.Title, msg.Tag, agentID)
		return
	}
	s.notify(msg, dev)
}

// Notify sends msg to every subscription, concurrently, and prunes any the push
// service reports as gone (404/410). Fire-and-forget: callers are event handlers on
// the hot path, so delivery must never block them.
func (s *PushService) Notify(msg PushMessage) { s.notify(msg, "") }

// notify is the shared fan-out. When deviceID is non-empty, only subscriptions
// registered by that device are targeted; empty means every subscription.
func (s *PushService) notify(msg PushMessage, deviceID string) {
	if !s.Enabled() {
		return
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	type sub struct{ endpoint, p256dh, auth string }
	query := "SELECT endpoint, p256dh, auth FROM push_subscriptions"
	var args []any
	if deviceID != "" {
		query += " WHERE device_id = ?"
		args = append(args, deviceID)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return
	}
	var subs []sub
	for rows.Next() {
		var su sub
		if rows.Scan(&su.endpoint, &su.p256dh, &su.auth) == nil {
			subs = append(subs, su)
		}
	}
	rows.Close()

	// One line per fan-out so delivery is observable: previously only failures logged,
	// so a "notification didn't arrive" report was undiagnosable — you couldn't tell a
	// push that was never sent from one the push service accepted (201) but the phone
	// silently dropped. Now every real event prints where it went and the status.
	log.Printf("push: notify %q tag=%s → %d subs", msg.Title, msg.Tag, len(subs))

	for _, su := range subs {
		go func(su sub) {
			keys := webpushKeys{p256dh: decodeB64(su.p256dh), auth: decodeB64(su.auth)}
			// 4 weeks: hold for a phone that's offline now but back later.
			status, err := sendWebPush(s.client, su.endpoint, keys, s.priv, s.pubB64, s.contact, payload, 2419200)
			if err != nil {
				log.Printf("push:   ✗ %s send error: %v", pushHost(su.endpoint), err)
				return
			}
			if status == http.StatusNotFound || status == http.StatusGone {
				log.Printf("push:   x %s status=%d → pruning dead subscription", pushHost(su.endpoint), status)
				s.Unsubscribe(su.endpoint) // subscription is dead — stop trying
				return
			}
			// 201 Created is the success status for Web Push; anything else (413 too
			// big, 429 rate-limited, 5xx) is worth seeing when a phone reports gaps.
			log.Printf("push:   → %s status=%d", pushHost(su.endpoint), status)
		}(su)
	}
}

// pushHost shortens a subscription endpoint to its host for readable logs
// (web.push.apple.com, *.notify.windows.com, fcm.googleapis.com), never the token.
func pushHost(endpoint string) string {
	rest := endpoint
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// loadOrCreateVAPID returns the persisted VAPID keypair, generating and storing one
// on first run so every device across every restart subscribes against the same
// application-server identity.
func loadOrCreateVAPID(db *sql.DB) (*ecdsa.PrivateKey, string) {
	var privB64, pubB64 string
	_ = db.QueryRow("SELECT value FROM app_config WHERE key = 'vapid_private'").Scan(&privB64)
	_ = db.QueryRow("SELECT value FROM app_config WHERE key = 'vapid_public'").Scan(&pubB64)
	if privB64 != "" && pubB64 != "" {
		if priv, err := parseVAPIDPrivate(privB64); err == nil {
			return priv, pubB64
		}
		log.Printf("push: stored VAPID key unreadable, regenerating")
	}
	privB64, pubB64, err := generateVAPIDKeys()
	if err != nil {
		log.Printf("push: VAPID key generation failed, push disabled: %v", err)
		return nil, ""
	}
	_, _ = db.Exec("INSERT OR REPLACE INTO app_config(key, value) VALUES('vapid_private', ?)", privB64)
	_, _ = db.Exec("INSERT OR REPLACE INTO app_config(key, value) VALUES('vapid_public', ?)", pubB64)
	priv, err := parseVAPIDPrivate(privB64)
	if err != nil {
		return nil, ""
	}
	log.Printf("push: generated new VAPID keypair")
	return priv, pubB64
}

// decodeB64 leniently decodes the base64url key material a browser sends (with or
// without padding, url or std alphabet).
func decodeB64(s string) []byte {
	s = strings.TrimRight(s, "=")
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b
	}
	b, _ := base64.RawStdEncoding.DecodeString(s)
	return b
}
