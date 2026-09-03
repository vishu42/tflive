package artifacts

import (
	"fmt"

	"github.com/vishu42/tflive/internal/config"
)

func NewObjectStore(cfg config.ArtifactStoreConfig) (ObjectStore, error) {
	switch cfg.Kind {
	case config.ArtifactStoreFilesystem:
		return NewFilesystemStore(cfg.FilesystemRoot), nil
	case config.ArtifactStoreS3:
		return NewS3Store(cfg.S3)
	default:
		return nil, fmt.Errorf("unsupported artifact store kind %q", cfg.Kind)
	}
}
