package authdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/queue"
)

const testSubjectSub = "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91"

type fakeRelationships struct {
	existing []authz.Grant
	written  []authz.Grant
	deleted  []authz.Grant
	listErr  error
	writeErr error
}

func (f *fakeRelationships) ListSubjectGrants(_ context.Context, _ authz.ListSubjectGrantsRequest) (authz.ListGrantsResult, error) {
	if f.listErr != nil {
		return authz.ListGrantsResult{}, f.listErr
	}
	return authz.ListGrantsResult{Grants: f.existing}, nil
}

func (f *fakeRelationships) WriteRelationships(_ context.Context, mutation authz.Mutation) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.written = append(f.written, mutation.Grants()...)
	return nil
}

func (f *fakeRelationships) DeleteRelationships(_ context.Context, mutation authz.Mutation) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.deleted = append(f.deleted, mutation.Grants()...)
	return nil
}

func mustGrant(t *testing.T, stackID, subject string, role authz.Role) authz.Grant {
	t.Helper()
	stack, err := authz.StackFromID(stackID)
	if err != nil {
		t.Fatalf("StackFromID(%q): %v", stackID, err)
	}
	sub, err := authz.SubjectFromKeycloakSub(subject)
	if err != nil {
		t.Fatalf("SubjectFromKeycloakSub(%q): %v", subject, err)
	}
	grant, err := authz.NewGrant(sub, stack, role)
	if err != nil {
		t.Fatalf("NewGrant: %v", err)
	}
	return grant
}

func grantPayload(role string) json.RawMessage {
	payload, _ := json.Marshal(GrantPayload{StackID: "stack_abc", Subject: testSubjectSub, Role: role})
	return payload
}

func TestKeyUsesCanonicalIdentifiers(t *testing.T) {
	t.Parallel()

	handler := NewStackGrantHandler(&fakeRelationships{})
	key, err := handler.Key(grantPayload("owner"))
	if err != nil {
		t.Fatalf("Key returned error: %v", err)
	}
	want := "stack:stack_abc/user:" + testSubjectSub
	if key != want {
		t.Fatalf("Key = %q, want %q", key, want)
	}
}

func TestKeyRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	handler := NewStackGrantHandler(&fakeRelationships{})
	if _, err := handler.Key(json.RawMessage(`{"stack_id":"","subject":""}`)); err == nil {
		t.Fatal("Key accepted an empty identity")
	}
}

func TestKindAndModeAreReconcile(t *testing.T) {
	t.Parallel()

	handler := NewStackGrantHandler(&fakeRelationships{})
	if handler.Mode() != queue.ModeReconcile {
		t.Fatal("stack grant handler must be ModeReconcile")
	}
	if handler.Kind() != KindReconcileStackGrant {
		t.Fatalf("Kind = %q, want %q", handler.Kind(), KindReconcileStackGrant)
	}
}

func TestDeliverWritesDesiredRoleWhenAbsent(t *testing.T) {
	t.Parallel()

	relationships := &fakeRelationships{}
	handler := NewStackGrantHandler(relationships)

	if err := handler.Deliver(context.Background(), queue.Item{Payload: grantPayload("owner")}); err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if len(relationships.written) != 1 || relationships.written[0].Role() != authz.RoleOwner {
		t.Fatalf("written = %+v, want one owner grant", relationships.written)
	}
	if len(relationships.deleted) != 0 {
		t.Fatalf("deleted = %+v, want none", relationships.deleted)
	}
}

func TestDeliverReplacesExistingRole(t *testing.T) {
	t.Parallel()

	relationships := &fakeRelationships{
		existing: []authz.Grant{mustGrant(t, "stack_abc", testSubjectSub, authz.RoleViewer)},
	}
	handler := NewStackGrantHandler(relationships)

	if err := handler.Deliver(context.Background(), queue.Item{Payload: grantPayload("owner")}); err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if len(relationships.written) != 1 || relationships.written[0].Role() != authz.RoleOwner {
		t.Fatalf("written = %+v, want one owner grant", relationships.written)
	}
	if len(relationships.deleted) != 1 || relationships.deleted[0].Role() != authz.RoleViewer {
		t.Fatalf("deleted = %+v, want the stale viewer grant", relationships.deleted)
	}
}

func TestDeliverIsIdempotentWhenAlreadyConverged(t *testing.T) {
	t.Parallel()

	relationships := &fakeRelationships{
		existing: []authz.Grant{mustGrant(t, "stack_abc", testSubjectSub, authz.RoleOwner)},
	}
	handler := NewStackGrantHandler(relationships)

	for attempt := 0; attempt < 2; attempt++ {
		if err := handler.Deliver(context.Background(), queue.Item{Payload: grantPayload("owner")}); err != nil {
			t.Fatalf("Deliver attempt %d returned error: %v", attempt, err)
		}
	}
	if len(relationships.written) != 0 || len(relationships.deleted) != 0 {
		t.Fatalf("converged state caused writes: written=%+v deleted=%+v", relationships.written, relationships.deleted)
	}
}

func TestDeliverEmptyRoleRevokesEverything(t *testing.T) {
	t.Parallel()

	relationships := &fakeRelationships{
		existing: []authz.Grant{mustGrant(t, "stack_abc", testSubjectSub, authz.RoleOperator)},
	}
	handler := NewStackGrantHandler(relationships)

	if err := handler.Deliver(context.Background(), queue.Item{Payload: grantPayload("")}); err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if len(relationships.written) != 0 {
		t.Fatalf("written = %+v, want none", relationships.written)
	}
	if len(relationships.deleted) != 1 || relationships.deleted[0].Role() != authz.RoleOperator {
		t.Fatalf("deleted = %+v, want the operator grant", relationships.deleted)
	}
}

func TestDeliverPropagatesListError(t *testing.T) {
	t.Parallel()

	relationships := &fakeRelationships{listErr: errors.New("openfga unavailable")}
	handler := NewStackGrantHandler(relationships)

	if err := handler.Deliver(context.Background(), queue.Item{Payload: grantPayload("owner")}); err == nil {
		t.Fatal("Deliver swallowed the list error — the item must be retried")
	}
}
