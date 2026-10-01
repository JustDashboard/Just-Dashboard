package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// Changes to what already exists: a column's type, nullability and default,
// the constraints on a table, its comments, and the views, schemas and enum
// types beside it. They are planned like the rest of the DDL (ddl.go) and
// differ from it in one way that shapes this file: several engines cannot
// change one property of a column without restating all of them, so some of
// these plans read the catalogue first to restate the rest faithfully.

// --- alter column -----------------------------------------------------------

// ColumnChange is what to change about one column. A nil pointer or an empty
// string leaves that property as it is.
type ColumnChange struct {
	Schema string
	Table  string
	Column string
	// Type is the new type.
	Type string
	// Using is Postgres's conversion expression for a type the old values do
	// not cast to by themselves.
	Using string
	// Nullable sets or clears NOT NULL.
	Nullable *bool
	// Default sets the default; DropDefault removes it.
	Default     *string
	DropDefault bool
}

// ChangesType reports whether the change rewrites the column's values, which
// is the part of an alter that can lose data.
func (c ColumnChange) ChangesType() bool { return strings.TrimSpace(c.Type) != "" }

func (c ColumnChange) validate() (typ, def string, setDefault bool, err error) {
	typ = strings.TrimSpace(c.Type)
	if typ != "" {
		if err := validateType(typ); err != nil {
			return "", "", false, err
		}
	}
	if c.Default != nil {
		def = strings.TrimSpace(*c.Default)
		if def == "" {
			return "", "", false, fmt.Errorf("a default is required; to remove the default, drop it")
		}
		if err := validateDefault(def); err != nil {
			return "", "", false, err
		}
		if c.DropDefault {
			return "", "", false, fmt.Errorf("a default cannot be set and dropped in the same change")
		}
		setDefault = true
	}
	if strings.TrimSpace(c.Using) != "" && typ == "" {
		return "", "", false, fmt.Errorf("a USING expression converts values to a new type, and no type was given")
	}
	if typ == "" && c.Nullable == nil && !setDefault && !c.DropDefault {
		return "", "", false, fmt.Errorf("nothing to change: name a type, a nullability or a default")
	}
	return typ, def, setDefault, nil
}

