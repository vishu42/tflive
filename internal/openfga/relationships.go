package openfga

import (
	"context"
	"net/http"
)

// This file is the relationship half of the OpenFGA API: the endpoints that
// evaluate and mutate tuples, as opposed to the store and model endpoints in
// client.go. The methods are deliberately thin. They render the documented
// request body, enforce the accepted status, and decode the documented
// response; they do not interpret results, retry, or wrap transport errors.
// Callers own the meaning of an answer, including what an absent field means.

// TupleKey is OpenFGA's relationship tuple: a user, a relation, and an object.
type TupleKey struct {
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

// CheckRequest asks whether one tuple holds under a specific model.
type CheckRequest struct {
	AuthorizationModelID string   `json:"authorization_model_id"`
	TupleKey             TupleKey `json:"tuple_key"`
}

// CheckResponse is OpenFGA's answer to a single check.
//
// Allowed is a pointer on purpose: a response that omits the field must stay
// distinguishable from one that says false. Decoding into a plain bool would
// turn a malformed response into a denial that no caller could detect.
type CheckResponse struct {
	Allowed *bool `json:"allowed"`
}

// Check evaluates one tuple against the store's authorization model. It
// reports what OpenFGA said and nothing more: a denial is a successful call
// returning Allowed false, and an absent field is returned as a nil Allowed
// for the caller to judge.
//
//	200 {"allowed":true}   → CheckResponse{Allowed: &true},  nil
//	200 {"allowed":false}  → CheckResponse{Allowed: &false}, nil
//	200 {}                 → CheckResponse{Allowed: nil},    nil  (caller decides)
//	400 / 500 / dial error → CheckResponse{}, error
func (client *Client) Check(ctx context.Context, storeID string, request CheckRequest) (CheckResponse, error) {
	var response CheckResponse
	endpoint := client.endpoint("stores", storeID, "check")
	if err := client.doJSON(ctx, http.MethodPost, endpoint, nil, request, &response, http.StatusOK); err != nil {
		return CheckResponse{}, err
	}
	return response, nil
}

// BatchCheckItem is one check within a batch, tagged so its answer can be
// matched back to the question the caller asked.
type BatchCheckItem struct {
	TupleKey      TupleKey `json:"tuple_key"`
	CorrelationID string   `json:"correlation_id"`
}

// BatchCheckRequest evaluates independent checks in one round trip. Consistency
// is omitted unless set, which selects OpenFGA's default consistency.
type BatchCheckRequest struct {
	AuthorizationModelID string           `json:"authorization_model_id"`
	Checks               []BatchCheckItem `json:"checks"`
	Consistency          string           `json:"consistency,omitempty"`
}

// ConsistencyHigherConsistency asks OpenFGA to avoid serving a check from
// possibly stale replicas, at the cost of latency. Use it to confirm a write.
const ConsistencyHigherConsistency = "HIGHER_CONSISTENCY"

// MaxChecksPerBatchCheck is OpenFGA's server-side default limit on checks in
// one BatchCheck request. Exceeding it is rejected with 400, so callers must
// split larger batches themselves.
const MaxChecksPerBatchCheck = 50

// BatchCheckSingleResult is one answer within a batch response.
type BatchCheckSingleResult struct {
	Allowed *bool `json:"allowed"`
}

// BatchCheckResponse maps correlation IDs to answers. It is a map, not a
// slice: nothing about the response guarantees the caller's ordering, and
// nothing guarantees every question is answered.
type BatchCheckResponse struct {
	Result map[string]BatchCheckSingleResult `json:"result"`
}

// BatchCheck evaluates independent checks in one round trip and returns
// OpenFGA's correlation-keyed answers verbatim.
//
// It does not split an oversized batch, reorder results, or verify that every
// question was answered. A request over MaxChecksPerBatchCheck is rejected by
// the server with 400; matching answers back to questions is the caller's job,
// and ranging the returned map instead of indexing it by correlation ID would
// attribute answers to the wrong subjects.
//
//	2 checks, both answered → Result{"0":…, "1":…}, nil
//	2 checks, 1 answered    → Result{"0":…}, nil       (caller must notice)
//	51 checks               → BatchCheckResponse{}, error (400, over the limit)
func (client *Client) BatchCheck(ctx context.Context, storeID string, request BatchCheckRequest) (BatchCheckResponse, error) {
	var response BatchCheckResponse
	endpoint := client.endpoint("stores", storeID, "batch-check")
	if err := client.doJSON(ctx, http.MethodPost, endpoint, nil, request, &response, http.StatusOK); err != nil {
		return BatchCheckResponse{}, err
	}
	return response, nil
}

// ReadFilter selects the tuples a read returns. An empty field is omitted and
// matches any value, so a filter with only Object set returns every tuple on
// that object.
type ReadFilter struct {
	User     string `json:"user,omitempty"`
	Relation string `json:"relation,omitempty"`
	Object   string `json:"object"`
}

// ReadRequest reads stored tuples one page at a time.
type ReadRequest struct {
	TupleKey          ReadFilter `json:"tuple_key"`
	PageSize          int        `json:"page_size"`
	ContinuationToken string     `json:"continuation_token,omitempty"`
}

// ReadTuple is one stored tuple. Key is a pointer so a response containing an
// entry with no key is visible to the caller rather than silently zeroed.
type ReadTuple struct {
	Key *TupleKey `json:"key"`
}

// ReadResponse is one page of stored tuples. Tuples is a pointer so an absent
// field stays distinguishable from an empty page.
type ReadResponse struct {
	Tuples            *[]ReadTuple `json:"tuples"`
	ContinuationToken string       `json:"continuation_token"`
}

// Read returns one page of stored tuples matching the request's filter. An
// empty filter field matches any value, so the filter's narrowness decides
// what comes back.
//
// It does not follow pagination: a non-empty ContinuationToken in the response
// means more pages exist, and the caller must pass it back to get them.
//
//	filter {Object: "stack:one"}                  → every tuple on stack:one
//	filter {User: "user:alice", Object: "stack:one"} → only alice's tuples there
//	no more pages  → ReadResponse{Tuples: &[…], ContinuationToken: ""}
//	more pages     → ReadResponse{Tuples: &[…], ContinuationToken: "…"}
func (client *Client) Read(ctx context.Context, storeID string, request ReadRequest) (ReadResponse, error) {
	var response ReadResponse
	endpoint := client.endpoint("stores", storeID, "read")
	if err := client.doJSON(ctx, http.MethodPost, endpoint, nil, request, &response, http.StatusOK); err != nil {
		return ReadResponse{}, err
	}
	return response, nil
}

// WriteTupleKeys is one direction of a write: the tuples to add, or to remove.
type WriteTupleKeys struct {
	TupleKeys []TupleKey `json:"tuple_keys"`
}

// WriteRequest adds or removes tuples in one transaction. OpenFGA rejects a
// request carrying both directions, and rejects one carrying neither.
type WriteRequest struct {
	AuthorizationModelID string          `json:"authorization_model_id"`
	Writes               *WriteTupleKeys `json:"writes,omitempty"`
	Deletes              *WriteTupleKeys `json:"deletes,omitempty"`
}

// Write applies one transactional tuple mutation: all of it lands, or none of
// it does.
//
// OpenFGA writes are not idempotent. Adding a tuple that exists, or removing
// one that does not, is rejected -- so a caller that needs replay safety must
// reconcile the rejection against observed state rather than treating it as
// failure. A request carrying both directions, or neither, is also rejected.
//
//	{Writes: [tuple]}            → nil            (tuple added)
//	{Deletes: [tuple]}           → nil            (tuple removed)
//	{Writes: [existing tuple]}   → error (400)    (caller must reconcile)
//	{Deletes: [missing tuple]}   → error (400)    (caller must reconcile)
//	{Writes: […], Deletes: […]}  → error (400)
func (client *Client) Write(ctx context.Context, storeID string, request WriteRequest) error {
	endpoint := client.endpoint("stores", storeID, "write")
	return client.doJSON(ctx, http.MethodPost, endpoint, nil, request, nil, http.StatusOK)
}
