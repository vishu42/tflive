package activities

import (
	"bytes"
	"context"
	"testing"

	"github.com/vishu42/tflive/internal/domain"
)

type testCredentialReader struct {
	credentials []domain.CredentialSet
}

// TestRedactingWriterRemovesCredentialValues verifies that command output never exposes exact secret values.
func TestRedactingWriterRemovesCredentialValues(t *testing.T) {
	var output bytes.Buffer
	writer := newRedactingWriter(nopWriteCloser{Buffer: &output}, []string{"secret"})
	if _, err := writer.Write([]byte("key=secret")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if output.String() != "key=******" || bytes.Contains(output.Bytes(), []byte("secret")) {
		t.Fatalf("output = %q", output.String())
	}
}

type nopWriteCloser struct{ *bytes.Buffer }

// Close implements io.WriteCloser for the in-memory test buffer.
func (writer nopWriteCloser) Close() error { return nil }

// ListCredentialsForStackTemplate returns fixture credentials to the resolver under test.
func (reader testCredentialReader) ListCredentialsForStackTemplate(context.Context, domain.TenantID, domain.StackTemplateID) ([]domain.CredentialSet, error) {
	return reader.credentials, nil
}

type testCredentialDecryptor struct{}

// Decrypt provides deterministic plaintext for resolver tests without using real key material.
func (testCredentialDecryptor) Decrypt(value string) (string, error) {
	return "decrypted:" + value, nil
}

// TestResolveCredentialEnvironmentTemplateOverridesStack verifies inheritance and template precedence.
func TestResolveCredentialEnvironmentTemplateOverridesStack(t *testing.T) {
	environment, err := resolveCredentialEnvironment(context.Background(), testCredentialReader{credentials: []domain.CredentialSet{
		{StackID: "stack_123", Name: "CLOUD_REGION", Ciphertext: "stack-region"},
		{StackID: "stack_123", Name: "CLOUD_TOKEN", Ciphertext: "stack-token"},
		{StackTemplateID: "template_123", Name: "CLOUD_TOKEN", Ciphertext: "template-token"},
	}}, testCredentialDecryptor{}, "tenant_123", "template_123")
	if err != nil {
		t.Fatalf("resolveCredentialEnvironment returned error: %v", err)
	}
	if environment["CLOUD_REGION"] != "decrypted:stack-region" {
		t.Fatalf("region = %q", environment["CLOUD_REGION"])
	}
	if environment["CLOUD_TOKEN"] != "decrypted:template-token" {
		t.Fatalf("token = %q", environment["CLOUD_TOKEN"])
	}
}
