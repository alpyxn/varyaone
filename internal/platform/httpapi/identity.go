package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

const sessionCookieName = "varyaone_session"
const csrfCookieName = "varyaone_csrf"

type identityHandler struct {
	service       *identity.Service
	secureCookies bool
}

type sessionContextKey struct{}

func mountIdentityRoutes(router chi.Router, service *identity.Service, secureCookies bool) {
	handler := identityHandler{service: service, secureCookies: secureCookies}
	router.Route("/api/v1", func(r chi.Router) {
		r.Get("/setup", handler.setupStatus)
		r.Post("/setup", handler.setup)
		r.Post("/auth/login", handler.login)
		r.Group(func(r chi.Router) {
			r.Use(handler.requireSession)
			r.Get("/session", handler.getSession)
			r.Get("/session/csrf", handler.rotateCSRF)
			r.Get("/api-tokens", handler.listAPITokens)
			r.Get("/permissions", handler.listPermissions)
			r.Get("/roles", handler.listRoles)
			r.Get("/users", handler.listMembers)
			r.Get("/company", handler.getCompany)
			r.Group(func(r chi.Router) {
				r.Use(handler.requireCSRF)
				r.Post("/auth/logout", handler.logout)
				r.Put("/session/company", handler.selectCompany)
				r.Post("/companies", handler.createCompany)
				r.Delete("/companies/{companyID}", handler.deleteCompany)
				r.Post("/security/totp/setup", handler.beginTOTP)
				r.Post("/security/totp/confirm", handler.confirmTOTP)
				r.Post("/security/totp/disable", handler.disableTOTP)
				r.Post("/api-tokens", handler.createAPIToken)
				r.Delete("/api-tokens/{tokenID}", handler.revokeAPIToken)
				r.Post("/roles", handler.createRole)
				r.Put("/roles/{roleID}", handler.updateRole)
				r.Post("/users", handler.addMember)
				r.Put("/company", handler.updateCompany)
			})
		})
	})
}

func (h identityHandler) setupStatus(w http.ResponseWriter, r *http.Request) {
	complete, err := h.service.SetupStatus(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Kurulum durumu okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"complete": complete})
}

func (h identityHandler) setup(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, r, http.StatusForbidden, "CSRF_REJECTED", "İstek kaynağı doğrulanamadı.")
		return
	}
	var input identity.SetupInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Kurulum bilgileri geçersiz.")
		return
	}
	session, err := h.service.Setup(r.Context(), input, requestMeta(r))
	if errors.Is(err, identity.ErrAlreadySetup) {
		writeError(w, r, http.StatusConflict, "SETUP_ALREADY_COMPLETE", "İlk kurulum daha önce tamamlanmış.")
		return
	}
	if err != nil {
		if errors.Is(err, identity.ErrValidation) {
			message := strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": ")
			writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", message)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İlk kurulum tamamlanamadı.")
		return
	}
	h.setSessionCookies(w, session.Token, session.CSRFToken)
	writeJSON(w, http.StatusCreated, session)
}

func (h identityHandler) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, r, http.StatusForbidden, "CSRF_REJECTED", "İstek kaynağı doğrulanamadı.")
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Giriş bilgileri geçersiz.")
		return
	}
	session, err := h.service.Login(r.Context(), input.Email, input.Password, input.TOTPCode, requestMeta(r))
	switch {
	case errors.Is(err, identity.ErrLoginLimited):
		retryAfter := 900
		var rateLimited *identity.LoginRateLimitedError
		if errors.As(err, &rateLimited) {
			retryAfter = int(rateLimited.RetryAfter.Seconds())
			if retryAfter < 1 {
				retryAfter = 1
			}
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		minutes := (retryAfter + 59) / 60
		writeError(w, r, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", fmt.Sprintf("Çok fazla başarısız deneme. Yaklaşık %d dakika sonra tekrar deneyin.", minutes))
	case errors.Is(err, identity.ErrTOTPRequired):
		writeError(w, r, http.StatusUnauthorized, "TOTP_REQUIRED", "İki adımlı doğrulama kodu gerekli.")
	case errors.Is(err, identity.ErrInvalidCredentials):
		writeError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "E-posta, parola veya doğrulama kodu hatalı.")
	case err != nil:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Giriş işlemi tamamlanamadı.")
	default:
		h.setSessionCookies(w, session.Token, session.CSRFToken)
		writeJSON(w, http.StatusOK, session)
	}
}

