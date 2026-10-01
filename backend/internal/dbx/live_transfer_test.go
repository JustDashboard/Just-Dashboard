package dbx

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Dumps that have to bring back more than rows, against real servers.
//
// The round trip in live_dump_test.go proves a dump restores. These prove what
// it restores: the index that kept a query fast, the constraint that refused a
// duplicate, the view an application reads through — and that a dump of one
// database is a dump of that database and no other.

// execAll runs statements in order and fails the test on the first error.
func execAll(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, s := range statements {
		if _, err := db.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
}

// readDump returns a dump's text, decompressed when it was written compressed.
func readDump(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(raw, []byte{0x1f, 0x8b}) {
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if raw, err = io.ReadAll(gz); err != nil {
			t.Fatal(err)
		}
	}
	return string(raw)
}

// scratchDatabase makes an empty database beside the fixture's own and drops
// it when the test ends. Its name carries the fixture's, so two runs against
// one server stay out of each other's way.
func scratchDatabase(t *testing.T, driver Driver, dsn, suffix string) string {
	t.Helper()
	info, err := ParseDSN(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := info.Database + "_" + suffix
	ctx := context.Background()
	_, _ = DropDatabase(ctx, driver, dsn, name)
	if err := CreateDatabase(ctx, driver, dsn, name); err != nil {
		t.Skipf("this login cannot create a database to restore into: %v", err)
	}
	t.Cleanup(func() { _, _ = DropDatabase(context.Background(), driver, dsn, name) })
	return name
}

func queryString(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var out sql.NullString
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&out); err != nil {
		t.Fatalf("%v\n%s", err, query)
	}
	return out.String
}

const pgRichSchema = "jd_rich"

// seedPostgresRich builds a schema that uses what a real one uses and the old
// generated DDL could not spell: an enum, an array, an identity column, a
// generated column, a partitioned table, a sequence of its own, a check, a
// partial index, a view over a view and a materialized view.
func seedPostgresRich(t *testing.T, db *sql.DB) {
	t.Helper()
	execAll(t, db,
		`DROP SCHEMA IF EXISTS `+pgRichSchema+` CASCADE`,
		`CREATE SCHEMA `+pgRichSchema,
		`CREATE TYPE jd_rich.mood AS ENUM ('calm', 'it''s complicated', 'loud')`,
		`CREATE SEQUENCE jd_rich.ticket_seq START WITH 100 INCREMENT BY 5`,
		`CREATE TABLE jd_rich.people (
			id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			email text NOT NULL,
			mood jd_rich.mood NOT NULL DEFAULT 'calm',
			tags text[] NOT NULL DEFAULT '{}',
			profile jsonb,
			avatar bytea,
			score numeric(12,2),
			ratio double precision,
			seen_at timestamptz,
			born date,
			shout text GENERATED ALWAYS AS (upper(email)) STORED,
			ticket integer NOT NULL DEFAULT nextval('jd_rich.ticket_seq'),
			CONSTRAINT people_email_key UNIQUE (email),
			CONSTRAINT people_score_check CHECK (score IS NULL OR score >= 0)
		)`,
		`COMMENT ON TABLE jd_rich.people IS 'Everyone; it''s a test'`,
		`COMMENT ON COLUMN jd_rich.people.email IS 'How to reach them'`,
		`CREATE INDEX people_calm_idx ON jd_rich.people (seen_at DESC) WHERE mood = 'calm'`,
		`CREATE INDEX people_tags_gin ON jd_rich.people USING gin (tags)`,
		`CREATE TABLE jd_rich.notes (
			id serial PRIMARY KEY,
			person_id bigint NOT NULL REFERENCES jd_rich.people(id) ON DELETE CASCADE,
			body text NOT NULL
		)`,
		`CREATE TABLE jd_rich.events (
			id bigint NOT NULL,
			at date NOT NULL,
			what text,
			PRIMARY KEY (id, at)
		) PARTITION BY RANGE (at)`,
		`CREATE TABLE jd_rich.events_2025 PARTITION OF jd_rich.events FOR VALUES FROM ('2025-01-01') TO ('2026-01-01')`,
		`CREATE TABLE jd_rich.events_2026 PARTITION OF jd_rich.events FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')`,
		`CREATE INDEX events_what_idx ON jd_rich.events (what)`,
		`CREATE VIEW jd_rich.calm_people AS SELECT id, email FROM jd_rich.people WHERE mood = 'calm'`,
		`CREATE VIEW jd_rich.calm_count AS SELECT count(*) AS n FROM jd_rich.calm_people`,
		`CREATE MATERIALIZED VIEW jd_rich.note_totals AS
			SELECT person_id, count(*) AS notes FROM jd_rich.notes GROUP BY person_id`,
		`CREATE UNIQUE INDEX note_totals_person ON jd_rich.note_totals (person_id)`,
		`INSERT INTO jd_rich.people (email, mood, tags, profile, avatar, score, ratio, seen_at, born) VALUES
			('ann@x.io', 'calm', '{a,"b c"}', '{"k": [1, 2]}', '\x00ff41', 12.50, 'NaN', '2026-03-04 05:06:07.123456+02', '1990-02-03'),
			('o''brien@x.io', 'it''s complicated', '{}', NULL, NULL, NULL, 1.5, NULL, NULL)`,
		`INSERT INTO jd_rich.notes (person_id, body) VALUES (1, 'semi; colon -- and a dash'), (1, E'two\nlines'), (2, 'x')`,
		`INSERT INTO jd_rich.events VALUES (1, '2025-06-01', 'old'), (2, '2026-06-01', 'new')`,
		`REFRESH MATERIALIZED VIEW jd_rich.note_totals`,
		`SELECT nextval('jd_rich.ticket_seq')`,
	)
}

// TestLiveBuiltInPostgresDumpIsFaithful restores the built-in dump into an
// empty database and reads the structure back out of the catalogue.
func TestLiveBuiltInPostgresDumpIsFaithful(t *testing.T) {
	const env, fallback = "JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable"
	db := liveSQL(t, DriverPostgres, env, fallback)
	dsn := liveDSN(t, env, fallback)
	ctx := context.Background()
	seedPostgresRich(t, db)
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS ` + pgRichSchema + ` CASCADE`) })

	var lines []string
	res, err := DumpWith(ctx, DriverPostgres, dsn, t.TempDir(), DumpOptions{
		builtIn: true, Progress: func(l string) { lines = append(lines, l) },
	})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	if res.Tool != BuiltInDumpTool {
		t.Fatalf("tool = %q, want the built-in dumper", res.Tool)
	}
	text := readDump(t, res.Path)
	if strings.Contains(text, "USER-DEFINED") || strings.Contains(text, " ARRAY") {
		t.Errorf("the dump carries information_schema's placeholder type names:\n%s", text)
	}
	if len(lines) == 0 {
		t.Error("the dump reported no progress")
	}

	target := scratchDatabase(t, DriverPostgres, dsn, "r1")
	out, err := RestoreWith(ctx, DriverPostgres, dsn, res.Path, RestoreOptions{Database: target})
	if err != nil {
		t.Fatalf("RestoreWith: %v\n%s", err, text)
	}
	t.Logf("%s; %s", res.Summary, out)

	restored, err := OpenDatabase(ctx, DriverPostgres, dsn, target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()

	for query, want := range map[string]string{
		`SELECT count(*)::text FROM jd_rich.people`:                                  "2",
		`SELECT count(*)::text FROM jd_rich.notes`:                                   "3",
		`SELECT count(*)::text FROM jd_rich.events`:                                  "2",
		`SELECT count(*)::text FROM jd_rich.events_2025`:                             "1",
		`SELECT mood::text FROM jd_rich.people WHERE id = 2`:                         "it's complicated",
		`SELECT tags[2] FROM jd_rich.people WHERE id = 1`:                            "b c",
		`SELECT profile->'k'->>1 FROM jd_rich.people WHERE id = 1`:                   "2",
		`SELECT encode(avatar, 'hex') FROM jd_rich.people WHERE id = 1`:              "00ff41",
		`SELECT score::text FROM jd_rich.people WHERE id = 1`:                        "12.50",
		`SELECT ratio::text FROM jd_rich.people WHERE id = 1`:                        "NaN",
		`SELECT (seen_at AT TIME ZONE 'UTC')::text FROM jd_rich.people WHERE id = 1`: "2026-03-04 03:06:07.123456",
		`SELECT born::text FROM jd_rich.people WHERE id = 1`:                         "1990-02-03",
		`SELECT shout FROM jd_rich.people WHERE id = 1`:                              "ANN@X.IO",
		`SELECT body FROM jd_rich.notes WHERE id = 1`:                                "semi; colon -- and a dash",
		`SELECT body FROM jd_rich.notes WHERE id = 2`:                                "two\nlines",
		`SELECT n::text FROM jd_rich.calm_count`:                                     "1",
		`SELECT notes::text FROM jd_rich.note_totals WHERE person_id = 1`:            "2",
		`SELECT obj_description('jd_rich.people'::regclass)`:                         "Everyone; it's a test",
		`SELECT col_description('jd_rich.people'::regclass, 2)`:                      "How to reach them",
		`SELECT count(*)::text FROM pg_indexes WHERE schemaname = 'jd_rich' AND indexname = 'people_calm_idx' AND indexdef LIKE '%WHERE%'`: "1",
		`SELECT count(*)::text FROM pg_indexes WHERE schemaname = 'jd_rich' AND indexname = 'people_tags_gin' AND indexdef LIKE '%gin%'`:   "1",
		`SELECT count(*)::text FROM pg_indexes WHERE schemaname = 'jd_rich' AND indexname = 'note_totals_person'`:                          "1",
		`SELECT count(*)::text FROM pg_indexes WHERE schemaname = 'jd_rich' AND tablename = 'events_2026' AND indexdef LIKE '%(what)%'`:    "1",
		`SELECT count(*)::text FROM pg_constraint WHERE conname = 'people_score_check'`:                                                    "1",
		`SELECT count(*)::text FROM pg_constraint WHERE conname = 'people_email_key' AND contype = 'u'`:                                    "1",
		`SELECT count(*)::text FROM pg_constraint WHERE conname = 'notes_person_id_fkey' AND contype = 'f'`:                                "1",
		`SELECT relkind::text FROM pg_class WHERE oid = 'jd_rich.events'::regclass`:                                                        "p",
		// The sequences are where they were: the next ticket follows the last
		// one handed out, and the next identity value the last row.
		`SELECT nextval('jd_rich.ticket_seq')::text`:                           "115",
		`SELECT nextval(pg_get_serial_sequence('jd_rich.people', 'id'))::text`: "3",
		`SELECT nextval('jd_rich.notes_id_seq')::text`:                         "4",
	} {
		if got := queryString(t, restored, query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}
	// The constraints are enforced, not merely listed.
	if _, err := restored.ExecContext(ctx, `INSERT INTO jd_rich.people (email) VALUES ('ann@x.io')`); err == nil {
		t.Error("the unique constraint did not come back: a duplicate was accepted")
	}
	if _, err := restored.ExecContext(ctx, `INSERT INTO jd_rich.notes (person_id, body) VALUES (99, 'orphan')`); err == nil {
		t.Error("the foreign key did not come back: an orphan was accepted")
	}

	// And a second restore over the first leaves the same thing, which is what
	// restoring into the database a dump came from is.
	if _, err := RestoreWith(ctx, DriverPostgres, dsn, res.Path, RestoreOptions{Database: target}); err != nil {
		t.Fatalf("restoring over the restored database: %v", err)
	}
	if got := queryString(t, restored, `SELECT count(*)::text FROM jd_rich.notes`); got != "3" {
		t.Errorf("notes after a second restore = %s, want 3", got)
	}
}

// TestLivePostgresNativeDumpHonoursItsOptions drives pg_dump itself: the
// options reach it, its own lines come back as progress, and what it wrote is
// recorded with the version that wrote it.
func TestLivePostgresNativeDumpHonoursItsOptions(t *testing.T) {
	const env, fallback = "JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable"
	db := liveSQL(t, DriverPostgres, env, fallback)
	dsn := liveDSN(t, env, fallback)
	ctx := context.Background()
	if tool := postgresTool("pg_dump", postgresServerMajor(ctx, dsn)); !toolAvailable(tool) {
		t.Skip("pg_dump is not installed here")
	}
	seedPostgresRich(t, db)
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS ` + pgRichSchema + ` CASCADE`) })

	var lines []string
	res, err := DumpWith(ctx, DriverPostgres, dsn, t.TempDir(), DumpOptions{
		SchemaOnly: true, Tables: []string{"jd_rich.people", "jd_rich.notes"},
		Progress: func(l string) { lines = append(lines, l) },
	})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	if res.Tool != "pg_dump" || res.ToolVersion == "" {
		t.Errorf("tool = %q version %q, want pg_dump and its version", res.Tool, res.ToolVersion)
	}
	if len(lines) == 0 {
		t.Error("pg_dump's own output did not come back as progress")
	}
	for _, line := range lines {
		if strings.Contains(line, "jdtest@") || strings.Contains(line, "PGPASSWORD") {
			t.Errorf("a progress line carries a credential: %q", line)
		}
	}

	target := scratchDatabase(t, DriverPostgres, dsn, "r2")
	// The schema and the enum are not part of a dump of two tables.
	scratch, err := OpenDatabase(ctx, DriverPostgres, dsn, target)
	if err != nil {
		t.Fatal(err)
	}
	defer scratch.Close()
	execAll(t, scratch, `CREATE SCHEMA jd_rich`, `CREATE TYPE jd_rich.mood AS ENUM ('calm', 'it''s complicated', 'loud')`,
		`CREATE SEQUENCE jd_rich.ticket_seq START WITH 100 INCREMENT BY 5`)
	var restoreLines []string
	if _, err := RestoreWith(ctx, DriverPostgres, dsn, res.Path, RestoreOptions{
		Database: target, Progress: func(l string) { restoreLines = append(restoreLines, l) },
	}); err != nil {
		t.Fatalf("RestoreWith: %v", err)
	}
	if len(restoreLines) == 0 {
		t.Error("pg_restore's own output did not come back as progress")
	}
	for query, want := range map[string]string{
		`SELECT count(*)::text FROM jd_rich.people`:                                           "0",
		`SELECT count(*)::text FROM jd_rich.notes`:                                            "0",
		`SELECT count(*)::text FROM pg_class WHERE relname = 'events' AND relkind = 'p'`:      "0",
		`SELECT count(*)::text FROM pg_class WHERE relname = 'people_tags_gin'`:               "1",
		`SELECT count(*)::text FROM pg_class WHERE relname = 'notes' AND relkind = 'r'`:       "1",
		`SELECT count(*)::text FROM pg_class WHERE relname = 'calm_people' AND relkind = 'v'`: "0",
	} {
		if got := queryString(t, scratch, query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}

	// Leaving a table out is the other spelling of the same choice.
	res, err = DumpWith(ctx, DriverPostgres, dsn, t.TempDir(), DumpOptions{
		ExcludeTables: []string{"jd_rich.notes"}, Compression: CompressionNone,
	})
	if err != nil {
		t.Fatalf("DumpWith(exclude): %v", err)
	}
	listing, err := toolRun{name: postgresTool("pg_restore", 0), args: []string{"--list", res.Path}}.run(ctx)
	if err != nil {
		t.Fatalf("pg_restore --list: %v", err)
	}
	if strings.Contains(listing, "TABLE DATA jd_rich notes") || !strings.Contains(listing, "TABLE DATA jd_rich people") {
		t.Errorf("the archive does not hold what was asked for:\n%s", listing)
	}
}

