package config

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLoadSecurityConfigDevelopmentModes(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"", "development"} {
		mode := mode
		t.Run(fmt.Sprintf("mode_%q", mode), func(t *testing.T) {
			t.Parallel()

			values := validSecurityValues()
			values["TFLIVE_ENVIRONMENT"] = mode
			cfg, err := loadSecurityConfig(mapConfigEnv(values))
			if err != nil {
				t.Fatalf("loadSecurityConfig returned error: %v", err)
			}
			if cfg.Mode != RuntimeDevelopment {
				t.Fatalf("Mode = %q, want %q", cfg.Mode, RuntimeDevelopment)
			}
			if cfg.TenantID != "tenant_123" {
				t.Fatalf("TenantID = %q, want tenant_123", cfg.TenantID)
			}
			if got := cfg.OIDC.IssuerURL.String(); got != "http://localhost:8082/realms/tflive" {
				t.Fatalf("IssuerURL = %q", got)
			}
			if cfg.OIDC.Audience != "tflive-api" {
				t.Fatalf("Audience = %q, want tflive-api", cfg.OIDC.Audience)
			}
			if got := cfg.OpenFGA.APIURL.String(); got != "http://localhost:8080" {
				t.Fatalf("OpenFGA APIURL = %q", got)
			}
			if cfg.OpenFGA.StoreID != "store-id" || cfg.OpenFGA.ModelID != "model-id" {
				t.Fatalf("OpenFGA IDs = %q/%q", cfg.OpenFGA.StoreID, cfg.OpenFGA.ModelID)
			}
			if !cfg.OpenFGA.APIToken.Empty() {
				t.Fatal("development API token is not empty")
			}
			if cfg.OpenFGA.RequestTimeout != 10*time.Second {
				t.Fatalf("RequestTimeout = %s, want 10s", cfg.OpenFGA.RequestTimeout)
			}
		})
	}
}

func TestLoadSecurityConfigProductionAndSecretFormatting(t *testing.T) {
	t.Parallel()

	values := validSecurityValues()
	values["TFLIVE_ENVIRONMENT"] = "production"
	values["OIDC_ISSUER_URL"] = "https://id.example.com/realms/tflive"
	values["OPENFGA_API_URL"] = "https://openfga.example.com"
	values["OPENFGA_API_TOKEN"] = "openfga-token-sentinel"

	cfg, err := loadSecurityConfig(mapConfigEnv(values))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	if cfg.Mode != RuntimeProduction {
		t.Fatalf("Mode = %q, want %q", cfg.Mode, RuntimeProduction)
	}
	if got := cfg.OpenFGA.APIToken.Value(); got != "openfga-token-sentinel" {
		t.Fatalf("APIToken.Value() = %q", got)
	}

	formatted := fmt.Sprintf("%s\n%v\n%+v\n%#v", cfg.OpenFGA.APIToken, cfg.OpenFGA, cfg.OpenFGA, cfg)
	if strings.Contains(formatted, "openfga-token-sentinel") {
		t.Fatalf("formatted configuration leaked token: %s", formatted)
	}
	if !strings.Contains(formatted, "[REDACTED]") {
		t.Fatalf("formatted configuration did not show redaction marker: %s", formatted)
	}
}

