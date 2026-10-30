package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The SQL editor's assistant (internal/assistant): the provider an
// administrator sets up for the install, and the one route a question to it
// goes through.
//
// That route is a POST under /databases/{id}, which the protected-connection
// guard refuses for a protected connection like every route not on its
// list: nothing about a connection the operator marked protected is sent to
// a model. Every call is audited with the connection, the person and what it
// cost in tokens, and with what was attached by kind and count, never by
// content.

const assistantSettingKey = "databases.assistant"

// assistantState is what the assistant's routes keep between requests.
type assistantState struct {
	conversations *assistant.Store
	// operators holds the deployment operator's conversations, one per
	// person and environment (handlers_deploy_operator.go).
	operators *assistant.Store
	// limit is the install's budget of model calls, one bucket however many
	// people ask, because every call is billed to the same key.
	limit *httpx.Limiter
	// slots bounds how many answers stream at once.
	slots   chan struct{}
	options assistant.Options
	// diagnosing holds the runs whose failure is being asked about, so a
	// second press waits for the first answer instead of paying for another.
	diagnosing sync.Map
}

func newAssistantState() *assistantState {
	return &assistantState{
		conversations: assistant.NewStore(),
		operators:     assistant.NewStore(),
		limit:         httpx.NewLimiter(20, 6),
		slots:         make(chan struct{}, 4),
		options:       assistant.Options{HTTP: assistant.NewHTTPClient()},
	}
}

// mountDatabaseAssistantRoutes is called inside /databases.
func (s *Server) mountDatabaseAssistantRoutes(r chi.Router) {
	// Whether the assistant is set up and with what, which every page that
	// offers it reads. The key is never in the answer.
	r.Method(http.MethodGet, "/assistant", s.handle(s.handleAssistantSettings))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPut, "/assistant", s.handle(s.handleAssistantConfigure))
		r.Method(http.MethodDelete, "/assistant", s.handle(s.handleAssistantRemove))
	})
	// Asking takes what running a statement takes: what it answers is a
	// statement to run.
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		r.Method(http.MethodPost, "/{id}/assistant", s.handle(s.handleDBAssistant))
	})
}

// assistantSettings is the stored provider, nil when there is none.
func (s *Server) assistantSettings(ctx context.Context) (*assistant.Settings, error) {
	sealed, ok, err := s.Store.Setting(ctx, assistantSettingKey)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	if !ok || sealed == "" {
		return nil, nil
	}
	plain, err := s.Sealer.Open(sealed)
	if err != nil {
		return nil, errAssistantSealed
	}
	var settings assistant.Settings
	if err := json.Unmarshal([]byte(plain), &settings); err != nil {
		return nil, errAssistantSealed
	}
	return &settings, nil
}

var errAssistantSealed = httpx.Err(http.StatusConflict, "assistant_unreadable",
	"the assistant's saved provider cannot be opened with this install's master key; an administrator sets it up again")

type assistantSettingsView struct {
	assistant.View
	// Problem is a saved provider this install cannot read.
	Problem string `json:"problem,omitempty"`
}

