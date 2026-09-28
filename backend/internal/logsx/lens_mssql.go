package logsx

import (
	"strings"
	"time"
)

// SQL Server's errorlog as a container writes it to stdout: "2024-01-01
// 00:00:00.12 spid41s     message", the stamp in hundredths and the source
// column padded — Server, Logon, Backup, or the session that wrote it. The
// host's own errorlog file is UTF-16 and outside the log roots, so this is
// the container's output only.
//
// An error is two lines with the same stamp and source: "Error: 18456,
// Severity: 14, State: 8." and then the message it numbers. The message is
// the line that says what happened, and it comes second, so the number and
// the severity are carried forward onto it and the event goes there.

func init() {
	register(&Lens{
		ID: "mssql",
		Events: []string{
			"startup", "ready", "recovery", "backup", "log_full", "io_slow", "auth_failed", "error",
		},
		Attrs: []string{"component", "client", "code", "user"},
		New:   func() Reader { return &mssqlReader{} },
	})
}

type mssqlReader struct {
	open  bool
	level string
	// The "Error: N, Severity: S" line waiting for its message: the stamp
	// and source it was written with, which the message line repeats.
	pending                 bool
	pendingAt, pendingFrom  string
	pendingCode, pendingLvl string
}

func (r *mssqlReader) Read(l *Line) {
	text := l.Text
	at, source, msg, ok := mssqlParse(text)
	if !ok {
		// The version banner carries on over lines indented with a tab.
		if strings.HasPrefix(text, "\t") {
			l.Cont = true
			if r.open && r.level != "" {
				l.SetLevel(r.level)
			}
			return
		}
		r.open, r.pending = false, false
		return
	}
	l.Timestamp = at
	component := source
	if strings.HasPrefix(source, "spid") {
		// spid51, spid9s: the session, not a part of the server.
		component = "spid"
	}
	l.SetAttr("component", component)

	if rest, found := strings.CutPrefix(msg, "Error: "); found {
		number, _, _ := strings.Cut(rest, ",")
		severity, _ := dbNumber(dbBetween(rest, "Severity: ", ","))
		level := "warn"
		if severity >= 16 {
			level = "error"
		}
		l.SetLevel(level)
		l.SetAttr("code", number)
		r.pending, r.pendingAt, r.pendingFrom = true, text[:22], source
		r.pendingCode, r.pendingLvl = number, level
		r.open, r.level = true, level
		return
	}
	followsError := r.pending && text[:22] == r.pendingAt && source == r.pendingFrom
	r.pending = false
	if followsError {
		l.SetLevel(r.pendingLvl)
		l.SetAttr("code", r.pendingCode)
	}

	switch {
	case strings.HasPrefix(msg, "Microsoft SQL Server "):
		l.Event = "startup"
	case strings.HasPrefix(msg, "SQL Server is now ready for client connections"):
		l.Event = "ready"
	case strings.HasPrefix(msg, "Recovery is complete"), strings.HasPrefix(msg, "Recovery of database"),
		strings.HasPrefix(msg, "Recovery completed for database"):
		l.Event = "recovery"
	case strings.HasPrefix(msg, "BACKUP "), strings.HasPrefix(msg, "RESTORE "),
		strings.HasPrefix(msg, "Database backed up."), strings.HasPrefix(msg, "Log was backed up."),
		strings.HasPrefix(msg, "Database was restored"):
		l.Event = "backup"
	case strings.HasPrefix(msg, "The transaction log for database"):
		l.Event = "log_full"
	case strings.Contains(msg, "I/O requests taking longer than"):
		l.Event = "io_slow"
	case strings.HasPrefix(msg, "Login failed for user '"):
		// "Login failed for user 'sa'. Reason: Password did not match that
		// for the login provided. [CLIENT: 172.17.0.1]"
		l.Event = "auth_failed"
		l.SetLevel("warn")
		l.SetAttr("user", dbBetween(msg, "for user '", "'"))
		l.SetAttr("client", dbIP(dbBetween(msg, "[CLIENT: ", "]")))
	case followsError:
		l.Event = "error"
	}
	r.open, r.level = true, l.Level
}

// mssqlParse reads the stamp, the source column and the message.
func mssqlParse(text string) (at *time.Time, source, msg string, ok bool) {
	if len(text) < 25 || text[19] != '.' || text[22] != ' ' {
		return nil, "", "", false
	}
	y, mo, d, ok := dbDate(text, '-')
	if !ok || text[10] != ' ' {
		return nil, "", "", false
	}
	h, mi, sec, n, ok := dbClock(text[11:])
	if !ok || n != 8 {
		return nil, "", "", false
	}
	nsec, fn := dbFraction(text[19:])
	if fn != 3 {
		return nil, "", "", false
	}
	source, msg, found := strings.Cut(text[23:], " ")
	if !found || source == "" {
		return nil, "", "", false
	}
	return dbStamp(y, mo, d, h, mi, sec, nsec, time.Local), source, strings.TrimLeft(msg, " "), true
}
