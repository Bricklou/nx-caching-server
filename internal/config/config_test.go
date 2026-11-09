package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{"STORAGE_DIR", "CLEANUP_THRESHOLD", "PORT", "AUTH_TOKEN", "AUTH_TOKENS"} {
		t.Setenv(key, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StorageDir != os.TempDir() || cfg.CleanupThreshold != time.Hour || cfg.Port != "8090" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("STORAGE_DIR", "/data")
	t.Setenv("CLEANUP_THRESHOLD", "30m")
	t.Setenv("PORT", "9000")
	t.Setenv("AUTH_TOKEN", "tok")
	t.Setenv("AUTH_TOKENS", "a:b")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{StorageDir: "/data", CleanupThreshold: 30 * time.Minute, Port: "9000", AuthToken: "tok", AuthTokens: "a:b"}
	if cfg != want {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadInvalidThreshold(t *testing.T) {
	t.Setenv("CLEANUP_THRESHOLD", "soon")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error")
	}
}
