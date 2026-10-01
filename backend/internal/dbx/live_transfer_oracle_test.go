package dbx

import (
	"bytes"
	"context"
	"database/sql"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The built-in dump, the import and the export against a real Oracle.
//
// What a dump means by "the database" there is a schema, and a schema is a
// user. The user a fixture gives is shared by whoever is testing, and a dump's
// restore replaces every table it holds — so nothing here runs as that user.
// Each test is given a user of its own by the administrator's connection, and
// drops it.

const transferOracleAdminEnv = "JD_TEST_ORACLE_ADMIN_DSN"

// liveOwnOracle makes a user for the test, with what an application's account
// ordinarily may do and no more, and returns a pool as that user, its
// connection string and its schema's name.
func liveOwnOracle(t *testing.T, name string) (*sql.DB, string, string) {
	t.Helper()
	adminDSN := os.Getenv(transferOracleAdminEnv)
	if adminDSN == "" {
		t.Skipf("set %s to an Oracle account that may create a user for this test", transferOracleAdminEnv)
	}
	admin := liveSQL(t, DriverOracle, transferOracleAdminEnv, "")
	ctx := context.Background()
	schema := strings.ToUpper(name)
	const password = "jdtest"
	drop := func() {
		// A session still open as the user keeps it from being dropped.
		rows, err := admin.QueryContext(context.Background(), `SELECT sid, serial# FROM v$session WHERE username = :1`, schema)
		if err == nil {
			var kills []string
			for rows.Next() {
				var sid, serial string
				if rows.Scan(&sid, &serial) == nil {
					kills = append(kills, "ALTER SYSTEM KILL SESSION '"+sid+","+serial+"' IMMEDIATE")
				}
			}
			rows.Close()
			for _, kill := range kills {
				_, _ = admin.ExecContext(context.Background(), kill)
			}
		}
		_, _ = admin.ExecContext(context.Background(), `DROP USER `+schema+` CASCADE`)
	}
	drop()
	for _, statement := range []string{
		`CREATE USER ` + schema + ` IDENTIFIED BY ` + password + ` QUOTA UNLIMITED ON USERS`,
		`GRANT CREATE SESSION, CREATE TABLE, CREATE VIEW, CREATE SEQUENCE, CREATE MATERIALIZED VIEW TO ` + schema,
	} {
		if _, err := admin.ExecContext(ctx, statement); err != nil {
			t.Skipf("the administrator's account cannot make a user for this test: %v", err)
		}
	}
	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(schema, password)
	dsn := u.String()
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		drop()
	})
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connecting as %s: %v", schema, err)
	}
	return db, dsn, schema
}

// oracleLongText and oracleLongBytes are values too long for a literal: the
// text past what a statement takes in pieces, the bytes past what it takes at
// all.
func oracleLongText() string { return strings.Repeat("Zoë says “it's”; -- not a comment\n", 9000) }
func oracleLongBytes() []byte {
	out := make([]byte, 100_000)
	for i := range out {
		out[i] = byte(i * 7)
	}
	return out
}

// seedOracleRich builds what an Oracle schema ordinarily holds and a list of
// tables does not carry: a column the table always numbers itself, a virtual
// column, a default drawn from a sequence, named and unnamed constraints, an
// index that sorts downwards and one on an expression, comments, a foreign key
// that cascades, a table with a quoted lower-case name, and a view over a view.
func seedOracleRich(t *testing.T, db *sql.DB) {
	t.Helper()
	execAll(t, db,
		`CREATE SEQUENCE TICKET_SEQ START WITH 100 INCREMENT BY 5 NOCACHE`,
		`CREATE TABLE PEOPLE (
			ID NUMBER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			EMAIL VARCHAR2(255) NOT NULL,
			NICK NVARCHAR2(50),
			BIO CLOB,
			AVATAR BLOB,
			TOKEN RAW(16),
			SCORE NUMBER(12,2),
			BIG NUMBER(38),
			TINY NUMBER,
			RATIO BINARY_DOUBLE,
			BORN DATE,
			SEEN_AT TIMESTAMP(9),
			SEEN_TZ TIMESTAMP WITH TIME ZONE,
			SEEN_LTZ TIMESTAMP WITH LOCAL TIME ZONE,
			GAP INTERVAL DAY TO SECOND,
			ACTIVE NUMBER(1) DEFAULT 1 NOT NULL,
			TICKET NUMBER DEFAULT TICKET_SEQ.NEXTVAL,
			SHOUT VARCHAR2(300) GENERATED ALWAYS AS (UPPER(EMAIL)) VIRTUAL,
			CONSTRAINT PEOPLE_EMAIL_UK UNIQUE (EMAIL),
			CONSTRAINT PEOPLE_SCORE_CK CHECK (SCORE IS NULL OR SCORE >= 0)
		)`,
		`COMMENT ON TABLE PEOPLE IS 'Everyone; it''s a test'`,
		`COMMENT ON COLUMN PEOPLE.EMAIL IS 'How to reach them'`,
		`CREATE INDEX PEOPLE_BORN_IX ON PEOPLE (BORN DESC, NICK)`,
		`CREATE INDEX PEOPLE_LOWER_IX ON PEOPLE (LOWER(EMAIL))`,
		`CREATE TABLE "notes" (
			"id" NUMBER(10) PRIMARY KEY,
			"person_id" NUMBER NOT NULL CONSTRAINT "notes_person_fk" REFERENCES PEOPLE(ID) ON DELETE CASCADE,
			"body" VARCHAR2(4000)
		)`,
		`CREATE VIEW ACTIVE_PEOPLE AS SELECT ID, EMAIL FROM PEOPLE WHERE ACTIVE = 1`,
		`CREATE VIEW ACTIVE_COUNT AS SELECT COUNT(*) N FROM ACTIVE_PEOPLE`,
		`INSERT INTO PEOPLE (EMAIL, NICK, BIO, AVATAR, TOKEN, SCORE, BIG, TINY, RATIO, BORN, SEEN_AT, SEEN_TZ, SEEN_LTZ, GAP) VALUES (
			'ann@x.io', N'Zoë', 'short; -- bio', HEXTORAW('00FF41'), HEXTORAW('6F9619FF8B86D011B42D00C04FC964FF'),
			12.50, 12345678901234567890123456789012345678, -.000015, 1.5,
			TO_DATE('1990-02-03 04:05:06', 'YYYY-MM-DD HH24:MI:SS'),
			TO_TIMESTAMP('2026-03-04 05:06:07.123456789', 'YYYY-MM-DD HH24:MI:SS.FF'),
			TO_TIMESTAMP_TZ('2026-03-04 05:06:07.123456 +02:00', 'YYYY-MM-DD HH24:MI:SS.FF TZH:TZM'),
			TO_TIMESTAMP_TZ('2026-03-04 05:06:07.123456 +02:00', 'YYYY-MM-DD HH24:MI:SS.FF TZH:TZM'),
			INTERVAL '1 02:03:04.5' DAY TO SECOND)`,
		`INSERT INTO PEOPLE (EMAIL, RATIO) VALUES ('o''brien@x.io', BINARY_DOUBLE_NAN)`,
		`INSERT INTO PEOPLE (EMAIL) VALUES ('gone@x.io')`,
		// The last id handed out is 3 and the row that had it is gone.
		`DELETE FROM PEOPLE WHERE EMAIL = 'gone@x.io'`,
		`INSERT INTO "notes" VALUES (1, 1, 'semi; colon')`,
		`INSERT INTO "notes" VALUES (2, 1, 'two
lines')`,
		`INSERT INTO "notes" VALUES (3, 2, NULL)`,
	)
	// Values no literal holds go in as bound parameters; how they come back
	// out of a text file is what is being tested.
	if _, err := db.Exec(`UPDATE PEOPLE SET BIO = :1, AVATAR = :2 WHERE EMAIL = 'o''brien@x.io'`,
		oracleLongText(), oracleLongBytes()); err != nil {
		t.Fatalf("seeding the long values: %v", err)
	}
}

