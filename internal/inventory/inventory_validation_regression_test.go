package inventory

import (
	"errors"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
)

const (
	validationTestCompany   = "20000000-0000-4000-8000-000000000101"
	validationTestWarehouse = "20000000-0000-4000-8000-000000000102"
	validationTestProduct   = "20000000-0000-4000-8000-000000000103"
)

func validValidationMovement() MovementInput {
	return MovementInput{
		CompanyID: validationTestCompany, WarehouseID: validationTestWarehouse, ProductID: validationTestProduct,
		MovementType: MovementManualAdjustment, Direction: DirectionIn, Quantity: "1", ReasonCode: "OPENING",
	}
}

func TestInventoryCommandValidationErrorsAreIdentityValidation(t *testing.T) {
	manufacturedAt := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	expiresAt := manufacturedAt.Add(-time.Hour)
	tests := []struct {
		name string
		call func() error
	}{
		{name: "required uuid", call: func() error { _, err := requireUUID("warehouse_id", ""); return err }},
		{name: "malformed uuid", call: func() error { _, err := requireUUID("warehouse_id", "not-a-uuid"); return err }},
		{name: "optional malformed uuid", call: func() error { _, err := optionalUUID("not-a-uuid"); return err }},
		{name: "invalid quantity", call: func() error { _, err := cleanQuantity("quantity", "not-a-number", true); return err }},
		{name: "invalid direction", call: func() error {
			input := validValidationMovement()
			input.Direction = "SIDEWAYS"
			_, err := normalizeMovement(input)
			return err
		}},
		{name: "invalid movement type", call: func() error {
			input := validValidationMovement()
			input.MovementType = "UNKNOWN"
			_, err := normalizeMovement(input)
			return err
		}},
		{name: "invalid currency", call: func() error {
			input := validValidationMovement()
			input.Currency = "EURO"
			_, err := normalizeMovement(input)
			return err
		}},
		{name: "missing expiry override reason", call: func() error {
			input := validValidationMovement()
			input.ExpiryOverride = true
			_, err := normalizeMovement(input)
			return err
		}},
		{name: "invalid lot expiry order", call: func() error {
			input := validValidationMovement()
			input.LotManufacturedAt = &manufacturedAt
			input.LotExpiresAt = &expiresAt
			_, err := normalizeMovement(input)
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err == nil || !errors.Is(err, identity.ErrValidation) {
				t.Fatalf("error=%v, want identity.ErrValidation", err)
			}
		})
	}
}
