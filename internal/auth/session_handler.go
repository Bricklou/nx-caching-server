// The endpoint that exchanges a forge token for a session token.
// It sits outside the Nx specification, which only defines the cache routes.
package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// SessionHandler answers POST requests carrying a forge token in the Authorization
// header with a session token. A session token cannot be exchanged again.
func SessionHandler(verifier *Verifier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearerToken(r)
		if !ok {
			reject(w, r, http.StatusUnauthorized, messageUnauthenticated, "missing or malformed Authorization header", nil)
			return
		}

		session, err := verifier.Exchange(r.Context(), raw)
		switch {
		case isForbidden(err):
			reject(w, r, http.StatusForbidden, messageForbidden, "token not allowed", err)
			return
		case err != nil:
			reject(w, r, http.StatusUnauthorized, messageUnauthenticated, "token rejected", err)
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
