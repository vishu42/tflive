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
	stackTemplates recordingStackTemplateRepository
	templateRuns   recordingTemplateRunRepository
	workflows      recordingWorkflowDispatcher
	runID          traits.TemplateRunID
	now            time.Time
}

func newAPITestDependencies() *apiTestDependencies {
	return &apiTestDependencies{
		runID: traits.TemplateRunID("run_123"),
		now:   time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
	}
}

func (deps *apiTestDependencies) service() *app.Service {
	return app.NewService(app.Service{
		StackTemplates: &deps.stackTemplates,
		TemplateRuns:   &deps.templateRuns,
		Workflows:      &deps.workflows,
		RunIDs:         fixedTemplateRunIDGenerator{runID: deps.runID},
		Clock:          fixedClock{now: deps.now},
	})
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

type recordingTemplateRunRepository struct {
	created         traits.TemplateRun
	approval        traits.TemplateRunApproval
	cancellation    traits.TemplateRunCancellation
	approvalErr     error
	cancellationErr error
}

func (repository *recordingTemplateRunRepository) CreateTemplateRun(_ context.Context, run traits.TemplateRun) error {
	repository.created = run
	return nil
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

type recordingWorkflowDispatcher struct {
	input         traits.TemplateRunWorkflowInput
	approvalRunID traits.TemplateRunID
	cancelRunID   traits.TemplateRunID
}

func (dispatcher *recordingWorkflowDispatcher) StartTemplateRun(_ context.Context, input traits.TemplateRunWorkflowInput) error {
	dispatcher.input = input
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

type fixedTemplateRunIDGenerator struct {
	runID traits.TemplateRunID
}

func (generator fixedTemplateRunIDGenerator) NewTemplateRunID() traits.TemplateRunID {
	return generator.runID
}

type fixedClock struct {
	now time.Time
}

func (clock fixedClock) Now() time.Time {
	return clock.now
}
