package deploy

import (
	"context"
	"strings"
	"testing"
)

func TestPersistentStateFindingsFollowTheMountsAndVariables(t *testing.T) {
	t.Parallel()
	prisma := DetectedPersistentPath{
		Kind: PersistentSQLite, Path: "/app/prisma/dev.db", Target: "/data", Variable: "DATABASE_URL", Value: "file:/data/dev.db",
		Source: "prisma/schema.prisma", Reason: "Prisma's SQLite datasource is read from DATABASE_URL",
	}
	laravel := DetectedPersistentPath{
		Kind: PersistentSQLite, Path: "/app/database/database.sqlite", Target: "/app/storage", Variable: "DB_DATABASE",
		Value: "/app/storage/database.sqlite", DatabaseVariable: "DB_URL", ConnectionVariable: "DB_CONNECTION",
		Source: "composer.json", Reason: "Laravel's database is SQLite",
	}
	django := DetectedPersistentPath{
		Kind: PersistentSQLite, Path: "/app/db.sqlite3", DatabaseVariable: "DATABASE_URL", Source: "mysite/settings.py", Reason: "Django falls back to SQLite",
	}
	keys := DetectedPersistentPath{Kind: PersistentKeys, Path: dotnetDataProtectionAt, Target: dotnetDataProtectionAt, Source: "Program.cs", Reason: "key ring"}
	volume := DetectedPersistentPath{Kind: PersistentVolume, Path: "/app/data", Target: "/app/data", Source: "Dockerfile", Reason: "VOLUME /app/data"}
	mount := func(target string, readOnly bool) RuntimeMount {
		return RuntimeMount{Source: "app-data", Target: target, ReadOnly: readOnly, Ownership: OwnershipManaged}
	}
	for _, fixture := range []struct {
		name   string
		entry  DetectedPersistentPath
		mounts []RuntimeMount
		values map[string]string
		code   string
		kept   bool
	}{
		{"no mount, no value", prisma, nil, nil, "sqlite_ephemeral", false},
		{"the planned volume and value", prisma, []RuntimeMount{mount("/data", false)}, map[string]string{"DATABASE_URL": "file:/data/dev.db"}, "", true},
		{"a volume but the value left in the image", prisma, []RuntimeMount{mount("/data", false)}, map[string]string{"DATABASE_URL": "file:./dev.db"}, "sqlite_ephemeral", false},
		{"a read-only volume keeps nothing", prisma, []RuntimeMount{mount("/data", true)}, map[string]string{"DATABASE_URL": "file:/data/dev.db"}, "sqlite_ephemeral", false},
		{"the value without a volume", prisma, nil, map[string]string{"DATABASE_URL": "file:/data/dev.db"}, "sqlite_ephemeral", false},
		{"a server database takes the file out of use", prisma, nil, map[string]string{"DATABASE_URL": "postgres://db-1:5432/app"}, "", false},
		{"a database reference takes the file out of use", django, nil, map[string]string{"DATABASE_URL": "${{database.3.url}}"}, "", false},
		{"laravel linked to mysql", laravel, nil, map[string]string{"DB_URL": "mysql://db-2/app"}, "", false},
		{"laravel moved into storage", laravel, []RuntimeMount{mount("/app/storage", false)}, map[string]string{"DB_DATABASE": "/app/storage/database.sqlite"}, "", true},
		{"laravel switched to mysql by its connection", laravel, nil, map[string]string{"DB_CONNECTION": "mysql", "DB_HOST": "db-2.jd.internal", "DB_DATABASE": "laravel"}, "", false},
		// The driver is compared without case; "Sqlite" also keeps the leak
		// check below from matching the word in the finding's own text.
		{"laravel's sqlite connection named explicitly", laravel, nil, map[string]string{"DB_CONNECTION": "Sqlite", "DB_DATABASE": "laravel"}, "sqlite_ephemeral", false},
		{"laravel's default path is not under storage", laravel, []RuntimeMount{mount("/app/storage", false)}, nil, "sqlite_ephemeral", false},
		{"django's fallback file with no database", django, nil, nil, "sqlite_ephemeral", false},
		{"django pointed at a sqlite file on a volume", django, []RuntimeMount{mount("/data", false)}, map[string]string{"DATABASE_URL": "sqlite:////data/db.sqlite3"}, "", true},
		{"keys without their volume", keys, nil, nil, "dotnet_data_protection_ephemeral", false},
		{"keys on their volume", keys, []RuntimeMount{mount(dotnetDataProtectionAt, false)}, nil, "", true},
		{"a declared volume a parent mount covers", volume, []RuntimeMount{mount("/app", false)}, nil, "", true},
		{"a declared volume left anonymous", volume, nil, nil, "declared_volume_unmounted", false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			candidate := &DetectedCandidate{PersistentPaths: []DetectedPersistentPath{fixture.entry}}
			configuration := PlanConfiguration{Runtime: RuntimePlanConfig{Mounts: fixture.mounts}}
			findings := persistentStateFindings(candidate, configuration, fixture.values)
			if fixture.code == "" && findingSeverity(findings, "sqlite_ephemeral")+findingSeverity(findings, "dotnet_data_protection_ephemeral")+findingSeverity(findings, "declared_volume_unmounted") != "" {
				t.Fatalf("covered state still warned: %+v", findings)
			}
			if fixture.code != "" && findingSeverity(findings, fixture.code) != PreflightWarning {
				t.Fatalf("expected %s: %+v", fixture.code, findings)
			}
			if (findingSeverity(findings, "persistent_state_kept") == PreflightPass) != fixture.kept {
				t.Fatalf("kept pass = %+v", findings)
			}
			for _, item := range findings {
				for _, value := range fixture.values {
					if strings.Contains(item.Measured+item.Means+item.Action, value) && value != fixture.entry.Value {
						t.Fatalf("a planned value leaked into a finding: %+v", item)
					}
				}
			}
		})
	}
}

