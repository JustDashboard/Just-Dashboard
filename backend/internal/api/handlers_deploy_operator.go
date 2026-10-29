package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The deployment operator (assistant/operator.go): the assistant's provider,
// asked about one deployment and the server under it. It reads through
// tools the person allows one call at a time, the SQL editor's rule; it may
// propose an action, which runs nothing here — the page draws a card, and
// the person's press on it calls the route the dashboard already has for
// that action, with that route's capability and audit. There is no shell and
// no other path from an answer to the host.

const (
	maxOperatorQuestion = 4000
	// maxOperatorResult bounds what one read answers the model with, as a
	// query result is bounded: the newest of a read is kept.
	maxOperatorResult = 24_000
	// operatorContinues bounds how many times one press carries an answer on
	// past proposals it made, which need nobody's leave.
	operatorContinues = 3
)

type operatorRequest struct {
	// Conversation carries one on; empty begins one.
	Conversation string `json:"conversation"`
	// Question is asked; Decisions, instead, answer the reads the last
	// answer ended on — true runs one, false declines it, and a call left
	// out is declined.
	Question  string          `json:"question"`
	Decisions map[string]bool `json:"decisions"`
}

// operatorPending is a read waiting for the person, as the page draws it.
type operatorPending struct {
	ID    string                  `json:"id"`
	Tool  string                  `json:"tool"`
	Label string                  `json:"label"`
	Input assistant.OperatorInput `json:"input"`
}

// operatorProposal is an action the operator proposed: what the page's card
// says and which route its press calls. Nothing has run.
type operatorProposal struct {
	ID            string `json:"id"`
	Action        string `json:"action"`
	Label         string `json:"label"`
	Reason        string `json:"reason"`
	ReleaseID     int64  `json:"releaseId,omitempty"`
	ReleaseNumber int64  `json:"releaseNumber,omitempty"`
	// Capability is what the action's route takes; a person without it is
	// shown the card without the press.
	Capability  string `json:"capability"`
	Destructive bool   `json:"destructive"`
}

