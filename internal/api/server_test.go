package api

import (
	"context"
	"encoding/json"
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
	"github.com/vishu42/tflive/internal/traits"
)

const apiKeycloakSubject = "6fdb4b4c-2a8f-4cf7-945f-38f67f6a0e91"
const configuredTenantID = traits.TenantID("tenant_123")

func authenticatedRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	ctx := authn.ContextWithPrincipal(request.Context(), authn.Principal{
		Subject: apiKeycloakSubject, RealmRoles: []string{"platform-admin"},
	})
	return request.WithContext(ctx)
}

func ordinaryAuthenticatedRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	ctx := authn.ContextWithPrincipal(request.Context(), authn.Principal{Subject: apiKeycloakSubject})
	return request.WithContext(ctx)
}

func requestWithGlobalRole(method, target string, body io.Reader, role string) *http.Request {
	request := httptest.NewRequest(method, target, body)
	ctx := authn.ContextWithPrincipal(request.Context(), authn.Principal{Subject: apiKeycloakSubject, RealmRoles: []string{role}})
	return request.WithContext(ctx)
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
	server := NewAuthenticatedServer(app.NewService(app.Service{}), apiTestVerifier{}, configuredTenantID)

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

	server := NewAuthenticatedServer(nil, apiTestVerifier{}, configuredTenantID)
	path := "/v1/tenants/tenant_other/stacks"

	unauthenticated := httptest.NewRecorder()
	server.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, path, nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthenticated.Code, http.StatusUnauthorized)
	}

	authenticatedRequest := httptest.NewRequest(http.MethodGet, path, nil)
	authenticatedRequest.Header.Set("Authorization", "Bearer test-token")
	authenticated := httptest.NewRecorder()
	server.ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusNotFound {
		t.Fatalf("authenticated status = %d, want %d", authenticated.Code, http.StatusNotFound)
	}
}

func TestAuthenticatedServerAllowsConfiguredTenantToReachService(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.stacks.list = []traits.Stack{{
		ID:       traits.StackID("stack_123"),
		TenantID: configuredTenantID,
		Name:     "Acme Prod",
		Slug:     "acme-prod",
	}}
	server := NewAuthenticatedServer(deps.service(), apiTestVerifier{}, configuredTenantID)
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks", nil)
	request.Header.Set("Authorization", "Bearer test-token")
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

func TestStartTemplateRunCallsService(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 3, 11, 30, 0, 0, time.UTC)
	deps := newAPITestDependencies()
	deps.stackTemplates.stackTemplate = traits.StackTemplate{
		ID:                        traits.StackTemplateID("stack_template_123"),
		StackID:                   traits.StackID("stack_123"),
		DesiredTemplateRevisionID: traits.TemplateRevisionID("template_123"),
		SelectedRef:               "main",
		WorkspaceName:             "smoke-workspace",
		Lifecycle:                 traits.StackTemplateActive,
	}
	deps.runID = traits.TemplateRunID("run_123")
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
	if deps.stackTemplates.gotTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q", deps.stackTemplates.gotTenantID)
	}
	if deps.stackTemplates.gotID != traits.StackTemplateID("stack_template_123") {
		t.Fatalf("stack template revision id = %q", deps.stackTemplates.gotID)
	}
	if deps.templateRuns.created.Operation != traits.OperationPlan {
		t.Fatalf("operation = %q", deps.templateRuns.created.Operation)
	}
	if deps.templateRuns.created.TriggerActor != traits.UserID(apiKeycloakSubject) {
		t.Fatalf("trigger actor = %q, want %q", deps.templateRuns.created.TriggerActor, apiKeycloakSubject)
	}

	var body traits.TemplateRun
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != traits.TemplateRunID("run_123") {
		t.Fatalf("id = %q, want run_123", body.ID)
	}
	if body.Status != traits.TemplateRunQueued {
		t.Fatalf("status = %q, want queued", body.Status)
	}
	if !body.StartedAt.Equal(startedAt) {
		t.Fatalf("started_at = %v, want %v", body.StartedAt, startedAt)
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
	deps := newAPITestDependencies()
	deps.registrationID = traits.TemplateRegistrationID("template_registration_123")
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
	if deps.registrations.created.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q", deps.registrations.created.TenantID)
	}
	if deps.registrations.created.RepoOwner != "acme" {
		t.Fatalf("repo owner = %q", deps.registrations.created.RepoOwner)
	}
	if deps.registrations.created.SourceRef != "v0.0.1" {
		t.Fatalf("source ref = %q", deps.registrations.created.SourceRef)
	}
	if deps.registrations.created.RequestedBy != traits.UserID(apiKeycloakSubject) {
		t.Fatalf("requested by = %q, want %q", deps.registrations.created.RequestedBy, apiKeycloakSubject)
	}
	if deps.workflows.syncInput.RegistrationID != traits.TemplateRegistrationID("template_registration_123") {
		t.Fatalf("workflow registration id = %q", deps.workflows.syncInput.RegistrationID)
	}

	var body traits.TemplateRegistration
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != traits.TemplateRegistrationID("template_registration_123") {
		t.Fatalf("id = %q, want template_registration_123", body.ID)
	}
	if body.Status != traits.TemplateRegistrationPending {
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

	server := NewServer(newAPITestDependencies().service(), configuredTenantID)
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
	deps := newAPITestDependencies()
	deps.stackID = traits.StackID("stack_123")
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
	if deps.stacks.created.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q, want tenant_123", deps.stacks.created.TenantID)
	}
	if deps.stacks.created.Slug != "acme-prod" {
		t.Fatalf("slug = %q, want acme-prod", deps.stacks.created.Slug)
	}
	if deps.stacks.created.CreatedBy != traits.UserID(apiKeycloakSubject) {
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

func TestCreateStackMapsOwnerWriteFailureAfterPersistence(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.authorizer.writeErr = authz.ErrUnavailable
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, authenticatedRequest(http.MethodPost, "/v1/tenants/tenant_123/stacks", strings.NewReader(`{"name":"Acme"}`)))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if deps.stacks.created.ID == "" {
		t.Fatal("owner-write failure did not preserve the persisted stack")
	}
}

