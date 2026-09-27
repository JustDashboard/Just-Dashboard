package logsx

import "strings"

// The syslog lens is the composite for a whole host's log — /var/log/syslog,
// /var/log/messages, the whole journal — where sshd, the kernel, cron and the
// service manager write one after another. It has no events of its own. It
// reads which program wrote each line, records it (By program is the first
// thing anyone asks of this log), and hands the message to that program's
// lens, so the sshd lines of syslog read exactly as auth.log's do. Line.Lens
// names the lens that read it, because "failed" means one thing from systemd
// and another from sshd. A lens that names the process a message is about —
// the task the kernel killed, the binary that segfaulted — records that as
// the program, as it does reading kern.log, so an OOM kill found here reads
// the same as there.
func init() {
	register(&Lens{
		ID:     "syslog",
		Events: []string{},
		Attrs:  []string{"program", "pid"},
		New:    func() Reader { return &syslogReader{readers: map[string]Reader{}, names: sysNames{}} },
	})
}

type syslogReader struct {
	// readers holds one reader per lens a program was handed to, created on
	// first use, because a lens's state (an open OOM report, the sudo command
	// being continued) is per stream. A nil entry is a lens that is not
	// built in, remembered so it is not looked up again on every line.
	readers map[string]Reader
	names   sysNames
	// scratch is the line a file's message is read through, reused so a
	// multi-gigabyte search does not allocate one per line.
	scratch Line
	// last is the lens of the last line that was not a continuation. A
	// continuation belongs to the line right above it, and in syslog that
	// can be another program's.
	last string
}

// syslogTarget names the lens for a syslog program. OpenSSH 9.8 and later log
// as sshd-session and sshd-auth, and fail2ban tags itself with its logger
// name, so those match by prefix.
func syslogTarget(program string) string {
	switch program {
	case "sudo", "su", "login", "systemd-logind", "useradd", "usermod", "userdel", "groupadd",
		"groupmod", "groupdel", "passwd", "chpasswd", "gpasswd", "chage":
		return "auth"
	case "CRON", "cron", "CROND", "crond":
		return "cron"
	case "kernel":
		return "kernel"
	case "systemd", "systemd-coredump":
		return "systemd"
	case "certbot":
		return "certbot"
	case "mysqld", "mariadbd":
		return "mysql"
	case "nginx":
		return "nginx-error"
	}
	switch {
	case strings.HasPrefix(program, "sshd"):
		return "auth"
	case strings.HasPrefix(program, "fail2ban"):
		return "fail2ban"
	case strings.HasPrefix(program, "postgres"):
		return "postgres"
	case strings.HasPrefix(program, "redis"), strings.HasPrefix(program, "valkey"):
		return "redis"
	}
	return ""
}

func (r *syslogReader) Read(l *Line) {
	m := sysEnvelope(l)
	if m.program == "" {
		r.last = ""
		return
	}
	sysSetOrigin(l, m, r.names)
	target := syslogTarget(m.program)
	reader := r.reader(target)
	if reader == nil {
		r.last = ""
		return
	}
	level, from := l.Level, l.levelFrom
	if m.own {
		// A file's line still has the envelope on it. The lens reads the
		// bare message, as it would from the journal, and what it names is
		// copied back; Text itself is never touched.
		r.scratch = Line{Text: m.text, Level: l.Level, Timestamp: l.Timestamp, Source: l.Source,
			Stream: l.Stream, Attrs: l.Attrs, levelFrom: l.levelFrom}
		reader.Read(&r.scratch)
		l.Event, l.Attrs, l.Cont, l.Lens = r.scratch.Event, r.scratch.Attrs, r.scratch.Cont, r.scratch.Lens
		l.Level, l.levelFrom = r.scratch.Level, r.scratch.levelFrom
		if l.Timestamp == nil {
			l.Timestamp = r.scratch.Timestamp
		}
		r.scratch = Line{}
	} else {
		reader.Read(l)
	}
	if l.Lens == "" {
		l.Lens = target
	}
	if l.Cont && r.last != l.Lens {
		// The record this continues is not the line above it here; drawn
		// under that line it would be wrong, so it stands on its own.
		l.Cont = false
		l.Level, l.levelFrom = level, from
	}
	if !l.Cont {
		r.last = l.Lens
	}
}

func (r *syslogReader) reader(target string) Reader {
	if target == "" {
		return nil
	}
	if reader, seen := r.readers[target]; seen {
		return reader
	}
	var reader Reader
	if lens, _ := LensByID(target); lens != nil {
		reader = lens.New()
	}
	r.readers[target] = reader
	return reader
}
