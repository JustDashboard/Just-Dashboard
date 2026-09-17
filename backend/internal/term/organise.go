package term

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Organising sessions, and the windows inside them.
//
// A dashboard terminal is not one shell, it is the five an operator keeps open
// — a tail here, a build there, a database console, a root shell for the thing
// that just broke. Every tool in this class stops at "a terminal": Cockpit has
// exactly one and loses it on navigation, ttyd and Wetty have no session
// concept at all, and Portainer's console is per-container and dies with the
// tab. tmux solves the *keeping* and solves none of the *finding*: `vpsd-3f2a`
// next to `vpsd-91c4` is not an answer to "which one was the migration".
//
// So the shape borrowed here is the one that works elsewhere: VS Code's
// terminal tab list, where every tab has a name you set and keeps it, and
// Guacamole's connection groups, where a long list becomes a short one by
// being folded. Favourites pin the two or three you actually live in.
//
// All of it is stored on the tmux session itself, as user options. tmux keeps
// anything prefixed with `@` as free-form metadata, which makes the session its
// own record — the same property that makes the work survive is what makes the
// *name* of the work survive, with no table to migrate and nothing to
// reconcile after a restart.

const (
	folderOption    = "@jd_folder"
	favouriteOption = "@jd_fav"
	colourOption    = "@jd_colour"
)

// Colours is the palette a session, a folder or a window may be tagged with.
//
// A fixed set rather than a free-form value, for two reasons. The obvious one
// is that these have to render against twelve themes, nine of them dark, so
// the hue is chosen here and the *shade* is left to the stylesheet. The other
// is that this string is written into a tmux format and read back out of one:
// an enumeration cannot contain the field separator, and cannot be the vector
// an arbitrary value would be.
var Colours = []string{"slate", "red", "amber", "green", "cyan", "blue", "violet", "pink"}

// NormaliseColour is normaliseColour for callers outside this package — the
// folder record, which stores the same palette against a name that has no
// tmux session to hang off.
func NormaliseColour(v string) string { return normaliseColour(v) }

// SanitiseName applies the same rules to a folder name that a session title
// gets. A folder name is compared against the value stored on a tmux session,
// so it has to survive the same round trip through a tmux format or the two
// stop matching.
func SanitiseName(v string) string { return sanitiseField(v) }

// normaliseColour keeps an unrecognised colour out of the record entirely
// rather than storing it and hoping the UI copes.
func normaliseColour(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, c := range Colours {
		if v == c {
			return v
		}
	}
	return ""
}

// SessionMeta is everything about a session that is the operator's choice
// rather than the shell's state.
type SessionMeta struct {
	Title string `json:"title"`
	// Named is whether Title was the operator's choice rather than the
	// dashboard's default. A chosen name is shown as given; a default gives
	// way to whatever the session is doing.
	Named     bool   `json:"named"`
	Folder    string `json:"folder"`
	Favourite bool   `json:"favourite"`
	Colour    string `json:"colour"`
}

var ErrNoPersistence = errors.New("this session is not tmux-backed, so it has nothing to remember settings on")

