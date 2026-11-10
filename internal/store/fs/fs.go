// Filesystem backend: an entry is the file <root>/<key>.cache, published atomically.
// Uploads go through a temp file next to the entry so readers never see a partial one.
package fs

import (
	"context"
	"errors"
	"io"
	iofs "io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nx-caching-server/internal/store"
)

const (
	suffix = ".cache"
	// tempInfix marks in-progress uploads (<hash>.cache.tmp-<random>), which are
	// never served and are removed by Housekeep once stale.
	tempInfix = ".tmp-"
)

// Store keeps its entries under one root directory.
type Store struct {
	root string
	// prune is held shared while an upload creates its directory and temp file,
	// and exclusively while empty directories are removed.
	prune sync.RWMutex
}

// New returns a store rooted at root.
func New(root string) *Store {
	return &Store{root: root}
}

var (
	_ store.Store       = (*Store)(nil)
	_ store.Sweeper     = (*Store)(nil)
	_ store.Housekeeper = (*Store)(nil)
)

// path maps a key to its file, refusing keys that could leave the root.
func (s *Store) path(key string) (string, error) {
	if err := store.ValidKey(key); err != nil {
		return "", err
	}
	return filepath.Join(s.root, filepath.FromSlash(key)+suffix), nil
}

func (s *Store) Get(_ context.Context, key string) (store.Object, error) {
	path, err := s.path(key)
	if err != nil {
		return store.Object{}, err
	}

	file, err := os.Open(path)
	if errors.Is(err, iofs.ErrNotExist) {
		return store.Object{}, store.ErrNotFound
	}
	if err != nil {
		return store.Object{}, err
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return store.Object{}, err
	}

	// Mark the entry as recently used so cleanup keeps it. Filesystem atime
	// is not reliable for this (noatime/relatime mounts, Windows).
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		slog.Warn("failed to update the access time", slog.String("path", path), slog.Any("error", err))
	}
	return store.Object{ReadCloser: file, Size: stat.Size()}, nil
}

func (s *Store) Put(_ context.Context, key string, body io.Reader, size int64) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return store.ErrExists
	}

	s.prune.RLock()
	file, err := createTemp(filepath.Dir(path), filepath.Base(path)+tempInfix+"*")
	s.prune.RUnlock()
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	defer os.Remove(tmpPath)

	_, err = io.CopyN(file, body, size)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmpPath, 0o644)
	}
	if err != nil {
		return err
	}

	if err := publishFile(tmpPath, path); err != nil {
		if os.IsExist(err) {
			return store.ErrExists
		}
		return err
	}
	return nil
}

// publishFile atomically makes tmpPath visible as finalPath without replacing an
// entry (os.IsExist), and falls back to rename without hard link support.
func publishFile(tmpPath, finalPath string) error {
	err := os.Link(tmpPath, finalPath)
	if err == nil || os.IsExist(err) {
		return err
	}
	return os.Rename(tmpPath, finalPath)
}

// createTemp creates a temp file in dir, creating dir first. The caller keeps
// housekeeping from pruning directories meanwhile.
func createTemp(dir, pattern string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, pattern)
}
