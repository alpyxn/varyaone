// Package secrets encrypts company-scoped secret values at rest.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// Box uses AES-256-GCM and authenticates the company and field names as AAD.
// A ciphertext copied to another company or column therefore cannot decrypt.
type Box struct {
	aead cipher.AEAD
}

func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("master key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Seal(companyID, name string, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	return append(nonce, b.aead.Seal(nil, nonce, plaintext, aad(companyID, name))...), nil
}

func (b *Box) Open(companyID, name string, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < b.aead.NonceSize()+b.aead.Overhead() {
		return nil, errors.New("invalid ciphertext")
	}
	nonce := ciphertext[:b.aead.NonceSize()]
	plaintext, err := b.aead.Open(nil, nonce, ciphertext[b.aead.NonceSize():], aad(companyID, name))
	if err != nil {
		return nil, errors.New("decrypt company secret")
	}
	return plaintext, nil
}

func aad(companyID, name string) []byte { return []byte(companyID + "\x00" + name) }
