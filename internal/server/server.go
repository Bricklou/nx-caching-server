// Package server exposes the cache over HTTP.
package server

import (
	"io"
	"net/http"

	"nx-caching-server/internal/auth"
)

// Storage is what the handlers need from the cache backend.
type Storage interface {
	Put(namespace, hash string, r io.Reader, size int64) error
	Open(namespace, hash string) (io.ReadCloser, int64, error)
	Exists(namespace, hash string) (bool, error)
}

// New returns the HTTP handler serving the cache API. Nil tokens disable authentication.
func New(store Storage, tokens auth.Tokens) http.Handler {
	h := &handlers{store: store}
	authed := auth.Middleware(tokens)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.health)
	mux.Handle("PUT /v1/cache/{hash}", authed(http.HandlerFunc(h.upload)))
	mux.Handle("HEAD /v1/cache/{hash}", authed(http.HandlerFunc(h.check)))
	mux.Handle("GET /v1/cache/{hash}", authed(http.HandlerFunc(h.download)))
	return mux
}
