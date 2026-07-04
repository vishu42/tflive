package config

import (
	"errors"
	"fmt"
	"strings"
)

const (
	DefaultHTTPAddress       = ":8081"
	DefaultTemporalTaskQueue = "terraform-runs"
	DefaultWorkerRunRoot     = "/tmp/megagega/runs"
)

var ErrInvalidConfig = errors.New("invalid config")

type APIConfig struct {
	DatabaseURL       string
	HTTPAddress       string
	TemporalAddress   string
	TemporalNamespace string
	TemporalTaskQueue string
}

type WorkerConfig struct {
	DatabaseURL       string
	TemporalAddress   string
	TemporalNamespace string
	TemporalTaskQueue string
	WorkerRunRoot     string
}

func LoadAPIConfig(getenv func(string) string) (APIConfig, error) {
	cfg := APIConfig{
		DatabaseURL:       strings.TrimSpace(getenv("DATABASE_URL")),
		HTTPAddress:       strings.TrimSpace(getenv("HTTP_ADDRESS")),
		TemporalAddress:   strings.TrimSpace(getenv("TEMPORAL_ADDRESS")),
		TemporalNamespace: strings.TrimSpace(getenv("TEMPORAL_NAMESPACE")),
		TemporalTaskQueue: strings.TrimSpace(getenv("TEMPORAL_TASK_QUEUE")),
	}
	if cfg.HTTPAddress == "" {
		cfg.HTTPAddress = DefaultHTTPAddress
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
		DatabaseURL:       strings.TrimSpace(getenv("DATABASE_URL")),
		TemporalAddress:   strings.TrimSpace(getenv("TEMPORAL_ADDRESS")),
		TemporalNamespace: strings.TrimSpace(getenv("TEMPORAL_NAMESPACE")),
		TemporalTaskQueue: strings.TrimSpace(getenv("TEMPORAL_TASK_QUEUE")),
		WorkerRunRoot:     strings.TrimSpace(getenv("WORKER_RUN_ROOT")),
	}
	if cfg.TemporalTaskQueue == "" {
		cfg.TemporalTaskQueue = DefaultTemporalTaskQueue
	}
	if cfg.WorkerRunRoot == "" {
		cfg.WorkerRunRoot = DefaultWorkerRunRoot
	}

	if cfg.TemporalAddress == "" {
		return WorkerConfig{}, fmt.Errorf("%w: TEMPORAL_ADDRESS is required", ErrInvalidConfig)
	}
	if cfg.DatabaseURL == "" {
		return WorkerConfig{}, fmt.Errorf("%w: DATABASE_URL is required", ErrInvalidConfig)
	}

	return cfg, nil
}
