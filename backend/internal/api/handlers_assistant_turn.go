package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"reflect"
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

// The dashboard's assistant acting on a deployment, on a draft of one, or on the
// whole server. A press asks a question, or answers the calls the last answer is
// waiting on, and the answer streams back as one JSON object a line while the
// assistant works: it reads, changes and reads back through tools, as many steps
// as the question takes, up to agentSteps a press.
//
// It acts as the person. Every call is a request through the dashboard's own
// router under their own session principal (s.assistantDispatch, httpx.WithAssistant),
// so the route's capability check, handler rules, audit entry and rate limit apply
// as to them, and the audit entry says the assistant made it. What it may do
// without asking is the person's mode, decided here on every press (agentHolds):
// nothing their role allows is refused, and what the old narrowing refused is asked
// instead, unless the mode is full access. What it reads is masked before the
// provider is sent it (handlers_assistant_mask.go), in every mode.
//
// A conversation is a saved chat: written after every step (saveChat), loaded back
// when it is not in memory, and carried on by one press at a time, which another
// press may take over (assistant.Store.TakeChat).

const (
	maxOperatorQuestion = 4000
	// maxAgentDescribe bounds one operation's description; a body's schema can be
	// long, and the model cannot use one cut in half.
	maxAgentDescribe = 60_000
	// agentSteps bounds how many answers one press takes. Past it, what the
	// assistant asks next waits for the person whatever the mode, so a loop spends
	// nothing more without somebody looking.
	agentSteps = 32
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
	// agentTitleRunes is how much of the first question a chat's title keeps.
	agentTitleRunes = 80
)

type operatorRequest struct {
	// Conversation carries a chat on; empty begins one.
	Conversation string `json:"conversation"`
	// Question is asked; Decisions, instead, answer the calls the last answer is
	// waiting on — true runs one, false declines it, and a call left out is
	// declined.
	Question  string          `json:"question"`
	Decisions map[string]bool `json:"decisions"`
	// Mode is how much may run without asking on this press; left out, the
	// install's default.
	Mode assistant.Mode `json:"mode"`
	// TakeOver carries on a chat a press is running somewhere else: that press
	// stops, and this one goes on with the chat as it stood.
	TakeOver bool `json:"takeOver"`
	// Images are what the person pasted with the question, each with a small
	// copy the saved chat keeps to draw the question again.
	Images []agentImage `json:"images"`
	// Attached is what the page the question was asked on hands over with it at
	// the person's choice — the SQL editor's statement, its last result or error —
	// each sent to the model with the question and drawn on it by its label alone.
	Attached []agentAttachment `json:"attached"`
	// About is the page a chat about the whole server was opened from, read on the
	// press that begins it.
	About *assistant.ChatAbout `json:"about"`
}

// agentAttachment is one thing handed over with a question, by its label.
type agentAttachment struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

// Bounds on what is handed over with a question, and on where a chat was opened.
const (
	maxAgentAttached      = 4
	maxAgentAttachedLabel = 60
	maxAgentAttachedText  = 20_000
	maxAboutPath          = 512
	maxAboutTitle         = 120
)

// agentImage is a pasted image as the page sends it: the image the model reads,
// made small by the page, and a thumbnail for the chat's own record.
type agentImage struct {
	assistant.Image
	Thumb assistant.Image `json:"thumb"`
}

// agentRequestBytes bounds a question's body: a question's words and its
// images, each with its thumbnail, as base64.
const agentRequestBytes = 1<<20 + assistant.MaxImages*((assistant.MaxImageBytes+assistant.MaxThumbBytes)*4/3+1024) +
	maxAgentAttached*(maxAgentAttachedText*6+maxAgentAttachedLabel*6)

