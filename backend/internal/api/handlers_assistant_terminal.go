package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
	"github.com/go-chi/chi/v5"
)

// A chat's terminal: one real window of the Terminal page's per chat, opened by
// its first terminal_run as the person, under the terminal capability, and held on
// the host like any other window, so it outlives the press, the page and the
// dashboard restarting. The person sees it on the Terminal page (named for the
// chat, owned by them) and may type into it there or from the chat.
//
// What it prints is gathered as it arrives (agentTerminal.pump), so nothing is
// missed between two calls, and each call answers with what arrived since the last,
// cleaned of the terminal's own codes (terminalText) and masked like any answer.
// A call waits until what runs in the window is back at the prompt (the kernel's
// foreground group, term.Session.Foreground), has gone quiet asking something, or
// the wait has run out; it goes on running after that, and a later call reads on.
//
// A password is never the model's. When the window goes quiet on a prompt for one,
// the call says so to the model and to the page, which asks the person for it and
// types it straight into the window (POST /assistant/chats/{chat}/terminal): it is
// in no request to the provider, no saved line and no audit entry. A program that
// reads one turns the terminal's echo off, so it is not in what the window prints
// either.

// agentTerminalKept bounds what a window printed that no call has read yet: its
// end is kept.
const agentTerminalKept = 256 << 10

// agentTerminalModel bounds how much of what was printed one answer sends the
// model, and agentTerminalPage how much the page draws under the call.
const (
	agentTerminalModel = 16_000
	agentTerminalPage  = 4000
)

var errNoChatTerminal = errors.New("this chat has no terminal window yet")

// agentTerminals are the chats' windows, by chat id.
type agentTerminals struct {
	mu     sync.Mutex
	byChat map[string]*agentTerminal
}

func newAgentTerminals() *agentTerminals {
	return &agentTerminals{byChat: map[string]*agentTerminal{}}
}

// agentTerminal is one chat's window and what it printed that is unread.
type agentTerminal struct {
	session *term.Session
	mu      sync.Mutex
	unread  []byte
	dropped int
	// recent is the end of everything it printed, read or not, for what it last
	// asked.
	recent  []byte
	lastOut time.Time
	// arrived is closed and replaced whenever something arrives.
	arrived chan struct{}
	ended   bool
}

// agentTerminalWindow names a chat's window, which is how it is found again
// after the dashboard restarted.
func agentTerminalWindow(chat string) string {
	if len(chat) > 12 {
		chat = chat[:12]
	}
	return "assistant-" + chat
}

func (t *agentTerminal) pump(ch <-chan []byte) {
	for chunk := range ch {
		t.mu.Lock()
		t.unread = append(t.unread, chunk...)
		if over := len(t.unread) - agentTerminalKept; over > 0 {
			t.dropped += over
			t.unread = append(t.unread[:0:0], t.unread[over:]...)
		}
		t.recent = append(t.recent, chunk...)
		if over := len(t.recent) - 4096; over > 0 {
			t.recent = append(t.recent[:0:0], t.recent[over:]...)
		}
		t.lastOut = time.Now()
		close(t.arrived)
		t.arrived = make(chan struct{})
		t.mu.Unlock()
	}
	t.mu.Lock()
	t.ended = true
	close(t.arrived)
	t.arrived = make(chan struct{})
	t.mu.Unlock()
}

// take is what arrived since the last take, and how much of it was left out.
func (t *agentTerminal) take() ([]byte, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	raw, dropped := t.unread, t.dropped
	t.unread, t.dropped = nil, 0
	return raw, dropped
}

// lastLine is the last line the window printed that has words in it.
func (t *agentTerminal) lastLine() string {
	t.mu.Lock()
	recent := append([]byte(nil), t.recent...)
	t.mu.Unlock()
	lines := strings.Split(terminalText(recent), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return clipText(line, 200)
		}
	}
	return ""
}

func (t *agentTerminal) watch() (<-chan struct{}, time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.arrived, t.lastOut, t.ended
}

// What a window is doing when a call answers.
const (
	terminalFinished = "finished"
	terminalRunning  = "running"
	terminalPassword = "password"
	terminalAsking   = "input"
	terminalClosed   = "closed"
)

