package dbx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The wire.
//
// Two things here cannot go through the client library. MONITOR turns a
// connection into a feed the library has no good way to read. And the
// console's reply is whatever an operator typed: KEYS * on fifty million keys,
// LRANGE 0 -1 on a list of ten million. The library reads a whole reply into
// memory before anybody can see how big it is, so a single line in a text box
// could take the dashboard down with it.
//
// So both speak the protocol themselves, over one connection each that is
// owned here and closed when the work is done. For the console that means a
// reply is read only as far as a page can show, and the connection is dropped
// on whatever is left.

// redisServerError is an error reply read off the wire. It answers to the
// client library's own test for one, so the two are told apart from a failed
// connection the same way wherever they came from.
type redisServerError string

func (e redisServerError) Error() string { return string(e) }
func (redisServerError) RedisError()     {}

// redisWire is one raw connection to a server.
type redisWire struct {
	conn net.Conn
	rd   *bufio.Reader
	opt  *redis.Options
	stop func() bool
}

// redisDial opens a connection the way the connection string describes it —
// TCP or a unix socket, with TLS for rediss:// — and ties it to ctx: a read
// blocked on a quiet server never sees a context end, and closing the
// connection is what wakes it.
func redisDial(ctx context.Context, dsn string) (*redisWire, error) {
	opt, err := redisOptions(dsn)
	if err != nil {
		return nil, err
	}
	opt.DialTimeout = 8 * time.Second
	return redisDialOptions(ctx, opt)
}

// redisDialOptions is redisDial for a server a client is already talking to:
// another connection to the same place, with the same credentials.
func redisDialOptions(ctx context.Context, opt *redis.Options) (*redisWire, error) {
	network := opt.Network
	if network == "" {
		network = "tcp"
	}
	conn, err := redis.NewDialer(opt)(ctx, network, opt.Addr)
	if err != nil {
		return nil, err
	}
	w := &redisWire{conn: conn, rd: bufio.NewReaderSize(conn, 64<<10), opt: opt}
	w.stop = context.AfterFunc(ctx, func() { conn.Close() })
	return w, nil
}

func (w *redisWire) close() {
	w.stop()
	w.conn.Close()
}

// send writes one command, giving the whole exchange that follows until
// timeout to finish.
func (w *redisWire) send(timeout time.Duration, args ...string) error {
	w.conn.SetDeadline(time.Now().Add(timeout))
	_, err := w.conn.Write(redisEncodeCommand(args))
	return err
}

// ask sends a command whose reply is small and is wanted whole.
func (w *redisWire) ask(args ...string) (RedisReply, error) {
	if err := w.send(8*time.Second, args...); err != nil {
		return RedisReply{}, err
	}
	reader := &redisReplyReader{rd: w.rd, nodes: redisReplyMaxNodes, bytes: redisReplyMaxBytes}
	return reader.read(0)
}

// credentials reports whether the connection string names a user or a
// password. A named user authenticates even with no password, or the
// connection would run as the default user instead of the one it is for.
func (w *redisWire) credentials() bool {
	return w.opt.Username != "" || w.opt.Password != ""
}

// authenticate logs in with AUTH, the form every version understands.
func (w *redisWire) authenticate() error {
	if !w.credentials() {
		return nil
	}
	args := []string{"AUTH", w.opt.Password}
	if w.opt.Username != "" {
		args = []string{"AUTH", w.opt.Username, w.opt.Password}
	}
	reply, err := w.ask(args...)
	if err != nil {
		return err
	}
	if reply.Type == "error" {
		return redisServerError(redisText(reply.Value))
	}
	return nil
}

// login authenticates and selects a database in the older protocol, where
// every reply is a string, a number or a list of them and one reader of a
// command's reply does for every server.
func (w *redisWire) login(db int) error {
	if err := w.authenticate(); err != nil {
		return err
	}
	return w.use(db)
}

