package dbx

// What the MongoDB surface offers, beside the code that serves it, for the
// driver catalogue. Only the flags no other engine has are registered here;
// the ones MongoDB shares with other engines are rows of the table in
// capabilities.go.
func init() {
	RegisterCapabilities(
		Capability{Flag: "documents", Has: mongoEngines},
		Capability{Flag: "shellSyntax", Has: mongoEngines},
		Capability{Flag: "collections", Has: mongoEngines},
		Capability{Flag: "aggregation", Has: mongoEngines},
		Capability{Flag: "schemaAnalysis", Has: mongoEngines},
		// FerretDB speaks the protocol and lacks these.
		Capability{Flag: "collectionOptions", Has: mongoDBProper},
		Capability{Flag: "indexUsage", Has: mongoDBProper},
		Capability{Flag: "indexHide", Has: mongoDBProper},
		Capability{Flag: "validation", Has: mongoDBProper},
		Capability{Flag: "profiler", Has: mongoDBProper},
	)
}
