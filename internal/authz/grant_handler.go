package authz

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vishu42/tflive/internal/queue"
)

// KindReconcileStackGrant names the work kind this handler serves.
const KindReconcileStackGrant queue.Kind = "reconcile_stack_grant"

// GrantPayload is the desired state for one subject on one stack. An empty
// Role means the subject should hold no access at all.
type GrantPayload struct {
	StackID string `json:"stack_id"`
	Subject string `json:"subject"`
	Role    string `json:"role"`
}

// Relationships is the slice of the authorization port this handler needs.
type Relationships interface {
	SubjectGrantLister
	WriteRelationships(context.Context, Mutation) error
	DeleteRelationships(context.Context, Mutation) error
}

// StackGrantHandler converges a subject's roles on a stack to the desired
// state in the payload. It is idempotent: re-applying converged state performs
// no writes at all, which is what makes at-least-once delivery safe.
type StackGrantHandler struct {
	relationships Relationships
}

func NewStackGrantHandler(relationships Relationships) *StackGrantHandler {
	return &StackGrantHandler{relationships: relationships}
}

// StackGrantSpec declares the reconcile_stack_grant kind. Producers register
// it alone; only the worker also constructs the handler.
//
// The key is "stack:<id>/user:<sub>", built from the canonical formatters so it
// cannot drift from the identity OpenFGA uses.
//
// This derivation is a frozen contract: keys are persisted, and changing the
// format splits one resource across two keys, disabling the mutual exclusion
// the unique partial index provides. To change it, drain the queue with
// producers stopped, or introduce a new Kind and let the old one drain.
var StackGrantSpec = queue.Spec{
	Kind: KindReconcileStackGrant,
	Mode: queue.ModeReconcile,
	Key:  stackGrantKey,
}

func stackGrantKey(payload json.RawMessage) (string, error) {
	identity, err := parseGrantPayload(payload)
	if err != nil {
		return "", err
	}
	return identity.stack.String() + "/" + identity.subject.String(), nil
}

func (handler *StackGrantHandler) Spec() queue.Spec { return StackGrantSpec }

// Deliver reads the subject's current roles on the stack and converges them to
// the desired state: stale roles are removed, and the desired role is written
// only when it is not already held. It never touches stacks.status, so a
// failing role change cannot regress a ready stack, and it chains nothing.
func (handler *StackGrantHandler) Deliver(ctx context.Context, item queue.Item) ([]queue.Request, error) {
	identity, err := parseGrantPayload(item.Payload)
	if err != nil {
		return nil, err
	}

	current, err := handler.relationships.ListSubjectGrants(ctx, ListSubjectGrantsRequest{
		Subject: identity.subject,
		Stack:   identity.stack,
	})
	if err != nil {
		return nil, fmt.Errorf("read current stack grants: %w", err)
	}

	var stale []Grant
	satisfied := false
	for _, grant := range current.Grants {
		if identity.hasRole && grant.Role() == identity.role {
			satisfied = true
			continue
		}
		stale = append(stale, grant)
	}

	if len(stale) > 0 {
		mutation, err := NewMutation(stale, true)
		if err != nil {
			return nil, fmt.Errorf("build stale grant mutation: %w", err)
		}
		if err := handler.relationships.DeleteRelationships(ctx, mutation); err != nil {
			return nil, fmt.Errorf("remove stale stack grants: %w", err)
		}
	}

	if !identity.hasRole || satisfied {
		return nil, nil
	}

	grant, err := NewGrant(identity.subject, identity.stack, identity.role)
	if err != nil {
		return nil, fmt.Errorf("build desired grant: %w", err)
	}
	mutation, err := NewMutation([]Grant{grant}, true)
	if err != nil {
		return nil, fmt.Errorf("build desired mutation: %w", err)
	}
	if err := handler.relationships.WriteRelationships(ctx, mutation); err != nil {
		return nil, fmt.Errorf("write desired stack grant: %w", err)
	}
	return nil, nil
}

// grantIdentity is the parsed payload. Role is a struct, not a string, so
// the role is parsed once here and compared by value; hasRole distinguishes
// "grant this role" from "revoke everything".
type grantIdentity struct {
	stack   Stack
	subject Subject
	role    Role
	hasRole bool
}

func parseGrantPayload(payload json.RawMessage) (grantIdentity, error) {
	var parsed GrantPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return grantIdentity{}, fmt.Errorf("decode stack grant payload: %w", err)
	}
	stack, err := StackFromID(parsed.StackID)
	if err != nil {
		return grantIdentity{}, fmt.Errorf("parse stack grant stack: %w", err)
	}
	subject, err := SubjectFromKeycloakSub(parsed.Subject)
	if err != nil {
		return grantIdentity{}, fmt.Errorf("parse stack grant subject: %w", err)
	}
	identity := grantIdentity{stack: stack, subject: subject}
	if parsed.Role != "" {
		role, err := RoleFromDirectRelation(parsed.Role)
		if err != nil {
			return grantIdentity{}, fmt.Errorf("parse stack grant role: %w", err)
		}
		identity.role = role
		identity.hasRole = true
	}
	return identity, nil
}
