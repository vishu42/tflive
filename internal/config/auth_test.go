package config

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
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
			if cfg.OIDC.ClientID != "tflive-api" {
				t.Fatalf("ClientID = %q, want tflive-api", cfg.OIDC.ClientID)
			}
			// The embedded server has one setting left: the store to adopt.
			// There is no URL to dial, no token to present, and no identifier
			// for an operator to record between two startup phases.
			if cfg.OpenFGA.StoreName != DefaultOpenFGAStoreName {
				t.Fatalf("StoreName = %q, want %q", cfg.OpenFGA.StoreName, DefaultOpenFGAStoreName)
			}
		})
	}
}

func TestLoadSecurityConfigProductionAndSecretFormatting(t *testing.T) {
	t.Parallel()

	values := validSecurityValues()
	values["TFLIVE_ENVIRONMENT"] = "production"
	values["TFLIVE_PUBLIC_URL"] = "https://app.example.com"
	values["OIDC_ISSUER_URL"] = "https://id.example.com/realms/tflive"

	cfg, err := loadSecurityConfig(mapConfigEnv(values))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	if cfg.Mode != RuntimeProduction {
		t.Fatalf("Mode = %q, want %q", cfg.Mode, RuntimeProduction)
	}
	// OpenFGA no longer carries a secret: there is no service to authenticate
	// to. The redaction this pins is the remaining one, on the client secret.
	formatted := fmt.Sprintf("%s\n%v\n%+v\n%#v", cfg.OIDC.ClientSecret, cfg.OpenFGA, cfg.OpenFGA, cfg)
	if strings.Contains(formatted, "directory-reader-secret-sentinel") {
		t.Fatalf("formatted configuration leaked directory reader secret: %s", formatted)
	}
	if !strings.Contains(formatted, "[REDACTED]") {
		t.Fatalf("formatted configuration did not show redaction marker: %s", formatted)
	}
}

