package dbx

import (
	"slices"
	"strings"
	"testing"
)

// Every flag is declared once and answers for every engine. A flag declared
// twice — two rows of the table, or a row and a registration from the file of
// one engine — is decided for every engine by whichever came last, and one
// missing for an engine would reach the page as `undefined`, which a gate
// reads as "no" for the wrong reason.
func TestCapabilityTableIsCoherent(t *testing.T) {
	capabilityMu.RLock()
	rows := append(append([]Capability{}, capabilityTable...), capabilityExtra...)
	capabilityMu.RUnlock()
	seen := map[string]bool{}
	for _, row := range rows {
		if row.Flag == "" || row.Has == nil {
			t.Errorf("incomplete capability row %+v", row)
		}
		if seen[row.Flag] {
			t.Errorf("capability %q is declared twice", row.Flag)
		}
		seen[row.Flag] = true
	}
	for _, d := range Drivers() {
		for _, flavor := range Flavors(d) {
			caps := Capabilities(d, flavor)
			if len(caps) != len(seen) {
				t.Errorf("%s/%s reads %d flags, %d are declared", d, flavor, len(caps), len(seen))
			}
			for flag := range seen {
				switch value := caps[flag].(type) {
				case bool, string:
				case []string:
					// A list is never nil: it travels as [] and not as null.
					if value == nil {
						t.Errorf("%s/%s: %q is a nil list", d, flavor, flag)
					}
				default:
					t.Errorf("%s/%s: %q is %T, want a boolean, a word or a list of words", d, flavor, flag, caps[flag])
				}
			}
		}
	}
}

