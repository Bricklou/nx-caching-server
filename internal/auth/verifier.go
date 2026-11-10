// Verifies forge OIDC tokens and resolves them to a cache Identity.
// Checks signature, issuer, audience and expiry, then the repository allowlist.
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"nx-caching-server/internal/auth/adapters"
	"nx-caching-server/internal/config"
	"nx-caching-server/internal/scope"
)

// Failure reasons. Callers answer 401 for all of them and log the reason.
var (
	ErrMalformedToken   = errors.New("auth: malformed token")
	ErrUnknownIssuer    = errors.New("auth: issuer is not configured")
	ErrUnsupportedForge = errors.New("auth: forge type has no claim adapter yet")
	ErrRepoNotAllowed   = errors.New("auth: repository is not allowed")
)

// signingAlgs are the only accepted signature algorithms.
var signingAlgs = []string{"RS256", "ES256"}

const (
	discoveryTimeout = 10 * time.Second
	// discoveryCooldown is how long a failed discovery is remembered, so a forge
	// that is down does not make every request wait for the timeout.
	discoveryCooldown = 5 * time.Second
)

// Verifier turns forge tokens and session tokens into identities.
type Verifier struct {
	byIssuer map[string]*forgeVerifier
	byName   map[string]*forgeVerifier
	sessions *sessions
}

// VerifierOption customizes a Verifier.
type VerifierOption func(*verifierSettings)

type verifierSettings struct {
	sessionTTL    time.Duration
	sessionSecret []byte
}

// WithSessions sets how long session tokens live and the key that signs them.
// An empty secret means a random key, lost when the process stops.
func WithSessions(ttl time.Duration, secret []byte) VerifierOption {
	return func(s *verifierSettings) { s.sessionTTL, s.sessionSecret = ttl, secret }
}

// Session is a session token and the moment it stops being valid.
type Session struct {
	Token  string
	Expiry time.Time
}

// NewVerifier builds a verifier for the forges of cfg. Nothing is fetched
// until a token arrives, so a forge being down at startup is not fatal.
func NewVerifier(cfg *config.AuthConfig, opts ...VerifierOption) *Verifier {
	return newVerifier(cfg, time.Now, opts...)
}

// newVerifier takes the clock used to check token expiry and the discovery
// cooldown, which tests control.
func newVerifier(cfg *config.AuthConfig, now func() time.Time, opts ...VerifierOption) *Verifier {
	client := &http.Client{Timeout: discoveryTimeout}

	settings := verifierSettings{sessionTTL: config.DefaultSessionTTL}
	for _, opt := range opts {
		opt(&settings)
	}

	v := &Verifier{
		byIssuer: make(map[string]*forgeVerifier, len(cfg.Forges)),
		byName:   make(map[string]*forgeVerifier, len(cfg.Forges)),
		sessions: newSessions(settings.sessionSecret, settings.sessionTTL, now),
	}
	for _, forge := range cfg.Forges {
		adapt, ok := adapters.For(forge.Type)
		if !ok {
			slog.Warn("forge type has no claim adapter, its tokens will be rejected",
				slog.String("forge", forge.Name), slog.String("type", forge.Type))
		}
		fv := &forgeVerifier{forge: forge, adapt: adapt, client: client, now: now}
		v.byIssuer[forge.Issuer] = fv
		v.byName[forge.Name] = fv
	}
	return v
}

// IsToken reports whether raw looks like a JWT rather than a static token.
func IsToken(raw string) bool {
	return strings.Count(raw, ".") == 2 && !strings.ContainsAny(raw, " \t\r\n")
}

// Verify checks raw, a forge token or a session token, and returns the identity it grants.
func (v *Verifier) Verify(ctx context.Context, raw string) (*Identity, error) {
	if strings.HasPrefix(raw, sessionPrefix) {
		return v.verifySession(raw)
	}

	fv, err := v.forgeFor(raw)
	if err != nil {
		return nil, err
	}
	claims, err := fv.verifiedClaims(ctx, raw)
	if err != nil {
		return nil, err
	}
	return fv.identityFor(claims)
}

// Exchange verifies a forge token and returns a session token for the same
// claims. A session token is refused: it cannot be exchanged to live longer.
func (v *Verifier) Exchange(ctx context.Context, raw string) (Session, error) {
	fv, err := v.forgeFor(raw)
	if err != nil {
		return Session{}, err
	}
	claims, err := fv.verifiedClaims(ctx, raw)
	if err != nil {
		return Session{}, err
	}
	// Refuse here the jobs that would be refused on every request.
	if _, err := fv.identityFor(claims); err != nil {
		return Session{}, err
	}

	token, expiry := v.sessions.issue(fv.forge.Name, claims)
	return Session{Token: token, Expiry: expiry}, nil
}

// verifySession checks a session token and resolves its claims again, so a
// change of configuration applies to tokens already issued.
func (v *Verifier) verifySession(raw string) (*Identity, error) {
	forge, claims, err := v.sessions.parse(raw)
	if err != nil {
		return nil, err
	}
	fv, ok := v.byName[forge]
	if !ok {
		return nil, fmt.Errorf("%w: forge %q is no longer configured", ErrInvalidSession, forge)
	}
	return fv.identityFor(claims)
}

