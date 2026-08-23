package openfga

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxResponseBody = 64 << 10

// Transport-level sentinels. Callers translating OpenFGA failures into their
// own domain errors match on these rather than on error strings.
var (
	// ErrMalformedHTTPResponse reports a response that was accepted by status
	// but could not be trusted: wrong media type, oversized, or undecodable.
	ErrMalformedHTTPResponse = fmt.Errorf("malformed HTTP response")
	// ErrHTTPTransport reports a request that never produced a response.
	ErrHTTPTransport = fmt.Errorf("OpenFGA HTTP transport failure")
	// ErrHTTPBodyRead reports a response whose body could not be read.
	ErrHTTPBodyRead = fmt.Errorf("OpenFGA HTTP response body read failure")
)

// HTTPStatusError reports a non-accepted OpenFGA HTTP response. Its body has
// already been redacted and bounded by Client.doJSON.
type HTTPStatusError struct {
	StatusCode int
	Status     string
	Body       string
}

// Error renders the status and the already-redacted, already-bounded body.
// Any bearer token has been replaced before this point, so the message is safe
// to log.
func (err *HTTPStatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status %s: %s", err.Status, err.Body)
}

type Store struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ModelRecord struct {
	ID    string
	Model AuthorizationModel
}

type Client struct {
	baseURL *url.URL
	token   string
	timeout time.Duration
	http    *http.Client
}

// NewClient returns a client for one OpenFGA API endpoint. HTTPTimeout is
// applied per request, not to the client's whole lifetime, and falls back to
// defaultHTTPTimeout when unset. A nil cfg.Transport means net/http's default.
//
//	Config{APIURL, HTTPTimeout: 5s} → client, 5s per request
//	Config{APIURL}                  → client, defaultHTTPTimeout per request
func NewClient(cfg Config) *Client {
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	return &Client{
		baseURL: cfg.APIURL,
		token:   cfg.APIToken,
		timeout: timeout,
		http:    &http.Client{Timeout: timeout, Transport: cfg.Transport},
	}
}

// ListStores returns every store on the endpoint, following pagination to the
// end. It validates each store as it goes -- an unsafe or missing id, or a
// missing name, fails the whole call rather than being skipped, because a
// store list is used to decide whether to create one.
//
// A repeated continuation token fails rather than looping forever.
//
//	one page, two stores      → [2 stores], nil
//	two pages                 → all stores, one slice
//	a store with an empty name → nil, error
//	a repeated page token      → nil, error
func (client *Client) ListStores(ctx context.Context) ([]Store, error) {
	var stores []Store
	token := ""
	seen := map[string]bool{}
	for {
		var page struct {
			Stores            []Store `json:"stores"`
			ContinuationToken string  `json:"continuation_token"`
		}
		query := url.Values{"page_size": {"100"}}
		if token != "" {
			query.Set("continuation_token", token)
		}
		if err := client.doJSON(ctx, http.MethodGet, client.endpoint("stores"), query, nil, &page, http.StatusOK); err != nil {
			return nil, fmt.Errorf("list OpenFGA stores: %w", err)
		}
		for _, store := range page.Stores {
			if !SafeOpaqueIdentifier(store.ID) || store.Name == "" {
				return nil, fmt.Errorf("list OpenFGA stores: response store has missing or unsafe id or missing name")
			}
			stores = append(stores, store)
		}
		if page.ContinuationToken == "" {
			return stores, nil
		}
		if seen[page.ContinuationToken] {
			return nil, fmt.Errorf("list OpenFGA stores: repeated continuation token")
		}
		seen[page.ContinuationToken] = true
		token = page.ContinuationToken
	}
}

