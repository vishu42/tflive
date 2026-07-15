# Authentication and Authorization Runtime Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add typed, secret-safe API runtime configuration for the configured tenant, OIDC, and OpenFGA, reject incomplete or insecure production settings before startup, and give every OpenFGA request an explicit deadline.

**Architecture:** `internal/config` owns nested runtime security types and all environment parsing. `cmd/api` loads this configuration before initializing dependencies and logs only sanitized errors. The existing `internal/openfga.Client` retains its transport timeout and also derives a context deadline for each request.

**Tech Stack:** Go 1.24.1 standard library (`context`, `fmt`, `net/url`, `time`, `unicode`), existing tflive configuration and OpenFGA packages, Node.js Compose contract checks, Markdown operational documentation

## Global Constraints

- An empty `TFLIVE_ENVIRONMENT` resolves to `development`; the only non-empty accepted values are `development` and `production`.
- Production requires HTTPS for both `OIDC_ISSUER_URL` and `OPENFGA_API_URL` and requires a non-empty `OPENFGA_API_TOKEN`.
- `TFLIVE_TENANT_ID` is 1 through 128 ASCII characters, starts with an ASCII alphanumeric character, and otherwise contains only ASCII alphanumerics, `_`, or `-`.
- `OPENFGA_STORE_ID` and `OPENFGA_MODEL_ID` are always explicit and are never discovered or defaulted.
- `OPENFGA_HTTP_TIMEOUT` defaults to `10s`, must be positive, and supplies both an HTTP-client timeout and a per-request context deadline.
- Raw environment values must never be embedded in validation errors. Bootstrap passwords, client secrets, and API tokens must not appear in formatting, errors, or startup logs.
- Development may use the documented HTTP endpoints and a tokenless local OpenFGA service.
- Do not implement OIDC verification, auth middleware, tenant-route enforcement, or the runtime authorization adapter in this ticket.
- Run every shell command through `rtk`.

---

## File Structure

- `internal/config/auth.go` — runtime mode, nested security types, redacting secret type, parsing, and common/production validation.
- `internal/config/auth_test.go` — development, production, missing, malformed, and secret-formatting unit tests.
- `internal/config/config.go` — attach `SecurityConfig` to `APIConfig` and invoke the focused loader.
- `internal/config/config_test.go` — supply valid security settings to existing API configuration tests.
- `cmd/api/main.go` — preserve fail-fast configuration loading and make the final startup log path directly testable.
- `cmd/api/main_test.go` — shared complete API environment, dependency short-circuit test, and log-redaction regression test.
- `internal/openfga/client.go` — store a normalized request timeout and derive a context deadline in `doJSON`.
- `internal/openfga/client_test.go` — configured deadline, earlier-parent deadline, safe default, cancellation, and existing redaction coverage.
- `.env.example` — documented local API security values while leaving generated OpenFGA IDs blank.
- `scripts/verify-auth-compose.mjs` — assert every runtime security example key appears exactly once with the intended local value.
- `docs/authentication.md` — runtime variable table and development/production behavior.
- `docs/sprint/authn_and_authz/README.md` — mark AUTH-005 `Done` only after complete verification.

---

### Task 1: Add Typed API Security Configuration

**Files:**
- Create: `internal/config/auth.go`
- Create: `internal/config/auth_test.go`
- Modify: `internal/config/config.go:18-92`
- Modify: `internal/config/config_test.go:1-298`

**Interfaces:**
- Consumes: `ErrInvalidConfig`, `traits.TenantID`, and `getenv func(string) string`.
- Produces: `RuntimeMode`, `SecurityConfig`, `OIDCConfig`, `OpenFGAConfig`, `Secret`, `loadSecurityConfig(func(string) string) (SecurityConfig, error)`, and `APIConfig.Security`.

- [ ] **Step 1: Write focused failing configuration tests**

Create `internal/config/auth_test.go`:

