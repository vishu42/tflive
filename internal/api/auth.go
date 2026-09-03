package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"

	"github.com/vishu42/tflive/internal/app"
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

	if err := server.establishSession(request.Context(), response, authn.Session{
		Subject:           verified.Subject,
		Name:              verified.Name,
		PreferredUsername: verified.PreferredUsername,
		Email:             verified.Email,
		IDPSessionID:      verified.SessionID,
		IDToken:           rawIDToken,
	}, verified.DisplayName()); err != nil {
		log.Printf("auth callback: %v", err)
		server.writeAuthFailure(response)
		return
	}

	http.Redirect(response, request, authn.SafeReturnTo(transaction.ReturnTo), http.StatusFound)
}

// establishSession is the tail every sign-in ends in, whichever method proved
// the identity: project the user, mint the session row, hand the browser its
// cookie. Both callers reach it, so a change to what a session carries cannot
// land on one path and miss the other.
//
// identity supplies only the claim-bearing fields of the session — the caller
// fills Subject, Name, PreferredUsername, Email, and, for an OIDC sign-in, the
// IdP's session id and ID token. The lifetimes and the id hash are set here,
// because they are tflive's to decide rather than the caller's.
//
// The projection happens before the session exists, so that a session row
// implies a projected user rather than merely coinciding with one. That
// ordering is what makes "signed in at least once" a fact the grants UI can
// rely on instead of a hope.
//
// A failed projection fails the sign-in. It is tempting to log and continue, as
// a failed session touch does in the middleware, but the cases are not alike: a
// failed touch shortens a session that still works, whereas a failed projection
// would sign someone in who is then invisible to search and cannot be granted a
// role, with nothing anywhere saying why. It also costs nothing to be strict —
// this is a single-row upsert into the same database the session row is about
// to go into, so if it fails, the session write was going to fail too.
func (server *Server) establishSession(
	ctx context.Context,
	response http.ResponseWriter,
	identity authn.Session,
	displayName string,
) error {
	if err := server.service.RecordSignIn(ctx, app.UserProfile{
		Sub:         identity.Subject,
		DisplayName: displayName,
		Email:       identity.Email,
	}); err != nil {
		return fmt.Errorf("failed to project signed-in user: %w", err)
	}

	sessionID, err := authn.NewSessionID()
	if err != nil {
		return fmt.Errorf("failed to generate session id: %w", err)
	}
	now := server.now()
	session := identity
	session.IDHash = authn.HashSessionID(sessionID)
	session.CreatedAt = now
	session.LastSeenAt = now
	// The IdP's token lifetime deliberately does not appear here. How long a
	// tflive session lasts is tflive's to decide; the token's exp bounded only
	// the authentication that just completed.
	session.AbsoluteExpiresAt = now.Add(server.auth.SessionAbsoluteTTL)

	if err := server.auth.Sessions.CreateSession(ctx, session); err != nil {
		return fmt.Errorf("failed to persist session: %w", err)
	}

	http.SetCookie(response, authn.SessionCookie(sessionID, server.auth.SecureCookies))
	return nil
}

// handleAuthLogout revokes our session and sends the browser on to end the
// IdP's. Without that second half, logging out and back in silently returns
// the same user, because the provider's SSO session still stands.
//
// Revoking the row rather than only clearing the cookie is what makes logout
// real: a copy of the cookie taken beforehand stops working too.
//
// The URL it redirects to carries the raw ID token as id_token_hint, which is
// how RP-initiated logout is specified and what stops Keycloak interrupting
// with a confirmation page. That token is an identity assertion and nothing
// more — the middleware authenticates against the session store alone, so a
// copy read out of browser history or an access log opens nothing.
//
// It still redirects rather than returning the URL in a body, because a body
// would be readable by any script on the origin and there is no reason to hand
// the token to script. A Location header on a 303 is not script-readable, and
// http.Redirect writes no body for a POST.
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
	// Both halves are checked. A session carrying an ID token does not imply a
	// Flow: the row outlives the configuration that created it, so an install
	// that had OIDC removed still serves logout requests from sessions minted
	// while it was on, and dereferencing the absent Flow there would panic on
	// the one route that is supposed to work for every method.
	if idTokenHint != "" && server.auth.Flow != nil {
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
