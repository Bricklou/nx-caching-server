// Tests session tokens: signing, expiry, the exchange of a forge token, and what a session may and may not do.
// A session outlives the forge token it came from but never grants more than the configuration allows.
package auth

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"nx-caching-server/internal/auth/adapters"
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
	s := newSessions(testSecret, time.Hour, func() time.Time { return now })

	token, expiry := s.issue("github", sampleClaims())
	forge, claims, err := s.parse(token)

	if err != nil || forge != "github" || claims != sampleClaims() {
		t.Errorf("parse = (%q, %+v, %v)", forge, claims, err)
	}
	if !strings.HasPrefix(token, sessionPrefix) || strings.Count(token, ".") != 1 {
		t.Errorf("token %q must have the session prefix and one dot, so it is never taken for a JWT", token)
	}
	if !expiry.Equal(now.Add(time.Hour)) {
		t.Errorf("expiry = %v", expiry)
	}
}

func TestSessionExpiry(t *testing.T) {
	now := time.Now()
	s := newSessions(testSecret, time.Hour, func() time.Time { return now })
	token, _ := s.issue("github", sampleClaims())

	now = now.Add(time.Hour - time.Second)
	if _, _, err := s.parse(token); err != nil {
		t.Errorf("a token before its expiry was refused: %v", err)
	}
	now = now.Add(time.Second)
	if _, _, err := s.parse(token); !errors.Is(err, ErrSessionExpired) {
		t.Errorf("error = %v, want ErrSessionExpired", err)
	}
}

func TestSessionRefusesForgedTokens(t *testing.T) {
	s := newSessions(testSecret, time.Hour, time.Now)
	other := newSessions([]byte(strings.Repeat("x", 32)), time.Hour, time.Now)
	token, _ := s.issue("github", sampleClaims())
	body, signature, _ := strings.Cut(strings.TrimPrefix(token, sessionPrefix), ".")

	// A payload that grants another repository, with the original signature.
	forgedClaims := sampleClaims()
	forgedClaims.RepoID = "999"
	forgedToken, _ := s.issue("github", forgedClaims)
	forgedBody, _, _ := strings.Cut(strings.TrimPrefix(forgedToken, sessionPrefix), ".")

	tests := map[string]string{
		"signed with another key": mustIssue(other),
		"payload swapped":         sessionPrefix + forgedBody + "." + signature,
		"signature removed":       sessionPrefix + body,
		"signature empty":         sessionPrefix + body + ".",
		"payload not base64":      sessionPrefix + "%%%." + signature,
		"empty":                   sessionPrefix,
	}
	for name, raw := range tests {
		if _, _, err := s.parse(raw); !errors.Is(err, ErrInvalidSession) {
			t.Errorf("%s: error = %v, want ErrInvalidSession", name, err)
		}
	}
}

func mustIssue(s *sessions) string {
	token, _ := s.issue("github", sampleClaims())
	return token
}

func TestSessionsWithoutASecretDrawARandomKey(t *testing.T) {
	a := newSessions(nil, time.Hour, time.Now)
	b := newSessions(nil, time.Hour, time.Now)

	token, _ := a.issue("github", sampleClaims())

	if _, _, err := a.parse(token); err != nil {
		t.Errorf("its own token was refused: %v", err)
	}
	if _, _, err := b.parse(token); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("another instance accepted the token: %v", err)
	}
}

// exchanged returns a verifier, the clock it uses, and a session for a push to main.
func exchanged(t *testing.T, tf *testForge, ttl time.Duration) (*Verifier, *time.Time, Session) {
	t.Helper()
	clock := time.Now()
	v := newVerifier(tf.config(), func() time.Time { return clock }, WithSessions(ttl, testSecret))

	session, err := v.Exchange(t.Context(), tf.token(t, nil))
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	return v, &clock, session
}

func TestSessionGrantsTheIdentityOfTheForgeToken(t *testing.T) {
	tf := newTestForge(t)
	v, _, session := exchanged(t, tf, time.Hour)

	fromSession, err := verify(t, v, session.Token)
	if err != nil {
		t.Fatalf("session refused: %v", err)
	}
	fromForge, err := verify(t, v, tf.token(t, nil))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fromSession, fromForge) {
		t.Errorf("session identity %+v differs from the forge token identity %+v", fromSession, fromForge)
	}
}

