package keycloak

import (
	"strings"
	"testing"
	"time"
)

func TestLoadConfigReadsValidLocalSettings(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(mapEnv(validConfigEnv()))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if got, want := cfg.AdminURL.String(), "http://keycloak:8080"; got != want {
		t.Fatalf("AdminURL = %q, want %q", got, want)
	}
	if got, want := cfg.AdminRealm, "master"; got != want {
		t.Fatalf("AdminRealm = %q, want %q", got, want)
	}
	if got, want := cfg.Realm, "tflive"; got != want {
		t.Fatalf("Realm = %q, want %q", got, want)
	}
	if got, want := cfg.APIClientID, "tflive-api"; got != want {
		t.Fatalf("APIClientID = %q, want %q", got, want)
	}
	if got, want := cfg.APIClientSecret, "oidc-client-secret"; got != want {
		t.Fatalf("APIClientSecret = %q, want %q", got, want)
	}
	if got, want := cfg.CallbackURI, "http://localhost:5173/v1/auth/callback"; got != want {
		t.Fatalf("CallbackURI = %q, want %q", got, want)
	}
	if got, want := cfg.PostLogoutRedirectURI, "http://localhost:5173/"; got != want {
		t.Fatalf("PostLogoutRedirectURI = %q, want %q", got, want)
	}
	if got, want := cfg.BackchannelLogoutURI, "http://localhost:5173/v1/auth/backchannel-logout"; got != want {
		t.Fatalf("BackchannelLogoutURI = %q, want %q", got, want)
	}
	if cfg.PlatformAdminEmail != "tflive-platform-admin@local.test" || cfg.PlatformAdminFirstName != "tflive" || cfg.PlatformAdminLastName != "Platform Administrator" {
		t.Fatalf("platform admin profile = email %q, first %q, last %q", cfg.PlatformAdminEmail, cfg.PlatformAdminFirstName, cfg.PlatformAdminLastName)
	}
	if got, want := cfg.HTTPTimeout, 10*time.Second; got != want {
		t.Fatalf("HTTPTimeout = %s, want %s", got, want)
	}
	if got, want := cfg.DirectoryReaderClientSecret, "dir-reader-secret"; got != want {
		t.Fatalf("DirectoryReaderClientSecret = %q, want %q", got, want)
	}
}

func TestLoadConfigUsesNonSecretDefaults(t *testing.T) {
	t.Parallel()

	env := validConfigEnv()
	delete(env, "KEYCLOAK_ADMIN_REALM")
	delete(env, "KEYCLOAK_REALM")
	delete(env, "KEYCLOAK_API_CLIENT_ID")
	delete(env, "KEYCLOAK_HTTP_TIMEOUT")

	cfg, err := LoadConfig(mapEnv(env))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.AdminRealm != "master" || cfg.Realm != "tflive" {
		t.Fatalf("realm defaults = admin %q, product %q", cfg.AdminRealm, cfg.Realm)
	}
	if cfg.APIClientID != "tflive-api" {
		t.Fatalf("client default = api %q", cfg.APIClientID)
	}
	if cfg.HTTPTimeout != 10*time.Second {
		t.Fatalf("HTTPTimeout = %s, want 10s", cfg.HTTPTimeout)
	}
}

func TestLoadConfigDerivesCallbackFromPublicURL(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(mapEnv(validConfigEnv()))
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if cfg.CallbackURI != "http://localhost:5173/v1/auth/callback" {
		t.Fatalf("CallbackURI = %q", cfg.CallbackURI)
	}
	if cfg.PostLogoutRedirectURI != "http://localhost:5173/" {
		t.Fatalf("PostLogoutRedirectURI = %q", cfg.PostLogoutRedirectURI)
	}
}

func TestLoadConfigRequiresPublicURLAndClientSecret(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"TFLIVE_PUBLIC_URL", "OIDC_CLIENT_SECRET"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := validConfigEnv()
			delete(env, name)
			if _, err := LoadConfig(mapEnv(env)); err == nil {
				t.Fatalf("LoadConfig accepted a missing %s", name)
			}
		})
	}
}

