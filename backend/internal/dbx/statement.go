package dbx

import (
	"fmt"
	"strings"
)

// SQLStatement is one statement of the text an operator submitted, as the runner
// will send it to the engine: from its first token to its last, without the
// comments around it. What is not sent cannot run, so a comment in front of a
// statement never has to be argued about.
type SQLStatement struct {
	SQL string `json:"sql"`
	// Line is where the statement starts in the submitted text, counted from 1,
	// so a failing statement in a script can be pointed at.
	Line int  `json:"line"`
	Risk Risk `json:"risk"`

	leader      string
	returnsRows bool
	dollar      bool
}

// Risk classifies a statement so the UI can warn before it runs.
type Risk struct {
	Destructive bool     `json:"destructive"`
	Level       string   `json:"level"`
	Reasons     []string `json:"reasons"`
}

// maxScriptStatements bounds one request. A script is typed or pasted by a
// person; a file of ten thousand statements is a restore, which has its own
// route and its own budget.
const maxScriptStatements = 500

// ParseScript splits a query into its statements for an engine and classifies
// each one. Anything the lexer cannot attribute is an error rather than a
// guess, and callers treat that error as a refusal.
func ParseScript(driver Driver, query string) ([]SQLStatement, error) {
	tokens, err := lexSQL(lexRulesFor(driver), query)
	if err != nil {
		return nil, err
	}
	var out []SQLStatement
	line, counted := 1, 0
	flush := func(part []sqlToken) error {
		if len(part) == 0 {
			return nil
		}
		if len(out) >= maxScriptStatements {
			return fmt.Errorf("a script may hold at most %d statements", maxScriptStatements)
		}
		start, end := part[0].start, part[len(part)-1].end
		line += strings.Count(query[counted:start], "\n")
		counted = start
		st := SQLStatement{SQL: query[start:end], Line: line}
		// Offsets become relative to the statement, which is the text every
		// later question is asked of.
		rel := make([]sqlToken, len(part))
		for i, t := range part {
			rel[i] = sqlToken{t.kind, t.start - start, t.end - start}
		}
		st.read(driver, rel)
		out = append(out, st)
		return nil
	}
	from := 0
	for i, t := range tokens {
		if t.kind != tokSemicolon {
			continue
		}
		if err := flush(tokens[from:i]); err != nil {
			return nil, err
		}
		from = i + 1
	}
	if err := flush(tokens[from:]); err != nil {
		return nil, err
	}
	return out, nil
}

// SingleStatementFor returns the one statement a query holds, or refuses.
func SingleStatementFor(driver Driver, query string) (*SQLStatement, error) {
	statements, err := ParseScript(driver, query)
	if err != nil {
		return nil, err
	}
	if len(statements) != 1 {
		return nil, fmt.Errorf("submit exactly one SQL statement at a time")
	}
	return &statements[0], nil
}

// SingleStatement is SingleStatementFor with no engine named, which reads the
// text by the strictest rules.
func SingleStatement(query string) (string, error) {
	st, err := SingleStatementFor("", query)
	if err != nil {
		return "", err
	}
	return st.SQL, nil
}

// explainLeaders are the statements a plan can be asked for. EXPLAIN options
// never come from the caller: `(ANALYZE) DELETE …` leads with a word that is
// not in this list, so a plan request cannot be turned into an execution.
var explainLeaders = map[string]bool{
	"select": true, "with": true, "insert": true, "update": true,
	"delete": true, "merge": true, "values": true, "table": true,
}

func explainStatement(driver Driver, query string) (*SQLStatement, error) {
	st, err := SingleStatementFor(driver, query)
	if err != nil {
		return nil, err
	}
	if st.dollar || !explainLeaders[st.leader] {
		return nil, fmt.Errorf("this statement cannot be explained safely")
	}
	return st, nil
}

