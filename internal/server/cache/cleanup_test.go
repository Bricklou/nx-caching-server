// Tests cleanup: stale entries and temp files, retention per kind of scope, and empty directory removal.
// A download refreshes an entry's modification time, which is what keeps it alive.
package cache

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nx-caching-server/internal/config"
)

const (
	hour = time.Hour
	day  = 24 * time.Hour
)

// uniform is a retention that treats every kind of scope the same.
func uniform(d time.Duration) config.Retention {
	return config.Retention{Default: d, Trusted: d, PR: d, Dev: d}
}

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

func scopedPath(dir, scopeName, name string) string {
	return filepath.Join(dir, "github", "1", scopeName, name)
}

func TestCleanupRemovesStaleEntriesInNestedScopes(t *testing.T) {
	dir := t.TempDir()
	old := scopedPath(dir, "pr-2", "old.cache")
	fresh := scopedPath(dir, mainScope, "new.cache")
	writeAged(t, old, "old", 2*hour)
	writeAged(t, fresh, "new", 0)

	Cleanup(dir, uniform(hour))

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
	writeAged(t, stale, "x", 2*hour)
	writeAged(t, fresh, "x", 0)

	Cleanup(dir, uniform(hour))

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
	writeAged(t, other, "keep me", 100*hour)

	Cleanup(dir, uniform(hour))

	if !exists(other) {
		t.Error("cleanup removed a file that is not a cache entry")
	}
}

func TestCleanupOfAMissingDirectoryDoesNotFail(t *testing.T) {
	Cleanup(filepath.Join(t.TempDir(), "absent"), uniform(hour))
}

func TestRetentionDependsOnTheKindOfScope(t *testing.T) {
	retention := config.Retention{Default: hour, Trusted: 7 * day, PR: day, Dev: 3 * day}
	dir := t.TempDir()

	const (
		trustedScopeName = "t-0123456789abcdef"
		devScopeName     = "dev-0123456789abcdef"
		prBranchScope    = "pr-b0123456789abcdef"
	)
	tests := []struct {
		name  string
		scope string
		age   time.Duration
		keep  bool
	}{
		{"trusted entry younger than its retention", trustedScopeName, 6 * day, true},
		{"trusted entry older than its retention", trustedScopeName, 8 * day, false},
		{"pull request entry younger than its retention", "pr-1", 12 * hour, true},
		{"pull request entry older than its retention", "pr-2", 2 * day, false},
		{"pull request keyed by branch, older", prBranchScope, 2 * day, false},
		{"dev entry younger than its retention", devScopeName, 2 * day, true},
		{"dev entry older than its retention", "dev-fedcba9876543210", 4 * day, false},
	}

	paths := make([]string, len(tests))
	for i, tt := range tests {
		paths[i] = scopedPath(dir, tt.scope, "entry"+strconv.Itoa(i)+".cache")
		writeAged(t, paths[i], "x", tt.age)
	}

	Cleanup(dir, retention)

	for i, tt := range tests {
		if got := exists(paths[i]); got != tt.keep {
			t.Errorf("%s: kept = %v, want %v", tt.name, got, tt.keep)
		}
	}
}

func TestRetentionFallsBackToTheDefaultOutsideAScope(t *testing.T) {
	retention := config.Retention{Default: hour, Trusted: 7 * day, PR: 7 * day, Dev: 7 * day}
	dir := t.TempDir()

	flat := filepath.Join(dir, "flat.cache")
	unknownScope := scopedPath(dir, "main", "unknown.cache")                // not a valid scope name
	tooShallow := filepath.Join(dir, "github", "1", "shallow.cache")        // no scope directory
	tooDeep := scopedPath(dir, mainScope, filepath.Join("x", "deep.cache")) // one level too many
	for _, path := range []string{flat, unknownScope, tooShallow, tooDeep} {
		writeAged(t, path, "x", 2*day)
	}

	Cleanup(dir, retention)

	for name, path := range map[string]string{"flat": flat, "unknown scope": unknownScope, "no scope": tooShallow, "too deep": tooDeep} {
		if exists(path) {
			t.Errorf("%s: an entry outside any scope outlived the default retention", name)
		}
	}
}

func TestCleanupRemovesDirectoriesLeftEmpty(t *testing.T) {
	dir := t.TempDir()
	stale := scopedPath(dir, "pr-9", "old.cache")
	writeAged(t, stale, "x", 2*hour)

	Cleanup(dir, uniform(hour))

	for _, path := range []string{
		filepath.Join(dir, "github", "1", "pr-9"),
		filepath.Join(dir, "github", "1"),
		filepath.Join(dir, "github"),
	} {
		if exists(path) {
			t.Errorf("empty directory %s was not removed", path)
		}
	}
	if !exists(dir) {
		t.Error("the storage directory itself was removed")
	}
}

func TestCleanupKeepsDirectoriesThatStillHoldEntries(t *testing.T) {
	dir := t.TempDir()
	writeAged(t, scopedPath(dir, "pr-9", "old.cache"), "x", 2*hour)
	fresh := scopedPath(dir, "pr-10", "fresh.cache")
	writeAged(t, fresh, "x", 0)

	Cleanup(dir, uniform(hour))

	if exists(filepath.Join(dir, "github", "1", "pr-9")) {
		t.Error("the emptied scope directory was kept")
	}
	if !exists(fresh) || !exists(filepath.Join(dir, "github", "1")) || !exists(filepath.Join(dir, "github")) {
		t.Error("a directory holding a fresh entry was removed")
	}
}

