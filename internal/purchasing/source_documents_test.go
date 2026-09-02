package purchasing

import "testing"

func TestPurchaseSourceDocumentReferenceUsesTypedMetadata(t *testing.T) {
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
			reference := SourceDocumentReference{
				ID:               "source-id",
				DocumentNo:       "ALG-001",
				DocumentTypeCode: test.code,
				Kind:             test.kind,
				RelationType:     "INVOICING",
				Direction:        "SOURCE",
				LifecycleStatus:  purchaseLifecycleStatus(kind, "POSTED"),
				Status:           "POSTED",
			}
			if reference.DocumentNo != "ALG-001" || reference.RelationType != "INVOICING" || reference.Direction != "SOURCE" {
				t.Fatalf("source reference lost read metadata: %+v", reference)
			}
		})
	}
}

func TestPurchaseSourceDocumentReferencesPreserveMultipleRelations(t *testing.T) {
	sources := []SourceDocumentReference{
		{ID: "receipt-a", DocumentNo: "MAL-001", DocumentTypeCode: "PURCHASE_DELIVERY", Kind: "RECEIPT", RelationType: "FULFILLMENT", Direction: "SOURCE", LifecycleStatus: "FINALIZED", Status: "POSTED"},
		{ID: "receipt-b", DocumentNo: "MAL-002", DocumentTypeCode: "PURCHASE_DELIVERY", Kind: "RECEIPT", RelationType: "INVOICING", Direction: "SOURCE", LifecycleStatus: "FINALIZED", Status: "POSTED"},
	}
	if len(sources) != 2 || sources[0].DocumentNo != "MAL-001" || sources[1].DocumentNo != "MAL-002" {
		t.Fatalf("multiple purchase source references were collapsed: %+v", sources)
	}
	if sources[0].RelationType == sources[1].RelationType {
		t.Fatal("purchase source relation types were collapsed")
	}
}
