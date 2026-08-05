package queue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type stubHandler struct {
	kind Kind
	mode Mode
	key  string
	err  error
}

func (h stubHandler) Kind() Kind { return h.kind }
func (h stubHandler) Mode() Mode { return h.mode }
func (h stubHandler) Key(json.RawMessage) (string, error) {
	if h.err != nil {
		return "", h.err
	}
	return h.key, nil
}
func (h stubHandler) Deliver(context.Context, Item) error { return nil }

func TestNewRegistryRejectsDuplicateKinds(t *testing.T) {
	t.Parallel()

	_, err := NewRegistry(
		stubHandler{kind: "a", key: "k"},
		stubHandler{kind: "a", key: "k"},
	)
	if err == nil {
		t.Fatal("NewRegistry accepted a duplicate kind")
	}
}

func TestNewRegistryRejectsEmptyKind(t *testing.T) {
	t.Parallel()

	if _, err := NewRegistry(stubHandler{kind: "", key: "k"}); err == nil {
		t.Fatal("NewRegistry accepted an empty kind")
	}
}

func TestResolveDerivesKeyAndMode(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(stubHandler{kind: "grant", mode: ModeReconcile, key: "stack:a/user:x"})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}

	resolved, err := registry.Resolve(Request{
		Kind:         "grant",
		Payload:      json.RawMessage(`{"role":"owner"}`),
		ActorSubject: "user:x",
		TenantID:     "tenant_1",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.Key != "stack:a/user:x" {
		t.Fatalf("Key = %q, want stack:a/user:x", resolved.Key)
	}
	if resolved.Mode != ModeReconcile {
		t.Fatalf("Mode = %v, want ModeReconcile", resolved.Mode)
	}
	if resolved.TenantID != "tenant_1" {
		t.Fatalf("TenantID = %q, want tenant_1", resolved.TenantID)
	}
}

func TestResolveUnknownKind(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(stubHandler{kind: "grant", key: "k"})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	if _, err := registry.Resolve(Request{Kind: "nope"}); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("Resolve error = %v, want ErrUnknownKind", err)
	}
}

func TestResolveRejectsEmptyKey(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(stubHandler{kind: "grant", key: ""})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}
	if _, err := registry.Resolve(Request{Kind: "grant"}); err == nil {
		t.Fatal("Resolve accepted an empty derived key")
	}
}