// checkOracleRich reads a restored schema back: the rows as they were, and
// the structure by what it enforces.
func checkOracleRich(t *testing.T, db *sql.DB) {
	t.Helper()
	one := func(query string) string { return queryString(t, db, query) }
	for query, want := range map[string]string{
		`SELECT TO_CHAR(COUNT(*)) FROM PEOPLE`:                                                                 "2",
		`SELECT TO_CHAR(COUNT(*)) FROM "notes"`:                                                                "3",
		`SELECT TO_CHAR(NICK) FROM PEOPLE WHERE ID = 1`:                                                        "Zoë",
		`SELECT TO_CHAR(BIO) FROM PEOPLE WHERE ID = 1`:                                                         "short; -- bio",
		`SELECT RAWTOHEX(DBMS_LOB.SUBSTR(AVATAR, 10, 1)) FROM PEOPLE WHERE ID = 1`:                             "00FF41",
		`SELECT RAWTOHEX(TOKEN) FROM PEOPLE WHERE ID = 1`:                                                      "6F9619FF8B86D011B42D00C04FC964FF",
		`SELECT TO_CHAR(SCORE, 'FM999990.00') FROM PEOPLE WHERE ID = 1`:                                        "12.50",
		`SELECT TO_CHAR(BIG) FROM PEOPLE WHERE ID = 1`:                                                         "12345678901234567890123456789012345678",
		`SELECT TO_CHAR(TINY, 'FM0.000000') FROM PEOPLE WHERE ID = 1`:                                          "-0.000015",
		`SELECT TO_CHAR(BORN, 'YYYY-MM-DD HH24:MI:SS') FROM PEOPLE WHERE ID = 1`:                               "1990-02-03 04:05:06",
		`SELECT TO_CHAR(SEEN_AT, 'YYYY-MM-DD HH24:MI:SS.FF9') FROM PEOPLE WHERE ID = 1`:                        "2026-03-04 05:06:07.123456789",
		`SELECT TO_CHAR(SEEN_TZ, 'YYYY-MM-DD HH24:MI:SS.FF6 TZH:TZM') FROM PEOPLE WHERE ID = 1`:                "2026-03-04 05:06:07.123456 +02:00",
		`SELECT TO_CHAR(SYS_EXTRACT_UTC(SEEN_LTZ), 'YYYY-MM-DD HH24:MI:SS.FF6') FROM PEOPLE WHERE ID = 1`:      "2026-03-04 03:06:07.123456",
		`SELECT TO_CHAR(GAP) FROM PEOPLE WHERE ID = 1`:                                                         "+01 02:03:04.500000",
		`SELECT EMAIL FROM PEOPLE WHERE ID = 2`:                                                                "o'brien@x.io",
		`SELECT SHOUT FROM PEOPLE WHERE ID = 2`:                                                                "O'BRIEN@X.IO",
		`SELECT CASE WHEN RATIO IS NAN THEN 'nan' ELSE 'number' END FROM PEOPLE WHERE ID = 2`:                  "nan",
		`SELECT TO_CHAR(TICKET) FROM PEOPLE WHERE ID = 2`:                                                      "105",
		`SELECT "body" FROM "notes" WHERE "id" = 2`:                                                            "two\nlines",
		`SELECT NVL("body", 'null') FROM "notes" WHERE "id" = 3`:                                               "null",
		`SELECT TO_CHAR(N) FROM ACTIVE_COUNT`:                                                                  "2",
		`SELECT virtual_column FROM user_tab_cols WHERE table_name = 'PEOPLE' AND column_name = 'SHOUT'`:       "YES",
		`SELECT generation_type FROM user_tab_identity_cols WHERE table_name = 'PEOPLE'`:                       "ALWAYS",
		`SELECT comments FROM user_tab_comments WHERE table_name = 'PEOPLE'`:                                   "Everyone; it's a test",
		`SELECT comments FROM user_col_comments WHERE table_name = 'PEOPLE' AND column_name = 'EMAIL'`:         "How to reach them",
		`SELECT TO_CHAR(COUNT(*)) FROM user_indexes WHERE index_name IN ('PEOPLE_BORN_IX', 'PEOPLE_LOWER_IX')`: "2",
		`SELECT descend FROM user_ind_columns WHERE index_name = 'PEOPLE_BORN_IX' AND column_position = 1`:     "DESC",
		`SELECT delete_rule FROM user_constraints WHERE constraint_name = 'notes_person_fk'`:                   "CASCADE",
		`SELECT constraint_type FROM user_constraints WHERE constraint_name = 'PEOPLE_EMAIL_UK'`:               "U",
		// The recycle bin is where a replaced table would otherwise be kept.
		`SELECT TO_CHAR(COUNT(*)) FROM user_recyclebin`: "0",
	} {
		if got := one(query); got != want {
			t.Errorf("%s\n got %q\nwant %q", strings.Join(strings.Fields(query), " "), got, want)
		}
	}
	ctx := context.Background()
	var (
		bio    string
		avatar []byte
	)
	if err := db.QueryRowContext(ctx, `SELECT BIO, AVATAR FROM PEOPLE WHERE ID = 2`).Scan(&bio, &avatar); err != nil {
		t.Fatal(err)
	}
	if bio != oracleLongText() {
		t.Errorf("the long text came back as %d bytes, want %d", len(bio), len(oracleLongText()))
	}
	if !bytes.Equal(avatar, oracleLongBytes()) {
		t.Errorf("the long bytes came back as %d bytes, want %d", len(avatar), len(oracleLongBytes()))
	}
	// The table still numbers its own rows, past the id a deleted row took,
	// and still refuses to be given one.
	if _, err := db.ExecContext(ctx, `INSERT INTO PEOPLE (ID, EMAIL) VALUES (99, 'given@x.io')`); err == nil {
		t.Error("the id column takes a number it is given: it is no longer GENERATED ALWAYS")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO PEOPLE (EMAIL) VALUES ('new@x.io')`); err != nil {
		t.Fatalf("an insert that leaves the id to the table: %v", err)
	}
	if got := one(`SELECT TO_CHAR(ID) FROM PEOPLE WHERE EMAIL = 'new@x.io'`); got == "1" || got == "2" || got == "3" {
		t.Errorf("the next id handed out is %s, which a row before the dump already had", got)
	}
	// Three rows drew from the sequence before the dump: 100, 105 and 110.
	if got := one(`SELECT TO_CHAR(TICKET) FROM PEOPLE WHERE EMAIL = 'new@x.io'`); got != "115" {
		t.Errorf("the sequence handed out %s next, want 115", got)
	}
	for name, statement := range map[string]string{
		"the unique constraint": `INSERT INTO PEOPLE (EMAIL) VALUES ('ann@x.io')`,
		"the check":             `INSERT INTO PEOPLE (EMAIL, SCORE) VALUES ('neg@x.io', -1)`,
		"the foreign key":       `INSERT INTO "notes" VALUES (9, 999, 'orphan')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err == nil {
			t.Errorf("%s did not come back: %s was accepted", name, statement)
		}
	}
}

