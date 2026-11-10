package procs

import (
	"regexp"
	"strconv"
	"strings"
)

// SplitEnvironment reads an environment list the way systemd writes one: the
// `Environment` property of `systemctl show` and the value of an
// `Environment=` line in a unit file. Entries are separated by whitespace; an
// entry holding a space, a quote or a control character is double-quoted with
// C-style backslash escapes (`A=1 "B=has space" "C=x\"q"`), and a unit file may
// quote only part of one (`B="has space"`) or use single quotes.
//
// The reading is lenient rather than strict: an unterminated quote runs to the
// end of the value and an unknown escape keeps its character. The result is
// used to hide credentials, and a strict reading that gave up on a malformed
// line would have to show it whole.
func SplitEnvironment(value string) []string {
	var out []string
	var word strings.Builder
	inWord := false
	var quote byte
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case quote == 0 && isSystemdSpace(c):
			if inWord {
				out = append(out, word.String())
				word.Reset()
				inWord = false
			}
		case quote == 0 && (c == '"' || c == '\''):
			quote, inWord = c, true
		case quote != 0 && c == quote:
			quote = 0
		case c == '\\' && i+1 < len(value):
			i += unescapeOne(value[i+1:], &word)
			inWord = true
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		out = append(out, word.String())
	}
	return out
}

func isSystemdSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// unescapeOne writes the character the escape at the start of rest (the text
// after a backslash) stands for, and returns how many bytes of rest it used.
func unescapeOne(rest string, out *strings.Builder) int {
	switch c := rest[0]; c {
	case 'a':
		out.WriteByte('\a')
	case 'b':
		out.WriteByte('\b')
	case 'f':
		out.WriteByte('\f')
	case 'n':
		out.WriteByte('\n')
	case 'r':
		out.WriteByte('\r')
	case 't':
		out.WriteByte('\t')
	case 'v':
		out.WriteByte('\v')
	case 's':
		out.WriteByte(' ')
	case 'x':
		if len(rest) >= 3 {
			if n, err := strconv.ParseUint(rest[1:3], 16, 8); err == nil {
				out.WriteByte(byte(n))
				return 3
			}
		}
		out.WriteByte(c)
	case 'u', 'U':
		width := 4
		if c == 'U' {
			width = 8
		}
		if len(rest) > width {
			if n, err := strconv.ParseUint(rest[1:1+width], 16, 32); err == nil {
				out.WriteRune(rune(n))
				return 1 + width
			}
		}
		out.WriteByte(c)
	case '0', '1', '2', '3', '4', '5', '6', '7':
		if len(rest) >= 3 {
			if n, err := strconv.ParseUint(rest[:3], 8, 8); err == nil {
				out.WriteByte(byte(n))
				return 3
			}
		}
		out.WriteByte(c)
	default:
		// \\, \", \', \$, \` and anything unknown: the character itself.
		out.WriteByte(c)
	}
	return 1
}

// JoinEnvironment writes entries back in the form SplitEnvironment reads,
// quoting an entry the way systemctl does when it holds anything a shell
// would read specially, so a masked list keeps the property's format.
func JoinEnvironment(entries []string) string {
	quoted := make([]string, len(entries))
	for i, entry := range entries {
		quoted[i] = quoteEnvironmentEntry(entry)
	}
	return strings.Join(quoted, " ")
}

// shellNeedsQuotes is systemd's SHELL_NEED_QUOTES: the characters that make
// `systemctl show` quote an entry.
const shellNeedsQuotes = "\"\\`$*?['()<>|&;!"

