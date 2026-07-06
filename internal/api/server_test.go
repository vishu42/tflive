package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vishu42/megagega/internal/app"
	"github.com/vishu42/megagega/internal/traits"
)

func TestHealthzReturnsOK(t *testing.T) {
	t.Parallel()

	server := NewServer(app.NewService(app.Service{}))
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

func TestStartTemplateRunCallsService(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 3, 11, 30, 0, 0, time.UTC)
	deps := newAPITestDependencies()
	deps.stackTemplates.stackTemplate = traits.StackTemplate{
		ID:            traits.StackTemplateID("stack_template_123"),
		StackID:       traits.StackID("stack_123"),
		TemplateID:    traits.TemplateID("template_123"),
		SelectedRef:   "main",
		WorkspaceName: "smoke-workspace",
		Lifecycle:     traits.StackTemplateActive,
	}
	deps.runID = traits.TemplateRunID("run_123")
	deps.now = startedAt
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stack-templates/stack_template_123/runs",
		strings.NewReader(`{"operation":"plan","trigger_actor":"user_123"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if deps.stackTemplates.gotTenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q", deps.stackTemplates.gotTenantID)
	}
	if deps.stackTemplates.gotID != traits.StackTemplateID("stack_template_123") {
		t.Fatalf("stack template id = %q", deps.stackTemplates.gotID)
	}
	if deps.templateRuns.created.Operation != traits.OperationPlan {
		t.Fatalf("operation = %q", deps.templateRuns.created.Operation)
	}
	if deps.templateRuns.created.TriggerActor != traits.UserID("user_123") {
		t.Fatalf("trigger actor = %q", deps.templateRuns.created.TriggerActor)
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

	server := NewServer(newAPITestDependencies().service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
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

	server := NewServer(newAPITestDependencies().service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stack-templates/stack_template_123/runs",
		strings.NewReader(`{"operation":"refresh","trigger_actor":"user_123"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestRegisterTemplateCallsService(t *testing.T) {
	t.Parallel()

	requestedAt := time.Date(2026, 7, 6, 11, 30, 0, 0, time.UTC)
	deps := newAPITestDependencies()
	deps.registrationID = traits.TemplateRegistrationID("template_registration_123")
	deps.now = requestedAt
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/templates",
		strings.NewReader(`{"repo_owner":"acme","repo_name":"infra-templates","source_ref":"v0.0.1","root_path":"modules/vpc","requested_by":"user_123"}`),
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

	server := NewServer(newAPITestDependencies().service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/templates",
		strings.NewReader(`{"repo_owner":`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestRegisterTemplateMapsInvalidCommandToBadRequest(t *testing.T) {
	t.Parallel()

	server := NewServer(newAPITestDependencies().service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/templates",
		strings.NewReader(`{"repo_owner":"acme","repo_name":"infra-templates","root_path":"modules/vpc","requested_by":"user_123"}`),
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
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stacks",
		strings.NewReader(`{"name":"Acme Prod","tags":{"env":"prod"},"default_credential_ids":["credential_123"],"actor":"user_123"}`),
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
				ID:            traits.StackTemplateID("stack_template_123"),
				TenantID:      traits.TenantID("tenant_123"),
				StackID:       traits.StackID("stack_123"),
				TemplateID:    traits.TemplateID("template_123"),
				SelectedRef:   "main",
				WorkspaceName: "meg_acme_prod_late_123",
				ConfigJSON:    json.RawMessage(`{"region":"us-east-1"}`),
				Lifecycle:     traits.StackTemplateActive,
			},
		},
	}
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant_123/stacks/stack_123", nil)

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
	deps.templates.template = traits.Template{ID: traits.TemplateID("template_123"), TenantID: traits.TenantID("tenant_123"), Status: traits.TemplateActive}
	deps.templates.variables = []traits.TemplateVariable{{Name: "region", Required: true}}
	deps.stackTemplateID = traits.StackTemplateID("stack_template_a1b2c3d4")
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/stacks/stack_123/templates",
		strings.NewReader(`{"template_id":"template_123","selected_ref":"main","config":{"region":"us-east-1"},"actor":"user_123"}`),
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if deps.stackTemplateInstaller.created.StackID != traits.StackID("stack_123") {
		t.Fatalf("stack id = %q, want stack_123", deps.stackTemplateInstaller.created.StackID)
	}

	var body stackTemplateResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != "stack_template_a1b2c3d4" {
		t.Fatalf("response id = %q, want stack_template_a1b2c3d4", body.ID)
	}
	if body.Config["region"] != "us-east-1" {
		t.Fatalf("response config = %#v", body.Config)
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
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/tenants/tenant_123/template-registrations/template_registration_123",
		nil,
	)

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

func TestGetTemplateVariablesReturnsVariables(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	deps.templates.variables = []traits.TemplateVariable{
		{
			TemplateID:     traits.TemplateID("template_123"),
			Name:           "region",
			TypeExpression: "string",
			Required:       true,
		},
	}
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/tenants/tenant_123/templates/template_123/variables",
		nil,
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if deps.templates.gotVariablesTemplateID != traits.TemplateID("template_123") {
		t.Fatalf("template id = %q, want template_123", deps.templates.gotVariablesTemplateID)
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
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/tenants/tenant_123/template-runs/run_123",
		nil,
	)

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
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/tenants/tenant_123/template-runs/run_123/logs/plan",
		nil,
	)

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
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/tenants/tenant_123/template-runs/run_123/logs",
		nil,
	)

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
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/tenants/tenant_123/template-runs/run_123/logs",
		nil,
	)

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

	server := NewServer(newAPITestDependencies().service())
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
	server := NewServer(deps.service())
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
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/tenants/tenant_123/template-runs/run_123/logs/plan",
		nil,
	)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestApproveRunCallsService(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/template-runs/run_123/approval",
		strings.NewReader(`{"approved_by":"user_123"}`),
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
	if deps.templateRuns.approval.ApprovedBy != traits.UserID("user_123") {
		t.Fatalf("approved by = %q", deps.templateRuns.approval.ApprovedBy)
	}
	if deps.workflows.approvalRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("workflow run id = %q", deps.workflows.approvalRunID)
	}
}

func TestCancelRunCallsService(t *testing.T) {
	t.Parallel()

	deps := newAPITestDependencies()
	server := NewServer(deps.service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/tenant_123/template-runs/run_123/cancellation",
		strings.NewReader(`{"requested_by":"user_123","reason":"testing"}`),
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
	if deps.templateRuns.cancellation.RequestedBy != traits.UserID("user_123") {
		t.Fatalf("requested by = %q", deps.templateRuns.cancellation.RequestedBy)
	}
	if deps.templateRuns.cancellation.Reason != "testing" {
		t.Fatalf("reason = %q", deps.templateRuns.cancellation.Reason)
	}
	if deps.workflows.cancelRunID != traits.TemplateRunID("run_123") {
		t.Fatalf("workflow run id = %q", deps.workflows.cancelRunID)
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
			body: `{"approved_by":"user_123"}`,
			configure: func(deps *apiTestDependencies) {
				deps.templateRuns.approvalErr = app.ErrRunNotApprovable
			},
			statusCode: http.StatusConflict,
		},
		{
			name: "cancellation",
			path: "/v1/tenants/tenant_123/template-runs/run_123/cancellation",
			body: `{"requested_by":"user_123"}`,
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
			server := NewServer(deps.service())
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))

			server.ServeHTTP(response, request)

			if response.Code != tt.statusCode {
				t.Fatalf("status = %d, want %d", response.Code, tt.statusCode)
			}
		})
	}
}