// agentScope is what a conversation is about.
type agentScope struct {
	kind       agentScopeKind
	deployment *deploy.DeploymentSummary
	draft      *deploy.Draft
	database   *dbConnection
	// about is the page a server chat was opened from.
	about *assistant.ChatAbout
	name  string
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

	// asks is a call that waits for the person whatever the scope, unless the
	// mode is full access: what the assistant used to be refused. draftOwn is a
	// call to the draft a draft's conversation is about, which otherwise never
	// waits, since a draft runs nothing.
	asks, draftOwn bool
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
	// terminal is the chat's window, for a call of the terminal's.
	terminal *agentTerminalView
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

// handleDatabaseAssistant asks the assistant about one database connection. A
// protected connection never reaches here: the guard in front of every database
// route refuses it, and agentProtected keeps the other scopes' calls out of it.
func (s *Server) handleDatabaseAssistant(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	return s.agentTurn(w, r, &agentScope{kind: agentScopeDatabase, database: conn, name: conn.Name,
		key: conn.ID, action: "database.assistant"})
}

// handleServerAssistant asks the assistant about the whole server.
func (s *Server) handleServerAssistant(w http.ResponseWriter, r *http.Request) error {
	name := "the server"
	if s.modules.sys != nil {
		if info, err := s.modules.sys.Host(r.Context()); err == nil && info != nil && info.Hostname != "" {
			name = info.Hostname
		}
	}
	return s.agentTurn(w, r, &agentScope{kind: agentScopeServer, name: name, action: "assistant.chat"})
}

// chatInfo is what a new chat about this scope says about itself.
func (scope *agentScope) chatInfo(question string, settings *assistant.Settings, now time.Time) *assistant.ChatInfo {
	info := &assistant.ChatInfo{Kind: scope.kind.word(), Name: scope.name, Title: chatTitle(question),
		Provider: string(settings.Provider), Model: settings.Model, Created: now, Updated: now}
	if scope.deployment != nil {
		info.Deployment, info.Environment = scope.deployment.ID, scope.deployment.EnvironmentID
	}
	if scope.draft != nil {
		info.Draft = scope.draft.ID
	}
	if scope.database != nil {
		info.Database = scope.database.ID
	}
	info.About = scope.about
	return info
}

// chatTitle is a chat's title until it is renamed: its first question, on one
// line and cut short.
func chatTitle(question string) string {
	title := strings.Join(strings.Fields(question), " ")
	if utf8.RuneCountInString(title) <= agentTitleRunes {
		return title
	}
	runes := []rune(title)
	cut := string(runes[:agentTitleRunes])
	if space := strings.LastIndex(cut, " "); space > agentTitleRunes/2 {
		cut = cut[:space]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}

// agentPress is one press: who, about what, in which mode, and what the page has
// been sent that is not saved yet.
type agentPress struct {
	s       *Server
	r       *http.Request
	p       *httpx.Principal
	scope   *agentScope
	mode    assistant.Mode
	c       *assistant.Conversation
	stream  *assistantStream
	secrets agentSecrets
	// lines are what the page was sent since the chat was last saved, each with
	// when, a run of text as one line.
	lines []map[string]any
	// images are what the person pasted since then, whole, for the page to open
	// one large; the question's line names each by its id.
	images []chatImage
	quiet  bool
	// commands are the lines run_command ran, masked, for the audit entry.
	commands []string
	// announced is a chat saved once the page has been sent its first line.
	announced bool
}

// keep records a line the page was sent. The text of an answer arrives a piece
// at a time and is kept as one line, which replays the same.
func (pr *agentPress) keep(raw []byte) {
	if pr.quiet {
		return
	}
	var line map[string]any
	if json.Unmarshal(raw, &line) != nil {
		return
	}
	kind, _ := line["type"].(string)
	if n := len(pr.lines); n > 0 {
		last := pr.lines[n-1]
		switch {
		case kind == "text" && last["type"] == "text":
			before, _ := last["text"].(string)
			more, _ := line["text"].(string)
			last["text"] = before + more
			return
		case kind == "thinking" && last["type"] == "thinking":
			return
		}
	}
	line["at"] = time.Now().UTC().Format(time.RFC3339)
	pr.lines = append(pr.lines, line)
}

// save writes the chat as it stands, once the page knows of it.
func (pr *agentPress) save(ctx context.Context) {
	if !pr.stream.started {
		return
	}
	if err := pr.s.saveChat(context.WithoutCancel(ctx), pr.c, pr.lines, pr.images...); err != nil {
		pr.s.Log.Warn("an assistant chat could not be saved", "chat", pr.c.ID[:8], "err", err)
		return
	}
	pr.lines, pr.images = nil, nil
}

func (pr *agentPress) masker(ctx context.Context) (*deploy.DiagnosisMasker, error) {
	return pr.secrets.get(ctx, pr.s)
}

func (s *Server) agentTurn(w http.ResponseWriter, r *http.Request, scope *agentScope) error {
	var req operatorRequest
	if err := httpx.DecodeJSONWithin(r, &req, agentRequestBytes); err != nil {
		return err
	}
	req.Question = strings.TrimSpace(req.Question)
	// An image is a question on its own: a screenshot of an error asks about it.
	asked := req.Question != "" || len(req.Images) > 0
	// Taking a chat over with nothing to say carries it on from where the press
	// that was running it put it down.
	resume := req.TakeOver && req.Conversation != "" && !asked && req.Decisions == nil
	if !resume && asked == (req.Decisions != nil) {
		return httpx.BadRequest("ask a question, or answer the calls the assistant is waiting on")
	}
	if len(req.Question) > maxOperatorQuestion || !utf8.ValidString(req.Question) {
		return httpx.BadRequest("a question is at most %d characters", maxOperatorQuestion)
	}
	if err := checkAgentImages(req.Images); err != nil {
		return err
	}
	if err := checkAgentAttached(req.Attached); err != nil {
		return err
	}
	if len(req.Attached) > 0 && req.Question == "" && len(req.Images) == 0 {
		return httpx.BadRequest("what is attached goes with a question")
	}
	if req.About != nil && scope.kind == agentScopeServer && req.Conversation == "" {
		about, err := checkAgentAbout(*req.About)
		if err != nil {
			return err
		}
		scope.about = about
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
	fresh := req.Conversation == ""
	var c *assistant.Conversation
	if fresh {
		if !asked {
			return httpx.BadRequest("there is no conversation to answer for")
		}
		title := req.Question
		if title == "" && len(req.Images) > 0 {
			title = "A pasted image"
		}
		c = store.BeginChat(principal.Username(), principal.UserID(), scope.key, scope.chatInfo(title, settings, time.Now().UTC()))
		s.agentBegin(r.Context(), c, scope)
		c.Tools = client.Tools()
		c.Revision = settings.Revision()
	} else {
		c, err = store.TakeChat(r.Context(), req.Conversation, principal.UserID(), scope.key, req.TakeOver, s.loadChat)
		switch {
		case errors.Is(err, assistant.ErrGone):
			return httpx.Err(http.StatusNotFound, "conversation_gone", "there is no such chat of yours here: ask again to begin a new one")
		case errors.Is(err, assistant.ErrBusy):
			return httpx.Err(http.StatusConflict, "chat_busy",
				"this chat is answering in another tab or browser; take it over to carry it on here")
		case err != nil:
			if r.Context().Err() != nil {
				return nil
			}
			return httpx.Internal(err)
		}
		if c.Chat == nil || c.Chat.Kind != scope.kind.word() {
			store.Release(c)
			return httpx.Err(http.StatusNotFound, "conversation_gone", "that chat is about something else: ask again to begin a new one")
		}
		if c.Revision != settings.Revision() {
			agentCarryOver(c, settings, client)
		}
	}
	defer store.Release(c)
	pressCtx, stopPress := context.WithCancelCause(r.Context())
	defer stopPress(nil)
	store.Running(c, stopPress)

	press := &agentPress{s: s, r: r, p: principal, scope: scope, mode: mode, c: c}
	stream := &assistantStream{w: w, keep: press.keep, start: map[string]any{
		"type": "start", "conversation": c.ID, "title": c.Chat.Title, "provider": settings.Provider, "model": settings.Model,
		"modelLabel": assistant.ModelLabel(settings.Provider, settings.Model), "tools": c.Tools, "mode": mode,
	}}
	press.stream = stream
	// What the page sent is kept with what it was sent back, for the chat to be
	// read again as it was; the press saves it, and a press the page never heard
	// from saves nothing, and a new chat that never began is forgotten.
	defer func() {
		if !stream.started && fresh {
			c.Chat, c.Messages = nil, nil
			return
		}
		press.save(pressCtx)
	}()
	// Deferred after the save, so it has stopped before the save reads the stream.
	stopKeepAlive := stream.keepAlive(agentStreamQuiet)
	defer stopKeepAlive()
	act := map[string]any{"type": "question", "text": req.Question}
	if len(req.Images) > 0 && !resume {
		// The saved chat draws the question with the thumbnails, and keeps each
		// image whole beside it for the page to open large, by its id.
		thumbs := make([]map[string]any, len(req.Images))
		for i, img := range req.Images {
			kept := chatImage{ID: rand.Text(), Image: img.Image}
			thumbs[i] = map[string]any{"mediaType": img.Thumb.MediaType, "data": img.Thumb.Data, "id": kept.ID}
			if width, height, ok := assistant.ImageSize(img.Image); ok {
				thumbs[i]["width"], thumbs[i]["height"] = width, height
			}
			press.images = append(press.images, kept)
		}
		act["images"] = thumbs
	}
	if len(req.Attached) > 0 && !resume {
		labels := make([]string, len(req.Attached))
		for i, a := range req.Attached {
			labels[i] = a.Label
		}
		act["attached"] = labels
	}
	if req.Decisions != nil {
		act = map[string]any{"type": "decisions", "decisions": req.Decisions}
	}
	if raw, err := json.Marshal(act); err == nil && !resume {
		press.keep(raw)
	}

	mark := c.Mark()
	detail := map[string]any{
		"provider": settings.Provider, "model": settings.Model, "conversation": c.ID[:8], "turn": c.Turns + 1, "mode": mode,
	}
	switch {
	case scope.deployment != nil:
		detail["deploymentId"], detail["environmentId"] = scope.deployment.ID, scope.deployment.EnvironmentID
	case scope.draft != nil:
		detail["draft"] = scope.draft.ID
	case scope.database != nil:
		detail["databaseId"] = scope.database.ID
	default:
		detail["scope"] = "server"
		if scope.about != nil {
			detail["about"] = scope.about.Path
		}
	}
	if req.TakeOver {
		detail["takeOver"] = true
	}
	if len(req.Images) > 0 {
		detail["images"] = len(req.Images)
	}
	allowed, declined := []string{}, []string{}
	var handOver *assistant.Mark
	// settled is a chat taken over that has nothing to carry on: its last answer
	// is whole, or it waits on the person, so no step of the model's is due.
	settled := false
	var pending []agentCallView
	if resume {
		detail["resumed"] = true
		last := len(c.Messages) - 1
		if len(c.Pending) > 0 || last < 0 || c.Messages[last].Role != "user" {
			settled = true
			for _, call := range c.Pending {
				pending = append(pending, press.view(pressCtx, call))
			}
		}
	} else if asked {
		images := make([]assistant.Image, len(req.Images))
		for i, img := range req.Images {
			images[i] = img.Image
		}
		if len(req.Attached) > 0 {
			labels := make([]string, len(req.Attached))
			for i, a := range req.Attached {
				labels[i] = a.Label
			}
			detail["attached"] = labels
		}
		if err := c.Ask(agentQuestion(req.Question, req.Attached), images...); err != nil {
			return httpx.Err(http.StatusConflict, "conversation_full", err.Error())
		}
		// Taken over before a step of it is kept, the chat is put down with the
		// question asked: the page shows it, and the press that takes over answers it.
		asked := c.Mark()
		handOver = &asked
	} else {
		if len(c.Pending) == 0 {
			return httpx.Err(http.StatusConflict, "nothing_pending", "the assistant is not waiting for an answer")
		}
		results := assistant.ToolResults(c.Pending, func(call assistant.ToolCall) (string, bool) {
			view := press.view(pressCtx, call)
			if !req.Decisions[call.ID] {
				declined = append(declined, agentName(call))
				stream.send(map[string]any{"type": "called", "id": call.ID, "tool": call.Name, "declined": true, "ok": false})
				return assistant.OperatorDeclined, false
			}
			allowed = append(allowed, agentName(call))
			stream.send(agentCallLine(view))
			result := press.execute(pressCtx, call, view)
			press.sendCalled(call, view, result)
			return call.NotesText() + result.text, result.ok
		})
		c.Answer(results)
		// What ran has run, so a turn that fails after this goes back to here and
		// not to before it: the conversation says what is true of the server.
		mark = c.Mark()
		press.save(pressCtx)
		detail["allowed"], detail["declined"] = allowed, declined
	}

	ctx, cancel := context.WithTimeout(pressCtx, agentTurnDeadline)
	defer cancel()
	emit := func(e assistant.Event) {
		if e.Thinking {
			stream.send(map[string]any{"type": "thinking"})
		} else {
			stream.send(map[string]any{"type": "text", "text": e.Text})
		}
		// A chat is saved as soon as the page knows of it, so a list shows it
		// running and another tab can take it over.
		if !press.announced {
			press.announced = true
			press.save(ctx)
		}
	}
	var usage assistant.Usage
	var outcome assistant.Outcome
	ran, refused, malformed := []string{}, []string{}, []string{}
	paused, long := false, false
	if settled {
		outcome.Kept = true
	}
	for step := 1; !settled; step++ {
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
			view := press.view(ctx, call)
			// A call that cannot run is answered at once with what was wrong, for the
			// model to correct on its next step. It is the model's slip, not something
			// done on the server, so the page is not shown it: a chat shows what the
			// assistant did, and the audit entry still counts it.
			if call.Problem != "" {
				malformed = append(malformed, call.Name)
				answers = append(answers, assistant.Block{Type: assistant.BlockToolResult, ToolID: call.ID, Text: call.Problem, IsError: true})
				continue
			}
			if agentHolds(scope, mode, view) || step >= agentSteps {
				paused = paused || step >= agentSteps
				held = append(held, call)
				pending = append(pending, view)
				continue
			}
			stream.send(agentCallLine(view))
			result := press.execute(ctx, call, view)
			press.sendCalled(call, view, result)
			if view.Kind != agentKindFree {
				if result.ok {
					ran = append(ran, agentName(call))
				} else {
					refused = append(refused, agentName(call))
				}
			}
			answers = append(answers, assistant.Block{Type: assistant.BlockToolResult, ToolID: call.ID, Text: call.NotesText() + result.text, IsError: !result.ok})
		}
		if len(held) > 0 {
			c.Pending, c.Answered = held, answers
			break
		}
		c.Pending = nil
		c.Answer(answers)
		mark, handOver = c.Mark(), nil
		press.save(ctx)
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
	if len(malformed) > 0 {
		detail["malformed"] = malformed
	}
	if len(press.commands) > 0 {
		detail["commands"] = press.commands
	}
	// Carried on somewhere else: this press puts the chat down as it stood after
	// its last step, and the press that took it goes on from there.
	if errors.Is(context.Cause(pressCtx), assistant.ErrTakenOver) {
		if handOver != nil {
			mark = *handOver
		}
		c.Restore(mark)
		detail["outcome"] = "taken_over"
		httpx.SetAudit(r, action, scope.name, detail)
		press.save(pressCtx)
		press.quiet = true
		stream.send(map[string]any{"type": "error", "error": map[string]any{"code": "taken_over", "message": "This chat was continued somewhere else."}})
		return nil
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

// sendCalled sends what a call came to, and counts it for the chat: a step for
// each call made, and a change for each that changed something.
func (pr *agentPress) sendCalled(call assistant.ToolCall, view agentCallView, result agentResult) {
	pr.stream.send(agentCalledLine(call, result))
	if result.link != nil {
		pr.stream.send(map[string]any{"type": "link", "link": result.link})
	}
	pr.c.Chat.Steps++
	if result.ok && (view.Kind == agentKindChange || view.Kind == agentKindDestructive) {
		pr.c.Chat.Changes++
	}
}

// agentCarryOver carries a saved chat on with the provider set up now. What one
// model gave back to be sent back as it came — its thinking — another refuses,
// so a chat that changes model goes on without it; the calls and their results
// are the same whoever reads them.
func agentCarryOver(c *assistant.Conversation, settings *assistant.Settings, client assistant.Client) {
	if c.Chat.Provider != string(settings.Provider) || c.Chat.Model != settings.Model {
		c.ForgetReasoning()
	}
	c.Revision, c.Tools = settings.Revision(), client.Tools()
	c.Chat.Provider, c.Chat.Model = string(settings.Provider), settings.Model
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

// agentOffer sets what a conversation of a scope is offered and how its calls
// are read, the same whether it begins here or is loaded back.
func agentOffer(c *assistant.Conversation, kind agentScopeKind) {
	switch kind {
	case agentScopeDraft:
		c.Offer = assistant.SetupTools
	case agentScopeServer:
		c.Offer = assistant.ServerTools
	case agentScopeDatabase:
		c.Offer = assistant.DatabaseTools
	default:
		c.Offer = assistant.AgentTools
	}
	c.Check, c.TurnLimit = assistant.CheckCalls(c.Offer), assistant.AgentTurnLimit
}

// apiOverview is what every prompt says about the dashboard's API.
func (s *Server) apiOverview() assistant.APIOverview {
	cat := s.apiCatalogue()
	return assistant.APIOverview{Areas: cat.areas, Excluded: cat.excluded}
}

// agentBegin writes what a conversation begins knowing.
func (s *Server) agentBegin(ctx context.Context, c *assistant.Conversation, scope *agentScope) {
	agentOffer(c, scope.kind)
	switch scope.kind {
	case agentScopeDeployment:
		c.System = assistant.AgentSystemPrompt(s.operatorContext(ctx, scope.deployment), s.agentCatalogue().lines(agentScopeDeployment), s.apiOverview())
	case agentScopeDraft:
		c.System = assistant.SetupSystemPrompt(s.draftContext(ctx, scope.draft), s.agentCatalogue().lines(agentScopeDraft), s.apiOverview())
	case agentScopeDatabase:
		c.System = assistant.DatabaseSystemPrompt(s.databaseContext(ctx, scope.database), s.apiOverview())
	default:
		o := assistant.ServerContext{Server: scope.name, Deployments: s.agentDeployments(ctx), About: scope.about}
		c.System = assistant.ServerSystemPrompt(o, s.apiOverview())
	}
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
			out.Detection = clipText(agentMaskShapes(masked), 12_000)
		}
	}
	return out
}

// agentName is a call as an audit entry names it.
func agentName(call assistant.ToolCall) string {
	switch call.Name {
	case assistant.ToolCallOperation:
		return call.AgentInput().Name
	case assistant.ToolAPIRequest:
		in := call.RequestInput()
		return in.Method + " " + in.Path
	}
	return call.Name
}

// agentHolds is the person's mode: whether a call waits for their answer. Full
// access runs everything. Otherwise what the assistant used to be refused waits
// (asks), a draft's own changes run, since a draft runs nothing until it is
// committed, and the mode decides the rest: "changes" runs reads, "ask" runs
// nothing unasked. Looking something up is free in every mode.
func agentHolds(scope *agentScope, mode assistant.Mode, view agentCallView) bool {
	switch {
	case view.Kind == agentKindFree, mode == assistant.ModeFull:
		return false
	case view.asks:
		return true
	case scope.kind == agentScopeDraft && view.draftOwn:
		return false
	case mode == assistant.ModeChanges:
		return view.Kind != agentKindRead
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
	if !ok || !op.offered(scope.kind) {
		return agentOperation{}, false
	}
	return *op, true
}

// view is a call as the page draws it, and the kind its mode reads.
func (pr *agentPress) view(ctx context.Context, call assistant.ToolCall) agentCallView {
	s, scope := pr.s, pr.scope
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
	case assistant.ToolFindRoutes:
		view.Kind = agentKindFree
		view.Label = "Look up the API's areas"
		if q := call.FindInput().Query; q != "" {
			view.Label = "Look up routes: " + clipText(q, 60)
		}
	case assistant.ToolWaitForRun:
		view.Label = fmt.Sprintf("Follow run #%d", call.AgentInput().Run)
	case assistant.ToolRunCommand:
		in := call.CommandInput()
		view.Reason, view.Input = in.Reason, call.Input
		view.Kind = assistant.ClassifyCommand(in.Command)
		view.Label = "Run " + clipText(strings.Join(strings.Fields(in.Command), " "), 80)
	case assistant.ToolTerminalRun, assistant.ToolTerminalSend, assistant.ToolTerminalRead:
		in := call.TerminalInput()
		view.Reason, view.Input = in.Reason, call.Input
		view.Kind = assistant.TerminalKind(call)
		switch call.Name {
		case assistant.ToolTerminalRun:
			view.Label = "In the terminal: " + clipText(strings.Join(strings.Fields(in.Command), " "), 80)
		case assistant.ToolTerminalSend:
			view.Label = "Type into the terminal"
			if in.Text != "" && !strings.Contains(in.Text, "[REDACTED]") {
				view.Label = "Type " + clipText(strconv.Quote(in.Text), 60)
			} else if len(in.Keys) > 0 {
				view.Label = "Press " + strings.Join(in.Keys, ", ")
			}
		default:
			view.Label = "Read the terminal"
		}
	case assistant.ToolAPIRequest:
		in := call.RequestInput()
		view.Reason, view.Input = in.Reason, call.Input
		view.Route = in.Method + " " + in.Path
		if len(in.Query) > 0 {
			view.Route += "?" + in.Query.Encode()
		}
		view.Label = in.Method + " " + in.Path
		route, _ := s.lookupRoute(in.Method, in.Path)
		if route == nil || route.Excluded != "" {
			// Nothing will run: it is answered at once with why.
			view.Kind = agentKindFree
			break
		}
		if s.agentProtected(ctx, route, in.Path) != "" {
			// Nothing will run: it is answered at once with why.
			view.Kind = agentKindFree
			view.Label = routeLabel(route.Words)
			break
		}
		view.Kind = s.apiRequestKind(ctx, route, in.Path, in.Body)
		view.Label = routeLabel(route.Words)
		if scope.kind == agentScopeDraft && strings.HasPrefix(route.Pattern, "/deploy/drafts/{draft}") &&
			strings.HasPrefix(in.Path+"/", "/deploy/drafts/"+scope.draft.ID+"/") {
			view.draftOwn = true
			view.asks = s.agentDraftAsks(ctx, scope, route.Method, strings.TrimSuffix(route.Pattern, "/"), in.Body)
		}
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
		// A draft's own operations, and what under /deploy it reads to choose
		// from, are the draft's: they run nothing.
		if scope.kind == agentScopeDraft && (!op.Both || op.Kind() == agentKindRead) {
			var args struct {
				Body json.RawMessage `json:"body"`
			}
			_ = json.Unmarshal(in.Arguments, &args)
			view.draftOwn = true
			view.asks = s.agentDraftAsks(ctx, scope, op.Method, op.Pattern, agentBody(args.Body))
		}
	default:
		view.Input = call.Input
	}
	if call.Problem != "" {
		view.Kind = agentKindFree
	}
	return view
}

// routeLabel is a route's words as a call's label: the first clause of its
// summary, its first letter up.
func routeLabel(words string) string {
	if cut := strings.IndexAny(words, ":;("); cut > 0 {
		words = words[:cut]
	}
	if cut := strings.Index(words, ". "); cut > 0 {
		words = words[:cut]
	}
	words = clipText(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(words), ".")), 80)
	if words == "" {
		return words
	}
	r, size := utf8.DecodeRuneInString(words)
	return strings.ToUpper(string(r)) + words[size:]
}

// agentBody is a body a model may have sent as the text of its JSON.
func agentBody(raw json.RawMessage) json.RawMessage {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		trimmed := strings.TrimSpace(text)
		if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
			return json.RawMessage(trimmed)
		}
	}
	return raw
}

