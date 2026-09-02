package inventory

import "testing"

func TestOperationLineEnteredQuantityIsKeptSeparateFromBaseQuantity(t *testing.T) {
	movement := MovementInput{EnteredQuantity: "2", Quantity: "20"}
	entered, base := operationLineQuantities(movement)
	if entered != "2" || base != "20" {
		t.Fatalf("operation quantities=(%q,%q), want entered=2 base=20", entered, base)
	}

	entered, base = operationLineQuantities(MovementInput{Quantity: "5"})
	if entered != "5" || base != "5" {
		t.Fatalf("quantity fallback=(%q,%q), want 5,5", entered, base)
	}
}
