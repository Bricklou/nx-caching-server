// Tests the cache routes: entry upload, check and download, with and without an identity.
// Identities are injected into the request context, as the authentication middleware does.
package cache

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/scope"
)

const (
	mainScope    = "t-0123456789abcdef"
	releaseScope = "t-fedcba9876543210"
)

func ident(repoID string, access scope.Access) *auth.Identity {
	return &auth.Identity{
		Forge:  auth.ForgeInfo{Name: "github"},
		Repo:   auth.RepoInfo{ID: repoID},
		Access: access,
	}
}

func mainAccess() scope.Access {
	return scope.Access{WriteScope: mainScope, ReadChain: []string{mainScope}}
}

func prAccess(number string) scope.Access {
	return scope.Access{WriteScope: "pr-" + number, ReadChain: []string{"pr-" + number, mainScope}}
}

func readOnlyAccess() scope.Access {
	return scope.Access{ReadOnly: true, ReadChain: []string{mainScope}}
}

func newHandler(t *testing.T) (*Handler, *memStore) {
	t.Helper()
	mem := newMemStore()
	return New(mem), mem
}

// do sends a request through the routes, as the server does after stripping the prefix.
func do(h *Handler, method, hash, body string, id *auth.Identity) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/"+hash, strings.NewReader(body))
	if method == "PUT" {
		req.Header.Set("Content-Length", strconv.Itoa(len(body)))
	}
	if id != nil {
		req = req.WithContext(auth.ContextWithIdentity(req.Context(), id))
	}
	w := httptest.NewRecorder()
	h.Routes().ServeHTTP(w, req)
	return w
}

func TestUploadWithoutAuthentication(t *testing.T) {
	h, mem := newHandler(t)

	t.Run("stores the entry under the hash alone", func(t *testing.T) {
		if w := do(h, "PUT", "testhash", "test content", nil); w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		if got := mem.content("testhash"); got != "test content" {
			t.Errorf("stored %q", got)
		}
	})

	t.Run("requires a Content-Length", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/nolength", bytes.NewBufferString("test"))
		w := httptest.NewRecorder()
		h.Routes().ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})

	t.Run("rejects an invalid Content-Length", func(t *testing.T) {
		req := httptest.NewRequest("PUT", "/badlength", bytes.NewBufferString("test"))
		req.Header.Set("Content-Length", "many")
		w := httptest.NewRecorder()
		h.Routes().ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})

	t.Run("refuses to replace an entry", func(t *testing.T) {
		mem.set("existing", "existing", 0)

		if w := do(h, "PUT", "existing", "new content", nil); w.Code != http.StatusConflict {
			t.Errorf("status = %d, want 409", w.Code)
		}
		if got := mem.content("existing"); got != "existing" {
			t.Errorf("entry was replaced: %q", got)
		}
	})
}

func TestDownloadWithoutAuthentication(t *testing.T) {
	h, mem := newHandler(t)
	mem.set("testhash", "test content", 0)

	t.Run("downloads an entry", func(t *testing.T) {
		w := do(h, "GET", "testhash", "", nil)

		if w.Code != http.StatusOK || w.Body.String() != "test content" {
			t.Errorf("got %d %q", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Content-Length"); got != "12" {
			t.Errorf("Content-Length = %q", got)
		}
		if got := w.Header().Get("Content-Type"); got != "application/octet-stream" {
			t.Errorf("Content-Type = %q", got)
		}
	})

	t.Run("answers 404 for a missing entry", func(t *testing.T) {
		w := do(h, "GET", "nonexistent", "", nil)

		if w.Code != http.StatusNotFound || w.Body.Len() != 0 {
			t.Errorf("got %d %q, want 404 and no body", w.Code, w.Body.String())
		}
	})
}

func TestUnsupportedMethod(t *testing.T) {
	h, _ := newHandler(t)

	// The Nx specification defines GET and PUT only, so HEAD is not served either.
	for _, method := range []string{"HEAD", "POST", "DELETE", "PATCH"} {
		w := do(h, method, "testhash", "", nil)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, w.Code)
		}
		if method == "HEAD" && w.Header().Get("Allow") != "GET, PUT" {
			t.Errorf("HEAD: Allow = %q", w.Header().Get("Allow"))
		}
	}
}