// SetMeta renames a session and files it away.
//
// Written straight through to tmux rather than to a local map: the point of
// naming a session is that the name is still there tomorrow, and a name held
// only in this process lasts until the next restart.
func (m *Manager) SetMeta(ctx context.Context, tmuxName string, meta SessionMeta) error {
	clean := SessionMeta{
		Title:     sanitiseField(meta.Title),
		Named:     meta.Named,
		Folder:    sanitiseField(meta.Folder),
		Favourite: meta.Favourite,
		Colour:    normaliseColour(meta.Colour),
	}
	// Direct sessions live only as long as this process, so their organisation
	// belongs in the same in-memory workspace. Apply it to every window so the
	// metadata survives closing whichever PTY happened to be first.
	if windows := m.Workspace(tmuxName); len(windows) > 0 {
		for _, sess := range windows {
			sess.setMeta(clean)
		}
		return nil
	}
	if !m.useTmux || tmuxName == "" {
		return ErrNoPersistence
	}

	// The live session first, so the change shows on the very next listing
	// rather than on whichever poll happens after tmux has answered. It is the
	// same copy Create seeds and Reattach reads back, which is what lets the
	// listing trust it over tmux for a session the dashboard is holding.
	live := false
	for _, sess := range m.List() {
		if sess.TmuxName == tmuxName {
			sess.setMeta(clean)
			live = true
		}
	}
	// A session this process is holding is by construction one it created, so
	// its name needs no further checking. Anything else does: the host's tmux
	// server holds the operator's own sessions too, and renaming one of those
	// is not this dashboard's business.
	if !live && !m.owns(ctx, tmuxName) {
		return ErrNotFound
	}

	favourite := ""
	if clean.Favourite {
		favourite = "1"
	}
	opts := []option{
		{titleOption, clean.Title},
		{folderOption, clean.Folder},
		{favouriteOption, favourite},
		{colourOption, clean.Colour},
	}
	var failed []option
	for _, o := range opts {
		if err := hostexec.CommandOnHost(ctx, "tmux", "set-option", "-t", tmuxName, o.name, o.value).Run(); err != nil {
			failed = append(failed, o)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	// A session created moments ago may not exist in tmux yet — `new-session`
	// has been handed to a PTY and the server may still be starting. Refusing
	// here would mean "file this in a folder" failing for the session most
	// likely to be filed: the one just opened. The in-memory copy is already
	// correct, so the write is retried in the background exactly as Create's
	// is, and the only thing at stake is whether the setting survives the next
	// restart.
	if live {
		m.rememberOptions(tmuxName, failed...)
		return nil
	}
	return errors.New("could not store that on the tmux session")
}

// WorkspaceMeta returns one metadata record per live direct-PTY workspace.
func (m *Manager) WorkspaceMeta() map[string]SessionMeta {
	out := map[string]SessionMeta{}
	for _, sess := range m.List() {
		if sess.TmuxName != "" {
			continue
		}
		if _, exists := out[sess.WorkspaceID]; !exists {
			out[sess.WorkspaceID] = sess.Meta()
		}
	}
	return out
}

// Meta reads a session's settings back.
//
// A live session answers from memory and a merely-running one from tmux, for
// the reason the listing does the same: the in-memory copy is seeded on create
// and reattach and written on every change, so for a session this process is
// holding it is never behind tmux and is sometimes half a second ahead.
//
// It exists so that changing one setting does not require the caller to echo
// the other three — a client that sends a colour and omits the title should
// not silently erase the title.
func (m *Manager) Meta(ctx context.Context, tmuxName string) (SessionMeta, error) {
	if windows := m.Workspace(tmuxName); len(windows) > 0 {
		return windows[0].Meta(), nil
	}
	if !m.useTmux || tmuxName == "" {
		return SessionMeta{}, ErrNoPersistence
	}
	for _, sess := range m.List() {
		if sess.TmuxName == tmuxName {
			return sess.Meta(), nil
		}
	}
	for _, s := range m.TmuxSessions(ctx) {
		if s.Name == tmuxName {
			return SessionMeta{Title: s.Title, Folder: s.Folder, Favourite: s.Favourite, Colour: s.Colour}, nil
		}
	}
	return SessionMeta{}, ErrNotFound
}

// owns reports whether a tmux session is one this dashboard created.
//
// The same rule Reattach applies, for the same reason: the host's tmux server
// holds the operator's own personal sessions too, and neither renaming nor
// listing has any business reaching them.
func (m *Manager) owns(ctx context.Context, tmuxName string) bool {
	if !strings.HasPrefix(tmuxName, sessionPrefix) {
		return false
	}
	for _, s := range m.TmuxSessions(ctx) {
		if s.Name == tmuxName {
			return true
		}
	}
	return false
}

// sanitiseField strips what would break the listing format or a single line.
//
// The separator is replaced rather than dropped so a title that contained one
// stays readable, and control characters go entirely: they would be escaped by
// tmux on the way back out and reappear as literal `\017` in the UI.
func sanitiseField(v string) string {
	v = strings.ReplaceAll(v, fieldSep, "/")
	v = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, v)
	v = strings.TrimSpace(v)
	if len(v) > 64 {
		v = strings.TrimSpace(v[:64])
	}
	return v
}

// Window is one tmux window inside a session — a tab within a tab.
//
// Worth surfacing because tmux already has the concept and an operator who has
// not memorised `C-b c` cannot reach it. It is also the cheap way to keep
// related work together: one session called "deploy" with windows for the
// build, the logs and a shell beats three sessions that have to be named to be
// told apart.
type Window struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
	Panes  int    `json:"panes"`
	CWD    string `json:"cwd,omitempty"`
	// Colour is the window's own tag, held as a *window* option so it belongs
	// to the window rather than to the session. Empty means "inherit the
	// session's", which is what the UI draws.
	Colour string `json:"colour,omitempty"`
	// Bell and Activity are flags tmux has been keeping all along and that
	// nothing in this class surfaces. They are the answer to the question a
	// window strip otherwise cannot answer: which of these five tabs did
	// something while I was looking at a different one. Zoomed is the third
	// piece of tmux state that changes what you are looking at without
	// changing which window you are in.
	Bell     bool `json:"bell"`
	Activity bool `json:"activity"`
	Zoomed   bool `json:"zoomed"`
	// Synchronized reports `synchronize-panes`, where a keystroke goes to
	// every pane at once. It is worth showing prominently rather than
	// discovering: it is the one setting that turns a typo into the same typo
	// on four servers.
	Synchronized bool `json:"synchronized"`
}

