package keycloak

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAdminRealm  = "master"
	defaultRealm       = "tflive"
	defaultAPIClient   = "tflive-api"
	defaultHTTPTimeout = 10 * time.Second
)

// Config contains the complete desired state and credentials needed for one
// Keycloak provisioning run. Secret fields must never be logged.
type Config struct {
	AdminURL              *url.URL
	AdminRealm            string
	AdminUsername         string
	AdminPassword         string
	Realm                 string
	APIClientID           string
	APIClientSecret       string
	CallbackURI           string
	PostLogoutRedirectURI string
	// BackchannelLogoutURI is TFLIVE_BACKCHANNEL_LOGOUT_URL when set, else
	// derived from TFLIVE_PUBLIC_URL like CallbackURI. Unlike the browser
	// redirect URIs above, this one is called by the IdP's own server, not the
	// browser, so it needs to be reachable from the IdP rather than from
	// wherever the browser sits.
	BackchannelLogoutURI   string
	PlatformAdminUsername  string
	PlatformAdminPassword  string
	PlatformAdminEmail     string
	PlatformAdminFirstName string
	PlatformAdminLastName  string
	Environment            string
	HTTPTimeout            time.Duration
}

// LoadConfig reads and validates Keycloak provisioning configuration.
func LoadConfig(getenv func(string) string) (Config, error) {
	adminURLRaw, err := required(getenv, "KEYCLOAK_ADMIN_URL")
	if err != nil {
		return Config{}, err
	}
	adminURL, err := parseAdminURL(adminURLRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid Keycloak config: KEYCLOAK_ADMIN_URL %w", err)
	}

	adminUsername, err := required(getenv, "KEYCLOAK_ADMIN_USERNAME")
	if err != nil {
		return Config{}, err
	}
	adminPassword, err := required(getenv, "KEYCLOAK_ADMIN_PASSWORD")
	if err != nil {
		return Config{}, err
	}
	publicURLRaw, err := required(getenv, "TFLIVE_PUBLIC_URL")
	if err != nil {
		return Config{}, err
	}
	publicURL, err := parseAdminURL(publicURLRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid Keycloak config: TFLIVE_PUBLIC_URL %w", err)
	}
	apiClientSecret, err := required(getenv, "OIDC_CLIENT_SECRET")
	if err != nil {
		return Config{}, err
	}
	platformUsername, err := required(getenv, "KEYCLOAK_PLATFORM_ADMIN_USERNAME")
	if err != nil {
		return Config{}, err
	}
	platformPassword, err := required(getenv, "KEYCLOAK_PLATFORM_ADMIN_PASSWORD")
	if err != nil {
		return Config{}, err
	}
	if strings.EqualFold(adminUsername, platformUsername) {
		return Config{}, fmt.Errorf("invalid Keycloak config: KEYCLOAK_PLATFORM_ADMIN_USERNAME must identify a different user than KEYCLOAK_ADMIN_USERNAME")
	}
	platformEmail, err := required(getenv, "KEYCLOAK_PLATFORM_ADMIN_EMAIL")
	if err != nil {
		return Config{}, err
	}
	platformFirstName, err := required(getenv, "KEYCLOAK_PLATFORM_ADMIN_FIRST_NAME")
	if err != nil {
		return Config{}, err
	}
	platformLastName, err := required(getenv, "KEYCLOAK_PLATFORM_ADMIN_LAST_NAME")
	if err != nil {
		return Config{}, err
	}

	// The callback and post-logout URIs are resolved by the browser, so
	// TFLIVE_PUBLIC_URL is always correct for them. A back-channel logout is a
	// server-to-server POST from the IdP's own process, which cannot in
	// general reach the browser's origin — an IdP on an internal network or
	// behind split-horizon DNS needs a different, IdP-reachable address here.
	backchannelLogoutURI := publicURL.String() + "/v1/auth/backchannel-logout"
	if raw := strings.TrimSpace(getenv("TFLIVE_BACKCHANNEL_LOGOUT_URL")); raw != "" {
		backchannelLogoutURL, err := parseAdminURL(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid Keycloak config: TFLIVE_BACKCHANNEL_LOGOUT_URL %w", err)
		}
		backchannelLogoutURI = backchannelLogoutURL.String()
	}

	timeout := defaultHTTPTimeout
	if raw := strings.TrimSpace(getenv("KEYCLOAK_HTTP_TIMEOUT")); raw != "" {
		timeout, err = time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid Keycloak config: KEYCLOAK_HTTP_TIMEOUT must be a duration: %w", err)
		}
		if timeout <= 0 {
			return Config{}, fmt.Errorf("invalid Keycloak config: KEYCLOAK_HTTP_TIMEOUT must be greater than zero")
		}
	}

	environment := strings.TrimSpace(getenv("TFLIVE_ENVIRONMENT"))
	if environment == "" {
		environment = "development"
	}
	if environment != "development" && environment != "production" {
		return Config{}, fmt.Errorf("invalid Keycloak config: TFLIVE_ENVIRONMENT must be development or production")
	}

	return Config{
		AdminURL:               adminURL,
		AdminRealm:             valueOrDefault(getenv("KEYCLOAK_ADMIN_REALM"), defaultAdminRealm),
		AdminUsername:          adminUsername,
		AdminPassword:          adminPassword,
		Realm:                  valueOrDefault(getenv("KEYCLOAK_REALM"), defaultRealm),
		APIClientID:            valueOrDefault(getenv("KEYCLOAK_API_CLIENT_ID"), defaultAPIClient),
		APIClientSecret:        apiClientSecret,
		CallbackURI:            publicURL.String() + "/v1/auth/callback",
		PostLogoutRedirectURI:  publicURL.String() + "/",
		BackchannelLogoutURI:   backchannelLogoutURI,
		PlatformAdminUsername:  platformUsername,
		PlatformAdminPassword:  platformPassword,
		PlatformAdminEmail:     platformEmail,
		PlatformAdminFirstName: platformFirstName,
		PlatformAdminLastName:  platformLastName,
		Environment:            environment,
		HTTPTimeout:            timeout,
	}, nil
}

func required(getenv func(string) string, name string) (string, error) {
	value := strings.TrimSpace(getenv(name))
	if value == "" {
		return "", fmt.Errorf("invalid Keycloak config: %s is required", name)
	}
	return value, nil
}

func valueOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func parseAdminURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("must be an absolute http or https URL")
	}
	if u.User != nil {
		return nil, fmt.Errorf("must not contain user information")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("must not contain a query or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}
