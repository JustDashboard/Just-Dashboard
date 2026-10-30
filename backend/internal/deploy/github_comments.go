package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PullRequestCommentPoster is the GitHub App as the commenter needs it.
type PullRequestCommentPoster interface {
	UpsertPullRequestComment(ctx context.Context, nameWithOwner string, number int, marker, body string) error
}

// PullRequestCommenter writes a preview's state back on the pull request
// that asked for it: one comment, edited in place as the preview builds,
// comes up, fails or is removed. It is a RunObserver like the status
// publisher — it never blocks or fails a run. On GitHub it speaks only
// through the GitHub App, because that is the identity a comment is posted
// as; on GitLab and Gitea/Forgejo as the token saved for the host.
type PullRequestCommenter struct {
	runs    *OrchestrationStore
	poster  PullRequestCommentPoster
	baseURL func() string
	log     *slog.Logger
	now     func() time.Time

	// forges comments on GitLab merge requests and Gitea/Forgejo pull
	// requests, as the token saved for the host.
	forges *ForgeClient

	mu     sync.Mutex
	posted map[string]bool
}

// WithForges lets the commenter write on GitLab and Gitea/Forgejo previews.
func (p *PullRequestCommenter) WithForges(forges *ForgeClient) *PullRequestCommenter {
	p.forges = forges
	return p
}

func NewPullRequestCommenter(runs *OrchestrationStore, poster PullRequestCommentPoster, baseURL func() string, log *slog.Logger) *PullRequestCommenter {
	return &PullRequestCommenter{runs: runs, poster: poster, baseURL: baseURL, log: log, now: time.Now, posted: map[string]bool{}}
}

func (p *PullRequestCommenter) RunStarted(ctx context.Context, run EngineRun) {
	if run.Operation == OperationPreviewRemove {
		return
	}
	p.report(ctx, run, "building")
}

func (p *PullRequestCommenter) RunFinished(ctx context.Context, run EngineRun) {
	switch run.State {
	case RunSucceeded:
		if run.Operation == OperationPreviewRemove {
			p.report(ctx, run, "removed")
			return
		}
		p.report(ctx, run, "ready")
	case RunFailed, RunFailedActivation, RunRolledBack:
		p.report(ctx, run, "failed")
	case RunCancelled, RunSuperseded:
		p.report(ctx, run, "cancelled")
	}
}

// pullRequestTarget is the pull request a preview environment belongs to.
type pullRequestTarget struct {
	provider    string
	repository  string
	number      int
	environment string
	url         string
	// owner is the environment whose trigger opened the preview; the
	// comment's outcome is kept there, where Settings reads it.
	owner int64
	// endpoint is set for GitLab and Gitea; endpointErr is why a comment
	// cannot be written there.
	endpoint    ForgeEndpoint
	endpointErr error
}

