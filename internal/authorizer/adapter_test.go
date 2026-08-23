package authorizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/openfga"
)

func TestAuthorizationAdapterCheckUsesConfiguredModelAndReturnsDecision(t *testing.T) {
	adapter, requests := testAuthorizationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/stores/store-id/check" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			AuthorizationModelID string `json:"authorization_model_id"`
			TupleKey             struct {
				User     string `json:"user"`
				Relation string `json:"relation"`
				Object   string `json:"object"`
			} `json:"tuple_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.AuthorizationModelID != "model-id" || body.TupleKey.User != "user:alice" || body.TupleKey.Relation != "can_view" || body.TupleKey.Object != "stack:one" {
			t.Fatalf("body = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"allowed":true}`)
	})

	result, err := adapter.Check(context.Background(), viewCheck(t))
	if err != nil || !result.Allowed || *requests != 1 {
		t.Fatalf("Check() = %#v, %v", result, err)
	}
}

func TestAuthorizationAdapterCheckDistinguishesDenialAndFailures(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
		want        error
		allowed     bool
	}{
		{name: "denied", contentType: "application/json", body: `{"allowed":false}`, status: http.StatusOK, allowed: false},
		{name: "unavailable", contentType: "application/json", body: `{}`, status: http.StatusServiceUnavailable, want: authz.ErrUnavailable},
		{name: "rate limited", contentType: "application/json", body: `{}`, status: http.StatusTooManyRequests, want: authz.ErrUnavailable},
		{name: "wrong media type", contentType: "text/plain", body: `{"allowed":true}`, status: http.StatusOK, want: authz.ErrMalformedResponse},
		{name: "invalid JSON", contentType: "application/json", body: `{`, status: http.StatusOK, want: authz.ErrMalformedResponse},
		{name: "missing allowed", contentType: "application/json", body: `{}`, status: http.StatusOK, want: authz.ErrMalformedResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := adapterForResponse(t, test.status, test.contentType, test.body)
			result, err := adapter.Check(context.Background(), viewCheck(t))
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("Check() error = %v, want %v", err, test.want)
				}
				return
			}
			if err != nil || result.Allowed != test.allowed {
				t.Fatalf("Check() = %#v, %v", result, err)
			}
		})
	}
}

func TestAuthorizationAdapterCheckDeadlineMapsToTimeoutHTTPStatus(t *testing.T) {
	adapter := adapterForTransport(t, 10*time.Millisecond, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	}))

	_, err := adapter.Check(context.Background(), viewCheck(t))
	if !errors.Is(err, authz.ErrTimeout) {
		t.Fatalf("Check() error = %v, want ErrTimeout", err)
	}
	status, code, ok := authz.HTTPStatus(err)
	if !ok || status != http.StatusServiceUnavailable || code != "authorization_unavailable" {
		t.Fatalf("HTTPStatus() = %d, %q, %t", status, code, ok)
	}
}

func TestAuthorizationAdapterCheckClassifiesTransportAndBodyReadFailuresAsUnavailable(t *testing.T) {
	tests := []struct {
		name      string
		transport http.RoundTripper
	}{
		{
			name: "transport",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial OpenFGA")
			}),
		},
		{
			name: "body read",
			transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(errorReader{}),
					Request:    request,
				}, nil
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := adapterForTransport(t, time.Second, test.transport)

			result, err := adapter.Check(context.Background(), viewCheck(t))
			if !errors.Is(err, authz.ErrUnavailable) {
				t.Fatalf("Check() error = %v, want ErrUnavailable", err)
			}
			if result.Allowed {
				t.Fatal("dependency failure returned an allow decision")
			}
		})
	}
}

