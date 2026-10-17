package dbx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Reading a stored program back as the editor's fields.
//
// The editor can only write what it can state, so a program is read into the
// same RoutineSpec or TriggerSpec the planner renders from, and the read
// refuses — by name — anything the form would leave out when it wrote the
// program again: a Postgres function with a COST, a SQL Server procedure
// WITH ENCRYPTION, an Oracle function that is PIPELINED. A program that
// cannot be stated is still shown (the object page has its text); it is just
// not offered for editing, with the reason, instead of being silently changed
// by the first save.

// Program is one stored program as the editor reads it.
type Program struct {
	Kind     string       `json:"kind"`
	Editable bool         `json:"editable"`
	Reason   string       `json:"reason,omitempty"`
	Routine  *RoutineSpec `json:"routine,omitempty"`
	Trigger  *TriggerSpec `json:"trigger,omitempty"`
}

func notEditable(kind, format string, args ...any) *Program {
	return &Program{Kind: kind, Reason: fmt.Sprintf(format, args...)}
}

// ReadProgram reads a function, a procedure or a trigger for editing.
func ReadProgram(ctx context.Context, db *sql.DB, driver Driver, ref ObjectRef) (*Program, error) {
	if ref.Kind != KindFunction && ref.Kind != KindProcedure && ref.Kind != KindTrigger {
		return nil, fmt.Errorf("%w: only functions, procedures and triggers are edited", ErrInvalidObject)
	}
	op := OpTriggers
	if ref.Kind != KindTrigger {
		op, _ = programKindOp(ref.Kind)
	}
	if _, err := ddlDialect(driver, op); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if strings.TrimSpace(ref.Name) == "" {
		return nil, fmt.Errorf("%w: a name is required", ErrInvalidObject)
	}
	var program *Program
	var err error
	switch driver {
	case DriverPostgres:
		if ref.Kind == KindTrigger {
			program, err = readPGTrigger(ctx, db, ref)
		} else {
			program, err = readPGRoutine(ctx, db, ref)
		}
	case DriverMySQL:
		if ref.Kind == KindTrigger {
			program, err = readMySQLTrigger(ctx, db, ref)
		} else {
			program, err = readMySQLRoutine(ctx, db, ref)
		}
	case DriverMSSQL:
		program, err = readMSSQLProgram(ctx, db, ref)
	case DriverOracle:
		program, err = readOracleProgram(ctx, db, ref)
	case DriverSQLite:
		program, err = readSQLiteTrigger(ctx, db, ref)
	default:
		return nil, fmt.Errorf("%w: %s has no stored programs", ErrUnsupported, driver)
	}
	if err != nil {
		return nil, err
	}
	if program.Reason == "" {
		program.Editable = true
		if err := checkProgram(driver, program); err != nil {
			program.Editable, program.Reason = false, err.Error()
		}
	}
	return program, nil
}

// checkProgram holds a program that was read to what the planner will accept
// back, so the form is never offered one it could not save.
func checkProgram(driver Driver, p *Program) error {
	switch {
	case p.Routine != nil:
		r := p.Routine
		d, _ := DialectFor(driver)
		for _, a := range r.Args {
			if err := validateType(driver, a.Type); err != nil {
				return fmt.Errorf("its parameter %q has the type %q, which this form cannot build", a.Name, a.Type)
			}
			if a.Default != "" {
				if _, err := validateFragment(driver, "its default for "+a.Name, a.Default); err != nil {
					return err
				}
			}
		}
		if r.Kind == KindFunction && (driver != DriverPostgres || !r.hasOutput()) {
			if _, err := returnsClause(driver, d, r.Returns); err != nil {
				return fmt.Errorf("its return type %q is not one this form can build", r.Returns)
			}
		}
		_, err := renderRoutine(driver, *r, "CREATE")
		return err
	case p.Trigger != nil:
		_, err := renderTrigger(driver, *p.Trigger, "CREATE")
		return err
	}
	return nil
}

// --- Postgres ---------------------------------------------------------------

type pgRoutineRow struct {
	prokind, lang, src, volatility, returns, arguments, comment, schema, parallel string
	secdef, strict, retset, leakproof, hasConfig, support, sqlBody, transforms    bool
	cost, rows                                                                    float64
	names, modes, types                                                           string
}

