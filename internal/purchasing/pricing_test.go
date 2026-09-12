package purchasing

import "testing"

func TestPurchaseDiscountChainSkipsZeroDiscount(t *testing.T) {
	line := &PurchaseInvoiceLine{DiscountAmount: "0"}
	if chain := purchaseDiscountChain(line); len(chain) != 0 {
		t.Fatalf("zero discount produced a chain: %+v", chain)
	}
	line.DiscountAmount = ""
	if chain := purchaseDiscountChain(line); len(chain) != 0 {
		t.Fatalf("empty discount produced a chain: %+v", chain)
	}
}

func TestPurchaseDiscountChainWrapsFixedAmount(t *testing.T) {
	line := &PurchaseInvoiceLine{DiscountAmount: "15.5"}
	chain := purchaseDiscountChain(line)
	if len(chain) != 1 || chain[0].Amount != "15.5" {
		t.Fatalf("discount chain = %+v", chain)
	}
}

func TestPurchaseConversionDiscountPreservesPartialShares(t *testing.T) {
	for _, tc := range []struct{ total, ordered, invoiced, quantity, want string }{
		{"100", "10", "0", "4", "40.00000000"},
		{"100", "10", "4", "6", "60.00000000"},
		{"1", "3", "0", "1", "0.33333333"},
		{"1", "3", "1", "1", "0.33333334"},
		{"1", "3", "2", "1", "0.33333333"},
		{"0", "10", "0", "10", "0.00000000"},
	} {
		got := purchaseConversionDiscount(purchaseConversionOrderLine{DiscountAmount: tc.total, OrderedQuantity: tc.ordered, InvoicedQuantity: tc.invoiced}, tc.quantity)
		if got != tc.want {
			t.Fatalf("discount share for %+v = %s", tc, got)
		}
	}
}
