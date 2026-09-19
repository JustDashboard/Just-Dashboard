package term

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// What a window is doing, read off the PTY rather than asked of the shell.
//
// A tab that says "Terminal" all day is a tab that has to be clicked to find
// out whether the build finished. Every desktop terminal answers that at a
// glance — the title follows the program and something marks a tab that is
// still working — and both facts are available here without touching the
// operator's shell configuration:
//
//   - The title is whatever the program set through OSC 0 or OSC 2, which the
//     read loop picks out of the byte stream on its way to the browsers. The
//     bundled prompts set it to the working directory at every prompt, so at
//     rest a tab is named after where it is.
//   - Whether something is running is the kernel's to say. TIOCGPGRP on the
//     PTY master returns the process group that holds the terminal: the shell's
//     own at a prompt, the job's while one runs. That is how a shell implements
//     job control in the first place, so it is right for every program, whether
//     or not it ever sets a title.
//
// The two are combined with one rule: a title counts only while the process
// group that set it still holds the terminal. A program that names itself is
// shown by that name; one that does not is shown by its process name; and the
// prompt's directory title comes back the moment the job ends, because the
// shell sets it again.
//
// Holding the terminal is not the same as working, and the difference is the
// whole point of marking anything. An editor, or an agent waiting at its
// prompt, holds the terminal for hours while doing nothing; `ls` holds it for
// three milliseconds. So a job is announced only once it has lasted a second,
// and it is *working* only while something is actually happening — output
// arriving for a second or more and still arriving, or CPU being burned by a
// job that prints nothing — which is exactly when an agent animates its own
// title glyph. When that stops, the window is marked finished until somebody
// looks at it.

// Activity is the answer, as sent to browsers and listed by the API.
type Activity struct {
	// Title is the OSC 0/2 title, when it was set by whatever is currently in
	// the foreground. Empty otherwise, so the caller falls back to the process
	// or the window's own name rather than showing a stale one.
	Title string `json:"title,omitempty"`
	// Busy is true while something other than an idle shell prompt holds the
	// terminal, once it has done so for longer than holdOff.
	Busy bool `json:"busy"`
	// Process names the foreground job while Busy — the command the operator
	// typed, as far as its argument vector reveals it.
	Process string `json:"process,omitempty"`
	// Working is true while the job is doing something: output has been
	// arriving for sustain and is still arriving, or the job is using CPU.
	Working bool `json:"working"`
	// FinishedAt is when the last stretch of work ended, in milliseconds since
	// the epoch, kept until work starts again. The browser decides how long to
	// show it — a moment for the window on screen, until it is looked at for
	// one that is not.
	FinishedAt int64 `json:"finishedAt,omitempty"`
}

const (
	// observeEvery bounds how often a burst of output re-reads the foreground
	// group: one ioctl and a /proc read per chunk would be wasted on a program
	// printing thousands of lines a second that is, throughout, the same
	// program.
	observeEvery = 100 * time.Millisecond
	// settleAfter is the follow-up look once output goes quiet. The bytes that
	// announce a command are the echo of the Enter that started it, which
	// arrive *before* the shell has handed the terminal over, so the
	// observation they trigger still sees the prompt.
	settleAfter = 350 * time.Millisecond
	// holdOff is how long a job must last before it is announced at all. `ls`
	// takes the terminal for a few milliseconds; a tab that switched its name
	// for every one of those would flicker all day.
	holdOff = time.Second
	// sustain is how long output must have been arriving before it counts as
	// work, and quietAfter how long a pause ends it. The first is longer than
	// anything a keystroke provokes; the second is shorter than a person's
	// patience but longer than the gaps in a build's log.
	sustain    = time.Second
	quietAfter = 2500 * time.Millisecond
	// echoWindow is how long after a keystroke output is taken to be its echo.
	// An editor, or an agent redrawing its input line as you type, produces a
	// burst per key and is not working.
	echoWindow = 150 * time.Millisecond
	// tickEvery is how often a window with a job in it is re-examined when no
	// output prompts a look: a job waiting out its hold-off, a compiler that
	// prints nothing, a run of output that has just gone quiet.
	tickEvery = 500 * time.Millisecond
	// cpuWorking is the share of one core, over a tick, that counts as work.
	cpuWorking = 0.05
	// userHZ is the unit of the CPU times in /proc/<pid>/stat, fixed on Linux.
	userHZ = 100
)

// jobState is the foreground job as last seen: its group, and when it first
// appeared, which is what the hold-off counts from.
type jobState struct {
	group int
	since time.Time
}

