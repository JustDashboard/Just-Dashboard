package dbx

// What moving data in and out offers per engine, for the driver catalogue: a
// table as a file, a file into a table, and what a dump can be asked for. The
// dump's own switches are read from DumpCapabilities, which is what the dump
// request is validated against.
func init() {
	dump := func(has func(DumpCapability) bool) CapabilityRule {
		return func(d Driver, _ string) any { return d.Valid() && has(DumpCapabilities(d)) }
	}
	// ClickHouse has no transaction to hold an import in and no key to
	// resolve a conflict on, and nothing here writes its CREATE TABLE.
	transactional := func(d Driver, _ string) any { return d.IsSQL() && d != DriverClickHouse }
	importable := anyOf(sqlEngines, mongoEngines)
	RegisterCapabilities(
		// GET /export. A collection leaves as CSV or JSON with every field;
		// the SQL engines add the other formats, a choice of columns and the
		// result of one statement as a file.
		Capability{"exportFormats", func(d Driver, _ string) any {
			switch {
			case d.IsSQL():
				return listOf(ExportFormats())
			case d == DriverMongo:
				return listOf([]ExportFormat{ExportCSV, ExportJSON})
			}
			return []string{}
		}},
		Capability{"exportColumns", sqlEngines},
		Capability{"exportQuery", sqlEngines},

		// POST /import/upload. Redis has no tables to import into.
		Capability{"import", importable},
		Capability{"importFormats", func(d Driver, flavor string) any {
			if on, _ := importable(d, flavor).(bool); on {
				return listOf(ImportFormats())
			}
			return []string{}
		}},
		Capability{"importMapping", sqlEngines},
		Capability{"importUpsert", transactional},
		Capability{"importReplace", importable},
		Capability{"importCreateTable", transactional},
		// Whether a failed import leaves nothing behind. MongoDB's does only
		// when it replaces the collection, which is swapped in whole.
		Capability{"importAtomic", func(d Driver, flavor string) any {
			if d == DriverMongo {
				return "replace"
			}
			return transactional(d, flavor)
		}},

		Capability{"dumpSchemaOnly", dump(func(c DumpCapability) bool { return c.SchemaOnly })},
		Capability{"dumpDataOnly", dump(func(c DumpCapability) bool { return c.DataOnly })},
		Capability{"dumpTables", dump(func(c DumpCapability) bool { return c.Tables })},
		Capability{"dumpCompression", dump(func(c DumpCapability) bool { return c.Compression })},
		// Redis's choice of numbered databases.
		Capability{"dumpDatabases", dump(func(c DumpCapability) bool { return c.Databases })},
		// A dump made elsewhere can be added to the list.
		Capability{"dumpUpload", everyEngine},
		// A dump restored into a database made for it, and a database copied
		// into a new one, which is that restore of a dump just taken.
		Capability{"restoreNewDatabase", dump(func(c DumpCapability) bool { return c.NewDatabase })},
		Capability{"copy", dump(func(c DumpCapability) bool { return c.NewDatabase })},
		Capability{"copyStructureOnly", dump(func(c DumpCapability) bool { return c.NewDatabase && c.SchemaOnly })},
	)
}
