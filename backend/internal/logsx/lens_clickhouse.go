package logsx

import (
	"strings"
	"time"
)

// ClickHouse's server log: "2024.01.01 00:00:00.000000 [ tid ] {query_id}
// <Level> Logger: message". The query id is what ties a line to its row in
// system.query_log, and the logger names the subsystem — or, for a table's
// background work, "db.table (uuid) (MergerMutator)". An exception's stack
// trace follows it on lines of its own with no prefix.
//
// The default logger level is trace, so most of a ClickHouse log is Debug and
// Trace; the lens does not hide them, but it names the lines worth finding
// among them: the query's start and its "Read N rows … in S sec." finish,
// exceptions by code, and the three a ClickHouse operator meets first —
// memory limits, too many parts, and failed logins.

func init() {
	register(&Lens{
		ID: "clickhouse",
		Events: []string{
			"startup", "ready", "shutdown", "query", "exception", "merge", "memory_limit",
			"too_many_parts", "auth_failed",
		},
		Attrs: []string{
			"thread", "query_id", "component", "code", "duration_ms", "rows",
			"user", "client", "port", "query", "fp", "table",
		},
		New: func() Reader { return &clickhouseReader{} },
	})
}

type clickhouseReader struct {
	open  bool
	level string
}

type clickhouseHead struct {
	at                     *time.Time
	thread, queryID, level string
	logger, msg            string
}

func (r *clickhouseReader) Read(l *Line) {
	h, ok := clickhouseParse(l.Text)
	if !ok {
		if r.open {
			l.Cont = true
			if r.level != "" {
				l.SetLevel(r.level)
			}
		}
		return
	}
	l.Timestamp = h.at
	switch h.level {
	case "Fatal", "Critical":
		l.SetLevel("critical")
	case "Error":
		l.SetLevel("error")
	case "Warning":
		l.SetLevel("warn")
	case "Notice", "Information":
		l.SetLevel("info")
	default:
		l.SetLevel("debug")
	}
	l.SetAttr("thread", h.thread)
	l.SetAttr("query_id", h.queryID)
	// A table's logger is "default.events (uuid) (MergerMutator)": the
	// component is the kind of work, and the table is kept apart so a
	// facet of components is not one row per table.
	component := h.logger
	if first := strings.Index(component, " ("); first > 0 && strings.HasSuffix(component, ")") &&
		strings.Contains(component[:first], ".") {
		l.SetAttr("table", component[:first])
		component = component[strings.LastIndex(component, " (")+2 : len(component)-1]
		if clickhouseUUID(component) {
			component = ""
		}
	}
	l.SetAttr("component", component)

	msg := h.msg
	switch {
	case component == "executeQuery" && strings.HasPrefix(msg, "(from "):
		clickhouseQuery(l, msg)
	case strings.Contains(msg, "Exception: ") || strings.HasPrefix(msg, "Code: "):
		clickhouseException(l, msg)
	case component == "executeQuery" && strings.HasPrefix(msg, "Read ") && strings.Contains(msg, " sec."):
		// "Read 1000000 rows, 7.63 MiB in 0.066 sec., …", the query's end.
		l.Event = "query"
		dbNumberAttr(l, "rows", dbBetween(msg, "Read ", " rows, "))
		if i := strings.Index(msg, " in "); i >= 0 {
			if sec, _, found := strings.Cut(msg[i+len(" in "):], " sec."); found {
				dbMillis(l, "duration_ms", sec, 1000)
			}
		}
	case strings.HasPrefix(msg, "Starting ClickHouse"):
		l.Event = "startup"
	case strings.HasPrefix(msg, "Ready for connections"):
		l.Event = "ready"
	case strings.HasPrefix(msg, "Received termination signal"), strings.HasPrefix(msg, "Shutting down storages"):
		l.Event = "shutdown"
	case strings.HasPrefix(msg, "Delaying inserting block"):
		l.Event = "too_many_parts"
	case clickhouseMerge(component):
		l.Event = "merge"
	}
	r.open, r.level = true, l.Level
}

// clickhouseQuery reads executeQuery's start line: "(from 172.18.0.5:54321,
// user: default) SELECT … (stage: Complete)".
func clickhouseQuery(l *Line, msg string) {
	inside, query, found := strings.Cut(msg[len("(from "):], ") ")
	if !found {
		return
	}
	l.Event = "query"
	address, rest, _ := strings.Cut(inside, ", ")
	dbPeer(l, address)
	l.SetAttr("user", dbWord(rest, "user: "))
	// Newer servers put the query's comment setting before it.
	if strings.HasPrefix(query, "(comment: ") {
		if _, after, found := strings.Cut(query, ") "); found {
			query = after
		}
	}
	if i := strings.LastIndex(query, " (stage: "); i >= 0 {
		query = query[:i]
	}
	query = strings.TrimSpace(query)
	if query != "" {
		l.SetAttr("query", query)
		l.SetAttr("fp", Fingerprint(query))
	}
}

