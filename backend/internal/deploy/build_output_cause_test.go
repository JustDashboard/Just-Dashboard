package deploy

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// buildCase is one failed build as BuildKit streams it: the failed step's
// own lines (numbered #10 below), what buildx reported, and the cause the
// dashboard must name. The lines are the tools' own words, from their
// sources and from fixture builds run while writing the table.
type buildCase struct {
	name        string
	lines       []string
	command     string
	exit        int
	reason      string
	instruction string
	build       BuildPlanConfig
	prepared    PreparedBuild
	candidate   *causeCandidate
	variables   []ReleaseVariableSnapshot
	want        BuildCause
}

var (
	nodeBuild       = BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", StartCommand: "npm run start"}
	bunPrepared     = PreparedBuild{Recipe: "node", DockerfilePreview: "FROM oven/bun@sha256:1 AS build\nRUN bun install --frozen-lockfile\nRUN npm run build\n"}
	npmPrepared     = PreparedBuild{Recipe: "node", DockerfilePreview: "FROM node@sha256:1 AS build\nRUN npm ci\nRUN npm run build\n"}
	pythonBuild     = BuildPlanConfig{Method: BuildRecipe, Recipe: "python", PythonVersion: "3.13", StartCommand: "gunicorn app:app"}
	goBuild         = BuildPlanConfig{Method: BuildRecipe, Recipe: "go", GoVersion: "1.25"}
	rustBuild       = BuildPlanConfig{Method: BuildRecipe, Recipe: "rust"}
	dockerfileBuild = BuildPlanConfig{Method: BuildDockerfile, Dockerfile: "Dockerfile"}
	inSyncBunLock   = &causeCandidate{Recipe: "node", Lockfiles: []struct {
		Path    string   `json:"path"`
		Manager string   `json:"manager"`
		State   string   `json:"state"`
		Missing []string `json:"missing"`
	}{
		{Path: "bun.lock", Manager: "bun", State: "in_sync"},
		{Path: "package-lock.json", Manager: "npm", State: "stale", Missing: []string{"is-odd"}},
	}}
)

