package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// statementsShowOnlyShapes runs each statement a few times as the connection's
// own account, then reads /statements as a viewer and checks that nothing the
// statements carried reached the response while every shape did. Oracle and
// SQL Server keep the text as it was sent, so what they hand over is the
// masker's work; MySQL's digest text is the server's.
func statementsShowOnlyShapes(t *testing.T, direct *sql.DB, viewer http.Handler, base string, runs int, statements, shapes, secrets []string) {
	t.Helper()
	// Calls order the list, and the shared pool of a busy server holds statements
	// run hundreds of times; enough runs keep these on the first page.
	for _, stmt := range statements {
		for i := 0; i < runs; i++ {
			if _, err := direct.Exec(stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		body := mustStatus(t, viewer, http.MethodGet, base+"/statements?sort=calls&limit=200", "", http.StatusOK)
		// The figures are floats, and a run of digits in one is not a literal.
		var list struct {
			Statements []struct{ Query string }
		}
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatal(err)
		}
		var queries []string
		for _, st := range list.Statements {
			queries = append(queries, st.Query)
			for _, secret := range secrets {
				if strings.Contains(st.Query, secret) {
					t.Fatalf("%q reached a viewer in %q", secret, st.Query)
				}
			}
		}
		all := strings.Join(queries, "\n")
		var missing []string
		for _, shape := range shapes {
			if !strings.Contains(all, shape) {
				missing = append(missing, shape)
			}
		}
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("these never appeared by their shape among %d statements: %q", len(list.Statements), missing)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// Every form of a literal an Oracle statement can carry — INSERT, INSERT ALL,
// MERGE, a PL/SQL block, a comment, a hint — is read by a viewer as a shape.
// The shared pool is the cluster's, so the table and the markers are this
// stream's own.
func TestLiveAPIOracleStatementsCarryNoLiterals(t *testing.T) {
	dsn := opsLiveAnyDSN(t, "JD_TEST_B3_ORACLE_ADMIN_DSN", "JD_TEST_ORACLE_ADMIN_DSN")
	s, _, id := liveAPIRouter(t, dbx.DriverOracle, dsn)
	base := pathf("/databases/%d", id)
	viewer := asRole(s, auth.RoleReadOnly)
	direct, err := sql.Open("oracle", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { direct.Close() })
	drop := func() { direct.Exec(`DROP TABLE jdt_oracle_masking_t PURGE`) }
	drop()
	t.Cleanup(drop)
	if _, err := direct.Exec(`CREATE TABLE jdt_oracle_masking_t (id NUMBER, who VARCHAR2(80), note VARCHAR2(80))`); err != nil {
		t.Fatal(err)
	}

	statementsShowOnlyShapes(t, direct, viewer, base, 6,
		[]string{
			`INSERT INTO jdt_oracle_masking_t (id, who, note) VALUES (6142031, 'jdt-om-insert@example.com', 'jdt-om-note')`,
			`INSERT ALL INTO jdt_oracle_masking_t (id, who) VALUES (6142032, 'jdt-om-all-one') INTO jdt_oracle_masking_t (id, who) VALUES (6142033, N'jdt-om-all-two') SELECT 1 FROM dual`,
			`MERGE INTO jdt_oracle_masking_t d USING (SELECT 6142034 AS id, q'[jdt-om-merge's]' AS who FROM dual) s ON (d.id = s.id) WHEN MATCHED THEN UPDATE SET d.who = s.who WHEN NOT MATCHED THEN INSERT (id, who, note) VALUES (s.id, s.who, 'jdt-om-merge-note')`,
			`BEGIN INSERT INTO jdt_oracle_masking_t (id, who) VALUES (6142035, 'jdt-om-block'); UPDATE jdt_oracle_masking_t SET note = nq'#jdt-om-block-note#' WHERE id = 6142036 + 1.5E2; END;`,
			`SELECT COUNT(*) FROM jdt_oracle_masking_t /* jdt-om-comment 6142037 */ WHERE id = 6142038`,
			`SELECT /*+ opt_param('parallel_execution_enabled', 'jdt-om-hint') */ COUNT(*) FROM jdt_oracle_masking_t WHERE who = 'x'`,
			`SELECT COUNT(*) FROM jdt_oracle_masking_t WHERE id = 6142039 -- jdt-om-trailing 'jdt-om-after'`,
		},
		[]string{
			`INSERT INTO jdt_oracle_masking_t (id, who, note) VALUES (?, ?, ?)`,
			`INSERT ALL INTO jdt_oracle_masking_t (id, who) VALUES (?, ?) INTO jdt_oracle_masking_t (id, who) VALUES (?, ?) SELECT ? FROM dual`,
			`MERGE INTO jdt_oracle_masking_t d USING (SELECT ? AS id, ? AS who FROM dual)`,
			`BEGIN INSERT INTO jdt_oracle_masking_t (id, who) VALUES (?, ?); UPDATE jdt_oracle_masking_t SET note = ? WHERE id = ? + ?; END;`,
			`SELECT COUNT(*) FROM jdt_oracle_masking_t WHERE id = ?`,
			`SELECT /*+ opt_param(?, ?) */ COUNT(*) FROM jdt_oracle_masking_t WHERE who = ?`,
		},
		[]string{"jdt-om-", "61420", "6142031"},
	)
}

// SQL Server keeps line breaks, comments and nested comments in the text; the
// shape has none of what they said. It groups by query hash, which ignores
// constants and comments, so the statements differ in more than those.
func TestLiveAPISQLServerStatementsCarryNoLiterals(t *testing.T) {
	dsn := opsLiveAnyDSN(t, "JD_TEST_B3_MSSQL_DSN", "JD_TEST_MSSQL_DSN")
	s, _, id := liveAPIRouter(t, dbx.DriverMSSQL, dsn)
	base := pathf("/databases/%d", id)
	viewer := asRole(s, auth.RoleReadOnly)
	direct, err := sql.Open("sqlserver", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { direct.Close() })

	statementsShowOnlyShapes(t, direct, viewer, base, 6,
		[]string{
			"SELECT COUNT(*) FROM (VALUES (6142041, N'jdt-om-values')) v(a, b) WHERE v.a = 6142042 AND v.b = N'jdt-om-where'",
			"SELECT MIN(v.a) FROM (VALUES (1)) v(a) -- jdt-om-line 6142043\nWHERE v.a = 6142044 AND 'jdt-om-after' = 'jdt-om-after'",
			"SELECT /* jdt-om-block /* jdt-om-nested */ jdt-om-tail */ COUNT(*) FROM (VALUES (1)) v(a) WHERE v.a = 6142045 + 0x1F",
		},
		[]string{
			"WHERE v.a = ? AND v.b = ?",
			"SELECT MIN(v.a) FROM (VALUES (?)) v(a) \nWHERE v.a = ? AND ? = ?",
			"SELECT COUNT(*) FROM (VALUES (?)) v(a) WHERE v.a = ? + ?",
		},
		[]string{"jdt-om-", "61420", "6142041", "6142042"},
	)
}

// MySQL's digest text is normalised by the server; this keeps it that way.
func TestLiveAPIMySQLStatementsCarryNoLiterals(t *testing.T) {
	servers := map[string]string{}
	if admin := os.Getenv("JD_TEST_MYSQL_ADMIN_DSN"); admin != "" {
		database := "jdtest"
		if user := os.Getenv("JD_TEST_MYSQL_DSN"); strings.Contains(user, "/") {
			database = user[strings.LastIndex(user, "/")+1:]
		}
		servers["mariadb"] = strings.TrimSuffix(admin, "/") + "/" + database
	}
	if admin := os.Getenv("JD_TEST_MYSQL8_ADMIN_DSN"); admin != "" {
		servers["mysql8"] = strings.TrimSuffix(admin, "/") + "/jdtest"
	}
	if len(servers) == 0 {
		t.Skip("neither JD_TEST_MYSQL_ADMIN_DSN nor JD_TEST_MYSQL8_ADMIN_DSN is set")
	}
	for name, dsn := range servers {
		t.Run(name, func(t *testing.T) {
			s, _, id := liveAPIRouter(t, dbx.DriverMySQL, dsn)
			base := pathf("/databases/%d", id)
			viewer := asRole(s, auth.RoleReadOnly)
			direct, err := sql.Open("mysql", dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { direct.Close() })
			if body := mustStatus(t, viewer, http.MethodGet, base+"/statements", "", http.StatusOK); strings.Contains(body, `"supported":false`) {
				t.Skipf("this server keeps no statement statistics: %s", body)
			}

			statementsShowOnlyShapes(t, direct, viewer, base, 150,
				[]string{
					"SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'jdt-om-name' AND 6142051 = 6142051",
					"SELECT /* jdt-om-block 6142052 */ COUNT(*) FROM information_schema.tables WHERE table_name = 'jdt-om-x' -- jdt-om-line 6142053",
				},
				[]string{"FROM `information_schema` . `tables` WHERE TABLE_NAME = ? AND ? = ?", "FROM `information_schema` . `tables` WHERE TABLE_NAME = ?"},
				[]string{"jdt-om-", "61420"},
			)
		})
	}
}
