// Listing and removal of entries, and the upkeep only a filesystem needs: stale temp files and empty directories.
package fs

import (
	"context"
	"errors"
	iofs "io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"nx-caching-server/internal/store"
)

// segment is the shape of a forge, repository or scope directory name.
var segment = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (s *Store) Walk(ctx context.Context, fn func(store.Entry) error) error {
	return filepath.WalkDir(s.root, func(path string, entry iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			return nil
		}

		info, err := entry.Info()
		if errors.Is(err, iofs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		key := strings.TrimSuffix(filepath.ToSlash(rel), suffix)
		return fn(store.Entry{Key: key, ModTime: info.ModTime()})
	})
}

func (s *Store) Delete(_ context.Context, key string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	return removeIgnoringMissing(path)
}

// Housekeep removes temp files unused for staleUpload, then the empty
// directories of the <forge>/<repository>/<scope> layout. The depth limit is a
// guard: the root may be a shared directory such as the system temp dir.
func (s *Store) Housekeep(_ context.Context, staleUpload time.Duration) {
	err := filepath.WalkDir(s.root, func(path string, entry iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.Contains(entry.Name(), suffix+tempInfix) {
			return nil
		}

		// An upload that completes or fails meanwhile is gone, not an error.
		info, err := entry.Info()
		if errors.Is(err, iofs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if age := time.Since(info.ModTime()); age > staleUpload {
			slog.Info("removing a stale upload", slog.String("path", path), slog.Duration("unused", age))
			return removeIgnoringMissing(path)
		}
		return nil
	})
	if err != nil {
		slog.Error("failed to walk the storage directory", slog.String("dir", s.root), slog.Any("error", err))
	}

	s.pruneEmptyDirs()
}

// pruneEmptyDirs removes empty scope, repository and forge directories, deepest
// first, ignoring other names. os.Remove refuses a non-empty directory.
func (s *Store) pruneEmptyDirs() {
	s.prune.Lock()
	defer s.prune.Unlock()

	for _, forgeDir := range subdirs(s.root) {
		for _, repoDir := range subdirs(forgeDir) {
			for _, scopeDir := range subdirs(repoDir) {
				removeEmptyDir(scopeDir)
			}
			removeEmptyDir(repoDir)
		}
		removeEmptyDir(forgeDir)
	}
}

// subdirs lists the subdirectories of dir that are named like the layout.
func subdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var paths []string
	for _, entry := range entries {
		if entry.IsDir() && segment.MatchString(entry.Name()) {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	return paths
}

// removeEmptyDir removes dir unless it still holds something.
func removeEmptyDir(dir string) {
	err := os.Remove(dir)
	if err == nil {
		slog.Debug("removed an empty directory", slog.String("dir", dir))
		return
	}
	if errors.Is(err, iofs.ErrNotExist) || errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
		return
	}
	slog.Warn("failed to remove an empty directory", slog.String("dir", dir), slog.Any("error", err))
}

// removeIgnoringMissing deletes path; a file removed concurrently is not an error.
func removeIgnoringMissing(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, iofs.ErrNotExist) {
		return err
	}
	return nil
}
