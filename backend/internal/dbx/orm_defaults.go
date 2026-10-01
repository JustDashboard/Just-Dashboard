package dbx

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Column defaults, read per engine.
//
// A default arrives as the text the engine's catalogue prints, and every
// engine prints it its own way: PostgreSQL appends a cast to a literal,
// SQL Server wraps everything in parentheses twice, MariaDB quotes strings
// where MySQL does not. A target can only say `@default(now())` or
// `default: 0` if that text is first read back into what it means — and where
// it cannot be read, the honest thing to carry is the expression itself, for
// the targets that have a way to pass one through.

type ormDefKind int

const (
	ormDefNone ormDefKind = iota
	// ormDefAuto is a value the engine numbers itself: a sequence, an identity
	// column, AUTO_INCREMENT.
	ormDefAuto
	// ormDefAssumedAuto is an integer primary key taken to be auto-numbered
	// because nothing could be asked. It makes the column optional on insert
	// and claims nothing else.
	ormDefAssumedAuto
	ormDefNow
	ormDefUUID
	ormDefString
	ormDefNumber
	ormDefBool
	// ormDefJSON is a JSON literal; Text is the JSON.
	ormDefJSON
	ormDefEmptyArray
	// ormDefExpr is anything else: Text is the engine's expression, verbatim.
	ormDefExpr
)

type ormDefault struct {
	Kind ormDefKind
	Text string
	Bool bool
	// Raw is the catalogue's text, kept for the targets that pass a default
	// through as SQL.
	Raw string
}

// sql renders the default as an expression the source engine accepts, for the
// targets that pass a default through as SQL. It is the catalogue's own text
// except where that text is not SQL by itself: MySQL prints a string default
// without its quotes.
func (d ormDefault) sql(driver Driver) string {
	switch d.Kind {
	case ormDefExpr, ormDefUUID:
		return d.Text
	case ormDefString:
		if driver == DriverMySQL {
			return ormSQLString(d.Text)
		}
	}
	return d.Raw
}

var ormNumberRe = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// parseORMDefault reads a column's default for its engine. A generated column
// has an expression, not a default, and gets none.
func parseORMDefault(driver Driver, flavor string, c *ormCol) ormDefault {
	raw := strings.TrimSpace(c.Default)
	if raw == "" || c.Generated {
		return ormDefault{}
	}
	var d ormDefault
	switch driver {
	case DriverPostgres:
		d = parsePostgresDefault(raw, c.t)
	case DriverMySQL:
		d = parseMySQLDefault(raw, flavor, c)
	case DriverSQLite:
		d = parseSQLiteDefault(raw, c.t)
	case DriverMSSQL:
		d = parseMSSQLDefault(raw, c.t)
	case DriverOracle:
		d = parseOracleDefault(raw, c.t)
	case DriverClickHouse:
		d = parseClickHouseDefault(raw, c.t)
	}
	if d.Kind != ormDefNone {
		d.Raw = raw
	}
	return d
}

// ormLiteralDefault types a literal's text by the column it defaults: the same
// '0' is a number on a numeric column and a string on a text one.
func ormLiteralDefault(text string, t ormType, raw string) ormDefault {
	switch {
	case t.Array:
		if strings.TrimSpace(text) == "{}" || strings.TrimSpace(text) == "[]" {
			return ormDefault{Kind: ormDefEmptyArray}
		}
	case t.Kind == ormJSON:
		if json.Valid([]byte(text)) {
			return ormDefault{Kind: ormDefJSON, Text: text}
		}
	case t.Kind == ormBool:
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "t", "true", "1", "yes", "on", "b'1'":
			return ormDefault{Kind: ormDefBool, Bool: true}
		case "f", "false", "0", "no", "off", "b'0'":
			return ormDefault{Kind: ormDefBool}
		}
	case t.isNumber():
		if ormNumberRe.MatchString(strings.TrimSpace(text)) {
			return ormDefault{Kind: ormDefNumber, Text: ormNumberText(text)}
		}
	case t.isString() || t.Kind == ormUnknown:
		return ormDefault{Kind: ormDefString, Text: text}
	}
	// A date, an interval, a byte string: the literal means something only to
	// the engine that parses it.
	return ormDefault{Kind: ormDefExpr, Text: raw}
}