// agentDraftAsks is what a draft's conversation asks before it does, whatever
// the scope would let run: committing the draft, which is pressing Deploy;
// discarding it; and a save that does what a deploy key's commit is refused —
// changing what is deployed, pointing a variable at a saved credential, a store
// or a database, or a plan that reaches past its container to the host. Text in
// the repository reaches the model through what detection read, so these wait
// for the person unless they chose full access.
func (s *Server) agentDraftAsks(ctx context.Context, scope *agentScope, method, pattern string, body json.RawMessage) bool {
	switch {
	case method == http.MethodPost && pattern == "/deploy/drafts/{draft}/commit",
		method == http.MethodDelete && pattern == "/deploy/drafts/{draft}":
		return true
	case method != http.MethodPut || pattern != "/deploy/drafts/{draft}":
		return false
	}
	var request deploy.DraftSaveRequest
	if json.Unmarshal(body, &request) != nil {
		// The route refuses what it cannot read.
		return false
	}
	draft, err := s.modules.deployPlanning.Get(ctx, scope.draft.ID)
	if err != nil {
		return true
	}
	source := draft.Data.Source
	if request.Source != nil {
		if !reflect.DeepEqual(request.Source, draft.Data.Source) {
			return true
		}
		source = request.Source
	}
	if request.Dotenv != nil && strings.Contains(*request.Dotenv, "${{") {
		return true
	}
	if request.Configuration == nil {
		return false
	}
	if len(hostAuthority(source, request.Configuration.Build, request.Configuration.Runtime)) > 0 {
		return true
	}
	previous := draft.VariableReferences()
	next := *draft
	next.Data.Configuration = request.Configuration
	for name, reference := range next.VariableReferences() {
		if previous[name] != reference {
			return true
		}
	}
	return false
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
	if result.terminal != nil {
		line["terminal"] = result.terminal
	}
	return line
}

