// Package runseal carries secrets from the control plane to the executor
// through Temporal without Temporal ever holding them in plaintext.
//
// The executor generates a key pair per run and keeps the private key in
// memory. The control plane seals each secret to the public key, and the
// executor opens it. Workflow history therefore holds only a public key and
// ciphertext, and anything that can read history, including other runs, learns
// nothing from it.
package runseal

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/nacl/box"
)

const keySize = 32

// DefaultKeyTTL bounds how long a key survives a run that never released it:
// longer than the 24-hour session a run's activities are pinned to.
const DefaultKeyTTL = 25 * time.Hour

// ErrNoKey reports a sealed value for a run this process holds no key for,
// typically because the executor restarted and its session died with it.
var ErrNoKey = errors.New("no key for run")

// Seal encrypts value, as JSON, so only the holder of publicKey's private key
// can read it.
func Seal(publicKey []byte, value any) ([]byte, error) {
	if len(publicKey) != keySize {
		return nil, fmt.Errorf("seal: public key is %d bytes, want %d", len(publicKey), keySize)
	}
	plaintext, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("seal: encode value: %w", err)
	}
	var recipient [keySize]byte
	copy(recipient[:], publicKey)
	sealed, err := box.SealAnonymous(nil, plaintext, &recipient, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	return sealed, nil
}

type key struct {
	public  [keySize]byte
	private [keySize]byte
	created time.Time
}

// KeyRing holds this executor's per-run private keys. It is safe for
// concurrent use by the activities of many runs.
type KeyRing struct {
	mu   sync.Mutex
	keys map[string]key
	ttl  time.Duration
	now  func() time.Time
}

func NewKeyRing() *KeyRing {
	return &KeyRing{keys: map[string]key{}, ttl: DefaultKeyTTL, now: time.Now}
}

// Generate creates a fresh key pair for id, replacing any earlier one, and
// returns the public half. Keys older than the ring's TTL are evicted.
func (ring *KeyRing) Generate(id string) ([]byte, error) {
	public, private, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate run key: %w", err)
	}
	ring.mu.Lock()
	defer ring.mu.Unlock()
	now := ring.now()
	for existing, entry := range ring.keys {
		if now.Sub(entry.created) > ring.ttl {
			delete(ring.keys, existing)
		}
	}
	ring.keys[id] = key{public: *public, private: *private, created: now}
	return append([]byte(nil), public[:]...), nil
}

// Open decrypts sealed with id's key into value.
func (ring *KeyRing) Open(id string, sealed []byte, value any) error {
	ring.mu.Lock()
	entry, ok := ring.keys[id]
	ring.mu.Unlock()
	if !ok {
		return fmt.Errorf("open sealed value: %w %q", ErrNoKey, id)
	}
	plaintext, ok := box.OpenAnonymous(nil, sealed, &entry.public, &entry.private)
	if !ok {
		return fmt.Errorf("open sealed value for run %q: decryption failed", id)
	}
	if err := json.Unmarshal(plaintext, value); err != nil {
		return fmt.Errorf("open sealed value: decode: %w", err)
	}
	return nil
}

// Forget drops id's key. Anything still sealed to it becomes unreadable.
func (ring *KeyRing) Forget(id string) {
	ring.mu.Lock()
	defer ring.mu.Unlock()
	delete(ring.keys, id)
}
