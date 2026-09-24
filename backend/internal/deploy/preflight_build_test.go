package deploy

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

func buildScopedDraft(t *testing.T, files map[string]string, values map[string]string) (*Draft, DetectedCandidate) {
	t.Helper()
	result, candidate := detectNodeTree(t, files)
	draft := nodeDraft(result)
	draft.environment = values
	return draft, candidate
}

func plannedVariables(scopes map[string][]string) []PlannedVariable {
	variables := []PlannedVariable{}
	for _, name := range slices.Sorted(maps.Keys(scopes)) {
		variables = append(variables, PlannedVariable{Name: name, Sensitivity: "secret", Scopes: scopes[name]})
	}
	return variables
}

const nextPrisma = `{"name":"shop","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.0.0","@prisma/client":"^6.2.0"},"devDependencies":{"prisma":"^6.2.0"}}`

// A heavy build on a small server is named before Deploy: the free memory
// and swap against what the recipe's build is estimated to peak at.
func TestPreflightWarnsWhenTheHostCannotHoldTheBuild(t *testing.T) {
	t.Parallel()
	draft, candidate := buildScopedDraft(t, map[string]string{"package.json": nextPrisma, "prisma/schema.prisma": prismaSchemaFor("postgresql")}, nil)
	if candidate.NodeBuild == nil || candidate.NodeBuild.MemoryMiB != 2048 {
		t.Fatalf("node build = %+v", candidate.NodeBuild)
	}
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "npm run start"})
	small := dockerHost
	small.AvailableMemory, small.AvailableSwap = 900<<20, 100<<20
	low := findingByCode(preflightFindings(draft, configuration, small, false), "build_memory_low")
	if low == nil || low.Severity != PreflightWarning || low.Measured != "1000 MiB free (900 MiB memory, 100 MiB swap); a Next.js build peaks around 2048 MiB" {
		t.Fatalf("build_memory_low = %+v", low)
	}
	small.AvailableSwap = 2 << 30
	if item := findingByCode(preflightFindings(draft, configuration, small, false), "build_memory_low"); item != nil {
		t.Fatalf("swap covers the build: %+v", item)
	}
	rust := &DetectedCandidate{Recipe: "rust"}
	small.AvailableSwap = 0
	if findings := buildMemoryFindings(rust, PlanConfiguration{Build: BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"}}, small); len(findings) != 1 ||
		!strings.Contains(findings[0].Measured, "a Rust release build peaks around 2048 MiB") {
		t.Fatalf("rust = %+v", findings)
	}
	if findings := buildMemoryFindings(rust, PlanConfiguration{Build: BuildPlanConfig{Method: BuildDockerfile}}, small); len(findings) != 0 {
		t.Fatalf("a Dockerfile build has no estimate: %+v", findings)
	}
}

// T3 Env validates its schema when the build imports it; preflight says
// which server variables the build lacks, whether the build skips the
// schema, and whether the server will still refuse to start.
func TestPreflightJudgesTheBuildsEnvValidation(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json": `{"name":"t3","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15.0.0","@t3-oss/env-nextjs":"^0.12.0","zod":"^3.24.0"}}`,
		"src/env.js":   t3Env,
	}
	draft, candidate := buildScopedDraft(t, files, map[string]string{"DATABASE_URL": "postgres://db.example.com/app", "AUTH_DISCORD_ID": "id"})
	if build := candidate.NodeBuild; build == nil || build.EnvSchema != "src/env.js" || !build.EnvSkippable ||
		strings.Join(build.EnvServer, ",") != "AUTH_DISCORD_ID,DATABASE_URL,RATE" || strings.Join(build.EnvClient, ",") != "NEXT_PUBLIC_POSTHOG_KEY" {
		t.Fatalf("node build = %+v", candidate.NodeBuild)
	}
	if err := validateDetectedNodeInstall(candidate); err != nil {
		t.Fatalf("the record does not validate: %v", err)
	}
	malformed := candidate
	malformed.NodeBuild = &DetectedNodeBuild{EnvServer: []string{"not a name"}}
	if validateDetectedNodeInstall(malformed) == nil {
		t.Fatal("a malformed variable name was accepted")
	}
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "npm run start"})
	configuration.Variables = plannedVariables(map[string][]string{"DATABASE_URL": {"runtime", "build"}, "AUTH_DISCORD_ID": {"runtime"}})
	findings := preflightFindings(draft, configuration, dockerHost, false)
	skipped := findingByCode(findings, "build_env_validation_skipped")
	if skipped == nil || skipped.Severity != PreflightWarning || skipped.Measured != "AUTH_DISCORD_ID, RATE" ||
		!strings.Contains(skipped.Means, "RATE have no value at all") || skipped.FieldID != "variables.RATE" {
		t.Fatalf("skipped = %+v", skipped)
	}
	client := findingByCode(findings, "build_env_client_missing")
	if client == nil || client.Measured != "NEXT_PUBLIC_POSTHOG_KEY" {
		t.Fatalf("client = %+v", client)
	}
	draft.environment["RATE"] = "5"
	configuration.Variables = append(configuration.Variables, PlannedVariable{Name: "RATE", Sensitivity: "secret", Scopes: []string{"runtime"}})
	if skipped := findingByCode(preflightFindings(draft, configuration, dockerHost, false), "build_env_validation_skipped"); skipped == nil || skipped.Severity != PreflightPass {
		t.Fatalf("every skipped variable has a runtime value: %+v", skipped)
	}

	files["src/env.js"] = strings.Replace(t3Env, "skipValidation: !!process.env.SKIP_ENV_VALIDATION,", "", 1)
	draft, _ = buildScopedDraft(t, files, map[string]string{"DATABASE_URL": "postgres://db.example.com/app"})
	configuration.Variables = plannedVariables(map[string][]string{"DATABASE_URL": {"runtime", "build"}})
	missing := findingByCode(preflightFindings(draft, configuration, dockerHost, false), "build_env_missing")
	if missing == nil || missing.Severity != PreflightWarning || missing.Measured != "AUTH_DISCORD_ID, RATE" || !strings.Contains(missing.Action, "skipValidation") {
		t.Fatalf("missing = %+v", missing)
	}
}

