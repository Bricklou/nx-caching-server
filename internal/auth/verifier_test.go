// Tests token verification against a local fake forge serving discovery and JWKS.
// Covers valid tokens, scope mapping, and every rejection path (iss, aud, exp, alg, kid, repo).
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oauth2-proxy/mockoidc"

	"nx-caching-server/internal/config"
	"nx-caching-server/internal/devforge"
	"nx-caching-server/internal/scope"
)

const (
	testAudience = "nx-cache"
	testRepoID   = "123456"
	testRepoPath = "my-org/app"
)

// testForge is a running fake forge plus a counter of key set downloads.
type testForge struct {
	*devforge.Forge
	jwksCalls atomic.Int32
}

func newTestForge(t *testing.T, extra ...func(http.Handler) http.Handler) *testForge {
	t.Helper()

	tf := &testForge{}
	countKeys := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "jwks.json") {
				tf.jwksCalls.Add(1)
			}
			next.ServeHTTP(w, r)
		})
	}

	opts := []devforge.Option{devforge.WithMiddleware(countKeys)}
	for _, mw := range extra {
		opts = append(opts, devforge.WithMiddleware(mw))
	}
	forge, err := devforge.Run(opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forge.Close() })

	tf.Forge = forge
	return tf
}

func (tf *testForge) config() *config.AuthConfig {
	return &config.AuthConfig{Forges: []config.Forge{{
		Name:     "github",
		Type:     config.ForgeTypeGitHub,
		Issuer:   tf.Issuer(),
		Audience: testAudience,
		Repos: []config.Repository{{
			ID:            testRepoID,
			Name:          testRepoPath,
			DefaultBranch: "main",
			BranchRules: []config.BranchRule{
				{Match: []string{"release/*"}, Mode: config.BranchModeTrusted},
				{Match: []string{"forbidden/*"}, Mode: config.BranchModeDeny},
			},
		}},
	}}}
}