func (s *Server) handleAssistantSettings(w http.ResponseWriter, r *http.Request) error {
	settings, err := s.assistantSettings(r.Context())
	view := assistantSettingsView{View: assistant.ViewOf(settings)}
	if errors.Is(err, errAssistantSealed) {
		view.Problem = errAssistantSealed.Message
	} else if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

// handleAssistantConfigure saves a provider once the provider has said the
// key, the address and the model answer — asked without asking the model
// anything — so a mistyped key is said under the field it was typed in
// rather than under the first question somebody asks.
func (s *Server) handleAssistantConfigure(w http.ResponseWriter, r *http.Request) error {
	var in assistant.Setup
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	httpx.SetAudit(r, "database.assistant.configure", string(in.Provider), nil)
	stored, err := s.assistantSettings(r.Context())
	if err != nil && !errors.Is(err, errAssistantSealed) {
		return err
	}
	settings, err := assistant.Prepare(in, stored)
	var field *assistant.FieldError
	if errors.As(err, &field) {
		return &httpx.APIError{Status: http.StatusBadRequest, Code: "bad_request", Message: field.Message, Field: field.Field}
	}
	if err != nil {
		return err
	}
	client, err := assistant.New(settings, s.assistant.options)
	if err != nil {
		return httpx.Internal(err)
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	if err := client.Probe(ctx); err != nil {
		return assistantCheckFailed(err)
	}
	settings.UpdatedAt = time.Now().UTC()
	settings.UpdatedBy = httpx.MustPrincipal(r).Username()
	plain, err := json.Marshal(settings)
	if err != nil {
		return httpx.Internal(err)
	}
	sealed, err := s.Sealer.Seal(string(plain))
	if err != nil {
		return httpx.Internal(err)
	}
	if err := s.Store.SetSetting(r.Context(), assistantSettingKey, sealed); err != nil {
		return httpx.Internal(err)
	}
	detail := map[string]any{
		"provider": settings.Provider, "model": settings.Model,
		"keyChanged": stored == nil || stored.Key != settings.Key,
	}
	if u, err := url.Parse(settings.BaseURL); err == nil && u.Host != "" {
		detail["host"] = u.Host
	}
	httpx.SetAudit(r, "database.assistant.configure", string(settings.Provider), detail)
	httpx.JSON(w, http.StatusOK, assistantSettingsView{View: assistant.ViewOf(&settings)})
	return nil
}

func assistantCheckFailed(err error) error {
	pe, ok := assistant.IsProviderError(err)
	if !ok {
		return httpx.Err(http.StatusBadGateway, "assistant_check_failed", "the provider could not be asked: "+err.Error()).Retry()
	}
	return &httpx.APIError{
		Status: http.StatusBadRequest, Code: "assistant_check_failed", Message: pe.Message,
		Field: pe.Field, Raw: pe.Raw, Retryable: pe.Retryable,
	}
}

// handleAssistantRemove turns the assistant off and forgets the key.
func (s *Server) handleAssistantRemove(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "database.assistant.remove", "", nil)
	stored, err := s.assistantSettings(r.Context())
	if err != nil && !errors.Is(err, errAssistantSealed) {
		return err
	}
	if err := s.Store.SetSetting(r.Context(), assistantSettingKey, ""); err != nil {
		return httpx.Internal(err)
	}
	if stored != nil {
		httpx.SetAudit(r, "database.assistant.remove", string(stored.Provider), map[string]any{"model": stored.Model})
	}
	httpx.JSON(w, http.StatusOK, assistantSettingsView{View: assistant.ViewOf(nil)})
	return nil
}

type assistantRequest struct {
	// Conversation carries one on; empty begins one.
	Conversation string `json:"conversation"`
	// Schema is the schema in focus, read when a conversation begins.
	Schema string `json:"schema"`
	// Task is a question. Allow, instead, answers the calls the last answer
	// ended on: true runs them, false declines them.
	Task  *assistant.Task `json:"task"`
	Allow *bool           `json:"allow"`
}

// assistantTurn is the turn deadline: a question that has not been answered
// in five minutes is not going to be. assistantFailure says it in words.
const assistantTurn = 5 * time.Minute

// handleDBAssistant asks the assistant one question, or answers the calls it
// asked for, and streams the answer back as one JSON object a line.
//
// Everything that can refuse the request refuses it before the provider is
// asked, as an ordinary error. Once the provider has begun to answer, the
// answer is streamed, and a failure after that is the stream's last line.
func (s *Server) handleDBAssistant(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req assistantRequest
	if err := httpx.DecodeJSONNumbers(r, &req); err != nil {
		return err
	}
	if (req.Task == nil) == (req.Allow == nil) {
		return httpx.BadRequest("ask a question, or answer the calls the assistant asked for")
	}
	action := "database.assistant.ask"
	if req.Allow != nil {
		action = "database.assistant.tool"
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, action, conn.Name, nil)
	settings, err := s.assistantSettings(r.Context())
	if err != nil {
		return err
	}
	if settings == nil {
		return httpx.Err(http.StatusConflict, "assistant_off",
			"the assistant is not set up: an administrator chooses its provider in the assistant's settings")
	}
	if !conn.Driver.IsSQL() {
		return httpx.BadRequest("the assistant writes SQL; %s has its own console", conn.Driver)
	}
	pool, err := s.modules.dbs.Pool(r.Context(), id, conn.Driver, dsn)
	if err != nil {
		return connectFailed(dsn, err)
	}
	identity := s.identityOf(conn.ID, dsn, func() dbx.Identity { return dbx.IdentifySQL(r.Context(), pool, conn.Driver) })
	if !dbx.Capable(conn.Driver, identity.Flavor, "assistant") {
		return httpx.BadRequest("the assistant is not offered for %s", dbx.FlavorLabel(identity.Flavor))
	}
	var question string
	if req.Task != nil {
		if req.Task.Kind == assistant.TaskPolicy && !dbx.Capable(conn.Driver, identity.Flavor, "assistantPolicy") {
			return httpx.BadRequest("row-level security policies are drafted for PostgreSQL; %s has none", dbx.FlavorLabel(identity.Flavor))
		}
		question, err = req.Task.Message()
		var field *assistant.FieldError
		if errors.As(err, &field) {
			return &httpx.APIError{Status: http.StatusBadRequest, Code: "bad_request", Message: field.Message, Field: field.Field}
		}
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
	store := s.assistant.conversations
	owner := httpx.MustPrincipal(r).Username()
	var c *assistant.Conversation
	if req.Conversation == "" {
		if req.Task == nil {
			return httpx.BadRequest("there is no conversation to answer for")
		}
		c = store.Begin(owner, id)
		product := strings.TrimSpace(dbx.FlavorLabel(identity.Flavor) + " " + identity.Number)
		if err := beginAssistantConversation(r.Context(), c, pool, conn.Driver, req.Schema, question, product, client.Tools()); err != nil {
			return err
		}
		c.Revision = settings.Revision()
	} else {
		c, err = store.Take(req.Conversation, owner, id)
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
		"provider": settings.Provider, "model": settings.Model,
		"conversation": c.ID[:8], "turn": c.Turns + 1,
	}
	stream := &assistantStream{w: w, start: map[string]any{
		"type": "start", "conversation": c.ID, "provider": settings.Provider, "model": settings.Model,
		"modelLabel": assistant.ModelLabel(settings.Provider, settings.Model), "context": c.Context,
	}}
	if req.Task != nil {
		detail["task"] = req.Task.Kind
		for k, v := range req.Task.Attachments() {
			detail[k] = v
		}
		if err := c.Ask(question); err != nil {
			return httpx.Err(http.StatusConflict, "conversation_full", err.Error())
		}
	} else {
		if len(c.Pending) == 0 {
			return httpx.Err(http.StatusConflict, "nothing_pending", "the assistant is not waiting for an answer")
		}
		allow := *req.Allow
		detail["allowed"] = allow
		read, missing := 0, 0
		described := []map[string]any{}
		results := assistant.ToolResults(c.Pending, func(call assistant.ToolCall) (string, bool) {
			if !allow {
				return assistant.Declined, false
			}
			ctx, cancel := timeoutCtx(r, 60*time.Second)
			defer cancel()
			tables, absent := assistant.Describe(ctx, pool, conn.Driver, c.Schema, call.Tables, c.Known)
			read, missing = read+len(tables), missing+len(absent)
			names := make([]string, 0, len(tables))
			for _, t := range tables {
				names = append(names, t.Name)
			}
			described = append(described, map[string]any{"tables": names, "missing": absent})
			return assistant.DescribedText(tables, absent, c.Schema), true
		})
		c.Answer(results)
		detail["tables"], detail["missing"] = read, missing
		if allow {
			stream.send(map[string]any{"type": "described", "calls": described})
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), assistantTurn)
	defer cancel()
	outcome, err := assistant.Exchange(ctx, client, c, func(e assistant.Event) {
		if e.Thinking {
			stream.send(map[string]any{"type": "thinking"})
			return
		}
		stream.send(map[string]any{"type": "text", "text": e.Text})
	})
	// The tokens the turn cost, under "usage": the audit log redacts any
	// field whose name says token.
	detail["usage"] = outcome.Usage
	if outcome.Model != "" && outcome.Model != settings.Model {
		detail["answeredBy"] = outcome.Model
	}
	if err != nil {
		c.Restore(mark)
		message, failure := assistantFailure(r.Context(), ctx, err)
		detail["outcome"] = failure
		httpx.SetAudit(r, action, conn.Name, detail)
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
	detail["outcome"] = assistantOutcomeWord(outcome)
	httpx.SetAudit(r, action, conn.Name, detail)
	done := map[string]any{"type": "done", "stop": outcome.Stop, "usage": outcome.Usage, "model": outcome.Model, "kept": outcome.Kept}
	if len(outcome.Pending) > 0 {
		done["pending"] = outcome.Pending
	}
	stream.send(done)
	return nil
}

// beginAssistantConversation reads the schema in focus and fixes the
// conversation's standing instructions from it. The first question is what
// decides which tables are described first.
func beginAssistantConversation(ctx context.Context, c *assistant.Conversation, pool *sql.DB, driver dbx.Driver, schema, question, product string, tools bool) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	snapshot, err := assistant.ReadSchema(ctx, pool, driver, schema, question)
	if err != nil {
		return queryFailed(err)
	}
	text, stats := assistant.SchemaText(snapshot, question)
	c.Schema, c.Known, c.Context, c.Tools = snapshot.Schema, snapshot.Names, stats, tools
	c.System = assistant.SystemPrompt(product, text, tools)
	return nil
}

// assistantFailure says why a turn did not finish, and names how it ended
// for the audit entry: stopped by the person, who closed the request, out of
// the turn's time, or failed.
func assistantFailure(request, turn context.Context, err error) (string, string) {
	switch {
	case request.Err() != nil:
		return "the answer was stopped", "cancelled"
	case errors.Is(turn.Err(), context.DeadlineExceeded):
		return "the answer took longer than five minutes and was stopped", "failed"
	case errors.Is(err, assistant.ErrMalformedCall):
		return err.Error() + "; ask again", "failed"
	}
	return err.Error(), "failed"
}

func assistantOutcomeWord(o assistant.Outcome) string {
	switch {
	case o.Stop == assistant.StopRefusal:
		return "refused"
	case len(o.Pending) > 0:
		return "asked"
	case o.Stop == assistant.StopLength:
		return "cut"
	case !o.Kept:
		return "empty"
	}
	return "answered"
}

// assistantStream writes an answer as it arrives, one JSON object a line.
// Nothing is written until the first line is: an error before then is
// answered as an ordinary error, with its status.
type assistantStream struct {
	w       http.ResponseWriter
	start   map[string]any
	started bool
}

func (st *assistantStream) send(v any) {
	if !st.started {
		st.started = true
		h := st.w.Header()
		h.Set("Content-Type", "application/x-ndjson")
		// A proxy that buffers would hold the answer until it was whole.
		h.Set("X-Accel-Buffering", "no")
		st.w.WriteHeader(http.StatusOK)
		st.write(st.start)
	}
	st.write(v)
}

func (st *assistantStream) write(v any) {
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = st.w.Write(append(line, '\n'))
	_ = http.NewResponseController(st.w).Flush()
}
