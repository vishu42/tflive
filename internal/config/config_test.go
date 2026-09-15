package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestLoadAPIConfigReadsAPISettings(t *testing.T) {
	t.Parallel()

	cfg, err := LoadAPIConfig(withValidSecurity(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return " postgres://user:pass@localhost:5432/db?sslmode=disable "
		case "HTTP_ADDRESS":
			return " :9090 "
		case "TEMPORAL_ADDRESS":
			return " localhost:7233 "
		case "TEMPORAL_NAMESPACE":
			return " tflive "
		case "WORKER_RUN_ROOT":
			return " /var/lib/tflive/runs "
		case "ARTIFACT_STORE_KIND":
			return " s3 "
		case "ARTIFACT_STORE_FILESYSTEM_ROOT":
			return " /var/lib/tflive/artifacts "
		case "S3_BUCKET":
			return " tflive-artifacts "
		case "S3_REGION":
			return " us-east-1 "
		case "S3_ENDPOINT":
			return " https://s3.us-east-1.amazonaws.com "
		case "S3_ACCESS_KEY_ID":
			return " access-key "
		case "S3_SECRET_ACCESS_KEY":
			return " secret-key "
		case "S3_FORCE_PATH_STYLE":
			return " true "
		default:
			return ""
		}
	}))
	if err != nil {
		t.Fatalf("LoadAPIConfig returned error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.HTTPAddress != ":9090" {
		t.Fatalf("HTTPAddress = %q", cfg.HTTPAddress)
	}
	if cfg.TemporalAddress != "localhost:7233" {
		t.Fatalf("TemporalAddress = %q", cfg.TemporalAddress)
	}
	if cfg.TemporalNamespace != "tflive" {
		t.Fatalf("TemporalNamespace = %q", cfg.TemporalNamespace)
	}
	if cfg.WorkerRunRoot != "/var/lib/tflive/runs" {
		t.Fatalf("WorkerRunRoot = %q, want /var/lib/tflive/runs", cfg.WorkerRunRoot)
	}
	if cfg.Security.TenantID != "tenant_123" {
		t.Fatalf("Security.TenantID = %q, want tenant_123", cfg.Security.TenantID)
	}
	assertArtifactStoreConfig(t, cfg.ArtifactStore)
}

func TestLoadAPIConfigAppliesDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := LoadAPIConfig(withValidSecurity(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		case "OPENFGA_API_URL":
			return "http://localhost:8080"
		case "OPENFGA_STORE_ID":
			return "store-id"
		case "OPENFGA_MODEL_ID":
			return "model-id"
		default:
			return ""
		}
	}))
	if err != nil {
		t.Fatalf("LoadAPIConfig returned error: %v", err)
	}

	if cfg.HTTPAddress != DefaultHTTPAddress {
		t.Fatalf("HTTPAddress = %q, want %q", cfg.HTTPAddress, DefaultHTTPAddress)
	}
	if cfg.TemporalNamespace != "" {
		t.Fatalf("TemporalNamespace = %q, want empty", cfg.TemporalNamespace)
	}
	if cfg.WorkerRunRoot != DefaultWorkerRunRoot {
		t.Fatalf("WorkerRunRoot = %q, want %q", cfg.WorkerRunRoot, DefaultWorkerRunRoot)
	}
	if cfg.ArtifactStore.Kind != ArtifactStoreFilesystem {
		t.Fatalf("ArtifactStore.Kind = %q, want %q", cfg.ArtifactStore.Kind, ArtifactStoreFilesystem)
	}
	if cfg.ArtifactStore.FilesystemRoot != DefaultArtifactStoreFilesystemRoot {
		t.Fatalf("ArtifactStore.FilesystemRoot = %q, want %q", cfg.ArtifactStore.FilesystemRoot, DefaultArtifactStoreFilesystemRoot)
	}
}

func TestLoadAPIConfigRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	_, err := LoadAPIConfig(withValidSecurity(func(key string) string {
		if key == "TEMPORAL_ADDRESS" {
			return "localhost:7233"
		}
		return ""
	}))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestLoadAPIConfigRequiresTemporalAddress(t *testing.T) {
	t.Parallel()

	_, err := LoadAPIConfig(withValidSecurity(func(key string) string {
		if key == "DATABASE_URL" {
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		}
		return ""
	}))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestLoadWorkerConfigReadsWorkerSettings(t *testing.T) {
	t.Parallel()

	cfg, err := LoadWorkerConfig(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return " postgres://user:pass@localhost:5432/db?sslmode=disable "
		case "TEMPORAL_ADDRESS":
			return " localhost:7233 "
		case "TEMPORAL_NAMESPACE":
			return " tflive "
		case "WORKER_RUN_ROOT":
			return " /var/lib/tflive/runs "
		case "ARTIFACT_STORE_KIND":
			return " s3 "
		case "ARTIFACT_STORE_FILESYSTEM_ROOT":
			return " /var/lib/tflive/artifacts "
		case "S3_BUCKET":
			return " tflive-artifacts "
		case "S3_REGION":
			return " us-east-1 "
		case "S3_ENDPOINT":
			return " https://s3.us-east-1.amazonaws.com "
		case "S3_ACCESS_KEY_ID":
			return " access-key "
		case "S3_SECRET_ACCESS_KEY":
			return " secret-key "
		case "S3_FORCE_PATH_STYLE":
			return " true "
		case "OPENFGA_API_URL":
			return "http://localhost:8080"
		case "OPENFGA_STORE_ID":
			return "store-id"
		case "OPENFGA_MODEL_ID":
			return "model-id"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("LoadWorkerConfig returned error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.TemporalAddress != "localhost:7233" {
		t.Fatalf("TemporalAddress = %q", cfg.TemporalAddress)
	}
	if cfg.TemporalNamespace != "tflive" {
		t.Fatalf("TemporalNamespace = %q", cfg.TemporalNamespace)
	}
	if cfg.WorkerRunRoot != "/var/lib/tflive/runs" {
		t.Fatalf("WorkerRunRoot = %q, want /var/lib/tflive/runs", cfg.WorkerRunRoot)
	}
	assertArtifactStoreConfig(t, cfg.ArtifactStore)
}

func TestLoadWorkerConfigAppliesDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := LoadWorkerConfig(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		case "OPENFGA_API_URL":
			return "http://localhost:8080"
		case "OPENFGA_STORE_ID":
			return "store-id"
		case "OPENFGA_MODEL_ID":
			return "model-id"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("LoadWorkerConfig returned error: %v", err)
	}

	if cfg.TemporalNamespace != "" {
		t.Fatalf("TemporalNamespace = %q, want empty", cfg.TemporalNamespace)
	}
	if cfg.WorkerRunRoot != DefaultWorkerRunRoot {
		t.Fatalf("WorkerRunRoot = %q, want %q", cfg.WorkerRunRoot, DefaultWorkerRunRoot)
	}
	if cfg.ArtifactStore.Kind != ArtifactStoreFilesystem {
		t.Fatalf("ArtifactStore.Kind = %q, want %q", cfg.ArtifactStore.Kind, ArtifactStoreFilesystem)
	}
	if cfg.ArtifactStore.FilesystemRoot != DefaultArtifactStoreFilesystemRoot {
		t.Fatalf("ArtifactStore.FilesystemRoot = %q, want %q", cfg.ArtifactStore.FilesystemRoot, DefaultArtifactStoreFilesystemRoot)
	}
}

func TestLoadWorkerConfigRequiresTemporalAddress(t *testing.T) {
	t.Parallel()

	_, err := LoadWorkerConfig(func(string) string {
		return ""
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestLoadWorkerConfigRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	_, err := LoadWorkerConfig(func(key string) string {
		if key == "TEMPORAL_ADDRESS" {
			return "localhost:7233"
		}
		return ""
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestLoadAPIConfigAllowsEmptyCredentialEncryptionKey(t *testing.T) {
	t.Parallel()

	cfg, err := LoadAPIConfig(withValidSecurity(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		default:
			return ""
		}
	}))
	if err != nil {
		t.Fatalf("LoadAPIConfig returned error: %v", err)
	}
	if !cfg.CredentialEncryptionKey.Empty() {
		t.Fatal("CredentialEncryptionKey is not empty, want empty: credential encryption is optional")
	}
}

func TestLoadAPIConfigAcceptsValidCredentialEncryptionKey(t *testing.T) {
	t.Parallel()

	cfg, err := LoadAPIConfig(withValidSecurity(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		case "CREDENTIAL_ENCRYPTION_KEY":
			return "01234567890123456789012345678901"
		default:
			return ""
		}
	}))
	if err != nil {
		t.Fatalf("LoadAPIConfig returned error: %v", err)
	}
	if cfg.CredentialEncryptionKey.Value() != "01234567890123456789012345678901" {
		t.Fatalf("CredentialEncryptionKey.Value() = %q", cfg.CredentialEncryptionKey.Value())
	}
}

func TestLoadAPIConfigRejectsMalformedCredentialEncryptionKey(t *testing.T) {
	t.Parallel()

	_, err := LoadAPIConfig(withValidSecurity(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		case "CREDENTIAL_ENCRYPTION_KEY":
			return "too-short"
		default:
			return ""
		}
	}))
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "CREDENTIAL_ENCRYPTION_KEY") {
		t.Fatalf("error = %v, want ErrInvalidConfig mentioning CREDENTIAL_ENCRYPTION_KEY", err)
	}
}

