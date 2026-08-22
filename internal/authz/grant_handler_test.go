package authz

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vishu42/tflive/internal/queue"
)

const testSubjectSub = "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91"

type fakeRelationships struct {
	existing []Grant
	written  []Grant
	deleted  []Grant
	listErr  error
	writeErr error
}

func (f *fakeRelationships) ListSubjectGrants(_ context.Context, _ ListSubjectGrantsRequest) (ListGrantsResult, error) {
	if f.listErr != nil {
		return ListGrantsResult{}, f.listErr
	}
	return ListGrantsResult{Grants: f.existing}, nil
}

func (f *fakeRelationships) WriteRelationships(_ context.Context, mutation Mutation) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.written = append(f.written, mutation.Grants()...)
	return nil
}

func (f *fakeRelationships) DeleteRelationships(_ context.Context, mutation Mutation) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.deleted = append(f.deleted, mutation.Grants()...)
	return nil
}

func mustGrant(t *testing.T, stackID, subject string, role Relation) Grant {
	t.Helper()
	stack, err := ObjectFromID(TypeStack, stackID)
	if err != nil {
		t.Fatalf("ObjectFromID(TypeStack, %q): %v", stackID, err)
	}
	sub, err := SubjectFromOIDCSub(subject)
	if err != nil {
		t.Fatalf("SubjectFromOIDCSub(%q): %v", subject, err)
	}
	grant, err := NewGrant(sub, stack, role)
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

	key, err := StackGrantSpec.Key(grantPayload("owner"))
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

	if _, err := StackGrantSpec.Key(json.RawMessage(`{"stack_id":"","subject":""}`)); err == nil {
		t.Fatal("Key accepted an empty identity")
	}
}

func TestKindAndModeAreReconcile(t *testing.T) {
	t.Parallel()

	spec := NewStackGrantHandler(&fakeRelationships{}).Spec()
	if spec.Mode != queue.ModeReconcile {
		t.Fatal("stack grant handler must be ModeReconcile")
	}
	if spec.Kind != KindReconcileStackGrant {
		t.Fatalf("Kind = %q, want %q", spec.Kind, KindReconcileStackGrant)
	}
}

func TestDeliverWritesDesiredRoleWhenAbsent(t *testing.T) {
	t.Parallel()

	relationships := &fakeRelationships{}
	handler := NewStackGrantHandler(relationships)

	followUps, err := handler.Deliver(context.Background(), queue.Item{Payload: grantPayload("owner")})
	if err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if len(followUps) != 0 {
		t.Fatalf("follow-ups = %+v, want none: a grant reconcile chains nothing", followUps)
	}
	if len(relationships.written) != 1 || relationships.written[0].Relation() != RelationOwner {
		t.Fatalf("written = %+v, want one owner grant", relationships.written)
	}
	if len(relationships.deleted) != 0 {
		t.Fatalf("deleted = %+v, want none", relationships.deleted)
	}
}

func TestDeliverReplacesExistingRole(t *testing.T) {
	t.Parallel()

	relationships := &fakeRelationships{
		existing: []Grant{mustGrant(t, "stack_abc", testSubjectSub, RelationViewer)},
	}
	handler := NewStackGrantHandler(relationships)

	if _, err := handler.Deliver(context.Background(), queue.Item{Payload: grantPayload("owner")}); err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if len(relationships.written) != 1 || relationships.written[0].Relation() != RelationOwner {
		t.Fatalf("written = %+v, want one owner grant", relationships.written)
	}
	if len(relationships.deleted) != 1 || relationships.deleted[0].Relation() != RelationViewer {
		t.Fatalf("deleted = %+v, want the stale viewer grant", relationships.deleted)
	}
}

func TestDeliverIsIdempotentWhenAlreadyConverged(t *testing.T) {
	t.Parallel()

	relationships := &fakeRelationships{
		existing: []Grant{mustGrant(t, "stack_abc", testSubjectSub, RelationOwner)},
	}
	handler := NewStackGrantHandler(relationships)

	for attempt := 0; attempt < 2; attempt++ {
		if _, err := handler.Deliver(context.Background(), queue.Item{Payload: grantPayload("owner")}); err != nil {
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
		existing: []Grant{mustGrant(t, "stack_abc", testSubjectSub, RelationOperator)},
	}
	handler := NewStackGrantHandler(relationships)

	if _, err := handler.Deliver(context.Background(), queue.Item{Payload: grantPayload("")}); err != nil {
		t.Fatalf("Deliver returned error: %v", err)
	}
	if len(relationships.written) != 0 {
		t.Fatalf("written = %+v, want none", relationships.written)
	}
	if len(relationships.deleted) != 1 || relationships.deleted[0].Relation() != RelationOperator {
		t.Fatalf("deleted = %+v, want the operator grant", relationships.deleted)
	}
}

// Pins a frozen contract. The key is persisted, so a format change splits one
// resource across two keys and disables the queue's mutual exclusion.
func TestStackGrantKeyFormatIsUnchanged(t *testing.T) {
	// The key is persisted. Changing its format splits one resource across two
	// keys and disables the queue's mutual exclusion.
	key, err := stackGrantKey([]byte(`{"stack_id":"stack-123","subject":"kc-sub-456","role":"viewer"}`))
	if err != nil {
		t.Fatalf("stackGrantKey() error = %v", err)
	}
	if key != "stack:stack-123/user:kc-sub-456" {
		t.Fatalf("stackGrantKey() = %q, want %q", key, "stack:stack-123/user:kc-sub-456")
	}
}

func TestDeliverPropagatesListError(t *testing.T) {
	t.Parallel()

	relationships := &fakeRelationships{listErr: errors.New("openfga unavailable")}
	handler := NewStackGrantHandler(relationships)

	if _, err := handler.Deliver(context.Background(), queue.Item{Payload: grantPayload("owner")}); err == nil {
		t.Fatal("Deliver swallowed the list error — the item must be retried")
	}
}
