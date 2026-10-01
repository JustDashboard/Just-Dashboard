package dbx

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	mssql "github.com/microsoft/go-mssqldb"
)

var jsonNumberPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

// JSON numbers must reach the driver before a float64 conversion can round a
// primary key or decimal. Numeric strings bind unchanged; the column determines
// their SQL type. This also preserves unsigned integers larger than MaxInt64.
func SQLValue(value any) (any, error) {
	switch v := value.(type) {
	case json.Number:
		// Parsing through float64 would reject valid database numerics outside
		// its exponent range and can discard digits before the driver sees them.
		if !jsonNumberPattern.MatchString(v.String()) {
			return nil, fmt.Errorf("invalid numeric value")
		}
		return v.String(), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("numeric values must be finite")
		}
		if math.Trunc(v) == v && math.Abs(v) > 9007199254740991 {
			return nil, fmt.Errorf("large integers must be decimal strings")
		}
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, fmt.Errorf("numeric values must be finite")
		}
	}
	return value, nil
}

func sqlArguments(args []any) ([]any, error) {
	values := make([]any, len(args))
	for i, arg := range args {
		value, err := SQLValue(arg)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", i+1, err)
		}
		values[i] = value
	}
	return values, nil
}

// ClickHouse can return typed arrays/maps containing Int64, UInt64 or big.Int.
// Protect their elements too: only converting top-level cells would still let
// JSON.parse round a nested identifier. Driver scalar Stringers (notably big.Int
// and decimal wrappers) retain their exact textual representation.
func normaliseNumericContainer(value any) any {
	if integer, ok := value.(big.Int); ok {
		return integer.String()
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return nil
	}
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		if text, ok := value.(fmt.Stringer); ok {
			return text.String()
		}
		return normaliseValue(rv.Elem().Interface())
	}
	if text, ok := value.(fmt.Stringer); ok {
		return text.String()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil
		}
		items := make([]any, rv.Len())
		for i := range items {
			items[i] = normaliseValue(rv.Index(i).Interface())
		}
		return items
	case reflect.Map:
		if rv.IsNil() {
			return nil
		}
		keyKind := rv.Type().Key().Kind()
		if keyKind != reflect.String && !(keyKind >= reflect.Int && keyKind <= reflect.Uint64) {
			return value
		}
		items := make(map[string]any, rv.Len())
		for _, key := range rv.MapKeys() {
			items[fmt.Sprint(key.Interface())] = normaliseValue(rv.MapIndex(key).Interface())
		}
		return items
	}
	return value
}

// normaliseValue converts driver values into something JSON can carry.
//
// []byte is the interesting one, and it arrives for two completely different
// reasons. MySQL hands back ordinary text columns as bytes, so encoding every
// []byte would turn most of a MySQL database into base64; but a bytea, a BLOB
// or a varbinary really is binary, and `string(t)` on it is both unreadable
// and *lossy* — invalid UTF-8 becomes U+FFFD, which put mojibake and raw
// control characters into the grid, into CSV and JSON exports, and into the
// INSERT statement the row menu copies to the clipboard, where the bytes that
// came back were no longer the bytes that went in.
//
// With no column type to go on, the decision is made on the content: bytes
// that are valid UTF-8 text are the text they are, and anything else becomes
// the hex form every one of these engines also accepts and prints. Long values
// are cut off rather than turning a megabyte blob into two megabytes of hex in
// a row nobody can read anyway. Where the column type is known, encodeCell
// decides from that instead.
func normaliseValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		if isPrintableText(t) {
			return string(t)
		}
		return hexPreview(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	case int:
		return strconv.FormatInt(int64(t), 10)
	case uint:
		return strconv.FormatUint(uint64(t), 10)
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return strconv.FormatFloat(t, 'g', -1, 64)
		}
		return t
	case float32:
		if math.IsNaN(float64(t)) || math.IsInf(float64(t), 0) {
			return strconv.FormatFloat(float64(t), 'g', -1, 32)
		}
		return t
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	default:
		return normaliseNumericContainer(v)
	}
}

// maxHexPreview bounds the rendered form of a binary value. A row is meant to
// be looked at; an operator who needs the whole blob asks for that one cell.
const maxHexPreview = 256

