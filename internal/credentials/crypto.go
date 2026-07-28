// Package credentials contains scoped Terraform credential storage helpers.
package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

var ErrInvalidKey = errors.New("credential encryption key must be 32 bytes")

type Cipher struct {
	aead cipher.AEAD
}

// NewCipher constructs an AES-GCM cipher from a 32-byte raw, base64, or hex key.
func NewCipher(rawKey string) (*Cipher, error) {
	key, err := decodeKey(rawKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential AEAD: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals a credential value and returns nonce-prefixed, base64 ciphertext.
func (cipher *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, cipher.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	ciphertext := cipher.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawStdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt authenticates and opens nonce-prefixed, base64 credential ciphertext.
func (cipher *Cipher) Decrypt(encoded string) (string, error) {
	ciphertext, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode credential ciphertext: %w", err)
	}
	nonceSize := cipher.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("credential ciphertext is too short")
	}
	plaintext, err := cipher.aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt credential: %w", err)
	}
	return string(plaintext), nil
}

// decodeKey accepts the supported key encodings and requires exactly 32 bytes.
func decodeKey(raw string) ([]byte, error) {
	if decoded, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len(raw) == 32 {
		return []byte(raw), nil
	}
	return nil, ErrInvalidKey
}
