package dbx

import (
	"regexp"
	"strings"
)

// Reading and writing the schema's own name in definition text.
//
// A default, a view and a routine spell the schema they were read from out
// (`"alpha".mood`, `FROM alpha.orders`), and the same text read from another
// schema must not be called different for that, nor written to a third with
// the wrong one in it. The name is only ever taken or changed where it is an
// identifier: a quoted text is somebody's data (`'noreply@shop.example'` is
// not a mention of a schema called shop) and a comment is somebody's prose.

// mapQualifier finds `old.` where it qualifies a name, in the spellings the
// engines print it in — bare, double-quoted, backtick-quoted and bracketed —
// and removes it (drop) or writes `replacement.` in its place. backslash says
// the engine reads a backslash in a quoted text as an escape (MySQL).
func mapQualifier(s, old, replacement string, drop, backslash bool) string {
	if old == "" {
		return s
	}
	bare := old + "."
	var b strings.Builder
	n := len(s)
	for i := 0; i < n; {
		c := s[i]
		switch {
		case c == '-' && i+1 < n && s[i+1] == '-':
			end := strings.IndexByte(s[i:], '\n')
			if end < 0 {
				end = n - i
			}
			b.WriteString(s[i : i+end])
			i += end
		case c == '/' && i+1 < n && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				end = n - i - 2
			} else {
				end += 2
			}
			b.WriteString(s[i : i+2+end])
			i += 2 + end
		case c == '\'':
			end, closed := endOfQuoted(s, i, '\'', backslash)
			// A text that names a relation to the engine — nextval('alpha.seq'),
			// 'alpha.t'::regclass — is a reference, and moves with the schema.
			if closed && namesARelation(s, i, end) {
				b.WriteString("'" + mapQualifier(s[i+1:end-1], old, replacement, drop, backslash) + "'")
			} else {
				b.WriteString(s[i:end])
			}
			i = end
		case c == '"' || c == '`' || c == '[':
			closer := c
			if c == '[' {
				closer = ']'
			}
			end, closed := endOfQuoted(s, i, closer, false)
			if closed && s[i+1:end-1] == old && end < n && s[end] == '.' {
				if !drop {
					b.WriteString(string(c) + replacement + string(closer) + ".")
				}
				i = end + 1
			} else {
				b.WriteString(s[i:end])
				i = end
			}
		case strings.HasPrefix(s[i:], bare) && (i == 0 || (!isIdentByte(s[i-1]) && s[i-1] != '.')):
			if !drop {
				b.WriteString(replacement + ".")
			}
			i += len(bare)
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// endOfQuoted returns the index after the quote that closes the one at s[i],
// where a doubled closer is a closer in the text, and whether it was closed.
func endOfQuoted(s string, i int, closer byte, backslash bool) (int, bool) {
	for k := i + 1; k < len(s); k++ {
		switch {
		case backslash && s[k] == '\\':
			k++
		case s[k] == closer && k+1 < len(s) && s[k+1] == closer:
			k++
		case s[k] == closer:
			return k + 1, true
		}
	}
	return len(s), false
}

var (
	regclassCast = regexp.MustCompile(`(?i)^\s*::\s*regclass\b`)
	relationCall = regexp.MustCompile(`(?i)\b(nextval|currval|setval|to_regclass|regclass)\s*\(\s*$`)
)

// namesARelation reports whether the quoted text at s[start:end] is handed to
// the engine as the name of a relation.
func namesARelation(s string, start, end int) bool {
	return regclassCast.MatchString(s[end:]) || relationCall.MatchString(s[:start])
}

// stripSchemaName takes `name.` out of text where it qualifies a name.
func stripSchemaName(s, name string, backslash bool) string {
	return mapQualifier(s, name, "", true, backslash)
}

// rebaseSchemaName writes `new.` where the text has `old.`.
func rebaseSchemaName(s, old, new string, backslash bool) string {
	return mapQualifier(s, old, new, false, backslash)
}
