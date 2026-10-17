package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Stored programs: functions, procedures and triggers.
//
// A routine's body is code the engine will run later, with the rights of
// whoever owns it. That is the one thing a schema form must not make cheaper
// than the console: the query runner classifies every CREATE of one as high
// risk, so the routes in front of these plans ask for the destructive
// capability as well, and the plan itself only keeps the statement in one
// piece. The header is built from validated parts exactly as the other DDL
// forms build theirs; the body is the operator's own text and is placed where
// it cannot end the statement it belongs to (a dollar-quote tag the body does
// not contain on Postgres, the rest of the statement everywhere else, where
// the engine reads one statement and refuses a second).

// Operations a stored-program form is advertised under.
const (
	OpFunctions  = "functions"
	OpProcedures = "procedures"
	OpTriggers   = "triggers"
)

// RoutineArg is one parameter of a function or procedure.
type RoutineArg struct {
	Name string `json:"name"`
	// Mode is in, out or inout.
	Mode string `json:"mode"`
	Type string `json:"type"`
	// Default is an expression, and empty when the parameter has none.
	Default string `json:"default,omitempty"`
}

// RoutineSpec is a function or procedure as the editor states it. Fields an
// engine has no word for stay empty, and the planner refuses one it was given
// that the engine cannot say rather than leave it out.
type RoutineSpec struct {
	Kind    string       `json:"kind"`
	Schema  string       `json:"schema"`
	Name    string       `json:"name"`
	Args    []RoutineArg `json:"args"`
	Returns string       `json:"returns,omitempty"`
	// Language is Postgres's: plpgsql, sql, … Every other engine has one.
	Language string `json:"language,omitempty"`
	Body     string `json:"body"`
	// Volatility is Postgres's: volatile, stable or immutable.
	Volatility string `json:"volatility,omitempty"`
	// Strict is Postgres's RETURNS NULL ON NULL INPUT.
	Strict bool `json:"strict,omitempty"`
	// Deterministic is MySQL's and Oracle's.
	Deterministic bool `json:"deterministic,omitempty"`
	// DataAccess is MySQL's: contains sql, no sql, reads sql data or modifies sql data.
	DataAccess string `json:"dataAccess,omitempty"`
	// Security is definer or invoker, and empty for the engine's default.
	Security string `json:"security,omitempty"`
	// Comment is what the object already carries. A form never states one; an
	// edit that has to drop and create the object states it again so the
	// comment survives.
	Comment string `json:"comment,omitempty"`
}

// TriggerSpec is a trigger as the editor states it.
type TriggerSpec struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Table  string `json:"table"`
	// Timing is before, after or instead of.
	Timing string `json:"timing"`
	// Events are insert, update, delete and, on Postgres, truncate.
	Events []string `json:"events"`
	// UpdateOf narrows an UPDATE to some columns. SQLite only.
	UpdateOf []string `json:"updateOf,omitempty"`
	// ForEach is row or statement.
	ForEach   string `json:"forEach"`
	Condition string `json:"condition,omitempty"`
	// Function, FunctionSchema and FunctionArgs name what a Postgres trigger executes.
	Function       string   `json:"function,omitempty"`
	FunctionSchema string   `json:"functionSchema,omitempty"`
	FunctionArgs   []string `json:"functionArgs,omitempty"`
	// Body is the trigger's code on every engine that writes one. SQLite's is
	// the statements between BEGIN and END, which the plan supplies.
	Body    string `json:"body,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// RoutineRef names the routine an edit replaces. Signature tells overloads
// apart, as the catalogue reports it.
type RoutineRef struct {
	Schema, Name, Signature string
}

// TriggerRef names the trigger an edit replaces.
type TriggerRef struct {
	Schema, Name, Table string
}

const maxProgramBody = 256 << 10

var (
	routineLanguageRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	// Argument names are written without quotes where the engine folds them:
	// Postgres to lower case and Oracle to upper, which is what keeps a body
	// able to say them in any case. SQL Server's carry an @.
	argNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
	pgBareRe  = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	oraBareRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$#]{0,29}$`)
)

func programKindOp(kind string) (string, error) {
	switch kind {
	case KindFunction:
		return OpFunctions, nil
	case KindProcedure:
		return OpProcedures, nil
	}
	return "", fmt.Errorf("a routine is a function or a procedure, not %q", kind)
}

func squashed(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// validateProgramBody holds a body to text the engine can be sent whole.
func validateProgramBody(what, body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("%s is required", what)
	}
	if len(body) > maxProgramBody {
		return "", fmt.Errorf("%s is longer than this form sends (%d KiB)", what, maxProgramBody>>10)
	}
	if !utf8.ValidString(body) {
		return "", fmt.Errorf("%s is not valid text", what)
	}
	if strings.ContainsRune(body, 0) {
		return "", fmt.Errorf("%s contains a NUL byte", what)
	}
	return body, nil
}

