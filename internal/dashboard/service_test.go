package dashboard

import (
	"errors"
	"strings"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
)

func TestNormalizeShortcutKeysTrimsLowercasesAndDedupes(t *testing.T) {
	got, err := normalizeShortcutKeys([]string{" Cari.Kartlar ", "cari.kartlar", "action:cari-yeni", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "cari.kartlar" || got[1] != "action:cari-yeni" {
		t.Fatalf("normalizeShortcutKeys() = %#v", got)
	}
}

func TestNormalizeShortcutKeysRejectsInvalidKey(t *testing.T) {
	if _, err := normalizeShortcutKeys([]string{"Cari Kartlar"}); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("invalid shortcut key returned %v", err)
	}
}

func TestNormalizeShortcutKeysEnforcesLimit(t *testing.T) {
	keys := make([]string, 0, maxPinnedShortcuts+1)
	for i := 0; i <= maxPinnedShortcuts; i++ {
		keys = append(keys, "k"+strings.Repeat("a", 1)+itoa(i))
	}
	if _, err := normalizeShortcutKeys(keys); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("over-limit shortcut list returned %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
