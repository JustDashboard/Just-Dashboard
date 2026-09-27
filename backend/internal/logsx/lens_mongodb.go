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
// decodes the line itself. The id is stable across versions for a given log
// statement, which is what it keys on first; the message text is the
// fallback for statements whose id the docs do not pin down.

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

// mongoLine is the part of a log line the lens reads. Fields of attr that
// hold something else in another statement — "error" is a string in an
// authentication failure and an object elsewhere — are kept raw.
type mongoLine struct {
	T struct {
		Date string `json:"$date"`
	} `json:"t"`
	S    string `json:"s"`
	C    string `json:"c"`
	ID   int64  `json:"id"`
	Ctx  string `json:"ctx"`
	Msg  string `json:"msg"`
	Attr struct {
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
	} `json:"attr"`
}

func readMongo(l *Line) {
	if !strings.HasPrefix(l.Text, `{"t":{"$date":`) {
		return
	}
	var m mongoLine
	// An attr of an unexpected type is skipped and the rest still decoded;
	// only a line that is not JSON at all is refused.
	if err := json.Unmarshal([]byte(l.Text), &m); err != nil {
		if _, syntax := err.(*json.SyntaxError); syntax || m.S == "" {
			return
		}
	}
	if at, err := time.Parse(time.RFC3339Nano, m.T.Date); err == nil {
		at = at.UTC()
		l.Timestamp = &at
	}
	switch {
	case m.S == "F":
		l.SetLevel("critical")
	case m.S == "E":
		l.SetLevel("error")
	case m.S == "W":
		l.SetLevel("warn")
	case m.S == "I":
		l.SetLevel("info")
	case strings.HasPrefix(m.S, "D"):
		l.SetLevel("debug")
	}
	// Decoded strings are the lens's own memory, not slices of the line.
	dbSetWord(l, "component", m.C)
	dbSetWord(l, "ctx", m.Ctx)

	a := &m.Attr
	switch {
	case m.ID == 22943 || m.Msg == "Connection accepted":
		l.Event = "connection"
		dbPeer(l, a.Remote)
	case m.ID == 22944 || m.Msg == "Connection ended":
		l.Event = "disconnection"
		dbPeer(l, a.Remote)
	case m.ID == 51800 || m.Msg == "client metadata":
		l.Event = "client_metadata"
		dbPeer(l, a.Remote)
		l.SetAttr("app", a.Doc.Application.Name)
	case m.ID == 51803 || m.Msg == "Slow query":
		l.Event = "slow"
		dbPeer(l, a.Remote)
		l.SetAttr("ns", a.NS)
		l.SetAttr("app", a.AppName)
		l.SetAttr("plan", a.PlanSummary)
		l.SetAttr("code", a.ErrName)
		dbMillis(l, "duration_ms", a.DurationMillis.String(), 1)
		dbNumberAttr(l, "docs_examined", a.DocsExamined.String())
		dbNumberAttr(l, "keys_examined", a.KeysExamined.String())
		dbNumberAttr(l, "rows", a.NReturned.String())
		if len(a.Command) > 0 {
			l.SetAttr("query", string(a.Command))
			l.SetAttr("fp", Fingerprint(mongoShape(a.NS, a.Command)))
		}
	case m.ID == 20250 || m.ID == 5286306 || m.Msg == "Authentication succeeded" ||
		m.Msg == "Successfully authenticated":
		l.Event = "authorized"
		dbPeer(l, a.Remote)
		l.SetAttr("user", a.PrincipalName)
	case m.ID == 20249 || m.ID == 5286307 || m.Msg == "Authentication failed" ||
		m.Msg == "Failed to authenticate":
		l.Event = "auth_failed"
		dbPeer(l, a.Remote)
		l.SetAttr("user", a.PrincipalName)
		// "UserNotFound: Could not find user…" — the name before the colon
		// is the error's code name.
		var reason string
		if json.Unmarshal(a.Error, &reason) == nil {
			if name, _, found := strings.Cut(reason, ":"); found {
				l.SetAttr("code", name)
			}
		}
	case m.Msg == "MongoDB starting":
		l.Event = "startup"
	case m.Msg == "Waiting for connections":
		l.Event = "ready"
	case m.Msg == "Received signal", m.Msg == "Shutting down", m.Msg == "Now exiting":
		l.Event = "shutdown"
	case m.C == "WTCHKPT", m.Ctx == "Checkpointer", m.Ctx == "WTCheckpointThread":
		l.Event = "checkpoint"
	case m.C == "REPL", m.C == "REPL_HB", m.C == "ELECTION", m.C == "ROLLBACK", m.C == "INITSYNC":
		l.Event = "replication"
	case m.C == "INDEX":
		l.Event = "index"
	}
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
