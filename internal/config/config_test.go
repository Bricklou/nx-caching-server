// Tests the top-level configuration loading.
// The removed token variables must stop the server instead of silently disabling authentication.
package config

import (
	"strings"
	"testing"
)

func TestLoadRefusesTheRemovedTokenVariables(t *testing.T) {
	for _, key := range legacyAuthKeys {
		t.Run(key, func(t *testing.T) {
			t.Setenv(authConfigKey, "")
			t.Setenv(key, "some-value")

			cfg, err := Load()

			if err == nil {
				t.Fatalf("Load accepted %s, config = %+v", key, cfg)
			}
			if !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), authConfigKey) {
				t.Errorf("error %q should name %s and point to %s", err, key, authConfigKey)
			}
		})
	}
}

func TestLoadWithoutAnyAuthSettings(t *testing.T) {
	t.Setenv(authConfigKey, "")
	for _, key := range legacyAuthKeys {
		t.Setenv(key, "")
	}

	cfg, err := Load()

	if err != nil || cfg.AuthConfig != nil {
		t.Errorf("got (%+v, %v), want authentication disabled", cfg, err)
	}
}
