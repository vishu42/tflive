package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/megagega/internal/app"
	"github.com/vishu42/megagega/internal/config"
	"github.com/vishu42/megagega/internal/postgres"
)

func main() {
	if err := run(context.Background(), os.Getenv); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, getenv func(string) string) error {
	cfg, err := config.LoadAPIConfig(getenv)
	if err != nil {
		return fmt.Errorf("load api config: %w", err)
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create postgres pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}

	if err := postgres.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate postgres: %w", err)
	}

	store := postgres.NewStore(pool)
	_ = app.NewService(app.Service{
		StackTemplates: store,
		TemplateRuns:   store,
	})

	return nil
}
