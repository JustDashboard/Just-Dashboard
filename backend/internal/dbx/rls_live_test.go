package dbx

import (
	"database/sql"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// Row-level security and the grants beside it, against a real PostgreSQL. The
// roles, the schema and everything in it are this file's own (jdt_rls_editor_)
// and are dropped afterwards.

const (
	rlsSchema  = "jdt_rls_editor_sch"
	rlsAlice   = "jdt_rls_editor_alice"
	rlsBob     = "jdt_rls_editor_bob"
	rlsLimited = "jdt_rls_editor_limited"
)

func rlsLive(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, dsn := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	var super bool
	if err := db.QueryRow(`SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil || !super {
		t.Skipf("this login is not a superuser (%v): it cannot make roles to become", err)
	}
	cleanup := func() {
		db.Exec(`DROP SCHEMA IF EXISTS ` + rlsSchema + ` CASCADE`)
		for _, role := range []string{rlsAlice, rlsBob, rlsLimited} {
			db.Exec(`DROP OWNED BY ` + role)
			db.Exec(`DROP ROLE IF EXISTS ` + role)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	opsMustExec(t, db,
		`CREATE ROLE `+rlsAlice, `CREATE ROLE `+rlsBob, `CREATE ROLE `+rlsLimited+` LOGIN PASSWORD 'jdt-rls-Pw1'`,
		`CREATE SCHEMA `+rlsSchema,
		`CREATE TABLE `+rlsSchema+`.notes (id serial PRIMARY KEY, owner text NOT NULL, tenant text NOT NULL, body text)`,
		`INSERT INTO `+rlsSchema+`.notes (owner, tenant, body) VALUES
		   ('`+rlsAlice+`', 'acme', 'a1'), ('`+rlsBob+`', 'acme', 'b1'),
		   ('`+rlsAlice+`', 'globex', 'a2'), ('`+rlsBob+`', 'globex', 'b2')`,
		`GRANT USAGE ON SCHEMA `+rlsSchema+` TO `+rlsAlice+`, `+rlsBob,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON `+rlsSchema+`.notes TO `+rlsAlice+`, `+rlsBob,
	)
	return db, dsn
}

// rlsExec returns a function that runs a plan, so that a planner's two
// results can be handed to it as they are.
func rlsExec(t *testing.T, db *sql.DB) func(*DDLPlan, error) {
	return func(plan *DDLPlan, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if err := plan.Exec(t.Context(), db); err != nil {
			t.Fatalf("%s: %v", plan, err)
		}
	}
}

func rlsBodies(t *testing.T, res *RoleTestResult) []string {
	t.Helper()
	var out []string
	for _, row := range res.Result.Rows {
		out = append(out, fmt.Sprint(row[0]))
	}
	return out
}

func TestLivePostgresRowSecurityPoliciesAndTheRoleTester(t *testing.T) {
	db, dsn := rlsLive(t)
	ctx := t.Context()
	// One connection, so that whatever a test leaves on it is what the next
	// statement meets.
	db.SetMaxOpenConns(1)
	run := rlsExec(t, db)

	state, err := ReadRowSecurity(ctx, db, DriverPostgres, rlsSchema, "notes")
	if err != nil || state.Enabled || state.Forced || len(state.Policies) != 0 || state.Schema != rlsSchema {
		t.Fatalf("a new table = %+v, %v", state, err)
	}
	if !slices.Contains(state.Exempt, "jdtest") {
		t.Errorf("the superuser is not named as exempt: %v", state.Exempt)
	}
	if _, err := ReadRowSecurity(ctx, db, DriverPostgres, rlsSchema, "no_such_table"); err != ErrObjectNotFound {
		t.Errorf("a missing table = %v", err)
	}

	seeing := func(role string, claims []Claim, table bool) *RoleTestResult {
		t.Helper()
		test := RoleTest{Role: role, Claims: claims, Query: `SELECT body FROM ` + rlsSchema + `.notes ORDER BY id`}
		if table {
			test.Schema, test.Table = rlsSchema, "notes"
		}
		res, err := RunAsRole(ctx, db, DriverPostgres, test)
		if err != nil {
			t.Fatalf("as %s: %v", role, err)
		}
		return res
	}

	// Off: every row is visible, and the test says why.
	res := seeing(rlsAlice, nil, true)
	if got := rlsBodies(t, res); strings.Join(got, ",") != "a1,b1,a2,b2" {
		t.Errorf("with the filter off alice saw %v", got)
	}
	if res.Role != rlsAlice || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "is off") {
		t.Errorf("the answer to a table that is not filtered: role %q, notes %v", res.Role, res.Notes)
	}

	run(PlanRowSecurity(DriverPostgres, RowSecurityChange{Schema: rlsSchema, Table: "notes", Enabled: boolPtr(true)}))
	// On with no policy: nothing is visible to a role that is held to it.
	if got := rlsBodies(t, seeing(rlsAlice, nil, false)); len(got) != 0 {
		t.Errorf("with the filter on and no policy alice saw %v", got)
	}

	// Owner only, by the session's user.
	run(PlanCreatePolicy(DriverPostgres, PolicySpec{Schema: rlsSchema, Table: "notes", Name: "owner_only",
		Using: "owner = current_user", WithCheck: "owner = current_user"}))
	if got := rlsBodies(t, seeing(rlsAlice, nil, false)); strings.Join(got, ",") != "a1,a2" {
		t.Errorf("alice saw %v under the owner policy", got)
	}
	if got := rlsBodies(t, seeing(rlsBob, nil, false)); strings.Join(got, ",") != "b1,b2" {
		t.Errorf("bob saw %v under the owner policy", got)
	}

	// A restrictive policy on a setting narrows what the permissive one lets
	// through; the setting is the test's claim.
	run(PlanCreatePolicy(DriverPostgres, PolicySpec{Schema: rlsSchema, Table: "notes", Name: "tenant", Restrictive: true,
		Using: "tenant = current_setting('app.tenant_id', true)"}))
	claims := []Claim{{"app.tenant_id", "globex"}}
	if got := rlsBodies(t, seeing(rlsAlice, claims, false)); strings.Join(got, ",") != "a2" {
		t.Errorf("alice in globex saw %v", got)
	}
	if got := rlsBodies(t, seeing(rlsAlice, nil, false)); len(got) != 0 {
		t.Errorf("alice with no tenant saw %v", got)
	}

	state, err = ReadRowSecurity(ctx, db, DriverPostgres, rlsSchema, "notes")
	if err != nil || !state.Enabled || len(state.Policies) != 2 {
		t.Fatalf("after two policies: %+v, %v", state, err)
	}
	owner, tenant := state.Policies[0], state.Policies[1]
	if tenant.Name == "owner_only" {
		owner, tenant = tenant, owner
	}
	if owner.Name != "owner_only" || !owner.Permissive || owner.Command != "ALL" || len(owner.Roles) != 0 ||
		!strings.Contains(owner.Using, "owner") || !strings.Contains(owner.WithCheck, "owner") {
		t.Errorf("the owner policy was read as %+v", owner)
	}
	if tenant.Name != "tenant" || tenant.Permissive || !strings.Contains(tenant.Using, "app.tenant_id") || tenant.WithCheck != "" {
		t.Errorf("the tenant policy was read as %+v", tenant)
	}

	// Nothing a test set is left on the connection the pool hands back.
	var left sql.NullString
	var who string
	if err := db.QueryRow(`SELECT current_setting('app.tenant_id', true), current_user`).Scan(&left, &who); err != nil {
		t.Fatal(err)
	}
	if left.String != "" || who != "jdtest" {
		t.Errorf("the connection was left as %q with tenant %q", who, left.String)
	}

	// The owner of a table is not held to its policies unless they are forced.
	opsMustExec(t, db, `ALTER TABLE `+rlsSchema+`.notes OWNER TO `+rlsAlice)
	res = seeing(rlsAlice, claims, true)
	if got := rlsBodies(t, res); len(got) != 4 || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "owns notes") {
		t.Errorf("the owner saw %v with notes %v", got, res.Notes)
	}
	run(PlanRowSecurity(DriverPostgres, RowSecurityChange{Schema: rlsSchema, Table: "notes", Force: boolPtr(true)}))
	if got := rlsBodies(t, seeing(rlsAlice, claims, true)); strings.Join(got, ",") != "a2" {
		t.Errorf("the owner of a forced table saw %v", got)
	}
	if state, _ = ReadRowSecurity(ctx, db, DriverPostgres, rlsSchema, "notes"); !state.Forced || state.Owner != rlsAlice {
		t.Errorf("forced = %v, owner %q", state.Forced, state.Owner)
	}

	// A role no policy filters.
	res = seeing("jdtest", nil, true)
	if got := rlsBodies(t, res); len(got) != 4 || len(res.Notes) == 0 || !strings.Contains(res.Notes[0], "superuser") {
		t.Errorf("the superuser saw %v with notes %v", got, res.Notes)
	}

	// Changing a policy.
	run(PlanAlterPolicy(ctx, db, DriverPostgres, PolicyChange{Schema: rlsSchema, Table: "notes", Name: "owner_only",
		SetRoles: true, Roles: []string{rlsAlice}, Using: "owner = current_user AND body <> 'a2'"}))
	if got := rlsBodies(t, seeing(rlsAlice, claims, false)); len(got) != 0 {
		t.Errorf("alice in globex under the altered policy saw %v", got)
	}
	if got := rlsBodies(t, seeing(rlsAlice, []Claim{{"app.tenant_id", "acme"}}, false)); strings.Join(got, ",") != "a1" {
		t.Errorf("alice in acme under the altered policy saw %v", got)
	}
	state, _ = ReadRowSecurity(ctx, db, DriverPostgres, rlsSchema, "notes")
	for _, p := range state.Policies {
		if p.Name == "owner_only" && (len(p.Roles) != 1 || p.Roles[0] != rlsAlice || !strings.Contains(p.Using, "a2") || !strings.Contains(p.WithCheck, "owner")) {
			t.Errorf("the altered policy reads %+v: a condition left empty has to stay as it was", p)
		}
	}
	// A condition changed alone leaves who the policy is for as it was: no
	// roles is everybody, and a change of a condition must not widen that.
	run(PlanAlterPolicy(ctx, db, DriverPostgres, PolicyChange{Schema: rlsSchema, Table: "notes", Name: "owner_only",
		Using: "owner = current_user AND body <> 'a2' AND true"}))
	state, _ = ReadRowSecurity(ctx, db, DriverPostgres, rlsSchema, "notes")
	for _, p := range state.Policies {
		if p.Name == "owner_only" && (len(p.Roles) != 1 || p.Roles[0] != rlsAlice || !strings.Contains(p.Using, "true")) {
			t.Errorf("a change of a condition alone left the policy as %+v", p)
		}
	}
	if plan, err := PlanAlterPolicy(ctx, db, DriverPostgres, PolicyChange{Schema: rlsSchema, Table: "notes", Name: "gone", Using: "true"}); err == nil {
		t.Errorf("a policy that is not there was altered: %s", plan)
	}
	run(PlanCreatePolicy(DriverPostgres, PolicySpec{Schema: rlsSchema, Table: "notes", Name: "ins_only", Command: "INSERT", WithCheck: "true"}))
	if plan, err := PlanAlterPolicy(ctx, db, DriverPostgres, PolicyChange{Schema: rlsSchema, Table: "notes", Name: "ins_only", Using: "true"}); err == nil {
		t.Errorf("an INSERT policy was given a USING condition: %s", plan)
	}

	// The test is a read, in a transaction that cannot write.
	opsMustExec(t, db, `CREATE FUNCTION `+rlsSchema+`.bump() RETURNS int LANGUAGE sql AS 'UPDATE `+rlsSchema+`.notes SET body = ''bumped'' RETURNING 1'`)
	_, err = RunAsRole(ctx, db, DriverPostgres, RoleTest{Role: "jdtest", Query: `SELECT ` + rlsSchema + `.bump()`})
	if err == nil || !strings.Contains(err.Error(), "read-only transaction") {
		t.Errorf("a function that writes ran from a test: %v", err)
	}
	var bumped int
	if err := db.QueryRow(`SELECT count(*) FROM ` + rlsSchema + `.notes WHERE body = 'bumped'`).Scan(&bumped); err != nil || bumped != 0 {
		t.Errorf("a test changed %d rows (%v)", bumped, err)
	}
	// The classifier already turns set_config away; a function that calls it
	// is a read to the classifier, and what the test checks afterwards is that
	// the statement is still running as the role it was put in.
	_, err = RunAsRole(ctx, db, DriverPostgres, RoleTest{Role: rlsBob, Query: `SELECT set_config('role', 'none', true)`})
	if _, ok := err.(ErrRoleTestRefused); !ok {
		t.Errorf("set_config as a test = %v", err)
	}
	opsMustExec(t, db, `CREATE FUNCTION `+rlsSchema+`.leave() RETURNS int LANGUAGE plpgsql AS $$ BEGIN PERFORM set_config('role', 'none', true); RETURN 1; END $$`)
	_, err = RunAsRole(ctx, db, DriverPostgres, RoleTest{Role: rlsBob, Query: `SELECT ` + rlsSchema + `.leave()`})
	if _, ok := err.(ErrRoleTestRefused); !ok || !strings.Contains(err.Error(), "changed the role") {
		t.Errorf("a statement that changed its role = %v", err)
	}
	// Rows are bounded.
	res, err = RunAsRole(ctx, db, DriverPostgres, RoleTest{Role: rlsBob, Query: `SELECT g FROM generate_series(1, 50) g`, MaxRows: 5})
	if err != nil || res.Result.RowCount != 5 || !res.Result.Truncated {
		t.Errorf("a bounded read = %+v, %v", res, err)
	}
	// What was sent first is on the answer, in order.
	res = seeing(rlsBob, []Claim{{"app.tenant_id", "o'brien"}}, false)
	want := []string{"SET LOCAL statement_timeout = '15000ms'", "SELECT set_config('app.tenant_id', 'o''brien', true)", `SET LOCAL ROLE "` + rlsBob + `"`}
	if strings.Join(res.Setup, "\n") != strings.Join(want, "\n") {
		t.Errorf("setup = %v, want %v", res.Setup, want)
	}
	if _, err := RunAsRole(ctx, db, DriverPostgres, RoleTest{Role: "jdt_rls_editor_nobody", Query: `SELECT 1`}); err == nil {
		t.Error("a role that does not exist was run as")
	}

	// An account that is not a member of the role cannot become it.
	limited, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	limited.User = url.UserPassword(rlsLimited, "jdt-rls-Pw1")
	as := opsOpen(t, DriverPostgres, limited.String(), "the limited account")
	if _, err := RunAsRole(ctx, as, DriverPostgres, RoleTest{Role: rlsBob, Query: `SELECT 1`}); err == nil || !strings.Contains(err.Error(), "not a member") {
		t.Errorf("an account became a role it is not a member of: %v", err)
	}
	opsMustExec(t, db, `GRANT `+rlsBob+` TO `+rlsLimited)
	if res, err := RunAsRole(ctx, as, DriverPostgres, RoleTest{Role: rlsBob, Query: `SELECT current_user`}); err != nil || res.Role != rlsBob {
		t.Errorf("a member could not become the role: %v", err)
	}

	// Taking it apart again.
	run(PlanDropPolicy(DriverPostgres, rlsSchema, "notes", "tenant"))
	run(PlanRowSecurity(DriverPostgres, RowSecurityChange{Schema: rlsSchema, Table: "notes", Enabled: boolPtr(false), Force: boolPtr(false)}))
	state, _ = ReadRowSecurity(ctx, db, DriverPostgres, rlsSchema, "notes")
	if state.Enabled || state.Forced || len(state.Policies) != 2 {
		t.Errorf("after the drop and the disable: %+v", state)
	}
}

