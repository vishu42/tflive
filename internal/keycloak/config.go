package keycloak

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAdminRealm  = "master"
	defaultRealm       = "tflive"
	defaultWebClient   = "tflive-web"
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
	WebClientID           string
	APIClientID           string
	RedirectURIs          []string
	WebOrigins            []string
	PlatformAdminUsername string
	PlatformAdminPassword string
	HTTPTimeout           time.Duration
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
	redirectsRaw, err := required(getenv, "KEYCLOAK_WEB_REDIRECT_URIS")
	if err != nil {
		return Config{}, err
	}
	originsRaw, err := required(getenv, "KEYCLOAK_WEB_ORIGINS")
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

	redirectURIs, err := parseBrowserURLs("KEYCLOAK_WEB_REDIRECT_URIS", redirectsRaw, false)
	if err != nil {
		return Config{}, err
	}
	webOrigins, err := parseBrowserURLs("KEYCLOAK_WEB_ORIGINS", originsRaw, true)
	if err != nil {
		return Config{}, err
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

	return Config{
		AdminURL:              adminURL,
		AdminRealm:            valueOrDefault(getenv("KEYCLOAK_ADMIN_REALM"), defaultAdminRealm),
		AdminUsername:         adminUsername,
		AdminPassword:         adminPassword,
		Realm:                 valueOrDefault(getenv("KEYCLOAK_REALM"), defaultRealm),
		WebClientID:           valueOrDefault(getenv("KEYCLOAK_WEB_CLIENT_ID"), defaultWebClient),
		APIClientID:           valueOrDefault(getenv("KEYCLOAK_API_CLIENT_ID"), defaultAPIClient),
		RedirectURIs:          redirectURIs,
		WebOrigins:            webOrigins,
		PlatformAdminUsername: platformUsername,
		PlatformAdminPassword: platformPassword,
		HTTPTimeout:           timeout,
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

func parseBrowserURLs(name, raw string, origin bool) ([]string, error) {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			return nil, fmt.Errorf("invalid Keycloak config: %s contains an empty value", name)
		}
		if strings.ContainsAny(value, "*+") {
			return nil, fmt.Errorf("invalid Keycloak config: %s must not contain wildcards", name)
		}

		u, err := url.Parse(value)
		if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("invalid Keycloak config: %s value %q must be an absolute http or https URL", name, value)
		}
		if u.User != nil {
			return nil, fmt.Errorf("invalid Keycloak config: %s value %q must not contain user information", name, value)
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("invalid Keycloak config: %s value %q must not contain a query or fragment", name, value)
		}
		if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
			return nil, fmt.Errorf("invalid Keycloak config: %s value %q plain HTTP is allowed only for loopback hosts", name, value)
		}
		if origin && u.Path != "" {
			return nil, fmt.Errorf("invalid Keycloak config: %s origin must not contain a path", name)
		}
		if _, ok := seen[value]; ok {
			return nil, fmt.Errorf("invalid Keycloak config: %s contains duplicate value %q", name, value)
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
