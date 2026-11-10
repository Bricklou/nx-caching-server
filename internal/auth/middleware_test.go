// Tests the authentication middleware: header parsing, rejection, and the identity handed to handlers.
// Verification itself is covered by the oidc tests; here a real fake forge issues the tokens.
package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/auth/oidc"
	"nx-caching-server/internal/auth/oidc/oidctest"
	"nx-caching-server/internal/devforge"
)

type middlewareFixture struct {
	tf       *oidctest.Forge
	handler  http.Handler
	called   int
	identity *auth.Identity
}

func newMiddlewareFixture(t *testing.T) *middlewareFixture {
	t.Helper()
	tf := oidctest.New(t)
	f := &middlewareFixture{tf: tf}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.called++
		f.identity, _ = auth.IdentityFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	f.handler = auth.Middleware(oidc.NewVerifier(tf.Config()))(next)
	return f
}

func (f *middlewareFixture) do(header string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/abc", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

func TestMiddlewareAcceptsAValidToken(t *testing.T) {
	f := newMiddlewareFixture(t)

	w := f.do("Bearer " + f.tf.Token(t, nil))

	if w.Code != http.StatusOK || f.called != 1 {
		t.Fatalf("status = %d, handler calls = %d", w.Code, f.called)
	}
	if f.identity == nil || f.identity.Repo.ID != oidctest.RepoID || f.identity.Forge.Name != "github" {
		t.Errorf("identity = %+v", f.identity)
	}
	if f.identity.Access.WriteScope == "" {
		t.Errorf("access = %+v", f.identity.Access)
	}
}

func TestMiddlewareAcceptsTheSchemeInAnyCase(t *testing.T) {
	f := newMiddlewareFixture(t)
	token := f.tf.Token(t, nil)

	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		if w := f.do(scheme + " " + token); w.Code != http.StatusOK {
			t.Errorf("scheme %q: status = %d", scheme, w.Code)
		}
	}
}

func TestMiddlewareRejects(t *testing.T) {
	f := newMiddlewareFixture(t)
	valid := f.tf.Token(t, nil)
	wrongAudience := f.tf.Token(t, func(g *devforge.GitHubToken) { g.Audience = "other" })

	tests := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"basic scheme", "Basic dXNlcjpwYXNz"},
		{"scheme only", "Bearer"},
		{"empty token", "Bearer "},
		{"token without scheme", valid},
		{"static looking token", "Bearer not-a-jwt"},
		{"wrong audience", "Bearer " + wrongAudience},
		{"tampered token", "Bearer " + valid[:len(valid)-4] + "AAAA"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := f.called

			w := f.do(tt.header)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
			if f.called != before {
				t.Error("the handler was called for a rejected request")
			}
			if got := strings.TrimSpace(w.Body.String()); got != auth.MessageUnauthenticated {
				t.Errorf("body = %q, want %q", got, auth.MessageUnauthenticated)
			}
			if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
				t.Errorf("Content-Type = %q, want text/plain", got)
			}
			if got := w.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("WWW-Authenticate = %q", got)
			}
		})
	}
}

func TestIdentityFromWithoutMiddleware(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)

	if id, ok := auth.IdentityFrom(req.Context()); ok || id != nil {
		t.Errorf("got (%+v, %v), want no identity", id, ok)
	}

	// A nil identity in the context is not an identity either.
	if _, ok := auth.IdentityFrom(auth.ContextWithIdentity(req.Context(), nil)); ok {
		t.Error("a nil identity was reported as present")
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
		ok     bool
	}{
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"Bearer a.b.c", "a.b.c", true},
		{"Bearer  abc", " abc", true}, // the rest of the header is the token, spaces included
		{"Bearer", "", false},
		{"Bearer ", "", false},
		{"Basic abc", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		if tt.header != "" {
			req.Header.Set("Authorization", tt.header)
		}

		got, ok := auth.BearerToken(req)

		if got != tt.want || ok != tt.ok {
			t.Errorf("auth.BearerToken(%q) = (%q, %v), want (%q, %v)", tt.header, got, ok, tt.want, tt.ok)
		}
	}
}

func TestMiddlewareForbidsAValidTokenThatMayNotUseTheCache(t *testing.T) {
	f := newMiddlewareFixture(t)

	tests := []struct {
		name  string
		token string
	}{
		{"repository not in the allowlist", f.tf.Token(t, func(g *devforge.GitHubToken) { g.RepoID = "999" })},
		{"branch denied by a rule", f.tf.Token(t, func(g *devforge.GitHubToken) { g.Ref = "refs/heads/forbidden/x" })},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := f.called

			w := f.do("Bearer " + tt.token)

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", w.Code)
			}
			if f.called != before {
				t.Error("the handler was called for a forbidden request")
			}
			if got := strings.TrimSpace(w.Body.String()); got != auth.MessageForbidden {
				t.Errorf("body = %q, want the generic message %q", got, auth.MessageForbidden)
			}
			if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
				t.Errorf("Content-Type = %q, want text/plain", got)
			}
			if got := w.Header().Get("WWW-Authenticate"); got != "" {
				t.Errorf("WWW-Authenticate = %q, want none on a 403", got)
			}
		})
	}
}

func TestMiddlewareDoesNotLeakTheReason(t *testing.T) {
	f := newMiddlewareFixture(t)

	forbidden := f.do("Bearer " + f.tf.Token(t, func(g *devforge.GitHubToken) { g.RepoID = "999" }))
	rejected := f.do("Bearer not-a-token")

	for name, w := range map[string]*httptest.ResponseRecorder{"forbidden": forbidden, "rejected": rejected} {
		body := strings.ToLower(w.Body.String())
		for _, leak := range []string{"repository", "999", "audience", "issuer", "signature", "expired", "denied", "allowlist"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s response reveals %q: %q", name, leak, w.Body.String())
			}
		}
	}
}

func TestMiddlewareUnauthenticatedStatusCanDependOnTheRequest(t *testing.T) {
	tf := oidctest.New(t)
	handler := auth.Middleware(oidc.NewVerifier(tf.Config()),
		auth.WithUnauthenticatedStatus(func(r *http.Request) int {
			if r.Method == http.MethodGet {
				return http.StatusForbidden
			}
			return http.StatusUnauthorized
		}),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	tests := []struct {
		method  string
		header  string
		want    int
		wwwAuth bool
	}{
		{"GET", "", http.StatusForbidden, false},
		{"GET", "Bearer garbage", http.StatusForbidden, false},
		{"PUT", "", http.StatusUnauthorized, true},
		{"PUT", "Bearer garbage", http.StatusUnauthorized, true},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, "/abc", nil)
		if tt.header != "" {
			req.Header.Set("Authorization", tt.header)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != tt.want {
			t.Errorf("%s %q: status = %d, want %d", tt.method, tt.header, w.Code, tt.want)
		}
		if got := w.Header().Get("WWW-Authenticate") != ""; got != tt.wwwAuth {
			t.Errorf("%s %q: WWW-Authenticate present = %v, want %v", tt.method, tt.header, got, tt.wwwAuth)
		}
	}

	// A valid token still passes, whatever the method.
	req := httptest.NewRequest("GET", "/abc", nil)
	req.Header.Set("Authorization", "Bearer "+tf.Token(t, nil))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("valid token: status = %d, want 200", w.Code)
	}
}