// bodyDollarTag finds a tag that opens and closes a Postgres body without the
// body containing it.
func bodyDollarTag(body string) string {
	for i := 0; ; i++ {
		tag := "jd"
		if i > 0 {
			tag = fmt.Sprintf("jd%d", i)
		}
		if !strings.Contains(body, "$"+tag) {
			return "$" + tag + "$"
		}
	}
}

// argName renders a parameter's name the way the engine reads one.
func argName(driver Driver, d Dialect, name string) (string, error) {
	switch driver {
	case DriverMSSQL:
		if name == "" {
			return "", fmt.Errorf("every parameter needs a name")
		}
		if !argNameRe.MatchString(name) {
			return "", fmt.Errorf("parameter name %q must be letters, digits and underscores", name)
		}
		return "@" + name, nil
	case DriverOracle:
		if name == "" {
			return "", fmt.Errorf("every parameter needs a name")
		}
		if oraBareRe.MatchString(name) {
			return name, nil
		}
		return d.QuoteIdent(name)
	case DriverPostgres:
		if pgBareRe.MatchString(name) {
			return name, nil
		}
	}
	if name == "" {
		if driver == DriverPostgres {
			return "", nil
		}
		return "", fmt.Errorf("every parameter needs a name")
	}
	return d.QuoteIdent(name)
}

func (r RoutineSpec) hasOutput() bool {
	return slices.ContainsFunc(r.Args, func(a RoutineArg) bool { return a.Mode == "out" || a.Mode == "inout" })
}

// returnsClause validates and renders a function's return type.
func returnsClause(driver Driver, d Dialect, returns string) (string, error) {
	returns = strings.TrimSpace(returns)
	if returns == "" {
		return "", fmt.Errorf("a function's return type is required")
	}
	if driver == DriverMSSQL && strings.EqualFold(returns, "table") {
		return "TABLE", nil
	}
	if driver == DriverPostgres {
		head, rest, _ := strings.Cut(returns, " ")
		if strings.EqualFold(head, "setof") {
			if err := validateType(driver, rest); err != nil {
				return "", fmt.Errorf("return type %q is not one this form can build", returns)
			}
			return "SETOF " + strings.TrimSpace(rest), nil
		}
		if len(returns) > 6 && strings.EqualFold(returns[:6], "table(") && strings.HasSuffix(returns, ")") {
			parts := splitTop(returns[6:len(returns)-1], ',')
			cols := make([]string, 0, len(parts))
			for _, part := range parts {
				name, typ, ok := strings.Cut(strings.TrimSpace(part), " ")
				name = strings.Trim(name, `"`)
				if !ok || name == "" {
					return "", fmt.Errorf("a TABLE return type lists columns as: name type")
				}
				q, err := d.QuoteIdent(name)
				if err != nil {
					return "", err
				}
				if err := validateType(driver, typ); err != nil {
					return "", err
				}
				cols = append(cols, q+" "+strings.TrimSpace(typ))
			}
			if len(cols) == 0 {
				return "", fmt.Errorf("a TABLE return type needs at least one column")
			}
			return "TABLE(" + strings.Join(cols, ", ") + ")", nil
		}
	}
	if err := validateType(driver, returns); err != nil {
		return "", fmt.Errorf("return type %q is not one this form can build; use the Query tab for it", returns)
	}
	return returns, nil
}

// splitTop splits on a separator outside quotes and brackets.
func splitTop(s string, sep byte) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
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
		case c == sep && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// routineArgs renders the parameter list's entries for one engine.
func routineArgs(driver Driver, d Dialect, spec RoutineSpec) ([]string, error) {
	out := make([]string, 0, len(spec.Args))
	for i, a := range spec.Args {
		mode := a.Mode
		if mode == "" {
			mode = "in"
		}
		if mode != "in" && mode != "out" && mode != "inout" {
			return nil, fmt.Errorf("parameter %d: mode must be in, out or inout", i+1)
		}
		if spec.Kind == KindFunction && mode != "in" && driver != DriverPostgres && driver != DriverOracle {
			return nil, fmt.Errorf("a %s function takes input parameters only; a value comes back through RETURNS", driver)
		}
		if err := validateType(driver, a.Type); err != nil {
			return nil, fmt.Errorf("parameter %d: %w", i+1, err)
		}
		name, err := argName(driver, d, strings.TrimSpace(a.Name))
		if err != nil {
			return nil, fmt.Errorf("parameter %d: %w", i+1, err)
		}
		var def string
		if strings.TrimSpace(a.Default) != "" {
			if driver == DriverMySQL {
				return nil, fmt.Errorf("MySQL parameters have no default values")
			}
			if mode == "out" {
				return nil, fmt.Errorf("parameter %d: an OUT parameter has no default", i+1)
			}
			if def, err = validateFragment(driver, fmt.Sprintf("parameter %d's default", i+1), a.Default); err != nil {
				return nil, err
			}
		}
		typ := strings.TrimSpace(a.Type)
		var text string
		switch driver {
		case DriverPostgres:
			parts := []string{}
			if mode != "in" {
				parts = append(parts, strings.ToUpper(mode))
			}
			if name != "" {
				parts = append(parts, name)
			}
			text = strings.Join(append(parts, typ), " ")
			if def != "" {
				text += " DEFAULT " + def
			}
		case DriverMySQL:
			text = name + " " + typ
			if spec.Kind == KindProcedure {
				text = strings.ToUpper(mode) + " " + text
			}
		case DriverMSSQL:
			text = name + " " + typ
			if def != "" {
				text += " = " + def
			}
			if mode != "in" {
				text += " OUTPUT"
			}
		case DriverOracle:
			text = name + " " + map[string]string{"in": "IN", "out": "OUT", "inout": "IN OUT"}[mode] + " " + typ
			if def != "" {
				text += " DEFAULT " + def
			}
		default:
			return nil, fmt.Errorf("%s has no stored routines", driver)
		}
		out = append(out, text)
	}
	return out, nil
}

