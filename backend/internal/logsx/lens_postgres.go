package logsx

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Postgres writes a message in parts: the message at its severity, then a
// DETAIL, a HINT, the STATEMENT that raised it, each on a line of its own
// behind the same prefix, and a statement that spans lines carries on with a
// tab at the start of each. Read one line at a time, an error is three
// unrelated lines and the STATEMENT line is the one a level filter drops. So
// the lens reads the prefix, names the message, and marks the parts that
// follow as the record's continuation.
//
// The prefix is whatever log_line_prefix says, but two of them are nearly
// every Postgres an operator runs: the Docker image's '%m [%p] ' and Debian's
// '%m [%p] %q%u@%d ', where %q means background processes stop after the pid.
// Rather than compile one exact prefix, the reader takes the stamp and then
// the words up to the severity, and recognises a pid, a user@db, a client
// address or a SQLSTATE among them — which also covers the prefixes
// pgBadger and pganalyze recommend.

func init() {
	register(&Lens{
		ID: "postgres",
		Events: []string{
			"startup", "ready", "shutdown", "crash", "oom", "disk_full", "checkpoint",
			"autovacuum", "autoanalyze", "connection", "authorized", "disconnection",
			"auth_failed", "too_many_clients", "deadlock", "lock_wait", "slow", "duration",
			"statement", "temp_file", "cancel", "terminated", "replication",
			"archive_failed", "config", "error", "fatal",
		},
		Attrs: []string{
			"pid", "user", "db", "client", "port", "app", "code", "severity",
			"duration_ms", "query", "fp", "table", "bytes",
			"buffers", "write_s", "sync_s", "total_s",
		},
		New: func() Reader { return &postgresReader{} },
	})
}

type postgresReader struct {
	// The record a DETAIL or a tab-indented line continues: open while the
	// lines above were Postgres's, with the pid that wrote the head and the
	// level it was given. A part written by another pid does not continue
	// it, whatever the order the lines arrived in.
	open  bool
	pid   string
	level string
	// peers is the address each backend connected from, kept when
	// log_connections is on. The line that matters — a failed password, a
	// refused slot — does not repeat the address, and "auth failures by
	// client" is the question the operator is asking.
	peers map[string]string
}

// pgPeersCap bounds the address memory. With log_disconnections off nothing
// ever forgets a backend, and a long search must not grow without end.
const pgPeersCap = 4096

// pgHead is one line's prefix and message.
type pgHead struct {
	at                                     *time.Time
	pid, user, db, app, client, port, code string
	severity, msg                          string
	// statement is jsonlog's: the statement travels in the same record
	// there, so an error can carry its query.
	statement string
}

func (r *postgresReader) Read(l *Line) {
	text := l.Text
	if text == "" {
		r.open = false
		return
	}
	// Postgres puts a tab after every newline it writes inside a message, so
	// a line starting with one continues whatever came before it.
	if text[0] == '\t' {
		l.Cont = true
		if r.open && r.level != "" {
			l.SetLevel(r.level)
		}
		return
	}
	h, ok := pgParse(text)
	if !ok {
		r.open = false
		return
	}
	if h.at != nil {
		l.Timestamp = h.at
	}
	if pgPart(h.severity) {
		if r.open && h.pid == r.pid {
			l.Cont = true
			if r.level != "" {
				l.SetLevel(r.level)
			}
			return
		}
		r.open = false
		return
	}

	l.SetLevel(pgLevel(h.severity))
	l.SetAttr("pid", h.pid)
	dbSetWord(l, "severity", pgSeverity(h.severity))
	if h.user != "[unknown]" {
		l.SetAttr("user", h.user)
	}
	if h.db != "[unknown]" {
		l.SetAttr("db", h.db)
	}
	if h.app != "[unknown]" {
		l.SetAttr("app", h.app)
	}
	l.SetAttr("client", h.client)
	l.SetAttr("port", h.port)
	if h.code != "00000" {
		l.SetAttr("code", h.code)
	}

	switch h.severity {
	case "ERROR", "FATAL", "PANIC":
		r.failure(l, &h)
	default:
		r.log(l, &h)
	}

	if r.peers != nil && h.pid != "" {
		if _, has := l.Attrs["client"]; !has {
			l.SetAttr("client", r.peers[h.pid])
		}
		// A FATAL ends the backend that wrote it, as a disconnection does.
		if h.severity == "FATAL" || l.Event == "disconnection" {
			delete(r.peers, h.pid)
		}
	}
	r.open, r.pid, r.level = true, h.pid, l.Level
}