// PlanAlterColumn plans a change to one column.
func PlanAlterColumn(ctx context.Context, db *sql.DB, driver Driver, c ColumnChange) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpAlterColumn)
	if err != nil {
		return nil, err
	}
	typ, def, setDefault, err := c.validate()
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, c.Schema, c.Table)
	if err != nil {
		return nil, err
	}
	col, err := d.QuoteIdent(c.Column)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Using) != "" && driver != DriverPostgres {
		return nil, fmt.Errorf("a USING expression is Postgres's; %s converts the values itself or refuses", driver)
	}
	def = renderDefault(driver, def)

	switch driver {
	case DriverPostgres:
		actions := []string{}
		if typ != "" {
			action := "ALTER COLUMN " + col + " TYPE " + typ
			if strings.TrimSpace(c.Using) != "" {
				using, err := validateFragment(driver, "the USING expression", c.Using)
				if err != nil {
					return nil, err
				}
				action += " USING (" + using + ")"
			}
			actions = append(actions, action)
		}
		if c.DropDefault {
			actions = append(actions, "ALTER COLUMN "+col+" DROP DEFAULT")
		}
		if setDefault {
			actions = append(actions, "ALTER COLUMN "+col+" SET DEFAULT "+def)
		}
		if c.Nullable != nil {
			verb := "SET"
			if *c.Nullable {
				verb = "DROP"
			}
			actions = append(actions, "ALTER COLUMN "+col+" "+verb+" NOT NULL")
		}
		// One statement, so a type that converts and a NOT NULL that does not
		// hold leave the column as it was.
		return planOf("ALTER TABLE " + rel + " " + strings.Join(actions, ", ")), nil

	case DriverMySQL:
		if typ == "" && c.Nullable == nil {
			// The default alone has a statement of its own that restates
			// nothing.
			if c.DropDefault {
				return planOf("ALTER TABLE " + rel + " ALTER COLUMN " + col + " DROP DEFAULT"), nil
			}
			return planOf("ALTER TABLE " + rel + " ALTER COLUMN " + col + " SET DEFAULT " + def), nil
		}
		current, err := readMySQLColumn(ctx, db, c.Schema, c.Table, c.Column)
		if err != nil {
			return nil, err
		}
		if typ != "" {
			current.columnType, current.collation = typ, ""
		}
		if c.Nullable != nil {
			current.nullable = *c.Nullable
		}
		if c.DropDefault {
			current.dflt = ""
		}
		if setDefault {
			current.dflt = def
		}
		definition, err := current.render(d)
		if err != nil {
			return nil, err
		}
		return planOf("ALTER TABLE " + rel + " MODIFY COLUMN " + col + " " + definition), nil

	case DriverMSSQL:
		steps := []string{}
		if typ != "" || c.Nullable != nil {
			// ALTER COLUMN restates the type and the nullability together; one
			// left out is not "unchanged" but "the default", and the default
			// nullability is a session setting.
			currentType, currentNullable, err := mssqlColumnShape(ctx, db, c.Schema, c.Table, c.Column)
			if err != nil {
				return nil, err
			}
			if typ == "" {
				typ = currentType
			}
			nullable := currentNullable
			if c.Nullable != nil {
				nullable = *c.Nullable
			}
			null := "NOT NULL"
			if nullable {
				null = "NULL"
			}
			steps = append(steps, "ALTER TABLE "+rel+" ALTER COLUMN "+col+" "+typ+" "+null)
		}
		if c.DropDefault || setDefault {
			// A default is a constraint object here, so replacing it is
			// dropping the one that exists and adding another.
			existing, err := mssqlDefaultConstraints(ctx, db, c.Schema, c.Table, c.Column)
			if err != nil {
				return nil, fmt.Errorf("could not read the column's default: %w", err)
			}
			if c.DropDefault && len(existing) == 0 {
				return nil, fmt.Errorf("%s has no default to drop", c.Column)
			}
			for _, name := range existing {
				q, err := d.QuoteIdent(name)
				if err != nil {
					return nil, err
				}
				steps = append(steps, "ALTER TABLE "+rel+" DROP CONSTRAINT "+q)
			}
			if setDefault {
				name, err := d.QuoteIdent(generatedName("DF_"+c.Table, []string{c.Column}, "df"))
				if err != nil {
					return nil, err
				}
				steps = append(steps, "ALTER TABLE "+rel+" ADD CONSTRAINT "+name+" DEFAULT "+def+" FOR "+col)
			}
		}
		if len(steps) == 1 {
			return planOf(steps[0]), nil
		}
		return planOf(mssqlAtomicBatch(steps)), nil

	case DriverOracle:
		parts := []string{col}
		if typ != "" {
			parts = append(parts, typ)
		}
		if c.DropDefault {
			// Oracle has no DROP DEFAULT; a default of NULL is the absence of
			// one.
			parts = append(parts, "DEFAULT NULL")
		}
		if setDefault {
			parts = append(parts, "DEFAULT "+def)
		}
		if c.Nullable != nil {
			if *c.Nullable {
				parts = append(parts, "NULL")
			} else {
				parts = append(parts, "NOT NULL")
			}
		}
		return planOf("ALTER TABLE " + rel + " MODIFY (" + strings.Join(parts, " ") + ")"), nil

	case DriverClickHouse:
		if c.Nullable != nil {
			return nil, fmt.Errorf("ClickHouse has no NOT NULL to set: a column is optional when its type is Nullable(T), so change the type")
		}
		statements := []string{}
		switch {
		case typ != "" && setDefault:
			statements = append(statements, "ALTER TABLE "+rel+" MODIFY COLUMN "+col+" "+typ+" DEFAULT "+def)
		case typ != "":
			statements = append(statements, "ALTER TABLE "+rel+" MODIFY COLUMN "+col+" "+typ)
		case setDefault:
			statements = append(statements, "ALTER TABLE "+rel+" MODIFY COLUMN "+col+" DEFAULT "+def)
		}
		if c.DropDefault {
			statements = append(statements, "ALTER TABLE "+rel+" MODIFY COLUMN "+col+" REMOVE DEFAULT")
		}
		return planOf(statements...), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsupported, driver)
}

// mssqlColumnShape returns a column's declared type and nullability.
func mssqlColumnShape(ctx context.Context, db *sql.DB, schema, table, column string) (string, bool, error) {
	cols, err := mssqlDialect{}.tableColumns(ctx, db, schema, table)
	if err != nil {
		return "", false, fmt.Errorf("could not read the column as it is now: %w", err)
	}
	for _, c := range cols {
		if c.Name == column {
			if c.Generated != "" {
				return "", false, fmt.Errorf("%s is a computed column; change its expression from the Query tab", column)
			}
			return c.Type, c.Nullable, nil
		}
	}
	return "", false, fmt.Errorf("no column %s on %s", column, table)
}

