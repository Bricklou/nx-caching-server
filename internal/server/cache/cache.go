// HTTP routes of the remote cache: GET and PUT of one entry by hash, as the Nx remote cache specification defines them.
// Entries live in the scopes the caller's identity may read or write.
package cache

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Handler serves the cache entries stored under one directory.
type Handler struct {
	dir string
}

// New returns a handler storing its entries under dir.
func New(dir string) *Handler {
	return &Handler{dir: dir}
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
func (h *Handler) request(w http.ResponseWriter, r *http.Request) (hash string, acc access, ok bool) {
	hash = r.PathValue("hash")
	if !validHash.MatchString(hash) {
		http.Error(w, "Invalid hash", http.StatusBadRequest)
		return "", access{}, false
	}

	acc, err := h.accessFor(r)
	if err != nil {
		slog.Error("invalid cache access", slog.Any("error", err))
		http.Error(w, "Failed to resolve the cache location", http.StatusInternalServerError)
		return "", access{}, false
	}
	return hash, acc, true
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	hash, acc, ok := h.request(w, r)
	if !ok {
		return
	}

	for _, dir := range acc.read {
		path := entryPath(dir, hash)
		file, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			http.Error(w, "Failed to read the file", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		stat, err := file.Stat()
		if err != nil {
			http.Error(w, "Failed to read the file", http.StatusInternalServerError)
			return
		}

		// Mark the entry as recently used so cleanup keeps it. Filesystem atime
		// is not reliable for this (noatime/relatime mounts, Windows).
		now := time.Now()
		if err := os.Chtimes(path, now, now); err != nil {
			slog.Warn("failed to update the access time", slog.String("path", path), slog.Any("error", err))
		}

		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
		w.Header().Set("Content-Type", "application/octet-stream")
		io.Copy(w, file)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	hash, acc, ok := h.request(w, r)
	if !ok {
		return
	}
	if acc.write == "" {
		http.Error(w, "This token can read the cache but not write to it", http.StatusForbidden)
		return
	}

	contentLengthString := r.Header.Get("Content-Length")
	if contentLengthString == "" {
		http.Error(w, "Content-Length header is required", http.StatusBadRequest)
		return
	}
	contentLength, err := strconv.ParseInt(contentLengthString, 10, 64)
	if err != nil {
		http.Error(w, "Invalid Content-Length header", http.StatusBadRequest)
		return
	}

	// The first writer of a hash wins, within the scope the caller writes.
	path := entryPath(acc.write, hash)
	if _, err := os.Stat(path); err == nil {
		w.WriteHeader(http.StatusConflict)
		return
	}

	// Write to a temp file and publish it atomically so that concurrent
	// downloads never observe a partially written entry.
	file, err := createTemp(acc.write, filepath.Base(path)+tempInfix+"*")
	if err != nil {
		http.Error(w, "Failed to create file", http.StatusInternalServerError)
		return
	}
	tmpPath := file.Name()
	defer os.Remove(tmpPath)

	_, err = io.CopyN(file, r.Body, contentLength)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmpPath, 0o644)
	}
	if err != nil {
		http.Error(w, "Failed to write file", http.StatusInternalServerError)
		return
	}

	if err := publishFile(tmpPath, path); err != nil {
		if os.IsExist(err) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		http.Error(w, "Failed to write file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
