package dbx

import (
	"fmt"
	"strconv"
	"strings"
)

// Where one statement of a script ends.
//
// A semicolon ends a statement on every engine, except inside the body of one
// that holds statements of its own — a procedure, a trigger, a PL/SQL block —
// where the semicolons are the body's and the engine reads the whole as one.
// Cutting at every semicolon cut such a statement into pieces no engine
// accepts, so a stored program could not be written from the console at all.
//
// Each engine is read the way its own client reads a script, because that is
// how such scripts are written and pasted:
//
//   - PostgreSQL as psql does: a semicolon inside parentheses, or inside the
//     BEGIN … END of a CREATE FUNCTION or PROCEDURE whose body is SQL (BEGIN
//     ATOMIC), ends nothing. A dollar-quoted body is one token already.
//   - MySQL and MariaDB as the mysql client does: DELIMITER, where a statement
//     would begin, names what ends a statement until the next DELIMITER, and
//     while that is not ; a semicolon ends nothing. With the plain ;, the
//     blocks of a stored program's body — BEGIN … END, IF … END IF, CASE,
//     LOOP, WHILE, REPEAT — are followed, so a procedure written without
//     DELIMITER, the way SHOW CREATE PROCEDURE prints one, is one statement too.
//   - SQLite as the engine does: the BEGIN … END of a CREATE TRIGGER.
//   - SQL Server as sqlcmd does: GO on a line of its own ends a batch, and
//     GO n runs the batch n times. A BEGIN … END block is one statement, a ;
//     before ELSE ends nothing, and the body of a CREATE PROCEDURE, FUNCTION or
//     TRIGGER runs to the end of its batch — or to the ; after its BEGIN … END.
//   - Oracle as SQL*Plus does: a PL/SQL unit — a DECLARE or BEGIN block, or a
//     CREATE PROCEDURE, FUNCTION, PACKAGE, TRIGGER or TYPE — runs to a line
//     holding only /, or to the end of the text, and keeps the semicolon after
//     its END, which PL/SQL needs. A / line ends a plain statement too.
//
// None of this is a reason to trust a statement more. One that holds a body
// is classified high whatever word leads it (read), so a protected connection
// refuses it and a role without the destructive capability cannot send it.
// Where this reading and the engine's disagree about where a body ends, the
// disagreement costs a confirmation or a syntax error, never a verdict.

// scriptPiece is one statement as the splitter cut it.
type scriptPiece struct {
	tokens []sqlToken
	line   int
	// plsql marks an Oracle PL/SQL unit, whose closing semicolon is its own.
	plsql bool
}

type scriptSplitter struct {
	driver Driver
	q      string
	lex    *sqlLexer
	out    []scriptPiece

	// The statement being read: its tokens, its first words outside
	// parentheses in lower case, how deep in parentheses it is, and the
	// blocks of its body that are open, by the word that opened each.
	cur    []sqlToken
	words  []string
	parens int
	blocks []string
	// closed is set once a SQL Server routine's outermost BEGIN … END has
	// ended: the next semicolon ends the routine.
	closed bool

	// batch is where the SQL Server batch being read began in out.
	batch int
	// line is the line counted has reached.
	line, counted int
}

// splitScript cuts a query into the statements its engine reads.
func splitScript(driver Driver, query string) ([]scriptPiece, error) {
	s := &scriptSplitter{
		driver: driver,
		q:      query,
		lex:    &sqlLexer{r: lexRulesFor(driver), q: query},
		line:   1,
	}
	for {
		t, ok, err := s.lex.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		if err := s.take(t); err != nil {
			return nil, err
		}
	}
	s.end()
	return s.out, nil
}

