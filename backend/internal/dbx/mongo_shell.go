package dbx

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// The shell's way of writing a document, rewritten as Extended JSON.
//
// mongosh is JavaScript, and this is not a JavaScript interpreter: it reads
// the subset that is a *literal* — objects, arrays, strings, numbers, regular
// expressions and the type constructors — and refuses the rest with the
// Extended JSON spelling of what it thinks was meant. It accepts:
//
//	unquoted keys            { name: 1 }
//	single-quoted strings    { name: 'Ann' }
//	trailing commas          [1, 2, ]
//	comments                 // … and /* … */
//	ObjectId("…")            and ObjectId() for a new one
//	ISODate("…"), new Date("…"), new Date(ms), and either with no argument for now
//	NumberLong(1) NumberLong("1") Long(…)   NumberInt(1) Int32(1)
//	NumberDecimal("1.5") Decimal128("1.5")  Double(1)
//	Timestamp(t, i) Timestamp({t: …, i: …})
//	UUID("…")  BinData(subtype, "base64")  HexData(subtype, "hex")
//	MinKey() MaxKey()  /pattern/flags  RegExp("pattern", "flags")
//	undefined  NaN  Infinity  -Infinity
//
// Anything that computes — a variable, an operator, a function body, a
// template string — is refused. The output is plain text handed to the
// driver's Extended JSON reader, so what a wrapper means is decided in one
// place, and this file only has to get the spelling right.

// mongoShellToExtJSON rewrites one shell-syntax value as Extended JSON.
func mongoShellToExtJSON(text string) (string, error) {
	p := &mongoShellParser{src: text}
	p.skipSpace()
	if p.eof() {
		return "", fmt.Errorf("nothing to read")
	}
	if err := p.value(0); err != nil {
		return "", err
	}
	p.skipSpace()
	if !p.eof() {
		// A trailing semicolon is how a statement pasted from the shell ends.
		if p.src[p.pos] == ';' {
			p.pos++
			p.skipSpace()
		}
		if !p.eof() {
			return "", p.errorf("unexpected %s after the end of the value", p.describe())
		}
	}
	return p.out.String(), nil
}

type mongoShellParser struct {
	src string
	pos int
	out strings.Builder
}

func (p *mongoShellParser) eof() bool { return p.pos >= len(p.src) }