func TestPersistentStateActionsNameTheRemedy(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		entry DetectedPersistentPath
		want  string
	}{
		{DetectedPersistentPath{Kind: PersistentSQLite, Target: "/data", Variable: "DATABASE_URL", Value: "file:/data/app.db"}, "Mount a volume at /data and set DATABASE_URL to file:/data/app.db"},
		{DetectedPersistentPath{Kind: PersistentSQLite, DatabaseVariable: "DATABASE_URL"}, "Link a PostgreSQL database through DATABASE_URL"},
		{DetectedPersistentPath{Kind: PersistentKeys}, "PersistKeysToDbContext"},
		{DetectedPersistentPath{Kind: PersistentSQLite, Path: "/pb_data"}, "serve --dir=/home/app/data"},
		{DetectedPersistentPath{Kind: PersistentSQLite, Path: "/app/app.db"}, "Keep the file in a directory of its own"},
	} {
		if action := persistentStateAction([]DetectedPersistentPath{fixture.entry}); !strings.Contains(action, fixture.want) || !strings.Contains(action, "stop the old container") {
			t.Errorf("action for %+v = %q", fixture.entry, action)
		}
	}
}

func TestPreflightReportsStateSeedAndPushBeforeDeploy(t *testing.T) {
	draft := completePlanningDraftModel()
	candidate := newDetectedCandidate("", BuildRecipe, DetectedCandidate{
		Name: "app", Profile: ProfileWeb, Confidence: ConfidenceHigh, Recipe: "node", Framework: "nextjs",
		StartCommand: "npm run start", SchemaTool: "prisma", SchemaInStart: true, SchemaPush: true,
		SeedCommand: "npx prisma db seed", SeedResets: true,
		PersistentPaths: []DetectedPersistentPath{{
			Kind: PersistentSQLite, Path: "/app/prisma/dev.db", Target: "/data", Variable: "DATABASE_URL", Value: "file:/data/dev.db",
			Source: "prisma/schema.prisma", Reason: "Prisma's SQLite datasource is read from DATABASE_URL",
		}},
		Evidence: []DetectionEvidence{{Path: "prisma/schema.prisma", Reason: "Prisma schema"}}, NeedsDecision: []string{},
	})
	draft.Data.Detection.Candidates = []DetectedCandidate{candidate}
	draft.Data.Detection.SelectedID = candidate.ID
	draft.Data.Configuration.Build = BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "npm run start"}
	observation := HostObservation{
		Facilities: map[string]FacilityObservation{"docker": {Available: true}, "buildx": {Available: true}},
		Paths:      []PathObservation{}, Ports: []PortObservation{}, Domains: []DomainObservation{},
		OS: "linux", Architecture: "amd64",
	}
	result, err := PreflightDraft(context.Background(), draft, &preflightObserverFake{observation: observation}, true)
	if err != nil {
		t.Fatal(err)
	}
	if findingSeverity(result.Findings, "sqlite_ephemeral") != PreflightWarning ||
		findingSeverity(result.Findings, "schema_push_unversioned") != PreflightWarning ||
		findingSeverity(result.Findings, "seed_available") != "" {
		t.Fatalf("unplanned volume findings = %#v", result.Findings)
	}

	// The plan the form proposes: the volume, the variable, stop-first.
	draft.Data.Configuration.Runtime.Strategy = StrategyStopFirst
	draft.Data.Configuration.Runtime.Mounts = []RuntimeMount{{Source: "app-1a2b3c4d-data", Target: "/data", Ownership: OwnershipManaged}}
	draft.environment = map[string]string{"DATABASE_URL": "file:/data/dev.db"}
	result, err = PreflightDraft(context.Background(), draft, &preflightObserverFake{observation: observation}, true)
	if err != nil {
		t.Fatal(err)
	}
	if findingSeverity(result.Findings, "sqlite_ephemeral") != "" || findingSeverity(result.Findings, "persistent_state_kept") != PreflightPass ||
		findingSeverity(result.Findings, "backup_policy_missing") != PreflightWarning {
		t.Fatalf("planned volume findings = %#v", result.Findings)
	}
	var seed PreflightFinding
	for _, item := range result.Findings {
		if item.Code == "seed_available" {
			seed = item
		}
	}
	if seed.Severity != PreflightWarning || !strings.Contains(seed.Action, "npx prisma db seed") || !strings.Contains(seed.Means, "empty database") {
		t.Fatalf("seed finding on a fresh volume = %+v", seed)
	}
	// A redeploy replaces a live runtime, whose database is not new.
	redeploy := &preflightObserverFake{observation: observation}
	redeploy.observation.ReplacesRuntime = true
	if again, err := PreflightDraft(context.Background(), draft, redeploy, true); err != nil || findingSeverity(again.Findings, "seed_available") != "" {
		t.Fatalf("redeploy seed findings = %#v, %v", again, err)
	}
	for _, item := range result.Findings {
		if strings.Contains(item.Measured+item.Means+item.Action+item.Title, "file:/data/dev.db") && item.Code != "sqlite_ephemeral" {
			t.Fatalf("a variable value reached a finding: %+v", item)
		}
	}

	// The operator seeds from the start command and moves to migrations.
	draft.Data.Configuration.Build.StartCommand = "npx prisma migrate deploy && npx prisma db seed && npm run start"
	result, err = PreflightDraft(context.Background(), draft, &preflightObserverFake{observation: observation}, true)
	if err != nil {
		t.Fatal(err)
	}
	if findingSeverity(result.Findings, "seed_available") != "" || findingSeverity(result.Findings, "schema_push_unversioned") != "" {
		t.Fatalf("configured seed and migrations findings = %#v", result.Findings)
	}
}

