package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alpyxn/varyaone/internal/fixedasset"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

type fixedAssetHandler struct{ service *fixedasset.Service }

func mountFixedAssetRoutes(router chi.Router, identityService *identity.Service, service *fixedasset.Service) {
	auth := identityHandler{service: identityService}
	h := fixedAssetHandler{service: service}
	read := router.With(auth.requireSession)
	read.Get("/api/v1/fixed-assets", h.list)
	read.Get("/api/v1/fixed-assets/{assetID}", h.get)
	read.Get("/api/v1/fixed-assets/{assetID}/assignments", h.listAssignments)
	read.Get("/api/v1/hr/employees/{employeeID}/asset-assignments", h.listByEmployee)
	read.Get("/api/v1/fixed-asset-categories", h.listCategories)
	write := router.With(auth.requireSession, auth.requireCSRF)
	write.Post("/api/v1/fixed-asset-categories", h.createCategory)
	write.Patch("/api/v1/fixed-asset-categories/{categoryID}", h.updateCategory)
	write.Post("/api/v1/fixed-asset-categories/{categoryID}/status", h.setCategoryStatus)
	write.Post("/api/v1/fixed-assets", h.create)
	write.Patch("/api/v1/fixed-assets/{assetID}", h.update)
	write.Post("/api/v1/fixed-assets/{assetID}/assignments", h.assign)
	write.Post("/api/v1/fixed-assets/{assetID}/assignments/{assignmentID}/return", h.returnAsset)
}

func (h fixedAssetHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := h.service.List(r.Context(), sessionFromRequest(r),
		r.URL.Query().Get("q"), r.URL.Query().Get("status"), r.URL.Query().Get("category"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeFixedAssetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h fixedAssetHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Get(r.Context(), sessionFromRequest(r), chi.URLParam(r, "assetID"))
	if err != nil {
		writeFixedAssetError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h fixedAssetHandler) create(w http.ResponseWriter, r *http.Request) {
	var input fixedasset.Input
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Sabit kıymet bilgileri geçersiz.")
		return
	}
	item, err := h.service.Create(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeFixedAssetError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusCreated, item)
}

func (h fixedAssetHandler) update(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Sabit kıymet güncellemesi için If-Match gereklidir.")
		return
	}
	var input fixedasset.Input
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Sabit kıymet bilgileri geçersiz.")
		return
	}
	item, err := h.service.Update(r.Context(), sessionFromRequest(r), chi.URLParam(r, "assetID"), version, input, requestMeta(r))
	if err != nil {
		writeFixedAssetError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h fixedAssetHandler) assign(w http.ResponseWriter, r *http.Request) {
	var input fixedasset.AssignInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Zimmet bilgileri geçersiz.")
		return
	}
	item, err := h.service.Assign(r.Context(), sessionFromRequest(r), chi.URLParam(r, "assetID"), input, requestMeta(r))
	if err != nil {
		writeFixedAssetError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h fixedAssetHandler) returnAsset(w http.ResponseWriter, r *http.Request) {
	var input fixedasset.ReturnInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İade bilgileri geçersiz.")
		return
	}
	item, err := h.service.Return(r.Context(), sessionFromRequest(r), chi.URLParam(r, "assetID"), chi.URLParam(r, "assignmentID"), input, requestMeta(r))
	if err != nil {
		writeFixedAssetError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h fixedAssetHandler) listAssignments(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListAssignments(r.Context(), sessionFromRequest(r), chi.URLParam(r, "assetID"), "", limit)
	if err != nil {
		writeFixedAssetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h fixedAssetHandler) listByEmployee(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListAssignments(r.Context(), sessionFromRequest(r), "", chi.URLParam(r, "employeeID"), limit)
	if err != nil {
		writeFixedAssetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h fixedAssetHandler) listCategories(w http.ResponseWriter, r *http.Request) {
	includeArchived := r.URL.Query().Get("include_archived") == "true"
	items, err := h.service.ListCategories(r.Context(), sessionFromRequest(r), includeArchived)
	if err != nil {
		writeFixedAssetError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h fixedAssetHandler) createCategory(w http.ResponseWriter, r *http.Request) {
	var input fixedasset.CategoryInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Kategori bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateCategory(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeFixedAssetError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusCreated, item)
}

func (h fixedAssetHandler) updateCategory(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Kategori güncellemesi için If-Match gereklidir.")
		return
	}
	var input fixedasset.CategoryInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Kategori bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateCategory(r.Context(), sessionFromRequest(r), chi.URLParam(r, "categoryID"), version, input, requestMeta(r))
	if err != nil {
		writeFixedAssetError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h fixedAssetHandler) setCategoryStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Active bool `json:"active"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Durum bilgisi geçersiz.")
		return
	}
	item, err := h.service.SetCategoryActive(r.Context(), sessionFromRequest(r), chi.URLParam(r, "categoryID"), body.Active, requestMeta(r))
	if err != nil {
		writeFixedAssetError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func writeFixedAssetError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, identity.ErrConflict):
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Sabit kıymet kaydı başka bir kullanıcı tarafından değiştirildi.")
	case errors.Is(err, fixedasset.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "FIXED_ASSET_NOT_FOUND", "Sabit kıymet bulunamadı.")
	case errors.Is(err, fixedasset.ErrCategoryNotFound):
		writeError(w, r, http.StatusNotFound, "FIXED_ASSET_CATEGORY_NOT_FOUND", "Sabit kıymet kategorisi bulunamadı.")
	case errors.Is(err, fixedasset.ErrAssignNotFound):
		writeError(w, r, http.StatusNotFound, "FIXED_ASSET_ASSIGNMENT_NOT_FOUND", "Zimmet kaydı bulunamadı.")
	case errors.Is(err, fixedasset.ErrEmployeeGone):
		writeError(w, r, http.StatusUnprocessableEntity, "FIXED_ASSET_EMPLOYEE_NOT_FOUND", "Çalışan bulunamadı.")
	case errors.Is(err, fixedasset.ErrAssetUnavailable):
		writeError(w, r, http.StatusConflict, "FIXED_ASSET_NOT_AVAILABLE", "Sabit kıymet zimmetlemeye uygun değil.")
	case errors.Is(err, fixedasset.ErrActiveAssignmentExists):
		writeError(w, r, http.StatusConflict, "FIXED_ASSET_ALREADY_ASSIGNED", "Sabit kıymetin açık bir zimmeti zaten var.")
	case errors.Is(err, fixedasset.ErrAssignmentReturned):
		writeError(w, r, http.StatusConflict, "FIXED_ASSET_ASSIGNMENT_RETURNED", "Bu zimmet zaten iade edilmiş ve değiştirilemez.")
	case errors.Is(err, fixedasset.ErrReturnBeforeAssignment):
		writeError(w, r, http.StatusUnprocessableEntity, "FIXED_ASSET_INVALID_RETURN_DATE", "İade tarihi zimmet tarihinden önce olamaz.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Sabit kıymet işlemi tamamlanamadı.")
	}
}
