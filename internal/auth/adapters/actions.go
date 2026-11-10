// Claim adapter for forges running GitHub Actions style workflows: GitHub and Forgejo.
// Both name the claims alike; names come from their docs and discovery documents.
package adapters

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"nx-caching-server/internal/scope"
)

var pullRefPattern = regexp.MustCompile(`^refs/pull/([1-9][0-9]{0,17})/(?:merge|head)$`)

type actionsClaims struct {
	Repository   string     `json:"repository"`
	RepositoryID flexString `json:"repository_id"`
	Ref          string     `json:"ref"`
	EventName    string     `json:"event_name"`
	BaseRef      string     `json:"base_ref"`
	HeadRef      string     `json:"head_ref"`
	Actor        string     `json:"actor"`
}

// actionsAdapter treats push-like events on a branch as pushes, same-repository
// pull requests as pull requests, and every other event (tags,
// pull_request_target, workflow_run, ...) as untrusted.
//
// No forge reports the pull request number as a claim. It is read from ref
// (refs/pull/N/merge) when the ref has that shape, otherwise the source branch
// (head_ref) identifies the pull request.
func actionsAdapter(payload []byte) (Claims, error) {
	var raw actionsClaims
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Claims{}, fmt.Errorf("claims: %w", err)
	}
	if raw.RepositoryID == "" {
		return Claims{}, errors.New("claims: repository_id is missing")
	}

	claims := Claims{
		RepoID:   string(raw.RepositoryID),
		RepoPath: raw.Repository,
		Actor:    raw.Actor,
		Ref:      raw.Ref,
		Event:    raw.EventName,
	}

	switch raw.EventName {
	case "push", "workflow_dispatch", "schedule":
		branch, ok := strings.CutPrefix(raw.Ref, "refs/heads/")
		if !ok || branch == "" {
			claims.Scope = scope.Untrusted()
			return claims, nil
		}
		claims.Scope = scope.Push(branch, raw.Actor)

	case "pull_request":
		if match := pullRefPattern.FindStringSubmatch(raw.Ref); match != nil {
			number, err := strconv.Atoi(match[1])
			if err != nil {
				return Claims{}, fmt.Errorf("claims: %w", err)
			}
			claims.Scope = scope.PullRequestNumber(number, raw.BaseRef, raw.Actor)
		} else if raw.HeadRef != "" {
			claims.Scope = scope.PullRequestBranch(raw.HeadRef, raw.BaseRef, raw.Actor)
		} else {
			return Claims{}, fmt.Errorf("claims: ref %q is not a pull request ref and head_ref is empty", raw.Ref)
		}

	default:
		claims.Scope = scope.Untrusted()
	}

	return claims, nil
}