// handleDeploymentOperator asks the operator one question about one
// environment (`?environment=` names staging), or answers the reads it asked
// for, and streams the answer back as one JSON object a line, the shape the
// SQL editor's assistant streams in.
func (s *Server) handleDeploymentOperator(w http.ResponseWriter, r *http.Request) error {
	summary, err := s.deploymentTarget(r)
	if err != nil {
		return err
	}
	var req operatorRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	req.Question = strings.TrimSpace(req.Question)
	if (req.Question == "") == (req.Decisions == nil) {
		return httpx.BadRequest("ask a question, or answer the reads the assistant asked for")
	}
	if len(req.Question) > maxOperatorQuestion || !utf8.ValidString(req.Question) {
		return httpx.BadRequest("a question is at most %d characters", maxOperatorQuestion)
	}
	action := "deploy.assistant.ask"
	if req.Decisions != nil {
		action = "deploy.assistant.tool"
	}
	httpx.SetAudit(r, action, summary.Name, nil)
	settings, err := s.assistantSettings(r.Context())
	if err != nil {
		return err
	}
	if settings == nil {
		return httpx.Err(http.StatusConflict, "assistant_off",
			"the assistant is not set up: an administrator chooses its provider in the assistant's settings")
	}
	if !s.assistant.limit.Allow("install") {
		return httpx.Err(http.StatusTooManyRequests, "rate_limited",
			"the assistant has answered as many questions as this install allows in a minute; ask again shortly")
	}
	select {
	case s.assistant.slots <- struct{}{}:
		defer func() { <-s.assistant.slots }()
	default:
		return httpx.Err(http.StatusTooManyRequests, "assistant_busy",
			"the assistant is answering several questions already; ask again when one of them has finished")
	}
	client, err := assistant.New(*settings, s.assistant.options)
	if err != nil {
		return httpx.Internal(err)
	}
	store := s.assistant.operators
	principal := httpx.MustPrincipal(r)
	owner := principal.Username()
	var c *assistant.Conversation
	if req.Conversation == "" {
		if req.Question == "" {
			return httpx.BadRequest("there is no conversation to answer for")
		}
		c = store.Begin(owner, summary.EnvironmentID)
		c.System = assistant.OperatorSystemPrompt(s.operatorContext(r.Context(), summary))
		c.Tools, c.Offer, c.Check = client.Tools(), assistant.OperatorTools, assistant.OperatorCalls
		c.Revision = settings.Revision()
	} else {
		c, err = store.Take(req.Conversation, owner, summary.EnvironmentID)
		switch {
		case errors.Is(err, assistant.ErrGone):
			return httpx.Err(http.StatusNotFound, "conversation_gone", "this conversation has ended: ask again to begin a new one")
		case errors.Is(err, assistant.ErrBusy):
			return httpx.Err(http.StatusConflict, "conversation_busy", "this conversation is still answering the last question")
		}
		if c.Revision != settings.Revision() {
			store.End(c)
			return httpx.Err(http.StatusConflict, "conversation_ended",
				"the assistant's settings changed since this conversation began: ask again to begin a new one")
		}
	}
	defer store.Release(c)

	mark := c.Mark()
	detail := map[string]any{
		"provider": settings.Provider, "model": settings.Model, "deploymentId": summary.ID,
		"environmentId": summary.EnvironmentID, "conversation": c.ID[:8], "turn": c.Turns + 1,
	}
	stream := &assistantStream{w: w, start: map[string]any{
		"type": "start", "conversation": c.ID, "provider": settings.Provider, "model": settings.Model,
		"modelLabel": assistant.ModelLabel(settings.Provider, settings.Model), "tools": c.Tools,
	}}
	if req.Question != "" {
		if err := c.Ask(req.Question); err != nil {
			return httpx.Err(http.StatusConflict, "conversation_full", err.Error())
		}
	} else {
		if len(c.Pending) == 0 {
			return httpx.Err(http.StatusConflict, "nothing_pending", "the assistant is not waiting for an answer")
		}
		allowed, declined := []string{}, []string{}
		ran := []map[string]any{}
		results := assistant.ToolResults(c.Pending, func(call assistant.ToolCall) (string, bool) {
			if !req.Decisions[call.ID] {
				declined = append(declined, call.Name)
				ran = append(ran, map[string]any{"id": call.ID, "tool": call.Name, "allowed": false})
				return assistant.OperatorDeclined, false
			}
			allowed = append(allowed, call.Name)
			ctx, cancel := timeoutCtx(r, 60*time.Second)
			defer cancel()
			text, said, ok := s.operatorRead(ctx, r, principal, summary, call)
			ran = append(ran, map[string]any{"id": call.ID, "tool": call.Name, "allowed": true, "read": said, "failed": !ok})
			return text, ok
		})
		c.Answer(results)
		detail["allowed"], detail["declined"] = allowed, declined
		stream.send(map[string]any{"type": "ran", "calls": ran})
	}

	ctx, cancel := context.WithTimeout(r.Context(), assistantTurn)
	defer cancel()
	emit := func(e assistant.Event) {
		if e.Thinking {
			stream.send(map[string]any{"type": "thinking"})
			return
		}
		stream.send(map[string]any{"type": "text", "text": e.Text})
	}
	var usage assistant.Usage
	var outcome assistant.Outcome
	proposed := []string{}
	var pending []operatorPending
	for round := 0; ; round++ {
		outcome, err = assistant.Exchange(ctx, client, c, emit)
		usage = addUsage(usage, outcome.Usage)
		if err != nil || !outcome.Kept || len(outcome.Pending) == 0 {
			break
		}
		// A proposal is answered at once — it runs nothing — and a read
		// waits for the person, with the proposals' answers held so the
		// one message after the answer carries every result.
		var reads []assistant.ToolCall
		var answers []assistant.Block
		for _, call := range outcome.Pending {
			if !call.Proposal() {
				reads = append(reads, call)
				continue
			}
			proposal, refusal := s.operatorProposal(r.Context(), principal, summary, call)
			if proposal != nil {
				proposed = append(proposed, proposal.Action)
				stream.send(map[string]any{"type": "proposal", "proposal": proposal})
				answers = append(answers, assistant.Block{Type: assistant.BlockToolResult, ToolID: call.ID, Text: assistant.Proposed})
			} else {
				answers = append(answers, assistant.Block{Type: assistant.BlockToolResult, ToolID: call.ID, Text: refusal, IsError: true})
			}
		}
		if len(reads) > 0 || round >= operatorContinues {
			c.Pending, c.Answered = reads, answers
			for _, call := range reads {
				pending = append(pending, operatorPending{ID: call.ID, Tool: call.Name, Label: operatorReadLabel(call), Input: call.OperatorInput()})
			}
			break
		}
		c.Pending = nil
		c.Answer(answers)
	}
	detail["usage"] = usage
	if len(proposed) > 0 {
		detail["proposed"] = proposed
	}
	if outcome.Model != "" && outcome.Model != settings.Model {
		detail["answeredBy"] = outcome.Model
	}
	if err != nil {
		c.Restore(mark)
		message, failure := assistantFailure(r.Context(), ctx, err)
		detail["outcome"] = failure
		httpx.SetAudit(r, action, summary.Name, detail)
		if !stream.started {
			if failure == "cancelled" {
				return nil
			}
			pe, _ := assistant.IsProviderError(err)
			apiErr := httpx.Err(http.StatusBadGateway, "assistant_failed", message)
			if pe != nil {
				apiErr.Raw, apiErr.Retryable = pe.Raw, pe.Retryable
			}
			return apiErr
		}
		stream.send(map[string]any{"type": "error", "error": map[string]any{"code": "assistant_failed", "message": message}})
		return nil
	}
	if !outcome.Kept {
		c.Restore(mark)
	}
	outcome.Pending = c.Pending
	detail["outcome"] = assistantOutcomeWord(outcome)
	httpx.SetAudit(r, action, summary.Name, detail)
	done := map[string]any{"type": "done", "stop": outcome.Stop, "usage": usage, "model": outcome.Model, "kept": outcome.Kept}
	if len(pending) > 0 {
		done["pending"] = pending
	}
	stream.send(done)
	return nil
}