// ExplainStatement accepts a statement, never caller-provided EXPLAIN options.
// Keeping ANALYZE outside this grammar prevents a read-only plan from
// executing it.
func ExplainStatement(query string) (string, error) {
	st, err := explainStatement("", query)
	if err != nil {
		return "", err
	}
	return st.SQL, nil
}

// ClassifyFor classifies everything in a query for an engine and keeps the
// strongest verdict. A query the lexer refuses is destructive: the capability
// check is derived from this answer, so the unknown has to cost the most.
func ClassifyFor(driver Driver, query string) Risk {
	statements, err := ParseScript(driver, query)
	if err != nil {
		return Risk{Level: "high", Destructive: true, Reasons: []string{err.Error()}}
	}
	return WorstRisk(statements)
}

// Classify is ClassifyFor with no engine named.
func Classify(query string) Risk { return ClassifyFor("", query) }

// WorstRisk folds the statements of a script into the one verdict its
// capability check uses.
func WorstRisk(statements []SQLStatement) Risk {
	result := Risk{Level: "read", Reasons: []string{}}
	seen := map[string]bool{}
	for _, st := range statements {
		if rank(st.Risk.Level) > rank(result.Level) {
			result.Level = st.Risk.Level
		}
		for _, reason := range st.Risk.Reasons {
			if !seen[reason] {
				seen[reason] = true
				result.Reasons = append(result.Reasons, reason)
			}
		}
	}
	result.Destructive = result.Level == "high" || result.Level == "critical"
	return result
}

func rank(level string) int {
	switch level {
	case "critical":
		return 3
	case "high":
		return 2
	case "medium":
		return 1
	default:
		return 0
	}
}

// readOnlyLeaders are the statement forms that cannot change anything. PRAGMA
// is absent on purpose — `PRAGMA journal_mode=WAL` writes.
var readOnlyLeaders = map[string]bool{
	"select": true, "with": true, "show": true, "describe": true,
	"desc": true, "explain": true, "table": true, "values": true,
}

// rowLeaders are the statements whose answer is a result set on every engine.
var rowLeaders = map[string]bool{
	"select": true, "with": true, "show": true, "describe": true, "desc": true,
	"explain": true, "table": true, "values": true, "pragma": true,
}

// engineRowLeaders are the ones that answer in rows on one engine only: a
// MySQL CALL or maintenance command, a SQL Server EXEC, a ClickHouse EXISTS.
// Run through Exec their rows are thrown away. They are per engine because the
// same word elsewhere returns nothing, and not every driver takes a statement
// with no result set through its query path.
var engineRowLeaders = map[Driver]map[string]bool{
	DriverMySQL: {
		"call": true, "check": true, "checksum": true, "analyze": true,
		"optimize": true, "repair": true, "help": true,
	},
	DriverMSSQL:      {"exec": true, "execute": true},
	DriverClickHouse: {"exists": true, "check": true},
	DriverPostgres:   {"fetch": true},
}

// batchWords are the words that start a second statement on SQL Server, which
// needs no separator between two: `SELECT 1 EXEC xp_cmdshell …` is one request
// and two statements. A leading SELECT says nothing about what follows it
// there, so any of these anywhere in the text ends the statement's claim to be
// a read.
var batchWords = []string{
	"exec", "execute", "set", "declare", "begin", "commit", "rollback", "save",
	"use", "kill", "backup", "restore", "dbcc", "shutdown", "reconfigure",
	"bulk", "openrowset", "opendatasource", "openquery", "waitfor", "raiserror",
	"throw", "checkpoint", "deny", "disable", "enable", "revert", "setuser",
	"writetext", "updatetext", "readtext", "goto", "while", "print", "call",
	"load", "dump",
}

