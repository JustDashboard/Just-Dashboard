package deploy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func standaloneNextTree(extra map[string]string) map[string]string {
	files := map[string]string{
		"package.json":      `{"name":"web","version":"1.0.0","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.3.5"}}`,
		"package-lock.json": `{"name":"web","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"web","version":"1.0.0","dependencies":{"next":"16.3.5"}},"node_modules/next":{"version":"16.3.5"}}}`,
		"next.config.mjs":   "export default { output: 'standalone' }",
	}
	for name, content := range extra {
		files[name] = content
	}
	return files
}

// A new detection of a standalone app runs its server, and the image
// carries the standalone folder alone: no node_modules layer, no source.
func TestNextStandaloneShipsOnlyItsServer(t *testing.T) {
	t.Parallel()
	_, candidate, prepared := prepareDetectedNode(t, standaloneNextTree(nil))
	if candidate.StartCommand != "node .next/standalone/server.js" {
		t.Fatalf("start = %q", candidate.StartCommand)
	}
	runtime := prepared.DockerfilePreview[strings.LastIndex(prepared.DockerfilePreview, "\nFROM "):]
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"RUN test -f /app/.next/standalone/server.js",
		"RUN mkdir -p .next/standalone/.next && cp -r .next/static .next/standalone/.next/ && if [ -d public ]; then cp -r public .next/standalone/; fi\n",
	}, nil)
	assertDockerfile(t, runtime, []string{
		"ENV HOSTNAME=0.0.0.0\n",
		"COPY --from=build /app/.next/standalone /app/.next/standalone\n" + `CMD ["/bin/sh","-c","node .next/standalone/server.js"]`,
	}, []string{"node_module[s]", "COPY --from=build --exclude", "COPY --from=build /app /app"})
}

// A workspace member's standalone server is written under its own path,
// since Next.js traces from the workspace root.
func TestNextStandaloneMemberStartsItsServerUnderItsPath(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json":            `{"name":"root","private":true,"workspaces":["apps/*"]}`,
		"package-lock.json":       `{"name":"root","lockfileVersion":3,"requires":true,"packages":{"":{"name":"root","workspaces":["apps/*"]},"apps/web":{"name":"web","dependencies":{"next":"16.3.5"}},"node_modules/web":{"resolved":"apps/web","link":true},"node_modules/next":{"version":"16.3.5"}}}`,
		"apps/web/package.json":   `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.3.5"}}`,
		"apps/web/next.config.js": "module.exports = { output: 'standalone' }",
	}
	root := writeNodeTree(t, files)
	source, err := readNodeInstallSource(root, "apps/web", "x64", newNodeReadBudget())
	if err != nil {
		t.Fatal(err)
	}
	var manifest nodeManifest
	parseNodeManifest([]byte(files["apps/web/package.json"]), &manifest)
	framework, _, _ := resolveNodeFramework(manifest, source.files, "")
	if framework == nil {
		t.Fatal("no framework")
	}
	resolution := framework.resolve(manifest, source.files, "npm")
	if resolution.Start != "node .next/standalone/apps/web/server.js" || resolution.Entry != ".next/standalone/apps/web/server.js" {
		t.Fatalf("member resolution = %q, entry %q", resolution.Start, resolution.Entry)
	}
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(context.Background(), root, filepath.Join(root, "apps", "web"), BuildPlanConfig{
		Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/web", BuildCommand: "npm run build", StartCommand: resolution.Start,
	}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"RUN test -f /app/apps/web/.next/standalone/apps/web/server.js",
		"COPY --from=build /app/apps/web/.next/standalone /app/apps/web/.next/standalone\n",
	}, []string{"node_module[s]"})
}

// An existing plan keeps next start, and its whole image; preflight says
// the standalone server is there until the start command runs it.
func TestNextStandaloneLeavesAStoredStartCommandAlone(t *testing.T) {
	t.Parallel()
	files := standaloneNextTree(nil)
	result, candidate := detectNodeTree(t, files)
	for _, test := range []struct {
		start   string
		suggest bool
	}{
		{"npx next start", true}, {"npm run start", true}, {"node .next/standalone/server.js", false},
	} {
		plan := BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: candidate.BuildCommand, StartCommand: test.start}
		item := findingByCode(preflightFindings(nodeDraft(result), nodeTestConfiguration(plan), dockerHost, false), "next_standalone_start")
		if (item != nil) != test.suggest || item != nil && !strings.Contains(item.Action, "node .next/standalone/server.js") {
			t.Fatalf("%s: next_standalone_start = %+v", test.start, item)
		}
	}
	prepared := prepareNode(t, files, BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npx next start"})
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"COPY --from=build /app/node_module[s] /app/node_modules/\nCOPY --from=build --exclude=node_modules /app /app\n",
		`CMD ["/bin/sh","-c","npx next start"]`,
	}, []string{"COPY --from=build /app/.next/standalone"})
}

func TestNodeStandaloneCopyOnlyForTheServerAlone(t *testing.T) {
	t.Parallel()
	for start, want := range map[string]bool{
		"node .next/standalone/server.js":                              true,
		"node ./.next/standalone/apps/web/server.js":                   true,
		"node .next/standalone/server.js --inspect":                    false,
		"npx prisma migrate deploy && node .next/standalone/server.js": false,
		"npm run start":                      false,
		"node ../.next/standalone/server.js": false,
	} {
		if got := nodeStandaloneCopy(start, "/app") != nil; got != want {
			t.Fatalf("%q: slim copy %v, want %v", start, got, want)
		}
	}
}