func TestAuthorizationAdapterCheckDoesNotLeakUpstreamHTTPResponse(t *testing.T) {
	for _, test := range []struct {
		status int
		want   error
	}{
		{status: http.StatusBadRequest, want: authz.ErrMalformedResponse},
		{status: http.StatusUnauthorized, want: authz.ErrMalformedResponse},
		{status: http.StatusForbidden, want: authz.ErrMalformedResponse},
		{status: http.StatusNotFound, want: authz.ErrMalformedResponse},
		{status: http.StatusTooManyRequests, want: authz.ErrUnavailable},
		{status: http.StatusInternalServerError, want: authz.ErrUnavailable},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			const body = "upstream OpenFGA detail that must not cross the authorization port"
			adapter := adapterForResponse(t, test.status, "application/json", body)

			_, err := adapter.Check(context.Background(), viewCheck(t))
			if !errors.Is(err, test.want) {
				t.Fatalf("Check() error = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), body) || strings.Contains(err.Error(), fmt.Sprintf("%d %s", test.status, http.StatusText(test.status))) {
				t.Fatalf("Check() leaked upstream response: %v", err)
			}
		})
	}
}

func TestNewAuthorizationAdapterRejectsNilAPIURL(t *testing.T) {
	_, err := New(openfga.Config{StoreID: "store-id", ModelID: "model-id"})
	if err == nil || !strings.Contains(err.Error(), "API URL") {
		t.Fatalf("New() error = %v, want API URL validation error", err)
	}
}

func TestAuthorizationAdapterBatchCheckUsesStableCorrelationsAndReturnsInputOrder(t *testing.T) {
	adapter, requests := testAuthorizationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/stores/store-id/batch-check" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			AuthorizationModelID string `json:"authorization_model_id"`
			Checks               []struct {
				CorrelationID string `json:"correlation_id"`
				TupleKey      struct {
					Relation string `json:"relation"`
				} `json:"tuple_key"`
			} `json:"checks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.AuthorizationModelID != "model-id" || len(body.Checks) != 2 || body.Checks[0].CorrelationID != "0" || body.Checks[1].CorrelationID != "1" || body.Checks[0].TupleKey.Relation != "can_view" || body.Checks[1].TupleKey.Relation != "can_operate" {
			t.Fatalf("body = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"result":{"1":{"allowed":false},"0":{"allowed":true}}}`)
	})

	result, err := adapter.BatchCheck(context.Background(), authz.BatchCheckRequest{Checks: []authz.CheckRequest{viewCheck(t), operateCheck(t)}})
	if err != nil || len(result.Results) != 2 || !result.Results[0].Allowed || result.Results[1].Allowed || *requests != 1 {
		t.Fatalf("BatchCheck() = %#v, %v", result, err)
	}
}

func TestAuthorizationAdapterBatchCheckRejectsMissingOrUnknownCorrelationResults(t *testing.T) {
	for _, body := range []string{`{"result":{"0":{"allowed":true}}}`, `{"result":{"0":{"allowed":true},"1":{"allowed":false},"extra":{"allowed":true}}}`} {
		adapter := adapterForResponse(t, http.StatusOK, "application/json", body)
		_, err := adapter.BatchCheck(context.Background(), authz.BatchCheckRequest{Checks: []authz.CheckRequest{viewCheck(t), operateCheck(t)}})
		if !errors.Is(err, authz.ErrMalformedResponse) {
			t.Fatalf("BatchCheck() error = %v", err)
		}
	}
}

func TestAuthorizationAdapterRejectsInvalidRequests(t *testing.T) {
	adapter := adapterForResponse(t, http.StatusOK, "application/json", `{"allowed":true}`)
	if _, err := adapter.Check(context.Background(), authz.CheckRequest{}); !errors.Is(err, authz.ErrInvalidInput) {
		t.Fatalf("Check() error = %v", err)
	}
	if _, err := adapter.BatchCheck(context.Background(), authz.BatchCheckRequest{}); !errors.Is(err, authz.ErrInvalidInput) {
		t.Fatalf("BatchCheck() error = %v", err)
	}
}