func isPrintableText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		// Tab, newline and carriage return are ordinary in a text column. The
		// rest of C0, and the NUL in particular, mean this is not text.
		if r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func hexPreview(b []byte) string {
	if len(b) <= maxHexPreview {
		return "\\x" + hex.EncodeToString(b)
	}
	return "\\x" + hex.EncodeToString(b[:maxHexPreview]) +
		"… (" + itoa(len(b)) + " bytes)"
}

// The kinds a column can be, in the dashboard's vocabulary rather than any one
// engine's. They are what lets one grid right-align a number, draw a boolean,
// open a JSON viewer and refuse to treat a blob as text on eight engines
// without a table of type names in the frontend.
const (
	KindText     = "text"
	KindInteger  = "integer"
	KindDecimal  = "decimal"
	KindFloat    = "float"
	KindBoolean  = "boolean"
	KindDate     = "date"
	KindTime     = "time"
	KindDateTime = "datetime"
	KindInterval = "interval"
	KindJSON     = "json"
	KindUUID     = "uuid"
	KindBinary   = "binary"
	KindArray    = "array"
	KindOther    = "other"
)

// ValueKind classifies a column type name. It accepts both what a driver
// reports for a result column (INT8, VARCHAR, _TEXT) and what a catalogue
// reports for a table column (character varying(255), int unsigned,
// Nullable(String)), because the grid meets both.
func ValueKind(driver Driver, typeName string) string {
	t := strings.ToUpper(strings.TrimSpace(typeName))
	// ClickHouse wraps the type it means in what it allows.
	for unwrapped := true; unwrapped; {
		unwrapped = false
		for _, wrapper := range []string{"NULLABLE(", "LOWCARDINALITY("} {
			if strings.HasPrefix(t, wrapper) && strings.HasSuffix(t, ")") {
				t, unwrapped = t[len(wrapper):len(t)-1], true
			}
		}
	}
	if t == "" {
		return KindOther
	}
	if strings.HasSuffix(t, "[]") || strings.HasPrefix(t, "_") || strings.HasPrefix(t, "ARRAY") {
		return KindArray
	}
	base := t
	if i := strings.IndexByte(base, '('); i >= 0 {
		base = strings.TrimSpace(base[:i])
	}
	// MySQL's driver spells an unsigned column "UNSIGNED BIGINT".
	base = strings.TrimPrefix(base, "UNSIGNED ")
	first := base
	if i := strings.IndexByte(first, ' '); i >= 0 {
		first = first[:i]
	}
	switch {
	case first == "BOOL" || first == "BOOLEAN":
		return KindBoolean
	case first == "BIT":
		// SQL Server's bit is its boolean; elsewhere it is a string of bits.
		if driver == DriverMSSQL {
			return KindBoolean
		}
		return KindBinary
	case first == "INTERVAL" || strings.HasPrefix(first, "INTERVAL"):
		return KindInterval
	case first == "BINARY_FLOAT" || first == "BINARY_DOUBLE":
		return KindFloat
	case first == "TIMESTAMP" && driver == DriverMSSQL, first == "ROWVERSION":
		// SQL Server's timestamp is a row version counter, eight opaque bytes.
		return KindBinary
	case first == "BYTEA" || strings.Contains(first, "BLOB") || strings.Contains(first, "BINARY") ||
		first == "IMAGE" || first == "RAW" || base == "LONG RAW" ||
		first == "GEOMETRY" || first == "GEOGRAPHY":
		return KindBinary
	case first == "JSON" || first == "JSONB":
		return KindJSON
	case first == "UUID" || first == "UNIQUEIDENTIFIER":
		return KindUUID
	case integerTypes[first] || sizedInteger(first):
		return KindInteger
	case first == "NUMERIC" || first == "NUMBER" || first == "MONEY" || first == "SMALLMONEY" ||
		strings.HasPrefix(first, "DECIMAL"):
		return KindDecimal
	case first == "REAL" || first == "DOUBLE" || strings.HasPrefix(first, "FLOAT"):
		return KindFloat
	case strings.HasPrefix(first, "TIMESTAMP") || strings.HasPrefix(first, "DATETIME") ||
		first == "SMALLDATETIME":
		return KindDateTime
	case first == "DATE" || first == "DATE32":
		return KindDate
	case first == "TIME" || first == "TIMETZ":
		return KindTime
	case strings.Contains(first, "CHAR") || strings.Contains(first, "TEXT") || strings.Contains(first, "CLOB") ||
		strings.Contains(first, "STRING") || first == "ENUM" || first == "SET" || first == "NAME" ||
		first == "XML" || first == "ENUM8" || first == "ENUM16":
		return KindText
	}
	return KindOther
}

