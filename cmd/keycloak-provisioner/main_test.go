package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/vishu42/tflive/internal/keycloak"
)

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	called := false
	err := run(context.Background(), func(string) string { return "" }, func(context.Context, keycloak.Config) (keycloak.Result, error) {
		called = true
		return keycloak.Result{}, nil
	}, func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "KEYCLOAK_ADMIN_URL is required") {
		t.Fatalf("run() error = %v", err)
	}
	if called {
		t.Fatal("provisioner called with invalid config")
	}
}

func TestRunWrapsProvisioningFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("Keycloak rejected role mapping")
	err := run(context.Background(), commandTestEnv(), func(context.Context, keycloak.Config) (keycloak.Result, error) {
		return keycloak.Result{}, want
	}, func(string, ...any) {})
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "provision Keycloak") {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunLogsOnlyNonSensitiveResultIdentifiers(t *testing.T) {
	t.Parallel()

	var logLine string
	err := run(context.Background(), commandTestEnv(), func(_ context.Context, cfg keycloak.Config) (keycloak.Result, error) {
		return keycloak.Result{
			Realm:                 cfg.Realm,
			WebClientID:           cfg.WebClientID,
			APIClientID:           cfg.APIClientID,
			PlatformAdminUsername: cfg.PlatformAdminUsername,
			DirectoryReaderClientID: "tflive-directory-reader",
		}, nil
	}, func(format string, args ...any) {
		logLine = fmt.Sprintf(format, args...)
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	for _, want := range []string{"tflive", "tflive-web", "tflive-api", "tflive-platform-admin", "tflive-directory-reader"} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("log = %q, missing %q", logLine, want)
		}
	}
	for _, secret := range []string{"master-local-only-secret", "platform-local-only-secret", "directory-reader-local-only-secret"} {
		if strings.Contains(logLine, secret) {
			t.Fatalf("log leaked secret %q: %q", secret, logLine)
		}
	}
}

func TestRunPassesCancellationToProvisioner(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := run(ctx, commandTestEnv(), func(ctx context.Context, _ keycloak.Config) (keycloak.Result, error) {
		return keycloak.Result{}, ctx.Err()
	}, func(string, ...any) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run() error = %v, want context canceled", err)
	}
}

func commandTestEnv() func(string) string {
	values := map[string]string{
		"KEYCLOAK_ADMIN_URL":                    "http://keycloak:8080",
		"KEYCLOAK_ADMIN_USERNAME":               "tflive-admin",
		"KEYCLOAK_ADMIN_PASSWORD":               "master-local-only-secret",
		"KEYCLOAK_WEB_REDIRECT_URIS":            "http://localhost:5173/",
		"KEYCLOAK_WEB_ORIGINS":                  "http://localhost:5173",
		"KEYCLOAK_PLATFORM_ADMIN_USERNAME":      "tflive-platform-admin",
		"KEYCLOAK_PLATFORM_ADMIN_PASSWORD":      "platform-local-only-secret",
		"KEYCLOAK_PLATFORM_ADMIN_EMAIL":         "tflive-platform-admin@local.test",
		"KEYCLOAK_PLATFORM_ADMIN_FIRST_NAME":    "tflive",
		"KEYCLOAK_PLATFORM_ADMIN_LAST_NAME":     "Platform Administrator",
		"KEYCLOAK_DIRECTORY_READER_CLIENT_SECRET": "directory-reader-local-only-secret",
	}
	return func(name string) string { return values[name] }
}
