# OpenFGA Model Provisioning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Add a versioned OpenFGA stack-role model, exhaustive permission tests, and a safe two-phase bootstrap/verification provisioner that emits explicit store and model IDs for environment configuration.

**Architecture:** Keep the canonical OpenFGA API model and model tests in a small top-level asset package. Add a standard-library Go package for typed configuration, canonical model comparison, bounded REST calls, and idempotent store/model reconciliation; expose it through a one-shot command and unprivileged Compose service. Bootstrap may discover the uniquely named store, while normal verification and later runtime use require exact environment-supplied IDs.

**Tech Stack:** Go 1.24.0 with toolchain 1.24.1, Go standard library, OpenFGA schema 1.1, OpenFGA server v1.15.1, OpenFGA CLI v0.7.15, Docker Compose, Node.js contract tests.

**Issue:** [AUTH-004](https://github.com/vishu42/tflive/issues/6)

**Design:** [2026-07-15-openfga-model-provisioning-design.md](../specs/2026-07-15-openfga-model-provisioning-design.md)

## Global Constraints

- Run every shell command through rtk.
- Preserve canonical subject and object forms user:<keycloak-sub> and stack:<stack-id>.
- Only owner, operator, approver, and viewer accept direct user relationships.
- can_view, can_operate, can_approve, and can_manage_access never accept direct relationships.
- The application and verify operation must use explicit OPENFGA_STORE_ID and OPENFGA_MODEL_ID values and must never select latest implicitly.
- Bootstrap is serialized; duplicate named stores or duplicate semantically identical models fail closed.
- The OpenFGA REST client uses only the Go standard library and limits every response body.
- OPENFGA_API_TOKEN, authorization headers, and request bodies never appear in logs or errors.
- No generated store ID, model ID, token, password, or usable production default is committed.
- Use TDD for every Go, command, Compose, and integration change.
- Keep unrelated user changes intact.

## File Map

**Create**

- openfga/authorization-model.json — canonical API-format schema 1.1 model.
- openfga/authorization-model-tests.fga.yaml — complete role-permission matrix.
- openfga/model.go — embeds and returns a defensive copy of the canonical model.
- internal/openfga/model.go — parses, validates, normalizes, and compares models.
- internal/openfga/model_test.go — parser, semantic-comparison, and direct-assignment tests.
- internal/openfga/config.go — typed provisioner configuration.
- internal/openfga/config_test.go — configuration validation tests.
- internal/openfga/client.go — bounded store/model REST client.
- internal/openfga/client_test.go — HTTP, pagination, escaping, and redaction tests.
- internal/openfga/provisioner.go — bootstrap and verify orchestration.
- internal/openfga/provisioner_test.go — stateful idempotency and failure tests.
- cmd/openfga-provisioner/main.go — signal-aware one-shot command.
- cmd/openfga-provisioner/main_test.go — command/output/cancellation tests.
- Dockerfile.openfga-provisioner — pinned unprivileged provisioner image.
- internal/openfga/live_test.go — opt-in real OpenFGA compatibility and rejected-write tests.

**Modify**

- docker-compose.yaml — add openfga-provision after the healthy OpenFGA service.
- .env.example — document generated IDs and optional token/timeout inputs.
- scripts/verify-auth-compose.mjs — assert the provisioner Compose contract.
- README.md — document bootstrap, environment recording, and verification.
- docs/authentication.md — document the model, matrix, reruns, upgrades, and recovery.
- docs/sprint/authn_and_authz/README.md — mark AUTH-004 Done only after final evidence.

---

### Task 1: Define the canonical stack authorization model

**Files:**

- Create: openfga/authorization-model-tests.fga.yaml
- Create: openfga/authorization-model.json
- Create: openfga/model.go
- Modify: docs/sprint/authn_and_authz/README.md

**Interfaces:**

- Consumes: OpenFGA schema 1.1 and the approved role matrix.
- Produces: func AuthorizationModelJSON() []byte in package openfgamodel; the pinned CLI test command used by all later verification.

- [ ] **Step 1: Mark AUTH-004 In Progress**

Change only the AUTH-004 status cell in docs/sprint/authn_and_authz/README.md from Not Started to In Progress.

Run:

~~~bash
rtk rg -n 'AUTH-004.*In Progress' docs/sprint/authn_and_authz/README.md
~~~

Expected: exactly one matching backlog row.

- [ ] **Step 2: Write the failing model test file**

Create openfga/authorization-model-tests.fga.yaml before the referenced model exists:

~~~yaml
name: tflive stack authorization
model_file: ./authorization-model.json
tuples:
  - user: user:owner
    relation: owner
    object: stack:example
  - user: user:operator
    relation: operator
    object: stack:example
  - user: user:approver
    relation: approver
    object: stack:example
  - user: user:viewer
    relation: viewer
    object: stack:example
tests:
  - name: every role permission combination
    check:
      - user: user:owner
        object: stack:example
        assertions:
          can_view: true
          can_operate: true
          can_approve: true
          can_manage_access: true
      - user: user:operator
        object: stack:example
        assertions:
          can_view: true
          can_operate: true
          can_approve: false
          can_manage_access: false
      - user: user:approver
        object: stack:example
        assertions:
          can_view: true
          can_operate: false
          can_approve: true
          can_manage_access: false
      - user: user:viewer
        object: stack:example
        assertions:
          can_view: true
          can_operate: false
          can_approve: false
          can_manage_access: false
      - user: user:unrelated
        object: stack:example
        assertions:
          can_view: false
          can_operate: false
          can_approve: false
          can_manage_access: false
~~~

- [ ] **Step 3: Run the model test and confirm the missing model failure**

Run:

~~~bash
rtk docker run --rm -v "$PWD:/workspace" -w /workspace openfga/cli:v0.7.15 model test --tests openfga/authorization-model-tests.fga.yaml
~~~

Expected: FAIL because openfga/authorization-model.json does not exist.

- [ ] **Step 4: Add the minimal canonical model**

Create openfga/authorization-model.json:

~~~json
{
  "schema_version": "1.1",
  "type_definitions": [
    {
      "type": "user"
    },
    {
      "type": "stack",
      "relations": {
        "owner": {
          "this": {}
        },
        "operator": {
          "this": {}
        },
        "approver": {
          "this": {}
        },
        "viewer": {
          "this": {}
        },
        "can_view": {
          "union": {
            "child": [
              {
                "computedUserset": {
                  "relation": "owner"
                }
              },
              {
                "computedUserset": {
                  "relation": "operator"
                }
              },
              {
                "computedUserset": {
                  "relation": "approver"
                }
              },
              {
                "computedUserset": {
                  "relation": "viewer"
                }
              }
            ]
          }
        },
        "can_operate": {
          "union": {
            "child": [
              {
                "computedUserset": {
                  "relation": "owner"
                }
              },
              {
                "computedUserset": {
                  "relation": "operator"
                }
              }
            ]
          }
        },
        "can_approve": {
          "union": {
            "child": [
              {
                "computedUserset": {
                  "relation": "owner"
                }
              },
              {
                "computedUserset": {
                  "relation": "approver"
                }
              }
            ]
          }
        },
        "can_manage_access": {
          "computedUserset": {
            "relation": "owner"
          }
        }
      },
      "metadata": {
        "relations": {
          "owner": {
            "directly_related_user_types": [
              {
                "type": "user"
              }
            ]
          },
          "operator": {
            "directly_related_user_types": [
              {
                "type": "user"
              }
            ]
          },
          "approver": {
            "directly_related_user_types": [
              {
                "type": "user"
              }
            ]
          },
          "viewer": {
            "directly_related_user_types": [
              {
                "type": "user"
              }
            ]
          }
        }
      }
    }
  ]
}
~~~

Create openfga/model.go:

~~~go
package openfgamodel

import _ "embed"

//go:embed authorization-model.json
var authorizationModelJSON []byte

// AuthorizationModelJSON returns a defensive copy of the canonical model.
func AuthorizationModelJSON() []byte {
	return append([]byte(nil), authorizationModelJSON...)
}
~~~

- [ ] **Step 5: Run the model tests and package compile**

Run:

~~~bash
rtk docker run --rm -v "$PWD:/workspace" -w /workspace openfga/cli:v0.7.15 model test --tests openfga/authorization-model-tests.fga.yaml
rtk go test ./openfga -count=1
~~~

Expected: the CLI reports 1/1 tests and 20/20 checks passing; the Go package passes.

- [ ] **Step 6: Commit the canonical model and In Progress status**

~~~bash
rtk git add openfga/authorization-model.json openfga/authorization-model-tests.fga.yaml openfga/model.go docs/sprint/authn_and_authz/README.md
rtk git commit -m 'feat: define OpenFGA stack authorization model'
~~~

### Task 2: Parse and compare authorization models semantically

**Files:**

- Create: internal/openfga/model_test.go
- Create: internal/openfga/model.go

**Interfaces:**

- Consumes: openfgamodel.AuthorizationModelJSON() []byte.
- Produces: AuthorizationModel, ParseAuthorizationModel([]byte) (AuthorizationModel, error), CanonicalJSON(AuthorizationModel) ([]byte, error), and ModelsEqual(AuthorizationModel, AuthorizationModel) (bool, error).

- [ ] **Step 1: Write failing parser, equality, and policy-structure tests**

Create internal/openfga/model_test.go with these tests:

~~~go
package openfga

import (
	"encoding/json"
	"strings"
	"testing"

	openfgamodel "github.com/vishu42/tflive/openfga"
)

func TestParseAuthorizationModelRejectsInvalidModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "malformed JSON", data: "{", want: "decode authorization model"},
		{name: "wrong schema", data: `{"schema_version":"1.0","type_definitions":[{"type":"user"}]}`, want: "schema_version must be 1.1"},
		{name: "no types", data: `{"schema_version":"1.1","type_definitions":[]}`, want: "type_definitions must not be empty"},
		{name: "duplicate type", data: `{"schema_version":"1.1","type_definitions":[{"type":"user"},{"type":"user"}]}`, want: `duplicate type "user"`},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseAuthorizationModel([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseAuthorizationModel() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestModelsEqualIgnoresNonSemanticOrderingAndIDs(t *testing.T) {
	t.Parallel()

	first, err := ParseAuthorizationModel(openfgamodel.AuthorizationModelJSON())
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseAuthorizationModel(openfgamodel.AuthorizationModelJSON())
	if err != nil {
		t.Fatal(err)
	}
	second.ID = "server-generated"
	second.TypeDefinitions[0], second.TypeDefinitions[1] = second.TypeDefinitions[1], second.TypeDefinitions[0]

	equal, err := ModelsEqual(first, second)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatal("semantically identical models compared unequal")
	}
}

func TestModelsEqualDetectsPermissionRewriteChanges(t *testing.T) {
	t.Parallel()

	first, err := ParseAuthorizationModel(openfgamodel.AuthorizationModelJSON())
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseAuthorizationModel(openfgamodel.AuthorizationModelJSON())
	if err != nil {
		t.Fatal(err)
	}
	for index := range second.TypeDefinitions {
		if second.TypeDefinitions[index].Type == "stack" {
			second.TypeDefinitions[index].Relations["can_manage_access"] = json.RawMessage(`{"computedUserset":{"relation":"viewer"}}`)
		}
	}

	equal, err := ModelsEqual(first, second)
	if err != nil {
		t.Fatal(err)
	}
	if equal {
		t.Fatal("security-relevant rewrite change compared equal")
	}
}

func TestCanonicalModelAllowsDirectWritesOnlyToRoles(t *testing.T) {
	t.Parallel()

	model, err := ParseAuthorizationModel(openfgamodel.AuthorizationModelJSON())
	if err != nil {
		t.Fatal(err)
	}
	stack, ok := model.TypeDefinition("stack")
	if !ok {
		t.Fatal("canonical model is missing stack type")
	}

	directRoles := map[string]bool{
		"owner": true, "operator": true, "approver": true, "viewer": true,
	}
	for relation := range stack.Relations {
		metadata := stack.Metadata.Relations[relation]
		hasDirectType := len(metadata.DirectlyRelatedUserTypes) != 0
		if hasDirectType != directRoles[relation] {
			t.Fatalf("relation %s direct assignment = %v", relation, hasDirectType)
		}
		for _, related := range metadata.DirectlyRelatedUserTypes {
			if related.Type != "user" || related.Relation != "" || related.Wildcard != nil {
				t.Fatalf("relation %s has unsafe direct type %#v", relation, related)
			}
		}
	}
}
~~~

- [ ] **Step 2: Run the focused tests and verify they fail**

Run:

~~~bash
rtk go test ./internal/openfga -run 'TestParseAuthorizationModel|TestModelsEqual|TestCanonicalModel' -count=1
~~~

Expected: FAIL because the internal/openfga package and model functions do not exist.

- [ ] **Step 3: Implement the typed model and deterministic canonicalization**

Create internal/openfga/model.go with these declarations and behaviors:

~~~go
package openfga

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

type AuthorizationModel struct {
	ID              string                     `json:"id,omitempty"`
	SchemaVersion   string                     `json:"schema_version"`
	TypeDefinitions []TypeDefinition           `json:"type_definitions"`
	Conditions      map[string]json.RawMessage `json:"conditions,omitempty"`
}

type TypeDefinition struct {
	Type      string                 `json:"type"`
	Relations map[string]json.RawMessage `json:"relations,omitempty"`
	Metadata  TypeDefinitionMetadata `json:"metadata,omitempty"`
}

type TypeDefinitionMetadata struct {
	Relations map[string]RelationMetadata `json:"relations,omitempty"`
}

type RelationMetadata struct {
	DirectlyRelatedUserTypes []RelationReference `json:"directly_related_user_types,omitempty"`
}

type RelationReference struct {
	Type      string          `json:"type"`
	Relation  string          `json:"relation,omitempty"`
	Wildcard  json.RawMessage `json:"wildcard,omitempty"`
	Condition string          `json:"condition,omitempty"`
}

func ParseAuthorizationModel(data []byte) (AuthorizationModel, error) {
	var model AuthorizationModel
	if err := json.Unmarshal(data, &model); err != nil {
		return AuthorizationModel{}, fmt.Errorf("decode authorization model: %w", err)
	}
	if model.SchemaVersion != "1.1" {
		return AuthorizationModel{}, fmt.Errorf("authorization model schema_version must be 1.1")
	}
	if len(model.TypeDefinitions) == 0 {
		return AuthorizationModel{}, fmt.Errorf("authorization model type_definitions must not be empty")
	}
	seen := make(map[string]struct{}, len(model.TypeDefinitions))
	for _, definition := range model.TypeDefinitions {
		if definition.Type == "" {
			return AuthorizationModel{}, fmt.Errorf("authorization model type must not be empty")
		}
		if _, exists := seen[definition.Type]; exists {
			return AuthorizationModel{}, fmt.Errorf("authorization model has duplicate type %q", definition.Type)
		}
		seen[definition.Type] = struct{}{}
		for relation, rewrite := range definition.Relations {
			if relation == "" || !json.Valid(rewrite) {
				return AuthorizationModel{}, fmt.Errorf("authorization model type %q has invalid relation %q", definition.Type, relation)
			}
		}
	}
	return model, nil
}

func (model AuthorizationModel) TypeDefinition(name string) (TypeDefinition, bool) {
	for _, definition := range model.TypeDefinitions {
		if definition.Type == name {
			return definition, true
		}
	}
	return TypeDefinition{}, false
}

func CanonicalJSON(model AuthorizationModel) ([]byte, error) {
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("copy authorization model: %w", err)
	}
	normalized, err := ParseAuthorizationModel(encoded)
	if err != nil {
		return nil, fmt.Errorf("validate authorization model: %w", err)
	}
	normalized.ID = ""
	sort.Slice(normalized.TypeDefinitions, func(i, j int) bool {
		return normalized.TypeDefinitions[i].Type < normalized.TypeDefinitions[j].Type
	})
	for index := range normalized.TypeDefinitions {
		definition := &normalized.TypeDefinitions[index]
		for name, rewrite := range definition.Relations {
			canonical, err := canonicalRawJSON(rewrite)
			if err != nil {
				return nil, fmt.Errorf("canonicalize %s#%s: %w", definition.Type, name, err)
			}
			definition.Relations[name] = canonical
		}
		for name, metadata := range definition.Metadata.Relations {
			sort.Slice(metadata.DirectlyRelatedUserTypes, func(i, j int) bool {
				left, _ := json.Marshal(metadata.DirectlyRelatedUserTypes[i])
				right, _ := json.Marshal(metadata.DirectlyRelatedUserTypes[j])
				return bytes.Compare(left, right) < 0
			})
			definition.Metadata.Relations[name] = metadata
		}
	}
	for name, condition := range normalized.Conditions {
		canonical, err := canonicalRawJSON(condition)
		if err != nil {
			return nil, fmt.Errorf("canonicalize condition %s: %w", name, err)
		}
		normalized.Conditions[name] = canonical
	}
	return json.Marshal(normalized)
}

