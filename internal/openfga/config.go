package openfga

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
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
	if apiURL.Host == "" {
		return Config{}, fmt.Errorf("OPENFGA_API_URL must include a host")
	}
	if apiURL.User != nil {
		return Config{}, fmt.Errorf("OPENFGA_API_URL must not include user information")
	}
	if apiURL.RawQuery != "" {
		return Config{}, fmt.Errorf("OPENFGA_API_URL must not include a query")
	}
	if apiURL.Fragment != "" {
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
		StoreID:     strings.TrimSpace(getenv("OPENFGA_STORE_ID")),
		ModelID:     strings.TrimSpace(getenv("OPENFGA_MODEL_ID")),
		APIToken:    getenv("OPENFGA_API_TOKEN"),
		HTTPTimeout: timeout,
	}, nil
}

func (cfg Config) ValidateVerify() error {
	if cfg.StoreID == "" {
		return fmt.Errorf("OPENFGA_STORE_ID is required for verify")
	}
	if !safeOpaqueIdentifier(cfg.StoreID) {
		return fmt.Errorf("OPENFGA_STORE_ID must not contain whitespace or control characters")
	}
	if cfg.ModelID == "" {
		return fmt.Errorf("OPENFGA_MODEL_ID is required for verify")
	}
	if !safeOpaqueIdentifier(cfg.ModelID) {
		return fmt.Errorf("OPENFGA_MODEL_ID must not contain whitespace or control characters")
	}
	return nil
}

func safeOpaqueIdentifier(value string) bool {
	return value != "" && strings.IndexFunc(value, func(character rune) bool {
		return unicode.IsSpace(character) || unicode.IsControl(character)
	}) == -1
}
