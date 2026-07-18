package openfga

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/vishu42/tflive/internal/authz"
)

// AuthorizationAdapter translates the provider-neutral authorization port to
// OpenFGA's check endpoints.
type AuthorizationAdapter struct {
	client  *Client
	storeID string
	modelID string
}

const maxConfirmationChecks = 25

// NewAuthorizationAdapter returns an OpenFGA adapter configured to check the
// exact verified store and authorization model.
func NewAuthorizationAdapter(cfg Config) (*AuthorizationAdapter, error) {
	if cfg.APIURL == nil {
		return nil, fmt.Errorf("OpenFGA API URL is required")
	}
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

// ListAccessibleStacks returns validated stacks for which the subject has the
// requested derived permission in the configured authorization model.
func (adapter *AuthorizationAdapter) ListAccessibleStacks(ctx context.Context, request authz.ListAccessibleStacksRequest) (authz.ListAccessibleStacksResult, error) {
	if !request.Valid() {
		return authz.ListAccessibleStacksResult{}, fmt.Errorf("%w: invalid accessible stacks request", authz.ErrInvalidInput)
	}

	var response struct {
		Objects *[]string `json:"objects"`
	}
	input := struct {
		AuthorizationModelID string `json:"authorization_model_id"`
		Type                 string `json:"type"`
		Relation             string `json:"relation"`
		User                 string `json:"user"`
	}{
		AuthorizationModelID: adapter.modelID,
		Type:                 "stack",
		Relation:             string(request.Permission),
		User:                 request.Subject.String(),
	}
	if err := adapter.client.doJSON(ctx, http.MethodPost, adapter.client.endpoint("stores", adapter.storeID, "list-objects"), nil, input, &response, http.StatusOK); err != nil {
		return authz.ListAccessibleStacksResult{}, adapter.classify(err)
	}
	if response.Objects == nil {
		return authz.ListAccessibleStacksResult{}, fmt.Errorf("%w: list objects response is missing objects", authz.ErrMalformedResponse)
	}

	result := authz.ListAccessibleStacksResult{Stacks: make([]authz.Stack, 0, len(*response.Objects))}
	seen := make(map[string]struct{}, len(*response.Objects))
	for _, object := range *response.Objects {
		stack, err := stackFromCanonicalObject(object)
		if err != nil {
			return authz.ListAccessibleStacksResult{}, fmt.Errorf("%w: list objects contains invalid stack %q", authz.ErrMalformedResponse, object)
		}
		if _, duplicate := seen[stack.String()]; duplicate {
			return authz.ListAccessibleStacksResult{}, fmt.Errorf("%w: list objects contains duplicate stack %q", authz.ErrMalformedResponse, object)
		}
		seen[stack.String()] = struct{}{}
		result.Stacks = append(result.Stacks, stack)
	}
	return result, nil
}

// ListGrants returns all direct role assignments for the requested stack.
func (adapter *AuthorizationAdapter) ListGrants(ctx context.Context, request authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	if !request.Valid() {
		return authz.ListGrantsResult{}, fmt.Errorf("%w: invalid grants request", authz.ErrInvalidInput)
	}

	type readTuple struct {
		Key *tupleKey `json:"key"`
	}
	type readResponse struct {
		Tuples            *[]readTuple `json:"tuples"`
		ContinuationToken string       `json:"continuation_token"`
	}

	result := authz.ListGrantsResult{}
	seenGrants := map[struct{ subject, role string }]struct{}{}
	seenTokens := map[string]struct{}{}
	continuationToken := ""
	for {
		input := struct {
			TupleKey struct {
				Object string `json:"object"`
			} `json:"tuple_key"`
			PageSize          int    `json:"page_size"`
			ContinuationToken string `json:"continuation_token,omitempty"`
		}{PageSize: 100, ContinuationToken: continuationToken}
		input.TupleKey.Object = request.Stack.String()

		var response readResponse
		if err := adapter.client.doJSON(ctx, http.MethodPost, adapter.client.endpoint("stores", adapter.storeID, "read"), nil, input, &response, http.StatusOK); err != nil {
			return authz.ListGrantsResult{}, adapter.classify(err)
		}
		if response.Tuples == nil {
			return authz.ListGrantsResult{}, fmt.Errorf("%w: read response is missing tuples", authz.ErrMalformedResponse)
		}
		for _, tuple := range *response.Tuples {
			grant, err := grantFromReadTuple(tuple.Key, request.Stack)
			if err != nil {
				return authz.ListGrantsResult{}, fmt.Errorf("%w: read response contains invalid tuple", authz.ErrMalformedResponse)
			}
			key := struct{ subject, role string }{subject: grant.Subject().String(), role: grant.Role().String()}
			if _, duplicate := seenGrants[key]; duplicate {
				return authz.ListGrantsResult{}, fmt.Errorf("%w: read response contains duplicate grant", authz.ErrMalformedResponse)
			}
			seenGrants[key] = struct{}{}
			result.Grants = append(result.Grants, grant)
		}
		if response.ContinuationToken == "" {
			break
		}
		if !safeOpaqueIdentifier(response.ContinuationToken) {
			return authz.ListGrantsResult{}, fmt.Errorf("%w: read response continuation token is invalid", authz.ErrMalformedResponse)
		}
		if _, repeated := seenTokens[response.ContinuationToken]; repeated {
			return authz.ListGrantsResult{}, fmt.Errorf("%w: read response repeats continuation token", authz.ErrMalformedResponse)
		}
		seenTokens[response.ContinuationToken] = struct{}{}
		continuationToken = response.ContinuationToken
	}

	sort.Slice(result.Grants, func(i, j int) bool {
		if result.Grants[i].Subject().String() != result.Grants[j].Subject().String() {
			return result.Grants[i].Subject().String() < result.Grants[j].Subject().String()
		}
		return result.Grants[i].Role().String() < result.Grants[j].Role().String()
	})
	return result, nil
}

// WriteRelationships grants direct roles. If OpenFGA reports a conflicting
// write, it confirms the desired state before deciding whether the operation
// can safely be treated as an idempotent success.
func (adapter *AuthorizationAdapter) WriteRelationships(ctx context.Context, mutation authz.Mutation) error {
	grants, err := validMutation(mutation)
	if err != nil {
		return err
	}
	if err := adapter.write(ctx, grants, nil); err != nil {
		matches, confirmErr := adapter.confirm(ctx, grants, true)
		if confirmErr != nil {
			return confirmErr
		}
		if !matches {
			return fmt.Errorf("%w: grants not visible after rejected write", authz.ErrWriteUnconfirmed)
		}
	}
	if !mutation.Confirm() {
		return nil
	}
	matches, err := adapter.confirm(ctx, grants, true)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("%w: grants not visible", authz.ErrWriteUnconfirmed)
	}
	return nil
}

