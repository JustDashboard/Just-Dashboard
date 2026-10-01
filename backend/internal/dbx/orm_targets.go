package dbx

// The target catalogue: what each generator is called, what it writes, which
// engines it can be pointed at and which switches it takes. It is one table
// because a target is all of those at once — a generator added without its
// engines listed would be offered for ClickHouse, and one added without its
// options listed would have switches the form never draws.

// ORMTargetInfo is one generator as the picker shows it.
type ORMTargetInfo struct {
	ID          ORMTarget `json:"id"`
	Label       string    `json:"label"`
	Filename    string    `json:"filename"`
	Description string    `json:"description"`
	// Language names the syntax of the output, for the code view's highlighter.
	Language string `json:"language"`
	Group    string `json:"group"`
	// Engines are the drivers this target can be generated for. Unsupported
	// gives the reason for each of the others, so a disabled choice can say why
	// without a round trip that would only be refused.
	Engines     []Driver          `json:"engines"`
	Unsupported map[Driver]string `json:"unsupported"`
	Options     []ORMOption       `json:"options"`
}

// The four groups the picker is arranged in.
const (
	ormGroupORM     = "ORM"
	ormGroupBuilder = "Query builder"
	ormGroupTypes   = "Types & validation"
	ormGroupSchema  = "Schema"
)

type ormTargetSpec struct {
	id          ORMTarget
	label       string
	filename    string
	description string
	language    string
	group       string
	engines     []Driver
	refusals    map[Driver]string
	options     []ORMOption
	generate    func(g *ormGen) []ORMFile
}

func (s ormTargetSpec) option(id string) (ORMOption, bool) {
	for _, o := range s.options {
		if o.ID == id {
			return o, true
		}
	}
	return ORMOption{}, false
}

var (
	ormEveryEngine = []Driver{
		DriverPostgres, DriverMySQL, DriverSQLite, DriverMSSQL, DriverClickHouse, DriverOracle,
	}
	ormRelational = []Driver{
		DriverPostgres, DriverMySQL, DriverSQLite, DriverMSSQL, DriverOracle,
	}
	ormFourEngines  = []Driver{DriverPostgres, DriverMySQL, DriverSQLite, DriverMSSQL}
	ormThreeEngines = []Driver{DriverPostgres, DriverMySQL, DriverSQLite}
)

// ormNoClickHouse is the refusal every relational mapper shares. ClickHouse's
// "primary key" is a sort order and it has no foreign keys, so an entity
// class for one of its tables would promise identity it cannot keep.
func ormNoClickHouse(label string) string {
	return label + " has no ClickHouse support: a ClickHouse table has a sorting key rather than a " +
		"primary key and no foreign keys, so there is nothing for an entity to be identified by. " +
		"The type and SQL targets work for ClickHouse."
}

// --- option builders ------------------------------------------------------

func ormOptRelations() ORMOption {
	return ORMOption{ID: "relations", Label: "Relations", Type: "boolean", Default: true,
		Description: "Turn foreign keys into relations, on both sides."}
}

func ormOptEnums() ORMOption {
	return ORMOption{ID: "enums", Label: "Enums", Type: "boolean", Default: true,
		Description: "Emit enum types as enums rather than as plain strings."}
}

func ormOptDefaults() ORMOption {
	return ORMOption{ID: "defaults", Label: "Defaults", Type: "boolean", Default: true,
		Description: "Carry over column defaults: auto-increment, now(), generated UUIDs and literals."}
}

func ormOptViews(def bool) ORMOption {
	return ORMOption{ID: "views", Label: "Views", Type: "boolean", Default: def,
		Description: "Include views and materialized views, as read-only shapes."}
}

func ormOptNaming() ORMOption {
	return ORMOption{ID: "naming", Label: "Naming", Type: "select", Default: ORMNamingPreserve,
		Description: "Keep the database's own names, or use camelCase in code and map back to them.",
		Choices: []ORMChoice{
			{Value: ORMNamingPreserve, Label: "As in the database"},
			{Value: ORMNamingCamel, Label: "camelCase"},
		}}
}

