package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/authz"
	"github.com/vishu42/tflive/internal/domain"
	"github.com/vishu42/tflive/internal/queue"
)

const apiKeycloakSubject = "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91"
const configuredTenantID = domain.TenantID("tenant_123")

func authenticatedRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	ctx := authn.ContextWithPrincipal(request.Context(), authn.Principal{Subject: apiKeycloakSubject})
	return request.WithContext(ctx)
}

func ordinaryAuthenticatedRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	ctx := authn.ContextWithPrincipal(request.Context(), authn.Principal{Subject: apiKeycloakSubject})
	return request.WithContext(ctx)
}

// platformTier is the capability set the model derives for a tier, which a
// test grants through its authorizer rather than through a token claim. There
// is no request-level variant any more: identity comes from the principal and
// the tier comes from OpenFGA, which is the whole point of #141.
func platformTier(tier string) []authz.Relation {
	switch tier {
	case "admin":
		return []authz.Relation{
			authz.RelationCanAdminister,
			authz.RelationCanCreateStack,
			authz.RelationCanPublishTemplate,
			authz.RelationCanReadTemplate,
		}
	case "editor":
		return []authz.Relation{
			authz.RelationCanCreateStack,
			authz.RelationCanPublishTemplate,
			authz.RelationCanReadTemplate,
		}
	default:
		return nil
	}
}

func TestHealthzReturnsOK(t *testing.T) {
	t.Parallel()

	server := NewServer(app.NewService(app.Service{}), configuredTenantID)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if strings.TrimSpace(response.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestAuthenticatedServerProtectsV1AndLeavesHealthPublic(t *testing.T) {
	server := NewAuthenticatedServer(app.NewService(app.Service{}), configuredTenantID, false)

	for _, test := range []struct {
		path   string
		status int
	}{
		{path: "/healthz", status: http.StatusOK},
		{path: "/v1/tenants/tenant_123/stacks", status: http.StatusUnauthorized},
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != test.status {
			t.Fatalf("%s status = %d, want %d", test.path, response.Code, test.status)
		}
	}
}

func TestTenantScopedRoutesRejectOtherTenantBeforeHandler(t *testing.T) {
	t.Parallel()

	server := NewServer(nil, configuredTenantID)
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "register template", method: http.MethodPost, path: "/v1/tenants/tenant_other/template-revisions"},
		{name: "list template revisions", method: http.MethodGet, path: "/v1/tenants/tenant_other/template-revisions"},
		{name: "get template registration", method: http.MethodGet, path: "/v1/tenants/tenant_other/template-registrations/registration_123"},
		{name: "get template variables", method: http.MethodGet, path: "/v1/tenants/tenant_other/template-revisions/revision_123/variables"},
		{name: "create stack", method: http.MethodPost, path: "/v1/tenants/tenant_other/stacks"},
		{name: "list stacks", method: http.MethodGet, path: "/v1/tenants/tenant_other/stacks"},
		{name: "get stack", method: http.MethodGet, path: "/v1/tenants/tenant_other/stacks/stack_123"},
		{name: "install template", method: http.MethodPost, path: "/v1/tenants/tenant_other/stacks/stack_123/templates"},
		{name: "update template config", method: http.MethodPatch, path: "/v1/tenants/tenant_other/stack-templates/stack_template_123/config"},
		{name: "upgrade template", method: http.MethodPost, path: "/v1/tenants/tenant_other/stack-templates/stack_template_123/upgrade"},
		{name: "start run", method: http.MethodPost, path: "/v1/tenants/tenant_other/stack-templates/stack_template_123/runs"},
		{name: "get run", method: http.MethodGet, path: "/v1/tenants/tenant_other/template-runs/run_123"},
		{name: "list run logs", method: http.MethodGet, path: "/v1/tenants/tenant_other/template-runs/run_123/logs"},
		{name: "get run log artifact", method: http.MethodGet, path: "/v1/tenants/tenant_other/template-runs/run_123/logs/plan"},
		{name: "approve run", method: http.MethodPost, path: "/v1/tenants/tenant_other/template-runs/run_123/approval"},
		{name: "cancel run", method: http.MethodPost, path: "/v1/tenants/tenant_other/template-runs/run_123/cancellation"},
		{name: "search users", method: http.MethodGet, path: "/v1/tenants/tenant_other/users/search?q=test"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, strings.NewReader("{"))

			server.ServeHTTP(response, request)

			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
			}
			var body errorResponse
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Error != "not_found" || body.Message != "resource not found" {
				t.Fatalf("body = %#v", body)
			}
		})
	}
}

func TestTenantBoundaryRejectsMissingAndMalformedPaths(t *testing.T) {
	t.Parallel()

	server := NewServer(nil, configuredTenantID)
	for _, path := range []string{
		"/v1/tenants/stacks",
		"/v1/tenants/-tenant/stacks",
		"/v1/tenants/tenant%2Fother/stacks",
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d", path, response.Code, http.StatusNotFound)
		}
	}
}

func TestAuthenticatedServerEvaluatesTenantAfterAuthentication(t *testing.T) {
	t.Parallel()

	server, cookie := sessionCookieServer(t, nil)
	path := "/v1/tenants/tenant_other/stacks"

	unauthenticated := httptest.NewRecorder()
	server.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, path, nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthenticated.Code, http.StatusUnauthorized)
	}

	authenticatedRequest := httptest.NewRequest(http.MethodGet, path, nil)
	authenticatedRequest.AddCookie(cookie)
	authenticated := httptest.NewRecorder()
	server.ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusNotFound {
		t.Fatalf("authenticated status = %d, want %d", authenticated.Code, http.StatusNotFound)
	}
}

func TestAuthenticatedServerAllowsConfiguredTenantToReachService(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stacks.list = []domain.Stack{{
		ID:       domain.StackID("stack_123"),
		TenantID: configuredTenantID,
		Name:     "Acme Prod",
		Slug:     "acme-prod",
	}}
	server, cookie := sessionCookieServer(t, deps.service())
	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.stacks.gotListTenantID != configuredTenantID {
		t.Fatalf("tenant list lookup = %q, want %q", deps.stacks.gotListTenantID, configuredTenantID)
	}
	var body []stackResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 1 || body[0].ID != "stack_123" || body[0].Slug != "acme-prod" {
		t.Fatalf("stack response = %#v", body)
	}
}

type apiTestVerifier struct{}

func (apiTestVerifier) Verify(context.Context, string) (authn.VerifiedToken, error) {
	return authn.VerifiedToken{Subject: "user-123"}, nil
}

// sessionCookieServer builds a server whose middleware can actually
// authenticate someone. The cookie is the only credential now, so a test that
// wants to reach a protected route has to hold a live session row rather than
// set an Authorization header.
func sessionCookieServer(t *testing.T, service *app.Service) (*Server, *http.Cookie) {
	t.Helper()

	const raw = "server-test-session"
	now := time.Now().UTC()
	sessions := &serverTestSessionStore{byHash: map[string]authn.Session{
		authn.HashSessionID(raw): {
			IDHash:            authn.HashSessionID(raw),
			Subject:           "user-123",
			LastSeenAt:        now,
			AbsoluteExpiresAt: now.Add(time.Hour),
		},
	}}
	server := NewAuthenticatedServer(service, configuredTenantID, false, WithAuth(AuthConfig{
		Sessions:       sessions,
		SessionIdleTTL: time.Hour,
	}))
	return server, &http.Cookie{Name: authn.SessionCookieName, Value: raw}
}

// serverTestSessionStore satisfies authn.SessionStore with only the lookup the
// middleware performs; the rest is unreachable from these tests.
type serverTestSessionStore struct {
	byHash map[string]authn.Session
}

func (store *serverTestSessionStore) CreateSession(context.Context, authn.Session) error { return nil }

func (store *serverTestSessionStore) SessionByHash(_ context.Context, idHash string) (authn.Session, error) {
	session, ok := store.byHash[idHash]
	if !ok {
		return authn.Session{}, authn.ErrSessionNotFound
	}
	return session, nil
}

func (store *serverTestSessionStore) TouchSession(context.Context, string, time.Time) error {
	return nil
}

func (store *serverTestSessionStore) RevokeSession(context.Context, string, time.Time) error {
	return nil
}

func (store *serverTestSessionStore) RevokeSessionsByIDPSessionID(context.Context, string, time.Time) (int, error) {
	return 0, nil
}

func (store *serverTestSessionStore) RevokeSessionsBySubject(context.Context, string, time.Time) (int, error) {
	return 0, nil
}

func (store *serverTestSessionStore) RevokeSessionsBySubjectWithoutIDPSession(context.Context, string, time.Time) (int, error) {
	return 0, nil
}

func (store *serverTestSessionStore) DeleteSessionsExpiredBefore(context.Context, time.Time) (int, error) {
	return 0, nil
}

func TestStartTemplateRunCallsService(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 3, 11, 30, 0, 0, time.UTC)
	deps := newAPITestDependencies()
	deps.stackTemplates.stackTemplate = domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_123"),
		StackID:                   domain.StackID("stack_123"),
		DesiredTemplateRevisionID: domain.TemplateRevisionID("template_123"),
		WorkspaceName:             "smoke-workspace",
		Lifecycle:                 domain.StackTemplateActive,
	}
	deps.runID = domain.TemplateRunID("run_123")
	deps.now = startedAt
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stack-templates/stack_template_123/runs",
		strings.NewReader(`{"operation":"plan"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if deps.stackTemplates.gotTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q", deps.stackTemplates.gotTenantID)
	}
	if deps.stackTemplates.gotID != domain.StackTemplateID("stack_template_123") {
		t.Fatalf("stack template revision id = %q", deps.stackTemplates.gotID)
	}
	if deps.templateRuns.created.Operation != domain.OperationPlan {
		t.Fatalf("operation = %q", deps.templateRuns.created.Operation)
	}
	if deps.templateRuns.created.TriggerActor != domain.UserID(apiKeycloakSubject) {
		t.Fatalf("trigger actor = %q, want %q", deps.templateRuns.created.TriggerActor, apiKeycloakSubject)
	}

	var body domain.TemplateRun
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != domain.TemplateRunID("run_123") {
		t.Fatalf("id = %q, want run_123", body.ID)
	}
	if body.Status != domain.TemplateRunQueued {
		t.Fatalf("status = %q, want queued", body.Status)
	}
	if !body.StartedAt.Equal(startedAt) {
		t.Fatalf("started_at = %v, want %v", body.StartedAt, startedAt)
	}
}

func TestStartTemplateRunMapsStalePlanToConflictWithItsOwnCode(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stackTemplates.stackTemplate = domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_123"),
		StackID:                   domain.StackID("stack_123"),
		DesiredTemplateRevisionID: domain.TemplateRevisionID("template_123"),
		DesiredConfigJSON:         json.RawMessage(`{"region":"eu-west-1"}`),
		WorkspaceName:             "smoke-workspace",
		Lifecycle:                 domain.StackTemplateActive,
		// Planned against the config as it was before the last save.
		LastPlannedRunID:              domain.TemplateRunID("run_plan_1"),
		LastPlannedTemplateRevisionID: domain.TemplateRevisionID("template_123"),
		LastPlannedConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stack-templates/stack_template_123/runs",
		strings.NewReader(`{"operation":"apply"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusConflict, response.Body.String())
	}
	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Distinct from the generic "conflict" so the client can tell the user to
	// re-plan rather than showing a generic failure.
	if body.Error != "plan_stale" {
		t.Fatalf("error code = %q, want plan_stale", body.Error)
	}
	if deps.templateRuns.created.ID != "" {
		t.Fatalf("created run = %#v, want no persisted run", deps.templateRuns.created)
	}
}

