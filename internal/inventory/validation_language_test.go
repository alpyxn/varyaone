package inventory

import (
	"errors"
	"github.com/alpyxn/varyaone/internal/identity"
	"strings"
	"testing"
)

func TestInventoryIdentifierValidationUsesTurkishLabels(t *testing.T) {
	for _, test := range []struct{ name, value, message string }{
		{"warehouse_id", "", "Depo gereklidir."},
		{"product_id", "invalid", "Stok kartı seçimi geçersiz."},
		{"unrecognized_id", "", "Kayıt kimliği gereklidir."},
	} {
		_, err := requireUUID(test.name, test.value)
		if !errors.Is(err, identity.ErrValidation) || !strings.HasSuffix(err.Error(), test.message) {
			t.Fatalf("%s: %v", test.name, err)
		}
	}
}
