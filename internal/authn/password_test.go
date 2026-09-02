package authn

import (
	"strings"
	"testing"
)

func TestVerifyPasswordAcceptsTheHashedPassword(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if !VerifyPassword(encoded, "correct horse battery staple") {
		t.Fatal("VerifyPassword rejected the password it hashed")
	}
}

func TestVerifyPasswordRejectsAWrongPassword(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if VerifyPassword(encoded, "Correct horse battery staple") {
		t.Fatal("VerifyPassword accepted a wrong password")
	}
}

// A per-hash salt is what stops one precomputed table covering every account,
// so two hashes of the same password must not be equal.
func TestHashPasswordSaltsEachHash(t *testing.T) {
	first, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	second, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if first == second {
		t.Fatal("two hashes of the same password are identical, so the salt is not per-hash")
	}
	if !VerifyPassword(second, "same password") {
		t.Fatal("the second hash does not verify")
	}
}

// The encoding carries its own parameters, so a cost increase later can verify
// rows written before it without a migration.
func TestHashPasswordEncodesArgon2idParameters(t *testing.T) {
	encoded, err := HashPassword("whatever")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("encoding %q is not a PHC argon2id string", encoded)
	}
}

// VerifyPassword is reached with whatever is in the column, and on the
// unknown-username path with no column at all. Every malformed input must be a
// plain false rather than a panic.
func TestVerifyPasswordRejectsMalformedEncodings(t *testing.T) {
	valid, err := HashPassword("password")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	for name, encoded := range map[string]string{
		"empty":             "",
		"not phc":           "password",
		"too few fields":    "$argon2id$v=19$m=65536,t=3,p=2",
		"wrong algorithm":   strings.Replace(valid, "argon2id", "argon2i", 1),
		"unknown version":   strings.Replace(valid, "v=19", "v=16", 1),
		"unparsable params": strings.Replace(valid, "m=", "m=x", 1),
		"salt not base64":   "$argon2id$v=19$m=65536,t=3,p=2$!!!!$" + strings.Split(valid, "$")[5],
		"digest not base64": "$argon2id$v=19$m=65536,t=3,p=2$" + strings.Split(valid, "$")[4] + "$!!!!",
	} {
		t.Run(name, func(t *testing.T) {
			if VerifyPassword(encoded, "password") {
				t.Fatalf("VerifyPassword accepted a malformed encoding %q", encoded)
			}
		})
	}
}

// The unknown-username path verifies against this constant so that a request
// for a user that does not exist costs the same as one for a user that does.
// If it were not a real hash, that path would return early and the timing
// difference would enumerate accounts.
func TestDummyPasswordHashIsAVerifiableArgon2idHash(t *testing.T) {
	if !strings.HasPrefix(DummyPasswordHash, "$argon2id$v=19$") {
		t.Fatalf("DummyPasswordHash %q is not a PHC argon2id string", DummyPasswordHash)
	}
	if VerifyPassword(DummyPasswordHash, "password") {
		t.Fatal("DummyPasswordHash verified against a guessable password")
	}
}
