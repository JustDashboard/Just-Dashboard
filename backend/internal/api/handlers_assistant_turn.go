package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/mcp"
)

// The dashboard's assistant acting on a deployment, or on a draft of one. A press
// asks a question, or answers the calls the last answer is waiting on, and the
// answer streams back as one JSON object a line while the assistant works: it reads,
// changes and reads back through tools, as many steps as the question takes, up to
// agentSteps a press.
//
// What it may do without asking is the person's mode, decided here on every press
// (agentHolds), and it can reach nothing a route would refuse them. A change is a
// request through the dashboard's own router as the person, held to everything a
// deploy key is (s.agentDispatch), so the route's capability check, audit entry and
// rate limit all apply. There is no shell, no command the model writes, and no other
// path from an answer to the host.

const (
	maxOperatorQuestion = 4000
	// maxAgentDescribe bounds one operation's description; a body's schema can be
	// long, and the model cannot use one cut in half.
	maxAgentDescribe = 60_000
	// agentSteps bounds how many answers one press takes. Past it, what the
	// assistant asks next waits for the person whatever the mode, so a loop spends
	// nothing more without somebody looking.
	agentSteps = 16
	// agentTurnDeadline is one press's whole deadline, long enough to see a deploy
	// through.
	agentTurnDeadline = 20 * time.Minute
	// agentCallDeadline is one call's, which a route that does its work in the
	// background answers long before.
	agentCallDeadline = 2 * time.Minute
	// agentOutputBytes bounds what of an answer the page is sent to show under its
	// call. The model gets the whole of it, up to maxOperatorResult.
	agentOutputBytes = 4000
	// agentDiffLines bounds how much of a change is spelled out.
	agentDiffLines = 12
	// agentBudgetWait bounds how long a press waits for the install's budget of
	// model calls before it is refused.
	agentBudgetWait = 20 * time.Second
	// agentAnswerBytes is the most of a route's answer read to mask it. An answer has
	// to be whole to be masked: cut, it is no longer JSON and a value in it no longer
	// matches, so one past this is refused rather than sent in part.
	agentAnswerBytes = 8 << 20
)

type operatorRequest struct {
	// Conversation carries one on; empty begins one.
	Conversation string `json:"conversation"`
	// Question is asked; Decisions, instead, answer the calls the last answer is
	// waiting on — true runs one, false declines it, and a call left out is
	// declined.
	Question  string          `json:"question"`
	Decisions map[string]bool `json:"decisions"`
	// Mode is how much may run without asking on this press; left out, the
	// install's default.
	Mode assistant.Mode `json:"mode"`
}

// agentScope is what a conversation is about.
type agentScope struct {
	kind       agentScopeKind
	deployment *deploy.DeploymentSummary
	draft      *deploy.Draft
	name       string
	// key tells one conversation's subject from another's in its store.
	key int64
	// action is the audit entry's name for a question; ".tool" is its answers.
	action string
}

// agentCallView is a call as the page draws it, running or waiting.
type agentCallView struct {
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Operation string `json:"operation,omitempty"`
	// Route is the route it calls, which the page draws its mark from.
	Route string `json:"route,omitempty"`
	Label string `json:"label"`
	// Kind is free, read, change or destructive.
	Kind   string          `json:"kind"`
	Reason string          `json:"reason,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
}

// agentChange is what a change did, for the page's shortcut to it.
type agentChange struct {
	Label string `json:"label"`
	Href  string `json:"href"`
	// Diff spells out a configuration's change.
	Diff []agentDiffLine `json:"diff,omitempty"`
}

type agentDiffLine struct {
	Path string `json:"path"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type agentLink struct {
	Label string `json:"label"`
	Href  string `json:"href"`
	Note  string `json:"note,omitempty"`
}

// agentResult is what one call came to.
type agentResult struct {
	text   string
	said   string
	ok     bool
	output string
	change *agentChange
	link   *agentLink
}

// handleDeploymentOperator asks the assistant about one environment
// (`?environment=` names staging), or answers the calls it is waiting on.
func (s *Server) handleDeploymentOperator(w http.ResponseWriter, r *http.Request) error {
	summary, err := s.deploymentTarget(r)
	if err != nil {
		return err
	}
	return s.agentTurn(w, r, &agentScope{kind: agentScopeDeployment, deployment: summary, name: summary.Name,
		key: summary.EnvironmentID, action: "deploy.assistant"})
}

// handleDraftOperator asks the assistant to configure a draft.
func (s *Server) handleDraftOperator(w http.ResponseWriter, r *http.Request) error {
	draft, err := s.deploymentDraftForPrincipal(r)
	if err != nil {
		return err
	}
	name := draft.ID
	if draft.Data.Intent != nil && draft.Data.Intent.Name != "" {
		name = draft.Data.Intent.Name
	}
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(draft.ID))
	return s.agentTurn(w, r, &agentScope{kind: agentScopeDraft, draft: draft, name: name,
		key: int64(sum.Sum64() >> 1), action: "deploy.draft.assistant"})
}

