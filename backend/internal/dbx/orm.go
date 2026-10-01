package dbx

import (
	"errors"
	"fmt"
	"go/token"
	"regexp"
	"strings"
)

// ORM schema generation is this dashboard's answer to "does it support Prisma".
// The honest support a server panel can give is introspection: read the live
// database and emit the schema file a developer would otherwise get from
// `prisma db pull` or `drizzle-kit pull`, without needing either CLI or a
// Node toolchain on the box. The output is a reviewed starting point, not a
// guaranteed drop-in — Postgres keeps no canonical DDL, type mappings are
// lossy in both directions, and relation names are inferred — so every
// generated file says so in its header, and everything a target could not
// express comes back as a warning instead of being dropped in silence.
//
// The generators are pure: they take an introspected structure and return
// text, which is what lets them be unit-tested without a database. The one
// file here that talks to a server is orm_catalog.go, and all it does is fill
// in that structure.

// ORMTarget names a supported code generator.
type ORMTarget string

const (
	ORMPrisma     ORMTarget = "prisma"
	ORMDrizzle    ORMTarget = "drizzle"
	ORMTypeScript ORMTarget = "typescript"
	ORMZod        ORMTarget = "zod"
	ORMKysely     ORMTarget = "kysely"
	ORMTypeORM    ORMTarget = "typeorm"
	ORMMikroORM   ORMTarget = "mikroorm"
	ORMSequelize  ORMTarget = "sequelize"
	ORMSQLAlchemy ORMTarget = "sqlalchemy"
	ORMDjango     ORMTarget = "django"
	ORMGorm       ORMTarget = "gorm"
	ORMGoStructs  ORMTarget = "go"
	ORMDiesel     ORMTarget = "diesel"
	ORMEloquent   ORMTarget = "eloquent"
	ORMJSONSchema ORMTarget = "jsonschema"
	ORMGraphQL    ORMTarget = "graphql"
	ORMSQL        ORMTarget = "sql"
)

func (t ORMTarget) Valid() bool {
	_, ok := ormTargetSpecFor(t)
	return ok
}

// ORMTargets is the list the UI offers, kept here so a new generator appears in
// the picker without a second list to update.
func ORMTargets() []ORMTarget {
	out := make([]ORMTarget, 0, len(ormTargetSpecs))
	for _, s := range ormTargetSpecs {
		out = append(out, s.id)
	}
	return out
}

// ErrORMRequest marks a generation request that cannot be honoured as asked —
// an option the target does not have, a table that is not there, an engine the
// target has no connector for. The caller's content is at fault, not the
// server's, which is the difference between a 400 and a 502 at the route.
var ErrORMRequest = errors.New("orm request")

func ormRequestErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrORMRequest}, args...)...)
}

// ORMRequestMessage strips the sentinel's own text from an error wrapping
// ErrORMRequest, leaving the sentence meant for the operator.
func ORMRequestMessage(err error) string {
	return strings.TrimPrefix(err.Error(), ErrORMRequest.Error()+": ")
}

// --- options --------------------------------------------------------------

// ORMOption describes one switch a target accepts, so the form that offers it
// is drawn from the server's list and cannot offer a switch the generator
// would refuse.
type ORMOption struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	Description string      `json:"description"`
	Type        string      `json:"type"` // "boolean", "select" or "text"
	Default     any         `json:"default"`
	Choices     []ORMChoice `json:"choices,omitempty"`
}

type ORMChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Naming conventions a target can be asked for.
const (
	ORMNamingPreserve = "preserve"
	ORMNamingCamel    = "camel"
)

// ORMRequest is the body of a generation request. Every field but the target
// is optional; a nil switch means "the target's default", which is not the
// same for every target (views are row types to TypeScript and noise to
// Prisma), so absence has to stay distinguishable from false.
type ORMRequest struct {
	Target  ORMTarget `json:"target"`
	Schema  string    `json:"schema"`
	Schemas []string  `json:"schemas"`
	Tables  []string  `json:"tables"`

	Relations *bool  `json:"relations"`
	Enums     *bool  `json:"enums"`
	Defaults  *bool  `json:"defaults"`
	Views     *bool  `json:"views"`
	Naming    string `json:"naming"`

	PrismaVersion string `json:"prismaVersion"`
	Split         *bool  `json:"split"`
	Package       string `json:"package"`
	JSONTags      *bool  `json:"jsonTags"`
	Nulls         string `json:"nulls"`
	Managed       *bool  `json:"managed"`
	InsertSchemas *bool  `json:"insertSchemas"`
	Dates         string `json:"dates"`
	IfNotExists   *bool  `json:"ifNotExists"`
	Inputs        *bool  `json:"inputs"`
}

