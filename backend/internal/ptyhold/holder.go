package ptyhold

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

const (
	// spawnWait is how long a new holder waits for the dashboard that started
	// it. One that never arrives gave up, and a holder with nothing to hold
	// should not linger.
	spawnWait = 30 * time.Second
	// backlog is how many packets a dashboard may fall behind before the
	// holder lets go of it. The dashboard never stops reading, so a full
	// queue is a peer that has gone, not one that is slow.
	backlog = 512
	// hangupGrace is how long the shell has to leave after a hangup before it
	// is killed, which is what the dashboard did when it owned the PTY.
	hangupGrace = 3 * time.Second
)

// Run is the holder: it listens on socket, runs the session the first
// connection describes, and exits when that session ends.
func Run(socket string) error {
	h := &holder{clients: map[*client]bool{}}
	return h.serve(socket, func() {
		// A stop from systemd is a hangup: the unit is being taken down,
		// and the session with it.
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			<-stop
			h.hangup()
		}()
	})
}

type holder struct {
	pty *os.File
	cmd *exec.Cmd

	mu         sync.Mutex
	scrollback []byte
	meta       json.RawMessage
	clients    map[*client]bool
	ended      bool

	once    sync.Once
	writers sync.WaitGroup
}

// client is one attached dashboard: a queue of packets for it, drained by its
// own writer so a peer that stops reading never stalls the PTY.
type client struct {
	conn *net.UnixConn
	out  chan []byte
}

func (h *holder) serve(socket string, started func()) error {
	ln, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: socket, Net: "unixpacket"})
	if err != nil {
		return err
	}
	// Closing the listener unlinks the socket, which is how the dashboard
	// tells a holder that has ended from one it merely cannot reach.
	defer ln.Close()
	// The socket hands out a shell, so it is root's alone; the directory it
	// is in already is.
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}

	ln.SetDeadline(time.Now().Add(spawnWait))
	conn, err := ln.AcceptUnix()
	if err != nil {
		return err
	}
	ln.SetDeadline(time.Time{})
	kind, payload, err := first(conn)
	if err != nil || kind != kindSpawn {
		conn.Close()
		return errors.New("the first connection did not describe a session")
	}
	var spec Spec
	if err := json.Unmarshal(payload, &spec); err != nil {
		conn.Close()
		return err
	}
	if err := h.start(spec); err != nil {
		reply, _ := json.Marshal(Hello{Version: Version, Error: err.Error()})
		conn.Write(append([]byte{kindHello}, reply...))
		conn.Close()
		return err
	}
	started()
	h.admit(conn, false)
	go h.accept(ln)
	h.pump()
	h.finish()
	return nil
}

func (h *holder) start(spec Spec) error {
	if len(spec.Argv) == 0 {
		return errors.New("no command to run")
	}
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir, cmd.Env = spec.Dir, spec.Env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: spec.Rows, Cols: spec.Cols})
	if err != nil {
		return err
	}
	// creack/pty left the master in blocking mode (see SetSize). Put it back,
	// or the read loop sits in the kernel where a hangup's Close cannot reach
	// it, and the dashboard's copy of the master would inherit the same mode.
	if err := control(f, func(fd int) error { return unix.SetNonblock(fd, true) }); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		f.Close()
		return err
	}
	h.pty, h.cmd, h.meta = f, cmd, spec.Meta
	return nil
}

// first reads a connection's opening packet, and refuses anybody but this
// holder's own user: the directory is root's, and this says so twice.
func first(conn *net.UnixConn) (byte, []byte, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, nil, err
	}
	var cred *unix.Ucred
	raw.Control(func(fd uintptr) {
		cred, _ = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if cred == nil || int(cred.Uid) != os.Geteuid() {
		return 0, nil, errors.New("refused a connection from another user")
	}
	conn.SetReadDeadline(time.Now().Add(handshake))
	buf := make([]byte, MaxPacket)
	n, err := conn.Read(buf)
	if err != nil {
		return 0, nil, err
	}
	conn.SetReadDeadline(time.Time{})
	return buf[0], append([]byte(nil), buf[1:n]...), nil
}

func (h *holder) accept(ln *net.UnixListener) {
	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			return
		}
		go func() {
			if kind, _, err := first(conn); err != nil || kind != kindAttach {
				conn.Close()
				return
			}
			h.admit(conn, true)
		}()
	}
}

