package authorization

import (
	"sort"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/proto"
)

// modelsEqual reports whether two models say the same thing.
//
// Both sides are already protobuf -- ours from the DSL transform, the stored
// one straight off the wire -- so proto.Equal answers this directly. It is the
// canonicalization below that earns its place, because a false negative does
// not fail loudly: resolveModel would write a new model version and re-pin the
// process on every restart, moving the id out from under tuples written
// against the old one.
//
//	the same model, one carrying a server-minted id → true
//	type definitions in a different order           → true
//	a changed permission rewrite                    → false
func modelsEqual(left, right *openfgav1.AuthorizationModel) bool {
	return proto.Equal(canonicalModel(left), canonicalModel(right))
}

// canonicalModel returns a copy holding only what the model means.
//
// proto.Equal compares map fields order-independently but repeated fields in
// order, so the two repeated fields are sorted. Ordering has not differed in
// practice on any round trip we have measured; sorting is insurance against a
// version that changes it, not a bug being worked around.
func canonicalModel(model *openfgav1.AuthorizationModel) *openfgav1.AuthorizationModel {
	canonical := proto.Clone(model).(*openfgav1.AuthorizationModel)

	// Ids are the server's to mint, so one side having it is not a difference.
	canonical.Id = ""

	definitions := canonical.GetTypeDefinitions()
	sort.Slice(definitions, func(i, j int) bool {
		return definitions[i].GetType() < definitions[j].GetType()
	})
	for _, definition := range definitions {
		canonicalizeMetadata(definition.GetMetadata())
		if isEmptyMetadata(definition.GetMetadata()) {
			definition.Metadata = nil
		}
	}
	return canonical
}

// isEmptyMetadata reports whether a metadata message carries nothing.
//
// A present-but-empty message and an absent one are different to proto.Equal
// but identical in meaning, and both forms occur: a model stored by an encoder
// that materializes the field comes back with "metadata":{} on a type that has
// no relations, where the DSL transform leaves it unset.
func isEmptyMetadata(metadata *openfgav1.Metadata) bool {
	return len(metadata.GetRelations()) == 0 &&
		metadata.GetModule() == "" &&
		metadata.GetSourceInfo() == nil
}

// canonicalizeMetadata drops metadata entries that say nothing and orders those
// that do.
//
// An entry with no directly related user types carries no information about who
// may be assigned, but it is still a present map key, which proto.Equal would
// read as a difference from a model that omits it.
func canonicalizeMetadata(metadata *openfgav1.Metadata) {
	for relation, entry := range metadata.GetRelations() {
		related := entry.GetDirectlyRelatedUserTypes()
		if len(related) == 0 {
			delete(metadata.GetRelations(), relation)
			continue
		}
		sort.Slice(related, func(i, j int) bool {
			return relationReferenceKey(related[i]) < relationReferenceKey(related[j])
		})
	}
}

// relationReferenceKey builds a total order over relation references from their
// fields.
//
// Spelled out rather than derived from an encoding: prototext deliberately
// varies its whitespace, and a key that is not stable for a given message
// could order the two sides differently and make equal models compare unequal.
func relationReferenceKey(reference *openfgav1.RelationReference) string {
	wildcard := ""
	if reference.GetWildcard() != nil {
		wildcard = "*"
	}
	return strings.Join([]string{
		reference.GetType(),
		reference.GetRelation(),
		wildcard,
		reference.GetCondition(),
	}, "\x00")
}