func (s *Server) agentTurn(w http.ResponseWriter, r *http.Request, scope *agentScope) error {
	var req operatorRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	req.Question = strings.TrimSpace(req.Question)
	if (req.Question == "") == (req.Decisions == nil) {
		return httpx.BadRequest("ask a question, or answer the calls the assistant is waiting on")
	}
	if len(req.Question) > maxOperatorQuestion || !utf8.ValidString(req.Question) {
		return httpx.BadRequest("a question is at most %d characters", maxOperatorQuestion)
	}
	if req.Mode != "" && !req.Mode.Valid() {
		return httpx.BadRequest("mode is ask, changes or full")
	}
	action := scope.action + ".ask"
	if req.Decisions != nil {
		action = scope.action + ".tool"
	}
	httpx.SetAudit(r, action, scope.name, nil)
	ring, err := s.assistantRing(r.Context())
	if err != nil {
		return err
	}
	settings := ring.Current()
	if settings == nil {
		return httpx.Err(http.StatusConflict, "assistant_off",
			"the assistant is not set up: an administrator chooses its provider in the assistant's settings")
	}
	mode := req.Mode
	if mode == "" {
		mode = ring.Mode()
	}
	// The steps of the last press may have spent the budget a moment ago, and a
	// person's answer refused for that would strand what is waiting on it. It waits a
	// little, as a step does, and only a budget that stays spent is a refusal.
	budget, stopWaiting := context.WithTimeout(r.Context(), agentBudgetWait)
	defer stopWaiting()
	if err := s.agentBudget(budget); err != nil {
		if r.Context().Err() != nil {
			return nil
		}
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
		c = store.Begin(owner, scope.key)
		s.agentBegin(r.Context(), c, scope)
		c.Tools = client.Tools()
		c.Revision = settings.Revision()
	} else {
		c, err = store.Take(req.Conversation, owner, scope.key)
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
		"provider": settings.Provider, "model": settings.Model, "conversation": c.ID[:8], "turn": c.Turns + 1, "mode": mode,
	}
	if scope.deployment != nil {
		detail["deploymentId"], detail["environmentId"] = scope.deployment.ID, scope.deployment.EnvironmentID
	} else {
		detail["draft"] = scope.draft.ID
	}
	stream := &assistantStream{w: w, start: map[string]any{
		"type": "start", "conversation": c.ID, "provider": settings.Provider, "model": settings.Model,
		"modelLabel": assistant.ModelLabel(settings.Provider, settings.Model), "tools": c.Tools, "mode": mode,
	}}
	allowed, declined := []string{}, []string{}
	if req.Question != "" {
		if err := c.Ask(req.Question); err != nil {
			return httpx.Err(http.StatusConflict, "conversation_full", err.Error())
		}
	} else {
		if len(c.Pending) == 0 {
			return httpx.Err(http.StatusConflict, "nothing_pending", "the assistant is not waiting for an answer")
		}
		results := assistant.ToolResults(c.Pending, func(call assistant.ToolCall) (string, bool) {
			view := s.agentView(scope, call)
			if !req.Decisions[call.ID] {
				declined = append(declined, agentName(call))
				stream.send(map[string]any{"type": "called", "id": call.ID, "tool": call.Name, "declined": true, "ok": false})
				return assistant.OperatorDeclined, false
			}
			allowed = append(allowed, agentName(call))
			stream.send(agentCallLine(view))
			result := s.agentExecute(r.Context(), r, principal, scope, call)
			stream.send(agentCalledLine(call, result))
			if result.link != nil {
				stream.send(map[string]any{"type": "link", "link": result.link})
			}
			return result.text, result.ok
		})
		c.Answer(results)
		// What ran has run, so a turn that fails after this goes back to here and
		// not to before it: the conversation says what is true of the server.
		mark = c.Mark()
		detail["allowed"], detail["declined"] = allowed, declined
	}

	ctx, cancel := context.WithTimeout(r.Context(), agentTurnDeadline)
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
	ran, refused := []string{}, []string{}
	var pending []agentCallView
	paused, long := false, false
	for step := 1; ; step++ {
		// Each step is a model call on the install's budget; the first was paid for
		// above. Waiting here is slower, and a person is watching it work.
		if step > 1 {
			if err = s.agentBudget(ctx); err != nil {
				break
			}
		}
		// A press can outlast the session that made it, or the role it was made
		// under, and it goes no further than they do.
		if _, err = s.Authn.Recheck(r, principal); err != nil {
			break
		}
		outcome, err = assistant.Exchange(ctx, client, c, emit)
		usage = addUsage(usage, outcome.Usage)
		if err != nil || !outcome.Kept || len(outcome.Pending) == 0 {
			break
		}
		var held []assistant.ToolCall
		var answers []assistant.Block
		for _, call := range outcome.Pending {
			view := s.agentView(scope, call)
			if agentHolds(scope, mode, view.Kind) || step >= agentSteps {
				paused = paused || step >= agentSteps
				held = append(held, call)
				pending = append(pending, view)
				continue
			}
			stream.send(agentCallLine(view))
			result := s.agentExecute(ctx, r, principal, scope, call)
			stream.send(agentCalledLine(call, result))
			if result.link != nil {
				stream.send(map[string]any{"type": "link", "link": result.link})
			}
			if view.Kind != agentKindFree {
				if result.ok {
					ran = append(ran, agentName(call))
				} else {
					refused = append(refused, agentName(call))
				}
			}
			answers = append(answers, assistant.Block{Type: assistant.BlockToolResult, ToolID: call.ID, Text: result.text, IsError: !result.ok})
		}
		if len(held) > 0 {
			c.Pending, c.Answered = held, answers
			break
		}
		c.Pending = nil
		c.Answer(answers)
		mark = c.Mark()
		if long = c.Long(); long {
			break
		}
	}
	detail["usage"] = usage
	if len(ran) > 0 {
		detail["ran"] = ran
	}
	if len(refused) > 0 {
		detail["refused"] = refused
	}
	if long && err == nil {
		detail["outcome"] = "too_long"
		httpx.SetAudit(r, action, scope.name, detail)
		stream.send(map[string]any{"type": "error", "error": map[string]any{"code": "conversation_full", "message": assistant.ErrFull.Error()}})
		return nil
	}
	if outcome.Model != "" && outcome.Model != settings.Model {
		detail["answeredBy"] = outcome.Model
	}
	if err != nil {
		c.Restore(mark)
		message, failure := assistantFailure(r.Context(), ctx, err)
		detail["outcome"] = failure
		httpx.SetAudit(r, action, scope.name, detail)
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
	httpx.SetAudit(r, action, scope.name, detail)
	done := map[string]any{"type": "done", "stop": outcome.Stop, "usage": usage, "model": outcome.Model, "kept": outcome.Kept}
	if len(pending) > 0 {
		done["pending"] = pending
	}
	if paused {
		done["paused"] = true
	}
	stream.send(done)
	return nil
}

func addUsage(a, b assistant.Usage) assistant.Usage {
	return assistant.Usage{Input: a.Input + b.Input, Output: a.Output + b.Output,
		CacheRead: a.CacheRead + b.CacheRead, CacheWrite: a.CacheWrite + b.CacheWrite}
}

// agentBudget waits for the install's budget of model calls: the limiter is a
// bucket, so a step past it is a few seconds off rather than a refusal halfway
// through what the person asked for.
func (s *Server) agentBudget(ctx context.Context) error {
	for !s.assistant.limit.Allow("install") {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1500 * time.Millisecond):
		}
	}
	return nil
}

