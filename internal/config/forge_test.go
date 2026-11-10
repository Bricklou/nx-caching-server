// Tests validation of forges, repositories and branch rules.
// Each case starts from a valid forge and breaks one field.
package config

import (
	"errors"
	"strings"
	"testing"
)

func validForge() Forge {
	return Forge{
		Name:     "github",
		Type:     ForgeTypeGitHub,
		Issuer:   "https://token.actions.githubusercontent.com",
		Audience: "nx-cache",
		Repos: []Repository{{
			ID:            "123456",
			DefaultBranch: "main",
			BranchRules: []BranchRule{
				{Match: []string{"v*.*.x", "release/*"}, Mode: BranchModeTrusted},
				{Match: []string{"experiments/*"}, Mode: BranchModeReadOnly},
			},
		}},
	}
}

func joined(errs []error) string {
	return errors.Join(errs...).Error()
}

func TestForgeValidate(t *testing.T) {
	if errs := validForge().validate(ValidateOptions{}); len(errs) != 0 {
		t.Fatalf("valid forge rejected: %v", errs)
	}

	tests := []struct {
		name   string
		mutate func(*Forge)
		want   string
	}{
		{"empty name", func(f *Forge) { f.Name = "" }, "name"},
		{"name with slash", func(f *Forge) { f.Name = "git/hub" }, "name"},
		{"name with dots", func(f *Forge) { f.Name = ".." }, "name"},
		{"name too long", func(f *Forge) { f.Name = strings.Repeat("a", 65) }, "name"},
		{"unknown type", func(f *Forge) { f.Type = "bitbucket" }, "type"},
		{"empty type", func(f *Forge) { f.Type = "" }, "type"},
		{"missing issuer", func(f *Forge) { f.Issuer = "" }, "issuer"},
		{"http issuer", func(f *Forge) { f.Issuer = "http://forge.example.com" }, "issuer"},
		{"issuer without host", func(f *Forge) { f.Issuer = "https://" }, "issuer"},
		{"issuer with credentials", func(f *Forge) { f.Issuer = "https://user:pw@forge.example.com" }, "issuer"},
		{"issuer with query", func(f *Forge) { f.Issuer = "https://forge.example.com?x=1" }, "issuer"},
		{"issuer not a url", func(f *Forge) { f.Issuer = "::" }, "issuer"},
		{"missing audience", func(f *Forge) { f.Audience = "" }, "audience"},
		{"http jwks url", func(f *Forge) { f.JWKSURL = "http://forge.example.com/keys" }, "jwks_url"},
		{"no repositories", func(f *Forge) { f.Repos = nil }, "repository"},
		{"duplicate repository id", func(f *Forge) { f.Repos = append(f.Repos, f.Repos[0]) }, "duplicate id"},
		{"empty repository id", func(f *Forge) { f.Repos[0].ID = "" }, "id"},
		{"non numeric repository id", func(f *Forge) { f.Repos[0].ID = "my-org/app" }, "id"},
		{"repository id traversal", func(f *Forge) { f.Repos[0].ID = "../1" }, "id"},
		{"missing default branch", func(f *Forge) { f.Repos[0].DefaultBranch = "" }, "default_branch"},
		{"blank default branch", func(f *Forge) { f.Repos[0].DefaultBranch = "  " }, "default_branch"},
		{"unknown mode", func(f *Forge) { f.Repos[0].BranchRules[0].Mode = "maybe" }, "unknown mode"},
		{"empty mode", func(f *Forge) { f.Repos[0].BranchRules[0].Mode = "" }, "unknown mode"},
		{"rule without patterns", func(f *Forge) { f.Repos[0].BranchRules[0].Match = nil }, "match"},
		{"empty pattern", func(f *Forge) { f.Repos[0].BranchRules[0].Match = []string{""} }, "empty pattern"},
		{"invalid glob", func(f *Forge) { f.Repos[0].BranchRules[0].Match = []string{"release/["} }, "invalid pattern"},
		{"trusted star", func(f *Forge) { f.Repos[0].BranchRules[0].Match = []string{"*"} }, "every branch"},
		{"trusted double star", func(f *Forge) { f.Repos[0].BranchRules[0].Match = []string{"**"} }, "every branch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forge := validForge()
			tt.mutate(&forge)

			errs := forge.validate(ValidateOptions{})

			if len(errs) == 0 {
				t.Fatal("expected a validation error")
			}
			if got := joined(errs); !strings.Contains(got, tt.want) {
				t.Errorf("errors %q do not mention %q", got, tt.want)
			}
		})
	}
}