func addUsage(a, b assistant.Usage) assistant.Usage {
	return assistant.Usage{Input: a.Input + b.Input, Output: a.Output + b.Output,
		CacheRead: a.CacheRead + b.CacheRead, CacheWrite: a.CacheWrite + b.CacheWrite}
}

// operatorContext is what the conversation begins knowing: what the
// project's page already shows anybody who may read it.
func (s *Server) operatorContext(ctx context.Context, summary *deploy.DeploymentSummary) assistant.OperatorContext {
	out := assistant.OperatorContext{
		Deployment: summary.Name, Environment: summary.EnvironmentName, Kind: string(summary.EnvironmentKind),
		Source: strings.TrimSpace(string(summary.SourceKind) + " " + summary.SourceRepository + " " + summary.SourceRef),
		Build:  string(summary.BuildMethod), Address: summary.Endpoint, Health: summary.Health,
	}
	if summary.LiveReleaseID > 0 {
		if release, err := s.modules.deployRuns.Release(ctx, summary.LiveReleaseID); err == nil {
			out.LiveRelease = release.Release.Number
		}
	}
	// A run's reason is masked as a read's is; with nothing to mask by, the
	// line goes without it.
	masker, _ := s.operatorMasker(ctx, summary, deploy.EngineRun{})
	if summary.ActiveRun != nil {
		out.ActiveRun = runLine(summary.ActiveRun, masker)
	}
	if summary.LastRun != nil {
		out.LastRun = runLine(summary.LastRun, masker)
	}
	if watch, err := s.modules.deployRuns.OpenRollbackWatch(ctx, summary.EnvironmentID); err == nil && watch.State == deploy.RollbackWatchWatching {
		out.WarmRelease = watch.WarmReleaseNum
	}
	if s.modules.sys != nil {
		if info, err := s.modules.sys.Host(ctx); err == nil && info != nil {
			out.Server = info.Hostname
		}
	}
	return out
}

func runLine(run *deploy.EngineRun, masker *deploy.DiagnosisMasker) string {
	line := fmt.Sprintf("#%d %s, %s", run.RunNumber, run.Operation, run.State)
	if run.TerminalReason != "" && masker != nil {
		reason, _ := masker.Mask(run.TerminalReason)
		line += ": " + clipText(reason, 300)
	}
	return line
}

