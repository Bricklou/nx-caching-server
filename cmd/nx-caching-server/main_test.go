package main

import (
	"os"
	"testing"
)

func TestGetEnv(t *testing.T) {
	t.Run("should return the value of the environment variable", func(t *testing.T) {
		os.Setenv("TEST_ENV", "test")
		if GetEnv("TEST_ENV", "default") != "test" {
			t.Errorf("GetEnv returned %s, expected %s", GetEnv("TEST_ENV", "default"), "test")
		}
	})

	t.Run("should fall back to the default value", func(t *testing.T) {
		os.Unsetenv("TEST_ENV_UNSET")
		if got := GetEnv("TEST_ENV_UNSET", "default"); got != "default" {
			t.Errorf("GetEnv returned %q, expected %q", got, "default")
		}
	})
}
