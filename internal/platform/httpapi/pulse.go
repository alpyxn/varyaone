package httpapi

import (
	"net/http"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/pulse"
	"github.com/go-chi/chi/v5"
)

const (
	maxFeedbackMessage = 4000
	maxFeedbackContact = 200
)

type pulseHandler struct{ service *pulse.Service }

func mountPulseRoutes(router chi.Router, identityService *identity.Service, service *pulse.Service) {
	auth := identityHandler{service: identityService}
	handler := pulseHandler{service: service}
	router.Route("/api/v1/pulse", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/feedback", handler.feedback)
		})
	})
}

func (h pulseHandler) feedback(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Category string `json:"category"`
		Message  string `json:"message"`
		Contact  string `json:"contact"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Geri bildirim verisi geçersiz.")
		return
	}

	category := strings.ToLower(strings.TrimSpace(input.Category))
	if category != "bug" && category != "idea" {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Tür 'bug' veya 'idea' olmalı.")
		return
	}
	message := strings.TrimSpace(input.Message)
	if message == "" {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Mesaj boş olamaz.")
		return
	}
	if len(message) > maxFeedbackMessage {
		message = message[:maxFeedbackMessage]
	}
	contact := strings.TrimSpace(input.Contact)
	if len(contact) > maxFeedbackContact {
		contact = contact[:maxFeedbackContact]
	}

	err := h.service.SendFeedback(r.Context(), pulse.FeedbackInput{
		Category:  category,
		Message:   message,
		Contact:   contact,
		CompanyID: sessionFromRequest(r).CurrentCompanyID,
	})
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "PULSE_UNAVAILABLE", "Geri bildirim şu an gönderilemedi. Lütfen sonra tekrar deneyin.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}
