package dbx

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// What a column of a file looks like, and what a column of a table will take.
//
// A delimited file has no types: every field is text, and the engine parses it
// on the way in. That works until it does not — "n/a" in a column of numbers
// stops the import at row 48211 — so before anything is written the first rows
// are read for what each column holds, and that is set against the type of the
// column it is going into. The answer is a warning, not a refusal: the sample
// is the start of the file, and the operator knows their data better than a
// thousand rows of it do.

// The kinds a column's values are inferred as.
const (
	kindEmpty    = "empty"
	kindInteger  = "integer"
	kindDecimal  = "decimal"
	kindBoolean  = "boolean"
	kindDate     = "date"
	kindDatetime = "datetime"
	kindJSON     = "json"
	kindText     = "text"
)

var (
	integerText = regexp.MustCompile(`^[+-]?[0-9]+$`)
	decimalText = regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?$`)
	dateText    = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	// A date, then T or a space, then a time, with or without a fraction and
	// a zone.
	datetimeText = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}[T ][0-9]{2}:[0-9]{2}(:[0-9]{2}(\.[0-9]+)?)?( ?(Z|[+-][0-9]{2}(:?[0-9]{2})?))?$`)
	zonedText    = regexp.MustCompile(`(Z|[+-][0-9]{2}(:?[0-9]{2})?)$`)
	hexBytesText = regexp.MustCompile(`^\\x([0-9a-fA-F]{2})*$`)
)

// columnProfile is what the sample says about one source column.
type columnProfile struct {
	kind string
	// zoned is a datetime column in which some value named its zone.
	zoned bool
	// longest is the longest value seen, in characters.
	longest int
	// examples are the first few distinct values, for the mapping screen.
	examples []string
	// seen are the kinds met so far, which is what settles the column's.
	seen map[string]bool
	// oddValue and oddLine are the first value that is not of the column's
	// kind — kept for the warning, which is worth more with the value in it.
	firstOf map[string]sampleValue
}

type sampleValue struct {
	text string
	line int
}

func newColumnProfile() *columnProfile {
	return &columnProfile{seen: map[string]bool{}, firstOf: map[string]sampleValue{}}
}

// valueKind classifies one value.
func valueKind(v importValue) string {
	if v.raw != nil {
		switch v.raw.(type) {
		case bool:
			return kindBoolean
		default:
			if integerText.MatchString(v.text) {
				return kindInteger
			}
			return kindDecimal
		}
	}
	text := strings.TrimSpace(v.text)
	switch {
	case text == "":
		return kindEmpty
	case integerText.MatchString(text):
		// A long run of digits with a leading zero is a code, not a count:
		// stored as a number it comes back without the zero.
		if len(text) > 1 && text[0] == '0' {
			return kindText
		}
		return kindInteger
	case decimalText.MatchString(text):
		return kindDecimal
	case dateText.MatchString(text):
		return kindDate
	case datetimeText.MatchString(text):
		return kindDatetime
	}
	switch strings.ToLower(text) {
	case "true", "false":
		return kindBoolean
	}
	if (text[0] == '{' && text[len(text)-1] == '}') || (text[0] == '[' && text[len(text)-1] == ']') {
		if validJSONText(text) {
			return kindJSON
		}
	}
	return kindText
}

func (p *columnProfile) add(v importValue, null bool, line int) {
	if null {
		return
	}
	kind := valueKind(v)
	if kind == kindEmpty {
		return
	}
	if kind == kindDatetime && zonedText.MatchString(strings.TrimSpace(v.text)) {
		p.zoned = true
	}
	if n := len([]rune(v.text)); n > p.longest {
		p.longest = n
	}
	if !p.seen[kind] {
		p.seen[kind] = true
		p.firstOf[kind] = sampleValue{text: v.text, line: line}
	}
	if len(p.examples) < 3 {
		known := false
		for _, e := range p.examples {
			if e == v.text {
				known = true
			}
		}
		if !known {
			p.examples = append(p.examples, clipText(v.text, 80))
		}
	}
}