// CreateStore creates a store and returns it, requiring 201. It verifies the
// response describes the store that was asked for: a returned name that does
// not match is an error, not a rename to accept.
//
// OpenFGA does not enforce unique store names, so calling this twice creates
// two stores. Callers that need one store reconcile before calling.
//
//	name "tflive", 201 {id, name: "tflive"} → Store{…}, nil
//	201 with a different name               → Store{}, error
//	201 with an unsafe or missing id        → Store{}, error
//	200 instead of 201                      → Store{}, error
func (client *Client) CreateStore(ctx context.Context, name string) (Store, error) {
	var store Store
	err := client.doJSON(ctx, http.MethodPost, client.endpoint("stores"), nil, map[string]string{"name": name}, &store, http.StatusCreated)
	if err != nil {
		return Store{}, fmt.Errorf("create OpenFGA store: %w", err)
	}
	if !SafeOpaqueIdentifier(store.ID) || store.Name == "" {
		return Store{}, fmt.Errorf("create OpenFGA store: response has missing or unsafe id or missing name")
	}
	if store.Name != name {
		return Store{}, fmt.Errorf("create OpenFGA store %q: response name is %q", name, store.Name)
	}
	return store, nil
}

// GetStore fetches one store and verifies the response is about it: an id
// other than the one requested is an error, not a redirect to follow.
//
//	existing id            → Store{…}, nil
//	response id mismatches → Store{}, error
//	404                    → Store{}, error
func (client *Client) GetStore(ctx context.Context, storeID string) (Store, error) {
	var store Store
	err := client.doJSON(ctx, http.MethodGet, client.endpoint("stores", storeID), nil, nil, &store, http.StatusOK)
	if err != nil {
		return Store{}, fmt.Errorf("get OpenFGA store %q: %w", storeID, err)
	}
	if !SafeOpaqueIdentifier(store.ID) {
		return Store{}, fmt.Errorf("get OpenFGA store %q: response has missing or unsafe id", storeID)
	}
	if store.ID != storeID {
		return Store{}, fmt.Errorf("get OpenFGA store %q: response id is %q", storeID, store.ID)
	}
	return store, nil
}

// ListAuthorizationModels returns every model in a store, in the order OpenFGA
// returns them, following pagination to the end. No ordering is imposed here,
// so callers that need a specific model match on it rather than on position.
//
// Each model must survive CanonicalJSON, so a model that cannot be normalized
// fails the call. That matters because the returned records exist to be
// compared against the repository's model, and a model that cannot be
// canonicalized cannot be compared -- reporting no match would be wrong.
//
//	one page, three models    → [3 records], nil
//	a model with an unsafe id → nil, error
//	a model that will not canonicalize → nil, error
//	a repeated page token     → nil, error
func (client *Client) ListAuthorizationModels(ctx context.Context, storeID string) ([]ModelRecord, error) {
	var records []ModelRecord
	token := ""
	seen := map[string]bool{}
	for {
		var page struct {
			Models            []AuthorizationModel `json:"authorization_models"`
			ContinuationToken string               `json:"continuation_token"`
		}
		query := url.Values{"page_size": {"100"}}
		if token != "" {
			query.Set("continuation_token", token)
		}
		endpoint := client.endpoint("stores", storeID, "authorization-models")
		if err := client.doJSON(ctx, http.MethodGet, endpoint, query, nil, &page, http.StatusOK); err != nil {
			return nil, fmt.Errorf("list authorization models for store %q: %w", storeID, err)
		}
		for _, model := range page.Models {
			if !SafeOpaqueIdentifier(model.ID) {
				return nil, fmt.Errorf("list authorization models for store %q: response model has missing or unsafe id", storeID)
			}
			if _, err := CanonicalJSON(model); err != nil {
				return nil, fmt.Errorf("list authorization models for store %q: invalid response model %q: %w", storeID, model.ID, err)
			}
			records = append(records, ModelRecord{ID: model.ID, Model: model})
		}
		if page.ContinuationToken == "" {
			return records, nil
		}
		if seen[page.ContinuationToken] {
			return nil, fmt.Errorf("list authorization models for store %q: repeated continuation token", storeID)
		}
		seen[page.ContinuationToken] = true
		token = page.ContinuationToken
	}
}

