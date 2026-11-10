// Authenticates requests with a bearer token and exposes the verified identity to the handlers.
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
	MessageUnauthenticated = "Missing or invalid authentication token"
	MessageForbidden       = "Access to the cache is forbidden"
)

type identityKey struct{}

// ContextWithIdentity returns a context carrying id.
func ContextWithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom returns the identity set by Middleware, or false when the request
// is not authenticated, which includes disabled authentication.
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

// Middleware rejects requests without a valid token and stores the identity in
// the context. A valid token refused access answers 403.
func Middleware(authenticator Authenticator, opts ...MiddlewareOption) func(http.Handler) http.Handler {
	settings := middlewareSettings{unauthenticatedStatus: func(*http.Request) int { return http.StatusUnauthorized }}
	for _, opt := range opts {
		opt(&settings)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := BearerToken(r)
			if !ok {
				Reject(w, r, settings.unauthenticatedStatus(r), MessageUnauthenticated, "missing or malformed Authorization header", nil)
				return
			}

			identity, err := authenticator.Authenticate(r.Context(), raw)
			switch {
			case IsForbidden(err):
				Reject(w, r, http.StatusForbidden, MessageForbidden, "token not allowed", err)
				return
			case err != nil:
				Reject(w, r, settings.unauthenticatedStatus(r), MessageUnauthenticated, "token rejected", err)
				return
			}

			next.ServeHTTP(w, r.WithContext(ContextWithIdentity(r.Context(), identity)))
		})
	}
}

// IsForbidden reports whether err means a valid token was refused access.
func IsForbidden(err error) bool {
	return errors.Is(err, ErrRepoNotAllowed) || errors.Is(err, scope.ErrDenied)
}

// BearerToken extracts the token of an "Authorization: Bearer <token>" header.
func BearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

// Reject answers with a generic text message. The reason goes to the log only.
func Reject(w http.ResponseWriter, r *http.Request, status int, message, reason string, err error) {
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
