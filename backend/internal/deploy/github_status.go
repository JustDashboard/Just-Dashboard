package deploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
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
// sources on GitHub, GitLab, Gitea or Forgejo whose environment policy asks
// for it.
type CommitStatusPublisher struct {
	runs    *OrchestrationStore
	poster  CommitStatusPoster
	baseURL func() string
	log     *slog.Logger

	// forges writes to GitLab and Gitea/Forgejo hosts; nil leaves a run on
	// one of them unreported, as before the client existed.
	forges *ForgeClient

	mu     sync.Mutex
	posted map[string]bool
}

func NewCommitStatusPublisher(runs *OrchestrationStore, poster CommitStatusPoster, baseURL func() string, log *slog.Logger) *CommitStatusPublisher {
	return &CommitStatusPublisher{runs: runs, poster: poster, baseURL: baseURL, log: log, posted: map[string]bool{}}
}

// WithForges lets the publisher report to GitLab and Gitea/Forgejo hosts.
func (p *CommitStatusPublisher) WithForges(forges *ForgeClient) *CommitStatusPublisher {
	p.forges = forges
	return p
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
	// A commit status has room for a few words, and a named cause's title
	// says more in them than the first words of its sentence.
	if cause := namedCauseTitle(run.TerminalCode); cause != "" {
		return "Deployment failed: " + cause
	}
	if reason := strings.TrimSpace(run.TerminalReason); reason != "" {
		return "Deployment failed: " + reason
	}
	if run.TerminalCode != "" {
		return "Deployment failed: " + run.TerminalCode
	}
	return "Deployment failed"
}