// agentBegin writes what a conversation begins knowing.
func (s *Server) agentBegin(ctx context.Context, c *assistant.Conversation, scope *agentScope) {
	cat := s.agentCatalogue()
	c.Check, c.TurnLimit = assistant.AgentCalls, assistant.AgentTurnLimit
	if scope.kind == agentScopeDeployment {
		c.Offer = assistant.AgentTools
		c.System = assistant.AgentSystemPrompt(s.operatorContext(ctx, scope.deployment), cat.lines(agentScopeDeployment))
		return
	}
	c.Offer = assistant.SetupTools
	c.System = assistant.SetupSystemPrompt(s.draftContext(ctx, scope.draft), cat.lines(agentScopeDraft))
}

// draftContext is what a draft's conversation begins knowing. What the dashboard
// read of the source can quote it, and goes through the masker's shapes of keys.
func (s *Server) draftContext(ctx context.Context, draft *deploy.Draft) assistant.DraftContext {
	out := assistant.DraftContext{}
	if draft.Data.Intent != nil {
		out.Name = draft.Data.Intent.Name
	}
	if s.modules.sys != nil {
		if info, err := s.modules.sys.Host(ctx); err == nil && info != nil {
			out.Server = info.Hostname
		}
	}
	if source := draft.Data.Source; source != nil {
		out.Source = strings.TrimSpace(strings.Join([]string{string(source.Kind), source.URL, source.Ref, source.Image, source.Subdirectory}, " "))
	}
	if draft.Data.Detection != nil {
		if raw, err := json.Marshal(draft.Data.Detection); err == nil {
			masked, _ := deploy.NewDiagnosisMasker(nil).Mask(string(raw))
			out.Detection = clipText(masked, 12_000)
		}
	}
	return out
}

