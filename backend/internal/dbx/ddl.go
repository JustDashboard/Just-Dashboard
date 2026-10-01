package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// DDL is the one place in this package that assembles a statement whose
// *structure* the operator chose, rather than only its values. A CREATE TABLE
// cannot bind anything: the table name, the column names, the type names and
// the defaults are all syntax. So every fragment is validated before it is
// concatenated, and the validation is by shape rather than by escaping —
// there is nothing to escape a type name into.
//
// The rules below are deliberately narrow. They accept what a schema form can
// legitimately produce and nothing else; anything more exotic belongs in the
// Query tab, where the statement is classified and audited as the arbitrary
// SQL it is. Refusing an unusual-but-valid type is a nuisance; accepting a
// crafted one is a shell on the database host.
//
// Every change is planned before it is run. The plan is the exact text that
// will be sent, which is what lets a form show the operator the server's
// statement rather than a client-side guess at it, and what lets the same
// function serve the preview and the execution without the two drifting apart.

// DDLPlan is one schema change rendered for one engine: the statements it
// takes, in the order they run.
type DDLPlan struct {
	Statements []string `json:"statements"`
	// Unvouched names the functions the request's own SQL calls that are not
	// on the short list of ones known to compute a value and do nothing else.
	// The engine evaluates a CHECK condition against every existing row and a
	// default for every new one, so a call in either is a call that runs — and
	// what an arbitrary function does when it runs cannot be read off its
	// name. A plan with any is one the caller has to be allowed to run as the
	// arbitrary SQL it is.
	Unvouched []string `json:"-"`
	// before is what a dialect needs done ahead of the statements. It runs
	// only when the plan is executed, so planning a change to show it never
	// touches the database.
	before func(ctx context.Context, db *sql.DB) error
}

func planOf(statements ...string) *DDLPlan { return &DDLPlan{Statements: statements} }

// vouching returns the plan with the calls found in the request's free SQL
// recorded on it.
func (p *DDLPlan) vouching(calls ...[]string) *DDLPlan {
	for _, list := range calls {
		p.Unvouched = append(p.Unvouched, list...)
	}
	return p
}

// String is the plan as one text, for showing and for the audit entry.
func (p *DDLPlan) String() string {
	if p == nil {
		return ""
	}
	return strings.Join(p.Statements, ";\n")
}

// Exec runs the plan. More than one statement runs on one connection in
// order and stops at the first failure: a change whose second step failed
// says which step, because what is left behind depends on it.
func (p *DDLPlan) Exec(ctx context.Context, db *sql.DB) error {
	if p == nil || len(p.Statements) == 0 {
		return fmt.Errorf("nothing to run")
	}
	if p.before != nil {
		if err := p.before(ctx, db); err != nil {
			return err
		}
	}
	if len(p.Statements) == 1 {
		_, err := db.ExecContext(ctx, p.Statements[0])
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	for i, stmt := range p.Statements {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("statement %d of %d failed: %w", i+1, len(p.Statements), err)
		}
	}
	return nil
}

// run executes a plan and returns its text either way, so a failed change
// still reports the statement the engine refused.
func (p *DDLPlan) run(ctx context.Context, db *sql.DB) (string, error) {
	return p.String(), p.Exec(ctx, db)
}

// --- what each engine can be asked to do ------------------------------------

// Schema operations, named as the /drivers catalogue advertises them.
const (
	OpCreateTable    = "createTable"
	OpDropTable      = "dropTable"
	OpTruncate       = "truncate"
	OpAddColumn      = "addColumn"
	OpDropColumn     = "dropColumn"
	OpRenameColumn   = "renameColumn"
	OpRenameTable    = "renameTable"
	OpAlterColumn    = "alterColumn"
	OpCreateIndex    = "createIndex"
	OpDropIndex      = "dropIndex"
	OpForeignKeys    = "foreignKeys"
	OpUnique         = "uniqueConstraints"
	OpCheck          = "checkConstraints"
	OpViews          = "views"
	OpMaterialized   = "materializedViews"
	OpSchemas        = "schemas"
	OpCommentTable   = "tableComments"
	OpCommentColumn  = "columnComments"
	OpEnumTypes      = "enumTypes"
	OpIndexMethod    = "indexMethod"
	OpIndexPartial   = "indexPartial"
	OpIndexIfMissing = "indexIfNotExists"
	OpIndexOnline    = "indexConcurrently"
)

// ddlAll is what an engine with ordinary relational DDL supports; the per-engine
// tables below take away what theirs does not.
var ddlAll = []string{
	OpCreateTable, OpDropTable, OpTruncate, OpAddColumn, OpDropColumn, OpRenameColumn, OpRenameTable,
	OpAlterColumn, OpCreateIndex, OpDropIndex, OpForeignKeys, OpUnique, OpCheck, OpViews,
	OpCommentTable, OpCommentColumn,
}

