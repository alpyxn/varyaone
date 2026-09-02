package idempotency

import (
	"errors"
	"testing"
)

func TestMemoryStoreReplaysOnlySamePayload(t *testing.T) {
	store := NewMemoryStore()
	first, replay, err := store.Reserve("company", "key-1", "party.post", []byte(`{"amount":"10.00"}`))
	if err != nil || replay || first.Status != "IN_PROGRESS" {
		t.Fatalf("reserve = %+v, replay=%v, err=%v", first, replay, err)
	}
	if err := store.Complete("company", "key-1", 201, map[string]string{"id": "x"}); err != nil {
		t.Fatal(err)
	}
	second, replay, err := store.Reserve("company", "key-1", "party.post", []byte(`{"amount":"10.00"}`))
	if err != nil || !replay || second.ResponseStatus != 201 {
		t.Fatalf("replay = %+v, replay=%v, err=%v", second, replay, err)
	}
	if _, _, err := store.Reserve("company", "key-1", "party.post", []byte(`{"amount":"11.00"}`)); !errors.Is(err, ErrPayloadConflict) {
		t.Fatalf("expected payload conflict, got %v", err)
	}
}

func TestNormalizeKeyAndHash(t *testing.T) {
	if got := PayloadHash([]byte("abc")); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("unexpected hash: %s", got)
	}
	if _, err := NormalizeKey(" "); !errors.Is(err, ErrKeyRequired) {
		t.Fatalf("expected key validation, got %v", err)
	}
}