// errorf reports a problem at the current position as line and column, which
// is what an editor can put a marker on.
func (p *mongoShellParser) errorf(format string, a ...any) error {
	line, col := 1, 1
	for i, r := range p.src {
		if i >= p.pos {
			break
		}
		if r == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return fmt.Errorf("line %d, column %d: %s", line, col, fmt.Sprintf(format, a...))
}

func (p *mongoShellParser) describe() string {
	if p.eof() {
		return "the end of the text"
	}
	r, _ := utf8.DecodeRuneInString(p.src[p.pos:])
	return strconv.QuoteRune(r)
}

func (p *mongoShellParser) skipSpace() {
	for !p.eof() {
		c := p.src[p.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.pos++
		case strings.HasPrefix(p.src[p.pos:], "//"):
			end := strings.IndexByte(p.src[p.pos:], '\n')
			if end < 0 {
				p.pos = len(p.src)
			} else {
				p.pos += end
			}
		case strings.HasPrefix(p.src[p.pos:], "/*"):
			end := strings.Index(p.src[p.pos+2:], "*/")
			if end < 0 {
				p.pos = len(p.src)
			} else {
				p.pos += end + 4
			}
		default:
			if r, size := utf8.DecodeRuneInString(p.src[p.pos:]); r != utf8.RuneError && unicode.IsSpace(r) {
				p.pos += size
				continue
			}
			return
		}
	}
}

func (p *mongoShellParser) value(depth int) error {
	if depth > mongoMaxDepth {
		return p.errorf("nested too deeply")
	}
	p.skipSpace()
	if p.eof() {
		return p.errorf("a value is missing")
	}
	c := p.src[p.pos]
	switch {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"' || c == '\'':
		s, err := p.stringLiteral()
		if err != nil {
			return err
		}
		p.out.Write(mongoJSONString(s))
		return nil
	case c == '`':
		return p.errorf("template strings are not supported; use a quoted string")
	case c == '/':
		return p.regexLiteral()
	case c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9'):
		return p.number()
	case isShellIdentStart(c):
		return p.word(depth)
	}
	return p.errorf("unexpected %s where a value was expected", p.describe())
}

func (p *mongoShellParser) object(depth int) error {
	p.pos++ // {
	p.out.WriteByte('{')
	first := true
	for {
		p.skipSpace()
		if p.eof() {
			return p.errorf("the document is not closed; a } is missing")
		}
		if p.src[p.pos] == '}' {
			p.pos++
			p.out.WriteByte('}')
			return nil
		}
		if !first {
			p.out.WriteByte(',')
		}
		first = false
		key, err := p.key()
		if err != nil {
			return err
		}
		p.out.Write(mongoJSONString(key))
		p.skipSpace()
		if p.eof() || p.src[p.pos] != ':' {
			return p.errorf("expected : after the field name %q, found %s", key, p.describe())
		}
		p.pos++
		p.out.WriteByte(':')
		if err := p.value(depth + 1); err != nil {
			return err
		}
		p.skipSpace()
		if p.eof() {
			return p.errorf("the document is not closed; a } is missing")
		}
		switch p.src[p.pos] {
		case ',':
			p.pos++
		case '}':
		default:
			return p.errorf("expected , or } after a field, found %s", p.describe())
		}
	}
}

func (p *mongoShellParser) key() (string, error) {
	c := p.src[p.pos]
	switch {
	case c == '"' || c == '\'':
		return p.stringLiteral()
	case isShellIdentStart(c):
		start := p.pos
		for !p.eof() && isShellIdentPart(p.src[p.pos]) {
			p.pos++
		}
		return p.src[start:p.pos], nil
	case c >= '0' && c <= '9':
		start := p.pos
		for !p.eof() && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
		return p.src[start:p.pos], nil
	}
	return "", p.errorf("expected a field name, found %s; a name holding a dot or a space needs quotes", p.describe())
}

func (p *mongoShellParser) array(depth int) error {
	p.pos++ // [
	p.out.WriteByte('[')
	first := true
	for {
		p.skipSpace()
		if p.eof() {
			return p.errorf("the list is not closed; a ] is missing")
		}
		if p.src[p.pos] == ']' {
			p.pos++
			p.out.WriteByte(']')
			return nil
		}
		if !first {
			p.out.WriteByte(',')
		}
		first = false
		if err := p.value(depth + 1); err != nil {
			return err
		}
		p.skipSpace()
		if p.eof() {
			return p.errorf("the list is not closed; a ] is missing")
		}
		switch p.src[p.pos] {
		case ',':
			p.pos++
		case ']':
		default:
			return p.errorf("expected , or ] after a value, found %s", p.describe())
		}
	}
}

// isShellIdentStart and isShellIdentPart are JavaScript's identifier rules
// for ASCII, plus every byte of a multi-byte character so a key written in
// another alphabet does not need quotes.
func isShellIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isShellIdentPart(c byte) bool {
	return isShellIdentStart(c) || (c >= '0' && c <= '9')
}

// stringLiteral reads a quoted string with JavaScript's escapes, which are a
// superset of JSON's: \x41, \u{1F600}, \', a backslash before a newline.
func (p *mongoShellParser) stringLiteral() (string, error) {
	quote := p.src[p.pos]
	start := p.pos
	p.pos++
	var b strings.Builder
	for {
		if p.eof() {
			p.pos = start
			return "", p.errorf("the string opened here is not closed")
		}
		c := p.src[p.pos]
		switch {
		case c == quote:
			p.pos++
			return b.String(), nil
		case c == '\n':
			p.pos = start
			return "", p.errorf("the string opened here is not closed before the end of the line")
		case c != '\\':
			b.WriteByte(c)
			p.pos++
		default:
			p.pos++
			if p.eof() {
				p.pos = start
				return "", p.errorf("the string opened here is not closed")
			}
			e := p.src[p.pos]
			p.pos++
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'v':
				b.WriteByte('\v')
			case '0':
				b.WriteByte(0)
			case '\n':
				// A backslash at the end of a line continues the string.
			case 'x':
				r, err := p.hexRune(2)
				if err != nil {
					return "", err
				}
				b.WriteRune(r)
			case 'u':
				r, err := p.unicodeEscape()
				if err != nil {
					return "", err
				}
				b.WriteRune(r)
			default:
				// \" \' \\ \/ and any other escaped character stand for
				// themselves.
				r, size := utf8.DecodeRuneInString(p.src[p.pos-1:])
				b.WriteRune(r)
				p.pos += size - 1
			}
		}
	}
}