// A dump that is stopped stops: nothing is left that looks like a backup, and
// the dashboard does not go and take one by another route instead.
func TestLivePostgresDumpStopsWhenCancelled(t *testing.T) {
	const env, fallback = "JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable"
	liveSQL(t, DriverPostgres, env, fallback)
	dsn := liveDSN(t, env, fallback)
	if !toolAvailable(postgresTool("pg_dump", 0)) {
		t.Skip("pg_dump is not installed here")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	_, err := DumpWith(ctx, DriverPostgres, dsn, dir, DumpOptions{})
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("err = %v, want the dump reported as stopped", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("a stopped dump left %d files behind", len(entries))
	}
}

// A plain-format pg_dump is a psql script. An operator who uploads one has a
// dump, and "pg_restore: input file appears to be a text format dump" is not
// an answer to that.
func TestLivePostgresRestoresAPlainSQLDump(t *testing.T) {
	const env, fallback = "JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable"
	liveSQL(t, DriverPostgres, env, fallback)
	dsn := liveDSN(t, env, fallback)
	ctx := context.Background()
	if !toolAvailable(postgresTool("psql", postgresServerMajor(ctx, dsn))) {
		t.Skip("psql is not installed here")
	}
	path := t.TempDir() + "/plain.sql"
	script := "--\n-- PostgreSQL database dump\n--\n\n" +
		"CREATE TABLE public.jd_plain (id integer, name text);\n" +
		"COPY public.jd_plain (id, name) FROM stdin;\n1\tone\n2\ttwo; still two\n\\.\n"
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dumpFormatOf(path); got != dumpFormatForeignSQL {
		t.Fatalf("dumpFormatOf = %v, want SQL somebody else wrote", got)
	}
	target := scratchDatabase(t, DriverPostgres, dsn, "r3")
	if _, err := RestoreWith(ctx, DriverPostgres, dsn, path, RestoreOptions{Database: target}); err != nil {
		t.Fatalf("RestoreWith: %v", err)
	}
	restored, err := OpenDatabase(ctx, DriverPostgres, dsn, target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if got := queryString(t, restored, `SELECT name FROM public.jd_plain WHERE id = 2`); got != "two; still two" {
		t.Errorf("row = %q", got)
	}

	// A script that fails changes nothing: it is replayed as one transaction.
	bad := t.TempDir() + "/bad.sql"
	if err := os.WriteFile(bad, []byte("CREATE TABLE public.jd_half (id integer);\nSELECT no_such_function();\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreWith(ctx, DriverPostgres, dsn, bad, RestoreOptions{Database: target}); err == nil {
		t.Fatal("a failing script was reported as restored")
	}
	if got := queryString(t, restored, `SELECT count(*)::text FROM pg_class WHERE relname = 'jd_half'`); got != "0" {
		t.Error("a failed restore left its first table behind")
	}
}

func seedMySQLRich(t *testing.T, db *sql.DB) {
	t.Helper()
	execAll(t, db,
		`DROP VIEW IF EXISTS jd_dx_summary`, `DROP VIEW IF EXISTS jd_dx_view`,
		`DROP TABLE IF EXISTS jd_dx_child`, `DROP TABLE IF EXISTS jd_dx_parent`,
		`CREATE TABLE jd_dx_parent (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(50) NOT NULL,
			price DECIMAL(10,2),
			doubled DECIMAL(12,2) AS (price * 2) STORED,
			blobby BLOB,
			at TIMESTAMP NULL,
			UNIQUE KEY jd_dx_parent_name (name),
			CONSTRAINT jd_dx_price_positive CHECK (price IS NULL OR price >= 0)
		)`,
		`CREATE TABLE jd_dx_child (
			id INT PRIMARY KEY,
			parent_id INT NOT NULL,
			note TEXT,
			KEY jd_dx_child_parent (parent_id),
			CONSTRAINT jd_dx_fk FOREIGN KEY (parent_id) REFERENCES jd_dx_parent(id)
		)`,
		`CREATE VIEW jd_dx_view AS SELECT p.id, p.name, c.note FROM jd_dx_parent p JOIN jd_dx_child c ON c.parent_id = p.id`,
		// Reads the view above, and sorts before it.
		`CREATE VIEW jd_dx_summary AS SELECT COUNT(*) AS n FROM jd_dx_view`,
		`INSERT INTO jd_dx_parent (name, price, blobby, at) VALUES
			('a\\b''c', 1.50, 0x00FF41, '2026-03-04 05:06:07'), ('second', NULL, NULL, NULL)`,
		`INSERT INTO jd_dx_child VALUES (1, 1, 'note; with -- a semicolon'), (2, 2, NULL)`,
	)
	t.Cleanup(func() {
		for _, s := range []string{
			`DROP VIEW IF EXISTS jd_dx_summary`, `DROP VIEW IF EXISTS jd_dx_view`,
			`DROP TABLE IF EXISTS jd_dx_child`, `DROP TABLE IF EXISTS jd_dx_parent`,
		} {
			db.Exec(s)
		}
	})
}

func checkMySQLRich(t *testing.T, db *sql.DB) {
	t.Helper()
	for query, want := range map[string]string{
		`SELECT COUNT(*) FROM jd_dx_parent`:                 "2",
		`SELECT COUNT(*) FROM jd_dx_child`:                  "2",
		`SELECT name FROM jd_dx_parent WHERE id = 1`:        `a\b'c`,
		`SELECT HEX(blobby) FROM jd_dx_parent WHERE id = 1`: "00FF41",
		`SELECT doubled FROM jd_dx_parent WHERE id = 1`:     "3.00",
		`SELECT note FROM jd_dx_child WHERE id = 1`:         "note; with -- a semicolon",
		`SELECT n FROM jd_dx_summary`:                       "2",
		`SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND INDEX_NAME = 'jd_dx_child_parent'`:               "1",
		`SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS WHERE TABLE_SCHEMA = DATABASE() AND CONSTRAINT_NAME = 'jd_dx_fk'`:             "1",
		`SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS WHERE TABLE_SCHEMA = DATABASE() AND CONSTRAINT_NAME = 'jd_dx_parent_name'`:    "1",
		`SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS WHERE TABLE_SCHEMA = DATABASE() AND CONSTRAINT_NAME = 'jd_dx_price_positive'`: "1",
	} {
		if got := queryString(t, db, query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}
	if _, err := db.Exec(`INSERT INTO jd_dx_child VALUES (9, 99, 'orphan')`); err == nil {
		t.Error("the foreign key did not come back: an orphan was accepted")
	}
}

// TestLiveBuiltInMySQLDumpStaysInItsDatabase is the regression for the dump
// that listed every database the account could see: the file held their
// tables, and the restore began by dropping them.
func TestLiveBuiltInMySQLDumpStaysInItsDatabase(t *testing.T) {
	const env = "JD_TEST_MYSQL_DSN"
	db := liveSQL(t, DriverMySQL, env, "jdtest:jdtest@tcp(127.0.0.1:3306)/jdtest")
	dsn := liveDSN(t, env, "jdtest:jdtest@tcp(127.0.0.1:3306)/jdtest")
	ctx := context.Background()
	info, err := ParseDSN(DriverMySQL, dsn)
	if err != nil {
		t.Fatal(err)
	}
	seedMySQLRich(t, db)

	res, err := DumpWith(ctx, DriverMySQL, dsn, t.TempDir(), DumpOptions{builtIn: true, Compression: CompressionGzip})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	if !strings.HasSuffix(res.Path, ".sql.gz") {
		t.Errorf("a compressed dump is called %s", res.File)
	}
	text := readDump(t, res.Path)

	// Every database this account can see, other than its own, must be absent
	// from the file: not as a qualifier, not as a table.
	rows, err := db.QueryContext(ctx, `SELECT TABLE_SCHEMA, TABLE_NAME FROM information_schema.TABLES
		WHERE TABLE_SCHEMA NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys') AND TABLE_SCHEMA <> DATABASE()`)
	if err != nil {
		t.Fatal(err)
	}
	foreign := 0
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			t.Fatal(err)
		}
		foreign++
		if strings.Contains(text, "`"+schema+"`.") {
			t.Fatalf("the dump of %s names database %s", info.Database, schema)
		}
	}
	rows.Close()
	t.Logf("%d tables of other databases are visible to this account and absent from the dump", foreign)
	for _, statement := range splitSQLStatements(DriverMySQL, text) {
		if isDropStatement(statement) && !strings.Contains(statement, "jd_") {
			t.Fatalf("the dump drops something this test did not make: %s", statement)
		}
	}
	// Created in an order a restore can follow: the view that reads another
	// comes after it.
	if strings.Index(text, "VIEW `jd_dx_view`") > strings.Index(text, "VIEW `jd_dx_summary`") {
		t.Error("a view is created before the view it reads")
	}
	if strings.Contains(text, "DEFINER=") {
		t.Error("a view still names the account that defined it")
	}

	execAll(t, db, `DELETE FROM jd_dx_child`, `DELETE FROM jd_dx_parent`, `DROP VIEW jd_dx_summary`)
	if _, err := RestoreWith(ctx, DriverMySQL, dsn, res.Path, RestoreOptions{}); err != nil {
		t.Fatalf("RestoreWith: %v\n%s", err, text)
	}
	checkMySQLRich(t, db)
}

// TestLiveMySQLNativeDump drives mysqldump and the mysql client, where they
// are installed: a narrowed, compressed dump, restored into a database made
// for it.
func TestLiveMySQLNativeDump(t *testing.T) {
	const env = "JD_TEST_MYSQL8_DSN"
	if os.Getenv(env) == "" {
		t.Skipf("set %s to a MySQL server this test may write to", env)
	}
	if firstAvailableTool("mysqldump", "mariadb-dump") == "" || firstAvailableTool("mysql", "mariadb") == "" {
		t.Skip("mysqldump and mysql are not installed here")
	}
	db := liveSQL(t, DriverMySQL, env, "")
	dsn := os.Getenv(env)
	ctx := context.Background()
	seedMySQLRich(t, db)

	var lines []string
	res, err := DumpWith(ctx, DriverMySQL, dsn, t.TempDir(), DumpOptions{
		Compression: CompressionGzip, Tables: []string{"jd_dx_parent", "jd_dx_child", "jd_dx_view", "jd_dx_summary"},
		Progress: func(l string) { lines = append(lines, l) },
	})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	if res.Tool == BuiltInDumpTool || res.ToolVersion == "" {
		t.Fatalf("tool = %q version %q (%s), want mysqldump and its version", res.Tool, res.ToolVersion, res.Summary)
	}
	if !strings.HasSuffix(res.File, ".sql.gz") || len(lines) == 0 {
		t.Errorf("file %s, %d progress lines", res.File, len(lines))
	}
	text := readDump(t, res.Path)
	if !strings.Contains(text, "jd_dx_parent") {
		t.Fatalf("the dump does not hold the table:\n%.400s", text)
	}

	execAll(t, db, `DELETE FROM jd_dx_child`, `DELETE FROM jd_dx_parent`)
	if _, err := RestoreWith(ctx, DriverMySQL, dsn, res.Path, RestoreOptions{}); err != nil {
		t.Fatalf("RestoreWith: %v", err)
	}
	checkMySQLRich(t, db)

	// Data only: the rows, and no statement that would recreate the table.
	res, err = DumpWith(ctx, DriverMySQL, dsn, t.TempDir(), DumpOptions{DataOnly: true, Tables: []string{"jd_dx_parent"}})
	if err != nil {
		t.Fatalf("DumpWith(data only): %v", err)
	}
	if text := readDump(t, res.Path); strings.Contains(text, "CREATE TABLE") || !strings.Contains(text, "INSERT INTO") {
		t.Errorf("a data-only dump holds:\n%.600s", text)
	}
}

// --- imports, against real servers ----------------------------------------

// TestLivePostgresImport is where skipping a bad row has to be proved: on
// Postgres a refused statement spoils the transaction, so before there were
// savepoints "skip bad rows" imported nothing and said the commit had failed.
func TestLivePostgresImport(t *testing.T) {
	const env, fallback = "JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable"
	db := liveSQL(t, DriverPostgres, env, fallback)
	ctx := context.Background()
	execAll(t, db, `DROP TABLE IF EXISTS jd_imp, jd_imp_new`,
		`CREATE TABLE jd_imp (
			id integer PRIMARY KEY,
			email text NOT NULL UNIQUE,
			qty integer,
			active boolean,
			born date,
			payload bytea,
			at timestamptz
		)`,
		`INSERT INTO jd_imp (id, email, qty) VALUES (1, 'kept@x.io', 5)`)
	t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS jd_imp, jd_imp_new`) })

	body := "id,email,qty,active,born,payload,at\n" +
		"10,a@x.io,3,yes,2026-01-02,\\x00ff41,2026-03-04 05:06:07+02\n" + // 2
		"1,dupe@x.io,1,no,,,\n" + // 3: duplicate key
		"11,b@x.io,n/a,,,,\n" + // 4: not a number
		"12,c@x.io,,false,,,\n" + // 5
		"13,a@x.io,,,,,\n" + // 6: duplicate email
		"14,d@x.io,7,TRUE,,,\n" // 7
	report, err := Import(ctx, db, DriverPostgres, strings.NewReader(body),
		ImportSpec{Schema: "public", Table: "jd_imp", SkipBadRows: true, BatchSize: 4})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 3 || report.Skipped != 3 || !report.Atomic {
		t.Fatalf("report = %+v", report)
	}
	lines := map[int]bool{}
	for _, e := range report.Errors {
		lines[e.Line] = true
	}
	if !lines[3] || !lines[4] || !lines[6] {
		t.Errorf("errors name lines %v, want 3, 4 and 6: %+v", lines, report.Errors)
	}
	for query, want := range map[string]string{
		`SELECT count(*)::text FROM jd_imp`:                              "4",
		`SELECT encode(payload, 'hex') FROM jd_imp WHERE id = 10`:        "00ff41",
		`SELECT active::text FROM jd_imp WHERE id = 10`:                  "true",
		`SELECT active::text FROM jd_imp WHERE id = 12`:                  "false",
		`SELECT (at AT TIME ZONE 'UTC')::text FROM jd_imp WHERE id = 10`: "2026-03-04 03:06:07",
		`SELECT COALESCE(qty::text, 'null') FROM jd_imp WHERE id = 12`:   "null",
		`SELECT email FROM jd_imp WHERE id = 1`:                          "kept@x.io",
	} {
		if got := queryString(t, db, query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}

	// Without the option the same file changes nothing.
	if _, err := Import(ctx, db, DriverPostgres, strings.NewReader(strings.ReplaceAll(body, "10,a@x.io", "20,z@x.io")),
		ImportSpec{Schema: "public", Table: "jd_imp"}); err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("err = %v, want the import stopped at line 3", err)
	}
	if got := queryString(t, db, `SELECT count(*)::text FROM jd_imp`); got != "4" {
		t.Errorf("a failed import left %s rows", got)
	}

	// Upsert on the primary key, then on the unique constraint by name.
	report, err = Import(ctx, db, DriverPostgres, strings.NewReader("id,email,qty\n1,kept@x.io,50\n30,new@x.io,1\n"),
		ImportSpec{Table: "jd_imp", Mode: ImportModeUpsert})
	if err != nil || report.Inserted != 1 || report.Updated != 1 {
		t.Fatalf("upsert: %+v, %v", report, err)
	}
	report, err = Import(ctx, db, DriverPostgres, strings.NewReader("id,email,qty\n31,new@x.io,2\n32,newer@x.io,3\n9,bad,x\n"),
		ImportSpec{Table: "jd_imp", Mode: ImportModeUpsert, ConflictConstraint: "jd_imp_email_key", SkipBadRows: true})
	if err != nil || report.Inserted != 1 || report.Updated != 1 || report.Skipped != 1 {
		t.Fatalf("upsert on the unique constraint: %+v, %v", report, err)
	}
	if got := queryString(t, db, `SELECT id::text || ':' || qty::text FROM jd_imp WHERE email = 'new@x.io'`); got != "31:2" {
		t.Errorf("the matched row is %s, want 31:2", got)
	}

	// Replace that fails puts the rows back: TRUNCATE is undone with the rest.
	before := queryString(t, db, `SELECT count(*)::text FROM jd_imp`)
	if _, err := Import(ctx, db, DriverPostgres, strings.NewReader("id,email\n1,x@x.io\n1,y@x.io\n"),
		ImportSpec{Table: "jd_imp", Mode: ImportModeReplace}); err == nil {
		t.Fatal("a replace with a duplicate key succeeded")
	}
	if got := queryString(t, db, `SELECT count(*)::text FROM jd_imp`); got != before {
		t.Errorf("a failed replace left %s rows, want the %s there were", got, before)
	}
	report, err = Import(ctx, db, DriverPostgres, strings.NewReader("id,email\n1,only@x.io\n"),
		ImportSpec{Table: "jd_imp", Mode: ImportModeReplace})
	if err != nil || report.Inserted != 1 || queryString(t, db, `SELECT count(*)::text FROM jd_imp`) != "1" {
		t.Fatalf("replace: %+v, %v", report, err)
	}

	// A table made from the file, with the types its values look like.
	fresh := "sku,qty,price,active,added,seen,attrs,note\n" +
		"A-1,3,9.50,true,2026-01-02,2026-01-02T03:04:05Z,\"{\"\"a\"\": 1}\",first\n" +
		"A-2,9007199254740993,12,false,2026-02-03,2026-02-03 04:05:06+01,\"{\"\"b\"\": [2]}\",\n"
	report, err = Import(ctx, db, DriverPostgres, strings.NewReader(fresh), ImportSpec{
		Table: "jd_imp_new", Create: &ImportCreate{Columns: []NewColumn{{Name: "sku", PrimaryKey: true, NotNull: true}}},
	})
	if err != nil || report.Inserted != 2 || report.Create == nil || !report.Create.Created {
		t.Fatalf("create: %+v, %v", report, err)
	}
	for column, want := range map[string]string{
		"qty": "bigint", "price": "numeric", "active": "boolean", "added": "date",
		"seen": "timestamp with time zone", "attrs": "jsonb", "note": "text",
	} {
		if got := queryString(t, db, `SELECT data_type FROM information_schema.columns
			WHERE table_name = 'jd_imp_new' AND column_name = $1`, column); got != want {
			t.Errorf("%s was created as %s, want %s", column, got, want)
		}
	}
	if got := queryString(t, db, `SELECT qty::text FROM jd_imp_new WHERE sku = 'A-2'`); got != "9007199254740993" {
		t.Errorf("a 64-bit quantity arrived as %s", got)
	}
}

func TestLiveMySQLImport(t *testing.T) {
	const env = "JD_TEST_MYSQL_DSN"
	db := liveSQL(t, DriverMySQL, env, "jdtest:jdtest@tcp(127.0.0.1:3306)/jdtest")
	ctx := context.Background()
	execAll(t, db, `DROP TABLE IF EXISTS jd_imp`, `DROP TABLE IF EXISTS jd_imp_new`,
		`CREATE TABLE jd_imp (
			id INT PRIMARY KEY,
			email VARCHAR(100) NOT NULL UNIQUE,
			qty INT,
			active TINYINT(1),
			payload BLOB
		) ENGINE=InnoDB`,
		`INSERT INTO jd_imp (id, email, qty) VALUES (1, 'kept@x.io', 5), (2, 'also@x.io', 6)`)
	t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS jd_imp`); db.Exec(`DROP TABLE IF EXISTS jd_imp_new`) })

	// The schema is not named: the connection's own database is where an
	// unqualified table lives, and the import has to find it there.
	report, err := Import(ctx, db, DriverMySQL,
		strings.NewReader("id,email,qty,active,payload\n10,a@x.io,3,yes,\\x00ff41\n1,dupe@x.io,1,no,\n11,b@x.io,,false,\n"),
		ImportSpec{Table: "jd_imp", SkipBadRows: true})
	if err != nil || report.Inserted != 2 || report.Skipped != 1 || report.Errors[0].Line != 3 {
		t.Fatalf("import: %+v, %v", report, err)
	}
	for query, want := range map[string]string{
		`SELECT HEX(payload) FROM jd_imp WHERE id = 10`:          "00FF41",
		`SELECT active FROM jd_imp WHERE id = 10`:                "1",
		`SELECT active FROM jd_imp WHERE id = 11`:                "0",
		`SELECT COALESCE(qty, 'null') FROM jd_imp WHERE id = 11`: "null",
	} {
		if got := queryString(t, db, query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}

	report, err = Import(ctx, db, DriverMySQL, strings.NewReader("id,email,qty\n1,kept@x.io,50\n30,new@x.io,1\n"),
		ImportSpec{Table: "jd_imp", Mode: ImportModeUpsert})
	if err != nil || report.Inserted != 1 || report.Updated != 1 {
		t.Fatalf("upsert: %+v, %v", report, err)
	}

	// TRUNCATE commits on its own here, so a replace that then failed used to
	// leave the table empty. The rows have to still be there.
	before := queryString(t, db, `SELECT COUNT(*) FROM jd_imp`)
	if _, err := Import(ctx, db, DriverMySQL, strings.NewReader("id,email\n1,x@x.io\n1,y@x.io\n"),
		ImportSpec{Table: "jd_imp", Mode: ImportModeReplace}); err == nil {
		t.Fatal("a replace with a duplicate key succeeded")
	}
	if got := queryString(t, db, `SELECT COUNT(*) FROM jd_imp`); got != before {
		t.Errorf("a failed replace left %s rows, want the %s there were", got, before)
	}

	// A table created for an import that fails is taken away again: CREATE
	// commits here, so it is dropped by hand.
	if _, err := Import(ctx, db, DriverMySQL, strings.NewReader("k,v\n1,a\n1,b\n"),
		ImportSpec{Table: "jd_imp_new", Create: &ImportCreate{Columns: []NewColumn{{Name: "k", PrimaryKey: true}}}}); err == nil {
		t.Fatal("a duplicate key in a new table was accepted")
	}
	if got := queryString(t, db, `SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'jd_imp_new'`); got != "0" {
		t.Error("the table of a failed import was left behind")
	}
	report, err = Import(ctx, db, DriverMySQL, strings.NewReader("k,v,n\n1,a,1.5\n2,b,2\n"),
		ImportSpec{Table: "jd_imp_new", Create: &ImportCreate{}})
	if err != nil || report.Inserted != 2 {
		t.Fatalf("create: %+v, %v", report, err)
	}
}

