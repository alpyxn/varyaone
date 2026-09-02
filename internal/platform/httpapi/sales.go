package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/sales"
	"github.com/go-chi/chi/v5"
)

func mountSalesRoutes(router chi.Router, identityService *identity.Service, service *sales.Service) {
	// Varya One exposes typed commercial aggregates. The former polymorphic
	// /documents and /invoices mounts are intentionally not compatibility
	// routes; callers must select the sales resource explicitly.
	mountTypedSalesRoutes(router, identityService, service)
}

func requireIdempotencyHeader(w http.ResponseWriter, r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) != "" {
		return true
	}
	writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Bu işlem için Idempotency-Key gereklidir.")
	return false
}

func parseRequiredIfMatch(r *http.Request) (int64, error) {
	value := strings.TrimSpace(r.Header.Get("If-Match"))
	if value == "" {
		return 0, errors.New("If-Match required")
	}
	return parseIfMatch(value)
}