```go
package config

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLoadSecurityConfigDevelopmentModes(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"", "development"} {
		mode := mode
		t.Run(fmt.Sprintf("mode_%q", mode), func(t *testing.T) {
			t.Parallel()

			values := validSecurityValues()
			values["TFLIVE_ENVIRONMENT"] = mode
			cfg, err := loadSecurityConfig(mapConfigEnv(values))
			if err != nil {
				t.Fatalf("loadSecurityConfig returned error: %v", err)
			}
			if cfg.Mode != RuntimeDevelopment {
				t.Fatalf("Mode = %q, want %q", cfg.Mode, RuntimeDevelopment)
			}
			if cfg.TenantID != "tenant_123" {
				t.Fatalf("TenantID = %q, want tenant_123", cfg.TenantID)
			}
			if got := cfg.OIDC.IssuerURL.String(); got != "http://localhost:8082/realms/tflive" {
				t.Fatalf("IssuerURL = %q", got)
			}
			if cfg.OIDC.Audience != "tflive-api" {
				t.Fatalf("Audience = %q, want tflive-api", cfg.OIDC.Audience)
			}
			if got := cfg.OpenFGA.APIURL.String(); got != "http://localhost:8080" {
				t.Fatalf("OpenFGA APIURL = %q", got)
			}
			if cfg.OpenFGA.StoreID != "store-id" || cfg.OpenFGA.ModelID != "model-id" {
				t.Fatalf("OpenFGA IDs = %q/%q", cfg.OpenFGA.StoreID, cfg.OpenFGA.ModelID)
			}
			if !cfg.OpenFGA.APIToken.Empty() {
				t.Fatal("development API token is not empty")
			}
			if cfg.OpenFGA.RequestTimeout != 10*time.Second {
				t.Fatalf("RequestTimeout = %s, want 10s", cfg.OpenFGA.RequestTimeout)
			}
		})
	}
}

func TestLoadSecurityConfigProductionAndSecretFormatting(t *testing.T) {
	t.Parallel()

	values := validSecurityValues()
	values["TFLIVE_ENVIRONMENT"] = "production"
	values["OIDC_ISSUER_URL"] = "https://id.example.com/realms/tflive"
	values["OPENFGA_API_URL"] = "https://openfga.example.com"
	values["OPENFGA_API_TOKEN"] = "openfga-token-sentinel"

	cfg, err := loadSecurityConfig(mapConfigEnv(values))
	if err != nil {
		t.Fatalf("loadSecurityConfig returned error: %v", err)
	}
	if cfg.Mode != RuntimeProduction {
		t.Fatalf("Mode = %q, want %q", cfg.Mode, RuntimeProduction)
	}
	if got := cfg.OpenFGA.APIToken.Value(); got != "openfga-token-sentinel" {
		t.Fatalf("APIToken.Value() = %q", got)
	}

	formatted := fmt.Sprintf("%s\n%v\n%+v\n%#v", cfg.OpenFGA.APIToken, cfg.OpenFGA, cfg.OpenFGA, cfg)
	if strings.Contains(formatted, "openfga-token-sentinel") {
		t.Fatalf("formatted configuration leaked token: %s", formatted)
	}
	if !strings.Contains(formatted, "[REDACTED]") {
		t.Fatalf("formatted configuration did not show redaction marker: %s", formatted)
	}
}

func TestLoadSecurityConfigRejectsMissingAndMalformedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "unknown environment", key: "TFLIVE_ENVIRONMENT", value: "staging", want: "TFLIVE_ENVIRONMENT must be development or production"},
		{name: "missing tenant", key: "TFLIVE_TENANT_ID", value: "", want: "TFLIVE_TENANT_ID is required"},
		{name: "tenant prefix", key: "TFLIVE_TENANT_ID", value: "-tenant", want: "TFLIVE_TENANT_ID must start"},
		{name: "tenant slash", key: "TFLIVE_TENANT_ID", value: "tenant/123", want: "TFLIVE_TENANT_ID must start"},
		{name: "tenant too long", key: "TFLIVE_TENANT_ID", value: strings.Repeat("a", 129), want: "TFLIVE_TENANT_ID must start"},
		{name: "missing issuer", key: "OIDC_ISSUER_URL", value: "", want: "OIDC_ISSUER_URL is required"},
		{name: "relative issuer", key: "OIDC_ISSUER_URL", value: "/realms/tflive", want: "OIDC_ISSUER_URL must be an absolute HTTP or HTTPS URL"},
		{name: "issuer user info", key: "OIDC_ISSUER_URL", value: "https://client:client-secret-sentinel@id.example.com/realms/tflive", want: "OIDC_ISSUER_URL must not include user information"},
		{name: "issuer query", key: "OIDC_ISSUER_URL", value: "https://id.example.com/realms/tflive?x=1", want: "OIDC_ISSUER_URL must not include a query"},
		{name: "issuer fragment", key: "OIDC_ISSUER_URL", value: "https://id.example.com/realms/tflive#keys", want: "OIDC_ISSUER_URL must not include a fragment"},
		{name: "missing audience", key: "OIDC_AUDIENCE", value: "", want: "OIDC_AUDIENCE is required"},
		{name: "audience whitespace", key: "OIDC_AUDIENCE", value: "tflive api", want: "OIDC_AUDIENCE must not contain whitespace or control characters"},
		{name: "missing OpenFGA URL", key: "OPENFGA_API_URL", value: "", want: "OPENFGA_API_URL is required"},
		{name: "OpenFGA scheme", key: "OPENFGA_API_URL", value: "ftp://openfga.example.com", want: "OPENFGA_API_URL must be an absolute HTTP or HTTPS URL"},
		{name: "OpenFGA user info", key: "OPENFGA_API_URL", value: "https://user:api-url-secret-sentinel@openfga.example.com", want: "OPENFGA_API_URL must not include user information"},
		{name: "OpenFGA query", key: "OPENFGA_API_URL", value: "https://openfga.example.com?x=1", want: "OPENFGA_API_URL must not include a query"},
		{name: "OpenFGA fragment", key: "OPENFGA_API_URL", value: "https://openfga.example.com#api", want: "OPENFGA_API_URL must not include a fragment"},
		{name: "missing store ID", key: "OPENFGA_STORE_ID", value: "", want: "OPENFGA_STORE_ID is required"},
		{name: "unsafe store ID", key: "OPENFGA_STORE_ID", value: "store id", want: "OPENFGA_STORE_ID must not contain whitespace or control characters"},
		{name: "missing model ID", key: "OPENFGA_MODEL_ID", value: "", want: "OPENFGA_MODEL_ID is required"},
		{name: "unsafe model ID", key: "OPENFGA_MODEL_ID", value: "model\nid", want: "OPENFGA_MODEL_ID must not contain whitespace or control characters"},
		{name: "malformed timeout", key: "OPENFGA_HTTP_TIMEOUT", value: "forever", want: "OPENFGA_HTTP_TIMEOUT must be a positive duration"},
		{name: "zero timeout", key: "OPENFGA_HTTP_TIMEOUT", value: "0s", want: "OPENFGA_HTTP_TIMEOUT must be a positive duration"},
		{name: "negative timeout", key: "OPENFGA_HTTP_TIMEOUT", value: "-1s", want: "OPENFGA_HTTP_TIMEOUT must be a positive duration"},
		{name: "unsafe token", key: "OPENFGA_API_TOKEN", value: "api token sentinel", want: "OPENFGA_API_TOKEN must not contain whitespace or control characters"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := validSecurityValues()
			values[test.key] = test.value
			_, err := loadSecurityConfig(mapConfigEnv(values))
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
			for _, secret := range []string{"client-secret-sentinel", "api-url-secret-sentinel", "api token sentinel"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestLoadSecurityConfigRejectsInsecureProductionValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{name: "HTTP issuer", mutate: func(values map[string]string) { values["OIDC_ISSUER_URL"] = "http://id.example.com/realms/tflive" }, want: "OIDC_ISSUER_URL must use HTTPS in production"},
		{name: "HTTP OpenFGA", mutate: func(values map[string]string) { values["OPENFGA_API_URL"] = "http://openfga.example.com" }, want: "OPENFGA_API_URL must use HTTPS in production"},
		{name: "missing OpenFGA token", mutate: func(values map[string]string) { values["OPENFGA_API_TOKEN"] = "" }, want: "OPENFGA_API_TOKEN is required in production"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := validSecurityValues()
			values["TFLIVE_ENVIRONMENT"] = "production"
			values["OIDC_ISSUER_URL"] = "https://id.example.com/realms/tflive"
			values["OPENFGA_API_URL"] = "https://openfga.example.com"
			values["OPENFGA_API_TOKEN"] = "production-token-sentinel"
			test.mutate(values)
			_, err := loadSecurityConfig(mapConfigEnv(values))
			if !errors.Is(err, ErrInvalidConfig) || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want ErrInvalidConfig containing %q", err, test.want)
			}
			if strings.Contains(err.Error(), "production-token-sentinel") {
				t.Fatalf("error leaked token: %v", err)
			}
		})
	}
}

func validSecurityValues() map[string]string {
	return map[string]string{
		"TFLIVE_ENVIRONMENT":  "development",
		"TFLIVE_TENANT_ID":    "tenant_123",
		"OIDC_ISSUER_URL":     "http://localhost:8082/realms/tflive",
		"OIDC_AUDIENCE":       "tflive-api",
		"OPENFGA_API_URL":     "http://localhost:8080",
		"OPENFGA_STORE_ID":    "store-id",
		"OPENFGA_MODEL_ID":    "model-id",
		"OPENFGA_API_TOKEN":   "",
		"OPENFGA_HTTP_TIMEOUT": "",
	}
}

func mapConfigEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
```

