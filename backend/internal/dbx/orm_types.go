package dbx

import (
	"strconv"
	"strings"
)

// Column types, read per engine.
//
// Every target needs the same question answered about a column — is it a
// 64-bit integer, a timestamp with a zone, text with a length — and every
// engine spells the answer differently. The mapping used to be one switch on
// the type's first word shared by all engines, which is how "number" on Oracle
// and "datetime2" on SQL Server both came out as String. Each engine has its
// own reader here, and a name a reader does not know stays unknown and is
// reported, rather than being quietly treated as PostgreSQL would treat it.

type ormKind int

const (
	ormUnknown ormKind = iota
	ormBool
	ormInt8
	ormInt16
	ormInt32
	ormInt64
	// ormBigNum is an integer wider than 64 bits (ClickHouse's Int128 and up).
	// Nothing a target can hold it in is a number, so it travels as text.
	ormBigNum
	ormFloat32
	ormFloat64
	ormDecimal
	ormMoney
	ormChar
	ormVarchar
	ormText
	ormUUID
	ormJSON
	ormXML
	ormBytes
	ormDate
	ormTime
	ormDateTime
	ormDateTimeTZ
	ormInterval
	ormYear
	ormEnumKind
	ormSet
	ormBit
	ormNet
	ormGeo
)

// ormType is a column type in the engine-neutral terms the targets map from.
type ormType struct {
	Kind ormKind
	// Name is the engine's own base name in its canonical short spelling
	// ("varchar", "timestamptz", "nvarchar"). Targets that annotate the native
	// type — Prisma's @db.*, TypeORM's column type — read it.
	Name string
	Raw  string
	// Length is a character or byte length; 0 means none was given and -1
	// means the engine's MAX.
	Length    int
	Precision int
	Scale     int
	// HasPrecision distinguishes numeric(10,0) from a bare numeric, and
	// timestamp(3) from timestamp.
	HasPrecision bool
	Unsigned     bool
	// TZ marks a time of day that carries a zone (timetz).
	TZ bool
	// Array marks a PostgreSQL array or a ClickHouse Array(T); the other
	// fields then describe the element.
	Array bool
	// Serial is PostgreSQL's serial family when it reaches here spelt that way.
	Serial bool
	Enum   *ormEnum
	Values []string
}

func (t ormType) isInteger() bool {
	switch t.Kind {
	case ormInt8, ormInt16, ormInt32, ormInt64:
		return true
	}
	return false
}

func (t ormType) isNumber() bool {
	switch t.Kind {
	case ormInt8, ormInt16, ormInt32, ormInt64, ormBigNum, ormFloat32, ormFloat64, ormDecimal, ormMoney, ormYear:
		return true
	}
	return false
}

func (t ormType) isString() bool {
	switch t.Kind {
	case ormChar, ormVarchar, ormText, ormUUID, ormXML, ormNet, ormEnumKind, ormSet:
		return true
	}
	return false
}

// parseORMType reads a column type as its engine reports it.
func parseORMType(driver Driver, raw string) ormType {
	var t ormType
	switch driver {
	case DriverPostgres:
		t = parsePostgresType(raw)
	case DriverMySQL:
		t = parseMySQLType(raw)
	case DriverSQLite:
		t = parseSQLiteType(raw)
	case DriverMSSQL:
		t = parseMSSQLType(raw)
	case DriverOracle:
		t = parseOracleType(raw)
	case DriverClickHouse:
		t = parseClickHouseType(raw)
	}
	t.Raw = raw
	return t
}

// ormTypeParts splits a type into its name and its parenthesised arguments. What
// follows the closing parenthesis belongs to the name, because that is where
// SQL puts it: "timestamp(3) with time zone" is the type "timestamp with time
// zone" with one argument.
func ormTypeParts(raw string) (name string, args []string) {
	s := strings.TrimSpace(raw)
	open := strings.IndexByte(s, '(')
	if open < 0 {
		return strings.Join(strings.Fields(strings.ToLower(s)), " "), nil
	}
	depth, end := 0, -1
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return strings.Join(strings.Fields(strings.ToLower(s)), " "), nil
	}
	name = strings.ToLower(strings.TrimSpace(s[:open]) + " " + strings.TrimSpace(s[end+1:]))
	name = strings.Join(strings.Fields(name), " ")
	return name, ormSplitTypeArgs(s[open+1 : end])
}

