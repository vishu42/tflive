package config

import (
	"errors"
	"testing"
)

func TestLoadAPIConfigReadsAPISettings(t *testing.T) {
	t.Parallel()

	cfg, err := LoadAPIConfig(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return " postgres://user:pass@localhost:5432/db?sslmode=disable "
		case "TEMPORAL_ADDRESS":
			return " localhost:7233 "
		case "TEMPORAL_NAMESPACE":
			return " megagega "
		case "TEMPORAL_TASK_QUEUE":
			return " terraform-runs-dev "
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("LoadAPIConfig returned error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.TemporalAddress != "localhost:7233" {
		t.Fatalf("TemporalAddress = %q", cfg.TemporalAddress)
	}
	if cfg.TemporalNamespace != "megagega" {
		t.Fatalf("TemporalNamespace = %q", cfg.TemporalNamespace)
	}
	if cfg.TemporalTaskQueue != "terraform-runs-dev" {
		t.Fatalf("TemporalTaskQueue = %q", cfg.TemporalTaskQueue)
	}
}

func TestLoadAPIConfigDefaultsTemporalTaskQueue(t *testing.T) {
	t.Parallel()

	cfg, err := LoadAPIConfig(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("LoadAPIConfig returned error: %v", err)
	}

	if cfg.TemporalTaskQueue != DefaultTemporalTaskQueue {
		t.Fatalf("TemporalTaskQueue = %q, want %q", cfg.TemporalTaskQueue, DefaultTemporalTaskQueue)
	}
	if cfg.TemporalNamespace != "" {
		t.Fatalf("TemporalNamespace = %q, want empty", cfg.TemporalNamespace)
	}
}

func TestLoadAPIConfigRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	_, err := LoadAPIConfig(func(key string) string {
		if key == "TEMPORAL_ADDRESS" {
			return "localhost:7233"
		}
		return ""
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestLoadAPIConfigRequiresTemporalAddress(t *testing.T) {
	t.Parallel()

	_, err := LoadAPIConfig(func(key string) string {
		if key == "DATABASE_URL" {
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		}
		return ""
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestLoadWorkerConfigReadsWorkerSettings(t *testing.T) {
	t.Parallel()

	cfg, err := LoadWorkerConfig(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return " postgres://user:pass@localhost:5432/db?sslmode=disable "
		case "TEMPORAL_ADDRESS":
			return " localhost:7233 "
		case "TEMPORAL_NAMESPACE":
			return " megagega "
		case "TEMPORAL_TASK_QUEUE":
			return " terraform-runs-dev "
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("LoadWorkerConfig returned error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.TemporalAddress != "localhost:7233" {
		t.Fatalf("TemporalAddress = %q", cfg.TemporalAddress)
	}
	if cfg.TemporalNamespace != "megagega" {
		t.Fatalf("TemporalNamespace = %q", cfg.TemporalNamespace)
	}
	if cfg.TemporalTaskQueue != "terraform-runs-dev" {
		t.Fatalf("TemporalTaskQueue = %q, want terraform-runs-dev", cfg.TemporalTaskQueue)
	}
}

func TestLoadWorkerConfigDefaultsTemporalTaskQueue(t *testing.T) {
	t.Parallel()

	cfg, err := LoadWorkerConfig(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("LoadWorkerConfig returned error: %v", err)
	}

	if cfg.TemporalTaskQueue != DefaultTemporalTaskQueue {
		t.Fatalf("TemporalTaskQueue = %q, want %q", cfg.TemporalTaskQueue, DefaultTemporalTaskQueue)
	}
	if cfg.TemporalNamespace != "" {
		t.Fatalf("TemporalNamespace = %q, want empty", cfg.TemporalNamespace)
	}
}

func TestLoadWorkerConfigRequiresTemporalAddress(t *testing.T) {
	t.Parallel()

	_, err := LoadWorkerConfig(func(string) string {
		return ""
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestLoadWorkerConfigRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	_, err := LoadWorkerConfig(func(key string) string {
		if key == "TEMPORAL_ADDRESS" {
			return "localhost:7233"
		}
		return ""
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}
