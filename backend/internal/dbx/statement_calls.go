package dbx

import (
	"sort"
	"strings"
)

// callRule is a way for a statement that leads with SELECT to do more than
// read. The verbs in a statement are found in its raw text (see read), and the
// calls are found the same way: a name anywhere in the text counts, quoted or
// not, so no disagreement about where a quote ends can put one out of sight.
type callRule struct {
	// drivers are the engines the rule is about. None means every engine, and
	// a statement read for no engine at all (driver "") meets every rule.
	drivers []Driver
	match   func(word string) bool
	// reason takes the word that matched.
	reason string
}

func named(names ...string) func(string) bool {
	return func(word string) bool {
		for _, name := range names {
			if word == name {
				return true
			}
		}
		return false
	}
}

func prefixed(prefixes ...string) func(string) bool {
	return func(word string) bool {
		for _, prefix := range prefixes {
			if strings.HasPrefix(word, prefix) {
				return true
			}
		}
		return false
	}
}

// callRules are the calls a read-only scope does not stop. Two kinds:
//
//   - A function that is handed SQL as text and runs it. What it runs is
//     spelled in a string the caller can build by concatenation
//     ('select set_'||'config(…)'), so no reading of the statement can know it,
//     and a role changed or a function called inside it is out of sight. Such a
//     function is never a read, whatever it is given. A function's own name
//     cannot be built that way, which is why this is also the answer to a name
//     put together from pieces.
//   - A call whose effect outlives the transaction it runs in. A read runs on
//     a pooled connection that is rolled back and handed to the next request:
//     a lock, a variable or a sequence value taken there is not.
var callRules = []callRule{
	{
		drivers: []Driver{DriverPostgres},
		match:   prefixed("query_to_xml", "cursor_to_xml", "table_to_xml", "dblink", "pg_background"),
		reason:  "runs the text it is given as SQL (%s)",
	},
	{
		drivers: []Driver{DriverPostgres},
		// connectby and xpath_table write their arguments into a query of
		// their own without quoting them.
		match: named("ts_stat", "ts_rewrite", "crosstab", "crosstab2", "crosstab3", "crosstab4",
			"connectby", "xpath_table"),
		reason: "runs the text it is given as SQL (%s)",
	},
	{
		drivers: []Driver{DriverPostgres},
		match:   prefixed("pg_advisory_", "pg_try_advisory_"),
		reason:  "takes an advisory lock, which stays on the connection after the rollback (%s)",
	},
	{
		drivers: []Driver{DriverPostgres},
		// pg_stat_reset, pg_stat_statements_reset, pg_stat_reset_shared… but
		// not pg_stat_get_db_stat_reset_time, which says when one happened.
		match: func(word string) bool {
			return strings.HasPrefix(word, "pg_") && strings.Contains(word, "_reset") &&
				!strings.HasPrefix(word, "pg_stat_get_")
		},
		reason: "resets the server's statistics (%s)",
	},
	{
		drivers: []Driver{DriverPostgres},
		match: prefixed("pg_logical_emit_message", "pg_replication_origin_", "pg_replication_slot_advance",
			"pg_wal_replay_", "pg_copy_", "pg_create_", "pg_drop_", "heap_force_"),
		reason: "calls a function that acts on the server (%s)",
	},
	{
		drivers: []Driver{DriverMySQL},
		match: named("get_lock", "release_lock", "release_all_locks",
			"service_get_read_locks", "service_get_write_locks", "service_release_locks"),
		reason: "takes a named lock, which stays on the connection after the rollback (%s)",
	},
	{
		drivers: []Driver{DriverOracle},
		match:   named("dbms_xmlgen", "dbms_xmlquery", "dbms_xmlstore", "dbms_sql"),
		reason:  "runs the text it is given as SQL (%s)",
	},
	{
		drivers: []Driver{DriverOracle},
		match: named("utl_http", "utl_tcp", "utl_smtp", "utl_inaddr", "utl_file", "utl_mail",
			"httpuritype", "dbms_ldap", "dbms_pipe", "dbms_lock", "dbms_alert", "dbms_scheduler",
			"dbms_job", "dbms_java"),
		reason: "calls a package that reaches outside the database or leaves state behind (%s)",
	},
	{
		match:  named("nextval", "setval"),
		reason: "moves a sequence, which a rollback does not undo (%s)",
	},
}

// callPhrases are the calls spelled as more than one word.
var callPhrases = []struct {
	words  []string
	reason string
}{
	{[]string{"next", "value", "for"}, "advances a sequence, which a rollback does not undo (NEXT VALUE FOR)"},
	{[]string{"execute", "immediate"}, "runs a string as SQL (EXECUTE IMMEDIATE)"},
}

// callReasons is what in a statement's text a read-only scope would not stop.
// sql is the statement and code its words in order.
func callReasons(driver Driver, sql string, words *wordSet, code []string) []string {
	var reasons []string
	names := words.names()
	for _, rule := range callRules {
		if !rule.appliesTo(driver) {
			continue
		}
		for _, name := range names {
			if rule.match(name) {
				reasons = append(reasons, strings.Replace(rule.reason, "%s", name, 1))
				break
			}
		}
	}
	for _, phrase := range callPhrases {
		if containsRun(code, phrase.words) {
			reasons = append(reasons, phrase.reason)
		}
	}
	// A variable set in a SELECT stays set on the pooled connection.
	if (driver == DriverMySQL || driver == "") && strings.Contains(sql, ":=") {
		reasons = append(reasons, "assigns a variable, which stays on the connection after the rollback (:=)")
	}
	return reasons
}

func (r callRule) appliesTo(driver Driver) bool {
	if len(r.drivers) == 0 || driver == "" {
		return true
	}
	for _, d := range r.drivers {
		if d == driver {
			return true
		}
	}
	return false
}

func containsRun(code, run []string) bool {
	for i := 0; i+len(run) <= len(code); i++ {
		match := true
		for j, word := range run {
			if code[i+j] != word {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// names is every word a call could be spelled as, in a fixed order so the
// reason given for a statement does not change between runs. A run that
// starts with a digit also offers what follows its digits: `1dblink_exec` is
// a number and a name to an engine that stops a number at the first letter.
func (w *wordSet) names() []string {
	out := make([]string, 0, len(w.seen))
	for word := range w.seen {
		out = append(out, word)
	}
	for _, run := range w.glued {
		for i := 1; i < len(run); i++ {
			if isDigit(run[i-1]) && !isDigit(run[i]) {
				out = append(out, run[i:])
			}
		}
	}
	sort.Strings(out)
	return out
}
