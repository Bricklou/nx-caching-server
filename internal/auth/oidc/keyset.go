// Signing keys of one forge, downloaded from its JWKS URL.
// An unknown key id triggers at most one download per minRefresh, so bad tokens cannot make the server hammer a forge.
package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const (
	// minRefresh is the shortest time between two key downloads.
	minRefresh = 30 * time.Second
	// maxKeySetSize bounds the key set a forge may send.
	maxKeySetSize = 1 << 20
)

var errUnknownKey = errors.New("oidc: signing key is not published by the forge")

// keySet verifies token signatures against the keys of a JWKS URL. It
// implements oidc.KeySet.
type keySet struct {
	url    string
	client *http.Client
	now    func() time.Time

	mu   sync.Mutex // guards keys
	keys map[string]jose.JSONWebKey

	fetchMu   sync.Mutex // serializes downloads and guards fetchedAt
	fetchedAt time.Time
}

func newKeySet(url string, client *http.Client, now func() time.Time) *keySet {
	return &keySet{url: url, client: client, now: now}
}

func (k *keySet) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	jws, err := jose.ParseSigned(raw, signatureAlgs())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedToken, err)
	}
	if len(jws.Signatures) != 1 {
		return nil, fmt.Errorf("%w: expected one signature", ErrMalformedToken)
	}
	kid := jws.Signatures[0].Header.KeyID

	key, ok := k.lookup(kid)
	if !ok {
		if err := k.refresh(ctx); err != nil {
			return nil, err
		}
		if key, ok = k.lookup(kid); !ok {
			return nil, fmt.Errorf("%w: kid %q", errUnknownKey, kid)
		}
	}
	return jws.Verify(&key)
}

func (k *keySet) lookup(kid string) (jose.JSONWebKey, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	key, ok := k.keys[kid]
	return key, ok
}

// refresh downloads the keys unless that happened less than minRefresh ago, in
// which case the caller finds the key unknown without a request to the forge.
func (k *keySet) refresh(ctx context.Context) error {
	k.fetchMu.Lock()
	defer k.fetchMu.Unlock()

	now := k.now()
	if !k.fetchedAt.IsZero() && now.Sub(k.fetchedAt) < minRefresh {
		return nil
	}
	// Failures count too, so a forge that is down is not retried on every token.
	k.fetchedAt = now

	// The download must not die with the request that triggered it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discoveryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return err
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return fmt.Errorf("oidc: downloading the keys: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc: downloading the keys: status %d", resp.StatusCode)
	}

	var set jose.JSONWebKeySet
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxKeySetSize)).Decode(&set); err != nil {
		return fmt.Errorf("oidc: reading the keys: %w", err)
	}

	keys := make(map[string]jose.JSONWebKey, len(set.Keys))
	for _, key := range set.Keys {
		if key.Use == "" || key.Use == "sig" {
			keys[key.KeyID] = key
		}
	}

	k.mu.Lock()
	k.keys = keys
	k.mu.Unlock()
	return nil
}

func signatureAlgs() []jose.SignatureAlgorithm {
	algs := make([]jose.SignatureAlgorithm, len(signingAlgs))
	for i, alg := range signingAlgs {
		algs[i] = jose.SignatureAlgorithm(alg)
	}
	return algs
}
