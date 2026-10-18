package dbx

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/go-sql-driver/mysql"
	mssql "github.com/microsoft/go-mssqldb"
)

// StatementShape is a statement's text with every value replaced by a ?, for
// a record of what ran that must not keep what it ran with: an AI client's
// statement, written to the audit log as its shape, by the rule the statement
// list is held to — a literal is somebody's e-mail address.
//
// It reads the text with the query runner's own lexer for the engine, so what
// is a string here is what the runner and the engine took for one: a string of
// any kind, a dollar-quoted body and a number each become one ?, and a quoted
// name is kept, except a "…" where it can be a string and so is masked:
// MySQL's, which is one unless the server says otherwise, and SQLite's, which
// is one wherever no column has that name. Comments are left out, since nothing the lexer
// skips can be said to be structure, and every run of space becomes one.
// Text the lexer refuses gives "": the runner refused it too, and a guess at
// its shape is the one answer that could keep a value.
func StatementShape(driver Driver, text string) string {
	tokens, err := lexSQL(lexRulesFor(driver), text)
	if err != nil {
		return ""
	}
	var b strings.Builder
	b.Grow(len(text))
	for i, t := range tokens {
		if i > 0 && t.start > tokens[i-1].end {
			b.WriteByte(' ')
		}
		if shapeMasks(driver, text, t) {
			b.WriteByte('?')
			continue
		}
		b.WriteString(text[t.start:t.end])
	}
	return clipStatementText(b.String(), 1000)
}

func shapeMasks(driver Driver, text string, t sqlToken) bool {
	switch t.kind {
	case tokNumber, tokDollar:
		return true
	case tokQuoted:
		return text[t.start] == '\'' || text[t.start] == '"' && (driver == DriverMySQL || driver == DriverSQLite)
	}
	return false
}

// ErrorCode names an engine's error by the code the engine gave it, for the
// same record. An engine's message quotes what it was sent — MySQL repeats the
// statement from where it stopped reading, PostgreSQL the input it could not
// take, SQL Server a duplicate key's value — and its code says what went wrong
// without saying with what. An error no engine sent is "".
func ErrorCode(err error) string {
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		return "SQLSTATE " + state.SQLState()
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		return fmt.Sprintf("MySQL %d", my.Number)
	}
	var ms mssql.Error
	if errors.As(err, &ms) {
		return fmt.Sprintf("SQL Server %d", ms.Number)
	}
	var ch *clickhouse.Exception
	if errors.As(err, &ch) {
		return fmt.Sprintf("ClickHouse %d", ch.Code)
	}
	// SQLite's error carries its result code; go-ora's only prints one.
	var lite interface{ Code() int }
	if errors.As(err, &lite) {
		return fmt.Sprintf("SQLite %d", lite.Code())
	}
	return oracleCode.FindString(err.Error())
}
