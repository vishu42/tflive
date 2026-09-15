package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/vishu42/tflive/internal/encryption"
	"github.com/vishu42/tflive/internal/githubapp"
)

const (
	DefaultHTTPAddress                 = ":8081"
	DefaultWorkerRunRoot               = "/tmp/tflive/runs"
	DefaultArtifactStoreFilesystemRoot = "/tmp/tflive/artifacts"
)

var ErrInvalidConfig = errors.New("invalid config")

type APIConfig struct {
	DatabaseURL             string
	HTTPAddress             string
	TemporalAddress         string
	TemporalNamespace       string
	WorkerRunRoot           string
	ArtifactStore           ArtifactStoreConfig
	Security                SecurityConfig
	CredentialEncryptionKey Secret
	// GitHubApp mints installation tokens for template sync, which runs on the
	// control plane.
	GitHubApp GitHubAppConfig
	Debug     bool
}

type WorkerConfig struct {
	DatabaseURL             string
	TemporalAddress         string
	TemporalNamespace       string
	WorkerRunRoot           string
	ArtifactStore           ArtifactStoreConfig
	OpenFGA                 OpenFGAConfig
	CredentialEncryptionKey Secret
	GitHubApp               GitHubAppConfig
}

type ArtifactStoreKind string

const (
	ArtifactStoreFilesystem ArtifactStoreKind = "filesystem"
	ArtifactStoreS3         ArtifactStoreKind = "s3"
)

type ArtifactStoreConfig struct {
	Kind           ArtifactStoreKind
	FilesystemRoot string
	S3             S3Config
}

type S3Config struct {
	Bucket          string
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
}

func LoadAPIConfig(getenv func(string) string) (APIConfig, error) {
	security, err := loadSecurityConfig(getenv)
	if err != nil {
		return APIConfig{}, err
	}
	artifactStore, err := loadArtifactStoreConfig(getenv)
	if err != nil {
		return APIConfig{}, err
	}

	cfg := APIConfig{
		DatabaseURL:       strings.TrimSpace(getenv("DATABASE_URL")),
		HTTPAddress:       strings.TrimSpace(getenv("HTTP_ADDRESS")),
		TemporalAddress:   strings.TrimSpace(getenv("TEMPORAL_ADDRESS")),
		TemporalNamespace: strings.TrimSpace(getenv("TEMPORAL_NAMESPACE")),
		WorkerRunRoot:     strings.TrimSpace(getenv("WORKER_RUN_ROOT")),
		ArtifactStore:     artifactStore,
		Security:          security,
	}
	if cfg.HTTPAddress == "" {
		cfg.HTTPAddress = DefaultHTTPAddress
	}
	if cfg.WorkerRunRoot == "" {
		cfg.WorkerRunRoot = DefaultWorkerRunRoot
	}

	cfg.Debug = parseBool(getenv("TFLIVE_DEBUG"))
	credentialKey, err := loadCredentialEncryptionKey(getenv)
	if err != nil {
		return APIConfig{}, err
	}
	cfg.CredentialEncryptionKey = credentialKey

	gitHubApp, err := loadGitHubAppConfig(getenv)
	if err != nil {
		return APIConfig{}, err
	}
	cfg.GitHubApp = gitHubApp

	if cfg.DatabaseURL == "" {
		return APIConfig{}, fmt.Errorf("%w: DATABASE_URL is required", ErrInvalidConfig)
	}
	if cfg.TemporalAddress == "" {
		return APIConfig{}, fmt.Errorf("%w: TEMPORAL_ADDRESS is required", ErrInvalidConfig)
	}

	return cfg, nil
}

func LoadWorkerConfig(getenv func(string) string) (WorkerConfig, error) {
	openFGA, err := loadOpenFGAConfig(getenv)
	if err != nil {
		return WorkerConfig{}, err
	}
	artifactStore, err := loadArtifactStoreConfig(getenv)
	if err != nil {
		return WorkerConfig{}, err
	}

	cfg := WorkerConfig{
		DatabaseURL:       strings.TrimSpace(getenv("DATABASE_URL")),
		TemporalAddress:   strings.TrimSpace(getenv("TEMPORAL_ADDRESS")),
		TemporalNamespace: strings.TrimSpace(getenv("TEMPORAL_NAMESPACE")),
		WorkerRunRoot:     strings.TrimSpace(getenv("WORKER_RUN_ROOT")),
		ArtifactStore:     artifactStore,
		OpenFGA:           openFGA,
	}
	if cfg.WorkerRunRoot == "" {
		cfg.WorkerRunRoot = DefaultWorkerRunRoot
	}
	credentialKey, err := loadCredentialEncryptionKey(getenv)
	if err != nil {
		return WorkerConfig{}, err
	}
	cfg.CredentialEncryptionKey = credentialKey

	gitHubApp, err := loadGitHubAppConfig(getenv)
	if err != nil {
		return WorkerConfig{}, err
	}
	cfg.GitHubApp = gitHubApp

	if cfg.TemporalAddress == "" {
		return WorkerConfig{}, fmt.Errorf("%w: TEMPORAL_ADDRESS is required", ErrInvalidConfig)
	}
	if cfg.DatabaseURL == "" {
		return WorkerConfig{}, fmt.Errorf("%w: DATABASE_URL is required", ErrInvalidConfig)
	}

	return cfg, nil
}

