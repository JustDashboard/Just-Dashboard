package assistant

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// run_command: a shell on the host, as the terminal's account, for what the
// dashboard's routes do not do. The person could type the same line into the
// terminal (it takes the same capability, api.handlers_assistant_command.go), so
// it reaches no further than they do; what it adds is that the assistant types it.
//
// A command's kind decides whether the person's mode lets it run unasked, so it is
// read here, conservatively: a read is a line of only well-known programs that
// only read, with nothing written by a redirection, substituted in, or run in the
// background; a destructive one names a program or flag that removes, stops or
// replaces something; anything else is a change. A read that is misread is asked
// about; nothing that writes is read as a read.
//
// That is kept by listing what a read may be made of, never what it may not: the
// programs, the flags each may be given and the subcommands that only read
// (command_readers.go), the wrappers and the variables it may run under
// (readUnder), and the few redirections that write no file (redirectionEnd).
// Whatever is not listed asks — a flag nobody has heard of, a variable or a
// pattern where an argument decides what the program does, a reader's name under
// a directory that is not the system's. It reads what a line says, not where it
// runs: the directory a cd left the terminal window in, and the configuration a
// program finds there, are the person's to have allowed.

const (
	ToolRunCommand = "run_command"
	// CommandMax bounds a command's text.
	CommandMax = 8000
	// CommandWaitDefault and CommandWaitMax bound how long a command runs, in
	// seconds, before it is stopped.
	CommandWaitDefault = 60
	CommandWaitMax     = 300
)

var runCommandTool = Tool{
	Name: ToolRunCommand,
	Description: "Run a shell command on the server, as the account the dashboard's terminal opens as, the way the person would type it into the terminal. " +
		"Use it for what the dashboard's routes do not do — a program's own status or version, a config file's syntax check, installing something, " +
		"what is listening or filling the disk — and prefer api_request where a route does the job. It is not interactive: nothing can be typed into it, " +
		"so give a program the flags it needs to run without asking (apt-get -y, systemctl --no-pager, git --no-pager). " +
		"Its working directory is the account's home; write cd /path && … for another. It answers with the exit code and the end of its output, secret values masked. " +
		"A line that only reads need not wait for the person when it is written plainly: reading programs with their usual flags spelled out, " +
		"no substitution and no output sent to a file, and no variable or wildcard given to a program whose flags could make it write (find, sort, git, docker). " +
		"What reaches another machine (ping, dig, curl, openssl s_client) waits, as a change does. " +
		"A line that changes packages or restarts Docker, or runs sudo that may ask for a password, is typed into this chat's terminal window instead, as terminal_run would, " +
		"where it outlives the dashboard restarting and can be answered; its answer then says so, and terminal_read follows it." +
		agentRoleLine + agentHoldLine,
	Schema: operatorSchema([]string{"command", "reason"}, map[string]any{
		"command": map[string]any{"type": "string", "maxLength": CommandMax, "description": "The command line, as typed into a shell."},
		"timeout": map[string]any{"type": "integer", "minimum": 1, "maximum": CommandWaitMax, "description": fmt.Sprintf("Seconds before it is stopped (default %d).", CommandWaitDefault)},
		"reason":  map[string]any{"type": "string", "maxLength": operatorReasonMax, "description": "One short sentence the person sees beside the call: what you are doing and why."},
	}),
}

// CommandInput is what run_command takes, as its check read it.
type CommandInput struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// CommandInput reads a run_command call's checked input back.
func (c ToolCall) CommandInput() CommandInput {
	var in CommandInput
	_ = json.Unmarshal(c.Input, &in)
	return in
}

func readCommandInput(f *fields) CommandInput {
	var in CommandInput
	in.Command = strings.TrimSpace(f.text("command", "cmd", "shell", "script", "line"))
	switch {
	case in.Command == "":
		f.fail("command is required.")
	case len(in.Command) > CommandMax:
		f.fail(fmt.Sprintf("command is %d bytes, and at most %d are run.", len(in.Command), CommandMax))
		in.Command = ""
	case strings.ContainsRune(in.Command, 0):
		f.fail("command holds a NUL.")
		in.Command = ""
	}
	in.Timeout = int(f.bounded("timeout", 1, CommandWaitMax, CommandWaitDefault, "seconds", "timeout_seconds", "timeoutSeconds"))
	in.Reason = clip(f.text("reason", "why"), operatorReasonMax)
	// A working directory is a common guess; it is the command's own cd.
	if dir := f.text("cwd", "dir", "directory", "workdir", "working_directory"); dir != "" && in.Command != "" {
		in.Command = "cd " + shellQuote(dir) + " && " + in.Command
		f.note("cwd was read as a cd before the command")
	}
	return in
}

