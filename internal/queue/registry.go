package queue

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrUnknownKind reports a Kind with no registered Handler.
var ErrUnknownKind = errors.New("queue: unknown kind")

// Resolved is a Request with the handler-derived fields filled in. It maps
// one-to-one onto the columns of a work_queue row.
type Resolved struct {
	Kind         Kind
	Key          string
	Mode         Mode
	Payload      json.RawMessage
	ActorSubject string
	TenantID     string
}

// Registry maps a Kind to its Handler.
type Registry struct {
	handlers map[Kind]Handler
}

// NewRegistry indexes handlers by kind and rejects duplicates at construction
// so a misconfigured binary fails at startup rather than at delivery time.
func NewRegistry(handlers ...Handler) (*Registry, error) {
	indexed := make(map[Kind]Handler, len(handlers))
	for _, handler := range handlers {
		kind := handler.Kind()
		if kind == "" {
			return nil, fmt.Errorf("queue: handler has an empty kind")
		}
		if _, duplicate := indexed[kind]; duplicate {
			return nil, fmt.Errorf("queue: duplicate handler for kind %q", kind)
		}
		indexed[kind] = handler
	}
	return &Registry{handlers: indexed}, nil
}

// Handler returns the handler registered for kind.
func (registry *Registry) Handler(kind Kind) (Handler, bool) {
	handler, ok := registry.handlers[kind]
	return handler, ok
}

// Kinds returns every registered kind. Order is unspecified.
func (registry *Registry) Kinds() []Kind {
	kinds := make([]Kind, 0, len(registry.handlers))
	for kind := range registry.handlers {
		kinds = append(kinds, kind)
	}
	return kinds
}

// Resolve derives the ordering key and mode for a request.
func (registry *Registry) Resolve(request Request) (Resolved, error) {
	handler, ok := registry.handlers[request.Kind]
	if !ok {
		return Resolved{}, fmt.Errorf("%w: %q", ErrUnknownKind, request.Kind)
	}
	key, err := handler.Key(request.Payload)
	if err != nil {
		return Resolved{}, fmt.Errorf("derive %s key: %w", request.Kind, err)
	}
	if key == "" {
		return Resolved{}, fmt.Errorf("queue: handler for kind %q derived an empty key", request.Kind)
	}
	return Resolved{
		Kind:         request.Kind,
		Key:          key,
		Mode:         handler.Mode(),
		Payload:      request.Payload,
		ActorSubject: request.ActorSubject,
		TenantID:     request.TenantID,
	}, nil
}
