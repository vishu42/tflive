package config

import (
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidConfig = errors.New("invalid config")

type APIConfig struct {
	DatabaseURL string
}

func LoadAPIConfig(getenv func(string) string) (APIConfig, error) {
	cfg := APIConfig{
		DatabaseURL: strings.TrimSpace(getenv("DATABASE_URL")),
	}
	if cfg.DatabaseURL == "" {
		return APIConfig{}, fmt.Errorf("%w: DATABASE_URL is required", ErrInvalidConfig)
	}

	return cfg, nil
}
