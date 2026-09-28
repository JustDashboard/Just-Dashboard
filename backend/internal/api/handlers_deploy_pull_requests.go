package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/ghx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubapp"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfcfg"
)

// The pull request side of a deploy project: what is open on GitHub, which
// of those have a preview environment, and the verbs that build, close,
// merge and comment on one from the deploy page.
//
//	read            the list, the checks, the fleet counts
//	system.admin    testing a pull request — it approves that exact head to
//	                build and run here, and may copy production's variables
//	                into the preview (session only, like every approval)
//	destructive     closing a preview — its container, volumes and address go
//	service.control merging and commenting, as on the Git page (session only:
//	                both speak on GitHub as the dashboard's own account)

var gitRevisionRE = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)

// pullRequestWithPreview is one pull request as the deploy page lists it,
// with the preview built from it beside it.
type pullRequestWithPreview struct {
	ghx.PullRequest
	Preview *deploy.PreviewRef `json:"preview"`
}

// productionFacts is what the page says about production next to its pull
// requests: whether a merge redeploys it by itself, and whether it has ever
// been deployed at all.
type productionFacts struct {
	EnvironmentID           int64 `json:"environmentId"`
	Automatic               bool  `json:"automatic"`
	IntervalSeconds         int   `json:"intervalSeconds"`
	AwaitingFirstDeployment bool  `json:"awaitingFirstDeployment"`
}

type projectPullRequests struct {
	Repository    string                   `json:"repository"`
	Host          string                   `json:"host"`
	Available     bool                     `json:"available"`
	Reason        string                   `json:"reason,omitempty"`
	Identity      string                   `json:"identity,omitempty"`
	CheckoutPath  string                   `json:"checkoutPath,omitempty"`
	Pulls         []pullRequestWithPreview `json:"pulls"`
	Previews      []deploy.PreviewRef      `json:"previews"`
	Production    productionFacts          `json:"production"`
	Tailnet       selfcfg.Identity         `json:"tailnet"`
	Compose       bool                     `json:"compose"`
	LocalCheckout bool                     `json:"localCheckout"`
	InternalPort  int                      `json:"internalPort"`
	ReleaseTasks  int                      `json:"releaseTasks"`
}

// previewFacts are the reasons a project cannot preview a pull request at
// all, read once and answered on the list so the dialog can say them before
// the button is pressed.
type previewFacts struct {
	compose       bool
	localCheckout bool
	internalPort  int
	releaseTasks  int
	production    productionFacts
}

func (s *Server) previewFactsFor(ctx context.Context, projectID int64, summary *deploy.DeploymentSummary) previewFacts {
	facts := previewFacts{
		compose:      summary.BuildMethod == deploy.BuildCompose || summary.BuildMethod == deploy.BuildLegacyCompose,
		internalPort: summary.InternalPort,
		production:   productionFacts{EnvironmentID: summary.EnvironmentID},
	}
	source, err := s.modules.deployRuns.SourceConfiguration(ctx, summary.EnvironmentID, summary.DesiredRevision)
	facts.localCheckout = err != nil || !deploy.IsRemoteGitSource(source)
	if configuration, err := s.modules.deployPlanning.EnvironmentConfiguration(ctx, projectID, summary.EnvironmentID); err == nil {
		facts.releaseTasks = len(configuration.Build.ReleaseTasks)
	}
	if watch, err := s.modules.deployRuns.GitWatchStatus(ctx, projectID, summary.EnvironmentID); err == nil {
		facts.production.Automatic = watch.Automatic
		facts.production.IntervalSeconds = watch.IntervalSeconds
		facts.production.AwaitingFirstDeployment = watch.Status == "awaiting_first_deployment"
	}
	if summary.LiveReleaseID == 0 && summary.LastRun == nil {
		facts.production.AwaitingFirstDeployment = true
	}
	return facts
}

// listedPreviews are the previews the Overview draws beside the pull
// requests: every open one, and a closed one only while its cleanup has not
// succeeded, so a failed removal can be retried from the page and a finished
// one does not stay on it forever. The pull requests are joined to this same
// list, so a cleaned-up preview does not follow its pull request around as
// "Closed" either.
func listedPreviews(previews []deploy.PreviewRef) []deploy.PreviewRef {
	out := make([]deploy.PreviewRef, 0, len(previews))
	for _, preview := range previews {
		if preview.State == "open" {
			out = append(out, preview)
			continue
		}
		if preview.LastRun != nil && preview.LastRun.Operation == deploy.OperationPreviewRemove && preview.LastRun.State != deploy.RunSucceeded {
			out = append(out, preview)
		}
	}
	return out
}

