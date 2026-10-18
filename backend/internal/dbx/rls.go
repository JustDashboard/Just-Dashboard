package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Row-level security on PostgreSQL: whether a table filters its rows by who
// asks, the policies that decide it, the forms that change both, and a way to
// see a table as another role would.
//
// The forms are schema changes in every respect that matters — each plans the
// exact statement, shows it for ?preview=1 and runs it otherwise — so they are
// written the way the DDL planners are (ddl.go): identifiers quoted by the
// dialect, the operator's own conditions confined by validateFragment and read
// for the calls they make. What differs is who may ask. A policy decides who
// can see which rows, as a grant decides who can see which tables, so the
// routes sit with grants under system.admin rather than with the forms that
// only add.

// PolicyCommands are the statements a policy applies to, as CREATE POLICY
// spells them.
var PolicyCommands = []string{"ALL", "SELECT", "INSERT", "UPDATE", "DELETE"}

// Policy is one row of pg_policy.
type Policy struct {
	Name string `json:"name"`
	// Command is one of PolicyCommands.
	Command string `json:"command"`
	// Permissive policies are combined with OR; a restrictive one must hold
	// as well, and does nothing when no permissive policy lets a row in.
	Permissive bool `json:"permissive"`
	// Roles are the roles it applies to; empty is everybody (PUBLIC).
	Roles []string `json:"roles"`
	// Using is the condition a row must meet to be seen, changed or deleted;
	// WithCheck the one a row written must meet. Either may be empty.
	Using     string `json:"using,omitempty"`
	WithCheck string `json:"withCheck,omitempty"`
}

// RowSecurity is a table's row-level security: whether it is on, and the
// policies that say what it does.
type RowSecurity struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	// Enabled is relrowsecurity: with it off the policies are kept and ignored.
	Enabled bool `json:"enabled"`
	// Forced is relforcerowsecurity: the table's owner is held to the policies
	// as well, which it is not otherwise.
	Forced bool   `json:"forced"`
	Owner  string `json:"owner"`
	// Exempt names the roles no policy filters: superusers and the roles with
	// BYPASSRLS.
	Exempt   []string `json:"exempt"`
	Policies []Policy `json:"policies"`
}

// maxExempt bounds the roles named as exempt; a server with more has made the
// attribute a habit and the list says nothing.
const maxExempt = 50

// rlsRefusal says why an engine has no row-level security to edit.
func rlsRefusal(driver Driver) error {
	return fmt.Errorf("row-level security is PostgreSQL's: %s has no policies to edit", ormEngineName(driver, ""))
}