func (h identityHandler) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		var session identity.Session
		if err == nil {
			session, err = h.service.Authenticate(r.Context(), cookie.Value)
			session.Token = cookie.Value
		} else {
			authorization := strings.TrimSpace(r.Header.Get("Authorization"))
			if strings.HasPrefix(authorization, "Bearer ") {
				session, err = h.service.AuthenticateAPIToken(r.Context(), strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")))
			} else {
				err = identity.ErrUnauthenticated
			}
		}
		if err != nil {
			if cookie != nil {
				h.clearSessionCookie(w)
			}
			writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Oturum geçersiz veya süresi dolmuş.")
			return
		}
		if module := moduleForPath(r.URL.Path); !session.HasModule(module) {
			writeError(w, r, http.StatusForbidden, "MODULE_DISABLED", "Bu modül devre dışı bırakılmış.")
			return
		}
		ctx := contextWithSession(r, session)
		ctx, release := scopeRequestConnection(ctx, session.CurrentCompanyID)
		defer release()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h identityHandler) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := sessionFromRequest(r)
		if !sameOrigin(r) || !h.service.ValidateCSRF(r.Context(), session.ID, r.Header.Get("X-CSRF-Token")) {
			writeError(w, r, http.StatusForbidden, "CSRF_REJECTED", "Güvenlik doğrulaması başarısız.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h identityHandler) getSession(w http.ResponseWriter, r *http.Request) {
	session := sessionFromRequest(r)
	if cookie, err := r.Cookie(csrfCookieName); err == nil && h.service.ValidateCSRF(r.Context(), session.ID, cookie.Value) {
		session.CSRFToken = cookie.Value
	}
	writeJSON(w, http.StatusOK, session)
}

func (h identityHandler) rotateCSRF(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, r, http.StatusForbidden, "CSRF_REJECTED", "İstek kaynağı doğrulanamadı.")
		return
	}
	session := sessionFromRequest(r)
	token, err := h.service.RotateCSRF(r.Context(), session)
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Oturum geçersiz veya süresi dolmuş.")
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case err != nil:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Güvenlik belirteci yenilenemedi.")
	default:
		h.setCSRFCookie(w, token)
		writeJSON(w, http.StatusOK, map[string]string{"csrf_token": token})
	}
}

