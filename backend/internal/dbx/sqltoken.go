package dbx

import (
	"fmt"
	"strings"
)

// One lexer reads every statement the query runner is given.
//
// There used to be two: a splitter that knew where statements ended and a
// comment stripper the risk patterns ran on. They disagreed about what a quote
// and a comment were — the splitter treated [..] as a quoted region on every
// engine, the stripper had never heard of it — so text one of them called a
// comment was invisible to the classifier while the engine still parsed and ran
// it. Splitting, the leading word, the row-returning test and the plan gate all
// read the tokens produced here now, so they cannot hold different opinions.
//
// The rules are per engine because the engines really do differ: a backtick is
// a quote on MySQL and an operator character on PostgreSQL, # opens a comment
// on MySQL and names a temporary table on SQL Server. Where an engine's own
// reading depends on a setting this code cannot see, the construct is refused
// rather than guessed at. This is an authorisation boundary, not a syntax
// highlighter.

type quoteRule uint8

const (
	// quoteNone: the character does not open a quoted region on this engine.
	quoteNone quoteRule = iota
	// quoteDoubled: the region ends at the closing character, and a doubled
	// closing character is an escaped one.
	quoteDoubled
	// quotePlain: the region ends at the first closing character, with no
	// escape at all. SQLite's [name] is the one case.
	quotePlain
	// quoteBackslash: doubling as above, and a backslash may escape too —
	// which depends on a server setting (NO_BACKSLASH_ESCAPES,
	// standard_conforming_strings) or a prefix (E'..'), so a quote the two
	// readings would disagree about is refused.
	quoteBackslash
)

type lexRules struct {
	single, double, backtick, bracket quoteRule

	// dollarQuote reads $tag$ … $tag$ as a quoted body (PostgreSQL).
	dollarQuote bool
	// refuseDollar refuses any $ outside a quoted region. ClickHouse opens a
	// heredoc with one, by rules loose enough that not reading them at all is
	// the only safe reading.
	refuseDollar bool
	// refuseDollarQuote refuses what looks like a dollar-quoted delimiter
	// without reading it, for input whose engine is not known.
	refuseDollarQuote bool
	// wordDollar, wordHash and wordAt make the character part of an identifier:
	// v$session, #temp, @variable.
	wordDollar, wordHash, wordAt bool
	// hashComment: # runs to the end of the line (MySQL).
	hashComment bool
	// refuseHash: a # outside a quoted region is refused.
	refuseHash bool
	// dashNeedsSpace: -- opens a comment only before whitespace (MySQL), so a
	// bare --x is two operators there and a comment everywhere else.
	dashNeedsSpace bool
	// refuseAltQuote refuses Oracle's q'[…]' literal, whose end is not the
	// first quote.
	refuseAltQuote bool
	// refuseCurlyQuotes refuses the typographic quotes ClickHouse reads as
	// real ones.
	refuseCurlyQuotes bool
}

// lexRulesFor returns the lexer for an engine. An engine this package does not
// know gets the strictest reading: every dialect-specific form is refused.
func lexRulesFor(driver Driver) *lexRules {
	switch driver {
	case DriverPostgres:
		return &lexRules{single: quoteBackslash, double: quoteDoubled, dollarQuote: true}
	case DriverMySQL:
		return &lexRules{
			single: quoteBackslash, double: quoteBackslash, backtick: quoteDoubled,
			wordDollar: true, wordAt: true, hashComment: true, dashNeedsSpace: true,
		}
	case DriverSQLite:
		return &lexRules{
			single: quoteDoubled, double: quoteDoubled, backtick: quoteDoubled,
			bracket: quotePlain, wordDollar: true,
		}
	case DriverMSSQL:
		return &lexRules{
			single: quoteDoubled, double: quoteDoubled, bracket: quoteDoubled,
			wordDollar: true, wordHash: true, wordAt: true,
		}
	case DriverOracle:
		return &lexRules{
			single: quoteDoubled, double: quoteDoubled,
			wordDollar: true, wordHash: true, refuseAltQuote: true,
		}
	case DriverClickHouse:
		return &lexRules{
			single: quoteBackslash, double: quoteBackslash, backtick: quoteBackslash,
			refuseDollar: true, refuseHash: true, refuseCurlyQuotes: true,
		}
	default:
		return &lexRules{
			single: quoteBackslash, double: quoteBackslash, backtick: quoteBackslash,
			bracket: quoteDoubled, refuseDollarQuote: true, refuseHash: true,
			dashNeedsSpace: true,
		}
	}
}

type tokenKind uint8

