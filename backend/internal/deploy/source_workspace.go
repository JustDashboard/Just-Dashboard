package deploy

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Where a deployment's code can be worked on, for the assistant's workspace: the
// repository a deployment of it builds, the branch, and the folder it builds
// from. A remote repository is cloned into a working checkout of the assistant's
// (api.handlers_assistant_workspace.go); a local checkout is the deployment's own
// folder, which it builds the committed state of.

// WorkspaceSource is where an environment's code lives.
type WorkspaceSource struct {
	// Remote is the repository's address, without credentials, for a remote
	// source; LocalPath is the checkout a local source builds from.
	Remote    string
	LocalPath string
	// Repository is the forge's owner/name when the address names one, and Host
	// the address's host.
	Repository string
	Host       string
	// Ref is the branch (or tag) a deployment builds, empty for the default
	// branch; Subdirectory is the folder inside the repository it builds.
	Ref          string
	Subdirectory string
	// CredentialID is the saved credential the deployment fetches with, 0 for
	// none. It is opened only to fetch or push for this environment.
	CredentialID int64
}

// WorkspaceSource reads where an environment's code lives, from its saved plan.
func (c *DeploymentChecker) WorkspaceSource(ctx context.Context, projectID, environmentID int64) (*WorkspaceSource, error) {
	plan, _, err := c.desiredPlan(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	if plan == nil || !sourceHasTree(plan.Source) {
		return nil, ErrSourceHasNoTree
	}
	source := canonicalSourceConfig(plan.Source)
	out := &WorkspaceSource{
		Ref: strings.TrimSpace(source.Ref), Subdirectory: source.Subdirectory, CredentialID: source.CredentialID,
	}
	if out.Ref == "" {
		out.Ref = strings.TrimSpace(plan.Identity.Ref)
	}
	if !IsRemoteGitSource(source) {
		out.LocalPath = source.LocalPath
		return out, nil
	}
	remote, repository, err := remoteForSource(source)
	if err != nil {
		return nil, err
	}
	out.Remote, out.Repository = remote, repository
	if parsed, err := url.Parse(remote); err == nil && parsed.Host != "" {
		out.Host = parsed.Hostname()
	} else if strings.HasPrefix(remote, "git@") {
		host, _, _ := strings.Cut(strings.TrimPrefix(remote, "git@"), ":")
		out.Host = host
	}
	return out, nil
}

// WorkspaceCredential is a saved credential as one git command over HTTPS sends
// it: git's own GIT_CONFIG_COUNT/KEY/VALUE environment, which configures that one
// process and is never written to a file or its arguments. The account's own
// credential helper is turned off for it, and the header is sent only to the
// exact remote. Name is how the credential is told to the person.
type WorkspaceCredential struct {
	Name        string
	Environment map[string]string
	// ReadOnly is a credential that fetches and cannot push: the deploy
	// GitHub App's, which is granted Contents: read.
	ReadOnly bool
}

// ErrWorkspaceCredential is a credential a workspace cannot use: an SSH key,
// which would have to be written to a file the checkout's account can read.
var ErrWorkspaceCredential = fmt.Errorf("%w: this credential cannot be used from a workspace", ErrSourceUnavailable)

// WorkspaceCredential opens a saved credential for one git command against
// remote. A credential that is not a token over HTTPS answers
// ErrWorkspaceCredential, and the caller falls back to the account's own.
func (s *PlanningStore) WorkspaceCredential(ctx context.Context, id int64, remote string) (*WorkspaceCredential, error) {
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Host == "" {
		return nil, ErrWorkspaceCredential
	}
	var name, kind string
	if err := s.db.QueryRowContext(ctx, `SELECT name, kind FROM deploy_credentials WHERE id = ?`, id).Scan(&name, &kind); err != nil {
		return nil, fmt.Errorf("%w: credential not found", ErrSourceUnavailable)
	}
	material, err := s.OpenCredential(ctx, id)
	if err != nil {
		return nil, err
	}
	switch material.Kind {
	case CredentialGitBearer, CredentialProviderToken:
	default:
		return nil, ErrWorkspaceCredential
	}
	if !validGitBearerToken(material.Secret) {
		return nil, fmt.Errorf("%w: Git credential is empty or malformed", ErrSourceUnavailable)
	}
	return &WorkspaceCredential{
		Name:        name,
		Environment: GitHeaderEnvironment(remote, gitBasicAuthorization(parsed.Hostname(), material.Secret)),
		ReadOnly:    kind == CredentialGitHubApp,
	}, nil
}

// GitHeaderEnvironment is the environment that sends authorization to remote
// alone, for one git process, with the account's credential helper off. No
// program the checkout names runs beside it: hooks (.git/hooks, or where
// core.hooksPath points, which a project's install sets) and a file-system
// monitor would inherit this environment, and with it the credential, and
// whoever can write the checkout chooses them. These variables are git's
// command-line scope, so they win over the checkout's own configuration.
func GitHeaderEnvironment(remote, authorization string) map[string]string {
	return map[string]string{
		"GIT_CONFIG_COUNT":   "4",
		"GIT_CONFIG_KEY_0":   "credential.helper",
		"GIT_CONFIG_VALUE_0": "",
		"GIT_CONFIG_KEY_1":   "http." + remote + ".extraHeader",
		"GIT_CONFIG_VALUE_1": "Authorization: " + authorization,
		"GIT_CONFIG_KEY_2":   "core.hooksPath",
		"GIT_CONFIG_VALUE_2": os.DevNull,
		"GIT_CONFIG_KEY_3":   "core.fsmonitor",
		"GIT_CONFIG_VALUE_3": "false",
	}
}

// GitBasicAuthorization is the HTTP Basic header a Git host takes a token in.
func GitBasicAuthorization(host, token string) string { return gitBasicAuthorization(host, token) }
