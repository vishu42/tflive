package openfga

import (
	"encoding/json"
	"fmt"
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

func TestModelsEqualNormalizesComputedUsersetObject(t *testing.T) {
	desired, err := ParseAuthorizationModel([]byte(`{
		"schema_version":"1.1",
		"type_definitions":[{
			"type":"stack",
			"relations":{"can_view":{"union":{"child":[{"computedUserset":{"relation":"viewer"}}]}}}
		}]
	}`))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		object    string
		wantEqual bool
	}{
		{name: "empty OpenFGA default", object: "", wantEqual: true},
		{name: "nonempty object", object: "stack:other", wantEqual: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := ParseAuthorizationModel([]byte(`{
				"schema_version":"1.1",
				"type_definitions":[{
					"type":"stack",
					"relations":{"can_view":{"union":{"child":[{"computedUserset":{"object":` + fmt.Sprintf("%q", test.object) + `,"relation":"viewer"}}]}}}
				}]
			}`))
			if err != nil {
				t.Fatal(err)
			}
			equal, err := ModelsEqual(desired, actual)
			if err != nil {
				t.Fatal(err)
			}
			if equal != test.wantEqual {
				t.Fatalf("ModelsEqual() = %t, want %t", equal, test.wantEqual)
			}
		})
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
