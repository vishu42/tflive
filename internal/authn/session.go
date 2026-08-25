package authn

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/vishu42/tflive/internal/secrets"
)

const (
	// SessionCookieName holds the IdP's raw ID token. It is deliberately not
	// encrypted: it is a signed JWT whose contents are the user's own claims,
	// and tampering is caught by verification.
	SessionCookieName = "tflive_session"
	// TransactionCookieName holds the in-flight login, sealed. state is only
	// meaningful if the browser cannot forge it.
	TransactionCookieName = "tflive_auth_tx"
	// transactionMaxAge bounds how long a login may sit half-finished.
	transactionMaxAge = 600
	// transactionCookiePath scopes the transaction cookie to the routes that
	// read it, so it is not attached to every API call.
	transactionCookiePath = "/v1/auth"
)

// Transaction is the state carried between the login redirect and the callback.
type Transaction struct {
	State        string `json:"state"`
	Nonce        string `json:"nonce"`
	CodeVerifier string `json:"code_verifier"`
	ReturnTo     string `json:"return_to"`
}

// SealTransaction encrypts a transaction for storage in a cookie.
func SealTransaction(cipher *secrets.Cipher, transaction Transaction) (string, error) {
	encoded, err := json.Marshal(transaction)
	if err != nil {
		return "", err
	}
	return cipher.Encrypt(string(encoded))
}

// OpenTransaction authenticates and decodes a sealed transaction. Any failure
// means the value was forged, truncated, or sealed under a different key.
func OpenTransaction(cipher *secrets.Cipher, sealed string) (Transaction, error) {
	plaintext, err := cipher.Decrypt(sealed)
	if err != nil {
		return Transaction{}, err
	}
	var transaction Transaction
	if err := json.Unmarshal([]byte(plaintext), &transaction); err != nil {
		return Transaction{}, err
	}
	return transaction, nil
}

// SafeReturnTo reduces a caller-supplied post-login destination to a same-origin
// path, falling back to "/". It is the open-redirect guard on /v1/auth/login,
// which is the one place the flow accepts untrusted input.
func SafeReturnTo(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") {
		return "/"
	}
	// "//host" and "/\host" are both read as protocol-relative URLs by browsers.
	if strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, `/\`) {
		return "/"
	}
	if strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsControl(r) }) {
		return "/"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil {
		return "/"
	}
	if parsed.Path != path.Clean(parsed.Path) {
		return "/"
	}
	return raw
}

// SessionCookie carries the ID token for the life of the browser session.
func SessionCookie(value string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		// Lax, never Strict: the IdP's callback is a cross-site top-level GET,
		// and Strict would withhold the cookie and break every login.
		SameSite: http.SameSiteLaxMode,
	}
}

// TransactionCookie carries one in-flight login.
func TransactionCookie(value string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     TransactionCookieName,
		Value:    value,
		Path:     transactionCookiePath,
		MaxAge:   transactionMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearedSessionCookie expires the session cookie.
func ClearedSessionCookie(secure bool) *http.Cookie {
	cookie := SessionCookie("", secure)
	cookie.MaxAge = -1
	return cookie
}

// ClearedTransactionCookie expires the transaction cookie.
func ClearedTransactionCookie(secure bool) *http.Cookie {
	cookie := TransactionCookie("", secure)
	cookie.MaxAge = -1
	return cookie
}
