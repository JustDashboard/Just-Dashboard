package deploy

import (
	"path"
	"regexp"
	"strings"
)

// persistentStateCodes gives each kind of state its own finding, so a warning
// names what would be lost rather than "something".
var persistentStateCodes = []struct {
	kind, code, title, means string
}{
	{
		PersistentSQLite, "sqlite_ephemeral", "The SQLite database is lost on every deploy",
		"The database file is written inside the container, and each release starts a new container from the image, so every deploy starts with an empty database. While two releases overlap they write two different copies.",
	},
	{
		PersistentStorage, "persistent_path_unmounted", "Data the application writes is lost on every deploy",
		"These directories are written inside the container, and each release starts a new container from the image, so their contents are gone after every deploy.",
	},
	{
		PersistentUploads, "uploads_ephemeral", "Uploaded files are lost on every deploy",
		"Uploads are written inside the container, and each release starts a new container from the image, so every file uploaded before a deploy is gone after it.",
	},
	{
		PersistentVolume, "declared_volume_unmounted", "Data in the image's declared volumes resets on every deploy",
		"Docker gives each new container a fresh anonymous volume for a path its image declares with VOLUME, so the data starts empty after every deploy while the old volumes stay behind on disk.",
	},
	{
		PersistentKeys, "dotnet_data_protection_ephemeral", "ASP.NET Core Data Protection keys reset on every deploy",
		"Sign-in cookies and antiforgery tokens are protected with a key ring kept inside the container, so every release signs everyone out and rejects forms opened before it.",
	},
}

// persistentStateFindings compares the state detection found with the plan:
// a path no writable mount covers, and no variable moves under one, is
// written into a container the next release discards. values are the
// planned variable values; they decide coverage and are never repeated.
func persistentStateFindings(candidate *DetectedCandidate, configuration PlanConfiguration, values map[string]string) []PreflightFinding {
	if candidate == nil || len(candidate.PersistentPaths) == 0 {
		return nil
	}
	uncovered := map[string][]DetectedPersistentPath{}
	kept := []string{}
	for _, entry := range candidate.PersistentPaths {
		if covered, mount := persistentPathCovered(entry, configuration.Runtime.Mounts, values); covered {
			if mount != "" {
				kept = append(kept, mount)
			}
			continue
		}
		uncovered[entry.Kind] = append(uncovered[entry.Kind], entry)
	}
	var findings []PreflightFinding
	for _, kind := range persistentStateCodes {
		entries := uncovered[kind.kind]
		if len(entries) == 0 {
			continue
		}
		measured := make([]string, 0, len(entries))
		for _, entry := range entries {
			measured = append(measured, entry.Path+" — "+entry.Reason)
		}
		findings = append(findings, finding(kind.code, PreflightWarning, kind.title,
			strings.Join(measured, "; "), kind.means, persistentStateAction(entries), "deploy", "runtime.mounts"))
	}
	if kept = uniqueSorted(kept); len(kept) > 0 {
		findings = append(findings, finding("persistent_state_kept", PreflightPass,
			"Application data is kept on a volume between releases", strings.Join(kept, ", "),
			"Every release mounts the same volume, so what the application wrote survives the deploy.", "", "deploy", "runtime.mounts"))
	}
	return findings
}

// persistentStateAction is the one remedy the plan can take, preferring the
// volume detection already proposed.
func persistentStateAction(entries []DetectedPersistentPath) string {
	var actions []string
	seen := map[string]bool{}
	for _, entry := range entries {
		action := ""
		switch {
		case entry.Target != "" && entry.Variable != "" && entry.Value != "":
			action = "Mount a volume at " + entry.Target + " and set " + entry.Variable + " to " + entry.Value
		case entry.Target != "":
			action = "Mount a volume at " + entry.Target
		case entry.DatabaseVariable != "":
			action = "Link a PostgreSQL database through " + entry.DatabaseVariable + ", or keep the file in a directory of its own and mount a volume there"
		case entry.Kind == PersistentKeys:
			action = "Persist the keys with PersistKeysToDbContext or Redis"
		case entry.Path == "/pb_data":
			action = "Start PocketBase with serve --dir=" + compiledRuntimeHome + "/data and mount a volume at " + compiledRuntimeHome + "/data"
		case entry.Kind == PersistentStorage && path.Ext(entry.Path) == "":
			action = "Commit the changes back to the repository, or mount volumes on these directories (the repository's copies then only seed the first release)"
		default:
			action = "Keep the file in a directory of its own, or read its location from a variable, and mount a volume there; or use a server database"
		}
		if !seen[action] {
			seen[action] = true
			actions = append(actions, action)
		}
	}
	return strings.Join(actions, ". ") + ". A volume makes each release stop the old container before starting the new one."
}

