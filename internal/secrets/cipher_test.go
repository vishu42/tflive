package secrets

import (
	"encoding/base64"
	"testing"
)

func TestCipherEncryptDecryptRoundTrip(t *testing.T) {
	// Verify that encryption is reversible while the stored representation is not plaintext.
	cipher, err := NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}

	ciphertext, err := cipher.Encrypt("provider-secret")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}
	if ciphertext == "provider-secret" {
		t.Fatal("ciphertext contains plaintext")
	}

	plaintext, err := cipher.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if plaintext != "provider-secret" {
		t.Fatalf("plaintext = %q, want provider-secret", plaintext)
	}
}

func TestCipherRejectsWrongKey(t *testing.T) {
	// Verify that authenticated encryption rejects ciphertext opened with another key.
	cipher, err := NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	ciphertext, err := cipher.Encrypt("provider-secret")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	other, err := NewCipher("abcdefghijklmnopqrstuvwxyz123456")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	if _, err := other.Decrypt(ciphertext); err == nil {
		t.Fatal("Decrypt succeeded with the wrong key")
	}
}

func TestNewCipherRejectsInvalidKey(t *testing.T) {
	// Verify that cipher construction fails closed for unsupported key material.
	if _, err := NewCipher("too-short"); err == nil {
		t.Fatal("NewCipher accepted an invalid key")
	}
}

func TestCipherRejectsTamperedCiphertext(t *testing.T) {
	// AEAD is the only reason a sealed cookie can be trusted, so prove it detects edits.
	cipher, err := NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	ciphertext, err := cipher.Encrypt("provider-secret")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	// Flip a bit inside the ciphertext body, not at the base64 tail: the final
	// character can encode as few as two significant bits, so editing it may
	// land entirely in discarded padding and escape detection. Any bit flip in
	// GCM ciphertext or tag fails authentication, so this is deterministic.
	raw, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		t.Fatalf("DecodeString returned error: %v", err)
	}
	raw[len(raw)/2] ^= 0x01
	if _, err := cipher.Decrypt(base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("Decrypt accepted tampered ciphertext")
	}
}
