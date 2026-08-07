package queue

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrUnknownKind reports a Kind with no registered Spec.
var ErrUnknownKind = errors.New("queue: unknown kind")

// Resolved is a Request with the spec-derived fields filled in. It maps
// one-to-one onto the columns of a work_queue row.
type Resolved struct {
	Kind         Kind
	Key          string
	Mode         Mode
	Payload      json.RawMessage
	ActorSubject string
	TenantID     string
}

// SpecRegistry maps a Kind to its Spec. A producer-only process registers
// specs alone: enqueueing needs the key and the mode, never a Handler, so the
// API binary does not construct handlers with nil dependencies just to reach
// their key derivation.
type SpecRegistry struct {
	specs map[Kind]Spec
}

// NewSpecRegistry indexes specs by kind and rejects duplicates at construction
// so a misconfigured binary fails at startup rather than at enqueue time.
func NewSpecRegistry(specs ...Spec) (*SpecRegistry, error) {
	indexed := make(map[Kind]Spec, len(specs))
	for _, spec := range specs {
		if spec.Kind == "" {
			return nil, fmt.Errorf("queue: spec has an empty kind")
		}
		if spec.Key == nil {
			return nil, fmt.Errorf("queue: spec for kind %q has no key derivation", spec.Kind)
		}
		if _, duplicate := indexed[spec.Kind]; duplicate {
			return nil, fmt.Errorf("queue: duplicate spec for kind %q", spec.Kind)
		}
		indexed[spec.Kind] = spec
	}
	return &SpecRegistry{specs: indexed}, nil
}

// Spec returns the spec registered for kind.
func (registry *SpecRegistry) Spec(kind Kind) (Spec, bool) {
	spec, ok := registry.specs[kind]
	return spec, ok
}

// Resolve derives the ordering key and mode for a request.
func (registry *SpecRegistry) Resolve(request Request) (Resolved, error) {
	spec, ok := registry.specs[request.Kind]
	if !ok {
		return Resolved{}, fmt.Errorf("%w: %q", ErrUnknownKind, request.Kind)
	}
	key, err := spec.Key(request.Payload)
	if err != nil {
		return Resolved{}, fmt.Errorf("derive %s key: %w", request.Kind, err)
	}
	if key == "" {
		return Resolved{}, fmt.Errorf("queue: spec for kind %q derived an empty key", request.Kind)
	}
	return Resolved{
		Kind:         request.Kind,
		Key:          key,
		Mode:         spec.Mode,
		Payload:      request.Payload,
		ActorSubject: request.ActorSubject,
		TenantID:     request.TenantID,
	}, nil
}

// Registry maps a Kind to its Handler. The worker needs this one; producers
// need only the specs it is built from.
type Registry struct {
	handlers map[Kind]Handler
	specs    *SpecRegistry
}

// NewRegistry indexes handlers by kind and rejects duplicates at construction
// so a misconfigured binary fails at startup rather than at delivery time.
func NewRegistry(handlers ...Handler) (*Registry, error) {
	indexed := make(map[Kind]Handler, len(handlers))
	specs := make([]Spec, 0, len(handlers))
	for _, handler := range handlers {
		spec := handler.Spec()
		if _, duplicate := indexed[spec.Kind]; duplicate {
			return nil, fmt.Errorf("queue: duplicate handler for kind %q", spec.Kind)
		}
		specs = append(specs, spec)
		indexed[spec.Kind] = handler
	}
	specRegistry, err := NewSpecRegistry(specs...)
	if err != nil {
		return nil, err
	}
	return &Registry{handlers: indexed, specs: specRegistry}, nil
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

// Specs exposes the spec half of this registry for enqueueing.
func (registry *Registry) Specs() *SpecRegistry { return registry.specs }

// Resolve derives the ordering key and mode for a request.
func (registry *Registry) Resolve(request Request) (Resolved, error) {
	return registry.specs.Resolve(request)
}