// ddlRefusals says why an engine cannot do an operation. An operation with no
// entry is supported. The reason is the message: "SQLite cannot …" tells the
// operator it is the engine and not the form, which is the difference between
// looking for another way and reporting a bug.
var ddlRefusals = map[Driver]map[string]string{
	DriverPostgres: {},
	DriverMySQL: {
		OpMaterialized: "MySQL has no materialized views",
		OpSchemas:      "on MySQL a schema is a database: create or drop it from the server's database list",
		OpEnumTypes:    "MySQL has no enum types; an enum is declared on the column, as enum('a','b')",
		OpIndexPartial: "MySQL has no partial indexes",
		OpIndexOnline:  "MySQL builds an InnoDB index without blocking writes by itself; it has no CONCURRENTLY",
	},
	DriverSQLite: {
		OpAlterColumn: "SQLite cannot change a column's type, nullability or default in place: its ALTER TABLE only " +
			"renames, adds and drops. Rebuild the table from the Query tab — create the new shape, copy the rows, swap the names",
		OpForeignKeys: "SQLite cannot add or drop a foreign key on an existing table: a foreign key is part of the " +
			"CREATE TABLE statement. Rebuild the table from the Query tab",
		OpUnique: "SQLite cannot add a unique constraint to an existing table. A unique index does the same job: " +
			"create one instead",
		OpCheck: "SQLite cannot add or drop a check constraint on an existing table: a check is part of the " +
			"CREATE TABLE statement. Rebuild the table from the Query tab",
		OpMaterialized:  "SQLite has no materialized views",
		OpSchemas:       "SQLite has no schemas: a database file is one, and another file is attached rather than created",
		OpCommentTable:  "SQLite stores no comments on tables or columns",
		OpCommentColumn: "SQLite stores no comments on tables or columns",
		OpEnumTypes:     "SQLite has no enum types",
		OpIndexMethod:   "SQLite has one kind of index; there is no method to choose",
		OpIndexOnline:   "SQLite has no CONCURRENTLY: the file is locked for the length of the build",
	},
	DriverMSSQL: {
		OpMaterialized:   "SQL Server has no materialized views; an indexed view is built from the Query tab",
		OpEnumTypes:      "SQL Server has no enum types",
		OpIndexIfMissing: "SQL Server has no CREATE INDEX IF NOT EXISTS",
	},
	DriverOracle: {
		OpMaterialized:   "materialized views on Oracle take refresh options this form does not choose; create one from the Query tab",
		OpSchemas:        "on Oracle a schema is a user: create or drop it as an account",
		OpEnumTypes:      "Oracle has no enum types",
		OpIndexPartial:   "Oracle has no partial indexes",
		OpIndexIfMissing: "Oracle has no CREATE INDEX IF NOT EXISTS before 23ai",
	},
	DriverClickHouse: {
		OpCreateTable: "ClickHouse tables need an engine and sorting key that this form cannot choose for you; " +
			"create them from the Query tab",
		OpCreateIndex: "a ClickHouse data-skipping index needs a type and a granularity this form cannot choose for you; " +
			"add one from the Query tab",
		OpForeignKeys:    "ClickHouse has no foreign keys",
		OpUnique:         "ClickHouse has no unique constraints: its sorting key orders rows and does not make them unique",
		OpMaterialized:   "a ClickHouse materialized view writes into a target table this form cannot choose; create one from the Query tab",
		OpSchemas:        "on ClickHouse a schema is a database: create or drop it from the server's database list",
		OpEnumTypes:      "ClickHouse has no enum types; an enum is declared on the column, as Enum8('a' = 1)",
		OpIndexMethod:    "a ClickHouse data-skipping index is added from the Query tab",
		OpIndexPartial:   "a ClickHouse data-skipping index is added from the Query tab",
		OpIndexIfMissing: "a ClickHouse data-skipping index is added from the Query tab",
		OpIndexOnline:    "a ClickHouse data-skipping index is added from the Query tab",
	},
}

// ddlEverything lists every operation, so DDLOperations can answer by
// subtraction and a new operation is advertised for every engine that has not
// said it cannot.
var ddlEverything = append(append([]string{}, ddlAll...),
	OpMaterialized, OpSchemas, OpEnumTypes, OpIndexMethod, OpIndexPartial, OpIndexIfMissing, OpIndexOnline)

// DDLOperations lists the schema operations an engine supports, sorted. It is
// what the driver catalogue advertises, so a form is never drawn for a change
// the route would refuse. flavor is the product behind the driver where one
// driver serves several; an empty flavor is the driver's own.
func DDLOperations(driver Driver, flavor string) []string {
	refusals, ok := ddlRefusals[driver]
	if !ok {
		return []string{}
	}
	out := []string{}
	for _, op := range ddlEverything {
		if _, refused := refusals[op]; refused {
			continue
		}
		// The one operation the two products behind the MySQL driver disagree
		// on: CREATE INDEX IF NOT EXISTS is MariaDB's.
		if op == OpIndexIfMissing && driver == DriverMySQL && !strings.EqualFold(flavor, "mariadb") {
			continue
		}
		out = append(out, op)
	}
	sort.Strings(out)
	return out
}

// SupportsOperation reports whether an engine can be asked for an operation.
func SupportsOperation(driver Driver, op string) bool {
	refusals, ok := ddlRefusals[driver]
	if !ok {
		return false
	}
	_, refused := refusals[op]
	return !refused
}

// ddlDialect returns the dialect for an operation, or the reason the engine
// cannot do it. Every planner starts here, so no route can send an engine a
// statement written for another one.
func ddlDialect(driver Driver, op string) (Dialect, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	if reason, refused := ddlRefusals[driver][op]; refused {
		return nil, fmt.Errorf("%s", reason)
	}
	return d, nil
}

// --- validation -------------------------------------------------------------

// typeWordsRefused are words that end a type and start a constraint. A type
// name is a run of words, so without this a "type" of `int references users`
// would ride a foreign key in on the add-column form.
var typeWordsRefused = map[string]bool{
	"primary": true, "key": true, "references": true, "unique": true, "check": true,
	"default": true, "not": true, "null": true, "constraint": true, "generated": true,
	"as": true, "collate": true, "auto_increment": true, "autoincrement": true, "identity": true,
	"comment": true, "on": true, "foreign": true, "index": true, "codec": true, "ttl": true,
	"materialized": true, "alias": true,
}

