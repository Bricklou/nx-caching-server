// Decides where a CI job may write the cache and which scopes it may read.
// Pure functions over forge-neutral events and the repository branch rules.
package scope

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"nx-caching-server/internal/config"
)

// Scope directory prefixes. Cleanup derives the retention from them.
const (
	PrefixTrusted = "t-"
	PrefixPR      = "pr-"
	PrefixDev     = "dev-"
)

// ErrDenied means the branch rules reject the caller.
var ErrDenied = errors.New("scope: caller not allowed")

// Kind is the kind of job that produced the token.
type Kind int

const (
	// KindPush is a job running on a branch of the repository itself.
	KindPush Kind = iota
	// KindPullRequest is a pull or merge request job from the same repository.
	KindPullRequest
	// KindUntrusted is a job that may read the default scope but never write.
	KindUntrusted
)

// Event is the forge-neutral view of a token's claims.
type Event struct {
	Kind   Kind
	Branch string // KindPush
	Actor  string

	// KindPullRequest: the request is identified by its number when the forge
	// reports one, otherwise by its source branch (GitLab).
	PRNumber       int
	PRSourceBranch string
	BaseBranch     string // branch the request targets, empty when unknown
}

// Push is an event for a job running on a branch.
func Push(branch, actor string) Event {
	return Event{Kind: KindPush, Branch: branch, Actor: actor}
}

// PullRequestNumber is an event for a pull request the forge numbers.
func PullRequestNumber(number int, base, actor string) Event {
	return Event{Kind: KindPullRequest, PRNumber: number, BaseBranch: base, Actor: actor}
}

// PullRequestBranch is an event for a pull request identified by its source branch.
func PullRequestBranch(source, base, actor string) Event {
	return Event{Kind: KindPullRequest, PRSourceBranch: source, BaseBranch: base, Actor: actor}
}

// Untrusted is an event that may only read the default scope.
func Untrusted() Event { return Event{Kind: KindUntrusted} }

// Access is what a caller may do. WriteScope is empty when ReadOnly is set.
type Access struct {
	WriteScope string
	ReadChain  []string
	ReadOnly   bool
}

// scopePattern describes every well-formed scope name.
var scopePattern = regexp.MustCompile(`^(?:(?:t-|dev-)[0-9a-f]{16}|pr-[1-9][0-9]{0,17}|pr-b[0-9a-f]{16})$`)

// Valid reports whether name is a well-formed scope, safe as one path segment.
func Valid(name string) bool { return scopePattern.MatchString(name) }

// Resolve computes the write scope and read chain of ev for repo.
func Resolve(repo config.Repository, ev Event) (Access, error) {
	defaultScope := trustedScope(repo.DefaultBranch)

	switch ev.Kind {
	case KindUntrusted:
		return readOnly(defaultScope), nil
	case KindPullRequest:
		return resolvePullRequest(repo, ev, defaultScope)
	case KindPush:
		return resolvePush(repo, ev, defaultScope)
	}
	return Access{}, fmt.Errorf("scope: unknown event kind %d", ev.Kind)
}

func resolvePullRequest(repo config.Repository, ev Event, defaultScope string) (Access, error) {
	var own string
	switch {
	case ev.PRNumber > 0:
		own = PrefixPR + strconv.Itoa(ev.PRNumber)
	case ev.PRSourceBranch != "":
		// The "b" keeps hashed names apart from numeric ones.
		own = PrefixPR + "b" + shortHash("pr-source:"+ev.PRSourceBranch)
	default:
		return Access{}, errors.New("scope: pull request without a number or a source branch")
	}
	chain := []string{own}

	// Only a trusted base is worth reading; a request targeting a dev branch
	// must not read that branch's scope.
	if ev.BaseBranch != "" && ev.BaseBranch != repo.DefaultBranch {
		if mode, _ := repo.ModeFor(ev.BaseBranch); mode == config.BranchModeTrusted {
			chain = append(chain, trustedScope(ev.BaseBranch))
		}
	}

	return Access{WriteScope: own, ReadChain: append(chain, defaultScope)}, nil
}

func resolvePush(repo config.Repository, ev Event, defaultScope string) (Access, error) {
	if ev.Branch == "" {
		return Access{}, errors.New("scope: push without a branch")
	}

	mode, ruleIndex := repo.ModeFor(ev.Branch)

	var own string
	switch mode {
	case config.BranchModeTrusted:
		own = trustedScope(ev.Branch)
	case config.BranchModeIsolated:
		own = devScope("branch:" + ev.Branch)
	case config.BranchModeShared:
		// Keyed by the rule's patterns so reordering rules keeps the scope.
		own = devScope("shared:" + strings.Join(repo.BranchRules[ruleIndex].Match, "\n"))
	case config.BranchModePerActor:
		if ev.Actor == "" {
			return Access{}, errors.New("scope: per_actor rule without an actor")
		}
		own = devScope("actor:" + ev.Actor)
	case config.BranchModeReadOnly:
		return readOnly(defaultScope), nil
	case config.BranchModeDeny:
		return Access{}, fmt.Errorf("%w: branch %q", ErrDenied, ev.Branch)
	default:
		return Access{}, fmt.Errorf("scope: unknown mode %q", mode)
	}

	chain := []string{own}
	if own != defaultScope {
		chain = append(chain, defaultScope)
	}
	return Access{WriteScope: own, ReadChain: chain}, nil
}

// readOnly is the access of a caller that may read the default scope only.
func readOnly(defaultScope string) Access {
	return Access{ReadOnly: true, ReadChain: []string{defaultScope}}
}

// trustedScope names the scope of a trusted branch. The name is hashed so any
// branch name is safe on disk.
func trustedScope(branch string) string {
	return PrefixTrusted + shortHash("branch:"+branch)
}

func devScope(key string) string {
	return PrefixDev + shortHash(key)
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