// previewByNumber picks the preview of one pull request out of a list, an
// open one first.
func previewByNumber(previews []deploy.PreviewRef, number int) *deploy.PreviewRef {
	var found *deploy.PreviewRef
	for index := range previews {
		preview := &previews[index]
		if preview.Number != number {
			continue
		}
		if preview.State == "open" {
			return preview
		}
		if found == nil {
			found = preview
		}
	}
	return found
}

func pullRequestsError(err error) error {
	var unavailable errPullRequestsUnavailable
	switch {
	case errors.As(err, &unavailable):
		return httpx.Err(http.StatusConflict, unavailable.reason, unavailable.Error())
	case errors.Is(err, githubapp.ErrPullRequestNotFound):
		return httpx.Err(http.StatusNotFound, "pull_request_not_found", err.Error())
	case errors.Is(err, ghx.ErrNotInstalled):
		return httpx.Err(http.StatusServiceUnavailable, "not_installed", err.Error())
	case errors.Is(err, errGitHubLoginRequired):
		return httpx.Err(http.StatusConflict, "github_login_required", err.Error())
	case rateLimited(err):
		return httpx.Err(http.StatusTooManyRequests, "github_rate_limited", err.Error())
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return httpx.Err(http.StatusBadGateway, "github_unavailable", "GitHub did not answer in time")
	}
	// gh's own words are the diagnosis, as on the Git page.
	return httpx.BadRequest("%v", err)
}

func (s *Server) deploymentRepository(ctx context.Context, projectID int64) (*deploy.DeploymentSummary, string, error) {
	summary, err := s.modules.deployRuns.DeploymentSummary(ctx, projectID, deploy.QueueBudget{
		Heavy: s.Cfg.DeployHeavySlots, Light: s.Cfg.DeployLightSlots,
	})
	if err != nil {
		return nil, "", mapDeployError(err)
	}
	repository, ok := s.modules.pullRequests.repositoryOf(*summary)
	if !ok {
		return summary, "", httpx.Err(http.StatusNotFound, "not_github", "this project does not deploy from a github.com repository")
	}
	return summary, repository, nil
}

func (s *Server) handleDeploymentPullRequests(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	summary, err := s.modules.deployRuns.DeploymentSummary(r.Context(), projectID, deploy.QueueBudget{
		Heavy: s.Cfg.DeployHeavySlots, Light: s.Cfg.DeployLightSlots,
	})
	if err != nil {
		return mapDeployError(err)
	}
	previews, err := s.modules.deployAutomation.ListPreviews(r.Context(), projectID)
	if err != nil {
		return httpx.Internal(err)
	}
	facts := s.previewFactsFor(r.Context(), projectID, summary)
	repository, ok := s.modules.pullRequests.repositoryOf(*summary)
	listed := listedPreviews(previews)
	response := projectPullRequests{
		Repository: repository, Host: ghx.DefaultHost,
		Pulls: []pullRequestWithPreview{}, Previews: listed,
		Production: facts.production, Tailnet: s.modules.pullRequests.Tailnet(r.Context()),
		Compose: facts.compose, LocalCheckout: facts.localCheckout,
		InternalPort: facts.internalPort, ReleaseTasks: facts.releaseTasks,
	}
	if !ok {
		response.Reason = "not_github"
		httpx.JSON(w, http.StatusOK, response)
		return nil
	}
	response.CheckoutPath = s.modules.pullRequests.checkoutFor(r.Context(), repository)
	pulls, identity, err := s.modules.pullRequests.List(r.Context(), repository, "open")
	if err != nil {
		var unavailable errPullRequestsUnavailable
		if errors.As(err, &unavailable) {
			response.Reason = unavailable.reason
			httpx.JSON(w, http.StatusOK, response)
			return nil
		}
		return pullRequestsError(err)
	}
	response.Available, response.Identity = true, identity
	for _, pull := range pulls {
		response.Pulls = append(response.Pulls, pullRequestWithPreview{
			PullRequest: pull, Preview: previewByNumber(listed, pull.Number),
		})
	}
	httpx.JSON(w, http.StatusOK, response)
	return nil
}

