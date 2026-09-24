package authorization

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// maxChecksPerBatch is OpenFGA's server-side limit on checks in one BatchCheck.
// Exceeding it is rejected, so CanAll splits larger batches itself and callers
// never have to know the number. The 13-stack case in #220 is what found it.
const maxChecksPerBatch = 50

// readPageSize is how many stored tuples one ListGrants page asks for.
const readPageSize = 100

// Check is one question within CanAll: a relation on an object, for the subject
// the batch is about.
type Check struct {
	Relation Relation
	Object   Object
}

// Can answers one permission question.
//
// It answers only allowed or denied; every other outcome is an error, never a
// decision, so a dependency failure can never read as a grant.
//
//	alice may view stack one → true, nil
//	alice may not view it    → false, nil
//	OpenFGA cannot answer    → false, error (never a decision)
func (auth *Authorization) Can(ctx context.Context, subject string, relation Relation, object Object) (bool, error) {
	subjectID, err := SubjectFromOIDCSub(subject)
	if err != nil {
		return false, fmt.Errorf("%w: authorization subject: %w", ErrInvalidInput, err)
	}
	if !relation.Valid() || !object.Valid() {
		return false, fmt.Errorf("%w: invalid authorization check", ErrInvalidInput)
	}

	response, err := auth.server.Check(ctx, &openfgav1.CheckRequest{
		StoreId:              auth.storeID,
		AuthorizationModelId: auth.modelID,
		TupleKey: &openfgav1.CheckRequestTupleKey{
			User:     subjectID.String(),
			Relation: relation.String(),
			Object:   object.String(),
		},
	})
	if err != nil {
		return false, fmt.Errorf("check %s on %s: %w", relation, object, err)
	}
	// A nil response with a nil error is the in-process equivalent of the
	// absent "allowed" field the HTTP client used to guard against. It means
	// unknown, not denied.
	if response == nil {
		return false, fmt.Errorf("check %s on %s returned no response", relation, object)
	}
	return response.GetAllowed(), nil
}

// CanAll answers independent questions about one subject, preserving the
// caller's ordering. It splits the batch at maxChecksPerBatch, so a caller with
// more questions than OpenFGA accepts in one request never has to know.
//
//	 12 checks → 1 request,  12 answers in input order
//	 52 checks → 2 requests (50 + 2), 52 answers in input order  [#220]
//	  0 checks → nil, nil
//	OpenFGA cannot answer → nil, error
func (auth *Authorization) CanAll(ctx context.Context, subject string, checks []Check) ([]bool, error) {
	if len(checks) == 0 {
		return nil, nil
	}
	subjectID, err := SubjectFromOIDCSub(subject)
	if err != nil {
		return nil, fmt.Errorf("%w: authorization subject: %w", ErrInvalidInput, err)
	}

	results := make([]bool, len(checks))
	for start := 0; start < len(checks); start += maxChecksPerBatch {
		end := min(start+maxChecksPerBatch, len(checks))
		chunk := checks[start:end]

		items := make([]*openfgav1.BatchCheckItem, len(chunk))
		for index, check := range chunk {
			if !check.Relation.Valid() || !check.Object.Valid() {
				return nil, fmt.Errorf("%w: invalid authorization check", ErrInvalidInput)
			}
			items[index] = &openfgav1.BatchCheckItem{
				TupleKey: &openfgav1.CheckRequestTupleKey{
					User:     subjectID.String(),
					Relation: check.Relation.String(),
					Object:   check.Object.String(),
				},
				CorrelationId: strconv.Itoa(index),
			}
		}

		response, err := auth.server.BatchCheck(ctx, &openfgav1.BatchCheckRequest{
			StoreId:              auth.storeID,
			AuthorizationModelId: auth.modelID,
			Checks:               items,
		})
		if err != nil {
			return nil, fmt.Errorf("batch check: %w", err)
		}
		if len(response.GetResult()) != len(chunk) {
			return nil, fmt.Errorf("batch check returned %d results for %d checks", len(response.GetResult()), len(chunk))
		}

		// Driven by the input, not by ranging the response map: ranging would
		// let OpenFGA's answers land against the wrong questions.
		for index := range chunk {
			correlationID := strconv.Itoa(index)
			single, ok := response.GetResult()[correlationID]
			if !ok {
				return nil, fmt.Errorf("batch check result %q is missing", correlationID)
			}
			if errorResult := single.GetError(); errorResult != nil {
				return nil, fmt.Errorf("batch check result %q failed: %s", correlationID, errorResult.String())
			}
			results[start+index] = single.GetAllowed()
		}
	}
	return results, nil
}

