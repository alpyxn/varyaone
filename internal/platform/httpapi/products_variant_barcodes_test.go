package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpyxn/varyaone/internal/products"
)

func TestReplaceVariantBarcodesRequiresIfMatch(t *testing.T) {
	request := httptest.NewRequest(http.MethodPut, "/api/v1/products/product-id/variants/variant-id/barcodes", strings.NewReader(`{"barcodes":[]}`))
	response := httptest.NewRecorder()

	(productHandler{}).replaceVariantBarcodes(response, request)

	if response.Code != http.StatusPreconditionRequired || !strings.Contains(response.Body.String(), "IF_MATCH_REQUIRED") {
		t.Fatalf("expected structured missing If-Match response, got %d: %s", response.Code, response.Body.String())
	}
}

func TestReplaceVariantBarcodesRejectsMalformedPayloadBeforeService(t *testing.T) {
	request := httptest.NewRequest(http.MethodPut, "/api/v1/products/product-id/variants/variant-id/barcodes", strings.NewReader(`{"unexpected":true}`))
	request.Header.Set("If-Match", `"1"`)
	response := httptest.NewRecorder()

	(productHandler{}).replaceVariantBarcodes(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "VALIDATION_ERROR") {
		t.Fatalf("expected malformed payload response, got %d: %s", response.Code, response.Body.String())
	}
}

func TestVariantBarcodeDuplicateIsConflict(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/products/product-id/variants/variant-id/barcodes", nil)
	handled := writeVariantBarcodeError(response, request, &products.VariantValidationError{
		Code: "VARIANT_BARCODE_DUPLICATE", Message: `Barkod "TAKEN-1" başka bir ürün veya varyantta kullanılıyor. Şirket içinde kullanılmamış bir barkod girin.`,
	})

	if !handled || response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "VARIANT_BARCODE_DUPLICATE") || !strings.Contains(response.Body.String(), "TAKEN-1") {
		t.Fatalf("expected barcode conflict response, got %d: %s", response.Code, response.Body.String())
	}
}

func TestVariantBarcodeListDuplicateIsValidationError(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/products/product-id/variants/variant-id/barcodes", nil)
	handled := writeVariantBarcodeError(response, request, &products.VariantValidationError{
		Code: "VARIANT_BARCODE_LIST_DUPLICATE", Message: "Aynı varyantta aynı barkod birden fazla kullanılamaz.",
	})

	if handled {
		t.Fatal("payload-local duplicate must be handled by the generic validation mapper")
	}
	response = httptest.NewRecorder()
	writeModuleError(response, request, &products.VariantValidationError{
		Code: "VARIANT_BARCODE_LIST_DUPLICATE", Message: "Aynı varyantta aynı barkod birden fazla kullanılamaz.",
	}, "Varyant barkodları değiştirilemedi.")
	if response.Code != http.StatusUnprocessableEntity || strings.Contains(response.Body.String(), "başka bir ürün") || !strings.Contains(response.Body.String(), "Aynı varyantta") {
		t.Fatalf("expected payload-local validation response, got %d: %s", response.Code, response.Body.String())
	}
}