// ORMOptions is a request with every switch resolved against its target.
type ORMOptions struct {
	Target ORMTarget
	// Selected is true when the caller named tables. A relation that leaves the
	// selection is then dropped quietly — it is what was asked for — where one
	// that leaves the selected schemas is worth a warning.
	Selected bool

	Relations bool
	Enums     bool
	Defaults  bool
	Views     bool
	Naming    string

	PrismaVersion string
	Split         bool
	Package       string
	JSONTags      bool
	Nulls         string
	Managed       bool
	InsertSchemas bool
	Dates         string
	IfNotExists   bool
	Inputs        bool
}

// ormPackageRe is what a Go package clause accepts and what a directory name
// tolerates. The value is written into generated source, so it is matched
// rather than escaped.
var ormPackageRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// Options resolves a request against its target: defaults filled in, choices
// checked, and any switch the target does not declare refused by name. Silently
// ignoring one would let a form show a toggle that does nothing.
func (r ORMRequest) Options() (ORMOptions, error) {
	spec, ok := ormTargetSpecFor(r.Target)
	if !ok {
		return ORMOptions{}, ormRequestErrorf("unsupported ORM target %q", r.Target)
	}
	o := ORMOptions{Target: r.Target, Selected: len(r.Tables) > 0}

	flag := func(id string, given *bool, dst *bool) error {
		opt, declared := spec.option(id)
		if !declared {
			if given != nil {
				return ormRequestErrorf("option %q does not apply to the %s target", id, spec.label)
			}
			return nil
		}
		*dst, _ = opt.Default.(bool)
		if given != nil {
			*dst = *given
		}
		return nil
	}
	choice := func(id, given string, dst *string) error {
		opt, declared := spec.option(id)
		if !declared {
			if given != "" {
				return ormRequestErrorf("option %q does not apply to the %s target", id, spec.label)
			}
			return nil
		}
		*dst, _ = opt.Default.(string)
		if given == "" {
			return nil
		}
		if opt.Type == "select" {
			values := make([]string, 0, len(opt.Choices))
			for _, c := range opt.Choices {
				if c.Value == given {
					*dst = given
					return nil
				}
				values = append(values, c.Value)
			}
			return ormRequestErrorf("%s must be one of %s", id, strings.Join(values, ", "))
		}
		*dst = given
		return nil
	}

	for _, err := range []error{
		flag("relations", r.Relations, &o.Relations),
		flag("enums", r.Enums, &o.Enums),
		flag("defaults", r.Defaults, &o.Defaults),
		flag("views", r.Views, &o.Views),
		flag("split", r.Split, &o.Split),
		flag("jsonTags", r.JSONTags, &o.JSONTags),
		flag("managed", r.Managed, &o.Managed),
		flag("insertSchemas", r.InsertSchemas, &o.InsertSchemas),
		flag("ifNotExists", r.IfNotExists, &o.IfNotExists),
		flag("inputs", r.Inputs, &o.Inputs),
		choice("naming", r.Naming, &o.Naming),
		choice("nulls", r.Nulls, &o.Nulls),
		choice("dates", r.Dates, &o.Dates),
		choice("package", r.Package, &o.Package),
		choice("prismaVersion", r.PrismaVersion, &o.PrismaVersion),
	} {
		if err != nil {
			return ORMOptions{}, err
		}
	}
	// A keyword fits the pattern and is not a name: `package func` does not parse.
	if o.Package != "" && (!ormPackageRe.MatchString(o.Package) || token.IsKeyword(o.Package)) {
		return ORMOptions{}, ormRequestErrorf("package must be a lower-case Go package name")
	}
	return o, nil
}

// --- result ---------------------------------------------------------------

// ORMFile is one generated file. The name is a bare file name the generator
// chose, never a path taken from the request.
type ORMFile struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

// ORMCounts says what went into the output, so the page can state "7 tables,
// 2 enums" without parsing a language it does not know.
type ORMCounts struct {
	Tables    int `json:"tables"`
	Views     int `json:"views"`
	Enums     int `json:"enums"`
	Relations int `json:"relations"`
}

