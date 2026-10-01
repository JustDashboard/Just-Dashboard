package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-sql-driver/mysql"
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

func (c ColumnChange) validate(driver Driver) (typ, def string, setDefault bool, err error) {
	typ = strings.TrimSpace(c.Type)
	if typ != "" {
		if err := validateType(driver, typ); err != nil {
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
	plan, err := planAlterColumn(ctx, db, driver, c)
	if err != nil {
		return nil, err
	}
	if c.Default != nil {
		plan.vouching(unvouchedDefault(*c.Default))
	}
	if using := strings.TrimSpace(c.Using); using != "" {
		plan.vouching(unvouchedCalls(driver, using))
	}
	return plan, nil
}

func planAlterColumn(ctx context.Context, db *sql.DB, driver Driver, c ColumnChange) (*DDLPlan, error) {
	d, err := ddlDialect(driver, OpAlterColumn)
	if err != nil {
		return nil, err
	}
	typ, def, setDefault, err := c.validate(driver)
	if err != nil {
		return nil, err
	}
	rel, err := ddlRel(d, c.Schema, c.Table)
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
			// A collation belongs to the type it was declared with; the new
			// one takes the table's unless it names its own character set.
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
			current, err := readMSSQLColumn(ctx, db, d, c.Schema, c.Table, c.Column)
			if err != nil {
				return nil, err
			}
			if typ != "" {
				current.retype(typ)
			}
			if c.Nullable != nil {
				current.nullable = *c.Nullable
			}
			steps = append(steps, "ALTER TABLE "+rel+" ALTER COLUMN "+col+" "+current.render())
		}
		if c.DropDefault || setDefault {
			// A default is a constraint object here, so replacing it is
			// dropping the one that exists and adding another.
			existing, err := mssqlDefaultConstraints(ctx, db, c.Schema, c.Table, c.Column)
			if err != nil {
				return nil, planRead("the column's default", err)
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

// mssqlColumn is a column as SQL Server would have it declared again. ALTER
// COLUMN restates the type, the collation and the nullability together, and
// one left out is not "unchanged": a missing NULL takes a session setting, and
// a missing COLLATE takes the database's default, which re-collates a column
// that was declared with any other.
type mssqlColumn struct {
	typ      string
	nullable bool
	// collation is set only where the column's differs from the database's,
	// so a column that follows the default is restated without one and keeps
	// following it.
	collation string
}

var mssqlCollationRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,128}$`)

// mssqlCharacterTypes are the types that carry a collation.
var mssqlCharacterTypes = stringSet("char", "varchar", "nchar", "nvarchar", "text", "ntext")

// retype gives the column a new type. Its collation goes with it only when
// the new type is one that has a collation at all.
func (c *mssqlColumn) retype(typ string) {
	base := strings.ToLower(typ)
	if i := strings.IndexAny(base, "( "); i >= 0 {
		base = base[:i]
	}
	if !mssqlCharacterTypes[base] {
		c.collation = ""
	}
	c.typ = typ
}

func (c *mssqlColumn) render() string {
	out := c.typ
	if c.collation != "" {
		out += " COLLATE " + c.collation
	}
	if c.nullable {
		return out + " NULL"
	}
	return out + " NOT NULL"
}

// readMSSQLColumn reads what ALTER COLUMN has to restate. An alias type is
// named with its schema and quoted: it is an identifier someone chose, and it
// is about to be written into a statement.
func readMSSQLColumn(ctx context.Context, db *sql.DB, d Dialect, schema, table, column string) (*mssqlColumn, error) {
	var (
		c                           mssqlColumn
		typ, typeSchema             string
		collation, databaseDefault  string
		maxLength, precision, scale int
		computed, userDefined       bool
	)
	err := db.QueryRowContext(ctx, `
	  SELECT t.name, ts.name, c.max_length, c.precision, c.scale, c.is_nullable,
	         ISNULL(c.collation_name, ''),
	         ISNULL(CONVERT(NVARCHAR(128), DATABASEPROPERTYEX(DB_NAME(), 'Collation')), ''),
	         c.is_computed, t.is_user_defined
	  FROM sys.columns c
	  JOIN sys.objects o ON o.object_id = c.object_id
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  JOIN sys.types t ON t.user_type_id = c.user_type_id
	  JOIN sys.schemas ts ON ts.schema_id = t.schema_id
	  WHERE `+mssqlSchemaIs("s")+` AND o.name = @p2 AND c.name = @p3 AND o.type = 'U'`,
		schema, table, column).Scan(&typ, &typeSchema, &maxLength, &precision, &scale, &c.nullable,
		&collation, &databaseDefault, &computed, &userDefined)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no column %s on %s", column, table)
	}
	if err != nil {
		return nil, planRead("the column as it is now", err)
	}
	if computed {
		return nil, fmt.Errorf("%s is a computed column; change its expression from the Query tab", column)
	}
	c.typ = mssqlTypeName(typ, maxLength, precision, scale)
	if userDefined {
		// An alias type carries its own length and, being a name, is quoted.
		if c.typ, err = qualify(d, typeSchema, typ); err != nil {
			return nil, err
		}
	}
	if collation != "" && !strings.EqualFold(collation, databaseDefault) {
		if !mssqlCollationRe.MatchString(collation) {
			return nil, fmt.Errorf("the column's collation %q cannot be restated safely; change this column from the Query tab", collation)
		}
		c.collation = collation
	}
	return &c, nil
}

// mysqlColumn is a column as MySQL would have it declared again. MODIFY
// COLUMN replaces the whole definition: anything not restated — the comment,
// AUTO_INCREMENT, ON UPDATE, the collation, INVISIBLE, a MariaDB column's own
// CHECK (which is all that makes a MariaDB JSON column one) — is silently
// reset, so changing one property means reading the others first. What this
// cannot restate it refuses by name rather than drop.
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
	invisible     bool
	// unversioned is MariaDB's WITHOUT SYSTEM VERSIONING on one column of a
	// versioned table.
	unversioned bool
	// checks are a MariaDB column's own CHECK clauses. MySQL files a column's
	// check as a constraint of the table, which MODIFY leaves alone.
	checks []string
	// srid is a spatial column's reference system, with the product's own
	// spelling of the attribute.
	srid string
}

var (
	mysqlOnUpdateRe  = regexp.MustCompile(`(?i)on update (current_timestamp(?:\(\d*\))?)`)
	mysqlCollationRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	// mysqlExtraKnownRe are the EXTRA entries readMySQLColumn accounts for.
	mysqlExtraKnownRe = regexp.MustCompile(`(?i)on update current_timestamp(?:\(\d*\))?|auto_increment|default_generated|invisible|without system versioning`)
)

// mysqlSpatialTypes are the column types that carry a reference system.
var mysqlSpatialTypes = stringSet("geometry", "point", "linestring", "polygon", "multipoint",
	"multilinestring", "multipolygon", "geometrycollection", "geomcollection")

// mysqlUnknownExtra returns what is left of a column's EXTRA once every entry
// readMySQLColumn accounts for is taken out of it.
func mysqlUnknownExtra(extra string) string {
	return strings.Trim(mysqlExtraKnownRe.ReplaceAllString(extra, ""), ", ")
}

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
		return nil, planRead("the column as it is now", err)
	}
	if generation != "" {
		return nil, fmt.Errorf("%s is a generated column; change its expression from the Query tab", column)
	}
	maria := strings.Contains(strings.ToLower(version), "mariadb")
	extra = strings.ToLower(extra)
	c.nullable = nullable == "YES"
	c.dflt = mysqlDefaultSQL(c.columnType, dflt, extra, maria)
	c.collation = collation.String
	c.autoIncrement = strings.Contains(extra, "auto_increment")
	c.invisible = strings.Contains(extra, "invisible")
	c.unversioned = strings.Contains(extra, "without system versioning")
	if m := mysqlOnUpdateRe.FindStringSubmatch(extra); m != nil {
		c.onUpdate = strings.ToUpper(m[1])
	}
	// EXTRA is where a server says what else a column is. An entry this does
	// not know is an attribute MODIFY would drop without a word.
	if rest := mysqlUnknownExtra(extra); rest != "" {
		return nil, fmt.Errorf("%s is declared %q, which this form cannot restate and MODIFY COLUMN would drop; "+
			"change this column from the Query tab", column, rest)
	}
	if maria {
		if c.checks, err = mariaColumnChecks(ctx, db, schema, table, column); err != nil {
			return nil, err
		}
	}
	base := strings.ToLower(c.columnType)
	if i := strings.IndexAny(base, "( "); i >= 0 {
		base = base[:i]
	}
	if mysqlSpatialTypes[base] {
		if c.srid, err = mysqlColumnSRID(ctx, db, schema, table, column, maria); err != nil {
			return nil, err
		}
	}
	return &c, nil
}

// The two server errors that mean "this server is too old to have that", as
// opposed to "this server would not answer".
const (
	mysqlErrUnknownColumn = 1054
	mysqlErrUnknownTable  = 1109
)

// mysqlErrorNumber returns the number the server gave an error, or zero for
// an error that is not the server's.
func mysqlErrorNumber(err error) uint16 {
	var e *mysql.MySQLError
	if errors.As(err, &e) {
		return e.Number
	}
	return 0
}

// mariaColumnChecks reads the CHECK clauses declared on a column itself.
// MariaDB names such a constraint after its column and marks its level.
func mariaColumnChecks(ctx context.Context, db *sql.DB, schema, table, column string) ([]string, error) {
	query := `
	  SELECT CHECK_CLAUSE
	  FROM information_schema.CHECK_CONSTRAINTS
	  WHERE ` + mysqlSchemaIs("CONSTRAINT_SCHEMA") + ` AND TABLE_NAME = ? AND CONSTRAINT_NAME = ?`
	rows, err := db.QueryContext(ctx, query+` AND LEVEL = 'Column'`, schema, table, column)
	if mysqlErrorNumber(err) == mysqlErrUnknownColumn {
		// LEVEL arrived in 10.5.10. Before it a column's own check is told from
		// a table's only by carrying the column's name.
		rows, err = db.QueryContext(ctx, query, schema, table, column)
	}
	if mysqlErrorNumber(err) == mysqlErrUnknownTable {
		// A server from before check constraints were kept has no such view,
		// and no check to lose.
		return nil, nil
	}
	if err != nil {
		return nil, planRead("the column's check constraint", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var clause string
		if err := rows.Scan(&clause); err != nil {
			return nil, planRead("the column's check constraint", err)
		}
		out = append(out, clause)
	}
	if err := rows.Err(); err != nil {
		return nil, planRead("the column's check constraint", err)
	}
	return out, nil
}

// mysqlColumnSRID returns a spatial column's reference system as the clause
// that declares it, or nothing for a column that has none. The two products
// keep it in different views and spell the attribute differently.
func mysqlColumnSRID(ctx context.Context, db *sql.DB, schema, table, column string, maria bool) (string, error) {
	query, form := `
	  SELECT SRS_ID FROM information_schema.COLUMNS
	  WHERE `+mysqlSchemaIs("TABLE_SCHEMA")+` AND TABLE_NAME = ? AND COLUMN_NAME = ?`, "SRID %d"
	if maria {
		query, form = `
	  SELECT SRID FROM information_schema.GEOMETRY_COLUMNS
	  WHERE `+mysqlSchemaIs("G_TABLE_SCHEMA")+` AND G_TABLE_NAME = ? AND G_GEOMETRY_COLUMN = ?`, "REF_SYSTEM_ID=%d"
	}
	var srid sql.NullInt64
	err := db.QueryRowContext(ctx, query, schema, table, column).Scan(&srid)
	if n := mysqlErrorNumber(err); err == sql.ErrNoRows || n == mysqlErrUnknownColumn || n == mysqlErrUnknownTable {
		// No row, or a server from before a column could declare one.
		return "", nil
	}
	if err != nil {
		return "", planRead("the column's spatial reference system", err)
	}
	if !srid.Valid || (maria && srid.Int64 == 0) {
		return "", nil
	}
	return fmt.Sprintf(form, srid.Int64), nil
}

func (c *mysqlColumn) render(d Dialect) (string, error) {
	out := c.columnType
	if strings.HasPrefix(c.srid, "REF_SYSTEM_ID") {
		out += " " + c.srid
	}
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
	if strings.HasPrefix(c.srid, "SRID") {
		out += " " + c.srid
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
	if c.invisible {
		out += " INVISIBLE"
	}
	if c.unversioned {
		out += " WITHOUT SYSTEM VERSIONING"
	}
	if c.comment != "" {
		comment, err := ddlLiteral(d.Driver(), "the comment", c.comment)
		if err != nil {
			return "", err
		}
		out += " COMMENT " + comment
	}
	// The clause is the server's own text for a constraint it already holds,
	// read back from its catalogue, not anything this request supplied.
	for _, clause := range c.checks {
		out += " CHECK (" + clause + ")"
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
	rel, err := ddlRel(d, spec.Schema, spec.Table)
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
	refRel, err := ddlRel(d, refSchema, spec.RefTable)
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
	rel, err := ddlRel(d, schema, table)
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
	rel, err := ddlRel(d, spec.Schema, spec.Table)
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
	return planOf("ALTER TABLE " + rel + " ADD CONSTRAINT " + name + " CHECK (" + expression + ")").
		vouching(unvouchedCalls(driver, expression)), nil
}

// PlanDropConstraint plans removing a unique or check constraint. MySQL drops
// the two differently, so when the caller does not say which it is, the
// catalogue is asked.
func PlanDropConstraint(ctx context.Context, db *sql.DB, driver Driver, schema, table, name, constraintType string) (*DDLPlan, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	rel, err := ddlRel(d, schema, table)
	if err != nil {
		return nil, err
	}
	q, err := d.QuoteIdent(name)
	if err != nil {
		return nil, err
	}
	constraintType = strings.ToLower(strings.TrimSpace(constraintType))
	if driver == DriverMySQL && strings.EqualFold(name, "PRIMARY") {
		// MySQL's primary key has one name on every table and a statement of
		// its own; it is neither of the two kinds the lookup below knows.
		return planOf("ALTER TABLE " + rel + " DROP PRIMARY KEY"), nil
	}
	if constraintType == "" && driver == DriverMySQL {
		constraints, err := mysqlDialect{}.tableConstraints(ctx, db, schema, table)
		if err != nil {
			return nil, planRead("the table's constraints", err)
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
	rel, err := ddlRel(d, spec.Schema, spec.Table)
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
	rel, err := ddlRel(d, spec.Schema, spec.Name)
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
	rel, err := ddlRel(d, schema, name)
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
	rel, err := ddlRel(d, spec.Schema, spec.Name)
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
