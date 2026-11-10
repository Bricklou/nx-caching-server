// Where cache entries live on disk: the directories a caller may use and the atomic publication of an entry.
// Without authentication entries sit directly in the storage directory.
package cache

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"nx-caching-server/internal/auth"
	"nx-caching-server/internal/scope"
)

// tempInfix marks in-progress uploads (<hash>.cache.tmp-<random>), which are
// never served and are removed by cleanup once stale.
const tempInfix = ".tmp-"

// validHash allows only characters that cannot form path separators or
// traversal sequences, so the hash is always a single file name. The forge
// name and repository id of an identity are held to the same rule.
var validHash = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// access lists the directories a request may read, in lookup order, and the
// directory it may write, which is empty when the caller is read only.
type access struct {
	read  []string
	write string
}

// accessFor maps the caller's identity to directories:
// <dir>/<forge>/<repository id>/<scope>. Every segment is checked, since the
// result is joined into a path.
func (h *Handler) accessFor(r *http.Request) (access, error) {
	id, ok := auth.IdentityFrom(r.Context())
	if !ok {
		// Authentication is disabled.
		return access{read: []string{h.dir}, write: h.dir}, nil
	}

	if !validHash.MatchString(id.Forge.Name) || !validHash.MatchString(id.Repo.ID) {
		return access{}, fmt.Errorf("unsafe forge %q or repository id %q", id.Forge.Name, id.Repo.ID)
	}
	base := filepath.Join(h.dir, id.Forge.Name, id.Repo.ID)

	var acc access
	for _, name := range id.Access.ReadChain {
		if !scope.Valid(name) {
			return access{}, fmt.Errorf("invalid read scope %q", name)
		}
		acc.read = append(acc.read, filepath.Join(base, name))
	}

	if !id.Access.ReadOnly {
		if !scope.Valid(id.Access.WriteScope) {
			return access{}, fmt.Errorf("invalid write scope %q", id.Access.WriteScope)
		}
		acc.write = filepath.Join(base, id.Access.WriteScope)
	}
	return acc, nil
}

func entryPath(dir, hash string) string {
	return filepath.Join(dir, hash+".cache")
}

// publishFile atomically makes tmpPath visible as finalPath without replacing
// an existing entry (os.IsExist is true in that case). It falls back to rename
// on filesystems without hard link support.
func publishFile(tmpPath, finalPath string) error {
	err := os.Link(tmpPath, finalPath)
	if err == nil || os.IsExist(err) {
		return err
	}
	return os.Rename(tmpPath, finalPath)
}
