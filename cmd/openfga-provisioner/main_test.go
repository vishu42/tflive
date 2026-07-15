package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	openfga "github.com/vishu42/tflive/internal/openfga"
	openfgamodel "github.com/vishu42/tflive/openfga"
)

func TestRunDefaultsToVerifyAndPrintsOnlyEnvironmentAssignments(t *testing.T) {
	t.Parallel()

	var operation string
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), nil, commandEnv(true), openfgamodel.AuthorizationModelJSON(), func(_ context.Context, got string, cfg openfga.Config, model openfga.AuthorizationModel) (openfga.Result, error) {
		operation = got
		return openfga.Result{StoreID: cfg.StoreID, ModelID: cfg.ModelID}, nil
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if operation != "verify" {
		t.Fatalf("operation = %q", operation)
	}
	if got, want := stdout.String(), "OPENFGA_STORE_ID=store-id\nOPENFGA_MODEL_ID=model-id\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if strings.Contains(stdout.String()+stderr.String(), "secret-token") {
		t.Fatal("output leaked API token")
	}
}

func TestRunBootstrapDoesNotRequireIDs(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := run(context.Background(), []string{"bootstrap"}, commandEnv(false), openfgamodel.AuthorizationModelJSON(), func(_ context.Context, operation string, _ openfga.Config, _ openfga.AuthorizationModel) (openfga.Result, error) {
		if operation != "bootstrap" {
			t.Fatalf("operation = %q", operation)
		}
		return openfga.Result{StoreID: "new-store", ModelID: "new-model"}, nil
	}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "OPENFGA_STORE_ID=new-store") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunRejectsUnknownOperationAndInvalidModel(t *testing.T) {
	t.Parallel()

	execute := func(context.Context, string, openfga.Config, openfga.AuthorizationModel) (openfga.Result, error) {
		t.Fatal("execute called")
		return openfga.Result{}, nil
	}
	for _, test := range []struct {
		args  []string
		model []byte
		want  string
	}{
		{args: []string{"destroy"}, model: openfgamodel.AuthorizationModelJSON(), want: "operation must be bootstrap or verify"},
		{args: []string{"verify"}, model: []byte("{"), want: "parse repository authorization model"},
	} {
		err := run(context.Background(), test.args, commandEnv(true), test.model, execute, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("run() error = %v, want containing %q", err, test.want)
		}
	}
}

func TestRunPreservesCancellationAndRedactsExecutionFailure(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	secretErr := fmt.Errorf("backend echoed secret-token: %w", context.Canceled)
	err := run(ctx, []string{"bootstrap"}, commandEnv(false), openfgamodel.AuthorizationModelJSON(), func(context.Context, string, openfga.Config, openfga.AuthorizationModel) (openfga.Result, error) {
		return openfga.Result{}, secretErr
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "context canceled") || !strings.Contains(err.Error(), "[REDACTED]") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunReportsEnvironmentOutputFailure(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"bootstrap"}, commandEnv(false), openfgamodel.AuthorizationModelJSON(), func(context.Context, string, openfga.Config, openfga.AuthorizationModel) (openfga.Result, error) {
		return openfga.Result{StoreID: "store-id", ModelID: "model-id"}, nil
	}, failingWriter{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "write OpenFGA environment assignments") {
		t.Fatalf("run() error = %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("output unavailable")
}

func commandEnv(withIDs bool) func(string) string {
	values := map[string]string{
		"OPENFGA_API_URL":   "http://openfga:8080",
		"OPENFGA_API_TOKEN": "secret-token",
	}
	if withIDs {
		values["OPENFGA_STORE_ID"] = "store-id"
		values["OPENFGA_MODEL_ID"] = "model-id"
	}
	return func(name string) string { return values[name] }
}
