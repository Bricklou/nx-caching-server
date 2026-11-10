// Session tokens: the longer-lived token a job gets in exchange for its forge token.
// SESSION_SECRET signs them; without it the server draws a key at startup.
package config

import (
	"fmt"
	"os"
	"time"
)

const (
	sessionTTLKey    = "SESSION_TTL"
	sessionSecretKey = "SESSION_SECRET"

	// DefaultSessionTTL is how long a session token lives.
	DefaultSessionTTL = 2 * time.Hour

	// minSessionSecretLength keeps the signing key out of guessing range.
	minSessionSecretLength = 32
)

// Session configures session tokens.
type Session struct {
	TTL time.Duration
	// Secret signs the tokens. Empty means the server draws a random key, which
	// is lost on restart and not shared between instances.
	Secret []byte
}

func loadSession() (Session, error) {
	ttl, err := durationFromEnv(sessionTTLKey, DefaultSessionTTL)
	if err != nil {
		return Session{}, err
	}

	secret := os.Getenv(sessionSecretKey)
	if secret != "" && len(secret) < minSessionSecretLength {
		return Session{}, fmt.Errorf("%s: must hold at least %d characters, for example `openssl rand -hex 32`", sessionSecretKey, minSessionSecretLength)
	}
	return Session{TTL: ttl, Secret: []byte(secret)}, nil
}