// use selects a logical database. Zero is where a connection starts.
func (w *redisWire) use(db int) error {
	if db <= 0 {
		return nil
	}
	reply, err := w.ask("SELECT", strconv.Itoa(db))
	if err != nil {
		return err
	}
	if reply.Type == "error" {
		return redisServerError(redisText(reply.Value))
	}
	return nil
}

// greet negotiates the newer protocol, which is what tells a map from a list
// and a number from the text of one, and falls back to the older for a server
// that has never heard of it. Then it selects the database.
func (w *redisWire) greet(db int) error {
	hello := []string{"HELLO", "3"}
	if w.credentials() {
		user := w.opt.Username
		if user == "" {
			user = "default"
		}
		hello = append(hello, "AUTH", user, w.opt.Password)
	}
	reply, err := w.ask(hello...)
	if err != nil {
		return err
	}
	if reply.Type == "error" {
		msg := redisText(reply.Value)
		if strings.HasPrefix(msg, "WRONGPASS") || strings.Contains(strings.ToLower(msg), "invalid password") {
			return redisServerError(msg)
		}
		// No HELLO, or no RESP3: an older server. It is spoken to the old
		// way, and everything it answers is a string or a list.
		if err := w.authenticate(); err != nil {
			return err
		}
	}
	return w.use(db)
}

// redisEncodeCommand writes a command as a RESP array of bulk strings.
func redisEncodeCommand(args []string) []byte {
	var out []byte
	out = append(out, '*')
	out = strconv.AppendInt(out, int64(len(args)), 10)
	out = append(out, '\r', '\n')
	for _, a := range args {
		out = append(out, '$')
		out = strconv.AppendInt(out, int64(len(a)), 10)
		out = append(out, '\r', '\n')
		out = append(out, a...)
		out = append(out, '\r', '\n')
	}
	return out
}

// redisReadLine reads one CRLF-terminated line, keeping at most max bytes of
// it. A MONITOR line carries its command's arguments whole, so a client
// writing a hundred-megabyte value produces a hundred-megabyte line; the rest
// is read and dropped.
func redisReadLine(rd *bufio.Reader, max int) (string, bool, error) {
	var (
		line      []byte
		truncated bool
	)
	for {
		part, err := rd.ReadSlice('\n')
		if room := max - len(line); room > 0 {
			if len(part) > room {
				part, truncated = part[:room], true
			}
			line = append(line, part...)
		} else if len(part) > 0 {
			truncated = true
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return "", false, err
		}
		return strings.TrimRight(string(line), "\r\n"), truncated, nil
	}
}

const (
	redisReplyMaxNodes  = 10000
	redisReplyMaxBytes  = 2 << 20
	redisReplyMaxString = 256 << 10
	// redisReplyMaxDrain is the most of an over-long string that is read and
	// thrown away to get at what follows it. Past that the rest of the reply
	// is abandoned instead.
	redisReplyMaxDrain = 1 << 20
	redisReplyMaxDepth = 64
	// redisSafeInteger is the largest integer a JavaScript number holds
	// exactly.
	redisSafeInteger = 1<<53 - 1
)

// redisReplyReader reads one reply off the wire into a tree that is never
// larger than a page will show.
//
// A reply is bounded twice: by how many nodes are kept and by how many bytes
// of string. When either runs out the reader marks the node it was in as cut,
// with the size the server declared for it. What happens to the rest of the
// reply is the caller's choice:
//
//   - by default the reader stops. Nothing more is read, and the connection
//     is left in the middle of a reply, good for nothing but closing. That is
//     the console: its one command has been answered as far as anybody will
//     look, and the rest may be gigabytes.
//   - with drain set the rest is read past without being kept, so the
//     connection is ready for the next command. That is a page of a key,
//     which is several commands, and where what is dropped is a few long
//     values rather than a whole keyspace.
type redisReplyReader struct {
	rd    *bufio.Reader
	nodes int
	bytes int
	// maxString is how much of any one string is kept; zero is
	// redisReplyMaxString.
	maxString int
	drain     bool
	// truncated says something was left out; stopped says the reader gave up
	// on the connection to leave it out.
	truncated bool
	stopped   bool
}