func (h identityHandler) selectCompany(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CompanyID string `json:"company_id"`
	}
	if err := decodeJSON(r, &input); err != nil || strings.TrimSpace(input.CompanyID) == "" {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Firma seçimi geçersiz.")
		return
	}
	session, err := h.service.SelectCompany(r.Context(), sessionFromRequest(r), input.CompanyID, requestMeta(r))
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, r, http.StatusForbidden, "COMPANY_ACCESS_DENIED", "Bu firmaya erişim yetkiniz yok.")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Firma seçilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h identityHandler) createCompany(w http.ResponseWriter, r *http.Request) {
	var input identity.CreateCompanyInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Firma bilgileri geçersiz.")
		return
	}
	session, err := h.service.CreateCompany(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, r, http.StatusForbidden, "COMPANY_CREATE_FORBIDDEN", "Yeni firma oluşturma yetkiniz yok.")
		return
	}
	if errors.Is(err, identity.ErrValidation) {
		message := strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": ")
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", message)
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Firma oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (h identityHandler) deleteCompany(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ConfirmName string `json:"confirm_name"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Firma silme isteği geçersiz.")
		return
	}
	session, err := h.service.DeleteCompany(r.Context(), sessionFromRequest(r), chi.URLParam(r, "companyID"), input.ConfirmName, requestMeta(r))
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, r, http.StatusForbidden, "COMPANY_DELETE_FORBIDDEN", "Firma silme yetkiniz yok.")
		return
	}
	if errors.Is(err, identity.ErrLastCompany) {
		writeError(w, r, http.StatusUnprocessableEntity, "COMPANY_LAST_REMAINING", "Erişiminizdeki son firma silinemez.")
		return
	}
	if errors.Is(err, identity.ErrValidation) {
		message := strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": ")
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", message)
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Firma silinemedi.")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h identityHandler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Logout(r.Context(), sessionFromRequest(r), requestMeta(r)); err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Oturum kapatılamadı.")
		return
	}
	h.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h identityHandler) beginTOTP(w http.ResponseWriter, r *http.Request) {
	secret, uri, err := h.service.BeginTOTP(r.Context(), sessionFromRequest(r), requestMeta(r))
	if err != nil {
		slog.Default().Error("begin totp failed", "trace_id", TraceID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İki adımlı doğrulama kurulumu başlatılamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauth_uri": uri})
}

func (h identityHandler) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Doğrulama kodu geçersiz.")
		return
	}
	codes, err := h.service.ConfirmTOTP(r.Context(), sessionFromRequest(r), input.Code, requestMeta(r))
	if errors.Is(err, identity.ErrInvalidCredentials) {
		writeError(w, r, http.StatusUnprocessableEntity, "INVALID_TOTP", "Doğrulama kodu geçersiz.")
		return
	}
	if err != nil {
		h.writeMutationError(w, r, err, "İki adımlı doğrulama etkinleştirilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (h identityHandler) disableTOTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Parola geçersiz.")
		return
	}
	err := h.service.DisableTOTP(r.Context(), sessionFromRequest(r), input.Password, requestMeta(r))
	if errors.Is(err, identity.ErrInvalidCredentials) {
		writeError(w, r, http.StatusUnprocessableEntity, "INVALID_CREDENTIALS", "Parola yanlış.")
		return
	}
	if err != nil {
		slog.Default().Error("disable totp failed", "trace_id", TraceID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İki adımlı doğrulama kapatılamadı.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h identityHandler) listAPITokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.service.ListAPITokens(r.Context(), sessionFromRequest(r))
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "API tokenları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": tokens})
}

func (h identityHandler) createAPIToken(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name      string     `json:"name"`
		Scopes    []string   `json:"scopes"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "API tokenı bilgileri geçersiz.")
		return
	}
	token, err := h.service.CreateAPIToken(r.Context(), sessionFromRequest(r), input.Name, input.Scopes, input.ExpiresAt, requestMeta(r))
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
		return
	}
	if err != nil {
		h.writeMutationError(w, r, err, "API tokenı oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, token)
}

func (h identityHandler) revokeAPIToken(w http.ResponseWriter, r *http.Request) {
	if err := h.service.RevokeAPIToken(r.Context(), sessionFromRequest(r), chi.URLParam(r, "tokenID"), requestMeta(r)); err != nil {
		if errors.Is(err, identity.ErrForbidden) {
			writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Token bulunamadı veya bu işlem için yetkiniz yok.")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "API tokenı iptal edilemedi.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h identityHandler) listPermissions(w http.ResponseWriter, r *http.Request) {
	permissions, err := h.service.ListPermissions(r.Context(), sessionFromRequest(r))
	if err != nil {
		h.writeAuthorizedError(w, r, err, "Yetkiler okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": permissions})
}

func (h identityHandler) listRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.service.ListRoles(r.Context(), sessionFromRequest(r))
	if err != nil {
		h.writeAuthorizedError(w, r, err, "Roller okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": roles})
}

func (h identityHandler) createRole(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Rol bilgileri geçersiz.")
		return
	}
	role, err := h.service.CreateRole(r.Context(), sessionFromRequest(r), input.Name, input.Permissions, requestMeta(r))
	if err != nil {
		h.writeMutationError(w, r, err, "Rol oluşturulamadı.")
		return
	}
	w.Header().Set("ETag", `"1"`)
	writeJSON(w, http.StatusCreated, role)
}

func (h identityHandler) updateRole(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Rol güncellemesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	var input struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Rol bilgileri geçersiz.")
		return
	}
	role, err := h.service.UpdateRole(r.Context(), sessionFromRequest(r), chi.URLParam(r, "roleID"), input.Name, input.Permissions, version, requestMeta(r))
	if errors.Is(err, identity.ErrConflict) {
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Rol başka bir kullanıcı tarafından değiştirilmiş.")
		return
	}
	if err != nil {
		h.writeMutationError(w, r, err, "Rol güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(role.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, role)
}

func (h identityHandler) listMembers(w http.ResponseWriter, r *http.Request) {
	members, err := h.service.ListMembers(r.Context(), sessionFromRequest(r))
	if err != nil {
		h.writeAuthorizedError(w, r, err, "Kullanıcılar okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": members})
}

func (h identityHandler) addMember(w http.ResponseWriter, r *http.Request) {
	var input identity.MemberInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Kullanıcı bilgileri geçersiz.")
		return
	}
	member, err := h.service.AddMember(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		h.writeMutationError(w, r, err, "Kullanıcı kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, member)
}

func (h identityHandler) getCompany(w http.ResponseWriter, r *http.Request) {
	company, err := h.service.CurrentCompany(r.Context(), sessionFromRequest(r))
	if err != nil {
		h.writeAuthorizedError(w, r, err, "Firma bilgileri okunamadı.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(company.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, company)
}

func (h identityHandler) updateCompany(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Firma güncellemesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	var input identity.CompanyInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Firma bilgileri geçersiz.")
		return
	}
	company, err := h.service.UpdateCompany(r.Context(), sessionFromRequest(r), input, version, requestMeta(r))
	if errors.Is(err, identity.ErrConflict) {
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Firma başka bir kullanıcı tarafından değiştirilmiş.")
		return
	}
	if err != nil {
		h.writeMutationError(w, r, err, "Firma güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(company.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, company)
}

func (h identityHandler) writeAuthorizedError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
		return
	}
	writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
}

func (h identityHandler) writeMutationError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
		return
	}
	// Domain validation messages are intentionally Turkish and contain no submitted secret.
	if errors.Is(err, identity.ErrValidation) {
		message := strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": ")
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", message)
		return
	}
	if errors.Is(err, identity.ErrBaseCurrencyLocked) {
		message := strings.TrimPrefix(err.Error(), identity.ErrBaseCurrencyLocked.Error()+": ")
		writeError(w, r, http.StatusConflict, "BASE_CURRENCY_LOCKED", message)
		return
	}
	writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
}

func parseIfMatch(value string) (int64, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "W/"))
	value = strings.Trim(value, `"`)
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version < 1 {
		return 0, errors.New("invalid If-Match")
	}
	return version, nil
}

