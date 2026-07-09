package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vishu42/megagega/internal/api"
	"github.com/vishu42/megagega/internal/app"
	"github.com/vishu42/megagega/internal/artifacts"
	"github.com/vishu42/megagega/internal/config"
	"github.com/vishu42/megagega/internal/postgres"
	"github.com/vishu42/megagega/internal/temporal"
	"go.temporal.io/sdk/client"
)

type postgresPool interface {
	Ping(context.Context) error
	Close()
}

type appRepositories interface {
	app.StackRepository
	app.StackTemplateRepository
	app.StackTemplateInstaller
	app.TemplateRunRepository
	app.TemplateRegistrationRepository
	app.TemplateRevisionMetadataRepository
	app.TemplateRevisionRepository
	app.TemplateRunLogRepository
}

type apiDependencies struct {
	newPostgresPool func(context.Context, string) (postgresPool, error)
	migratePostgres func(context.Context, postgresPool) error
	newStore        func(postgresPool) (appRepositories, error)
	dialTemporal    func(context.Context, temporal.Config) (client.Client, error)
	newDispatcher   func(client.Client, string) app.WorkflowDispatcher
	newLogReader    func(config.ArtifactStoreConfig) (app.TemplateRunLogReader, error)
	newService      func(app.Service) (*app.Service, error)
	listenAndServe  func(context.Context, string, http.Handler) error
}

func main() {
	if err := run(context.Background(), os.Getenv); err != nil {
		log.Fatal(err)
	}
}

func defaultAPIDependencies() apiDependencies {
	return apiDependencies{
		newPostgresPool: func(ctx context.Context, databaseURL string) (postgresPool, error) {
			return pgxpool.New(ctx, databaseURL)
		},
		migratePostgres: func(ctx context.Context, pool postgresPool) error {
			pgxPool, ok := pool.(*pgxpool.Pool)
			if !ok {
				return fmt.Errorf("unexpected postgres pool type %T", pool)
			}
			return postgres.Migrate(ctx, pgxPool)
		},
		newStore: func(pool postgresPool) (appRepositories, error) {
			pgxPool, ok := pool.(*pgxpool.Pool)
			if !ok {
				return nil, fmt.Errorf("unexpected postgres pool type %T", pool)
			}
			return postgres.NewStore(pgxPool), nil
		},
		dialTemporal: temporal.Dial,
		newDispatcher: func(temporalClient client.Client, taskQueue string) app.WorkflowDispatcher {
			return temporal.NewDispatcher(temporalClient, taskQueue)
		},
		newLogReader: func(cfg config.ArtifactStoreConfig) (app.TemplateRunLogReader, error) {
			store, err := artifacts.NewObjectStore(cfg)
			if err != nil {
				return nil, err
			}
			return artifacts.NewLogStore(store), nil
		},
		newService: func(service app.Service) (*app.Service, error) {
			return app.NewService(service), nil
		},
		listenAndServe: listenAndServe,
	}
}

func run(ctx context.Context, getenv func(string) string) error {
	return runWithDependencies(ctx, getenv, defaultAPIDependencies())
}

func runWithDependencies(ctx context.Context, getenv func(string) string, deps apiDependencies) error {
	cfg, err := config.LoadAPIConfig(getenv)
	if err != nil {
		return fmt.Errorf("load api config: %w", err)
	}

	pool, err := deps.newPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create postgres pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}

	if err := deps.migratePostgres(ctx, pool); err != nil {
		return fmt.Errorf("migrate postgres: %w", err)
	}

	store, err := deps.newStore(pool)
	if err != nil {
		return fmt.Errorf("wire service: %w", err)
	}

	temporalClient, err := deps.dialTemporal(ctx, temporal.Config{
		Address:   cfg.TemporalAddress,
		Namespace: cfg.TemporalNamespace,
	})
	if err != nil {
		return fmt.Errorf("dial temporal: %w", err)
	}
	defer temporalClient.Close()

	dispatcher := deps.newDispatcher(temporalClient, cfg.TemporalTaskQueue)
	logReader, err := deps.newLogReader(cfg.ArtifactStore)
	if err != nil {
		return fmt.Errorf("wire log reader: %w", err)
	}
	service, err := deps.newService(app.Service{
		Stacks:                   store,
		StackTemplates:           store,
		StackTemplateInstaller:   store,
		TemplateRuns:             store,
		TemplateRegistrations:    store,
		TemplateRevisionMetadata: store,
		TemplateRevisions:        store,
		TemplateRunLogs:          logReader,
		TemplateRunLogMetadata:   store,
		Workflows:                dispatcher,
	})
	if err != nil {
		return fmt.Errorf("wire service: %w", err)
	}

	handler := api.NewServer(service)
	if err := deps.listenAndServe(ctx, cfg.HTTPAddress, handler); err != nil {
		return fmt.Errorf("listen and serve api: %w", err)
	}

	return nil
}

func listenAndServe(ctx context.Context, address string, handler http.Handler) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	log.Printf("api listening on %s", listener.Addr().String())

	server := &http.Server{
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