// shellQuote is one word for a POSIX shell, whatever it holds.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Kinds a command is read as, the same words a route's call uses.
const (
	CommandRead        = "read"
	CommandChange      = "change"
	CommandDestructive = "destructive"
)

// destructivePrograms remove, stop, replace or wipe something.
var destructivePrograms = setOf(
	"rm", "rmdir", "shred", "unlink", "dd", "mkfs", "wipefs", "fdisk", "sfdisk", "parted", "sgdisk", "reboot",
	"shutdown", "poweroff", "halt", "kill", "killall", "pkill", "userdel", "groupdel", "deluser", "delgroup",
	"truncate", "init", "telinit",
)

// destructiveSubcommands are the subcommands that do, by the first word that is
// not a flag, and by the second under a first (docker.compose down).
var destructiveSubcommands = map[string][]string{
	"systemctl":        {"stop", "kill", "disable", "mask", "reboot", "poweroff", "halt", "isolate", "rescue", "emergency", "kexec", "soft-reboot"},
	"loginctl":         {"terminate-session", "terminate-user", "terminate-seat", "kill-session", "kill-user"},
	"docker":           {"rm", "rmi", "kill", "stop", "prune"},
	"docker.compose":   {"down", "rm", "kill", "stop"},
	"docker.container": {"rm", "kill", "stop", "prune"},
	"docker.image":     {"rm", "prune"},
	"docker.volume":    {"rm", "prune"},
	"docker.network":   {"rm", "prune"},
	"docker.system":    {"prune"},
	"apt":              {"remove", "purge", "autoremove"},
	"apt-get":          {"remove", "purge", "autoremove"},
	"snap":             {"remove"},
	"git":              {"reset", "clean"},
	"ufw":              {"delete", "reset", "disable"},
	"pm2":              {"delete", "stop", "kill"},
	"service":          {"stop"},
}

