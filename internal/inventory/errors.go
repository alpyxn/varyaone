package inventory

import (
	"errors"
	"fmt"
)

// Stable codes are part of the domain contract.  HTTP/API layers should map
// these values to short Turkish operation messages instead of inspecting SQL
// error text.
var (
	ErrInsufficientStock             = errors.New("INSUFFICIENT_STOCK")
	ErrSerialAlreadyInStock          = errors.New("SERIAL_ALREADY_IN_STOCK")
	ErrSerialNotAvailable            = errors.New("SERIAL_NOT_AVAILABLE")
	ErrLotExpired                    = errors.New("LOT_EXPIRED")
	ErrWarehouseTransferInvalidState = errors.New("WAREHOUSE_TRANSFER_INVALID_STATE")
	ErrStockCountAlreadyPosted       = errors.New("STOCK_COUNT_ALREADY_POSTED")
	ErrMovementAlreadyReversed       = errors.New("MOVEMENT_ALREADY_REVERSED")
	ErrDocumentOriginMovement        = errors.New("DOCUMENT_ORIGIN_MOVEMENT")
	ErrIdempotencyConflict           = errors.New("IDEMPOTENCY_CONFLICT")
	ErrInvalidReason                 = errors.New("INVALID_REASON")
	ErrNotFound                      = errors.New("INVENTORY_NOT_FOUND")
	ErrConflict                      = errors.New("INVENTORY_CONFLICT")
	ErrWarehouseInactive             = errors.New("WAREHOUSE_INACTIVE")
	ErrWarehouseNotStandard          = errors.New("WAREHOUSE_NOT_STANDARD")
	ErrWarehouseMovementNotAllowed   = errors.New("WAREHOUSE_MOVEMENT_NOT_ALLOWED")
	ErrTransferSameWarehouse         = errors.New("WAREHOUSE_TRANSFER_SAME_WAREHOUSE")
	ErrWarehouseHasHistory           = errors.New("WAREHOUSE_HAS_HISTORY")
	ErrWarehouseInUse                = errors.New("WAREHOUSE_IN_USE")
	ErrWarehouseHasOpenTransfer      = errors.New("WAREHOUSE_HAS_OPEN_TRANSFER")
	ErrWarehouseSystem               = errors.New("WAREHOUSE_SYSTEM")
	ErrWarehouseTypeImmutable        = errors.New("WAREHOUSE_TYPE_IMMUTABLE")
	ErrVariantRequired               = errors.New("VARIANT_REQUIRED")
	ErrVariantInactive               = errors.New("VARIANT_INACTIVE")
	ErrVariantProductMismatch        = errors.New("VARIANT_PRODUCT_MISMATCH")
)

type CodeError struct {
	Code    string
	Message string
	Cause   error
}

func (e *CodeError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *CodeError) Unwrap() error { return e.Cause }

func codeError(code string, cause error, format string, args ...any) error {
	return &CodeError{Code: code, Cause: cause, Message: fmt.Sprintf(format, args...)}
}

func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var coded *CodeError
	if errors.As(err, &coded) {
		return coded.Code
	}
	switch {
	case errors.Is(err, ErrInsufficientStock):
		return ErrInsufficientStock.Error()
	case errors.Is(err, ErrSerialAlreadyInStock):
		return ErrSerialAlreadyInStock.Error()
	case errors.Is(err, ErrSerialNotAvailable):
		return ErrSerialNotAvailable.Error()
	case errors.Is(err, ErrLotExpired):
		return ErrLotExpired.Error()
	case errors.Is(err, ErrWarehouseTransferInvalidState):
		return ErrWarehouseTransferInvalidState.Error()
	case errors.Is(err, ErrStockCountAlreadyPosted):
		return ErrStockCountAlreadyPosted.Error()
	case errors.Is(err, ErrStockCountEngineReviewRequired):
		return ErrStockCountEngineReviewRequired.Error()
	case errors.Is(err, ErrMovementAlreadyReversed):
		return ErrMovementAlreadyReversed.Error()
	case errors.Is(err, ErrDocumentOriginMovement):
		return ErrDocumentOriginMovement.Error()
	case errors.Is(err, ErrIdempotencyConflict):
		return ErrIdempotencyConflict.Error()
	case errors.Is(err, ErrInvalidReason):
		return ErrInvalidReason.Error()
	case errors.Is(err, ErrWarehouseInactive):
		return ErrWarehouseInactive.Error()
	case errors.Is(err, ErrWarehouseNotStandard):
		return ErrWarehouseNotStandard.Error()
	case errors.Is(err, ErrWarehouseMovementNotAllowed):
		return ErrWarehouseMovementNotAllowed.Error()
	case errors.Is(err, ErrTransferSameWarehouse):
		return ErrTransferSameWarehouse.Error()
	case errors.Is(err, ErrWarehouseHasHistory):
		return ErrWarehouseHasHistory.Error()
	case errors.Is(err, ErrWarehouseInUse):
		return ErrWarehouseInUse.Error()
	case errors.Is(err, ErrWarehouseHasOpenTransfer):
		return ErrWarehouseHasOpenTransfer.Error()
	case errors.Is(err, ErrWarehouseSystem):
		return ErrWarehouseSystem.Error()
	case errors.Is(err, ErrWarehouseTypeImmutable):
		return ErrWarehouseTypeImmutable.Error()
	case errors.Is(err, ErrVariantRequired):
		return ErrVariantRequired.Error()
	case errors.Is(err, ErrVariantInactive):
		return ErrVariantInactive.Error()
	case errors.Is(err, ErrVariantProductMismatch):
		return ErrVariantProductMismatch.Error()
	}
	return ""
}