func TestStartTemplateRunMapsAnInFlightRunToConflictWithItsOwnCode(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stackTemplates.stackTemplate = domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_123"),
		StackID:                   domain.StackID("stack_123"),
		DesiredTemplateRevisionID: domain.TemplateRevisionID("template_123"),
		DesiredConfigJSON:         json.RawMessage(`{"region":"eu-west-1"}`),
		WorkspaceName:             "smoke-workspace",
		Lifecycle:                 domain.StackTemplateActive,
	}
	// What the store returns when template_runs_in_flight_idx refuses the insert.
	deps.templateRuns.createErr = app.ErrTemplateRunInFlight
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stack-templates/stack_template_123/runs",
		strings.NewReader(`{"operation":"plan"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusConflict, response.Body.String())
	}
	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Distinct from the generic "conflict" so the client knows its run history is
	// stale and can refetch, rather than showing an unexplained failure.
	if body.Error != "run_in_flight" {
		t.Fatalf("error code = %q, want run_in_flight", body.Error)
	}
}

func TestStackTemplateResponseCarriesDerivedStatesNotRawConfigs(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stackTemplates.stackTemplate = domain.StackTemplate{
		ID:                            domain.StackTemplateID("stack_template_123"),
		TenantID:                      domain.TenantID("tenant_123"),
		DesiredTemplateRevisionID:     domain.TemplateRevisionID("template_rev_1"),
		Lifecycle:                     domain.StackTemplateActive,
		LastPlannedRunID:              domain.TemplateRunID("run_plan_1"),
		LastPlannedTemplateRevisionID: domain.TemplateRevisionID("template_rev_1"),
		LastPlannedConfigJSON:         json.RawMessage(`{"region":"us-west-2"}`),
		LastAppliedRunID:              domain.TemplateRunID("run_apply_1"),
		LastAppliedTemplateRevisionID: domain.TemplateRevisionID("template_rev_1"),
		LastAppliedConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
	}
	deps.templates.variables = []domain.TemplateVariable{{Name: "region", Required: true}}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPatch,
		"/v1/tenants/tenant_123/stack-templates/stack_template_123/config",
		strings.NewReader(`{"config":{"region":"us-west-2"}}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["plan_state"] != "matches" {
		t.Fatalf("plan_state = %#v, want matches", body["plan_state"])
	}
	if body["live_state"] != "differs" {
		t.Fatalf("live_state = %#v, want differs", body["live_state"])
	}
	if body["last_planned_run_id"] != "run_plan_1" {
		t.Fatalf("last_planned_run_id = %#v", body["last_planned_run_id"])
	}
	// The snapshot configs stay server-side; returning them invites the client
	// to redo the comparison, which is the mistake these states replace.
	for _, key := range []string{"last_planned_config_json", "last_applied_config_json"} {
		if _, present := body[key]; present {
			t.Fatalf("response exposes %s", key)
		}
	}
}

func TestStartTemplateRunRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	server := NewServer(newAPITestDependencies().service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stack-templates/stack_template_123/runs",
		strings.NewReader(`{"operation":`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestStartTemplateRunMapsInvalidCommandToBadRequest(t *testing.T) {
	t.Parallel()

	server := NewServer(newAPITestDependencies().service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stack-templates/stack_template_123/runs",
		strings.NewReader(`{"operation":"refresh"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestStartTemplateRunHidesMissingStackTemplateAsForbidden(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stackTemplates.getErr = app.ErrNotFound
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stack-templates/missing_stack_template/runs",
		strings.NewReader(`{"operation":"plan"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestRegisterTemplateCallsService(t *testing.T) {
	t.Parallel()

	requestedAt := time.Date(2026, 7, 6, 11, 30, 0, 0, time.UTC)
	deps := newAPITestDependencies().withPlatformTier("admin")
	deps.registrationID = domain.TemplateRegistrationID("template_registration_123")
	deps.now = requestedAt
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/template-revisions",
		strings.NewReader(`{"repo_owner":"acme","repo_name":"infra-templates","source_ref":"v0.0.1","root_path":"modules/vpc"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if deps.registrations.created.TenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q", deps.registrations.created.TenantID)
	}
	if deps.registrations.created.RepoOwner != "acme" {
		t.Fatalf("repo owner = %q", deps.registrations.created.RepoOwner)
	}
	if deps.registrations.created.SourceRef != "v0.0.1" {
		t.Fatalf("source ref = %q", deps.registrations.created.SourceRef)
	}
	if deps.registrations.created.RequestedBy != domain.UserID(apiKeycloakSubject) {
		t.Fatalf("requested by = %q, want %q", deps.registrations.created.RequestedBy, apiKeycloakSubject)
	}
	if len(deps.work.requests) != 1 || deps.work.requests[0].Kind != app.KindStartTemplateSync {
		t.Fatalf("queued requests = %#v, want one start_template_sync request", deps.work.requests)
	}

	var body domain.TemplateRegistration
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != domain.TemplateRegistrationID("template_registration_123") {
		t.Fatalf("id = %q, want template_registration_123", body.ID)
	}
	if body.Status != domain.TemplateRegistrationPending {
		t.Fatalf("status = %q, want pending", body.Status)
	}
	if !body.RequestedAt.Equal(requestedAt) {
		t.Fatalf("requested_at = %v, want %v", body.RequestedAt, requestedAt)
	}
}

func TestRegisterTemplateRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	server := NewServer(newAPITestDependencies().service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/template-revisions",
		strings.NewReader(`{"repo_owner":`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestRegisterTemplateMapsInvalidCommandToBadRequest(t *testing.T) {
	t.Parallel()

	server := NewServer(newAPITestDependencies().withPlatformTier("admin").service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/template-revisions",
		strings.NewReader(`{"repo_owner":"acme","repo_name":"infra-templates","root_path":"modules/vpc"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestCreateStackCallsService(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 6, 13, 30, 0, 0, time.UTC)
	deps := newAPITestDependencies().withPlatformTier("admin")
	deps.stackID = domain.StackID("stack_123")
	deps.now = createdAt
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stacks",
		strings.NewReader(`{"name":"Acme Prod","tags":{"env":"prod"},"default_credential_ids":["credential_123"]}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if deps.stacks.created.TenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q, want tenant_123", deps.stacks.created.TenantID)
	}
	if deps.stacks.created.Slug != "acme-prod" {
		t.Fatalf("slug = %q, want acme-prod", deps.stacks.created.Slug)
	}
	if deps.stacks.created.CreatedBy != domain.UserID(apiKeycloakSubject) {
		t.Fatalf("created by = %q, want %q", deps.stacks.created.CreatedBy, apiKeycloakSubject)
	}

	var body stackResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != "stack_123" {
		t.Fatalf("response id = %q, want stack_123", body.ID)
	}
	if body.Tags["env"] != "prod" {
		t.Fatalf("response tags = %#v", body.Tags)
	}
}

func TestWriteAppErrorMapsAuthorizationDependencyFailure(t *testing.T) {
	t.Parallel()

	response := httptest.NewRecorder()
	writeAppError(response, authz.ErrUnavailable)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != "authorization_unavailable" {
		t.Fatalf("error = %q, want authorization_unavailable", body.Error)
	}
}

func TestCreateStackRejectsPrincipalWithoutCreatorRole(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID)
	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant_123/stacks", strings.NewReader(`{"name":"Acme"}`))
	request = request.WithContext(authn.ContextWithPrincipal(request.Context(), authn.Principal{Subject: apiKeycloakSubject}))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	if deps.stacks.created.ID != "" {
		t.Fatalf("created stack = %#v, want no persistence", deps.stacks.created)
	}
}

// CreateStack no longer writes to OpenFGA at request time, so there is no
// "owner write failed after persistence" case left to map. What replaces it is
// a failing unit of work, which persists nothing and surfaces as 503.
func TestCreateStackMapsUnitOfWorkFailure(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies().withPlatformTier("admin")
	service := app.NewService(app.Service{
		Authorizer: deps.authorizer,
		Stacks:     &deps.stacks,
		Work:       &apiUnitOfWork{stacks: &deps.stacks, err: authz.ErrUnavailable},
		StackIDs:   fixedStackIDGenerator{id: deps.stackID},
		Clock:      fixedClock{now: deps.now},
	})
	server := NewServer(service, configuredTenantID)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, authenticatedRequest(http.MethodPost, "/v1/tenants/tenant_123/stacks", strings.NewReader(`{"name":"Acme"}`)))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if deps.stacks.created.ID != "" {
		t.Fatal("a failed unit of work must not persist the stack")
	}
}

func TestCreateStackEnqueuesProvisioningIntent(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies().withPlatformTier("admin")
	work := &apiUnitOfWork{stacks: &deps.stacks}
	service := app.NewService(app.Service{
		Authorizer: deps.authorizer,
		Stacks:     &deps.stacks,
		Work:       work,
		StackIDs:   fixedStackIDGenerator{id: deps.stackID},
		Clock:      fixedClock{now: deps.now},
	})
	server := NewServer(service, configuredTenantID)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, authenticatedRequest(http.MethodPost, "/v1/tenants/tenant_123/stacks", strings.NewReader(`{"name":"Acme"}`)))

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if len(work.requests) != 1 {
		t.Fatalf("enqueued %d intents, want 1", len(work.requests))
	}
	if work.requests[0].Kind != app.KindGrantStackOwner {
		t.Fatalf("kind = %q, want %q", work.requests[0].Kind, app.KindGrantStackOwner)
	}
	if deps.stacks.created.ID == "" {
		t.Fatal("stack was not persisted")
	}

	// The caller is told the stack is not usable yet rather than being left to
	// discover it through a failing follow-up request.
	var body stackResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != string(domain.StackStatusProvisioning) {
		t.Fatalf("status = %q, want %q", body.Status, domain.StackStatusProvisioning)
	}
}

func TestListStacksReturnsTenantStacks(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 6, 13, 30, 0, 0, time.UTC)
	deps := newAPITestDependencies()
	deps.stacks.list = []domain.Stack{
		{
			ID:        domain.StackID("stack_123"),
			TenantID:  domain.TenantID("tenant_123"),
			Name:      "Acme Prod",
			Slug:      "acme-prod",
			Tags:      map[string]string{"env": "prod"},
			CreatedBy: domain.UserID("user_123"),
			CreatedAt: createdAt,
		},
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.stacks.gotListTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant list lookup = %q, want tenant_123", deps.stacks.gotListTenantID)
	}

	var body []stackResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("len(body) = %d, want 1", len(body))
	}
	if body[0].ID != "stack_123" || body[0].Slug != "acme-prod" {
		t.Fatalf("stack response = %#v", body[0])
	}
}

func TestGetStackReturnsStackView(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stacks.view = app.StackView{
		Stack: domain.Stack{
			ID:       domain.StackID("stack_123"),
			TenantID: domain.TenantID("tenant_123"),
			Name:     "Acme Prod",
			Slug:     "acme-prod",
			Tags:     map[string]string{"env": "prod"},
		},
		Templates: []app.StackTemplateView{
			{StackTemplate: domain.StackTemplate{
				ID:                        domain.StackTemplateID("stack_template_123"),
				TenantID:                  domain.TenantID("tenant_123"),
				StackID:                   domain.StackID("stack_123"),
				DesiredTemplateRevisionID: domain.TemplateRevisionID("template_123"),
				WorkspaceName:             "meg_acme_prod_late_123",
				// A persisted row always has a desired config: the column is
				// not null and CreateStackTemplate seeds it from the install
				// config. Setting only the install config modelled a row that
				// cannot exist, which the removed fallback used to paper over.
				InstalledConfigJSON: json.RawMessage(`{"region":"us-east-1"}`),
				DesiredConfigJSON:   json.RawMessage(`{"region":"us-east-1"}`),
				Lifecycle:           domain.StackTemplateActive,
			}},
		},
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack_123", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.stacks.gotStackID != domain.StackID("stack_123") {
		t.Fatalf("stack lookup = %q, want stack_123", deps.stacks.gotStackID)
	}

	var body stackViewResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Stack.ID != "stack_123" {
		t.Fatalf("stack id = %q, want stack_123", body.Stack.ID)
	}
	if len(body.Templates) != 1 {
		t.Fatalf("len(templates) = %d, want 1", len(body.Templates))
	}
	if body.Templates[0].Config["region"] != "us-east-1" {
		t.Fatalf("template config = %#v", body.Templates[0].Config)
	}
}

func TestAddTemplateToStackCallsService(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stacks.stack = domain.Stack{ID: domain.StackID("stack_123"), TenantID: domain.TenantID("tenant_123"), Slug: "acme-prod"}
	deps.templates.template = domain.TemplateRevision{ID: domain.TemplateRevisionID("template_123"), TenantID: domain.TenantID("tenant_123"), Status: domain.TemplateRevisionActive}
	deps.templates.variables = []domain.TemplateVariable{{Name: "region", Required: true}}
	deps.stackTemplateID = domain.StackTemplateID("stack_template_a1b2c3d4")
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stacks/stack_123/templates",
		strings.NewReader(`{"template_revision_id":"template_123","config":{"region":"us-east-1"}}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if deps.stackTemplateInstaller.created.StackID != domain.StackID("stack_123") {
		t.Fatalf("stack id = %q, want stack_123", deps.stackTemplateInstaller.created.StackID)
	}
	if deps.stackTemplateInstaller.created.CreatedBy != domain.UserID(apiKeycloakSubject) {
		t.Fatalf("created by = %q, want %q", deps.stackTemplateInstaller.created.CreatedBy, apiKeycloakSubject)
	}

	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := body["template_revision_id"]; ok {
		t.Fatalf("response should not include legacy template_revision_id: %#v", body)
	}
	if body["id"] != "stack_template_a1b2c3d4" {
		t.Fatalf("response id = %q, want stack_template_a1b2c3d4", body["id"])
	}
	if body["created_by"] != apiKeycloakSubject {
		t.Fatalf("response created by = %q, want %q", body["created_by"], apiKeycloakSubject)
	}
	config, ok := body["config"].(map[string]any)
	if !ok || config["region"] != "us-east-1" {
		t.Fatalf("response config = %#v", body["config"])
	}
}

func TestUpdateStackTemplateConfigCallsService(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stackTemplates.stackTemplate = domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_123"),
		TenantID:                  domain.TenantID("tenant_123"),
		DesiredTemplateRevisionID: domain.TemplateRevisionID("template_rev_1"),
		Lifecycle:                 domain.StackTemplateActive,
	}
	deps.templates.variables = []domain.TemplateVariable{{Name: "region", Required: true}}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPatch,
		"/v1/tenants/tenant_123/stack-templates/stack_template_123/config",
		strings.NewReader(`{"config":{"region":"us-west-2"}}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if string(deps.stackTemplates.gotConfigJSON) != `{"region":"us-west-2"}` {
		t.Fatalf("config update = %s", deps.stackTemplates.gotConfigJSON)
	}

	var body stackTemplateResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Config["region"] != "us-west-2" {
		t.Fatalf("response config = %#v", body.Config)
	}
}

func TestUpgradeStackTemplateCallsService(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stackTemplates.stackTemplate = domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_123"),
		TenantID:                  domain.TenantID("tenant_123"),
		SourceTemplateID:          domain.SourceTemplateID("source_template_vpc"),
		DesiredTemplateRevisionID: domain.TemplateRevisionID("template_rev_1"),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		Lifecycle:                 domain.StackTemplateActive,
	}
	deps.templates.template = domain.TemplateRevision{
		ID:               domain.TemplateRevisionID("template_rev_2"),
		TenantID:         domain.TenantID("tenant_123"),
		SourceTemplateID: domain.SourceTemplateID("source_template_vpc"),
		Status:           domain.TemplateRevisionActive,
	}
	deps.templates.variables = []domain.TemplateVariable{{Name: "region", Required: true}}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stack-templates/stack_template_123/upgrade",
		strings.NewReader(`{"target_template_revision_id":"template_rev_2"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.stackTemplates.gotDesiredTemplateRevisionID != domain.TemplateRevisionID("template_rev_2") {
		t.Fatalf("desired template revision update = %q, want template_rev_2", deps.stackTemplates.gotDesiredTemplateRevisionID)
	}

	var body stackTemplateResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.DesiredTemplateRevisionID != "template_rev_2" {
		t.Fatalf("desired template revision id = %q, want template_rev_2", body.DesiredTemplateRevisionID)
	}
	if body.Config["region"] != "us-east-1" {
		t.Fatalf("response config = %#v", body.Config)
	}
}

func TestUpgradeStackTemplateMapsMissingRequiredVariableToConflict(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stackTemplates.stackTemplate = domain.StackTemplate{
		ID:                        domain.StackTemplateID("stack_template_123"),
		TenantID:                  domain.TenantID("tenant_123"),
		SourceTemplateID:          domain.SourceTemplateID("source_template_vpc"),
		DesiredTemplateRevisionID: domain.TemplateRevisionID("template_rev_1"),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		Lifecycle:                 domain.StackTemplateActive,
	}
	deps.templates.template = domain.TemplateRevision{
		ID:               domain.TemplateRevisionID("template_rev_2"),
		TenantID:         domain.TenantID("tenant_123"),
		SourceTemplateID: domain.SourceTemplateID("source_template_vpc"),
		Status:           domain.TemplateRevisionActive,
	}
	deps.templates.variables = []domain.TemplateVariable{
		{Name: "region", Required: true},
		{Name: "cidr_block", Required: true},
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stack-templates/stack_template_123/upgrade",
		strings.NewReader(`{"target_template_revision_id":"template_rev_2"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusConflict, response.Body.String())
	}
}

func TestUpgradeStackTemplateMapsSourceMismatchToConflict(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stackTemplates.stackTemplate = domain.StackTemplate{
		ID:               domain.StackTemplateID("stack_template_123"),
		TenantID:         domain.TenantID("tenant_123"),
		SourceTemplateID: domain.SourceTemplateID("source_template_vpc"),
		Lifecycle:        domain.StackTemplateActive,
	}
	deps.templates.template = domain.TemplateRevision{
		ID:               domain.TemplateRevisionID("template_rev_2"),
		TenantID:         domain.TenantID("tenant_123"),
		SourceTemplateID: domain.SourceTemplateID("source_template_db"),
		Status:           domain.TemplateRevisionActive,
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stack-templates/stack_template_123/upgrade",
		strings.NewReader(`{"target_template_revision_id":"template_rev_2"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusConflict, response.Body.String())
	}
}

func TestStackTemplateEditRoutesHideMissingStackTemplateAsForbidden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "config edit",
			method: http.MethodPatch,
			path:   "/v1/tenants/tenant_123/stack-templates/missing_stack_template/config",
			body:   `{"config":{}}`,
		},
		{
			name:   "upgrade",
			method: http.MethodPost,
			path:   "/v1/tenants/tenant_123/stack-templates/missing_stack_template/upgrade",
			body:   `{"target_template_revision_id":"template_rev_2"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			deps := newAPITestDependencies()
			deps.stackTemplates.getErr = app.ErrNotFound
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			request := authenticatedRequest(tt.method, tt.path, strings.NewReader(tt.body))

			server.ServeHTTP(response, request)

			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusForbidden, response.Body.String())
			}
		})
	}
}

func TestGetTemplateRegistrationReturnsRegistration(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies().withPlatformTier("admin")
	deps.registrations.registration = domain.TemplateRegistration{
		ID:       domain.TemplateRegistrationID("template_registration_123"),
		TenantID: domain.TenantID("tenant_123"),
		Status:   domain.TemplateRegistrationCompleted,
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/template-registrations/template_registration_123", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.registrations.gotGetTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q, want tenant_123", deps.registrations.gotGetTenantID)
	}

	var body domain.TemplateRegistration
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != domain.TemplateRegistrationID("template_registration_123") {
		t.Fatalf("id = %q, want template_registration_123", body.ID)
	}
}

func TestListTemplateRevisionsReturnsTenantTemplateRevisions(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	deps := newAPITestDependencies().withPlatformTier("admin")
	deps.templates.templates = []domain.TemplateRevision{
		{
			ID:                domain.TemplateRevisionID("template_123"),
			TenantID:          domain.TenantID("tenant_123"),
			RepoOwner:         "acme",
			RepoName:          "infra-templates",
			SourceRef:         "main",
			ResolvedCommitSHA: "abc123",
			RootPath:          ".",
			Name:              "infra-templates",
			Tags:              []string{"aws"},
			Status:            domain.TemplateRevisionActive,
			CreatedAt:         createdAt,
		},
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/template-revisions", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.templates.gotListTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant list lookup = %q, want tenant_123", deps.templates.gotListTenantID)
	}

	var body []domain.TemplateRevision
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("len(body) = %d, want 1", len(body))
	}
	if body[0].ID != domain.TemplateRevisionID("template_123") || body[0].Status != domain.TemplateRevisionActive {
		t.Fatalf("template revision response = %#v", body[0])
	}
}

func TestListTemplateRunsReturnsRunsForStackTemplate(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	deps := newAPITestDependencies()
	deps.templateRuns.list = []domain.TemplateRun{
		{
			ID:              domain.TemplateRunID("run_apply_1"),
			TenantID:        domain.TenantID("tenant_123"),
			StackTemplateID: domain.StackTemplateID("stack_template_123"),
			Operation:       domain.OperationApply,
			Status:          domain.TemplateRunWaitingApproval,
			TriggerActor:    domain.UserID("user_456"),
			StartedAt:       startedAt,
		},
	}
	deps.stackTemplates.stackTemplate = domain.StackTemplate{
		ID:       domain.StackTemplateID("stack_template_123"),
		TenantID: domain.TenantID("tenant_123"),
		StackID:  domain.StackID("stack_123"),
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.templateRuns.gotListStackTemplateID != domain.StackTemplateID("stack_template_123") {
		t.Fatalf("stack template lookup = %q, want stack_template_123", deps.templateRuns.gotListStackTemplateID)
	}

	var body []domain.TemplateRun
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 1 || body[0].ID != domain.TemplateRunID("run_apply_1") || body[0].Status != domain.TemplateRunWaitingApproval {
		t.Fatalf("runs = %#v", body)
	}
}

func TestGetTemplateRevisionVariablesReturnsVariables(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies().withPlatformTier("admin")
	deps.templates.variables = []domain.TemplateVariable{
		{
			TemplateRevisionID: domain.TemplateRevisionID("template_123"),
			Name:               "region",
			TypeExpression:     "string",
			Required:           true,
		},
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/template-revisions/template_123/variables", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.templates.gotVariablesTemplateRevisionID != domain.TemplateRevisionID("template_123") {
		t.Fatalf("template revision id = %q, want template_123", deps.templates.gotVariablesTemplateRevisionID)
	}

	var body []domain.TemplateVariable
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("len(body) = %d, want 1", len(body))
	}
	if body[0].Name != "region" {
		t.Fatalf("variable name = %q, want region", body[0].Name)
	}
}

func TestGetTemplateRunReturnsRun(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.templateRuns.run = domain.TemplateRun{
		ID:              domain.TemplateRunID("run_123"),
		TenantID:        domain.TenantID("tenant_123"),
		StackTemplateID: domain.StackTemplateID("stack_template_123"),
		Operation:       domain.OperationPlan,
		Status:          domain.TemplateRunCompleted,
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/template-runs/run_123", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.templateRuns.gotGetTenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q, want tenant_123", deps.templateRuns.gotGetTenantID)
	}
	if deps.templateRuns.gotGetRunID != domain.TemplateRunID("run_123") {
		t.Fatalf("run id = %q, want run_123", deps.templateRuns.gotGetRunID)
	}

	var body domain.TemplateRun
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != domain.TemplateRunID("run_123") {
		t.Fatalf("id = %q, want run_123", body.ID)
	}
	if body.Status != domain.TemplateRunCompleted {
		t.Fatalf("status = %q, want completed", body.Status)
	}
}

func TestGetTemplateRunLogReturnsPlainText(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.templateRuns.run = domain.TemplateRun{
		ID:       domain.TemplateRunID("run_123"),
		TenantID: domain.TenantID("tenant_123"),
	}
	deps.logs.content = []byte("terraform plan output\n")
	deps.logMetadata.log = domain.TemplateRunLog{
		TenantID:  domain.TenantID("tenant_123"),
		RunID:     domain.TemplateRunID("run_123"),
		Phase:     "plan",
		ObjectKey: "tenants/tenant_123/runs/run_123/logs/plan.log",
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/template-runs/run_123/logs/plan", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain; charset=utf-8", contentType)
	}
	if response.Body.String() != "terraform plan output\n" {
		t.Fatalf("body = %q, want terraform plan output", response.Body.String())
	}
	if deps.logs.gotPhase != "plan" {
		t.Fatalf("phase = %q, want plan", deps.logs.gotPhase)
	}
}

func TestListTemplateRunLogsReturnsMetadata(t *testing.T) {
	t.Parallel()

	uploadedAt := time.Date(2026, 7, 6, 10, 15, 0, 0, time.UTC)
	deps := newAPITestDependencies()
	deps.templateRuns.run = domain.TemplateRun{
		ID:       domain.TemplateRunID("run_123"),
		TenantID: domain.TenantID("tenant_123"),
	}
	deps.logMetadata.logs = []domain.TemplateRunLog{
		{
			TenantID:    domain.TenantID("tenant_123"),
			RunID:       domain.TemplateRunID("run_123"),
			Phase:       "init",
			ObjectKey:   "tenants/tenant_123/runs/run_123/logs/init.log",
			ContentType: "text/plain; charset=utf-8",
			SizeBytes:   12,
			UploadedAt:  uploadedAt,
		},
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/template-runs/run_123/logs", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.logMetadata.gotListRunID != domain.TemplateRunID("run_123") {
		t.Fatalf("metadata run ID = %q, want run_123", deps.logMetadata.gotListRunID)
	}

	var body []domain.TemplateRunLog
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("len(body) = %d, want 1", len(body))
	}
	if body[0].Phase != "init" {
		t.Fatalf("phase = %q, want init", body[0].Phase)
	}
	if body[0].SizeBytes != 12 {
		t.Fatalf("size_bytes = %d, want 12", body[0].SizeBytes)
	}
}

func TestListTemplateRunLogsReturnsEmptyArray(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.templateRuns.run = domain.TemplateRun{
		ID:       domain.TemplateRunID("run_123"),
		TenantID: domain.TenantID("tenant_123"),
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/template-runs/run_123/logs", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if strings.TrimSpace(response.Body.String()) != "[]" {
		t.Fatalf("body = %q, want []", response.Body.String())
	}
}

func TestGetTemplateRunLogMapsInvalidPhaseToBadRequest(t *testing.T) {
	t.Parallel()

	server := NewServer(newAPITestDependencies().service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/tenants/tenant_123/template-runs/run_123/logs/refresh",
		nil,
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestGetTemplateRunMapsMissingRunToNotFound(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.templateRuns.getErr = app.ErrNotFound
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/template-runs/run_123", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestGetTemplateRunLogMapsMissingLogToNotFound(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.templateRuns.run = domain.TemplateRun{
		ID:       domain.TemplateRunID("run_123"),
		TenantID: domain.TenantID("tenant_123"),
	}
	deps.logs.err = app.ErrNotFound
	deps.logMetadata.log = domain.TemplateRunLog{
		TenantID:  domain.TenantID("tenant_123"),
		RunID:     domain.TemplateRunID("run_123"),
		Phase:     "plan",
		ObjectKey: "tenants/tenant_123/runs/run_123/logs/plan.log",
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/template-runs/run_123/logs/plan", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestApproveRunCallsService(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/template-runs/run_123/approval",
		strings.NewReader(`{}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNoContent, response.Body.String())
	}
	if deps.templateRuns.approval.TenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q", deps.templateRuns.approval.TenantID)
	}
	if deps.templateRuns.approval.RunID != domain.TemplateRunID("run_123") {
		t.Fatalf("run id = %q", deps.templateRuns.approval.RunID)
	}
	if deps.templateRuns.approval.ApprovedBy != domain.UserID(apiKeycloakSubject) {
		t.Fatalf("approved by = %q, want %q", deps.templateRuns.approval.ApprovedBy, apiKeycloakSubject)
	}
	if len(deps.work.requests) != 1 || deps.work.requests[0].Kind != app.KindSignalRunApproval {
		t.Fatalf("queued requests = %#v, want one signal_run_approval request", deps.work.requests)
	}
}

func TestApproveRunAllowsSelfApproval(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.templateRuns.run = domain.TemplateRun{
		ID:              "run_123",
		TenantID:        "tenant_123",
		StackTemplateID: "stack_template_123",
		TriggerActor:    domain.UserID(apiKeycloakSubject),
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/template-runs/run_123/approval",
		strings.NewReader(`{}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNoContent, response.Body.String())
	}
	if deps.templateRuns.approval.RunID == "" {
		t.Fatalf("approval was not recorded, want approval")
	}
	if len(deps.work.requests) != 1 || deps.work.requests[0].Kind != app.KindSignalRunApproval {
		t.Fatalf("queued requests = %#v, want one approval signal", deps.work.requests)
	}
}

func TestCancelRunCallsService(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/template-runs/run_123/cancellation",
		strings.NewReader(`{"reason":"testing"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNoContent, response.Body.String())
	}
	if deps.templateRuns.cancellation.TenantID != domain.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q", deps.templateRuns.cancellation.TenantID)
	}
	if deps.templateRuns.cancellation.RunID != domain.TemplateRunID("run_123") {
		t.Fatalf("run id = %q", deps.templateRuns.cancellation.RunID)
	}
	if deps.templateRuns.cancellation.RequestedBy != domain.UserID(apiKeycloakSubject) {
		t.Fatalf("requested by = %q, want %q", deps.templateRuns.cancellation.RequestedBy, apiKeycloakSubject)
	}
	if deps.templateRuns.cancellation.Reason != "testing" {
		t.Fatalf("reason = %q", deps.templateRuns.cancellation.Reason)
	}
	if len(deps.work.requests) != 1 || deps.work.requests[0].Kind != app.KindSignalRunCancellation {
		t.Fatalf("queued requests = %#v, want one cancellation signal", deps.work.requests)
	}
}

func TestRunDecisionRequestsRejectTopLevelNull(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		path            string
		assertNoEffects func(*testing.T, *apiTestDependencies)
	}{
		{
			name: "approval",
			path: "/v1/tenants/tenant_123/template-runs/run_123/approval",
			assertNoEffects: func(t *testing.T, deps *apiTestDependencies) {
				t.Helper()
				if deps.templateRuns.approval.RunID != "" {
					t.Errorf("approval run ID = %q, want no approval", deps.templateRuns.approval.RunID)
				}
				if deps.workflows.approvalRunID != "" {
					t.Errorf("workflow approval run ID = %q, want no signal", deps.workflows.approvalRunID)
				}
			},
		},
		{
			name: "cancellation",
			path: "/v1/tenants/tenant_123/template-runs/run_123/cancellation",
			assertNoEffects: func(t *testing.T, deps *apiTestDependencies) {
				t.Helper()
				if deps.templateRuns.cancellation.RunID != "" {
					t.Errorf("cancellation run ID = %q, want no cancellation", deps.templateRuns.cancellation.RunID)
				}
				if deps.workflows.cancelRunID != "" {
					t.Errorf("workflow cancellation run ID = %q, want no signal", deps.workflows.cancelRunID)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			deps := newAPITestDependencies()
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			request := authenticatedRequest(http.MethodPost, test.path, strings.NewReader(`null`))

			server.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			test.assertNoEffects(t, deps)
		})
	}
}

func TestRunDecisionConflictErrorsReturnConflict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		body       string
		configure  func(*apiTestDependencies)
		statusCode int
	}{
		{
			name: "approval",
			path: "/v1/tenants/tenant_123/template-runs/run_123/approval",
			body: `{}`,
			configure: func(deps *apiTestDependencies) {
				deps.templateRuns.approvalErr = app.ErrRunNotApprovable
			},
			statusCode: http.StatusConflict,
		},
		{
			name: "cancellation",
			path: "/v1/tenants/tenant_123/template-runs/run_123/cancellation",
			body: `{}`,
			configure: func(deps *apiTestDependencies) {
				deps.templateRuns.cancellationErr = app.ErrRunNotCancelable
			},
			statusCode: http.StatusConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			deps := newAPITestDependencies()
			tt.configure(deps)
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			request := authenticatedRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))

			server.ServeHTTP(response, request)

			if response.Code != tt.statusCode {
				t.Fatalf("status = %d, want %d", response.Code, tt.statusCode)
			}
		})
	}
}

func TestMutationRequestsRejectIdentityOverrides(t *testing.T) {
	assertNoStackCreation := func(t *testing.T, deps *apiTestDependencies) {
		t.Helper()
		if deps.stacks.created.ID != "" {
			t.Errorf("created stack ID = %q, want no mutation", deps.stacks.created.ID)
		}
	}
	assertNoRegistration := func(t *testing.T, deps *apiTestDependencies) {
		t.Helper()
		if deps.registrations.created.ID != "" {
			t.Errorf("created registration ID = %q, want no mutation", deps.registrations.created.ID)
		}
		if deps.workflows.syncInput.RegistrationID != "" {
			t.Errorf("workflow registration ID = %q, want no workflow start", deps.workflows.syncInput.RegistrationID)
		}
	}
	assertNoRun := func(t *testing.T, deps *apiTestDependencies) {
		t.Helper()
		if deps.templateRuns.created.ID != "" {
			t.Errorf("created run ID = %q, want no mutation", deps.templateRuns.created.ID)
		}
		if deps.workflows.input.RunID != "" {
			t.Errorf("workflow run ID = %q, want no workflow start", deps.workflows.input.RunID)
		}
	}
	assertNoApproval := func(t *testing.T, deps *apiTestDependencies) {
		t.Helper()
		if deps.templateRuns.approval.RunID != "" {
			t.Errorf("approval run ID = %q, want no mutation", deps.templateRuns.approval.RunID)
		}
		if deps.workflows.approvalRunID != "" {
			t.Errorf("workflow approval run ID = %q, want no signal", deps.workflows.approvalRunID)
		}
	}

	tests := []struct {
		name            string
		path            string
		body            string
		assertNoEffects func(*testing.T, *apiTestDependencies)
	}{
		{name: "actor", path: "/v1/tenants/tenant_123/stacks", body: `{"name":"Acme","actor":"spoofed"}`, assertNoEffects: assertNoStackCreation},
		{name: "requested by", path: "/v1/tenants/tenant_123/template-revisions", body: `{"repo_owner":"acme","repo_name":"infra","source_ref":"main","root_path":".","requested_by":"spoofed"}`, assertNoEffects: assertNoRegistration},
		{name: "trigger actor", path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", body: `{"operation":"plan","trigger_actor":"spoofed"}`, assertNoEffects: assertNoRun},
		{name: "approved by", path: "/v1/tenants/tenant_123/template-runs/run_123/approval", body: `{"approved_by":"spoofed"}`, assertNoEffects: assertNoApproval},
		{name: "created by", path: "/v1/tenants/tenant_123/stacks", body: `{"name":"Acme","created_by":"spoofed"}`, assertNoEffects: assertNoStackCreation},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := newAPITestDependencies()
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			request := authenticatedRequest(http.MethodPost, test.path, strings.NewReader(test.body))

			server.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			test.assertNoEffects(t, deps)
		})
	}
}

func TestDecodeRequestBodyRejectsNonObjectAndMultipleValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "array", body: `[]`},
		{name: "string", body: `"value"`},
		{name: "number", body: `42`},
		{name: "boolean", body: `true`},
		{name: "second value", body: `{"name":"accepted"} {"name":"extra"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			var destination struct {
				Name string `json:"name"`
			}

			if decodeRequestBody(response, request, &destination) {
				t.Fatal("decodeRequestBody accepted request, want rejection before service effect")
			}
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}

func TestMutationRequestsRejectMissingPrincipal(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stacks",
		strings.NewReader(`{"name":"Acme Prod","tags":{"env":"prod"},"default_credential_ids":["credential_123"]}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != "unauthorized" {
		t.Fatalf("error code = %q, want unauthorized", body.Error)
	}
	if deps.stacks.created.ID != "" {
		t.Fatalf("created stack ID = %q, want no mutation", deps.stacks.created.ID)
	}
}

func TestUnknownRouteReturnsNotFound(t *testing.T) {
	t.Parallel()

	server := NewServer(newAPITestDependencies().service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/nope", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestTemplateCatalogRoutesRejectOrdinaryUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "register", method: http.MethodPost, path: "/v1/tenants/tenant_123/template-revisions", body: `{"repo_owner":"acme","repo_name":"infra","source_ref":"main","root_path":"."}`},
		{name: "list revisions", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-revisions"},
		{name: "registration status", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-registrations/registration_123"},
		{name: "revision variables", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-revisions/revision_123/variables"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deps := newAPITestDependencies()
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			request := ordinaryAuthenticatedRequest(test.method, test.path, strings.NewReader(test.body))

			server.ServeHTTP(response, request)

			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusForbidden, response.Body.String())
			}
			if deps.registrations.created.ID != "" {
				t.Fatalf("registration = %#v, want no mutation", deps.registrations.created)
			}
		})
	}
}

