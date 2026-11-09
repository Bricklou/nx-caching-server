// Nx Caching Server
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/config"
	"nx-caching-server/internal/server"
	"nx-caching-server/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	tokens, err := auth.ParseTokens(cfg.AuthToken, cfg.AuthTokens)
	if err != nil {
		log.Fatalf("Invalid auth configuration: %v", err)
	}

	store := storage.New(cfg.StorageDir)
	if namespaces := tokens.Namespaces(); len(namespaces) > 0 {
		if err := store.EnsureNamespaces(namespaces); err != nil {
			log.Fatalf("Failed to create namespace directories: %v", err)
		}
		log.Printf("Loaded namespaces: %s", strings.Join(namespaces, ", "))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go store.RunCleanup(ctx, cfg.CleanupThreshold)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           server.New(store, tokens),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Server error: %v", err)
	}
}
