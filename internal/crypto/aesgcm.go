// Package crypto provides authenticated symmetric encryption for at-rest
// secrets (e.g., OAuth refresh tokens). 32-byte key → AES-256-GCM.
// The output is [nonce | ciphertext+tag]; Decrypt unpacks the nonce.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds an AES-256-GCM cipher from a 32-byte key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm new: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt returns nonce||ciphertext||tag (12-byte nonce).
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt unpacks nonce||ciphertext, verifying the GCM tag.
func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(blob) < ns {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ciphertext := blob[:ns], blob[ns:]
	out, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	return out, nil
}