func setOf(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// ClassifyCommand reads what a command line does: CommandRead, CommandChange or
// CommandDestructive. The most a part of it does is what the whole does.
func ClassifyCommand(command string) string {
	commands, plain := parseLine(command)
	kind := CommandRead
	if !plain || len(commands) == 0 {
		kind = CommandChange
	}
	for _, c := range commands {
		switch commandKind(c) {
		case CommandDestructive:
			return CommandDestructive
		case CommandChange:
			kind = CommandChange
		}
	}
	return kind
}

// simple is one simple command of a line: its words, and which of them are not
// what was typed. A word that holds a variable, or a pattern the shell matches
// file names against, is whatever those turn out to be when it runs — one word
// or several, a flag as easily as a name — so a command whose arguments decide
// what it does is not read past one.
type simple struct {
	words  []string
	varies []bool
}

// splitCommand cuts a line into its simple commands, each as its words, and
// reports whether the line is plain (parseLine).
func splitCommand(line string) ([][]string, bool) {
	commands, plain := parseLine(line)
	out := make([][]string, len(commands))
	for i, c := range commands {
		out[i] = c.words
	}
	return out, plain
}

// parseLine cuts a line into its simple commands at ; && || | & and new lines,
// and reports whether the line is plain: nothing written by a redirection but
// to /dev/null or another stream, no substitution, no subshell or group, no
// here-document, no background job, no comment and no quote left open. A line
// that is not plain is never a read.
//
// It reads the line as bash and zsh both would, run with -c or typed at a
// prompt, and where those differ it takes the reading that asks: >&word writes a
// file unless the word is a stream's number, $'…' and ${…} can spell or do
// anything, a ! is the history's in a terminal window, and a # begins a comment
// in one and is a word in the other, so what follows it may or may not run.
func parseLine(line string) ([]simple, bool) {
	var (
		commands []simple
		current  simple
		word     strings.Builder
		inWord   bool
		// varies and quoted are of the word in hand: it holds an expansion, and
		// part of it was quoted or escaped.
		varies, quoted bool
		quote          rune
		plain          = true
	)
	endWord := func() {
		if inWord {
			current.words = append(current.words, word.String())
			// A [ alone is test's other name, not the start of a pattern.
			current.varies = append(current.varies, varies && word.String() != "[")
			word.Reset()
		}
		inWord, varies, quoted = false, false, false
	}
	endCommand := func() {
		endWord()
		if len(current.words) > 0 {
			commands = append(commands, current)
		}
		current = simple{}
	}
	runes := []rune(line)
	at := func(i int) rune {
		if i < len(runes) {
			return runes[i]
		}
		return 0
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote == '\'' {
			if r == '\'' {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			continue
		}
		// What the shell expands, it expands inside double quotes too.
		switch r {
		case '$':
			next := at(i + 1)
			switch {
			case next == '(' || next == '{' || next == '[':
				// A substitution runs something else; ${…} and $[…] can assign,
				// evaluate and, in zsh, run.
				plain = false
			case quote == 0 && (next == '\'' || next == '"'):
				// $'\x2d…' spells any character, a flag's dash among them.
				plain = false
			case nameRune(next) || strings.ContainsRune("@*#?$!-~=^+", next):
				// A variable, a special one, or zsh's $~name, $=name, $^name and $+name.
				varies = true
				end := i + 1
				for nameRune(at(end)) {
					end++
				}
				if at(end) == '[' {
					// zsh reads a subscript, which it evaluates.
					plain = false
				}
			}
			word.WriteRune(r)
			inWord = true
			continue
		case '`':
			plain = false
			word.WriteRune(r)
			inWord = true
			continue
		case '!':
			// The history's in a terminal window, which is a line nobody wrote
			// here; alone, or before = or (, it is itself.
			if next := at(i + 1); next != 0 && !strings.ContainsRune(" \t\n=(", next) {
				plain = false
			}
			word.WriteRune(r)
			inWord = true
			continue
		}
		if quote == '"' {
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(runes):
				i++
				word.WriteRune(runes[i])
			default:
				word.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote, inWord, quoted = r, true, true
		case '\\':
			if i+1 < len(runes) {
				i++
				if runes[i] != '\n' {
					word.WriteRune(runes[i])
					inWord, quoted = true, true
				}
			}
		case ' ', '\t':
			endWord()
		case '\n', ';':
			endCommand()
		case '|':
			endCommand()
			if at(i+1) == '|' {
				i++
			}
		case '&':
			if at(i+1) == '&' {
				i++
			} else {
				// A background job outlives what its answer says.
				plain = false
			}
			endCommand()
		case '>':
			// The digit before 2> is the stream, not a word of the command.
			if inWord && !quoted && !varies && (word.String() == "1" || word.String() == "2") {
				word.Reset()
				inWord = false
			}
			endWord()
			if end, harmless := redirectionEnd(runes, i+1); harmless {
				i = end - 1
			} else {
				plain = false
				if at(i+1) == '&' {
					// >&word writes word; the & is not a background job's.
					i++
				}
			}
		case '<':
			if at(i+1) == '<' || !inputFile(runes, i+1) {
				plain = false
			}
			endWord()
		case '(', ')', '{', '}':
			plain = false
			endWord()
		case '#':
			if !inWord {
				plain = false
			}
			word.WriteRune(r)
			inWord = true
		case '*', '?', '[':
			varies = true
			word.WriteRune(r)
			inWord = true
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		plain = false
	}
	endCommand()
	return commands, plain
}

// nameRune is a character of a variable's name, or of a positional's number.
func nameRune(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// redirectionEnd reads what follows a >, and reports where it ends when it
// writes no file: &2 and &- point the stream at another or close it, and
// /dev/null discards it. Each is the whole word — >&2x and >/dev/null.txt are
// files.
func redirectionEnd(runes []rune, i int) (int, bool) {
	ends := func(j int) bool {
		return j >= len(runes) || strings.ContainsRune(" \t\n;|&<>()", runes[j])
	}
	if i < len(runes) && runes[i] == '&' {
		j := i + 1
		for j < len(runes) && runes[j] >= '0' && runes[j] <= '9' {
			j++
		}
		if j == i+1 {
			if j >= len(runes) || runes[j] != '-' {
				return 0, false
			}
			j++
		}
		return j, ends(j)
	}
	if i < len(runes) && runes[i] == '>' {
		i++
	}
	for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
		i++
	}
	const null = "/dev/null"
	if !strings.HasPrefix(string(runes[i:]), null) {
		return 0, false
	}
	return i + len(null), ends(i + len(null))
}

// inputFile reads what follows a <, and reports whether it is a file's path
// written out. bash opens a connection for /dev/tcp/host/port and /dev/udp, and a
// name that is quoted, or a variable's, could be one.
func inputFile(runes []rune, i int) bool {
	for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
		i++
	}
	start := i
	for i < len(runes) && (nameRune(runes[i]) || strings.ContainsRune("./~+,:@%=-", runes[i])) {
		i++
	}
	if i < len(runes) && !strings.ContainsRune(" \t\n;|&<>()", runes[i]) {
		return false
	}
	target := string(runes[start:i])
	return !strings.HasPrefix(target, "/dev/tcp") && !strings.HasPrefix(target, "/dev/udp")
}

// wrappers only change how the command after them runs, by the flags of
// theirs that take a value.
var wrappers = map[string][]string{
	"sudo": {"-u", "-g", "-C", "-h", "-p", "-r", "-t", "-U", "-D"}, "nice": {"-n"}, "ionice": {"-c", "-n", "-p"},
	"nohup": nil, "stdbuf": {"-i", "-o", "-e"}, "exec": {"-a"}, "command": nil, "builtin": nil, "time": {"-f", "-o"},
	"timeout": {"-s", "-k", "--signal", "--kill-after"},
}

// unwrap takes off what only changes how a command runs: variables set for it,
// sudo, nice, timeout and the like. It takes off whatever is there, to find what
// a line runs under them; whether a read may be run under them is readUnder's.
func unwrap(words []string) []string {
	for len(words) > 0 {
		w := words[0]
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "=") {
			words = words[1:]
			continue
		}
		valued, wraps := wrappers[w]
		if !wraps {
			return words
		}
		words = words[1:]
		for len(words) > 0 && strings.HasPrefix(words[0], "-") {
			flag := words[0]
			words = words[1:]
			if slices.Contains(valued, flag) && len(words) > 0 {
				words = words[1:]
			}
		}
		if w == "timeout" && len(words) > 0 {
			// Its duration.
			words = words[1:]
		}
	}
	return words
}

// readWrappers are the wrappers a read may run under, each with the flags that
// leave it the command that follows, run once, in the foreground: not sudo -e,
// -i, -s or -b, not ionice -p, which is another process's, not time -o, which
// writes a file. nohup writes one too, and exec ends the shell it is typed into.
var readWrappers = map[string]*flags{
	"sudo": flagsOf("-n -E -H -S -l -V -u: -g: --non-interactive --set-home --stdin --list --version " +
		"--preserve-env? --user= --group="),
	"nice":    flagsOf("-# -n: --adjustment="),
	"ionice":  flagsOf("-t -c: -n: --ignore --class= --classdata="),
	"stdbuf":  flagsOf("-i: -o: -e: --input= --output= --error="),
	"command": flagsOf("-p -v -V"),
	"builtin": flagsOf(""),
	"time":    flagsOf("-p"),
	"timeout": flagsOf("-v -s: -k: --verbose --preserve-status --foreground --signal= --kill-after="),
}

// A variable set in front of a command is the command's to obey: git runs what
// GIT_SSH_COMMAND names, the loader what LD_PRELOAD does, any program the PATH
// it is found on. These few only say how output is written, with values that
// are a locale's, a zone's or a number; and a pager set to cat is no pager.
var (
	harmlessVariables = setOf("LANG", "LANGUAGE", "LC_ALL", "LC_COLLATE", "LC_CTYPE", "LC_MESSAGES", "LC_MONETARY",
		"LC_NUMERIC", "LC_TIME", "TZ", "COLUMNS", "LINES", "NO_COLOR", "TERM")
	pagerVariables = setOf("PAGER", "GIT_PAGER", "SYSTEMD_PAGER", "MANPAGER")
)

func harmlessVariable(word string) bool {
	name, value, _ := strings.Cut(word, "=")
	if pagerVariables[name] {
		return value == "" || value == "cat"
	}
	if !harmlessVariables[name] {
		return false
	}
	return !strings.ContainsFunc(value, func(r rune) bool {
		return !(nameRune(r) || strings.ContainsRune(".@:+-", r) || r == '/' && name == "TZ")
	})
}

// readUnder takes off what a read may run under, and reports whether that is
// all there was in front of the command: false for a variable that is not one of
// the harmless ones, a wrapper's flag that is not listed, or a word of theirs
// that is not what was typed, since then what runs is not known here. A line
// that only sets variables sets them for every line after it.
func readUnder(c simple) (simple, bool) {
	for len(c.words) > 0 {
		w := c.words[0]
		if c.varies[0] {
			return c, false
		}
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "=") {
			if !harmlessVariable(w) || len(c.words) == 1 {
				return c, false
			}
			c = simple{c.words[1:], c.varies[1:]}
			continue
		}
		form, wraps := readWrappers[w]
		if !wraps {
			return c, true
		}
		given, took, ok := form.lead(c.words[1:])
		if w == "timeout" && took < len(c.words)-1 {
			// Its duration.
			took++
		}
		if !ok || slices.Contains(c.varies[1:1+took], true) {
			return c, false
		}
		if w == "command" && given.any("-v", "-V") {
			// It says what a name is, and runs nothing.
			return simple{}, true
		}
		c = simple{c.words[1+took:], c.varies[1+took:]}
	}
	return c, true
}