// TestLiveBuiltInOracleDumpIsFaithful dumps a schema, loads the dump over it
// after it has been changed and after it has been emptied, and reads the
// structure back out of the catalogue.
func TestLiveBuiltInOracleDumpIsFaithful(t *testing.T) {
	db, dsn, schema := liveOwnOracle(t, "jd_transfer_live")
	ctx := context.Background()
	seedOracleRich(t, db)

	res, err := DumpWith(ctx, DriverOracle, dsn, t.TempDir(), DumpOptions{})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	text := readDump(t, res.Path)
	if res.Tool != BuiltInDumpTool || !strings.Contains(res.Summary, "2 tables, 5 rows") || !strings.Contains(res.Summary, "2 views") {
		t.Errorf("result = %+v", res)
	}
	if !strings.Contains(text, `CREATE TABLE "`+schema+`"."PEOPLE"`) {
		t.Errorf("the dump is not of schema %s:\n%s", schema, clipDumpText(text))
	}
	if strings.Contains(text, "SKIPPED") {
		t.Errorf("the dump left something out:\n%s", clipDumpText(text))
	}
	for _, storage := range []string{"TABLESPACE", "PCTFREE", "STORAGE("} {
		if strings.Contains(text, storage) {
			t.Errorf("the dump carries %s: where the rows were kept is not part of the schema", storage)
		}
	}

	// Over the schema as it now is: rows changed, a view gone.
	execAll(t, db,
		`DELETE FROM "notes"`,
		`UPDATE PEOPLE SET EMAIL = 'changed@x.io' WHERE ID = 1`,
		`DROP VIEW ACTIVE_COUNT`,
	)
	if out, err := RestoreWith(ctx, DriverOracle, dsn, res.Path, RestoreOptions{}); err != nil {
		t.Fatalf("RestoreWith: %v\n%s\n%s", err, out, clipDumpText(text))
	}
	checkOracleRich(t, db)
	if t.Failed() {
		t.Logf("the dump:\n%s", clipDumpText(text))
	}

	// And into the schema with nothing in it, where every DROP finds nothing.
	execAll(t, db,
		`DROP VIEW ACTIVE_COUNT`, `DROP VIEW ACTIVE_PEOPLE`,
		`DROP TABLE "notes" PURGE`, `DROP TABLE PEOPLE PURGE`, `DROP SEQUENCE TICKET_SEQ`,
	)
	out, err := RestoreWith(ctx, DriverOracle, dsn, res.Path, RestoreOptions{})
	if err != nil {
		t.Fatalf("RestoreWith into an empty schema: %v", err)
	}
	if !strings.Contains(out, "drops skipped") {
		t.Errorf("the restore says %q of a schema that had nothing to drop", out)
	}
	checkOracleRich(t, db)
}