func (h identityHandler) setSessionCookies(w http.ResponseWriter, token, csrfToken string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 60 * 60})
	h.setCSRFCookie(w, csrfToken)
}

func (h identityHandler) setCSRFCookie(w http.ResponseWriter, csrfToken string) {
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: csrfToken, Path: "/", HttpOnly: false, Secure: h.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 60 * 60})
}

func (h identityHandler) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: "", Path: "/", HttpOnly: false, Secure: h.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	host := r.Host
	return origin == "http://"+host || origin == "https://"+host
}

func requestMeta(r *http.Request) identity.RequestMeta {
	return identity.RequestMeta{TraceID: TraceID(r.Context()), IP: clientIP(r), UserAgent: r.UserAgent(), IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key"))}
}

// clientIP prefers X-Forwarded-For, set by the SvelteKit proxy that fronts
// every browser request, over RemoteAddr — which is otherwise always the
// proxy's own address and would put every visitor in the same IP-scoped
// login rate-limit bucket.
func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if first, _, ok := strings.Cut(forwarded, ","); ok {
			return strings.TrimSpace(first)
		}
		return forwarded
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func contextWithSession(r *http.Request, session identity.Session) context.Context {
	return context.WithValue(r.Context(), sessionContextKey{}, session)
}

func sessionFromRequest(r *http.Request) identity.Session {
	session, _ := r.Context().Value(sessionContextKey{}).(identity.Session)
	return session
}
