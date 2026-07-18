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

var errMalformedHTTPResponse = fmt.Errorf("malformed HTTP response")

// HTTPStatusError reports a non-accepted OpenFGA HTTP response. Its body has
// already been redacted and bounded by Client.doJSON.
type HTTPStatusError struct {
	StatusCode int
	Status     string
	Body       string
}

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

func NewClient(cfg Config) *Client {
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	return &Client{
		baseURL: cfg.APIURL,
		token:   cfg.APIToken,
		timeout: timeout,
		http:    &http.Client{Timeout: timeout},
	}
}

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
			if !safeOpaqueIdentifier(store.ID) || store.Name == "" {
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

func (client *Client) CreateStore(ctx context.Context, name string) (Store, error) {
	var store Store
	err := client.doJSON(ctx, http.MethodPost, client.endpoint("stores"), nil, map[string]string{"name": name}, &store, http.StatusCreated)
	if err != nil {
		return Store{}, fmt.Errorf("create OpenFGA store: %w", err)
	}
	if !safeOpaqueIdentifier(store.ID) || store.Name == "" {
		return Store{}, fmt.Errorf("create OpenFGA store: response has missing or unsafe id or missing name")
	}
	if store.Name != name {
		return Store{}, fmt.Errorf("create OpenFGA store %q: response name is %q", name, store.Name)
	}
	return store, nil
}

func (client *Client) GetStore(ctx context.Context, storeID string) (Store, error) {
	var store Store
	err := client.doJSON(ctx, http.MethodGet, client.endpoint("stores", storeID), nil, nil, &store, http.StatusOK)
	if err != nil {
		return Store{}, fmt.Errorf("get OpenFGA store %q: %w", storeID, err)
	}
	if !safeOpaqueIdentifier(store.ID) {
		return Store{}, fmt.Errorf("get OpenFGA store %q: response has missing or unsafe id", storeID)
	}
	if store.ID != storeID {
		return Store{}, fmt.Errorf("get OpenFGA store %q: response id is %q", storeID, store.ID)
	}
	return store, nil
}

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
			if !safeOpaqueIdentifier(model.ID) {
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

func (client *Client) GetAuthorizationModel(ctx context.Context, storeID, modelID string) (AuthorizationModel, error) {
	var response struct {
		Model AuthorizationModel `json:"authorization_model"`
	}
	endpoint := client.endpoint("stores", storeID, "authorization-models", modelID)
	if err := client.doJSON(ctx, http.MethodGet, endpoint, nil, nil, &response, http.StatusOK); err != nil {
		return AuthorizationModel{}, fmt.Errorf("get authorization model %q in store %q: %w", modelID, storeID, err)
	}
	if !safeOpaqueIdentifier(response.Model.ID) {
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
	if !safeOpaqueIdentifier(response.ID) {
		return ModelRecord{}, fmt.Errorf("write authorization model in store %q: response has missing or unsafe authorization_model_id", storeID)
	}
	model.ID = response.ID
	return ModelRecord{ID: response.ID, Model: model}, nil
}

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
		return fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()
	data, truncated, err := readBounded(response.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
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
		return fmt.Errorf("%w: response exceeds %s bytes", errMalformedHTTPResponse, strconv.Itoa(maxResponseBody))
	}
	if output != nil {
		mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			return fmt.Errorf("%w: response content type must be application/json", errMalformedHTTPResponse)
		}
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("%w: decode response: %w", errMalformedHTTPResponse, err)
		}
	}
	return nil
}

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

func cloneURL(value *url.URL) *url.URL {
	clone := *value
	return &clone
}

func containsStatus(statuses []int, status int) bool {
	for _, accepted := range statuses {
		if accepted == status {
			return true
		}
	}
	return false
}

func redact(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}