- [ ] **Step 2: Run the focused tests and confirm the new contract is absent**

Run:

```bash
rtk go test ./internal/config -run 'TestLoadSecurityConfig' -count=1
```

Expected: compilation fails because `loadSecurityConfig`, `RuntimeDevelopment`, and the nested configuration types do not exist.

- [ ] **Step 3: Implement the complete focused parser and redacting types**

Create `internal/config/auth.go`:

```go
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
		"SecurityConfig{Mode:%q TenantID:%q OIDC:{IssuerURL:%v Audience:%q} OpenFGA:%s}",
		cfg.Mode,
		cfg.TenantID,
		cfg.OIDC.IssuerURL,
		cfg.OIDC.Audience,
		cfg.OpenFGA,
	)
}

func (cfg SecurityConfig) GoString() string {
	return cfg.String()
}

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

	openFGAURL, err := parseConfigURL("OPENFGA_API_URL", getenv("OPENFGA_API_URL"))
	if err != nil {
		return SecurityConfig{}, err
	}
	storeID := strings.TrimSpace(getenv("OPENFGA_STORE_ID"))
	if storeID == "" {
		return SecurityConfig{}, authConfigError("OPENFGA_STORE_ID is required")
	}
	if !safeOpaqueValue(storeID) {
		return SecurityConfig{}, authConfigError("OPENFGA_STORE_ID must not contain whitespace or control characters")
	}
	modelID := strings.TrimSpace(getenv("OPENFGA_MODEL_ID"))
	if modelID == "" {
		return SecurityConfig{}, authConfigError("OPENFGA_MODEL_ID is required")
	}
	if !safeOpaqueValue(modelID) {
		return SecurityConfig{}, authConfigError("OPENFGA_MODEL_ID must not contain whitespace or control characters")
	}

	tokenValue := getenv("OPENFGA_API_TOKEN")
	if tokenValue != "" && !safeOpaqueValue(tokenValue) {
		return SecurityConfig{}, authConfigError("OPENFGA_API_TOKEN must not contain whitespace or control characters")
	}
	token := newSecret(tokenValue)

	timeout := DefaultOpenFGAHTTPTimeout
	if rawTimeout := strings.TrimSpace(getenv("OPENFGA_HTTP_TIMEOUT")); rawTimeout != "" {
		timeout, err = time.ParseDuration(rawTimeout)
		if err != nil || timeout <= 0 {
			return SecurityConfig{}, authConfigError("OPENFGA_HTTP_TIMEOUT must be a positive duration")
		}
	}

	if mode == RuntimeProduction {
		if issuerURL.Scheme != "https" {
			return SecurityConfig{}, authConfigError("OIDC_ISSUER_URL must use HTTPS in production")
		}
		if openFGAURL.Scheme != "https" {
			return SecurityConfig{}, authConfigError("OPENFGA_API_URL must use HTTPS in production")
		}
		if token.Empty() {
			return SecurityConfig{}, authConfigError("OPENFGA_API_TOKEN is required in production")
		}
	}

	return SecurityConfig{
		Mode:     mode,
		TenantID: traits.TenantID(tenantID),
		OIDC: OIDCConfig{
			IssuerURL: issuerURL,
			Audience:  audience,
		},
		OpenFGA: OpenFGAConfig{
			APIURL:         openFGAURL,
			StoreID:        storeID,
			ModelID:        modelID,
			APIToken:       token,
			RequestTimeout: timeout,
		},
	}, nil
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
```