func TestLivePostgresFunctionAndColumnPrivileges(t *testing.T) {
	db, _ := rlsLive(t)
	ctx := t.Context()
	opsMustExec(t, db,
		`CREATE FUNCTION `+rlsSchema+`.twice(n int) RETURNS int LANGUAGE sql IMMUTABLE AS 'SELECT n * 2'`,
		`CREATE FUNCTION `+rlsSchema+`.twice(n text) RETURNS text LANGUAGE sql IMMUTABLE AS 'SELECT n || n'`,
		`REVOKE EXECUTE ON ALL ROUTINES IN SCHEMA `+rlsSchema+` FROM PUBLIC`,
	)
	can := func(query string, args ...any) bool {
		t.Helper()
		var ok bool
		if err := db.QueryRow(query, args...).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	change := func(c PrivilegeChange) []string {
		t.Helper()
		c.Role = rlsAlice
		out, err := ChangePrivileges(ctx, db, DriverPostgres, c, false)
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		return out.Statements
	}
	integer := "n integer"
	twice := func(arguments *string) PrivilegeChange {
		return PrivilegeChange{Level: GrantOnFunction, Schema: rlsSchema, Routine: "twice", Arguments: arguments, Privileges: []string{"EXECUTE"}}
	}
	const asInt, asText = rlsSchema + `.twice(integer)`, rlsSchema + `.twice(text)`
	hasExecute := `SELECT has_function_privilege('` + rlsAlice + `', $1, 'EXECUTE')`

	// One overload, told apart by its argument types.
	change(twice(&integer))
	if !can(hasExecute, asInt) || can(hasExecute, asText) {
		t.Error("granting one overload did not give exactly that one")
	}
	// The name alone is ambiguous between the two, and the engine says so.
	if _, err := ChangePrivileges(ctx, db, DriverPostgres, func() PrivilegeChange { c := twice(nil); c.Role = rlsAlice; return c }(), false); err == nil {
		t.Error("an overloaded name was granted without its arguments")
	}
	grants, _, err := ListGrants(ctx, db, DriverPostgres, GrantFilter{Role: rlsAlice, Schema: rlsSchema})
	if err != nil {
		t.Fatal(err)
	}
	var listed *Grant
	for i := range grants {
		if grants[i].Level == GrantOnFunction && !grants[i].Future {
			listed = &grants[i]
		}
	}
	if listed == nil || listed.Routine != "twice" || listed.Arguments != "n integer" || listed.Schema != rlsSchema ||
		strings.Join(listed.Privileges, ",") != "EXECUTE" || listed.Table != "" {
		t.Errorf("the function grant was listed as %+v", listed)
	}
	change(PrivilegeChange{Level: GrantOnFunction, Schema: rlsSchema, Routine: "twice", Arguments: &integer, Privileges: []string{"EXECUTE"}, Revoke: true})
	if can(hasExecute, asInt) {
		t.Error("the revoke left EXECUTE")
	}

	// Every function in the schema, and the ones made later.
	stmts := change(PrivilegeChange{Level: GrantOnFunction, Schema: rlsSchema, Privileges: []string{"EXECUTE"}, Future: true})
	if len(stmts) != 2 || !strings.HasPrefix(stmts[1], `ALTER DEFAULT PRIVILEGES IN SCHEMA "`+rlsSchema+`" GRANT EXECUTE ON FUNCTIONS TO`) {
		t.Errorf("schema-wide grant ran %v", stmts)
	}
	opsMustExec(t, db, `CREATE FUNCTION `+rlsSchema+`.later() RETURNS int LANGUAGE sql AS 'SELECT 1'`)
	if !can(hasExecute, asInt) || !can(hasExecute, asText) || !can(hasExecute, rlsSchema+`.later()`) {
		t.Error("the schema-wide grant missed an existing or a later function")
	}
	grants, _, err = ListGrants(ctx, db, DriverPostgres, GrantFilter{Role: rlsAlice, Schema: rlsSchema})
	if err != nil {
		t.Fatal(err)
	}
	future := false
	for _, g := range grants {
		future = future || (g.Level == GrantOnFunction && g.Future && g.Schema == rlsSchema && strings.Join(g.Privileges, ",") == "EXECUTE")
	}
	if !future {
		t.Errorf("the default privilege on functions is not listed: %+v", grants)
	}

	// Columns.
	hasColumn := func(column, privilege string) bool {
		t.Helper()
		return can(`SELECT has_column_privilege($1, $2, $3, $4)`, rlsBob, rlsSchema+".notes", column, privilege)
	}
	// Bob holds the table privileges from the fixture; take them away to see
	// the column ones alone.
	opsMustExec(t, db, `REVOKE ALL ON `+rlsSchema+`.notes FROM `+rlsBob)
	out, err := ChangePrivileges(ctx, db, DriverPostgres, PrivilegeChange{Role: rlsBob, Level: GrantOnColumn, Schema: rlsSchema, Table: "notes",
		Columns: []string{"id", "body"}, Privileges: []string{"SELECT", "update"}}, false)
	if err != nil || len(out.Statements) != 1 {
		t.Fatalf("column grant: %v %v", out, err)
	}
	if !hasColumn("id", "SELECT") || !hasColumn("body", "UPDATE") || hasColumn("owner", "SELECT") || hasColumn("id", "INSERT") {
		t.Error("the column grant did not give exactly SELECT and UPDATE on the two columns")
	}
	grants, _, err = ListGrants(ctx, db, DriverPostgres, GrantFilter{Role: rlsBob, Schema: rlsSchema, Table: "notes"})
	if err != nil {
		t.Fatal(err)
	}
	held := map[string]string{}
	for _, g := range grants {
		if g.Level == GrantOnColumn {
			if g.Table != "notes" || g.Routine != "" {
				t.Errorf("a column grant names its table wrongly: %+v", g)
			}
			held[g.Column] = strings.Join(g.Privileges, ",")
		}
	}
	if held["id"] != "SELECT,UPDATE" || held["body"] != "SELECT,UPDATE" || len(held) != 2 {
		t.Errorf("the column grants were listed as %v", held)
	}
	if _, err := ChangePrivileges(ctx, db, DriverPostgres, PrivilegeChange{Role: rlsBob, Level: GrantOnColumn, Schema: rlsSchema, Table: "notes",
		Columns: []string{"id"}, Privileges: []string{"UPDATE"}, Revoke: true}, false); err != nil {
		t.Fatal(err)
	}
	if hasColumn("id", "UPDATE") || !hasColumn("id", "SELECT") || !hasColumn("body", "UPDATE") {
		t.Error("the revoke did not take exactly UPDATE on the one column")
	}
	if _, err := ChangePrivileges(ctx, db, DriverPostgres, PrivilegeChange{Role: rlsBob, Level: GrantOnColumn, Schema: rlsSchema, Table: "notes",
		Columns: []string{"no_such_column"}, Privileges: []string{"SELECT"}}, false); err == nil {
		t.Error("a column that is not there was granted")
	}
	// The preview of any of it dials nothing and changes nothing.
	if _, err := ChangePrivileges(ctx, db, DriverPostgres, PrivilegeChange{Role: rlsBob, Level: GrantOnColumn, Schema: rlsSchema, Table: "notes",
		Columns: []string{"owner"}, Privileges: []string{"SELECT"}}, true); err != nil || hasColumn("owner", "SELECT") {
		t.Errorf("a preview changed something (%v)", err)
	}
}