func TestUnknownRouteReturnsNotFound(t *testing.T) {
	t.Parallel()

	server := NewServer(newAPITestDependencies().service())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/nope", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

type apiTestDependencies struct {
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
		stackID:         traits.StackID("stack_123"),
		stackTemplateID: traits.StackTemplateID("stack_template_123"),
		runID:           traits.TemplateRunID("run_123"),
		registrationID:  traits.TemplateRegistrationID("template_registration_123"),
		now:             time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
	}
}

func (deps *apiTestDependencies) service() *app.Service {
	return app.NewService(app.Service{
		Stacks:                 &deps.stacks,
		StackTemplates:         &deps.stackTemplates,
		StackTemplateInstaller: &deps.stackTemplateInstaller,
		TemplateRuns:           &deps.templateRuns,
		TemplateRegistrations:  &deps.registrations,
		TemplateMetadata:       &deps.templates,
		Templates:              &deps.templates,
		TemplateRunLogs:        &deps.logs,
		TemplateRunLogMetadata: &deps.logMetadata,
		Workflows:              &deps.workflows,
		StackIDs:               fixedStackIDGenerator{id: deps.stackID},
		StackTemplateIDs:       fixedStackTemplateIDGenerator{id: deps.stackTemplateID},
		RunIDs:                 fixedTemplateRunIDGenerator{runID: deps.runID},
		RegistrationIDs:        fixedTemplateRegistrationIDGenerator{id: deps.registrationID},
		Clock:                  fixedClock{now: deps.now},
	})
}