func (s *Server) handleDeploymentPullRequestChecks(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	_, repository, err := s.deploymentRepository(r.Context(), projectID)
	if err != nil {
		return err
	}
	head := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("head")))
	if head == "" {
		head, err = s.modules.pullRequests.HeadOf(r.Context(), repository, number)
		if err != nil {
			return pullRequestsError(err)
		}
	}
	if !gitRevisionRE.MatchString(head) {
		return httpx.BadRequest("the pull request has no head commit to read checks for")
	}
	checks, err := s.modules.pullRequests.Checks(r.Context(), repository, head)
	if err != nil {
		return pullRequestsError(err)
	}
	if checks == nil {
		checks = []ghx.CheckRun{}
	}
	httpx.JSON(w, http.StatusOK, checks)
	return nil
}

type pullRequestPreviewRequest struct {
	Revision      string `json:"revision"`
	CopyVariables bool   `json:"copyVariables"`
	AcceptFork    bool   `json:"acceptFork"`
}

// handleDeploymentPullRequestPreview is "Test this pull request": the
// administrator's approval of exactly the head they looked at, then the
// preview environment, its variables, its tailnet address and the run that
// builds it. Every refusal the dialog can explain — the project's shape, the
// pull request's state, the tailnet, the quota, a free port — comes before
// anything is written, in the order the dialog explains them. What can still
// refuse after that point (a cleanup that has not finished, a store failure)
// is recorded in the audit log against the pull request and head it was for.
func (s *Server) handleDeploymentPullRequestPreview(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	var request pullRequestPreviewRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	revision := strings.ToLower(strings.TrimSpace(request.Revision))
	if !gitRevisionRE.MatchString(revision) {
		return httpx.BadRequest("revision must be the pull request's full head commit")
	}
	ctx := r.Context()
	summary, repository, err := s.deploymentRepository(ctx, projectID)
	if err != nil {
		return err
	}
	facts := s.previewFactsFor(ctx, projectID, summary)
	switch {
	case facts.localCheckout:
		return httpx.Err(http.StatusConflict, "not_remote_git", "this project deploys a local checkout; previews need a remote Git source to fetch the pull request from")
	case facts.compose:
		return httpx.Err(http.StatusConflict, "preview_compose", "previews are not available for Compose projects")
	case facts.internalPort == 0:
		return httpx.Err(http.StatusConflict, "preview_no_port", "the runtime plan has no internal port to publish on the tailnet")
	}
	pull, err := s.modules.pullRequests.Get(ctx, repository, number)
	if err != nil {
		return pullRequestsError(err)
	}
	switch {
	case pull.State != "open":
		return httpx.Err(http.StatusConflict, "pull_request_closed", fmt.Sprintf("pull request #%d is %s", number, pull.State))
	case !strings.EqualFold(pull.HeadSHA, revision):
		return httpx.Err(http.StatusConflict, "pull_request_head_changed", fmt.Sprintf("pull request #%d has moved on to %s; review the new head before testing it", number, pull.HeadSHA))
	case pull.Fork && !request.AcceptFork:
		return httpx.Err(http.StatusConflict, "pull_request_fork", fmt.Sprintf("pull request #%d comes from %s, a fork; confirm you have reviewed its changes", number, pull.HeadRepository))
	case pull.Fork && request.CopyVariables:
		return httpx.Err(http.StatusBadRequest, "preview_fork_variables", "production variables are never copied into a preview of a fork")
	}
	tailnet := s.modules.pullRequests.Tailnet(ctx)
	if !tailnet.Usable() {
		return httpx.Err(http.StatusConflict, "tailnet_unavailable", tailnet.Detail)
	}
	served := map[int]int{}
	if s.modules.tailnet != nil {
		if served, err = s.modules.tailnet.ServedTailnetPorts(ctx); err != nil {
			return httpx.Err(http.StatusConflict, "tailnet_unavailable", err.Error())
		}
	}
	project, err := s.modules.deployStore.Get(ctx, projectID)
	if err != nil {
		return mapDeployError(err)
	}
	actor := httpx.MustPrincipal(r).Username()
	trigger, err := s.modules.deployAutomation.EnsurePullRequestTrigger(ctx, projectID, summary.EnvironmentID, repository, strings.TrimPrefix(summary.SourceRef, "refs/heads/"))
	if err != nil {
		return mapAutomationError(err)
	}
	previews, err := s.modules.deployAutomation.ListPreviews(ctx, projectID)
	if err != nil {
		return httpx.Internal(err)
	}
	if err := s.previewCapacity(ctx, trigger, previews, number, served); err != nil {
		return err
	}
	// The approval is the first write, so the audit row is claimed here: a
	// refusal further down still records which head of which pull request
	// it was for.
	httpx.SetAudit(r, "deploy.preview.test", strconv.Itoa(number), map[string]any{
		"deploymentId": projectID, "number": number, "revision": revision, "fork": pull.Fork,
	})
	event := deploy.ProviderEvent{
		Event: "pull_request", Action: "synchronize", Repository: repository,
		PreviewNumber: number, PreviewRef: fmt.Sprintf("refs/pull/%d/head", number), Revision: revision,
		HeadRef: pull.Head, HeadRepository: pull.HeadRepository, Author: pull.Author, BaseRef: pull.Base,
	}
	approval, err := s.modules.deployAutomation.RecordPreviewApproval(ctx, trigger, event, actor)
	if err != nil {
		return mapAutomationError(err)
	}
	preview, created, err := s.modules.deployAutomation.EnsurePreview(ctx, trigger, event)
	switch {
	case errors.Is(err, deploy.ErrPreviewCleanupPending):
		return httpx.Err(http.StatusConflict, "preview_cleanup_pending", err.Error())
	case errors.Is(err, deploy.ErrPreviewQuota):
		return httpx.Err(http.StatusConflict, "preview_quota", err.Error())
	case err != nil:
		return mapAutomationError(err)
	}
	if err := s.modules.deployAutomation.MarkPreviewOrigin(ctx, preview.ID, deploy.PreviewOriginDashboard, pull.Title); err != nil {
		return mapAutomationError(err)
	}
	if err := s.modules.deployAutomation.RecordPreviewHead(ctx, preview.ID, revision); err != nil {
		return mapAutomationError(err)
	}
	// The checkbox is the truth every time: copying refreshes production's
	// current values, unchecking drops the copies an earlier test made.
	copied, skipped := []string{}, []string{}
	if request.CopyVariables {
		copied, skipped, err = s.modules.deployPlanning.CopyEnvironmentVariables(ctx, projectID, summary.EnvironmentID, preview.EnvironmentID, actor)
		if err != nil {
			return mapDeployError(err)
		}
		if err := s.modules.deployAutomation.RecordPreviewVariablesCopied(ctx, preview.ID, revision); err != nil {
			return mapAutomationError(err)
		}
	} else {
		dropped, err := s.modules.deployPlanning.DeactivateCopiedVariables(ctx, preview.EnvironmentID)
		if err != nil {
			return mapDeployError(err)
		}
		current, err := s.modules.deployAutomation.PreviewByNumber(ctx, projectID, number)
		if err != nil {
			return httpx.Internal(err)
		}
		if dropped > 0 || (current != nil && current.VariablesCopiedRevision != "") {
			if err := s.modules.deployAutomation.RecordPreviewVariablesCopied(ctx, preview.ID, ""); err != nil {
				return mapAutomationError(err)
			}
		}
	}
	address, err := s.modules.deployRuns.AllocatePreviewAddress(ctx, preview.EnvironmentID, served)
	if err != nil {
		return mapDeployError(err)
	}
	// A retried approval answers with the run the first one created while
	// that run is in flight or its preview still stands. A retest after a
	// failed build, or after the sweep found nothing serving the address,
	// gets a run of its own: republishing the mapping is the run's job.
	run, key, err := s.previewRunKey(ctx, preview.EnvironmentID, fmt.Sprintf("preview-approval:%d:%d", approval.ID, approval.Generation), address.Published)
	if err != nil {
		return mapDeployError(err)
	}
	if run == nil {
		operation := deploy.OperationPreviewUpdate
		if created {
			operation = deploy.OperationPreviewCreate
		}
		run, err = s.enqueueNormalizedDeploymentAtSource(ctx, project, preview.EnvironmentID, operation, 0, deploy.TriggerPreview, actor, key, nil, revision, "", 0)
		if err != nil {
			return mapDeployError(err)
		}
	}
	s.modules.pullRequests.Invalidate(repository)
	httpx.SetAudit(r, "deploy.preview.test", strconv.Itoa(number), map[string]any{
		"deploymentId": projectID, "number": number, "revision": revision, "runId": run.ID,
		"copiedVariables": copied, "skipped": skipped, "fork": pull.Fork,
	})
	final, err := s.modules.deployAutomation.PreviewByNumber(ctx, projectID, number)
	if err != nil {
		return httpx.Internal(err)
	}
	if final == nil {
		final = preview
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"preview": final, "runId": run.ID, "copied": copied, "skipped": skipped,
		"address": address, "variablesFromProduction": request.CopyVariables,
	})
	return nil
}

