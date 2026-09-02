package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alpyxn/varyaone/internal/dashboard"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

type dashboardHandler struct{ service *dashboard.Service }

func mountDashboardRoutes(router chi.Router, identityService *identity.Service, service *dashboard.Service) {
	auth := identityHandler{service: identityService}
	handler := dashboardHandler{service: service}
	router.Route("/api/v1/dashboard", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/recent-activity", handler.recentActivity)
		r.Get("/shortcuts", handler.getShortcuts)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Put("/shortcuts", handler.saveShortcuts)
		})
	})
}

func (h dashboardHandler) recentActivity(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := h.service.RecentActivity(r.Context(), sessionFromRequest(r), limit)
	if err != nil {
		h.writeError(w, r, err, "Son işlemler okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h dashboardHandler) getShortcuts(w http.ResponseWriter, r *http.Request) {
	shortcuts, err := h.service.GetShortcuts(r.Context(), sessionFromRequest(r))
	if err != nil {
		h.writeError(w, r, err, "Kısayol tercihleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, shortcuts)
}

func (h dashboardHandler) saveShortcuts(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PinnedShortcuts []string `json:"pinned_shortcuts"`
	}
	if err := decodeJSON(r, &input); err != nil {
		h.writeError(w, r, fmt.Errorf("%w: kısayol bilgileri geçersiz", identity.ErrValidation), "Kısayol tercihleri kaydedilemedi.")
		return
	}
	shortcuts, err := h.service.SaveShortcuts(r.Context(), sessionFromRequest(r), input.PinnedShortcuts)
	if err != nil {
		h.writeError(w, r, err, "Kısayol tercihleri kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, shortcuts)
}

func (h dashboardHandler) writeError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		message := strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": ")
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", message)
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
	}
}
