package dbx

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"
)

// The grid shows a preview of a large value — the first bytes of a blob, the
// first lines of a document — because a page of a thousand rows cannot carry a
// megabyte in each. This is how the value itself is fetched once somebody opens
// that one cell, and it is the only honest source for an edit: a preview
// written back would replace the value with its own first lines.

// MaxCellBytes is the largest value one cell read returns. The driver holds the
// whole value in memory before it can be sent, so the bound is what one request
// may cost the dashboard, not a comment on how large a value may be.
const MaxCellBytes = 8 << 20

// CellValue is one cell, whole.
type CellValue struct {
	Column string `json:"column"`
	Type   string `json:"type"`
	Kind   string `json:"kind"`
	// Encoding says how Value travels: "text" for a string as it is, "base64"
	// for bytes, "json" for anything else in its JSON form, "null" for NULL.
	Encoding string `json:"encoding"`
	Value    any    `json:"value"`
	// Size is the value's length in bytes as stored.
	Size int64 `json:"size"`
}

var (
	// ErrRowNotFound and ErrRowAmbiguous are the two ways a key fails to name
	// one row.
	ErrRowNotFound  = errors.New("no row matches this key")
	ErrRowAmbiguous = errors.New("more than one row matches this key")
)

// CellTooLargeError is a value past MaxCellBytes. It carries the size so the
// operator is told what they asked for rather than only that it was refused.
// AtLeast marks a size the engine could only bound from below.
type CellTooLargeError struct {
	Size    int64
	AtLeast bool
}

func (e *CellTooLargeError) Error() string {
	size := strconv.FormatInt(e.Size, 10)
	if e.AtLeast {
		size = "at least " + size
	}
	return fmt.Sprintf("this value is %s bytes, past the %d the dashboard reads into one cell",
		size, int64(MaxCellBytes))
}

// ReadCell returns one column of the one row a key names.
func ReadCell(ctx context.Context, db *sql.DB, driver Driver, schema, table, column string, key map[string]any) (*CellValue, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	// The same resolution an edit goes through: the columns exist, the key
	// names every primary key column, values are bound.
	plan, err := loadTable(ctx, db, driver, schema, table)
	if err != nil {
		return nil, err
	}
	col, quoted, err := plan.column(column)
	if err != nil {
		return nil, err
	}
	where, args, _, err := plan.identity(key, 1)
	if err != nil {
		return nil, err
	}
	if args, err = sqlArguments(args); err != nil {
		return nil, err
	}
	tail, tailArgs := d.Paginate(2, 0, len(args)+1)
	args = append(args, tailArgs...)
	out := &CellValue{Column: column, Type: col.Type, Kind: ValueKind(driver, col.Type)}

	// Measured first, on the server. Fetching the value to find out it was a
	// gigabyte is the thing the bound exists to prevent, so a value the engine
	// cannot measure is not fetched either.
	measure, floor, err := d.byteLength(col, quoted)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT "+measure+" FROM "+plan.rel+" WHERE "+where+" "+tail, args...)
	if err != nil {
		return nil, err
	}
	sizes := []sql.NullInt64{}
	for rows.Next() {
		var n sql.NullInt64
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		sizes = append(sizes, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch {
	case len(sizes) == 0:
		return nil, ErrRowNotFound
	case len(sizes) > 1:
		return nil, ErrRowAmbiguous
	case sizes[0].Int64 > MaxCellBytes:
		return nil, &CellTooLargeError{Size: sizes[0].Int64, AtLeast: floor}
	}

	read := quoted
	if reader, ok := d.(columnReader); ok {
		read = reader.readExpr(col, quoted)
	}
	rows, err = db.QueryContext(ctx, "SELECT "+read+" FROM "+plan.rel+" WHERE "+where+" "+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var value any
	found := 0
	for rows.Next() {
		found++
		if found > 1 {
			return nil, ErrRowAmbiguous
		}
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if found == 0 {
		return nil, ErrRowNotFound
	}
	return out, out.fill(driver, value)
}

// fill encodes a scanned value. Unlike a grid cell nothing is cut and nothing
// is guessed from the content: a binary column is bytes, whatever they spell.
func (c *CellValue) fill(driver Driver, v any) error {
	if flag, ok := oracleBoolean(driver, c.Kind, v); ok {
		c.Encoding, c.Value = "json", flag
		return nil
	}
	switch t := v.(type) {
	case nil:
		c.Encoding, c.Value = "null", nil
	case []byte:
		c.Size = int64(len(t))
		if c.Size > MaxCellBytes {
			return &CellTooLargeError{Size: c.Size}
		}
		if id, ok := mssqlGUID(driver, c.Type, t); ok {
			// The same text the grid shows, not the bytes it travels as.
			c.Encoding, c.Value = "text", id
			return nil
		}
		if c.Kind != KindBinary && driver != DriverSQLite && utf8.Valid(t) {
			c.Encoding, c.Value = "text", string(t)
		} else {
			c.Encoding, c.Value = "base64", base64.StdEncoding.EncodeToString(t)
		}
	case string:
		c.Size = int64(len(t))
		if c.Size > MaxCellBytes {
			return &CellTooLargeError{Size: c.Size}
		}
		if utf8.ValidString(t) {
			c.Encoding, c.Value = "text", t
		} else {
			// A string the engine handed over that is not text: JSON would
			// replace the bytes it cannot write.
			c.Encoding, c.Value = "base64", base64.StdEncoding.EncodeToString([]byte(t))
		}
	case time.Time:
		c.Encoding, c.Value = "text", t.UTC().Format(time.RFC3339Nano)
		c.Size = int64(len(c.Value.(string)))
	default:
		c.Encoding, c.Value = "json", normaliseValue(v)
	}
	return nil
}
