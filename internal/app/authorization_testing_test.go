package app

import (
	"context"
	"testing"

	"github.com/openfga/openfga/pkg/storage/memory"

	"github.com/vishu42/tflive/internal/authorization"
)

// This file replaces the hand-written authorizer fakes the suite used while
// authorization was reached through an interface.
//
// There is no interface any more, so tests run a real engine over an in-memory
// datastore and seed real tuples. That is cheap -- no Postgres, no container --
// and it is more honest: a fake had to restate what the model says, and could
// restate it wrongly. These helpers state only what a user was granted, and the
// model decides what that implies.

// newTestAuthorization builds a real Authorization backed by memory.
func newTestAuthorization(t *testing.T) *authorization.Authorization {
	t.Helper()
	auth, err := authorization.NewWithDatastore(context.Background(), memory.New(), "tflive-test")
	if err != nil {
		t.Fatalf("build test authorization: %v", err)
	}
	t.Cleanup(auth.Close)
	return auth
}

// grantPlatform gives a subject a platform-level role. Capabilities follow from
// the model's tier ordering rather than from anything written here.
func grantPlatform(t *testing.T, auth *authorization.Authorization, sub string, relation authorization.Relation) {
	t.Helper()
	subject, err := authorization.SubjectFromOIDCSub(sub)
	if err != nil {
		t.Fatalf("subject %q: %v", sub, err)
	}
	grant, err := authorization.NewGrant(subject, authorization.Platform, relation)
	if err != nil {
		t.Fatalf("platform grant: %v", err)
	}
	if err := auth.Grant(context.Background(), grant); err != nil {
		t.Fatalf("write platform grant: %v", err)
	}
}

// grantStack gives a subject a role on one stack, and stores the parent edge
// the model needs for platform administrators to reach it.
func grantStack(t *testing.T, auth *authorization.Authorization, sub, stackID string, relation authorization.Relation) {
	t.Helper()
	subject, err := authorization.SubjectFromOIDCSub(sub)
	if err != nil {
		t.Fatalf("subject %q: %v", sub, err)
	}
	object, err := authorization.ObjectFromID(authorization.TypeStack, stackID)
	if err != nil {
		t.Fatalf("stack %q: %v", stackID, err)
	}
	grant, err := authorization.NewGrant(subject, object, relation)
	if err != nil {
		t.Fatalf("stack grant: %v", err)
	}
	if err := auth.Grant(context.Background(), grant); err != nil {
		t.Fatalf("write stack grant: %v", err)
	}
}

// linkStackToPlatform stores the parent edge on a stack, which is what lets a
// platform administrator reach it.
func linkStackToPlatform(t *testing.T, auth *authorization.Authorization, stackID string) {
	t.Helper()
	object, err := authorization.ObjectFromID(authorization.TypeStack, stackID)
	if err != nil {
		t.Fatalf("stack %q: %v", stackID, err)
	}
	edge, err := authorization.NewStructuralRelationship(
		authorization.PlatformSubject, object, authorization.RelationParent)
	if err != nil {
		t.Fatalf("parent edge: %v", err)
	}
	if err := auth.Grant(context.Background(), edge); err != nil {
		t.Fatalf("write parent edge: %v", err)
	}
}

// seedGrants writes grants into an already-built Authorization and returns it,
// so a test can state a starting state inline where it used to hand a fake a
// slice of grants to return.
func seedGrants(t *testing.T, auth *authorization.Authorization, grants ...authorization.Grant) *authorization.Authorization {
	t.Helper()
	if len(grants) == 0 {
		return auth
	}
	if err := auth.Grant(context.Background(), grants...); err != nil {
		t.Fatalf("seed grants: %v", err)
	}
	return auth
}