func ModelsEqual(left, right AuthorizationModel) (bool, error) {
	leftJSON, err := CanonicalJSON(left)
	if err != nil {
		return false, err
	}
	rightJSON, err := CanonicalJSON(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftJSON, rightJSON), nil
}

func canonicalRawJSON(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
~~~

- [ ] **Step 4: Format and run model tests**

Run:

~~~bash
rtk gofmt -w internal/openfga/model.go internal/openfga/model_test.go
rtk go test ./internal/openfga -run 'TestParseAuthorizationModel|TestModelsEqual|TestCanonicalModel' -count=1
rtk docker run --rm -v "$PWD:/workspace" -w /workspace openfga/cli:v0.7.15 model test --tests openfga/authorization-model-tests.fga.yaml
~~~

Expected: all Go tests pass and the CLI still reports the complete matrix passing.

- [ ] **Step 5: Commit model parsing**

~~~bash
rtk git add internal/openfga/model.go internal/openfga/model_test.go
rtk git commit -m 'feat: validate OpenFGA authorization models'
~~~

### Task 3: Add typed provisioner configuration

**Files:**

- Create: internal/openfga/config_test.go
- Create: internal/openfga/config.go

**Interfaces:**

- Consumes: environment lookup func(string) string.
- Produces: Config, LoadConfig(func(string) string) (Config, error), and Config.ValidateVerify() error.

- [ ] **Step 1: Write failing table-driven configuration tests**

Create internal/openfga/config_test.go:

~~~go
package openfga

import (
	"strings"
	"testing"
	"time"
)

func TestLoadConfigReadsValidValuesAndDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(mapEnv(map[string]string{
		"OPENFGA_API_URL":  "http://openfga:8080",
		"OPENFGA_STORE_ID": "store-id",
		"OPENFGA_MODEL_ID": "model-id",
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
		{name: "userinfo", values: map[string]string{"OPENFGA_API_URL": "http://user@openfga"}, want: "must not include user information"},
		{name: "query", values: map[string]string{"OPENFGA_API_URL": "http://openfga?x=1"}, want: "must not include a query"},
		{name: "fragment", values: map[string]string{"OPENFGA_API_URL": "http://openfga#x"}, want: "must not include a fragment"},
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

func TestValidateVerifyRejectsLineBreakingIdentifiers(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(mapEnv(map[string]string{
		"OPENFGA_API_URL":  "http://openfga:8080",
		"OPENFGA_STORE_ID": "store\nid",
		"OPENFGA_MODEL_ID": "model-id",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateVerify(); err == nil || !strings.Contains(err.Error(), "whitespace or control") {
		t.Fatalf("ValidateVerify() error = %v", err)
	}
}

func mapEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
~~~

- [ ] **Step 2: Run the configuration tests and confirm failure**

Run:

~~~bash
rtk go test ./internal/openfga -run 'TestLoadConfig|TestValidateVerify' -count=1
~~~

Expected: FAIL because Config and LoadConfig are undefined.

- [ ] **Step 3: Implement strict common configuration and verify-only ID validation**

Create internal/openfga/config.go:

~~~go
package openfga

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const (
	defaultStoreName  = "tflive"
	defaultHTTPTimeout = 10 * time.Second
)

type Config struct {
	APIURL      *url.URL
	StoreName  string
	StoreID    string
	ModelID    string
	APIToken   string
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
		APIURL:       apiURL,
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
~~~

- [ ] **Step 4: Format and run the package tests**

Run:

~~~bash
rtk gofmt -w internal/openfga/config.go internal/openfga/config_test.go
rtk go test ./internal/openfga -count=1
~~~

Expected: PASS.

- [ ] **Step 5: Commit configuration**

~~~bash
rtk git add internal/openfga/config.go internal/openfga/config_test.go
rtk git commit -m 'feat: validate OpenFGA provisioner config'
~~~

### Task 4: Add the bounded OpenFGA store/model REST client

**Files:**

- Create: internal/openfga/client_test.go
- Create: internal/openfga/client.go

**Interfaces:**

- Consumes: Config and AuthorizationModel.
- Produces: Store, ModelRecord, Client, NewClient(Config) *Client, ListStores, CreateStore, GetStore, ListAuthorizationModels, GetAuthorizationModel, and WriteAuthorizationModel.

- [ ] **Step 1: Write failing HTTP contract tests**

Create internal/openfga/client_test.go. Use httptest.Server and assert these exact contracts:

~~~go
package openfga

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestClientListsStoresAcrossPagesAndAuthenticates(t *testing.T) {
	t.Parallel()

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("continuation_token") {
		case "":
			fmt.Fprint(w, `{"stores":[{"id":"store-1","name":"other"}],"continuation_token":"next"}`)
		case "next":
			fmt.Fprint(w, `{"stores":[{"id":"store-2","name":"tflive"}]}`)
		default:
			t.Fatalf("unexpected continuation token %q", r.URL.Query().Get("continuation_token"))
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL, "test-secret")
	stores, err := client.ListStores(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(stores) != 2 || stores[1].ID != "store-2" {
		t.Fatalf("calls = %d stores = %#v", calls, stores)
	}
}

func TestClientUsesExactEscapedStoreAndModelPaths(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.EscapedPath(), "/stores/store%2Fid/authorization-models/model%2Fid"; got != want {
			t.Fatalf("EscapedPath = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"authorization_model":{"id":"model/id","schema_version":"1.1","type_definitions":[{"type":"user"}]}}`)
	}))
	defer server.Close()

	client := testClient(t, server.URL, "")
	model, err := client.GetAuthorizationModel(context.Background(), "store/id", "model/id")
	if err != nil {
		t.Fatal(err)
	}
	if model.ID != "model/id" {
		t.Fatalf("model = %#v", model)
	}
}

func TestClientBoundsAndRedactsServerErrors(t *testing.T) {
	t.Parallel()

	secret := "server-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, strings.Repeat(secret, 10000), http.StatusInternalServerError)
	}))
	defer server.Close()

	client := testClient(t, server.URL, secret)
	_, err := client.GetStore(context.Background(), "store-id")
	if err == nil {
		t.Fatal("GetStore() error = nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked token: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") || len(err.Error()) > 70000 {
		t.Fatalf("error was not bounded and redacted: length=%d error=%v", len(err.Error()), err)
	}
}

func TestClientRejectsMalformedJSONAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{")
	}))
	defer server.Close()

	client := testClient(t, server.URL, "")
	if _, err := client.ListStores(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("ListStores() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetStore(ctx, "store-id"); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("GetStore() error = %v", err)
	}
}

func testClient(t *testing.T, rawURL, token string) *Client {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return NewClient(Config{
		APIURL: parsed, APIToken: token, HTTPTimeout: time.Second,
	})
}
~~~

Add these endpoint and defensive-response tests to the same file:

~~~go
func TestClientStoreAndModelEndpointContracts(t *testing.T) {
	t.Parallel()

	model := AuthorizationModel{
		SchemaVersion: "1.1",
		TypeDefinitions: []TypeDefinition{{Type: "user"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/stores":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request["name"] != "tflive" || r.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("create store request = %#v content-type = %q", request, r.Header.Get("Content-Type"))
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"store-id","name":"tflive"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/stores/store-id":
			fmt.Fprint(w, `{"id":"store-id","name":"tflive"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/stores/store-id/authorization-models":
			responseModel := model
			responseModel.ID = "model-id"
			json.NewEncoder(w).Encode(map[string]any{"authorization_models": []AuthorizationModel{responseModel}})
		case r.Method == http.MethodGet && r.URL.Path == "/stores/store-id/authorization-models/model-id":
			responseModel := model
			responseModel.ID = "model-id"
			json.NewEncoder(w).Encode(map[string]any{"authorization_model": responseModel})
		case r.Method == http.MethodPost && r.URL.Path == "/stores/store-id/authorization-models":
			var request AuthorizationModel
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.ID != "" || request.SchemaVersion != "1.1" {
				t.Fatalf("write model request = %#v", request)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"authorization_model_id":"new-model-id"}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.EscapedPath())
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL, "")
	store, err := client.CreateStore(context.Background(), "tflive")
	if err != nil || store.ID != "store-id" {
		t.Fatalf("CreateStore() = %#v, %v", store, err)
	}
	if _, err := client.GetStore(context.Background(), store.ID); err != nil {
		t.Fatal(err)
	}
	models, err := client.ListAuthorizationModels(context.Background(), store.ID)
	if err != nil || len(models) != 1 || models[0].ID != "model-id" {
		t.Fatalf("ListAuthorizationModels() = %#v, %v", models, err)
	}
	if _, err := client.GetAuthorizationModel(context.Background(), store.ID, "model-id"); err != nil {
		t.Fatal(err)
	}
	written, err := client.WriteAuthorizationModel(context.Background(), store.ID, model)
	if err != nil || written.ID != "new-model-id" {
		t.Fatalf("WriteAuthorizationModel() = %#v, %v", written, err)
	}
}

func TestClientRejectsRepeatedPaginationToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"stores":[],"continuation_token":"same"}`)
	}))
	defer server.Close()

	_, err := testClient(t, server.URL, "").ListStores(context.Background())
	if err == nil || !strings.Contains(err.Error(), "repeated continuation token") {
		t.Fatalf("ListStores() error = %v", err)
	}
}

func TestClientRejectsOversizedSuccessAndMissingIdentifiers(t *testing.T) {
	t.Parallel()

	t.Run("oversized success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, strings.Repeat("x", maxResponseBody+1))
		}))
		defer server.Close()
		_, err := testClient(t, server.URL, "").ListStores(context.Background())
		if err == nil || !strings.Contains(err.Error(), "response exceeds") {
			t.Fatalf("ListStores() error = %v", err)
		}
	})

	t.Run("missing created store id", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"name":"tflive"}`)
		}))
		defer server.Close()
		_, err := testClient(t, server.URL, "").CreateStore(context.Background(), "tflive")
		if err == nil || !strings.Contains(err.Error(), "missing or unsafe id") {
			t.Fatalf("CreateStore() error = %v", err)
		}
	})

	t.Run("wrong exact model id", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"authorization_model":{"id":"other-model","schema_version":"1.1","type_definitions":[{"type":"user"}]}}`)
		}))
		defer server.Close()
		_, err := testClient(t, server.URL, "").GetAuthorizationModel(context.Background(), "store-id", "model-id")
		if err == nil || !strings.Contains(err.Error(), `response id is "other-model"`) {
			t.Fatalf("GetAuthorizationModel() error = %v", err)
		}
	})

	t.Run("non JSON success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, `{"stores":[]}`)
		}))
		defer server.Close()
		_, err := testClient(t, server.URL, "").ListStores(context.Background())
		if err == nil || !strings.Contains(err.Error(), "content type must be application/json") {
			t.Fatalf("ListStores() error = %v", err)
		}
	})
}
~~~

