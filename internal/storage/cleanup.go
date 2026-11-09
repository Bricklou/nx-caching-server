package storage

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EnsureNamespaces creates the directory of each namespace.
func (s *Store) EnsureNamespaces(names []string) error {
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(s.dir, name), 0755); err != nil {
			return err
		}
	}
	return nil
}

// RunCleanup removes stale entries now, then every threshold, until ctx ends.
func (s *Store) RunCleanup(ctx context.Context, threshold time.Duration) {
	ticker := time.NewTicker(threshold)
	defer ticker.Stop()
	for {
		s.Cleanup(threshold)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Cleanup removes entries and abandoned temp files unused for longer than threshold.
func (s *Store) Cleanup(threshold time.Duration) {
	root := s.dir
	if !strings.HasSuffix(root, "/") {
		root += "/"
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if !strings.HasSuffix(path, ".cache") && !strings.Contains(filepath.Base(path), ".cache"+tempInfix) {
			return nil
		}

		// ModTime is refreshed on every download, so it tracks last use.
		lastUsed := info.ModTime()
		if time.Since(lastUsed) > threshold {
			log.Printf("Removing %s: last accessed %s ago", path, time.Since(lastUsed))
			return os.Remove(path)
		}
		return nil
	})

	if err != nil {
		log.Printf("Error walking the storage directory: %v", err)
	}
}
