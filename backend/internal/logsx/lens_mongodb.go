package logsx

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// mongod 4.4 and later log one JSON object per line:
// {"t":{"$date":…},"s":"I","c":"NETWORK","id":22943,"ctx":"listener",
// "msg":"Connection accepted","attr":{…}}. ParseLine reads it as structured,
// but by the generic rules — "s" is not a level key, "t" is an object rather
// than a time, and attr is flattened into one capped string — so the lens
// reads the line itself. The id is stable across versions for a given log
// statement, which is what it keys on first; the message text is the
// fallback for statements whose id the docs do not pin down.
//
// A mongod log is mostly connections opening and closing, and decoding each
// of those lines as JSON cost the lens several times what it costs to read
// any other database's line. mongod writes t, s, c, id, ctx and msg first and
// in that order, so the lens reads those by hand, takes a connection's
// address straight out of attr, and decodes attr as JSON only for the lines
// that need more of it — a slow query, a login. A line that does not have
// that shape is decoded whole.

func init() {
	register(&Lens{
		ID: "mongodb",
		Events: []string{
			"connection", "disconnection", "client_metadata", "slow", "authorized",
			"auth_failed", "startup", "ready", "shutdown", "checkpoint", "replication", "index",
		},
		Attrs: []string{
			"component", "ctx", "ns", "plan", "duration_ms", "docs_examined", "keys_examined",
			"rows", "client", "port", "app", "user", "code", "query", "fp",
		},
		New: func() Reader { return ReaderFunc(readMongo) },
	})
}

// mongoHead is the front of a line. msg is as written, escapes and all: it is
// only compared with messages that have none.
type mongoHead struct {
	date, s, c, ctx, msg string
	id                   int64
	// attr is the attr object's JSON, "" when the line has none.
	attr string
}

// mongoAttr is the part of attr the lens reads. A field that holds something
// else in another statement — "error" is a string in a failed login and an
// object elsewhere — is kept raw.
type mongoAttr struct {
	Remote string `json:"remote"`
	Doc    struct {
		Application struct {
			Name string `json:"name"`
		} `json:"application"`
	} `json:"doc"`
	PrincipalName  string          `json:"principalName"`
	Error          json.RawMessage `json:"error"`
	NS             string          `json:"ns"`
	AppName        string          `json:"appName"`
	Command        json.RawMessage `json:"command"`
	PlanSummary    string          `json:"planSummary"`
	KeysExamined   json.Number     `json:"keysExamined"`
	DocsExamined   json.Number     `json:"docsExamined"`
	NReturned      json.Number     `json:"nreturned"`
	DurationMillis json.Number     `json:"durationMillis"`
	ErrName        string          `json:"errName"`
}

