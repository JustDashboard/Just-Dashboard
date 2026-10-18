package dbx

import (
	"strings"
	"testing"
)

func boolPtr(on bool) *bool { return &on }

func TestRowSecurityStatements(t *testing.T) {
	for _, c := range []struct {
		name   string
		change RowSecurityChange
		want   string
	}{
		{"enable", RowSecurityChange{Schema: "app", Table: "orders", Enabled: boolPtr(true)},
			`ALTER TABLE "app"."orders" ENABLE ROW LEVEL SECURITY`},
		{"disable", RowSecurityChange{Schema: "app", Table: "orders", Enabled: boolPtr(false)},
			`ALTER TABLE "app"."orders" DISABLE ROW LEVEL SECURITY`},
		{"enable and force", RowSecurityChange{Schema: "app", Table: "or\"ders", Enabled: boolPtr(true), Force: boolPtr(true)},
			"ALTER TABLE \"app\".\"or\"\"ders\" ENABLE ROW LEVEL SECURITY;\nALTER TABLE \"app\".\"or\"\"ders\" FORCE ROW LEVEL SECURITY"},
		{"stop forcing", RowSecurityChange{Schema: "app", Table: "orders", Force: boolPtr(false)},
			`ALTER TABLE "app"."orders" NO FORCE ROW LEVEL SECURITY`},
	} {
		plan, err := PlanRowSecurity(DriverPostgres, c.change)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if plan.String() != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, plan, c.want)
		}
	}
	if _, err := PlanRowSecurity(DriverPostgres, RowSecurityChange{Schema: "app", Table: "orders"}); err == nil {
		t.Error("a change that says nothing was planned")
	}
	for change, off := range map[string]struct {
		c    RowSecurityChange
		want bool
	}{
		"disabled":         {RowSecurityChange{Enabled: boolPtr(false)}, true},
		"no longer forced": {RowSecurityChange{Force: boolPtr(false)}, true},
		"both":             {RowSecurityChange{Enabled: boolPtr(false), Force: boolPtr(false)}, true},
		"enabled":          {RowSecurityChange{Enabled: boolPtr(true)}, false},
		"forced":           {RowSecurityChange{Force: boolPtr(true)}, false},
		"enabled, forced":  {RowSecurityChange{Enabled: boolPtr(true), Force: boolPtr(true)}, false},
	} {
		if got := off.c.TurnsOff(); got != off.want {
			t.Errorf("%s: TurnsOff = %v, want %v", change, got, off.want)
		}
	}
}

func TestPolicyStatements(t *testing.T) {
	for _, c := range []struct {
		name  string
		spec  PolicySpec
		want  string
		calls []string
	}{
		{"owner only", PolicySpec{Schema: "app", Table: "notes", Name: "notes_owner", Using: "owner = current_user", WithCheck: "owner = current_user"},
			"CREATE POLICY \"notes_owner\" ON \"app\".\"notes\"\n  AS PERMISSIVE\n  FOR ALL\n  TO PUBLIC\n  USING (owner = current_user)\n  WITH CHECK (owner = current_user)", nil},
		{"tenant isolation by a setting", PolicySpec{Schema: "app", Table: "notes", Name: "tenant", Restrictive: true, Command: "all", Roles: []string{"web", "worker"},
			Using: "tenant_id::text = current_setting('app.tenant_id', true)"},
			"CREATE POLICY \"tenant\" ON \"app\".\"notes\"\n  AS RESTRICTIVE\n  FOR ALL\n  TO \"web\", \"worker\"\n  USING (tenant_id::text = current_setting('app.tenant_id', true))", nil},
		{"read only for a role", PolicySpec{Schema: "app", Table: "notes", Name: "auditors_read", Command: "SELECT", Roles: []string{"auditor"}, Using: "true"},
			"CREATE POLICY \"auditors_read\" ON \"app\".\"notes\"\n  AS PERMISSIVE\n  FOR SELECT\n  TO \"auditor\"\n  USING (true)", nil},
		{"insert takes only the check", PolicySpec{Table: "notes", Name: "ins", Command: "INSERT", Roles: []string{"PUBLIC"}, WithCheck: "length(body) < 10000"},
			"CREATE POLICY \"ins\" ON \"notes\"\n  AS PERMISSIVE\n  FOR INSERT\n  TO PUBLIC\n  WITH CHECK (length(body) < 10000)", nil},
		{"a call of its own is named", PolicySpec{Schema: "app", Table: "notes", Name: "mine", Using: "owner = auth.uid() AND visible(owner)"},
			"CREATE POLICY \"mine\" ON \"app\".\"notes\"\n  AS PERMISSIVE\n  FOR ALL\n  TO PUBLIC\n  USING (owner = auth.uid() AND visible(owner))",
			[]string{"auth.uid", "visible"}},
		{"a qualified spelling is not the vouched call", PolicySpec{Schema: "app", Table: "notes", Name: "q", Using: "x = public.current_setting('a.b')"},
			"CREATE POLICY \"q\" ON \"app\".\"notes\"\n  AS PERMISSIVE\n  FOR ALL\n  TO PUBLIC\n  USING (x = public.current_setting('a.b'))",
			[]string{"public.current_setting"}},
	} {
		plan, err := PlanCreatePolicy(DriverPostgres, c.spec)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if plan.String() != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, plan, c.want)
		}
		if strings.Join(plan.Unvouched, ",") != strings.Join(c.calls, ",") {
			t.Errorf("%s: unvouched calls %v, want %v", c.name, plan.Unvouched, c.calls)
		}
	}
}