// ormNumberText puts a number the way every target language reads one: no
// leading plus, a digit on both sides of the point. SQL accepts ".5" and "1.";
// JSON, and so most of the targets, do not.
func ormNumberText(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "+")
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	if strings.HasPrefix(s, ".") {
		s = "0" + s
	}
	if i := strings.IndexByte(s, '.'); i >= 0 && (i == len(s)-1 || s[i+1] == 'e' || s[i+1] == 'E') {
		s = s[:i] + s[i+1:]
	}
	return sign + s
}

// ormBareDefault types an unquoted token: a number, a boolean word, NULL, or
// failing those an expression.
func ormBareDefault(token string, t ormType, raw string) ormDefault {
	lower := strings.ToLower(strings.TrimSpace(token))
	switch {
	case lower == "null":
		return ormDefault{}
	case lower == "true" || lower == "false":
		if t.Kind == ormBool || t.Kind == ormUnknown {
			return ormDefault{Kind: ormDefBool, Bool: lower == "true"}
		}
	case ormNumberRe.MatchString(lower):
		if t.Kind == ormBool && (lower == "0" || lower == "1") {
			return ormDefault{Kind: ormDefBool, Bool: lower == "1"}
		}
		if t.isNumber() {
			return ormDefault{Kind: ormDefNumber, Text: ormNumberText(lower)}
		}
		if t.isString() {
			return ormDefault{Kind: ormDefString, Text: strings.TrimSpace(token)}
		}
	}
	return ormDefault{Kind: ormDefExpr, Text: raw}
}

// ormStripParens removes parentheses that wrap the whole expression, as many
// layers as there are.
func ormStripParens(s string) string {
	for {
		s = strings.TrimSpace(s)
		if len(s) < 2 || s[0] != '(' || s[len(s)-1] != ')' {
			return s
		}
		depth, quote := 0, false
		wraps := true
		for i := 0; i < len(s); i++ {
			switch {
			case s[i] == '\'':
				quote = !quote
			case quote:
			case s[i] == '(':
				depth++
			case s[i] == ')':
				depth--
				if depth == 0 && i < len(s)-1 {
					wraps = false
				}
			}
		}
		if !wraps {
			return s
		}
		s = s[1 : len(s)-1]
	}
}

// ormLeadingSQLString splits `'it”s'::text` into the literal's value and what
// follows it.
func ormLeadingSQLString(s string) (value, rest string, ok bool) {
	if len(s) == 0 || s[0] != '\'' {
		return "", "", false
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] == '\'' {
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			return b.String(), s[i+1:], true
		}
		b.WriteByte(s[i])
	}
	return "", "", false
}

// postgresCastRe matches the cast chain PostgreSQL appends to a literal
// default: ::text, ::character varying, ::"My Enum", ::numeric(10,2), ::text[].
var postgresCastRe = regexp.MustCompile(`^(\s*::\s*("[^"]+"|[A-Za-z_][\w ]*)(\."[^"]+"|\.[A-Za-z_]\w*)?(\(\d+(,\s*\d+)?\))?(\[\])*)*\s*$`)

func parsePostgresDefault(raw string, t ormType) ormDefault {
	s := ormStripParens(raw)
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "nextval("), lower == "unique_rowid()":
		// unique_rowid() is CockroachDB's serial.
		return ormDefault{Kind: ormDefAuto}
	case lower == "now()", lower == "current_timestamp", lower == "transaction_timestamp()",
		lower == "localtimestamp":
		return ormDefault{Kind: ormDefNow}
	case lower == "gen_random_uuid()", lower == "uuid_generate_v4()":
		return ormDefault{Kind: ormDefUUID, Text: s}
	}
	if value, rest, ok := ormLeadingSQLString(s); ok {
		if postgresCastRe.MatchString(rest) {
			return ormLiteralDefault(value, t, raw)
		}
		return ormDefault{Kind: ormDefExpr, Text: raw}
	}
	// A bare token may still carry a cast: 0::numeric, NULL::text.
	token := s
	if i := strings.Index(s, "::"); i >= 0 && postgresCastRe.MatchString(s[i:]) {
		token = ormStripParens(s[:i])
	}
	if t.Array && strings.EqualFold(token, "ARRAY[]") {
		return ormDefault{Kind: ormDefEmptyArray}
	}
	return ormBareDefault(token, t, raw)
}

var mysqlNowRe = regexp.MustCompile(`(?i)^(current_timestamp|now|localtime|localtimestamp)(\(\d*\))?$`)