func (p *PullRequestCommenter) report(ctx context.Context, run EngineRun, state string) {
	if p == nil || p.runs == nil || run.EnvironmentID == 0 {
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
	runURL := ""
	if p.baseURL != nil {
		if base := strings.TrimRight(strings.TrimSpace(p.baseURL()), "/"); base != "" {
			runURL = fmt.Sprintf("%s/deploy/%d/runs/%d", base, run.ProjectID, run.ID)
		}
	}
	marker := fmt.Sprintf("<!-- just-dashboard:preview:%d -->", run.EnvironmentID)
	body := renderPullRequestComment(target, run, state, runURL, p.now().UTC())
	var err error
	switch {
	case target.provider == ForgeGitHub:
		err = p.poster.UpsertPullRequestComment(ctx, target.repository, target.number, marker, body)
	case target.endpointErr != nil:
		err = target.endpointErr
	default:
		err = p.forges.UpsertComment(ctx, target.endpoint, target.number, marker, body)
		if err == nil {
			p.forges.markUsed(ctx, target.endpoint)
		}
	}
	if err != nil && p.log != nil {
		p.log.Warn("pull request comment was not posted", "run", run.ID, "provider", target.provider, "repository", target.repository, "pull", target.number, "err", err)
	}
	report := ForgeReport{Provider: target.provider, Repository: target.repository, Revision: strings.ToLower(run.SourceRevision),
		State: state, RunID: run.ID, OK: err == nil, At: p.now()}
	if err != nil {
		report.Message = err.Error()
	}
	p.runs.recordForgeReport(ctx, target.owner, forgeReportComment, report)
}

// target resolves the pull request behind a run's environment: a preview
// environment created by a GitHub, GitLab or Gitea trigger. Any other run has
// no pull request to write on.
func (p *PullRequestCommenter) target(ctx context.Context, run EngineRun) (pullRequestTarget, bool) {
	var providerRef, provider, config, slug, domain, domainConfig string
	var owner int64
	err := p.runs.db.QueryRowContext(ctx, `
		SELECT pr.provider_ref, t.provider, t.config_json, e.slug, t.environment_id,
		       COALESCE((SELECT d.resource_id FROM deploy_dependencies d WHERE d.environment_id = e.id AND d.kind = 'domain' ORDER BY d.id LIMIT 1), ''),
		       COALESCE((SELECT d.config_json FROM deploy_dependencies d WHERE d.environment_id = e.id AND d.kind = 'domain' ORDER BY d.id LIMIT 1), '{}')
		  FROM deploy_preview_refs pr
		  JOIN deploy_triggers t ON t.id = pr.trigger_id
		  JOIN deploy_environments e ON e.id = pr.environment_id
		 WHERE pr.environment_id = ?`, run.EnvironmentID).
		Scan(&providerRef, &provider, &config, &slug, &owner, &domain, &domainConfig)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return pullRequestTarget{}, false
	}
	var trigger TriggerConfig
	if json.Unmarshal([]byte(config), &trigger) != nil || trigger.Repository == "" {
		return pullRequestTarget{}, false
	}
	number, err := strconv.Atoi(providerRef)
	if err != nil || number <= 0 {
		return pullRequestTarget{}, false
	}
	target := pullRequestTarget{provider: provider, repository: strings.TrimSuffix(trigger.Repository, ".git"), number: number, environment: slug, owner: owner}
	switch provider {
	case ForgeGitHub:
		if p.poster == nil {
			return pullRequestTarget{}, false
		}
	case ForgeGitLab, ForgeGitea:
		if p.forges == nil {
			return pullRequestTarget{}, false
		}
		source, credentialID, _, err := p.runs.environmentSource(ctx, run.EnvironmentID, run.PlanRevision)
		if err != nil {
			return pullRequestTarget{}, false
		}
		endpoint, err := p.forges.Resolve(ctx, source, credentialID)
		switch {
		case errors.Is(err, ErrForgeNoToken):
			err = fmt.Errorf("%w %s: save a provider token for it under Deployments → Credentials", ErrForgeNoToken, endpoint.Host)
		case err == nil && endpoint.Provider != provider:
			err = fmt.Errorf("the trigger is for %s, but %s answers as %s", provider, endpoint.Host, endpoint.Provider)
		case err == nil && !strings.EqualFold(endpoint.Repository, target.repository):
			// The hook named one repository and the source clones another:
			// writing on either would be a guess.
			err = fmt.Errorf("the trigger is for %s, but the preview builds %s", target.repository, endpoint.Repository)
		}
		if errors.Is(err, ErrForgeUnsupported) {
			return pullRequestTarget{}, false
		}
		target.endpoint, target.endpointErr = endpoint, err
	default:
		return pullRequestTarget{}, false
	}
	if domain != "" {
		var parsed struct {
			HTTPS bool `json:"https"`
		}
		_ = json.Unmarshal([]byte(domainConfig), &parsed)
		scheme := "http"
		if parsed.HTTPS {
			scheme = "https"
		}
		target.url = scheme + "://" + domain + "/"
	}
	return target, true
}

// renderPullRequestComment is the comment's Markdown. One table row per
// preview, the state first because that is the one word the author reads.
func renderPullRequestComment(target pullRequestTarget, run EngineRun, state, runURL string, at time.Time) string {
	label := map[string]string{
		"building": "🔄 Building", "ready": "✅ Ready", "failed": "❌ Failed", "removed": "🗑️ Removed", "cancelled": "⏹️ Cancelled",
	}[state]
	address := "—"
	if target.url != "" && (state == "ready" || state == "building") {
		address = target.url
	}
	var builder strings.Builder
	builder.WriteString("### Just Dashboard preview\n\n")
	builder.WriteString("| Environment | State | Preview | Updated |\n| --- | --- | --- | --- |\n")
	fmt.Fprintf(&builder, "| `%s` | %s | %s | %s |\n", target.environment, label, address, at.Format("2006-01-02 15:04 UTC"))
	details := []string{}
	if revision := strings.TrimSpace(run.SourceRevision); len(revision) >= 7 {
		details = append(details, "Commit `"+revision[:7]+"`")
	}
	if runURL != "" {
		details = append(details, fmt.Sprintf("[Deployment run #%d](%s)", run.RunNumber, runURL))
	}
	if state == "failed" {
		reason := strings.TrimSpace(run.TerminalReason)
		if reason == "" {
			reason = run.TerminalCode
		}
		if reason != "" {
			details = append(details, "Reason: "+reason)
		}
	}
	if len(details) > 0 {
		builder.WriteString("\n" + strings.Join(details, " · ") + "\n")
	}
	return builder.String()
}