// operatorReadLabel is a read as its card names it.
func operatorReadLabel(call assistant.ToolCall) string {
	in := call.OperatorInput()
	switch call.Name {
	case assistant.ToolRecentRuns:
		return fmt.Sprintf("Read the last %d runs", in.Limit)
	case assistant.ToolRunTranscript:
		return fmt.Sprintf("Read run #%d's transcript (last %d lines)", in.Run, in.Lines)
	case assistant.ToolContainerLogs:
		if in.Service != "" {
			return fmt.Sprintf("Read the last %d lines %s wrote", in.Lines, in.Service)
		}
		return fmt.Sprintf("Read the last %d lines the live containers wrote", in.Lines)
	case assistant.ToolReleaseMetrics:
		return "Read the live release's usage and right-sizing verdict"
	case assistant.ToolLifecycleEvents:
		return fmt.Sprintf("Read the container events of the last %d hours", in.Hours)
	case assistant.ToolHostAlerts:
		return "Read the server's firing host alerts"
	case assistant.ToolFirewallRules:
		return "Read the firewall's rules for this deployment's ports"
	case assistant.ToolSecurityUpdates:
		return "Read the server's pending security updates"
	}
	return call.Name
}

// operatorRead runs one allowed read and answers the model with its JSON,
// bounded, and the page with a few words of what was read. A read the caller's
// role could not make through the dashboard's own route answers that instead.
func (s *Server) operatorRead(ctx context.Context, r *http.Request, p *httpx.Principal, summary *deploy.DeploymentSummary, call assistant.ToolCall) (string, string, bool) {
	in := call.OperatorInput()
	refuse := func(why string) (string, string, bool) { return why, why, false }
	answer := func(v any, said string) (string, string, bool) {
		raw, err := json.Marshal(v)
		if err != nil {
			return refuse("the read could not be written out")
		}
		text := string(raw)
		if len(text) > maxOperatorResult {
			text = clipText(text, maxOperatorResult) + " (cut at the read's bound)"
		}
		return text, said, true
	}
	switch call.Name {
	case assistant.ToolRecentRuns:
		runs, _, err := s.modules.deployRuns.ProjectRunsFiltered(ctx, summary.ID, deploy.RunListFilter{Environment: summary.EnvironmentID, Limit: in.Limit})
		if err != nil {
			return refuse("the runs could not be read")
		}
		masker, err := s.operatorMasker(ctx, summary, deploy.EngineRun{})
		if err != nil {
			return refuse("the environment's secret values could not be read to mask, so nothing was read")
		}
		out := make([]*mcpRun, 0, len(runs))
		for i := range runs {
			run := mcpRunOf(&runs[i])
			run.Reason, _ = masker.Mask(run.Reason)
			out = append(out, run)
		}
		return answer(map[string]any{"runs": out}, operatorCount(len(out), "run"))
	case assistant.ToolRunTranscript:
		return s.operatorTranscript(ctx, summary, in, answer, refuse)
	case assistant.ToolContainerLogs:
		return s.operatorContainerLogs(ctx, summary, in, answer, refuse)
	case assistant.ToolReleaseMetrics:
		targets, err := s.modules.deployPlanning.SizingTargets(ctx, summary.ID, summary.EnvironmentID)
		if err != nil || len(targets) == 0 {
			return refuse("the environment's sizing could not be read")
		}
		sizing, err := s.sizeEnvironment(ctx, targets[0], time.Now(), s.hostCPUs(ctx))
		if err != nil {
			return refuse("the environment's sizing could not be read")
		}
		containers := []map[string]any{}
		if s.modules.docker != nil {
			list, err := s.modules.docker.ListContainersWithLastRun(ctx, map[string]string{
				"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": strconv.FormatInt(summary.EnvironmentID, 10)})
			if err == nil {
				for _, container := range list {
					containers = append(containers, map[string]any{
						"name": container.Name, "state": container.State, "status": container.Status, "health": container.Health,
						"release": container.Labels["io.just-dashboard.release-number"], "startedAt": container.StartedAt,
						"memoryLimitBytes": container.MemoryLimit,
					})
				}
			}
		}
		return answer(map[string]any{"sizing": sizing, "containers": containers}, "usage and the sizing verdict")
	case assistant.ToolLifecycleEvents:
		if s.modules.dockerEvents == nil {
			return refuse("Docker is unavailable on this host, so container lifecycle cannot be read")
		}
		events := s.deploymentLifecycleEvents(r, summary.EnvironmentID, 100)
		since := time.Now().Add(-time.Duration(in.Hours) * time.Hour)
		kept := []dockerx.Event{}
		for _, event := range events {
			if event.Time.After(since) {
				kept = append(kept, event)
			}
		}
		s.correlateDeploymentEvents(r, kept, summary.Name)
		return answer(map[string]any{"hours": in.Hours, "events": kept}, operatorCount(len(kept), "event"))
	case assistant.ToolHostAlerts:
		// The host's Alerts page is an administrator's, so its readings are.
		if !p.Can(auth.CapSystemAdmin) {
			return refuse("your role does not read the host's alerts (" + string(auth.CapSystemAdmin) + ")")
		}
		if s.modules.hostAlerts == nil {
			return refuse("host alerts are unavailable")
		}
		subjects, err := s.modules.hostAlerts.Subjects(ctx)
		if err != nil {
			return refuse("the host alerts could not be read")
		}
		return answer(map[string]any{"alerts": subjects}, operatorCount(len(subjects), "alert"))
	case assistant.ToolFirewallRules:
		if s.modules.netsec == nil {
			return refuse("the firewall is unavailable on this host")
		}
		status, err := s.modules.netsec.Status(ctx)
		if err != nil || status == nil {
			return refuse("the firewall could not be read")
		}
		ports := operatorPorts(summary)
		rules := []any{}
		for _, rule := range status.Rules {
			raw, _ := json.Marshal(rule)
			if mentionsPort(string(raw), ports) {
				rules = append(rules, rule)
			}
		}
		return answer(map[string]any{
			"backend": status.Backend, "available": status.Available, "enabled": status.Enabled, "policy": status.Policy,
			"ports": ports, "rules": rules, "otherRules": len(status.Rules) - len(rules),
		}, fmt.Sprintf("%s for ports %v", operatorCount(len(rules), "rule"), ports))
	case assistant.ToolSecurityUpdates:
		if s.modules.updates == nil {
			return refuse("package updates are unavailable on this host")
		}
		report, err := s.modules.updates.Check(ctx)
		if err != nil || report == nil {
			return refuse("the pending updates could not be read")
		}
		security := []any{}
		for _, pkg := range report.Packages {
			if pkg.Security {
				security = append(security, pkg)
			}
		}
		return answer(map[string]any{
			"manager": report.Manager, "securityFiltering": report.SecurityFiltering, "security": security,
			"securityCount": report.SecurityCount, "allPending": len(report.Packages), "rebootRequired": report.RebootRequired,
		}, operatorCount(len(security), "security update"))
	}
	return refuse("no such read")
}