func TestLoadSecurityConfigRejectsMissingAndMalformedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "unknown environment", key: "TFLIVE_ENVIRONMENT", value: "staging", want: "TFLIVE_ENVIRONMENT must be development or production"},
		{name: "missing tenant", key: "TFLIVE_TENANT_ID", value: "", want: "TFLIVE_TENANT_ID is required"},
		{name: "tenant prefix", key: "TFLIVE_TENANT_ID", value: "-tenant", want: "TFLIVE_TENANT_ID must start"},
		{name: "tenant slash", key: "TFLIVE_TENANT_ID", value: "tenant/123", want: "TFLIVE_TENANT_ID must start"},
		{name: "tenant too long", key: "TFLIVE_TENANT_ID", value: strings.Repeat("a", 129), want: "TFLIVE_TENANT_ID must start"},
		{name: "missing issuer", key: "OIDC_ISSUER_URL", value: "", want: "OIDC_ISSUER_URL is required"},
		{name: "relative issuer", key: "OIDC_ISSUER_URL", value: "/realms/tflive", want: "OIDC_ISSUER_URL must be an absolute HTTP or HTTPS URL"},
		{name: "issuer user info", key: "OIDC_ISSUER_URL", value: "https://client:client-secret-sentinel@id.example.com/realms/tflive", want: "OIDC_ISSUER_URL must not include user information"},
		{name: "issuer query", key: "OIDC_ISSUER_URL", value: "https://id.example.com/realms/tflive?x=1", want: "OIDC_ISSUER_URL must not include a query"},
		{name: "issuer fragment", key: "OIDC_ISSUER_URL", value: "https://id.example.com/realms/tflive#keys", want: "OIDC_ISSUER_URL must not include a fragment"},
		{name: "missing audience", key: "OIDC_AUDIENCE", value: "", want: "OIDC_AUDIENCE is required"},
		{name: "audience whitespace", key: "OIDC_AUDIENCE", value: "tflive api", want: "OIDC_AUDIENCE must not contain whitespace or control characters"},
		{name: "missing OpenFGA URL", key: "OPENFGA_API_URL", value: "", want: "OPENFGA_API_URL is required"},
		{name: "OpenFGA scheme", key: "OPENFGA_API_URL", value: "ftp://openfga.example.com", want: "OPENFGA_API_URL must be an absolute HTTP or HTTPS URL"},
		{name: "OpenFGA user info", key: "OPENFGA_API_URL", value: "https://user:api-url-secret-sentinel@openfga.example.com", want: "OPENFGA_API_URL must not include user information"},
		{name: "OpenFGA query", key: "OPENFGA_API_URL", value: "https://openfga.example.com?x=1", want: "OPENFGA_API_URL must not include a query"},
		{name: "OpenFGA fragment", key: "OPENFGA_API_URL", value: "https://openfga.example.com#api", want: "OPENFGA_API_URL must not include a fragment"},
		{name: "missing store ID", key: "OPENFGA_STORE_ID", value: "", want: "OPENFGA_STORE_ID is required"},
		{name: "unsafe store ID", key: "OPENFGA_STORE_ID", value: "store id", want: "OPENFGA_STORE_ID must not contain whitespace or control characters"},
		{name: "missing model ID", key: "OPENFGA_MODEL_ID", value: "", want: "OPENFGA_MODEL_ID is required"},
		{name: "unsafe model ID", key: "OPENFGA_MODEL_ID", value: "model\nid", want: "OPENFGA_MODEL_ID must not contain whitespace or control characters"},
		{name: "malformed timeout", key: "OPENFGA_HTTP_TIMEOUT", value: "forever", want: "OPENFGA_HTTP_TIMEOUT must be a positive duration"},
		{name: "zero timeout", key: "OPENFGA_HTTP_TIMEOUT", value: "0s", want: "OPENFGA_HTTP_TIMEOUT must be a positive duration"},
		{name: "negative timeout", key: "OPENFGA_HTTP_TIMEOUT", value: "-1s", want: "OPENFGA_HTTP_TIMEOUT must be a positive duration"},
		{name: "unsafe token", key: "OPENFGA_API_TOKEN", value: "api token sentinel", want: "OPENFGA_API_TOKEN must not contain whitespace or control characters"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := validSecurityValues()
			values[test.key] = test.value
			_, err := loadSecurityConfig(mapConfigEnv(values))
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
			for _, secret := range []string{"client-secret-sentinel", "api-url-secret-sentinel", "api token sentinel"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestLoadSecurityConfigRejectsInsecureProductionValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{name: "HTTP issuer", mutate: func(values map[string]string) { values["OIDC_ISSUER_URL"] = "http://id.example.com/realms/tflive" }, want: "OIDC_ISSUER_URL must use HTTPS in production"},
		{name: "HTTP OpenFGA", mutate: func(values map[string]string) { values["OPENFGA_API_URL"] = "http://openfga.example.com" }, want: "OPENFGA_API_URL must use HTTPS in production"},
		{name: "missing OpenFGA token", mutate: func(values map[string]string) { values["OPENFGA_API_TOKEN"] = "" }, want: "OPENFGA_API_TOKEN is required in production"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := validSecurityValues()
			values["TFLIVE_ENVIRONMENT"] = "production"
			values["OIDC_ISSUER_URL"] = "https://id.example.com/realms/tflive"
			values["OPENFGA_API_URL"] = "https://openfga.example.com"
			values["OPENFGA_API_TOKEN"] = "production-token-sentinel"
			test.mutate(values)
			_, err := loadSecurityConfig(mapConfigEnv(values))
			if !errors.Is(err, ErrInvalidConfig) || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want ErrInvalidConfig containing %q", err, test.want)
			}
			if strings.Contains(err.Error(), "production-token-sentinel") {
				t.Fatalf("error leaked token: %v", err)
			}
		})
	}
}

func validSecurityValues() map[string]string {
	return map[string]string{
		"TFLIVE_ENVIRONMENT":   "development",
		"TFLIVE_TENANT_ID":     "tenant_123",
		"OIDC_ISSUER_URL":      "http://localhost:8082/realms/tflive",
		"OIDC_AUDIENCE":        "tflive-api",
		"OPENFGA_API_URL":      "http://localhost:8080",
		"OPENFGA_STORE_ID":     "store-id",
		"OPENFGA_MODEL_ID":     "model-id",
		"OPENFGA_API_TOKEN":    "",
		"OPENFGA_HTTP_TIMEOUT": "",
	}
}

func mapConfigEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
