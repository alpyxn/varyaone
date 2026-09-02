package identity

import (
	"bytes"
	"testing"
)

func TestCompanySecretUsesAuthenticatedContext(t *testing.T) {
	box, err := NewSecretBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Seal("company-a", "nilvera", []byte("credential"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := box.Open("company-a", "nilvera", ciphertext)
	if err != nil || string(plaintext) != "credential" {
		t.Fatalf("round trip failed: %q %v", plaintext, err)
	}
	if _, err := box.Open("company-b", "nilvera", ciphertext); err == nil {
		t.Fatal("ciphertext was accepted under a different company boundary")
	}
	if _, err := box.Open("company-a", "another-secret", ciphertext); err == nil {
		t.Fatal("ciphertext was accepted under a different secret name")
	}
}

func TestAPITokenPermissionsAreIntersectionOfOwnerAndScopes(t *testing.T) {
	permissions := intersectTokenPermissions(
		[]string{"organization.company.read", "security.user.read", "security.token.manage"},
		[]string{"organization:read", "security:audit:read", "unknown"},
	)
	if len(permissions) != 1 || permissions[0] != "organization.company.read" {
		t.Fatalf("unexpected effective permissions: %v", permissions)
	}
}
