package authorization_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/openfga/openfga/pkg/storage/memory"
	"github.com/stretchr/testify/require"

	"github.com/vishu42/tflive/internal/authorization"
)

// newTestAuthorization runs a real engine over an in-memory datastore. This is
// why the package needs no interface: the real thing is cheap enough to build,
// so the tests exercise the actual model -- including its tier ordering --
// rather than a fake that restates what the model is assumed to say.
func newTestAuthorization(t *testing.T) *authorization.Authorization {
	t.Helper()
	auth, err := authorization.NewWithDatastore(context.Background(), memory.New(), "tflive-test")
	require.NoError(t, err)
	t.Cleanup(auth.Close)
	return auth
}

func stackObject(t *testing.T, id string) authorization.Object {
	t.Helper()
	object, err := authorization.ObjectFromID(authorization.TypeStack, id)
	require.NoError(t, err)
	return object
}

func stackGrant(t *testing.T, sub, stackID string, relation authorization.Relation) authorization.Grant {
	t.Helper()
	subject, err := authorization.SubjectFromOIDCSub(sub)
	require.NoError(t, err)
	grant, err := authorization.NewGrant(subject, stackObject(t, stackID), relation)
	require.NoError(t, err)
	return grant
}

func platformGrant(t *testing.T, sub string, relation authorization.Relation) authorization.Grant {
	t.Helper()
	subject, err := authorization.SubjectFromOIDCSub(sub)
	require.NoError(t, err)
	grant, err := authorization.NewGrant(subject, authorization.Platform, relation)
	require.NoError(t, err)
	return grant
}

func structuralParent(t *testing.T, stackID string) authorization.Grant {
	t.Helper()
	grant, err := authorization.NewStructuralRelationship(
		authorization.PlatformSubject, stackObject(t, stackID), authorization.RelationParent)
	require.NoError(t, err)
	return grant
}

