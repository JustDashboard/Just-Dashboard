package dbx

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestExtensionStatementsQuoteTheNameAndCarryEachOption(t *testing.T) {
	d := postgresDialect{}
	for _, c := range []struct {
		name, schema, version string
		cascade               bool
		want                  string
	}{
		{"pg_trgm", "", "", false, `CREATE EXTENSION IF NOT EXISTS "pg_trgm"`},
		{"uuid-ossp", "extensions", "", false, `CREATE EXTENSION IF NOT EXISTS "uuid-ossp" WITH SCHEMA "extensions"`},
		{"hstore", "", "1.4", false, `CREATE EXTENSION IF NOT EXISTS "hstore" VERSION '1.4'`},
		{"earthdistance", "geo", "1.2", true, `CREATE EXTENSION IF NOT EXISTS "earthdistance" WITH SCHEMA "geo" VERSION '1.2' CASCADE`},
		// A version is a literal: whatever the server lists, it cannot end the statement.
		{"odd", "", "1'; DROP TABLE x; --", false, `CREATE EXTENSION IF NOT EXISTS "odd" VERSION '1''; DROP TABLE x; --'`},
	} {
		got, err := createExtensionSQL(d, c.name, c.schema, c.version, c.cascade)
		if err != nil || got != c.want {
			t.Errorf("create %s = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	if _, err := createExtensionSQL(d, "x\x00", "", "", false); err == nil {
		t.Error("a name with a NUL byte was quoted")
	}

	for version, want := range map[string]string{
		"":    `ALTER EXTENSION "hstore" UPDATE`,
		"1.8": `ALTER EXTENSION "hstore" UPDATE TO '1.8'`,
	} {
		if got, err := updateExtensionSQL(d, "hstore", version); err != nil || got != want {
			t.Errorf("update to %q = %q, %v; want %q", version, got, err, want)
		}
	}
	for cascade, want := range map[bool]string{
		false: `DROP EXTENSION "hstore"`,
		true:  `DROP EXTENSION "hstore" CASCADE`,
	} {
		if got, err := dropExtensionSQL(d, "hstore", cascade); err != nil || got != want {
			t.Errorf("drop cascade=%v = %q, %v; want %q", cascade, got, err, want)
		}
	}
}

func TestExtensionVersionsAreOrderedAsTheirAuthorsMeanThem(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.10", "1.9", 1},
		{"1.9", "1.10", -1},
		{"1.5", "1.5", 0},
		{"1.5.1", "1.5", 1},
		{"3.5.0", "3.4.3", 1},
		{"2.17.2", "2.9.0", 1},
		{"1.0beta1", "1.0", 1},
		{"unpackaged", "1.0", 1},
	} {
		if got := compareExtensionVersions(c.a, c.b); got != c.want {
			t.Errorf("compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestTheCatalogueIsLaidOverWhatTheServerLists(t *testing.T) {
	list := []Extension{
		{Name: "pg_stat_statements", Version: "1.11", AvailableVersion: "1.11", Installed: true, Schema: "public"},
		{Name: "hstore", AvailableVersion: "1.8"},
		{Name: "something_local", AvailableVersion: "0.1", Comment: "the server's own words"},
	}
	byName := func(out []Extension) map[string]Extension {
		m := map[string]Extension{}
		for _, e := range out {
			m[e.Name] = e
		}
		return m
	}

	out := withCatalogue(list, []string{"pg_stat_statements", "auto_explain"},
		map[string]bool{"auto_explain": true, "passwordcheck": true, "pg_partman_bgw": false})
	got := byName(out)
	if e := got["pg_stat_statements"]; e.Category != ExtensionMonitoring || e.Library != "pg_stat_statements" || e.About == "" {
		t.Errorf("a catalogue entry the server lists = %+v", e)
	}
	if e := got["something_local"]; e.Category != "" || e.About != "" || e.Comment != "the server's own words" {
		t.Errorf("an extension the catalogue does not know = %+v", e)
	}
	// A module is a row of its own: loaded, shipped and not loaded, or not in the image.
	if e := got["auto_explain"]; !e.Module || !e.Installed || e.Unavailable {
		t.Errorf("a loaded module = %+v", e)
	}
	if e := got["passwordcheck"]; !e.Module || e.Installed || e.Unavailable {
		t.Errorf("a module the image ships = %+v", e)
	}
	if e := got["pg_partman_bgw"]; !e.Module || e.Installed || !e.Unavailable {
		t.Errorf("a module the image lacks = %+v", e)
	}
	// Somebody else's extension the image lacks is listed as such; one of
	// PostgreSQL's own that a release left out is not.
	if e := got["vector"]; !e.Unavailable || e.Installed || e.Category != ExtensionVectors {
		t.Errorf("pgvector, not shipped = %+v", e)
	}
	if _, listed := got["adminpack"]; listed {
		t.Error("adminpack, which PostgreSQL 17 left out of contrib, was listed as missing from the image")
	}
	// What the database has comes first, then what could be enabled, then
	// what the image lacks.
	rank := map[string]int{}
	for i, e := range out {
		rank[e.Name] = i
	}
	if !(rank["auto_explain"] < rank["hstore"] && rank["pg_stat_statements"] < rank["hstore"] &&
		rank["hstore"] < rank["vector"] && rank["passwordcheck"] < rank["vector"]) {
		t.Errorf("order = %v", extensionNames(out))
	}

	// An account that cannot look in the library directory lists only the
	// modules that are loaded: "not in this image" would be a guess.
	got = byName(withCatalogue(list, []string{"pg_stat_statements"}, nil))
	for _, module := range []string{"auto_explain", "passwordcheck", "pg_partman_bgw"} {
		if _, listed := got[module]; listed {
			t.Errorf("%s was listed when the library directory could not be read", module)
		}
	}
}

func extensionNames(list []Extension) []string {
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = e.Name
	}
	return out
}

func TestTheExtensionCatalogueIsCoherent(t *testing.T) {
	categories := map[string]bool{}
	for _, c := range []string{ExtensionMonitoring, ExtensionSearch, ExtensionGeospatial, ExtensionTimeSeries,
		ExtensionVectors, ExtensionDataTypes, ExtensionSecurity, ExtensionScheduling, ExtensionPartitioning,
		ExtensionForeignData, ExtensionIndexing, ExtensionFunctions, ExtensionLanguages, ExtensionMaintenance} {
		categories[c] = true
	}
	library := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	libraries := map[string]string{}
	for name, info := range extensionCatalogue {
		if !categories[info.Category] {
			t.Errorf("%s: category %q is not one the page groups by", name, info.Category)
		}
		if info.About == "" || !strings.HasSuffix(info.About, ".") || len(info.About) > 110 {
			t.Errorf("%s: about %q is not one short sentence", name, info.About)
		}
		if info.Library != "" {
			if !library.MatchString(info.Library) {
				t.Errorf("%s: library %q is not a bare library name", name, info.Library)
			}
			if other, taken := libraries[info.Library]; taken {
				t.Errorf("%s and %s both load %s", name, other, info.Library)
			}
			libraries[info.Library] = name
		}
		if info.Library == "" && (info.Module || info.Optional || info.First || len(info.Settings) > 0) {
			t.Errorf("%s: says how it is loaded at start and names no library", name)
		}
		for _, s := range info.Settings {
			// A library's own parameters are dotted; a core one is the Parameters page's.
			if !strings.Contains(s.Name, ".") {
				t.Errorf("%s: %s is not a library's own parameter", name, s.Name)
			}
			if s.Database == (s.Value != "") || s.Why == "" {
				t.Errorf("%s: %s has to say its value one way and why", name, s.Name)
			}
		}
	}
	// A library is found by its own name and by the extension it is loaded for.
	for _, name := range []string{"pg_cron", "auto_explain", "pg_stat_statements", "timescaledb"} {
		lib, ok := PreloadLibraryFor(name)
		if !ok || lib.Library != name || lib.Extension != name {
			t.Errorf("PreloadLibraryFor(%s) = %+v, %v", name, lib, ok)
		}
	}
	if lib, ok := PreloadLibraryFor("pg_partman_bgw"); !ok || lib.Extension != "pg_partman_bgw" || !lib.Info.Module {
		t.Errorf("pg_partman_bgw = %+v, %v", lib, ok)
	}
	// Nothing outside the closed list reaches shared_preload_libraries.
	for _, name := range []string{"", "hstore", "/tmp/evil", "pg_cron,evil", "$libdir/pg_cron"} {
		if lib, ok := PreloadLibraryFor(name); ok {
			t.Errorf("PreloadLibraryFor(%q) = %+v", name, lib)
		}
	}
}

func TestThePreloadListKeepsWhatIsThere(t *testing.T) {
	for value, want := range map[string][]string{
		"":                                    {},
		"pg_stat_statements":                  {"pg_stat_statements"},
		"timescaledb, pg_stat_statements":     {"timescaledb", "pg_stat_statements"},
		`"pg_stat_statements" , auto_explain`: {"pg_stat_statements", "auto_explain"},
		`"$libdir/plugins/a,b", c`:            {"$libdir/plugins/a,b", "c"},
		" , ":                                 {},
	} {
		if got := parsePreloadList(value); !reflect.DeepEqual(got, want) {
			t.Errorf("parsePreloadList(%q) = %q, want %q", value, got, want)
		}
	}
	for _, c := range []struct {
		list    []string
		library string
		first   bool
		want    []string
	}{
		{[]string{}, "pg_cron", false, []string{"pg_cron"}},
		{[]string{"timescaledb", "pg_stat_statements"}, "pg_cron", false, []string{"timescaledb", "pg_stat_statements", "pg_cron"}},
		{[]string{"pg_stat_statements"}, "citus", true, []string{"citus", "pg_stat_statements"}},
		{[]string{"pg_cron", "pg_stat_statements"}, "pg_cron", false, []string{"pg_stat_statements", "pg_cron"}},
	} {
		if got := withPreloadLibrary(c.list, c.library, c.first); !reflect.DeepEqual(got, c.want) {
			t.Errorf("withPreloadLibrary(%q, %s) = %q, want %q", c.list, c.library, got, c.want)
		}
	}
	// The list is written as one literal per library, which is what keeps a
	// list from being stored as one library called "a, b".
	got := pgSettingLiteral(Setting{Name: "shared_preload_libraries"}, strings.Join([]string{"timescaledb", "pg_cron"}, ","))
	if got != `'timescaledb', 'pg_cron'` {
		t.Errorf("literal = %s", got)
	}
}

func TestAStartTimeParameterIsSetOnlyWhereNobodyHasSetIt(t *testing.T) {
	cron := extensionCatalogue["pg_cron"].Settings[0]
	stmt, note := preloadSettingStep(cron, "pg_cron", "postgres", "shop", true, 17)
	if stmt != `ALTER SYSTEM SET "cron"."database_name" = 'shop'` || !strings.HasPrefix(note, "cron.database_name is set to shop: ") {
		t.Errorf("unset on 17 = %q, %q", stmt, note)
	}
	// Already what it would be set to: nothing to say.
	if stmt, note := preloadSettingStep(cron, "pg_cron", "shop", "shop", true, 17); stmt != "" || note != "" {
		t.Errorf("already set = %q, %q", stmt, note)
	}
	// Configured by somebody: left alone, and said.
	if stmt, note := preloadSettingStep(cron, "pg_cron", "jobs", "shop", true, 17); stmt != "" ||
		!strings.HasPrefix(note, "cron.database_name stays jobs, as it is configured") {
		t.Errorf("configured = %q, %q", stmt, note)
	}
	// A release that refuses an unknown parameter before the library is loaded.
	if stmt, note := preloadSettingStep(cron, "pg_cron", "postgres", "shop", false, 16); stmt != "" ||
		!strings.HasPrefix(note, "PostgreSQL 16 takes cron.database_name only once pg_cron is loaded, so it stays postgres") {
		t.Errorf("on 16 = %q, %q", stmt, note)
	}
	explain := extensionCatalogue["auto_explain"].Settings[0]
	if stmt, _ := preloadSettingStep(explain, "auto_explain", "-1", "1s", true, 17); stmt != `ALTER SYSTEM SET "auto_explain"."log_min_duration" = '1s'` {
		t.Errorf("auto_explain = %q", stmt)
	}
	worker := extensionCatalogue["pg_partman_bgw"].Settings[0]
	if _, note := preloadSettingStep(worker, "pg_partman_bgw", "", "shop", false, 15); !strings.Contains(note, "so it stays empty") {
		t.Errorf("an unset empty parameter on 15 = %q", note)
	}
}
