package config

type Config struct {
	// AuthConfig is nil when AUTH_CONFIG is unset, meaning authentication is off.
	AuthConfig *AuthConfig
	// Retention says how long unused cache entries are kept.
	Retention Retention
	// Storage says where cache entries are kept.
	Storage Storage
	// Server configures the HTTP listener.
	Server Server
	// Session configures the tokens exchanged for forge tokens.
	Session Session
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

	storage, err := loadStorage()
	if err != nil {
		return Config{}, err
	}

	server, err := loadServer()
	if err != nil {
		return Config{}, err
	}

	session, err := loadSession()
	if err != nil {
		return Config{}, err
	}

	return Config{
		AuthConfig: authConfig,
		Retention:  retention,
		Storage:    storage,
		Server:     server,
		Session:    session,
	}, nil
}