func buildCases() []buildCase {
	return []buildCase{
		// The incident: npm ci over a stale package-lock.json beside an in-sync bun.lock.
		{
			name: "npm lockfile out of sync", command: "npm ci", exit: 1, build: nodeBuild, prepared: npmPrepared, candidate: inSyncBunLock,
			lines: []string{
				"npm error code EUSAGE", "npm error",
				"npm error `npm ci` can only install packages when your package.json and package-lock.json or npm-shrinkwrap.json are in sync. Please update your lock file with `npm install` before continuing.",
				"npm error Missing: is-odd@3.0.1 from lock file", "npm error Missing: @prisma/adapter-pg@7.0.0 from lock file",
				"npm error Clean install a project",
			},
			want: BuildCause{
				Code: "build_lockfile_out_of_sync", Phase: phaseInstall, Command: "npm ci", ExitCode: 1, Detail: "package-lock.json",
				Subjects: []string{"is-odd", "@prisma/adapter-pg"},
				Fix:      &CauseFix{Kind: fixSetBuild, Field: "configuration.build.packageManager", Value: "bun"},
			},
		},
		{
			name: "bun frozen lockfile", command: "bun install --frozen-lockfile", exit: 1, build: nodeBuild,
			lines: []string{"bun install v1.4.2", "error: lockfile had changes, but lockfile is frozen", "note: try re-running without --frozen-lockfile and commit the updated lockfile"},
			want:  BuildCause{Code: "build_lockfile_out_of_sync", Phase: phaseInstall, Command: "bun install --frozen-lockfile", ExitCode: 1, Detail: "bun.lock"},
		},
		{
			name: "pnpm outdated lockfile", command: "corepack enable && pnpm install --frozen-lockfile", exit: 1, build: nodeBuild,
			lines: []string{` ERR_PNPM_OUTDATED_LOCKFILE  Cannot install with "frozen-lockfile" because pnpm-lock.yaml is not up to date with <ROOT>/package.json`},
			want:  BuildCause{Code: "build_lockfile_out_of_sync", Phase: phaseInstall, Command: "corepack enable && pnpm install --frozen-lockfile", ExitCode: 1, Detail: "pnpm-lock.yaml"},
		},
		{
			name: "Yarn Berry immutable", command: "corepack enable && yarn install --immutable", exit: 1, build: nodeBuild,
			lines: []string{"➤ YN0028: │ The lockfile would have been modified by this install, which is explicitly forbidden."},
			want:  BuildCause{Code: "build_lockfile_out_of_sync", Phase: phaseInstall, Command: "corepack enable && yarn install --immutable", ExitCode: 1, Detail: "yarn.lock"},
		},
		{
			name: "Yarn 1 frozen lockfile", command: "yarn install --frozen-lockfile", exit: 1, build: nodeBuild,
			lines: []string{"error Your lockfile needs to be updated, but yarn was run with `--frozen-lockfile`."},
			want:  BuildCause{Code: "build_lockfile_out_of_sync", Phase: phaseInstall, Command: "yarn install --frozen-lockfile", ExitCode: 1, Detail: "yarn.lock"},
		},
		{
			name: "pnpm lockfile from another release", command: "corepack enable && pnpm install --frozen-lockfile", exit: 1, build: nodeBuild,
			lines: []string{" ERR_PNPM_LOCKFILE_BREAKING_CHANGE  Lockfile /app/pnpm-lock.yaml not compatible with current pnpm"},
			want:  BuildCause{Code: "build_lockfile_incompatible", Phase: phaseInstall, Command: "corepack enable && pnpm install --frozen-lockfile", ExitCode: 1, Detail: "pnpm-lock.yaml"},
		},
		{
			name: "pnpm ignored build scripts", command: "corepack enable && pnpm install --frozen-lockfile", exit: 1, build: nodeBuild,
			lines: []string{" ERR_PNPM_IGNORED_BUILDS  Ignored build scripts: esbuild, sharp."},
			want: BuildCause{Code: "build_lifecycle_script_blocked", Phase: phaseInstall, Command: "corepack enable && pnpm install --frozen-lockfile", ExitCode: 1,
				Detail: "pnpm", Subjects: []string{"esbuild", "sharp"}},
		},
		{
			name: "Bun blocked postinstall then Prisma", command: "bun run build", exit: 1, build: nodeBuild,
			lines: []string{"Blocked 2 postinstalls. Run `bun pm untrusted` for details.", `Error: @prisma/client did not initialize yet. Please run "prisma generate" and try to import it again.`},
			want:  BuildCause{Code: "build_lifecycle_script_blocked", Phase: phaseBuild, Command: "bun run build", ExitCode: 1, Detail: "bun"},
		},
		{
			name: "corepack names another manager", command: "corepack enable && pnpm install --frozen-lockfile", exit: 1, build: nodeBuild,
			lines: []string{`UsageError: This project is configured to use yarn because /app/package.json has a "packageManager" field`},
			want: BuildCause{Code: "build_package_manager_mismatch", Phase: phaseInstall, Command: "corepack enable && pnpm install --frozen-lockfile", ExitCode: 1,
				Subjects: []string{"yarn"}, Fix: &CauseFix{Kind: fixSetBuild, Field: "configuration.build.packageManager", Value: "yarn"}},
		},
		{
			name: "ERESOLVE", command: "npm ci", exit: 1, build: nodeBuild,
			lines: []string{"npm error code ERESOLVE", "npm error ERESOLVE unable to resolve dependency tree", "npm error Found: react@19.0.0", `npm error peer react@"^18.0.0" from some-lib@2.0.0`},
			want:  BuildCause{Code: "build_dependency_conflict", Phase: phaseInstall, Command: "npm ci", ExitCode: 1, Detail: "npm", Subjects: []string{"react"}},
		},
		{
			name: "private scope without a token", command: "npm ci", exit: 1, build: nodeBuild,
			lines: []string{"npm error code E404", "npm error 404 Not Found - GET https://registry.npmjs.org/@acme%2fui - Not found", "npm error 404  '@acme/ui@^1.0.0' is not in this registry."},
			want:  BuildCause{Code: "build_dependency_unavailable", Phase: phaseInstall, Command: "npm ci", ExitCode: 1, Detail: "npm", Subjects: []string{"@acme/ui"}},
		},
		{
			name: "registry token refused", command: "npm ci", exit: 1, build: nodeBuild,
			lines: []string{"npm error code E401", "npm error Unable to authenticate, your authentication token seems to be invalid."},
			want:  BuildCause{Code: "build_registry_auth", Phase: phaseInstall, Command: "npm ci", ExitCode: 1},
		},
		{
			name: "npm on the Bun image", command: "npm run build", exit: 127, build: nodeBuild, prepared: bunPrepared,
			lines: []string{"/bin/sh: npm: not found"},
			want: BuildCause{Code: "build_command_not_found", Phase: phaseBuild, Command: "npm run build", ExitCode: 127, Subjects: []string{"npm"},
				Fix: &CauseFix{Kind: fixSetBuild, Field: "configuration.build.buildCommand", Value: "bun run build"}},
		},
		{
			name: "engine-strict", command: "npm ci", exit: 1, build: nodeBuild,
			lines: []string{"npm error code EBADENGINE", "npm error engine Unsupported engine", "npm error engine Not compatible with your version of node/npm: next@16.0.0"},
			want:  BuildCause{Code: "build_runtime_version", Phase: phaseInstall, Command: "npm ci", ExitCode: 1, Detail: "node", Subjects: []string{"next@16.0.0"}},
		},
		{
			name: "Next needs a newer Node", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{`You are using Node.js 18.17.0. For Next.js, Node.js version ">=20.9.0" is required.`},
			want:  BuildCause{Code: "build_runtime_version", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Detail: "node", Subjects: []string{">=20.9.0"}},
		},
		{
			name: "node-gyp without Python", command: "npm ci", exit: 1, build: nodeBuild,
			lines: []string{"npm error gyp info using node-gyp@10.2.0", "npm error gyp ERR! find Python", "npm error gyp ERR! find Python Python is not set from command line or npm configuration"},
			want:  BuildCause{Code: "build_native_toolchain_missing", Phase: phaseInstall, Command: "npm ci", ExitCode: 1, Detail: "node-gyp", Subjects: []string{"Python"}},
		},
		{
			name: "module not found", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{"Error: Cannot find module 'autoprefixer'", "Require stack:", "- /app/postcss.config.js"},
			want:  BuildCause{Code: "build_module_not_found", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Subjects: []string{"autoprefixer"}},
		},
		{
			name: "Prisma engine at build on Alpine", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{"prisma:warn Prisma failed to detect the libssl/openssl version to use, and may not work as expected. Defaulting to \"openssl-1.1.x\"."},
			want:  BuildCause{Code: "build_system_library_missing", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Detail: "prisma", Subjects: []string{"openssl"}},
		},
		{
			name: "Prisma 7 config reads DATABASE_URL", command: "npm run build", exit: 1, build: nodeBuild,
			variables: []ReleaseVariableSnapshot{{Name: "DATABASE_URL", Scopes: "runtime"}},
			lines:     []string{"Failed to load config file \"/app/prisma.config.ts\" as a TypeScript/JavaScript module. Error: PrismaConfigEnvError: Missing required environment variable: DATABASE_URL"},
			want: BuildCause{Code: "build_env_missing", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Detail: "prisma", Subjects: []string{"DATABASE_URL"},
				Fix: &CauseFix{Kind: fixVariableScope, Field: "variables.DATABASE_URL", Value: "build", Scope: "build"}},
		},
		{
			name: "SvelteKit static env", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{`"PUBLIC_API_URL" is not exported by "$env/static/public", imported by "src/routes/+page.svelte"`},
			want: BuildCause{Code: "build_env_missing", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Detail: "sveltekit", Subjects: []string{"PUBLIC_API_URL"},
				Fix: &CauseFix{Kind: fixAddVariable, Field: "variables.PUBLIC_API_URL", Scope: "build"}},
		},
		{
			name: "Node heap", command: "npm run build", exit: 134, build: nodeBuild,
			lines: []string{"<--- Last few GCs --->", "FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory"},
			want: BuildCause{Code: "build_out_of_memory", Phase: phaseBuild, Command: "npm run build", ExitCode: 134, Detail: "node",
				Fix: &CauseFix{Kind: fixAddVariable, Field: "variables.NODE_OPTIONS", Value: "--max-old-space-size=3072", Scope: "build"}},
		},
		{
			name: "killed by the kernel", command: "cargo build --release --locked", exit: 137, build: rustBuild,
			lines: []string{"   Compiling serde v1.0.210"},
			want:  BuildCause{Code: "build_out_of_memory", Phase: phaseBuild, Command: "cargo build --release --locked", ExitCode: 137},
		},
		{
			name: "prerender reaches for the database", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{
				"Error occurred prerendering page \"/blog\". Read more: https://nextjs.org/docs/messages/prerender-error",
				"PrismaClientKnownRequestError: Can't reach database server at `db-4.jd.internal:5432`",
			},
			want: BuildCause{Code: "build_database_unreachable", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Detail: "next", Subjects: []string{"/blog"}},
		},
		{
			name: "build reaches for a linked database", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{"Error: getaddrinfo ENOTFOUND db-3.jd.internal", "    at GetAddrInfoReqWrap.onlookupall [as oncomplete] (node:dns:120:26)"},
			want:  BuildCause{Code: "build_database_unreachable", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Subjects: []string{"db-3.jd.internal"}},
		},
		{
			name: "a dependency's install script", command: "npm ci", exit: 1, build: nodeBuild,
			lines: []string{"npm error code 1", "npm error path /app/node_modules/@scope/native-addon", "npm error command failed", "npm error command sh -c node install.js"},
			want:  BuildCause{Code: "build_install_script_failed", Phase: phaseInstall, Command: "npm ci", ExitCode: 1, Detail: "npm", Subjects: []string{"@scope/native-addon"}},
		},
		{
			name: "a Yarn Berry build script", command: "corepack enable && yarn install --immutable", exit: 1, build: nodeBuild,
			lines: []string{"➤ YN0009: │ sharp@npm:0.33.5 couldn't be built successfully (exit code 1, logs can be found here: /tmp/xfs-1/build.log)"},
			want: BuildCause{Code: "build_install_script_failed", Phase: phaseInstall, Command: "corepack enable && yarn install --immutable", ExitCode: 1,
				Detail: "yarn", Subjects: []string{"sharp"}},
		},
		{
			name: "prerender throws", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{"Error occurred prerendering page \"/about\". Read more: https://nextjs.org/docs/messages/prerender-error", "TypeError: Cannot read properties of undefined"},
			want:  BuildCause{Code: "build_prerender_failed", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Detail: "next", Subjects: []string{"/about"}},
		},
		{
			name: "TypeScript", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{"Failed to compile.", "./src/app/page.tsx:12:5", "Type error: Property 'title' does not exist on type 'Props'."},
			want:  BuildCause{Code: "build_type_error", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Detail: "typescript", Subjects: []string{"src/app/page.tsx"}},
		},
		{
			name: "webpack 4 on Node 22", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{"Error: error:0308010C:digital envelope routines::unsupported"},
			want: BuildCause{Code: "build_legacy_openssl", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Detail: "node",
				Fix: &CauseFix{Kind: fixAddVariable, Field: "variables.NODE_OPTIONS", Value: "--openssl-legacy-provider", Scope: "build"}},
		},
		{
			name: "rollup native package left out of the lockfile", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{"Error: Cannot find module @rollup/rollup-linux-x64-musl. npm has a bug related to optional dependencies (https://github.com/npm/cli/issues/4828)."},
			want:  BuildCause{Code: "build_platform_binary_missing", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Subjects: []string{"@rollup/rollup-linux-x64-musl"}},
		},
		{
			name: "Prisma Client not generated", command: "npm run build", exit: 1, build: nodeBuild, prepared: npmPrepared,
			lines: []string{`Error: @prisma/client did not initialize yet. Please run "prisma generate" and try to import it again.`},
			want: BuildCause{Code: "build_prisma_client_missing", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Detail: "prisma",
				Fix: &CauseFix{Kind: fixSetBuild, Field: "configuration.build.buildCommand", Value: "npx prisma generate && npm run build"}},
		},
		{
			name: "framework output guard", command: "test -f /app/.next/BUILD_ID || (echo 'Next.js must produce .next/BUILD_ID; configure its output and start command together' >&2; exit 1)",
			exit: 1, build: nodeBuild,
			lines: []string{"Next.js must produce .next/BUILD_ID; configure its output and start command together"},
			want: BuildCause{Code: "build_output_missing", Phase: phaseOutputCheck, ExitCode: 1, Subjects: []string{".next/BUILD_ID"},
				Command: "test -f /app/.next/BUILD_ID || (echo 'Next.js must produce .next/BUILD_ID; configure its output and start command together' >&2; exit 1)",
				Fix:     &CauseFix{Kind: fixReview, Field: "configuration.build.startCommand"}},
		},
		{
			name: "static output directory", exit: -1, instruction: "COPY --from=build /app/dist/ /usr/share/nginx/html/",
			build:     BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", OutputDirectory: "dist"},
			candidate: &causeCandidate{OutputDirectory: "build"},
			reason:    `failed to compute cache key: failed to calculate checksum of ref a::b: "/app/dist": not found`,
			lines:     []string{`failed to calculate checksum of ref a::b: "/app/dist": not found`},
			want: BuildCause{Code: "build_output_missing", Phase: phaseOutputCheck, ExitCode: -1, Subjects: []string{"/app/dist"},
				Fix: &CauseFix{Kind: fixSetBuild, Field: "configuration.build.outputDirectory", Value: "build"}},
		},
		{
			name: "Dockerfile COPY source", exit: -1, instruction: "COPY missing.txt /x", build: dockerfileBuild,
			reason: `failed to compute cache key: failed to calculate checksum of ref a::b: "/missing.txt": not found`,
			lines:  []string{`failed to calculate checksum of ref a::b: "/missing.txt": not found`},
			want:   BuildCause{Code: "build_copy_source_missing", Phase: phaseDockerfile, ExitCode: -1, Subjects: []string{"/missing.txt"}},
		},
		{
			name: "Dockerfile does not parse", exit: -1, build: dockerfileBuild,
			reason: "dockerfile parse error on line 1: unknown instruction: FROMM (did you mean FROM?)",
			want:   BuildCause{Code: "build_dockerfile_invalid", Phase: phaseDockerfile, ExitCode: -1},
		},
		{
			name: "private or mistyped FROM", exit: -1, build: dockerfileBuild,
			reason: "nodee:22: failed to resolve source metadata for docker.io/library/nodee:22: pull access denied, repository does not exist or may require authorization: server message: insufficient_scope: authorization failed",
			want:   BuildCause{Code: "build_base_image_missing", Phase: phaseBaseImage, ExitCode: -1, Subjects: []string{"docker.io/library/nodee:22"}},
		},
		{
			name: "registry rate limit", exit: -1, build: dockerfileBuild,
			reason: "node:22: failed to resolve source metadata for docker.io/library/node:22: toomanyrequests: You have reached your pull rate limit.",
			want:   BuildCause{Code: "build_registry_rate_limited", Phase: phaseBaseImage, ExitCode: -1},
		},

		// Python.
		{
			name: "psycopg2 without libpq", command: "pip install --no-cache-dir -r requirements.txt", exit: 1, build: pythonBuild,
			lines: []string{"      Error: pg_config executable not found.", "      pg_config is required to build psycopg2 from source."},
			want:  BuildCause{Code: "build_system_library_missing", Phase: phaseInstall, Command: "pip install --no-cache-dir -r requirements.txt", ExitCode: 1, Detail: "python", Subjects: []string{"pg_config"}},
		},
		{
			name: "no compiler for a wheel", command: "pip install --no-cache-dir -r requirements.txt", exit: 1, build: pythonBuild,
			lines: []string{"      error: command 'gcc' failed: No such file or directory", "  ERROR: Failed building wheel for uwsgi"},
			want:  BuildCause{Code: "build_native_toolchain_missing", Phase: phaseInstall, Command: "pip install --no-cache-dir -r requirements.txt", ExitCode: 1, Detail: "python", Subjects: []string{"gcc"}},
		},
		{
			name: "no distribution", command: "pip install --no-cache-dir -r requirements.txt", exit: 1, build: pythonBuild,
			lines: []string{"ERROR: Could not find a version that satisfies the requirement tensorflow==2.99 (from versions: none)", "ERROR: No matching distribution found for tensorflow==2.99"},
			want:  BuildCause{Code: "build_dependency_unavailable", Phase: phaseInstall, Command: "pip install --no-cache-dir -r requirements.txt", ExitCode: 1, Detail: "python", Subjects: []string{"tensorflow==2.99"}},
		},
		{
			name: "resolution impossible", command: "pip install --no-cache-dir -r requirements.txt", exit: 1, build: pythonBuild,
			lines: []string{"ERROR: Cannot install flask==3.0.0 and werkzeug==2.0.0 because these package versions have conflicting dependencies.", "ERROR: ResolutionImpossible: for help visit https://pip.pypa.io/en/latest/topics/dependency-resolution/"},
			want:  BuildCause{Code: "build_dependency_conflict", Phase: phaseInstall, Command: "pip install --no-cache-dir -r requirements.txt", ExitCode: 1, Detail: "python"},
		},
		{
			name: "requires an older Python", command: "pip install --no-cache-dir -r requirements.txt", exit: 1, build: pythonBuild,
			lines: []string{"ERROR: Package 'legacy-app' requires a different Python: 3.13.1 not in '<3.12,>=3.10'"},
			want: BuildCause{Code: "build_runtime_version", Phase: phaseInstall, Command: "pip install --no-cache-dir -r requirements.txt", ExitCode: 1, Detail: "python",
				Subjects: []string{"<3.12,>=3.10"}, Fix: &CauseFix{Kind: fixSetBuild, Field: "configuration.build.pythonVersion", Value: "3.11"}},
		},
		{
			name: "Poetry lock stale", command: "poetry install --only main --no-root", exit: 1, build: pythonBuild,
			lines: []string{"pyproject.toml changed significantly since poetry.lock was last generated. Run `poetry lock` to fix the lock file."},
			want:  BuildCause{Code: "build_lockfile_out_of_sync", Phase: phaseInstall, Command: "poetry install --only main --no-root", ExitCode: 1, Detail: "poetry.lock"},
		},
		{
			name: "uv lock stale", command: "uv sync --locked --no-dev", exit: 2, build: pythonBuild,
			lines: []string{"error: The lockfile at `uv.lock` needs to be updated, but `--locked` was provided. To update the lockfile, run `uv lock`."},
			want:  BuildCause{Code: "build_lockfile_out_of_sync", Phase: phaseInstall, Command: "uv sync --locked --no-dev", ExitCode: 2, Detail: "uv.lock"},
		},
		{
			name: "requirements from a conda environment", command: "pip install --no-cache-dir -r requirements.txt", exit: 1, build: pythonBuild,
			lines: []string{"ERROR: Could not install packages due to an OSError: [Errno 2] No such file or directory: '/croot/certifi_1690232220950/work'"},
			want:  BuildCause{Code: "build_dependency_local_path", Phase: phaseInstall, Command: "pip install --no-cache-dir -r requirements.txt", ExitCode: 1, Detail: "python", Subjects: []string{"/croot/certifi_1690232220950/work"}},
		},
		{
			name: "git+ requirement without git", command: "pip install --no-cache-dir -r requirements.txt", exit: 1, build: pythonBuild,
			lines: []string{"ERROR: Cannot find command 'git' - do you have 'git' installed and in your PATH?"},
			want:  BuildCause{Code: "build_command_not_found", Phase: phaseInstall, Command: "pip install --no-cache-dir -r requirements.txt", ExitCode: 1, Subjects: []string{"git"}},
		},
		{
			name: "Django collectstatic without a secret", command: "python manage.py collectstatic --noinput", exit: 1,
			build: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", BuildCommand: "python manage.py collectstatic --noinput"},
			lines: []string{"django.core.exceptions.ImproperlyConfigured: Set the SECRET_KEY environment variable"},
			want: BuildCause{Code: "build_env_missing", Phase: phaseBuild, Command: "python manage.py collectstatic --noinput", ExitCode: 1, Detail: "django",
				Subjects: []string{"SECRET_KEY"}, Fix: &CauseFix{Kind: fixAddVariable, Field: "variables.SECRET_KEY", Scope: "build"}},
		},

		// Go, Rust, Java and .NET.
		{
			name: "go.sum entry", command: "go build -trimpath -ldflags='-s -w' -o /out/app .", exit: 1, build: goBuild,
			lines: []string{"main.go:5:2: missing go.sum entry for module providing package github.com/gin-gonic/gin (imported by example.com/app); to add:"},
			want: BuildCause{Code: "build_lockfile_out_of_sync", Phase: phaseBuild, Command: "go build -trimpath -ldflags='-s -w' -o /out/app .", ExitCode: 1,
				Detail: "go.sum", Subjects: []string{"github.com/gin-gonic/gin"}},
		},
		{
			name: "go.mod needs a newer Go", command: "go mod download", exit: 1, build: goBuild,
			lines: []string{"go: go.mod requires go >= 1.26.1 (running go 1.25.4; GOTOOLCHAIN=local)"},
			want: BuildCause{Code: "build_runtime_version", Phase: phaseInstall, Command: "go mod download", ExitCode: 1, Detail: "go",
				Subjects: []string{"1.26.1"}, Fix: &CauseFix{Kind: fixSetBuild, Field: "configuration.build.goVersion", Value: "1.26"}},
		},
		{
			name: "go:embed of an unbuilt frontend", command: "go build -trimpath -ldflags='-s -w' -o /out/app .", exit: 1, build: goBuild,
			lines: []string{"main.go:7:12: pattern web/dist: no matching files found"},
			want:  BuildCause{Code: "build_embed_source_missing", Phase: phaseBuild, Command: "go build -trimpath -ldflags='-s -w' -o /out/app .", ExitCode: 1, Detail: "go", Subjects: []string{"web/dist"}},
		},
		{
			name: "cgo without a compiler", command: "go build -trimpath -ldflags='-s -w' -o /out/app .", exit: 1, build: goBuild,
			lines: []string{"# runtime/cgo", `cgo: C compiler "gcc" not found: exec: "gcc": executable file not found in $PATH`},
			want:  BuildCause{Code: "build_native_toolchain_missing", Phase: phaseBuild, Command: "go build -trimpath -ldflags='-s -w' -o /out/app .", ExitCode: 1, Detail: "go", Subjects: []string{"gcc"}},
		},
		{
			name: "Go compile error", command: "go build -trimpath -ldflags='-s -w' -o /out/app .", exit: 1, build: goBuild,
			lines: []string{"# example.com/app", "./main.go:12:2: undefined: handler"},
			want:  BuildCause{Code: "build_compile_error", Phase: phaseBuild, Command: "go build -trimpath -ldflags='-s -w' -o /out/app .", ExitCode: 1, Detail: "go", Subjects: []string{"main.go"}},
		},
		{
			name: "Cargo.lock stale", command: "cargo fetch --locked", exit: 101, build: rustBuild,
			lines: []string{"error: the lock file /src/Cargo.lock needs to be updated but --locked was passed to prevent this"},
			want:  BuildCause{Code: "build_lockfile_out_of_sync", Phase: phaseInstall, Command: "cargo fetch --locked", ExitCode: 101, Detail: "Cargo.lock"},
		},
		{
			name: "openssl-sys", command: "cargo build --release --locked", exit: 101, build: rustBuild,
			lines: []string{"error: failed to run custom build command for `openssl-sys v0.9.103`"},
			want:  BuildCause{Code: "build_system_library_missing", Phase: phaseBuild, Command: "cargo build --release --locked", ExitCode: 101, Detail: "rust", Subjects: []string{"openssl-sys"}},
		},
		{
			name: "sqlx without a database", command: "cargo build --release --locked", exit: 101, build: rustBuild,
			lines: []string{"error: set `DATABASE_URL` to use query macros online, or run `cargo sqlx prepare` to update the query cache"},
			want: BuildCause{Code: "build_sqlx_offline", Phase: phaseBuild, Command: "cargo build --release --locked", ExitCode: 101, Detail: "sqlx",
				Subjects: []string{"DATABASE_URL"}, Fix: &CauseFix{Kind: fixAddVariable, Field: "variables.SQLX_OFFLINE", Value: "true", Scope: "build"}},
		},
		{
			name: "rustc too old", command: "cargo build --release --locked", exit: 101, build: rustBuild,
			lines: []string{"error: package `foo v1.2.0` cannot be built because it requires rustc 1.85 or newer, while the currently active rustc version is 1.84.0"},
			want:  BuildCause{Code: "build_runtime_version", Phase: phaseBuild, Command: "cargo build --release --locked", ExitCode: 101, Detail: "rust", Subjects: []string{"1.85"}},
		},
		{
			name: "Rust compile error", command: "cargo build --release --locked", exit: 101, build: rustBuild,
			lines: []string{"error[E0425]: cannot find value `x` in this scope"},
			want:  BuildCause{Code: "build_compile_error", Phase: phaseBuild, Command: "cargo build --release --locked", ExitCode: 101, Detail: "rust", Subjects: []string{"E0425"}},
		},
		{
			name: "Maven target release", command: "mvn -q -B -DskipTests package", exit: 1, build: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"},
			lines: []string{"[ERROR] Failed to execute goal org.apache.maven.plugins:maven-compiler-plugin:3.13.0:compile (default-compile) on project app: Fatal error compiling: error: invalid target release: 25 -> [Help 1]"},
			want:  BuildCause{Code: "build_runtime_version", Phase: phaseBuild, Command: "mvn -q -B -DskipTests package", ExitCode: 1, Detail: "java", Subjects: []string{"25"}},
		},
		{
			name: "Gradle wrapper jar", command: "./gradlew --no-daemon bootJar", exit: 1, build: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"},
			lines: []string{"Error: Could not find or load main class org.gradle.wrapper.GradleWrapperMain"},
			want:  BuildCause{Code: "build_wrapper_missing", Phase: phaseBuild, Command: "./gradlew --no-daemon bootJar", ExitCode: 1, Detail: "gradle", Subjects: []string{"gradle-wrapper.jar"}},
		},
		{
			name: "gradlew not executable", command: "./gradlew --no-daemon bootJar", exit: 126, build: BuildPlanConfig{Method: BuildRecipe, Recipe: "java"},
			lines: []string{"/bin/sh: ./gradlew: Permission denied"},
			want:  BuildCause{Code: "build_permission", Phase: phaseBuild, Command: "./gradlew --no-daemon bootJar", ExitCode: 126, Subjects: []string{"./gradlew"}},
		},
		{
			name: "NETSDK1045", command: "dotnet publish -c Release -o /out", exit: 1, build: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"},
			lines: []string{"/usr/share/dotnet/sdk/8.0.404/Sdks/Microsoft.NET.Sdk/targets/Microsoft.NET.TargetFrameworkInference.targets(166,5): error NETSDK1045: The current .NET SDK does not support targeting .NET 10.0.  Either target .NET 8.0 or lower, or use a version of the .NET SDK that supports .NET 10.0."},
			want:  BuildCause{Code: "build_runtime_version", Phase: phaseBuild, Command: "dotnet publish -c Release -o /out", ExitCode: 1, Detail: "dotnet", Subjects: []string{"10.0"}},
		},
		{
			name: "C# compile error", command: "dotnet publish -c Release -o /out", exit: 1, build: BuildPlanConfig{Method: BuildRecipe, Recipe: "dotnet"},
			lines: []string{"/src/Program.cs(3,1): error CS0246: The type or namespace name 'Foo' could not be found"},
			want:  BuildCause{Code: "build_compile_error", Phase: phaseBuild, Command: "dotnet publish -c Release -o /out", ExitCode: 1, Detail: "dotnet", Subjects: []string{"CS0246"}},
		},

		// PHP, Deno, static generators and the languages without a recipe.
		{
			name: "PHP extension", command: "composer install --no-dev --optimize-autoloader --no-interaction --prefer-dist", exit: 2, build: BuildPlanConfig{Method: BuildRecipe, Recipe: "php"},
			lines: []string{"    - laravel/framework[v11.0.0, ..., v11.9.2] require ext-intl * -> it is missing from your system. Install or enable PHP's intl extension."},
			want: BuildCause{Code: "build_php_extension_missing", Phase: phaseInstall, Command: "composer install --no-dev --optimize-autoloader --no-interaction --prefer-dist", ExitCode: 2,
				Detail: "php", Subjects: []string{"ext-intl"}},
		},
		{
			name: "PHP release", command: "composer install --no-dev --optimize-autoloader --no-interaction --prefer-dist", exit: 2, build: BuildPlanConfig{Method: BuildRecipe, Recipe: "php"},
			lines: []string{"    - Root composer.json requires php ^8.4 but your php version (8.3.12) does not satisfy that requirement."},
			want: BuildCause{Code: "build_runtime_version", Phase: phaseInstall, Command: "composer install --no-dev --optimize-autoloader --no-interaction --prefer-dist", ExitCode: 2,
				Detail: "php", Subjects: []string{"^8.4"}},
		},
		{
			name: "Symfony dev bundle", command: "composer install --no-dev --optimize-autoloader --no-interaction --prefer-dist", exit: 1, build: BuildPlanConfig{Method: BuildRecipe, Recipe: "php"},
			lines: []string{`PHP Fatal error:  Uncaught Symfony\Component\Debug\Exception\ClassNotFoundException: Attempted to load class "DebugBundle" from namespace "Symfony\Bundle\DebugBundle".`},
			want: BuildCause{Code: "build_dev_dependency_in_production", Phase: phaseInstall, Command: "composer install --no-dev --optimize-autoloader --no-interaction --prefer-dist", ExitCode: 1,
				Detail: "symfony", Subjects: []string{"DebugBundle"}},
		},
		{
			name: "composer.lock stale", command: "composer install --no-dev --optimize-autoloader --no-interaction --prefer-dist", exit: 4, build: BuildPlanConfig{Method: BuildRecipe, Recipe: "php"},
			lines: []string{`  - Required package "laravel/sanctum" is not present in the lock file.`},
			want: BuildCause{Code: "build_lockfile_out_of_sync", Phase: phaseInstall, Command: "composer install --no-dev --optimize-autoloader --no-interaction --prefer-dist", ExitCode: 4,
				Detail: "composer.lock", Subjects: []string{"laravel/sanctum"}},
		},
		{
			name: "Deno lockfile", command: "deno install --frozen", exit: 1, build: BuildPlanConfig{Method: BuildRecipe, Recipe: "deno"},
			lines: []string{"error: The lockfile is out of date. Run `deno install --frozen=false`, or rerun with `--frozen=false` to update it."},
			want:  BuildCause{Code: "build_lockfile_out_of_sync", Phase: phaseInstall, Command: "deno install --frozen", ExitCode: 1, Detail: "deno.lock"},
		},
		{
			name: "Hugo extended", command: "hugo --minify", exit: 1, build: dockerfileBuild,
			lines: []string{`Error: error building site: TOCSS: failed to transform "scss/main.scss" (text/x-scss). Check your Hugo installation; you need the extended version to build SCSS/SASS with transpiler LIBSASS.`},
			want:  BuildCause{Code: "build_hugo_extended_required", Phase: phaseDockerfile, Command: "hugo --minify", ExitCode: 1, Detail: "hugo"},
		},
		{
			name: "Gemfile.lock without Linux", command: "bundle install", exit: 5, build: dockerfileBuild,
			lines: []string{`Your bundle only supports platforms ["arm64-darwin-23"] but your local platform is x86_64-linux. Add the current platform to the lockfile with`},
			want:  BuildCause{Code: "build_bundle_platform_missing", Phase: phaseDockerfile, Command: "bundle install", ExitCode: 5, Detail: "ruby", Subjects: []string{"arm64-darwin-23"}},
		},
		{
			name: "Ruby release", command: "bundle install", exit: 18, build: dockerfileBuild,
			lines: []string{"Your Ruby version is 3.3.9, but your Gemfile specified 3.3.6"},
			want:  BuildCause{Code: "build_runtime_version", Phase: phaseDockerfile, Command: "bundle install", ExitCode: 18, Detail: "ruby", Subjects: []string{"3.3.6"}},
		},
		{
			name: "Ruby tool in a Node image", command: "npm run build", exit: 127, build: nodeBuild,
			lines: []string{"sh: 1: bundle: not found"},
			want:  BuildCause{Code: "build_command_not_found", Phase: phaseBuild, Command: "npm run build", ExitCode: 127, Subjects: []string{"bundle"}},
		},

		// The network and the host.
		{
			name: "DNS during install", command: "npm ci", exit: 1, build: nodeBuild,
			lines: []string{"npm error code EAI_AGAIN", "npm error request to https://registry.npmjs.org/next failed, reason: getaddrinfo EAI_AGAIN registry.npmjs.org"},
			want:  BuildCause{Code: "build_network", Phase: phaseInstall, Command: "npm ci", ExitCode: 1, Subjects: []string{"registry.npmjs.org"}},
		},
		{
			name: "disk full", command: "npm ci", exit: 1, build: nodeBuild,
			lines: []string{"npm error code ENOSPC", "npm error syscall write", "npm error nospc ENOSPC: no space left on device, write"},
			want:  BuildCause{Code: "build_disk_full", Phase: phaseInstall, Command: "npm ci", ExitCode: 1},
		},
		{
			name: "script with Windows line endings", command: "./build.sh", exit: 127, build: dockerfileBuild,
			lines: []string{`/usr/bin/env: 'bash\r': No such file or directory`},
			want:  BuildCause{Code: "build_script_crlf", Phase: phaseDockerfile, Command: "./build.sh", ExitCode: 127},
		},
		{
			name: "missing script", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{`npm error Missing script: "build"`},
			want: BuildCause{Code: "build_script_missing", Phase: phaseBuild, Command: "npm run build", ExitCode: 1, Subjects: []string{"build"},
				Fix: &CauseFix{Kind: fixReview, Field: "configuration.build.buildCommand"}},
		},
		{
			name: "wrong root", command: "npm run build", exit: 1, build: nodeBuild,
			lines: []string{"Error: > Couldn't find any `pages` or `app` directory. Please create one under the project root"},
			want: BuildCause{Code: "build_wrong_root", Phase: phaseBuild, Command: "npm run build", ExitCode: 1,
				Fix: &CauseFix{Kind: fixReview, Field: "configuration.build.rootDirectory"}},
		},
		{
			name: "nothing recognisable", command: "npm run build", exit: 2, build: nodeBuild,
			lines: []string{"> app@1.0.0 build", "> node scripts/build.js", "it did not work"},
			want:  BuildCause{Code: "build_failed", Phase: phaseBuild, Command: "npm run build", ExitCode: 2},
		},
	}
}

