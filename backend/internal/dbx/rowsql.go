package dbx

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Copying a row out of the browser as an INSERT is the small thing a developer
// does constantly: reproducing one production record in a local database to
// debug against, seeding a fixture, attaching the offending row to a bug
// report. Doing it by hand means retyping a dozen values and getting the
// quoting wrong on the one that contains an apostrophe.
//
// This is the one place in the package that puts a value into SQL text rather
// than binding it, and it is safe for a reason that does not generalise: the
// statement is never executed here. It is rendered to a string, handed to the
// operator's clipboard, and run — if at all — somewhere else, by them, after
// they have read it. Nothing in this file may be called from a code path that
// then executes what it produced; the editing routes bind their values, and
// that is not negotiable.

// RowInsertSQL renders a row as an INSERT statement for the engine it came
// from, so pasting it into that engine's own client works unaltered.
func RowInsertSQL(driver Driver, schema, table string, row map[string]any) (string, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return "", err
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return "", err
	}
	cols := make([]string, 0, len(row))
	for c := range row {
		cols = append(cols, c)
	}
	// Column order in a map is random, and a statement that comes out in a
	// different order every time it is copied is unreadable in a diff.
	sort.Strings(cols)

	names := make([]string, 0, len(cols))
	values := make([]string, 0, len(cols))
	for _, c := range cols {
		// JSON numbers are emitted as numeric literals below. Validate their
		// syntax and reject already-rounded/non-finite floats before rendering.
		if _, err := SQLValue(row[c]); err != nil {
			return "", fmt.Errorf("column %s: %w", c, err)
		}
		q, err := d.QuoteIdent(c)
		if err != nil {
			return "", err
		}
		names = append(names, q)
		values = append(values, sqlLiteral(d, row[c]))
	}
	if len(names) == 0 {
		return "", fmt.Errorf("row has no columns")
	}
	return fmt.Sprintf("INSERT INTO %s (%s)\nVALUES (%s);",
		rel, strings.Join(names, ", "), strings.Join(values, ", ")), nil
}

// RowsInsertSQL renders several rows, one statement each. Separate statements
// rather than a multi-row VALUES list: the rows in a selection do not
// necessarily share a column set, and a partial paste of separate statements
// still gets most of the rows in.
func RowsInsertSQL(driver Driver, schema, table string, rows []map[string]any) (string, error) {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		s, err := RowInsertSQL(driver, schema, table, r)
		if err != nil {
			return "", err
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n"), nil
}

// sqlLiteral renders one value as SQL text.
//
// Strings are single-quoted with the quote doubled, which is the escape every
// engine here accepts and the only one they all agree on — a backslash escape
// is MySQL-specific and would be a literal backslash on Postgres.
func sqlLiteral(d Dialect, v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case bool:
		// SQLite, SQL Server and Oracle have no boolean literal; 1/0 is
		// accepted by all six and means the same thing in each.
		if t {
			return "1"
		}
		return "0"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(t)
	case float32:
		return formatFloat(float64(t))
	case float64:
		return formatFloat(t)
	case json.Number:
		return t.String()
	case time.Time:
		return quoteLiteral(t.UTC().Format("2006-01-02 15:04:05.999999"))
	case []byte:
		// A byte slice reaching here is either text the driver did not decode
		// or genuine binary. Rendering it as a hex literal is correct for the
		// second and legible for neither, so text is preferred when it is text.
		if s := string(t); isPrintable(s) {
			return quoteLiteral(s)
		}
		return hexLiteral(d, t)
	case string:
		return quoteLiteral(t)
	case map[string]any, []any:
		b, err := json.Marshal(t)
		if err != nil {
			return "NULL"
		}
		return quoteLiteral(string(b))
	default:
		return quoteLiteral(fmt.Sprint(t))
	}
}

func formatFloat(f float64) string {
	// A NaN or an infinity has no literal any of these engines will parse, and
	// silently emitting the token would produce a statement that fails on
	// paste with no clue why.
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "NULL"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func hexLiteral(d Dialect, b []byte) string {
	h := fmt.Sprintf("%x", b)
	switch d.Driver() {
	case DriverPostgres:
		return "'\\x" + h + "'::bytea"
	case DriverMySQL, DriverSQLite, DriverMSSQL:
		return "0x" + h
	default:
		return quoteLiteral(h)
	}
}

func isPrintable(s string) bool {
	for _, r := range s {
		if r == 0 || (r < 0x20 && r != '\t' && r != '\n' && r != '\r') {
			return false
		}
	}
	return true
}
