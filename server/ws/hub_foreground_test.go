package ws

import "testing"

// Push suppression decides NOT to alert. If it is wrong in the permissive direction
// you get a double buzz; if it is wrong in the other direction the alert is lost
// entirely — and a missed 승인 필요 leaves the agent blocked with nobody watching.
// These pin the asymmetry: suppress only on an explicit, live "I am looking at it".

func newFGClient(hub *Hub, deviceID string) *Client {
	c := &Client{hub: hub, deviceID: deviceID, send: make(chan []byte, 1)}
	hub.clients.Store(c, true)
	return c
}

func TestDeviceForegroundRequiresExplicitReport(t *testing.T) {
	h := &Hub{}
	c := newFGClient(h, "dev-1")

	// A brand-new connection has said nothing yet. Unknown must mean "push it".
	if h.deviceForeground("dev-1") {
		t.Fatal("a connection that never reported visibility was treated as foregrounded — the push would be suppressed on a guess")
	}

	c.setForeground(true)
	if !h.deviceForeground("dev-1") {
		t.Fatal("device reported visible but deviceForeground says otherwise")
	}

	// Backgrounding must re-enable push immediately.
	c.setForeground(false)
	if h.deviceForeground("dev-1") {
		t.Fatal("device reported hidden but is still counted as foregrounded — pushes would stay suppressed while the phone is away")
	}
}

func TestDeviceForegroundIsPerDevice(t *testing.T) {
	h := &Hub{}
	newFGClient(h, "phone").setForeground(true)
	newFGClient(h, "laptop") // never reported

	if !h.deviceForeground("phone") {
		t.Fatal("the foregrounded device was not detected")
	}
	if h.deviceForeground("laptop") {
		t.Fatal("another device's foreground state leaked — the laptop would lose its push")
	}
	if h.deviceForeground("never-seen") {
		t.Fatal("an unknown device id was treated as foregrounded")
	}
	if h.deviceForeground("") {
		t.Fatal("an empty device id must never suppress a push")
	}
}

// A device may hold several tabs. One visible tab is enough for the toast to be seen,
// so it is enough to skip the push — but a hidden tab must not veto a visible one.
func TestDeviceForegroundAnyLiveTabCounts(t *testing.T) {
	h := &Hub{}
	newFGClient(h, "dev-2") // hidden tab
	visible := newFGClient(h, "dev-2")
	visible.setForeground(true)

	if !h.deviceForeground("dev-2") {
		t.Fatal("a visible tab was ignored because another tab on the same device was hidden")
	}
}

// A dropped socket must restore push. Otherwise closing the app while its last known
// state was "visible" would silence notifications indefinitely.
func TestDeviceForegroundDropsWithTheConnection(t *testing.T) {
	h := &Hub{}
	c := newFGClient(h, "dev-3")
	c.setForeground(true)
	if !h.deviceForeground("dev-3") {
		t.Fatal("setup: device should be foregrounded")
	}

	h.clients.Delete(c) // what unregister() does when the socket dies
	if h.deviceForeground("dev-3") {
		t.Fatal("a device with no live connection is still counted as foregrounded — push would stay suppressed after the app is gone")
	}
}
