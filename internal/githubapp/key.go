package githubapp

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// ParsePrivateKey reads a GitHub App's RSA signing key.
//
// Two encodings are accepted for the same bytes: a PEM document as GitHub
// downloads it, and a base64 blob of that document. The second exists because a
// multi-line value is awkward to carry through a .env file or a container
// environment variable, where a single line is the only reliable shape.
//
// GitHub issues PKCS#1 keys; PKCS#8 is accepted too, since a key round-tripped
// through other tooling often comes back in that form.
//
// No error returned here includes any part of the input: these errors reach
// startup logs.
func ParsePrivateKey(value string) (*rsa.PrivateKey, error) {
	raw := []byte(strings.TrimSpace(value))
	if len(raw) == 0 {
		return nil, errors.New("private key is empty")
	}
	if !strings.HasPrefix(string(raw), "-----BEGIN") {
		decoded, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil {
			return nil, errors.New("private key is neither PEM nor base64-encoded PEM")
		}
		raw = decoded
	}

	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("no PEM block found in private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("private key is not a valid PKCS#1 or PKCS#8 RSA key")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is %T, want an RSA key", parsed)
	}
	return key, nil
}