// cpuSample is the job's CPU time at one look, so the next look can take the
// difference; working is what that difference last said.
type cpuSample struct {
	group   int
	at      time.Time
	ticks   int64
	working bool
}

// noteOutput is the read loop's hook: titles are parsed out of the chunk, the
// output is noted as activity unless it is the echo of a keystroke, and the
// foreground is re-examined, rate-limited.
func (s *Session) noteOutput(chunk []byte) {
	titled := false
	for _, title := range s.titles.feed(chunk) {
		s.setTitle(title)
		titled = true
	}
	now := time.Now()
	s.mu.Lock()
	if now.Sub(s.lastInput) > echoWindow {
		if now.Sub(s.lastOutput) > quietAfter {
			s.activeSince = now
		}
		s.lastOutput = now
	}
	due := titled || now.Sub(s.observed) >= observeEvery
	s.armTickLocked(settleAfter)
	s.mu.Unlock()
	if due {
		s.Activity()
	}
}

// armTickLocked schedules a look at the window unless one is already due; the
// caller holds s.mu.
func (s *Session) armTickLocked(after time.Duration) {
	if s.tick != nil || s.closed {
		return
	}
	s.tick = time.AfterFunc(after, func() {
		s.mu.Lock()
		s.tick = nil
		closed := s.closed
		s.mu.Unlock()
		if !closed {
			s.Activity()
		}
	})
}

// setTitle records a title together with who set it: the process group holding
// the terminal at that moment, which is what later decides whether the title
// still applies.
func (s *Session) setTitle(title string) {
	owner := s.foregroundGroup()
	s.mu.Lock()
	s.title = sanitiseField(title)
	s.titleOwner = owner
	s.mu.Unlock()
}

// Activity examines the PTY now, remembers the answer, and tells every attached
// browser if it changed. Cheap enough for the listing poll: one ioctl and a
// handful of small /proc reads per window.
func (s *Session) Activity() Activity {
	fg, busy, process, title, transition := s.inspect()
	var ticks int64
	sampled := false
	if busy {
		ticks, sampled = jobCPU(fg)
	}
	now := time.Now()

	s.mu.Lock()
	if !busy || fg != s.job.group {
		s.job = jobState{group: fg, since: now}
	}
	cpu := false
	switch {
	case !busy || !sampled:
		s.cpu = cpuSample{}
	case s.cpu.group != fg || s.cpu.at.IsZero():
		s.cpu = cpuSample{group: fg, at: now, ticks: ticks}
	case now.Sub(s.cpu.at) < tickEvery/2:
		// Too soon for the difference to mean anything; the last one stands.
		cpu = s.cpu.working
	default:
		elapsed := now.Sub(s.cpu.at).Seconds()
		cpu = float64(ticks-s.cpu.ticks)/userHZ/elapsed >= cpuWorking
		s.cpu = cpuSample{group: fg, at: now, ticks: ticks, working: cpu}
	}
	output := busy && !s.lastOutput.IsZero() &&
		now.Sub(s.lastOutput) <= quietAfter && now.Sub(s.activeSince) >= sustain
	working := busy && (output || cpu)

	next := Activity{Title: title, Busy: busy, Process: process, Working: working}
	if transition || (busy && now.Sub(s.job.since) < holdOff) {
		// A job younger than the hold-off is not news yet, and a terminal
		// between a job and its shell is not news either. What was published
		// stands until it either lasts or ends.
		next = s.activity
	} else {
		if s.activity.Working && !working {
			s.finishedAt = now
		} else if working {
			s.finishedAt = time.Time{}
		}
		if !s.finishedAt.IsZero() {
			next.FinishedAt = s.finishedAt.UnixMilli()
		}
	}
	changed := next != s.activity
	s.activity = next
	s.observed = now
	// Keep looking while there is something to notice that no output will
	// announce: a job waiting out its hold-off, a silent job's CPU, a run of
	// output that is about to count as quiet.
	if busy || transition || (!s.lastOutput.IsZero() && now.Sub(s.lastOutput) <= quietAfter) {
		s.armTickLocked(tickEvery)
	}
	var targets []chan Activity
	if changed {
		for _, ch := range s.events {
			targets = append(targets, ch)
		}
	}
	s.mu.Unlock()
	for _, ch := range targets {
		select {
		case ch <- next:
		default:
			// Only the newest state is worth delivering: an older frame that is
			// still queued describes a moment that has passed.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- next:
			default:
			}
		}
	}
	return next
}

