// Claim adapter for GitLab CI ID tokens.
// Maps pipeline_source and the plain-name ref to a scope event; merge requests are keyed by source branch.
package adapters

import (
	"encoding/json"
	"errors"
	"fmt"

	"nx-caching-server/internal/scope"
)

type gitlabClaims struct {
	ProjectID      flexString `json:"project_id"`
	ProjectPath    string     `json:"project_path"`
	Ref            string     `json:"ref"`
	RefType        string     `json:"ref_type"`
	PipelineSource string     `json:"pipeline_source"`
	PipelineID     flexString `json:"pipeline_id"`
	UserLogin      string     `json:"user_login"`
}

// gitlabAdapter reads ref as a plain branch name. A merge request has no number
// or target claim, and its project_id is the source project, so a fork fails the allowlist.
// Do not read job_project_id instead: it is the parent of a fork, which would let a fork in.
func gitlabAdapter(payload []byte) (Claims, error) {
	var raw gitlabClaims
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Claims{}, fmt.Errorf("claims: %w", err)
	}
	if raw.ProjectID == "" {
		return Claims{}, errors.New("claims: project_id is missing")
	}

	claims := Claims{
		RepoID:   string(raw.ProjectID),
		RepoPath: raw.ProjectPath,
		Actor:    raw.UserLogin,
		Ref:      raw.Ref,
		Event:    raw.PipelineSource,
		Scope:    scope.Untrusted(),
	}

	if raw.RefType != "branch" || raw.Ref == "" {
		return claims, nil
	}

	switch raw.PipelineSource {
	case "push", "web", "api", "schedule":
		claims.Scope = scope.Push(raw.Ref, raw.UserLogin)
	case "merge_request_event":
		// ref is the source branch of the merge request.
		claims.Scope = scope.PullRequestBranch(raw.Ref, "", raw.UserLogin)
		claims.PipelineID = string(raw.PipelineID)
	}

	return claims, nil
}