func quoteEnvironmentEntry(entry string) string {
	plain := entry != ""
	for i := 0; i < len(entry) && plain; i++ {
		c := entry[i]
		plain = c > ' ' && c < 127 && !strings.ContainsRune(shellNeedsQuotes, rune(c))
	}
	if plain {
		return entry
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(entry); i++ {
		switch c := entry[i]; c {
		case '"', '\\', '`', '$':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\a':
			b.WriteString(`\a`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\v':
			b.WriteString(`\v`)
		default:
			if c < ' ' || c == 127 {
				b.WriteByte('\\')
				b.WriteString(strconv.FormatUint(uint64(c)>>6&7, 8))
				b.WriteString(strconv.FormatUint(uint64(c)>>3&7, 8))
				b.WriteString(strconv.FormatUint(uint64(c)&7, 8))
				continue
			}
			// Bytes of a UTF-8 character are kept as they are, as systemd keeps them.
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// MaskEnvironment replaces the value of every entry of an environment list
// whose name secret reports with placeholder. A list with nothing to hide is
// returned exactly as given; one with something is rewritten in the same
// format, so a reader of the property cannot tell masking from the original
// by its shape.
func MaskEnvironment(value string, secret func(name string) bool, placeholder string) string {
	entries, masked := maskEntries(SplitEnvironment(value), secret, placeholder)
	if !masked {
		return value
	}
	return JoinEnvironment(entries)
}

func maskEntries(entries []string, secret func(string) bool, placeholder string) ([]string, bool) {
	masked := false
	for i, entry := range entries {
		if name, _, ok := strings.Cut(entry, "="); ok && secret(name) {
			entries[i] = name + "=" + placeholder
			masked = true
		}
	}
	return entries, masked
}

// unitSecretLine is an assignment in a unit file whose value can carry a
// credential: Environment= holds variables, and SetCredential= holds the
// credential itself in plain text. EnvironmentFile=, PassEnvironment= and
// LoadCredential= name files and variables, not values, and are left alone.
// systemd allows whitespace around the "=".
var unitSecretLine = regexp.MustCompile(`^([ \t]*)(Environment|SetCredential)[ \t]*=[ \t]*(.*)$`)

// MaskUnitFileSecrets hides credentials in a unit file's text: in every
// Environment= line, the value of each variable whose name secret reports;
// in every SetCredential= line, the credential, whatever its name. A line
// continued with a trailing backslash is read whole, and when something in it
// is hidden it is written as one line, with the lines it continued onto left
// blank so the file's line numbers still match the host's. Lines with nothing
// to hide are kept exactly.
func MaskUnitFileSecrets(content string, secret func(name string) bool, placeholder string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		m := unitSecretLine.FindStringSubmatch(lines[i])
		if m == nil {
			out = append(out, lines[i])
			continue
		}
		indent, key, value := m[1], m[2], m[3]
		// Gather the continuation lines. systemd skips comment lines inside
		// a continued value and replaces each joining backslash with a space.
		end := i
		var comments []int
		for continued(value) && end+1 < len(lines) {
			end++
			next := lines[end]
			if trimmed := strings.TrimLeft(next, " \t"); strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
				comments = append(comments, end)
				continue
			}
			value = value[:len(value)-1] + " " + next
		}
		var rewritten string
		switch key {
		case "SetCredential":
			// ID:VALUE. Without the colon systemd refuses the line, but what
			// is there may still be the credential.
			rewritten = indent + key + "=" + placeholder
			if id, _, ok := strings.Cut(value, ":"); ok {
				rewritten = indent + key + "=" + id + ":" + placeholder
			}
		default:
			entries, masked := maskEntries(SplitEnvironment(value), secret, placeholder)
			if !masked {
				out = append(out, lines[i:end+1]...)
				i = end
				continue
			}
			rewritten = indent + key + "=" + JoinEnvironment(entries)
		}
		out = append(out, rewritten)
		for j := i + 1; j <= end; j++ {
			if len(comments) > 0 && comments[0] == j {
				out = append(out, lines[j])
				comments = comments[1:]
				continue
			}
			out = append(out, "")
		}
		i = end
	}
	return strings.Join(out, "\n")
}

// continued reports whether a unit-file line goes on to the next: it ends in
// a backslash that is not itself escaped.
func continued(line string) bool {
	trailing := 0
	for i := len(line) - 1; i >= 0 && line[i] == '\\'; i-- {
		trailing++
	}
	return trailing%2 == 1
}
