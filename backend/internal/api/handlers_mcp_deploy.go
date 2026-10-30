package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/mcp"
)

// What each deployment tool does, and what it is held to. A key reaches the
// deployments only through its own grant (api_tokens.deploy_access), read on
// every request with the key: read lists and reads, deploy also queues runs.
// A run is queued through the same functions the deployment routes call, so
// the plan checks, the backup gate, the pull-request refusal and the queue's
// own rules hold here as they do there, and the key's owner must hold the
// capability the route asks: service.control to deploy, destructive (and the
// destructive budget) to roll back. Every call is audited as
// deploy.mcp.<tool> with the deployment as its target.

// mcpOffer is which tools a key is offered: the database tools as its grants
// and role allow, and the deployment tools as its deployment grant and role
// allow.
func mcpOffer(p *httpx.Principal, grants []auth.TokenConnection) mcp.Offer {
	query, write := mcpToolsFor(p, grants)
	access := mcpDeployAccess(p)
	offer := mcp.Offer{Databases: len(grants) > 0, Query: query, Write: write}
	offer.Deployments = access != auth.DeployNone
	offer.Deploy = access == auth.DeployRun && p.Can(auth.CapServiceControl)
	offer.Rollback = offer.Deploy && p.Can(auth.CapDestructive)
	offer.Operations = offer.Deployments && mcpOperationsOn(p)
	return offer
}

func mcpDeployAccess(p *httpx.Principal) auth.DeployAccess {
	if p.Token == nil {
		return auth.DeployNone
	}
	return p.Token.Deployments
}

// mcpDeployment is one deployment as list_deployments states it. Its id and
// its production environment's are what the generated tools take as id and
// env.
type mcpDeployment struct {
	Deployment     string  `json:"deployment"`
	ID             int64   `json:"id"`
	Environment    string  `json:"environment"`
	EnvironmentID  int64   `json:"environmentId"`
	Source         string  `json:"source"`
	Repository     string  `json:"repository,omitempty"`
	Ref            string  `json:"ref,omitempty"`
	Commit         string  `json:"commit,omitempty"`
	Build          string  `json:"build"`
	Framework      string  `json:"framework,omitempty"`
	Address        string  `json:"address,omitempty"`
	Health         string  `json:"health"`
	Stopped        bool    `json:"stopped,omitempty"`
	LiveRelease    int64   `json:"liveRelease,omitempty"`
	PendingChanges bool    `json:"pendingChanges,omitempty"`
	ActiveRun      *mcpRun `json:"activeRun,omitempty"`
	LastRun        *mcpRun `json:"lastRun,omitempty"`
}

// mcpRun is a run as the tools state it: its number is how the run page and
// a notification name it.
type mcpRun struct {
	Run int64 `json:"run"`
	// ID is the run's id, which the generated tools take as run.
	ID          int64      `json:"id"`
	Operation   string     `json:"operation"`
	Trigger     string     `json:"trigger"`
	Actor       string     `json:"actor,omitempty"`
	State       string     `json:"state"`
	Step        string     `json:"step,omitempty"`
	RequestedAt time.Time  `json:"requestedAt"`
	EndedAt     *time.Time `json:"endedAt,omitempty"`
	Commit      string     `json:"commit,omitempty"`
	Release     int64      `json:"release,omitempty"`
	Code        string     `json:"code,omitempty"`
	Reason      string     `json:"reason,omitempty"`
}

func mcpRunOf(run *deploy.EngineRun) *mcpRun {
	if run == nil {
		return nil
	}
	out := &mcpRun{
		Run: run.RunNumber, ID: run.ID, Operation: string(run.Operation), Trigger: string(run.Trigger), Actor: run.Actor,
		State: string(run.State), RequestedAt: run.RequestedAt, EndedAt: run.EndedAt, Commit: run.SourceRevision,
		Release: run.ReleaseID, Code: run.TerminalCode, Reason: clipText(run.TerminalReason, mcp.LineBytes),
	}
	if run.CurrentStep != nil {
		out.Step = run.CurrentStep.Label
	}
	return out
}

// clipText cuts a text at a byte bound on a rune boundary.
func clipText(text string, most int) string {
	if len(text) <= most {
		return text
	}
	cut := text[:most]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}

// needDeployments refuses a key that was not given the deployments, and
// returns false.
func (c *mcpCall) needDeployments() (map[string]any, bool) {
	if mcpDeployAccess(c.p) == auth.DeployNone {
		return c.refuse("not_granted", "This key was not given the deployments. Its owner can give it them under Databases → MCP server."), false
	}
	return nil, true
}

