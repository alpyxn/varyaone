package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/alpyxn/varyaone/internal/platform/secrets"
)

type SecretBox = secrets.Box

func NewSecretBox(key []byte) (*SecretBox, error) {
	return secrets.NewBox(key)
}

func randomToken(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func tokenHash(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func identifierHash(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func newID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
