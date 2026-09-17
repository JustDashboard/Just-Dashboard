package term

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/creack/pty"
)

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	// pending counts sessions whose PTY is being spawned. Without it the cap
	// check and the insert sat in two different critical sections, so
	// concurrent creates all passed a check that had room for one of them.
	pending int
	enabled bool
	shell   string
	useTmux bool
	// The account every session runs as, resolved once at boot. accountErr
	// is kept rather than returned from NewManager so that a bad
	// JD_TERMINAL_USER disables the terminal with a precise message instead
	// of refusing to start a dashboard whose other fourteen pages are fine.
	account    Account
	accountErr error
	clipboard  *clipboardStore
	shellDir   string
}

// reserve takes one of the session slots, or reports that none are free. The
// returned function gives the slot back and must be called however the caller
// returns.
func (m *Manager) reserve() (release func(), err error) {
	m.mu.Lock()
	if len(m.sessions)+m.pending >= maxSessions {
		m.mu.Unlock()
		return nil, ErrTooMany
	}
	m.pending++
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			m.pending--
			m.mu.Unlock()
		})
	}, nil
}

func NewManager(enabled bool, shell, username string) *Manager {
	m := &Manager{
		sessions:  map[string]*Session{},
		enabled:   enabled,
		shell:     shell,
		clipboard: newClipboardStore(ClipboardRoot),
	}
	m.account, m.accountErr = resolveAccount(username)
	// The host's tmux, not this image's. Sessions are created out there now,
	// so asking the image whether tmux exists would answer a question about
	// the wrong machine — and offer persistence backed by a tmux server that
	// can never see them.
	m.useTmux = hostexec.AvailableOnHost("tmux")
	go m.reap()
	return m
}

func (m *Manager) Enabled() bool       { return m.enabled }
func (m *Manager) TmuxAvailable() bool { return m.useTmux }

// Account is who sessions run as, for the listing endpoint to show. An
// operator who cannot see which account they are about to land in has to open
// a shell and run `whoami` to find out.
func (m *Manager) Account() (Account, error) { return m.account, m.accountErr }

type CreateOptions struct {
	Title   string
	Owner   string
	Rows    uint16
	Cols    uint16
	CWD     string
	Persist bool
	// Folder files the new session away as it is created, so a shell opened
	// from a stack lands in that stack's group without a second step.
	Folder string
	// Colour tags the session in the rail. A session created inside a folder
	// inherits the folder's colour, which is what makes a group readable as a
	// group rather than as four rows that happen to be adjacent.
	Colour string
	// WorkspaceID is empty for the first window. Additional direct-PTY
	// windows reuse the first window's workspace id.
	WorkspaceID string
	WindowName  string
}

