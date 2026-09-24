package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	pnpm9Lock     = "lockfileVersion: '9.0'\n\nimporters:\n\n  .:\n    dependencies:\n      left-pad:\n        specifier: ^1.3.0\n        version: 1.3.0\n\npackages:\n\n  left-pad@1.3.0: {}\n"
	pnpm6Lock     = "lockfileVersion: '6.0'\n\ndependencies:\n  left-pad:\n    specifier: ^1.3.0\n    version: 1.3.0\n\npackages:\n\n  /left-pad@1.3.0: {}\n"
	npmLock       = `{"lockfileVersion":3,"packages":{"":{"dependencies":{"left-pad":"^1.3.0"}},"node_modules/left-pad":{"version":"1.3.0"}}}`
	bunLock       = `{"lockfileVersion":1,"workspaces":{"":{"dependencies":{"left-pad":"^1.3.0",},},},"packages":{"left-pad":["left-pad@1.3.0","",{},"x"],}}`
	yarnClassic   = "# yarn lockfile v1\n\nleft-pad@^1.3.0:\n  version \"1.3.0\"\n"
	leftPadServer = `{"name":"svc","scripts":{"build":"tsc","start":"node dist/index.js"},"dependencies":{"left-pad":"^1.3.0"}%s}`
)

func leftPad(extra string) string { return strings.Replace(leftPadServer, "%s", extra, 1) }

func yarnBerryLock(metadata string) string {
	return "__metadata:\n  version: " + metadata + "\n  cacheKey: 10c0\n\n\"left-pad@npm:^1.3.0\":\n  version: 1.3.0\n\n\"svc@workspace:.\":\n  version: 0.0.0-use.local\n  dependencies:\n    left-pad: \"npm:^1.3.0\"\n"
}

// The resolution order: the operator's choice, the manifest's declaration,
// the one lockfile a frozen install accepts, the one in-sync lockfile, the
// files only one manager writes, and only then a question.
func TestNodeInstallResolvesThePackageManager(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		files    map[string]string
		selected string
		manager  string
		install  string
		reason   string
		refused  string
		findings []string
	}{
		{name: "single lockfile", files: map[string]string{"package.json": leftPad(""), "bun.lock": bunLock},
			manager: "bun", install: "bun install --frozen-lockfile", reason: "the only lockfile is bun.lock"},
		{name: "bun.lock supersedes bun.lockb", files: map[string]string{"package.json": leftPad(""), "bun.lock": bunLock, "bun.lockb": "\x00left-pad"},
			manager: "bun", install: "bun install --frozen-lockfile", reason: "the only lockfile is bun.lock"},
		{name: "unreadable competitors need a decision", files: map[string]string{"package.json": leftPad(""), "bun.lock": "{}", "package-lock.json": "{}"},
			refused: "competing lockfiles bun.lock and package-lock.json"},
		{name: "a manager-exclusive file breaks the tie", files: map[string]string{"package.json": leftPad(`,"trustedDependencies":[]`), "bun.lock": "{}", "package-lock.json": "{}"},
			manager: "bun", reason: "trustedDependencies in package.json point to Bun", findings: []string{"package_manager_resolved"}},
		{name: "the one lockfile a frozen install accepts wins", files: map[string]string{
			"package.json": leftPad(""), "pnpm-lock.yaml": pnpm9Lock,
			"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"dependencies":{}}}}`},
			manager: "pnpm", reason: "pnpm-lock.yaml matches package.json; package-lock.json is missing 1 dependency (left-pad)"},
		{name: "the one in-sync lockfile wins over an unreadable one", files: map[string]string{"package.json": leftPad(""), "pnpm-lock.yaml": pnpm9Lock, "yarn.lock": ""},
			manager: "pnpm"},
		{name: "packageManager selects among lockfiles", files: map[string]string{"package.json": leftPad(`,"packageManager":"npm@10.9.2"`), "pnpm-lock.yaml": pnpm9Lock, "package-lock.json": npmLock},
			manager: "npm", install: "npm ci", reason: "packageManager in package.json selects npm"},
		{name: "devEngines selects among lockfiles", files: map[string]string{"package.json": leftPad(`,"devEngines":{"packageManager":{"name":"pnpm","version":"^9"}}`), "pnpm-lock.yaml": pnpm9Lock, "package-lock.json": npmLock},
			manager: "pnpm", reason: "devEngines in package.json selects pnpm"},
		{name: "a declaration naming a manager without its lockfile", files: map[string]string{"package.json": leftPad(`,"packageManager":"npm@10.9.2"`), "pnpm-lock.yaml": pnpm9Lock},
			manager: "pnpm", findings: []string{"package_manager_declaration_conflict"}},
		{name: "no lockfile installs with npm unfrozen", files: map[string]string{"package.json": leftPad("")},
			manager: "npm", install: "npm install --no-audit --no-fund", findings: []string{"dependencies_unpinned"}},
		{name: "no lockfile follows the declaration", files: map[string]string{"package.json": leftPad(`,"packageManager":"pnpm@10.18.0"`)},
			manager: "pnpm", install: "pnpm install --no-frozen-lockfile --config.dangerously-allow-all-builds=true"},
		{name: "no lockfile follows Bun's own files", files: map[string]string{"package.json": leftPad(""), "bunfig.toml": "[install]\n"},
			manager: "bun", install: "bun install"},
		{name: "an operator's choice needs its lockfile", files: map[string]string{"package.json": leftPad(""), "bun.lock": bunLock}, selected: "pnpm",
			refused: "the build uses pnpm, but the source has no pnpm lockfile; bun.lock is committed — choose Bun or the lockfile in the build settings"},
		{name: "an operator's choice without any lockfile", files: map[string]string{"package.json": leftPad("")}, selected: "yarn",
			manager: "yarn", install: "yarn install"},
		{name: "a stale chosen lockfile installs unfrozen", selected: "npm", files: map[string]string{
			"package.json": leftPad(""), "bun.lock": bunLock, "package-lock.json": `{"lockfileVersion":3,"packages":{"":{}}}`},
			manager: "npm", install: "npm install --no-audit --no-fund", findings: []string{"lockfile_out_of_sync"}},
		{name: "a single stale lockfile installs unfrozen", files: map[string]string{
			"package.json": leftPad(""), "pnpm-lock.yaml": strings.Replace(pnpm9Lock, "^1.3.0", "^1.2.0", 1)},
			manager: "pnpm", install: "pnpm install --no-frozen-lockfile --config.dangerously-allow-all-builds=true", findings: []string{"lockfile_out_of_sync"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := readNodeTree(t, test.files, "")
			plan := planNodeInstall(source.facts, nodeInstallChoice{selected: test.selected, build: "npm run build", start: "npm run start"})
			if test.refused != "" {
				if plan.blocked == nil || !strings.Contains(plan.blocked.Measured, test.refused) {
					t.Fatalf("blocked = %+v, want %q", plan.blocked, test.refused)
				}
				if !errors.Is(plan.blockedError(), ErrUnsupportedBuilder) {
					t.Fatal("a blocked plan must refuse the build as an unsupported builder")
				}
				return
			}
			if plan.blocked != nil || plan.manager != test.manager || (test.install != "" && plan.installLine() != test.install) ||
				(test.reason != "" && !strings.Contains(plan.reason, test.reason)) {
				t.Fatalf("plan = manager %q install %q reason %q blocked %+v", plan.manager, plan.installLine(), plan.reason, plan.blocked)
			}
			for _, code := range test.findings {
				if findingByCode(plan.findings, code) == nil {
					t.Fatalf("findings %+v lack %s", plan.findings, code)
				}
			}
		})
	}
}