func TestSeedFindingNeedsAFreshDatabase(t *testing.T) {
	t.Parallel()
	candidate := &DetectedCandidate{SeedCommand: "php artisan db:seed --force"}
	if seedFinding(candidate, PlanConfiguration{}, nil, true) != nil {
		t.Fatal("a seed was offered with no database in the plan")
	}
	linked := PlanConfiguration{Dependencies: []PlannedDependency{{Kind: "database", ResourceKind: "database_connection", ResourceID: "4"}}}
	if item := seedFinding(candidate, linked, nil, true); item == nil || item.Severity != PreflightWarning || strings.Contains(item.Means, "clears tables") {
		t.Fatalf("linked database seed finding = %+v", item)
	}
	// A redeploy's database already holds whatever was seeded into it.
	if seedFinding(candidate, linked, nil, false) != nil {
		t.Fatal("a redeploy was told its database is new")
	}
	linked.Build.ReleaseTasks = []ReleaseTaskConfig{{Name: "seed", Command: "php artisan db:seed --force"}}
	if seedFinding(candidate, linked, nil, true) != nil {
		t.Fatal("a release task that seeds still warned")
	}
	// SQLite moved onto a volume by its variable starts empty too.
	onVolume := &DetectedCandidate{SeedCommand: "npx prisma db seed", PersistentPaths: []DetectedPersistentPath{{
		Kind: PersistentSQLite, Path: "/app/prisma/dev.db", Target: "/data", Variable: "DATABASE_URL", Value: "file:/data/dev.db",
		Source: "prisma/schema.prisma", Reason: "Prisma's SQLite datasource is read from DATABASE_URL",
	}}}
	mounted := PlanConfiguration{Runtime: RuntimePlanConfig{Mounts: []RuntimeMount{{Source: "notes-data", Target: "/data", Ownership: OwnershipManaged}}}}
	if seedFinding(onVolume, mounted, map[string]string{"DATABASE_URL": "file:/data/dev.db"}, true) == nil {
		t.Fatal("sqlite relocated onto a new volume was not offered its seed")
	}
	if seedFinding(onVolume, mounted, map[string]string{"DATABASE_URL": "postgres://db-4.jd.internal/app"}, true) != nil {
		t.Fatal("a server database in place of the file was treated as the file")
	}
}

