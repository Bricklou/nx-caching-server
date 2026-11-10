// Tests the removal of stale entries and temp files, in the flat directory and in nested scopes.
// A download refreshes an entry's modification time, which is what keeps it alive.
package cache

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeAged(t *testing.T, path, content string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-age)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupRemovesStaleEntriesInNestedScopes(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "github", "1", "pr-2", "old.cache")
	fresh := filepath.Join(dir, "github", "1", mainScope, "new.cache")
	writeAged(t, old, "old", 2*time.Hour)
	writeAged(t, fresh, "new", 0)

	Cleanup(dir, time.Hour)

	if exists(old) {
		t.Error("expected the stale entry to be removed")
	}
	if !exists(fresh) {
		t.Error("expected the fresh entry to remain")
	}
}

func TestCleanupRemovesStaleTempFiles(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "a.cache.tmp-123")
	fresh := filepath.Join(dir, "b.cache.tmp-456")
	writeAged(t, stale, "x", 2*time.Hour)
	writeAged(t, fresh, "x", 0)

	Cleanup(dir, time.Hour)

	if exists(stale) {
		t.Error("expected the stale temp file to be removed")
	}
	if !exists(fresh) {
		t.Error("expected the fresh temp file to remain")
	}
}

func TestCleanupLeavesOtherFilesAlone(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "notes.txt")
	writeAged(t, other, "keep me", 100*time.Hour)

	Cleanup(dir, time.Hour)

	if !exists(other) {
		t.Error("cleanup removed a file that is not a cache entry")
	}
}

func TestCleanupOfAMissingDirectoryDoesNotFail(t *testing.T) {
	Cleanup(filepath.Join(t.TempDir(), "absent"), time.Hour)
}

func TestDownloadKeepsEntryFromCleanup(t *testing.T) {
	h, dir := newHandler(t)
	entry := filepath.Join(dir, "github", "1", mainScope, "used.cache")
	writeAged(t, entry, "used", 2*time.Hour)

	if w := do(h, "GET", "used", "", ident("1", mainAccess())); w.Code != http.StatusOK {
		t.Fatalf("download status = %d", w.Code)
	}
	Cleanup(dir, time.Hour)

	if !exists(entry) {
		t.Error("a recently downloaded entry did not survive cleanup")
	}
}

func TestRunCleanupStopsWhenTheContextEnds(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "x.cache")
	writeAged(t, stale, "x", 2*time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunCleanup(ctx, dir, time.Hour)
	}()

	// The first pass runs immediately.
	deadline := time.After(5 * time.Second)
	for exists(stale) {
		select {
		case <-deadline:
			t.Fatal("the first cleanup pass did not run")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunCleanup did not stop after the context was cancelled")
	}
}
