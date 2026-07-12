package middleware

import "net/http"

func Helmet(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "0")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Allow iframe embedding from same origin + localhost (for built-in browser panel).
		// X-Frame-Options: SAMEORIGIN allows the app to iframe its own pages.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")

		// CSP: add frame-src to allow localhost iframes.
		// 'wasm-unsafe-eval' lets the terminal (wterm) instantiate its WebAssembly
		// core — without it the browser blocks WebAssembly.instantiate and the
		// terminal renders nothing. It permits WASM compilation only, not JS eval.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"connect-src 'self' ws: wss:; "+
				"img-src 'self' data: blob:; "+
				"font-src 'self' data:; "+
				"frame-src 'self' http://localhost:* http://127.0.0.1:*")

		next.ServeHTTP(w, r)
	})
}
