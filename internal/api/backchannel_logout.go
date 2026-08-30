package api

import (
	"context"
	"log"
	"net/http"

	"github.com/vishu42/tflive/internal/authn"
)

// LogoutTokenVerifier authenticates a back-channel logout notification.
// *authn.OIDCVerifier satisfies it; the interface exists so handler tests need
// no live IdP.
type LogoutTokenVerifier interface {
	VerifyLogoutToken(ctx context.Context, raw string) (authn.LogoutToken, error)
}

// handleBackchannelLogout ends sessions on the IdP's instruction.
//
// It is unauthenticated by necessity: the notification arrives from the
// provider's server, which holds no tflive cookie and no bearer token. The
// logout token is the credential, and it is verified against the same JWKS
// that verifies ID tokens.
//
// Without this endpoint, disabling a user at the IdP would not reach tflive
// until their session hit its own expiry, because tflive stops consulting the
// provider once a session exists.
func (server *Server) handleBackchannelLogout(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")

	if server.auth.LogoutTokenVerifier == nil {
		log.Printf("backchannel logout: no logout token verifier configured; refusing")
		http.Error(response, "not available", http.StatusServiceUnavailable)
		return
	}

	// ParseForm buffers the whole body before the 16 KB logout-token check
	// below ever runs, and Go's own limit for an urlencoded body is 10 MB.
	// This route is unauthenticated by necessity, so that 10 MB is free to
	// anyone on the internet; cap it well under the smallest real logout
	// token has any business needing.
	request.Body = http.MaxBytesReader(response, request.Body, 64*1024)
	if err := request.ParseForm(); err != nil {
		http.Error(response, "invalid request", http.StatusBadRequest)
		return
	}
	raw := request.PostFormValue("logout_token")
	if raw == "" {
		http.Error(response, "invalid request", http.StatusBadRequest)
		return
	}

	token, err := server.auth.LogoutTokenVerifier.VerifyLogoutToken(request.Context(), raw)
	if err != nil {
		log.Printf("backchannel logout: token rejected: %v", err)
		http.Error(response, "invalid request", http.StatusBadRequest)
		return
	}

	now := server.now()
	// sid identifies one browser session; sub identifies the person. Prefer
	// the narrower one, so a provider that signs one device out does not sign
	// the user out everywhere.
	var revoked int
	if token.SessionID != "" {
		revoked, err = server.auth.Sessions.RevokeSessionsByIDPSessionID(request.Context(), token.SessionID, now)
	} else {
		revoked, err = server.auth.Sessions.RevokeSessionsBySubject(request.Context(), token.Subject, now)
	}
	if err != nil {
		log.Printf("backchannel logout: revoke failed: %v", err)
		http.Error(response, "revocation failed", http.StatusInternalServerError)
		return
	}

	// 200 whether or not anything matched. Whether tflive holds a session for
	// a given sid is not something an unauthenticated caller gets to learn.
	log.Printf("backchannel logout: revoked %d session(s)", revoked)
	response.WriteHeader(http.StatusOK)
}
