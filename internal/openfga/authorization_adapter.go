package openfga

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/vishu42/tflive/internal/authz"
)

// AuthorizationAdapter translates the provider-neutral authorization port to
// OpenFGA's check endpoints.
type AuthorizationAdapter struct {
	client  *Client
	storeID string
	modelID string
}

// NewAuthorizationAdapter returns an OpenFGA adapter configured to check the
// exact verified store and authorization model.
func NewAuthorizationAdapter(cfg Config) (*AuthorizationAdapter, error) {
	if err := cfg.ValidateVerify(); err != nil {
		return nil, fmt.Errorf("validate OpenFGA authorization config: %w", err)
	}
	return &AuthorizationAdapter{client: NewClient(cfg), storeID: cfg.StoreID, modelID: cfg.ModelID}, nil
}

// Check evaluates one derived permission against the configured OpenFGA model.
func (adapter *AuthorizationAdapter) Check(ctx context.Context, request authz.CheckRequest) (authz.CheckResult, error) {
	if !request.Valid() {
		return authz.CheckResult{}, fmt.Errorf("%w: invalid authorization check", authz.ErrInvalidInput)
	}

	var response struct {
		Allowed *bool `json:"allowed"`
	}
	err := adapter.client.doJSON(ctx, http.MethodPost, adapter.client.endpoint("stores", adapter.storeID, "check"), nil, checkInput(adapter.modelID, request), &response, http.StatusOK)
	if err != nil {
		return authz.CheckResult{}, adapter.classify(err)
	}
	if response.Allowed == nil {
		return authz.CheckResult{}, fmt.Errorf("%w: check allowed is missing", authz.ErrMalformedResponse)
	}
	return authz.CheckResult{Allowed: *response.Allowed}, nil
}

// BatchCheck evaluates independently correlated permission checks in one
// OpenFGA request and preserves the caller's input ordering.
func (adapter *AuthorizationAdapter) BatchCheck(ctx context.Context, request authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	if !request.Valid() {
		return authz.BatchCheckResult{}, fmt.Errorf("%w: invalid authorization batch check", authz.ErrInvalidInput)
	}

	type batchCheck struct {
		TupleKey      tupleKey `json:"tuple_key"`
		CorrelationID string   `json:"correlation_id"`
	}
	input := struct {
		AuthorizationModelID string       `json:"authorization_model_id"`
		Checks               []batchCheck `json:"checks"`
	}{AuthorizationModelID: adapter.modelID, Checks: make([]batchCheck, len(request.Checks))}
	for index, check := range request.Checks {
		input.Checks[index] = batchCheck{TupleKey: tuple(check), CorrelationID: strconv.Itoa(index)}
	}

	var response struct {
		Result map[string]struct {
			Allowed *bool `json:"allowed"`
		} `json:"result"`
	}
	err := adapter.client.doJSON(ctx, http.MethodPost, adapter.client.endpoint("stores", adapter.storeID, "batch-check"), nil, input, &response, http.StatusOK)
	if err != nil {
		return authz.BatchCheckResult{}, adapter.classify(err)
	}
	if len(response.Result) != len(request.Checks) {
		return authz.BatchCheckResult{}, fmt.Errorf("%w: batch check correlation results do not match requests", authz.ErrMalformedResponse)
	}

	result := authz.BatchCheckResult{Results: make([]authz.CheckResult, len(request.Checks))}
	for index := range request.Checks {
		correlationID := strconv.Itoa(index)
		check, ok := response.Result[correlationID]
		if !ok || check.Allowed == nil {
			return authz.BatchCheckResult{}, fmt.Errorf("%w: batch check result %q is missing or invalid", authz.ErrMalformedResponse, correlationID)
		}
		result.Results[index] = authz.CheckResult{Allowed: *check.Allowed}
	}
	return result, nil
}

type tupleKey struct {
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

func tuple(request authz.CheckRequest) tupleKey {
	return tupleKey{User: request.Subject.String(), Relation: string(request.Permission), Object: request.Stack.String()}
}

func checkInput(modelID string, request authz.CheckRequest) any {
	return struct {
		AuthorizationModelID string   `json:"authorization_model_id"`
		TupleKey             tupleKey `json:"tuple_key"`
	}{AuthorizationModelID: modelID, TupleKey: tuple(request)}
}

func (adapter *AuthorizationAdapter) classify(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", authz.ErrTimeout, err)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("authorization check canceled: %w", err)
	case errors.Is(err, errMalformedHTTPResponse):
		return fmt.Errorf("%w: %v", authz.ErrMalformedResponse, err)
	}

	var statusError *HTTPStatusError
	if errors.As(err, &statusError) && (statusError.StatusCode == http.StatusTooManyRequests || statusError.StatusCode >= http.StatusInternalServerError) {
		return fmt.Errorf("%w: %w", authz.ErrUnavailable, err)
	}
	return err
}
