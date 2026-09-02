package httpapi

import (
	"errors"
	"net/http"

	"github.com/alpyxn/varyaone/internal/exchange"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

type exchangeHandler struct{ service *exchange.Service }

func mountExchangeRoutes(router chi.Router, identityService *identity.Service, service *exchange.Service) {
	auth := identityHandler{service: identityService}
	h := exchangeHandler{service: service}
	router.Route("/api/v1/exchange-rates", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", h.dashboard)
		r.With(auth.requireCSRF).Put("/settings", h.updateSettings)
		r.With(auth.requireCSRF).Post("/refresh", h.refresh)
	})
}

func (h exchangeHandler) dashboard(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.GetDashboard(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeExchangeError(w, r, err, "Döviz kurları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h exchangeHandler) updateSettings(w http.ResponseWriter, r *http.Request) {
	var input exchange.SettingsInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Kur ayarları geçersiz.")
		return
	}
	result, err := h.service.UpdateSettings(r.Context(), sessionFromRequest(r), input)
	if err != nil {
		writeExchangeError(w, r, err, "Kur ayarları kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h exchangeHandler) refresh(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.RefreshNow(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeExchangeError(w, r, err, "Döviz kurları güncellenemedi.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeExchangeError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok veya kayıt kapsamınız dışında.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Kur bilgileri geçersiz.")
	case errors.Is(err, exchange.ErrRateUnavailable):
		writeError(w, r, http.StatusServiceUnavailable, "EXCHANGE_RATE_UNAVAILABLE", "Güncel kur kaynağına ulaşılamadı; kayıtlı kur yoksa belge oluşturulamaz.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
	}
}