func (s *scriptSplitter) take(t sqlToken) error {
	switch {
	case t.kind == tokDelimiter:
		s.end()
		return nil
	case s.driver == DriverMySQL && len(s.cur) == 0 && s.is(t, "delimiter"):
		if taken, err := s.delimiterLine(t); taken || err != nil {
			return err
		}
	case s.driver == DriverMSSQL && s.is(t, "go"):
		count, next, ok, err := goLine(s.q, t)
		if err != nil {
			return fmt.Errorf("line %d: %w", s.lineAt(t.start), err)
		}
		if ok {
			return s.endBatch(count, next)
		}
	case s.driver == DriverOracle && t.kind == tokPunct && s.q[t.start] == '/':
		if next, ok := slashLine(s.q, t); ok {
			s.end()
			s.lex.i = next
			return nil
		}
	case t.kind == tokSemicolon && s.ends():
		s.end()
		return nil
	}
	s.track(t)
	s.cur = append(s.cur, t)
	return nil
}

// end closes the statement being read, if one is.
func (s *scriptSplitter) end() {
	tokens, plsql := s.cur, s.plsql()
	if !plsql {
		// `END;` before a DELIMITER's own delimiter, or `;` before a GO, is
		// a terminator the server does not need.
		for len(tokens) > 0 && tokens[len(tokens)-1].kind == tokSemicolon {
			tokens = tokens[:len(tokens)-1]
		}
	}
	if len(tokens) > 0 {
		s.out = append(s.out, scriptPiece{tokens: tokens, line: s.lineAt(tokens[0].start), plsql: plsql})
	}
	s.cur, s.words, s.parens, s.blocks, s.closed = nil, nil, 0, nil, false
}

// endBatch closes a SQL Server batch at its GO line, repeating it as many times
// as the line asks.
func (s *scriptSplitter) endBatch(count, next int) error {
	s.end()
	batch := s.out[s.batch:]
	for range count - 1 {
		if len(s.out)+len(batch) > maxScriptStatements {
			return fmt.Errorf("a script may hold at most %d statements, and GO %d repeats its batch past that", maxScriptStatements, count)
		}
		s.out = append(s.out, batch...)
	}
	s.batch = len(s.out)
	s.lex.i = next
	return nil
}

// lineAt is the line the offset is on. Offsets are asked for in order.
func (s *scriptSplitter) lineAt(offset int) int {
	s.line += strings.Count(s.q[s.counted:offset], "\n")
	s.counted = offset
	return s.line
}

func (s *scriptSplitter) is(t sqlToken, word string) bool {
	return t.kind == tokWord && strings.EqualFold(s.q[t.start:t.end], word)
}

func (s *scriptSplitter) punct(t sqlToken, c byte) bool {
	return t.kind == tokPunct && s.q[t.start] == c
}

// lastWord is the statement's last token in lower case when it is a word.
func (s *scriptSplitter) lastWord() string {
	if n := len(s.cur); n > 0 && s.cur[n-1].kind == tokWord {
		return strings.ToLower(s.q[s.cur[n-1].start:s.cur[n-1].end])
	}
	return ""
}

// ends reports whether the semicolon just read ends the statement.
func (s *scriptSplitter) ends() bool {
	switch {
	case s.lex.delimiter != "":
		// A script that chose a delimiter of its own says where its
		// statements end, and a semicolon is not where.
		return false
	case len(s.blocks) > 0:
		return false
	}
	switch s.driver {
	case DriverPostgres:
		return s.parens == 0
	case DriverMSSQL:
		if s.routine() && !s.closed {
			return false
		}
		// IF … ; ELSE … is one statement.
		next, ok := s.lex.peek()
		return !ok || !s.is(next, "else")
	case DriverOracle:
		return !s.plsql()
	}
	return true
}

// track follows the parentheses and the body blocks of the statement as each
// token is added to it.
func (s *scriptSplitter) track(t sqlToken) {
	switch {
	case s.punct(t, '('):
		s.parens++
		return
	case s.punct(t, ')'):
		if s.parens > 0 {
			s.parens--
		}
		return
	case t.kind != tokWord || s.parens > 0:
		return
	}
	word := strings.ToLower(s.q[t.start:t.end])
	// With a delimiter of its own the script says where its statements end.
	if s.lex.delimiter == "" {
		s.block(word)
	}
	if len(s.words) < 8 {
		s.words = append(s.words, word)
	}
}