func parseMySQLDefault(raw, flavor string, c *ormCol) ormDefault {
	s := ormStripParens(raw)
	lower := strings.ToLower(s)
	switch {
	case lower == "null":
		return ormDefault{}
	case mysqlNowRe.MatchString(s):
		return ormDefault{Kind: ormDefNow}
	case lower == "uuid()":
		return ormDefault{Kind: ormDefUUID, Text: "(uuid())"}
	}
	// MariaDB prints a string default quoted and an expression bare. MySQL
	// prints a string default bare and flags an expression in EXTRA, which is
	// what DefaultExpr carries; without that flag a call-shaped default is
	// taken for the expression it looks like.
	if value, ok := ormUnquoteSQL(s); ok {
		return ormLiteralDefault(value, c.t, raw)
	}
	if c.DefaultExpr || s != strings.TrimSpace(raw) {
		return ormDefault{Kind: ormDefExpr, Text: raw}
	}
	if ormNumberRe.MatchString(s) || lower == "true" || lower == "false" {
		return ormBareDefault(s, c.t, raw)
	}
	if strings.HasPrefix(lower, "b'") {
		return ormLiteralDefault(lower, c.t, raw)
	}
	if flavor == "mariadb" || (strings.HasSuffix(s, ")") && strings.Contains(s, "(")) {
		return ormDefault{Kind: ormDefExpr, Text: raw}
	}
	// MySQL's bare literal. Where it has to be passed on as SQL — a date, say —
	// it needs the quotes the catalogue left off.
	return ormLiteralDefault(s, c.t, ormSQLString(s))
}

// ormSQLString quotes text as a standard SQL string.
func ormSQLString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func parseSQLiteDefault(raw string, t ormType) ormDefault {
	s := strings.TrimSpace(raw)
	switch strings.ToUpper(s) {
	case "NULL":
		return ormDefault{}
	case "CURRENT_TIMESTAMP", "CURRENT_DATE", "CURRENT_TIME":
		return ormDefault{Kind: ormDefNow}
	}
	if value, ok := ormUnquoteSQL(s); ok {
		return ormLiteralDefault(value, t, raw)
	}
	// SQLite reads a double-quoted token that names no column as a string.
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return ormLiteralDefault(strings.ReplaceAll(s[1:len(s)-1], `""`, `"`), t, raw)
	}
	if s != ormStripParens(s) {
		inner := ormStripParens(s)
		if ormNumberRe.MatchString(inner) {
			return ormBareDefault(inner, t, raw)
		}
		return ormDefault{Kind: ormDefExpr, Text: raw}
	}
	return ormBareDefault(s, t, raw)
}

func parseMSSQLDefault(raw string, t ormType) ormDefault {
	s := ormStripParens(raw)
	lower := strings.ToLower(s)
	switch lower {
	case "null":
		return ormDefault{}
	case "getdate()", "sysdatetime()", "current_timestamp":
		return ormDefault{Kind: ormDefNow}
	case "sysdatetimeoffset()":
		if t.Kind == ormDateTimeTZ {
			return ormDefault{Kind: ormDefNow}
		}
	case "newid()", "newsequentialid()":
		return ormDefault{Kind: ormDefUUID, Text: s}
	}
	literal := s
	if strings.HasPrefix(literal, "N'") || strings.HasPrefix(literal, "n'") {
		literal = literal[1:]
	}
	if value, ok := ormUnquoteSQL(literal); ok {
		return ormLiteralDefault(value, t, raw)
	}
	return ormBareDefault(s, t, raw)
}

func parseOracleDefault(raw string, t ormType) ormDefault {
	s := ormStripParens(raw)
	lower := strings.ToLower(s)
	switch {
	case lower == "null":
		return ormDefault{}
	case strings.HasSuffix(lower, ".nextval"):
		// An identity column's default is its system sequence.
		return ormDefault{Kind: ormDefAuto}
	case lower == "sysdate", lower == "systimestamp", lower == "current_timestamp",
		lower == "current_date", lower == "localtimestamp":
		return ormDefault{Kind: ormDefNow}
	}
	if value, ok := ormUnquoteSQL(s); ok {
		return ormLiteralDefault(value, t, raw)
	}
	return ormBareDefault(s, t, raw)
}

func parseClickHouseDefault(raw string, t ormType) ormDefault {
	s := ormStripParens(raw)
	lower := strings.ToLower(s)
	switch {
	case lower == "now()", strings.HasPrefix(lower, "now64("):
		return ormDefault{Kind: ormDefNow}
	case lower == "generateuuidv4()":
		return ormDefault{Kind: ormDefUUID, Text: s}
	}
	if value, ok := ormUnquoteSQL(s); ok {
		return ormLiteralDefault(value, t, raw)
	}
	return ormBareDefault(s, t, raw)
}
