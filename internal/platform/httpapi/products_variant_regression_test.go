package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpyxn/varyaone/internal/products"
)

func TestUpdateVariantConfigRequiresIfMatchBeforeCallingService(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/products/product-id/variant-config", nil)
	productHandler{}.updateVariantConfig(response, request)
	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("expected 428 for missing If-Match, got %d", response.Code)
	}
	if response.Body.String() == "" {
		t.Fatal("expected a structured precondition error")
	}
}

func TestVariantStateConflictUsesConflictStatus(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/products/product-id", nil)
	writeModuleError(response, request, &products.VariantValidationError{
		Code: "VARIANT_STATE_CONFLICT", Message: "Varyant kimliği kilitli",
	}, "Varyant güncellenemedi.")
	if response.Code != http.StatusConflict {
		t.Fatalf("expected 409 for variant state conflict, got %d", response.Code)
	}
}

func TestVariantModeProductBarcodeGuardUsesConflictStatus(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/products/product-id/variant-config", nil)
	writeModuleError(response, request, &products.VariantValidationError{
		Code: "VARIANT_MODE_REQUIRES_EMPTY_PRODUCT_BARCODES", Message: "Ürün üzerindeki barkodlar kaldırılmalı",
	}, "Varyant ayarları kaydedilemedi.")
	if response.Code != http.StatusConflict {
		t.Fatalf("expected 409 for product barcode transition guard, got %d", response.Code)
	}
}

func TestUpdateVariantConfigRejectsInvalidIfMatchBeforeCallingService(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/products/product-id/variant-config", nil)
	request.Header.Set("If-Match", `"not-a-version"`)
	productHandler{}.updateVariantConfig(response, request)
	if response.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 412 for invalid If-Match, got %d", response.Code)
	}
}
