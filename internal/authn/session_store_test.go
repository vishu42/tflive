package authn

import (
	"strings"
	"testing"
	"time"
)

func TestNewSessionIDIsUnpredictableAndURLSafe(t *testing.T) {
	first, err := NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	second, err := NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	if first == second {
		t.Fatal("two session IDs are identical, so they are not random")
	}
	// 32 bytes base64url-encoded without padding.
	if len(first) != 43 {
		t.Fatalf("session ID length = %d, want 43", len(first))
	}
	if strings.ContainsAny(first, "+/=") {
		t.Fatalf("session ID %q is not URL-safe", first)
	}
}

func TestHashSessionIDIsStableAndNotTheInput(t *testing.T) {
	raw, err := NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	hash := HashSessionID(raw)
	if hash == raw {
		t.Fatal("hash equals the raw ID, so the database would hold a usable cookie")
	}
	if hash != HashSessionID(raw) {
		t.Fatal("hash is not stable, so a session could never be looked up twice")
	}
}

func TestExpiresAtIsTheEarlierBound(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	idleTTL := time.Hour

	t.Run("idle bound is earlier", func(t *testing.T) {
		session := Session{
			LastSeenAt:        now,
			AbsoluteExpiresAt: now.Add(8 * time.Hour),
		}
		if got, want := session.ExpiresAt(idleTTL), now.Add(time.Hour); !got.Equal(want) {
			t.Fatalf("ExpiresAt = %v, want %v", got, want)
		}
	})

	t.Run("absolute bound is earlier", func(t *testing.T) {
		session := Session{
			LastSeenAt:        now,
			AbsoluteExpiresAt: now.Add(10 * time.Minute),
		}
		if got, want := session.ExpiresAt(idleTTL), now.Add(10*time.Minute); !got.Equal(want) {
			t.Fatalf("ExpiresAt = %v, want %v", got, want)
		}
	})
}

func TestIsLive(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	idleTTL := time.Hour
	live := Session{LastSeenAt: now, AbsoluteExpiresAt: now.Add(8 * time.Hour)}

	tests := []struct {
		name    string
		session Session
		at      time.Time
		want    bool
	}{
		{"fresh", live, now, true},
		{"just inside the idle bound", live, now.Add(59 * time.Minute), true},
		{"past the idle bound", live, now.Add(61 * time.Minute), false},
		{
			name:    "past the absolute bound despite recent activity",
			session: Session{LastSeenAt: now.Add(8 * time.Hour), AbsoluteExpiresAt: now.Add(8 * time.Hour)},
			at:      now.Add(8*time.Hour + time.Second),
			want:    false,
		},
		{
			name:    "revoked",
			session: Session{LastSeenAt: now, AbsoluteExpiresAt: now.Add(8 * time.Hour), RevokedAt: now},
			at:      now,
			want:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.session.IsLive(test.at, idleTTL); got != test.want {
				t.Fatalf("IsLive = %v, want %v", got, test.want)
			}
		})
	}
}
