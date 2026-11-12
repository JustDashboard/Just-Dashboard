package assistant

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// The terminal: a real terminal window of the chat's own, on the host, for what
// run_command cannot do — a command that runs for a long time, asks a question,
// wants a password, or that the person may want to watch. It is a window of the
// Terminal page's, so the person sees what is typed into it and can type into it
// themselves; it keeps its directory and environment between commands, as a shell
// does; and it outlives the press, the chat's page and the dashboard restarting.
//
// terminal_run types a command line and Enter; terminal_send types text or presses
// keys; terminal_read waits for more of what it prints. Each answers with what was
// printed since the last answer, once the command is back at the prompt, has gone
// quiet asking something, or the wait has run out. A password is never the
// model's to type: the page asks the person for it and types it straight into the
// window (api/handlers_assistant_terminal.go).

const (
	ToolTerminalRun  = "terminal_run"
	ToolTerminalSend = "terminal_send"
	ToolTerminalRead = "terminal_read"

	// TerminalWaitDefault and TerminalWaitMax bound how long terminal_run and
	// terminal_send wait for the window before they answer, in seconds;
	// TerminalReadMax bounds terminal_read's.
	TerminalWaitDefault = 20
	TerminalWaitMax     = 120
	TerminalReadDefault = 30
	TerminalReadMax     = 300
	// TerminalTextMax bounds what one terminal_send types.
	TerminalTextMax = 4000
)

// TerminalKeys are the keys terminal_send presses by name, with what each sends.
var TerminalKeys = map[string]string{
	"enter": "\r", "tab": "\t", "escape": "\x1b", "backspace": "\x7f", "space": " ",
	"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
	"ctrl-c": "\x03", "ctrl-d": "\x04", "ctrl-z": "\x1a", "ctrl-l": "\x0c", "ctrl-u": "\x15",
	"y": "y", "n": "n", "q": "q",
}