- [ ] **Step 4: Run the focused parser tests**

Run:

```bash
rtk gofmt -w internal/config/auth.go internal/config/auth_test.go
rtk go test ./internal/config -run 'TestLoadSecurityConfig' -count=1
```

Expected: all new focused tests pass.

- [ ] **Step 5: Integrate security configuration into `APIConfig`**

Add the field to `APIConfig` in `internal/config/config.go`:

```go
type APIConfig struct {
	DatabaseURL       string
	HTTPAddress       string
	TemporalAddress   string
	TemporalNamespace string
	TemporalTaskQueue string
	WorkerRunRoot     string
	ArtifactStore     ArtifactStoreConfig
	Security          SecurityConfig
}
```

Start `LoadAPIConfig` with security loading, retain artifact loading, and include the result in the struct:

```go
func LoadAPIConfig(getenv func(string) string) (APIConfig, error) {
	security, err := loadSecurityConfig(getenv)
	if err != nil {
		return APIConfig{}, err
	}
	artifactStore, err := loadArtifactStoreConfig(getenv)
	if err != nil {
		return APIConfig{}, err
	}

	cfg := APIConfig{
		DatabaseURL:       strings.TrimSpace(getenv("DATABASE_URL")),
		HTTPAddress:       strings.TrimSpace(getenv("HTTP_ADDRESS")),
		TemporalAddress:   strings.TrimSpace(getenv("TEMPORAL_ADDRESS")),
		TemporalNamespace: strings.TrimSpace(getenv("TEMPORAL_NAMESPACE")),
		TemporalTaskQueue: strings.TrimSpace(getenv("TEMPORAL_TASK_QUEUE")),
		WorkerRunRoot:     strings.TrimSpace(getenv("WORKER_RUN_ROOT")),
		ArtifactStore:     artifactStore,
		Security:          security,
	}
	if cfg.HTTPAddress == "" {
		cfg.HTTPAddress = DefaultHTTPAddress
	}
	if cfg.TemporalTaskQueue == "" {
		cfg.TemporalTaskQueue = DefaultTemporalTaskQueue
	}
	if cfg.WorkerRunRoot == "" {
		cfg.WorkerRunRoot = DefaultWorkerRunRoot
	}

	if cfg.DatabaseURL == "" {
		return APIConfig{}, fmt.Errorf("%w: DATABASE_URL is required", ErrInvalidConfig)
	}
	if cfg.TemporalAddress == "" {
		return APIConfig{}, fmt.Errorf("%w: TEMPORAL_ADDRESS is required", ErrInvalidConfig)
	}

	return cfg, nil
}
```

- [ ] **Step 6: Give existing API config tests valid security values**

Add this helper to `internal/config/config_test.go`:

```go
func withValidSecurity(getenv func(string) string) func(string) string {
	security := validSecurityValues()
	return func(name string) string {
		if value, ok := security[name]; ok {
			return value
		}
		return getenv(name)
	}
}
```

Wrap every existing `LoadAPIConfig` callback in that file with `withValidSecurity`. For example, change:
Specifically, in `TestLoadAPIConfigReadsAPISettings`,
`TestLoadAPIConfigRequiresDatabaseURL`,
`TestLoadAPIConfigRequiresTemporalAddress`,
`TestLoadAPIConfigUsesDefaults`, and
`TestLoadAPIConfigRejectsUnknownArtifactStoreKind`, change each opening call
from `LoadAPIConfig(func(key string) string {` to
`LoadAPIConfig(withValidSecurity(func(key string) string {`, and change its
matching closing `})` to `}))`. This preserves each test's database, Temporal,
and artifact-store switch while ensuring security parsing succeeds first.

