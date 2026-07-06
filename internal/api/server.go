package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/vishu42/megagega/internal/app"
	"github.com/vishu42/megagega/internal/traits"
)

type Server struct {
	service *app.Service
	mux     *http.ServeMux
}

func NewServer(service *app.Service) *Server {
	server := &Server{
		service: service,
		mux:     http.NewServeMux(),
	}

	// Health routes.
	// Reports process liveness for probes and local smoke checks.
	server.mux.HandleFunc("GET /healthz", server.handleHealth)

	// Template registration routes.
	// Starts async registration for a public GitHub Terraform template source.
	server.mux.HandleFunc("POST /v1/tenants/{tenant_id}/templates", server.handleRegisterTemplate)
	// Reads the current state of a template registration attempt.
	server.mux.HandleFunc("GET /v1/tenants/{tenant_id}/template-registrations/{registration_id}", server.handleGetTemplateRegistration)
	// Lists variables inferred from an immutable registered template.
	server.mux.HandleFunc("GET /v1/tenants/{tenant_id}/templates/{template_id}/variables", server.handleGetTemplateVariables)

	// Template run routes.
	// Starts a Terraform operation for an installed stack template.
	server.mux.HandleFunc("POST /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/runs", server.handleStartTemplateRun)
	// Reads the current state of a template run.
	server.mux.HandleFunc("GET /v1/tenants/{tenant_id}/template-runs/{run_id}", server.handleGetTemplateRun)
	// Lists persisted log metadata for all phases of a template run.
	server.mux.HandleFunc("GET /v1/tenants/{tenant_id}/template-runs/{run_id}/logs", server.handleListTemplateRunLogs)
	// Reads the persisted log body for one template run phase.
	server.mux.HandleFunc("GET /v1/tenants/{tenant_id}/template-runs/{run_id}/logs/{phase}", server.handleGetTemplateRunLog)

	// Template run decision routes.
	// Records approval for a waiting template run.
	server.mux.HandleFunc("POST /v1/tenants/{tenant_id}/template-runs/{run_id}/approval", server.handleApproveRun)
	// Requests cancellation for a running template run.
	server.mux.HandleFunc("POST /v1/tenants/{tenant_id}/template-runs/{run_id}/cancellation", server.handleCancelRun)
	return server
}

func (server *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	server.mux.ServeHTTP(response, request)
}

func (server *Server) handleHealth(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (server *Server) handleRegisterTemplate(response http.ResponseWriter, request *http.Request) {
	var body registerTemplateRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	registration, err := server.service.RegisterTemplate(request.Context(), app.RegisterTemplateCommand{
		TenantID:    traits.TenantID(request.PathValue("tenant_id")),
		RepoOwner:   body.RepoOwner,
		RepoName:    body.RepoName,
		SourceRef:   body.SourceRef,
		RootPath:    body.RootPath,
		RequestedBy: traits.UserID(body.RequestedBy),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusAccepted, registration)
}

func (server *Server) handleGetTemplateRegistration(response http.ResponseWriter, request *http.Request) {
	registration, err := server.service.GetTemplateRegistration(request.Context(), app.GetTemplateRegistrationCommand{
		TenantID:       traits.TenantID(request.PathValue("tenant_id")),
		RegistrationID: traits.TemplateRegistrationID(request.PathValue("registration_id")),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusOK, registration)
}

func (server *Server) handleGetTemplateVariables(response http.ResponseWriter, request *http.Request) {
	variables, err := server.service.GetTemplateVariables(request.Context(), app.GetTemplateVariablesCommand{
		TenantID:   traits.TenantID(request.PathValue("tenant_id")),
		TemplateID: traits.TemplateID(request.PathValue("template_id")),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusOK, variables)
}

func (server *Server) handleStartTemplateRun(response http.ResponseWriter, request *http.Request) {
	var body startTemplateRunRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	run, err := server.service.StartTemplateRun(request.Context(), app.StartTemplateRunCommand{
		TenantID:        traits.TenantID(request.PathValue("tenant_id")),
		StackTemplateID: traits.StackTemplateID(request.PathValue("stack_template_id")),
		Operation:       traits.OperationType(body.Operation),
		TriggerActor:    traits.UserID(body.TriggerActor),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusCreated, run)
}

func (server *Server) handleGetTemplateRun(response http.ResponseWriter, request *http.Request) {
	run, err := server.service.GetTemplateRun(request.Context(), app.GetTemplateRunCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
		RunID:    traits.TemplateRunID(request.PathValue("run_id")),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusOK, run)
}

func (server *Server) handleGetTemplateRunLog(response http.ResponseWriter, request *http.Request) {
	content, err := server.service.GetTemplateRunLog(request.Context(), app.GetTemplateRunLogCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
		RunID:    traits.TemplateRunID(request.PathValue("run_id")),
		Phase:    request.PathValue("phase"),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(content)
}

func (server *Server) handleListTemplateRunLogs(response http.ResponseWriter, request *http.Request) {
	logs, err := server.service.ListTemplateRunLogs(request.Context(), app.ListTemplateRunLogsCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
		RunID:    traits.TemplateRunID(request.PathValue("run_id")),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusOK, logs)
}

func (server *Server) handleApproveRun(response http.ResponseWriter, request *http.Request) {
	var body approveRunRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	err := server.service.ApproveRun(request.Context(), app.ApproveRunCommand{
		TenantID:   traits.TenantID(request.PathValue("tenant_id")),
		RunID:      traits.TemplateRunID(request.PathValue("run_id")),
		ApprovedBy: traits.UserID(body.ApprovedBy),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) handleCancelRun(response http.ResponseWriter, request *http.Request) {
	var body cancelRunRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	err := server.service.CancelRun(request.Context(), app.CancelRunCommand{
		TenantID:    traits.TenantID(request.PathValue("tenant_id")),
		RunID:       traits.TemplateRunID(request.PathValue("run_id")),
		RequestedBy: traits.UserID(body.RequestedBy),
		Reason:      body.Reason,
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	response.WriteHeader(http.StatusNoContent)
}

type registerTemplateRequest struct {
	RepoOwner   string `json:"repo_owner"`
	RepoName    string `json:"repo_name"`
	SourceRef   string `json:"source_ref"`
	RootPath    string `json:"root_path"`
	RequestedBy string `json:"requested_by"`
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

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeAppError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrInvalidCommand):
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, app.ErrNotFound):
		writeError(response, http.StatusNotFound, "not_found", err.Error())
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
