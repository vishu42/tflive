// Package authorizer implements the provider-neutral authz port on top of
// OpenFGA.
//
// The split is deliberate. Package openfga knows OpenFGA's wire format and
// nothing about this application's domain; this package knows both, and is the
// only place the two meet. Everything that makes an authorization answer
// trustworthy lives here rather than in the client: deciding what an absent
// field means, matching batch answers back to the questions that produced
// them, and classifying every failure so that no transport problem can be
// mistaken for a grant.
package authorizer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/openfga"
)

// Adapter translates the provider-neutral authorization port to OpenFGA.
type Adapter struct {
	client  *openfga.Client
	storeID string
	modelID string
}

const maxConfirmationChecks = 25

// New returns an OpenFGA adapter configured to check the exact verified store
// and authorization model.
func New(cfg openfga.Config) (*Adapter, error) {
	if cfg.APIURL == nil {
		return nil, fmt.Errorf("OpenFGA API URL is required")
	}
	if err := cfg.ValidateVerify(); err != nil {
		return nil, fmt.Errorf("validate OpenFGA authorization config: %w", err)
	}
	return &Adapter{client: openfga.NewClient(cfg), storeID: cfg.StoreID, modelID: cfg.ModelID}, nil
}

// Check evaluates one derived permission against the configured OpenFGA model.
func (adapter *Adapter) Check(ctx context.Context, request authz.CheckRequest) (authz.CheckResult, error) {
	if !request.Valid() {
		return authz.CheckResult{}, fmt.Errorf("%w: invalid authorization check", authz.ErrInvalidInput)
	}

	response, err := adapter.client.Check(ctx, adapter.storeID, openfga.CheckRequest{
		AuthorizationModelID: adapter.modelID,
		TupleKey:             tuple(request),
	})
	if err != nil {
		return authz.CheckResult{}, adapter.classify(err)
	}
	if response.Allowed == nil {
		return authz.CheckResult{}, fmt.Errorf("%w: check allowed is missing", authz.ErrMalformedResponse)
	}
	return authz.CheckResult{Allowed: *response.Allowed}, nil
}

// BatchCheck evaluates independently correlated permission checks and preserves
// the caller's input ordering. It splits the request into upstream calls of at
// most openfga.MaxChecksPerBatchCheck, so callers never have to know the limit.
//
//	 12 checks  → 1 upstream request,  12 results in input order
//	 51 checks  → 2 upstream requests (50 + 1), 51 results in input order
//	 52 checks  → 2 upstream requests (50 + 2)   [the 13-stack case, #220]
//	  0 checks  → ErrInvalidInput (BatchCheckRequest.Valid rejects it)
//	upstream 5xx → ErrUnavailable
func (adapter *Adapter) BatchCheck(ctx context.Context, request authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	if !request.Valid() {
		return authz.BatchCheckResult{}, fmt.Errorf("%w: invalid authorization batch check", authz.ErrInvalidInput)
	}

	result := authz.BatchCheckResult{Results: make([]authz.CheckResult, len(request.Checks))}
	for start := 0; start < len(request.Checks); start += openfga.MaxChecksPerBatchCheck {
		end := min(start+openfga.MaxChecksPerBatchCheck, len(request.Checks))
		chunk := request.Checks[start:end]

		input := openfga.BatchCheckRequest{
			AuthorizationModelID: adapter.modelID,
			Checks:               make([]openfga.BatchCheckItem, len(chunk)),
		}
		for index, check := range chunk {
			input.Checks[index] = openfga.BatchCheckItem{TupleKey: tuple(check), CorrelationID: strconv.Itoa(index)}
		}

		response, err := adapter.client.BatchCheck(ctx, adapter.storeID, input)
		if err != nil {
			return authz.BatchCheckResult{}, adapter.classify(err)
		}
		if len(response.Result) != len(chunk) {
			return authz.BatchCheckResult{}, fmt.Errorf("%w: batch check correlation results do not match requests", authz.ErrMalformedResponse)
		}

		// Driven by the input, not by ranging the response map: ranging would
		// let OpenFGA's answers land against the wrong questions.
		for index := range chunk {
			correlationID := strconv.Itoa(index)
			check, ok := response.Result[correlationID]
			if !ok || check.Allowed == nil {
				return authz.BatchCheckResult{}, fmt.Errorf("%w: batch check result %q is missing or invalid", authz.ErrMalformedResponse, correlationID)
			}
			result.Results[start+index] = authz.CheckResult{Allowed: *check.Allowed}
		}
	}
	return result, nil
}