// agentName is a call as an audit entry names it.
func agentName(call assistant.ToolCall) string {
	if call.Name == assistant.ToolCallOperation {
		return call.AgentInput().Name
	}
	return call.Name
}

// agentHolds is the person's mode: whether a call of a kind waits for their answer.
// A draft runs nothing until they press Deploy, so nothing done to it needs their
// leave. What the whole server shares waits in every mode: full access is about the
// deployment the conversation is about.
func agentHolds(scope *agentScope, mode assistant.Mode, kind string) bool {
	if kind == agentKindServer {
		return true
	}
	if kind == agentKindFree || scope.kind == agentScopeDraft {
		return false
	}
	switch mode {
	case assistant.ModeFull:
		return false
	case assistant.ModeChanges:
		return kind != agentKindRead
	}
	return true
}

// agentOperationFor finds an operation a conversation may call.
func (s *Server) agentOperationFor(scope *agentScope, name string) (agentOperation, bool) {
	cat := s.agentCatalogue()
	if cat.err != nil {
		return agentOperation{}, false
	}
	op, ok := cat.byName[name]
	if !ok || op.Scope != scope.kind {
		return agentOperation{}, false
	}
	return *op, true
}

// agentView is a call as the page draws it, and the kind its mode reads.
func (s *Server) agentView(scope *agentScope, call assistant.ToolCall) agentCallView {
	view := agentCallView{ID: call.ID, Tool: call.Name, Kind: agentKindRead, Label: operatorReadLabel(call)}
	switch call.Name {
	case assistant.ToolDescribeOperation:
		view.Kind, view.Operation = agentKindFree, call.AgentInput().Name
		view.Label = "Look up " + view.Operation
		if op, ok := s.agentOperationFor(scope, view.Operation); ok {
			view.Label = "Look up: " + strings.ToLower(op.Tool.Title)
		}
	case assistant.ToolOpenPage:
		view.Kind = agentKindFree
		view.Label = "Offer a shortcut"
	case assistant.ToolWaitForRun:
		view.Label = fmt.Sprintf("Follow run #%d", call.AgentInput().Run)
	case assistant.ToolCallOperation:
		in := call.AgentInput()
		view.Operation, view.Reason = in.Name, in.Reason
		view.Input = in.Arguments
		op, ok := s.agentOperationFor(scope, in.Name)
		if !ok {
			view.Kind, view.Label = agentKindFree, "Call "+in.Name
			break
		}
		view.Kind, view.Label = op.Kind(), op.Tool.Title
		view.Route = op.Method + " " + op.Pattern
	default:
		view.Input = call.Input
	}
	return view
}

func agentCallLine(view agentCallView) map[string]any {
	return map[string]any{"type": "call", "call": view}
}