// feedBuild streams a case through the collector the way BuildKit prints it:
// a step header, the step's numbered output, its ERROR line, and the closing
// replay without step numbers.
func feedBuild(test buildCase) (*buildOutputCollector, error) {
	seq := int64(100)
	collector := newBuildOutputCollector(func() int64 { return seq })
	emit := func(text string) {
		seq++
		collector.observe(BuildLog{Stream: "stderr", Text: text})
	}
	emit("#0 building with \"default\" instance using docker driver")
	emit("#9 [2/5] WORKDIR /app")
	emit("#9 DONE 0.0s")
	instruction := test.instruction
	if instruction == "" && test.command != "" {
		instruction = "RUN " + test.command
	}
	if instruction != "" {
		emit("#10 [4/5] " + instruction)
	}
	for _, line := range test.lines {
		emit("#10 1.303 " + line)
	}
	failure := &dockerx.BuildError{Command: test.command, ExitCode: test.exit, Reason: test.reason, BuildxExit: 1}
	if instruction != "" {
		failure.Step, failure.Instruction = 10, instruction
		if test.command != "" {
			emit(fmt.Sprintf("#10 ERROR: process %q did not complete successfully: exit code: %d", "/bin/sh -c "+test.command, test.exit))
		} else if test.reason != "" {
			emit("#10 ERROR: " + test.lines[0])
		}
	}
	emit("------")
	emit(" > [4/5] " + instruction + ":")
	for _, line := range test.lines {
		emit("1.303 " + line)
	}
	emit("------")
	if test.command == "" {
		failure.ExitCode = -1
	}
	return collector, failure
}

