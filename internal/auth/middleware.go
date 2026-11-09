package auth

import (
	"context"
	"crypto/sha256"
	"net/http"
	"strings"
)

type namespaceCtxKey struct{}

// NamespaceFrom returns the namespace assigned by Middleware, or "" when none was set.
func NamespaceFrom(req *http.Request) string {
	ns, _ := req.Context().Value(namespaceCtxKey{}).(string)
	return ns
}

// Middleware authenticates the request against tokens and stores the matching
// namespace in the request context. Nil tokens disable authentication.
// Looking up the SHA-256 hash in a map is safe against timing attacks: any
// timing difference leaks information about the hash, not the token.
func Middleware(tokens Tokens) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if tokens == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			scheme, token, found := strings.Cut(req.Header.Get("Authorization"), " ")
			if !found || scheme != "Bearer" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			ns, ok := tokens[sha256.Sum256([]byte(token))]
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(req.Context(), namespaceCtxKey{}, ns)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
}
