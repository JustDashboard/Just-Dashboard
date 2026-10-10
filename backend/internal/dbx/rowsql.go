package dbx

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
	return rowInsertSQL(driver, schema, table, nil, row)
}

// rowInsertSQL renders one row. columns is the table as its catalogue has it,
// or nil where nobody read it: with it the statement names the columns in the
// table's own order and writes a number as a number, and without it the
// columns go by name and each value is written as what it arrived as.
func rowInsertSQL(driver Driver, schema, table string, columns []Column, row map[string]any) (string, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return "", err
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return "", err
	}
	known := make(map[string]Column, len(columns))
	for _, c := range columns {
		known[c.Name] = c
	}
	cols := make([]string, 0, len(row))
	for _, c := range columns {
		if _, held := row[c.Name]; held {
			cols = append(cols, c.Name)
		}
	}
	// What the catalogue does not have — every column, when it was not read —
	// goes by name. Column order in a map is random, and a statement that
	// comes out in a different order every time it is copied is unreadable in
	// a diff.
	strays := make([]string, 0, len(row))
	for c := range row {
		if _, ok := known[c]; !ok {
			strays = append(strays, c)
		}
	}
	sort.Strings(strays)
	cols = append(cols, strays...)

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
		literal, ok := "", false
		if column, typed := known[c]; typed {
			literal, ok = numberLiteral(driver, column, row[c])
		}
		if !ok {
			if literal, err = sqlLiteral(d, row[c]); err != nil {
				return "", fmt.Errorf("column %s %w", c, err)
			}
		}
		names = append(names, q)
		values = append(values, literal)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("row has no columns")
	}
	return fmt.Sprintf("INSERT INTO %s (%s)\nVALUES (%s);",
		rel, strings.Join(names, ", "), strings.Join(values, ", ")), nil
}

// The digits a numeric column's cell may be written as without quotes. An
// exponent is left to the approximate types: on MySQL and SQL Server a
// literal that carries one is a float whatever column it is headed for.
var (
	integerDigitsPattern = regexp.MustCompile(`^-?[0-9]+$`)
	decimalDigitsPattern = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
)

// numberLiteral is a cell of a numeric column as the number it is.
//
// A 64-bit integer and a decimal travel to the grid as text, because a
// browser's number cannot hold either, and so they come back as text: copied
// out as they arrived they read '9007199254740993' and '12.50', which every
// engine accepts and nobody would have typed. Where the catalogue says the
// column is a number and the text is nothing but one, it is written bare.
// Anything else in such a column — NaN, a money column's '$1,234.00', a
// zero-filled 007 — is not a literal any engine reads the same way without
// its quotes, and keeps them.
func numberLiteral(driver Driver, column Column, v any) (string, bool) {
	text, ok := v.(string)
	if !ok {
		return "", false
	}
	switch ValueKind(driver, column.Type) {
	case KindInteger:
		return text, integerDigitsPattern.MatchString(text)
	case KindDecimal:
		return text, decimalDigitsPattern.MatchString(text)
	case KindFloat:
		return text, jsonNumberPattern.MatchString(text)
	}
	return "", false
}

// RowsInsertSQL renders several rows, one statement each. Separate statements
// rather than a multi-row VALUES list: the rows in a selection do not
// necessarily share a column set, and a partial paste of separate statements
// still gets most of the rows in.
func RowsInsertSQL(driver Driver, schema, table string, rows []map[string]any) (string, error) {
	return rowsInsertSQL(driver, schema, table, nil, rows)
}

func rowsInsertSQL(driver Driver, schema, table string, columns []Column, rows []map[string]any) (string, error) {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		s, err := rowInsertSQL(driver, schema, table, columns, r)
		if err != nil {
			return "", err
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n"), nil
}

// TableRowsInsertSQL is RowsInsertSQL for rows of a table the server can be
// asked about: the columns in the order the table has them, and the numbers
// of its numeric columns written as numbers.
//
// The catalogue is read for that and nothing else, and reading it is best
// effort. A server that does not answer, a table this account cannot see and
// a nil pool all leave the statement as RowsInsertSQL writes it — still one
// the engine accepts — because what was asked for is text for a clipboard,
// and a row already on the page should not need its server to be copied.
func TableRowsInsertSQL(ctx context.Context, db *sql.DB, driver Driver, schema, table string, rows []map[string]any) (string, error) {
	var columns []Column
	if d, err := DialectFor(driver); err == nil && db != nil && table != "" {
		if cols, err := d.Columns(ctx, db, catalogSchema(ctx, db, d, schema), table); err == nil {
			columns = cols
		}
	}
	return rowsInsertSQL(driver, schema, table, columns, rows)
}

// previewPattern is the form the grid shows for a binary value it cut short.
// It is a description of a value, not the value, and rendering it into a
// statement would write the description into the column.
var previewPattern = regexp.MustCompile(`^\\x[0-9a-f]*… \(\d+ bytes\)$`)

// sqlLiteral renders one value as SQL text for an engine.
//
// The rules are the dump's, because they have to be: a string is quoted with
// the quote doubled, and on the engines where a backslash escapes — MySQL,
// ClickHouse — a backslash is doubled too, or a value ending in one would
// swallow the closing quote. A boolean is TRUE/FALSE where the engine has the
// type and 1/0 where it does not; rendering 1 for PostgreSQL produced an INSERT
// its own boolean column refused.
func sqlLiteral(d Dialect, v any) (string, error) {
	driver := d.Driver()
	switch t := v.(type) {
	case nil:
		return "NULL", nil
	case json.Number:
		return t.String(), nil
	case float32:
		return formatFloat(float64(t)), nil
	case float64:
		return formatFloat(t), nil
	case string:
		if previewPattern.MatchString(t) {
			return "", fmt.Errorf("holds a preview of a larger binary value, not the value; open the cell to copy it")
		}
		// The grid shows a binary value as \x and its hex digits. Copied back
		// out it is the bytes it stood for, not a string that begins with a
		// backslash.
		if hexValuePattern.MatchString(t) && len(t) > 2 {
			if b, err := hex.DecodeString(t[2:]); err == nil {
				return dumpBytes(driver, b), nil
			}
		}
		return dumpString(driver, t), nil
	case []byte:
		// A byte slice reaching here is either text the driver did not decode
		// or genuine binary. Rendering it as a hex literal is correct for the
		// second and legible for neither, so text is preferred when it is text.
		if isPrintableText(t) {
			return dumpString(driver, string(t)), nil
		}
		return dumpBytes(driver, t), nil
	case map[string]any, []any:
		b, err := json.Marshal(t)
		if err != nil {
			return "", err
		}
		return dumpString(driver, string(b)), nil
	}
	return dumpLiteral(driver, v), nil
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
