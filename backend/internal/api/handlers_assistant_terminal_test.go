package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
)

// A chat's terminal over HTTP, against the fake provider, with a real window held
// the way the Terminal page's are: a password asked for and typed by the person
// without reaching the model, a question answered, a long command followed and
// stopped, and the window found again after the dashboard forgot it.

// withHeldTerminal gives the fixture a terminal that opens real held windows as
// this test's own account, in a shell that reads no profile of the developer's.
func withHeldTerminal(t *testing.T, f *assistantFixture) *term.Manager {
	t.Helper()
	me, err := user.Current()
	if err != nil {
		t.Skip("no current account")
	}
	home := t.TempDir()
	shell := filepath.Join(home, "bash")
	wrapper := "#!/bin/sh\nexport HOME=" + evidenceShellQuote(home) + "\nexec /bin/bash \"$@\"\n"
	if err := os.WriteFile(shell, []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	m := term.NewManager(true, shell, me.Username)
	m.SetClipboardRootForTest(t.TempDir())
	if err := m.SetupShell(); err != nil {
		t.Fatal(err)
	}
	holders, err := os.MkdirTemp("", "jd-agent-term-")
	if err != nil {
		t.Fatal(err)
	}
	launched := &terminalHolders{}
	m.SetHolderLauncherForTest(holders, launched.launch)
	f.s.modules.term = m
	t.Cleanup(func() {
		for _, sess := range m.List() {
			m.Kill(context.Background(), sess.ID)
		}
		launched.wait(t)
		os.RemoveAll(holders)
	})
	return m
}

func terminalCall(name string, input map[string]any) [2]any { return [2]any{name, input} }

func calledTerminal(t *testing.T, events []map[string]any) map[string]any {
	t.Helper()
	called := ofType(events, "called")
	if len(called) != 1 {
		t.Fatalf("called = %v", events)
	}
	view, _ := called[0]["terminal"].(map[string]any)
	if view == nil {
		t.Fatalf("no terminal on %v", called[0])
	}
	return called[0]
}

// A password is the person's: the window goes quiet on its prompt, the model and
// the page are told, the person types it through the chat's route, and the model
// reads on without ever being sent it.
func TestAPasswordGoesFromThePersonToTheTerminalAlone(t *testing.T) {
	f := databaseAssistantAs(t, auth.RoleAdmin)
	m := withHeldTerminal(t, f)
	const secret = "hunter2-typed-by-the-person"
	f.provider.queue(
		claudeCalls("Running it.", terminalCall(assistant.ToolTerminalRun, map[string]any{
			"command": `bash -c 'read -s -p "Password: " pw; echo; echo "length ${#pw}"'`, "wait": 15, "reason": "Running what asks for it.",
		})),
		claudeAnswer("It is waiting for your password."),
	)
	events := lines(t, f.do(http.MethodPost, "/assistant/chat", `{"question":"Run it","mode":"full"}`))
	called := calledTerminal(t, events)
	view := called["terminal"].(map[string]any)
	if view["state"] != "password" || view["prompt"] != "Password:" || called["said"] != "waiting for your password" {
		t.Fatalf("called = %v", called)
	}
	chat := conversationOf(events)
	var window *term.Session
	for _, sess := range m.List() {
		if sess.WindowName == agentTerminalWindow(chat) {
			window = sess
		}
	}
	if window == nil || window.Owner != "operator" || view["session"] != window.WorkspaceID {
		t.Fatalf("the chat's window = %+v, view = %v", window, view)
	}

	rec := f.do(http.MethodPost, "/assistant/chats/"+chat+"/terminal", fmt.Sprintf(`{"text":%q}`, secret))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("typing it = %d %s", rec.Code, rec.Body.String())
	}
	detail, _ := f.audit(t, "assistant.terminal.input")
	if strings.Contains(detail, secret) || !strings.Contains(detail, fmt.Sprintf(`"bytes":%d`, len(secret))) {
		t.Fatalf("audit = %s", detail)
	}

	f.provider.queue(
		claudeCalls("Reading on.", terminalCall(assistant.ToolTerminalRead, map[string]any{"wait": 10})),
		claudeAnswer("Done."),
	)
	events = lines(t, f.do(http.MethodPost, "/assistant/chat", fmt.Sprintf(`{"conversation":%q,"question":"I typed it","mode":"full"}`, chat)))
	called = calledTerminal(t, events)
	if called["terminal"].(map[string]any)["state"] != "finished" || !strings.Contains(fmt.Sprint(called["output"]), fmt.Sprintf("length %d", len(secret))) {
		t.Fatalf("read on = %v", called)
	}
	for i, sent := range f.provider.raw {
		if strings.Contains(sent, secret) {
			t.Fatalf("request %d to the provider carried the password", i)
		}
	}
	if rec := f.do(http.MethodGet, "/assistant/chats/"+chat, ""); strings.Contains(rec.Body.String(), secret) {
		t.Fatal("the saved chat holds the password")
	}
}