// log names the lines Postgres writes below ERROR: the lifecycle, the
// statement and duration logging, connections, maintenance.
func (r *postgresReader) log(l *Line, h *pgHead) {
	msg := h.msg
	switch {
	case strings.HasPrefix(msg, "duration: "):
		pgDuration(l, msg[len("duration: "):])
	case strings.HasPrefix(msg, "statement: "):
		l.Event = "statement"
		pgQuery(l, msg[len("statement: "):])
	case strings.HasPrefix(msg, "execute "):
		// log_statement's spelling for the extended protocol:
		// "execute <unnamed>: SELECT …".
		if _, query, found := strings.Cut(msg, ": "); found {
			l.Event = "statement"
			pgQuery(l, query)
		}
	case strings.HasPrefix(msg, "checkpoint "), strings.HasPrefix(msg, "restartpoint "),
		strings.HasPrefix(msg, "checkpoints are occurring too frequently"):
		l.Event = "checkpoint"
		if strings.Contains(msg, " complete: ") {
			dbNumberAttr(l, "buffers", dbBetween(msg, "wrote ", " buffers"))
			dbNumberAttr(l, "write_s", dbBetween(msg, "write=", " s"))
			dbNumberAttr(l, "sync_s", dbBetween(msg, ", sync=", " s"))
			dbNumberAttr(l, "total_s", dbBetween(msg, ", total=", " s"))
		}
	case strings.HasPrefix(msg, "connection received: "):
		l.Event = "connection"
		host := dbWord(msg, "host=")
		l.SetAttr("client", dbIP(host))
		l.SetAttr("port", dbWord(msg, " port="))
		if client := l.Attrs["client"]; client != "" && h.pid != "" {
			if r.peers == nil || len(r.peers) >= pgPeersCap {
				r.peers = make(map[string]string)
			}
			r.peers[strings.Clone(h.pid)] = client
		}
	case strings.HasPrefix(msg, "connection authorized: "),
		strings.HasPrefix(msg, "replication connection authorized: "):
		l.Event = "authorized"
		l.SetAttr("user", dbWord(msg, "user="))
		l.SetAttr("db", dbWord(msg, " database="))
		if i := strings.Index(msg, " application_name="); i >= 0 {
			app := msg[i+len(" application_name="):]
			// The application name may hold spaces; what follows it is
			// the transport, which always starts the same way.
			for _, tail := range []string{" SSL enabled (", " GSS ("} {
				if j := strings.Index(app, tail); j >= 0 {
					app = app[:j]
				}
			}
			l.SetAttr("app", app)
		}
	case strings.HasPrefix(msg, "disconnection: "):
		l.Event = "disconnection"
		pgSessionTime(l, dbWord(msg, "session time: "))
		l.SetAttr("user", dbWord(msg, " user="))
		l.SetAttr("db", dbWord(msg, " database="))
		l.SetAttr("client", dbIP(dbWord(msg, " host=")))
		l.SetAttr("port", dbWord(msg, " port="))
	case strings.HasPrefix(msg, "automatic "):
		switch {
		case strings.Contains(msg, " vacuum "):
			l.Event = "autovacuum"
		case strings.Contains(msg, " analyze "):
			l.Event = "autoanalyze"
		default:
			return
		}
		l.SetAttr("table", dbBetween(msg, `of table "`, `"`))
	case strings.HasPrefix(msg, "temporary file: "):
		l.Event = "temp_file"
		dbNumberAttr(l, "bytes", dbWord(msg, ", size "))
	case strings.HasPrefix(msg, "process ") && strings.Contains(msg, " still waiting for "):
		l.Event = "lock_wait"
		if i := strings.LastIndex(msg, " after "); i >= 0 {
			if ms, _, found := strings.Cut(msg[i+len(" after "):], " ms"); found {
				dbMillis(l, "duration_ms", ms, 1)
			}
		}
	case strings.HasPrefix(msg, "starting PostgreSQL"):
		l.Event = "startup"
	case strings.HasPrefix(msg, "database system is ready to accept"):
		l.Event = "ready"
	case strings.HasPrefix(msg, "received ") && strings.HasSuffix(msg, " shutdown request"),
		msg == "database system is shut down":
		l.Event = "shutdown"
	case strings.Contains(msg, ") was terminated by "):
		// "server process (PID 660) was terminated by signal 6: Aborted".
		// Nine is the kernel's OOM killer, which is a different fix from a
		// segfault in an extension.
		l.Event = "crash"
		if dbBetween(msg, "terminated by signal ", ":") == "9" {
			l.Event = "oom"
		}
		l.SetLevel("critical")
	case strings.HasPrefix(msg, "received SIGHUP"), strings.HasPrefix(msg, `parameter "`),
		strings.HasPrefix(msg, `configuration file "`):
		l.Event = "config"
	case strings.HasPrefix(msg, "archive command "), strings.HasPrefix(msg, "archiving write-ahead log file"):
		l.Event = "archive_failed"
	case pgReplication(msg):
		l.Event = "replication"
	case strings.Contains(msg, "No space left on device"):
		l.Event = "disk_full"
		l.SetLevel("critical")
	}
}

