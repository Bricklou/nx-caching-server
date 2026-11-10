// Verifies GitLab and Forgejo tokens end to end against the local fake forge.
// Expected scopes are computed from the scope resolver so only the claim mapping is under test.
package oidc

import (
	"errors"
	"reflect"
	"testing"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/auth/oidc/oidctest"
	"nx-caching-server/internal/config"
	"nx-caching-server/internal/devforge"
	"nx-caching-server/internal/scope"
)

func wantScopes(t *testing.T, tf *oidctest.Forge, ev scope.Event) scope.Access {
	t.Helper()
	res, err := scope.Resolve(tf.Config().Forges[0].Repos[0], ev)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestVerifyGitLab(t *testing.T) {
	tf := oidctest.New(t)
	v := NewVerifier(tf.ConfigOfType(config.ForgeTypeGitlab))

	token := func(t *testing.T, mutate func(*devforge.GitLabToken)) string {
		t.Helper()
		spec := devforge.GitLabToken{
			Audience: testAudience, ProjectID: testRepoID, ProjectPath: testRepoPath,
			Ref: "main", PipelineSource: "push", UserLogin: "louis",
		}
		if mutate != nil {
			mutate(&spec)
		}
		raw, err := tf.MintGitLab(spec)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	tests := []struct {
		name   string
		mutate func(*devforge.GitLabToken)
		event  scope.Event
	}{
		{"push on the default branch", nil, scope.Event{Kind: scope.KindPush, Branch: "main"}},
		{
			"push on a trusted branch",
			func(g *devforge.GitLabToken) { g.Ref = "release/2.0" },
			scope.Event{Kind: scope.KindPush, Branch: "release/2.0"},
		},
		{
			"merge request keyed by the source branch",
			func(g *devforge.GitLabToken) { g.Ref, g.PipelineSource = "feature/x", "merge_request_event" },
			scope.Event{Kind: scope.KindPullRequest, PRSourceBranch: "feature/x"},
		},
		{
			"tag is read only",
			func(g *devforge.GitLabToken) { g.Ref, g.RefType = "v1.0.0", "tag" },
			scope.Event{Kind: scope.KindUntrusted},
		},
		{
			"trigger pipeline is read only",
			func(g *devforge.GitLabToken) { g.PipelineSource = "trigger" },
			scope.Event{Kind: scope.KindUntrusted},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := verify(t, v, token(t, tt.mutate))

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := wantScopes(t, tf, tt.event); !reflect.DeepEqual(got.Access, want) {
				t.Errorf("scopes = %+v, want %+v", got.Access, want)
			}
			if got.Forge.Type != config.ForgeTypeGitlab || got.Repo.ID != testRepoID || got.Repo.Path != testRepoPath {
				t.Errorf("identity = %+v", got)
			}
		})
	}

	t.Run("project not allowed", func(t *testing.T) {
		_, err := verify(t, v, token(t, func(g *devforge.GitLabToken) { g.ProjectID = "999" }))
		if !errors.Is(err, auth.ErrRepoNotAllowed) {
			t.Errorf("error = %v, want auth.ErrRepoNotAllowed", err)
		}
	})

	t.Run("missing project id", func(t *testing.T) {
		if _, err := verify(t, v, token(t, func(g *devforge.GitLabToken) { g.ProjectID = "" })); err == nil {
			t.Error("a token without project_id was accepted")
		}
	})

	t.Run("branch denied by rule", func(t *testing.T) {
		_, err := verify(t, v, token(t, func(g *devforge.GitLabToken) { g.Ref = "forbidden/x" }))
		if !errors.Is(err, scope.ErrDenied) {
			t.Errorf("error = %v, want ErrDenied", err)
		}
	})
}

func TestVerifyForgejo(t *testing.T) {
	tf := oidctest.New(t)
	v := NewVerifier(tf.ConfigOfType(config.ForgeTypeForgejo))

	repoID := int64(123456) // numeric on Forgejo
	token := func(t *testing.T, mutate func(*devforge.ForgejoToken)) string {
		t.Helper()
		spec := devforge.ForgejoToken{
			Audience: testAudience, RepoID: repoID, Repo: testRepoPath, Owner: "my-org", OwnerID: 1,
			Ref: "refs/heads/main", EventName: "push", Actor: "louis", ActorID: 1,
		}
		if mutate != nil {
			mutate(&spec)
		}
		raw, err := tf.MintForgejo(spec)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	tests := []struct {
		name   string
		mutate func(*devforge.ForgejoToken)
		event  scope.Event
	}{
		{"push on the default branch", nil, scope.Event{Kind: scope.KindPush, Branch: "main"}},
		{
			"push on a trusted branch",
			func(f *devforge.ForgejoToken) { f.Ref = "refs/heads/release/2.0" },
			scope.Event{Kind: scope.KindPush, Branch: "release/2.0"},
		},
		{
			"pull request number from the ref",
			func(f *devforge.ForgejoToken) {
				f.EventName, f.Ref, f.BaseRef = "pull_request", "refs/pull/7/head", "main"
			},
			scope.Event{Kind: scope.KindPullRequest, PRNumber: 7, BaseBranch: "main"},
		},
		{
			"pull_request_target is read only",
			func(f *devforge.ForgejoToken) { f.EventName = "pull_request_target" },
			scope.Event{Kind: scope.KindUntrusted},
		},
		{
			"tag is read only",
			func(f *devforge.ForgejoToken) { f.Ref, f.RefType = "refs/tags/v1.0.0", "tag" },
			scope.Event{Kind: scope.KindUntrusted},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := verify(t, v, token(t, tt.mutate))

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := wantScopes(t, tf, tt.event); !reflect.DeepEqual(got.Access, want) {
				t.Errorf("scopes = %+v, want %+v", got.Access, want)
			}
			if got.Forge.Type != config.ForgeTypeForgejo || got.Repo.ID != testRepoID {
				t.Errorf("identity = %+v", got)
			}
		})
	}

	t.Run("repository not allowed", func(t *testing.T) {
		_, err := verify(t, v, token(t, func(f *devforge.ForgejoToken) { f.RepoID = 999 }))
		if !errors.Is(err, auth.ErrRepoNotAllowed) {
			t.Errorf("error = %v, want auth.ErrRepoNotAllowed", err)
		}
	})
}
