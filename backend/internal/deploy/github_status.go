package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// CommitStatusPoster is the GitHub half the publisher needs. ghx implements it;
// tests substitute a recorder.
type CommitStatusPoster interface {
	PostCommitStatus(ctx context.Context, nameWithOwner, sha string, status CommitStatus) error
}

// CommitStatus mirrors ghx.CommitStatus so the deploy package does not import
// the GitHub CLI wrapper.
type CommitStatus struct {
	State       string
	TargetURL   string
	Description string
	Context     string
}

var githubRemoteRE = regexp.MustCompile(`(?i)^(?:https?://|git@|ssh://git@)?(?:www\.)?github\.com[:/]([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// CommitStatusPublisher reports run outcomes back to the commit they deployed.
// It is a RunObserver: it never blocks or fails a run, and it posts only for
// sources whose remote is GitHub and whose environment policy asks for it.
type CommitStatusPublisher struct {
	runs    *OrchestrationStore
	poster  CommitStatusPoster
	baseURL func() string
	log     *slog.Logger

	mu     sync.Mutex
	posted map[string]bool
}

func NewCommitStatusPublisher(runs *OrchestrationStore, poster CommitStatusPoster, baseURL func() string, log *slog.Logger) *CommitStatusPublisher {
	return &CommitStatusPublisher{runs: runs, poster: poster, baseURL: baseURL, log: log, posted: map[string]bool{}}
}

func (p *CommitStatusPublisher) RunStarted(ctx context.Context, run EngineRun) {
	p.publish(ctx, run, "pending", "Deployment in progress")
}

func (p *CommitStatusPublisher) RunFinished(ctx context.Context, run EngineRun) {
	switch run.State {
	case RunSucceeded:
		p.publish(ctx, run, "success", "Deployed successfully")
	case RunFailed, RunFailedActivation:
		p.publish(ctx, run, "failure", failureDescription(run))
	case RunRolledBack:
		p.publish(ctx, run, "failure", "Deployment failed; the previous release was restored")
	case RunCancelled, RunSuperseded:
		p.publish(ctx, run, "error", "Deployment was cancelled")
	}
}

func failureDescription(run EngineRun) string {
	if reason := strings.TrimSpace(run.TerminalReason); reason != "" {
		return "Deployment failed: " + reason
	}
	if run.TerminalCode != "" {
		return "Deployment failed: " + run.TerminalCode
	}
	return "Deployment failed"
}

func (p *CommitStatusPublisher) publish(ctx context.Context, run EngineRun, state, description string) {
	if p == nil || p.poster == nil || p.runs == nil {
		return
	}
	if run.Operation == OperationRestart || run.Operation == OperationPreviewRemove {
		// Neither changes which commit is deployed.
		return
	}
	target, ok := p.target(ctx, run)
	if !ok {
		return
	}
	key := fmt.Sprintf("%d:%s", run.ID, state)
	p.mu.Lock()
	already := p.posted[key]
	if !already {
		p.posted[key] = true
		if len(p.posted) > 4096 {
			p.posted = map[string]bool{key: true}
		}
	}
	p.mu.Unlock()
	if already {
		return
	}
	status := CommitStatus{State: state, Description: description, Context: target.context}
	if p.baseURL != nil {
		if base := strings.TrimRight(strings.TrimSpace(p.baseURL()), "/"); base != "" {
			status.TargetURL = fmt.Sprintf("%s/deploy/%d/runs/%d", base, run.ProjectID, run.ID)
		}
	}
	if err := p.poster.PostCommitStatus(ctx, target.repository, target.revision, status); err != nil && p.log != nil {
		p.log.Warn("commit status was not posted", "run", run.ID, "repository", target.repository, "err", err)
	}
}

type commitStatusTarget struct {
	repository string
	revision   string
	context    string
}

// target resolves the GitHub repository and exact commit a run deploys, or
// reports that this run has nothing to tell GitHub about.
func (p *CommitStatusPublisher) target(ctx context.Context, run EngineRun) (commitStatusTarget, bool) {
	if run.EnvironmentID == 0 {
		return commitStatusTarget{}, false
	}
	policy, err := p.runs.GitDeploymentPolicy(ctx, run.ProjectID, run.EnvironmentID)
	if err != nil || !policy.CommitStatuses {
		return commitStatusTarget{}, false
	}
	var identity string
	var environment string
	err = p.runs.db.QueryRowContext(ctx, `
		SELECT e.slug,
		       COALESCE((SELECT src.identity_json FROM deploy_sources src
		                  WHERE src.environment_id = e.id AND src.revision = ? ORDER BY src.id DESC LIMIT 1), '{}')
		  FROM deploy_environments e WHERE e.id = ?`, run.PlanRevision, run.EnvironmentID).
		Scan(&environment, &identity)
	if err != nil {
		return commitStatusTarget{}, false
	}
	var source SourceIdentity
	if json.Unmarshal([]byte(identity), &source) != nil || source.Kind != SourceGit {
		return commitStatusTarget{}, false
	}
	repository := githubRepository(source)
	revision := strings.ToLower(strings.TrimSpace(run.SourceRevision))
	if revision == "" {
		revision = strings.ToLower(strings.TrimSpace(source.Revision))
	}
	if repository == "" || !gitObjectIDRE.MatchString(revision) {
		return commitStatusTarget{}, false
	}
	return commitStatusTarget{
		repository: repository, revision: revision,
		context: "just-dashboard/" + sanitizeStatusContext(environment),
	}, true
}

// githubRepository accepts only a remote that is unambiguously github.com;
// a GitHub Enterprise host or another provider yields nothing.
func githubRepository(source SourceIdentity) string {
	remote := strings.TrimSpace(source.Remote)
	if remote == "" {
		return ""
	}
	if parsed, err := url.Parse(remote); err == nil && parsed.Host != "" {
		if host := strings.ToLower(parsed.Hostname()); host != "github.com" && host != "www.github.com" {
			return ""
		}
	}
	match := githubRemoteRE.FindStringSubmatch(remote)
	if match == nil {
		return ""
	}
	repository := match[1] + "/" + match[2]
	if source.Repository != "" && !strings.EqualFold(source.Repository, repository) {
		// The recorded repository and the remote disagree; trust neither.
		return ""
	}
	return repository
}

func sanitizeStatusContext(environment string) string {
	environment = strings.TrimSpace(environment)
	if environment == "" {
		return "production"
	}
	var out strings.Builder
	for _, r := range environment {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			out.WriteRune(r)
		default:
			out.WriteRune('-')
		}
	}
	return out.String()
}