// Every flag that is a yes or a no, and who has it. The table is written out
// by hand, from what each surface's routes do, and not derived from the rules
// it checks: a rule that drifts is caught here, and an engine given a feature
// has to be given it in both places.
//
// A word is a driver (every flavour of it), a flavour, or "sql" for every
// engine with a dialect; "-word" takes one away again.
func TestCapabilitiesPerEngine(t *testing.T) {
	const (
		redis = "redis"
		mongo = "mongodb"
		// MongoDB itself: FerretDB speaks the protocol and lacks the feature.
		mongoProper = "mongodb -ferretdb"
		// What the SQL statistics views serve; CockroachDB and TiDB keep
		// theirs elsewhere.
		sqlStats = "sql -cockroachdb -tidb"
	)
	want := map[string]string{
		// connections and discovery
		"server":           "sql -sqlite mongodb redis",
		"provision":        "postgres -cockroachdb -yugabytedb mysql -percona -tidb clickhouse mongodb -ferretdb redis",
		"inventoryConnect": "sql mongodb redis",
		"hostAccount":      "postgres -cockroachdb -yugabytedb mysql -tidb clickhouse mongodb -ferretdb redis -keydb -dragonfly",
		"fileBased":        "sqlite",
		"openByDefault":    "tidb clickhouse mongodb -ferretdb redis",
		"dump":             "sql mongodb redis",

		// the workbench
		"sql":            "sql",
		"console":        "sql mongodb redis",
		"changeSets":     "sql -clickhouse",
		"keylessEdits":   "sql -clickhouse",
		"updateDefault":  "postgres mysql sqlserver oracle",
		"script":         "sql",
		"transactions":   "sql -clickhouse",
		"queryCancel":    "sql",
		"dollarQuoting":  "postgres",
		"regexFilter":    "postgres mysql oracle clickhouse",
		"rowEstimate":    "sql -sqlite",
		"cellRead":       "sql",
		"explainJSON":    "postgres -cockroachdb mysql -tidb " + mongoProper,
		"explainAnalyze": "postgres -cockroachdb mysql " + mongoProper,

		// the schema
		"ddl":               "sql -clickhouse",
		"schemas":           "sql -sqlite",
		"catalog":           "sql",
		"views":             "sql " + mongoProper,
		"materializedViews": "postgres oracle clickhouse",
		"routines":          "sql -sqlite",
		"triggers":          "sql -clickhouse",
		"sequences":         "postgres mariadb sqlserver oracle",
		"enums":             "postgres",
		"comments":          "sql -sqlite",
		"indexes":           "sql mongodb",
		"extensions":        "postgres -cockroachdb mysql -tidb",

		// watching and maintaining a server
		"stats":           sqlStats + " mongodb redis",
		"sessions":        "sql -sqlite redis " + mongoProper,
		"kill":            "sql -sqlite redis",
		"cancel":          "postgres mysql oracle " + mongoProper,
		"locks":           "postgres -cockroachdb mysql -tidb sqlserver oracle",
		"replication":     "postgres -cockroachdb mysql -tidb redis " + mongoProper,
		"tableStats":      sqlStats,
		"indexStats":      sqlStats,
		"maintenance":     sqlStats,
		"settings":        "sql mongodb redis",
		"settingsWrite":   "postgres -cockroachdb mysql -tidb sqlite sqlserver oracle redis",
		"roles":           "postgres mysql sqlserver clickhouse mongodb redis",
		"privileges":      "postgres -cockroachdb mysql sqlserver clickhouse",
		"statements":      "postgres -cockroachdb mysql -tidb clickhouse sqlserver oracle",
		"statementsReset": "postgres -cockroachdb mysql -tidb",
		"advisor":         "sql",
		"engineAdvisor":   sqlStats,
		"queryLog":        "postgres mysql clickhouse mongodb redis",
		"clickhouseViews": "clickhouse",
		"sqliteFile":      "sqlite",

		// Redis
		"keys": redis, "keyTree": redis, "keyTypeFilter": redis, "keyMeta": redis, "valueDownload": redis,
		"bulkKeys": redis, "streams": redis, "logicalDatabases": redis, "consoleClassify": redis,
		"serverInfo": redis, "commandStats": redis, "latency": redis, "queryLogReset": redis,
		"persistence": redis, "memoryAnalysis": redis, "aclRules": redis, "pubsub": redis,
		"pubsubLive": redis, "monitor": redis,
		"keyEncoding": "redis -dragonfly",
		"aofRewrite":  "redis -dragonfly",

		// MongoDB
		"documents": mongo, "shellSyntax": mongo, "collections": mongo, "aggregation": mongo, "schemaAnalysis": mongo,
		"collectionOptions": mongoProper, "indexUsage": mongoProper, "indexHide": mongoProper,
		"validation": mongoProper, "profiler": mongoProper,

		// code generation and moving data
		"orm":                "sql",
		"export":             "sql mongodb",
		"exportColumns":      "sql",
		"exportQuery":        "sql",
		"import":             "sql mongodb",
		"importMapping":      "sql",
		"importUpsert":       "sql -clickhouse",
		"importReplace":      "sql mongodb",
		"importCreateTable":  "sql -clickhouse",
		"dumpSchemaOnly":     "sql",
		"dumpDataOnly":       "sql",
		"dumpTables":         "sql mongodb",
		"dumpCompression":    "sql mongodb",
		"dumpDatabases":      redis,
		"dumpUpload":         "sql mongodb redis",
		"restoreNewDatabase": "postgres mysql sqlserver mongodb redis",
		"copy":               "postgres mysql sqlserver mongodb redis",
		"copyStructureOnly":  "postgres mysql sqlserver",
	}

	has := func(words string, d Driver, flavor string) bool {
		on := false
		for _, word := range strings.Fields(words) {
			grant := !strings.HasPrefix(word, "-")
			word = strings.TrimPrefix(word, "-")
			if word == string(d) || word == flavor || (word == "sql" && d.IsSQL()) {
				on = grant
			}
		}
		return on
	}
	for _, d := range Drivers() {
		for _, flavor := range Flavors(d) {
			caps := Capabilities(d, flavor)
			for flag, words := range want {
				if got, isBool := caps[flag].(bool); !isBool || got != has(words, d, flavor) {
					t.Errorf("%s/%s: %s = %v, want %v", d, flavor, flag, caps[flag], has(words, d, flavor))
				}
			}
			// A boolean flag nobody wrote down here is one nobody checked.
			for flag, value := range caps {
				if _, isBool := value.(bool); isBool {
					if _, pinned := want[flag]; !pinned && !wordCapabilities[flag] {
						t.Errorf("%s/%s: %s is not in this table", d, flavor, flag)
					}
				}
			}
		}
	}
}

// wordCapabilities are the flags whose value is a word or a list for the
// engines that have the feature, and false for the rest.
var wordCapabilities = map[string]bool{
	"rowIdentity": true, "returnsChangedRow": true, "readOnlyScope": true, "importAtomic": true,
	"json": true, "hashFieldTtl": true, "commandReference": true,
}

