// The endpoint that exchanges a forge token for a session token.
// It sits outside the Nx specification, which only defines the cache routes.
package session

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"nx-caching-server/internal/auth"
)

// Handler answers POST requests carrying a forge token in the Authorization
// header with a session token. A session token cannot be exchanged again.
func (m *Manager) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := auth.BearerToken(r)
		if !ok {
			auth.Reject(w, r, http.StatusUnauthorized, auth.MessageUnauthenticated, "missing or malformed Authorization header", nil)
			return
		}

		session, err := m.Exchange(r.Context(), raw)
		switch {
		case auth.IsForbidden(err):
			auth.Reject(w, r, http.StatusForbidden, auth.MessageForbidden, "token not allowed", err)
			return
		case err != nil:
			auth.Reject(w, r, http.StatusUnauthorized, auth.MessageUnauthenticated, "token rejected", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		err = json.NewEncoder(w).Encode(map[string]any{
			"token":      session.Token,
			"expires_in": int(time.Until(session.Expiry).Round(time.Second).Seconds()),
		})
		if err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
			http.Error(w, "Failed to answer", http.StatusInternalServerError)
		}
	})
}
