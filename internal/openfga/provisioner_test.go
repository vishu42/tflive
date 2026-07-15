package openfga

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	openfgamodel "github.com/vishu42/tflive/openfga"
)

func TestBootstrapIsRepeatableAndReturnsStableIDs(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	backend := &fakeBackend{}
	cfg := Config{StoreName: "tflive"}

	first, err := Bootstrap(context.Background(), cfg, desired, backend)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Bootstrap(context.Background(), cfg, desired, backend)
	if err != nil {
		t.Fatal(err)
	}
	if first.StoreID != "store-1" || first.ModelID != "model-1" || first != second {
		t.Fatalf("first = %#v second = %#v", first, second)
	}
	if backend.createStoreCalls != 1 || backend.writeModelCalls != 1 {
		t.Fatalf("createStoreCalls = %d writeModelCalls = %d", backend.createStoreCalls, backend.writeModelCalls)
	}
}

func TestBootstrapRejectsAmbiguousStoresAndModels(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	tests := []struct {
		name    string
		backend *fakeBackend
		want    string
	}{
		{
			name: "duplicate store names",
			backend: &fakeBackend{stores: []Store{
				{ID: "store-1", Name: "tflive"},
				{ID: "store-2", Name: "tflive"},
			}},
			want: "found 2 stores named",
		},
		{
			name: "duplicate semantic models",
			backend: &fakeBackend{
				stores: []Store{{ID: "store-1", Name: "tflive"}},
				models: map[string][]ModelRecord{"store-1": {
					{ID: "model-1", Model: withID(desired, "model-1")},
					{ID: "model-2", Model: withID(desired, "model-2")},
				}},
			},
			want: "found 2 matching authorization models",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, test.backend)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Bootstrap() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestBootstrapRecoversAfterStoreCreationFailure(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	backend := &fakeBackend{writeErr: errors.New("temporary write failure")}
	_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
	if !errors.Is(err, backend.writeErr) {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	backend.writeErr = nil
	result, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
	if err != nil {
		t.Fatal(err)
	}
	if result.StoreID != "store-1" || backend.createStoreCalls != 1 {
		t.Fatalf("result = %#v createStoreCalls = %d", result, backend.createStoreCalls)
	}
}

func TestVerifyUsesExactIDsAndNeverMutates(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	backend := &fakeBackend{
		stores: []Store{{ID: "configured-store", Name: "renamed-store"}},
		models: map[string][]ModelRecord{
			"configured-store": {{ID: "configured-model", Model: withID(desired, "configured-model")}},
		},
	}
	cfg := Config{StoreID: "configured-store", ModelID: "configured-model"}
	result, err := Verify(context.Background(), cfg, desired, backend)
	if err != nil {
		t.Fatal(err)
	}
	if result.StoreID != cfg.StoreID || result.ModelID != cfg.ModelID {
		t.Fatalf("result = %#v", result)
	}
	if backend.createStoreCalls != 0 || backend.writeModelCalls != 0 || backend.listStoreCalls != 0 || backend.listModelCalls != 0 {
		t.Fatalf("verify mutated or listed: %#v", backend)
	}
}

func TestBootstrapReusesStoreAndCreatesOneVersionForChangedModel(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	old := desiredModel(t)
	for index := range old.TypeDefinitions {
		if old.TypeDefinitions[index].Type == "stack" {
			old.TypeDefinitions[index].Relations["can_manage_access"] = []byte(`{"computedUserset":{"relation":"viewer"}}`)
		}
	}
	backend := &fakeBackend{
		stores: []Store{{ID: "store-existing", Name: "tflive"}},
		models: map[string][]ModelRecord{
			"store-existing": {{ID: "old-model", Model: withID(old, "old-model")}},
		},
	}

	first, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || backend.createStoreCalls != 0 || backend.writeModelCalls != 1 {
		t.Fatalf("first = %#v second = %#v backend = %#v", first, second, backend)
	}
}

func TestVerifyRejectsMissingIDsAndSemanticMismatch(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	if _, err := Verify(context.Background(), Config{}, desired, &fakeBackend{}); err == nil || !strings.Contains(err.Error(), "OPENFGA_STORE_ID") {
		t.Fatalf("Verify() missing-ID error = %v", err)
	}

	actual := desiredModel(t)
	for index := range actual.TypeDefinitions {
		if actual.TypeDefinitions[index].Type == "stack" {
			actual.TypeDefinitions[index].Relations["can_manage_access"] = []byte(`{"computedUserset":{"relation":"viewer"}}`)
		}
	}
	backend := &fakeBackend{
		stores: []Store{{ID: "store-id", Name: "tflive"}},
		models: map[string][]ModelRecord{
			"store-id": {{ID: "model-id", Model: withID(actual, "model-id")}},
		},
	}
	_, err := Verify(context.Background(), Config{StoreID: "store-id", ModelID: "model-id"}, desired, backend)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Verify() mismatch error = %v", err)
	}
}

func TestProvisionerWrapsEveryBackendFailure(t *testing.T) {
	t.Parallel()

	desired := desiredModel(t)
	want := errors.New("backend unavailable")
	tests := []struct {
		name      string
		operation func(*fakeBackend) error
	}{
		{
			name: "list stores",
			operation: func(backend *fakeBackend) error {
				backend.listStoreErr = want
				_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
				return err
			},
		},
		{
			name: "create store",
			operation: func(backend *fakeBackend) error {
				backend.createStoreErr = want
				_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
				return err
			},
		},
		{
			name: "list models",
			operation: func(backend *fakeBackend) error {
				backend.stores = []Store{{ID: "store-id", Name: "tflive"}}
				backend.listModelErr = want
				_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
				return err
			},
		},
		{
			name: "write model",
			operation: func(backend *fakeBackend) error {
				backend.stores = []Store{{ID: "store-id", Name: "tflive"}}
				backend.writeErr = want
				_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desired, backend)
				return err
			},
		},
		{
			name: "get store",
			operation: func(backend *fakeBackend) error {
				backend.getStoreErr = want
				_, err := Verify(context.Background(), Config{StoreID: "store-id", ModelID: "model-id"}, desired, backend)
				return err
			},
		},
		{
			name: "get model",
			operation: func(backend *fakeBackend) error {
				backend.stores = []Store{{ID: "store-id", Name: "tflive"}}
				backend.getModelErr = want
				_, err := Verify(context.Background(), Config{StoreID: "store-id", ModelID: "model-id"}, desired, backend)
				return err
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.operation(&fakeBackend{}); !errors.Is(err, want) {
				t.Fatalf("operation error = %v", err)
			}
		})
	}
}

func TestBootstrapPreservesCancellation(t *testing.T) {
	t.Parallel()

	backend := &fakeBackend{listStoreErr: context.Canceled}
	_, err := Bootstrap(context.Background(), Config{StoreName: "tflive"}, desiredModel(t), backend)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Bootstrap() error = %v", err)
	}
}