// validateType accepts a SQL type name: words, an optional argument list that
// may nest (Array(Nullable(String))) and may hold quoted labels
// (enum('a','b')), an optional qualifier after it (timestamp(3) with time
// zone) and optional array brackets (text[]). It admits no semicolon, no
// comment introducer and no backslash, so a type cannot carry a second
// statement, and no constraint keyword, so it cannot carry a clause either.
func validateType(t string) error {
	t = strings.TrimSpace(t)
	if t == "" {
		return fmt.Errorf("a column type is required")
	}
	refuse := func() error {
		return fmt.Errorf("column type %q is not one this form can build; use the Query tab for it", t)
	}
	if len(t) > 200 || !isASCIILetter(t[0]) {
		return refuse()
	}
	var (
		depth   int
		inQuote bool
		word    strings.Builder
	)
	endWord := func() bool {
		ok := !typeWordsRefused[strings.ToLower(word.String())]
		word.Reset()
		return ok
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if inQuote {
			switch {
			case c == '\'':
				inQuote = false
			case isASCIILetter(c) || isASCIIDigit(c) || strings.IndexByte("_ .:/+-", c) >= 0:
			default:
				return refuse()
			}
			continue
		}
		switch {
		case isASCIILetter(c) || isASCIIDigit(c) || c == '_':
			word.WriteByte(c)
			continue
		case c == ' ' || c == ',' || c == '.' || c == '=':
		case c == '(':
			if depth++; depth > 4 {
				return refuse()
			}
		case c == ')':
			if depth--; depth < 0 {
				return refuse()
			}
		case c == '\'':
			// A label only makes sense inside an argument list.
			if depth == 0 {
				return refuse()
			}
			inQuote = true
		case c == '[':
			// Array brackets close the type: `text[]`, `integer[3][]`.
			rest := strings.TrimSpace(t[i:])
			if depth != 0 || !arraySuffixRe.MatchString(rest) {
				return refuse()
			}
			if !endWord() {
				return refuse()
			}
			return nil
		default:
			return refuse()
		}
		if !endWord() {
			return refuse()
		}
	}
	if depth != 0 || inQuote || !endWord() {
		return refuse()
	}
	return nil
}

var arraySuffixRe = regexp.MustCompile(`^(\[[0-9]{0,6}\])+$`)

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isASCIIDigit(c byte) bool  { return c >= '0' && c <= '9' }

// defaultRe accepts a literal default: a number, a single-quoted string with no
// embedded quote or backslash, one of the bare keywords engines share, or a
// function call with no arguments (gen_random_uuid(), now(), NEWID()).
//
// The backslash is refused because it is an escape inside a string on MySQL
// and ClickHouse: `'a\'` there is an unterminated literal that swallows the
// rest of the statement.
var defaultRe = regexp.MustCompile(`^(-?[0-9]+(\.[0-9]+)?|'[^'\\]*'|NULL|TRUE|FALSE|CURRENT_TIMESTAMP|CURRENT_DATE|CURRENT_TIME|CURRENT_USER|LOCALTIMESTAMP|SYSDATE|[A-Za-z_][A-Za-z0-9_]{0,62}\(\))$`)

func validateDefault(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if !defaultRe.MatchString(strings.ToUpper(v)) && !defaultRe.MatchString(v) {
		return fmt.Errorf("default %q is not a plain literal or a function call with no arguments; use the Query tab for an expression default", v)
	}
	return nil
}

// pureFunctions are the calls a form vouches for: they compute a value from
// their arguments and touch nothing. The list is short on purpose. It is not
// an attempt to enumerate every harmless function in six engines — it is the
// ones a check condition or a partial index actually uses, and anything else
// is not refused but handed to the rule for arbitrary SQL. Type names are here
// because `CAST(x AS varchar(10))` spells one like a call.
var pureFunctions = stringSet(
	"abs", "array_length", "bit_length", "btrim", "cardinality", "cast", "ceil", "ceiling", "char_length",
	"character_length", "charindex", "coalesce", "concat", "concat_ws", "convert", "datalength", "date",
	"date_part", "date_trunc", "dateadd", "datediff", "datepart", "day", "empty", "exp", "extract", "floor",
	"greatest", "hour", "if", "ifnull", "initcap", "instr", "isdate", "isnull", "isnumeric", "json_array_length",
	"json_typeof", "json_valid", "jsonb_array_length", "jsonb_typeof", "least", "left", "len", "length",
	"lengthutf8", "ln", "locate", "log", "log10", "lower", "lpad", "ltrim", "minute", "mod", "month",
	"notempty", "nullif", "num_nonnulls", "num_nulls", "nvl", "nvl2", "octet_length", "patindex", "position",
	"pow", "power", "regexp_like", "replace", "reverse", "right", "round", "rpad", "rtrim", "second", "sign",
	"sqrt", "starts_with", "strpos", "substr", "substring", "time", "to_char", "to_date", "to_number",
	"to_timestamp", "todate", "todatetime", "tostring", "toyear", "toyyyymm", "trim", "trunc", "truncate",
	"try_cast", "try_convert", "upper", "year",
	// types, as they appear inside a cast
	"binary", "bit", "char", "character", "datetime2", "decimal", "float", "interval", "nchar", "number",
	"numeric", "nvarchar", "timestamp", "varbinary", "varchar", "varchar2", "varying",
)

// fragmentKeywords are the words that are followed by a parenthesis without
// being a call: `x IN (…)`, `NOT (…)`, `CASE WHEN (…)`.
var fragmentKeywords = stringSet(
	"all", "and", "any", "as", "between", "case", "else", "exists", "filter", "from", "ilike", "in", "is",
	"like", "not", "on", "or", "over", "select", "similar", "some", "then", "to", "using", "values", "when",
	"where",
)

