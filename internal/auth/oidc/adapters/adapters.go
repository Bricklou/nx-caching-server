// Package adapters maps the claims of a verified forge token to a scope event.
// Each forge names its claims differently; one adapter hides that difference.
package adapters

import (
	"nx-caching-server/internal/config"
	"nx-caching-server/internal/scope"
)

// Claims is what an adapter extracts from a verified token.
type Claims struct {
	RepoID   string
	RepoPath string
	Actor    string
	Ref      string
	Event    string
	Scope    scope.Event
	// PipelineID is set for GitLab merge request pipelines, whose token has no
	// merge request number. The server can ask GitLab which merge request it is.
	PipelineID string
}

// Adapter turns the raw payload of a verified token into Claims.
type Adapter func(payload []byte) (Claims, error)

// For returns the adapter of a forge type.
func For(forgeType string) (Adapter, bool) {
	switch forgeType {
	case config.ForgeTypeGitHub, config.ForgeTypeForgejo:
		return actionsAdapter, true
	case config.ForgeTypeGitlab:
		return gitlabAdapter, true
	}
	return nil, false
}
