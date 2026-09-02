package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/products"
	"github.com/go-chi/chi/v5"
)

type productHandler struct{ service *products.Service }

func mountProductRoutes(router chi.Router, identityService *identity.Service, service *products.Service) {
	auth := identityHandler{service: identityService}
	handler := productHandler{service: service}
	router.Route("/api/v1/products", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", handler.list)
		r.Get("/{productID}", handler.get)
		r.Get("/{productID}/variant-config", handler.getVariantConfig)
		r.Get("/{productID}/variants", handler.listVariants)
		r.Get("/barcode/{barcode}", handler.resolveBarcode)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/", handler.create)
			r.Put("/{productID}", handler.update)
			r.Post("/{productID}/deactivate", handler.deactivate)
			r.Put("/{productID}/variant-config", handler.updateVariantConfig)
			r.Post("/{productID}/variants/generate", handler.generateVariants)
			r.Post("/{productID}/variants", handler.createVariant)
			r.Put("/{productID}/variants/{variantID}/barcodes", handler.replaceVariantBarcodes)
			r.Put("/{productID}/variants/{variantID}", handler.updateVariant)
			r.Post("/{productID}/variants/{variantID}/deactivate", handler.deactivateVariant)
		})
	})
	router.Route("/api/v1/variant-definitions", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", handler.listVariantDefinitions)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/", handler.createVariantDefinition)
			r.Put("/{definitionID}", handler.updateVariantDefinition)
			r.Post("/{definitionID}/activate", handler.activateVariantDefinition)
			r.Post("/{definitionID}/deactivate", handler.deactivateVariantDefinition)
			r.Post("/{definitionID}/options", handler.createVariantOption)
			r.Put("/{definitionID}/options/{optionID}", handler.updateVariantOption)
			r.Post("/{definitionID}/options/{optionID}/activate", handler.activateVariantOption)
			r.Post("/{definitionID}/options/{optionID}/deactivate", handler.deactivateVariantOption)
		})
	})
	router.With(auth.requireSession).Get("/api/v1/variant-packages", handler.listVariantPackages)
	router.With(auth.requireSession).Get("/api/v1/products", handler.list)
	router.With(auth.requireSession, auth.requireCSRF).Post("/api/v1/products", handler.create)
	router.Route("/api/v1/product-references", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/units", handler.listUnits)
		r.Get("/categories", handler.listCategories)
		r.Get("/brands", handler.listBrands)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/categories", handler.createCategory)
			r.Post("/brands", handler.createBrand)
			r.Post("/categories/{referenceID}/activate", handler.activateCategory)
			r.Post("/categories/{referenceID}/deactivate", handler.deactivateCategory)
			r.Post("/brands/{referenceID}/activate", handler.activateBrand)
			r.Post("/brands/{referenceID}/deactivate", handler.deactivateBrand)
		})
	})
	router.Route("/api/v1/product-code-sequence", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", handler.getCodeSequence)
		r.With(auth.requireCSRF).Put("/", handler.saveCodeSequence)
	})
}

func (h productHandler) listVariantPackages(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListVariantPackages(r.Context())
	if err != nil {
		writeModuleError(w, r, err, "Varyant paketleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h productHandler) listVariantDefinitions(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListVariantDefinitions(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeModuleError(w, r, err, "Varyant tanımları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h productHandler) createVariantDefinition(w http.ResponseWriter, r *http.Request) {
	var input products.VariantDefinitionInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Varyant tanımı geçersiz.")
		return
	}
	item, err := h.service.CreateVariantDefinition(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Varyant tanımı oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h productHandler) updateVariantDefinition(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Tanım güncellemesi için sürüm gereklidir.")
		return
	}
	var input products.VariantDefinitionInput
	if err = decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Varyant tanımı geçersiz.")
		return
	}
	item, err := h.service.UpdateVariantDefinition(r.Context(), sessionFromRequest(r), chi.URLParam(r, "definitionID"), version, input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Varyant tanımı güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h productHandler) setVariantDefinitionActive(w http.ResponseWriter, r *http.Request, active bool) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Tanım işlemi için sürüm gereklidir.")
		return
	}
	item, err := h.service.SetVariantDefinitionActive(r.Context(), sessionFromRequest(r), chi.URLParam(r, "definitionID"), version, active, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Varyant tanımı güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h productHandler) activateVariantDefinition(w http.ResponseWriter, r *http.Request) {
	h.setVariantDefinitionActive(w, r, true)
}
func (h productHandler) deactivateVariantDefinition(w http.ResponseWriter, r *http.Request) {
	h.setVariantDefinitionActive(w, r, false)
}

