// Package ptyhold keeps a terminal open on behalf of a dashboard that may come
// and go.
//
// The web terminal's shells used to be PTYs the dashboard process held, and a
// PTY ends when the last descriptor on its master is closed. Every restart of
// the dashboard closed them all — an upgrade, a settings change, a rebuild,
// a crash — and the hangup the kernel delivers then took the shell and whatever
// was running in it along. An agent left working while its operator closed the
// laptop died at the next restart, which is the opposite of what a shell on a
// server is for. The shells themselves were never inside the container: su
// hands each one to logind, which gives it a session scope of its own. The
// descriptor was the whole problem.
//
// A holder is the smallest thing that can own the master instead. It is a
// separate binary, started as a transient systemd unit on the host so nothing
// that happens to the dashboard's container reaches it. It starts the shell on
// a PTY, reads everything the shell writes — so a program never blocks on a
// full terminal while nobody is watching — keeps the most recent part of it,
// and answers on a unix socket. The dashboard attaches over that socket and is
// handed the master itself (SCM_RIGHTS): keystrokes, resizes and the
// foreground-group question the activity marks are built on go straight to the
// kernel, exactly as they did when the dashboard owned the PTY. Only output
// passes through the holder, because only one process can read it.
//
// It interprets nothing. Every byte reaches the browser as the program wrote
// it, which is the property a direct PTY had and a multiplexer does not: tmux
// keeps a screen of its own and redraws it, and the emulator's capability
// negotiation, scrollback and mouse handling become tmux's instead.
package ptyhold

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Version is the protocol a holder speaks. A holder outlives the dashboard
// that started it — that is its purpose — so the dashboard that attaches next
// may be a newer build, and must still understand the holders already running.
const Version = 1

// Every message is one SOCK_SEQPACKET packet whose first byte says what it is.
// A packet keeps its boundary, which is the whole of the framing.
const (
	// Dashboard to holder.
	kindSpawn  = 'S' // the first packet on the first connection: the Spec to run
	kindAttach = 'A' // the first packet on any later connection
	kindMeta   = 'M' // replace the stored metadata
	kindHangup = 'C' // end the session: hang the terminal up and exit

	// Holder to dashboard. The hello answers a spawn or an attach and carries
	// the master; on an attach it is followed by the output kept from before,
	// and then by the Kinds.
	kindHello  = 'H'
	kindReplay = 'R'
)

// Kind is what a packet from the holder carries.
type Kind byte

const (
	// Output is live output.
	Output Kind = 'O'
	// Exit says the session has ended. Nothing follows it.
	Exit Kind = 'X'
)

const (
	// MaxPacket is the receive buffer a packet always fits in: output is read
	// in chunks smaller than this, and a hello or a metadata record is far
	// smaller.
	MaxPacket = 64 << 10
	chunk     = 32 << 10
	// retain is how much recent output a holder keeps for the next attach —
	// the same bound the dashboard keeps for a reconnecting browser.
	retain = 128 << 10
	// handshake bounds how long either side waits for the other's first
	// packet.
	handshake = 10 * time.Second
)

// Spec is the session a holder runs. The command is started as it is given,
// in Dir, with exactly Env: a holder is a host service, and nothing of the
// environment it was started in belongs to the shell.
type Spec struct {
	Argv []string `json:"argv"`
	Dir  string   `json:"dir,omitempty"`
	Env  []string `json:"env"`
	Rows uint16   `json:"rows"`
	Cols uint16   `json:"cols"`
	// Meta is the dashboard's own record of the session — its name, which
	// session a window belongs to — kept here because this is the one place
	// that survives the dashboard. The holder stores it and hands it back, and
	// never reads it.
	Meta json.RawMessage `json:"meta,omitempty"`
}

// Hello is the holder's answer to a spawn or an attach.
type Hello struct {
	Version int `json:"version"`
	// PID is the process the holder started, whose descendants are the
	// session.
	PID  int             `json:"pid,omitempty"`
	Meta json.RawMessage `json:"meta,omitempty"`
	// Replay is how many bytes of kept output follow an attach's hello.
	Replay int `json:"replay,omitempty"`
	// Error is why a spawn failed. The holder exits after sending it.
	Error string `json:"error,omitempty"`
	// Past is that output, read by Attach before it returns, so a dashboard
	// taking a session back has its history before anybody can ask for it.
	Past []byte `json:"-"`
}

// Conn is the dashboard's end of one holder.
type Conn struct {
	conn *net.UnixConn
	mu   sync.Mutex
}