// operatorMasker masks the environment's secret values out of what a read
// sends, as a failed run's diagnosis does: a line the dashboard prints to a
// page is not therefore a line to send to somebody else's model unmasked.
func (s *Server) operatorMasker(ctx context.Context, summary *deploy.DeploymentSummary, run deploy.EngineRun) (*deploy.DiagnosisMasker, error) {
	if run.EnvironmentID == 0 {
		run.EnvironmentID = summary.EnvironmentID
	}
	values, err := s.modules.deployPlanning.DiagnosisSecrets(ctx, run)
	if err != nil {
		return nil, err
	}
	return deploy.NewDiagnosisMasker(values), nil
}

type operatorAnswer func(any, string) (string, string, bool)
type operatorRefusal func(string) (string, string, bool)

func (s *Server) operatorTranscript(ctx context.Context, summary *deploy.DeploymentSummary, in assistant.OperatorInput, answer operatorAnswer, refuse operatorRefusal) (string, string, bool) {
	run, err := s.modules.deployRuns.ProjectRunByNumber(ctx, summary.ID, in.Run)
	if err != nil {
		return refuse(fmt.Sprintf("%s has no run #%d", summary.Name, in.Run))
	}
	snapshot, err := s.modules.deployRuns.Snapshot(ctx, run.ID)
	if err != nil {
		return refuse("the run could not be read")
	}
	masker, err := s.operatorMasker(ctx, summary, snapshot.Run)
	if err != nil {
		return refuse("the run's secret values could not be read to mask, so nothing was read")
	}
	tail, earlier, err := s.modules.deployRuns.RunTranscriptTail(ctx, run.ID, in.Lines)
	if err != nil {
		return refuse("the run's transcript could not be read")
	}
	names := map[int64]string{}
	steps := make([]mcpStep, 0, len(snapshot.Steps))
	for _, step := range snapshot.Steps {
		label := deploy.StepLabel(step.Key, snapshot.Run.Operation)
		if label == "" {
			label = string(step.Key)
		}
		names[step.ID] = label
		message, _ := masker.Mask(step.ErrorMessage)
		steps = append(steps, mcpStep{Step: label, State: string(step.State), Attempt: step.Attempt, Code: step.ErrorCode, Error: clipText(message, 2000)})
	}
	lines := make([]mcpLine, 0, len(tail))
	for _, line := range tail {
		text, _ := masker.Mask(line.Text)
		lines = append(lines, mcpLine{Step: names[line.StepID], Stream: line.Stream, At: line.At, Text: clipText(text, 2000)})
	}
	return answer(map[string]any{"run": mcpRunOf(&snapshot.Run), "steps": steps, "lines": lines, "earlierLines": earlier},
		fmt.Sprintf("run #%d, %s", in.Run, operatorCount(len(lines), "line")))
}