func TestTemplateCatalogRoutesAllowGlobalRoles(t *testing.T) {
	t.Parallel()

	routes := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "register", method: http.MethodPost, path: "/v1/tenants/tenant_123/template-revisions", body: `{"repo_owner":"acme","repo_name":"infra","source_ref":"main","root_path":"."}`, status: http.StatusAccepted},
		{name: "list revisions", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-revisions", status: http.StatusOK},
		{name: "registration status", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-registrations/registration_123", status: http.StatusOK},
		{name: "revision variables", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-revisions/revision_123/variables", status: http.StatusOK},
	}

	for _, tier := range []string{"admin", "editor"} {
		for _, route := range routes {
			t.Run(tier+" "+route.name, func(t *testing.T) {
				t.Parallel()
				deps := newAPITestDependencies().withPlatformTier(tier)
				server := NewServer(deps.service(), configuredTenantID)
				response := httptest.NewRecorder()
				request := authenticatedRequest(route.method, route.path, strings.NewReader(route.body))

				server.ServeHTTP(response, request)

				if response.Code != route.status {
					t.Fatalf("status = %d, want %d; body = %s", response.Code, route.status, response.Body.String())
				}
			})
		}
	}
}

func TestCreateStackAllowsGlobalRoles(t *testing.T) {
	t.Parallel()

	for _, tier := range []string{"admin", "editor"} {
		t.Run(tier, func(t *testing.T) {
			t.Parallel()
			deps := newAPITestDependencies().withPlatformTier(tier)
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			request := authenticatedRequest(http.MethodPost, "/v1/tenants/tenant_123/stacks", strings.NewReader(`{"name":"Acme"}`))

			server.ServeHTTP(response, request)

			if response.Code != http.StatusCreated {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
			}
		})
	}
}

func TestStackRoleRoutesUseInheritedPermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		role       authz.Relation
		method     string
		path       string
		body       string
		status     int
		permission authz.Relation
	}{
		{name: "viewer lists stacks", role: authz.RelationViewer, method: http.MethodGet, path: "/v1/tenants/tenant_123/stacks", status: http.StatusOK, permission: authz.RelationCanView},
		{name: "viewer reads stack", role: authz.RelationViewer, method: http.MethodGet, path: "/v1/tenants/tenant_123/stacks/stack_123", status: http.StatusOK, permission: authz.RelationCanView},
		{name: "operator installs template", role: authz.RelationOperator, method: http.MethodPost, path: "/v1/tenants/tenant_123/stacks/stack_123/templates", body: `{"template_revision_id":"revision_123","config":{}}`, status: http.StatusCreated, permission: authz.RelationCanOperate},
		{name: "owner operates config", role: authz.RelationOwner, method: http.MethodPatch, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/config", body: `{"config":{}}`, status: http.StatusOK, permission: authz.RelationCanOperate},
		{name: "operator upgrades template", role: authz.RelationOperator, method: http.MethodPost, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/upgrade", body: `{"target_template_revision_id":"revision_123"}`, status: http.StatusOK, permission: authz.RelationCanOperate},
		{name: "operator starts run", role: authz.RelationOperator, method: http.MethodPost, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", body: `{"operation":"plan"}`, status: http.StatusCreated, permission: authz.RelationCanOperate},
		{name: "approver approves run", role: authz.RelationApprover, method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/approval", body: `{}`, status: http.StatusNoContent, permission: authz.RelationCanApprove},
		{name: "owner cancels run", role: authz.RelationOwner, method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/cancellation", body: `{}`, status: http.StatusNoContent, permission: authz.RelationCanOperate},
		{name: "viewer reads run", role: authz.RelationViewer, method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123", status: http.StatusOK, permission: authz.RelationCanView},
		{name: "approver lists run logs", role: authz.RelationApprover, method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs", status: http.StatusOK, permission: authz.RelationCanView},
		{name: "viewer reads run log", role: authz.RelationViewer, method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs/plan", status: http.StatusOK, permission: authz.RelationCanView},
		{name: "viewer lists run history", role: authz.RelationViewer, method: http.MethodGet, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", status: http.StatusOK, permission: authz.RelationCanView},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deps := newPermissionMatrixDependencies()
			deps.authorizer.enforceRole = true
			deps.authorizer.role = test.role
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			request := ordinaryAuthenticatedRequest(test.method, test.path, strings.NewReader(test.body))

			server.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.status, response.Body.String())
			}
			matched := deps.authorizer.check.Relation == test.permission
			if !matched {
				for _, check := range deps.authorizer.batchChecks {
					if check.Relation == test.permission {
						matched = true
						break
					}
				}
			}
			if !matched {
				t.Fatalf("permission %q not found in authorization checks", test.permission)
			}
		})
	}
}

func TestStackRoleRoutesDenyInsufficientRoles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		role       authz.Relation
		method     string
		path       string
		body       string
		status     int
		permission authz.Relation
	}{
		{name: "unassigned list is empty", method: http.MethodGet, path: "/v1/tenants/tenant_123/stacks", status: http.StatusOK, permission: authz.RelationCanView},
		{name: "unassigned cannot read stack", method: http.MethodGet, path: "/v1/tenants/tenant_123/stacks/stack_123", status: http.StatusNotFound, permission: authz.RelationCanView},
		{name: "viewer cannot install template", role: authz.RelationViewer, method: http.MethodPost, path: "/v1/tenants/tenant_123/stacks/stack_123/templates", body: `{"template_revision_id":"revision_123","config":{}}`, status: http.StatusForbidden, permission: authz.RelationCanOperate},
		{name: "approver cannot update config", role: authz.RelationApprover, method: http.MethodPatch, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/config", body: `{"config":{}}`, status: http.StatusForbidden, permission: authz.RelationCanOperate},
		{name: "viewer cannot upgrade template", role: authz.RelationViewer, method: http.MethodPost, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/upgrade", body: `{"target_template_revision_id":"revision_123"}`, status: http.StatusForbidden, permission: authz.RelationCanOperate},
		{name: "approver cannot start run", role: authz.RelationApprover, method: http.MethodPost, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", body: `{"operation":"plan"}`, status: http.StatusForbidden, permission: authz.RelationCanOperate},
		{name: "operator cannot approve run", role: authz.RelationOperator, method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/approval", body: `{}`, status: http.StatusForbidden, permission: authz.RelationCanApprove},
		{name: "approver cannot cancel run", role: authz.RelationApprover, method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/cancellation", body: `{}`, status: http.StatusForbidden, permission: authz.RelationCanOperate},
		{name: "unassigned cannot read run", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123", status: http.StatusNotFound, permission: authz.RelationCanView},
		{name: "unassigned cannot list logs", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs", status: http.StatusNotFound, permission: authz.RelationCanView},
		{name: "unassigned cannot read log", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs/plan", status: http.StatusNotFound, permission: authz.RelationCanView},
		{name: "unassigned cannot list run history", method: http.MethodGet, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", status: http.StatusNotFound, permission: authz.RelationCanView},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deps := newPermissionMatrixDependencies()
			deps.authorizer.enforceRole = true
			deps.authorizer.role = test.role
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			request := ordinaryAuthenticatedRequest(test.method, test.path, strings.NewReader(test.body))

			server.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.status, response.Body.String())
			}
			matched := deps.authorizer.check.Relation == test.permission
			if !matched {
				for _, check := range deps.authorizer.batchChecks {
					if check.Relation == test.permission {
						matched = true
						break
					}
				}
			}
			if !matched {
				t.Fatalf("permission %q not found in authorization checks", test.permission)
			}
			if deps.stackTemplateInstaller.created.ID != "" || deps.templateRuns.created.ID != "" || deps.templateRuns.approval.RunID != "" || deps.templateRuns.cancellation.RunID != "" {
				t.Fatal("denied mutation had side effects")
			}
			if len(deps.stackTemplates.gotConfigJSON) != 0 || deps.stackTemplates.gotDesiredTemplateRevisionID != "" || deps.workflows.approvalRunID != "" || deps.workflows.cancelRunID != "" {
				t.Fatal("denied mutation updated state or signaled a workflow")
			}
			if test.name == "unassigned list is empty" {
				var stacks []stackResponse
				if err := json.NewDecoder(response.Body).Decode(&stacks); err != nil {
					t.Fatalf("decode stack list: %v", err)
				}
				if len(stacks) != 0 {
					t.Fatalf("stacks = %#v, want empty", stacks)
				}
			}
		})
	}
}

func TestStackListFiltersMixedDecisions(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stacks.list = []domain.Stack{
		{ID: "stack_allowed", TenantID: "tenant_123", CreatedAt: time.Unix(2, 0)},
		{ID: "stack_denied", TenantID: "tenant_123", CreatedAt: time.Unix(1, 0)},
	}
	deps.authorizer.batchDecisions = []bool{true, false}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := ordinaryAuthenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var stacks []stackResponse
	if err := json.NewDecoder(response.Body).Decode(&stacks); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(stacks) != 1 || stacks[0].ID != "stack_allowed" {
		t.Fatalf("stacks = %#v, want only stack_allowed", stacks)
	}
}

func TestStackListLaterBatchFailureReturnsNoPartialResponse(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stacks.list = make([]domain.Stack, 51)
	for i := range deps.stacks.list {
		deps.stacks.list[i] = domain.Stack{ID: domain.StackID(fmt.Sprintf("stack_%02d", 51-i)), TenantID: "tenant_123", CreatedAt: time.Unix(int64(100-i), 0)}
	}
	deps.authorizer.batchErr = authz.ErrUnavailable
	deps.authorizer.failBatch = 2
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := ordinaryAuthenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error != "authorization_unavailable" {
		t.Fatalf("error = %q, want authorization_unavailable", body.Error)
	}
}

func TestInheritedRouteMissingAndDeniedStatusesMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		status   int
		resource string
	}{
		{name: "config", method: http.MethodPatch, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/config", body: `{"config":{}}`, status: http.StatusForbidden, resource: "stack-template"},
		{name: "upgrade", method: http.MethodPost, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/upgrade", body: `{"target_template_revision_id":"revision_123"}`, status: http.StatusForbidden, resource: "stack-template"},
		{name: "start run", method: http.MethodPost, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", body: `{"operation":"plan"}`, status: http.StatusForbidden, resource: "stack-template"},
		{name: "run detail", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123", status: http.StatusNotFound},
		{name: "approval", method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/approval", body: `{}`, status: http.StatusForbidden},
		{name: "cancellation", method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/cancellation", body: `{}`, status: http.StatusForbidden},
		{name: "log list", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs", status: http.StatusNotFound},
		{name: "log body", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs/plan", status: http.StatusNotFound},
		{name: "run history", method: http.MethodGet, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", status: http.StatusNotFound, resource: "stack-template"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			statuses := make([]int, 0, 2)
			for _, condition := range []string{"missing", "denied"} {
				deps := newPermissionMatrixDependencies()
				if condition == "missing" {
					if test.resource == "stack-template" {
						deps.stackTemplates.getErr = app.ErrNotFound
					} else {
						deps.templateRuns.getErr = app.ErrNotFound
					}
				} else {
					deps.authorizer.denied = true
				}
				server := NewServer(deps.service(), configuredTenantID)
				response := httptest.NewRecorder()
				request := ordinaryAuthenticatedRequest(test.method, test.path, strings.NewReader(test.body))
				server.ServeHTTP(response, request)
				statuses = append(statuses, response.Code)
				if deps.stackTemplateInstaller.created.ID != "" || len(deps.stackTemplates.gotConfigJSON) != 0 || deps.stackTemplates.gotDesiredTemplateRevisionID != "" || deps.templateRuns.created.ID != "" || deps.templateRuns.approval.RunID != "" || deps.templateRuns.cancellation.RunID != "" || deps.workflows.approvalRunID != "" || deps.workflows.cancelRunID != "" {
					t.Fatal("protected missing or denied request had side effects")
				}
			}
			if statuses[0] != test.status || statuses[1] != test.status {
				t.Fatalf("missing=%d denied=%d want=%d", statuses[0], statuses[1], test.status)
			}
		})
	}
}

func newPermissionMatrixDependencies() *apiTestDependencies {
	deps := newAPITestDependencies()
	stack := domain.Stack{ID: "stack_123", TenantID: "tenant_123", Name: "Acme", Slug: "acme", CreatedAt: time.Unix(1, 0)}
	deps.stacks.stack = stack
	deps.stacks.list = []domain.Stack{stack}
	deps.stacks.view = app.StackView{Stack: stack}
	deps.stackTemplates.stackTemplate = domain.StackTemplate{
		ID:                        "stack_template_123",
		TenantID:                  "tenant_123",
		StackID:                   "stack_123",
		DesiredTemplateRevisionID: "revision_123",
		WorkspaceName:             "acme-vpc",
		Lifecycle:                 domain.StackTemplateActive,
	}
	deps.templates.template = domain.TemplateRevision{ID: "revision_123", TenantID: "tenant_123", Status: domain.TemplateRevisionActive}
	deps.templateRuns.run = domain.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}
	deps.templateRuns.list = []domain.TemplateRun{deps.templateRuns.run}
	deps.logMetadata.logs = []domain.TemplateRunLog{{TenantID: "tenant_123", RunID: "run_123", Phase: "plan"}}
	deps.logMetadata.log = domain.TemplateRunLog{TenantID: "tenant_123", RunID: "run_123", Phase: "plan", ObjectKey: "runs/run_123/plan.log"}
	deps.logs.content = []byte("plan output")
	return deps
}

func TestDeniedStackMutationReturnsForbiddenWithoutSideEffects(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.authorizer.denied = true
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := ordinaryAuthenticatedRequest(http.MethodPost, "/v1/tenants/tenant_123/stacks/stack_123/templates", strings.NewReader(`{"template_revision_id":"revision_123","config":{}}`))

	server.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusForbidden, response.Body.String())
	}
	if deps.stackTemplateInstaller.created.ID != "" {
		t.Fatalf("stack template = %#v, want no mutation", deps.stackTemplateInstaller.created)
	}
}

func TestAuthorizationDependencyFailureReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.authorizer.checkErr = authz.ErrUnavailable
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := ordinaryAuthenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack_123", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != "authorization_unavailable" {
		t.Fatalf("error = %q, want authorization_unavailable", body.Error)
	}
}

func TestMissingAuthorizerReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()

	service := app.NewService(app.Service{Stacks: &recordingStackRepository{}})
	server := NewServer(service, configuredTenantID)
	response := httptest.NewRecorder()
	request := ordinaryAuthenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack_123", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
}

func TestCreateStackWithMissingAuthorizerReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()

	stacks := &recordingStackRepository{}
	service := app.NewService(app.Service{Stacks: stacks})
	server := NewServer(service, configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPost, "/v1/tenants/tenant_123/stacks", strings.NewReader(`{"name":"Acme"}`))

	server.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	if stacks.created.ID != "" {
		t.Fatalf("created stack = %#v, want no persistence", stacks.created)
	}
}

func TestPlatformAdminCannotBypassMissingAuthorizer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "stack creation", method: http.MethodPost, path: "/v1/tenants/tenant_123/stacks", body: `{"name":"Acme"}`},
		{name: "stack list", method: http.MethodGet, path: "/v1/tenants/tenant_123/stacks"},
		{name: "stack detail", method: http.MethodGet, path: "/v1/tenants/tenant_123/stacks/stack_123"},
		{name: "stack mutation", method: http.MethodPost, path: "/v1/tenants/tenant_123/stacks/stack_123/templates", body: `{"template_revision_id":"revision_123","config":{}}`},
		{name: "config mutation", method: http.MethodPatch, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/config", body: `{"config":{}}`},
		{name: "upgrade mutation", method: http.MethodPost, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/upgrade", body: `{"target_template_revision_id":"revision_123"}`},
		{name: "start run", method: http.MethodPost, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", body: `{"operation":"plan"}`},
		{name: "inherited read", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123"},
		{name: "approval", method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/approval", body: `{}`},
		{name: "cancellation", method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/cancellation", body: `{}`},
		{name: "log list", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs"},
		{name: "log body", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs/plan"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stacks := &recordingStackRepository{}
			runs := &recordingTemplateRunRepository{}
			installer := &recordingStackTemplateInstaller{}
			service := app.NewService(app.Service{Stacks: stacks, TemplateRuns: runs, StackTemplateInstaller: installer})
			server := NewServer(service, configuredTenantID)
			response := httptest.NewRecorder()
			request := authenticatedRequest(test.method, test.path, strings.NewReader(test.body))

			server.ServeHTTP(response, request)

			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
			}
			if stacks.gotStackID != "" || stacks.gotListTenantID != "" || stacks.created.ID != "" || runs.gotGetRunID != "" || runs.created.ID != "" || runs.approval.RunID != "" || runs.cancellation.RunID != "" || installer.created.ID != "" {
				t.Fatal("missing authorizer allowed repository access or mutation")
			}
		})
	}
}

func TestMalformedStackIDReturnsProtectedNotFound(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := ordinaryAuthenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack:bad", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}
}

func TestPlatformAdminMalformedStackIDReturnsProtectedNotFound(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies().withPlatformTier("admin")
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack:bad", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}
}

func TestMalformedPersistedOwningStackIDReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()

	for _, tier := range []string{"", "admin"} {
		t.Run(tier, func(t *testing.T) {
			t.Parallel()
			deps := newPermissionMatrixDependencies()
			deps.stackTemplates.stackTemplate.StackID = "bad:id"
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			if tier != "" {
				deps.withPlatformTier(tier)
			}
			request := ordinaryAuthenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/template-runs/run_123", nil)

			server.ServeHTTP(response, request)

			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
			}
		})
	}
}

func TestMalformedGeneratedStackIDReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stackID = "bad:id"
	deps.withPlatformTier("editor")
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodPost, "/v1/tenants/tenant_123/stacks", strings.NewReader(`{"name":"Acme"}`))

	server.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	if deps.stacks.created.ID != "" {
		t.Fatalf("created stack = %#v, want no persistence", deps.stacks.created)
	}
}

func TestSearchUsersPlatformAdminAllowed(t *testing.T) {
	t.Parallel()

	expected := []app.UserProfile{
		{Sub: "u1", DisplayName: "Alice Smith", Email: "alice@example.com"},
		{Sub: "u2", DisplayName: "Bob Jones", Email: "bob@example.com"},
	}
	deps := newAPITestDependencies().withPlatformTier("admin")
	deps.users = apiFakeUserRepository{users: expected}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/users/search?q=ali", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body searchUsersResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Users) != 2 {
		t.Fatalf("users = %d, want 2", len(body.Users))
	}
	if body.Users[0].DisplayName != "Alice Smith" {
		t.Fatalf("first user = %q, want Alice Smith", body.Users[0].DisplayName)
	}
	if body.First != 0 {
		t.Fatalf("first = %d, want 0", body.First)
	}
	if body.Max != 20 {
		t.Fatalf("max = %d, want 20", body.Max)
	}
}

func TestSearchUsersNonAdminForbidden(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := ordinaryAuthenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/users/search?q=ali", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestSearchUsersMissingQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
	}{
		{name: "no q param", url: "/v1/tenants/tenant_123/users/search"},
		{name: "empty q", url: "/v1/tenants/tenant_123/users/search?q="},
		{name: "whitespace q", url: "/v1/tenants/tenant_123/users/search?q=+"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			deps := newAPITestDependencies()
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			request := authenticatedRequest(http.MethodGet, test.url, nil)

			server.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}

func TestSearchUsersInvalidPagination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
	}{
		{name: "negative first", url: "/v1/tenants/tenant_123/users/search?q=test&first=-1"},
		{name: "max=0", url: "/v1/tenants/tenant_123/users/search?q=test&max=0"},
		{name: "max=51", url: "/v1/tenants/tenant_123/users/search?q=test&max=51"},
		{name: "non-numeric first", url: "/v1/tenants/tenant_123/users/search?q=test&first=abc"},
		{name: "non-numeric max", url: "/v1/tenants/tenant_123/users/search?q=test&max=abc"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			deps := newAPITestDependencies()
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			request := authenticatedRequest(http.MethodGet, test.url, nil)

			server.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}

// The projection is a local table, so a read failure is a database failure and
// gets the same 500 as any other repository error. There is no longer an
// external directory that can be "unavailable" on its own.
func TestSearchUsersRepositoryFailureIsInternalError(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies().withPlatformTier("admin")
	deps.users = apiFakeUserRepository{searchErr: errors.New("connection refused")}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/users/search?q=test", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != "internal_error" {
		t.Fatalf("error = %q, want internal_error", body.Error)
	}
}

func TestSearchUsersUnauthenticated(t *testing.T) {
	t.Parallel()

	server := NewServer(newAPITestDependencies().service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant_123/users/search?q=test", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
}

type apiTestDependencies struct {
	authorizer             *apiAuthorizer
	stacks                 recordingStackRepository
	stackTemplates         recordingStackTemplateRepository
	stackTemplateInstaller recordingStackTemplateInstaller
	templateRuns           recordingTemplateRunRepository
	registrations          recordingTemplateRegistrationRepository
	templates              recordingTemplateRepository
	logs                   recordingTemplateRunLogReader
	logMetadata            recordingTemplateRunLogRepository
	workflows              recordingWorkflowDispatcher
	users                  apiFakeUserRepository
	stackID                domain.StackID
	stackTemplateID        domain.StackTemplateID
	runID                  domain.TemplateRunID
	registrationID         domain.TemplateRegistrationID
	now                    time.Time
	work                   *apiUnitOfWork
}

func newAPITestDependencies() *apiTestDependencies {
	return &apiTestDependencies{
		authorizer:      &apiAuthorizer{},
		stackID:         domain.StackID("stack_123"),
		stackTemplateID: domain.StackTemplateID("stack_template_123"),
		runID:           domain.TemplateRunID("run_123"),
		registrationID:  domain.TemplateRegistrationID("template_registration_123"),
		now:             time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
	}
}

func (deps *apiTestDependencies) withPlatformTier(tier string) *apiTestDependencies {
	deps.authorizer.platform = map[string]bool{}
	for _, relation := range platformTier(tier) {
		deps.authorizer.platform[relation.String()] = true
	}
	return deps
}

func (deps *apiTestDependencies) withGrants(grants ...authz.Grant) *apiTestDependencies {
	deps.authorizer.grants = grants
	return deps
}

func (deps *apiTestDependencies) service() *app.Service {
	work := &apiUnitOfWork{
		stacks:                &deps.stacks,
		templateRuns:          &deps.templateRuns,
		templateRegistrations: &deps.registrations,
	}
	deps.work = work
	return app.NewService(app.Service{
		Authorizer:               deps.authorizer,
		Work:                     work,
		Stacks:                   &deps.stacks,
		StackTemplates:           &deps.stackTemplates,
		StackTemplateInstaller:   &deps.stackTemplateInstaller,
		TemplateRuns:             &deps.templateRuns,
		TemplateRegistrations:    &deps.registrations,
		TemplateRevisionMetadata: &deps.templates,
		TemplateRevisions:        &deps.templates,
		TemplateRunLogs:          &deps.logs,
		TemplateRunLogMetadata:   &deps.logMetadata,
		Workflows:                &deps.workflows,
		Users:                    &deps.users,
		StackIDs:                 fixedStackIDGenerator{id: deps.stackID},
		StackTemplateIDs:         fixedStackTemplateIDGenerator{id: deps.stackTemplateID},
		RunIDs:                   fixedTemplateRunIDGenerator{runID: deps.runID},
		RegistrationIDs:          fixedTemplateRegistrationIDGenerator{id: deps.registrationID},
		Clock:                    fixedClock{now: deps.now},
	})
}

// apiUnitOfWork applies writes immediately; transactional behaviour is proven
// against a real database in internal/postgres/unitofwork_test.go.
type apiUnitOfWork struct {
	stacks                app.StackRepository
	templateRuns          app.TemplateRunRepository
	templateRegistrations app.TemplateRegistrationRepository
	requests              []queue.Request
	audits                []domain.SecurityAuditEvent
	err                   error
}

func (unit *apiUnitOfWork) InTx(ctx context.Context, fn func(app.TxRepo, queue.Enqueuer) error) error {
	if unit.err != nil {
		return unit.err
	}
	return fn(unit, unit)
}

func (unit *apiUnitOfWork) CreateStack(ctx context.Context, stack domain.Stack) error {
	if unit.stacks == nil {
		return nil
	}
	return unit.stacks.CreateStack(ctx, stack)
}

func (unit *apiUnitOfWork) AppendAuditEvent(_ context.Context, event domain.SecurityAuditEvent) error {
	unit.audits = append(unit.audits, event)
	return nil
}

func (unit *apiUnitOfWork) CreateTemplateRun(ctx context.Context, run domain.TemplateRun) error {
	if repository, ok := unit.templateRuns.(interface {
		CreateTemplateRun(context.Context, domain.TemplateRun) error
	}); ok {
		return repository.CreateTemplateRun(ctx, run)
	}
	return nil
}

func (unit *apiUnitOfWork) CreateTemplateRegistration(ctx context.Context, registration domain.TemplateRegistration) error {
	if repository, ok := unit.templateRegistrations.(interface {
		CreateTemplateRegistration(context.Context, domain.TemplateRegistration) error
	}); ok {
		return repository.CreateTemplateRegistration(ctx, registration)
	}
	return nil
}

func (unit *apiUnitOfWork) ApproveTemplateRun(ctx context.Context, approval domain.TemplateRunApproval) error {
	if repository, ok := unit.templateRuns.(interface {
		ApproveTemplateRun(context.Context, domain.TemplateRunApproval) error
	}); ok {
		return repository.ApproveTemplateRun(ctx, approval)
	}
	return nil
}

func (unit *apiUnitOfWork) RequestTemplateRunCancellation(ctx context.Context, cancellation domain.TemplateRunCancellation) error {
	if repository, ok := unit.templateRuns.(interface {
		RequestTemplateRunCancellation(context.Context, domain.TemplateRunCancellation) error
	}); ok {
		return repository.RequestTemplateRunCancellation(ctx, cancellation)
	}
	return nil
}

func (unit *apiUnitOfWork) Enqueue(_ context.Context, requests ...queue.Request) error {
	unit.requests = append(unit.requests, requests...)
	return nil
}

type apiAuthorizer struct {
	// platform holds the capabilities the test subject was granted on the
	// singleton. Empty is the honest default: a subject holds nothing until a
	// tuple says otherwise.
	platform            map[string]bool
	writeErr            error
	checkErr            error
	denied              bool
	enforceRole         bool
	role                authz.Relation
	check               authz.CheckRequest
	batchErr            error
	failBatch           int
	batchCalls          int
	batchDecisions      []bool
	batchChecks         []authz.CheckRequest
	truncateBatchResult bool
	grants              []authz.Grant
	listGrantsErr       error
	deleteErr           error
}

func (authorizer *apiAuthorizer) roleAllows(request authz.CheckRequest) bool {
	switch authorizer.role {
	case authz.RelationOwner:
		return true
	case authz.RelationOperator:
		return request.Relation == authz.RelationCanView || request.Relation == authz.RelationCanOperate
	case authz.RelationApprover:
		return request.Relation == authz.RelationCanView || request.Relation == authz.RelationCanApprove
	case authz.RelationViewer:
		return request.Relation == authz.RelationCanView
	default:
		return false
	}
}

func (authorizer *apiAuthorizer) Check(_ context.Context, request authz.CheckRequest) (authz.CheckResult, error) {
	authorizer.check = request
	if authorizer.checkErr != nil {
		return authz.CheckResult{}, authorizer.checkErr
	}
	if request.Object == authz.Platform {
		return authz.CheckResult{Allowed: authorizer.platform[request.Relation.String()]}, nil
	}
	if authorizer.enforceRole {
		return authz.CheckResult{Allowed: authorizer.roleAllows(request)}, nil
	}
	return authz.CheckResult{Allowed: !authorizer.denied}, nil
}
func (authorizer *apiAuthorizer) BatchCheck(ctx context.Context, request authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	authorizer.batchCalls++
	authorizer.batchChecks = request.Checks
	if authorizer.batchErr != nil && (authorizer.failBatch == 0 || authorizer.failBatch == authorizer.batchCalls) {
		return authz.BatchCheckResult{}, authorizer.batchErr
	}
	result := authz.BatchCheckResult{Results: make([]authz.CheckResult, len(request.Checks))}
	for i, check := range request.Checks {
		if check.Object == authz.Platform {
			result.Results[i] = authz.CheckResult{Allowed: authorizer.platform[check.Relation.String()]}
		} else if authorizer.batchDecisions != nil && i < len(authorizer.batchDecisions) {
			result.Results[i] = authz.CheckResult{Allowed: authorizer.batchDecisions[i]}
		} else if authorizer.enforceRole {
			result.Results[i] = authz.CheckResult{Allowed: authorizer.roleAllows(check)}
		} else {
			result.Results[i] = authz.CheckResult{Allowed: !authorizer.denied}
		}
	}
	if authorizer.truncateBatchResult && len(result.Results) > 0 {
		result.Results = result.Results[:len(result.Results)-1]
	}
	return result, nil
}
func (authorizer *apiAuthorizer) ListGrants(context.Context, authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	if authorizer.listGrantsErr != nil {
		return authz.ListGrantsResult{}, authorizer.listGrantsErr
	}
	if authorizer.grants == nil {
		return authz.ListGrantsResult{Grants: []authz.Grant{}}, nil
	}
	return authz.ListGrantsResult{Grants: authorizer.grants}, nil
}
func (authorizer *apiAuthorizer) WriteRelationships(context.Context, authz.Mutation) error {
	return authorizer.writeErr
}
func (authorizer *apiAuthorizer) DeleteRelationships(context.Context, authz.Mutation) error {
	return authorizer.deleteErr
}

type recordingStackRepository struct {
	created         domain.Stack
	stack           domain.Stack
	list            []domain.Stack
	view            app.StackView
	gotTenantID     domain.TenantID
	gotStackID      domain.StackID
	gotListTenantID domain.TenantID
	createErr       error
	getErr          error
	listErr         error
	getViewErr      error
}

func (repository *recordingStackRepository) CreateStack(_ context.Context, stack domain.Stack) error {
	if repository.createErr != nil {
		return repository.createErr
	}
	repository.created = stack
	return nil
}

func (repository *recordingStackRepository) GetStack(_ context.Context, tenantID domain.TenantID, stackID domain.StackID) (domain.Stack, error) {
	repository.gotTenantID = tenantID
	repository.gotStackID = stackID
	if repository.getErr != nil {
		return domain.Stack{}, repository.getErr
	}
	return repository.stack, nil
}

func (repository *recordingStackRepository) GetStackWithTemplates(_ context.Context, tenantID domain.TenantID, stackID domain.StackID) (app.StackView, error) {
	repository.gotTenantID = tenantID
	repository.gotStackID = stackID
	if repository.getViewErr != nil {
		return app.StackView{}, repository.getViewErr
	}
	return repository.view, nil
}

func (repository *recordingStackRepository) ListStacks(_ context.Context, tenantID domain.TenantID) ([]domain.Stack, error) {
	repository.gotListTenantID = tenantID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return repository.list, nil
}

func (repository *recordingStackRepository) ListStacksPage(_ context.Context, tenantID domain.TenantID, after *app.StackPageCursor, limit int) ([]domain.Stack, error) {
	repository.gotListTenantID = tenantID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	start := 0
	if after != nil {
		for i, stack := range repository.list {
			if stack.ID == after.ID && stack.CreatedAt.Equal(after.CreatedAt) {
				start = i + 1
				break
			}
		}
	}
	end := min(start+limit, len(repository.list))
	return append([]domain.Stack(nil), repository.list[start:end]...), nil
}

type recordingStackTemplateRepository struct {
	stackTemplate                domain.StackTemplate
	gotTenantID                  domain.TenantID
	gotID                        domain.StackTemplateID
	gotConfigJSON                json.RawMessage
	gotDesiredTemplateRevisionID domain.TemplateRevisionID
	getErr                       error
}

func (repository *recordingStackTemplateRepository) GetStackTemplate(_ context.Context, tenantID domain.TenantID, id domain.StackTemplateID) (domain.StackTemplate, error) {
	repository.gotTenantID = tenantID
	repository.gotID = id
	if repository.getErr != nil {
		return domain.StackTemplate{}, repository.getErr
	}
	stackTemplate := repository.stackTemplate
	if stackTemplate.StackID == "" {
		stackTemplate.StackID = "stack_123"
	}
	return stackTemplate, nil
}

func (repository *recordingStackTemplateRepository) UpdateStackTemplateConfig(_ context.Context, tenantID domain.TenantID, id domain.StackTemplateID, configJSON json.RawMessage) (domain.StackTemplate, error) {
	repository.gotTenantID = tenantID
	repository.gotID = id
	repository.gotConfigJSON = configJSON
	updated := repository.stackTemplate
	updated.DesiredConfigJSON = configJSON
	return updated, nil
}

func (repository *recordingStackTemplateRepository) UpdateStackTemplateDesiredRevision(_ context.Context, tenantID domain.TenantID, id domain.StackTemplateID, templateID domain.TemplateRevisionID, configJSON json.RawMessage) (domain.StackTemplate, error) {
	repository.gotTenantID = tenantID
	repository.gotID = id
	repository.gotDesiredTemplateRevisionID = templateID
	repository.gotConfigJSON = configJSON
	updated := repository.stackTemplate
	updated.DesiredTemplateRevisionID = templateID
	updated.DesiredConfigJSON = configJSON
	return updated, nil
}

type recordingStackTemplateInstaller struct {
	created   domain.StackTemplate
	createErr error
}

func (installer *recordingStackTemplateInstaller) CreateStackTemplate(_ context.Context, stackTemplate domain.StackTemplate) error {
	if installer.createErr != nil {
		return installer.createErr
	}
	installer.created = stackTemplate
	return nil
}

type recordingTemplateRunRepository struct {
	created                domain.TemplateRun
	run                    domain.TemplateRun
	list                   []domain.TemplateRun
	approval               domain.TemplateRunApproval
	cancellation           domain.TemplateRunCancellation
	gotGetTenantID         domain.TenantID
	gotGetRunID            domain.TemplateRunID
	gotListTenantID        domain.TenantID
	gotListStackTemplateID domain.StackTemplateID
	getErr                 error
	createErr              error
	approvalErr            error
	cancellationErr        error
}

func (repository *recordingTemplateRunRepository) CreateTemplateRun(_ context.Context, run domain.TemplateRun) error {
	if repository.createErr != nil {
		return repository.createErr
	}
	repository.created = run
	return nil
}

func (repository *recordingTemplateRunRepository) ListTemplateRuns(_ context.Context, tenantID domain.TenantID, stackTemplateID domain.StackTemplateID) ([]domain.TemplateRun, error) {
	repository.gotListTenantID = tenantID
	repository.gotListStackTemplateID = stackTemplateID
	return repository.list, nil
}

func (repository *recordingTemplateRunRepository) GetTemplateRun(_ context.Context, tenantID domain.TenantID, runID domain.TemplateRunID) (domain.TemplateRun, error) {
	repository.gotGetTenantID = tenantID
	repository.gotGetRunID = runID
	if repository.getErr != nil {
		return domain.TemplateRun{}, repository.getErr
	}
	return repository.run, nil
}

func (repository *recordingTemplateRunRepository) ApproveTemplateRun(_ context.Context, approval domain.TemplateRunApproval) error {
	if repository.approvalErr != nil {
		return repository.approvalErr
	}
	repository.approval = approval
	return nil
}

func (repository *recordingTemplateRunRepository) RequestTemplateRunCancellation(_ context.Context, cancellation domain.TemplateRunCancellation) error {
	if repository.cancellationErr != nil {
		return repository.cancellationErr
	}
	repository.cancellation = cancellation
	return nil
}

func (repository *recordingTemplateRunRepository) ReconcileTemplateRunCancellation(_ context.Context, _ domain.TenantID, _ domain.TemplateRunID, _ string) error {
	return nil
}

type recordingTemplateRunLogReader struct {
	content     []byte
	err         error
	gotTenantID domain.TenantID
	gotRunID    domain.TemplateRunID
	gotPhase    string
}

func (reader *recordingTemplateRunLogReader) ReadTemplateRunLog(_ context.Context, log domain.TemplateRunLog) ([]byte, error) {
	reader.gotTenantID = log.TenantID
	reader.gotRunID = log.RunID
	reader.gotPhase = log.Phase
	if reader.err != nil {
		return nil, reader.err
	}
	return reader.content, nil
}

type recordingTemplateRunLogRepository struct {
	log             domain.TemplateRunLog
	logs            []domain.TemplateRunLog
	gotGetTenantID  domain.TenantID
	gotGetRunID     domain.TemplateRunID
	gotGetPhase     string
	gotListTenantID domain.TenantID
	gotListRunID    domain.TemplateRunID
	getErr          error
	listErr         error
}

func (repository *recordingTemplateRunLogRepository) GetTemplateRunLog(_ context.Context, tenantID domain.TenantID, runID domain.TemplateRunID, phase string) (domain.TemplateRunLog, error) {
	repository.gotGetTenantID = tenantID
	repository.gotGetRunID = runID
	repository.gotGetPhase = phase
	if repository.getErr != nil {
		return domain.TemplateRunLog{}, repository.getErr
	}
	return repository.log, nil
}

func (repository *recordingTemplateRunLogRepository) ListTemplateRunLogs(_ context.Context, tenantID domain.TenantID, runID domain.TemplateRunID) ([]domain.TemplateRunLog, error) {
	repository.gotListTenantID = tenantID
	repository.gotListRunID = runID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return repository.logs, nil
}

type recordingTemplateRegistrationRepository struct {
	created        domain.TemplateRegistration
	registration   domain.TemplateRegistration
	gotGetTenantID domain.TenantID
	gotGetID       domain.TemplateRegistrationID
	createErr      error
	getErr         error
	statusInput    domain.TemplateRegistrationStatusActivityInput
	statusErr      error
}

func (repository *recordingTemplateRegistrationRepository) CreateTemplateRegistration(_ context.Context, registration domain.TemplateRegistration) error {
	if repository.createErr != nil {
		return repository.createErr
	}
	repository.created = registration
	return nil
}

func (repository *recordingTemplateRegistrationRepository) GetTemplateRegistration(_ context.Context, tenantID domain.TenantID, id domain.TemplateRegistrationID) (domain.TemplateRegistration, error) {
	repository.gotGetTenantID = tenantID
	repository.gotGetID = id
	if repository.getErr != nil {
		return domain.TemplateRegistration{}, repository.getErr
	}
	return repository.registration, nil
}

func (repository *recordingTemplateRegistrationRepository) RecordTemplateRegistrationStatus(_ context.Context, input domain.TemplateRegistrationStatusActivityInput) error {
	if repository.statusErr != nil {
		return repository.statusErr
	}
	repository.statusInput = input
	return nil
}

type recordingTemplateRepository struct {
	template                       domain.TemplateRevision
	templates                      []domain.TemplateRevision
	variables                      []domain.TemplateVariable
	gotTemplate                    domain.TemplateRevision
	gotVariables                   []domain.TemplateVariable
	gotListTenantID                domain.TenantID
	gotGetTemplateTenantID         domain.TenantID
	gotGetTemplateRevisionID       domain.TemplateRevisionID
	gotVariablesTenantID           domain.TenantID
	gotVariablesTemplateRevisionID domain.TemplateRevisionID
	getTemplateErr                 error
	listErr                        error
	upsertErr                      error
	variablesErr                   error
}

func (repository *recordingTemplateRepository) UpsertTemplateRevisionWithVariables(_ context.Context, template domain.TemplateRevision, variables []domain.TemplateVariable) (domain.TemplateRevision, error) {
	repository.gotTemplate = template
	repository.gotVariables = variables
	if repository.upsertErr != nil {
		return domain.TemplateRevision{}, repository.upsertErr
	}
	if repository.template.ID != "" {
		return repository.template, nil
	}
	return template, nil
}

func (repository *recordingTemplateRepository) GetTemplateRevision(_ context.Context, tenantID domain.TenantID, templateID domain.TemplateRevisionID) (domain.TemplateRevision, error) {
	repository.gotGetTemplateTenantID = tenantID
	repository.gotGetTemplateRevisionID = templateID
	if repository.getTemplateErr != nil {
		return domain.TemplateRevision{}, repository.getTemplateErr
	}
	return repository.template, nil
}

func (repository *recordingTemplateRepository) ListTemplateRevisions(_ context.Context, tenantID domain.TenantID) ([]domain.TemplateRevision, error) {
	repository.gotListTenantID = tenantID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return repository.templates, nil
}

func (repository *recordingTemplateRepository) GetTemplateRevisionVariables(_ context.Context, tenantID domain.TenantID, templateID domain.TemplateRevisionID) ([]domain.TemplateVariable, error) {
	repository.gotVariablesTenantID = tenantID
	repository.gotVariablesTemplateRevisionID = templateID
	if repository.variablesErr != nil {
		return nil, repository.variablesErr
	}
	return repository.variables, nil
}

type recordingWorkflowDispatcher struct {
	input          domain.TemplateRunWorkflowInput
	syncInput      domain.TemplateSyncWorkflowInput
	approvalRunID  domain.TemplateRunID
	approvalSignal domain.ApprovalSignal
	cancelRunID    domain.TemplateRunID
	cancelSignal   domain.CancelSignal
}

func (dispatcher *recordingWorkflowDispatcher) StartTemplateRun(_ context.Context, input domain.TemplateRunWorkflowInput) error {
	dispatcher.input = input
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) StartTemplateSync(_ context.Context, input domain.TemplateSyncWorkflowInput) error {
	dispatcher.syncInput = input
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) ApproveTemplateRun(_ context.Context, _ domain.TenantID, runID domain.TemplateRunID, signal domain.ApprovalSignal) error {
	dispatcher.approvalRunID = runID
	dispatcher.approvalSignal = signal
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) CancelTemplateRun(_ context.Context, _ domain.TenantID, runID domain.TemplateRunID, signal domain.CancelSignal) error {
	dispatcher.cancelRunID = runID
	dispatcher.cancelSignal = signal
	return nil
}

type fixedStackIDGenerator struct {
	id domain.StackID
}

func (generator fixedStackIDGenerator) NewStackID() domain.StackID {
	return generator.id
}

type fixedStackTemplateIDGenerator struct {
	id domain.StackTemplateID
}

func (generator fixedStackTemplateIDGenerator) NewStackTemplateID() domain.StackTemplateID {
	return generator.id
}

type fixedTemplateRunIDGenerator struct {
	runID domain.TemplateRunID
}

func (generator fixedTemplateRunIDGenerator) NewTemplateRunID() domain.TemplateRunID {
	return generator.runID
}

type fixedTemplateRegistrationIDGenerator struct {
	id domain.TemplateRegistrationID
}

func (generator fixedTemplateRegistrationIDGenerator) NewTemplateRegistrationID() domain.TemplateRegistrationID {
	return generator.id
}

type fixedClock struct {
	now time.Time
}

func (clock fixedClock) Now() time.Time {
	return clock.now
}

type apiFakeUserRepository struct {
	users     []app.UserProfile
	searchErr error
	lookupErr error
	upsertErr error
}

func (f *apiFakeUserRepository) UpsertUser(_ context.Context, profile app.UserProfile, _ time.Time) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.users = append(f.users, profile)
	return nil
}

func (f *apiFakeUserRepository) SearchUsers(_ context.Context, _ string, _, _ int) ([]app.UserProfile, error) {
	return f.users, f.searchErr
}

func (f *apiFakeUserRepository) UsersBySubs(_ context.Context, subs []string) (map[string]app.UserProfile, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	wanted := make(map[string]struct{}, len(subs))
	for _, sub := range subs {
		wanted[sub] = struct{}{}
	}
	found := make(map[string]app.UserProfile)
	for _, user := range f.users {
		if _, ok := wanted[user.Sub]; ok {
			found[user.Sub] = user
		}
	}
	return found, nil
}

func TestMeReturnsIdentityWithGlobalCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		tier       string
		wantAdmin  bool
		wantCreate bool
	}{
		// An administrator holds can_create_stack too: the model derives it
		// from can_edit, which can_administer satisfies.
		{name: "platform admin", tier: "admin", wantAdmin: true, wantCreate: true},
		{name: "editor", tier: "editor", wantAdmin: false, wantCreate: true},
		{name: "no tier", tier: "", wantAdmin: false, wantCreate: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			deps := newAPITestDependencies().withPlatformTier(test.tier)
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
			ctx := authn.ContextWithPrincipal(request.Context(), authn.Principal{
				Subject: apiKeycloakSubject,
				Name:    "Test User",
				Email:   "test@example.com",
			})
			request = request.WithContext(ctx)

			server.ServeHTTP(response, request)

			// 200 with the identity, not 204: every assertion below reads the
			// body, and the web client calls this endpoint expecting one.
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
			}

			var body struct {
				Sub                string `json:"sub"`
				DisplayName        string `json:"displayName"`
				Email              string `json:"email"`
				GlobalCapabilities struct {
					IsPlatformAdmin bool `json:"isPlatformAdmin"`
					CanCreateStack  bool `json:"canCreateStack"`
				} `json:"globalCapabilities"`
				TenantID string `json:"tenantID"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}

			if body.Sub != apiKeycloakSubject {
				t.Errorf("sub = %q, want %q", body.Sub, apiKeycloakSubject)
			}
			if body.DisplayName != "Test User" {
				t.Errorf("displayName = %q, want %q", body.DisplayName, "Test User")
			}
			if body.Email != "test@example.com" {
				t.Errorf("email = %q, want %q", body.Email, "test@example.com")
			}
			if body.GlobalCapabilities.IsPlatformAdmin != test.wantAdmin {
				t.Errorf("isPlatformAdmin = %t, want %t", body.GlobalCapabilities.IsPlatformAdmin, test.wantAdmin)
			}
			if body.GlobalCapabilities.CanCreateStack != test.wantCreate {
				t.Errorf("canCreateStack = %t, want %t", body.GlobalCapabilities.CanCreateStack, test.wantCreate)
			}
			if body.TenantID != string(configuredTenantID) {
				t.Errorf("tenantID = %q, want %q", body.TenantID, string(configuredTenantID))
			}
		})
	}
}

func TestMeReturnsUnauthorizedWithoutPrincipal(t *testing.T) {
	t.Parallel()

	server := NewServer(app.NewService(app.Service{}), configuredTenantID)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
}

func testGrant(t *testing.T, sub, stackID, role string) authz.Grant {
	t.Helper()
	subject, err := authz.SubjectFromOIDCSub(sub)
	if err != nil {
		t.Fatalf("subject from sub: %v", err)
	}
	stack, err := authz.ObjectFromID(authz.TypeStack, stackID)
	if err != nil {
		t.Fatalf("stack from id: %v", err)
	}
	r, err := authz.GrantRelation(role)
	if err != nil {
		t.Fatalf("role from relation: %v", err)
	}
	grant, err := authz.NewGrant(subject, stack, r)
	if err != nil {
		t.Fatalf("new grant: %v", err)
	}
	return grant
}

func TestListStackGrantsListsGrants(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.users = apiFakeUserRepository{
		users: []app.UserProfile{{Sub: "user-1", DisplayName: "Alice", Email: "alice@example.com"}},
	}
	deps.withGrants(testGrant(t, "user-1", "stack_123", "owner"))
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack_123/grants", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body listStackGrantsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Grants) != 1 {
		t.Fatalf("len(grants) = %d, want 1", len(body.Grants))
	}
	if body.Grants[0].UserSub != "user-1" || body.Grants[0].Role != "owner" {
		t.Fatalf("grant = %#v", body.Grants[0])
	}
	if body.Grants[0].DisplayName != "Alice" {
		t.Fatalf("display name = %q, want Alice", body.Grants[0].DisplayName)
	}
}

func TestListStackGrantsReturnsEmptyOnNoGrants(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack_123/grants", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body listStackGrantsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Grants) != 0 {
		t.Fatalf("len(grants) = %d, want 0", len(body.Grants))
	}
}

func TestAssignStackRoleAssignsRoleAndReturnsGrantView(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.users = apiFakeUserRepository{
		users: []app.UserProfile{{Sub: "user-2", DisplayName: "bob", Email: "bob@example.com"}},
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stacks/stack_123/grants",
		strings.NewReader(`{"user_sub":"user-2","role":"operator"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body app.GrantView
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.UserSub != "user-2" || body.Role != "operator" {
		t.Fatalf("grant view = %#v", body)
	}
}

func TestAssignStackRoleRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stacks/stack_123/grants",
		strings.NewReader(`{"user_sub":`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestAssignStackRoleRequiresManageAccess(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.authorizer.denied = true
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := ordinaryAuthenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stacks/stack_123/grants",
		strings.NewReader(`{"user_sub":"user-2","role":"viewer"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestRevokeStackRoleRemovesGrant(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.withGrants(testGrant(t, "user-3", "stack_123", "viewer"))
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodDelete, "/v1/tenants/tenant_123/stacks/stack_123/grants/user-3", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNoContent, response.Body.String())
	}
}

func TestRevokeStackRoleLastOwnerReturnsConflict(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.withGrants(testGrant(t, apiKeycloakSubject, "stack_123", "owner"))
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodDelete, "/v1/tenants/tenant_123/stacks/stack_123/grants/"+apiKeycloakSubject, nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusConflict, response.Body.String())
	}
}

func TestAssignStackRoleLastOwnerDemotionReturnsConflict(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.users = apiFakeUserRepository{
		users: []app.UserProfile{{Sub: apiKeycloakSubject, DisplayName: "admin", Email: "admin@example.com"}},
	}
	deps.withGrants(testGrant(t, apiKeycloakSubject, "stack_123", "owner"))
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stacks/stack_123/grants",
		strings.NewReader(`{"user_sub":"`+apiKeycloakSubject+`","role":"viewer"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusConflict, response.Body.String())
	}
}

func TestAuthenticatedServerProtectsMeRoute(t *testing.T) {
	server := NewAuthenticatedServer(app.NewService(app.Service{}), configuredTenantID, false)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
}

type stubQueueReader struct {
	statuses     []queue.Status
	tenantID     string
	actorSubject string
	err          error
}

func (reader *stubQueueReader) ListByActor(_ context.Context, tenantID, actorSubject string, _ int) ([]queue.Status, error) {
	reader.tenantID = tenantID
	reader.actorSubject = actorSubject
	return reader.statuses, reader.err
}

func TestListQueueReturnsOnlyCallerItems(t *testing.T) {
	t.Parallel()

	reader := &stubQueueReader{statuses: []queue.Status{{
		Kind:      "reconcile_stack_grant",
		State:     queue.StatePending,
		Attempts:  3,
		LastError: "openfga unavailable",
		CreatedAt: time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC),
	}}}
	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID, WithQueueReader(reader))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/queue", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}

	var body struct {
		Items []struct {
			Kind      string `json:"kind"`
			State     string `json:"state"`
			Summary   string `json:"summary"`
			Attempts  int    `json:"attempts"`
			LastError string `json:"last_error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(body.Items))
	}
	// Retry is forever, so a stuck item stays pending; attempts and last_error
	// are the only way to tell "just queued" from "failing for an hour".
	if body.Items[0].Attempts != 3 || body.Items[0].LastError != "openfga unavailable" {
		t.Fatalf("item = %+v, want attempts and last_error surfaced", body.Items[0])
	}
	if body.Items[0].Summary != "reconcile_stack_grant" {
		t.Fatalf("summary = %q, want the kind as fallback", body.Items[0].Summary)
	}
	if reader.tenantID != "tenant_123" || reader.actorSubject != apiKeycloakSubject {
		t.Fatalf("reader called with tenant %q actor %q, want the authenticated caller", reader.tenantID, reader.actorSubject)
	}
}

func TestListQueueWithoutReaderReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/queue", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestListQueueRequiresAuthentication(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service(), configuredTenantID, WithQueueReader(&stubQueueReader{}))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant_123/queue", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}
