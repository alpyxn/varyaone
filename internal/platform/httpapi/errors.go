package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alpyxn/varyaone/internal/platform/httpapi/contract"
	"github.com/jackc/pgx/v5/pgconn"
)

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, contract.ErrorResponse{Code: code, Message: message, Details: map[string]any{}, TraceId: TraceID(r.Context())})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeConcurrencyConflictIfSerializationFailure turns a PostgreSQL
// serialization failure (40001) or deadlock (40P01) — the way two concurrent
// draft->finalize commands racing over the same source line surface — into a
// clean 409 the client can retry, instead of a generic 500. Returns true when
// it handled the error.
func writeConcurrencyConflictIfSerializationFailure(w http.ResponseWriter, r *http.Request, err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
		writeError(w, r, http.StatusConflict, "CONCURRENCY_CONFLICT", "Belge başka bir işlemle aynı anda değiştirildi. Lütfen tekrar deneyin.")
		return true
	}
	return false
}