func TestForgeValidateAccepts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Forge)
	}{
		{"jwks url override", func(f *Forge) { f.JWKSURL = "https://forge.example.com/keys" }},
		{"issuer with path", func(f *Forge) { f.Issuer = "https://forge.example.com/api/actions" }},
		{"no branch rules", func(f *Forge) { f.Repos[0].BranchRules = nil }},
		{"star in non trusted rule", func(f *Forge) {
			f.Repos[0].BranchRules = []BranchRule{{Match: []string{"*"}, Mode: BranchModeDeny}}
		}},
		{"two repositories", func(f *Forge) {
			f.Repos = append(f.Repos, Repository{ID: "789", DefaultBranch: "develop"})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forge := validForge()
			tt.mutate(&forge)

			if errs := forge.validate(ValidateOptions{}); len(errs) != 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestForgeValidateReportsEveryProblem(t *testing.T) {
	forge := validForge()
	forge.Audience = ""
	forge.Repos[0].ID = "abc"
	forge.Repos[0].BranchRules[0].Mode = "maybe"

	got := joined(forge.validate(ValidateOptions{}))

	for _, want := range []string{"audience", "id", "unknown mode"} {
		if !strings.Contains(got, want) {
			t.Errorf("errors %q do not mention %q", got, want)
		}
	}
}

func TestBranchModeValid(t *testing.T) {
	valid := []BranchMode{
		BranchModeTrusted, BranchModeIsolated, BranchModeShared,
		BranchModePerActor, BranchModeReadOnly, BranchModeDeny,
	}
	for _, mode := range valid {
		if !mode.valid() {
			t.Errorf("%q should be valid", mode)
		}
	}
	for _, mode := range []BranchMode{"", "Trusted", "none"} {
		if mode.valid() {
			t.Errorf("%q should be invalid", mode)
		}
	}
}

func TestForgeValidateInsecureIssuer(t *testing.T) {
	insecure := ValidateOptions{AllowInsecureIssuer: true}

	tests := []struct {
		name     string
		issuer   string
		jwksURL  string
		strictOK bool
		devOK    bool
	}{
		{"https", "https://forge.example.com", "", true, true},
		{"http issuer", "http://127.0.0.1:9100", "", false, true},
		{"http jwks url", "https://forge.example.com", "http://127.0.0.1:9100/jwks", false, true},
		{"ftp issuer", "ftp://forge.example.com", "", false, false},
		{"http without host", "http://", "", false, false},
		{"http with credentials", "http://user:pw@127.0.0.1:9100", "", false, false},
		{"http with query", "http://127.0.0.1:9100?x=1", "", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forge := validForge()
			forge.Issuer = tt.issuer
			forge.JWKSURL = tt.jwksURL

			if ok := len(forge.validate(ValidateOptions{})) == 0; ok != tt.strictOK {
				t.Errorf("strict: accepted = %v, want %v", ok, tt.strictOK)
			}
			if ok := len(forge.validate(insecure)) == 0; ok != tt.devOK {
				t.Errorf("insecure allowed: accepted = %v, want %v", ok, tt.devOK)
			}
		})
	}
}

func TestRepositoryModeFor(t *testing.T) {
	repo := Repository{
		ID:            "1",
		DefaultBranch: "main",
		BranchRules: []BranchRule{
			{Match: []string{"main", "v*.*.x", "release/*"}, Mode: BranchModeDeny},
			{Match: []string{"v1.0.x"}, Mode: BranchModeReadOnly},
			{Match: []string{"wip/*"}, Mode: BranchModeShared},
		},
	}

	tests := []struct {
		branch   string
		wantMode BranchMode
		wantRule int
	}{
		{"main", BranchModeTrusted, -1},            // default branch wins over any rule
		{"v1.1.x", BranchModeDeny, 0},              // first matching rule wins
		{"v1.0.x", BranchModeDeny, 0},              // later rule never reached
		{"release/2.0", BranchModeDeny, 0},         // one path segment after the slash
		{"release/2.0/rc", BranchModeIsolated, -1}, // * does not cross a slash
		{"wip/login", BranchModeShared, 2},
		{"feature/login", BranchModeIsolated, -1},
		{"", BranchModeIsolated, -1},
	}

	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			mode, rule := repo.ModeFor(tt.branch)

			if mode != tt.wantMode || rule != tt.wantRule {
				t.Errorf("ModeFor(%q) = (%q, %d), want (%q, %d)", tt.branch, mode, rule, tt.wantMode, tt.wantRule)
			}
		})
	}
}

func TestForgeValidateAcceptsEverySupportedType(t *testing.T) {
	for _, forgeType := range []string{ForgeTypeGitHub, ForgeTypeGitlab, ForgeTypeForgejo} {
		forge := validForge()
		forge.Type = forgeType

		if errs := forge.validate(ValidateOptions{}); len(errs) != 0 {
			t.Errorf("type %q rejected: %v", forgeType, errs)
		}
	}
}
