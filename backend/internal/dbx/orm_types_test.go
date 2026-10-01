package dbx

import (
	"errors"
	"strings"
	"testing"
)

// Every engine spells its types its own way, and each has its own reader. A
// type one reader does not know must come back unknown — never as whatever
// another engine would have made of the same word.
func TestORMTypesAreReadPerEngine(t *testing.T) {
	type want struct {
		kind      ormKind
		name      string
		length    int
		precision int
		scale     int
		unsigned  bool
		array     bool
	}
	cases := []struct {
		driver Driver
		raw    string
		want   want
	}{
		// PostgreSQL: information_schema's long names, format_type's, and the short ones.
		{DriverPostgres, "integer", want{kind: ormInt32, name: "int4"}},
		{DriverPostgres, "bigint", want{kind: ormInt64, name: "int8"}},
		{DriverPostgres, "smallint", want{kind: ormInt16, name: "int2"}},
		{DriverPostgres, "character varying(255)", want{kind: ormVarchar, name: "varchar", length: 255}},
		{DriverPostgres, "varchar", want{kind: ormVarchar, name: "varchar"}},
		{DriverPostgres, "character(2)", want{kind: ormChar, name: "char", length: 2}},
		{DriverPostgres, "numeric(12,2)", want{kind: ormDecimal, name: "numeric", precision: 12, scale: 2}},
		{DriverPostgres, "numeric", want{kind: ormDecimal, name: "numeric"}},
		{DriverPostgres, "timestamp with time zone", want{kind: ormDateTimeTZ, name: "timestamptz"}},
		{DriverPostgres, "timestamp(3) with time zone", want{kind: ormDateTimeTZ, name: "timestamptz", precision: 3}},
		{DriverPostgres, "timestamp without time zone", want{kind: ormDateTime, name: "timestamp"}},
		{DriverPostgres, "time with time zone", want{kind: ormTime, name: "timetz"}},
		{DriverPostgres, "double precision", want{kind: ormFloat64, name: "float8"}},
		{DriverPostgres, "text[]", want{kind: ormText, name: "text", array: true}},
		{DriverPostgres, "integer[]", want{kind: ormInt32, name: "int4", array: true}},
		{DriverPostgres, "jsonb", want{kind: ormJSON, name: "jsonb"}},
		{DriverPostgres, "uuid", want{kind: ormUUID, name: "uuid"}},
		{DriverPostgres, "bytea", want{kind: ormBytes, name: "bytea"}},
		{DriverPostgres, "bit(8)", want{kind: ormBit, name: "bit", length: 8}},
		{DriverPostgres, "inet", want{kind: ormNet, name: "inet"}},
		{DriverPostgres, "interval", want{kind: ormInterval, name: "interval"}},
		// What information_schema says for an enum and for an array: nothing.
		{DriverPostgres, "USER-DEFINED", want{kind: ormUnknown}},
		{DriverPostgres, "ARRAY", want{kind: ormUnknown, array: true}},
		{DriverPostgres, "tsvector", want{kind: ormUnknown, name: "tsvector"}},
		// A MySQL-only word is not a PostgreSQL type.
		{DriverPostgres, "mediumtext", want{kind: ormUnknown, name: "mediumtext"}},

		{DriverMySQL, "int", want{kind: ormInt32, name: "int"}},
		{DriverMySQL, "int(10) unsigned", want{kind: ormInt32, name: "int", unsigned: true}},
		{DriverMySQL, "bigint unsigned", want{kind: ormInt64, name: "bigint", unsigned: true}},
		{DriverMySQL, "tinyint(1)", want{kind: ormBool, name: "tinyint", length: 1}},
		{DriverMySQL, "tinyint(4)", want{kind: ormInt8, name: "tinyint"}},
		{DriverMySQL, "tinyint", want{kind: ormInt8, name: "tinyint"}},
		{DriverMySQL, "mediumint", want{kind: ormInt32, name: "mediumint"}},
		{DriverMySQL, "varchar(40)", want{kind: ormVarchar, name: "varchar", length: 40}},
		{DriverMySQL, "decimal(10,2)", want{kind: ormDecimal, name: "decimal", precision: 10, scale: 2}},
		{DriverMySQL, "datetime(6)", want{kind: ormDateTime, name: "datetime", precision: 6}},
		{DriverMySQL, "timestamp", want{kind: ormDateTimeTZ, name: "timestamp"}},
		{DriverMySQL, "mediumtext", want{kind: ormText, name: "mediumtext"}},
		{DriverMySQL, "longblob", want{kind: ormBytes, name: "longblob"}},
		{DriverMySQL, "binary(16)", want{kind: ormBytes, name: "binary", length: 16}},
		{DriverMySQL, "json", want{kind: ormJSON, name: "json"}},
		{DriverMySQL, "year", want{kind: ormYear, name: "year"}},
		{DriverMySQL, "bit(1)", want{kind: ormBool, name: "bit", length: 1}},
		{DriverMySQL, "bit(8)", want{kind: ormBit, name: "bit", length: 8}},
		// A PostgreSQL-only word is not a MySQL type.
		{DriverMySQL, "jsonb", want{kind: ormUnknown, name: "jsonb"}},

		// SQLite: common declared names, then the engine's own affinity rule.
		{DriverSQLite, "INTEGER", want{kind: ormInt32, name: "integer"}},
		{DriverSQLite, "BIGINT", want{kind: ormInt64, name: "integer"}},
		{DriverSQLite, "VARCHAR(120)", want{kind: ormVarchar, name: "text", length: 120}},
		{DriverSQLite, "BOOLEAN", want{kind: ormBool, name: "boolean"}},
		{DriverSQLite, "DATETIME", want{kind: ormDateTime, name: "datetime"}},
		{DriverSQLite, "DECIMAL(10,2)", want{kind: ormDecimal, name: "numeric", precision: 10, scale: 2}},
		{DriverSQLite, "UNSIGNED BIG INT", want{kind: ormInt64, name: "integer", unsigned: true}},
		{DriverSQLite, "MY_INT_TYPE", want{kind: ormInt32, name: "integer"}},
		{DriverSQLite, "NATIVE CHARACTER(70)", want{kind: ormChar, name: "text", length: 70}},
		{DriverSQLite, "", want{kind: ormBytes, name: "blob"}},
		{DriverSQLite, "WHATEVER", want{kind: ormDecimal, name: "numeric"}},

		{DriverMSSQL, "nvarchar(200)", want{kind: ormVarchar, name: "nvarchar", length: 200}},
		{DriverMSSQL, "nvarchar(MAX)", want{kind: ormVarchar, name: "nvarchar", length: -1}},
		{DriverMSSQL, "varbinary(MAX)", want{kind: ormBytes, name: "varbinary", length: -1}},
		{DriverMSSQL, "bit", want{kind: ormBool, name: "bit"}},
		{DriverMSSQL, "tinyint", want{kind: ormInt8, name: "tinyint", unsigned: true}},
		{DriverMSSQL, "uniqueidentifier", want{kind: ormUUID, name: "uniqueidentifier"}},
		{DriverMSSQL, "datetime2", want{kind: ormDateTime, name: "datetime2"}},
		{DriverMSSQL, "datetimeoffset", want{kind: ormDateTimeTZ, name: "datetimeoffset"}},
		{DriverMSSQL, "decimal(18,2)", want{kind: ormDecimal, name: "decimal", precision: 18, scale: 2}},
		{DriverMSSQL, "money", want{kind: ormMoney, name: "money"}},
		{DriverMSSQL, "timestamp", want{kind: ormBytes, name: "rowversion"}},
		{DriverMSSQL, "jsonb", want{kind: ormUnknown, name: "jsonb"}},

		{DriverOracle, "NUMBER(10,0)", want{kind: ormInt64, name: "number", precision: 10}},
		{DriverOracle, "NUMBER(9,0)", want{kind: ormInt32, name: "number", precision: 9}},
		{DriverOracle, "NUMBER(18,2)", want{kind: ormDecimal, name: "number", precision: 18, scale: 2}},
		{DriverOracle, "NUMBER", want{kind: ormDecimal, name: "number"}},
		{DriverOracle, "VARCHAR2(255)", want{kind: ormVarchar, name: "varchar2", length: 255}},
		{DriverOracle, "CLOB", want{kind: ormText, name: "clob"}},
		{DriverOracle, "RAW(16)", want{kind: ormBytes, name: "raw", length: 16}},
		{DriverOracle, "DATE", want{kind: ormDateTime, name: "date"}},
		{DriverOracle, "TIMESTAMP(6) WITH TIME ZONE", want{kind: ormDateTimeTZ, name: "timestamp with time zone", precision: 6}},
		{DriverOracle, "INTERVAL DAY(2) TO SECOND(6)", want{kind: ormInterval, name: "interval day to second"}},
		{DriverOracle, "BINARY_DOUBLE", want{kind: ormFloat64, name: "binary_double"}},

		{DriverClickHouse, "UInt64", want{kind: ormInt64, name: "uint64", unsigned: true}},
		{DriverClickHouse, "Nullable(String)", want{kind: ormText, name: "string"}},
		{DriverClickHouse, "LowCardinality(Nullable(String))", want{kind: ormText, name: "string"}},
		{DriverClickHouse, "Array(String)", want{kind: ormText, name: "string", array: true}},
		{DriverClickHouse, "Array(Array(Int8))", want{kind: ormJSON, name: "array"}},
		{DriverClickHouse, "Decimal(18, 4)", want{kind: ormDecimal, name: "decimal", precision: 18, scale: 4}},
		{DriverClickHouse, "Decimal64(4)", want{kind: ormDecimal, name: "decimal", precision: 18, scale: 4}},
		{DriverClickHouse, "DateTime64(3, 'UTC')", want{kind: ormDateTimeTZ, name: "datetime64", precision: 3}},
		{DriverClickHouse, "FixedString(16)", want{kind: ormChar, name: "fixedstring", length: 16}},
		{DriverClickHouse, "Map(String, String)", want{kind: ormJSON, name: "map"}},
		{DriverClickHouse, "UInt128", want{kind: ormBigNum, name: "uint128", unsigned: true}},
		{DriverClickHouse, "UUID", want{kind: ormUUID, name: "uuid"}},
	}
	for _, c := range cases {
		got := parseORMType(c.driver, c.raw)
		w := c.want
		if got.Kind != w.kind || got.Name != w.name || got.Length != w.length || got.Precision != w.precision ||
			got.Scale != w.scale || got.Unsigned != w.unsigned || got.Array != w.array {
			t.Errorf("%s %q = kind %d name %q len %d prec %d scale %d unsigned %v array %v, want %+v",
				c.driver, c.raw, got.Kind, got.Name, got.Length, got.Precision, got.Scale, got.Unsigned, got.Array, w)
		}
	}

	for _, c := range []struct {
		driver Driver
		raw    string
		values []string
	}{
		{DriverMySQL, "enum('draft','published','it''s')", []string{"draft", "published", "it's"}},
		{DriverMySQL, "set('a,b','c')", []string{"a,b", "c"}},
		{DriverClickHouse, "Enum8('desktop' = 1, 'mobile' = 2)", []string{"desktop", "mobile"}},
		{DriverClickHouse, "Nullable(Enum16('a=b' = -1))", []string{"a=b"}},
	} {
		got := parseORMType(c.driver, c.raw)
		if strings.Join(got.Values, "|") != strings.Join(c.values, "|") {
			t.Errorf("%s %q values = %q, want %q", c.driver, c.raw, got.Values, c.values)
		}
	}
}