// The flags that carry a word or a list say what the code they describe does.
func TestCapabilityWordsAndLists(t *testing.T) {
	word := func(d Driver, flavor, flag string) any { return Capabilities(d, flavor)[flag] }
	for _, c := range []struct {
		d            Driver
		flavor, flag string
		want         any
	}{
		{DriverPostgres, "", "rowIdentity", "primaryKey"},
		{DriverClickHouse, "", "rowIdentity", "none"},
		{DriverRedis, "", "rowIdentity", false},
		{DriverPostgres, "", "returnsChangedRow", "always"},
		{DriverSQLite, "", "returnsChangedRow", "always"},
		{DriverMySQL, FlavorMariaDB, "returnsChangedRow", "byKey"},
		{DriverMSSQL, "", "returnsChangedRow", "byKey"},
		{DriverOracle, "", "returnsChangedRow", "byKey"},
		{DriverClickHouse, "", "returnsChangedRow", false},
		{DriverMSSQL, "", "readOnlyScope", "rollback"},
		{DriverOracle, "", "readOnlyScope", "enforced"},
		{DriverPostgres, "", "readOnlyScope", "enforced"},
		{DriverMongo, "", "readOnlyScope", false},
		{DriverPostgres, "", "importAtomic", true},
		{DriverClickHouse, "", "importAtomic", false},
		{DriverMongo, "", "importAtomic", "replace"},
		{DriverRedis, "", "importAtomic", false},
		{DriverRedis, FlavorRedis, "json", "module"},
		{DriverRedis, FlavorValkey, "json", "module"},
		{DriverRedis, FlavorKeyDB, "json", false},
		{DriverRedis, FlavorDragonfly, "json", true},
		{DriverRedis, FlavorRedis, "hashFieldTtl", "7.4+"},
		{DriverRedis, FlavorValkey, "hashFieldTtl", "9.0+"},
		{DriverRedis, FlavorDragonfly, "hashFieldTtl", false},
		{DriverRedis, FlavorValkey, "commandReference", "docs"},
		{DriverRedis, FlavorKeyDB, "commandReference", "names"},
		{DriverPostgres, "", "commandReference", false},
	} {
		if got := word(c.d, c.flavor, c.flag); got != c.want {
			t.Errorf("%s/%s: %s = %v, want %v", c.d, c.flavor, c.flag, got, c.want)
		}
	}

	list := func(d Driver, flavor, flag string) []string {
		items, _ := Capabilities(d, flavor)[flag].([]string)
		return items
	}
	// The lists are the functions the routes themselves consult.
	for _, d := range Drivers() {
		for _, flavor := range Flavors(d) {
			if got, want := list(d, flavor, "catalogGroups"), CatalogGroups(d, flavor); !slices.Equal(got, want) && len(got)+len(want) > 0 {
				t.Errorf("%s/%s: catalogGroups = %v, the catalogue reads %v", d, flavor, got, want)
			}
			if got, want := list(d, flavor, "ddlOperations"), DDLOperations(d, flavor); !slices.Equal(got, want) {
				t.Errorf("%s/%s: ddlOperations = %v, the forms run %v", d, flavor, got, want)
			}
			for _, target := range ORMTargets() {
				if listed, offered := slices.Contains(list(d, flavor, "ormTargets"), string(target)), ORMUnsupported(target, d) == ""; listed != offered {
					t.Errorf("%s/%s: ormTargets lists %s = %v, the generator is offered = %v", d, flavor, target, listed, offered)
				}
			}
			for _, action := range list(d, flavor, "maintenanceActions") {
				if _, ok := MaintenanceActionFor(d, action); !ok {
					t.Errorf("%s/%s: maintenanceActions lists %q, which the engine does not offer", d, flavor, action)
				}
			}
			if on := Capable(d, flavor, "maintenance"); on != (len(list(d, flavor, "maintenanceActions")) > 0) {
				t.Errorf("%s/%s: maintenance = %v with actions %v", d, flavor, on, list(d, flavor, "maintenanceActions"))
			}
			if on := Capable(d, flavor, "export"); on != (len(list(d, flavor, "exportFormats")) > 0) {
				t.Errorf("%s/%s: export = %v with formats %v", d, flavor, on, list(d, flavor, "exportFormats"))
			}
			if on := Capable(d, flavor, "import"); on != (len(list(d, flavor, "importFormats")) > 0) {
				t.Errorf("%s/%s: import = %v with formats %v", d, flavor, on, list(d, flavor, "importFormats"))
			}
		}
	}
	if got := list(DriverMySQL, FlavorMariaDB, "catalogGroups"); !slices.Contains(got, GroupSequences) {
		t.Errorf("MariaDB has sequences and its catalogue groups are %v", got)
	}
	if got := list(DriverMongo, "", "exportFormats"); !slices.Equal(got, []string{"csv", "json"}) {
		t.Errorf("a collection exports as %v, want csv and json", got)
	}
	if got := list(DriverClickHouse, "", "ormTargets"); slices.Contains(got, "prisma") || !slices.Contains(got, "kysely") {
		t.Errorf("ClickHouse is offered %v: Prisma has no connector for it and Kysely has", got)
	}
}