func TestBuildFailureCauseNamesEveryKnownFailure(t *testing.T) {
	t.Parallel()
	for _, test := range buildCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			collector, err := feedBuild(test)
			cause := buildFailureCause(err, collector, causeContext{
				build: test.build, prepared: test.prepared, candidate: test.candidate,
				variables: test.variables, hostMemory: 4 << 30,
			}, nil)
			if cause == nil {
				t.Fatal("no cause")
			}
			if cause.LineSeq == 0 && test.want.Code != "build_failed" && test.want.Code != "build_dockerfile_invalid" && test.reason == "" {
				t.Fatalf("cause has no line: %+v", cause)
			}
			got := *cause
			got.LineSeq = 0
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("cause = %+v\nwant    %+v", got, test.want)
			}
			sentence := cause.sentence()
			if len(sentence) > causeSentenceLength || !strings.HasSuffix(sentence, ".") || strings.Contains(sentence, "\n") {
				t.Fatalf("sentence = %q", sentence)
			}
			if causeTitle(cause.Code) == "" {
				t.Fatalf("%s has no title", cause.Code)
			}
		})
	}
}

func TestBuildFailureCausePointsAtTheDecisiveLineOnce(t *testing.T) {
	t.Parallel()
	test := buildCases()[0]
	collector, err := feedBuild(test)
	cause := buildFailureCause(err, collector, causeContext{build: nodeBuild}, nil)
	// feedBuild numbers lines from 101: the header lines take 101-103, the
	// step header 104, and the EUSAGE sentence is the step's third line.
	if cause.LineSeq != 107 {
		t.Fatalf("line = %d, want the step's own line rather than the replay", cause.LineSeq)
	}
	if lines := collector.stepLines(10); len(lines) != len(test.lines) {
		t.Fatalf("step kept %d lines, want %d: the replay and the ERROR line must not be added", len(lines), len(test.lines))
	}
}

