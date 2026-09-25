package routing

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestIsLocalNetIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "192.168.1.22": true, "10.0.0.5": true, "172.20.1.1": true,
		"100.64.0.1": true, "100.101.102.103": true, "100.127.255.254": true, "fd7a:115c:a1e0::1": true,
		"8.8.8.8": false, "100.128.0.1": false, "172.32.0.1": false, "1.1.1.1": false, "2606:4700::1111": false,
	} {
		if got := IsLocalNetIP(net.ParseIP(ip)); got != want {
			t.Errorf("%s: %v, want %v", ip, got, want)
		}
	}
}

func TestLocalNetOnlyRefusesPublicAddresses(t *testing.T) {
	ep := LocalEndpoint{ID: "x", URL: "http://8.8.8.8:8080", Kind: "openai", AllowPrivate: true, AllowInsecureHTTP: true, LocalNetOnly: true}
	if err := ep.Validate(); err == nil || !strings.Contains(err.Error(), "private networks") {
		t.Fatalf("public literal accepted: %v", err)
	}
	// A hostname is checked after DNS, at dial time: this one resolves publicly.
	ep.URL = "http://dns.google:8080"
	if err := ep.Validate(); err != nil {
		t.Fatalf("hostname rejected before dial: %v", err)
	}
	if _, err := NewLocalClient().Models(context.Background(), ep); err == nil || !strings.Contains(err.Error(), "private networks") {
		t.Fatalf("public host reached: %v", err)
	}
	// The metadata address inside Tailscale's range stays refused.
	if err := checkIP(net.ParseIP("100.100.100.200"), true); err == nil {
		t.Fatal("metadata address allowed")
	}
}
