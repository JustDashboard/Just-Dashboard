package dbx

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// One question is put to a server in several spellings. When none is accepted,
// what is reported is the answer that says the account may not ask, which
// names the privilege, and not whichever came last — on MySQL 8.4 that is the
// oldest spelling's syntax error.
func TestMySQLRefusalsPreferTheDeniedAnswer(t *testing.T) {
	syntax := &mysql.MySQLError{Number: 1064, Message: "You have an error in your SQL syntax near 'SLAVE STATUS'"}
	denied := &mysql.MySQLError{Number: 1227, Message: "Access denied; you need (at least one of) the SUPER, REPLICATION CLIENT privilege(s) for this operation"}
	missing := &mysql.MySQLError{Number: 1109, Message: "Unknown table 'INNODB_LOCK_WAITS' in information_schema"}
	table := &mysql.MySQLError{Number: 1142, Message: "SELECT command denied to user 'app'@'%' for table 'data_lock_waits'"}

	var replication mysqlRefusals
	for _, err := range []error{syntax, syntax, denied, syntax} {
		replication.add(err)
	}
	if got := replication.err(); got != error(denied) {
		t.Errorf("reported %v, want the answer that names the privilege", got)
	}
	var locks mysqlRefusals
	locks.add(table)
	locks.add(missing)
	if got := locks.err(); got != error(table) {
		t.Errorf("reported %v, want the denied read of the table the server does have", got)
	}
	var unknown mysqlRefusals
	unknown.add(syntax)
	unknown.add(missing)
	if got := unknown.err(); got != error(missing) {
		t.Errorf("with nothing denied, reported %v, want the last answer", got)
	}
	if mysqlAccessDenied(errors.New("dial tcp: connection refused")) || mysqlAccessDenied(syntax) {
		t.Error("an error that is not a refusal of the account was read as one")
	}
}

// An ordinary account on a MySQL 8 whose performance_schema is on: every
// report names what the account lacks, and none says the server lacks it.
func TestLiveMySQL8ReportsBlameTheAccountNotTheServer(t *testing.T) {
	const env = "JD_TEST_MYSQL8_DSN"
	if os.Getenv(env) == "" {
		t.Skipf("set %s to an account on MySQL 8 without PROCESS or REPLICATION CLIENT", env)
	}
	db := liveSQL(t, DriverMySQL, env, "")
	ctx := t.Context()
	var grants int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM performance_schema.data_lock_waits`).Scan(&grants); err == nil {
		t.Skip("this account may read performance_schema, so nothing is refused to it")
	}
	wrong := []string{"Unknown table", "syntax", "does not offer", "is off on this server"}
	check := func(what, text string, want ...string) {
		t.Helper()
		for _, blame := range wrong {
			if strings.Contains(text, blame) {
				t.Errorf("%s blames the server: %q", what, text)
			}
		}
		for _, word := range want {
			if !strings.Contains(text, word) {
				t.Errorf("%s = %q, want it to say %q", what, text, word)
			}
		}
	}
	locks, err := ListLocks(ctx, db, DriverMySQL)
	if err != nil {
		t.Fatal(err)
	}
	check("the locks reason", locks.Reason, "denied")
	replication, err := ReadReplication(ctx, db, DriverMySQL)
	if err != nil {
		t.Fatal(err)
	}
	check("the replication notes", strings.Join(replication.Notes, " | "), "REPLICATION CLIENT")
	tables, err := ReadTableStats(ctx, db, DriverMySQL, StatsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	check("the table statistics notes", strings.Join(tables.Notes, " | "), "this account may not read performance_schema")
	indexes, err := ReadIndexStats(ctx, db, DriverMySQL, StatsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The use counts are only missed where there is an index to count for.
	if len(indexes.Indexes) > 0 {
		check("the index statistics notes", strings.Join(indexes.Notes, " | "), "this account may not read performance_schema")
	}
}