// A default is the text a catalogue prints. What it means depends on the
// engine that printed it and on the column it belongs to.
func TestORMDefaultsAreReadPerEngine(t *testing.T) {
	cases := []struct {
		driver Driver
		flavor string
		typ    string
		raw    string
		kind   ormDefKind
		text   string
		truth  bool
		expr   bool // the catalogue flagged it as an expression (MySQL)
	}{
		{driver: DriverPostgres, typ: "bigint", raw: "nextval('customers_id_seq'::regclass)", kind: ormDefAuto},
		{driver: DriverPostgres, typ: "timestamp with time zone", raw: "now()", kind: ormDefNow},
		{driver: DriverPostgres, typ: "timestamp with time zone", raw: "CURRENT_TIMESTAMP", kind: ormDefNow},
		{driver: DriverPostgres, typ: "timestamp without time zone", raw: "timezone('utc'::text, now())", kind: ormDefExpr, text: "timezone('utc'::text, now())"},
		{driver: DriverPostgres, typ: "date", raw: "CURRENT_DATE", kind: ormDefExpr, text: "CURRENT_DATE"},
		{driver: DriverPostgres, typ: "uuid", raw: "gen_random_uuid()", kind: ormDefUUID, text: "gen_random_uuid()"},
		{driver: DriverPostgres, typ: "text", raw: "'it''s'::text", kind: ormDefString, text: "it's"},
		{driver: DriverPostgres, typ: "character varying(16)", raw: "'N/A'::character varying", kind: ormDefString, text: "N/A"},
		{driver: DriverPostgres, typ: "character(3)", raw: "'EUR'::bpchar", kind: ormDefString, text: "EUR"},
		{driver: DriverPostgres, typ: "numeric(12,2)", raw: "0", kind: ormDefNumber, text: "0"},
		{driver: DriverPostgres, typ: "numeric(12,2)", raw: "'0'::numeric", kind: ormDefNumber, text: "0"},
		{driver: DriverPostgres, typ: "integer", raw: "(-1)", kind: ormDefNumber, text: "-1"},
		{driver: DriverPostgres, typ: "double precision", raw: ".5", kind: ormDefNumber, text: "0.5"},
		{driver: DriverPostgres, typ: "boolean", raw: "false", kind: ormDefBool},
		{driver: DriverPostgres, typ: "boolean", raw: "true", kind: ormDefBool, truth: true},
		{driver: DriverPostgres, typ: "jsonb", raw: "'{}'::jsonb", kind: ormDefJSON, text: "{}"},
		{driver: DriverPostgres, typ: "text[]", raw: "'{}'::text[]", kind: ormDefEmptyArray},
		{driver: DriverPostgres, typ: "text[]", raw: "ARRAY[]::text[]", kind: ormDefEmptyArray},
		{driver: DriverPostgres, typ: "text", raw: "NULL::text", kind: ormDefNone},
		{driver: DriverPostgres, typ: "date", raw: "'2020-01-01'::date", kind: ormDefExpr, text: "'2020-01-01'::date"},
		{driver: DriverPostgres, typ: "text", raw: "('a'::text || 'b'::text)", kind: ormDefExpr, text: "('a'::text || 'b'::text)"},
		// CockroachDB's serial.
		{driver: DriverPostgres, typ: "bigint", raw: "unique_rowid()", kind: ormDefAuto},

		// MariaDB quotes a string default and prints an expression bare.
		{driver: DriverMySQL, flavor: "mariadb", typ: "varchar(20)", raw: "'draft'", kind: ormDefString, text: "draft"},
		{driver: DriverMySQL, flavor: "mariadb", typ: "varchar(20)", raw: "NULL", kind: ormDefNone},
		{driver: DriverMySQL, flavor: "mariadb", typ: "datetime", raw: "current_timestamp()", kind: ormDefNow},
		{driver: DriverMySQL, flavor: "mariadb", typ: "int", raw: "0", kind: ormDefNumber, text: "0"},
		{driver: DriverMySQL, flavor: "mariadb", typ: "varchar(36)", raw: "uuid()", kind: ormDefUUID, text: "(uuid())"},
		// MySQL prints a string default bare and flags an expression elsewhere.
		{driver: DriverMySQL, typ: "enum('draft','published')", raw: "draft", kind: ormDefString, text: "draft"},
		{driver: DriverMySQL, typ: "tinyint(1)", raw: "0", kind: ormDefBool},
		{driver: DriverMySQL, typ: "tinyint(1)", raw: "1", kind: ormDefBool, truth: true},
		{driver: DriverMySQL, typ: "timestamp(3)", raw: "CURRENT_TIMESTAMP(3)", kind: ormDefNow, expr: true},
		{driver: DriverMySQL, typ: "varchar(64)", raw: "concat(_utf8mb4'a',_utf8mb4'b')", kind: ormDefExpr, text: "concat(_utf8mb4'a',_utf8mb4'b')", expr: true},
		{driver: DriverMySQL, typ: "datetime", raw: "2020-01-01 00:00:00", kind: ormDefExpr, text: "'2020-01-01 00:00:00'"},

		{driver: DriverSQLite, typ: "TEXT", raw: "'untitled'", kind: ormDefString, text: "untitled"},
		{driver: DriverSQLite, typ: "BOOLEAN", raw: "0", kind: ormDefBool},
		{driver: DriverSQLite, typ: "DATETIME", raw: "CURRENT_TIMESTAMP", kind: ormDefNow},
		{driver: DriverSQLite, typ: "REAL", raw: "1.5", kind: ormDefNumber, text: "1.5"},
		{driver: DriverSQLite, typ: "TEXT", raw: "(datetime('now'))", kind: ormDefExpr, text: "(datetime('now'))"},
		{driver: DriverSQLite, typ: "TEXT", raw: "NULL", kind: ormDefNone},

		// SQL Server wraps everything in parentheses, numbers twice.
		{driver: DriverMSSQL, typ: "int", raw: "((0))", kind: ormDefNumber, text: "0"},
		{driver: DriverMSSQL, typ: "bit", raw: "((1))", kind: ormDefBool, truth: true},
		{driver: DriverMSSQL, typ: "datetime2", raw: "(sysdatetime())", kind: ormDefNow},
		{driver: DriverMSSQL, typ: "datetime", raw: "(getdate())", kind: ormDefNow},
		{driver: DriverMSSQL, typ: "datetime2", raw: "(getutcdate())", kind: ormDefExpr, text: "(getutcdate())"},
		{driver: DriverMSSQL, typ: "uniqueidentifier", raw: "(newid())", kind: ormDefUUID, text: "newid()"},
		{driver: DriverMSSQL, typ: "nvarchar(20)", raw: "(N'draft')", kind: ormDefString, text: "draft"},
		{driver: DriverMSSQL, typ: "varchar(255)", raw: "('')", kind: ormDefString, text: ""},

		{driver: DriverOracle, typ: "NUMBER(10,0)", raw: `"SHOP"."ISEQ$$_73412".nextval`, kind: ormDefAuto},
		{driver: DriverOracle, typ: "DATE", raw: "SYSDATE", kind: ormDefNow},
		{driver: DriverOracle, typ: "NUMBER(18,2)", raw: "0 ", kind: ormDefNumber, text: "0"},
		{driver: DriverOracle, typ: "CHAR(1)", raw: "'A'", kind: ormDefString, text: "A"},

		{driver: DriverClickHouse, typ: "UUID", raw: "generateUUIDv4()", kind: ormDefUUID, text: "generateUUIDv4()"},
		{driver: DriverClickHouse, typ: "DateTime64(3, 'UTC')", raw: "now64(3)", kind: ormDefNow},
		{driver: DriverClickHouse, typ: "String", raw: "'x'", kind: ormDefString, text: "x"},
	}
	for _, c := range cases {
		col := &ormCol{ORMColumn: &ORMColumn{Name: "c", Type: c.typ, Default: c.raw, DefaultExpr: c.expr}}
		col.t = parseORMType(c.driver, c.typ)
		got := parseORMDefault(c.driver, c.flavor, col)
		if got.Kind != c.kind || got.Bool != c.truth || (c.text != "" && got.Text != c.text) {
			t.Errorf("%s %s DEFAULT %s = kind %d text %q bool %v, want kind %d text %q bool %v",
				c.driver, c.typ, c.raw, got.Kind, got.Text, got.Bool, c.kind, c.text, c.truth)
		}
	}
}