func (s *Server) operatorContainerLogs(ctx context.Context, summary *deploy.DeploymentSummary, in assistant.OperatorInput, answer operatorAnswer, refuse operatorRefusal) (string, string, bool) {
	if s.modules.docker == nil {
		return refuse("Docker is unavailable on this host")
	}
	if summary.LiveReleaseID <= 0 {
		return refuse("this environment has no live release, so no container to read")
	}
	list, err := s.modules.docker.ListContainersWithLabels(ctx, map[string]string{
		"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": strconv.FormatInt(summary.EnvironmentID, 10),
		"io.just-dashboard.release-id": strconv.FormatInt(summary.LiveReleaseID, 10)})
	if err != nil {
		return refuse("the live release's containers could not be listed")
	}
	masker, err := s.operatorMasker(ctx, summary, deploy.EngineRun{})
	if err != nil {
		return refuse("the environment's secret values could not be read to mask, so nothing was read")
	}
	type containerLines struct {
		Name  string   `json:"name"`
		State string   `json:"state"`
		Lines []string `json:"lines"`
	}
	out := []containerLines{}
	for _, container := range list {
		if in.Service != "" && container.ComposeSvc != in.Service && container.Name != in.Service {
			continue
		}
		lines, closer, err := s.modules.docker.Logs(ctx, container.ID, dockerx.LogOptions{
			Tail: strconv.Itoa(in.Lines), Timestamps: true, Stdout: true, Stderr: true})
		read := containerLines{Name: container.Name, State: container.State, Lines: []string{}}
		if err == nil {
			for line := range lines {
				text, _ := masker.Mask(line.Text)
				read.Lines = append(read.Lines, clipText(line.Stream+" "+text, 2000))
			}
			_ = closer.Close()
		}
		out = append(out, read)
	}
	if len(out) == 0 {
		return refuse("no live container matched")
	}
	total := 0
	for _, c := range out {
		total += len(c.Lines)
	}
	return answer(map[string]any{"containers": out}, operatorCount(total, "line")+" from "+operatorCount(len(out), "container"))
}

// operatorCount is a count as a read's few words say it: "1 run", "12 runs".
func operatorCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// operatorPorts are the ports a visitor reaches the deployment on: the
// ingress's two when it has an address, and the one it publishes itself.
func operatorPorts(summary *deploy.DeploymentSummary) []int {
	ports := []int{}
	if summary.Endpoint != "" {
		ports = append(ports, 80, 443)
	}
	if summary.HostPort > 0 && !slices.Contains(ports, summary.HostPort) {
		ports = append(ports, summary.HostPort)
	}
	return ports
}

// mentionsPort reads whether a rule, as its tool prints it, names one of the
// ports: a bare number bounded by anything that is not a digit.
func mentionsPort(rule string, ports []int) bool {
	for _, port := range ports {
		needle := strconv.Itoa(port)
		for index := strings.Index(rule, needle); index >= 0; {
			before := index == 0 || rule[index-1] < '0' || rule[index-1] > '9'
			end := index + len(needle)
			after := end >= len(rule) || rule[end] < '0' || rule[end] > '9'
			if before && after {
				return true
			}
			next := strings.Index(rule[end:], needle)
			if next < 0 {
				break
			}
			index = end + next
		}
	}
	return false
}

