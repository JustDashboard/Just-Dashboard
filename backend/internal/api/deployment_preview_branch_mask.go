package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// Masking a branch's chosen columns.
//
// A masked value keeps its shape: every letter becomes a letter of the same
// case, every digit a digit, and everything else — an @, a dot, a dash, a
// space — stays where it was, so an e-mail address still reads as one, a
// phone number still fits its column, and a UUID is still a UUID. The
// replacement is drawn from a keyed hash of the value with a key made for
// the branch and forgotten after it: the same value masks the same way
// throughout the branch, so a join on a masked column still joins, and a
// short value cannot be recovered by hashing guesses at it. NULL stays NULL.
//
// The copy is read and written through the readers and writers the Databases
// pages use (dbx.BrowseTablePage, dbx.ApplyChanges), a page of rows at a time
// by primary key, as the server's own superuser on the branch, before the
// branch is marked ready, so nothing but the restore has read the column.

// previewBranchMaskRows bounds one column: every row is read and written
// back, and a branch is a preview's database, not a warehouse's.
const previewBranchMaskRows = 200_000

// mask overwrites the branch's masked columns and answers how many values
// it wrote.
func (o *previewBranchOwner) mask(ctx context.Context, branch deploy.PreviewBranch, made *madeCopy, network string, port int) (int64, error) {
	address, err := o.server.modules.dataCopies.copyAddress(ctx, deploy.DataCopy{Container: made.copy.Container, Network: network})
	if err != nil {
		return 0, err
	}
	// The copy's superuser still signs in with production's password: the
	// branch's own passwords are given after the masking.
	superuser := dataCopySecret{User: made.engine.user, Password: made.engine.password}
	target, err := copyDSN(made.conn.Driver, made.dsn, made.info, address, port, superuser)
	if err != nil {
		return 0, err
	}
	db, err := dbx.OpenDatabase(ctx, made.conn.Driver, target, made.info.Database)
	if err != nil {
		return 0, fmt.Errorf("the branch could not be reached: %w", err)
	}
	defer db.Close()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return 0, err
	}
	var total int64
	for _, name := range branch.MaskedColumns {
		schema, table, column := splitMaskedColumn(name, made.conn.Driver, made.info.Database)
		written, err := maskColumn(ctx, db, made.conn.Driver, key, schema, table, column)
		if err != nil {
			return total, fmt.Errorf("%s: %w", name, err)
		}
		total += written
	}
	return total, nil
}

// splitMaskedColumn reads table.column, or schema.table.column, with the
// engine's default schema: PostgreSQL's public, MySQL's database itself.
func splitMaskedColumn(name string, driver dbx.Driver, database string) (schema, table, column string) {
	parts := strings.Split(name, ".")
	if len(parts) == 3 {
		return parts[0], parts[1], parts[2]
	}
	schema = "public"
	if driver == dbx.DriverMySQL {
		schema = database
	}
	return schema, parts[0], parts[1]
}