func (p *mongoShellParser) hexRune(digits int) (rune, error) {
	if p.pos+digits > len(p.src) {
		return 0, p.errorf("an escape sequence is cut short")
	}
	n, err := strconv.ParseUint(p.src[p.pos:p.pos+digits], 16, 32)
	if err != nil {
		return 0, p.errorf("an escape sequence is not hexadecimal")
	}
	p.pos += digits
	return rune(n), nil
}

func (p *mongoShellParser) unicodeEscape() (rune, error) {
	if !p.eof() && p.src[p.pos] == '{' {
		end := strings.IndexByte(p.src[p.pos:], '}')
		if end < 2 || end > 7 {
			return 0, p.errorf("a \\u{…} escape is malformed")
		}
		n, err := strconv.ParseUint(p.src[p.pos+1:p.pos+end], 16, 32)
		if err != nil || n > unicode.MaxRune {
			return 0, p.errorf("a \\u{…} escape is not a code point")
		}
		p.pos += end + 1
		return rune(n), nil
	}
	r, err := p.hexRune(4)
	if err != nil {
		return 0, err
	}
	// A surrogate pair is two \u escapes for one character.
	if r >= 0xD800 && r < 0xDC00 && strings.HasPrefix(p.src[p.pos:], `\u`) {
		save := p.pos
		p.pos += 2
		if low, err := p.hexRune(4); err == nil && low >= 0xDC00 && low < 0xE000 {
			return 0x10000 + (r-0xD800)<<10 + (low - 0xDC00), nil
		}
		p.pos = save
	}
	return r, nil
}

// number reads a JavaScript numeric literal and writes it as a JSON one. JSON
// is stricter about the same numbers: it has no leading +, no bare decimal
// point and no hexadecimal.
func (p *mongoShellParser) number() error {
	start := p.pos
	neg := false
	if c := p.src[p.pos]; c == '-' || c == '+' {
		neg = c == '-'
		p.pos++
	}
	if strings.HasPrefix(p.src[p.pos:], "Infinity") {
		p.pos += len("Infinity")
		if neg {
			p.out.WriteString(`{"$numberDouble":"-Infinity"}`)
		} else {
			p.out.WriteString(`{"$numberDouble":"Infinity"}`)
		}
		return nil
	}
	if strings.HasPrefix(p.src[p.pos:], "0x") || strings.HasPrefix(p.src[p.pos:], "0X") {
		p.pos += 2
		digits := p.pos
		for !p.eof() && isHexDigit(p.src[p.pos]) {
			p.pos++
		}
		n, err := strconv.ParseUint(p.src[digits:p.pos], 16, 63)
		if err != nil {
			p.pos = start
			return p.errorf("not a hexadecimal number")
		}
		if neg {
			p.out.WriteByte('-')
		}
		p.out.WriteString(strconv.FormatUint(n, 10))
		return p.endOfNumber(start)
	}
	intStart := p.pos
	for !p.eof() && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
	}
	intPart := p.src[intStart:p.pos]
	frac := ""
	if !p.eof() && p.src[p.pos] == '.' {
		p.pos++
		fracStart := p.pos
		for !p.eof() && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
		frac = p.src[fracStart:p.pos]
		if frac == "" {
			frac = "0"
		}
	}
	if intPart == "" && frac == "" {
		p.pos = start
		return p.errorf("unexpected %s where a value was expected", p.describe())
	}
	if intPart == "" {
		intPart = "0"
	}
	if len(intPart) > 1 && intPart[0] == '0' {
		p.pos = start
		return p.errorf("a number cannot start with 0; write it without the leading zeros, or quote it if it is text")
	}
	exp := ""
	if !p.eof() && (p.src[p.pos] == 'e' || p.src[p.pos] == 'E') {
		expStart := p.pos
		p.pos++
		if !p.eof() && (p.src[p.pos] == '-' || p.src[p.pos] == '+') {
			p.pos++
		}
		digits := p.pos
		for !p.eof() && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
		if digits == p.pos {
			p.pos = start
			return p.errorf("a number's exponent has no digits")
		}
		exp = p.src[expStart:p.pos]
	}
	if neg {
		p.out.WriteByte('-')
	}
	p.out.WriteString(intPart)
	if frac != "" {
		p.out.WriteString("." + frac)
	}
	p.out.WriteString(exp)
	return p.endOfNumber(start)
}

