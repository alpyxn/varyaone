package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alpyxn/varyaone/internal/agenda"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

type agendaHandler struct{ service *agenda.Service }

func mountAgendaRoutes(router chi.Router, identityService *identity.Service, service *agenda.Service) {
	auth := identityHandler{service: identityService}
	handler := agendaHandler{service: service}
	router.Route("/api/v1/agenda", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/events", handler.list)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/events", handler.create)
			r.Delete("/events/{id}", handler.remove)
			r.Post("/events/notified", handler.markNotified)
			r.Post("/events/{id}/complete", handler.setCompleted)
		})
	})
}

func (h agendaHandler) list(w http.ResponseWriter, r *http.Request) {
	events, err := h.service.List(r.Context(), sessionFromRequest(r))
	if err != nil {
		h.writeError(w, r, err, "Takvim etkinlikleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (h agendaHandler) create(w http.ResponseWriter, r *http.Request) {
	var input agenda.Input
	if err := decodeJSON(r, &input); err != nil {
		h.writeError(w, r, fmt.Errorf("%w: etkinlik bilgileri geçersiz", identity.ErrValidation), "Etkinlik kaydedilemedi.")
		return
	}
	event, err := h.service.Create(r.Context(), sessionFromRequest(r), input)
	if err != nil {
		h.writeError(w, r, err, "Etkinlik kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (h agendaHandler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Delete(r.Context(), sessionFromRequest(r), chi.URLParam(r, "id")); err != nil {
		h.writeError(w, r, err, "Etkinlik silinemedi.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h agendaHandler) setCompleted(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Completed bool `json:"completed"`
	}
	if err := decodeJSON(r, &input); err != nil {
		h.writeError(w, r, fmt.Errorf("%w: durum bilgisi geçersiz", identity.ErrValidation), "Etkinlik güncellenemedi.")
		return
	}
	event, err := h.service.SetCompleted(r.Context(), sessionFromRequest(r), chi.URLParam(r, "id"), input.Completed)
	if err != nil {
		h.writeError(w, r, err, "Etkinlik güncellenemedi.")
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (h agendaHandler) markNotified(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(r, &input); err != nil {
		h.writeError(w, r, fmt.Errorf("%w: kimlik listesi geçersiz", identity.ErrValidation), "Etkinlik güncellenemedi.")
		return
	}
	if err := h.service.MarkNotified(r.Context(), sessionFromRequest(r), input.IDs); err != nil {
		h.writeError(w, r, err, "Etkinlik güncellenemedi.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h agendaHandler) writeError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
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
