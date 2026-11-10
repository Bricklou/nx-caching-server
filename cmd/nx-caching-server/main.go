// Nx Caching Server
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/config"
	"nx-caching-server/internal/core"
	"nx-caching-server/internal/server"
	"nx-caching-server/internal/server/cache"
)

func GetEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

const (
	storageDirKey       = "STORAGE_DIR"
	cleanupThresholdKey = "CLEANUP_THRESHOLD"
	portKey             = "PORT"
)

// logConfigError logs each problem of a config file on its own line.
func logConfigError(err error) {
	var fileErr *config.FileError
	if !errors.As(err, &fileErr) {
		slog.Error("invalid configuration", slog.Any("error", err))
		return
	}

	for _, problem := range fileErr.Problems {
		slog.Error("invalid auth config",
			slog.String("file", fileErr.Path),
			slog.String("problem", problem.Error()),
		)
	}
}

// newVerifier returns nil, which disables authentication, when no auth config is set.
func newVerifier(cfg config.Config) *auth.Verifier {
	if cfg.AuthConfig == nil {
		slog.Warn("authentication is disabled: every client can read and write the cache")
		return nil
	}
	return auth.NewVerifier(cfg.AuthConfig)
}

func main() {
	core.SetupLogger()

	cfg, err := config.Load()
	if err != nil {
		logConfigError(err)
		os.Exit(1)
	}
	slog.Debug("auth config", slog.Any("config", cfg.AuthConfig))

	cleanupThreshold, err := time.ParseDuration(GetEnv(cleanupThresholdKey, "1h"))
	if err != nil {
		slog.Error("invalid cleanup threshold", slog.Any("error", err))
		os.Exit(1)
	}
	storageDir := GetEnv(storageDirKey, os.TempDir())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go cache.RunCleanup(ctx, storageDir, cleanupThreshold)

	srv := &http.Server{
		Addr: ":" + GetEnv(portKey, "8090"),
		Handler: server.New(server.Options{
			Cache:    cache.New(storageDir),
			Verifier: newVerifier(cfg),
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", slog.String("addr", srv.Addr), slog.String("storage", storageDir))
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server error", slog.Any("error", err))
		os.Exit(1)
	}
}