func (p *CommitStatusPublisher) publish(ctx context.Context, run EngineRun, state, description string) {
	if p == nil || p.runs == nil {
		return
	}
	if run.Operation == OperationRestart || run.Operation == OperationPreviewRemove ||
		run.Operation == OperationStop || run.Operation == OperationStart {
		// None of these change which commit is deployed.
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
	report := ForgeReport{Provider: target.provider, Repository: target.repository, Revision: target.revision, State: state, RunID: run.ID, At: time.Now()}
	var err error
	switch {
	case target.provider == ForgeGitHub:
		err = p.poster.PostCommitStatus(ctx, target.repository, target.revision, status)
	case target.endpointErr != nil:
		err = target.endpointErr
	default:
		err = p.forges.PostStatus(ctx, target.endpoint, target.revision, status)
		if err == nil {
			p.forges.markUsed(ctx, target.endpoint)
		}
	}
	report.OK = err == nil
	if err != nil {
		report.Message = err.Error()
		if p.log != nil {
			p.log.Warn("commit status was not posted", "run", run.ID, "provider", target.provider, "repository", target.repository, "err", err)
		}
	}
	p.runs.recordForgeReport(ctx, run.EnvironmentID, forgeReportStatus, report)
}

type commitStatusTarget struct {
	provider   string
	repository string
	revision   string
	context    string
	// endpoint is set for GitLab and Gitea; endpointErr is why a run on one
	// of those hosts cannot be reported, recorded rather than posted.
	endpoint    ForgeEndpoint
	endpointErr error
}

// target resolves the repository and exact commit a run deploys, or reports
// that this run has nothing to tell a Git host about.
func (p *CommitStatusPublisher) target(ctx context.Context, run EngineRun) (commitStatusTarget, bool) {
	if run.EnvironmentID == 0 {
		return commitStatusTarget{}, false
	}
	policy, err := p.runs.GitDeploymentPolicy(ctx, run.ProjectID, run.EnvironmentID)
	if err != nil || !policy.CommitStatuses {
		return commitStatusTarget{}, false
	}
	source, credentialID, environment, err := p.runs.environmentSource(ctx, run.EnvironmentID, run.PlanRevision)
	if err != nil || source.Kind != SourceGit {
		return commitStatusTarget{}, false
	}
	revision := strings.ToLower(strings.TrimSpace(run.SourceRevision))
	if revision == "" {
		revision = strings.ToLower(strings.TrimSpace(source.Revision))
	}
	if !gitObjectIDRE.MatchString(revision) {
		return commitStatusTarget{}, false
	}
	target := commitStatusTarget{revision: revision, context: "just-dashboard/" + sanitizeStatusContext(environment)}
	if repository := githubRepository(source); repository != "" {
		if p.poster == nil {
			return commitStatusTarget{}, false
		}
		target.provider, target.repository = ForgeGitHub, repository
		return target, true
	}
	if p.forges == nil {
		return commitStatusTarget{}, false
	}
	endpoint, err := p.forges.Resolve(ctx, source, credentialID)
	if errors.Is(err, ErrForgeUnsupported) {
		return commitStatusTarget{}, false
	}
	target.provider, target.repository, target.endpoint, target.endpointErr = endpoint.Provider, endpoint.Repository, endpoint, err
	if errors.Is(err, ErrForgeNoToken) {
		target.endpointErr = fmt.Errorf("%w %s: save a provider token for it under Deployments → Credentials", ErrForgeNoToken, endpoint.Host)
	}
	if target.provider == "" {
		// Resolve failed before it could name the host's software (a token
		// that may not travel over http); the reading names the remote.
		target.provider = "git"
	}
	return target, true
}

// Describe is the reading Settings shows for an environment: where its
// statuses go, whether anything can be written, and the last outcomes.
func (p *CommitStatusPublisher) Describe(ctx context.Context, projectID, environmentID int64) (*CommitStatusReading, error) {
	if p == nil || p.runs == nil {
		return &CommitStatusReading{}, nil
	}
	if _, err := p.runs.EnvironmentExecutionTarget(ctx, projectID, environmentID); err != nil {
		return nil, err
	}
	source, credentialID, environment, err := p.runs.environmentSource(ctx, environmentID, 0)
	if err != nil {
		return nil, err
	}
	reading := &CommitStatusReading{Context: "just-dashboard/" + sanitizeStatusContext(environment)}
	if reports, err := p.runs.forgeReports(ctx, environmentID); err == nil {
		reading.LastStatus, reading.LastComment = reports[forgeReportStatus], reports[forgeReportComment]
	}
	if source.Kind != SourceGit {
		reading.Reason = "Only a Git source has commits to report on."
		return reading, nil
	}
	if repository := githubRepository(source); repository != "" {
		reading.Provider, reading.Product, reading.Host, reading.Repository = ForgeGitHub, "github", "github.com", repository
		reading.Ready = p.poster != nil
		return reading, nil
	}
	if p.forges == nil {
		reading.Reason = "Commit statuses are written to GitHub, GitLab, Gitea and Forgejo."
		return reading, nil
	}
	endpoint, err := p.forges.Resolve(ctx, source, credentialID)
	reading.Provider, reading.Product, reading.Host = endpoint.Provider, endpoint.Product, endpoint.Host
	reading.Repository, reading.Credential = endpoint.Repository, endpoint.Credential
	switch {
	case err == nil:
		reading.Ready = true
	case errors.Is(err, ErrForgeUnsupported):
		reading.Reason = "This source's host is not GitHub, GitLab, Gitea or Forgejo, or did not answer as one."
	case errors.Is(err, ErrForgeNoToken):
		reading.Reason = "Save a provider token for " + endpoint.Host + " under Deployments → Credentials to report here."
	default:
		reading.Reason = err.Error()
	}
	return reading, nil
}

// GitHubRepository is the owner/name behind a source identity, accepting
// only a remote that is unambiguously github.com; a GitHub Enterprise host
// or another provider yields nothing.
func GitHubRepository(source SourceIdentity) string {
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

func githubRepository(source SourceIdentity) string { return GitHubRepository(source) }

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