func TestLiveClickHouseImport(t *testing.T) {
	const env = "JD_TEST_CLICKHOUSE_DSN"
	db := liveSQL(t, DriverClickHouse, env, "clickhouse://default@127.0.0.1:9000/default")
	ctx := context.Background()
	execAll(t, db, `DROP TABLE IF EXISTS jd_imp`,
		`CREATE TABLE jd_imp (id UInt64, name String, qty Nullable(Int32), ratio Float64, price Decimal(10,2), at DateTime) ENGINE = MergeTree ORDER BY id`,
		`INSERT INTO jd_imp (id, name, ratio, price, at) VALUES (1, 'old', 0, 0, now())`)
	t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS jd_imp`) })

	body := "id,name,qty,ratio,price,at\n10,ten,3,1.5,9.50,2026-01-02 03:04:05\nx,bad,,1,1,2026-01-02 03:04:05\n11,eleven,,2,12.25,2026-01-03 00:00:00\n"
	report, err := Import(ctx, db, DriverClickHouse, strings.NewReader(body), ImportSpec{Table: "jd_imp", SkipBadRows: true})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 2 || report.Skipped != 1 || report.Atomic {
		t.Fatalf("report = %+v", report)
	}
	for query, want := range map[string]string{
		`SELECT toString(count()) FROM jd_imp`:                   "3",
		`SELECT toString(price) FROM jd_imp WHERE id = 10`:       "9.5",
		`SELECT toString(ratio) FROM jd_imp WHERE id = 10`:       "1.5",
		`SELECT toString(isNull(qty)) FROM jd_imp WHERE id = 11`: "1",
	} {
		if got := queryString(t, db, query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}
	if _, err := Import(ctx, db, DriverClickHouse, strings.NewReader("id,name\n1,x\n"),
		ImportSpec{Table: "jd_imp", Mode: ImportModeUpsert}); err == nil {
		t.Error("ClickHouse accepted an upsert")
	}
	// Replace empties the table only once the new rows have been accepted.
	report, err = Import(ctx, db, DriverClickHouse, strings.NewReader("id,name,ratio,price,at\n20,only,1,1,2026-01-02 03:04:05\n"),
		ImportSpec{Table: "jd_imp", Mode: ImportModeReplace})
	if err != nil || report.Inserted != 1 {
		t.Fatalf("replace: %+v, %v", report, err)
	}
	if got := queryString(t, db, `SELECT toString(count()) FROM jd_imp`); got != "1" {
		t.Errorf("after a replace the table holds %s rows", got)
	}
}

// --- ClickHouse, Mongo and Redis dumps ---------------------------------------

// ownMongo is liveMongo with the database the connection string names, which
// on a server several runs share is the one this run may write to.
func ownMongo(t *testing.T) (*mongo.Client, string, string) {
	t.Helper()
	client, _ := liveMongo(t)
	dsn := os.Getenv("JD_TEST_MONGO_DSN")
	if dsn == "" {
		dsn = "mongodb://127.0.0.1:27017/jdtest"
	}
	info, err := ParseDSN(DriverMongo, dsn)
	if err != nil || info.Database == "" {
		t.Fatalf("the Mongo test connection names no database: %v", err)
	}
	return client, info.Database, dsn
}

func TestLiveClickHouseDumpKeepsItsTypes(t *testing.T) {
	const env = "JD_TEST_CLICKHOUSE_DSN"
	db := liveSQL(t, DriverClickHouse, env, "clickhouse://default@127.0.0.1:9000/default")
	dsn := liveDSN(t, env, "clickhouse://default@127.0.0.1:9000/default")
	ctx := context.Background()
	info, err := ParseDSN(DriverClickHouse, dsn)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := []string{`DROP VIEW IF EXISTS jd_ch_recent`, `DROP TABLE IF EXISTS jd_ch_types`}
	execAll(t, db, cleanup...)
	t.Cleanup(func() {
		for _, s := range cleanup {
			db.Exec(s)
		}
	})
	execAll(t, db,
		`CREATE TABLE jd_ch_types (
			id UInt64,
			wide UInt256,
			signed Int128,
			price Decimal(18, 4),
			at DateTime,
			precise DateTime64(3, 'Europe/Berlin'),
			day Date,
			note Nullable(String),
			tags Array(String),
			ratio Float64
		) ENGINE = MergeTree ORDER BY id`,
		`INSERT INTO jd_ch_types VALUES
			(1, 115792089237316195423570985008687907853269984665640564039457584007913129639935, -170141183460469231731687303715884105728,
			 12.3456, '2026-03-04 05:06:07', '2026-03-04 05:06:07.891', '2026-03-04', 'it''s \\ here', ['a', 'b c'], 1.5),
			(2, 0, 0, 0, '2026-01-01 00:00:00', '2026-01-01 00:00:00.000', '2026-01-01', NULL, [], 0)`,
		`CREATE VIEW jd_ch_recent AS SELECT id, note FROM jd_ch_types WHERE id > 1`,
	)
	read := func() string {
		rows, err := db.QueryContext(ctx, `SELECT concat(toString(id), '|', toString(wide), '|', toString(signed), '|', toString(price), '|',
			toString(toUnixTimestamp(at)), '|', toString(toUnixTimestamp64Milli(precise)), '|', toString(day), '|',
			ifNull(note, 'NULL'), '|', toString(tags), '|', toString(ratio)) FROM jd_ch_types ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			out = append(out, line)
		}
		return strings.Join(out, "\n")
	}
	before := read()

	res, err := DumpWith(ctx, DriverClickHouse, dsn, t.TempDir(), DumpOptions{
		Database: info.Database, Tables: []string{"jd_ch_types", "jd_ch_recent"},
	})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	text := readDump(t, res.Path)
	if !strings.Contains(res.Summary, "1 views") {
		t.Errorf("summary = %q", res.Summary)
	}
	execAll(t, db, cleanup...)
	if _, err := RestoreWith(ctx, DriverClickHouse, dsn, res.Path, RestoreOptions{Database: info.Database}); err != nil {
		t.Fatalf("RestoreWith: %v\n%s", err, text)
	}
	if after := read(); after != before {
		t.Errorf("the rows changed on the way through the dump:\nbefore\n%s\nafter\n%s\n%s", before, after, text)
	}
	if got := queryString(t, db, `SELECT toString(count()) FROM jd_ch_recent`); got != "1" {
		t.Errorf("the view came back reading %s rows", got)
	}
}

