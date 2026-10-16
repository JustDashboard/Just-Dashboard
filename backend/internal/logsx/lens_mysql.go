package logsx

import (
	"strings"
	"time"
)

// The MySQL family's error log: MySQL 8's "stamp thread [Label] [MY-code]
// [Subsystem] message", 5.7's same line without the code, MariaDB's naive
// local stamp with its hour padded by a space, and the Docker entrypoint's
// own lines between them. Only the error log: the slow log is a block of
// comment lines that precede the statement they describe, which a
// forward-only reader cannot fold, and the database page reads slow queries
// over SQL instead.
//
// A line without a stamp continues the one above it. That is how the InnoDB
// deadlock dump, a crash's stack trace and MariaDB's "Version: …" line after
// "ready for connections" arrive.

func init() {
	register(&Lens{
		ID: "mysql",
		Events: []string{
			"startup", "ready", "shutdown", "crash", "innodb", "deadlock",
			"aborted_connection", "too_many_connections", "auth_failed", "replication",
			"replication_failed", "recovery", "startup_failed", "disk_full", "table_full", "oom",
			"io_slow", "table_crashed", "config_warning", "error",
		},
		Attrs: []string{
			"thread", "code", "component", "user", "client", "db", "reason", "table", "duration_ms",
		},
		New: func() Reader { return &mysqlReader{} },
	})
}

type mysqlReader struct {
	open  bool
	level string
	// dump is set while an InnoDB deadlock report is being written. MySQL 8
	// logs each of its sections as a line of its own (MY-012469, starting
	// "*** "), and those belong to the report, not beside it.
	dump bool
}

type mysqlHead struct {
	// at is nil for the RFC 3339 stamp of MySQL 5.7 and 8, which ParseLine
	// already read with its zone.
	at                   *time.Time
	thread, label        string
	code, component, msg string
	crash                bool
}

