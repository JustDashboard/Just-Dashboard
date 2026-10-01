package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The operations routes against real servers, through the real handlers.
//
// These make accounts and change a server parameter, so — unlike the older
// live tests — they never fall back to an engine's standard port: with no
// variable naming a server that is safe to do that to, they skip.

func opsLiveDSN(t *testing.T, env string) string {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("%s is not set", env)
	}
	return dsn
}

func mustStatus(t *testing.T, r http.Handler, method, path, body string, want int) string {
	t.Helper()
	rec := do(t, r, method, path, body)
	if rec.Code != want {
		t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body.String(), want)
	}
	return rec.Body.String()
}

func TestLiveAPIOpsPostgres(t *testing.T) {
	dsn := opsLiveDSN(t, "JD_TEST_POSTGRES_DSN")
	s, r, id := liveAPIRouter(t, dbx.DriverPostgres, dsn)
	s.Cfg.BackupLocalDir = t.TempDir()
	base := pathf("/databases/%d", id)

	direct, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Closed by a cleanup rather than a defer: a defer runs when this function
	// returns, before the cleanups below, which would then drop nothing.
	t.Cleanup(func() { direct.Close() })
	cleanup := func() {
		direct.Exec(`DROP TABLE IF EXISTS jd_b3_api_t`)
		for _, role := range []string{"jd_b3_api_app", "jd_b3_api_self", "jd_b3_api_team"} {
			direct.Exec(`DROP OWNED BY ` + role)
			direct.Exec(`DROP ROLE IF EXISTS ` + role)
		}
		direct.Exec(`ALTER SYSTEM RESET deadlock_timeout`)
		direct.Exec(`SELECT pg_reload_conf()`)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := direct.Exec(`CREATE TABLE jd_b3_api_t (id int primary key, v text)`); err != nil {
		t.Fatal(err)
	}

	t.Run("stats", func(t *testing.T) {
		var stats struct {
			Supported   bool               `json:"supported"`
			Role        string             `json:"role"`
			Counters    map[string]float64 `json:"counters"`
			Connections struct {
				Max int `json:"max"`
			} `json:"connections"`
			Pool *struct {
				MaxOpen int `json:"maxOpen"`
			} `json:"pool"`
		}
		if err := json.Unmarshal([]byte(mustStatus(t, r, http.MethodGet, base+"/stats", "", http.StatusOK)), &stats); err != nil {
			t.Fatal(err)
		}
		if !stats.Supported || stats.Connections.Max <= 0 || stats.Pool == nil || stats.Pool.MaxOpen <= 0 || stats.Role == "" {
			t.Errorf("stats: %+v", stats)
		}
		if _, ok := stats.Counters["transactionsCommitted"]; !ok {
			t.Errorf("no commit counter: %+v", stats.Counters)
		}
	})

	t.Run("reads", func(t *testing.T) {
		for path, want := range map[string]string{
			"/locks":                           `"supported":true`,
			"/replication":                     `"role":"`,
			"/tablestats?schema=public":        `"table":"jd_b3_api_t"`,
			"/indexstats?table=jd_b3_api_t":    `"name":"jd_b3_api_t_pkey"`,
			"/maintenance":                     `"id":"vacuum_full"`,
			"/server/privileges":               `"level":"sequence"`,
			"/settings":                        `"name":"max_connections"`,
			"/statements?sort=mean&limit=3":    `"sort":"mean"`,
			"/advisor?schema=public":           `"engineChecks":true`,
			"/activity":                        `"status":"active"`,
			"/server/grants?table=jd_b3_api_t": `"supported":true`,
		} {
			if body := mustStatus(t, r, http.MethodGet, base+path, "", http.StatusOK); !strings.Contains(body, want) {
				t.Errorf("GET %s lacks %s: %s", path, want, body)
			}
		}
		var all struct {
			Settings []dbx.Setting `json:"settings"`
			Writable bool          `json:"writable"`
		}
		if err := json.Unmarshal([]byte(mustStatus(t, r, http.MethodGet, base+"/settings?all=1", "", http.StatusOK)), &all); err != nil {
			t.Fatal(err)
		}
		if len(all.Settings) < 200 || !all.Writable {
			t.Errorf("the full list has %d parameters, writable %v", len(all.Settings), all.Writable)
		}
	})

	t.Run("maintenance", func(t *testing.T) {
		body := mustStatus(t, r, http.MethodPost, base+"/maintenance", `{"action":"vacuum_analyze","table":"jd_b3_api_t"}`, http.StatusOK)
		if !strings.Contains(body, `VACUUM (VERBOSE, ANALYZE) \"public\".\"jd_b3_api_t\"`) || !strings.Contains(body, "INFO: ") {
			t.Errorf("vacuum: %s", body)
		}
		if body := mustStatus(t, r, http.MethodPost, base+"/maintenance", `{"action":"analyze","table":"jd_b3_no_such_table"}`, http.StatusBadRequest); !strings.Contains(body, "does not exist") {
			t.Errorf("a missing table: %s", body)
		}
	})

	t.Run("settings", func(t *testing.T) {
		body := mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"deadlock_timeout","value":"1234ms"}`, http.StatusOK)
		if !strings.Contains(body, `"persisted":true`) || !strings.Contains(body, `ALTER SYSTEM SET \"deadlock_timeout\" = '1234ms'`) {
			t.Errorf("set: %s", body)
		}
		mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"deadlock_timeout","value":"0"}`, http.StatusBadRequest)
		mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"server_version","value":"1"}`, http.StatusBadRequest)
		if body := mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"deadlock_timeout","reset":true}`, http.StatusOK); !strings.Contains(body, "ALTER SYSTEM RESET") {
			t.Errorf("reset: %s", body)
		}
	})

	// The whole of the password-change bug, through the route the dialog
	// uses: the body carries a password and nothing else, and the role must
	// come out with every attribute it went in with.
	t.Run("a_password_change_leaves_the_role_alone", func(t *testing.T) {
		mustStatus(t, r, http.MethodPost, base+"/server/roles",
			`{"name":"jd_b3_api_app","password":"first-pw","superuser":true,"createDb":true,"connectionLimit":5}`, http.StatusCreated)
		mustStatus(t, r, http.MethodPut, base+"/server/roles/jd_b3_api_app", `{"password":"second-pw","host":""}`, http.StatusOK)
		var detail dbx.RoleDetail
		read := func() {
			t.Helper()
			detail = dbx.RoleDetail{}
			if err := json.Unmarshal([]byte(mustStatus(t, r, http.MethodGet, base+"/server/roles/jd_b3_api_app", "", http.StatusOK)), &detail); err != nil {
				t.Fatal(err)
			}
		}
		read()
		if !detail.Superuser || !detail.CreateDB || !detail.Login || detail.ConnLimit != 5 {
			t.Fatalf("a password change altered the role: %+v", detail.Role)
		}
		if len(detail.Editable) == 0 || detail.Attributes == nil {
			t.Errorf("detail: %+v", detail)
		}
		mustStatus(t, r, http.MethodPut, base+"/server/roles/jd_b3_api_app", `{"superuser":false,"validUntil":"2031-01-02"}`, http.StatusOK)
		read()
		if detail.Superuser || !detail.CreateDB || !strings.HasPrefix(detail.ValidUntil, "2031-01-02") {
			t.Errorf("clearing one flag: %+v", detail.Role)
		}
		mustStatus(t, r, http.MethodPut, base+"/server/roles/jd_b3_api_app", `{}`, http.StatusBadRequest)
		mustStatus(t, r, http.MethodPut, base+"/server/roles/jd_b3_api_app", `{"locked":true}`, http.StatusBadRequest)
		mustStatus(t, r, http.MethodGet, base+"/server/roles/jd_b3_no_such_role", "", http.StatusNotFound)
	})

	t.Run("privileges", func(t *testing.T) {
		grant := `{"level":"table","schema":"public","table":"jd_b3_api_t","privileges":["SELECT","UPDATE"]}`
		can := func(privilege string) bool {
			t.Helper()
			var ok bool
			if err := direct.QueryRow(`SELECT has_table_privilege('jd_b3_api_app', 'public.jd_b3_api_t', $1)`, privilege).Scan(&ok); err != nil {
				t.Fatal(err)
			}
			return ok
		}
		// The role was a superuser a moment ago; it is not now, and holds
		// nothing on the table until it is granted.
		if can("SELECT") {
			t.Fatal("the role can already read the table")
		}
		preview := mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_app/privileges?preview=1", grant, http.StatusOK)
		if !strings.Contains(preview, `GRANT SELECT, UPDATE ON TABLE \"public\".\"jd_b3_api_t\" TO \"jd_b3_api_app\"`) || !strings.Contains(preview, `"preview":true`) {
			t.Errorf("preview: %s", preview)
		}
		if can("SELECT") {
			t.Fatal("a preview granted the privilege")
		}
		mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_app/privileges", grant, http.StatusOK)
		if !can("SELECT") || !can("UPDATE") || can("DELETE") {
			t.Error("the grant did not give exactly SELECT and UPDATE")
		}
		listed := mustStatus(t, r, http.MethodGet, base+"/server/grants?role=jd_b3_api_app", "", http.StatusOK)
		if !strings.Contains(listed, `"table":"jd_b3_api_t"`) || !strings.Contains(listed, `"privileges":["SELECT","UPDATE"]`) {
			t.Errorf("grants: %s", listed)
		}
		mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_app/privileges/revoke",
			`{"level":"table","schema":"public","table":"jd_b3_api_t","privileges":["UPDATE"]}`, http.StatusOK)
		if !can("SELECT") || can("UPDATE") {
			t.Error("the revoke did not take exactly UPDATE")
		}
		for _, bad := range []string{
			`{"level":"table","schema":"public","table":"jd_b3_api_t","privileges":["SELECT; DROP TABLE jd_b3_api_t"]}`,
			`{"level":"table","table":"jd_b3_api_t","privileges":["SELECT"]}`,
			`{"level":"cluster","privileges":["SELECT"]}`,
		} {
			mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_app/privileges", bad, http.StatusBadRequest)
		}
		if _, err := direct.Exec(`CREATE ROLE jd_b3_api_team`); err != nil {
			t.Fatal(err)
		}
		mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_app/privileges", `{"level":"role","memberOf":"jd_b3_api_team"}`, http.StatusOK)
		if body := mustStatus(t, r, http.MethodGet, base+"/server/roles/jd_b3_api_app", "", http.StatusOK); !strings.Contains(body, `"memberOf":["jd_b3_api_team"]`) {
			t.Errorf("membership: %s", body)
		}
		// The one-step database grant, narrowed to a schema.
		body := mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_app/grant", `{"level":"read","schema":"public"}`, http.StatusOK)
		if !strings.Contains(body, `GRANT USAGE ON SCHEMA \"public\"`) || !strings.Contains(body, `"schemas":["public"]`) {
			t.Errorf("database grant: %s", body)
		}
	})

	// Before the fact: what a grant on "this database" would reach, and who
	// else's future tables a schema-wide grant would cover. Neither changes
	// anything, and neither is on the audit trail.
	t.Run("previews", func(t *testing.T) {
		if _, err := direct.Exec(`REVOKE ALL ON ALL TABLES IN SCHEMA public FROM jd_b3_api_app`); err != nil {
			t.Fatal(err)
		}
		var before int
		if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		var grant struct {
			Statements []string `json:"statements"`
			Schemas    []string `json:"schemas"`
			Preview    bool     `json:"preview"`
			OK         bool     `json:"ok"`
		}
		body := mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_app/grant?preview=1", `{"level":"write"}`, http.StatusOK)
		if err := json.Unmarshal([]byte(body), &grant); err != nil {
			t.Fatal(err)
		}
		if !grant.Preview || grant.OK || len(grant.Statements) < 5 || len(grant.Schemas) == 0 {
			t.Errorf("grant preview: %s", body)
		}
		var change struct {
			Statements   []string `json:"statements"`
			FutureOwners []string `json:"futureOwners"`
			Preview      bool     `json:"preview"`
		}
		body = mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_app/privileges?preview=1",
			`{"level":"table","schema":"public","privileges":["INSERT"],"future":true}`, http.StatusOK)
		if err := json.Unmarshal([]byte(body), &change); err != nil {
			t.Fatal(err)
		}
		if !change.Preview || len(change.FutureOwners) == 0 || len(change.Statements) != 1+len(change.FutureOwners) {
			t.Errorf("privilege preview: %s", body)
		}
		var can bool
		if err := direct.QueryRow(`SELECT has_table_privilege('jd_b3_api_app', 'public.jd_b3_api_t', 'INSERT')`).Scan(&can); err != nil || can {
			t.Errorf("a preview granted INSERT (%v)", err)
		}
		// The audit log is written after the response; give a stray entry
		// the time it would need to land.
		time.Sleep(200 * time.Millisecond)
		var after int
		if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&after); err != nil || after != before {
			t.Errorf("two previews left %d audit entries (%v)", after-before, err)
		}
	})

	// A role's own settings are where an application's secret ends up. An
	// administrator reads them; a viewer is told the name and not the value.
	t.Run("role_settings_are_an_administrators", func(t *testing.T) {
		for _, stmt := range []string{`ALTER ROLE jd_b3_api_app SET search_path = public`,
			`ALTER ROLE jd_b3_api_app SET jd_b3.jwt_secret = 'jd-b3-signing-key'`} {
			if _, err := direct.Exec(stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		body := mustStatus(t, r, http.MethodGet, base+"/server/roles/jd_b3_api_app", "", http.StatusOK)
		if !strings.Contains(body, "jd_b3.jwt_secret=jd-b3-signing-key") || strings.Contains(body, "configRedacted") {
			t.Errorf("an administrator's view: %s", body)
		}
		viewer := chi.NewRouter()
		viewer.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				p := &httpx.Principal{User: &auth.User{ID: 2, Username: "viewer"}, Role: auth.RoleReadOnly, Kind: "session", IP: "127.0.0.1"}
				next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
			})
		})
		s.mountDatabaseRoutes(viewer)
		body = mustStatus(t, viewer, http.MethodGet, base+"/server/roles/jd_b3_api_app", "", http.StatusOK)
		if strings.Contains(body, "jd-b3-signing-key") || !strings.Contains(body, `"configRedacted":["jd_b3.jwt_secret"]`) ||
			!strings.Contains(body, `"config":["search_path=public"]`) {
			t.Errorf("a viewer's view: %s", body)
		}
	})

	t.Run("statements", func(t *testing.T) {
		body := mustStatus(t, r, http.MethodGet, base+"/statements?sort=calls", "", http.StatusOK)
		if !strings.Contains(body, `"supported":true`) {
			t.Skipf("pg_stat_statements is not enabled: %s", body)
		}
		if !strings.Contains(body, `"resettable":true`) || !strings.Contains(body, `"share":`) {
			t.Errorf("statements: %s", body)
		}
		if body := mustStatus(t, r, http.MethodPost, base+"/statements/reset", `{}`, http.StatusOK); !strings.Contains(body, "pg_stat_statements_reset") {
			t.Errorf("reset: %s", body)
		}
		mustStatus(t, r, http.MethodGet, base+"/statements?sort=longest", "", http.StatusBadRequest)
	})

	t.Run("cancel", func(t *testing.T) {
		if body := mustStatus(t, r, http.MethodPost, base+"/activity/cancel", `{"pid":"2147483000"}`, http.StatusBadRequest); !strings.Contains(body, "no longer running") {
			t.Errorf("cancel of a missing session: %s", body)
		}
		mustStatus(t, r, http.MethodPost, base+"/activity/cancel", `{"pid":"1; SELECT 1"}`, http.StatusBadRequest)
	})

	// Changing the password of the account a connection signs in with would
	// leave the saved connection unable to sign in. It is replaced, once the
	// new password has been seen to work.
	t.Run("own_password", func(t *testing.T) {
		if _, err := direct.Exec(`CREATE ROLE jd_b3_api_self WITH LOGIN PASSWORD 'self-first'`); err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		parsed.User = url.UserPassword("jd_b3_api_self", "self-first")
		sealed, err := s.Sealer.Seal(parsed.String())
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
			"live-self", "postgres", sealed, 0)
		if err != nil {
			t.Fatal(err)
		}
		selfID, _ := res.LastInsertId()
		self := pathf("/databases/%d", selfID)
		if body := mustStatus(t, r, http.MethodGet, self+"/ping", "", http.StatusOK); !strings.Contains(body, `"ok":true`) {
			t.Fatalf("the connection does not open: %s", body)
		}
		body := mustStatus(t, r, http.MethodPut, self+"/server/roles/jd_b3_api_self", `{"password":"self-second"}`, http.StatusOK)
		if !strings.Contains(body, `"connectionUpdated":true`) {
			t.Fatalf("the saved connection was not updated: %s", body)
		}
		if body := mustStatus(t, r, http.MethodGet, self+"/ping", "", http.StatusOK); !strings.Contains(body, `"ok":true`) {
			t.Errorf("the connection no longer opens after its own password change: %s", body)
		}
		var stored string
		if err := s.Store.DB.QueryRow(`SELECT dsn_enc FROM db_connections WHERE id = ?`, selfID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if opened, err := s.Sealer.Open(stored); err != nil || !strings.Contains(opened, "self-second") {
			t.Errorf("the stored connection string was not replaced (%v)", err)
		}
		// What it may not do to itself: stop being able to sign in.
		body = mustStatus(t, r, http.MethodPut, self+"/server/roles/jd_b3_api_self", `{"login":false}`, http.StatusBadRequest)
		if !strings.Contains(body, "the account this connection signs in with") {
			t.Errorf("locking its own account out: %s", body)
		}
		var login bool
		if err := direct.QueryRow(`SELECT rolcanlogin FROM pg_roles WHERE rolname = 'jd_b3_api_self'`).Scan(&login); err != nil || !login {
			t.Errorf("the connection's own account lost its login (%v)", err)
		}
	})

	t.Run("drop", func(t *testing.T) {
		direct.Exec(`DROP OWNED BY jd_b3_api_app`)
		mustStatus(t, r, http.MethodDelete, base+"/server/roles/jd_b3_api_app", "", http.StatusOK)
	})
}