// operatorProposal checks a proposal against the environment as it is now —
// a release to roll back to, a warm one kept, a verdict to apply — and names
// the route its card's press calls. A proposal that cannot be carried out is
// answered with why, and no card is drawn.
func (s *Server) operatorProposal(ctx context.Context, p *httpx.Principal, summary *deploy.DeploymentSummary, call assistant.ToolCall) (*operatorProposal, string) {
	in := call.OperatorInput()
	out := &operatorProposal{ID: call.ID, Action: in.Action, Reason: in.Reason, Capability: string(auth.CapServiceControl)}
	env := summary.EnvironmentName
	switch in.Action {
	case assistant.ActionRestart, assistant.ActionRedeploy:
		if summary.LiveReleaseID <= 0 {
			return nil, "There is no live release to " + in.Action + "."
		}
		if summary.ActiveRun != nil {
			return nil, fmt.Sprintf("Run #%d is in progress; nothing else is proposed until it finishes.", summary.ActiveRun.RunNumber)
		}
		out.Label = "Redeploy the live release of " + env
		if in.Action == assistant.ActionRestart {
			out.Label, out.Capability, out.Destructive = "Restart "+env, string(auth.CapDestructive), true
		}
	case assistant.ActionRollback:
		releases, err := s.modules.deployRuns.EnvironmentReleases(ctx, summary.ID, summary.EnvironmentID, 50)
		if err != nil {
			return nil, "The releases could not be read."
		}
		for _, release := range releases {
			if release.Number == in.Release {
				if release.State != "retained" {
					return nil, fmt.Sprintf("Release #%d is %s, not a retained release a rollback can switch to.", in.Release, release.State)
				}
				out.ReleaseID, out.ReleaseNumber = release.ID, release.Number
			}
		}
		if out.ReleaseID == 0 {
			return nil, fmt.Sprintf("%s has no release #%d.", env, in.Release)
		}
		out.Label = fmt.Sprintf("Roll %s back to release #%d", env, in.Release)
		out.Capability, out.Destructive = string(auth.CapDestructive), true
	case assistant.ActionInstantRollback:
		watch, err := s.modules.deployRuns.OpenRollbackWatch(ctx, summary.EnvironmentID)
		if err != nil || watch.State != deploy.RollbackWatchWatching || watch.RollbackRunID != 0 {
			return nil, "No release is kept warm for this environment, so there is nothing to switch back to instantly; propose a rollback to a release number instead."
		}
		out.ReleaseID, out.ReleaseNumber = watch.WarmReleaseID, watch.WarmReleaseNum
		out.Label = fmt.Sprintf("Switch %s back to warm release #%d", env, watch.WarmReleaseNum)
		out.Capability, out.Destructive = string(auth.CapDestructive), true
	case assistant.ActionApplySizing:
		targets, err := s.modules.deployPlanning.SizingTargets(ctx, summary.ID, summary.EnvironmentID)
		if err != nil || len(targets) == 0 {
			return nil, "The environment's sizing could not be read."
		}
		sizing, err := s.sizeEnvironment(ctx, targets[0], time.Now(), s.hostCPUs(ctx))
		if err != nil || sizing.Apply == nil {
			return nil, "The right-sizing verdict has no limits to suggest for this environment now."
		}
		out.Label = "Apply the suggested limits to " + env
		out.Capability = string(auth.CapSystemAdmin)
	case assistant.ActionMaintenanceOn, assistant.ActionMaintenanceOff:
		if summary.Endpoint == "" {
			return nil, "This environment has no route, so there is no maintenance page to switch."
		}
		out.Label = "Turn maintenance on for " + env
		if in.Action == assistant.ActionMaintenanceOff {
			out.Label = "Turn maintenance off for " + env
		}
		out.Capability, out.Destructive = string(auth.CapDestructive), true
	default:
		return nil, "That action is not one this dashboard offers."
	}
	if !p.Can(auth.Capability(out.Capability)) {
		return nil, fmt.Sprintf("The person's role cannot %s (%s), so it is not proposed; say what someone who can should do.", strings.ToLower(out.Label), out.Capability)
	}
	return out, ""
}
