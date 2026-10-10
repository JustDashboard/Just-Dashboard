package dbx

import (
	"fmt"
	"regexp"
	"strings"
)

// A column type is the one fragment of a schema form that is neither quoted
// nor bound nor wrapped in parentheses of the server's own: it is written
// straight after the column's name in ALTER TABLE … ADD COLUMN and CREATE
// TABLE. Whatever follows it is therefore read by the engine as the next
// clause, the next action of the same ALTER TABLE — `integer, DROP COLUMN
// email` — or, on SQL Server, which needs nothing between two statements, the
// next statement.
//
// A list of words to refuse cannot close that, because the list is the
// engine's whole grammar. So a type is matched against what a type is:
//
//	name [ (arguments) ] [ qualifier ] [ array brackets ]
//
// with the name one identifier (or one of the standard's several-word
// names), the qualifier one of a closed set, and the arguments held to what
// that engine's types take. Anything else is refused, however harmless it
// looks, and belongs in the Query tab where it is classified as the SQL it is.

// typeNames are the type names the standard and the engines spell with more
// than one word. A run of words in front of the arguments has to be one of
// them: `int unsigned` is a name and a qualifier, `int drop` is nothing.
var typeNames = stringSet(
	"double precision", "character varying", "char varying", "national character", "national char",
	"national character varying", "national char varying", "national varchar", "nchar varying",
	"bit varying", "binary varying", "character large object", "char large object",
	"binary large object", "national character large object", "nchar large object",
	"long raw", "long varchar", "long varbinary",
	"interval year", "interval month", "interval day", "interval hour", "interval minute", "interval second",
	// SQLite reads a type for its affinity and documents these spellings.
	"unsigned big int", "native character", "varying character",
)

// typeQualifiers are what may follow the name or its arguments.
var typeQualifiers = stringSet(
	"with time zone", "without time zone", "with local time zone",
	"unsigned", "signed", "zerofill", "unsigned zerofill",
	"to month", "to hour", "to minute", "to second",
)

// typeWordsRefused are words no type is named: the ones that start a
// constraint, a column option, another action or another statement. The
// grammar above already leaves them nowhere to stand after a type; this keeps
// one from standing in for it, on the engines where a column's type is
// optional and `"c" check(1)` would be a column with a constraint.
var typeWordsRefused = stringSet(
	"primary", "key", "references", "unique", "check", "default", "not", "null", "constraint", "generated",
	"as", "collate", "auto_increment", "autoincrement", "identity", "comment", "on", "foreign", "index",
	"codec", "ttl", "materialized", "alias", "ephemeral", "after", "first", "settings",
	"add", "alter", "drop", "modify", "change", "rename", "create", "truncate", "delete", "insert", "update",
	"select", "merge", "replace", "exec", "execute", "call", "grant", "revoke", "deny", "set", "use", "begin",
	"commit", "rollback", "declare", "with", "to", "from", "into", "where", "values", "table", "column",
	"owner", "enable", "disable", "attach", "detach", "convert", "go", "if", "while", "print", "shutdown",
	"kill", "backup", "restore", "dbcc", "waitfor",
)

var (
	typeArraySuffixRe = regexp.MustCompile(`(\[[0-9]{0,6}\])+$`)
	typeCharsetRe     = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
)

// typeScanner reads a type left to right. It never backs up, so what it has
// accepted is a prefix of the text and what is left is all that remains.
type typeScanner struct {
	driver Driver
	text   string
	pos    int
}

func (s *typeScanner) done() bool { return s.pos >= len(s.text) }

func (s *typeScanner) peek() byte {
	if s.done() {
		return 0
	}
	return s.text[s.pos]
}

func (s *typeScanner) spaces() {
	for !s.done() && s.text[s.pos] == ' ' {
		s.pos++
	}
}

// word reads an identifier, lower-cased, or nothing.
func (s *typeScanner) word() string {
	start := s.pos
	if s.done() || !(isASCIILetter(s.text[s.pos]) || s.text[s.pos] == '_') {
		return ""
	}
	for !s.done() && (isASCIILetter(s.text[s.pos]) || isASCIIDigit(s.text[s.pos]) || s.text[s.pos] == '_') {
		s.pos++
	}
	return strings.ToLower(s.text[start:s.pos])
}

func (s *typeScanner) number() bool {
	start := s.pos
	for !s.done() && isASCIIDigit(s.text[s.pos]) {
		s.pos++
	}
	return s.pos > start
}

// words reads a run of words separated by spaces.
func (s *typeScanner) words() []string {
	var out []string
	for {
		s.spaces()
		w := s.word()
		if w == "" {
			return out
		}
		out = append(out, w)
	}
}

// label reads a quoted label: an enum's value, a time zone. What it may hold
// is narrow on purpose — no quote, no backslash, nothing an engine reads as
// an escape — and a doubled quote is simply two labels side by side.
func (s *typeScanner) label() bool {
	if s.peek() != '\'' {
		return false
	}
	for s.pos++; !s.done(); s.pos++ {
		c := s.text[s.pos]
		if c == '\'' {
			s.pos++
			return true
		}
		if !isASCIILetter(c) && !isASCIIDigit(c) && strings.IndexByte("_ .:/+-", c) < 0 {
			return false
		}
	}
	return false
}