// safeDefaults are the functions with no arguments that a column default may
// call on a form's say-so: the clock, the session's user, and a fresh
// identifier. Any other is still a valid default — and is a function the
// engine will run for every row, so it is planned as unvouched.
var safeDefaults = stringSet(
	"clock_timestamp", "curdate", "current_date", "current_time", "current_timestamp", "current_user",
	"curtime", "gen_random_uuid", "generateuuidv4", "getdate", "getutcdate", "localtime", "localtimestamp",
	"newid", "newsequentialid", "now", "rand", "random", "session_user", "statement_timestamp", "sys_guid",
	"sysdate", "sysdatetime", "sysdatetimeoffset", "systimestamp", "sysutcdatetime", "today",
	"transaction_timestamp", "unix_timestamp", "utc_timestamp", "uuid", "uuid_generate_v4", "uuidv4", "uuidv7",
	"yesterday",
)

func stringSet(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		out[v] = true
	}
	return out
}

// unvouchedCalls lists the functions a validated fragment calls that are not
// in pureFunctions. A name counts as a call when a parenthesis follows it,
// whether it is written bare, qualified or quoted: `"lo_unlink"(1)` and
// `pg_catalog.lo_unlink(1)` are the same call as `lo_unlink(1)`, and a
// qualified name is never vouched for — `public.lower` is not `lower`.
func unvouchedCalls(driver Driver, text string) []string {
	quotes := fragmentQuotes(driver)
	var out []string
	seen := map[string]bool{}
	note := func(name string, vouched bool) {
		if !vouched && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	followedByCall := func(i int) bool {
		for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\n' || text[i] == '\r') {
			i++
		}
		return i < len(text) && text[i] == '('
	}
	for i := 0; i < len(text); {
		c := text[i]
		if closer, ok := quotes[c]; ok {
			start := i
			for i++; i < len(text); i++ {
				if text[i] == closer {
					if i+1 < len(text) && text[i+1] == closer {
						i++
						continue
					}
					break
				}
			}
			i++
			// A quoted name in front of a parenthesis is a call by a name
			// this list was never going to contain.
			if c != '\'' && followedByCall(i) {
				note(text[start:min(i, len(text))], false)
			}
			continue
		}
		// A byte past ASCII is part of a name too: every engine here allows
		// letters outside it, and a function named with them is still a call.
		if !isASCIILetter(c) && c != '_' && c < 0x80 {
			i++
			continue
		}
		start := i
		for i < len(text) && (isASCIILetter(text[i]) || isASCIIDigit(text[i]) || text[i] == '_' ||
			text[i] == '.' || text[i] == '$' || text[i] >= 0x80) {
			i++
		}
		name := strings.ToLower(text[start:i])
		if followedByCall(i) && !fragmentKeywords[name] {
			note(name, pureFunctions[name])
		}
	}
	return out
}

// unvouchedDefault reports a function default that is not one of the known
// harmless ones.
func unvouchedDefault(def string) []string {
	def = strings.TrimSpace(def)
	if !functionDefaultRe.MatchString(def) {
		return nil
	}
	name := strings.ToLower(strings.TrimSuffix(def, "()"))
	if safeDefaults[name] {
		return nil
	}
	return []string{name}
}

var functionDefaultRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\(\)$`)

// renderDefault writes a validated default the way the engine wants it. MySQL
// takes a function call as a default only inside parentheses — NOW() being the
// one it has always allowed bare — and MariaDB accepts the parentheses too.
func renderDefault(driver Driver, def string) string {
	if driver == DriverMySQL && functionDefaultRe.MatchString(def) && !strings.EqualFold(def, "NOW()") {
		return "(" + def + ")"
	}
	return def
}

// fragmentQuotes are the characters that open a quoted region on each engine,
// with the character that closes it. They differ, and the difference is the
// whole point: a bracket is a quoted identifier on SQL Server and an array
// subscript on Postgres, and a scanner that treats it as a quote on both lets
// the text after it hide from one of them.
func fragmentQuotes(driver Driver) map[byte]byte {
	quotes := map[byte]byte{'\'': '\'', '"': '"'}
	switch driver {
	case DriverMySQL, DriverClickHouse:
		quotes['`'] = '`'
	case DriverSQLite:
		quotes['`'], quotes['['] = '`', ']'
	case DriverMSSQL:
		quotes['['] = ']'
	}
	return quotes
}

// validateFragment accepts a SQL expression that the caller will place inside
// parentheses of its own: a CHECK condition, a partial index predicate, a
// USING conversion.
//
// An expression cannot be bound and cannot be checked against a pattern, so
// what is enforced is that it cannot leave the place it was put: its
// parentheses balance and never close one it did not open, and outside its
// quoted regions it holds no statement separator, no comment introducer and
// no character that opens a kind of quoting this scanner does not model
// (a backslash escape, a Postgres dollar quote, an Oracle q-quote). What the
// expression *says* is the engine's to judge, exactly as it would be typed
// into a CREATE TABLE in the Query tab.
func validateFragment(driver Driver, what, text string) (string, error) {
	text = strings.TrimSpace(text)
	refuse := func(why string) (string, error) {
		return "", fmt.Errorf("%s %s; use the Query tab for it", what, why)
	}
	if text == "" {
		return "", fmt.Errorf("%s is required", what)
	}
	if len(text) > 4000 {
		return refuse("is too long for this form")
	}
	if !utf8.ValidString(text) {
		return refuse("is not valid text")
	}
	quotes := fragmentQuotes(driver)
	depth := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\\' {
			return refuse("contains a backslash, which is an escape on some engines and not on others")
		}
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' || c == 0x7f {
			return refuse("contains a control character")
		}
		if closer, ok := quotes[c]; ok {
			if c == '\'' && driver == DriverOracle && i > 0 && (text[i-1] == 'q' || text[i-1] == 'Q') {
				return refuse("uses Oracle's q-quoting")
			}
			closed := false
			for i++; i < len(text); i++ {
				if text[i] == '\\' {
					return refuse("contains a backslash, which is an escape on some engines and not on others")
				}
				if text[i] == closer {
					if i+1 < len(text) && text[i+1] == closer {
						i++
						continue
					}
					closed = true
					break
				}
			}
			if !closed {
				return refuse("has an unterminated quote")
			}
			continue
		}
		switch c {
		case '(':
			depth++
		case ')':
			if depth--; depth < 0 {
				return refuse("closes a parenthesis it did not open")
			}
		case ';':
			return refuse("contains a statement separator")
		case '-':
			if i+1 < len(text) && text[i+1] == '-' {
				return refuse("contains a comment")
			}
		case '/':
			if i+1 < len(text) && text[i+1] == '*' {
				return refuse("contains a comment")
			}
		case '*':
			if i+1 < len(text) && text[i+1] == '/' {
				return refuse("contains a comment")
			}
		case '#':
			if driver == DriverMySQL || driver == DriverClickHouse {
				return refuse("contains a comment")
			}
		case '$':
			if driver != DriverOracle && driver != DriverMSSQL {
				return refuse("contains a dollar sign, which opens a quoted string on some engines")
			}
		case '?':
			if driver != DriverPostgres {
				return refuse("contains a bind marker")
			}
		case ':':
			if driver == DriverOracle {
				return refuse("contains a bind marker")
			}
		}
	}
	if depth != 0 {
		return refuse("has an unclosed parenthesis")
	}
	return text, nil
}

// ddlLiteral quotes a value a DDL statement cannot bind: a comment, an enum
// label. Doubling the quote is the complete escape for a string on every
// engine except the ones where a backslash is one too, and on Postgres a
// backslash is made unambiguous by saying which rule applies.
func ddlLiteral(driver Driver, what, s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("%s is not valid text", what)
	}
	for _, r := range s {
		if r == 0 || r < 0x20 && r != '\n' && r != '\r' && r != '\t' || r == 0x7f {
			return "", fmt.Errorf("%s contains a control character", what)
		}
	}
	quoted := strings.ReplaceAll(s, "'", "''")
	switch driver {
	case DriverMySQL, DriverClickHouse:
		return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", "''") + "'", nil
	case DriverPostgres:
		if strings.ContainsRune(s, '\\') {
			// E'' reads a backslash as an escape whatever
			// standard_conforming_strings says, so doubling it is right on
			// every server rather than on the ones with the modern default.
			return "E'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", "''") + "'", nil
		}
	case DriverMSSQL:
		// N'' keeps a character outside the database's code page intact.
		return "N'" + quoted + "'", nil
	}
	return "'" + quoted + "'", nil
}

// quoteColumns validates and quotes a column list.
func quoteColumns(d Dialect, what string, columns []string) (string, error) {
	if len(columns) == 0 {
		return "", fmt.Errorf("%s needs at least one column", what)
	}
	out := make([]string, len(columns))
	for i, c := range columns {
		q, err := d.QuoteIdent(c)
		if err != nil {
			return "", err
		}
		out[i] = q
	}
	return strings.Join(out, ", "), nil
}

// generatedName builds a constraint or index name from the table and columns
// when the operator gave none, cut to what the shortest identifier limit
// among these engines still accepts.
func generatedName(table string, columns []string, suffix string) string {
	name := table
	for _, c := range columns {
		name += "_" + c
	}
	const limit = 63 // Postgres; MySQL allows 64, SQL Server and Oracle 128
	if len(name)+len(suffix)+1 > limit {
		cut := limit - len(suffix) - 1
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut]
	}
	return name + "_" + suffix
}

// --- tables and columns -----------------------------------------------------

// NewColumn is one column in a create-table or add-column request.
type NewColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	NotNull    bool   `json:"notNull"`
	PrimaryKey bool   `json:"primaryKey"`
	Default    string `json:"default,omitempty"`
}

// renderColumn produces one column definition line.
func renderColumn(d Dialect, c NewColumn, inline bool) (string, error) {
	q, err := d.QuoteIdent(c.Name)
	if err != nil {
		return "", err
	}
	if err := validateType(c.Type); err != nil {
		return "", err
	}
	if err := validateDefault(c.Default); err != nil {
		return "", err
	}
	line := q + " " + strings.TrimSpace(c.Type)
	// Trimmed once and tested trimmed: a default of spaces used to pass the
	// check as empty and then render as `DEFAULT` with nothing after it.
	if def := strings.TrimSpace(c.Default); def != "" {
		line += " DEFAULT " + renderDefault(d.Driver(), def)
	}
	// ClickHouse spells optional as Nullable(T); a column of any other type
	// already refuses NULL, and its parser wants no clause saying so.
	if c.NotNull && d.Driver() != DriverClickHouse {
		line += " NOT NULL"
	}
	// A single-column primary key is written inline; a composite one becomes a
	// table constraint, which is why CreateTable collects them instead.
	if inline && c.PrimaryKey {
		line += " PRIMARY KEY"
	}
	return line, nil
}

// CreateTableSQL renders the statement without running it, which is what lets
// the UI show the operator exactly what they are about to execute.
func CreateTableSQL(driver Driver, schema, table string, cols []NewColumn) (string, error) {
	d, err := ddlDialect(driver, OpCreateTable)
	if err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("a table needs at least one column")
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return "", err
	}
	pks := []string{}
	for _, c := range cols {
		if c.PrimaryKey {
			q, err := d.QuoteIdent(c.Name)
			if err != nil {
				return "", err
			}
			pks = append(pks, q)
		}
	}
	lines := make([]string, 0, len(cols)+1)
	for _, c := range cols {
		line, err := renderColumn(d, c, len(pks) == 1)
		if err != nil {
			return "", err
		}
		lines = append(lines, "  "+line)
	}
	if len(pks) > 1 {
		lines = append(lines, "  PRIMARY KEY ("+strings.Join(pks, ", ")+")")
	}
	return fmt.Sprintf("CREATE TABLE %s (\n%s\n)", rel, strings.Join(lines, ",\n")), nil
}

// PlanCreateTable plans a CREATE TABLE.
func PlanCreateTable(driver Driver, schema, table string, cols []NewColumn) (*DDLPlan, error) {
	stmt, err := CreateTableSQL(driver, schema, table, cols)
	if err != nil {
		return nil, err
	}
	plan := planOf(stmt)
	for _, c := range cols {
		plan.vouching(unvouchedDefault(c.Default))
	}
	return plan, nil
}

// CreateTable renders and executes.
func CreateTable(ctx context.Context, db *sql.DB, driver Driver, schema, table string, cols []NewColumn) (string, error) {
	plan, err := PlanCreateTable(driver, schema, table, cols)
	if err != nil {
		return "", err
	}
	return plan.run(ctx, db)
}

// PlanDropTable plans removing a table. The route in front of this is in the
// destructive group, as DROP through the query runner is: the route being a
// form must not make the guard weaker.
func PlanDropTable(driver Driver, schema, table string) (*DDLPlan, error) {
	return planRelStatement(driver, OpDropTable, schema, table, "DROP TABLE %s")
}

func DropTable(ctx context.Context, db *sql.DB, driver Driver, schema, table string) (string, error) {
	plan, err := PlanDropTable(driver, schema, table)
	if err != nil {
		return "", err
	}
	return plan.run(ctx, db)
}

// PlanTruncate plans emptying a table. SQLite has no TRUNCATE and optimises an
// unqualified DELETE into the same thing, so it gets that instead of an error.
func PlanTruncate(driver Driver, schema, table string) (*DDLPlan, error) {
	form := "TRUNCATE TABLE %s"
	if driver == DriverSQLite {
		form = "DELETE FROM %s"
	}
	return planRelStatement(driver, OpTruncate, schema, table, form)
}

func TruncateTable(ctx context.Context, db *sql.DB, driver Driver, schema, table string) (string, error) {
	plan, err := PlanTruncate(driver, schema, table)
	if err != nil {
		return "", err
	}
	return plan.run(ctx, db)
}

func planRelStatement(driver Driver, op, schema, table, form string) (*DDLPlan, error) {
	d, err := ddlDialect(driver, op)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return nil, err
	}
	return planOf(fmt.Sprintf(form, rel)), nil
}

// PlanAddColumn plans appending a column to an existing table.
func PlanAddColumn(driver Driver, schema, table string, col NewColumn) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpAddColumn)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return nil, err
	}
	// A column added to a table that already has rows cannot be NOT NULL without
	// a default — every existing row would violate it. Saying so here is more
	// use than relaying whichever way the engine phrases that. ClickHouse fills
	// existing rows with the type's own default, so it has no such case.
	if col.NotNull && strings.TrimSpace(col.Default) == "" && driver != DriverClickHouse {
		return nil, fmt.Errorf("a NOT NULL column added to an existing table needs a default, or every existing row would violate it")
	}
	def, err := renderColumn(d, col, false)
	if err != nil {
		return nil, err
	}
	return planOf(fmt.Sprintf("ALTER TABLE %s %s %s", rel, d.AddColumnKeyword(), def)).
		vouching(unvouchedDefault(col.Default)), nil
}

func AddColumn(ctx context.Context, db *sql.DB, driver Driver, schema, table string, col NewColumn) (string, error) {
	plan, err := PlanAddColumn(driver, schema, table, col)
	if err != nil {
		return "", err
	}
	return plan.run(ctx, db)
}

// PlanDropColumn plans removing a column and everything in it.
//
// SQL Server holds a column hostage behind the default constraint it created
// for it, so clearing that is part of the same action. It is part of the same
// *statement* too: the constraint used to be dropped first and the column
// after, and a column that then refused to go (an index on it, a check) had
// already lost its default. One batch in one transaction either does both or
// neither.
func PlanDropColumn(ctx context.Context, db *sql.DB, driver Driver, schema, table, column string) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpDropColumn)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return nil, err
	}
	col, err := d.QuoteIdent(column)
	if err != nil {
		return nil, err
	}
	drop := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", rel, col)
	if driver != DriverMSSQL {
		// Whatever a dialect needs cleared first is cleared when the plan
		// runs, not when it is drawn up.
		plan := planOf(drop)
		plan.before = func(ctx context.Context, db *sql.DB) error {
			if err := d.BeforeDropColumn(ctx, db, schema, table, column); err != nil {
				return fmt.Errorf("could not clear what depends on %s first: %w", column, err)
			}
			return nil
		}
		return plan, nil
	}
	defaults, err := mssqlDefaultConstraints(ctx, db, schema, table, column)
	if err != nil {
		return nil, fmt.Errorf("could not read what depends on %s: %w", column, err)
	}
	if len(defaults) == 0 {
		return planOf(drop), nil
	}
	steps := []string{}
	for _, name := range defaults {
		q, err := d.QuoteIdent(name)
		if err != nil {
			return nil, err
		}
		steps = append(steps, "ALTER TABLE "+rel+" DROP CONSTRAINT "+q)
	}
	return planOf(mssqlAtomicBatch(append(steps, drop))), nil
}

// mssqlAtomicBatch joins statements into one batch that commits all of them or
// none. XACT_ABORT is what makes the "none" true: without it SQL Server carries
// on to the next statement after most errors and commits what did run.
func mssqlAtomicBatch(statements []string) string {
	return "SET XACT_ABORT ON;\nBEGIN TRANSACTION;\n" + strings.Join(statements, ";\n") + ";\nCOMMIT TRANSACTION"
}

func DropColumn(ctx context.Context, db *sql.DB, driver Driver, schema, table, column string) (string, error) {
	plan, err := PlanDropColumn(ctx, db, driver, schema, table, column)
	if err != nil {
		return "", err
	}
	return plan.run(ctx, db)
}

// mssqlName renders a multi-part name as the N'…' string sp_rename and the
// extended-property procedures take: each part bracket-quoted, so a dot inside
// a name is not read as a separator.
func mssqlName(d Dialect, parts ...string) (string, error) {
	quoted := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		q, err := d.QuoteIdent(p)
		if err != nil {
			return "", err
		}
		quoted = append(quoted, q)
	}
	return "N'" + strings.ReplaceAll(strings.Join(quoted, "."), "'", "''") + "'", nil
}

// PlanRenameColumn plans renaming a column. Every engine here spells this the
// same way except SQL Server, which has no ALTER ... RENAME COLUMN at all and
// does it through a system stored procedure.
func PlanRenameColumn(driver Driver, schema, table, from, to string) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpRenameColumn)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return nil, err
	}
	qFrom, err := d.QuoteIdent(from)
	if err != nil {
		return nil, err
	}
	qTo, err := d.QuoteIdent(to)
	if err != nil {
		return nil, err
	}
	if driver == DriverMSSQL {
		// The names are literals in the statement rather than bound arguments,
		// so the statement shown before running is the statement that runs.
		// The new name is not bracketed: sp_rename takes it verbatim.
		object, err := mssqlName(d, schema, table, from)
		if err != nil {
			return nil, err
		}
		target, err := ddlLiteral(driver, "the new name", to)
		if err != nil {
			return nil, err
		}
		return planOf(fmt.Sprintf("EXEC sp_rename %s, %s, 'COLUMN'", object, target)), nil
	}
	return planOf(fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", rel, qFrom, qTo)), nil
}

func RenameColumn(ctx context.Context, db *sql.DB, driver Driver, schema, table, from, to string) (string, error) {
	plan, err := PlanRenameColumn(driver, schema, table, from, to)
	if err != nil {
		return "", err
	}
	return plan.run(ctx, db)
}

// PlanRenameTable plans renaming a table.
func PlanRenameTable(driver Driver, schema, table, to string) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpRenameTable)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return nil, err
	}
	qTo, err := d.QuoteIdent(to)
	if err != nil {
		return nil, err
	}
	switch driver {
	case DriverMSSQL:
		object, err := mssqlName(d, schema, table)
		if err != nil {
			return nil, err
		}
		target, err := ddlLiteral(driver, "the new name", to)
		if err != nil {
			return nil, err
		}
		return planOf(fmt.Sprintf("EXEC sp_rename %s, %s", object, target)), nil
	case DriverMySQL, DriverClickHouse:
		// RENAME TABLE takes a qualified name on both sides and reads an
		// unqualified one against the connection's database. Left bare, the
		// target moved the table into whichever database the pool happened to
		// be using.
		target, err := qualify(d, schema, to)
		if err != nil {
			return nil, err
		}
		return planOf(fmt.Sprintf("RENAME TABLE %s TO %s", rel, target)), nil
	default:
		return planOf(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", rel, qTo)), nil
	}
}

func RenameTable(ctx context.Context, db *sql.DB, driver Driver, schema, table, to string) (string, error) {
	plan, err := PlanRenameTable(driver, schema, table, to)
	if err != nil {
		return "", err
	}
	return plan.run(ctx, db)
}

// --- indexes ----------------------------------------------------------------

// IndexSpec is an index to build.
type IndexSpec struct {
	Schema  string
	Table   string
	Name    string
	Columns []string
	Unique  bool
	// Method is the access method or index type: btree, gin, hash, fulltext,
	// clustered. Empty takes the engine's default.
	Method string
	// Where is a partial index's predicate.
	Where string
	// IfNotExists makes the statement a no-op when the name is taken.
	IfNotExists bool
	// Concurrently builds without blocking writes, on the engines that have a
	// way to say so.
	Concurrently bool
}

// indexMethods are the methods each engine's form may name, mapped to how the
// statement spells them. Postgres also accepts any installed access method by
// its name (hnsw, ivfflat, bloom), which is checked by shape.
var indexMethods = map[Driver]map[string]string{
	DriverMySQL:  {"btree": "BTREE", "hash": "HASH", "fulltext": "FULLTEXT", "spatial": "SPATIAL"},
	DriverMSSQL:  {"clustered": "CLUSTERED", "nonclustered": "NONCLUSTERED"},
	DriverOracle: {"bitmap": "BITMAP"},
}

var accessMethodRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,30}$`)

