package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/update"
	"github.com/go-chi/chi/v5"
)

const updatePermission = "system.update.manage"

type updateHandler struct {
	service    *update.Service
	agentToken string
}

func mountSystemUpdateRoutes(router chi.Router, identityService *identity.Service, service *update.Service, agentToken string) {
	auth := identityHandler{service: identityService}
	handler := updateHandler{service: service, agentToken: strings.TrimSpace(agentToken)}

	// Operator-facing: guarded by session + permission, proxied from the SPA.
	router.Route("/api/v1/system/update", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Use(handler.requirePermission)
		r.Get("/", handler.status)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/check", handler.check)
			r.Post("/apply", handler.apply)
			r.Post("/snooze", handler.snooze)
			r.Post("/ack", handler.ack)
		})
	})

	// Agent-facing: no session, a shared bearer token only. Reached directly by
	// the host systemd agent on 127.0.0.1, never through the SPA proxy.
	if handler.agentToken != "" {
		router.Route("/internal/update", func(r chi.Router) {
			r.Use(handler.requireAgentToken)
			r.Get("/next", handler.agentNext)
			r.Post("/progress", handler.agentProgress)
			r.Post("/result", handler.agentResult)
		})
	}
}

func (h updateHandler) requirePermission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sessionFromRequest(r).HasPermission(updatePermission) {
			writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h updateHandler) requireAgentToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if presented == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(h.agentToken)) != 1 {
			writeError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Geçersiz güncelleme aracı belirteci.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h updateHandler) status(w http.ResponseWriter, r *http.Request) {
	st, err := h.service.Status(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Güncelleme durumu okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// check contacts the release catalog now and answers with the refreshed status,
// so the settings screen's button reports what the catalog actually says
// instead of redrawing a status the worker last refreshed hours ago.
func (h updateHandler) check(w http.ResponseWriter, r *http.Request) {
	switch err := h.service.CheckNow(r.Context()); {
	case errors.Is(err, update.ErrNotConfigured):
		writeError(w, r, http.StatusConflict, "UPDATE_NOT_CONFIGURED", "Bu kurulumda güncelleme kontrolü kapalı.")
		return
	case errors.Is(err, update.ErrCheckTooSoon):
		w.Header().Set("Retry-After", "60")
		writeError(w, r, http.StatusTooManyRequests, "UPDATE_CHECK_TOO_SOON", "Az önce kontrol edildi, biraz sonra tekrar deneyin.")
		return
	case err != nil:
		// The check recorded its attempt; only the fetch failed. Say so rather
		// than letting the screen imply there is no update.
		writeError(w, r, http.StatusBadGateway, "UPDATE_CHECK_FAILED", "Sürüm kataloğuna ulaşılamadı.")
		return
	}
	h.status(w, r)
}

func (h updateHandler) apply(w http.ResponseWriter, r *http.Request) {
	switch err := h.service.RequestApply(r.Context()); {
	case errors.Is(err, update.ErrNotAvailable):
		writeError(w, r, http.StatusConflict, "NO_UPDATE", "Yüklenecek daha yeni bir sürüm yok.")
	case errors.Is(err, update.ErrBusy):
		writeError(w, r, http.StatusConflict, "UPDATE_BUSY", "Bir güncelleme zaten sürüyor.")
	case err != nil:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Güncelleme başlatılamadı.")
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	}
}

func (h updateHandler) snooze(w http.ResponseWriter, r *http.Request) {
	switch err := h.service.Snooze(r.Context()); {
	case errors.Is(err, update.ErrMandatory):
		writeError(w, r, http.StatusConflict, "UPDATE_MANDATORY", "Bu güncelleme zorunlu, ertelenemez.")
	case err != nil:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Erteleme kaydedilemedi.")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func (h updateHandler) ack(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Ack(r.Context()); err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Onay kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h updateHandler) agentNext(w http.ResponseWriter, r *http.Request) {
	action, err := h.service.NextAction(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Sıradaki işlem okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, action)
}

func (h updateHandler) agentProgress(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Phase   string `json:"phase"`
		Message string `json:"message"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Geçersiz istek.")
		return
	}
	if err := h.service.RecordProgress(r.Context(), input.Phase, input.Message); err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "İlerleme kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h updateHandler) agentResult(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		RolledBack  bool   `json:"rolled_back"`
		FromVersion string `json:"from_version"`
		ToVersion   string `json:"to_version"`
		LogTail     string `json:"log_tail"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Geçersiz istek.")
		return
	}
	if err := h.service.RecordResult(r.Context(), update.ResultInput{
		OK:          input.OK,
		Error:       input.Error,
		RolledBack:  input.RolledBack,
		FromVersion: input.FromVersion,
		ToVersion:   input.ToVersion,
		LogTail:     input.LogTail,
	}); err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Sonuç kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
