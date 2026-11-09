// Package storage persists cache entries on the local filesystem.
package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
)

var (
	ErrInvalidHash = errors.New("invalid hash")
	ErrNotFound    = errors.New("entry not found")
	ErrExists      = errors.New("entry already exists")
)

// tempInfix marks in-progress uploads (<hash>.cache.tmp-<random>), which are
// never served and are removed by cleanup once stale.
const tempInfix = ".tmp-"

// validHash allows only characters that cannot form path separators or
// traversal sequences, so the hash is always a single file name.
var validHash = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// ValidHash reports whether s is safe to use as a hash or namespace name.
func ValidHash(s string) bool {
	return validHash.MatchString(s)
}

// Store keeps entries under dir. An empty namespace stores files directly in dir.
type Store struct {
	dir string
}

func New(dir string) *Store {
	return &Store{dir: dir}
}

// path returns the file path for hash within namespace.
func (s *Store) path(namespace, hash string) (string, error) {
	if !ValidHash(hash) {
		return "", ErrInvalidHash
	}
	return filepath.Join(s.dir, namespace, fmt.Sprintf("%s.cache", hash)), nil
}
