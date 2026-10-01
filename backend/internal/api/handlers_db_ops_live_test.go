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

// opsLiveAnyDSN is opsLiveDSN for an engine the environment may name under
// either of two variables: this file's own, which wins, or the shared one.
func opsLiveAnyDSN(t *testing.T, envs ...string) string {
	t.Helper()
	for _, env := range envs {
		if dsn := os.Getenv(env); dsn != "" {
			return dsn
		}
	}
	t.Skipf("none of %s is set", strings.Join(envs, ", "))
	return ""
}

// asRole mounts the database routes for a principal of the given role, with
// the audit middleware in front of them as it is in production. The router
// liveAPIRouter returns is an administrator's and records nothing; what a
// route answers depends on who asks, and what it leaves on the audit trail is
// half of what these tests are about.
func asRole(s *Server, role auth.Role) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 2, Username: "as-" + string(role)}, Role: role, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	s.mountDatabaseRoutes(r)
	return r
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
		for _, role := range []string{"jd_b3_api_app", "jd_b3_api_self", "jd_b3_api_team", "jd_b3_api_made"} {
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

	// A parameter that can hold a credential, through the routes: the value
	// reaches an administrator and not a viewer, and setting it leaves the
	// name on the audit trail and not the value. archive_cleanup_command is
	// the one used; only a standby ever runs it.
	t.Run("a_credential_parameter_is_an_administrators", func(t *testing.T) {
		const secret = "/bin/true jd-b3-archive-token %r"
		const parameter = "archive_cleanup_command"
		t.Cleanup(func() {
			direct.Exec(`ALTER SYSTEM RESET ` + parameter)
			direct.Exec(`SELECT pg_reload_conf()`)
		})
		admin := asRole(s, auth.RoleAdmin)
		body := mustStatus(t, admin, http.MethodPut, base+"/settings", `{"name":"`+parameter+`","value":"`+secret+`"}`, http.StatusOK)
		if !strings.Contains(body, `"persisted":true`) {
			t.Fatalf("set: %s", body)
		}
		setting := func(h http.Handler, name string) dbx.Setting {
			t.Helper()
			var list struct {
				Settings []dbx.Setting `json:"settings"`
			}
			if err := json.Unmarshal([]byte(mustStatus(t, h, http.MethodGet, base+"/settings?all=1", "", http.StatusOK)), &list); err != nil {
				t.Fatal(err)
			}
			for _, s := range list.Settings {
				if s.Name == name {
					return s
				}
			}
			t.Fatalf("%s is not in the list", name)
			return dbx.Setting{}
		}
		// The reload is a signal; the running value follows a moment later.
		deadline := time.Now().Add(10 * time.Second)
		for setting(r, parameter).Value != secret {
			if time.Now().After(deadline) {
				t.Fatalf("the administrator never saw the new value: %+v", setting(r, parameter))
			}
			time.Sleep(100 * time.Millisecond)
		}
		viewer := asRole(s, auth.RoleReadOnly)
		if got := setting(viewer, parameter); got.Value != "" || got.Default != "" || !got.Redacted {
			t.Errorf("a viewer was shown %+v", got)
		}
		if body := mustStatus(t, viewer, http.MethodGet, base+"/settings?all=1", "", http.StatusOK); strings.Contains(body, "jd-b3-archive-token") {
			t.Error("the value reached a viewer somewhere in the list")
		}
		// What is policy and no secret is shown to both.
		if got := setting(viewer, "password_encryption"); got.Value == "" || got.Redacted {
			t.Errorf("password_encryption was withheld from a viewer: %+v", got)
		}
		if got := setting(r, parameter); got.Redacted {
			t.Errorf("an administrator's value is marked withheld: %+v", got)
		}
		_, detail, status := auditEntry(t, s, "database.setting.set")
		if status != http.StatusOK || !strings.Contains(detail, `"name":"`+parameter+`"`) || strings.Contains(detail, "jd-b3-archive-token") ||
			strings.Contains(detail, `"value"`) || strings.Contains(detail, `"statements"`) {
			t.Errorf("audit detail: %d %s", status, detail)
		}
		// Putting it back names it and, again, nothing else.
		mustStatus(t, admin, http.MethodPut, base+"/settings", `{"name":"`+parameter+`","reset":true}`, http.StatusOK)
		if _, detail, _ := auditEntry(t, s, "database.setting.reset"); !strings.Contains(detail, `"name":"`+parameter+`"`) || strings.Contains(detail, `"statements"`) {
			t.Errorf("reset audit detail: %s", detail)
		}
		// An ordinary parameter's value and statement are recorded.
		mustStatus(t, admin, http.MethodPut, base+"/settings", `{"name":"deadlock_timeout","value":"1100ms"}`, http.StatusOK)
		if _, detail, _ := auditEntry(t, s, "database.setting.set"); !strings.Contains(detail, `"name":"deadlock_timeout"`) ||
			!strings.Contains(detail, `"value":"1100ms"`) || !strings.Contains(detail, "ALTER SYSTEM SET") {
			t.Errorf("an ordinary change: %s", detail)
		}
		mustStatus(t, admin, http.MethodPut, base+"/settings", `{"name":"deadlock_timeout","reset":true}`, http.StatusOK)
		// The value the list prints for a file mode is one the form accepts.
		mode := setting(r, "log_file_mode")
		t.Cleanup(func() { direct.Exec(`ALTER SYSTEM RESET log_file_mode`) })
		if body := mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"log_file_mode","value":"`+mode.Value+`"}`, http.StatusOK); !strings.Contains(body, `'`+mode.Value+`'`) {
			t.Errorf("log_file_mode = %s: %s", mode.Value, body)
		}
		mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"log_file_mode","value":"0640"}`, http.StatusOK)
		mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"log_file_mode","value":"01000"}`, http.StatusBadRequest)
		mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"log_file_mode","reset":true}`, http.StatusOK)
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

		// A create carries the attributes only this engine has, or is refused
		// for the one it has not: a role is made as asked or not made.
		if body := mustStatus(t, r, http.MethodPost, base+"/server/roles", `{"name":"jd_b3_api_made","password":"made-pw","locked":true}`, http.StatusBadRequest); !strings.Contains(body, "taking away its login") {
			t.Errorf("a locked PostgreSQL role: %s", body)
		}
		mustStatus(t, r, http.MethodGet, base+"/server/roles/jd_b3_api_made", "", http.StatusNotFound)
		mustStatus(t, r, http.MethodPost, base+"/server/roles",
			`{"name":"jd_b3_api_made","password":"made-pw","login":false,"inherit":false,"replication":true,"bypassRls":false,"validUntil":"2032-03-04","connectionLimit":2}`, http.StatusCreated)
		var made dbx.RoleDetail
		if err := json.Unmarshal([]byte(mustStatus(t, r, http.MethodGet, base+"/server/roles/jd_b3_api_made", "", http.StatusOK)), &made); err != nil {
			t.Fatal(err)
		}
		if made.Login || made.Attributes["inherit"] || !made.Attributes["replication"] || made.Attributes["bypassRls"] ||
			!strings.HasPrefix(made.ValidUntil, "2032-03-04") || made.ConnLimit != 2 || made.Superuser {
			t.Errorf("made as %+v with %v", made.Role, made.Attributes)
		}
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
		// Through a router that records, so that "no entry" is something the
		// handler did rather than something the test router lacks.
		admin := asRole(s, auth.RoleAdmin)
		body := mustStatus(t, admin, http.MethodPost, base+"/server/roles/jd_b3_api_app/grant?preview=1", `{"level":"write"}`, http.StatusOK)
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
		body = mustStatus(t, admin, http.MethodPost, base+"/server/roles/jd_b3_api_app/privileges?preview=1",
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
		// The same request without the preview is recorded, by the same router.
		mustStatus(t, admin, http.MethodPost, base+"/server/roles/jd_b3_api_app/privileges",
			`{"level":"table","schema":"public","table":"jd_b3_api_t","privileges":["INSERT"]}`, http.StatusOK)
		if _, detail, _ := auditEntry(t, s, "database.role.grant"); !strings.Contains(detail, `"role":"jd_b3_api_app"`) || !strings.Contains(detail, "GRANT INSERT") {
			t.Errorf("a grant that ran: %s", detail)
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

// The MariaDB the shared variables name, and a MySQL 8 behind a variable of
// this file's own: the two differ in where locks and statements are kept and
// in whether a parameter change can be persisted, and the routes have to
// answer on both.
func TestLiveAPIOpsMySQL(t *testing.T) {
	servers := map[string]string{}
	if admin := os.Getenv("JD_TEST_MYSQL_ADMIN_DSN"); admin != "" {
		database := "jdtest"
		if user := os.Getenv("JD_TEST_MYSQL_DSN"); strings.Contains(user, "/") {
			database = user[strings.LastIndex(user, "/")+1:]
		}
		servers["mariadb"] = strings.TrimSuffix(admin, "/") + "/" + database
	}
	if admin := os.Getenv("JD_TEST_B3_MYSQL8_ADMIN_DSN"); admin != "" {
		servers["mysql8"] = admin
	}
	if len(servers) == 0 {
		t.Skip("neither JD_TEST_MYSQL_ADMIN_DSN nor JD_TEST_B3_MYSQL8_ADMIN_DSN is set")
	}
	for flavour, dsn := range servers {
		t.Run(flavour, func(t *testing.T) { liveAPIOpsMySQL(t, flavour, dsn) })
	}
}

func liveAPIOpsMySQL(t *testing.T, flavour, dsn string) {
	database := dsn[strings.LastIndex(dsn, "/")+1:]
	s, r, id := liveAPIRouter(t, dbx.DriverMySQL, dsn)
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
		direct.Exec(`DROP USER IF EXISTS 'jd_b3_api_m'@'%'`)
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
	// A create carries what this engine's accounts can be given, and is
	// refused whole for what they cannot: an expiry is PostgreSQL's.
	mustStatus(t, r, http.MethodPost, base+"/server/roles",
		`{"name":"jd_b3_api_m","host":"%","password":"made-pw-1A","validUntil":"2032-01-01"}`, http.StatusBadRequest)
	mustStatus(t, r, http.MethodGet, base+"/server/roles/jd_b3_api_m?host=%25", "", http.StatusNotFound)
	mustStatus(t, r, http.MethodPost, base+"/server/roles",
		`{"name":"jd_b3_api_m","host":"%","password":"made-pw-1A","locked":true,"createRole":true,"connectionLimit":4}`, http.StatusCreated)
	var made dbx.RoleDetail
	if err := json.Unmarshal([]byte(mustStatus(t, r, http.MethodGet, base+"/server/roles/jd_b3_api_m?host=%25", "", http.StatusOK)), &made); err != nil {
		t.Fatal(err)
	}
	if !made.Locked || made.Login || !made.CreateRole || made.Superuser || made.ConnLimit != 4 {
		t.Errorf("made as %+v", made.Role)
	}
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
		direct.Exec(`RESET PERSIST IF EXISTS max_connect_errors`)
		direct.Exec(`SET GLOBAL max_connect_errors = DEFAULT`)
	})
	set := mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"max_connect_errors","value":"777"}`, http.StatusOK)
	if !strings.Contains(set, `"value":"777"`) {
		t.Errorf("set: %s", set)
	}
	// MySQL writes the change to mysqld-auto.cnf; MariaDB has nowhere to, and
	// says the change lasts until the restart.
	if flavour == "mysql8" {
		var persisted string
		if err := direct.QueryRow(`SELECT VARIABLE_VALUE FROM performance_schema.persisted_variables WHERE VARIABLE_NAME = 'max_connect_errors'`).Scan(&persisted); err != nil || persisted != "777" ||
			!strings.Contains(set, `"persisted":true`) || !strings.Contains(set, "SET PERSIST max_connect_errors") {
			t.Errorf("persisted as %q (%v): %s", persisted, err, set)
		}
	} else if !strings.Contains(set, `"persisted":false`) || !strings.Contains(set, "until the server restarts") {
		t.Errorf("MariaDB's change: %s", set)
	}
	mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"max_connect_errors","value":"many"}`, http.StatusBadRequest)
	mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"max_connect_errors","reset":true}`, http.StatusOK)

	// Statement statistics: MySQL keeps them and can zero them; a MariaDB
	// started with performance_schema off keeps none and says what to do.
	statements := mustStatus(t, r, http.MethodGet, base+"/statements?sort=calls&limit=5", "", http.StatusOK)
	if flavour == "mysql8" {
		if !strings.Contains(statements, `"supported":true`) || !strings.Contains(statements, `"resettable":true`) {
			t.Errorf("statements: %s", statements)
		}
		if body := mustStatus(t, r, http.MethodPost, base+"/statements/reset", `{}`, http.StatusOK); !strings.Contains(body, "TRUNCATE TABLE performance_schema") {
			t.Errorf("reset: %s", body)
		}
	} else if !strings.Contains(statements, `"supported":false`) || !strings.Contains(statements, `"enable"`) {
		t.Errorf("statements with performance_schema off: %s", statements)
	}

	// The capability follows the action, through the real route: a limited
	// account may check a table and may not rebuild it, and an account that
	// only reads may do neither.
	limited, viewer := asRole(s, auth.RoleLimited), asRole(s, auth.RoleReadOnly)
	if body := mustStatus(t, limited, http.MethodPost, base+"/maintenance", `{"action":"analyze","table":"jd_b3_api_t"}`, http.StatusOK); !strings.Contains(body, "ANALYZE TABLE") {
		t.Errorf("a limited account's analyze: %s", body)
	}
	if _, detail, status := auditEntry(t, s, "database.maintenance"); status != http.StatusOK || !strings.Contains(detail, `"action":"analyze"`) || !strings.Contains(detail, "ANALYZE TABLE") {
		t.Errorf("audit: %d %s", status, detail)
	}
	mustStatus(t, limited, http.MethodPost, base+"/maintenance", `{"action":"optimize","table":"jd_b3_api_t"}`, http.StatusForbidden)
	mustStatus(t, viewer, http.MethodPost, base+"/maintenance", `{"action":"analyze","table":"jd_b3_api_t"}`, http.StatusForbidden)
	mustStatus(t, limited, http.MethodPut, base+"/settings", `{"name":"max_connect_errors","value":"5"}`, http.StatusForbidden)
	if body := mustStatus(t, r, http.MethodPost, base+"/maintenance", `{"action":"optimize","table":"jd_b3_api_t"}`, http.StatusOK); !strings.Contains(body, "OPTIMIZE TABLE") {
		t.Errorf("an administrator's optimize: %s", body)
	}
	if body := mustStatus(t, viewer, http.MethodGet, base+"/advisor", "", http.StatusOK); !strings.Contains(body, `"engineChecks":true`) {
		t.Errorf("advisor: %s", body)
	}
	mustStatus(t, r, http.MethodPost, base+"/activity/cancel", `{"pid":"not-a-session"}`, http.StatusBadRequest)
}

// --- SQL Server ----------------------------------------------------------------

// The SQL Server fixture is shared by every stream: this works in a database
// of its own, under logins carrying this stream's prefix, and puts back the
// one server option it changes.
func TestLiveAPIOpsSQLServer(t *testing.T) {
	server := opsLiveAnyDSN(t, "JD_TEST_B3_MSSQL_DSN", "JD_TEST_MSSQL_DSN")
	bootstrap, err := sql.Open("sqlserver", server)
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Close()
	if _, err := bootstrap.Exec(`IF DB_ID('jd_b3') IS NULL CREATE DATABASE jd_b3`); err != nil {
		t.Skipf("sqlserver unreachable: %v", err)
	}
	dsn := dbx.DSNForDatabase(dbx.DriverMSSQL, server, "jd_b3")
	s, r, id := liveAPIRouter(t, dbx.DriverMSSQL, dsn)
	base := pathf("/databases/%d", id)
	direct, err := sql.Open("sqlserver", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { direct.Close() })
	cleanup := func() {
		direct.Exec(`IF OBJECT_ID('dbo.jd_b3_api_t') IS NOT NULL DROP TABLE dbo.jd_b3_api_t`)
		direct.Exec(`IF USER_ID('jd_b3_api_login') IS NOT NULL DROP USER [jd_b3_api_login]`)
		direct.Exec(`IF SUSER_ID('jd_b3_api_login') IS NOT NULL DROP LOGIN [jd_b3_api_login]`)
	}
	cleanup()
	t.Cleanup(cleanup)
	for _, stmt := range []string{
		`CREATE TABLE dbo.jd_b3_api_t (id INT PRIMARY KEY, v NVARCHAR(40), n INT)`,
		`CREATE INDEX jd_b3_api_t_n ON dbo.jd_b3_api_t (n)`,
		`INSERT INTO dbo.jd_b3_api_t VALUES (1, 'a', 1), (2, 'b', 2)`,
	} {
		if _, err := direct.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	limited, viewer, admin := asRole(s, auth.RoleLimited), asRole(s, auth.RoleReadOnly), asRole(s, auth.RoleAdmin)

	t.Run("reads", func(t *testing.T) {
		// Every one of them answers an account that only reads.
		for path, want := range map[string]string{
			"/stats":                        `"supported":true`,
			"/activity":                     `"status":"`,
			"/locks":                        `"supported":true`,
			"/replication":                  `"supported":false`,
			"/tablestats?schema=dbo":        `"table":"jd_b3_api_t"`,
			"/indexstats?table=jd_b3_api_t": `"name":"jd_b3_api_t_n"`,
			"/maintenance":                  `"id":"rebuild"`,
			"/settings":                     `"name":"max degree of parallelism"`,
			"/settings?all=1":               `"writable":true`,
			"/server/privileges":            `"level":"schema"`,
			"/server/roles":                 `"name":"sa"`,
			"/server/grants":                `"supported":true`,
			"/statements?sort=calls":        `"sort":"calls"`,
			"/advisor":                      `"engineChecks":true`,
		} {
			if body := mustStatus(t, viewer, http.MethodGet, base+path, "", http.StatusOK); !strings.Contains(body, want) {
				t.Errorf("GET %s lacks %s: %s", path, want, body)
			}
		}
		var list struct {
			Actions []dbx.MaintenanceAction `json:"actions"`
		}
		if err := json.Unmarshal([]byte(mustStatus(t, viewer, http.MethodGet, base+"/maintenance", "", http.StatusOK)), &list); err != nil {
			t.Fatal(err)
		}
		requires := map[string]string{}
		for _, a := range list.Actions {
			requires[a.ID] = a.Requires
		}
		if len(requires) != 4 || requires["rebuild"] != "destructive" || requires["reorganize"] != "service.control" ||
			requires["update_statistics"] != "service.control" || requires["check"] != "service.control" {
			t.Errorf("actions: %v", requires)
		}
		mustStatus(t, viewer, http.MethodGet, base+"/statements?sort=longest", "", http.StatusBadRequest)
		mustStatus(t, viewer, http.MethodGet, base+"/clickhouse/parts", "", http.StatusBadRequest)
	})

	t.Run("statements_carry_no_literals", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			if _, err := direct.Exec(`SELECT COUNT(*) FROM dbo.jd_b3_api_t t JOIN dbo.jd_b3_api_t u ON u.id = t.id WHERE t.v = 'jd-b3-api-literal' AND u.n = 515151`); err != nil {
				t.Fatal(err)
			}
		}
		body := mustStatus(t, viewer, http.MethodGet, base+"/statements?sort=calls&limit=200", "", http.StatusOK)
		if !strings.Contains(body, `"supported":true`) || !strings.Contains(body, `"resettable":false`) {
			t.Fatalf("statements: %s", body)
		}
		if strings.Contains(body, "jd-b3-api-literal") || strings.Contains(body, "515151") {
			t.Error("a literal reached a viewer")
		}
		if !strings.Contains(body, `WHERE t.v = ? AND u.n = ?`) {
			t.Errorf("the probe is not in the list by its shape: %s", body)
		}
		mustStatus(t, r, http.MethodPost, base+"/statements/reset", `{}`, http.StatusBadRequest)
	})

	t.Run("maintenance", func(t *testing.T) {
		body := mustStatus(t, limited, http.MethodPost, base+"/maintenance", `{"action":"update_statistics","table":"jd_b3_api_t"}`, http.StatusOK)
		if !strings.Contains(body, "UPDATE STATISTICS [dbo].[jd_b3_api_t]") || !strings.Contains(body, "2 rows, 2 sampled") {
			t.Errorf("update statistics: %s", body)
		}
		if body := mustStatus(t, limited, http.MethodPost, base+"/maintenance", `{"action":"reorganize","table":"jd_b3_api_t"}`, http.StatusOK); !strings.Contains(body, "REORGANIZE") {
			t.Errorf("reorganize: %s", body)
		}
		body = mustStatus(t, limited, http.MethodPost, base+"/maintenance", `{"action":"check","table":"jd_b3_api_t"}`, http.StatusOK)
		if !strings.Contains(body, "DBCC CHECKTABLE") || !strings.Contains(body, `"ok":true`) || !strings.Contains(body, "2 rows") {
			t.Errorf("check: %s", body)
		}
		// A rebuild locks the table: the destructive capability, by content.
		if body := mustStatus(t, limited, http.MethodPost, base+"/maintenance", `{"action":"rebuild","table":"jd_b3_api_t"}`, http.StatusForbidden); !strings.Contains(body, "your role does not permit it") {
			t.Errorf("a limited account's rebuild: %s", body)
		}
		mustStatus(t, viewer, http.MethodPost, base+"/maintenance", `{"action":"check"}`, http.StatusForbidden)
		body = mustStatus(t, admin, http.MethodPost, base+"/maintenance", `{"action":"rebuild","table":"jd_b3_api_t","index":"jd_b3_api_t_n","options":{"online":true}}`, http.StatusOK)
		if !strings.Contains(body, "ALTER INDEX [jd_b3_api_t_n] ON [dbo].[jd_b3_api_t] REBUILD WITH (ONLINE = ON)") {
			t.Errorf("rebuild: %s", body)
		}
		_, detail, status := auditEntry(t, s, "database.maintenance")
		if status != http.StatusOK || !strings.Contains(detail, `"action":"rebuild"`) || !strings.Contains(detail, `"index":"jd_b3_api_t_n"`) ||
			!strings.Contains(detail, "REBUILD WITH (ONLINE = ON)") || !strings.Contains(detail, `"ok":true`) {
			t.Errorf("audit: %d %s", status, detail)
		}
		// The engine's refusal is recorded with what was asked for.
		if body := mustStatus(t, admin, http.MethodPost, base+"/maintenance", `{"action":"rebuild","table":"jd_b3_no_such_table"}`, http.StatusBadRequest); !strings.Contains(body, "jd_b3_no_such_table") {
			t.Errorf("a missing table: %s", body)
		}
		if _, detail, status := auditEntry(t, s, "database.maintenance"); status != http.StatusBadRequest || !strings.Contains(detail, `"error"`) {
			t.Errorf("audit of a refused rebuild: %d %s", status, detail)
		}
		mustStatus(t, r, http.MethodPost, base+"/maintenance", `{"action":"rebuild"}`, http.StatusBadRequest)
		mustStatus(t, r, http.MethodPost, base+"/maintenance", `{"action":"vacuum"}`, http.StatusBadRequest)
	})

	t.Run("settings", func(t *testing.T) {
		const option = "cost threshold for parallelism"
		var before, shown string
		if err := direct.QueryRow(`SELECT CAST(value AS NVARCHAR(64)) FROM sys.configurations WHERE name = @p1`, option).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if err := direct.QueryRow(`SELECT CAST(value_in_use AS NVARCHAR(64)) FROM sys.configurations WHERE name = 'show advanced options'`).Scan(&shown); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			direct.Exec(`EXEC sys.sp_configure N'show advanced options', 1`)
			direct.Exec(`RECONFIGURE`)
			direct.Exec(`EXEC sys.sp_configure N'` + option + `', ` + before)
			direct.Exec(`EXEC sys.sp_configure N'show advanced options', ` + shown)
			direct.Exec(`RECONFIGURE`)
		})
		mustStatus(t, limited, http.MethodPut, base+"/settings", `{"name":"`+option+`","value":"9"}`, http.StatusForbidden)
		body := mustStatus(t, admin, http.MethodPut, base+"/settings", `{"name":"`+option+`","value":"9"}`, http.StatusOK)
		if !strings.Contains(body, `"value":"9"`) || !strings.Contains(body, `"persisted":true`) || !strings.Contains(body, `"restartRequired":false`) ||
			!strings.Contains(body, `EXEC sys.sp_configure N'cost threshold for parallelism', 9`) || !strings.Contains(body, `RECONFIGURE`) {
			t.Errorf("set: %s", body)
		}
		var running string
		if err := direct.QueryRow(`SELECT CAST(value_in_use AS NVARCHAR(64)) FROM sys.configurations WHERE name = @p1`, option).Scan(&running); err != nil || running != "9" {
			t.Errorf("the server runs with %q (%v)", running, err)
		}
		_, detail, _ := auditEntry(t, s, "database.setting.set")
		if !strings.Contains(detail, `"name":"cost threshold for parallelism"`) || !strings.Contains(detail, `"value":"9"`) || !strings.Contains(detail, "sp_configure") {
			t.Errorf("audit: %s", detail)
		}
		if body := mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"`+option+`","value":"99999"}`, http.StatusBadRequest); !strings.Contains(body, "at most 32767") {
			t.Errorf("out of range: %s", body)
		}
		if body := mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"`+option+`","reset":true}`, http.StatusBadRequest); !strings.Contains(body, "does not record the default") {
			t.Errorf("reset: %s", body)
		}
		mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"`+option+`","value":"`+before+`"}`, http.StatusOK)
		var list struct {
			Settings []dbx.Setting `json:"settings"`
		}
		if err := json.Unmarshal([]byte(mustStatus(t, viewer, http.MethodGet, base+"/settings?all=1", "", http.StatusOK)), &list); err != nil {
			t.Fatal(err)
		}
		for _, setting := range list.Settings {
			if setting.Name == option && (!setting.Editable || setting.Value != before || setting.Max != "32767") {
				t.Errorf("the option afterwards: %+v", setting)
			}
		}
	})

	t.Run("roles", func(t *testing.T) {
		mustStatus(t, r, http.MethodPost, base+"/server/roles", `{"name":"jd_b3_api_login","password":"Jd-b3-api#2026","connectionLimit":3}`, http.StatusBadRequest)
		mustStatus(t, r, http.MethodPost, base+"/server/roles", `{"name":"jd_b3_api_login","password":"Jd-b3-api#2026","createDb":true,"locked":true}`, http.StatusCreated)
		body := mustStatus(t, viewer, http.MethodGet, base+"/server/roles/jd_b3_api_login", "", http.StatusOK)
		if !strings.Contains(body, `"locked":true`) || !strings.Contains(body, `"createDb":true`) || !strings.Contains(body, `"login":false`) {
			t.Errorf("created as: %s", body)
		}
		mustStatus(t, r, http.MethodPut, base+"/server/roles/jd_b3_api_login", `{"password":"Jd-b3-api#2027"}`, http.StatusOK)
		if body := mustStatus(t, viewer, http.MethodGet, base+"/server/roles/jd_b3_api_login", "", http.StatusOK); !strings.Contains(body, `"locked":true`) || !strings.Contains(body, `"createDb":true`) {
			t.Errorf("a password change altered the login: %s", body)
		}
		mustStatus(t, r, http.MethodPut, base+"/server/roles/jd_b3_api_login", `{"inherit":false}`, http.StatusBadRequest)
		// The connection's own login may not be disabled from its connection.
		if body := mustStatus(t, r, http.MethodPut, base+"/server/roles/sa", `{"locked":true}`, http.StatusBadRequest); !strings.Contains(body, "the account this connection signs in with") {
			t.Errorf("disabling sa: %s", body)
		}
		grant := `{"level":"table","schema":"dbo","table":"jd_b3_api_t","privileges":["SELECT"]}`
		if body := mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_login/privileges?preview=1", grant, http.StatusOK); !strings.Contains(body, "GRANT SELECT ON OBJECT::[dbo].[jd_b3_api_t] TO [jd_b3_api_login]") {
			t.Errorf("preview: %s", body)
		}
		mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_login/privileges", grant, http.StatusOK)
		if body := mustStatus(t, viewer, http.MethodGet, base+"/server/grants?role=jd_b3_api_login", "", http.StatusOK); !strings.Contains(body, `"table":"jd_b3_api_t"`) || !strings.Contains(body, `"privileges":["SELECT"]`) {
			t.Errorf("grants: %s", body)
		}
		mustStatus(t, r, http.MethodPost, base+"/server/roles/jd_b3_api_login/privileges/revoke", grant, http.StatusOK)
		direct.Exec(`DROP USER [jd_b3_api_login]`)
		mustStatus(t, r, http.MethodDelete, base+"/server/roles/jd_b3_api_login", "", http.StatusOK)
	})

	mustStatus(t, r, http.MethodPost, base+"/activity/cancel", `{"pid":"52"}`, http.StatusBadRequest)
}

// --- Oracle ------------------------------------------------------------------

// The Oracle fixture is shared by every stream: everything made here carries
// this stream's prefix, and the one parameter changed is reset. The
// administrative account may read the V$ views; the ordinary one may not, and
// the routes have to say so rather than fail.
func TestLiveAPIOpsOracle(t *testing.T) {
	dsn := opsLiveAnyDSN(t, "JD_TEST_B3_ORACLE_ADMIN_DSN", "JD_TEST_ORACLE_ADMIN_DSN")
	s, r, id := liveAPIRouter(t, dbx.DriverOracle, dsn)
	base := pathf("/databases/%d", id)
	direct, err := sql.Open("oracle", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { direct.Close() })
	direct.Exec(`DROP TABLE jd_b3_api_t PURGE`)
	t.Cleanup(func() {
		direct.Exec(`DROP TABLE jd_b3_api_t PURGE`)
		direct.Exec(`ALTER SYSTEM RESET undo_retention SCOPE=BOTH`)
	})
	for _, stmt := range []string{
		`CREATE TABLE jd_b3_api_t (id NUMBER PRIMARY KEY, v VARCHAR2(40), n NUMBER)`,
		`CREATE INDEX jd_b3_api_t_n ON jd_b3_api_t (n)`,
		`INSERT INTO jd_b3_api_t VALUES (1, 'a', 1)`,
		`INSERT INTO jd_b3_api_t VALUES (2, 'b', 2)`,
	} {
		if _, err := direct.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	limited, viewer, admin := asRole(s, auth.RoleLimited), asRole(s, auth.RoleReadOnly), asRole(s, auth.RoleAdmin)

	t.Run("maintenance", func(t *testing.T) {
		body := mustStatus(t, viewer, http.MethodGet, base+"/maintenance", "", http.StatusOK)
		if !strings.Contains(body, `"id":"gather_stats"`) || !strings.Contains(body, `"requires":"service.control"`) || !strings.Contains(body, `"supported":true`) {
			t.Errorf("actions: %s", body)
		}
		mustStatus(t, viewer, http.MethodPost, base+"/maintenance", `{"action":"gather_stats","table":"JD_B3_API_T"}`, http.StatusForbidden)
		body = mustStatus(t, limited, http.MethodPost, base+"/maintenance", `{"action":"gather_stats","table":"JD_B3_API_T"}`, http.StatusOK)
		if !strings.Contains(body, `DBMS_STATS.GATHER_TABLE_STATS`) || !strings.Contains(body, `tabname =\u003e '\"JD_B3_API_T\"'`) ||
			!strings.Contains(body, "JD_B3_API_T: 2 rows, ") || !strings.Contains(body, `"ok":true`) {
			t.Errorf("gather: %s", body)
		}
		_, detail, status := auditEntry(t, s, "database.maintenance")
		if status != http.StatusOK || !strings.Contains(detail, `"action":"gather_stats"`) || !strings.Contains(detail, "DBMS_STATS") {
			t.Errorf("audit: %d %s", status, detail)
		}
		if body := mustStatus(t, limited, http.MethodPost, base+"/maintenance", `{"action":"gather_stats","table":"JD_B3_NO_SUCH_TABLE"}`, http.StatusBadRequest); !strings.Contains(body, "ORA-") {
			t.Errorf("a missing table: %s", body)
		}
		mustStatus(t, limited, http.MethodPost, base+"/maintenance", `{"action":"vacuum"}`, http.StatusBadRequest)
	})

	t.Run("reads", func(t *testing.T) {
		for path, want := range map[string]string{
			"/stats":                        `"supported":true`,
			"/activity":                     `"status":"`,
			"/locks":                        `"supported":true`,
			"/replication":                  `"supported":false`,
			"/tablestats":                   `"table":"JD_B3_API_T"`,
			"/indexstats?table=JD_B3_API_T": `"name":"JD_B3_API_T_N"`,
			"/settings":                     `"name":"open_cursors"`,
			"/settings?all=1":               `"writable":true`,
			"/server/privileges":            `"supported":false`,
			"/server/grants":                `"supported":false`,
			"/statements?sort=calls":        `"supported":true`,
			"/advisor":                      `"engineChecks":true`,
		} {
			if body := mustStatus(t, viewer, http.MethodGet, base+path, "", http.StatusOK); !strings.Contains(body, want) {
				t.Errorf("GET %s lacks %s: %s", path, want, body)
			}
		}
		// Oracle keeps no longest execution; that order is the request's mistake.
		if body := mustStatus(t, viewer, http.MethodGet, base+"/statements?sort=max", "", http.StatusBadRequest); !strings.Contains(body, "no longest execution") {
			t.Errorf("sort=max: %s", body)
		}
		// Its accounts are not managed from here, and the refusal says so.
		mustStatus(t, viewer, http.MethodGet, base+"/server/roles", "", http.StatusBadRequest)
		mustStatus(t, r, http.MethodPost, base+"/statements/reset", `{}`, http.StatusBadRequest)
	})

	t.Run("statements_carry_no_literals", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			if _, err := direct.Exec(`SELECT COUNT(*) FROM jd_b3_api_t WHERE n = 515151 AND 'jd-b3-api-literal' = v`); err != nil {
				t.Fatal(err)
			}
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			body := mustStatus(t, viewer, http.MethodGet, base+"/statements?sort=calls&limit=200", "", http.StatusOK)
			if strings.Contains(body, "jd-b3-api-literal") || strings.Contains(body, "515151") {
				t.Fatal("a literal reached a viewer")
			}
			if strings.Contains(body, `FROM jd_b3_api_t WHERE n = ? AND ? = v`) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the probe never appeared by its shape: %s", body)
			}
			time.Sleep(500 * time.Millisecond)
		}
	})

	t.Run("settings", func(t *testing.T) {
		mustStatus(t, limited, http.MethodPut, base+"/settings", `{"name":"undo_retention","value":"902"}`, http.StatusForbidden)
		body := mustStatus(t, admin, http.MethodPut, base+"/settings", `{"name":"undo_retention","value":"902"}`, http.StatusOK)
		if !strings.Contains(body, `"value":"902"`) || !strings.Contains(body, `"persisted":true`) ||
			!strings.Contains(body, `ALTER SYSTEM SET undo_retention = 902 SCOPE=BOTH`) {
			t.Errorf("set: %s", body)
		}
		_, detail, _ := auditEntry(t, s, "database.setting.set")
		if !strings.Contains(detail, `"name":"undo_retention"`) || !strings.Contains(detail, `"value":"902"`) || !strings.Contains(detail, "ALTER SYSTEM SET") {
			t.Errorf("audit: %s", detail)
		}
		var list struct {
			Settings []dbx.Setting `json:"settings"`
		}
		if err := json.Unmarshal([]byte(mustStatus(t, viewer, http.MethodGet, base+"/settings?all=1", "", http.StatusOK)), &list); err != nil {
			t.Fatal(err)
		}
		seen := map[string]dbx.Setting{}
		for _, setting := range list.Settings {
			seen[setting.Name] = setting
		}
		if got := seen["undo_retention"]; got.Value != "902" || !got.Editable || !got.Changed || got.Context != "immediate" {
			t.Errorf("undo_retention afterwards: %+v", got)
		}
		if got := seen["db_block_size"]; got.Editable || !got.RestartRequired {
			t.Errorf("db_block_size: %+v", got)
		}
		if body := mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"db_block_size","value":"16384"}`, http.StatusBadRequest); !strings.Contains(body, "cannot be changed from here") {
			t.Errorf("a parameter read only at start: %s", body)
		}
		mustStatus(t, r, http.MethodPut, base+"/settings", `{"name":"undo_retention","value":"900; SHUTDOWN"}`, http.StatusBadRequest)
		if body := mustStatus(t, admin, http.MethodPut, base+"/settings", `{"name":"undo_retention","reset":true}`, http.StatusOK); !strings.Contains(body, "ALTER SYSTEM RESET undo_retention SCOPE=BOTH") {
			t.Errorf("reset: %s", body)
		}
		if _, detail, _ := auditEntry(t, s, "database.setting.reset"); !strings.Contains(detail, `"name":"undo_retention"`) {
			t.Errorf("reset audit: %s", detail)
		}
	})

	// An application schema: what it may not read is a sentence, on a 200.
	t.Run("ordinary_account", func(t *testing.T) {
		plainDSN := os.Getenv("JD_TEST_B3_ORACLE_DSN")
		if plainDSN == "" {
			plainDSN = os.Getenv("JD_TEST_ORACLE_DSN")
		}
		if plainDSN == "" {
			t.Skip("neither JD_TEST_B3_ORACLE_DSN nor JD_TEST_ORACLE_DSN is set")
		}
		sealed, err := s.Sealer.Seal(plainDSN)
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
			"live-oracle-plain", "oracle", sealed, 0)
		if err != nil {
			t.Fatal(err)
		}
		plainID, _ := res.LastInsertId()
		plain := pathf("/databases/%d", plainID)
		for path, wants := range map[string][]string{
			"/activity":   {`"supported":false`, "v$session"},
			"/locks":      {`"supported":false`, "v$session"},
			"/statements": {`"supported":false`, "v$sqlstats"},
			"/stats":      {`"supported":true`, `"notes":[`},
			"/tablestats": {`"supported":true`},
			"/indexstats": {`"supported":true`, "DBA_INDEX_USAGE"},
		} {
			body := mustStatus(t, viewer, http.MethodGet, plain+path, "", http.StatusOK)
			for _, want := range wants {
				if !strings.Contains(body, want) {
					t.Errorf("GET %s lacks %s: %s", path, want, body)
				}
			}
		}
		if body := mustStatus(t, r, http.MethodPut, plain+"/settings", `{"name":"undo_retention","value":"903"}`, http.StatusBadRequest); !strings.Contains(body, "cannot be changed from here") {
			t.Errorf("a parameter change without ALTER SYSTEM: %s", body)
		}
	})
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
