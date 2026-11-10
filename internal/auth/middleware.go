// Authenticates requests with a forge token and exposes the verified identity to the handlers.
// Failures answer 401 or 403 with a generic text message; the reason is only logged.
package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"nx-caching-server/internal/scope"
)

// Messages shown to the client. They never say why a token was rejected.
const (
	messageUnauthenticated = "Missing or invalid authentication token"
	messageForbidden       = "Access to the cache is forbidden"
)

type identityKey struct{}

// ContextWithIdentity returns a context carrying id.
func ContextWithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom returns the identity set by Middleware. It reports false when
// the request was not authenticated, which is the case when authentication is
// disabled.
func IdentityFrom(ctx context.Context) (*Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(*Identity)
	return id, ok && id != nil
}

type middlewareSettings struct {
	unauthenticatedStatus func(*http.Request) int
}

// MiddlewareOption customizes Middleware.
type MiddlewareOption func(*middlewareSettings)

// WithUnauthenticatedStatus sets the status answering a request that has no
// valid token. The default is 401, for every request.
func WithUnauthenticatedStatus(status func(*http.Request) int) MiddlewareOption {
	return func(s *middlewareSettings) { s.unauthenticatedStatus = status }
}

// Middleware rejects requests without a valid token and stores the verified
// identity in the request context. A valid token that is not allowed to use the
// cache (unlisted repository, denied branch) answers 403.
func Middleware(verifier *Verifier, opts ...MiddlewareOption) func(http.Handler) http.Handler {
	settings := middlewareSettings{unauthenticatedStatus: func(*http.Request) int { return http.StatusUnauthorized }}
	for _, opt := range opts {
		opt(&settings)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := bearerToken(r)
			if !ok {
				reject(w, r, settings.unauthenticatedStatus(r), messageUnauthenticated, "missing or malformed Authorization header", nil)
				return
			}

			identity, err := verifier.Verify(r.Context(), raw)
			switch {
			case isForbidden(err):
				reject(w, r, http.StatusForbidden, messageForbidden, "token not allowed", err)
				return
			case err != nil:
				reject(w, r, settings.unauthenticatedStatus(r), messageUnauthenticated, "token rejected", err)
				return
			}

			next.ServeHTTP(w, r.WithContext(ContextWithIdentity(r.Context(), identity)))
		})
	}
}

// isForbidden reports whether err means a valid token was refused access.
func isForbidden(err error) bool {
	return errors.Is(err, ErrRepoNotAllowed) || errors.Is(err, scope.ErrDenied)
}

// bearerToken extracts the token of an "Authorization: Bearer <token>" header.
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

// reject answers with a generic text message. The reason goes to the log only.
func reject(w http.ResponseWriter, r *http.Request, status int, message, reason string, err error) {
	slog.Warn("request rejected",
		slog.Int("status", status),
		slog.String("reason", reason),
		slog.Any("error", err),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("remote", r.RemoteAddr),
	)
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	http.Error(w, message, status)
}