// block opens or closes a block of the body with the word about to be added.
func (s *scriptSplitter) block(word string) {
	switch s.driver {
	case DriverPostgres:
		// psql's own rule (psqlscan.l), which dump_script.go follows too.
		if !s.routine() {
			return
		}
		switch word {
		case "begin":
			s.open(word)
		case "case":
			if len(s.blocks) > 0 {
				s.open(word)
			}
		case "end":
			s.close()
		}
	case DriverSQLite:
		if !s.routine() {
			return
		}
		switch word {
		case "begin", "case":
			s.open(word)
		case "end":
			s.close()
		}
	case DriverMSSQL:
		s.tsqlBlock(word)
	case DriverMySQL:
		s.mysqlBlock(word)
	}
}

func (s *scriptSplitter) open(word string) { s.blocks = append(s.blocks, word) }

func (s *scriptSplitter) close() {
	n := len(s.blocks)
	if n == 0 {
		return
	}
	if n == 1 && s.blocks[0] == "begin" {
		s.closed = true
	}
	s.blocks = s.blocks[:n-1]
}

// tsqlNotBlocks are the words after BEGIN that make it a statement rather than
// the start of a block.
var tsqlNotBlocks = map[string]bool{
	"tran": true, "transaction": true, "distributed": true, "dialog": true, "conversation": true,
}

func (s *scriptSplitter) nextWord() string {
	if next, ok := s.lex.peek(); ok && next.kind == tokWord {
		return strings.ToLower(s.q[next.start:next.end])
	}
	return ""
}

func (s *scriptSplitter) tsqlBlock(word string) {
	switch word {
	case "begin":
		if !tsqlNotBlocks[s.nextWord()] {
			s.open(word)
		}
	case "case":
		// Only a block's own END needs telling from a CASE's; outside one a
		// CASE left open by a typo would swallow the statements after it.
		if len(s.blocks) > 0 {
			s.open(word)
		}
	case "end":
		// END CONVERSATION is Service Broker's, not a block's end.
		if s.nextWord() != "conversation" {
			s.close()
		}
	}
}

// mysqlBlock follows the blocks of a stored program's body, and of the
// compound statements MariaDB also runs on their own (BEGIN NOT ATOMIC, IF,
// CASE, LOOP, WHILE, REPEAT). Every block ends at an END, whatever follows it
// (END IF, END LOOP, a label), so only the openers need telling apart from the
// same words used otherwise: the function IF(…) and REPEAT(…), DDL's IF [NOT]
// EXISTS, START TRANSACTION's synonym BEGIN, END IF's IF.
func (s *scriptSplitter) mysqlBlock(word string) {
	body := len(s.blocks) > 0 || s.routine()
	after := s.lastWord()
	start := s.statementStart(body)
	switch word {
	case "begin":
		if body || s.afterLabel() || s.notAtomicNext() {
			s.open(word)
		}
	case "case":
		// A CASE statement, or an expression's CASE in a body, where its END
		// must not be taken for the body's.
		if after != "end" && (start || body) {
			s.open(word)
		}
	case "if":
		if after != "end" && (start || body && s.ifStatement()) {
			s.open(word)
		}
	case "while", "loop":
		if after != "end" && (start || body) {
			s.open(word)
		}
	case "repeat":
		if start {
			s.open(word)
		}
	case "end":
		s.close()
	}
}

// statementStart reports whether the word about to be added begins a statement
// of its own: the first word, or one after a semicolon, after a label, after
// BEGIN, THEN, ELSE, LOOP or REPEAT, after the DO of an event or a WHILE, or
// after a trigger's FOR EACH ROW.
func (s *scriptSplitter) statementStart(body bool) bool {
	n := len(s.cur)
	if n == 0 {
		return true
	}
	last := s.cur[n-1]
	if last.kind == tokSemicolon || s.punct(last, ':') {
		return true
	}
	switch s.lastWord() {
	case "begin", "then", "else", "loop", "repeat":
		return true
	case "do":
		// At the top level DO evaluates expressions, IF(…) among them.
		return body
	case "row":
		return n > 1 && s.is(s.cur[n-2], "each")
	}
	return false
}