// GetAuthorizationModel fetches one model by id and verifies the response is
// about it. Like ListAuthorizationModels it requires the model to canonicalize.
//
//	existing id            → AuthorizationModel{…}, nil
//	response id mismatches → AuthorizationModel{}, error
//	unparseable model      → AuthorizationModel{}, error
func (client *Client) GetAuthorizationModel(ctx context.Context, storeID, modelID string) (AuthorizationModel, error) {
	var response struct {
		Model AuthorizationModel `json:"authorization_model"`
	}
	endpoint := client.endpoint("stores", storeID, "authorization-models", modelID)
	if err := client.doJSON(ctx, http.MethodGet, endpoint, nil, nil, &response, http.StatusOK); err != nil {
		return AuthorizationModel{}, fmt.Errorf("get authorization model %q in store %q: %w", modelID, storeID, err)
	}
	if !SafeOpaqueIdentifier(response.Model.ID) {
		return AuthorizationModel{}, fmt.Errorf("get authorization model %q in store %q: response has missing or unsafe id", modelID, storeID)
	}
	if response.Model.ID != modelID {
		return AuthorizationModel{}, fmt.Errorf("get authorization model %q in store %q: response id is %q", modelID, storeID, response.Model.ID)
	}
	if _, err := CanonicalJSON(response.Model); err != nil {
		return AuthorizationModel{}, fmt.Errorf("get authorization model %q in store %q: invalid response model: %w", modelID, storeID, err)
	}
	return response.Model, nil
}

// WriteAuthorizationModel writes a new immutable model version and returns it
// with the id OpenFGA assigned. Any id on the supplied model is cleared first:
// ids are the server's to mint, and sending one would be meaningless.
//
// The model is validated locally before the round trip, so a malformed model
// fails without a request. Every call creates a new version -- writing the
// same model twice yields two ids, so callers reconcile before calling.
//
//	valid model, 201        → ModelRecord{ID: assigned, Model: …}, nil
//	model with an id set    → id ignored, new one assigned
//	model that will not canonicalize → ModelRecord{}, error, no request sent
//	201 with an unsafe id   → ModelRecord{}, error
func (client *Client) WriteAuthorizationModel(ctx context.Context, storeID string, model AuthorizationModel) (ModelRecord, error) {
	model.ID = ""
	if _, err := CanonicalJSON(model); err != nil {
		return ModelRecord{}, fmt.Errorf("write authorization model in store %q: invalid model: %w", storeID, err)
	}
	var response struct {
		ID string `json:"authorization_model_id"`
	}
	endpoint := client.endpoint("stores", storeID, "authorization-models")
	if err := client.doJSON(ctx, http.MethodPost, endpoint, nil, model, &response, http.StatusCreated); err != nil {
		return ModelRecord{}, fmt.Errorf("write authorization model in store %q: %w", storeID, err)
	}
	if !SafeOpaqueIdentifier(response.ID) {
		return ModelRecord{}, fmt.Errorf("write authorization model in store %q: response has missing or unsafe authorization_model_id", storeID)
	}
	model.ID = response.ID
	return ModelRecord{ID: response.ID, Model: model}, nil
}

// endpoint builds a request URL by appending path-escaped segments to the
// configured base, preserving any base path. Escaping is why a caller may pass
// a store id straight through: a segment cannot break out of its position.
//
//	base https://fga.example, ("stores", "01H")      → https://fga.example/stores/01H
//	base https://fga.example/api/, ("stores")        → https://fga.example/api/stores
//	("stores", "a/b")                                → …/stores/a%2Fb
//
// It panics if the escaped path cannot be unescaped, which cannot happen for
// input this function itself escaped.
func (client *Client) endpoint(segments ...string) *url.URL {
	clone := *client.baseURL
	rawPath := strings.TrimRight(clone.EscapedPath(), "/")
	for _, segment := range segments {
		rawPath += "/" + url.PathEscape(segment)
	}
	path, err := url.PathUnescape(rawPath)
	if err != nil {
		panic(err)
	}
	clone.Path = path
	clone.RawPath = rawPath
	return &clone
}