// deployment finds the deployment a call names among those list_deployments
// gives: by its name, or by a name that matches one of them alone ignoring case.
func (c *mcpCall) deployment(name string) (*deploy.DeploymentSummary, map[string]any) {
	if refusal, ok := c.needDeployments(); !ok {
		return nil, refusal
	}
	if name == "" {
		return nil, c.refuse("arguments", "Name the deployment: deployment is required.")
	}
	c.target = clipTarget(name)
	fleet, err := c.s.modules.deployRuns.Fleet(c.r.Context(), c.s.deployBudget())
	if err != nil {
		return nil, c.fail("the deployments could not be read", nil)
	}
	var folded []*deploy.DeploymentSummary
	for i := range fleet.Deployments {
		one := &fleet.Deployments[i]
		// A key narrowed to chosen deployments finds those alone, as a
		// deploy key narrowed so does.
		if !c.p.Token.ReachesDeployment(one.ID) {
			continue
		}
		if one.Name == name {
			c.target = one.Name
			return one, nil
		}
		if strings.EqualFold(one.Name, name) {
			folded = append(folded, one)
		}
	}
	if len(folded) == 1 {
		c.target = folded[0].Name
		return folded[0], nil
	}
	return nil, c.refuse("not_found", fmt.Sprintf("No deployment is named %q; list_deployments names them.", name))
}

func (s *Server) deployBudget() deploy.QueueBudget {
	return deploy.QueueBudget{Heavy: s.Cfg.DeployHeavySlots, Light: s.Cfg.DeployLightSlots}
}

// deployError is what the model is told about a run the deployment code
// refused: the route's own sentence, under the route's own code. A failure
// the dashboard could not answer at all is named without its text.
func (c *mcpCall) deployError(err error) map[string]any {
	var api *httpx.APIError
	if !errors.As(mapDeployError(err), &api) {
		return c.fail("the deployment could not be read", nil)
	}
	if api.Status >= http.StatusInternalServerError {
		c.failed = true
		c.detail["outcome"], c.detail["error"] = "failed", api.Code
		if api.Code == "internal" {
			return mcp.Failure("The dashboard could not answer; try again.")
		}
		return mcp.Failure(api.Message)
	}
	return c.refuse(api.Code, api.Message)
}

func (c *mcpCall) listDeployments() map[string]any {
	if refusal, ok := c.needDeployments(); !ok {
		return refusal
	}
	fleet, err := c.s.modules.deployRuns.Fleet(c.r.Context(), c.s.deployBudget())
	if err != nil {
		return c.fail("the deployments could not be read", nil)
	}
	out := make([]mcpDeployment, 0, len(fleet.Deployments))
	for _, d := range fleet.Deployments {
		if !c.p.Token.ReachesDeployment(d.ID) {
			continue
		}
		out = append(out, mcpDeployment{
			Deployment: d.Name, ID: d.ID, Environment: d.EnvironmentName, EnvironmentID: d.EnvironmentID, Source: string(d.SourceKind),
			Repository: d.SourceRepository, Ref: d.SourceRef, Commit: d.SourceRevision,
			Build: string(d.BuildMethod), Framework: d.Framework, Address: d.Endpoint,
			Health: d.Health, Stopped: d.Stopped, LiveRelease: d.LiveReleaseID, PendingChanges: d.PendingChanges,
			ActiveRun: mcpRunOf(d.ActiveRun), LastRun: mcpRunOf(d.LastRun),
		})
	}
	c.detail["deployments"] = len(out)
	return c.answer(map[string]any{"deployments": out})
}

func (c *mcpCall) listRuns(args mcp.Arguments) map[string]any {
	d, refusal := c.deployment(args.Deployment)
	if refusal != nil {
		return refusal
	}
	limit := clampInt(args.Limit, 1, mcp.RunsMax)
	if args.Limit == 0 {
		limit = mcp.RunsDefault
	}
	runs, next, err := c.s.modules.deployRuns.ProjectRunsFiltered(c.r.Context(), d.ID,
		deploy.RunListFilter{Environment: d.EnvironmentID, Limit: limit})
	if err != nil {
		return c.fail("the runs could not be read", nil)
	}
	out := make([]*mcpRun, 0, len(runs))
	for i := range runs {
		out = append(out, mcpRunOf(&runs[i]))
	}
	return c.answer(map[string]any{"deployment": d.Name, "runs": out, "more": next != 0})
}

type mcpStep struct {
	Step    string `json:"step"`
	State   string `json:"state"`
	Attempt int    `json:"attempt,omitempty"`
	Code    string `json:"code,omitempty"`
	Error   string `json:"error,omitempty"`
}

