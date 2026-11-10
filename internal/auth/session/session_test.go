// Tests session tokens: signing, expiry, the exchange of a forge token, and what a session may and may not do.
// A session outlives the forge token it came from but never grants more than the configuration allows.
package session

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/auth/oidc"
	"nx-caching-server/internal/auth/oidc/adapters"
	"nx-caching-server/internal/auth/oidc/oidctest"
	"nx-caching-server/internal/config"
	"nx-caching-server/internal/devforge"
	"nx-caching-server/internal/scope"
)

var testSecret = []byte(strings.Repeat("k", 32))

func sampleClaims() adapters.Claims {
	return adapters.Claims{
		RepoID: "123456", RepoPath: "my-org/app", Actor: "louis", Ref: "refs/pull/7/merge", Event: "pull_request",
		Scope: scope.PullRequestNumber(7, "main", "louis"),
	}
}

func TestSessionRoundTrip(t *testing.T) {
	now := time.Now()
	m := New(nil, time.Hour, testSecret, WithClock(func() time.Time { return now }))

	token, expiry := m.issue("github", sampleClaims())
	forge, claims, err := m.parse(token)

	if err != nil || forge != "github" || claims != sampleClaims() {
		t.Errorf("parse = (%q, %+v, %v)", forge, claims, err)
	}
	if !strings.HasPrefix(token, prefix) || strings.Count(token, ".") != 1 {
		t.Errorf("token %q must have the session prefix and one dot, so it is never taken for a JWT", token)
	}
	if !m.Accepts(token) || m.Accepts("a.b.c") {
		t.Error("Accepts must recognize session tokens only")
	}
	if !expiry.Equal(now.Add(time.Hour)) {
		t.Errorf("expiry = %v", expiry)
	}
}

func TestSessionExpiry(t *testing.T) {
	now := time.Now()
	m := New(nil, time.Hour, testSecret, WithClock(func() time.Time { return now }))
	token, _ := m.issue("github", sampleClaims())

	now = now.Add(time.Hour - time.Second)
	if _, _, err := m.parse(token); err != nil {
		t.Errorf("a token before its expiry was refused: %v", err)
	}
	now = now.Add(time.Second)
	if _, _, err := m.parse(token); !errors.Is(err, ErrExpired) {
		t.Errorf("error = %v, want ErrExpired", err)
	}
}

func TestSessionRefusesForgedTokens(t *testing.T) {
	m := New(nil, time.Hour, testSecret)
	other := New(nil, time.Hour, []byte(strings.Repeat("x", 32)))
	token, _ := m.issue("github", sampleClaims())
	body, signature, _ := strings.Cut(strings.TrimPrefix(token, prefix), ".")

	// A payload that grants another repository, with the original signature.
	forgedClaims := sampleClaims()
	forgedClaims.RepoID = "999"
	forgedToken, _ := m.issue("github", forgedClaims)
	forgedBody, _, _ := strings.Cut(strings.TrimPrefix(forgedToken, prefix), ".")

	otherToken, _ := other.issue("github", sampleClaims())
	tests := map[string]string{
		"signed with another key": otherToken,
		"payload swapped":         prefix + forgedBody + "." + signature,
		"signature removed":       prefix + body,
		"signature empty":         prefix + body + ".",
		"payload not base64":      prefix + "%%%." + signature,
		"empty":                   prefix,
	}
	for name, raw := range tests {
		if _, _, err := m.parse(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
	}
}

func TestSessionsWithoutASecretDrawARandomKey(t *testing.T) {
	a := New(nil, time.Hour, nil)
	b := New(nil, time.Hour, nil)

	token, _ := a.issue("github", sampleClaims())

	if _, _, err := a.parse(token); err != nil {
		t.Errorf("its own token was refused: %v", err)
	}
	if _, _, err := b.parse(token); !errors.Is(err, ErrInvalid) {
		t.Errorf("another instance accepted the token: %v", err)
	}
}

func authenticate(t *testing.T, m *Manager, raw string) (*auth.Identity, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.Authenticate(ctx, raw)
}

// restarted is a manager of a freshly started server with the same signing key.
func restarted(cfg *config.AuthConfig) *Manager {
	return New(oidc.NewVerifier(cfg), time.Hour, testSecret)
}

// exchanged returns a manager, the clock it uses, and a session for a push to main.
func exchanged(t *testing.T, tf *oidctest.Forge, ttl time.Duration) (*Manager, *time.Time, Session) {
	t.Helper()
	clock := time.Now()
	now := func() time.Time { return clock }
	m := New(oidc.NewVerifier(tf.Config(), oidc.WithClock(now)), ttl, testSecret, WithClock(now))

	session, err := m.Exchange(t.Context(), tf.Token(t, nil))
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	return m, &clock, session
}

func TestSessionGrantsTheIdentityOfTheForgeToken(t *testing.T) {
	tf := oidctest.New(t)
	m, _, session := exchanged(t, tf, time.Hour)
	forge := oidc.NewVerifier(tf.Config())

	fromSession, err := authenticate(t, m, session.Token)
	if err != nil {
		t.Fatalf("session refused: %v", err)
	}
	fromForge, err := forge.Authenticate(t.Context(), tf.Token(t, nil))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fromSession, fromForge) {
		t.Errorf("session identity %+v differs from the forge token identity %+v", fromSession, fromForge)
	}
}