// Create spawns a session. When tmux is present and persistence is requested
// the shell runs inside `tmux new-session -A`, so the session outlives both the
// browser tab and a restart of this process — the same guarantee an operator
// gets from SSH plus tmux, which is why it is worth the extra layer.
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (*Session, error) {
	if !m.enabled {
		return nil, ErrDisabled
	}
	release, err := m.reserve()
	if err != nil {
		return nil, err
	}
	defer release()

	id := randomID()
	if opts.Rows == 0 {
		opts.Rows = 24
	}
	if opts.Cols == 0 {
		opts.Cols = 80
	}
	if m.accountErr != nil {
		return nil, m.accountErr
	}
	// The shell is the account's own unless the operator pinned one. Resolving
	// it here rather than in loginArgv keeps the answer on the Session, where
	// the listing endpoint can show which shell a tab is actually running.
	shell := m.shell
	if shell == "" {
		shell = m.account.Shell
	}
	if shell == "" {
		shell = "/bin/bash"
	}

	sess := &Session{
		ID:          id,
		WorkspaceID: opts.WorkspaceID,
		WindowName:  sanitiseField(opts.WindowName),
		Title:       m.defaultTitle(opts.Title),
		Shell:       shell,
		User:        m.account.Name,
		CreatedAt:   time.Now().UTC(),
		Owner:       opts.Owner,
		Rows:        opts.Rows,
		Cols:        opts.Cols,
		subscribers: map[int64]chan []byte{},
		events:      map[int64]chan Activity{},
		scrollback:  newRingBuffer(scrollbackKB * 1024),
		lastActive:  time.Now(),
		focusedAt:   time.Now(),
		folder:      sanitiseField(opts.Folder),
		colour:      normaliseColour(opts.Colour),
		named:       opts.Title != "",
		windowNamed: opts.WindowName != "",
	}
	if sess.WorkspaceID == "" {
		sess.WorkspaceID = id
	}
	if sess.WindowName == "" {
		sess.WindowName = m.defaultWindowName(sess.WorkspaceID)
	}
	// Where the session should start. Empty unless the caller asked for one
	// and it is a real directory on the host, so everything below can treat a
	// non-empty value as settled.
	startDir := hostDir(ctx, opts.CWD)
	cmdDir := ""

	// What ssh would have run: become the account and exec its login shell.
	// A requested directory changes *how* the login is assembled rather than
	// being applied on top of it — see loginArgv, where a plain login's
	// chdir-to-home is the thing standing in the way.
	argv := m.loginArgv(startDir != "")
	if startDir != "" {
		// The directory the command itself starts in. It has to be handed to
		// hostexec rather than set on cmd.Dir: a host command crosses into the
		// host's mount namespace via nsenter, which resets the working
		// directory to that namespace's root and discards whatever Go chdir'd
		// to on this side.
		cmdDir = startDir
		sess.CWDHint = startDir
	}
	if m.useTmux && opts.Persist {
		sess.TmuxName = sessionPrefix + id
		sess.Persisted = true
		// tmux wraps the login rather than replacing it, so a persistent
		// session is the same session as a plain one with a multiplexer in
		// front. `-c` sets the directory tmux starts the pane in, which the
		// login above is now built to keep rather than override.
		// The environment on the tmux *client* is not automatically the
		// environment of the pane. In particular COLORTERM is not in tmux's
		// default update-environment list, so an already-running server silently
		// dropped truecolor from every new dashboard shell even though the outer
		// PTY correctly advertised it. `new-session -e` writes the capability on
		// the session before the first pane process starts; setting it later is
		// too late for Codex, Claude and any login shell already running there.
		argv = tmuxNewSessionArgv(sess.TmuxName, startDir, argv)
	}
	// CommandOnHost always crosses into the host's namespaces, even though
	// this image happens to ship bash and tmux of its own. That is the whole
	// point: the operator's tools, dotfiles and processes live out there, and
	// a shell in here would be a convincing imitation of their server rather
	// than their server.
	cmd := hostexec.CommandOnHostInDir(context.Background(), cmdDir, argv[0], argv[1:]...)
	// A login resets the environment anyway; these are the two variables that
	// survive it and that the terminal on the other end depends on to render
	// colour and cursor keys correctly.
	cmd.Env = terminalEnv(os.Environ(), id)

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: opts.Rows, Cols: opts.Cols})
	if err != nil {
		return nil, err
	}
	sess.pty = f
	sess.cmd = cmd
	if cmd.Process != nil {
		sess.PID = cmd.Process.Pid
	}

	m.mu.Lock()
	for _, existing := range m.sessions {
		if existing.WorkspaceID == sess.WorkspaceID && existing.WindowOrder >= sess.WindowOrder {
			sess.WindowOrder = existing.WindowOrder + 1
		}
	}
	m.sessions[id] = sess
	m.mu.Unlock()

	// Written onto the tmux session so it outlives this process. A session
	// that comes back after a restart with its own name on it is the whole
	// difference between "my work is still here" and a row of hex strings.
	//
	// One goroutine for all of them rather than one each: they share the wait
	// for the session to exist, and four independent retry loops firing
	// `tmux set-option` at a server that is still starting is four times the
	// subprocesses for the same answer. `default-command` is the odd one out
	// — not the operator's choice but the command every *later* window in the
	// session must run. Without it the first window is the operator's account,
	// because it was handed the login explicitly, and every window after it is
	// root: tmux falls back to the shell of whoever started the tmux server,
	// which is this dashboard.
	//
	// Only the settings that have a value: a session created outside a folder
	// has nothing to record there, and writing an empty option would be a
	// subprocess to say nothing. SetMeta is the path that writes an empty
	// value, because there it means "clear this".
	create := []option{
		{titleOption, sanitiseField(sess.Title)},
		{"default-command", m.defaultCommand()},
		{folderOption, sess.folder},
		{colourOption, sess.colour},
		// tmux's own status line, off. It is the same information the page
		// already draws above the pane — the window list, which window is
		// active, how it is named — rendered in green-on-green inside a
		// terminal that is short to begin with, and duplicated chrome is worse
		// than none. Set on this session only, so the operator's own tmux
		// sessions and anything they attach to from a real SSH client keep
		// theirs.
		{"status", "off"},
		// Mouse reporting on, which is what makes the scroll wheel scroll.
		//
		// Without it the wheel does something actively wrong rather than
		// nothing: tmux holds the alternate screen for the whole session, and
		// xterm translates a wheel tick in the alternate screen into a cursor
		// key — so scrolling up in a shell walked backwards through command
		// history instead of showing what had scrolled past. The scrollback
		// that matters is tmux's anyway; xterm's own is empty, because tmux
		// repaints the viewport rather than emitting lines.
		//
		// It costs nothing on the pointer, because the page takes that back:
		// the pane tells xterm to treat a plain drag as a browser selection
		// rather than forwarding it, so selecting and copying behave as they
		// do anywhere else and only the wheel is tmux's. Holding Alt is what
		// hands the mouse to whatever is running in the pane.
		{"mouse", "on"},
	}
	set := make([]option, 0, len(create))
	for _, o := range create {
		if o.value != "" {
			set = append(set, o)
		}
	}
	m.rememberOptions(sess.TmuxName, set...)

	go sess.readLoop(func() { m.remove(id) })
	return sess, nil
}

