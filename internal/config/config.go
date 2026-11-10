package config

type Config struct {
	// AuthConfig is nil when AUTH_CONFIG is unset, meaning authentication is off.
	AuthConfig *AuthConfig
}

func Load() (Config, error) {
	authConfig, err := loadAuthConfig()
	if err != nil {
		return Config{}, err
	}

	return Config{
		AuthConfig: authConfig,
	}, nil
}