func readMongo(l *Line) {
	if !strings.HasPrefix(l.Text, `{"t":{"$date":"`) {
		return
	}
	h, ok := mongoParse(l.Text)
	if !ok {
		if h, ok = mongoDecode(l.Text); !ok {
			return
		}
	}
	l.Timestamp = mongoTime(h.date)
	switch {
	case h.s == "F":
		l.SetLevel("critical")
	case h.s == "E":
		l.SetLevel("error")
	case h.s == "W":
		l.SetLevel("warn")
	case h.s == "I":
		l.SetLevel("info")
	case strings.HasPrefix(h.s, "D"):
		l.SetLevel("debug")
	}
	l.SetAttr("component", h.c)
	l.SetAttr("ctx", h.ctx)

	switch {
	case h.id == 22943 || h.msg == "Connection accepted":
		l.Event = "connection"
		dbPeer(l, dbBetween(h.attr, `"remote":"`, `"`))
	case h.id == 22944 || h.msg == "Connection ended":
		l.Event = "disconnection"
		dbPeer(l, dbBetween(h.attr, `"remote":"`, `"`))
	case h.id == 51800 || h.msg == "client metadata":
		l.Event = "client_metadata"
		a := mongoDecodeAttr(h.attr)
		dbPeer(l, a.Remote)
		dbSetWord(l, "app", a.Doc.Application.Name)
	case h.id == 51803 || h.msg == "Slow query":
		l.Event = "slow"
		a := mongoDecodeAttr(h.attr)
		dbPeer(l, a.Remote)
		// Decoded strings are the lens's own memory, not slices of the
		// line, and need no copy.
		dbSetWord(l, "ns", a.NS)
		dbSetWord(l, "app", a.AppName)
		dbSetWord(l, "plan", a.PlanSummary)
		dbSetWord(l, "code", a.ErrName)
		dbMillis(l, "duration_ms", a.DurationMillis.String(), 1)
		dbNumberAttr(l, "docs_examined", a.DocsExamined.String())
		dbNumberAttr(l, "keys_examined", a.KeysExamined.String())
		dbNumberAttr(l, "rows", a.NReturned.String())
		if len(a.Command) > 0 {
			dbSetWord(l, "query", string(a.Command))
			dbSetWord(l, "fp", Fingerprint(mongoShape(a.NS, a.Command)))
		}
	case h.id == 20250 || h.id == 5286306 || h.msg == "Authentication succeeded" ||
		h.msg == "Successfully authenticated":
		l.Event = "authorized"
		a := mongoDecodeAttr(h.attr)
		dbPeer(l, a.Remote)
		dbSetWord(l, "user", a.PrincipalName)
	case h.id == 20249 || h.id == 5286307 || h.msg == "Authentication failed" ||
		h.msg == "Failed to authenticate":
		l.Event = "auth_failed"
		a := mongoDecodeAttr(h.attr)
		dbPeer(l, a.Remote)
		dbSetWord(l, "user", a.PrincipalName)
		// "UserNotFound: Could not find user…" — the name before the colon
		// is the error's code name.
		var reason string
		if json.Unmarshal(a.Error, &reason) == nil {
			if name, _, found := strings.Cut(reason, ":"); found {
				dbSetWord(l, "code", name)
			}
		}
	case h.msg == "MongoDB starting":
		l.Event = "startup"
	case h.msg == "Waiting for connections":
		l.Event = "ready"
	case h.msg == "Received signal", h.msg == "Shutting down", h.msg == "Now exiting":
		l.Event = "shutdown"
	case h.c == "WTCHKPT", h.ctx == "Checkpointer", h.ctx == "WTCheckpointThread":
		l.Event = "checkpoint"
	case h.c == "REPL", h.c == "REPL_HB", h.c == "ELECTION", h.c == "ROLLBACK", h.c == "INITSYNC":
		l.Event = "replication"
	case h.c == "INDEX":
		l.Event = "index"
	}
}

// mongoDecodeAttr decodes attr for the lines that need more than the address. A
// field of an unexpected type is skipped and the rest still decoded.
func mongoDecodeAttr(attr string) mongoAttr {
	var a mongoAttr
	if attr != "" {
		_ = json.Unmarshal([]byte(attr), &a)
	}
	return a
}

// mongoParse reads the front of a line by hand: t, s, c, id, ctx, the svc a
// sharded cluster adds, msg, and the extent of attr.
func mongoParse(text string) (mongoHead, bool) {
	var h mongoHead
	rest := text[len(`{"t":{"$date":"`):]
	var ok bool
	if h.date, rest, ok = strings.Cut(rest, `"}`); !ok {
		return h, false
	}
	if h.s, rest, ok = mongoString(rest, "s"); !ok {
		return h, false
	}
	if h.c, rest, ok = mongoString(rest, "c"); !ok {
		return h, false
	}
	var id string
	if id, rest, ok = mongoValue(rest, "id"); !ok {
		return h, false
	}
	n, _ := dbNumber(id)
	h.id = int64(n)
	if h.ctx, rest, ok = mongoString(rest, "ctx"); !ok {
		return h, false
	}
	if _, after, found := mongoString(rest, "svc"); found {
		rest = after
	}
	if h.msg, rest, ok = mongoString(rest, "msg"); !ok {
		return h, false
	}
	if tail, found := mongoKey(rest, "attr"); found {
		end := mongoObjectEnd(tail)
		if end < 0 {
			return h, false
		}
		h.attr = tail[:end]
	}
	return h, true
}

// mongoKey steps over the comma and the alignment padding mongod puts between
// fields, and over `"key":`.
func mongoKey(s, key string) (string, bool) {
	s = strings.TrimLeft(strings.TrimPrefix(s, ","), " ")
	if len(s) < len(key)+3 || s[0] != '"' || s[1:1+len(key)] != key || s[1+len(key)] != '"' || s[2+len(key)] != ':' {
		return s, false
	}
	return s[len(key)+3:], true
}

// mongoString reads `"key":"value"`, the value as written.
func mongoString(s, key string) (value, rest string, ok bool) {
	s, ok = mongoKey(s, key)
	if !ok || s == "" || s[0] != '"' {
		return "", s, false
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return s[1:i], s[i+1:], true
		}
	}
	return "", s, false
}

// mongoValue reads `"key":value` for a value that is not a string.
func mongoValue(s, key string) (value, rest string, ok bool) {
	s, ok = mongoKey(s, key)
	if !ok {
		return "", s, false
	}
	end := strings.IndexAny(s, ",}")
	if end < 0 {
		return "", s, false
	}
	return strings.TrimSpace(s[:end]), s[end:], true
}