// A question is the model's to answer, a long command is followed and stopped, and
// what the window printed is read without its codes.
func TestTheTerminalAnswersAQuestionAndStopsALongCommand(t *testing.T) {
	f := databaseAssistantAs(t, auth.RoleAdmin)
	withHeldTerminal(t, f)
	f.provider.queue(
		claudeCalls("Asking.", terminalCall(assistant.ToolTerminalRun, map[string]any{
			"command": `bash -c 'printf "\033[1;32mready\033[0m\n"; read -p "Proceed? [y/N] " a; echo "answer-$a"'`, "wait": 15, "reason": "x",
		})),
		claudeCalls("Yes.", terminalCall(assistant.ToolTerminalSend, map[string]any{"text": "y", "wait": 10, "reason": "Saying yes."})),
		claudeCalls("Long.", terminalCall(assistant.ToolTerminalRun, map[string]any{"command": `bash -c 'echo started-mark; sleep 60'`, "wait": 2, "reason": "x"})),
		claudeCalls("Stopping.", terminalCall(assistant.ToolTerminalSend, map[string]any{"keys": []string{"ctrl-c"}, "wait": 10, "reason": "Stopping it."})),
		claudeAnswer("Done."),
	)
	events := lines(t, f.do(http.MethodPost, "/assistant/chat", `{"question":"Go","mode":"full"}`))
	called := ofType(events, "called")
	if len(called) != 4 {
		t.Fatalf("called = %v", called)
	}
	state := func(i int) string { return fmt.Sprint(called[i]["terminal"].(map[string]any)["state"]) }
	if state(0) != "input" || !strings.Contains(fmt.Sprint(called[0]["output"]), "ready\nProceed? [y/N]") || strings.Contains(fmt.Sprint(called[0]["output"]), "\x1b") {
		t.Fatalf("the question = %q (%s)", called[0]["output"], state(0))
	}
	if state(1) != "finished" || !strings.Contains(fmt.Sprint(called[1]["output"]), "answer-y") {
		t.Fatalf("the answer = %v", called[1])
	}
	if state(2) != "running" || !strings.Contains(fmt.Sprint(called[2]["output"]), "started-mark") || called[2]["said"] != "still running" {
		t.Fatalf("the long one = %v", called[2])
	}
	if state(3) != "finished" {
		t.Fatalf("stopped = %v", called[3])
	}
	if detail, _ := f.audit(t, "assistant.chat.ask"); !strings.Contains(detail, "terminal: bash -c") || !strings.Contains(detail, "typed: y") || !strings.Contains(detail, "[ctrl-c]") {
		t.Errorf("audit = %s", detail)
	}

	// Forgotten, as after a restart, the window is found again by its name.
	f.s.assistant.terminals = newAgentTerminals()
	f.provider.queue(claudeCalls("Again.", terminalCall(assistant.ToolTerminalRun, map[string]any{"command": "echo found-again", "wait": 5, "reason": "x"})), claudeAnswer("Yes."))
	events = lines(t, f.do(http.MethodPost, "/assistant/chat", fmt.Sprintf(`{"conversation":%q,"question":"Again","mode":"full"}`, conversationOf(events))))
	again := calledTerminal(t, events)
	if !strings.Contains(fmt.Sprint(again["output"]), "found-again") || len(f.s.modules.term.List()) != 1 {
		t.Fatalf("again = %v, windows = %d", again, len(f.s.modules.term.List()))
	}
}

