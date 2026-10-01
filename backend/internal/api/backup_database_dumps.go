package api

import (
	"context"
	"errors"
	"fmt"

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
		// A backup run records why a dump failed, and shows it.
		return backups.DatabaseDumpResult{}, withoutSecrets(dsn, err)
	}
	method := result.Tool
	if method == "" || method == dbx.BuiltInDumpTool {
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
	// The route that reaches this refuses a protected connection with its own
	// answer. This is the same refusal where nothing can come past it: a
	// restore replaces what is in the database, by whichever road it arrives.
	if conn.ReadOnly {
		return "", fmt.Errorf("%s is protected: a dump cannot be restored into it until protection is turned off in its settings", conn.Name)
	}
	// The dashboard's own sessions go first, as they do for a restore started
	// from the Databases page: a pooled one would carry plans for tables that
	// are about to be replaced, and on SQLite would hold a lock on the file.
	if conn.Driver.IsSQL() {
		d.server.modules.dbs.Close(id)
		defer d.server.modules.dbs.Close(id)
	}
	output, err := dbx.Restore(ctx, conn.Driver, dsn, database, dumpPath)
	return output, withoutSecrets(dsn, err)
}
