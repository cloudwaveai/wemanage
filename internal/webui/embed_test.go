package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIndexIsRevalidated(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/", "/accounts"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: status %d cache-control %q", path, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
}
