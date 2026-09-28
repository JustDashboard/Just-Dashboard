package logsx

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Helpers shared by the app, pm2, packages and certbot lenses. Each is named
// for this group so it cannot collide with another lens file's helper of the
// same idea.

// appParseStamp reads the timestamp a line starts with, in the spellings
// application loggers and package tools actually write: an ISO date or Go's
// slashed one, a T or one or two spaces (apt pads its Start-Date with two),
// seconds optional, a fraction after a dot or Python's comma, and an optional
// zone — Z, +hh:mm, +hhmm or +hh, directly or after one space (PM2's
// "YYYY-MM-DD HH:mm:ss Z"). It answers the instant and how many bytes it
// read.
//
// It is hand-scanned because it runs on every line of a Python or Spring
// log, and time.Parse would be tried and refused on most of them. A stamp
// with no zone is the host's local time: that is what logging.Formatter,
// dpkg and apt write, and what netsec already assumes for the same files.
func appParseStamp(s string) (time.Time, int, bool) {
	if len(s) < 16 || !appDigits(s[0:4]) || (s[4] != '-' && s[4] != '/') || !appDigits(s[5:7]) || s[7] != s[4] || !appDigits(s[8:10]) {
		return time.Time{}, 0, false
	}
	i := 10
	switch {
	case s[i] == 'T':
		i++
	case s[i] == ' ':
		i++
		if i < len(s) && s[i] == ' ' {
			i++
		}
	default:
		return time.Time{}, 0, false
	}
	if len(s) < i+5 || !appDigits(s[i:i+2]) || s[i+2] != ':' || !appDigits(s[i+3:i+5]) {
		return time.Time{}, 0, false
	}
	year, _ := strconv.Atoi(s[0:4])
	month, _ := strconv.Atoi(s[5:7])
	day, _ := strconv.Atoi(s[8:10])
	hour, _ := strconv.Atoi(s[i : i+2])
	minute, _ := strconv.Atoi(s[i+3 : i+5])
	i += 5
	second, nanos := 0, 0
	if len(s) >= i+3 && s[i] == ':' && appDigits(s[i+1:i+3]) {
		second, _ = strconv.Atoi(s[i+1 : i+3])
		i += 3
		if len(s) > i+1 && (s[i] == '.' || s[i] == ',') && s[i+1] >= '0' && s[i+1] <= '9' {
			j := i + 1
			scale := 100000000
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				if scale > 0 {
					nanos += int(s[j]-'0') * scale
					scale /= 10
				}
				j++
			}
			i = j
		}
	}
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 || second > 60 {
		return time.Time{}, 0, false
	}
	if offset, n, ok := appZone(s[i:]); ok {
		t := time.Date(year, time.Month(month), day, hour, minute, second, nanos, time.UTC).Add(-offset)
		return t, i + n, true
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, nanos, time.Local).UTC(), i, true
}

// appZone reads a zone suffix: Z, or a sign, two hour digits and optionally
// two minute digits with or without a colon, directly or after one space.
func appZone(s string) (time.Duration, int, bool) {
	lead := 0
	if len(s) > 1 && s[0] == ' ' && (s[1] == '+' || s[1] == '-') {
		lead = 1
	}
	s = s[lead:]
	if s == "" {
		return 0, 0, false
	}
	if s[0] == 'Z' {
		return 0, lead + 1, true
	}
	if (s[0] != '+' && s[0] != '-') || len(s) < 3 || !appDigits(s[1:3]) {
		return 0, 0, false
	}
	hours, _ := strconv.Atoi(s[1:3])
	n, minutes := 3, 0
	switch {
	case len(s) >= 6 && s[3] == ':' && appDigits(s[4:6]):
		minutes, _ = strconv.Atoi(s[4:6])
		n = 6
	case len(s) >= 5 && appDigits(s[3:5]):
		minutes, _ = strconv.Atoi(s[3:5])
		n = 5
	}
	offset := time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute
	if s[0] == '-' {
		offset = -offset
	}
	return offset, lead + n, true
}

func appDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// appParseLocal parses a stamp with a layout that carries no zone, in the
// host's zone, for the formats appParseStamp does not cover: Django's
// "27/Sep/2026 10:00:00", PHP-FPM's "27-Sep-2026 10:00:00", gin's slashed
// date with a dash before the time.
func appParseLocal(layout, value string) (*time.Time, bool) {
	t, err := time.ParseInLocation(layout, value, time.Local)
	if err != nil {
		return nil, false
	}
	t = t.UTC()
	return &t, true
}

// appStatusClass is the status family a request is read by.
func appStatusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	}
	return "1xx"
}

// appDurationMS turns a number and the unit written after it into
// milliseconds. Frameworks disagree about the unit — Next.js switches to
// seconds past one, Phoenix and gin print microseconds for a fast answer —
// so the unit is read rather than assumed.
func appDurationMS(number, unit string) (float64, bool) {
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	switch unit {
	case "ms":
		return value, true
	case "s":
		return value * 1000, true
	case "µs", "μs", "us":
		return value / 1000, true
	case "ns":
		return value / 1e6, true
	case "m":
		return value * 60000, true
	}
	return 0, false
}

// appGoDurationMS reads a Go duration string ("5s", "1.234ms", "150.5µs",
// "2m3s") as milliseconds; gin and echo print latency this way.
func appGoDurationMS(value string) (float64, bool) {
	d, err := time.ParseDuration(strings.Replace(value, "μ", "µ", 1))
	if err != nil || d < 0 {
		return 0, false
	}
	return float64(d) / float64(time.Millisecond), true
}

// appCap bounds a value kept as an attr. A facet holds every distinct value
// it sees, and an exception message that embeds a request body would
// otherwise make each occurrence its own megabyte-long row.
func appCap(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// appFirstLine is a value's first line, trimmed: what an exception's message
// is grouped by.
func appFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// appIndented says a line starts with whitespace, which is how every runtime
// prints the body of a stack trace.
func appIndented(s string) bool {
	return s != "" && (s[0] == ' ' || s[0] == '\t')
}
