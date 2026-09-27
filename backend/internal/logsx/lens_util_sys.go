package logsx

import (
	"net/netip"
	"strings"
	"time"
)

// The host's own logs — auth.log, kern.log, ufw.log, syslog, /var/log/cron —
// share one envelope, rsyslog's "stamp host program[pid]: message", and the
// system lenses meet the same sshd or kernel line dressed three ways:
//
//   - read straight from a file, with the envelope still on it;
//   - from the journal, where the engine has already put the program and the
//     pid in Attrs and the text is the bare message;
//   - handed on by the syslog composite, which strips the envelope and sets
//     program and pid exactly as the journal does.
//
// sysEnvelope is the one place that tells these apart, so a lens is written
// once against the bare message and reads auth.log and journal:ssh.service
// alike.

// sysMessage is a line's program, pid and message with the envelope removed.
type sysMessage struct {
	program, pid, text string
	// own says the envelope was read here, off the line's own text. A lens
	// then records program and pid itself; when they came in Attrs they are
	// already there.
	own bool
}

// sysEnvelope answers the program, pid and message a system line carries. A
// program already in Attrs means something upstream took the envelope off,
// and the text is then the message itself — reading a prefix off it again
// would mistake a message that happens to start with a date for a second
// envelope. A BSD stamp is re-read here in local time: it has no zone, and
// the generic parse would otherwise file every line on a non-UTC host under
// the wrong hour.
func sysEnvelope(l *Line) sysMessage {
	if program := l.Attrs["program"]; program != "" {
		return sysMessage{program: program, pid: l.Attrs["pid"], text: sysUnrepeat(l.Text)}
	}
	program, pid, msg, stamp, ok := sysPrefix(l.Text)
	if !ok {
		return sysMessage{text: sysUnrepeat(l.Text)}
	}
	if stamp != "" {
		if at, ok := sysBSDStamp(stamp, time.Now()); ok {
			l.Timestamp = &at
		}
	}
	return sysMessage{program: program, pid: pid, text: sysUnrepeat(msg), own: true}
}

// sysPrefix splits "stamp host program[pid]: message". The stamp is RFC 3339
// (rsyslog's default since Ubuntu 24.04, which the generic parse already
// reads with its zone) or BSD's "Jan  2 15:04:05", which it returns so the
// caller can place it in local time. The checks are byte tests at fixed
// offsets because every line of a multi-gigabyte syslog comes through here.
func sysPrefix(text string) (program, pid, msg, bsd string, ok bool) {
	if len(text) < 17 {
		return "", "", "", "", false
	}
	var rest string
	switch c := text[0]; {
	case c >= '0' && c <= '9':
		if text[4] != '-' || text[7] != '-' || text[10] != 'T' {
			return "", "", "", "", false
		}
		sp := strings.IndexByte(text, ' ')
		if sp < 19 {
			return "", "", "", "", false
		}
		rest = text[sp+1:]
	case c >= 'A' && c <= 'Z':
		if text[3] != ' ' || text[6] != ' ' || text[9] != ':' || text[12] != ':' || text[15] != ' ' {
			return "", "", "", "", false
		}
		bsd, rest = text[:15], text[16:]
	default:
		return "", "", "", "", false
	}
	sp := strings.IndexByte(rest, ' ')
	if sp <= 0 {
		return "", "", "", "", false
	}
	rest = rest[sp+1:]
	// The tag runs to the pid's bracket or the colon; a space first means
	// this is prose, not a tag, and the line is not in syslog's shape.
	end := strings.IndexAny(rest, "[: ")
	if end <= 0 || rest[end] == ' ' {
		return "", "", "", "", false
	}
	program, rest = rest[:end], rest[end:]
	if rest[0] == '[' {
		rb := strings.IndexByte(rest, ']')
		if rb < 2 || sysNumber(rest[1:rb]) < 0 {
			return "", "", "", "", false
		}
		pid, rest = rest[1:rb], rest[rb+1:]
	}
	if rest == "" || rest[0] != ':' {
		return "", "", "", "", false
	}
	return program, pid, strings.TrimPrefix(rest[1:], " "), bsd, true
}

var sysMonths = map[string]time.Month{
	"Jan": time.January, "Feb": time.February, "Mar": time.March, "Apr": time.April,
	"May": time.May, "Jun": time.June, "Jul": time.July, "Aug": time.August,
	"Sep": time.September, "Oct": time.October, "Nov": time.November, "Dec": time.December,
}

