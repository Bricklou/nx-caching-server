// Tests the top-level configuration loading.
// Without AUTH_CONFIG authentication is off.
package config

import "testing"

func TestLoadWithoutAuthConfig(t *testing.T) {
	t.Setenv(authConfigKey, "")

	cfg, err := Load()

	if err != nil || cfg.AuthConfig != nil {
		t.Errorf("got (%+v, %v), want authentication disabled", cfg, err)
	}
}
