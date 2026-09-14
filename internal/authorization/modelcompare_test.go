package authorization

import (
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestModelsEqualIgnoresNonSemanticOrderingAndIDs(t *testing.T) {
	t.Parallel()

	first := canonicalModelProto(t)
	second := canonicalModelProto(t)
	second.Id = "server-generated"
	definitions := second.GetTypeDefinitions()
	definitions[0], definitions[1] = definitions[1], definitions[0]

	if !modelsEqual(first, second) {
		t.Fatal("semantically identical models compared unequal")
	}
}

func TestModelsEqualDetectsPermissionRewriteChanges(t *testing.T) {
	t.Parallel()

	first := canonicalModelProto(t)
	second := canonicalModelProto(t)
	for _, definition := range second.GetTypeDefinitions() {
		if definition.GetType() != "stack" {
			continue
		}
		definition.GetRelations()["can_manage_access"] = &openfgav1.Userset{
			Userset: &openfgav1.Userset_ComputedUserset{
				ComputedUserset: &openfgav1.ObjectRelation{Relation: "viewer"},
			},
		}
	}

	if modelsEqual(first, second) {
		t.Fatal("security-relevant rewrite change compared equal")
	}
}

// TestModelsEqualIgnoresEmptyRelationMetadata pins the one normalization that
// is not ordering: a metadata entry with no directly related user types says
// nothing about who may be assigned, but it is a present map key that
// proto.Equal would otherwise read as a difference.
func TestModelsEqualIgnoresEmptyRelationMetadata(t *testing.T) {
	t.Parallel()

	withEntries := canonicalModelProto(t)
	withoutEntries := canonicalModelProto(t)

	stripped := 0
	for _, definition := range withoutEntries.GetTypeDefinitions() {
		relations := definition.GetMetadata().GetRelations()
		for relation, entry := range relations {
			if len(entry.GetDirectlyRelatedUserTypes()) == 0 {
				delete(relations, relation)
				stripped++
			}
		}
	}
	if stripped == 0 {
		t.Fatal("canonical model has no empty relation metadata to strip")
	}

	if !modelsEqual(withEntries, withoutEntries) {
		t.Fatal("models differing only in empty relation metadata compared unequal")
	}
}

// TestModelsEqualIgnoresDirectTypeOrdering covers the second repeated field.
// It is built rather than taken from the canonical model, because no relation
// there has more than one direct type today and a test that silently skipped
// would stop covering the sort the moment it mattered.
func TestModelsEqualIgnoresDirectTypeOrdering(t *testing.T) {
	t.Parallel()

	build := func(types ...string) *openfgav1.AuthorizationModel {
		related := make([]*openfgav1.RelationReference, 0, len(types))
		for _, name := range types {
			related = append(related, &openfgav1.RelationReference{Type: name})
		}
		return &openfgav1.AuthorizationModel{
			SchemaVersion: "1.1",
			TypeDefinitions: []*openfgav1.TypeDefinition{{
				Type:      "stack",
				Relations: map[string]*openfgav1.Userset{"viewer": {Userset: &openfgav1.Userset_This{}}},
				Metadata: &openfgav1.Metadata{
					Relations: map[string]*openfgav1.RelationMetadata{
						"viewer": {DirectlyRelatedUserTypes: related},
					},
				},
			}},
		}
	}

	if !modelsEqual(build("user", "platform"), build("platform", "user")) {
		t.Fatal("models differing only in direct type ordering compared unequal")
	}
	if modelsEqual(build("user", "platform"), build("user", "service")) {
		t.Fatal("models with different direct types compared equal")
	}
}

// TestModelsEqualIgnoresAnEmptyMetadataMessage covers a type that declares no
// relations at all, such as "user".
//
// The DSL transform leaves its metadata unset, while a model encoded by
// something that materializes the field comes back carrying "metadata":{}.
// proto.Equal reads present-but-empty and absent as different; they are not.
func TestModelsEqualIgnoresAnEmptyMetadataMessage(t *testing.T) {
	t.Parallel()

	build := func(metadata *openfgav1.Metadata) *openfgav1.AuthorizationModel {
		return &openfgav1.AuthorizationModel{
			SchemaVersion:   "1.1",
			TypeDefinitions: []*openfgav1.TypeDefinition{{Type: "user", Metadata: metadata}},
		}
	}

	if !modelsEqual(build(nil), build(&openfgav1.Metadata{})) {
		t.Fatal("an empty metadata message compared unequal to an absent one")
	}
	if modelsEqual(build(nil), build(&openfgav1.Metadata{Module: "core"})) {
		t.Fatal("a metadata message carrying a module compared equal to an absent one")
	}
}

// TestCanonicalModelIsNotMutatedByComparison guards the clone in canonicalModel.
// resolveModel compares the cached desired model against every stored one, so a
// comparison that edited its input would corrupt every later comparison.
func TestCanonicalModelIsNotMutatedByComparison(t *testing.T) {
	t.Parallel()

	model := canonicalModelProto(t)
	before, err := protojson.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}

	modelsEqual(model, canonicalModelProto(t))

	after, err := protojson.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("comparing models mutated one of them")
	}
}

func TestCanonicalModelAllowsDirectWritesOnlyToRoles(t *testing.T) {
	t.Parallel()

	model := canonicalModelProto(t)

	// parent is the one structural exception: directly assignable like a role,
	// but its direct type is the platform singleton rather than a user. It is
	// what carries administrator inheritance onto a stack, so it has to be
	// writable -- which is exactly why grantableRelations excludes it.
	assertDirectTypes(t, model, "stack", map[string]string{
		"owner": "user", "operator": "user", "approver": "user", "viewer": "user",
		"parent": "platform",
	})

	// Every platform tier is a direct user grant; every capability derives.
	assertDirectTypes(t, model, "platform", map[string]string{
		"root": "user", "admin": "user", "editor": "user", "viewer": "user",
	})
}

// assertDirectTypes pins which relations of a type are directly writable and
// what may occupy the user slot of each. A relation absent from want must
// derive, which is what stops a client writing a permission straight in.
func assertDirectTypes(t *testing.T, model *openfgav1.AuthorizationModel, typeName string, want map[string]string) {
	t.Helper()

	var definition *openfgav1.TypeDefinition
	for _, candidate := range model.GetTypeDefinitions() {
		if candidate.GetType() == typeName {
			definition = candidate
		}
	}
	if definition == nil {
		t.Fatalf("canonical model is missing the %s type", typeName)
	}

	for relation := range definition.GetRelations() {
		related := definition.GetMetadata().GetRelations()[relation].GetDirectlyRelatedUserTypes()
		wantType, direct := want[relation]
		if hasDirectType := len(related) != 0; hasDirectType != direct {
			t.Fatalf("%s relation %s direct assignment = %v, want %v", typeName, relation, hasDirectType, direct)
		}
		for _, reference := range related {
			if reference.GetType() != wantType || reference.GetRelation() != "" || reference.GetWildcard() != nil {
				t.Fatalf("%s relation %s has unsafe direct type %v", typeName, relation, reference)
			}
		}
	}
}

// canonicalModelProto is the embedded model, or a fatal test error when the
// embedded DSL does not transform. Each call returns an independent copy, so a
// test may edit one side freely.
func canonicalModelProto(t *testing.T) *openfgav1.AuthorizationModel {
	t.Helper()
	model, err := authorizationModel()
	if err != nil {
		t.Fatal(err)
	}
	return proto.Clone(model).(*openfgav1.AuthorizationModel)
}
