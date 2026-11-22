package deploy

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
)

const orderLock = `{"name":"app","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"app","version":"1.0.0"}}}`

// A plain npm app installs from its inputs before the source arrives, and
// the server image copies node_modules as a layer of its own.
func TestNodeRecipeInstallsBeforeTheSourceAndShipsNodeModulesApart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBuildFixture(t, root, "package.json", `{"name":"app","version":"1.0.0","scripts":{"build":"node build.js","start":"node server.js","prepare":"husky"}}`)
	writeBuildFixture(t, root, "package-lock.json", orderLock)
	writeBuildFixture(t, root, ".npmrc", "fund=false\n")
	writeBuildFixture(t, root, "patches/left-pad+1.3.0.patch", "--- a\n+++ b\n")
	writeBuildFixture(t, root, "server.js", "")
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root, BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "npm start",
	}, false, "just-dashboard/deployment-3:run-1")
	if err != nil {
		t.Fatal(err)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"WORKDIR /app\nCOPY --parents [\"package.json\",\"package-lock.json\",\".npmrc\",\"patches/\",\"./\"]\nRUN npm ci\nCOPY . /app/\n",
		"COPY --from=build /app/node_module[s] /app/node_modules/\nCOPY --from=build --exclude=node_modules /app /app\nCMD",
	}, []string{"COPY . .\n", "COPY --from=build /app /app\n"})
	if !slices.ContainsFunc(prepared.Notes, func(note string) bool {
		return strings.HasPrefix(note, "Dependencies install before the source is copied, from package.json, package-lock.json, .npmrc, patches/")
	}) {
		t.Fatalf("notes = %v", prepared.Notes)
	}
}

// Whatever could make the install read a file outside its inputs keeps the
// source-first order, and the run's notes say why.
func TestNodeInstallKeepsTheSourceFirstWhenItCouldReadPastItsInputs(t *testing.T) {
	t.Parallel()
	manifest := func(extra string) string {
		return `{"name":"app","version":"1.0.0","scripts":{"build":"node build.js","start":"node server.js"` + extra + `}`
	}
	for _, test := range []struct {
		name   string
		files  map[string]string
		member string
		reason string
	}{
		{name: "a postinstall script", files: map[string]string{"package.json": manifest(`,"postinstall":"node scripts/setup.js"}`)}, reason: "package.json has a postinstall script"},
		{name: "a workspace member's prepare script", files: map[string]string{
			"package.json":            `{"name":"root","private":true,"workspaces":["packages/*"]}`,
			"packages/a/package.json": `{"name":"a","scripts":{"prepare":"tsc"}}`,
		}, reason: "packages/a/package.json has a prepare script"},
		{name: "a native addon of the package's own", files: map[string]string{"package.json": manifest("}"), "binding.gyp": "{}"}, reason: "binding.gyp makes the install compile"},
		{name: "a file: dependency", files: map[string]string{"package.json": manifest(`},"dependencies":{"local":"file:./vendor/local"}`)}, reason: "installs local from a path of the repository (file:./vendor/local)"},
		{name: "an override to a link", files: map[string]string{"package.json": manifest(`},"overrides":{"a":{"b":"link:../b"}}`)}, reason: "installs a>b from a path of the repository"},
		{name: "a Yarn patch outside .yarn/patches", files: map[string]string{"package.json": manifest(`},"resolutions":{"x":"patch:x@npm%3A1.0.0#./fixes/x.patch"}`)}, reason: "patches x with a file outside .yarn/patches"},
		{name: "Prisma", files: map[string]string{"package.json": manifest(`},"dependencies":{"@prisma/client":"6.2.0"}`)}, reason: "depends on @prisma/client"},
		{name: "a pnpm patch that is not a file", files: map[string]string{"package.json": manifest(`},"pnpm":{"patchedDependencies":{"x@1.0.0":"patches/missing.patch"}}`)}, reason: "patches x@1.0.0 with patches/missing.patch"},
		{name: "a pnpmfile", files: map[string]string{"package.json": manifest("}"), ".pnpmfile.cjs": "module.exports = {}"}, reason: "a pnpmfile is code"},
		{name: "injected workspace packages", files: map[string]string{"package.json": manifest(`},"dependenciesMeta":{"ui":{"injected":true}}`)}, reason: "injects ui"},
		{name: "an npmrc naming a CA file", files: map[string]string{"package.json": manifest("}"), ".npmrc": "cafile=./certs/ca.pem\n"}, reason: ".npmrc sets cafile"},
		{name: "a member's npmrc naming a CA file", member: "web", files: map[string]string{
			"package.json": `{"name":"root","private":true,"workspaces":["web"]}`, "web/package.json": manifest("}"), "web/.npmrc": "cafile=ca.pem\n",
		}, reason: "web/.npmrc sets cafile"},
		{name: "Yarn from outside .yarn/releases", files: map[string]string{"package.json": manifest("}"), ".yarnrc.yml": "yarnPath: tools/yarn.cjs\n"}, reason: "outside .yarn/releases"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reach := readFixtureReach(t, test.files, test.member)
			if !strings.Contains(reach.sourceFirst, test.reason) {
				t.Fatalf("source first = %q, want it to say %q", reach.sourceFirst, test.reason)
			}
		})
	}
}