func (s *scriptSplitter) afterLabel() bool {
	n := len(s.cur)
	return n > 0 && s.punct(s.cur[n-1], ':')
}

// notAtomicNext reports MariaDB's BEGIN NOT ATOMIC, a block run on its own.
func (s *scriptSplitter) notAtomicNext() bool {
	ahead := *s.lex
	first, ok, _ := ahead.next()
	if !ok || !s.is(first, "not") {
		return false
	}
	second, ok, _ := ahead.next()
	return ok && s.is(second, "atomic")
}

// ifStatement reports an IF in a body that is not where a statement begins —
// the first word of a body written with no BEGIN, after a routine's
// characteristics — and is neither the function IF(…) nor IF [NOT] EXISTS.
func (s *scriptSplitter) ifStatement() bool {
	ahead := *s.lex
	next, ok, _ := ahead.next()
	if !ok || s.punct(next, '(') || s.is(next, "exists") {
		return false
	}
	if !s.is(next, "not") {
		return true
	}
	after, ok, _ := ahead.next()
	return !ok || !s.is(after, "exists")
}

// routine reports a statement that defines a stored program, whose body is
// read by its blocks.
func (s *scriptSplitter) routine() bool {
	w := s.words
	if len(w) < 2 {
		return false
	}
	switch s.driver {
	case DriverPostgres:
		kind := w[1]
		if len(w) >= 4 && w[1] == "or" && w[2] == "replace" {
			kind = w[3]
		}
		return w[0] == "create" && (kind == "function" || kind == "procedure")
	case DriverMySQL:
		if w[0] == "alter" {
			return w[1] == "event"
		}
		if w[0] != "create" {
			return false
		}
		// DEFINER = … and AGGREGATE may stand before the kind.
		for _, word := range w[1:] {
			switch word {
			case "procedure", "function", "trigger", "event", "package":
				return true
			case "table", "view", "index", "database", "schema", "user", "role", "server",
				"tablespace", "sequence", "logfile":
				return false
			}
		}
	case DriverMSSQL:
		if w[0] != "create" && w[0] != "alter" {
			return false
		}
		for _, word := range w[1:] {
			switch word {
			case "or", "alter":
				continue
			case "proc", "procedure", "function", "trigger":
				return true
			}
			return false
		}
	case DriverSQLite:
		if w[0] != "create" {
			return false
		}
		for _, word := range w[1:] {
			if word != "temp" && word != "temporary" {
				return word == "trigger"
			}
		}
	}
	return false
}

// plsql reports an Oracle statement that is a PL/SQL unit, by its first words
// as SQL*Plus decides.
func (s *scriptSplitter) plsql() bool {
	if s.driver != DriverOracle || len(s.cur) == 0 {
		return false
	}
	if len(s.cur) > 1 && s.punct(s.cur[0], '<') && s.punct(s.cur[1], '<') {
		// <<label>> in front of a block.
		return true
	}
	w := s.words
	if len(w) == 0 {
		return false
	}
	switch w[0] {
	case "declare", "begin":
		return true
	case "with":
		// A query that declares PL/SQL functions of its own.
		return len(w) > 1 && (w[1] == "function" || w[1] == "procedure")
	case "create":
		for _, word := range w[1:] {
			switch word {
			case "or", "replace", "editionable", "noneditionable", "and", "compile", "resolve", "noforce":
				continue
			case "procedure", "function", "package", "trigger", "type", "library", "java":
				return true
			}
			return false
		}
	}
	return false
}

