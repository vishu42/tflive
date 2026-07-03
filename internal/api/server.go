package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vishu42/megagega/internal/app"
	"github.com/vishu42/megagega/internal/traits"
)

type service interface {
	StartTemplateRun(context.Context, app.StartTemplateRunCommand) (traits.TemplateRun, error)
	ApproveRun(context.Context, app.ApproveRunCommand) error
	CancelRun(context.Context, app.CancelRunCommand) error
}

type Server struct {
	service service
}

func NewServer(service service) *Server {
	return &Server{service: service}
}

func (server *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/healthz" {
		server.handleHealth(response, request)
		return
	}

	route := splitPath(request.URL.Path)
	switch {
	case matchStartTemplateRunRoute(route):
		server.handleStartTemplateRun(response, request, route[2], route[4])
	case matchRunDecisionRoute(route, "approval"):
		server.handleApproveRun(response, request, route[2], route[4])
	case matchRunDecisionRoute(route, "cancellation"):
		server.handleCancelRun(response, request, route[2], route[4])
	default:
		writeError(response, http.StatusNotFound, "not_found", "route not found")
	}
}

func (server *Server) handleHealth(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (server *Server) handleStartTemplateRun(response http.ResponseWriter, request *http.Request, tenantID string, stackTemplateID string) {
	if request.Method != http.MethodPost {
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	var body startTemplateRunRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	run, err := server.service.StartTemplateRun(request.Context(), app.StartTemplateRunCommand{
		TenantID:        traits.TenantID(tenantID),
		StackTemplateID: traits.StackTemplateID(stackTemplateID),
		Operation:       traits.OperationType(body.Operation),
		TriggerActor:    traits.UserID(body.TriggerActor),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusCreated, templateRunResponseFrom(run))
}

func (server *Server) handleApproveRun(response http.ResponseWriter, request *http.Request, tenantID string, runID string) {
	if request.Method != http.MethodPost {
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	var body approveRunRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	err := server.service.ApproveRun(request.Context(), app.ApproveRunCommand{
		TenantID:   traits.TenantID(tenantID),
		RunID:      traits.TemplateRunID(runID),
		ApprovedBy: traits.UserID(body.ApprovedBy),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) handleCancelRun(response http.ResponseWriter, request *http.Request, tenantID string, runID string) {
	if request.Method != http.MethodPost {
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	var body cancelRunRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	err := server.service.CancelRun(request.Context(), app.CancelRunCommand{
		TenantID:    traits.TenantID(tenantID),
		RunID:       traits.TemplateRunID(runID),
		RequestedBy: traits.UserID(body.RequestedBy),
		Reason:      body.Reason,
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	response.WriteHeader(http.StatusNoContent)
}

type startTemplateRunRequest struct {
	Operation    string `json:"operation"`
	TriggerActor string `json:"trigger_actor"`
}

type approveRunRequest struct {
	ApprovedBy string `json:"approved_by"`
}

type cancelRunRequest struct {
	RequestedBy string `json:"requested_by"`
	Reason      string `json:"reason"`
}

type templateRunResponse struct {
	ID                string `json:"id"`
	TenantID          string `json:"tenant_id"`
	StackTemplateID   string `json:"stack_template_id"`
	Operation         string `json:"operation"`
	SelectedRef       string `json:"selected_ref"`
	ResolvedCommitSHA string `json:"resolved_commit_sha"`
	WorkspaceName     string `json:"workspace_name"`
	BackendType       string `json:"backend_type"`
	BackendConfigHash string `json:"backend_config_hash"`
	Status            string `json:"status"`
	TriggerActor      string `json:"trigger_actor"`
	StartedAt         string `json:"started_at"`
	CompletedAt       string `json:"completed_at,omitempty"`
	ErrorSummary      string `json:"error_summary"`
}

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func templateRunResponseFrom(run traits.TemplateRun) templateRunResponse {
	response := templateRunResponse{
		ID:                string(run.ID),
		TenantID:          string(run.TenantID),
		StackTemplateID:   string(run.StackTemplateID),
		Operation:         string(run.Operation),
		SelectedRef:       run.SelectedRef,
		ResolvedCommitSHA: run.ResolvedCommitSHA,
		WorkspaceName:     run.WorkspaceName,
		BackendType:       run.BackendType,
		BackendConfigHash: run.BackendConfigHash,
		Status:            string(run.Status),
		TriggerActor:      string(run.TriggerActor),
		ErrorSummary:      run.ErrorSummary,
	}
	if !run.StartedAt.IsZero() {
		response.StartedAt = run.StartedAt.Format(time.RFC3339Nano)
	}
	if !run.CompletedAt.IsZero() {
		response.CompletedAt = run.CompletedAt.Format(time.RFC3339Nano)
	}
	return response
}

func writeAppError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrInvalidCommand):
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, app.ErrStackTemplateNotRunnable),
		errors.Is(err, app.ErrRunNotApprovable),
		errors.Is(err, app.ErrRunNotCancelable):
		writeError(response, http.StatusConflict, "conflict", err.Error())
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}

func writeError(response http.ResponseWriter, status int, code string, message string) {
	writeJSON(response, status, errorResponse{
		Error:   code,
		Message: message,
	})
}

func splitPath(value string) []string {
	trimmed := strings.Trim(value, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func matchStartTemplateRunRoute(route []string) bool {
	return len(route) == 6 &&
		route[0] == "v1" &&
		route[1] == "tenants" &&
		route[3] == "stack-templates" &&
		route[5] == "runs" &&
		route[2] != "" &&
		route[4] != ""
}

func matchRunDecisionRoute(route []string, decision string) bool {
	return len(route) == 6 &&
		route[0] == "v1" &&
		route[1] == "tenants" &&
		route[3] == "template-runs" &&
		route[5] == decision &&
		route[2] != "" &&
		route[4] != ""
}
