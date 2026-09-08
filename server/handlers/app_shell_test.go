package handlers

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestAppShellDeepLinksDoNotRedirectOrRewriteRequest(t *testing.T) {
	handler := AppShell(fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}})
	for _, path := range []string{"/runs", "/runs/", "/runs/run_example?view=history", "/control", "/settings"} {
		for _, method := range []string{"GET", "HEAD"} {
			request := httptest.NewRequest(method, path, nil)
			before := request.URL.String()
			response := httptest.NewRecorder()
			handler(response, request)
			if response.Code != 200 || response.Header().Get("Location") != "" || request.URL.String() != before {
				t.Fatal(path, response.Code, response.Header(), request.URL)
			}
			if response.Header().Get("Cache-Control") != "no-cache" || response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatal(response.Header())
			}
			if method == "GET" && response.Body.String() != "<html>app</html>" || method == "HEAD" && response.Body.Len() != 0 {
				t.Fatal(method, response.Body.String())
			}
		}
	}
}

func TestAppShellMissingBuildReturnsError(t *testing.T) {
	response := httptest.NewRecorder()
	AppShell(fstest.MapFS{})(response, httptest.NewRequest("GET", "/runs", nil))
	if response.Code != 500 || response.Header().Get("Location") != "" {
		t.Fatal(response.Code, response.Header())
	}
}