// endOfNumber refuses a literal that runs straight into a letter, which is a
// typo (12abc) or an expression (1n) rather than a number.
func (p *mongoShellParser) endOfNumber(start int) error {
	if !p.eof() && isShellIdentPart(p.src[p.pos]) {
		p.pos = start
		return p.errorf("this is not a number; quote it if it is text")
	}
	return nil
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// regexLiteral reads /pattern/flags.
func (p *mongoShellParser) regexLiteral() error {
	start := p.pos
	p.pos++
	var pattern strings.Builder
	inClass := false
	for {
		if p.eof() || p.src[p.pos] == '\n' {
			p.pos = start
			return p.errorf("the regular expression opened here is not closed")
		}
		c := p.src[p.pos]
		if c == '\\' && p.pos+1 < len(p.src) {
			// \/ is how a slash is written inside a literal; the pattern
			// itself holds a plain slash.
			if p.src[p.pos+1] == '/' {
				pattern.WriteByte('/')
			} else {
				pattern.WriteString(p.src[p.pos : p.pos+2])
			}
			p.pos += 2
			continue
		}
		if c == '[' {
			inClass = true
		} else if c == ']' {
			inClass = false
		} else if c == '/' && !inClass {
			p.pos++
			break
		}
		pattern.WriteByte(c)
		p.pos++
	}
	flagStart := p.pos
	for !p.eof() && p.src[p.pos] >= 'a' && p.src[p.pos] <= 'z' {
		p.pos++
	}
	return p.writeRegex(pattern.String(), p.src[flagStart:p.pos], start)
}

func (p *mongoShellParser) writeRegex(pattern, flags string, at int) error {
	opts := []byte{}
	for i := 0; i < len(flags); i++ {
		switch f := flags[i]; f {
		case 'i', 'm', 's', 'x', 'u':
			opts = append(opts, f)
		case 'g':
			// "Find every match" means nothing to a query, which only asks
			// whether there is one. The shell drops it too.
		default:
			p.pos = at
			return p.errorf("regular expression flag %q is not one MongoDB has (i, m, s, x, u)", string(f))
		}
	}
	// Extended JSON wants the options in alphabetical order.
	sort.Slice(opts, func(i, j int) bool { return opts[i] < opts[j] })
	p.out.WriteString(`{"$regularExpression":{"pattern":`)
	p.out.Write(mongoJSONString(pattern))
	p.out.WriteString(`,"options":`)
	p.out.Write(mongoJSONString(string(opts)))
	p.out.WriteString(`}}`)
	return nil
}

// shellArg is one constructor argument, already read.
type shellArg struct {
	kind string // "string", "number" or "json"
	text string // the string's content, or the value as Extended JSON
}

// word reads a bare word: a keyword, or a constructor and its arguments.
func (p *mongoShellParser) word(depth int) error {
	start := p.pos
	name := p.ident()
	if name == "new" {
		p.skipSpace()
		if p.eof() || !isShellIdentStart(p.src[p.pos]) {
			return p.errorf("expected a type name after new")
		}
		name = p.ident()
	}
	switch name {
	case "true", "false", "null":
		p.out.WriteString(name)
		return nil
	case "undefined":
		p.out.WriteString(`{"$undefined":true}`)
		return nil
	case "NaN":
		p.out.WriteString(`{"$numberDouble":"NaN"}`)
		return nil
	case "Infinity":
		p.out.WriteString(`{"$numberDouble":"Infinity"}`)
		return nil
	}
	p.skipSpace()
	if p.eof() || p.src[p.pos] != '(' {
		// MinKey and MaxKey are written without parentheses as often as with.
		switch name {
		case "MinKey":
			p.out.WriteString(`{"$minKey":1}`)
			return nil
		case "MaxKey":
			p.out.WriteString(`{"$maxKey":1}`)
			return nil
		}
		p.pos = start
		return p.errorf("%q is not a value; text needs quotes, and a variable or an expression cannot be used here", name)
	}
	args, err := p.arguments(depth)
	if err != nil {
		return err
	}
	ext, err := shellConstructor(name, args)
	if err != nil {
		p.pos = start
		return p.errorf("%v", err)
	}
	p.out.WriteString(ext)
	return nil
}

func (p *mongoShellParser) ident() string {
	start := p.pos
	for !p.eof() && isShellIdentPart(p.src[p.pos]) {
		p.pos++
	}
	return p.src[start:p.pos]
}

func (p *mongoShellParser) arguments(depth int) ([]shellArg, error) {
	p.pos++ // (
	args := []shellArg{}
	for {
		p.skipSpace()
		if p.eof() {
			return nil, p.errorf("the ( opened for this value is not closed")
		}
		if p.src[p.pos] == ')' {
			p.pos++
			return args, nil
		}
		c := p.src[p.pos]
		if c == '"' || c == '\'' {
			s, err := p.stringLiteral()
			if err != nil {
				return nil, err
			}
			args = append(args, shellArg{kind: "string", text: s})
		} else {
			// Anything else is read as a value in its own right, into a
			// buffer of its own so it can be inspected before it is used.
			sub := &mongoShellParser{src: p.src, pos: p.pos}
			if err := sub.value(depth + 1); err != nil {
				return nil, err
			}
			kind := "json"
			if c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9') {
				kind = "number"
			}
			p.pos = sub.pos
			args = append(args, shellArg{kind: kind, text: sub.out.String()})
		}
		p.skipSpace()
		if p.eof() {
			return nil, p.errorf("the ( opened for this value is not closed")
		}
		switch p.src[p.pos] {
		case ',':
			p.pos++
		case ')':
		default:
			return nil, p.errorf("expected , or ) in the arguments, found %s", p.describe())
		}
	}
}

