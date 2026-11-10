// ABOUTME: Tests loading of the AUTH_CONFIG YAML file.
// ABOUTME: Covers fail-closed cases: empty, null, multi-document and unknown-key files.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `
forges:
  - name: github
    type: github
    issuer: https://token.actions.githubusercontent.com
    audience: nx-cache
    repos:
      - id: "123456"
        name: my-org/app
        default_branch: main
        branch_rules:
          - match: ["release/*"]
            mode: trusted
`

func TestDecodeAuthConfig(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
		errText string
	}{
		{name: "valid", input: validConfig},
		{name: "empty file", input: "", wantErr: errEmptyAuthConfig},
		{name: "comments only", input: "# nothing here\n", wantErr: errEmptyAuthConfig},
		{name: "null document", input: "---\n", wantErr: errNoForges},
		{name: "no forges", input: "forges: []\n", wantErr: errNoForges},
		{name: "multiple documents", input: validConfig + "---\n" + validConfig, wantErr: errMultipleDocuments},
		{name: "unknown top-level key", input: "forgs: []\n", errText: "forgs"},
		{name: "unknown nested key", input: "forges:\n  - name: a\n    audiance: x\n", errText: "audiance"},
		{name: "invalid yaml", input: "forges: [", errText: "yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeAuthConfig(strings.NewReader(tt.input))

			if tt.wantErr == nil && tt.errText == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got == nil || len(got.Forges) != 1 {
					t.Fatalf("expected one forge, got %+v", got)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected an error, got config %+v", got)
			}
			if got != nil {
				t.Errorf("config must be nil on error, got %+v", got)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.errText != "" && !strings.Contains(err.Error(), tt.errText) {
				t.Errorf("error %q does not mention %q", err, tt.errText)
			}
		})
	}
}

func TestDecodeAuthConfigFields(t *testing.T) {
	got, err := decodeAuthConfig(strings.NewReader(validConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	forge := got.Forges[0]
	if forge.Name != "github" || forge.Type != "github" || forge.Audience != "nx-cache" {
		t.Errorf("unexpected forge: %+v", forge)
	}

	repo := forge.Repos[0]
	if repo.ID != "123456" || repo.DefaultBranch != "main" {
		t.Errorf("unexpected repo: %+v", repo)
	}
	rule := repo.BranchRules[0]
	if rule.Mode != BranchModeTrusted || len(rule.Match) != 1 || rule.Match[0] != "release/*" {
		t.Errorf("unexpected branch rule: %+v", rule)
	}
}

func TestDecodeAuthConfigNumericIDStaysString(t *testing.T) {
	input := strings.Replace(validConfig, `"123456"`, "123456", 1)

	got, err := decodeAuthConfig(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id := got.Forges[0].Repos[0].ID; id != "123456" {
		t.Errorf("id = %q, want 123456", id)
	}
}

func TestLoadAuthConfig(t *testing.T) {
	t.Run("unset means auth off", func(t *testing.T) {
		t.Setenv(authConfigKey, "")

		got, err := loadAuthConfig()

		if err != nil || got != nil {
			t.Errorf("got (%+v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("valid file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "auth.yaml")
		if err := os.WriteFile(path, []byte(validConfig), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(authConfigKey, path)

		got, err := loadAuthConfig()

		if err != nil || got == nil {
			t.Fatalf("got (%+v, %v)", got, err)
		}
	})

	t.Run("missing file fails and names the path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "absent.yaml")
		t.Setenv(authConfigKey, path)

		got, err := loadAuthConfig()

		if err == nil || got != nil {
			t.Fatalf("got (%+v, %v), want an error", got, err)
		}
		if !strings.Contains(err.Error(), path) {
			t.Errorf("error %q does not name %s", err, path)
		}
	})

	t.Run("empty file fails closed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "auth.yaml")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(authConfigKey, path)

		got, err := loadAuthConfig()

		if err == nil || got != nil {
			t.Fatalf("got (%+v, %v), want an error", got, err)
		}
		if !strings.Contains(err.Error(), path) {
			t.Errorf("error %q does not name %s", err, path)
		}
	})
}

func TestAuthConfigLogValue(t *testing.T) {
	var disabled *AuthConfig
	if got := disabled.LogValue().String(); got != "disabled" {
		t.Errorf("nil config logs %q, want disabled", got)
	}

	cfg := &AuthConfig{Forges: []Forge{{Name: "github", Audience: "secret-audience"}}}
	got := cfg.LogValue().String()
	if !strings.Contains(got, "github") || strings.Contains(got, "secret-audience") {
		t.Errorf("unexpected log value %q", got)
	}
}

func validAuthConfig() *AuthConfig {
	return &AuthConfig{Forges: []Forge{validForge()}}
}

func TestAuthConfigValidate(t *testing.T) {
	if err := validAuthConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	second := validForge()
	second.Name = "forgejo"
	second.Issuer = "https://forge.example.com/api/actions"
	if err := (&AuthConfig{Forges: []Forge{validForge(), second}}).Validate(); err != nil {
		t.Errorf("two distinct forges rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*AuthConfig)
		want   string
	}{
		{"duplicate name", func(c *AuthConfig) {
			dup := validForge()
			dup.Issuer = "https://other.example.com"
			c.Forges = append(c.Forges, dup)
		}, "duplicate name"},
		{"duplicate issuer", func(c *AuthConfig) {
			dup := validForge()
			dup.Name = "other"
			c.Forges = append(c.Forges, dup)
		}, "issuer already used"},
		{"invalid forge is located", func(c *AuthConfig) {
			c.Forges[0].Audience = ""
		}, `forges[0] ("github")`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validAuthConfig()
			tt.mutate(cfg)

			err := cfg.Validate()

			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestAuthConfigValidateNoForges(t *testing.T) {
	if err := (&AuthConfig{}).Validate(); !errors.Is(err, errNoForges) {
		t.Errorf("error = %v, want %v", err, errNoForges)
	}
}

func TestDecodeAuthConfigRunsValidation(t *testing.T) {
	input := strings.Replace(validConfig, "audience: nx-cache", "audience: \"\"", 1)

	got, err := decodeAuthConfig(strings.NewReader(input))

	if err == nil || got != nil {
		t.Fatalf("got (%+v, %v), want a validation error", got, err)
	}
	if !strings.Contains(err.Error(), "audience") {
		t.Errorf("error %q does not mention audience", err)
	}
}

func TestLoadAuthConfigReportsEachProblem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.yaml")
	content := "forges:\n  - name: gitlab\n    type: gitlab\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(authConfigKey, path)

	_, err := loadAuthConfig()

	var fileErr *FileError
	if !errors.As(err, &fileErr) {
		t.Fatalf("error %v is not a *FileError", err)
	}
	if fileErr.Path != path {
		t.Errorf("path = %q, want %q", fileErr.Path, path)
	}

	// type, issuer, audience and repositories are all wrong.
	if len(fileErr.Problems) != 4 {
		t.Fatalf("got %d problems, want 4: %v", len(fileErr.Problems), fileErr.Problems)
	}
	for _, problem := range fileErr.Problems {
		if strings.Contains(problem.Error(), "\n") {
			t.Errorf("problem %q spans several lines", problem)
		}
	}
	if !strings.Contains(fileErr.Problems[0].Error(), `type "gitlab" is not supported`) {
		t.Errorf("unexpected first problem: %v", fileErr.Problems[0])
	}
}

func TestLoadAuthConfigMissingFileMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")
	t.Setenv(authConfigKey, path)

	_, err := loadAuthConfig()

	var fileErr *FileError
	if !errors.As(err, &fileErr) || len(fileErr.Problems) != 1 {
		t.Fatalf("unexpected error %v", err)
	}
	if strings.Contains(fileErr.Problems[0].Error(), path) {
		t.Errorf("problem %q repeats the path", fileErr.Problems[0])
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error %v should wrap os.ErrNotExist", err)
	}
}

func TestFlatten(t *testing.T) {
	a, b, c := errors.New("a"), errors.New("b"), errors.New("c")

	got := flatten(errors.Join(a, errors.Join(b, c)))

	if len(got) != 3 || got[0] != a || got[1] != b || got[2] != c {
		t.Errorf("flatten = %v", got)
	}
	if got := flatten(a); len(got) != 1 || got[0] != a {
		t.Errorf("flatten of a plain error = %v", got)
	}
}
