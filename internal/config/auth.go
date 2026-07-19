package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/vishu42/tflive/internal/traits"
)

const DefaultOpenFGAHTTPTimeout = 10 * time.Second

type RuntimeMode string

const (
	RuntimeDevelopment RuntimeMode = "development"
	RuntimeProduction  RuntimeMode = "production"
)

type SecurityConfig struct {
	Mode     RuntimeMode
	TenantID traits.TenantID
	OIDC     OIDCConfig
	OpenFGA  OpenFGAConfig

	KeycloakAdminURL             *url.URL
	KeycloakRealm                string
	DirectoryReaderClientID      string
	DirectoryReaderClientSecret  Secret
	DirectoryReaderHTTPTimeout   time.Duration
}

type OIDCConfig struct {
	IssuerURL *url.URL
	Audience  string
}

type OpenFGAConfig struct {
	APIURL         *url.URL
	StoreID        string
	ModelID        string
	APIToken       Secret
	RequestTimeout time.Duration
}

type Secret struct {
	value string
}

func newSecret(value string) Secret {
	return Secret{value: value}
}

func (secret Secret) Empty() bool {
	return secret.value == ""
}

func (secret Secret) Value() string {
	return secret.value
}

func (Secret) String() string {
	return "[REDACTED]"
}

func (Secret) GoString() string {
	return "[REDACTED]"
}

func (cfg OpenFGAConfig) String() string {
	return fmt.Sprintf(
		"OpenFGAConfig{APIURL:%v StoreID:%q ModelID:%q APIToken:%s RequestTimeout:%s}",
		cfg.APIURL,
		cfg.StoreID,
		cfg.ModelID,
		cfg.APIToken,
		cfg.RequestTimeout,
	)
}

func (cfg OpenFGAConfig) GoString() string {
	return cfg.String()
}

func (cfg SecurityConfig) String() string {
	return fmt.Sprintf(
		"SecurityConfig{Mode:%q TenantID:%q OIDC:{IssuerURL:%v Audience:%q} OpenFGA:%s KeycloakAdminURL:%v KeycloakRealm:%q DirectoryReaderClientID:%q DirectoryReaderClientSecret:%s DirectoryReaderHTTPTimeout:%s}",
		cfg.Mode,
		cfg.TenantID,
		cfg.OIDC.IssuerURL,
		cfg.OIDC.Audience,
		cfg.OpenFGA,
		cfg.KeycloakAdminURL,
		cfg.KeycloakRealm,
		cfg.DirectoryReaderClientID,
		cfg.DirectoryReaderClientSecret,
		cfg.DirectoryReaderHTTPTimeout,
	)
}

func (cfg SecurityConfig) GoString() string {
	return cfg.String()
}

const DefaultDirectoryReaderHTTPTimeout = 10 * time.Second

func loadSecurityConfig(getenv func(string) string) (SecurityConfig, error) {
	mode, err := parseRuntimeMode(getenv("TFLIVE_ENVIRONMENT"))
	if err != nil {
		return SecurityConfig{}, err
	}

	tenantID := strings.TrimSpace(getenv("TFLIVE_TENANT_ID"))
	if tenantID == "" {
		return SecurityConfig{}, authConfigError("TFLIVE_TENANT_ID is required")
	}
	if !validTenantID(tenantID) {
		return SecurityConfig{}, authConfigError("TFLIVE_TENANT_ID must start with an ASCII alphanumeric character, contain only ASCII alphanumerics, underscore, or hyphen, and be at most 128 characters")
	}

	issuerURL, err := parseConfigURL("OIDC_ISSUER_URL", getenv("OIDC_ISSUER_URL"))
	if err != nil {
		return SecurityConfig{}, err
	}
	audience := strings.TrimSpace(getenv("OIDC_AUDIENCE"))
	if audience == "" {
		return SecurityConfig{}, authConfigError("OIDC_AUDIENCE is required")
	}
	if !safeOpaqueValue(audience) {
		return SecurityConfig{}, authConfigError("OIDC_AUDIENCE must not contain whitespace or control characters")
	}

	openFGA, err := loadOpenFGAConfig(getenv)
	if err != nil {
		return SecurityConfig{}, err
	}
	if mode == RuntimeProduction && openFGA.APIURL.Scheme != "https" {
		return SecurityConfig{}, authConfigError("OPENFGA_API_URL must use HTTPS in production")
	}
	if mode == RuntimeProduction {
		if issuerURL.Scheme != "https" {
			return SecurityConfig{}, authConfigError("OIDC_ISSUER_URL must use HTTPS in production")
		}
		if openFGA.APIToken.Empty() {
			return SecurityConfig{}, authConfigError("OPENFGA_API_TOKEN is required in production")
		}
	}

	keycloakAdminURL, err := parseOptionalConfigURL("KEYCLOAK_ADMIN_URL", getenv("KEYCLOAK_ADMIN_URL"))
	if err != nil {
		return SecurityConfig{}, err
	}
	keycloakRealm := strings.TrimSpace(getenv("KEYCLOAK_REALM"))
	if keycloakRealm == "" {
		keycloakRealm = "tflive"
	}
	directoryReaderClientID := strings.TrimSpace(getenv("KEYCLOAK_DIRECTORY_READER_CLIENT_ID"))
	directoryReaderSecret := newSecret(getenv("KEYCLOAK_DIRECTORY_READER_CLIENT_SECRET"))
	directoryReaderTimeout := DefaultDirectoryReaderHTTPTimeout
	if raw := strings.TrimSpace(getenv("KEYCLOAK_DIRECTORY_READER_HTTP_TIMEOUT")); raw != "" {
		directoryReaderTimeout, err = time.ParseDuration(raw)
		if err != nil || directoryReaderTimeout <= 0 {
			return SecurityConfig{}, authConfigError("KEYCLOAK_DIRECTORY_READER_HTTP_TIMEOUT must be a positive duration")
		}
	}
	if mode == RuntimeProduction {
		if directoryReaderClientID == "" {
			return SecurityConfig{}, authConfigError("KEYCLOAK_DIRECTORY_READER_CLIENT_ID is required in production")
		}
		if directoryReaderSecret.Empty() {
			return SecurityConfig{}, authConfigError("KEYCLOAK_DIRECTORY_READER_CLIENT_SECRET is required in production")
		}
	}

	return SecurityConfig{
		Mode:     mode,
		TenantID: traits.TenantID(tenantID),
		OIDC: OIDCConfig{
			IssuerURL: issuerURL,
			Audience:  audience,
		},
		OpenFGA: openFGA,

		KeycloakAdminURL:            keycloakAdminURL,
		KeycloakRealm:               keycloakRealm,
		DirectoryReaderClientID:     directoryReaderClientID,
		DirectoryReaderClientSecret: directoryReaderSecret,
		DirectoryReaderHTTPTimeout:  directoryReaderTimeout,
	}, nil
}