func TestAuthorizationAdapterListsOnlyDirectRoleGrantsAcrossPages(t *testing.T) {
	var requests *int
	adapter, requests := testAuthorizationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/stores/store-id/read" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			TupleKey struct {
				Object string `json:"object"`
			} `json:"tuple_key"`
			PageSize          int    `json:"page_size"`
			ContinuationToken string `json:"continuation_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.TupleKey.Object != "stack:one" || body.PageSize != 100 {
			t.Fatalf("body = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		switch *requests {
		case 1:
			if body.ContinuationToken != "" {
				t.Fatalf("first continuation token = %q", body.ContinuationToken)
			}
			fmt.Fprint(w, `{"tuples":[{"key":{"user":"user:bob","relation":"viewer","object":"stack:one"}}],"continuation_token":"next"}`)
		case 2:
			if body.ContinuationToken != "next" {
				t.Fatalf("second continuation token = %q", body.ContinuationToken)
			}
			fmt.Fprint(w, `{"tuples":[{"key":{"user":"user:alice","relation":"owner","object":"stack:one"}}]}`)
		default:
			t.Fatalf("unexpected request count %d", *requests)
		}
	})

	result, err := adapter.ListGrants(context.Background(), authz.ListGrantsRequest{Object: mustStack(t, "one")})
	want := authz.ListGrantsResult{Grants: []authz.Grant{
		mustGrant(t, "alice", "one", authz.RelationOwner),
		mustGrant(t, "bob", "one", authz.RelationViewer),
	}}
	if err != nil || !reflect.DeepEqual(result, want) || *requests != 2 {
		t.Fatalf("ListGrants() = %#v, %v", result, err)
	}
}

func TestAuthorizationAdapterRejectsMalformedGrantReadPages(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"tuples":[{"key":{"user":"user:alice","relation":"can_view","object":"stack:one"}}]}`,
		`{"tuples":[{"key":{"user":"user:alice","relation":"owner","object":"stack:other"}}]}`,
		`{"tuples":[{"key":{"user":"user:alice","relation":"owner","object":"stack:one"}},{"key":{"user":"user:alice","relation":"owner","object":"stack:one"}}]}`,
	} {
		adapter := adapterForResponse(t, http.StatusOK, "application/json", body)
		_, err := adapter.ListGrants(context.Background(), authz.ListGrantsRequest{Object: mustStack(t, "one")})
		if !errors.Is(err, authz.ErrMalformedResponse) {
			t.Fatalf("body %q error = %v", body, err)
		}
	}
}

func TestAuthorizationAdapterRejectsRepeatedGrantReadTokens(t *testing.T) {
	var requests *int
	adapter, requests := testAuthorizationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/stores/store-id/read" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch *requests {
		case 1, 2:
			fmt.Fprint(w, `{"tuples":[],"continuation_token":"next"}`)
		case 3:
			fmt.Fprint(w, `{"tuples":[]}`)
		default:
			t.Fatalf("unexpected request count %d", *requests)
		}
	})

	_, err := adapter.ListGrants(context.Background(), authz.ListGrantsRequest{Object: mustStack(t, "one")})
	if !errors.Is(err, authz.ErrMalformedResponse) {
		t.Fatalf("ListGrants() error = %v", err)
	}
}

func TestAuthorizationAdapterRelationshipWritesAreIdempotent(t *testing.T) {
	adapter := adapterForHandler(t, duplicateWriteThenConfirmedHandler(t, true))
	if err := adapter.WriteRelationships(context.Background(), mutationForGrant(t, ownerGrant(t), false)); err != nil {
		t.Fatalf("WriteRelationships() error = %v", err)
	}
}

func TestAuthorizationAdapterRelationshipDeletesAreIdempotent(t *testing.T) {
	adapter := adapterForHandler(t, duplicateWriteThenConfirmedHandler(t, false))
	if err := adapter.DeleteRelationships(context.Background(), mutationForGrant(t, ownerGrant(t), false)); err != nil {
		t.Fatalf("DeleteRelationships() error = %v", err)
	}
}