func (r *mysqlReader) Read(l *Line) {
	h, ok := mysqlParse(l.Text)
	if !ok {
		if r.open {
			l.Cont = true
			if r.level != "" {
				l.SetLevel(r.level)
			}
		}
		return
	}
	if h.at != nil {
		l.Timestamp = h.at
	}
	// The thread, label, code and subsystem are the attrs and the level.
	l.SetBody(h.msg)
	if r.dump && (h.code == "MY-012469" || strings.HasPrefix(h.msg, "*** ")) {
		l.Cont = true
		l.SetLevel(r.level)
		return
	}
	r.dump = false

	switch h.label {
	case "System", "Note":
		l.SetLevel("info")
	case "Warning", "Warn":
		l.SetLevel("warn")
	default:
		l.SetLevel("error")
	}
	if h.thread != "0" {
		l.SetAttr("thread", h.thread)
	}
	l.SetAttr("code", h.code)
	l.SetAttr("component", h.component)

	msg := h.msg
	switch {
	case h.crash || strings.Contains(msg, "got signal ") || strings.Contains(msg, "Assertion failure") ||
		strings.Contains(msg, "got exception") || strings.Contains(msg, "We intentionally crash the server"):
		l.Event = "crash"
		l.SetLevel("critical")
	case strings.Contains(msg, "deadlock detected"):
		l.Event = "deadlock"
		r.dump = true
	case strings.Contains(msg, "starting as process"), strings.HasPrefix(msg, "Starting MariaDB"):
		l.Event = "startup"
	case strings.Contains(msg, "ready for connections") && !strings.HasPrefix(msg, "X Plugin"):
		l.Event = "ready"
	case strings.Contains(msg, "Shutdown complete"), strings.Contains(msg, "Received SHUTDOWN"),
		strings.Contains(msg, "Normal shutdown"):
		l.Event = "shutdown"
	case strings.HasPrefix(msg, "Access denied for user '"):
		// 'root'@'172.18.0.1' — the host part may be a name or a pattern.
		l.Event = "auth_failed"
		l.SetAttr("user", dbBetween(msg, "for user '", "'@"))
		l.SetAttr("client", dbIP(dbBetween(msg, "'@'", "'")))
	case strings.HasPrefix(msg, "Aborted connection "):
		// The reason in parentheses may be the connection limit, which is
		// its own event: the server refusing work, not a client going away.
		l.Event = "aborted_connection"
		if strings.Contains(msg, "(Too many connections)") {
			l.Event = "too_many_connections"
		}
		// MariaDB names a connection that never authenticated or chose a
		// database with placeholders, which are not a user or a database.
		if db := dbBetween(msg, "db: '", "'"); db != "unconnected" {
			l.SetAttr("db", db)
		}
		if user := dbBetween(msg, "user: '", "'"); user != "unauthenticated" {
			l.SetAttr("user", user)
		}
		l.SetAttr("client", dbIP(dbBetween(msg, "host: '", "'")))
		if l.Event == "aborted_connection" {
			l.SetAttr("reason", mysqlReason(msg))
		}
	case strings.Contains(msg, "Too many connections"):
		l.Event = "too_many_connections"
	case msg == "Aborting":
		// The server gave up starting; the line before it says why.
		l.Event = "startup_failed"
		l.SetLevel("critical")
	case strings.HasPrefix(msg, "Database was not shutdown normally"), strings.HasPrefix(msg, "Starting crash recovery"):
		// A restart that nothing shut down first is what the OOM killer
		// leaves behind, so it is worth a warning though InnoDB calls it news.
		l.Event = "recovery"
		l.SetLevel("warn")
	case strings.HasPrefix(msg, "Disk is full writing"), strings.Contains(msg, "Operating system error number 28"),
		strings.Contains(msg, "No space left on device"):
		l.Event = "disk_full"
		l.SetLevel("critical")
	case strings.Contains(msg, "Out of memory"), strings.Contains(msg, "bytes) failed; errno 12"),
		strings.Contains(msg, "Cannot allocate memory"):
		l.Event = "oom"
		l.SetLevel("critical")
	case strings.HasSuffix(msg, "' is full") && strings.Contains(msg, "The table '"):
		// MariaDB prefixes the program name to errors it passes through.
		l.Event = "table_full"
		l.SetLevel("error")
		l.SetAttr("table", dbBetween(msg, "The table '", "'"))
	case strings.Contains(msg, "is marked as crashed"):
		l.Event = "table_crashed"
		l.SetLevel("error")
		l.SetAttr("table", dbBetween(msg, "Table '", "'"))
	case strings.Contains(msg, "intended loop took"), strings.HasPrefix(msg, "A long semaphore wait"):
		// Both say the disk is not keeping up; only the page cleaner says by
		// how much. The semaphore wait that ends in a crash is caught above.
		l.Event = "io_slow"
		l.SetLevel("warn")
		dbMillis(l, "duration_ms", dbBetween(msg, "loop took ", "ms"), 1)
	case mysqlReplicationFailed(h.component, l.Level, msg):
		l.Event = "replication_failed"
		l.SetLevel("error")
		if h.code == "" {
			l.SetAttr("code", strings.TrimRight(dbWord(msg, "Error_code: "), ";,"))
		}
		l.SetAttr("client", mysqlSourceHost(msg))
	case h.component == "Repl", strings.HasPrefix(msg, "Slave "), strings.HasPrefix(msg, "Replica "):
		l.Event = "replication"
	case l.Level == "warn" && mysqlConfigWarning(msg):
		l.Event = "config_warning"
	case h.component == "InnoDB":
		l.Event = "innodb"
	case l.Level == "error":
		l.Event = "error"
	}
	r.open, r.level = true, l.Level
}

// mysqlReason is the text in the last parentheses of an aborted connection's
// message: the server's account of why it let go.
func mysqlReason(msg string) string {
	i := strings.LastIndexByte(msg, '(')
	if i < 0 {
		return ""
	}
	reason, _, _ := strings.Cut(msg[i+1:], ")")
	return reason
}

// mysqlReplicationFailed is a replication thread that stopped or could not
// connect: anything the Repl component logs as an error, and the older
// "Slave SQL" and "Replica I/O" lines, which carry no component but say
// Error_code when they are failing and nothing when they are merely working.
func mysqlReplicationFailed(component, level, msg string) bool {
	if component == "Repl" && level == "error" {
		return true
	}
	for _, p := range []string{"Replica SQL", "Slave SQL", "Replica I/O", "Slave I/O"} {
		if strings.HasPrefix(msg, p) && strings.Contains(msg, "Error_code:") {
			return true
		}
	}
	return strings.HasPrefix(msg, "Error reconnecting to ")
}

// mysqlSourceHost is the address in "source 'repl@10.0.0.1:3306'", "master"
// before 8.0.26, or "" when it is a host name.
func mysqlSourceHost(msg string) string {
	src := dbBetween(msg, "source '", "'")
	if src == "" {
		src = dbBetween(msg, "master '", "'")
	}
	_, host, _ := strings.Cut(src, "@")
	if strings.HasPrefix(host, "[") {
		host, _, _ = strings.Cut(host, "]")
	} else if i := strings.LastIndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	return dbIP(host)
}

