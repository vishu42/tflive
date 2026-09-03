package authn

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/encryption"
)

func testCipher(t *testing.T) *encryption.Cipher {
	t.Helper()
	cipher, err := encryption.NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	return cipher
}

func TestTransactionSealRoundTrip(t *testing.T) {
	cipher := testCipher(t)
	want := Transaction{State: "state-1", Nonce: "nonce-1", CodeVerifier: "verifier-1", ReturnTo: "/stacks/abc"}

	sealed, err := SealTransaction(cipher, want)
	if err != nil {
		t.Fatalf("SealTransaction returned error: %v", err)
	}
	for _, secret := range []string{want.State, want.Nonce, want.CodeVerifier, want.ReturnTo} {
		if contains(sealed, secret) {
			t.Fatalf("sealed transaction leaked %q: %s", secret, sealed)
		}
	}

	got, err := OpenTransaction(cipher, sealed)
	if err != nil {
		t.Fatalf("OpenTransaction returned error: %v", err)
	}
	want.IssuedAt = got.IssuedAt // stamped by SealTransaction, not caller-supplied
	if got != want {
		t.Fatalf("transaction = %#v, want %#v", got, want)
	}
	if since := time.Since(time.Unix(got.IssuedAt, 0)); since < 0 || since > time.Minute {
		t.Fatalf("IssuedAt = %v, want close to now", time.Unix(got.IssuedAt, 0))
	}
}

func TestOpenTransactionRejectsExpiredTransaction(t *testing.T) {
	// SealTransaction always stamps IssuedAt with time.Now(), so an expired
	// transaction is built directly rather than through SealTransaction — this
	// is what a transaction sealed long ago and replayed now looks like.
	cipher := testCipher(t)
	transaction := Transaction{
		State:    "state-1",
		IssuedAt: time.Now().Add(-(transactionMaxAge + 1) * time.Second).Unix(),
	}
	sealed := sealTransactionAt(t, cipher, transaction)

	if _, err := OpenTransaction(cipher, sealed); !errors.Is(err, ErrTransactionExpired) {
		t.Fatalf("OpenTransaction error = %v, want ErrTransactionExpired", err)
	}
}

// sealTransactionAt seals a transaction with the IssuedAt already set on it,
// bypassing SealTransaction's own timestamping. Production code has no such
// path; this exists so the test can forge an old transaction.
func sealTransactionAt(t *testing.T, cipher *encryption.Cipher, transaction Transaction) string {
	t.Helper()
	encoded, err := json.Marshal(transaction)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	sealed, err := cipher.Encrypt(string(encoded))
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}
	return sealed
}

func TestOpenTransactionAcceptsFreshTransaction(t *testing.T) {
	cipher := testCipher(t)
	sealed, err := SealTransaction(cipher, Transaction{State: "state-1"})
	if err != nil {
		t.Fatalf("SealTransaction returned error: %v", err)
	}
	if _, err := OpenTransaction(cipher, sealed); err != nil {
		t.Fatalf("OpenTransaction returned error: %v, want a fresh transaction to be accepted", err)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestOpenTransactionRejectsTampering(t *testing.T) {
	// A forgeable transaction cookie makes state worthless: an attacker who can
	// set it can set the query parameter too, and login-CSRF follows.
	cipher := testCipher(t)
	sealed, err := SealTransaction(cipher, Transaction{State: "state-1"})
	if err != nil {
		t.Fatalf("SealTransaction returned error: %v", err)
	}
	// Flip a bit inside the sealed body rather than at the base64 tail, which
	// can encode only padding. Any bit flip in GCM ciphertext or tag fails.
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatalf("DecodeString returned error: %v", err)
	}
	raw[len(raw)/2] ^= 0x01
	if _, err := OpenTransaction(cipher, base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("OpenTransaction accepted a tampered value")
	}
}

func TestOpenTransactionRejectsAnotherKey(t *testing.T) {
	sealed, err := SealTransaction(testCipher(t), Transaction{State: "state-1"})
	if err != nil {
		t.Fatalf("SealTransaction returned error: %v", err)
	}
	other, err := encryption.NewCipher("abcdefghijklmnopqrstuvwxyz123456")
	if err != nil {
		t.Fatalf("NewCipher returned error: %v", err)
	}
	if _, err := OpenTransaction(other, sealed); err == nil {
		t.Fatal("OpenTransaction accepted a value sealed with another key")
	}
}

func TestSafeReturnTo(t *testing.T) {
	for _, test := range []struct{ name, in, want string }{
		{name: "empty", in: "", want: "/"},
		{name: "path", in: "/stacks/abc", want: "/stacks/abc"},
		{name: "path with query", in: "/stacks?tab=runs", want: "/stacks?tab=runs"},
		{name: "protocol relative", in: "//evil.test/steal", want: "/"},
		{name: "backslash relative", in: `/\evil.test`, want: "/"},
		{name: "absolute url", in: "https://evil.test/steal", want: "/"},
		{name: "scheme relative uppercase", in: "//EVIL.test", want: "/"},
		{name: "relative", in: "stacks", want: "/"},
		{name: "parent traversal", in: "/../../etc", want: "/"},
		{name: "control character", in: "/stacks\n/evil", want: "/"},
		// The callback would restart the login it has just finished, and with
		// an SSO session standing the browser loops until it gives up. These
		// are server-side redirects, so the client's loop guard never sees a
		// page load to count.
		{name: "login route", in: "/v1/auth/login", want: "/"},
		{name: "login route with query", in: "/v1/auth/login?return_to=%2Fstacks", want: "/"},
		{name: "api route", in: "/v1/stacks", want: "/"},
		{name: "api root", in: "/v1", want: "/"},
		// Not an API path: only the /v1 segment itself is refused.
		{name: "v1 lookalike path", in: "/v10/stacks", want: "/v10/stacks"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := SafeReturnTo(test.in); got != test.want {
				t.Fatalf("SafeReturnTo(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestCookieAttributes(t *testing.T) {
	session := SessionCookie("token", true)
	if session.Name != SessionCookieName || !session.HttpOnly || !session.Secure ||
		session.SameSite != http.SameSiteLaxMode || session.Path != "/" {
		t.Fatalf("session cookie = %#v", session)
	}
	if session.MaxAge != 0 {
		t.Fatalf("session cookie MaxAge = %d, want 0 so it dies with the browser session", session.MaxAge)
	}

	transaction := TransactionCookie("sealed", false)
	if transaction.Name != TransactionCookieName || !transaction.HttpOnly || transaction.Secure ||
		transaction.SameSite != http.SameSiteLaxMode || transaction.Path != "/v1/auth" || transaction.MaxAge != 600 {
		t.Fatalf("transaction cookie = %#v", transaction)
	}

	if cleared := ClearedSessionCookie(true); cleared.Value != "" || cleared.MaxAge != -1 {
		t.Fatalf("cleared session cookie = %#v", cleared)
	}
	if cleared := ClearedTransactionCookie(true); cleared.Value != "" || cleared.MaxAge != -1 || cleared.Path != "/v1/auth" {
		t.Fatalf("cleared transaction cookie = %#v", cleared)
	}
}