// serverFunctions are the PostgreSQL functions that act on the server rather
// than return something about it: end another session, write a file, run a
// statement over a second connection. A SELECT is all it takes to call one, the
// read-only transaction does not stop them, and the routes that do the same
// things by name are in the destructive group — so calling one from the
// console costs what those routes cost.
var serverFunctions = []string{
	"pg_terminate_backend", "pg_cancel_backend", "pg_reload_conf", "pg_rotate_logfile",
	"pg_promote", "pg_switch_wal", "pg_create_restore_point",
	"pg_backup_start", "pg_backup_stop", "pg_start_backup", "pg_stop_backup",
	"pg_drop_replication_slot", "pg_create_physical_replication_slot",
	"pg_create_logical_replication_slot", "pg_replication_origin_drop",
	"lo_import", "lo_export", "lo_unlink", "pg_file_write", "pg_file_unlink",
	"pg_file_rename", "dblink", "dblink_exec", "dblink_connect", "set_config",
}

// read fills in everything the runner asks of a statement.
//
// Two different readings are used on purpose. The *tokens* — the lexer's view
// of what is code — decide the shape: the leading word, whether rows come
// back, whether there is a WHERE. The *verbs* are looked for in the raw text
// instead, quoted text and comments included. That over-reports: a SELECT whose
// comment says "delete" costs the operator one extra confirmation. It is also
// the only reading that cannot be argued out of a verdict, because a verb the
// engine executes has to be spelled somewhere in the text it was sent, and
// every byte of that text is looked at whatever this lexer made of the quotes.
func (st *SQLStatement) read(driver Driver, tokens []sqlToken) {
	keywords := map[string]bool{}
	// code is the statement's words in order, with the comments between them
	// gone — which is what two words being next to each other has to mean.
	var code []string
	// Leading parentheses do not hide the verb: (SELECT 1) UNION … leads with
	// SELECT. Anything else in front of the first word means there is no
	// leading verb to trust.
	leading := true
	for _, t := range tokens {
		switch {
		case t.kind == tokWord:
			word := strings.ToLower(st.SQL[t.start:t.end])
			keywords[word] = true
			code = append(code, word)
			if leading {
				st.leader, leading = word, false
			}
		case t.kind == tokPunct && st.SQL[t.start] == '(':
		default:
			st.dollar = st.dollar || t.kind == tokDollar
			leading = false
		}
	}

	writes := keywords["insert"] || keywords["update"] || keywords["delete"] || keywords["merge"]
	st.returnsRows = rowLeaders[st.leader] || engineRowLeaders[driver][st.leader] ||
		keywords["returning"] || driver == DriverMSSQL && keywords["output"]
	if st.leader == "with" && writes && !keywords["returning"] && !keywords["output"] {
		st.returnsRows = false
	}

	words := rawWords(st.SQL)
	risk := Risk{Level: "read", Reasons: []string{}}
	add := func(level, reason string) {
		risk.Reasons = append(risk.Reasons, reason)
		if rank(level) > rank(risk.Level) {
			risk.Level = level
		}
	}
	// Deliberately "any DROP" rather than a list of object types: the list
	// omitted ROLE, OWNED, FUNCTION and everything a future dialect adds, and
	// each omission was a statement that ran without confirmation.
	if words.has("drop") {
		add("critical", "drops a database object")
	}
	if words.has("truncate") {
		add("critical", "truncates a table")
	}
	if words.has("copy") && words.has("program") {
		add("critical", "runs a shell command on the database host")
	}
	// The WHERE that scopes a statement has to be code. One that only appears
	// in a comment or a string scopes nothing.
	if words.has("delete") && !keywords["where"] {
		add("critical", "deletes every row (no WHERE clause)")
	}
	if words.has("update") && words.has("set") && !keywords["where"] {
		add("critical", "updates every row (no WHERE clause)")
	}
	if words.has("delete") {
		add("high", "deletes rows")
	}
	if words.has("update") {
		add("high", "updates rows")
	}
	if words.has("alter") {
		add("high", "alters a database object")
	}
	if words.has("grant") || words.has("revoke") {
		add("high", "changes permissions")
	}
	if words.has("merge") {
		add("high", "merges rows")
	}
	if words.has("refresh") {
		add("high", "refreshes a materialized view")
	}
	if words.has("outfile") || words.has("dumpfile") {
		add("high", "writes a file on the database host")
	}
	if st.dollar {
		add("high", "contains a dollar-quoted body, which runs whatever is written inside it")
	}
	if driver == DriverMSSQL {
		for _, word := range batchWords {
			if words.has(word) {
				add("high", "SQL Server runs statements with no separator between them, and this text contains "+strings.ToUpper(word))
				break
			}
		}
	}
	if driver == DriverPostgres {
		for _, name := range serverFunctions {
			if words.has(name) {
				add("high", "calls a function that acts on the server ("+name+")")
				break
			}
		}
	}
	// REPLACE INTO and INSERT OR REPLACE delete the row that was in the way,
	// and CREATE OR REPLACE discards the definition that was there — on
	// MariaDB, the table. REPLACE on its own is a string function, so it is
	// the pair of words that counts.
	replaces, account := false, false
	for i := 0; i+1 < len(code); i++ {
		switch {
		case code[i] == "replace" && code[i+1] == "into", code[i] == "or" && code[i+1] == "replace":
			replaces = true
		case code[i] == "create":
			switch code[i+1] {
			case "role", "user", "login", "group":
				account = true
			}
		}
	}
	if replaces {
		add("high", "replaces what is already there")
	}
	if account {
		add("high", "creates a database account")
	}
	if words.has("insert") {
		add("medium", "inserts rows")
	} else if words.has("into") && !words.has("merge") && !replaces {
		add("medium", "stores its result (INTO)")
	}
	if words.has("create") {
		add("medium", "creates a database object")
	}
	// Fail closed. An unrecognised statement — DO, CALL, VACUUM, whatever the
	// next dialect adds — used to be indistinguishable from a SELECT, and the
	// query runner derives its capability check from this verdict. Costing the
	// operator a confirmation for a statement nobody enumerated is the right
	// side to be wrong on.
	if risk.Level == "read" && !readOnlyLeaders[st.leader] {
		add("high", "statement is not a recognised read")
	}
	risk.Destructive = risk.Level == "critical" || risk.Level == "high"
	st.Risk = risk
}

