// Tests the merge request lookup: what it asks GitLab, what it refuses, and how answers are cached.
// A fake GitLab API counts the requests, so caching and request sharing are asserted by number.
package gitlabapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nx-caching-server/internal/auth/oidc/oidctest"
)

var ctx = context.Background()

const token = oidctest.GitLabJobToken

func newClient(t *testing.T) (*Client, *oidctest.GitLabAPI, *time.Time) {
	t.Helper()
	api := oidctest.NewGitLabAPI(t)
	clock := time.Now()
	return New(api.URL, func() time.Time { return clock }), api, &clock
}

func TestLookupFindsTheMergeRequestAndItsTarget(t *testing.T) {
	c, api, _ := newClient(t)
	api.AddMergeRequest("20", "700", 12, "feature/x", "release/1.0")

	mr, err := c.Lookup(ctx, token, "20", "700", "feature/x")

	if err != nil || mr != (MergeRequest{IID: 12, TargetBranch: "release/1.0"}) {
		t.Errorf("got (%+v, %v)", mr, err)
	}
	if got := api.Calls.Load(); got != 2 {
		t.Errorf("%d requests, want one for the pipeline and one for the merge request", got)
	}
}

func TestLookupSeparatesMergeRequestsOfTheSameSourceBranch(t *testing.T) {
	c, api, _ := newClient(t)
	api.AddMergeRequest("20", "700", 12, "feature/x", "main")
	api.AddMergeRequest("20", "701", 13, "feature/x", "release/1.0")

	a, errA := c.Lookup(ctx, token, "20", "700", "feature/x")
	b, errB := c.Lookup(ctx, token, "20", "701", "feature/x")

	if errA != nil || errB != nil || a.IID != 12 || b.IID != 13 || a.TargetBranch != "main" || b.TargetBranch != "release/1.0" {
		t.Errorf("got (%+v, %v) and (%+v, %v)", a, errA, b, errB)
	}
}

