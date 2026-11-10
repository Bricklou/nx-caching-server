// Removes cache entries and abandoned uploads that were not used for a while.
// Downloads refresh the modification time, so it tracks last use.
package cache

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RunCleanup removes stale entries of dir now and then every threshold, until
// ctx is done.
func RunCleanup(ctx context.Context, dir string, threshold time.Duration) {
	ticker := time.NewTicker(threshold)
	defer ticker.Stop()

	for {
		Cleanup(dir, threshold)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Cleanup removes the entries and temp files of dir, in every scope, that were
// last used more than threshold ago.
func Cleanup(dir string, threshold time.Duration) {
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !isCacheFile(entry.Name()) {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if age := time.Since(info.ModTime()); age > threshold {
			slog.Info("removing a stale cache entry", slog.String("path", path), slog.Duration("unused", age))
			return removeIgnoringMissing(path)
		}
		return nil
	})
	if err != nil {
		slog.Error("failed to walk the storage directory", slog.String("dir", dir), slog.Any("error", err))
	}
}

func isCacheFile(name string) bool {
	return strings.HasSuffix(name, ".cache") || strings.Contains(name, ".cache"+tempInfix)
}

// removeIgnoringMissing deletes path; a file removed meanwhile is not an error.
func removeIgnoringMissing(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