// arguments reads a parenthesised argument list, the opening parenthesis
// included. What an argument may be is the engine's: a length or a precision
// everywhere, a label where an enum is declared on the column, and a whole
// type again on ClickHouse, whose types nest.
func (s *typeScanner) arguments(depth int) bool {
	if s.peek() != '(' || depth > 4 {
		return false
	}
	s.pos++
	for {
		s.spaces()
		if !s.argument(depth) {
			return false
		}
		s.spaces()
		switch s.peek() {
		case ',':
			s.pos++
		case ')':
			s.pos++
			return true
		default:
			return false
		}
	}
}

func (s *typeScanner) argument(depth int) bool {
	switch s.driver {
	case DriverClickHouse:
		// `String`, `Nullable(String)`, `3`, `'UTC'`, `a String` (a named tuple
		// element), `'click' = 1` (an enum label and its number).
		atoms := 0
	scan:
		for ; atoms < 3; atoms++ {
			s.spaces()
			switch {
			case s.peek() == '\'':
				if !s.label() {
					return false
				}
			case s.number():
			case s.word() != "":
				if s.peek() == '(' && !s.arguments(depth+1) {
					return false
				}
			default:
				break scan
			}
		}
		if atoms == 0 {
			return false
		}
		s.spaces()
		if s.peek() == '=' {
			s.pos++
			s.spaces()
			if s.peek() == '-' {
				s.pos++
			}
			return s.number()
		}
		return true
	case DriverMySQL:
		// enum('a','b') and set('a','b') declare their labels here.
		if s.peek() == '\'' {
			for s.peek() == '\'' {
				if !s.label() {
					return false
				}
			}
			return true
		}
	}
	// `255`, `10`, `max`, `Point`, and Oracle's `10 char`.
	if !s.number() && s.word() == "" {
		return false
	}
	s.spaces()
	if w := s.word(); w != "" && w != "char" && w != "byte" {
		return false
	}
	return true
}

// validateType accepts a column type for one engine and nothing after it.
func validateType(driver Driver, t string) error {
	t = strings.TrimSpace(t)
	if t == "" {
		return fmt.Errorf("a column type is required")
	}
	if !validType(driver, t) {
		return fmt.Errorf("column type %q is not one this form can build; use the Query tab for it", t)
	}
	return nil
}

func validType(driver Driver, t string) bool {
	if len(t) > 200 {
		return false
	}
	if driver == DriverPostgres {
		// Array brackets close the type: `text[]`, `integer[3][]`.
		t = strings.TrimRight(typeArraySuffixRe.ReplaceAllString(t, ""), " ")
	}
	s := &typeScanner{driver: driver, text: t}

	name := s.words()
	if len(name) == 0 {
		return false
	}
	qualified := false
	if s.peek() == '.' {
		// A type in a schema: `shop.status`. One name, two parts, and only on
		// the engines where a type lives in a schema.
		if len(name) != 1 || (driver != DriverPostgres && driver != DriverMSSQL && driver != DriverOracle) {
			return false
		}
		s.pos++
		part := s.word()
		if part == "" || typeWordsRefused[name[0]] || typeWordsRefused[part] {
			return false
		}
		qualified = true
	}
	var qualifier []string
	if !qualified {
		// In front of any parenthesis the name and its qualifier are one run
		// of words, and the qualifier is the longest known ending.
		name, qualifier = splitTypeQualifier(driver, name)
	}
	hasArguments := len(qualifier) == 0 && s.peek() == '('
	if hasArguments {
		if !s.arguments(1) {
			return false
		}
		qualifier = s.words()
	}
	switch {
	case qualified:
		if len(qualifier) > 0 {
			return false
		}
	case len(name) == 1 && name[0] == "set":
		// SET is a statement everywhere and, with its labels, a column type on
		// MySQL.
		if driver != DriverMySQL || !hasArguments {
			return false
		}
	case !validTypeName(name):
		return false
	}
	if len(qualifier) > 0 && !validTypeQualifier(driver, qualifier) {
		return false
	}
	// A precision on the far side of the qualifier: `interval day(2) to second(6)`.
	if s.peek() == '(' {
		if strings.Join(qualifier, " ") != "to second" {
			return false
		}
		s.pos++
		if !s.number() || s.peek() != ')' {
			return false
		}
		s.pos++
	}
	s.spaces()
	return s.done()
}

func validTypeName(words []string) bool {
	if len(words) == 1 {
		return !typeWordsRefused[words[0]]
	}
	return typeNames[strings.Join(words, " ")]
}

func validTypeQualifier(driver Driver, words []string) bool {
	if typeQualifiers[strings.Join(words, " ")] {
		return true
	}
	// MySQL names a column's character set after its type.
	if driver != DriverMySQL {
		return false
	}
	switch {
	case len(words) == 3 && words[0] == "character" && words[1] == "set":
		return typeCharsetRe.MatchString(words[2])
	case len(words) == 2 && words[0] == "charset":
		return typeCharsetRe.MatchString(words[1])
	}
	return false
}

// splitTypeQualifier divides a run of words into the type's name and the
// qualifier after it, taking the longest ending that is a qualifier and
// leaves a name in front of it.
func splitTypeQualifier(driver Driver, words []string) (name, qualifier []string) {
	for i := 1; i < len(words); i++ {
		if validTypeQualifier(driver, words[i:]) && validTypeName(words[:i]) {
			return words[:i], words[i:]
		}
	}
	return words, nil
}
