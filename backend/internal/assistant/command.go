package assistant

import (
	"encoding/json"
	"fmt"
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

// readPrograms only read, whatever their arguments, but for what segmentKind
// checks of them by name.
var readPrograms = setOf(
	"ls", "ll", "cat", "head", "tail", "grep", "egrep", "fgrep", "rg", "wc", "sort", "uniq", "cut", "tr", "column",
	"find", "stat", "file", "du", "df", "free", "uptime", "uname", "hostname", "whoami", "id", "groups", "who", "w",
	"last", "date", "echo", "printf", "pwd", "which", "type", "whereis", "ps", "pgrep", "pstree", "lsof", "ss",
	"netstat", "ip", "ping", "dig", "nslookup", "host", "getent", "traceroute", "lsblk", "blkid", "findmnt", "lscpu",
	"lsmem", "lspci", "lsusb", "vmstat", "iostat", "mpstat", "nproc", "dmesg", "journalctl", "systemctl", "loginctl",
	"tree", "readlink", "realpath", "basename", "dirname", "md5sum", "sha1sum", "sha256sum", "jq", "diff", "cmp",
	"zcat", "zgrep", "xxd", "od", "strings", "nl", "tac", "rev", "test", "[", "true", "false", "seq", "sleep",
	"nginx", "apache2ctl", "apachectl", "docker", "git", "apt", "apt-cache", "dpkg", "dpkg-query", "snap", "crontab",
	"ufw", "iptables", "nft", "fail2ban-client", "openssl", "certbot", "pm2", "npm", "systemd-analyze",
)

// readSubcommands are what a program that both reads and writes only reads with,
// as its first word that is not a flag.
var readSubcommands = map[string][]string{
	"systemctl":       {"status", "show", "cat", "list-units", "list-unit-files", "list-timers", "list-sockets", "list-dependencies", "is-active", "is-enabled", "is-failed", "is-system-running", "get-default"},
	"docker":          {"ps", "images", "image", "inspect", "logs", "stats", "top", "port", "version", "info", "history", "diff", "network", "volume", "compose", "container", "system"},
	"git":             {"status", "log", "diff", "show", "branch", "remote", "rev-parse", "describe", "ls-files", "blame", "tag", "shortlog", "reflog", "ls-remote"},
	"apt":             {"list", "show", "search", "policy", "depends", "rdepends", "changelog"},
	"apt-cache":       {"show", "search", "policy", "depends", "rdepends", "showpkg", "madison", "pkgnames"},
	"snap":            {"list", "info", "find", "version", "services", "changes"},
	"ufw":             {"status", "show"},
	"nft":             {"list"},
	"fail2ban-client": {"status", "get", "ping", "version"},
	"openssl":         {"x509", "s_client", "version", "verify", "crl", "ciphers"},
	"certbot":         {"certificates"},
	"pm2":             {"list", "ls", "status", "show", "describe", "info", "jlist", "prettylist"},
	"npm":             {"ls", "list", "view", "outdated", "config"},
	"ip":              {"a", "addr", "address", "r", "route", "l", "link", "n", "neigh", "rule", "maddr"},
	"systemd-analyze": {"blame", "critical-chain", "time", "verify", "security", "cat-config", "calendar", "timespan"},
}

// readSecond are what a read subcommand reads with when it takes a word of its
// own (docker network ls, git remote -v); "" is the subcommand alone.
var readSecond = map[string][]string{
	"docker.image":     {"ls", "list", "inspect", "history"},
	"docker.network":   {"ls", "list", "inspect"},
	"docker.volume":    {"ls", "list", "inspect"},
	"docker.compose":   {"ps", "ls", "logs", "config", "top", "images", "version", "port"},
	"docker.container": {"ls", "list", "inspect", "logs", "top", "port", "stats", "diff"},
	"docker.system":    {"df", "info"},
	"git.branch":       {"", "--list", "--show-current"},
	"git.remote":       {"", "show", "get-url"},
	"git.tag":          {"", "--list"},
	"npm.config":       {"get", "list", "ls"},
	"ip.r":             {"", "show", "list", "get"},
	"ip.route":         {"", "show", "list", "get"},
	"ip.a":             {"", "show", "list"},
	"ip.addr":          {"", "show", "list"},
	"ip.address":       {"", "show", "list"},
	"ip.l":             {"", "show", "list"},
	"ip.link":          {"", "show", "list"},
	"ip.rule":          {"", "show", "list"},
	"ip.n":             {"", "show", "list"},
	"ip.neigh":         {"", "show", "list"},
	"ip.maddr":         {"", "show"},
}

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
	segments, plain := splitCommand(command)
	kind := CommandRead
	if !plain || len(segments) == 0 {
		kind = CommandChange
	}
	for _, words := range segments {
		switch segmentKind(words) {
		case CommandDestructive:
			return CommandDestructive
		case CommandChange:
			kind = CommandChange
		}
	}
	return kind
}

