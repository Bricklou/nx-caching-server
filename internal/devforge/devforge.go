// Fake git forge OIDC issuer built on mockoidc, minting GitHub, GitLab and Forgejo shaped tokens.
// Used by tests and by the dev tool to exercise auth without a real CI job.
package devforge

import (
	"crypto/rand"
	"crypto/rsa"
	"net"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/oauth2-proxy/mockoidc"
)

// DefaultTTL is the lifetime of minted tokens when none is given.
const DefaultTTL = 10 * time.Minute

// Forge is a running mock OIDC server. It serves discovery and the key set;
// tokens are minted directly with Mint, MintGitHub, MintGitLab and MintForgejo.
type Forge struct {
	*mockoidc.MockOIDC
}

// Option customizes a Forge before it starts.
type Option func(*Forge) error

// WithMiddleware wraps every endpoint of the server, e.g. to count requests.
func WithMiddleware(mw func(http.Handler) http.Handler) Option {
	return func(f *Forge) error { return f.AddMiddleware(mw) }
}

// Run starts a forge on a random local port, signing with a fresh key.
func Run(opts ...Option) (*Forge, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		ln.Close()
		return nil, err
	}
	return start(ln, key, opts)
}

// Listen starts a forge on ln, signing with the key built into mockoidc, so
// tokens minted by DefaultSigner stay valid across restarts.
func Listen(ln net.Listener, opts ...Option) (*Forge, error) {
	return start(ln, nil, opts)
}

func start(ln net.Listener, key *rsa.PrivateKey, opts []Option) (*Forge, error) {
	server, err := mockoidc.NewServer(key)
	if err != nil {
		ln.Close()
		return nil, err
	}

	forge := &Forge{MockOIDC: server}
	for _, opt := range opts {
		if err := opt(forge); err != nil {
			ln.Close()
			return nil, err
		}
	}
	if err := server.Start(ln, nil); err != nil {
		ln.Close()
		return nil, err
	}
	return forge, nil
}

// Close stops the server.
func (f *Forge) Close() error { return f.Shutdown() }

// KeyID returns the id of the signing key published in the key set.
func (f *Forge) KeyID() string {
	id, err := f.Keypair.KeyID()
	if err != nil {
		panic(err) // only fails when the public key cannot be encoded
	}
	return id
}

// Signer returns a signer for this forge. It reads the key and clock of the
// server on each use, so FastForward and a modified Kid apply.
func (f *Forge) Signer() *Signer {
	return &Signer{issuer: f.Issuer(), keypair: f.Keypair, now: f.Now}
}

// Mint signs claims as given, without adding any.
func (f *Forge) Mint(claims map[string]any) (string, error) {
	return f.Signer().Mint(claims)
}

// Signer mints tokens for one issuer.
type Signer struct {
	issuer  string
	keypair *mockoidc.Keypair
	now     func() time.Time
}

// DefaultSigner signs with the key built into mockoidc, the one used by Listen.
func DefaultSigner(issuer string) (*Signer, error) {
	keypair, err := mockoidc.DefaultKeypair()
	if err != nil {
		return nil, err
	}
	return &Signer{issuer: issuer, keypair: keypair, now: time.Now}, nil
}

// Issuer returns the issuer put in the iss claim of minted tokens.
func (s *Signer) Issuer() string { return s.issuer }

// Mint signs claims as given, without adding any.
func (s *Signer) Mint(claims map[string]any) (string, error) {
	return s.keypair.SignJWT(jwt.MapClaims(claims))
}