// Events is the per-subscriber channel of activity changes, created on demand
// beside the output subscription so the attach handler can select on both.
// A subscriber that is already gone gets a closed channel, which is what its
// reader expects to see when the socket is over.
func (s *Session) Events(id int64) <-chan Activity {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subscribers[id]; !ok {
		ch := make(chan Activity)
		close(ch)
		return ch
	}
	if ch, ok := s.events[id]; ok {
		return ch
	}
	ch := make(chan Activity, 4)
	s.events[id] = ch
	return ch
}

// dropEventsLocked closes a subscriber's event channel; the caller holds s.mu.
func (s *Session) dropEventsLocked(id int64) {
	if ch, ok := s.events[id]; ok {
		delete(s.events, id)
		close(ch)
	}
}

// MarkFocused notes that a browser is showing this window, which is what the
// listing uses to pick the window that represents a session: the one the
// operator was last looking at, or failing that the one opened most recently.
func (s *Session) MarkFocused() {
	s.mu.Lock()
	s.focusedAt = time.Now()
	s.mu.Unlock()
}

func (s *Session) FocusedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.focusedAt
}

// WindowNamed reports whether the operator named this window, as opposed to
// it carrying the default the dashboard gave it.
func (s *Session) WindowNamed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.windowNamed
}

// inspect reads the raw facts: which group holds the terminal, whether that is
// a job rather than the prompt, what the job is called, and the title if the
// group that set it is the one holding the terminal. A group that still holds
// the terminal but has no live process in it is a job that has just ended and
// a shell that has not yet taken the terminal back; that instant is reported
// as a transition rather than as an idle prompt with no title.
func (s *Session) inspect() (fg int, busy bool, process, title string, transition bool) {
	fg = s.foregroundGroup()
	s.mu.Lock()
	stored, owner, root := s.title, s.titleOwner, s.PID
	s.mu.Unlock()
	if fg <= 0 {
		return 0, false, "", "", false
	}
	busy, process, transition = foregroundJob(fg, root)
	if owner == fg {
		title = stored
	}
	return fg, busy, process, title, transition
}

// foregroundGroup asks the kernel which process group holds the terminal.
//
// Asked of the master side, which Linux permits precisely for this purpose:
// the check that refuses a tty that is not the caller's own is skipped for a
// master, and the answer is the slave's foreground group. Read through
// SyscallConn rather than Fd() so the descriptor is not switched to blocking
// mode under the read loop.
func (s *Session) foregroundGroup() int {
	s.mu.Lock()
	f, closed := s.pty, s.closed
	s.mu.Unlock()
	if closed || f == nil {
		return 0
	}
	rc, err := f.SyscallConn()
	if err != nil {
		return 0
	}
	group := 0
	_ = rc.Control(func(fd uintptr) {
		if pg, err := unix.IoctlGetInt(int(fd), unix.TIOCGPGRP); err == nil {
			group = pg
		}
	})
	return group
}

// foregroundJob decides whether the group holding the terminal is a shell
// waiting at its prompt or a program, and names the program.
//
// root is the process this session spawned, used only when the group's leader
// has already gone: in `cat big.log | less` the leader is `cat`, which finishes
// long before `less` does, yet the terminal stays the pipeline's until the
// shell takes it back. Whatever the shell still has in that group is the job.
func foregroundJob(group, root int) (busy bool, process string, gone bool) {
	argv := cmdline(group)
	if argv == nil {
		argv = groupMember(group, root)
	}
	if argv == nil {
		return false, "", true
	}
	probe := argv
	if wrappers[programName(argv[0])] {
		// `sudo -i` is a root shell at a prompt, not a running program. The
		// wrapper's innermost descendant is what actually has the keyboard.
		if leaf := leafDescendant(group); leaf != group {
			if inner := cmdline(leaf); inner != nil {
				probe = inner
			}
		}
	}
	if atPrompt(probe) {
		return false, "", false
	}
	return true, commandName(argv), false
}

// groupMember finds a live process in the shell's part of the tree that
// belongs to the group, for the case where the group's leader has exited.
func groupMember(group, root int) []string {
	if root <= 0 {
		return nil
	}
	shell := leafDescendant(root)
	for _, pid := range append([]int{shell}, childrenOf(shell)...) {
		if pid != group && processGroup(pid) == group {
			return cmdline(pid)
		}
	}
	return nil
}

// cmdline is a process's argument vector, or nil for one that has gone or
// that has none (a kernel thread, a zombie).
func cmdline(pid int) []string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(raw) == 0 {
		return nil
	}
	parts := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	if len(parts) == 0 || parts[0] == "" {
		return nil
	}
	return parts
}

