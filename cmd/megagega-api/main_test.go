package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/vishu42/megagega/internal/config"
)

func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), func(string) string { return "" })
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestRunMigratesRealPostgresWhenDSNIsSet(t *testing.T) {
	t.Parallel()

	dsn := os.Getenv("MEGAGEGA_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("MEGAGEGA_POSTGRES_TEST_DSN is not set")
	}

	err := run(context.Background(), func(key string) string {
		if key == "DATABASE_URL" {
			return dsn
		}
		return ""
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
}
