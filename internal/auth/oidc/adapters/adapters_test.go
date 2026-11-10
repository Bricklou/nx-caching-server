// Tests the claim adapters on raw payloads: event mapping, id types and missing claims.
// No signature or network involved, only the mapping from claims to a scope event.
package adapters

import (
	"reflect"
	"strings"
	"testing"

	"nx-caching-server/internal/scope"
)

func TestActionsAdapter(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    Claims
	}{
		{
			name:    "push",
			payload: `{"repository_id":"1","repository":"o/r","ref":"refs/heads/main","event_name":"push","actor":"a"}`,
			want: Claims{RepoID: "1", RepoPath: "o/r", Actor: "a", Ref: "refs/heads/main", Event: "push",
				Scope: scope.Event{Kind: scope.KindPush, Branch: "main", Actor: "a"}},
		},
		{
			name:    "numeric repository id",
			payload: `{"repository_id":456,"ref":"refs/heads/main","event_name":"push"}`,
			want: Claims{RepoID: "456", Ref: "refs/heads/main", Event: "push",
				Scope: scope.Event{Kind: scope.KindPush, Branch: "main"}},
		},
		{
			name:    "branch with slashes",
			payload: `{"repository_id":"1","ref":"refs/heads/release/2.0","event_name":"workflow_dispatch"}`,
			want: Claims{RepoID: "1", Ref: "refs/heads/release/2.0", Event: "workflow_dispatch",
				Scope: scope.Event{Kind: scope.KindPush, Branch: "release/2.0"}},
		},
		{
			name:    "pull request number from the ref",
			payload: `{"repository_id":"1","ref":"refs/pull/42/merge","event_name":"pull_request","base_ref":"main","head_ref":"feature"}`,
			want: Claims{RepoID: "1", Ref: "refs/pull/42/merge", Event: "pull_request",
				Scope: scope.Event{Kind: scope.KindPullRequest, PRNumber: 42, BaseBranch: "main"}},
		},
		{
			name:    "pull request head ref shape",
			payload: `{"repository_id":"1","ref":"refs/pull/7/head","event_name":"pull_request","base_ref":"main"}`,
			want: Claims{RepoID: "1", Ref: "refs/pull/7/head", Event: "pull_request",
				Scope: scope.Event{Kind: scope.KindPullRequest, PRNumber: 7, BaseBranch: "main"}},
		},
		{
			name:    "pull request falls back to the source branch",
			payload: `{"repository_id":"1","ref":"refs/something/else","event_name":"pull_request","base_ref":"main","head_ref":"feature/x"}`,
			want: Claims{RepoID: "1", Ref: "refs/something/else", Event: "pull_request",
				Scope: scope.Event{Kind: scope.KindPullRequest, PRSourceBranch: "feature/x", BaseBranch: "main"}},
		},
		{
			name:    "tag",
			payload: `{"repository_id":"1","ref":"refs/tags/v1","event_name":"push","ref_type":"tag"}`,
			want:    Claims{RepoID: "1", Ref: "refs/tags/v1", Event: "push", Scope: scope.Event{Kind: scope.KindUntrusted}},
		},
		{
			name:    "pull_request_target",
			payload: `{"repository_id":"1","ref":"refs/heads/main","event_name":"pull_request_target"}`,
			want:    Claims{RepoID: "1", Ref: "refs/heads/main", Event: "pull_request_target", Scope: scope.Event{Kind: scope.KindUntrusted}},
		},
		{
			name:    "unknown event",
			payload: `{"repository_id":"1","ref":"refs/heads/main","event_name":"workflow_run"}`,
			want:    Claims{RepoID: "1", Ref: "refs/heads/main", Event: "workflow_run", Scope: scope.Event{Kind: scope.KindUntrusted}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := actionsAdapter([]byte(tt.payload))

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestActionsAdapterErrors(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{"not json", `nope`, "claims"},
		{"missing repository id", `{"ref":"refs/heads/main","event_name":"push"}`, "repository_id"},
		{"empty repository id", `{"repository_id":"","event_name":"push"}`, "repository_id"},
		{"repository id of the wrong type", `{"repository_id":{"x":1}}`, "claims"},
		{"pull request without ref shape or head ref", `{"repository_id":"1","ref":"refs/heads/main","event_name":"pull_request"}`, "pull request ref"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := actionsAdapter([]byte(tt.payload))

			if err == nil {
				t.Fatalf("expected an error, got %+v", got)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestGitlabAdapter(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    Claims
	}{
		{
			name:    "push",
			payload: `{"project_id":"20","project_path":"g/p","ref":"main","ref_type":"branch","pipeline_source":"push","user_login":"u"}`,
			want: Claims{RepoID: "20", RepoPath: "g/p", Actor: "u", Ref: "main", Event: "push",
				Scope: scope.Event{Kind: scope.KindPush, Branch: "main", Actor: "u"}},
		},
		{
			name:    "branch with slashes is a plain name",
			payload: `{"project_id":"20","ref":"release/2.0","ref_type":"branch","pipeline_source":"web"}`,
			want: Claims{RepoID: "20", Ref: "release/2.0", Event: "web",
				Scope: scope.Event{Kind: scope.KindPush, Branch: "release/2.0"}},
		},
		{
			name:    "numeric project id",
			payload: `{"project_id":20,"ref":"main","ref_type":"branch","pipeline_source":"schedule"}`,
			want: Claims{RepoID: "20", Ref: "main", Event: "schedule",
				Scope: scope.Event{Kind: scope.KindPush, Branch: "main"}},
		},
		{
			name:    "merge request is keyed by its source branch",
			payload: `{"project_id":"20","ref":"feature/x","ref_type":"branch","pipeline_source":"merge_request_event","user_login":"u"}`,
			want: Claims{RepoID: "20", Actor: "u", Ref: "feature/x", Event: "merge_request_event",
				Scope: scope.Event{Kind: scope.KindPullRequest, PRSourceBranch: "feature/x", Actor: "u"}},
		},
		{
			name:    "tag",
			payload: `{"project_id":"20","ref":"v1.0.0","ref_type":"tag","pipeline_source":"push"}`,
			want:    Claims{RepoID: "20", Ref: "v1.0.0", Event: "push", Scope: scope.Event{Kind: scope.KindUntrusted}},
		},
		{
			name:    "other pipeline sources are untrusted",
			payload: `{"project_id":"20","ref":"main","ref_type":"branch","pipeline_source":"trigger"}`,
			want:    Claims{RepoID: "20", Ref: "main", Event: "trigger", Scope: scope.Event{Kind: scope.KindUntrusted}},
		},
		{
			name:    "missing ref type is untrusted",
			payload: `{"project_id":"20","ref":"main","pipeline_source":"push"}`,
			want:    Claims{RepoID: "20", Ref: "main", Event: "push", Scope: scope.Event{Kind: scope.KindUntrusted}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gitlabAdapter([]byte(tt.payload))

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestGitlabAdapterErrors(t *testing.T) {
	for name, payload := range map[string]string{
		"not json":           `nope`,
		"missing project id": `{"ref":"main","ref_type":"branch","pipeline_source":"push"}`,
		"empty project id":   `{"project_id":"","ref":"main"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := gitlabAdapter([]byte(payload)); err == nil {
				t.Errorf("expected an error, got %+v", got)
			}
		})
	}
}
