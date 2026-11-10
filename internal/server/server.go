// Package server exposes the cache over HTTP: it mounts the routes under their prefixes and applies the middlewares.
package server

import (
	"net/http"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/server/cache"
	"nx-caching-server/internal/server/health"
)

// Options holds what the server is built from.
type Options struct {
	Cache *cache.Handler
	// Verifier authenticates the cache routes. Nil disables authentication.
	Verifier *auth.Verifier
}

// New builds the HTTP handler. /health is public, the cache routes sit behind
// the authentication, and every route is covered by recovery and request logging.
func New(opts Options) http.Handler {
	root := http.NewServeMux()

	health.Register(root)

	// Authentication wraps the prefix handling so it logs the full request path.
	cacheRoutes := http.StripPrefix("/v1/cache", opts.Cache.Routes())
	if opts.Verifier != nil {
		cacheRoutes = auth.Middleware(
			opts.Verifier,
			auth.WithUnauthenticatedStatus(cache.UnauthenticatedStatus),
		)(cacheRoutes)
	}
	root.Handle("/v1/cache/", cacheRoutes)

	return chain(root, recovery, requestLog)
}