func tmuxNewSessionArgv(name, startDir string, login []string) []string {
	argv := []string{"tmux", "new-session", "-A", "-e", "COLORTERM=truecolor", "-s", name}
	if startDir != "" {
		argv = append(argv, "-c", startDir)
	}
	return append(argv, login...)
}

// hostDir accepts a requested working directory only if it is one on the
// host, where the shell will actually run. The check has to cross into the
// host's mount namespace: this process sees the container's filesystem, in
// which only /home, /opt, /srv, /root, /etc and a handful of other paths are
// the host's. A plain os.Stat here answered about the wrong machine in both
// directions — a shell sitting in /var/www or /tmp (host paths the image does
// not carry) was reported as nowhere and the new window fell back to home,
// while a container-only path such as /usr/lib/postgresql passed and then
// made nsenter fail, killing the new PTY on arrival.
func hostDir(ctx context.Context, dir string) string {
	if dir == "" || strings.ContainsRune(dir, 0) {
		return ""
	}
	// `test -d` follows symlinks and fails for deleted (" (deleted)") and
	// missing paths alike. CommandOnHost runs it on the host when containerised
	// and locally otherwise, so both deployments ask the right filesystem.
	if err := hostexec.CommandOnHost(ctx, "test", "-d", dir).Run(); err != nil {
		return ""
	}
	return dir
}

// defaultName is what a session or window is called until somebody names it
// or the shell says what it is doing. "Terminal" rather than "shell": it is
// the word on the page's own navigation, and the one a person uses.
const defaultName = "Terminal"

// defaultTitle names a session the operator did not name.
//
// Numbered, and that is the whole point: the rail exists because five sessions
// called `vpsd-3f2a91c4` are indistinguishable, and five called `Terminal` are
// no better. The number is the lowest one free across everything this dashboard
// knows about, so closing "Terminal 2" and opening another gives back
// "Terminal 2" rather than counting forever upward. The default only shows
// while the window has nothing better to say for itself — see activity.go.
func (m *Manager) defaultTitle(t string) string {
	if t != "" {
		if len(t) > 64 {
			return t[:64]
		}
		return t
	}
	taken := map[string]bool{}
	for _, meta := range m.WorkspaceMeta() {
		taken[meta.Title] = true
	}
	return freeName(defaultName, taken)
}

// defaultWindowName numbers windows the same way, within their session.
func (m *Manager) defaultWindowName(workspaceID string) string {
	taken := map[string]bool{}
	for _, sess := range m.Workspace(workspaceID) {
		taken[sess.WindowName] = true
	}
	return freeName(defaultName, taken)
}

func (m *Manager) Get(id string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return sess, nil
}

func (m *Manager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Workspace returns every direct PTY window in one dashboard session. The
// workspace id is stable even when its first window is later closed.
func (m *Manager) Workspace(id string) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []*Session{}
	for _, sess := range m.sessions {
		if sess.TmuxName == "" && (sess.WorkspaceID == id || sess.ID == id) {
			out = append(out, sess)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WindowOrder == out[j].WindowOrder {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].WindowOrder < out[j].WindowOrder
	})
	return out
}

