// Removes cache entries and abandoned uploads that were not used for a while, then the directories left empty.
// How long an entry lives depends on the kind of scope it sits in; downloads refresh the modification time.
package cache

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"nx-caching-server/internal/config"
	"nx-caching-server/internal/scope"
)

// RunCleanup cleans dir immediately, then at the pace of the shortest retention,
// until ctx is done.
func RunCleanup(ctx context.Context, dir string, retention config.Retention) {
	ticker := time.NewTicker(retention.Shortest())
	defer ticker.Stop()

	for {
		Cleanup(dir, retention)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Cleanup removes entries and temp files unused for longer than the retention of
// their scope, then the empty directories.
func Cleanup(dir string, retention config.Retention) {
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
		if age := time.Since(info.ModTime()); age > retentionFor(retention, dir, path) {
			slog.Info("removing a stale cache entry", slog.String("path", path), slog.Duration("unused", age))
			return removeIgnoringMissing(path)
		}
		return nil
	})
	if err != nil {
		slog.Error("failed to walk the storage directory", slog.String("dir", dir), slog.Any("error", err))
	}

	pruneEmptyDirs(dir)
}

func isCacheFile(name string) bool {
	return strings.HasSuffix(name, ".cache") || strings.Contains(name, ".cache"+tempInfix)
}

// retentionFor picks a file's retention from its scope kind in
// <dir>/<forge>/<repository>/<scope>/<file>; anything else gets the default.
func retentionFor(retention config.Retention, dir, path string) time.Duration {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return retention.Default
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 4 || !scope.Valid(parts[2]) {
		return retention.Default
	}

	switch name := parts[2]; {
	case strings.HasPrefix(name, scope.PrefixTrusted):
		return retention.Trusted
	case strings.HasPrefix(name, scope.PrefixPR):
		return retention.PR
	case strings.HasPrefix(name, scope.PrefixDev):
		return retention.Dev
	}
	return retention.Default
}

// pruneEmptyDirs removes empty scope, repository and forge directories, deepest
// first, ignoring other names. os.Remove refuses a non-empty directory.
func pruneEmptyDirs(dir string) {
	for _, forgeDir := range subdirs(dir, validHash.MatchString) {
		for _, repoDir := range subdirs(forgeDir, validHash.MatchString) {
			for _, scopeDir := range subdirs(repoDir, scope.Valid) {
				removeEmptyDir(scopeDir)
			}
			removeEmptyDir(repoDir)
		}
		removeEmptyDir(forgeDir)
	}
}

// subdirs lists the subdirectories of dir whose name satisfies valid.
func subdirs(dir string, valid func(string) bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var paths []string
	for _, entry := range entries {
		if entry.IsDir() && valid(entry.Name()) {
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
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
		return
	}
	slog.Warn("failed to remove an empty directory", slog.String("dir", dir), slog.Any("error", err))
}

// removeIgnoringMissing deletes path; a file removed concurrently is not an error.
func removeIgnoringMissing(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
