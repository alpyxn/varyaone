package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/party"
	"github.com/alpyxn/varyaone/internal/platform/idempotency"
	"github.com/go-chi/chi/v5"
)

type partyOperationsHandler struct{ service *party.Service }

func mountPartyMovementRoutes(router chi.Router, identityService *identity.Service, service *party.Service) {
	auth := identityHandler{service: identityService}
	h := partyOperationsHandler{service: service}
	router.Route("/api/v1/party-movements", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", h.list)
		r.Get("/{movementID}", h.get)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/", h.post)
			r.Post("/manual", h.post)
		})
	})
	router.Route("/api/v1/parties/{partyID}/movements", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", h.listForParty)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/", h.postForParty)
		})
	})
	router.With(auth.requireSession).Get("/api/v1/party-movements", h.list)
	router.With(auth.requireSession, auth.requireCSRF).Post("/api/v1/party-movements", h.post)
	router.With(auth.requireSession).Get("/api/v1/parties/{partyID}/movements", h.listForParty)
	router.With(auth.requireSession, auth.requireCSRF).Post("/api/v1/parties/{partyID}/movements", h.postForParty)
}

func (h partyOperationsHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetLedgerEntry(r.Context(), sessionFromRequest(r), chi.URLParam(r, "movementID"))
	if err != nil {
		writePartyError(w, r, err, "Cari hareketi okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h partyOperationsHandler) list(w http.ResponseWriter, r *http.Request) {
	from, to, err := ledgerMovementDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Cari hareket tarih aralığı geçersiz.")
		return
	}
	result, err := h.service.ListLedgerEntries(r.Context(), sessionFromRequest(r), r.URL.Query().Get("party_id"), r.URL.Query().Get("currency"), r.URL.Query().Get("cursor"), queryLimit(r, 50, 200), from, to)
	if err != nil {
		writePartyError(w, r, err, "Cari hareketleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h partyOperationsHandler) listForParty(w http.ResponseWriter, r *http.Request) {
	from, to, err := ledgerMovementDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Cari hareket tarih aralığı geçersiz.")
		return
	}
	result, err := h.service.ListLedgerEntries(r.Context(), sessionFromRequest(r), chi.URLParam(r, "partyID"), r.URL.Query().Get("currency"), r.URL.Query().Get("cursor"), queryLimit(r, 50, 200), from, to)
	if err != nil {
		writePartyError(w, r, err, "Cari hareketleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func ledgerMovementDateRange(r *http.Request) (*time.Time, *time.Time, error) {
	parse := func(value string) (*time.Time, error) {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, nil
		}
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return nil, err
		}
		parsed = parsed.UTC()
		return &parsed, nil
	}
	from, err := parse(r.URL.Query().Get("from"))
	if err != nil {
		return nil, nil, err
	}
	to, err := parse(r.URL.Query().Get("to"))
	if err != nil {
		return nil, nil, err
	}
	if from != nil && to != nil && to.Before(*from) {
		return nil, nil, errors.New("tarih aralığı ters")
	}
	return from, to, nil
}

func (h partyOperationsHandler) post(w http.ResponseWriter, r *http.Request) {
	var input party.LedgerEntry
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Cari hareket bilgileri geçersiz.")
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	item, err := h.service.PostLedgerEntry(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writePartyOperationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h partyOperationsHandler) postForParty(w http.ResponseWriter, r *http.Request) {
	var input party.LedgerEntry
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Cari hareket bilgileri geçersiz.")
		return
	}
	input.PartyID = chi.URLParam(r, "partyID")
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	item, err := h.service.PostLedgerEntry(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writePartyOperationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

// Keep the error mapping local to the party operation surface so stable
// idempotency/concurrency errors do not become generic 500 responses.
func writePartyOperationError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, idempotency.ErrKeyRequired) {
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Bu işlem için Idempotency-Key gereklidir.")
		return
	}
	if errors.Is(err, idempotency.ErrPayloadConflict) {
		writeError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Aynı anahtar farklı cari hareket verisiyle kullanıldı.")
		return
	}
	if errors.Is(err, identity.ErrConflict) {
		writeError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Aynı anahtar farklı cari hareket verisiyle kullanıldı.")
		return
	}
	writePartyError(w, r, err, "Cari hareket işlemi tamamlanamadı.")
}
