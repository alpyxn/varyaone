package preferences

import (
	"errors"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
)

func TestNormalizeTableKey(t *testing.T) {
	got, err := normalizeTableKey(" Cari-Kartlar ")
	if err != nil || got != "cari-kartlar" {
		t.Fatalf("normalizeTableKey() = %q, %v", got, err)
	}
	if _, err = normalizeTableKey("Cari Kartlar"); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("invalid table key returned %v", err)
	}
}

func TestNormalizeColumnVisibilityStoresOnlyHiddenColumns(t *testing.T) {
	got, err := normalizeColumnVisibility(map[string]bool{"code": true, "risk_policy": false})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["risk_policy"] != false {
		t.Fatalf("normalized visibility = %#v, want only hidden columns", got)
	}
	if _, err = normalizeColumnVisibility(map[string]bool{"risk policy": false}); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("invalid column key returned %v", err)
	}
}