// Windows lists the windows of a session, in tmux's own order.
func (m *Manager) Windows(ctx context.Context, tmuxName string) ([]Window, error) {
	out := []Window{}
	if !m.useTmux || tmuxName == "" {
		return out, nil
	}
	if !m.owns(ctx, tmuxName) {
		return nil, ErrNotFound
	}
	// The path last, for the reason the session listing puts it last: a
	// directory may contain the separator and none of the fields before it
	// can — a window name is sanitised on write, the colour is an
	// enumeration, and the rest are flags and numbers.
	const format = "#{window_index}" + fieldSep + "#{window_name}" + fieldSep +
		"#{window_active}" + fieldSep + "#{window_panes}" + fieldSep +
		"#{window_bell_flag}" + fieldSep + "#{window_activity_flag}" + fieldSep +
		"#{window_zoomed_flag}" + fieldSep + "#{?pane_synchronized,1,0}" + fieldSep +
		"#{" + colourOption + "}" + fieldSep + "#{pane_current_path}"
	const fields = 10
	raw, err := hostexec.CommandOnHost(ctx, "tmux", "list-windows", "-t", tmuxName, "-F", format).Output()
	if err != nil {
		return out, nil
	}
	for _, line := range splitLines(string(raw)) {
		f := strings.SplitN(line, fieldSep, fields)
		if len(f) < fields {
			continue
		}
		w := Window{
			Name: f[1], Active: f[2] != "0",
			Bell: f[4] != "0", Activity: f[5] != "0", Zoomed: f[6] != "0",
			Synchronized: f[7] != "0", Colour: normaliseColour(f[8]), CWD: f[9],
		}
		w.Index, _ = strconv.Atoi(f[0])
		w.Panes, _ = strconv.Atoi(f[3])
		out = append(out, w)
	}
	return out, nil
}

// NewWindow opens another window in an existing session.
//
// `-d` so it is created without stealing focus from whatever the operator is
// watching; selecting it is a separate, deliberate act.
func (m *Manager) NewWindow(ctx context.Context, tmuxName, name, cwd string) error {
	if !m.useTmux || tmuxName == "" {
		return ErrNoPersistence
	}
	if !m.owns(ctx, tmuxName) {
		return ErrNotFound
	}
	args := []string{"new-window", "-d", "-t", tmuxName}

	// Where the new window starts. Falling back to the directory the session
	// is currently in, rather than to tmux's default of wherever the session
	// began, is what every tabbed terminal does: a new tab opens beside the
	// one you were looking at, not back at the start.
	dir := hostDir(ctx, cwd)
	if dir == "" {
		dir = hostDir(ctx, tmuxPanePath(tmuxName))
	}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	if n := sanitiseField(name); n != "" {
		args = append(args, "-n", n)
	}

	// The login, passed explicitly. The session also carries a
	// `default-command` that says the same thing — which is what covers a
	// window opened with `C-b c` from inside tmux — but naming it here does
	// not depend on that option having been set, and this is the path the
	// dashboard's own button takes. Without either, tmux runs the shell of
	// whoever started the tmux server: the dashboard, as root.
	args = append(args, m.loginArgv(true)...)
	return hostexec.CommandOnHost(ctx, "tmux", args...).Run()
}

// RenameWindow gives a window a name that says what it is for.
func (m *Manager) RenameWindow(ctx context.Context, tmuxName string, index int, name string) error {
	if !m.useTmux || tmuxName == "" {
		return ErrNoPersistence
	}
	if !m.owns(ctx, tmuxName) {
		return ErrNotFound
	}
	clean := sanitiseField(name)
	if clean == "" {
		return errors.New("a window name is required")
	}
	target := tmuxName + ":" + strconv.Itoa(index)
	return hostexec.CommandOnHost(ctx, "tmux", "rename-window", "-t", target, clean).Run()
}

// ColourWindow tags one window, or clears the tag so it inherits the
// session's. Held as a window option (`-w`) rather than a session one, which
// is what keeps five windows in one session distinguishable.
func (m *Manager) ColourWindow(ctx context.Context, tmuxName string, index int, colour string) error {
	if !m.useTmux || tmuxName == "" {
		return ErrNoPersistence
	}
	if !m.owns(ctx, tmuxName) {
		return ErrNotFound
	}
	target := tmuxName + ":" + strconv.Itoa(index)
	return hostexec.CommandOnHost(ctx, "tmux", "set-option", "-w", "-t", target,
		colourOption, normaliseColour(colour)).Run()
}

