package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/party"
	"github.com/go-chi/chi/v5"
)

type partyHandler struct{ service *party.Service }

func mountPartyRoutes(router chi.Router, identityService *identity.Service, service *party.Service) {
	auth := identityHandler{service: identityService}
	handler := partyHandler{service: service}
	router.Route("/api/v1/parties", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", handler.list)
		r.Get("/{partyID}", handler.get)
		r.Get("/{partyID}/balances", handler.balances)
		r.Get("/{partyID}/statement", handler.statement)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/", handler.create)
			r.Put("/{partyID}", handler.update)
			r.Post("/{partyID}/deactivate", handler.deactivate)
			r.Post("/{partyID}/activate", handler.activate)
		})
	})
	router.Route("/api/v1/party-settings", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/payment-terms", handler.listPaymentTerms)
		r.Get("/groups", handler.listGroups)
		r.Get("/custom-fields", handler.listCustomFields)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/payment-terms", handler.createPaymentTerm)
			r.Post("/groups", handler.createGroup)
			r.Put("/groups/{groupID}", handler.updateGroup)
			r.Post("/groups/{groupID}/deactivate", handler.deactivateGroup)
			r.Post("/groups/{groupID}/activate", handler.activateGroup)
			r.Post("/custom-fields", handler.createCustomField)
		})
	})
	router.Route("/api/v1/address-references", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/provinces", handler.listTurkishProvinces)
		r.Get("/provinces/{provinceID}/districts", handler.listTurkishDistricts)
		r.Get("/districts/{districtID}/neighborhoods", handler.listTurkishNeighborhoods)
	})
	router.Route("/api/v1/tax-office-references", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", handler.listTaxOfficeReferences)
	})
	router.Route("/api/v1/address-preferences", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/default", handler.getAddressPreference)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Put("/default", handler.saveAddressPreference)
		})
	})
}

