package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/storage"
)

func serve(h http.Handler, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHealth(t *testing.T) {
	w := serve(New(storage.New(t.TempDir()), nil), "GET", "/health", nil, nil)

	if w.Code != http.StatusOK || w.Body.String() != "OK" {
		t.Errorf("got %d %q", w.Code, w.Body.String())
	}
}

func TestUpload(t *testing.T) {
	dir := t.TempDir()
	h := New(storage.New(dir), nil)

	t.Run("should upload file successfully", func(t *testing.T) {
		w := serve(h, "PUT", "/v1/cache/testhash", bytes.NewBufferString("test content"), map[string]string{"Content-Length": "12"})

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
		if _, err := os.Stat(filepath.Join(dir, "testhash.cache")); err != nil {
			t.Errorf("file was not created: %v", err)
		}
	})

	t.Run("should return error when Content-Length is missing", func(t *testing.T) {
		w := serve(h, "PUT", "/v1/cache/testhash2", bytes.NewBufferString("test"), nil)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("should return error when Content-Length is invalid", func(t *testing.T) {
		w := serve(h, "PUT", "/v1/cache/testhash3", bytes.NewBufferString("test"), map[string]string{"Content-Length": "abc"})

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("should return conflict when file already exists", func(t *testing.T) {
		os.WriteFile(filepath.Join(dir, "existing.cache"), []byte("existing"), 0644)

		w := serve(h, "PUT", "/v1/cache/existing", bytes.NewBufferString("new content"), map[string]string{"Content-Length": "11"})

		if w.Code != http.StatusConflict {
			t.Errorf("expected status %d, got %d", http.StatusConflict, w.Code)
		}
	})

	t.Run("should fail when the body is shorter than Content-Length", func(t *testing.T) {
		w := serve(h, "PUT", "/v1/cache/short", strings.NewReader("abc"), map[string]string{"Content-Length": "10"})

		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected status %d, got %d", http.StatusInternalServerError, w.Code)
		}
		if _, err := os.Stat(filepath.Join(dir, "short.cache")); !os.IsNotExist(err) {
			t.Error("a failed upload must not publish an entry")
		}
	})
}

func TestDownloadAndCheck(t *testing.T) {
	dir := t.TempDir()
	h := New(storage.New(dir), nil)
	os.WriteFile(filepath.Join(dir, "testhash.cache"), []byte("test content"), 0644)

	t.Run("should download file successfully", func(t *testing.T) {
		w := serve(h, "GET", "/v1/cache/testhash", nil, nil)

		if w.Code != http.StatusOK || w.Body.String() != "test content" {
			t.Errorf("got %d %q", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Content-Length"); got != "12" {
			t.Errorf("expected Content-Length 12, got %q", got)
		}
	})

	t.Run("should return not found on download", func(t *testing.T) {
		if w := serve(h, "GET", "/v1/cache/nonexistent", nil, nil); w.Code != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
		}
	})

	t.Run("should answer HEAD without body", func(t *testing.T) {
		w := serve(h, "HEAD", "/v1/cache/testhash", nil, nil)

		if w.Code != http.StatusOK || w.Body.String() != "" {
			t.Errorf("got %d %q", w.Code, w.Body.String())
		}
	})

	t.Run("should return not found on HEAD", func(t *testing.T) {
		if w := serve(h, "HEAD", "/v1/cache/nonexistent", nil, nil); w.Code != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
		}
	})

	t.Run("should reject unsupported method", func(t *testing.T) {
		if w := serve(h, "POST", "/v1/cache/testhash", nil, nil); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
		}
	})
}

func TestInvalidHashRejected(t *testing.T) {
	tmpDir := t.TempDir()
	storageDir := filepath.Join(tmpDir, "storage")
	os.Mkdir(storageDir, 0755)
	os.WriteFile(filepath.Join(tmpDir, "secret.cache"), []byte("secret"), 0644)
	h := &handlers{store: storage.New(storageDir)}

	hashes := []string{"../secret", "../../etc/x", "a/b", `a\b`, "..", ".", "", "a.b", strings.Repeat("a", 129)}
	handlerFor := map[string]http.HandlerFunc{"PUT": h.upload, "HEAD": h.check, "GET": h.download}
	for method, handler := range handlerFor {
		for _, hash := range hashes {
			req := httptest.NewRequest(method, "/v1/cache/x", bytes.NewBufferString("pwned"))
			req.Header.Set("Content-Length", "5")
			req.SetPathValue("hash", hash)
			w := httptest.NewRecorder()

			handler(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("%s %q: expected status %d, got %d", method, hash, http.StatusBadRequest, w.Code)
			}
		}
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "x.cache")); !os.IsNotExist(err) {
		t.Error("file was created outside the storage directory")
	}
}

func TestPathTraversalThroughRouter(t *testing.T) {
	tmpDir := t.TempDir()
	storageDir := filepath.Join(tmpDir, "storage")
	os.Mkdir(storageDir, 0755)
	os.WriteFile(filepath.Join(tmpDir, "secret.cache"), []byte("secret"), 0644)

	w := serve(New(storage.New(storageDir), nil), "GET", "/v1/cache/..%2Fsecret", nil, nil)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d (body %q)", http.StatusBadRequest, w.Code, w.Body.String())
	}
}

