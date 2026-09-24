package deploy

import (
	"strings"
	"testing"
)

// The Node release comes from the repository's own declarations, nearest
// version file first, then package.json; a range keeps the default when it
// allows it. A release outside the catalogue runs on the nearest major with a
// warning, and a mismatch is refused only where the install is certain to
// stop on it.
func TestNodeReleaseFollowsTheRepositorysDeclarations(t *testing.T) {
	t.Parallel()
	npm := map[string]string{"package-lock.json": npmLock}
	with := func(base map[string]string, extra map[string]string) map[string]string {
		files := map[string]string{}
		for name, content := range base {
			files[name] = content
		}
		for name, content := range extra {
			files[name] = content
		}
		return files
	}
	for _, test := range []struct {
		name     string
		files    map[string]string
		dir      string
		label    string
		image    string
		findings []string
		severity PreflightSeverity
		blocked  string
	}{
		{name: "no declaration", files: with(npm, map[string]string{"package.json": leftPad("")}),
			label: "22 (recipe default)", image: "node:22-alpine"},
		{name: ".nvmrc major", files: with(npm, map[string]string{"package.json": leftPad(""), ".nvmrc": "24\n"}),
			label: "24 (.nvmrc)", image: "node:24-alpine"},
		{name: ".nvmrc exact release", files: with(npm, map[string]string{"package.json": leftPad(""), ".nvmrc": "v20.11.1"}),
			label: "20 (.nvmrc)", image: "node:20-alpine", findings: []string{"node_version_eol"}},
		{name: "lts codename", files: with(npm, map[string]string{"package.json": leftPad(""), ".nvmrc": "lts/iron"}),
			label: "20 (.nvmrc)"},
		{name: "lts star", files: with(npm, map[string]string{"package.json": leftPad(""), ".nvmrc": "lts/*"}),
			label: "24 (.nvmrc)"},
		{name: "an unreadable .nvmrc gives way to engines", files: with(npm, map[string]string{"package.json": leftPad(`,"engines":{"node":">=24"}`), ".nvmrc": "system"}),
			label: "24 (engines.node >=24)"},
		{name: ".node-version outside the catalogue", files: with(npm, map[string]string{"package.json": leftPad(""), ".node-version": "18.19.0"}),
			label: "20 (.node-version)", findings: []string{"node_version_unsupported", "node_version_eol"}, severity: PreflightWarning},
		{name: ".tool-versions", files: with(npm, map[string]string{"package.json": leftPad(""), ".tool-versions": "# asdf\nnodejs 24.1.0\nbun 1.1.38\n"}),
			label: "24 (.tool-versions)"},
		{name: "volta", files: with(npm, map[string]string{"package.json": leftPad(`,"volta":{"node":"20.11.0"}`)}),
			label: "20 (volta.node)"},
		{name: "devEngines runtime", files: with(npm, map[string]string{"package.json": leftPad(`,"devEngines":{"runtime":{"name":"node","version":">=24"}}`)}),
			label: "24 (devEngines.runtime >=24)"},
		{name: "an engines floor keeps the default", files: with(npm, map[string]string{"package.json": leftPad(`,"engines":{"node":">=18.17.0"}`)}),
			label: "22 (engines.node >=18.17.0)"},
		{name: "an engines range above the default", files: with(npm, map[string]string{"package.json": leftPad(`,"engines":{"node":"^24.3.0"}`)}),
			label: "24 (engines.node ^24.3.0)", image: "node:24-alpine"},
		{name: "an engines range below the default", files: with(npm, map[string]string{"package.json": leftPad(`,"engines":{"node":"20.x"}`)}),
			label: "20 (engines.node 20.x)"},
		{name: "a version file outranks engines", files: with(npm, map[string]string{"package.json": leftPad(`,"engines":{"node":"^20"}`), ".nvmrc": "24"}),
			label: "24 (.nvmrc)", findings: []string{"node_version_unsupported"}, severity: PreflightWarning},
		{name: "engines outside the catalogue warns under npm", files: with(npm, map[string]string{"package.json": leftPad(`,"engines":{"node":"18.x"}`)}),
			label: "20 (engines.node 18.x)", findings: []string{"node_version_unsupported"}, severity: PreflightWarning},
		{name: "engine-strict npm refuses a release engines excludes", files: with(npm, map[string]string{"package.json": leftPad(`,"engines":{"node":"18.x"}`), ".npmrc": "engine-strict=true\n"}),
			blocked: "engines.node asks for 18.x; the build runs Node 20"},
		{name: "Yarn 1 refuses a release engines excludes", files: map[string]string{"package.json": leftPad(`,"engines":{"node":"18.x"}`), "yarn.lock": yarnClassic},
			blocked: "engines.node asks for 18.x"},
		{name: "Yarn 1 installs when engines allows the release", files: map[string]string{"package.json": leftPad(`,"engines":{"node":">=20"}`), "yarn.lock": yarnClassic},
			label: "22 (engines.node >=20)"},
		{name: "the nearest version file wins", dir: "apps/web", files: map[string]string{
			"package.json": `{"name":"root","workspaces":["apps/*"]}`, "package-lock.json": `{"lockfileVersion":3,"packages":{"":{"name":"root"},"apps/web":{"name":"web"}}}`,
			".nvmrc": "24", "apps/web/package.json": `{"name":"web"}`, "apps/web/.node-version": "20"},
			label: "20 (apps/web/.node-version)"},
		{name: "a workspace root's version file applies to its members", dir: "apps/web", files: map[string]string{
			"package.json": `{"name":"root","workspaces":["apps/*"]}`, "package-lock.json": `{"lockfileVersion":3,"packages":{"":{"name":"root"},"apps/web":{"name":"web"}}}`,
			".nvmrc": "24", "apps/web/package.json": `{"name":"web"}`},
			label: "24 (.nvmrc)"},
		{name: "node-sass moves an undeclared build to Node 20", files: with(npm, map[string]string{"package.json": leftPad(`,"devDependencies":{"node-sass":"^9.0.0"}`)}),
			label: "20 (node-sass supports Node 20 at most)", findings: []string{"node_version_eol"}},
		{name: "node-sass keeps a declared newer Node and warns", files: with(npm, map[string]string{"package.json": leftPad(`,"devDependencies":{"node-sass":"^9.0.0"}`), ".nvmrc": "22"}),
			label: "22 (.nvmrc)", findings: []string{"node_sass_unsupported"}, severity: PreflightWarning},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := readNodeTree(t, test.files, test.dir)
			plan := planNodeInstall(source.facts, nodeInstallChoice{build: "npm run build", start: "npm run start"})
			if test.blocked != "" {
				if plan.blocked == nil || plan.blocked.Code != "node_version_unsupported" || !strings.Contains(plan.blocked.Measured, test.blocked) {
					t.Fatalf("blocked = %+v, want node_version_unsupported %q", plan.blocked, test.blocked)
				}
				return
			}
			if plan.blocked != nil {
				t.Fatalf("plan blocked: %+v", plan.blocked)
			}
			if plan.node.label() != test.label || nodeReleaseFor(source.facts).label() != test.label {
				t.Fatalf("release = %q, want %q", plan.node.label(), test.label)
			}
			if test.image != "" && plan.nodeImage() != test.image {
				t.Fatalf("image = %q, want %q", plan.nodeImage(), test.image)
			}
			selected := findingByCode(plan.findings, "node_version_selected")
			if selected == nil || selected.Measured != test.label || selected.Severity != PreflightPass {
				t.Fatalf("node_version_selected = %+v", selected)
			}
			for _, code := range test.findings {
				item := findingByCode(plan.findings, code)
				if item == nil || (test.severity != "" && code != "node_version_eol" && item.Severity != test.severity) {
					t.Fatalf("findings %+v lack %s", plan.findings, code)
				}
			}
			if len(test.findings) == 0 {
				for _, code := range []string{"node_version_unsupported", "node_version_eol", "node_sass_unsupported"} {
					if item := findingByCode(plan.findings, code); item != nil && !strings.HasPrefix(test.label, "20 ") {
						t.Fatalf("unexpected %+v", item)
					}
				}
			}
		})
	}
}