In `TestLoadAPIConfigReadsAPISettings`, add this assertion after the existing
field assertions to prove the integration point is populated:

```go
if cfg.Security.TenantID != "tenant_123" {
	t.Fatalf("Security.TenantID = %q, want tenant_123", cfg.Security.TenantID)
}
```

- [ ] **Step 7: Run the complete configuration package**

Run:

```bash
rtk gofmt -w internal/config/config.go internal/config/config_test.go
rtk go test ./internal/config -count=1
```

Expected: all configuration tests pass, including pre-existing worker tests.

- [ ] **Step 8: Commit the typed configuration slice**

```bash
rtk git add internal/config/auth.go internal/config/auth_test.go internal/config/config.go internal/config/config_test.go
rtk git commit -m "feat: add auth runtime configuration"
```

---

### Task 2: Fail Fast and Prove Startup Log Redaction

**Files:**
- Modify: `cmd/api/main.go:3-54`
- Modify: `cmd/api/main_test.go:1-297`

**Interfaces:**
- Consumes: `config.LoadAPIConfig`, `SecurityConfig`, and `apiDependencies`.
- Produces: `writeStartupError(io.Writer, error)` and a complete reusable API test environment.

- [ ] **Step 1: Replace partial API test environments with one complete environment**

Replace `apiTestEnv` in `cmd/api/main_test.go` with:

```go
func apiTestValues() map[string]string {
	return map[string]string{
		"DATABASE_URL":                  "postgres://user:pass@localhost:5432/db?sslmode=disable",
		"HTTP_ADDRESS":                 ":9090",
		"TEMPORAL_ADDRESS":             "localhost:7233",
		"TEMPORAL_NAMESPACE":           "tflive",
		"TEMPORAL_TASK_QUEUE":          "terraform-runs-dev",
		"WORKER_RUN_ROOT":              "/var/lib/tflive/runs",
		"ARTIFACT_STORE_KIND":          "filesystem",
		"ARTIFACT_STORE_FILESYSTEM_ROOT": "/var/lib/tflive/artifacts",
		"TFLIVE_ENVIRONMENT":           "development",
		"TFLIVE_TENANT_ID":             "tenant_123",
		"OIDC_ISSUER_URL":              "http://localhost:8082/realms/tflive",
		"OIDC_AUDIENCE":                "tflive-api",
		"OPENFGA_API_URL":              "http://localhost:8080",
		"OPENFGA_STORE_ID":             "store-id",
		"OPENFGA_MODEL_ID":             "model-id",
		"OPENFGA_API_TOKEN":            "",
		"OPENFGA_HTTP_TIMEOUT":         "10s",
	}
}

func apiTestEnv(name string) string {
	return apiTestValues()[name]
}

func apiTestGetenv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
```

Update the two existing required-value tests to start from this map and delete only the value under test:

```go
func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	values := apiTestValues()
	delete(values, "DATABASE_URL")
	err := run(context.Background(), apiTestGetenv(values))
	if !errors.Is(err, config.ErrInvalidConfig) || err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("error = %v, want DATABASE_URL ErrInvalidConfig", err)
	}
}

func TestRunRequiresTemporalAddress(t *testing.T) {
	t.Parallel()

	values := apiTestValues()
	delete(values, "TEMPORAL_ADDRESS")
	err := run(context.Background(), apiTestGetenv(values))
	if !errors.Is(err, config.ErrInvalidConfig) || err == nil || !strings.Contains(err.Error(), "TEMPORAL_ADDRESS is required") {
		t.Fatalf("error = %v, want TEMPORAL_ADDRESS ErrInvalidConfig", err)
	}
}
```

Update `TestRunUsesDefaultTemporalTaskQueue` by deleting `TEMPORAL_TASK_QUEUE` from `apiTestValues()` and passing `apiTestGetenv(values)`. Update `TestRunMigratesRealPostgresWhenDSNIsSet` by setting `values["DATABASE_URL"] = dsn` and passing the resulting getter. Leave tests that already use `apiTestEnv` unchanged.

- [ ] **Step 2: Add failing fail-fast and logging regression tests**

Add to `cmd/api/main_test.go`:

```go
func TestRunRejectsSecurityConfigBeforeDependencies(t *testing.T) {
	t.Parallel()

	values := apiTestValues()
	delete(values, "TFLIVE_TENANT_ID")
	postgresCalled := false
	deps := apiDependencies{
		newPostgresPool: func(context.Context, string) (postgresPool, error) {
			postgresCalled = true
			return nil, nil
		},
	}

	err := runWithDependencies(context.Background(), apiTestGetenv(values), deps)
	if !errors.Is(err, config.ErrInvalidConfig) || err == nil || !strings.Contains(err.Error(), "TFLIVE_TENANT_ID is required") {
		t.Fatalf("error = %v, want tenant ErrInvalidConfig", err)
	}
	if postgresCalled {
		t.Fatal("Postgres initialization ran after invalid security configuration")
	}
}

func TestWriteStartupErrorDoesNotLeakSecuritySecrets(t *testing.T) {
	t.Parallel()

	values := apiTestValues()
	values["TFLIVE_ENVIRONMENT"] = "production"
	values["OIDC_ISSUER_URL"] = "https://client:oidc-client-secret-sentinel@id.example.com/realms/tflive"
	values["OPENFGA_API_URL"] = "https://openfga.example.com"
	values["OPENFGA_API_TOKEN"] = "openfga-api-token-sentinel"
	values["KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD"] = "bootstrap-password-sentinel"

	err := runWithDependencies(context.Background(), apiTestGetenv(values), apiDependencies{})
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
	var output bytes.Buffer
	writeStartupError(&output, err)
	for _, secret := range []string{
		"oidc-client-secret-sentinel",
		"openfga-api-token-sentinel",
		"bootstrap-password-sentinel",
	} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("startup log leaked %q: %s", secret, output.String())
		}
	}
	if !strings.Contains(output.String(), "OIDC_ISSUER_URL must not include user information") {
		t.Fatalf("startup log = %q", output.String())
	}
}
```

- [ ] **Step 3: Run the focused startup tests and confirm the log seam is missing**

Run:

```bash
rtk go test ./cmd/api -run 'TestRunRejectsSecurityConfigBeforeDependencies|TestWriteStartupErrorDoesNotLeakSecuritySecrets' -count=1
```

Expected: compilation fails because `writeStartupError` does not exist.

- [ ] **Step 4: Make startup logging testable without changing exit semantics**

Add `io` to `cmd/api/main.go` imports and replace `main` with:

```go
func main() {
	if err := run(context.Background(), os.Getenv); err != nil {
		writeStartupError(os.Stderr, err)
		os.Exit(1)
	}
}

func writeStartupError(writer io.Writer, err error) {
	log.New(writer, "", log.LstdFlags).Printf("tflive API failed: %v", err)
}
```

Keep `log` imported because `listenAndServe` also uses the package logger.

- [ ] **Step 5: Run and format the API startup package**

Run:

```bash
rtk gofmt -w cmd/api/main.go cmd/api/main_test.go
rtk go test ./cmd/api -count=1
```

Expected: all API command tests pass; invalid security configuration never invokes Postgres initialization, and the captured log contains no sentinel secrets.

- [ ] **Step 6: Commit the startup integration slice**

```bash
rtk git add cmd/api/main.go cmd/api/main_test.go
rtk git commit -m "test: enforce secure API startup config"
```

---

### Task 3: Give Every OpenFGA Request an Explicit Context Deadline

**Files:**
- Modify: `internal/openfga/client.go:28-40,196-254`
- Modify: `internal/openfga/client_test.go:1-146`

**Interfaces:**
- Consumes: `Config.HTTPTimeout`, `defaultHTTPTimeout`, and caller contexts.
- Produces: `Client.timeout time.Duration`; every `doJSON` request uses `context.WithTimeout` without extending an earlier parent deadline.

- [ ] **Step 1: Add failing request-deadline tests**

Add the following imports to `internal/openfga/client_test.go` if they are not already present: `errors`, `io`, and `time`.

Add these tests and helpers:

```go
func TestClientAddsConfiguredRequestDeadline(t *testing.T) {
	client := testClient(t, "http://openfga.test", "")
	client.timeout = 75 * time.Millisecond
	client.http.Timeout = client.timeout
	var gotDeadline time.Time
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var ok bool
		gotDeadline, ok = request.Context().Deadline()
		if !ok {
			return nil, fmt.Errorf("request context has no deadline")
		}
		return storeResponse(request), nil
	})

	started := time.Now()
	if _, err := client.GetStore(context.Background(), "store-id"); err != nil {
		t.Fatal(err)
	}
	if !gotDeadline.After(started) || gotDeadline.After(started.Add(client.timeout+25*time.Millisecond)) {
		t.Fatalf("request deadline = %s, started = %s, timeout = %s", gotDeadline, started, client.timeout)
	}
}

func TestClientPreservesEarlierCallerDeadline(t *testing.T) {
	client := testClient(t, "http://openfga.test", "")
	client.timeout = time.Second
	client.http.Timeout = client.timeout
	var gotDeadline time.Time
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var ok bool
		gotDeadline, ok = request.Context().Deadline()
		if !ok {
			return nil, fmt.Errorf("request context has no deadline")
		}
		return storeResponse(request), nil
	})

	parentDeadline := time.Now().Add(100 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), parentDeadline)
	defer cancel()
	if _, err := client.GetStore(ctx, "store-id"); err != nil {
		t.Fatal(err)
	}
	if gotDeadline.After(parentDeadline.Add(5 * time.Millisecond)) {
		t.Fatalf("request deadline %s extended parent deadline %s", gotDeadline, parentDeadline)
	}
}

func TestClientDefaultsNonPositiveTimeout(t *testing.T) {
	parsed, err := url.Parse("http://openfga.test")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Config{APIURL: parsed})
	if client.timeout != defaultHTTPTimeout {
		t.Fatalf("client timeout = %s, want %s", client.timeout, defaultHTTPTimeout)
	}
	if client.http.Timeout != defaultHTTPTimeout {
		t.Fatalf("HTTP timeout = %s, want %s", client.http.Timeout, defaultHTTPTimeout)
	}
}

func TestClientDeadlineCancellationSurfaces(t *testing.T) {
	client := testClient(t, "http://openfga.test", "")
	client.timeout = 10 * time.Millisecond
	client.http.Timeout = client.timeout
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})

	_, err := client.GetStore(context.Background(), "store-id")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func storeResponse(request *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"store-id","name":"tflive"}`)),
		Request:    request,
	}
}
```

- [ ] **Step 2: Run the focused tests and confirm the deadline field is absent**

Run:

```bash
rtk go test ./internal/openfga -run 'TestClient(AddsConfiguredRequestDeadline|PreservesEarlierCallerDeadline|DefaultsNonPositiveTimeout|DeadlineCancellationSurfaces)' -count=1
```

Expected: compilation fails because `Client.timeout` does not exist.

- [ ] **Step 3: Store a normalized timeout in the client**

Change `Client` and `NewClient` in `internal/openfga/client.go` to:

```go
type Client struct {
	baseURL *url.URL
	token   string
	timeout time.Duration
	http    *http.Client
}

