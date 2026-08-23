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

// New returns an OpenFGA adapter pinned to one store and one authorization
// model. Both IDs are required and validated here rather than per request: an
// adapter that cannot name the exact model it checks against would silently
// follow model changes, so it refuses to exist instead.
//
//	Config{APIURL, StoreID, ModelID} → *Adapter, nil
//	Config{} with no APIURL          → nil, error
//	Config with empty StoreID        → nil, error
//	Config with empty ModelID        → nil, error
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
// It answers only allowed or denied; every other outcome is an error, never a
// decision, so a dependency failure can never read as a grant.
//
//	{alice, can_view, one}, OpenFGA allows  → CheckResult{Allowed: true}, nil
//	{alice, can_view, one}, OpenFGA denies  → CheckResult{Allowed: false}, nil
//	response omits "allowed"                → CheckResult{}, ErrMalformedResponse
//	zero-value or invalid request           → CheckResult{}, ErrInvalidInput (no request sent)
//	OpenFGA unreachable                     → CheckResult{}, ErrUnavailable
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

// ListGrants returns every direct role assignment on the requested object,
// sorted by subject then relation, paging until OpenFGA stops returning a
// continuation token.
//
// The read is filtered by object alone, so OpenFGA returns every tuple stored
// against it -- not only grants. Each one is classified by whether it could
// legitimately be there: a structural edge is real and is not access, so it is
// skipped, while anything OpenFGA would never have stored means the store or
// the response cannot be trusted, and fails the call. See grantFromReadTuple.
//
// Every example below is one ListGrants({Object: stack:one}) call, described
// by what OpenFGA returned. The requested object is what "wrong object" is
// measured against, so it is the whole reason two of these differ.
//
//	tuples [{alice, owner, one}]                      → [{alice, owner, one}], nil
//	tuples [{platform:tflive, parent, one}]           → [], nil          (structural, skipped)
//	tuples [{alice, can_view, one}]                   → ErrMalformedResponse  (unstorable)
//	tuples [{alice, owner, one}, {bob, viewer, one}]  → both, sorted by subject
//	tuples [{alice, owner, other}]                    → ErrMalformedResponse  (wrong object)
//	the same grant twice                              → ErrMalformedResponse
//	a repeated continuation token                     → ErrMalformedResponse  (page loop)
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
			if errors.Is(err, errNotAGrant) {
				continue
			}
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
//
// Filtering by user is also why structural edges rarely reach it: a platform
// subject cannot match a user filter. The classification is applied anyway so
// both listers answer "who has access" by exactly the same rule.
//
// Every example below is one ListSubjectGrants({Subject: user:alice, Object:
// stack:one}) call, described by what OpenFGA returned. Both halves of that
// request are what the last two rows are measured against.
//
//	tuples [{alice, owner, one}]   → [{alice, owner, one}], nil
//	tuples []                      → [], nil
//	tuples [{bob, owner, one}]     → ErrMalformedResponse  (another subject; filter not honored)
//	tuples [{alice, owner, other}] → ErrMalformedResponse  (another object)
//	OpenFGA unreachable            → ErrUnavailable
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
		if errors.Is(err, errNotAGrant) {
			continue
		}
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

// WriteRelationships grants direct roles, making a non-idempotent OpenFGA
// write safe to retry. An OpenFGA write that adds an existing tuple is
// rejected, so a rejection is not taken at face value: the desired state is
// re-read at higher consistency, and the operation succeeds only if the grants
// are actually visible.
//
//	new grants, write accepted            → nil
//	grants already held, write rejected   → nil    (confirmed present; replay is a no-op)
//	write rejected, grants not visible    → ErrWriteUnconfirmed
//	Confirm() set, grants not yet visible → ErrWriteUnconfirmed
//	OpenFGA unreachable                   → ErrUnavailable
//
// ErrWriteUnconfirmed means "unknown", not "failed": the write may have landed,
// so the caller may safely retry but must not report success.
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

// DeleteRelationships revokes direct roles, the mirror of WriteRelationships.
// An OpenFGA delete of a missing tuple is rejected, so a rejection is checked
// against observed state before it is believed.
//
//	held grants, delete accepted           → nil
//	grants already absent, delete rejected → nil    (confirmed absent; replay is a no-op)
//	delete rejected, grants still visible  → ErrWriteUnconfirmed
//	Confirm() set, grants still visible    → ErrWriteUnconfirmed
//	OpenFGA unreachable                    → ErrUnavailable
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