- [ ] **Step 2: Run the client tests and confirm failure**

Run:

~~~bash
rtk go test ./internal/openfga -run TestClient -count=1
~~~

Expected: FAIL because Client and its endpoint methods are undefined.

- [ ] **Step 3: Implement the client types and request boundary**

Create internal/openfga/client.go with these exact public types and methods:

~~~go
package openfga

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maxResponseBody = 64 << 10

type Store struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ModelRecord struct {
	ID    string
	Model AuthorizationModel
}

type Client struct {
	baseURL  *url.URL
	token    string
	http     *http.Client
}

func NewClient(cfg Config) *Client {
	return &Client{
		baseURL: cfg.APIURL,
		token:   cfg.APIToken,
		http:    &http.Client{Timeout: cfg.HTTPTimeout},
	}
}

func (client *Client) ListStores(ctx context.Context) ([]Store, error) {
	var stores []Store
	token := ""
	seen := map[string]bool{}
	for {
		var page struct {
			Stores            []Store `json:"stores"`
			ContinuationToken string  `json:"continuation_token"`
		}
		query := url.Values{"page_size": {"100"}}
		if token != "" {
			query.Set("continuation_token", token)
		}
		if err := client.doJSON(ctx, http.MethodGet, client.endpoint("stores"), query, nil, &page, http.StatusOK); err != nil {
			return nil, fmt.Errorf("list OpenFGA stores: %w", err)
		}
		for _, store := range page.Stores {
			if !safeOpaqueIdentifier(store.ID) || store.Name == "" {
				return nil, fmt.Errorf("list OpenFGA stores: response store has missing or unsafe id or missing name")
			}
			stores = append(stores, store)
		}
		if page.ContinuationToken == "" {
			return stores, nil
		}
		if seen[page.ContinuationToken] {
			return nil, fmt.Errorf("list OpenFGA stores: repeated continuation token")
		}
		seen[page.ContinuationToken] = true
		token = page.ContinuationToken
	}
}