func TestSessionOutlivesTheForgeToken(t *testing.T) {
	tf := newTestForge(t)
	v, clock, session := exchanged(t, tf, 2*time.Hour)
	forgeToken := tf.token(t, nil)

	*clock = clock.Add(time.Hour) // the forge token lives 10 minutes by default

	if _, err := verify(t, v, forgeToken); err == nil {
		t.Error("the forge token was still accepted after its expiry")
	}
	if _, err := verify(t, v, session.Token); err != nil {
		t.Errorf("the session was refused while valid: %v", err)
	}

	*clock = clock.Add(time.Hour + time.Second)
	if _, err := verify(t, v, session.Token); !errors.Is(err, ErrSessionExpired) {
		t.Errorf("error = %v, want ErrSessionExpired", err)
	}
}

func TestExchangeRefusesWhatTheCacheWouldRefuse(t *testing.T) {
	tf := newTestForge(t)
	v := NewVerifier(tf.config(), WithSessions(time.Hour, testSecret))

	tests := []struct {
		name string
		raw  string
		want error
	}{
		{"repository not allowed", tf.token(t, func(s *devforge.GitHubToken) { s.RepoID = "999" }), ErrRepoNotAllowed},
		{"branch denied by a rule", tf.token(t, func(s *devforge.GitHubToken) { s.Ref = "refs/heads/forbidden/x" }), scope.ErrDenied},
		{"unknown issuer", withIssuer(t, tf, "https://evil.example"), ErrUnknownIssuer},
		{"not a token", "garbage", ErrMalformedToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := v.Exchange(t.Context(), tt.raw); !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestExchangeRefusesAnExpiredForgeToken(t *testing.T) {
	tf := newTestForge(t)
	clock := time.Now()
	v := newVerifier(tf.config(), func() time.Time { return clock }, WithSessions(time.Hour, testSecret))
	raw := tf.token(t, nil)

	clock = clock.Add(time.Hour)

	if _, err := v.Exchange(t.Context(), raw); err == nil {
		t.Error("an expired forge token was exchanged")
	}
}

func TestASessionCannotBeExchangedForAnotherOne(t *testing.T) {
	tf := newTestForge(t)
	v, _, session := exchanged(t, tf, time.Hour)

	if _, err := v.Exchange(t.Context(), session.Token); err == nil {
		t.Error("a session token was exchanged, which would let it renew itself forever")
	}
}

func TestSessionIsResolvedAgainstTheCurrentConfiguration(t *testing.T) {
	tf := newTestForge(t)
	_, _, session := exchanged(t, tf, time.Hour)

	// A restarted server with the same secret and the repository removed.
	cfg := tf.config()
	cfg.Forges[0].Repos = nil
	restarted := NewVerifier(cfg, WithSessions(time.Hour, testSecret))

	if _, err := verify(t, restarted, session.Token); !errors.Is(err, ErrRepoNotAllowed) {
		t.Errorf("error = %v, want ErrRepoNotAllowed", err)
	}
}

func TestSessionOfAForgeThatIsNoLongerConfigured(t *testing.T) {
	tf := newTestForge(t)
	_, _, session := exchanged(t, tf, time.Hour)

	cfg := tf.config()
	cfg.Forges[0].Name = "renamed"
	restarted := NewVerifier(cfg, WithSessions(time.Hour, testSecret))

	if _, err := verify(t, restarted, session.Token); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("error = %v, want ErrInvalidSession", err)
	}
}

func TestSessionWithAnotherSecretIsRefused(t *testing.T) {
	tf := newTestForge(t)
	_, _, session := exchanged(t, tf, time.Hour)
	other := NewVerifier(tf.config(), WithSessions(time.Hour, []byte(strings.Repeat("z", 32))))

	if _, err := verify(t, other, session.Token); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("error = %v, want ErrInvalidSession", err)
	}
}