// settle decides the column's kind from everything it held. Mixed kinds widen
// to the narrowest that holds them all: integers and decimals are decimals,
// dates and datetimes are datetimes, and anything else together is text.
func (p *columnProfile) settle() {
	kinds := make([]string, 0, len(p.seen))
	for k := range p.seen {
		kinds = append(kinds, k)
	}
	only := func(allowed ...string) bool {
		for _, k := range kinds {
			ok := false
			for _, a := range allowed {
				if k == a {
					ok = true
				}
			}
			if !ok {
				return false
			}
		}
		return true
	}
	switch {
	case len(kinds) == 0:
		p.kind = kindEmpty
	case len(kinds) == 1:
		p.kind = kinds[0]
	case only(kindInteger, kindDecimal):
		p.kind = kindDecimal
	case only(kindDate, kindDatetime):
		p.kind = kindDatetime
	default:
		p.kind = kindText
	}
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func validJSONText(text string) bool {
	rec := importRecord{}
	if text[0] == '{' {
		parseJSONRecord([]byte(text), &rec)
		return rec.err == nil
	}
	var probe importRecord
	parseJSONRecord([]byte(`{"v":`+text+`}`), &probe)
	return probe.err == nil
}

// typeFamily sorts an engine's type name into what it holds. The names differ
// per engine and the families do not: it is the family that decides whether a
// value fits.
func typeFamily(typeName string) string {
	t := strings.ToLower(strings.TrimSpace(typeName))
	base := t
	if i := strings.IndexAny(base, "( "); i >= 0 {
		base = base[:i]
	}
	// An array, whatever it is an array of, takes the engine's own text form.
	if strings.HasSuffix(t, "[]") || strings.HasPrefix(t, "array") || strings.HasPrefix(t, "_") {
		return "other"
	}
	// ClickHouse wraps the type that matters.
	for _, wrapper := range []string{"nullable(", "lowcardinality("} {
		if strings.HasPrefix(t, wrapper) && strings.HasSuffix(t, ")") {
			return typeFamily(t[len(wrapper) : len(t)-1])
		}
	}
	switch base {
	case "uuid", "uniqueidentifier", "time", "timetz", "interval", "inet", "cidr", "macaddr",
		"point", "line", "polygon", "geometry", "geography", "tsvector", "hstore":
		// Each has a text form only its engine can judge.
		return "other"
	}
	switch {
	case t == "tinyint(1)" || base == "bool" || base == "boolean" || base == "bit":
		return kindBoolean
	case strings.Contains(base, "int") || base == "serial" || base == "bigserial" || base == "smallserial" || base == "year":
		return kindInteger
	case base == "numeric" || base == "decimal" || base == "number" || base == "money" || base == "smallmoney" ||
		base == "real" || base == "double" || strings.HasPrefix(base, "float") || strings.HasPrefix(base, "binary_"):
		return kindDecimal
	case base == "date" || base == "date32":
		return kindDate
	case strings.HasPrefix(base, "timestamp") || strings.HasPrefix(base, "datetime") || base == "smalldatetime":
		return kindDatetime
	case base == "json" || base == "jsonb":
		return kindJSON
	case isBinaryTypeName(base):
		return "binary"
	case strings.Contains(base, "char") || strings.Contains(base, "text") || base == "string" ||
		strings.Contains(base, "clob") || base == "citext" || base == "enum" || base == "set" || base == "xml":
		return kindText
	}
	// A type this does not know — an enum by its own name, a domain, a
	// geometry — is the engine's to judge.
	return "other"
}

// fits reports whether values of an inferred kind go into a column of a
// family without the engine having to refuse any of them.
func fits(kind, family string) bool {
	if kind == kindEmpty || family == "other" || family == kindText || family == "binary" {
		return true
	}
	switch kind {
	case kindInteger:
		return family == kindInteger || family == kindDecimal || family == kindBoolean
	case kindDecimal:
		return family == kindDecimal
	case kindBoolean:
		return family == kindBoolean || family == kindInteger
	case kindDate:
		return family == kindDate || family == kindDatetime
	case kindDatetime:
		return family == kindDatetime
	case kindJSON:
		return family == kindJSON
	}
	return false
}

// compatibilityWarning says what will go wrong, with the value it will go
// wrong on, or nothing.
func compatibilityWarning(p *columnProfile, target Column) string {
	family := typeFamily(target.Type)
	if fits(p.kind, family) {
		return ""
	}
	if p.kind == kindDatetime && family == kindDate {
		return fmt.Sprintf("%s holds a date; the time of day in values like %q will be dropped or refused",
			target.Name, p.firstOf[kindDatetime].text)
	}
	// Name the value that does not fit rather than the column's settled kind:
	// "is text" about a column of numbers with one "n/a" in it sends somebody
	// looking for the wrong thing.
	for _, kind := range []string{kindText, kindJSON, kindDatetime, kindDate, kindBoolean, kindDecimal, kindInteger} {
		odd, ok := p.firstOf[kind]
		if !ok || fits(kind, family) {
			continue
		}
		return fmt.Sprintf("%s is %s, and line %d holds %q", target.Name, target.Type, odd.line, clipText(odd.text, 60))
	}
	return fmt.Sprintf("%s is %s, and the file's values look like %s", target.Name, target.Type, p.kind)
}

// inferredColumnType is the type a new table's column is given for values of
// a kind, in each engine's own spelling.
func inferredColumnType(driver Driver, p *columnProfile) string {
	type names struct{ integer, decimal, boolean, date, datetime, zoned, json, text string }
	var n names
	switch driver {
	case DriverPostgres:
		n = names{"bigint", "numeric", "boolean", "date", "timestamp", "timestamptz", "jsonb", "text"}
	case DriverMySQL:
		n = names{"bigint", "double", "tinyint(1)", "date", "datetime", "datetime", "json", "text"}
	case DriverSQLite:
		n = names{"INTEGER", "NUMERIC", "BOOLEAN", "DATE", "DATETIME", "DATETIME", "TEXT", "TEXT"}
	case DriverMSSQL:
		n = names{"bigint", "float", "bit", "date", "datetime2", "datetimeoffset", "nvarchar(max)", "nvarchar(max)"}
	case DriverOracle:
		text := "VARCHAR2(4000)"
		if p.longest > 1000 {
			// VARCHAR2 is sized in bytes by default, and a character is up to
			// four of them.
			text = "CLOB"
		}
		n = names{"NUMBER(19)", "NUMBER", "NUMBER(1)", "DATE", "TIMESTAMP", "TIMESTAMP WITH TIME ZONE", "CLOB", text}
	default:
		n = names{"Int64", "Float64", "Bool", "Date", "DateTime64(3)", "DateTime64(3)", "String", "String"}
	}
	switch p.kind {
	case kindInteger:
		return n.integer
	case kindDecimal:
		return n.decimal
	case kindBoolean:
		return n.boolean
	case kindDate:
		return n.date
	case kindDatetime:
		if p.zoned {
			return n.zoned
		}
		return n.datetime
	case kindJSON:
		return n.json
	}
	return n.text
}

// normaliseName is how two column names are compared when they are not
// spelled alike: case aside, and with everything that is not a letter or a
// digit read as one separator. "Full Name", "full_name" and "FULL-NAME" are
// the same column to a person.
func normaliseName(name string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if gap && b.Len() > 0 {
				b.WriteByte('_')
			}
			gap = false
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		gap = true
	}
	return b.String()
}

