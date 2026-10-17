package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// PostgreSQL's extensions: creating one with the options CREATE EXTENSION
// takes, updating one to a newer version, and dropping one with what depends
// on it — each planned before it runs, so what the dialog shows is what the
// route runs.
//
// The page used to run CREATE EXTENSION IF NOT EXISTS with the name and
// nothing else, and DROP EXTENSION with no CASCADE: an extension that needed
// another was refused with the engine's words, one with something built on it
// could not be dropped from here at all, and a newer version showed as a tag
// there was no way to act on. Every option here is checked against the
// server's own catalogue before a statement is written — a version it ships,
// a schema that exists, the schema the control file insists on — so a refusal
// names what to choose instead of quoting a syntax error.

// ExtensionManager is the optional half of Admin for an engine whose
// extensions are created, updated and dropped from a session. MySQL's plugins
// are its server's configuration, and nothing else here has extensions.
type ExtensionManager interface {
	ExtensionDetail(ctx context.Context, db *sql.DB, name string) (*ExtensionDetail, error)
	PlanCreateExtension(ctx context.Context, db *sql.DB, spec ExtensionSpec) (*DDLPlan, error)
	PlanUpdateExtension(ctx context.Context, db *sql.DB, name, version string) (*DDLPlan, error)
	PlanDropExtension(ctx context.Context, db *sql.DB, name string, cascade bool) (*DDLPlan, error)
}

// ExtensionManagerFor returns the engine's extension manager, or false where
// its extensions cannot be changed from a connection.
func ExtensionManagerFor(driver Driver) (ExtensionManager, bool) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, false
	}
	m, ok := d.(ExtensionManager)
	return m, ok
}

// ExtensionSpec is what a create asks for. An empty Schema and Version leave
// the choice to the server: the control file's schema or the first of the
// search path, and the default version.
type ExtensionSpec struct {
	Name    string
	Schema  string
	Version string
	// Cascade creates the extensions it requires that are not there yet.
	Cascade bool
}

// ExtensionVersion is one version of an extension the server ships.
type ExtensionVersion struct {
	Version string `json:"version"`
	// Superuser is true when creating it needs a superuser, unless Trusted.
	Superuser bool `json:"superuser"`
	// Trusted lets a role with CREATE on the database create it anyway.
	Trusted     bool `json:"trusted"`
	Relocatable bool `json:"relocatable"`
	// Schema is the schema its control file fixes, where it fixes one.
	Schema   string   `json:"schema,omitempty"`
	Requires []string `json:"requires"`
	// Missing are the extensions it requires that this database lacks:
	// CASCADE creates them first.
	Missing []string `json:"missing"`
}

// ExtensionDependent is an object outside an extension that would go with it.
type ExtensionDependent struct {
	// Kind is the engine's own word for it: table column, view, index.
	Kind string `json:"kind"`
	// Object names it as the engine does in its own refusal.
	Object string `json:"object"`
	// Via is what of the extension's it uses, where it uses it directly.
	Via string `json:"via,omitempty"`
}

// ExtensionMember counts what an extension added, by kind.
type ExtensionMember struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// ExtensionDetail is everything a dialog needs before it creates, updates or
// drops an extension.
type ExtensionDetail struct {
	Extension
	// Versions are the versions the server ships, newest first.
	Versions []ExtensionVersion `json:"versions"`
	// Updates are the versions the installed one has an update path to,
	// newest first.
	Updates []string `json:"updates"`
	// Schemas are the database's schemas this account may create in, and
	// DefaultSchema the one a create that names none lands in.
	Schemas       []string `json:"schemas"`
	DefaultSchema string   `json:"defaultSchema"`
	// Members is what it added; Dependents what outside it is built on it,
	// up to dependentsShown of DependentsTotal.
	Members         []ExtensionMember    `json:"members"`
	Dependents      []ExtensionDependent `json:"dependents"`
	DependentsTotal int                  `json:"dependentsTotal"`
	// Drop and DropCascade are the two statements a drop can run.
	Drop        string `json:"drop,omitempty"`
	DropCascade string `json:"dropCascade,omitempty"`
	// Superuser is whether the connection signs in as one, which decides
	// whether an extension marked SuperuserOnly can be created from it.
	Superuser bool `json:"superuser"`
}

