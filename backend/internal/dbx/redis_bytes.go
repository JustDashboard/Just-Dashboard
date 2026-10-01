package dbx

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A Redis string is bytes and a JSON string is Unicode. Sending one as the
// other is what turned a serialised session, a MessagePack blob or a key some
// Java client wrote into U+FFFD on the way out — and then, on the next save,
// into different bytes on the way back in. The types here are how a Redis
// string crosses that boundary without being changed by it.

// RedisBytes is one Redis string — a key name, a value, a member, a field.
//
// It marshals as a plain JSON string whenever the bytes are valid UTF-8, which
// is nearly always, so ordinary text looks exactly as it did before. Bytes
// that are not valid UTF-8 travel as {"base64":"…"} instead, and either form
// is accepted on the way in. Nothing is ever replaced or guessed at.
type RedisBytes string

func (b RedisBytes) MarshalJSON() ([]byte, error) {
	if utf8.ValidString(string(b)) {
		return json.Marshal(string(b))
	}
	return json.Marshal(struct {
		Base64 string `json:"base64"`
	}{base64.StdEncoding.EncodeToString([]byte(b))})
}

func (b *RedisBytes) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*b = RedisBytes(s)
		return nil
	}
	// Decoded strictly: the request decoder refuses unknown fields everywhere
	// else, and a custom unmarshaler is otherwise the one place they would
	// slip through.
	var wrapped struct {
		Base64 *string `json:"base64"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wrapped); err != nil || wrapped.Base64 == nil {
		return fmt.Errorf(`expected a string or {"base64": "…"}`)
	}
	raw, err := base64.StdEncoding.DecodeString(*wrapped.Base64)
	if err != nil {
		return fmt.Errorf("base64 is not valid: %v", err)
	}
	*b = RedisBytes(raw)
	return nil
}

// Binary reports whether the bytes cannot be shown as text.
func (b RedisBytes) Binary() bool { return !utf8.ValidString(string(b)) }

// Display renders the bytes for a sentence — an error message, a log line —
// the way redis-cli prints them: text as it is, everything else as \xNN.
func (b RedisBytes) Display() string {
	if !b.Binary() {
		return string(b)
	}
	return redisEscape(string(b))
}

// redisEscape writes a string in redis-cli's quoting, without the quotes.
func redisEscape(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&out, `\x%02x`, s[i])
		case r == '\\':
			out.WriteString(`\\`)
		case r == '"':
			out.WriteString(`\"`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&out, `\x%02x`, r)
		default:
			out.WriteString(s[i : i+size])
		}
		i += size
	}
	return out.String()
}

func redisBytesList(in []string) []RedisBytes {
	out := make([]RedisBytes, len(in))
	for i, s := range in {
		out[i] = RedisBytes(s)
	}
	return out
}

func redisStrings(in []RedisBytes) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = string(s)
	}
	return out
}

// RedisScore is a sorted-set score. Redis allows +inf and -inf there and JSON
// has no way to write either, so those two travel as the strings "inf" and
// "-inf" and every finite score as the number it is.
type RedisScore float64

func (s RedisScore) MarshalJSON() ([]byte, error) {
	f := float64(s)
	switch {
	case math.IsInf(f, 1):
		return []byte(`"inf"`), nil
	case math.IsInf(f, -1):
		return []byte(`"-inf"`), nil
	case math.IsNaN(f):
		return []byte(`"nan"`), nil
	}
	return []byte(strconv.FormatFloat(f, 'g', -1, 64)), nil
}

func (s *RedisScore) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	text := string(data)
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
	}
	f, err := parseRedisScore(text)
	if err != nil {
		return err
	}
	*s = RedisScore(f)
	return nil
}

// parseRedisScore reads a score the way ZADD does: a float, or inf with an
// optional sign. NaN is refused, as the server refuses it.
func parseRedisScore(text string) (float64, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "inf", "+inf", "infinity", "+infinity":
		return math.Inf(1), nil
	case "-inf", "-infinity":
		return math.Inf(-1), nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(f) {
		return 0, fmt.Errorf("a numeric score is required for a sorted set member")
	}
	return f, nil
}

// formatRedisScore writes a score as an argument Redis accepts.
func formatRedisScore(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "+inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// redisGlobEscape quotes a literal so it can stand inside a MATCH pattern:
// a namespace called "cache[v2]:" must match itself, not a character class.
func redisGlobEscape(literal string) string {
	var out strings.Builder
	for i := 0; i < len(literal); i++ {
		switch literal[i] {
		case '*', '?', '[', ']', '\\':
			out.WriteByte('\\')
		}
		out.WriteByte(literal[i])
	}
	return out.String()
}
