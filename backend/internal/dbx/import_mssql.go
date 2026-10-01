package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// How an import writes to SQL Server.
//
// Everywhere else a value is bound as the text it is and the engine parses it
// into the column's type; a text it cannot parse is refused, and the import
// carries on or stops as it was told. SQL Server refuses it differently: a
// text that does not convert ends the batch and takes the whole transaction
// with it, so "leave the bad row out" lost every row before it, and the commit
// at the end found no transaction to commit. And some of its types do not take
// a text at all without being asked to: a binary column refuses even a NULL
// sent as one, a money column anything.
//
// So each value is converted in the statement, to the column's own type, by
// the form of conversion that answers NULL where the other raises — and a row
// with a value that did not convert is refused before the statement that
// would have written it runs, by an error that ends nothing but itself.

// mssqlConvertMarker is how the statement says a value did not convert. The
// number after it, where there is one, is the column's position.
const mssqlConvertMarker = "jd-import-convert"

// mssqlTypeText is a type as the catalogue spells one. It goes into the
// statement, so it is held to that shape whatever the catalogue said.
var mssqlTypeText = regexp.MustCompile(`^[a-z][a-z0-9]*(\((max|[0-9]+(,[0-9]+)?)\))?$`)

// mssqlImportValue writes one bound value into a statement for a column of
// this type. failed is the condition under which the value holds something
// the column's type cannot be given, or empty where nothing has to be asked:
// a text column takes any text, and a binary one is only ever bound bytes.
//
// bytes says whether a binary column's values arrive as bytes. Where they
// arrive as whatever text the file held — the inline route, which reads no
// \x form — the text is left for the server to refuse as it always has.
func mssqlImportValue(typeName, mark string, bytes bool) (value, failed string) {
	t := strings.ToLower(strings.TrimSpace(typeName))
	if !mssqlTypeText.MatchString(t) {
		return mark, ""
	}
	base := t
	if i := strings.IndexByte(base, '('); i >= 0 {
		base = base[:i]
	}
	var try string
	switch base {
	case "binary", "varbinary", "image":
		if !bytes {
			return mark, ""
		}
		// A NULL is sent as a text with nothing in it, which a binary column
		// refuses for being a text.
		return "CONVERT(varbinary(max), " + mark + ")", ""
	case "datetime", "smalldatetime":
		// Read as the wider type first, which takes every ISO spelling the
		// same way under every language setting. On their own these two read
		// 2026-03-04 as the third of April for a login whose language puts
		// the day first.
		try = fmt.Sprintf("COALESCE(TRY_CONVERT(%[1]s, TRY_CONVERT(datetime2, %[2]s)), TRY_CONVERT(%[1]s, %[2]s))", t, mark)
	case "tinyint", "smallint", "int", "bigint", "bit", "decimal", "numeric", "money", "smallmoney",
		"float", "real", "date", "time", "datetime2", "datetimeoffset", "uniqueidentifier", "xml":
		try = "TRY_CONVERT(" + t + ", " + mark + ")"
	default:
		return mark, ""
	}
	return try, mark + " IS NOT NULL AND " + try + " IS NULL"
}

func (p *importPlan) mssqlConverts() bool { return p.d.Driver() == DriverMSSQL }

// mssqlValue is mssqlImportValue for one of the plan's columns.
func (p *importPlan) mssqlValue(at int, mark string) (value, failed string) {
	return mssqlImportValue(p.cols[at].typeName, mark, !p.spec.trusted)
}

// mssqlGuard is what goes in front of a statement of n rows: the check that
// every value converts, and the error raised instead of the statement where
// one does not. For one row it says which column; for several, only that one
// of them is at fault, and the rows are then written one at a time to find it.
func (p *importPlan) mssqlGuard(n int) string {
	if !p.mssqlConverts() {
		return ""
	}
	var guarded []int
	for i := range p.cols {
		if _, failed := p.mssqlValue(i, ""); failed != "" {
			guarded = append(guarded, i)
		}
	}
	if len(guarded) == 0 {
		return ""
	}
	if n == 1 {
		var b strings.Builder
		for _, at := range guarded {
			_, failed := p.mssqlValue(at, p.d.Placeholder(at+1))
			fmt.Fprintf(&b, "IF %s RAISERROR(N'%s:%d', 16, 1) ELSE ", failed, mssqlConvertMarker, at+1)
		}
		return b.String()
	}
	rows := make([]string, n)
	for r := range rows {
		marks := make([]string, len(guarded))
		for i, at := range guarded {
			marks[i] = p.d.Placeholder(r*len(p.cols) + at + 1)
		}
		rows[r] = "(" + strings.Join(marks, ", ") + ")"
	}
	names := make([]string, len(guarded))
	conds := make([]string, len(guarded))
	for i, at := range guarded {
		names[i] = "g" + strconv.Itoa(i+1)
		_, failed := p.mssqlValue(at, names[i])
		conds[i] = "(" + failed + ")"
	}
	return fmt.Sprintf("IF EXISTS (SELECT 1 FROM (VALUES %s) AS r (%s) WHERE %s) RAISERROR(N'%s', 16, 1) ELSE ",
		strings.Join(rows, ", "), strings.Join(names, ", "), strings.Join(conds, " OR "), mssqlConvertMarker)
}

// rowError puts a refusal in the file's terms where the engine's own says
// less: which column, and the value it would not take.
func (p *importPlan) rowError(err error, args []any) error {
	if err == nil || !p.mssqlConverts() {
		return err
	}
	_, after, found := strings.Cut(err.Error(), mssqlConvertMarker+":")
	if !found {
		return err
	}
	digits := after
	if end := strings.IndexFunc(after, func(r rune) bool { return r < '0' || r > '9' }); end >= 0 {
		digits = after[:end]
	}
	at, convErr := strconv.Atoi(digits)
	if convErr != nil || at < 1 || at > len(p.cols) || at > len(args) {
		return err
	}
	c := p.cols[at-1]
	return fmt.Errorf("%s: %q is not a value a %s column takes", c.name, clipText(fmt.Sprint(args[at-1]), 60), c.typeName)
}

// writesIdentity reports whether the import writes the column the table
// numbers itself.
func (p *importPlan) writesIdentity(ctx context.Context, db *sql.DB) bool {
	var name sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT name FROM sys.identity_columns WHERE object_id = OBJECT_ID(@p1)`, p.rel).Scan(&name); err != nil {
		return false
	}
	for _, c := range p.cols {
		if c.name == name.String {
			return true
		}
	}
	return false
}
