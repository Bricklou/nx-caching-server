// Session tokens: a signed copy of the claims of a verified forge token, valid for longer than the forge token.
// A job exchanges its short-lived token once and runs with the session token; access is resolved again on every request.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"nx-caching-server/internal/auth/adapters"
)

// sessionPrefix tells a session token from a forge token, which has two dots.
const sessionPrefix = "nxs_"

var (
	ErrInvalidSession = errors.New("auth: invalid session token")
	ErrSessionExpired = errors.New("auth: session token expired")
)

// sessionPayload is what a session token carries: the forge that vouched for
// the claims, and when the token stops being valid. It holds no access rights.
type sessionPayload struct {
	Forge  string          `json:"f"`
	Expiry int64           `json:"e"`
	Claims adapters.Claims `json:"c"`
}

type sessions struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

// newSessions signs with key, or with a random key when it is empty.
func newSessions(key []byte, ttl time.Duration, now func() time.Time) *sessions {
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic(err) // the system source of randomness is unavailable
		}
	}
	return &sessions{key: key, ttl: ttl, now: now}
}

func (s *sessions) issue(forge string, claims adapters.Claims) (token string, expiry time.Time) {
	expiry = s.now().Add(s.ttl)
	payload, err := json.Marshal(sessionPayload{Forge: forge, Expiry: expiry.Unix(), Claims: claims})
	if err != nil {
		panic(err) // plain strings and numbers always encode
	}

	body := base64.RawURLEncoding.EncodeToString(payload)
	return sessionPrefix + body + "." + s.sign(body), expiry
}

// parse checks the signature and the expiry, and returns what the token carries.
func (s *sessions) parse(token string) (forge string, claims adapters.Claims, err error) {
	body, signature, found := strings.Cut(strings.TrimPrefix(token, sessionPrefix), ".")
	if !found || !hmac.Equal([]byte(signature), []byte(s.sign(body))) {
		return "", adapters.Claims{}, ErrInvalidSession
	}

	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", adapters.Claims{}, ErrInvalidSession
	}
	var payload sessionPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", adapters.Claims{}, fmt.Errorf("%w: %v", ErrInvalidSession, err)
	}
	if !s.now().Before(time.Unix(payload.Expiry, 0)) {
		return "", adapters.Claims{}, ErrSessionExpired
	}
	return payload.Forge, payload.Claims, nil
}

func (s *sessions) sign(body string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