func TestInvalidHashRejected(t *testing.T) {
	h, mem := newHandler(t)

	handlers := map[string]http.HandlerFunc{"PUT": h.upload, "GET": h.download}
	hashes := []string{"../secret", "../../etc/x", "a/b", `a\b`, "..", ".", "", "a.b", strings.Repeat("a", 129)}

	for _, id := range []*auth.Identity{nil, ident("1", prAccess("1"))} {
		for method, handle := range handlers {
			for _, hash := range hashes {
				req := httptest.NewRequest(method, "/x", bytes.NewBufferString("pwned"))
				req.Header.Set("Content-Length", "5")
				req.SetPathValue("hash", hash)
				if id != nil {
					req = req.WithContext(auth.ContextWithIdentity(req.Context(), id))
				}
				w := httptest.NewRecorder()

				handle(w, req)

				if w.Code != http.StatusBadRequest {
					t.Errorf("%s %q: status = %d, want 400", method, hash, w.Code)
				}
			}
		}
	}

	if mem.count() != 0 {
		t.Error("an entry was created for an invalid hash")
	}
}

func TestPathTraversalThroughTheRoutes(t *testing.T) {
	h, _ := newHandler(t)

	w := do(h, "GET", "..%2Fsecret", "", nil)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body %q)", w.Code, w.Body.String())
	}
}

func TestEntriesLiveUnderForgeRepositoryAndScope(t *testing.T) {
	h, mem := newHandler(t)

	if w := do(h, "PUT", "abc", "one", ident("123", prAccess("7"))); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	if got := mem.content("github/123/pr-7/abc"); got != "one" {
		t.Errorf("entry at github/123/pr-7/abc = %q", got)
	}
	if mem.has("abc") {
		t.Error("an authenticated upload reached the unscoped key")
	}
}

func TestScopesAreIsolated(t *testing.T) {
	h, _ := newHandler(t)
	pr1 := ident("1", prAccess("1"))
	pr2 := ident("1", prAccess("2"))
	main := ident("1", mainAccess())

	if w := do(h, "PUT", "x", "from pr 1", pr1); w.Code != http.StatusOK {
		t.Fatalf("pr 1 upload: %d", w.Code)
	}

	for name, caller := range map[string]*auth.Identity{"another pr": pr2, "the default branch": main} {
		if w := do(h, "GET", "x", "", caller); w.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, w.Code)
		}
	}
	if w := do(h, "GET", "x", "", pr1); w.Code != http.StatusOK || w.Body.String() != "from pr 1" {
		t.Errorf("pr 1 reads its own entry: %d %q", w.Code, w.Body.String())
	}
}

func TestRepositoriesAreIsolated(t *testing.T) {
	h, _ := newHandler(t)

	if w := do(h, "PUT", "x", "repo one", ident("1", mainAccess())); w.Code != http.StatusOK {
		t.Fatalf("upload: %d", w.Code)
	}

	if w := do(h, "GET", "x", "", ident("2", mainAccess())); w.Code != http.StatusNotFound {
		t.Errorf("another repository: status = %d, want 404", w.Code)
	}
}

func TestReadChainFallsBackToTheDefaultScope(t *testing.T) {
	h, _ := newHandler(t)

	if w := do(h, "PUT", "shared", "from main", ident("1", mainAccess())); w.Code != http.StatusOK {
		t.Fatalf("main upload: %d", w.Code)
	}

	pr := ident("1", prAccess("5"))
	if w := do(h, "GET", "shared", "", pr); w.Code != http.StatusOK || w.Body.String() != "from main" {
		t.Errorf("a hit from the default scope: %d %q", w.Code, w.Body.String())
	}
}

func TestOwnScopeWinsOverTheDefaultScope(t *testing.T) {
	h, _ := newHandler(t)
	do(h, "PUT", "x", "from main", ident("1", mainAccess()))
	pr := ident("1", prAccess("5"))
	do(h, "PUT", "x", "from pr", pr)

	if w := do(h, "GET", "x", "", pr); w.Body.String() != "from pr" {
		t.Errorf("body = %q, want the pull request's own entry", w.Body.String())
	}
	if w := do(h, "GET", "x", "", ident("1", mainAccess())); w.Body.String() != "from main" {
		t.Errorf("the default scope sees %q", w.Body.String())
	}
}

func TestThePullRequestNeverWritesTheDefaultScope(t *testing.T) {
	h, mem := newHandler(t)

	do(h, "PUT", "poison", "bad", ident("1", prAccess("9")))

	if mem.has("github/1/" + mainScope + "/poison") {
		t.Fatal("a pull request wrote into the default scope")
	}
	if w := do(h, "GET", "poison", "", ident("1", mainAccess())); w.Code != http.StatusNotFound {
		t.Errorf("the default branch sees the pull request entry: %d", w.Code)
	}
}

func TestConflictIsPerScope(t *testing.T) {
	h, _ := newHandler(t)
	pr1 := ident("1", prAccess("1"))

	if w := do(h, "PUT", "x", "a", pr1); w.Code != http.StatusOK {
		t.Fatalf("first upload: %d", w.Code)
	}
	if w := do(h, "PUT", "x", "b", pr1); w.Code != http.StatusConflict {
		t.Errorf("second upload in the same scope: %d, want 409", w.Code)
	}
	if w := do(h, "PUT", "x", "c", ident("1", prAccess("2"))); w.Code != http.StatusOK {
		t.Errorf("same hash in another scope: %d, want 200", w.Code)
	}
	if w := do(h, "PUT", "x", "d", ident("1", mainAccess())); w.Code != http.StatusOK {
		t.Errorf("same hash in the default scope: %d, want 200", w.Code)
	}
}