func TestListStacksReturnsTenantStacks(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 6, 13, 30, 0, 0, time.UTC)
	deps := newAPITestDependencies()
	deps.stacks.list = []traits.Stack{
		{
			ID:        traits.StackID("stack_123"),
			TenantID:  traits.TenantID("tenant_123"),
			Name:      "Acme Prod",
			Slug:      "acme-prod",
			Tags:      map[string]string{"env": "prod"},
			CreatedBy: traits.UserID("user_123"),
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
	if deps.stacks.gotListTenantID != traits.TenantID("tenant_123") {
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
		Stack: traits.Stack{
			ID:       traits.StackID("stack_123"),
			TenantID: traits.TenantID("tenant_123"),
			Name:     "Acme Prod",
			Slug:     "acme-prod",
			Tags:     map[string]string{"env": "prod"},
		},
		Templates: []traits.StackTemplate{
			{
				ID:                        traits.StackTemplateID("stack_template_123"),
				TenantID:                  traits.TenantID("tenant_123"),
				StackID:                   traits.StackID("stack_123"),
				DesiredTemplateRevisionID: traits.TemplateRevisionID("template_123"),
				SelectedRef:               "main",
				WorkspaceName:             "meg_acme_prod_late_123",
				ConfigJSON:                json.RawMessage(`{"region":"us-east-1"}`),
				Lifecycle:                 traits.StackTemplateActive,
			},
		},
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack_123", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.stacks.gotStackID != traits.StackID("stack_123") {
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
	deps.stacks.stack = traits.Stack{ID: traits.StackID("stack_123"), TenantID: traits.TenantID("tenant_123"), Slug: "acme-prod"}
	deps.templates.template = traits.TemplateRevision{ID: traits.TemplateRevisionID("template_123"), TenantID: traits.TenantID("tenant_123"), Status: traits.TemplateRevisionActive}
	deps.templates.variables = []traits.TemplateVariable{{Name: "region", Required: true}}
	deps.stackTemplateID = traits.StackTemplateID("stack_template_a1b2c3d4")
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stacks/stack_123/templates",
		strings.NewReader(`{"template_revision_id":"template_123","selected_ref":"main","config":{"region":"us-east-1"}}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if deps.stackTemplateInstaller.created.StackID != traits.StackID("stack_123") {
		t.Fatalf("stack id = %q, want stack_123", deps.stackTemplateInstaller.created.StackID)
	}
	if deps.stackTemplateInstaller.created.CreatedBy != traits.UserID(apiKeycloakSubject) {
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
	deps.stackTemplates.stackTemplate = traits.StackTemplate{
		ID:                        traits.StackTemplateID("stack_template_123"),
		TenantID:                  traits.TenantID("tenant_123"),
		DesiredTemplateRevisionID: traits.TemplateRevisionID("template_rev_1"),
		Lifecycle:                 traits.StackTemplateActive,
	}
	deps.templates.variables = []traits.TemplateVariable{{Name: "region", Required: true}}
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
	deps.stackTemplates.stackTemplate = traits.StackTemplate{
		ID:                        traits.StackTemplateID("stack_template_123"),
		TenantID:                  traits.TenantID("tenant_123"),
		SourceTemplateID:          traits.SourceTemplateID("source_template_vpc"),
		DesiredTemplateRevisionID: traits.TemplateRevisionID("template_rev_1"),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		Lifecycle:                 traits.StackTemplateActive,
	}
	deps.templates.template = traits.TemplateRevision{
		ID:               traits.TemplateRevisionID("template_rev_2"),
		TenantID:         traits.TenantID("tenant_123"),
		SourceTemplateID: traits.SourceTemplateID("source_template_vpc"),
		Status:           traits.TemplateRevisionActive,
	}
	deps.templates.variables = []traits.TemplateVariable{{Name: "region", Required: true}}
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
	if deps.stackTemplates.gotDesiredTemplateRevisionID != traits.TemplateRevisionID("template_rev_2") {
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
	deps.stackTemplates.stackTemplate = traits.StackTemplate{
		ID:                        traits.StackTemplateID("stack_template_123"),
		TenantID:                  traits.TenantID("tenant_123"),
		SourceTemplateID:          traits.SourceTemplateID("source_template_vpc"),
		DesiredTemplateRevisionID: traits.TemplateRevisionID("template_rev_1"),
		DesiredConfigJSON:         json.RawMessage(`{"region":"us-east-1"}`),
		Lifecycle:                 traits.StackTemplateActive,
	}
	deps.templates.template = traits.TemplateRevision{
		ID:               traits.TemplateRevisionID("template_rev_2"),
		TenantID:         traits.TenantID("tenant_123"),
		SourceTemplateID: traits.SourceTemplateID("source_template_vpc"),
		Status:           traits.TemplateRevisionActive,
	}
	deps.templates.variables = []traits.TemplateVariable{
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
	deps.stackTemplates.stackTemplate = traits.StackTemplate{
		ID:               traits.StackTemplateID("stack_template_123"),
		TenantID:         traits.TenantID("tenant_123"),
		SourceTemplateID: traits.SourceTemplateID("source_template_vpc"),
		Lifecycle:        traits.StackTemplateActive,
	}
	deps.templates.template = traits.TemplateRevision{
		ID:               traits.TemplateRevisionID("template_rev_2"),
		TenantID:         traits.TenantID("tenant_123"),
		SourceTemplateID: traits.SourceTemplateID("source_template_db"),
		Status:           traits.TemplateRevisionActive,
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

	deps := newAPITestDependencies()
	deps.registrations.registration = traits.TemplateRegistration{
		ID:       traits.TemplateRegistrationID("template_registration_123"),
		TenantID: traits.TenantID("tenant_123"),
		Status:   traits.TemplateRegistrationCompleted,
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/template-registrations/template_registration_123", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.registrations.gotGetTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q, want tenant_123", deps.registrations.gotGetTenantID)
	}

	var body traits.TemplateRegistration
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != traits.TemplateRegistrationID("template_registration_123") {
		t.Fatalf("id = %q, want template_registration_123", body.ID)
	}
}

func TestListTemplateRevisionsReturnsTenantTemplateRevisions(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	deps := newAPITestDependencies()
	deps.templates.templates = []traits.TemplateRevision{
		{
			ID:                traits.TemplateRevisionID("template_123"),
			TenantID:          traits.TenantID("tenant_123"),
			RepoOwner:         "acme",
			RepoName:          "infra-templates",
			SourceRef:         "main",
			ResolvedCommitSHA: "abc123",
			RootPath:          ".",
			Name:              "infra-templates",
			Tags:              []string{"aws"},
			Status:            traits.TemplateRevisionActive,
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
	if deps.templates.gotListTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant list lookup = %q, want tenant_123", deps.templates.gotListTenantID)
	}

	var body []traits.TemplateRevision
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("len(body) = %d, want 1", len(body))
	}
	if body[0].ID != traits.TemplateRevisionID("template_123") || body[0].Status != traits.TemplateRevisionActive {
		t.Fatalf("template revision response = %#v", body[0])
	}
}

func TestGetTemplateRevisionVariablesReturnsVariables(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.templates.variables = []traits.TemplateVariable{
		{
			TemplateRevisionID: traits.TemplateRevisionID("template_123"),
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
	if deps.templates.gotVariablesTemplateRevisionID != traits.TemplateRevisionID("template_123") {
		t.Fatalf("template revision id = %q, want template_123", deps.templates.gotVariablesTemplateRevisionID)
	}

	var body []traits.TemplateVariable
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
	deps.templateRuns.run = traits.TemplateRun{
		ID:              traits.TemplateRunID("run_123"),
		TenantID:        traits.TenantID("tenant_123"),
		StackTemplateID: traits.StackTemplateID("stack_template_123"),
		Operation:       traits.OperationPlan,
		Status:          traits.TemplateRunCompleted,
	}
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/v1/tenants/tenant_123/template-runs/run_123", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.templateRuns.gotGetTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q, want tenant_123", deps.templateRuns.gotGetTenantID)
	}
	if deps.templateRuns.gotGetRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("run id = %q, want run_123", deps.templateRuns.gotGetRunID)
	}

	var body traits.TemplateRun
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != traits.TemplateRunID("run_123") {
		t.Fatalf("id = %q, want run_123", body.ID)
	}
	if body.Status != traits.TemplateRunCompleted {
		t.Fatalf("status = %q, want completed", body.Status)
	}
}

func TestGetTemplateRunLogReturnsPlainText(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.templateRuns.run = traits.TemplateRun{
		ID:       traits.TemplateRunID("run_123"),
		TenantID: traits.TenantID("tenant_123"),
	}
	deps.logs.content = []byte("terraform plan output\n")
	deps.logMetadata.log = traits.TemplateRunLog{
		TenantID:  traits.TenantID("tenant_123"),
		RunID:     traits.TemplateRunID("run_123"),
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
	deps.templateRuns.run = traits.TemplateRun{
		ID:       traits.TemplateRunID("run_123"),
		TenantID: traits.TenantID("tenant_123"),
	}
	deps.logMetadata.logs = []traits.TemplateRunLog{
		{
			TenantID:    traits.TenantID("tenant_123"),
			RunID:       traits.TemplateRunID("run_123"),
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
	if deps.logMetadata.gotListRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("metadata run ID = %q, want run_123", deps.logMetadata.gotListRunID)
	}

	var body []traits.TemplateRunLog
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
	deps.templateRuns.run = traits.TemplateRun{
		ID:       traits.TemplateRunID("run_123"),
		TenantID: traits.TenantID("tenant_123"),
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
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/tenants/tenant_123/template-runs/run_123",
		nil,
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestGetTemplateRunLogMapsMissingLogToNotFound(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.templateRuns.run = traits.TemplateRun{
		ID:       traits.TemplateRunID("run_123"),
		TenantID: traits.TenantID("tenant_123"),
	}
	deps.logs.err = app.ErrNotFound
	deps.logMetadata.log = traits.TemplateRunLog{
		TenantID:  traits.TenantID("tenant_123"),
		RunID:     traits.TemplateRunID("run_123"),
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
	if deps.templateRuns.approval.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q", deps.templateRuns.approval.TenantID)
	}
	if deps.templateRuns.approval.RunID != traits.TemplateRunID("run_123") {
		t.Fatalf("run id = %q", deps.templateRuns.approval.RunID)
	}
	if deps.templateRuns.approval.ApprovedBy != traits.UserID(apiKeycloakSubject) {
		t.Fatalf("approved by = %q, want %q", deps.templateRuns.approval.ApprovedBy, apiKeycloakSubject)
	}
	if deps.workflows.approvalRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("workflow run id = %q", deps.workflows.approvalRunID)
	}
	if deps.workflows.approvalSignal.ApprovedBy != traits.UserID(apiKeycloakSubject) {
		t.Fatalf("workflow approved by = %q, want %q", deps.workflows.approvalSignal.ApprovedBy, apiKeycloakSubject)
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
	if deps.templateRuns.cancellation.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q", deps.templateRuns.cancellation.TenantID)
	}
	if deps.templateRuns.cancellation.RunID != traits.TemplateRunID("run_123") {
		t.Fatalf("run id = %q", deps.templateRuns.cancellation.RunID)
	}
	if deps.templateRuns.cancellation.RequestedBy != traits.UserID(apiKeycloakSubject) {
		t.Fatalf("requested by = %q, want %q", deps.templateRuns.cancellation.RequestedBy, apiKeycloakSubject)
	}
	if deps.templateRuns.cancellation.Reason != "testing" {
		t.Fatalf("reason = %q", deps.templateRuns.cancellation.Reason)
	}
	if deps.workflows.cancelRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("workflow run id = %q", deps.workflows.cancelRunID)
	}
	if deps.workflows.cancelSignal.RequestedBy != traits.UserID(apiKeycloakSubject) {
		t.Fatalf("workflow requested by = %q, want %q", deps.workflows.cancelSignal.RequestedBy, apiKeycloakSubject)
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

	for _, role := range []string{"platform-admin", "stack-creator"} {
		for _, route := range routes {
			t.Run(role+" "+route.name, func(t *testing.T) {
				t.Parallel()
				deps := newAPITestDependencies()
				server := NewServer(deps.service(), configuredTenantID)
				response := httptest.NewRecorder()
				request := requestWithGlobalRole(route.method, route.path, strings.NewReader(route.body), role)

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

	for _, role := range []string{"platform-admin", "stack-creator"} {
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			deps := newAPITestDependencies()
			server := NewServer(deps.service(), configuredTenantID)
			response := httptest.NewRecorder()
			request := requestWithGlobalRole(http.MethodPost, "/v1/tenants/tenant_123/stacks", strings.NewReader(`{"name":"Acme"}`), role)

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
		role       authz.Role
		method     string
		path       string
		body       string
		status     int
		permission authz.Permission
	}{
		{name: "viewer lists stacks", role: authz.RoleViewer, method: http.MethodGet, path: "/v1/tenants/tenant_123/stacks", status: http.StatusOK, permission: authz.PermissionView},
		{name: "viewer reads stack", role: authz.RoleViewer, method: http.MethodGet, path: "/v1/tenants/tenant_123/stacks/stack_123", status: http.StatusOK, permission: authz.PermissionView},
		{name: "operator installs template", role: authz.RoleOperator, method: http.MethodPost, path: "/v1/tenants/tenant_123/stacks/stack_123/templates", body: `{"template_revision_id":"revision_123","selected_ref":"main","config":{}}`, status: http.StatusCreated, permission: authz.PermissionOperate},
		{name: "owner operates config", role: authz.RoleOwner, method: http.MethodPatch, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/config", body: `{"config":{}}`, status: http.StatusOK, permission: authz.PermissionOperate},
		{name: "operator upgrades template", role: authz.RoleOperator, method: http.MethodPost, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/upgrade", body: `{"target_template_revision_id":"revision_123"}`, status: http.StatusOK, permission: authz.PermissionOperate},
		{name: "operator starts run", role: authz.RoleOperator, method: http.MethodPost, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", body: `{"operation":"plan"}`, status: http.StatusCreated, permission: authz.PermissionOperate},
		{name: "approver approves run", role: authz.RoleApprover, method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/approval", body: `{}`, status: http.StatusNoContent, permission: authz.PermissionApprove},
		{name: "owner cancels run", role: authz.RoleOwner, method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/cancellation", body: `{}`, status: http.StatusNoContent, permission: authz.PermissionOperate},
		{name: "viewer reads run", role: authz.RoleViewer, method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123", status: http.StatusOK, permission: authz.PermissionView},
		{name: "approver lists run logs", role: authz.RoleApprover, method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs", status: http.StatusOK, permission: authz.PermissionView},
		{name: "viewer reads run log", role: authz.RoleViewer, method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs/plan", status: http.StatusOK, permission: authz.PermissionView},
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
			if deps.authorizer.check.Permission != test.permission {
				t.Fatalf("permission = %q, want %q", deps.authorizer.check.Permission, test.permission)
			}
		})
	}
}

func TestStackRoleRoutesDenyInsufficientRoles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		role       authz.Role
		method     string
		path       string
		body       string
		status     int
		permission authz.Permission
	}{
		{name: "unassigned list is empty", method: http.MethodGet, path: "/v1/tenants/tenant_123/stacks", status: http.StatusOK, permission: authz.PermissionView},
		{name: "unassigned cannot read stack", method: http.MethodGet, path: "/v1/tenants/tenant_123/stacks/stack_123", status: http.StatusNotFound, permission: authz.PermissionView},
		{name: "viewer cannot install template", role: authz.RoleViewer, method: http.MethodPost, path: "/v1/tenants/tenant_123/stacks/stack_123/templates", body: `{"template_revision_id":"revision_123","selected_ref":"main","config":{}}`, status: http.StatusForbidden, permission: authz.PermissionOperate},
		{name: "approver cannot update config", role: authz.RoleApprover, method: http.MethodPatch, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/config", body: `{"config":{}}`, status: http.StatusForbidden, permission: authz.PermissionOperate},
		{name: "viewer cannot upgrade template", role: authz.RoleViewer, method: http.MethodPost, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/upgrade", body: `{"target_template_revision_id":"revision_123"}`, status: http.StatusForbidden, permission: authz.PermissionOperate},
		{name: "approver cannot start run", role: authz.RoleApprover, method: http.MethodPost, path: "/v1/tenants/tenant_123/stack-templates/stack_template_123/runs", body: `{"operation":"plan"}`, status: http.StatusForbidden, permission: authz.PermissionOperate},
		{name: "operator cannot approve run", role: authz.RoleOperator, method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/approval", body: `{}`, status: http.StatusForbidden, permission: authz.PermissionApprove},
		{name: "approver cannot cancel run", role: authz.RoleApprover, method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/cancellation", body: `{}`, status: http.StatusForbidden, permission: authz.PermissionOperate},
		{name: "unassigned cannot read run", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123", status: http.StatusNotFound, permission: authz.PermissionView},
		{name: "unassigned cannot list logs", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs", status: http.StatusNotFound, permission: authz.PermissionView},
		{name: "unassigned cannot read log", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs/plan", status: http.StatusNotFound, permission: authz.PermissionView},
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
			if deps.authorizer.check.Permission != test.permission {
				t.Fatalf("permission = %q, want %q", deps.authorizer.check.Permission, test.permission)
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
	deps.stacks.list = []traits.Stack{
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
	deps.stacks.list = make([]traits.Stack, 51)
	for i := range deps.stacks.list {
		deps.stacks.list[i] = traits.Stack{ID: traits.StackID(fmt.Sprintf("stack_%02d", 51-i)), TenantID: "tenant_123", CreatedAt: time.Unix(int64(100-i), 0)}
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
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "approval", method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/approval", body: `{}`, status: http.StatusForbidden},
		{name: "cancellation", method: http.MethodPost, path: "/v1/tenants/tenant_123/template-runs/run_123/cancellation", body: `{}`, status: http.StatusForbidden},
		{name: "log list", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs", status: http.StatusNotFound},
		{name: "log body", method: http.MethodGet, path: "/v1/tenants/tenant_123/template-runs/run_123/logs/plan", status: http.StatusNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			statuses := make([]int, 0, 2)
			for _, condition := range []string{"missing", "denied"} {
				deps := newPermissionMatrixDependencies()
				if condition == "missing" {
					deps.templateRuns.getErr = app.ErrNotFound
				} else {
					deps.authorizer.denied = true
				}
				server := NewServer(deps.service(), configuredTenantID)
				response := httptest.NewRecorder()
				request := ordinaryAuthenticatedRequest(test.method, test.path, strings.NewReader(test.body))
				server.ServeHTTP(response, request)
				statuses = append(statuses, response.Code)
			}
			if statuses[0] != test.status || statuses[1] != test.status {
				t.Fatalf("missing=%d denied=%d want=%d", statuses[0], statuses[1], test.status)
			}
		})
	}
}

func newPermissionMatrixDependencies() *apiTestDependencies {
	deps := newAPITestDependencies()
	stack := traits.Stack{ID: "stack_123", TenantID: "tenant_123", Name: "Acme", Slug: "acme", CreatedAt: time.Unix(1, 0)}
	deps.stacks.stack = stack
	deps.stacks.list = []traits.Stack{stack}
	deps.stacks.view = app.StackView{Stack: stack}
	deps.stackTemplates.stackTemplate = traits.StackTemplate{
		ID:                        "stack_template_123",
		TenantID:                  "tenant_123",
		StackID:                   "stack_123",
		DesiredTemplateRevisionID: "revision_123",
		SelectedRef:               "main",
		WorkspaceName:             "acme-vpc",
		Lifecycle:                 traits.StackTemplateActive,
	}
	deps.templates.template = traits.TemplateRevision{ID: "revision_123", TenantID: "tenant_123", Status: traits.TemplateRevisionActive}
	deps.templateRuns.run = traits.TemplateRun{ID: "run_123", TenantID: "tenant_123", StackTemplateID: "stack_template_123"}
	deps.logMetadata.logs = []traits.TemplateRunLog{{TenantID: "tenant_123", RunID: "run_123", Phase: "plan"}}
	deps.logMetadata.log = traits.TemplateRunLog{TenantID: "tenant_123", RunID: "run_123", Phase: "plan", ObjectKey: "runs/run_123/plan.log"}
	deps.logs.content = []byte("plan output")
	return deps
}

func TestDeniedStackMutationReturnsForbiddenWithoutSideEffects(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.authorizer.denied = true
	server := NewServer(deps.service(), configuredTenantID)
	response := httptest.NewRecorder()
	request := ordinaryAuthenticatedRequest(http.MethodPost, "/v1/tenants/tenant_123/stacks/stack_123/templates", strings.NewReader(`{"template_revision_id":"revision_123","selected_ref":"main","config":{}}`))

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
	request := requestWithGlobalRole(http.MethodPost, "/v1/tenants/tenant_123/stacks", strings.NewReader(`{"name":"Acme"}`), "stack-creator")

	server.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	if stacks.created.ID != "" {
		t.Fatalf("created stack = %#v, want no persistence", stacks.created)
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
	stackID                traits.StackID
	stackTemplateID        traits.StackTemplateID
	runID                  traits.TemplateRunID
	registrationID         traits.TemplateRegistrationID
	now                    time.Time
}

func newAPITestDependencies() *apiTestDependencies {
	return &apiTestDependencies{
		authorizer:      &apiAuthorizer{},
		stackID:         traits.StackID("stack_123"),
		stackTemplateID: traits.StackTemplateID("stack_template_123"),
		runID:           traits.TemplateRunID("run_123"),
		registrationID:  traits.TemplateRegistrationID("template_registration_123"),
		now:             time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
	}
}

func (deps *apiTestDependencies) service() *app.Service {
	return app.NewService(app.Service{
		Authorizer:               deps.authorizer,
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
		StackIDs:                 fixedStackIDGenerator{id: deps.stackID},
		StackTemplateIDs:         fixedStackTemplateIDGenerator{id: deps.stackTemplateID},
		RunIDs:                   fixedTemplateRunIDGenerator{runID: deps.runID},
		RegistrationIDs:          fixedTemplateRegistrationIDGenerator{id: deps.registrationID},
		Clock:                    fixedClock{now: deps.now},
	})
}

type apiAuthorizer struct {
	writeErr            error
	checkErr            error
	denied              bool
	enforceRole         bool
	role                authz.Role
	check               authz.CheckRequest
	batchErr            error
	failBatch           int
	batchCalls          int
	batchDecisions      []bool
	truncateBatchResult bool
}

func (authorizer *apiAuthorizer) Check(_ context.Context, request authz.CheckRequest) (authz.CheckResult, error) {
	authorizer.check = request
	if authorizer.checkErr != nil {
		return authz.CheckResult{}, authorizer.checkErr
	}
	if authorizer.enforceRole {
		allowed := request.Permission == authz.PermissionView
		switch authorizer.role {
		case authz.RoleOwner:
			allowed = true
		case authz.RoleOperator:
			allowed = request.Permission == authz.PermissionView || request.Permission == authz.PermissionOperate
		case authz.RoleApprover:
			allowed = request.Permission == authz.PermissionView || request.Permission == authz.PermissionApprove
		case authz.RoleViewer:
			allowed = request.Permission == authz.PermissionView
		default:
			allowed = false
		}
		return authz.CheckResult{Allowed: allowed}, nil
	}
	return authz.CheckResult{Allowed: !authorizer.denied}, nil
}
func (authorizer *apiAuthorizer) BatchCheck(ctx context.Context, request authz.BatchCheckRequest) (authz.BatchCheckResult, error) {
	authorizer.batchCalls++
	if authorizer.batchErr != nil && (authorizer.failBatch == 0 || authorizer.failBatch == authorizer.batchCalls) {
		return authz.BatchCheckResult{}, authorizer.batchErr
	}
	result := authz.BatchCheckResult{Results: make([]authz.CheckResult, len(request.Checks))}
	for i, check := range request.Checks {
		if authorizer.batchDecisions != nil && i < len(authorizer.batchDecisions) {
			result.Results[i] = authz.CheckResult{Allowed: authorizer.batchDecisions[i]}
		} else {
			decision, err := authorizer.Check(ctx, check)
			if err != nil {
				return authz.BatchCheckResult{}, err
			}
			result.Results[i] = decision
		}
	}
	if authorizer.truncateBatchResult && len(result.Results) > 0 {
		result.Results = result.Results[:len(result.Results)-1]
	}
	return result, nil
}
func (authorizer apiAuthorizer) ListAccessibleStacks(context.Context, authz.ListAccessibleStacksRequest) (authz.ListAccessibleStacksResult, error) {
	if authorizer.denied {
		return authz.ListAccessibleStacksResult{}, nil
	}
	stack, _ := authz.StackFromID("stack_123")
	return authz.ListAccessibleStacksResult{Stacks: []authz.Stack{stack}}, nil
}
func (apiAuthorizer) ListGrants(context.Context, authz.ListGrantsRequest) (authz.ListGrantsResult, error) {
	return authz.ListGrantsResult{}, nil
}
func (authorizer *apiAuthorizer) WriteRelationships(context.Context, authz.Mutation) error {
	return authorizer.writeErr
}
func (apiAuthorizer) DeleteRelationships(context.Context, authz.Mutation) error { return nil }

type recordingStackRepository struct {
	created         traits.Stack
	stack           traits.Stack
	list            []traits.Stack
	view            app.StackView
	gotTenantID     traits.TenantID
	gotStackID      traits.StackID
	gotListTenantID traits.TenantID
	createErr       error
	getErr          error
	listErr         error
	getViewErr      error
}

func (repository *recordingStackRepository) CreateStack(_ context.Context, stack traits.Stack) error {
	if repository.createErr != nil {
		return repository.createErr
	}
	repository.created = stack
	return nil
}

func (repository *recordingStackRepository) GetStack(_ context.Context, tenantID traits.TenantID, stackID traits.StackID) (traits.Stack, error) {
	repository.gotTenantID = tenantID
	repository.gotStackID = stackID
	if repository.getErr != nil {
		return traits.Stack{}, repository.getErr
	}
	return repository.stack, nil
}

func (repository *recordingStackRepository) GetStackWithTemplates(_ context.Context, tenantID traits.TenantID, stackID traits.StackID) (app.StackView, error) {
	repository.gotTenantID = tenantID
	repository.gotStackID = stackID
	if repository.getViewErr != nil {
		return app.StackView{}, repository.getViewErr
	}
	return repository.view, nil
}

func (repository *recordingStackRepository) ListStacks(_ context.Context, tenantID traits.TenantID) ([]traits.Stack, error) {
	repository.gotListTenantID = tenantID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return repository.list, nil
}

func (repository *recordingStackRepository) ListStacksPage(_ context.Context, tenantID traits.TenantID, after *app.StackPageCursor, limit int) ([]traits.Stack, error) {
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
	return append([]traits.Stack(nil), repository.list[start:end]...), nil
}

type recordingStackTemplateRepository struct {
	stackTemplate                traits.StackTemplate
	gotTenantID                  traits.TenantID
	gotID                        traits.StackTemplateID
	gotConfigJSON                json.RawMessage
	gotDesiredTemplateRevisionID traits.TemplateRevisionID
	getErr                       error
}

func (repository *recordingStackTemplateRepository) GetStackTemplate(_ context.Context, tenantID traits.TenantID, id traits.StackTemplateID) (traits.StackTemplate, error) {
	repository.gotTenantID = tenantID
	repository.gotID = id
	if repository.getErr != nil {
		return traits.StackTemplate{}, repository.getErr
	}
	return repository.stackTemplate, nil
}

func (repository *recordingStackTemplateRepository) UpdateStackTemplateConfig(_ context.Context, tenantID traits.TenantID, id traits.StackTemplateID, configJSON json.RawMessage) (traits.StackTemplate, error) {
	repository.gotTenantID = tenantID
	repository.gotID = id
	repository.gotConfigJSON = configJSON
	updated := repository.stackTemplate
	updated.DesiredConfigJSON = configJSON
	return updated, nil
}

func (repository *recordingStackTemplateRepository) UpdateStackTemplateDesiredRevision(_ context.Context, tenantID traits.TenantID, id traits.StackTemplateID, templateID traits.TemplateRevisionID, configJSON json.RawMessage) (traits.StackTemplate, error) {
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
	created   traits.StackTemplate
	createErr error
}

func (installer *recordingStackTemplateInstaller) CreateStackTemplate(_ context.Context, stackTemplate traits.StackTemplate) error {
	if installer.createErr != nil {
		return installer.createErr
	}
	installer.created = stackTemplate
	return nil
}

type recordingTemplateRunRepository struct {
	created         traits.TemplateRun
	run             traits.TemplateRun
	approval        traits.TemplateRunApproval
	cancellation    traits.TemplateRunCancellation
	gotGetTenantID  traits.TenantID
	gotGetRunID     traits.TemplateRunID
	getErr          error
	approvalErr     error
	cancellationErr error
}

func (repository *recordingTemplateRunRepository) CreateTemplateRun(_ context.Context, run traits.TemplateRun) error {
	repository.created = run
	return nil
}

func (repository *recordingTemplateRunRepository) GetTemplateRun(_ context.Context, tenantID traits.TenantID, runID traits.TemplateRunID) (traits.TemplateRun, error) {
	repository.gotGetTenantID = tenantID
	repository.gotGetRunID = runID
	if repository.getErr != nil {
		return traits.TemplateRun{}, repository.getErr
	}
	return repository.run, nil
}

func (repository *recordingTemplateRunRepository) ApproveTemplateRun(_ context.Context, approval traits.TemplateRunApproval) error {
	if repository.approvalErr != nil {
		return repository.approvalErr
	}
	repository.approval = approval
	return nil
}

func (repository *recordingTemplateRunRepository) RequestTemplateRunCancellation(_ context.Context, cancellation traits.TemplateRunCancellation) error {
	if repository.cancellationErr != nil {
		return repository.cancellationErr
	}
	repository.cancellation = cancellation
	return nil
}

type recordingTemplateRunLogReader struct {
	content     []byte
	err         error
	gotTenantID traits.TenantID
	gotRunID    traits.TemplateRunID
	gotPhase    string
}

func (reader *recordingTemplateRunLogReader) ReadTemplateRunLog(_ context.Context, log traits.TemplateRunLog) ([]byte, error) {
	reader.gotTenantID = log.TenantID
	reader.gotRunID = log.RunID
	reader.gotPhase = log.Phase
	if reader.err != nil {
		return nil, reader.err
	}
	return reader.content, nil
}

type recordingTemplateRunLogRepository struct {
	log             traits.TemplateRunLog
	logs            []traits.TemplateRunLog
	gotGetTenantID  traits.TenantID
	gotGetRunID     traits.TemplateRunID
	gotGetPhase     string
	gotListTenantID traits.TenantID
	gotListRunID    traits.TemplateRunID
	getErr          error
	listErr         error
}

func (repository *recordingTemplateRunLogRepository) GetTemplateRunLog(_ context.Context, tenantID traits.TenantID, runID traits.TemplateRunID, phase string) (traits.TemplateRunLog, error) {
	repository.gotGetTenantID = tenantID
	repository.gotGetRunID = runID
	repository.gotGetPhase = phase
	if repository.getErr != nil {
		return traits.TemplateRunLog{}, repository.getErr
	}
	return repository.log, nil
}

func (repository *recordingTemplateRunLogRepository) ListTemplateRunLogs(_ context.Context, tenantID traits.TenantID, runID traits.TemplateRunID) ([]traits.TemplateRunLog, error) {
	repository.gotListTenantID = tenantID
	repository.gotListRunID = runID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return repository.logs, nil
}

type recordingTemplateRegistrationRepository struct {
	created        traits.TemplateRegistration
	registration   traits.TemplateRegistration
	gotGetTenantID traits.TenantID
	gotGetID       traits.TemplateRegistrationID
	createErr      error
	getErr         error
	statusInput    traits.TemplateRegistrationStatusActivityInput
	statusErr      error
}

func (repository *recordingTemplateRegistrationRepository) CreateTemplateRegistration(_ context.Context, registration traits.TemplateRegistration) error {
	if repository.createErr != nil {
		return repository.createErr
	}
	repository.created = registration
	return nil
}

func (repository *recordingTemplateRegistrationRepository) GetTemplateRegistration(_ context.Context, tenantID traits.TenantID, id traits.TemplateRegistrationID) (traits.TemplateRegistration, error) {
	repository.gotGetTenantID = tenantID
	repository.gotGetID = id
	if repository.getErr != nil {
		return traits.TemplateRegistration{}, repository.getErr
	}
	return repository.registration, nil
}

func (repository *recordingTemplateRegistrationRepository) RecordTemplateRegistrationStatus(_ context.Context, input traits.TemplateRegistrationStatusActivityInput) error {
	if repository.statusErr != nil {
		return repository.statusErr
	}
	repository.statusInput = input
	return nil
}

type recordingTemplateRepository struct {
	template                       traits.TemplateRevision
	templates                      []traits.TemplateRevision
	variables                      []traits.TemplateVariable
	gotTemplate                    traits.TemplateRevision
	gotVariables                   []traits.TemplateVariable
	gotListTenantID                traits.TenantID
	gotGetTemplateTenantID         traits.TenantID
	gotGetTemplateRevisionID       traits.TemplateRevisionID
	gotVariablesTenantID           traits.TenantID
	gotVariablesTemplateRevisionID traits.TemplateRevisionID
	getTemplateErr                 error
	listErr                        error
	upsertErr                      error
	variablesErr                   error
}

func (repository *recordingTemplateRepository) UpsertTemplateRevisionWithVariables(_ context.Context, template traits.TemplateRevision, variables []traits.TemplateVariable) (traits.TemplateRevision, error) {
	repository.gotTemplate = template
	repository.gotVariables = variables
	if repository.upsertErr != nil {
		return traits.TemplateRevision{}, repository.upsertErr
	}
	if repository.template.ID != "" {
		return repository.template, nil
	}
	return template, nil
}

func (repository *recordingTemplateRepository) GetTemplateRevision(_ context.Context, tenantID traits.TenantID, templateID traits.TemplateRevisionID) (traits.TemplateRevision, error) {
	repository.gotGetTemplateTenantID = tenantID
	repository.gotGetTemplateRevisionID = templateID
	if repository.getTemplateErr != nil {
		return traits.TemplateRevision{}, repository.getTemplateErr
	}
	return repository.template, nil
}

func (repository *recordingTemplateRepository) ListTemplateRevisions(_ context.Context, tenantID traits.TenantID) ([]traits.TemplateRevision, error) {
	repository.gotListTenantID = tenantID
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	return repository.templates, nil
}

func (repository *recordingTemplateRepository) GetTemplateRevisionVariables(_ context.Context, tenantID traits.TenantID, templateID traits.TemplateRevisionID) ([]traits.TemplateVariable, error) {
	repository.gotVariablesTenantID = tenantID
	repository.gotVariablesTemplateRevisionID = templateID
	if repository.variablesErr != nil {
		return nil, repository.variablesErr
	}
	return repository.variables, nil
}

type recordingWorkflowDispatcher struct {
	input          traits.TemplateRunWorkflowInput
	syncInput      traits.TemplateSyncWorkflowInput
	approvalRunID  traits.TemplateRunID
	approvalSignal traits.ApprovalSignal
	cancelRunID    traits.TemplateRunID
	cancelSignal   traits.CancelSignal
}

func (dispatcher *recordingWorkflowDispatcher) StartTemplateRun(_ context.Context, input traits.TemplateRunWorkflowInput) error {
	dispatcher.input = input
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) StartTemplateSync(_ context.Context, input traits.TemplateSyncWorkflowInput) error {
	dispatcher.syncInput = input
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) ApproveTemplateRun(_ context.Context, _ traits.TenantID, runID traits.TemplateRunID, signal traits.ApprovalSignal) error {
	dispatcher.approvalRunID = runID
	dispatcher.approvalSignal = signal
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) CancelTemplateRun(_ context.Context, _ traits.TenantID, runID traits.TemplateRunID, signal traits.CancelSignal) error {
	dispatcher.cancelRunID = runID
	dispatcher.cancelSignal = signal
	return nil
}

type fixedStackIDGenerator struct {
	id traits.StackID
}

func (generator fixedStackIDGenerator) NewStackID() traits.StackID {
	return generator.id
}

type fixedStackTemplateIDGenerator struct {
	id traits.StackTemplateID
}

func (generator fixedStackTemplateIDGenerator) NewStackTemplateID() traits.StackTemplateID {
	return generator.id
}

type fixedTemplateRunIDGenerator struct {
	runID traits.TemplateRunID
}

func (generator fixedTemplateRunIDGenerator) NewTemplateRunID() traits.TemplateRunID {
	return generator.runID
}

type fixedTemplateRegistrationIDGenerator struct {
	id traits.TemplateRegistrationID
}

func (generator fixedTemplateRegistrationIDGenerator) NewTemplateRegistrationID() traits.TemplateRegistrationID {
	return generator.id
}

type fixedClock struct {
	now time.Time
}

func (clock fixedClock) Now() time.Time {
	return clock.now
}