// clickhouseException reads "Code: 60. DB::Exception: … (UNKNOWN_TABLE)
// (version …) (from 172.18.0.5:54322) (in query: …)". The code is the
// error's name where the server wrote one — UNKNOWN_TABLE says what 60 does
// not — and its number otherwise, as versions before 20.x write it.
func clickhouseException(l *Line, msg string) {
	number := dbBetween(msg, "Code: ", ".")
	if number == "" {
		number = dbBetween(msg, "Code: ", ",")
	}
	name := ""
	head := msg
	if i := strings.Index(head, " (version "); i >= 0 {
		head = head[:i]
	}
	if strings.HasSuffix(head, ")") {
		if i := strings.LastIndexByte(head, '('); i >= 0 && clickhouseErrorName(head[i+1:len(head)-1]) {
			name = head[i+1 : len(head)-1]
		}
	}
	code := name
	if code == "" {
		code = number
	}
	l.SetAttr("code", code)
	if from := dbBetween(msg, "(from ", ")"); from != "" {
		dbPeer(l, from)
	}
	switch {
	case name == "MEMORY_LIMIT_EXCEEDED" || number == "241" || strings.Contains(msg, "Memory limit"):
		l.Event = "memory_limit"
	case name == "TOO_MANY_PARTS" || number == "252" || strings.Contains(msg, "Too many parts"):
		l.Event = "too_many_parts"
	case name == "AUTHENTICATION_FAILED" || number == "516" || strings.Contains(msg, "Authentication failed"):
		l.Event = "auth_failed"
		// "DB::Exception: default: Authentication failed" — the user the
		// login claimed is the word before the reason.
		l.SetAttr("user", dbBetween(msg, "Exception: ", ": Authentication failed"))
	default:
		l.Event = "exception"
	}
}

// clickhouseMerge is the background merge's loggers. MergeTree's read path
// has loggers named MergeTree… too, and a SELECT is not a merge.
func clickhouseMerge(component string) bool {
	return strings.Contains(component, "MergerMutator") || strings.HasPrefix(component, "MergeTask") ||
		strings.HasPrefix(component, "MergeFromLogEntryTask") ||
		strings.HasPrefix(component, "MergeTreeBackgroundExecutor")
}

// clickhouseUUID recognises a table's uuid standing where a logger's kind
// would be.
func clickhouseUUID(s string) bool {
	return len(s) == 36 && strings.Count(s, "-") == 4
}

func clickhouseErrorName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'A' || c > 'Z') && c != '_' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func clickhouseParse(text string) (clickhouseHead, bool) {
	var h clickhouseHead
	if len(text) < 40 || text[4] != '.' {
		return h, false
	}
	y, mo, d, ok := dbDate(text, '.')
	if !ok || text[10] != ' ' {
		return h, false
	}
	hour, mi, sec, n, ok := dbClock(text[11:])
	if !ok || n != 8 {
		return h, false
	}
	nsec, fn := dbFraction(text[19:])
	rest := text[19+fn:]
	// " [ 812 ] "
	rest, ok = strings.CutPrefix(rest, " [")
	if !ok {
		return h, false
	}
	rest = strings.TrimLeft(rest, " ")
	tn := dbDigits(rest)
	if tn == 0 {
		return h, false
	}
	h.thread = rest[:tn]
	rest = strings.TrimLeft(rest[tn:], " ")
	if rest, ok = strings.CutPrefix(rest, "] "); !ok {
		return h, false
	}
	// "{query_id} ", absent before 20.x.
	if strings.HasPrefix(rest, "{") {
		end := strings.IndexByte(rest, '}')
		if end < 0 || end+1 >= len(rest) || rest[end+1] != ' ' {
			return h, false
		}
		h.queryID, rest = rest[1:end], rest[end+2:]
	}
	if !strings.HasPrefix(rest, "<") {
		return h, false
	}
	end := strings.IndexByte(rest, '>')
	if end < 0 {
		return h, false
	}
	h.level = rest[1:end]
	switch h.level {
	case "Fatal", "Critical", "Error", "Warning", "Notice", "Information", "Debug", "Trace", "Test":
	default:
		return h, false
	}
	h.logger, h.msg, _ = strings.Cut(strings.TrimPrefix(rest[end+1:], " "), ": ")
	h.at = dbStamp(y, mo, d, hour, mi, sec, nsec, time.Local)
	return h, true
}