// ReadRowSecurity reads a table's row-level security.
func ReadRowSecurity(ctx context.Context, db *sql.DB, driver Driver, schema, table string) (*RowSecurity, error) {
	if driver != DriverPostgres {
		return nil, rlsRefusal(driver)
	}
	out := &RowSecurity{Table: table, Exempt: []string{}, Policies: []Policy{}}
	var oid string
	err := db.QueryRowContext(ctx, `
	  SELECT c.oid::text, n.nspname, c.relrowsecurity, c.relforcerowsecurity, COALESCE(pg_get_userbyid(c.relowner), '')
	  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	  WHERE `+pgSchemaIs("n")+` AND c.relname = $2 AND c.relkind IN ('r', 'p')`, schema, table).
		Scan(&oid, &out.Schema, &out.Enabled, &out.Forced, &out.Owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT p.polname::text,
	         CASE p.polcmd WHEN 'r' THEN 'SELECT' WHEN 'a' THEN 'INSERT' WHEN 'w' THEN 'UPDATE'
	                       WHEN 'd' THEN 'DELETE' ELSE 'ALL' END,
	         p.polpermissive,
	         COALESCE((SELECT string_agg(r.rolname, chr(31) ORDER BY r.rolname)
	                     FROM pg_roles r WHERE r.oid = ANY(p.polroles)), ''),
	         COALESCE(pg_get_expr(p.polqual, p.polrelid), ''),
	         COALESCE(pg_get_expr(p.polwithcheck, p.polrelid), '')
	  FROM pg_policy p WHERE p.polrelid = $1::text::oid ORDER BY p.polname`, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Policy
		var roles string
		if err := rows.Scan(&p.Name, &p.Command, &p.Permissive, &roles, &p.Using, &p.WithCheck); err != nil {
			return nil, err
		}
		p.Roles = splitUnit(roles)
		out.Policies = append(out.Policies, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	exempt, err := db.QueryContext(ctx,
		`SELECT rolname::text FROM pg_roles WHERE rolsuper OR rolbypassrls ORDER BY rolname LIMIT $1`, maxExempt)
	if err != nil {
		return nil, err
	}
	defer exempt.Close()
	for exempt.Next() {
		var name string
		if err := exempt.Scan(&name); err != nil {
			return nil, err
		}
		out.Exempt = append(out.Exempt, name)
	}
	return out, exempt.Err()
}

// --- planning -----------------------------------------------------------------

// policyCalls are the calls a policy condition may make that the form vouches
// for. Every user of the table runs the condition for every row they touch, so
// the list is the one in ddl.go (pureFunctions) widened by the few functions a
// policy is written with and that only read the session: its settings and its
// privileges. A qualified or quoted spelling is not on it — unvouchedCalls
// reports those by the spelling, and public.current_setting is not
// current_setting.
var policyCalls = stringSet(
	"current_setting", "has_table_privilege", "has_schema_privilege", "has_column_privilege", "pg_has_role",
)

// policyExpression validates a condition and returns it with the calls in it
// that the form cannot vouch for.
func policyExpression(what, text string) (string, []string, error) {
	expression, err := validateFragment(DriverPostgres, what, text)
	if err != nil {
		return "", nil, err
	}
	var calls []string
	for _, call := range unvouchedCalls(DriverPostgres, expression) {
		if !policyCalls[call] {
			calls = append(calls, call)
		}
	}
	return expression, calls, nil
}

// PolicyCommand folds a command to the spelling CREATE POLICY takes; empty is ALL.
func PolicyCommand(command string) (string, error) {
	command = strings.ToUpper(strings.TrimSpace(command))
	if command == "" {
		return "ALL", nil
	}
	if !slices.Contains(PolicyCommands, command) {
		return "", fmt.Errorf("a policy applies to one of %s, not %q", strings.Join(PolicyCommands, ", "), command)
	}
	return command, nil
}

// checkPolicyClauses refuses the clause a command has no use for, as the
// engine does — but before the statement is drawn up, so that a form can say
// so in its own words while it is being filled in.
func checkPolicyClauses(command, using, withCheck string) error {
	switch command {
	case "INSERT":
		if using != "" {
			return fmt.Errorf("an INSERT policy has no USING condition: it checks the row being written, so it takes WITH CHECK")
		}
	case "SELECT", "DELETE":
		if withCheck != "" {
			return fmt.Errorf("a %s policy has no WITH CHECK condition: it only decides which rows are visible, so it takes USING", command)
		}
	}
	return nil
}

// policyRoles renders the TO clause: the roles quoted, PUBLIC as the keyword.
func policyRoles(d Dialect, roles []string) (string, error) {
	if len(roles) == 0 {
		return "PUBLIC", nil
	}
	quoted := make([]string, 0, len(roles))
	for _, role := range roles {
		if strings.EqualFold(strings.TrimSpace(role), "public") {
			quoted = append(quoted, "PUBLIC")
			continue
		}
		q, err := d.QuoteIdent(strings.TrimSpace(role))
		if err != nil {
			return "", err
		}
		quoted = append(quoted, q)
	}
	return strings.Join(quoted, ", "), nil
}

func rlsTarget(driver Driver, schema, table string) (Dialect, string, error) {
	if driver != DriverPostgres {
		return nil, "", rlsRefusal(driver)
	}
	d, err := DialectFor(driver)
	if err != nil {
		return nil, "", err
	}
	rel, err := ddlRel(d, schema, table)
	return d, rel, err
}

// RowSecurityChange turns a table's row-level security on or off, and holds
// its owner to it or not. A nil field is left as it is.
type RowSecurityChange struct {
	Schema, Table string
	Enabled       *bool
	Force         *bool
}

// TurnsOff reports whether the change takes the filter off anybody: off for
// the table, which shows every row to everybody who may read it, or no longer
// forced, which shows them to its owner. That is the opposite of what the
// policies were written for, so the caller treats it as a drop.
func (c RowSecurityChange) TurnsOff() bool {
	return c.Enabled != nil && !*c.Enabled || c.Force != nil && !*c.Force
}

// PlanRowSecurity plans enabling, disabling or forcing row-level security.
func PlanRowSecurity(driver Driver, c RowSecurityChange) (*DDLPlan, error) {
	_, rel, err := rlsTarget(driver, c.Schema, c.Table)
	if err != nil {
		return nil, err
	}
	if c.Enabled == nil && c.Force == nil {
		return nil, fmt.Errorf("say whether row-level security is to be on or off, or forced on the owner")
	}
	var statements []string
	if c.Enabled != nil {
		verb := "DISABLE"
		if *c.Enabled {
			verb = "ENABLE"
		}
		statements = append(statements, "ALTER TABLE "+rel+" "+verb+" ROW LEVEL SECURITY")
	}
	if c.Force != nil {
		verb := "NO FORCE"
		if *c.Force {
			verb = "FORCE"
		}
		statements = append(statements, "ALTER TABLE "+rel+" "+verb+" ROW LEVEL SECURITY")
	}
	return planOf(statements...), nil
}

// PolicySpec is a policy to create.
type PolicySpec struct {
	Schema, Table, Name string
	// Command is one of PolicyCommands; empty is ALL.
	Command     string
	Restrictive bool
	// Roles it applies to; empty is everybody.
	Roles     []string
	Using     string
	WithCheck string
}

// PlanCreatePolicy plans CREATE POLICY.
func PlanCreatePolicy(driver Driver, spec PolicySpec) (*DDLPlan, error) {
	d, rel, err := rlsTarget(driver, spec.Schema, spec.Table)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.Name) == "" {
		return nil, fmt.Errorf("the policy's name is required")
	}
	name, err := d.QuoteIdent(strings.TrimSpace(spec.Name))
	if err != nil {
		return nil, err
	}
	command, err := PolicyCommand(spec.Command)
	if err != nil {
		return nil, err
	}
	using, withCheck := strings.TrimSpace(spec.Using), strings.TrimSpace(spec.WithCheck)
	if using == "" && withCheck == "" {
		return nil, fmt.Errorf("a policy needs a condition: write USING for the rows it lets through, or WITH CHECK for the rows it lets be written")
	}
	if err := checkPolicyClauses(command, using, withCheck); err != nil {
		return nil, err
	}
	roles, err := policyRoles(d, spec.Roles)
	if err != nil {
		return nil, err
	}
	kind := "PERMISSIVE"
	if spec.Restrictive {
		kind = "RESTRICTIVE"
	}
	statement := "CREATE POLICY " + name + " ON " + rel + "\n  AS " + kind + "\n  FOR " + command + "\n  TO " + roles
	var calls []string
	for _, clause := range []struct{ keyword, what, text string }{
		{"USING", "the USING condition", using},
		{"WITH CHECK", "the WITH CHECK condition", withCheck},
	} {
		if clause.text == "" {
			continue
		}
		expression, unvouched, err := policyExpression(clause.what, clause.text)
		if err != nil {
			return nil, err
		}
		statement += "\n  " + clause.keyword + " (" + expression + ")"
		calls = append(calls, unvouched...)
	}
	return planOf(statement).vouching(calls), nil
}

// PolicyChange restates what a policy applies to and checks. ALTER POLICY
// cannot change the command or whether a policy is permissive, and cannot take
// a condition away: a condition left empty here is left as it is — and so are
// the roles, unless SetRoles says they are being restated, because "no roles"
// means everybody and a change of a condition must not widen who it is for.
type PolicyChange struct {
	Schema, Table, Name string
	SetRoles            bool
	// Roles it applies to once SetRoles; empty is everybody.
	Roles     []string
	Using     string
	WithCheck string
}

// PlanAlterPolicy plans ALTER POLICY. It reads the policy's command first, so
// that a condition its command cannot take is refused here and not at the
// engine.
func PlanAlterPolicy(ctx context.Context, db *sql.DB, driver Driver, c PolicyChange) (*DDLPlan, error) {
	d, rel, err := rlsTarget(driver, c.Schema, c.Table)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Name) == "" {
		return nil, fmt.Errorf("the policy to change is required")
	}
	name, err := d.QuoteIdent(strings.TrimSpace(c.Name))
	if err != nil {
		return nil, err
	}
	using, withCheck := strings.TrimSpace(c.Using), strings.TrimSpace(c.WithCheck)
	if !c.SetRoles && using == "" && withCheck == "" {
		return nil, fmt.Errorf("say what is to change: the roles, or a condition")
	}
	var command string
	err = db.QueryRowContext(ctx, `
	  SELECT CASE p.polcmd WHEN 'r' THEN 'SELECT' WHEN 'a' THEN 'INSERT' WHEN 'w' THEN 'UPDATE'
	                       WHEN 'd' THEN 'DELETE' ELSE 'ALL' END
	  FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid JOIN pg_namespace n ON n.oid = c.relnamespace
	  WHERE `+pgSchemaIs("n")+` AND c.relname = $2 AND p.polname = $3`, c.Schema, c.Table, strings.TrimSpace(c.Name)).Scan(&command)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no policy named %s on %s", c.Name, c.Table)
	}
	if err != nil {
		return nil, planRead("the policy", err)
	}
	if err := checkPolicyClauses(command, using, withCheck); err != nil {
		return nil, err
	}
	statement := "ALTER POLICY " + name + " ON " + rel
	if c.SetRoles {
		roles, err := policyRoles(d, c.Roles)
		if err != nil {
			return nil, err
		}
		statement += "\n  TO " + roles
	}
	var calls []string
	for _, clause := range []struct{ keyword, what, text string }{
		{"USING", "the USING condition", using},
		{"WITH CHECK", "the WITH CHECK condition", withCheck},
	} {
		if clause.text == "" {
			continue
		}
		expression, unvouched, err := policyExpression(clause.what, clause.text)
		if err != nil {
			return nil, err
		}
		statement += "\n  " + clause.keyword + " (" + expression + ")"
		calls = append(calls, unvouched...)
	}
	return planOf(statement).vouching(calls), nil
}

// PlanDropPolicy plans DROP POLICY.
func PlanDropPolicy(driver Driver, schema, table, name string) (*DDLPlan, error) {
	d, rel, err := rlsTarget(driver, schema, table)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("the policy to drop is required")
	}
	q, err := d.QuoteIdent(strings.TrimSpace(name))
	if err != nil {
		return nil, err
	}
	return planOf("DROP POLICY " + q + " ON " + rel), nil
}

// --- seeing a table as another role ----------------------------------------------

const (
	// testDefaultRows and testMaxRows bound what a test reads back: it is a
	// look at what a role can see, not an export.
	testDefaultRows = 100
	testMaxRows     = 1000
	// testTimeout stops a statement the server is still running; the request's
	// own deadline is a backstop for the connection.
	testTimeout = 15 * time.Second
	// maxClaims and maxClaimValue bound the settings a test puts on the session.
	maxClaims     = 20
	maxClaimValue = 4096
)

// Claim is one setting of the session a test runs in: what a policy reads with
// current_setting, such as app.tenant_id or request.jwt.claims.
type Claim struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// claimNameRe is a setting in a namespace of its own. The dot is the point:
// set_config('role', …) is how a statement leaves the role it was put in, and
// every setting PostgreSQL defines itself is spelled without one.
var claimNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)+$`)

// RoleTest is a read to run as another role.
type RoleTest struct {
	Role   string
	Claims []Claim
	// Query is exactly one SELECT (or a WITH, TABLE or VALUES that reads).
	Query string
	// Schema and Table, when given, add what is true of that table for this
	// role to the answer: whether the filter applies to it at all.
	Schema, Table string
	MaxRows       int
}

// RoleTestResult is what the role saw.
type RoleTestResult struct {
	// Result is absent when Skipped.
	Result *QueryResult `json:"result,omitempty"`
	// Skipped is true when nothing was run: the role is a superuser.
	Skipped bool `json:"skipped"`
	// Role is the one the statement ran as, as the server reported it.
	Role string `json:"role"`
	// Setup is every statement sent before the query, in order. The query is
	// in Result.Statement.
	Setup []string `json:"setup"`
	// Notes say why the rows may not be what the policies describe: a role
	// that no policy filters, a table that is not filtered at all.
	Notes []string `json:"notes"`
}

// ErrRoleTestRefused marks a test that was refused for what it asked.
type ErrRoleTestRefused struct{ msg string }

func (e ErrRoleTestRefused) Error() string { return e.msg }

func roleTestRefused(format string, args ...any) error {
	return ErrRoleTestRefused{msg: fmt.Sprintf(format, args...)}
}

// rlsTestLeaders are the statements a test accepts: the ones that are a query
// on every engine and, classified as a read, change nothing.
var rlsTestLeaders = map[string]bool{"select": true, "with": true, "table": true, "values": true}

// CheckRoleTest validates a test without opening a connection, and returns the
// statement it will run. The classifier decides what the statement is; the
// transaction it runs in is the second line, for when the classifier is wrong.
func CheckRoleTest(driver Driver, t RoleTest) (*SQLStatement, error) {
	if driver != DriverPostgres {
		return nil, rlsRefusal(driver)
	}
	if strings.TrimSpace(t.Role) == "" {
		return nil, roleTestRefused("choose the role to run the query as")
	}
	if len(t.Claims) > maxClaims {
		return nil, roleTestRefused("a test sets at most %d settings", maxClaims)
	}
	seen := map[string]bool{}
	for _, claim := range t.Claims {
		if !claimNameRe.MatchString(claim.Name) || len(claim.Name) > 63 {
			return nil, roleTestRefused("%q is not a setting this test may set: use a name with a dot in it, such as app.tenant_id", claim.Name)
		}
		if seen[strings.ToLower(claim.Name)] {
			return nil, roleTestRefused("%s is set twice", claim.Name)
		}
		seen[strings.ToLower(claim.Name)] = true
		if len(claim.Value) > maxClaimValue {
			return nil, roleTestRefused("the value of %s is too long", claim.Name)
		}
	}
	if strings.TrimSpace(t.Query) == "" {
		return nil, roleTestRefused("write the query to run")
	}
	statement, err := SingleStatementFor(driver, t.Query)
	if err != nil {
		return nil, roleTestRefused("%v", err)
	}
	if !rlsTestLeaders[statement.leader] || !statement.returnsRows {
		return nil, roleTestRefused("only a SELECT can be tried as a role: this one starts with %s", strings.ToUpper(statement.leader))
	}
	if statement.dollar {
		return nil, roleTestRefused("a dollar-quoted string cannot be checked here: write it between single quotes")
	}
	if statement.Risk.Level != "read" {
		why := strings.Join(statement.Risk.Reasons, "; ")
		if why == "" {
			why = "it is classified " + statement.Risk.Level
		}
		return nil, roleTestRefused("that statement is not a read: %s", why)
	}
	return statement, nil
}

// RunAsRole runs a read as another role and puts everything back.
//
// The query runs in a read-only transaction that is rolled back and never
// committed, after SET LOCAL ROLE — which lasts to the end of that
// transaction and no further — and the settings, which are bound
// set_config($1, $2, true) calls made as that role, so that a setting only a
// superuser may change is refused to a test as it is to the role. Nothing is
// left on the connection the pool gets back, and a function the query calls
// that writes is refused by the transaction. The rows are bounded, and the
// server is given a timeout of its own.
//
// The account the connection signs in with has to be a member of the role, as
// SET ROLE requires; that is what keeps this from being a way to become a role
// the account could not already act as. A superuser is not become at all: no
// policy filters it, so its rows say nothing about the policies, and becoming
// one would hand its reach (pg_read_file, pg_authid) to whoever may run a test.
func RunAsRole(ctx context.Context, db *sql.DB, driver Driver, t RoleTest) (*RoleTestResult, error) {
	statement, err := CheckRoleTest(driver, t)
	if err != nil {
		return nil, err
	}
	d := postgresDialect{}
	role, err := d.QuoteIdent(strings.TrimSpace(t.Role))
	if err != nil {
		return nil, roleTestRefused("%v", err)
	}
	var super bool
	err = db.QueryRowContext(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = $1`, strings.TrimSpace(t.Role)).Scan(&super)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if super {
		return &RoleTestResult{
			Role: strings.TrimSpace(t.Role), Skipped: true, Setup: []string{},
			Notes: []string{strings.TrimSpace(t.Role) + " is a superuser, so no policy filters what it reads. Nothing was run as it."},
		}, nil
	}
	// What is sent, and how it is said on the answer: the role, then each
	// setting as the role. The settings are bound, and shown with their values
	// written in so that the answer reads as the statements it stands for.
	type step struct {
		shown, query string
		args         []any
	}
	steps := []step{
		{shown: fmt.Sprintf("SET LOCAL statement_timeout = '%dms'", testTimeout.Milliseconds())},
		{shown: "SET LOCAL ROLE " + role},
	}
	for _, claim := range t.Claims {
		value, err := ddlLiteral(driver, "the value of "+claim.Name, claim.Value)
		if err != nil {
			return nil, roleTestRefused("%v", err)
		}
		steps = append(steps, step{
			shown: "SELECT set_config('" + claim.Name + "', " + value + ", true)",
			query: "SELECT set_config($1, $2, true)", args: []any{claim.Name, claim.Value},
		})
	}
	setup := make([]string, len(steps))
	for i, st := range steps {
		setup[i] = st.shown
		if st.query == "" {
			steps[i].query = st.shown
		}
	}

	s, err := openSession(ctx, db, driver)
	if err != nil {
		return nil, err
	}
	defer s.close()
	tx, err := s.conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, st := range steps {
		rows, err := tx.QueryContext(ctx, st.query, st.args...)
		if err != nil {
			if isInsufficientPrivilege(err) && strings.HasPrefix(st.query, "SET LOCAL ROLE") {
				return nil, roleTestRefused("the account this connection signs in with may not become %s: it is not a member of that role", t.Role)
			}
			return nil, err
		}
		_ = rows.Close()
	}
	result := &RoleTestResult{Setup: setup, Notes: []string{}}
	var ranAs string
	var exempt bool
	if err := tx.QueryRowContext(ctx,
		`SELECT current_user::text, COALESCE((SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user), false)`).
		Scan(&ranAs, &exempt); err != nil {
		return nil, err
	}
	result.Role = ranAs
	if exempt {
		result.Notes = append(result.Notes, ranAs+" is a superuser or has BYPASSRLS, so no policy filters what it reads.")
	}
	if t.Table != "" {
		note, err := tableFilterNote(ctx, tx, t.Schema, t.Table, ranAs, exempt)
		if err != nil {
			return nil, err
		}
		if note != "" {
			result.Notes = append(result.Notes, note)
		}
	}
	res, err := runOn(ctx, tx, driver, statement.SQL, true, collectOptions{
		maxRows: clampRows(t.MaxRows, testDefaultRows, testMaxRows),
	})
	if err != nil {
		if ctx.Err() != nil {
			s.dirty = true
		}
		return nil, err
	}
	// A function the statement calls can leave the role it was put in. The
	// rows it returned were then read under another identity, and showing them
	// as this role's would be the one lie the test must not tell. This catches
	// a role left changed; it is not what keeps a test from doing harm, which
	// is the read-only transaction and the role being a member's.
	var after string
	if err := tx.QueryRowContext(ctx, `SELECT current_user::text`).Scan(&after); err != nil {
		return nil, err
	}
	if after != ranAs {
		return nil, roleTestRefused("the query changed the role it was running as, so its rows are not shown")
	}
	result.Result = res
	return result, nil
}

// tableFilterNote says why a table's policies may not apply to the role.
func tableFilterNote(ctx context.Context, q *sql.Tx, schema, table, role string, exempt bool) (string, error) {
	var enabled, forced, owns bool
	err := q.QueryRowContext(ctx, `
	  SELECT c.relrowsecurity, c.relforcerowsecurity, pg_has_role(current_user, c.relowner, 'USAGE')
	  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	  WHERE `+pgSchemaIs("n")+` AND c.relname = $2 AND c.relkind IN ('r', 'p')`, schema, table).
		Scan(&enabled, &forced, &owns)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	switch {
	case !enabled:
		return "Row-level security is off on " + table + ": " + role + " sees every row its privileges allow.", nil
	case owns && !forced && !exempt:
		return role + " owns " + table + " or inherits its owner's rights, and an owner is not filtered unless row-level security is forced on it.", nil
	}
	return "", nil
}

// isInsufficientPrivilege reports SQLSTATE 42501, which SET ROLE answers when
// the account is not a member of the role.
func isInsufficientPrivilege(err error) bool {
	var state interface{ SQLState() string }
	return errors.As(err, &state) && state.SQLState() == "42501"
}