// DeleteRelationships revokes direct roles. If OpenFGA reports a conflicting
// delete, it confirms the desired absence before reporting an error.
func (adapter *AuthorizationAdapter) DeleteRelationships(ctx context.Context, mutation authz.Mutation) error {
	grants, err := validMutation(mutation)
	if err != nil {
		return err
	}
	if err := adapter.write(ctx, nil, grants); err != nil {
		matches, confirmErr := adapter.confirm(ctx, grants, false)
		if confirmErr != nil {
			return confirmErr
		}
		if !matches {
			return fmt.Errorf("%w: grants still visible after rejected delete", authz.ErrWriteUnconfirmed)
		}
	}
	if !mutation.Confirm() {
		return nil
	}
	matches, err := adapter.confirm(ctx, grants, false)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("%w: grants still visible", authz.ErrWriteUnconfirmed)
	}
	return nil
}

func validMutation(mutation authz.Mutation) ([]authz.Grant, error) {
	if !mutation.Valid() {
		return nil, fmt.Errorf("%w: invalid relationship mutation", authz.ErrInvalidInput)
	}
	grants := mutation.Grants()
	seen := make(map[tupleKey]struct{}, len(grants))
	for _, grant := range grants {
		if !grant.Valid() {
			return nil, fmt.Errorf("%w: invalid relationship mutation grant", authz.ErrInvalidInput)
		}
		key := tupleKey{User: grant.Subject().String(), Relation: grant.Role().String(), Object: grant.Stack().String()}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate relationship mutation grant", authz.ErrInvalidInput)
		}
		seen[key] = struct{}{}
	}
	return grants, nil
}

func (adapter *AuthorizationAdapter) write(ctx context.Context, writes, deletes []authz.Grant) error {
	type tupleKeys struct {
		TupleKeys []tupleKey `json:"tuple_keys"`
	}
	input := struct {
		AuthorizationModelID string     `json:"authorization_model_id"`
		Writes               *tupleKeys `json:"writes,omitempty"`
		Deletes              *tupleKeys `json:"deletes,omitempty"`
	}{AuthorizationModelID: adapter.modelID}
	if len(writes) > 0 && len(deletes) == 0 {
		input.Writes = &tupleKeys{TupleKeys: tuples(writes)}
	} else if len(deletes) > 0 && len(writes) == 0 {
		input.Deletes = &tupleKeys{TupleKeys: tuples(deletes)}
	} else {
		return fmt.Errorf("%w: relationship write must contain exactly one mutation direction", authz.ErrInvalidInput)
	}
	return adapter.client.doJSON(ctx, http.MethodPost, adapter.client.endpoint("stores", adapter.storeID, "write"), nil, input, nil, http.StatusOK)
}