// The recipe builds and serves on the chosen major, and records it in the
// build evidence; Bun follows a version file or engines.bun as well as
// packageManager.
func TestNodeRecipeBuildsOnTheDeclaredRelease(t *testing.T) {
	t.Parallel()
	server := BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"}
	for _, test := range []struct {
		name    string
		files   map[string]string
		config  BuildPlanConfig
		want    []string
		absent  []string
		version string
	}{
		{name: "the default", files: map[string]string{"package.json": leftPad(""), "package-lock.json": npmLock}, config: server,
			want: []string{"FROM node:22-alpine@sha256:"}, version: "22 (recipe default)"},
		{name: ".nvmrc", files: map[string]string{"package.json": leftPad(""), "package-lock.json": npmLock, ".nvmrc": "24"}, config: server,
			want: []string{"FROM node:24-alpine@sha256:", " AS build\n", "\nFROM node:24-alpine@sha256:"}, absent: []string{"node:22-alpine"}, version: "24 (.nvmrc)"},
		{name: "a toolchain stage starts from the chosen major", files: map[string]string{"package.json": leftPad(`,"engines":{"node":">=24"}`), "pnpm-lock.yaml": pnpm9Lock},
			config: server, want: []string{"FROM node:24-alpine@sha256:", " AS toolchain\n"}, version: "24 (engines.node >=24)"},
		{name: ".bun-version", files: map[string]string{"package.json": leftPad(""), "bun.lock": bunLock, ".bun-version": "1.1.38"}, config: server,
			want: []string{"COPY --from=oven/bun:1.1.38-alpine@sha256:"}, version: "22 (recipe default)"},
		{name: "engines.bun pinned to a minor line", files: map[string]string{"package.json": leftPad(`,"engines":{"bun":"~1.1.30"}`), "bun.lock": bunLock}, config: server,
			want: []string{"COPY --from=oven/bun:1.1-alpine@sha256:"}},
		{name: "engines.bun the newest 1.x satisfies", files: map[string]string{"package.json": leftPad(`,"engines":{"bun":">=1.1"}`), "bun.lock": bunLock}, config: server,
			want: []string{"COPY --from=oven/bun:1-alpine@sha256:"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared := prepareNode(t, test.files, test.config)
			assertDockerfile(t, prepared.DockerfilePreview, test.want, test.absent)
			if test.version != "" && prepared.NodeVersion != test.version {
				t.Fatalf("prepared.NodeVersion = %q, want %q", prepared.NodeVersion, test.version)
			}
		})
	}
}

