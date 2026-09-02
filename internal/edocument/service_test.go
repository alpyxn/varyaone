package edocument

import (
	"context"
	"testing"
)

func TestLifecycleTransitions(t *testing.T) {
	cases := []struct {
		from, to Status
		ok       bool
	}{
		{Draft, Queued, true}, {Queued, Submitting, true}, {Submitting, Accepted, true},
		{Accepted, CancelRequested, true}, {CancelRequested, Cancelled, true},
		{Draft, Accepted, false}, {Cancelled, Queued, false},
	}
	for _, tc := range cases {
		if got := canTransition(tc.from, tc.to); got != tc.ok {
			t.Errorf("%s -> %s: got %v, want %v", tc.from, tc.to, got, tc.ok)
		}
	}
}

func TestMockProviderDoesNotExposeProviderDTO(t *testing.T) {
	provider := NewMockProvider()
	result, err := provider.Submit(context.Background(), CanonicalDocument{SourceID: "source-123", Currency: "TRY", DocumentDate: "2026-08-21", DocumentType: DocumentInvoice, Direction: Outgoing, Lines: []Line{{Description: "test"}}})
	if err != nil || result.Status != Accepted || result.ProviderKey != "mock" {
		t.Fatalf("unexpected mock result: %#v %v", result, err)
	}
}

func TestCreateValidationRejectsCredentialLikeProviderKey(t *testing.T) {
	err := validateCreate(CreateInput{ProviderKey: "provider-token", Payload: CanonicalDocument{SourceType: "invoice", SourceID: "id", Currency: "TRY", DocumentDate: "2026-08-21", DocumentType: DocumentInvoice, Direction: Outgoing, Lines: []Line{{Description: "x"}}}})
	if err != ErrSensitiveData {
		t.Fatalf("expected sensitive data rejection, got %v", err)
	}
}