var integerTypes = map[string]bool{
	"INT": true, "INTEGER": true, "TINYINT": true, "SMALLINT": true, "MEDIUMINT": true,
	"BIGINT": true, "HUGEINT": true, "SERIAL": true, "BIGSERIAL": true, "SMALLSERIAL": true,
	"OID": true, "YEAR": true,
}

// sizedInteger reports the names that carry their width: INT4, INT64, UINT8.
// Spelled out rather than "starts with INT", which INTERVAL does too, or "ends
// with INT", which POINT does.
func sizedInteger(name string) bool {
	rest := strings.TrimPrefix(name, "U")
	if !strings.HasPrefix(rest, "INT") || len(rest) == 3 {
		return false
	}
	digits := rest[3:]
	for i := 0; i < len(digits); i++ {
		if !isDigit(digits[i]) {
			return false
		}
	}
	return true
}

// encodeCell is normaliseValue for a cell whose column type is known.
//
// A binary column is always shown as hex, even when its bytes happen to spell
// something readable: deciding by content made the same column text in one row
// and hex in the next, and made a binary key unusable — the editor sent back
// the display string and it matched nothing. size is the value's length in
// bytes when it was cut, 0 when the cell holds the whole value.
func encodeCell(driver Driver, kind, typeName string, v any, clipText int) (out any, size int64) {
	switch t := v.(type) {
	case []byte:
		if driver == DriverMSSQL && len(t) == 16 && strings.EqualFold(typeName, "UNIQUEIDENTIFIER") {
			// The wire form is byte-swapped in its first three groups; the
			// driver's own type knows how to read it.
			var id mssql.UniqueIdentifier
			if id.Scan(t) == nil {
				return id.String(), 0
			}
		}
		// SQLite's driver returns text as a string and only a BLOB as bytes,
		// whatever the column was declared as.
		binary := kind == KindBinary || driver == DriverSQLite
		if !binary && isPrintableText(t) {
			return clipString(string(t), clipText)
		}
		if len(t) > maxHexPreview {
			return hexPreview(t), int64(len(t))
		}
		return hexPreview(t), 0
	case string:
		return clipString(t, clipText)
	}
	return normaliseValue(v), 0
}

// clipString cuts text to limit bytes on a character boundary. A limit of 0
// leaves it whole.
func clipString(s string, limit int) (any, int64) {
	if limit <= 0 || len(s) <= limit {
		return s, 0
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], int64(len(s))
}

var hexValuePattern = regexp.MustCompile(`^\\x(?:[0-9a-fA-F]{2})*$`)

// cellArgument turns a value from an edit request into what the driver binds.
//
// Three things cannot travel as plain JSON. Bytes: {"$hex": "…"} or
// {"$base64": "…"}, and for a binary column the \x… form the grid itself
// displays, so a value read from a cell can be sent straight back. Structured
// values: an object or an array is bound as its JSON text, which is what a json
// column takes. Exact numbers arrive as json.Number and are bound as their
// digits by SQLValue.
func cellArgument(column Column, driver Driver, v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 1 {
			if raw, ok := t["$hex"].(string); ok {
				b, err := hex.DecodeString(raw)
				if err != nil {
					return nil, fmt.Errorf("column %s: $hex is not hexadecimal", column.Name)
				}
				return b, nil
			}
			if raw, ok := t["$base64"].(string); ok {
				b, err := base64.StdEncoding.DecodeString(raw)
				if err != nil {
					return nil, fmt.Errorf("column %s: $base64 is not base64", column.Name)
				}
				return b, nil
			}
		}
		return jsonArgument(column, t)
	case []any:
		return jsonArgument(column, t)
	case string:
		if ValueKind(driver, column.Type) == KindBinary && hexValuePattern.MatchString(t) {
			b, err := hex.DecodeString(t[2:])
			if err != nil {
				return nil, fmt.Errorf("column %s: %v", column.Name, err)
			}
			return b, nil
		}
	}
	return v, nil
}

func jsonArgument(column Column, v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("column %s: %v", column.Name, err)
	}
	return string(b), nil
}
