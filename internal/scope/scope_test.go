// Tests scope resolution for every event kind and branch mode.
// Checks the isolation guarantees: default reads only itself, names are path safe.
package scope

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"nx-caching-server/internal/config"
)

var repo = config.Repository{
	ID:            "123456",
	DefaultBranch: "main",
	BranchRules: []config.BranchRule{
		{Match: []string{"v*.*.x", "release/*"}, Mode: config.BranchModeTrusted},
		{Match: []string{"wip/*", "spike/*"}, Mode: config.BranchModeShared},
		{Match: []string{"bot/*"}, Mode: config.BranchModePerActor},
		{Match: []string{"docs/*"}, Mode: config.BranchModeReadOnly},
		{Match: []string{"forbidden/*"}, Mode: config.BranchModeDeny},
	},
}

func TestResolve(t *testing.T) {
	main := trustedScope("main")
	v11 := trustedScope("v1.1.x")

	tests := []struct {
		name string
		ev   Event
		want Access
	}{
		{
			name: "push default branch",
			ev:   Event{Kind: KindPush, Branch: "main"},
			want: Access{WriteScope: main, ReadChain: []string{main}},
		},
		{
			name: "push version branch",
			ev:   Event{Kind: KindPush, Branch: "v1.1.x"},
			want: Access{WriteScope: v11, ReadChain: []string{v11, main}},
		},
		{
			name: "push release branch with a slash",
			ev:   Event{Kind: KindPush, Branch: "release/2.0"},
			want: Access{WriteScope: trustedScope("release/2.0"), ReadChain: []string{trustedScope("release/2.0"), main}},
		},
		{
			name: "push unmatched branch is isolated",
			ev:   Event{Kind: KindPush, Branch: "feature/login"},
			want: Access{WriteScope: devScope("branch:feature/login"), ReadChain: []string{devScope("branch:feature/login"), main}},
		},
		{
			name: "push shared branch",
			ev:   Event{Kind: KindPush, Branch: "wip/a"},
			want: Access{WriteScope: devScope("shared:wip/*\nspike/*"), ReadChain: []string{devScope("shared:wip/*\nspike/*"), main}},
		},
		{
			name: "push per actor branch",
			ev:   Event{Kind: KindPush, Branch: "bot/deps", Actor: "renovate"},
			want: Access{WriteScope: devScope("actor:renovate"), ReadChain: []string{devScope("actor:renovate"), main}},
		},
		{
			name: "push read only branch",
			ev:   Event{Kind: KindPush, Branch: "docs/api"},
			want: Access{ReadOnly: true, ReadChain: []string{main}},
		},
		{
			name: "pull request on default branch",
			ev:   Event{Kind: KindPullRequest, PRNumber: 42, BaseBranch: "main"},
			want: Access{WriteScope: "pr-42", ReadChain: []string{"pr-42", main}},
		},
		{
			name: "pull request on trusted version branch",
			ev:   Event{Kind: KindPullRequest, PRNumber: 7, BaseBranch: "v1.1.x"},
			want: Access{WriteScope: "pr-7", ReadChain: []string{"pr-7", v11, main}},
		},
		{
			name: "pull request on untrusted branch skips its scope",
			ev:   Event{Kind: KindPullRequest, PRNumber: 7, BaseBranch: "feature/x"},
			want: Access{WriteScope: "pr-7", ReadChain: []string{"pr-7", main}},
		},
		{
			name: "pull request without base",
			ev:   Event{Kind: KindPullRequest, PRNumber: 7},
			want: Access{WriteScope: "pr-7", ReadChain: []string{"pr-7", main}},
		},
		{
			name: "pull request identified by its source branch",
			ev:   Event{Kind: KindPullRequest, PRSourceBranch: "feature/login"},
			want: Access{WriteScope: prBranchScope("feature/login"), ReadChain: []string{prBranchScope("feature/login"), main}},
		},
		{
			name: "pull request number wins over the source branch",
			ev:   Event{Kind: KindPullRequest, PRNumber: 9, PRSourceBranch: "feature/login", BaseBranch: "main"},
			want: Access{WriteScope: "pr-9", ReadChain: []string{"pr-9", main}},
		},
		{
			name: "untrusted event reads the default scope only",
			ev:   Event{Kind: KindUntrusted, Branch: "main"},
			want: Access{ReadOnly: true, ReadChain: []string{main}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(repo, tt.ev)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	tests := []struct {
		name string
		ev   Event
	}{
		{"deny rule", Event{Kind: KindPush, Branch: "forbidden/x"}},
		{"per actor without actor", Event{Kind: KindPush, Branch: "bot/deps"}},
		{"push without branch", Event{Kind: KindPush}},
		{"pull request without number or source branch", Event{Kind: KindPullRequest, BaseBranch: "main"}},
		{"negative pull request number", Event{Kind: KindPullRequest, PRNumber: -1}},
		{"unknown kind", Event{Kind: Kind(99)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(repo, tt.ev)

			if err == nil {
				t.Fatalf("expected an error, got %+v", got)
			}
		})
	}

	_, err := Resolve(repo, Event{Kind: KindPush, Branch: "forbidden/x"})
	if !errors.Is(err, ErrDenied) {
		t.Errorf("deny rule error = %v, want ErrDenied", err)
	}
}

// The default branch must never read a scope that untrusted code can write.
func TestDefaultBranchReadsOnlyItself(t *testing.T) {
	got, err := Resolve(repo, Event{Kind: KindPush, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ReadChain) != 1 || !strings.HasPrefix(got.ReadChain[0], PrefixTrusted) {
		t.Errorf("default branch chain = %v", got.ReadChain)
	}
}

func TestTrustedScopesNeverReadUntrustedOnes(t *testing.T) {
	events := []Event{
		{Kind: KindPush, Branch: "main"},
		{Kind: KindPush, Branch: "v1.1.x"},
		{Kind: KindPush, Branch: "release/2.0"},
	}
	for _, ev := range events {
		got, err := Resolve(repo, ev)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range got.ReadChain {
			if !strings.HasPrefix(name, PrefixTrusted) {
				t.Errorf("%+v reads untrusted scope %q", ev, name)
			}
		}
	}
}

func TestNoWriteForReadOnlyResults(t *testing.T) {
	for _, ev := range []Event{
		{Kind: KindUntrusted},
		{Kind: KindPush, Branch: "docs/api"},
	} {
		got, err := Resolve(repo, ev)
		if err != nil {
			t.Fatal(err)
		}
		if !got.ReadOnly || got.WriteScope != "" {
			t.Errorf("%+v: result %+v must be read only", ev, got)
		}
	}
}

func TestChainsHaveNoDuplicatesAndOwnScopeFirst(t *testing.T) {
	events := []Event{
		{Kind: KindPush, Branch: "main"},
		{Kind: KindPush, Branch: "v1.1.x"},
		{Kind: KindPush, Branch: "feature/x"},
		{Kind: KindPullRequest, PRNumber: 1, BaseBranch: "main"},
		{Kind: KindPullRequest, PRNumber: 1, BaseBranch: "v1.1.x"},
	}
	for _, ev := range events {
		got, err := Resolve(repo, ev)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, name := range got.ReadChain {
			if seen[name] {
				t.Errorf("%+v: duplicate scope %q in %v", ev, name, got.ReadChain)
			}
			seen[name] = true
		}
		if got.ReadChain[0] != got.WriteScope {
			t.Errorf("%+v: chain %v does not start with the write scope %q", ev, got.ReadChain, got.WriteScope)
		}
	}
}

func TestScopeNamesAreSafePathSegments(t *testing.T) {
	branches := []string{
		"main", "../../etc/passwd", "a/b/c", strings.Repeat("x", 300),
		"é/ü", "a\x00b", "feature/ ", "..", ".",
	}
	for _, branch := range branches {
		hostile := config.Repository{ID: "1", DefaultBranch: "main"}
		for _, ev := range []Event{
			{Kind: KindPush, Branch: branch},
			{Kind: KindPullRequest, PRNumber: 5, BaseBranch: branch},
			{Kind: KindPullRequest, PRSourceBranch: branch},
		} {
			got, err := Resolve(hostile, ev)
			if err != nil {
				t.Fatalf("%+v: %v", ev, err)
			}
			for _, name := range append([]string{got.WriteScope}, got.ReadChain...) {
				if !Valid(name) {
					t.Errorf("branch %q produced invalid scope %q", branch, name)
				}
			}
		}
	}
}

func prBranchScope(branch string) string { return PrefixPR + "b" + shortHash("pr-source:"+branch) }

func TestPullRequestSourceBranchScopes(t *testing.T) {
	a, err := Resolve(repo, Event{Kind: KindPullRequest, PRSourceBranch: "feature/a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Resolve(repo, Event{Kind: KindPullRequest, PRSourceBranch: "feature/b"})
	if err != nil {
		t.Fatal(err)
	}

	if a.WriteScope == b.WriteScope {
		t.Error("different source branches must not share a scope")
	}
	if !strings.HasPrefix(a.WriteScope, PrefixPR) || !Valid(a.WriteScope) {
		t.Errorf("scope %q is not a valid pull request scope", a.WriteScope)
	}
}

func TestScopeNamesAreStableAndDistinct(t *testing.T) {
	if trustedScope("a") != trustedScope("a") {
		t.Error("same branch must give the same scope")
	}
	if trustedScope("a") == trustedScope("b") {
		t.Error("different branches must give different scopes")
	}
	if trustedScope("a") == devScope("branch:a") {
		t.Error("trusted and dev scopes must differ")
	}
}

func TestSharedScopeSurvivesRuleReordering(t *testing.T) {
	reordered := repo
	reordered.BranchRules = append([]config.BranchRule{repo.BranchRules[1]}, repo.BranchRules[0], repo.BranchRules[2])

	a, err := Resolve(repo, Event{Kind: KindPush, Branch: "wip/x"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Resolve(reordered, Event{Kind: KindPush, Branch: "wip/x"})
	if err != nil {
		t.Fatal(err)
	}
	if a.WriteScope != b.WriteScope {
		t.Errorf("scope changed after reordering: %q vs %q", a.WriteScope, b.WriteScope)
	}
}

func TestValid(t *testing.T) {
	for _, name := range []string{"t-0123456789abcdef", "dev-0123456789abcdef", "pr-1", "pr-42", "pr-b0123456789abcdef"} {
		if !Valid(name) {
			t.Errorf("%q should be valid", name)
		}
	}
	invalid := []string{
		"", "..", "t-", "t-xyz", "main", "t-0123456789abcdef0", "t-0123456789abcde",
		"pr-0", "pr-01", "pr-4/2", "pr-x", "pr-b", "pr-b0123456789abcde", "pr-0123456789abcdef", "dev-../x", "T-0123456789abcdef", "pr-1234567890123456789",
	}
	for _, name := range invalid {
		if Valid(name) {
			t.Errorf("%q should be invalid", name)
		}
	}
}
