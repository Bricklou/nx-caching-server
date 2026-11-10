// In-memory store for the handler and cleanup tests, with hooks to age entries and force failures.
// It follows the store contracts: create-only Put, Get that marks an entry as used.
package cache

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	"nx-caching-server/internal/store"
)

type memEntry struct {
	data []byte
	mod  time.Time
}

type memStore struct {
	mu         sync.Mutex
	entries    map[string]*memEntry
	getErr     error
	putErr     error
	deleteFail map[string]error
}

func newMemStore() *memStore {
	return &memStore{entries: map[string]*memEntry{}, deleteFail: map[string]error{}}
}

func (m *memStore) Get(_ context.Context, key string) (store.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return store.Object{}, m.getErr
	}
	e, ok := m.entries[key]
	if !ok {
		return store.Object{}, store.ErrNotFound
	}
	e.mod = time.Now()
	return store.Object{ReadCloser: io.NopCloser(bytes.NewReader(e.data)), Size: int64(len(e.data))}, nil
}

func (m *memStore) Put(_ context.Context, key string, body io.Reader, size int64) error {
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, body, size); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putErr != nil {
		return m.putErr
	}
	if _, ok := m.entries[key]; ok {
		return store.ErrExists
	}
	m.entries[key] = &memEntry{data: buf.Bytes(), mod: time.Now()}
	return nil
}

func (m *memStore) Walk(_ context.Context, fn func(store.Entry) error) error {
	m.mu.Lock()
	snapshot := make([]store.Entry, 0, len(m.entries))
	for key, e := range m.entries {
		snapshot = append(snapshot, store.Entry{Key: key, ModTime: e.mod})
	}
	m.mu.Unlock()

	for _, entry := range snapshot {
		if err := fn(entry); err != nil {
			return err
		}
	}
	return nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.deleteFail[key]; err != nil {
		return err
	}
	delete(m.entries, key)
	return nil
}

// set stores an entry that is age old.
func (m *memStore) set(key, content string, age time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[key] = &memEntry{data: []byte(content), mod: time.Now().Add(-age)}
}

func (m *memStore) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.entries[key]
	return ok
}

func (m *memStore) content(key string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[key]; ok {
		return string(e.data)
	}
	return ""
}

func (m *memStore) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}