// PlanCreateIndex plans a CREATE INDEX. It takes the connection because one
// option depends on which product is behind the MySQL driver.
func PlanCreateIndex(ctx context.Context, db *sql.DB, driver Driver, spec IndexSpec) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpCreateIndex)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, spec.Schema, spec.Table)
	if err != nil {
		return nil, err
	}
	cols, err := quoteColumns(d, "an index", spec.Columns)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.Name) == "" {
		spec.Name = generatedName(spec.Table, spec.Columns, "idx")
	}
	name, err := d.QuoteIdent(spec.Name)
	if err != nil {
		return nil, err
	}
	method := strings.ToLower(strings.TrimSpace(spec.Method))
	for _, option := range []struct {
		op  string
		set bool
	}{
		{OpIndexMethod, method != ""},
		{OpIndexPartial, strings.TrimSpace(spec.Where) != ""},
		{OpIndexIfMissing, spec.IfNotExists},
		{OpIndexOnline, spec.Concurrently},
	} {
		if !option.set {
			continue
		}
		if reason, refused := ddlRefusals[driver][option.op]; refused {
			return nil, fmt.Errorf("%s", reason)
		}
	}
	where := ""
	var calls []string
	if strings.TrimSpace(spec.Where) != "" {
		predicate, err := validateFragment(driver, "the index predicate", spec.Where)
		if err != nil {
			return nil, err
		}
		where, calls = " WHERE ("+predicate+")", unvouchedCalls(driver, predicate)
	}
	// The predicate is the one part of an index the operator writes as SQL.
	done := func(statement string) (*DDLPlan, error) { return planOf(statement).vouching(calls), nil }
	keyword := ""
	if method != "" && driver != DriverPostgres {
		spelled, ok := indexMethods[driver][method]
		if !ok {
			return nil, fmt.Errorf("%q is not an index method this form offers for %s", spec.Method, driver)
		}
		keyword = spelled
	}

	unique := ""
	if spec.Unique {
		unique = "UNIQUE "
	}
	ifNotExists := ""
	if spec.IfNotExists {
		ifNotExists = "IF NOT EXISTS "
	}
	switch driver {
	case DriverPostgres:
		using := ""
		if method != "" {
			if !accessMethodRe.MatchString(method) {
				return nil, fmt.Errorf("%q is not an index method name", spec.Method)
			}
			using = " USING " + method
		}
		concurrently := ""
		if spec.Concurrently {
			concurrently = "CONCURRENTLY "
		}
		return done("CREATE " + unique + "INDEX " + concurrently + ifNotExists + name +
			" ON " + rel + using + " (" + cols + ")" + where)
	case DriverMySQL:
		if spec.IfNotExists && !isMariaDB(ctx, db) {
			return nil, fmt.Errorf("MySQL has no CREATE INDEX IF NOT EXISTS; MariaDB does")
		}
		switch keyword {
		case "FULLTEXT", "SPATIAL":
			if spec.Unique {
				return nil, fmt.Errorf("a %s index cannot be unique", strings.ToLower(keyword))
			}
			return done("CREATE " + keyword + " INDEX " + ifNotExists + name + " ON " + rel + " (" + cols + ")")
		case "":
			return done("CREATE " + unique + "INDEX " + ifNotExists + name + " ON " + rel + " (" + cols + ")")
		default:
			return done("CREATE " + unique + "INDEX " + ifNotExists + name + " ON " + rel + " (" + cols + ") USING " + keyword)
		}
	case DriverMSSQL:
		if keyword != "" {
			keyword += " "
		}
		online := ""
		if spec.Concurrently {
			online = " WITH (ONLINE = ON)"
		}
		return done("CREATE " + unique + keyword + "INDEX " + name + " ON " + rel + " (" + cols + ")" + where + online)
	case DriverOracle:
		if keyword == "BITMAP" {
			if spec.Unique {
				return nil, fmt.Errorf("a bitmap index cannot be unique")
			}
			unique = "BITMAP "
		}
		// An unqualified index name lands in the session's schema, which is
		// not the table's when the table is someone else's.
		qualified, err := qualify(d, spec.Schema, spec.Name)
		if err != nil {
			return nil, err
		}
		online := ""
		if spec.Concurrently {
			online = " ONLINE"
		}
		return done("CREATE " + unique + "INDEX " + qualified + " ON " + rel + " (" + cols + ")" + online)
	default:
		return done("CREATE " + unique + "INDEX " + ifNotExists + name + " ON " + rel + " (" + cols + ")" + where)
	}
}

