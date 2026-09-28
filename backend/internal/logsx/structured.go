package logsx

import (
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// What a container actually writes, as opposed to what a log viewer assumes.
//
// Two things arrive on a container's stdout that a plain text reader gets
// wrong, and both of them were visible on the first deployment anybody opened.
//
// The first is terminal control. A build tool with a progress spinner writes
// escape sequences to move the cursor and clear the line, and a reader that
// treats the bytes as text renders "B[2KB[1AB[2KB[G Generated Prisma Client".
// Worse than ugly: the escape bytes are part of the string the level detector
// and the search then run over.
//
// The second is structure. A modern application does not write "ERROR: it
// broke", it writes {"level":"error","msg":"it broke","requestId":"..."} — and
// the level detector, which looks for an English word, was finding the *key*
// rather than the value, or nothing at all. A page whose level chips are wrong
// for every structured logger is a page whose level chips nobody uses.

// StripANSI removes terminal control sequences and resolves carriage returns
// to the frame a terminal would have left on screen.
//
// A progress bar writes its whole animation into one line separated by \r; the
// last frame is what the operator would have seen, and the forty before it are
// the same sentence at different lengths.
func StripANSI(text string) string {
	if !strings.ContainsAny(text, "\x1b\r\b") {
		return text
	}
	// The final frame only. A line ending in \r has nothing after it, so the
	// trailing separators go first and the frame before the last remaining one
	// is what the terminal was left showing.
	text = strings.TrimRight(text, "\r")
	if i := strings.LastIndexByte(text, '\r'); i >= 0 {
		text = text[i+1:]
	}
	var out strings.Builder
	out.Grow(len(text))
	for i := 0; i < len(text); {
		c := text[i]
		if c != 0x1b {
			// A lone backspace is a terminal erasing the character before it,
			// which is how some spinners animate. Honour it rather than
			// drawing a control picture.
			if c == '\b' {
				current := out.String()
				out.Reset()
				if len(current) > 0 {
					out.WriteString(current[:len(current)-1])
				}
				i++
				continue
			}
			out.WriteByte(c)
			i++
			continue
		}
		i++
		if i >= len(text) {
			break
		}
		switch text[i] {
		case '[':
			// CSI: parameters and intermediates, then one final byte in
			// @ through ~. That final byte is what ends the sequence, which is
			// why a fixed-length skip loses the rest of a long colour run.
			i++
			for i < len(text) && (text[i] < '@' || text[i] > '~') {
				i++
			}
			if i < len(text) {
				i++
			}
		case ']':
			// OSC: runs to BEL or to the two-byte string terminator.
			i++
			for i < len(text) {
				if text[i] == 0x07 {
					i++
					break
				}
				if text[i] == 0x1b && i+1 < len(text) && text[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		case 'P', 'X', '^', '_':
			// DCS, SOS, PM and APC all run to the string terminator.
			i++
			for i < len(text) {
				if text[i] == 0x1b && i+1 < len(text) && text[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default:
			// A two-byte escape: skip the one byte that follows.
			i++
		}
	}
	return out.String()
}

// structuredCap bounds what one line contributes. A log line carrying a whole
// request body would otherwise arrive at the browser as a hundred fields.
const (
	structuredCap      = 24
	structuredValueCap = 512
)

// levelKeys, messageKeys and timeKeys are the spellings the common loggers
// use. slog, zap, zerolog, logrus, pino, bunyan and Python's JSON formatters
// between them cover almost everything that reaches a container's stdout, and
// they disagree about all three names.
var (
	levelKeys   = []string{"level", "severity", "lvl", "levelname", "log.level", "@level"}
	messageKeys = []string{"msg", "message", "@message", "event", "short_message"}
	timeKeys    = []string{"time", "ts", "timestamp", "@timestamp", "eventTime", "asctime"}
)

// parseStructured reads a JSON or logfmt log line, and answers false for
// anything that is neither.
func parseStructured(text string) (level, message string, at *time.Time, fields map[string]string, ok bool) {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return parseJSON(trimmed)
	}
	return parseLogfmt(trimmed)
}

// parseJSON reads a JSON log line. It answers false for anything that is not
// one — including a line that merely starts with a brace, because a
// pretty-printed fragment in the middle of a stack trace does too.
func parseJSON(trimmed string) (level, message string, at *time.Time, fields map[string]string, ok bool) {
	if len(trimmed) < 2 || trimmed[len(trimmed)-1] != '}' {
		return "", "", nil, nil, false
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(trimmed), &raw) != nil || len(raw) == 0 {
		return "", "", nil, nil, false
	}
	used := map[string]bool{}
	pick := func(keys []string) (string, bool) {
		for _, key := range keys {
			value, present := raw[key]
			if !present {
				continue
			}
			text, ok := scalar(value)
			if !ok {
				continue
			}
			used[key] = true
			return text, true
		}
		return "", false
	}
	if word, found := pick(levelKeys); found {
		// A numeric level is pino's and bunyan's spelling, and it is a syslog
		// priority in neither: 50 is an error in pino and 3 is one in syslog.
		if n, err := strconv.Atoi(word); err == nil {
			level = levelFromNumber(n)
		} else {
			level = Normalise(word)
		}
	}
	message, _ = pick(messageKeys)
	if stamp, found := pick(timeKeys); found {
		at = structuredTime(stamp)
	}
	// Everything else is context, and context is the reason to log as JSON in
	// the first place: a request id beside a message is what turns one line
	// into a thread you can pull.
	fields = map[string]string{}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		if !used[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(fields) >= structuredCap {
			break
		}
		value, ok := scalar(raw[key])
		if !ok {
			value = compact(raw[key])
		}
		if value == "" {
			continue
		}
		if len(value) > structuredValueCap {
			value = value[:structuredValueCap] + "…"
		}
		fields[key] = value
	}
	if len(fields) == 0 {
		fields = nil
	}
	return level, message, at, fields, level != "" || message != ""
}

// structuredTime reads a time key's value: a timestamp in any spelling the
// line parser knows, or a number of seconds or milliseconds since the epoch.
func structuredTime(stamp string) *time.Time {
	if parsed, good := parseTimestamp(stamp); good {
		return &parsed
	}
	if seconds, err := strconv.ParseFloat(stamp, 64); err == nil && seconds > 0 {
		// pino writes milliseconds since the epoch; a bare seconds value
		// is what Go's own encoders produce.
		if seconds > 1e11 {
			seconds /= 1000
		}
		// The fraction is kept to the microsecond a float of unix seconds
		// can hold: Caddy writes its "ts" this way, and a second is a long
		// time between a request's access entry and the error line that
		// says why it failed.
		parsed := time.UnixMicro(int64(math.Round(seconds * 1e6))).UTC()
		return &parsed
	}
	return nil
}

// logfmtMarkers are the keys that make a run of key=value pairs a log line
// rather than a sentence that happens to contain one. Without them a UFW
// line (IN=eth0 OUT= SRC=…) or systemd's "code=exited, status=1/FAILURE"
// would be read as structure, and would render differently even with no lens.
var logfmtMarkers = []string{"level", "lvl", "msg", "message", "time", "at"}

// parseLogfmt reads a logfmt line — dockerd's `time="…" level=warning
// msg="…"`, go-kit's, Heroku's. It is strict on purpose: the first token must
// be a key=value pair, every token must be one, and one of the marker keys
// must be present. A line that fails any of the three is plain text.
func parseLogfmt(text string) (level, message string, at *time.Time, fields map[string]string, ok bool) {
	if !logfmtStart(text) {
		return "", "", nil, nil, false
	}
	type pair struct{ key, value string }
	pairs := make([]pair, 0, 8)
	for i := 0; i < len(text); {
		if text[i] == ' ' || text[i] == '\t' {
			i++
			continue
		}
		start := i
		for i < len(text) && text[i] > ' ' && text[i] != '=' && text[i] != '"' {
			i++
		}
		if i == start || i >= len(text) || text[i] != '=' {
			return "", "", nil, nil, false
		}
		key := text[start:i]
		i++
		value := ""
		if i < len(text) && text[i] == '"' {
			end := closingQuote(text, i)
			if end < 0 {
				return "", "", nil, nil, false
			}
			unquoted, err := strconv.Unquote(text[i : end+1])
			if err != nil {
				unquoted = text[i+1 : end]
			}
			value = unquoted
			i = end + 1
		} else {
			vs := i
			for i < len(text) && text[i] != ' ' && text[i] != '\t' {
				i++
			}
			value = text[vs:i]
		}
		pairs = append(pairs, pair{key, value})
	}
	marked := false
	for _, p := range pairs {
		if slices.Contains(logfmtMarkers, p.key) {
			marked = true
			break
		}
	}
	if !marked {
		return "", "", nil, nil, false
	}
	fields = map[string]string{}
	for _, p := range pairs {
		switch {
		case level == "" && (p.key == "level" || p.key == "lvl" || p.key == "at") && Normalise(p.value) != "":
			// Heroku writes the level as at=error; an at= that is not a level
			// is an ordinary field.
			level = Normalise(p.value)
		case message == "" && (p.key == "msg" || p.key == "message"):
			message = p.value
		case at == nil && (p.key == "time" || p.key == "ts" || p.key == "timestamp") && structuredTime(p.value) != nil:
			at = structuredTime(p.value)
		default:
			if p.value == "" || len(fields) >= structuredCap {
				continue
			}
			value := p.value
			if len(value) > structuredValueCap {
				value = value[:structuredValueCap] + "…"
			}
			fields[p.key] = value
		}
	}
	if len(fields) == 0 {
		fields = nil
	}
	return level, message, at, fields, true
}

// logfmtStart is the cheap first test, run on every plain line: a key in the
// shape ^[A-Za-z_][A-Za-z0-9_.-]* followed by "=".
func logfmtStart(text string) bool {
	if text == "" {
		return false
	}
	if c := text[0]; !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
		return false
	}
	for i := 1; i < len(text); i++ {
		switch c := text[i]; {
		case c == '=':
			return true
		case tokenByte(c), c == '.', c == '-':
		default:
			return false
		}
	}
	return false
}

// scalar renders a JSON value as the string a column would hold, and refuses
// objects and arrays so a nested payload does not become an unreadable cell.
func scalar(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	switch raw[0] {
	case '"':
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return "", false
		}
		return text, true
	case 't', 'f', 'n':
		return string(raw), true
	case '{', '[':
		return "", false
	}
	return string(raw), true
}

func compact(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	out, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(out)
}

// levelFromNumber maps the numeric scales the JSON loggers use. pino and
// bunyan count upward in tens from 10 (trace) to 60 (fatal); anything smaller
// is a syslog priority, which counts the other way.
func levelFromNumber(n int) string {
	if n >= 10 {
		switch {
		case n >= 60:
			return "critical"
		case n >= 50:
			return "error"
		case n >= 40:
			return "warn"
		case n >= 30:
			return "info"
		default:
			return "debug"
		}
	}
	if n >= 0 && n <= 7 {
		return LevelFromPriority(n)
	}
	return ""
}
