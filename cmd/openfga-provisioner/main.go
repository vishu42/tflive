package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	openfga "github.com/vishu42/tflive/internal/openfga"
	openfgamodel "github.com/vishu42/tflive/openfga"
)

type executeFunc func(context.Context, string, openfga.Config, openfga.AuthorizationModel) (openfga.Result, error)

type sanitizedExecutionError struct {
	message string
	cause   error
}

func (e sanitizedExecutionError) Error() string {
	return e.message
}

func (e sanitizedExecutionError) Is(target error) bool {
	return errors.Is(e.cause, target)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, openfgamodel.AuthorizationModelJSON(), execute, os.Stdout, os.Stderr); err != nil {
		log.New(os.Stderr, "", 0).Printf("OpenFGA provisioner failed: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, modelJSON []byte, execute executeFunc, stdout, stderr io.Writer) error {
	operation := "verify"
	if len(args) == 1 {
		operation = args[0]
	}
	if len(args) > 1 || (operation != "bootstrap" && operation != "verify") {
		return fmt.Errorf("operation must be bootstrap or verify")
	}
	cfg, err := openfga.LoadConfig(getenv)
	if err != nil {
		return fmt.Errorf("load OpenFGA provisioner config: %w", err)
	}
	model, err := openfga.ParseAuthorizationModel(modelJSON)
	if err != nil {
		return fmt.Errorf("parse repository authorization model: %w", err)
	}
	result, err := execute(ctx, operation, cfg, model)
	if err != nil {
		return sanitizedExecutionError{
			message: fmt.Sprintf("%s OpenFGA: %s", operation, redact(err.Error(), cfg.APIToken)),
			cause:   err,
		}
	}
	if _, err := fmt.Fprintf(stdout, "OPENFGA_STORE_ID=%s\nOPENFGA_MODEL_ID=%s\n", result.StoreID, result.ModelID); err != nil {
		return fmt.Errorf("write OpenFGA environment assignments: %w", err)
	}
	if _, err := fmt.Fprintf(stderr, "OpenFGA %s succeeded for explicit store and model identifiers\n", operation); err != nil {
		return fmt.Errorf("write OpenFGA diagnostic: %w", err)
	}
	return nil
}

func execute(ctx context.Context, operation string, cfg openfga.Config, model openfga.AuthorizationModel) (openfga.Result, error) {
	client := openfga.NewClient(cfg)
	if operation == "bootstrap" {
		return openfga.Bootstrap(ctx, cfg, model, client)
	}
	return openfga.Verify(ctx, cfg, model, client)
}

func redact(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}
