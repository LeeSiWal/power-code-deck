package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A non-HTML page fetched through the proxy is served from the deck's origin.
// It must come back inert: no scripts with the deck's privileges, no rendering
// at top level, and no upstream cookies planted on the deck.
func TestProxyNonHTMLIsInert(t *testing.T) {
	const svg = `<svg xmlns="http://www.w3.org/2000/svg"><script>fetch('/api/agents',{method:'POST'})</script></svg>`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Set-Cookie", "pcd_token=planted; Path=/")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte(svg))
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/proxy?url="+url.QueryEscape(upstream.URL+"/x.svg"), nil)
	rec := httptest.NewRecorder()
	ProxyHandler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	h := rec.Header()
	if csp := h.Get("Content-Security-Policy"); !strings.HasPrefix(csp, "sandbox") {
		t.Errorf("Content-Security-Policy = %q, want a sandbox policy", csp)
	}
	if cd := h.Get("Content-Disposition"); cd != "attachment" {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	for _, leaked := range []string{"Set-Cookie", "Access-Control-Allow-Origin"} {
		if v := h.Get(leaked); v != "" {
			t.Errorf("upstream %s copied onto the deck: %q", leaked, v)
		}
	}
	if ct := h.Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want the upstream type", ct)
	}
	if rec.Body.String() != svg {
		t.Errorf("body changed: %q", rec.Body.String())
	}
}
