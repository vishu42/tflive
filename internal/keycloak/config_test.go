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
	if got, want := cfg.WebClientID, "tflive-web"; got != want {
		t.Fatalf("WebClientID = %q, want %q", got, want)
	}
	if got, want := cfg.APIClientID, "tflive-api"; got != want {
		t.Fatalf("APIClientID = %q, want %q", got, want)
	}
	if got, want := cfg.RedirectURIs, []string{"http://localhost:5173/", "http://127.0.0.1:5173/"}; !equalStrings(got, want) {
		t.Fatalf("RedirectURIs = %#v, want %#v", got, want)
	}
	if got, want := cfg.WebOrigins, []string{"http://localhost:5173", "http://127.0.0.1:5173"}; !equalStrings(got, want) {
		t.Fatalf("WebOrigins = %#v, want %#v", got, want)
	}
	if cfg.PlatformAdminEmail != "tflive-platform-admin@local.test" || cfg.PlatformAdminFirstName != "tflive" || cfg.PlatformAdminLastName != "Platform Administrator" {
		t.Fatalf("platform admin profile = email %q, first %q, last %q", cfg.PlatformAdminEmail, cfg.PlatformAdminFirstName, cfg.PlatformAdminLastName)
	}
	if got, want := cfg.HTTPTimeout, 10*time.Second; got != want {
		t.Fatalf("HTTPTimeout = %s, want %s", got, want)
	}
}

func TestLoadConfigUsesNonSecretDefaults(t *testing.T) {
	t.Parallel()

	env := validConfigEnv()
	delete(env, "KEYCLOAK_ADMIN_REALM")
	delete(env, "KEYCLOAK_REALM")
	delete(env, "KEYCLOAK_WEB_CLIENT_ID")
	delete(env, "KEYCLOAK_API_CLIENT_ID")
	delete(env, "KEYCLOAK_HTTP_TIMEOUT")

	cfg, err := LoadConfig(mapEnv(env))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.AdminRealm != "master" || cfg.Realm != "tflive" {
		t.Fatalf("realm defaults = admin %q, product %q", cfg.AdminRealm, cfg.Realm)
	}
	if cfg.WebClientID != "tflive-web" || cfg.APIClientID != "tflive-api" {
		t.Fatalf("client defaults = web %q, api %q", cfg.WebClientID, cfg.APIClientID)
	}
	if cfg.HTTPTimeout != 10*time.Second {
		t.Fatalf("HTTPTimeout = %s, want 10s", cfg.HTTPTimeout)
	}
}

func TestLoadConfigRequiresRuntimeSecretsAndEndpoints(t *testing.T) {
	t.Parallel()

	required := []string{
		"KEYCLOAK_ADMIN_URL",
		"KEYCLOAK_ADMIN_USERNAME",
		"KEYCLOAK_ADMIN_PASSWORD",
		"KEYCLOAK_WEB_REDIRECT_URIS",
		"KEYCLOAK_WEB_ORIGINS",
		"KEYCLOAK_PLATFORM_ADMIN_USERNAME",
		"KEYCLOAK_PLATFORM_ADMIN_PASSWORD",
		"KEYCLOAK_PLATFORM_ADMIN_EMAIL",
		"KEYCLOAK_PLATFORM_ADMIN_FIRST_NAME",
		"KEYCLOAK_PLATFORM_ADMIN_LAST_NAME",
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
		{name: "wildcard redirect", key: "KEYCLOAK_WEB_REDIRECT_URIS", value: "http://localhost:5173/*", wantErr: "must not contain wildcards"},
		{name: "redirect credentials", key: "KEYCLOAK_WEB_REDIRECT_URIS", value: "https://user:pass@app.example.com/callback", wantErr: "must not contain user information"},
		{name: "redirect fragment", key: "KEYCLOAK_WEB_REDIRECT_URIS", value: "https://app.example.com/callback#token", wantErr: "must not contain a query or fragment"},
		{name: "public insecure redirect", key: "KEYCLOAK_WEB_REDIRECT_URIS", value: "http://app.example.com/callback", wantErr: "plain HTTP is allowed only for loopback hosts"},
		{name: "wildcard origin", key: "KEYCLOAK_WEB_ORIGINS", value: "+", wantErr: "must not contain wildcards"},
		{name: "origin has path", key: "KEYCLOAK_WEB_ORIGINS", value: "https://app.example.com/path", wantErr: "origin must not contain a path"},
		{name: "duplicate redirect", key: "KEYCLOAK_WEB_REDIRECT_URIS", value: "http://localhost:5173/, http://localhost:5173/", wantErr: "contains duplicate value"},
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
		"KEYCLOAK_ADMIN_URL":                 "http://keycloak:8080/",
		"KEYCLOAK_ADMIN_REALM":               "master",
		"KEYCLOAK_ADMIN_USERNAME":            "tflive-admin",
		"KEYCLOAK_ADMIN_PASSWORD":            "master-local-only-secret",
		"KEYCLOAK_REALM":                     "tflive",
		"KEYCLOAK_WEB_CLIENT_ID":             "tflive-web",
		"KEYCLOAK_API_CLIENT_ID":             "tflive-api",
		"KEYCLOAK_WEB_REDIRECT_URIS":         "http://localhost:5173/, http://127.0.0.1:5173/",
		"KEYCLOAK_WEB_ORIGINS":               "http://localhost:5173, http://127.0.0.1:5173",
		"KEYCLOAK_PLATFORM_ADMIN_USERNAME":   "tflive-platform-admin",
		"KEYCLOAK_PLATFORM_ADMIN_PASSWORD":   "platform-local-only-secret",
		"KEYCLOAK_PLATFORM_ADMIN_EMAIL":      "tflive-platform-admin@local.test",
		"KEYCLOAK_PLATFORM_ADMIN_FIRST_NAME": "tflive",
		"KEYCLOAK_PLATFORM_ADMIN_LAST_NAME":  "Platform Administrator",
		"KEYCLOAK_HTTP_TIMEOUT":              "10s",
	}
}

func mapEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