// splitCommand cuts a line into its simple commands at ; && || | & and new
// lines, each as its words, and reports whether the line is plain: nothing
// written by a redirection but to /dev/null, no substitution, no subshell or
// group, no here-document and no quote left open. A line that is not plain is
// never a read.
func splitCommand(line string) ([][]string, bool) {
	var (
		segments [][]string
		words    []string
		word     strings.Builder
		inWord   bool
		quote    rune
		plain    = true
	)
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endSegment := func() {
		endWord()
		if len(words) > 0 {
			segments = append(segments, words)
		}
		words = nil
	}
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			switch {
			case r == quote:
				quote = 0
			case quote == '"' && (r == '`' || r == '$' && i+1 < len(runes) && runes[i+1] == '('):
				plain = false
				word.WriteRune(r)
			case quote == '"' && r == '\\' && i+1 < len(runes):
				i++
				word.WriteRune(runes[i])
			default:
				word.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote, inWord = r, true
		case '\\':
			if i+1 < len(runes) {
				i++
				if runes[i] != '\n' {
					word.WriteRune(runes[i])
					inWord = true
				}
			}
		case ' ', '\t':
			endWord()
		case '\n', ';':
			endSegment()
		case '|':
			endSegment()
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
		case '&':
			switch {
			case i+1 < len(runes) && runes[i+1] == '&':
				i++
				endSegment()
			case i > 0 && runes[i-1] == '>':
				// The & of 2>&1, which only points one stream at another.
			default:
				// A background job outlives what its answer says.
				plain = false
				endSegment()
			}
		case '>':
			rest := string(runes[i+1:])
			target := strings.TrimLeft(strings.TrimPrefix(rest, ">"), " \t")
			// The digit before 2> is the stream, not a word of the command.
			if inWord && (word.String() == "1" || word.String() == "2") {
				word.Reset()
				inWord = false
			}
			endWord()
			switch {
			case strings.HasPrefix(rest, "&"):
				// 2>&1: the & is skipped above, the digit after it here.
				i++
				for i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9' {
					i++
				}
			case strings.HasPrefix(target, "/dev/null"):
				i += len([]rune(rest)) - len([]rune(target)) + len("/dev/null")
			default:
				plain = false
			}
		case '<':
			if i+1 < len(runes) && runes[i+1] == '<' {
				plain = false
			}
			endWord()
		case '`':
			plain = false
			word.WriteRune(r)
			inWord = true
		case '$':
			// A variable is read; a substitution runs something else.
			if i+1 < len(runes) && runes[i+1] == '(' {
				plain = false
			}
			word.WriteRune(r)
			inWord = true
		case '(', ')', '{', '}':
			plain = false
			endWord()
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		plain = false
	}
	endSegment()
	return segments, plain
}

// wrappers only change how the command after them runs, by the flags of
// theirs that take a value.
var wrappers = map[string][]string{
	"sudo": {"-u", "-g", "-C", "-h", "-p", "-r", "-t", "-U", "-D"}, "nice": {"-n"}, "ionice": {"-c", "-n", "-p"},
	"nohup": nil, "stdbuf": {"-i", "-o", "-e"}, "exec": {"-a"}, "command": nil, "builtin": nil, "time": {"-f", "-o"},
	"timeout": {"-s", "-k", "--signal", "--kill-after"},
}

// unwrap takes off what only changes how a command runs: variables set for it,
// sudo, nice, timeout and the like.
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

