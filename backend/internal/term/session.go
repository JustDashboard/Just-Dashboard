// Package term runs real PTY sessions and multiplexes them over WebSockets.
//
// This is the most dangerous surface in the dashboard: a shell here is a shell
// on the host, with whatever privileges this process holds. It is gated on the
// terminal capability, can be disabled outright in configuration, and every
// session open, attach and close is written to the audit trail.
package term

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/ptyhold"
)

var (
	ErrDisabled = errors.New("the web terminal is disabled in this dashboard's configuration")
	ErrNotFound = errors.New("terminal session not found")
	ErrTooMany  = errors.New("too many terminal sessions are already open")
	// The limit counts PTY windows, because each direct window owns a process,
	// file descriptor and reader goroutine even when no browser is attached.
	maxSessions  = 32
	scrollbackKB = 128
)

// Session is one PTY. Output is fanned out to every attached client and kept
// in a bounded buffer as best-effort shell history for reconnecting a browser.
type Session struct {
	ID string `json:"id"`
	// WorkspaceID groups direct PTYs into the session/window model exposed by
	// the dashboard. Direct mode deliberately has no multiplexer: every window
	// is its own real PTY.
	WorkspaceID string `json:"workspaceId"`
	WindowName  string `json:"windowName"`
	WindowOrder int    `json:"windowOrder"`
	Title       string `json:"title"`
	Shell       string `json:"shell"`
	// User is the host account the shell runs as, which is the answer to
	// "whoami" without having to open the session and ask.
	User      string    `json:"user"`
	Persisted bool      `json:"persisted"`
	TmuxName  string    `json:"tmuxName,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	Owner     string    `json:"owner"`
	Rows      uint16    `json:"rows"`
	Cols      uint16    `json:"cols"`
	PID       int       `json:"pid"`
	// CWDHint is the directory the session was asked to start in, kept for the
	// case where /proc cannot answer.
	CWDHint string `json:"-"`

	// Workspace metadata is copied to every direct window so listing any live
	// member reconstructs the same session row.
	folder    string
	favourite bool
	colour    string

	mu  sync.Mutex
	pty *os.File
	cmd *exec.Cmd
	// holder is set when a holder owns the PTY (held.go). pty is then this
	// process's copy of the master, for input, size and the foreground group,
	// and the output arrives from the holder; cmd is nil, because the shell is
	// not this process's child.
	holder      *ptyhold.Conn
	subscribers map[int64]chan []byte
	// events carries activity changes to the subscribers that asked for them,
	// beside their output channel rather than inside it: the output stream is
	// bytes whose order is sacred, and a state frame is not part of it.
	events     map[int64]chan Activity
	nextSub    atomic.Int64
	scrollback *ringBuffer
	closed     bool
	lastActive time.Time

	// What the window is doing — see activity.go. titles is fed only by the
	// read loop; the rest is read under mu.
	titles     oscScanner
	title      string
	titleOwner int
	activity   Activity
	observed   time.Time
	tick       *time.Timer
	focusedAt  time.Time
	// The raw material of Working: the job as last seen, when output last
	// arrived and when the current run of it began, when the operator last
	// typed (so an echo is not mistaken for work), the last CPU reading, and
	// when the last stretch of work ended.
	job         jobState
	lastOutput  time.Time
	activeSince time.Time
	lastInput   time.Time
	cpu         cpuSample
	finishedAt  time.Time
	// named is whether the operator titled the workspace; windowNamed whether
	// they named this window. A name somebody chose is shown as given; a
	// default follows whatever the shell is doing.
	named       bool
	windowNamed bool
}

func (s *Session) Attached() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subscribers)
}

// Size reads the window under the lock Resize writes it under. The listing
// handler runs on a different goroutine from the socket that resizes, so
// reading the fields directly was a race the detector flags — benign on amd64,
// but there is no reason to keep it.
func (s *Session) Size() (rows, cols uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Rows, s.Cols
}

func (s *Session) LastActive() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActive
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Meta reads back everything about the session that was the operator's choice
// rather than the shell's state.
func (s *Session) Meta() SessionMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionMeta{
		Title: s.Title, Named: s.named, Folder: s.folder, Favourite: s.favourite, Colour: s.colour,
	}
}

// setMeta records the operator's choice on the live session. Callers write
// tmux as well; this is only what keeps the answer correct until tmux catches
// up, and correct afterwards without a round trip.
func (s *Session) setMeta(meta SessionMeta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if meta.Title != "" {
		s.Title = meta.Title
	}
	s.named = meta.Named
	s.folder, s.favourite, s.colour = meta.Folder, meta.Favourite, meta.Colour
}

// Subscribe returns the bounded output suffix plus a channel of subsequent
// output. The suffix and subscription are taken under the same lock so no
// output can slip between them. Callers must not mistake the suffix for an
// emulator snapshot: it may begin halfway through terminal protocol state.
func (s *Session) Subscribe() (snapshot []byte, id int64, ch chan []byte, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, 0, nil, ErrNotFound
	}
	id = s.nextSub.Add(1)
	ch = make(chan []byte, 64)
	s.subscribers[id] = ch
	return s.scrollback.Bytes(), id, ch, nil
}

func (s *Session) Unsubscribe(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.subscribers[id]; ok {
		delete(s.subscribers, id)
		close(ch)
	}
	s.dropEventsLocked(id)
}

func (s *Session) Write(p []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, ErrNotFound
	}
	s.lastActive = time.Now()
	s.lastInput = s.lastActive
	f := s.pty
	s.mu.Unlock()
	return f.Write(p)
}

// Resize sets the PTY's window, and reports whether it actually changed.
//
// The answer matters to the caller: a size change is what makes a full-screen
// program redraw, and what leaves the browser's emulator and tmux disagreeing
// about the result — see Manager.Redraw. A resize frame that says what the PTY
// already is arrives constantly (every layout nudge in the page sends one) and
// must not be treated as an event.
func (s *Session) Resize(rows, cols uint16) (changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, ErrNotFound
	}
	if rows == 0 || cols == 0 {
		return false, nil
	}
	changed = s.Rows != rows || s.Cols != cols
	if !changed {
		return false, nil
	}
	if err := ptyhold.SetSize(s.pty, rows, cols); err != nil {
		return false, err
	}
	s.Rows, s.Cols = rows, cols
	return true, nil
}

// SynchronizeSize unconditionally applies the browser's authoritative size.
// Resize can skip a duplicate during a drag, but reconnect is a boundary at
// which the cached fields must not be trusted more than the kernel PTY.
func (s *Session) SynchronizeSize(rows, cols uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrNotFound
	}
	if rows == 0 || cols == 0 {
		return nil
	}
	if err := ptyhold.SetSize(s.pty, rows, cols); err != nil {
		return err
	}
	s.Rows, s.Cols = rows, cols
	return nil
}

// maxPending bounds how far behind one attached browser may fall before it is
// disconnected. A full-screen repaint at a normal size is a few kilobytes, so
// this is a client that has stopped reading rather than one that is merely
// slow.
const maxPending = 4 << 20

// broadcast delivers output to attached clients.
//
// A terminal stream is not a sequence of independent messages: every byte is a
// state transition applied to the screen the previous bytes produced. Dropping
// a chunk from the middle of one — which is what this used to do when a
// subscriber's channel was full — does not cost the reader "a frame". It
// leaves their emulator's grid permanently out of step with what the program
// thinks it drew, and the program will never send those cells again because as
// far as it is concerned they arrived. What that looks like from the outside is
// a prompt stuck on screen that survives `clear`, a line missing from the
// middle of the output, a pane that has half of another pane's border in it —
// and it is worst exactly when the burst is biggest, which is a pane being
// split or a window being resized.
//
// So a slow client is never given a corrupted stream. When its queue is full
// the pending chunks are drained and coalesced with the new one: the bytes and
// their order are preserved exactly, and the queue goes back to holding one
// item. Only a client that is not reading at all — more than maxPending
// buffered — is dropped, and dropped *completely*, by closing its channel.
// That ends its socket. A tmux-backed reconnect is reconstructed by tmux; a
// direct PTY receives its bounded best-effort output history.
//
// The original concern was right, and is still met: a stalled browser cannot
// wedge the PTY for anybody else. Nothing here blocks the reader.
func (s *Session) broadcast(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastActive = time.Now()
	s.scrollback.Write(chunk)
	for id, ch := range s.subscribers {
		select {
		case ch <- chunk:
			continue
		default:
		}
		// The queue is full. Take everything out of it, in order, and put it
		// back as one chunk with the new bytes on the end.
		merged := make([]byte, 0, len(chunk)+cap(ch)*512)
		for {
			select {
			case pending := <-ch:
				merged = append(merged, pending...)
				continue
			default:
			}
			break
		}
		merged = append(merged, chunk...)
		if len(merged) > maxPending {
			// Not slow — gone. A closed channel is what tells its socket to
			// finish; Unsubscribe on the way out is then a no-op.
			delete(s.subscribers, id)
			close(ch)
			s.dropEventsLocked(id)
			continue
		}
		select {
		case ch <- merged:
		default:
			// The reader emptied nothing and the channel is full again, which
			// can only mean it is not reading. Same answer as above.
			delete(s.subscribers, id)
			close(ch)
			s.dropEventsLocked(id)
		}
	}
}

func (s *Session) readLoop(onExit func()) {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.broadcast(chunk)
			s.noteOutput(chunk)
		}
		if err != nil {
			onExit()
			return
		}
	}
}

// readHolder is readLoop for a held session: the holder reads the PTY and
// this reads the holder.
func (s *Session) readHolder(onExit func()) {
	defer onExit()
	buf := make([]byte, ptyhold.MaxPacket)
	for {
		kind, data, err := s.holder.Read(buf)
		if err != nil || kind == ptyhold.Exit {
			return
		}
		chunk := bytes.Clone(data)
		s.broadcast(chunk)
		s.noteOutput(chunk)
	}
}

// seed takes the output a holder kept from before this process attached. It
// goes into the scrollback a reconnecting browser is sent, and past the title
// scanner so the tab is named as it was, but it is not output now: nobody is
// attached yet to be sent it twice, and it does not count as work.
func (s *Session) seed(past []byte) {
	for _, title := range s.titles.feed(past) {
		s.setTitle(title)
	}
	s.mu.Lock()
	s.scrollback.Write(past)
	s.mu.Unlock()
}

// Close tears down this process's hold on the PTY. A PTY this process owns ends
// with it; a tmux-backed session keeps running inside tmux and a held one
// inside its holder, so closing either only lets go of it, which is what makes
// a session survive a dashboard restart. Ending one is Manager.Kill.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for id, ch := range s.subscribers {
		delete(s.subscribers, id)
		close(ch)
		s.dropEventsLocked(id)
	}
	if s.tick != nil {
		s.tick.Stop()
		s.tick = nil
	}
	f, cmd, link := s.pty, s.cmd, s.holder
	s.mu.Unlock()

	if link != nil {
		link.Close()
	}
	if f != nil {
		f.Close()
	}
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
	return nil
}

// CWD reports the shell's current directory, which is what in-session file
// upload and download resolve relative paths against.
func (s *Session) CWD() string {
	s.mu.Lock()
	pid, tmuxName := s.PID, s.TmuxName
	s.mu.Unlock()

	// A tmux-backed session is the one case the process walk below cannot
	// answer. What we spawned is the tmux *client*; the shell is a child of
	// the tmux **server**, in a different process tree entirely, so following
	// our own descendants finds the client and reports its directory — which
	// is `/`, for every session, however the operator has navigated. tmux
	// tracks the pane's directory across every `cd`, so ask the thing that
	// knows.
	if tmuxName != "" {
		if dir := tmuxPanePath(tmuxName); dir != "" {
			return dir
		}
	}
	if pid == 0 {
		return ""
	}
	// The process we spawned is no longer the shell: a session is nsenter,
	// then su, then the login shell, and each of those only forwards to the
	// next. Their cwd never changes, so reading the leader's would pin this to
	// wherever the session started and quietly stop tracking `cd` — the one
	// thing this function exists to follow.
	//
	// Walking down to the innermost descendant finds the shell again, and
	// keeps finding it when the shell itself forks (tmux adds another layer,
	// and a running command adds one more).
	if link, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", leafDescendant(pid))); err == nil {
		return link
	}
	return s.CWDHint
}

// leafDescendant follows the single-child chain from pid to its innermost
// process. It stops where the chain branches, because two children mean there
// is no longer one obvious "the shell" to follow, and the last unambiguous
// process is a better answer than an arbitrary one of them.
//
// The container shares the host's PID namespace, so these are host PIDs and
// /proc is the host's process table — the same reason the process list page
// can see the server's services.
func leafDescendant(pid int) int {
	// Bounded so that a pathological chain, or a /proc that starts pointing at
	// itself, cannot spin here.
	for depth := 0; depth < 16; depth++ {
		kids := childrenOf(pid)
		if len(kids) != 1 {
			return pid
		}
		pid = kids[0]
	}
	return pid
}

func childrenOf(pid int) []int {
	// The kernel exposes children per-thread; a shell is single-threaded in
	// practice, but reading the whole task directory is what makes this
	// correct for anything that is not.
	tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
	if err != nil {
		return nil
	}
	var out []int
	for _, t := range tasks {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", pid, t.Name()))
		if err != nil {
			continue
		}
		for _, f := range strings.Fields(string(raw)) {
			if n, err := strconv.Atoi(f); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

// ringBuffer keeps the most recent output for reattachment, bounded so a
// process printing endlessly cannot grow the dashboard's heap.
type ringBuffer struct {
	buf  []byte
	size int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, 0, size), size: size}
}

func (r *ringBuffer) Write(p []byte) {
	if len(p) >= r.size {
		r.buf = append(r.buf[:0], p[len(p)-r.size:]...)
		return
	}
	if len(r.buf)+len(p) > r.size {
		drop := len(r.buf) + len(p) - r.size
		r.buf = append(r.buf[:0], r.buf[drop:]...)
	}
	r.buf = append(r.buf, p...)
}

func (r *ringBuffer) Bytes() []byte {
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}

// tmuxPanePath asks tmux where a session's active pane currently is.
//
// One short-lived subprocess, and only on the paths that need it: this is
// called when an in-session upload or download has to resolve a relative path,
// not on a poll.
func tmuxPanePath(name string) string {
	cmd := hostexec.CommandOnHost(context.Background(), "tmux",
		"display-message", "-p", "-t", name, "#{pane_current_path}")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