func TestBuildFailureCauseReadsOnlyWhatTheBuildPrinted(t *testing.T) {
	t.Parallel()
	// The failing step names a registry refusal; the Dockerfile excerpt in
	// BuildKit's closing block carries the output guard's own text, which
	// must not be read as the build having failed its output check.
	seq := int64(0)
	collector := newBuildOutputCollector(func() int64 { seq++; return seq })
	for _, line := range []string{
		"#10 [4/5] RUN npm ci",
		"#10 2.1 npm error code E401",
		`#10 ERROR: process "/bin/sh -c npm ci" did not complete successfully: exit code: 1`,
		"Dockerfile:6",
		"--------------------",
		"   5 |     RUN npm ci",
		"   6 | >>> RUN test -f /app/.next/BUILD_ID || (echo 'Next.js must produce .next/BUILD_ID' >&2; exit 1)",
		"--------------------",
		`ERROR: failed to build: failed to solve: process "/bin/sh -c test -f /app/.next/BUILD_ID || (echo 'Next.js must produce .next/BUILD_ID' >&2; exit 1)" did not complete successfully: exit code: 1`,
	} {
		collector.observe(BuildLog{Stream: "stderr", Text: line})
	}
	for _, line := range collector.stream.lines {
		if strings.Contains(line.text, "must produce") || strings.Contains(line.text, "did not complete") {
			t.Fatalf("the plan's own text entered the stream: %q", line.text)
		}
	}
	cause := buildFailureCause(&dockerx.BuildError{Step: 99, Command: "npm ci", ExitCode: 1}, collector, causeContext{build: nodeBuild}, nil)
	if cause.Code != "build_registry_auth" {
		t.Fatalf("cause = %+v", cause)
	}
}

