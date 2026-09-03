package authn

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. They are encoded into every hash rather than only living
// here, so raising them later verifies rows written under the old cost without
// a migration: the encoding says what it was hashed with.
//
// Memory is the expensive dimension on purpose — it is what a GPU cannot
// parallelise away — and it is also the one that costs the server, at
// passwordMemoryKiB per verification in flight.
//
// Nothing currently bounds how many are in flight. The login route is
// unauthenticated and not rate limited (deliberately descoped from #211), so
// concurrent requests multiply this constant directly: sixteen at once is a
// gigabyte. That is acceptable for the POC and demo deployments this feature
// exists for, and is not acceptable for an internet-facing one. Whatever fixes
// it — a rate limiter, or a semaphore capping concurrent verifications — is
// what makes this constant safe to keep at 64 MiB; until then the two facts
// belong next to each other rather than in separate files.
const (
	passwordSaltBytes = 16
	passwordKeyBytes  = 32
	passwordMemoryKiB = 64 * 1024
	passwordTime      = 3
	passwordThreads   = 2
)

// HashPassword returns a PHC-encoded argon2id hash with a fresh random salt.
func HashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	digest := argon2.IDKey([]byte(password), salt, passwordTime, passwordMemoryKiB, passwordThreads, passwordKeyBytes)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		passwordMemoryKiB, passwordTime, passwordThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	), nil
}

// VerifyPassword reports whether password produces encoded's digest under
// encoded's own parameters.
//
// Every malformed input is false rather than an error. The callers are a login
// handler, which has one answer for every failure, and the unknown-username
// path, which verifies against DummyPasswordHash and must not behave
// differently for having no row.
func VerifyPassword(encoded, password string) bool {
	fields := strings.Split(encoded, "$")
	if len(fields) != 6 || fields[0] != "" || fields[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(fields[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memoryKiB, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(fields[3], "m=%d,t=%d,p=%d", &memoryKiB, &time, &threads); err != nil {
		return false
	}
	// argon2.IDKey panics on a zero cost, and a zero-length digest would make
	// the comparison below trivially satisfiable.
	if memoryKiB == 0 || time == 0 || threads == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(fields[4])
	if err != nil || len(salt) == 0 {
		return false
	}
	digest, err := base64.RawStdEncoding.DecodeString(fields[5])
	if err != nil || len(digest) == 0 {
		return false
	}

	computed := argon2.IDKey([]byte(password), salt, time, memoryKiB, threads, uint32(len(digest)))
	return subtle.ConstantTimeCompare(computed, digest) == 1
}

// DummyPasswordHash is verified against when no account matches the submitted
// username, so an unknown username costs the same argon2id run as a known one.
// Without it the handler would return before hashing, and the timing difference
// would enumerate accounts.
//
// It is a real hash of a discarded 32 bytes of randomness, not a placeholder:
// nothing verifies against it, and the preimage was never written down.
const DummyPasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$QKYbclYn15wfy7StA8l0Tg$HH1aiLbtTZyQtkafPHgmXHjku8R3oCSHxiacqAz6zag"
