// Blob storage of cache entries, independent of where the bytes live.
// A key is an opaque "/"-separated string, validated by the caller.
package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var (
	// ErrNotFound is returned by Get when the key holds no entry.
	ErrNotFound = errors.New("entry not found")
	// ErrExists is returned by Put when the key already holds an entry.
	ErrExists = errors.New("entry already exists")
)

// ValidKey refuses keys with an empty or relative segment, or characters that
// backends treat specially, so a key can never leave its backend's root.
func ValidKey(key string) error {
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, `\`+"\x00") {
			return fmt.Errorf("unsafe key %q", key)
		}
	}
	return nil
}

// Object is an open entry; the caller closes it.
type Object struct {
	io.ReadCloser
	Size int64
}

// Store reads and writes entries by key.
type Store interface {
	// Get opens an entry and marks it as recently used.
	Get(ctx context.Context, key string) (Object, error)
	// Put stores exactly size bytes of body unless the key is taken, and never
	// exposes a partial entry.
	Put(ctx context.Context, key string, body io.Reader, size int64) error
}

// Entry describes a stored entry for cleanup.
type Entry struct {
	Key     string
	ModTime time.Time
}

// Sweeper is implemented by stores that list and delete their own entries.
type Sweeper interface {
	// Walk calls fn for every entry; a returned error stops the walk.
	Walk(ctx context.Context, fn func(Entry) error) error
	// Delete removes an entry; a missing one is not an error.
	Delete(ctx context.Context, key string) error
}

// BatchDeleter is implemented by stores that remove many entries per request.
type BatchDeleter interface {
	// DeleteMany removes the entries; missing ones are not errors, and a failure
	// on one key does not stop the others.
	DeleteMany(ctx context.Context, keys []string) error
}

// Housekeeper is implemented by stores with maintenance of their own.
type Housekeeper interface {
	// Housekeep removes abandoned uploads older than staleUpload and whatever
	// else the backend leaves behind.
	Housekeep(ctx context.Context, staleUpload time.Duration)
}
