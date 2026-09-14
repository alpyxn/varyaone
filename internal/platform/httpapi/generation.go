package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync/atomic"
)

// DataGenerationHeader names the data the server is currently serving.
//
// A restore replaces the whole database under pages that are already open.
// Those pages keep the session and company they loaded at start, so every list
// they ask for afterwards comes back empty until someone reloads them — on the
// machine that ran the restore and on every other client on the network. The
// value changes when a restore changes the data (and when the server starts),
// so an open page that sees a different value knows to load itself again.
const DataGenerationHeader = "X-Varya-Generation"

var dataGeneration atomic.Pointer[string]

func init() { bumpDataGeneration() }

// bumpDataGeneration marks the data as replaced.
func bumpDataGeneration() {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	value := hex.EncodeToString(raw[:])
	dataGeneration.Store(&value)
}

func currentDataGeneration() string { return *dataGeneration.Load() }

// dataGenerationHeader stamps every response with the current generation.
func dataGenerationHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(DataGenerationHeader, currentDataGeneration())
		next.ServeHTTP(w, r)
	})
}