const (
	// tokWord is an identifier or keyword outside any quoted region.
	tokWord tokenKind = iota
	// tokNumber is a numeric literal and whatever the engine would glue to it.
	tokNumber
	// tokQuoted is a string literal or a quoted identifier.
	tokQuoted
	// tokDollar is a dollar-quoted body.
	tokDollar
	// tokPunct is one character of anything else.
	tokPunct
	// tokSemicolon separates statements.
	tokSemicolon
)

type sqlToken struct {
	kind       tokenKind
	start, end int
}

func isWordStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (r *lexRules) wordStart(c byte) bool {
	switch c {
	case '$':
		return r.wordDollar
	case '#':
		return r.wordHash
	case '@':
		return r.wordAt
	}
	return isWordStart(c)
}

// wordCont is what may follow the first character of an identifier. PostgreSQL
// allows a $ there though not in front, which is what makes a$$b$$ one
// identifier rather than a name followed by a dollar-quoted body.
func (r *lexRules) wordCont(c byte) bool {
	if c == '$' {
		return r.wordDollar || r.dollarQuote
	}
	return isDigit(c) || r.wordStart(c)
}

func (r *lexRules) quote(c byte) (rule quoteRule, closing byte) {
	switch c {
	case '\'':
		return r.single, '\''
	case '"':
		return r.double, '"'
	case '`':
		return r.backtick, '`'
	case '[':
		return r.bracket, ']'
	}
	return quoteNone, 0
}

// lexSQL turns a query into tokens. Whitespace and comments produce none.
func lexSQL(r *lexRules, q string) ([]sqlToken, error) {
	tokens := make([]sqlToken, 0, 16)
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == 0:
			return nil, fmt.Errorf("the statement contains a NUL byte")
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			if r.dashNeedsSpace && i+2 < len(q) && q[i+2] > ' ' {
				return nil, fmt.Errorf("ambiguous SQL comment: add whitespace after --")
			}
			end, err := lineCommentEnd(q, i+2)
			if err != nil {
				return nil, err
			}
			i = end
		case c == '#' && r.hashComment:
			end, err := lineCommentEnd(q, i+1)
			if err != nil {
				return nil, err
			}
			i = end
		case c == '#' && r.refuseHash:
			return nil, fmt.Errorf("use standard SQL comments in the query runner")
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			end, err := blockCommentEnd(q, i)
			if err != nil {
				return nil, err
			}
			i = end
		case c == ';':
			tokens = append(tokens, sqlToken{tokSemicolon, i, i + 1})
			i++
		case c == '$' && r.refuseDollar:
			return nil, fmt.Errorf("a $ outside quotes opens a heredoc on this engine, which the query runner does not read")
		case c == '$' && (r.dollarQuote || r.refuseDollarQuote):
			tag := dollarTag(q, i)
			if tag == 0 {
				tokens = append(tokens, sqlToken{tokPunct, i, i + 1})
				i++
				break
			}
			if r.refuseDollarQuote {
				return nil, fmt.Errorf("dollar quoted SQL is not supported by the query runner")
			}
			// The body ends at the first exact repeat of the opening delimiter.
			// PostgreSQL's lexer examines every $ in the body as a possible
			// delimiter and backs off by one character when it is not this one,
			// which is the same set of candidates a substring search visits.
			closing := strings.Index(q[i+tag:], q[i:i+tag])
			if closing < 0 {
				return nil, fmt.Errorf("unterminated dollar-quoted SQL")
			}
			end := i + tag + closing + tag
			tokens = append(tokens, sqlToken{tokDollar, i, end})
			i = end
		case r.refuseCurlyQuotes && isCurlyQuote(q, i):
			return nil, fmt.Errorf("typographic quotes are read as real quotes by this engine; use ' and \"")
		case isDigit(c):
			j := i + 1
			for j < len(q) && isDigit(q[j]) {
				j++
			}
			// Letters glued to the digits belong to the same token on every
			// engine that accepts the text at all (1e5, 0xFF, or a syntax
			// error). On PostgreSQL that run may contain a $ once a letter has
			// started it, and never directly after the digits.
			if j < len(q) && isWordStart(q[j]) {
				for j < len(q) && r.wordCont(q[j]) {
					j++
				}
			}
			tokens = append(tokens, sqlToken{tokNumber, i, j})
			i = j
		case r.wordStart(c):
			j := i + 1
			for j < len(q) && r.wordCont(q[j]) {
				j++
			}
			if r.refuseAltQuote && j < len(q) && q[j] == '\'' {
				if w := strings.ToLower(q[i:j]); w == "q" || w == "nq" {
					return nil, fmt.Errorf("alternative quoting (q'[...]') is not supported by the query runner; use doubled quotes")
				}
			}
			tokens = append(tokens, sqlToken{tokWord, i, j})
			i = j
		default:
			rule, closing := r.quote(c)
			if rule == quoteNone {
				tokens = append(tokens, sqlToken{tokPunct, i, i + 1})
				i++
				break
			}
			end, err := quotedEnd(q, i+1, rule, closing)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, sqlToken{tokQuoted, i, end})
			i = end
		}
	}
	return tokens, nil
}