type mcpLine struct {
	Step   string    `json:"step,omitempty"`
	Stream string    `json:"stream"`
	At     time.Time `json:"at"`
	Text   string    `json:"text"`
}

func (c *mcpCall) readRunLog(args mcp.Arguments) map[string]any {
	d, refusal := c.deployment(args.Deployment)
	if refusal != nil {
		return refusal
	}
	ctx := c.r.Context()
	var run *deploy.EngineRun
	var err error
	if args.Run > 0 {
		run, err = c.s.modules.deployRuns.ProjectRunByNumber(ctx, d.ID, args.Run)
	} else if d.ActiveRun != nil {
		run = d.ActiveRun
	} else if d.LastRun != nil {
		run = d.LastRun
	} else {
		err = deploy.ErrRunNotFound
	}
	if errors.Is(err, deploy.ErrRunNotFound) {
		if args.Run > 0 {
			return c.refuse("run_not_found", fmt.Sprintf("%s has no run #%d; list_runs names its runs.", d.Name, args.Run))
		}
		return c.refuse("run_not_found", d.Name+" has not run yet.")
	}
	if err != nil {
		return c.fail("the run could not be read", nil)
	}
	c.detail["run"] = run.RunNumber
	snapshot, err := c.s.modules.deployRuns.Snapshot(ctx, run.ID)
	if err != nil {
		return c.deployError(err)
	}
	lines := clampInt(args.Lines, 1, mcp.LogMax)
	if args.Lines == 0 {
		lines = mcp.LogDefault
	}
	tail, earlier, err := c.s.modules.deployRuns.RunTranscriptTail(ctx, run.ID, lines)
	if err != nil {
		return c.fail("the run's transcript could not be read", nil)
	}
	stepNames := map[int64]string{}
	steps := make([]mcpStep, 0, len(snapshot.Steps))
	for _, step := range snapshot.Steps {
		label := deploy.StepLabel(step.Key, snapshot.Run.Operation)
		if label == "" {
			label = string(step.Key)
		}
		stepNames[step.ID] = label
		steps = append(steps, mcpStep{
			Step: label, State: string(step.State), Attempt: step.Attempt,
			Code: step.ErrorCode, Error: clipText(step.ErrorMessage, mcp.LineBytes),
		})
	}
	// The newest lines are kept when the budget runs out: the end of a
	// transcript is where it failed.
	out := []mcpLine{}
	total := 0
	for i := len(tail) - 1; i >= 0; i-- {
		line := mcpLine{Step: stepNames[tail[i].StepID], Stream: tail[i].Stream, At: tail[i].At,
			Text: clipText(tail[i].Text, mcp.LineBytes)}
		if total+len(line.Text) > mcp.ResultBytes && len(out) > 0 {
			earlier = true
			break
		}
		total += len(line.Text)
		out = append(out, line)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return c.answer(map[string]any{
		"deployment": d.Name, "run": mcpRunOf(&snapshot.Run), "steps": steps,
		"lines": out, "earlierLines": earlier,
	})
}

type mcpRelease struct {
	Release     int64      `json:"release"`
	Number      int64      `json:"number"`
	State       string     `json:"state"`
	Commit      string     `json:"commit,omitempty"`
	ImageDigest string     `json:"imageDigest,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	ActivatedAt *time.Time `json:"activatedAt,omitempty"`
	Pinned      bool       `json:"pinned,omitempty"`
}

func (c *mcpCall) listReleases(args mcp.Arguments) map[string]any {
	d, refusal := c.deployment(args.Deployment)
	if refusal != nil {
		return refusal
	}
	limit := clampInt(args.Limit, 1, mcp.ReleasesMax)
	if args.Limit == 0 {
		limit = mcp.ReleasesDefault
	}
	releases, err := c.s.modules.deployRuns.EnvironmentReleases(c.r.Context(), d.ID, d.EnvironmentID, limit)
	if err != nil {
		return c.deployError(err)
	}
	out := make([]mcpRelease, 0, len(releases))
	for _, r := range releases {
		out = append(out, mcpRelease{
			Release: r.ID, Number: r.Number, State: r.State, Commit: r.SourceRevision, ImageDigest: r.ImageDigest,
			CreatedAt: r.CreatedAt, ActivatedAt: r.ActivatedAt, Pinned: r.Pinned,
		})
	}
	return c.answer(map[string]any{"deployment": d.Name, "releases": out})
}

// mayRunDeploy refuses a key that may not deploy, and an owner whose role
// does not hold what the route asks, before the deployment is looked for.
func (c *mcpCall) mayRunDeploy(name string, capability auth.Capability, verb string) (map[string]any, bool) {
	c.target = clipTarget(name)
	if refusal, ok := c.needDeployments(); !ok {
		return refusal, false
	}
	if mcpDeployAccess(c.p) != auth.DeployRun {
		return c.refuse("read_only", "This key may read the deployments but not "+verb+" them. Its owner can allow it under Databases → MCP server."), false
	}
	if !c.p.Can(auth.CapServiceControl) || !c.p.Can(capability) {
		return c.refuse("role", fmt.Sprintf("The key's owner is a %s account, which may not %s a deployment.", c.p.Role, verb)), false
	}
	return nil, true
}

func (c *mcpCall) deploy(args mcp.Arguments) map[string]any {
	if refusal, ok := c.mayRunDeploy(args.Deployment, auth.CapServiceControl, "deploy"); !ok {
		return refusal
	}
	if args.Ref != "" && args.Revision != "" {
		return c.refuse("arguments", "Name a ref or a revision, not both.")
	}
	d, refusal := c.deployment(args.Deployment)
	if refusal != nil {
		return refusal
	}
	ctx := c.r.Context()
	if err := deploy.RequireSupportedProject(ctx, c.s.Store.DB, d.ID); err != nil {
		return c.deployError(err)
	}
	project, err := c.s.modules.deployStore.Get(ctx, d.ID)
	if err != nil {
		return c.deployError(err)
	}
	var run *deploy.EngineRun
	if d.BuildMethod == deploy.BuildLegacyCompose {
		if args.Ref != "" || args.Revision != "" {
			return c.refuse("ref_not_applicable", d.Name+" is a Compose deployment from before releases, which builds its configured branch only.")
		}
		run, err = c.s.enqueueLegacyDeployment(ctx, project, deploy.OperationDeploy,
			deploy.TriggerManual, c.p.Username(), "", "")
	} else {
		run, err = c.s.enqueueNormalizedDeploymentAtSource(ctx, project, d.EnvironmentID,
			deploy.OperationDeploy, 0, deploy.TriggerManual, c.p.Username(), "", nil, args.Revision, args.Ref, 0)
	}
	if err != nil {
		return c.deployError(err)
	}
	c.detail["run"], c.detail["runId"] = run.RunNumber, run.ID
	if args.Ref != "" {
		c.detail["ref"] = args.Ref
	}
	if args.Revision != "" {
		c.detail["revision"] = args.Revision
	}
	return c.answer(map[string]any{"deployment": d.Name, "queued": mcpRunOf(run)})
}

func (c *mcpCall) rollback(args mcp.Arguments) map[string]any {
	if refusal, ok := c.mayRunDeploy(args.Deployment, auth.CapDestructive, "roll back"); !ok {
		return refusal
	}
	d, refusal := c.deployment(args.Deployment)
	if refusal != nil {
		return refusal
	}
	if d.BuildMethod == deploy.BuildLegacyCompose {
		return c.refuse("legacy", d.Name+" is a Compose deployment from before releases; roll it back to a commit from its page in the dashboard.")
	}
	ctx := c.r.Context()
	if err := deploy.RequireSupportedProject(ctx, c.s.Store.DB, d.ID); err != nil {
		return c.deployError(err)
	}
	target := args.Release
	if target == 0 {
		releases, err := c.s.modules.deployRuns.EnvironmentReleases(ctx, d.ID, d.EnvironmentID, 200)
		if err != nil {
			return c.deployError(err)
		}
		// Releases come newest first; the first retained one after the live
		// one is what serving was before it.
		passedLive := d.LiveReleaseID == 0
		for _, r := range releases {
			if r.ID == d.LiveReleaseID {
				passedLive = true
				continue
			}
			if passedLive && r.State == "retained" {
				target = r.ID
				break
			}
		}
		if target == 0 {
			return c.refuse("no_release", d.Name+" has no retained release older than the live one to go back to; list_releases names what it has.")
		}
	}
	// The rollback route spends the destructive budget per account and
	// address, and so does this.
	if !c.s.destrLim.Allow(c.p.Username() + "|" + httpx.ClientIP(c.r)) {
		return c.refuse("rate_limited", "Too many destructive actions from this account; try again shortly.")
	}
	project, err := c.s.modules.deployStore.Get(ctx, d.ID)
	if err != nil {
		return c.deployError(err)
	}
	run, err := c.s.enqueueNormalizedDeployment(ctx, project, d.EnvironmentID,
		deploy.OperationRollback, target, deploy.TriggerRollback, c.p.Username(), "")
	if err != nil {
		return c.deployError(err)
	}
	c.detail["release"], c.detail["run"], c.detail["runId"] = target, run.RunNumber, run.ID
	return c.answer(map[string]any{"deployment": d.Name, "release": target, "queued": mcpRunOf(run)})
}