// renderRoutine writes the CREATE statement for a routine. create is the
// verb phrase in front of the object's kind: CREATE, CREATE OR REPLACE or
// CREATE OR ALTER.
func renderRoutine(driver Driver, spec RoutineSpec, create string) (string, error) {
	op, err := programKindOp(spec.Kind)
	if err != nil {
		return "", err
	}
	d, err := ddlDialect(driver, op)
	if err != nil {
		return "", err
	}
	rel, err := ddlRel(d, spec.Schema, spec.Name)
	if err != nil {
		return "", err
	}
	args, err := routineArgs(driver, d, spec)
	if err != nil {
		return "", err
	}
	word := strings.ToUpper(spec.Kind)
	var returns string
	if spec.Kind == KindFunction && (driver != DriverPostgres || !spec.hasOutput() || strings.TrimSpace(spec.Returns) != "") {
		if returns, err = returnsClause(driver, d, spec.Returns); err != nil {
			return "", err
		}
	}
	if spec.Kind == KindProcedure && strings.TrimSpace(spec.Returns) != "" {
		return "", fmt.Errorf("a procedure returns nothing; pass values back through OUT parameters")
	}
	if spec.Security != "" && spec.Security != "definer" && spec.Security != "invoker" {
		return "", fmt.Errorf("security must be definer or invoker")
	}
	body, err := validateProgramBody("the body", spec.Body)
	if err != nil {
		return "", err
	}
	head := create + " " + word + " " + rel
	switch driver {
	case DriverPostgres:
		lang := strings.ToLower(strings.TrimSpace(spec.Language))
		if !routineLanguageRe.MatchString(lang) {
			return "", fmt.Errorf("language %q is not a language name", spec.Language)
		}
		lines := []string{head + "(" + strings.Join(args, ", ") + ")"}
		if returns != "" {
			lines = append(lines, "RETURNS "+returns)
		}
		lines = append(lines, "LANGUAGE "+lang)
		if spec.Kind == KindFunction {
			switch spec.Volatility {
			case "":
			case "volatile", "stable", "immutable":
				lines = append(lines, strings.ToUpper(spec.Volatility))
			default:
				return "", fmt.Errorf("volatility must be volatile, stable or immutable")
			}
			if spec.Strict {
				lines = append(lines, "STRICT")
			}
		}
		if spec.Security != "" {
			lines = append(lines, "SECURITY "+strings.ToUpper(spec.Security))
		}
		tag := bodyDollarTag(body)
		lines = append(lines, "AS "+tag+body+tag)
		return strings.Join(lines, "\n"), nil

	case DriverMySQL:
		lines := []string{head + "(" + strings.Join(args, ", ") + ")"}
		if returns != "" {
			lines[0] += " RETURNS " + returns
		}
		var traits []string
		if spec.Deterministic {
			traits = append(traits, "DETERMINISTIC")
		}
		switch spec.DataAccess {
		case "":
		case "contains sql", "no sql", "reads sql data", "modifies sql data":
			traits = append(traits, strings.ToUpper(spec.DataAccess))
		default:
			return "", fmt.Errorf("data access must be contains sql, no sql, reads sql data or modifies sql data")
		}
		if spec.Security != "" {
			traits = append(traits, "SQL SECURITY "+strings.ToUpper(spec.Security))
		}
		if spec.Comment != "" {
			c, err := ddlLiteral(driver, "the comment", spec.Comment)
			if err != nil {
				return "", err
			}
			traits = append(traits, "COMMENT "+c)
		}
		lines = append(lines, traits...)
		lines = append(lines, strings.TrimRight(body, "; \t\r\n"))
		return strings.Join(lines, "\n"), nil

	case DriverMSSQL:
		var header string
		if spec.Kind == KindFunction {
			header = head + "(" + strings.Join(args, ", ") + ")\nRETURNS " + returns
		} else {
			header = head
			if len(args) > 0 {
				header += " " + strings.Join(args, ", ")
			}
		}
		lines := []string{header}
		if spec.Security != "" {
			lines = append(lines, "WITH EXECUTE AS "+map[string]string{"definer": "OWNER", "invoker": "CALLER"}[spec.Security])
		}
		lines = append(lines, "AS", strings.TrimRight(body, "; \t\r\n"))
		return strings.Join(lines, "\n"), nil

	case DriverOracle:
		header := head
		if len(args) > 0 {
			header += "(" + strings.Join(args, ", ") + ")"
		}
		if returns != "" {
			header += " RETURN " + returns
		}
		lines := []string{header}
		if spec.Security != "" {
			lines = append(lines, "AUTHID "+map[string]string{"definer": "DEFINER", "invoker": "CURRENT_USER"}[spec.Security])
		}
		if spec.Deterministic {
			if spec.Kind != KindFunction {
				return "", fmt.Errorf("only a function can be DETERMINISTIC")
			}
			lines = append(lines, "DETERMINISTIC")
		}
		lines = append(lines, "IS", body)
		return strings.Join(lines, "\n"), nil
	}
	return "", fmt.Errorf("%s has no stored routines", driver)
}

