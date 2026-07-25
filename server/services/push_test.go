package services

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
	"powercodedeck/db"
)

// TestPushServicePersistence exercises the whole non-network path against a real DB:
// migrations build the push tables, the VAPID keypair is generated + persisted (and
// stable across a second construction), and a subscription round-trips through
// storage. This is the runtime evidence that the feature wires up end to end.
func TestPushServicePersistence(t *testing.T) {
	dir := t.TempDir()
	database, err := sql.Open("sqlite", dir+"/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ps := NewPushService(database, "mailto:test@localhost")
	if !ps.Enabled() {
		t.Fatal("push should be enabled after key generation")
	}
	pub := ps.PublicKey()
	if pub == "" {
		t.Fatal("empty public key")
	}

	// A second service on the same DB must reuse the stored keypair, or every
	// restart would invalidate every device's subscription.
	if ps2 := NewPushService(database, "mailto:test@localhost"); ps2.PublicKey() != pub {
		t.Fatalf("VAPID key not stable across restart: %q vs %q", ps2.PublicKey(), pub)
	}

	// Subscription lifecycle.
	var sub PushSubscription
	sub.Endpoint = "https://push.example.com/abc"
	sub.Keys.P256dh = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	sub.Keys.Auth = "BTBZMqHH6r4Tts7J_aSIgg"
	if !ps.Subscribe(sub) {
		t.Fatal("subscribe rejected a valid subscription")
	}
	var n int
	if err := database.QueryRow("SELECT COUNT(*) FROM push_subscriptions").Scan(&n); err != nil || n != 1 {
		t.Fatalf("want 1 stored subscription, got %d (err %v)", n, err)
	}

	// Incomplete subscriptions are refused.
	if ps.Subscribe(PushSubscription{Endpoint: "https://x/y"}) {
		t.Fatal("subscribe accepted an incomplete subscription")
	}

	ps.Unsubscribe(sub.Endpoint)
	_ = database.QueryRow("SELECT COUNT(*) FROM push_subscriptions").Scan(&n)
	if n != 0 {
		t.Fatalf("unsubscribe left %d rows", n)
	}
}

// TestActiveDeviceAndSubscriptionDeviceID covers the device-targeted push wiring:
// a subscription stores its device id, and the active-device record round-trips so
// NotifyAgent can later select only that device's subscription.
func TestActiveDeviceAndSubscriptionDeviceID(t *testing.T) {
	dir := t.TempDir()
	database, err := sql.Open("sqlite", dir+"/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ps := NewPushService(database, "mailto:test@localhost")

	// A subscription remembers which device registered it.
	var sub PushSubscription
	sub.Endpoint = "https://push.example.com/desktop"
	sub.Keys.P256dh = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	sub.Keys.Auth = "BTBZMqHH6r4Tts7J_aSIgg"
	sub.DeviceID = "device-desktop"
	if !ps.Subscribe(sub) {
		t.Fatal("subscribe rejected a valid subscription")
	}
	var stored string
	if err := database.QueryRow("SELECT device_id FROM push_subscriptions WHERE endpoint = ?", sub.Endpoint).Scan(&stored); err != nil {
		t.Fatalf("read device_id: %v", err)
	}
	if stored != "device-desktop" {
		t.Fatalf("device_id not stored: got %q", stored)
	}

	// No active device yet → ActiveDevice is empty (NotifyAgent would send nothing).
	if got := ps.ActiveDevice("agent-1"); got != "" {
		t.Fatalf("expected no active device, got %q", got)
	}

	// Claiming, then re-claiming from another device, moves ownership (last wins).
	ps.SetActiveDevice("agent-1", "device-desktop")
	if got := ps.ActiveDevice("agent-1"); got != "device-desktop" {
		t.Fatalf("active device = %q, want device-desktop", got)
	}
	ps.SetActiveDevice("agent-1", "device-phone")
	if got := ps.ActiveDevice("agent-1"); got != "device-phone" {
		t.Fatalf("active device after handoff = %q, want device-phone", got)
	}

	// A blank device id is ignored — it must not wipe the current owner.
	ps.SetActiveDevice("agent-1", "")
	if got := ps.ActiveDevice("agent-1"); got != "device-phone" {
		t.Fatalf("blank SetActiveDevice clobbered owner: got %q", got)
	}

	// The device-filtered query underlying NotifyAgent selects only matching subs.
	var n int
	_ = database.QueryRow("SELECT COUNT(*) FROM push_subscriptions WHERE device_id = ?", "device-desktop").Scan(&n)
	if n != 1 {
		t.Fatalf("filter by device-desktop = %d rows, want 1", n)
	}
	_ = database.QueryRow("SELECT COUNT(*) FROM push_subscriptions WHERE device_id = ?", "device-phone").Scan(&n)
	if n != 0 {
		t.Fatalf("filter by device-phone = %d rows, want 0 (no sub registered)", n)
	}
}