// CreateIndex builds a plain index over one or more columns.
func CreateIndex(ctx context.Context, db *sql.DB, driver Driver, schema, table, name string, columns []string, unique bool) (string, error) {
	if len(columns) == 0 {
		return "", fmt.Errorf("an index needs at least one column")
	}
	if _, err := validIdent(driver, name); err != nil {
		return "", err
	}
	plan, err := PlanCreateIndex(ctx, db, driver, IndexSpec{
		Schema: schema, Table: table, Name: name, Columns: columns, Unique: unique,
	})
	if err != nil {
		return "", err
	}
	return plan.run(ctx, db)
}

// validIdent quotes a name the caller must have supplied.
func validIdent(driver Driver, name string) (string, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return "", err
	}
	return d.QuoteIdent(name)
}

// PlanDropIndex plans removing an index. MySQL and SQL Server scope the name
// to a table; Postgres, SQLite and Oracle treat it as a schema-level object;
// ClickHouse drops a data-skipping index as a change to its table.
func PlanDropIndex(driver Driver, schema, table, name string) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpDropIndex)
	if err != nil {
		return nil, err
	}
	qName, err := d.QuoteIdent(name)
	if err != nil {
		return nil, err
	}
	switch driver {
	case DriverMySQL, DriverMSSQL, DriverClickHouse:
		rel, err := qualify(d, schema, table)
		if err != nil {
			return nil, err
		}
		if driver == DriverClickHouse {
			return planOf(fmt.Sprintf("ALTER TABLE %s DROP INDEX %s", rel, qName)), nil
		}
		return planOf(fmt.Sprintf("DROP INDEX %s ON %s", qName, rel)), nil
	default:
		rel, err := qualify(d, schema, name)
		if err != nil {
			return nil, err
		}
		return planOf("DROP INDEX " + rel), nil
	}
}

func DropIndex(ctx context.Context, db *sql.DB, driver Driver, schema, table, name string) (string, error) {
	plan, err := PlanDropIndex(driver, schema, table, name)
	if err != nil {
		return "", err
	}
	return plan.run(ctx, db)
}