// Spawn gives a holder that has just been started the session to run, and
// returns the connection, the holder's answer and the PTY master. The holder
// may not be listening yet when its unit has only just been started, so a
// socket that does not exist, or exists but is not listening yet, is retried
// until wait has passed.
func Spawn(socket string, spec Spec, wait time.Duration) (*Conn, Hello, *os.File, error) {
	payload, err := json.Marshal(spec)
	if err != nil {
		return nil, Hello{}, nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		conn, err := dial(socket)
		if err == nil {
			return greet(conn, kindSpawn, payload)
		}
		starting := errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
		if !starting || time.Now().After(deadline) {
			return nil, Hello{}, nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Attach connects to a holder that is already running a session.
func Attach(socket string) (*Conn, Hello, *os.File, error) {
	conn, err := dial(socket)
	if err != nil {
		return nil, Hello{}, nil, err
	}
	return greet(conn, kindAttach, nil)
}

func dial(socket string) (*net.UnixConn, error) {
	return net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: socket, Net: "unixpacket"})
}

// greet sends the first packet and reads the hello, with the master that comes
// with it. Go receives with MSG_CMSG_CLOEXEC, so the descriptor is never
// inherited by a command the dashboard runs later.
func greet(conn *net.UnixConn, kind byte, payload []byte) (*Conn, Hello, *os.File, error) {
	fail := func(err error) (*Conn, Hello, *os.File, error) {
		conn.Close()
		return nil, Hello{}, nil, err
	}
	conn.SetDeadline(time.Now().Add(handshake))
	if _, err := conn.Write(append([]byte{kind}, payload...)); err != nil {
		return fail(err)
	}
	buf := make([]byte, MaxPacket)
	oob := make([]byte, unix.CmsgSpace(4))
	n, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return fail(fmt.Errorf("the terminal holder did not answer: %w", err))
	}
	fds := rights(oob[:oobn])
	var hello Hello
	switch {
	case buf[0] != kindHello || json.Unmarshal(buf[1:n], &hello) != nil:
		err = errors.New("the terminal holder sent something other than a hello")
	case hello.Error != "":
		err = errors.New(hello.Error)
	case hello.Version != Version:
		err = fmt.Errorf("the terminal holder speaks protocol %d, not %d", hello.Version, Version)
	case len(fds) != 1:
		err = errors.New("the terminal holder did not send the terminal")
	}
	for err == nil && len(hello.Past) < hello.Replay {
		n, err = conn.Read(buf)
		if err == nil && buf[0] != kindReplay {
			err = errors.New("the terminal holder's history was cut short")
		}
		if err == nil {
			hello.Past = append(hello.Past, buf[1:n]...)
		}
	}
	if err == nil {
		err = unix.SetNonblock(fds[0], true)
	}
	if err != nil {
		for _, fd := range fds {
			unix.Close(fd)
		}
		return fail(err)
	}
	conn.SetDeadline(time.Time{})
	// Non-blocking before NewFile, so the runtime polls it: a read or write
	// blocked in the kernel would hold the descriptor open past Close.
	return &Conn{conn: conn}, hello, os.NewFile(uintptr(fds[0]), "ptmx"), nil
}

func rights(oob []byte) []int {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil
	}
	var fds []int
	for i := range msgs {
		if got, err := unix.ParseUnixRights(&msgs[i]); err == nil {
			fds = append(fds, got...)
		}
	}
	return fds
}

// Read returns the next packet from the holder. The data aliases buf, which
// must be MaxPacket long.
func (c *Conn) Read(buf []byte) (Kind, []byte, error) {
	n, err := c.conn.Read(buf)
	if err != nil {
		return 0, nil, err
	}
	return Kind(buf[0]), buf[1:n], nil
}

// SetMeta replaces the record the holder keeps for the dashboard.
func (c *Conn) SetMeta(meta []byte) error { return c.send(kindMeta, meta) }

// Hangup ends the session. The caller must also close its copy of the master,
// because the terminal is hung up only once every descriptor on it is closed.
func (c *Conn) Hangup() error { return c.send(kindHangup, nil) }

// Close lets go of the holder, which carries on without us.
func (c *Conn) Close() error { return c.conn.Close() }

func (c *Conn) send(kind byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.conn.Write(append([]byte{kind}, payload...))
	return err
}

// SetSize sets a PTY's window. creack/pty's helpers reach the descriptor
// through Fd, which switches it to blocking mode — and the master of a held
// session shares its file status with the holder's, whose read loop must stay
// interruptible for a hangup to end it. So the ioctl goes through SyscallConn.
func SetSize(f *os.File, rows, cols uint16) error {
	return control(f, func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})
	})
}

// Size reads a PTY's window, for the same reason through SyscallConn.
func Size(f *os.File) (rows, cols uint16, err error) {
	err = control(f, func(fd int) error {
		ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
		if err == nil {
			rows, cols = ws.Row, ws.Col
		}
		return err
	})
	return rows, cols, err
}

func control(f *os.File, fn func(fd int) error) error {
	raw, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var inner error
	if err := raw.Control(func(fd uintptr) { inner = fn(int(fd)) }); err != nil {
		return err
	}
	return inner
}
