package dbx

import "sort"

// What the dashboard knows about the common PostgreSQL extensions beyond what
// the server says of them.
//
// pg_available_extensions lists what the server's image ships, each with the
// one-line comment from its control file. The comment is written for somebody
// who already knows the extension ("track planning and execution statistics of
// all SQL statements executed"), it says nothing of what the extension needs
// before it works, and the list says nothing of what the image does not ship.
// So the catalogue below adds three facts: a category to find it by, a sentence
// a reader new to it can act on, and the library the server has to load at
// start before it works at all — the one thing that turns "Enable" from one
// statement into a restart.
//
// The catalogue is a closed list, and it is the only source of a library name
// the preload route writes into shared_preload_libraries: a library is code the
// server runs in every process, and a server told to load one that is not there
// does not start.

// Extension categories, as the page groups and filters by them.
const (
	ExtensionMonitoring   = "Monitoring"
	ExtensionSearch       = "Search"
	ExtensionGeospatial   = "Geospatial"
	ExtensionTimeSeries   = "Time series"
	ExtensionVectors      = "Vectors"
	ExtensionDataTypes    = "Data types"
	ExtensionSecurity     = "Security"
	ExtensionScheduling   = "Scheduling"
	ExtensionPartitioning = "Partitioning"
	ExtensionForeignData  = "Foreign data"
	ExtensionIndexing     = "Indexing"
	ExtensionFunctions    = "Functions"
	ExtensionLanguages    = "Languages"
	ExtensionMaintenance  = "Maintenance"
)

// ExtensionInfo is one catalogue entry.
type ExtensionInfo struct {
	Category string
	About    string
	// Library is the shared library the server must load at start: in
	// shared_preload_libraries, read once when the postmaster starts.
	Library string
	// Optional marks a library the extension works without; loaded at start
	// it adds a background worker or a cache that survives a restart.
	Optional bool
	// First marks a library that must be the first one in the list.
	First bool
	// Module is a library with no extension to create: enabling it is
	// loading it at start, and nothing else.
	Module bool
	// Contrib marks an extension PostgreSQL ships in its own contrib package.
	// One the server lacks was left out of its release (adminpack went in 17),
	// and is not listed as missing from the image.
	Contrib bool
	// Settings are the parameters the library reads at start that loading it
	// is pointless without, with the value enabling it gives each one.
	Settings []PreloadSetting
}

// PreloadSetting is one parameter a library needs set when it is loaded.
type PreloadSetting struct {
	Name string
	// Database sets it to the connection's own database: the one the reader
	// is enabling the extension in.
	Database bool
	// Value is a fixed value, where Database is false.
	Value string
	// Unset is what the parameter reads before anybody configured it.
	Unset string
	// Why says what the value does, for the plan's notes.
	Why string
}

