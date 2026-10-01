package dbx

import "testing"

// Every flag is declared once and answers for every engine. A flag declared
// twice would be decided by whichever row happened to come last, and one
// missing for an engine would reach the page as `undefined`, which a gate
// reads as "no" for the wrong reason.
func TestCapabilityTableIsCoherent(t *testing.T) {
	seen := map[string]bool{}
	for _, row := range capabilityTable {
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
			for flag := range seen {
				switch caps[flag].(type) {
				case bool, string:
				default:
					t.Errorf("%s/%s: %q is %T, want a boolean or a word", d, flavor, flag, caps[flag])
				}
			}
		}
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
		if caps["settings"] != wantRoles {
			t.Errorf("%s: settings = %v, want %v", d, caps["settings"], wantRoles)
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
		Capability{"streams", On(FlavorRedis).Except(FlavorKeyDB)},
		Capability{"objectWord", func(d Driver, _ string) any {
			if d == DriverMongo {
				return "collections"
			}
			return "tables"
		}},
		// A file that owns a feature overrules the table's placeholder.
		Capability{"locks", On("postgres")},
	)
	if !Capable(DriverRedis, FlavorValkey, "streams") || Capable(DriverRedis, FlavorKeyDB, "streams") {
		t.Error("a registered flag did not follow its rule")
	}
	if got := Capabilities(DriverMongo, "")["objectWord"]; got != "collections" {
		t.Errorf("a registered word = %v", got)
	}
	if !Capable(DriverPostgres, "", "locks") || Capable(DriverMySQL, "", "locks") {
		t.Error("a registered flag did not replace the table's row")
	}
}