// previewCapacity is the two refusals that EnsurePreview and
// AllocatePreviewAddress would otherwise raise after the approval and the
// variables were already written: the trigger's quota of open previews, and a
// tailnet port that nothing else holds. A preview that is already open is not
// asking for a slot, and one that already has its port is not asking for one.
func (s *Server) previewCapacity(ctx context.Context, trigger *deploy.Trigger, previews []deploy.PreviewRef, number int, served map[int]int) error {
	open := 0
	var mine *deploy.PreviewRef
	for index := range previews {
		preview := &previews[index]
		if preview.TriggerID != trigger.ID {
			continue
		}
		if preview.State == "open" {
			open++
		}
		if preview.Number == number {
			mine = preview
		}
	}
	quota := trigger.Config.PreviewQuota
	if quota == 0 {
		quota = 5
	}
	if (mine == nil || mine.State != "open") && open >= quota {
		return httpx.Err(http.StatusConflict, "preview_quota",
			fmt.Sprintf("%v: %d of %d previews of this repository are open; close one first", deploy.ErrPreviewQuota, open, quota))
	}
	if mine != nil && mine.Address != nil {
		return nil
	}
	addresses, err := s.modules.deployRuns.ListPreviewAddresses(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	held := make(map[int]bool, len(addresses)+len(served))
	for _, address := range addresses {
		held[address.Port] = true
	}
	for port := range served {
		held[port] = true
	}
	for port := selfcfg.TailnetPortMin; port <= selfcfg.TailnetPortMax; port++ {
		if !held[port] {
			return nil
		}
	}
	return mapDeployError(deploy.ErrPreviewAddressExhausted)
}

// previewRunKey is the idempotency key a preview lifecycle run should carry
// and the run already holding it when that run is the answer. The base key
// is reused while its run is in flight (or, when the caller says so, once it
// succeeded); a run that ended any other way keeps its key, and the first
// free ":a<n>" suffix — n being how many runs the prefix already has — names
// the run that tries again, so a retest rebuilds instead of answering with
// the failure.
func (s *Server) previewRunKey(ctx context.Context, environmentID int64, base string, reuseSucceeded bool) (*deploy.EngineRun, string, error) {
	for attempt := 0; attempt < 1000; attempt++ {
		key := base
		if attempt > 0 {
			key = fmt.Sprintf("%s:a%d", base, attempt)
		}
		run, err := s.modules.deployRuns.RunByIdempotencyKey(ctx, environmentID, key)
		if err != nil {
			return nil, "", err
		}
		if run == nil {
			return nil, key, nil
		}
		if !run.State.Terminal() || (reuseSucceeded && run.State == deploy.RunSucceeded) {
			return run, key, nil
		}
	}
	return nil, "", fmt.Errorf("%w: too many attempts recorded for %s", deploy.ErrInvalidPlan, base)
}

// closePreview closes one preview: the ref is marked closed, its queued work
// cancelled and its removal run enqueued, once. Closing again while the
// removal is in flight answers with that run; after it finished, with no run
// at all — a failed removal is retried through the run's own retry route,
// and a finished one left the environment archived.
//
// It writes its own audit line, through the request when there is one and as
// the reconciler otherwise, so a close is recorded exactly once whichever
// way it came.
func (s *Server) closePreview(ctx context.Context, r *http.Request, target deploy.PreviewTarget, actor, reason string) (runID int64, err error) {
	defer func() {
		detail := map[string]any{"deploymentId": target.ProjectID, "number": target.Number, "runId": runID, "reason": reason}
		if r != nil {
			httpx.SetAudit(r, "deploy.preview.close", strconv.Itoa(target.Number), detail)
			return
		}
		s.Audit.Record(ctx, audit.Entry{
			Actor: "reconciler", Action: "deploy.preview.close", Target: strconv.FormatInt(target.ProjectID, 10),
			Success: err == nil, Detail: audit.Detail(detail),
		})
	}()
	defer s.modules.pullRequests.Invalidate(target.Repository)
	if target.State == "closed" {
		runs, _, err := s.modules.deployRuns.ProjectRunsFiltered(ctx, target.ProjectID, deploy.RunListFilter{
			Environment: target.EnvironmentID, Operation: deploy.OperationPreviewRemove, Limit: 1,
		})
		if err != nil {
			return 0, err
		}
		if len(runs) > 0 && !runs[0].State.Terminal() {
			return runs[0].ID, nil
		}
		return 0, nil
	}
	project, err := s.modules.deployStore.Get(ctx, target.ProjectID)
	if err != nil {
		return 0, err
	}
	trigger, _, err := s.modules.deployAutomation.TriggerByID(ctx, target.ProjectID, target.TriggerID)
	if err != nil {
		return 0, err
	}
	event := deploy.ProviderEvent{
		Event: "pull_request", Action: "closed", Repository: target.Repository,
		PreviewNumber: target.Number, PreviewRef: fmt.Sprintf("refs/pull/%d/head", target.Number),
		PreviewClosed: true, Merged: reason == "merged",
	}
	if _, _, err := s.modules.deployAutomation.EnsurePreview(ctx, trigger, event); err != nil {
		return 0, err
	}
	if err := s.modules.deployRuns.CancelPreviewWork(ctx, target.EnvironmentID); err != nil {
		return 0, err
	}
	existing, key, err := s.previewRunKey(ctx, target.EnvironmentID, fmt.Sprintf("pull-request-close:%d", target.PreviewID), false)
	if err != nil {
		return 0, err
	}
	if existing != nil {
		return existing.ID, nil
	}
	run, err := s.enqueueNormalizedDeploymentAtSource(ctx, project, target.EnvironmentID, deploy.OperationPreviewRemove, 0, deploy.TriggerPreview, actor, key, nil, "", "", 0)
	if err != nil {
		return 0, err
	}
	return run.ID, nil
}

// closePreviewTarget is the reconciler's closer: a pull request GitHub
// reports as merged or closed takes its preview down the same way a click
// does.
func (s *Server) closePreviewTarget(ctx context.Context, target deploy.PreviewTarget, merged bool) error {
	reason := "closed"
	if merged {
		reason = "merged"
	}
	_, err := s.closePreview(ctx, nil, target, "reconciler", reason)
	return err
}

// previewTargetOf is a project's preview of one pull request as the closer
// addresses it.
func (s *Server) previewTargetOf(ctx context.Context, projectID int64, number int) (deploy.PreviewTarget, error) {
	preview, err := s.modules.deployAutomation.PreviewByNumber(ctx, projectID, number)
	if err != nil {
		return deploy.PreviewTarget{}, httpx.Internal(err)
	}
	if preview == nil {
		return deploy.PreviewTarget{}, httpx.Err(http.StatusNotFound, "preview_not_found", fmt.Sprintf("pull request #%d has no preview in this project", number))
	}
	trigger, _, err := s.modules.deployAutomation.TriggerByID(ctx, projectID, preview.TriggerID)
	if err != nil {
		return deploy.PreviewTarget{}, mapAutomationError(err)
	}
	return deploy.PreviewTarget{
		PreviewID: preview.ID, TriggerID: preview.TriggerID, ProjectID: projectID, EnvironmentID: preview.EnvironmentID,
		Repository: strings.TrimSuffix(trigger.Config.Repository, ".git"), Number: number,
		Revision: preview.Revision, HeadRevision: preview.HeadRevision, Origin: preview.Origin, State: preview.State,
	}, nil
}

func (s *Server) handleDeploymentPullRequestClose(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	target, err := s.previewTargetOf(r.Context(), projectID, number)
	if err != nil {
		return err
	}
	runID, err := s.closePreview(r.Context(), r, target, httpx.MustPrincipal(r).Username(), "closed")
	if err != nil {
		return mapDeployError(err)
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"runId": runID})
	return nil
}

