package logsx

import (
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// What the database lenses share: reading a stamp, an address and a duration
// out of text that every one of the engines spells a little differently.
//
// The stamps are read field by field rather than with time.Parse. A lens runs
// on every line of a search over gigabytes of rotated logs, and one
// time.Parse costs more than everything else a lens does to a line put
// together; the formats here are fixed-width, so the digits can simply be
// read and handed to time.Date.

// dbNumber reads s as an unsigned decimal, all of it.
func dbNumber(s string) (int, bool) {
	if s == "" || len(s) > 9 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// dbDigits answers how many ASCII digits s starts with.
func dbDigits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

// dbDate reads "2006-01-02" with sep in place of the hyphens: ClickHouse
// writes dots.
func dbDate(s string, sep byte) (y, m, d int, ok bool) {
	if len(s) < 10 || s[4] != sep || s[7] != sep {
		return 0, 0, 0, false
	}
	y, okY := dbNumber(s[0:4])
	m, okM := dbNumber(s[5:7])
	d, okD := dbNumber(s[8:10])
	return y, m, d, okY && okM && okD && m >= 1 && m <= 12 && d >= 1 && d <= 31
}

// dbClock reads "15:04:05", or "5:04:05" — MariaDB pads the hour with a space
// rather than a zero, which is why the generic parser loses every MariaDB
// line written before ten in the morning — and answers how many bytes it
// read.
func dbClock(s string) (h, m, sec, n int, ok bool) {
	hl := dbDigits(s)
	if hl < 1 || hl > 2 || len(s) < hl+6 || s[hl] != ':' || s[hl+3] != ':' {
		return 0, 0, 0, 0, false
	}
	h, _ = dbNumber(s[:hl])
	m, okM := dbNumber(s[hl+1 : hl+3])
	sec, okS := dbNumber(s[hl+4 : hl+6])
	if !okM || !okS || h > 23 || m > 59 || sec > 60 {
		return 0, 0, 0, 0, false
	}
	return h, m, sec, hl + 6, true
}

// dbFraction reads the fractional seconds after a dot, when there is one, and
// answers the nanoseconds and how many bytes it read, the dot included.
func dbFraction(s string) (nsec, n int) {
	if s == "" || s[0] != '.' {
		return 0, 0
	}
	digits := dbDigits(s[1:])
	if digits == 0 {
		return 0, 0
	}
	scale := 100_000_000
	for i := 1; i <= digits && i <= 9; i++ {
		nsec += int(s[i]-'0') * scale
		scale /= 10
	}
	return nsec, digits + 1
}

// dbOffset reads a numeric zone: "+03", "-0530", "+05:30".
func dbOffset(s string) (int, bool) {
	if len(s) < 3 || (s[0] != '+' && s[0] != '-') {
		return 0, false
	}
	digits := strings.ReplaceAll(s[1:], ":", "")
	if len(digits) != 2 && len(digits) != 4 {
		return 0, false
	}
	h, ok := dbNumber(digits[:2])
	if !ok {
		return 0, false
	}
	m := 0
	if len(digits) == 4 {
		if m, ok = dbNumber(digits[2:]); !ok {
			return 0, false
		}
	}
	off := h*3600 + m*60
	if s[0] == '-' {
		off = -off
	}
	return off, true
}

// dbStamp places a stamp's fields in a zone and answers it in UTC, the form
// every other reader of Line.Timestamp produces. A naive stamp is placed in
// time.Local: a database writes its local time, and the host's zone is the
// one the operator's other logs — and netsec's readings of them — use.
func dbStamp(y, mo, d, h, mi, sec, nsec int, loc *time.Location) *time.Time {
	t := time.Date(y, time.Month(mo), d, h, mi, sec, nsec, loc).UTC()
	return &t
}

// dbMonths is the English month abbreviations Redis's strftime writes.
var dbMonths = map[string]time.Month{
	"Jan": time.January, "Feb": time.February, "Mar": time.March, "Apr": time.April,
	"May": time.May, "Jun": time.June, "Jul": time.July, "Aug": time.August,
	"Sep": time.September, "Oct": time.October, "Nov": time.November, "Dec": time.December,
}

// dbIP answers host when it is an address, unwrapping "[::1]" and an IPv4
// address mapped into IPv6. `client` is what the firewall and fail2ban pages
// take, and they take addresses: a host name, "localhost" or Postgres's
// "[local]" would be a value nothing downstream can act on.
func dbIP(host string) string {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	if addr.Is4In6() {
		return addr.Unmap().String()
	}
	return host
}

// dbPeer records "10.0.1.9:32988" or "[::1]:5432" as client and port.
func dbPeer(l *Line, hostport string) {
	i := strings.LastIndexByte(hostport, ':')
	if i <= 0 {
		l.SetAttr("client", dbIP(hostport))
		return
	}
	host, port := hostport[:i], hostport[i+1:]
	// A bare IPv6 address has colons of its own and no port.
	if strings.IndexByte(host, ':') >= 0 && !strings.HasPrefix(host, "[") {
		l.SetAttr("client", dbIP(hostport))
		return
	}
	ip := dbIP(host)
	if ip == "" {
		return
	}
	l.SetAttr("client", ip)
	if _, ok := dbNumber(port); ok {
		l.SetAttr("port", port)
	}
}

// dbMillis records a duration as decimal milliseconds, scale being how many
// milliseconds the written unit is. The rounding is to the microsecond: a
// ClickHouse "0.066 sec" is 66 ms, not 66.00000000000001.
func dbMillis(l *Line, key, value string, scale float64) {
	v, err := strconv.ParseFloat(value, 64)
	if err != nil || v < 0 {
		return
	}
	l.SetAttrNumber(key, math.Round(v*scale*1000)/1000)
}

// dbSetWord records a value the lens spelled itself — a constant, or a string
// decoded into memory of its own — which shares no bytes with the line.
// SetAttr copies every value so that a facet tally never pins a long line;
// these have nothing to pin, and not copying them is an allocation fewer on
// every line of a log.
func dbSetWord(l *Line, key, value string) {
	if value == "" {
		return
	}
	if l.Attrs == nil {
		l.Attrs = make(map[string]string, 4)
	}
	l.Attrs[key] = value
}

// dbNumberAttr records a count or a size in the one spelling SetAttrNumber
// gives every number, so "0.000" and "0" are one facet value.
func dbNumberAttr(l *Line, key, value string) {
	if v, err := strconv.ParseFloat(value, 64); err == nil {
		l.SetAttrNumber(key, v)
	}
}

// dbBetween answers the text after the first start and before the next end,
// or "" when either is missing.
func dbBetween(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	s = s[i+len(start):]
	j := strings.Index(s, end)
	if j < 0 {
		return ""
	}
	return s[:j]
}

// dbWord answers the text after key up to the next space, or to the end.
func dbWord(s, key string) string {
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	s = s[i+len(key):]
	if j := strings.IndexByte(s, ' '); j >= 0 {
		return s[:j]
	}
	return s
}