func TestAuthorizationAdapterRejectedWriteWithUnconfirmedStateFailsClosed(t *testing.T) {
	adapter := adapterForHandler(t, rejectedMutationThenConfirmedHandler(t, true, false))
	err := adapter.WriteRelationships(context.Background(), mutationForGrant(t, ownerGrant(t), false))
	if !errors.Is(err, authz.ErrWriteUnconfirmed) {
		t.Fatalf("WriteRelationships() error = %v, want ErrWriteUnconfirmed", err)
	}
}

func TestAuthorizationAdapterRejectedDeleteWithUnconfirmedStateFailsClosed(t *testing.T) {
	adapter := adapterForHandler(t, rejectedMutationThenConfirmedHandler(t, false, true))
	err := adapter.DeleteRelationships(context.Background(), mutationForGrant(t, ownerGrant(t), false))
	if !errors.Is(err, authz.ErrWriteUnconfirmed) {
		t.Fatalf("DeleteRelationships() error = %v, want ErrWriteUnconfirmed", err)
	}
}

func TestAuthorizationAdapterConfirmationUsesHigherConsistency(t *testing.T) {
	adapter := adapterForHandler(t, confirmedWriteHandler(t, true))
	if err := adapter.WriteRelationships(context.Background(), mutationForGrant(t, ownerGrant(t), true)); err != nil {
		t.Fatalf("WriteRelationships() error = %v", err)
	}
}

func TestAuthorizationAdapterUnconfirmedAndInvalidMutationsFailClosed(t *testing.T) {
	adapter := adapterForHandler(t, confirmedWriteHandler(t, false))
	err := adapter.WriteRelationships(context.Background(), mutationForGrant(t, ownerGrant(t), true))
	if !errors.Is(err, authz.ErrWriteUnconfirmed) {
		t.Fatalf("unconfirmed write error = %v", err)
	}

	if err := adapter.WriteRelationships(context.Background(), authz.Mutation{}); !errors.Is(err, authz.ErrInvalidInput) {
		t.Fatalf("invalid mutation error = %v", err)
	}
}

func TestAuthorizationAdapterRejectsDuplicateMutationGrants(t *testing.T) {
	grant := ownerGrant(t)
	mutation, err := authz.NewMutation([]authz.Grant{grant, grant}, false)
	if err != nil {
		t.Fatal(err)
	}
	adapter := adapterForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	if err := adapter.WriteRelationships(context.Background(), mutation); !errors.Is(err, authz.ErrInvalidInput) {
		t.Fatalf("duplicate mutation error = %v", err)
	}
}

func TestAuthorizationAdapterBoundsConfirmationBatchChecks(t *testing.T) {
	const maxChecksPerRequest = 25
	grants := make([]authz.Grant, maxChecksPerRequest+1)
	for index := range grants {
		grants[index] = mustGrant(t, fmt.Sprintf("user-%d", index), "one", authz.RelationOwner)
	}
	mutation, err := authz.NewMutation(grants, true)
	if err != nil {
		t.Fatal(err)
	}

	confirmationRequests := 0
	adapter := adapterForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stores/store-id/write":
			w.WriteHeader(http.StatusOK)
		case "/stores/store-id/batch-check":
			var body struct {
				Consistency string `json:"consistency"`
				Checks      []struct {
					CorrelationID string `json:"correlation_id"`
				} `json:"checks"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Consistency != "HIGHER_CONSISTENCY" || len(body.Checks) == 0 || len(body.Checks) > maxChecksPerRequest {
				t.Fatalf("body = %#v", body)
			}
			confirmationRequests++
			result := make(map[string]map[string]bool, len(body.Checks))
			for _, check := range body.Checks {
				result[check.CorrelationID] = map[string]bool{"allowed": true}
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{"result": result}); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	})

	if err := adapter.WriteRelationships(context.Background(), mutation); err != nil {
		t.Fatalf("WriteRelationships() error = %v", err)
	}
	if confirmationRequests != 2 {
		t.Fatalf("confirmation requests = %d, want 2", confirmationRequests)
	}
}

func duplicateWriteThenConfirmedHandler(t *testing.T, expected bool) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stores/store-id/write":
			assertRelationshipWrite(t, r, expected)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{}`)
		case "/stores/store-id/batch-check":
			assertRelationshipConfirmation(t, r)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"result":{"0":{"allowed":%t}}}`, expected)
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}
}

func rejectedMutationThenConfirmedHandler(t *testing.T, expectedWrite, confirmed bool) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stores/store-id/write":
			assertRelationshipWrite(t, r, expectedWrite)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{}`)
		case "/stores/store-id/batch-check":
			assertRelationshipConfirmation(t, r)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"result":{"0":{"allowed":%t}}}`, confirmed)
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}
}

func confirmedWriteHandler(t *testing.T, confirmed bool) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stores/store-id/write":
			assertRelationshipWrite(t, r, true)
			w.WriteHeader(http.StatusOK)
		case "/stores/store-id/batch-check":
			assertRelationshipConfirmation(t, r)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"result":{"0":{"allowed":%t}}}`, confirmed)
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}
}