// matchColumn finds the table column a source column goes into when nobody
// said: the same name, then the same name whatever its case, then the same
// name however it is punctuated.
func matchColumn(source string, targets []Column) (Column, bool) {
	for _, t := range targets {
		if t.Name == source {
			return t, true
		}
	}
	for _, t := range targets {
		if strings.EqualFold(t.Name, source) {
			return t, true
		}
	}
	want := normaliseName(source)
	if want == "" {
		return Column{}, false
	}
	for _, t := range targets {
		if normaliseName(t.Name) == want {
			return t, true
		}
	}
	return Column{}, false
}

// booleanValue reads the spellings of true and false a file may use.
func booleanValue(text string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "true", "t", "yes", "y", "1":
		return true, true
	case "false", "f", "no", "n", "0":
		return false, true
	}
	return false, false
}

// bindValue turns a field into what is bound for a column of a family.
//
// Mostly that is the text as it stands: the engine parses it into the column's
// type, and its parser is the authority on what that type's values look like.
// The exceptions are the ones where the engines disagree with each other or
// with the file: a boolean spelled as a word, which MySQL reads as zero; the
// \x form of binary this dashboard's own export writes; and ClickHouse, whose
// driver takes a number as a number or not at all.
func bindValue(driver Driver, v importValue, family, typeName string) (any, error) {
	if v.raw != nil {
		if b, ok := v.raw.(bool); ok {
			return boolForDriver(driver, b, family), nil
		}
		if driver != DriverClickHouse {
			// The number as the file wrote it. Bound as text it reaches the
			// column exact, where a float would have rounded it.
			return v.text, nil
		}
	}
	switch family {
	case kindBoolean:
		if b, ok := booleanValue(v.text); ok {
			return boolForDriver(driver, b, family), nil
		}
	case kindInteger, kindDecimal:
		// A column of true and false is a column of numbers on the engines
		// with no boolean type: Oracle's is NUMBER(1), and that is what a
		// table made from such a file is given there.
		if driver != DriverClickHouse {
			switch strings.ToLower(strings.TrimSpace(v.text)) {
			case "true":
				return int64(1), nil
			case "false":
				return int64(0), nil
			}
		}
	case "binary":
		if hexBytesText.MatchString(v.text) {
			if b, err := hex.DecodeString(v.text[2:]); err == nil {
				return b, nil
			}
		}
		if driver == DriverMSSQL {
			// The others take a text into a binary column as its bytes. SQL
			// Server would take it as the bytes of its UTF-16 form, which is
			// nobody's intention.
			return nil, fmt.Errorf("%q is not bytes; a binary column takes \\x followed by hex digits", clipText(v.text, 40))
		}
	case kindDate, kindDatetime:
		if driver == DriverOracle {
			// Oracle reads a text as a date by the session's own format, which
			// is not ISO's. A value in an ISO form is bound as the instant it
			// names; anything else is left for the session to read its way.
			if t, ok := importISOTime(v.text); ok {
				return t, nil
			}
		}
	}
	if driver == DriverClickHouse {
		text := strings.TrimSpace(v.text)
		switch family {
		case kindInteger:
			if strings.HasPrefix(text, "-") {
				if n, err := strconv.ParseInt(text, 10, 64); err == nil {
					return n, nil
				}
			} else if n, err := strconv.ParseUint(text, 10, 64); err == nil {
				return n, nil
			}
			return nil, fmt.Errorf("%q is not a whole number", clipText(v.text, 40))
		case kindDecimal:
			n, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return nil, fmt.Errorf("%q is not a number", clipText(v.text, 40))
			}
			if strings.HasPrefix(strings.ToLower(clickhouseInnerType(typeName)), "float") {
				return n, nil
			}
			// A Decimal is taken as text, which keeps every digit of it.
			return text, nil
		}
	}
	return v.text, nil
}

