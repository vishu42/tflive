package openfga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestClientListsStoresAcrossPagesAndAuthenticates(t *testing.T) {
	t.Parallel()

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("continuation_token") {
		case "":
			fmt.Fprint(w, `{"stores":[{"id":"store-1","name":"other"}],"continuation_token":"next"}`)
		case "next":
			fmt.Fprint(w, `{"stores":[{"id":"store-2","name":"tflive"}]}`)
		default:
			t.Fatalf("unexpected continuation token %q", r.URL.Query().Get("continuation_token"))
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL, "test-secret")
	stores, err := client.ListStores(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(stores) != 2 || stores[1].ID != "store-2" {
		t.Fatalf("calls = %d stores = %#v", calls, stores)
	}
}

func TestClientUsesExactEscapedStoreAndModelPaths(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.EscapedPath(), "/stores/store%2Fid/authorization-models/model%2Fid"; got != want {
			t.Fatalf("EscapedPath = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"authorization_model":{"id":"model/id","schema_version":"1.1","type_definitions":[{"type":"user"}]}}`)
	}))
	defer server.Close()

	client := testClient(t, server.URL, "")
	model, err := client.GetAuthorizationModel(context.Background(), "store/id", "model/id")
	if err != nil {
		t.Fatal(err)
	}
	if model.ID != "model/id" {
		t.Fatalf("model = %#v", model)
	}
}

func TestClientBoundsAndRedactsServerErrors(t *testing.T) {
	t.Parallel()

	secret := "server-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, strings.Repeat(secret, 10000), http.StatusInternalServerError)
	}))
	defer server.Close()

	client := testClient(t, server.URL, secret)
	_, err := client.GetStore(context.Background(), "store-id")
	if err == nil {
		t.Fatal("GetStore() error = nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked token: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") || len(err.Error()) > 70000 {
		t.Fatalf("error was not bounded and redacted: length=%d error=%v", len(err.Error()), err)
	}
	var statusError *HTTPStatusError
	if !errors.As(err, &statusError) || statusError.StatusCode != http.StatusInternalServerError {
		t.Fatalf("error = %T %[1]v, want HTTPStatusError", err)
	}
}

func TestClientBoundsErrorsAfterRedactionExpansion(t *testing.T) {
	t.Parallel()

	secret := "z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, strings.Repeat(secret, maxResponseBody+1), http.StatusInternalServerError)
	}))
	defer server.Close()

	client := testClient(t, server.URL, secret)
	_, err := client.GetStore(context.Background(), "store-id")
	if err == nil {
		t.Fatal("GetStore() error = nil")
	}
	message := err.Error()
	if strings.Contains(message, secret) {
		t.Fatal("error leaked token")
	}
	if !strings.Contains(message, "[REDACTED]") || !strings.Contains(message, "[TRUNCATED]") {
		t.Fatal("error did not preserve redaction and truncation markers")
	}
	if len(message) > 70000 {
		t.Fatalf("error was not bounded after redaction: length=%d", len(message))
	}
}

func TestClientRejectsMalformedJSONAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{")
	}))
	defer server.Close()

	client := testClient(t, server.URL, "")
	if _, err := client.ListStores(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("ListStores() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetStore(ctx, "store-id"); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("GetStore() error = %v", err)
	}
}

func testClient(t *testing.T, rawURL, token string) *Client {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return NewClient(Config{
		APIURL: parsed, APIToken: token, HTTPTimeout: time.Second,
	})
}

func TestClientAddsConfiguredRequestDeadline(t *testing.T) {
	client := testClient(t, "http://openfga.test", "")
	client.timeout = 75 * time.Millisecond
	client.http.Timeout = client.timeout
	var gotDeadline time.Time
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var ok bool
		gotDeadline, ok = request.Context().Deadline()
		if !ok {
			return nil, fmt.Errorf("request context has no deadline")
		}
		return storeResponse(request), nil
	})

	started := time.Now()
	if _, err := client.GetStore(context.Background(), "store-id"); err != nil {
		t.Fatal(err)
	}
	if !gotDeadline.After(started) || gotDeadline.After(started.Add(client.timeout+25*time.Millisecond)) {
		t.Fatalf("request deadline = %s, started = %s, timeout = %s", gotDeadline, started, client.timeout)
	}
}

func TestClientPreservesEarlierCallerDeadline(t *testing.T) {
	client := testClient(t, "http://openfga.test", "")
	client.timeout = time.Second
	client.http.Timeout = client.timeout
	var gotDeadline time.Time
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var ok bool
		gotDeadline, ok = request.Context().Deadline()
		if !ok {
			return nil, fmt.Errorf("request context has no deadline")
		}
		return storeResponse(request), nil
	})

	parentDeadline := time.Now().Add(100 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), parentDeadline)
	defer cancel()
	if _, err := client.GetStore(ctx, "store-id"); err != nil {
		t.Fatal(err)
	}
	if gotDeadline.After(parentDeadline.Add(5 * time.Millisecond)) {
		t.Fatalf("request deadline %s extended parent deadline %s", gotDeadline, parentDeadline)
	}
}