// validMutation re-checks a Mutation at the provider boundary and returns its
// grants. authz.NewMutation already refused an empty or invalid one; the rule
// added here is OpenFGA's, not the domain's: a write carrying the same tuple
// twice is rejected by the server, so it is caught before the round trip.
//
//	Mutation{[{alice, owner, one}]}                    → [1 grant], nil
//	Mutation{[{alice, owner, one}, {bob, owner, one}]} → [2 grants], nil
//	Mutation{[{alice, owner, one}, {alice, owner, one}]} → nil, ErrInvalidInput  (duplicate)
//	Mutation{}                                         → nil, ErrInvalidInput  (zero value)
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

// write issues one transactional tuple mutation in exactly one direction.
// OpenFGA rejects a request carrying both writes and deletes, and rejects one
// carrying neither, so that is refused here before the round trip rather than
// surfacing as an opaque 400.
//
// It does not retry and does not interpret rejection: an OpenFGA write is not
// idempotent, so adding a tuple that exists or removing one that does not is
// an error the caller must reconcile against observed state. That is what
// WriteRelationships and DeleteRelationships use confirm for.
//
//	write(ctx, [grant], nil)     → POST /write {writes:{tuple_keys:[…]}}
//	write(ctx, nil, [grant])     → POST /write {deletes:{tuple_keys:[…]}}
//	write(ctx, [grant], [grant]) → ErrInvalidInput, no request sent
//	write(ctx, nil, nil)         → ErrInvalidInput, no request sent
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

// confirm re-reads grants at HIGHER_CONSISTENCY and reports whether every one
// of them matches expected. It is how a non-idempotent write is turned into a
// safe one: a rejected write is only tolerated if the desired state is already
// visible, and a mutation asking to be confirmed is only reported as success
// once it is.
//
// It returns (false, nil) for "checked, and the state is not what you wanted"
// and (false, err) for "could not find out". Those are different outcomes and
// callers must not collapse them: the first is a definite no, the second is
// ErrWriteUnconfirmed territory. Checks are chunked at maxConfirmationChecks.
//
//	confirm(ctx, [granted alice], true)   → true, nil
//	confirm(ctx, [ungranted alice], true) → false, nil   (definite: not visible)
//	confirm(ctx, [revoked alice], false)  → true, nil    (absence confirmed)
//	OpenFGA 5xx                           → false, ErrUnavailable
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

// tuples renders each Grant as OpenFGA's wire tuple, preserving order.
//
//	tuples([{alice, owner, one}, {bob, viewer, one}])
//	  → [{user:alice, owner, stack:one}, {user:bob, viewer, stack:one}]
//	tuples(nil) → []
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

// A read filtered only by object returns every tuple stored against it, and
// not all of them are grants. These sentinels separate the two reasons one
// cannot become a Grant, because the callers must treat them differently.
var (
	// errNotAGrant reports a tuple that legitimately exists and simply is not
	// access -- a structural edge such as {platform:tflive, parent, stack:X}.
	// "Who has access to this stack" still has a correct and complete answer
	// with such a tuple present, so a lister skips it.
	errNotAGrant = errors.New("tuple is not a grant")
	// errMalformedTuple reports a tuple that could not be trusted at all: no
	// key, an object other than the one asked about, a subject that does not
	// survive canonicalization, or a relation OpenFGA would never have stored.
	// There is no safe answer to give when the provider returns one of these,
	// so a lister fails closed rather than returning a shortened list that
	// looks ordinary.
	errMalformedTuple = errors.New("tuple is malformed")
)