// ListGrants returns every direct role assignment on one object, sorted by
// subject then relation, paging until OpenFGA stops returning a token.
//
// The read is filtered by object alone, so OpenFGA returns every tuple stored
// against it -- not only grants. Each one is classified by whether it could
// legitimately be there: a structural edge is real and is not access, so it is
// skipped, while anything OpenFGA would never have stored means the store or
// the response cannot be trusted, and fails the call. See grantFromTuple.
//
//	[{alice, owner, one}]                      → [{alice, owner, one}]
//	[{platform:tflive, parent, one}]           → []            (structural)
//	[{alice, can_view, one}]                   → error         (unstorable)
//	[{alice, owner, one}, {bob, viewer, one}]  → both, sorted by subject
//	[{alice, owner, other}]                    → error         (wrong object)
func (auth *Authorization) ListGrants(ctx context.Context, object Object) ([]Grant, error) {
	if !object.Valid() {
		return nil, fmt.Errorf("%w: invalid grants object", ErrInvalidInput)
	}

	var grants []Grant
	seenGrants := map[string]struct{}{}
	seenTokens := map[string]struct{}{}
	token := ""
	for {
		response, err := auth.server.Read(ctx, &openfgav1.ReadRequest{
			StoreId:           auth.storeID,
			TupleKey:          &openfgav1.ReadRequestTupleKey{Object: object.String()},
			PageSize:          wrapperspb.Int32(readPageSize),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("read grants on %s: %w", object, err)
		}
		for _, stored := range response.GetTuples() {
			grant, err := grantFromTuple(stored.GetKey(), object)
			if errors.Is(err, errNotAGrant) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read grants on %s: %w", object, err)
			}
			key := grant.Subject().String() + "\x00" + grant.Relation().String()
			if _, duplicate := seenGrants[key]; duplicate {
				return nil, fmt.Errorf("read grants on %s: duplicate grant", object)
			}
			seenGrants[key] = struct{}{}
			grants = append(grants, grant)
		}
		token = response.GetContinuationToken()
		if token == "" {
			break
		}
		if _, repeated := seenTokens[token]; repeated {
			return nil, fmt.Errorf("read grants on %s: repeated continuation token", object)
		}
		seenTokens[token] = struct{}{}
	}

	sort.Slice(grants, func(i, j int) bool {
		if grants[i].Subject().String() != grants[j].Subject().String() {
			return grants[i].Subject().String() < grants[j].Subject().String()
		}
		return grants[i].Relation().String() < grants[j].Relation().String()
	})
	return grants, nil
}

// Grant adds direct role assignments.
//
// Under a context carrying a transaction the write lands in it, so the grant
// commits with whatever domain change caused it. There is nothing to confirm
// afterwards: it lands, or the caller's transaction is rolled back.
//
//	Grant(ctx, owner, parent) → both, or neither
//	Grant(ctx)                → ErrInvalidInput
//	the same grant twice      → ErrInvalidInput (OpenFGA rejects duplicates)
func (auth *Authorization) Grant(ctx context.Context, grants ...Grant) error {
	keys, err := writeKeys(grants)
	if err != nil {
		return err
	}
	if _, err := auth.server.Write(ctx, &openfgav1.WriteRequest{
		StoreId:              auth.storeID,
		AuthorizationModelId: auth.modelID,
		Writes:               &openfgav1.WriteRequestWrites{TupleKeys: keys},
	}); err != nil {
		return fmt.Errorf("grant: %w", err)
	}
	return nil
}

// Revoke removes direct role assignments, the mirror of Grant.
func (auth *Authorization) Revoke(ctx context.Context, grants ...Grant) error {
	keys, err := deleteKeys(grants)
	if err != nil {
		return err
	}
	if _, err := auth.server.Write(ctx, &openfgav1.WriteRequest{
		StoreId:              auth.storeID,
		AuthorizationModelId: auth.modelID,
		Deletes:              &openfgav1.WriteRequestDeletes{TupleKeys: keys},
	}); err != nil {
		return fmt.Errorf("revoke: %w", err)
	}
	return nil
}

