// Tests the GitLab merge request lookup of the verifier: the scope a merge request gets, and when GitLab is not asked.
// A fake GitLab API serves pipelines and merge requests, checks the job token and counts the requests it receives.
package oidc

import (
	"errors"
	"testing"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/auth/oidc/oidctest"
	"nx-caching-server/internal/config"
	"nx-caching-server/internal/devforge"
	"nx-caching-server/internal/scope"
)

const gitlabProject = oidctest.RepoID

// gitlabFixture is a GitLab forge whose API is the fake one, plus a verifier for it.
type gitlabFixture struct {
	tf  *oidctest.Forge
	api *oidctest.GitLabAPI
	v   *Verifier
}

func newGitlabFixture(t *testing.T) *gitlabFixture {
	t.Helper()
	tf := oidctest.New(t)
	api := oidctest.NewGitLabAPI(t)

	cfg := tf.ConfigOfType(config.ForgeTypeGitlab)
	cfg.Forges[0].APIURL = api.URL
	return &gitlabFixture{tf: tf, api: api, v: NewVerifier(cfg)}
}

// mergeRequestToken is what a merge request job of the given pipeline receives.
func (f *gitlabFixture) mergeRequestToken(t *testing.T, projectID, pipelineID, branch string) string {
	t.Helper()
	raw, err := f.tf.MintGitLab(devforge.GitLabToken{
		Audience: oidctest.Audience, ProjectID: projectID, ProjectPath: oidctest.RepoPath,
		Ref: branch, PipelineSource: "merge_request_event", PipelineID: pipelineID, UserLogin: "louis",
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// resolved verifies raw, asks GitLab for its merge request with jobToken, and resolves the identity.
func (f *gitlabFixture) resolved(t *testing.T, raw, jobToken string) *auth.Identity {
	t.Helper()
	verified, err := f.v.Verify(t.Context(), raw)
	if err != nil {
		t.Fatalf("token refused: %v", err)
	}
	verified = f.v.WithMergeRequest(t.Context(), verified, jobToken)
	id, err := f.v.Identity(verified.Forge, verified.Claims)
	if err != nil {
		t.Fatalf("identity refused: %v", err)
	}
	return id
}

func TestMergeRequestsOfTheSameBranchGetTheirOwnScopes(t *testing.T) {
	f := newGitlabFixture(t)
	f.api.AddMergeRequest(gitlabProject, "700", 12, "feature/x", "main")
	f.api.AddMergeRequest(gitlabProject, "701", 13, "feature/x", "main")

	a := f.resolved(t, f.mergeRequestToken(t, gitlabProject, "700", "feature/x"), oidctest.GitLabJobToken)
	b := f.resolved(t, f.mergeRequestToken(t, gitlabProject, "701", "feature/x"), oidctest.GitLabJobToken)

	if a.Access.WriteScope != "pr-12" || b.Access.WriteScope != "pr-13" {
		t.Errorf("write scopes %q and %q, want pr-12 and pr-13", a.Access.WriteScope, b.Access.WriteScope)
	}
}

func TestMergeRequestReadsItsTargetBranchWhenTrusted(t *testing.T) {
	f := newGitlabFixture(t)
	f.api.AddMergeRequest(gitlabProject, "700", 12, "feature/x", "release/1.0")

	id := f.resolved(t, f.mergeRequestToken(t, gitlabProject, "700", "feature/x"), oidctest.GitLabJobToken)

	want, err := scope.Resolve(f.tf.Config().Forges[0].Repos[0], scope.PullRequestNumber(12, "release/1.0", "louis"))
	if err != nil {
		t.Fatal(err)
	}
	if id.Access.WriteScope != want.WriteScope || len(id.Access.ReadChain) != 3 {
		t.Errorf("access %+v, want %+v with the trusted target branch in the read chain", id.Access, want)
	}
}

func TestMergeRequestLookupIsCached(t *testing.T) {
	f := newGitlabFixture(t)
	f.api.AddMergeRequest(gitlabProject, "700", 12, "feature/x", "main")
	raw := f.mergeRequestToken(t, gitlabProject, "700", "feature/x")

	f.resolved(t, raw, oidctest.GitLabJobToken)
	before := f.api.Calls.Load()
	for range 10 {
		f.resolved(t, raw, oidctest.GitLabJobToken)
	}

	if got := f.api.Calls.Load() - before; got != 0 {
		t.Errorf("repeated exchanges of one job sent %d requests to GitLab", got)
	}
}

func TestWhenGitLabCannotSayTheJobOnlyReads(t *testing.T) {
	tests := map[string]func(*gitlabFixture) string{
		"GitLab is down": func(f *gitlabFixture) string {
			f.api.Failing.Store(true)
			return oidctest.GitLabJobToken
		},
		"the job token is refused": func(*gitlabFixture) string { return "glcbt-wrong" },
		"the job token is missing": func(*gitlabFixture) string { return "" },
	}

	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			f := newGitlabFixture(t)
			f.api.AddMergeRequest(gitlabProject, "700", 12, "feature/x", "main")
			jobToken := setup(f)

			id := f.resolved(t, f.mergeRequestToken(t, gitlabProject, "700", "feature/x"), jobToken)

			if jobToken == "" {
				// Nothing was asked, so the default applies: the source branch.
				if id.Access.ReadOnly || id.Access.WriteScope == "" {
					t.Errorf("access %+v, want the source branch scope", id.Access)
				}
				return
			}
			if !id.Access.ReadOnly || len(id.Access.ReadChain) != 1 {
				t.Errorf("access %+v: a job whose merge request is unknown must read the default scope only", id.Access)
			}
		})
	}
}

