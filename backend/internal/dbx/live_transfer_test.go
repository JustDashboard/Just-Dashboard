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

	// A view is there to be asked for by name beside a table. It used to be
	// reported as a table that does not exist: views are kept under a key of
	// their own, and the name asked for was compared with the key.
	named, err := DumpWith(ctx, DriverPostgres, dsn, t.TempDir(), DumpOptions{
		builtIn: true, Tables: []string{"jd_rich.people", "jd_rich.calm_people"},
	})
	if err != nil {
		t.Fatalf("a dump that names a view: %v", err)
	}
	if text := readDump(t, named.Path); !strings.Contains(text, `CREATE VIEW "jd_rich"."calm_people"`) || strings.Contains(text, "calm_count") {
		t.Errorf("a dump of a table and a view by name:\n%s", text)
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
// an answer to that. It is replayed over the dashboard's own connection: no
// psql has to be installed for it, and none is run.
func TestLivePostgresRestoresAPlainSQLDump(t *testing.T) {
	const env, fallback = "JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable"
	liveSQL(t, DriverPostgres, env, fallback)
	dsn := liveDSN(t, env, fallback)
	ctx := context.Background()
	path := t.TempDir() + "/plain.sql"
	script := "--\n-- PostgreSQL database dump\n--\n\n\\restrict k3y\n\n" +
		"SET standard_conforming_strings = on;\n" +
		"CREATE TABLE public.jd_plain (id integer, name text);\n" +
		"CREATE FUNCTION public.jd_plain_two() RETURNS integer\n    LANGUAGE sql\n    BEGIN ATOMIC\n SELECT 1;\n SELECT 2;\nEND;\n" +
		"COPY public.jd_plain (id, name) FROM stdin;\n1\tone\n2\ttwo; still two\n3\t\\\\! not a command\n\\.\n\n" +
		"\\unrestrict k3y\n"
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dumpFormatOf(path); got != dumpFormatForeignSQL {
		t.Fatalf("dumpFormatOf = %v, want SQL somebody else wrote", got)
	}
	target := scratchDatabase(t, DriverPostgres, dsn, "r3")
	out, err := RestoreWith(ctx, DriverPostgres, dsn, path, RestoreOptions{Database: target})
	if err != nil {
		t.Fatalf("RestoreWith: %v", err)
	}
	if !strings.Contains(out, "3 rows loaded") {
		t.Errorf("the restore reported %q, want the rows it loaded", out)
	}
	restored, err := OpenDatabase(ctx, DriverPostgres, dsn, target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	for query, want := range map[string]string{
		`SELECT name FROM public.jd_plain WHERE id = 2`: "two; still two",
		`SELECT name FROM public.jd_plain WHERE id = 3`: `\! not a command`,
		`SELECT public.jd_plain_two()::text`:            "2",
	} {
		if got := queryString(t, restored, query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}

	// refused restores a script that begins by creating a table and must not
	// be left with it.
	refused := func(name, script, want string) {
		t.Helper()
		path := t.TempDir() + "/" + name
		if err := os.WriteFile(path, []byte("CREATE TABLE public.jd_half (id integer);\n"+script), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := RestoreWith(ctx, DriverPostgres, dsn, path, RestoreOptions{Database: target})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want it refused: %s", name, err, want)
		}
		if got := queryString(t, restored, `SELECT count(*)::text FROM pg_class WHERE relname = 'jd_half'`); got != "0" {
			t.Errorf("%s: a refused restore left its first table behind", name)
		}
	}
	// A script that reconnects to another database would carry on there,
	// wherever on a line it says so.
	refused("create.sql", "CREATE DATABASE other;\n\\connect other\nCREATE TABLE public.jd_elsewhere (id integer);\n", "connects to another database")
	refused("midline.sql", "SELECT 1 \\connect other\n", "connects to another database")
	// What psql would have run on this machine is not run by anything: not
	// when it is in plain sight, and not when the script changes how a string
	// is read so that a reader takes the command for part of one.
	marker := t.TempDir() + "/ran"
	refused("shell.sql", "\\! touch "+marker+"\n", "only psql runs")
	refused("hidden.sql", "SET standard_conforming_strings = off;\nSELECT 'a\\' || ' \\! touch "+marker+" ';\n", "nothing was changed")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a line of the script was run as a command on this machine")
	}
	// A script that fails changes nothing: it is replayed as one transaction.
	refused("bad.sql", "SELECT no_such_function();\n", "statement 2 (line 2) failed, and nothing was changed")
	// Rows the table will not take stop it too, and by the line of their COPY.
	refused("rows.sql", "COPY public.jd_plain (id, name) FROM stdin;\nnot a number\tx\n\\.\n", "statement 2 (line 2) failed, and nothing was changed")

	// A script that commits for itself has kept that much, and is not told
	// otherwise.
	own := t.TempDir() + "/commits.sql"
	if err := os.WriteFile(own, []byte("CREATE TABLE public.jd_kept (id integer);\nCOMMIT;\nSELECT no_such_function();\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreWith(ctx, DriverPostgres, dsn, own, RestoreOptions{Database: target}); err == nil || strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("a script that committed and then failed: %v", err)
	}
	if got := queryString(t, restored, `SELECT count(*)::text FROM pg_class WHERE relname = 'jd_kept'`); got != "1" {
		t.Error("what the script committed is gone")
	}
}

// TestLivePostgresRestoresWhatPgDumpWritesAsText takes real plain-format
// dumps, with whatever this pg_dump puts in one, and replays them.
func TestLivePostgresRestoresWhatPgDumpWritesAsText(t *testing.T) {
	const env, fallback = "JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable"
	db := liveSQL(t, DriverPostgres, env, fallback)
	dsn := liveDSN(t, env, fallback)
	ctx := context.Background()
	tool := postgresTool("pg_dump", postgresServerMajor(ctx, dsn))
	if !toolAvailable(tool) {
		t.Skip("pg_dump is not installed here")
	}
	info, err := ParseDSN(DriverPostgres, dsn)
	if err != nil {
		t.Fatal(err)
	}
	seedPostgresRich(t, db)
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS ` + pgRichSchema + ` CASCADE`) })
	execAll(t, db,
		`CREATE FUNCTION jd_rich.shout(v text) RETURNS text LANGUAGE plpgsql AS $fn$
			BEGIN
				RETURN upper(v) || '; \!';
			END;
		$fn$`,
		`CREATE FUNCTION jd_rich.three() RETURNS integer LANGUAGE sql BEGIN ATOMIC SELECT 1; SELECT 3; END`,
		"INSERT INTO jd_rich.notes (person_id, body) VALUES (2, E'a\\\\b\\ttab \\\\. and a ''quote''')",
	)

	for _, c := range []struct {
		name string
		args []string
		gzip bool
	}{
		{"copy", nil, false},
		{"inserts", []string{"--inserts", "--rows-per-insert=2"}, true},
	} {
		path := t.TempDir() + "/" + c.name + ".sql"
		args := append(pgConnArgs(info), "--format=plain", "--no-owner", "--schema="+pgRichSchema, "--file="+path)
		args = append(append(args, c.args...), "--dbname="+info.Database)
		if out, err := (toolRun{name: tool, args: args, env: []string{"PGPASSWORD=" + info.Password}}).run(ctx); err != nil {
			t.Fatalf("pg_dump: %v\n%s", err, out)
		}
		if c.gzip {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var packed bytes.Buffer
			gz := gzip.NewWriter(&packed)
			gz.Write(raw)
			gz.Close()
			path += ".gz"
			if err := os.WriteFile(path, packed.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		target := scratchDatabase(t, DriverPostgres, dsn, "r4"+c.name)
		if _, err := RestoreWith(ctx, DriverPostgres, dsn, path, RestoreOptions{Database: target}); err != nil {
			t.Fatalf("%s: RestoreWith: %v", c.name, err)
		}
		restored, err := OpenDatabase(ctx, DriverPostgres, dsn, target)
		if err != nil {
			t.Fatal(err)
		}
		for query, want := range map[string]string{
			`SELECT count(*)::text FROM jd_rich.people`:                                 "2",
			`SELECT count(*)::text FROM jd_rich.notes`:                                  "4",
			`SELECT count(*)::text FROM jd_rich.events_2025`:                            "1",
			`SELECT mood::text FROM jd_rich.people WHERE id = 2`:                        "it's complicated",
			`SELECT encode(avatar, 'hex') FROM jd_rich.people WHERE id = 1`:             "00ff41",
			`SELECT body FROM jd_rich.notes WHERE id = 2`:                               "two\nlines",
			`SELECT body FROM jd_rich.notes WHERE id = 4`:                               "a\\b\ttab \\. and a 'quote'",
			`SELECT n::text FROM jd_rich.calm_count`:                                    "1",
			`SELECT notes::text FROM jd_rich.note_totals WHERE person_id = 1`:           "2",
			`SELECT jd_rich.shout('a')`:                                                 `A; \!`,
			`SELECT jd_rich.three()::text`:                                              "3",
			`SELECT nextval('jd_rich.ticket_seq')::text`:                                "115",
			`SELECT relkind::text FROM pg_class WHERE oid = 'jd_rich.events'::regclass`: "p",
		} {
			if got := queryString(t, restored, query); got != want {
				t.Errorf("%s: %s = %q, want %q", c.name, query, got, want)
			}
		}
		restored.Close()
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

// TestLiveBuiltInMySQLDumpLeavesTheDatabaseNextDoorAlone is the same
// regression from the side of the database that was being damaged. The login
// is the administrator's, which sees every database on the server — the case
// in which the old dump wrote all of them into one file — and a database next
// to the one being dumped holds a table of the same name. Nothing of it may be
// in the file, and nothing of it may have changed once the file is loaded.
//
// It runs against each server an administrator's connection is given for.
func TestLiveBuiltInMySQLDumpLeavesTheDatabaseNextDoorAlone(t *testing.T) {
	for _, env := range [][2]string{
		{"JD_TEST_MYSQL_ADMIN_DSN", "JD_TEST_MYSQL_DSN"},
		{"JD_TEST_MYSQL8_ADMIN_DSN", "JD_TEST_MYSQL8_DSN"},
	} {
		t.Run(env[1], func(t *testing.T) {
			admin := os.Getenv(env[0])
			own, err := ParseDSN(DriverMySQL, os.Getenv(env[1]))
			if admin == "" || err != nil || own.Database == "" {
				t.Skipf("set %s and %s to run this", env[0], env[1])
			}
			// The administrator's connection, pointed at this run's own database.
			dsn := dsnForDatabase(DriverMySQL, admin, own.Database)
			const pointed = "JD_TEST_MYSQL_ROOT_OWN_DSN"
			t.Setenv(pointed, dsn)
			db := liveSQL(t, DriverMySQL, pointed, dsn)
			ctx := context.Background()
			seedMySQLRich(t, db)

			next := scratchDatabase(t, DriverMySQL, dsn, "nextdoor")
			nextDB, err := OpenDatabase(ctx, DriverMySQL, dsn, next)
			if err != nil {
				t.Fatal(err)
			}
			defer nextDB.Close()
			execAll(t, nextDB,
				`CREATE TABLE jd_dx_parent (id INT PRIMARY KEY, name VARCHAR(50))`,
				`INSERT INTO jd_dx_parent VALUES (7, 'not yours')`,
				`CREATE TABLE jd_dx_nextdoor_only (id INT PRIMARY KEY)`,
			)
			var visible int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT TABLE_SCHEMA) FROM information_schema.TABLES
				WHERE TABLE_SCHEMA NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys') AND TABLE_SCHEMA <> DATABASE()`).Scan(&visible); err != nil {
				t.Fatal(err)
			}
			if visible == 0 {
				t.Fatal("this login sees no database but its own, so the test proves nothing")
			}

			res, err := DumpWith(ctx, DriverMySQL, dsn, t.TempDir(), DumpOptions{builtIn: true})
			if err != nil {
				t.Fatalf("DumpWith: %v", err)
			}
			text := readDump(t, res.Path)
			for _, foreign := range []string{next, "jd_dx_nextdoor_only", "not yours"} {
				if strings.Contains(text, foreign) {
					t.Errorf("the dump of %s holds %q", own.Database, foreign)
				}
			}
			// No statement in it names a database at all, so each can only
			// land in the one the session is in.
			rows, err := db.QueryContext(ctx, `SELECT SCHEMA_NAME FROM information_schema.SCHEMATA`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var schema string
				if err := rows.Scan(&schema); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(text, "`"+schema+"`.") {
					t.Errorf("the dump names database %s in a statement", schema)
				}
			}
			rows.Close()
			t.Logf("%d other databases are visible to this login and absent from the dump of %s", visible, own.Database)

			execAll(t, db, `DELETE FROM jd_dx_child`, `DELETE FROM jd_dx_parent`)
			if _, err := RestoreWith(ctx, DriverMySQL, dsn, res.Path, RestoreOptions{}); err != nil {
				t.Fatalf("RestoreWith: %v", err)
			}
			checkMySQLRich(t, db)
			for query, want := range map[string]string{
				`SELECT name FROM jd_dx_parent WHERE id = 7`:                                     "not yours",
				`SELECT COUNT(*) FROM jd_dx_parent`:                                              "1",
				`SELECT COUNT(*) FROM jd_dx_nextdoor_only`:                                       "0",
				`SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()`: "2",
			} {
				if got := queryString(t, nextDB, query); got != want {
					t.Errorf("in the database next door, %s = %q, want %q", query, got, want)
				}
			}
		})
	}
}