// lineCommentEnd returns the index just past a comment that runs to the end of
// the line. The engines agree that a line feed ends one and disagree about a
// bare carriage return: PostgreSQL ends the comment there, MySQL and SQLite do
// not. Text after one would be code to some and comment to others, so it is
// refused.
func lineCommentEnd(q string, from int) (int, error) {
	for i := from; i < len(q); i++ {
		switch q[i] {
		case 0:
			return 0, fmt.Errorf("the statement contains a NUL byte")
		case '\n':
			return i + 1, nil
		case '\r':
			if i+1 < len(q) && q[i+1] != '\n' {
				return 0, fmt.Errorf("ambiguous line ending inside a SQL comment: use a line feed")
			}
		}
	}
	return len(q), nil
}

// blockCommentEnd returns the index just past the comment opening at from.
//
// A comment whose body starts with ! (or one letter and !) is not a comment on
// MySQL, MariaDB or TiDB: the server executes what is inside. PostgreSQL and
// SQL Server nest comments and the others do not, so a second opener inside one
// ends in different places on different engines. Both are refused everywhere
// rather than per engine, because nothing legitimate needs either.
func blockCommentEnd(q string, from int) (int, error) {
	body := from + 2
	if body < len(q) && q[body] == '!' ||
		body+1 < len(q) && q[body+1] == '!' && (q[body] >= 'a' && q[body] <= 'z' || q[body] >= 'A' && q[body] <= 'Z') {
		return 0, fmt.Errorf("executable SQL comments are not supported")
	}
	for i := body; i < len(q); i++ {
		switch {
		case q[i] == 0:
			return 0, fmt.Errorf("the statement contains a NUL byte")
		case q[i] == '/' && i+1 < len(q) && q[i+1] == '*':
			return 0, fmt.Errorf("nested SQL comments are not supported")
		case q[i] == '*' && i+1 < len(q) && q[i+1] == '/':
			return i + 2, nil
		}
	}
	return 0, fmt.Errorf("unterminated SQL comment")
}

// quotedEnd returns the index just past the quoted region whose body starts at
// from.
func quotedEnd(q string, from int, rule quoteRule, closing byte) (int, error) {
	backslashes := 0
	for i := from; i < len(q); i++ {
		c := q[i]
		if c == 0 {
			return 0, fmt.Errorf("the statement contains a NUL byte")
		}
		if c == '\\' {
			backslashes++
			continue
		}
		if c == closing {
			// An odd run of backslashes in front of the quote escapes it where
			// backslashes escape and ends the string where they do not.
			if rule == quoteBackslash && backslashes%2 == 1 {
				return 0, fmt.Errorf("use doubled quotes instead of ambiguous backslash escapes")
			}
			if rule != quotePlain && i+1 < len(q) && q[i+1] == closing {
				i++
				backslashes = 0
				continue
			}
			return i + 1, nil
		}
		backslashes = 0
	}
	return 0, fmt.Errorf("unterminated SQL quote")
}

// dollarTag returns the length of the dollar-quote delimiter starting at i, or
// 0 when there is none. The delimiter is $, an optional tag, $; the tag is an
// identifier that cannot start with a digit and cannot contain $, and any byte
// above ASCII counts as a letter — PostgreSQL's own definition, which is what
// lets $tagé$ open a body.
func dollarTag(q string, i int) int {
	j := i + 1
	for j < len(q) && (isWordStart(q[j]) || j > i+1 && isDigit(q[j])) {
		j++
	}
	if j < len(q) && q[j] == '$' {
		return j + 1 - i
	}
	return 0
}

// isCurlyQuote reports a typographic quote: U+2018, U+2019, U+201C or U+201D.
func isCurlyQuote(q string, i int) bool {
	if i+2 >= len(q) || q[i] != 0xE2 || q[i+1] != 0x80 {
		return false
	}
	switch q[i+2] {
	case 0x98, 0x99, 0x9C, 0x9D:
		return true
	}
	return false
}