// ormSplitTypeArgs splits on the commas that are not inside quotes or nested
// parentheses, so enum('a,b','c') has two arguments and Map(String, Array(Int8))
// has two as well.
func ormSplitTypeArgs(s string) []string {
	var out []string
	var b strings.Builder
	depth := 0
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case quote != 0:
			b.WriteByte(ch)
			if ch == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			} else if ch == quote {
				if i+1 < len(s) && s[i+1] == quote {
					i++
					b.WriteByte(s[i])
				} else {
					quote = 0
				}
			}
		case ch == '\'':
			quote = ch
			b.WriteByte(ch)
		case ch == '(':
			depth++
			b.WriteByte(ch)
		case ch == ')':
			depth--
			b.WriteByte(ch)
		case ch == ',' && depth == 0:
			out = append(out, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteByte(ch)
		}
	}
	if rest := strings.TrimSpace(b.String()); rest != "" || len(out) > 0 {
		out = append(out, rest)
	}
	return out
}

// ormTypeFold changes the case of a type as its engine wrote it, for the
// targets that pass one through, and leaves alone whatever is inside quotes.
// The labels in MySQL's enum('Draft','Published') and a quoted PostgreSQL type
// name are data: folded with the rest they name labels and a type the database
// does not have.
func ormTypeFold(raw string, fold func(string) string) string {
	var b strings.Builder
	start := 0
	for i := 0; i < len(raw); i++ {
		quote := raw[i]
		if quote != '\'' && quote != '"' && quote != '`' {
			continue
		}
		b.WriteString(fold(raw[start:i]))
		start = i
		for i++; i < len(raw) && raw[i] != quote; i++ {
			if raw[i] == '\\' && quote == '\'' {
				// MySQL and ClickHouse escape a quote inside a label this way.
				i++
			}
		}
		if i >= len(raw) {
			// Never closed: nothing after the quote is known to be a keyword.
			return b.String() + raw[start:]
		}
		b.WriteString(raw[start : i+1])
		start = i + 1
	}
	return b.String() + fold(raw[start:])
}

// ormUnquoteSQL reads one single-quoted SQL literal, undoing the doubled
// quote and the backslash escapes MySQL and ClickHouse use.
func ormUnquoteSQL(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return "", false
	}
	body := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		ch := body[i]
		switch {
		case ch == '\'' && i+1 < len(body) && body[i+1] == '\'':
			b.WriteByte('\'')
			i++
		case ch == '\'':
			// A lone quote inside the body means this was not one literal.
			return "", false
		case ch == '\\' && i+1 < len(body):
			i++
			b.WriteByte(body[i])
		default:
			b.WriteByte(ch)
		}
	}
	return b.String(), true
}

