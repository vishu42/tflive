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
		server.writeAuthFailure(response)
		return
	}
	authorizationURL, err := server.auth.Flow.AuthorizationURL(transaction.State, transaction.Nonce, transaction.CodeVerifier)
	if err != nil {
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
		server.writeAuthFailure(response)
		return
	}
	cookie, err := request.Cookie(authn.TransactionCookieName)
	if err != nil || cookie.Value == "" {
		server.writeAuthFailure(response)
		return
	}
	transaction, err := authn.OpenTransaction(server.auth.Sealer, cookie.Value)
	if err != nil {
		server.writeAuthFailure(response)
		return
	}
	if subtle.ConstantTimeCompare([]byte(transaction.State), []byte(query.Get("state"))) != 1 {
		server.writeAuthFailure(response)
		return
	}
	code := query.Get("code")
	if code == "" {
		server.writeAuthFailure(response)
		return
	}

	rawIDToken, err := server.auth.Flow.Exchange(request.Context(), code, transaction.CodeVerifier)
	if err != nil {
		server.writeAuthFailure(response)
		return
	}
	verified, err := server.auth.Verifier.Verify(request.Context(), rawIDToken)
	if err != nil {
		server.writeAuthFailure(response)
		return
	}
	if subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(transaction.Nonce)) != 1 {
		server.writeAuthFailure(response)
		return
	}

	server.warnOnOversizedSession(rawIDToken)
	http.SetCookie(response, authn.SessionCookie(rawIDToken, server.auth.SecureCookies))
	http.Redirect(response, request, authn.SafeReturnTo(transaction.ReturnTo), http.StatusFound)
}

// handleAuthLogout clears our session and sends the browser on to end the
// IdP's. Without that second half, logging out and back in silently returns
// the same user, because the provider's SSO session still stands.
//
// It redirects rather than returning the URL in a body: that URL carries the
// raw ID token as id_token_hint, a body would be readable by any script on the
// origin, and the middleware accepts that token as a bearer credential. A
// Location header on a 303 is not script-readable, and http.Redirect writes no
// body for a POST.
func (server *Server) handleAuthLogout(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")

	var idTokenHint string
	if cookie, err := request.Cookie(authn.SessionCookieName); err == nil {
		idTokenHint = cookie.Value
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

// warnOnOversizedSession logs when an ID token approaches the 4096-byte cookie
// limit. Past it, browsers drop the cookie silently and the user simply never
// appears logged in. A provider that stuffs group or role claims into the ID
// token is the realistic way to get there.
func (server *Server) warnOnOversizedSession(rawIDToken string) {
	const warnThresholdBytes = 3072
	if len(rawIDToken) > warnThresholdBytes {
		log.Printf("id token is %d bytes, approaching the 4096-byte cookie limit; sessions will break silently past it", len(rawIDToken))
	}
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
