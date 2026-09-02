package spa

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerServesIndexAndAssets(t *testing.T) {
	h := Handler()

	// Deep link falls back to index.html.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cari/kartlar", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("deep link status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Fatalf("missing content-type on fallback")
	}

	// Root.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("root status = %d", rec.Code)
	}
}