func TestBuildFailureCauseRedactsTheCommand(t *testing.T) {
	t.Parallel()
	collector := newBuildOutputCollector(nil)
	redact := buildRedactor(map[string]string{"TOKEN": "hunter2-secret"})
	cause := buildFailureCause(&dockerx.BuildError{Command: "curl -H 'x: hunter2-secret' https://example.test", ExitCode: 22},
		collector, causeContext{build: dockerfileBuild}, redact)
	if strings.Contains(cause.Command, "hunter2") || strings.Contains(cause.sentence(), "hunter2") {
		t.Fatalf("the command kept a variable value: %q", cause.Command)
	}
	long := strings.Repeat("x", 400)
	cause = buildFailureCause(&dockerx.BuildError{Command: long, ExitCode: 1}, collector, causeContext{build: dockerfileBuild}, nil)
	if len(cause.Command) > causeCommandLength+len("…") || !strings.HasSuffix(cause.Command, "…") {
		t.Fatalf("command kept %d bytes", len(cause.Command))
	}
}

func TestBuildFailureCauseNamesTimeoutsPullsAndComposeServices(t *testing.T) {
	t.Parallel()
	collector := newBuildOutputCollector(nil)
	for _, line := range []string{
		"#8 [build 3/6] RUN cargo fetch --locked", "#8 DONE 40.2s",
		"#9 [build 4/6] RUN --mount=type=secret,id=TOKEN,env=TOKEN,required=true cargo build --release --locked",
		"#9 1799.2    Compiling tokio v1.40.0",
	} {
		collector.observe(BuildLog{Stream: "stderr", Text: line})
	}
	timeout := buildFailureCause(&dockerx.BuildTimeoutError{}, collector, causeContext{build: rustBuild}, nil)
	if timeout.Code != "build_timeout" || timeout.Command != "cargo build --release --locked" || timeout.Phase != phaseBuild {
		t.Fatalf("timeout = %+v", timeout)
	}
	if sentence := timeout.sentence(); !strings.Contains(sentence, "still running when the build reached its 30-minute limit") ||
		strings.Contains(sentence, "cancel") {
		t.Fatalf("timeout sentence = %q", sentence)
	}

	pull := buildFailureCause(errors.New("pull image: toomanyrequests: You have reached your pull rate limit."),
		newBuildOutputCollector(nil), causeContext{build: BuildPlanConfig{Method: BuildImage}}, nil)
	if pull == nil || pull.Code != "build_registry_rate_limited" || pull.Phase != phasePull {
		t.Fatalf("pull = %+v", pull)
	}
	if cause := buildFailureCause(fmt.Errorf("%w: build variable X is unavailable", ErrArtifactMissing),
		newBuildOutputCollector(nil), causeContext{build: nodeBuild}, nil); cause != nil {
		t.Fatalf("a plan refusal was named as a build cause: %+v", cause)
	}

	compose := newBuildOutputCollector(nil)
	for _, line := range []BuildLog{
		{Stream: "status", Text: composeServiceBuildStatus + "api"},
		{Stream: "stderr", Text: "#5 [2/2] RUN npm run build"},
		{Stream: "stderr", Text: "#5 0.5 npm error Missing script: \"build\""},
		{Stream: "status", Text: composeServiceBuildStatus + "web"},
		{Stream: "stderr", Text: "#5 [2/2] RUN bun install --frozen-lockfile"},
		{Stream: "stderr", Text: "#5 0.4 error: lockfile had changes, but lockfile is frozen"},
	} {
		compose.observe(line)
	}
	service := buildFailureCause(&ComposeServiceError{Service: "web", Err: &dockerx.BuildError{Step: 5, Command: "bun install --frozen-lockfile", ExitCode: 1}},
		compose, causeContext{build: BuildPlanConfig{Method: BuildCompose}}, nil)
	if service.Code != "build_lockfile_out_of_sync" || service.Service != "web" || service.Phase != phaseDockerfile {
		t.Fatalf("compose cause = %+v", service)
	}
	if sentence := service.sentence(); !strings.HasPrefix(sentence, "Compose service web: The Dockerfile step (`bun install --frozen-lockfile`) exited with code 1") {
		t.Fatalf("compose sentence = %q", sentence)
	}
}

