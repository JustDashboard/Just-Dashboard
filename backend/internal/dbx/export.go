package dbx

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// ExportFormat is a wire format a result set can be downloaded as.
type ExportFormat string

const (
	ExportCSV  ExportFormat = "csv"
	ExportJSON ExportFormat = "json"
)

func (f ExportFormat) Valid() bool { return f == ExportCSV || f == ExportJSON }

// ContentType and Extension keep the HTTP header and the download filename in
// step with the format in one place.
func (f ExportFormat) ContentType() string {
	if f == ExportJSON {
		return "application/json"
	}
	return "text/csv"
}

func (f ExportFormat) Extension() string { return string(f) }

// StreamExport runs a query and writes its rows straight to w in the chosen
// format, one row at a time, rather than materialising the whole result in
// memory the way RunQuery does. A table export can be far larger than anything
// worth holding in a browser, so the export path and the browse path are
// deliberately different: browse caps and buffers, export streams and does not.
//
// The row cap still exists — an unbounded export from a dashboard is a foot-gun
// — but it is high, and hitting it is reported to the caller so a truncated file
// is never mistaken for a complete one.
func StreamExport(ctx context.Context, db *sql.DB, query string, args []any, format ExportFormat, w io.Writer, maxRows int) (int, bool, error) {
	if maxRows <= 0 || maxRows > 1_000_000 {
		maxRows = 100_000
	}
	args, err := sqlArguments(args)
	if err != nil {
		return 0, false, err
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, false, err
	}
	switch format {
	case ExportCSV:
		return streamCSV(rows, cols, w, maxRows)
	case ExportJSON:
		return streamJSON(rows, cols, w, maxRows)
	default:
		return 0, false, fmt.Errorf("unsupported export format %q", format)
	}
}

// ExportTable streams an entire table to w. The relation is built from
// validated, quoted identifiers — the same choke point BrowseTable uses — so no
// caller-supplied text reaches the statement unescaped.
func ExportTable(ctx context.Context, db *sql.DB, driver Driver, schema, table string, format ExportFormat, w io.Writer, maxRows int) (int, bool, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return 0, false, err
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return 0, false, err
	}
	return StreamExport(ctx, db, "SELECT * FROM "+rel, nil, format, w, maxRows)
}

func scanRow(rows *sql.Rows, n int) ([]any, error) {
	holders := make([]any, n)
	ptrs := make([]any, n)
	for i := range holders {
		ptrs[i] = &holders[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make([]any, n)
	for i, v := range holders {
		out[i] = normaliseValue(v)
	}
	return out, nil
}

func streamCSV(rows *sql.Rows, cols []string, w io.Writer, maxRows int) (int, bool, error) {
	cw := csv.NewWriter(w)
	if err := cw.Write(cols); err != nil {
		return 0, false, err
	}
	count := 0
	truncated := false
	rec := make([]string, len(cols))
	for rows.Next() {
		if count >= maxRows {
			truncated = true
			break
		}
		vals, err := scanRow(rows, len(cols))
		if err != nil {
			return count, truncated, err
		}
		for i, v := range vals {
			rec[i] = csvCell(v)
		}
		if err := cw.Write(rec); err != nil {
			return count, truncated, err
		}
		count++
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return count, truncated, err
	}
	return count, truncated, rows.Err()
}

// csvCell renders a value as a spreadsheet-friendly string. A NULL becomes an
// empty field (distinct from the empty string only in JSON, which CSV cannot
// express); nested objects and arrays are JSON-encoded so a cell is never a
// Go-syntax "map[...]".
func csvCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}

func streamJSON(rows *sql.Rows, cols []string, w io.Writer, maxRows int) (int, bool, error) {
	if _, err := io.WriteString(w, "[\n"); err != nil {
		return 0, false, err
	}
	enc := json.NewEncoder(w)
	count := 0
	truncated := false
	for rows.Next() {
		if count >= maxRows {
			truncated = true
			break
		}
		vals, err := scanRow(rows, len(cols))
		if err != nil {
			return count, truncated, err
		}
		obj := make(map[string]any, len(cols))
		for i, c := range cols {
			obj[c] = vals[i]
		}
		if count > 0 {
			if _, err := io.WriteString(w, ",\n"); err != nil {
				return count, truncated, err
			}
		}
		if err := enc.Encode(obj); err != nil {
			return count, truncated, err
		}
		count++
	}
	if _, err := io.WriteString(w, "]\n"); err != nil {
		return count, truncated, err
	}
	return count, truncated, rows.Err()
}