// delimiterLine takes a DELIMITER line where a statement would begin. Its
// first word after DELIMITER is the delimiter from the next line on, and the
// rest of the line is the client's, as it is to mysql.
func (s *scriptSplitter) delimiterLine(t sqlToken) (bool, error) {
	q := s.q
	if t.end < len(q) && q[t.end] != ' ' && q[t.end] != '\t' && q[t.end] != '\r' && q[t.end] != '\n' {
		return false, nil
	}
	end, next := len(q), len(q)
	if nl := strings.IndexByte(q[t.end:], '\n'); nl >= 0 {
		end, next = t.end+nl, t.end+nl+1
	}
	fields := strings.Fields(q[t.end:end])
	if len(fields) == 0 {
		return true, fmt.Errorf("line %d: DELIMITER names no delimiter", s.lineAt(t.start))
	}
	delimiter := fields[0]
	if err := checkDelimiter(delimiter); err != nil {
		return true, fmt.Errorf("line %d: %w", s.lineAt(t.start), err)
	}
	if delimiter == ";" {
		delimiter = ""
	}
	s.lex.delimiter, s.lex.i = delimiter, next
	return true, nil
}

// checkDelimiter refuses a delimiter that would be read as something else
// before it is read as itself: one that opens a comment, holds a quote or a
// backslash, or is spelled with letters, which every word would then have to
// be searched for.
func checkDelimiter(d string) error {
	if strings.HasPrefix(d, "#") || strings.HasPrefix(d, "-") || strings.HasPrefix(d, "/*") {
		return fmt.Errorf("DELIMITER %s would begin a comment; use another, such as $$ or //", d)
	}
	for i := 0; i < len(d); i++ {
		c := d[i]
		if c == '\'' || c == '"' || c == '`' || c == '\\' || c == '_' || isDigit(c) || c >= 0x80 ||
			c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			return fmt.Errorf("DELIMITER %s is not punctuation; use one that is, such as $$ or //", d)
		}
	}
	return nil
}

// lineStartsAt reports whether only spaces stand between the start of the line
// and offset.
func lineStartsAt(q string, offset int) bool {
	i := offset - 1
	for i >= 0 && (q[i] == ' ' || q[i] == '\t') {
		i--
	}
	return i < 0 || q[i] == '\n'
}

// lineEndsAt returns where the next line begins when only spaces, and with
// comment a -- comment, stand between offset and the end of its line.
func lineEndsAt(q string, offset int, comment bool) (int, bool) {
	i := offset
	for i < len(q) && (q[i] == ' ' || q[i] == '\t') {
		i++
	}
	switch {
	case i == len(q):
		return i, true
	case q[i] == '\n':
		return i + 1, true
	case q[i] == '\r' && i+1 < len(q) && q[i+1] == '\n':
		return i + 2, true
	case comment && strings.HasPrefix(q[i:], "--"):
		if nl := strings.IndexByte(q[i:], '\n'); nl >= 0 {
			return i + nl + 1, true
		}
		return len(q), true
	}
	return 0, false
}

// goLine reads a GO line: the word alone at the start of a line, with at most
// a count and a comment after it. It returns how many times the batch runs and
// where the next line begins.
func goLine(q string, t sqlToken) (count, next int, ok bool, err error) {
	if !lineStartsAt(q, t.start) {
		return 0, 0, false, nil
	}
	i := t.end
	for i < len(q) && (q[i] == ' ' || q[i] == '\t') {
		i++
	}
	digits := i
	for i < len(q) && isDigit(q[i]) {
		i++
	}
	if next, ok = lineEndsAt(q, i, true); !ok {
		return 0, 0, false, nil
	}
	count = 1
	if written := q[digits:i]; written != "" {
		count, err = strconv.Atoi(written)
		if err != nil || count < 1 || count > maxScriptStatements {
			return 0, 0, false, fmt.Errorf("GO %s: a batch runs at least once and at most %d times", written, maxScriptStatements)
		}
	}
	return count, next, true, nil
}

// slashLine reads a line holding only /, which ends a statement in SQL*Plus.
func slashLine(q string, t sqlToken) (int, bool) {
	if !lineStartsAt(q, t.start) {
		return 0, false
	}
	return lineEndsAt(q, t.end, false)
}