func terminalKeyNames() []string {
	names := make([]string, 0, len(TerminalKeys))
	for name := range TerminalKeys {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

var (
	terminalRunTool = Tool{
		Name: ToolTerminalRun,
		Description: "Type a command into this chat's own terminal window on the server and press Enter, as the person would at the Terminal page, where they can watch it and type into it too. " +
			"Use it, rather than run_command, for what runs long (an upgrade, a build, a migration), asks a question or a password, or the person may want to watch. " +
			"The window keeps its directory and environment between commands, and the first call opens it. It answers with what the command printed once it is back at the prompt, " +
			"has gone quiet waiting for an answer, or the wait has run out, in which case it is still running: read on with terminal_read. " +
			"Avoid full-screen programs (top, htop, vim, less): use their batch or non-interactive forms." + agentRoleLine + agentHoldLine,
		Schema: operatorSchema([]string{"command", "reason"}, map[string]any{
			"command": map[string]any{"type": "string", "maxLength": CommandMax, "description": "The command line, as typed into a shell."},
			"wait":    map[string]any{"type": "integer", "minimum": 1, "maximum": TerminalWaitMax, "description": fmt.Sprintf("Seconds to wait for it before answering (default %d). It goes on running after.", TerminalWaitDefault)},
			"reason":  map[string]any{"type": "string", "maxLength": operatorReasonMax, "description": "One short sentence the person sees beside the call: what you are doing and why."},
		}),
	}
	terminalSendTool = Tool{
		Name: ToolTerminalSend,
		Description: "Type into this chat's terminal window: an answer to what the program running in it asked (y, a number, a name), with Enter after it unless enter is false, or keys by name — " +
			"ctrl-c stops what is running, up and down move through a menu. Never type a password, passphrase or key: the person types those on the page." + agentRoleLine + agentHoldLine,
		Schema: operatorSchema([]string{"reason"}, map[string]any{
			"text":   map[string]any{"type": "string", "maxLength": TerminalTextMax, "description": "Text to type."},
			"enter":  map[string]any{"type": "boolean", "description": "Press Enter after the text (default true)."},
			"keys":   map[string]any{"type": "array", "maxItems": 20, "items": map[string]any{"type": "string", "enum": terminalKeyNames()}, "description": "Keys to press after the text, in order."},
			"wait":   map[string]any{"type": "integer", "minimum": 1, "maximum": TerminalWaitMax, "description": fmt.Sprintf("Seconds to wait for it before answering (default %d).", TerminalWaitDefault)},
			"reason": map[string]any{"type": "string", "maxLength": operatorReasonMax, "description": "One short sentence the person sees beside the call."},
		}),
	}
	terminalReadTool = Tool{
		Name: ToolTerminalRead,
		Description: "Wait for this chat's terminal window and read what it printed since your last look: until what runs in it is back at the prompt, goes quiet asking something, or the wait runs out. " +
			"Use it to follow a long command, and after the person has been asked for a password, with a long wait, to carry on once they have typed it. It types nothing.",
		Schema: operatorSchema(nil, map[string]any{
			"wait": map[string]any{"type": "integer", "minimum": 1, "maximum": TerminalReadMax, "description": fmt.Sprintf("Seconds to wait at most (default %d).", TerminalReadDefault)},
		}),
	}
)

// TerminalTools are the terminal's three, offered together.
var TerminalTools = []Tool{terminalRunTool, terminalSendTool, terminalReadTool}

// TerminalInput is what the terminal's tools take, as their check read it.
type TerminalInput struct {
	Command string   `json:"command,omitempty"`
	Text    string   `json:"text,omitempty"`
	Enter   bool     `json:"enter,omitempty"`
	Keys    []string `json:"keys,omitempty"`
	Wait    int      `json:"wait"`
	Reason  string   `json:"reason,omitempty"`
}

// TerminalInput reads a terminal call's checked input back.
func (c ToolCall) TerminalInput() TerminalInput {
	var in TerminalInput
	_ = json.Unmarshal(c.Input, &in)
	return in
}

// Typed is what a terminal_send call types, keys and all.
func (in TerminalInput) Typed() string {
	out := in.Text
	if in.Text != "" && in.Enter {
		out += "\r"
	}
	for _, key := range in.Keys {
		out += TerminalKeys[key]
	}
	return out
}

func readTerminalInput(tool string, f *fields) TerminalInput {
	var in TerminalInput
	in.Reason = clip(f.text("reason", "why"), operatorReasonMax)
	switch tool {
	case ToolTerminalRun:
		in.Command = strings.TrimSpace(f.text("command", "cmd", "line", "shell"))
		switch {
		case in.Command == "":
			f.fail("command is required.")
		case len(in.Command) > CommandMax:
			f.fail(fmt.Sprintf("command is %d bytes, and at most %d are typed.", len(in.Command), CommandMax))
			in.Command = ""
		case strings.ContainsAny(in.Command, "\x00\r\n"):
			f.fail("command is one line: run one command at a time, or join them with && or ;.")
			in.Command = ""
		}
		in.Wait = int(f.bounded("wait", 1, TerminalWaitMax, TerminalWaitDefault, "timeout", "seconds"))
	case ToolTerminalSend:
		in.Text = f.text("text", "input", "answer", "value")
		in.Enter = true
		if raw, given := f.take("enter", "newline", "submit"); given != "" {
			in.Enter = strings.TrimSpace(string(raw)) != "false"
		}
		in.Keys = f.list(20, 20, "keys", "key")
		for i, key := range in.Keys {
			key = strings.NewReplacer("_", "-", "+", "-", " ", "-").Replace(strings.ToLower(strings.TrimSpace(key)))
			key = strings.Replace(key, "control-", "ctrl-", 1)
			switch key {
			case "return":
				key = "enter"
			case "esc":
				key = "escape"
			}
			if key == "^c" {
				key = "ctrl-c"
			}
			if _, ok := TerminalKeys[key]; !ok {
				f.fail(fmt.Sprintf("there is no key %q; the keys are %s.", in.Keys[i], strings.Join(terminalKeyNames(), ", ")))
			}
			in.Keys[i] = key
		}
		switch {
		case in.Text == "" && len(in.Keys) == 0:
			f.fail("text or keys is required.")
		case len(in.Text) > TerminalTextMax:
			f.fail(fmt.Sprintf("text is %d bytes, and at most %d are typed.", len(in.Text), TerminalTextMax))
		case strings.ContainsRune(in.Text, 0):
			f.fail("text holds a NUL.")
		}
		in.Wait = int(f.bounded("wait", 1, TerminalWaitMax, TerminalWaitDefault, "timeout", "seconds"))
	case ToolTerminalRead:
		in.Wait = int(f.bounded("wait", 1, TerminalReadMax, TerminalReadDefault, "timeout", "seconds"))
	}
	return in
}

// TerminalKind is how a person's mode reads a terminal call: a command by what it
// does, a read as a read, and anything typed into a running program as a change,
// since an answer to a question is a decision.
func TerminalKind(call ToolCall) string {
	switch call.Name {
	case ToolTerminalRun:
		return TypedKind(call.TerminalInput().Command)
	case ToolTerminalRead:
		return CommandRead
	}
	return CommandChange
}

// TypedKind reads a line that is typed into a terminal window rather than
// handed to a shell whole, as terminal_run's is and as run_command's is when it
// goes to the window: what ClassifyCommand reads it as, except that a tab is a
// key there as well, which has the shell complete the word before it with
// whatever it finds, so a line with one is not known to run as it was written.
func TypedKind(line string) string {
	kind := ClassifyCommand(line)
	if kind == CommandRead && strings.ContainsRune(line, '\t') {
		return CommandChange
	}
	return kind
}
