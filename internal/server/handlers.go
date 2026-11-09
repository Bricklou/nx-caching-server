package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/storage"
)

type handlers struct {
	store Storage
}

func (h *handlers) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "OK")
}

func (h *handlers) upload(w http.ResponseWriter, req *http.Request) {
	contentLengthString := req.Header.Get("Content-Length")
	if contentLengthString == "" {
		http.Error(w, "Content-Length header is required", http.StatusBadRequest)
		return
	}

	contentLength, err := strconv.ParseInt(contentLengthString, 10, 64)
	if err != nil {
		http.Error(w, "Invalid Content-Length header", http.StatusBadRequest)
		return
	}

	err = h.store.Put(auth.NamespaceFrom(req), req.PathValue("hash"), req.Body, contentLength)
	if err != nil {
		writeStorageError(w, err, "Failed to write file")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *handlers) check(w http.ResponseWriter, req *http.Request) {
	exists, err := h.store.Exists(auth.NamespaceFrom(req), req.PathValue("hash"))
	if err != nil {
		writeStorageError(w, err, "Failed to check the file")
		return
	}
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *handlers) download(w http.ResponseWriter, req *http.Request) {
	file, size, err := h.store.Open(auth.NamespaceFrom(req), req.PathValue("hash"))
	if err != nil {
		writeStorageError(w, err, "Failed to read the file")
		return
	}
	defer file.Close()

	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Type", "application/octet-stream")

	io.Copy(w, file)
}

// writeStorageError maps storage sentinels to HTTP statuses; anything else is
// logged and reported as a 500 with internalMsg.
func writeStorageError(w http.ResponseWriter, err error, internalMsg string) {
	switch {
	case errors.Is(err, storage.ErrInvalidHash):
		http.Error(w, "Invalid hash", http.StatusBadRequest)
	case errors.Is(err, storage.ErrNotFound):
		w.WriteHeader(http.StatusNotFound)
	case errors.Is(err, storage.ErrExists):
		w.WriteHeader(http.StatusConflict)
	default:
		log.Printf("%s: %v", internalMsg, err)
		http.Error(w, internalMsg, http.StatusInternalServerError)
	}
}