func (p *redisReplyReader) line() (string, error) {
	line, cut, err := redisReadLine(p.rd, 64<<10)
	if err != nil {
		return "", err
	}
	if line == "" {
		return "", fmt.Errorf("the server sent an empty reply line")
	}
	if cut {
		p.truncated = true
	}
	return line, nil
}

func (p *redisReplyReader) length(header string) (int, error) {
	n, err := strconv.Atoi(header[1:])
	if err != nil || n < -1 {
		return 0, fmt.Errorf("the server sent a length that is not a number: %.40q", header)
	}
	return n, nil
}

// blob reads a length-prefixed string, keeping as much of it as the budget
// allows.
func (p *redisReplyReader) blob(n int) (string, bool, error) {
	limit := p.maxString
	if limit <= 0 {
		limit = redisReplyMaxString
	}
	keep := n
	if keep > limit {
		keep = limit
	}
	if keep > p.bytes {
		keep = p.bytes
	}
	if keep < 0 {
		keep = 0
	}
	buf := make([]byte, keep)
	if _, err := io.ReadFull(p.rd, buf); err != nil {
		return "", false, err
	}
	p.bytes -= keep
	rest := n - keep
	if rest == 0 {
		_, err := p.rd.Discard(2)
		return string(buf), false, err
	}
	p.truncated = true
	if rest > redisReplyMaxDrain && !p.drain {
		p.stopped = true
		return redisTrimPartialRune(string(buf)), true, nil
	}
	if _, err := io.CopyN(io.Discard, p.rd, int64(rest)+2); err != nil {
		return "", false, err
	}
	return redisTrimPartialRune(string(buf)), true, nil
}