func TestORMNames(t *testing.T) {
	for _, c := range []struct {
		f    func(string) string
		name string
		in   string
		want string
	}{
		{ormCamel, "camel", "created_at", "createdAt"},
		{ormCamel, "camel", "ID", "id"},
		{ormCamel, "camel", "customerID", "customerID"},
		{ormCamel, "camel", "Mixed Case Table", "mixedCaseTable"},
		{ormCamel, "camel", "2fa", "_2fa"},
		{ormSnake, "snake", "orderItems", "order_items"},
		{ormSnake, "snake", "Mixed Case Table", "mixed_case_table"},
		{pascal, "pascal", "user_profiles", "UserProfiles"},
		{pascal, "pascal", "2fa", "_2fa"},
		{singular, "singular", "categories", "category"},
		{singular, "singular", "addresses", "address"},
		{singular, "singular", "boxes", "box"},
		{singular, "singular", "status", "status"},
		{singular, "singular", "analysis", "analysis"},
		{singular, "singular", "series", "series"},
		{singular, "singular", "users", "user"},
		{ormPlural, "plural", "category", "categories"},
		{ormPlural, "plural", "box", "boxes"},
		{ormPlural, "plural", "users", "users"},
		{ormPlural, "plural", "day", "days"},
		{ormPlural, "plural", "status", "statuses"},
		{goName, "go", "customer_id", "CustomerID"},
		{goName, "go", "api_url", "APIURL"},
		{goName, "go", "CUSTOMERS", "Customers"},
		{goName, "go", "weird column", "WeirdColumn"},
		{goName, "go", "2fa", "X2fa"},
		{prismaIdent, "prisma", "_migrations", "migrations"},
		{prismaIdent, "prisma", "2fa", "fa"},
		{prismaIdent, "prisma", "weird column", "weird_column"},
		{rustIdent, "rust", "type", "type_"},
		{rustIdent, "rust", "Mixed Case", "mixed_case"},
		{djangoFieldName, "django", "Id", "id"},
		{djangoFieldName, "django", "class", "class_field"},
		{djangoFieldName, "django", "a__b", "a_b"},
		{djangoFieldName, "django", "_private", "field_private"},
		{djangoFieldName, "django", "trailing_", "trailing_field"},
		{graphqlName, "graphql", "__typename", "_typename"},
		{tsTypeName, "ts type", "Date", "DateRecord"},
		{tsTypeName, "ts type", "Customer", "Customer"},
		{jsIdent, "js", "delete", "delete_"},
	} {
		if got := c.f(c.in); got != c.want {
			t.Errorf("%s(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// Every target either generates for an engine or says why not; none of them
// answers for an engine it has no connector for.
func TestORMEngineSupport(t *testing.T) {
	for _, target := range ORMTargets() {
		spec, _ := ormTargetSpecFor(target)
		for _, driver := range ormEveryEngine {
			reason := ORMUnsupported(target, driver)
			supported := false
			for _, d := range spec.engines {
				supported = supported || d == driver
			}
			if supported && reason != "" {
				t.Errorf("%s lists %s and refuses it: %s", target, driver, reason)
			}
			if !supported && len(reason) < 20 {
				t.Errorf("%s does not support %s and gives no usable reason: %q", target, driver, reason)
			}
			opts, err := ORMRequest{Target: target}.Options()
			if err != nil {
				t.Fatal(err)
			}
			res, err := GenerateORMFiles(ormFixtureFor(map[Driver]string{
				DriverPostgres: "postgres", DriverMySQL: "mysql", DriverSQLite: "sqlite", DriverMSSQL: "sqlserver",
				DriverOracle: "oracle", DriverClickHouse: "clickhouse",
			}[driver]), opts)
			switch {
			case supported && target == ORMPrisma && driver == DriverMySQL:
				// Prisma reads one MySQL database at a time, and says so.
				if err == nil || !strings.Contains(err.Error(), "one database per connection") {
					t.Errorf("Prisma over two MySQL databases: %v", err)
				}
			case supported && err != nil:
				t.Errorf("%s for %s failed: %v", target, driver, err)
			case !supported && (err == nil || !errors.Is(err, ErrORMRequest) || ORMRequestMessage(err) != reason):
				t.Errorf("%s for %s was not refused with its reason: res=%v err=%v", target, driver, res != nil, err)
			}
		}
		for _, driver := range []Driver{DriverMongo, DriverRedis} {
			if reason := ORMUnsupported(target, driver); !strings.Contains(reason, "covers the SQL engines") {
				t.Errorf("%s for %s: %q", target, driver, reason)
			}
		}
	}
	// The refusals that were wrong output before: these used to come out as a
	// PostgreSQL schema.
	for target, drivers := range map[ORMTarget][]Driver{
		ORMPrisma:  {DriverClickHouse, DriverOracle},
		ORMDrizzle: {DriverMSSQL, DriverOracle, DriverClickHouse},
	} {
		for _, d := range drivers {
			if ORMUnsupported(target, d) == "" {
				t.Errorf("%s still answers for %s", target, d)
			}
		}
	}
	if _, err := GenerateORM(ORMPrisma, DriverMongo, nil, nil); err == nil ||
		err.Error() != "ORM schema generation covers the SQL engines; use the connection's native tooling for MongoDB" {
		t.Errorf("the MongoDB refusal changed: %v", err)
	}
}

func TestORMRequestOptions(t *testing.T) {
	// Defaults differ by target: a view is a row type to TypeScript and noise
	// to Prisma.
	for target, views := range map[ORMTarget]bool{
		ORMPrisma: false, ORMDrizzle: false, ORMTypeScript: true, ORMZod: true, ORMKysely: true, ORMSQL: false,
	} {
		o, err := ORMRequest{Target: target}.Options()
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if o.Views != views {
			t.Errorf("%s views default = %v, want %v", target, o.Views, views)
		}
	}
	o, err := ORMRequest{Target: ORMPrisma}.Options()
	if err != nil {
		t.Fatal(err)
	}
	if !o.Relations || !o.Enums || !o.Defaults || o.Naming != ORMNamingPreserve || o.PrismaVersion != "6" {
		t.Errorf("prisma defaults = %+v", o)
	}
	o, err = ORMRequest{Target: ORMGorm, Package: "store", JSONTags: ormNo(), Relations: ormNo()}.Options()
	if err != nil {
		t.Fatal(err)
	}
	if o.Package != "store" || o.JSONTags || o.Relations {
		t.Errorf("gorm options = %+v", o)
	}
	if o, _ := (ORMRequest{Target: ORMGoStructs}).Options(); o.Package != "models" || o.Nulls != "sql" || !o.JSONTags {
		t.Errorf("go defaults = %+v", o)
	}

	for name, c := range map[string]struct {
		req  ORMRequest
		want string
	}{
		"unknown target":        {ORMRequest{Target: "hibernate"}, `unsupported ORM target "hibernate"`},
		"switch of another":     {ORMRequest{Target: ORMTypeScript, Relations: ormYes()}, `option "relations" does not apply to the TypeScript types target`},
		"choice of another":     {ORMRequest{Target: ORMSQL, Naming: "camel"}, `option "naming" does not apply to the SQL target`},
		"choice out of range":   {ORMRequest{Target: ORMGoStructs, Nulls: "maybe"}, "nulls must be one of sql, pointer"},
		"prisma version":        {ORMRequest{Target: ORMPrisma, PrismaVersion: "5"}, "prismaVersion must be one of 6, 7"},
		"package with a space":  {ORMRequest{Target: ORMGorm, Package: "my models"}, "package must be a lower-case Go package name"},
		"package with a quote":  {ORMRequest{Target: ORMGorm, Package: `a"b`}, "package must be a lower-case Go package name"},
		"package that is upper": {ORMRequest{Target: ORMGorm, Package: "Models"}, "package must be a lower-case Go package name"},
	} {
		_, err := c.req.Options()
		if err == nil || !errors.Is(err, ErrORMRequest) || ORMRequestMessage(err) != c.want {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestORMScope(t *testing.T) {
	scope, err := ORMRequest{Schema: " public ", Schemas: []string{"analytics", ""}, Tables: []string{"a", "public.b"}}.
		Scope(DriverPostgres, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(scope.Schemas, ",") != "public,analytics" || strings.Join(scope.Tables, ",") != "a,public.b" {
		t.Errorf("scope = %+v", scope)
	}
	// No schema named: PostgreSQL reads every schema in the database, MySQL
	// and ClickHouse read the connection's own database and nothing else.
	for driver, want := range map[Driver]string{DriverPostgres: "", DriverMSSQL: "", DriverMySQL: "shop", DriverClickHouse: "shop"} {
		scope, err := ORMRequest{}.Scope(driver, "shop")
		if err != nil || strings.Join(scope.Schemas, ",") != want {
			t.Errorf("%s default scope = %+v (%v), want %q", driver, scope, err, want)
		}
	}
	for name, req := range map[string]ORMRequest{
		"control character": {Tables: []string{"a\x00b"}},
		"overlong schema":   {Schema: strings.Repeat("s", 129)},
		"too many schemas":  {Schemas: make65("s")},
	} {
		if _, err := req.Scope(DriverPostgres, ""); err == nil || !errors.Is(err, ErrORMRequest) {
			t.Errorf("%s was accepted: %v", name, err)
		}
	}

	tables := []ORMTable{{Schema: "public", Name: "events"}, {Schema: "analytics", Name: "events"}, {Schema: "public", Name: "a.b"}}
	key := func(tb ORMTable) (string, string) { return tb.Schema, tb.Name }
	for names, want := range map[string]int{"events": 2, "analytics.events": 1, "a.b": 1, "public.a.b": 1} {
		got, err := ormSelect(tables, key, []string{names})
		if err != nil || len(got) != want {
			t.Errorf("select %q = %d tables (%v), want %d", names, len(got), err, want)
		}
	}
	if _, err := ormSelect(tables, key, []string{"events", "missing"}); err == nil || !strings.Contains(err.Error(), `table "missing"`) {
		t.Errorf("a table that is not there was accepted: %v", err)
	}
}

func make65(prefix string) []string {
	out := make([]string, 65)
	for i := range out {
		out[i] = prefix + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	return out
}

// A generation with no model to write is refused with the reason. An empty
// file with a header on it would look like an answer.
func TestORMNothingToGenerate(t *testing.T) {
	onlyViews := &ORMSchema{Driver: DriverPostgres, Tables: []ORMTable{{
		Schema: "public", Name: "v", Kind: ORMKindView, Columns: []ORMColumn{ormColumn("id", "integer")},
	}}}
	noKeys := &ORMSchema{Driver: DriverPostgres, Tables: []ORMTable{{
		Schema: "public", Name: "log", Kind: ORMKindTable, Columns: []ORMColumn{ormColumn("at", "timestamptz")},
	}}}
	for name, c := range map[string]struct {
		schema *ORMSchema
		req    ORMRequest
		want   string
	}{
		"empty":                  {&ORMSchema{Driver: DriverPostgres}, ORMRequest{Target: ORMPrisma}, "there are no tables here to generate from"},
		"only views":             {onlyViews, ORMRequest{Target: ORMPrisma}, "only 1 view(s); turn on views to include them"},
		"only views, no option":  {onlyViews, ORMRequest{Target: ORMDjango}, "only 1 view(s), which this target does not model"},
		"diesel with no key":     {noKeys, ORMRequest{Target: ORMDiesel}, "Diesel needs a primary key on every table it describes"},
		"views asked for are ok": {onlyViews, ORMRequest{Target: ORMPrisma, Views: ormYes()}, ""},
	} {
		opts, err := c.req.Options()
		if err != nil {
			t.Fatal(err)
		}
		_, err = GenerateORMFiles(c.schema, opts)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case c.want != "" && (err == nil || !errors.Is(err, ErrORMRequest) || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}