func readPGRoutine(ctx context.Context, db *sql.DB, ref ObjectRef) (*Program, error) {
	match := "p.prokind <> 'p'"
	if ref.Kind == KindProcedure {
		match = "p.prokind = 'p'"
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT p.prokind::text, l.lanname, COALESCE(p.prosrc, ''), p.provolatile::text,
	         COALESCE(pg_get_function_result(p.oid), ''), pg_get_function_arguments(p.oid),
	         COALESCE(obj_description(p.oid, 'pg_proc'), ''), n.nspname, p.proparallel::text,
	         p.prosecdef, p.proisstrict, p.proretset, p.proleakproof, p.proconfig IS NOT NULL,
	         COALESCE(to_jsonb(p)->>'prosupport', '-') <> '-',
	         to_jsonb(p)->>'prosqlbody' IS NOT NULL,
	         to_jsonb(p)->>'protrftypes' IS NOT NULL,
	         p.procost::float8, p.prorows::float8,
	         COALESCE(to_json(p.proargnames)::text, 'null'),
	         COALESCE(to_json(p.proargmodes::text[])::text, 'null'),
	         to_json(ARRAY(SELECT format_type(u.t, NULL)
	                       FROM unnest(COALESCE(p.proallargtypes, p.proargtypes::oid[])) WITH ORDINALITY u(t, i)
	                       ORDER BY u.i))::text
	  FROM pg_proc p
	  JOIN pg_namespace n ON n.oid = p.pronamespace
	  JOIN pg_language l ON l.oid = p.prolang
	  WHERE `+match+` AND `+pgSchemaIs("n")+` AND p.proname = $2
	    AND ($3::text = '' OR pg_get_function_identity_arguments(p.oid) = $3::text)`,
		ref.Schema, ref.Name, ref.Signature)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []pgRoutineRow
	for rows.Next() {
		var r pgRoutineRow
		if err := rows.Scan(&r.prokind, &r.lang, &r.src, &r.volatility, &r.returns, &r.arguments, &r.comment, &r.schema,
			&r.parallel, &r.secdef, &r.strict, &r.retset, &r.leakproof, &r.hasConfig, &r.support, &r.sqlBody,
			&r.transforms, &r.cost, &r.rows, &r.names, &r.modes, &r.types); err != nil {
			return nil, err
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(found) {
	case 0:
		return nil, ErrObjectNotFound
	case 1:
	default:
		return nil, fmt.Errorf("%w: %s has %d forms — name one by its signature", ErrAmbiguousObject, ref.Name, len(found))
	}
	r := found[0]
	kind := ref.Kind
	switch {
	case r.prokind == "a":
		return notEditable(kind, "it is an aggregate"), nil
	case r.prokind == "w":
		return notEditable(kind, "it is a window function"), nil
	case r.lang == "internal" || r.lang == "c":
		return notEditable(kind, "it is written in %s", r.lang), nil
	case r.sqlBody:
		return notEditable(kind, "it has a SQL-standard body (BEGIN ATOMIC), which is not text a form can hold"), nil
	case r.cost != 100:
		return notEditable(kind, "it sets COST %g", r.cost), nil
	case r.retset && r.rows != 1000, !r.retset && r.rows != 0:
		return notEditable(kind, "it sets ROWS %g", r.rows), nil
	case r.parallel != "u":
		return notEditable(kind, "it sets a PARALLEL mode"), nil
	case r.leakproof:
		return notEditable(kind, "it is LEAKPROOF"), nil
	case r.hasConfig:
		return notEditable(kind, "it has SET options"), nil
	case r.support:
		return notEditable(kind, "it has a planner support function"), nil
	case r.transforms:
		return notEditable(kind, "it has TRANSFORM types"), nil
	}
	var names, modes, types []*string
	_ = json.Unmarshal([]byte(r.names), &names)
	_ = json.Unmarshal([]byte(r.modes), &modes)
	_ = json.Unmarshal([]byte(r.types), &types)
	var listed []string
	if strings.TrimSpace(r.arguments) != "" {
		listed = splitTop(r.arguments, ',')
	}
	spec := &RoutineSpec{
		Kind: kind, Schema: r.schema, Name: ref.Name, Language: r.lang, Body: r.src,
		Volatility: map[string]string{"i": "immutable", "s": "stable", "v": "volatile"}[r.volatility],
		Strict:     r.strict, Comment: r.comment, Args: []RoutineArg{},
	}
	if r.secdef {
		spec.Security = "definer"
	}
	table := false
	n := 0
	for i, typ := range types {
		mode := "i"
		if i < len(modes) && modes[i] != nil {
			mode = *modes[i]
		}
		switch mode {
		case "t":
			table = true
			continue
		case "v":
			return notEditable(kind, "it has a VARIADIC parameter"), nil
		}
		arg := RoutineArg{Mode: map[string]string{"i": "in", "o": "out", "b": "inout"}[mode], Type: *typ}
		if i < len(names) && names[i] != nil {
			arg.Name = *names[i]
		}
		if n >= len(listed) {
			return notEditable(kind, "its parameter list could not be matched to the catalogue"), nil
		}
		if at := indexTopWord(listed[n], "default"); at >= 0 {
			arg.Default = strings.TrimSpace(listed[n][at+len("default"):])
		}
		n++
		spec.Args = append(spec.Args, arg)
	}
	if n != len(listed) {
		return notEditable(kind, "its parameter list could not be matched to the catalogue"), nil
	}
	if kind == KindFunction && (table || !spec.hasOutput()) {
		spec.Returns = r.returns
	}
	return &Program{Kind: kind, Routine: spec}, nil
}

// indexTopWord finds a keyword outside quotes and brackets, as a whole word,
// and returns where it starts, or -1.
func indexTopWord(s, word string) int {
	depth := 0
	var quote byte
	lower := strings.ToLower(s)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case depth == 0 && strings.HasPrefix(lower[i:], word):
			before := i == 0 || !isWordByte(lower[i-1])
			end := i + len(word)
			after := end >= len(lower) || !isWordByte(lower[end])
			if before && after {
				return i
			}
		}
	}
	return -1
}

func isWordByte(c byte) bool { return isASCIILetter(c) || isASCIIDigit(c) || c == '_' }

func readPGTrigger(ctx context.Context, db *sql.DB, ref ObjectRef) (*Program, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT n.nspname, c.relname, t.tgtype::int, t.tgenabled::text, t.tgconstraint::bigint,
	         pn.nspname, p.proname, pg_get_triggerdef(t.oid, true),
	         t.tgattr::text,
	         COALESCE(t.tgoldtable, ''), COALESCE(t.tgnewtable, ''),
	         COALESCE(obj_description(t.oid, 'pg_trigger'), '')
	  FROM pg_trigger t
	  JOIN pg_class c ON c.oid = t.tgrelid
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  JOIN pg_proc p ON p.oid = t.tgfoid
	  JOIN pg_namespace pn ON pn.oid = p.pronamespace
	  WHERE NOT t.tgisinternal AND `+pgSchemaIs("n")+` AND t.tgname = $2
	    AND ($3::text = '' OR c.relname = $3::text)`, ref.Schema, ref.Name, ref.Table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type found struct {
		schema, table, enabled, fnSchema, fn, def, attrs, oldTable, newTable, comment string
		tgtype                                                                        int
		constraint                                                                    int64
	}
	var all []found
	for rows.Next() {
		var f found
		if err := rows.Scan(&f.schema, &f.table, &f.tgtype, &f.enabled, &f.constraint, &f.fnSchema, &f.fn,
			nullText{&f.def}, &f.attrs, &f.oldTable, &f.newTable, &f.comment); err != nil {
			return nil, err
		}
		all = append(all, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(all) {
	case 0:
		return nil, ErrObjectNotFound
	case 1:
	default:
		return nil, fmt.Errorf("%w: triggers named %s exist on more than one table — name the table", ErrAmbiguousObject, ref.Name)
	}
	f := all[0]
	switch {
	case f.constraint != 0:
		return notEditable(KindTrigger, "it is a constraint trigger"), nil
	case f.attrs != "":
		return notEditable(KindTrigger, "it fires on UPDATE OF some columns"), nil
	case f.oldTable != "" || f.newTable != "":
		return notEditable(KindTrigger, "it has a REFERENCING transition table"), nil
	case f.enabled != "O":
		return notEditable(KindTrigger, "it is disabled or replica-only, and saving would enable it"), nil
	}
	spec := &TriggerSpec{
		Schema: f.schema, Name: ref.Name, Table: f.table, Function: f.fn, FunctionSchema: f.fnSchema,
		Condition: pgTriggerWhen(f.def), Comment: f.comment, Timing: "after", ForEach: "statement", Events: []string{},
	}
	switch {
	case f.tgtype&2 != 0:
		spec.Timing = "before"
	case f.tgtype&64 != 0:
		spec.Timing = "instead of"
	}
	if f.tgtype&1 != 0 {
		spec.ForEach = "row"
	}
	for _, e := range []struct {
		bit  int
		name string
	}{{4, "insert"}, {16, "update"}, {8, "delete"}, {32, "truncate"}} {
		if f.tgtype&e.bit != 0 {
			spec.Events = append(spec.Events, e.name)
		}
	}
	lower := strings.ToLower(f.def)
	at := strings.LastIndex(lower, "execute function ")
	if at < 0 {
		at = strings.LastIndex(lower, "execute procedure ")
	}
	if at < 0 {
		return notEditable(KindTrigger, "its definition could not be read"), nil
	}
	call := f.def[at:]
	open := strings.IndexByte(call, '(')
	closing := strings.LastIndexByte(call, ')')
	if open < 0 || closing < open {
		return notEditable(KindTrigger, "its definition could not be read"), nil
	}
	if inner := strings.TrimSpace(call[open+1 : closing]); inner != "" {
		for _, arg := range splitTop(inner, ',') {
			arg = strings.TrimSpace(arg)
			if len(arg) < 2 || arg[0] != '\'' || arg[len(arg)-1] != '\'' {
				return notEditable(KindTrigger, "it passes an argument that is not a string"), nil
			}
			spec.FunctionArgs = append(spec.FunctionArgs, strings.ReplaceAll(arg[1:len(arg)-1], "''", "'"))
		}
	}
	return &Program{Kind: KindTrigger, Trigger: spec}, nil
}

// pgTriggerWhen is the condition of a trigger definition: the parenthesised
// text after its WHEN. The catalogue's own expression cannot be printed alone,
// because a condition names both OLD and NEW.
func pgTriggerWhen(def string) string {
	at := indexTopWord(def, "when")
	if at < 0 {
		return ""
	}
	rest := strings.TrimSpace(def[at+len("when"):])
	if !strings.HasPrefix(rest, "(") {
		return ""
	}
	depth := 0
	var quote byte
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			if depth--; depth == 0 {
				return strings.TrimSpace(rest[1:i])
			}
		}
	}
	return ""
}

// --- MySQL and MariaDB ------------------------------------------------------

func mysqlSchemaOf(ctx context.Context, db *sql.DB, schema string) (string, error) {
	if schema != "" {
		return schema, nil
	}
	var current sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&current); err != nil {
		return "", err
	}
	if current.String == "" {
		return "", fmt.Errorf("a database is required: this connection has none selected")
	}
	return current.String, nil
}

