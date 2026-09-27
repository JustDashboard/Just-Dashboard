package logsx

import (
	"strconv"
	"strings"
	"time"
)

// Redis, Valkey and KeyDB write "pid:R dd Mon yyyy HH:MM:SS.mmm C message":
// the role letter says which process wrote the line — the primary, a
// replica, or the fork that is saving a snapshot — and the character before
// the message is the level. Redis before 4 left the year out, and before 3
// wrote "[pid]" instead of "pid:R". A signal handler, which may not call
// the formatting code, writes "pid:signal-handler (epoch) message".
//
// Redis has no error level: "#" is its warning, and a failed background
// save is logged at "#" too. So the lens raises the failures it recognises,
// because a snapshot that did not save is the one line an operator must not
// read as a warning among warnings.

func init() {
	register(&Lens{
		ID: "redis",
		Events: []string{
			"startup", "ready", "shutdown", "loading", "saved", "bgsave", "aof",
			"persistence_failed", "memory_warning", "replication", "security_attack",
			"client_closed", "crash",
		},
		Attrs: []string{"pid", "role"},
		New:   func() Reader { return &redisReader{} },
	})
}

type redisReader struct {
	// report is set between a crash's "BUG REPORT START" and its end: every
	// line of the report — registers, the stack, INFO, the client list —
	// belongs to the crash, stamped or not.
	report bool
}

type redisHead struct {
	pid  string
	role byte
	at   *time.Time
	// level is the character before the message; zero for a line from the
	// signal handler, which has none.
	level byte
	msg   string
}

func (r *redisReader) Read(l *Line) {
	text := l.Text
	h, ok := redisParse(text)
	if r.report {
		if !ok || redisEvent(h.msg) != "startup" {
			if strings.Contains(text, "BUG REPORT END") {
				r.report = false
			}
			if ok && h.at != nil {
				l.Timestamp = h.at
			}
			l.Cont = true
			l.SetLevel("critical")
			return
		}
		// A report cut off by the process dying is ended by the next start.
		r.report = false
	}
	if !ok {
		if strings.Contains(text, "BUG REPORT START") {
			l.Event = "crash"
			l.SetLevel("critical")
			r.report = true
		}
		// Valkey 8.1 can log as JSON; ParseLine has already read its level
		// and message, and the words are the same.
		if l.Message != "" && l.Fields["role"] != "" && l.Fields["pid"] != "" {
			r.name(l, l.Message)
			dbSetWord(l, "pid", l.Fields["pid"])
			dbSetWord(l, "role", redisRole(l.Fields["role"]))
		}
		return
	}
	if h.at != nil {
		l.Timestamp = h.at
	}
	switch h.level {
	case '.', '-':
		l.SetLevel("debug")
	case '*':
		l.SetLevel("info")
	case '#':
		l.SetLevel("warn")
	}
	l.SetAttr("pid", h.pid)
	switch h.role {
	case 'M':
		dbSetWord(l, "role", "primary")
	case 'S':
		dbSetWord(l, "role", "replica")
	case 'C':
		dbSetWord(l, "role", "child")
	case 'X':
		dbSetWord(l, "role", "sentinel")
	}
	r.name(l, h.msg)
}

func (r *redisReader) name(l *Line, msg string) {
	l.Event = redisEvent(msg)
	switch l.Event {
	case "crash":
		l.SetLevel("critical")
	case "persistence_failed":
		l.SetLevel("error")
	}
}

// redisRole puts Valkey's role words in the vocabulary the legacy letters
// are read into, so one facet covers both servers.
func redisRole(role string) string {
	switch role {
	case "primary", "master":
		return "primary"
	case "replica", "slave":
		return "replica"
	case "RDB/AOF":
		return "child"
	}
	return role
}

// redisEvent names a message. The strings are the servers' own, from
// server.c, rdb.c, aof.c, replication.c and networking.c. Most messages are
// known by how they start, which costs a byte comparison to rule out, so the
// prefixes are tried first and the phrases that can sit anywhere in a
// message last; within each table the order matters where one message
// contains another's words — a failed AOF rewrite is a persistence failure
// before it is AOF news, and "DB loaded from append only file" is loading.
func redisEvent(msg string) string {
	for _, p := range redisPrefixes {
		if strings.HasPrefix(msg, p.text) {
			return p.event
		}
	}
	for _, p := range redisPhrases {
		if strings.Contains(msg, p.text) {
			return p.event
		}
	}
	return ""
}