type pullRequestMergeRequest struct {
	Method       string `json:"method"`
	DeleteBranch bool   `json:"deleteBranch"`
	HeadSHA      string `json:"headSha"`
}

// handleDeploymentPullRequestMerge merges as the dashboard's own account and
// then asks GitHub, through the trusted identity, what became of the
// project's previews of that repository — which closes the merged one — so
// the answer can say whether production redeploys by itself.
func (s *Server) handleDeploymentPullRequestMerge(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	var request pullRequestMergeRequest
	_ = httpx.DecodeJSON(r, &request)
	summary, repository, err := s.deploymentRepository(r.Context(), projectID)
	if err != nil {
		return err
	}
	err = s.modules.pullRequests.Merge(r.Context(), repository, number, request.Method, request.DeleteBranch, request.HeadSHA)
	httpx.SetAudit(r, "github.pull.merge", repository, map[string]any{
		"ok": err == nil, "repository": repository, "number": number, "method": request.Method,
		"deleteBranch": request.DeleteBranch, "deploymentId": projectID,
	})
	if err != nil {
		return pullRequestsError(err)
	}
	s.modules.pullRequests.Invalidate(repository)
	previewRunID := s.reconcileMerged(r.Context(), projectID, repository, number)
	facts := s.previewFactsFor(r.Context(), projectID, summary)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"merged": true, "previewRunId": previewRunID,
		"production": map[string]any{
			"automatic": facts.production.Automatic, "awaitingFirstDeployment": facts.production.AwaitingFirstDeployment,
		},
	})
	return nil
}