var triggerEventOrder = []string{"insert", "update", "delete", "truncate"}

func triggerEvents(driver Driver, events []string) ([]string, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("a trigger fires on at least one event")
	}
	var out []string
	for _, e := range triggerEventOrder {
		if slices.Contains(events, e) {
			out = append(out, e)
		}
	}
	if len(out) != len(events) {
		return nil, fmt.Errorf("a trigger's events are insert, update, delete and truncate, each once")
	}
	if slices.Contains(out, "truncate") && driver != DriverPostgres {
		return nil, fmt.Errorf("only Postgres fires a trigger on TRUNCATE")
	}
	if (driver == DriverMySQL || driver == DriverSQLite) && len(out) != 1 {
		return nil, fmt.Errorf("a %s trigger fires on one event; make one trigger for each", driver)
	}
	return out, nil
}

func renderTrigger(driver Driver, spec TriggerSpec, create string) (string, error) {
	d, err := ddlDialect(driver, OpTriggers)
	if err != nil {
		return "", err
	}
	table, err := ddlRel(d, spec.Schema, spec.Table)
	if err != nil {
		return "", err
	}
	name, err := ddlRel(d, spec.Schema, spec.Name)
	if err != nil {
		return "", err
	}
	if driver == DriverPostgres || driver == DriverSQLite {
		// A Postgres trigger lives on its table and a SQLite one in the file.
		if name, err = d.QuoteIdent(spec.Name); err != nil {
			return "", err
		}
	}
	events, err := triggerEvents(driver, spec.Events)
	if err != nil {
		return "", err
	}
	timing := strings.ToUpper(spec.Timing)
	switch spec.Timing {
	case "before", "after", "instead of":
	default:
		return "", fmt.Errorf("timing must be before, after or instead of")
	}
	if driver == DriverMSSQL && spec.Timing == "before" {
		return "", fmt.Errorf("SQL Server has no BEFORE trigger: use AFTER, or INSTEAD OF to take the change over")
	}
	if (driver == DriverMySQL) && spec.Timing == "instead of" {
		return "", fmt.Errorf("MySQL has no INSTEAD OF trigger")
	}
	if spec.ForEach != "row" && spec.ForEach != "statement" {
		return "", fmt.Errorf("a trigger fires for each row or for each statement")
	}
	if spec.ForEach == "statement" && (driver == DriverMySQL || driver == DriverSQLite) {
		return "", fmt.Errorf("a %s trigger fires for each row", driver)
	}
	if spec.ForEach == "row" && driver == DriverMSSQL {
		return "", fmt.Errorf("a SQL Server trigger fires once for each statement and reads the rows from inserted and deleted")
	}
	var cond string
	if strings.TrimSpace(spec.Condition) != "" {
		if driver == DriverMySQL || driver == DriverMSSQL {
			return "", fmt.Errorf("%s has no WHEN condition on a trigger: test it inside the body", map[Driver]string{DriverMySQL: "MySQL", DriverMSSQL: "SQL Server"}[driver])
		}
		if cond, err = validateFragment(driver, "the condition", spec.Condition); err != nil {
			return "", err
		}
	}
	if len(spec.UpdateOf) > 0 && (driver != DriverSQLite || !slices.Contains(events, "update")) {
		return "", fmt.Errorf("UPDATE OF is SQLite's and narrows an update trigger")
	}

	switch driver {
	case DriverPostgres:
		if spec.Function == "" {
			return "", fmt.Errorf("choose the function the trigger executes")
		}
		fn, err := ddlRel(d, spec.FunctionSchema, spec.Function)
		if err != nil {
			return "", err
		}
		args := make([]string, 0, len(spec.FunctionArgs))
		for _, a := range spec.FunctionArgs {
			lit, err := ddlLiteral(driver, "a function argument", a)
			if err != nil {
				return "", err
			}
			args = append(args, lit)
		}
		lines := []string{
			create + " TRIGGER " + name,
			timing + " " + strings.ToUpper(strings.Join(events, " OR ")) + " ON " + table,
			"FOR EACH " + strings.ToUpper(spec.ForEach),
		}
		if cond != "" {
			lines = append(lines, "WHEN ("+cond+")")
		}
		lines = append(lines, "EXECUTE FUNCTION "+fn+"("+strings.Join(args, ", ")+")")
		return strings.Join(lines, "\n"), nil

	case DriverMySQL:
		body, err := validateProgramBody("the body", spec.Body)
		if err != nil {
			return "", err
		}
		return create + " TRIGGER " + name + " " + timing + " " + strings.ToUpper(events[0]) + " ON " + table +
			"\nFOR EACH ROW\n" + strings.TrimRight(body, "; \t\r\n"), nil

	case DriverMSSQL:
		body, err := validateProgramBody("the body", spec.Body)
		if err != nil {
			return "", err
		}
		return create + " TRIGGER " + name + " ON " + table + "\n" + timing + " " +
			strings.ToUpper(strings.Join(events, ", ")) + "\nAS\n" + strings.TrimRight(body, "; \t\r\n"), nil

	case DriverOracle:
		body, err := validateProgramBody("the body", spec.Body)
		if err != nil {
			return "", err
		}
		lines := []string{create + " TRIGGER " + name, timing + " " + strings.ToUpper(strings.Join(events, " OR ")) + " ON " + table}
		if spec.ForEach == "row" {
			lines = append(lines, "FOR EACH ROW")
			if cond != "" {
				lines = append(lines, "WHEN ("+cond+")")
			}
		} else if cond != "" {
			return "", fmt.Errorf("Oracle allows a WHEN condition on a row trigger only")
		}
		return strings.Join(append(lines, body), "\n"), nil

	case DriverSQLite:
		body, err := validateProgramBody("the body", spec.Body)
		if err != nil {
			return "", err
		}
		if !strings.HasSuffix(body, ";") {
			body += ";"
		}
		event := strings.ToUpper(events[0])
		if len(spec.UpdateOf) > 0 {
			cols, err := quoteColumns(d, "the UPDATE OF list", spec.UpdateOf)
			if err != nil {
				return "", err
			}
			event += " OF " + cols
		}
		lines := []string{create + " TRIGGER " + name, timing + " " + event + " ON " + table, "FOR EACH ROW"}
		if cond != "" {
			lines = append(lines, "WHEN "+cond)
		}
		return strings.Join(append(lines, "BEGIN", body, "END"), "\n"), nil
	}
	return "", fmt.Errorf("%s has no triggers", driver)
}