func TestClientDefaultsNonPositiveTimeout(t *testing.T) {
	parsed, err := url.Parse("http://openfga.test")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Config{APIURL: parsed})
	if client.timeout != defaultHTTPTimeout {
		t.Fatalf("client timeout = %s, want %s", client.timeout, defaultHTTPTimeout)
	}
	if client.http.Timeout != defaultHTTPTimeout {
		t.Fatalf("HTTP timeout = %s, want %s", client.http.Timeout, defaultHTTPTimeout)
	}
}

func TestClientDeadlineCancellationSurfaces(t *testing.T) {
	client := testClient(t, "http://openfga.test", "")
	client.timeout = 10 * time.Millisecond
	client.http.Timeout = client.timeout
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})

	_, err := client.GetStore(context.Background(), "store-id")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func storeResponse(request *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"store-id","name":"tflive"}`)),
		Request:    request,
	}
}

func TestClientStoreAndModelEndpointContracts(t *testing.T) {
	t.Parallel()

	model := AuthorizationModel{
		SchemaVersion:   "1.1",
		TypeDefinitions: []TypeDefinition{{Type: "user"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/stores":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request["name"] != "tflive" || r.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("create store request = %#v content-type = %q", request, r.Header.Get("Content-Type"))
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"store-id","name":"tflive"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/stores/store-id":
			fmt.Fprint(w, `{"id":"store-id","name":"tflive"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/stores/store-id/authorization-models":
			responseModel := model
			responseModel.ID = "model-id"
			json.NewEncoder(w).Encode(map[string]any{"authorization_models": []AuthorizationModel{responseModel}})
		case r.Method == http.MethodGet && r.URL.Path == "/stores/store-id/authorization-models/model-id":
			responseModel := model
			responseModel.ID = "model-id"
			json.NewEncoder(w).Encode(map[string]any{"authorization_model": responseModel})
		case r.Method == http.MethodPost && r.URL.Path == "/stores/store-id/authorization-models":
			var request AuthorizationModel
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.ID != "" || request.SchemaVersion != "1.1" {
				t.Fatalf("write model request = %#v", request)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"authorization_model_id":"new-model-id"}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.EscapedPath())
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL, "")
	store, err := client.CreateStore(context.Background(), "tflive")
	if err != nil || store.ID != "store-id" {
		t.Fatalf("CreateStore() = %#v, %v", store, err)
	}
	if _, err := client.GetStore(context.Background(), store.ID); err != nil {
		t.Fatal(err)
	}
	models, err := client.ListAuthorizationModels(context.Background(), store.ID)
	if err != nil || len(models) != 1 || models[0].ID != "model-id" {
		t.Fatalf("ListAuthorizationModels() = %#v, %v", models, err)
	}
	if _, err := client.GetAuthorizationModel(context.Background(), store.ID, "model-id"); err != nil {
		t.Fatal(err)
	}
	written, err := client.WriteAuthorizationModel(context.Background(), store.ID, model)
	if err != nil || written.ID != "new-model-id" {
		t.Fatalf("WriteAuthorizationModel() = %#v, %v", written, err)
	}
}

func TestClientRejectsRepeatedPaginationToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"stores":[],"continuation_token":"same"}`)
	}))
	defer server.Close()

	_, err := testClient(t, server.URL, "").ListStores(context.Background())
	if err == nil || !strings.Contains(err.Error(), "repeated continuation token") {
		t.Fatalf("ListStores() error = %v", err)
	}
}

func TestClientRejectsOversizedSuccessAndMissingIdentifiers(t *testing.T) {
	t.Parallel()

	t.Run("oversized success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, strings.Repeat("x", maxResponseBody+1))
		}))
		defer server.Close()
		_, err := testClient(t, server.URL, "").ListStores(context.Background())
		if err == nil || !strings.Contains(err.Error(), "response exceeds") {
			t.Fatalf("ListStores() error = %v", err)
		}
	})

	t.Run("missing created store id", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"name":"tflive"}`)
		}))
		defer server.Close()
		_, err := testClient(t, server.URL, "").CreateStore(context.Background(), "tflive")
		if err == nil || !strings.Contains(err.Error(), "missing or unsafe id") {
			t.Fatalf("CreateStore() error = %v", err)
		}
	})

	t.Run("wrong exact model id", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"authorization_model":{"id":"other-model","schema_version":"1.1","type_definitions":[{"type":"user"}]}}`)
		}))
		defer server.Close()
		_, err := testClient(t, server.URL, "").GetAuthorizationModel(context.Background(), "store-id", "model-id")
		if err == nil || !strings.Contains(err.Error(), `response id is "other-model"`) {
			t.Fatalf("GetAuthorizationModel() error = %v", err)
		}
	})

	t.Run("non JSON success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, `{"stores":[]}`)
		}))
		defer server.Close()
		_, err := testClient(t, server.URL, "").ListStores(context.Background())
		if err == nil || !strings.Contains(err.Error(), "content type must be application/json") {
			t.Fatalf("ListStores() error = %v", err)
		}
	})
}
