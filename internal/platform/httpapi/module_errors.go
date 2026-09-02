package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/idempotency"
	"github.com/alpyxn/varyaone/internal/pricing"
	"github.com/alpyxn/varyaone/internal/products"
	"github.com/alpyxn/varyaone/internal/taxes"
)

func writeModuleError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	if code := products.ErrorCode(err); code != "" {
		message := strings.TrimPrefix(err.Error(), code+": ")
		status := http.StatusUnprocessableEntity
		switch code {
		case "VARIANT_BARCODE_DUPLICATE":
			status = http.StatusConflict
		case "VARIANT_STATE_CONFLICT", "VARIANT_IDENTITY_LOCKED", "VARIANT_CONFIG_LOCKED", "VARIANT_MODE_CANNOT_BE_DISABLED", "VARIANT_MODE_REQUIRES_EMPTY_STOCK_HISTORY", "VARIANT_MODE_REQUIRES_EMPTY_PRODUCT_BARCODES":
			status = http.StatusConflict
		}
		writeError(w, r, status, code, message)
		return
	}
	if isVariantBarcodeDuplicateError(err) {
		writeError(w, r, http.StatusConflict, "VARIANT_BARCODE_DUPLICATE", "Bu barkod firmada başka bir ürün veya varyantta kullanılıyor.")
		return
	}
	switch {
	case errors.Is(err, idempotency.ErrKeyRequired):
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Bu işlem için Idempotency-Key gereklidir.")
	case errors.Is(err, idempotency.ErrPayloadConflict):
		writeError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Aynı anahtar farklı istek verisiyle kullanıldı.")
	case errors.Is(err, idempotency.ErrCommandInProgress):
		writeError(w, r, http.StatusConflict, "COMMAND_IN_PROGRESS", "Aynı işlem halen yürütülüyor.")
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok veya kayıt kapsamınız dışında.")
	case errors.Is(err, identity.ErrConflict):
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Kayıt başka bir kullanıcı tarafından değiştirilmiş.")
	case errors.Is(err, pricing.ErrOverlap), errors.Is(err, taxes.ErrRateOverlap):
		writeError(w, r, http.StatusConflict, "PERIOD_OVERLAP", "Aynı kayıt için çakışan geçerlilik dönemi oluşturulamaz.")
	case errors.Is(err, pricing.ErrNotFound), errors.Is(err, taxes.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "İstenen kayıt bulunamadı.")
	case errors.Is(err, identity.ErrValidation):
		message := strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": ")
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", message)
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
	}
}

func isVariantBarcodeDuplicateError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "barkod bu firmada zaten kullanılıyor") ||
		strings.Contains(message, "product_barcodes_company_id_barcode_key") ||
		strings.Contains(message, "product_primary_barcode_unique") ||
		strings.Contains(message, "product_variant_primary_barcode_unique")
}