// replacedRead is a refused read of the program an edit replaces: that it is
// not there, or not one program, is the request's fault; the rest is the
// engine failing.
func replacedRead(what string, err error) error {
	if errors.Is(err, ErrObjectNotFound) || errors.Is(err, ErrAmbiguousObject) {
		return fmt.Errorf("the %s being replaced: %w", what, err)
	}
	return planRead("the "+what+" being replaced", err)
}

func uneditable(what string, p *Program) error {
	return fmt.Errorf("%s cannot be changed from this form: %s. Change it in the Query tab", what, p.Reason)
}

// PlanSaveRoutine plans making a function or procedure, or — given edit —
// replacing the one it names.
//
// Replacing is whatever the engine offers that keeps the old definition if the
// new one is refused: CREATE OR REPLACE where there is one, CREATE OR ALTER on
// SQL Server, and a DROP and CREATE in one transaction on Postgres when the
// parameters or the return type change (the one thing OR REPLACE refuses).
// MySQL has neither, so it drops and creates and puts the old definition back
// if the new one fails; Oracle's replacement can succeed and leave an object
// that did not compile, which is checked for and undone the same way.
func PlanSaveRoutine(ctx context.Context, db *sql.DB, driver Driver, spec RoutineSpec, edit *RoutineRef) (*DDLPlan, error) {
	op, err := programKindOp(spec.Kind)
	if err != nil {
		return nil, err
	}
	if _, err := ddlDialect(driver, op); err != nil {
		return nil, err
	}
	var old *RoutineSpec
	if edit != nil {
		program, err := ReadProgram(ctx, db, driver, ObjectRef{Kind: spec.Kind, Schema: edit.Schema, Name: edit.Name, Signature: edit.Signature})
		if err != nil {
			return nil, replacedRead("routine", err)
		}
		if !program.Editable {
			return nil, uneditable("this "+spec.Kind, program)
		}
		old = program.Routine
		spec.Schema, spec.Name, spec.Comment = old.Schema, old.Name, old.Comment
	}
	replacing := old != nil
	switch driver {
	case DriverPostgres:
		if !replacing {
			return planStmt(renderRoutine(driver, spec, "CREATE"))
		}
		if pgReplaceable(*old, spec) {
			stmt, err := renderRoutine(driver, spec, "CREATE OR REPLACE")
			if err != nil {
				return nil, err
			}
			return planOf(stmt), nil
		}
		drop, err := pgDropRoutine(ctx, db, *old, edit.Signature)
		if err != nil {
			return nil, err
		}
		create, err := renderRoutine(driver, spec, "CREATE")
		if err != nil {
			return nil, err
		}
		plan := &DDLPlan{Statements: []string{drop, create}, atomic: true,
			Note: "Postgres cannot replace a function whose parameters or return type change, so this drops it and creates it again in one transaction: if the new definition is refused, the old one stays."}
		if old.Comment != "" {
			comment, err := pgRoutineComment(spec, old.Comment)
			if err != nil {
				return nil, err
			}
			plan.Statements = append(plan.Statements, comment)
		}
		return plan, nil

	case DriverMySQL:
		maria := isMariaDB(ctx, db)
		if !replacing {
			return planStmt(renderRoutine(driver, spec, "CREATE"))
		}
		if maria {
			stmt, err := renderRoutine(driver, spec, "CREATE OR REPLACE")
			if err != nil {
				return nil, err
			}
			return planOf(stmt), nil
		}
		create, err := renderRoutine(driver, spec, "CREATE")
		if err != nil {
			return nil, err
		}
		restore, err := renderRoutine(driver, *old, "CREATE")
		if err != nil {
			return nil, err
		}
		drop, err := dropRoutineStatement(driver, spec.Kind, spec.Schema, spec.Name)
		if err != nil {
			return nil, err
		}
		return &DDLPlan{Statements: []string{drop, create}, undo: []string{restore},
			Note: "MySQL cannot replace a routine in place, so this drops it and creates it again. MySQL commits a DROP at once: if the new definition is refused, the old one is created again from the text it was read as. The definer becomes the account this connection uses."}, nil

	case DriverMSSQL:
		verb := "CREATE"
		if replacing {
			verb = "CREATE OR ALTER"
		}
		return planStmt(renderRoutine(driver, spec, verb))

	case DriverOracle:
		verb := "CREATE"
		if replacing {
			verb = "CREATE OR REPLACE"
		}
		stmt, err := renderRoutine(driver, spec, verb)
		if err != nil {
			return nil, err
		}
		plan := planOf(stmt)
		plan.after = oracleCompiled(strings.ToUpper(spec.Kind), spec.Schema, spec.Name)
		if replacing {
			restore, err := renderRoutine(driver, *old, "CREATE OR REPLACE")
			if err != nil {
				return nil, err
			}
			plan.undo = []string{restore}
			plan.Note = "Oracle keeps a routine that did not compile, so this checks the result and puts the previous definition back if it has errors."
		} else {
			drop, err := dropRoutineStatement(driver, spec.Kind, spec.Schema, spec.Name)
			if err != nil {
				return nil, err
			}
			plan.undo = []string{drop}
			plan.Note = "Oracle keeps a routine that did not compile, so this checks the result and drops it again if it has errors."
		}
		return plan, nil
	}
	return nil, fmt.Errorf("%s has no stored routines", driver)
}

