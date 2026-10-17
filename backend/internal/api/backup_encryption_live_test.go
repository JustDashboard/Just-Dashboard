package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// A native dump travels inside an encrypted archive and comes back through the
// Databases owner: the engine's dump of a database of the test's own is sealed
// with the archive, then restored into a drill database on the same server
// and read there. JD_TEST_POSTGRES_DSN and JD_TEST_MYSQL_ADMIN_DSN (an account
// that may create databases) choose the servers.
func TestLiveEncryptedArchiveRestoresANativeDump(t *testing.T) {
	const source, drill = "jdt_backup_encryption_src", "jdt_backup_encryption_drill"
	for _, engine := range []struct {
		name, variable string
		driver         dbx.Driver
		// in is the server's DSN pointing at one database.
		in    func(dsn, database string) string
		table string
	}{
		{"postgres", "JD_TEST_POSTGRES_DSN", dbx.DriverPostgres, func(dsn, database string) string {
			head, query, _ := strings.Cut(dsn, "?")
			return head[:strings.LastIndex(head, "/")+1] + database + "?" + query
		}, "CREATE TABLE jdt_backup_encryption_orders (id integer PRIMARY KEY, note text NOT NULL)"},
		{"mariadb", "JD_TEST_MYSQL_ADMIN_DSN", dbx.DriverMySQL, func(dsn, database string) string {
			return dsn[:strings.LastIndex(dsn, "/")+1] + database
		}, "CREATE TABLE jdt_backup_encryption_orders (id int PRIMARY KEY, note varchar(64) NOT NULL)"},
	} {
		t.Run(engine.name, func(t *testing.T) {
			dsn := os.Getenv(engine.variable)
			if dsn == "" {
				t.Skipf("set %s", engine.variable)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			// The server's own database is only where the two of the test's
			// are made from; the connection the job dumps is the source.
			s, _, serverID := liveAPIRouter(t, engine.driver, dsn)
			server, _, err := s.dbPool(ctx, serverID)
			if err != nil {
				t.Fatal(err)
			}
			drop := func() {
				for _, name := range []string{source, drill} {
					statement := "DROP DATABASE IF EXISTS " + name
					if engine.driver == dbx.DriverPostgres {
						statement += " WITH (FORCE)"
					}
					if _, err := server.ExecContext(context.Background(), statement); err != nil {
						t.Errorf("%s: %v", statement, err)
					}
				}
			}
			drop()
			t.Cleanup(drop)
			for _, name := range []string{source, drill} {
				if _, err := server.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
					t.Fatal(err)
				}
			}
			sealed, err := s.Sealer.Seal(engine.in(dsn, source))
			if err != nil {
				t.Fatal(err)
			}
			res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
				"jdt_backup_encryption_"+engine.name, string(engine.driver), sealed, 0)
			if err != nil {
				t.Fatal(err)
			}
			connID, _ := res.LastInsertId()
			pool, _, err := s.dbPool(ctx, connID)
			if err != nil {
				t.Fatal(err)
			}
			for _, statement := range []string{engine.table, `INSERT INTO jdt_backup_encryption_orders VALUES (1, 'sealed-canary')`} {
				if _, err := pool.ExecContext(ctx, statement); err != nil {
					t.Fatalf("%s: %v", statement, err)
				}
			}

			job, err := s.modules.backupStore.Create(ctx, &backups.Job{
				Name: "jdt-backup-encryption-" + engine.name, DatabaseDumps: []int64{connID}, Encrypt: true,
				TargetKind: backups.TargetLocal, Target: backups.TargetConfig{Path: filepath.Join(s.Cfg.FileRoots[0], "archives")},
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			run, err := s.modules.backupRunner.Execute(ctx, job.ID, "test")
			if err != nil || run.Status != backups.StatusSuccess {
				t.Fatalf("run = %+v, %v", run, err)
			}
			raw, err := os.ReadFile(run.Artifact)
			if err != nil || !strings.HasPrefix(string(raw), "age-encryption.org/v1\n") || strings.Contains(string(raw), "sealed-canary") {
				t.Fatalf("the archive on disk is not sealed: %v", err)
			}
			t.Logf("%s dump sealed with the archive: %s, %d bytes", engine.name, run.Manifest.DatabaseDumps[0].Method, run.Manifest.DatabaseDumps[0].Bytes)
			if err := s.modules.backupRunner.VerifyDatabaseCoverage(ctx, run.ID, []int64{connID}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.modules.backupRunner.RestoreDatabase(ctx, run.ID, connID, drill); err != nil {
				t.Fatal(err)
			}
			restored, err := s.modules.dbs.Pool(ctx, connID+1000, engine.driver, engine.in(dsn, drill))
			if err != nil {
				t.Fatal(err)
			}
			defer s.modules.dbs.Close(connID + 1000)
			var note string
			if err := restored.QueryRowContext(ctx, `SELECT note FROM jdt_backup_encryption_orders WHERE id = 1`).Scan(&note); err != nil || note != "sealed-canary" {
				t.Fatalf("drill database holds %q, %v", note, err)
			}
		})
	}
}
