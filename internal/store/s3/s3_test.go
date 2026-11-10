// Tests the S3 backend: error mapping against a stub server, and the full contract against a real service.
// The real service tests run when NX_TEST_S3_ENDPOINT is set, e.g. the dev Garage of docker-compose.dev.yml.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"nx-caching-server/internal/config"
	"nx-caching-server/internal/store"
)

var ctx = context.Background()

func useCredentials(t *testing.T, id, secret string) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", id)
	t.Setenv("AWS_SECRET_ACCESS_KEY", secret)
	t.Setenv("AWS_CONFIG_FILE", os.DevNull)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", os.DevNull)
}

func stubStore(t *testing.T, handler http.HandlerFunc) *Store {
	t.Helper()
	useCredentials(t, "id", "secret")
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	s, err := New(ctx, config.S3Storage{Bucket: "b", Endpoint: srv.URL, Region: "r", PathStyle: true, Prefix: "p"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func s3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>x</Message></Error>`, code)
}

func TestGetOfAMissingObject(t *testing.T) {
	s := stubStore(t, func(w http.ResponseWriter, r *http.Request) { s3Error(w, 404, "NoSuchKey") })

	if _, err := s.Get(ctx, "a/b"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// absentUnlessPut answers HEAD with 404 and runs put for PUT.
func absentUnlessPut(put http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		put(w, r)
	}
}

func TestPutWhenTheConditionFails(t *testing.T) {
	var gotCondition, gotPath string
	s := stubStore(t, absentUnlessPut(func(w http.ResponseWriter, r *http.Request) {
		gotCondition, gotPath = r.Header.Get("If-None-Match"), r.URL.Path
		io.Copy(io.Discard, r.Body)
		s3Error(w, 412, "PreconditionFailed")
	}))

	err := s.Put(ctx, "a/b", strings.NewReader("data"), 4)

	if !errors.Is(err, store.ErrExists) {
		t.Errorf("error = %v, want ErrExists", err)
	}
	if gotCondition != "*" || gotPath != "/b/p/a/b.cache" {
		t.Errorf("sent If-None-Match %q to %q", gotCondition, gotPath)
	}
}

func TestPutOfAnExistingObjectSendsNoBody(t *testing.T) {
	put := false
	s := stubStore(t, func(w http.ResponseWriter, r *http.Request) {
		put = put || r.Method == http.MethodPut
	})

	if err := s.Put(ctx, "a", strings.NewReader("data"), 4); !errors.Is(err, store.ErrExists) {
		t.Errorf("error = %v, want ErrExists", err)
	}
	if put {
		t.Error("an existing entry was uploaded again")
	}
}

func TestPutWithAShortBodySendsNothing(t *testing.T) {
	put := false
	s := stubStore(t, absentUnlessPut(func(w http.ResponseWriter, r *http.Request) { put = true }))

	if err := s.Put(ctx, "a", strings.NewReader("abc"), 10); err == nil {
		t.Error("expected an error")
	}
	if put {
		t.Error("a short body reached the bucket")
	}
}

func TestUnsafeKeysAreRefused(t *testing.T) {
	s := stubStore(t, func(w http.ResponseWriter, r *http.Request) { t.Error("request sent") })

	for _, key := range []string{"", "../x", "a//b", "a/./b"} {
		if _, err := s.Get(ctx, key); err == nil {
			t.Errorf("Get %q: expected an error", key)
		}
		if err := s.Put(ctx, key, strings.NewReader("x"), 1); err == nil {
			t.Errorf("Put %q: expected an error", key)
		}
	}
}

// realStore returns a store on a unique prefix of the bucket named by the environment.
func realStore(t *testing.T, touchAfter time.Duration) *Store {
	t.Helper()
	endpoint := os.Getenv("NX_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("NX_TEST_S3_ENDPOINT is not set")
	}
	t.Setenv("AWS_CONFIG_FILE", os.DevNull)

	s, err := New(ctx, config.S3Storage{
		Bucket:    envOr("NX_TEST_S3_BUCKET", "nx-cache"),
		Endpoint:  endpoint,
		Region:    envOr("NX_TEST_S3_REGION", "garage"),
		PathStyle: true,
		Prefix:    fmt.Sprintf("test-%d", time.Now().UnixNano()),
	}, Options{TouchAfter: touchAfter})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Walk(ctx, func(e store.Entry) error { return s.Delete(ctx, e.Key) })
	})
	return s
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func read(t *testing.T, s *Store, key string) string {
	t.Helper()
	obj, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get %s: %v", key, err)
	}
	defer obj.Close()
	body, _ := io.ReadAll(obj)
	if int64(len(body)) != obj.Size {
		t.Errorf("Size = %d for %d bytes", obj.Size, len(body))
	}
	return string(body)
}

func TestRealPutThenGet(t *testing.T) {
	s := realStore(t, time.Hour)

	if err := s.Put(ctx, "github/1/pr-2/abc", strings.NewReader("hello"), 5); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if got := read(t, s, "github/1/pr-2/abc"); got != "hello" {
		t.Errorf("got %q", got)
	}
	if _, err := s.Get(ctx, "github/1/pr-2/other"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing entry: %v", err)
	}
}

func TestRealPutDoesNotReplace(t *testing.T) {
	s := realStore(t, time.Hour)
	s.Put(ctx, "x", strings.NewReader("old"), 3)

	if err := s.Put(ctx, "x", strings.NewReader("new"), 3); !errors.Is(err, store.ErrExists) {
		t.Fatalf("error = %v, want ErrExists", err)
	}
	if got := read(t, s, "x"); got != "old" {
		t.Errorf("entry was replaced: %q", got)
	}
}

func TestRealLargePutIsStreamedInParts(t *testing.T) {
	s := realStore(t, time.Hour)
	size := int64(2*partSize + 1<<20) // two full parts and a short one

	if err := s.Put(ctx, "big", io.LimitReader(&counting{}, size), size); err != nil {
		t.Fatalf("Put: %v", err)
	}

	obj, err := s.Get(ctx, "big")
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()
	buf := make([]byte, 1<<16)
	var total int64
	for {
		n, err := obj.Read(buf)
		for _, b := range buf[:n] {
			if b != byte(total) {
				t.Fatalf("byte %d = %d, want %d", total, b, byte(total))
			}
			total++
		}
		if err != nil {
			break
		}
	}
	if total != size || obj.Size != size {
		t.Errorf("read %d bytes, Size %d, want %d", total, obj.Size, size)
	}
}

// counting yields 0, 1, ..., 255, 0, ... so a part sent twice or out of order shows.
type counting struct{ offset int64 }

func (c *counting) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(c.offset + int64(i))
	}
	c.offset += int64(len(p))
	return len(p), nil
}

func TestRealLargePutDoesNotReplace(t *testing.T) {
	s := realStore(t, time.Hour)
	size := int64(partSize + 1)
	s.Put(ctx, "big", io.LimitReader(&counting{}, size), size)

	if err := s.Put(ctx, "big", io.LimitReader(&counting{}, size), size); !errors.Is(err, store.ErrExists) {
		t.Errorf("error = %v, want ErrExists", err)
	}
}

func TestRealShortLargeBodyLeavesNothing(t *testing.T) {
	s := realStore(t, time.Hour)
	size := int64(2 * partSize)

	if err := s.Put(ctx, "cut", io.LimitReader(&counting{}, partSize+5), size); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := s.Get(ctx, "cut"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a failed upload left an entry: %v", err)
	}
}

func TestRealShortBodyLeavesNothing(t *testing.T) {
	s := realStore(t, time.Hour)

	if err := s.Put(ctx, "short", strings.NewReader("abc"), 10); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := s.Get(ctx, "short"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a failed upload left an entry: %v", err)
	}
}

func TestRealWalkAndDelete(t *testing.T) {
	s := realStore(t, time.Hour)
	s.Put(ctx, "github/1/pr-2/abc", strings.NewReader("x"), 1)
	s.Put(ctx, "flat", strings.NewReader("y"), 1)

	keys := map[string]bool{}
	err := s.Walk(ctx, func(e store.Entry) error {
		keys[e.Key] = true
		if time.Since(e.ModTime) > time.Minute {
			t.Errorf("%s: ModTime %v", e.Key, e.ModTime)
		}
		return nil
	})
	if err != nil || len(keys) != 2 || !keys["flat"] || !keys["github/1/pr-2/abc"] {
		t.Fatalf("Walk = (%v, %v)", keys, err)
	}

	if err := s.Delete(ctx, "flat"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if err := s.Delete(ctx, "flat"); err != nil {
		t.Errorf("deleting a missing entry: %v", err)
	}
	if _, err := s.Get(ctx, "flat"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted entry: %v", err)
	}
}

func modTime(t *testing.T, s *Store, key string) time.Time {
	t.Helper()
	var mod time.Time
	s.Walk(ctx, func(e store.Entry) error {
		if e.Key == key {
			mod = e.ModTime
		}
		return nil
	})
	return mod
}

func TestRealGetRefreshesTheModificationTime(t *testing.T) {
	s := realStore(t, 0)
	s.Put(ctx, "x", strings.NewReader("x"), 1)
	before := modTime(t, s, "x")
	time.Sleep(1500 * time.Millisecond)

	read(t, s, "x")

	// The refresh runs after the read returns.
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if modTime(t, s, "x").After(before) {
			return
		}
	}
	t.Errorf("ModTime %v was not refreshed", before)
}

func TestRealRecentEntriesAreNotTouched(t *testing.T) {
	s := realStore(t, time.Hour)
	s.Put(ctx, "x", strings.NewReader("x"), 1)
	before := modTime(t, s, "x")
	time.Sleep(1500 * time.Millisecond)

	read(t, s, "x")
	time.Sleep(500 * time.Millisecond)

	if after := modTime(t, s, "x"); !after.Equal(before) {
		t.Errorf("ModTime changed from %v to %v", before, after)
	}
}