func TestLoadConfigRequiresRuntimeSecretsAndEndpoints(t *testing.T) {
	t.Parallel()

	required := []string{
		"KEYCLOAK_ADMIN_URL",
		"KEYCLOAK_ADMIN_USERNAME",
		"KEYCLOAK_ADMIN_PASSWORD",
		"TFLIVE_PUBLIC_URL",
		"OIDC_CLIENT_SECRET",
		"KEYCLOAK_PLATFORM_ADMIN_USERNAME",
		"KEYCLOAK_PLATFORM_ADMIN_PASSWORD",
		"KEYCLOAK_PLATFORM_ADMIN_EMAIL",
		"KEYCLOAK_PLATFORM_ADMIN_FIRST_NAME",
		"KEYCLOAK_PLATFORM_ADMIN_LAST_NAME",
		"KEYCLOAK_DIRECTORY_READER_CLIENT_SECRET",
	}
	for _, name := range required {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := validConfigEnv()
			delete(env, name)

			_, err := LoadConfig(mapEnv(env))
			if err == nil || !strings.Contains(err.Error(), name+" is required") {
				t.Fatalf("LoadConfig() error = %v, want required-field error", err)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidSecuritySettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		value   string
		wantErr string
	}{
		{name: "admin URL is relative", key: "KEYCLOAK_ADMIN_URL", value: "/keycloak", wantErr: "absolute http or https URL"},
		{name: "admin URL has credentials", key: "KEYCLOAK_ADMIN_URL", value: "https://admin:secret@keycloak.example.com", wantErr: "must not contain user information"},
		{name: "admin URL has query", key: "KEYCLOAK_ADMIN_URL", value: "https://keycloak.example.com?token=x", wantErr: "must not contain a query or fragment"},
		{name: "public URL is relative", key: "TFLIVE_PUBLIC_URL", value: "/app", wantErr: "absolute http or https URL"},
		{name: "public URL has credentials", key: "TFLIVE_PUBLIC_URL", value: "https://user:pass@app.example.com", wantErr: "must not contain user information"},
		{name: "public URL has query", key: "TFLIVE_PUBLIC_URL", value: "https://app.example.com?token=x", wantErr: "must not contain a query or fragment"},
		{name: "invalid timeout", key: "KEYCLOAK_HTTP_TIMEOUT", value: "0s", wantErr: "must be greater than zero"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := validConfigEnv()
			env[tt.key] = tt.value

			_, err := LoadConfig(mapEnv(env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadConfig() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigRequiresDistinctAdminUsers(t *testing.T) {
	t.Parallel()

	env := validConfigEnv()
	env["KEYCLOAK_PLATFORM_ADMIN_USERNAME"] = env["KEYCLOAK_ADMIN_USERNAME"]

	_, err := LoadConfig(mapEnv(env))
	if err == nil || !strings.Contains(err.Error(), "must identify a different user") {
		t.Fatalf("LoadConfig() error = %v, want distinct-user error", err)
	}
}

func validConfigEnv() map[string]string {
	return map[string]string{
		"TFLIVE_ENVIRONMENT":                      "production",
		"KEYCLOAK_ADMIN_URL":                      "http://keycloak:8080/",
		"KEYCLOAK_ADMIN_REALM":                    "master",
		"KEYCLOAK_ADMIN_USERNAME":                 "tflive-admin",
		"KEYCLOAK_ADMIN_PASSWORD":                 "master-local-only-secret",
		"KEYCLOAK_REALM":                          "tflive",
		"KEYCLOAK_API_CLIENT_ID":                  "tflive-api",
		"TFLIVE_PUBLIC_URL":                       "http://localhost:5173/",
		"OIDC_CLIENT_SECRET":                      "oidc-client-secret",
		"KEYCLOAK_PLATFORM_ADMIN_USERNAME":        "tflive-platform-admin",
		"KEYCLOAK_PLATFORM_ADMIN_PASSWORD":        "platform-local-only-secret",
		"KEYCLOAK_PLATFORM_ADMIN_EMAIL":           "tflive-platform-admin@local.test",
		"KEYCLOAK_PLATFORM_ADMIN_FIRST_NAME":      "tflive",
		"KEYCLOAK_PLATFORM_ADMIN_LAST_NAME":       "Platform Administrator",
		"KEYCLOAK_DIRECTORY_READER_CLIENT_SECRET": "dir-reader-secret",
		"KEYCLOAK_HTTP_TIMEOUT":                   "10s",
	}
}

func mapEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
