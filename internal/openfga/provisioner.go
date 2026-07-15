package openfga

import (
	"context"
	"fmt"
)

type Backend interface {
	ListStores(context.Context) ([]Store, error)
	CreateStore(context.Context, string) (Store, error)
	GetStore(context.Context, string) (Store, error)
	ListAuthorizationModels(context.Context, string) ([]ModelRecord, error)
	GetAuthorizationModel(context.Context, string, string) (AuthorizationModel, error)
	WriteAuthorizationModel(context.Context, string, AuthorizationModel) (ModelRecord, error)
}

type Result struct {
	StoreID string
	ModelID string
}

func Bootstrap(ctx context.Context, cfg Config, desired AuthorizationModel, backend Backend) (Result, error) {
	stores, err := backend.ListStores(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("list stores: %w", err)
	}
	var matches []Store
	for _, store := range stores {
		if store.Name == cfg.StoreName {
			matches = append(matches, store)
		}
	}
	if len(matches) > 1 {
		return Result{}, fmt.Errorf("found %d stores named %q; bootstrap requires one unique store", len(matches), cfg.StoreName)
	}

	var store Store
	if len(matches) == 0 {
		store, err = backend.CreateStore(ctx, cfg.StoreName)
		if err != nil {
			return Result{}, fmt.Errorf("create store %q: %w", cfg.StoreName, err)
		}
	} else {
		store = matches[0]
	}

	records, err := backend.ListAuthorizationModels(ctx, store.ID)
	if err != nil {
		return Result{}, fmt.Errorf("list authorization models for store %q: %w", store.ID, err)
	}
	var modelMatches []ModelRecord
	for _, record := range records {
		equal, compareErr := ModelsEqual(desired, record.Model)
		if compareErr != nil {
			return Result{}, fmt.Errorf("compare authorization model %q: %w", record.ID, compareErr)
		}
		if equal {
			modelMatches = append(modelMatches, record)
		}
	}
	if len(modelMatches) > 1 {
		return Result{}, fmt.Errorf("found %d matching authorization models in store %q; selection is ambiguous", len(modelMatches), store.ID)
	}
	if len(modelMatches) == 1 {
		return Result{StoreID: store.ID, ModelID: modelMatches[0].ID}, nil
	}

	written, err := backend.WriteAuthorizationModel(ctx, store.ID, desired)
	if err != nil {
		return Result{}, fmt.Errorf("write authorization model in store %q: %w", store.ID, err)
	}
	return Result{StoreID: store.ID, ModelID: written.ID}, nil
}

func Verify(ctx context.Context, cfg Config, desired AuthorizationModel, backend Backend) (Result, error) {
	if err := cfg.ValidateVerify(); err != nil {
		return Result{}, err
	}
	if _, err := backend.GetStore(ctx, cfg.StoreID); err != nil {
		return Result{}, fmt.Errorf("verify configured store %q: %w", cfg.StoreID, err)
	}
	actual, err := backend.GetAuthorizationModel(ctx, cfg.StoreID, cfg.ModelID)
	if err != nil {
		return Result{}, fmt.Errorf("verify configured model %q in store %q: %w", cfg.ModelID, cfg.StoreID, err)
	}
	equal, err := ModelsEqual(desired, actual)
	if err != nil {
		return Result{}, fmt.Errorf("compare configured model %q: %w", cfg.ModelID, err)
	}
	if !equal {
		return Result{}, fmt.Errorf("configured OpenFGA model %q does not match the repository model", cfg.ModelID)
	}
	return Result{StoreID: cfg.StoreID, ModelID: cfg.ModelID}, nil
}
