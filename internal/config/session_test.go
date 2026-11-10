// Tests the session settings: defaults, parsing and refusal of weak secrets.
package config

import (
	"strings"
	"testing"
	"time"
)

func TestSessionDefaults(t *testing.T) {
	t.Setenv(sessionTTLKey, "")
	t.Setenv(sessionSecretKey, "")

	got, err := loadSession()

	if err != nil || got.TTL != DefaultSessionTTL || len(got.Secret) != 0 {
		t.Errorf("got (%+v, %v)", got, err)
	}
}

func TestSessionFromEnvironment(t *testing.T) {
	secret := strings.Repeat("s", 40)
	t.Setenv(sessionTTLKey, "90m")
	t.Setenv(sessionSecretKey, secret)

	got, err := loadSession()

	if err != nil || got.TTL != 90*time.Minute || string(got.Secret) != secret {
		t.Errorf("got (%+v, %v)", got, err)
	}
}

func TestSessionErrors(t *testing.T) {
	tests := []struct{ name, ttl, secret, want string }{
		{"weak secret", "", "short", sessionSecretKey},
		{"bad duration", "soon", "", sessionTTLKey},
		{"zero duration", "0s", "", sessionTTLKey},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(sessionTTLKey, tt.ttl)
			t.Setenv(sessionSecretKey, tt.secret)

			if _, err := loadSession(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want one naming %s", err, tt.want)
			}
		})
	}
}