func readMySQLRoutine(ctx context.Context, db *sql.DB, ref ObjectRef) (*Program, error) {
	schema, err := mysqlSchemaOf(ctx, db, ref.Schema)
	if err != nil {
		return nil, err
	}
	routineType := strings.ToUpper(ref.Kind)
	var body, definition, returns, deterministic, access, security, comment string
	err = db.QueryRowContext(ctx, `
	  SELECT COALESCE(ROUTINE_BODY, ''), COALESCE(ROUTINE_DEFINITION, ''), COALESCE(DTD_IDENTIFIER, ''),
	         COALESCE(IS_DETERMINISTIC, ''), COALESCE(SQL_DATA_ACCESS, ''), COALESCE(SECURITY_TYPE, ''),
	         COALESCE(ROUTINE_COMMENT, '')
	  FROM information_schema.ROUTINES
	  WHERE ROUTINE_SCHEMA = ? AND ROUTINE_NAME = ? AND ROUTINE_TYPE = ?`, schema, ref.Name, routineType).
		Scan(&body, &definition, &returns, &deterministic, &access, &security, &comment)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	if body != "SQL" {
		return notEditable(ref.Kind, "it is an external %s routine", body), nil
	}
	if strings.TrimSpace(definition) == "" {
		return notEditable(ref.Kind, "the server returned no body for it: this login may not read it"), nil
	}
	spec := &RoutineSpec{
		Kind: ref.Kind, Schema: schema, Name: ref.Name, Body: definition,
		Deterministic: strings.EqualFold(deterministic, "YES"), DataAccess: strings.ToLower(access),
		Security: strings.ToLower(security), Comment: comment, Args: []RoutineArg{},
	}
	if ref.Kind == KindFunction {
		spec.Returns = returns
	}
	var schemaCharset string
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(DEFAULT_CHARACTER_SET_NAME, '') FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?`,
		schema).Scan(&schemaCharset)
	params, err := db.QueryContext(ctx, `
	  SELECT COALESCE(PARAMETER_MODE, ''), COALESCE(PARAMETER_NAME, ''), COALESCE(DTD_IDENTIFIER, ''),
	         COALESCE(CHARACTER_SET_NAME, '')
	  FROM information_schema.PARAMETERS
	  WHERE SPECIFIC_SCHEMA = ? AND SPECIFIC_NAME = ? AND ROUTINE_TYPE = ? AND ORDINAL_POSITION > 0
	  ORDER BY ORDINAL_POSITION`, schema, ref.Name, routineType)
	if err != nil {
		return nil, err
	}
	defer params.Close()
	for params.Next() {
		var mode, name, typ, charset string
		if err := params.Scan(&mode, &name, &typ, &charset); err != nil {
			return nil, err
		}
		if charset != "" && !strings.EqualFold(charset, schemaCharset) {
			return notEditable(ref.Kind, "its parameter %s has the character set %s, which this form cannot state", name, charset), nil
		}
		mode = strings.ToLower(mode)
		switch mode {
		case "", "in":
			mode = "in"
		case "out", "inout":
		default:
			return notEditable(ref.Kind, "its parameter %s has the mode %s", name, mode), nil
		}
		spec.Args = append(spec.Args, RoutineArg{Name: name, Mode: mode, Type: typ})
	}
	if err := params.Err(); err != nil {
		return nil, err
	}
	return &Program{Kind: ref.Kind, Routine: spec}, nil
}

func readMySQLTrigger(ctx context.Context, db *sql.DB, ref ObjectRef) (*Program, error) {
	schema, err := mysqlSchemaOf(ctx, db, ref.Schema)
	if err != nil {
		return nil, err
	}
	var event, timing, orientation, statement, tableSchema, table string
	var order int
	err = db.QueryRowContext(ctx, `
	  SELECT EVENT_MANIPULATION, ACTION_TIMING, COALESCE(ACTION_ORIENTATION, 'ROW'),
	         COALESCE(ACTION_STATEMENT, ''), COALESCE(ACTION_ORDER, 1), EVENT_OBJECT_SCHEMA, EVENT_OBJECT_TABLE
	  FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = ? AND TRIGGER_NAME = ?`, schema, ref.Name).
		Scan(&event, &timing, &orientation, &statement, &order, &tableSchema, &table)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	switch {
	case !strings.EqualFold(tableSchema, schema):
		return notEditable(KindTrigger, "it is on a table in another database"), nil
	case order > 1:
		return notEditable(KindTrigger, "it fires after another trigger on the same event (FOLLOWS)"), nil
	case strings.TrimSpace(statement) == "":
		return notEditable(KindTrigger, "the server returned no body for it"), nil
	}
	return &Program{Kind: KindTrigger, Trigger: &TriggerSpec{
		Schema: schema, Name: ref.Name, Table: table, Timing: strings.ToLower(timing),
		Events: []string{strings.ToLower(event)}, ForEach: strings.ToLower(orientation), Body: statement,
	}}, nil
}

// --- header scanning for engines that keep a program's text ------------------

// scan walks the tokens of a program's text, past its header.
type headerScan struct {
	driver Driver
	text   string
	toks   []sqlToken
	pos    int
}

func newHeaderScan(driver Driver, text string) (*headerScan, error) {
	toks, err := lexSQL(lexRulesFor(driver), text)
	if err != nil {
		return nil, err
	}
	return &headerScan{driver: driver, text: text, toks: toks}, nil
}

func (h *headerScan) more() bool { return h.pos < len(h.toks) }

func (h *headerScan) raw(i int) string { return h.text[h.toks[i].start:h.toks[i].end] }

// word is the lower-case word at the current token, or "".
func (h *headerScan) word() string {
	if h.pos < len(h.toks) && h.toks[h.pos].kind == tokWord {
		return strings.ToLower(h.raw(h.pos))
	}
	return ""
}

func (h *headerScan) take(word string) bool {
	if h.word() == word {
		h.pos++
		return true
	}
	return false
}

func (h *headerScan) punct(c byte) bool {
	if h.pos < len(h.toks) && h.toks[h.pos].kind == tokPunct && h.text[h.toks[h.pos].start] == c {
		h.pos++
		return true
	}
	return false
}

// name reads one identifier, quoted or not, and returns it unquoted and
// whether it was quoted.
func (h *headerScan) name() (string, bool, bool) {
	if h.pos >= len(h.toks) {
		return "", false, false
	}
	t := h.toks[h.pos]
	text := h.text[t.start:t.end]
	switch t.kind {
	case tokWord:
		h.pos++
		return text, false, true
	case tokQuoted:
		h.pos++
		open, closing := text[0], text[len(text)-1]
		inner := text[1 : len(text)-1]
		if open == '[' {
			closing = ']'
		}
		return strings.ReplaceAll(inner, string(closing)+string(closing), string(closing)), true, true
	}
	return "", false, false
}

// qualifiedName reads name or schema.name and returns the last part.
func (h *headerScan) qualifiedName() (string, bool) {
	name, _, ok := h.name()
	if !ok {
		return "", false
	}
	for h.punct('.') {
		if name, _, ok = h.name(); !ok {
			return "", false
		}
	}
	return name, true
}

// until moves to the next token at depth zero for which stop is true and
// returns its index, or -1. Parentheses are tracked.
func (h *headerScan) until(stop func(i int) bool) int {
	depth := 0
	for i := h.pos; i < len(h.toks); i++ {
		t := h.toks[i]
		if t.kind == tokPunct {
			switch h.text[t.start] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if depth == 0 && stop(i) {
			return i
		}
	}
	return -1
}

func (h *headerScan) isWord(i int, word string) bool {
	return h.toks[i].kind == tokWord && strings.EqualFold(h.raw(i), word)
}

// closing finds the parenthesis that closes the one just consumed, before
// token limit, or -1.
func (h *headerScan) closing(limit int) int {
	depth := 1
	for i := h.pos; i < limit; i++ {
		if h.toks[i].kind != tokPunct {
			continue
		}
		switch h.text[h.toks[i].start] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// span is the text from one token's start to another's, exclusive.
func (h *headerScan) span(from, to int) string {
	if from >= to || from >= len(h.toks) {
		return ""
	}
	return strings.TrimSpace(h.text[h.toks[from].start:h.toks[to-1].end])
}

// rest is the text after token i.
func (h *headerScan) rest(i int) string { return strings.TrimSpace(h.text[h.toks[i].end:]) }

var trailingOutputRe = regexp.MustCompile(`(?i)\s+(output|out)\s*$`)

// --- SQL Server -------------------------------------------------------------

func readMSSQLProgram(ctx context.Context, db *sql.DB, ref ObjectRef) (*Program, error) {
	types := "'P', 'FN', 'IF', 'TF'"
	if ref.Kind == KindTrigger {
		types = "'TR'"
	}
	var schema, kind, parent, parentSchema string
	var text sql.NullString
	var encrypted bool
	err := db.QueryRowContext(ctx, `
	  SELECT s.name, o.type, ISNULL(po.name, ''), ISNULL(ps.name, ''), m.definition,
	         CAST(CASE WHEN m.definition IS NULL THEN 1 ELSE 0 END AS BIT)
	  FROM sys.objects o
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  LEFT JOIN sys.sql_modules m ON m.object_id = o.object_id
	  LEFT JOIN sys.objects po ON po.object_id = o.parent_object_id
	  LEFT JOIN sys.schemas ps ON ps.schema_id = po.schema_id
	  WHERE o.type IN (`+types+`) AND `+mssqlSchemaIs("s")+` AND o.name = @p2
	    AND (@p3 = '' OR po.name = @p3)`, ref.Schema, ref.Name, ref.Table).
		Scan(&schema, &kind, &parent, &parentSchema, &text, &encrypted)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	if encrypted || strings.TrimSpace(text.String) == "" {
		return notEditable(ref.Kind, "the server returned no text for it: it is encrypted, or this login may not view its definition"), nil
	}
	if kind == "TF" {
		return notEditable(ref.Kind, "it is a multi-statement table function"), nil
	}
	return parseMSSQLModule(text.String, ref.Kind, schema, ref.Name, parent, parentSchema), nil
}

// parseMSSQLModule reads the text SQL Server kept for a procedure, function
// or trigger.
func parseMSSQLModule(text, kind, schema, name, parent, parentSchema string) *Program {
	what := kind
	h, err := newHeaderScan(DriverMSSQL, text)
	if err != nil {
		return notEditable(what, "its text could not be read: %v", err)
	}
	if !(h.take("create") || h.take("alter")) {
		return notEditable(what, "its text does not begin with CREATE")
	}
	if h.take("or") && !h.take("alter") {
		return notEditable(what, "its text could not be read")
	}
	if kind == KindTrigger {
		return readMSSQLTrigger(h, schema, name, parent, parentSchema)
	}
	if !(h.take("procedure") || h.take("proc") || h.take("function")) {
		return notEditable(what, "its text could not be read")
	}
	if _, ok := h.qualifiedName(); !ok {
		return notEditable(what, "its name could not be read")
	}
	// The AS that ends the header: not one that is part of "@p AS int".
	end := h.until(func(i int) bool {
		// Not the AS of "@p AS int" or of "EXECUTE AS OWNER".
		return h.isWord(i, "as") && !(i > 0 && h.toks[i-1].kind == tokWord &&
			(strings.HasPrefix(h.raw(i-1), "@") || strings.EqualFold(h.raw(i-1), "execute")))
	})
	if end < 0 {
		return notEditable(what, "its header could not be read")
	}
	with := h.until(func(i int) bool { return h.isWord(i, "with") || i >= end })
	optionsAt := end
	if with >= 0 && with < end {
		optionsAt = with
	}
	spec := &RoutineSpec{Kind: kind, Schema: schema, Name: name, Body: h.rest(end), Args: []RoutineArg{}}
	paramsEnd := optionsAt
	if kind == KindFunction {
		if !h.punct('(') {
			return notEditable(what, "its parameter list could not be read")
		}
		closing := h.closing(optionsAt)
		if closing < 0 {
			return notEditable(what, "its parameter list could not be read")
		}
		if err := mssqlParams(h, h.pos, closing, spec); err != nil {
			return notEditable(what, "%v", err)
		}
		h.pos = closing + 1
		if !h.take("returns") {
			return notEditable(what, "its return type could not be read")
		}
		spec.Returns = h.span(h.pos, optionsAt)
	} else {
		if err := mssqlParams(h, h.pos, paramsEnd, spec); err != nil {
			return notEditable(what, "%v", err)
		}
	}
	if optionsAt < end {
		opts := strings.ToLower(h.span(optionsAt+1, end))
		switch opts {
		case "execute as owner":
			spec.Security = "definer"
		case "execute as caller":
			spec.Security = "invoker"
		default:
			return notEditable(what, "it has the option WITH %s", strings.ToUpper(opts))
		}
	}
	return &Program{Kind: kind, Routine: spec}
}

// mssqlParams reads the parameters between two tokens into the spec.
func mssqlParams(h *headerScan, from, to int, spec *RoutineSpec) error {
	list := h.span(from, to)
	if strings.TrimSpace(list) == "" {
		return nil
	}
	if strings.HasPrefix(list, "(") && strings.HasSuffix(list, ")") {
		list = strings.TrimSpace(list[1 : len(list)-1])
	}
	for _, seg := range splitTop(list, ',') {
		seg = strings.TrimSpace(seg)
		if !strings.HasPrefix(seg, "@") {
			return fmt.Errorf("its parameter %q could not be read", seg)
		}
		arg := RoutineArg{Mode: "in"}
		if loc := trailingOutputRe.FindStringIndex(seg); loc != nil {
			arg.Mode = "out"
			seg = strings.TrimSpace(seg[:loc[0]])
		}
		name, rest, _ := strings.Cut(seg, " ")
		arg.Name = strings.TrimPrefix(name, "@")
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(strings.ToLower(rest), "as ") {
			rest = strings.TrimSpace(rest[3:])
		}
		if eq := indexTopRune(rest, '='); eq >= 0 {
			arg.Default = strings.TrimSpace(rest[eq+1:])
			rest = strings.TrimSpace(rest[:eq])
		}
		arg.Type = rest
		spec.Args = append(spec.Args, arg)
	}
	return nil
}

func indexTopRune(s string, want byte) int {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case depth == 0 && c == want:
			return i
		}
	}
	return -1
}

func readMSSQLTrigger(h *headerScan, schema, name, parent, parentSchema string) *Program {
	if !h.take("trigger") {
		return notEditable(KindTrigger, "its text could not be read")
	}
	if _, ok := h.qualifiedName(); !ok || !h.take("on") {
		return notEditable(KindTrigger, "its header could not be read")
	}
	if _, ok := h.qualifiedName(); !ok {
		return notEditable(KindTrigger, "its table could not be read")
	}
	if !strings.EqualFold(parentSchema, schema) {
		return notEditable(KindTrigger, "it is in a different schema from its table")
	}
	end := h.until(func(i int) bool { return h.isWord(i, "as") })
	if end < 0 {
		return notEditable(KindTrigger, "its header could not be read")
	}
	spec := &TriggerSpec{Schema: schema, Name: name, Table: parent, ForEach: "statement", Events: []string{}, Body: h.rest(end)}
	switch {
	case h.take("after"), h.take("for"):
		spec.Timing = "after"
	case h.take("instead"):
		if !h.take("of") {
			return notEditable(KindTrigger, "its timing could not be read")
		}
		spec.Timing = "instead of"
	default:
		return notEditable(KindTrigger, "its timing could not be read")
	}
	for h.pos < end {
		switch w := h.word(); w {
		case "insert", "update", "delete":
			spec.Events = append(spec.Events, w)
			h.pos++
			h.punct(',')
		default:
			return notEditable(KindTrigger, "it has the clause %s", strings.ToUpper(h.span(h.pos, end)))
		}
	}
	return &Program{Kind: KindTrigger, Trigger: spec}
}

// --- Oracle -----------------------------------------------------------------

func readOracleProgram(ctx context.Context, db *sql.DB, ref ObjectRef) (*Program, error) {
	types, ok := oracleObjectTypes[ref.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoDefinition, ref.Kind)
	}
	var owner, status string
	err := db.QueryRowContext(ctx, `
	  SELECT o.owner, o.status FROM all_objects o
	  WHERE `+oracleSchemaIs("o.owner")+` AND o.object_name = :2 AND o.object_type = :3`,
		oracleSchemaArg(ref.Schema), ref.Name, types[0]).Scan(&owner, nullText{&status})
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	text, err := queryText(ctx, db, "", `
	  SELECT s.text FROM all_source s WHERE s.owner = :1 AND s.name = :2 AND s.type = :3 ORDER BY s.line`,
		owner, ref.Name, types[0])
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return notEditable(ref.Kind, "the server returned no source for it: it is wrapped, or this login may not read it"), nil
	}
	if ref.Kind == KindTrigger {
		return readOracleTrigger(ctx, db, text, owner, ref.Name)
	}
	return parseOracleRoutine(text, ref.Kind, owner, ref.Name), nil
}

// parseOracleRoutine reads the source Oracle kept for a function or procedure.
func parseOracleRoutine(text, kind, owner, name string) *Program {
	h, err := newHeaderScan(DriverOracle, text)
	if err != nil {
		return notEditable(kind, "its text could not be read: %v", err)
	}
	if h.take("create") {
		h.take("or")
		h.take("replace")
		if h.take("editionable") || h.take("noneditionable") {
			return notEditable(kind, "it is editionable")
		}
	}
	if !h.take(kind) {
		return notEditable(kind, "its text could not be read")
	}
	if _, ok := h.qualifiedName(); !ok {
		return notEditable(kind, "its name could not be read")
	}
	spec := &RoutineSpec{Kind: kind, Schema: owner, Name: name, Args: []RoutineArg{}}
	if h.punct('(') {
		closing := h.closing(len(h.toks))
		if closing < 0 {
			return notEditable(kind, "its parameter list could not be read")
		}
		if err := oracleParams(h.span(h.pos, closing), spec); err != nil {
			return notEditable(kind, "%v", err)
		}
		h.pos = closing + 1
	}
	body := h.until(func(i int) bool { return h.isWord(i, "is") || h.isWord(i, "as") })
	if body < 0 {
		return notEditable(kind, "its header could not be read")
	}
	if kind == KindFunction {
		if !h.take("return") {
			return notEditable(kind, "its return type could not be read")
		}
		stop := h.until(func(i int) bool {
			return i >= body || h.isWord(i, "authid") || h.isWord(i, "deterministic")
		})
		if stop < 0 {
			stop = body
		}
		spec.Returns = h.span(h.pos, stop)
		if err := validateType(DriverOracle, spec.Returns); err != nil {
			return notEditable(kind, "its return type %q is not one this form can build", spec.Returns)
		}
		h.pos = stop
	}
	for h.pos < body {
		switch {
		case h.take("authid"):
			switch {
			case h.take("definer"):
				spec.Security = "definer"
			case h.take("current_user"):
				spec.Security = "invoker"
			default:
				return notEditable(kind, "its AUTHID could not be read")
			}
		case h.take("deterministic"):
			spec.Deterministic = true
		default:
			return notEditable(kind, "it has the clause %s", strings.ToUpper(h.span(h.pos, body)))
		}
	}
	spec.Body = h.rest(body)
	return &Program{Kind: kind, Routine: spec}
}

var oracleModeRe = regexp.MustCompile(`(?i)^(in\s+out|in|out)\s+(.*)$`)

// oracleParams reads an Oracle parameter list: name [mode] type [DEFAULT x].
func oracleParams(list string, spec *RoutineSpec) error {
	if strings.TrimSpace(list) == "" {
		return nil
	}
	for _, seg := range splitTop(list, ',') {
		seg = strings.TrimSpace(seg)
		var name string
		if strings.HasPrefix(seg, `"`) {
			end := strings.Index(seg[1:], `"`)
			if end < 0 {
				return fmt.Errorf("its parameter %q could not be read", seg)
			}
			name, seg = seg[1:1+end], strings.TrimSpace(seg[end+2:])
			if name != strings.ToUpper(name) {
				return fmt.Errorf("its parameter %q has a quoted lower-case name, which this form writes unquoted", name)
			}
		} else {
			var rest string
			name, rest, _ = strings.Cut(seg, " ")
			seg = strings.TrimSpace(rest)
		}
		arg := RoutineArg{Name: name, Mode: "in"}
		if m := oracleModeRe.FindStringSubmatch(seg); m != nil {
			arg.Mode = strings.ToLower(strings.Join(strings.Fields(m[1]), ""))
			seg = m[2]
			if strings.HasPrefix(strings.ToLower(seg), "nocopy") {
				return fmt.Errorf("its parameter %s is NOCOPY", name)
			}
		}
		cut := -1
		if at := indexTopWord(seg, "default"); at >= 0 {
			cut = at
			arg.Default = strings.TrimSpace(seg[at+len("default"):])
		} else if at := strings.Index(seg, ":="); at >= 0 {
			cut = at
			arg.Default = strings.TrimSpace(seg[at+2:])
		}
		if cut >= 0 {
			seg = seg[:cut]
		}
		arg.Type = strings.TrimSpace(seg)
		spec.Args = append(spec.Args, arg)
	}
	return nil
}

