package artifacts

import (
	"fmt"

	"github.com/vishu42/megagega/internal/config"
)

func NewObjectStore(cfg config.ArtifactStoreConfig) (ObjectStore, error) {
	switch cfg.Kind {
	case config.ArtifactStoreFilesystem:
		return NewFilesystemStore(cfg.FilesystemRoot), nil
	case config.ArtifactStoreS3:
		return NewS3Store(S3Config{
			Bucket:          cfg.S3.Bucket,
			Region:          cfg.S3.Region,
			Endpoint:        cfg.S3.Endpoint,
			AccessKeyID:     cfg.S3.AccessKeyID,
			SecretAccessKey: cfg.S3.SecretAccessKey,
			ForcePathStyle:  cfg.S3.ForcePathStyle,
		})
	default:
		return nil, fmt.Errorf("unsupported artifact store kind %q", cfg.Kind)
	}
}
