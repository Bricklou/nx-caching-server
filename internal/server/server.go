// Package server exposes the cache over HTTP
package server

import (
	"net/http"
	"nx-caching-server/internal/server/health"
)

func New() *http.ServeMux {
	mux := http.NewServeMux()

	health.Register(mux)

	return mux
}
