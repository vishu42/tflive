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

// ListGrants returns all direct role assignments for the requested object.
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
	seenGrants := map[struct{ subject, relation string }]struct{}{}
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
		input.TupleKey.Object = request.Object.String()

		var response readResponse
		if err := adapter.client.doJSON(ctx, http.MethodPost, adapter.client.endpoint("stores", adapter.storeID, "read"), nil, input, &response, http.StatusOK); err != nil {
			return authz.ListGrantsResult{}, adapter.classify(err)
		}
		if response.Tuples == nil {
			return authz.ListGrantsResult{}, fmt.Errorf("%w: read response is missing tuples", authz.ErrMalformedResponse)
		}
		for _, tuple := range *response.Tuples {
			grant, err := grantFromReadTuple(tuple.Key, request.Object)
			if err != nil {
				return authz.ListGrantsResult{}, fmt.Errorf("%w: read response contains invalid tuple", authz.ErrMalformedResponse)
			}
			key := struct{ subject, relation string }{subject: grant.Subject().String(), relation: grant.Relation().String()}
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
		return result.Grants[i].Relation().String() < result.Grants[j].Relation().String()
	})
	return result, nil
}

// ListSubjectGrants returns the direct roles one subject holds on one stack.
// Unlike ListGrants it filters the read by user as well as object, so a
// reconciling handler can compute a delta without paging every grant on the
// stack. A subject holds at most one tuple per role, so this never paginates.
func (adapter *AuthorizationAdapter) ListSubjectGrants(ctx context.Context, request authz.ListSubjectGrantsRequest) (authz.ListGrantsResult, error) {
	if !request.Valid() {
		return authz.ListGrantsResult{}, fmt.Errorf("%w: invalid subject grants request", authz.ErrInvalidInput)
	}

	type readTuple struct {
		Key *tupleKey `json:"key"`
	}
	var response struct {
		Tuples *[]readTuple `json:"tuples"`
	}
	input := struct {
		TupleKey struct {
			User   string `json:"user"`
			Object string `json:"object"`
		} `json:"tuple_key"`
		PageSize int `json:"page_size"`
	}{PageSize: 100}
	input.TupleKey.User = request.Subject.String()
	input.TupleKey.Object = request.Object.String()

	if err := adapter.client.doJSON(ctx, http.MethodPost, adapter.client.endpoint("stores", adapter.storeID, "read"), nil, input, &response, http.StatusOK); err != nil {
		return authz.ListGrantsResult{}, adapter.classify(err)
	}
	if response.Tuples == nil {
		return authz.ListGrantsResult{}, fmt.Errorf("%w: read response is missing tuples", authz.ErrMalformedResponse)
	}

	result := authz.ListGrantsResult{}
	for _, tuple := range *response.Tuples {
		grant, err := grantFromReadTuple(tuple.Key, request.Object)
		if err != nil {
			return authz.ListGrantsResult{}, fmt.Errorf("%w: read response contains invalid tuple", authz.ErrMalformedResponse)
		}
		if grant.Subject().String() != request.Subject.String() {
			return authz.ListGrantsResult{}, fmt.Errorf("%w: read response contains another subject", authz.ErrMalformedResponse)
		}
		result.Grants = append(result.Grants, grant)
	}
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
		key := tupleKey{User: grant.Subject().String(), Relation: grant.Relation().String(), Object: grant.Object().String()}
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

// tupleForGrant renders a Grant as OpenFGA's wire tuple.
//
//	NewGrant(user:alice, stack:abc, RelationOwner)
//	  → tupleKey{User: "user:alice", Relation: "owner", Object: "stack:abc"}
func tupleForGrant(grant authz.Grant) tupleKey {
	return tupleKey{User: grant.Subject().String(), Relation: grant.Relation().String(), Object: grant.Object().String()}
}

// objectFromCanonical parses a wire object string back into a validated Object,
// requiring it to round-trip exactly so a malformed response cannot smuggle a
// different identifier past validation.
//
//	objectFromCanonical(TypeStack, "stack:abc")   → Object{"stack:abc"}, nil
//	objectFromCanonical(TypeStack, "user:abc")    → Object{}, error  (wrong prefix)
//	objectFromCanonical(TypeStack, "stack:a:b")   → Object{}, error
//	objectFromCanonical(TypeStack, "abc")         → Object{}, error  (no prefix)
func objectFromCanonical(objectType authz.ObjectType, raw string) (authz.Object, error) {
	prefix := string(objectType) + ":"
	if !strings.HasPrefix(raw, prefix) {
		return authz.Object{}, fmt.Errorf("missing %s prefix", objectType)
	}
	object, err := authz.ObjectFromID(objectType, strings.TrimPrefix(raw, prefix))
	if err != nil || object.String() != raw {
		return authz.Object{}, fmt.Errorf("invalid %s object", objectType)
	}
	return object, nil
}

// grantFromReadTuple converts one tuple from a read response into a Grant,
// refusing anything that is not a grant on the object that was asked about.
//
//	{user:alice, owner, stack:abc}, stack:abc      → Grant{…}, nil
//	{platform:tflive, parent, stack:abc}, stack:abc → Grant{}, error  (not a grant)
//	{user:alice, can_view, stack:abc}, stack:abc   → Grant{}, error
//	{user:alice, owner, stack:other}, stack:abc    → Grant{}, error  (wrong object)
//	nil, stack:abc                                 → Grant{}, error
func grantFromReadTuple(key *tupleKey, requestedObject authz.Object) (authz.Grant, error) {
	const subjectPrefix = "user:"
	if key == nil || key.Object != requestedObject.String() || !strings.HasPrefix(key.User, subjectPrefix) {
		return authz.Grant{}, fmt.Errorf("invalid tuple key")
	}
	subject, err := authz.SubjectFromOIDCSub(strings.TrimPrefix(key.User, subjectPrefix))
	if err != nil || subject.String() != key.User {
		return authz.Grant{}, fmt.Errorf("invalid tuple subject")
	}
	// GrantRelation, not NewRelation: ListGrants answers "who has access", and
	// a structural edge such as parent is not access. Once #141 writes parent
	// edges, this is what keeps them out of the grant list.
	relation, err := authz.GrantRelation(key.Relation)
	if err != nil {
		return authz.Grant{}, fmt.Errorf("invalid tuple relation")
	}
	return authz.NewGrant(subject, requestedObject, relation)
}

type tupleKey struct {
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

// tuple renders a CheckRequest as OpenFGA's wire tuple.
//
//	CheckRequest{user:alice, can_view, stack:abc}
//	  → tupleKey{User: "user:alice", Relation: "can_view", Object: "stack:abc"}
func tuple(request authz.CheckRequest) tupleKey {
	return tupleKey{User: request.Subject.String(), Relation: request.Relation.String(), Object: request.Object.String()}
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
	if errors.As(err, &statusError) {
		if statusError.StatusCode == http.StatusTooManyRequests || statusError.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: OpenFGA returned a retryable response", authz.ErrUnavailable)
		}
		return fmt.Errorf("%w: OpenFGA returned an unexpected response", authz.ErrMalformedResponse)
	}
	return err
}
