package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

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
	// Lists registered template metadata for the tenant.
	server.mux.HandleFunc("GET /v1/tenants/{tenant_id}/templates", server.handleListTemplates)
	// Reads the current state of a template registration attempt.
	server.mux.HandleFunc("GET /v1/tenants/{tenant_id}/template-registrations/{registration_id}", server.handleGetTemplateRegistration)
	// Lists variables inferred from an immutable registered template.
	server.mux.HandleFunc("GET /v1/tenants/{tenant_id}/templates/{template_id}/variables", server.handleGetTemplateVariables)

	// Stack routes.
	// Creates a logical infrastructure stack.
	server.mux.HandleFunc("POST /v1/tenants/{tenant_id}/stacks", server.handleCreateStack)
	// Lists tenant-owned stacks.
	server.mux.HandleFunc("GET /v1/tenants/{tenant_id}/stacks", server.handleListStacks)
	// Reads one stack with installed templates.
	server.mux.HandleFunc("GET /v1/tenants/{tenant_id}/stacks/{stack_id}", server.handleGetStack)
	// Installs a registered template into a stack.
	server.mux.HandleFunc("POST /v1/tenants/{tenant_id}/stacks/{stack_id}/templates", server.handleAddTemplateToStack)
	// Edits desired config for an installed stack template.
	server.mux.HandleFunc("PATCH /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/config", server.handleUpdateStackTemplateConfig)
	// Stages an installed stack template to a newer template revision.
	server.mux.HandleFunc("POST /v1/tenants/{tenant_id}/stack-templates/{stack_template_id}/upgrade", server.handleUpgradeStackTemplate)

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