func maskColumn(ctx context.Context, db *sql.DB, driver dbx.Driver, key []byte, schema, table, column string) (int64, error) {
	columns, err := dbx.ListColumns(ctx, db, driver, schema, table)
	if err != nil {
		return 0, fmt.Errorf("its table could not be read: %w", err)
	}
	var found *dbx.Column
	for index := range columns {
		if columns[index].Name == column || (driver == dbx.DriverMySQL && strings.EqualFold(columns[index].Name, column)) {
			found = &columns[index]
			break
		}
	}
	if found == nil {
		return 0, errors.New("the branch has no such column")
	}
	kind := dbx.ValueKind(driver, found.Type)
	upper := strings.ToUpper(found.Type)
	switch {
	case kind == dbx.KindText && (strings.HasPrefix(upper, "ENUM") || strings.HasPrefix(upper, "SET") || strings.HasPrefix(upper, "XML")):
		return 0, fmt.Errorf("a %s column keeps only the values it allows, so it is not masked", strings.ToLower(found.Type))
	case kind == dbx.KindText, kind == dbx.KindInteger, kind == dbx.KindDecimal, kind == dbx.KindUUID:
	default:
		return 0, fmt.Errorf("a %s column is not masked: text, numbers and UUIDs are", strings.ToLower(found.Type))
	}
	var written int64
	for offset := 0; ; offset += dbx.MaxBrowseRows {
		if offset >= previewBranchMaskRows {
			return written, fmt.Errorf("the table holds more than %d rows to mask", previewBranchMaskRows)
		}
		page, err := dbx.BrowseTablePage(ctx, db, driver, dbx.BrowseOptions{
			Schema: schema, Table: table, Limit: dbx.MaxBrowseRows, Offset: offset,
		})
		if err != nil {
			return written, fmt.Errorf("its rows could not be read: %w", err)
		}
		if len(page.PrimaryKey) == 0 {
			return written, errors.New("its table has no primary key to write each row back by")
		}
		position := map[string]int{}
		for index, name := range page.Columns {
			position[name] = index
		}
		at, ok := position[found.Name]
		if !ok {
			return written, errors.New("the column was not read")
		}
		changes := []dbx.Change{}
		for _, row := range page.Rows {
			value, present := maskCellText(row[at])
			if !present {
				continue
			}
			rowKey := map[string]any{}
			for _, name := range page.PrimaryKey {
				if name == found.Name {
					return written, errors.New("it is part of its table's primary key, which a branch keeps")
				}
				index, ok := position[name]
				if !ok {
					return written, errors.New("the primary key was not read")
				}
				rowKey[name] = row[index]
			}
			masked, err := maskShape(key, value, kind)
			if err != nil {
				return written, err
			}
			if masked == value {
				continue
			}
			var typed any = masked
			if kind == dbx.KindInteger {
				if number, err := strconv.ParseInt(masked, 10, 64); err == nil {
					typed = number
				}
			}
			changes = append(changes, dbx.Change{Op: dbx.ChangeUpdate, Key: rowKey, Values: map[string]any{found.Name: typed}})
		}
		if len(changes) > 0 {
			if _, err := dbx.ApplyChanges(ctx, db, driver, dbx.ChangeSet{Schema: schema, Table: table, Changes: changes}); err != nil {
				return written, fmt.Errorf("the masked values could not be written: %w", err)
			}
			written += int64(len(changes))
		}
		if len(page.Rows) < dbx.MaxBrowseRows {
			return written, nil
		}
	}
}

// maskCellText is a cell as the text it is masked as; false is NULL.
func maskCellText(cell any) (string, bool) {
	switch value := cell.(type) {
	case nil:
		return "", false
	case string:
		return value, true
	case []byte:
		return string(value), true
	case int64:
		return strconv.FormatInt(value, 10), true
	case int:
		return strconv.Itoa(value), true
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), true
	default:
		return fmt.Sprint(value), true
	}
}

// maskShape is the masked value: letters for letters and digits for digits,
// drawn from HMAC-SHA256 of the value, everything else kept. A number keeps
// its sign and point and never gains a leading zero, so it stays the number
// of digits it was.
func maskShape(key []byte, value string, kind string) (string, error) {
	var stream []byte
	block := uint32(0)
	next := func() byte {
		if len(stream) == 0 {
			mac := hmac.New(sha256.New, key)
			mac.Write([]byte(value))
			var counter [4]byte
			binary.BigEndian.PutUint32(counter[:], block)
			mac.Write(counter[:])
			stream = mac.Sum(nil)
			block++
		}
		b := stream[0]
		stream = stream[1:]
		return b
	}
	var out strings.Builder
	out.Grow(len(value))
	leading := true
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z' && kind != dbx.KindInteger && kind != dbx.KindDecimal:
			if kind == dbx.KindUUID && r <= 'f' {
				out.WriteByte("0123456789abcdef"[next()%16])
				continue
			}
			out.WriteByte(byte('a' + next()%26))
		case r >= 'A' && r <= 'Z' && kind != dbx.KindInteger && kind != dbx.KindDecimal:
			if kind == dbx.KindUUID && r <= 'F' {
				out.WriteByte("0123456789ABCDEF"[next()%16])
				continue
			}
			out.WriteByte(byte('A' + next()%26))
		case r >= '0' && r <= '9':
			if kind == dbx.KindUUID {
				out.WriteByte("0123456789abcdef"[next()%16])
				continue
			}
			digit := next() % 10
			if leading && (kind == dbx.KindInteger || kind == dbx.KindDecimal) && r != '0' {
				digit = 1 + next()%9
			}
			out.WriteByte('0' + digit)
			leading = false
		case (kind == dbx.KindInteger || kind == dbx.KindDecimal) && strings.ContainsRune("+-.eE", r):
			out.WriteRune(r)
			if r == '.' {
				leading = false
			}
		case kind == dbx.KindInteger || kind == dbx.KindDecimal:
			return "", fmt.Errorf("a number read as %q is not masked", value)
		default:
			out.WriteRune(r)
		}
	}
	return out.String(), nil
}
