// Tests cleanup over any sweeper: retention per kind of scope, resilience to failures, and the store's own housekeeping.
// Entries are aged in a memory store; the filesystem specifics are tested with the backend.
package cache

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"nx-caching-server/internal/config"
)

var ctx = context.Background()

const (
	hour = time.Hour
	day  = 24 * time.Hour
)

// uniform is a retention that treats every kind of scope the same.
func uniform(d time.Duration) config.Retention {
	return config.Retention{Default: d, Trusted: d, PR: d, Dev: d}
}

func scopedKey(scopeName, name string) string {
	return "github/1/" + scopeName + "/" + name
}

func TestCleanupRemovesStaleEntriesInNestedScopes(t *testing.T) {
	mem := newMemStore()
	old := scopedKey("pr-2", "old")
	fresh := scopedKey(mainScope, "new")
	mem.set(old, "old", 2*hour)
	mem.set(fresh, "new", 0)

	Cleanup(ctx, mem, uniform(hour))

	if mem.has(old) {
		t.Error("expected the stale entry to be removed")
	}
	if !mem.has(fresh) {
		t.Error("expected the fresh entry to remain")
	}
}

func TestCleanupContinuesAfterAFailedDeletion(t *testing.T) {
	mem := newMemStore()
	mem.set("a", "x", 2*hour)
	mem.set("b", "x", 2*hour)
	mem.set("c", "x", 2*hour)
	mem.deleteFail["a"] = errors.New("permission denied")
	mem.deleteFail["b"] = errors.New("permission denied")

	Cleanup(ctx, mem, uniform(hour))

	if mem.has("c") {
		t.Error("a failed deletion stopped the cleanup")
	}
}

func TestRetentionDependsOnTheKindOfScope(t *testing.T) {
	retention := config.Retention{Default: hour, Trusted: 7 * day, PR: day, Dev: 3 * day}
	mem := newMemStore()

	const (
		trustedScopeName = "t-0123456789abcdef"
		devScopeName     = "dev-0123456789abcdef"
		prBranchScope    = "pr-b0123456789abcdef"
	)
	tests := []struct {
		name  string
		scope string
		age   time.Duration
		keep  bool
	}{
		{"trusted entry younger than its retention", trustedScopeName, 6 * day, true},
		{"trusted entry older than its retention", trustedScopeName, 8 * day, false},
		{"pull request entry younger than its retention", "pr-1", 12 * hour, true},
		{"pull request entry older than its retention", "pr-2", 2 * day, false},
		{"pull request keyed by branch, older", prBranchScope, 2 * day, false},
		{"dev entry younger than its retention", devScopeName, 2 * day, true},
		{"dev entry older than its retention", "dev-fedcba9876543210", 4 * day, false},
	}

	keys := make([]string, len(tests))
	for i, tt := range tests {
		keys[i] = scopedKey(tt.scope, "entry"+strconv.Itoa(i))
		mem.set(keys[i], "x", tt.age)
	}

	Cleanup(ctx, mem, retention)

	for i, tt := range tests {
		if got := mem.has(keys[i]); got != tt.keep {
			t.Errorf("%s: kept = %v, want %v", tt.name, got, tt.keep)
		}
	}
}

func TestRetentionFallsBackToTheDefaultOutsideAScope(t *testing.T) {
	retention := config.Retention{Default: hour, Trusted: 7 * day, PR: 7 * day, Dev: 7 * day}
	mem := newMemStore()

	keys := map[string]string{
		"flat":          "flat",
		"unknown scope": scopedKey("main", "unknown"),   // not a valid scope name
		"no scope":      "github/1/shallow",             // no scope segment
		"too deep":      scopedKey(mainScope, "x/deep"), // one level too many
	}
	for _, key := range keys {
		mem.set(key, "x", 2*day)
	}

	Cleanup(ctx, mem, retention)

	for name, key := range keys {
		if mem.has(key) {
			t.Errorf("%s: an entry outside any scope outlived the default retention", name)
		}
	}
}

func TestRetentionForEdgeCases(t *testing.T) {
	retention := config.Retention{Default: hour, Trusted: 7 * day, PR: day, Dev: 3 * day}

	tests := []struct {
		name string
		key  string
		want time.Duration
	}{
		{"trusted scope", "github/1/t-0123456789abcdef/a", 7 * day},
		{"pull request scope", "github/1/pr-12/a", day},
		{"dev scope", "github/1/dev-0123456789abcdef/a", 3 * day},
		{"flat entry", "a", hour},
		{"no scope", "github/1/a", hour},
		{"unknown scope", "github/1/main/a", hour},
	}
	for _, tt := range tests {
		if got := retentionFor(retention, tt.key); got != tt.want {
			t.Errorf("%s: retention = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// batched removes through DeleteMany and records the calls.
type batched struct {
	*memStore
	calls [][]string
}

func (b *batched) DeleteMany(ctx context.Context, keys []string) error {
	b.calls = append(b.calls, append([]string(nil), keys...))
	for _, key := range keys {
		b.memStore.Delete(ctx, key)
	}
	return nil
}

func TestCleanupDeletesInBatchesWhenTheStoreCan(t *testing.T) {
	s := &batched{memStore: newMemStore()}
	for i := 0; i < batchSize+5; i++ {
		s.set("old"+strconv.Itoa(i), "x", 2*hour)
	}
	s.set("fresh", "x", 0)

	Cleanup(ctx, s, uniform(hour))

	if len(s.calls) != 2 || len(s.calls[0]) != batchSize || len(s.calls[1]) != 5 {
		t.Errorf("batches = %d, want one of %d and one of 5", len(s.calls), batchSize)
	}
	if s.count() != 1 || !s.has("fresh") {
		t.Errorf("%d entries remain, want only the fresh one", s.count())
	}
}

// housekept records the staleness it was given.
type housekept struct {
	*memStore
	staleUpload time.Duration
}

func (h *housekept) Housekeep(_ context.Context, staleUpload time.Duration) {
	h.staleUpload = staleUpload
}

func TestCleanupRunsTheStoresHousekeeping(t *testing.T) {
	s := &housekept{memStore: newMemStore()}

	Cleanup(ctx, s, config.Retention{Default: 3 * hour, Trusted: day, PR: day, Dev: day})

	if s.staleUpload != 3*hour {
		t.Errorf("stale upload = %v, want the default retention", s.staleUpload)
	}
}

func TestDownloadKeepsEntryFromCleanup(t *testing.T) {
	h, mem := newHandler(t)
	key := scopedKey(mainScope, "used")
	mem.set(key, "used", 2*hour)

	if w := do(h, "GET", "used", "", ident("1", mainAccess())); w.Code != http.StatusOK {
		t.Fatalf("download status = %d", w.Code)
	}
	Cleanup(ctx, mem, uniform(hour))

	if !mem.has(key) {
		t.Error("a recently downloaded entry did not survive cleanup")
	}
}

func TestRunCleanupStopsWhenTheContextEnds(t *testing.T) {
	mem := newMemStore()
	mem.set("x", "x", 2*hour)

	runCtx, cancel := context.WithCancel(context.Background())
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		RunCleanup(runCtx, mem, uniform(hour))
	}()

	// The first pass runs immediately.
	deadline := time.After(5 * time.Second)
	for mem.has("x") {
		select {
		case <-deadline:
			t.Fatal("the first cleanup pass did not run")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	stopped := make(chan struct{})
	go func() { done.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("RunCleanup did not stop after the context was cancelled")
	}
}