// reconcileMerged runs the reconciler over a repository right after a merge
// and answers with the removal run of the project's preview of that pull
// request, when the reconciler closed one.
func (s *Server) reconcileMerged(ctx context.Context, projectID int64, repository string, number int) int64 {
	if s.modules.previewReconciler != nil {
		if err := s.modules.previewReconciler.ReconcileRepository(ctx, repository); err != nil {
			s.Log.Warn("previews could not be reconciled after the merge", "repository", repository, "error", err)
		}
	}
	preview, err := s.modules.deployAutomation.PreviewByNumber(ctx, projectID, number)
	if err != nil || preview == nil || preview.State != "closed" || preview.LastRun == nil || preview.LastRun.Operation != deploy.OperationPreviewRemove {
		return 0
	}
	return preview.LastRun.ID
}

type pullRequestCommentRequest struct {
	Body string `json:"body"`
}

func (s *Server) handleDeploymentPullRequestComment(w http.ResponseWriter, r *http.Request) error {
	projectID, err := parseID(r)
	if err != nil {
		return err
	}
	number, err := pullNumber(r)
	if err != nil {
		return err
	}
	var request pullRequestCommentRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		return err
	}
	if strings.TrimSpace(request.Body) == "" {
		return httpx.BadRequest("a comment body is required")
	}
	_, repository, err := s.deploymentRepository(r.Context(), projectID)
	if err != nil {
		return err
	}
	err = s.modules.pullRequests.Comment(r.Context(), repository, number, request.Body)
	httpx.SetAudit(r, "github.pull.comment", repository, map[string]any{
		"ok": err == nil, "repository": repository, "number": number, "deploymentId": projectID,
	})
	if err != nil {
		return pullRequestsError(err)
	}
	s.modules.pullRequests.Invalidate(repository)
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// fleetPullRequestCounts is one project's line on a fleet card.
type fleetPullRequestCounts struct {
	Open     int `json:"open"`
	Previews int `json:"previews"`
}