// planStmt wraps a rendered statement as a one-statement plan.
func planStmt(stmt string, err error) (*DDLPlan, error) {
	if err != nil {
		return nil, err
	}
	return planOf(stmt), nil
}

// pgReplaceable reports whether CREATE OR REPLACE can stand for the change:
// the parameters and the return type are what the routine already has.
func pgReplaceable(old, next RoutineSpec) bool {
	if len(old.Args) != len(next.Args) || squashed(old.Returns) != squashed(next.Returns) {
		return false
	}
	for i := range old.Args {
		a, b := old.Args[i], next.Args[i]
		am, bm := a.Mode, b.Mode
		if am == "" {
			am = "in"
		}
		if bm == "" {
			bm = "in"
		}
		if a.Name != b.Name || am != bm || squashed(a.Type) != squashed(b.Type) || squashed(a.Default) != squashed(b.Default) {
			return false
		}
	}
	return true
}

// pgInputTypes are the types that identify a routine to DROP, read from the
// catalogue so the statement carries the server's spelling and none of the
// request's.
func pgInputTypes(ctx context.Context, db *sql.DB, kind, schema, name, signature string) ([]string, error) {
	match := "p.prokind <> 'p'"
	if kind == KindProcedure {
		match = "p.prokind = 'p'"
	}
	// An empty signature names the routine when it is the only one of its name,
	// as the read does; two overloads need to be told apart.
	rows, err := db.QueryContext(ctx, `
	  SELECT COALESCE(to_json(ARRAY(
	           SELECT format_type(t.oid, NULL) FROM unnest(p.proargtypes::oid[]) WITH ORDINALITY u(oid, i)
	           JOIN pg_type t ON t.oid = u.oid ORDER BY u.i))::text, '[]')
	  FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
	  WHERE `+match+` AND `+pgSchemaIs("n")+` AND p.proname = $2
	    AND ($3::text = '' OR pg_get_function_identity_arguments(p.oid) = $3::text)
	  LIMIT 2`, schema, name, signature)
	if err != nil {
		return nil, planRead("the routine's parameter types", err)
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, planRead("the routine's parameter types", err)
		}
		found = append(found, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, planRead("the routine's parameter types", err)
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("%w: no %s %s(%s)", ErrObjectNotFound, kind, name, signature)
	case 1:
		return jsonStrings(found[0]), nil
	}
	return nil, fmt.Errorf("%w: %s has more than one form — name one by its signature", ErrAmbiguousObject, name)
}

