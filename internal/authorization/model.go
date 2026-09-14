// This file embeds the authorization model the server is bootstrapped with.
//
// authorization-model.fga is the source of truth and the only form of the model
// in this repository. It is transformed to OpenFGA's protobuf form in process,
// so there is no generated JSON artifact to regenerate or keep in step.
// authorization-model-tests.fga.yaml beside it is the model's own test matrix,
// run by the `fga model test` CLI rather than by `go test`.
package authorization

import (
	"context"
	_ "embed"
	"fmt"
	"sync"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/language/pkg/go/transformer"
	"github.com/openfga/openfga/pkg/typesystem"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

//go:embed authorization-model.fga
var authorizationModelDSL string

// The DSL is embedded, so the transform is deterministic and worth doing once.
//
// Validation is upstream's rather than ours: typesystem.NewAndValidate resolves
// every relation reference and rewrite against the rest of the model, which is
// the check that actually catches a broken model. A local schema-version and
// duplicate-type check only ever restated what the transformer already
// guarantees about its own output.
var authorizationModel = sync.OnceValues(func() (*openfgav1.AuthorizationModel, error) {
	model, err := transformer.TransformDSLToProto(authorizationModelDSL)
	if err != nil {
		return nil, fmt.Errorf("transform authorization model: %w", err)
	}
	if _, err := typesystem.NewAndValidate(context.Background(), model); err != nil {
		return nil, fmt.Errorf("validate authorization model: %w", err)
	}
	return model, nil
})

// desiredModel is the repository's model, as the server represents it.
//
// The returned message is a copy: callers hand it to request builders that
// clear the id, and the cached original must survive that.
func desiredModel() (*openfgav1.AuthorizationModel, error) {
	model, err := authorizationModel()
	if err != nil {
		return nil, fmt.Errorf("authorization: load model: %w", err)
	}
	return proto.Clone(model).(*openfgav1.AuthorizationModel), nil
}

// AuthorizationModelJSON returns the canonical model in OpenFGA's API wire
// format. The returned slice is a fresh copy the caller may retain.
func AuthorizationModelJSON() ([]byte, error) {
	model, err := authorizationModel()
	if err != nil {
		return nil, err
	}
	return protojson.Marshal(model)
}
