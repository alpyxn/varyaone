package inventory

import "testing"

func TestCommercialMovementTypeUsesTypedSalesDiscriminator(t *testing.T) {
	tests := []struct {
		documentType string
		movementType string
		direction    string
	}{
		{documentType: "SALES_DELIVERY", movementType: MovementSalesDispatch, direction: DirectionOut},
		{documentType: "SALES_INVOICE", movementType: MovementSalesDispatch, direction: DirectionOut},
		{documentType: "SALES_RETURN_INVOICE", movementType: MovementSalesReturn, direction: DirectionIn},
		{documentType: "GOODS_RECEIPT", movementType: MovementPurchaseReceipt, direction: DirectionIn},
		{documentType: "PURCHASE_RETURN_INVOICE", movementType: MovementPurchaseReturn, direction: DirectionOut},
	}
	for _, test := range tests {
		t.Run(test.documentType, func(t *testing.T) {
			movementType, direction := commercialMovementType(test.documentType)
			if movementType != test.movementType || direction != test.direction {
				t.Fatalf("commercial movement for %s = (%q, %q), want (%q, %q)", test.documentType, movementType, direction, test.movementType, test.direction)
			}
		})
	}
}

func TestCommercialMovementTypeRejectsUnknownDocument(t *testing.T) {
	if movementType, direction := commercialMovementType("SALES_DISPATCH"); movementType != MovementSalesDispatch || direction != DirectionOut {
		t.Fatalf("legacy sales dispatch discriminator = (%q, %q)", movementType, direction)
	}
	if movementType, direction := commercialMovementType("UNKNOWN"); movementType != "" || direction != "" {
		t.Fatalf("unknown discriminator = (%q, %q), want empty effect", movementType, direction)
	}
}
