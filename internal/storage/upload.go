package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Put stores size bytes read from r. It returns ErrExists if the entry is
// already present; existing entries are never overwritten.
func (s *Store) Put(namespace, hash string, r io.Reader, size int64) error {
	finalPath, err := s.path(namespace, hash)
	if err != nil {
		return err
	}

	if _, err := os.Stat(finalPath); err == nil {
		return ErrExists
	}

	// Write to a temp file and publish it atomically so that concurrent
	// downloads never observe a partially written entry.
	file, err := os.CreateTemp(filepath.Dir(finalPath), filepath.Base(finalPath)+tempInfix+"*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := file.Name()
	defer os.Remove(tmpPath)

	_, err = io.CopyN(file, r, size)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmpPath, 0644)
	}
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	if err := publishFile(tmpPath, finalPath); err != nil {
		if os.IsExist(err) {
			return ErrExists
		}
		return fmt.Errorf("publish file: %w", err)
	}
	return nil
}

// publishFile atomically makes tmpPath visible as finalPath without replacing
// an existing entry (os.IsExist is true in that case). It falls back to rename
// on filesystems without hard link support.
func publishFile(tmpPath, finalPath string) error {
	err := os.Link(tmpPath, finalPath)
	if err == nil || os.IsExist(err) {
		return err
	}
	return os.Rename(tmpPath, finalPath)
}
