package api

import (
	"context"
	"errors"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// backupDatabaseDumper is the Databases owner's side of a backup job's native
// dumps. It opens a saved connection the same way every Databases route does
// (sealed DSN, contained again against the current roots) and delegates the
// dump and restore to dbx, which already knows each engine's native tool and
// its built-in fallback. Backups never sees a credential.
type backupDatabaseDumper struct {
	server *Server
}

func (d *backupDatabaseDumper) DescribeDatabase(ctx context.Context, id int64) (backups.DatabaseDescription, error) {
	conn, _, err := d.server.dbConnRow(ctx, id)
	if err != nil {
		return backups.DatabaseDescription{}, errors.New("database connection was not found")
	}
	return backups.DatabaseDescription{ID: conn.ID, Name: conn.Name, Driver: string(conn.Driver), Database: conn.Database}, nil
}

func (d *backupDatabaseDumper) DumpDatabase(ctx context.Context, id int64, directory string) (backups.DatabaseDumpResult, error) {
	conn, dsn, err := d.server.dbConnRow(ctx, id)
	if err != nil {
		return backups.DatabaseDumpResult{}, errors.New("database connection was not found")
	}
	result, err := dbx.Dump(ctx, conn.Driver, dsn, "", directory)
	if err != nil {
		return backups.DatabaseDumpResult{}, err
	}
	method := strings.TrimPrefix(result.Summary, "written by ")
	if method == "" {
		method = "built-in dump"
	}
	return backups.DatabaseDumpResult{
		Description: backups.DatabaseDescription{ID: conn.ID, Name: conn.Name, Driver: string(conn.Driver), Database: result.Database},
		Path:        result.Path, Method: method,
	}, nil
}

func (d *backupDatabaseDumper) RestoreDatabase(ctx context.Context, id int64, database, dumpPath string) (string, error) {
	conn, dsn, err := d.server.dbConnRow(ctx, id)
	if err != nil {
		return "", errors.New("database connection was not found")
	}
	return dbx.Restore(ctx, conn.Driver, dsn, database, dumpPath)
}
