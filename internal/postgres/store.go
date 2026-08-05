package postgres

import (
	"errors"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/credentials"
	"github.com/vishu42/tflive/internal/queue"
)

var ErrNotFound = errors.New("postgres: not found")

type Store struct {
	pool             *pgxpool.Pool
	credentialCipher *credentials.Cipher
	queueRegistry    *queue.Registry
}

// Option configures a Store at construction.
type Option func(*Store)

// WithQueueRegistry lets the store resolve a queue.Request into an ordering key
// and mode. Stores that never enqueue can omit it.
func WithQueueRegistry(registry *queue.Registry) Option {
	return func(store *Store) { store.queueRegistry = registry }
}

// NewStore creates a repository store and loads the process-wide credential encryption key.
func NewStore(pool *pgxpool.Pool, options ...Option) *Store {
	var cipher *credentials.Cipher
	if rawKey := os.Getenv("CREDENTIAL_ENCRYPTION_KEY"); rawKey != "" {
		cipher, _ = credentials.NewCipher(rawKey)
	}
	store := &Store{pool: pool, credentialCipher: cipher}
	for _, option := range options {
		option(store)
	}
	return store
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
