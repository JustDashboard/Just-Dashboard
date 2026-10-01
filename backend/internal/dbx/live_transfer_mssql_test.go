package dbx

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The built-in dump, the import and the export against a real SQL Server.
//
// The server a fixture gives is shared, and a connection string that names no
// database lands in master. So nothing here runs in the database the
// connection string names: each test makes one of its own, and drops it.

const transferMSSQLEnv = "JD_TEST_MSSQL_DSN"

// liveOwnMSSQL makes an empty database for the test on the server the
// environment names, and returns a pool on it and its connection string.
func liveOwnMSSQL(t *testing.T, name string) (*sql.DB, string) {
	t.Helper()
	base := os.Getenv(transferMSSQLEnv)
	if base == "" {
		t.Skipf("set %s to a SQL Server this test may create a database on", transferMSSQLEnv)
	}
	liveSQL(t, DriverMSSQL, transferMSSQLEnv, "")
	ctx := context.Background()
	_, _ = DropDatabase(ctx, DriverMSSQL, base, name)
	if err := CreateDatabase(ctx, DriverMSSQL, base, name); err != nil {
		t.Skipf("this login cannot create a database: %v", err)
	}
	t.Cleanup(func() { _, _ = DropDatabase(context.Background(), DriverMSSQL, base, name) })
	dsn := dsnForDatabase(DriverMSSQL, base, name)
	db, err := OpenDatabase(ctx, DriverMSSQL, base, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dsn
}

// seedMSSQLRich builds what a SQL Server database ordinarily holds and a table
// list alone does not carry: an identity column with a gap after it, a
// computed column, a rowversion, named defaults, a check, a unique constraint,
// a filtered index with an included column, a second schema, a foreign key
// that cascades, a sequence that has been drawn from, and a view over a view.
func seedMSSQLRich(t *testing.T, db *sql.DB) {
	t.Helper()
	execAll(t, db,
		`CREATE SCHEMA sales`,
		`CREATE SEQUENCE dbo.ticket_seq AS int START WITH 100 INCREMENT BY 5`,
		`CREATE TABLE dbo.people (
			id int IDENTITY(1,1) NOT NULL CONSTRAINT people_pk PRIMARY KEY,
			email nvarchar(255) NOT NULL,
			nick varchar(40) NULL,
			bio nvarchar(max) NULL,
			avatar varbinary(max) NULL,
			token uniqueidentifier NOT NULL CONSTRAINT people_token_df DEFAULT NEWID(),
			score decimal(12,2) NULL,
			balance money NULL,
			ratio float NULL,
			active bit NOT NULL CONSTRAINT people_active_df DEFAULT 1,
			born date NULL,
			woke time(3) NULL,
			seen_at datetime2(7) NULL,
			seen_legacy datetime NULL,
			seen_tz datetimeoffset NULL,
			shout AS UPPER(email) PERSISTED,
			ver rowversion,
			CONSTRAINT people_email_uq UNIQUE (email),
			CONSTRAINT people_score_ck CHECK (score IS NULL OR score >= 0)
		)`,
		`CREATE INDEX people_born_ix ON dbo.people (born DESC) INCLUDE (nick) WHERE born IS NOT NULL`,
		`CREATE TABLE sales.notes (
			id bigint IDENTITY(10,10) NOT NULL PRIMARY KEY,
			person_id int NOT NULL CONSTRAINT notes_person_fk REFERENCES dbo.people(id) ON DELETE CASCADE,
			body nvarchar(400) NOT NULL
		)`,
		`CREATE VIEW dbo.active_people AS SELECT id, email FROM dbo.people WHERE active = 1`,
		`CREATE VIEW dbo.active_count AS SELECT COUNT(*) AS n FROM dbo.active_people`,
		`INSERT INTO dbo.people (email, nick, bio, avatar, token, score, balance, ratio, active, born, woke, seen_at, seen_legacy, seen_tz) VALUES
			(N'ann@x.io', 'ann', N'Zoë says “hi”; -- not a comment', 0x00FF41, '6F9619FF-8B86-D011-B42D-00C04FC964FF', 12.50, 1234.5678, 1.5,
			 1, '1990-02-03', '07:08:09.123', '2026-03-04 05:06:07.1234567', '2026-03-04 05:06:07.123', '2026-03-04 05:06:07.1234567 +02:00'),
			(N'o''brien@x.io', NULL, NULL, NULL, '00000000-0000-0000-0000-000000000001', NULL, NULL, NULL, 0, NULL, NULL, NULL, NULL, NULL),
			(N'gone@x.io', NULL, NULL, NULL, NEWID(), NULL, NULL, NULL, 1, NULL, NULL, NULL, NULL, NULL)`,
		// The last id handed out is 3 and the row that had it is gone: the next
		// one is 4 here, and has to be 4 in a restored copy.
		`DELETE FROM dbo.people WHERE email = N'gone@x.io'`,
		`INSERT INTO sales.notes (person_id, body) VALUES (1, N'semi; colon'), (1, N'two
lines'), (2, N'x')`,
	)
	if got := queryString(t, db, `SELECT CAST(NEXT VALUE FOR dbo.ticket_seq AS varchar(20))`); got != "100" {
		t.Fatalf("the sequence began at %s", got)
	}
}

// checkMSSQLRich reads a restored copy back: the rows as they were, and the
// structure by what it enforces.
func checkMSSQLRich(t *testing.T, db *sql.DB) {
	t.Helper()
	one := func(query string) string { return queryString(t, db, query) }
	for query, want := range map[string]string{
		`SELECT CAST(COUNT(*) AS varchar(10)) FROM dbo.people`:                                                           "2",
		`SELECT CAST(COUNT(*) AS varchar(10)) FROM sales.notes`:                                                          "3",
		`SELECT bio FROM dbo.people WHERE id = 1`:                                                                        "Zoë says “hi”; -- not a comment",
		`SELECT CONVERT(varchar(20), avatar, 1) FROM dbo.people WHERE id = 1`:                                            "0x00FF41",
		`SELECT CAST(token AS varchar(40)) FROM dbo.people WHERE id = 1`:                                                 "6F9619FF-8B86-D011-B42D-00C04FC964FF",
		`SELECT CAST(score AS varchar(20)) FROM dbo.people WHERE id = 1`:                                                 "12.50",
		`SELECT CONVERT(varchar(30), balance, 2) FROM dbo.people WHERE id = 1`:                                           "1234.5678",
		`SELECT CONVERT(varchar(10), born, 23) FROM dbo.people WHERE id = 1`:                                             "1990-02-03",
		`SELECT CONVERT(varchar(20), woke, 114) FROM dbo.people WHERE id = 1`:                                            "07:08:09.123",
		`SELECT CONVERT(varchar(40), seen_at, 121) FROM dbo.people WHERE id = 1`:                                         "2026-03-04 05:06:07.1234567",
		`SELECT CONVERT(varchar(40), seen_legacy, 121) FROM dbo.people WHERE id = 1`:                                     "2026-03-04 05:06:07.123",
		`SELECT CONVERT(varchar(40), seen_tz, 121) FROM dbo.people WHERE id = 1`:                                         "2026-03-04 05:06:07.1234567 +02:00",
		`SELECT email FROM dbo.people WHERE id = 2`:                                                                      "o'brien@x.io",
		`SELECT shout FROM dbo.people WHERE id = 2`:                                                                      "O'BRIEN@X.IO",
		`SELECT body FROM sales.notes WHERE id = 20`:                                                                     "two\nlines",
		`SELECT CAST(n AS varchar(10)) FROM dbo.active_count`:                                                            "1",
		`SELECT CAST(COLUMNPROPERTY(OBJECT_ID('dbo.people'), 'shout', 'IsComputed') AS varchar(5))`:                      "1",
		`SELECT CAST(COLUMNPROPERTY(OBJECT_ID('dbo.people'), 'id', 'IsIdentity') AS varchar(5))`:                         "1",
		`SELECT TYPE_NAME(system_type_id) FROM sys.columns WHERE object_id = OBJECT_ID('dbo.people') AND name = 'ver'`:   "timestamp",
		`SELECT CAST(has_filter AS varchar(5)) + ':' + filter_definition FROM sys.indexes WHERE name = 'people_born_ix'`: "1:([born] IS NOT NULL)",
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM sys.index_columns ic JOIN sys.indexes i ON i.object_id = ic.object_id AND i.index_id = ic.index_id
			WHERE i.name = 'people_born_ix' AND (ic.is_included_column = 1 OR ic.is_descending_key = 1)`: "2",
		`SELECT name FROM sys.default_constraints WHERE parent_object_id = OBJECT_ID('dbo.people') AND name = 'people_active_df'`: "people_active_df",
		`SELECT name FROM sys.key_constraints WHERE name = 'people_email_uq'`:                                                     "people_email_uq",
		`SELECT name FROM sys.key_constraints WHERE name = 'people_pk'`:                                                           "people_pk",
		`SELECT delete_referential_action_desc FROM sys.foreign_keys WHERE name = 'notes_person_fk'`:                              "CASCADE",
	} {
		if got := one(query); got != want {
			t.Errorf("%s\n got %q\nwant %q", strings.Join(strings.Fields(query), " "), got, want)
		}
	}
	ctx := context.Background()
	// Where the counters stand. The ids 1 to 3 were handed out before the dump
	// and the row that had 3 is gone, so the rows alone would say 2. They are
	// read before anything below is tried: a refused insert uses an id up.
	for query, want := range map[string]string{
		`SELECT CAST(IDENT_CURRENT('dbo.people') AS varchar(20))`:  "3",
		`SELECT CAST(IDENT_CURRENT('sales.notes') AS varchar(20))`: "30",
	} {
		if got := one(query); got != want {
			t.Errorf("%s = %s, want %s", query, got, want)
		}
	}
	var next int64
	if err := db.QueryRowContext(ctx,
		`INSERT INTO dbo.people (email) OUTPUT CAST(inserted.id AS bigint) VALUES (N'new@x.io')`).Scan(&next); err != nil {
		t.Fatalf("an insert that leaves the id to the table: %v", err)
	}
	if next != 4 {
		t.Errorf("the next id handed out is %d, want 4", next)
	}
	if got := one(`SELECT CAST(NEXT VALUE FOR dbo.ticket_seq AS varchar(20))`); got != "105" {
		t.Errorf("the sequence hands out %s next, want 105", got)
	}
	// What the constraints refuse.
	for name, statement := range map[string]string{
		"the unique constraint": `INSERT INTO dbo.people (email) VALUES (N'ann@x.io')`,
		"the check":             `INSERT INTO dbo.people (email, score) VALUES (N'neg@x.io', -1)`,
		"the foreign key":       `INSERT INTO sales.notes (person_id, body) VALUES (999, N'orphan')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err == nil {
			t.Errorf("%s did not come back: %s was accepted", name, statement)
		}
	}
	// And the default still fills what an insert leaves out.
	if got := one(`SELECT CAST(active AS varchar(5)) + ':' + CAST(LEN(CAST(token AS varchar(40))) AS varchar(5)) FROM dbo.people WHERE email = N'new@x.io'`); got != "1:36" {
		t.Errorf("defaults on the new row: %s", got)
	}
}

// TestLiveBuiltInMSSQLDumpIsFaithful restores the built-in dump into a
// database made for it and reads the structure back out of the catalogue,
// then restores it over the database it came from.
func TestLiveBuiltInMSSQLDumpIsFaithful(t *testing.T) {
	db, dsn := liveOwnMSSQL(t, "jd_transfer_live")
	ctx := context.Background()
	seedMSSQLRich(t, db)

	var lines []string
	res, err := DumpWith(ctx, DriverMSSQL, dsn, t.TempDir(), DumpOptions{Progress: func(l string) { lines = append(lines, l) }})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	text := readDump(t, res.Path)
	if res.Tool != BuiltInDumpTool || !strings.Contains(res.Summary, "2 tables, 5 rows") || !strings.Contains(res.Summary, "2 views") {
		t.Errorf("result = %+v", res)
	}
	if strings.Contains(text, "SKIPPED") {
		t.Errorf("the dump left something out:\n%s", text)
	}
	if strings.Contains(text, "jd_transfer_live") && strings.Contains(text, "[jd_transfer_live]") {
		t.Errorf("the dump names its database in a statement, so it cannot be loaded into another:\n%s", text)
	}

	target := scratchDatabase(t, DriverMSSQL, dsn, "copy")
	if out, err := RestoreWith(ctx, DriverMSSQL, dsn, res.Path, RestoreOptions{Database: target}); err != nil {
		t.Fatalf("RestoreWith into %s: %v\n%s\n%s", target, err, out, text)
	}
	restored, err := OpenDatabase(ctx, DriverMSSQL, dsn, target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	checkMSSQLRich(t, restored)
	if t.Failed() {
		t.Logf("the dump:\n%s", text)
	}

	// Over the database it came from, with its rows changed since.
	execAll(t, db,
		`DELETE FROM sales.notes`,
		`UPDATE dbo.people SET email = N'changed@x.io' WHERE id = 1`,
		`DROP VIEW dbo.active_count`,
	)
	if _, err := RestoreWith(ctx, DriverMSSQL, dsn, res.Path, RestoreOptions{}); err != nil {
		t.Fatalf("RestoreWith over itself: %v", err)
	}
	checkMSSQLRich(t, db)
}

// TestLiveBuiltInMSSQLDumpStaysInItsDatabase is the property a shared server
// depends on: a dump of one database holds that database and no other, and
// loading it back touches no other. The login here is the administrator's,
// which can see and drop everything on the server.
func TestLiveBuiltInMSSQLDumpStaysInItsDatabase(t *testing.T) {
	db, dsn := liveOwnMSSQL(t, "jd_transfer_live")
	other, _ := liveOwnMSSQL(t, "jd_transfer_other")
	ctx := context.Background()
	seedMSSQLRich(t, db)
	// Another database on the same server, with a table of the same name and
	// a sequence of the same name.
	execAll(t, other,
		`CREATE TABLE dbo.people (id int PRIMARY KEY, email nvarchar(255))`,
		`INSERT INTO dbo.people VALUES (7, N'not yours')`,
		`CREATE TABLE dbo.jd_other_only (id int PRIMARY KEY)`,
		`CREATE SEQUENCE dbo.ticket_seq START WITH 9000`,
	)

	res, err := DumpWith(ctx, DriverMSSQL, dsn, t.TempDir(), DumpOptions{Compression: CompressionGzip})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	if !strings.HasSuffix(res.Path, ".sql.gz") {
		t.Errorf("a compressed dump is called %s", res.File)
	}
	text := readDump(t, res.Path)
	for _, foreign := range []string{"jd_transfer_other", "jd_other_only", "not yours", "9000", "USE ", "[master]", "spt_"} {
		if strings.Contains(text, foreign) {
			t.Errorf("the dump of one database holds %q", foreign)
		}
	}
	// Nothing in the file says which database it is for beyond its header, so
	// a statement can only land where the session is.
	for _, statement := range splitSQLStatements(DriverMSSQL, text) {
		if strings.Contains(statement, "jd_transfer_live") {
			t.Errorf("a statement names the database: %s", statement)
		}
	}

	if _, err := RestoreWith(ctx, DriverMSSQL, dsn, res.Path, RestoreOptions{}); err != nil {
		t.Fatalf("RestoreWith: %v", err)
	}
	for query, want := range map[string]string{
		`SELECT email FROM dbo.people WHERE id = 7`:                                   "not yours",
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM dbo.people`:                         "1",
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM dbo.jd_other_only`:                  "0",
		`SELECT CAST(NEXT VALUE FOR dbo.ticket_seq AS varchar(20))`:                   "9000",
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM sys.tables WHERE is_ms_shipped = 0`: "2",
	} {
		if got := queryString(t, other, query); got != want {
			t.Errorf("after a restore next door, %s = %q, want %q", query, got, want)
		}
	}
}

// TestLiveMSSQLRestoreThatFailsChangesNothing loads a dump whose last
// statement is refused. The tables it had already dropped and refilled by
// then are as they were before it began.
func TestLiveMSSQLRestoreThatFailsChangesNothing(t *testing.T) {
	db, dsn := liveOwnMSSQL(t, "jd_transfer_live")
	ctx := context.Background()
	seedMSSQLRich(t, db)
	res, err := DumpWith(ctx, DriverMSSQL, dsn, t.TempDir(), DumpOptions{})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	// The same dump with a row added that its own check constraint refuses:
	// the rows go in, and the constraint cannot be put back over them.
	text := readDump(t, res.Path)
	broken := strings.Replace(text, "SET IDENTITY_INSERT [dbo].[people] OFF;",
		"INSERT INTO [dbo].[people] ([id], [email], [token], [active], [score]) VALUES (50, N'neg@x.io', 0x00000000000000000000000000000050, 1, -5);\nSET IDENTITY_INSERT [dbo].[people] OFF;", 1)
	if broken == text {
		t.Fatalf("the dump has no line to break:\n%s", text)
	}
	path := res.Path + ".broken.sql"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	execAll(t, db, `UPDATE dbo.people SET nick = 'since' WHERE id = 1`, `INSERT INTO sales.notes (person_id, body) VALUES (2, N'since the dump')`)
	_, err = RestoreWith(ctx, DriverMSSQL, dsn, path, RestoreOptions{})
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") || !strings.Contains(err.Error(), "people_score_ck") {
		t.Fatalf("a dump that cannot be loaded: %v", err)
	}
	for query, want := range map[string]string{
		`SELECT nick FROM dbo.people WHERE id = 1`:                                               "since",
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM sales.notes`:                                   "4",
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM dbo.people`:                                    "2",
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM dbo.active_count`:                              "1",
		`SELECT name FROM sys.foreign_keys WHERE name = 'notes_person_fk'`:                       "notes_person_fk",
		`SELECT CAST(COLUMNPROPERTY(OBJECT_ID('dbo.people'), 'id', 'IsIdentity') AS varchar(5))`: "1",
	} {
		if got := queryString(t, db, query); got != want {
			t.Errorf("after a restore that failed, %s = %q, want %q", query, got, want)
		}
	}
}

// TestLiveMSSQLDumpHonoursItsOptions takes the narrower dumps: the structure
// alone, the rows alone, and some of the tables.
func TestLiveMSSQLDumpHonoursItsOptions(t *testing.T) {
	db, dsn := liveOwnMSSQL(t, "jd_transfer_live")
	ctx := context.Background()
	seedMSSQLRich(t, db)

	t.Run("structure_only", func(t *testing.T) {
		res, err := DumpWith(ctx, DriverMSSQL, dsn, t.TempDir(), DumpOptions{SchemaOnly: true})
		if err != nil {
			t.Fatalf("DumpWith: %v", err)
		}
		text := readDump(t, res.Path)
		if strings.Contains(text, "INSERT INTO") || strings.Contains(text, "IDENTITY_INSERT") || !strings.Contains(res.Summary, "structure only") {
			t.Errorf("a dump of the structure carries rows (%s):\n%s", res.Summary, text)
		}
		target := scratchDatabase(t, DriverMSSQL, dsn, "shape")
		if _, err := RestoreWith(ctx, DriverMSSQL, dsn, res.Path, RestoreOptions{Database: target}); err != nil {
			t.Fatalf("RestoreWith: %v\n%s", err, text)
		}
		shape, err := OpenDatabase(ctx, DriverMSSQL, dsn, target)
		if err != nil {
			t.Fatal(err)
		}
		defer shape.Close()
		// Empty, and numbering from the start: an identity nobody has drawn
		// from hands out its seed, and a sequence its first value.
		execAll(t, shape, `INSERT INTO dbo.people (email) VALUES (N'first@x.io')`, `INSERT INTO sales.notes (person_id, body) VALUES (1, N'n')`)
		for query, want := range map[string]string{
			`SELECT CAST(id AS varchar(10)) FROM dbo.people`:            "1",
			`SELECT CAST(id AS varchar(10)) FROM sales.notes`:           "10",
			`SELECT CAST(NEXT VALUE FOR dbo.ticket_seq AS varchar(20))`: "100",
			`SELECT CAST(n AS varchar(10)) FROM dbo.active_count`:       "1",
		} {
			if got := queryString(t, shape, query); got != want {
				t.Errorf("%s = %q, want %q", query, got, want)
			}
		}
	})

	t.Run("rows_only", func(t *testing.T) {
		res, err := DumpWith(ctx, DriverMSSQL, dsn, t.TempDir(), DumpOptions{DataOnly: true})
		if err != nil {
			t.Fatalf("DumpWith: %v", err)
		}
		text := readDump(t, res.Path)
		if strings.Contains(text, "CREATE ") || strings.Contains(text, "DROP ") || strings.Contains(text, "ALTER TABLE") {
			t.Errorf("a dump of the rows carries structure:\n%s", text)
		}
		// A parent's rows before its children's: the keys are enforced while
		// these go in.
		if strings.Index(text, "INSERT INTO [dbo].[people]") > strings.Index(text, "INSERT INTO [sales].[notes]") {
			t.Errorf("a child's rows are written before its parent's:\n%s", text)
		}
		execAll(t, db, `DELETE FROM sales.notes`, `DELETE FROM dbo.people`)
		if _, err := RestoreWith(ctx, DriverMSSQL, dsn, res.Path, RestoreOptions{}); err != nil {
			t.Fatalf("RestoreWith: %v\n%s", err, text)
		}
		checkMSSQLRich(t, db)
		execAll(t, db, `DELETE FROM dbo.people WHERE email = N'new@x.io'`, `DBCC CHECKIDENT ('dbo.people', RESEED, 3) WITH NO_INFOMSGS`,
			`ALTER SEQUENCE dbo.ticket_seq RESTART WITH 105`)
	})

	t.Run("some_tables", func(t *testing.T) {
		// The child alone. Its parent stays as it is, the key between them is
		// taken off for the drop and put back after.
		res, err := DumpWith(ctx, DriverMSSQL, dsn, t.TempDir(), DumpOptions{Tables: []string{"sales.notes"}})
		if err != nil {
			t.Fatalf("DumpWith: %v", err)
		}
		text := readDump(t, res.Path)
		if strings.Contains(text, "CREATE TABLE [dbo].[people]") || strings.Contains(text, "CREATE VIEW") || strings.Contains(text, "ticket_seq") {
			t.Errorf("a dump of one table carries others:\n%s", text)
		}
		execAll(t, db, `DELETE FROM sales.notes WHERE id = 10`)
		if _, err := RestoreWith(ctx, DriverMSSQL, dsn, res.Path, RestoreOptions{}); err != nil {
			t.Fatalf("RestoreWith: %v\n%s", err, text)
		}
		// The parent alone. The child this dump leaves out points at it, and
		// a table something points at cannot be dropped.
		res, err = DumpWith(ctx, DriverMSSQL, dsn, t.TempDir(), DumpOptions{ExcludeTables: []string{"sales.notes"}})
		if err != nil {
			t.Fatalf("DumpWith: %v", err)
		}
		text = readDump(t, res.Path)
		execAll(t, db, `UPDATE dbo.people SET nick = 'changed' WHERE id = 1`)
		if _, err := RestoreWith(ctx, DriverMSSQL, dsn, res.Path, RestoreOptions{}); err != nil {
			t.Fatalf("RestoreWith: %v\n%s", err, text)
		}
		for query, want := range map[string]string{
			`SELECT nick FROM dbo.people WHERE id = 1`:                                                   "ann",
			`SELECT CAST(COUNT(*) AS varchar(5)) FROM sales.notes`:                                       "3",
			`SELECT delete_referential_action_desc FROM sys.foreign_keys WHERE name = 'notes_person_fk'`: "CASCADE",
		} {
			if got := queryString(t, db, query); got != want {
				t.Errorf("%s = %q, want %q", query, got, want)
			}
		}
		if _, err := DumpWith(ctx, DriverMSSQL, dsn, t.TempDir(), DumpOptions{Tables: []string{"no_such_table"}}); err == nil ||
			!strings.Contains(err.Error(), "no such table to dump") {
			t.Errorf("a table that is not there: %v", err)
		}
		// A view and a sequence are there to be asked for by name, with or
		// without a schema; a view nobody named is not dragged in.
		res, err = DumpWith(ctx, DriverMSSQL, dsn, t.TempDir(), DumpOptions{Tables: []string{"dbo.people", "dbo.active_people", "ticket_seq"}})
		if err != nil {
			t.Fatalf("a dump that names a view and a sequence: %v", err)
		}
		text = readDump(t, res.Path)
		if !strings.Contains(text, "CREATE VIEW dbo.active_people") || strings.Contains(text, "active_count") ||
			!strings.Contains(text, "CREATE SEQUENCE [dbo].[ticket_seq]") || strings.Contains(text, "DROP SEQUENCE") {
			t.Errorf("a dump of a table, a view and a sequence by name:\n%s", text)
		}
		if _, err := DumpWith(ctx, DriverMSSQL, dsn, t.TempDir(), DumpOptions{Tables: []string{"sales.active_people"}}); err == nil {
			t.Error("a view asked for in a schema it is not in was found")
		}
	})
}

// TestLiveMSSQLImport loads files into SQL Server: enough rows to go in
// several statements, with rows among them the table refuses for each of the
// reasons it can.
func TestLiveMSSQLImport(t *testing.T) {
	db, _ := liveOwnMSSQL(t, "jd_transfer_live")
	ctx := context.Background()
	execAll(t, db, `CREATE TABLE dbo.things (
		id int NOT NULL PRIMARY KEY,
		name nvarchar(20) NOT NULL CONSTRAINT things_name UNIQUE,
		price money NULL,
		qty smallint NULL,
		born date NULL,
		seen datetime NULL,
		seen2 datetime2(3) NULL,
		tz datetimeoffset(0) NULL,
		token uniqueidentifier NULL,
		raw varbinary(max) NULL,
		live bit NULL
	)`)
	count := func() string { return queryString(t, db, `SELECT CAST(COUNT(*) AS varchar(10)) FROM dbo.things`) }

	// 1500 rows, four of them wrong, each in a different way. A value that
	// does not convert used to end the whole transaction on this engine, so
	// "skip the bad rows" lost the good ones before it and failed at the end.
	var file strings.Builder
	file.WriteString("id,name,price,qty\n")
	for i := 1; i <= 1500; i++ {
		id, name, price, qty := strconv.Itoa(i), "n"+strconv.Itoa(i), "1.5", "2"
		switch i {
		case 700:
			qty = "many" // not a number
		case 900:
			id = "5" // a key already used
		case 1100:
			name = strings.Repeat("x", 40) // longer than the column
		case 1300:
			qty = "99999" // a number the column cannot hold
		}
		file.WriteString(id + "," + name + "," + price + "," + qty + "\n")
	}
	report, err := Import(ctx, db, DriverMSSQL, strings.NewReader(file.String()),
		ImportSpec{Schema: "dbo", Table: "things", SkipBadRows: true})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 1496 || report.Skipped != 4 || len(report.Errors) != 4 || !report.Atomic || count() != "1496" {
		t.Fatalf("inserted %d, skipped %d, %d rows in the table: %+v", report.Inserted, report.Skipped, len(report.Errors), report.Errors)
	}
	for i, want := range []struct {
		line int
		says string
	}{
		{701, `qty: "many" is not a value a smallint column takes`},
		{901, "things"},
		{1101, "truncated"},
		{1301, `qty: "99999" is not a value a smallint column takes`},
	} {
		if e := report.Errors[i]; e.Line != want.line || !strings.Contains(e.Message, want.says) {
			t.Errorf("error %d = line %d %q, want line %d saying %q", i, e.Line, e.Message, want.line, want.says)
		}
	}
	// The statement a report shows is the one that writes a row.
	if !strings.HasPrefix(report.Statement, "INSERT INTO [dbo].[things] (") {
		t.Errorf("statement = %s", report.Statement)
	}

	// Without leave to skip, the first of them ends the import and the table
	// is as it was.
	execAll(t, db, `DELETE FROM dbo.things`)
	_, err = Import(ctx, db, DriverMSSQL, strings.NewReader(file.String()), ImportSpec{Schema: "dbo", Table: "things"})
	if err == nil || !strings.Contains(err.Error(), "row 700 (line 701)") || !strings.Contains(err.Error(), `"many"`) || count() != "0" {
		t.Fatalf("an import with a bad row: %v (%s rows)", err, count())
	}

	// Every kind of value this engine converts for itself, bound as the text
	// a file holds.
	report, err = Import(ctx, db, DriverMSSQL, strings.NewReader(
		"id,name,price,qty,born,seen,seen2,tz,token,raw,live\n"+
			`1,ann,1234.5678,3,2026-03-04,2026-03-04 05:06:07,2026-03-04T05:06:07.123Z,2026-03-04T05:06:07+02:00,6F9619FF-8B86-D011-B42D-00C04FC964FF,\x00ff41,true`+"\n"+
			"2,bo,,,,,,,,,\n"),
		ImportSpec{Schema: "dbo", Table: "things"})
	if err != nil || report.Inserted != 2 {
		t.Fatalf("Import: %+v, %v", report, err)
	}
	checkThings := func(t *testing.T, db *sql.DB) {
		t.Helper()
		for query, want := range map[string]string{
			`SELECT CONVERT(varchar(30), price, 2) FROM dbo.things WHERE id = 1`:                                                                   "1234.5678",
			`SELECT CONVERT(varchar(10), born, 23) FROM dbo.things WHERE id = 1`:                                                                   "2026-03-04",
			`SELECT CONVERT(varchar(30), seen, 121) FROM dbo.things WHERE id = 1`:                                                                  "2026-03-04 05:06:07.000",
			`SELECT CONVERT(varchar(30), seen2, 121) FROM dbo.things WHERE id = 1`:                                                                 "2026-03-04 05:06:07.123",
			`SELECT CONVERT(varchar(40), tz, 121) FROM dbo.things WHERE id = 1`:                                                                    "2026-03-04 05:06:07 +02:00",
			`SELECT CAST(token AS varchar(40)) FROM dbo.things WHERE id = 1`:                                                                       "6F9619FF-8B86-D011-B42D-00C04FC964FF",
			`SELECT CONVERT(varchar(20), raw, 1) FROM dbo.things WHERE id = 1`:                                                                     "0x00FF41",
			`SELECT CAST(live AS varchar(5)) FROM dbo.things WHERE id = 1`:                                                                         "1",
			`SELECT CAST(COUNT(*) AS varchar(5)) FROM dbo.things WHERE id = 2 AND raw IS NULL AND price IS NULL AND seen IS NULL AND live IS NULL`: "1",
		} {
			if got := queryString(t, db, query); got != want {
				t.Errorf("%s = %q, want %q", strings.Join(strings.Fields(query), " "), got, want)
			}
		}
	}
	checkThings(t, db)

	// The inline route's import skips a value that does not convert as well.
	execAll(t, db, `DELETE FROM dbo.things`)
	res, err := ImportCSV(ctx, db, DriverMSSQL, strings.NewReader("id,name,qty\n1,a,1\n2,b,many\n3,c,3\n"),
		ImportOptions{Schema: "dbo", Table: "things", HasHeader: true})
	if err != nil || res.Inserted != 2 || res.Failed != 1 || count() != "2" {
		t.Fatalf("the inline route: %+v, %v (%s rows)", res, err, count())
	}

	// An upsert finds rows by key, and says which it added and which it
	// overwrote — past a row it has to leave out.
	report, err = Import(ctx, db, DriverMSSQL, strings.NewReader("id,name,qty\n1,a,10\n4,d,many\n5,e,5\n"),
		ImportSpec{Schema: "dbo", Table: "things", Mode: ImportModeUpsert, SkipBadRows: true})
	if err != nil || report.Inserted != 1 || report.Updated != 1 || report.Skipped != 1 || count() != "3" {
		t.Fatalf("upsert: %+v, %v (%s rows)", report, err, count())
	}
	if got := queryString(t, db, `SELECT CAST(qty AS varchar(10)) FROM dbo.things WHERE id = 1`); got != "10" {
		t.Errorf("the row that was there holds qty %s", got)
	}
}

// TestLiveMSSQLReadsDatesTheSameForEveryLogin imports and restores as a login
// whose language puts the day before the month. A date written year first is
// the same date for it as for anyone: the old datetime type, left to itself,
// reads 2026-03-04 as the third of April for such a login.
func TestLiveMSSQLReadsDatesTheSameForEveryLogin(t *testing.T) {
	db, dsn := liveOwnMSSQL(t, "jd_transfer_live")
	ctx := context.Background()
	const login, password = "jd_transfer_british", "JdTest#2024british"
	dropLogin := func() {
		_, _ = db.ExecContext(context.Background(), `IF SUSER_ID('`+login+`') IS NOT NULL DROP LOGIN `+login)
	}
	if _, err := db.ExecContext(ctx, `IF SUSER_ID('`+login+`') IS NOT NULL DROP LOGIN `+login); err != nil {
		t.Skipf("this login cannot manage logins: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE LOGIN `+login+` WITH PASSWORD = '`+password+`', DEFAULT_LANGUAGE = British, CHECK_POLICY = OFF`); err != nil {
		t.Skipf("this login cannot create one to test with: %v", err)
	}
	// After the database it is a user of has gone.
	defer dropLogin()
	execAll(t, db,
		`CREATE USER `+login+` FOR LOGIN `+login,
		`ALTER ROLE db_owner ADD MEMBER `+login,
		`CREATE TABLE dbo.seen (id int NOT NULL PRIMARY KEY, at datetime NULL, short smalldatetime NULL, day date NULL)`,
	)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(login, password)
	british := u.String()
	asBritish, err := OpenDatabase(ctx, DriverMSSQL, british, "")
	if err != nil {
		t.Fatalf("connecting as %s: %v", login, err)
	}
	defer asBritish.Close()
	if got := queryString(t, asBritish, `SELECT @@LANGUAGE`); got != "British" {
		t.Fatalf("the login's language is %s", got)
	}

	report, err := Import(ctx, asBritish, DriverMSSQL, strings.NewReader(
		"id,at,short,day\n1,2026-03-04 05:06:07,2026-03-04 05:06,2026-03-04\n2,2026-03-04T05:06:07.123Z,2026-03-04T05:06:00,2026-03-04\n"),
		ImportSpec{Schema: "dbo", Table: "seen"})
	if err != nil || report.Inserted != 2 {
		t.Fatalf("Import: %+v, %v", report, err)
	}
	inMarch := func(t *testing.T, db *sql.DB) {
		t.Helper()
		if got := queryString(t, db, `SELECT CAST(COUNT(*) AS varchar(5)) FROM dbo.seen
			WHERE MONTH(at) = 3 AND DAY(at) = 4 AND MONTH(short) = 3 AND DAY(short) = 4 AND MONTH(day) = 3 AND DAY(day) = 4`); got != "2" {
			t.Errorf("%s of 2 rows hold the fourth of March", got)
		}
	}
	inMarch(t, db)

	// And a dump taken and loaded by that login puts the same dates back.
	res, err := DumpWith(ctx, DriverMSSQL, british, t.TempDir(), DumpOptions{})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	execAll(t, db, `DELETE FROM dbo.seen`)
	if _, err := RestoreWith(ctx, DriverMSSQL, british, res.Path, RestoreOptions{}); err != nil {
		t.Fatalf("RestoreWith: %v\n%s", err, readDump(t, res.Path))
	}
	inMarch(t, db)
}

// TestLiveMSSQLExportImportsBack exports a table that has every kind of
// column the server fills in itself, empties it, and imports the export. The
// file carries the computed column and the rowversion like any other; those
// are left out, the ids it carries are kept, and what is exported after is
// what was exported before.
func TestLiveMSSQLExportImportsBack(t *testing.T) {
	db, _ := liveOwnMSSQL(t, "jd_transfer_live")
	ctx := context.Background()
	seedMSSQLRich(t, db)
	export := func(format ExportFormat, columns ...string) string {
		t.Helper()
		var out strings.Builder
		if _, _, err := ExportSelection(ctx, db, DriverMSSQL, BrowseOptions{Schema: "dbo", Table: "people", OrderBy: "id"},
			ExportOptions{Format: format, Columns: columns}, &out); err != nil {
			t.Fatalf("export as %s: %v", format, err)
		}
		return out.String()
	}
	// Everything but the rowversion, which is a different stamp every time a
	// row is written.
	stable := []string{"id", "email", "nick", "bio", "avatar", "token", "score", "balance", "ratio", "active",
		"born", "woke", "seen_at", "seen_legacy", "seen_tz", "shout"}
	before := export(ExportCSV, stable...)
	// An identifier as the identifier it is, and a time of day as one.
	for _, want := range []string{"6F9619FF-8B86-D011-B42D-00C04FC964FF", ",07:08:09.123,", "1234.5678", `\x00ff41`} {
		if !strings.Contains(before, want) {
			t.Errorf("the export has no %s:\n%s", want, before)
		}
	}
	for _, format := range []ExportFormat{ExportCSV, ExportNDJSON} {
		file := export(format)
		execAll(t, db, `DELETE FROM sales.notes`, `DELETE FROM dbo.people`)
		report, err := Import(ctx, db, DriverMSSQL, strings.NewReader(file),
			ImportSpec{Schema: "dbo", Table: "people", Format: ImportFormat(format)})
		if err != nil || report.Inserted != 2 {
			t.Fatalf("importing the %s export: %+v, %v", format, report, err)
		}
		warned := strings.Join(report.Warnings, "\n")
		if !strings.Contains(warned, "shout is filled in by the server") || !strings.Contains(warned, "ver is filled in by the server") {
			t.Errorf("warnings = %q", report.Warnings)
		}
		if after := export(ExportCSV, stable...); after != before {
			t.Errorf("after importing its own %s export the table reads\n%s\nwas\n%s", format, after, before)
		}
		// The session that was allowed to give ids is not left allowed to:
		// an insert that leaves the id to the table still works on every
		// connection of the pool, and gets one past those the file carried.
		for i := 0; i < 6; i++ {
			execAll(t, db, `INSERT INTO dbo.people (email) VALUES (N'probe`+strconv.Itoa(i)+`@x.io')`)
		}
		if got := queryString(t, db, `SELECT CAST(MIN(id) AS varchar(10)) FROM dbo.people WHERE email LIKE N'probe%'`); got == "1" || got == "2" {
			t.Errorf("a row added after the import was given id %s", got)
		}
		execAll(t, db, `DELETE FROM dbo.people WHERE email LIKE N'probe%'`)
	}
}

// TestLiveMSSQLDumpOfTheLessOrdinary dumps the shapes a schema has less often
// and a dump gets wrong more easily — a primary key that is not the clustered
// index, a column with a collation of its own, a constraint that is switched
// off over rows that break it, an alias type, two tables that point at each
// other, a view the server keeps the rows of, a sequence that has gone round
// — and the ones it does not carry, which it has to name rather than write
// as something they are not.
func TestLiveMSSQLDumpOfTheLessOrdinary(t *testing.T) {
	db, dsn := liveOwnMSSQL(t, "jd_transfer_live")
	ctx := context.Background()
	execAll(t, db,
		`CREATE TYPE dbo.code_t FROM varchar(10) NOT NULL`,
		`CREATE TABLE dbo.odd (
			id int NOT NULL CONSTRAINT odd_pk PRIMARY KEY NONCLUSTERED,
			code dbo.code_t,
			exact varchar(10) COLLATE Latin1_General_BIN NULL,
			n int NULL CONSTRAINT odd_n_ck CHECK (n > 0)
		)`,
		`CREATE CLUSTERED INDEX odd_code_cx ON dbo.odd (code)`,
		`CREATE NONCLUSTERED COLUMNSTORE INDEX odd_cs ON dbo.odd (n)`,
		`CREATE INDEX odd_off_ix ON dbo.odd (n)`,
		`ALTER INDEX odd_off_ix ON dbo.odd DISABLE`,
		`INSERT INTO dbo.odd VALUES (1, 'a', 'Aa', 5), (2, 'b', 'aa', 6)`,
		// Switched off, and then a row that breaks it.
		`ALTER TABLE dbo.odd NOCHECK CONSTRAINT odd_n_ck`,
		`INSERT INTO dbo.odd VALUES (3, 'c', NULL, -5)`,
		// Each points at the other: neither can be created, or dropped, first.
		`CREATE TABLE dbo.hen (id int NOT NULL PRIMARY KEY, egg_id int NULL)`,
		`CREATE TABLE dbo.egg (id int NOT NULL PRIMARY KEY, hen_id int NULL CONSTRAINT egg_hen_fk REFERENCES dbo.hen(id))`,
		`ALTER TABLE dbo.hen ADD CONSTRAINT hen_egg_fk FOREIGN KEY (egg_id) REFERENCES dbo.egg(id)`,
		`INSERT INTO dbo.hen VALUES (1, NULL)`, `INSERT INTO dbo.egg VALUES (1, 1)`, `UPDATE dbo.hen SET egg_id = 1`,
		`CREATE TABLE dbo.versioned (
			id int NOT NULL PRIMARY KEY, v int,
			since datetime2 GENERATED ALWAYS AS ROW START NOT NULL,
			until datetime2 GENERATED ALWAYS AS ROW END NOT NULL,
			PERIOD FOR SYSTEM_TIME (since, until)
		) WITH (SYSTEM_VERSIONING = ON (HISTORY_TABLE = dbo.versioned_history))`,
		`CREATE VIEW dbo.secret WITH ENCRYPTION AS SELECT 1 AS x`,
		`CREATE VIEW dbo.totals WITH SCHEMABINDING AS SELECT code, COUNT_BIG(*) AS n FROM dbo.odd GROUP BY code`,
		`CREATE UNIQUE CLUSTERED INDEX totals_cx ON dbo.totals (code)`,
		`CREATE SEQUENCE dbo.round AS tinyint START WITH 1 INCREMENT BY 1 MINVALUE 1 MAXVALUE 3 CYCLE NO CACHE`,
	)
	// The server will not drop a table it keeps history for while it does.
	t.Cleanup(func() { db.Exec(`ALTER TABLE dbo.versioned SET (SYSTEM_VERSIONING = OFF)`) })
	for i := 0; i < 3; i++ {
		queryString(t, db, `SELECT CAST(NEXT VALUE FOR dbo.round AS varchar(5))`)
	}

	res, err := DumpWith(ctx, DriverMSSQL, dsn, t.TempDir(), DumpOptions{})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	text := readDump(t, res.Path)
	// What it does not carry it names, in the file and in the summary.
	for _, left := range []string{"[dbo].[versioned]", "[dbo].[versioned_history]", "odd_cs", "odd_off_ix", "[dbo].[secret]"} {
		if !strings.Contains(text, "-- SKIPPED") || !strings.Contains(text, left) {
			t.Errorf("the dump does not say it left out %s:\n%s", left, text)
		}
	}
	if !strings.Contains(res.Summary, "3 tables, 5 rows") || !strings.Contains(res.Summary, "skipped 5") {
		t.Errorf("summary = %q", res.Summary)
	}
	if strings.Contains(text, "CREATE TABLE [dbo].[versioned") || strings.Contains(text, "code_t") {
		t.Errorf("the dump writes what it cannot carry:\n%s", text)
	}

	target := scratchDatabase(t, DriverMSSQL, dsn, "copy")
	if _, err := RestoreWith(ctx, DriverMSSQL, dsn, res.Path, RestoreOptions{Database: target}); err != nil {
		t.Fatalf("RestoreWith: %v\n%s", err, text)
	}
	restored, err := OpenDatabase(ctx, DriverMSSQL, dsn, target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	for query, want := range map[string]string{
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM dbo.odd`:                                                                                                 "3",
		`SELECT type_desc FROM sys.indexes WHERE name = 'odd_pk'`:                                                                                          "NONCLUSTERED",
		`SELECT type_desc FROM sys.indexes WHERE name = 'odd_code_cx'`:                                                                                     "CLUSTERED",
		`SELECT collation_name FROM sys.columns WHERE object_id = OBJECT_ID('dbo.odd') AND name = 'exact'`:                                                 "Latin1_General_BIN",
		`SELECT TYPE_NAME(user_type_id) + ':' + CAST(is_nullable AS varchar(1)) FROM sys.columns WHERE object_id = OBJECT_ID('dbo.odd') AND name = 'code'`: "varchar:0",
		`SELECT CAST(is_disabled AS varchar(1)) FROM sys.check_constraints WHERE name = 'odd_n_ck'`:                                                        "1",
		`SELECT CAST(n AS varchar(5)) FROM dbo.odd WHERE id = 3`:                                                                                           "-5",
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM sys.foreign_keys WHERE name IN ('egg_hen_fk', 'hen_egg_fk')`:                                             "2",
		`SELECT CAST(egg_id AS varchar(5)) FROM dbo.hen WHERE id = 1`:                                                                                      "1",
		`SELECT CAST(n AS varchar(5)) FROM dbo.totals WITH (NOEXPAND) WHERE code = 'a'`:                                                                    "1",
		`SELECT name FROM sys.indexes WHERE object_id = OBJECT_ID('dbo.totals')`:                                                                           "totals_cx",
		// Three were drawn of a round of three: the next is the first again.
		`SELECT CAST(NEXT VALUE FOR dbo.round AS varchar(5))`:                                                             "1",
		`SELECT CAST(is_cycling AS varchar(1)) + ':' + TYPE_NAME(system_type_id) FROM sys.sequences WHERE name = 'round'`: "1:tinyint",
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM sys.tables WHERE name LIKE 'versioned%'`:                                "0",
	} {
		if got := queryString(t, restored, query); got != want {
			t.Errorf("%s\n got %q\nwant %q", strings.Join(strings.Fields(query), " "), got, want)
		}
	}
	if t.Failed() {
		t.Logf("the dump:\n%s", text)
	}

	// Over the database it came from: the two tables that point at each other
	// are dropped and remade, and the tables the dump does not carry are as
	// they were.
	execAll(t, db, `INSERT INTO dbo.versioned (id, v) VALUES (1, 1)`, `UPDATE dbo.hen SET egg_id = NULL`)
	if _, err := RestoreWith(ctx, DriverMSSQL, dsn, res.Path, RestoreOptions{}); err != nil {
		t.Fatalf("RestoreWith over itself: %v", err)
	}
	for query, want := range map[string]string{
		`SELECT CAST(egg_id AS varchar(5)) FROM dbo.hen WHERE id = 1`:                                 "1",
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM dbo.versioned`:                                      "1",
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM dbo.secret`:                                         "1",
		`SELECT CAST(COUNT(*) AS varchar(5)) FROM sys.indexes WHERE name IN ('odd_cs', 'odd_off_ix')`: "0",
	} {
		if got := queryString(t, db, query); got != want {
			t.Errorf("after restoring over itself, %s = %q, want %q", query, got, want)
		}
	}
}
