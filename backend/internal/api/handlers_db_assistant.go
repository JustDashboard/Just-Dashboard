package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Where the assistant's provider lived before there were several, under
// /databases/assistant, which still reads and saves the active one; the state
// every assistant route shares; and a connection's own assistant, which is the
// dashboard's one assistant told about that connection
// (handlers_assistant_database.go).
//
// A connection's assistant is a POST under /databases/{id}, which the
// protected-connection guard refuses for a protected connection like every
// route not on its list: nothing about a connection the operator marked
// protected is sent to a model.

const assistantSettingKey = "databases.assistant"

// assistantState is what the assistant's routes keep between requests.
type assistantState struct {
	// operators holds the deployment operator's conversations, one per
	// person and environment (handlers_deploy_operator.go).
	operators *assistant.Store
	// limit is the install's budget of model calls, one bucket however many
	// people ask, because every call is billed to the same key.
	limit *httpx.Limiter
	// slots bounds how many answers stream at once.
	slots   chan struct{}
	options assistant.Options
	// ringMu holds a change to the keyring from its read to its write, which spans a
	// check of the provider: a slow save must not bring back a key just removed.
	ringMu sync.Mutex
	// terminals are the chats' terminal windows (handlers_assistant_terminal.go).
	terminals *agentTerminals
	// diagnosing holds the runs whose failure is being asked about, so a
	// second press waits for the first answer instead of paying for another.
	diagnosing sync.Map
}

func newAssistantState() *assistantState {
	return &assistantState{
		operators: assistant.NewStore(),
		terminals: newAgentTerminals(),
		limit:     httpx.NewLimiter(20, 6),
		slots:     make(chan struct{}, 4),
		options:   assistant.Options{HTTP: assistant.NewHTTPClient()},
	}
}

// mountDatabaseAssistantRoutes is called inside /databases.
func (s *Server) mountDatabaseAssistantRoutes(r chi.Router) {
	// Whether the assistant is set up and with what, which every page that
	// offers it reads. The key is never in the answer.
	r.Method(http.MethodGet, "/assistant", s.handle(s.handleAssistantSettings))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		// Who answers is a person's to choose: a key pointing it at a server of its
		// own would be handed what everybody asks, and could answer with what to do.
		r.Use(httpx.RequireSession)
		r.Method(http.MethodPut, "/assistant", s.handle(s.handleAssistantConfigure))
		r.Method(http.MethodDelete, "/assistant", s.handle(s.handleAssistantRemove))
	})
	// A connection's assistant (handlers_assistant_database.go): the dashboard's
	// one assistant, told about this connection. It acts as the person who asks, so
	// it is a session's and asks for what a deployment's does; a protected
	// connection is refused by the guard every database route is behind.
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl), httpx.RequireSession)
		r.Method(http.MethodPost, "/{id}/assistant", s.handle(s.handleDatabaseAssistant))
	})
}

// assistantSettings is the provider every page asks, nil when there is none.
func (s *Server) assistantSettings(ctx context.Context) (*assistant.Settings, error) {
	ring, err := s.assistantRing(ctx)
	if err != nil {
		return nil, err
	}
	return ring.Current(), nil
}

var errAssistantSealed = httpx.Err(http.StatusConflict, "assistant_unreadable",
	"the assistant's saved provider cannot be opened with this install's master key; an administrator sets it up again")

type assistantSettingsView struct {
	assistant.View
	// Problem is a saved provider this install cannot read.
	Problem string `json:"problem,omitempty"`
}

func (s *Server) handleAssistantSettings(w http.ResponseWriter, r *http.Request) error {
	ring, err := s.assistantRing(r.Context())
	view := assistantSettingsView{}
	if errors.Is(err, errAssistantSealed) {
		view.View = assistant.RingView(nil)
		view.Problem = errAssistantSealed.Message
	} else if err != nil {
		return err
	} else {
		view.View = assistant.RingView(ring)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

// handleAssistantConfigure saves the provider this route is given and makes it
// the active one.
func (s *Server) handleAssistantConfigure(w http.ResponseWriter, r *http.Request) error {
	var in assistant.Setup
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.Provider != assistant.ProviderAnthropic && in.Provider != assistant.ProviderOpenAI {
		httpx.SetAudit(r, "database.assistant.configure", string(in.Provider), nil)
		return &httpx.APIError{Status: http.StatusBadRequest, Code: "bad_request", Message: "choose Anthropic or an OpenAI-compatible server", Field: "provider"}
	}
	ring, err := s.saveAssistantProfile(r, assistant.PresetForSetup(in), in, "database.assistant.configure", true)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, assistantSettingsView{View: assistant.RingView(ring)})
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

// handleAssistantRemove turns the assistant off and forgets every key.
func (s *Server) handleAssistantRemove(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "database.assistant.remove", "", nil)
	s.assistant.ringMu.Lock()
	defer s.assistant.ringMu.Unlock()
	stored, err := s.assistantSettings(r.Context())
	if err != nil && !errors.Is(err, errAssistantSealed) {
		return err
	}
	if err := s.saveAssistantRing(r.Context(), assistant.RingOf(nil)); err != nil {
		return err
	}
	if stored != nil {
		httpx.SetAudit(r, "database.assistant.remove", string(stored.Provider), map[string]any{"model": stored.Model})
	}
	httpx.JSON(w, http.StatusOK, assistantSettingsView{View: assistant.RingView(nil)})
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
	case errors.Is(err, assistant.ErrMalformedCall), errors.Is(err, assistant.ErrUnreadableAnswer):
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
	// keep, when set, is handed every line as it is sent, for a saved chat to
	// keep what the page was shown.
	keep func([]byte)
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
	if st.keep != nil {
		st.keep(line)
	}
	_, _ = st.w.Write(append(line, '\n'))
	_ = http.NewResponseController(st.w).Flush()
}
