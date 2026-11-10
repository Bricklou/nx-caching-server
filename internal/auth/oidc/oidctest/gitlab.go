// A fake GitLab API for tests of the merge request lookup.
// It checks the job token, counts requests and can be made to fail.
package oidctest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// GitLabJobToken is the job token the fake API accepts.
const GitLabJobToken = "glcbt-test-job-token"

// GitLabAPI serves pipelines and merge requests.
type GitLabAPI struct {
	*httptest.Server
	// Calls counts the requests that reached the API, rejected ones included.
	Calls atomic.Int32
	// Failing makes every request answer 500.
	Failing atomic.Bool

	mu        sync.Mutex
	pipelines map[string]string // "<project>/<pipeline>" to ref
	requests  map[string]mergeRequest
}

type mergeRequest struct {
	IID          int    `json:"iid"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
}

// NewGitLabAPI starts a fake API that stops with the test.
func NewGitLabAPI(t *testing.T) *GitLabAPI {
	t.Helper()

	g := &GitLabAPI{pipelines: map[string]string{}, requests: map[string]mergeRequest{}}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.Close)
	return g
}

// AddMergeRequest registers a merge request pipeline and its merge request.
func (g *GitLabAPI) AddMergeRequest(projectID, pipelineID string, iid int, source, target string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pipelines[projectID+"/"+pipelineID] = fmt.Sprintf("refs/merge-requests/%d/head", iid)
	g.requests[fmt.Sprintf("%s/%d", projectID, iid)] = mergeRequest{IID: iid, SourceBranch: source, TargetBranch: target}
}

// AddPipeline registers a pipeline on a ref, such as refs/heads/main.
func (g *GitLabAPI) AddPipeline(projectID, pipelineID, ref string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pipelines[projectID+"/"+pipelineID] = ref
}

func (g *GitLabAPI) serve(w http.ResponseWriter, r *http.Request) {
	g.Calls.Add(1)

	if r.Header.Get("JOB-TOKEN") != GitLabJobToken {
		http.Error(w, `{"message":"401 Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if g.Failing.Load() {
		http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
		return
	}

	// /api/v4/projects/<project>/pipelines/<id> or /merge_requests/<iid>
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v4/projects/"), "/")
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	switch parts[1] {
	case "pipelines":
		if ref, ok := g.pipelines[parts[0]+"/"+parts[2]]; ok {
			json.NewEncoder(w).Encode(map[string]any{"id": 1, "ref": ref})
			return
		}
	case "merge_requests":
		if mr, ok := g.requests[parts[0]+"/"+parts[2]]; ok {
			json.NewEncoder(w).Encode(mr)
			return
		}
	}
	http.Error(w, `{"message":"404 Not Found"}`, http.StatusNotFound)
}
