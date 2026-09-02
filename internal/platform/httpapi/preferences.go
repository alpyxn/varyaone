package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/preferences"
	"github.com/go-chi/chi/v5"
)

type preferenceHandler struct{ service *preferences.Service }

func mountPreferenceRoutes(router chi.Router, identityService *identity.Service, service *preferences.Service) {
	auth := identityHandler{service: identityService}
	handler := preferenceHandler{service: service}
	router.Route("/api/v1/preferences", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/tables/{tableKey}", handler.getTable)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Put("/tables/{tableKey}", handler.saveTable)
		})
	})
}

func (h preferenceHandler) getTable(w http.ResponseWriter, r *http.Request) {
	preference, err := h.service.GetTable(r.Context(), sessionFromRequest(r), chi.URLParam(r, "tableKey"))
	if err != nil {
		h.writeError(w, r, err, "Tablo görünüm tercihi okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, preference)
}

func (h preferenceHandler) saveTable(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ColumnVisibility map[string]bool `json:"column_visibility"`
	}
	if err := decodeJSON(r, &input); err != nil {
		h.writeError(w, r, fmt.Errorf("%w: görünüm tercihi bilgileri geçersiz", identity.ErrValidation), "Tablo görünüm tercihi kaydedilemedi.")
		return
	}
	preference, err := h.service.SaveTable(r.Context(), sessionFromRequest(r), chi.URLParam(r, "tableKey"), input.ColumnVisibility)
	if err != nil {
		h.writeError(w, r, err, "Tablo görünüm tercihi kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, preference)
}

func (h preferenceHandler) writeError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu görünüm tercihi için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		message := strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": ")
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", message)
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
	}
}