// segmentKind reads one simple command.
func segmentKind(words []string) string {
	words = unwrap(words)
	if len(words) == 0 {
		return CommandRead
	}
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
	case !readPrograms[program]:
		return CommandChange
	}
	// Programs that only read but for some of their flags or words.
	switch program {
	case "find":
		if anyArg(args, func(a string) bool {
			return a == "-exec" || a == "-execdir" || a == "-ok" || a == "-okdir" || strings.HasPrefix(a, "-fprint") || a == "-fls"
		}) {
			return CommandChange
		}
		return CommandRead
	case "sort", "tree":
		if anyArg(args, func(a string) bool { return a == "-o" || strings.HasPrefix(a, "--output") }) {
			return CommandChange
		}
		return CommandRead
	case "uniq":
		// uniq in out writes out.
		if len(plain) > 1 {
			return CommandChange
		}
		return CommandRead
	case "date":
		if anyArg(args, func(a string) bool { return a == "-s" || strings.HasPrefix(a, "--set") }) {
			return CommandChange
		}
		return CommandRead
	case "hostname":
		if first != "" || anyArg(args, func(a string) bool { return a == "-F" || a == "-b" || strings.HasPrefix(a, "--file") || a == "--boot" }) {
			return CommandChange
		}
		return CommandRead
	case "dmesg":
		if anyArg(args, func(a string) bool {
			return a == "-c" || a == "-C" || a == "--clear" || a == "--read-clear" || a == "-n" || a == "-E" || a == "-D" || strings.HasPrefix(a, "--console")
		}) {
			return CommandChange
		}
		return CommandRead
	case "journalctl":
		if anyArg(args, func(a string) bool {
			return strings.HasPrefix(a, "--vacuum") || a == "--rotate" || a == "--flush" || a == "--sync" || a == "--relinquish-var" || a == "--smart-relinquish-var"
		}) {
			return CommandChange
		}
		return CommandRead
	case "crontab":
		// crontab -l, with -u name; anything else installs or edits one.
		if !slices.Contains(args, "-l") {
			return CommandChange
		}
		for i := 0; i < len(args); i++ {
			switch {
			case args[i] == "-l":
			case args[i] == "-u" && i+1 < len(args):
				i++
			default:
				return CommandChange
			}
		}
		return CommandRead
	case "nginx", "apache2ctl", "apachectl":
		reads := []string{"-t", "-T", "-v", "-V", "-q", "-S", "-M", "configtest"}
		if len(args) == 0 {
			return CommandChange
		}
		for i := 0; i < len(args); i++ {
			switch {
			case slices.Contains(reads, args[i]):
			case (args[i] == "-c" || args[i] == "-p") && i+1 < len(args):
				i++
			default:
				return CommandChange
			}
		}
		return CommandRead
	case "dpkg", "dpkg-query":
		actions := []string{"-l", "-L", "-s", "-S", "-p", "-W", "--list", "--listfiles", "--status", "--search", "--print-avail",
			"--show", "--print-architecture", "--get-selections", "--audit", "-C", "--version"}
		read := false
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				continue
			}
			name := strings.SplitN(a, "=", 2)[0]
			if slices.Contains(actions, name) {
				read = true
				continue
			}
			if name == "-f" || name == "--showformat" {
				continue
			}
			return CommandChange
		}
		if read {
			return CommandRead
		}
		return CommandChange
	case "iptables":
		listing := false
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case a == "-L" || a == "-S" || a == "--list" || a == "--list-rules" || a == "-nL" || a == "-nvL" || a == "-vnL" || a == "-Ln" || a == "-vL":
				listing = true
			case a == "-n" || a == "-v" || a == "-x" || a == "--line-numbers" || a == "-w":
			case a == "-t" && i+1 < len(args):
				i++
			case !strings.HasPrefix(a, "-"):
				// A chain's name.
			default:
				return CommandChange
			}
		}
		if listing {
			return CommandRead
		}
		return CommandChange
	case "openssl":
		if anyArg(args, func(a string) bool { return a == "-out" || a == "-keyout" || a == "-signkey" }) {
			return CommandChange
		}
	case "git":
		if anyArg(args, func(a string) bool { return strings.HasPrefix(a, "--output") || a == "-o" }) {
			return CommandChange
		}
	}
	subs, both := readSubcommands[program]
	if !both {
		return CommandRead
	}
	if first == "" {
		// Bare `systemctl`, `docker`, `git status`-less `git` print and change nothing.
		switch program {
		case "systemctl", "ip", "git", "docker", "pm2", "snap", "ufw", "systemd-analyze":
			return CommandRead
		}
		return CommandChange
	}
	if !slices.Contains(subs, first) {
		return CommandChange
	}
	nested, takesSecond := readSecond[program+"."+first]
	if !takesSecond {
		return CommandRead
	}
	if program == "git" {
		// git branch -d, git tag -a and git remote add change; listing does not.
		for _, a := range args {
			if strings.HasPrefix(a, "-") && !slices.Contains(nested, a) &&
				!(first == "branch" && (a == "-a" || a == "-r" || a == "-v" || a == "-vv" || a == "-l")) &&
				!(first == "remote" && a == "-v") && !(first == "tag" && a == "-l") {
				return CommandChange
			}
		}
		if (first == "branch" || first == "tag") && second != "" {
			return CommandChange
		}
	}
	if !slices.Contains(nested, second) {
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