// dependentsShown bounds the dependents a detail lists. The engine's own
// refusal names a hundred and then counts the rest.
const dependentsShown = 100

// --- PostgreSQL --------------------------------------------------------------

func (d postgresDialect) Extensions(ctx context.Context, db *sql.DB) ([]Extension, error) {
	list, err := pgExtensionRows(ctx, db, "")
	if err != nil {
		return nil, err
	}
	loaded, err := pgLoadedLibraries(ctx, db)
	if err != nil {
		return nil, err
	}
	return withCatalogue(list, loaded, pgLibrariesPresent(ctx, db, preloadLibraries())), nil
}

// pgExtensionRows reads pg_available_extensions joined to what is installed,
// for every extension or for one.
func pgExtensionRows(ctx context.Context, db *sql.DB, name string) ([]Extension, error) {
	// trusted arrived in 13; before it every extension that needed a
	// superuser needed one.
	trusted := "false"
	if pgServerVersion(ctx, db) >= 130000 {
		trusted = "COALESCE(v.trusted, false)"
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT a.name, COALESCE(e.extversion, ''), COALESCE(a.default_version, ''),
	         e.oid IS NOT NULL, COALESCE(n.nspname, ''), COALESCE(a.comment, ''),
	         COALESCE(v.superuser, true), `+trusted+`
	  FROM pg_available_extensions a
	  LEFT JOIN pg_extension e ON e.extname = a.name
	  LEFT JOIN pg_namespace n ON n.oid = e.extnamespace
	  LEFT JOIN pg_available_extension_versions v ON v.name = a.name AND v.version = a.default_version
	  WHERE $1 = '' OR a.name = $1
	  ORDER BY (e.oid IS NULL), a.name`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Extension{}
	for rows.Next() {
		var e Extension
		var superuser, trusted bool
		if err := rows.Scan(&e.Name, &e.Version, &e.AvailableVersion, &e.Installed, &e.Schema, &e.Comment,
			&superuser, &trusted); err != nil {
			return nil, err
		}
		e.SuperuserOnly = superuser && !trusted
		out = append(out, e)
	}
	return out, rows.Err()
}

// withCatalogue lays the catalogue over what the server listed: a category,
// a sentence and the library each needs, a row for each module (a library
// with no extension) and one for each extension the image does not ship.
//
// present is which libraries the server's library directory holds, or nil
// where this account cannot look; a module is then listed only once it is
// loaded, because "not in this image" would be a guess.
func withCatalogue(list []Extension, loaded []string, present map[string]bool) []Extension {
	listed := map[string]bool{}
	out := make([]Extension, 0, len(list)+len(extensionCatalogue))
	for _, e := range list {
		listed[e.Name] = true
		out = append(out, annotated(e))
	}
	for _, name := range catalogueNames() {
		info := extensionCatalogue[name]
		if listed[name] {
			continue
		}
		switch {
		case info.Module && info.Library != "":
			on := containsString(loaded, info.Library)
			if !on && present == nil {
				continue
			}
			out = append(out, annotated(Extension{Name: name, Installed: on, Module: true,
				Unavailable: !on && !present[info.Library]}))
		case !info.Module && !info.Contrib:
			out = append(out, annotated(Extension{Name: name, Unavailable: true}))
		}
	}
	// What the database has, then what could be enabled, then what the image
	// lacks; by name within each.
	rank := func(e Extension) int {
		switch {
		case e.Installed:
			return 0
		case e.Unavailable:
			return 2
		}
		return 1
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := rank(out[i]), rank(out[j]); ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func annotated(e Extension) Extension {
	if info, ok := extensionCatalogue[e.Name]; ok {
		e.Category, e.About, e.Library = info.Category, info.About, info.Library
	}
	return e
}

// pgServerVersion is server_version_num, or 0 where it cannot be read.
func pgServerVersion(ctx context.Context, db *sql.DB) int {
	var n int
	_ = db.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&n)
	return n
}

// pgExtensionName is the extension as the server spells it, from what it
// ships: a name is looked up, never taken from the request into a statement.
func pgExtensionName(ctx context.Context, db *sql.DB, name string) (Extension, error) {
	list, err := pgExtensionRows(ctx, db, strings.TrimSpace(name))
	if err != nil {
		return Extension{}, planRead("the server's extensions", err)
	}
	if len(list) == 0 {
		return Extension{}, fmt.Errorf("%q is not an extension this server ships", strings.TrimSpace(name))
	}
	return annotated(list[0]), nil
}

func pgExtensionVersions(ctx context.Context, db *sql.DB, name string) ([]ExtensionVersion, error) {
	trusted := "false"
	if pgServerVersion(ctx, db) >= 130000 {
		trusted = "trusted"
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT version, superuser, `+trusted+`, relocatable, COALESCE(schema::text, ''),
	         COALESCE(array_to_string(requires, chr(31)), ''),
	         COALESCE((SELECT string_agg(r, chr(31) ORDER BY r) FROM unnest(requires::text[]) r
	                   WHERE NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = r)), '')
	  FROM pg_available_extension_versions WHERE name = $1`, name)
	if err != nil {
		return nil, planRead("the extension's versions", err)
	}
	defer rows.Close()
	out := []ExtensionVersion{}
	for rows.Next() {
		var v ExtensionVersion
		var requires, missing string
		if err := rows.Scan(&v.Version, &v.Superuser, &v.Trusted, &v.Relocatable, &v.Schema, &requires, &missing); err != nil {
			return nil, planRead("the extension's versions", err)
		}
		v.Requires, v.Missing = splitUnit(requires), splitUnit(missing)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, planRead("the extension's versions", err)
	}
	sort.Slice(out, func(i, j int) bool { return compareExtensionVersions(out[i].Version, out[j].Version) > 0 })
	return out, nil
}