func pgDropRoutine(ctx context.Context, db *sql.DB, old RoutineSpec, signature string) (string, error) {
	types, err := pgInputTypes(ctx, db, old.Kind, old.Schema, old.Name, signature)
	if err != nil {
		return "", err
	}
	d, _ := DialectFor(DriverPostgres)
	rel, err := ddlRel(d, old.Schema, old.Name)
	if err != nil {
		return "", err
	}
	return "DROP " + strings.ToUpper(old.Kind) + " " + rel + "(" + strings.Join(types, ", ") + ")", nil
}

func pgRoutineComment(spec RoutineSpec, comment string) (string, error) {
	d, _ := DialectFor(DriverPostgres)
	rel, err := ddlRel(d, spec.Schema, spec.Name)
	if err != nil {
		return "", err
	}
	lit, err := ddlLiteral(DriverPostgres, "the comment", comment)
	if err != nil {
		return "", err
	}
	var types []string
	for _, a := range spec.Args {
		if a.Mode != "out" {
			types = append(types, strings.TrimSpace(a.Type))
		}
	}
	return "COMMENT ON " + strings.ToUpper(spec.Kind) + " " + rel + "(" + strings.Join(types, ", ") + ") IS " + lit, nil
}

func dropRoutineStatement(driver Driver, kind, schema, name string) (string, error) {
	op, err := programKindOp(kind)
	if err != nil {
		return "", err
	}
	d, err := ddlDialect(driver, op)
	if err != nil {
		return "", err
	}
	rel, err := ddlRel(d, schema, name)
	if err != nil {
		return "", err
	}
	return "DROP " + strings.ToUpper(kind) + " " + rel, nil
}

// PlanDropRoutine plans removing a function or procedure. It never cascades:
// an object that depends on the routine is the engine's reason to refuse.
func PlanDropRoutine(ctx context.Context, db *sql.DB, driver Driver, kind, schema, name, signature string) (*DDLPlan, error) {
	if driver == DriverPostgres {
		op, err := programKindOp(kind)
		if err != nil {
			return nil, err
		}
		if _, err := ddlDialect(driver, op); err != nil {
			return nil, err
		}
		types, err := pgInputTypes(ctx, db, kind, schema, name, signature)
		if err != nil {
			return nil, err
		}
		d, _ := DialectFor(driver)
		rel, err := ddlRel(d, schema, name)
		if err != nil {
			return nil, err
		}
		return planOf("DROP " + strings.ToUpper(kind) + " " + rel + "(" + strings.Join(types, ", ") + ")"), nil
	}
	return planStmt(dropRoutineStatement(driver, kind, schema, name))
}