func agentCalledLine(call assistant.ToolCall, result agentResult) map[string]any {
	line := map[string]any{"type": "called", "id": call.ID, "tool": call.Name, "ok": result.ok, "said": result.said}
	if result.output != "" {
		line["output"] = result.output
	}
	if result.change != nil {
		line["change"] = result.change
	}
	return line
}

// agentExecute runs one call the person's mode, or the person, has let through.
func (s *Server) agentExecute(ctx context.Context, r *http.Request, p *httpx.Principal, scope *agentScope, call assistant.ToolCall) agentResult {
	ctx, cancel := context.WithTimeout(ctx, agentCallDeadline+time.Duration(assistant.AgentWaitMax)*time.Second)
	defer cancel()
	refuse := func(why string) agentResult { return agentResult{text: why, said: why} }
	// Asked again for each call, so a role taken away during a press takes effect.
	p, err := s.Authn.Recheck(r, p)
	if err != nil {
		return refuse("The session this was asked under has ended, so nothing was done.")
	}
	switch call.Name {
	case assistant.ToolDescribeOperation:
		return s.agentDescribe(scope, call.AgentInput().Name)
	case assistant.ToolCallOperation:
		return s.agentCall(ctx, r, p, scope, call.AgentInput())
	case assistant.ToolOpenPage:
		return s.agentOpenPage(scope, call.AgentInput())
	case assistant.ToolWaitForRun:
		if scope.deployment == nil {
			return refuse("a draft has no runs")
		}
		return s.agentWaitForRun(ctx, scope.deployment, call.AgentInput())
	}
	if scope.deployment == nil {
		return refuse("that read is about a deployment, and this one does not exist yet")
	}
	readCtx, done := context.WithTimeout(ctx, 60*time.Second)
	defer done()
	text, said, ok := s.operatorRead(readCtx, r, p, scope.deployment, call)
	return agentResult{text: text, said: said, ok: ok}
}

// agentDescribe is one operation's parameters.
func (s *Server) agentDescribe(scope *agentScope, name string) agentResult {
	op, ok := s.agentOperationFor(scope, name)
	if !ok {
		return agentResult{text: "There is no operation called " + name + " in your list.", said: "no such operation"}
	}
	raw, err := json.Marshal(map[string]any{
		"name": op.Tool.Name, "description": op.Tool.Description, "kind": op.Kind(),
		"route": op.Method + " " + op.Pattern, "inputSchema": op.Tool.InputSchema,
	})
	if err != nil {
		return agentResult{text: "The description could not be written out.", said: "could not be written out"}
	}
	text := string(raw)
	if len(text) > maxAgentDescribe {
		text = clipText(text, maxAgentDescribe) + " (cut: this schema is longer than can be sent; read the current value with the matching read and send back what you read, changed)"
	}
	return agentResult{text: text, said: op.Tool.Title, ok: true}
}

// agentOpenPage is a shortcut for the page to draw.
func (s *Server) agentOpenPage(scope *agentScope, in assistant.AgentInput) agentResult {
	page, ok := agentOpenablePages[in.Page]
	if !ok || scope.deployment == nil {
		return agentResult{text: "That page cannot be offered here.", said: "no such page"}
	}
	link := &agentLink{Label: page.Label, Href: agentHref(scope, page), Note: in.Note}
	return agentResult{text: "The person has been shown a shortcut to " + page.Label + ".", said: page.Label, ok: true, link: link}
}

// agentHref is a page's address for a conversation.
func agentHref(scope *agentScope, page agentPage) string {
	href := page.Path
	if !page.Absolute && scope.deployment != nil {
		href = "/deploy/" + strconv.FormatInt(scope.deployment.ID, 10) + page.Path
	}
	if page.Section != "" {
		href += "#" + page.Section
	}
	return href
}

