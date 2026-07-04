package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maxProxyResponseSize = 10 * 1024 * 1024 // 10MB

func ProxyHandler() http.HandlerFunc {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			jsonError(w, "only GET allowed", http.StatusMethodNotAllowed)
			return
		}

		targetURL := r.URL.Query().Get("url")
		if targetURL == "" {
			jsonError(w, "url parameter required", http.StatusBadRequest)
			return
		}

		parsed, err := url.Parse(targetURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			jsonError(w, "invalid URL", http.StatusBadRequest)
			return
		}

		// localhost is allowed (needed for iPad Link Preview bypass via proxy)

		req, err := http.NewRequest("GET", targetURL, nil)
		if err != nil {
			jsonError(w, "request error", http.StatusBadRequest)
			return
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; PowerCodeDeck/0.2)")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9,ko;q=0.8")

		resp, err := client.Do(req)
		if err != nil {
			jsonError(w, "fetch failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxProxyResponseSize))
		if err != nil {
			jsonError(w, "read error", http.StatusBadGateway)
			return
		}

		contentType := resp.Header.Get("Content-Type")

		// For HTML: rewrite relative URLs to absolute and return as srcdoc-ready JSON
		if strings.Contains(contentType, "text/html") {
			baseURL := parsed.Scheme + "://" + parsed.Host
			html := rewriteRelativeURLs(string(body), baseURL, targetURL)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"html":       html,
				"statusCode": resp.StatusCode,
				"url":        targetURL,
			})
			return
		}

		// Non-HTML: pass through as-is (images, CSS, JS, etc.)
		resp.Header.Del("X-Frame-Options")
		resp.Header.Del("Content-Security-Policy")
		resp.Header.Del("Cross-Origin-Embedder-Policy")
		resp.Header.Del("Cross-Origin-Opener-Policy")
		resp.Header.Del("Cross-Origin-Resource-Policy")

		for key, vals := range resp.Header {
			for _, val := range vals {
				w.Header().Add(key, val)
			}
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
	}
}

var (
	srcRe    = regexp.MustCompile(`(src|href|action)\s*=\s*"(/[^"]*)"`)
	srcSqRe  = regexp.MustCompile(`(src|href|action)\s*=\s*'(/[^']*)'`)
	srcsetRe = regexp.MustCompile(`(srcset)\s*=\s*"([^"]*)"`)
)

// iPadOS Safari Link Preview bypass: CSS + JS injected into proxied HTML.
// Safari intercepts touch → hover → Link Preview BEFORE JS click events fire.
// Solution: handle touchend (fires before Safari's click interception) + CSS to
// disable touch-callout, user-drag, and double-tap zoom delay.
const safariBypassInjection = `<style>
*{-webkit-touch-callout:none!important;touch-action:manipulation!important}
a{-webkit-user-drag:none!important;-webkit-tap-highlight-color:transparent!important}
</style>
<script>
(function(){
var sx=0,sy=0;
document.addEventListener('touchstart',function(e){
var t=e.touches[0];sx=t.clientX;sy=t.clientY;
},{passive:true});
document.addEventListener('touchend',function(e){
var t=e.changedTouches[0];
var dx=Math.abs(t.clientX-sx),dy=Math.abs(t.clientY-sy);
if(dx>10||dy>10)return;
var a=document.elementFromPoint(t.clientX,t.clientY);
if(a)a=a.closest('a');
if(a&&a.href){e.preventDefault();e.stopImmediatePropagation();window.location.href=a.href;}
},{capture:true});
document.addEventListener('click',function(e){
var a=e.target.closest('a');
if(a&&a.href){e.preventDefault();e.stopImmediatePropagation();window.location.href=a.href;}
},true);
})();
</script>`

func rewriteRelativeURLs(html, baseURL, pageURL string) string {
	// Inject <base href> so relative URLs resolve correctly
	baseTag := `<base href="` + pageURL + `" />`
	injection := baseTag + safariBypassInjection

	if strings.Contains(strings.ToLower(html), "<head") {
		headIdx := strings.Index(strings.ToLower(html), "<head")
		if headIdx >= 0 {
			closeIdx := strings.Index(html[headIdx:], ">")
			if closeIdx >= 0 {
				insertPos := headIdx + closeIdx + 1
				html = html[:insertPos] + injection + html[insertPos:]
			}
		}
	} else {
		html = injection + html
	}

	// Rewrite /path URLs to absolute baseURL/path
	html = srcRe.ReplaceAllString(html, `${1}="`+baseURL+`${2}"`)
	html = srcSqRe.ReplaceAllString(html, `${1}='`+baseURL+`${2}'`)

	return html
}