// shellConstructor turns one constructor call into its Extended JSON wrapper.
// Every value is checked here rather than left for the driver to reject, so
// the message can say what the constructor wanted.
func shellConstructor(name string, args []shellArg) (string, error) {
	scalar := func() (string, bool) {
		if len(args) != 1 || args[0].kind == "json" {
			return "", false
		}
		return args[0].text, true
	}
	switch name {
	case "ObjectId", "ObjectID":
		if len(args) == 0 {
			return `{"$oid":"` + primitive.NewObjectID().Hex() + `"}`, nil
		}
		if len(args) != 1 || args[0].kind != "string" {
			return "", fmt.Errorf(`ObjectId takes 24 hexadecimal characters in quotes, like ObjectId("65f1c0ffee0123456789abcd")`)
		}
		if _, err := primitive.ObjectIDFromHex(args[0].text); err != nil {
			return "", fmt.Errorf("%q is not an ObjectId: it must be exactly 24 hexadecimal characters", args[0].text)
		}
		return `{"$oid":"` + strings.ToLower(args[0].text) + `"}`, nil

	case "ISODate", "Date":
		if len(args) == 0 {
			return shellDate(time.Now().UnixMilli()), nil
		}
		if len(args) != 1 || args[0].kind == "json" {
			return "", fmt.Errorf(`%s takes one date in quotes, like ISODate("2024-05-01T12:00:00Z"), or milliseconds since 1970`, name)
		}
		if args[0].kind == "number" {
			ms, err := strconv.ParseInt(args[0].text, 10, 64)
			if err != nil {
				return "", fmt.Errorf("%s(%s) is not a whole number of milliseconds", name, args[0].text)
			}
			return shellDate(ms), nil
		}
		t, err := shellParseTime(args[0].text)
		if err != nil {
			return "", err
		}
		return shellDate(t.UnixMilli()), nil

	case "NumberLong", "Long":
		s, ok := scalar()
		if !ok {
			return "", fmt.Errorf(`%s takes one whole number, like NumberLong("9007199254740993")`, name)
		}
		if _, err := strconv.ParseInt(s, 10, 64); err != nil {
			return "", fmt.Errorf("%s(%s) is not a 64-bit whole number", name, s)
		}
		return `{"$numberLong":"` + s + `"}`, nil

	case "NumberInt", "Int32":
		s, ok := scalar()
		if !ok {
			return "", fmt.Errorf("%s takes one whole number, like NumberInt(42)", name)
		}
		if _, err := strconv.ParseInt(s, 10, 32); err != nil {
			return "", fmt.Errorf("%s(%s) is not a 32-bit whole number", name, s)
		}
		return `{"$numberInt":"` + s + `"}`, nil

	case "NumberDecimal", "Decimal128":
		s, ok := scalar()
		if !ok {
			return "", fmt.Errorf(`%s takes one number in quotes, like NumberDecimal("19.99")`, name)
		}
		if _, err := primitive.ParseDecimal128(s); err != nil {
			return "", fmt.Errorf("%s(%q) is not a decimal number", name, s)
		}
		return `{"$numberDecimal":` + string(mongoJSONString(s)) + `}`, nil

	case "Double":
		s, ok := scalar()
		if !ok {
			return "", fmt.Errorf("Double takes one number, like Double(1.5)")
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsInf(f, 0) {
			return "", fmt.Errorf("Double(%s) is not a number", s)
		}
		return `{"$numberDouble":` + string(mongoJSONString(s)) + `}`, nil

	case "Timestamp":
		t, i, err := shellTimestamp(args)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"$timestamp":{"t":%d,"i":%d}}`, t, i), nil

	case "UUID":
		if len(args) != 1 || args[0].kind != "string" {
			return "", fmt.Errorf(`UUID takes one value in quotes, like UUID("123e4567-e89b-12d3-a456-426614174000")`)
		}
		raw, err := hex.DecodeString(strings.ReplaceAll(args[0].text, "-", ""))
		if err != nil || len(raw) != 16 {
			return "", fmt.Errorf("%q is not a UUID: it must be 32 hexadecimal characters", args[0].text)
		}
		return shellBinary(4, raw), nil

	case "BinData", "HexData":
		if len(args) != 2 || args[0].kind != "number" || args[1].kind != "string" {
			return "", fmt.Errorf(`%s takes a subtype and the data in quotes, like BinData(0, "aGVsbG8=")`, name)
		}
		subtype, err := strconv.ParseUint(args[0].text, 10, 8)
		if err != nil {
			return "", fmt.Errorf("%s subtype %s is not a number from 0 to 255", name, args[0].text)
		}
		var raw []byte
		if name == "HexData" {
			raw, err = hex.DecodeString(args[1].text)
		} else {
			raw, err = base64.StdEncoding.DecodeString(args[1].text)
		}
		if err != nil {
			return "", fmt.Errorf("the data given to %s could not be decoded: %v", name, err)
		}
		return shellBinary(byte(subtype), raw), nil

	case "MinKey":
		return `{"$minKey":1}`, nil
	case "MaxKey":
		return `{"$maxKey":1}`, nil

	case "RegExp":
		if len(args) == 0 || len(args) > 2 || args[0].kind != "string" || (len(args) == 2 && args[1].kind != "string") {
			return "", fmt.Errorf(`RegExp takes a pattern and optional flags in quotes, like RegExp("^a", "i")`)
		}
		flags := ""
		if len(args) == 2 {
			flags = args[1].text
		}
		sub := &mongoShellParser{}
		if err := sub.writeRegex(args[0].text, flags, 0); err != nil {
			return "", fmt.Errorf("regular expression flags %q are not ones MongoDB has (i, m, s, x, u)", flags)
		}
		return sub.out.String(), nil
	}
	return "", fmt.Errorf(`%s(…) is not a type this editor reads; write the value as Extended JSON, for example {"$oid": "…"}, {"$date": "…"} or {"$numberLong": "…"}`, name)
}

func shellDate(ms int64) string {
	return `{"$date":{"$numberLong":"` + strconv.FormatInt(ms, 10) + `"}}`
}

func shellBinary(subtype byte, data []byte) string {
	return fmt.Sprintf(`{"$binary":{"base64":"%s","subType":"%02x"}}`,
		base64.StdEncoding.EncodeToString(data), subtype)
}

// shellTimeLayouts are the forms ISODate accepts in the shell. A time with no
// zone is UTC there, and it is here.
var shellTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04",
	"2006-01-02T15:04Z07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02",
	"20060102T150405Z",
	"20060102",
}

func shellParseTime(s string) (time.Time, error) {
	for _, layout := range shellTimeLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf(`%q is not a date this editor reads; write it like "2024-05-01T12:00:00Z"`, s)
}

func shellTimestamp(args []shellArg) (uint32, uint32, error) {
	usage := fmt.Errorf("Timestamp takes seconds and an increment, like Timestamp(1700000000, 1)")
	part := func(s string) (uint32, error) {
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return 0, usage
		}
		return uint32(n), nil
	}
	if len(args) == 2 && args[0].kind == "number" && args[1].kind == "number" {
		t, err := part(args[0].text)
		if err != nil {
			return 0, 0, err
		}
		i, err := part(args[1].text)
		return t, i, err
	}
	// Timestamp({t: 1, i: 2}) is what the newer shell prints. The argument
	// was already rewritten as JSON, so it is read as that.
	if len(args) == 1 && args[0].kind == "json" {
		var obj struct {
			T *uint32 `json:"t"`
			I *uint32 `json:"i"`
		}
		if err := jsonUnmarshalStrict(args[0].text, &obj); err == nil && obj.T != nil && obj.I != nil {
			return *obj.T, *obj.I, nil
		}
	}
	return 0, 0, usage
}
