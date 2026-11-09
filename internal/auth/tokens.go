// Package auth authenticates requests with bearer tokens mapped to namespaces.
package auth

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"nx-caching-server/internal/storage"
)

// Tokens maps the SHA-256 hash of each token to its namespace. A nil value
// disables authentication.
type Tokens map[[32]byte]string

// ParseTokens builds Tokens from the AUTH_TOKEN and AUTH_TOKENS values. A token
// set through authToken maps to the empty namespace (flat storage directory).
// authTokens uses the form "name:token,name:token".
func ParseTokens(authToken, authTokens string) (Tokens, error) {
	if authToken != "" && authTokens != "" {
		return nil, fmt.Errorf("AUTH_TOKEN and AUTH_TOKENS cannot be used together")
	}

	if authToken != "" {
		return Tokens{sha256.Sum256([]byte(authToken)): ""}, nil
	}

	if authTokens == "" {
		return nil, nil
	}

	tokens := make(Tokens)
	names := make(map[string]bool)
	for i, entry := range strings.Split(authTokens, ",") {
		name, token, found := strings.Cut(strings.TrimSpace(entry), ":")
		name, token = strings.TrimSpace(name), strings.TrimSpace(token)
		if !found || name == "" || token == "" {
			return nil, fmt.Errorf("AUTH_TOKENS entry %d: expected name:token", i+1)
		}
		if !storage.ValidHash(name) {
			return nil, fmt.Errorf("AUTH_TOKENS entry %d: invalid name %q (allowed: letters, digits, '_' and '-')", i+1, name)
		}
		if names[name] {
			return nil, fmt.Errorf("AUTH_TOKENS: duplicate name %q", name)
		}
		hash := sha256.Sum256([]byte(token))
		if _, ok := tokens[hash]; ok {
			return nil, fmt.Errorf("AUTH_TOKENS: duplicate token for name %q", name)
		}
		names[name] = true
		tokens[hash] = name
	}
	return tokens, nil
}

// Namespaces returns the sorted non-empty namespace names.
func (t Tokens) Namespaces() []string {
	var names []string
	for _, name := range t {
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