// mysqlColumn is a column as MySQL would have it declared again. MODIFY
// COLUMN replaces the whole definition: anything not restated — the comment,
// AUTO_INCREMENT, ON UPDATE, the collation — is silently reset, so changing
// one property means reading the others first.
type mysqlColumn struct {
	columnType string
	nullable   bool
	dflt       string
	collation  string
	comment    string
	// autoIncrement and onUpdate are the two EXTRA clauses that are part of
	// the declaration rather than a description of it.
	autoIncrement bool
	onUpdate      string
}

var (
	mysqlOnUpdateRe  = regexp.MustCompile(`(?i)on update (current_timestamp(?:\(\d*\))?)`)
	mysqlCollationRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
)

func readMySQLColumn(ctx context.Context, db *sql.DB, schema, table, column string) (*mysqlColumn, error) {
	var (
		c                                    mysqlColumn
		nullable, extra, generation, version string
		dflt, collation                      sql.NullString
	)
	err := db.QueryRowContext(ctx, `
	  SELECT COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COALESCE(EXTRA, ''), COALESCE(COLUMN_COMMENT, ''),
	         COLLATION_NAME, COALESCE(GENERATION_EXPRESSION, ''), VERSION()
	  FROM information_schema.COLUMNS
	  WHERE `+mysqlSchemaIs("TABLE_SCHEMA")+` AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
		schema, table, column).Scan(&c.columnType, &nullable, &dflt, &extra, &c.comment, &collation, &generation, &version)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no column %s on %s", column, table)
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the column as it is now: %w", err)
	}
	if generation != "" {
		return nil, fmt.Errorf("%s is a generated column; change its expression from the Query tab", column)
	}
	extra = strings.ToLower(extra)
	c.nullable = nullable == "YES"
	c.dflt = mysqlDefaultSQL(c.columnType, dflt, extra, strings.Contains(strings.ToLower(version), "mariadb"))
	c.collation = collation.String
	c.autoIncrement = strings.Contains(extra, "auto_increment")
	if m := mysqlOnUpdateRe.FindStringSubmatch(extra); m != nil {
		c.onUpdate = strings.ToUpper(m[1])
	}
	return &c, nil
}

func (c *mysqlColumn) render(d Dialect) (string, error) {
	out := c.columnType
	if c.collation != "" {
		if !mysqlCollationRe.MatchString(c.collation) {
			return "", fmt.Errorf("the column's collation %q cannot be restated safely", c.collation)
		}
		out += " COLLATE " + c.collation
	}
	if c.nullable {
		out += " NULL"
	} else {
		out += " NOT NULL"
	}
	if c.dflt != "" {
		out += " DEFAULT " + c.dflt
	}
	if c.autoIncrement {
		out += " AUTO_INCREMENT"
	}
	if c.onUpdate != "" {
		out += " ON UPDATE " + c.onUpdate
	}
	if c.comment != "" {
		comment, err := ddlLiteral(d.Driver(), "the comment", c.comment)
		if err != nil {
			return "", err
		}
		out += " COMMENT " + comment
	}
	return out, nil
}

// --- foreign keys -----------------------------------------------------------

// ForeignKeySpec is a foreign key to add.
type ForeignKeySpec struct {
	Schema     string
	Table      string
	Name       string
	Columns    []string
	RefSchema  string
	RefTable   string
	RefColumns []string
	OnDelete   string
	OnUpdate   string
}

// referentialActions is the closed set a foreign key may name. They are
// keywords in the statement, so they are matched, never passed through.
var referentialActions = map[string]bool{
	"NO ACTION": true, "RESTRICT": true, "CASCADE": true, "SET NULL": true, "SET DEFAULT": true,
}

func referentialAction(driver Driver, clause, action string) (string, error) {
	action = strings.ToUpper(strings.Join(strings.Fields(action), " "))
	if action == "" || action == "NO ACTION" {
		// The default everywhere, and the one spelling Oracle rejects.
		return "", nil
	}
	if !referentialActions[action] {
		return "", fmt.Errorf("%q is not a referential action; use NO ACTION, RESTRICT, CASCADE, SET NULL or SET DEFAULT", action)
	}
	switch {
	case driver == DriverMSSQL && action == "RESTRICT":
		return "", fmt.Errorf("SQL Server has no RESTRICT; NO ACTION is its equivalent")
	case driver == DriverOracle && clause == "ON UPDATE":
		return "", fmt.Errorf("Oracle has no ON UPDATE action: a referenced key cannot be changed while it is referenced")
	case driver == DriverOracle && action != "CASCADE" && action != "SET NULL":
		return "", fmt.Errorf("Oracle's ON DELETE takes CASCADE or SET NULL")
	}
	return " " + clause + " " + action, nil
}

// PlanAddForeignKey plans adding a foreign key to an existing table.
func PlanAddForeignKey(driver Driver, spec ForeignKeySpec) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpForeignKeys)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, spec.Schema, spec.Table)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.RefTable) == "" {
		return nil, fmt.Errorf("the table the key references is required")
	}
	if len(spec.Columns) != len(spec.RefColumns) {
		return nil, fmt.Errorf("a foreign key pairs each column with a referenced column: %d given against %d",
			len(spec.Columns), len(spec.RefColumns))
	}
	cols, err := quoteColumns(d, "a foreign key", spec.Columns)
	if err != nil {
		return nil, err
	}
	refCols, err := quoteColumns(d, "a foreign key", spec.RefColumns)
	if err != nil {
		return nil, err
	}
	refSchema := spec.RefSchema
	if refSchema == "" {
		refSchema = spec.Schema
	}
	refRel, err := qualify(d, refSchema, spec.RefTable)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.Name) == "" {
		spec.Name = generatedName(spec.Table, spec.Columns, "fkey")
	}
	name, err := d.QuoteIdent(spec.Name)
	if err != nil {
		return nil, err
	}
	onDelete, err := referentialAction(driver, "ON DELETE", spec.OnDelete)
	if err != nil {
		return nil, err
	}
	onUpdate, err := referentialAction(driver, "ON UPDATE", spec.OnUpdate)
	if err != nil {
		return nil, err
	}
	return planOf(fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)%s%s",
		rel, name, cols, refRel, refCols, onDelete, onUpdate)), nil
}

// PlanDropForeignKey plans removing a foreign key by name.
func PlanDropForeignKey(driver Driver, schema, table, name string) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpForeignKeys)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return nil, err
	}
	q, err := d.QuoteIdent(name)
	if err != nil {
		return nil, err
	}
	if driver == DriverMySQL {
		return planOf("ALTER TABLE " + rel + " DROP FOREIGN KEY " + q), nil
	}
	return planOf("ALTER TABLE " + rel + " DROP CONSTRAINT " + q), nil
}

// --- unique and check constraints -------------------------------------------

// ConstraintSpec is a unique or check constraint to add.
type ConstraintSpec struct {
	Schema string
	Table  string
	Name   string
	// Type is ConstraintUnique or ConstraintCheck.
	Type string
	// Columns are a unique constraint's columns.
	Columns []string
	// Expression is a check constraint's condition.
	Expression string
}

func constraintOp(constraintType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(constraintType)) {
	case ConstraintUnique:
		return OpUnique, nil
	case ConstraintCheck:
		return OpCheck, nil
	default:
		return "", fmt.Errorf("a constraint is %q or %q", ConstraintUnique, ConstraintCheck)
	}
}

// PlanAddConstraint plans adding a unique or check constraint.
func PlanAddConstraint(driver Driver, spec ConstraintSpec) (*DDLPlan, error) {
	op, err := constraintOp(spec.Type)
	if err != nil {
		return nil, err
	}
	d, err := ddlDialect(driver, op)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, spec.Schema, spec.Table)
	if err != nil {
		return nil, err
	}
	if op == OpUnique {
		cols, err := quoteColumns(d, "a unique constraint", spec.Columns)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(spec.Name) == "" {
			spec.Name = generatedName(spec.Table, spec.Columns, "key")
		}
		name, err := d.QuoteIdent(spec.Name)
		if err != nil {
			return nil, err
		}
		return planOf("ALTER TABLE " + rel + " ADD CONSTRAINT " + name + " UNIQUE (" + cols + ")"), nil
	}
	expression, err := validateFragment(driver, "the check condition", spec.Expression)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.Name) == "" {
		spec.Name = generatedName(spec.Table, nil, "check")
	}
	name, err := d.QuoteIdent(spec.Name)
	if err != nil {
		return nil, err
	}
	return planOf("ALTER TABLE " + rel + " ADD CONSTRAINT " + name + " CHECK (" + expression + ")"), nil
}

// PlanDropConstraint plans removing a unique or check constraint. MySQL drops
// the two differently, so when the caller does not say which it is, the
// catalogue is asked.
func PlanDropConstraint(ctx context.Context, db *sql.DB, driver Driver, schema, table, name, constraintType string) (*DDLPlan, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return nil, err
	}
	q, err := d.QuoteIdent(name)
	if err != nil {
		return nil, err
	}
	constraintType = strings.ToLower(strings.TrimSpace(constraintType))
	if constraintType == "" && driver == DriverMySQL {
		constraints, err := mysqlDialect{}.tableConstraints(ctx, db, schema, table)
		if err != nil {
			return nil, fmt.Errorf("could not read the table's constraints: %w", err)
		}
		for _, c := range constraints {
			if c.Name == name {
				constraintType = c.Type
			}
		}
		if constraintType == "" {
			return nil, fmt.Errorf("no unique or check constraint named %s on %s", name, table)
		}
	}
	if constraintType == "" {
		// Every other engine drops both the same way, so the op that decides
		// support is whichever of the two the engine has.
		constraintType = ConstraintCheck
		if !SupportsOperation(driver, OpCheck) {
			constraintType = ConstraintUnique
		}
	}
	op, err := constraintOp(constraintType)
	if err != nil {
		return nil, err
	}
	if _, err := ddlDialect(driver, op); err != nil {
		return nil, err
	}
	if driver == DriverMySQL && op == OpUnique {
		// A unique constraint is its index on this engine.
		return planOf("ALTER TABLE " + rel + " DROP INDEX " + q), nil
	}
	return planOf("ALTER TABLE " + rel + " DROP CONSTRAINT " + q), nil
}

// --- comments ---------------------------------------------------------------

// CommentSpec sets the comment on a table, or on one of its columns when
// Column is named. An empty comment removes it.
type CommentSpec struct {
	Schema  string
	Table   string
	Column  string
	Comment string
}

// PlanComment plans setting a comment.
func PlanComment(ctx context.Context, db *sql.DB, driver Driver, spec CommentSpec) (*DDLPlan, error) {
	op := OpCommentTable
	if spec.Column != "" {
		op = OpCommentColumn
	}
	d, err := ddlDialect(driver, op)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, spec.Schema, spec.Table)
	if err != nil {
		return nil, err
	}
	col := ""
	if spec.Column != "" {
		if col, err = d.QuoteIdent(spec.Column); err != nil {
			return nil, err
		}
	}
	if len(spec.Comment) > 4000 {
		return nil, fmt.Errorf("a comment is at most 4000 bytes")
	}
	literal, err := ddlLiteral(driver, "the comment", spec.Comment)
	if err != nil {
		return nil, err
	}

	switch driver {
	case DriverPostgres, DriverOracle:
		// Postgres removes a comment with NULL; Oracle's empty string is NULL.
		if spec.Comment == "" && driver == DriverPostgres {
			literal = "NULL"
		}
		if col != "" {
			return planOf("COMMENT ON COLUMN " + rel + "." + col + " IS " + literal), nil
		}
		return planOf("COMMENT ON TABLE " + rel + " IS " + literal), nil
	case DriverMySQL:
		if col == "" {
			return planOf("ALTER TABLE " + rel + " COMMENT = " + literal), nil
		}
		current, err := readMySQLColumn(ctx, db, spec.Schema, spec.Table, spec.Column)
		if err != nil {
			return nil, err
		}
		current.comment = spec.Comment
		definition, err := current.render(d)
		if err != nil {
			return nil, err
		}
		return planOf("ALTER TABLE " + rel + " MODIFY COLUMN " + col + " " + definition), nil
	case DriverClickHouse:
		if col != "" {
			return planOf("ALTER TABLE " + rel + " COMMENT COLUMN " + col + " " + literal), nil
		}
		return planOf("ALTER TABLE " + rel + " MODIFY COMMENT " + literal), nil
	case DriverMSSQL:
		return mssqlCommentPlan(d, spec, literal)
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsupported, driver)
}

// mssqlCommentPlan writes a comment as the MS_Description extended property,
// which is where SQL Server's own tools keep it. Adding one that exists and
// updating one that does not are both errors, so the batch asks first.
func mssqlCommentPlan(d Dialect, spec CommentSpec, literal string) (*DDLPlan, error) {
	schema := spec.Schema
	if schema == "" {
		schema = d.DefaultSchema()
	}
	object, err := mssqlName(d, schema, spec.Table)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, 3)
	for _, n := range []string{schema, spec.Table, spec.Column} {
		// These are values to the procedure, not identifiers in the statement,
		// so they are string literals and are not bracketed.
		lit, err := ddlLiteral(DriverMSSQL, "the name", n)
		if err != nil {
			return nil, err
		}
		names = append(names, lit)
	}
	minor := "0"
	args := "@name = N'MS_Description', @value = " + literal +
		", @level0type = N'SCHEMA', @level0name = " + names[0] +
		", @level1type = N'TABLE', @level1name = " + names[1]
	if spec.Column != "" {
		minor = "COLUMNPROPERTY(OBJECT_ID(" + object + "), " + names[2] + ", 'ColumnId')"
		args += ", @level2type = N'COLUMN', @level2name = " + names[2]
	}
	return planOf("IF EXISTS (SELECT 1 FROM sys.extended_properties ep WHERE ep.class = 1" +
		" AND ep.name = N'MS_Description' AND ep.major_id = OBJECT_ID(" + object + ") AND ep.minor_id = " + minor + ")\n" +
		"  EXEC sys.sp_updateextendedproperty " + args + "\n" +
		"ELSE\n" +
		"  EXEC sys.sp_addextendedproperty " + args), nil
}

// --- views ------------------------------------------------------------------

// ViewSpec is a view to create.
type ViewSpec struct {
	Schema string
	Name   string
	// Query is the SELECT the view is defined by.
	Query string
	// Replace overwrites a view of the same name.
	Replace bool
	// Materialized stores the result, on the engine that has a plain form of
	// that.
	Materialized bool
}

// viewQuery accepts the statement a view is defined by: exactly one, and a
// read. The same splitter and classifier that guard the query runner decide
// it, so a view form is never a way to run something the console would have
// asked more for.
func viewQuery(query string) (string, error) {
	stmt, err := SingleStatement(query)
	if err != nil {
		return "", fmt.Errorf("a view is defined by one SELECT: %w", err)
	}
	if risk := Classify(stmt); risk.Level != "read" {
		return "", fmt.Errorf("a view is defined by a SELECT; this statement %s", strings.Join(risk.Reasons, ", "))
	}
	// The classifier's reads include SHOW, DESCRIBE and EXPLAIN, which answer
	// a question and define nothing.
	switch word := leadingKeyword(stmt); word {
	case "select", "with", "values", "table":
		return stmt, nil
	default:
		return "", fmt.Errorf("a view is defined by a SELECT, and this starts with %q", word)
	}
}

// leadingKeyword returns a statement's first word in lower case, past any
// whitespace, opening parentheses and comments in front of it.
func leadingKeyword(stmt string) string {
	i := 0
	for i < len(stmt) {
		switch {
		case stmt[i] == ' ' || stmt[i] == '\t' || stmt[i] == '\r' || stmt[i] == '\n' || stmt[i] == '(':
			i++
		case strings.HasPrefix(stmt[i:], "--"):
			end := strings.IndexByte(stmt[i:], '\n')
			if end < 0 {
				return ""
			}
			i += end + 1
		case strings.HasPrefix(stmt[i:], "/*"):
			end := strings.Index(stmt[i:], "*/")
			if end < 0 {
				return ""
			}
			i += end + 2
		default:
			j := i
			for j < len(stmt) && (isASCIILetter(stmt[j]) || stmt[j] == '_') {
				j++
			}
			return strings.ToLower(stmt[i:j])
		}
	}
	return ""
}

// PlanCreateView plans creating, or replacing, a view.
func PlanCreateView(driver Driver, spec ViewSpec) (*DDLPlan, error) {
	op := OpViews
	if spec.Materialized {
		op = OpMaterialized
	}
	d, err := ddlDialect(driver, op)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, spec.Schema, spec.Name)
	if err != nil {
		return nil, err
	}
	query, err := viewQuery(spec.Query)
	if err != nil {
		return nil, err
	}
	if spec.Materialized {
		if spec.Replace {
			return nil, fmt.Errorf("Postgres cannot replace a materialized view: drop it and create it again")
		}
		return planOf("CREATE MATERIALIZED VIEW " + rel + " AS\n" + query), nil
	}
	create := "CREATE VIEW"
	if spec.Replace {
		switch driver {
		case DriverSQLite:
			return nil, fmt.Errorf("SQLite has no CREATE OR REPLACE VIEW: drop the view and create it again")
		case DriverMSSQL:
			create = "CREATE OR ALTER VIEW"
		default:
			create = "CREATE OR REPLACE VIEW"
		}
	}
	return planOf(create + " " + rel + " AS\n" + query), nil
}

// PlanDropView plans removing a view.
func PlanDropView(driver Driver, schema, name string, materialized bool) (*DDLPlan, error) {
	op, form := OpViews, "DROP VIEW %s"
	if materialized {
		op, form = OpMaterialized, "DROP MATERIALIZED VIEW %s"
	}
	return planRelStatement(driver, op, schema, name, form)
}

// --- schemas ----------------------------------------------------------------

// PlanCreateSchema plans creating a schema, on the engines where a schema is
// a namespace inside a database rather than the database itself.
func PlanCreateSchema(driver Driver, name string) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpSchemas)
	if err != nil {
		return nil, err
	}
	q, err := d.QuoteIdent(name)
	if err != nil {
		return nil, err
	}
	return planOf("CREATE SCHEMA " + q), nil
}

// PlanDropSchema plans removing a schema. It never cascades: the engine
// refuses a schema that still holds anything, and emptying one object at a
// time is what keeps "drop schema" from being "drop everything in it".
func PlanDropSchema(driver Driver, name string) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpSchemas)
	if err != nil {
		return nil, err
	}
	q, err := d.QuoteIdent(name)
	if err != nil {
		return nil, err
	}
	return planOf("DROP SCHEMA " + q), nil
}

// --- enum types -------------------------------------------------------------

func enumLabel(driver Driver, value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("an enum label cannot be empty")
	}
	// NAMEDATALEN - 1: Postgres refuses a longer label.
	if len(value) > 63 {
		return "", fmt.Errorf("an enum label is at most 63 bytes")
	}
	return ddlLiteral(driver, "an enum label", value)
}

// PlanCreateEnum plans creating an enum type.
func PlanCreateEnum(driver Driver, schema, name string, values []string) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpEnumTypes)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, schema, name)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("an enum type needs at least one label")
	}
	seen := map[string]bool{}
	labels := make([]string, 0, len(values))
	for _, v := range values {
		if seen[v] {
			return nil, fmt.Errorf("the label %q is listed twice", v)
		}
		seen[v] = true
		lit, err := enumLabel(driver, v)
		if err != nil {
			return nil, err
		}
		labels = append(labels, lit)
	}
	return planOf("CREATE TYPE " + rel + " AS ENUM (" + strings.Join(labels, ", ") + ")"), nil
}

// EnumValueSpec is a label to add to an enum type. Before and After place it;
// with neither it goes last.
type EnumValueSpec struct {
	Schema      string
	Name        string
	Value       string
	Before      string
	After       string
	IfNotExists bool
}

// PlanAddEnumValue plans adding a label to an enum type. A label cannot be
// removed or reordered afterwards, which is the engine's rule and the reason
// placement is offered here.
func PlanAddEnumValue(driver Driver, spec EnumValueSpec) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpEnumTypes)
	if err != nil {
		return nil, err
	}
	rel, err := qualify(d, spec.Schema, spec.Name)
	if err != nil {
		return nil, err
	}
	value, err := enumLabel(driver, spec.Value)
	if err != nil {
		return nil, err
	}
	stmt := "ALTER TYPE " + rel + " ADD VALUE "
	if spec.IfNotExists {
		stmt += "IF NOT EXISTS "
	}
	stmt += value
	switch {
	case spec.Before != "" && spec.After != "":
		return nil, fmt.Errorf("a label goes before one label or after one, not both")
	case spec.Before != "":
		neighbour, err := enumLabel(driver, spec.Before)
		if err != nil {
			return nil, err
		}
		stmt += " BEFORE " + neighbour
	case spec.After != "":
		neighbour, err := enumLabel(driver, spec.After)
		if err != nil {
			return nil, err
		}
		stmt += " AFTER " + neighbour
	}
	return planOf(stmt), nil
}
