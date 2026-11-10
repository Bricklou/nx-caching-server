package config

type Config struct {
	// AuthConfig is nil when AUTH_CONFIG is unset, meaning authentication is off.
	AuthConfig *AuthConfig
	// Retention says how long unused cache entries are kept.
	Retention Retention
}

func Load() (Config, error) {
	authConfig, err := loadAuthConfig()
	if err != nil {
		return Config{}, err
	}

	retention, err := loadRetention()
	if err != nil {
		return Config{}, err
	}

	return Config{
		AuthConfig: authConfig,
		Retention:  retention,
	}, nil
}
