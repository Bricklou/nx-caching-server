// Tests the assembled server: route prefixes, public health, protected cache, and branch isolation end to end.
// Tokens come from a local fake forge for each supported forge type.
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/auth/oidc"
	"nx-caching-server/internal/auth/oidc/oidctest"
	"nx-caching-server/internal/auth/session"
	"nx-caching-server/internal/config"
	"nx-caching-server/internal/devforge"
	"nx-caching-server/internal/server/cache"
	"nx-caching-server/internal/store/fs"
)

const (
	audience = "nx-cache"
	repoID   = "123456"
	repoPath = "my-org/app"
)

// harness is a server wired to a fake forge.
type harness struct {
	t         *testing.T
	handler   http.Handler
	forge     *devforge.Forge
	forgeType string
}

func newHarness(t *testing.T, forgeType string) *harness {
	t.Helper()

	forge, err := devforge.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forge.Close() })

	cfg := &config.AuthConfig{Forges: []config.Forge{{
		Name:     "dev",
		Type:     forgeType,
		Issuer:   forge.Issuer(),
		Audience: audience,
		Repos: []config.Repository{{
			ID:            repoID,
			Name:          repoPath,
			DefaultBranch: "main",
			BranchRules: []config.BranchRule{
				{Match: []string{"release/*"}, Mode: config.BranchModeTrusted},
			},
		}},
	}}}

	handler := newAuthenticatedHandler(t, cfg)
	return &harness{t: t, handler: handler, forge: forge, forgeType: forgeType}
}