func TestAMergeRequestThatContradictsTheTokenOnlyReads(t *testing.T) {
	f := newGitlabFixture(t)
	// GitLab says pipeline 700 belongs to a merge request of another branch.
	f.api.AddMergeRequest(gitlabProject, "700", 12, "someone-elses-branch", "main")

	id := f.resolved(t, f.mergeRequestToken(t, gitlabProject, "700", "feature/x"), oidctest.GitLabJobToken)

	if !id.Access.ReadOnly {
		t.Errorf("access %+v, want read only", id.Access)
	}
}

func TestRepositoriesOutsideTheAllowlistCostNoRequest(t *testing.T) {
	f := newGitlabFixture(t)
	f.api.AddMergeRequest("999", "700", 12, "feature/x", "main")

	verified, err := f.v.Verify(t.Context(), f.mergeRequestToken(t, "999", "700", "feature/x"))
	if err != nil {
		t.Fatal(err)
	}
	resolved := f.v.WithMergeRequest(t.Context(), verified, oidctest.GitLabJobToken)

	if _, err := f.v.Identity(resolved.Forge, resolved.Claims); !errors.Is(err, auth.ErrRepoNotAllowed) {
		t.Errorf("error = %v, want ErrRepoNotAllowed", err)
	}
	if got := f.api.Calls.Load(); got != 0 {
		t.Errorf("a token of an unlisted project caused %d requests to GitLab", got)
	}
}

func TestNothingIsLookedUpForOtherEvents(t *testing.T) {
	f := newGitlabFixture(t)
	raw, err := f.tf.MintGitLab(devforge.GitLabToken{
		Audience: oidctest.Audience, ProjectID: gitlabProject, ProjectPath: oidctest.RepoPath,
		Ref: "main", PipelineSource: "push", PipelineID: "700", UserLogin: "louis",
	})
	if err != nil {
		t.Fatal(err)
	}

	id := f.resolved(t, raw, oidctest.GitLabJobToken)

	if f.api.Calls.Load() != 0 {
		t.Error("a push caused a request to GitLab")
	}
	if id.Access.WriteScope == "" || id.Access.ReadOnly {
		t.Errorf("access %+v, want the default branch scope", id.Access)
	}
}

func TestWithoutAJobTokenMergeRequestsAreKeyedBySourceBranch(t *testing.T) {
	f := newGitlabFixture(t)

	a, errA := f.v.Authenticate(t.Context(), f.mergeRequestToken(t, gitlabProject, "700", "feature/x"))
	b, errB := f.v.Authenticate(t.Context(), f.mergeRequestToken(t, gitlabProject, "701", "feature/x"))

	if errA != nil || errB != nil || a.Access.WriteScope != b.Access.WriteScope || a.Access.WriteScope == "" {
		t.Fatalf("got (%+v, %v) and (%+v, %v): merge requests of one branch share a scope without a lookup", a, errA, b, errB)
	}
	if f.api.Calls.Load() != 0 {
		t.Error("GitLab was called by a plain authentication")
	}
}

func TestOtherForgesAreNeverLookedUp(t *testing.T) {
	tf := oidctest.New(t) // a GitHub forge
	v := NewVerifier(tf.Config())
	verified, err := v.Verify(t.Context(), tf.Token(t, nil))
	if err != nil {
		t.Fatal(err)
	}

	if got := v.WithMergeRequest(t.Context(), verified, "any"); got != verified {
		t.Errorf("a GitHub token changed: %+v", got)
	}
}

func TestAForkMergeRequestIsStillRefused(t *testing.T) {
	f := newGitlabFixture(t)
	f.api.AddMergeRequest(gitlabProject, "700", 12, "feature/x", "main")
	// project_id is the fork; job_project_id would be the allowed project.
	raw, err := f.tf.MintGitLab(devforge.GitLabToken{
		Audience: oidctest.Audience, ProjectID: "999", ProjectPath: "fork/app",
		Ref: "feature/x", PipelineSource: "merge_request_event", PipelineID: "700", UserLogin: "mallory",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.v.Authenticate(t.Context(), raw); !errors.Is(err, auth.ErrRepoNotAllowed) {
		t.Errorf("error = %v, want ErrRepoNotAllowed", err)
	}
}
