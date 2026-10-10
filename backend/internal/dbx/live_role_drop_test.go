package dbx

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
)

// An account's life from the dashboard's own forms: made, granted a database,
// dropped. The last step has to work on what the middle one left behind.

func TestLivePostgresDropsARoleItsOwnGrantWasGivenTo(t *testing.T) {
	const env = "JD_TEST_POSTGRES_DSN"
	if os.Getenv(env) == "" {
		t.Skipf("set %s to run this", env)
	}
	db := liveSQL(t, DriverPostgres, env, "")
	ctx := context.Background()
	var mayCreate bool
	if err := db.QueryRowContext(ctx, `SELECT rolsuper OR rolcreaterole FROM pg_roles WHERE rolname = current_user`).Scan(&mayCreate); err != nil || !mayCreate {
		t.Skipf("this login may not make roles (%v)", err)
	}
	admin, _ := AdminFor(DriverPostgres)
	var database string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	exists := func(role string) bool {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_roles WHERE rolname = $1`, role).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	cleanup := func() {
		db.Exec(`DROP TABLE IF EXISTS jd_int_drop_owned`)
		for _, role := range []string{"jd_int_drop_granted", "jd_int_drop_owner"} {
			db.Exec(`DROP OWNED BY ` + role)
			db.Exec(`DROP ROLE IF EXISTS ` + role)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	// Granted through the dashboard's own three levels, default privileges
	// and all: DROP ROLE alone refuses this role.
	spec := RoleSpec{Name: "jd_int_drop_granted", Password: "Jd-int-drop#2026", SetPassword: true, Login: true}
	if err := admin.CreateRole(ctx, db, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Grant(ctx, db, DatabaseGrant{Role: spec.Name, Database: database, Level: GrantWrite}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP ROLE jd_int_drop_granted`); err == nil {
		t.Fatal("the grant left nothing a plain DROP ROLE objects to, so this test proves nothing")
	}
	if err := admin.DropRole(ctx, db, spec.Name, ""); err != nil {
		t.Fatalf("dropping a role the dashboard granted = %v", err)
	}
	if exists(spec.Name) {
		t.Error("the role is still there")
	}

	// A role that owns a table is not dropped, and neither is its table: the
	// engine's refusal comes back as the request's to deal with.
	owner := RoleSpec{Name: "jd_int_drop_owner", Password: "Jd-int-drop#2026", SetPassword: true, Login: true}
	if err := admin.CreateRole(ctx, db, owner); err != nil {
		t.Fatal(err)
	}
	execAll(t, db, `CREATE TABLE jd_int_drop_owned (id int)`, `ALTER TABLE jd_int_drop_owned OWNER TO jd_int_drop_owner`)
	err := admin.DropRole(ctx, db, owner.Name, "")
	var inUse ErrRoleInUse
	if !errors.As(err, &inUse) {
		t.Fatalf("dropping a role that owns a table = %v, want the engine's refusal as a role in use", err)
	}
	var kept sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('jd_int_drop_owned')::text`).Scan(&kept); err != nil || !kept.Valid {
		t.Errorf("the table the role owned is gone (%v): dropping an account dropped its data", err)
	}
	if !exists(owner.Name) {
		t.Error("the role that owns a table was dropped")
	}
}

func TestLiveSQLServerDropsALoginWithItsDatabaseUsers(t *testing.T) {
	db, _ := liveOwnMSSQL(t, "jd_drop_login")
	ctx := context.Background()
	admin, _ := AdminFor(DriverMSSQL)
	const login = "jd_int_drop_login"
	cleanup := func() {
		db.Exec(`IF SCHEMA_ID('jd_int_drop_schema') IS NOT NULL DROP SCHEMA jd_int_drop_schema`)
		db.Exec(`IF USER_ID('` + login + `') IS NOT NULL DROP USER [` + login + `]`)
		db.Exec(`IF SUSER_ID('` + login + `') IS NOT NULL DROP LOGIN [` + login + `]`)
	}
	cleanup()
	t.Cleanup(cleanup)
	users := func() int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sys.database_principals WHERE name = @p1`, login).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	logins := func() int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sys.server_principals WHERE name = @p1`, login).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	make := func() {
		t.Helper()
		if err := admin.CreateRole(ctx, db, RoleSpec{Name: login, Password: "Jd-int-drop#2026", SetPassword: true, Login: true}); err != nil {
			t.Fatal(err)
		}
		// The grant form makes the login a user in the database.
		if _, err := admin.Grant(ctx, db, DatabaseGrant{Role: login, Database: "jd_drop_login", Level: GrantRead}); err != nil {
			t.Fatal(err)
		}
		if users() != 1 {
			t.Fatal("the grant made no database user, so this test proves nothing")
		}
	}

	make()
	if err := admin.DropRole(ctx, db, login, ""); err != nil {
		t.Fatalf("drop = %v", err)
	}
	if logins() != 0 || users() != 0 {
		t.Errorf("after the drop: %d logins and %d database users of that name, want neither", logins(), users())
	}

	// A user that owns a schema cannot be dropped; the login is then kept
	// rather than left without its user.
	make()
	execAll(t, db, `CREATE SCHEMA jd_int_drop_schema AUTHORIZATION [`+login+`]`)
	err := admin.DropRole(ctx, db, login, "")
	var inUse ErrRoleInUse
	if !errors.As(err, &inUse) {
		t.Fatalf("dropping a login whose user owns a schema = %v, want it refused as in use", err)
	}
	if logins() != 1 || users() != 1 {
		t.Errorf("after a refused drop: %d logins and %d users, want both kept", logins(), users())
	}
}
