package secrets

import (
	"bytes"
	"testing"
)

func TestBoxBindsCiphertextToCompanyAndField(t *testing.T) {
	box, err := NewBox(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Seal("company-a", "smtp_password", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := box.Open("company-a", "smtp_password", ciphertext)
	if err != nil || string(plaintext) != "secret" {
		t.Fatalf("open = %q, %v", plaintext, err)
	}
	if _, err := box.Open("company-b", "smtp_password", ciphertext); err == nil {
		t.Fatal("ciphertext opened in another company")
	}
	if _, err := box.Open("company-a", "tckn", ciphertext); err == nil {
		t.Fatal("ciphertext opened for another field")
	}
}
