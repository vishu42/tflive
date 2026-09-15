package runseal

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestSealedValueOpensOnlyWithItsRunKey(t *testing.T) {
	t.Parallel()

	ring := NewKeyRing()
	publicA, err := ring.Generate("tenant/run_a")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if _, err := ring.Generate("tenant/run_b"); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	secret := map[string]string{"AWS_SECRET_ACCESS_KEY": "canary-7f3a"}
	sealed, err := Seal(publicA, secret)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	if bytes.Contains(sealed, []byte("canary-7f3a")) {
		t.Fatal("sealed value contains the plaintext")
	}

	var opened map[string]string
	if err := ring.Open("tenant/run_a", sealed, &opened); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if opened["AWS_SECRET_ACCESS_KEY"] != "canary-7f3a" {
		t.Fatalf("opened = %v, want the sealed secret", opened)
	}

	// Another run's key must not open it.
	if err := ring.Open("tenant/run_b", sealed, &opened); err == nil {
		t.Fatal("run_b's key opened a value sealed to run_a")
	}
}

func TestForgottenKeyCannotOpen(t *testing.T) {
	t.Parallel()

	ring := NewKeyRing()
	public, _ := ring.Generate("tenant/run")
	sealed, _ := Seal(public, "token")
	ring.Forget("tenant/run")

	var opened string
	if err := ring.Open("tenant/run", sealed, &opened); !errors.Is(err, ErrNoKey) {
		t.Fatalf("error = %v, want ErrNoKey", err)
	}
}

func TestGenerateEvictsExpiredKeys(t *testing.T) {
	t.Parallel()

	ring := NewKeyRing()
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	ring.now = func() time.Time { return now }
	public, _ := ring.Generate("tenant/abandoned")
	sealed, _ := Seal(public, "token")

	now = now.Add(DefaultKeyTTL + time.Minute)
	if _, err := ring.Generate("tenant/next"); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	var opened string
	if err := ring.Open("tenant/abandoned", sealed, &opened); !errors.Is(err, ErrNoKey) {
		t.Fatalf("error = %v, want the abandoned key evicted", err)
	}
}

func TestSealRejectsMalformedPublicKey(t *testing.T) {
	t.Parallel()

	if _, err := Seal([]byte("short"), "token"); err == nil {
		t.Fatal("Seal accepted a malformed public key")
	}
}
