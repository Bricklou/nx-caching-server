// Session tokens: a signed copy of the claims of a verified forge token, valid for longer than the forge token.
// A job exchanges its short-lived token once and runs with the session token; access is resolved again on every request.
package session

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/auth/oidc"
	"nx-caching-server/internal/auth/oidc/adapters"
)

// prefix tells a session token from a forge token, which has two dots.
const prefix = "nxs_"

var (
	ErrInvalid = errors.New("session: invalid token")
	ErrExpired = errors.New("session: token expired")
)

// sessionPayload is what a session token carries: the forge that vouched for
// the claims, and when the token stops being valid. It holds no access rights.
type sessionPayload struct {
	Forge  string          `json:"f"`
	Expiry int64           `json:"e"`
	Claims adapters.Claims `json:"c"`
}

// Session is a session token and the moment it stops being valid.
type Session struct {
	Token  string
	Expiry time.Time
}

// Manager issues and verifies session tokens. It implements auth.Authenticator.
type Manager struct {
	verifier *oidc.Verifier
	key      []byte
	ttl      time.Duration
	now      func() time.Time
}

// Option customizes a Manager.
type Option func(*Manager)

// WithClock sets the clock that dates and expires tokens. It exists for tests.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) { m.now = now }
}

// New returns a manager whose sessions last ttl and are signed with key, or with
// a random key when it is empty. The verifier checks the forge tokens that are
// exchanged and resolves the access of each session.
func New(verifier *oidc.Verifier, ttl time.Duration, key []byte, opts ...Option) *Manager {
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic(err) // the system source of randomness is unavailable
		}
	}

	m := &Manager{verifier: verifier, key: key, ttl: ttl, now: time.Now}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Accepts reports whether raw carries the session token prefix.
func (m *Manager) Accepts(raw string) bool {
	return strings.HasPrefix(raw, prefix)
}

// Authenticate checks a session token and resolves its claims again, so a
// change of configuration applies to tokens already issued.
func (m *Manager) Authenticate(_ context.Context, raw string) (*auth.Identity, error) {
	forge, claims, err := m.parse(raw)
	if err != nil {
		return nil, err
	}
	return m.verifier.Identity(forge, claims)
}

// Exchange verifies a forge token and returns a session token for the same
// claims. A session token is refused: it cannot be exchanged to live longer.
// jobToken, the CI_JOB_TOKEN of a GitLab job, is optional: it lets the server
// ask GitLab which merge request the job runs for, and is used once, never kept.
func (m *Manager) Exchange(ctx context.Context, raw, jobToken string) (Session, error) {
	verified, err := m.verifier.Verify(ctx, raw)
	if err != nil {
		return Session{}, err
	}
	verified = m.verifier.WithMergeRequest(ctx, verified, jobToken)
	// Refuse here the jobs that would be refused on every request.
	if _, err := m.verifier.Identity(verified.Forge, verified.Claims); err != nil {
		return Session{}, err
	}

	token, expiry := m.issue(verified.Forge, verified.Claims)
	return Session{Token: token, Expiry: expiry}, nil
}

func (m *Manager) issue(forge string, claims adapters.Claims) (token string, expiry time.Time) {
	expiry = m.now().Add(m.ttl)
	payload, err := json.Marshal(sessionPayload{Forge: forge, Expiry: expiry.Unix(), Claims: claims})
	if err != nil {
		panic(err) // plain strings and numbers always encode
	}

	body := base64.RawURLEncoding.EncodeToString(payload)
	return prefix + body + "." + m.sign(body), expiry
}

// parse checks the signature and the expiry, and returns what the token carries.
func (m *Manager) parse(token string) (forge string, claims adapters.Claims, err error) {
	body, signature, found := strings.Cut(strings.TrimPrefix(token, prefix), ".")
	if !found || !hmac.Equal([]byte(signature), []byte(m.sign(body))) {
		return "", adapters.Claims{}, ErrInvalid
	}

	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", adapters.Claims{}, ErrInvalid
	}
	var payload sessionPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", adapters.Claims{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if !m.now().Before(time.Unix(payload.Expiry, 0)) {
		return "", adapters.Claims{}, ErrExpired
	}
	return payload.Forge, payload.Claims, nil
}

func (m *Manager) sign(body string) string {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
