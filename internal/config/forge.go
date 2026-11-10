package config

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
)

// ForgeTypeGitHub selects the GitHub Actions claim mapping.
const ForgeTypeGitHub = "github"

// ForgeTypeGitlab selects the Gitlab Job claim mapping.
const ForgeTypeGitlab = "gitlab"

// supportedForgeTypes lists the forge types that have a claim adapter.
var supportedForgeTypes = []string{ForgeTypeGitHub, ForgeTypeGitlab}

var (
	// forgeNamePattern keeps the name usable as a single path segment.
	forgeNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// repoIDPattern keeps the id usable as a single path segment.
	repoIDPattern = regexp.MustCompile(`^[0-9]{1,19}$`)
)

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

// validate returns every problem found in the forge, without a forge prefix.
func (f Forge) validate() []error {
	var errs []error

	if !forgeNamePattern.MatchString(f.Name) {
		errs = append(errs, fmt.Errorf("name %q must match %s", f.Name, forgeNamePattern))
	}
	if !slices.Contains(supportedForgeTypes, f.Type) {
		errs = append(errs, fmt.Errorf("type %q is not supported (supported: %s)", f.Type, strings.Join(supportedForgeTypes, ", ")))
	}
	if err := validateHTTPSURL(f.Issuer); err != nil {
		errs = append(errs, fmt.Errorf("issuer %w", err))
	}
	if f.Audience == "" {
		errs = append(errs, errors.New("audience is required"))
	}
	if f.JWKSURL != "" {
		if err := validateHTTPSURL(f.JWKSURL); err != nil {
			errs = append(errs, fmt.Errorf("jwks_url %w", err))
		}
	}

	if len(f.Repos) == 0 {
		errs = append(errs, errors.New("at least one repository is required"))
	}
	seen := make(map[string]bool, len(f.Repos))
	for i, repo := range f.Repos {
		for _, err := range repo.validate() {
			errs = append(errs, fmt.Errorf("repos[%d]: %w", i, err))
		}
		if repo.ID != "" && seen[repo.ID] {
			errs = append(errs, fmt.Errorf("repos[%d]: duplicate id %q", i, repo.ID))
		}
		seen[repo.ID] = true
	}

	return errs
}

func (r Repository) validate() []error {
	var errs []error

	if !repoIDPattern.MatchString(r.ID) {
		errs = append(errs, fmt.Errorf("id %q must match %s", r.ID, repoIDPattern))
	}
	if strings.TrimSpace(r.DefaultBranch) == "" {
		errs = append(errs, errors.New("default_branch is required"))
	}
	for i, rule := range r.BranchRules {
		for _, err := range rule.validate() {
			errs = append(errs, fmt.Errorf("branch_rules[%d]: %w", i, err))
		}
	}

	return errs
}

func (r BranchRule) validate() []error {
	var errs []error

	if !r.Mode.valid() {
		errs = append(errs, fmt.Errorf("unknown mode %q", r.Mode))
	}
	if len(r.Match) == 0 {
		errs = append(errs, errors.New("match needs at least one pattern"))
	}
	for _, pattern := range r.Match {
		if pattern == "" {
			errs = append(errs, errors.New("match contains an empty pattern"))
			continue
		}
		if _, err := path.Match(pattern, ""); err != nil {
			errs = append(errs, fmt.Errorf("invalid pattern %q: %w", pattern, err))
			continue
		}
		if r.Mode == BranchModeTrusted && matchesEverything(pattern) {
			errs = append(errs, fmt.Errorf("pattern %q would make every branch trusted", pattern))
		}
	}

	return errs
}

// matchesEverything reports whether the pattern is made only of stars.
func matchesEverything(pattern string) bool {
	return strings.Trim(pattern, "*") == ""
}

func validateHTTPSURL(raw string) error {
	if raw == "" {
		return errors.New("is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("is not a valid URL: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("must be an https URL with a host (got %q)", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must not contain credentials, a query or a fragment (got %q)", raw)
	}
	return nil
}