func TestSecretIsRedactedInEveryFormatVerb(t *testing.T) {
	t.Parallel()

	secret := newSecret("my-secret-value")
	if got := secret.String(); got != "[REDACTED]" {
		t.Fatalf("Secret.String() = %q, want [REDACTED]", got)
	}
	if got := fmt.Sprintf("%v", secret); got != "[REDACTED]" {
		t.Fatalf("Secret via %%v = %q, want [REDACTED]", got)
	}
	if got := fmt.Sprintf("%+v", secret); got != "[REDACTED]" {
		t.Fatalf("Secret via %%+v = %q, want [REDACTED]", got)
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
		{name: "unsafe store name", key: "OPENFGA_STORE_NAME", value: "store name", want: "OPENFGA_STORE_NAME must not contain whitespace or control characters"},
		{name: "unknown environment", key: "TFLIVE_ENVIRONMENT", value: "staging", want: "TFLIVE_ENVIRONMENT must be development or production"},
		{name: "missing tenant", key: "TFLIVE_TENANT_ID", value: "", want: "TFLIVE_TENANT_ID is required"},
		{name: "tenant prefix", key: "TFLIVE_TENANT_ID", value: "-tenant", want: "TFLIVE_TENANT_ID must start"},
		{name: "tenant slash", key: "TFLIVE_TENANT_ID", value: "tenant/123", want: "TFLIVE_TENANT_ID must start"},
		{name: "tenant too long", key: "TFLIVE_TENANT_ID", value: strings.Repeat("a", 129), want: "TFLIVE_TENANT_ID must start"},
		// An absent issuer is no longer an error by itself -- it is how a
		// local-only deployment opts out of OIDC (#211). It is an error only
		// when client credentials name a provider that would never be
		// contacted, which is what the rest of this fixture supplies.
		{name: "client credentials without issuer", key: "OIDC_ISSUER_URL", value: "", want: "OIDC_CLIENT_ID and OIDC_CLIENT_SECRET require OIDC_ISSUER_URL"},
		{name: "relative issuer", key: "OIDC_ISSUER_URL", value: "/realms/tflive", want: "OIDC_ISSUER_URL must be an absolute HTTP or HTTPS URL"},
		{name: "issuer user info", key: "OIDC_ISSUER_URL", value: "https://client:client-secret-sentinel@id.example.com/realms/tflive", want: "OIDC_ISSUER_URL must not include user information"},
		{name: "issuer query", key: "OIDC_ISSUER_URL", value: "https://id.example.com/realms/tflive?x=1", want: "OIDC_ISSUER_URL must not include a query"},
		{name: "issuer fragment", key: "OIDC_ISSUER_URL", value: "https://id.example.com/realms/tflive#keys", want: "OIDC_ISSUER_URL must not include a fragment"},
		{name: "missing client id", key: "OIDC_CLIENT_ID", value: "", want: "OIDC_CLIENT_ID is required"},
		{name: "client id whitespace", key: "OIDC_CLIENT_ID", value: "tflive api", want: "OIDC_CLIENT_ID must not contain whitespace or control characters"},
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
		{name: "HTTP public URL", mutate: func(values map[string]string) { values["TFLIVE_PUBLIC_URL"] = "http://app.example.com" }, want: "TFLIVE_PUBLIC_URL must use HTTPS in production"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := validSecurityValues()
			values["TFLIVE_ENVIRONMENT"] = "production"
			values["TFLIVE_PUBLIC_URL"] = "https://app.example.com"
			values["OIDC_ISSUER_URL"] = "https://id.example.com/realms/tflive"
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

func TestLoadSecurityConfigRequiresOIDCClientCredentials(t *testing.T) {
	for _, name := range []string{"OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "TFLIVE_PUBLIC_URL", "SESSION_ENCRYPTION_KEY"} {
		t.Run(name, func(t *testing.T) {
			env := validSecurityValues()
			delete(env, name)
			if _, err := loadSecurityConfig(mapConfigEnv(env)); err == nil {
				t.Fatalf("loadSecurityConfig accepted a missing %s", name)
			}
		})
	}
}

func TestLoadSecurityConfigRejectsRetiredOIDCAudience(t *testing.T) {
	// OIDC_AUDIENCE changed meaning from a resource identifier to a client ID.
	// Silently accepting the old name would validate a value nobody re-checked.
	env := validSecurityValues()
	delete(env, "OIDC_CLIENT_ID")
	env["OIDC_AUDIENCE"] = "tflive-api"
	if _, err := loadSecurityConfig(mapConfigEnv(env)); err == nil {
		t.Fatal("loadSecurityConfig accepted the retired OIDC_AUDIENCE")
	}
}

func TestLoadSecurityConfigReadsPublicURLAndSessionKey(t *testing.T) {
	env := validSecurityValues()
	cfg, err := loadSecurityConfig(mapConfigEnv(env))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	if cfg.PublicURL == nil || cfg.PublicURL.String() != "http://localhost:5173" {
		t.Fatalf("PublicURL = %v", cfg.PublicURL)
	}
	if cfg.OIDC.ClientID != "tflive-api" {
		t.Fatalf("ClientID = %q", cfg.OIDC.ClientID)
	}
	if cfg.OIDC.ClientSecret.Value() != "oidc-client-secret" {
		t.Fatalf("ClientSecret = %q", cfg.OIDC.ClientSecret.Value())
	}
	if cfg.SessionEncryptionKey.Value() != "01234567890123456789012345678901" {
		t.Fatalf("SessionEncryptionKey = %q", cfg.SessionEncryptionKey.Value())
	}
}

func TestSecurityConfigStringRedactsSecrets(t *testing.T) {
	cfg, err := loadSecurityConfig(mapConfigEnv(validSecurityValues()))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	rendered := cfg.String()
	for _, secret := range []string{"oidc-client-secret", "01234567890123456789012345678901"} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("SecurityConfig.String() leaked %q: %s", secret, rendered)
		}
	}
}

func TestSessionTTLDefaults(t *testing.T) {
	cfg := loadValidSecurityConfig(t, nil)
	if cfg.SessionAbsoluteTTL != 8*time.Hour {
		t.Fatalf("SessionAbsoluteTTL = %v, want 8h", cfg.SessionAbsoluteTTL)
	}
	if cfg.SessionIdleTTL != time.Hour {
		t.Fatalf("SessionIdleTTL = %v, want 1h", cfg.SessionIdleTTL)
	}
}

func TestSessionTTLOverrides(t *testing.T) {
	cfg := loadValidSecurityConfig(t, map[string]string{
		"TFLIVE_SESSION_ABSOLUTE_TTL": "2h",
		"TFLIVE_SESSION_IDLE_TTL":     "15m",
	})
	if cfg.SessionAbsoluteTTL != 2*time.Hour {
		t.Fatalf("SessionAbsoluteTTL = %v, want 2h", cfg.SessionAbsoluteTTL)
	}
	if cfg.SessionIdleTTL != 15*time.Minute {
		t.Fatalf("SessionIdleTTL = %v, want 15m", cfg.SessionIdleTTL)
	}
}

func TestSessionTTLRejectsNonPositiveAndInverted(t *testing.T) {
	tests := map[string]map[string]string{
		"zero absolute":        {"TFLIVE_SESSION_ABSOLUTE_TTL": "0s"},
		"negative idle":        {"TFLIVE_SESSION_IDLE_TTL": "-1m"},
		"unparseable":          {"TFLIVE_SESSION_IDLE_TTL": "soon"},
		"idle longer than cap": {"TFLIVE_SESSION_ABSOLUTE_TTL": "1h", "TFLIVE_SESSION_IDLE_TTL": "2h"},
		// At or below the touch interval, LastSeenAt is never written back
		// before IsLive expires the session, so it can never slide.
		"idle equal to touch interval": {"TFLIVE_SESSION_ABSOLUTE_TTL": "1h", "TFLIVE_SESSION_IDLE_TTL": authn.SessionTouchInterval.String()},
		"idle below touch interval":    {"TFLIVE_SESSION_ABSOLUTE_TTL": "1h", "TFLIVE_SESSION_IDLE_TTL": "1m"},
	}
	for name, env := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := loadSecurityConfigWith(t, env); err == nil {
				t.Fatal("want an error, got none")
			}
		})
	}
}