func TestSessionOutlivesTheForgeToken(t *testing.T) {
	tf := oidctest.New(t)
	clock := time.Now()
	now := func() time.Time { return clock }
	verifier := oidc.NewVerifier(tf.Config(), oidc.WithClock(now))
	m := New(verifier, 2*time.Hour, testSecret, WithClock(now))
	forgeToken := tf.Token(t, nil)
	session, err := m.Exchange(t.Context(), forgeToken)
	if err != nil {
		t.Fatal(err)
	}

	clock = clock.Add(time.Hour) // the forge token lives 10 minutes by default

	if _, err := verifier.Authenticate(t.Context(), forgeToken); err == nil {
		t.Error("the forge token was still accepted after its expiry")
	}
	if _, err := authenticate(t, m, session.Token); err != nil {
		t.Errorf("the session was refused while valid: %v", err)
	}

	clock = clock.Add(time.Hour + time.Second)
	if _, err := authenticate(t, m, session.Token); !errors.Is(err, ErrExpired) {
		t.Errorf("error = %v, want ErrExpired", err)
	}
}

func TestExchangeRefusesWhatTheCacheWouldRefuse(t *testing.T) {
	tf := oidctest.New(t)
	m := New(oidc.NewVerifier(tf.Config()), time.Hour, testSecret)

	tests := []struct {
		name string
		raw  string
		want error
	}{
		{"repository not allowed", tf.Token(t, func(s *devforge.GitHubToken) { s.RepoID = "999" }), auth.ErrRepoNotAllowed},
		{"branch denied by a rule", tf.Token(t, func(s *devforge.GitHubToken) { s.Ref = "refs/heads/forbidden/x" }), scope.ErrDenied},
		{"not a token", "garbage", oidc.ErrMalformedToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := m.Exchange(t.Context(), tt.raw); !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestExchangeRefusesAnExpiredForgeToken(t *testing.T) {
	tf := oidctest.New(t)
	clock := time.Now()
	m := New(oidc.NewVerifier(tf.Config(), oidc.WithClock(func() time.Time { return clock })), time.Hour, testSecret)
	raw := tf.Token(t, nil)

	clock = clock.Add(time.Hour)

	if _, err := m.Exchange(t.Context(), raw); err == nil {
		t.Error("an expired forge token was exchanged")
	}
}

func TestASessionCannotBeExchangedForAnotherOne(t *testing.T) {
	tf := oidctest.New(t)
	m, _, session := exchanged(t, tf, time.Hour)

	if _, err := m.Exchange(t.Context(), session.Token); err == nil {
		t.Error("a session token was exchanged, which would let it renew itself forever")
	}
}

func TestSessionIsResolvedAgainstTheCurrentConfiguration(t *testing.T) {
	tf := oidctest.New(t)
	_, _, session := exchanged(t, tf, time.Hour)

	// A restarted server with the same secret and the repository removed.
	cfg := tf.Config()
	cfg.Forges[0].Repos = nil

	if _, err := authenticate(t, restarted(cfg), session.Token); !errors.Is(err, auth.ErrRepoNotAllowed) {
		t.Errorf("error = %v, want ErrRepoNotAllowed", err)
	}
}

func TestSessionOfAForgeThatIsNoLongerConfigured(t *testing.T) {
	tf := oidctest.New(t)
	_, _, session := exchanged(t, tf, time.Hour)

	cfg := tf.Config()
	cfg.Forges[0].Name = "renamed"

	if _, err := authenticate(t, restarted(cfg), session.Token); !errors.Is(err, oidc.ErrUnknownForge) {
		t.Errorf("error = %v, want ErrUnknownForge", err)
	}
}

func TestSessionWithAnotherSecretIsRefused(t *testing.T) {
	tf := oidctest.New(t)
	_, _, session := exchanged(t, tf, time.Hour)
	other := New(oidc.NewVerifier(tf.Config()), time.Hour, []byte(strings.Repeat("z", 32)))

	if _, err := authenticate(t, other, session.Token); !errors.Is(err, ErrInvalid) {
		t.Errorf("error = %v, want ErrInvalid", err)
	}
}