// read reads one value. depth guards against a reply nested without end.
func (p *redisReplyReader) read(depth int) (RedisReply, error) {
	if depth > redisReplyMaxDepth {
		return RedisReply{}, fmt.Errorf("the reply is nested more deeply than this can read")
	}
	header, err := p.line()
	if err != nil {
		return RedisReply{}, err
	}
	p.nodes--
	body := header[1:]
	switch header[0] {
	case '+':
		return RedisReply{Type: "status", Value: body}, nil
	case '-':
		return RedisReply{Type: "error", Value: body}, nil
	case ':':
		n, err := strconv.ParseInt(body, 10, 64)
		if err != nil {
			return RedisReply{}, fmt.Errorf("the server sent an integer that is not one: %.40q", header)
		}
		if n > redisSafeInteger || n < -redisSafeInteger {
			return RedisReply{Type: "integer", Value: body}, nil
		}
		return RedisReply{Type: "integer", Value: n}, nil
	case '_':
		return RedisReply{Type: "nil"}, nil
	case ',':
		f, err := parseRedisScore(body)
		if err != nil {
			f = math.NaN()
		}
		return RedisReply{Type: "double", Value: RedisScore(f)}, nil
	case '#':
		return RedisReply{Type: "boolean", Value: body == "t"}, nil
	case '(':
		return RedisReply{Type: "bignumber", Value: body}, nil
	case '$', '!', '=':
		n, err := p.length(header)
		if err != nil {
			return RedisReply{}, err
		}
		if n < 0 {
			return RedisReply{Type: "nil"}, nil
		}
		text, cut, err := p.blob(n)
		if err != nil {
			return RedisReply{}, err
		}
		switch header[0] {
		case '!':
			return RedisReply{Type: "error", Value: text}, nil
		case '=':
			// A verbatim string leads with its format and a colon: "txt:".
			if len(text) >= 4 && text[3] == ':' {
				text = text[4:]
			}
		}
		out := RedisReply{Type: "string", Value: RedisBytes(text)}
		if cut {
			out.Truncated, out.Length = true, n
		}
		return out, nil
	case '*', '~', '>':
		n, err := p.length(header)
		if err != nil {
			return RedisReply{}, err
		}
		if n < 0 {
			return RedisReply{Type: "nil"}, nil
		}
		out := RedisReply{Type: "array", Items: make([]RedisReply, 0, min(n, 64))}
		for i := 0; i < n; i++ {
			if p.spent() {
				out.Truncated, out.Length = true, n
				return out, p.skip(n-i, depth+1)
			}
			item, err := p.read(depth + 1)
			if err != nil {
				return RedisReply{}, err
			}
			out.Items = append(out.Items, item)
			if p.stopped {
				out.Truncated, out.Length = i+1 < n, n
				return out, nil
			}
		}
		return out, nil
	case '%', '|':
		n, err := p.length(header)
		if err != nil {
			return RedisReply{}, err
		}
		out := RedisReply{Type: "map", Entries: make([]RedisReplyEntry, 0, min(max(n, 0), 64))}
		for i := 0; i < n; i++ {
			if p.spent() {
				out.Truncated, out.Length = true, n
				return out, p.skip(2*(n-i), depth+1)
			}
			key, err := p.read(depth + 1)
			if err != nil {
				return RedisReply{}, err
			}
			var value RedisReply
			if !p.stopped {
				if value, err = p.read(depth + 1); err != nil {
					return RedisReply{}, err
				}
			}
			if p.stopped {
				out.Truncated, out.Length = true, n
				return out, nil
			}
			out.Entries = append(out.Entries, RedisReplyEntry{Key: key, Value: value})
		}
		if header[0] == '|' {
			// An attribute is a note about the reply that follows it, not the
			// reply. It is read past.
			return p.read(depth)
		}
		return out, nil
	}
	return RedisReply{}, fmt.Errorf("the server sent a reply this cannot read: %.40q", header)
}

// spent marks the reply as cut once either budget has run out, and says so.
func (p *redisReplyReader) spent() bool {
	if p.nodes > 0 && p.bytes > 0 && !p.stopped {
		return false
	}
	p.truncated = true
	if !p.drain {
		p.stopped = true
	}
	return true
}

// skip reads past n values without keeping any of them, when the reader is
// one that drains. One that stops has nothing to do here: it is finished
// with the connection.
func (p *redisReplyReader) skip(n, depth int) error {
	if !p.drain {
		return nil
	}
	if depth > redisReplyMaxDepth {
		return fmt.Errorf("the reply is nested more deeply than this can read")
	}
	for ; n > 0; n-- {
		header, err := p.line()
		if err != nil {
			return err
		}
		switch header[0] {
		case '$', '!', '=':
			size, err := p.length(header)
			if err != nil {
				return err
			}
			if size >= 0 {
				if _, err := io.CopyN(io.Discard, p.rd, int64(size)+2); err != nil {
					return err
				}
			}
		case '*', '~', '>':
			size, err := p.length(header)
			if err != nil {
				return err
			}
			if err := p.skip(max(size, 0), depth+1); err != nil {
				return err
			}
		case '%', '|':
			size, err := p.length(header)
			if err != nil {
				return err
			}
			if err := p.skip(2*max(size, 0), depth+1); err != nil {
				return err
			}
			if header[0] == '|' {
				// The attribute is skipped; the value it describes is still
				// to come and is one of the n.
				n++
			}
		}
	}
	return nil
}

