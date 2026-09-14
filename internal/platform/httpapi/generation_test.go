package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDataGenerationHeaderChangesOnlyWhenBumped(t *testing.T) {
	handler := dataGenerationHeader(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	read := func() string {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/session", nil))
		return recorder.Header().Get(DataGenerationHeader)
	}

	first := read()
	if first == "" {
		t.Fatal("response carries no data generation")
	}
	if again := read(); again != first {
		t.Fatalf("generation changed without a restore: %q -> %q", first, again)
	}
	bumpDataGeneration()
	if after := read(); after == first {
		t.Fatalf("generation did not change after a restore: still %q", after)
	}
}
