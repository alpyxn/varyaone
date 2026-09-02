package inventory

import (
	"bytes"
	"testing"
)

const (
	operationTestCompany   = "10000000-0000-4000-8000-000000000101"
	operationTestWarehouse = "10000000-0000-4000-8000-000000000102"
	operationTestProduct   = "10000000-0000-4000-8000-000000000103"
	operationTestVariantA  = "10000000-0000-4000-8000-000000000104"
	operationTestVariantB  = "10000000-0000-4000-8000-000000000105"
)

func validStockMovementOperationInput() StockMovementOperationInput {
	return StockMovementOperationInput{
		CompanyID: operationTestCompany, WarehouseID: operationTestWarehouse, ProductID: operationTestProduct,
		MovementType: MovementManualAdjustment, Direction: DirectionIn, UnitCode: " adet ", Currency: "try",
		ReasonCode: "opening", ReasonDescription: "İlk sayım", IdempotencyKey: "stock-operation-test",
		Lines: []StockMovementOperationLine{
			{VariantID: operationTestVariantB, Quantity: "2.00000000", UnitCost: "120.50"},
			{VariantID: operationTestVariantA, Quantity: "3", UnitCost: "100"},
		},
	}
}

func TestNormalizeStockMovementOperationCanonicalizesAndSortsLines(t *testing.T) {
	first, firstLines, firstHash, err := normalizeStockMovementOperation(validStockMovementOperationInput())
	if err != nil {
		t.Fatal(err)
	}
	if first.Direction != DirectionIn || first.Currency != "TRY" || first.UnitCode != "ADET" || first.ReasonCode != "OPENING" {
		t.Fatalf("normalized header=%+v", first)
	}
	if len(firstLines) != 2 || firstLines[0].VariantID != operationTestVariantA || firstLines[1].VariantID != operationTestVariantB {
		t.Fatalf("lines were not sorted deterministically: %+v", firstLines)
	}

	secondInput := validStockMovementOperationInput()
	secondInput.Lines[0], secondInput.Lines[1] = secondInput.Lines[1], secondInput.Lines[0]
	_, _, secondHash, err := normalizeStockMovementOperation(secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstHash, secondHash) {
		t.Fatal("line order changed the idempotency payload hash")
	}
}

func TestNormalizeStockMovementOperationRejectsInvalidLines(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*StockMovementOperationInput)
	}{
		{name: "no lines", mutate: func(input *StockMovementOperationInput) { input.Lines = nil }},
		{name: "duplicate variant", mutate: func(input *StockMovementOperationInput) { input.Lines[1].VariantID = input.Lines[0].VariantID }},
		{name: "zero quantity", mutate: func(input *StockMovementOperationInput) { input.Lines[0].Quantity = "0" }},
		{name: "outbound cost", mutate: func(input *StockMovementOperationInput) { input.Direction = DirectionOut; input.ReasonCode = "DAMAGE" }},
		{name: "wrong reason direction", mutate: func(input *StockMovementOperationInput) { input.ReasonCode = "DAMAGE" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validStockMovementOperationInput()
			test.mutate(&input)
			if _, _, _, err := normalizeStockMovementOperation(input); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestNormalizeStockMovementOperationKeepsDifferentPayloadsDistinct(t *testing.T) {
	first := validStockMovementOperationInput()
	second := validStockMovementOperationInput()
	second.Lines[0].Quantity = "4"
	_, _, firstHash, err := normalizeStockMovementOperation(first)
	if err != nil {
		t.Fatal(err)
	}
	_, _, secondHash, err := normalizeStockMovementOperation(second)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(firstHash, secondHash) {
		t.Fatal("different operation payloads produced the same hash")
	}
}
