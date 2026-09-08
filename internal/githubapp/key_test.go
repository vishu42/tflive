package githubapp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"
)

func pkcs1PEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return string(encoded), key
}

// GitHub hands out PKCS#1 ("BEGIN RSA PRIVATE KEY").
func TestParsePrivateKeyAcceptsPKCS1PEM(t *testing.T) {
	t.Parallel()

	encoded, key := pkcs1PEM(t)

	parsed, err := ParsePrivateKey(encoded)
	if err != nil {
		t.Fatalf("ParsePrivateKey returned error: %v", err)
	}
	if !parsed.Equal(key) {
		t.Fatal("parsed key does not match the original")
	}
}

func TestParsePrivateKeyAcceptsPKCS8PEM(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	parsed, err := ParsePrivateKey(string(encoded))
	if err != nil {
		t.Fatalf("ParsePrivateKey returned error: %v", err)
	}
	if !parsed.Equal(key) {
		t.Fatal("parsed key does not match the original")
	}
}

// A multi-line PEM is awkward to carry in a .env file, so a base64 blob of the
// whole PEM is accepted as an equivalent single-line form.
func TestParsePrivateKeyAcceptsBase64EncodedPEM(t *testing.T) {
	t.Parallel()

	encoded, key := pkcs1PEM(t)
	blob := base64.StdEncoding.EncodeToString([]byte(encoded))

	parsed, err := ParsePrivateKey(blob)
	if err != nil {
		t.Fatalf("ParsePrivateKey returned error: %v", err)
	}
	if !parsed.Equal(key) {
		t.Fatal("parsed key does not match the original")
	}
}

func TestParsePrivateKeyRejectsGarbage(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "not a key", "-----BEGIN RSA PRIVATE KEY-----\nnope\n-----END RSA PRIVATE KEY-----"} {
		if _, err := ParsePrivateKey(value); err == nil {
			t.Fatalf("ParsePrivateKey(%q) returned no error", value)
		}
	}
}

// The key must never be echoed back in an error; error strings reach logs.
func TestParsePrivateKeyErrorOmitsKeyMaterial(t *testing.T) {
	t.Parallel()

	_, err := ParsePrivateKey("-----BEGIN RSA PRIVATE KEY-----\nSECRETMATERIAL\n-----END RSA PRIVATE KEY-----")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "SECRETMATERIAL") {
		t.Fatalf("error = %q, want no key material", err)
	}
}