func loadOpenFGAConfig(getenv func(string) string) (OpenFGAConfig, error) {
	apiURL, err := parseConfigURL("OPENFGA_API_URL", getenv("OPENFGA_API_URL"))
	if err != nil {
		return OpenFGAConfig{}, err
	}
	storeID := strings.TrimSpace(getenv("OPENFGA_STORE_ID"))
	if storeID == "" {
		return OpenFGAConfig{}, authConfigError("OPENFGA_STORE_ID is required")
	}
	if !safeOpaqueValue(storeID) {
		return OpenFGAConfig{}, authConfigError("OPENFGA_STORE_ID must not contain whitespace or control characters")
	}
	modelID := strings.TrimSpace(getenv("OPENFGA_MODEL_ID"))
	if modelID == "" {
		return OpenFGAConfig{}, authConfigError("OPENFGA_MODEL_ID is required")
	}
	if !safeOpaqueValue(modelID) {
		return OpenFGAConfig{}, authConfigError("OPENFGA_MODEL_ID must not contain whitespace or control characters")
	}
	token := newSecret(getenv("OPENFGA_API_TOKEN"))
	if !token.Empty() && !safeOpaqueValue(token.Value()) {
		return OpenFGAConfig{}, authConfigError("OPENFGA_API_TOKEN must not contain whitespace or control characters")
	}
	timeout := DefaultOpenFGAHTTPTimeout
	if raw := strings.TrimSpace(getenv("OPENFGA_HTTP_TIMEOUT")); raw != "" {
		timeout, err = time.ParseDuration(raw)
		if err != nil || timeout <= 0 {
			return OpenFGAConfig{}, authConfigError("OPENFGA_HTTP_TIMEOUT must be a positive duration")
		}
	}
	return OpenFGAConfig{APIURL: apiURL, StoreID: storeID, ModelID: modelID, APIToken: token, RequestTimeout: timeout}, nil
}

func parseRuntimeMode(raw string) (RuntimeMode, error) {
	switch strings.TrimSpace(raw) {
	case "", string(RuntimeDevelopment):
		return RuntimeDevelopment, nil
	case string(RuntimeProduction):
		return RuntimeProduction, nil
	default:
		return "", authConfigError("TFLIVE_ENVIRONMENT must be development or production")
	}
}

func parseConfigURL(name, raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, authConfigError("%s is required", name)
	}
	return parseAbsoluteURL(name, raw)
}

func parseOptionalConfigURL(name, raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	return parseAbsoluteURL(name, raw)
}

func parseAbsoluteURL(name, raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, authConfigError("%s must be an absolute HTTP or HTTPS URL", name)
	}
	if parsed.User != nil {
		return nil, authConfigError("%s must not include user information", name)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return nil, authConfigError("%s must not include a query", name)
	}
	if parsed.Fragment != "" || strings.Contains(raw, "#") {
		return nil, authConfigError("%s must not include a fragment", name)
	}
	return parsed, nil
}

func validTenantID(value string) bool {
	if len(value) == 0 || len(value) > 128 || !asciiAlphanumeric(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !asciiAlphanumeric(character) && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func asciiAlphanumeric(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9'
}

func safeOpaqueValue(value string) bool {
	return value != "" && strings.IndexFunc(value, func(character rune) bool {
		return unicode.IsSpace(character) || unicode.IsControl(character)
	}) == -1
}

func authConfigError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, fmt.Sprintf(format, arguments...))
}