// generic turns a reply tree back into the plain values the client library's
// own generic reads produce, so one parser reads a COMMAND reply from either.
func (r RedisReply) generic() any {
	switch r.Type {
	case "array":
		out := make([]any, len(r.Items))
		for i, item := range r.Items {
			out[i] = item.generic()
		}
		return out
	case "map":
		out := make(map[any]any, len(r.Entries))
		for _, e := range r.Entries {
			out[redisText(e.Key.generic())] = e.Value.generic()
		}
		return out
	case "string":
		if b, ok := r.Value.(RedisBytes); ok {
			return string(b)
		}
	case "double":
		if f, ok := r.Value.(RedisScore); ok {
			return float64(f)
		}
	case "error":
		return errors.New(redisText(r.Value))
	}
	return r.Value
}

// RedisConsole is one connection for one console command: opened, asked
// about the command, given it, and closed.
type RedisConsole struct {
	wire    *redisWire
	db      int
	timeout time.Duration
}

// RedisConsoleOpen dials, authenticates and selects db (RedisDSNDatabase for
// the connection string's own). timeout bounds how long the command it is
// then given may take to answer.
func RedisConsoleOpen(ctx context.Context, dsn string, db int, timeout time.Duration) (*RedisConsole, error) {
	wire, err := redisDial(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if db < 0 {
		db = wire.opt.DB
	}
	if err := wire.greet(db); err != nil {
		wire.close()
		return nil, err
	}
	return &RedisConsole{wire: wire, db: db, timeout: timeout}, nil
}

func (c *RedisConsole) Close() { c.wire.close() }

// DB is the logical database the console is on.
func (c *RedisConsole) DB() int { return c.db }

// Lookup asks the server about one command. It answers nil, without an
// error, for a command the server does not have or will not describe: the
// classifier treats both as unknown.
func (c *RedisConsole) Lookup(args []string) *RedisCommandFlags {
	if len(args) == 0 {
		return nil
	}
	reply, err := c.wire.ask("COMMAND", "INFO", args[0])
	if err != nil || reply.Type != "array" || len(reply.Items) != 1 {
		return nil
	}
	entry := redisCommandEntry(reply.Items[0].generic())
	if entry == nil {
		return nil
	}
	if redisContainers[strings.ToUpper(args[0])] && len(args) > 1 {
		want := strings.ToLower(args[0] + "|" + args[1])
		for i := range entry.subs {
			if strings.ToLower(entry.subs[i].Name) == want {
				return &entry.subs[i].RedisCommandFlags
			}
		}
	}
	return &entry.RedisCommandFlags
}

// Run sends one already-classified command and reads as much of its reply as
// a page will show. truncated says there was more.
//
// An error reply from the server — a wrong type, a syntax error — is a result
// and comes back as a reply of type error. The error this returns is the
// other kind: the command never got an answer.
//
// The console is spent afterwards whether or not the reply was read to its
// end, and must be closed.
func (c *RedisConsole) Run(args []string) (RedisReply, bool, time.Duration, error) {
	started := time.Now()
	if err := c.wire.send(c.timeout, args...); err != nil {
		return RedisReply{}, false, time.Since(started), err
	}
	reader := &redisReplyReader{rd: c.wire.rd, nodes: redisReplyMaxNodes, bytes: redisReplyMaxBytes}
	reply, err := reader.read(0)
	elapsed := time.Since(started)
	if err != nil {
		return RedisReply{}, false, elapsed, err
	}
	if reply.Type == "error" && !reader.stopped {
		reply.Value = c.explain(redisText(reply.Value))
	}
	return reply, reader.truncated, elapsed, nil
}

// explain rewrites the refusals that mean "this is not a single server" as
// RedisExplainError does for the client library's errors.
func (c *RedisConsole) explain(msg string) string {
	if sentence, ok := redisExplainCluster(msg); ok {
		return sentence
	}
	if strings.Contains(msg, "unknown command") {
		if info, err := c.wire.ask("INFO", "server"); err == nil && info.Type == "string" {
			if redisInfoMap(parseRedisInfo(redisText(info.generic())))["redis_mode"] == "sentinel" {
				return ErrRedisSentinel.Error()
			}
		}
	}
	return msg
}
