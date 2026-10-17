package dbx

import "strings"

// PostgreSQL's statement text, as a shape.
//
// pg_stat_statements writes $1, $2… over the constants of a statement it
// plans — SELECT, INSERT, UPDATE, DELETE, MERGE — and keeps every other
// statement as it was sent while pg_stat_statements.track_utility is on, which
// is its default. CREATE ROLE … PASSWORD '…' is one of those, and so are a SET,
// a COMMENT, a column's DEFAULT and a DO body, each with its value in it. The
// statement list is on the read surface, so the text is shaped again here
// before it leaves this package, by the rule every engine is held to: every
// literal becomes a ?. statementShape cannot do it — it reads $1 as SQL
// Server's money literal, and the $n markers are what the Statements panel
// asks a value for — so this pass reads the text by PostgreSQL's own lexical
// rules: dollar quoting, E'' and U&'' strings, bit and hex strings, block
// comments that nest, and numbers in every base the scanner takes.
//
// Two values need no quotes to be values, and in a statement the extension
// keeps as it came both are masked whatever they are written as: what follows
// a role's PASSWORD, and a setting's value in SET, ALTER … SET and a
// function's SET clause. SET app.token = abc123 sets a value as surely as
// SET app.token = 'abc123' does.
//
// Whether a backslash escapes inside a plain '…' string depends on
// standard_conforming_strings in the session that sent it, which the text does
// not record: 'it\'s secret' is one string with the setting off, and a string
// followed by the words s secret with it on. So the text is read both ways and
// whatever is a value in either reading is masked. The wrong reading can only
// hide more of a statement; it never shows a value.

// pgStatementShape replaces every value in PostgreSQL statement text with a ?,
// and keeps the $n markers the extension wrote where it took one out itself.
func pgStatementShape(text string) string {
	masked := make([]bool, len(text))
	for _, backslash := range []bool{false, true} {
		for _, t := range pgValues(text, pgTokens(text, backslash)) {
			for i := t.start; i < t.end; i++ {
				masked[i] = true
			}
		}
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		switch {
		case !masked[i]:
			b.WriteByte(text[i])
		case i == 0 || !masked[i-1]:
			b.WriteByte('?')
		}
	}
	return b.String()
}

type pgTokenKind uint8

const (
	pgBlank      pgTokenKind = iota // whitespace, or a comment
	pgKeyword                       // a keyword, or a name written unquoted
	pgQuotedName                    // a quoted name: "…" or U&"…"
	pgConstant                      // a string of any kind, or a number
	pgMarker                        // $1, $2…: where the extension took a value out
	pgPunct                         // one byte of an operator or of punctuation
)

// pgToken is one token of statement text, by where it stands in the text.
type pgToken struct {
	kind       pgTokenKind
	start, end int
}

// pgTokens splits statement text the way PostgreSQL's scanner does, as far as
// telling a value from the rest needs. backslash is whether a backslash
// escapes the next character in a plain '…' string, as it does with
// standard_conforming_strings off. Text the server cut short ends inside its
// last token, and that token runs to the end.
func pgTokens(text string, backslash bool) []pgToken {
	n := len(text)
	// The scanner counts every byte of a multibyte character as a letter.
	letter := func(c byte) bool { return c == '_' || c >= 0x80 || (c|0x20 >= 'a' && c|0x20 <= 'z') }
	digit := func(c byte) bool { return c >= '0' && c <= '9' }
	space := func(c byte) bool { return strings.IndexByte(" \t\n\r\f\v", c) >= 0 }
	// quoted returns where a string or a name opened at i ends: past its
	// closing quote, a doubled quote being one of its characters, and with
	// escapes a backslash taking the byte after it.
	quoted := func(i int, quote byte, escapes bool) int {
		for j := i + 1; j < n; j++ {
			switch {
			case escapes && text[j] == '\\':
				j++
			case text[j] != quote:
			case j+1 < n && text[j+1] == quote:
				j++
			default:
				return j + 1
			}
		}
		return n
	}
	var out []pgToken
	for i := 0; i < n; {
		start, kind, c := i, pgPunct, text[i]
		switch {
		case space(c):
			for i < n && space(text[i]) {
				i++
			}
			kind = pgBlank
		case strings.HasPrefix(text[i:], "--"):
			if end := strings.IndexByte(text[i:], '\n'); end >= 0 {
				i += end
			} else {
				i = n
			}
			kind = pgBlank
		case strings.HasPrefix(text[i:], "/*"):
			// A */ closes the innermost comment only: what reads as code after
			// it may still be inside the outer one, and a quote there opens
			// nothing.
			for depth := 0; i < n; {
				if strings.HasPrefix(text[i:], "/*") {
					depth, i = depth+1, i+2
				} else if strings.HasPrefix(text[i:], "*/") {
					if depth, i = depth-1, i+2; depth == 0 {
						break
					}
				} else {
					i++
				}
			}
			kind = pgBlank
		case c == '\'':
			i, kind = quoted(i, '\'', backslash), pgConstant
		case c == '"':
			i, kind = quoted(i, '"', false), pgQuotedName
		case c == '$' && i+1 < n && digit(text[i+1]):
			for i++; i < n && digit(text[i]); i++ {
			}
			kind = pgMarker
		case c == '$':
			// $$ or $tag$ opens a string that runs to the same delimiter, and
			// nothing inside it is an escape.
			j := i + 1
			for j < n && (letter(text[j]) || digit(text[j])) {
				j++
			}
			if j == n || text[j] != '$' {
				i++
				break
			}
			tag := text[i : j+1]
			if end := strings.Index(text[j+1:], tag); end >= 0 {
				i = j + 1 + end + len(tag)
			} else {
				i = n
			}
			kind = pgConstant
		case digit(c) || (c == '.' && i+1 < n && digit(text[i+1])):
			i, kind = pgNumberEnd(text, i), pgConstant
		case letter(c):
			switch {
			case i+1 < n && text[i+1] == '\'' && strings.IndexByte("eEnNbBxX", c) >= 0:
				// The letter is part of the string it opens: E'…' takes
				// backslash escapes, N'…' is read as a plain string, B'…' and
				// X'…' are bit strings.
				escapes := c|0x20 == 'e' || (c|0x20 == 'n' && backslash)
				i, kind = quoted(i+1, '\'', escapes), pgConstant
			case c|0x20 == 'u' && i+2 < n && text[i+1] == '&' && (text[i+2] == '\'' || text[i+2] == '"'):
				// U&'…' and U&"…" spell characters by their code points.
				kind = pgConstant
				if text[i+2] == '"' {
					kind = pgQuotedName
				}
				i = quoted(i+2, text[i+2], false)
			default:
				for i < n && (letter(text[i]) || digit(text[i]) || text[i] == '$') {
					i++
				}
				kind = pgKeyword
			}
		default:
			i++
		}
		out = append(out, pgToken{kind, start, i})
	}
	return out
}

