package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log"
	"net/http"

	"github.com/vishu42/tflive/internal/authn"
)

// authFailureBody is the single response every authentication failure renders.
// Distinguishing "bad state" from "expired code" from "nonce mismatch" would
// tell an attacker which check they tripped.
const authFailureBody = `<!doctype html><meta charset="utf-8"><title>Sign-in failed</title>` +
	`<p>Sign-in could not be completed. <a href="/v1/auth/login">Try again</a>.</p>`

// handleAuthLogin starts the flow. It makes no network call: the authorization
// endpoint comes from discovery the verifier already cached.
func (server *Server) handleAuthLogin(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")

	state, stateErr := randomToken()
	nonce, nonceErr := randomToken()
	if stateErr != nil || nonceErr != nil {
		log.Printf("auth login: failed to generate state/nonce token: state error = %v, nonce error = %v", stateErr, nonceErr)
		server.writeAuthFailure(response)
		return
	}
	transaction := authn.Transaction{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: authn.GenerateVerifier(),
		ReturnTo:     authn.SafeReturnTo(request.URL.Query().Get("return_to")),
	}

	sealed, err := authn.SealTransaction(server.auth.Sealer, transaction)
	if err != nil {
		log.Printf("auth login: failed to seal transaction cookie: %v", err)
		server.writeAuthFailure(response)
		return
	}
	authorizationURL, err := server.auth.Flow.AuthorizationURL(transaction.State, transaction.Nonce, transaction.CodeVerifier)
	if err != nil {
		log.Printf("auth login: failed to build authorization URL: %v", err)
		server.writeAuthFailure(response)
		return
	}

	http.SetCookie(response, authn.TransactionCookie(sealed, server.auth.SecureCookies))
	http.Redirect(response, request, authorizationURL, http.StatusFound)
}

// handleAuthCallback finishes the flow: redeem the code on the back channel,
// verify the ID token, and hand the browser a session cookie. It redirects
// rather than rendering, so the authorization code leaves the address bar and
// the browser history.
func (server *Server) handleAuthCallback(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	http.SetCookie(response, authn.ClearedTransactionCookie(server.auth.SecureCookies))

	query := request.URL.Query()
	if query.Get("error") != "" {
		log.Printf("auth callback: idp returned error=%q error_description=%q", query.Get("error"), query.Get("error_description"))
		server.writeAuthFailure(response)
		return
	}
	cookie, err := request.Cookie(authn.TransactionCookieName)
	if err != nil || cookie.Value == "" {
		log.Printf("auth callback: missing or unreadable transaction cookie: %v", err)
		server.writeAuthFailure(response)
		return
	}
	transaction, err := authn.OpenTransaction(server.auth.Sealer, cookie.Value)
	if err != nil {
		log.Printf("auth callback: failed to open transaction cookie: %v", err)
		server.writeAuthFailure(response)
		return
	}
	if subtle.ConstantTimeCompare([]byte(transaction.State), []byte(query.Get("state"))) != 1 {
		log.Printf("auth callback: state mismatch")
		server.writeAuthFailure(response)
		return
	}
	code := query.Get("code")
	if code == "" {
		log.Printf("auth callback: no authorization code in callback")
		server.writeAuthFailure(response)
		return
	}

	rawIDToken, err := server.auth.Flow.Exchange(request.Context(), code, transaction.CodeVerifier)
	if err != nil {
		log.Printf("auth callback: token exchange failed: %v", err)
		server.writeAuthFailure(response)
		return
	}
	verified, err := server.auth.Verifier.Verify(request.Context(), rawIDToken)
	if err != nil {
		log.Printf("auth callback: id token verification failed: %v", err)
		server.writeAuthFailure(response)
		return
	}
	if subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(transaction.Nonce)) != 1 {
		log.Printf("auth callback: nonce mismatch")
		server.writeAuthFailure(response)
		return
	}

	sessionID, err := authn.NewSessionID()
	if err != nil {
		log.Printf("auth callback: failed to generate session id: %v", err)
		server.writeAuthFailure(response)
		return
	}
	now := server.now()
	session := authn.Session{
		IDHash:            authn.HashSessionID(sessionID),
		Subject:           verified.Subject,
		Name:              verified.Name,
		PreferredUsername: verified.PreferredUsername,
		Email:             verified.Email,
		IDPSessionID:      verified.SessionID,
		IDToken:           rawIDToken,
		CreatedAt:         now,
		LastSeenAt:        now,
		// The IdP's token lifetime deliberately does not appear here. How long
		// a tflive session lasts is tflive's to decide; the token's exp bounded
		// only the authentication we just completed.
		AbsoluteExpiresAt: now.Add(server.auth.SessionAbsoluteTTL),
	}
	if err := server.auth.Sessions.CreateSession(request.Context(), session); err != nil {
		log.Printf("auth callback: failed to persist session: %v", err)
		server.writeAuthFailure(response)
		return
	}

	http.SetCookie(response, authn.SessionCookie(sessionID, server.auth.SecureCookies))
	http.Redirect(response, request, authn.SafeReturnTo(transaction.ReturnTo), http.StatusFound)
}

// handleAuthLogout revokes our session and sends the browser on to end the
// IdP's. Without that second half, logging out and back in silently returns
// the same user, because the provider's SSO session still stands.
//
// Revoking the row rather than only clearing the cookie is what makes logout
// real: a copy of the cookie taken beforehand stops working too.
//
// It redirects rather than returning the URL in a body: that URL carries the
// raw ID token as id_token_hint, a body would be readable by any script on the
// origin, and the middleware accepts that token as a bearer credential. A
// Location header on a 303 is not script-readable, and http.Redirect writes no
// body for a POST.
func (server *Server) handleAuthLogout(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")

	var idTokenHint string
	if cookie, err := request.Cookie(authn.SessionCookieName); err == nil && cookie.Value != "" {
		idHash := authn.HashSessionID(cookie.Value)
		if session, err := server.auth.Sessions.SessionByHash(request.Context(), idHash); err == nil {
			idTokenHint = session.IDToken
		}
		if err := server.auth.Sessions.RevokeSession(request.Context(), idHash, server.now()); err != nil {
			// The cookie is cleared regardless, so the browser is signed out
			// either way; the row outliving it is what this logs.
			log.Printf("auth logout: failed to revoke session: %v", err)
		}
	}
	http.SetCookie(response, authn.ClearedSessionCookie(server.auth.SecureCookies))

	destination := server.auth.PublicURL + "/"
	if idTokenHint != "" {
		if logoutURL := server.auth.Flow.EndSessionURL(idTokenHint, server.auth.PublicURL+"/"); logoutURL != "" {
			destination = logoutURL
		}
	}
	http.Redirect(response, request, destination, http.StatusSeeOther)
}

func (server *Server) writeAuthFailure(response http.ResponseWriter) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusUnauthorized)
	_, _ = response.Write([]byte(authFailureBody))
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