func TestSQLiteOnANewVolumeNeedsItsSchemaStep(t *testing.T) {
	t.Parallel()
	candidate := &DetectedCandidate{SchemaTool: "ef-core", PersistentPaths: []DetectedPersistentPath{{
		Kind: PersistentSQLite, Path: "/app/app.db", Target: "/app/data", Variable: "ConnectionStrings__DefaultConnection",
		Value: "Data Source=/app/data/app.db", Source: "appsettings.json", Reason: "the DefaultConnection connection string opens the SQLite file app.db",
	}}}
	plan := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"},
		Runtime: RuntimePlanConfig{Mounts: []RuntimeMount{{Source: "shop-data", Target: "/app/data", Ownership: OwnershipManaged}}},
	}
	values := map[string]string{"ConnectionStrings__DefaultConnection": "Data Source=/app/data/app.db"}
	if !sqliteOnVolume(candidate, plan, values) {
		t.Fatal("the relocated database was not seen on its volume")
	}
	item := sqliteSchemaStepFinding(candidate, plan.Build)
	if item.Code != "schema_step_missing" || item.Severity != PreflightWarning ||
		!strings.Contains(item.Title, "SQLite database on the volume") || !strings.Contains(item.Action, "Database.Migrate()") {
		t.Fatalf("finding = %+v", item)
	}
	candidate.SchemaInStart = true
	if item := sqliteSchemaStepFinding(candidate, plan.Build); item.Severity != PreflightPass || !strings.Contains(item.Means, "on the volume") {
		t.Fatalf("applied finding = %+v", item)
	}
	if sqliteOnVolume(candidate, PlanConfiguration{}, values) {
		t.Fatal("a plan without the volume keeps the database on one")
	}
}

func TestApplicationOutputNamesARefusedPushAndAnUnwritableDatabase(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct{ line, code string }{
		{"Error: Use the --accept-data-loss flag to ignore the data loss warnings like prisma migrate reset", "schema_push_refused"},
		{"⚠️ We found changes that cannot be executed:", "schema_push_refused"},
		{"Is display_name column in users table created or renamed from another column?", "schema_push_refused"},
		{"Error: Interactive prompts require a TTY terminal (process.stdin.isTTY or process.stdout.isTTY is false)", "schema_push_refused"},
		{"SqliteError: attempt to write a readonly database", "sqlite_not_writable"},
		{"sqlite3.OperationalError: unable to open database file", "sqlite_not_writable"},
		{`relation "users" does not exist`, "schema_missing"},
	} {
		cause := applicationOutputCause([]ContainerDiagnostics{{Lines: []RuntimeLogLine{{Text: "starting"}, {Text: fixture.line}}}})
		if cause == nil || cause.Code != fixture.code || cause.sentence() == "" {
			t.Errorf("%q: cause = %+v", fixture.line, cause)
		}
	}
}

func TestTheHostObservationSaysWhenALiveRuntimeIsReplaced(t *testing.T) {
	t.Parallel()
	observer := NewHostPreflightObserver(nil, t.TempDir(), nil)
	first, err := observer.Observe(context.Background(), ObservationRequest{})
	if err != nil || first.ReplacesRuntime {
		t.Fatalf("first release observation = %+v, %v", first.ReplacesRuntime, err)
	}
	again, err := observer.Observe(context.Background(), ObservationRequest{ExistingRuntimeID: "jd-e4-web", ExistingRuntimeKind: "container"})
	if err != nil || !again.ReplacesRuntime {
		t.Fatalf("redeploy observation = %+v, %v", again.ReplacesRuntime, err)
	}
}
