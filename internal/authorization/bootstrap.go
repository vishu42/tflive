package authorization

import (
	"context"
	"fmt"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/vishu42/tflive/internal/strval"
)

// bootstrapPageSize is how many stores or models one reconcile page asks for.
const bootstrapPageSize = 100

// safeOpaqueIdentifier reports whether a server-minted id is usable: non-empty
// and free of whitespace and control characters, so it cannot corrupt a later
// request that carries it.
//
// Bootstrap checks every id it adopts because an id it cannot trust is one it
// must not pin the process to.
func safeOpaqueIdentifier(value string) bool {
	return strval.SafeOpaque(value)
}

// bootstrap resolves the store and authorization model this process will use,
// creating either only when it is absent.
//
// The refusals here are the point, and each one exists because the alternative
// is to silently decide something nobody chose:
//
//   - two stores with the configured name is ambiguous, and picking one decides
//     which tuples count;
//   - two stored models matching the repository's is ambiguous the same way;
//   - a matching model is adopted rather than rewritten, so a restart does not
//     mint a new model id that existing tuples were never written against.
//
//	no store        → create one, write the model
//	store, no match → adopt the store, write a new model version
//	store + match   → adopt both, write nothing
//	two stores      → error
//	two matches     → error
func (auth *Authorization) bootstrap(ctx context.Context, storeName string) error {
	desired, err := desiredModel()
	if err != nil {
		return err
	}

	storeID, err := auth.resolveStore(ctx, storeName)
	if err != nil {
		return err
	}

	modelID, err := auth.resolveModel(ctx, storeID, desired)
	if err != nil {
		return err
	}

	auth.storeID, auth.modelID = storeID, modelID
	return nil
}

// desiredModel is the repository's model in the comparable form ModelsEqual
// wants. The DSL is the source of truth; this is only its parsed shape.
func desiredModel() (AuthorizationModel, error) {
	encoded, err := AuthorizationModelJSON()
	if err != nil {
		return AuthorizationModel{}, fmt.Errorf("authorization: load model: %w", err)
	}
	model, err := ParseAuthorizationModel(encoded)
	if err != nil {
		return AuthorizationModel{}, fmt.Errorf("authorization: parse model: %w", err)
	}
	return model, nil
}

// resolveStore finds the single store with this name, or creates it.
func (auth *Authorization) resolveStore(ctx context.Context, storeName string) (string, error) {
	var matches []string
	token := ""
	seen := map[string]bool{}
	for {
		response, err := auth.server.ListStores(ctx, &openfgav1.ListStoresRequest{
			PageSize:          wrapperspb.Int32(bootstrapPageSize),
			ContinuationToken: token,
		})
		if err != nil {
			return "", fmt.Errorf("authorization: list stores: %w", err)
		}
		for _, store := range response.GetStores() {
			if store.GetName() == storeName {
				if !safeOpaqueIdentifier(store.GetId()) {
					return "", fmt.Errorf("authorization: store %q has an unsafe id", storeName)
				}
				matches = append(matches, store.GetId())
			}
		}
		token = response.GetContinuationToken()
		if token == "" {
			break
		}
		// A provider that keeps handing back the same token would loop here
		// forever, so a repeat is treated as a broken response rather than
		// more pages.
		if seen[token] {
			return "", fmt.Errorf("authorization: list stores repeated a continuation token")
		}
		seen[token] = true
	}

	if len(matches) > 1 {
		return "", fmt.Errorf("authorization: found %d stores named %q; bootstrap requires one", len(matches), storeName)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}

	created, err := auth.server.CreateStore(ctx, &openfgav1.CreateStoreRequest{Name: storeName})
	if err != nil {
		return "", fmt.Errorf("authorization: create store %q: %w", storeName, err)
	}
	if !safeOpaqueIdentifier(created.GetId()) {
		return "", fmt.Errorf("authorization: created store %q has an unsafe id", storeName)
	}
	return created.GetId(), nil
}

// resolveModel adopts the stored model matching the repository's, or writes a
// new version when none does.
func (auth *Authorization) resolveModel(ctx context.Context, storeID string, desired AuthorizationModel) (string, error) {
	var matches []string
	token := ""
	seen := map[string]bool{}
	for {
		response, err := auth.server.ReadAuthorizationModels(ctx, &openfgav1.ReadAuthorizationModelsRequest{
			StoreId:           storeID,
			PageSize:          wrapperspb.Int32(bootstrapPageSize),
			ContinuationToken: token,
		})
		if err != nil {
			return "", fmt.Errorf("authorization: list models: %w", err)
		}
		for _, message := range response.GetAuthorizationModels() {
			stored, err := modelFromProto(message)
			if err != nil {
				return "", err
			}
			equal, err := ModelsEqual(desired, stored)
			if err != nil {
				return "", fmt.Errorf("authorization: compare model %q: %w", message.GetId(), err)
			}
			if equal {
				matches = append(matches, message.GetId())
			}
		}
		token = response.GetContinuationToken()
		if token == "" {
			break
		}
		if seen[token] {
			return "", fmt.Errorf("authorization: list models repeated a continuation token")
		}
		seen[token] = true
	}

	if len(matches) > 1 {
		return "", fmt.Errorf("authorization: found %d models matching the repository model in store %q; selection is ambiguous", len(matches), storeID)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}

	request, err := writeModelRequest(storeID, desired)
	if err != nil {
		return "", err
	}
	written, err := auth.server.WriteAuthorizationModel(ctx, request)
	if err != nil {
		return "", fmt.Errorf("authorization: write model in store %q: %w", storeID, err)
	}
	if !safeOpaqueIdentifier(written.GetAuthorizationModelId()) {
		return "", fmt.Errorf("authorization: written model has an unsafe id")
	}
	return written.GetAuthorizationModelId(), nil
}

// modelFromProto converts a stored model into the comparable shape.
//
// Both directions go through protojson because AuthorizationModel is defined by
// the same JSON wire format the protobuf marshals to, so there is one encoding
// to agree on rather than a hand-written field mapping to drift.
func modelFromProto(message *openfgav1.AuthorizationModel) (AuthorizationModel, error) {
	encoded, err := protojson.Marshal(message)
	if err != nil {
		return AuthorizationModel{}, fmt.Errorf("authorization: encode stored model: %w", err)
	}
	model, err := ParseAuthorizationModel(encoded)
	if err != nil {
		return AuthorizationModel{}, fmt.Errorf("authorization: parse stored model: %w", err)
	}
	return model, nil
}

// writeModelRequest renders the repository model as a write request. The id is
// cleared first: ids are the server's to mint, and sending one is meaningless.
func writeModelRequest(storeID string, model AuthorizationModel) (*openfgav1.WriteAuthorizationModelRequest, error) {
	model.ID = ""
	encoded, err := CanonicalJSON(model)
	if err != nil {
		return nil, fmt.Errorf("authorization: encode model: %w", err)
	}
	var message openfgav1.WriteAuthorizationModelRequest
	if err := protojson.Unmarshal(encoded, &message); err != nil {
		return nil, fmt.Errorf("authorization: decode model: %w", err)
	}
	message.StoreId = storeID
	return &message, nil
}