// systemDirectories are where the system keeps its own programs. A reader's
// name under any other directory is whatever file somebody put there.
var systemDirectories = setOf("/bin/", "/sbin/", "/usr/bin/", "/usr/sbin/", "/usr/local/bin/", "/usr/local/sbin/", "/snap/bin/")

// nonFlags are a command's words that are not flags, in order.
func nonFlags(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

func anyArg(args []string, match func(string) bool) bool {
	return slices.ContainsFunc(args, match)
}

// commandKind reads one simple command.
func commandKind(c simple) string {
	if words := unwrap(c.words); len(words) > 0 {
		program := filepath.Base(words[0])
		args := words[1:]
		plain := nonFlags(args)
		first, second := "", ""
		if len(plain) > 0 {
			first = plain[0]
		}
		if len(plain) > 1 {
			second = plain[1]
		}
		switch {
		case destructivePrograms[program], strings.HasPrefix(program, "mkfs."):
			return CommandDestructive
		case slices.Contains(destructiveSubcommands[program], first),
			slices.Contains(destructiveSubcommands[program+"."+first], second):
			return CommandDestructive
		case program == "find" && slices.Contains(args, "-delete"):
			return CommandDestructive
		case program == "iptables" && anyArg(args, func(a string) bool {
			return a == "-D" || a == "-F" || a == "--flush" || a == "-X" || a == "--delete" || a == "-P" || a == "--policy"
		}):
			return CommandDestructive
		case program == "crontab" && slices.Contains(args, "-r"):
			return CommandDestructive
		case program == "ss" && anyArg(args, func(a string) bool { return a == "-K" || a == "--kill" }):
			return CommandDestructive
		}
	}
	// A read is what is listed as one, under nothing but what is listed too.
	c, ok := readUnder(c)
	switch {
	case !ok:
		return CommandChange
	case len(c.words) == 0:
		return CommandRead
	}
	dir, name := path.Split(c.words[0])
	if dir != "" && !systemDirectories[dir] {
		return CommandChange
	}
	reader, known := readers[name]
	switch {
	case !known:
		return CommandChange
	case reader.whatever:
		return CommandRead
	case slices.Contains(c.varies, true), !reader.reads(c.words[1:]):
		return CommandChange
	}
	return CommandRead
}

// What run_command types into the chat's terminal window instead of running it
// on its own. A line run on its own is a child of the dashboard's process, in
// its container: when Docker restarts, every container restarts with it, and a
// package upgrade restarts Docker when Docker is one of the packages — so an
// upgrade run on its own stopped itself halfway, and dpkg with it. The window is
// held on the host outside the dashboard (term.Manager.HoldSessions), and goes
// on whatever happens to it; and a window can ask a question, or for sudo's
// password, which the person answers on the page.

// Why a line goes to the terminal window, as the model is told it.
const (
	TerminalForPackages = "it changes the server's packages, which can take minutes, stop to ask about a changed config file, and restart Docker — and with it this dashboard, which would stop a command it runs on its own halfway"
	TerminalForDocker   = "it restarts Docker, which restarts this dashboard with every other container, and would stop a command it runs on its own halfway"
	TerminalForSudo     = "sudo may ask for a password, which only a terminal can take: the person types it on the page, and it never reaches you"
)

// packageChanges are the words, by package manager, that change what is
// installed.
var packageChanges = map[string][]string{
	"apt":      {"install", "reinstall", "upgrade", "full-upgrade", "dist-upgrade", "remove", "purge", "autoremove"},
	"apt-get":  {"install", "reinstall", "upgrade", "dist-upgrade", "remove", "purge", "autoremove", "build-dep"},
	"aptitude": {"install", "reinstall", "upgrade", "safe-upgrade", "full-upgrade", "dist-upgrade", "remove", "purge"},
	"dnf":      {"install", "reinstall", "upgrade", "update", "distro-sync", "downgrade", "remove", "erase", "autoremove"},
	"yum":      {"install", "reinstall", "upgrade", "update", "distro-sync", "downgrade", "remove", "erase", "autoremove"},
	"microdnf": {"install", "reinstall", "upgrade", "update", "remove"},
	"zypper":   {"install", "in", "update", "up", "dist-upgrade", "dup", "patch", "remove", "rm"},
	"apk":      {"add", "del", "upgrade", "fix"},
	"snap":     {"install", "refresh", "remove", "revert"},
}

// packagePrograms change packages whatever they are given.
var packagePrograms = setOf("unattended-upgrade", "unattended-upgrades", "do-release-upgrade")

// dpkgChanges are dpkg's actions that change what is installed.
var dpkgChanges = setOf("-i", "--install", "-r", "--remove", "-P", "--purge", "--configure", "--unpack")

// dockerUnits are the services whose restart restarts every container.
var dockerUnits = setOf("docker", "docker.service", "docker.socket", "containerd", "containerd.service")

// dockerRestarts are systemctl's and service's words that stop a service.
var dockerRestarts = setOf("restart", "stop", "try-restart", "reload-or-restart", "try-reload-or-restart", "kill")

// CommandForTerminal is why a line given to run_command is better typed into the
// chat's terminal window, or "" when it runs as well on its own: a line that
// changes packages, or restarts Docker.
func CommandForTerminal(line string) string {
	segments, _ := splitCommand(line)
	for _, words := range segments {
		words = unwrap(words)
		if len(words) == 0 {
			continue
		}
		program := filepath.Base(words[0])
		args := words[1:]
		plain := nonFlags(args)
		switch {
		case packagePrograms[program],
			slices.ContainsFunc(plain, func(w string) bool { return slices.Contains(packageChanges[program], w) }),
			program == "dpkg" && anyArg(args, func(a string) bool { return dpkgChanges[a] }),
			program == "pacman" && anyArg(args, pacmanChanges):
			return TerminalForPackages
		case program == "systemctl" && len(plain) > 1 && dockerRestarts[plain[0]] &&
			slices.ContainsFunc(plain[1:], func(w string) bool { return dockerUnits[w] }):
			return TerminalForDocker
		case program == "service" && len(plain) > 1 && dockerUnits[plain[0]] && dockerRestarts[plain[1]]:
			return TerminalForDocker
		}
	}
	return ""
}

// pacmanChanges is a pacman operation that installs, upgrades or removes: -S
// without a query's letters, -U and -R.
func pacmanChanges(arg string) bool {
	switch arg {
	case "--sync", "--upgrade", "--remove":
		return true
	}
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false
	}
	letters := arg[1:]
	switch {
	case strings.ContainsAny(letters, "UR"):
		return true
	case strings.Contains(letters, "S"):
		return !strings.ContainsAny(letters, "silgpc")
	}
	return false
}

// CommandAsksSudo reports whether a line runs something under sudo that may ask
// for a password: sudo without -n or -S.
func CommandAsksSudo(line string) bool {
	segments, _ := splitCommand(line)
	for _, words := range segments {
		if sudoAsks(words) {
			return true
		}
	}
	return false
}

// sudoAsks walks a simple command's wrappers, as unwrap does, for a sudo that
// may ask.
func sudoAsks(words []string) bool {
	for len(words) > 0 {
		w := words[0]
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "=") {
			words = words[1:]
			continue
		}
		valued, wraps := wrappers[w]
		if !wraps {
			return false
		}
		words = words[1:]
		quiet := false
		for len(words) > 0 && strings.HasPrefix(words[0], "-") {
			flag := words[0]
			words = words[1:]
			if w == "sudo" && (flag == "--non-interactive" || flag == "--stdin" ||
				!strings.HasPrefix(flag, "--") && strings.ContainsAny(flag[1:], "nS")) {
				quiet = true
			}
			if slices.Contains(valued, flag) && len(words) > 0 {
				words = words[1:]
			}
		}
		if w == "sudo" && !quiet {
			return true
		}
		if w == "timeout" && len(words) > 0 {
			words = words[1:]
		}
	}
	return false
}