// clipDumpText keeps a dump's long lines short enough to read in a test log.
func clipDumpText(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if len(line) > 400 {
			lines[i] = line[:400] + "…"
		}
	}
	if len(lines) > 80 {
		lines = append(lines[:80], "…")
	}
	return strings.Join(lines, "\n")
}

// TestLiveBuiltInOracleDumpStaysInItsSchema is the property a shared server
// depends on: a dump holds the schema it was taken of and no other, whoever
// took it and whatever else that login can see.
func TestLiveBuiltInOracleDumpStaysInItsSchema(t *testing.T) {
	db, dsn, schema := liveOwnOracle(t, "jd_transfer_live")
	other, _, otherSchema := liveOwnOracle(t, "jd_transfer_other")
	ctx := context.Background()
	seedOracleRich(t, db)
	// Another schema with a table of the same name, which this login may
	// read — so the catalogue lists it among the tables it can see.
	execAll(t, other,
		`CREATE TABLE PEOPLE (ID NUMBER PRIMARY KEY, EMAIL VARCHAR2(255))`,
		`INSERT INTO PEOPLE VALUES (7, 'not yours')`,
		`CREATE TABLE JD_OTHER_ONLY (ID NUMBER PRIMARY KEY)`,
		`CREATE SEQUENCE TICKET_SEQ START WITH 9000 NOCACHE`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON PEOPLE TO `+schema,
		`GRANT SELECT ON JD_OTHER_ONLY TO `+schema,
	)
	untouched := func(t *testing.T) {
		t.Helper()
		for query, want := range map[string]string{
			`SELECT EMAIL FROM PEOPLE WHERE ID = 7`:                                              "not yours",
			`SELECT TO_CHAR(COUNT(*)) FROM PEOPLE`:                                               "1",
			`SELECT TO_CHAR(COUNT(*)) FROM JD_OTHER_ONLY`:                                        "0",
			`SELECT TO_CHAR(COUNT(*)) FROM user_tables`:                                          "2",
			`SELECT TO_CHAR(last_number) FROM user_sequences WHERE sequence_name = 'TICKET_SEQ'`: "9000",
		} {
			if got := queryString(t, other, query); got != want {
				t.Errorf("in the schema next door, %s = %q, want %q", query, got, want)
			}
		}
	}

	check := func(t *testing.T, dsn string, opts DumpOptions) {
		t.Helper()
		res, err := DumpWith(ctx, DriverOracle, dsn, t.TempDir(), opts)
		if err != nil {
			t.Fatalf("DumpWith: %v", err)
		}
		text := readDump(t, res.Path)
		for _, foreign := range []string{otherSchema, "JD_OTHER_ONLY", "not yours", "START WITH 9000", `"SYSTEM"`, `"SYS"`} {
			if strings.Contains(text, foreign) {
				t.Errorf("the dump of %s holds %q", schema, foreign)
			}
		}
		if !strings.Contains(res.Summary, "2 tables, 5 rows") {
			t.Errorf("the dump holds %s, want this schema's 2 tables and 5 rows", res.Summary)
		}
		execAll(t, db, `DELETE FROM "notes"`)
		if _, err := RestoreWith(ctx, DriverOracle, dsn, res.Path, RestoreOptions{Database: opts.Database}); err != nil {
			t.Fatalf("RestoreWith: %v", err)
		}
		if got := queryString(t, db, `SELECT TO_CHAR(COUNT(*)) FROM "notes"`); got != "3" {
			t.Errorf("the schema's own rows did not come back: %s notes", got)
		}
		untouched(t)
	}
	t.Run("as_its_owner", func(t *testing.T) { check(t, dsn, DumpOptions{}) })

	// The administrator sees every schema on the server. Told which one to
	// dump, it dumps that one; told nothing, it is in a schema of the
	// server's own, and that is refused rather than dumped.
	adminDSN := os.Getenv(transferOracleAdminEnv)
	t.Run("as_the_administrator_told_which", func(t *testing.T) {
		check(t, adminDSN, DumpOptions{Database: schema})
	})
	t.Run("as_the_administrator_told_nothing", func(t *testing.T) {
		_, err := DumpWith(ctx, DriverOracle, adminDSN, t.TempDir(), DumpOptions{})
		if err == nil || !strings.Contains(err.Error(), "one of Oracle's own schemas") {
			t.Errorf("a dump of the administrator's own schema: %v", err)
		}
		untouched(t)
	})
}

// TestLiveOracleDumpHonoursItsOptions takes the narrower dumps: the structure
// alone, the rows alone, and some of the tables.
func TestLiveOracleDumpHonoursItsOptions(t *testing.T) {
	db, dsn, _ := liveOwnOracle(t, "jd_transfer_live")
	ctx := context.Background()
	seedOracleRich(t, db)
	full, err := DumpWith(ctx, DriverOracle, dsn, t.TempDir(), DumpOptions{Compression: CompressionGzip})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	if !strings.HasSuffix(full.Path, ".sql.gz") {
		t.Errorf("a compressed dump is called %s", full.File)
	}

	t.Run("rows_only", func(t *testing.T) {
		res, err := DumpWith(ctx, DriverOracle, dsn, t.TempDir(), DumpOptions{DataOnly: true})
		if err != nil {
			t.Fatalf("DumpWith: %v", err)
		}
		text := readDump(t, res.Path)
		if strings.Contains(text, "CREATE ") || strings.Contains(text, "DROP ") {
			t.Errorf("a dump of the rows carries structure:\n%s", clipDumpText(text))
		}
		if strings.Index(text, `INSERT INTO "JD_TRANSFER_LIVE"."PEOPLE"`) > strings.Index(text, `INSERT INTO "JD_TRANSFER_LIVE"."notes"`) {
			t.Errorf("a child's rows are written before its parent's:\n%s", clipDumpText(text))
		}
		// Into the tables as they stand, one of which always numbers its own
		// rows.
		execAll(t, db, `DELETE FROM "notes"`, `DELETE FROM PEOPLE`)
		if _, err := RestoreWith(ctx, DriverOracle, dsn, res.Path, RestoreOptions{}); err != nil {
			t.Fatalf("RestoreWith: %v\n%s", err, clipDumpText(text))
		}
		for query, want := range map[string]string{
			`SELECT TO_CHAR(COUNT(*)) FROM PEOPLE`:                                           "2",
			`SELECT TO_CHAR(COUNT(*)) FROM "notes"`:                                          "3",
			`SELECT generation_type FROM user_tab_identity_cols WHERE table_name = 'PEOPLE'`: "ALWAYS",
		} {
			if got := queryString(t, db, query); got != want {
				t.Errorf("%s = %q, want %q", query, got, want)
			}
		}
	})

	t.Run("some_tables", func(t *testing.T) {
		// The child alone: its parent, the views and the sequence are not in
		// the file and are not touched.
		res, err := DumpWith(ctx, DriverOracle, dsn, t.TempDir(), DumpOptions{Tables: []string{"notes"}})
		if err != nil {
			t.Fatalf("DumpWith: %v", err)
		}
		text := readDump(t, res.Path)
		if strings.Contains(text, `"PEOPLE" `) && strings.Contains(text, `CREATE TABLE "JD_TRANSFER_LIVE"."PEOPLE"`) ||
			strings.Contains(text, "VIEW") || strings.Contains(text, "TICKET_SEQ") {
			t.Errorf("a dump of one table carries others:\n%s", clipDumpText(text))
		}
		execAll(t, db, `DELETE FROM "notes" WHERE "id" = 1`)
		if _, err := RestoreWith(ctx, DriverOracle, dsn, res.Path, RestoreOptions{}); err != nil {
			t.Fatalf("RestoreWith: %v\n%s", err, clipDumpText(text))
		}
		// The parent alone. Dropping it takes the child's key with it, and
		// the key is put back on the child the dump left alone.
		res, err = DumpWith(ctx, DriverOracle, dsn, t.TempDir(), DumpOptions{ExcludeTables: []string{"notes"}})
		if err != nil {
			t.Fatalf("DumpWith: %v", err)
		}
		text = readDump(t, res.Path)
		execAll(t, db, `UPDATE PEOPLE SET NICK = N'changed' WHERE ID = 1`)
		if _, err := RestoreWith(ctx, DriverOracle, dsn, res.Path, RestoreOptions{}); err != nil {
			t.Fatalf("RestoreWith: %v\n%s", err, clipDumpText(text))
		}
		for query, want := range map[string]string{
			`SELECT TO_CHAR(NICK) FROM PEOPLE WHERE ID = 1`:                                      "Zoë",
			`SELECT TO_CHAR(COUNT(*)) FROM "notes"`:                                              "3",
			`SELECT delete_rule FROM user_constraints WHERE constraint_name = 'notes_person_fk'`: "CASCADE",
			`SELECT status FROM user_constraints WHERE constraint_name = 'notes_person_fk'`:      "ENABLED",
		} {
			if got := queryString(t, db, query); got != want {
				t.Errorf("%s = %q, want %q", query, got, want)
			}
		}
		if _, err := DumpWith(ctx, DriverOracle, dsn, t.TempDir(), DumpOptions{Tables: []string{"NO_SUCH_TABLE"}}); err == nil ||
			!strings.Contains(err.Error(), "no such table to dump") {
			t.Errorf("a table that is not there: %v", err)
		}
		// A view and a sequence are there to be asked for by name; a view
		// nobody named is not dragged in.
		res, err = DumpWith(ctx, DriverOracle, dsn, t.TempDir(), DumpOptions{Tables: []string{"notes", "ACTIVE_PEOPLE", "TICKET_SEQ"}})
		if err != nil {
			t.Fatalf("a dump that names a view and a sequence: %v", err)
		}
		text = readDump(t, res.Path)
		if !strings.Contains(text, `VIEW "JD_TRANSFER_LIVE"."ACTIVE_PEOPLE"`) || strings.Contains(text, "ACTIVE_COUNT") ||
			!strings.Contains(text, `CREATE SEQUENCE  "JD_TRANSFER_LIVE"."TICKET_SEQ"`) {
			t.Errorf("a dump of a table, a view and a sequence by name:\n%s", clipDumpText(text))
		}
	})

	t.Run("structure_only", func(t *testing.T) {
		res, err := DumpWith(ctx, DriverOracle, dsn, t.TempDir(), DumpOptions{SchemaOnly: true})
		if err != nil {
			t.Fatalf("DumpWith: %v", err)
		}
		text := readDump(t, res.Path)
		if strings.Contains(text, "INSERT INTO") || strings.Contains(text, "MODIFY") || !strings.Contains(res.Summary, "structure only") {
			t.Errorf("a dump of the structure carries rows (%s):\n%s", res.Summary, clipDumpText(text))
		}
		if _, err := RestoreWith(ctx, DriverOracle, dsn, res.Path, RestoreOptions{}); err != nil {
			t.Fatalf("RestoreWith: %v\n%s", err, clipDumpText(text))
		}
		// Empty, and still numbering its own rows. Where the numbering starts
		// is the server's to say: it writes a definition from where the
		// counter stands, not from where it began.
		execAll(t, db, `INSERT INTO PEOPLE (EMAIL) VALUES ('first@x.io')`)
		for query, want := range map[string]string{
			`SELECT TO_CHAR(COUNT(*)) FROM PEOPLE`:                                           "1",
			`SELECT TO_CHAR(COUNT(*)) FROM "notes"`:                                          "0",
			`SELECT TO_CHAR(N) FROM ACTIVE_COUNT`:                                            "1",
			`SELECT generation_type FROM user_tab_identity_cols WHERE table_name = 'PEOPLE'`: "ALWAYS",
		} {
			if got := queryString(t, db, query); got != want {
				t.Errorf("%s = %q, want %q", query, got, want)
			}
		}
		// And the whole dump, compressed, back over that.
		if _, err := RestoreWith(ctx, DriverOracle, dsn, full.Path, RestoreOptions{}); err != nil {
			t.Fatalf("RestoreWith: %v", err)
		}
		checkOracleRich(t, db)
	})
}

// TestLiveOracleImport loads files into Oracle: dates in the forms a file
// carries, which the server reads by its session's own format and no other;
// rows the table refuses, left out or ending the import; and an upsert.
func TestLiveOracleImport(t *testing.T) {
	db, _, schema := liveOwnOracle(t, "jd_transfer_live")
	ctx := context.Background()
	execAll(t, db, `CREATE TABLE "things" (
		"id" NUMBER(10) PRIMARY KEY,
		"name" VARCHAR2(20) NOT NULL CONSTRAINT "things_name" UNIQUE,
		"price" NUMBER(10,2),
		"born" DATE,
		"seen" TIMESTAMP(6),
		"tz" TIMESTAMP(0) WITH TIME ZONE,
		"raw" BLOB,
		"live" NUMBER(1)
	)`)
	count := func() string { return queryString(t, db, `SELECT TO_CHAR(COUNT(*)) FROM "things"`) }

	// 1200 rows, one statement each on this engine, with three it refuses.
	var file strings.Builder
	file.WriteString("id,name,price\n")
	for i := 1; i <= 1200; i++ {
		id, name, price := strconv.Itoa(i), "n"+strconv.Itoa(i), "1.5"
		switch i {
		case 400:
			price = "lots"
		case 800:
			id = "5"
		case 1000:
			name = strings.Repeat("x", 40)
		}
		file.WriteString(id + "," + name + "," + price + "\n")
	}
	report, err := Import(ctx, db, DriverOracle, strings.NewReader(file.String()),
		ImportSpec{Schema: schema, Table: "things", SkipBadRows: true})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 1197 || report.Skipped != 3 || !report.Atomic || count() != "1197" {
		t.Fatalf("inserted %d, skipped %d, %s rows in the table: %+v", report.Inserted, report.Skipped, count(), report.Errors)
	}
	if len(report.Errors) != 3 || report.Errors[0].Line != 401 || report.Errors[1].Line != 801 || report.Errors[2].Line != 1001 {
		t.Errorf("errors = %+v", report.Errors)
	}
	execAll(t, db, `DELETE FROM "things"`)
	_, err = Import(ctx, db, DriverOracle, strings.NewReader(file.String()), ImportSpec{Schema: schema, Table: "things"})
	if err == nil || !strings.Contains(err.Error(), "row 400 (line 401)") || count() != "0" {
		t.Fatalf("an import with a bad row: %v (%s rows)", err, count())
	}

	// Dates and instants as ISO writes them, a word for a number, bytes.
	report, err = Import(ctx, db, DriverOracle, strings.NewReader(
		"id,name,price,born,seen,tz,raw,live\n"+
			`1,ann,1234.5,2026-03-04,2026-03-04 05:06:07.123456,2026-03-04T05:06:07+02:00,\x00ff41,true`+"\n"+
			"2,bo,,2026-03-04T05:06:07Z,2026-03-04T05:06,,,false\n"+
			"3,cy,,,,,,\n"),
		ImportSpec{Schema: schema, Table: "things"})
	if err != nil || report.Inserted != 3 {
		t.Fatalf("Import: %+v, %v", report, err)
	}
	for query, want := range map[string]string{
		`SELECT TO_CHAR("price") FROM "things" WHERE "id" = 1`:                                                          "1234.5",
		`SELECT TO_CHAR("born", 'YYYY-MM-DD HH24:MI:SS') FROM "things" WHERE "id" = 1`:                                  "2026-03-04 00:00:00",
		`SELECT TO_CHAR("seen", 'YYYY-MM-DD HH24:MI:SS.FF6') FROM "things" WHERE "id" = 1`:                              "2026-03-04 05:06:07.123456",
		`SELECT TO_CHAR("tz", 'YYYY-MM-DD HH24:MI:SS TZH:TZM') FROM "things" WHERE "id" = 1`:                            "2026-03-04 05:06:07 +02:00",
		`SELECT RAWTOHEX(DBMS_LOB.SUBSTR("raw", 10, 1)) FROM "things" WHERE "id" = 1`:                                   "00FF41",
		`SELECT TO_CHAR("live") FROM "things" WHERE "id" = 1`:                                                           "1",
		`SELECT TO_CHAR("born", 'YYYY-MM-DD HH24:MI:SS') FROM "things" WHERE "id" = 2`:                                  "2026-03-04 05:06:07",
		`SELECT TO_CHAR("seen", 'YYYY-MM-DD HH24:MI:SS') FROM "things" WHERE "id" = 2`:                                  "2026-03-04 05:06:00",
		`SELECT TO_CHAR("live") FROM "things" WHERE "id" = 2`:                                                           "0",
		`SELECT TO_CHAR(COUNT(*)) FROM "things" WHERE "id" = 3 AND "born" IS NULL AND "raw" IS NULL AND "live" IS NULL`: "1",
	} {
		if got := queryString(t, db, query); got != want {
			t.Errorf("%s = %q, want %q", strings.Join(strings.Fields(query), " "), got, want)
		}
	}

	// An upsert finds rows by key and says which it added and which it
	// overwrote, past a row it has to leave out; and on the unique constraint
	// by its name, where the file carries no key of the table's own.
	report, err = Import(ctx, db, DriverOracle, strings.NewReader("id,name,price\n1,ann,10\n4,di,lots\n5,ed,5\n"),
		ImportSpec{Schema: schema, Table: "things", Mode: ImportModeUpsert, SkipBadRows: true})
	if err != nil || report.Inserted != 1 || report.Updated != 1 || report.Skipped != 1 || count() != "4" {
		t.Fatalf("upsert: %+v, %v (%s rows)", report, err, count())
	}
	report, err = Import(ctx, db, DriverOracle, strings.NewReader("name,price\nbo,77\n"),
		ImportSpec{Schema: schema, Table: "things", Mode: ImportModeUpsert, ConflictConstraint: "things_name"})
	if err != nil || report.Updated != 1 || report.Inserted != 0 {
		t.Fatalf("upsert on a named constraint: %+v, %v", report, err)
	}
	for query, want := range map[string]string{
		`SELECT TO_CHAR("price") FROM "things" WHERE "id" = 1`: "10",
		`SELECT TO_CHAR("price") FROM "things" WHERE "id" = 2`: "77",
		// A column the file did not carry is as it was.
		`SELECT TO_CHAR("live") FROM "things" WHERE "id" = 1`: "1",
	} {
		if got := queryString(t, db, query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}

	// A file that carries a column the table works out for itself — an
	// export of the table does — goes in without it.
	execAll(t, db, `ALTER TABLE "things" ADD ("shout" VARCHAR2(40) GENERATED ALWAYS AS (UPPER("name")) VIRTUAL)`)
	report, err = Import(ctx, db, DriverOracle, strings.NewReader("id,name,shout\n50,zed,ignored\n"),
		ImportSpec{Schema: schema, Table: "things"})
	if err != nil || report.Inserted != 1 || len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "shout is filled in by the server") {
		t.Fatalf("a file with a virtual column: %+v, %v", report, err)
	}
	if got := queryString(t, db, `SELECT "shout" FROM "things" WHERE "id" = 50`); got != "ZED" {
		t.Errorf("the virtual column holds %q", got)
	}
	execAll(t, db, `DELETE FROM "things" WHERE "id" = 50`)

	// A replace that fails leaves the table as it was: the rows are deleted
	// inside the transaction, not truncated outside it.
	_, err = Import(ctx, db, DriverOracle, strings.NewReader("id,name\n9,z\n9,again\n"),
		ImportSpec{Schema: schema, Table: "things", Mode: ImportModeReplace})
	if err == nil || count() != "4" {
		t.Fatalf("a replace that fails: %v (%s rows)", err, count())
	}
}

// An XMLTYPE column is the one Oracle type its driver cannot read as it
// stands: a document comes back laid out afresh, and an empty cell fails the
// statement or holds it until its time runs out. The dump reads such a column
// as the text it was written as, and a restore puts that text back.
func TestLiveOracleDumpKeepsAnXMLColumn(t *testing.T) {
	db, dsn, _ := liveOwnOracle(t, "jd_transfer_live")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	execAll(t, db,
		`CREATE TABLE DOCS (ID NUMBER PRIMARY KEY, X XMLTYPE, NOTE VARCHAR2(20))`,
		`INSERT INTO DOCS VALUES (1, XMLTYPE('<a><b>1</b></a>'), 'full')`,
		`INSERT INTO DOCS VALUES (2, NULL, 'empty')`,
		// Longer than one string literal holds, which is written a piece at a time.
		`INSERT INTO DOCS VALUES (3, XMLTYPE(TO_CLOB('<r>') || TO_CLOB(RPAD('x', 3000, 'x')) || TO_CLOB(RPAD('y', 3000, 'y')) || TO_CLOB('</r>')), 'long')`,
	)
	res, err := DumpWith(ctx, DriverOracle, dsn, t.TempDir(), DumpOptions{})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	text := readDump(t, res.Path)
	if !strings.Contains(res.Summary, "1 tables, 3 rows") || strings.Contains(res.Summary, "skipped") {
		t.Errorf("summary = %q", res.Summary)
	}
	execAll(t, db, `DELETE FROM DOCS`)
	if out, err := RestoreWith(ctx, DriverOracle, dsn, res.Path, RestoreOptions{}); err != nil {
		t.Fatalf("RestoreWith: %v\n%s\n%s", err, out, clipDumpText(text))
	}
	for query, want := range map[string]string{
		`SELECT XMLSERIALIZE(CONTENT X AS VARCHAR2(200) NO INDENT) FROM DOCS WHERE ID = 1`:                     "<a><b>1</b></a>",
		`SELECT NVL2(X, 'kept', 'null') FROM DOCS WHERE ID = 2`:                                                "null",
		`SELECT TO_CHAR(DBMS_LOB.GETLENGTH(XMLSERIALIZE(CONTENT X AS CLOB NO INDENT))) FROM DOCS WHERE ID = 3`: "6007",
		`SELECT TO_CHAR(COUNT(*)) FROM DOCS`:                                                                   "3",
	} {
		if got := queryString(t, db, query); got != want {
			t.Errorf("%s\n got %q\nwant %q", query, got, want)
		}
	}
	if t.Failed() {
		t.Logf("the dump:\n%s", clipDumpText(text))
	}
}

// TestLiveOracleDumpOfTheLessOrdinary dumps the shapes a schema has less
// often: a table kept in its own index, a partitioned one, a temporary one
// whose rows are each session's own, an identity that only steps in for a
// NULL, names that need their quotes, two tables that point at each other —
// and beside them a materialized view and its log, whose storage the
// catalogue lists as tables and a dump must not write as tables.
func TestLiveOracleDumpOfTheLessOrdinary(t *testing.T) {
	db, dsn, _ := liveOwnOracle(t, "jd_transfer_live")
	ctx := context.Background()
	execAll(t, db,
		`CREATE TABLE IOT (K NUMBER PRIMARY KEY, V VARCHAR2(10)) ORGANIZATION INDEX`,
		`CREATE TABLE PARTS (ID NUMBER PRIMARY KEY, AT DATE NOT NULL)
			PARTITION BY RANGE (AT) (PARTITION P_OLD VALUES LESS THAN (DATE '2026-01-01'), PARTITION P_NEW VALUES LESS THAN (MAXVALUE))`,
		`CREATE GLOBAL TEMPORARY TABLE SCRATCH (ID NUMBER) ON COMMIT PRESERVE ROWS`,
		`CREATE TABLE "Mixed Case" ("a b" NUMBER GENERATED BY DEFAULT ON NULL AS IDENTITY PRIMARY KEY, "é" NVARCHAR2(20))`,
		`CREATE TABLE HEN (ID NUMBER PRIMARY KEY, EGG_ID NUMBER)`,
		`CREATE TABLE EGG (ID NUMBER PRIMARY KEY, HEN_ID NUMBER CONSTRAINT EGG_HEN_FK REFERENCES HEN(ID))`,
		`ALTER TABLE HEN ADD CONSTRAINT HEN_EGG_FK FOREIGN KEY (EGG_ID) REFERENCES EGG(ID)`,
		`INSERT INTO IOT VALUES (1, 'one')`, `INSERT INTO IOT VALUES (2, 'two')`,
		`INSERT INTO PARTS VALUES (1, DATE '2025-06-01')`, `INSERT INTO PARTS VALUES (2, DATE '2026-06-01')`,
		`INSERT INTO "Mixed Case" ("é") VALUES (N'Zoë')`, `INSERT INTO "Mixed Case" ("a b", "é") VALUES (10, N'ten')`,
		`INSERT INTO HEN VALUES (1, NULL)`, `INSERT INTO EGG VALUES (1, 1)`, `UPDATE HEN SET EGG_ID = 1`,
		`CREATE MATERIALIZED VIEW LOG ON PARTS WITH PRIMARY KEY`,
		`CREATE MATERIALIZED VIEW PARTS_MV AS SELECT ID, AT FROM PARTS`,
	)

	res, err := DumpWith(ctx, DriverOracle, dsn, t.TempDir(), DumpOptions{})
	if err != nil {
		t.Fatalf("DumpWith: %v", err)
	}
	text := readDump(t, res.Path)
	// Six tables, one of them with no rows of its own to write; not the
	// materialized view's storage, and not its log's.
	if !strings.Contains(res.Summary, "6 tables, 8 rows") || strings.Contains(res.Summary, "skipped") {
		t.Errorf("summary = %q", res.Summary)
	}
	for _, storage := range []string{"PARTS_MV", "MLOG$", "RUPD$", `INSERT INTO "JD_TRANSFER_LIVE"."SCRATCH"`} {
		if strings.Contains(text, storage) {
			t.Errorf("the dump writes %s:\n%s", storage, clipDumpText(text))
		}
	}

	check := func(t *testing.T) {
		t.Helper()
		for query, want := range map[string]string{
			`SELECT V FROM IOT WHERE K = 2`:                                                                                               "two",
			`SELECT iot_type FROM user_tables WHERE table_name = 'IOT'`:                                                                   "IOT",
			`SELECT TO_CHAR(COUNT(*)) FROM PARTS PARTITION (P_NEW)`:                                                                       "1",
			`SELECT TO_CHAR(COUNT(*)) FROM user_tab_partitions WHERE table_name = 'PARTS'`:                                                "2",
			`SELECT temporary || duration FROM user_tables WHERE table_name = 'SCRATCH'`:                                                  "YSYS$SESSION",
			`SELECT TO_CHAR("é") FROM "Mixed Case" WHERE "a b" = 1`:                                                                       "Zoë",
			`SELECT generation_type FROM user_tab_identity_cols WHERE table_name = 'Mixed Case'`:                                          "BY DEFAULT",
			`SELECT default_on_null FROM user_tab_columns WHERE table_name = 'Mixed Case' AND column_name = 'a b'`:                        "YES",
			`SELECT TO_CHAR(EGG_ID) FROM HEN WHERE ID = 1`:                                                                                "1",
			`SELECT TO_CHAR(COUNT(*)) FROM user_constraints WHERE constraint_name IN ('EGG_HEN_FK', 'HEN_EGG_FK') AND status = 'ENABLED'`: "2",
		} {
			if got := queryString(t, db, query); got != want {
				t.Errorf("%s\n got %q\nwant %q", strings.Join(strings.Fields(query), " "), got, want)
			}
		}
		// The identity still steps in for a NULL, past the ids already there.
		execAll(t, db, `INSERT INTO "Mixed Case" ("a b", "é") VALUES (NULL, N'next')`)
		if got := queryString(t, db, `SELECT TO_CHAR("a b") FROM "Mixed Case" WHERE "é" = N'next'`); got == "1" || got == "10" || got == "" {
			t.Errorf("the identity gave %q to a row that left it NULL", got)
		}
		execAll(t, db, `DELETE FROM "Mixed Case" WHERE "é" = N'next'`)
	}

	// A session that still holds rows in the temporary table keeps it from
	// being dropped. That is said as what it is, by the statement it stopped
	// at; passed over as "nothing to drop", it used to surface one statement
	// later as a name that was already taken.
	holder, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.ExecContext(ctx, `INSERT INTO SCRATCH VALUES (2)`); err != nil {
		t.Fatal(err)
	}
	_, err = RestoreWith(ctx, DriverOracle, dsn, res.Path, RestoreOptions{})
	if err == nil || !strings.Contains(err.Error(), "ORA-14452") || !strings.Contains(err.Error(), `DROP TABLE "JD_TRANSFER_LIVE"."SCRATCH"`) {
		t.Errorf("a restore over a temporary table in use: %v", err)
	}
	if _, err := holder.ExecContext(ctx, `TRUNCATE TABLE SCRATCH`); err != nil {
		t.Fatal(err)
	}
	holder.Close()

	// Over the schema as it stands, changed since.
	execAll(t, db, `UPDATE HEN SET EGG_ID = NULL`, `DELETE FROM IOT`, `DELETE FROM PARTS WHERE ID = 2`)
	if out, err := RestoreWith(ctx, DriverOracle, dsn, res.Path, RestoreOptions{}); err != nil {
		t.Fatalf("RestoreWith: %v\n%s\n%s", err, out, clipDumpText(text))
	}
	check(t)
	if t.Failed() {
		t.Logf("the dump:\n%s", clipDumpText(text))
	}
}
