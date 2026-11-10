// ABOUTME: Tests validation of forges, repositories and branch rules.
// ABOUTME: Each case starts from a valid forge and breaks one field.
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
	if errs := validForge().validate(); len(errs) != 0 {
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

			errs := forge.validate()

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

			if errs := forge.validate(); len(errs) != 0 {
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

	got := joined(forge.validate())

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