func TestLiveMongoBuiltInDumpKeepsIndexesAndOptions(t *testing.T) {
	client, dbName, dsn := ownMongo(t)
	ctx := context.Background()
	db := client.Database(dbName)
	const coll, other, view = "jd_dump_rich", "jd_dump_other", "jd_dump_view"
	drop := func() {
		for _, name := range []string{view, coll, other} {
			_ = db.Collection(name).Drop(context.Background())
		}
	}
	drop()
	t.Cleanup(drop)

	if err := db.RunCommand(ctx, bson.D{
		{Key: "create", Value: coll},
		{Key: "validator", Value: bson.D{{Key: "email", Value: bson.D{{Key: "$type", Value: "string"}}}}},
	}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := db.RunCommand(ctx, bson.D{{Key: "createIndexes", Value: coll}, {Key: "indexes", Value: bson.A{
		bson.D{{Key: "key", Value: bson.D{{Key: "email", Value: 1}}}, {Key: "name", Value: "email_unique"}, {Key: "unique", Value: true}},
		bson.D{{Key: "key", Value: bson.D{{Key: "seen", Value: 1}}}, {Key: "name", Value: "seen_ttl"}, {Key: "expireAfterSeconds", Value: 86400 * 365}},
	}}}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(coll).InsertMany(ctx, []any{
		bson.D{{Key: "email", Value: "a@x.io"}, {Key: "n", Value: int64(1) << 60}},
		bson.D{{Key: "email", Value: "b@x.io"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(other).InsertOne(ctx, bson.D{{Key: "k", Value: "left out"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.RunCommand(ctx, bson.D{{Key: "create", Value: view}, {Key: "viewOn", Value: coll},
		{Key: "pipeline", Value: bson.A{bson.D{{Key: "$match", Value: bson.D{{Key: "email", Value: "a@x.io"}}}}}}}).Err(); err != nil {
		t.Fatal(err)
	}

	res, err := DumpWith(ctx, DriverMongo, dsn, t.TempDir(), DumpOptions{
		Database: dbName, Tables: []string{coll, view}, builtIn: true,
	})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	if res.Summary != "2 collections, 2 documents" {
		t.Errorf("summary = %q", res.Summary)
	}
	drop()
	if _, err := db.Collection(other).InsertOne(ctx, bson.D{{Key: "k", Value: "still here"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreWith(ctx, DriverMongo, dsn, res.Path, RestoreOptions{Database: dbName}); err != nil {
		t.Fatalf("RestoreWith: %v", err)
	}
	if n, _ := db.Collection(coll).CountDocuments(ctx, bson.D{}); n != 2 {
		t.Errorf("%d documents came back, want 2", n)
	}
	// The unique index refuses a duplicate again, and the validator a number.
	if _, err := db.Collection(coll).InsertOne(ctx, bson.D{{Key: "email", Value: "a@x.io"}}); err == nil {
		t.Error("the unique index did not come back: a duplicate was accepted")
	}
	if _, err := db.Collection(coll).InsertOne(ctx, bson.D{{Key: "email", Value: 5}}); err == nil {
		t.Error("the validator did not come back: a document it forbids was accepted")
	}
	specs, err := mongoIndexSpecs(ctx, db.Collection(coll))
	if err != nil || len(specs) != 2 || !strings.Contains(string(specs[1])+string(specs[0]), "expireAfterSeconds") {
		t.Errorf("indexes after the restore: %s (%v)", specs, err)
	}
	if n, _ := db.Collection(view).CountDocuments(ctx, bson.D{}); n != 1 {
		t.Errorf("the view reads %d documents, want 1", n)
	}
	// What the dump did not hold was not touched.
	if n, _ := db.Collection(other).CountDocuments(ctx, bson.D{}); n != 1 {
		t.Errorf("a collection outside the dump holds %d documents", n)
	}
	if _, err := DumpWith(ctx, DriverMongo, dsn, t.TempDir(), DumpOptions{Database: dbName, Tables: []string{"nothere"}, builtIn: true}); err == nil {
		t.Error("a dump of a collection that does not exist succeeded")
	}
}

// TestLiveMongoNativeTools drives mongodump and mongorestore, where they are
// installed, including a restore under another database's name.
func TestLiveMongoNativeTools(t *testing.T) {
	if !toolAvailable("mongodump") || !toolAvailable("mongorestore") {
		t.Skip("mongodump and mongorestore are not installed here")
	}
	client, dbName, dsn := ownMongo(t)
	ctx := context.Background()
	db := client.Database(dbName)
	const keep, skip = "jd_native_keep", "jd_native_skip"
	copyName := dbName + "_r1"
	cleanup := func() {
		_ = db.Collection(keep).Drop(context.Background())
		_ = db.Collection(skip).Drop(context.Background())
		_ = client.Database(copyName).Drop(context.Background())
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := db.Collection(keep).InsertMany(ctx, []any{bson.D{{Key: "n", Value: 1}}, bson.D{{Key: "n", Value: 2}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(skip).InsertOne(ctx, bson.D{{Key: "n", Value: 3}}); err != nil {
		t.Fatal(err)
	}

	var lines []string
	res, err := DumpWith(ctx, DriverMongo, dsn, t.TempDir(), DumpOptions{
		Database: dbName, ExcludeTables: []string{skip}, Progress: func(l string) { lines = append(lines, l) },
	})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	if res.Tool != "mongodump" || res.ToolVersion == "" || len(lines) == 0 {
		t.Fatalf("tool = %q version %q, %d progress lines (%s)\n%s", res.Tool, res.ToolVersion, len(lines), res.Summary, strings.Join(lines, "\n"))
	}
	for _, line := range lines {
		if strings.Contains(line, "mongodb://") {
			t.Errorf("a progress line carries the connection string: %q", line)
		}
	}

	exists, err := DatabaseExists(ctx, DriverMongo, dsn, copyName)
	if err != nil || exists {
		t.Fatalf("DatabaseExists before the restore = %v, %v", exists, err)
	}
	if _, err := RestoreWith(ctx, DriverMongo, dsn, res.Path, RestoreOptions{Database: copyName, SourceDatabase: dbName}); err != nil {
		t.Fatalf("RestoreWith: %v", err)
	}
	if n, _ := client.Database(copyName).Collection(keep).CountDocuments(ctx, bson.D{}); n != 2 {
		t.Errorf("the copy holds %d documents, want 2", n)
	}
	if n, _ := client.Database(copyName).Collection(skip).CountDocuments(ctx, bson.D{}); n != 0 {
		t.Errorf("the excluded collection was dumped: %d documents", n)
	}
	if exists, _ := DatabaseExists(ctx, DriverMongo, dsn, copyName); !exists {
		t.Error("DatabaseExists does not see the database just restored")
	}

	// And back over the database it came from, uncompressed this time.
	res, err = DumpWith(ctx, DriverMongo, dsn, t.TempDir(), DumpOptions{Database: dbName, Tables: []string{keep}, Compression: CompressionNone})
	if err != nil {
		t.Fatalf("DumpWith(one collection): %v", err)
	}
	if _, err := db.Collection(keep).DeleteMany(ctx, bson.D{}); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreWith(ctx, DriverMongo, dsn, res.Path, RestoreOptions{Database: dbName, SourceDatabase: dbName}); err != nil {
		t.Fatalf("RestoreWith(same database): %v", err)
	}
	if n, _ := db.Collection(keep).CountDocuments(ctx, bson.D{}); n != 2 {
		t.Errorf("%d documents after restoring over the source, want 2", n)
	}
}

// TestLiveRedisDumpCoversEveryNumberedDatabase needs a Redis server of its
// own: it writes to several numbered databases and flushes them, which is not
// something to do to a server other tests share.
func TestLiveRedisDumpCoversEveryNumberedDatabase(t *testing.T) {
	dsn := os.Getenv("JD_TEST_REDIS_OWN_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_REDIS_OWN_DSN to a Redis server this test may flush")
	}
	ctx := context.Background()
	client, err := RedisClient(ctx, dsn, 0)
	if err != nil {
		t.Skipf("redis unreachable: %v", err)
	}
	defer client.Close()
	conn := client.Conn()
	defer conn.Close()
	seed := func() {
		conn.FlushAll(ctx)
		for db, keys := range map[int][]string{0: {"zero:a", "zero:b"}, 2: {"two:a"}, 5: {"five:a", "five:b", "five:c"}} {
			conn.Select(ctx, db)
			for _, k := range keys {
				if err := conn.Set(ctx, k, k+"-value", time.Hour).Err(); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	size := func(db int) int64 {
		conn.Select(ctx, db)
		return conn.DBSize(ctx).Val()
	}
	seed()
	t.Cleanup(func() { conn.FlushAll(context.Background()) })

	var lines []string
	res, err := DumpWith(ctx, DriverRedis, dsn, t.TempDir(), DumpOptions{Progress: func(l string) { lines = append(lines, l) }})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	if res.Database != "0,2,5" || res.Summary != "6 keys in 3 databases" || len(lines) != 3 {
		t.Fatalf("result = %+v, progress %q", res, lines)
	}
	conn.FlushAll(ctx)
	out, err := RestoreWith(ctx, DriverRedis, dsn, res.Path, RestoreOptions{})
	if err != nil {
		t.Fatalf("RestoreWith: %v", err)
	}
	if size(0) != 2 || size(2) != 1 || size(5) != 3 || !strings.Contains(out, "3 databases") {
		t.Errorf("after the restore: db0=%d db2=%d db5=%d (%s)", size(0), size(2), size(5), out)
	}
	conn.Select(ctx, 5)
	if got := conn.Get(ctx, "five:b").Val(); got != "five:b-value" {
		t.Errorf("a key in database 5 came back as %q", got)
	}
	if ttl := conn.TTL(ctx, "five:b").Val(); ttl <= 0 {
		t.Errorf("the key came back without its expiry: %v", ttl)
	}
	// An archive of several databases goes back where it came from, or not at
	// all.
	if _, err := RestoreWith(ctx, DriverRedis, dsn, res.Path, RestoreOptions{Database: "7"}); err == nil {
		t.Error("an archive of three databases was restored into one")
	}

	// The chosen ones, and one of them moved.
	res, err = DumpWith(ctx, DriverRedis, dsn, t.TempDir(), DumpOptions{RedisDatabases: []int{2}})
	if err != nil {
		t.Fatalf("DumpWith(db2): %v", err)
	}
	if res.Database != "2" || res.Summary != "1 keys" || !strings.Contains(res.File, "redis-db2-") {
		t.Errorf("result = %+v", res)
	}
	if exists, err := DatabaseExists(ctx, DriverRedis, dsn, "7"); err != nil || exists {
		t.Errorf("database 7 is reported as holding keys: %v, %v", exists, err)
	}
	if _, err := RestoreWith(ctx, DriverRedis, dsn, res.Path, RestoreOptions{Database: "7"}); err != nil {
		t.Fatalf("RestoreWith(into 7): %v", err)
	}
	if size(7) != 1 || size(2) != 1 {
		t.Errorf("db7=%d db2=%d after restoring database 2 into 7", size(7), size(2))
	}
	if exists, _ := DatabaseExists(ctx, DriverRedis, dsn, "7"); !exists {
		t.Error("database 7 holds a key and is reported as empty")
	}

	// A server with nothing in it still gets a dump, of the connection's own
	// database, rather than a refusal.
	conn.FlushAll(ctx)
	res, err = DumpWith(ctx, DriverRedis, dsn, t.TempDir(), DumpOptions{})
	if err != nil || res.Database != "0" || res.Summary != "0 keys" {
		t.Errorf("dump of an empty server: %+v, %v", res, err)
	}
}
