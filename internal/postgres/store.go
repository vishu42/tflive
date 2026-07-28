package postgres

import (
	"errors"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/tflive/internal/app"
	"github.com/vishu42/tflive/internal/credentials"
)

var ErrNotFound = errors.New("postgres: not found")

type Store struct {
	pool             *pgxpool.Pool
	credentialCipher *credentials.Cipher
}

// NewStore creates a repository store and loads the process-wide credential encryption key.
func NewStore(pool *pgxpool.Pool) *Store {
	var cipher *credentials.Cipher
	if rawKey := os.Getenv("CREDENTIAL_ENCRYPTION_KEY"); rawKey != "" {
		cipher, _ = credentials.NewCipher(rawKey)
	}
	return &Store{pool: pool, credentialCipher: cipher}
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
