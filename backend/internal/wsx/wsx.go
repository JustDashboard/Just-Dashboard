// Package wsx wraps gorilla/websocket with the origin and lifetime policy the
// dashboard needs. Every socket here is already behind the network allowlist,
// authentication and capability middleware — the upgrade itself adds the
// cross-origin check that HTTP handlers get from SameSite cookies.
package wsx

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 25 * time.Second
	// Terminal and exec sockets carry pasted text; anything larger than this
	// is a client bug or an attempt to exhaust memory.
	maxMessageSize = 1 << 20
)

type Upgrader struct {
	up             websocket.Upgrader
	expectedScheme string
	allowedOrigins []origin
}

type origin struct {
	scheme string
	host   string
	port   string
}

func NewUpgrader(allowedOrigins []string, secure bool) *Upgrader {
	expectedScheme := "http"
	if secure {
		expectedScheme = "https"
	}
	u := &Upgrader{expectedScheme: expectedScheme}
	for _, raw := range allowedOrigins {
		if allowed, ok := parseOrigin(strings.TrimSpace(raw)); ok {
			u.allowedOrigins = append(u.allowedOrigins, allowed)
		}
	}
	u.up = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     u.checkOrigin,
	}
	return u
}

// checkOrigin refuses browser sockets from other sites. A WebSocket handshake
// is not subject to CORS, so without this a malicious page in the operator's
// browser could open an authenticated terminal using their session cookie.
func (u *Upgrader) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser clients (scripts using an API token) send no Origin.
		return true
	}
	o, err := url.Parse(origin)
	if err != nil {
		return false
	}
	candidate, ok := originFromURL(o)
	if !ok {
		return false
	}
	sameSite, ok := parseOrigin(u.expectedScheme + "://" + r.Host)
	if ok && candidate == sameSite {
		return true
	}
	for _, allowed := range u.allowedOrigins {
		if candidate == allowed {
			return true
		}
	}
	return false
}

func parseOrigin(raw string) (origin, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return origin{}, false
	}
	return originFromURL(u)
}

func originFromURL(u *url.URL) (origin, bool) {
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Host == "" || u.User != nil ||
		u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return origin{}, false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return origin{}, false
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return origin{scheme: scheme, host: host, port: port}, true
}

func (u *Upgrader) Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	return u.upgrade(w, r, nil)
}

// UpgradeProtocol answers with the subprotocol the client asked for. The
// Guacamole client asks for "guacamole", and a browser drops a handshake
// whose answer does not name the protocol it requested.
func (u *Upgrader) UpgradeProtocol(w http.ResponseWriter, r *http.Request, protocol string) (*Conn, error) {
	var header http.Header
	for _, offered := range websocket.Subprotocols(r) {
		if offered == protocol {
			header = http.Header{"Sec-Websocket-Protocol": {protocol}}
		}
	}
	return u.upgrade(w, r, header)
}

func (u *Upgrader) upgrade(w http.ResponseWriter, r *http.Request, header http.Header) (*Conn, error) {
	c, err := u.up.Upgrade(w, r, header)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(maxMessageSize)
	c.SetReadDeadline(time.Now().Add(pongWait))
	c.SetPongHandler(func(string) error {
		return c.SetReadDeadline(time.Now().Add(pongWait))
	})
	return &Conn{ws: c}, nil
}

// Conn serialises writes, which gorilla requires and which several of our
// endpoints would otherwise violate by writing from a producer goroutine and
// a keepalive ticker at once.
type Conn struct {
	ws     *websocket.Conn
	mu     sync.Mutex
	closed bool
}

func (c *Conn) WriteJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.write(websocket.TextMessage, b)
}

func (c *Conn) WriteText(b []byte) error   { return c.write(websocket.TextMessage, b) }
func (c *Conn) WriteBinary(b []byte) error { return c.write(websocket.BinaryMessage, b) }

func (c *Conn) write(kind int, b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return websocket.ErrCloseSent
	}
	c.ws.SetWriteDeadline(time.Now().Add(writeWait))
	return c.ws.WriteMessage(kind, b)
}

func (c *Conn) ReadMessage() (int, []byte, error) { return c.ws.ReadMessage() }

func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.ws.SetWriteDeadline(time.Now().Add(writeWait))
	c.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	return c.ws.Close()
}

// Keepalive pings the peer and closes the socket when the context ends, so a
// dropped VPN tunnel does not leave a PTY or log tail running forever.
func (c *Conn) Keepalive(ctx context.Context) {
	t := time.NewTicker(pingPeriod)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			c.Close()
			return
		case <-t.C:
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return
			}
			c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			err := c.ws.WriteMessage(websocket.PingMessage, nil)
			c.mu.Unlock()
			if err != nil {
				c.Close()
				return
			}
		}
	}
}

// DrainControl reads and discards inbound frames on push-only sockets. Without
// a reader the pong handler never runs and the connection dies at pongWait.
func (c *Conn) DrainControl(cancel context.CancelFunc) {
	defer cancel()
	for {
		if _, _, err := c.ws.ReadMessage(); err != nil {
			return
		}
	}
}

// Envelope is the frame shape every push endpoint uses, so the frontend can
// route messages with one switch.
type Envelope struct {
	Type  string `json:"type"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
	TS    int64  `json:"ts"`
}

func (c *Conn) Send(kind string, data any) error {
	return c.WriteJSON(Envelope{Type: kind, Data: data, TS: time.Now().UnixMilli()})
}

func (c *Conn) SendError(msg string) error {
	return c.WriteJSON(Envelope{Type: "error", Error: msg, TS: time.Now().UnixMilli()})
}