func assertRelationshipWrite(t *testing.T, r *http.Request, expectedWrite bool) {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Fatalf("method = %s", r.Method)
	}
	var body struct {
		AuthorizationModelID string `json:"authorization_model_id"`
		Writes               *struct {
			TupleKeys []openfga.TupleKey `json:"tuple_keys"`
		} `json:"writes"`
		Deletes *struct {
			TupleKeys []openfga.TupleKey `json:"tuple_keys"`
		} `json:"deletes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AuthorizationModelID != "model-id" {
		t.Fatalf("model = %q", body.AuthorizationModelID)
	}
	if expectedWrite && (body.Writes == nil || body.Deletes != nil) {
		t.Fatalf("body = %#v", body)
	}
	if !expectedWrite && (body.Deletes == nil || body.Writes != nil) {
		t.Fatalf("body = %#v", body)
	}
	var tuples []openfga.TupleKey
	if expectedWrite {
		tuples = body.Writes.TupleKeys
	} else {
		tuples = body.Deletes.TupleKeys
	}
	if !reflect.DeepEqual(tuples, []openfga.TupleKey{{User: "user:alice", Relation: "owner", Object: "stack:one"}}) {
		t.Fatalf("tuple keys = %#v", tuples)
	}
}

func assertRelationshipConfirmation(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Fatalf("method = %s", r.Method)
	}
	var body struct {
		AuthorizationModelID string `json:"authorization_model_id"`
		Consistency          string `json:"consistency"`
		Checks               []struct {
			TupleKey      openfga.TupleKey `json:"tuple_key"`
			CorrelationID string           `json:"correlation_id"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AuthorizationModelID != "model-id" || body.Consistency != "HIGHER_CONSISTENCY" || len(body.Checks) != 1 || body.Checks[0].CorrelationID != "0" || body.Checks[0].TupleKey != (openfga.TupleKey{User: "user:alice", Relation: "owner", Object: "stack:one"}) {
		t.Fatalf("body = %#v", body)
	}
}

func ownerGrant(t *testing.T) authz.Grant {
	t.Helper()
	return mustGrant(t, "alice", "one", authz.RelationOwner)
}

func mutationForGrant(t *testing.T, grant authz.Grant, confirm bool) authz.Mutation {
	t.Helper()
	mutation, err := authz.NewMutation([]authz.Grant{grant}, confirm)
	if err != nil {
		t.Fatal(err)
	}
	return mutation
}

func testAuthorizationAdapter(t *testing.T, handler http.HandlerFunc) (*Adapter, *int) {
	t.Helper()
	requests := new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests++
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(openfga.Config{APIURL: parsed, StoreID: "store-id", ModelID: "model-id", HTTPTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, requests
}

func adapterForResponse(t *testing.T, status int, contentType, body string) *Adapter {
	t.Helper()
	adapter, _ := testAuthorizationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	})
	return adapter
}

