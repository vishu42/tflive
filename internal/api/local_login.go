package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/vishu42/tflive/internal/authn"
)

// maxLoginBodyBytes bounds the login request body. A username and a password
// do not approach it; anything that does is not a sign-in attempt, and
// decoding it would be work done on behalf of an unauthenticated caller.
const maxLoginBodyBytes = 4 << 10

type localLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// authMethodsResponse tells the sign-in screen which ways in exist, so it
// renders the ones that do rather than discovering them by failing (#213).
//
// In a real deployment Local is always true — root is a local account that
// cannot be locked out (#212) — so today only OIDC varies, and the screen is a
// password form plus an SSO button that appears when a provider is configured.
// Local is still reported rather than assumed, because a client that reads
// what the server says needs no change if that ever stops being true, and
// because the api package does allow a server with no local authenticator.
type authMethodsResponse struct {
	Local bool `json:"local"`
	OIDC  bool `json:"oidc"`
}

// handleAuthMethods reports the enabled sign-in methods.
//
// Public and pre-session by necessity: it is read to decide what the sign-in
// screen looks like, before anyone has signed in. It discloses nothing the
// routes do not already — an absent method is an absent route — and naming it
// here is what stops the client inferring availability from a failed login,
// which cannot tell "local auth is off" from "wrong password".
func (server *Server) handleAuthMethods(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, authMethodsResponse{
		Local: server.auth.LocalAuthenticator != nil,
		OIDC:  server.auth.Flow != nil,
	})
}

// handleLocalLogin signs in against tflive's own account table.
//
// It issues no token. On success it ends in exactly the tail the OIDC callback
// ends in — project, mint a session row, set the cookie — so every request
// after it is authenticated by the same opaque session cookie, and nothing
// downstream can tell which method was used.
//
// It answers 204 rather than returning the identity: the SPA refetches /v1/me,
// which is the one place identity is rendered from, so returning it here would
// be a second copy to keep in step with the first.
func (server *Server) handleLocalLogin(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")

	var credentials localLoginRequest
	body := http.MaxBytesReader(response, request.Body, maxLoginBodyBytes)
	if err := json.NewDecoder(body).Decode(&credentials); err != nil {
		// Deliberately not distinguishing a syntax error from an oversized
		// body: both mean the caller did not send a sign-in request, and
		// neither is worth a distinct code the SPA would have to handle.
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) || errors.Is(err, io.EOF) || err != nil {
			writeError(response, http.StatusBadRequest, "invalid_request", "malformed request body")
			return
		}
	}

	// The credentials go through untouched. Folding and trimming belong to the
	// authenticator, which defines what makes two spellings the same account;
	// doing any of it here would give the lookup two definitions to disagree
	// about.
	identity, err := server.auth.LocalAuthenticator.Authenticate(request.Context(), credentials.Username, credentials.Password)
	if err != nil {
		if errors.Is(err, authn.ErrInvalidCredentials) {
			// One answer for a wrong password and an unknown username. The
			// authenticator has already equalised the cost of the two; saying
			// which one it was here would undo that.
			writeError(response, http.StatusUnauthorized, "unauthorized", "authentication failed")
			return
		}
		// Not a credential decision — a store outage or similar. Reporting it
		// as 401 would tell the user to retype a correct password and would
		// bury the outage in the 401 rate.
		log.Printf("local login: authentication failed: %v", err)
		writeError(response, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	// No IDToken and no IDPSessionID: there is no provider behind this session.
	// Logout reads IDToken to build an id_token_hint and skips the provider
	// redirect when it is empty, which is exactly right here.
	if err := server.establishSession(request.Context(), response, authn.Session{
		Subject:           identity.Subject,
		Name:              identity.DisplayName,
		PreferredUsername: identity.PreferredUsername,
		Email:             identity.Email,
	}, identity.DisplayName); err != nil {
		log.Printf("local login: %v", err)
		writeError(response, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	response.WriteHeader(http.StatusNoContent)
}