func (h productHandler) createVariantOption(w http.ResponseWriter, r *http.Request) {
	var input products.VariantOptionInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Varyant seçeneği geçersiz.")
		return
	}
	item, err := h.service.CreateVariantOption(r.Context(), sessionFromRequest(r), chi.URLParam(r, "definitionID"), input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Varyant seçeneği oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (h productHandler) updateVariantOption(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Seçenek güncellemesi için sürüm gereklidir.")
		return
	}
	var input products.VariantOptionInput
	if err = decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Varyant seçeneği geçersiz.")
		return
	}
	item, err := h.service.UpdateVariantOption(r.Context(), sessionFromRequest(r), chi.URLParam(r, "definitionID"), chi.URLParam(r, "optionID"), version, input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Varyant seçeneği güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h productHandler) setVariantOptionActive(w http.ResponseWriter, r *http.Request, active bool) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Seçenek işlemi için sürüm gereklidir.")
		return
	}
	item, err := h.service.SetVariantOptionActive(r.Context(), sessionFromRequest(r), chi.URLParam(r, "definitionID"), chi.URLParam(r, "optionID"), version, active, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Varyant seçeneği güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h productHandler) activateVariantOption(w http.ResponseWriter, r *http.Request) {
	h.setVariantOptionActive(w, r, true)
}
func (h productHandler) deactivateVariantOption(w http.ResponseWriter, r *http.Request) {
	h.setVariantOptionActive(w, r, false)
}