func (client *Client) CreateStore(ctx context.Context, name string) (Store, error) {
	var store Store
	err := client.doJSON(ctx, http.MethodPost, client.endpoint("stores"), nil, map[string]string{"name": name}, &store, http.StatusCreated)
	if err != nil {
		return Store{}, fmt.Errorf("create OpenFGA store: %w", err)
	}
	if !safeOpaqueIdentifier(store.ID) || store.Name == "" {
		return Store{}, fmt.Errorf("create OpenFGA store: response has missing or unsafe id or missing name")
	}
	if store.Name != name {
		return Store{}, fmt.Errorf("create OpenFGA store %q: response name is %q", name, store.Name)
	}
	return store, nil
}

func (client *Client) GetStore(ctx context.Context, storeID string) (Store, error) {
	var store Store
	err := client.doJSON(ctx, http.MethodGet, client.endpoint("stores", storeID), nil, nil, &store, http.StatusOK)
	if err != nil {
		return Store{}, fmt.Errorf("get OpenFGA store %q: %w", storeID, err)
	}
	if !safeOpaqueIdentifier(store.ID) {
		return Store{}, fmt.Errorf("get OpenFGA store %q: response has missing or unsafe id", storeID)
	}
	if store.ID != storeID {
		return Store{}, fmt.Errorf("get OpenFGA store %q: response id is %q", storeID, store.ID)
	}
	return store, nil
}

func (client *Client) ListAuthorizationModels(ctx context.Context, storeID string) ([]ModelRecord, error) {
	var records []ModelRecord
	token := ""
	seen := map[string]bool{}
	for {
		var page struct {
			Models            []AuthorizationModel `json:"authorization_models"`
			ContinuationToken string               `json:"continuation_token"`
		}
		query := url.Values{"page_size": {"100"}}
		if token != "" {
			query.Set("continuation_token", token)
		}
		endpoint := client.endpoint("stores", storeID, "authorization-models")
		if err := client.doJSON(ctx, http.MethodGet, endpoint, query, nil, &page, http.StatusOK); err != nil {
			return nil, fmt.Errorf("list authorization models for store %q: %w", storeID, err)
		}
		for _, model := range page.Models {
			if !safeOpaqueIdentifier(model.ID) {
				return nil, fmt.Errorf("list authorization models for store %q: response model has missing or unsafe id", storeID)
			}
			if _, err := CanonicalJSON(model); err != nil {
				return nil, fmt.Errorf("list authorization models for store %q: invalid response model %q: %w", storeID, model.ID, err)
			}
			records = append(records, ModelRecord{ID: model.ID, Model: model})
		}
		if page.ContinuationToken == "" {
			return records, nil
		}
		if seen[page.ContinuationToken] {
			return nil, fmt.Errorf("list authorization models for store %q: repeated continuation token", storeID)
		}
		seen[page.ContinuationToken] = true
		token = page.ContinuationToken
	}
}

func (client *Client) GetAuthorizationModel(ctx context.Context, storeID, modelID string) (AuthorizationModel, error) {
	var response struct {
		Model AuthorizationModel `json:"authorization_model"`
	}
	endpoint := client.endpoint("stores", storeID, "authorization-models", modelID)
	if err := client.doJSON(ctx, http.MethodGet, endpoint, nil, nil, &response, http.StatusOK); err != nil {
		return AuthorizationModel{}, fmt.Errorf("get authorization model %q in store %q: %w", modelID, storeID, err)
	}
	if !safeOpaqueIdentifier(response.Model.ID) {
		return AuthorizationModel{}, fmt.Errorf("get authorization model %q in store %q: response has missing or unsafe id", modelID, storeID)
	}
	if response.Model.ID != modelID {
		return AuthorizationModel{}, fmt.Errorf("get authorization model %q in store %q: response id is %q", modelID, storeID, response.Model.ID)
	}
	if _, err := CanonicalJSON(response.Model); err != nil {
		return AuthorizationModel{}, fmt.Errorf("get authorization model %q in store %q: invalid response model: %w", modelID, storeID, err)
	}
	return response.Model, nil
}

func (client *Client) WriteAuthorizationModel(ctx context.Context, storeID string, model AuthorizationModel) (ModelRecord, error) {
	model.ID = ""
	if _, err := CanonicalJSON(model); err != nil {
		return ModelRecord{}, fmt.Errorf("write authorization model in store %q: invalid model: %w", storeID, err)
	}
	var response struct {
		ID string `json:"authorization_model_id"`
	}
	endpoint := client.endpoint("stores", storeID, "authorization-models")
	if err := client.doJSON(ctx, http.MethodPost, endpoint, nil, model, &response, http.StatusCreated); err != nil {
		return ModelRecord{}, fmt.Errorf("write authorization model in store %q: %w", storeID, err)
	}
	if !safeOpaqueIdentifier(response.ID) {
		return ModelRecord{}, fmt.Errorf("write authorization model in store %q: response has missing or unsafe authorization_model_id", storeID)
	}
	model.ID = response.ID
	return ModelRecord{ID: response.ID, Model: model}, nil
}

func (client *Client) endpoint(segments ...string) *url.URL {
	clone := *client.baseURL
	rawPath := strings.TrimRight(clone.EscapedPath(), "/")
	for _, segment := range segments {
		rawPath += "/" + url.PathEscape(segment)
	}
	path, err := url.PathUnescape(rawPath)
	if err != nil {
		panic(err)
	}
	clone.Path = path
	clone.RawPath = rawPath
	return &clone
}

func (client *Client) doJSON(ctx context.Context, method string, endpoint *url.URL, query url.Values, input, output any, accepted ...int) error {
	if query != nil {
		endpoint = cloneURL(endpoint)
		endpoint.RawQuery = query.Encode()
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}

	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()
	data, truncated, err := readBounded(response.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if !containsStatus(accepted, response.StatusCode) {
		safe := redact(string(data), client.token)
		if truncated {
			safe += " [TRUNCATED]"
		}
		return fmt.Errorf("unexpected HTTP status %s: %s", response.Status, strings.TrimSpace(safe))
	}
	if truncated {
		return fmt.Errorf("response exceeds %s bytes", strconv.Itoa(maxResponseBody))
	}
	if output != nil {
		mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			return fmt.Errorf("response content type must be application/json")
		}
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func readBounded(reader io.Reader) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxResponseBody+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxResponseBody {
		return data[:maxResponseBody], true, nil
	}
	return data, false, nil
}

func cloneURL(value *url.URL) *url.URL {
	clone := *value
	return &clone
}

func containsStatus(statuses []int, status int) bool {
	for _, accepted := range statuses {
		if accepted == status {
			return true
		}
	}
	return false
}

func redact(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}
~~~

- [ ] **Step 4: Format, run client tests, and inspect race-sensitive pagination**

Run:

~~~bash
rtk gofmt -w internal/openfga/client.go internal/openfga/client_test.go
rtk go test ./internal/openfga -run TestClient -race -count=1
rtk go test ./internal/openfga -count=1
~~~

Expected: PASS with no race reports.

- [ ] **Step 5: Commit the REST client**

~~~bash
rtk git add internal/openfga/client.go internal/openfga/client_test.go
rtk git commit -m 'feat: add bounded OpenFGA provisioning client'
~~~

### Task 5: Reconcile one store and one semantic model version

**Files:**

- Create: internal/openfga/provisioner_test.go
- Create: internal/openfga/provisioner.go

**Interfaces:**

- Consumes: Config, AuthorizationModel, Store, ModelRecord, and the Client method set from Task 4.
- Produces: Backend, Result, Bootstrap(context.Context, Config, AuthorizationModel, Backend) (Result, error), and Verify(context.Context, Config, AuthorizationModel, Backend) (Result, error).

- [ ] **Step 1: Write failing stateful reconciliation tests**

Create internal/openfga/provisioner_test.go with a fake implementing the exact Backend interface:

~~~go
package openfga

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	openfgamodel "github.com/vishu42/tflive/openfga"
)

