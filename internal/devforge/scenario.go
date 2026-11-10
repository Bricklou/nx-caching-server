// Mints the token a forge would issue for a typical CI situation: a push, a pull request, a tag
// or an untrusted event. Keeps the situation-to-claims mapping in one place for the CLI and tests.
package devforge

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"nx-caching-server/internal/config"
)

// ScenarioKind is the CI situation a token describes.
type ScenarioKind string

const (
	// ScenarioPush is a push to a branch.
	ScenarioPush ScenarioKind = "push"
	// ScenarioPR is a pull request (a merge request pipeline on GitLab) from the same repository.
	ScenarioPR ScenarioKind = "pr"
	// ScenarioTag is a pipeline on a tag.
	ScenarioTag ScenarioKind = "tag"
	// ScenarioUntrusted is an event that must only read: pull_request_target, or a GitLab trigger pipeline.
	ScenarioUntrusted ScenarioKind = "untrusted"
)

var scenarioKinds = []ScenarioKind{ScenarioPush, ScenarioPR, ScenarioTag, ScenarioUntrusted}

// ParseScenarioKind validates a scenario name.
func ParseScenarioKind(name string) (ScenarioKind, error) {
	for _, kind := range scenarioKinds {
		if string(kind) == name {
			return kind, nil
		}
	}

	names := make([]string, len(scenarioKinds))
	for i, kind := range scenarioKinds {
		names[i] = string(kind)
	}
	return "", fmt.Errorf("unknown scenario %q (%s)", name, strings.Join(names, ", "))
}

// Scenario holds the values a kind of scenario needs; the others are ignored.
type Scenario struct {
	Kind   ScenarioKind
	Branch string // push: the pushed branch. pr: the source branch.
	Base   string // pr and untrusted: the target branch
	Tag    string // tag
	Number int    // pr: the pull request number
}

// Job says who runs the job and for which repository.
type Job struct {
	Audience  string
	RepoID    string
	RepoPath  string // owner/name
	Actor     string
	Protected bool // gitlab and forgejo
	TTL       time.Duration
}

// scenarioMinters maps a forge type to the function building its tokens.
var scenarioMinters = map[string]func(*Signer, Scenario, Job) (string, error){
	config.ForgeTypeGitHub:  githubScenario,
	config.ForgeTypeGitlab:  gitlabScenario,
	config.ForgeTypeForgejo: forgejoScenario,
}

// MintScenario signs the token that forgeType would issue for sc and job.
func (s *Signer) MintScenario(forgeType string, sc Scenario, job Job) (string, error) {
	mint, ok := scenarioMinters[forgeType]
	if !ok {
		return "", fmt.Errorf("unknown forge %q (%s, %s or %s)", forgeType,
			config.ForgeTypeGitHub, config.ForgeTypeGitlab, config.ForgeTypeForgejo)
	}
	if _, err := ParseScenarioKind(string(sc.Kind)); err != nil {
		return "", err
	}
	return mint(s, sc, job)
}

func githubScenario(s *Signer, sc Scenario, job Job) (string, error) {
	token := GitHubToken{
		Audience: job.Audience, RepoID: job.RepoID, Repo: job.RepoPath, Actor: job.Actor, TTL: job.TTL,
		EventName: "push", Ref: "refs/heads/" + sc.Branch,
	}

	switch sc.Kind {
	case ScenarioPR:
		token.EventName, token.Ref, token.BaseRef = "pull_request", fmt.Sprintf("refs/pull/%d/merge", sc.Number), sc.Base
	case ScenarioTag:
		token.Ref, token.RefType = "refs/tags/"+sc.Tag, "tag"
	case ScenarioUntrusted:
		token.EventName, token.Ref, token.BaseRef = "pull_request_target", "refs/heads/"+sc.Base, sc.Base
	}
	return s.MintGitHub(token)
}

func gitlabScenario(s *Signer, sc Scenario, job Job) (string, error) {
	token := GitLabToken{
		Audience: job.Audience, ProjectID: job.RepoID, ProjectPath: job.RepoPath, NamespacePath: ownerOf(job.RepoPath),
		NamespaceID: "1", UserLogin: job.Actor, UserID: "1", Protected: job.Protected, TTL: job.TTL,
		Ref: sc.Branch, PipelineSource: "push",
	}

	switch sc.Kind {
	case ScenarioPR:
		// The source branch is the ref; GitLab has no number or target claim.
		token.PipelineSource = "merge_request_event"
	case ScenarioTag:
		token.Ref, token.RefType = sc.Tag, "tag"
	case ScenarioUntrusted:
		token.PipelineSource = "trigger"
	}
	return s.MintGitLab(token)
}

func forgejoScenario(s *Signer, sc Scenario, job Job) (string, error) {
	id, err := strconv.ParseInt(job.RepoID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("repository id %q must be a number for forgejo", job.RepoID)
	}

	token := ForgejoToken{
		Audience: job.Audience, RepoID: id, Repo: job.RepoPath, Owner: ownerOf(job.RepoPath), OwnerID: 1,
		Actor: job.Actor, ActorID: 1, Protected: job.Protected, TTL: job.TTL,
		EventName: "push", Ref: "refs/heads/" + sc.Branch,
	}

	switch sc.Kind {
	case ScenarioPR:
		token.EventName, token.Ref, token.BaseRef = "pull_request", fmt.Sprintf("refs/pull/%d/head", sc.Number), sc.Base
	case ScenarioTag:
		token.Ref, token.RefType = "refs/tags/"+sc.Tag, "tag"
	case ScenarioUntrusted:
		token.EventName, token.Ref, token.BaseRef = "pull_request_target", "refs/heads/"+sc.Base, sc.Base
	}
	return s.MintForgejo(token)
}

func ownerOf(repo string) string {
	owner, _, _ := strings.Cut(repo, "/")
	return owner
}