func readOracleTrigger(ctx context.Context, db *sql.DB, text, owner, name string) (*Program, error) {
	var triggerType, event, tableOwner, baseType, table, when, status, referencing string
	err := db.QueryRowContext(ctx, `
	  SELECT t.trigger_type, t.triggering_event, t.table_owner, t.base_object_type, t.table_name,
	         t.when_clause, t.status, t.referencing_names
	  FROM all_triggers t WHERE t.owner = :1 AND t.trigger_name = :2`, owner, name).
		Scan(&triggerType, &event, nullText{&tableOwner}, &baseType, nullText{&table}, nullText{&when}, &status, &referencing)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	trigger := strings.ToUpper(strings.TrimSpace(triggerType))
	switch {
	case baseType != "TABLE" && baseType != "VIEW":
		return notEditable(KindTrigger, "it is a %s trigger", strings.ToLower(baseType)), nil
	case !strings.EqualFold(tableOwner, owner):
		return notEditable(KindTrigger, "it is on a table in another schema"), nil
	case strings.Contains(trigger, "COMPOUND"):
		return notEditable(KindTrigger, "it is a compound trigger"), nil
	case strings.EqualFold(status, "DISABLED"):
		return notEditable(KindTrigger, "it is disabled, and saving would enable it"), nil
	case !strings.EqualFold(strings.Join(strings.Fields(referencing), " "), "REFERENCING NEW AS NEW OLD AS OLD"):
		return notEditable(KindTrigger, "it renames the NEW and OLD correlation names"), nil
	}
	spec := &TriggerSpec{Schema: owner, Name: name, Table: table, Condition: strings.TrimSpace(when), Events: []string{}}
	switch {
	case strings.HasPrefix(trigger, "BEFORE"):
		spec.Timing = "before"
	case strings.HasPrefix(trigger, "AFTER"):
		spec.Timing = "after"
	case strings.HasPrefix(trigger, "INSTEAD OF"):
		spec.Timing = "instead of"
	default:
		return notEditable(KindTrigger, "its timing %q could not be read", triggerType), nil
	}
	spec.ForEach = "statement"
	if strings.Contains(trigger, "EACH ROW") {
		spec.ForEach = "row"
	}
	for _, e := range strings.Split(strings.ToLower(event), " or ") {
		e = strings.TrimSpace(e)
		if e != "insert" && e != "update" && e != "delete" {
			return notEditable(KindTrigger, "it fires on %s", strings.ToUpper(e)), nil
		}
		spec.Events = append(spec.Events, e)
	}
	return parseOracleTriggerBody(text, spec), nil
}

