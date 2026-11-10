// The verified identity of a cache caller: forge, repository and cache scopes.
// Produced by the token verifier, consumed by the cache handlers.
package auth

import "nx-caching-server/internal/scope"

// Identity says who is calling and what the caller may read and write.
type Identity struct {
	Forge  ForgeInfo
	Repo   RepoInfo
	Actor  string
	Ref    string
	Event  string
	Access scope.Access
}

// ForgeInfo identifies the forge that issued the token.
type ForgeInfo struct {
	Name string // name given in the configuration
	Type string
	URL  string // issuer URL
}

// RepoInfo identifies the repository the job belongs to.
type RepoInfo struct {
	ID   string
	Path string // owner/name as reported by the forge, informational only
}
