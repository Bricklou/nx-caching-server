// Tests the filesystem backend: key to path mapping, atomic publication, unsafe keys, and housekeeping.
// Handler-level behavior is covered in the cache package against a memory store.
package fs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nx-caching-server/internal/store"
)

var ctx = context.Background()

func TestPutThenGet(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	if err := s.Put(ctx, "github/1/pr-2/abc", strings.NewReader("hello"), 5); err != nil {
		t.Fatalf("Put: %v", err)
	}

	path := filepath.Join(dir, "github", "1", "pr-2", "abc.cache")
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("entry at %s: (%v, %v)", path, info, err)
	}
	obj, err := s.Get(ctx, "github/1/pr-2/abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer obj.Close()
	if body, _ := io.ReadAll(obj); string(body) != "hello" || obj.Size != 5 {
		t.Errorf("got %q, size %d", body, obj.Size)
	}
}

func TestGetOfAMissingEntry(t *testing.T) {
	if _, err := New(t.TempDir()).Get(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestPutDoesNotReplaceAnEntry(t *testing.T) {
	s := New(t.TempDir())
	s.Put(ctx, "x", strings.NewReader("old"), 3)

	if err := s.Put(ctx, "x", strings.NewReader("new"), 3); !errors.Is(err, store.ErrExists) {
		t.Fatalf("error = %v, want ErrExists", err)
	}
	obj, _ := s.Get(ctx, "x")
	defer obj.Close()
	if body, _ := io.ReadAll(obj); string(body) != "old" {
		t.Errorf("entry was replaced: %q", body)
	}
}

func TestShortBodyLeavesNothing(t *testing.T) {
	dir := t.TempDir()

	if err := New(dir).Put(ctx, "a/b/x", strings.NewReader("abc"), 10); err == nil {
		t.Fatal("expected an error for a short body")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "a", "b"))
	if len(entries) != 0 {
		t.Errorf("a failed upload left %v", entries)
	}
}

func TestUnsafeKeysAreRefused(t *testing.T) {
	root := t.TempDir()
	s := New(filepath.Join(root, "storage"))

	for _, key := range []string{"", "../x", "a/../../x", "a//b", "/abs", "a/./b", `a\b`, "a/\x00"} {
		if err := s.Put(ctx, key, strings.NewReader("x"), 1); err == nil {
			t.Errorf("Put %q: expected an error", key)
		}
		if _, err := s.Get(ctx, key); err == nil || errors.Is(err, store.ErrNotFound) {
			t.Errorf("Get %q: error = %v, want a refusal", key, err)
		}
		if err := s.Delete(ctx, key); err == nil {
			t.Errorf("Delete %q: expected an error", key)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("an unsafe key created %v", entries)
	}
}

func TestPublishFileDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "x.cache.tmp-1")
	final := filepath.Join(dir, "x.cache")
	os.WriteFile(tmp, []byte("new"), 0o644)
	os.WriteFile(final, []byte("old"), 0o644)

	if err := publishFile(tmp, final); !os.IsExist(err) {
		t.Fatalf("expected an already-exists error, got %v", err)
	}
	if got, _ := os.ReadFile(final); string(got) != "old" {
		t.Errorf("existing entry was overwritten: %q", got)
	}
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

func TestWalkAndDelete(t *testing.T) {
	s := New(t.TempDir())
	s.Put(ctx, "github/1/pr-2/abc", strings.NewReader("x"), 1)
	s.Put(ctx, "flat", strings.NewReader("y"), 1)

	var keys []string
	err := s.Walk(ctx, func(e store.Entry) error {
		keys = append(keys, e.Key)
		return nil
	})
	if err != nil || len(keys) != 2 {
		t.Fatalf("Walk = (%v, %v)", keys, err)
	}

	if err := s.Delete(ctx, "flat"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if err := s.Delete(ctx, "flat"); err != nil {
		t.Errorf("deleting a missing entry: %v", err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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

func TestGetRefreshesTheModificationTime(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "github", "1", "t-0123456789abcdef", "x.cache")
	writeAged(t, entry, "x", 2*time.Hour)

	obj, err := New(dir).Get(ctx, "github/1/t-0123456789abcdef/x")
	if err != nil {
		t.Fatal(err)
	}
	obj.Close()

	info, _ := os.Stat(entry)
	if time.Since(info.ModTime()) > time.Minute {
		t.Errorf("the entry that was read was not refreshed: %v", info.ModTime())
	}
}

func TestGetOfAMissingEntryCreatesNoDirectory(t *testing.T) {
	dir := t.TempDir()

	New(dir).Get(ctx, "github/1/pr-4/x")

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a read created %v", entries)
	}
}

func TestWalkOnlyListsEntries(t *testing.T) {
	dir := t.TempDir()
	writeAged(t, filepath.Join(dir, "a", "b", "c", "x.cache"), "x", 0)
	writeAged(t, filepath.Join(dir, "a", "b", "c", "y.cache.tmp-1"), "x", 0)
	writeAged(t, filepath.Join(dir, "notes.txt"), "x", 0)

	var keys []string
	New(dir).Walk(ctx, func(e store.Entry) error {
		keys = append(keys, e.Key)
		return nil
	})

	if len(keys) != 1 || keys[0] != "a/b/c/x" {
		t.Errorf("keys = %v, want [a/b/c/x]", keys)
	}
}

func TestWalkOfAMissingRootReportsAnError(t *testing.T) {
	err := New(filepath.Join(t.TempDir(), "absent")).Walk(ctx, func(store.Entry) error { return nil })

	if err == nil {
		t.Error("expected an error")
	}
}

func TestHousekeepOfAMissingRootDoesNotFail(t *testing.T) {
	New(filepath.Join(t.TempDir(), "absent")).Housekeep(ctx, time.Hour)
}

func TestHousekeepRemovesStaleUploadsOnly(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "a.cache.tmp-123")
	fresh := filepath.Join(dir, "b.cache.tmp-456")
	entry := filepath.Join(dir, "c.cache")
	other := filepath.Join(dir, "notes.txt")
	writeAged(t, stale, "x", 2*time.Hour)
	writeAged(t, fresh, "x", 0)
	writeAged(t, entry, "x", 100*time.Hour)
	writeAged(t, other, "x", 100*time.Hour)

	New(dir).Housekeep(ctx, time.Hour)

	if exists(stale) {
		t.Error("expected the stale upload to be removed")
	}
	for _, path := range []string{fresh, entry, other} {
		if !exists(path) {
			t.Errorf("%s was removed", path)
		}
	}
}

func TestHousekeepRemovesDirectoriesLeftEmpty(t *testing.T) {
	dir := t.TempDir()
	emptied := filepath.Join(dir, "github", "1", "pr-9")
	neverFilled := filepath.Join(dir, "github", "1", "pr-3")
	for _, path := range []string{emptied, neverFilled} {
		os.MkdirAll(path, 0o755)
	}
	kept := filepath.Join(dir, "gitlab", "2", "pr-1", "fresh.cache")
	writeAged(t, kept, "x", 0)

	New(dir).Housekeep(ctx, time.Hour)

	for _, path := range []string{emptied, neverFilled, filepath.Join(dir, "github")} {
		if exists(path) {
			t.Errorf("empty directory %s was not removed", path)
		}
	}
	if !exists(kept) || !exists(dir) {
		t.Error("a directory holding an entry, or the root, was removed")
	}
}

func TestHousekeepKeepsADirectoryHoldingAnInProgressUpload(t *testing.T) {
	dir := t.TempDir()
	temp := filepath.Join(dir, "github", "1", "pr-9", "slow.cache.tmp-123")
	writeAged(t, temp, "partial", 0)

	New(dir).Housekeep(ctx, time.Hour)

	if !exists(temp) {
		t.Error("an in-progress upload was removed")
	}
}

func TestHousekeepOnlyRemovesDirectoriesOfTheLayout(t *testing.T) {
	dir := t.TempDir()
	foreign := []string{
		filepath.Join(dir, "lost+found"),                    // not a valid forge name
		filepath.Join(dir, "backup", ".snapshots"),          // a directory someone else keeps
		filepath.Join(dir, "github", "not a repo", "pr-1"),  // not a valid repository name
		filepath.Join(dir, "github", "1", "pr-1", "deeper"), // below the scope level
	}
	for _, path := range foreign {
		os.MkdirAll(path, 0o755)
	}

	New(dir).Housekeep(ctx, time.Hour)

	for _, path := range foreign {
		if !exists(path) {
			t.Errorf("housekeeping removed %s, which is not part of the layout", path)
		}
	}
}

// Housekeeping removing empty directories must never make a concurrent upload fail.
func TestPutsSurviveConcurrentHousekeeping(t *testing.T) {
	s := New(t.TempDir())

	stop := make(chan struct{})
	var housekeeper sync.WaitGroup
	housekeeper.Add(1)
	go func() {
		defer housekeeper.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s.Housekeep(ctx, time.Hour)
			}
		}
	}()

	for i := 0; i < 150; i++ {
		key := "github/1/pr-5/entry" + strconv.Itoa(i)
		if err := s.Put(ctx, key, strings.NewReader("content"), 7); err != nil {
			t.Errorf("put %d during housekeeping: %v", i, err)
		}
		// Removing the entry empties the directory, forcing the race again.
		s.Delete(ctx, key)
	}
	close(stop)
	housekeeper.Wait()
}