// parseOracleTriggerBody finds the block in a trigger's source: it starts at
// the first DECLARE or BEGIN outside the header's own parentheses (a WHEN
// condition holds neither).
func parseOracleTriggerBody(text string, spec *TriggerSpec) *Program {
	h, err := newHeaderScan(DriverOracle, text)
	if err != nil {
		return notEditable(KindTrigger, "its text could not be read: %v", err)
	}
	start := h.until(func(i int) bool { return h.isWord(i, "declare") || h.isWord(i, "begin") || h.isWord(i, "call") })
	if start < 0 {
		return notEditable(KindTrigger, "its body could not be found")
	}
	if h.isWord(start, "call") {
		return notEditable(KindTrigger, "its body is a CALL")
	}
	spec.Body = strings.TrimSpace(h.text[h.toks[start].start:])
	return &Program{Kind: KindTrigger, Trigger: spec}
}

// --- SQLite -----------------------------------------------------------------

func readSQLiteTrigger(ctx context.Context, db *sql.DB, ref ObjectRef) (*Program, error) {
	prefix := ""
	if ref.Schema != "" && !strings.EqualFold(ref.Schema, "main") {
		d, _ := DialectFor(DriverSQLite)
		q, err := d.QuoteIdent(ref.Schema)
		if err != nil {
			return nil, err
		}
		prefix = q + "."
	}
	var text, table string
	err := db.QueryRowContext(ctx, `SELECT COALESCE(sql, ''), tbl_name FROM `+prefix+`sqlite_master WHERE type = 'trigger' AND name = ?`,
		ref.Name).Scan(&text, &table)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	schema := ref.Schema
	if schema == "" {
		schema = "main"
	}
	if prefix != "" {
		return notEditable(KindTrigger, "it is in the attached database %s, and this form changes main only", schema), nil
	}
	return parseSQLiteTrigger(text, schema, ref.Name, table), nil
}

