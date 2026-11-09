package auth

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestMiddleware(t *testing.T) {
	var gotNS string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotNS = NamespaceFrom(r)
		w.WriteHeader(http.StatusOK)
	})

	tokens, err := ParseTokens("", "repoA:test-token")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		tokens Tokens
		header string
		want   int
	}{
		{name: "no token configured", tokens: nil, want: http.StatusOK},
		{name: "valid token", tokens: tokens, header: "Bearer test-token", want: http.StatusOK},
		{name: "invalid token", tokens: tokens, header: "Bearer wrong-token", want: http.StatusUnauthorized},
		{name: "missing header", tokens: tokens, want: http.StatusUnauthorized},
		{name: "wrong scheme", tokens: tokens, header: "Basic test-token", want: http.StatusUnauthorized},
		{name: "no scheme", tokens: tokens, header: "test-token", want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNS = ""
			req := httptest.NewRequest("GET", "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			w := httptest.NewRecorder()

			Middleware(tt.tokens)(handler).ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Errorf("expected status %d, got %d", tt.want, w.Code)
			}
		})
	}

	t.Run("namespace is passed through context", func(t *testing.T) {
		gotNS = ""
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		Middleware(tokens)(handler).ServeHTTP(httptest.NewRecorder(), req)

		if gotNS != "repoA" {
			t.Errorf("expected namespace repoA, got %q", gotNS)
		}
	})
}

func TestParseTokens(t *testing.T) {
	hash := func(s string) [32]byte { return sha256.Sum256([]byte(s)) }

	tests := []struct {
		name       string
		authToken  string
		authTokens string
		want       Tokens
		wantErr    bool
	}{
		{name: "no variables set", want: nil},
		{name: "AUTH_TOKEN only", authToken: "tok", want: Tokens{hash("tok"): ""}},
		{
			name:       "AUTH_TOKENS with two entries",
			authTokens: "repoA:tokA, repoB : tokB",
			want:       Tokens{hash("tokA"): "repoA", hash("tokB"): "repoB"},
		},
		{
			name:       "token containing colon",
			authTokens: "repoA:tok:with:colons",
			want:       Tokens{hash("tok:with:colons"): "repoA"},
		},
		{name: "both variables set", authToken: "tok", authTokens: "repoA:tokA", wantErr: true},
		{name: "bad name", authTokens: "../x:tok", wantErr: true},
		{name: "duplicate name", authTokens: "repoA:tok1,repoA:tok2", wantErr: true},
		{name: "duplicate token", authTokens: "repoA:tok,repoB:tok", wantErr: true},
		{name: "malformed entry", authTokens: "foo", wantErr: true},
		{name: "empty entry", authTokens: "repoA:tokA,,repoB:tokB", wantErr: true},
		{name: "missing token", authTokens: "repoA:", wantErr: true},
		{name: "missing name", authTokens: ":tok", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTokens(tt.authToken, tt.authTokens)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNamespaces(t *testing.T) {
	tokens, err := ParseTokens("", "repoB:b,repoA:a")
	if err != nil {
		t.Fatal(err)
	}
	if got := tokens.Namespaces(); !reflect.DeepEqual(got, []string{"repoA", "repoB"}) {
		t.Errorf("got %v", got)
	}

	flat, _ := ParseTokens("tok", "")
	if got := flat.Namespaces(); len(got) != 0 {
		t.Errorf("flat token should have no namespaces, got %v", got)
	}
}
