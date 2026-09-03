package openfga

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vishu42/tflive/internal/strval"
)

const (
	defaultStoreName   = "tflive"
	defaultHTTPTimeout = 10 * time.Second
)

type Config struct {
	APIURL      *url.URL
	StoreName   string
	StoreID     string
	ModelID     string
	APIToken    string
	HTTPTimeout time.Duration
	// Transport overrides the HTTP transport used for requests. LoadConfig
	// never sets it; callers use it to install instrumentation, and tests use
	// it to simulate transport and response-body failures.
	Transport http.RoundTripper
}

func LoadConfig(getenv func(string) string) (Config, error) {
	rawURL := strings.TrimSpace(getenv("OPENFGA_API_URL"))
	if rawURL == "" {
		return Config{}, fmt.Errorf("OPENFGA_API_URL is required")
	}
	apiURL, err := url.Parse(rawURL)
	if err != nil {
		return Config{}, fmt.Errorf("OPENFGA_API_URL must be a URL: %w", err)
	}
	if apiURL.Scheme != "http" && apiURL.Scheme != "https" {
		return Config{}, fmt.Errorf("OPENFGA_API_URL scheme must be http or https")
	}
	if apiURL.Hostname() == "" {
		return Config{}, fmt.Errorf("OPENFGA_API_URL must include a host")
	}
	if apiURL.User != nil {
		return Config{}, fmt.Errorf("OPENFGA_API_URL must not include user information")
	}
	if apiURL.RawQuery != "" || apiURL.ForceQuery {
		return Config{}, fmt.Errorf("OPENFGA_API_URL must not include a query")
	}
	if apiURL.Fragment != "" || strings.Contains(rawURL, "#") {
		return Config{}, fmt.Errorf("OPENFGA_API_URL must not include a fragment")
	}

	storeName := defaultStoreName
	if raw := getenv("OPENFGA_STORE_NAME"); raw != "" {
		storeName = strings.TrimSpace(raw)
		if storeName == "" {
			return Config{}, fmt.Errorf("OPENFGA_STORE_NAME must not be blank")
		}
	}

	timeout := defaultHTTPTimeout
	if raw := strings.TrimSpace(getenv("OPENFGA_HTTP_TIMEOUT")); raw != "" {
		timeout, err = time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("OPENFGA_HTTP_TIMEOUT must be a duration: %w", err)
		}
		if timeout <= 0 {
			return Config{}, fmt.Errorf("OPENFGA_HTTP_TIMEOUT must be greater than zero")
		}
	}

	return Config{
		APIURL:      apiURL,
		StoreName:   storeName,
		StoreID:     getenv("OPENFGA_STORE_ID"),
		ModelID:     getenv("OPENFGA_MODEL_ID"),
		APIToken:    getenv("OPENFGA_API_TOKEN"),
		HTTPTimeout: timeout,
	}, nil
}

func (cfg Config) ValidateVerify() error {
	if cfg.StoreID == "" {
		return fmt.Errorf("OPENFGA_STORE_ID is required for verify")
	}
	if !SafeOpaqueIdentifier(cfg.StoreID) {
		return fmt.Errorf("OPENFGA_STORE_ID must not contain whitespace or control characters")
	}
	if cfg.ModelID == "" {
		return fmt.Errorf("OPENFGA_MODEL_ID is required for verify")
	}
	if !SafeOpaqueIdentifier(cfg.ModelID) {
		return fmt.Errorf("OPENFGA_MODEL_ID must not contain whitespace or control characters")
	}
	return nil
}

// SafeOpaqueIdentifier reports whether value is usable as an OpenFGA store,
// model, or continuation-token identifier: non-empty and free of whitespace
// and control characters, so it cannot corrupt a URL path or a request body.
func SafeOpaqueIdentifier(value string) bool {
	return strval.SafeOpaque(value)
}
