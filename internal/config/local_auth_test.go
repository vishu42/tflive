package config

import (
	"strings"
	"testing"
)

// localOnlyValues is a deployment with no IdP at all — the POC, demo, or test
// case #211 exists to make possible.
func localOnlyValues() map[string]string {
	values := validSecurityValues()
	delete(values, "OIDC_ISSUER_URL")
	delete(values, "OIDC_CLIENT_ID")
	delete(values, "OIDC_CLIENT_SECRET")
	values["TFLIVE_LOCAL_AUTH_ENABLED"] = "true"
	return values
}

func TestLoadSecurityConfigDefaultsLocalAuthOff(t *testing.T) {
	t.Parallel()

	cfg, err := loadSecurityConfig(mapConfigEnv(validSecurityValues()))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	if cfg.LocalAuthEnabled {
		t.Fatal("LocalAuthEnabled is true without TFLIVE_LOCAL_AUTH_ENABLED being set")
	}
}

func TestLoadSecurityConfigEnablesLocalAuth(t *testing.T) {
	t.Parallel()

	values := validSecurityValues()
	values["TFLIVE_LOCAL_AUTH_ENABLED"] = "true"
	cfg, err := loadSecurityConfig(mapConfigEnv(values))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	if !cfg.LocalAuthEnabled {
		t.Fatal("LocalAuthEnabled is false with TFLIVE_LOCAL_AUTH_ENABLED=true")
	}
	if cfg.OIDC.IssuerURL == nil {
		t.Fatal("enabling local auth dropped the OIDC configuration")
	}
}

// The goal of #211: boot with local accounts and no IdP configuration at all.
// Until this passes, "a POC needs no IdP" is unreachable however much of the
// mechanism exists.
func TestLoadSecurityConfigAcceptsLocalAuthWithNoOIDC(t *testing.T) {
	t.Parallel()

	cfg, err := loadSecurityConfig(mapConfigEnv(localOnlyValues()))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	if !cfg.LocalAuthEnabled {
		t.Fatal("LocalAuthEnabled is false")
	}
	if cfg.OIDC.IssuerURL != nil {
		t.Fatalf("IssuerURL = %v, want nil with no OIDC configured", cfg.OIDC.IssuerURL)
	}
}

// An API nobody can sign in to is a worse outcome than a refused boot, and it
// fails silently: every route answers 401 and nothing says why.
func TestLoadSecurityConfigRejectsNoAuthenticationMethod(t *testing.T) {
	t.Parallel()

	values := localOnlyValues()
	delete(values, "TFLIVE_LOCAL_AUTH_ENABLED")

	_, err := loadSecurityConfig(mapConfigEnv(values))
	if err == nil {
		t.Fatal("loadSecurityConfig accepted a configuration with no way to sign in")
	}
	if !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("error = %v, want it to name the missing authentication method", err)
	}
}

// The issuer is what enables OIDC, so naming one and omitting its client
// credentials is a half-configured provider rather than a deliberate opt-out.
func TestLoadSecurityConfigStillRequiresClientCredentialsAlongsideAnIssuer(t *testing.T) {
	t.Parallel()

	for _, missing := range []string{"OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET"} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()

			values := validSecurityValues()
			values["TFLIVE_LOCAL_AUTH_ENABLED"] = "true"
			delete(values, missing)

			_, err := loadSecurityConfig(mapConfigEnv(values))
			if err == nil {
				t.Fatalf("loadSecurityConfig accepted an issuer with no %s", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Fatalf("error = %v, want it to name %s", err, missing)
			}
		})
	}
}

// Client credentials with no issuer are the mirror image: they configure a
// provider that is never contacted, so the operator believes SSO is on when it
// is not.
func TestLoadSecurityConfigRejectsClientCredentialsWithoutAnIssuer(t *testing.T) {
	t.Parallel()

	values := localOnlyValues()
	values["OIDC_CLIENT_ID"] = "tflive-api"

	_, err := loadSecurityConfig(mapConfigEnv(values))
	if err == nil {
		t.Fatal("loadSecurityConfig accepted OIDC client credentials with no issuer")
	}
	if !strings.Contains(err.Error(), "OIDC_ISSUER_URL") {
		t.Fatalf("error = %v, want it to name OIDC_ISSUER_URL", err)
	}
}

func TestLoadSecurityConfigRejectsAnUnparsableLocalAuthFlag(t *testing.T) {
	t.Parallel()

	values := validSecurityValues()
	values["TFLIVE_LOCAL_AUTH_ENABLED"] = "yes please"

	_, err := loadSecurityConfig(mapConfigEnv(values))
	if err == nil {
		t.Fatal("loadSecurityConfig accepted an unparsable TFLIVE_LOCAL_AUTH_ENABLED")
	}
	if !strings.Contains(err.Error(), "TFLIVE_LOCAL_AUTH_ENABLED") {
		t.Fatalf("error = %v, want it to name the setting", err)
	}
}

// Production still requires HTTPS for an issuer that is present, and must not
// start dereferencing one that is absent.
func TestLoadSecurityConfigAllowsLocalOnlyProduction(t *testing.T) {
	t.Parallel()

	values := localOnlyValues()
	values["TFLIVE_ENVIRONMENT"] = "production"
	values["TFLIVE_PUBLIC_URL"] = "https://app.example.com"
	values["OPENFGA_API_URL"] = "https://openfga.example.com"
	values["OPENFGA_API_TOKEN"] = "openfga-token"

	cfg, err := loadSecurityConfig(mapConfigEnv(values))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	if !cfg.LocalAuthEnabled {
		t.Fatal("LocalAuthEnabled is false")
	}
}

// The redacting String() is what keeps secrets out of a config dump, so a new
// field must not be the one that reintroduces a leak.
func TestSecurityConfigStringReportsLocalAuth(t *testing.T) {
	t.Parallel()

	values := validSecurityValues()
	values["TFLIVE_LOCAL_AUTH_ENABLED"] = "true"
	cfg, err := loadSecurityConfig(mapConfigEnv(values))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	if !strings.Contains(cfg.String(), "LocalAuthEnabled:true") {
		t.Fatalf("String() = %s, want it to report LocalAuthEnabled", cfg.String())
	}
}