// grantFromReadTuple converts one tuple from a read response into a Grant,
// classifying anything that is not one as either not-a-grant or malformed.
//
//	{user:alice, owner, stack:abc}, stack:abc       → Grant{…}, nil
//	{platform:tflive, parent, stack:abc}, stack:abc → errNotAGrant       (structural edge)
//	{user:alice, root, stack:abc}, stack:abc        → errNotAGrant       (structural)
//	{user:alice, can_view, stack:abc}, stack:abc    → errMalformedTuple  (derived; unstorable)
//	{user:alice, nonsense, stack:abc}, stack:abc    → errMalformedTuple  (unknown; unstorable)
//	{user:alice, owner, stack:other}, stack:abc     → errMalformedTuple  (wrong object)
//	{user:al#ce, owner, stack:abc}, stack:abc       → errMalformedTuple  (unparseable subject)
//	nil, stack:abc                                  → errMalformedTuple
func grantFromReadTuple(key *openfga.TupleKey, requestedObject authz.Object) (authz.Grant, error) {
	const subjectPrefix = "user:"
	if key == nil {
		return authz.Grant{}, fmt.Errorf("%w: missing key", errMalformedTuple)
	}
	// A tuple for another object is an integrity failure, not a filtering
	// question: we asked about one object and the provider answered about a
	// different one. It must never be quietly dropped.
	if key.Object != requestedObject.String() {
		return authz.Grant{}, fmt.Errorf("%w: object is not the one requested", errMalformedTuple)
	}
	// A non-user subject is legitimate: #141 stores {platform:tflive, parent,
	// stack:X} on every stack so admins inherit stack permissions. It is a
	// structural edge, not access, so it is skipped rather than refused.
	if !strings.HasPrefix(key.User, subjectPrefix) {
		return authz.Grant{}, fmt.Errorf("%w: subject is not a user", errNotAGrant)
	}
	// Past the prefix, the subject must canonicalize back to exactly what was
	// stored. A "user:"-prefixed value that does not is anomalous rather than
	// merely uninteresting -- usersets and other types are caught above by
	// their own prefix -- so this fails rather than skips.
	subject, err := authz.SubjectFromOIDCSub(strings.TrimPrefix(key.User, subjectPrefix))
	if err != nil || subject.String() != key.User {
		return authz.Grant{}, fmt.Errorf("%w: subject is not canonical", errMalformedTuple)
	}
	relation, err := authz.NewRelation(key.Relation)
	if err != nil {
		return authz.Grant{}, fmt.Errorf("%w: relation name is invalid", errMalformedTuple)
	}
	// Storability, not grantability, is what separates these two. A structural
	// relation is one OpenFGA stores and that is not access, so it legitimately
	// sits on the object and is skipped.
	if relation.Structural() {
		return authz.Grant{}, fmt.Errorf("%w: relation %q is structural", errNotAGrant, key.Relation)
	}
	// Anything else non-grantable cannot legitimately be stored at all --
	// a derived relation declares no directly_related_user_types and OpenFGA
	// rejects the write, and an unknown relation does not exist on the type.
	// Being handed one means the store is corrupt or this is not the store we
	// think it is, so it fails rather than quietly shortening the answer.
	if !relation.Grantable() {
		return authz.Grant{}, fmt.Errorf("%w: relation %q cannot be a stored grant", errMalformedTuple, key.Relation)
	}
	grant, err := authz.NewGrant(subject, requestedObject, relation)
	if err != nil {
		return authz.Grant{}, fmt.Errorf("%w: %v", errMalformedTuple, err)
	}
	return grant, nil
}

// tuple renders a CheckRequest as OpenFGA's wire tuple.
//
//	CheckRequest{user:alice, can_view, stack:abc}
//	  → TupleKey{User: "user:alice", Relation: "can_view", Object: "stack:abc"}
func tuple(request authz.CheckRequest) openfga.TupleKey {
	return openfga.TupleKey{User: request.Subject.String(), Relation: request.Relation.String(), Object: request.Object.String()}
}

// classify maps a transport or protocol failure onto the port's error
// vocabulary. It is the single place a provider problem becomes a domain
// error, and it exists so no failure mode can reach a caller as a decision.
//
// Every branch fails closed. The 4xx-to-ErrMalformedResponse mapping is
// deliberate: a request this adapter built being rejected as invalid means the
// adapter and the model disagree, which is a bug here, not a denial.
//
//	context deadline exceeded → ErrTimeout
//	context canceled          → plain error (the caller went away; not a fault)
//	bad media type, undecodable, oversized → ErrMalformedResponse
//	dial failure, body read failure        → ErrUnavailable
//	429, 500, 502, 503                     → ErrUnavailable  (retryable)
//	400, 401, 403, 404                     → ErrMalformedResponse
//	anything else             → returned unchanged
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