// agentWaitForRun waits for a run to end, or for the seconds asked.
func (s *Server) agentWaitForRun(ctx context.Context, summary *deploy.DeploymentSummary, in assistant.AgentInput) agentResult {
	run, err := s.modules.deployRuns.ProjectRunByNumber(ctx, summary.ID, in.Run)
	if err != nil {
		return agentResult{text: fmt.Sprintf("%s has no run #%d.", summary.Name, in.Run), said: "no such run"}
	}
	seconds := in.Seconds
	if seconds <= 0 {
		seconds = assistant.AgentWaitDefault
	}
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for {
		snapshot, err := s.modules.deployRuns.Snapshot(ctx, run.ID)
		if err != nil {
			return agentResult{text: "The run could not be read.", said: "could not be read"}
		}
		if snapshot.Run.State.Terminal() || time.Now().After(deadline) {
			masker, err := s.operatorMasker(ctx, summary)
			if err != nil {
				return agentResult{text: "The run's secret values could not be read to mask, so nothing was read.", said: "could not be masked"}
			}
			view := mcpRunOf(&snapshot.Run)
			view.Reason, _ = masker.Mask(view.Reason)
			finished := snapshot.Run.State.Terminal()
			raw, _ := json.Marshal(map[string]any{"finished": finished, "run": view})
			said := string(snapshot.Run.State)
			return agentResult{text: string(raw), said: said, ok: true}
		}
		select {
		case <-ctx.Done():
			return agentResult{text: "Waiting for the run was stopped.", said: "stopped"}
		case <-time.After(2 * time.Second):
		}
	}
}

// agentDelegate is the person as the router sees the assistant: their user and
// role, held to everything a deploy key is, and narrowed to the deployment the
// conversation is about. A context value is set only by code in this process, so
// no request from the network carries one.
func agentDelegate(p *httpx.Principal, scope *agentScope) *httpx.Principal {
	token := &auth.APIToken{Name: "Assistant", Scope: auth.ScopeMCP, Role: p.Role, Deployments: auth.DeployRun, Operations: true}
	if scope.deployment != nil {
		token.DeploymentIDs = []int64{scope.deployment.ID}
	}
	delegate := *p
	delegate.Token, delegate.TokenID, delegate.Kind, delegate.Scope = token, 0, "token", auth.ScopeMCP
	delegate.Assistant = true
	return &delegate
}

// agentRequest fills in what a conversation already knows: the deployment and, by
// default, the environment, and the draft. The model naming another deployment's
// id is refused here, and would be by the delegate's narrowing as well.
func agentArguments(scope *agentScope, op agentOperation, raw json.RawMessage) (json.RawMessage, error) {
	args := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(raw)) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, errors.New("arguments must be an object")
		}
	}
	set := func(name string, value any) {
		encoded, _ := json.Marshal(value)
		args[name] = encoded
	}
	for _, param := range op.Path {
		given, named := args[param.Name]
		switch {
		case param.Name == "id" && scope.deployment != nil && strings.HasPrefix(op.Pattern, "/deploy/{id}"):
			if named {
				var id int64
				if json.Unmarshal(given, &id) != nil || id != scope.deployment.ID {
					var text string
					if json.Unmarshal(given, &text) != nil || text != strconv.FormatInt(scope.deployment.ID, 10) {
						return nil, errors.New("this conversation is about one deployment, and that id is another's")
					}
				}
			}
			set("id", scope.deployment.ID)
		case param.Name == "env" && scope.deployment != nil && !named:
			set("env", scope.deployment.EnvironmentID)
		case param.Name == "draft" && scope.draft != nil:
			set("draft", scope.draft.ID)
		}
	}
	return json.Marshal(args)
}

// agentDispatch makes one route's request as the assistant.
func (s *Server) agentDispatch(ctx context.Context, r *http.Request, p *httpx.Principal, scope *agentScope, method string, req mcp.RouteRequest) (*boundedRecorder, error) {
	return s.dispatchKeeping(r.WithContext(ctx), agentDelegate(p, scope), method, req, agentAnswerBytes)
}

