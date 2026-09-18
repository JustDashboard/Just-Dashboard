package term

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// newTmuxManager gives a manager backed by the machine's real tmux, or skips.
//
// These tests drive tmux rather than a stand-in because the whole class of bug
// they exist to catch lives in the gap between this process and that one: what
// tmux has been told, what it has got round to storing, and what it reports
// half a second later. A fake tmux would answer instantly and pass every one
// of them while the product stayed broken.
func newTmuxManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(true, "", "")
	if !m.useTmux {
		t.Skip("tmux is not installed on this machine")
	}
	if m.accountErr != nil {
		t.Skipf("no account to open a session as: %v", m.accountErr)
	}
	return m
}

func newSession(t *testing.T, m *Manager, opts CreateOptions) *Session {
	t.Helper()
	opts.Persist = true
	sess, err := m.Create(context.Background(), opts)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { m.Kill(context.Background(), sess.ID) })
	return sess
}

// A session created inside a folder is in that folder in the very next
// listing, not in the one after it.
//
// This is the bug that made the feature unusable: `tmux new-session` has only
// been handed to a PTY when Create returns, and the `set-option` that files
// the session away lands up to half a second later. The page refreshes as soon
// as the POST answers, so the new session appeared under "Other" — and then
// jumped into its folder on a later poll, at the moment some *other* session
// was created, which read as the two swapping places.
func TestSessionAnswersWithItsFolderImmediately(t *testing.T) {
	m := newTmuxManager(t)
	sess := newSession(t, m, CreateOptions{Title: "migration", Folder: "deploy", Colour: "blue"})

	meta := sess.Meta()
	if meta.Folder != "deploy" {
		t.Errorf("folder = %q immediately after Create, want %q", meta.Folder, "deploy")
	}
	if meta.Title != "migration" {
		t.Errorf("title = %q, want %q", meta.Title, "migration")
	}
	if meta.Colour != "blue" {
		t.Errorf("colour = %q, want %q", meta.Colour, "blue")
	}
}

// And the same answer comes back through Meta, which is what the listing and
// every partial update read.
func TestMetaPrefersTheLiveSessionOverTmux(t *testing.T) {
	m := newTmuxManager(t)
	sess := newSession(t, m, CreateOptions{Title: "api", Folder: "deploy"})

	meta, err := m.Meta(context.Background(), sess.TmuxName)
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	if meta.Folder != "deploy" || meta.Title != "api" {
		t.Fatalf("Meta = %+v, want folder=deploy title=api", meta)
	}
}

// SetMeta writes through to both stores, so a rename is visible without
// waiting for tmux and survives being read back out of it.
func TestSetMetaIsVisibleImmediatelyAndPersisted(t *testing.T) {
	m := newTmuxManager(t)
	sess := newSession(t, m, CreateOptions{Title: "before"})
	ctx := context.Background()

	// tmux has to have caught up before SetMeta, which addresses the session
	// by name and would otherwise find nothing to write on.
	waitForTmuxSession(t, m, sess.TmuxName)

	want := SessionMeta{Title: "after", Folder: "infra", Favourite: true, Colour: "amber"}
	if err := m.SetMeta(ctx, sess.TmuxName, want); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if got := sess.Meta(); got != want {
		t.Errorf("in-memory meta = %+v, want %+v", got, want)
	}
	for _, s := range m.TmuxSessions(ctx) {
		if s.Name != sess.TmuxName {
			continue
		}
		got := SessionMeta{Title: s.Title, Folder: s.Folder, Favourite: s.Favourite, Colour: s.Colour}
		if got != want {
			t.Errorf("tmux meta = %+v, want %+v", got, want)
		}
		return
	}
	t.Fatal("the session was not in the tmux listing")
}

