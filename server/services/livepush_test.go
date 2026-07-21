package services

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestLivePushSend is a manual, opt-in smoke test: it reads the real subscriptions +
// VAPID keys from a live DB and sends N notifications, spaced out, using the exact
// production send path (sendWebPush). Run it against the prod DB like:
//
//	RUN_LIVE_PUSH=1 LIVE_PUSH_DB=/home/siwal/PowerCodeDeck/powercodedeck.db \
//	  go test ./services/ -run TestLivePushSend -count=1 -v
//
// It never runs in the normal suite (guarded by RUN_LIVE_PUSH).
func TestLivePushSend(t *testing.T) {
	if os.Getenv("RUN_LIVE_PUSH") != "1" {
		t.Skip("set RUN_LIVE_PUSH=1 to send real notifications")
	}
	dbPath := os.Getenv("LIVE_PUSH_DB")
	if dbPath == "" {
		t.Fatal("LIVE_PUSH_DB must point at the live powercodedeck.db")
	}
	// Read-only, WAL — safe alongside the running server.
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	priv, pub := loadOrCreateVAPID(db)
	if priv == nil || pub == "" {
		t.Fatal("no VAPID keys in DB")
	}
	contact := os.Getenv("LIVE_PUSH_CONTACT")
	if contact == "" {
		contact = "https://pcd.19921005.xyz"
	}

	type sub struct{ endpoint, p256dh, auth string }
	var subs []sub
	rows, err := db.Query("SELECT endpoint, p256dh, auth FROM push_subscriptions")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s sub
		if rows.Scan(&s.endpoint, &s.p256dh, &s.auth) == nil {
			subs = append(subs, s)
		}
	}
	rows.Close()
	if len(subs) == 0 {
		t.Fatal("no push subscriptions — enable notifications on a device first")
	}
	t.Logf("sending to %d subscription(s)", len(subs))

	client := &http.Client{Timeout: 15 * time.Second}
	total := envInt("LIVE_PUSH_COUNT", 3)
	gap := time.Duration(envInt("LIVE_PUSH_INTERVAL", 5)) * time.Second
	for i := 1; i <= total; i++ {
		msg := fmt.Sprintf(`{"title":"PowerCodeDeck 테스트 %d/%d","body":"알림이 잘 도착하나요? (%d번째)","tag":"pcd-test-%d","url":"/"}`, i, total, i, i)
		for _, s := range subs {
			keys := webpushKeys{p256dh: decodeB64(s.p256dh), auth: decodeB64(s.auth)}
			status, err := sendWebPush(client, s.endpoint, keys, priv, pub, contact, []byte(msg), 60)
			if err != nil {
				t.Errorf("[%d] send error: %v", i, err)
				continue
			}
			// Push services return 201 Created on accept.
			t.Logf("[%d/%d] %s → HTTP %d", i, total, shortEndpoint(s.endpoint), status)
			if status != http.StatusCreated && status != http.StatusOK {
				t.Errorf("[%d] unexpected status %d", i, status)
			}
		}
		if i < total {
			time.Sleep(gap)
		}
	}
}

// envInt reads an int env var, falling back to def when unset or unparseable.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func shortEndpoint(e string) string {
	if len(e) > 45 {
		return e[:45] + "…"
	}
	return e
}
