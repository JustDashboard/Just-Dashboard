package dbx

import (
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
		literal, err := sqlLiteral(d, row[c])
		if err != nil {
			return "", fmt.Errorf("column %s %w", c, err)
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