// token mints what the forge would issue for sc.
func (h *harness) token(sc devforge.Scenario) string {
	h.t.Helper()
	raw, err := h.forge.Signer().MintScenario(h.forgeType, sc, devforge.Job{
		Audience: audience, RepoID: repoID, RepoPath: repoPath, Actor: "louis",
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return raw
}

// tokenForRepo mints a push token for another repository of the same forge.
func (h *harness) tokenForRepo(id string) string {
	h.t.Helper()
	raw, err := h.forge.Signer().MintScenario(h.forgeType, pushMain, devforge.Job{
		Audience: audience, RepoID: id, RepoPath: "someone/else", Actor: "x",
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return raw
}

func (h *harness) do(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if method == "PUT" {
		req.Header.Set("Content-Length", strconv.Itoa(len(body)))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, req)
	return w
}

var (
	pushMain     = devforge.Scenario{Kind: devforge.ScenarioPush, Branch: "main"}
	pushRelease  = devforge.Scenario{Kind: devforge.ScenarioPush, Branch: "release/1.0", Base: "main"}
	pushFeature  = devforge.Scenario{Kind: devforge.ScenarioPush, Branch: "feature/login"}
	pullRequest1 = devforge.Scenario{Kind: devforge.ScenarioPR, Branch: "feature/login", Base: "main", Number: 1}
	pullRequest2 = devforge.Scenario{Kind: devforge.ScenarioPR, Branch: "feature/other", Base: "main", Number: 2}
	untrusted    = devforge.Scenario{Kind: devforge.ScenarioUntrusted, Branch: "main", Base: "main"}
)

func allForgeTypes() []string {
	return []string{config.ForgeTypeGitHub, config.ForgeTypeGitlab, config.ForgeTypeForgejo}
}

func TestHealthIsPublic(t *testing.T) {
	h := newHarness(t, config.ForgeTypeGitHub)

	w := h.do("GET", "/health", "", "")

	if w.Code != http.StatusOK || w.Body.String() != "OK" {
		t.Errorf("got %d %q", w.Code, w.Body.String())
	}
}

func TestCacheRequiresAuthentication(t *testing.T) {
	h := newHarness(t, config.ForgeTypeGitHub)

	// The Nx specification lists 401 for uploads and 403 for downloads.
	want := map[string]int{"PUT": http.StatusUnauthorized, "GET": http.StatusForbidden}
	for method, status := range want {
		if w := h.do(method, "/v1/cache/abc", "", "x"); w.Code != status {
			t.Errorf("%s without a token: status = %d, want %d", method, w.Code, status)
		}
		if w := h.do(method, "/v1/cache/abc", "not-a-token", "x"); w.Code != status {
			t.Errorf("%s with a bad token: status = %d, want %d", method, w.Code, status)
		}
	}
}

func TestRoutePrefixes(t *testing.T) {
	h := newHarness(t, config.ForgeTypeGitHub)
	token := h.token(pushMain)

	if w := h.do("PUT", "/v1/cache/abc", token, "body"); w.Code != http.StatusOK {
		t.Fatalf("upload: %d", w.Code)
	}

	for _, path := range []string{"/cache/abc", "/v1/abc", "/abc", "/v1/cache/", "/v2/cache/abc"} {
		if w := h.do("GET", path, token, ""); w.Code == http.StatusOK {
			t.Errorf("GET %s answered 200", path)
		}
	}
	if w := h.do("GET", "/v1/cache/abc", token, ""); w.Code != http.StatusOK || w.Body.String() != "body" {
		t.Errorf("GET /v1/cache/abc: %d %q", w.Code, w.Body.String())
	}
	if w := h.do("POST", "/v1/cache/abc", token, ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status = %d, want 405", w.Code)
	}
}

func TestWithoutAuthenticationTheCacheIsOpen(t *testing.T) {
	handler := New(Options{Cache: cache.New(fs.New(t.TempDir()))})

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if method == "PUT" {
			req.Header.Set("Content-Length", strconv.Itoa(len(body)))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	if w := do("PUT", "/v1/cache/abc", "open"); w.Code != http.StatusOK {
		t.Fatalf("upload: %d", w.Code)
	}
	if w := do("GET", "/v1/cache/abc", ""); w.Code != http.StatusOK || w.Body.String() != "open" {
		t.Errorf("download: %d %q", w.Code, w.Body.String())
	}
	if w := do("GET", "/health", ""); w.Code != http.StatusOK {
		t.Errorf("health: %d", w.Code)
	}
}

func TestBranchIsolationForEveryForge(t *testing.T) {
	for _, forgeType := range allForgeTypes() {
		t.Run(forgeType, func(t *testing.T) {
			h := newHarness(t, forgeType)
			main := h.token(pushMain)
			pr1 := h.token(pullRequest1)
			pr2 := h.token(pullRequest2)
			feature := h.token(pushFeature)

			// A pull request and a feature branch each write their own scope.
			if w := h.do("PUT", "/v1/cache/pr-entry", pr1, "from the pull request"); w.Code != http.StatusOK {
				t.Fatalf("pull request upload: %d", w.Code)
			}
			if w := h.do("PUT", "/v1/cache/feature-entry", feature, "from the feature branch"); w.Code != http.StatusOK {
				t.Fatalf("feature upload: %d", w.Code)
			}

			// Nobody else sees them, the default branch included.
			for name, other := range map[string]string{"main": main, "another pull request": pr2} {
				for _, hash := range []string{"pr-entry", "feature-entry"} {
					if w := h.do("GET", "/v1/cache/"+hash, other, ""); w.Code != http.StatusNotFound {
						t.Errorf("%s reads %s: status = %d, want 404", name, hash, w.Code)
					}
				}
			}

			// The default branch writes, and everybody reads it.
			if w := h.do("PUT", "/v1/cache/main-entry", main, "from main"); w.Code != http.StatusOK {
				t.Fatalf("main upload: %d", w.Code)
			}
			for name, caller := range map[string]string{"pull request": pr1, "feature branch": feature} {
				if w := h.do("GET", "/v1/cache/main-entry", caller, ""); w.Code != http.StatusOK || w.Body.String() != "from main" {
					t.Errorf("%s reads main: %d %q", name, w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestPullRequestsOfTrustedBasesReadThatBase(t *testing.T) {
	h := newHarness(t, config.ForgeTypeGitHub)
	release := h.token(pushRelease)
	pr := h.token(devforge.Scenario{Kind: devforge.ScenarioPR, Branch: "fix", Base: "release/1.0", Number: 3})
	main := h.token(pushMain)

	h.do("PUT", "/v1/cache/release-entry", release, "from release")

	if w := h.do("GET", "/v1/cache/release-entry", pr, ""); w.Code != http.StatusOK {
		t.Errorf("a pull request on the release branch cannot read it: %d", w.Code)
	}
	if w := h.do("GET", "/v1/cache/release-entry", main, ""); w.Code != http.StatusNotFound {
		t.Errorf("the default branch reads a release entry: %d", w.Code)
	}
}

func TestUntrustedEventsOnlyRead(t *testing.T) {
	for _, forgeType := range allForgeTypes() {
		t.Run(forgeType, func(t *testing.T) {
			h := newHarness(t, forgeType)
			h.do("PUT", "/v1/cache/main-entry", h.token(pushMain), "from main")
			token := h.token(untrusted)

			if w := h.do("PUT", "/v1/cache/poison", token, "bad"); w.Code != http.StatusForbidden {
				t.Errorf("upload: status = %d, want 403", w.Code)
			}
			if w := h.do("GET", "/v1/cache/main-entry", token, ""); w.Code != http.StatusOK {
				t.Errorf("read of the default scope: status = %d, want 200", w.Code)
			}
			if w := h.do("GET", "/v1/cache/poison", h.token(pushMain), ""); w.Code != http.StatusNotFound {
				t.Errorf("the rejected upload is visible to main: %d", w.Code)
			}
		})
	}
}

func TestAPullRequestCannotPoisonTheDefaultBranch(t *testing.T) {
	h := newHarness(t, config.ForgeTypeGitHub)

	// The attack: a pull request uploads the hash the default branch will compute.
	h.do("PUT", "/v1/cache/computed-by-main", h.token(pullRequest1), "poisoned")

	if w := h.do("PUT", "/v1/cache/computed-by-main", h.token(pushMain), "genuine"); w.Code != http.StatusOK {
		t.Fatalf("the default branch could not upload its own entry: %d", w.Code)
	}
	if w := h.do("GET", "/v1/cache/computed-by-main", h.token(pushMain), ""); w.Body.String() != "genuine" {
		t.Errorf("main serves %q", w.Body.String())
	}
	if w := h.do("GET", "/v1/cache/computed-by-main", h.token(pullRequest2), ""); w.Body.String() != "genuine" {
		t.Errorf("another pull request serves %q, want the default branch's entry", w.Body.String())
	}
}

func TestRepositoryNotInTheAllowlistIsForbidden(t *testing.T) {
	h := newHarness(t, config.ForgeTypeGitHub)

	raw, err := h.forge.Signer().MintScenario(config.ForgeTypeGitHub, pushMain, devforge.Job{
		Audience: audience, RepoID: "999", RepoPath: "someone/else", Actor: "x",
	})
	if err != nil {
		t.Fatal(err)
	}

	if w := h.do("GET", "/v1/cache/abc", raw, ""); w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestADeniedBranchIsForbidden(t *testing.T) {
	forge, err := devforge.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forge.Close() })
	cfg := &config.AuthConfig{Forges: []config.Forge{{
		Name: "dev", Type: config.ForgeTypeGitHub, Issuer: forge.Issuer(), Audience: audience,
		Repos: []config.Repository{{
			ID: repoID, DefaultBranch: "main",
			BranchRules: []config.BranchRule{{Match: []string{"forbidden/*"}, Mode: config.BranchModeDeny}},
		}},
	}}}
	handler := newAuthenticatedHandler(t, cfg)

	raw, err := forge.Signer().MintScenario(config.ForgeTypeGitHub,
		devforge.Scenario{Kind: devforge.ScenarioPush, Branch: "forbidden/x"},
		devforge.Job{Audience: audience, RepoID: repoID, RepoPath: repoPath})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/v1/cache/abc", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// exchange trades a forge token for a session token through the public endpoint.
func (h *harness) exchange(forgeToken string) (string, *httptest.ResponseRecorder) {
	h.t.Helper()
	w := h.do("POST", "/v1/auth/session", forgeToken, "")
	if w.Code != http.StatusOK {
		return "", w
	}

	var body struct {
		Token     string `json:"token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Token == "" || body.ExpiresIn <= 0 {
		h.t.Fatalf("exchange answered %q (%v)", w.Body.String(), err)
	}
	return body.Token, w
}

func TestSessionTokenWorksLikeTheForgeToken(t *testing.T) {
	for _, forgeType := range allForgeTypes() {
		t.Run(forgeType, func(t *testing.T) {
			h := newHarness(t, forgeType)
			session, w := h.exchange(h.token(pullRequest1))
			if session == "" {
				t.Fatalf("exchange: %d %q", w.Code, w.Body.String())
			}
			if got := w.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}

			// The pull request writes its own scope and reads the default one.
			if w := h.do("PUT", "/v1/cache/abc", session, "body"); w.Code != http.StatusOK {
				t.Fatalf("upload with the session: %d", w.Code)
			}
			if w := h.do("GET", "/v1/cache/abc", h.token(pullRequest1), ""); w.Code != http.StatusOK {
				t.Errorf("the forge token of the same pull request does not see the entry: %d", w.Code)
			}
			if w := h.do("GET", "/v1/cache/abc", h.token(pullRequest2), ""); w.Code != http.StatusNotFound {
				t.Errorf("another pull request sees the entry: %d, want 404", w.Code)
			}
		})
	}
}

func TestSessionKeepsTheScopeOfTheForgeToken(t *testing.T) {
	h := newHarness(t, config.ForgeTypeGitHub)
	if w := h.do("PUT", "/v1/cache/main-entry", h.token(pushMain), "from main"); w.Code != http.StatusOK {
		t.Fatalf("seed: %d", w.Code)
	}

	untrustedSession, _ := h.exchange(h.token(untrusted))

	if w := h.do("GET", "/v1/cache/main-entry", untrustedSession, ""); w.Code != http.StatusOK {
		t.Errorf("an untrusted session cannot read the default scope: %d", w.Code)
	}
	if w := h.do("PUT", "/v1/cache/poison", untrustedSession, "bad"); w.Code != http.StatusForbidden {
		t.Errorf("an untrusted session wrote: %d, want 403", w.Code)
	}
}

func TestExchangeAnswers(t *testing.T) {
	h := newHarness(t, config.ForgeTypeGitHub)
	session, _ := h.exchange(h.token(pushMain))

	tests := []struct {
		name   string
		method string
		token  string
		want   int
	}{
		{"no token", "POST", "", http.StatusUnauthorized},
		{"bad token", "POST", "not-a-token", http.StatusUnauthorized},
		{"repository not allowed", "POST", h.tokenForRepo("999"), http.StatusForbidden},
		{"a session token", "POST", session, http.StatusUnauthorized},
		{"GET is not allowed", "GET", h.token(pushMain), http.StatusMethodNotAllowed},
		{"PUT is not allowed", "PUT", h.token(pushMain), http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		if w := h.do(tt.method, "/v1/auth/session", tt.token, ""); w.Code != tt.want {
			t.Errorf("%s: status = %d, want %d", tt.name, w.Code, tt.want)
		}
	}
}

func TestExchangeDoesNotExistWithoutAuthentication(t *testing.T) {
	handler := New(Options{Cache: cache.New(fs.New(t.TempDir()))})
	req := httptest.NewRequest("POST", "/v1/auth/session", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// newAuthenticatedHandler wires forge and session tokens the way main does.
func newAuthenticatedHandler(t *testing.T, cfg *config.AuthConfig) http.Handler {
	t.Helper()
	verifier := oidc.NewVerifier(cfg)
	sessions := session.New(verifier, time.Hour, nil)
	return New(Options{
		Cache:         cache.New(fs.New(t.TempDir())),
		Authenticator: auth.Chain(verifier, sessions),
		Sessions:      sessions,
	})
}

// doWithHeader is do with one more request header.
func (h *harness) doWithHeader(method, path, token, headerName, headerValue string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(headerName, headerValue)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, req)
	return w
}

func TestGitLabJobTokenGivesEachMergeRequestItsOwnScope(t *testing.T) {
	api := oidctest.NewGitLabAPI(t)
	api.AddMergeRequest(repoID, "700", 12, "feature/x", "main")
	api.AddMergeRequest(repoID, "701", 13, "feature/x", "main")

	forge, err := devforge.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forge.Close() })
	cfg := &config.AuthConfig{Forges: []config.Forge{{
		Name: "gitlab", Type: config.ForgeTypeGitlab, Issuer: forge.Issuer(), Audience: audience, APIURL: api.URL,
		Repos: []config.Repository{{ID: repoID, Name: repoPath, DefaultBranch: "main"}},
	}}}
	h := &harness{t: t, handler: newAuthenticatedHandler(t, cfg), forge: forge, forgeType: config.ForgeTypeGitlab}

	mergeRequestToken := func(pipeline string) string {
		raw, err := forge.MintGitLab(devforge.GitLabToken{
			Audience: audience, ProjectID: repoID, ProjectPath: repoPath, Ref: "feature/x",
			PipelineSource: "merge_request_event", PipelineID: pipeline, UserLogin: "louis",
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	exchange := func(pipeline string) string {
		w := h.doWithHeader("POST", "/v1/auth/session", mergeRequestToken(pipeline), "Job-Token", oidctest.GitLabJobToken)
		var body struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
			t.Fatalf("exchange: %d %q", w.Code, w.Body.String())
		}
		return body.Token
	}
	first, second := exchange("700"), exchange("701")

	if w := h.do("PUT", "/v1/cache/abc", first, "from mr 12"); w.Code != http.StatusOK {
		t.Fatalf("upload: %d", w.Code)
	}
	if w := h.do("GET", "/v1/cache/abc", first, ""); w.Code != http.StatusOK || w.Body.String() != "from mr 12" {
		t.Errorf("the merge request does not read its own entry: %d %q", w.Code, w.Body.String())
	}
	if w := h.do("GET", "/v1/cache/abc", second, ""); w.Code != http.StatusNotFound {
		t.Errorf("another merge request of the same branch sees the entry: %d, want 404", w.Code)
	}
}