// pgExtensionUpdates are the versions an installed extension can be updated
// to. pg_extension_update_paths works out every path between every pair of
// versions, which is a moment's work on an extension with a long history; it
// is asked only by the dialogs, never by the list.
func pgExtensionUpdates(ctx context.Context, db *sql.DB, name, from string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT target FROM pg_extension_update_paths($1) WHERE source = $2 AND path IS NOT NULL`, name, from)
	if err != nil {
		return nil, planRead("the extension's update paths", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			return nil, planRead("the extension's update paths", err)
		}
		out = append(out, target)
	}
	if err := rows.Err(); err != nil {
		return nil, planRead("the extension's update paths", err)
	}
	sort.Slice(out, func(i, j int) bool { return compareExtensionVersions(out[i], out[j]) > 0 })
	return out, nil
}

func (d postgresDialect) PlanCreateExtension(ctx context.Context, db *sql.DB, spec ExtensionSpec) (*DDLPlan, error) {
	ext, err := pgExtensionName(ctx, db, spec.Name)
	if err != nil {
		return nil, err
	}
	if ext.Installed {
		return nil, fmt.Errorf("%s is already enabled in this database, at version %s", ext.Name, ext.Version)
	}
	versions, err := pgExtensionVersions(ctx, db, ext.Name)
	if err != nil {
		return nil, err
	}
	version := strings.TrimSpace(spec.Version)
	chosen, ok := findExtensionVersion(versions, version, ext.AvailableVersion)
	if !ok {
		return nil, fmt.Errorf("%s has no version %s on this server; it ships %s", ext.Name, version, versionWords(versions))
	}
	schema := strings.TrimSpace(spec.Schema)
	if schema != "" {
		if chosen.Schema != "" && chosen.Schema != schema {
			return nil, fmt.Errorf("%s is always created in the schema %s", ext.Name, chosen.Schema)
		}
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema).Scan(&exists); err != nil {
			return nil, planRead("the database's schemas", err)
		}
		if !exists {
			return nil, fmt.Errorf("there is no schema %s in this database", schema)
		}
	}
	stmt, err := createExtensionSQL(d, ext.Name, schema, version, spec.Cascade)
	if err != nil {
		return nil, err
	}
	return planOf(stmt), nil
}

// findExtensionVersion is the version a create would make: the one named,
// or the server's default where none is.
func findExtensionVersion(versions []ExtensionVersion, version, fallback string) (ExtensionVersion, bool) {
	want := version
	if want == "" {
		want = fallback
	}
	for _, v := range versions {
		if v.Version == want {
			return v, true
		}
	}
	// A default the versions view does not list is the server's to resolve.
	return ExtensionVersion{}, version == ""
}

func versionWords(versions []ExtensionVersion) string {
	words := make([]string, len(versions))
	for i, v := range versions {
		words[i] = v.Version
	}
	return strings.Join(words, ", ")
}

// createExtensionSQL writes CREATE EXTENSION. The version is a literal: it
// is one the server listed, and the statement takes no bind marker.
func createExtensionSQL(d postgresDialect, name, schema, version string, cascade bool) (string, error) {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return "", err
	}
	stmt := "CREATE EXTENSION IF NOT EXISTS " + q
	if schema != "" {
		s, err := d.QuoteIdent(schema)
		if err != nil {
			return "", err
		}
		stmt += " WITH SCHEMA " + s
	}
	if version != "" {
		stmt += " VERSION " + dumpString(DriverPostgres, version)
	}
	if cascade {
		stmt += " CASCADE"
	}
	return stmt, nil
}

func (d postgresDialect) PlanUpdateExtension(ctx context.Context, db *sql.DB, name, version string) (*DDLPlan, error) {
	ext, err := pgExtensionName(ctx, db, name)
	if err != nil {
		return nil, err
	}
	if !ext.Installed {
		return nil, fmt.Errorf("%s is not enabled in this database", ext.Name)
	}
	target := strings.TrimSpace(version)
	if target == "" {
		target = ext.AvailableVersion
	}
	if target == ext.Version {
		return nil, fmt.Errorf("%s is already at version %s", ext.Name, ext.Version)
	}
	updates, err := pgExtensionUpdates(ctx, db, ext.Name, ext.Version)
	if err != nil {
		return nil, err
	}
	if !containsString(updates, target) {
		if len(updates) == 0 {
			return nil, fmt.Errorf("the server ships no update for %s from version %s", ext.Name, ext.Version)
		}
		return nil, fmt.Errorf("the server ships no update for %s from %s to %s; it can be updated to %s",
			ext.Name, ext.Version, target, strings.Join(updates, ", "))
	}
	stmt, err := updateExtensionSQL(d, ext.Name, strings.TrimSpace(version))
	if err != nil {
		return nil, err
	}
	return planOf(stmt), nil
}

// updateExtensionSQL writes ALTER EXTENSION … UPDATE: to the version named,
// or with none to the server's default.
func updateExtensionSQL(d postgresDialect, name, version string) (string, error) {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return "", err
	}
	stmt := "ALTER EXTENSION " + q + " UPDATE"
	if version != "" {
		stmt += " TO " + dumpString(DriverPostgres, version)
	}
	return stmt, nil
}

func (d postgresDialect) PlanDropExtension(ctx context.Context, db *sql.DB, name string, cascade bool) (*DDLPlan, error) {
	ext, err := pgExtensionName(ctx, db, name)
	if err != nil {
		return nil, err
	}
	if !ext.Installed {
		return nil, fmt.Errorf("%s is not enabled in this database", ext.Name)
	}
	stmt, err := dropExtensionSQL(d, ext.Name, cascade)
	if err != nil {
		return nil, err
	}
	return planOf(stmt), nil
}

func dropExtensionSQL(d postgresDialect, name string, cascade bool) (string, error) {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return "", err
	}
	stmt := "DROP EXTENSION " + q
	if cascade {
		stmt += " CASCADE"
	}
	return stmt, nil
}

func (d postgresDialect) ExtensionDetail(ctx context.Context, db *sql.DB, name string) (*ExtensionDetail, error) {
	ext, err := pgExtensionName(ctx, db, name)
	if err != nil {
		return nil, err
	}
	out := &ExtensionDetail{Extension: ext, Updates: []string{}, Schemas: []string{},
		Members: []ExtensionMember{}, Dependents: []ExtensionDependent{}}
	if out.Versions, err = pgExtensionVersions(ctx, db, ext.Name); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT nspname FROM pg_namespace
	  WHERE nspname !~ '^pg_' AND nspname <> 'information_schema' AND has_schema_privilege(oid, 'CREATE')
	  ORDER BY nspname`)
	if err != nil {
		return nil, planRead("the database's schemas", err)
	}
	if out.Schemas, err = scanStrings(rows); err != nil {
		return nil, planRead("the database's schemas", err)
	}
	var current sql.NullString
	if err := db.QueryRowContext(ctx, `
	  SELECT current_schema(), COALESCE((SELECT rolsuper FROM pg_roles WHERE rolname = current_user), false)`).
		Scan(&current, &out.Superuser); err != nil {
		return nil, planRead("the search path", err)
	}
	out.DefaultSchema = current.String
	if !ext.Installed {
		return out, nil
	}
	if out.Updates, err = pgExtensionUpdates(ctx, db, ext.Name, ext.Version); err != nil {
		return nil, err
	}
	if out.Members, err = pgExtensionMembers(ctx, db, ext.Name); err != nil {
		return nil, err
	}
	if out.Dependents, out.DependentsTotal, err = pgExtensionDependents(ctx, db, ext.Name); err != nil {
		return nil, err
	}
	if out.Drop, err = dropExtensionSQL(d, ext.Name, false); err != nil {
		return nil, err
	}
	out.DropCascade, err = dropExtensionSQL(d, ext.Name, true)
	return out, err
}