// ListGrants returns all direct role assignments for the requested object.
func (adapter *Adapter) ListGrants(ctx context.Context, request authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	if !request.Valid() {
		return authz.ListGrantsResult{}, fmt.Errorf("%w: invalid grants request", authz.ErrInvalidInput)
	}

	result := authz.ListGrantsResult{}
	seenGrants := map[struct{ subject, relation string }]struct{}{}
	seenTokens := map[string]struct{}{}
	continuationToken := ""
	for {
		response, err := adapter.client.Read(ctx, adapter.storeID, openfga.ReadRequest{
			TupleKey:          openfga.ReadFilter{Object: request.Object.String()},
			PageSize:          100,
			ContinuationToken: continuationToken,
		})
		if err != nil {
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
		if !openfga.SafeOpaqueIdentifier(response.ContinuationToken) {
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
func (adapter *Adapter) ListSubjectGrants(ctx context.Context, request authz.ListSubjectGrantsRequest) (authz.ListGrantsResult, error) {
	if !request.Valid() {
		return authz.ListGrantsResult{}, fmt.Errorf("%w: invalid subject grants request", authz.ErrInvalidInput)
	}

	response, err := adapter.client.Read(ctx, adapter.storeID, openfga.ReadRequest{
		TupleKey: openfga.ReadFilter{User: request.Subject.String(), Object: request.Object.String()},
		PageSize: 100,
	})
	if err != nil {
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
func (adapter *Adapter) WriteRelationships(ctx context.Context, mutation authz.Mutation) error {
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
func (adapter *Adapter) DeleteRelationships(ctx context.Context, mutation authz.Mutation) error {
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
	seen := make(map[openfga.TupleKey]struct{}, len(grants))
	for _, grant := range grants {
		if !grant.Valid() {
			return nil, fmt.Errorf("%w: invalid relationship mutation grant", authz.ErrInvalidInput)
		}
		key := tupleForGrant(grant)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate relationship mutation grant", authz.ErrInvalidInput)
		}
		seen[key] = struct{}{}
	}
	return grants, nil
}

func (adapter *Adapter) write(ctx context.Context, writes, deletes []authz.Grant) error {
	request := openfga.WriteRequest{AuthorizationModelID: adapter.modelID}
	switch {
	case len(writes) > 0 && len(deletes) == 0:
		request.Writes = &openfga.WriteTupleKeys{TupleKeys: tuples(writes)}
	case len(deletes) > 0 && len(writes) == 0:
		request.Deletes = &openfga.WriteTupleKeys{TupleKeys: tuples(deletes)}
	default:
		return fmt.Errorf("%w: relationship write must contain exactly one mutation direction", authz.ErrInvalidInput)
	}
	return adapter.client.Write(ctx, adapter.storeID, request)
}

func (adapter *Adapter) confirm(ctx context.Context, grants []authz.Grant, expected bool) (bool, error) {
	for start := 0; start < len(grants); start += maxConfirmationChecks {
		end := min(start+maxConfirmationChecks, len(grants))
		chunk := grants[start:end]

		input := openfga.BatchCheckRequest{
			AuthorizationModelID: adapter.modelID,
			Checks:               make([]openfga.BatchCheckItem, len(chunk)),
			Consistency:          openfga.ConsistencyHigherConsistency,
		}
		for index, grant := range chunk {
			input.Checks[index] = openfga.BatchCheckItem{TupleKey: tupleForGrant(grant), CorrelationID: strconv.Itoa(index)}
		}

		response, err := adapter.client.BatchCheck(ctx, adapter.storeID, input)
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

func tuples(grants []authz.Grant) []openfga.TupleKey {
	result := make([]openfga.TupleKey, len(grants))
	for index, grant := range grants {
		result[index] = tupleForGrant(grant)
	}
	return result
}

// tupleForGrant renders a Grant as OpenFGA's wire tuple.
//
//	NewGrant(user:alice, stack:abc, RelationOwner)
//	  → TupleKey{User: "user:alice", Relation: "owner", Object: "stack:abc"}
func tupleForGrant(grant authz.Grant) openfga.TupleKey {
	return openfga.TupleKey{User: grant.Subject().String(), Relation: grant.Relation().String(), Object: grant.Object().String()}
}

// grantFromReadTuple converts one tuple from a read response into a Grant,
// refusing anything that is not a grant on the object that was asked about.
//
//	{user:alice, owner, stack:abc}, stack:abc      → Grant{…}, nil
//	{platform:tflive, parent, stack:abc}, stack:abc → Grant{}, error  (not a grant)
//	{user:alice, can_view, stack:abc}, stack:abc   → Grant{}, error
//	{user:alice, owner, stack:other}, stack:abc    → Grant{}, error  (wrong object)
//	nil, stack:abc                                 → Grant{}, error
func grantFromReadTuple(key *openfga.TupleKey, requestedObject authz.Object) (authz.Grant, error) {
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

// tuple renders a CheckRequest as OpenFGA's wire tuple.
//
//	CheckRequest{user:alice, can_view, stack:abc}
//	  → TupleKey{User: "user:alice", Relation: "can_view", Object: "stack:abc"}
func tuple(request authz.CheckRequest) openfga.TupleKey {
	return openfga.TupleKey{User: request.Subject.String(), Relation: request.Relation.String(), Object: request.Object.String()}
}

func (adapter *Adapter) classify(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", authz.ErrTimeout, err)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("authorization check canceled: %w", err)
	case errors.Is(err, openfga.ErrMalformedHTTPResponse):
		return fmt.Errorf("%w: %v", authz.ErrMalformedResponse, err)
	case errors.Is(err, openfga.ErrHTTPTransport), errors.Is(err, openfga.ErrHTTPBodyRead):
		return fmt.Errorf("%w: OpenFGA request failed", authz.ErrUnavailable)
	}

	var statusError *openfga.HTTPStatusError
	if errors.As(err, &statusError) {
		if statusError.StatusCode == http.StatusTooManyRequests || statusError.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: OpenFGA returned a retryable response", authz.ErrUnavailable)
		}
		return fmt.Errorf("%w: OpenFGA returned an unexpected response", authz.ErrMalformedResponse)
	}
	return err
}