func TestLoadWorkerConfigAcceptsValidCredentialEncryptionKey(t *testing.T) {
	t.Parallel()

	cfg, err := LoadWorkerConfig(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		case "OPENFGA_API_URL":
			return "http://localhost:8080"
		case "OPENFGA_STORE_ID":
			return "store-id"
		case "OPENFGA_MODEL_ID":
			return "model-id"
		case "CREDENTIAL_ENCRYPTION_KEY":
			return "01234567890123456789012345678901"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("LoadWorkerConfig returned error: %v", err)
	}
	if cfg.CredentialEncryptionKey.Value() != "01234567890123456789012345678901" {
		t.Fatalf("CredentialEncryptionKey.Value() = %q", cfg.CredentialEncryptionKey.Value())
	}
}

func TestLoadWorkerConfigRejectsMalformedCredentialEncryptionKey(t *testing.T) {
	t.Parallel()

	_, err := LoadWorkerConfig(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		case "OPENFGA_API_URL":
			return "http://localhost:8080"
		case "OPENFGA_STORE_ID":
			return "store-id"
		case "OPENFGA_MODEL_ID":
			return "model-id"
		case "CREDENTIAL_ENCRYPTION_KEY":
			return "too-short"
		default:
			return ""
		}
	})
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "CREDENTIAL_ENCRYPTION_KEY") {
		t.Fatalf("error = %v, want ErrInvalidConfig mentioning CREDENTIAL_ENCRYPTION_KEY", err)
	}
}

func TestLoadAPIConfigRejectsInvalidArtifactStoreKind(t *testing.T) {
	t.Parallel()

	_, err := LoadAPIConfig(withValidSecurity(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		case "ARTIFACT_STORE_KIND":
			return "tape-drive"
		default:
			return ""
		}
	}))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func assertArtifactStoreConfig(t *testing.T, cfg ArtifactStoreConfig) {
	t.Helper()

	if cfg.Kind != ArtifactStoreS3 {
		t.Fatalf("ArtifactStore.Kind = %q, want %q", cfg.Kind, ArtifactStoreS3)
	}
	if cfg.FilesystemRoot != "/var/lib/tflive/artifacts" {
		t.Fatalf("ArtifactStore.FilesystemRoot = %q, want /var/lib/tflive/artifacts", cfg.FilesystemRoot)
	}
	if cfg.S3.Bucket != "tflive-artifacts" {
		t.Fatalf("S3.Bucket = %q, want tflive-artifacts", cfg.S3.Bucket)
	}
	if cfg.S3.Region != "us-east-1" {
		t.Fatalf("S3.Region = %q, want us-east-1", cfg.S3.Region)
	}
	if cfg.S3.Endpoint != "https://s3.us-east-1.amazonaws.com" {
		t.Fatalf("S3.Endpoint = %q", cfg.S3.Endpoint)
	}
	if cfg.S3.AccessKeyID != "access-key" {
		t.Fatalf("S3.AccessKeyID = %q, want access-key", cfg.S3.AccessKeyID)
	}
	if cfg.S3.SecretAccessKey != "secret-key" {
		t.Fatalf("S3.SecretAccessKey = %q, want secret-key", cfg.S3.SecretAccessKey)
	}
	if !cfg.S3.ForcePathStyle {
		t.Fatal("S3.ForcePathStyle = false, want true")
	}
}