func NewClient(cfg Config) *Client {
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	return &Client{
		baseURL: cfg.APIURL,
		token:   cfg.APIToken,
		timeout: timeout,
		http:    &http.Client{Timeout: timeout},
	}
}
```

Add `time` to the imports in `internal/openfga/client.go`.

- [ ] **Step 4: Derive the per-request deadline in `doJSON`**

Immediately before `http.NewRequestWithContext`, add:

```go
	requestContext, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, method, endpoint.String(), body)
```

Replace the existing `http.NewRequestWithContext(ctx, ...)` line rather than creating a second request. Do not change request headers, response bounds, or token redaction.

- [ ] **Step 5: Run all OpenFGA tests**

Run:

```bash
rtk gofmt -w internal/openfga/client.go internal/openfga/client_test.go
rtk go test ./internal/openfga -count=1
```

Expected: all OpenFGA model, config, client, provisioner, and non-live tests pass.

- [ ] **Step 6: Commit the request-deadline slice**

```bash
rtk git add internal/openfga/client.go internal/openfga/client_test.go
rtk git commit -m "fix: bound OpenFGA requests with deadlines"
```

---

### Task 4: Document and Verify the Runtime Environment Contract

**Files:**
- Modify: `.env.example:42-88`
- Modify: `scripts/verify-auth-compose.mjs:1-30`
- Modify: `docs/authentication.md:96-128`

**Interfaces:**
- Consumes: the exact environment names and defaults produced by Task 1.
- Produces: a machine-checked local example and operator-facing development/production guidance.

- [ ] **Step 1: Extend the environment contract test before editing the example**

Near the top of `scripts/verify-auth-compose.mjs`, after reading `docker-compose.yaml`, add:

```js
const envExample = readFileSync(resolve(root, ".env.example"), "utf8");

function envValue(name) {
  const prefix = `${name}=`;
  const matches = envExample
    .split(/\r?\n/)
    .filter((line) => line.startsWith(prefix));
  assert.equal(matches.length, 1, `${name} must appear exactly once in .env.example`);
  return matches[0].slice(prefix.length);
}

for (const [name, value] of Object.entries({
  TFLIVE_ENVIRONMENT: "development",
  TFLIVE_TENANT_ID: "tenant_123",
  OIDC_ISSUER_URL: "http://localhost:8082/realms/tflive",
  OIDC_AUDIENCE: "tflive-api",
  OPENFGA_API_URL: "http://localhost:8080",
  OPENFGA_STORE_ID: "",
  OPENFGA_MODEL_ID: "",
  OPENFGA_API_TOKEN: "",
  OPENFGA_HTTP_TIMEOUT: "10s",
})) {
  assert.equal(envValue(name), value, `${name} has the wrong local example value`);
}
```

- [ ] **Step 2: Run the contract test and confirm the new API values are missing**

Run:

```bash
rtk node scripts/verify-auth-compose.mjs
```

Expected: failure naming `TFLIVE_ENVIRONMENT` as missing from `.env.example`.

- [ ] **Step 3: Add the API runtime block to `.env.example`**

Change the authentication section comment so it says the section is used by the API runtime and local Compose services. Insert this block before the Keycloak database values:

```dotenv
# API authentication and authorization runtime.
# Empty TFLIVE_ENVIRONMENT also means development; set production explicitly
# to enable HTTPS and OpenFGA credential enforcement.
TFLIVE_ENVIRONMENT=development
TFLIVE_TENANT_ID=tenant_123
OIDC_ISSUER_URL=http://localhost:8082/realms/tflive
OIDC_AUDIENCE=tflive-api
OPENFGA_API_URL=http://localhost:8080

```

Keep the existing `OPENFGA_STORE_ID`, `OPENFGA_MODEL_ID`, `OPENFGA_API_TOKEN`, and `OPENFGA_HTTP_TIMEOUT` lines exactly once. Keep both generated IDs blank.

- [ ] **Step 4: Add the API runtime table and policy to the auth runbook**

Rename the current `## Runtime Configuration` heading in `docs/authentication.md` to `## Keycloak Provisioner Configuration`. After its existing redaction paragraph, add:

