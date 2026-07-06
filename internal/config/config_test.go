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
		case "HTTP_ADDRESS":
			return " :9090 "
		case "TEMPORAL_ADDRESS":
			return " localhost:7233 "
		case "TEMPORAL_NAMESPACE":
			return " megagega "
		case "TEMPORAL_TASK_QUEUE":
			return " terraform-runs-dev "
		case "WORKER_RUN_ROOT":
			return " /var/lib/megagega/runs "
		case "ARTIFACT_STORE_KIND":
			return " s3 "
		case "ARTIFACT_STORE_FILESYSTEM_ROOT":
			return " /var/lib/megagega/artifacts "
		case "S3_BUCKET":
			return " megagega-artifacts "
		case "S3_REGION":
			return " us-east-1 "
		case "S3_ENDPOINT":
			return " https://s3.us-east-1.amazonaws.com "
		case "S3_ACCESS_KEY_ID":
			return " access-key "
		case "S3_SECRET_ACCESS_KEY":
			return " secret-key "
		case "S3_FORCE_PATH_STYLE":
			return " true "
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
	if cfg.HTTPAddress != ":9090" {
		t.Fatalf("HTTPAddress = %q", cfg.HTTPAddress)
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
	if cfg.WorkerRunRoot != "/var/lib/megagega/runs" {
		t.Fatalf("WorkerRunRoot = %q, want /var/lib/megagega/runs", cfg.WorkerRunRoot)
	}
	assertArtifactStoreConfig(t, cfg.ArtifactStore)
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
	if cfg.HTTPAddress != DefaultHTTPAddress {
		t.Fatalf("HTTPAddress = %q, want %q", cfg.HTTPAddress, DefaultHTTPAddress)
	}
	if cfg.TemporalNamespace != "" {
		t.Fatalf("TemporalNamespace = %q, want empty", cfg.TemporalNamespace)
	}
	if cfg.WorkerRunRoot != DefaultWorkerRunRoot {
		t.Fatalf("WorkerRunRoot = %q, want %q", cfg.WorkerRunRoot, DefaultWorkerRunRoot)
	}
	if cfg.ArtifactStore.Kind != ArtifactStoreFilesystem {
		t.Fatalf("ArtifactStore.Kind = %q, want %q", cfg.ArtifactStore.Kind, ArtifactStoreFilesystem)
	}
	if cfg.ArtifactStore.FilesystemRoot != DefaultArtifactStoreFilesystemRoot {
		t.Fatalf("ArtifactStore.FilesystemRoot = %q, want %q", cfg.ArtifactStore.FilesystemRoot, DefaultArtifactStoreFilesystemRoot)
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
		case "WORKER_RUN_ROOT":
			return " /var/lib/megagega/runs "
		case "ARTIFACT_STORE_KIND":
			return " s3 "
		case "ARTIFACT_STORE_FILESYSTEM_ROOT":
			return " /var/lib/megagega/artifacts "
		case "S3_BUCKET":
			return " megagega-artifacts "
		case "S3_REGION":
			return " us-east-1 "
		case "S3_ENDPOINT":
			return " https://s3.us-east-1.amazonaws.com "
		case "S3_ACCESS_KEY_ID":
			return " access-key "
		case "S3_SECRET_ACCESS_KEY":
			return " secret-key "
		case "S3_FORCE_PATH_STYLE":
			return " true "
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
	if cfg.WorkerRunRoot != "/var/lib/megagega/runs" {
		t.Fatalf("WorkerRunRoot = %q, want /var/lib/megagega/runs", cfg.WorkerRunRoot)
	}
	assertArtifactStoreConfig(t, cfg.ArtifactStore)
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
	if cfg.WorkerRunRoot != DefaultWorkerRunRoot {
		t.Fatalf("WorkerRunRoot = %q, want %q", cfg.WorkerRunRoot, DefaultWorkerRunRoot)
	}
	if cfg.ArtifactStore.Kind != ArtifactStoreFilesystem {
		t.Fatalf("ArtifactStore.Kind = %q, want %q", cfg.ArtifactStore.Kind, ArtifactStoreFilesystem)
	}
	if cfg.ArtifactStore.FilesystemRoot != DefaultArtifactStoreFilesystemRoot {
		t.Fatalf("ArtifactStore.FilesystemRoot = %q, want %q", cfg.ArtifactStore.FilesystemRoot, DefaultArtifactStoreFilesystemRoot)
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

func TestLoadAPIConfigRejectsInvalidArtifactStoreKind(t *testing.T) {
	t.Parallel()

	_, err := LoadAPIConfig(func(key string) string {
		switch key {
		case "DATABASE_URL":
			return "postgres://user:pass@localhost:5432/db?sslmode=disable"
		case "TEMPORAL_ADDRESS":
			return "localhost:7233"
		case "ARTIFACT_STORE_KIND":
			return "tape-drive"
		default:
			return ""
		}
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func assertArtifactStoreConfig(t *testing.T, cfg ArtifactStoreConfig) {
	t.Helper()

	if cfg.Kind != ArtifactStoreS3 {
		t.Fatalf("ArtifactStore.Kind = %q, want %q", cfg.Kind, ArtifactStoreS3)
	}
	if cfg.FilesystemRoot != "/var/lib/megagega/artifacts" {
		t.Fatalf("ArtifactStore.FilesystemRoot = %q, want /var/lib/megagega/artifacts", cfg.FilesystemRoot)
	}
	if cfg.S3.Bucket != "megagega-artifacts" {
		t.Fatalf("S3.Bucket = %q, want megagega-artifacts", cfg.S3.Bucket)
	}
	if cfg.S3.Region != "us-east-1" {
		t.Fatalf("S3.Region = %q, want us-east-1", cfg.S3.Region)
	}
	if cfg.S3.Endpoint != "https://s3.us-east-1.amazonaws.com" {
		t.Fatalf("S3.Endpoint = %q", cfg.S3.Endpoint)
	}
	if cfg.S3.AccessKeyID != "access-key" {
		t.Fatalf("S3.AccessKeyID = %q, want access-key", cfg.S3.AccessKeyID)
	}
	if cfg.S3.SecretAccessKey != "secret-key" {
		t.Fatalf("S3.SecretAccessKey = %q, want secret-key", cfg.S3.SecretAccessKey)
	}
	if !cfg.S3.ForcePathStyle {
		t.Fatal("S3.ForcePathStyle = false, want true")
	}
}
