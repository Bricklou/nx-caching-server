// What the middleware needs from a kind of credential: recognize its tokens and turn them into identities.
// Kinds (forge tokens, session tokens, ...) live in their own packages and are combined with Chain.
package auth

import (
	"context"
	"errors"
)

var (
	// ErrUnsupportedToken is returned when no authenticator recognizes the token.
	ErrUnsupportedToken = errors.New("auth: token is not of a supported kind")
	// ErrRepoNotAllowed means a valid credential belongs to a repository that is not allowed.
	ErrRepoNotAllowed = errors.New("auth: repository is not allowed")
)

// Authenticator verifies one kind of bearer token.
type Authenticator interface {
	// Accepts reports whether raw has the shape of this kind of token, without verifying it.
	Accepts(raw string) bool
	// Authenticate verifies raw and returns the identity it grants.
	Authenticate(ctx context.Context, raw string) (*Identity, error)
}

type chain []Authenticator

// Chain returns an Authenticator that hands each token to the first
// authenticator that accepts it.
func Chain(authenticators ...Authenticator) Authenticator {
	return chain(authenticators)
}

func (c chain) Accepts(raw string) bool {
	for _, a := range c {
		if a.Accepts(raw) {
			return true
		}
	}
	return false
}

func (c chain) Authenticate(ctx context.Context, raw string) (*Identity, error) {
	for _, a := range c {
		if a.Accepts(raw) {
			return a.Authenticate(ctx, raw)
		}
	}
	return nil, ErrUnsupportedToken
}