func TestNodeReleaseDeclarationsAreReadAsData(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		spec  string
		major int
		ok    bool
	}{
		{"22", 22, true}, {"v24.1.0", 24, true}, {"20.11", 20, true}, {"lts/jod", 22, true}, {"lts/krypton", 24, true},
		{"lts/*", nodeNewestLTS, true}, {"node", 24, true}, {"lts/unknown", 0, false}, {"system", 0, false}, {">=20", 0, false},
	} {
		if major, ok := nodeSpecMajor(test.spec); major != test.major || ok != test.ok {
			t.Fatalf("nodeSpecMajor(%q) = %d, %t", test.spec, major, ok)
		}
	}
	if node, bun := readNodeVersionFile(".tool-versions", "python 3.12\nnodejs 22.11.0 20.0.0 # pinned\nbun 1.2.0\n"); node != "22.11.0" || bun != "1.2.0" {
		t.Fatalf("tool-versions = %q, %q", node, bun)
	}
	if got := firstVersionLine("# comment\n\n  v20 \n"); got != "v20" {
		t.Fatalf("first line = %q", got)
	}
	if boundedSpec(`22"; rm -rf /`) != "" || boundedSpec("$(id)") != "" {
		t.Fatal("a declaration with shell characters must not be read")
	}
	for _, test := range []struct {
		declaration nodeReleaseDeclaration
		want        string
	}{
		{nodeReleaseDeclaration{spec: "1.1.38", exact: true}, "1.1.38"},
		{nodeReleaseDeclaration{spec: "1.2", exact: true}, "1.2"},
		{nodeReleaseDeclaration{spec: "latest", exact: true}, ""},
		{nodeReleaseDeclaration{spec: "~1.1.30"}, "1.1"},
		{nodeReleaseDeclaration{spec: ">=1.0.0"}, ""},
	} {
		if got := nodeBunRelease(test.declaration); got != test.want {
			t.Fatalf("nodeBunRelease(%+v) = %q, want %q", test.declaration, got, test.want)
		}
	}
}