```markdown
## API Runtime Security Configuration

The API validates its authentication and authorization configuration before it
connects to Postgres or Temporal or starts its HTTP listener.

| Variable | Sensitive | Purpose |
|---|---:|---|
| `TFLIVE_ENVIRONMENT` | No | Optional runtime mode; empty defaults to `development`; valid values are `development` and `production` |
| `TFLIVE_TENANT_ID` | No | Required single configured tenant identifier |
| `OIDC_ISSUER_URL` | No | Required exact Keycloak issuer URL |
| `OIDC_AUDIENCE` | No | Required access-token audience; local value is `tflive-api` |
| `OPENFGA_API_URL` | No | Required OpenFGA API base URL |
| `OPENFGA_STORE_ID` | No | Required exact store ID emitted by bootstrap |
| `OPENFGA_MODEL_ID` | No | Required exact immutable model ID emitted by bootstrap |
| `OPENFGA_API_TOKEN` | Yes | Optional for local development and required in production |
| `OPENFGA_HTTP_TIMEOUT` | No | Positive per-request deadline; defaults to `10s` |

Development permits the documented loopback HTTP issuer, local HTTP OpenFGA
endpoint, and tokenless OpenFGA service. Production must be selected explicitly
with `TFLIVE_ENVIRONMENT=production`; it requires HTTPS for both external
dependencies and a non-empty OpenFGA bearer token. Unknown modes, malformed
tenant IDs, unsafe URLs or identifiers, and non-positive timeouts stop startup.

The OpenFGA store and model IDs are never discovered at API startup. Copy the
exact assignments printed by the serialized bootstrap command into runtime
configuration before starting the API. Keycloak bootstrap passwords and
provisioner administrator tokens are not API runtime credentials.
```

- [ ] **Step 5: Verify the example, documentation, and focused Go packages**

Run:

```bash
rtk node scripts/verify-auth-compose.mjs
rtk go test ./internal/config ./cmd/api ./internal/openfga -count=1
rtk git diff --check
```

Expected: the Compose/environment contract prints `authentication Compose contract verified`; all three Go packages pass; diff check produces no output.

- [ ] **Step 6: Commit the operational contract**

```bash
rtk git add .env.example scripts/verify-auth-compose.mjs docs/authentication.md
rtk git commit -m "docs: describe auth runtime configuration"
```

---

### Task 5: Run the Release-Level Verification and Complete AUTH-005

**Files:**
- Modify: `docs/sprint/authn_and_authz/README.md:68`

**Interfaces:**
- Consumes: all deliverables from Tasks 1 through 4.
- Produces: complete repository verification evidence and AUTH-005 backlog status `Done`.

- [ ] **Step 1: Run the complete Go suite**

Run:

```bash
rtk go test ./... -count=1
```

Expected: every Go package passes with zero failures. Tests requiring an opt-in external DSN or live OpenFGA remain skipped unless their documented environment variables are present.

- [ ] **Step 2: Run the frontend regression suite**

From `web/`, run:

```bash
rtk npm test
```

Expected: Vitest exits zero with all existing tests passing.

- [ ] **Step 3: Build the frontend production bundle**

From `web/`, run:

```bash
rtk npm run build
```

Expected: TypeScript and Vite complete successfully and emit the production bundle.

- [ ] **Step 4: Run configuration and repository hygiene checks**

From the repository root, run:

```bash
rtk node scripts/verify-auth-compose.mjs
rtk git diff --check
rtk git status --short
```

Expected: the authentication contract passes, diff check is silent, and status lists only the intended AUTH-005 files.

- [ ] **Step 5: Mark AUTH-005 done only after all verification passes**

In the AUTH-005 row of `docs/sprint/authn_and_authz/README.md`, change only the final status cell:

```markdown
| AUTH-005 | Backend foundation | Add authentication and authorization configuration | Define validated runtime configuration for the OIDC issuer and audience, configured tenant, OpenFGA endpoint, store, model, credentials, timeouts, and production mode. | Configuration has typed parsing and actionable validation errors.<br>The API refuses to start when required production values are absent or insecure.<br>No bootstrap passwords, client secrets, or API tokens are logged.<br>The configured tenant ID is non-empty and validated.<br>OpenFGA calls have explicit deadlines.<br>Configuration tests cover valid, missing, malformed, development, and production cases. | AUTH-003, AUTH-004 | P0 | Done |
```

- [ ] **Step 6: Re-run the final documentation checks**

Run:

```bash
rtk node scripts/verify-auth-compose.mjs
rtk git diff --check
```

Expected: the contract passes and diff check remains silent.

- [ ] **Step 7: Commit ticket completion**

```bash
rtk git add docs/sprint/authn_and_authz/README.md
rtk git commit -m "docs: complete auth runtime config ticket"
```

- [ ] **Step 8: Record final evidence for the pull request**

Run:

```bash
rtk git log --oneline --decorate -6
rtk git status --short --branch
```

Expected: the task commits are visible, the worktree is clean, and the branch is ahead of its base by the implementation commits. The pull-request summary must list the Go suite, web suite, production build, environment contract, secret-redaction tests, and explicit-deadline tests.
