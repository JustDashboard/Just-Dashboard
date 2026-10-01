package dbx

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/stdlib"
)

// Replaying a SQL script something else wrote: a plain-format pg_dump, a
// mysqldump, a file exported from another tool and uploaded.
//
// Such a script used to be handed to the engine's own client on standard
// input, and a client does more with a script than send it. psql runs
// `\! command` in a shell and mysql runs `system command`; both read other
// files and both reconnect elsewhere. A dump is a file somebody put on the
// server, so restoring one was a way to run a command on this machine — the
// request-defined shell invariant 6 says there is exactly one of. Reading the
// script first does not close that: where a string ends, and so where a command
// could begin, depends on settings the script itself changes as it runs
// (standard_conforming_strings, NO_BACKSLASH_ESCAPES), so a reader and the
// client can be made to disagree.
//
// So the script is read here and its statements go over the connection the
// dashboard already has. Nothing but the server interprets them. What only a
// client could do is refused by name, and a reader that disagrees with the
// server about where a statement ends gets a syntax error from the server and
// nothing else.

// maxScriptStatement bounds one statement of a replayed script. It is what
// MySQL takes in one packet by default; a script whose single statement is
// larger than that was not written by a dump tool.
const maxScriptStatement = 64 << 20

// scriptStatement is one statement of a script and the line it began on.
type scriptStatement struct {
	sql  string
	line int
	// copyIn marks a Postgres COPY … FROM stdin: its rows follow it in the
	// script, and are read with copyData before the next statement.
	copyIn bool
}

// scriptSource reads a script a byte at a time and knows which line it is on.
type scriptSource struct {
	r    *bufio.Reader
	line int
}

func newScriptSource(r io.Reader) *scriptSource {
	return &scriptSource{r: bufio.NewReaderSize(r, 256<<10), line: 1}
}

func (s *scriptSource) next() (byte, error) {
	c, err := s.r.ReadByte()
	if c == '\n' {
		s.line++
	}
	return c, err
}

// peek returns the byte that follows, or 0 at the end.
func (s *scriptSource) peek() byte {
	b, err := s.r.Peek(1)
	if err != nil {
		return 0
	}
	return b[0]
}

// ahead reports whether the bytes that follow are exactly text.
func (s *scriptSource) ahead(text string) bool {
	b, _ := s.r.Peek(len(text))
	return string(b) == text
}

func (s *scriptSource) skip(n int) {
	for ; n > 0; n-- {
		s.next()
	}
}

// restOfLine consumes up to and including the newline and returns what it
// read, without the newline.
func (s *scriptSource) restOfLine() (string, error) {
	var line strings.Builder
	for {
		c, err := s.next()
		if err == io.EOF || c == '\n' {
			return line.String(), nil
		}
		if err != nil {
			return "", err
		}
		// Only the start of a line is ever looked at, and a comment can be as
		// long as its author liked.
		if line.Len() < 256 {
			line.WriteByte(c)
		}
	}
}

// statementText gathers one statement, and stops gathering at the bound
// rather than holding a file's worth of one in memory.
type statementText struct {
	b    strings.Builder
	max  int
	over bool
}

func (t *statementText) put(c byte) {
	if t.b.Len() >= t.max {
		t.over = true
		return
	}
	t.b.WriteByte(c)
}

func (t *statementText) take(line int) (scriptStatement, error) {
	over := t.over
	sql := strings.TrimSpace(t.b.String())
	t.b.Reset()
	t.over = false
	if over {
		return scriptStatement{}, fmt.Errorf("the statement at line %d is larger than %d MiB, which is more than a server takes in one",
			line, t.max>>20)
	}
	return scriptStatement{sql: sql, line: line}, nil
}

func isScriptSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func scriptWordStart(c byte) bool {
	return c == '_' || c >= 0x80 || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func scriptWordPart(c byte) bool {
	return scriptWordStart(c) || c == '$' || (c >= '0' && c <= '9')
}

// --- Postgres ----------------------------------------------------------------

// scriptReconnectError is a psql script asking to carry on in another
// database.
type scriptReconnectError struct{ command string }

func (e *scriptReconnectError) Error() string {
	return "this script connects to another database (" + e.command + ")"
}

// postgresScript cuts a psql script into statements where psql would.
//
// The rules are psql's own (psqlscan.l): a semicolon ends a statement unless
// it is inside parentheses, a quoted string or identifier, a dollar-quoted
// body, a comment, or the BEGIN … END of a function written in SQL. A plain
// string is read as the server reads one with standard_conforming_strings on,
// which is what every pg_dump sets on its first lines.
type postgresScript struct {
	src     *scriptSource
	text    statementText
	started bool
	line    int

	parens int
	// begins counts the BEGIN … END and CASE … END blocks of a CREATE FUNCTION
	// or PROCEDURE whose body is SQL (BEGIN ATOMIC). The semicolons inside one
	// belong to the body.
	begins int
	// words are the statement's first keywords, which say whether it is one of
	// those, and prev the keyword before this one.
	words  []string
	prev   string
	copyIn bool
	// escape is set when the token just read is the letter E on its own, which
	// makes the string after it one that backslashes escape in.
	escape bool
}

func newPostgresScript(r io.Reader) *postgresScript {
	return &postgresScript{src: newScriptSource(r), text: statementText{max: maxScriptStatement}}
}

func (p *postgresScript) begin() {
	if !p.started {
		p.started, p.line = true, p.src.line
	}
}

func (p *postgresScript) put(c byte) {
	p.begin()
	p.text.put(c)
}

// gap stands for white space or a comment: nothing before a statement has
// begun, and one separator inside it.
func (p *postgresScript) gap(c byte) {
	if p.started {
		p.text.put(c)
	}
}

func (p *postgresScript) take() (scriptStatement, bool, error) {
	started, line, copyIn := p.started, p.line, p.copyIn
	p.started, p.parens, p.begins, p.words, p.prev, p.copyIn = false, 0, 0, p.words[:0], "", false
	st, err := p.text.take(line)
	if err != nil || !started {
		return scriptStatement{}, false, err
	}
	st.copyIn = copyIn
	return st, true, nil
}

// next returns the next statement, or io.EOF after the last.
func (p *postgresScript) next() (scriptStatement, error) {
	for {
		c, err := p.src.next()
		if err == io.EOF {
			// A last statement with no semicolon is still run, as psql runs it.
			st, ok, terr := p.take()
			if terr != nil {
				return scriptStatement{}, terr
			}
			if ok {
				st.copyIn = false
				return st, nil
			}
			return scriptStatement{}, io.EOF
		}
		if err != nil {
			return scriptStatement{}, err
		}
		afterE := p.escape
		p.escape = false
		switch {
		case isScriptSpace(c):
			p.gap(c)
		case c == '-' && p.src.peek() == '-':
			if _, err := p.src.restOfLine(); err != nil {
				return scriptStatement{}, err
			}
			p.gap('\n')
		case c == '/' && p.src.peek() == '*':
			if err := p.blockComment(); err != nil {
				return scriptStatement{}, err
			}
			p.gap(' ')
		case c == '\'':
			p.put(c)
			p.prev = ""
			if err := p.quoted('\'', afterE); err != nil {
				return scriptStatement{}, err
			}
		case c == '"':
			p.put(c)
			p.prev = ""
			if err := p.quoted('"', false); err != nil {
				return scriptStatement{}, err
			}
		case c == '$':
			p.put(c)
			p.prev = ""
			if err := p.dollarQuoted(); err != nil {
				return scriptStatement{}, err
			}
		case c == '\\':
			if err := p.metaCommand(); err != nil {
				return scriptStatement{}, err
			}
		case c == ';' && p.parens == 0 && p.begins == 0:
			st, ok, err := p.take()
			if err != nil {
				return scriptStatement{}, err
			}
			if !ok {
				continue
			}
			if st.copyIn {
				if err := p.endCopyLine(); err != nil {
					return scriptStatement{}, err
				}
			}
			return st, nil
		case scriptWordStart(c):
			p.word(c)
		case c >= '0' && c <= '9':
			// A number takes its letters with it, so that the e of 1e5 is not
			// read as the E before a string.
			p.put(c)
			for scriptWordPart(p.src.peek()) || p.src.peek() == '.' {
				n, _ := p.src.next()
				p.put(n)
			}
			p.prev = ""
		default:
			switch c {
			case '(':
				p.parens++
			case ')':
				if p.parens > 0 {
					p.parens--
				}
			}
			p.put(c)
			p.prev = ""
		}
	}
}

func (p *postgresScript) word(first byte) {
	var word [16]byte
	n := 0
	for c := first; ; {
		p.put(c)
		if n < len(word) {
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			word[n] = c
		}
		n++
		if !scriptWordPart(p.src.peek()) {
			break
		}
		c, _ = p.src.next()
	}
	if n == 1 && word[0] == 'e' {
		p.escape = true
	}
	if p.parens != 0 || n > len(word) {
		p.prev = ""
		return
	}
	w := string(word[:n])
	if len(p.words) < 4 {
		p.words = append(p.words, w)
	}
	if p.sqlRoutine() {
		switch w {
		case "begin":
			p.begins++
		case "case":
			if p.begins > 0 {
				p.begins++
			}
		case "end":
			if p.begins > 0 {
				p.begins--
			}
		}
	}
	if p.words[0] == "copy" && p.prev == "from" && w == "stdin" {
		p.copyIn = true
	}
	p.prev = w
}

// sqlRoutine reports whether the statement being read is a CREATE [OR
// REPLACE] FUNCTION or PROCEDURE.
func (p *postgresScript) sqlRoutine() bool {
	w := p.words
	routine := func(s string) bool { return s == "function" || s == "procedure" }
	if len(w) < 2 || w[0] != "create" {
		return false
	}
	return routine(w[1]) || (len(w) == 4 && w[1] == "or" && w[2] == "replace" && routine(w[3]))
}

// quoted copies the rest of a string or a quoted identifier, whose opening
// quote has been read. A doubled quote is one quote and not the end.
func (p *postgresScript) quoted(closer byte, backslashes bool) error {
	for {
		c, err := p.src.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p.text.put(c)
		if backslashes && c == '\\' {
			n, err := p.src.next()
			if err == nil {
				p.text.put(n)
			}
			continue
		}
		if c == closer {
			if p.src.peek() != closer {
				return nil
			}
			p.src.next()
			p.text.put(closer)
		}
	}
}

// dollarQuoted copies a dollar-quoted body, when the dollar just read opens
// one: $$ … $$ or $tag$ … $tag$. Anything else is a parameter or an operator
// and is left as it is.
func (p *postgresScript) dollarQuoted() error {
	head, _ := p.src.r.Peek(64)
	end := -1
	for i, c := range head {
		if c == '$' {
			end = i
			break
		}
		if !scriptWordStart(c) && !(i > 0 && c >= '0' && c <= '9') {
			return nil
		}
	}
	if end < 0 {
		return nil
	}
	closer := string(head[:end]) + "$"
	for range closer {
		c, _ := p.src.next()
		p.text.put(c)
	}
	for {
		c, err := p.src.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p.text.put(c)
		if c == '$' && p.src.ahead(closer) {
			for range closer {
				c, _ := p.src.next()
				p.text.put(c)
			}
			return nil
		}
	}
}

// blockComment skips a comment whose slash has been read. Postgres nests them.
func (p *postgresScript) blockComment() error {
	p.src.next()
	for depth := 1; depth > 0; {
		c, err := p.src.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case c == '/' && p.src.peek() == '*':
			p.src.next()
			depth++
		case c == '*' && p.src.peek() == '/':
			p.src.next()
			depth--
		}
	}
	return nil
}

