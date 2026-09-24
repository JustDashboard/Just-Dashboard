package deploy

import (
	"slices"
	"strings"
	"testing"
)

// nodeBuildRun is the build command's RUN as the recipe renders it with no
// defaults beyond the heap: the probe, then NODE_OPTIONS unless the build
// supplies its own.
func nodeBuildRun(command string) string {
	return nodeBuildRunWith("", "", command)
}

// nodeBuildRunWith is the same RUN with mounts and further defaults.
func nodeBuildRunWith(mounts, defaults, command string) string {
	if defaults != "" {
		defaults = " " + defaults
	}
	return "RUN " + mounts + nodeHeapProbe + ` && export NODE_OPTIONS="${NODE_OPTIONS:-$jd_heap}"` + defaults + " && " + command
}

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
		{"22", 22, true}, {"v24.1.0", 24, true}, {"20.11", 20, true}, {"22.x", 22, true}, {"lts/jod", 22, true}, {"lts/krypton", 24, true},
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

// Each dependency that needs something the Node image lacks gets it, in the
// stage that needs it: compilers and git only where the install runs,
// libraries and browsers where the server runs, and Debian instead of Alpine
// only for packages that ship no musl binary.
func TestNodeRecipeAddsTheSystemPackagesDependenciesNeed(t *testing.T) {
	t.Parallel()
	server := BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"}
	manifest := func(dependencies string) string {
		return `{"name":"svc","scripts":{"build":"tsc","start":"node dist/index.js"},` + dependencies + `}`
	}
	for _, test := range []struct {
		name     string
		files    map[string]string
		config   BuildPlanConfig
		want     []string
		absent   []string
		findings []string
	}{
		{name: "bcrypt gets compilers in the build stage only", files: map[string]string{"package.json": manifest(`"dependencies":{"bcrypt":"^5.1.1"}`)},
			config: server, findings: []string{"native_addon_toolchain"},
			want:   []string{" AS build\nRUN apk add --no-cache g++ make python3\nWORKDIR /app\nCOPY . .\n", "\nFROM node:22-alpine@sha256:"},
			absent: []string{"cairo"}},
		{name: "a lockfile's native addon counts", files: map[string]string{"package.json": manifest(`"dependencies":{"auth":"^1.0.0"}`),
			"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"dependencies":{"auth":"^1.0.0"}},"node_modules/auth":{"version":"1.0.0"},"node_modules/better-sqlite3":{"version":"8.7.0"}}}`},
			config: server, want: []string{"RUN apk add --no-cache g++ make python3\n"}, findings: []string{"native_addon_toolchain"}},
		{name: "canvas compiles against cairo and runs with its libraries", files: map[string]string{"package.json": manifest(`"dependencies":{"canvas":"^3.1.0"}`)},
			config: server, findings: []string{"native_addon_toolchain"},
			want: []string{
				"RUN apk add --no-cache cairo-dev g++ giflib-dev jpeg-dev librsvg-dev make pango-dev pixman-dev pkgconf python3\n",
				"\nFROM node:22-alpine@sha256:" + fakeContentDigest("node:22-alpine")[len("sha256:"):] + "\nRUN apk add --no-cache cairo giflib jpeg librsvg pango pixman\nWORKDIR /app\n"}},
		{name: "a glibc-only package moves the image to Debian slim", files: map[string]string{"package.json": manifest(`"dependencies":{"onnxruntime-node":"^1.20.0"}`), "package-lock.json": npmLock},
			config: server, findings: []string{"glibc_image_selected"},
			want: []string{"FROM node:22-bookworm-slim@sha256:"}, absent: []string{"alpine"}},
		{name: "transformers.js needs glibc through onnxruntime-node", files: map[string]string{"package.json": manifest(`"dependencies":{"@huggingface/transformers":"^3.0.0","bcrypt":"^5.1.1"}`)},
			config: server, findings: []string{"glibc_image_selected", "native_addon_toolchain"},
			want: []string{"FROM node:22-bookworm-slim@sha256:", "RUN apt-get update && apt-get install -y --no-install-recommends g++ make python3 && rm -rf /var/lib/apt/lists/*\n"}},
		{name: "Bun is copied from its Debian build on Debian", files: map[string]string{"package.json": manifest(`"dependencies":{"onnxruntime-node":"^1.20.0"}`), "bun.lock": `{"lockfileVersion":1,"workspaces":{"":{"dependencies":{"onnxruntime-node":"^1.20.0"}}},"packages":{"onnxruntime-node":["onnxruntime-node@1.20.1","",{},"x"]}}`},
			config: BuildPlanConfig{BuildCommand: "bun run build", StartCommand: "bun run start"},
			want:   []string{"FROM node:22-bookworm-slim@sha256:", "COPY --from=oven/bun:1-slim@sha256:"}, absent: []string{"alpine"}},
		{name: "a Git dependency gets git", files: map[string]string{"package.json": manifest(`"dependencies":{"lib":"github:owner/lib#v1.2.0"}`)},
			config: server, findings: []string{"git_dependencies"}, want: []string{"RUN apk add --no-cache git\n"}, absent: []string{"openssh"}},
		{name: "an SSH Git dependency gets ssh and a warning", files: map[string]string{"package.json": manifest(`"dependencies":{"lib":"git+ssh://git@git.example.com/team/lib.git#main"}`)},
			config: server, findings: []string{"git_dependencies", "git_dependency_credentials"}, want: []string{"RUN apk add --no-cache git openssh-client\n"}},
		{name: "npm's lockfile names a transitive Git dependency", files: map[string]string{"package.json": manifest(`"dependencies":{"left-pad":"^1.3.0"}`),
			"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"dependencies":{"left-pad":"^1.3.0"}},"node_modules/left-pad":{"version":"1.3.0"},"node_modules/patched":{"version":"1.0.0","resolved":"git+https://github.com/owner/patched.git#0123456789abcdef0123456789abcdef01234567"}}}`},
			config: server, findings: []string{"git_dependencies"}, want: []string{"RUN apk add --no-cache git\n"}},
		{name: "Prisma gets OpenSSL where it generates and where it runs", files: map[string]string{"package.json": manifest(`"dependencies":{"@prisma/client":"^6.2.0"},"devDependencies":{"prisma":"^6.2.0"}`)},
			config: server, want: []string{" AS build\nRUN apk add --no-cache openssl\n", "\nRUN apk add --no-cache openssl\nWORKDIR /app\n"}},
		{name: "puppeteer uses the image's Chromium", files: map[string]string{"package.json": manifest(`"dependencies":{"puppeteer":"^23.0.0"}`), "package-lock.json": npmLock},
			config: server, findings: []string{"headless_browser"},
			want: []string{
				`RUN export PUPPETEER_SKIP_DOWNLOAD="${PUPPETEER_SKIP_DOWNLOAD:-true}" PUPPETEER_SKIP_CHROMIUM_DOWNLOAD="${PUPPETEER_SKIP_CHROMIUM_DOWNLOAD:-true}" && npm install --no-audit --no-fund` + "\n",
				"RUN apk add --no-cache ca-certificates chromium font-freefont freetype harfbuzz nss\n",
				"ENV PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium-browser\n"}},
		{name: "Playwright downloads Chromium into the application on Debian", files: map[string]string{"package.json": manifest(`"dependencies":{"playwright":"^1.48.0"}`), "package-lock.json": npmLock},
			config: server, findings: []string{"headless_browser", "glibc_image_selected"},
			want: []string{"FROM node:22-bookworm-slim@sha256:", "ENV PLAYWRIGHT_BROWSERS_PATH=/app/.cache/ms-playwright\nWORKDIR /app\n",
				"RUN npx playwright install chromium\n" + nodeBuildRun("npm run build\n"), "COPY --from=build /app /app\nRUN npx playwright install-deps chromium\nCMD"}},
		{name: "Playwright for tests changes nothing", files: map[string]string{"package.json": manifest(`"devDependencies":{"@playwright/test":"^1.48.0","playwright":"^1.48.0"}`), "package-lock.json": npmLock},
			config: server, absent: []string{"bookworm", "playwright", "apk add"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared := prepareNode(t, test.files, test.config)
			assertDockerfile(t, prepared.DockerfilePreview, test.want, test.absent)
			plan := planNodeInstall(readNodeTree(t, test.files, "").facts, nodeInstallChoice{build: test.config.BuildCommand, start: test.config.StartCommand})
			for _, code := range test.findings {
				if findingByCode(plan.findings, code) == nil {
					t.Fatalf("findings %+v lack %s", plan.findings, code)
				}
			}
		})
	}
}

func TestNodeGitSourcesAreReadFromTheSpecification(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		spec        string
		cloned, ssh bool
	}{
		{"github:owner/repo#v1", true, false}, {"owner/repo", true, false}, {"owner/repo#semver:^1.0", true, false},
		{"git+https://github.com/owner/repo.git", true, false}, {"git://github.com/owner/repo.git", true, false},
		{"https://github.com/owner/repo.git#main", true, false}, {"git+ssh://git@github.com/owner/repo.git", true, true},
		{"git@github.com:owner/repo.git", true, true}, {"gitlab:group/repo", true, false},
		{"^1.2.3", false, false}, {"npm:other@^1", false, false}, {"workspace:*", false, false}, {"file:../lib", false, false},
		{"https://codeload.github.com/owner/repo/tar.gz/main", false, false}, {"@scope/pkg", false, false}, {"latest", false, false},
	} {
		if cloned, ssh := nodeGitSource(test.spec); cloned != test.cloned || ssh != test.ssh {
			t.Fatalf("nodeGitSource(%q) = %t, %t", test.spec, cloned, ssh)
		}
	}
}

// Every finding the runtime planner can record fits the bounds a saved
// draft is validated against: a detection that did not would be refused as
// malformed when the draft is saved.
func TestNodeRuntimeFindingsFitTheDetectionBounds(t *testing.T) {
	t.Parallel()
	trees := []map[string]string{
		{"package.json": `{"name":"a","scripts":{"build":"tsc","start":"node --env-file=.env dist/index.js"},"engines":{"node":"18.x"},` +
			`"dependencies":{"canvas":"^3.1.0","puppeteer":"^24.0.0","lib":"git+ssh://git@git.example.com/team/lib.git","node-sass":"^9.0.0",` +
			`"@prisma/client":"^7.0.0","@t3-oss/env-nextjs":"^0.12.0","react-scripts":"4.0.3"},"devDependencies":{"prisma":"^7.0.0"}}`,
			".nvmrc": "18", "prisma.config.ts": prisma7Config, "prisma/schema.prisma": prismaSchemaFor("sqlserver"), "src/env.js": t3Env},
		{"package.json": `{"name":"b","scripts":{"build":"prisma generate && next build","start":"next start"},` +
			`"dependencies":{"next":"16.0.0","playwright":"^1.48.0","@huggingface/transformers":"^3.0.0","@prisma/client":"^7.0.0"},"devDependencies":{"prisma":"^7.0.0"}}`,
			"yarn.lock": yarnClassic, "prisma.config.ts": prisma7Config, "prisma/schema.prisma": prismaSchemaFor("postgresql")},
	}
	for _, files := range trees {
		_, candidate := detectNodeTree(t, files)
		if err := validateDetectedNodeInstall(candidate); err != nil {
			for _, install := range candidate.NodeInstalls {
				for _, item := range install.Findings {
					if len(item.Means) > 512 || len(item.Action) > 512 || len(item.Title) > 512 {
						t.Errorf("%s/%s is too long: %d %d %d", install.Manager, item.Code, len(item.Title), len(item.Means), len(item.Action))
					}
				}
			}
			t.Fatalf("detection does not validate: %v", err)
		}
		resolved := slices.IndexFunc(candidate.NodeInstalls, func(install DetectedNodeInstall) bool { return install.Manager == candidate.PackageManager })
		if resolved < 0 || len(candidate.NodeInstalls[resolved].Findings) < 5 {
			t.Fatalf("installs = %+v", candidate.NodeInstalls)
		}
	}
}
