// Tests the scenario mapping of every forge: which event, ref and subject each situation produces.
// Claims are checked on the decoded token; signature checks live in the other tests.
package devforge

import (
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func scenarioClaims(t *testing.T, forgeType string, sc Scenario, job Job) jwt.MapClaims {
	t.Helper()

	signer, err := DefaultSigner("http://127.0.0.1:9100/oidc")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signer.MintScenario(forgeType, sc, job)
	if err != nil {
		t.Fatalf("MintScenario: %v", err)
	}

	parsed, _, err := jwt.NewParser().ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Claims.(jwt.MapClaims)
}

var testJob = Job{Audience: "nx-cache", RepoID: "123", RepoPath: "org/app", Actor: "louis"}

func TestMintScenario(t *testing.T) {
	tests := []struct {
		name      string
		forgeType string
		scenario  Scenario
		want      map[string]any
	}{
		{"github push", "github", Scenario{Kind: ScenarioPush, Branch: "main"},
			map[string]any{"event_name": "push", "ref": "refs/heads/main", "ref_type": "branch"}},
		{"github pr", "github", Scenario{Kind: ScenarioPR, Number: 42, Base: "main", Branch: "feature"},
			map[string]any{"event_name": "pull_request", "ref": "refs/pull/42/merge", "base_ref": "main"}},
		{"github tag", "github", Scenario{Kind: ScenarioTag, Tag: "v2.0.0"},
			map[string]any{"ref": "refs/tags/v2.0.0", "ref_type": "tag", "event_name": "push"}},
		{"github untrusted", "github", Scenario{Kind: ScenarioUntrusted, Base: "main"},
			map[string]any{"event_name": "pull_request_target", "ref": "refs/heads/main"}},

		{"gitlab push", "gitlab", Scenario{Kind: ScenarioPush, Branch: "main"},
			map[string]any{"pipeline_source": "push", "ref": "main", "ref_type": "branch", "namespace_path": "org"}},
		{"gitlab merge request", "gitlab", Scenario{Kind: ScenarioPR, Branch: "feature/x", Number: 9},
			map[string]any{"pipeline_source": "merge_request_event", "ref": "feature/x"}},
		{"gitlab tag", "gitlab", Scenario{Kind: ScenarioTag, Tag: "v1"},
			map[string]any{"ref": "v1", "ref_type": "tag", "ref_path": "refs/tags/v1"}},
		{"gitlab untrusted", "gitlab", Scenario{Kind: ScenarioUntrusted, Branch: "main"},
			map[string]any{"pipeline_source": "trigger"}},

		{"forgejo push", "forgejo", Scenario{Kind: ScenarioPush, Branch: "main"},
			map[string]any{"event_name": "push", "ref": "refs/heads/main", "repository_id": float64(123), "repository_owner": "org"}},
		{"forgejo pr", "forgejo", Scenario{Kind: ScenarioPR, Number: 7, Base: "release/1.0"},
			map[string]any{"event_name": "pull_request", "ref": "refs/pull/7/head", "base_ref": "release/1.0", "sub": "repo:org/app:pull_request"}},
		{"forgejo tag", "forgejo", Scenario{Kind: ScenarioTag, Tag: "v3"},
			map[string]any{"ref": "refs/tags/v3", "ref_type": "tag"}},
		{"forgejo untrusted", "forgejo", Scenario{Kind: ScenarioUntrusted, Base: "main"},
			map[string]any{"event_name": "pull_request_target", "ref": "refs/heads/main"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := scenarioClaims(t, tt.forgeType, tt.scenario, testJob)

			for key, want := range tt.want {
				if claims[key] != want {
					t.Errorf("%s = %v, want %v", key, claims[key], want)
				}
			}
			if claims["aud"] != "nx-cache" {
				t.Errorf("aud = %v", claims["aud"])
			}
		})
	}
}

func TestMintScenarioJobFields(t *testing.T) {
	job := Job{Audience: "a", RepoID: "5", RepoPath: "org/app", Actor: "bot", Protected: true}

	gitlab := scenarioClaims(t, "gitlab", Scenario{Kind: ScenarioPush, Branch: "main"}, job)
	forgejo := scenarioClaims(t, "forgejo", Scenario{Kind: ScenarioPush, Branch: "main"}, job)

	if gitlab["user_login"] != "bot" || gitlab["ref_protected"] != "true" {
		t.Errorf("gitlab claims = %v", gitlab)
	}
	if forgejo["actor"] != "bot" || forgejo["ref_protected"] != true {
		t.Errorf("forgejo claims = %v", forgejo)
	}
}

func TestMintScenarioErrors(t *testing.T) {
	signer, err := DefaultSigner("http://x/oidc")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		forgeType string
		scenario  Scenario
		job       Job
		want      string
	}{
		{"unknown forge", "bitbucket", Scenario{Kind: ScenarioPush, Branch: "main"}, testJob, "unknown forge"},
		{"unknown scenario", "github", Scenario{Kind: "release"}, testJob, "unknown scenario"},
		{"empty scenario", "github", Scenario{}, testJob, "unknown scenario"},
		{"forgejo with a text id", "forgejo", Scenario{Kind: ScenarioPush, Branch: "main"}, Job{RepoID: "abc"}, "must be a number"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := signer.MintScenario(tt.forgeType, tt.scenario, tt.job)

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got (%q, %v), want an error mentioning %q", got, err, tt.want)
			}
		})
	}
}

func TestParseScenarioKind(t *testing.T) {
	for _, name := range []string{"push", "pr", "tag", "untrusted"} {
		if kind, err := ParseScenarioKind(name); err != nil || string(kind) != name {
			t.Errorf("ParseScenarioKind(%q) = (%q, %v)", name, kind, err)
		}
	}
	for _, name := range []string{"", "Push", "release"} {
		if _, err := ParseScenarioKind(name); err == nil {
			t.Errorf("ParseScenarioKind(%q) should fail", name)
		}
	}
}