// PlanSaveTrigger plans making a trigger, or replacing the one edit names.
func PlanSaveTrigger(ctx context.Context, db *sql.DB, driver Driver, spec TriggerSpec, edit *TriggerRef) (*DDLPlan, error) {
	if _, err := ddlDialect(driver, OpTriggers); err != nil {
		return nil, err
	}
	if edit == nil {
		return planStmt(renderTrigger(driver, spec, "CREATE"))
	}
	program, err := ReadProgram(ctx, db, driver, ObjectRef{Kind: KindTrigger, Schema: edit.Schema, Name: edit.Name, Table: edit.Table})
	if err != nil {
		return nil, replacedRead("trigger", err)
	}
	if !program.Editable {
		return nil, uneditable("this trigger", program)
	}
	old := *program.Trigger
	// An edit keeps the trigger where it is: it is on its table, whatever the
	// request said, and carries the comment it had.
	spec.Schema, spec.Name, spec.Table, spec.Comment = old.Schema, old.Name, old.Table, old.Comment
	switch driver {
	case DriverMSSQL:
		return planStmt(renderTrigger(driver, spec, "CREATE OR ALTER"))
	case DriverOracle:
		stmt, err := renderTrigger(driver, spec, "CREATE OR REPLACE")
		if err != nil {
			return nil, err
		}
		restore, err := renderTrigger(driver, old, "CREATE OR REPLACE")
		if err != nil {
			return nil, err
		}
		plan := planOf(stmt)
		plan.after = oracleCompiled("TRIGGER", spec.Schema, spec.Name)
		plan.undo = []string{restore}
		plan.Note = "Oracle keeps a trigger that did not compile, so this checks the result and puts the previous definition back if it has errors."
		return plan, nil
	case DriverMySQL:
		if isMariaDB(ctx, db) {
			return planStmt(renderTrigger(driver, spec, "CREATE OR REPLACE"))
		}
		create, err := renderTrigger(driver, spec, "CREATE")
		if err != nil {
			return nil, err
		}
		restore, err := renderTrigger(driver, old, "CREATE")
		if err != nil {
			return nil, err
		}
		drop, err := dropTriggerStatement(driver, old.Schema, old.Name, old.Table)
		if err != nil {
			return nil, err
		}
		return &DDLPlan{Statements: []string{drop, create}, undo: []string{restore},
			Note: "MySQL cannot replace a trigger in place, so this drops it and creates it again. MySQL commits a DROP at once: if the new definition is refused, the old one is created again from the text it was read as. The definer becomes the account this connection uses."}, nil
	}
	// Postgres and SQLite change DDL inside a transaction.
	create, err := renderTrigger(driver, spec, "CREATE")
	if err != nil {
		return nil, err
	}
	drop, err := dropTriggerStatement(driver, old.Schema, old.Name, old.Table)
	if err != nil {
		return nil, err
	}
	plan := &DDLPlan{Statements: []string{drop, create}, atomic: true,
		Note: "This drops the trigger and creates it again in one transaction: if the new definition is refused, the old one stays."}
	if driver == DriverPostgres && old.Comment != "" {
		d, _ := DialectFor(driver)
		table, err := ddlRel(d, spec.Schema, spec.Table)
		if err != nil {
			return nil, err
		}
		name, _ := d.QuoteIdent(spec.Name)
		lit, err := ddlLiteral(driver, "the comment", old.Comment)
		if err != nil {
			return nil, err
		}
		plan.Statements = append(plan.Statements, "COMMENT ON TRIGGER "+name+" ON "+table+" IS "+lit)
	}
	return plan, nil
}

func dropTriggerStatement(driver Driver, schema, name, table string) (string, error) {
	d, err := ddlDialect(driver, OpTriggers)
	if err != nil {
		return "", err
	}
	switch driver {
	case DriverPostgres:
		q, err := d.QuoteIdent(name)
		if err != nil {
			return "", err
		}
		on, err := ddlRel(d, schema, table)
		if err != nil {
			return "", err
		}
		return "DROP TRIGGER " + q + " ON " + on, nil
	case DriverSQLite:
		if schema != "" && !strings.EqualFold(schema, "main") {
			if _, err := ddlRel(d, schema, name); err != nil {
				return "", err
			}
		}
		q, err := d.QuoteIdent(name)
		if err != nil {
			return "", err
		}
		return "DROP TRIGGER " + q, nil
	}
	rel, err := ddlRel(d, schema, name)
	if err != nil {
		return "", err
	}
	return "DROP TRIGGER " + rel, nil
}

// PlanDropTrigger plans removing a trigger.
func PlanDropTrigger(driver Driver, schema, name, table string) (*DDLPlan, error) {
	return planStmt(dropTriggerStatement(driver, schema, name, table))
}

// oracleCompiled returns the check that runs after Oracle accepts a CREATE: it
// reads the compiler's errors for the object and turns any into a refusal.
func oracleCompiled(kind, schema, name string) func(context.Context, *sql.Conn) error {
	return func(ctx context.Context, conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, `
		  SELECT line, position, text FROM all_errors
		  WHERE owner = COALESCE(NULLIF(:1, ''), SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA'))
		    AND name = :2 AND type = :3 AND attribute = 'ERROR'
		  ORDER BY sequence`, schema, name, kind)
		if err != nil {
			return fmt.Errorf("could not read the compiler's errors: %w", err)
		}
		defer rows.Close()
		var found []string
		for rows.Next() {
			var line, position int
			var text string
			if err := rows.Scan(&line, &position, &text); err != nil {
				return err
			}
			if len(found) < 5 {
				found = append(found, fmt.Sprintf("line %d, column %d: %s", line, position, strings.TrimSpace(text)))
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(found) > 0 {
			return &compileError{kind: strings.ToLower(kind), errors: found}
		}
		return nil
	}
}

// compileError is Oracle accepting a CREATE and compiling it to nothing.
type compileError struct {
	kind   string
	errors []string
}

func (e *compileError) Error() string {
	return "the " + e.kind + " did not compile: " + strings.Join(e.errors, "; ")
}
