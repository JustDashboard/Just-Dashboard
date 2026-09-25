package term

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/ptyhold"
)

// Held sessions: windows whose PTY a holder owns rather than this process, so
// they outlive it — see internal/ptyhold for why the descriptor is the whole
// story. The dashboard restarting, upgrading or crashing lets go of them and
// takes them back at the next boot; they end when the operator closes them or
// the shell exits, and not otherwise.

// holderName is the holder binary, installed beside the dashboard's own.
const holderName = "jd-terminal-holder"

// heldID is the shape of a session id, which names its socket and its unit.
var heldID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// HoldSessions makes new windows outlive this process and takes back the ones
// that already have. It says why when it cannot, and the terminal then works as
// it always did, ending with the dashboard: the holder runs as a systemd unit
// on the host from a copy under the data directory, so it needs root, a host
// running systemd, and a data directory the host sees at the same path.
func (m *Manager) HoldSessions(dataDir string) error {
	if os.Geteuid() != 0 {
		return errors.New("the dashboard is not running as root")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	src := filepath.Join(filepath.Dir(exe), holderName)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if hostexec.CommandOnHost(ctx, "test", "-d", "/run/systemd/system").Run() != nil ||
		!hostexec.AvailableOnHost("systemd-run") {
		return errors.New("the host is not running systemd")
	}
	dir := filepath.Join(dataDir, "terminal")
	// A unix socket's path has 107 bytes to live in, and a data directory
	// deep enough to spend them would fail every new window at bind.
	if len(filepath.Join(dir, "0123456789abcdef.sock")) > 107 {
		return fmt.Errorf("%s is too long a path for the sessions' sockets", dir)
	}
	if err := newClipboardStore(dir).ensureRoot(); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	bin := filepath.Join(dir, holderName)
	if err := installHolder(src, bin); err != nil {
		return err
	}
	if hostexec.CommandOnHost(ctx, "test", "-x", bin).Run() != nil {
		return fmt.Errorf("the host cannot see %s at the same path", dir)
	}
	m.holders = dir
	m.launch = func(ctx context.Context, id, socket string) error {
		// KillMode=process: the unit ending is the holder ending, which is
		// the session's end already. Anything the operator deliberately left
		// running from it — nohup, disown — survives the way it survives an
		// ssh session closing.
		out, err := hostexec.CommandOnHost(ctx, "systemd-run",
			"--unit", "jd-terminal-"+id, "--description", "Just Dashboard terminal session",
			"--collect", "--quiet", "--property", "KillMode=process",
			"--", bin, socket).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
		}
		return nil
	}
	m.adopt()
	return nil
}

// Holding reports whether sessions outlive this process.
func (m *Manager) Holding() bool { return m.holders != "" }

// installHolder copies the holder to where the host can run it. An identical
// copy is left alone: a running holder pins the file it was started from, so
// replacing an unchanged one on every restart would keep a deleted copy alive
// per restart for as long as those sessions run.
func installHolder(src, dst string) error {
	want, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("no terminal holder beside the dashboard: %w", err)
	}
	if have, err := os.ReadFile(dst); err == nil && bytes.Equal(have, want) {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(dst), ".holder-")
	if err != nil {
		return err
	}
	temp := f.Name()
	if _, err = f.Write(want); err == nil {
		err = f.Chmod(0o700)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp, dst)
	}
	if err != nil {
		os.Remove(temp)
	}
	return err
}

// adopt takes back every holder still running from before this process.
func (m *Manager) adopt() {
	entries, err := os.ReadDir(m.holders)
	if err != nil {
		return
	}
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".sock")
		if !ok || !heldID.MatchString(id) {
			continue
		}
		socket := filepath.Join(m.holders, entry.Name())
		link, hello, f, err := ptyhold.Attach(socket)
		if err != nil {
			// Nobody listening is a holder that ended without tidying up — it
			// was killed, or the host went down under it. The socket is all
			// that is left of it.
			if errors.Is(err, syscall.ECONNREFUSED) {
				os.Remove(socket)
			}
			continue
		}
		sess := newHeldSession(id, hello, f, link)
		sess.seed(hello.Past)
		m.mu.Lock()
		m.sessions[id] = sess
		m.mu.Unlock()
		go sess.readHolder(func() { m.remove(id) })
	}
}

// heldMeta is what a holder keeps for the dashboard: everything about a window
// that is not the PTY's own state, so a dashboard that starts after the window
// did can put it back where it was.
type heldMeta struct {
	WorkspaceID string    `json:"workspaceId"`
	WindowName  string    `json:"windowName"`
	WindowNamed bool      `json:"windowNamed,omitempty"`
	WindowOrder int       `json:"windowOrder"`
	Title       string    `json:"title"`
	Named       bool      `json:"named,omitempty"`
	Folder      string    `json:"folder,omitempty"`
	Favourite   bool      `json:"favourite,omitempty"`
	Owner       string    `json:"owner"`
	Shell       string    `json:"shell"`
	User        string    `json:"user"`
	CreatedAt   time.Time `json:"createdAt"`
	CWD         string    `json:"cwd,omitempty"`
}

