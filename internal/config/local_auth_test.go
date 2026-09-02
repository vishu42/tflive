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
	return values
}

// Local sign-in is not configurable. Root is a local account, seeded at every
// boot and unable to be locked out (#212), so the password path is always
// present and there is no setting that could remove it. A flag here would only
// be able to lie.
func TestLoadSecurityConfigHasNoLocalAuthSetting(t *testing.T) {
	t.Parallel()

	values := validSecurityValues()
	values["TFLIVE_LOCAL_AUTH_ENABLED"] = "false"

	// Accepted and ignored: an unknown variable is not an error, and the point
	// is that setting it changes nothing.
	if _, err := loadSecurityConfig(mapConfigEnv(values)); err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
}

// The goal of #211: boot with local accounts and no IdP configuration at all.
func TestLoadSecurityConfigAcceptsNoOIDC(t *testing.T) {
	t.Parallel()

	cfg, err := loadSecurityConfig(mapConfigEnv(localOnlyValues()))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	if cfg.OIDC.IssuerURL != nil {
		t.Fatalf("IssuerURL = %v, want nil with no OIDC configured", cfg.OIDC.IssuerURL)
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

// Production still requires HTTPS for an issuer that is present, and must not
// start dereferencing one that is absent.
func TestLoadSecurityConfigAllowsLocalOnlyProduction(t *testing.T) {
	t.Parallel()

	values := localOnlyValues()
	values["TFLIVE_ENVIRONMENT"] = "production"
	values["TFLIVE_PUBLIC_URL"] = "https://app.example.com"
	values["OPENFGA_API_URL"] = "https://openfga.example.com"
	values["OPENFGA_API_TOKEN"] = "openfga-token"

	if _, err := loadSecurityConfig(mapConfigEnv(values)); err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
}