// pgNumberEnd returns where the number that starts at i ends: an integer in
// decimal, or in hexadecimal, octal or binary after 0x, 0o or 0b, with the
// underscores PostgreSQL 16 allows between digits; or a decimal with a
// fraction and an exponent.
func pgNumberEnd(text string, i int) int {
	n := len(text)
	digits := func(j int) int {
		for j < n && ((text[j] >= '0' && text[j] <= '9') || text[j] == '_') {
			j++
		}
		return j
	}
	if text[i] == '0' && i+1 < n && strings.IndexByte("xXoObB", text[i+1]) >= 0 {
		j := i + 2
		for j < n && (text[j] == '_' || (text[j] >= '0' && text[j] <= '9') || (text[j]|0x20 >= 'a' && text[j]|0x20 <= 'f')) {
			j++
		}
		return j
	}
	j := digits(i)
	if j < n && text[j] == '.' {
		j = digits(j + 1)
	}
	if j < n && text[j]|0x20 == 'e' {
		k := j + 1
		if k < n && (text[k] == '+' || text[k] == '-') {
			k++
		}
		if k < n && text[k] >= '0' && text[k] <= '9' {
			j = digits(k)
		}
	}
	return j
}

// pgValues picks out the tokens that are values: every literal, and in a
// statement the extension keeps as it came, a role's password and a setting's
// value however they are written.
func pgValues(text string, tokens []pgToken) []pgToken {
	var nonblank, out []pgToken
	for _, t := range tokens {
		if t.kind == pgConstant {
			out = append(out, t)
		}
		if t.kind != pgBlank {
			nonblank = append(nonblank, t)
		}
	}
	is := func(i int, keywords ...string) bool {
		if i >= len(nonblank) || nonblank[i].kind != pgKeyword {
			return false
		}
		for _, keyword := range keywords {
			if strings.EqualFold(text[nonblank[i].start:nonblank[i].end], keyword) {
				return true
			}
		}
		return false
	}
	symbol := func(i int, c byte) bool {
		return i < len(nonblank) && nonblank[i].kind == pgPunct && text[nonblank[i].start] == c
	}
	name := func(i int) bool {
		return i < len(nonblank) && (nonblank[i].kind == pgKeyword || nonblank[i].kind == pgQuotedName)
	}
	value := func(i int) bool { return name(i) || (i < len(nonblank) && nonblank[i].kind == pgConstant) }
	// The extension took the values out of a statement it plans, and a word
	// after SET there is a column being assigned, not a setting's value.
	if len(nonblank) == 0 || nonblank[0].kind != pgKeyword ||
		is(0, "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "WITH", "VALUES", "TABLE") {
		return out
	}
	role := is(0, "CREATE", "ALTER") && is(1, "ROLE", "USER", "GROUP")
	for i := range nonblank {
		if role && is(i, "PASSWORD") && value(i+1) {
			out = append(out, nonblank[i+1])
		}
		if !is(i, "SET") {
			continue
		}
		// SET [SESSION | LOCAL] name {TO | =} value [, …], where the name may
		// be an extension's, dotted.
		j := i + 1
		if is(j, "SESSION", "LOCAL") {
			j++
		}
		if !name(j) {
			continue
		}
		for j++; symbol(j, '.') && name(j+1); j += 2 {
		}
		if !is(j, "TO") && !symbol(j, '=') {
			continue
		}
		for j++; value(j); j += 2 {
			out = append(out, nonblank[j])
			if !symbol(j+1, ',') {
				break
			}
		}
	}
	return out
}