// An unrecognised colour is dropped rather than stored: it is written into a
// tmux format and read back out of one, and the UI has nothing to draw it as.
func TestNormaliseColourRejectsWhatItCannotDraw(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"blue", "blue"},
		{"  BLUE ", "blue"},
		{"", ""},
		{"chartreuse", ""},
		{"blue|red", ""},
		{"#ff0000", ""},
	} {
		if got := normaliseColour(tc.in); got != tc.want {
			t.Errorf("normaliseColour(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Dragging a window along the strip lands it at the position it was dropped
// at, and takes the windows it passed with it rather than swapping with one.
func TestMoveWindowReordersTheStrip(t *testing.T) {
	m := newTmuxManager(t)
	sess := newSession(t, m, CreateOptions{Title: "reorder"})
	ctx := context.Background()
	waitForTmuxSession(t, m, sess.TmuxName)

	if err := m.RenameWindow(ctx, sess.TmuxName, firstWindow(t, m, sess.TmuxName), "one"); err != nil {
		t.Fatalf("RenameWindow: %v", err)
	}
	for _, name := range []string{"two", "three"} {
		if err := m.NewWindow(ctx, sess.TmuxName, name, ""); err != nil {
			t.Fatalf("NewWindow(%s): %v", name, err)
		}
	}
	if got := windowNames(t, m, sess.TmuxName); !sameStrings(got, []string{"one", "two", "three"}) {
		t.Fatalf("setup produced %v", got)
	}

	// The last window dragged to the front.
	last := windowsOf(t, m, sess.TmuxName)[2]
	if err := m.MoveWindow(ctx, sess.TmuxName, last.Index, 0); err != nil {
		t.Fatalf("MoveWindow: %v", err)
	}
	if got := windowNames(t, m, sess.TmuxName); !sameStrings(got, []string{"three", "one", "two"}) {
		t.Errorf("after moving to the front: %v, want [three one two]", got)
	}

	// And back to the end, which exercises the other direction.
	first := windowsOf(t, m, sess.TmuxName)[0]
	if err := m.MoveWindow(ctx, sess.TmuxName, first.Index, 2); err != nil {
		t.Fatalf("MoveWindow: %v", err)
	}
	if got := windowNames(t, m, sess.TmuxName); !sameStrings(got, []string{"one", "two", "three"}) {
		t.Errorf("after moving to the end: %v, want [one two three]", got)
	}
}

// A window dragged onto another session arrives there, and leaves the one it
// came from.
func TestMoveWindowToAnotherSession(t *testing.T) {
	m := newTmuxManager(t)
	ctx := context.Background()
	src := newSession(t, m, CreateOptions{Title: "source"})
	dst := newSession(t, m, CreateOptions{Title: "dest"})
	waitForTmuxSession(t, m, src.TmuxName)
	waitForTmuxSession(t, m, dst.TmuxName)

	if err := m.NewWindow(ctx, src.TmuxName, "build", ""); err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	moving := windowsOf(t, m, src.TmuxName)[1]
	if err := m.MoveWindowToSession(ctx, src.TmuxName, moving.Index, dst.TmuxName); err != nil {
		t.Fatalf("MoveWindowToSession: %v", err)
	}
	if got := len(windowsOf(t, m, src.TmuxName)); got != 1 {
		t.Errorf("source kept %d windows, want 1", got)
	}
	if got := windowNames(t, m, dst.TmuxName); len(got) != 2 || got[1] != "build" {
		t.Errorf("destination windows = %v, want the moved one appended", got)
	}
}

// A window this dashboard does not own is not a destination, however the name
// is spelled. The listing already restricts itself to `vpsd-` sessions; this
// is the same rule where it is enforced rather than displayed.
func TestMoveWindowRefusesASessionItDoesNotOwn(t *testing.T) {
	m := newTmuxManager(t)
	sess := newSession(t, m, CreateOptions{Title: "source"})
	waitForTmuxSession(t, m, sess.TmuxName)
	err := m.MoveWindowToSession(context.Background(), sess.TmuxName, firstWindow(t, m, sess.TmuxName), "someone-elses-session")
	if err != ErrNotFound {
		t.Fatalf("MoveWindowToSession to a foreign session = %v, want ErrNotFound", err)
	}
}

// Splitting gives a second pane; closing the last one is refused, because tmux
// would take the window with it and "close this pane" must not close the tab.
func TestPanesSplitAndRefuseToCloseTheLast(t *testing.T) {
	m := newTmuxManager(t)
	sess := newSession(t, m, CreateOptions{Title: "panes"})
	ctx := context.Background()
	waitForTmuxSession(t, m, sess.TmuxName)
	window := firstWindow(t, m, sess.TmuxName)

	panes, err := m.Panes(ctx, sess.TmuxName, window)
	if err != nil {
		t.Fatalf("Panes: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("a new window has %d panes, want 1", len(panes))
	}
	if err := m.KillPane(ctx, sess.TmuxName, window, panes[0].Index); err == nil {
		t.Fatal("KillPane closed the window's only pane")
	}

	if err := m.SplitPane(ctx, sess.TmuxName, window, true); err != nil {
		t.Fatalf("SplitPane: %v", err)
	}
	panes, _ = m.Panes(ctx, sess.TmuxName, window)
	if len(panes) != 2 {
		t.Fatalf("after a split there are %d panes, want 2", len(panes))
	}
	if err := m.KillPane(ctx, sess.TmuxName, window, panes[1].Index); err != nil {
		t.Fatalf("KillPane: %v", err)
	}
	if panes, _ = m.Panes(ctx, sess.TmuxName, window); len(panes) != 1 {
		t.Fatalf("after killing one there are %d panes, want 1", len(panes))
	}
}

// A layout outside tmux's named set never reaches tmux: the value is an
// argument to select-layout, where a custom layout string would be accepted
// and anything else is a typo the operator should hear about.
func TestSetLayoutRejectsAnUnknownShape(t *testing.T) {
	m := newTmuxManager(t)
	sess := newSession(t, m, CreateOptions{Title: "layout"})
	waitForTmuxSession(t, m, sess.TmuxName)
	window := firstWindow(t, m, sess.TmuxName)
	if err := m.SetLayout(context.Background(), sess.TmuxName, window, "diagonal"); err == nil {
		t.Fatal("SetLayout accepted a layout tmux does not have")
	}
	if err := m.SetLayout(context.Background(), sess.TmuxName, window, "tiled"); err != nil {
		t.Fatalf("SetLayout(tiled): %v", err)
	}
}

// send-keys is the one route that types into a shell without a keyboard, so
// the key names it accepts are a closed list rather than whatever arrives.
func TestSendKeysRejectsAKeyItDoesNotKnow(t *testing.T) {
	m := newTmuxManager(t)
	sess := newSession(t, m, CreateOptions{Title: "keys"})
	waitForTmuxSession(t, m, sess.TmuxName)
	window := firstWindow(t, m, sess.TmuxName)
	if err := m.SendKeys(context.Background(), sess.TmuxName, window, "", []string{"C-c; rm -rf /"}); err == nil {
		t.Fatal("SendKeys accepted a key name outside the list")
	}
	if err := m.SendKeys(context.Background(), sess.TmuxName, window, "", []string{"C-c"}); err != nil {
		t.Fatalf("SendKeys(C-c): %v", err)
	}
}

func waitForTmuxSession(t *testing.T, m *Manager, name string) {
	t.Helper()
	var pending *Session
	for _, sess := range m.List() {
		if sess.TmuxName == name {
			pending = sess
			break
		}
	}
	// Generous, because what is being waited on is another process starting:
	// tmux may have to boot a server, and a test that fails at two seconds is
	// reporting the machine's load rather than the product's behaviour.
	for attempt := 0; attempt < 200; attempt++ {
		for _, s := range m.TmuxSessions(context.Background()) {
			if s.Name == name {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	// What tmux does say, which is the difference between "slow" and "never
	// created" and is the whole question when this fails.
	names := []string{}
	for _, s := range m.TmuxSessions(context.Background()) {
		names = append(names, s.Name)
	}
	out, err := exec.Command("tmux", "list-sessions").CombinedOutput()
	var startup []byte
	if pending != nil {
		pending.mu.Lock()
		startup = append([]byte(nil), pending.scrollback.Bytes()...)
		pending.mu.Unlock()
		if len(startup) > 2048 {
			startup = startup[len(startup)-2048:]
		}
	}
	t.Fatalf("tmux never reported session %s; manager sees %v; tmux says %q (err %v); startup %q; TMUX_TMPDIR=%s",
		name, names, string(out), err, string(startup), os.Getenv("TMUX_TMPDIR"))
}

func windowsOf(t *testing.T, m *Manager, name string) []Window {
	t.Helper()
	windows, err := m.Windows(context.Background(), name)
	if err != nil {
		t.Fatalf("Windows: %v", err)
	}
	return windows
}

func firstWindow(t *testing.T, m *Manager, name string) int {
	t.Helper()
	windows := windowsOf(t, m, name)
	if len(windows) == 0 {
		t.Fatal("the session has no windows")
	}
	return windows[0].Index
}

func windowNames(t *testing.T, m *Manager, name string) []string {
	t.Helper()
	out := []string{}
	for _, w := range windowsOf(t, m, name) {
		out = append(out, w.Name)
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
