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
