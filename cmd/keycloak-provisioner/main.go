package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/vishu42/tflive/internal/keycloak"
)

type provisionFunc func(context.Context, keycloak.Config) (keycloak.Result, error)
type logFunc func(string, ...any)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := log.New(os.Stdout, "", 0)
	if err := run(ctx, os.Getenv, keycloak.Provision, logger.Printf); err != nil {
		log.New(os.Stderr, "", 0).Printf("Keycloak provisioner failed: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string, provision provisionFunc, logf logFunc) error {
	cfg, err := keycloak.LoadConfig(getenv)
	if err != nil {
		return fmt.Errorf("load Keycloak provisioner config: %w", err)
	}
	result, err := provision(ctx, cfg)
	if err != nil {
		return fmt.Errorf("provision Keycloak: %w", err)
	}
	logf(
		"Keycloak realm %s provisioned: browser client %s, API audience %s, platform administrator %s, directory reader %s",
		result.Realm,
		result.WebClientID,
		result.APIClientID,
		result.PlatformAdminUsername,
		result.DirectoryReaderClientID,
	)
	return nil
}