func TestNamespaceIsolation(t *testing.T) {
	storageDir := t.TempDir()
	for _, ns := range []string{"repoA", "repoB"} {
		os.Mkdir(filepath.Join(storageDir, ns), 0755)
	}
	tokens, err := auth.ParseTokens("", "repoA:tokA,repoB:tokB")
	if err != nil {
		t.Fatal(err)
	}
	h := New(storage.New(storageDir), tokens)

	do := func(method, token, body string) *httptest.ResponseRecorder {
		headers := map[string]string{"Authorization": "Bearer " + token}
		if method == "PUT" {
			headers["Content-Length"] = strconv.Itoa(len(body))
		}
		return serve(h, method, "/v1/cache/abc123", strings.NewReader(body), headers)
	}

	if w := do("PUT", "tokA", "contentA"); w.Code != http.StatusOK {
		t.Fatalf("repoA upload: expected %d, got %d", http.StatusOK, w.Code)
	}
	if w := do("HEAD", "tokB", ""); w.Code != http.StatusNotFound {
		t.Errorf("repoB HEAD: expected %d, got %d", http.StatusNotFound, w.Code)
	}
	if w := do("GET", "tokB", ""); w.Code != http.StatusNotFound {
		t.Errorf("repoB GET: expected %d, got %d", http.StatusNotFound, w.Code)
	}
	if w := do("PUT", "tokB", "contentB"); w.Code != http.StatusOK {
		t.Fatalf("repoB upload: expected %d, got %d", http.StatusOK, w.Code)
	}
	if w := do("GET", "tokA", ""); w.Code != http.StatusOK || w.Body.String() != "contentA" {
		t.Errorf("repoA GET: got %d %q", w.Code, w.Body.String())
	}
	if w := do("GET", "tokB", ""); w.Code != http.StatusOK || w.Body.String() != "contentB" {
		t.Errorf("repoB GET: got %d %q", w.Code, w.Body.String())
	}
	if w := do("GET", "unknown", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("unknown token: expected %d, got %d", http.StatusUnauthorized, w.Code)
	}

	for _, p := range []string{"repoA/abc123.cache", "repoB/abc123.cache"} {
		if _, err := os.Stat(filepath.Join(storageDir, p)); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(storageDir, "abc123.cache")); !os.IsNotExist(err) {
		t.Error("file was written to the flat storage directory")
	}
}

func TestHealthIsNotAuthenticated(t *testing.T) {
	tokens, _ := auth.ParseTokens("secret", "")
	w := serve(New(storage.New(t.TempDir()), tokens), "GET", "/health", nil, nil)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}
}

// failingStore returns err from every operation.
type failingStore struct{ err error }

func (f failingStore) Put(string, string, io.Reader, int64) error { return f.err }
func (f failingStore) Exists(string, string) (bool, error)        { return false, f.err }
func (f failingStore) Open(string, string) (io.ReadCloser, int64, error) {
	return nil, 0, f.err
}

func TestStorageErrorMapping(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{storage.ErrInvalidHash, http.StatusBadRequest},
		{storage.ErrNotFound, http.StatusNotFound},
		{storage.ErrExists, http.StatusConflict},
		{errors.New("disk on fire"), http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			h := New(failingStore{err: tt.err}, nil)

			for method, headers := range map[string]map[string]string{
				"PUT":  {"Content-Length": "1"},
				"HEAD": nil,
				"GET":  nil,
			} {
				w := serve(h, method, "/v1/cache/abc", strings.NewReader("x"), headers)
				if w.Code != tt.want {
					t.Errorf("%s: expected status %d, got %d", method, tt.want, w.Code)
				}
			}
		})
	}
}