// persistentPathCovered reports whether a release keeps the state, and on
// which mount: a driver other than SQLite or a linked server database takes
// the file out of use (no mount), a variable moves it under a writable
// mount, or a writable mount covers where it is written by default.
func persistentPathCovered(entry DetectedPersistentPath, mounts []RuntimeMount, values map[string]string) (bool, string) {
	onMount := func(location string) (bool, string) {
		mount := coveringMount(mounts, location)
		return mount != "", mount
	}
	if entry.ConnectionVariable != "" {
		if driver := strings.TrimSpace(values[entry.ConnectionVariable]); driver != "" && !strings.EqualFold(driver, "sqlite") {
			return true, ""
		}
	}
	if entry.DatabaseVariable != "" {
		if value := strings.TrimSpace(values[entry.DatabaseVariable]); value != "" {
			location, isFile := persistentValueLocation(PersistentSQLite, value)
			if !isFile {
				return true, ""
			}
			if entry.Variable == "" {
				return onMount(location)
			}
		}
	}
	if entry.Variable != "" {
		if value := strings.TrimSpace(values[entry.Variable]); value != "" {
			location, isFile := persistentValueLocation(entry.Kind, value)
			if !isFile {
				return true, ""
			}
			return onMount(location)
		}
	}
	return onMount(entry.Path)
}

var connectionStringFileRE = regexp.MustCompile(`(?i)(?:^|;)\s*(?:data\s?source|filename)\s*=\s*([^;]+)`)

// persistentValueLocation is where a planned value puts the state: an
// absolute container path, "" for a relative one, and isFile false when the
// value names no file at all — a server URL, an in-memory database.
func persistentValueLocation(kind, value string) (string, bool) {
	if kind == PersistentSQLite {
		if match := connectionStringFileRE.FindStringSubmatch(value); match != nil {
			value = strings.TrimSpace(match[1])
		}
		_, file, _, ok := sqliteLocation(value)
		if !ok {
			return "", false
		}
		value = file
	}
	if strings.Contains(value, "://") {
		return "", false
	}
	if !path.IsAbs(value) {
		return "", true
	}
	return path.Clean(value), true
}

// coveringMount is the writable mount whose target holds location.
func coveringMount(mounts []RuntimeMount, location string) string {
	if location == "" {
		return ""
	}
	location = path.Clean(location)
	for _, mount := range mounts {
		target := path.Clean(mount.Target)
		if mount.ReadOnly || mount.Target == "" {
			continue
		}
		if location == target || strings.HasPrefix(location, strings.TrimSuffix(target, "/")+"/") {
			return target
		}
	}
	return ""
}

// schemaPushFinding warns about a schema step that pushes the declared model
// on every start. The first deploy whose model drops or renames a column
// makes the push refuse — or stop for an answer nobody can give — and the
// application never starts.
func schemaPushFinding(candidate *DetectedCandidate, build BuildPlanConfig) *PreflightFinding {
	tool := schemaToolByName(candidate.SchemaTool)
	if tool == nil || tool.Push == "" || tool.Push == tool.Deploy {
		return nil
	}
	pushes := strings.Contains(build.StartCommand, tool.Push) ||
		(candidate.SchemaInStart && candidate.SchemaPush && !strings.Contains(build.StartCommand, tool.Deploy))
	for _, task := range build.ReleaseTasks {
		pushes = pushes || strings.Contains(task.Command, tool.Push)
	}
	if !pushes {
		return nil
	}
	action := "Commit migrations so the start command applies them with " + tool.Deploy + "."
	if generate := schemaGenerateCommand[tool.Name]; generate != "" {
		action = "Commit migrations (" + generate + ") so the start command applies them with " + tool.Deploy + " instead of " + tool.Push + "."
	}
	item := finding("schema_push_unversioned", PreflightWarning,
		"The schema is pushed on every start, with no migrations", tool.Push,
		tool.Label+" changes the database to match the declared model each time the application starts. The first deploy whose model drops or renames a column makes "+tool.Push+" refuse the change, and the application does not start; with candidate-first releases the push reaches the live database before the new release is verified.",
		action, "deploy", "build.startCommand")
	return &item
}