// mysqlConfigWarning is the warnings about how the server was configured
// rather than what it is doing: every one of them is fixed in my.cnf.
func mysqlConfigWarning(msg string) bool {
	for _, s := range []string{"deprecated", "self signed", "Insecure configuration", "secure-file-priv",
		"secure_file_priv", "Changed limits"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func mysqlParse(text string) (mysqlHead, bool) {
	var h mysqlHead
	if len(text) < 16 || text[0] < '0' || text[0] > '9' {
		return h, false
	}
	var rest string
	switch {
	case text[4] == '-' && text[10] == 'T':
		// MySQL 5.7 and 8 write RFC 3339, UTC unless log_timestamps says
		// SYSTEM, and then with the offset.
		sp := strings.IndexByte(text, ' ')
		if sp < 0 {
			return h, false
		}
		rest = text[sp+1:]
		// The crash banner is written by a signal handler with its own
		// spelling: "2023-06-26T08:20:10Z UTC - mysqld got signal 11 ;".
		if tail, found := strings.CutPrefix(rest, "UTC - "); found {
			h.label, h.msg, h.crash = "ERROR", tail, true
			return h, true
		}
	case text[4] == '-':
		// MariaDB, MySQL 5.6 and the entrypoint write the host's local time.
		y, mo, d, ok := dbDate(text, '-')
		if !ok {
			return h, false
		}
		at, n := mysqlClock(text[10:], y, mo, d)
		if at == nil {
			return h, false
		}
		h.at, rest = at, text[10+n:]
	case dbDigits(text) == 6 && text[6] == ' ':
		// MariaDB before 10.1.5 and MySQL 5.5: "160615 16:53:08".
		yy, _ := dbNumber(text[0:2])
		mo, okM := dbNumber(text[2:4])
		d, okD := dbNumber(text[4:6])
		if !okM || !okD || mo < 1 || mo > 12 || d < 1 || d > 31 {
			return h, false
		}
		at, n := mysqlClock(text[6:], 2000+yy, mo, d)
		if at == nil {
			return h, false
		}
		h.at, rest = at, text[6+n:]
	default:
		return h, false
	}

	if n := dbDigits(rest); n > 0 && n < len(rest) && rest[n] == ' ' {
		h.thread, rest = rest[:n], strings.TrimLeft(rest[n:], " ")
	}
	label, rest, _ := mysqlBracket(rest)
	switch label {
	case "System", "Note", "Warning", "Warn", "ERROR", "Error":
	default:
		return h, false
	}
	h.label = label
	if code, after, ok := mysqlBracket(rest); ok && strings.HasPrefix(code, "MY-") {
		h.code, rest = code, after
		if component, after, ok := mysqlBracket(rest); ok {
			h.component, rest = component, after
		}
	} else if tail, found := strings.CutPrefix(rest, "[Entrypoint]: "); found {
		h.component, rest = "Entrypoint", tail
	} else if word, tail, found := strings.Cut(rest, ": "); found {
		// 5.7 and MariaDB name the storage engine in the message instead.
		switch word {
		case "InnoDB", "Aria", "WSREP", "Galera":
			h.component, rest = word, tail
		}
	}
	h.msg = rest
	return h, true
}

// mysqlClock reads the time after a naive date — one or two spaces, then
// "H:MM:SS" — and the entrypoint's "+00:00" after it when there is one,
// answering how many bytes it took including the space that follows.
func mysqlClock(s string, y, mo, d int) (*time.Time, int) {
	i := 0
	for i < len(s) && i < 2 && s[i] == ' ' {
		i++
	}
	if i == 0 {
		return nil, 0
	}
	h, mi, sec, n, ok := dbClock(s[i:])
	if !ok {
		return nil, 0
	}
	i += n
	loc := time.Local
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		end := i + 1
		for end < len(s) && s[end] != ' ' {
			end++
		}
		off, ok := dbOffset(s[i:end])
		if !ok {
			return nil, 0
		}
		loc, i = time.FixedZone("", off), end
	}
	if i >= len(s) || s[i] != ' ' {
		return nil, 0
	}
	return dbStamp(y, mo, d, h, mi, sec, 0, loc), i + 1
}

// mysqlBracket reads "[word] " off the front of s.
func mysqlBracket(s string) (word, rest string, ok bool) {
	if !strings.HasPrefix(s, "[") {
		return "", s, false
	}
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return "", s, false
	}
	return s[1:end], strings.TrimPrefix(s[end+1:], " "), true
}
