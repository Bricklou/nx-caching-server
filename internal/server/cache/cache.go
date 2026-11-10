// HTTP routes of the remote cache: GET and PUT of one entry by hash, as the Nx remote cache specification defines them.
// Entries live in the scopes the caller's identity may read or write.
package cache

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"nx-caching-server/internal/store"
)

// Handler serves the cache entries held by a store.
type Handler struct {
	store store.Store
}

// New returns a handler reading and writing entries through s.
func New(s store.Store) *Handler {
	return &Handler{store: s}
}

// Routes returns the routes relative to the cache prefix, so the server picks
// where to mount them.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /{hash}", h.upload)
	mux.HandleFunc("GET /{hash}", h.download)
	// GET also matches HEAD, and a pattern with a method wins over one without,
	// so HEAD needs its own route; the pattern without a method takes every other one.
	mux.HandleFunc("HEAD /{hash}", methodNotAllowed)
	mux.HandleFunc("/{hash}", methodNotAllowed)
	return mux
}

// UnauthenticatedStatus answers a request without a valid token: the Nx
// specification lists 401 for uploads and 403 for downloads.
func UnauthenticatedStatus(r *http.Request) int {
	if r.Method == http.MethodGet {
		return http.StatusForbidden
	}
	return http.StatusUnauthorized
}

func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "GET, PUT")
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

// request validates the hash and resolves the directories the caller may use.
// It answers the request itself and returns false when it cannot continue.
func request(w http.ResponseWriter, r *http.Request) (hash string, acc access, ok bool) {
	hash = r.PathValue("hash")
	if !validHash.MatchString(hash) {
		http.Error(w, "Invalid hash", http.StatusBadRequest)
		return "", access{}, false
	}

	acc, err := accessFor(r)
	if err != nil {
		slog.Error("invalid cache access", slog.Any("error", err))
		http.Error(w, "Failed to resolve the cache location", http.StatusInternalServerError)
		return "", access{}, false
	}
	return hash, acc, true
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	hash, acc, ok := request(w, r)
	if !ok {
		return
	}

	for _, prefix := range acc.read {
		obj, err := h.store.Get(r.Context(), entryKey(prefix, hash))
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			slog.Error("failed to read an entry", slog.Any("error", err))
			http.Error(w, "Failed to read the file", http.StatusInternalServerError)
			return
		}
		defer obj.Close()

		w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
		w.Header().Set("Content-Type", "application/octet-stream")
		io.Copy(w, obj)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	hash, acc, ok := request(w, r)
	if !ok {
		return
	}
	if !acc.canWrite() {
		http.Error(w, "This token can read the cache but not write to it", http.StatusForbidden)
		return
	}

	contentLengthString := r.Header.Get("Content-Length")
	if contentLengthString == "" {
		http.Error(w, "Content-Length header is required", http.StatusBadRequest)
		return
	}
	contentLength, err := strconv.ParseInt(contentLengthString, 10, 64)
	if err != nil || contentLength < 0 {
		http.Error(w, "Invalid Content-Length header", http.StatusBadRequest)
		return
	}

	// The first writer of a hash wins, within the scope the caller writes.
	err = h.store.Put(r.Context(), entryKey(*acc.write, hash), r.Body, contentLength)
	switch {
	case errors.Is(err, store.ErrExists):
		w.WriteHeader(http.StatusConflict)
	case err != nil:
		slog.Error("failed to write an entry", slog.Any("error", err))
		http.Error(w, "Failed to write file", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusOK)
	}
}
