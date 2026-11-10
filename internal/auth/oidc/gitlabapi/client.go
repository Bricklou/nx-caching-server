// Finds the merge request of a GitLab pipeline through the GitLab API, with the job token of the caller.
// The ID token names no merge request, but the pipeline it belongs to does; answers are cached.
package gitlabapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// successTTL is how long a found merge request is remembered. A pipeline
	// never changes merge request, but a merge request can change its target.
	successTTL = time.Hour
	// failureTTL is how long a failed lookup is remembered, so a GitLab that is
	// down or refuses the token is not asked again for every request.
	failureTTL = 30 * time.Second
	// maxEntries bounds the cache.
	maxEntries = 4096

	requestTimeout  = 10 * time.Second
	maxResponseSize = 1 << 20
)

var (
	// ErrNotMergeRequest means the pipeline does not belong to a merge request.
	ErrNotMergeRequest = errors.New("gitlabapi: pipeline is not a merge request pipeline")
	// ErrMismatch means GitLab's answer contradicts the token.
	ErrMismatch = errors.New("gitlabapi: merge request does not match the token")

	digits = regexp.MustCompile(`^[0-9]{1,19}$`)
	// mergeRequestRef is the ref of a merge request pipeline: refs/merge-requests/<iid>/head,
	// or /merge for a merged results pipeline.
	mergeRequestRef = regexp.MustCompile(`^refs/merge-requests/([0-9]{1,9})/(?:head|merge)$`)
)

// MergeRequest is what the server needs to know about a merge request.
type MergeRequest struct {
	IID          int
	TargetBranch string
}

// Client calls the GitLab API with the job token each caller brings, and keeps
// no credential of its own.
type Client struct {
	base string
	http *http.Client
	now  func() time.Time

	mu       sync.Mutex // guards the two maps
	entries  map[string]entry
	inflight map[string]*flight
}

type entry struct {
	mr      MergeRequest
	err     error
	expires time.Time
}

// flight is a lookup in progress that concurrent callers wait for.
type flight struct {
	done chan struct{}
	mr   MergeRequest
	err  error
}

// New returns a client of the GitLab instance at baseURL. now dates the cache.
func New(baseURL string, now func() time.Time) *Client {
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		now:  now,
		http: &http.Client{
			Timeout: requestTimeout,
			// The job token must never follow a redirect to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		entries:  make(map[string]entry),
		inflight: make(map[string]*flight),
	}
}

// Lookup returns the merge request of a pipeline of a project. jobToken only
// authorizes the calls: the pipeline comes from the verified ID token, so a job
// token of another job changes nothing. sourceBranch is the branch the ID token
// reports; an answer for another branch is refused. Results, failures included,
// are cached by pipeline and not by token, and concurrent lookups share one request.
func (c *Client) Lookup(ctx context.Context, jobToken, projectID, pipelineID, sourceBranch string) (MergeRequest, error) {
	if jobToken == "" {
		return MergeRequest{}, errors.New("gitlabapi: no job token")
	}
	if !digits.MatchString(projectID) || !digits.MatchString(pipelineID) {
		return MergeRequest{}, fmt.Errorf("gitlabapi: project %q or pipeline %q is not a number", projectID, pipelineID)
	}

	key := projectID + "/" + pipelineID
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && c.now().Before(e.expires) {
		c.mu.Unlock()
		return e.mr, e.err
	}
	if f, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-f.done:
			return f.mr, f.err
		case <-ctx.Done():
			return MergeRequest{}, ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	c.inflight[key] = f
	c.mu.Unlock()

	// The request is shared, so it must not die with the caller that started it.
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
	f.mr, f.err = c.fetch(fetchCtx, jobToken, projectID, pipelineID, sourceBranch)
	cancel()

	c.mu.Lock()
	delete(c.inflight, key)
	c.store(key, f.mr, f.err)
	c.mu.Unlock()
	close(f.done)

	return f.mr, f.err
}

// store remembers an answer. The caller holds the lock.
func (c *Client) store(key string, mr MergeRequest, err error) {
	ttl := successTTL
	if err != nil {
		ttl = failureTTL
	}

	if len(c.entries) >= maxEntries {
		now := c.now()
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		// Still full: make room by dropping an arbitrary entry.
		for k := range c.entries {
			if len(c.entries) < maxEntries {
				break
			}
			delete(c.entries, k)
		}
	}
	c.entries[key] = entry{mr: mr, err: err, expires: c.now().Add(ttl)}
}

func (c *Client) fetch(ctx context.Context, jobToken, projectID, pipelineID, sourceBranch string) (MergeRequest, error) {
	var pipeline struct {
		Ref string `json:"ref"`
	}
	if err := c.get(ctx, fmt.Sprintf("/api/v4/projects/%s/pipelines/%s", projectID, pipelineID), jobToken, &pipeline); err != nil {
		return MergeRequest{}, err
	}
	match := mergeRequestRef.FindStringSubmatch(pipeline.Ref)
	if match == nil {
		return MergeRequest{}, fmt.Errorf("%w: ref %q", ErrNotMergeRequest, pipeline.Ref)
	}
	iid, _ := strconv.Atoi(match[1])

	var mr struct {
		IID          int    `json:"iid"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
	}
	if err := c.get(ctx, fmt.Sprintf("/api/v4/projects/%s/merge_requests/%d", projectID, iid), jobToken, &mr); err != nil {
		return MergeRequest{}, err
	}
	if mr.IID != iid || mr.SourceBranch != sourceBranch || mr.TargetBranch == "" {
		return MergeRequest{}, fmt.Errorf("%w: merge request %d has source %q, the token says %q", ErrMismatch, iid, mr.SourceBranch, sourceBranch)
	}
	return MergeRequest{IID: iid, TargetBranch: mr.TargetBranch}, nil
}

func (c *Client) get(ctx context.Context, path, jobToken string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("JOB-TOKEN", jobToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// The error names the URL, never the token, which only travels in a header.
		return fmt.Errorf("gitlabapi: GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gitlabapi: GET %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(into); err != nil {
		return fmt.Errorf("gitlabapi: GET %s: %w", path, err)
	}
	return nil
}