// parseSQLiteTrigger reads the statement SQLite kept for a trigger.
func parseSQLiteTrigger(text, schema, name, table string) *Program {
	h, err := newHeaderScan(DriverSQLite, text)
	if err != nil {
		return notEditable(KindTrigger, "its text could not be read: %v", err)
	}
	if !h.take("create") {
		return notEditable(KindTrigger, "its text could not be read")
	}
	if h.take("temp") || h.take("temporary") {
		return notEditable(KindTrigger, "it is a temporary trigger")
	}
	if !h.take("trigger") {
		return notEditable(KindTrigger, "its text could not be read")
	}
	if h.take("if") {
		h.take("not")
		h.take("exists")
	}
	if _, ok := h.qualifiedName(); !ok {
		return notEditable(KindTrigger, "its name could not be read")
	}
	spec := &TriggerSpec{Schema: schema, Name: name, Table: table, ForEach: "row", Events: []string{}}
	switch {
	case h.take("before"):
		spec.Timing = "before"
	case h.take("after"):
		spec.Timing = "after"
	case h.take("instead"):
		h.take("of")
		spec.Timing = "instead of"
	default:
		spec.Timing = "after"
	}
	switch w := h.word(); w {
	case "insert", "update", "delete":
		spec.Events = append(spec.Events, w)
		h.pos++
	default:
		return notEditable(KindTrigger, "its event could not be read")
	}
	if spec.Events[0] == "update" && h.take("of") {
		for {
			column, _, ok := h.name()
			if !ok {
				return notEditable(KindTrigger, "its UPDATE OF list could not be read")
			}
			spec.UpdateOf = append(spec.UpdateOf, column)
			if !h.punct(',') {
				break
			}
		}
	}
	if !h.take("on") {
		return notEditable(KindTrigger, "its table could not be read")
	}
	if _, ok := h.qualifiedName(); !ok {
		return notEditable(KindTrigger, "its table could not be read")
	}
	if h.take("for") && !(h.take("each") && h.take("row")) {
		return notEditable(KindTrigger, "its FOR EACH clause could not be read")
	}
	begin := h.until(func(i int) bool { return h.isWord(i, "begin") })
	if begin < 0 {
		return notEditable(KindTrigger, "its BEGIN could not be found")
	}
	if h.take("when") {
		spec.Condition = h.span(h.pos, begin)
	} else if h.pos != begin {
		return notEditable(KindTrigger, "it has the clause %s", strings.ToUpper(h.span(h.pos, begin)))
	}
	last := len(h.toks) - 1
	for last > begin && h.toks[last].kind == tokSemicolon {
		last--
	}
	if last <= begin || !h.isWord(last, "end") {
		return notEditable(KindTrigger, "its END could not be found")
	}
	spec.Body = strings.TrimSpace(h.text[h.toks[begin].end:h.toks[last].start])
	return &Program{Kind: KindTrigger, Trigger: spec}
}