var extensionCatalogue = map[string]ExtensionInfo{
	// PostgreSQL's own.
	"adminpack":          {Category: ExtensionMaintenance, Contrib: true, About: "Functions pgAdmin uses to read and write files on the server."},
	"amcheck":            {Category: ExtensionMaintenance, Contrib: true, About: "Checks tables and B-tree indexes for corruption."},
	"autoinc":            {Category: ExtensionFunctions, Contrib: true, About: "A trigger that fills a column from a sequence."},
	"bloom":              {Category: ExtensionIndexing, Contrib: true, About: "Bloom-filter indexes, for equality checks on any of many columns."},
	"btree_gin":          {Category: ExtensionIndexing, Contrib: true, About: "GIN operator classes for ordinary types, so one GIN index can mix them with arrays or JSON."},
	"btree_gist":         {Category: ExtensionIndexing, Contrib: true, About: "GiST operator classes for ordinary types; exclusion constraints over ranges need it."},
	"citext":             {Category: ExtensionDataTypes, Contrib: true, About: "A text type that compares without regard to case, for email addresses and user names."},
	"cube":               {Category: ExtensionDataTypes, Contrib: true, About: "Points and boxes in many dimensions."},
	"dblink":             {Category: ExtensionForeignData, Contrib: true, About: "Runs a query in another PostgreSQL database from inside this one."},
	"dict_int":           {Category: ExtensionSearch, Contrib: true, About: "A full-text search dictionary for whole numbers."},
	"dict_xsyn":          {Category: ExtensionSearch, Contrib: true, About: "A full-text search dictionary that replaces words with their synonyms."},
	"earthdistance":      {Category: ExtensionGeospatial, Contrib: true, About: "Great-circle distances between points on the Earth, built on cube."},
	"file_fdw":           {Category: ExtensionForeignData, Contrib: true, About: "Reads a file on the server as a table."},
	"fuzzystrmatch":      {Category: ExtensionSearch, Contrib: true, About: "Soundex, Metaphone and Levenshtein distance between strings."},
	"hstore":             {Category: ExtensionDataTypes, Contrib: true, About: "Sets of key–value pairs in one column."},
	"insert_username":    {Category: ExtensionFunctions, Contrib: true, About: "A trigger that records who changed a row."},
	"intagg":             {Category: ExtensionFunctions, Contrib: true, About: "Aggregates integers into arrays and back; superseded by array_agg and unnest."},
	"intarray":           {Category: ExtensionDataTypes, Contrib: true, About: "Functions, operators and indexes for arrays of integers."},
	"isn":                {Category: ExtensionDataTypes, Contrib: true, About: "Types for ISBN, ISMN, ISSN, EAN13 and UPC numbers, with their check digits."},
	"lo":                 {Category: ExtensionDataTypes, Contrib: true, About: "Removes a large object once the row that referenced it is gone."},
	"ltree":              {Category: ExtensionDataTypes, Contrib: true, About: "Paths in a tree, for hierarchies such as categories and org charts."},
	"moddatetime":        {Category: ExtensionFunctions, Contrib: true, About: "A trigger that stamps a column with the time its row last changed."},
	"old_snapshot":       {Category: ExtensionMonitoring, Contrib: true, About: "Shows the snapshot limits old_snapshot_threshold keeps."},
	"pageinspect":        {Category: ExtensionMaintenance, Contrib: true, About: "Reads the raw contents of table and index pages."},
	"pg_buffercache":     {Category: ExtensionMonitoring, Contrib: true, About: "What the shared buffer cache holds right now."},
	"pg_freespacemap":    {Category: ExtensionMonitoring, Contrib: true, About: "The free space PostgreSQL records for each page of a table."},
	"pg_logicalinspect":  {Category: ExtensionMonitoring, Contrib: true, About: "Reads the snapshots logical decoding keeps."},
	"pg_prewarm":         {Category: ExtensionMaintenance, Contrib: true, Library: "pg_prewarm", Optional: true, About: "Loads tables into the buffer cache. Loaded at start, it also refills the cache after a restart."},
	"pg_stat_statements": {Category: ExtensionMonitoring, Contrib: true, Library: "pg_stat_statements", About: "How often each statement runs and what it costs. The Performance page reads it."},
	"pg_surgery":         {Category: ExtensionMaintenance, Contrib: true, About: "Repairs a damaged table one row at a time. A last resort."},
	"pg_trgm":            {Category: ExtensionSearch, Contrib: true, About: "Trigram similarity between strings, and indexes that speed up LIKE and ILIKE."},
	"pg_visibility":      {Category: ExtensionMaintenance, Contrib: true, About: "Reads and checks a table's visibility map."},
	"pg_walinspect":      {Category: ExtensionMonitoring, Contrib: true, About: "Reads the write-ahead log from SQL."},
	"pgcrypto":           {Category: ExtensionSecurity, Contrib: true, About: "Hashing, encryption and random bytes in SQL."},
	"pgrowlocks":         {Category: ExtensionMonitoring, Contrib: true, About: "Which rows of a table are locked, and by whom."},
	"pgstattuple":        {Category: ExtensionMonitoring, Contrib: true, About: "Dead rows and free space inside a table or an index."},
	"plperl":             {Category: ExtensionLanguages, Contrib: true, About: "Functions written in Perl, without access to the server's files."},
	"plperlu":            {Category: ExtensionLanguages, Contrib: true, About: "Functions written in Perl, with the server's files and network. Superusers only."},
	"plpgsql":            {Category: ExtensionLanguages, Contrib: true, About: "PostgreSQL's own procedural language. Installed in every database."},
	"plpython3u":         {Category: ExtensionLanguages, Contrib: true, About: "Functions written in Python, with the server's files and network. Superusers only."},
	"pltcl":              {Category: ExtensionLanguages, Contrib: true, About: "Functions written in Tcl, without access to the server's files."},
	"pltclu":             {Category: ExtensionLanguages, Contrib: true, About: "Functions written in Tcl, with the server's files and network. Superusers only."},
	"postgres_fdw":       {Category: ExtensionForeignData, Contrib: true, About: "Queries the tables of another PostgreSQL server as if they were here."},
	"refint":             {Category: ExtensionFunctions, Contrib: true, About: "Triggers that check references between tables; foreign keys do this better."},
	"seg":                {Category: ExtensionDataTypes, Contrib: true, About: "Line segments and floating-point intervals, such as a measurement and its error."},
	"sslinfo":            {Category: ExtensionSecurity, Contrib: true, About: "The certificate the current client connected with."},
	"tablefunc":          {Category: ExtensionFunctions, Contrib: true, About: "crosstab and other functions that return tables."},
	"tcn":                {Category: ExtensionFunctions, Contrib: true, About: "A trigger that notifies listeners when a table's rows change."},
	"tsm_system_rows":    {Category: ExtensionFunctions, Contrib: true, About: "A TABLESAMPLE method that takes a given number of rows."},
	"tsm_system_time":    {Category: ExtensionFunctions, Contrib: true, About: "A TABLESAMPLE method that reads for a given time."},
	"unaccent":           {Category: ExtensionSearch, Contrib: true, About: "A full-text search dictionary that removes accents."},
	"uuid-ossp":          {Category: ExtensionDataTypes, Contrib: true, About: "Functions that generate UUIDs; gen_random_uuid() is built in since 13."},
	"xml2":               {Category: ExtensionDataTypes, Contrib: true, About: "XPath queries and XSLT; deprecated in favour of the built-in XML functions."},
	"auto_explain": {Category: ExtensionMonitoring, Contrib: true, Module: true, Library: "auto_explain",
		About: "Writes the plan of every statement slower than a threshold to the server log.",
		Settings: []PreloadSetting{{Name: "auto_explain.log_min_duration", Value: "1s", Unset: "-1",
			Why: "the plan of every statement slower than that is written to the server log"}}},
	"passwordcheck": {Category: ExtensionSecurity, Contrib: true, Module: true, Library: "passwordcheck",
		About: "Refuses short and easily guessed passwords set with CREATE ROLE or ALTER ROLE."},

	// Somebody else's, shipped by some images and not others.
	"vector":           {Category: ExtensionVectors, About: "A vector type with nearest-neighbour indexes (HNSW and IVFFlat), for embeddings."},
	"vectorscale":      {Category: ExtensionVectors, About: "A disk-based approximate nearest-neighbour index for pgvector's vectors."},
	"postgis":          {Category: ExtensionGeospatial, About: "Geometry and geography types, spatial indexes and hundreds of spatial functions."},
	"postgis_raster":   {Category: ExtensionGeospatial, About: "Raster data, such as elevation maps and imagery, for PostGIS."},
	"postgis_topology": {Category: ExtensionGeospatial, About: "Topological types and functions for PostGIS."},
	"postgis_sfcgal":   {Category: ExtensionGeospatial, About: "3D geometry operations for PostGIS."},
	"address_standardizer": {Category: ExtensionGeospatial,
		About: "Parses a postal address into its parts."},
	"postgis_tiger_geocoder": {Category: ExtensionGeospatial,
		About: "Geocodes US addresses against the TIGER census data."},
	"pgrouting":   {Category: ExtensionGeospatial, About: "Shortest paths and routing over graphs, on top of PostGIS."},
	"h3":          {Category: ExtensionGeospatial, About: "Uber's H3 hexagonal grid."},
	"timescaledb": {Category: ExtensionTimeSeries, Library: "timescaledb", About: "Hypertables partitioned by time, with compression and continuous aggregates."},
	"timescaledb_toolkit": {Category: ExtensionTimeSeries,
		About: "Hyperfunctions for time series: percentiles, time-weighted averages, downsampling."},
	"pg_cron": {Category: ExtensionScheduling, Library: "pg_cron",
		About: "Runs SQL on a cron schedule, from inside the database.",
		Settings: []PreloadSetting{{Name: "cron.database_name", Database: true, Unset: "postgres",
			Why: "pg_cron keeps its jobs in that database and can only be created there"}}},
	"pg_partman": {Category: ExtensionPartitioning, About: "Creates and drops time or number partitions ahead of the data, on a schedule."},
	"pg_partman_bgw": {Category: ExtensionPartitioning, Module: true, Library: "pg_partman_bgw",
		About: "pg_partman's background worker: runs its partition maintenance without pg_cron.",
		Settings: []PreloadSetting{{Name: "pg_partman_bgw.dbname", Database: true, Unset: "",
			Why: "the worker maintains the partitions of that database"}}},
	"citus":          {Category: ExtensionPartitioning, Library: "citus", First: true, About: "Distributes tables and queries across several PostgreSQL servers."},
	"pgaudit":        {Category: ExtensionSecurity, Library: "pgaudit", About: "Writes an audit trail of sessions and statements to the server log."},
	"pgsodium":       {Category: ExtensionSecurity, About: "Encryption with libsodium; Supabase's Vault is built on it."},
	"supabase_vault": {Category: ExtensionSecurity, About: "Secrets stored encrypted in a table."},
	"pg_jsonschema":  {Category: ExtensionFunctions, About: "Validates JSON against a JSON Schema, in a CHECK constraint."},
	"pg_graphql":     {Category: ExtensionFunctions, About: "A GraphQL API answered from the database's own schema."},
	"pg_net":         {Category: ExtensionFunctions, Library: "pg_net", About: "Sends HTTP requests from SQL, without waiting for the answer."},
	"http":           {Category: ExtensionFunctions, About: "Sends HTTP requests from SQL and waits for the answer."},
	"pgjwt":          {Category: ExtensionSecurity, About: "Signs and verifies JSON Web Tokens in SQL."},
	"pg_uuidv7":      {Category: ExtensionDataTypes, About: "Time-ordered UUIDs (version 7); built in from PostgreSQL 18."},
	"hypopg":         {Category: ExtensionIndexing, About: "Hypothetical indexes: whether the planner would use one, before it is built."},
	"pg_repack":      {Category: ExtensionMaintenance, About: "Rebuilds a bloated table or index without a long lock, with its command-line client."},
	"pg_squeeze":     {Category: ExtensionMaintenance, Library: "pg_squeeze", About: "Rebuilds bloated tables in the background, on a schedule."},
	"pg_hint_plan":   {Category: ExtensionMonitoring, Library: "pg_hint_plan", Optional: true, About: "Planner hints in comments, to force a join order or an index."},
	"pg_qualstats":   {Category: ExtensionMonitoring, Library: "pg_qualstats", About: "Which predicates queries filter on, for choosing indexes."},
	"pg_stat_kcache": {Category: ExtensionMonitoring, Library: "pg_stat_kcache", About: "CPU and disk use per statement, beside pg_stat_statements."},
	"pg_wait_sampling": {Category: ExtensionMonitoring, Library: "pg_wait_sampling",
		About: "Samples what each session waits on, over time."},
	"orafce":    {Category: ExtensionFunctions, About: "Oracle's functions and packages, for porting an Oracle schema."},
	"age":       {Category: ExtensionDataTypes, About: "Graph queries in openCypher over tables."},
	"plv8":      {Category: ExtensionLanguages, About: "Functions written in JavaScript."},
	"rum":       {Category: ExtensionSearch, About: "An index for full-text search that ranks and orders by distance."},
	"pglogical": {Category: ExtensionMaintenance, Library: "pglogical", About: "Logical replication between servers, from before it was built in."},
}