// validGrants re-checks a mutation at the provider boundary. The rule added
// here is OpenFGA's, not the domain's: a request carrying the same tuple twice
// is rejected by the server, so it is caught before the round trip.
func validGrants(grants []Grant) error {
	if len(grants) == 0 {
		return fmt.Errorf("%w: relationship mutation is empty", ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(grants))
	for _, grant := range grants {
		if !grant.Valid() {
			return fmt.Errorf("%w: invalid relationship mutation grant", ErrInvalidInput)
		}
		key := grant.Subject().String() + "\x00" + grant.Relation().String() + "\x00" + grant.Object().String()
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: duplicate relationship mutation grant", ErrInvalidInput)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// writeKeys renders grants as the tuple keys a write takes.
func writeKeys(grants []Grant) ([]*openfgav1.TupleKey, error) {
	if err := validGrants(grants); err != nil {
		return nil, err
	}
	keys := make([]*openfgav1.TupleKey, len(grants))
	for index, grant := range grants {
		keys[index] = &openfgav1.TupleKey{
			User:     grant.Subject().String(),
			Relation: grant.Relation().String(),
			Object:   grant.Object().String(),
		}
	}
	return keys, nil
}

// deleteKeys renders grants as the tuple keys a delete takes. Deletes carry a
// different type from writes -- no condition slot -- hence two renderers.
func deleteKeys(grants []Grant) ([]*openfgav1.TupleKeyWithoutCondition, error) {
	if err := validGrants(grants); err != nil {
		return nil, err
	}
	keys := make([]*openfgav1.TupleKeyWithoutCondition, len(grants))
	for index, grant := range grants {
		keys[index] = &openfgav1.TupleKeyWithoutCondition{
			User:     grant.Subject().String(),
			Relation: grant.Relation().String(),
			Object:   grant.Object().String(),
		}
	}
	return keys, nil
}

// A read filtered only by object returns every tuple stored against it, and not
// all of them are grants. These sentinels separate the two reasons one cannot
// become a Grant, because callers must treat them differently.
var (
	// errNotAGrant reports a tuple that legitimately exists and simply is not
	// access -- a structural edge such as {platform:tflive, parent, stack:X}.
	// "Who has access to this stack" still has a correct and complete answer
	// with such a tuple present, so a lister skips it.
	errNotAGrant = errors.New("tuple is not a grant")
	// errMalformedTuple reports a tuple that could not be trusted at all: no
	// key, an object other than the one asked about, a subject that does not
	// survive canonicalization, or a relation OpenFGA would never have stored.
	// There is no safe answer when the provider returns one, so a lister fails
	// closed rather than returning a shortened list that looks ordinary.
	errMalformedTuple = errors.New("tuple is malformed")
)

// grantFromTuple converts one stored tuple into a Grant, classifying anything
// that is not one as either not-a-grant or malformed.
//
//	{user:alice, owner, stack:abc}, stack:abc       → Grant{…}, nil
//	{platform:tflive, parent, stack:abc}, stack:abc → errNotAGrant       (structural edge)
//	{user:alice, root, stack:abc}, stack:abc        → errNotAGrant       (structural)
//	{user:alice, can_view, stack:abc}, stack:abc    → errMalformedTuple  (derived; unstorable)
//	{user:alice, nonsense, stack:abc}, stack:abc    → errMalformedTuple  (unknown; unstorable)
//	{user:alice, owner, stack:other}, stack:abc     → errMalformedTuple  (wrong object)
//	{user:al#ce, owner, stack:abc}, stack:abc       → errMalformedTuple  (unparseable subject)
//	nil, stack:abc                                  → errMalformedTuple
func grantFromTuple(key *openfgav1.TupleKey, requestedObject Object) (Grant, error) {
	const subjectPrefix = "user:"
	if key == nil {
		return Grant{}, fmt.Errorf("%w: missing key", errMalformedTuple)
	}
	// A tuple for another object is an integrity failure, not a filtering
	// question: we asked about one object and the provider answered about a
	// different one. It must never be quietly dropped.
	if key.GetObject() != requestedObject.String() {
		return Grant{}, fmt.Errorf("%w: object is not the one requested", errMalformedTuple)
	}
	// A non-user subject is legitimate: #141 stores {platform:tflive, parent,
	// stack:X} on every stack so admins inherit stack permissions. It is a
	// structural edge, not access, so it is skipped rather than refused.
	if !strings.HasPrefix(key.GetUser(), subjectPrefix) {
		return Grant{}, fmt.Errorf("%w: subject is not a user", errNotAGrant)
	}
	// Past the prefix, the subject must canonicalize back to exactly what was
	// stored. A "user:"-prefixed value that does not is anomalous rather than
	// merely uninteresting -- usersets and other types are caught above by
	// their own prefix -- so this fails rather than skips.
	subject, err := SubjectFromOIDCSub(strings.TrimPrefix(key.GetUser(), subjectPrefix))
	if err != nil || subject.String() != key.GetUser() {
		return Grant{}, fmt.Errorf("%w: subject is not canonical", errMalformedTuple)
	}
	relation, err := NewRelation(key.GetRelation())
	if err != nil {
		return Grant{}, fmt.Errorf("%w: relation name is invalid", errMalformedTuple)
	}
	// Storability, not grantability, is what separates these two. A structural
	// relation is one OpenFGA stores and that is not access, so it legitimately
	// sits on the object and is skipped.
	if relation.Structural() {
		return Grant{}, fmt.Errorf("%w: relation %q is structural", errNotAGrant, key.GetRelation())
	}
	// Anything else non-grantable cannot legitimately be stored at all -- a
	// derived relation declares no directly_related_user_types and OpenFGA
	// rejects the write, and an unknown relation does not exist on the type.
	// Being handed one means the store is corrupt or this is not the store we
	// think it is, so it fails rather than quietly shortening the answer.
	if !relation.Grantable() {
		return Grant{}, fmt.Errorf("%w: relation %q cannot be a stored grant", errMalformedTuple, key.GetRelation())
	}
	grant, err := NewGrant(subject, requestedObject, relation)
	if err != nil {
		return Grant{}, fmt.Errorf("%w: %w", errMalformedTuple, err)
	}
	return grant, nil
}
