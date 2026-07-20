package api

import (
	"net/http"

	"github.com/vishu42/tflive/internal/app"
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