func (tf *testForge) token(t *testing.T, mutate func(*devforge.GitHubToken)) string {
	t.Helper()

	spec := devforge.GitHubToken{
		Audience:  testAudience,
		RepoID:    testRepoID,
		Repo:      testRepoPath,
		Ref:       "refs/heads/main",
		EventName: "push",
		Actor:     "louis",
	}
	if mutate != nil {
		mutate(&spec)
	}
	raw, err := tf.MintGitHub(spec)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func verify(t *testing.T, v *Verifier, raw string) (*Identity, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return v.Verify(ctx, raw)
}

func TestVerifyValidTokens(t *testing.T) {
	tf := newTestForge(t)
	v := NewVerifier(tf.config())

	mainScope := scopeOf(t, tf, "main")
	releaseScope := scopeOf(t, tf, "release/2.0")

	tests := []struct {
		name   string
		mutate func(*devforge.GitHubToken)
		want   scope.Access
	}{
		{
			name: "push on the default branch",
			want: scope.Access{WriteScope: mainScope, ReadChain: []string{mainScope}},
		},
		{
			name:   "push on a trusted branch",
			mutate: func(g *devforge.GitHubToken) { g.Ref = "refs/heads/release/2.0" },
			want:   scope.Access{WriteScope: releaseScope, ReadChain: []string{releaseScope, mainScope}},
		},
		{
			name: "pull request",
			mutate: func(g *devforge.GitHubToken) {
				g.EventName, g.Ref, g.BaseRef = "pull_request", "refs/pull/42/merge", "main"
			},
			want: scope.Access{WriteScope: "pr-42", ReadChain: []string{"pr-42", mainScope}},
		},
		{
			name:   "workflow dispatch counts as a push",
			mutate: func(g *devforge.GitHubToken) { g.EventName = "workflow_dispatch" },
			want:   scope.Access{WriteScope: mainScope, ReadChain: []string{mainScope}},
		},
		{
			name:   "pull_request_target is read only",
			mutate: func(g *devforge.GitHubToken) { g.EventName = "pull_request_target" },
			want:   scope.Access{ReadOnly: true, ReadChain: []string{mainScope}},
		},
		{
			name: "tag is read only",
			mutate: func(g *devforge.GitHubToken) {
				g.Ref, g.RefType = "refs/tags/v1.0.0", "tag"
			},
			want: scope.Access{ReadOnly: true, ReadChain: []string{mainScope}},
		},
		{
			name:   "unknown event is read only",
			mutate: func(g *devforge.GitHubToken) { g.EventName = "workflow_run" },
			want:   scope.Access{ReadOnly: true, ReadChain: []string{mainScope}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := verify(t, v, tf.token(t, tt.mutate))

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Access.WriteScope != tt.want.WriteScope || got.Access.ReadOnly != tt.want.ReadOnly ||
				strings.Join(got.Access.ReadChain, ",") != strings.Join(tt.want.ReadChain, ",") {
				t.Errorf("scopes = %+v, want %+v", got.Access, tt.want)
			}
		})
	}
}

func scopeOf(t *testing.T, tf *testForge, branch string) string {
	t.Helper()
	repo := tf.config().Forges[0].Repos[0]
	res, err := scope.Resolve(repo, scope.Event{Kind: scope.KindPush, Branch: branch})
	if err != nil {
		t.Fatal(err)
	}
	return res.WriteScope
}

func TestVerifyIdentityFields(t *testing.T) {
	tf := newTestForge(t)
	v := NewVerifier(tf.config())

	got, err := verify(t, v, tf.token(t, nil))
	if err != nil {
		t.Fatal(err)
	}

	if got.Forge.Name != "github" || got.Forge.Type != config.ForgeTypeGitHub || got.Forge.URL != tf.Issuer() {
		t.Errorf("forge = %+v", got.Forge)
	}
	if got.Repo.ID != testRepoID || got.Repo.Path != testRepoPath {
		t.Errorf("repo = %+v", got.Repo)
	}
	if got.Actor != "louis" || got.Ref != "refs/heads/main" || got.Event != "push" {
		t.Errorf("identity = %+v", got)
	}
}

func TestVerifyNumericRepositoryID(t *testing.T) {
	tf := newTestForge(t)
	v := NewVerifier(tf.config())

	raw, err := tf.Mint(map[string]any{
		"iss": tf.Issuer(), "aud": testAudience, "repository": testRepoPath,
		"repository_id": 123456, // a number instead of a string
		"ref":           "refs/heads/main", "event_name": "push",
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := verify(t, v, raw); err != nil {
		t.Errorf("numeric repository_id rejected: %v", err)
	}
}

func TestVerifyWithJWKSURLSkipsDiscovery(t *testing.T) {
	var discoveryCalls atomic.Int32
	tf := newTestForge(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "openid-configuration") {
				discoveryCalls.Add(1)
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	cfg := tf.config()
	cfg.Forges[0].JWKSURL = tf.JWKSEndpoint()
	v := NewVerifier(cfg)

	if _, err := verify(t, v, tf.token(t, nil)); err != nil {
		t.Errorf("token rejected with jwks_url set: %v", err)
	}
	if got := discoveryCalls.Load(); got != 0 {
		t.Errorf("discovery was called %d times, want 0", got)
	}
}

func TestVerifyRejections(t *testing.T) {
	tf := newTestForge(t)
	other := newTestForge(t)
	impostor := newTestForge(t)
	impostor.Keypair.Kid = tf.KeyID() // same key id, different key

	now := time.Now()
	clock := &now
	v := newVerifier(tf.config(), func() time.Time { return *clock })

	tests := []struct {
		name    string
		token   func(t *testing.T) string
		wantErr error
	}{
		{
			name:  "wrong audience",
			token: func(t *testing.T) string { return tf.token(t, func(g *devforge.GitHubToken) { g.Audience = "other" }) },
		},
		{
			name: "expired",
			token: func(t *testing.T) string {
				return tf.token(t, func(g *devforge.GitHubToken) {
					g.Now = now.Add(-time.Hour)
					g.TTL = time.Minute
				})
			},
		},
		{
			name:    "issuer not configured",
			token:   func(t *testing.T) string { return other.token(t, nil) },
			wantErr: ErrUnknownIssuer,
		},
		{
			name:  "signed by another key with the same key id",
			token: func(t *testing.T) string { return withIssuer(t, impostor, tf.Issuer()) },
		},
		{
			name:  "unknown key id",
			token: func(t *testing.T) string { return withIssuer(t, other, tf.Issuer()) },
		},
		{
			name: "tampered payload",
			token: func(t *testing.T) string {
				return tamper(t, tf.token(t, nil), func(c map[string]any) { c["repository_id"] = "999" })
			},
		},
		{
			name:    "repository not allowed",
			token:   func(t *testing.T) string { return tf.token(t, func(g *devforge.GitHubToken) { g.RepoID = "999" }) },
			wantErr: ErrRepoNotAllowed,
		},
		{
			name:  "missing repository id",
			token: func(t *testing.T) string { return tf.token(t, func(g *devforge.GitHubToken) { g.RepoID = "" }) },
		},
		{
			name: "branch denied by rule",
			token: func(t *testing.T) string {
				return tf.token(t, func(g *devforge.GitHubToken) { g.Ref = "refs/heads/forbidden/x" })
			},
			wantErr: scope.ErrDenied,
		},
		{
			name: "pull request with a non pull request ref",
			token: func(t *testing.T) string {
				return tf.token(t, func(g *devforge.GitHubToken) { g.EventName = "pull_request" })
			},
		},
		{
			name:  "alg none",
			token: func(t *testing.T) string { return unsigned(t, tf.Issuer(), "none") },
		},
		{
			name:  "alg HS256 with a guessed secret",
			token: func(t *testing.T) string { return hs256(t, tf.Issuer(), tf.KeyID()) },
		},
		{name: "empty token", token: func(*testing.T) string { return "" }, wantErr: ErrMalformedToken},
		{name: "not a jwt", token: func(*testing.T) string { return "just-a-static-token" }, wantErr: ErrMalformedToken},
		{name: "payload not base64", token: func(*testing.T) string { return "a.!!!.c" }, wantErr: ErrMalformedToken},
		{name: "payload not json", token: func(*testing.T) string { return "a." + b64([]byte("nope")) + ".c" }, wantErr: ErrMalformedToken},
		{name: "no issuer", token: func(*testing.T) string { return "a." + b64([]byte(`{}`)) + ".c" }, wantErr: ErrMalformedToken},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := verify(t, v, tt.token(t))

			if err == nil {
				t.Fatalf("expected an error, got identity %+v", got)
			}
			if got != nil {
				t.Errorf("identity must be nil on error, got %+v", got)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyExpiryFollowsTheClock(t *testing.T) {
	tf := newTestForge(t)
	now := time.Now()
	v := newVerifier(tf.config(), func() time.Time { return now })
	raw := tf.token(t, func(g *devforge.GitHubToken) { g.Now, g.TTL = now, 5*time.Minute })

	if _, err := verify(t, v, raw); err != nil {
		t.Fatalf("fresh token rejected: %v", err)
	}

	now = now.Add(6 * time.Minute)
	if _, err := verify(t, v, raw); err == nil {
		t.Error("token accepted after its expiry")
	}
}

func TestVerifyUnknownKeyIsDownloadedAtMostOncePerInterval(t *testing.T) {
	tf := newTestForge(t)
	other := newTestForge(t)
	clock := time.Now()
	v := newVerifier(tf.config(), func() time.Time { return clock })
	unknown := func() string { return withIssuer(t, other, tf.Issuer()) }

	if _, err := verify(t, v, tf.token(t, nil)); err != nil {
		t.Fatal(err)
	}
	before := tf.jwksCalls.Load()

	for range 20 {
		if _, err := verify(t, v, unknown()); err == nil {
			t.Fatal("unknown key accepted")
		}
	}
	if got := tf.jwksCalls.Load() - before; got != 0 {
		t.Errorf("tokens with unknown key ids caused %d key downloads within the interval, want 0", got)
	}

	clock = clock.Add(minRefresh)
	for range 20 {
		if _, err := verify(t, v, unknown()); err == nil {
			t.Fatal("unknown key accepted")
		}
	}
	if got := tf.jwksCalls.Load() - before; got != 1 {
		t.Errorf("tokens with unknown key ids caused %d key downloads after the interval, want 1", got)
	}

	// Known keys never need a download.
	before = tf.jwksCalls.Load()
	if _, err := verify(t, v, tf.token(t, nil)); err != nil {
		t.Fatal(err)
	}
	if got := tf.jwksCalls.Load() - before; got != 0 {
		t.Errorf("a valid token caused %d key downloads, want 0", got)
	}
}

func TestVerifyConcurrentUnknownKeysShareOneDownload(t *testing.T) {
	tf := newTestForge(t)
	other := newTestForge(t)
	v := NewVerifier(tf.config())
	tokens := make([]string, 30)
	for i := range tokens {
		tokens[i] = withIssuer(t, other, tf.Issuer())
	}

	var wg sync.WaitGroup
	for _, raw := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			verify(t, v, raw)
		}()
	}
	wg.Wait()

	if got := tf.jwksCalls.Load(); got != 1 {
		t.Errorf("%d concurrent tokens caused %d key downloads, want 1", len(tokens), got)
	}
}

func TestVerifyAcceptsAKeyPublishedAfterTheLastDownload(t *testing.T) {
	tf := newTestForge(t)
	clock := time.Now()
	v := newVerifier(tf.config(), func() time.Time { return clock })

	// The forge rotates its key; the old key set is cached.
	if _, err := verify(t, v, tf.token(t, nil)); err != nil {
		t.Fatal(err)
	}
	rotated, err := mockoidc.RandomKeypair(2048)
	if err != nil {
		t.Fatal(err)
	}
	tf.Keypair = rotated
	clock = clock.Add(minRefresh)

	if _, err := verify(t, v, tf.token(t, nil)); err != nil {
		t.Errorf("a token signed with a rotated key was refused: %v", err)
	}
}

func TestVerifyRetriesDiscoveryAfterTheCooldown(t *testing.T) {
	var discoveryCalls atomic.Int32
	tf := newTestForge(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "openid-configuration") {
				discoveryCalls.Add(1)
			}
			next.ServeHTTP(w, r)
		})
	})

	now := time.Now()
	v := newVerifier(tf.config(), func() time.Time { return now })
	raw := tf.token(t, func(g *devforge.GitHubToken) { g.Now = now })

	// The next request to the forge, the discovery call, fails.
	tf.QueueError(&mockoidc.ServerError{Code: http.StatusServiceUnavailable, Error: "unavailable"})
	if _, err := verify(t, v, raw); err == nil {
		t.Fatal("token accepted while the forge is down")
	}
	if got := discoveryCalls.Load(); got != 1 {
		t.Fatalf("discovery calls = %d, want 1", got)
	}

	// Within the cooldown the failure is served from memory.
	now = now.Add(discoveryCooldown / 2)
	if _, err := verify(t, v, raw); err == nil {
		t.Fatal("token accepted during the cooldown")
	}
	if got := discoveryCalls.Load(); got != 1 {
		t.Errorf("discovery was retried during the cooldown: %d calls", got)
	}

	// After it, discovery runs again and the forge is back.
	now = now.Add(discoveryCooldown)
	if _, err := verify(t, v, raw); err != nil {
		t.Errorf("token rejected after the cooldown: %v", err)
	}
	if got := discoveryCalls.Load(); got != 2 {
		t.Errorf("discovery calls = %d, want 2", got)
	}
}

func TestVerifyBuildsTheVerifierOnceUnderConcurrency(t *testing.T) {
	var discoveryCalls atomic.Int32
	tf := newTestForge(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "openid-configuration") {
				discoveryCalls.Add(1)
			}
			next.ServeHTTP(w, r)
		})
	})
	v := NewVerifier(tf.config())
	raw := tf.token(t, nil)

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := verify(t, v, raw)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent verification failed: %v", err)
		}
	}
	if got := discoveryCalls.Load(); got != 1 {
		t.Errorf("discovery calls = %d, want 1", got)
	}
}

