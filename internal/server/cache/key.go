// Entry keys: the <forge>/<repository id>/<scope>/<hash> a caller may read and write.
// Without authentication the key is the hash alone.
package cache

import (
	"fmt"
	"net/http"
	"path"
	"regexp"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/scope"
)

// validHash admits only characters that cannot form path separators or
// traversal, so a hash, forge name or repository id is one key segment.
var validHash = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// access lists the scopes a request may read, in lookup order, and the scope it
// may write, nil when the caller is read only. Each is a key prefix, empty when
// authentication is disabled.
type access struct {
	read  []string
	write *string
}

func (a access) canWrite() bool {
	return a.write != nil
}

// accessFor maps the caller's identity to its scope prefixes, checking every
// segment since the result becomes part of a storage key.
func accessFor(r *http.Request) (access, error) {
	id, ok := auth.IdentityFrom(r.Context())
	if !ok {
		// Authentication is disabled.
		unscoped := ""
		return access{read: []string{unscoped}, write: &unscoped}, nil
	}

	if !validHash.MatchString(id.Forge.Name) || !validHash.MatchString(id.Repo.ID) {
		return access{}, fmt.Errorf("unsafe forge %q or repository id %q", id.Forge.Name, id.Repo.ID)
	}
	base := path.Join(id.Forge.Name, id.Repo.ID)

	var acc access
	for _, name := range id.Access.ReadChain {
		if !scope.Valid(name) {
			return access{}, fmt.Errorf("invalid read scope %q", name)
		}
		acc.read = append(acc.read, path.Join(base, name))
	}

	if !id.Access.ReadOnly {
		if !scope.Valid(id.Access.WriteScope) {
			return access{}, fmt.Errorf("invalid write scope %q", id.Access.WriteScope)
		}
		writeScope := path.Join(base, id.Access.WriteScope)
		acc.write = &writeScope
	}
	return acc, nil
}

// entryKey joins a scope prefix and a hash.
func entryKey(prefix, hash string) string {
	if prefix == "" {
		return hash
	}
	return prefix + "/" + hash
}