// sqliteOnVolume reports whether the plan keeps a detected SQLite database
// on a volume. A volume mounted for the first time is empty, so the database
// there starts without the schema the image's copy had, the way a newly
// linked server database does.
func sqliteOnVolume(candidate *DetectedCandidate, configuration PlanConfiguration, values map[string]string) bool {
	if candidate == nil {
		return false
	}
	for _, entry := range candidate.PersistentPaths {
		if entry.Kind != PersistentSQLite {
			continue
		}
		if covered, mount := persistentPathCovered(entry, configuration.Runtime.Mounts, values); covered && mount != "" {
			return true
		}
	}
	return false
}

// sqliteSchemaStepFinding is schemaStepFinding for a SQLite database moved
// onto a volume, when no server database is linked: the file there starts
// empty — or, seeded from a committed copy, only the first time — and a tool
// whose schema step nothing runs creates no table.
func sqliteSchemaStepFinding(candidate *DetectedCandidate, build BuildPlanConfig) PreflightFinding {
	// A release task runs on the host or in a one-off container of the
	// image, neither of which mounts the plan's volumes, so the schema it
	// applies never reaches the database on one.
	build.ReleaseTasks = nil
	item := schemaStepFinding(candidate, build)
	if item.Severity == PreflightPass {
		item.Means = "The SQLite database on the volume receives the " + item.Measured + " schema from the start command or the application itself."
		return item
	}
	item.Title = "The SQLite database on the volume will not receive the application's schema"
	item.Means = "The volume starts empty, and " + item.Measured + " creates no table until its schema step runs, so the first request that reads the database fails with \"no such table\". A database file committed to the repository seeds the volume only the first time it is mounted; migrations added later are never applied."
	return item
}

var seedStepRE = regexp.MustCompile(`db[: ]seed|\bseed\b`)

// seedFinding says a new database will have the schema but none of the rows
// the project seeds — often the only administrator account. A database is
// new only on a project's first release: a redeploy's database already holds
// whatever was seeded into it.
func seedFinding(candidate *DetectedCandidate, configuration PlanConfiguration, values map[string]string, firstRelease bool) *PreflightFinding {
	if candidate == nil || candidate.SeedCommand == "" || !firstRelease {
		return nil
	}
	fresh := hasDatabaseDependency(configuration.Dependencies) || sqliteOnVolume(candidate, configuration, values)
	if !fresh || seedStepRE.MatchString(configuration.Build.StartCommand) {
		return nil
	}
	// A release task reaches a linked server, not a volume.
	for _, task := range configuration.Build.ReleaseTasks {
		if hasDatabaseDependency(configuration.Dependencies) && seedStepRE.MatchString(task.Command) {
			return nil
		}
	}
	means := "The project loads its first rows — an administrator account, the lookup data its forms need — with " + candidate.SeedCommand + ", and nothing runs it against a new database."
	if candidate.SeedResets {
		means += " The seed clears tables before inserting, so it must only ever run against an empty database."
	}
	item := finding("seed_available", PreflightWarning,
		"The new database will have the schema but no seed data", candidate.SeedCommand, means,
		"After the first release is live, run "+candidate.SeedCommand+" once from the project's console.",
		"deploy", "build.startCommand")
	return &item
}

// stateFindings is every persistent-state and seed finding for the selected
// candidate. firstRelease is false when a live release's runtime is being
// replaced.
func stateFindings(candidate *DetectedCandidate, configuration PlanConfiguration, values map[string]string, firstRelease bool) []PreflightFinding {
	if candidate == nil || candidate.BuildMethod != configuration.Build.Method {
		return nil
	}
	findings := persistentStateFindings(candidate, configuration, values)
	if seed := seedFinding(candidate, configuration, values, firstRelease); seed != nil {
		findings = append(findings, *seed)
	}
	return findings
}