// failure names an ERROR, a FATAL or a PANIC, and gives it a SQLSTATE when
// the prefix did not: the stderr log carries no code unless %e is in the
// prefix, and "errors by code" is how a page groups them.
func (r *postgresReader) failure(l *Line, h *pgHead) {
	msg := h.msg
	switch {
	case strings.Contains(msg, "No space left on device"):
		l.Event = "disk_full"
		l.SetLevel("critical")
	case strings.HasPrefix(msg, "out of memory"):
		l.Event = "oom"
		l.SetLevel("critical")
	case strings.HasPrefix(msg, "deadlock detected"):
		l.Event = "deadlock"
	case strings.HasPrefix(msg, "canceling "):
		l.Event = "cancel"
	case pgReplication(msg):
		l.Event = "replication"
	case h.severity == "FATAL" && pgAuthFailure(l, msg):
		l.Event = "auth_failed"
	case strings.HasPrefix(msg, "sorry, too many clients"),
		strings.HasPrefix(msg, "remaining connection slots are reserved"),
		strings.HasPrefix(msg, "too many connections for "):
		l.Event = "too_many_clients"
	case strings.HasPrefix(msg, "terminating "):
		l.Event = "terminated"
	case h.severity == "PANIC":
		l.Event = "crash"
	case h.severity == "FATAL":
		l.Event = "fatal"
	default:
		l.Event = "error"
	}
	if h.code == "" {
		l.SetAttr("code", pgCode(msg))
	}
	if h.statement != "" {
		pgQuery(l, h.statement)
	}
}

// pgAuthFailure reads who was refused. Every one of these is a FATAL that
// ends the connection before a session exists: a wrong password, a
// pg_hba.conf that does not admit the host, a role or a database that does
// not exist (which is what a healthcheck running psql without -U produces).
func pgAuthFailure(l *Line, msg string) bool {
	switch {
	case strings.Contains(msg, `authentication failed for user "`):
		l.SetAttr("user", dbBetween(msg, `for user "`, `"`))
	case strings.HasPrefix(msg, "pg_hba.conf rejects "), strings.HasPrefix(msg, "no pg_hba.conf entry for "):
		l.SetAttr("client", dbIP(dbBetween(msg, `host "`, `"`)))
		l.SetAttr("user", dbBetween(msg, `, user "`, `"`))
		l.SetAttr("db", dbBetween(msg, `, database "`, `"`))
	case strings.HasPrefix(msg, `role "`) &&
		(strings.HasSuffix(msg, `" does not exist`) || strings.HasSuffix(msg, " is not permitted to log in")):
		l.SetAttr("user", dbBetween(msg, `role "`, `"`))
	case strings.HasPrefix(msg, `database "`) && strings.HasSuffix(msg, `" does not exist`):
		l.SetAttr("db", dbBetween(msg, `database "`, `"`))
	default:
		return false
	}
	return true
}

// pgDuration reads log_min_duration_statement's and log_duration's lines.
// "duration: 12.3 ms" alone is log_duration, logged for every statement; with
// the statement after it, the statement crossed log_min_duration_statement,
// which is what a slow query is.
func pgDuration(l *Line, rest string) {
	ms, after, found := strings.Cut(rest, " ms")
	if !found {
		return
	}
	dbMillis(l, "duration_ms", ms, 1)
	switch {
	case after == "":
		l.Event = "duration"
	case strings.HasPrefix(after, "  statement: "):
		l.Event = "slow"
		pgQuery(l, after[len("  statement: "):])
	case strings.HasPrefix(after, "  parse "), strings.HasPrefix(after, "  bind "),
		strings.HasPrefix(after, "  execute "):
		l.Event = "slow"
		if _, query, ok := strings.Cut(after, ": "); ok {
			pgQuery(l, query)
		}
	default:
		// auto_explain's "plan:", whose query is in the tab-indented body.
		l.Event = "slow"
	}
}

