package api

import (
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"strings"

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

// isSameSiteLogin reports whether the request could have come from tflive's
// own origin, which is what stands between this route and cross-site request
// forgery.
//
// The OIDC flow gets this from its sealed state cookie: a login it did not
// start cannot be completed. This route has no such transaction — it is a
// single unauthenticated POST — and without a check any page on the internet
// could submit a form that signs the visitor into an account the attacker
// controls, silently, because the session cookie is SameSite=Lax and Lax
// permits the cookie the response sets.
//
// Two signals, in order of trustworthiness. Sec-Fetch-Site is set by the
// browser and cannot be forged by script; "none" is a typed address or a
// bookmark, "same-origin" is our own page. Origin is the fallback for browsers
// that do not send fetch metadata, and is present on every cross-origin form
// post and on every POST issued by fetch. Neither header present means the
// caller is not a browser — curl, a script, a health check — which cannot be
// coerced into carrying somebody else's ambient cookies and so is not what CSRF
// protects against.
func (server *Server) isSameSiteLogin(request *http.Request) bool {
	switch request.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		// No fetch metadata. Fall through to Origin.
	default:
		// same-site or cross-site: another origin drove this.
		return false
	}

	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	// An opaque origin arrives as the literal "null"; it matches no configured
	// public URL, so it is refused by the same comparison.
	return strings.EqualFold(origin, server.auth.PublicURL)
}

// hasJSONContentType reports whether the body is declared as JSON.
//
// A correctness check that is also the second half of the CSRF defence. An
// HTML form can only post text/plain, application/x-www-form-urlencoded, or
// multipart/form-data, and json.Decoder happily reads a JSON object out of a
// text/plain form body; requiring the declared type is what stops that. A
// cross-origin fetch that does declare JSON is preflighted, and the preflight
// is what the browser will not let through.
func hasJSONContentType(request *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
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

	if !server.isSameSiteLogin(request) {
		writeError(response, http.StatusForbidden, "forbidden", "cross-site sign-in requests are not accepted")
		return
	}
	if !hasJSONContentType(request) {
		writeError(response, http.StatusUnsupportedMediaType, "unsupported_media_type", "content-type must be application/json")
		return
	}

	var credentials localLoginRequest
	body := http.MaxBytesReader(response, request.Body, maxLoginBodyBytes)
	if err := json.NewDecoder(body).Decode(&credentials); err != nil {
		// Deliberately not distinguishing a syntax error from an oversized
		// body or an empty one: all of them mean the caller did not send a
		// sign-in request, and none is worth a distinct code the SPA would
		// have to handle.
		writeError(response, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
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