func withValidSecurity(getenv func(string) string) func(string) string {
	security := validSecurityValues()
	return func(name string) string {
		if value, ok := security[name]; ok {
			return value
		}
		return getenv(name)
	}
}

func TestLoadWorkerConfigReadsGitHubApp(t *testing.T) {
	t.Parallel()

	encoded, _ := testRSAPrivateKeyPEM(t)
	cfg, err := LoadWorkerConfig(withValidWorkerEnv(map[string]string{
		"GITHUB_APP_ID":          " 12345 ",
		"GITHUB_APP_PRIVATE_KEY": encoded,
	}))
	if err != nil {
		t.Fatalf("LoadWorkerConfig returned error: %v", err)
	}
	if !cfg.GitHubApp.Enabled() {
		t.Fatal("GitHubApp should be enabled")
	}
	if cfg.GitHubApp.AppID != "12345" {
		t.Fatalf("AppID = %q, want 12345", cfg.GitHubApp.AppID)
	}
}

// An unconfigured App is the supported default, not a misconfiguration: the
// deployment simply cannot reach private repositories.
func TestLoadWorkerConfigAllowsAbsentGitHubApp(t *testing.T) {
	t.Parallel()

	cfg, err := LoadWorkerConfig(withValidWorkerEnv(nil))
	if err != nil {
		t.Fatalf("LoadWorkerConfig returned error: %v", err)
	}
	if cfg.GitHubApp.Enabled() {
		t.Fatal("GitHubApp should be disabled")
	}
}

// Half a configuration is always a mistake, and one that would otherwise
// surface as a puzzling clone failure much later.
func TestLoadWorkerConfigRejectsPartialGitHubApp(t *testing.T) {
	t.Parallel()

	encoded, _ := testRSAPrivateKeyPEM(t)
	for name, env := range map[string]map[string]string{
		"id without key": {"GITHUB_APP_ID": "12345"},
		"key without id": {"GITHUB_APP_PRIVATE_KEY": encoded},
	} {
		if _, err := LoadWorkerConfig(withValidWorkerEnv(env)); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: error = %v, want ErrInvalidConfig", name, err)
		}
	}
}

func TestLoadWorkerConfigRejectsMalformedGitHubAppValues(t *testing.T) {
	t.Parallel()

	encoded, _ := testRSAPrivateKeyPEM(t)
	for name, env := range map[string]map[string]string{
		"non-numeric app id": {"GITHUB_APP_ID": "not-a-number", "GITHUB_APP_PRIVATE_KEY": encoded},
		"malformed key":      {"GITHUB_APP_ID": "12345", "GITHUB_APP_PRIVATE_KEY": "not-a-key"},
	} {
		if _, err := LoadWorkerConfig(withValidWorkerEnv(env)); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: error = %v, want ErrInvalidConfig", name, err)
		}
	}
}

// The key must not be recoverable from a config value printed in a log.
func TestGitHubAppConfigRedactsPrivateKey(t *testing.T) {
	t.Parallel()

	encoded, _ := testRSAPrivateKeyPEM(t)
	cfg := GitHubAppConfig{AppID: "12345", PrivateKey: newSecret(encoded)}

	if rendered := fmt.Sprintf("%v", cfg.PrivateKey); rendered != "[REDACTED]" {
		t.Fatalf("PrivateKey = %q, want [REDACTED]", rendered)
	}
}

func testRSAPrivateKeyPEM(t *testing.T) (string, *rsa.PrivateKey) {
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

// withValidWorkerEnv returns a getenv covering the worker's required settings,
// with overrides layered on top.
func withValidWorkerEnv(overrides map[string]string) func(string) string {
	base := map[string]string{
		"DATABASE_URL":     "postgres://user:pass@localhost:5432/db?sslmode=disable",
		"TEMPORAL_ADDRESS": "localhost:7233",
		"OPENFGA_API_URL":  "http://localhost:8080",
		"OPENFGA_STORE_ID": "store-id",
		"OPENFGA_MODEL_ID": "model-id",
	}
	return func(key string) string {
		if value, ok := overrides[key]; ok {
			return value
		}
		return base[key]
	}
}