// pgQuery records the statement on the head line and its shape. A statement
// that spans lines is only its first line here: the rest arrive as
// continuation lines, after this one has been emitted.
func pgQuery(l *Line, query string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return
	}
	l.SetAttr("query", query)
	l.SetAttr("fp", Fingerprint(query))
}

// pgSessionTime reads "0:01:02.345" as milliseconds.
func pgSessionTime(l *Line, s string) {
	hours, rest, ok := strings.Cut(s, ":")
	if !ok {
		return
	}
	minutes, seconds, ok := strings.Cut(rest, ":")
	if !ok {
		return
	}
	h, okH := dbNumber(hours)
	m, okM := dbNumber(minutes)
	sec, err := strconv.ParseFloat(seconds, 64)
	if !okH || !okM || err != nil {
		return
	}
	dbMillis(l, "duration_ms", strconv.FormatFloat(float64(h*3600+m*60)+sec, 'f', -1, 64), 1000)
}

// pgReplication is the standby and WAL-shipping vocabulary. Checked by
// prefix only: "logical replication launcher" appears in every shutdown and
// is not news.
func pgReplication(msg string) bool {
	for _, p := range pgReplicationPrefixes {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}

var pgReplicationPrefixes = []string{
	"started streaming WAL", "restarted WAL streaming", "entering standby mode",
	"consistent recovery state reached", "restored log file", "replication terminated by primary",
	"could not receive data from WAL stream", "terminating walreceiver", "could not connect to the primary",
	"requested WAL segment", "fetching timeline history file", "selected new timeline ID",
	"received promote request", "replication slot",
}

// pgCodes maps the messages an application meets most onto their SQLSTATE,
// in the order they are tried. A message is recognised by how it starts and,
// where the start is shared, by a phrase inside it.
var pgCodes = []struct{ prefix, contains, code string }{
	{"duplicate key value violates unique constraint", "", "23505"},
	{"insert or update on table", "violates foreign key", "23503"},
	{"update or delete on table", "violates foreign key", "23503"},
	{"null value in column", "", "23502"},
	{"new row for relation", "violates check constraint", "23514"},
	{"conflicting key value violates exclusion constraint", "", "23P01"},
	{"syntax error at", "", "42601"},
	{"unterminated quoted", "", "42601"},
	{`relation "`, " does not exist", "42P01"},
	{"missing FROM-clause entry", "", "42P01"},
	{`relation "`, " already exists", "42P07"},
	{"column ", " does not exist", "42703"},
	{"column reference ", " is ambiguous", "42702"},
	{"column ", "must appear in the GROUP BY clause", "42803"},
	{"function ", " does not exist", "42883"},
	{"operator does not exist", "", "42883"},
	{`type "`, " does not exist", "42704"},
	{`schema "`, " does not exist", "3F000"},
	{"permission denied", "", "42501"},
	{"must be owner of", "", "42501"},
	{"cannot cast type", "", "42846"},
	{"there is no unique or exclusion constraint matching", "", "42P10"},
	{"current transaction is aborted", "", "25P02"},
	{"invalid input syntax for", "", "22P02"},
	{"invalid input value for enum", "", "22P02"},
	{"value too long for type", "", "22001"},
	{"integer out of range", "", "22003"},
	{"bigint out of range", "", "22003"},
	{"smallint out of range", "", "22003"},
	{"numeric field overflow", "", "22003"},
	{"date/time field value out of range", "", "22008"},
	{"division by zero", "", "22012"},
	{"invalid byte sequence for encoding", "", "22021"},
	{"could not serialize access", "", "40001"},
	{"deadlock detected", "", "40P01"},
	{"canceling statement due to statement timeout", "", "57014"},
	{"canceling statement due to user request", "", "57014"},
	{"canceling autovacuum task", "", "57014"},
	{"canceling statement due to lock timeout", "", "55P03"},
	{"could not obtain lock", "", "55P03"},
	{"canceling statement due to conflict with recovery", "", "40001"},
	{"password authentication failed", "", "28P01"},
	{"", "authentication failed for user", "28000"},
	{"pg_hba.conf rejects", "", "28000"},
	{"no pg_hba.conf entry", "", "28000"},
	{`role "`, " does not exist", "28000"},
	{`role "`, "is not permitted to log in", "28000"},
	{`database "`, " does not exist", "3D000"},
	{"sorry, too many clients", "", "53300"},
	{"remaining connection slots are reserved", "", "53300"},
	{"too many connections for", "", "53300"},
	{"out of memory", "", "53200"},
	{"", "No space left on device", "53100"},
	{"terminating connection due to administrator command", "", "57P01"},
	{"terminating connection due to idle-in-transaction timeout", "", "25P03"},
	{"terminating connection due to idle-session timeout", "", "57P05"},
	{"the database system is", "", "57P03"},
	{"connection to client lost", "", "08006"},
	{"unsupported frontend protocol", "", "08P01"},
	{`prepared statement "`, " does not exist", "26000"},
	{"cannot drop", "other objects depend on it", "2BP01"},
	{"database is not accepting commands to avoid wraparound", "", "54000"},
}

func pgCode(msg string) string {
	for _, c := range pgCodes {
		if strings.HasPrefix(msg, c.prefix) && (c.contains == "" || strings.Contains(msg, c.contains)) {
			return c.code
		}
	}
	return ""
}

// pgLevel maps a severity onto the dashboard's scale. FATAL is an error, not
// a critical: it ends one session — a wrong password is a FATAL — while PANIC
// ends them all.
func pgLevel(severity string) string {
	switch severity {
	case "LOG", "INFO", "NOTICE":
		return "info"
	case "WARNING":
		return "warn"
	case "ERROR", "FATAL":
		return "error"
	case "PANIC":
		return "critical"
	}
	if strings.HasPrefix(severity, "DEBUG") {
		return "debug"
	}
	return ""
}

// pgSeverity answers the severity as the lens's own constant, which every
// line of a log can share.
func pgSeverity(severity string) string {
	switch severity {
	case "LOG":
		return "LOG"
	case "INFO":
		return "INFO"
	case "NOTICE":
		return "NOTICE"
	case "WARNING":
		return "WARNING"
	case "ERROR":
		return "ERROR"
	case "FATAL":
		return "FATAL"
	case "PANIC":
		return "PANIC"
	}
	return strings.Clone(severity)
}

// pgPart says whether a severity is one of the parts written after a
// message, which continue it rather than start a record.
func pgPart(severity string) bool {
	switch severity {
	case "DETAIL", "HINT", "CONTEXT", "STATEMENT", "QUERY", "LOCATION", "BACKTRACE":
		return true
	}
	return false
}

// pgParse reads one line: the stderr format, the same behind the entrypoint's
// "waiting for server to start...." (which does not end its own line before
// the server starts writing), or jsonlog.
func pgParse(text string) (pgHead, bool) {
	if h, ok := pgParseText(text); ok {
		return h, true
	}
	if strings.HasPrefix(text, "waiting for server to ") {
		if i := strings.Index(text, "...."); i >= 0 {
			rest := strings.TrimLeft(text[i:], ".")
			return pgParseText(rest)
		}
	}
	if strings.HasPrefix(text, `{"timestamp":"`) {
		return pgParseJSON(text)
	}
	return pgHead{}, false
}

func pgParseText(text string) (pgHead, bool) {
	var h pgHead
	if len(text) < 26 || text[0] < '0' || text[0] > '9' {
		return h, false
	}
	at, n := pgStamp(text)
	if at == nil || n >= len(text) || text[n] != ' ' {
		return h, false
	}
	rest := text[n+1:]
	// The severity is the word before the first colon followed by two
	// spaces; everything between the stamp and it is the rest of the prefix.
	sevEnd := strings.Index(rest, ":  ")
	if sevEnd < 0 {
		return h, false
	}
	sevStart := strings.LastIndexByte(rest[:sevEnd], ' ') + 1
	h.severity = rest[sevStart:sevEnd]
	if pgLevel(h.severity) == "" && !pgPart(h.severity) {
		return h, false
	}
	h.at = at
	h.msg = rest[sevEnd+3:]
	pgPrefix(&h, rest[:sevStart])
	return h, true
}

// pgPrefix reads the words log_line_prefix put between the stamp and the
// severity.
func pgPrefix(h *pgHead, prefix string) {
	for prefix != "" {
		var word string
		word, prefix, _ = strings.Cut(prefix, " ")
		switch {
		case word == "":
		case word[0] == '[' && len(word) > 1 && word[1] >= '0' && word[1] <= '9':
			// [%p], or [%p-%l] with the session line number.
			h.pid = word[1 : 1+dbDigits(word[1:])]
		case strings.Contains(word, "="):
			// pgBadger's user=%u,db=%d,app=%a,client=%h
			for _, pair := range strings.Split(strings.TrimRight(word, ","), ",") {
				key, value, _ := strings.Cut(pair, "=")
				switch key {
				case "user":
					h.user = value
				case "db":
					h.db = value
				case "app":
					h.app = value
				case "client", "host":
					h.client = dbIP(value)
				}
			}
		case strings.Contains(word, "@"):
			// %u@%d, or %u@%d/%a.
			h.user, h.db, _ = strings.Cut(word, "@")
			h.db, h.app, _ = strings.Cut(h.db, "/")
		case pgSQLState(word):
			h.code = word
		default:
			// %h, or %r's host(port).
			host, port := word, ""
			if i := strings.IndexByte(word, '('); i > 0 && strings.HasSuffix(word, ")") {
				host, port = word[:i], word[i+1:len(word)-1]
			}
			if ip := dbIP(host); ip != "" {
				h.client, h.port = ip, port
			}
		}
	}
}

func pgSQLState(word string) bool {
	if len(word) != 5 {
		return false
	}
	digit := false
	for i := 0; i < 5; i++ {
		c := word[i]
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'A' && c <= 'Z':
		default:
			return false
		}
	}
	return digit
}