// NewDirectWindow opens another independent PTY inside a direct dashboard session.
// It intentionally does not use tmux: windows are organisation in the UI,
// while each child remains a native PTY with its own terminal negotiation.
//
// The directory falls back down the chain — the caller's (the active
// window's) directory first, then the workspace's first window as it stands,
// then home — and every step is validated on the host, so a stale directory
// can only move the new window home, never kill it on arrival.
func (m *Manager) NewDirectWindow(ctx context.Context, workspaceID, name, cwd string, rows, cols uint16) (*Session, error) {
	windows := m.Workspace(workspaceID)
	if len(windows) == 0 {
		return nil, ErrNotFound
	}
	root := windows[0]
	meta := root.Meta()
	if hostDir(ctx, cwd) == "" {
		cwd = root.CWD()
	}
	created, err := m.Create(ctx, CreateOptions{
		Title: meta.Title, Owner: root.Owner, Rows: rows, Cols: cols, CWD: cwd,
		Folder: meta.Folder, WorkspaceID: root.WorkspaceID, WindowName: name,
	})
	if err != nil {
		return nil, err
	}
	created.setMeta(meta)
	return created, nil
}

// RenameDirectWindow changes only the compact tab label; the session name in
// the rail remains the name of the whole workspace.
func (m *Manager) RenameDirectWindow(workspaceID, windowID, name string) error {
	name = sanitiseField(name)
	if name == "" {
		return errors.New("a window name is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[windowID]
	if !ok || sess.TmuxName != "" || sess.WorkspaceID != workspaceID {
		return ErrNotFound
	}
	sess.WindowName = name
	sess.mu.Lock()
	sess.windowNamed = true
	sess.mu.Unlock()
	return nil
}

// MoveDirectWindow updates the explicit display order after a tab drag.
func (m *Manager) MoveDirectWindow(workspaceID, windowID string, position int) error {
	windows := m.Workspace(workspaceID)
	if position < 0 {
		position = 0
	}
	if position >= len(windows) {
		position = len(windows) - 1
	}
	from := -1
	for i, sess := range windows {
		if sess.ID == windowID {
			from = i
			break
		}
	}
	if from < 0 {
		return ErrNotFound
	}
	moving := windows[from]
	windows = append(windows[:from], windows[from+1:]...)
	windows = append(windows, nil)
	copy(windows[position+1:], windows[position:])
	windows[position] = moving
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, sess := range windows {
		sess.WindowOrder = i
	}
	return nil
}

// KillDirectWindow closes one PTY but refuses the last: the session close
// route owns that clearer consequence and confirmation.
func (m *Manager) KillDirectWindow(ctx context.Context, workspaceID, windowID string) error {
	windows := m.Workspace(workspaceID)
	if len(windows) == 0 {
		return ErrNotFound
	}
	if len(windows) == 1 {
		return errors.New("this is the only window; close the session instead")
	}
	found := false
	for _, sess := range windows {
		if sess.ID == windowID {
			found = true
			break
		}
	}
	if !found {
		return ErrNotFound
	}
	return m.Kill(ctx, windowID)
}

// KillWorkspace ends every window in a direct session.
func (m *Manager) KillWorkspace(ctx context.Context, id string) error {
	windows := m.Workspace(id)
	if len(windows) == 0 {
		return ErrNotFound
	}
	var first error
	for _, sess := range windows {
		if err := m.Kill(ctx, sess.ID); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	sess, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if ok {
		// Close called before the read loop reached here means the manager is
		// shutting down. A persisted tmux session is still running in that
		// case, so its temporary files stay available and the TTL owns them.
		// A read loop that ended on its own means the PTY truly exited.
		wasClosed := sess.isClosed()
		sess.Close()
		if m.clipboard != nil && (!sess.Persisted || !wasClosed) {
			m.clipboard.removeSession(id)
		}
	}
}

// Kill ends a session for good. For a tmux-backed one the tmux session is
// destroyed too, otherwise "close" would silently leave a live shell behind.
func (m *Manager) Kill(ctx context.Context, id string) error {
	m.mu.Lock()
	sess, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	if sess.TmuxName != "" {
		hostexec.CommandOnHost(ctx, "tmux", "kill-session", "-t", sess.TmuxName).Run()
	}
	err := sess.Close()
	if m.clipboard != nil {
		m.clipboard.removeSession(id)
	}
	return err
}

// Detach drops the PTY without destroying the underlying tmux session, so the
// work continues and can be reattached later.
func (m *Manager) Detach(id string) error {
	sess, err := m.Get(id)
	if err != nil {
		return err
	}
	if sess.TmuxName == "" {
		return m.Kill(context.Background(), id)
	}
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return sess.Close()
}

// TmuxSession is a persistent session as tmux knows it, which is the only
// record that survives this process restarting.
type TmuxSession struct {
	Name string `json:"name"`
	// Title is the name the operator gave it, stored on the tmux session
	// itself. Without it a session that outlives the dashboard comes back as
	// `vpsd-3f2a91c4` — and three of those side by side are indistinguishable,
	// which is how somebody loses track of which shell was doing what.
	Title string `json:"title,omitempty"`
	// Folder, Favourite and Colour are how a list of fifteen becomes readable.
	// All three live on the tmux session, so they survive everything the work
	// does.
	Folder    string    `json:"folder,omitempty"`
	Favourite bool      `json:"favourite"`
	Colour    string    `json:"colour,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	// CWD is where the session's active pane is now, so a detached session can
	// be identified by the work it is in the middle of.
	CWD      string `json:"cwd,omitempty"`
	Windows  int    `json:"windows"`
	Attached bool   `json:"attached"`
}

// titleOption is a tmux user option. tmux keeps anything prefixed with `@` as
// free-form metadata on the session, which makes the session itself the store
// — exactly the property needed here, since the whole point is to survive the
// process that created it.
const titleOption = "@jd_title"

// fieldSep separates the fields of one listing line.
//
// A printable character, and that is the whole point: tmux escapes
// non-printable bytes in format output, so asking it for a 0x1f separator
// returns the four literal characters `\037` and every line parses as a single
// field. A pipe survives, and is safe here because the only fields that could
// contain one are the path — which is last, and read as the remainder — and
// the title, which is stripped of it on the way in.
const fieldSep = "|"

// TmuxSessions lists persistent sessions that exist in tmux, with enough about
// each to tell them apart — typically ones left behind by a restart, or
// detached after an hour idle.
//
// One call to tmux for all of it: the format string asks for every field at
// once rather than a subprocess per session per field.
//
// It always returns a non-nil slice: a nil slice marshals to JSON null, and a
// list endpoint that answers null instead of [] breaks every consumer that
// reasonably expects to iterate the result.
func (m *Manager) TmuxSessions(ctx context.Context) []TmuxSession {
	out := []TmuxSession{}
	if !m.useTmux {
		return out
	}
	const format = "#{session_name}" + fieldSep + "#{" + titleOption + "}" +
		fieldSep + "#{" + folderOption + "}" + fieldSep + "#{" + favouriteOption + "}" +
		fieldSep + "#{" + colourOption + "}" +
		fieldSep + "#{session_created}" + fieldSep + "#{session_attached}" +
		fieldSep + "#{session_windows}" + fieldSep + "#{pane_current_path}"
	raw, err := hostexec.CommandOnHost(ctx, "tmux", "list-sessions", "-F", format).Output()
	if err != nil {
		// No server running is the ordinary "nothing is persisted" case, not
		// a failure worth reporting.
		return out
	}
	for _, line := range splitLines(string(raw)) {
		if sess, ok := parseTmuxLine(line); ok {
			out = append(out, sess)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// parseTmuxLine reads one line of the listing format.
//
// SplitN, not Split, and the path deliberately last: a directory may contain
// the separator, and everything after the last one is still part of it. The
// fields before it cannot — a session name is `vpsd-` and hex, three are
// numbers, the colour is one of a fixed set, and the title and folder have the
// separator stripped out when they are written.
func parseTmuxLine(line string) (TmuxSession, bool) {
	const fields = 9
	f := strings.SplitN(line, fieldSep, fields)
	if len(f) < fields || !strings.HasPrefix(f[0], sessionPrefix) {
		return TmuxSession{}, false
	}
	sess := TmuxSession{
		Name: f[0], Title: f[1], Folder: f[2], Favourite: f[3] == "1",
		Colour: normaliseColour(f[4]), CWD: f[8],
	}
	if secs, err := strconv.ParseInt(f[5], 10, 64); err == nil {
		sess.CreatedAt = time.Unix(secs, 0).UTC()
	}
	sess.Attached = f[6] != "0"
	sess.Windows, _ = strconv.Atoi(f[7])
	return sess, true
}

// tmuxNames is the membership test Reattach needs, kept separate so the richer
// listing is not what authorisation depends on.
func (m *Manager) tmuxNames(ctx context.Context) []string {
	names := []string{}
	for _, s := range m.TmuxSessions(ctx) {
		names = append(names, s.Name)
	}
	return names
}

// option is one tmux setting to write onto a session.
type option struct{ name, value string }

// rememberOptions writes the session's settings onto tmux, retrying briefly.
//
// Retried, because the session may not exist yet: `tmux new-session` has only
// just been handed to a PTY, and on a host where the tmux *server* also has to
// start, the first attempt lands before there is anything to set an option on.
// That is not hypothetical — it is why the first session opened after a reboot
// came back with no title while the second was fine, and why the copy of these
// values on the Session is what the listing answers from in the meantime.
//
// Best effort throughout: a title that never sticks costs a recognisable name
// after the next restart, not a working session.
func (m *Manager) rememberOptions(tmuxName string, opts ...option) {
	if tmuxName == "" {
		return
	}
	pending := append([]option(nil), opts...)
	if len(pending) == 0 {
		return
	}
	go func() {
		for attempt := 0; attempt < 8 && len(pending) > 0; attempt++ {
			if attempt > 0 {
				time.Sleep(250 * time.Millisecond)
			}
			// A session that has been closed is not a session that is still
			// starting, and the difference matters: without this the retries
			// keep firing `tmux set-option` at a name that will never exist
			// again, and eight rounds of that per closed session is real
			// contention on the one tmux server everything else is waiting on.
			if attempt > 0 && !m.sessionExists(tmuxName) {
				return
			}
			left := pending[:0]
			for _, o := range pending {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := hostexec.CommandOnHost(ctx, "tmux", "set-option", "-t", tmuxName, o.name, o.value).Run()
				cancel()
				if err != nil {
					left = append(left, o)
				}
			}
			pending = left
		}
	}()
}

// sessionExists asks whether this process still holds the session, which is
// the cheap half of "is it worth retrying" — no subprocess, and it is only
// ever wrong in the direction that costs one more attempt.
func (m *Manager) sessionExists(tmuxName string) bool {
	for _, sess := range m.List() {
		if sess.TmuxName == tmuxName {
			return true
		}
	}
	return false
}

// defaultCommand is the login, as the one string tmux's `default-command`
// takes. tmux runs that value through `sh -c`, so it is the one place in this
// package that produces a shell command rather than an argument vector.
//
// What it is built from is why that is acceptable: the account name comes from
// JD_TERMINAL_USER or /etc/passwd and the shell from JD_TERMINAL_SHELL or the
// account's entry — operator configuration resolved once at boot, never
// anything supplied per request. Each field is quoted anyway, so a shell path
// or account name containing a space is passed as one word rather than two.
func (m *Manager) defaultCommand() string {
	argv := m.loginArgv(true)
	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, shellWord(arg))
	}
	return strings.Join(quoted, " ")
}

// shellWord quotes a token so `sh -c` sees exactly one argument.
func shellWord(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("_-./:=@+,", r):
		default:
			safe = false
		}
		if !safe {
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Reattach binds a PTY to an existing tmux session so the operator can pick up
// exactly where they left off.
func (m *Manager) Reattach(ctx context.Context, tmuxName, owner string, rows, cols uint16) (*Session, error) {
	if !m.enabled {
		return nil, ErrDisabled
	}
	if !m.useTmux {
		return nil, ErrNotFound
	}
	// Only the sessions this dashboard created, and only ones that actually
	// exist. Passing the name straight to `tmux attach-session` let an
	// operator attach to anybody's personal tmux session on the host — the
	// listing endpoint already restricts itself to the vpsd- ones, and this is
	// the same rule applied where it is enforced rather than displayed.
	if !slices.Contains(m.tmuxNames(ctx), tmuxName) {
		return nil, ErrNotFound
	}
	// A reattached session is a session; it counts against the same cap.
	release, err := m.reserve()
	if err != nil {
		return nil, err
	}
	defer release()
	if rows == 0 {
		rows = 24
	}
	if cols == 0 {
		cols = 80
	}
	// Everything the operator chose, recovered from the tmux session rather
	// than reinvented: coming back to `vpsd-3f2a91c4` in no folder is the
	// difference between picking up where you left off and guessing which
	// shell this was. Reading it here is also what keeps the in-memory copy
	// authoritative for every session this process holds — it is seeded from
	// tmux on the way in and written on the way out, so it is never behind.
	title := tmuxName
	var meta SessionMeta
	for _, s := range m.TmuxSessions(ctx) {
		if s.Name != tmuxName {
			continue
		}
		if s.Title != "" {
			title = s.Title
		}
		meta = SessionMeta{Folder: s.Folder, Favourite: s.Favourite, Colour: s.Colour}
	}
	id := randomID()
	sess := &Session{
		ID:          id,
		Title:       title,
		folder:      meta.Folder,
		favourite:   meta.Favourite,
		colour:      meta.Colour,
		Shell:       m.shell,
		User:        m.account.Name,
		TmuxName:    tmuxName,
		Persisted:   true,
		CreatedAt:   time.Now().UTC(),
		Owner:       owner,
		Rows:        rows,
		Cols:        cols,
		subscribers: map[int64]chan []byte{},
		events:      map[int64]chan Activity{},
		scrollback:  newRingBuffer(scrollbackKB * 1024),
		lastActive:  time.Now(),
		focusedAt:   time.Now(),
	}
	// The display options are set on the way in as well as at creation, so a
	// session made before they existed — or by an older build — is fixed by
	// being picked up rather than by being recreated. Both are idempotent.
	m.rememberOptions(tmuxName, option{"status", "off"}, option{"mouse", "on"})
	// Sessions created by older dashboard builds have no COLORTERM in their
	// session environment. This cannot rewrite the environment of a process
	// that is already running, but it makes every new window/pane correct and
	// means restarting a TUI after reattach observes the same capabilities as
	// a newly-created session.
	_ = hostexec.CommandOnHost(ctx, "tmux", "set-environment", "-t", tmuxName,
		"COLORTERM", "truecolor").Run()
	cmd := hostexec.CommandOnHost(context.Background(), "tmux", "attach-session", "-t", tmuxName)
	cmd.Env = terminalEnv(os.Environ(), id)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return nil, err
	}
	sess.pty, sess.cmd = f, cmd
	if cmd.Process != nil {
		sess.PID = cmd.Process.Pid
	}
	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()
	go sess.readLoop(func() { m.remove(id) })
	return sess, nil
}

// terminalEnv replaces terminal capability variables rather than appending
// duplicates. execve permits duplicate names and many libc implementations
// return the first one, so append(os.Environ(), "TERM=...") can leave a
// service manager's TERM=dumb in force inside the shell.
func terminalEnv(base []string, sessionID string) []string {
	env := make([]string, 0, len(base)+3)
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "TERM", "COLORTERM", "JD_SESSION":
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"JD_SESSION="+sessionID,
	)
}

// reap closes sessions nobody is attached to and that have seen no output for
// an hour. A forgotten root shell is a standing risk, so it is not left open
// indefinitely; tmux-backed sessions survive this because closing them only
// detaches.
func (m *Manager) reap() {
	t := time.NewTicker(5 * time.Minute)
	for range t.C {
		sessions := m.List()
		if m.clipboard != nil {
			live := make(map[string]bool, len(sessions))
			for _, sess := range sessions {
				live[sess.ID] = true
			}
			m.clipboard.cleanupExpired(time.Now().Add(-ClipboardTTL), live)
		}
		cutoff := time.Now().Add(-idleDetach)
		for _, s := range sessions {
			if s.Attached() > 0 || !s.LastActive().Before(cutoff) {
				continue
			}
			if s.Persisted {
				// Detach only. The tmux session, its processes and its
				// scrollback all continue and it reappears under "still
				// running" with its name — releasing the PTY and the slot
				// without ending anybody's work.
				m.Detach(s.ID)
				continue
			}
			// Nothing is holding this one but us, so releasing the PTY *is*
			// ending it. A forgotten root shell on a host with no tmux is a
			// standing risk, and there is no third option available.
			m.Kill(context.Background(), s.ID)
		}
	}
}

func (m *Manager) Shutdown() {
	for _, s := range m.List() {
		s.Close()
	}
}

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func splitLines(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