func TestBootstrapIsRepeatableAndReturnsStableIDs(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	backend := &fakeBackend{}
	cfg := Config{StoreName: "tflive"}

	first, err := Bootstrap(context.Background(), cfg, desired, backend)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Bootstrap(context.Background(), cfg, desired, backend)
	if err != nil {
		t.Fatal(err)
	}
	if first.StoreID != "store-1" || first.ModelID != "model-1" || first != second {
		t.Fatalf("first = %#v second = %#v", first, second)
	}
	if backend.createStoreCalls != 1 || backend.writeModelCalls != 1 {
		t.Fatalf("createStoreCalls = %d writeModelCalls = %d", backend.createStoreCalls, backend.writeModelCalls)
	}
}

func TestBootstrapRejectsAmbiguousStoresAndModels(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	tests := []struct {
		name    string
		backend *fakeBackend
		want    string
	}{
		{
			name: "duplicate store names",
			backend: &fakeBackend{stores: []Store{
				{ID: "store-1", Name: "tflive"},
				{ID: "store-2", Name: "tflive"},
			}},
			want: "found 2 stores named",
		},
		{
			name: "duplicate semantic models",
			backend: &fakeBackend{
				stores: []Store{{ID: "store-1", Name: "tflive"}},
				models: map[string][]ModelRecord{"store-1": {
					{ID: "model-1", Model: withID(desired, "model-1")},
					{ID: "model-2", Model: withID(desired, "model-2")},
				}},
			},
			want: "found 2 matching authorization models",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, test.backend)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Bootstrap() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestBootstrapRecoversAfterStoreCreationFailure(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	backend := &fakeBackend{writeErr: errors.New("temporary write failure")}
	_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
	if !errors.Is(err, backend.writeErr) {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	backend.writeErr = nil
	result, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
	if err != nil {
		t.Fatal(err)
	}
	if result.StoreID != "store-1" || backend.createStoreCalls != 1 {
		t.Fatalf("result = %#v createStoreCalls = %d", result, backend.createStoreCalls)
	}
}

func TestVerifyUsesExactIDsAndNeverMutates(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	backend := &fakeBackend{
		stores: []Store{{ID: "configured-store", Name: "renamed-store"}},
		models: map[string][]ModelRecord{
			"configured-store": {{ID: "configured-model", Model: withID(desired, "configured-model")}},
		},
	}
	cfg := Config{StoreID: "configured-store", ModelID: "configured-model"}
	result, err := Verify(context.Background(), cfg, desired, backend)
	if err != nil {
		t.Fatal(err)
	}
	if result.StoreID != cfg.StoreID || result.ModelID != cfg.ModelID {
		t.Fatalf("result = %#v", result)
	}
	if backend.createStoreCalls != 0 || backend.writeModelCalls != 0 || backend.listStoreCalls != 0 || backend.listModelCalls != 0 {
		t.Fatalf("verify mutated or listed: %#v", backend)
	}
}

func desiredModel(t *testing.T) AuthorizationModel {
	t.Helper()
	model, err := ParseAuthorizationModel(openfgamodel.AuthorizationModelJSON())
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func withID(model AuthorizationModel, id string) AuthorizationModel {
	model.ID = id
	return model
}

type fakeBackend struct {
	stores           []Store
	models           map[string][]ModelRecord
	createStoreCalls int
	writeModelCalls  int
	listStoreCalls   int
	listModelCalls   int
	listStoreErr     error
	createStoreErr   error
	getStoreErr      error
	listModelErr     error
	getModelErr      error
	writeErr         error
}

func (backend *fakeBackend) ListStores(context.Context) ([]Store, error) {
	backend.listStoreCalls++
	if backend.listStoreErr != nil {
		return nil, backend.listStoreErr
	}
	return append([]Store(nil), backend.stores...), nil
}

func (backend *fakeBackend) CreateStore(_ context.Context, name string) (Store, error) {
	backend.createStoreCalls++
	if backend.createStoreErr != nil {
		return Store{}, backend.createStoreErr
	}
	store := Store{ID: fmt.Sprintf("store-%d", backend.createStoreCalls), Name: name}
	backend.stores = append(backend.stores, store)
	return store, nil
}

func (backend *fakeBackend) GetStore(_ context.Context, id string) (Store, error) {
	if backend.getStoreErr != nil {
		return Store{}, backend.getStoreErr
	}
	for _, store := range backend.stores {
		if store.ID == id {
			return store, nil
		}
	}
	return Store{}, fmt.Errorf("store %s not found", id)
}

func (backend *fakeBackend) ListAuthorizationModels(_ context.Context, storeID string) ([]ModelRecord, error) {
	backend.listModelCalls++
	if backend.listModelErr != nil {
		return nil, backend.listModelErr
	}
	return append([]ModelRecord(nil), backend.models[storeID]...), nil
}

func (backend *fakeBackend) GetAuthorizationModel(_ context.Context, storeID, modelID string) (AuthorizationModel, error) {
	if backend.getModelErr != nil {
		return AuthorizationModel{}, backend.getModelErr
	}
	for _, record := range backend.models[storeID] {
		if record.ID == modelID {
			return record.Model, nil
		}
	}
	return AuthorizationModel{}, fmt.Errorf("model %s not found", modelID)
}

func (backend *fakeBackend) WriteAuthorizationModel(_ context.Context, storeID string, model AuthorizationModel) (ModelRecord, error) {
	backend.writeModelCalls++
	if backend.writeErr != nil {
		return ModelRecord{}, backend.writeErr
	}
	record := ModelRecord{ID: fmt.Sprintf("model-%d", backend.writeModelCalls), Model: withID(model, fmt.Sprintf("model-%d", backend.writeModelCalls))}
	if backend.models == nil {
		backend.models = make(map[string][]ModelRecord)
	}
	backend.models[storeID] = append(backend.models[storeID], record)
	return record, nil
}
~~~

Add these remaining reconciliation tests:

~~~go
func TestBootstrapReusesStoreAndCreatesOneVersionForChangedModel(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	old := desiredModel(t)
	for index := range old.TypeDefinitions {
		if old.TypeDefinitions[index].Type == "stack" {
			old.TypeDefinitions[index].Relations["can_manage_access"] = []byte(`{"computedUserset":{"relation":"viewer"}}`)
		}
	}
	backend := &fakeBackend{
		stores: []Store{{ID: "store-existing", Name: "tflive"}},
		models: map[string][]ModelRecord{
			"store-existing": {{ID: "old-model", Model: withID(old, "old-model")}},
		},
	}

	first, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || backend.createStoreCalls != 0 || backend.writeModelCalls != 1 {
		t.Fatalf("first = %#v second = %#v backend = %#v", first, second, backend)
	}
}

func TestVerifyRejectsMissingIDsAndSemanticMismatch(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	if _, err := Verify(context.Background(), Config{}, desired, &fakeBackend{}); err == nil || !strings.Contains(err.Error(), "OPENFGA_STORE_ID") {
		t.Fatalf("Verify() missing-ID error = %v", err)
	}

	actual := desiredModel(t)
	for index := range actual.TypeDefinitions {
		if actual.TypeDefinitions[index].Type == "stack" {
			actual.TypeDefinitions[index].Relations["can_manage_access"] = []byte(`{"computedUserset":{"relation":"viewer"}}`)
		}
	}
	backend := &fakeBackend{
		stores: []Store{{ID: "store-id", Name: "tflive"}},
		models: map[string][]ModelRecord{
			"store-id": {{ID: "model-id", Model: withID(actual, "model-id")}},
		},
	}
	_, err := Verify(context.Background(), Config{StoreID: "store-id", ModelID: "model-id"}, desired, backend)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Verify() mismatch error = %v", err)
	}
}

func TestProvisionerWrapsEveryBackendFailure(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	want := errors.New("backend unavailable")
	tests := []struct {
		name      string
		operation func(*fakeBackend) error
	}{
		{
			name: "list stores",
			operation: func(backend *fakeBackend) error {
				backend.listStoreErr = want
				_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
				return err
			},
		},
		{
			name: "create store",
			operation: func(backend *fakeBackend) error {
				backend.createStoreErr = want
				_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
				return err
			},
		},
		{
			name: "list models",
			operation: func(backend *fakeBackend) error {
				backend.stores = []Store{{ID: "store-id", Name: "tflive"}}
				backend.listModelErr = want
				_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
				return err
			},
		},
		{
			name: "write model",
			operation: func(backend *fakeBackend) error {
				backend.stores = []Store{{ID: "store-id", Name: "tflive"}}
				backend.writeErr = want
				_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
				return err
			},
		},
		{
			name: "get store",
			operation: func(backend *fakeBackend) error {
				backend.getStoreErr = want
				_, err := Verify(context.Background(), Config{StoreID: "store-id", ModelID: "model-id"}, desired, backend)
				return err
			},
		},
		{
			name: "get model",
			operation: func(backend *fakeBackend) error {
				backend.stores = []Store{{ID: "store-id", Name: "tflive"}}
				backend.getModelErr = want
				_, err := Verify(context.Background(), Config{StoreID: "store-id", ModelID: "model-id"}, desired, backend)
				return err
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.operation(&fakeBackend{}); !errors.Is(err, want) {
				t.Fatalf("operation error = %v", err)
			}
		})
	}
}

func TestBootstrapPreservesCancellation(t *testing.T) {
	t.Parallel()

	backend := &fakeBackend{listStoreErr: context.Canceled}
	_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desiredModel(t), backend)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Bootstrap() error = %v", err)
	}
}
~~~

- [ ] **Step 2: Run provisioner tests and confirm failure**

Run:

~~~bash
rtk go test ./internal/openfga -run 'TestBootstrap|TestVerify' -count=1
~~~

Expected: FAIL because Backend, Result, Bootstrap, and Verify are undefined.

- [ ] **Step 3: Implement bootstrap and exact-ID verification**

Create internal/openfga/provisioner.go:

~~~go
package openfga

import (
	"context"
	"fmt"
)

type Backend interface {
	ListStores(context.Context) ([]Store, error)
	CreateStore(context.Context, string) (Store, error)
	GetStore(context.Context, string) (Store, error)
	ListAuthorizationModels(context.Context, string) ([]ModelRecord, error)
	GetAuthorizationModel(context.Context, string, string) (AuthorizationModel, error)
	WriteAuthorizationModel(context.Context, string, AuthorizationModel) (ModelRecord, error)
}

type Result struct {
	StoreID string
	ModelID string
}

func Bootstrap(ctx context.Context, cfg Config, desired AuthorizationModel, backend Backend) (Result, error) {
	stores, err := backend.ListStores(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("list stores: %w", err)
	}
	var matches []Store
	for _, store := range stores {
		if store.Name == cfg.StoreName {
			matches = append(matches, store)
		}
	}
	if len(matches) > 1 {
		return Result{}, fmt.Errorf("found %d stores named %q; bootstrap requires one unique store", len(matches), cfg.StoreName)
	}

	var store Store
	if len(matches) == 0 {
		store, err = backend.CreateStore(ctx, cfg.StoreName)
		if err != nil {
			return Result{}, fmt.Errorf("create store %q: %w", cfg.StoreName, err)
		}
	} else {
		store = matches[0]
	}

	records, err := backend.ListAuthorizationModels(ctx, store.ID)
	if err != nil {
		return Result{}, fmt.Errorf("list authorization models for store %q: %w", store.ID, err)
	}
	var modelMatches []ModelRecord
	for _, record := range records {
		equal, compareErr := ModelsEqual(desired, record.Model)
		if compareErr != nil {
			return Result{}, fmt.Errorf("compare authorization model %q: %w", record.ID, compareErr)
		}
		if equal {
			modelMatches = append(modelMatches, record)
		}
	}
	if len(modelMatches) > 1 {
		return Result{}, fmt.Errorf("found %d matching authorization models in store %q; selection is ambiguous", len(modelMatches), store.ID)
	}
	if len(modelMatches) == 1 {
		return Result{StoreID: store.ID, ModelID: modelMatches[0].ID}, nil
	}

	written, err := backend.WriteAuthorizationModel(ctx, store.ID, desired)
	if err != nil {
		return Result{}, fmt.Errorf("write authorization model in store %q: %w", store.ID, err)
	}
	return Result{StoreID: store.ID, ModelID: written.ID}, nil
}

func Verify(ctx context.Context, cfg Config, desired AuthorizationModel, backend Backend) (Result, error) {
	if err := cfg.ValidateVerify(); err != nil {
		return Result{}, err
	}
	if _, err := backend.GetStore(ctx, cfg.StoreID); err != nil {
		return Result{}, fmt.Errorf("verify configured store %q: %w", cfg.StoreID, err)
	}
	actual, err := backend.GetAuthorizationModel(ctx, cfg.StoreID, cfg.ModelID)
	if err != nil {
		return Result{}, fmt.Errorf("verify configured model %q in store %q: %w", cfg.ModelID, cfg.StoreID, err)
	}
	equal, err := ModelsEqual(desired, actual)
	if err != nil {
		return Result{}, fmt.Errorf("compare configured model %q: %w", cfg.ModelID, err)
	}
	if !equal {
		return Result{}, fmt.Errorf("configured OpenFGA model %q does not match the repository model", cfg.ModelID)
	}
	return Result{StoreID: cfg.StoreID, ModelID: cfg.ModelID}, nil
}
~~~

- [ ] **Step 4: Format and run reconciliation tests**

Run:

~~~bash
rtk gofmt -w internal/openfga/provisioner.go internal/openfga/provisioner_test.go
rtk go test ./internal/openfga -run 'TestBootstrap|TestVerify' -race -count=1
rtk go test ./internal/openfga -count=1
~~~

Expected: PASS with no race reports.

- [ ] **Step 5: Commit reconciliation**

~~~bash
rtk git add internal/openfga/provisioner.go internal/openfga/provisioner_test.go
rtk git commit -m 'feat: reconcile OpenFGA store and model'
~~~

### Task 6: Add the one-shot bootstrap and verify command

**Files:**

- Create: cmd/openfga-provisioner/main_test.go
- Create: cmd/openfga-provisioner/main.go

**Interfaces:**

- Consumes: openfgamodel.AuthorizationModelJSON, LoadConfig, NewClient, Bootstrap, and Verify.
- Produces: an openfga-provisioner binary accepting bootstrap or verify, with verify as the no-argument default.

- [ ] **Step 1: Write failing command-boundary tests**

Create cmd/openfga-provisioner/main_test.go:

~~~go
package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	openfga "github.com/vishu42/tflive/internal/openfga"
	openfgamodel "github.com/vishu42/tflive/openfga"
)

