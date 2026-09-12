package purchasing

import "testing"

func TestPurchaseSourceTypesMapToKind(t *testing.T) {
	tests := []struct {
		code, kind string
	}{
		{code: "PURCHASE_ORDER", kind: "ORDER"},
		{code: "PURCHASE_DELIVERY", kind: "RECEIPT"},
		{code: "PURCHASE_INVOICE", kind: "INVOICE"},
		{code: "PURCHASE_RETURN_INVOICE", kind: "RETURN"},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			kind, ok := purchaseKindForDocumentType(test.code)
			if !ok || purchaseSourceKind(kind) != test.kind {
				t.Fatalf("source type %q mapped to kind=%q ok=%v, want %q", test.code, purchaseSourceKind(kind), ok, test.kind)
			}
		})
	}
}