func (h partyHandler) listTurkishProvinces(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListTurkishProvinces(r.Context(), sessionFromRequest(r))
	if err != nil {
		writePartyError(w, r, err, "İller okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h partyHandler) listTurkishDistricts(w http.ResponseWriter, r *http.Request) {
	provinceID, err := strconv.ParseInt(chi.URLParam(r, "provinceID"), 10, 64)
	if err != nil {
		writePartyError(w, r, fmt.Errorf("%w: il kimliği geçersiz", identity.ErrValidation), "İlçeler okunamadı.")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListTurkishDistricts(r.Context(), sessionFromRequest(r), provinceID, r.URL.Query().Get("q"), limit)
	if err != nil {
		writePartyError(w, r, err, "İlçeler okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h partyHandler) listTurkishNeighborhoods(w http.ResponseWriter, r *http.Request) {
	districtID, err := strconv.ParseInt(chi.URLParam(r, "districtID"), 10, 64)
	if err != nil {
		writePartyError(w, r, fmt.Errorf("%w: ilçe kimliği geçersiz", identity.ErrValidation), "Mahalleler okunamadı.")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListTurkishNeighborhoods(r.Context(), sessionFromRequest(r), districtID, r.URL.Query().Get("q"), limit)
	if err != nil {
		writePartyError(w, r, err, "Mahalleler okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h partyHandler) listTaxOfficeReferences(w http.ResponseWriter, r *http.Request) {
	provinceID := int64(0)
	if value := strings.TrimSpace(r.URL.Query().Get("province_id")); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			writePartyError(w, r, fmt.Errorf("%w: il kimliği geçersiz", identity.ErrValidation), "Vergi daireleri okunamadı.")
			return
		}
		provinceID = parsed
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListTaxOfficeReferences(r.Context(), sessionFromRequest(r), provinceID, r.URL.Query().Get("district_name"), r.URL.Query().Get("q"), limit)
	if err != nil {
		writePartyError(w, r, err, "Vergi daireleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h partyHandler) getAddressPreference(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetAddressPreference(r.Context(), sessionFromRequest(r))
	if err != nil {
		writePartyError(w, r, err, "Adres varsayılanı okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h partyHandler) saveAddressPreference(w http.ResponseWriter, r *http.Request) {
	var input party.AddressPreference
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Adres varsayılanı bilgileri geçersiz.")
		return
	}
	item, err := h.service.SaveAddressPreference(r.Context(), sessionFromRequest(r), input)
	if err != nil {
		writePartyError(w, r, err, "Adres varsayılanı kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h partyHandler) listPaymentTerms(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListPaymentTerms(r.Context(), sessionFromRequest(r))
	if err != nil {
		writePartyError(w, r, err, "Vade seçenekleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h partyHandler) createPaymentTerm(w http.ResponseWriter, r *http.Request) {
	var input party.PaymentTerm
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Vade bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreatePaymentTerm(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writePartyError(w, r, err, "Vade oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (h partyHandler) listGroups(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListGroups(r.Context(), sessionFromRequest(r))
	if err != nil {
		writePartyError(w, r, err, "Cari grupları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h partyHandler) createGroup(w http.ResponseWriter, r *http.Request) {
	var input party.Group
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Grup bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateGroup(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writePartyError(w, r, err, "Cari grubu oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (h partyHandler) updateGroup(w http.ResponseWriter, r *http.Request) {
	var input party.Group
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Cari grubu bilgileri geçersiz.")
		return
	}
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Cari grubu güncellemesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	item, err := h.service.UpdateGroup(r.Context(), sessionFromRequest(r), chi.URLParam(r, "groupID"), version, input, requestMeta(r))
	if err != nil {
		writePartyError(w, r, err, "Cari grubu güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h partyHandler) deactivateGroup(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Cari grubu pasifleştirmesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	item, err := h.service.DeactivateGroup(r.Context(), sessionFromRequest(r), chi.URLParam(r, "groupID"), version, requestMeta(r))
	if err != nil {
		writePartyError(w, r, err, "Cari grubu pasifleştirilemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h partyHandler) activateGroup(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Cari grubu aktifleştirmesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	item, err := h.service.ActivateGroup(r.Context(), sessionFromRequest(r), chi.URLParam(r, "groupID"), version, requestMeta(r))
	if err != nil {
		writePartyError(w, r, err, "Cari grubu aktifleştirilemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h partyHandler) listCustomFields(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListCustomFields(r.Context(), sessionFromRequest(r))
	if err != nil {
		writePartyError(w, r, err, "Özel alanlar okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h partyHandler) createCustomField(w http.ResponseWriter, r *http.Request) {
	var input party.CustomFieldDefinition
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Özel alan bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateCustomField(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writePartyError(w, r, err, "Özel alan oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h partyHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := h.service.List(r.Context(), sessionFromRequest(r), r.URL.Query().Get("q"), r.URL.Query().Get("cursor"), limit, r.URL.Query().Get("include_inactive") == "true", r.URL.Query().Get("role"))
	if err != nil {
		writePartyError(w, r, err, "Cari listesi okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h partyHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Get(r.Context(), sessionFromRequest(r), chi.URLParam(r, "partyID"))
	if err != nil {
		writePartyError(w, r, err, "Cari kartı okunamadı.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h partyHandler) create(w http.ResponseWriter, r *http.Request) {
	var input party.Input
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Cari bilgileri geçersiz.")
		return
	}
	item, err := h.service.Create(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writePartyError(w, r, err, "Cari kartı oluşturulamadı.")
		return
	}
	w.Header().Set("ETag", `"1"`)
	writeJSON(w, http.StatusCreated, item)
}

func (h partyHandler) update(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Cari güncellemesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	var input party.Input
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Cari bilgileri geçersiz.")
		return
	}
	item, err := h.service.Update(r.Context(), sessionFromRequest(r), chi.URLParam(r, "partyID"), version, input, requestMeta(r))
	if err != nil {
		writePartyError(w, r, err, "Cari kartı güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h partyHandler) deactivate(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Cari pasifleştirmesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	item, err := h.service.Deactivate(r.Context(), sessionFromRequest(r), chi.URLParam(r, "partyID"), version, requestMeta(r))
	if err != nil {
		writePartyError(w, r, err, "Cari pasifleştirilemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h partyHandler) activate(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Cari kartı aktifleştirmesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	item, err := h.service.Activate(r.Context(), sessionFromRequest(r), chi.URLParam(r, "partyID"), version, requestMeta(r))
	if err != nil {
		writePartyError(w, r, err, "Cari kartı aktifleştirilemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h partyHandler) balances(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.BalancesResult(r.Context(), sessionFromRequest(r), chi.URLParam(r, "partyID"))
	if err != nil {
		writePartyError(w, r, err, "Cari bakiyesi okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h partyHandler) statement(w http.ResponseWriter, r *http.Request) {
	from := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Now().AddDate(1, 0, 0)
	var err error
	if value := r.URL.Query().Get("from"); value != "" {
		from, err = time.Parse("2006-01-02", value)
	}
	if err == nil {
		if value := r.URL.Query().Get("to"); value != "" {
			to, err = time.Parse("2006-01-02", value)
		}
	}
	if err != nil || to.Before(from) {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Ekstre tarih aralığı geçersiz.")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	report, err := h.service.StatementReportPage(r.Context(), sessionFromRequest(r), chi.URLParam(r, "partyID"), from, to, r.URL.Query().Get("currency"), r.URL.Query().Get("cursor"), r.URL.Query().Get("order"), limit)
	if err != nil {
		writePartyError(w, r, err, "Cari ekstresi okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func writePartyError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Cari kaydı bulunamadı veya bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrConflict):
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Cari başka bir kullanıcı tarafından değiştirilmiş.")
	case errors.Is(err, identity.ErrValidation):
		message := strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": ")
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", message)
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
	}
}
