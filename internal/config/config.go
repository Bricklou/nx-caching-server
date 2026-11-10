package config

import (
	"fmt"
	"os"
)

type Config struct {
	// AuthConfig is nil when AUTH_CONFIG is unset, meaning authentication is off.
	AuthConfig *AuthConfig
}

// legacyAuthKeys are the removed token variables. They are refused rather than
// ignored, so a deployment that still sets one cannot run unauthenticated by mistake.
var legacyAuthKeys = []string{"AUTH_TOKEN", "AUTH_TOKENS"}

func Load() (Config, error) {
	if err := rejectLegacyAuth(); err != nil {
		return Config{}, err
	}

	authConfig, err := loadAuthConfig()
	if err != nil {
		return Config{}, err
	}

	return Config{
		AuthConfig: authConfig,
	}, nil
}

func rejectLegacyAuth() error {
	for _, key := range legacyAuthKeys {
		if os.Getenv(key) != "" {
			return fmt.Errorf("%s is no longer supported: configure authentication in the file named by %s", key, authConfigKey)
		}
	}
	return nil
}
