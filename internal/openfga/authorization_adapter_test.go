package openfga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