// The terminal takes the terminal's capability; a chat with no window cannot be
// typed into or read; a masked value is never typed; and, in the default mode, a
// command that writes and anything typed wait for the person while a read runs.
func TestTheTerminalIsHeldLikeTheShell(t *testing.T) {
	limited := databaseAssistantAs(t, auth.RoleLimited)
	withHeldTerminal(t, limited)
	limited.provider.queue(claudeCalls("x", terminalCall(assistant.ToolTerminalRun, map[string]any{"command": "echo no", "reason": "x"})), claudeAnswer("No."))
	events := lines(t, limited.do(http.MethodPost, "/assistant/chat", `{"question":"x","mode":"full"}`))
	if called := ofType(events, "called"); len(called) != 1 || !strings.Contains(fmt.Sprint(called[0]["said"]), "terminal capability") {
		t.Fatalf("a role without the terminal = %v", called)
	}
	if rec := limited.do(http.MethodPost, "/assistant/chats/"+conversationOf(events)+"/terminal", `{"text":"x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("typing without the terminal = %d", rec.Code)
	}

	f := databaseAssistantAs(t, auth.RoleAdmin)
	withHeldTerminal(t, f)
	f.provider.queue(
		claudeCalls("x",
			terminalCall(assistant.ToolTerminalRead, map[string]any{"wait": 1}),
			terminalCall(assistant.ToolTerminalSend, map[string]any{"text": "[REDACTED]", "reason": "x"})),
		claudeAnswer("No window."),
	)
	events = lines(t, f.do(http.MethodPost, "/assistant/chat", `{"question":"x","mode":"full"}`))
	called := ofType(events, "called")
	if len(called) != 2 || !strings.Contains(fmt.Sprint(called[0]["said"]), "no terminal window") || called[1]["ok"] != false {
		t.Fatalf("with no window = %v", called)
	}
	if rec := f.do(http.MethodPost, "/assistant/chats/"+conversationOf(events)+"/terminal", `{"text":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("typing into no window = %d", rec.Code)
	}

	f.provider.queue(claudeCalls("x",
		terminalCall(assistant.ToolTerminalRun, map[string]any{"command": "df -h", "reason": "x"}),
		terminalCall(assistant.ToolTerminalRun, map[string]any{"command": "apt-get install -y htop", "reason": "x"}),
		terminalCall(assistant.ToolTerminalSend, map[string]any{"text": "y", "reason": "x"}),
	))
	events = lines(t, f.do(http.MethodPost, "/assistant/chat", `{"question":"x","mode":"changes"}`))
	pending, _ := doneOf(t, events)["pending"].([]any)
	if called := ofType(events, "called"); len(called) != 1 || len(pending) != 2 {
		t.Fatalf("called = %v, pending = %v", called, pending)
	}
}

func TestTerminalTextIsWhatAPersonReads(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{"\x1b]0;~\x07\x1b[36m~/src\x1b[0m\r\n\x1b[36m>\x1b[0m ls\r\n", "~/src\n> ls"},
		{"Downloading  10%\rDownloading  55%\rDownloading 100%\r\ndone\r\n", "Downloading 100%\ndone"},
		{"abc\b\bX\r\n", "aX"},
		{"one\r\n\r\n\r\n\r\ntwo", "one\n\ntwo"},
		{"\x1b(B\x1b[?2004hplain\x1b[?2004l", "plain"},
	} {
		if got := terminalText([]byte(c.raw)); got != c.want {
			t.Errorf("%q = %q, want %q", c.raw, got, c.want)
		}
	}
}
