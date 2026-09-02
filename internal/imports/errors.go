package imports

import "errors"

var (
	// ErrIdentityConflict means that an import row tries to reuse a company
	// identity for a different catalog record. It is a validation outcome, not
	// an infrastructure failure, and is safe to expose through the API.
	ErrIdentityConflict            = errors.New("IMPORT_IDENTITY_CONFLICT")
	ErrOpeningStockExistingProduct = errors.New("IMPORT_OPENING_STOCK_EXISTING_PRODUCT")
	ErrOpeningStockNotAuthorized   = errors.New("IMPORT_OPENING_STOCK_NOT_AUTHORIZED")
	ErrNotReady                    = errors.New("IMPORT_NOT_READY")
)
