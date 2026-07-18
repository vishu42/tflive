package openfga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authz"
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

func TestAuthorizationAdapterListAccessibleStacksWithConfiguredModel(t *testing.T) {
	adapter, requests := testAuthorizationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/stores/store-id/list-objects" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			AuthorizationModelID string `json:"authorization_model_id"`
			Type                 string `json:"type"`
			Relation             string `json:"relation"`
			User                 string `json:"user"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.AuthorizationModelID != "model-id" || body.Type != "stack" || body.Relation != "can_view" || body.User != "user:alice" {
			t.Fatalf("body = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"objects":["stack:one","stack:two"]}`)
	})

	result, err := adapter.ListAccessibleStacks(context.Background(), authz.ListAccessibleStacksRequest{Subject: mustSubject(t, "alice"), Permission: authz.PermissionView})
	want := authz.ListAccessibleStacksResult{Stacks: []authz.Stack{mustStack(t, "one"), mustStack(t, "two")}}
	if err != nil || !reflect.DeepEqual(result, want) || *requests != 1 {
		t.Fatalf("ListAccessibleStacks() = %#v, %v", result, err)
	}
}

func TestAuthorizationAdapterRejectsInvalidListObjects(t *testing.T) {
	for _, body := range []string{`{"objects":["stack:"]}`, `{"objects":["user:alice"]}`, `{"objects":["stack:one","stack:one"]}`, `{`, `{}`} {
		adapter := adapterForResponse(t, http.StatusOK, "application/json", body)
		_, err := adapter.ListAccessibleStacks(context.Background(), authz.ListAccessibleStacksRequest{Subject: mustSubject(t, "alice"), Permission: authz.PermissionView})
		if !errors.Is(err, authz.ErrMalformedResponse) {
			t.Fatalf("body %q error = %v", body, err)
		}
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

	result, err := adapter.ListGrants(context.Background(), authz.ListGrantsRequest{Stack: mustStack(t, "one")})
	want := authz.ListGrantsResult{Grants: []authz.Grant{
		mustGrant(t, "alice", "one", authz.RoleOwner),
		mustGrant(t, "bob", "one", authz.RoleViewer),
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
		_, err := adapter.ListGrants(context.Background(), authz.ListGrantsRequest{Stack: mustStack(t, "one")})
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

	_, err := adapter.ListGrants(context.Background(), authz.ListGrantsRequest{Stack: mustStack(t, "one")})
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
		grants[index] = mustGrant(t, fmt.Sprintf("user-%d", index), "one", authz.RoleOwner)
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
			TupleKeys []tupleKey `json:"tuple_keys"`
		} `json:"writes"`
		Deletes *struct {
			TupleKeys []tupleKey `json:"tuple_keys"`
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
	var tuples []tupleKey
	if expectedWrite {
		tuples = body.Writes.TupleKeys
	} else {
		tuples = body.Deletes.TupleKeys
	}
	if !reflect.DeepEqual(tuples, []tupleKey{{User: "user:alice", Relation: "owner", Object: "stack:one"}}) {
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
			TupleKey      tupleKey `json:"tuple_key"`
			CorrelationID string   `json:"correlation_id"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AuthorizationModelID != "model-id" || body.Consistency != "HIGHER_CONSISTENCY" || len(body.Checks) != 1 || body.Checks[0].CorrelationID != "0" || body.Checks[0].TupleKey != (tupleKey{User: "user:alice", Relation: "owner", Object: "stack:one"}) {
		t.Fatalf("body = %#v", body)
	}
}

func ownerGrant(t *testing.T) authz.Grant {
	t.Helper()
	return mustGrant(t, "alice", "one", authz.RoleOwner)
}

func mutationForGrant(t *testing.T, grant authz.Grant, confirm bool) authz.Mutation {
	t.Helper()
	mutation, err := authz.NewMutation([]authz.Grant{grant}, confirm)
	if err != nil {
		t.Fatal(err)
	}
	return mutation
}

func testAuthorizationAdapter(t *testing.T, handler http.HandlerFunc) (*AuthorizationAdapter, *int) {
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
	adapter, err := NewAuthorizationAdapter(Config{APIURL: parsed, StoreID: "store-id", ModelID: "model-id", HTTPTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, requests
}

func adapterForResponse(t *testing.T, status int, contentType, body string) *AuthorizationAdapter {
	t.Helper()
	adapter, _ := testAuthorizationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	})
	return adapter
}

func adapterForHandler(t *testing.T, handler http.HandlerFunc) *AuthorizationAdapter {
	t.Helper()
	adapter, _ := testAuthorizationAdapter(t, handler)
	return adapter
}

func viewCheck(t *testing.T) authz.CheckRequest {
	t.Helper()
	user, err := authz.SubjectFromKeycloakSub("alice")
	if err != nil {
		t.Fatal(err)
	}
	stack, err := authz.StackFromID("one")
	if err != nil {
		t.Fatal(err)
	}
	return authz.CheckRequest{Subject: user, Stack: stack, Permission: authz.PermissionView}
}

func operateCheck(t *testing.T) authz.CheckRequest {
	t.Helper()
	check := viewCheck(t)
	check.Permission = authz.PermissionOperate
	return check
}

func mustSubject(t *testing.T, sub string) authz.Subject {
	t.Helper()
	subject, err := authz.SubjectFromKeycloakSub(sub)
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

func mustStack(t *testing.T, id string) authz.Stack {
	t.Helper()
	stack, err := authz.StackFromID(id)
	if err != nil {
		t.Fatal(err)
	}
	return stack
}

func mustGrant(t *testing.T, subject, stack string, role authz.Role) authz.Grant {
	t.Helper()
	grant, err := authz.NewGrant(mustSubject(t, subject), mustStack(t, stack), role)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}
