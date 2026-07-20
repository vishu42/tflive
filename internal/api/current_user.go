package api

import (
	"net/http"
	"strings"

	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/traits"
)

type meResponse struct {
	Subject            string                 `json:"subject"`
	Name               string                 `json:"name"`
	PreferredUsername  string                 `json:"preferred_username"`
	Email              string                 `json:"email"`
	GlobalCapabilities app.GlobalCapabilities `json:"global_capabilities"`
}

func (server *Server) handleGetMe(response http.ResponseWriter, request *http.Request) {
	me, err := server.service.GetMe(request.Context())
	if err != nil {
		writeAppError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, meResponse{
		Subject:            me.Subject,
		Name:               me.Name,
		PreferredUsername:  me.PreferredUsername,
		Email:              me.Email,
		GlobalCapabilities: me.GlobalCapabilities,
	})
}

type listGrantsResponse struct {
	Grants []app.GrantEntry `json:"grants"`
}

func (server *Server) handleListStackGrants(response http.ResponseWriter, request *http.Request) {
	entries, err := server.service.ListStackGrants(request.Context(), app.ListStackGrantsCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
		StackID:  traits.StackID(request.PathValue("stack_id")),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, listGrantsResponse{Grants: entries})
}

type putGrantRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

func (server *Server) handlePutStackGrant(response http.ResponseWriter, request *http.Request) {
	var body putGrantRequest
	if !decodeRequestBody(response, request, &body) {
		return
	}

	entries, err := server.service.PutStackGrant(request.Context(), app.PutStackGrantCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
		StackID:  traits.StackID(request.PathValue("stack_id")),
		UserID:   strings.TrimSpace(body.UserID),
		Role:     strings.TrimSpace(body.Role),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, listGrantsResponse{Grants: entries})
}

func (server *Server) handleRevokeStackGrant(response http.ResponseWriter, request *http.Request) {
	err := server.service.RevokeStackGrant(request.Context(), app.RevokeStackGrantCommand{
		TenantID: traits.TenantID(request.PathValue("tenant_id")),
		StackID:  traits.StackID(request.PathValue("stack_id")),
		UserID:   request.PathValue("user_id"),
	})
	if err != nil {
		writeAppError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
