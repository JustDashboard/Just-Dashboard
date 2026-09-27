package logsx

import (
	"strings"
	"unicode/utf8"
)

// Pattern is a line with its variable parts replaced by <*>: numbers,
// addresses, ids, timestamps and quoted strings. "connection from 10.0.0.4
// port 51202" and the same sentence from another address are one pattern,
// which is how a page of forty thousand lines becomes the twelve things that
// actually happened. It reads the structured message when there is one, since
// a JSON line's keys are the same on every line and say nothing.
//
// It is computed only when a facet, a histogram or a predicate asks for it,
// and never stored on the line: most searches never want it, and it costs a
// pass over the text.
func Pattern(l *Line) string {
	text := l.Message
	if text == "" {
		text = l.Text
	}
	return patternOf(text)
}

const (
	// patternInput and patternCap bound the work and the value. The start of
	// a line is what distinguishes one kind from another; a pattern as long as
	// a stack frame groups nothing a shorter one would not.
	patternInput = 1024
	patternCap   = 256
	wildcard     = "<*>"
)

func patternOf(text string) string {
	if len(text) > patternInput {
		cut := patternInput
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	var b strings.Builder
	b.Grow(min(len(text), patternCap+len(wildcard)))
	for i := 0; i < len(text) && b.Len() < patternCap; {
		c := text[i]
		switch {
		case (c == '"' || c == '\'') && (i == 0 || !wordByte(text[i-1])):
			// A quote opens a literal only where a word does not run into it,
			// so the apostrophe in "can't" stays prose.
			if end := closingQuote(text, i); end > 0 {
				b.WriteString(wildcard)
				i = end + 1
				continue
			}
			b.WriteByte(c)
			i++
		case tokenByte(c):
			j := tokenEnd(text, i+1)
			if variableToken(text[i:j]) {
				b.WriteString(wildcard)
			} else {
				b.WriteString(text[i:j])
			}
			i = j
		case c >= utf8.RuneSelf:
			_, size := utf8.DecodeRuneInString(text[i:])
			b.WriteString(text[i : i+size])
			i += size
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func closingQuote(text string, open int) int {
	quote := text[open]
	for j := open + 1; j < len(text); j++ {
		switch text[j] {
		case '\\':
			j++
		case quote:
			return j
		}
	}
	return -1
}

func tokenByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// joinerByte keeps an address, a version, a clock time and a UUID one token,
// so 10.0.0.4 becomes one <*> rather than four separated by dots. Up to two
// joiners in a row may sit inside a token — IPv6 compresses with "::" — but a
// token never ends on one.
func joinerByte(c byte) bool {
	return c == '.' || c == ':' || c == '-'
}

func tokenEnd(text string, j int) int {
	for j < len(text) {
		if tokenByte(text[j]) {
			j++
			continue
		}
		k := j
		for k < len(text) && k-j < 3 && joinerByte(text[k]) {
			k++
		}
		if k == j || k-j > 2 || k >= len(text) || !tokenByte(text[k]) {
			break
		}
		j = k
	}
	return j
}

// variableToken decides whether a token is data. Anything with a digit in it
// is — a count, an id, an address — except a word with a short number on the
// end, which is a name: sha256, utf8, http2, ipv4, ec2.
func variableToken(tok string) bool {
	digits, letters := 0, 0
	for i := 0; i < len(tok); i++ {
		switch c := tok[i]; {
		case c >= '0' && c <= '9':
			digits++
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_':
			if digits > 0 {
				// A letter after a digit: an id like 8f3a2b, not a name.
				return true
			}
			letters++
		default:
			if digits > 0 {
				return true
			}
		}
	}
	if digits == 0 {
		return false
	}
	return letters == 0 || digits > 3 || len(tok) != letters+digits
}