var mysqlPasswordHash = regexp.MustCompile(`\*[0-9A-F]{40}`)

func TestLiveAPIOpsMySQL(t *testing.T) {
	admin := opsLiveDSN(t, "JD_TEST_MYSQL_ADMIN_DSN")
	database := "jdtest"
	if user := os.Getenv("JD_TEST_MYSQL_DSN"); strings.Contains(user, "/") {
		database = user[strings.LastIndex(user, "/")+1:]
	}
	dsn := strings.TrimSuffix(admin, "/") + "/" + database
	_, r, id := liveAPIRouter(t, dbx.DriverMySQL, dsn)
	base := pathf("/databases/%d", id)
	direct, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { direct.Close() })
	cleanup := func() {
		direct.Exec(`DROP TABLE IF EXISTS jd_b3_api_t`)
		direct.Exec(`DROP USER IF EXISTS 'jd_b3_api_u'@'%'`)
		direct.Exec(`DROP USER IF EXISTS 'jd_b3_api_l'@'localhost'`)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := direct.Exec(`CREATE TABLE jd_b3_api_t (id INT PRIMARY KEY, v VARCHAR(20))`); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{
		"/stats":                        `"supported":true`,
		"/locks":                        `"supported":true`,
		"/replication":                  `"role":"standalone"`,
		"/tablestats":                   `"table":"jd_b3_api_t"`,
		"/indexstats?table=jd_b3_api_t": `"name":"PRIMARY"`,
		"/maintenance":                  `"destructive":true`,
		"/settings?all=1":               `"name":"max_connections"`,
		"/server/privileges":            `"level":"database"`,
		"/activity":                     `"status":"`,
	} {
		if body := mustStatus(t, r, http.MethodGet, base+path, "", http.StatusOK); !strings.Contains(body, want) {
			t.Errorf("GET %s lacks %s: %s", path, want, body)
		}
	}
	if body := mustStatus(t, r, http.MethodPost, base+"/maintenance", `{"action":"check","table":"jd_b3_api_t"}`, http.StatusOK); !strings.Contains(body, "status: OK") {
		t.Errorf("check: %s", body)
	}

	mustStatus(t, r, http.MethodPost, base+"/server/roles",
		`{"name":"jd_b3_api_u","host":"%","password":"first-pw","database":"`+database+`","level":"write"}`, http.StatusCreated)
	mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_u/privileges",
		`{"host":"%","level":"table","database":"`+database+`","table":"jd_b3_api_t","privileges":["ALTER"]}`, http.StatusOK)
	detail := mustStatus(t, r, http.MethodGet, base+"/server/roles/jd_b3_api_u?host=%25", "", http.StatusOK)
	// MariaDB prints the password hash in SHOW GRANTS. It must not get here.
	if mysqlPasswordHash.MatchString(detail) || strings.Contains(detail, "IDENTIFIED") {
		t.Errorf("a password hash reached the role detail: %s", detail)
	}
	if !strings.Contains(detail, `"table":"jd_b3_api_t"`) || !strings.Contains(detail, `"database":"`+database+`"`) {
		t.Errorf("the grants are missing from the detail: %s", detail)
	}
	mustStatus(t, r, http.MethodPut, base+"/server/roles/jd_b3_api_u", `{"host":"%","locked":true}`, http.StatusOK)
	mustStatus(t, r, http.MethodPut, base+"/server/roles/jd_b3_api_u", `{"host":"%","password":"second-pw"}`, http.StatusOK)
	if detail := mustStatus(t, r, http.MethodGet, base+"/server/roles/jd_b3_api_u?host=%25", "", http.StatusOK); !strings.Contains(detail, `"locked":true`) {
		t.Errorf("a password change unlocked the account: %s", detail)
	}
	mustStatus(t, r, http.MethodPut, base+"/server/roles/jd_b3_api_u", `{"host":"%","inherit":false}`, http.StatusBadRequest)
	// An account at one host only, asked about by name alone.
	if _, err := direct.Exec(`CREATE USER 'jd_b3_api_l'@'localhost' IDENTIFIED BY 'pw-local-1A'`); err != nil {
		t.Fatal(err)
	}
	if body := mustStatus(t, r, http.MethodGet, base+"/server/grants?role=jd_b3_api_l", "", http.StatusOK); !strings.Contains(body, `"host":"localhost"`) {
		t.Errorf("grants of an account that is not at %%: %s", body)
	}
	mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_u/privileges/revoke",
		`{"host":"%","level":"database","database":"`+database+`","privileges":["ALL"]}`, http.StatusOK)

	t.Cleanup(func() {
		direct.Exec(`SET GLOBAL max_connect_errors = DEFAULT`)
	})
	if body := mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"max_connect_errors","value":"777"}`, http.StatusOK); !strings.Contains(body, `"value":"777"`) {
		t.Errorf("set: %s", body)
	}
	mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"max_connect_errors","value":"many"}`, http.StatusBadRequest)
	mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"max_connect_errors","reset":true}`, http.StatusOK)
}

func TestLiveAPIOpsClickHouse(t *testing.T) {
	dsn := opsLiveDSN(t, "JD_TEST_CLICKHOUSE_DSN")
	_, r, id := liveAPIRouter(t, dbx.DriverClickHouse, dsn)
	base := pathf("/databases/%d", id)
	d, _ := dbx.DialectFor(dbx.DriverClickHouse)
	direct, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { direct.Close() })
	direct.Exec(`DROP TABLE IF EXISTS jd_b3_api_events`)
	t.Cleanup(func() { direct.Exec(`DROP TABLE IF EXISTS jd_b3_api_events`) })
	for _, stmt := range []string{
		`CREATE TABLE jd_b3_api_events (id UInt64, d Date) ENGINE = MergeTree PARTITION BY toYYYYMM(d) ORDER BY id`,
		`INSERT INTO jd_b3_api_events SELECT number, toDate('2026-03-01') FROM numbers(100)`,
		`INSERT INTO jd_b3_api_events SELECT number, toDate('2026-03-02') FROM numbers(100)`,
	} {
		if _, err := direct.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	for path, want := range map[string]string{
		"/stats":      `"activeParts"`,
		"/tablestats": `"table":"jd_b3_api_events"`,
		"/indexstats": `"supported":true`,
		"/clickhouse/parts?table=jd_b3_api_events": `"partition":"202603"`,
		"/clickhouse/merges":                       `"merges":[`,
		"/clickhouse/mutations":                    `"mutations":[`,
		"/clickhouse/queries":                      `"self":true`,
		"/maintenance":                             `"id":"optimize"`,
		"/settings?all=1":                          `"writable":false`,
		"/locks":                                   `"supported":false`,
		"/replication":                             `"supported":false`,
		"/statements":                              `"sort":"total"`,
		"/advisor":                                 `"endOfLife"`,
	} {
		if body := mustStatus(t, r, http.MethodGet, base+path, "", http.StatusOK); !strings.Contains(body, want) {
			t.Errorf("GET %s lacks %s: %s", path, want, body)
		}
	}
	if body := mustStatus(t, r, http.MethodPost, base+"/maintenance", `{"action":"optimize","table":"jd_b3_api_events","options":{"final":true}}`, http.StatusOK); !strings.Contains(body, "OPTIMIZE TABLE `jd_b3_api_events` FINAL") {
		t.Errorf("optimize: %s", body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		body := mustStatus(t, r, http.MethodGet, base+"/clickhouse/parts?table=jd_b3_api_events", "", http.StatusOK)
		if strings.Count(body, `"name":"202603_`) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("optimize final left more than one part: %s", body)
		}
		time.Sleep(200 * time.Millisecond)
	}
	mustStatus(t, r, http.MethodPost, base+"/maintenance", `{"action":"optimize"}`, http.StatusBadRequest)
	mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"max_connections","value":"10"}`, http.StatusBadRequest)
	mustStatus(t, r, http.MethodPost, base+"/activity/cancel", `{"pid":"x"}`, http.StatusBadRequest)
	mustStatus(t, r, http.MethodGet, base+"/sqlite/file", "", http.StatusBadRequest)
}
