package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gatedReader yields its first chunk, then blocks until release is closed.
type gatedReader struct {
	first   []byte
	rest    []byte
	started chan struct{}
	release chan struct{}
	stage   int
}

func (g *gatedReader) Read(p []byte) (int, error) {
	switch g.stage {
	case 0:
		g.stage = 1
		n := copy(p, g.first)
		close(g.started)
		return n, nil
	case 1:
		<-g.release
		g.stage = 2
		n := copy(p, g.rest)
		return n, nil
	}
	return 0, io.EOF
}

func age(t *testing.T, path string, d time.Duration) {
	t.Helper()
	past := time.Now().Add(-d)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
}

func TestPutAndOpen(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	if err := s.Put("", "testhash", strings.NewReader("test content"), 12); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "testhash.cache")); err != nil {
		t.Errorf("file was not created: %v", err)
	}

	f, size, err := s.Open("", "testhash")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if string(got) != "test content" || size != 12 {
		t.Errorf("got %q (size %d)", got, size)
	}
}

func TestPutExisting(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "existing.cache"), []byte("existing"), 0644)

	err := New(dir).Put("", "existing", strings.NewReader("new content"), 11)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("expected ErrExists, got %v", err)
	}
}

func TestNotFound(t *testing.T) {
	s := New(t.TempDir())

	if _, _, err := s.Open("", "nonexistent"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open: expected ErrNotFound, got %v", err)
	}
	if ok, err := s.Exists("", "nonexistent"); ok || err != nil {
		t.Errorf("Exists: got %v, %v", ok, err)
	}
}

func TestInvalidHashRejected(t *testing.T) {
	tmpDir := t.TempDir()
	storageDir := filepath.Join(tmpDir, "storage")
	os.Mkdir(storageDir, 0755)
	s := New(storageDir)

	hashes := []string{"../secret", "../../etc/x", "a/b", `a\b`, "..", ".", "", "a.b", strings.Repeat("a", 129)}
	for _, hash := range hashes {
		if err := s.Put("", hash, strings.NewReader("pwned"), 5); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("Put %q: expected ErrInvalidHash, got %v", hash, err)
		}
		if _, _, err := s.Open("", hash); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("Open %q: expected ErrInvalidHash, got %v", hash, err)
		}
		if _, err := s.Exists("", hash); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("Exists %q: expected ErrInvalidHash, got %v", hash, err)
		}
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "x.cache")); !os.IsNotExist(err) {
		t.Error("file was created outside the storage directory")
	}
}

func TestInProgressUploadIsNotVisible(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	body := &gatedReader{
		first:   []byte("hello "),
		rest:    []byte("world"),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	done := make(chan error)
	go func() { done <- s.Put("", "slow", body, 11) }()
	<-body.started

	if ok, err := s.Exists("", "slow"); ok || err != nil {
		t.Errorf("Exists during upload: got %v, %v", ok, err)
	}
	if _, _, err := s.Open("", "slow"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open during upload: expected ErrNotFound, got %v", err)
	}

	close(body.release)
	if err := <-done; err != nil {
		t.Fatalf("Put: %v", err)
	}

	f, _, err := s.Open("", "slow")
	if err != nil {
		t.Fatalf("Open after upload: %v", err)
	}
	defer f.Close()
	if got, _ := io.ReadAll(f); string(got) != "hello world" {
		t.Errorf("expected full content after upload, got %q", got)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "slow.cache" {
		t.Errorf("expected only slow.cache in storage dir, got %v", entries)
	}
	info, _ := os.Stat(filepath.Join(dir, "slow.cache"))
	if info.Mode().Perm() != 0644 {
		t.Errorf("expected mode 0644, got %v", info.Mode().Perm())
	}
}

func TestFailedUploadLeavesNothing(t *testing.T) {
	dir := t.TempDir()

	if err := New(dir).Put("", "short", strings.NewReader("abc"), 10); err == nil {
		t.Fatal("expected an error for a truncated body")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("expected empty storage dir, got %v", entries)
	}
}

func TestPublishFileDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "x.cache.tmp-1")
	final := filepath.Join(dir, "x.cache")
	os.WriteFile(tmp, []byte("new"), 0644)
	os.WriteFile(final, []byte("old"), 0644)

	if err := publishFile(tmp, final); !os.IsExist(err) {
		t.Fatalf("expected an already-exists error, got %v", err)
	}
	if got, _ := os.ReadFile(final); string(got) != "old" {
		t.Errorf("existing entry was overwritten: %q", got)
	}
}

func TestEnsureNamespaces(t *testing.T) {
	dir := t.TempDir()

	if err := New(dir).EnsureNamespaces([]string{"repoA", "repoB"}); err != nil {
		t.Fatal(err)
	}
	for _, ns := range []string{"repoA", "repoB"} {
		if info, err := os.Stat(filepath.Join(dir, ns)); err != nil || !info.IsDir() {
			t.Errorf("namespace %s not created: %v", ns, err)
		}
	}
}

func TestCleanupNamespacedFiles(t *testing.T) {
	dir := t.TempDir()
	nsDir := filepath.Join(dir, "repoA")
	os.Mkdir(nsDir, 0755)

	oldFile := filepath.Join(nsDir, "old.cache")
	newFile := filepath.Join(nsDir, "new.cache")
	os.WriteFile(oldFile, []byte("old"), 0644)
	os.WriteFile(newFile, []byte("new"), 0644)
	age(t, oldFile, 2*time.Hour)

	New(dir).Cleanup(time.Hour)

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("expected old namespaced file to be removed")
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Errorf("expected new namespaced file to remain: %v", err)
	}
}

func TestOpenKeepsEntryFromCleanup(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	filePath := filepath.Join(dir, "used.cache")
	os.WriteFile(filePath, []byte("used"), 0644)
	age(t, filePath, 2*time.Hour)

	f, _, err := s.Open("", "used")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	s.Cleanup(time.Hour)

	if _, err := os.Stat(filePath); err != nil {
		t.Errorf("expected recently opened file to survive cleanup: %v", err)
	}
}

func TestCleanupRemovesStaleTempFiles(t *testing.T) {
	dir := t.TempDir()

	stale := filepath.Join(dir, "a.cache.tmp-123")
	fresh := filepath.Join(dir, "b.cache.tmp-456")
	os.WriteFile(stale, []byte("x"), 0644)
	os.WriteFile(fresh, []byte("x"), 0644)
	age(t, stale, 2*time.Hour)

	New(dir).Cleanup(time.Hour)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("expected stale temp file to be removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("expected fresh temp file to remain: %v", err)
	}
}
