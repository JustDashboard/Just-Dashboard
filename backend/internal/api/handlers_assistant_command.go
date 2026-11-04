package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/assistant"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
)

// run_command, as the assistant's press makes it: the terminal's own way of
// running a line (term.Manager.RunCommand), under what opening the terminal asks
// of a role — the terminal capability — so it reaches no further than the person
// typing it would. Whether it waits for their Allow is the mode's, by what the
// line does (assistant.ClassifyCommand). Its output is masked as a route's
// answer is before the model or the page is sent it, and the press's audit entry
// lists each line it ran, masked.

// agentCommandAudit bounds how much of a line the audit entry keeps.
const agentCommandAudit = 300

// runCommand runs one line on the host, as the person.
func (pr *agentPress) runCommand(ctx context.Context, p *httpx.Principal, in assistant.CommandInput) agentResult {
	s := pr.s
	refuse := func(why string) agentResult { return agentResult{text: why, said: clipText(why, 300)} }
	switch {
	case in.Command == "":
		return refuse("There was no command to run.")
	case !p.Can(auth.CapTerminal):
		return refuse(fmt.Sprintf("Running a command on the server takes what the terminal takes (%s), which the person's role does not have, so nothing was run. Use a route through api_request instead.", auth.CapTerminal))
	case s.modules.term == nil || !s.modules.term.Enabled():
		return refuse("The terminal is turned off on this install, and a command runs the way the terminal would, so nothing was run. Use a route through api_request instead.")
	}
	masker, err := pr.masker(ctx)
	if err != nil {
		return refuse("The secret values to mask could not be read, so nothing was run.")
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = assistant.CommandWaitDefault
	}
	pr.commands = append(pr.commands, clipText(agentMaskText(strings.Join(strings.Fields(in.Command), " "), masker), agentCommandAudit))
	result, err := s.modules.term.RunCommand(ctx, in.Command, time.Duration(timeout)*time.Second)
	switch {
	case errors.Is(err, term.ErrDisabled):
		return refuse("The terminal is turned off on this install, so nothing was run.")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return refuse("The command was stopped with the answer, before it finished.")
	case err != nil:
		return refuse("The command could not be started: " + agentMaskText(err.Error(), masker))
	}
	output := agentMaskText(result.Output, masker)
	if len(output) > maxOperatorResult {
		cut := len(output) - maxOperatorResult
		for cut < len(output) && !utf8Start(output[cut]) {
			cut++
		}
		result.Cut += cut
		output = output[cut:]
	}
	said := fmt.Sprintf("exit %d", result.ExitCode)
	var head strings.Builder
	switch {
	case result.TimedOut:
		said = fmt.Sprintf("stopped after %ds", timeout)
		fmt.Fprintf(&head, "It was still running after %d seconds and was stopped, with everything it started. ", timeout)
	default:
		fmt.Fprintf(&head, "Exit code %d. ", result.ExitCode)
	}
	fmt.Fprintf(&head, "Ran as %s in %s.", result.User, result.Duration.Round(10*time.Millisecond))
	if result.Cut > 0 {
		fmt.Fprintf(&head, " The first %d bytes of its output were left out; this is its end.", result.Cut)
	}
	if strings.TrimSpace(output) == "" {
		head.WriteString(" It wrote nothing.")
	}
	text := head.String()
	if strings.TrimSpace(output) != "" {
		text += "\n" + output
	}
	return agentResult{
		text: text, said: said, ok: !result.TimedOut && result.ExitCode == 0,
		output: tailText(output, agentOutputBytes),
	}
}

// commandChange is a shell line that changed something, said as a change: the
// chat's list of what it did names it, with the way to where it shows — its
// terminal window, or, for a line run to its end, the audit entry that lists it.
// A line in the terminal that has not finished has not changed anything yet that
// can be said.
func commandChange(result agentResult, view agentCallView) agentResult {
	if !result.ok || result.change != nil || (view.Kind != agentKindChange && view.Kind != agentKindDestructive) {
		return result
	}
	if result.terminal != nil {
		if result.terminal.State != terminalFinished {
			return result
		}
		result.change = &agentChange{Label: "Terminal", Href: "/terminal?session=" + url.QueryEscape(result.terminal.Session)}
		return result
	}
	result.change = &agentChange{Label: "Audit log", Href: "/audit"}
	return result
}

// tailText is the end of a text, at most n bytes of it.
func tailText(text string, n int) string {
	if len(text) <= n {
		return text
	}
	cut := len(text) - n
	for cut < len(text) && !utf8Start(text[cut]) {
		cut++
	}
	return "…" + text[cut:]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
