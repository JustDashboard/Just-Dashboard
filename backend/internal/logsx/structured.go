package logsx

import (
	"encoding/json"
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

// parseStructured reads a JSON log line. It answers false for anything that is
// not one — including a line that merely starts with a brace, because a
// pretty-printed fragment in the middle of a stack trace does too.
func parseStructured(text string) (level, message string, at *time.Time, fields map[string]string, ok bool) {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
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
		if parsed, good := parseTimestamp(stamp); good {
			at = &parsed
		} else if seconds, err := strconv.ParseFloat(stamp, 64); err == nil && seconds > 0 {
			// pino writes milliseconds since the epoch; a bare seconds value
			// is what Go's own encoders produce.
			if seconds > 1e11 {
				seconds /= 1000
			}
			parsed := time.Unix(int64(seconds), 0).UTC()
			at = &parsed
		}
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