// handleDeploymentPullRequestSummary answers the fleet's "N pull requests"
// lines in one request. Each repository is read once however many projects
// deploy it, and a project whose repository cannot be read is left out
// rather than failing the fleet.
func (s *Server) handleDeploymentPullRequestSummary(w http.ResponseWriter, r *http.Request) error {
	fleet, err := s.modules.deployRuns.Fleet(r.Context(), deploy.QueueBudget{
		Heavy: s.Cfg.DeployHeavySlots, Light: s.Cfg.DeployLightSlots,
	})
	if err != nil {
		return httpx.Internal(err)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	byRepository := map[string][]deploy.DeploymentSummary{}
	for _, summary := range fleet.Deployments {
		repository, ok := s.modules.pullRequests.repositoryOf(summary)
		if !ok {
			continue
		}
		byRepository[strings.ToLower(repository)] = append(byRepository[strings.ToLower(repository)], summary)
	}
	type reading struct {
		open int
		err  error
	}
	readings := map[string]reading{}
	var mu sync.Mutex
	var group sync.WaitGroup
	for key, summaries := range byRepository {
		repository, ok := s.modules.pullRequests.repositoryOf(summaries[0])
		if !ok {
			continue
		}
		group.Add(1)
		go func(key, repository string) {
			defer group.Done()
			pulls, _, err := s.modules.pullRequests.List(ctx, repository, "open")
			mu.Lock()
			readings[key] = reading{open: len(pulls), err: err}
			mu.Unlock()
		}(key, repository)
	}
	group.Wait()
	projects := map[string]fleetPullRequestCounts{}
	for key, summaries := range byRepository {
		read := readings[key]
		if read.err != nil {
			continue
		}
		for _, summary := range summaries {
			previews, err := s.modules.deployAutomation.ListPreviews(r.Context(), summary.ID)
			if err != nil {
				continue
			}
			open := 0
			for _, preview := range previews {
				if preview.State == "open" {
					open++
				}
			}
			projects[strconv.FormatInt(summary.ID, 10)] = fleetPullRequestCounts{Open: read.open, Previews: open}
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"projects": projects})
	return nil
}

// The Git page's side: every GitHub-backed checkout's open pull requests,
// read as the checkout's owner the way the rest of that page reads, joined
// to the deploy projects and previews of the same repository.

type gitPullRequestDeployment struct {
	ProjectID     int64  `json:"projectId"`
	Name          string `json:"name"`
	EnvironmentID int64  `json:"environmentId"`
}

type gitPullRequestRepo struct {
	Path        string                     `json:"path"`
	Repository  string                     `json:"repository"`
	Pulls       []pullRequestWithPreview   `json:"pulls"`
	Deployments []gitPullRequestDeployment `json:"deployments"`
	Error       string                     `json:"error,omitempty"`
}

func (s *Server) handleGitPullRequests(w http.ResponseWriter, r *http.Request) error {
	available := s.modules.git.Available() && s.modules.github.Available()
	response := map[string]any{"available": available, "repos": []gitPullRequestRepo{}}
	if !available {
		httpx.JSON(w, http.StatusOK, response)
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var checkouts []gitPullRequestRepo
	if r.URL.Query().Get("path") != "" {
		path, err := s.gitRepo(r)
		if err != nil {
			return err
		}
		repo, err := s.modules.git.Summary(ctx, path)
		if err != nil {
			return gitErr(err)
		}
		if name := deploy.GitHubRepository(deploy.SourceIdentity{Kind: deploy.SourceGit, Remote: repo.Remote}); name != "" {
			checkouts = append(checkouts, gitPullRequestRepo{Path: repo.Path, Repository: name})
		}
	} else {
		repos, err := s.modules.git.Discover(ctx)
		if err != nil {
			return gitErr(err)
		}
		for _, repo := range repos {
			if name := deploy.GitHubRepository(deploy.SourceIdentity{Kind: deploy.SourceGit, Remote: repo.Remote}); name != "" {
				checkouts = append(checkouts, gitPullRequestRepo{Path: repo.Path, Repository: name})
			}
		}
	}
	deployments := map[string][]gitPullRequestDeployment{}
	if fleet, err := s.modules.deployRuns.Fleet(ctx, deploy.QueueBudget{Heavy: s.Cfg.DeployHeavySlots, Light: s.Cfg.DeployLightSlots}); err == nil {
		for _, summary := range fleet.Deployments {
			if repository, ok := s.modules.pullRequests.repositoryOf(summary); ok {
				key := strings.ToLower(repository)
				deployments[key] = append(deployments[key], gitPullRequestDeployment{
					ProjectID: summary.ID, Name: summary.Name, EnvironmentID: summary.EnvironmentID,
				})
			}
		}
	}
	var group sync.WaitGroup
	for index := range checkouts {
		group.Add(1)
		go func(entry *gitPullRequestRepo) {
			defer group.Done()
			entry.Pulls, entry.Deployments = []pullRequestWithPreview{}, []gitPullRequestDeployment{}
			if listed, ok := deployments[strings.ToLower(entry.Repository)]; ok {
				entry.Deployments = listed
			}
			pulls, err := s.modules.pullRequests.CheckoutPulls(ctx, entry.Path, entry.Repository)
			if err != nil {
				entry.Error = err.Error()
				return
			}
			previews, err := s.modules.deployAutomation.PreviewsByRepository(ctx, entry.Repository)
			if err != nil {
				entry.Error = err.Error()
				return
			}
			listed := listedPreviews(previews)
			for _, pull := range pulls {
				entry.Pulls = append(entry.Pulls, pullRequestWithPreview{
					PullRequest: pull, Preview: previewByNumber(listed, pull.Number),
				})
			}
		}(&checkouts[index])
	}
	group.Wait()
	if checkouts == nil {
		checkouts = []gitPullRequestRepo{}
	}
	response["repos"] = checkouts
	httpx.JSON(w, http.StatusOK, response)
	return nil
}