// admit queues the hello — and, for a dashboard coming back, everything kept
// since — ahead of any output, under the same lock the read loop takes, so
// nothing written in between is lost or sent twice.
func (h *holder) admit(conn *net.UnixConn, replay bool) {
	h.mu.Lock()
	if h.ended {
		h.mu.Unlock()
		conn.Close()
		return
	}
	var past []byte
	if replay {
		past = h.scrollback
	}
	hello, _ := json.Marshal(Hello{Version: Version, PID: h.cmd.Process.Pid, Meta: h.meta, Replay: len(past)})
	c := &client{conn: conn, out: make(chan []byte, backlog)}
	c.out <- append([]byte{kindHello}, hello...)
	for len(past) > 0 {
		n := min(len(past), chunk)
		c.out <- append([]byte{kindReplay}, past[:n]...)
		past = past[n:]
	}
	h.clients[c] = true
	h.writers.Add(1)
	h.mu.Unlock()
	go h.write(c)
	go h.read(c)
}

func (h *holder) write(c *client) {
	defer h.writers.Done()
	defer c.conn.Close()
	for packet := range c.out {
		if err := h.send(c.conn, packet); err != nil {
			h.drop(c)
			for range c.out {
			}
			return
		}
	}
}

func (h *holder) send(conn *net.UnixConn, packet []byte) error {
	if packet[0] != kindHello {
		_, err := conn.Write(packet)
		return err
	}
	// The master travels with the hello. Control holds the descriptor open for
	// the length of the call, so a hangup racing this cannot leave a different
	// file behind the same number.
	var sent error
	err := control(h.pty, func(fd int) error {
		_, _, sent = conn.WriteMsgUnix(packet, unix.UnixRights(fd), nil)
		return nil
	})
	if err != nil {
		return err
	}
	return sent
}

func (h *holder) read(c *client) {
	buf := make([]byte, MaxPacket)
	for {
		n, err := c.conn.Read(buf)
		if err != nil {
			h.drop(c)
			return
		}
		switch buf[0] {
		case kindMeta:
			h.mu.Lock()
			h.meta = append(json.RawMessage(nil), buf[1:n]...)
			h.mu.Unlock()
		case kindHangup:
			h.hangup()
		}
	}
}

func (h *holder) drop(c *client) {
	h.mu.Lock()
	h.dropLocked(c)
	h.mu.Unlock()
}

func (h *holder) dropLocked(c *client) {
	if h.clients[c] {
		delete(h.clients, c)
		close(c.out)
	}
}

// pump is the one reader of the PTY. It keeps reading whether or not anybody
// is attached, so a program that writes while the dashboard is away is never
// blocked on a full terminal, and it ends when the last process holding the
// terminal lets go — or when a hangup closes the master.
func (h *holder) pump() {
	buf := make([]byte, chunk)
	for {
		n, err := h.pty.Read(buf)
		if n > 0 {
			packet := append([]byte{byte(Output)}, buf[:n]...)
			h.mu.Lock()
			h.scrollback = append(h.scrollback, packet[1:]...)
			if over := len(h.scrollback) - retain; over > 0 {
				h.scrollback = append(h.scrollback[:0], h.scrollback[over:]...)
			}
			for c := range h.clients {
				select {
				case c.out <- packet:
				default:
					h.dropLocked(c)
				}
			}
			h.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// hangup closes this holder's master and tells the shell. The terminal is hung
// up once the attached dashboard has closed its copy too, which it does as it
// asks for this; a shell still there after the grace is killed in finish.
func (h *holder) hangup() {
	h.once.Do(func() {
		h.pty.Close()
		h.cmd.Process.Signal(syscall.SIGHUP)
	})
}

func (h *holder) finish() {
	done := make(chan struct{})
	go func() {
		h.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(hangupGrace):
		h.cmd.Process.Kill()
		<-done
	}
	h.mu.Lock()
	h.ended = true
	for c := range h.clients {
		select {
		case c.out <- []byte{byte(Exit)}:
		default:
		}
		h.dropLocked(c)
	}
	h.mu.Unlock()
	h.pty.Close()
	// Let the exit reach the dashboard before the process goes.
	flushed := make(chan struct{})
	go func() {
		h.writers.Wait()
		close(flushed)
	}()
	select {
	case <-flushed:
	case <-time.After(2 * time.Second):
	}
}