func ormAtoi(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

// withArgs fills in length or precision from a type's arguments, by kind.
func (t ormType) withArgs(args []string) ormType {
	if len(args) == 0 {
		return t
	}
	switch t.Kind {
	case ormChar, ormVarchar, ormBytes, ormBit:
		if strings.EqualFold(strings.TrimSpace(args[0]), "max") {
			t.Length = -1
		} else {
			t.Length = ormAtoi(args[0], 0)
		}
	case ormDecimal:
		t.Precision, t.HasPrecision = ormAtoi(args[0], 0), true
		if len(args) > 1 {
			t.Scale = ormAtoi(args[1], 0)
		}
	case ormTime, ormDateTime, ormDateTimeTZ:
		t.Precision, t.HasPrecision = ormAtoi(args[0], 0), true
	}
	return t
}

// --- PostgreSQL -----------------------------------------------------------

// postgresTypes maps every spelling PostgreSQL prints for a built-in type —
// information_schema's long names, format_type's, and the catalogue's short
// ones — onto a kind and the short name.
var postgresTypes = map[string]ormType{
	"boolean": {Kind: ormBool, Name: "bool"}, "bool": {Kind: ormBool, Name: "bool"},
	"smallint": {Kind: ormInt16, Name: "int2"}, "int2": {Kind: ormInt16, Name: "int2"},
	"integer": {Kind: ormInt32, Name: "int4"}, "int": {Kind: ormInt32, Name: "int4"},
	"int4":   {Kind: ormInt32, Name: "int4"},
	"bigint": {Kind: ormInt64, Name: "int8"}, "int8": {Kind: ormInt64, Name: "int8"},
	"smallserial": {Kind: ormInt16, Name: "int2", Serial: true},
	"serial2":     {Kind: ormInt16, Name: "int2", Serial: true},
	"serial":      {Kind: ormInt32, Name: "int4", Serial: true},
	"serial4":     {Kind: ormInt32, Name: "int4", Serial: true},
	"bigserial":   {Kind: ormInt64, Name: "int8", Serial: true},
	"serial8":     {Kind: ormInt64, Name: "int8", Serial: true},
	"oid":         {Kind: ormInt64, Name: "oid"},
	"real":        {Kind: ormFloat32, Name: "float4"}, "float4": {Kind: ormFloat32, Name: "float4"},
	"double precision": {Kind: ormFloat64, Name: "float8"},
	"float8":           {Kind: ormFloat64, Name: "float8"}, "float": {Kind: ormFloat64, Name: "float8"},
	"numeric": {Kind: ormDecimal, Name: "numeric"}, "decimal": {Kind: ormDecimal, Name: "numeric"},
	"money":     {Kind: ormMoney, Name: "money"},
	"character": {Kind: ormChar, Name: "char"}, "char": {Kind: ormChar, Name: "char"},
	"bpchar":            {Kind: ormChar, Name: "char"},
	`"char"`:            {Kind: ormChar, Name: "char", Length: 1},
	"character varying": {Kind: ormVarchar, Name: "varchar"},
	"varchar":           {Kind: ormVarchar, Name: "varchar"},
	"text":              {Kind: ormText, Name: "text"}, "citext": {Kind: ormText, Name: "citext"},
	"name": {Kind: ormText, Name: "name"},
	"uuid": {Kind: ormUUID, Name: "uuid"},
	"json": {Kind: ormJSON, Name: "json"}, "jsonb": {Kind: ormJSON, Name: "jsonb"},
	"xml":   {Kind: ormXML, Name: "xml"},
	"bytea": {Kind: ormBytes, Name: "bytea"},
	"date":  {Kind: ormDate, Name: "date"},
	"time":  {Kind: ormTime, Name: "time"}, "time without time zone": {Kind: ormTime, Name: "time"},
	"timetz":                      {Kind: ormTime, Name: "timetz", TZ: true},
	"time with time zone":         {Kind: ormTime, Name: "timetz", TZ: true},
	"timestamp":                   {Kind: ormDateTime, Name: "timestamp"},
	"timestamp without time zone": {Kind: ormDateTime, Name: "timestamp"},
	"timestamptz":                 {Kind: ormDateTimeTZ, Name: "timestamptz"},
	"timestamp with time zone":    {Kind: ormDateTimeTZ, Name: "timestamptz"},
	"interval":                    {Kind: ormInterval, Name: "interval"},
	"inet":                        {Kind: ormNet, Name: "inet"}, "cidr": {Kind: ormNet, Name: "cidr"},
	"macaddr": {Kind: ormNet, Name: "macaddr"}, "macaddr8": {Kind: ormNet, Name: "macaddr8"},
	"bit": {Kind: ormBit, Name: "bit"}, "bit varying": {Kind: ormBit, Name: "varbit"},
	"varbit": {Kind: ormBit, Name: "varbit"},
	"point":  {Kind: ormGeo, Name: "point"}, "line": {Kind: ormGeo, Name: "line"},
	"lseg": {Kind: ormGeo, Name: "lseg"}, "box": {Kind: ormGeo, Name: "box"},
	"path": {Kind: ormGeo, Name: "path"}, "polygon": {Kind: ormGeo, Name: "polygon"},
	"circle": {Kind: ormGeo, Name: "circle"}, "geometry": {Kind: ormGeo, Name: "geometry"},
	"geography": {Kind: ormGeo, Name: "geography"},
}

func parsePostgresType(raw string) ormType {
	s := strings.TrimSpace(raw)
	array := false
	for strings.HasSuffix(s, "[]") {
		s, array = strings.TrimSpace(strings.TrimSuffix(s, "[]")), true
	}
	// information_schema says only "ARRAY" for an array column and
	// "USER-DEFINED" for an enum, a domain or an extension's type. Neither
	// names the type; both stay unknown unless the catalogue filled them in.
	switch strings.ToUpper(s) {
	case "ARRAY":
		return ormType{Kind: ormUnknown, Array: true}
	case "USER-DEFINED":
		return ormType{Kind: ormUnknown}
	}
	if t, ok := postgresTypes[strings.ToLower(s)]; ok {
		t.Array = array
		return t
	}
	name, args := ormTypeParts(s)
	if t, ok := postgresTypes[name]; ok {
		t = t.withArgs(args)
		t.Array = array
		return t
	}
	// Not a built-in: an enum, a domain, an extension's type. The name is kept
	// as written — case and quotes included — so it can be matched to an enum.
	return ormType{Kind: ormUnknown, Name: s, Array: array}
}

// --- MySQL and MariaDB ----------------------------------------------------

var mysqlTypes = map[string]ormType{
	"tinyint":   {Kind: ormInt8, Name: "tinyint"},
	"smallint":  {Kind: ormInt16, Name: "smallint"},
	"mediumint": {Kind: ormInt32, Name: "mediumint"},
	"int":       {Kind: ormInt32, Name: "int"}, "integer": {Kind: ormInt32, Name: "int"},
	"bigint":  {Kind: ormInt64, Name: "bigint"},
	"bool":    {Kind: ormBool, Name: "tinyint", Length: 1},
	"boolean": {Kind: ormBool, Name: "tinyint", Length: 1},
	"float":   {Kind: ormFloat32, Name: "float"},
	"double":  {Kind: ormFloat64, Name: "double"}, "double precision": {Kind: ormFloat64, Name: "double"},
	"real":    {Kind: ormFloat64, Name: "double"},
	"decimal": {Kind: ormDecimal, Name: "decimal"}, "numeric": {Kind: ormDecimal, Name: "decimal"},
	"dec": {Kind: ormDecimal, Name: "decimal"}, "fixed": {Kind: ormDecimal, Name: "decimal"},
	"bit":        {Kind: ormBit, Name: "bit"},
	"char":       {Kind: ormChar, Name: "char"},
	"varchar":    {Kind: ormVarchar, Name: "varchar"},
	"tinytext":   {Kind: ormText, Name: "tinytext"},
	"text":       {Kind: ormText, Name: "text"},
	"mediumtext": {Kind: ormText, Name: "mediumtext"},
	"longtext":   {Kind: ormText, Name: "longtext"},
	"json":       {Kind: ormJSON, Name: "json"},
	"binary":     {Kind: ormBytes, Name: "binary"},
	"varbinary":  {Kind: ormBytes, Name: "varbinary"},
	"tinyblob":   {Kind: ormBytes, Name: "tinyblob"},
	"blob":       {Kind: ormBytes, Name: "blob"},
	"mediumblob": {Kind: ormBytes, Name: "mediumblob"},
	"longblob":   {Kind: ormBytes, Name: "longblob"},
	"date":       {Kind: ormDate, Name: "date"},
	"time":       {Kind: ormTime, Name: "time"},
	"datetime":   {Kind: ormDateTime, Name: "datetime"},
	// A MySQL TIMESTAMP is stored as UTC and converted to the session's zone,
	// which is an instant, not a wall-clock reading.
	"timestamp": {Kind: ormDateTimeTZ, Name: "timestamp"},
	"year":      {Kind: ormYear, Name: "year"},
	"enum":      {Kind: ormEnumKind, Name: "enum"},
	"set":       {Kind: ormSet, Name: "set"},
	"uuid":      {Kind: ormUUID, Name: "uuid"},
	"inet4":     {Kind: ormNet, Name: "inet4"}, "inet6": {Kind: ormNet, Name: "inet6"},
	"geometry": {Kind: ormGeo, Name: "geometry"}, "point": {Kind: ormGeo, Name: "point"},
	"linestring": {Kind: ormGeo, Name: "linestring"}, "polygon": {Kind: ormGeo, Name: "polygon"},
	"multipoint":         {Kind: ormGeo, Name: "multipoint"},
	"multilinestring":    {Kind: ormGeo, Name: "multilinestring"},
	"multipolygon":       {Kind: ormGeo, Name: "multipolygon"},
	"geometrycollection": {Kind: ormGeo, Name: "geometrycollection"},
	"geomcollection":     {Kind: ormGeo, Name: "geometrycollection"},
}

func parseMySQLType(raw string) ormType {
	name, args := ormTypeParts(raw)
	unsigned := false
	for _, word := range []string{" zerofill", " unsigned", " signed"} {
		if strings.Contains(name, word) {
			unsigned = unsigned || word != " signed"
			name = strings.TrimSpace(strings.ReplaceAll(name, word, ""))
		}
	}
	t, ok := mysqlTypes[name]
	if !ok {
		return ormType{Kind: ormUnknown, Name: name}
	}
	t.Unsigned = unsigned
	switch t.Kind {
	case ormEnumKind, ormSet:
		for _, a := range args {
			if v, ok := ormUnquoteSQL(a); ok {
				t.Values = append(t.Values, v)
			}
		}
		return t
	case ormInt8:
		// tinyint(1) is how MySQL spells a boolean, and the display width is
		// the only place it says so. Any other width is a small integer.
		if len(args) == 1 && strings.TrimSpace(args[0]) == "1" && !unsigned {
			t.Kind, t.Length = ormBool, 1
		}
		return t
	case ormInt16, ormInt32, ormInt64, ormYear:
		// A display width on an integer is not a size.
		return t
	case ormBit:
		t.Length = 1
		if len(args) > 0 {
			t.Length = ormAtoi(args[0], 1)
		}
		if t.Length == 1 {
			t.Kind = ormBool
		}
		return t
	case ormFloat32, ormFloat64:
		return t
	}
	return t.withArgs(args)
}

// --- SQLite ---------------------------------------------------------------

var sqliteTypes = map[string]ormType{
	// INTEGER can hold 64 bits, but it is what every SQLite schema calls an
	// ordinary integer and what its drivers hand back as a plain number; only a
	// column declared BIGINT is treated as needing more than 53 of them.
	"integer": {Kind: ormInt32, Name: "integer"}, "int": {Kind: ormInt32, Name: "integer"},
	"tinyint": {Kind: ormInt8, Name: "integer"}, "smallint": {Kind: ormInt16, Name: "integer"},
	"mediumint": {Kind: ormInt32, Name: "integer"}, "bigint": {Kind: ormInt64, Name: "integer"},
	"int2": {Kind: ormInt16, Name: "integer"}, "int8": {Kind: ormInt64, Name: "integer"},
	"unsigned big int": {Kind: ormInt64, Name: "integer", Unsigned: true},
	"boolean":          {Kind: ormBool, Name: "boolean"}, "bool": {Kind: ormBool, Name: "boolean"},
	"real": {Kind: ormFloat64, Name: "real"}, "double": {Kind: ormFloat64, Name: "real"},
	"double precision": {Kind: ormFloat64, Name: "real"}, "float": {Kind: ormFloat64, Name: "real"},
	"numeric": {Kind: ormDecimal, Name: "numeric"}, "decimal": {Kind: ormDecimal, Name: "numeric"},
	"text": {Kind: ormText, Name: "text"}, "clob": {Kind: ormText, Name: "text"},
	"character": {Kind: ormChar, Name: "text"}, "char": {Kind: ormChar, Name: "text"},
	"nchar": {Kind: ormChar, Name: "text"}, "native character": {Kind: ormChar, Name: "text"},
	"varchar": {Kind: ormVarchar, Name: "text"}, "nvarchar": {Kind: ormVarchar, Name: "text"},
	"varying character": {Kind: ormVarchar, Name: "text"},
	"string":            {Kind: ormText, Name: "text"},
	"blob":              {Kind: ormBytes, Name: "blob"},
	"date":              {Kind: ormDate, Name: "date"},
	"time":              {Kind: ormTime, Name: "time"},
	"datetime":          {Kind: ormDateTime, Name: "datetime"},
	"timestamp":         {Kind: ormDateTime, Name: "datetime"},
	"json":              {Kind: ormJSON, Name: "json"}, "jsonb": {Kind: ormJSON, Name: "json"},
	"uuid": {Kind: ormUUID, Name: "text"},
}

// parseSQLiteType reads a declared type. SQLite accepts any text there, so the
// names a schema is likely to use are looked up first and anything else falls
// to the rule SQLite itself applies to decide a column's affinity (section 3.1
// of its datatype documentation) — the engine's own answer, not a guess.
func parseSQLiteType(raw string) ormType {
	name, args := ormTypeParts(raw)
	if t, ok := sqliteTypes[name]; ok {
		return t.withArgs(args)
	}
	upper := strings.ToUpper(raw)
	switch {
	case strings.Contains(upper, "INT"):
		return ormType{Kind: ormInt32, Name: "integer"}
	case strings.Contains(upper, "CHAR"), strings.Contains(upper, "CLOB"), strings.Contains(upper, "TEXT"):
		return ormType{Kind: ormText, Name: "text"}
	case strings.Contains(upper, "BLOB"), strings.TrimSpace(raw) == "":
		return ormType{Kind: ormBytes, Name: "blob"}
	case strings.Contains(upper, "REAL"), strings.Contains(upper, "FLOA"), strings.Contains(upper, "DOUB"):
		return ormType{Kind: ormFloat64, Name: "real"}
	}
	return ormType{Kind: ormDecimal, Name: "numeric"}
}

// --- SQL Server -----------------------------------------------------------

var mssqlTypes = map[string]ormType{
	"bit": {Kind: ormBool, Name: "bit"},
	// tinyint is 0 to 255 on SQL Server: one byte, unsigned.
	"tinyint":  {Kind: ormInt8, Name: "tinyint", Unsigned: true},
	"smallint": {Kind: ormInt16, Name: "smallint"},
	"int":      {Kind: ormInt32, Name: "int"},
	"bigint":   {Kind: ormInt64, Name: "bigint"},
	"decimal":  {Kind: ormDecimal, Name: "decimal"}, "numeric": {Kind: ormDecimal, Name: "numeric"},
	"money": {Kind: ormMoney, Name: "money"}, "smallmoney": {Kind: ormMoney, Name: "smallmoney"},
	"float": {Kind: ormFloat64, Name: "float"}, "real": {Kind: ormFloat32, Name: "real"},
	"date":             {Kind: ormDate, Name: "date"},
	"time":             {Kind: ormTime, Name: "time"},
	"datetime":         {Kind: ormDateTime, Name: "datetime"},
	"datetime2":        {Kind: ormDateTime, Name: "datetime2"},
	"smalldatetime":    {Kind: ormDateTime, Name: "smalldatetime"},
	"datetimeoffset":   {Kind: ormDateTimeTZ, Name: "datetimeoffset"},
	"char":             {Kind: ormChar, Name: "char"},
	"nchar":            {Kind: ormChar, Name: "nchar"},
	"varchar":          {Kind: ormVarchar, Name: "varchar"},
	"nvarchar":         {Kind: ormVarchar, Name: "nvarchar"},
	"text":             {Kind: ormText, Name: "text"},
	"ntext":            {Kind: ormText, Name: "ntext"},
	"binary":           {Kind: ormBytes, Name: "binary"},
	"varbinary":        {Kind: ormBytes, Name: "varbinary"},
	"image":            {Kind: ormBytes, Name: "image"},
	"timestamp":        {Kind: ormBytes, Name: "rowversion"},
	"rowversion":       {Kind: ormBytes, Name: "rowversion"},
	"uniqueidentifier": {Kind: ormUUID, Name: "uniqueidentifier"},
	"xml":              {Kind: ormXML, Name: "xml"},
	"geometry":         {Kind: ormGeo, Name: "geometry"},
	"geography":        {Kind: ormGeo, Name: "geography"},
}

func parseMSSQLType(raw string) ormType {
	name, args := ormTypeParts(raw)
	t, ok := mssqlTypes[name]
	if !ok {
		return ormType{Kind: ormUnknown, Name: name}
	}
	if t.Kind == ormFloat64 || t.Kind == ormFloat32 {
		return t
	}
	return t.withArgs(args)
}

// --- Oracle ---------------------------------------------------------------

var oracleTypes = map[string]ormType{
	"varchar2": {Kind: ormVarchar, Name: "varchar2"}, "varchar": {Kind: ormVarchar, Name: "varchar2"},
	"nvarchar2": {Kind: ormVarchar, Name: "nvarchar2"},
	"char":      {Kind: ormChar, Name: "char"}, "nchar": {Kind: ormChar, Name: "nchar"},
	"clob": {Kind: ormText, Name: "clob"}, "nclob": {Kind: ormText, Name: "nclob"},
	"long": {Kind: ormText, Name: "long"},
	"blob": {Kind: ormBytes, Name: "blob"}, "raw": {Kind: ormBytes, Name: "raw"},
	"long raw": {Kind: ormBytes, Name: "long raw"}, "bfile": {Kind: ormBytes, Name: "bfile"},
	"binary_float":  {Kind: ormFloat32, Name: "binary_float"},
	"binary_double": {Kind: ormFloat64, Name: "binary_double"},
	"float":         {Kind: ormFloat64, Name: "float"},
	// An Oracle DATE carries a time of day to the second.
	"date":                           {Kind: ormDateTime, Name: "date"},
	"timestamp":                      {Kind: ormDateTime, Name: "timestamp"},
	"timestamp with time zone":       {Kind: ormDateTimeTZ, Name: "timestamp with time zone"},
	"timestamp with local time zone": {Kind: ormDateTimeTZ, Name: "timestamp with local time zone"},
	"interval year to month":         {Kind: ormInterval, Name: "interval year to month"},
	"interval day to second":         {Kind: ormInterval, Name: "interval day to second"},
	"rowid":                          {Kind: ormText, Name: "rowid"}, "urowid": {Kind: ormText, Name: "urowid"},
	"xmltype": {Kind: ormXML, Name: "xmltype"},
	"json":    {Kind: ormJSON, Name: "json"},
	"boolean": {Kind: ormBool, Name: "boolean"},
}

func parseOracleType(raw string) ormType {
	name, args := ormTypeParts(raw)
	if name == "number" || name == "integer" || name == "int" || name == "smallint" {
		return oracleNumber(name, args)
	}
	// INTERVAL DAY(2) TO SECOND(6) has two argument lists; ormTypeParts consumed
	// the first, and the second is still in the name.
	if strings.HasPrefix(name, "interval") {
		if i := strings.IndexByte(name, '('); i >= 0 {
			name = strings.TrimSpace(name[:i])
		}
	}
	t, ok := oracleTypes[name]
	if !ok {
		return ormType{Kind: ormUnknown, Name: name}
	}
	if t.Kind == ormFloat64 || t.Kind == ormFloat32 || t.Kind == ormInterval {
		return t
	}
	return t.withArgs(args)
}

// oracleNumber sizes a NUMBER. Oracle has one numeric type, so whether a column
// is an integer is read from its scale and how wide from its precision: nine
// digits always fit 32 bits and eighteen always fit 64.
func oracleNumber(name string, args []string) ormType {
	if name != "number" {
		return ormType{Kind: ormDecimal, Name: "number", Precision: 38, HasPrecision: true}
	}
	if len(args) == 0 {
		return ormType{Kind: ormDecimal, Name: "number"}
	}
	precision := ormAtoi(args[0], 0)
	scale := 0
	if len(args) > 1 {
		scale = ormAtoi(args[1], 0)
	}
	t := ormType{Name: "number", Precision: precision, Scale: scale, HasPrecision: true}
	switch {
	case scale != 0 || precision == 0 || precision > 18:
		t.Kind = ormDecimal
	case precision <= 9:
		t.Kind = ormInt32
	default:
		t.Kind = ormInt64
	}
	return t
}

// --- ClickHouse -----------------------------------------------------------

var clickhouseTypes = map[string]ormType{
	"bool": {Kind: ormBool, Name: "bool"},
	"int8": {Kind: ormInt8, Name: "int8"}, "int16": {Kind: ormInt16, Name: "int16"},
	"int32": {Kind: ormInt32, Name: "int32"}, "int64": {Kind: ormInt64, Name: "int64"},
	"int128": {Kind: ormBigNum, Name: "int128"}, "int256": {Kind: ormBigNum, Name: "int256"},
	"uint8":   {Kind: ormInt8, Name: "uint8", Unsigned: true},
	"uint16":  {Kind: ormInt16, Name: "uint16", Unsigned: true},
	"uint32":  {Kind: ormInt32, Name: "uint32", Unsigned: true},
	"uint64":  {Kind: ormInt64, Name: "uint64", Unsigned: true},
	"uint128": {Kind: ormBigNum, Name: "uint128", Unsigned: true},
	"uint256": {Kind: ormBigNum, Name: "uint256", Unsigned: true},
	"float32": {Kind: ormFloat32, Name: "float32"}, "float64": {Kind: ormFloat64, Name: "float64"},
	"decimal": {Kind: ormDecimal, Name: "decimal"},
	"string":  {Kind: ormText, Name: "string"}, "fixedstring": {Kind: ormChar, Name: "fixedstring"},
	"uuid": {Kind: ormUUID, Name: "uuid"},
	"date": {Kind: ormDate, Name: "date"}, "date32": {Kind: ormDate, Name: "date32"},
	// A ClickHouse DateTime is a Unix timestamp: an instant, shown in a zone.
	"datetime":   {Kind: ormDateTimeTZ, Name: "datetime"},
	"datetime64": {Kind: ormDateTimeTZ, Name: "datetime64"},
	"enum8":      {Kind: ormEnumKind, Name: "enum8"}, "enum16": {Kind: ormEnumKind, Name: "enum16"},
	"enum": {Kind: ormEnumKind, Name: "enum8"},
	"ipv4": {Kind: ormNet, Name: "ipv4"}, "ipv6": {Kind: ormNet, Name: "ipv6"},
	"json": {Kind: ormJSON, Name: "json"}, "object": {Kind: ormJSON, Name: "json"},
	"map": {Kind: ormJSON, Name: "map"}, "tuple": {Kind: ormJSON, Name: "tuple"},
	"nested": {Kind: ormJSON, Name: "nested"}, "variant": {Kind: ormJSON, Name: "variant"},
	"dynamic": {Kind: ormJSON, Name: "dynamic"},
	"point":   {Kind: ormGeo, Name: "point"}, "ring": {Kind: ormGeo, Name: "ring"},
	"polygon": {Kind: ormGeo, Name: "polygon"}, "multipolygon": {Kind: ormGeo, Name: "multipolygon"},
}

func parseClickHouseType(raw string) ormType {
	s := strings.TrimSpace(raw)
	// Nullable and LowCardinality wrap a type without changing what it holds;
	// the column's nullability is carried separately.
	for {
		name, args := ormTypeParts(s)
		if (name == "nullable" || name == "lowcardinality") && len(args) == 1 {
			s = args[0]
			continue
		}
		break
	}
	name, args := ormTypeParts(s)
	if name == "array" && len(args) == 1 {
		el := parseClickHouseType(args[0])
		if el.Array {
			// An array of arrays has no flat element type; it is JSON-shaped.
			return ormType{Kind: ormJSON, Name: "array"}
		}
		el.Array = true
		return el
	}
	// Decimal32(S), Decimal64(S), Decimal128(S), Decimal256(S) fix the
	// precision in the name and take only the scale.
	if strings.HasPrefix(name, "decimal") && name != "decimal" {
		precision := map[string]int{"decimal32": 9, "decimal64": 18, "decimal128": 38, "decimal256": 76}[name]
		if precision > 0 {
			t := ormType{Kind: ormDecimal, Name: "decimal", Precision: precision, HasPrecision: true}
			if len(args) > 0 {
				t.Scale = ormAtoi(args[0], 0)
			}
			return t
		}
	}
	t, ok := clickhouseTypes[name]
	if !ok {
		return ormType{Kind: ormUnknown, Name: name}
	}
	switch t.Kind {
	case ormEnumKind:
		// Enum8('a' = 1, 'b' = 2): the label is what a row holds.
		for _, a := range args {
			label := a
			if i := strings.LastIndex(a, "="); i >= 0 {
				label = a[:i]
			}
			if v, ok := ormUnquoteSQL(label); ok {
				t.Values = append(t.Values, v)
			}
		}
	case ormChar:
		if len(args) > 0 {
			t.Length = ormAtoi(args[0], 0)
		}
	case ormDecimal:
		t = t.withArgs(args)
	case ormDateTimeTZ:
		if t.Name == "datetime64" && len(args) > 0 {
			t.Precision, t.HasPrecision = ormAtoi(args[0], 0), true
		}
	}
	return t
}