func TestCleanupKeepsADirectoryHoldingAnInProgressUpload(t *testing.T) {
	dir := t.TempDir()
	temp := scopedPath(dir, "pr-9", "slow.cache.tmp-123")
	writeAged(t, temp, "partial", 0)

	Cleanup(dir, uniform(hour))

	if !exists(temp) {
		t.Error("an in-progress upload was removed")
	}
}

func TestCleanupOnlyRemovesDirectoriesOfTheLayout(t *testing.T) {
	dir := t.TempDir()
	foreign := []string{
		filepath.Join(dir, "lost+found"),                   // not a valid forge name
		filepath.Join(dir, "backup", ".snapshots"),         // a directory someone else keeps
		filepath.Join(dir, "github", "1", "notes"),         // not a valid scope name
		filepath.Join(dir, "github", "not a repo", "pr-1"), // not a valid repository name
	}
	for _, path := range foreign {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	Cleanup(dir, uniform(hour))

	for _, path := range foreign {
		if !exists(path) {
			t.Errorf("cleanup removed %s, which is not part of the layout", path)
		}
	}
}

func TestCleanupRemovesAnEmptyScopeDirectoryNeverFilled(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "github", "1", "pr-3")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	Cleanup(dir, uniform(hour))

	if exists(empty) {
		t.Error("an empty scope directory was kept")
	}
}

func TestUploadRecreatesAScopeDirectoryRemovedByCleanup(t *testing.T) {
	h, dir := newHandler(t)
	caller := ident("1", prAccess("4"))
	if w := do(h, "PUT", "first", "a", caller); w.Code != http.StatusOK {
		t.Fatalf("first upload: %d", w.Code)
	}
	writeAged(t, scopedPath(dir, "pr-4", "first.cache"), "a", 2*hour)

	Cleanup(dir, uniform(hour))
	if exists(filepath.Join(dir, "github", "1", "pr-4")) {
		t.Fatal("the scope directory was not removed")
	}

	if w := do(h, "PUT", "second", "b", caller); w.Code != http.StatusOK {
		t.Errorf("upload after the directory was removed: %d", w.Code)
	}
	if w := do(h, "GET", "second", "", caller); w.Body.String() != "b" {
		t.Errorf("download: %q", w.Body.String())
	}
}

// Cleanup removing empty directories must never make a concurrent upload fail.
func TestUploadsSurviveConcurrentCleanup(t *testing.T) {
	h, dir := newHandler(t)
	caller := ident("1", prAccess("5"))

	stop := make(chan struct{})
	var cleaner sync.WaitGroup
	cleaner.Add(1)
	go func() {
		defer cleaner.Done()
		for {
			select {
			case <-stop:
				return
			default:
				Cleanup(dir, uniform(hour))
			}
		}
	}()

	for i := 0; i < 150; i++ {
		hash := "entry" + strconv.Itoa(i)
		if w := do(h, "PUT", hash, "content", caller); w.Code != http.StatusOK {
			t.Errorf("upload %d during cleanup: status = %d", i, w.Code)
		}
		// Age the entry so the next pass removes it and the directory, forcing the race again.
		past := time.Now().Add(-2 * hour)
		os.Chtimes(scopedPath(dir, "pr-5", hash+".cache"), past, past)
	}
	close(stop)
	cleaner.Wait()
}

func TestCreateTempCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")

	file, err := createTemp(dir, "x.cache.tmp-*")
	if err != nil {
		t.Fatalf("createTemp: %v", err)
	}
	defer file.Close()

	if !strings.HasPrefix(filepath.Base(file.Name()), "x.cache.tmp-") || filepath.Dir(file.Name()) != dir {
		t.Errorf("temp file at %s", file.Name())
	}
}

func TestDownloadKeepsEntryFromCleanup(t *testing.T) {
	h, dir := newHandler(t)
	entry := scopedPath(dir, mainScope, "used.cache")
	writeAged(t, entry, "used", 2*hour)

	if w := do(h, "GET", "used", "", ident("1", mainAccess())); w.Code != http.StatusOK {
		t.Fatalf("download status = %d", w.Code)
	}
	Cleanup(dir, uniform(hour))

	if !exists(entry) {
		t.Error("a recently downloaded entry did not survive cleanup")
	}
}

func TestRunCleanupStopsWhenTheContextEnds(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "x.cache")
	writeAged(t, stale, "x", 2*hour)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunCleanup(ctx, dir, uniform(hour))
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

func TestRetentionForEdgeCases(t *testing.T) {
	retention := config.Retention{Default: hour, Trusted: 7 * day, PR: day, Dev: 3 * day}
	dir := t.TempDir()

	tests := []struct {
		name string
		path string
		want time.Duration
	}{
		{"trusted scope", scopedPath(dir, "t-0123456789abcdef", "a.cache"), 7 * day},
		{"pull request scope", scopedPath(dir, "pr-12", "a.cache"), day},
		{"dev scope", scopedPath(dir, "dev-0123456789abcdef", "a.cache"), 3 * day},
		{"flat file", filepath.Join(dir, "a.cache"), hour},
		{"temp file in a scope", scopedPath(dir, "pr-12", "a.cache.tmp-1"), day},
		{"outside the directory", filepath.Join(filepath.Dir(dir), "other", "a.cache"), hour},
	}
	for _, tt := range tests {
		if got := retentionFor(retention, dir, tt.path); got != tt.want {
			t.Errorf("%s: retention = %v, want %v", tt.name, got, tt.want)
		}
	}
}