func TestBuildOutputCollectorStaysBounded(t *testing.T) {
	t.Parallel()
	collector := newBuildOutputCollector(nil)
	for vertex := 1; vertex <= openVertexLimit*2; vertex++ {
		collector.observe(BuildLog{Stream: "stderr", Text: fmt.Sprintf("#%d [x] RUN step %d", vertex, vertex)})
		for line := 0; line < 10; line++ {
			collector.observe(BuildLog{Stream: "stderr", Text: fmt.Sprintf("#%d 0.1 line %d of %d %s", vertex, line, vertex, strings.Repeat("y", 3000))})
		}
	}
	if len(collector.vertices) > openVertexLimit {
		t.Fatalf("kept %d open steps", len(collector.vertices))
	}
	for _, ring := range append([]*lineRing{collector.stream}, mapsValues(collector.vertices)...) {
		if ring.bytes > ring.maxBytes || len(ring.lines) > ring.maxLines {
			t.Fatalf("ring holds %d lines, %d bytes", len(ring.lines), ring.bytes)
		}
		for _, line := range ring.lines {
			if len(line.text) > collectedLineBytes {
				t.Fatalf("line kept %d bytes", len(line.text))
			}
		}
	}
	collector.observe(BuildLog{Stream: "stderr", Text: "#96 DONE 1.0s"})
	if collector.vertices[96] != nil {
		t.Fatal("a finished step's lines are still held")
	}
}