// TestLiveMySQLNativeDump drives mysqldump, where it is installed: a narrowed,
// compressed dump, and what it wrote replayed without a client.
func TestLiveMySQLNativeDump(t *testing.T) {
	const env = "JD_TEST_MYSQL8_DSN"
	if os.Getenv(env) == "" {
		t.Skipf("set %s to a MySQL server this test may write to", env)
	}
	if firstAvailableTool("mysqldump", "mariadb-dump") == "" {
		t.Skip("mysqldump is not installed here")
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

// TestLiveMySQLReplaysAScript restores a script written the way mysqldump
// writes one — conditional comments, a trigger between DELIMITER lines, rows
// with everything in them that is not the end of a statement — with no client
// installed, and refuses the ones that would leave the database or run
// something on this machine.
func TestLiveMySQLReplaysAScript(t *testing.T) {
	for _, env := range []string{"JD_TEST_MYSQL_DSN", "JD_TEST_MYSQL8_DSN"} {
		t.Run(env, func(t *testing.T) {
			const fallback = "jdtest:jdtest@tcp(127.0.0.1:3306)/jdtest"
			if env != "JD_TEST_MYSQL_DSN" && os.Getenv(env) == "" {
				t.Skipf("set %s to a second MySQL server this test may write to", env)
			}
			db := liveSQL(t, DriverMySQL, env, fallback)
			dsn := liveDSN(t, env, fallback)
			ctx := context.Background()
			info, err := ParseDSN(DriverMySQL, dsn)
			if err != nil {
				t.Fatal(err)
			}
			drop := func() {
				db.Exec("DROP PROCEDURE IF EXISTS jd_rp_count")
				db.Exec("DROP TABLE IF EXISTS jd_rp_item")
				db.Exec("DROP TABLE IF EXISTS jd_rp_probe")
			}
			drop()
			t.Cleanup(drop)

			// A server that writes a binary log lets only a privileged account
			// define a trigger. Where this one may not, the script goes
			// without.
			execAll(t, db, "CREATE TABLE jd_rp_probe (id int)")
			_, probe := db.Exec("CREATE TRIGGER jd_rp_probe_bi BEFORE INSERT ON jd_rp_probe FOR EACH ROW SET NEW.id = NEW.id")
			routines := probe == nil
			if !routines {
				t.Logf("this account cannot define a trigger here (%v); the script is replayed without one", probe)
			}

			script := "-- MySQL dump 10.13  Distrib 8.4.2, for Linux (x86_64)\n--\n" +
				"-- Host: localhost    Database: shop\n-- ------------------------------------------------------\n\n" +
				"/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;\n" +
				"/*!50503 SET NAMES utf8mb4 */;\n" +
				"/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;\n" +
				"/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;\n\n" +
				"USE `" + info.Database + "`;\n" +
				"DROP TABLE IF EXISTS `jd_rp_item`;\n" +
				"/*!40101 SET @saved_cs_client     = @@character_set_client */;\n" +
				"CREATE TABLE `jd_rp_item` (\n  `id` int NOT NULL AUTO_INCREMENT,\n  `name` varchar(50) NOT NULL,\n" +
				"  `note` text,\n  `raw` blob,\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;\n" +
				"/*!40101 SET character_set_client = @saved_cs_client */;\n\n" +
				"LOCK TABLES `jd_rp_item` WRITE;\n" +
				"/*!40000 ALTER TABLE `jd_rp_item` DISABLE KEYS */;\n" +
				"INSERT INTO `jd_rp_item` VALUES (1,'O\\'Brien; \\\\','line one\\nUSE `other`;\\n# not a comment',_binary '\\0\xffA'),(2,'two',NULL,NULL);\n" +
				"/*!40000 ALTER TABLE `jd_rp_item` ENABLE KEYS */;\n" +
				"UNLOCK TABLES;\n"
			if routines {
				script += "/*!50003 SET @saved_sql_mode       = @@sql_mode */ ;\n" +
					"/*!50003 SET sql_mode              = 'STRICT_TRANS_TABLES' */ ;\n" +
					"DELIMITER ;;\n" +
					"/*!50003 CREATE*/ /*!50003 TRIGGER `jd_rp_item_bi` BEFORE INSERT ON `jd_rp_item` FOR EACH ROW BEGIN\n" +
					"  IF NEW.note IS NULL THEN SET NEW.note = 'none; given'; END IF;\nEND */;;\n" +
					"DELIMITER ;\n" +
					"/*!50003 SET sql_mode              = @saved_sql_mode */ ;\n" +
					"DELIMITER ;;\n" +
					"CREATE PROCEDURE `jd_rp_count`()\nBEGIN\n  SELECT COUNT(*) FROM jd_rp_item;\nEND ;;\n" +
					"DELIMITER ;\n"
			}
			// A conditional comment for a version no server is comes to an
			// empty statement, which is not a failure.
			script += "/*!99999 SET what_no_server_has = 1 */;\n" +
				"/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;\n" +
				"/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;\n\n" +
				"-- Dump completed on 2026-01-02  3:04:05\n"
			dir := t.TempDir()
			write := func(name, content string) string {
				path := dir + "/" + name
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			}
			path := write("shop.sql", script)
			if got := dumpFormatOf(path); got != dumpFormatForeignSQL {
				t.Fatalf("dumpFormatOf = %v, want SQL somebody else wrote", got)
			}
			if _, err := RestoreWith(ctx, DriverMySQL, dsn, path, RestoreOptions{}); err != nil {
				t.Fatalf("RestoreWith: %v", err)
			}
			for query, want := range map[string]string{
				`SELECT COUNT(*) FROM jd_rp_item`:                            "2",
				`SELECT name FROM jd_rp_item WHERE id = 1`:                   `O'Brien; \`,
				`SELECT note FROM jd_rp_item WHERE id = 1`:                   "line one\nUSE `other`;\n# not a comment",
				`SELECT HEX(raw) FROM jd_rp_item WHERE id = 1`:               "00FF41",
				`SELECT COALESCE(note, 'null') FROM jd_rp_item WHERE id = 2`: "null",
			} {
				if got := queryString(t, db, query); got != want {
					t.Errorf("%s = %q, want %q", query, got, want)
				}
			}
			if routines {
				execAll(t, db, "INSERT INTO jd_rp_item (name) VALUES ('three')")
				if got := queryString(t, db, `SELECT note FROM jd_rp_item WHERE name = 'three'`); got != "none; given" {
					t.Errorf("the trigger did not come back: note = %q", got)
				}
				if got := queryString(t, db, `SELECT COUNT(*) FROM information_schema.ROUTINES
					WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_NAME = 'jd_rp_count'`); got != "1" {
					t.Error("the procedure did not come back")
				}
			}

			// Refused before anything is run: each of these begins by dropping
			// the table, and the table has to be there afterwards.
			marker := dir + "/ran"
			for name, c := range map[string]struct{ script, want string }{
				"a USE in the middle of a line":  {"SELECT 1;USE `mysql`;\nCREATE TABLE jd_rp_elsewhere (id int);\n", "switches to another database"},
				"a USE inside a comment it runs": {"/*!50000 USE mysql */;\n", "switches to another database"},
				"a command of the client's":      {"\\! touch " + marker + "\n", "only the client runs"},
				"the same, spelt out":            {"SELECT 1;\nsystem touch " + marker + "\n", "only the client runs"},
				"a file of this machine's":       {"source /etc/hostname\n", "only the client runs"},
				"a USE the client would take":    {"use mysql\nCREATE TABLE jd_rp_elsewhere (id int);\n", "switches to another database"},
			} {
				_, err := RestoreWith(ctx, DriverMySQL, dsn, write("refused.sql", "DROP TABLE IF EXISTS `jd_rp_item`;\n"+c.script), RestoreOptions{})
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Errorf("%s: %v, want it refused: %s", name, err, c.want)
				}
				if got := queryString(t, db, `SELECT COUNT(*) FROM information_schema.TABLES
					WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'jd_rp_item'`); got != "1" {
					t.Fatalf("%s: the script was refused after its first statement had run", name)
				}
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("a line of the script was run as a command on this machine")
			}
			// A file dressed as one of the dashboard's own is held to the same.
			if _, err := RestoreWith(ctx, DriverMySQL, dsn, write("own.sql", dumpHeader+"\nUSE `mysql`;\nCREATE TABLE jd_rp_elsewhere (id int);\n"),
				RestoreOptions{}); err == nil || !strings.Contains(err.Error(), "switches to another database") {
				t.Errorf("a dashboard-format file with a USE: %v", err)
			}
		})
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

	// A file that names one key twice: the server will not overwrite a row
	// twice in one statement, so those rows go in one at a time and the later
	// one wins.
	report, err = Import(ctx, db, DriverPostgres, strings.NewReader("id,email,qty\n40,twice@x.io,1\n41,once@x.io,1\n40,twice@x.io,2\n"),
		ImportSpec{Table: "jd_imp", Mode: ImportModeUpsert})
	if err != nil || report.Inserted != 2 || report.Updated != 1 {
		t.Fatalf("upsert with a repeated key: %+v, %v", report, err)
	}
	if got := queryString(t, db, `SELECT qty::text FROM jd_imp WHERE id = 40`); got != "2" {
		t.Errorf("the repeated key holds qty %s, want the later row's 2", got)
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
	// The archive says which database it is of, so nobody has to remember.
	if got := mongoArchiveDatabases(res.Path); len(got) != 1 || got[0] != dbName {
		t.Fatalf("the archive is read as holding %v, want [%s]", got, dbName)
	}
	// Restored under another name with nothing said about where it came
	// from: it goes where it was told, and the database it came from is not
	// written to.
	if _, err := db.Collection(keep).DeleteMany(ctx, bson.D{}); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreWith(ctx, DriverMongo, dsn, res.Path, RestoreOptions{Database: copyName}); err != nil {
		t.Fatalf("RestoreWith: %v", err)
	}
	if n, _ := db.Collection(keep).CountDocuments(ctx, bson.D{}); n != 0 {
		t.Fatalf("a restore into %s wrote %d documents into %s", copyName, n, dbName)
	}
	if _, err := db.Collection(keep).InsertMany(ctx, []any{bson.D{{Key: "n", Value: 1}}, bson.D{{Key: "n", Value: 2}}}); err != nil {
		t.Fatal(err)
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
	if got := mongoArchiveDatabases(res.Path); len(got) != 1 || got[0] != dbName {
		t.Fatalf("the uncompressed archive is read as holding %v, want [%s]", got, dbName)
	}
	if _, err := RestoreWith(ctx, DriverMongo, dsn, res.Path, RestoreOptions{Database: dbName}); err != nil {
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
	// One key put back as nothing has used it for an hour, to see what being
	// dumped does to that.
	const hour = 3600
	conn.Select(ctx, 5)
	payload := conn.Dump(ctx, "five:a").Val()
	conn.Del(ctx, "five:a")
	aged := conn.Do(ctx, "RESTORE", "five:a", time.Hour.Milliseconds(), payload, "IDLETIME", hour).Err() == nil

	var lines []string
	res, err := DumpWith(ctx, DriverRedis, dsn, t.TempDir(), DumpOptions{Progress: func(l string) { lines = append(lines, l) }})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	if res.Database != "0,2,5" || res.Summary != "6 keys in 3 databases" || len(lines) != 3 {
		t.Fatalf("result = %+v, progress %q", res, lines)
	}
	// A dump reads every key and is not the application using any of them:
	// counted as use, the nightly backup made every key on the server its
	// most recently used.
	if profile, err := RedisProbe(ctx, client); aged && err == nil && profile.Features.NoTouch && profile.Features.ObjectIdleTime {
		conn.Select(ctx, 5)
		if idle, err := conn.Do(ctx, "OBJECT", "IDLETIME", "five:a").Int64(); err != nil || idle < hour {
			t.Errorf("the dump left the key idle for %d seconds (%v), want the hour it had", idle, err)
		}
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

// TestLiveMySQLRestoreIntoANewDatabase is why the built-in MySQL dump names no
// database: the same file has to load into the one it came from and into one
// made a moment ago. It needs an account that may create a database.
func TestLiveMySQLRestoreIntoANewDatabase(t *testing.T) {
	admin := os.Getenv("JD_TEST_MYSQL_ADMIN_DSN")
	own, err := ParseDSN(DriverMySQL, os.Getenv("JD_TEST_MYSQL_DSN"))
	if admin == "" || err != nil || own.Database == "" {
		t.Skip("set JD_TEST_MYSQL_ADMIN_DSN and JD_TEST_MYSQL_DSN to run this")
	}
	// The administrator's connection, pointed at this run's own database.
	dsn := dsnForDatabase(DriverMySQL, admin, own.Database)
	t.Setenv("JD_TEST_MYSQL_ROOT_OWN_DSN", dsn)
	db := liveSQL(t, DriverMySQL, "JD_TEST_MYSQL_ROOT_OWN_DSN", dsn)
	ctx := context.Background()
	seedMySQLRich(t, db)

	res, err := DumpWith(ctx, DriverMySQL, dsn, t.TempDir(), DumpOptions{
		builtIn: true, Tables: []string{"jd_dx_parent", "jd_dx_child", "jd_dx_view", "jd_dx_summary"},
	})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	target := scratchDatabase(t, DriverMySQL, dsn, "r1")
	if exists, err := DatabaseExists(ctx, DriverMySQL, dsn, target); err != nil || !exists {
		t.Fatalf("DatabaseExists(%s) = %v, %v", target, exists, err)
	}
	if _, err := RestoreWith(ctx, DriverMySQL, dsn, res.Path, RestoreOptions{Database: target}); err != nil {
		t.Fatalf("RestoreWith: %v\n%s", err, readDump(t, res.Path))
	}
	restored, err := OpenDatabase(ctx, DriverMySQL, dsn, target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	checkMySQLRich(t, restored)
	// The view in the new database reads the new database's tables.
	execAll(t, db, `DELETE FROM jd_dx_child`)
	if got := queryString(t, restored, `SELECT n FROM jd_dx_summary`); got != "2" {
		t.Errorf("the copy's view reads %s rows after the original was emptied, want its own 2", got)
	}

	// The engine's own tool writes a script that names no database either,
	// which is what a copy relies on: it is replayed into the one made for it.
	if firstAvailableTool("mysqldump", "mariadb-dump") == "" {
		return
	}
	seedMySQLRich(t, db)
	res, err = DumpWith(ctx, DriverMySQL, dsn, t.TempDir(), DumpOptions{
		Tables: []string{"jd_dx_parent", "jd_dx_child", "jd_dx_view", "jd_dx_summary"},
	})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	if res.Tool == BuiltInDumpTool {
		t.Logf("the installed tool refused this server (%s); its script is not what was replayed", res.Summary)
	}
	second := scratchDatabase(t, DriverMySQL, dsn, "r2")
	if _, err := RestoreWith(ctx, DriverMySQL, dsn, res.Path, RestoreOptions{Database: second}); err != nil {
		t.Fatalf("RestoreWith(%s): %v", res.Tool, err)
	}
	copied, err := OpenDatabase(ctx, DriverMySQL, dsn, second)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	checkMySQLRich(t, copied)
}

// TestLiveImportLeavesOutWhatTheServerComputes imports a file that carries a
// generated column — an export of the table does — into each engine that
// lists such a column among a table's own. The column is left out and the
// report says so; matched to it, every row would have been refused.
func TestLiveImportLeavesOutWhatTheServerComputes(t *testing.T) {
	for _, c := range []struct {
		driver        Driver
		env, fallback string
		create        string
	}{
		{DriverPostgres, "JD_TEST_POSTGRES_DSN", "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable",
			`CREATE TABLE jd_gen (id integer PRIMARY KEY, net numeric NOT NULL, gross numeric GENERATED ALWAYS AS (net * 2) STORED)`},
		{DriverMySQL, "JD_TEST_MYSQL_DSN", "jdtest:jdtest@tcp(127.0.0.1:3306)/jdtest",
			`CREATE TABLE jd_gen (id INT PRIMARY KEY, net DECIMAL(10,2) NOT NULL, gross DECIMAL(12,2) AS (net * 2) STORED)`},
		{DriverMySQL, "JD_TEST_MYSQL8_DSN", "",
			`CREATE TABLE jd_gen (id INT PRIMARY KEY, net DECIMAL(10,2) NOT NULL, gross DECIMAL(12,2) AS (net * 2) STORED)`},
		{DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN", "clickhouse://default@127.0.0.1:9000/default",
			`CREATE TABLE jd_gen (id Int32, net Float64, gross Float64 MATERIALIZED net * 2) ENGINE = MergeTree ORDER BY id`},
	} {
		t.Run(c.env, func(t *testing.T) {
			if c.fallback == "" && os.Getenv(c.env) == "" {
				t.Skipf("set %s to run this", c.env)
			}
			db := liveSQL(t, c.driver, c.env, c.fallback)
			ctx := context.Background()
			db.Exec(`DROP TABLE IF EXISTS jd_gen`)
			execAll(t, db, c.create)
			t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS jd_gen`) })

			report, err := Import(ctx, db, c.driver, strings.NewReader("id,net,gross\n1,10,999\n2,20,999\n"), ImportSpec{Table: "jd_gen"})
			if err != nil || report.Inserted != 2 {
				t.Fatalf("Import: %+v, %v", report, err)
			}
			if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "gross is filled in by the server") {
				t.Errorf("warnings = %q", report.Warnings)
			}
			var gross float64
			if err := db.QueryRowContext(ctx, `SELECT gross FROM jd_gen WHERE id = 2`).Scan(&gross); err != nil || gross != 40 {
				t.Errorf("gross = %v (%v), want what the table computes", gross, err)
			}
		})
	}
}