// sysBSDStamp reads "Jan  2 15:04:05" in local time. The format has no year:
// the current one is assumed and walked back while that would put the line
// more than a day in the future — which is also what places a "Feb 29" in the
// last year that had one.
func sysBSDStamp(s string, now time.Time) (time.Time, bool) {
	month, ok := sysMonths[s[:3]]
	if !ok {
		return time.Time{}, false
	}
	day := sysTwoDigits(s[4:6])
	hour, minute, second := sysTwoDigits(s[7:9]), sysTwoDigits(s[10:12]), sysTwoDigits(s[13:15])
	if day < 1 || day > 31 || hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 60 {
		return time.Time{}, false
	}
	limit := now.Add(24 * time.Hour)
	for year := now.Year(); year > now.Year()-8; year-- {
		at := time.Date(year, month, day, hour, minute, second, 0, time.Local)
		if at.Day() != day || at.After(limit) {
			continue
		}
		return at, true
	}
	return time.Time{}, false
}

// sysTwoDigits reads a day, hour, minute or second; BSD pads the day with a
// space rather than a zero. It answers -1 for anything else.
func sysTwoDigits(s string) int {
	a, b := s[0], s[1]
	if a == ' ' {
		a = '0'
	}
	if a < '0' || a > '9' || b < '0' || b > '9' {
		return -1
	}
	return int(a-'0')*10 + int(b-'0')
}

// sysNumber reads a fixed-width run of digits, answering -1 when any byte is
// not one.
func sysNumber(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return -1
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// sysDigits answers the run of digits s starts with.
func sysDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// sysUntil answers the value s starts with, which runs to stop or to the
// next space.
func sysUntil(s string, stop byte) string {
	for i := 0; i < len(s); i++ {
		if s[i] == stop || s[i] == ' ' {
			return s[:i]
		}
	}
	return s
}

// sysUnrepeat unwraps rsyslog's "message repeated 148 times: [ Failed
// password for root …]", which is the line it repeats: left wrapped, the
// hundred and forty-eight failures from one address would be the only ones a
// lens could not see.
func sysUnrepeat(msg string) string {
	if !strings.HasPrefix(msg, "message repeated ") {
		return msg
	}
	i := strings.Index(msg, " times: [")
	if i < 0 {
		return msg
	}
	inner := strings.TrimSuffix(msg[i+len(" times: ["):], "]")
	return strings.TrimPrefix(inner, " ")
}

// sysStripUptime drops the "[ 1234.567890] " printk time older kernels put in
// front of every kern.log message, so a UFW line reads the same with or
// without it.
func sysStripUptime(msg string) string {
	if len(msg) < 4 || msg[0] != '[' {
		return msg
	}
	rb := strings.IndexByte(msg, ']')
	if rb < 2 {
		return msg
	}
	for i := 1; i < rb; i++ {
		if c := msg[i]; c != ' ' && c != '.' && (c < '0' || c > '9') {
			return msg
		}
	}
	return strings.TrimPrefix(msg[rb+1:], " ")
}

// sysAddr answers s when it is an IP address. client is an address and
// nothing else: a facet mixing addresses with the stray hostname or the
// "UNKNOWN" some daemons write would not group, and a "Block this address"
// verb on it would be wrong.
func sysAddr(s string) string {
	if _, err := netip.ParseAddr(s); err != nil {
		return ""
	}
	return s
}

// sysSetMissing records a value the text carries unless the line already has
// one: a journal entry's structured EXIT_STATUS or UNIT is exact where the
// sentence may be abbreviated or translated.
func sysSetMissing(l *Line, key, value string) {
	if _, ok := l.Attrs[key]; ok {
		return
	}
	l.SetAttr(key, value)
}

// sysSetOrigin records the program and pid of a line whose envelope was read
// off its own text; for a journal line or one the composite handed on, they
// are already in Attrs.
func sysSetOrigin(l *Line, m sysMessage, names sysNames) {
	if !m.own {
		return
	}
	sysSetShared(l, "program", names.of(m.program))
	l.SetAttr("pid", m.pid)
}

// sysNames keeps one copy of each program name a reader has met. SetAttr
// clones every value so a facet tally never pins a whole line; a host has a
// few dozen programs, and cloning the name afresh on each of a syslog's
// millions of lines would be millions of copies of "systemd".
type sysNames map[string]string

// sysNamesCap bounds a reader that meets an unbounded set of tags, which a
// log with junk in the tag column would otherwise make it.
const sysNamesCap = 1024

func (n sysNames) of(name string) string {
	if kept, ok := n[name]; ok {
		return kept
	}
	if len(n) >= sysNamesCap {
		clear(n)
	}
	kept := strings.Clone(name)
	n[kept] = kept
	return kept
}

// sysSetShared records a value that is already a copy of its own, not a
// slice of the line's text, and so needs none of SetAttr's cloning.
func sysSetShared(l *Line, key, value string) {
	if value == "" {
		return
	}
	if l.Attrs == nil {
		l.Attrs = make(map[string]string, 4)
	}
	l.Attrs[key] = value
}