// agentCall runs one operation, through the router, as the person.
func (s *Server) agentCall(ctx context.Context, r *http.Request, p *httpx.Principal, scope *agentScope, in assistant.AgentInput) agentResult {
	refuse := func(why string) agentResult { return agentResult{text: why, said: why} }
	op, ok := s.agentOperationFor(scope, in.Name)
	if !ok {
		return refuse("There is no operation called " + in.Name + " in your list.")
	}
	raw, err := agentArguments(scope, op, in.Arguments)
	if err != nil {
		return refuse(err.Error())
	}
	req, err := mcp.DecodeRequest(op.Operation, raw)
	if err != nil {
		return refuse(err.Error())
	}
	masker := deploy.NewDiagnosisMasker(nil)
	if scope.deployment != nil {
		masker, err = s.operatorMasker(ctx, scope.deployment)
		if err != nil {
			return refuse("The environment's secret values could not be read to mask, so nothing was read or changed.")
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, agentCallDeadline)
	defer cancel()
	// A configuration's change is spelled out, which a read each side of it gives.
	before := s.agentConfiguration(callCtx, r, p, scope, op, req)
	rec, err := s.agentDispatch(callCtx, r, p, scope, op.Method, req)
	if err != nil {
		return refuse("The route could not be called.")
	}
	if rec.status >= http.StatusBadRequest {
		return agentRefusal(rec, masker)
	}
	text, whole := agentAnswer(rec, masker)
	if op.Grant == mcp.GrantRead {
		if !whole {
			return refuse(text)
		}
		return agentResult{text: text, ok: true, said: agentReadWords(rec), output: clipText(text, agentOutputBytes)}
	}
	// A change has been made whatever its answer came to, and the model has to know.
	if !whole {
		text = fmt.Sprintf(`{"status":%d,"done":true}`, rec.status)
	}
	result := agentResult{text: text, ok: true, output: clipText(text, agentOutputBytes)}
	result.change = s.agentChangeOf(callCtx, r, p, scope, op, req, rec, before, masker)
	result.said = "Done"
	return result
}

// agentRefusal is a route's refusal, which the model reads and may act on.
func agentRefusal(rec *boundedRecorder, masker *deploy.DiagnosisMasker) agentResult {
	var envelope struct {
		Error *httpx.APIError `json:"error"`
	}
	_ = json.Unmarshal(rec.body.Bytes(), &envelope)
	message := http.StatusText(rec.status)
	if envelope.Error != nil && envelope.Error.Message != "" {
		message = envelope.Error.Message
	}
	if rec.status >= http.StatusInternalServerError {
		message = "The dashboard could not do this: " + message
	}
	message, _ = masker.Mask(message)
	return agentResult{text: message, said: message}
}

// agentAnswer is a route's JSON as the model reads it, and whether it could be read:
// every secret value of the deployment masked in every string of it, the fields that
// name a secret withheld whatever a route ever answers, and only then cut at the
// read's bound. An answer that cannot be parsed whole cannot be masked, so none of it
// is sent, and the text says why.
func agentAnswer(rec *boundedRecorder, masker *deploy.DiagnosisMasker) (string, bool) {
	body := bytes.TrimSpace(rec.body.Bytes())
	if len(body) == 0 {
		return fmt.Sprintf(`{"status":%d,"done":true}`, rec.status), true
	}
	if rec.total > rec.body.Len() {
		return fmt.Sprintf("The answer is %d bytes, too long to mask whole, so none of it was read. Ask for less with the operation's parameters.", rec.total), false
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return "The answer could not be read to mask it, so none of it was read.", false
	}
	raw, err := json.Marshal(agentMaskValue(value, masker))
	if err != nil {
		return "The answer could not be written out.", false
	}
	text := string(raw)
	if len(text) > maxOperatorResult {
		text = clipText(text, maxOperatorResult) + fmt.Sprintf(" (cut: the answer is %d bytes; ask for less with the operation's parameters)", len(text))
	}
	return text, true
}

// agentSecretFields are names of a field no answer's value is sent to a model
// under. No route a deploy key reaches answers with one, by its rules; this is the
// second lock on it.
var agentSecretFields = map[string]bool{
	"secret": true, "password": true, "token": true, "apikey": true, "api_key": true,
	"privatekey": true, "private_key": true, "accesskey": true, "secretkey": true, "secretaccesskey": true,
	"webhooksecret": true, "clientsecret": true, "plaintext": true,
}

func agentMaskValue(value any, masker *deploy.DiagnosisMasker) any {
	switch v := value.(type) {
	case string:
		masked, _ := masker.Mask(v)
		return masked
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = agentMaskValue(item, masker)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if _, isString := item.(string); isString && agentSecretFields[strings.ToLower(key)] {
				out[key] = deploy.DiagnosisMasked
				continue
			}
			out[key] = agentMaskValue(item, masker)
		}
		return out
	}
	return value
}

// agentReadWords says a read's answer in a few words: how many of what a list came
// to, for the page's line under the call.
func agentReadWords(rec *boundedRecorder) string {
	var list []json.RawMessage
	if json.Unmarshal(bytes.TrimSpace(rec.body.Bytes()), &list) == nil {
		return operatorCount(len(list), "item")
	}
	return "read"
}