func TestRunDefaultsToVerifyAndPrintsOnlyEnvironmentAssignments(t *testing.T) {
	t.Parallel()

	var operation string
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), nil, commandEnv(true), openfgamodel.AuthorizationModelJSON(), func(_ context.Context, got string, cfg openfga.Config, model openfga.AuthorizationModel) (openfga.Result, error) {
		operation = got
		return openfga.Result{StoreID: cfg.StoreID, ModelID: cfg.ModelID}, nil
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if operation != "verify" {
		t.Fatalf("operation = %q", operation)
	}
	if got, want := stdout.String(), "OPENFGA_STORE_ID=store-id\nOPENFGA_MODEL_ID=model-id\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if strings.Contains(stdout.String()+stderr.String(), "secret-token") {
		t.Fatal("output leaked API token")
	}
}

func TestRunBootstrapDoesNotRequireIDs(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := run(context.Background(), []string{"bootstrap"}, commandEnv(false), openfgamodel.AuthorizationModelJSON(), func(_ context.Context, operation string, _ openfga.Config, _ openfga.AuthorizationModel) (openfga.Result, error) {
		if operation != "bootstrap" {
			t.Fatalf("operation = %q", operation)
		}
		return openfga.Result{StoreID: "new-store", ModelID: "new-model"}, nil
	}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "OPENFGA_STORE_ID=new-store") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunRejectsUnknownOperationAndInvalidModel(t *testing.T) {
	t.Parallel()

	execute := func(context.Context, string, openfga.Config, openfga.AuthorizationModel) (openfga.Result, error) {
		t.Fatal("execute called")
		return openfga.Result{}, nil
	}
	for _, test := range []struct {
		args  []string
		model []byte
		want  string
	}{
		{args: []string{"destroy"}, model: openfgamodel.AuthorizationModelJSON(), want: "operation must be bootstrap or verify"},
		{args: []string{"verify"}, model: []byte("{"), want: "parse repository authorization model"},
	} {
		err := run(context.Background(), test.args, commandEnv(true), test.model, execute, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("run() error = %v, want containing %q", err, test.want)
		}
	}
}

func TestRunPreservesCancellationAndRedactsExecutionFailure(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	secretErr := fmt.Errorf("backend echoed secret-token: %w", context.Canceled)
	err := run(ctx, []string{"bootstrap"}, commandEnv(false), openfgamodel.AuthorizationModelJSON(), func(context.Context, string, openfga.Config, openfga.AuthorizationModel) (openfga.Result, error) {
		return openfga.Result{}, secretErr
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "context canceled") || !strings.Contains(err.Error(), "[REDACTED]") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunReportsEnvironmentOutputFailure(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"bootstrap"}, commandEnv(false), openfgamodel.AuthorizationModelJSON(), func(context.Context, string, openfga.Config, openfga.AuthorizationModel) (openfga.Result, error) {
		return openfga.Result{StoreID: "store-id", ModelID: "model-id"}, nil
	}, failingWriter{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "write OpenFGA environment assignments") {
		t.Fatalf("run() error = %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("output unavailable")
}

func commandEnv(withIDs bool) func(string) string {
	values := map[string]string{
		"OPENFGA_API_URL":   "http://openfga:8080",
		"OPENFGA_API_TOKEN": "secret-token",
	}
	if withIDs {
		values["OPENFGA_STORE_ID"] = "store-id"
		values["OPENFGA_MODEL_ID"] = "model-id"
	}
	return func(name string) string { return values[name] }
}
~~~

- [ ] **Step 2: Run command tests and confirm failure**

Run:

~~~bash
rtk go test ./cmd/openfga-provisioner -count=1
~~~

Expected: FAIL because run and the command executable do not exist.

- [ ] **Step 3: Implement signal handling, dispatch, and clean output channels**

Create cmd/openfga-provisioner/main.go:

~~~go
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	openfga "github.com/vishu42/tflive/internal/openfga"
	openfgamodel "github.com/vishu42/tflive/openfga"
)

type executeFunc func(context.Context, string, openfga.Config, openfga.AuthorizationModel) (openfga.Result, error)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, openfgamodel.AuthorizationModelJSON(), execute, os.Stdout, os.Stderr); err != nil {
		log.New(os.Stderr, "", 0).Printf("OpenFGA provisioner failed: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, modelJSON []byte, execute executeFunc, stdout, stderr io.Writer) error {
	operation := "verify"
	if len(args) == 1 {
		operation = args[0]
	}
	if len(args) > 1 || (operation != "bootstrap" && operation != "verify") {
		return fmt.Errorf("operation must be bootstrap or verify")
	}
	cfg, err := openfga.LoadConfig(getenv)
	if err != nil {
		return fmt.Errorf("load OpenFGA provisioner config: %w", err)
	}
	model, err := openfga.ParseAuthorizationModel(modelJSON)
	if err != nil {
		return fmt.Errorf("parse repository authorization model: %w", err)
	}
	result, err := execute(ctx, operation, cfg, model)
	if err != nil {
		return fmt.Errorf("%s OpenFGA: %s", operation, redact(err.Error(), cfg.APIToken))
	}
	if _, err := fmt.Fprintf(stdout, "OPENFGA_STORE_ID=%s\nOPENFGA_MODEL_ID=%s\n", result.StoreID, result.ModelID); err != nil {
		return fmt.Errorf("write OpenFGA environment assignments: %w", err)
	}
	if _, err := fmt.Fprintf(stderr, "OpenFGA %s succeeded for explicit store and model identifiers\n", operation); err != nil {
		return fmt.Errorf("write OpenFGA diagnostic: %w", err)
	}
	return nil
}

func execute(ctx context.Context, operation string, cfg openfga.Config, model openfga.AuthorizationModel) (openfga.Result, error) {
	client := openfga.NewClient(cfg)
	if operation == "bootstrap" {
		return openfga.Bootstrap(ctx, cfg, model, client)
	}
	return openfga.Verify(ctx, cfg, model, client)
}

func redact(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}
~~~

- [ ] **Step 4: Format, run, and build the command**

Run:

~~~bash
rtk gofmt -w cmd/openfga-provisioner/main.go cmd/openfga-provisioner/main_test.go
rtk go test ./cmd/openfga-provisioner ./internal/openfga -race -count=1
rtk go build -o /tmp/openfga-provisioner ./cmd/openfga-provisioner
~~~

Expected: tests pass, no race reports, and the binary builds.

- [ ] **Step 5: Commit the command**

~~~bash
rtk git add cmd/openfga-provisioner/main.go cmd/openfga-provisioner/main_test.go
rtk git commit -m 'feat: add OpenFGA provisioning command'
~~~

### Task 7: Package and wire the provisioner into Compose

**Files:**

- Create: Dockerfile.openfga-provisioner
- Modify: docker-compose.yaml
- Modify: .env.example
- Modify: scripts/verify-auth-compose.mjs

**Interfaces:**

- Consumes: the openfga-provisioner binary and existing healthy openfga service.
- Produces: the openfga-provision Compose service; docker compose run --rm openfga-provision bootstrap and verify workflows.

- [ ] **Step 1: Extend the Compose contract test first**

In scripts/verify-auth-compose.mjs, add the provisioner service beside the existing OpenFGA variables:

~~~js
const openfgaProvision = service("openfga-provision");
~~~

Add these assertions after the OpenFGA health assertions:

~~~js
assert.equal(openfgaProvision.depends_on?.openfga?.condition, "service_healthy");
assert.equal(openfgaProvision.restart, "no");
assert.equal(openfgaProvision.build?.dockerfile, "Dockerfile.openfga-provisioner");
assert.deepEqual(openfgaProvision.ports ?? [], []);
assert.deepEqual(openfgaProvision.command, ["verify"]);
assert.equal(openfgaProvision.environment?.OPENFGA_API_URL, "http://openfga:8080");
assert.equal(openfgaProvision.environment?.OPENFGA_STORE_NAME, "tflive");
assert.equal(openfgaProvision.environment?.OPENFGA_STORE_ID, "");
assert.equal(openfgaProvision.environment?.OPENFGA_MODEL_ID, "");
assert.equal(openfgaProvision.environment?.OPENFGA_HTTP_TIMEOUT, "10s");
~~~

Add image checks beside the Keycloak provisioner checks:

~~~js
const openfgaProvisionerDockerfile = resolve(root, "Dockerfile.openfga-provisioner");
assert.ok(existsSync(openfgaProvisionerDockerfile), "missing OpenFGA provisioner Dockerfile");
const openfgaProvisionerImage = readFileSync(openfgaProvisionerDockerfile, "utf8");
assert.match(openfgaProvisionerImage, /^FROM golang:1\.24\.1-alpine3\.21 AS build/m);
assert.match(openfgaProvisionerImage, /^FROM alpine:3\.21$/m);
assert.match(openfgaProvisionerImage, /^USER openfga-provisioner$/m);
~~~

- [ ] **Step 2: Run the contract test and confirm the missing-service failure**

Run:

~~~bash
rtk node scripts/verify-auth-compose.mjs
~~~

Expected: FAIL with missing Compose service: openfga-provision.

- [ ] **Step 3: Add the pinned unprivileged image**

Create Dockerfile.openfga-provisioner:

~~~dockerfile
FROM golang:1.24.1-alpine3.21 AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY openfga ./openfga
COPY internal/openfga ./internal/openfga
COPY cmd/openfga-provisioner ./cmd/openfga-provisioner
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/openfga-provisioner ./cmd/openfga-provisioner

FROM alpine:3.21

RUN apk add --no-cache ca-certificates \
    && addgroup -S openfga-provisioner \
    && adduser -S -D -H -G openfga-provisioner openfga-provisioner
COPY --from=build /out/openfga-provisioner /usr/local/bin/openfga-provisioner

USER openfga-provisioner
ENTRYPOINT ["/usr/local/bin/openfga-provisioner"]
~~~

- [ ] **Step 4: Add explicit Compose configuration**

Insert this service after openfga in docker-compose.yaml:

~~~yaml
  openfga-provision:
    build:
      context: .
      dockerfile: Dockerfile.openfga-provisioner
    command: ["verify"]
    depends_on:
      openfga:
        condition: service_healthy
    environment:
      OPENFGA_API_URL: http://openfga:8080
      OPENFGA_STORE_NAME: tflive
      OPENFGA_STORE_ID: ${OPENFGA_STORE_ID:-}
      OPENFGA_MODEL_ID: ${OPENFGA_MODEL_ID:-}
      OPENFGA_HTTP_TIMEOUT: ${OPENFGA_HTTP_TIMEOUT:-10s}
      OPENFGA_API_TOKEN: ${OPENFGA_API_TOKEN:-}
    restart: "no"
~~~

Add these non-secret and optional entries after the OpenFGA database values in .env.example:

~~~dotenv
# Generated by: docker compose run --rm openfga-provision bootstrap
# Copy the exact output here before running verify or starting the API.
OPENFGA_STORE_ID=
OPENFGA_MODEL_ID=

# Optional. Bearer credential when provisioning a protected OpenFGA deployment.
# The local OpenFGA service does not require one.
OPENFGA_API_TOKEN=

# Optional. Provisioning request deadline.
# Default: 10s
OPENFGA_HTTP_TIMEOUT=10s
~~~

- [ ] **Step 5: Run Compose, image, and Go verification**

Run:

~~~bash
rtk node scripts/verify-auth-compose.mjs
rtk docker compose --env-file .env.example config --quiet
rtk docker compose --env-file .env.example build openfga-provision
rtk go test ./cmd/openfga-provisioner ./internal/openfga -count=1
~~~

Expected: all commands pass; the image build ends with an unprivileged runtime.

- [ ] **Step 6: Commit the container and Compose integration**

~~~bash
rtk git add Dockerfile.openfga-provisioner docker-compose.yaml .env.example scripts/verify-auth-compose.mjs
rtk git commit -m 'feat: provision OpenFGA model in Compose'
~~~

### Task 8: Verify compatibility against live OpenFGA v1.15.1

**Files:**

- Create: internal/openfga/live_test.go

**Interfaces:**

- Consumes: the real OpenFGA HTTP API, canonical model asset, Bootstrap, Verify, and explicit IDs.
- Produces: an opt-in integration test proving stable reruns and server-side rejection of direct derived-permission writes.

- [ ] **Step 1: Write the opt-in live test**

Create internal/openfga/live_test.go in external test package form:

~~~go
package openfga_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	openfga "github.com/vishu42/tflive/internal/openfga"
	openfgamodel "github.com/vishu42/tflive/openfga"
)

func TestLiveBootstrapVerifyAndDerivedWriteRejection(t *testing.T) {
	if os.Getenv("OPENFGA_INTEGRATION") != "1" {
		t.Skip("set OPENFGA_INTEGRATION=1 to run against OpenFGA")
	}

	cfg, err := openfga.LoadConfig(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	cfg.StoreName = fmt.Sprintf("tflive-integration-%d", time.Now().UnixNano())
	cfg.StoreID = ""
	cfg.ModelID = ""
	model, err := openfga.ParseAuthorizationModel(openfgamodel.AuthorizationModelJSON())
	if err != nil {
		t.Fatal(err)
	}
	client := openfga.NewClient(cfg)

	first, err := openfga.Bootstrap(context.Background(), cfg, model, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := deleteStore(cfg, first.StoreID); err != nil {
			t.Errorf("delete integration store: %v", err)
		}
	})
	second, err := openfga.Bootstrap(context.Background(), cfg, model, client)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("first = %#v second = %#v", first, second)
	}

	cfg.StoreID = first.StoreID
	cfg.ModelID = first.ModelID
	verified, err := openfga.Verify(context.Background(), cfg, model, client)
	if err != nil {
		t.Fatal(err)
	}
	if verified != first {
		t.Fatalf("verified = %#v first = %#v", verified, first)
	}

	for _, relation := range []string{"can_view", "can_operate", "can_approve", "can_manage_access"} {
		relation := relation
		t.Run(relation, func(t *testing.T) {
			if err := requireRejectedDerivedWrite(cfg, relation); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func requireRejectedDerivedWrite(cfg openfga.Config, relation string) error {
	payload := map[string]any{
		"authorization_model_id": cfg.ModelID,
		"writes": map[string]any{
			"tuple_keys": []map[string]string{{
				"user": "user:direct-write",
				"relation": relation,
				"object": "stack:example",
			}},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := liveEndpoint(cfg.APIURL, "stores", cfg.StoreID, "write")
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if cfg.APIToken != "" {
		request.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}
	response, err := (&http.Client{Timeout: cfg.HTTPTimeout}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("direct write to %s returned %s: %s", relation, response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func deleteStore(cfg openfga.Config, storeID string) error {
	request, err := http.NewRequest(http.MethodDelete, liveEndpoint(cfg.APIURL, "stores", storeID), nil)
	if err != nil {
		return err
	}
	if cfg.APIToken != "" {
		request.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}
	response, err := (&http.Client{Timeout: cfg.HTTPTimeout}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("delete store returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func liveEndpoint(base *url.URL, segments ...string) string {
	clone := *base
	rawPath := strings.TrimRight(clone.EscapedPath(), "/")
	for _, segment := range segments {
		rawPath += "/" + url.PathEscape(segment)
	}
	path, err := url.PathUnescape(rawPath)
	if err != nil {
		panic(err)
	}
	clone.Path = path
	clone.RawPath = rawPath
	return clone.String()
}
~~~

- [ ] **Step 2: Run without the opt-in flag and verify an intentional skip**

Run:

~~~bash
rtk go test ./internal/openfga -run TestLiveBootstrapVerifyAndDerivedWriteRejection -v -count=1
~~~

Expected: PASS with a skip message requiring OPENFGA_INTEGRATION=1.

- [ ] **Step 3: Start the pinned server and run the real compatibility test**

Run:

~~~bash
rtk docker compose --env-file .env.example up -d openfga-postgres openfga-migrate openfga
OPENFGA_INTEGRATION=1 OPENFGA_API_URL=http://localhost:8083 rtk go test ./internal/openfga -run TestLiveBootstrapVerifyAndDerivedWriteRejection -v -count=1
~~~

Expected: PASS. The second bootstrap returns the first IDs, verify succeeds with those exact IDs, all four derived direct writes return HTTP 400, and cleanup deletes the temporary store.

- [ ] **Step 4: Re-run the model matrix and full OpenFGA package tests**

Run:

~~~bash
rtk docker run --rm -v "$PWD:/workspace" -w /workspace openfga/cli:v0.7.15 model test --tests openfga/authorization-model-tests.fga.yaml
rtk go test ./internal/openfga ./cmd/openfga-provisioner -race -count=1
~~~

Expected: CLI and Go tests pass with no race reports.

- [ ] **Step 5: Commit live verification**

~~~bash
rtk git add internal/openfga/live_test.go
rtk git commit -m 'test: verify OpenFGA provisioning against live server'
~~~

### Task 9: Document operation, update tracking, and run the release gate

**Files:**

- Modify: README.md
- Modify: docs/authentication.md
- Modify: docs/sprint/authn_and_authz/README.md

**Interfaces:**

- Consumes: the final bootstrap/verify commands, role matrix, explicit environment contract, and verified failure behavior.
- Produces: clean-checkout operating instructions and completed AUTH-004 backlog evidence.

- [ ] **Step 1: Confirm AUTH-004 remains In Progress**

Do not alter other ticket rows. Confirm the status set in Task 1 still reflects the active implementation.

Run:

~~~bash
rtk rg -n 'AUTH-004.*In Progress' docs/sprint/authn_and_authz/README.md
~~~

Expected: exactly one matching backlog row.

- [ ] **Step 2: Document the two-phase clean-checkout workflow**

Update README.md so local startup contains these commands and tells the reader to copy, not execute, stdout assignments:

~~~bash
docker compose --env-file .env.example up -d openfga-postgres openfga-migrate openfga
docker compose --env-file .env.example run --rm openfga-provision bootstrap
# Copy OPENFGA_STORE_ID and OPENFGA_MODEL_ID from stdout into .env.
docker compose run --rm openfga-provision verify
~~~

State that bootstrap must not run concurrently, verify never mutates, and the API will later consume the same explicit IDs.

- [ ] **Step 3: Add the authorization model and recovery runbook**

Append an OpenFGA authorization section to docs/authentication.md containing:

~~~text
Direct stack roles
- owner: view, operate, approve, and manage access
- operator: view and operate
- approver: view and approve
- viewer: view only

Derived relations
- can_view = owner or operator or approver or viewer
- can_operate = owner or operator
- can_approve = owner or approver
- can_manage_access = owner

Provisioning contract
- bootstrap discovers only the uniquely named tflive store and reuses one semantic model match
- duplicate names or duplicate matches fail closed
- stdout contains only OPENFGA_STORE_ID and OPENFGA_MODEL_ID assignments
- deployment administrators record both IDs in environment configuration
- verify fetches exact IDs, compares the exact model, and never uses latest
- a failed store-only or model-only bootstrap can be rerun safely
- model changes create a new immutable model ID and require an explicit environment update
- bootstrap is serialized because OpenFGA store names are not unique
~~~

Use “deployment administrator” for the person or pipeline configuring services. Reserve operator for the per-stack OpenFGA role.

- [ ] **Step 4: Verify documentation coverage before marking Done**

Run:

~~~bash
rtk rg -n 'OPENFGA_STORE_ID|OPENFGA_MODEL_ID|openfga-provision bootstrap|openfga-provision verify' README.md docs/authentication.md
rtk rg -n 'can_view|can_operate|can_approve|can_manage_access|deployment administrator' docs/authentication.md
rtk git diff --check
~~~

Expected: both docs contain the setup identifiers and commands, authentication.md contains all four permissions and unambiguous terminology, and diff check is clean.

- [ ] **Step 5: Run fresh complete verification**

Run:

~~~bash
rtk docker run --rm -v "$PWD:/workspace" -w /workspace openfga/cli:v0.7.15 model test --tests openfga/authorization-model-tests.fga.yaml
rtk go test ./... -count=1
rtk npm --prefix web test
rtk npm --prefix web run build
rtk node scripts/verify-auth-compose.mjs
rtk docker compose --env-file .env.example config --quiet
OPENFGA_INTEGRATION=1 OPENFGA_API_URL=http://localhost:8083 rtk go test ./internal/openfga -run TestLiveBootstrapVerifyAndDerivedWriteRejection -v -count=1
rtk git diff --check
~~~

Expected: model matrix, full Go suite, frontend tests, frontend build, Compose contract, Compose rendering, live OpenFGA compatibility, and whitespace verification all pass.

- [ ] **Step 6: Mark AUTH-004 Done only after Step 5 passes**

Change only the AUTH-004 status cell in docs/sprint/authn_and_authz/README.md from In Progress to Done.

Run:

~~~bash
rtk rg -n 'AUTH-004.*Done' docs/sprint/authn_and_authz/README.md
~~~

Expected: exactly one matching completed backlog row.

- [ ] **Step 7: Commit documentation and ticket tracking**

~~~bash
rtk git add README.md docs/authentication.md docs/sprint/authn_and_authz/README.md
rtk git commit -m 'docs: document OpenFGA model provisioning'
~~~

- [ ] **Step 8: Confirm the branch is clean and review acceptance coverage**

Run:

~~~bash
rtk git status --short
rtk git log --oneline -9
~~~

Expected: no status output and nine focused implementation commits after the design/plan commits. Confirm the issue checklist maps to evidence as follows:

- user and stack types plus four roles: authorization-model.json and CLI tests.
- four derived permissions: authorization-model.json and the 20-check matrix.
- no direct derived assignment: structural Go test and four live rejected writes.
- explicit IDs and safe reruns: command tests, stateful fake tests, Compose flow, and live two-run test.
- all allowed and denied combinations: CLI matrix.
- model and provisioning documentation: README.md and docs/authentication.md.
- status update: AUTH-004 Done row.
