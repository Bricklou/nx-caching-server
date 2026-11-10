// Checks the server against api-spec.json, the Nx remote cache specification, which is the source of truth.
// Every documented operation and status needs a scenario, and each answer must match the declared content.
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"nx-caching-server/internal/config"
)

const specPath = "../../api-spec.json"

type specResponse struct {
	Content map[string]json.RawMessage `json:"content"`
}

type specOperation struct {
	Security  []map[string][]string   `json:"security"`
	Responses map[string]specResponse `json:"responses"`
}

// operations returns the operations of the specification by path then by HTTP method.
func loadSpec(t *testing.T) map[string]map[string]specOperation {
	t.Helper()

	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("cannot read the specification: %v", err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid specification: %v", err)
	}

	httpMethods := []string{"get", "put", "post", "delete", "head", "patch", "options", "trace"}
	operations := map[string]map[string]specOperation{}
	for path, item := range doc.Paths {
		operations[path] = map[string]specOperation{}
		for key, raw := range item {
			if !slices.Contains(httpMethods, key) {
				continue // path level fields such as parameters
			}
			var op specOperation
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("invalid operation %s %s: %v", key, path, err)
			}
			operations[path][key] = op
		}
	}
	return operations
}

// scenario produces the answer of the server for one documented status.
type scenario struct {
	name   string
	method string // lower case, as in the specification
	status int
	run    func(h *harness) *httptest.ResponseRecorder
}

func specScenarios() []scenario {
	url := func(hash string) string { return "/v1/cache/" + hash }

	return []scenario{
		{"upload", "put", 200, func(h *harness) *httptest.ResponseRecorder {
			return h.do("PUT", url("put200"), h.token(pushMain), "content")
		}},
		{"upload without a token", "put", 401, func(h *harness) *httptest.ResponseRecorder {
			return h.do("PUT", url("put401"), "", "content")
		}},
		{"upload with an invalid token", "put", 401, func(h *harness) *httptest.ResponseRecorder {
			return h.do("PUT", url("put401b"), "not-a-token", "content")
		}},
		{"upload with a read only token", "put", 403, func(h *harness) *httptest.ResponseRecorder {
			return h.do("PUT", url("put403"), h.token(untrusted), "content")
		}},
		{"upload from a repository that is not allowed", "put", 403, func(h *harness) *httptest.ResponseRecorder {
			return h.do("PUT", url("put403b"), h.tokenForRepo("999"), "content")
		}},
		{"upload of an existing entry", "put", 409, func(h *harness) *httptest.ResponseRecorder {
			token := h.token(pushMain)
			h.do("PUT", url("put409"), token, "first")
			return h.do("PUT", url("put409"), token, "second")
		}},

		{"download", "get", 200, func(h *harness) *httptest.ResponseRecorder {
			token := h.token(pushMain)
			h.do("PUT", url("get200"), token, "stored content")
			return h.do("GET", url("get200"), token, "")
		}},
		{"download without a token", "get", 403, func(h *harness) *httptest.ResponseRecorder {
			return h.do("GET", url("get403"), "", "")
		}},
		{"download with an invalid token", "get", 403, func(h *harness) *httptest.ResponseRecorder {
			return h.do("GET", url("get403b"), "not-a-token", "")
		}},
		{"download from a repository that is not allowed", "get", 403, func(h *harness) *httptest.ResponseRecorder {
			return h.do("GET", url("get403c"), h.tokenForRepo("999"), "")
		}},
		{"download of a missing entry", "get", 404, func(h *harness) *httptest.ResponseRecorder {
			return h.do("GET", url("get404"), h.token(pushMain), "")
		}},
	}
}

func TestServerFollowsTheSpecification(t *testing.T) {
	spec := loadSpec(t)
	operations, ok := spec["/v1/cache/{hash}"]
	if !ok {
		t.Fatal("the specification has no /v1/cache/{hash} path")
	}

	covered := map[string]bool{}
	for _, sc := range specScenarios() {
		t.Run(sc.method+" "+sc.name, func(t *testing.T) {
			op, ok := operations[sc.method]
			if !ok {
				t.Fatalf("the specification has no %s operation", sc.method)
			}
			declared, ok := op.Responses[strconv.Itoa(sc.status)]
			if !ok {
				t.Fatalf("the specification does not declare %d for %s", sc.status, sc.method)
			}
			covered[sc.method+" "+strconv.Itoa(sc.status)] = true

			w := sc.run(newHarness(t, config.ForgeTypeGitHub))

			if w.Code != sc.status {
				t.Fatalf("status = %d, want %d (body %q)", w.Code, sc.status, w.Body.String())
			}
			checkDeclaredContent(t, declared, w)
		})
	}

	// Every operation and status of the specification needs a scenario.
	for method, op := range operations {
		for status := range op.Responses {
			if !covered[method+" "+status] {
				t.Errorf("the specification declares %s for %s but no scenario covers it", status, method)
			}
		}
	}
}