// importISOTime reads a date or an instant in the ISO forms a file is likely to
// carry: a date alone, or a date and a time joined by T or a space, to the
// minute or the second or a fraction of one, with or without a zone. One
// without a zone is the wall-clock time it says.
func importISOTime(text string) (time.Time, bool) {
	text = strings.TrimSpace(text)
	if dateText.MatchString(text) {
		t, err := time.Parse("2006-01-02", text)
		return t, err == nil
	}
	if !datetimeText.MatchString(text) {
		return time.Time{}, false
	}
	// One spelling of what the pattern admits several of.
	text = strings.Replace(text, " ", "T", 1)
	text = strings.ReplaceAll(text, " ", "")
	zone := zonedText.FindString(text)
	clock := strings.TrimSuffix(text, zone)
	if strings.Count(clock, ":") == 1 {
		clock += ":00"
	}
	switch {
	case zone == "" || zone == "Z":
		zone = "Z"
	case len(zone) == 3:
		zone += ":00"
	case len(zone) == 5:
		zone = zone[:3] + ":" + zone[3:]
	}
	t, err := time.Parse(time.RFC3339Nano, clock+zone)
	return t, err == nil
}

// importKeyColumnType is the type a new table's key column is given where the one
// inferred for its values cannot be a key: SQL Server and MySQL index a text
// of bounded length only.
func importKeyColumnType(driver Driver, inferred string) string {
	switch {
	case driver == DriverMSSQL && strings.EqualFold(inferred, "nvarchar(max)"):
		// 900 bytes is the most a key may be, and a character is two.
		return "nvarchar(450)"
	case driver == DriverMySQL && strings.EqualFold(inferred, "text"):
		return "varchar(255)"
	}
	return inferred
}

// boolForDriver is true and false as the engine's column takes them: the
// engines with a boolean type are given one, the rest a one or a zero.
func boolForDriver(driver Driver, b bool, family string) any {
	if (driver == DriverPostgres && family != kindInteger) || driver == DriverClickHouse {
		return b
	}
	if b {
		return int64(1)
	}
	return int64(0)
}

// currentSchema asks the engine which schema an unqualified table name is
// looked up in. The catalogue has to be asked by schema, and the answer is the
// session's, not a constant: a Postgres role's search path may not start at
// public.
func currentSchema(ctx context.Context, db *sql.DB, d Dialect) string {
	var query string
	switch d.Driver() {
	case DriverPostgres:
		query = "SELECT current_schema()"
	case DriverMySQL:
		query = "SELECT DATABASE()"
	case DriverMSSQL:
		query = "SELECT SCHEMA_NAME()"
	case DriverOracle:
		query = "SELECT SYS_CONTEXT('USERENV','CURRENT_SCHEMA') FROM dual"
	case DriverClickHouse:
		query = "SELECT currentDatabase()"
	default:
		return d.DefaultSchema()
	}
	var name sql.NullString
	if err := db.QueryRowContext(ctx, query).Scan(&name); err != nil || name.String == "" {
		return d.DefaultSchema()
	}
	return name.String
}
