package logsx

import (
	"strings"
	"time"
)

// The fail2ban lens reads fail2ban's own log, "2023-02-17 23:44:17,037
// fail2ban.actions        [992]: NOTICE  [sshd] Ban 203.0.113.228": the
// strikes a filter counted (Found), the bans they earned, and the jails
// starting and stopping. The same message reaches the journal and syslog
// without the stamp, and without the logger when syslog took it as the tag,
// so each part of the envelope is optional and read in turn.
func init() {
	register(&Lens{
		ID: "fail2ban",
		Events: []string{"ban", "unban", "restore_ban", "increase", "found", "ignore",
			"already_banned", "jail_started", "jail_stopped", "error"},
		Attrs: []string{"jail", "client", "pid"},
		New:   func() Reader { return ReaderFunc(fail2banRead) },
	})
}

func fail2banRead(l *Line) {
	var msg, pid string
	text := l.Text
	if at, ok := fail2banStamp(text); ok {
		l.Timestamp = &at
		msg = text[len("2006-01-02 15:04:05,000 "):]
	} else {
		m := sysEnvelope(l)
		msg, pid = m.text, m.pid
	}
	// "fail2ban.actions        [992]: " — the logger is padded to a column.
	if strings.HasPrefix(msg, "fail2ban.") {
		open := strings.IndexByte(msg, '[')
		shut := strings.Index(msg, "]: ")
		if open < 0 || shut < open {
			return
		}
		pid, msg = msg[open+1:shut], msg[shut+len("]: "):]
	}
	// The level word is fail2ban's own; the journal's priority stands in for
	// it when a handler left it out.
	word, rest, _ := strings.Cut(msg, " ")
	level, known := fail2banLevels[word]
	if known {
		msg = strings.TrimLeft(rest, " ")
	}
	var jail string
	if strings.HasPrefix(msg, "[") {
		if shut := strings.Index(msg, "] "); shut > 0 {
			jail, msg = msg[1:shut], msg[shut+2:]
		}
	}
	event, addr := fail2banEvent(msg)
	if event == "jail_started" || event == "jail_stopped" {
		jail = addr
		addr = ""
	}
	if event == "" && (level == "error" || level == "critical") {
		event = "error"
	}
	if known {
		l.SetLevel(level)
	}
	if event == "" {
		return
	}
	l.Event = event
	if event == "ban" {
		// A ban is the moment fail2ban acted; NOTICE undersells it next to
		// the thousands of strikes around it.
		l.SetLevel("warn")
	}
	l.SetAttr("jail", jail)
	l.SetAttr("client", sysAddr(addr))
	l.SetAttr("pid", pid)
}

// fail2banLevels is Python logging's vocabulary plus fail2ban's own debug
// grades, as fail2ban pads them into the column after the pid.
var fail2banLevels = map[string]string{
	"CRITICAL": "critical", "ERROR": "error", "WARNING": "warn",
	"NOTICE": "info", "INFO": "info",
	"DEBUG": "debug", "HEAVYDEBUG": "debug", "TRACEDEBUG": "debug",
}

// fail2banEvent names the message after the jail, with the address or jail
// name it is about. The strings are fail2ban's own (actions.py, filter.py,
// observer.py, jail.py) and have not changed since 0.10.
func fail2banEvent(msg string) (event, subject string) {
	switch {
	case strings.HasPrefix(msg, "Ban "):
		return "ban", sysUntil(msg[len("Ban "):], ' ')
	case strings.HasPrefix(msg, "Found "):
		return "found", sysUntil(msg[len("Found "):], ' ')
	case strings.HasPrefix(msg, "Unban "):
		return "unban", sysUntil(msg[len("Unban "):], ' ')
	case strings.HasPrefix(msg, "Restore Ban "):
		// Re-applied from fail2ban's database after a restart: not a new
		// offence, and counted apart so a restart is not a wave of bans.
		return "restore_ban", sysUntil(msg[len("Restore Ban "):], ' ')
	case strings.HasPrefix(msg, "Increase Ban "):
		return "increase", sysUntil(msg[len("Increase Ban "):], ' ')
	case strings.HasPrefix(msg, "Ignore "):
		return "ignore", sysUntil(msg[len("Ignore "):], ' ')
	case strings.HasSuffix(msg, " already banned"):
		return "already_banned", strings.TrimSuffix(msg, " already banned")
	case strings.HasPrefix(msg, "Jail '"):
		name, state, ok := strings.Cut(msg[len("Jail '"):], "' ")
		if !ok {
			return "", ""
		}
		switch state {
		case "started":
			return "jail_started", name
		case "stopped":
			return "jail_stopped", name
		}
	}
	return "", ""
}

// fail2banStamp reads "2006-01-02 15:04:05,000", which fail2ban writes in
// local time with no zone.
func fail2banStamp(s string) (time.Time, bool) {
	const layout = "2006-01-02 15:04:05,000 "
	if len(s) < len(layout) || s[4] != '-' || s[7] != '-' || s[10] != ' ' || s[13] != ':' ||
		s[16] != ':' || (s[19] != ',' && s[19] != '.') || s[23] != ' ' {
		return time.Time{}, false
	}
	year, month, day := sysNumber(s[0:4]), sysNumber(s[5:7]), sysNumber(s[8:10])
	hour, minute, second := sysNumber(s[11:13]), sysNumber(s[14:16]), sysNumber(s[17:19])
	milli := sysNumber(s[20:23])
	if year < 0 || month < 1 || month > 12 || day < 1 || day > 31 || hour < 0 || hour > 23 ||
		minute < 0 || minute > 59 || second < 0 || second > 60 || milli < 0 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, milli*int(time.Millisecond), time.Local), true
}