func ormOptSplit() ORMOption {
	return ORMOption{ID: "split", Label: "One file per model", Type: "boolean", Default: false,
		Description: "Write each model to its own file instead of one file holding them all."}
}

func ormOptPackage() ORMOption {
	return ORMOption{ID: "package", Label: "Package", Type: "text", Default: "models",
		Description: "The Go package the file declares."}
}

func ormOptJSONTags() ORMOption {
	return ORMOption{ID: "jsonTags", Label: "JSON tags", Type: "boolean", Default: true,
		Description: "Add json struct tags beside the database ones."}
}

// --- the catalogue --------------------------------------------------------

// ormTargetSpecs is in the order the picker offers them. The first four are
// the original targets and keep their place, ids, labels and file names.
var ormTargetSpecs []ormTargetSpec

func init() {
	ormTargetSpecs = []ormTargetSpec{
		{
			id: ORMPrisma, label: "Prisma", filename: "schema.prisma", language: "prisma",
			group:       ormGroupORM,
			description: "A schema.prisma to drop into an existing Prisma project.",
			engines:     ormFourEngines,
			refusals: map[Driver]string{
				DriverClickHouse: "Prisma has no connector for ClickHouse, so there is no provider a schema.prisma could name. The type and SQL targets work for ClickHouse.",
				DriverOracle:     "Prisma has no connector for Oracle, so there is no provider a schema.prisma could name.",
			},
			options: []ORMOption{
				ormOptRelations(), ormOptEnums(), ormOptDefaults(), ormOptViews(false), ormOptNaming(),
				{ID: "prismaVersion", Label: "Prisma version", Type: "select", Default: "6",
					Description: "Prisma 7 moved the connection URL out of the schema into prisma.config.ts.",
					Choices: []ORMChoice{
						{Value: "6", Label: "6 and earlier"},
						{Value: "7", Label: "7 and later"},
					}},
			},
			generate: generatePrisma,
		},
		{
			id: ORMDrizzle, label: "Drizzle", filename: "schema.ts", language: "typescript",
			group:       ormGroupORM,
			description: "Drizzle ORM table definitions.",
			engines:     ormThreeEngines,
			refusals: map[Driver]string{
				DriverMSSQL:      "Drizzle's released packages have column builders for PostgreSQL, MySQL and SQLite, and none for SQL Server.",
				DriverOracle:     "Drizzle's released packages have column builders for PostgreSQL, MySQL and SQLite, and none for Oracle.",
				DriverClickHouse: "Drizzle's released packages have column builders for PostgreSQL, MySQL and SQLite, and none for ClickHouse. The type and SQL targets work for ClickHouse.",
			},
			options: []ORMOption{
				ormOptRelations(), ormOptEnums(), ormOptDefaults(), ormOptViews(false), ormOptNaming(),
			},
			generate: generateDrizzle,
		},
		{
			id: ORMTypeScript, label: "TypeScript types", filename: "types.ts", language: "typescript",
			group:       ormGroupTypes,
			description: "Plain interfaces — no runtime dependency, useful with any client.",
			engines:     ormEveryEngine,
			options: []ORMOption{
				ormOptEnums(), ormOptViews(true), ormOptNaming(),
				{ID: "dates", Label: "Dates", Type: "select", Default: "string",
					Description: "What a date or timestamp column is typed as: the string a JSON response carries, or the Date a driver returns.",
					Choices: []ORMChoice{
						{Value: "string", Label: "string"},
						{Value: "Date", Label: "Date"},
					}},
			},
			generate: generateTypeScript,
		},
		{
			id: ORMZod, label: "Zod schemas", filename: "schemas.ts", language: "typescript",
			group:       ormGroupTypes,
			description: "Runtime validators, plus an insert variant with defaults optional.",
			engines:     ormEveryEngine,
			options: []ORMOption{
				ormOptEnums(), ormOptViews(true), ormOptNaming(),
				{ID: "insertSchemas", Label: "Insert schemas", Type: "boolean", Default: true,
					Description: "Add an Insert variant per table with the columns the database fills in made optional."},
			},
			generate: generateZod,
		},
		{
			id: ORMKysely, label: "Kysely", filename: "database.ts", language: "typescript",
			group:       ormGroupBuilder,
			description: "The Database interface Kysely is typed by, with Generated columns marked.",
			engines:     ormEveryEngine,
			options:     []ORMOption{ormOptEnums(), ormOptViews(true), ormOptNaming()},
			generate:    generateKysely,
		},
		{
			id: ORMTypeORM, label: "TypeORM", filename: "entities.ts", language: "typescript",
			group:       ormGroupORM,
			description: "Entity classes with decorators, relations on both sides.",
			engines:     ormRelational,
			refusals:    map[Driver]string{DriverClickHouse: ormNoClickHouse("TypeORM")},
			options: []ORMOption{
				ormOptRelations(), ormOptEnums(), ormOptDefaults(), ormOptViews(false), ormOptNaming(),
				ormOptSplit(),
			},
			generate: generateTypeORM,
		},
		{
			id: ORMMikroORM, label: "MikroORM", filename: "entities.ts", language: "typescript",
			group:       ormGroupORM,
			description: "Entity classes for MikroORM's decorator API.",
			engines:     ormRelational,
			refusals:    map[Driver]string{DriverClickHouse: ormNoClickHouse("MikroORM")},
			options: []ORMOption{
				ormOptRelations(), ormOptEnums(), ormOptDefaults(), ormOptNaming(), ormOptSplit(),
			},
			generate: generateMikroORM,
		},
		{
			id: ORMSequelize, label: "Sequelize", filename: "models.ts", language: "typescript",
			group:       ormGroupORM,
			description: "Typed Sequelize models with an initModels function that wires the associations.",
			engines:     ormRelational,
			refusals:    map[Driver]string{DriverClickHouse: ormNoClickHouse("Sequelize")},
			options: []ORMOption{
				ormOptRelations(), ormOptEnums(), ormOptDefaults(), ormOptNaming(), ormOptSplit(),
			},
			generate: generateSequelize,
		},
		{
			id: ORMSQLAlchemy, label: "SQLAlchemy", filename: "models.py", language: "python",
			group:       ormGroupORM,
			description: "SQLAlchemy 2 declarative models with typed Mapped columns.",
			engines:     ormRelational,
			refusals:    map[Driver]string{DriverClickHouse: ormNoClickHouse("SQLAlchemy's own dialect set")},
			options: []ORMOption{
				ormOptRelations(), ormOptEnums(), ormOptDefaults(), ormOptViews(false),
			},
			generate: generateSQLAlchemy,
		},
		{
			id: ORMDjango, label: "Django", filename: "models.py", language: "python",
			group:       ormGroupORM,
			description: "A models.py in the shape inspectdb writes, with relations and choices filled in.",
			engines:     ormRelational,
			refusals:    map[Driver]string{DriverClickHouse: ormNoClickHouse("Django")},
			options: []ORMOption{
				ormOptRelations(), ormOptEnums(), ormOptDefaults(),
				{ID: "managed", Label: "Managed", Type: "boolean", Default: false,
					Description: "Let Django's migrations own these tables. Off keeps managed = False, which is right for a database that already exists."},
			},
			generate: generateDjango,
		},
		{
			id: ORMGorm, label: "GORM", filename: "models.go", language: "go",
			group:       ormGroupORM,
			description: "Go structs with gorm tags, relations and TableName methods.",
			engines:     ormEveryEngine,
			options: []ORMOption{
				ormOptRelations(), ormOptEnums(), ormOptDefaults(), ormOptViews(false),
				ormOptPackage(), ormOptJSONTags(),
			},
			generate: generateGorm,
		},
		{
			id: ORMGoStructs, label: "Go structs", filename: "models.go", language: "go",
			group:       ormGroupBuilder,
			description: "Plain structs with db tags, the shape sqlc and sqlx scan rows into.",
			engines:     ormEveryEngine,
			options: []ORMOption{
				ormOptEnums(), ormOptViews(true), ormOptPackage(), ormOptJSONTags(),
				{ID: "nulls", Label: "Nullable columns", Type: "select", Default: "sql",
					Description: "How a nullable column is typed: database/sql's Null types, as sqlc writes them, or pointers.",
					Choices: []ORMChoice{
						{Value: "sql", Label: "sql.Null types"},
						{Value: "pointer", Label: "Pointers"},
					}},
			},
			generate: generateGoStructs,
		},
		{
			id: ORMDiesel, label: "Diesel", filename: "schema.rs", language: "rust",
			group:       ormGroupORM,
			description: "Diesel's table! schema and the Queryable structs that read from it.",
			engines:     ormThreeEngines,
			refusals: map[Driver]string{
				DriverMSSQL:      "Diesel has backends for PostgreSQL, MySQL and SQLite, and none for SQL Server.",
				DriverOracle:     "Diesel has backends for PostgreSQL, MySQL and SQLite, and none for Oracle.",
				DriverClickHouse: "Diesel has backends for PostgreSQL, MySQL and SQLite, and none for ClickHouse. The type and SQL targets work for ClickHouse.",
			},
			options:  []ORMOption{ormOptRelations(), ormOptEnums()},
			generate: generateDiesel,
		},
		{
			id: ORMEloquent, label: "Eloquent", filename: "Models.php", language: "php",
			group:       ormGroupORM,
			description: "Laravel models with $fillable, $casts and relation methods.",
			engines:     ormFourEngines,
			refusals: map[Driver]string{
				DriverOracle:     "Laravel ships drivers for PostgreSQL, MySQL, SQLite and SQL Server, and none for Oracle.",
				DriverClickHouse: ormNoClickHouse("Eloquent"),
			},
			options:  []ORMOption{ormOptRelations(), ormOptEnums(), ormOptSplit()},
			generate: generateEloquent,
		},
		{
			id: ORMJSONSchema, label: "JSON Schema", filename: "schema.json", language: "json",
			group:       ormGroupTypes,
			description: "One JSON Schema document with a definition per table.",
			engines:     ormEveryEngine,
			options: []ORMOption{
				ormOptEnums(), ormOptDefaults(), ormOptViews(false), ormOptNaming(),
			},
			generate: generateJSONSchema,
		},
		{
			id: ORMGraphQL, label: "GraphQL", filename: "schema.graphql", language: "graphql",
			group:       ormGroupSchema,
			description: "GraphQL SDL object types, with relations as fields.",
			engines:     ormEveryEngine,
			options: []ORMOption{
				ormOptRelations(), ormOptEnums(), ormOptViews(false), ormOptNaming(),
				{ID: "inputs", Label: "Input types", Type: "boolean", Default: false,
					Description: "Add an input type per table for mutations, with the columns the database fills in optional."},
			},
			generate: generateGraphQL,
		},
		{
			id: ORMSQL, label: "SQL", filename: "schema.sql", language: "sql",
			group:       ormGroupSchema,
			description: "The schema as CREATE statements in the source engine's own dialect.",
			engines:     ormEveryEngine,
			options: []ORMOption{
				ormOptRelations(), ormOptViews(false),
				{ID: "ifNotExists", Label: "IF NOT EXISTS", Type: "boolean", Default: false,
					Description: "Guard each CREATE so the script can be run against a database that already has some of the objects."},
			},
			generate: generateSQL,
		},
	}
}

func ormTargetSpecFor(t ORMTarget) (ormTargetSpec, bool) {
	for _, s := range ormTargetSpecs {
		if s.id == t {
			return s, true
		}
	}
	return ormTargetSpec{}, false
}

// ORMTargetCatalogue is every generator with what the picker needs to offer it.
func ORMTargetCatalogue() []ORMTargetInfo {
	out := make([]ORMTargetInfo, 0, len(ormTargetSpecs))
	for _, s := range ormTargetSpecs {
		info := ORMTargetInfo{
			ID: s.id, Label: s.label, Filename: s.filename, Description: s.description,
			Language: s.language, Group: s.group,
			Engines: s.engines, Unsupported: map[Driver]string{}, Options: s.options,
		}
		for _, d := range ormEveryEngine {
			if reason := ORMUnsupported(s.id, d); reason != "" {
				info.Unsupported[d] = reason
			}
		}
		out = append(out, info)
	}
	return out
}
