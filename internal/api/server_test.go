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

	server := NewServer(&recordingService{})
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
	service := &recordingService{
		startRun: traits.TemplateRun{
			ID:              traits.TemplateRunID("run_123"),
			TenantID:        traits.TenantID("tenant_123"),
			StackTemplateID: traits.StackTemplateID("stack_template_123"),
			Operation:       traits.OperationPlan,
			SelectedRef:     "main",
			WorkspaceName:   "smoke-workspace",
			Status:          traits.TemplateRunQueued,
			TriggerActor:    traits.UserID("user_123"),
			StartedAt:       startedAt,
		},
	}
	server := NewServer(service)
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
	if service.startCommand.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q", service.startCommand.TenantID)
	}
	if service.startCommand.StackTemplateID != traits.StackTemplateID("stack_template_123") {
		t.Fatalf("stack template id = %q", service.startCommand.StackTemplateID)
	}
	if service.startCommand.Operation != traits.OperationPlan {
		t.Fatalf("operation = %q", service.startCommand.Operation)
	}
	if service.startCommand.TriggerActor != traits.UserID("user_123") {
		t.Fatalf("trigger actor = %q", service.startCommand.TriggerActor)
	}

	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["id"] != "run_123" {
		t.Fatalf("id = %v, want run_123", body["id"])
	}
	if body["status"] != "queued" {
		t.Fatalf("status = %v, want queued", body["status"])
	}
	if body["started_at"] != startedAt.Format(time.RFC3339Nano) {
		t.Fatalf("started_at = %v", body["started_at"])
	}
}

func TestStartTemplateRunRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	server := NewServer(&recordingService{})
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

	service := &recordingService{startErr: app.ErrInvalidCommand}
	server := NewServer(service)
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

	service := &recordingService{}
	server := NewServer(service)
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
	if service.approveCommand.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q", service.approveCommand.TenantID)
	}
	if service.approveCommand.RunID != traits.TemplateRunID("run_123") {
		t.Fatalf("run id = %q", service.approveCommand.RunID)
	}
	if service.approveCommand.ApprovedBy != traits.UserID("user_123") {
		t.Fatalf("approved by = %q", service.approveCommand.ApprovedBy)
	}
}

func TestCancelRunCallsService(t *testing.T) {
	t.Parallel()

	service := &recordingService{}
	server := NewServer(service)
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
	if service.cancelCommand.TenantID != traits.TenantID("tenant_123") {
		t.Fatalf("tenant id = %q", service.cancelCommand.TenantID)
	}
	if service.cancelCommand.RunID != traits.TemplateRunID("run_123") {
		t.Fatalf("run id = %q", service.cancelCommand.RunID)
	}
	if service.cancelCommand.RequestedBy != traits.UserID("user_123") {
		t.Fatalf("requested by = %q", service.cancelCommand.RequestedBy)
	}
	if service.cancelCommand.Reason != "testing" {
		t.Fatalf("reason = %q", service.cancelCommand.Reason)
	}
}

func TestRunDecisionConflictErrorsReturnConflict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		body       string
		service    *recordingService
		statusCode int
	}{
		{
			name: "approval",
			path: "/v1/tenants/tenant_123/template-runs/run_123/approval",
			body: `{"approved_by":"user_123"}`,
			service: &recordingService{
				approveErr: app.ErrRunNotApprovable,
			},
			statusCode: http.StatusConflict,
		},
		{
			name: "cancellation",
			path: "/v1/tenants/tenant_123/template-runs/run_123/cancellation",
			body: `{"requested_by":"user_123"}`,
			service: &recordingService{
				cancelErr: app.ErrRunNotCancelable,
			},
			statusCode: http.StatusConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := NewServer(tt.service)
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

	server := NewServer(&recordingService{})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/nope", nil)

	server.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

type recordingService struct {
	startCommand   app.StartTemplateRunCommand
	startRun       traits.TemplateRun
	startErr       error
	approveCommand app.ApproveRunCommand
	approveErr     error
	cancelCommand  app.CancelRunCommand
	cancelErr      error
}

func (service *recordingService) StartTemplateRun(_ context.Context, command app.StartTemplateRunCommand) (traits.TemplateRun, error) {
	service.startCommand = command
	if service.startErr != nil {
		return traits.TemplateRun{}, service.startErr
	}
	return service.startRun, nil
}

func (service *recordingService) ApproveRun(_ context.Context, command app.ApproveRunCommand) error {
	service.approveCommand = command
	if service.approveErr != nil {
		return service.approveErr
	}
	return nil
}

func (service *recordingService) CancelRun(_ context.Context, command app.CancelRunCommand) error {
	service.cancelCommand = command
	if service.cancelErr != nil {
		return service.cancelErr
	}
	return nil
}
