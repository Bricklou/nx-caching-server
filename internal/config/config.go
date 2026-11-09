// Package config loads the server settings from environment variables.
package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	StorageDir       string
	CleanupThreshold time.Duration
	Port             string
	AuthToken        string
	AuthTokens       string
}

func Load() (Config, error) {
	threshold, err := time.ParseDuration(getEnv("CLEANUP_THRESHOLD", "1h"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid cleanup threshold: %w", err)
	}

	return Config{
		StorageDir:       getEnv("STORAGE_DIR", os.TempDir()),
		CleanupThreshold: threshold,
		Port:             getEnv("PORT", "8090"),
		AuthToken:        os.Getenv("AUTH_TOKEN"),
		AuthTokens:       os.Getenv("AUTH_TOKENS"),
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
