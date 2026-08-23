// Package openfgamodel embeds the OpenFGA authorization model that
// cmd/openfga-provisioner writes.
//
// authorization-model.fga is the source of truth and the only form of the model
// in this repository. It is transformed to OpenFGA's API wire format in process,
// so there is no generated JSON artifact to regenerate or keep in step.
package openfgamodel

import (
	_ "embed"
	"fmt"
	"sync"

	"github.com/openfga/language/pkg/go/transformer"
)

//go:embed authorization-model.fga
var authorizationModelDSL string

// The DSL is embedded, so the transform is deterministic and worth doing once.
var transformModel = sync.OnceValues(func() (string, error) {
	encoded, err := transformer.TransformDSLToJSON(authorizationModelDSL)
	if err != nil {
		return "", fmt.Errorf("transform authorization model: %w", err)
	}
	return encoded, nil
})

// AuthorizationModelJSON returns the canonical model in OpenFGA's API wire
// format. The returned slice is a fresh copy the caller may retain.
func AuthorizationModelJSON() ([]byte, error) {
	encoded, err := transformModel()
	if err != nil {
		return nil, err
	}
	return []byte(encoded), nil
}