// execute runs one call the person's mode, or the person, has let through.
func (pr *agentPress) execute(ctx context.Context, call assistant.ToolCall, view agentCallView) agentResult {
	s, scope := pr.s, pr.scope
	deadline := agentCallDeadline + time.Duration(assistant.AgentWaitMax)*time.Second
	switch call.Name {
	case assistant.ToolRunCommand:
		// A command keeps to its own time, up to the most it may ask for.
		deadline = time.Duration(assistant.CommandWaitMax)*time.Second + agentCallDeadline
	case assistant.ToolTerminalRun, assistant.ToolTerminalSend, assistant.ToolTerminalRead:
		deadline = time.Duration(call.TerminalInput().Wait)*time.Second + agentCallDeadline
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	refuse := func(why string) agentResult { return agentResult{text: why, said: why} }
	// Asked again for each call, so a role taken away during a press takes effect.
	p, err := s.Authn.Recheck(pr.r, pr.p)
	if err != nil {
		return refuse("The session this was asked under has ended, so nothing was done.")
	}
	switch call.Name {
	case assistant.ToolDescribeOperation:
		return s.agentDescribe(scope, call.AgentInput().Name)
	case assistant.ToolCallOperation:
		return pr.callOperation(ctx, p, call.AgentInput())
	case assistant.ToolOpenPage:
		return s.agentOpenPage(scope, call.AgentInput())
	case assistant.ToolFindRoutes:
		return s.apiCatalogue().findRoutes(call.FindInput())
	case assistant.ToolAPIRequest:
		return pr.apiRequest(ctx, p, call.RequestInput(), view)
	case assistant.ToolRunCommand:
		return commandChange(pr.runCommand(ctx, p, call.CommandInput()), view)
	case assistant.ToolTerminalRun, assistant.ToolTerminalSend, assistant.ToolTerminalRead:
		return commandChange(pr.terminal(ctx, p, call), view)
	case assistant.ToolWaitForRun:
		if scope.deployment == nil {
			return refuse("wait_for_run follows a deployment's run, and this conversation is not about one: read the run with api_request")
		}
		return s.agentWaitForRun(ctx, scope.deployment, call.AgentInput())
	}
	// Reading a repository fetches it first, which the checkout bounds itself at
	// a minute and more; the other reads are the store's and Docker's.
	readFor := 60 * time.Second
	if call.Name == assistant.ToolReadSource {
		readFor = agentCallDeadline
	}
	readCtx, done := context.WithTimeout(ctx, readFor)
	defer done()
	summary := scope.deployment
	switch call.Name {
	case assistant.ToolHostAlerts, assistant.ToolFirewallRules, assistant.ToolSecurityUpdates:
	case assistant.ToolReadSource:
		if id := call.OperatorInput().Deployment; id > 0 && (summary == nil || summary.ID != id) {
			other, err := s.agentDeployment(readCtx, p, id)
			if err != nil {
				return refuse(err.Error())
			}
			summary = other
		}
		if summary == nil {
			return refuse("read_source reads one deployment's code: name it with deployment, its id as your list gives it")
		}
	default:
		if summary == nil {
			return refuse("that read is about a deployment, and this conversation is not about one")
		}
	}
	text, said, ok := s.operatorRead(readCtx, pr.r, p, summary, call)
	return agentResult{text: text, said: said, ok: ok}
}

// checkAgentImages reads what was pasted with a question: at most
// assistant.MaxImages, each an image of a format every provider reads, made small,
// with a thumbnail smaller still.
func checkAgentImages(images []agentImage) error {
	if len(images) > assistant.MaxImages {
		return httpx.BadRequest("a question carries at most %d images", assistant.MaxImages)
	}
	for _, img := range images {
		if err := assistant.CheckImage(img.Image, assistant.MaxImageBytes); err != nil {
			return httpx.BadRequest("%s", err.Error())
		}
		if err := assistant.CheckImage(img.Thumb, assistant.MaxThumbBytes); err != nil {
			return httpx.BadRequest("an image's thumbnail: %s", err.Error())
		}
	}
	return nil
}

// agentDeployment is another deployment a call names, its production
// environment, read under what the deployment's own assistant asks of a role.
func (s *Server) agentDeployment(ctx context.Context, p *httpx.Principal, id int64) (*deploy.DeploymentSummary, error) {
	if !p.Can(auth.CapServiceControl) {
		return nil, fmt.Errorf("your role does not reach a deployment's assistant reads (%s)", auth.CapServiceControl)
	}
	summary, err := s.modules.deployRuns.EnvironmentDeploymentSummary(ctx, id, 0, deploy.QueueBudget{
		Heavy: s.Cfg.DeployHeavySlots, Light: s.Cfg.DeployLightSlots,
	})
	if err != nil || summary == nil || summary.EnvironmentID <= 0 {
		return nil, fmt.Errorf("there is no deployment %d with an environment to read", id)
	}
	return summary, nil
}

// agentDescribe is one operation's parameters.
func (s *Server) agentDescribe(scope *agentScope, name string) agentResult {
	op, ok := s.agentOperationFor(scope, name)
	if !ok {
		return agentResult{text: "There is no operation called " + name + " in your list. Your list names them; api_request makes any route by its path.", said: "no such operation"}
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
	pages := agentOpenablePages
	switch scope.kind {
	case agentScopeServer:
		pages = agentServerPages
	case agentScopeDatabase:
		pages = agentDatabasePages
	}
	page, ok := pages[in.Page]
	if !ok || (scope.kind == agentScopeDeployment && scope.deployment == nil) {
		return agentResult{text: "That page cannot be offered here.", said: "no such page"}
	}
	link := &agentLink{Label: page.Label, Href: agentHref(scope, page), Note: in.Note}
	return agentResult{text: "The person has been shown a shortcut to " + page.Label + ".", said: page.Label, ok: true, link: link}
}

// agentHref is a page's address for a conversation.
func agentHref(scope *agentScope, page agentPage) string {
	href := page.Path
	switch {
	case page.Absolute:
	case scope.deployment != nil:
		href = "/deploy/" + strconv.FormatInt(scope.deployment.ID, 10) + page.Path
	case scope.database != nil:
		href = "/databases/" + strconv.FormatInt(scope.database.ID, 10) + page.Path
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

// agentArguments fills in what a conversation already knows: the deployment and,
// by default, the environment, and the draft. call_operation is about the
// conversation's deployment; another's is api_request's, by its path.
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
	if body, ok := args["body"]; ok {
		args["body"] = agentBody(body)
	}
	for _, param := range op.Path {
		given, named := args[param.Name]
		switch {
		case param.Name == "id" && scope.deployment != nil && strings.HasPrefix(op.Pattern, "/deploy/{id}"):
			if named {
				var id float64
				var text string
				same := json.Unmarshal(given, &id) == nil && int64(id) == scope.deployment.ID ||
					json.Unmarshal(given, &text) == nil && text == strconv.FormatInt(scope.deployment.ID, 10)
				if !same {
					return nil, fmt.Errorf("call_operation works on this conversation's deployment (id %d); to reach another deployment, use api_request with its path, such as %s",
						scope.deployment.ID, strings.Replace(op.Pattern, "{id}", strings.Trim(string(given), `"`), 1))
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

// callOperation runs one operation, through the router, as the person.
func (pr *agentPress) callOperation(ctx context.Context, p *httpx.Principal, in assistant.AgentInput) agentResult {
	s, scope := pr.s, pr.scope
	refuse := func(why string) agentResult { return agentResult{text: why, said: why} }
	op, ok := s.agentOperationFor(scope, in.Name)
	if !ok {
		return refuse("There is no operation called " + in.Name + " in your list. Your list names them; api_request makes any route by its path.")
	}
	raw, err := agentArguments(scope, op, in.Arguments)
	if err != nil {
		return refuse(err.Error())
	}
	req, err := mcp.DecodeRequest(op.Operation, raw)
	if err != nil {
		return refuse(err.Error())
	}
	masker, err := pr.masker(ctx)
	if err != nil {
		return refuse("The secret values to mask could not be read, so nothing was read or changed.")
	}
	callCtx, cancel := context.WithTimeout(ctx, agentCallDeadline)
	defer cancel()
	// A configuration's change is spelled out, which a read each side of it gives.
	before := s.agentConfiguration(callCtx, pr.r, p, op, req)
	rec, err := s.assistantDispatch(callCtx, pr.r, p, op.Method, req)
	if err != nil {
		return refuse(agentDispatchFailure(err))
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
	result.change = s.agentChangeOf(callCtx, pr.r, p, scope, op, req, rec, before, masker)
	result.said = "Done"
	return result
}

// apiRequest makes one route's request of the dashboard's API, as the person.
func (pr *agentPress) apiRequest(ctx context.Context, p *httpx.Principal, in assistant.RequestInput, view agentCallView) agentResult {
	s := pr.s
	refuse := func(why string) agentResult { return agentResult{text: why, said: clipText(why, 300)} }
	route, methods := s.lookupRoute(in.Method, in.Path)
	switch {
	case !assistant.CanonicalPath(in.Path):
		// What was looked up is what will be routed, or nothing is sent.
		return refuse(fmt.Sprintf("%q is not a route's path.", in.Path))
	case route == nil && len(methods) > 0:
		return refuse(fmt.Sprintf("%s takes %s, not %s.", in.Path, strings.Join(methods, ", "), in.Method))
	case route == nil:
		return refuse(fmt.Sprintf("No route answers %s %s. find_routes looks them up by words.", in.Method, in.Path))
	case route.Excluded != "":
		return refuse(fmt.Sprintf("The assistant cannot make %s %s: %s.", route.Method, route.Path, route.Excluded))
	}
	if why := s.agentProtected(ctx, route, in.Path); why != "" {
		return refuse(why)
	}
	masker, err := pr.masker(ctx)
	if err != nil {
		return refuse("The secret values to mask could not be read, so nothing was read or changed.")
	}
	callCtx, cancel := context.WithTimeout(ctx, agentCallDeadline)
	defer cancel()
	rec, err := s.assistantDispatch(callCtx, pr.r, p, in.Method, mcp.RouteRequest{Path: in.Path, Query: in.Query, Body: in.Body})
	if err != nil {
		return refuse(agentDispatchFailure(err))
	}
	if rec.status >= http.StatusBadRequest {
		refusal := agentRefusal(rec, masker)
		refusal.text = fmt.Sprintf("%s %s answered %d: %s", in.Method, in.Path, rec.status, refusal.text)
		return refusal
	}
	text, whole := agentAnswer(rec, masker)
	if view.Kind == agentKindRead {
		if !whole {
			return refuse(text)
		}
		return agentResult{text: text, ok: true, said: agentReadWords(rec), output: clipText(text, agentOutputBytes)}
	}
	if !whole {
		text = fmt.Sprintf(`{"status":%d,"done":true}`, rec.status)
	}
	return agentResult{text: text, ok: true, said: "Done", output: clipText(text, agentOutputBytes), change: agentAPIChange(route, in.Path)}
}

// agentDispatchFailure says why a request could not be made.
func agentDispatchFailure(err error) string {
	if errors.Is(err, errDispatchTimeout) {
		return fmt.Sprintf("The route did not answer within %s, so the answer was not waited for; what it was doing may still finish. Read the state again before trying again.", agentCallDeadline)
	}
	return "The route could not be called."
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
		if envelope.Error.Field != "" {
			message += " (field: " + envelope.Error.Field + ")"
		}
	}
	if rec.status >= http.StatusInternalServerError {
		message = "The dashboard could not do this: " + message
	}
	message = agentMaskText(message, masker)
	return agentResult{text: message, said: clipText(message, 300)}
}

// agentAnswer is a route's answer as the model reads it, and whether it could be
// read: every secret masked in every string of it (agentMaskValue), and only then
// cut at the read's bound. An answer that cannot be read whole cannot be masked, so
// none of it is sent, and the text says why. Text that is not JSON — a log, a
// file — is masked as text; anything else is described, not sent.
func agentAnswer(rec *boundedRecorder, masker *deploy.DiagnosisMasker) (string, bool) {
	body := bytes.TrimSpace(rec.body.Bytes())
	if len(body) == 0 {
		return fmt.Sprintf(`{"status":%d,"done":true}`, rec.status), true
	}
	if rec.total > rec.body.Len() {
		return fmt.Sprintf("The answer is %d bytes, too long to mask whole, so none of it was read. Ask for less with the route's parameters.", rec.total), false
	}
	media := strings.ToLower(strings.TrimSpace(strings.Split(rec.header.Get("Content-Type"), ";")[0]))
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		if (strings.HasPrefix(media, "text/") || media == "application/x-ndjson" || media == "application/yaml") && utf8.Valid(body) {
			text := agentMaskText(string(body), masker)
			if len(text) > maxOperatorResult {
				// The newest of a log is its end.
				text = fmt.Sprintf("(the first %d bytes were left out) ", len(text)-maxOperatorResult) + text[len(text)-maxOperatorResult:]
			}
			return text, true
		}
		if media == "" || strings.Contains(media, "json") {
			return "The answer could not be read to mask it, so none of it was read.", false
		}
		return fmt.Sprintf("The answer is %s, %d bytes, which is not text the assistant can read. The person can open it from its page.", media, rec.total), false
	}
	raw, err := json.Marshal(agentMaskValue(value, masker))
	if err != nil {
		return "The answer could not be written out.", false
	}
	text := string(raw)
	if len(text) > maxOperatorResult {
		text = clipText(text, maxOperatorResult) + fmt.Sprintf(" (cut: the answer is %d bytes; ask for less with the route's parameters)", len(text))
	}
	return text, true
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
func (s *Server) agentConfiguration(ctx context.Context, r *http.Request, p *httpx.Principal, op agentOperation, req mcp.RouteRequest) map[string]any {
	if op.Grant == mcp.GrantRead || !strings.HasSuffix(op.Pattern, "/environments/{env}/configuration") {
		return nil
	}
	rec, err := s.assistantDispatch(ctx, r, p, http.MethodGet, mcp.RouteRequest{Path: req.Path, Query: url.Values{}})
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
	if scope.draft != nil && op.Scope == agentScopeDraft && !op.Both {
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
	after, err := s.assistantDispatch(ctx, r, p, http.MethodGet, mcp.RouteRequest{Path: req.Path, Query: url.Values{}})
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
