// A local fake forge and the configuration that trusts it, for tests of packages that verify forge tokens.
// It counts key set downloads so tests can assert how often a forge is contacted.
package oidctest

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"nx-caching-server/internal/config"
	"nx-caching-server/internal/devforge"
)

// What the test forge is configured with.
const (
	Audience = "nx-cache"
	RepoID   = "123456"
	RepoPath = "my-org/app"
)

// Forge is a running fake forge plus a counter of key set downloads.
type Forge struct {
	*devforge.Forge
	JWKSCalls atomic.Int32
}

// New starts a forge that stops with the test. Each extra wraps its endpoints.
func New(t *testing.T, extra ...func(http.Handler) http.Handler) *Forge {
	t.Helper()

	f := &Forge{}
	countKeys := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "jwks.json") {
				f.JWKSCalls.Add(1)
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

	f.Forge = forge
	return f
}

// Config trusts the forge as a GitHub forge with one repository.
func (f *Forge) Config() *config.AuthConfig {
	return &config.AuthConfig{Forges: []config.Forge{{
		Name:     "github",
		Type:     config.ForgeTypeGitHub,
		Issuer:   f.Issuer(),
		Audience: Audience,
		Repos: []config.Repository{{
			ID:            RepoID,
			Name:          RepoPath,
			DefaultBranch: "main",
			BranchRules: []config.BranchRule{
				{Match: []string{"release/*"}, Mode: config.BranchModeTrusted},
				{Match: []string{"forbidden/*"}, Mode: config.BranchModeDeny},
			},
		}},
	}}}
}

// ConfigOfType is Config with another forge type.
func (f *Forge) ConfigOfType(forgeType string) *config.AuthConfig {
	cfg := f.Config()
	cfg.Forges[0].Type = forgeType
	return cfg
}

// Token mints a GitHub-shaped push token for the configured repository on main;
// mutate adjusts it.
func (f *Forge) Token(t *testing.T, mutate func(*devforge.GitHubToken)) string {
	t.Helper()

	spec := devforge.GitHubToken{
		Audience:  Audience,
		RepoID:    RepoID,
		Repo:      RepoPath,
		Ref:       "refs/heads/main",
		EventName: "push",
		Actor:     "louis",
	}
	if mutate != nil {
		mutate(&spec)
	}
	raw, err := f.MintGitHub(spec)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