// pgStamp reads %m or %t — "2026-09-27 10:00:00.123 UTC" — and answers the
// time and how many bytes it took. The zone is log_timezone's abbreviation
// or, for a zone without one, a numeric offset. An abbreviation other than
// UTC is resolved the way time.ParseInLocation resolves it in time.Local:
// correctly when log_timezone is the host's zone, which is the usual case.
func pgStamp(s string) (*time.Time, int) {
	y, mo, d, ok := dbDate(s, '-')
	if !ok || len(s) < 21 || s[10] != ' ' {
		return nil, 0
	}
	h, mi, sec, n, ok := dbClock(s[11:])
	if !ok || n != 8 {
		return nil, 0
	}
	i := 19
	nsec, fn := dbFraction(s[i:])
	i += fn
	if i >= len(s) || s[i] != ' ' {
		return nil, 0
	}
	zone := s[i+1:]
	if j := strings.IndexByte(zone, ' '); j >= 0 {
		zone = zone[:j]
	}
	if zone == "" {
		return nil, 0
	}
	end := i + 1 + len(zone)
	switch {
	case zone == "UTC" || zone == "GMT":
		return dbStamp(y, mo, d, h, mi, sec, nsec, time.UTC), end
	case zone[0] == '+' || zone[0] == '-':
		off, ok := dbOffset(zone)
		if !ok {
			return nil, 0
		}
		return dbStamp(y, mo, d, h, mi, sec, nsec, time.FixedZone("", off)), end
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05 MST", s[:end], time.Local)
	if err != nil {
		return nil, 0
	}
	t = t.UTC()
	return &t, end
}

// pgJSON is the part of a jsonlog record (Postgres 15+) the lens reads.
type pgJSON struct {
	Timestamp   string `json:"timestamp"`
	User        string `json:"user"`
	DBName      string `json:"dbname"`
	PID         int    `json:"pid"`
	RemoteHost  string `json:"remote_host"`
	RemotePort  int    `json:"remote_port"`
	Severity    string `json:"error_severity"`
	StateCode   string `json:"state_code"`
	Message     string `json:"message"`
	Statement   string `json:"statement"`
	Application string `json:"application_name"`
}

func pgParseJSON(text string) (pgHead, bool) {
	var rec pgJSON
	if json.Unmarshal([]byte(text), &rec) != nil || pgLevel(rec.Severity) == "" {
		return pgHead{}, false
	}
	h := pgHead{
		user: rec.User, db: rec.DBName, app: rec.Application, client: dbIP(rec.RemoteHost),
		code: rec.StateCode, severity: rec.Severity, msg: rec.Message, statement: rec.Statement,
	}
	h.at, _ = pgStamp(rec.Timestamp)
	if rec.PID > 0 {
		h.pid = strconv.Itoa(rec.PID)
	}
	if rec.RemotePort > 0 && h.client != "" {
		h.port = strconv.Itoa(rec.RemotePort)
	}
	return h, true
}
