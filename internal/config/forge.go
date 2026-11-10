package config

import "path"

// ForgeTypeGitHub selects the GitHub Actions claim mapping.
const ForgeTypeGitHub = "github"

// ForgeTypeGitlab selects the Gitlab Job claim mapping.
const ForgeTypeGitlab = "gitlab"

// ForgeTypeForgejo selects the Forgejo Actions claim mapping.
const ForgeTypeForgejo = "forgejo"

type Forge struct {
	Name     string       `yaml:"name"`
	Type     string       `yaml:"type"`
	Issuer   string       `yaml:"issuer"`
	Audience string       `yaml:"audience"`
	JWKSURL  string       `yaml:"jwks_url,omitempty"`
	Repos    []Repository `yaml:"repos"`
}

type Repository struct {
	ID            string `yaml:"id"`
	Name          string `yaml:"name,omitempty"`
	DefaultBranch string `yaml:"default_branch"`
	// BranchRules are tried in order, the first matching rule applies.
	BranchRules []BranchRule `yaml:"branch_rules,omitempty"`
}

// BranchRule says how pushes to branches matching Match are cached.
type BranchRule struct {
	// Match holds globs (path.Match syntax) tested against the branch name.
	Match []string   `yaml:"match"`
	Mode  BranchMode `yaml:"mode"`
}

// BranchMode is the cache behavior of a branch rule.
type BranchMode string

const (
	// BranchModeTrusted writes a shared scope that other branches may read.
	BranchModeTrusted BranchMode = "trusted"
	// BranchModeIsolated writes a scope private to the branch.
	BranchModeIsolated BranchMode = "isolated"
	// BranchModeShared writes one scope shared by all branches of the rule.
	BranchModeShared BranchMode = "shared"
	// BranchModePerActor writes one scope per pusher.
	BranchModePerActor BranchMode = "per_actor"
	// BranchModeReadOnly reads the trusted scopes but never writes.
	BranchModeReadOnly BranchMode = "read_only"
	// BranchModeDeny rejects the request.
	BranchModeDeny BranchMode = "deny"
)

func (m BranchMode) valid() bool {
	switch m {
	case BranchModeTrusted, BranchModeIsolated, BranchModeShared,
		BranchModePerActor, BranchModeReadOnly, BranchModeDeny:
		return true
	}
	return false
}

// ModeFor returns the mode applying to branch and the index of the matching
// rule, or -1 when the mode is implicit: the default branch is always
// trusted and unmatched branches are isolated.
func (r Repository) ModeFor(branch string) (BranchMode, int) {
	if branch == r.DefaultBranch {
		return BranchModeTrusted, -1
	}

	for i, rule := range r.BranchRules {
		for _, pattern := range rule.Match {
			if ok, _ := path.Match(pattern, branch); ok {
				return rule.Mode, i
			}
		}
	}
	return BranchModeIsolated, -1
}