// loadCredentialEncryptionKey validates CREDENTIAL_ENCRYPTION_KEY the same
// way SESSION_ENCRYPTION_KEY is validated: by constructing the cipher it
// backs. Unlike SESSION_ENCRYPTION_KEY, it is optional — an empty value means
// credential encryption is unavailable, not misconfigured — but a value that
// is present and malformed must fail at startup rather than at first use.
func loadCredentialEncryptionKey(getenv func(string) string) (Secret, error) {
	key := newSecret(strings.TrimSpace(getenv("CREDENTIAL_ENCRYPTION_KEY")))
	if key.Empty() {
		return key, nil
	}
	if _, err := encryption.NewCipher(key.Value()); err != nil {
		return Secret{}, fmt.Errorf("%w: CREDENTIAL_ENCRYPTION_KEY must be a 32-byte raw, base64, or hex key", ErrInvalidConfig)
	}
	return key, nil
}

// GitHubAppConfig identifies the GitHub App whose installations grant access to
// private template repositories.
//
// It lives on the worker alone: the API process never clones, so it has no use
// for a signing key and should not hold one.
type GitHubAppConfig struct {
	AppID      string
	PrivateKey Secret
}

// Enabled reports whether a GitHub App is configured. When it is not, source
// fetches stay unauthenticated and only public repositories are reachable.
func (cfg GitHubAppConfig) Enabled() bool {
	return cfg.AppID != "" && !cfg.PrivateKey.Empty()
}

// loadGitHubAppConfig validates GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY the
// same way loadCredentialEncryptionKey validates its key: by constructing the
// thing it backs, so a malformed value fails at startup rather than at the
// first clone.
//
// Both absent means the App is not configured, which is a supported deployment
// and not an error. Exactly one present is always a mistake -- and one that
// would otherwise surface much later as an unexplained authentication failure.
func loadGitHubAppConfig(getenv func(string) string) (GitHubAppConfig, error) {
	appID := strings.TrimSpace(getenv("GITHUB_APP_ID"))
	privateKey := newSecret(strings.TrimSpace(getenv("GITHUB_APP_PRIVATE_KEY")))

	if appID == "" && privateKey.Empty() {
		return GitHubAppConfig{}, nil
	}
	if appID == "" {
		return GitHubAppConfig{}, fmt.Errorf("%w: GITHUB_APP_ID is required when GITHUB_APP_PRIVATE_KEY is set", ErrInvalidConfig)
	}
	if privateKey.Empty() {
		return GitHubAppConfig{}, fmt.Errorf("%w: GITHUB_APP_PRIVATE_KEY is required when GITHUB_APP_ID is set", ErrInvalidConfig)
	}
	if _, err := strconv.ParseInt(appID, 10, 64); err != nil {
		return GitHubAppConfig{}, fmt.Errorf("%w: GITHUB_APP_ID must be the App's numeric id", ErrInvalidConfig)
	}
	if _, err := githubapp.ParsePrivateKey(privateKey.Value()); err != nil {
		return GitHubAppConfig{}, fmt.Errorf("%w: GITHUB_APP_PRIVATE_KEY must be an RSA private key as PEM or base64-encoded PEM", ErrInvalidConfig)
	}
	return GitHubAppConfig{AppID: appID, PrivateKey: privateKey}, nil
}

func loadArtifactStoreConfig(getenv func(string) string) (ArtifactStoreConfig, error) {
	kind := ArtifactStoreKind(strings.TrimSpace(getenv("ARTIFACT_STORE_KIND")))
	if kind == "" {
		kind = ArtifactStoreFilesystem
	}

	cfg := ArtifactStoreConfig{
		Kind:           kind,
		FilesystemRoot: strings.TrimSpace(getenv("ARTIFACT_STORE_FILESYSTEM_ROOT")),
		S3: S3Config{
			Bucket:          strings.TrimSpace(getenv("S3_BUCKET")),
			Region:          strings.TrimSpace(getenv("S3_REGION")),
			Endpoint:        strings.TrimSpace(getenv("S3_ENDPOINT")),
			AccessKeyID:     strings.TrimSpace(getenv("S3_ACCESS_KEY_ID")),
			SecretAccessKey: strings.TrimSpace(getenv("S3_SECRET_ACCESS_KEY")),
			ForcePathStyle:  parseBool(getenv("S3_FORCE_PATH_STYLE")),
		},
	}
	if cfg.FilesystemRoot == "" {
		cfg.FilesystemRoot = DefaultArtifactStoreFilesystemRoot
	}

	switch cfg.Kind {
	case ArtifactStoreFilesystem:
		return cfg, nil
	case ArtifactStoreS3:
		if cfg.S3.Bucket == "" {
			return ArtifactStoreConfig{}, fmt.Errorf("%w: S3_BUCKET is required", ErrInvalidConfig)
		}
		if cfg.S3.Region == "" {
			return ArtifactStoreConfig{}, fmt.Errorf("%w: S3_REGION is required", ErrInvalidConfig)
		}
		if cfg.S3.AccessKeyID == "" {
			return ArtifactStoreConfig{}, fmt.Errorf("%w: S3_ACCESS_KEY_ID is required", ErrInvalidConfig)
		}
		if cfg.S3.SecretAccessKey == "" {
			return ArtifactStoreConfig{}, fmt.Errorf("%w: S3_SECRET_ACCESS_KEY is required", ErrInvalidConfig)
		}
		return cfg, nil
	default:
		return ArtifactStoreConfig{}, fmt.Errorf("%w: ARTIFACT_STORE_KIND must be filesystem or s3", ErrInvalidConfig)
	}
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