// ExtensionCatalogue returns what the catalogue knows about an extension.
func ExtensionCatalogue(name string) (ExtensionInfo, bool) {
	info, ok := extensionCatalogue[name]
	return info, ok
}

// PreloadLibrary is one library the preload route may write, and what it is.
type PreloadLibrary struct {
	Library string
	// Extension is the catalogue entry it belongs to: the extension it is
	// loaded for, or the module that is nothing but it.
	Extension string
	Info      ExtensionInfo
}

// PreloadLibraryFor looks a library up in the catalogue, by its own name or
// by the extension it is loaded for. It is the only way a name reaches
// shared_preload_libraries from a request.
func PreloadLibraryFor(name string) (PreloadLibrary, bool) {
	if info, ok := extensionCatalogue[name]; ok && info.Library != "" {
		return PreloadLibrary{Library: info.Library, Extension: name, Info: info}, true
	}
	for _, ext := range catalogueNames() {
		if info := extensionCatalogue[ext]; info.Library != "" && info.Library == name {
			return PreloadLibrary{Library: name, Extension: ext, Info: info}, true
		}
	}
	return PreloadLibrary{}, false
}

// catalogueNames is every entry, in a fixed order.
func catalogueNames() []string {
	names := make([]string, 0, len(extensionCatalogue))
	for name := range extensionCatalogue {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// preloadLibraries is every library the catalogue names, in a fixed order.
func preloadLibraries() []string {
	out := []string{}
	for _, name := range catalogueNames() {
		if lib := extensionCatalogue[name].Library; lib != "" {
			out = append(out, lib)
		}
	}
	return out
}