func (h productHandler) getVariantConfig(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetVariantConfig(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"))
	if err != nil {
		writeModuleError(w, r, err, "Ürün varyant ayarları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (h productHandler) updateVariantConfig(w http.ResponseWriter, r *http.Request) {
	raw := r.Header.Get("If-Match")
	if strings.TrimSpace(raw) == "" {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Varyant ayarlarını kaydetmek için güncel sürüm gereklidir.")
		return
	}
	expectedVersion, err := parseIfMatch(raw)
	if err != nil {
		writeError(w, r, http.StatusPreconditionFailed, "IF_MATCH_INVALID", "Varyant ayarları sürümü geçersiz.")
		return
	}
	var input products.ProductVariantConfigInput
	if err = decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Ürün varyant ayarları geçersiz.")
		return
	}
	item, err := h.service.UpdateVariantConfig(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), input, expectedVersion, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Ürün varyant ayarları kaydedilemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h productHandler) generateVariants(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.GenerateVariants(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Varyant kombinasyonları oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h productHandler) listVariants(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListVariants(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"))
	if err != nil {
		writeModuleError(w, r, err, "Ürün varyantları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h productHandler) createVariant(w http.ResponseWriter, r *http.Request) {
	var input products.VariantInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Varyant bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateVariant(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Ürün varyantı oluşturulamadı.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusCreated, item)
}

func (h productHandler) updateVariant(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Varyant güncellemesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	var input products.VariantInput
	if err = decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Varyant bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateVariant(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), chi.URLParam(r, "variantID"), version, input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Ürün varyantı güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h productHandler) replaceVariantBarcodes(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Varyant barkodlarını değiştirmek için geçerli If-Match başlığı gereklidir.")
		return
	}
	var input products.VariantBarcodeReplacementInput
	if err = decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Varyant barkod listesi geçersiz.")
		return
	}
	item, err := h.service.ReplaceVariantBarcodes(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), chi.URLParam(r, "variantID"), version, input, requestMeta(r))
	if err != nil {
		if writeVariantBarcodeError(w, r, err) {
			return
		}
		writeModuleError(w, r, err, "Varyant barkodları değiştirilemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func writeVariantBarcodeError(w http.ResponseWriter, r *http.Request, err error) bool {
	code := products.ErrorCode(err)
	if code != "VARIANT_BARCODE_DUPLICATE" {
		return false
	}
	message := "Bu barkod firmada başka bir ürün veya varyantta kullanılıyor."
	var validationErr *products.VariantValidationError
	if errors.As(err, &validationErr) && strings.TrimSpace(validationErr.Message) != "" {
		message = validationErr.Message
	}
	writeError(w, r, http.StatusConflict, code, message)
	return true
}

func (h productHandler) deactivateVariant(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Varyant pasifleştirmesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	if err = h.service.DeactivateVariant(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), chi.URLParam(r, "variantID"), version, requestMeta(r)); err != nil {
		writeModuleError(w, r, err, "Ürün varyantı pasifleştirilemedi.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h productHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := h.service.List(r.Context(), sessionFromRequest(r), products.ListOptions{
		Scope: products.Scope{BranchID: r.URL.Query().Get("branch_id"), WarehouseID: r.URL.Query().Get("warehouse_id")},
		Query: r.URL.Query().Get("q"), Kind: r.URL.Query().Get("kind"), CategoryID: r.URL.Query().Get("category_id"), BrandID: r.URL.Query().Get("brand_id"), Cursor: r.URL.Query().Get("cursor"), Limit: limit,
		IncludeInactive: r.URL.Query().Get("include_inactive") == "true",
	})
	if err != nil {
		writeModuleError(w, r, err, "Stok kartları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h productHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Get(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), products.Scope{BranchID: r.URL.Query().Get("branch_id"), WarehouseID: r.URL.Query().Get("warehouse_id")})
	if err != nil {
		writeModuleError(w, r, err, "Stok kartı okunamadı.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h productHandler) resolveBarcode(w http.ResponseWriter, r *http.Request) {
	match, err := h.service.ResolveBarcode(r.Context(), sessionFromRequest(r), chi.URLParam(r, "barcode"))
	if errors.Is(err, products.ErrBarcodeNotFound) {
		writeError(w, r, http.StatusNotFound, "BARCODE_NOT_FOUND", "Barkod bu şirkette tanımlı değil.")
		return
	}
	if err != nil {
		writeModuleError(w, r, err, "Barkod çözümlenemedi.")
		return
	}
	writeJSON(w, http.StatusOK, match)
}

func (h productHandler) create(w http.ResponseWriter, r *http.Request) {
	var input products.Input
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Stok kartı bilgileri geçersiz.")
		return
	}
	item, err := h.service.Create(r.Context(), sessionFromRequest(r), input, products.Scope{BranchID: r.URL.Query().Get("branch_id"), WarehouseID: r.URL.Query().Get("warehouse_id")}, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Stok kartı oluşturulamadı.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusCreated, item)
}

func (h productHandler) update(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Stok kartı güncellemesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	var input products.Input
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Stok kartı bilgileri geçersiz.")
		return
	}
	item, err := h.service.Update(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), version, input, products.Scope{BranchID: r.URL.Query().Get("branch_id"), WarehouseID: r.URL.Query().Get("warehouse_id")}, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Stok kartı güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h productHandler) deactivate(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Stok kartı pasifleştirmesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	item, err := h.service.Deactivate(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), version, products.Scope{BranchID: r.URL.Query().Get("branch_id"), WarehouseID: r.URL.Query().Get("warehouse_id")}, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Stok kartı pasifleştirilemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h productHandler) listUnits(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListUnits(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeModuleError(w, r, err, "Birimler okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h productHandler) listCategories(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListCategories(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeModuleError(w, r, err, "Ürün kategorileri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h productHandler) listBrands(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListBrands(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeModuleError(w, r, err, "Ürün markaları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h productHandler) createCategory(w http.ResponseWriter, r *http.Request) {
	var input products.ReferenceInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Kategori bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateCategory(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Ürün kategorisi oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (h productHandler) createBrand(w http.ResponseWriter, r *http.Request) {
	var input products.ReferenceInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Marka bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateBrand(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Ürün markası oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (h productHandler) activateCategory(w http.ResponseWriter, r *http.Request) {
	h.setCategoryActive(w, r, true)
}
func (h productHandler) deactivateCategory(w http.ResponseWriter, r *http.Request) {
	h.setCategoryActive(w, r, false)
}
func (h productHandler) setCategoryActive(w http.ResponseWriter, r *http.Request, active bool) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Kategori sürümü gereklidir.")
		return
	}
	item, err := h.service.SetCategoryActive(r.Context(), sessionFromRequest(r), chi.URLParam(r, "referenceID"), version, active, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Kategori durumu güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h productHandler) activateBrand(w http.ResponseWriter, r *http.Request) {
	h.setBrandActive(w, r, true)
}
func (h productHandler) deactivateBrand(w http.ResponseWriter, r *http.Request) {
	h.setBrandActive(w, r, false)
}
func (h productHandler) setBrandActive(w http.ResponseWriter, r *http.Request, active bool) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Marka sürümü gereklidir.")
		return
	}
	item, err := h.service.SetBrandActive(r.Context(), sessionFromRequest(r), chi.URLParam(r, "referenceID"), version, active, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Marka durumu güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h productHandler) getCodeSequence(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetCodeSequence(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeModuleError(w, r, err, "Stok kodu ayarı okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (h productHandler) saveCodeSequence(w http.ResponseWriter, r *http.Request) {
	var input products.CodeSequence
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Stok kodu ayarı geçersiz.")
		return
	}
	item, err := h.service.SaveCodeSequence(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Stok kodu ayarı kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}