// MoveWindow puts a window at another position in the same session.
//
// tmux has no insert: `move-window` refuses an index that is in use and
// `swap-window` exchanges two. So the move is done as the swaps it decomposes
// into, walking the window one position at a time — which is exactly what
// dragging a tab means, and stays correct when the indices are not contiguous
// because a window in the middle was closed. A strip holds a handful of
// windows, so the handful of subprocesses is not worth avoiding with a
// renumber that would move every *other* window's index out from under
// whoever is looking at it.
func (m *Manager) MoveWindow(ctx context.Context, tmuxName string, from, to int) error {
	windows, err := m.Windows(ctx, tmuxName)
	if err != nil {
		return err
	}
	indices := make([]int, 0, len(windows))
	at := -1
	for i, w := range windows {
		indices = append(indices, w.Index)
		if w.Index == from {
			at = i
		}
	}
	if at < 0 {
		return ErrNotFound
	}
	// `to` is a position in the strip, which is how a drag reports itself —
	// not a tmux index, which the operator never sees.
	if to < 0 {
		to = 0
	}
	if to > len(indices)-1 {
		to = len(indices) - 1
	}
	swap := func(a, b int) error {
		return hostexec.CommandOnHost(ctx, "tmux", "swap-window",
			"-d", "-s", tmuxName+":"+strconv.Itoa(a), "-t", tmuxName+":"+strconv.Itoa(b)).Run()
	}
	for at > to {
		if err := swap(indices[at], indices[at-1]); err != nil {
			return err
		}
		at--
	}
	for at < to {
		if err := swap(indices[at], indices[at+1]); err != nil {
			return err
		}
		at++
	}
	return nil
}

// MoveWindowToSession hands a window to another session.
//
// The reason to have it is the reason folders exist: work gets misfiled. A
// build that turned out to belong to the deployment session should be
// draggable there rather than reopened, because reopening loses whatever is
// scrolled back in it.
//
// Both sessions are checked for ownership, not just the source: `move-window`
// takes a target, and a target this dashboard does not own would be a way to
// push a window into the operator's personal tmux.
func (m *Manager) MoveWindowToSession(ctx context.Context, tmuxName string, index int, dest string) error {
	if !m.useTmux || tmuxName == "" {
		return ErrNoPersistence
	}
	if !m.owns(ctx, tmuxName) || !m.owns(ctx, dest) {
		return ErrNotFound
	}
	if tmuxName == dest {
		return errors.New("that window is already in this session")
	}
	// A free index at the end of the destination, because move-window refuses
	// to land on one that is taken.
	next := 0
	existing, err := m.Windows(ctx, dest)
	if err != nil {
		return err
	}
	for _, w := range existing {
		if w.Index >= next {
			next = w.Index + 1
		}
	}
	return hostexec.CommandOnHost(ctx, "tmux", "move-window", "-d",
		"-s", tmuxName+":"+strconv.Itoa(index),
		"-t", dest+":"+strconv.Itoa(next)).Run()
}

// SelectWindow switches which window the attached client is showing.
//
// This is what makes the window strip in the UI a control rather than a
// display: tmux redraws the pane for every attached client, so the browser
// sees the new window without reconnecting anything.
func (m *Manager) SelectWindow(ctx context.Context, tmuxName string, index int) error {
	if !m.useTmux || tmuxName == "" {
		return ErrNoPersistence
	}
	if !m.owns(ctx, tmuxName) {
		return ErrNotFound
	}
	target := tmuxName + ":" + strconv.Itoa(index)
	return hostexec.CommandOnHost(ctx, "tmux", "select-window", "-t", target).Run()
}

// KillWindow closes one window, taking whatever runs in it.
//
// Refused for the last window, because tmux would destroy the session with it
// — and "close this tab" quietly ending the whole session, its siblings and
// their work is the kind of surprise this panel exists to avoid. The caller is
// told to close the session instead, which is a route with a typed
// confirmation in front of it.
func (m *Manager) KillWindow(ctx context.Context, tmuxName string, index int) error {
	if !m.useTmux || tmuxName == "" {
		return ErrNoPersistence
	}
	windows, err := m.Windows(ctx, tmuxName)
	if err != nil {
		return err
	}
	if len(windows) <= 1 {
		return errors.New("this is the session's only window; close the session itself instead")
	}
	target := tmuxName + ":" + strconv.Itoa(index)
	return hostexec.CommandOnHost(ctx, "tmux", "kill-window", "-t", target).Run()
}

// sessionPrefix marks the tmux sessions this dashboard owns, and is what keeps
// every listing and every action off the operator's personal ones.
const sessionPrefix = "vpsd-"

// idleDetach is how long a persisted session may sit with nobody attached
// before its PTY is released.
//
// Long, and it only ever *detaches*: the tmux session, its processes and its
// scrollback all continue, and the session reappears under "still running"
// with its name intact. The point is to give back the PTY and the slot, not to
// end anybody's work — which is why the reaper no longer kills a persisted
// session at all, however long it has been idle.
var idleDetach = 12 * time.Hour