func TestVerifyUnsupportedForgeType(t *testing.T) {
	tf := newTestForge(t)
	cfg := tf.config()
	cfg.Forges[0].Type = "bitbucket"
	v := NewVerifier(cfg)

	_, err := verify(t, v, tf.token(t, nil))

	if !errors.Is(err, ErrUnsupportedForge) {
		t.Errorf("error = %v, want ErrUnsupportedForge", err)
	}
}

func TestIsToken(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"a.b.c", true},
		{"eyJhbGciOiJSUzI1NiJ9.eyJpc3MiOiJ4In0.c2ln", true},
		{"static-token", false},
		{"a.b", false},
		{"a.b.c.d", false},
		{"a. b.c", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsToken(tt.raw); got != tt.want {
			t.Errorf("IsToken(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

// withIssuer mints a valid-looking token from forge f that claims issuer.
func withIssuer(t *testing.T, f *testForge, issuer string) string {
	t.Helper()
	raw, err := f.Mint(map[string]any{
		"iss": issuer, "aud": testAudience, "repository": testRepoPath,
		"repository_id": testRepoID, "ref": "refs/heads/main", "event_name": "push",
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func b64(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func jsonPart(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b64(data)
}

func claimsFor(issuer string) map[string]any {
	return map[string]any{
		"iss": issuer, "aud": testAudience, "repository": testRepoPath,
		"repository_id": testRepoID, "ref": "refs/heads/main", "event_name": "push",
		"exp": time.Now().Add(time.Minute).Unix(),
	}
}

func unsigned(t *testing.T, issuer, alg string) string {
	t.Helper()
	header := jsonPart(t, map[string]any{"alg": alg, "typ": "JWT"})
	return header + "." + jsonPart(t, claimsFor(issuer)) + "."
}

func hs256(t *testing.T, issuer, kid string) string {
	t.Helper()
	header := jsonPart(t, map[string]any{"alg": "HS256", "typ": "JWT", "kid": kid})
	signingInput := header + "." + jsonPart(t, claimsFor(issuer))
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte(signingInput))
	return signingInput + "." + b64(mac.Sum(nil))
}

// tamper rewrites the payload of a signed token and keeps the original signature.
func tamper(t *testing.T, raw string, mutate func(map[string]any)) string {
	t.Helper()
	parts := strings.Split(raw, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]any{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	mutate(claims)
	return parts[0] + "." + jsonPart(t, claims) + "." + parts[2]
}
