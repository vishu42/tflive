package temporal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.temporal.io/sdk/client"
)

const defaultNamespace = "default"

var ErrInvalidConfig = errors.New("temporal: invalid config")

type Config struct {
	Address   string
	Namespace string
}

func Dial(ctx context.Context, cfg Config) (client.Client, error) {
	address := strings.TrimSpace(cfg.Address)
	if address == "" {
		return nil, fmt.Errorf("%w: address is required", ErrInvalidConfig)
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("dial temporal: %w", ctx.Err())
	default:
	}

	namespace := strings.TrimSpace(cfg.Namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}

	temporalClient, err := client.Dial(client.Options{
		HostPort:  address,
		Namespace: namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("dial temporal: %w", err)
	}

	return temporalClient, nil
}
