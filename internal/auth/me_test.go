package auth

import (
	"testing"
	"time"

	"github.com/vishu42/tflive/internal/authn"
)

func TestMeFromPrincipalReportsSessionExpiry(t *testing.T) {
	expiry := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	me := MeFromPrincipal(
		authn.Principal{Subject: "user-123", Name: "Ada", ExpiresAt: expiry},
		"tenant_123",
		GlobalCapabilities{},
	)
	if me.SessionExpiresAt != "2026-08-25T12:00:00Z" {
		t.Fatalf("SessionExpiresAt = %q", me.SessionExpiresAt)
	}
}

func TestMeFromPrincipalOmitsZeroExpiry(t *testing.T) {
	me := MeFromPrincipal(authn.Principal{Subject: "user-123"}, "tenant_123", GlobalCapabilities{})
	if me.SessionExpiresAt != "" {
		t.Fatalf("SessionExpiresAt = %q, want empty", me.SessionExpiresAt)
	}
}