func TestPolicyRequestsAreRefusedForWhatTheyAsk(t *testing.T) {
	base := PolicySpec{Schema: "app", Table: "notes", Name: "p", Using: "true"}
	with := func(edit func(*PolicySpec)) PolicySpec {
		s := base
		edit(&s)
		return s
	}
	for name, spec := range map[string]PolicySpec{
		"no name":                with(func(s *PolicySpec) { s.Name = " " }),
		"no condition":           with(func(s *PolicySpec) { s.Using = "" }),
		"an unknown command":     with(func(s *PolicySpec) { s.Command = "TRUNCATE" }),
		"insert with using":      with(func(s *PolicySpec) { s.Command = "INSERT" }),
		"select with check":      with(func(s *PolicySpec) { s.Command = "SELECT"; s.WithCheck = "true" }),
		"delete with check":      with(func(s *PolicySpec) { s.Command = "DELETE"; s.WithCheck = "true" }),
		"a second statement":     with(func(s *PolicySpec) { s.Using = "true); DROP TABLE notes; --" }),
		"a closed parenthesis":   with(func(s *PolicySpec) { s.Using = "true) TO PUBLIC (true" }),
		"a comment":              with(func(s *PolicySpec) { s.Using = "true -- all of it" }),
		"a dollar quote":         with(func(s *PolicySpec) { s.Using = "$$x$$ = 'y'" }),
		"a backslash":            with(func(s *PolicySpec) { s.WithCheck = `a = '\'` }),
		"a role with a bad name": with(func(s *PolicySpec) { s.Roles = []string{""} }),
	} {
		if plan, err := PlanCreatePolicy(DriverPostgres, spec); err == nil {
			t.Errorf("%s was planned: %s", name, plan)
		}
	}
	for _, driver := range []Driver{DriverMySQL, DriverSQLite, DriverMSSQL, DriverClickHouse, DriverOracle} {
		_, err := PlanCreatePolicy(driver, base)
		if err == nil || !strings.Contains(err.Error(), "PostgreSQL's") {
			t.Errorf("%s: a policy was planned or refused without saying whose they are: %v", driver, err)
		}
		if _, err := PlanDropPolicy(driver, "s", "t", "p"); err == nil {
			t.Errorf("%s: a policy drop was planned", driver)
		}
		if _, err := PlanRowSecurity(driver, RowSecurityChange{Table: "t", Enabled: boolPtr(true)}); err == nil {
			t.Errorf("%s: row-level security was planned", driver)
		}
	}
}

func TestAChangeOfAPolicyHasToSaySomething(t *testing.T) {
	// Refused before the policy is read, so no connection is needed.
	if plan, err := PlanAlterPolicy(t.Context(), nil, DriverPostgres, PolicyChange{Schema: "app", Table: "notes", Name: "p"}); err == nil {
		t.Errorf("a change that changes nothing was planned: %s", plan)
	}
	if plan, err := PlanAlterPolicy(t.Context(), nil, DriverPostgres, PolicyChange{Schema: "app", Table: "notes"}); err == nil {
		t.Errorf("a change of no policy was planned: %s", plan)
	}
	if _, err := PlanAlterPolicy(t.Context(), nil, DriverMySQL, PolicyChange{Table: "t", Name: "p", Using: "true"}); err == nil ||
		!strings.Contains(err.Error(), "PostgreSQL's") {
		t.Errorf("another engine's change = %v", err)
	}
}