// doJSON performs one JSON request and is the single point every OpenFGA call
// passes through, so the guarantees callers depend on live here:
//
//   - the per-request timeout is applied on top of the caller's context;
//   - the bearer token is attached, and redacted out of any error body;
//   - the response body is read bounded, so a hostile or broken server cannot
//     exhaust memory;
//   - a status outside accepted becomes *HTTPStatusError, carrying the
//     redacted body;
//   - a response that is not application/json is refused rather than decoded,
//     so an HTML error page never half-parses into a valid-looking result.
//
// A truncated body is an error even on an accepted status: a partial JSON
// document that happens to parse is more dangerous than no answer.
//
//	200, application/json, decodable  → nil, output populated
//	200, text/html                    → ErrMalformedHTTPResponse
//	200, body over maxResponseBody    → ErrMalformedHTTPResponse
//	404 (not accepted)                → *HTTPStatusError
//	dial failure                      → ErrHTTPTransport
//	body read failure                 → ErrHTTPBodyRead
//	output nil                        → body not decoded, status still enforced
func (client *Client) doJSON(ctx context.Context, method string, endpoint *url.URL, query url.Values, input, output any, accepted ...int) error {
	if query != nil {
		endpoint = cloneURL(endpoint)
		endpoint.RawQuery = query.Encode()
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	requestContext, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, method, endpoint.String(), body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}

	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("%w: send request: %w", ErrHTTPTransport, err)
	}
	defer response.Body.Close()
	data, truncated, err := readBounded(response.Body)
	if err != nil {
		return fmt.Errorf("%w: read response: %w", ErrHTTPBodyRead, err)
	}
	if !containsStatus(accepted, response.StatusCode) {
		safe := redact(string(data), client.token)
		if len(safe) > maxResponseBody {
			safe = safe[:maxResponseBody]
			truncated = true
		}
		if truncated {
			safe += " [TRUNCATED]"
		}
		return &HTTPStatusError{StatusCode: response.StatusCode, Status: response.Status, Body: strings.TrimSpace(safe)}
	}
	if truncated {
		return fmt.Errorf("%w: response exceeds %s bytes", ErrMalformedHTTPResponse, strconv.Itoa(maxResponseBody))
	}
	if output != nil {
		mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			return fmt.Errorf("%w: response content type must be application/json", ErrMalformedHTTPResponse)
		}
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("%w: decode response: %w", ErrMalformedHTTPResponse, err)
		}
	}
	return nil
}

// readBounded reads at most maxResponseBody bytes and reports whether more
// were available. It reads one byte past the limit to tell "exactly at the
// limit" from "over it".
//
//	1 KB body   → data, false, nil
//	64 KB body  → data, false, nil   (exactly at the limit)
//	65 KB body  → first 64 KB, true, nil
func readBounded(reader io.Reader) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxResponseBody+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxResponseBody {
		return data[:maxResponseBody], true, nil
	}
	return data, false, nil
}

// cloneURL shallow-copies a URL so a query can be attached without mutating
// the caller's value. Shallow is sufficient: only RawQuery is rewritten, and
// the pointer fields are never modified through the copy.
func cloneURL(value *url.URL) *url.URL {
	clone := *value
	return &clone
}

// containsStatus reports whether status is one the caller accepts.
//
//	([200], 200)      → true
//	([200, 201], 201) → true
//	([200], 404)      → false
//	(nil, 200)        → false  (a caller naming no status accepts none)
func containsStatus(statuses []int, status int) bool {
	for _, accepted := range statuses {
		if accepted == status {
			return true
		}
	}
	return false
}

// redact removes every occurrence of secret from value. It is applied to
// response bodies before they reach an error, because OpenFGA echoes request
// context on some failures and that must not carry the token into a log.
//
//	("token abc leaked", "abc") → "token [REDACTED] leaked"
//	("nothing here", "abc")     → "nothing here"
//	("anything", "")            → unchanged (no secret configured)
func redact(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}
