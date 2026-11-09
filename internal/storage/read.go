package storage

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

// Exists reports whether the entry is stored.
func (s *Store) Exists(namespace, hash string) (bool, error) {
	filePath, err := s.path(namespace, hash)
	if err != nil {
		return false, err
	}

	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat file: %w", err)
	}
	return true, nil
}

// Open returns the entry and its size, or ErrNotFound. The caller must close it.
func (s *Store) Open(namespace, hash string) (io.ReadCloser, int64, error) {
	filePath, err := s.path(namespace, hash)
	if err != nil {
		return nil, 0, err
	}

	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("open file: %w", err)
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("stat file: %w", err)
	}

	// Mark the entry as recently used so cleanup keeps it. Filesystem atime
	// is not reliable for this (noatime/relatime mounts, Windows).
	now := time.Now()
	if err := os.Chtimes(filePath, now, now); err != nil {
		log.Printf("Failed to update access time for %s: %v", filePath, err)
	}

	return file, stat.Size(), nil
}