func mapsValues(values map[int]*lineRing) []*lineRing {
	result := []*lineRing{}
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func TestBuildPhaseReadsTheRenderedInstruction(t *testing.T) {
	t.Parallel()
	for command, want := range map[string]string{
		"npm ci": phaseInstall, "corepack enable && pnpm install --frozen-lockfile": phaseInstall,
		"pip install --no-cache-dir -r requirements.txt": phaseInstall, "go mod download": phaseInstall,
		"cargo fetch --locked": phaseInstall, "composer install --no-dev": phaseInstall,
		"apk add --no-cache musl-dev pkgconfig openssl-dev openssl-libs-static": phaseInstall,
		"npm run build": phaseBuild, "go build -trimpath -o /out/app .": phaseBuild, "cargo build --release": phaseBuild,
		"dotnet publish -c Release -o /out": phaseBuild, "custom thing": phaseBuild,
		"test -f /out/app || (echo 'Go build command must write an executable to /out/app' >&2; exit 1)": phaseOutputCheck,
		"adduser -D -u 10001 app": phaseSetup,
	} {
		build := BuildPlanConfig{Method: BuildRecipe, BuildCommand: "custom thing"}
		if got := buildPhase(causeContext{build: build}, command, "RUN "+command); got != want {
			t.Fatalf("phase of %q = %s, want %s", command, got, want)
		}
	}
	if got := buildPhase(causeContext{build: dockerfileBuild}, "npm ci", "RUN npm ci"); got != phaseDockerfile {
		t.Fatalf("a Dockerfile's step = %s", got)
	}
}

func TestRewriteNodeRunnerMovesOnlyPlainRunners(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ command, manager, want string }{
		{"npm run build", "bun", "bun run build"},
		{"npx prisma migrate deploy && npm run start", "bun", "bunx prisma migrate deploy && bun run start"},
		{"bun run build", "npm", "npm run build"},
		{"bunx prisma generate && bun run build", "pnpm", "pnpm exec prisma generate && pnpm run build"},
		{"pnpm exec tsc", "yarn", "yarn tsc"},
		{"node server.js", "bun", "node server.js"},
		{"npm install && npm run build", "bun", "npm install && bun run build"},
		{"npm run build", "", "npm run build"},
	} {
		if got := rewriteNodeRunner(test.command, test.manager); got != test.want {
			t.Fatalf("rewrite(%q, %s) = %q, want %q", test.command, test.manager, got, test.want)
		}
	}
}

func TestNodeBuildHeapScalesWithTheServer(t *testing.T) {
	t.Parallel()
	for memory, want := range map[int64]int{0: 0, 1 << 30: 0, 2 << 30: 1536, 4 << 30: 3072, 64 << 30: 8192} {
		if got := nodeBuildHeapMB(memory); got != want {
			t.Fatalf("heap for %d bytes = %d, want %d", memory, got, want)
		}
	}
}

func TestVariableFixNeverOverwritesAScopedVariable(t *testing.T) {
	t.Parallel()
	variables := []ReleaseVariableSnapshot{{Name: "NODE_OPTIONS", Scopes: "build,runtime"}, {Name: "API", Scopes: "runtime"}}
	if fix := variableFix("NODE_OPTIONS", "build", "--max-old-space-size=3072", variables); fix != nil {
		t.Fatalf("fix for a variable the build already had = %+v", fix)
	}
	if fix := variableFix("API", "build", "", variables); fix == nil || fix.Kind != fixVariableScope {
		t.Fatalf("scope fix = %+v", fix)
	}
	if fix := variableFix("not a name", "build", "", nil); fix != nil {
		t.Fatalf("fix for an invalid name = %+v", fix)
	}
}

func TestEverySignatureCodeHasATitleAndAnExplanation(t *testing.T) {
	t.Parallel()
	codes := []string{"build_timeout", "build_failed", "build_copy_source_missing", "build_dockerfile_invalid"}
	for _, signature := range append(slices.Clone(buildSignatures), pullSignatures...) {
		codes = append(codes, signature.code)
	}
	for _, code := range codes {
		if causeTitle(code) == "" {
			t.Fatalf("%s has no title", code)
		}
		what, action := (&BuildCause{Code: code, Detail: "node"}).explain()
		if action == "" || (what == "" && code != "build_failed" && code != "build_timeout") {
			t.Fatalf("%s explains nothing: %q / %q", code, what, action)
		}
	}
}

func TestNodeManagerForCommandLooksPastCorepack(t *testing.T) {
	t.Parallel()
	for command, want := range map[string]string{
		"corepack enable && pnpm install --frozen-lockfile":                         "pnpm",
		"corepack prepare pnpm@9.15.0 --activate && pnpm install --frozen-lockfile": "pnpm",
		"corepack enable && yarn install --immutable":                               "yarn",
		"npx prisma generate && npm run build":                                      "npm",
		"bunx prisma generate":                                                      "bun",
		"go mod download":                                                           "",
	} {
		if got := nodeManagerForCommand(command); got != want {
			t.Fatalf("manager of %q = %q, want %q", command, got, want)
		}
	}
	if got := lockfileForCommand("corepack prepare pnpm@9 --activate && pnpm install --frozen-lockfile"); got != "pnpm-lock.yaml" {
		t.Fatalf("lockfile = %q", got)
	}
}

func TestPreparedNodeManagerReadsTheInstallLine(t *testing.T) {
	t.Parallel()
	for preview, want := range map[string]string{
		"FROM x\nRUN --mount=type=secret,id=NPM_TOKEN,env=NPM_TOKEN,required=true npm ci\nRUN npm run build\n": "npm",
		"RUN corepack enable && pnpm install --frozen-lockfile\n":                                              "pnpm",
		"RUN bun install --frozen-lockfile\n":                                                                  "bun",
		"RUN go mod download\n":                                                                                "",
	} {
		if got := preparedNodeManager(PreparedBuild{Recipe: "node", DockerfilePreview: preview}); got != want {
			t.Fatalf("manager of %q = %q, want %q", preview, got, want)
		}
	}
}
