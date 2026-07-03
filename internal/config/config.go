package config

import (
	"errors"
	"fmt"
	"strings"
)

const DefaultTemporalTaskQueue = "terraform-runs"

var ErrInvalidConfig = errors.New("invalid config")

type APIConfig struct {
	DatabaseURL       string
	TemporalAddress   string
	TemporalNamespace string
	TemporalTaskQueue string
}

type WorkerConfig struct {
	TemporalAddress   string
	TemporalNamespace string
	TemporalTaskQueue string
}

func LoadAPIConfig(getenv func(string) string) (APIConfig, error) {
	cfg := APIConfig{
		DatabaseURL:       strings.TrimSpace(getenv("DATABASE_URL")),
		TemporalAddress:   strings.TrimSpace(getenv("TEMPORAL_ADDRESS")),
		TemporalNamespace: strings.TrimSpace(getenv("TEMPORAL_NAMESPACE")),
		TemporalTaskQueue: strings.TrimSpace(getenv("TEMPORAL_TASK_QUEUE")),
	}
	if cfg.TemporalTaskQueue == "" {
		cfg.TemporalTaskQueue = DefaultTemporalTaskQueue
	}

	if cfg.DatabaseURL == "" {
		return APIConfig{}, fmt.Errorf("%w: DATABASE_URL is required", ErrInvalidConfig)
	}
	if cfg.TemporalAddress == "" {
		return APIConfig{}, fmt.Errorf("%w: TEMPORAL_ADDRESS is required", ErrInvalidConfig)
	}

	return cfg, nil
}

func LoadWorkerConfig(getenv func(string) string) (WorkerConfig, error) {
	cfg := WorkerConfig{
		TemporalAddress:   strings.TrimSpace(getenv("TEMPORAL_ADDRESS")),
		TemporalNamespace: strings.TrimSpace(getenv("TEMPORAL_NAMESPACE")),
		TemporalTaskQueue: strings.TrimSpace(getenv("TEMPORAL_TASK_QUEUE")),
	}
	if cfg.TemporalTaskQueue == "" {
		cfg.TemporalTaskQueue = DefaultTemporalTaskQueue
	}

	if cfg.TemporalAddress == "" {
		return WorkerConfig{}, fmt.Errorf("%w: TEMPORAL_ADDRESS is required", ErrInvalidConfig)
	}

	return cfg, nil
}