func scanStrings(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func pgExtensionMembers(ctx context.Context, db *sql.DB, name string) ([]ExtensionMember, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT (pg_identify_object(d.classid, d.objid, 0)).type, count(*)
	  FROM pg_depend d JOIN pg_extension e ON e.oid = d.refobjid
	  WHERE d.refclassid = 'pg_extension'::regclass AND d.deptype = 'e' AND e.extname = $1
	  GROUP BY 1 ORDER BY 2 DESC, 1`, name)
	if err != nil {
		return nil, planRead("what the extension added", err)
	}
	defer rows.Close()
	out := []ExtensionMember{}
	for rows.Next() {
		var m ExtensionMember
		if err := rows.Scan(&m.Kind, &m.Count); err != nil {
			return nil, planRead("what the extension added", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// pgExtensionDependentsSQL finds what DROP EXTENSION … CASCADE would take
// beyond the extension itself, named the way the engine's own refusal names
// it.
//
// The engine's list is not asked for by trying the drop: a rolled-back DROP
// still takes an exclusive lock on everything it would remove and fires the
// database's event triggers, and this is a read. So it is pg_depend, walked
// the way the engine walks it:
//
//   - part is what goes with the extension by itself: its members ('e'), and
//     whatever is internal to or automatically dropped with one of them
//     ('i', 'a') — a type's array type, a domain's constraint.
//   - dep is every object that depends on a part normally ('n'), which is
//     what a plain DROP refuses over, and then what depends on those, to the
//     depth a real schema reaches. A column is matched as itself: what uses
//     a column goes with it, what uses only its table does not.
//   - named is each of those as the object a reader knows: a member of
//     another extension is that extension, and an internal part — a view's
//     rewrite rule — is its owner.
//   - gone is everything that is dropped, and one is what is left once
//     everything carried by something else on the list is taken away: an
//     extension's members, a domain's constraints, a view's columns.
const pgExtensionDependentsSQL = `
WITH RECURSIVE ext AS (
  SELECT oid FROM pg_extension WHERE extname = $1
),
part(classid, objid) AS (
  SELECT 'pg_extension'::regclass::oid, oid FROM ext
  UNION
  SELECT d.classid, d.objid FROM pg_depend d JOIN part p ON d.refclassid = p.classid AND d.refobjid = p.objid
  WHERE d.deptype IN ('e', 'i', 'a')
),
dep(classid, objid, objsubid, refclassid, refobjid, refobjsubid, depth) AS (
  SELECT d.classid, d.objid, d.objsubid, d.refclassid, d.refobjid, d.refobjsubid, 1
  FROM pg_depend d JOIN part p ON d.refclassid = p.classid AND d.refobjid = p.objid
  WHERE d.deptype = 'n'
    AND NOT EXISTS (SELECT 1 FROM part q WHERE q.classid = d.classid AND q.objid = d.objid)
  UNION
  SELECT d.classid, d.objid, d.objsubid, d.refclassid, d.refobjid, d.refobjsubid, x.depth + 1
  FROM pg_depend d JOIN dep x ON d.refclassid = x.classid AND d.refobjid = x.objid
    AND (x.objsubid = 0 OR d.refobjsubid = x.objsubid)
  WHERE d.deptype = 'n' AND x.depth < 8
),
named AS (
  SELECT COALESCE(e.refclassid, i.refclassid, x.classid) AS classid,
         COALESCE(e.refobjid, i.refobjid, x.objid) AS objid,
         CASE WHEN e.refobjid IS NOT NULL THEN 0
              WHEN i.refobjid IS NOT NULL THEN i.refobjsubid
              ELSE x.objsubid END AS objsubid,
         e.refobjid IS NULL AND i.refobjid IS NULL AS direct,
         x.refclassid, x.refobjid, x.refobjsubid, x.depth
  FROM dep x
  LEFT JOIN pg_depend e ON e.classid = x.classid AND e.objid = x.objid AND e.deptype = 'e'
  LEFT JOIN pg_depend i ON i.classid = x.classid AND i.objid = x.objid AND i.deptype = 'i'
),
gone(classid, objid) AS (
  SELECT classid, objid FROM named
  UNION
  SELECT d.classid, d.objid FROM pg_depend d JOIN gone g ON d.refclassid = g.classid AND d.refobjid = g.objid
  WHERE d.deptype IN ('e', 'i', 'a')
),
one AS (
  SELECT DISTINCT ON (classid, objid, objsubid) classid, objid, objsubid, refclassid, refobjid, refobjsubid, depth
  FROM named n
  WHERE NOT (n.objsubid <> 0 AND EXISTS (
          SELECT 1 FROM named w WHERE w.classid = n.classid AND w.objid = n.objid AND w.objsubid = 0))
    AND NOT EXISTS (
          SELECT 1 FROM pg_depend c JOIN gone g ON c.refclassid = g.classid AND c.refobjid = g.objid
          WHERE c.classid = n.classid AND c.objid = n.objid AND c.deptype IN ('e', 'i', 'a')
            AND NOT (c.refclassid = n.classid AND c.refobjid = n.objid))
  ORDER BY classid, objid, objsubid, depth, direct DESC
)
SELECT COALESCE((pg_identify_object(classid, objid, objsubid)).type, ''),
       COALESCE(pg_describe_object(classid, objid, objsubid), ''),
       COALESCE(pg_describe_object(refclassid, refobjid, refobjsubid), ''),
       count(*) OVER ()
FROM one
ORDER BY depth, 2
LIMIT $2`

func pgExtensionDependents(ctx context.Context, db *sql.DB, name string) ([]ExtensionDependent, int, error) {
	rows, err := db.QueryContext(ctx, pgExtensionDependentsSQL, name, dependentsShown)
	if err != nil {
		return nil, 0, planRead("what depends on the extension", err)
	}
	defer rows.Close()
	out := []ExtensionDependent{}
	total := 0
	for rows.Next() {
		var dep ExtensionDependent
		if err := rows.Scan(&dep.Kind, &dep.Object, &dep.Via, &total); err != nil {
			return nil, 0, planRead("what depends on the extension", err)
		}
		out = append(out, dep)
	}
	return out, total, rows.Err()
}

// compareExtensionVersions orders two version strings the way their authors
// mean them: by each run of digits as a number, so 1.10 is after 1.9, and
// by the text between them otherwise.
func compareExtensionVersions(a, b string) int {
	for a != "" && b != "" {
		ca, ra := versionChunk(a)
		cb, rb := versionChunk(b)
		na, errA := strconv.Atoi(ca)
		nb, errB := strconv.Atoi(cb)
		switch {
		case errA == nil && errB == nil && na != nb:
			if na < nb {
				return -1
			}
			return 1
		case (errA != nil || errB != nil) && ca != cb:
			return strings.Compare(ca, cb)
		}
		a, b = ra, rb
	}
	return strings.Compare(a, b)
}

// versionChunk splits a version into its leading run of digits or of
// anything else, and the rest.
func versionChunk(s string) (string, string) {
	digit := s[0] >= '0' && s[0] <= '9'
	i := 1
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') == digit {
		i++
	}
	return s[:i], s[i:]
}