var redisPrefixes = []struct{ text, event string }{
	{"Out Of Memory allocating", "crash"},
	{"Background saving error", "persistence_failed"},
	{"Background saving terminated by signal", "persistence_failed"},
	{"Can't save in background", "persistence_failed"},
	{"Write error saving DB", "persistence_failed"},
	{"Write error while saving DB", "persistence_failed"},
	{"Write error writing append only file", "persistence_failed"},
	{"Failed opening the temp RDB file", "persistence_failed"},
	{"Failed opening the RDB file", "persistence_failed"},
	{"Failed opening .rdb", "persistence_failed"},
	{"Error trying to save the DB", "persistence_failed"},
	{"Background AOF rewrite terminated with error", "persistence_failed"},
	{"Background AOF rewrite terminated by signal", "persistence_failed"},
	{"Error writing to the AOF file", "persistence_failed"},
	{"Fail to fsync the AOF file", "persistence_failed"},
	{"Can't recover from AOF write error", "persistence_failed"},
	{"Bad file format reading the append only file", "persistence_failed"},
	{"Unexpected end of file reading the append only file", "persistence_failed"},
	{"Wrong RDB checksum", "persistence_failed"},
	{"Short read or OOM loading DB", "persistence_failed"},
	{"Fatal error loading the DB", "persistence_failed"},
	{"Error moving temp DB file", "persistence_failed"},
	{"Possible SECURITY ATTACK detected", "security_attack"},
	{"Client closed connection", "client_closed"},
	{"Evicting client", "client_closed"},
	{"Closing client that reached max query buffer", "client_closed"},
	{"oO0OoO0OoO0Oo", "startup"},
	{"Server started, Redis version", "startup"},
	{"Ready to accept connections", "ready"},
	{"The server is now ready to accept connections", "ready"},
	{"User requested shutdown", "shutdown"},
	{"Received SIG", "shutdown"},
	{"Received shutdown signal", "shutdown"},
	{"Loading RDB produced by", "loading"},
	{"DB loaded from", "loading"},
	{"DB pre-loaded from", "loading"},
	{"Done loading RDB", "loading"},
	{"DB saved on disk", "saved"},
	{"BGSAVE done", "saved"},
	{"Background saving started", "bgsave"},
	{"Background saving terminated with success", "bgsave"},
	{"Fork CoW for RDB", "bgsave"},
}

var redisPhrases = []struct{ text, event string }{
	{"crashed by signal", "crash"},
	{"ASSERTION FAILED", "crash"},
	{"Guru Meditation", "crash"},
	{"scheduled to be closed ASAP", "client_closed"},
	{"closed for overcoming of output buffer limits", "client_closed"},
	{"overcommit", "memory_warning"},
	{"Transparent Huge Pages", "memory_warning"},
	{"maxmemory", "memory_warning"},
	{"ready to exit, bye bye", "shutdown"},
	{" seconds. Saving...", "bgsave"},
	{"MASTER", "replication"},
	{"master", "replication"},
	{"PRIMARY", "replication"},
	{"primary", "replication"},
	{"REPLICA", "replication"},
	{"eplica", "replication"},
	{"resynchronization", "replication"},
	{"SLAVE", "replication"},
	{"BGSAVE for SYNC", "replication"},
	{"AOF", "aof"},
	{"append only file", "aof"},
}

func redisParse(text string) (redisHead, bool) {
	var h redisHead
	if len(text) < 20 {
		return h, false
	}
	var rest string
	if text[0] == '[' {
		n := dbDigits(text[1:])
		if n == 0 || 3+n > len(text) || text[1+n] != ']' || text[2+n] != ' ' {
			return h, false
		}
		h.pid, rest = text[1:1+n], text[3+n:]
	} else {
		n := dbDigits(text)
		if n == 0 || n == len(text) || text[n] != ':' {
			return h, false
		}
		h.pid, rest = text[:n], text[n+1:]
		if tail, found := strings.CutPrefix(rest, "signal-handler ("); found {
			epoch, msg, ok := strings.Cut(tail, ") ")
			seconds, err := strconv.ParseInt(epoch, 10, 64)
			if !ok || err != nil {
				return h, false
			}
			at := time.Unix(seconds, 0).UTC()
			h.at, h.msg = &at, msg
			return h, true
		}
		if len(rest) < 2 || rest[1] != ' ' || strings.IndexByte("MSCX", rest[0]) < 0 {
			return h, false
		}
		h.role, rest = rest[0], rest[2:]
	}

	// dd Mon [yyyy] HH:MM:SS[.mmm]
	dayLen := dbDigits(rest)
	if dayLen < 1 || dayLen > 2 || len(rest) < dayLen+5 || rest[dayLen] != ' ' || rest[dayLen+4] != ' ' {
		return h, false
	}
	d, _ := dbNumber(rest[:dayLen])
	month, ok := dbMonths[rest[dayLen+1:dayLen+4]]
	if !ok {
		return h, false
	}
	rest = rest[dayLen+5:]
	y := 0
	if dbDigits(rest) == 4 && len(rest) > 4 && rest[4] == ' ' {
		y, _ = dbNumber(rest[:4])
		rest = rest[5:]
	}
	hour, mi, sec, n, ok := dbClock(rest)
	if !ok || n != 8 {
		return h, false
	}
	nsec, fn := dbFraction(rest[n:])
	rest = rest[n+fn:]
	if len(rest) < 3 || rest[0] != ' ' || rest[2] != ' ' || strings.IndexByte(".-*#", rest[1]) < 0 {
		return h, false
	}
	h.level, h.msg = rest[1], rest[3:]
	if y == 0 {
		// No year: this one, unless that would put the line in the future,
		// which is what a line from last December read in January does.
		now := time.Now()
		h.at = dbStamp(now.Year(), int(month), d, hour, mi, sec, nsec, time.Local)
		if h.at.After(now.Add(24 * time.Hour)) {
			h.at = dbStamp(now.Year()-1, int(month), d, hour, mi, sec, nsec, time.Local)
		}
		return h, true
	}
	h.at = dbStamp(y, int(month), d, hour, mi, sec, nsec, time.Local)
	return h, true
}
