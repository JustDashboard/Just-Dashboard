package remotedesk

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// protocolVersion is what the dashboard speaks to guacd. guacd 1.6 still
// accepts it, and it is the newest the browser client in the frontend knows.
const protocolVersion = "VERSION_1_5_0"

const handshakeTimeout = 15 * time.Second

// Display is what the browser told the dashboard about the window the remote
// screen will be drawn in.
type Display struct {
	Width    int
	Height   int
	DPI      int
	Timezone string
}

func (d Display) normalized() Display {
	if d.Width < 320 || d.Width > 8192 {
		d.Width = 1280
	}
	if d.Height < 240 || d.Height > 8192 {
		d.Height = 720
	}
	if d.DPI < 48 || d.DPI > 480 {
		d.DPI = 96
	}
	if len(d.Timezone) > 64 || strings.ContainsAny(d.Timezone, " ;,") {
		d.Timezone = ""
	}
	return d
}

// The browser client decodes these itself; offering anything else makes guacd
// send formats it would then drop.
var (
	audioTypes = []string{"audio/L8", "audio/L16"}
	imageTypes = []string{"image/png", "image/jpeg", "image/webp"}
)

// handshake does guacd's side of the connection setup, so the parameters,
// and the password among them, never pass through the browser.
func handshake(conn net.Conn, rd *Reader, protocol string, params map[string]string, d Display, user string) error {
	conn.SetDeadline(time.Now().Add(handshakeTimeout))
	defer conn.SetDeadline(time.Time{})
	if _, err := conn.Write(encode("select", protocol)); err != nil {
		return err
	}
	args, err := expect(rd, "args")
	if err != nil {
		return err
	}
	var setup []byte
	setup = append(setup, encode("size", strconv.Itoa(d.Width), strconv.Itoa(d.Height), strconv.Itoa(d.DPI))...)
	setup = append(setup, encode("audio", audioTypes...)...)
	setup = append(setup, encode("video")...)
	setup = append(setup, encode("image", imageTypes...)...)
	if d.Timezone != "" {
		setup = append(setup, encode("timezone", d.Timezone)...)
	}
	if user != "" {
		setup = append(setup, encode("name", user)...)
	}
	values := make([]string, len(args.Args))
	for i, name := range args.Args {
		if strings.HasPrefix(name, "VERSION_") {
			values[i] = protocolVersion
			continue
		}
		values[i] = params[name]
	}
	setup = append(setup, encode("connect", values...)...)
	if _, err := conn.Write(setup); err != nil {
		return err
	}
	_, err = expect(rd, "ready")
	return err
}

func expect(rd *Reader, opcode string) (Instruction, error) {
	for {
		_, in, err := rd.Read()
		if err != nil {
			return in, err
		}
		switch in.Opcode {
		case opcode:
			return in, nil
		case "error":
			return in, errorFrom(in)
		case "nop", "log":
			continue
		default:
			return in, errMalformed
		}
	}
}

// Socket is the browser side of a session: a WebSocket that carries the
// protocol as text frames. wsx.Conn satisfies it.
type Socket interface {
	WriteText([]byte) error
	ReadMessage() (int, []byte, error)
}

// Session is one open connection through guacd.
type Session struct {
	conn     net.Conn
	rd       *Reader
	once     sync.Once
	computer string
	record   int64
}

func (s *Session) Close() error {
	var err error
	s.once.Do(func() { err = s.conn.Close() })
	return err
}

// The browser client sends whole instructions per frame and parses each frame
// it receives as whole instructions, so batches only ever end on a ";".
const batchBytes = 8 << 10

// Relay copies the session both ways until either side ends. It returns the
// error that ended it, nil when the remote side or the browser simply closed.
func (s *Session) Relay(ctx context.Context, ws Socket) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		s.Close()
	}()
	if err := ws.WriteText(encode("", tunnelID())); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- s.toBrowser(ws)
		cancel()
	}()
	fromErr := s.fromBrowser(ctx, ws)
	cancel()
	toErr := <-done
	for _, err := range []error{toErr, fromErr} {
		if err != nil && !closed(err) {
			return err
		}
	}
	return nil
}

func (s *Session) toBrowser(ws Socket) error {
	batch := make([]byte, 0, batchBytes*2)
	for {
		raw, _, err := s.rd.Read()
		if err != nil {
			if len(batch) > 0 {
				ws.WriteText(batch)
			}
			return err
		}
		batch = append(batch, raw...)
		if len(batch) >= batchBytes || s.rd.Buffered() == 0 {
			if err := ws.WriteText(batch); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
}

func (s *Session) fromBrowser(ctx context.Context, ws Socket) error {
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		// The computer ended the session. The browser keeps pinging until it
		// reads the disconnect, and those pings must not keep this alive.
		if ctx.Err() != nil {
			return nil
		}
		rd := newReaderSize(bytes.NewReader(data), len(data)+16)
		var forward []byte
		for {
			raw, in, err := rd.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			// The empty opcode is the tunnel's own channel. The client pings
			// through it and treats a missing echo as a dead connection.
			if in.Opcode == "" {
				if len(in.Args) > 0 && in.Args[0] == "ping" {
					if err := ws.WriteText(raw); err != nil {
						return err
					}
				}
				continue
			}
			forward = append(forward, raw...)
		}
		if len(forward) > 0 {
			if _, err := s.conn.Write(forward); err != nil {
				return err
			}
		}
	}
}

func closed(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) && strings.Contains(netErr.Err.Error(), "use of closed") {
		return true
	}
	return strings.Contains(err.Error(), "websocket: close")
}

func tunnelID() string {
	var b [16]byte
	rand.Read(b[:])
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// RefuseSocket tells the browser why a session could not start, as the
// protocol's own error so the client shows it in place of the screen.
func RefuseSocket(ws Socket, err error) {
	var guac *GuacError
	if !errors.As(err, &guac) {
		guac = &GuacError{Message: err.Error(), Code: statusServerError}
	}
	ws.WriteText(append(encode("", tunnelID()), guac.instruction()...))
}
