// Tests the global middlewares: ordering, panic recovery and request logging.
// Logging is captured by swapping the default slog handler for the duration of a test.
package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChainRunsTheFirstMiddlewareFirst(t *testing.T) {
	var order []string
	mark := func(name string) middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+" in")
				next.ServeHTTP(w, r)
				order = append(order, name+" out")
			})
		}
	}
	h := chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "handler") }),
		mark("a"), mark("b"))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	want := "a in,b in,handler,b out,a out"
	if got := strings.Join(order, ","); got != want {
		t.Errorf("order = %s, want %s", got, want)
	}
}

func TestChainWithoutMiddlewares(t *testing.T) {
	called := false
	h := chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	if !called {
		t.Error("the handler was not called")
	}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func TestRecoveryTurnsAPanicIntoA500(t *testing.T) {
	logs := captureLogs(t)
	calls := 0
	h := recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/boom" {
			panic("kaboom")
		}
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/boom", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(logs.String(), "kaboom") {
		t.Errorf("the panic was not logged: %s", logs)
	}

	// The next request is served normally.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/fine", nil))
	if w.Code != http.StatusOK || calls != 2 {
		t.Errorf("after a panic: status = %d, calls = %d", w.Code, calls)
	}
}

func TestRecoveryLetsAbortHandlerThrough(t *testing.T) {
	h := recovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))

	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler to propagate", rec)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestRequestLogRecordsTheOutcome(t *testing.T) {
	logs := captureLogs(t)
	h := requestLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest("GET", "/v1/cache/abc", nil)
	req.Header.Set("Authorization", "Bearer super-secret-token")
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := logs.String()
	for _, want := range []string{"method=GET", "path=/v1/cache/abc", "status=404", "duration="} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q does not contain %q", out, want)
		}
	}
	if strings.Contains(out, "super-secret-token") || strings.Contains(out, "Authorization") {
		t.Errorf("the log leaks the credentials: %s", out)
	}
}

func TestRequestLogDefaultsToStatus200(t *testing.T) {
	logs := captureLogs(t)
	h := requestLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "body")
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	if !strings.Contains(logs.String(), "status=200") {
		t.Errorf("log = %s", logs)
	}
}

func TestStatusRecorderPassesBodiesThrough(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: inner, status: http.StatusOK}

	n, err := io.Copy(rec, strings.NewReader("streamed body"))

	if err != nil || n != 13 || inner.Body.String() != "streamed body" {
		t.Errorf("io.Copy = (%d, %v), body %q", n, err, inner.Body.String())
	}
	if rec.Unwrap() != http.ResponseWriter(inner) {
		t.Error("Unwrap does not return the underlying writer")
	}
}