var (
	// passwordPrompt is a last line asking for something a person keeps to
	// themselves.
	passwordPrompt = regexp.MustCompile(`(?i)(pass ?word|pass ?phrase|passcode|\bpin\b)[^\n]{0,80}[:?]\s*$`)
	// askingPrompt is a last line asking a question.
	askingPrompt = regexp.MustCompile(`(?i)(\[[yn]/[yn]\]|\(y(es)?/n(o)?\)|\[y(es)?/n(o)?\]|[?:>\]]\s*$)`)
)

// await waits for the window: back at the prompt, quiet on a question, or out of
// time.
func (t *agentTerminal) await(ctx context.Context, wait time.Duration) (state, process string) {
	start := time.Now()
	deadline := start.Add(wait)
	for {
		arrived, last, ended := t.watch()
		if ended {
			return terminalClosed, ""
		}
		now := time.Now()
		if last.Before(start) {
			last = start
		}
		quiet := now.Sub(last)
		busy, process := t.session.Foreground()
		switch {
		case now.Sub(start) > 500*time.Millisecond && !busy && quiet > 300*time.Millisecond:
			return terminalFinished, ""
		case busy && quiet > 1200*time.Millisecond && passwordPrompt.MatchString(t.lastLine()):
			return terminalPassword, process
		case busy && quiet > 2500*time.Millisecond && askingPrompt.MatchString(t.lastLine()):
			return terminalAsking, process
		case !now.Before(deadline):
			if busy {
				return terminalRunning, process
			}
			return terminalFinished, ""
		}
		select {
		case <-ctx.Done():
			return terminalRunning, process
		case <-arrived:
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// chatTerminal is a chat's window: the one it has, the one found again after a
// restart, or — when open is set — a new one, opened as the person.
func (s *Server) chatTerminal(ctx context.Context, p *httpx.Principal, chat, title string, open bool) (*agentTerminal, error) {
	m := s.modules.term
	if m == nil || !m.Enabled() {
		return nil, term.ErrDisabled
	}
	terminals := s.assistant.terminals
	terminals.mu.Lock()
	defer terminals.mu.Unlock()
	if t, ok := terminals.byChat[chat]; ok {
		if _, _, ended := t.watch(); !ended {
			return t, nil
		}
		delete(terminals.byChat, chat)
	}
	name := agentTerminalWindow(chat)
	var session *term.Session
	for _, sess := range m.List() {
		if sess.Owner == p.Username() && sess.WindowName == name {
			session = sess
		}
	}
	fresh := false
	if session == nil {
		if !open {
			return nil, errNoChatTerminal
		}
		created, err := m.Create(ctx, term.CreateOptions{
			Title: clipText("Assistant: "+title, 60), Owner: p.Username(), Rows: 40, Cols: 160, WindowName: name,
		})
		if err != nil {
			return nil, err
		}
		session, fresh = created, true
	}
	_, id, ch, err := session.Subscribe()
	if err != nil {
		return nil, errNoChatTerminal
	}
	t := &agentTerminal{session: session, arrived: make(chan struct{})}
	go func() {
		t.pump(ch)
		session.Unsubscribe(id)
	}()
	terminals.byChat[chat] = t
	if fresh {
		// A new shell prints its prompt before it reads a line; what is typed
		// before that is still read, but the answer would begin with the prompt's
		// own codes. A second is plenty, and it is waited for once.
		ready, cancel := context.WithTimeout(ctx, 3*time.Second)
		for {
			arrived, last, _ := t.watch()
			if !last.IsZero() && time.Since(last) > 400*time.Millisecond {
				break
			}
			select {
			case <-ready.Done():
			case <-arrived:
				continue
			case <-time.After(100 * time.Millisecond):
				continue
			}
			break
		}
		cancel()
		t.take()
	}
	return t, nil
}

// agentTerminalView is what the page is told of the window under a call.
type agentTerminalView struct {
	// Session is the window's workspace, which /terminal?session= opens.
	Session string `json:"session"`
	State   string `json:"state"`
	// Prompt is what it asked, for a password or an answer.
	Prompt string `json:"prompt,omitempty"`
}

// terminal makes one call of the terminal's tools.
func (pr *agentPress) terminal(ctx context.Context, p *httpx.Principal, call assistant.ToolCall) agentResult {
	s := pr.s
	in := call.TerminalInput()
	refuse := func(why string) agentResult { return agentResult{text: why, said: clipText(why, 300)} }
	if !p.Can(auth.CapTerminal) {
		return refuse(fmt.Sprintf("The terminal takes the terminal capability (%s), which the person's role does not have, so nothing was typed. Use a route through api_request instead.", auth.CapTerminal))
	}
	title := ""
	if pr.c.Chat != nil {
		title = pr.c.Chat.Title
	}
	t, err := s.chatTerminal(ctx, p, pr.c.ID, title, call.Name == assistant.ToolTerminalRun)
	switch {
	case errors.Is(err, errNoChatTerminal):
		return refuse("This chat has no terminal window open: terminal_run opens one with the first command.")
	case errors.Is(err, term.ErrDisabled):
		return refuse("The terminal is turned off on this install, so no window can be opened. Use a route through api_request instead.")
	case err != nil:
		return refuse("No terminal window could be opened here (" + err.Error() + "). run_command runs a command that finishes on its own and asks nothing.")
	}
	masker, err := pr.masker(ctx)
	if err != nil {
		return refuse("The secret values to mask could not be read, so nothing was typed.")
	}
	switch call.Name {
	case assistant.ToolTerminalRun:
		// What it printed before this command was not asked about.
		t.take()
		pr.commands = append(pr.commands, clipText("terminal: "+agentMaskText(in.Command, masker), agentCommandAudit))
		if _, err := t.session.Write([]byte(in.Command + "\r")); err != nil {
			return refuse("The window has closed; terminal_run opens a new one.")
		}
	case assistant.ToolTerminalSend:
		if strings.Contains(in.Text, "[REDACTED]") {
			return refuse("[REDACTED] is where a secret was masked, not the secret. Nothing was typed: a password or key is the person's to type, on the page.")
		}
		typed := agentMaskText(in.Text, masker)
		if len(in.Keys) > 0 {
			typed += " [" + strings.Join(in.Keys, " ") + "]"
		}
		pr.commands = append(pr.commands, clipText("typed: "+typed, agentCommandAudit))
		if _, err := t.session.Write([]byte(in.Typed())); err != nil {
			return refuse("The window has closed; terminal_run opens a new one.")
		}
	}
	state, process := t.await(ctx, time.Duration(in.Wait)*time.Second)
	raw, dropped := t.take()
	printed := agentMaskText(terminalText(raw), masker)
	view := &agentTerminalView{Session: t.session.WorkspaceID, State: state}
	var head strings.Builder
	said := ""
	switch state {
	case terminalFinished:
		said = "finished"
		head.WriteString("It finished: the window is back at the prompt.")
	case terminalRunning:
		said = "still running"
		name := process
		if name == "" {
			name = "the command"
		}
		fmt.Fprintf(&head, "%s is still running after %d seconds and goes on in the window. Read on with terminal_read, or stop it with terminal_send and keys [\"ctrl-c\"].", name, in.Wait)
	case terminalPassword:
		said = "waiting for your password"
		view.Prompt = t.lastLine()
		fmt.Fprintf(&head, "It is waiting for a password (%q). Do not type it and do not ask for it in the chat: the page has asked the person, and what they type goes into the window without reaching you. Say in one short line that it is waiting for their password, then call terminal_read with a long wait.", view.Prompt)
	case terminalAsking:
		said = "waiting for an answer"
		view.Prompt = t.lastLine()
		fmt.Fprintf(&head, "It has gone quiet with %q as its last line, so it may be asking something. Answer with terminal_send, or read on with terminal_read if it is only slow.", view.Prompt)
	case terminalClosed:
		said = "window closed"
		head.WriteString("The window has closed: its shell exited. terminal_run opens a new one.")
	}
	if dropped > 0 {
		fmt.Fprintf(&head, " The first %d bytes it printed since your last look were left out; this is its end.", dropped)
	}
	text := head.String()
	if strings.TrimSpace(printed) != "" {
		model := printed
		if len(model) > agentTerminalModel {
			model = tailText(model, agentTerminalModel)
		}
		text += "\nIt printed:\n" + model
	} else if call.Name != assistant.ToolTerminalRead || state != terminalFinished {
		text += " It printed nothing new."
	}
	return agentResult{text: text, said: said, ok: state != terminalClosed, output: tailText(printed, agentTerminalPage), terminal: view}
}

var (
	// terminalOSC is an operating system command: a title, a link, a prompt mark.
	terminalOSC = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)
	// terminalCSI is a control sequence: colour, cursor, erase, mode.
	terminalCSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	// terminalESC is any other escape: a charset, a keypad mode, a save.
	terminalESC = regexp.MustCompile(`\x1b[()*+][0-9A-Za-z]|\x1b[@-Z\\-_=>78]`)
)

// terminalText is what a terminal printed as a person reads it: its codes taken
// out, each line as a carriage return last left it (a progress bar is its final
// state), backspaces applied, and long runs of blank lines closed up.
func terminalText(raw []byte) string {
	text := string(raw)
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "?")
	}
	text = terminalOSC.ReplaceAllString(text, "")
	text = terminalCSI.ReplaceAllString(text, "")
	text = terminalESC.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		if at := strings.LastIndex(strings.TrimRight(line, "\r"), "\r"); at >= 0 {
			line = line[at+1:]
		}
		line = strings.TrimRight(line, "\r")
		var b strings.Builder
		for _, r := range line {
			switch {
			case r == '\b':
				s := []rune(b.String())
				if len(s) > 0 {
					b.Reset()
					b.WriteString(string(s[:len(s)-1]))
				}
			case r == '\t' || r >= 0x20 && r != 0x7f:
				b.WriteRune(r)
			}
		}
		line = strings.TrimRight(b.String(), " ")
		if line == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

// handleAssistantTerminalInput types what the person writes into their chat's
// window: a password a command asked for, or any answer they would rather give
// themselves. It is theirs: it goes to the window and nowhere else — not to the
// provider, not into the chat, and into the audit entry only as how much.
func (s *Server) handleAssistantTerminalInput(w http.ResponseWriter, r *http.Request) error {
	chat, err := s.chatFor(r)
	if err != nil {
		return err
	}
	var in struct {
		Text  string `json:"text"`
		Enter *bool  `json:"enter"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	enter := in.Enter == nil || *in.Enter
	httpx.SetAudit(r, "assistant.terminal.input", chat.ID[:8], map[string]any{"bytes": len(in.Text), "enter": enter})
	if len(in.Text) > assistant.TerminalTextMax || strings.ContainsRune(in.Text, 0) || !utf8.ValidString(in.Text) {
		return httpx.BadRequest("what is typed is at most %d characters of text", assistant.TerminalTextMax)
	}
	t, err := s.chatTerminal(r.Context(), httpx.MustPrincipal(r), chat.ID, chat.Title, false)
	switch {
	case errors.Is(err, errNoChatTerminal):
		return httpx.Err(http.StatusNotFound, "no_terminal", "this chat has no terminal window open")
	case errors.Is(err, term.ErrDisabled):
		return httpx.Err(http.StatusServiceUnavailable, "terminal_disabled", err.Error())
	case err != nil:
		return httpx.Internal(err)
	}
	typed := in.Text
	if enter {
		typed += "\r"
	}
	if _, err := t.session.Write([]byte(typed)); err != nil {
		return httpx.Err(http.StatusNotFound, "no_terminal", "this chat's terminal window has closed")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// mountAssistantTerminalRoutes is called inside /assistant/chats.
func (s *Server) mountAssistantTerminalRoutes(r chi.Router) {
	r.With(httpx.RequireCapability(auth.CapTerminal), httpx.RequireSession).
		Method(http.MethodPost, "/{chat}/terminal", s.handle(s.handleAssistantTerminalInput))
}
