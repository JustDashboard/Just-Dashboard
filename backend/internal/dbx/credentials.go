package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// The engines' own credential catalogues.
//
// The grid, the export, the cell read and the search take any schema and any
// table and are open to every role, and the connection they read through is
// usually an administrator's: the account this dashboard makes on a host
// server is one by default, and a container is adopted with its image's root
// account. Pointed at pg_catalog.pg_authid or mysql.user, they handed every
// account's password verifier to whoever may look at a table. The same is
// withheld everywhere else here — a role listing never selects the hash, a
// setting that holds a password is blanked, MongoDB's system.users is refused
// (mongoGuardCredentials) — and this is that refusal for the SQL engines.
//
// It is a list of names, and guards the forms that read a table by name. A
// statement in the console is the operator's own, needs a capability a
// read-only role does not have, and is not judged here.

// ErrCredentialsWithheld is a request to read a relation that holds what
// accounts sign in with.
var ErrCredentialsWithheld = errors.New("it holds what accounts sign in with and is not read from here; accounts are listed, without that, under Access")

// credentialRelations are those relations by engine, each under the schemas
// it can be named in, all in lower case.
var credentialRelations = map[Driver]map[string][]string{
	DriverPostgres: {
		// Password verifiers, and the view over them.
		"pg_authid": {"pg_catalog"},
		"pg_shadow": {"pg_catalog"},
		// A foreign server's or a subscription's password, as its options or
		// its connection string carry it.
		"pg_user_mapping":      {"pg_catalog"},
		"pg_user_mappings":     {"pg_catalog"},
		"pg_subscription":      {"pg_catalog"},
		"_pg_user_mappings":    {"information_schema"},
		"user_mapping_options": {"information_schema"},
		// Settings as the server holds them. A value can be a secret — a
		// replica's primary_conninfo, a module's own key — and the Settings
		// page blanks those for every role but an administrator; read as a
		// table they would come back whole.
		"pg_settings":        {"pg_catalog"},
		"pg_file_settings":   {"pg_catalog"},
		"pg_db_role_setting": {"pg_catalog"},
	},
	DriverMySQL: {
		// authentication_string, and where MariaDB keeps it.
		"user":        {"mysql"},
		"global_priv": {"mysql"},
		// Verifiers an account used before, and the passwords of a federated
		// server and of the source a replica signs in to.
		"password_history":  {"mysql"},
		"servers":           {"mysql"},
		"slave_master_info": {"mysql"},
		// The server's variables, for the reason pg_settings is here.
		"global_variables":    {"performance_schema"},
		"session_variables":   {"performance_schema"},
		"persisted_variables": {"performance_schema"},
	},
	DriverMSSQL: {
		"sql_logins": {"sys"},
		// The compatibility view, which answers under either schema.
		"syslogins": {"sys", "dbo"},
	},
	DriverOracle: {
		// The verifiers, their history, a database link's password, and the
		// views that carry the verifier on older releases.
		"user$":         {"sys"},
		"user_history$": {"sys"},
		"link$":         {"sys"},
		"dba_users":     {"sys", "public"},
		"ku$_user_view": {"sys", "public"},
	},
}

// implicitSchemas are the schemas an engine resolves a name in when the
// statement gives none, whatever the session's own schema is: pg_catalog is
// searched before the search path, SQL Server finds a system view from any
// schema, and Oracle falls back to a public synonym.
var implicitSchemas = map[Driver][]string{
	DriverPostgres: {"pg_catalog"},
	DriverMSSQL:    {"sys"},
	DriverOracle:   {"sys", "public"},
}

// guardCredentials refuses a read of one of those relations. Names are
// compared without case: an engine that folds them reaches the relation under
// any spelling, and on one that does not the other spellings name nothing.
//
// A request that names no schema is resolved the way the engine will resolve
// it: against the schemas it searches implicitly, and then against the
// session's own, which is asked for only when the table's name is on the list
// — a MySQL connection on the mysql database reads its user table with no
// schema named, and an application's table called user is nobody's secret.
func guardCredentials(ctx context.Context, db *sql.DB, d Dialect, schema, table string) error {
	within := credentialRelations[d.Driver()][strings.ToLower(strings.TrimSpace(table))]
	if len(within) == 0 {
		return nil
	}
	candidates := []string{schema}
	if strings.TrimSpace(schema) == "" {
		candidates = append(append([]string{}, implicitSchemas[d.Driver()]...), catalogSchema(ctx, db, d, ""))
	}
	for _, candidate := range candidates {
		for _, held := range within {
			if strings.EqualFold(strings.TrimSpace(candidate), held) {
				return fmt.Errorf("%s.%s: %w", held, table, ErrCredentialsWithheld)
			}
		}
	}
	return nil
}