// metaCommand answers a backslash outside a string, which to psql begins a
// command of its own. Nothing here runs one.
func (p *postgresScript) metaCommand() error {
	line := p.src.line
	var name strings.Builder
	for c := p.src.peek(); c != 0 && c != '\\' && !isScriptSpace(c) && name.Len() < 32; c = p.src.peek() {
		p.src.next()
		name.WriteByte(c)
	}
	rest, err := p.src.restOfLine()
	if err != nil {
		return err
	}
	switch name.String() {
	case "restrict", "unrestrict":
		// pg_dump has bracketed its output with these since the 2025 minor
		// releases: they tell psql to refuse every other command in between.
		// Every other command is refused here anyway.
		return nil
	case "connect", "c":
		return &scriptReconnectError{command: strings.TrimSpace(`\` + name.String() + rest)}
	}
	return fmt.Errorf("line %d: this script uses psql's \\%s, which only psql runs; replay it with psql yourself", line, name.String())
}

// endCopyLine reads what is left of the line a COPY … FROM stdin ended on. Its
// rows begin on the next one; psql would run anything else on this line after
// them, and no dump tool puts anything there.
func (p *postgresScript) endCopyLine() error {
	line := p.src.line
	rest, err := p.src.restOfLine()
	if err != nil {
		return err
	}
	if rest = strings.TrimSpace(rest); rest != "" && !strings.HasPrefix(rest, "--") {
		return fmt.Errorf("line %d: something follows COPY … FROM stdin on its own line, where only its rows can", line)
	}
	return nil
}

// copyData returns the rows that follow a COPY … FROM stdin, up to the line
// that ends them. It has to be read to its end before the next statement.
func (p *postgresScript) copyData() io.Reader {
	return &copyBlock{src: p.src, lineStart: true}
}

// copyBlock is the rows of one COPY, as they stand in the script.
type copyBlock struct {
	src       *scriptSource
	lineStart bool
	rest      []byte
	done      bool
}

func (c *copyBlock) Read(out []byte) (int, error) {
	for len(c.rest) == 0 {
		if c.done {
			return 0, io.EOF
		}
		// A row longer than the buffer comes back in pieces, and only the
		// first of them is the start of a line.
		chunk, err := c.src.r.ReadSlice('\n')
		whole := err == nil
		if err != nil && err != bufio.ErrBufferFull {
			c.done = true
			if err != io.EOF {
				return 0, err
			}
		}
		if whole {
			c.src.line++
		}
		if c.lineStart && err != bufio.ErrBufferFull && string(bytes.TrimRight(chunk, "\r\n")) == `\.` {
			c.done = true
			return 0, io.EOF
		}
		c.lineStart = whole
		c.rest = chunk
	}
	n := copy(out, c.rest)
	c.rest = c.rest[n:]
	return n, nil
}

// checkPostgresScript reads a psql script through without running any of it,
// and refuses one that only psql could run.
//
// The replay is one transaction and would undo itself anyway. It is read first
// so that a dump taken with --create is told what it is, rather than failing on
// its CREATE DATABASE in the server's words for something else.
func checkPostgresScript(path, database string) error {
	text, closeDump, err := openDumpText(path)
	if err != nil {
		return err
	}
	defer closeDump()
	script := newPostgresScript(text)
	for {
		st, err := script.next()
		if err == io.EOF {
			return nil
		}
		var reconnects *scriptReconnectError
		if errors.As(err, &reconnects) {
			return fmt.Errorf("%v, so it cannot be restored into %s; "+
				"take the dump of one database without --create, or replay it with psql yourself", err, database)
		}
		if err != nil {
			return err
		}
		if st.copyIn {
			if _, err := io.Copy(io.Discard, script.copyData()); err != nil {
				return err
			}
		}
	}
}

// endsTransaction reports whether a statement closes the transaction it runs
// in. A script that does has kept what it had done by then.
func endsTransaction(statement string) bool {
	words := strings.Fields(strings.ToLower(statement[:min(len(statement), 64)]))
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "commit", "end", "abort":
		return true
	case "rollback":
		return len(words) == 1 || words[1] != "to"
	case "prepare":
		return len(words) > 1 && words[1] == "transaction"
	}
	return false
}

// replayPostgresScript runs a psql script over one connection, as one
// transaction: a script that fails on its fortieth statement leaves the
// database as it was.
func replayPostgresScript(ctx context.Context, conn *sql.Conn, text io.Reader, opts RestoreOptions) (string, error) {
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return "", err
	}
	// A script that commits for itself has kept what it committed, and a
	// later failure cannot then be reported as having changed nothing.
	committed := false
	fail := func(what, detail string) (string, error) {
		// The job may be what was cancelled; the rollback still has to reach
		// the server.
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		if !committed {
			what += ", and nothing was changed"
		}
		return "", fmt.Errorf("%s: %s", what, detail)
	}

	script := newPostgresScript(text)
	var ran int
	var rows int64
	lastReport := time.Now()
	for {
		st, err := script.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(fmt.Sprintf("the script could not be read after statement %d", ran), err.Error())
		}
		if st.copyIn {
			err = conn.Raw(func(driverConn any) error {
				tag, err := driverConn.(*stdlib.Conn).Conn().PgConn().CopyFrom(ctx, script.copyData(), st.sql)
				rows += tag.RowsAffected()
				return err
			})
		} else {
			_, err = conn.ExecContext(ctx, st.sql)
		}
		if err != nil {
			return fail(fmt.Sprintf("statement %d (line %d) failed", ran+1, st.line),
				fmt.Sprintf("%v\n%s", err, truncateForMessage(st.sql)))
		}
		committed = committed || endsTransaction(st.sql)
		ran++
		if opts.Progress != nil && time.Since(lastReport) > 2*time.Second {
			opts.progress("%d statements applied, %d rows loaded", ran, rows)
			lastReport = time.Now()
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fail("the restore could not be committed", err.Error())
	}
	out := fmt.Sprintf("%d statements applied, %d rows loaded", ran, rows)
	opts.progress("%s", out)
	return out, nil
}

// --- MySQL -------------------------------------------------------------------

// mysqlScript cuts a script written for the mysql client into statements
// where the client would.
//
// The client's rules are few (mysql.cc, add_line): the delimiter ends a
// statement unless it is inside a string, a quoted identifier or a comment;
// DELIMITER on a line of its own changes it, which is how mysqldump writes a
// trigger whose body holds semicolons; and a conditional comment — /*!50003 … */
// — is not a comment at all but statement text the server decides about. The
// statements go to the server as they stand, conditional comments included.
type mysqlScript struct {
	src       *scriptSource
	text      statementText
	started   bool
	line      int
	delimiter string
	// lineStart is true until something other than white space has been read
	// on the line, which is the only place DELIMITER is one.
	lineStart bool
}

func newMySQLScript(r io.Reader) *mysqlScript {
	return &mysqlScript{src: newScriptSource(r), text: statementText{max: maxScriptStatement}, delimiter: ";", lineStart: true}
}

func (m *mysqlScript) put(c byte) {
	if !m.started {
		m.started, m.line = true, m.src.line
	}
	m.text.put(c)
}

func (m *mysqlScript) gap(c byte) {
	if m.started {
		m.text.put(c)
	}
}

func (m *mysqlScript) take() (scriptStatement, bool, error) {
	started, line := m.started, m.line
	m.started = false
	st, err := m.text.take(line)
	if err != nil || !started {
		return scriptStatement{}, false, err
	}
	return st, true, nil
}

// next returns the next statement, or io.EOF after the last.
func (m *mysqlScript) next() (scriptStatement, error) {
	for {
		if m.lineStart && !m.started {
			line, use, taken, err := m.commandLine()
			if err != nil {
				return scriptStatement{}, err
			}
			if use != "" {
				return scriptStatement{sql: use, line: line}, nil
			}
			if taken {
				continue
			}
		}
		c, err := m.src.next()
		if err == io.EOF {
			st, ok, terr := m.take()
			if terr != nil {
				return scriptStatement{}, terr
			}
			if ok {
				return st, nil
			}
			return scriptStatement{}, io.EOF
		}
		if err != nil {
			return scriptStatement{}, err
		}
		if isScriptSpace(c) {
			if c == '\n' {
				m.lineStart = true
			}
			m.gap(c)
			continue
		}
		m.lineStart = false
		switch {
		case c == m.delimiter[0] && m.src.ahead(m.delimiter[1:]):
			m.src.skip(len(m.delimiter) - 1)
			st, ok, err := m.take()
			if err != nil {
				return scriptStatement{}, err
			}
			if ok {
				return st, nil
			}
		case c == '\'' || c == '"':
			m.put(c)
			if err := m.quoted(c, true); err != nil {
				return scriptStatement{}, err
			}
		case c == '`':
			m.put(c)
			if err := m.quoted(c, false); err != nil {
				return scriptStatement{}, err
			}
		case c == '#', c == '-' && m.dashComment():
			if _, err := m.src.restOfLine(); err != nil {
				return scriptStatement{}, err
			}
			m.lineStart = true
			m.gap('\n')
		case c == '/' && m.src.peek() == '*':
			if m.conditionalComment() {
				// Its text is the statement's, up to and including the */.
				m.put(c)
				continue
			}
			if err := m.blockComment(); err != nil {
				return scriptStatement{}, err
			}
			m.gap(' ')
		case c == '\\':
			// Outside a string a backslash begins a command of the client's
			// own: \! runs a shell, \. reads a file, \u changes database. Two
			// are not that. \N is NULL to the server, and \- is the line
			// mariadb-dump opens with, inside a conditional comment no server
			// takes up.
			if n := m.src.peek(); n != 'N' && n != '-' {
				return scriptStatement{}, fmt.Errorf("line %d: this script uses the mysql client's \\%c, which only the client runs; replay it with the client yourself",
					m.src.line, n)
			}
			m.put(c)
		default:
			m.put(c)
		}
	}
}

// mysqlClientCommands are the words the client takes for itself when one
// begins a line that begins a statement. DELIMITER and USE are among them and
// are honoured; every other one reads a file, runs a program or reconnects.
var mysqlClientCommands = map[string]bool{
	"?": true, "charset": true, "clear": true, "connect": true, "edit": true, "ego": true, "exit": true,
	"go": true, "help": true, "nopager": true, "notee": true, "nowarning": true, "pager": true,
	"print": true, "prompt": true, "query_attributes": true, "quit": true, "rehash": true,
	"resetconnection": true, "sandbox": true, "source": true, "ssl_session_data_print": true,
	"status": true, "system": true, "tee": true, "warnings": true,
}

// commandLine deals with a line that is a command to the client rather than
// SQL, when the line that begins here is one: it is one where no statement
// is open, its first word is a command's name, and — except for DELIMITER —
// the delimiter is not on it.
//
// DELIMITER is taken. A USE is handed back as the statement it amounts to,
// with the line it stood on. Any other is refused.
func (m *mysqlScript) commandLine() (line int, use string, taken bool, err error) {
	for c := m.src.peek(); c == ' ' || c == '\t'; c = m.src.peek() {
		m.src.next()
	}
	head, _ := m.src.r.Peek(512)
	if end := bytes.IndexByte(head, '\n'); end >= 0 {
		head = head[:end]
	}
	word, rest, _ := strings.Cut(strings.TrimRight(string(head), "\r"), " ")
	word, tabbed, _ := strings.Cut(word, "\t")
	rest = strings.TrimSpace(tabbed + " " + rest)
	command := strings.ToLower(word)
	line = m.src.line
	switch {
	case command == "delimiter":
		if _, err := m.src.restOfLine(); err != nil {
			return 0, "", false, err
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 || strings.Contains(fields[0], `\`) {
			return 0, "", false, fmt.Errorf("line %d: DELIMITER names no delimiter", line)
		}
		m.delimiter = fields[0]
		return line, "", true, nil
	case strings.Contains(string(head), m.delimiter):
		return 0, "", false, nil
	case command == "use" && rest != "":
		if _, err := m.src.restOfLine(); err != nil {
			return 0, "", false, err
		}
		return line, "USE " + rest, true, nil
	case mysqlClientCommands[command]:
		return 0, "", false, fmt.Errorf("line %d: this script uses the mysql client's %s, which only the client runs; replay it with the client yourself",
			line, command)
	}
	return 0, "", false, nil
}

// dashComment reports whether the dash just read begins a comment: two
// dashes and then white space, which is the server's rule and what keeps
// 5--3 arithmetic.
func (m *mysqlScript) dashComment() bool {
	head, _ := m.src.r.Peek(2)
	if len(head) == 0 || head[0] != '-' {
		return false
	}
	return len(head) == 1 || head[1] <= ' '
}

// conditionalComment reports whether the comment that opens here is one the
// server reads: /*!…, MariaDB's /*M!…, or an optimizer hint.
func (m *mysqlScript) conditionalComment() bool {
	head, _ := m.src.r.Peek(3)
	if len(head) < 2 {
		return false
	}
	return head[1] == '!' || head[1] == '+' || (len(head) == 3 && head[1] == 'M' && head[2] == '!')
}

func (m *mysqlScript) blockComment() error {
	m.src.next()
	for {
		c, err := m.src.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if c == '*' && m.src.peek() == '/' {
			m.src.next()
			return nil
		}
	}
}

// quoted copies the rest of a string or a quoted identifier, whose opening
// quote has been read. A backslash escapes inside a string and not inside a
// backticked name.
func (m *mysqlScript) quoted(closer byte, backslashes bool) error {
	for {
		c, err := m.src.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		m.text.put(c)
		if backslashes && c == '\\' {
			n, err := m.src.next()
			if err == nil {
				m.text.put(n)
			}
			continue
		}
		if c == closer {
			if m.src.peek() != closer {
				return nil
			}
			m.src.next()
			m.text.put(closer)
		}
	}
}

// mysqlUse returns the database a statement switches to, when the statement
// is a USE.
//
// It is the one statement that moves a session: it cannot be prepared and it
// is not allowed inside a routine, so the first word of each statement the
// server is sent is the whole question. That word may stand inside a
// conditional comment, which the server unwraps before reading it.
func mysqlUse(statement string) (string, bool) {
	s := statement
	for {
		s = strings.TrimLeft(s, " \t\r\n\f\v")
		switch {
		case strings.HasPrefix(s, "/*!"), strings.HasPrefix(s, "/*M!"):
			s = strings.TrimLeft(s[strings.IndexByte(s, '!')+1:], "0123456789")
		case strings.HasPrefix(s, "/*"):
			end := strings.Index(s, "*/")
			if end < 0 {
				return "", false
			}
			s = s[end+2:]
		case strings.HasPrefix(s, "*/"):
			s = s[2:]
		default:
			if len(s) < 4 || !strings.EqualFold(s[:3], "use") || scriptWordPart(s[3]) {
				return "", false
			}
			name := strings.TrimLeft(s[3:], " \t\r\n\f\v")
			if strings.HasPrefix(name, "`") {
				var b strings.Builder
				for i := 1; i < len(name); i++ {
					if name[i] != '`' {
						b.WriteByte(name[i])
						continue
					}
					if i+1 < len(name) && name[i+1] == '`' {
						b.WriteByte('`')
						i++
						continue
					}
					break
				}
				return b.String(), true
			}
			end := strings.IndexAny(name, " \t\r\n\f\v;*")
			if end < 0 {
				end = len(name)
			}
			return name[:end], true
		}
	}
}

// errScriptSwitches is the refusal of a script that would carry on in another
// database.
func errScriptSwitches(statement, database string) error {
	return fmt.Errorf("this script switches to another database (%s), so it cannot be restored into %s; "+
		"take the dump of one database without --databases, or replay it with the mysql client yourself",
		truncateForMessage(statement), database)
}

// checkMySQLScript reads a script through without running any of it, and
// refuses one that leaves the database it is being restored into or that only
// the client could run.
//
// It is read first because MySQL commits at every statement that changes
// structure: a script refused at its fortieth statement would have dropped
// and recreated thirty-nine things on the way there. mysqldump writes a USE
// when given --databases, and that is the ordinary way to arrive here.
func checkMySQLScript(path, database string) error {
	text, closeDump, err := openDumpText(path)
	if err != nil {
		return err
	}
	defer closeDump()
	script := newMySQLScript(text)
	for {
		st, err := script.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if name, ok := mysqlUse(st.sql); ok && name != database {
			return errScriptSwitches(st.sql, database)
		}
	}
}

// mysqlEmptyQuery is the server's answer to a statement with nothing in it,
// which is what a conditional comment for another version comes to.
const mysqlEmptyQuery = 1065

// replayMySQLScript runs a script over one connection, a statement at a time.
func replayMySQLScript(ctx context.Context, conn *sql.Conn, text io.Reader, opts RestoreOptions) (string, error) {
	script := newMySQLScript(text)
	var ran int
	lastReport := time.Now()
	for {
		st, err := script.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("the script could not be read after statement %d: %w", ran, err)
		}
		if _, err := conn.ExecContext(ctx, st.sql); err != nil {
			var refused *mysql.MySQLError
			if errors.As(err, &refused) && refused.Number == mysqlEmptyQuery {
				continue
			}
			return "", fmt.Errorf("statement %d (line %d) failed: %w\n%s", ran+1, st.line, err, truncateForMessage(st.sql))
		}
		ran++
		if opts.Progress != nil && time.Since(lastReport) > 2*time.Second {
			opts.progress("%d statements applied", ran)
			lastReport = time.Now()
		}
	}
	out := fmt.Sprintf("%d statements applied", ran)
	opts.progress("%s", out)
	return out, nil
}

