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
	stack, ok := relations["stack"]
	if !ok {
		t.Fatal("model is missing the stack type")
	}
	want := map[string]bool{
		"owner": true, "operator": true, "approver": true, "viewer": true,
		"can_view": true, "can_operate": true, "can_approve": true, "can_manage_access": true,
	}
	for _, name := range stack {
		if !want[name] {
			t.Fatalf("stack has unexpected relation %q", name)
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("stack is missing relations %v", want)
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
