package openfga

import (
	"strings"
	"testing"
	"time"
)

func TestLoadConfigReadsValidValuesAndDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(mapEnv(map[string]string{
		"OPENFGA_API_URL":   "http://openfga:8080",
		"OPENFGA_STORE_ID":  "store-id",
		"OPENFGA_MODEL_ID":  "model-id",
		"OPENFGA_API_TOKEN": "secret-token",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIURL.String() != "http://openfga:8080" || cfg.StoreName != "tflive" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if cfg.StoreID != "store-id" || cfg.ModelID != "model-id" || cfg.APIToken != "secret-token" {
		t.Fatalf("explicit config missing: %#v", cfg)
	}
	if cfg.HTTPTimeout != 10*time.Second {
		t.Fatalf("HTTPTimeout = %s", cfg.HTTPTimeout)
	}
	if err := cfg.ValidateVerify(); err != nil {
		t.Fatalf("ValidateVerify() error = %v", err)
	}
}

func TestLoadConfigRejectsInvalidCommonValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{name: "missing URL", values: map[string]string{}, want: "OPENFGA_API_URL is required"},
		{name: "unsupported scheme", values: map[string]string{"OPENFGA_API_URL": "ftp://openfga"}, want: "scheme must be http or https"},
		{name: "missing host", values: map[string]string{"OPENFGA_API_URL": "http:///path"}, want: "must include a host"},
		{name: "port-only authority", values: map[string]string{"OPENFGA_API_URL": "http://:8080"}, want: "must include a host"},
		{name: "userinfo", values: map[string]string{"OPENFGA_API_URL": "http://user@openfga"}, want: "must not include user information"},
		{name: "query", values: map[string]string{"OPENFGA_API_URL": "http://openfga?x=1"}, want: "must not include a query"},
		{name: "bare query marker", values: map[string]string{"OPENFGA_API_URL": "http://openfga?"}, want: "must not include a query"},
		{name: "fragment", values: map[string]string{"OPENFGA_API_URL": "http://openfga#x"}, want: "must not include a fragment"},
		{name: "bare fragment marker", values: map[string]string{"OPENFGA_API_URL": "http://openfga#"}, want: "must not include a fragment"},
		{name: "empty store name", values: map[string]string{"OPENFGA_API_URL": "http://openfga", "OPENFGA_STORE_NAME": " "}, want: "OPENFGA_STORE_NAME must not be blank"},
		{name: "bad timeout", values: map[string]string{"OPENFGA_API_URL": "http://openfga", "OPENFGA_HTTP_TIMEOUT": "later"}, want: "must be a duration"},
		{name: "zero timeout", values: map[string]string{"OPENFGA_API_URL": "http://openfga", "OPENFGA_HTTP_TIMEOUT": "0s"}, want: "must be greater than zero"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadConfig(mapEnv(test.values))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadConfig() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestValidateVerifyRequiresExplicitIDs(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(mapEnv(map[string]string{"OPENFGA_API_URL": "http://openfga:8080"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateVerify(); err == nil || !strings.Contains(err.Error(), "OPENFGA_STORE_ID is required") {
		t.Fatalf("ValidateVerify() error = %v", err)
	}
	cfg.StoreID = "store-id"
	if err := cfg.ValidateVerify(); err == nil || !strings.Contains(err.Error(), "OPENFGA_MODEL_ID is required") {
		t.Fatalf("ValidateVerify() error = %v", err)
	}
}

func TestValidateVerifyRejectsUnsafeOpaqueIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		storeID string
		modelID string
		want    string
	}{
		{name: "line break in store ID", storeID: "store\nid", modelID: "model-id", want: "OPENFGA_STORE_ID must not contain whitespace or control"},
		{name: "leading whitespace in store ID", storeID: " store-id", modelID: "model-id", want: "OPENFGA_STORE_ID must not contain whitespace or control"},
		{name: "trailing whitespace in store ID", storeID: "store-id ", modelID: "model-id", want: "OPENFGA_STORE_ID must not contain whitespace or control"},
		{name: "leading whitespace in model ID", storeID: "store-id", modelID: " model-id", want: "OPENFGA_MODEL_ID must not contain whitespace or control"},
		{name: "trailing whitespace in model ID", storeID: "store-id", modelID: "model-id ", want: "OPENFGA_MODEL_ID must not contain whitespace or control"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := LoadConfig(mapEnv(map[string]string{
				"OPENFGA_API_URL":  "http://openfga:8080",
				"OPENFGA_STORE_ID": test.storeID,
				"OPENFGA_MODEL_ID": test.modelID,
			}))
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.ValidateVerify(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateVerify() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func mapEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