// mysqlOneStatementDSN is the connection string with multi-statement queries
// switched off, for a connection that replays a file. With them on, one
// Exec can carry "…; USE other", and what a statement is stops being decided
// here.
func mysqlOneStatementDSN(dsn string) string {
	if !strings.Contains(dsn, "multiStatements") {
		return dsn
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return dsn
	}
	cfg.MultiStatements = false
	return cfg.FormatDSN()
}

// --- both --------------------------------------------------------------------

// restoreScript replays a SQL script this package did not write.
func restoreScript(ctx context.Context, driver Driver, dsn, database, path string, opts RestoreOptions) (string, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return "", err
	}
	// Read through before anything is run, so that what is refused is refused
	// with the database untouched.
	if driver == DriverMySQL {
		err = checkMySQLScript(path, database)
		dsn = mysqlOneStatementDSN(dsn)
	} else {
		err = checkPostgresScript(path, database)
	}
	if err != nil {
		return "", err
	}
	text, closeDump, err := openDumpText(path)
	if err != nil {
		return "", err
	}
	defer closeDump()
	db, err := openForDump(ctx, d, dsnForDatabase(driver, dsn, database))
	if err != nil {
		return "", err
	}
	defer db.Close()
	// One connection for the whole script: what a statement sets for the
	// session has to still be set for the next.
	conn, err := db.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if driver == DriverPostgres {
		return replayPostgresScript(ctx, conn, text, opts)
	}
	return replayMySQLScript(ctx, conn, text, opts)
}
