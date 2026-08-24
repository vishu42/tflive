package openfgamodel_test

import (
	"encoding/json"
	"testing"

	openfgamodel "github.com/vishu42/tflive/openfga"
)

// The DSL is transformed at runtime, so a malformed model would otherwise first
// surface when the provisioner runs. This is what fails it at `go test` instead.
func TestEmbeddedDSLTransformsToExpectedModel(t *testing.T) {
	t.Parallel()

	data, err := openfgamodel.AuthorizationModelJSON()
	if err != nil {
		t.Fatal(err)
	}

	var model struct {
		SchemaVersion   string `json:"schema_version"`
		TypeDefinitions []struct {
			Type      string                     `json:"type"`
			Relations map[string]json.RawMessage `json:"relations"`
		} `json:"type_definitions"`
	}
	if err := json.Unmarshal(data, &model); err != nil {
		t.Fatal(err)
	}
	if model.SchemaVersion != "1.1" {
		t.Fatalf("schema_version = %q, want 1.1", model.SchemaVersion)
	}

	relations := map[string][]string{}
	for _, definition := range model.TypeDefinitions {
		names := make([]string, 0, len(definition.Relations))
		for name := range definition.Relations {
			names = append(names, name)
		}
		relations[definition.Type] = names
	}
	if _, ok := relations["user"]; !ok {
		t.Fatal("model is missing the user type")
	}
	expected := map[string][]string{
		"platform": {
			"root", "admin", "editor", "viewer",
			"can_administer", "can_edit", "can_view",
			"can_create_stack", "can_publish_template", "can_read_template",
		},
		"stack": {
			"owner", "operator", "approver", "viewer", "parent",
			"can_view", "can_operate", "can_approve", "can_manage_access",
		},
	}
	for typeName, names := range expected {
		actual, ok := relations[typeName]
		if !ok {
			t.Fatalf("model is missing the %s type", typeName)
		}
		want := map[string]bool{}
		for _, name := range names {
			want[name] = true
		}
		for _, name := range actual {
			if !want[name] {
				t.Fatalf("%s has unexpected relation %q", typeName, name)
			}
			delete(want, name)
		}
		if len(want) != 0 {
			t.Fatalf("%s is missing relations %v", typeName, want)
		}
	}
}

// AuthorizationModelJSON hands out a cached transform, so callers must not be
// able to corrupt it for one another.
func TestAuthorizationModelJSONReturnsIndependentCopies(t *testing.T) {
	t.Parallel()

	first, err := openfgamodel.AuthorizationModelJSON()
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 'x'
	second, err := openfgamodel.AuthorizationModelJSON()
	if err != nil {
		t.Fatal(err)
	}
	if second[0] != '{' {
		t.Fatal("mutating a returned model corrupted the next caller's copy")
	}
}