func (adapter *AuthorizationAdapter) confirm(ctx context.Context, grants []authz.Grant, expected bool) (bool, error) {
	type batchCheck struct {
		TupleKey      tupleKey `json:"tuple_key"`
		CorrelationID string   `json:"correlation_id"`
	}
	for start := 0; start < len(grants); start += maxConfirmationChecks {
		end := start + maxConfirmationChecks
		if end > len(grants) {
			end = len(grants)
		}
		input := struct {
			AuthorizationModelID string       `json:"authorization_model_id"`
			Checks               []batchCheck `json:"checks"`
			Consistency          string       `json:"consistency"`
		}{
			AuthorizationModelID: adapter.modelID,
			Checks:               make([]batchCheck, end-start),
			Consistency:          "HIGHER_CONSISTENCY",
		}
		for index, grant := range grants[start:end] {
			input.Checks[index] = batchCheck{TupleKey: tupleForGrant(grant), CorrelationID: strconv.Itoa(index)}
		}

		var response struct {
			Result map[string]struct {
				Allowed *bool `json:"allowed"`
			} `json:"result"`
		}
		err := adapter.client.doJSON(ctx, http.MethodPost, adapter.client.endpoint("stores", adapter.storeID, "batch-check"), nil, input, &response, http.StatusOK)
		if err != nil {
			return false, adapter.classify(err)
		}
		if len(response.Result) != len(input.Checks) {
			return false, fmt.Errorf("%w: confirmation results do not match grants", authz.ErrMalformedResponse)
		}
		for index := range input.Checks {
			result, ok := response.Result[strconv.Itoa(index)]
			if !ok || result.Allowed == nil {
				return false, fmt.Errorf("%w: confirmation result %q is missing or invalid", authz.ErrMalformedResponse, strconv.Itoa(index))
			}
			if *result.Allowed != expected {
				return false, nil
			}
		}
	}
	return true, nil
}

func tuples(grants []authz.Grant) []tupleKey {
	result := make([]tupleKey, len(grants))
	for index, grant := range grants {
		result[index] = tupleForGrant(grant)
	}
	return result
}

func tupleForGrant(grant authz.Grant) tupleKey {
	return tupleKey{User: grant.Subject().String(), Relation: grant.Role().String(), Object: grant.Stack().String()}
}

func stackFromCanonicalObject(object string) (authz.Stack, error) {
	const prefix = "stack:"
	if !strings.HasPrefix(object, prefix) {
		return authz.Stack{}, fmt.Errorf("missing stack prefix")
	}
	stack, err := authz.StackFromID(strings.TrimPrefix(object, prefix))
	if err != nil || stack.String() != object {
		return authz.Stack{}, fmt.Errorf("invalid stack")
	}
	return stack, nil
}

func grantFromReadTuple(key *tupleKey, requestedStack authz.Stack) (authz.Grant, error) {
	const subjectPrefix = "user:"
	if key == nil || key.Object != requestedStack.String() || !strings.HasPrefix(key.User, subjectPrefix) {
		return authz.Grant{}, fmt.Errorf("invalid tuple key")
	}
	subject, err := authz.SubjectFromKeycloakSub(strings.TrimPrefix(key.User, subjectPrefix))
	if err != nil || subject.String() != key.User {
		return authz.Grant{}, fmt.Errorf("invalid tuple subject")
	}
	role, err := authz.RoleFromDirectRelation(key.Relation)
	if err != nil {
		return authz.Grant{}, fmt.Errorf("invalid tuple relation")
	}
	return authz.NewGrant(subject, requestedStack, role)
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
	case errors.Is(err, errHTTPTransport), errors.Is(err, errHTTPBodyRead):
		return fmt.Errorf("%w: OpenFGA request failed", authz.ErrUnavailable)
	}

	var statusError *HTTPStatusError
	if errors.As(err, &statusError) && (statusError.StatusCode == http.StatusTooManyRequests || statusError.StatusCode >= http.StatusInternalServerError) {
		return fmt.Errorf("%w: OpenFGA returned a retryable response", authz.ErrUnavailable)
	}
	return err
}