func TestNodeInstallAddsWhatItReadsToItsInputs(t *testing.T) {
	t.Parallel()
	reach := readFixtureReach(t, map[string]string{
		"package.json":                `{"name":"root","private":true,"workspaces":["packages/*"],"scripts":{"prepare":"is-ci || husky"},"patchedDependencies":{"y@2.0.0":"fixes/y.patch"},"pnpm":{"patchedDependencies":{"x@1.0.0":"patches/x.patch"}}}`,
		"patches/x.patch":             "",
		"fixes/y.patch":               "",
		"packages/cli/package.json":   `{"name":"cli","bin":{"cli":"./bin/cli.js","built":"dist/cli.js"}}`,
		"packages/cli/bin/cli.js":     "",
		"packages/lib/package.json":   `{"name":"lib","bin":"lib.js"}`,
		".env.yarn":                   "",
		"packages/lib/package-x.json": "",
	}, "")
	if reach.sourceFirst != "" {
		t.Fatalf("source first: %s", reach.sourceFirst)
	}
	want := []string{"patches/x.patch", "fixes/y.patch", "packages/cli/bin/cli.js", ".env.yarn"}
	if !slices.Equal(reach.extra, want) {
		t.Fatalf("extra inputs = %v, want %v", reach.extra, want)
	}
}

func readFixtureReach(t *testing.T, files map[string]string, member string) nodeInstallReach {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		writeBuildFixture(t, root, name, content)
	}
	opened, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { opened.Close() })
	tree := nodeFiles{root: opened, budget: newNodeReadBudget()}
	return readNodeInstallReach(tree, member, nodeWorkspaceManifests(tree, member))
}

// The plan's own facts keep the source first too: Yarn's committed cache
// and .pnp.cjs, a stale lockfile, and an npm lock linking outside the inputs.
func TestNodeInstallOrderFromThePlan(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		plan   nodeInstallPlan
		facts  nodeInstallFacts
		reason string
	}{
		{name: "zero-installs", plan: nodeInstallPlan{berry: true}, facts: nodeInstallFacts{reach: nodeInstallReach{yarnCache: true}}, reason: "zero-installs"},
		{name: "a committed .pnp.cjs", plan: nodeInstallPlan{berry: true}, facts: nodeInstallFacts{reach: nodeInstallReach{pnp: true}}, reason: ".pnp.cjs is committed"},
		{name: "a stale lockfile", plan: nodeInstallPlan{lockfile: "package-lock.json", reading: &nodeLockfileReading{}}, reason: "package-lock.json is out of sync"},
		{name: "a link outside the inputs", plan: nodeInstallPlan{lockfile: "package-lock.json", frozen: true, reading: &nodeLockfileReading{links: []string{"packages/a", "vendor/x"}}},
			facts: nodeInstallFacts{workspaceManifests: []string{"packages/a/package.json"}}, reason: "links a package to vendor/x"},
		{name: "a link to a member", plan: nodeInstallPlan{lockfile: "package-lock.json", frozen: true, reading: &nodeLockfileReading{links: []string{"packages/a"}}},
			facts: nodeInstallFacts{workspaceManifests: []string{"packages/a/package.json"}}},
	} {
		plan := test.plan
		planNodeInstallOrder(test.facts, &plan)
		first, reason := plan.installFirst()
		if first != (test.reason == "") || !strings.Contains(reason, test.reason) {
			t.Fatalf("%s: install first %v, reason %q", test.name, first, reason)
		}
		if note := plan.installOrderNote(); first != strings.HasPrefix(note, "Dependencies install before") {
			t.Fatalf("%s: note %q", test.name, note)
		}
	}
	// A path a COPY would read as a glob is never copied by name.
	plan := nodeInstallPlan{inputs: []string{"package.json", "packages/[a]/package.json"}}
	if first, reason := plan.installFirst(); first || !strings.Contains(reason, "cannot be copied by name") {
		t.Fatalf("a glob-shaped input: %v %q", first, reason)
	}
}

// The callers that install in a subdirectory get the inputs at their path
// in the context, and the stage keeps the source-first order on fallback.
func TestNodeInstallLinesForEveryCaller(t *testing.T) {
	t.Parallel()
	plan := nodeInstallPlan{inputs: []string{"package.json", "package-lock.json", ".yarn/releases/"}}
	if got := strings.Join(nodeInstallLines(plan, "web", "RUN npm ci"), "\n"); got !=
		"COPY --parents [\"web/package.json\",\"web/package-lock.json\",\"web/.yarn/releases/\",\"./\"]\nWORKDIR /app/web\nRUN npm ci\nCOPY . /app/" {
		t.Fatalf("install-first lines:\n%s", got)
	}
	plan.sourceFirst, plan.berry = "package.json has a postinstall script", true
	if got := strings.Join(nodeInstallLines(plan, ".", "RUN yarn install --immutable"), "\n"); got !=
		"COPY . .\nENV YARN_ENABLE_GLOBAL_CACHE=false\nRUN yarn install --immutable" {
		t.Fatalf("source-first lines:\n%s", got)
	}
}
