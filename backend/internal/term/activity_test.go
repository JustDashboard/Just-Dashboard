package term

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// The name shown for a running job is the one the operator typed, not the
// interpreter that happens to be running it.
func TestCommandNameLooksThroughInterpretersAndWrappers(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"vim", "notes.md"}, "vim"},
		{[]string{"/usr/bin/htop"}, "htop"},
		{[]string{"-bash"}, "bash"},
		{[]string{"node", "/usr/local/bin/claude"}, "claude"},
		{[]string{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"}, "claude-code"},
		{[]string{"node", "/usr/lib/node_modules/@openai/codex/bin/codex.js"}, "codex"},
		{[]string{"python3", "manage.py", "runserver"}, "manage"},
		{[]string{"python3", "-m", "http.server", "8000"}, "http.server"},
		{[]string{"/usr/bin/env", "node", "app.js"}, "app"},
		{[]string{"sudo", "-u", "www-data", "php", "artisan", "queue:work"}, "artisan"},
		{[]string{"sudo", "apt", "install", "jq"}, "apt"},
		{[]string{"timeout", "10", "npm", "run", "build"}, "npm"},
		{[]string{"bash", "-c", "npm run dev"}, "npm"},
		{[]string{"bash", "./deploy.sh", "--prod"}, "deploy"},
		{[]string{"sudo", "-i"}, "sudo"},
		{[]string{"go", "build", "./..."}, "go"},
	}
	for _, c := range cases {
		if got := commandName(c.argv); got != c.want {
			t.Errorf("commandName(%q) = %q, want %q", c.argv, got, c.want)
		}
	}
	if got := commandName([]string{strings.Repeat("x", 100)}); len(got) != 40 {
		t.Errorf("a long name was not capped: %d bytes", len(got))
	}
}

// Idle is an interactive shell with nothing to run. A shell handed a script or
// a command string is a program like any other.
func TestAtPromptRecognisesAnIdleShell(t *testing.T) {
	idle := [][]string{
		{"-bash"},
		{"bash", "--rcfile", "/tmp/jd/.shell/bashrc", "-i"},
		{"/usr/bin/zsh", "-i"},
		{"zsh", "-o", "vi", "-i"},
		{"bash"},
		{"fish", "-l"},
	}
	for _, argv := range idle {
		if !atPrompt(argv) {
			t.Errorf("atPrompt(%q) = false, want true", argv)
		}
	}
	busy := [][]string{
		{"bash", "deploy.sh"},
		{"bash", "-c", "sleep 100"},
		{"sh", "--", "run.sh"},
		{"vim"},
		{"node"},
		{},
	}
	for _, argv := range busy {
		if atPrompt(argv) {
			t.Errorf("atPrompt(%q) = true, want false", argv)
		}
	}
}

// Titles arrive as OSC 0 or OSC 2, end in BEL or ST, and are cut anywhere by
// the read buffer. Other OSC sequences and stray escapes must not confuse it.
func TestOSCScannerFindsTitlesAcrossChunks(t *testing.T) {
	var scanner oscScanner
	var got []string
	feed := func(parts ...string) {
		for _, p := range parts {
			got = append(got, scanner.feed([]byte(p))...)
		}
	}
	feed("plain text \x1b[32mgreen\x1b[0m")
	feed("\x1b]0;first title\x07")
	feed("\x1b]2;sec", "ond\x1b", "\\after")
	feed("\x1b]8;;https://example.com\x07link\x1b]8;;\x07")
	feed("\x1b]11;?\x07")
	feed("\x1b]0;~/pro", "jects", "\x07tail")
	// A cut-short sequence: the ESC that opens the next one ends it.
	feed("\x1b]0;unfinished\x1b]2;third\x07")
	want := []string{"first title", "second", "~/projects", "third"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("titles = %q, want %q", got, want)
	}

	// A runaway title is dropped rather than buffered without bound.
	var runaway oscScanner
	long := "\x1b]0;" + strings.Repeat("x", maxTitleBytes*3) + "\x07\x1b]0;ok\x07"
	if titles := runaway.feed([]byte(long)); len(titles) != 1 || titles[0] != "ok" {
		t.Fatalf("after an oversized title got %q, want [ok]", titles)
	}
}

func TestFreeNameTakesTheLowestNumber(t *testing.T) {
	if got := freeName("Terminal", map[string]bool{}); got != "Terminal" {
		t.Errorf("empty: %q", got)
	}
	taken := map[string]bool{"Terminal": true, "Terminal 2": true, "Terminal 4": true}
	if got := freeName("Terminal", taken); got != "Terminal 3" {
		t.Errorf("with 1, 2 and 4 taken: %q", got)
	}
}

