// Validation of forges, repositories and branch rules, reporting every problem found.
// The types and the branch lookup live in forge.go.
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

// supportedForgeTypes lists the forge types that have a claim adapter.
var supportedForgeTypes = []string{ForgeTypeGitHub, ForgeTypeGitlab, ForgeTypeForgejo}

var (
	// forgeNamePattern keeps the name usable as a single path segment.
	forgeNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// repoIDPattern keeps the id usable as a single path segment.
	repoIDPattern = regexp.MustCompile(`^[0-9]{1,19}$`)
)

// validate returns every problem found in the forge, without a forge prefix.
func (f Forge) validate(opts ValidateOptions) []error {
	var errs []error

	if !forgeNamePattern.MatchString(f.Name) {
		errs = append(errs, fmt.Errorf("name %q must match %s", f.Name, forgeNamePattern))
	}
	if !slices.Contains(supportedForgeTypes, f.Type) {
		errs = append(errs, fmt.Errorf("type %q is not supported (supported: %s)", f.Type, strings.Join(supportedForgeTypes, ", ")))
	}
	if err := validateURL(f.Issuer, opts.AllowInsecureIssuer); err != nil {
		errs = append(errs, fmt.Errorf("issuer %w", err))
	}
	if f.Audience == "" {
		errs = append(errs, errors.New("audience is required"))
	}
	if f.JWKSURL != "" {
		if err := validateURL(f.JWKSURL, opts.AllowInsecureIssuer); err != nil {
			errs = append(errs, fmt.Errorf("jwks_url %w", err))
		}
	}

	if f.APIURL != "" {
		if f.Type != ForgeTypeGitlab {
			errs = append(errs, fmt.Errorf("api_url is only used by %s forges, not %q", ForgeTypeGitlab, f.Type))
		}
		if err := validateURL(f.APIURL, opts.AllowInsecureIssuer); err != nil {
			errs = append(errs, fmt.Errorf("api_url %w", err))
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

// validateURL requires https, or http as well when allowHTTP is set.
func validateURL(raw string, allowHTTP bool) error {
	if raw == "" {
		return errors.New("is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("is not a valid URL: %w", err)
	}
	schemeOK := u.Scheme == "https" || (allowHTTP && u.Scheme == "http")
	if !schemeOK || u.Host == "" {
		want := "an https URL"
		if allowHTTP {
			want = "an http or https URL"
		}
		return fmt.Errorf("must be %s with a host (got %q)", want, raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must not contain credentials, a query or a fragment (got %q)", raw)
	}
	return nil
}