// loadValidSecurityConfig builds a complete valid environment, applies
// overrides, and fails the test on error.
func loadValidSecurityConfig(t *testing.T, overrides map[string]string) SecurityConfig {
	t.Helper()
	cfg, err := loadSecurityConfigWith(t, overrides)
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	return cfg
}

// loadSecurityConfigWith builds a complete valid environment, applies
// overrides, and returns whatever loadSecurityConfig does with it.
func loadSecurityConfigWith(t *testing.T, overrides map[string]string) (SecurityConfig, error) {
	t.Helper()
	values := validSecurityValues()
	for key, value := range overrides {
		values[key] = value
	}
	return loadSecurityConfig(mapConfigEnv(values))
}

func validSecurityValues() map[string]string {
	return map[string]string{
		"TFLIVE_ENVIRONMENT":     "development",
		"TFLIVE_TENANT_ID":       "tenant_123",
		"TFLIVE_PUBLIC_URL":      "http://localhost:5173",
		"OIDC_ISSUER_URL":        "http://localhost:8082/realms/tflive",
		"OIDC_CLIENT_ID":         "tflive-api",
		"OIDC_CLIENT_SECRET":     "oidc-client-secret",
		"SESSION_ENCRYPTION_KEY": "01234567890123456789012345678901",
		"TFLIVE_ROOT_PASSWORD":   "root-local-only",
	}
}

func mapConfigEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