func prepareNode(t *testing.T, files map[string]string, config BuildPlanConfig) PreparedBuild {
	t.Helper()
	config.Method, config.Recipe = BuildRecipe, "node"
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), writeNodeTree(t, files), config, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func assertDockerfile(t *testing.T, dockerfile string, want, absent []string) {
	t.Helper()
	for _, line := range want {
		if !strings.Contains(dockerfile, line) {
			t.Fatalf("Dockerfile missing %q:\n%s", line, dockerfile)
		}
	}
	for _, line := range absent {
		if strings.Contains(dockerfile, line) {
			t.Fatalf("Dockerfile has %q:\n%s", line, dockerfile)
		}
	}
}

// What the recipe renders for each manager shape. Every pnpm and Berry
// release is exact and installed once into a toolchain stage both the build
// and the runtime start from, so a start command that runs the manager
// works offline; Yarn 1 uses its real frozen flag; Bun is copied beside Node.
func TestNodeRecipeRendersEachManagersInstall(t *testing.T) {
	t.Parallel()
	server := BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npm run start"}
	for _, test := range []struct {
		name      string
		files     map[string]string
		config    BuildPlanConfig
		want      []string
		absent    []string
		toolchain string
	}{
		{
			name: "pnpm without packageManager pins the lockfile's release", files: map[string]string{"package.json": leftPad(""), "pnpm-lock.yaml": pnpm9Lock},
			config: server,
			want: []string{
				"FROM node:22-alpine@sha256:", " AS toolchain",
				"ENV COREPACK_HOME=/opt/corepack COREPACK_ENABLE_DOWNLOAD_PROMPT=0 COREPACK_ENABLE_AUTO_PIN=0\n",
				"RUN corepack enable pnpm && corepack install -g pnpm@10.34.5\n",
				"FROM toolchain AS build\nWORKDIR /app\nCOPY . .\nRUN pnpm install --frozen-lockfile --config.dangerously-allow-all-builds=true\n",
				nodeBuildRun("pnpm run build\n"),
				"FROM toolchain\nWORKDIR /app\nENV NODE_ENV=production\nENV COREPACK_ENABLE_NETWORK=0\nENV PATH=/app/node_modules/.bin:$PATH\n",
				`CMD ["/bin/sh","-c","pnpm run start"]`,
			},
			absent:    []string{"corepack enable &&", "COREPACK_ENABLE_STRICT"},
			toolchain: "pnpm 10.34.5 (lockfileVersion 9.0)",
		},
		{
			name: "a pnpm 8 lockfile installs with pnpm 8", files: map[string]string{"package.json": leftPad(""), "pnpm-lock.yaml": pnpm6Lock},
			config: server, want: []string{"corepack install -g pnpm@8.15.9", "RUN pnpm install --frozen-lockfile\n"},
			absent: []string{"dangerously-allow-all-builds"}, toolchain: "pnpm 8.15.9 (lockfileVersion 6.0)",
		},
		{
			name: "packageManager's exact release and hash", files: map[string]string{
				"package.json":   leftPad(`,"packageManager":"pnpm@9.15.9+sha512.68046141893c66fad01c079231128e9afb89ef87e2691d69e4d40eee228988295fd4682181bae55b58418c3a253bde65a505ec7c5f9403ece5cc3cd37dcf2531"`),
				"pnpm-lock.yaml": pnpm9Lock},
			config:    server,
			want:      []string{"corepack install -g pnpm@9.15.9+sha512.68046141893c66fad01c079231128e9afb89ef87e2691d69e4d40eee228988295fd4682181bae55b58418c3a253bde65a505ec7c5f9403ece5cc3cd37dcf2531\n"},
			toolchain: "pnpm 9.15.9 (packageManager)",
		},
		{
			name: "a declared build policy is respected", files: map[string]string{
				"package.json": leftPad(`,"pnpm":{"onlyBuiltDependencies":["esbuild"]}`), "pnpm-lock.yaml": pnpm9Lock},
			config: server, want: []string{"RUN pnpm install --frozen-lockfile\n"}, absent: []string{"dangerously-allow-all-builds"},
		},
		{
			name: "a conflicting declaration lets Corepack run the committed manager", files: map[string]string{
				"package.json": leftPad(`,"packageManager":"npm@10.9.2"`), "pnpm-lock.yaml": pnpm9Lock},
			config: server, want: []string{"COREPACK_ENABLE_STRICT=0", "corepack install -g pnpm@10.34.5"},
		},
		{
			name: "Yarn 1 installs frozen with its own flag", files: map[string]string{"package.json": leftPad(""), "yarn.lock": yarnClassic},
			config: server, want: []string{"FROM node:22-alpine@sha256:", " AS build\n", "RUN yarn install --frozen-lockfile\n", nodeBuildRun("yarn run build\n")},
			absent: []string{"--immutable", "corepack", "AS toolchain"}, toolchain: "yarn 1.22 (classic, bundled with the Node image)",
		},
		{
			name: "Berry with packageManager keeps Plug'n'Play for yarn commands", files: map[string]string{
				"package.json": leftPad(`,"packageManager":"yarn@4.9.2"`), "yarn.lock": yarnBerryLock("8")},
			config: BuildPlanConfig{BuildCommand: "yarn run build", StartCommand: "yarn run start"},
			want: []string{"RUN corepack enable yarn && corepack install -g yarn@4.9.2\n", "ENV YARN_ENABLE_GLOBAL_CACHE=false\nRUN yarn install --immutable\n",
				"ENV COREPACK_ENABLE_NETWORK=0"},
			absent: []string{"YARN_NODE_LINKER"}, toolchain: "yarn 4.9.2 (packageManager)",
		},
		{
			name: "Berry links node_modules for a start outside yarn", files: map[string]string{
				"package.json": leftPad(`,"packageManager":"yarn@4.9.2"`), "yarn.lock": yarnBerryLock("8")},
			config: BuildPlanConfig{BuildCommand: "yarn run build", StartCommand: "node dist/index.js"},
			want:   []string{"RUN YARN_NODE_LINKER=node-modules yarn install --immutable\n"},
		},
		{
			name: "Berry's node-modules linker needs no adjustment", files: map[string]string{
				"package.json": leftPad(`,"packageManager":"yarn@4.9.2"`), "yarn.lock": yarnBerryLock("8"), ".yarnrc.yml": "nodeLinker: node-modules\n"},
			config: BuildPlanConfig{BuildCommand: "yarn run build", StartCommand: "node dist/index.js"},
			want:   []string{"RUN yarn install --immutable\n"}, absent: []string{"YARN_NODE_LINKER"},
		},
		{
			name: "Berry without a declaration uses the release that writes its lock", files: map[string]string{"package.json": leftPad(""), "yarn.lock": yarnBerryLock("6")},
			config: BuildPlanConfig{BuildCommand: "yarn run build", StartCommand: "yarn run start"},
			want:   []string{"corepack install -g yarn@3.8.7", "RUN yarn install --immutable\n"}, toolchain: "yarn 3.8.7 (lockfile metadata version 6)",
		},
		{
			name: "Berry's committed release needs no other", files: map[string]string{
				"package.json": leftPad(""), "yarn.lock": yarnBerryLock("8"), ".yarnrc.yml": "yarnPath: .yarn/releases/yarn-4.9.2.cjs\n",
				".yarn/releases/yarn-4.9.2.cjs": "//"},
			config: BuildPlanConfig{BuildCommand: "yarn run build", StartCommand: "yarn run start"},
			want:   []string{"RUN yarn install --immutable\n"}, absent: []string{"corepack"}, toolchain: "yarn from .yarnrc.yml yarnPath (.yarn/releases/yarn-4.9.2.cjs)",
		},
		{
			name: "an unknown Berry lock format updates with the newest reviewed Yarn", files: map[string]string{"package.json": leftPad(""), "yarn.lock": yarnBerryLock("9")},
			config: BuildPlanConfig{BuildCommand: "yarn run build", StartCommand: "yarn run start"},
			want:   []string{"corepack install -g yarn@4.18.0", "RUN yarn install --no-immutable\n"},
		},
		{
			name: "Bun installs beside Node at the declared release", files: map[string]string{"package.json": leftPad(`,"packageManager":"bun@1.2.21"`), "bun.lock": bunLock},
			config: server,
			want: []string{"FROM node:22-alpine@sha256:", "COPY --from=oven/bun:1.2.21-alpine@sha256:", " /usr/local/bin/bun /usr/local/bin/bun\nRUN ln -s bun /usr/local/bin/bunx\n",
				"RUN bun install --frozen-lockfile\n", nodeBuildRun("bun run build\n"), `CMD ["/bin/sh","-c","bun run start"]`},
			absent: []string{"FROM oven/bun"}, toolchain: "bun 1.2.21 (packageManager)",
		},
		{
			name: "declared trustedDependencies still run the scripts the app loads", files: map[string]string{
				"package.json": `{"name":"svc","scripts":{"start":"node index.js"},"dependencies":{"better-sqlite3":"^12.0.0"},"trustedDependencies":["esbuild"]}`,
				"bun.lock":     `{"lockfileVersion":1,"workspaces":{"":{"dependencies":{"better-sqlite3":"^12.0.0"}}},"packages":{"better-sqlite3":["better-sqlite3@12.4.1","",{},"x"]}}`},
			config: BuildPlanConfig{StartCommand: "bun run start"},
			want:   []string{"RUN bun install --frozen-lockfile && bun pm trust better-sqlite3\n"},
		},
		{
			name: "a lock written with --legacy-peer-deps installs the same way", files: map[string]string{
				"package.json":      `{"name":"svc","scripts":{"start":"node index.js"},"dependencies":{"react":"19.1.0","react-helmet-async":"2.0.5"}}`,
				"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"dependencies":{"react":"19.1.0","react-helmet-async":"2.0.5"}},"node_modules/react":{"version":"19.1.0"},"node_modules/react-helmet-async":{"version":"2.0.5","peerDependencies":{"react":"^16.6.0 || ^17.0.0 || ^18.0.0"}}}}`},
			config: BuildPlanConfig{StartCommand: "npm run start"},
			want:   []string{"RUN npm ci --legacy-peer-deps\n"},
		},
		{
			name: "an .npmrc that already relaxes peers needs no flag", files: map[string]string{
				".npmrc":            "legacy-peer-deps=true\n",
				"package.json":      `{"name":"svc","scripts":{"start":"node index.js"},"dependencies":{"react":"19.1.0","react-helmet-async":"2.0.5"}}`,
				"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"dependencies":{"react":"19.1.0","react-helmet-async":"2.0.5"}},"node_modules/react":{"version":"19.1.0"},"node_modules/react-helmet-async":{"version":"2.0.5","peerDependencies":{"react":"^18.0.0"}}}}`},
			config: BuildPlanConfig{StartCommand: "npm run start"},
			want:   []string{"RUN npm ci\n"},
		},
		{
			name: "a platform binary the lock forgot is added at its exact version", files: map[string]string{
				"package.json":      `{"name":"site","scripts":{"build":"vite build"},"devDependencies":{"vite":"6.0.0"}}`,
				"package-lock.json": `{"lockfileVersion":3,"packages":{"":{"devDependencies":{"vite":"6.0.0"}},"node_modules/vite":{"version":"6.0.0"},"node_modules/rollup":{"version":"4.40.0","optionalDependencies":{"@rollup/rollup-linux-x64-musl":"4.40.0","@rollup/rollup-linux-arm64-musl":"4.40.0"}}}}`},
			config: BuildPlanConfig{BuildCommand: "npm run build", OutputDirectory: "dist", TargetPlatform: "linux/amd64"},
			want:   []string{"RUN npm ci && npm install --no-save --no-audit --no-fund @rollup/rollup-linux-x64-musl@4.40.0\n"},
			absent: []string{"arm64-musl"},
		},
		{
			name: "no lockfile installs rather than refuses", files: map[string]string{"package.json": leftPad("")},
			config: server, want: []string{"RUN npm install --no-audit --no-fund\n", nodeBuildRun("npm run build\n")},
		},
		{
			name: "scripts that call bunx get Bun under npm", files: map[string]string{
				"package.json":      `{"name":"svc","scripts":{"build":"bunx tsc","start":"node dist/index.js"},"dependencies":{"left-pad":"^1.3.0"}}`,
				"package-lock.json": npmLock},
			config: server, want: []string{"COPY --from=oven/bun:1-alpine@sha256:", "RUN npm ci\n"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared := prepareNode(t, test.files, test.config)
			assertDockerfile(t, prepared.DockerfilePreview, test.want, test.absent)
			if test.toolchain != "" && prepared.Toolchain != test.toolchain {
				t.Fatalf("toolchain = %q, want %q", prepared.Toolchain, test.toolchain)
			}
			if prepared.Install == "" || !strings.Contains(prepared.DockerfilePreview, prepared.Install) {
				t.Fatalf("recorded install %q is not what the Dockerfile runs", prepared.Install)
			}
		})
	}
}

// A saved command still naming another manager's runner runs through the
// resolved manager, and the run log says so: the commands were detected
// for an npm lockfile that a later commit replaced with bun.lock.
func TestPrepareMovesSavedCommandsToTheResolvedManager(t *testing.T) {
	t.Parallel()
	prepared := prepareNode(t, map[string]string{"package.json": leftPad(""), "bun.lock": bunLock},
		BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "npx prisma migrate deploy && npm run start"})
	if prepared.BuildCommand != "bun run build" || prepared.StartCommand != "bunx prisma migrate deploy && bun run start" {
		t.Fatalf("prepared commands = %q / %q", prepared.BuildCommand, prepared.StartCommand)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{nodeBuildRun("bun run build\n"), `"bunx prisma migrate deploy \u0026\u0026 bun run start"`}, []string{"npm run"})
	if !slices.Contains(prepared.Notes, "Build command runs with bun: `bun run build` (saved: `npm run build`)") {
		t.Fatalf("notes = %v", prepared.Notes)
	}
	// An operator's own command is theirs.
	custom := prepareNode(t, map[string]string{"package.json": leftPad(""), "bun.lock": bunLock},
		BuildPlanConfig{BuildCommand: "npm run build -- --prod", StartCommand: "node dist/index.js"})
	if custom.BuildCommand != "" || custom.StartCommand != "" {
		t.Fatalf("custom commands were rewritten: %q / %q", custom.BuildCommand, custom.StartCommand)
	}
}

func TestNodeRecipeRefusesWhatNoStageProvides(t *testing.T) {
	t.Parallel()
	builder := NewArtifactBuilder(&artifactBackendFake{})
	for _, test := range []struct {
		name   string
		files  map[string]string
		config BuildPlanConfig
		want   string
	}{
		{name: "deno", files: map[string]string{"package.json": leftPad(""), "package-lock.json": npmLock},
			config: BuildPlanConfig{BuildCommand: "npm run build", StartCommand: "deno run main.ts"}, want: "deno"},
		{name: "a declared pnpm that cannot read the lock", files: map[string]string{
			"package.json": leftPad(`,"packageManager":"pnpm@10.18.0"`), "pnpm-lock.yaml": pnpm6Lock},
			config: BuildPlanConfig{BuildCommand: "pnpm run build", StartCommand: "pnpm run start"}, want: "pnpm 10.18.0; pnpm-lock.yaml lockfileVersion 6.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.config.Method, test.config.Recipe = BuildRecipe, "node"
			_, err := builder.Prepare(context.Background(), writeNodeTree(t, test.files), test.config, false, "t:1")
			if !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

// A declared Bun release without an alpine image falls back to the newest
// 1.x image rather than failing the build on a registry lookup.
type missingBunImageBackend struct{ artifactBackendFake }

func (b *missingBunImageBackend) ResolveImage(ctx context.Context, reference, auth string) (ResolvedImage, error) {
	if reference == "oven/bun:1.0.2-alpine" {
		return ResolvedImage{}, errors.New("manifest unknown")
	}
	return b.artifactBackendFake.ResolveImage(ctx, reference, auth)
}

func TestDeclaredBunReleaseWithoutAnImageFallsBack(t *testing.T) {
	t.Parallel()
	prepared, err := NewArtifactBuilder(&missingBunImageBackend{}).Prepare(context.Background(),
		writeNodeTree(t, map[string]string{"package.json": leftPad(`,"packageManager":"bun@1.0.2"`), "bun.lock": bunLock}),
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "bun run start"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{"COPY --from=oven/bun:1-alpine@sha256:"}, []string{"1.0.2"})
	if !slices.ContainsFunc(prepared.Notes, func(note string) bool { return strings.Contains(note, "installing with oven/bun:1-alpine") }) {
		t.Fatalf("notes = %v", prepared.Notes)
	}
}

// A repository's own .dockerignore can exclude the lockfile or the registry
// configuration the install reads. The generated Dockerfile gets its own
// ignore file: the repository's rules, less the ones that would leave out a
// root input the recipe reads, then exceptions for install inputs below the
// root, then the dashboard's own exclusions.
func TestGeneratedDockerignoreKeepsInstallInputs(t *testing.T) {
	t.Parallel()
	root := writeNodeTree(t, map[string]string{
		"package.json": leftPad(""), "bun.lock": bunLock, ".npmrc": "save-exact=true\n", "bunfig.toml": "[install]\n",
		".yarn/releases/yarn.cjs": "", ".dockerignore": "node_modules\n*.lock\n.npmrc\n.env\n*.toml\n.yarn\n",
	})
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", StartCommand: "bun run start"}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	ignore, err := os.ReadFile(filepath.Join(root, ".just-dashboard", "Dockerfile.dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	rules := parseDockerignore(ignore)
	for path, excluded := range map[string]bool{
		"package.json": false, "bun.lock": false, ".npmrc": false, "bunfig.toml": false,
		".env": true, "node_modules/x/index.js": true, "other.toml": true,
	} {
		if got, _ := dockerignoreExcludes(rules, path); got != excluded {
			t.Errorf("%s excluded = %v, want %v:\n%s", path, got, excluded, ignore)
		}
	}
	if !strings.Contains(string(ignore), "!.yarn/releases/**\n") {
		t.Fatalf("the Yarn release directory is not brought back:\n%s", ignore)
	}
	for _, want := range []string{".dockerignore excludes bun.lock", ".dockerignore excludes .npmrc", ".dockerignore excludes bunfig.toml"} {
		if !slices.ContainsFunc(prepared.Notes, func(note string) bool { return strings.HasPrefix(note, want) }) {
			t.Fatalf("notes = %v, want %q", prepared.Notes, want)
		}
	}
}

// A workspace member builds from its workspace root: the context widens to
// the root that holds the lockfile, the install runs there, and the build
// and the server run in the member's directory.
func TestWorkspaceMemberBuildsFromItsWorkspaceRoot(t *testing.T) {
	t.Parallel()
	boundary := writeNodeTree(t, map[string]string{
		"package.json":             `{"name":"root","private":true,"devDependencies":{"turbo":"2.5.0"}}`,
		"pnpm-workspace.yaml":      "packages:\n  - apps/*\n  - packages/*\n",
		"apps/web/package.json":    `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.1.3","ui":"workspace:*"}}`,
		"packages/ui/package.json": `{"name":"ui"}`,
		"pnpm-lock.yaml":           "lockfileVersion: '9.0'\n\nimporters:\n\n  .:\n    devDependencies:\n      turbo:\n        specifier: 2.5.0\n        version: 2.5.0\n\n  apps/web:\n    dependencies:\n      next:\n        specifier: 16.1.3\n        version: 16.1.3\n      ui:\n        specifier: workspace:*\n        version: link:../../packages/ui\n\n  packages/ui: {}\n",
	})
	result, err := (Detector{}).DetectPath(context.Background(), boundary, SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(result.Candidates, func(candidate DetectedCandidate) bool { return candidate.Root == "apps/web" })
	if index < 0 {
		t.Fatalf("candidates = %+v", result.Candidates)
	}
	member := result.Candidates[index]
	if member.PackageManager != "pnpm" || member.BuildCommand != "pnpm exec turbo run build --filter=web..." || member.Confidence != ConfidenceHigh ||
		!slices.ContainsFunc(member.Evidence, func(evidence DetectionEvidence) bool {
			return evidence.Reason == "installed from the workspace lockfile at ."
		}) {
		t.Fatalf("member = manager %q build %q confidence %q evidence %+v", member.PackageManager, member.BuildCommand, member.Confidence, member.Evidence)
	}

	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(context.Background(), boundary, filepath.Join(boundary, "apps", "web"),
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/web", BuildCommand: member.BuildCommand, StartCommand: member.StartCommand}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ContextDirectory != "." {
		t.Fatalf("context = %q", prepared.ContextDirectory)
	}
	if _, err := os.Stat(filepath.Join(boundary, ".just-dashboard", "Dockerfile")); err != nil {
		t.Fatalf("the Dockerfile is not at the workspace root: %v", err)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{
		"FROM toolchain AS build\nWORKDIR /app\nCOPY . .\nRUN pnpm install --frozen-lockfile --config.dangerously-allow-all-builds=true\nWORKDIR /app/apps/web\n",
		"ENV PATH=/app/apps/web/node_modules/.bin:/app/node_modules/.bin:$PATH",
		nodeBuildRun("pnpm exec turbo run build --filter=web...\n"),
		"FROM toolchain\nWORKDIR /app/apps/web\n", "COPY --from=build /app /app",
	}, nil)
}

// npm and Yarn 1 workspaces refer to a sibling by name and a plain range,
// not workspace:; a Turborepo member that depends on one still builds it
// first. The workspace's link entries are neither Git dependencies nor
// downloads, and the member's install is named before Deploy.
func TestNPMWorkspaceMemberBuildsItsSiblingsWithTurbo(t *testing.T) {
	t.Parallel()
	boundary := writeNodeTree(t, map[string]string{
		"package.json":                 `{"name":"root","private":true,"workspaces":["apps/*","packages/*"],"devDependencies":{"turbo":"2.5.0"}}`,
		"apps/web/package.json":        `{"name":"web","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.1.3","@acme/shared":"*"}}`,
		"apps/docs/package.json":       `{"name":"docs","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16.1.3"}}`,
		"packages/shared/package.json": `{"name":"@acme/shared","version":"1.0.0","scripts":{"build":"tsc"}}`,
		"package-lock.json": `{"name":"root","lockfileVersion":3,"requires":true,"packages":{` +
			`"":{"name":"root","workspaces":["apps/*","packages/*"],"devDependencies":{"turbo":"2.5.0"}},` +
			`"apps/web":{"name":"web","dependencies":{"next":"16.1.3","@acme/shared":"*"}},` +
			`"apps/docs":{"name":"docs","dependencies":{"next":"16.1.3"}},` +
			`"packages/shared":{"name":"@acme/shared","version":"1.0.0"},` +
			`"node_modules/web":{"resolved":"apps/web","link":true},"node_modules/docs":{"resolved":"apps/docs","link":true},` +
			`"node_modules/@acme/shared":{"resolved":"packages/shared","link":true},` +
			`"node_modules/next":{"version":"16.1.3","resolved":"https://registry.npmjs.org/next/-/next-16.1.3.tgz"},` +
			`"node_modules/turbo":{"version":"2.5.0","resolved":"https://registry.npmjs.org/turbo/-/turbo-2.5.0.tgz"}}}`,
	})
	result, err := (Detector{}).DetectPath(context.Background(), boundary, SourceIdentity{Kind: SourceGit, Revision: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	candidate := func(root string) DetectedCandidate {
		index := slices.IndexFunc(result.Candidates, func(candidate DetectedCandidate) bool { return candidate.Root == root })
		if index < 0 {
			t.Fatalf("candidates = %+v", result.Candidates)
		}
		return result.Candidates[index]
	}
	web, docs := candidate("apps/web"), candidate("apps/docs")
	if web.PackageManager != "npm" || web.BuildCommand != "npx turbo run build --filter=web..." || docs.BuildCommand != "npm run build" {
		t.Fatalf("web = %q %q, docs = %q", web.PackageManager, web.BuildCommand, docs.BuildCommand)
	}
	install := web.NodeInstalls[slices.IndexFunc(web.NodeInstalls, func(install DetectedNodeInstall) bool { return install.Manager == "npm" })]
	if install.Install != "npm ci" || findingByCode(install.Findings, "git_dependencies") != nil {
		t.Fatalf("install = %+v", install)
	}
	if workspace := findingByCode(install.Findings, "workspace_lockfile"); workspace == nil || workspace.Severity != PreflightPass ||
		workspace.Title != "Installed from the workspace lockfile at ." || workspace.Measured != "package-lock.json records apps/web" {
		t.Fatalf("workspace finding = %+v", workspace)
	}
	if runner := findingByCode(install.Findings, "runtime_runner_available"); runner == nil || runner.Measured != "npm: bundled with Node 22" {
		t.Fatalf("runtime runner = %+v", runner)
	}
	if err := validateDetectedNodeInstall(web); err != nil {
		t.Fatal(err)
	}

	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(context.Background(), boundary, filepath.Join(boundary, "apps", "web"),
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/web", BuildCommand: web.BuildCommand, StartCommand: web.StartCommand}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	assertDockerfile(t, prepared.DockerfilePreview, []string{"RUN npm ci\nWORKDIR /app/apps/web\n", nodeBuildRun("npx turbo run build --filter=web...\n")}, []string{"apk add"})
}

// A member directory the recipe cannot write unquoted into WORKDIR, ENV and
// RUN lines is refused by name before Deploy and at build, never rendered
// into a Dockerfile BuildKit cannot parse.
func TestWorkspaceMemberPathMustBePlain(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json":             `{"name":"root","private":true,"workspaces":["apps/*"]}`,
		"apps/my web/package.json": `{"name":"web","scripts":{"build":"tsc","start":"node dist/index.js"},"dependencies":{"left-pad":"^1.3.0"}}`,
		"package-lock.json":        `{"lockfileVersion":3,"packages":{"":{"name":"root","workspaces":["apps/*"]},"apps/my web":{"name":"web","dependencies":{"left-pad":"^1.3.0"}},"node_modules/left-pad":{"version":"1.3.0"}}}`,
	}
	source := readNodeTree(t, files, "apps/my web")
	plan := planNodeInstall(source.facts, nodeInstallChoice{build: "npm run build", start: "npm run start"})
	if plan.blocked == nil || plan.blocked.Code != "workspace_member_path_unsupported" || plan.blocked.FieldID != "configuration.build.rootDirectory" {
		t.Fatalf("plan = %+v", plan.blocked)
	}
	boundary := writeNodeTree(t, files)
	_, err := NewArtifactBuilder(&artifactBackendFake{}).PrepareWithin(context.Background(), boundary, filepath.Join(boundary, "apps", "my web"),
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", RootDirectory: "apps/my web", BuildCommand: "npm run build", StartCommand: "npm run start"}, false, "t:1")
	if !errors.Is(err, ErrUnsupportedBuilder) || !strings.Contains(err.Error(), "apps/my web is not a plain path") {
		t.Fatalf("prepare = %v", err)
	}
	if !nodeMemberPathRE.MatchString("packages/@scope/ui+v2") || nodeMemberPathRE.MatchString("apps/$HOME") {
		t.Fatal("the member path rule is not the documented one")
	}
}

// nodeRunnerFor is the Go side of the configure form's
// withPackageManagerRunner; these rows are the ones deployment-defaults
// tests hold it to.
func TestNodeRunnerForMatchesTheConfigureForm(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ command, manager, want string }{
		{"npm run build", "bun", "bun run build"},
		{"bun run start", "npm", "npm run start"},
		{"prisma generate && next build", "bun", "prisma generate && next build"},
		{"npm run build", "", "npm run build"},
		{"yarn build", "bun", "yarn build"},
		{"npx prisma migrate deploy && npm run start", "bun", "bunx prisma migrate deploy && bun run start"},
		{"bunx prisma db push && bun run start", "pnpm", "pnpm exec prisma db push && pnpm run start"},
		{"yarn knex migrate:latest && yarn run start", "npm", "npx knex migrate:latest && npm run start"},
		{"pnpm exec drizzle-kit migrate && node dist/index.js", "yarn", "yarn drizzle-kit migrate && node dist/index.js"},
		{"npm start", "bun", "bun run start"},
		{"yarn start", "yarn", "yarn start"},
		{"yarn test", "pnpm", "pnpm run test"},
		{"NODE_ENV=production npm run start", "bun", "NODE_ENV=production bun run start"},
		{"bun test", "npm", "bun test"},
		{"npm run build --if-present", "bun", "npm run build --if-present"},
	} {
		if got := nodeRunnerFor(test.command, test.manager); got != test.want {
			t.Errorf("nodeRunnerFor(%q, %q) = %q, want %q", test.command, test.manager, got, test.want)
		}
	}
}

func TestNodeCommandToolsFollowScriptsTheCommandsRun(t *testing.T) {
	t.Parallel()
	scripts := map[string]string{
		"prebuild":    "cross-env NODE_ENV=production pnpm run codegen",
		"build":       "next build",
		"codegen":     "bunx prisma generate",
		"postinstall": "dotenv -e .env -- node scripts/setup.js",
		"start":       "next start",
		"lint":        "deno lint",
	}
	tools := nodeCommandTools(scripts, []string{"npm run build", "npm start"})
	for _, want := range []string{"npm", "pnpm", "bunx", "next", "node"} {
		if !tools[want] {
			t.Fatalf("tools = %v, missing %s", tools, want)
		}
	}
	if tools["deno"] || tools["cross-env"] || tools["dotenv"] {
		t.Fatalf("tools = %v include what the commands never run", tools)
	}
	if got := nodeInstallSegments("npm ci && npm install && yarn && pnpm install --frozen-lockfile && bun add zod"); !slices.Equal(got, []string{"npm install", "yarn", "bun add zod"}) {
		t.Fatalf("install segments = %v", got)
	}
}