// TestCanReflectsTheModelsTierOrdering is the test a fake could not have
// written: admin satisfies can_create_stack only through can_administer ->
// can_edit, which is the model's business and must never be restated in Go.
func TestCanReflectsTheModelsTierOrdering(t *testing.T) {
	ctx := context.Background()
	auth := newTestAuthorization(t)
	require.NoError(t, auth.Grant(ctx, platformGrant(t, "alice", authorization.RelationAdmin)))

	allowed, err := auth.Can(ctx, "alice", authorization.RelationCanCreateStack, authorization.Platform)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestCanReturnsFalseNilForADenial(t *testing.T) {
	allowed, err := newTestAuthorization(t).Can(context.Background(), "nobody",
		authorization.RelationCanCreateStack, authorization.Platform)
	require.NoError(t, err, "a denial is an answer, not a failure")
	require.False(t, allowed)
}

func TestCanRejectsAMalformedSubject(t *testing.T) {
	_, err := newTestAuthorization(t).Can(context.Background(), "al#ce",
		authorization.RelationCanCreateStack, authorization.Platform)
	require.ErrorIs(t, err, authorization.ErrInvalidInput)
}

// TestStackOwnerInheritsThroughTheParentEdge pins the structural half of the
// model: a platform administrator reaches a stack only because the parent edge
// is stored on it.
func TestStackOwnerInheritsThroughTheParentEdge(t *testing.T) {
	ctx := context.Background()
	auth := newTestAuthorization(t)
	require.NoError(t, auth.Grant(ctx,
		platformGrant(t, "admin", authorization.RelationAdmin),
		structuralParent(t, "one"),
	))

	allowed, err := auth.Can(ctx, "admin", authorization.RelationCanManageAccess, stackObject(t, "one"))
	require.NoError(t, err)
	require.True(t, allowed, "an administrator reaches a stack through its parent edge")

	// Without the edge, the same administrator must not reach a different stack.
	allowed, err = auth.Can(ctx, "admin", authorization.RelationCanManageAccess, stackObject(t, "two"))
	require.NoError(t, err)
	require.False(t, allowed)
}

// TestCanAllPreservesInputOrderAcrossChunks exercises the two-chunk path
// (50 + 2) that #220 found. An implementation that ranged the response map
// instead of driving from the input would pass with one chunk and attribute
// answers to the wrong stacks here.
func TestCanAllPreservesInputOrderAcrossChunks(t *testing.T) {
	ctx := context.Background()
	auth := newTestAuthorization(t)

	checks := make([]authorization.Check, 52)
	for i := range checks {
		checks[i] = authorization.Check{
			Relation: authorization.RelationCanView,
			Object:   stackObject(t, fmt.Sprintf("s%02d", i)),
		}
	}
	require.NoError(t, auth.Grant(ctx, stackGrant(t, "alice", "s07", authorization.RelationOwner)))

	results, err := auth.CanAll(ctx, "alice", checks)
	require.NoError(t, err)
	require.Len(t, results, 52)
	for i, allowed := range results {
		require.Equal(t, i == 7, allowed, "answer %d landed against the wrong question", i)
	}
}

func TestCanAllWithNoChecksAsksNothing(t *testing.T) {
	results, err := newTestAuthorization(t).CanAll(context.Background(), "alice", nil)
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestListGrantsSkipsStructuralEdges(t *testing.T) {
	ctx := context.Background()
	auth := newTestAuthorization(t)
	require.NoError(t, auth.Grant(ctx,
		stackGrant(t, "alice", "one", authorization.RelationOwner),
		structuralParent(t, "one"),
	))

	grants, err := auth.ListGrants(ctx, stackObject(t, "one"))
	require.NoError(t, err)
	require.Len(t, grants, 1, "the parent edge is structure, not access")
	require.Equal(t, "user:alice", grants[0].Subject().String())
	require.Equal(t, "owner", grants[0].Relation().String())
}

func TestListGrantsSortsBySubjectThenRelation(t *testing.T) {
	ctx := context.Background()
	auth := newTestAuthorization(t)
	require.NoError(t, auth.Grant(ctx,
		stackGrant(t, "bob", "one", authorization.RelationViewer),
		stackGrant(t, "alice", "one", authorization.RelationOwner),
		stackGrant(t, "alice", "one", authorization.RelationApprover),
	))

	grants, err := auth.ListGrants(ctx, stackObject(t, "one"))
	require.NoError(t, err)
	rendered := make([]string, len(grants))
	for i, grant := range grants {
		rendered[i] = grant.Subject().String() + "/" + grant.Relation().String()
	}
	require.Equal(t, []string{"user:alice/approver", "user:alice/owner", "user:bob/viewer"}, rendered)
}

func TestGrantAndRevokeRoundTrip(t *testing.T) {
	ctx := context.Background()
	auth := newTestAuthorization(t)
	grant := stackGrant(t, "alice", "one", authorization.RelationOwner)

	require.NoError(t, auth.Grant(ctx, grant))
	allowed, err := auth.Can(ctx, "alice", authorization.RelationCanView, stackObject(t, "one"))
	require.NoError(t, err)
	require.True(t, allowed)

	require.NoError(t, auth.Revoke(ctx, grant))
	allowed, err = auth.Can(ctx, "alice", authorization.RelationCanView, stackObject(t, "one"))
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestGrantRejectsAnEmptyOrDuplicateMutation(t *testing.T) {
	ctx := context.Background()
	auth := newTestAuthorization(t)
	grant := stackGrant(t, "alice", "one", authorization.RelationOwner)

	require.ErrorIs(t, auth.Grant(ctx), authorization.ErrInvalidInput)
	require.ErrorIs(t, auth.Grant(ctx, grant, grant), authorization.ErrInvalidInput)
	require.ErrorIs(t, auth.Revoke(ctx), authorization.ErrInvalidInput)
}

// TestGrantIsAllOrNothing pins the transactional promise at the OpenFGA level:
// a mutation carrying one tuple the server refuses writes none of them.
//
// The refusal used here is a re-grant of a tuple that already exists, which is
// what a replayed request looks like. validGrants cannot catch it -- the two
// grants in the request are different from each other -- so it reaches OpenFGA
// and the whole write is rejected.
func TestGrantIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	auth := newTestAuthorization(t)

	existing := stackGrant(t, "alice", "one", authorization.RelationOwner)
	require.NoError(t, auth.Grant(ctx, existing))

	fresh := stackGrant(t, "bob", "one", authorization.RelationViewer)
	require.Error(t, auth.Grant(ctx, fresh, existing),
		"re-granting an existing tuple must fail the whole mutation")

	grants, err := auth.ListGrants(ctx, stackObject(t, "one"))
	require.NoError(t, err)
	require.Len(t, grants, 1, "the fresh grant must not survive a rejected mutation")
	require.Equal(t, "user:alice", grants[0].Subject().String())
}