// processGroup reads a process's group out of /proc/<pid>/stat. The command
// name in that file is parenthesised and may contain spaces or parentheses of
// its own, so the fields are counted from the *last* closing parenthesis.
func processGroup(pid int) int {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	stat := string(raw)
	end := strings.LastIndex(stat, ")")
	if end < 0 {
		return 0
	}
	fields := strings.Fields(stat[end+1:])
	// state, ppid, pgrp, ...
	if len(fields) < 3 {
		return 0
	}
	group, _ := strconv.Atoi(fields[2])
	return group
}

// jobCPU is the CPU time the job has used so far, as USER_HZ ticks: the
// group's leader, the children it has already reaped, and its live
// descendants. A make that farms the work out to compilers shows nothing on
// its own line; the descendants are where the time goes.
func jobCPU(group int) (ticks int64, ok bool) {
	ticks, ok = cpuTicks(group, true)
	if !ok {
		return 0, false
	}
	for _, pid := range descendants(group, 64) {
		if t, ok := cpuTicks(pid, false); ok {
			ticks += t
		}
	}
	return ticks, true
}

// cpuTicks reads a process's own user and system time, plus that of its
// reaped children when asked, out of /proc/<pid>/stat.
func cpuTicks(pid int, withReaped bool) (int64, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	stat := string(raw)
	end := strings.LastIndex(stat, ")")
	if end < 0 {
		return 0, false
	}
	// state, ppid, pgrp, session, tty, tpgid, flags, minflt, cminflt, majflt,
	// cmajflt, utime, stime, cutime, cstime, ...
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 15 {
		return 0, false
	}
	var total int64
	for _, i := range []int{11, 12} {
		n, _ := strconv.ParseInt(fields[i], 10, 64)
		total += n
	}
	if withReaped {
		for _, i := range []int{13, 14} {
			n, _ := strconv.ParseInt(fields[i], 10, 64)
			total += n
		}
	}
	return total, true
}

// descendants walks down from a process, breadth first, stopping at limit so
// a fork bomb cannot turn a poll into a crawl of the whole process table.
func descendants(pid, limit int) []int {
	var out []int
	queue := []int{pid}
	for len(queue) > 0 && len(out) < limit {
		next := queue[0]
		queue = queue[1:]
		for _, child := range childrenOf(next) {
			out = append(out, child)
			queue = append(queue, child)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// shells are the programs whose presence in the foreground, with nothing to
// run, means "waiting for the operator".
var shells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ash": true,
	"ksh": true, "mksh": true, "csh": true, "tcsh": true, "nu": true, "elvish": true,
	"xonsh": true,
}

// interpreters run something named later on the line, and that is the name a
// reader wants: `python manage.py runserver` is "manage", not "python". The
// wrappers are the same idea one level out — `sudo apt install` is "apt".
var interpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true,
	"node": true, "nodejs": true, "bun": true, "deno": true, "tsx": true,
	"python": true, "python2": true, "python3": true, "perl": true, "ruby": true, "php": true,
	"env": true, "sudo": true, "doas": true, "nice": true, "nohup": true, "timeout": true,
	"stdbuf": true, "unbuffer": true, "time": true, "nsenter": true, "chroot": true,
}

var wrappers = map[string]bool{
	"sudo": true, "doas": true, "su": true, "env": true, "nice": true, "nohup": true,
	"timeout": true, "stdbuf": true, "unbuffer": true, "nsenter": true, "chroot": true,
	"script": true,
}

// valued are the options that take the next argument, so that argument is
// never mistaken for a script or a command.
var valued = map[string]bool{
	"-o": true, "+o": true, "-O": true, "+O": true, "--rcfile": true, "--init-file": true,
	"-C": true, "--init-command": true, "-u": true, "-g": true, "-n": true,
}

// atPrompt reports whether an argument vector is an interactive shell with
// nothing to run — the definition of idle. A shell given a script or a `-c`
// command is a program like any other.
func atPrompt(argv []string) bool {
	if len(argv) == 0 || !shells[programName(argv[0])] {
		return false
	}
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "-c", arg == "--command":
			return false
		case valued[arg]:
			i++
		case strings.HasPrefix(arg, "-"), strings.HasPrefix(arg, "+"):
		default:
			return false
		}
	}
	return true
}