func (server *Server) handleListTemplates(response http.ResponseWriter, request *http.Request) {
	templates, err := server.service.ListTemplates(request.Context(), app.ListTemplatesCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusOK, templates)
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

func (server *Server) handleCreateStack(response http.ResponseWriter, request *http.Request) {
	var body createStackRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	credentialIDs := make([]traits.CredentialSetID, 0, len(body.DefaultCredentialIDs))
	for _, id := range body.DefaultCredentialIDs {
		credentialIDs = append(credentialIDs, traits.CredentialSetID(id))
	}

	stack, err := server.service.CreateStack(request.Context(), app.CreateStackCommand{
		TenantID:             traits.TenantID(request.PathValue("tenant_id")),
		Name:                 body.Name,
		Slug:                 body.Slug,
		Tags:                 body.Tags,
		DefaultCredentialIDs: credentialIDs,
		Actor:                traits.UserID(body.Actor),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusCreated, newStackResponse(stack))
}

func (server *Server) handleListStacks(response http.ResponseWriter, request *http.Request) {
	stacks, err := server.service.ListStacks(request.Context(), app.ListStacksCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	body := make([]stackResponse, 0, len(stacks))
	for _, stack := range stacks {
		body = append(body, newStackResponse(stack))
	}
	writeJSON(response, http.StatusOK, body)
}

func (server *Server) handleGetStack(response http.ResponseWriter, request *http.Request) {
	view, err := server.service.GetStack(request.Context(), app.GetStackCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
		StackID:  traits.StackID(request.PathValue("stack_id")),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusOK, newStackViewResponse(view))
}

func (server *Server) handleAddTemplateToStack(response http.ResponseWriter, request *http.Request) {
	var body addTemplateToStackRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	configJSON, ok := stackTemplateConfigJSON(response, body.Config)
	if !ok {
		return
	}

	stackTemplate, err := server.service.AddTemplateToStack(request.Context(), app.AddTemplateToStackCommand{
		TenantID:     traits.TenantID(request.PathValue("tenant_id")),
		StackID:      traits.StackID(request.PathValue("stack_id")),
		TemplateID:   traits.TemplateID(body.TemplateID),
		ComponentKey: body.ComponentKey,
		SelectedRef:  body.SelectedRef,
		ConfigJSON:   configJSON,
		Actor:        traits.UserID(body.Actor),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusCreated, newStackTemplateResponse(stackTemplate))
}

func (server *Server) handleUpdateStackTemplateConfig(response http.ResponseWriter, request *http.Request) {
	var body updateStackTemplateConfigRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	configJSON, ok := stackTemplateConfigJSON(response, body.Config)
	if !ok {
		return
	}

	stackTemplate, err := server.service.UpdateStackTemplateConfig(request.Context(), app.UpdateStackTemplateConfigCommand{
		TenantID:        traits.TenantID(request.PathValue("tenant_id")),
		StackTemplateID: traits.StackTemplateID(request.PathValue("stack_template_id")),
		ConfigJSON:      configJSON,
		Actor:           traits.UserID(body.Actor),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusOK, newStackTemplateResponse(stackTemplate))
}

func (server *Server) handleUpgradeStackTemplate(response http.ResponseWriter, request *http.Request) {
	var body upgradeStackTemplateRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}

	var configJSON json.RawMessage
	if body.Config != nil {
		var ok bool
		configJSON, ok = stackTemplateConfigJSON(response, body.Config)
		if !ok {
			return
		}
	}

	stackTemplate, err := server.service.UpgradeStackTemplate(request.Context(), app.UpgradeStackTemplateCommand{
		TenantID:         traits.TenantID(request.PathValue("tenant_id")),
		StackTemplateID:  traits.StackTemplateID(request.PathValue("stack_template_id")),
		TargetTemplateID: traits.TemplateID(body.TargetTemplateRevisionID),
		ConfigJSON:       configJSON,
		Actor:            traits.UserID(body.Actor),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}

	writeJSON(response, http.StatusOK, newStackTemplateResponse(stackTemplate))
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

func stackTemplateConfigJSON(response http.ResponseWriter, config map[string]any) (json.RawMessage, bool) {
	if config == nil {
		config = map[string]any{}
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "config must be a JSON object")
		return nil, false
	}
	return configJSON, true
}

type registerTemplateRequest struct {
	RepoOwner   string `json:"repo_owner"`
	RepoName    string `json:"repo_name"`
	SourceRef   string `json:"source_ref"`
	RootPath    string `json:"root_path"`
	RequestedBy string `json:"requested_by"`
}

type createStackRequest struct {
	Name                 string            `json:"name"`
	Slug                 string            `json:"slug"`
	Tags                 map[string]string `json:"tags"`
	DefaultCredentialIDs []string          `json:"default_credential_ids"`
	Actor                string            `json:"actor"`
}

type addTemplateToStackRequest struct {
	TemplateID   string         `json:"template_id"`
	ComponentKey string         `json:"component_key"`
	SelectedRef  string         `json:"selected_ref"`
	Config       map[string]any `json:"config"`
	Actor        string         `json:"actor"`
}

type updateStackTemplateConfigRequest struct {
	Config map[string]any `json:"config"`
	Actor  string         `json:"actor"`
}

type upgradeStackTemplateRequest struct {
	TargetTemplateRevisionID string         `json:"target_template_revision_id"`
	Config                   map[string]any `json:"config"`
	Actor                    string         `json:"actor"`
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

type stackViewResponse struct {
	Stack     stackResponse           `json:"stack"`
	Templates []stackTemplateResponse `json:"templates"`
}

type stackResponse struct {
	ID                   string            `json:"id"`
	TenantID             string            `json:"tenant_id"`
	Name                 string            `json:"name"`
	Slug                 string            `json:"slug"`
	Tags                 map[string]string `json:"tags"`
	DefaultCredentialIDs []string          `json:"default_credential_ids"`
	CreatedBy            string            `json:"created_by"`
	CreatedAt            string            `json:"created_at"`
}

type stackTemplateResponse struct {
	ID                    string         `json:"id"`
	StackID               string         `json:"stack_id"`
	TemplateID            string         `json:"template_id"`
	ComponentKey          string         `json:"component_key"`
	SourceTemplateID      string         `json:"source_template_id"`
	DesiredTemplateID     string         `json:"desired_template_id"`
	LastAppliedTemplateID string         `json:"last_applied_template_id"`
	SelectedRef           string         `json:"selected_ref"`
	WorkspaceName         string         `json:"workspace_name"`
	Config                map[string]any `json:"config"`
	LastAppliedRunID      string         `json:"last_applied_run_id"`
	LastAppliedRef        string         `json:"last_applied_ref"`
	LastAppliedAt         string         `json:"last_applied_at,omitempty"`
	CreatedBy             string         `json:"created_by"`
	Lifecycle             string         `json:"lifecycle"`
}

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func newStackViewResponse(view app.StackView) stackViewResponse {
	templates := make([]stackTemplateResponse, 0, len(view.Templates))
	for _, stackTemplate := range view.Templates {
		templates = append(templates, newStackTemplateResponse(stackTemplate))
	}
	return stackViewResponse{
		Stack:     newStackResponse(view.Stack),
		Templates: templates,
	}
}

func newStackResponse(stack traits.Stack) stackResponse {
	credentialIDs := make([]string, 0, len(stack.DefaultCredentialIDs))
	for _, id := range stack.DefaultCredentialIDs {
		credentialIDs = append(credentialIDs, string(id))
	}

	tags := stack.Tags
	if tags == nil {
		tags = map[string]string{}
	}

	return stackResponse{
		ID:                   string(stack.ID),
		TenantID:             string(stack.TenantID),
		Name:                 stack.Name,
		Slug:                 stack.Slug,
		Tags:                 tags,
		DefaultCredentialIDs: credentialIDs,
		CreatedBy:            string(stack.CreatedBy),
		CreatedAt:            stack.CreatedAt.Format(time.RFC3339Nano),
	}
}

func newStackTemplateResponse(stackTemplate traits.StackTemplate) stackTemplateResponse {
	var config map[string]any
	configJSON := stackTemplate.DesiredConfigJSON
	if len(configJSON) == 0 {
		configJSON = stackTemplate.ConfigJSON
	}
	if len(configJSON) > 0 {
		_ = json.Unmarshal(configJSON, &config)
	}
	if config == nil {
		config = map[string]any{}
	}

	response := stackTemplateResponse{
		ID:                    string(stackTemplate.ID),
		StackID:               string(stackTemplate.StackID),
		TemplateID:            string(stackTemplate.TemplateID),
		ComponentKey:          stackTemplate.ComponentKey,
		SourceTemplateID:      string(stackTemplate.SourceTemplateID),
		DesiredTemplateID:     string(stackTemplate.DesiredTemplateID),
		LastAppliedTemplateID: string(stackTemplate.LastAppliedTemplateID),
		SelectedRef:           stackTemplate.SelectedRef,
		WorkspaceName:         stackTemplate.WorkspaceName,
		Config:                config,
		LastAppliedRunID:      string(stackTemplate.LastAppliedRunID),
		LastAppliedRef:        stackTemplate.LastAppliedRef,
		CreatedBy:             string(stackTemplate.CreatedBy),
		Lifecycle:             string(stackTemplate.Lifecycle),
	}
	if !stackTemplate.LastAppliedAt.IsZero() {
		response.LastAppliedAt = stackTemplate.LastAppliedAt.Format(time.RFC3339Nano)
	}
	return response
}

func writeAppError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrInvalidCommand):
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, app.ErrNotFound):
		writeError(response, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, app.ErrStackTemplateNotRunnable),
		errors.Is(err, app.ErrRunNotApprovable),
		errors.Is(err, app.ErrRunNotCancelable),
		errors.Is(err, app.ErrDuplicateStackSlug),
		errors.Is(err, app.ErrTemplateNotInstallable),
		errors.Is(err, app.ErrStackTemplateConfigInvalid),
		errors.Is(err, app.ErrStackTemplateUpgradeInvalid):
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