func TestExistingEntryInTheDefaultScopeDoesNotBlockAPullRequestUpload(t *testing.T) {
	h, _ := newHandler(t)
	do(h, "PUT", "x", "from main", ident("1", mainAccess()))

	if w := do(h, "PUT", "x", "from pr", ident("1", prAccess("3"))); w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestReadOnlyCallerCannotWrite(t *testing.T) {
	h, mem := newHandler(t)
	do(h, "PUT", "x", "from main", ident("1", mainAccess()))
	readOnly := ident("1", readOnlyAccess())

	w := do(h, "PUT", "y", "nope", readOnly)
	if w.Code != http.StatusForbidden {
		t.Errorf("upload: status = %d, want 403", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") || strings.TrimSpace(w.Body.String()) == "" {
		t.Errorf("a 403 must carry a text message, got %q %q", got, w.Body.String())
	}
	if w := do(h, "GET", "x", "", readOnly); w.Code != http.StatusOK || w.Body.String() != "from main" {
		t.Errorf("a read only caller reads the default scope: %d %q", w.Code, w.Body.String())
	}
	if mem.has("github/1/" + mainScope + "/y") {
		t.Error("a read only caller created an entry")
	}
}

func TestInvalidHashBeatsReadOnly(t *testing.T) {
	h, _ := newHandler(t)

	req := httptest.NewRequest("PUT", "/x", strings.NewReader("a"))
	req.SetPathValue("hash", "../x")
	req = req.WithContext(auth.ContextWithIdentity(req.Context(), ident("1", readOnlyAccess())))
	w := httptest.NewRecorder()
	h.upload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestUnsafeIdentitiesAreRefused(t *testing.T) {
	h, mem := newHandler(t)

	tests := []struct {
		name string
		id   *auth.Identity
	}{
		{"forge name with a traversal", &auth.Identity{Forge: auth.ForgeInfo{Name: "../x"}, Repo: auth.RepoInfo{ID: "1"}, Access: mainAccess()}},
		{"repository id with a slash", ident("1/2", mainAccess())},
		{"empty repository id", ident("", mainAccess())},
		{"unknown write scope", ident("1", scope.Access{WriteScope: "main", ReadChain: []string{mainScope}})},
		{"traversal in the read chain", ident("1", scope.Access{WriteScope: mainScope, ReadChain: []string{"../.."}})},
		{"missing write scope on a writer", ident("1", scope.Access{ReadChain: []string{mainScope}})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, method := range []string{"PUT", "GET"} {
				if w := do(h, method, "x", "body", tt.id); w.Code != http.StatusInternalServerError {
					t.Errorf("%s: status = %d, want 500", method, w.Code)
				}
			}
		})
	}

	if mem.count() != 0 {
		t.Error("unsafe identities created entries")
	}
}

func TestFailedUploadLeavesNothing(t *testing.T) {
	for name, id := range map[string]*auth.Identity{"without authentication": nil, "scoped": ident("1", prAccess("1"))} {
		t.Run(name, func(t *testing.T) {
			h, mem := newHandler(t)

			req := httptest.NewRequest("PUT", "/short", strings.NewReader("abc"))
			req.Header.Set("Content-Length", "10")
			if id != nil {
				req = req.WithContext(auth.ContextWithIdentity(req.Context(), id))
			}
			w := httptest.NewRecorder()
			h.Routes().ServeHTTP(w, req)

			if w.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", w.Code)
			}
			if mem.count() != 0 {
				t.Error("a failed upload left an entry")
			}
		})
	}
}

func TestStoreFailuresAnswer500(t *testing.T) {
	h, mem := newHandler(t)
	mem.getErr = errors.New("disk on fire")
	mem.putErr = errors.New("disk on fire")

	for _, method := range []string{"GET", "PUT"} {
		if w := do(h, method, "x", "body", nil); w.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500", method, w.Code)
		}
	}
}

func TestUnauthenticatedStatusFollowsTheSpecification(t *testing.T) {
	tests := []struct {
		method string
		want   int
	}{
		{"GET", http.StatusForbidden},
		{"PUT", http.StatusUnauthorized},
		{"HEAD", http.StatusUnauthorized},
		{"POST", http.StatusUnauthorized},
		{"DELETE", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, "/abc", nil)

		if got := UnauthenticatedStatus(req); got != tt.want {
			t.Errorf("%s: status = %d, want %d", tt.method, got, tt.want)
		}
	}
}
