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
	"nx-caching-server/internal/auth/oidc"
	"nx-caching-server/internal/auth/session"
	"nx-caching-server/internal/config"
	"nx-caching-server/internal/core"
	"nx-caching-server/internal/server"
	"nx-caching-server/internal/server/cache"
	"nx-caching-server/internal/store"
	"nx-caching-server/internal/store/fs"
	"nx-caching-server/internal/store/s3"
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

// newAuth builds the authentication: forge tokens and the session tokens
// exchanged for them. Both are nil, which disables authentication, when no auth
// config is set.
func newAuth(cfg config.Config) (auth.Authenticator, *session.Manager) {
	if cfg.AuthConfig == nil {
		slog.Warn("authentication is disabled: every client can read and write the cache")
		return nil, nil
	}
	if len(cfg.Session.Secret) == 0 {
		slog.Info("session tokens are signed with a random key: they stop working when the server restarts, and instances do not share them; set SESSION_SECRET to change that")
	}

	verifier := oidc.NewVerifier(cfg.AuthConfig)
	sessions := session.New(verifier, cfg.Session.TTL, cfg.Session.Secret)
	return auth.Chain(verifier, sessions), sessions
}

// newStore opens the backend that STORAGE_BACKEND selects.
func newStore(ctx context.Context, cfg config.Config) (store.Store, error) {
	if cfg.Storage.Backend == config.BackendS3 {
		// A read refreshes an entry at most four times per shortest retention.
		return s3.New(ctx, cfg.Storage.S3, s3.Options{TouchAfter: cfg.Retention.Shortest() / 4})
	}
	return fs.New(cfg.Storage.Dir), nil
}

func describeStorage(storage config.Storage) string {
	if storage.Backend == config.BackendS3 {
		return "s3://" + storage.S3.Bucket
	}
	return storage.Dir
}

func main() {
	core.SetupLogger()

	cfg, err := config.Load()
	if err != nil {
		logConfigError(err)
		os.Exit(1)
	}
	slog.Debug("auth config", slog.Any("config", cfg.AuthConfig))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	entries, err := newStore(ctx, cfg)
	if err != nil {
		slog.Error("cannot open the storage", slog.Any("error", err))
		os.Exit(1)
	}
	if sweeper, ok := entries.(store.Sweeper); ok {
		go cache.RunCleanup(ctx, sweeper, cfg.Retention)
	}

	authenticator, sessions := newAuth(cfg)
	srv := &http.Server{
		Addr: cfg.Server.Addr(),
		Handler: server.New(server.Options{
			Cache:         cache.New(entries),
			Authenticator: authenticator,
			Sessions:      sessions,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", slog.String("addr", srv.Addr), slog.String("storage", describeStorage(cfg.Storage)))
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server error", slog.Any("error", err))
		os.Exit(1)
	}
}