func TestLookupRefusesWhatIsNotAMergeRequestOfTheToken(t *testing.T) {
	c, api, _ := newClient(t)
	api.AddPipeline("20", "1", "refs/heads/main")
	api.AddPipeline("20", "2", "refs/merge-requests/12/../../evil")
	api.AddMergeRequest("20", "3", 12, "someone-elses-branch", "main")
	api.AddMergeRequest("20", "4", 13, "feature/x", "")

	tests := []struct {
		name     string
		pipeline string
		want     error
	}{
		{"a branch pipeline", "1", ErrNotMergeRequest},
		{"a ref that only looks like one", "2", ErrNotMergeRequest},
		{"another source branch than the token says", "3", ErrMismatch},
		{"no target branch", "4", ErrMismatch},
		{"unknown pipeline", "99", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.Lookup(ctx, token, "20", tt.pipeline, "feature/x")

			if err == nil || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestLookupNeedsAJobToken(t *testing.T) {
	c, api, _ := newClient(t)
	api.AddMergeRequest("20", "700", 12, "feature/x", "main")

	if _, err := c.Lookup(ctx, "", "20", "700", "feature/x"); err == nil {
		t.Error("a lookup without a job token was accepted")
	}
	if api.Calls.Load() != 0 {
		t.Error("a lookup without a job token reached the API")
	}
}

func TestLookupWithARefusedTokenFails(t *testing.T) {
	c, api, _ := newClient(t)
	api.AddMergeRequest("20", "700", 12, "feature/x", "main")

	if _, err := c.Lookup(ctx, "glcbt-wrong", "20", "700", "feature/x"); err == nil {
		t.Error("a job token GitLab refuses was accepted")
	}
}

func TestLookupRefusesIdentifiersThatAreNotNumbers(t *testing.T) {
	c, api, _ := newClient(t)

	for _, id := range [][2]string{{"20/../x", "1"}, {"20", "1?x=y"}, {"", "1"}, {"20", ""}, {"20", "-1"}} {
		if _, err := c.Lookup(ctx, token, id[0], id[1], "b"); err == nil {
			t.Errorf("project %q pipeline %q accepted", id[0], id[1])
		}
	}
	if api.Calls.Load() != 0 {
		t.Error("a malformed identifier reached the API")
	}
}

func TestTheCacheIsSharedBetweenTokens(t *testing.T) {
	c, api, _ := newClient(t)
	api.AddMergeRequest("20", "700", 12, "feature/x", "main")
	c.Lookup(ctx, token, "20", "700", "feature/x")
	before := api.Calls.Load()

	// The pipeline decides the answer, not the token that asked.
	mr, err := c.Lookup(ctx, "glcbt-another-job", "20", "700", "feature/x")

	if err != nil || mr.IID != 12 || api.Calls.Load() != before {
		t.Errorf("got (%+v, %v) with %d new requests", mr, err, api.Calls.Load()-before)
	}
}

func TestLookupCachesSuccesses(t *testing.T) {
	c, api, clock := newClient(t)
	api.AddMergeRequest("20", "700", 12, "feature/x", "main")
	c.Lookup(ctx, token, "20", "700", "feature/x")
	before := api.Calls.Load()

	for range 5 {
		if mr, err := c.Lookup(ctx, token, "20", "700", "feature/x"); err != nil || mr.IID != 12 {
			t.Fatalf("cached lookup: (%+v, %v)", mr, err)
		}
	}
	if got := api.Calls.Load() - before; got != 0 {
		t.Errorf("cached lookups sent %d requests", got)
	}

	*clock = clock.Add(successTTL + time.Second)
	c.Lookup(ctx, token, "20", "700", "feature/x")
	if got := api.Calls.Load() - before; got != 2 {
		t.Errorf("a lookup after the TTL sent %d requests, want 2", got)
	}
}

func TestLookupCachesFailuresBrieflyThenRetries(t *testing.T) {
	c, api, clock := newClient(t)
	api.AddMergeRequest("20", "700", 12, "feature/x", "main")
	api.Failing.Store(true)

	if _, err := c.Lookup(ctx, token, "20", "700", "feature/x"); err == nil {
		t.Fatal("expected a failure")
	}
	before := api.Calls.Load()
	for range 5 {
		if _, err := c.Lookup(ctx, token, "20", "700", "feature/x"); err == nil {
			t.Fatal("expected the cached failure")
		}
	}
	if got := api.Calls.Load() - before; got != 0 {
		t.Errorf("a failing GitLab was asked %d more times within the failure TTL", got)
	}

	api.Failing.Store(false)
	*clock = clock.Add(failureTTL + time.Second)
	if mr, err := c.Lookup(ctx, token, "20", "700", "feature/x"); err != nil || mr.IID != 12 {
		t.Errorf("after the failure TTL: (%+v, %v)", mr, err)
	}
}

func TestConcurrentLookupsShareOneRequest(t *testing.T) {
	c, api, _ := newClient(t)
	api.AddMergeRequest("20", "700", 12, "feature/x", "main")

	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if mr, err := c.Lookup(ctx, token, "20", "700", "feature/x"); err != nil || mr.IID != 12 {
				t.Errorf("got (%+v, %v)", mr, err)
			}
		}()
	}
	wg.Wait()

	if got := api.Calls.Load(); got != 2 {
		t.Errorf("40 concurrent lookups sent %d requests, want 2", got)
	}
}

func TestACancelledCallerDoesNotFailTheOthers(t *testing.T) {
	c, api, _ := newClient(t)
	api.AddMergeRequest("20", "700", 12, "feature/x", "main")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	c.Lookup(cancelled, token, "20", "700", "feature/x")

	if mr, err := c.Lookup(ctx, token, "20", "700", "feature/x"); err != nil || mr.IID != 12 {
		t.Errorf("a later caller got (%+v, %v)", mr, err)
	}
}

func TestLookupSendsTheTokenInAHeaderOnly(t *testing.T) {
	var gotPath, gotQuery, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotHeader = r.URL.Path, r.URL.RawQuery, r.Header.Get("JOB-TOKEN")
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer srv.Close()
	c := New(srv.URL+"/", time.Now)

	_, err := c.Lookup(ctx, "glcbt-secret", "20", "700", "b")

	if gotHeader != "glcbt-secret" {
		t.Errorf("JOB-TOKEN = %q", gotHeader)
	}
	if err == nil || strings.Contains(err.Error(), "glcbt-secret") {
		t.Errorf("error = %v", err)
	}
	if gotPath != "/api/v4/projects/20/pipelines/700" || gotQuery != "" {
		t.Errorf("requested %q ? %q", gotPath, gotQuery)
	}
}

func TestTheTokenDoesNotFollowRedirects(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("PRIVATE-TOKEN") != ""
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer srv.Close()

	_, err := New(srv.URL, time.Now).Lookup(ctx, "glcbt-secret", "20", "700", "b")

	if err == nil || leaked {
		t.Errorf("error = %v, token leaked = %v", err, leaked)
	}
}

func TestTheCacheIsBounded(t *testing.T) {
	c, api, _ := newClient(t)
	for i := 0; i < maxEntries+50; i++ {
		id := 1000 + i
		api.AddMergeRequest("20", itoa(id), id, "b", "main")
		c.Lookup(ctx, token, "20", itoa(id), "b")
	}

	c.mu.Lock()
	size := len(c.entries)
	c.mu.Unlock()
	if size > maxEntries {
		t.Errorf("%d entries, want at most %d", size, maxEntries)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