// The flags are statements about what this package can do, so they are
// checked against the package rather than against a second list: an engine is
// "sql" exactly when it has a dialect, "ddl" exactly when that dialect says
// its generated DDL runs, and it has roles exactly when something lists them.
func TestCapabilitiesFollowTheCode(t *testing.T) {
	for _, d := range Drivers() {
		caps := Capabilities(d, "")
		if caps["sql"] != d.IsSQL() {
			t.Errorf("%s: sql = %v, IsSQL = %v", d, caps["sql"], d.IsSQL())
		}
		dialect, err := DialectFor(d)
		if caps["ddl"] != (err == nil && dialect.SupportsDDL()) {
			t.Errorf("%s: ddl = %v disagrees with the dialect", d, caps["ddl"])
		}
		_, adminErr := AdminFor(d)
		wantRoles := adminErr == nil || d == DriverMongo || d == DriverRedis
		if caps["roles"] != wantRoles {
			t.Errorf("%s: roles = %v, want %v", d, caps["roles"], wantRoles)
		}
		// Every engine's parameters can be read, SQLite's pragmas included.
		if caps["settings"] != true {
			t.Errorf("%s: settings = %v, want true", d, caps["settings"])
		}
		// A change set is what the engine's own check allows, and a plan
		// form is one the dialect renders.
		if caps["changeSets"] != ChangesSupported(d) {
			t.Errorf("%s: changeSets = %v, ChangesSupported = %v", d, caps["changeSets"], ChangesSupported(d))
		}
		if jsonPlan, analyze := ExplainForms(d); d.IsSQL() && (caps["explainJSON"] != jsonPlan || caps["explainAnalyze"] != analyze) {
			t.Errorf("%s: explainJSON/explainAnalyze = %v/%v, the dialect renders %v/%v",
				d, caps["explainJSON"], caps["explainAnalyze"], jsonPlan, analyze)
		}
		if caps["regexFilter"] != slices.Contains(FilterOpsFor(d), "regex") {
			t.Errorf("%s: regexFilter = %v disagrees with the filter operators %v", d, caps["regexFilter"], FilterOpsFor(d))
		}
		for flag, on := range OpsCapabilities(d, "") {
			if caps[flag] != on {
				t.Errorf("%s: %s = %v, the dialect says %v", d, flag, caps[flag], on)
			}
		}
		if caps["dump"] != true {
			t.Errorf("%s: every engine can be dumped, got %v", d, caps["dump"])
		}
	}
	// SQLite is the one engine with no session list, and says so itself.
	if Capable(DriverSQLite, "", "sessions") || !Capable(DriverPostgres, "", "sessions") {
		t.Error("sessions: SQLite has no session list and Postgres has one")
	}
	if Capable(DriverSQLite, "", "server") || !Capable(DriverRedis, "", "server") {
		t.Error("server: a SQLite database is a file, a Redis is a process")
	}
}

// A fork shares its driver's reading except where the table takes a flag
// away, and an unknown flavour reads as the driver's own rather than as
// nothing.
func TestCapabilitiesByFlavor(t *testing.T) {
	if !Capable(DriverMySQL, FlavorMariaDB, "roles") {
		t.Error("MariaDB lost a flag its driver has")
	}
	if !Capable(DriverPostgres, FlavorPostgres, "statements") || Capable(DriverPostgres, FlavorCockroachDB, "statements") {
		t.Error("statements: Postgres keeps pg_stat_statements, CockroachDB has none")
	}
	if Capable(DriverPostgres, FlavorCockroachDB, "provision") || !Capable(DriverRedis, FlavorValkey, "provision") {
		t.Error("provision: no CockroachDB image is offered, a Valkey one is")
	}
	if got, want := Capabilities(DriverRedis, "nonesuch")["roles"], Capabilities(DriverRedis, "")["roles"]; got != want {
		t.Errorf("an unknown flavour read %v, the driver's own reads %v", got, want)
	}
	// A flavour of another driver is not this driver's flavour.
	if Capable(DriverPostgres, FlavorMariaDB, "statements") != Capable(DriverPostgres, "", "statements") {
		t.Error("a foreign flavour changed a driver's reading")
	}
}

func TestRegisteredCapabilitiesJoinTheReading(t *testing.T) {
	capabilityMu.Lock()
	saved := capabilityExtra
	capabilityMu.Unlock()
	t.Cleanup(func() {
		capabilityMu.Lock()
		capabilityExtra = saved
		capabilityMu.Unlock()
	})

	RegisterCapabilities(
		Capability{"testFeature", On(FlavorRedis, FlavorValkey).Except(FlavorKeyDB)},
		Capability{"testWord", func(d Driver, _ string) any {
			if d == DriverMongo {
				return "collections"
			}
			return "tables"
		}},
	)
	if !Capable(DriverRedis, FlavorValkey, "testFeature") || Capable(DriverRedis, FlavorKeyDB, "testFeature") {
		t.Error("a registered flag did not follow its rule")
	}
	if got := Capabilities(DriverMongo, "")["testWord"]; got != "collections" {
		t.Errorf("a registered word = %v", got)
	}
}
