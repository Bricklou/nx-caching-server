// Tests Chain: a token goes to the first authenticator that accepts it, and to none when nobody does.
// The authenticators are stubs, so the core package is tested without any credential kind.
package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stub struct {
	prefix string
	calls  int
	err    error
}

func (s *stub) Accepts(raw string) bool { return strings.HasPrefix(raw, s.prefix) }

func (s *stub) Authenticate(_ context.Context, raw string) (*Identity, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &Identity{Actor: s.prefix}, nil
}

func TestChainHandsTheTokenToTheFirstAcceptingAuthenticator(t *testing.T) {
	a, b, both := &stub{prefix: "a_"}, &stub{prefix: "b_"}, &stub{prefix: ""}
	chain := Chain(a, b, both)

	for raw, want := range map[string]string{"a_1": "a_", "b_1": "b_", "other": ""} {
		id, err := chain.Authenticate(context.Background(), raw)
		if err != nil || id.Actor != want {
			t.Errorf("%q: got (%+v, %v), want actor %q", raw, id, err, want)
		}
	}
	if a.calls != 1 || b.calls != 1 || both.calls != 1 {
		t.Errorf("calls = %d, %d, %d, want one each", a.calls, b.calls, both.calls)
	}
}

func TestChainRefusesATokenNobodyAccepts(t *testing.T) {
	chain := Chain(&stub{prefix: "a_"})

	if chain.Accepts("b_1") {
		t.Error("Accepts is true for an unknown kind of token")
	}
	if _, err := chain.Authenticate(context.Background(), "b_1"); !errors.Is(err, ErrUnsupportedToken) {
		t.Errorf("error = %v, want ErrUnsupportedToken", err)
	}
}

func TestChainReturnsTheErrorOfTheAcceptingAuthenticator(t *testing.T) {
	boom := errors.New("boom")
	chain := Chain(&stub{prefix: "a_", err: boom}, &stub{prefix: ""})

	// The later authenticator must not get a second chance at a token the first one claimed.
	if _, err := chain.Authenticate(context.Background(), "a_1"); !errors.Is(err, boom) {
		t.Errorf("error = %v, want boom", err)
	}
}

func TestEmptyChainAcceptsNothing(t *testing.T) {
	if Chain().Accepts("x") {
		t.Error("an empty chain accepted a token")
	}
}
