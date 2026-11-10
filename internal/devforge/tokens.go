// Mints OIDC tokens shaped like the ones GitHub Actions, GitLab CI and Forgejo Actions issue.
// Claim names follow each forge's documentation and discovery document, not captured real tokens.
package devforge

import (
	"fmt"
	"maps"
	"strconv"
	"time"
)

// MintGitHub signs a GitHub-shaped token issued by this forge.
func (f *Forge) MintGitHub(t GitHubToken) (string, error) { return f.Signer().MintGitHub(t) }

// MintGitLab signs a GitLab-shaped token issued by this forge.
func (f *Forge) MintGitLab(t GitLabToken) (string, error) { return f.Signer().MintGitLab(t) }

// MintForgejo signs a Forgejo-shaped token issued by this forge.
func (f *Forge) MintForgejo(t ForgejoToken) (string, error) { return f.Signer().MintForgejo(t) }

// registered returns the claims every token carries, whatever the forge.
func (s *Signer) registered(aud, sub string, now time.Time, ttl time.Duration) map[string]any {
	if now.IsZero() {
		now = s.now()
	}
	if ttl == 0 {
		ttl = DefaultTTL
	}
	return map[string]any{
		"iss": s.issuer,
		"aud": aud,
		"sub": sub,
		"iat": now.Unix(),
		"nbf": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// GitHubToken describes a GitHub Actions token. repository_id is emitted as a
// string, since the GitHub docs do not state its type.
type GitHubToken struct {
	Audience  string
	RepoID    string
	Repo      string // owner/name
	Ref       string // full ref, e.g. refs/heads/main or refs/pull/42/merge
	RefType   string // branch or tag, branch when empty
	EventName string // push, pull_request, ...
	BaseRef   string // pull requests only
	Actor     string
	Now       time.Time // zero means the signer's clock
	TTL       time.Duration
}

// MintGitHub signs a GitHub-shaped token.
func (s *Signer) MintGitHub(t GitHubToken) (string, error) {
	claims := s.registered(t.Audience, fmt.Sprintf("repo:%s:ref:%s", t.Repo, t.Ref), t.Now, t.TTL)
	maps.Copy(claims, map[string]any{
		"repository":         t.Repo,
		"ref":                t.Ref,
		"ref_type":           orDefault(t.RefType, "branch"),
		"event_name":         t.EventName,
		"actor":              t.Actor,
		"base_ref":           t.BaseRef,
		"head_ref":           "",
		"workflow":           "ci",
		"runner_environment": "github-hosted",
	})
	if t.RepoID != "" {
		claims["repository_id"] = t.RepoID
	}
	return s.Mint(claims)
}

// GitLabToken describes a GitLab CI ID token. Ids are strings, ref is a plain
// branch name, and a merge request has no number or target claim.
type GitLabToken struct {
	Audience       string
	ProjectID      string
	ProjectPath    string // group/project
	NamespaceID    string
	NamespacePath  string // group
	Ref            string // branch or tag name, without refs/heads/
	RefType        string // branch or tag, branch when empty
	Protected      bool
	PipelineSource string // push, merge_request_event, schedule, ...
	UserLogin      string
	UserID         string
	Now            time.Time
	TTL            time.Duration
}

// MintGitLab signs a GitLab-shaped token.
func (s *Signer) MintGitLab(t GitLabToken) (string, error) {
	refType := orDefault(t.RefType, "branch")
	refPath := "refs/heads/" + t.Ref
	if refType == "tag" {
		refPath = "refs/tags/" + t.Ref
	}

	sub := fmt.Sprintf("project_path:%s:ref_type:%s:ref:%s", t.ProjectPath, refType, t.Ref)
	claims := s.registered(t.Audience, sub, t.Now, t.TTL)
	maps.Copy(claims, map[string]any{
		"jti": "00000000-0000-0000-0000-000000000000",

		"project_path":    t.ProjectPath,
		"namespace_id":    t.NamespaceID,
		"namespace_path":  t.NamespacePath,
		"user_id":         t.UserID,
		"user_login":      t.UserLogin,
		"pipeline_id":     "1",
		"pipeline_source": t.PipelineSource,
		"job_id":          "1",
		"ref":             t.Ref,
		"ref_type":        refType,
		"ref_path":        refPath,
		// The docs describe a boolean but their example shows a string.
		"ref_protected":      strconv.FormatBool(t.Protected),
		"sha":                "0000000000000000000000000000000000000000",
		"runner_environment": "self-hosted",
		"project_visibility": "private",
	})
	if t.ProjectID != "" {
		claims["project_id"] = t.ProjectID
	}
	return s.Mint(claims)
}

// ForgejoToken describes a Forgejo Actions ID token. Ids are numbers. The PR
// ref format is not documented; refs/pull/N/head is assumed.
type ForgejoToken struct {
	Audience  string
	RepoID    int64
	Repo      string // owner/name
	Owner     string
	OwnerID   int64
	Ref       string // full ref
	RefType   string // branch or tag, branch when empty
	Protected bool
	EventName string // push, pull_request, ...
	BaseRef   string // pull request events only
	Actor     string
	ActorID   int64
	Now       time.Time
	TTL       time.Duration
}

// MintForgejo signs a Forgejo-shaped token.
func (s *Signer) MintForgejo(t ForgejoToken) (string, error) {
	// The documented subject differs for pull requests.
	sub := fmt.Sprintf("repo:%s:ref:%s", t.Repo, t.Ref)
	if t.EventName == "pull_request" {
		sub = fmt.Sprintf("repo:%s:pull_request", t.Repo)
	}

	claims := s.registered(t.Audience, sub, t.Now, t.TTL)
	maps.Copy(claims, map[string]any{
		"actor":               t.Actor,
		"actor_id":            t.ActorID,
		"base_ref":            t.BaseRef,
		"event_name":          t.EventName,
		"ref":                 t.Ref,
		"ref_protected":       t.Protected,
		"ref_type":            orDefault(t.RefType, "branch"),
		"repository":          t.Repo,
		"repository_id":       t.RepoID,
		"repository_owner":    t.Owner,
		"repository_owner_id": t.OwnerID,
		"run_attempt":         1,
		"run_id":              1,
		"run_number":          1,
		"sha":                 "0000000000000000000000000000000000000000",
		"workflow":            "ci",
		"workflow_ref":        t.Repo + "/.forgejo/workflows/ci.yml@" + t.Ref,
	})
	return s.Mint(claims)
}