// wordSet is every word in a statement's raw text.
type wordSet struct {
	seen map[string]bool
	// glued holds the runs that start with a digit. `1DELETE` is a number and
	// a keyword to SQL Server and an identifier to MySQL, so a verb anywhere
	// inside one counts.
	glued []string
}

func (w *wordSet) has(word string) bool {
	if w.seen[word] {
		return true
	}
	for _, run := range w.glued {
		if strings.Contains(run, word) {
			return true
		}
	}
	return false
}

// rawWords splits text into runs of ASCII letters, digits and underscores.
// Every other byte separates — including every byte above ASCII, which is a
// letter to PostgreSQL and whitespace to ClickHouse and SQL Server. Splitting
// there can only find a verb an engine would not have seen as one.
func rawWords(text string) *wordSet {
	w := &wordSet{seen: map[string]bool{}}
	isWord := func(c byte) bool {
		return c == '_' || isDigit(c) || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	}
	for i := 0; i < len(text); {
		if !isWord(text[i]) {
			i++
			continue
		}
		j := i
		for j < len(text) && isWord(text[j]) {
			j++
		}
		run := strings.ToLower(text[i:j])
		if isDigit(run[0]) {
			w.glued = append(w.glued, run)
		} else {
			w.seen[run] = true
		}
		i = j
	}
	return w
}

// returnsRows decides between Query and Exec for a statement this package
// generated itself, where the engine is not at hand. A statement the strict
// lexer refuses — a quoted name with a backslash in it — is judged on its
// first word, which for generated SQL is always the verb.
func returnsRows(query string) bool {
	if st, err := SingleStatementFor("", query); err == nil {
		return st.returnsRows
	}
	word := strings.ToLower(strings.TrimLeft(query, " \t\r\n("))
	if i := strings.IndexAny(word, " \t\r\n("); i >= 0 {
		word = word[:i]
	}
	return rowLeaders[word]
}
