// Tests the devforge command line: scenarios per forge, config defaults, overrides and errors.
// Tokens are decoded without verification, only their claims matter here.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oauth2-proxy/mockoidc"
)

func runToken(t *testing.T, args ...string) (map[string]any, string, error) {
	t.Helper()
	t.Setenv("AUTH_CONFIG", "")

	var stdout, stderr bytes.Buffer
	err := run(append([]string{"token"}, args...), &stdout, &stderr)
	if err != nil {
		return nil, stderr.String(), err
	}

	raw := strings.TrimSpace(stdout.String())
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("output %q is not a JWT", raw)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]any{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	return claims, raw, nil
}

func TestTokenScenarios(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want map[string]any
	}{
		{"github push by default", []string{"github"}, map[string]any{
			"iss": "http://127.0.0.1:9100/oidc", "aud": "nx-cache", "repository_id": "123456",
			"repository": "my-org/app", "ref": "refs/heads/main", "event_name": "push", "actor": "dev",
		}},
		{"github push on a branch", []string{"github", "push", "--branch", "release/2.0"}, map[string]any{
			"ref": "refs/heads/release/2.0", "event_name": "push",
		}},
		{"github pull request", []string{"github", "pr", "--number", "42", "--base", "release/1.0"}, map[string]any{
			"ref": "refs/pull/42/merge", "event_name": "pull_request", "base_ref": "release/1.0",
		}},
		{"github tag", []string{"github", "tag", "--tag", "v2.0.0"}, map[string]any{
			"ref": "refs/tags/v2.0.0", "ref_type": "tag", "event_name": "push",
		}},
		{"github untrusted", []string{"github", "untrusted"}, map[string]any{
			"event_name": "pull_request_target", "ref": "refs/heads/main",
		}},
		{"gitlab push", []string{"gitlab"}, map[string]any{
			"project_id": "123456", "project_path": "my-org/app", "ref": "main", "ref_type": "branch",
			"pipeline_source": "push", "namespace_path": "my-org", "user_login": "dev",
		}},
		{"gitlab merge request from a source branch", []string{"gitlab", "pr", "--branch", "feature/login"}, map[string]any{
			"ref": "feature/login", "pipeline_source": "merge_request_event",
		}},
		{"gitlab merge request default source branch", []string{"gitlab", "pr"}, map[string]any{
			"ref": "feature", "pipeline_source": "merge_request_event",
		}},
		{"gitlab tag", []string{"gitlab", "tag"}, map[string]any{
			"ref": "v1.0.0", "ref_type": "tag", "ref_path": "refs/tags/v1.0.0",
		}},
		{"gitlab untrusted", []string{"gitlab", "untrusted"}, map[string]any{"pipeline_source": "trigger"}},
		{"gitlab protected", []string{"gitlab", "--protected"}, map[string]any{"ref_protected": "true"}},
		{"forgejo push with numeric ids", []string{"forgejo"}, map[string]any{
			"repository_id": float64(123456), "ref": "refs/heads/main", "event_name": "push",
		}},
		{"forgejo pull request", []string{"forgejo", "pr", "--number", "7"}, map[string]any{
			"ref": "refs/pull/7/head", "event_name": "pull_request", "base_ref": "main",
		}},
		{"forgejo untrusted", []string{"forgejo", "untrusted"}, map[string]any{"event_name": "pull_request_target"}},
		{"actor and repository overrides", []string{"github", "--actor", "louis", "--repo", "acme/api", "--repo-id", "9"}, map[string]any{
			"actor": "louis", "repository": "acme/api", "repository_id": "9",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, _, err := runToken(t, tt.args...)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for key, want := range tt.want {
				if claims[key] != want {
					t.Errorf("%s = %v, want %v", key, claims[key], want)
				}
			}
		})
	}
}

func TestTokenIsSignedWithTheServeKey(t *testing.T) {
	_, raw, err := runToken(t, "github")
	if err != nil {
		t.Fatal(err)
	}

	keypair, err := mockoidc.DefaultKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keypair.VerifyJWT(raw, time.Now); err != nil {
		t.Errorf("token does not verify with the key `serve` publishes: %v", err)
	}
}

func TestTokenTTL(t *testing.T) {
	claims, _, err := runToken(t, "github", "--ttl", "90s")
	if err != nil {
		t.Fatal(err)
	}

	if got := claims["exp"].(float64) - claims["iat"].(float64); got != 90 {
		t.Errorf("lifetime = %v seconds, want 90", got)
	}
}

const configFile = `
forges:
  - name: hub
    type: github
    issuer: http://127.0.0.1:9100/oidc
    audience: from-config
    repos:
      - id: "777"
        name: acme/service
        default_branch: develop
  - name: lab
    type: gitlab
    issuer: http://localhost:9999/oidc
    audience: lab-audience
    repos:
      - id: "55"
        name: group/project
        default_branch: trunk
`

func writeConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.yaml")
	if err := os.WriteFile(path, []byte(configFile), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTokenReadsTheConfig(t *testing.T) {
	path := writeConfig(t)

	github, _, err := runToken(t, "github", "--config", path)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"aud": "from-config", "repository_id": "777", "repository": "acme/service", "ref": "refs/heads/develop",
	} {
		if github[key] != want {
			t.Errorf("github %s = %v, want %v", key, github[key], want)
		}
	}

	gitlab, _, err := runToken(t, "gitlab", "--config", path)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"iss": "http://localhost:9999/oidc", "aud": "lab-audience", "project_id": "55", "ref": "trunk",
	} {
		if gitlab[key] != want {
			t.Errorf("gitlab %s = %v, want %v", key, gitlab[key], want)
		}
	}
}

func TestTokenRefusesAnIncompleteConfigEntry(t *testing.T) {
	tests := []struct {
		name  string
		entry string
		want  string
	}{
		{"no issuer", "type: github\n    audience: a\n    repos:\n      - id: \"1\"\n        default_branch: main", "issuer is missing"},
		{"no audience", "type: github\n    issuer: http://x/oidc\n    repos:\n      - id: \"1\"\n        default_branch: main", "audience is missing"},
		{"no repository", "type: github\n    issuer: http://x/oidc\n    audience: a", "no repository"},
		{"no repository id", "type: github\n    issuer: http://x/oidc\n    audience: a\n    repos:\n      - default_branch: main", "id and a default_branch"},
		{"no default branch", "type: github\n    issuer: http://x/oidc\n    audience: a\n    repos:\n      - id: \"1\"", "id and a default_branch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.yaml")
			if err := os.WriteFile(path, []byte("forges:\n  - name: hub\n    "+tt.entry+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			_, _, err := runToken(t, "github", "--config", path)

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestTokenRejectsAConfigTypo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.yaml")
	content := "forges:\n  - name: hub\n    type: github\n    issuer: http://x/oidc\n    audiance: a\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := runToken(t, "github", "--config", path)

	if err == nil || !strings.Contains(err.Error(), "audiance") {
		t.Errorf("error = %v, want it to name the unknown key", err)
	}
}

func TestTokenConfigWithoutARepositoryNameUsesTheDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.yaml")
	content := "forges:\n  - name: hub\n    type: github\n    issuer: http://x/oidc\n    audience: a\n    repos:\n      - id: \"1\"\n        default_branch: main\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	claims, _, err := runToken(t, "github", "--config", path)
	if err != nil {
		t.Fatal(err)
	}

	if claims["repository"] != "my-org/app" || claims["repository_id"] != "1" {
		t.Errorf("claims = %v", claims)
	}
}

func TestTokenConfigFromEnvironment(t *testing.T) {
	path := writeConfig(t)

	var stdout, stderr bytes.Buffer
	t.Setenv("AUTH_CONFIG", path)
	if err := run([]string{"token", "github"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	payload, err := base64.RawURLEncoding.DecodeString(strings.Split(strings.TrimSpace(stdout.String()), ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"aud":"from-config"`) {
		t.Errorf("AUTH_CONFIG was not used: %s", payload)
	}
}

func TestTokenFlagsBeatTheConfig(t *testing.T) {
	path := writeConfig(t)

	claims, _, err := runToken(t, "github", "--config", path, "--audience", "override", "--issuer", "http://x/oidc", "--repo-id", "1")
	if err != nil {
		t.Fatal(err)
	}

	if claims["aud"] != "override" || claims["iss"] != "http://x/oidc" || claims["repository_id"] != "1" {
		t.Errorf("claims = %v", claims)
	}
}

func TestTokenErrors(t *testing.T) {
	path := writeConfig(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no forge", nil, "FORGE"},
		{"too many arguments", []string{"github", "push", "extra"}, "FORGE"},
		{"unknown forge", []string{"bitbucket"}, "unknown forge"},
		{"unknown scenario", []string{"github", "release"}, "unknown scenario"},
		{"forge missing from the config", []string{"forgejo", "--config", path}, "no forge of type"},
		{"missing config file", []string{"github", "--config", filepath.Join(t.TempDir(), "absent.yaml")}, "absent.yaml"},
		{"forgejo needs a numeric id", []string{"forgejo", "--repo-id", "abc"}, "must be a number"},
		{"single dash flag", []string{"github", "-pr"}, "shorthand"},
		{"unknown flag", []string{"github", "--nope"}, "unknown flag"},
		{"invalid number", []string{"github", "--number", "x"}, "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, err := runToken(t, tt.args...)

			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error()+stderr, tt.want) {
				t.Errorf("error %q (stderr %q) does not mention %q", err, stderr, tt.want)
			}
		})
	}
}

func TestTokenHelpIsNotAnError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if err := run([]string{"token", "--help"}, &stdout, &stderr); err != nil {
		t.Errorf("--help returned %v", err)
	}
	if !strings.Contains(stderr.String(), "--branch") {
		t.Errorf("usage does not list the flags: %q", stderr.String())
	}
}

func TestRunCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if err := run([]string{"help"}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "devforge token FORGE") {
		t.Errorf("help: err = %v, stdout = %q", err, stdout.String())
	}
	if err := run(nil, &stdout, &stderr); err == nil {
		t.Error("expected an error without a command")
	}
	if err := run([]string{"nope"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("unknown command: err = %v", err)
	}
}