func newHeldSession(id string, hello ptyhold.Hello, f *os.File, link *ptyhold.Conn) *Session {
	var meta heldMeta
	_ = json.Unmarshal(hello.Meta, &meta)
	rows, cols, err := ptyhold.Size(f)
	if err != nil || rows == 0 || cols == 0 {
		rows, cols = 24, 80
	}
	sess := &Session{
		ID:          id,
		WorkspaceID: meta.WorkspaceID,
		WindowName:  meta.WindowName,
		WindowOrder: meta.WindowOrder,
		Title:       meta.Title,
		Shell:       meta.Shell,
		User:        meta.User,
		CreatedAt:   meta.CreatedAt,
		Owner:       meta.Owner,
		Rows:        rows,
		Cols:        cols,
		PID:         hello.PID,
		CWDHint:     meta.CWD,
		folder:      meta.Folder,
		favourite:   meta.Favourite,
		named:       meta.Named,
		windowNamed: meta.WindowNamed,
		pty:         f,
		holder:      link,
		subscribers: map[int64]chan []byte{},
		events:      map[int64]chan Activity{},
		scrollback:  newRingBuffer(scrollbackKB * 1024),
		lastActive:  time.Now(),
	}
	// A record that never arrived — the dashboard went down in the instant
	// between starting the holder and writing it — leaves a window that is
	// still the operator's, just unnamed.
	if sess.WorkspaceID == "" {
		sess.WorkspaceID = id
	}
	if sess.WindowName == "" {
		sess.WindowName = defaultName
	}
	if sess.Title == "" {
		sess.Title = defaultName
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now().UTC()
	}
	sess.focusedAt = sess.CreatedAt
	return sess
}

// spawnHeld starts a holder for a new window and hands it the login.
func (m *Manager) spawnHeld(ctx context.Context, sess *Session, argv []string, dir string) error {
	socket := filepath.Join(m.holders, sess.ID+".sock")
	if err := m.launch(ctx, sess.ID, socket); err != nil {
		return fmt.Errorf("start the terminal holder: %w", err)
	}
	meta, err := json.Marshal(sess.heldMeta())
	if err != nil {
		return err
	}
	link, hello, f, err := ptyhold.Spawn(socket, ptyhold.Spec{
		Argv: argv, Dir: dir, Env: m.heldEnv(sess.ID, sess.Shell),
		Rows: sess.Rows, Cols: sess.Cols, Meta: meta,
	}, 10*time.Second)
	if err != nil {
		return fmt.Errorf("start the terminal holder: %w", err)
	}
	sess.pty, sess.holder, sess.PID = f, link, hello.PID
	return nil
}

// heldEnv is the environment a held shell starts with. It is built rather
// than inherited: this process's environment carries the dashboard's own
// secrets and is no business of a shell, and the holder's is a systemd
// service's. su replaces most of it for another account anyway; this is what a
// login needs before its profile runs, and what the terminal on the other end
// depends on.
func (m *Manager) heldEnv(id, shell string) []string {
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + m.account.Home,
		"USER=" + m.account.Name,
		"LOGNAME=" + m.account.Name,
		"SHELL=" + shell,
	}
	if lang := os.Getenv("LANG"); lang != "" {
		env = append(env, "LANG="+lang)
	}
	return terminalEnv(env, id)
}

func (s *Session) heldMeta() heldMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return heldMeta{
		WorkspaceID: s.WorkspaceID, WindowName: s.WindowName, WindowNamed: s.windowNamed,
		WindowOrder: s.WindowOrder, Title: s.Title, Named: s.named, Folder: s.folder,
		Favourite: s.favourite, Owner: s.Owner, Shell: s.Shell, User: s.User,
		CreatedAt: s.CreatedAt, CWD: s.CWDHint,
	}
}

// remember sends a window's record to its holder after anything in it
// changed. Best effort: a record that did not arrive costs a name after the
// next restart, not the session. The record is read under the lock the window
// fields are written under and sent after it is released, so a holder slow to
// read cannot hold up the rest of the terminal.
func (m *Manager) remember(sessions ...*Session) {
	records := make([][]byte, len(sessions))
	m.mu.RLock()
	for i, sess := range sessions {
		if sess.holder != nil {
			records[i], _ = json.Marshal(sess.heldMeta())
		}
	}
	m.mu.RUnlock()
	for i, sess := range sessions {
		if records[i] != nil {
			_ = sess.holder.SetMeta(records[i])
		}
	}
}