type recordingStackRepository struct {
	created     traits.Stack
	stack       traits.Stack
	view        app.StackView
	gotTenantID traits.TenantID
	gotStackID  traits.StackID
	createErr   error
	getErr      error
	getViewErr  error
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

type recordingStackTemplateRepository struct {
	stackTemplate traits.StackTemplate
	gotTenantID   traits.TenantID
	gotID         traits.StackTemplateID
}

func (repository *recordingStackTemplateRepository) GetStackTemplate(_ context.Context, tenantID traits.TenantID, id traits.StackTemplateID) (traits.StackTemplate, error) {
	repository.gotTenantID = tenantID
	repository.gotID = id
	return repository.stackTemplate, nil
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
	template               traits.Template
	variables              []traits.TemplateVariable
	gotTemplate            traits.Template
	gotVariables           []traits.TemplateVariable
	gotGetTemplateTenantID traits.TenantID
	gotGetTemplateID       traits.TemplateID
	gotVariablesTenantID   traits.TenantID
	gotVariablesTemplateID traits.TemplateID
	getTemplateErr         error
	upsertErr              error
	variablesErr           error
}

func (repository *recordingTemplateRepository) UpsertTemplateWithVariables(_ context.Context, template traits.Template, variables []traits.TemplateVariable) (traits.Template, error) {
	repository.gotTemplate = template
	repository.gotVariables = variables
	if repository.upsertErr != nil {
		return traits.Template{}, repository.upsertErr
	}
	if repository.template.ID != "" {
		return repository.template, nil
	}
	return template, nil
}

func (repository *recordingTemplateRepository) GetTemplate(_ context.Context, tenantID traits.TenantID, templateID traits.TemplateID) (traits.Template, error) {
	repository.gotGetTemplateTenantID = tenantID
	repository.gotGetTemplateID = templateID
	if repository.getTemplateErr != nil {
		return traits.Template{}, repository.getTemplateErr
	}
	return repository.template, nil
}

func (repository *recordingTemplateRepository) GetTemplateVariables(_ context.Context, tenantID traits.TenantID, templateID traits.TemplateID) ([]traits.TemplateVariable, error) {
	repository.gotVariablesTenantID = tenantID
	repository.gotVariablesTemplateID = templateID
	if repository.variablesErr != nil {
		return nil, repository.variablesErr
	}
	return repository.variables, nil
}

type recordingWorkflowDispatcher struct {
	input         traits.TemplateRunWorkflowInput
	syncInput     traits.TemplateSyncWorkflowInput
	approvalRunID traits.TemplateRunID
	cancelRunID   traits.TemplateRunID
}

func (dispatcher *recordingWorkflowDispatcher) StartTemplateRun(_ context.Context, input traits.TemplateRunWorkflowInput) error {
	dispatcher.input = input
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) StartTemplateSync(_ context.Context, input traits.TemplateSyncWorkflowInput) error {
	dispatcher.syncInput = input
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) ApproveTemplateRun(_ context.Context, _ traits.TenantID, runID traits.TemplateRunID, _ traits.ApprovalSignal) error {
	dispatcher.approvalRunID = runID
	return nil
}

func (dispatcher *recordingWorkflowDispatcher) CancelTemplateRun(_ context.Context, _ traits.TenantID, runID traits.TemplateRunID, _ traits.CancelSignal) error {
	dispatcher.cancelRunID = runID
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