// "No value" has to be taken by a column of every type, on every engine: one
// that types what it is sent may refuse a NULL for arriving as the wrong type,
// as SQL Server does for a binary column bound a text. Each engine is asked
// for a row of nothing but NULLs under the types that could object, by the
// route that reads a file against the table and — where it binds a file's
// cells as the text they are, which ClickHouse's driver takes for no number —
// by the inline one.
func TestLiveImportLeavesEveryTypedColumnEmpty(t *testing.T) {
	for _, c := range []struct {
		name    string
		driver  Driver
		env     string
		columns string
		// suffix is what follows the column list in CREATE TABLE.
		suffix string
		inline bool
	}{
		{"postgres", DriverPostgres, "JD_TEST_POSTGRES_DSN",
			`id INT PRIMARY KEY, bin BYTEA, js JSONB, u UUID, n NUMERIC(10,2), ts TIMESTAMPTZ, flag BOOLEAN, arr INT[], bits BIT(8)`, "", true},
		{"mariadb", DriverMySQL, "JD_TEST_MYSQL_DSN",
			`id INT PRIMARY KEY, bin VARBINARY(16), bl BLOB, js JSON, n DECIMAL(10,2), ts DATETIME, flag TINYINT(1), bits BIT(8)`, "", true},
		{"mysql8", DriverMySQL, "JD_TEST_MYSQL8_DSN",
			`id INT PRIMARY KEY, bin VARBINARY(16), bl BLOB, js JSON, n DECIMAL(10,2), ts DATETIME, flag TINYINT(1), bits BIT(8)`, "", true},
		{"clickhouse", DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN",
			`id UInt64, i Nullable(Int32), u Nullable(UUID), n Nullable(Decimal(10,2)), ts Nullable(DateTime), d Nullable(Date),
			 f Nullable(Float64), s Nullable(String), fs Nullable(FixedString(4)), flag Nullable(Bool), ip Nullable(IPv4)`,
			" ENGINE = MergeTree ORDER BY id", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if os.Getenv(c.env) == "" {
				t.Skipf("set %s to run this", c.env)
			}
			db := liveSQL(t, c.driver, c.env, "")
			ctx := context.Background()
			execAll(t, db, `DROP TABLE IF EXISTS jdbf_import_null`, `CREATE TABLE jdbf_import_null (`+c.columns+`)`+c.suffix)
			t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS jdbf_import_null`) })
			d := mustDialectFor(c.driver)
			cols, err := d.Columns(ctx, db, catalogSchema(ctx, db, d, ""), "jdbf_import_null")
			if err != nil || len(cols) < 2 {
				t.Fatalf("columns: %v %v", cols, err)
			}
			file := func(id, null string) io.Reader {
				header, row := make([]string, len(cols)), make([]string, len(cols))
				for i, col := range cols {
					header[i], row[i] = col.Name, null
				}
				row[0] = id
				return strings.NewReader(strings.Join(header, ",") + "\n" + strings.Join(row, ",") + "\n")
			}
			rows := "1"
			report, err := Import(ctx, db, c.driver, file("1", ""), ImportSpec{Table: "jdbf_import_null"})
			if err != nil || report.Inserted != 1 {
				t.Fatalf("a row of no values, read against the table: %+v, %v", report, err)
			}
			if c.inline {
				rows = "2"
				res, err := ImportCSV(ctx, db, c.driver, file("2", `\N`),
					ImportOptions{Table: "jdbf_import_null", HasHeader: true, NullAs: `\N`, StopOnError: true})
				if err != nil || res.Inserted != 1 || res.Failed != 0 {
					t.Fatalf("a row of no values, bound as text: %+v, %v", res, err)
				}
			}
			for _, col := range cols[1:] {
				quoted, err := d.QuoteIdent(col.Name)
				if err != nil {
					t.Fatal(err)
				}
				if got := queryString(t, db, `SELECT `+d.CastText(`COUNT(*)`)+` FROM jdbf_import_null WHERE `+quoted+` IS NULL`); got != rows {
					t.Errorf("%s (%s): %s rows hold no value, want %s", col.Name, col.Type, got, rows)
				}
			}
		})
	}
}
