package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/pflag"

	"nx-caching-server/internal/config"
	"nx-caching-server/internal/devforge"
)

// Built-in development defaults, used when no auth config is available.
const (
	defaultAudience = "nx-cache"
	defaultRepoID   = "123456"
	defaultRepoPath = "my-org/app"
	defaultBranch   = "main"
	issuerPath      = "/oidc" // issuer path of mockoidc
)

// target is the forge, audience and repository a token is minted for.
type target struct {
	issuer        string
	audience      string
	repoID        string
	repoPath      string
	defaultBranch string
}

// options are the command line flags of `token`. Empty values mean "derive".
type options struct {
	config    string
	issuer    string
	audience  string
	repoID    string
	repoPath  string
	branch    string
	base      string
	tag       string
	number    int
	actor     string
	protected bool
	ttl       time.Duration
}

func token(args []string, stdout, stderr io.Writer) error {
	var opts options
	flags := pflag.NewFlagSet("token", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.config, "config", os.Getenv("AUTH_CONFIG"), "auth config file giving issuer, audience and repository (default $AUTH_CONFIG)")
	flags.StringVar(&opts.issuer, "issuer", "", "issuer URL, overrides the config")
	flags.StringVar(&opts.audience, "audience", "", "audience, overrides the config")
	flags.StringVar(&opts.repoID, "repo-id", "", "repository id, overrides the config")
	flags.StringVar(&opts.repoPath, "repo", "", "repository path owner/name, overrides the config")
	flags.StringVar(&opts.branch, "branch", "", "branch (source branch for pr), default: the repository default branch (feature for pr)")
	flags.StringVar(&opts.base, "base", "", "pull request target branch, default: the repository default branch")
	flags.StringVar(&opts.tag, "tag", "v1.0.0", "tag name for the tag scenario")
	flags.IntVar(&opts.number, "number", 1, "pull request number")
	flags.StringVar(&opts.actor, "actor", "dev", "user that triggered the job")
	flags.BoolVar(&opts.protected, "protected", false, "mark the ref as protected (gitlab and forgejo)")
	flags.DurationVar(&opts.ttl, "ttl", devforge.DefaultTTL, "token lifetime")
	flags.Usage = func() { fmt.Fprintln(stderr, usageText); flags.PrintDefaults() }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil
		}
		return err
	}

	positional := flags.Args()
	if len(positional) == 0 || len(positional) > 2 {
		flags.Usage()
		return errors.New("expected FORGE and an optional SCENARIO")
	}
	forgeType, scenario := positional[0], "push"
	if len(positional) == 2 {
		scenario = positional[1]
	}

	dest, err := resolveTarget(forgeType, opts)
	if err != nil {
		return err
	}
	signer, err := devforge.DefaultSigner(dest.issuer)
	if err != nil {
		return err
	}

	raw, err := mint(signer, forgeType, scenario, dest, opts)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, raw)
	return nil
}

// resolveTarget reads the first forge of forgeType from the auth config, then
// applies the flag overrides on top of it.
func resolveTarget(forgeType string, opts options) (target, error) {
	dest := target{
		issuer:        "http://" + defaultAddr + issuerPath,
		audience:      defaultAudience,
		repoID:        defaultRepoID,
		repoPath:      defaultRepoPath,
		defaultBranch: defaultBranch,
	}

	if opts.config != "" {
		fromConfig, err := targetFromConfig(opts.config, forgeType)
		if err != nil {
			return target{}, err
		}
		dest = fromConfig
	}

	if opts.issuer != "" {
		dest.issuer = opts.issuer
	}
	if opts.audience != "" {
		dest.audience = opts.audience
	}
	if opts.repoID != "" {
		dest.repoID = opts.repoID
	}
	if opts.repoPath != "" {
		dest.repoPath = opts.repoPath
	}
	return dest, nil
}

// targetFromConfig parses the file like the server but skips validation, which
// rejects the http issuer of a development config.
func targetFromConfig(path, forgeType string) (target, error) {
	file, err := os.Open(path)
	if err != nil {
		return target{}, err
	}
	defer file.Close()

	cfg, err := config.ParseAuthConfig(file)
	if err != nil {
		return target{}, fmt.Errorf("%s: %w", path, err)
	}

	for _, forge := range cfg.Forges {
		if forge.Type != forgeType {
			continue
		}
		return targetFromForge(path, forge)
	}
	return target{}, fmt.Errorf("%s has no forge of type %q", path, forgeType)
}

// targetFromForge refuses a forge entry with a missing field, since a token
// built from it would be rejected by the server for a reason hard to guess.
func targetFromForge(path string, forge config.Forge) (target, error) {
	where := fmt.Sprintf("%s, forge %q", path, forge.Name)
	if forge.Issuer == "" {
		return target{}, fmt.Errorf("%s: issuer is missing", where)
	}
	if forge.Audience == "" {
		return target{}, fmt.Errorf("%s: audience is missing", where)
	}
	if len(forge.Repos) == 0 {
		return target{}, fmt.Errorf("%s: no repository listed", where)
	}

	repo := forge.Repos[0]
	if repo.ID == "" || repo.DefaultBranch == "" {
		return target{}, fmt.Errorf("%s: the first repository needs an id and a default_branch", where)
	}
	repoPath := repo.Name
	if repoPath == "" {
		repoPath = defaultRepoPath
	}

	return target{
		issuer:        forge.Issuer,
		audience:      forge.Audience,
		repoID:        repo.ID,
		repoPath:      repoPath,
		defaultBranch: repo.DefaultBranch,
	}, nil
}

// mint fills the defaults that depend on the target, then builds the token.
func mint(signer *devforge.Signer, forgeType, scenario string, dest target, opts options) (string, error) {
	kind, err := devforge.ParseScenarioKind(scenario)
	if err != nil {
		return "", err
	}

	branch := opts.branch
	if branch == "" {
		branch = dest.defaultBranch
		if kind == devforge.ScenarioPR {
			branch = "feature"
		}
	}
	base := opts.base
	if base == "" {
		base = dest.defaultBranch
	}

	return signer.MintScenario(forgeType,
		devforge.Scenario{Kind: kind, Branch: branch, Base: base, Tag: opts.tag, Number: opts.number},
		devforge.Job{
			Audience: dest.audience, RepoID: dest.repoID, RepoPath: dest.repoPath,
			Actor: opts.actor, Protected: opts.protected, TTL: opts.ttl,
		})
}
