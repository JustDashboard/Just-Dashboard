package api

import (
	"strings"
	"testing"
)

// The account the host route makes is a superuser by default, and its
// password used to be an argument of the client that made it, where every
// account on the machine can read it in /proc/<pid>/cmdline while the client
// runs. No argument may carry it now.
func TestHostAccountPasswordsStayOutOfTheClientsArguments(t *testing.T) {
	const password = "S3cret-sentinel-Password"
	inArgs := func(name string, args []string) {
		t.Helper()
		for _, arg := range args {
			if strings.Contains(arg, password) {
				t.Errorf("%s: an argument carries the password: %q", name, arg)
			}
		}
	}

	args, input, err := postgresAccountInvocation(5432, "just_dashboard", password, true)
	if err != nil {
		t.Fatal(err)
	}
	inArgs("psql", args)
	if !strings.Contains(input, sqlLiteral(password)) {
		t.Errorf("psql: the password is not in what it reads: %q", input)
	}

	args, input, err = mysqlAccountInvocation("/run/mysqld/mysqld.sock", "just_dashboard", password, true)
	if err != nil {
		t.Fatal(err)
	}
	inArgs("mysql", args)
	for _, arg := range args {
		if arg == "--execute" || arg == "-e" || strings.HasPrefix(arg, "--execute=") {
			t.Errorf("mysql: the statements are an argument again: %v", args)
		}
	}
	if strings.Count(input, mysqlLiteral(password)) != 4 {
		t.Errorf("mysql: the password is not in every statement it reads: %q", input)
	}

	args, env := mongoAccountInvocation("mongosh", 27017, "just_dashboard", password, true)
	inArgs("mongosh", args)
	if len(env) != 1 || env[0] != mongoAccountPasswordVariable+"="+password {
		t.Errorf("mongosh: the password is not handed over in its environment: %v", env)
	}
	if script := args[len(args)-1]; !strings.Contains(script, "process.env."+mongoAccountPasswordVariable) {
		t.Errorf("mongosh: the script does not read the password from its environment: %s", script)
	}

	stmts := clickHouseAccountStatements("just_dashboard", password, true)
	if len(stmts) != 3 || !strings.Contains(stmts[0], mysqlLiteral(password)) {
		t.Errorf("clickhouse: the statements it reads are %v", stmts)
	}
}

// psql and mysql read their standard input as a script, in which a
// backslash outside a quoted string is one of the client's own commands and
// \! runs a shell. A hostile password must reach them inside its literal, on
// the statement's one line, or not at all.
func TestAHostilePasswordReachesTheClientOnlyAsALiteral(t *testing.T) {
	// A line break ends the line the client is reading, so what follows it
	// would be a line of its own: refused before anything runs.
	for _, password := range []string{"it's\n\\! touch /tmp/x", "a\r\\! id", "tab\there", "nul\x00"} {
		if _, _, err := postgresAccountInvocation(5432, "just_dashboard", password, true); err == nil {
			t.Errorf("psql was handed a password with a control character: %q", password)
		}
		if _, _, err := mysqlAccountInvocation("", "just_dashboard", password, true); err == nil {
			t.Errorf("mysql was handed a password with a control character: %q", password)
		}
	}

	hostile := []string{
		`it's \! touch /tmp/x \`,
		`'; \! touch /tmp/x; '`,
		`\\\'\"`,
		`$jd$ \! id $$ :name :'name'`,
		"`; system id; `",
		`trailing\`,
	}
	for _, password := range hostile {
		_, input, err := postgresAccountInvocation(5432, "just_dashboard", password, false)
		if err != nil {
			t.Fatalf("%q: %v", password, err)
		}
		if strings.Count(input, "\n") != 1 || !strings.HasSuffix(input, "\n") {
			t.Errorf("psql %q: the script is not one line: %q", password, input)
		}
		// Everything psql could read as a command or a variable lies outside
		// the dollar quote; the password lies inside it.
		open := strings.Index(input, "$jd")
		tag := input[open : strings.Index(input[open+1:], "$")+open+2]
		first, last := strings.Index(input, tag), strings.LastIndex(input, tag)
		if first == last {
			t.Fatalf("psql %q: the block is not closed: %q", password, input)
		}
		outside, inside := input[:first]+input[last+len(tag):], input[first+len(tag):last]
		if outside != "DO ;\n" {
			t.Errorf("psql %q: outside the dollar quote psql reads %q", password, outside)
		}
		if !strings.Contains(inside, sqlLiteral(password)) || strings.Contains(inside, tag) {
			t.Errorf("psql %q: the password is not inside the quote: %q", password, inside)
		}

		_, input, err = mysqlAccountInvocation("", "just_dashboard", password, true)
		if err != nil {
			t.Fatalf("%q: %v", password, err)
		}
		if strings.Count(input, "\n") != 1 || !strings.HasPrefix(input, "CREATE USER ") {
			t.Errorf("mysql %q: the statements are not one line starting with CREATE: %q", password, input)
		}
		if commands := mysqlClientCommands(input); len(commands) > 0 {
			t.Errorf("mysql %q: the client would read %q as its own commands in %q", password, commands, input)
		}
	}

	// What reaches psql for the first of them, in full.
	_, input, err := postgresAccountInvocation(5432, "just_dashboard", hostile[0], false)
	if err != nil {
		t.Fatal(err)
	}
	open := strings.Index(input, "$jd")
	tag := input[open : strings.Index(input[open+1:], "$")+open+2]
	want := "DO " + tag + " BEGIN IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'just_dashboard') THEN " +
		`EXECUTE format('ALTER ROLE %I WITH LOGIN PASSWORD %L', 'just_dashboard', 'it''s \! touch /tmp/x \'); ` +
		`ELSE EXECUTE format('CREATE ROLE %I WITH LOGIN PASSWORD %L', 'just_dashboard', 'it''s \! touch /tmp/x \'); ` +
		"END IF; END " + tag + ";\n"
	if input != want {
		t.Errorf("psql reads\n%q\nwant\n%q", input, want)
	}
}

// mysqlClientCommands is the mysql client's own reading of a line, as far as
// this needs it: outside a quoted string a backslash and the character after
// it are a command; inside one, the client passes a backslash and the next
// character through as part of the string.
func mysqlClientCommands(line string) []string {
	var commands []string
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote == 0 && (c == '\'' || c == '"' || c == '`'):
			quote = c
		case quote == 0 && c == '\\':
			commands = append(commands, line[i:min(i+2, len(line))])
			i++
		case quote != 0 && c == '\\':
			i++
		case quote != 0 && c == quote:
			quote = 0
		}
	}
	return commands
}

// The account names reach the statements unquoted by the server, so the
// statement builders hold the route's rule for them as well.
func TestHostAccountStatementsRefuseAnAccountThatIsNotAName(t *testing.T) {
	for _, account := range []string{"", "a'b", "x' OR 1", "1abc", strings.Repeat("a", 64)} {
		if _, _, err := postgresAccountInvocation(5432, account, "pw", false); err == nil {
			t.Errorf("psql accepted the account %q", account)
		}
		if _, _, err := mysqlAccountInvocation("", account, "pw", false); err == nil {
			t.Errorf("mysql accepted the account %q", account)
		}
	}
}