// The whole path against a real shell on a real PTY: a command that is over
// in a blink is never announced; a long one is announced under its own name
// but not marked working while it does nothing; a title set at the prompt
// counts while idle and gives way while a job that set none runs; sustained
// output and silent CPU both count as work; the end of work is stamped; and
// every change reaches a subscriber as an event.
func TestActivityFollowsTheForegroundJob(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip(err)
	}
	cmd := exec.Command(bash, "--norc", "--noprofile", "-i")
	cmd.Env = append(os.Environ(), "PS1=$ ", "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	sess := &Session{
		ID: "test", PID: cmd.Process.Pid, pty: f, cmd: cmd,
		subscribers: map[int64]chan []byte{}, events: map[int64]chan Activity{},
		scrollback: newRingBuffer(4096),
	}
	go sess.readLoop(func() {})
	defer sess.Close()

	_, id, out, err := sess.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range out {
		}
	}()
	events := sess.Events(id)

	until := func(what string, within time.Duration, ok func(Activity) bool) Activity {
		t.Helper()
		deadline := time.Now().Add(within)
		for {
			a := sess.Activity()
			if ok(a) {
				return a
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: last activity %+v", what, a)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	type_ := func(s string) {
		t.Helper()
		if _, err := sess.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	drain := func(for_ time.Duration) []Activity {
		var got []Activity
		deadline := time.After(for_)
		for {
			select {
			case a := <-events:
				got = append(got, a)
			case <-deadline:
				return got
			}
		}
	}

	until("shell at its prompt", 5*time.Second, func(a Activity) bool { return !a.Busy })
	type_("printf '\\e]2;hello world\\a'\n")
	until("title set at the prompt", 5*time.Second, func(a Activity) bool {
		return a.Title == "hello world" && !a.Busy
	})
	drain(200 * time.Millisecond)

	// A command over in a blink is never announced: nothing flashes.
	type_("true\n")
	for _, a := range drain(1400 * time.Millisecond) {
		if a.Busy || a.Working || a.FinishedAt != 0 {
			t.Errorf("a momentary command was announced: %+v", a)
		}
	}

	// A long, silent command is announced after the hold-off, under its own
	// name, and is not working: nothing is happening.
	type_("sleep 30\n")
	busy := until("sleep announced", 5*time.Second, func(a Activity) bool { return a.Busy })
	if busy.Process != "sleep" || busy.Title != "" || busy.Working {
		t.Errorf("sleep = %+v, want busy sleep, no title, not working", busy)
	}
	time.Sleep(1200 * time.Millisecond)
	if a := sess.Activity(); a.Working {
		t.Errorf("sleep was marked working: %+v", a)
	}
	type_("\x03")
	idle := until("prompt back after Ctrl+C", 5*time.Second, func(a Activity) bool { return !a.Busy })
	if idle.Title != "hello world" || idle.FinishedAt != 0 {
		t.Errorf("after sleep: %+v, want the prompt's title and nothing finished", idle)
	}
	drain(200 * time.Millisecond)

	// An idle program's odd redraw is not work — Claude Code at its prompt
	// repaints on a focus change or a resize. Bursts a second and a half
	// apart, while it holds the terminal, never become a run.
	type_("sh -c 'while :; do sleep 1.5; printf x; done'\n")
	until("redrawing program announced", 5*time.Second, func(a Activity) bool { return a.Busy })
	for _, a := range drain(4 * time.Second) {
		if a.Working {
			t.Errorf("occasional redraws were marked working: %+v", a)
		}
	}
	if a := sess.Activity(); a.Working {
		t.Errorf("occasional redraws were marked working: %+v", a)
	}
	type_("\x03")
	until("prompt back after the redraws", 5*time.Second, func(a Activity) bool { return !a.Busy })
	drain(200 * time.Millisecond)

	// Sustained output is work; its end is stamped and reaches the subscriber.
	type_("sh -c 'while :; do echo tick; sleep 0.1; done'\n")
	until("output counted as work", 8*time.Second, func(a Activity) bool { return a.Working })
	type_("\x03")
	done := until("prompt back after the loop", 5*time.Second, func(a Activity) bool { return !a.Busy })
	if done.Working || done.FinishedAt == 0 {
		t.Errorf("after the loop: %+v, want finished", done)
	}
	seenWorking, seenFinished := false, false
	for _, a := range drain(300 * time.Millisecond) {
		seenWorking = seenWorking || a.Working
		seenFinished = seenFinished || (!a.Working && a.FinishedAt != 0)
	}
	if !seenWorking || !seenFinished {
		t.Errorf("subscriber saw working=%v finished=%v, want both", seenWorking, seenFinished)
	}

	// CPU with no output is work too: a build that prints nothing.
	type_("sh -c 'i=0; while [ $i -lt 30000000 ]; do i=$((i+1)); done'\n")
	cpu := until("silent CPU counted as work", 10*time.Second, func(a Activity) bool { return a.Working })
	if !cpu.Busy {
		t.Errorf("working without busy: %+v", cpu)
	}
	type_("\x03")
	until("prompt back after the CPU loop", 5*time.Second, func(a Activity) bool { return !a.Busy })
	type_("exit\n")
}
