package dbx

// What the table editor and the query runner offer per engine, read from the
// dialects that implement it, for the driver catalogue. An engine with no
// dialect answers false to every one of them.
func init() {
	dialectHas := func(has func(Dialect) bool) CapabilityRule {
		return func(d Driver, _ string) any {
			dialect, err := DialectFor(d)
			return err == nil && has(dialect)
		}
	}
	changeSets := func(d Driver, _ string) any { return ChangesSupported(d) }
	RegisterCapabilities(
		// Staged edits applied as one transaction (POST /changes).
		Capability{"changeSets", changeSets},
		// A table without a primary key is edited by its whole row.
		Capability{"keylessEdits", changeSets},
		// What identifies a row: its primary key, or nothing — ClickHouse's
		// key orders rows without telling two of them apart.
		Capability{"rowIdentity", func(d Driver, _ string) any {
			switch {
			case !d.IsSQL():
				return false
			case ChangesSupported(d):
				return "primaryKey"
			}
			return "none"
		}},
		// Whether an applied change hands the row back: always, or only when
		// the row can be read again by its key.
		Capability{"returnsChangedRow", func(d Driver, _ string) any {
			dialect, err := DialectFor(d)
			switch {
			case err != nil || !ChangesSupported(d):
				return false
			case dialect.SupportsReturning():
				return "always"
			}
			return "byKey"
		}},
		// An existing row's column can be put back to its default.
		Capability{"updateDefault", func(d Driver, _ string) any {
			dialect, err := DialectFor(d)
			if err != nil || !ChangesSupported(d) {
				return false
			}
			defaultless, _ := dialect.(defaultlessUpdater)
			return defaultless == nil || !defaultless.updateCannotSetDefault()
		}},
		// Several statements on one session, and all of them in one
		// transaction where the engine has transactions.
		Capability{"script", sqlEngines},
		Capability{"transactions", func(d Driver, _ string) any { return d.IsSQL() && d != DriverClickHouse }},
		// A named run can be stopped from the editor that started it.
		Capability{"queryCancel", sqlEngines},
		Capability{"dollarQuoting", func(d Driver, _ string) any { return d.IsSQL() && lexRulesFor(d).dollarQuote }},
		Capability{"regexFilter", dialectHas(func(dialect Dialect) bool { _, ok := dialect.(regexMatcher); return ok })},
		Capability{"rowEstimate", dialectHas(func(dialect Dialect) bool { _, ok := dialect.(rowEstimator); return ok })},
		// One whole value of a cell the grid clipped (GET /cell).
		Capability{"cellRead", sqlEngines},
		// How a statement labelled a read is kept from writing: the engine
		// refuses the write, or the write is made and rolled back.
		Capability{"readOnlyScope", func(d Driver, _ string) any {
			dialect, err := DialectFor(d)
			switch {
			case err != nil:
				return false
			case dialect.readScope() == readScopeRollback:
				return "rollback"
			}
			return "enforced"
		}},
	)
}
