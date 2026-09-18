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
// publisher — it never blocks or fails a run — and it speaks only through the
// GitHub App, because that is the identity a comment is posted as.
type PullRequestCommenter struct {
	runs    *OrchestrationStore
	poster  PullRequestCommentPoster
	baseURL func() string
	log     *slog.Logger
	now     func() time.Time

	mu     sync.Mutex
	posted map[string]bool
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
	repository  string
	number      int
	environment string
	url         string
}

func (p *PullRequestCommenter) report(ctx context.Context, run EngineRun, state string) {
	if p == nil || p.poster == nil || p.runs == nil || run.EnvironmentID == 0 {
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
	if err := p.poster.UpsertPullRequestComment(ctx, target.repository, target.number, marker, body); err != nil && p.log != nil {
		p.log.Warn("pull request comment was not posted", "run", run.ID, "repository", target.repository, "pull", target.number, "err", err)
	}
}

// target resolves the pull request behind a run's environment: a preview
// environment created by a GitHub trigger. Any other run has no pull request
// to write on.
func (p *PullRequestCommenter) target(ctx context.Context, run EngineRun) (pullRequestTarget, bool) {
	var providerRef, provider, config, slug, domain, domainConfig string
	err := p.runs.db.QueryRowContext(ctx, `
		SELECT pr.provider_ref, t.provider, t.config_json, e.slug,
		       COALESCE((SELECT d.resource_id FROM deploy_dependencies d WHERE d.environment_id = e.id AND d.kind = 'domain' ORDER BY d.id LIMIT 1), ''),
		       COALESCE((SELECT d.config_json FROM deploy_dependencies d WHERE d.environment_id = e.id AND d.kind = 'domain' ORDER BY d.id LIMIT 1), '{}')
		  FROM deploy_preview_refs pr
		  JOIN deploy_triggers t ON t.id = pr.trigger_id
		  JOIN deploy_environments e ON e.id = pr.environment_id
		 WHERE pr.environment_id = ?`, run.EnvironmentID).
		Scan(&providerRef, &provider, &config, &slug, &domain, &domainConfig)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return pullRequestTarget{}, false
	}
	var trigger TriggerConfig
	if provider != "github" || json.Unmarshal([]byte(config), &trigger) != nil || trigger.Repository == "" {
		return pullRequestTarget{}, false
	}
	number, err := strconv.Atoi(providerRef)
	if err != nil || number <= 0 {
		return pullRequestTarget{}, false
	}
	target := pullRequestTarget{repository: strings.TrimSuffix(trigger.Repository, ".git"), number: number, environment: slug}
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
