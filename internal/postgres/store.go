package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/authn"
	"github.com/vishu42/tflive/internal/queue"
	"github.com/vishu42/tflive/internal/secrets"
)

var ErrNotFound = errors.New("postgres: not found")

type Store struct {
	pool             *pgxpool.Pool
	credentialCipher *secrets.Cipher
	sessionCipher    *secrets.Cipher
	queueSpecs       *queue.SpecRegistry
}

// Option configures a Store at construction.
type Option func(*Store)

// WithQueueSpecs lets the store resolve a queue.Request into an resource key
// and mode. Specs carry no dependencies, so a producer-only binary registers
// kinds without building the handlers that deliver them. Stores that never
// enqueue can omit it.
func WithQueueSpecs(specs *queue.SpecRegistry) Option {
	return func(store *Store) { store.queueSpecs = specs }
}

// NewStore creates a repository store. The credential cipher is injected by the
// caller from internal/config; the store reads no environment of its own.
func NewStore(pool *pgxpool.Pool, options ...Option) *Store {
	store := &Store{pool: pool}
	for _, option := range options {
		option(store)
	}
	return store
}

// WithCredentialCipher supplies the process-wide credential encryption cipher.
// Without it, Encrypt and Decrypt return app.ErrCredentialEncryptionUnavailable.
func WithCredentialCipher(cipher *secrets.Cipher) Option {
	return func(store *Store) { store.credentialCipher = cipher }
}

// WithSessionCipher supplies the process-wide session encryption cipher, used
// only for session rows. Without it, encryptSession and decryptSession return
// authn.ErrSessionEncryptionUnavailable.
func WithSessionCipher(cipher *secrets.Cipher) Option {
	return func(store *Store) { store.sessionCipher = cipher }
}

// Encrypt protects a credential value with the configured application cipher before persistence.
func (store *Store) Encrypt(value string) (string, error) {
	if store.credentialCipher == nil {
		return "", app.ErrCredentialEncryptionUnavailable
	}
	return store.credentialCipher.Encrypt(value)
}

// Decrypt opens stored credential ciphertext for worker-local runtime use.
func (store *Store) Decrypt(value string) (string, error) {
	if store.credentialCipher == nil {
		return "", app.ErrCredentialEncryptionUnavailable
	}
	return store.credentialCipher.Decrypt(value)
}

// encryptSession protects a session's ID token with the configured session
// cipher before persistence. This cipher is separate from credentialCipher:
// SESSION_ENCRYPTION_KEY is required configuration, while
// CREDENTIAL_ENCRYPTION_KEY is optional, and a session row must not fail to
// write because no one configured customer credential encryption.
func (store *Store) encryptSession(value string) (string, error) {
	if store.sessionCipher == nil {
		return "", authn.ErrSessionEncryptionUnavailable
	}
	return store.sessionCipher.Encrypt(value)
}

// decryptSession opens a stored session's ID token ciphertext.
func (store *Store) decryptSession(value string) (string, error) {
	if store.sessionCipher == nil {
		return "", authn.ErrSessionEncryptionUnavailable
	}
	return store.sessionCipher.Decrypt(value)
}