// ORMResult is a generation's whole answer. Schema and Filename repeat the
// first file: they are what the route returned before a target could produce
// more than one, and a caller that only knows those two still gets a complete
// file.
type ORMResult struct {
	Target   ORMTarget `json:"target"`
	Language string    `json:"language"`
	Schema   string    `json:"schema"`
	Filename string    `json:"filename"`
	Files    []ORMFile `json:"files"`
	Warnings []string  `json:"warnings"`
	Counts   ORMCounts `json:"counts"`
}

// ormWarningCap bounds the warning list. A schema with a thousand expression
// indexes should say so, not ship a thousand sentences to the browser.
const ormWarningCap = 200

// GenerateORMFiles renders a loaded schema for one target.
func GenerateORMFiles(s *ORMSchema, o ORMOptions) (*ORMResult, error) {
	spec, ok := ormTargetSpecFor(o.Target)
	if !ok {
		return nil, ormRequestErrorf("unsupported ORM target %q", o.Target)
	}
	if reason := ORMUnsupported(o.Target, s.Driver); reason != "" {
		return nil, ormRequestErrorf("%s", reason)
	}
	g := newORMGen(s, o)
	if len(g.models) == 0 {
		return nil, ormRequestErrorf("%s", g.nothingToGenerate())
	}
	files := spec.generate(g)
	if g.refusal != "" {
		return nil, ormRequestErrorf("%s", g.refusal)
	}
	if o.Target != ORMSQL {
		g.warnSkippedIndexes()
	}
	warnings := g.warnings
	if len(warnings) > ormWarningCap {
		more := len(warnings) - ormWarningCap
		warnings = append(warnings[:ormWarningCap:ormWarningCap],
			fmt.Sprintf("… and %d more notes like these.", more))
	}
	if warnings == nil {
		warnings = []string{}
	}
	return &ORMResult{
		Target: o.Target, Language: spec.language,
		Schema: files[0].Content, Filename: files[0].Filename,
		Files: files, Warnings: warnings, Counts: g.counts(),
	}, nil
}

// GenerateORM renders a target with its default options from the structures
// the rest of the package already introspects. It is the form the route used
// before options existed, kept because it is also the shortest way to ask.
func GenerateORM(target ORMTarget, driver Driver, tables []Table, details map[string]*TableDetail) (string, error) {
	if !driver.IsSQL() {
		return "", errors.New(ormNonSQLReason(driver))
	}
	if !target.Valid() {
		return "", fmt.Errorf("unsupported ORM target %q", target)
	}
	opts, err := ORMRequest{Target: target}.Options()
	if err != nil {
		return "", err
	}
	res, err := GenerateORMFiles(NewORMSchema(driver, tables, details), opts)
	if err != nil {
		return "", errors.New(ORMRequestMessage(err))
	}
	return res.Schema, nil
}

// ormNonSQLReason is the refusal for an engine with no relational schema.
// Mongo is rejected here rather than producing a misleading relational schema.
func ormNonSQLReason(driver Driver) string {
	if driver == DriverMongo {
		return "ORM schema generation covers the SQL engines; use the connection's native tooling for MongoDB"
	}
	return fmt.Sprintf("ORM schema generation covers the SQL engines; %s has no relational schema to read", driver)
}

// ORMUnsupported returns why a target cannot be generated for an engine, or ""
// when it can. A refusal is the answer here because the alternative is what
// this code used to do — emit a PostgreSQL schema for a ClickHouse server — and
// a file that looks right and is wrong is worse than no file.
func ORMUnsupported(target ORMTarget, driver Driver) string {
	if !driver.IsSQL() {
		return ormNonSQLReason(driver)
	}
	spec, ok := ormTargetSpecFor(target)
	if !ok {
		return fmt.Sprintf("unsupported ORM target %q", target)
	}
	for _, d := range spec.engines {
		if d == driver {
			return ""
		}
	}
	if reason := spec.refusals[driver]; reason != "" {
		return reason
	}
	return fmt.Sprintf("%s cannot be generated for %s", spec.label, ormEngineName(driver, ""))
}

// ormEngineName is how an engine is named in a sentence.
func ormEngineName(driver Driver, flavor string) string {
	switch flavor {
	case "cockroachdb":
		return "CockroachDB"
	case "mariadb":
		return "MariaDB"
	}
	switch driver {
	case DriverPostgres:
		return "PostgreSQL"
	case DriverMySQL:
		return "MySQL"
	case DriverSQLite:
		return "SQLite"
	case DriverMSSQL:
		return "SQL Server"
	case DriverClickHouse:
		return "ClickHouse"
	case DriverOracle:
		return "Oracle"
	}
	return string(driver)
}