// A prerendering build cannot reach a linked database, whose alias exists
// only on the network the running release joins, nor a loopback one.
func TestPreflightWarnsWhenThePrerenderCannotReachTheDatabase(t *testing.T) {
	t.Parallel()
	files := map[string]string{"package.json": nextPrisma, "prisma/schema.prisma": prismaSchemaFor("postgresql")}
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "npm run start"})
	for _, test := range []struct {
		name, value string
		scopes      []string
		reference   string
		measured    string
	}{
		{name: "linked database", value: "postgresql://app:hunter2@db-12.jd.internal:5432/app", scopes: []string{"runtime", "build"},
			measured: "DATABASE_URL points at db-12.jd.internal, which only the running release can resolve"},
		{name: "loopback", value: "postgres://app:hunter2@127.0.0.1:5432/app", scopes: []string{"runtime", "build"},
			measured: "DATABASE_URL points at 127.0.0.1, which inside the build is the build container itself"},
		{name: "a typed database reference", reference: "${{database.12.url}}", scopes: []string{"runtime", "build"},
			measured: "DATABASE_URL is a linked database, which only the running release can reach"},
		{name: "a hosted database", value: "postgresql://app:hunter2@ep-cool.neon.tech/app", scopes: []string{"runtime", "build"}},
		{name: "runtime only", value: "postgresql://app:hunter2@db-12.jd.internal:5432/app", scopes: []string{"runtime"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := map[string]string{}
			if test.value != "" {
				values["DATABASE_URL"] = test.value
			}
			draft, _ := buildScopedDraft(t, files, values)
			configuration := configuration
			configuration.Variables = []PlannedVariable{{Name: "DATABASE_URL", Sensitivity: "secret", Scopes: test.scopes, Reference: test.reference}}
			unreachable := findingByCode(preflightFindings(draft, configuration, dockerHost, false), "build_database_unreachable")
			if test.measured == "" {
				if unreachable != nil {
					t.Fatalf("unexpected %+v", unreachable)
				}
				return
			}
			if unreachable == nil || unreachable.Severity != PreflightWarning || unreachable.Measured != test.measured || unreachable.FieldID != "variables.DATABASE_URL" {
				t.Fatalf("unreachable = %+v", unreachable)
			}
			if strings.Contains(unreachable.Measured+unreachable.Means+unreachable.Action, "hunter2") {
				t.Fatal("a value was echoed")
			}
		})
	}
}

// PORT, NODE_ENV and a loopback HOSTNAME pasted from a local .env replace
// what the deployment sets itself.
func TestPreflightNamesPlatformVariablesAnOperatorSet(t *testing.T) {
	t.Parallel()
	draft, _ := buildScopedDraft(t, map[string]string{"package.json": `{"name":"api","scripts":{"start":"node index.js"},"dependencies":{"express":"^5.1.0"}}`},
		map[string]string{"PORT": "5000", "NODE_ENV": "development", "HOSTNAME": "localhost", "HOST": "0.0.0.0"})
	configuration := nodeTestConfiguration(BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "npm run start"})
	configuration.Runtime.InternalPort = 3000
	configuration.Variables = plannedVariables(map[string][]string{"PORT": {"runtime"}, "NODE_ENV": {"runtime", "build"}, "HOSTNAME": {"runtime"}, "HOST": {"runtime"}})
	findings := preflightFindings(draft, configuration, dockerHost, false)
	for code, measured := range map[string]string{
		"port_variable_mismatch": "PORT=5000; the internal port is 3000", "node_env_not_production": "NODE_ENV=development",
		"host_variable_loopback": "HOSTNAME=localhost",
	} {
		item := findingByCode(findings, code)
		if item == nil || item.Severity != PreflightWarning || item.Measured != measured {
			t.Fatalf("%s = %+v", code, item)
		}
	}
	if item := findingByCode(findings, "node_env_not_production"); !strings.Contains(item.Means, "the build and the running server") {
		t.Fatalf("node env = %+v", item)
	}
	draft.environment = map[string]string{"PORT": "3000", "NODE_ENV": "production", "HOSTNAME": "0.0.0.0"}
	findings = preflightFindings(draft, configuration, dockerHost, false)
	for _, code := range []string{"port_variable_mismatch", "node_env_not_production", "host_variable_loopback"} {
		if item := findingByCode(findings, code); item != nil {
			t.Fatalf("unexpected %+v", item)
		}
	}
	static := configuration
	static.Build.OutputDirectory = "dist"
	draft.environment = map[string]string{"PORT": "5000"}
	if item := findingByCode(preflightFindings(draft, static, dockerHost, false), "port_variable_mismatch"); item != nil {
		t.Fatalf("nginx serves static output whatever PORT says: %+v", item)
	}
}