func TestDropPolicyStatement(t *testing.T) {
	plan, err := PlanDropPolicy(DriverPostgres, "app", "notes", "notes_owner")
	if err != nil || plan.String() != `DROP POLICY "notes_owner" ON "app"."notes"` {
		t.Errorf("drop = %v, %v", plan, err)
	}
	if _, err := PlanDropPolicy(DriverPostgres, "app", "notes", ""); err == nil {
		t.Error("a drop with no name was planned")
	}
}

func TestACommandIsFoldedOrRefused(t *testing.T) {
	for in, want := range map[string]string{"": "ALL", " select ": "SELECT", "Update": "UPDATE", "ALL": "ALL"} {
		if got, err := PolicyCommand(in); err != nil || got != want {
			t.Errorf("PolicyCommand(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := PolicyCommand("MERGE"); err == nil {
		t.Error("MERGE is not a command a policy applies to")
	}
}

func TestARoleTestAcceptsOnlyAReadAndOnlyASettingOfItsOwn(t *testing.T) {
	ok := RoleTest{Role: "web", Query: "SELECT * FROM app.notes", Claims: []Claim{{"app.tenant_id", "acme"}, {"request.jwt.claims", `{"sub":"1"}`}}}
	if st, err := CheckRoleTest(DriverPostgres, ok); err != nil || st.SQL != "SELECT * FROM app.notes" {
		t.Fatalf("a read was refused: %v", err)
	}
	for _, query := range []string{"WITH t AS (SELECT 1) SELECT * FROM t", "TABLE app.notes", "VALUES (1)", "SELECT 1;"} {
		ok.Query = query
		if _, err := CheckRoleTest(DriverPostgres, ok); err != nil {
			t.Errorf("%q: %v", query, err)
		}
	}
	for name, test := range map[string]RoleTest{
		"no role":                 {Query: "SELECT 1"},
		"no query":                {Role: "web"},
		"an update":               {Role: "web", Query: "UPDATE app.notes SET body = ''"},
		"a delete":                {Role: "web", Query: "DELETE FROM app.notes"},
		"an insert":               {Role: "web", Query: "INSERT INTO app.notes DEFAULT VALUES"},
		"a drop":                  {Role: "web", Query: "DROP TABLE app.notes"},
		"two statements":          {Role: "web", Query: "SELECT 1; DELETE FROM app.notes"},
		"a select into":           {Role: "web", Query: "SELECT * INTO app.copy FROM app.notes"},
		"a write behind a with":   {Role: "web", Query: "WITH d AS (DELETE FROM app.notes RETURNING *) SELECT * FROM d"},
		"a show":                  {Role: "web", Query: "SHOW search_path"},
		"an explain":              {Role: "web", Query: "EXPLAIN SELECT 1"},
		"a set":                   {Role: "web", Query: "SET ROLE postgres"},
		"a reset":                 {Role: "web", Query: "RESET ROLE"},
		"a do block":              {Role: "web", Query: "DO $$ BEGIN PERFORM 1; END $$"},
		"a dollar quoted select":  {Role: "web", Query: "SELECT $$x$$"},
		"the role setting":        {Role: "web", Query: "SELECT 1", Claims: []Claim{{"role", "postgres"}}},
		"a session setting":       {Role: "web", Query: "SELECT 1", Claims: []Claim{{"session_authorization", "postgres"}}},
		"a name that quotes":      {Role: "web", Query: "SELECT 1", Claims: []Claim{{"app.x', true); DROP TABLE t; --", "1"}}},
		"a setting set twice":     {Role: "web", Query: "SELECT 1", Claims: []Claim{{"app.x", "1"}, {"APP.X", "2"}}},
		"a value that is too big": {Role: "web", Query: "SELECT 1", Claims: []Claim{{"app.x", strings.Repeat("a", maxClaimValue+1)}}},
	} {
		if _, err := CheckRoleTest(DriverPostgres, test); err == nil {
			t.Errorf("%s was accepted", name)
		} else if _, refused := err.(ErrRoleTestRefused); !refused {
			t.Errorf("%s: %v is not a refusal of the request", name, err)
		}
	}
	tooMany := RoleTest{Role: "web", Query: "SELECT 1"}
	for i := range maxClaims + 1 {
		tooMany.Claims = append(tooMany.Claims, Claim{Name: "app.k" + string(rune('a'+i)), Value: "v"})
	}
	if _, err := CheckRoleTest(DriverPostgres, tooMany); err == nil {
		t.Error("a test setting more claims than the limit was accepted")
	}
	for _, driver := range []Driver{DriverMySQL, DriverSQLite, DriverMSSQL} {
		if _, err := CheckRoleTest(driver, RoleTest{Role: "web", Query: "SELECT 1"}); err == nil {
			t.Errorf("%s was offered a role test", driver)
		}
	}
}