func desiredModel(t *testing.T) AuthorizationModel {
	t.Helper()
	model, err := ParseAuthorizationModel(openfgamodel.AuthorizationModelJSON())
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func withID(model AuthorizationModel, id string) AuthorizationModel {
	model.ID = id
	return model
}

type fakeBackend struct {
	stores           []Store
	models           map[string][]ModelRecord
	createStoreCalls int
	writeModelCalls  int
	listStoreCalls   int
	listModelCalls   int
	listStoreErr     error
	createStoreErr   error
	getStoreErr      error
	listModelErr     error
	getModelErr      error
	writeErr         error
}

func (backend *fakeBackend) ListStores(context.Context) ([]Store, error) {
	backend.listStoreCalls++
	if backend.listStoreErr != nil {
		return nil, backend.listStoreErr
	}
	return append([]Store(nil), backend.stores...), nil
}

func (backend *fakeBackend) CreateStore(_ context.Context, name string) (Store, error) {
	backend.createStoreCalls++
	if backend.createStoreErr != nil {
		return Store{}, backend.createStoreErr
	}
	store := Store{ID: fmt.Sprintf("store-%d", backend.createStoreCalls), Name: name}
	backend.stores = append(backend.stores, store)
	return store, nil
}

func (backend *fakeBackend) GetStore(_ context.Context, id string) (Store, error) {
	if backend.getStoreErr != nil {
		return Store{}, backend.getStoreErr
	}
	for _, store := range backend.stores {
		if store.ID == id {
			return store, nil
		}
	}
	return Store{}, fmt.Errorf("store %s not found", id)
}

func (backend *fakeBackend) ListAuthorizationModels(_ context.Context, storeID string) ([]ModelRecord, error) {
	backend.listModelCalls++
	if backend.listModelErr != nil {
		return nil, backend.listModelErr
	}
	return append([]ModelRecord(nil), backend.models[storeID]...), nil
}

func (backend *fakeBackend) GetAuthorizationModel(_ context.Context, storeID, modelID string) (AuthorizationModel, error) {
	if backend.getModelErr != nil {
		return AuthorizationModel{}, backend.getModelErr
	}
	for _, record := range backend.models[storeID] {
		if record.ID == modelID {
			return record.Model, nil
		}
	}
	return AuthorizationModel{}, fmt.Errorf("model %s not found", modelID)
}

func (backend *fakeBackend) WriteAuthorizationModel(_ context.Context, storeID string, model AuthorizationModel) (ModelRecord, error) {
	backend.writeModelCalls++
	if backend.writeErr != nil {
		return ModelRecord{}, backend.writeErr
	}
	record := ModelRecord{ID: fmt.Sprintf("model-%d", backend.writeModelCalls), Model: withID(model, fmt.Sprintf("model-%d", backend.writeModelCalls))}
	if backend.models == nil {
		backend.models = make(map[string][]ModelRecord)
	}
	backend.models[storeID] = append(backend.models[storeID], record)
	return record, nil
}