// forgeFor picks the configured forge that must verify raw.
func (v *Verifier) forgeFor(raw string) (*forgeVerifier, error) {
	issuer, err := peekIssuer(raw)
	if err != nil {
		return nil, err
	}

	fv, ok := v.byIssuer[issuer]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownIssuer, issuer)
	}
	if fv.adapt == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedForge, fv.forge.Type)
	}
	return fv, nil
}

// peekIssuer reads the issuer of an unverified token to pick the forge that
// verifies it; the verifier checks the issuer again.
func peekIssuer(raw string) (string, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("%w: expected 3 parts", ErrMalformedToken)
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("%w: payload is not base64url", ErrMalformedToken)
	}

	var claims struct {
		Issuer string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Issuer == "" {
		return "", fmt.Errorf("%w: no issuer", ErrMalformedToken)
	}
	return claims.Issuer, nil
}

// forgeVerifier verifies the tokens of one configured forge.
type forgeVerifier struct {
	forge  config.Forge
	adapt  adapters.Adapter // nil when the forge type has no adapter
	client *http.Client
	now    func() time.Time

	// verifier is built on first use and then read without a lock.
	verifier atomic.Pointer[oidc.IDTokenVerifier]

	mu       sync.Mutex // guards the build and the two fields below
	failure  error
	failedAt time.Time
}

// verifiedClaims checks the signature, issuer, audience and expiry of raw,
// then maps its claims.
func (f *forgeVerifier) verifiedClaims(ctx context.Context, raw string) (adapters.Claims, error) {
	verifier, err := f.get(ctx)
	if err != nil {
		return adapters.Claims{}, err
	}
	token, err := verifier.Verify(ctx, raw)
	if err != nil {
		return adapters.Claims{}, err
	}

	var payload json.RawMessage
	if err := token.Claims(&payload); err != nil {
		return adapters.Claims{}, fmt.Errorf("%w: %v", ErrMalformedToken, err)
	}
	return f.adapt(payload)
}

// identityFor checks the repository allowlist and resolves what the job may do.
func (f *forgeVerifier) identityFor(claims adapters.Claims) (*Identity, error) {
	repo, ok := f.repository(claims.RepoID)
	if !ok {
		return nil, fmt.Errorf("%w: id %q (%s) on forge %q", ErrRepoNotAllowed, claims.RepoID, claims.RepoPath, f.forge.Name)
	}
	access, err := scope.Resolve(repo, claims.Scope)
	if err != nil {
		return nil, err
	}

	return &Identity{
		Forge:  ForgeInfo{Name: f.forge.Name, Type: f.forge.Type, URL: f.forge.Issuer},
		Repo:   RepoInfo{ID: claims.RepoID, Path: claims.RepoPath},
		Actor:  claims.Actor,
		Ref:    claims.Ref,
		Event:  claims.Event,
		Access: access,
	}, nil
}

func (f *forgeVerifier) repository(id string) (config.Repository, bool) {
	for _, repo := range f.forge.Repos {
		if repo.ID == id {
			return repo, true
		}
	}
	return config.Repository{}, false
}

// get returns the OIDC verifier, built on first use then read without a lock. A
// failed discovery is remembered for discoveryCooldown so requests fail fast.
func (f *forgeVerifier) get(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	if verifier := f.verifier.Load(); verifier != nil {
		return verifier, nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if verifier := f.verifier.Load(); verifier != nil { // built while we waited
		return verifier, nil
	}
	if f.failure != nil && f.now().Sub(f.failedAt) < discoveryCooldown {
		return nil, f.failure
	}

	verifier, err := f.build(ctx)
	if err != nil {
		f.failure, f.failedAt = err, f.now()
		return nil, err
	}
	f.failure = nil
	f.verifier.Store(verifier)
	return verifier, nil
}

// build creates the OIDC verifier, discovering the forge's keys unless a
// jwks_url is configured.
func (f *forgeVerifier) build(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	// The key set keeps this context for its later fetches, so it carries the
	// client but no deadline.
	base := oidc.ClientContext(context.WithoutCancel(ctx), f.client)
	cfg := &oidc.Config{
		ClientID:             f.forge.Audience,
		SupportedSigningAlgs: signingAlgs,
		Now:                  f.now,
	}

	if f.forge.JWKSURL != "" {
		return oidc.NewVerifier(f.forge.Issuer, newKeySet(f.forge.JWKSURL, f.client, f.now), cfg), nil
	}

	discoveryCtx, cancel := context.WithTimeout(base, discoveryTimeout)
	defer cancel()
	provider, err := oidc.NewProvider(discoveryCtx, f.forge.Issuer)
	if err != nil {
		return nil, fmt.Errorf("auth: discovery of forge %q failed: %w", f.forge.Name, err)
	}

	var metadata struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := provider.Claims(&metadata); err != nil || metadata.JWKSURI == "" {
		return nil, fmt.Errorf("auth: discovery of forge %q lists no jwks_uri", f.forge.Name)
	}
	return oidc.NewVerifier(f.forge.Issuer, newKeySet(metadata.JWKSURI, f.client, f.now), cfg), nil
}