// mongoObjectEnd answers the length of the JSON object s starts with, or -1.
func mongoObjectEnd(s string) int {
	if s == "" || s[0] != '{' {
		return -1
	}
	depth, quoted := 0, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quoted {
			switch c {
			case '\\':
				i++
			case '"':
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// mongoDecode reads a line whose fields are not in mongod's order — written
// by something else, or rearranged on the way — as ordinary JSON.
func mongoDecode(text string) (mongoHead, bool) {
	var m struct {
		T struct {
			Date string `json:"$date"`
		} `json:"t"`
		S    string          `json:"s"`
		C    string          `json:"c"`
		ID   int64           `json:"id"`
		Ctx  string          `json:"ctx"`
		Msg  string          `json:"msg"`
		Attr json.RawMessage `json:"attr"`
	}
	if json.Unmarshal([]byte(text), &m) != nil || m.S == "" {
		return mongoHead{}, false
	}
	return mongoHead{date: m.T.Date, s: m.S, c: m.C, id: m.ID, ctx: m.Ctx, msg: m.Msg, attr: string(m.Attr)}, true
}

// mongoTime reads $date: "2026-09-27T10:00:00.000+00:00" by field, with
// time.Parse for any other spelling.
func mongoTime(s string) *time.Time {
	y, mo, d, ok := dbDate(s, '-')
	if ok && len(s) > 19 && s[10] == 'T' {
		if h, mi, sec, n, ok := dbClock(s[11:]); ok && n == 8 {
			nsec, fn := dbFraction(s[19:])
			switch zone := s[19+fn:]; zone {
			case "Z", "+00:00":
				return dbStamp(y, mo, d, h, mi, sec, nsec, time.UTC)
			default:
				if off, ok := dbOffset(zone); ok {
					at := dbStamp(y, mo, d, h, mi, sec, nsec, time.UTC).Add(-time.Duration(off) * time.Second)
					return &at
				}
			}
		}
	}
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	at = at.UTC()
	return &at
}

// mongoNoise is what a driver adds to every command and says nothing about
// its shape: sessions, cluster time, read preference, the API version.
var mongoNoise = map[string]bool{
	"$db": true, "lsid": true, "$clusterTime": true, "$readPreference": true, "txnNumber": true,
	"autocommit": true, "startTransaction": true, "comment": true, "maxTimeMS": true,
	"readConcern": true, "writeConcern": true, "cursor": true, "batchSize": true, "apiVersion": true,
	"apiStrict": true, "apiDeprecationErrors": true, "$audit": true, "$client": true,
	"mayBypassWriteBlocking": true, "$configTime": true, "$topologyTime": true, "shardVersion": true,
	"databaseVersion": true, "clientOperationKey": true, "singleBatch": true, "ordered": true,
}

// mongoShape is a slow command with its values taken out: the collection,
// the operation and the fields it filters, sorts and projects on. Fingerprint
// alone would replace every quoted key as a literal and make every find on
// the collection one shape; this keeps the keys, and turns every value into
// ?, a list into the shapes of its distinct members.
func mongoShape(ns string, command json.RawMessage) string {
	var doc map[string]any
	if json.Unmarshal(command, &doc) != nil {
		return ns
	}
	var b strings.Builder
	b.WriteString(ns)
	b.WriteByte(' ')
	// The operation is the command's first key, which a map forgets.
	b.WriteString(mongoFirstKey(command))
	keys := make([]string, 0, len(doc))
	for k := range doc {
		if !mongoNoise[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteByte(':')
		mongoShapeOf(&b, doc[k])
	}
	return b.String()
}

func mongoShapeOf(b *strings.Builder, v any) {
	switch v := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(k)
			b.WriteByte(':')
			mongoShapeOf(b, v[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		seen := map[string]bool{}
		for _, item := range v {
			var inner strings.Builder
			mongoShapeOf(&inner, item)
			if s := inner.String(); !seen[s] {
				if len(seen) > 0 {
					b.WriteByte(',')
				}
				seen[s] = true
				b.WriteString(s)
			}
		}
		b.WriteByte(']')
	default:
		b.WriteByte('?')
	}
}

func mongoFirstKey(command json.RawMessage) string {
	dec := json.NewDecoder(strings.NewReader(string(command)))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return ""
	}
	if tok, err := dec.Token(); err == nil {
		if key, ok := tok.(string); ok {
			return key
		}
	}
	return ""
}