func adapterForHandler(t *testing.T, handler http.HandlerFunc) *Adapter {
	t.Helper()
	adapter, _ := testAuthorizationAdapter(t, handler)
	return adapter
}

// adapterForTransport builds an adapter whose requests are served by transport
// instead of a live test server, so transport and body-read failures can be
// simulated without reaching into the client.
func adapterForTransport(t *testing.T, timeout time.Duration, transport http.RoundTripper) *Adapter {
	t.Helper()
	parsed, err := url.Parse("http://openfga.invalid")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(openfga.Config{APIURL: parsed, StoreID: "store-id", ModelID: "model-id", HTTPTimeout: timeout, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("read OpenFGA response")
}

func viewCheck(t *testing.T) authz.CheckRequest {
	t.Helper()
	return authz.CheckRequest{Subject: mustSubject(t, "alice"), Object: mustStack(t, "one"), Relation: authz.RelationCanView}
}

func operateCheck(t *testing.T) authz.CheckRequest {
	t.Helper()
	check := viewCheck(t)
	check.Relation = authz.RelationCanOperate
	return check
}

// mustSubject builds a Subject or fails the test.
//
//	mustSubject(t, "alice")  → Subject{"user:alice"}
//	mustSubject(t, "a*b")    → t.Fatal
func mustSubject(t *testing.T, sub string) authz.Subject {
	t.Helper()
	subject, err := authz.SubjectFromOIDCSub(sub)
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

// mustObject builds an Object of any type, or fails the test.
//
//	mustObject(t, authz.TypeStack, "one")  → Object{"stack:one"}
//	mustObject(t, authz.TypeUser, "alice") → Object{"user:alice"}
func mustObject(t *testing.T, objectType authz.ObjectType, id string) authz.Object {
	t.Helper()
	object, err := authz.ObjectFromID(objectType, id)
	if err != nil {
		t.Fatal(err)
	}
	return object
}

// mustStack is mustObject fixed to TypeStack, which most tests want.
//
//	mustStack(t, "one")  → Object{"stack:one"}
func mustStack(t *testing.T, id string) authz.Object {
	t.Helper()
	return mustObject(t, authz.TypeStack, id)
}

// mustGrant builds a Grant from bare IDs, or fails the test.
//
//	mustGrant(t, "alice", "one", authz.RelationOwner)
//	  → Grant{user:alice, stack:one, owner}
//	mustGrant(t, "alice", "one", authz.RelationCanView)  → t.Fatal (not grantable)
func mustGrant(t *testing.T, subject, stack string, relation authz.Relation) authz.Grant {
	t.Helper()
	grant, err := authz.NewGrant(mustSubject(t, subject), mustStack(t, stack), relation)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

// Pins that a parent edge read back from OpenFGA is refused rather than
// surfacing as a grant. Inert until #141 writes the first one.
func TestListGrantsRejectsAStructuralTuple(t *testing.T) {
	t.Parallel()

	// Two distinct reasons a parent edge must never surface as a Grant. A
	// platform subject is refused by the subject-prefix guard before
	// GrantRelation is ever reached. A well-formed user subject reaches
	// GrantRelation, which refuses "parent" directly — and even if it
	// admitted it, authz.NewGrant's own Grantable() check would still
	// refuse the resulting Grant, so this case is defended twice over.
	// Both must independently produce ErrMalformedResponse out of
	// ListGrants.
	tests := []struct {
		name string
		body string
	}{
		{
			name: "well-formed user subject carrying a structural relation is refused",
			body: `{"tuples":[{"key":{"user":"user:alice","relation":"parent","object":"stack:one"}}],"continuation_token":""}`,
		},
		{
			name: "platform subject relies on the subject-prefix guard",
			body: `{"tuples":[{"key":{"user":"platform:tflive","relation":"parent","object":"stack:one"}}],"continuation_token":""}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			adapter := adapterForHandler(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				// A parent edge is a legal tuple on the wire; it is not a grant.
				fmt.Fprint(w, test.body)
			})

			_, err := adapter.ListGrants(context.Background(), authz.ListGrantsRequest{Object: mustStack(t, "one")})
			if !errors.Is(err, authz.ErrMalformedResponse) {
				t.Fatalf("ListGrants() error = %v, want ErrMalformedResponse", err)
			}
		})
	}
}

func TestAuthorizationAdapterListSubjectGrantsFiltersByUserAndObject(t *testing.T) {
	adapter, requests := testAuthorizationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/stores/store-id/read" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			TupleKey struct {
				User   string `json:"user"`
				Object string `json:"object"`
			} `json:"tuple_key"`
			PageSize int `json:"page_size"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.TupleKey.User != "user:alice" || body.TupleKey.Object != "stack:one" || body.PageSize != 100 {
			t.Fatalf("body = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tuples":[{"key":{"user":"user:alice","relation":"owner","object":"stack:one"}}]}`)
	})

	result, err := adapter.ListSubjectGrants(context.Background(), authz.ListSubjectGrantsRequest{
		Subject: mustSubject(t, "alice"),
		Object:  mustStack(t, "one"),
	})
	want := authz.ListGrantsResult{Grants: []authz.Grant{mustGrant(t, "alice", "one", authz.RelationOwner)}}
	if err != nil || !reflect.DeepEqual(result, want) || *requests != 1 {
		t.Fatalf("ListSubjectGrants() = %#v, %v (requests %d)", result, err, *requests)
	}
}

func TestAuthorizationAdapterListSubjectGrantsRejectsOtherSubjectsAndInvalidRequests(t *testing.T) {
	adapter, _ := testAuthorizationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tuples":[{"key":{"user":"user:bob","relation":"owner","object":"stack:one"}}]}`)
	})

	if _, err := adapter.ListSubjectGrants(context.Background(), authz.ListSubjectGrantsRequest{
		Subject: mustSubject(t, "alice"),
		Object:  mustStack(t, "one"),
	}); !errors.Is(err, authz.ErrMalformedResponse) {
		t.Fatalf("ListSubjectGrants() error = %v, want ErrMalformedResponse", err)
	}

	if _, err := adapter.ListSubjectGrants(context.Background(), authz.ListSubjectGrantsRequest{}); !errors.Is(err, authz.ErrInvalidInput) {
		t.Fatalf("ListSubjectGrants() error = %v, want ErrInvalidInput", err)
	}
}

// batchCheckWantAllowed returns the deterministic decision the fake
// batch-check handler in TestBatchCheckChunksAtFiftyAndPreservesOrder
// answers for check index, and what the test asserts the merged result
// holds at that index. It follows the parity of index's population count
// (the Thue-Morse sequence) rather than any fixed-period pattern like
// index%10, because a period that divides the 50-check chunk size would go
// unnoticed by a merge bug that misaligns a chunk boundary by exactly that
// period — the old %10 pattern could not distinguish index i from i+10,
// i+20, ... within the first, 50-wide chunk.
//
//	batchCheckWantAllowed(0)   → false  (popcount 0, even)
//	batchCheckWantAllowed(1)   → true   (popcount 1, odd)
//	batchCheckWantAllowed(50)  → true   (popcount 3, odd; the lone check in the second chunk)
func batchCheckWantAllowed(index int) bool {
	return bits.OnesCount(uint(index))%2 == 1
}

// Pins #220. Ordering is asserted as well as chunking, because a merge that
// loses the caller's positions silently returns another stack's answer. The
// expected pattern (batchCheckWantAllowed) is per-index rather than
// periodic so a merge offset wrong by any multiple of the chunk size's
// divisors cannot alias with the correct answer. The batch sizes are
// asserted exactly ([50, 1]), not just bounded by 50, so a chunker that
// split unevenly would also be caught.
func TestBatchCheckChunksAtFiftyAndPreservesOrder(t *testing.T) {
	t.Parallel()

	const total = 51
	var mu sync.Mutex
	var batchSizes []int

	adapter := adapterForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Checks []struct {
				TupleKey      openfga.TupleKey `json:"tuple_key"`
				CorrelationID string           `json:"correlation_id"`
			} `json:"checks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode batch-check body: %v", err)
		}
		mu.Lock()
		batchSizes = append(batchSizes, len(body.Checks))
		mu.Unlock()

		// Each check's object encodes its own index (mustStack(t,
		// fmt.Sprintf("stack-%d", i)) below), so the handler recovers that
		// index and answers via batchCheckWantAllowed — the same function
		// the assertion below uses, so a misordered merge produces a
		// visibly wrong result rather than a coincidentally passing one.
		results := map[string]any{}
		for _, check := range body.Checks {
			suffix := strings.TrimPrefix(check.TupleKey.Object, "stack:stack-")
			index, err := strconv.Atoi(suffix)
			if err != nil {
				t.Errorf("object %q does not encode a check index: %v", check.TupleKey.Object, err)
				continue
			}
			results[check.CorrelationID] = map[string]any{"allowed": batchCheckWantAllowed(index)}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": results})
	})

	checks := make([]authz.CheckRequest, total)
	for i := range checks {
		checks[i] = authz.CheckRequest{
			Subject:  mustSubject(t, "alice"),
			Relation: authz.RelationCanView,
			Object:   mustStack(t, fmt.Sprintf("stack-%d", i)),
		}
	}

	result, err := adapter.BatchCheck(context.Background(), authz.BatchCheckRequest{Checks: checks})
	if err != nil {
		t.Fatalf("BatchCheck() error = %v", err)
	}
	if len(result.Results) != total {
		t.Fatalf("len(Results) = %d, want %d", len(result.Results), total)
	}

	mu.Lock()
	defer mu.Unlock()
	if want := []int{50, 1}; !reflect.DeepEqual(batchSizes, want) {
		t.Fatalf("upstream batch sizes = %v, want %v", batchSizes, want)
	}

	for i, decision := range result.Results {
		wantAllowed := batchCheckWantAllowed(i)
		if decision.Allowed != wantAllowed {
			t.Fatalf("Results[%d].Allowed = %t, want %t (ordering lost across chunks)", i, decision.Allowed, wantAllowed)
		}
	}
}

// Pins that a chunk failing after an earlier chunk already succeeded returns
// ErrUnavailable and the zero BatchCheckResult — not the `result` variable
// partially filled by the chunk(s) that did succeed. Every error return in
// BatchCheck already says authz.BatchCheckResult{}, never `result`, but the
// only multi-chunk test before this one was the happy path, so a change that
// swapped one of those returns to `result` would have shipped undetected.
func TestBatchCheckReturnsZeroResultWhenALaterChunkFails(t *testing.T) {
	t.Parallel()

	const total = 51
	var mu sync.Mutex
	requestCount := 0

	adapter := adapterForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		n := requestCount
		mu.Unlock()

		if n == 1 {
			var body struct {
				Checks []struct {
					CorrelationID string `json:"correlation_id"`
				} `json:"checks"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode batch-check body: %v", err)
			}
			results := map[string]any{}
			for _, check := range body.Checks {
				results[check.CorrelationID] = map[string]any{"allowed": true}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"result": results})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})

	checks := make([]authz.CheckRequest, total)
	for i := range checks {
		checks[i] = authz.CheckRequest{
			Subject:  mustSubject(t, "alice"),
			Relation: authz.RelationCanView,
			Object:   mustStack(t, fmt.Sprintf("stack-%d", i)),
		}
	}

	result, err := adapter.BatchCheck(context.Background(), authz.BatchCheckRequest{Checks: checks})
	if !errors.Is(err, authz.ErrUnavailable) {
		t.Fatalf("BatchCheck() error = %v, want ErrUnavailable", err)
	}
	if !reflect.DeepEqual(result, authz.BatchCheckResult{}) {
		t.Fatalf("BatchCheck() result = %#v, want the zero value, not the first chunk's partial results", result)
	}
}