// checkDeclaredContent compares an answer with the content types its response declares.
func checkDeclaredContent(t *testing.T, declared specResponse, w *httptest.ResponseRecorder) {
	t.Helper()
	contentType := w.Header().Get("Content-Type")

	switch {
	case len(declared.Content) == 0:
		if w.Body.Len() != 0 {
			t.Errorf("the specification declares no content, got body %q", w.Body.String())
		}
	case hasContent(declared, "text/plain"):
		if !strings.HasPrefix(contentType, "text/plain") {
			t.Errorf("Content-Type = %q, want text/plain", contentType)
		}
		if strings.TrimSpace(w.Body.String()) == "" {
			t.Error("the specification declares an error message, the body is empty")
		}
	case hasContent(declared, "application/octet-stream"):
		if contentType != "application/octet-stream" {
			t.Errorf("Content-Type = %q, want application/octet-stream", contentType)
		}
		if got := w.Header().Get("Content-Length"); got != strconv.Itoa(w.Body.Len()) {
			t.Errorf("Content-Length = %q, body has %d bytes", got, w.Body.Len())
		}
	default:
		t.Errorf("no check for the declared content types %v", declared.Content)
	}
}

func hasContent(r specResponse, mediaType string) bool {
	_, ok := r.Content[mediaType]
	return ok
}

func TestEveryOperationRequiresTheDeclaredSecurity(t *testing.T) {
	spec := loadSpec(t)

	for method, op := range spec["/v1/cache/{hash}"] {
		if len(op.Security) == 0 {
			continue
		}
		t.Run(method, func(t *testing.T) {
			h := newHarness(t, config.ForgeTypeGitHub)

			w := h.do(strings.ToUpper(method), "/v1/cache/abc", "", "x")

			// The rejection must be one the specification lists for the operation.
			if _, declared := op.Responses[strconv.Itoa(w.Code)]; !declared || w.Code < 400 {
				t.Errorf("an unauthenticated %s answered %d, which the specification does not declare", method, w.Code)
			}
		})
	}
}

func TestMethodsOutsideTheSpecificationAreNotServed(t *testing.T) {
	spec := loadSpec(t)
	h := newHarness(t, config.ForgeTypeGitHub)
	token := h.token(pushMain)
	h.do("PUT", "/v1/cache/abc", token, "stored")

	for _, method := range []string{"HEAD", "POST", "DELETE", "PATCH", "OPTIONS"} {
		if _, documented := spec["/v1/cache/{hash}"][strings.ToLower(method)]; documented {
			continue
		}
		if w := h.do(method, "/v1/cache/abc", token, ""); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, w.Code)
		}
	}
}

// Requests that break the contract (bad hash, no Content-Length) have no status in the
// specification; they may only answer with a generic client or server error.
func TestRequestsOutsideTheContractUseGenericStatuses(t *testing.T) {
	h := newHarness(t, config.ForgeTypeGitHub)
	token := h.token(pushMain)
	allowed := []int{http.StatusBadRequest, http.StatusMethodNotAllowed, http.StatusInternalServerError}

	noLength := httptest.NewRequest("PUT", "/v1/cache/nolength", strings.NewReader("data"))
	noLength.Header.Set("Authorization", "Bearer "+token)
	noLength.ContentLength = -1
	noLength.Header.Del("Content-Length")

	tests := map[string]*httptest.ResponseRecorder{
		"upload with a bad hash":   h.do("PUT", "/v1/cache/a.b", token, "data"),
		"download with a bad hash": h.do("GET", "/v1/cache/a.b", token, ""),
	}
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, noLength)
	tests["upload without a Content-Length"] = w

	for name, rec := range tests {
		if !slices.Contains(allowed, rec.Code) {
			t.Errorf("%s: status = %d, want one of %v", name, rec.Code, allowed)
		}
	}
}