var numberLike = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[smhd]?$`)

// commandName is the program as the operator would name it: the interpreters
// and wrappers are looked through, a script drops its path and extension, and
// a globally installed npm tool run as `node …/cli.js` answers with its
// package rather than "cli".
func commandName(argv []string) string {
	rest := argv
	name := ""
	for depth := 0; depth < 4 && len(rest) > 0; depth++ {
		name = programName(rest[0])
		if !interpreters[name] {
			break
		}
		next, remaining := scriptOf(rest[1:])
		if next == "" {
			break
		}
		name = next
		rest = remaining
		if !interpreters[name] {
			break
		}
	}
	name = sanitiseField(name)
	if len(name) > 40 {
		name = name[:40]
	}
	return name
}

// scriptOf finds what an interpreter was told to run, skipping its options.
func scriptOf(args []string) (name string, rest []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-c" || arg == "--command":
			if i+1 < len(args) {
				return programName(strings.Fields(args[i+1] + " ")[0]), nil
			}
			return "", nil
		case arg == "-m":
			if i+1 < len(args) {
				return programName(args[i+1]), nil
			}
			return "", nil
		case valued[arg]:
			i++
		case strings.HasPrefix(arg, "-"), numberLike.MatchString(arg):
			// An option, or `timeout 10 …`'s duration.
		default:
			return programName(arg), args[i+1:]
		}
	}
	return "", nil
}

var scriptExtensions = map[string]bool{
	".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".py": true, ".rb": true,
	".pl": true, ".sh": true, ".bash": true, ".zsh": true, ".php": true,
}

// programName reduces one argument to the name a person would use for it.
func programName(arg string) string {
	arg = strings.TrimSpace(arg)
	if i := strings.LastIndex(arg, "node_modules/"); i >= 0 {
		// The package the script belongs to. Scoped packages are two segments,
		// and the scope is the part nobody types.
		pkg := strings.Split(arg[i+len("node_modules/"):], "/")
		if len(pkg) > 0 && strings.HasPrefix(pkg[0], "@") && len(pkg) > 1 {
			return pkg[1]
		}
		if len(pkg) > 0 && pkg[0] != "" {
			return pkg[0]
		}
	}
	name := strings.TrimPrefix(filepath.Base(arg), "-")
	if ext := filepath.Ext(name); scriptExtensions[strings.ToLower(ext)] {
		name = strings.TrimSuffix(name, ext)
	}
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// oscScanner picks OSC 0 and OSC 2 titles out of the byte stream. A sequence
// can straddle two reads, so this is a state machine fed chunk by chunk rather
// than a search over each chunk.
type oscScanner struct {
	state oscState
	param []byte
	body  []byte
	// keep is whether the sequence in progress is one whose body is wanted:
	// a colour query (OSC 10, 11) or a hyperlink (OSC 8) is skipped, as is a
	// title that has run past maxTitleBytes.
	keep bool
}

type oscState uint8

const (
	oscText oscState = iota
	oscEsc
	oscParam
	oscBody
	oscBodyEsc
)

const maxTitleBytes = 512

func (o *oscScanner) feed(chunk []byte) []string {
	var titles []string
	for _, b := range chunk {
	again:
		switch o.state {
		case oscText:
			if b == 0x1b {
				o.state = oscEsc
			}
		case oscEsc:
			if b == ']' {
				o.state = oscParam
				o.param = o.param[:0]
				o.body = o.body[:0]
			} else {
				o.state = oscText
			}
		case oscParam:
			switch {
			case b >= '0' && b <= '9' && len(o.param) < 4:
				o.param = append(o.param, b)
			case b == ';':
				o.keep = string(o.param) == "0" || string(o.param) == "2"
				o.state = oscBody
			case b == 0x07:
				o.state = oscText
			default:
				o.state = oscText
			}
		case oscBody:
			switch b {
			case 0x07:
				if o.keep {
					titles = append(titles, string(o.body))
				}
				o.state = oscText
			case 0x1b:
				o.state = oscBodyEsc
			default:
				if o.keep {
					if len(o.body) < maxTitleBytes {
						o.body = append(o.body, b)
					} else {
						o.keep = false
					}
				}
			}
		case oscBodyEsc:
			if b == '\\' {
				if o.keep {
					titles = append(titles, string(o.body))
				}
				o.state = oscText
			} else {
				// Not the string terminator: the sequence was cut short and
				// this ESC opens a new one.
				o.state = oscEsc
				goto again
			}
		}
	}
	return titles
}

// freeName is the lowest-numbered name not in use: closing "Terminal 2" and
// opening another gives "Terminal 2" back rather than counting upward forever.
func freeName(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for n := 2; n < 1000; n++ {
		candidate := base + " " + strconv.Itoa(n)
		if !taken[candidate] {
			return candidate
		}
	}
	return base
}