// gatedReader yields its first chunk, then blocks until release is closed.
type gatedReader struct {
	started chan struct{}
	release chan struct{}
	stage   int
}

func (g *gatedReader) Read(p []byte) (int, error) {
	switch g.stage {
	case 0:
		g.stage = 1
		close(g.started)
		return copy(p, "hello "), nil
	case 1:
		<-g.release
		g.stage = 2
		return copy(p, "world"), nil
	}
	return 0, io.EOF
}

func TestInProgressPutIsNotVisible(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	body := &gatedReader{started: make(chan struct{}), release: make(chan struct{})}

	done := make(chan error)
	go func() { done <- s.Put(ctx, "a/b/c/slow", body, 11) }()
	<-body.started

	if _, err := s.Get(ctx, "a/b/c/slow"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get during the upload: %v, want ErrNotFound", err)
	}

	close(body.release)
	if err := <-done; err != nil {
		t.Fatalf("Put: %v", err)
	}
	obj, err := s.Get(ctx, "a/b/c/slow")
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()
	if got, _ := io.ReadAll(obj); string(got) != "hello world" {
		t.Errorf("after the upload: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "a", "b", "c"))
	if len(entries) != 1 || entries[0].Name() != "slow.cache" {
		t.Errorf("expected only slow.cache, got %v", entries)
	}
}
