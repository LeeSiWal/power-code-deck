package handlers

import (
	"io/fs"
	"net/http"
)

// AppShell serves client routes directly. FileServer redirects index.html to
// ./, which loops when a deep client route uses it as its fallback.
func AppShell(files fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := fs.ReadFile(files, "index.html")
		if err != nil {
			http.Error(w, "app shell unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if r.Method != http.MethodHead {
			_, _ = w.Write(page)
		}
	}
}