// agentConfiguration reads an environment's configuration before a call that
// changes it, for the change to be spelled out. Nil for any other call.
func (s *Server) agentConfiguration(ctx context.Context, r *http.Request, p *httpx.Principal, scope *agentScope, op agentOperation, req mcp.RouteRequest) map[string]any {
	if op.Grant == mcp.GrantRead || !strings.HasSuffix(op.Pattern, "/environments/{env}/configuration") {
		return nil
	}
	rec, err := s.agentDispatch(ctx, r, p, scope, http.MethodGet, mcp.RouteRequest{Path: req.Path, Query: url.Values{}})
	if err != nil || rec.status != http.StatusOK {
		return nil
	}
	var value map[string]any
	if json.Unmarshal(rec.body.Bytes(), &value) != nil {
		return nil
	}
	return value
}

// agentChangeOf says what a change did and where to see it. A run says which; a
// configuration says what is different, and points at the page that part is set on.
func (s *Server) agentChangeOf(ctx context.Context, r *http.Request, p *httpx.Principal, scope *agentScope, op agentOperation, req mcp.RouteRequest, rec *boundedRecorder, before map[string]any, masker *deploy.DiagnosisMasker) *agentChange {
	if scope.draft != nil {
		// A detection or a preflight changes the draft's revision and nothing the
		// person chose; only a save is a change to show them.
		if op.Method != http.MethodPut {
			return nil
		}
		return &agentChange{Label: "the plan", Href: "/deploy/new?step=review"}
	}
	change := &agentChange{Label: op.Page.Label, Href: agentHref(scope, op.Page)}
	var answer struct {
		ID        int64 `json:"id"`
		RunNumber int64 `json:"runNumber"`
	}
	if json.Unmarshal(rec.body.Bytes(), &answer) == nil && answer.ID > 0 && answer.RunNumber > 0 && scope.deployment != nil {
		change.Label = fmt.Sprintf("Run #%d", answer.RunNumber)
		change.Href = fmt.Sprintf("/deploy/%d/runs/%d", scope.deployment.ID, answer.ID)
		return change
	}
	if before == nil {
		return change
	}
	after, err := s.agentDispatch(ctx, r, p, scope, http.MethodGet, mcp.RouteRequest{Path: req.Path, Query: url.Values{}})
	if err != nil || after.status != http.StatusOK {
		return change
	}
	var now map[string]any
	if json.Unmarshal(after.body.Bytes(), &now) != nil {
		return change
	}
	lines := []agentDiffLine{}
	jsonDiff("", agentMaskValue(before, masker), agentMaskValue(now, masker), &lines)
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Path < lines[j].Path })
	if len(lines) > 0 {
		if page, ok := agentConfigurationPage(lines[0].Path); ok {
			change.Label, change.Href = page.Label, agentHref(scope, page)
		}
	}
	if len(lines) > agentDiffLines {
		lines = lines[:agentDiffLines]
	}
	change.Diff = lines
	return change
}

// agentDiffIgnored are parts of a configuration that say when, not what.
var agentDiffIgnored = map[string]bool{"revision": true, "pending": true, "detected": true, "identity": true, "updatedAt": true, "digest": true}

// jsonDiff lists where two JSON values differ, each leaf by its path.
func jsonDiff(path string, before, after any, out *[]agentDiffLine) {
	if len(*out) > 200 {
		return
	}
	beforeMap, beforeIsMap := before.(map[string]any)
	afterMap, afterIsMap := after.(map[string]any)
	if beforeIsMap && afterIsMap {
		keys := map[string]bool{}
		for key := range beforeMap {
			keys[key] = true
		}
		for key := range afterMap {
			keys[key] = true
		}
		for key := range keys {
			if agentDiffIgnored[key] {
				continue
			}
			next := key
			if path != "" {
				next = path + "." + key
			}
			jsonDiff(next, beforeMap[key], afterMap[key], out)
		}
		return
	}
	beforeRaw, _ := json.Marshal(before)
	afterRaw, _ := json.Marshal(after)
	if bytes.Equal(beforeRaw, afterRaw) {
		return
	}
	line := agentDiffLine{Path: path}
	if before != nil {
		line.From = clipText(string(beforeRaw), 120)
	}
	if after != nil {
		line.To = clipText(string(afterRaw), 120)
	}
	*out = append(*out, line)
}
