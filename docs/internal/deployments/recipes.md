# Automatic recipes and serving defaults

`just-dashboard-recipes-v2` prepares immutable Dockerfiles using digest-pinned catalogue bases. Build
commands execute inside the build container; source inspection never executes repository configuration
on the host. Generated Dockerfiles use root-relative, exclusive writes so a checkout symlink cannot
redirect output outside the build context. Detection ignores this generated directory.

Detection reads manifests as data — `package.json`, `angular.json`, `requirements.txt`, `pyproject.toml`,
`uv.lock`, `poetry.lock`, `Cargo.toml`, `pom.xml`, `build.gradle(.kts)`, `*.csproj`, `deno.json(c)`, a
`Procfile`, and for a JavaScript package its lockfiles, `.npmrc`, `.yarnrc.yml`, `bunfig.toml` and
`pnpm-workspace.yaml` — and names a candidate per root with the framework, the build and start commands, the port,
static output, the interpreter or toolchain release, and the environment variables and databases the
source reads. Every default is a plan field the configure form and the Build settings can change. A
detected framework that the recipe cannot serve automatically (a provider adapter, a workspace without a
root package, an unsupported interpreter release) is a low-confidence candidate with a decision or a
`recipe_unsupported` preflight finding, never a silent guess.

## Build values

Every `build`-scoped variable automatically reaches the recipe's build command through a required
BuildKit environment secret mount. Frozen run inputs supply the names at preparation and values at
build time; the bindings must agree. A variable's explicit `build.secrets` mapping to `install` limits
it to dependency installation instead, and `install_and_build` mounts the same secret in both RUN
steps — for a value a root package's own `postinstall` or `prepare` script reads, since those run inside
the install. A variable has one mapping; `install_and_build` is the one way to reach both steps, and
BuildKit still receives one secret. Build settings expose this choice for each build-scoped value
("Build", "Install only", "Both"). A registry credential a package manager's configuration names is
mapped to install automatically when a draft gives it a value (see
[JavaScript installs](#javascript-installs)). Nothing maps a database URL to the install on its own:
the install also runs every dependency's install script, and Prisma's `generate` does not connect (see
[JavaScript runtime and toolchain](#javascript-runtime-and-toolchain)).
Runtime and release-task scopes remain separate. Custom Dockerfiles do not gain automatic values or
secret mappings; they retain their existing refusal of requested secrets.

Neither generated Dockerfiles nor command arguments contain variable values. Logs redact exact values.
An ephemeral mount does not prevent application build code from intentionally copying a value into its
output. In particular, Next.js `NEXT_PUBLIC_` values and Vite `VITE_` values embedded in browser assets
are public, even if encrypted in the dashboard. Quick setup and the variable editor explain this.
See the upstream [Next.js environment contract](https://nextjs.org/docs/app/guides/environment-variables)
and [Vite environment contract](https://vite.dev/guide/env-and-mode).

## Environment discovery and database suggestions

Detection lists the variables a source expects so the configure form opens with them as rows. Names
come from a documented template (`.env.example`, `.env.sample`, `.env.template`, `.env.dist`,
`example.env` and the like) with the template's own value kept as the row's placeholder when it is not
credential-shaped, from a committed `.env` (names only, never values), and from the code's own reads of
its environment — `process.env.X`, `import.meta.env.X`, `Bun.env.X`, `Deno.env.get("X")`,
`os.environ["X"]`, `os.getenv("X")`, `env("X")`, `config("X")`, `os.Getenv("X")`, `getenv("X")`,
`ENV["X"]`. Tests, fixtures, documentation, migrations and generated files are not read, and names the
platform supplies (`PORT`, `HOST`, `NODE_ENV`, …) are left out. The scan has its own budget (400 files,
3 MiB) apart from detection's limits: a large repository stops contributing names quietly and is never
reported truncated for it. The root's own template comes first in file order, then a committed `.env`,
then what the repository documents above the root, then code references by name. Each row carries where
it was read; a detected row left empty is skipped at submit, not set to an empty value, and the form
says how many are unset.

The engines a source connects to are read from its dependencies (`pg`, `mysql2`, `mongoose`, `ioredis`,
`psycopg`, `asyncpg`, `pymongo`, `redis`, `github.com/jackc/pgx`, …), from a Prisma datasource
provider, and from variable names and example URLs (`REDIS_URL`, `MONGODB_URI`, `postgres://…`). Each
engine quick setup can provision — `postgres`, `mysql`, `mariadb`, `redis`, `mongodb` — is offered as one
button that opens the database sheet on that engine and the variable the connection belongs in
(`DATABASE_URL` for a relational engine the dependencies name, or the documented name). SQLite names no
engine.

## Procfile

A `web:` process in a Heroku-style `Procfile` is the repository declaring how it is served, and outranks
a start script and a framework default alike; it is only ever a server command, so a site framework's
build is still served by nginx. `release:` processes are not run automatically; add one as a release
task. A command that carries credential material is ignored.

## JavaScript and static output

How dependencies install — which package manager, from which lockfile, frozen or not, with which
release — is [JavaScript installs](#javascript-installs) below. Plain HTML uses the selected source directory as
its public root; its output directory is empty, not the source directory repeated a second time.
Packaged static output always serves on nginx port 80, regardless of a repository's development/start
script port. Quick setup and the wizard generate required HTTP readiness checks for that serving port.

The catalogue in `deploy/frameworks_node.go` is ordered and the first match wins, so a meta-framework
built on Vite is recognised before Vite itself — every one of them lists `vite`, and reading that alone
made a Remix server a static site. A framework's `start` script (Nest: `start:prod`) wins over its
default start command; a site framework ignores start scripts. A framework whose build script is
missing is a low-confidence candidate that asks for one.

| Framework | Recognised by | Serves as | Default |
| --- | --- | --- | --- |
| Next.js | `next` | server, 3000 | `<manager> run start` (`<runner> next start` without a script) |
| SvelteKit | `@sveltejs/kit` + `adapter-node` / `adapter-static` | server, 3000 / site `build` | `node build` (Bun: `bun ./build/index.js`); other adapters need a Dockerfile |
| Astro | `astro` (+ `@astrojs/node`) | site `dist` / server, 4321 | `node ./dist/server/entry.mjs` with `HOST=0.0.0.0`; a provider adapter is a decision |
| Nuxt 3/4 | `nuxt` | server, 3000 / site `.output/public` for `nuxt generate` | `node .output/server/index.mjs`; Nuxt 2 runs `nuxt start` |
| Remix | `@remix-run/dev` (+ `@remix-run/serve`) | server, 3000 | `remix-serve ./build/server/index.js` |
| React Router (framework mode) | `@react-router/dev` (+ `@react-router/serve`) | server, 3000 | `react-router-serve ./build/server/index.js`; `react-router` alone is a Vite site |
| SolidStart, TanStack Start, Nitro | `@solidjs/start`, `@tanstack/*-start`, `nitropack` | server, 3000 | `node .output/server/index.mjs` (TanStack asks to confirm the entry) |
| Angular | `@angular/core` (+ `@angular/ssr`) | site from `angular.json` (`dist/<app>/browser` with the application builder) / server, 4000 | `node dist/<app>/server/server.mjs` |
| NestJS | `@nestjs/core` | server, 3000 | `start:prod` script, else `node dist/main` |
| Gatsby, Docusaurus, VitePress, Eleventy | their packages | site `public`, `build`, `<docs>/.vitepress/dist`, `_site` | VitePress reads the docs directory from its build script (`docs:build`) |
| Create React App, Vue CLI, Ember, Parcel, Vite | their packages | site `build` / `dist` | single-page fallback on by default |
| Express, Fastify, Hono, Koa, Elysia, hapi | their packages | server, 3000 | the `start` script, else `node <main>` (`bun <main>` with a Bun lockfile) |

Every command the table proposes uses the resolved manager's runner (`bun`/`npm`/`pnpm`/`yarn run`,
`bunx`/`npx`/`pnpm exec`/`yarn` for a binary), and detection records the commands it would propose for
each of the four managers, so choosing another manager swaps whole commands.

A server framework's entry file (`.output/server/index.mjs`, `build/server/index.js`, `dist/main.js`,
…) is checked in the generated Dockerfile after the build when the start command is still the
framework's own, so a wrong output path fails the build with the framework's name instead of failing the
readiness gate. A custom start command skips the check. `main` without a start script is a low-confidence
worker unless the manifest names an HTTP library.

A site whose client owns its routes — Vite, Create React App, Vue CLI, Ember, Parcel and Angular
detections — carries `build.spaFallback`, which makes nginx answer any path with no file behind it with
`index.html`; multi-page generators (Astro, Gatsby, Docusaurus, VitePress, Eleventy, SvelteKit static)
keep nginx's own configuration byte for byte. The switch is on the configure form and Build settings
whenever there is static output, and is valid only with a static site or a recipe with an output
directory.

A service whose manifest depends on a recognised migration tool starts by applying its schema: the
detected start command becomes `<runner> <schema command> && <start>`, in front of a start script or a
framework default alike. Prisma (`*.prisma`) runs `prisma migrate deploy` when a `migration.sql` is
committed and `prisma db push` otherwise; Drizzle (`drizzle.config.*`) runs `drizzle-kit migrate` with a
`_journal.json` and `drizzle-kit push` otherwise; Knex (`knexfile.*`) runs `knex migrate:latest`;
Sequelize CLI runs `sequelize-cli db:migrate`; MikroORM migrations run `mikro-orm migration:up`. TypeORM
is recognised but needs an operator's command. A start script that already runs the tool is left as it
is, and static output never gains a start command. Prisma's push refuses destructive changes without an
explicit flag and Drizzle's stops to ask a question nobody can answer, so a schema that would lose data
fails the start instead of dropping it. The tool must be installed by the lockfile; the runtime stage
copies the build's `node_modules`, so a devDependency is available to the start command.

## JavaScript installs

`deploy/build_node_install.go` plans a JavaScript package's dependency install once, from files read as
data, and detection (which records the plan on the candidate for every package manager, as
`nodeInstalls`), preflight (which judges the operator's choice from that record) and the recipe (which
renders it) all use the same plan, so they cannot disagree. Reads go through an `os.Root` and never
follow a symlink to a file; nothing in the checkout is executed.

**Lockfiles are compared, not only named.** Every committed lockfile — `bun.lock`, `bun.lockb`,
`package-lock.json`, `npm-shrinkwrap.json`, `pnpm-lock.yaml`, `yarn.lock` — is read under a budget of its
own (16 MiB a file, 32 MiB a detection or build, never counted as detection truncation) and compared with
the `package.json` of every workspace it records, the way that manager's frozen install compares it
(`build_node_lockfile.go`): a dependency missing from the lock or removed from `package.json` is drift for
every manager; pnpm and Yarn also compare the range text, while npm and Bun accept a changed range the
locked version still satisfies (npm's range grammar is evaluated as data, `node_semver.go`). Yarn 1 is read
from its entry headers, Berry from its workspace blocks, pnpm up to its package list, npm entry by entry,
Bun's text lock as JSONC; `bun.lockb` is binary and can only prove a name missing. Each reading on the
candidate (`lockfiles`) is `in_sync`, `stale` — the frozen install would refuse it — or `unknown`, with the
sentence the screens show: "package-lock.json is missing 15 dependencies (prisma, @prisma/client, … and 12
more)".

**Which manager installs.** The build setting `packageManager`; then `packageManager` or
`devEngines.packageManager` in `package.json` (the workspace root's, for a member); then the one lockfile
a frozen install would accept; then the one lockfile that is in sync; then the files only one manager
writes — `trustedDependencies`, `bunfig.toml`, `@types/bun`, `pnpm-workspace.yaml`, the `pnpm` field,
`.pnpmfile.cjs`, `.yarnrc.yml`, `.yarn/releases`, and package scripts that call `bun`, `bunx`, `pnpm` or
`yarn`. Only a genuine tie is a decision (`package_manager_ambiguous`, blocked). `bun.lock` supersedes
`bun.lockb` and `npm-shrinkwrap.json` supersedes `package-lock.json`. Without any lockfile the same order
ends at npm. A chosen manager whose lockfile is not committed while another's is is refused before the
build, naming the lockfile that is (`package_manager_lockfile_missing`, blocked) — the shape a later
commit that switched lockfiles leaves behind. When lockfiles competed, the `package_manager_resolved`
pass says why one won and which to delete.

**Frozen when the lockfile is right, a warning when it is not.**

| Manager | Lockfile in sync or unknown | Lockfile provably stale, or none |
| --- | --- | --- |
| npm | `npm ci` | `npm install --no-audit --no-fund` |
| pnpm | `pnpm install --frozen-lockfile` | `pnpm install --no-frozen-lockfile` |
| Yarn 1 (`# yarn lockfile v1`) | `yarn install --frozen-lockfile` | `yarn install` |
| Yarn 2 and later (`__metadata:`) | `yarn install --immutable` | `yarn install --no-immutable` |
| Bun | `bun install --frozen-lockfile` | `bun install` |

A stale lockfile raises `lockfile_out_of_sync` (a warning that names the drift and, when another lockfile
matches, offers that manager), and the run log says the install is unfrozen and why. A repository without
a lockfile — tutorials, repositories that ignore it — installs unfrozen with `dependencies_unpinned`,
rather than being refused. Yarn 1 ignores `--immutable`, which is why it gets its own flag.

**Pinned manager releases.** pnpm and Yarn Berry run an exact release: the one `package.json` declares
(its hash included), else the reviewed table `nodeManagerReleases` keyed by the lockfile format — pnpm
`lockfileVersion` 5.x → 7.33.7, 6.x → 8.15.9, 9.0 → 10.34.5 (also the release without a lockfile); Berry
metadata 4 → 2.4.3, 5 → 3.1.1, 6 → 3.8.7, 8 → 4.9.2, 10 → 4.18.0 — or the release a committed `.yarnrc.yml`
`yarnPath` names, which Yarn 1 dispatches to. Corepack's own default is the newest release, which moves
under a digest-pinned image and rejects older lockfiles. A Berry lock in no known format installs with
4.18.0 unfrozen (`yarn_version_inferred`), and a declared pnpm whose major cannot read the lockfile is
refused (`package_manager_lockfile_incompatible`). Yarn 1 is the Node image's own 1.22. Bun follows a
`packageManager` of `bun@x.y.z` (`oven/bun:x.y.z-alpine`), falling back to `oven/bun:1-alpine` with a
logged note when that release has no image. `package_manager_version` (a pass) and the build evidence's
`toolchain` name the release and what chose it.

**One toolchain for the build and the server.** The pinned releases are installed through Corepack into a
`toolchain` stage (`COREPACK_HOME=/opt/corepack`), and both the build stage and a server's runtime stage
start from it, with `COREPACK_ENABLE_NETWORK=0` at run time: a start command that runs `pnpm`, `yarn` or
`bunx` finds the exact release offline, where the runtime stage used to have no pnpm at all. The runtime
and build stages put `node_modules/.bin` on `PATH`. Bun is copied from its digest-pinned image beside
Node 22, with `bunx` linked to it: Bun installs and runs `bun` and `bunx` commands, and anything started
through `node` runs on Node — the Bun image's own `node` is Bun, which made Bun the production runtime of
every `bun.lock` project (`runtime_selected`). A command or package script the build runs that calls a
manager other than the chosen one — `bunx` in an npm project, `pnpm` in a Bun one — gets that tool added
the same way (`script_runtime_added`); `deno` cannot be, and is refused (`command_runner_missing`). A
manifest declaring another manager than the committed lockfile's runs with `COREPACK_ENABLE_STRICT=0`
(`package_manager_declaration_conflict`).

**Yarn Plug'n'Play.** Berry keeps its cache inside the build (`YARN_ENABLE_GLOBAL_CACHE=false`) so it is
copied with the application. When the linker is Plug'n'Play and a command runs outside `yarn` — a
framework's `node build` — the install sets `YARN_NODE_LINKER=node-modules`; the lockfile does not depend
on the linker (`yarn_linker_adjusted`).

**Dependency install scripts.** pnpm 10 skips dependency build scripts without a policy and pnpm 11 fails
on them; with no policy declared the install adds `--config.dangerously-allow-all-builds=true` — what npm,
Yarn and pnpm 9 do, inside the build container (`dependency_scripts_allowed`). A declared policy is kept;
one written only in pnpm 10's fields under pnpm 11 or later is `pnpm_build_policy_ignored`, and one that
leaves out a package whose script the application needs at run time (better-sqlite3, sqlite3, bcrypt,
argon2, canvas, puppeteer, node-sass, Prisma 6 and earlier, sharp before 0.33, …) is
`install_scripts_blocked`. Declaring Bun's `trustedDependencies` replaces Bun's default allowlist, so the
install trusts such packages that are installed but not listed (`bun pm trust`, `install_scripts_trusted`).

**npm's lockfile quirks.** A `package-lock.json` whose locked peers break their ranges was written with
`--legacy-peer-deps`; unless `.npmrc` already says so the install passes that flag, which installs exactly
the locked tree (`peer_dependencies_legacy`). A lock that lacks this platform's `-linux-<arch>-musl`
optional binaries (npm/cli#4828 — Rollup, lightningcss, Tailwind's oxide, SWC) gets them added at the
exact version the parent names, after `npm ci` (`optional_binary_missing`). A lock resolving packages from
an intranet host is `registry_host_private`.

**Registry credentials.** `.npmrc` (`${NAME}`), `.yarnrc.yml` (`${NAME}`, with or without a default) and
`bunfig.toml` (`$NAME`) are read as data for the variables their credentials name. Each becomes a detected
variable marked for the install step (`step: "install"`), required when a dependency's scope installs from
that registry, or when Yarn would abort without it (Berry fails every install on an unset variable with no
default). A value given in the draft is mapped to the install step automatically; preflight's
`registry_token_missing` is blocked when a required credential cannot reach the install and a warning
otherwise, and a literal token committed to a configuration file is `registry_token_committed` (the value
is never echoed).

**Workspaces.** A package with no lockfile of its own installs from the nearest ancestor whose lockfile
and `workspaces` (or `pnpm-workspace.yaml` `packages`) include it; a `pnpm-workspace.yaml` of settings
alone is not a workspace. The build context widens to that root (`prepared.contextDirectory`), the
install runs there, the build and the server run in the member's directory, and every workspace the
lockfile records is compared. A member of a Turborepo that depends on workspace packages builds with
`<runner> turbo run build --filter=<name>...`.

**The repository's `.dockerignore`.** When the repository has one, the generated Dockerfile gets
`.just-dashboard/Dockerfile.dockerignore`, which BuildKit prefers: the repository's file verbatim, then
exceptions for the install inputs the recipe reads (`package.json`, the lockfile, `.npmrc`, `.yarnrc.yml`,
`.yarn/releases`, `patches`, …). The operator's other exclusions stand, and the run log names each input
a repository pattern had excluded.

**Commands follow the manager.** Choosing a manager in the configure form or Build settings swaps a
command that is still one detection proposed for the one it proposes for the new manager (`nodeInstalls`),
and "From the lockfile" means the manager detection resolved; a command the operator wrote keeps its
words with only its plain runner segments moved. At build time a saved command whose plain runner names
another manager (`npm run build` after a commit replaced `package-lock.json` with `bun.lock`) runs through
the resolved manager's runner; the run log says so and preflight raises `runner_mismatch`. A second
install in the build command is `install_in_build_command`. Review lists the install, build and start
commands the Dockerfile runs (`build_commands`).

Detection preserves an actual Dockerfile/Containerfile filename relative to its build root. A single
literal TCP `EXPOSE` in the final stage supplies the suggested port; dynamic/multiple ports still need
an operator's explicit choice. A Dockerfile inherited base's unrecorded exposed port is not guessed.

## Python

The recipe reads `requirements.txt`, a PEP 621 `pyproject.toml`, a Poetry `pyproject.toml`, `uv.lock`
and `poetry.lock`. Unpinned requirements are accepted — refusing them turned the most ordinary Python
repository there is into a manual Dockerfile — and preflight raises `dependencies_unpinned` as a warning
the operator acknowledges, naming what a rebuild may resolve differently and how to pin. Installation
follows the manifest: `pip install --requirement` for requirements, `uv sync --frozen --no-dev` into the
project's `.venv` (put first on `PATH`, with `VIRTUAL_ENV` set, so a start command can say `uvicorn`),
`poetry install --only main --no-root` into the interpreter (`POETRY_VIRTUALENVS_CREATE=false`), and for
a bare pyproject the standard library's `tomllib` reads `[project].dependencies` into a requirements
file rather than `pip install .`, which needs a build backend an application never set up (3.10 brings
`tomli`). A process manager the start command runs that no manifest declares — `gunicorn`, `uvicorn` —
is installed at the exact release `build_python.go` pins, into the same environment as the dependencies.

The interpreter family comes from `build.pythonVersion`, then `.python-version`, then Heroku-style
`runtime.txt`, then pyproject's `requires-python` (or Poetry's `python` constraint): `>=3.11` picks the
newest release the constraint allows, `~=3.11.0` and `==3.12.*` mean that family. The catalogue carries
3.10 to 3.13; anything else is a `recipe_unsupported` finding pointing at a Dockerfile.

`deploy/frameworks_python.go` recognises the frameworks from the dependency names and finds the
application object in the conventional entry files (`main.py`, `app.py`, `server.py`, `api.py`,
`wsgi.py`, `asgi.py`, `streamlit_app.py`, a package's `__init__.py`; never under `tests/`, `migrations/`
or `examples/`), shallowest first:

| Framework | Start | Port |
| --- | --- | --- |
| Django (`manage.py`) | `python manage.py migrate --noinput && gunicorn <project>.wsgi:application --bind 0.0.0.0:8000`, with `collectstatic` before it when WhiteNoise is installed; `uvicorn <project>.asgi:application` when only an ASGI module exists and uvicorn is declared | 8000 |
| FastAPI | `uvicorn <module>:<object> --host 0.0.0.0 --port 8000` for the `= FastAPI(` object found | 8000 |
| Flask | `gunicorn --bind 0.0.0.0:8000 <module>:<object>`, or `'<package>:create_app()'` for a factory | 8000 |
| Streamlit | `streamlit run <script> --server.port 8501 --server.address 0.0.0.0 --server.headless true` | 8501 |
| Gradio | `python <script>` with `GRADIO_SERVER_NAME=0.0.0.0` in the image | 7860 |

A framework whose application object is not in an entry file keeps the port and asks for the module. A
Django project answers only the hosts its settings allow; `ALLOWED_HOSTS` read from the environment is
listed like any other variable. A plain `main.py`/`app.py` is a low-confidence worker that asks whether
it serves. The recipe refuses a plan with no start command, naming the frameworks detection proposes one
for.

## Go

The Go recipe supports stable 1.25 and 1.26 toolchains. `build.goVersion` optionally pins a language
family or patch version. Without it, `.go-version` takes precedence over the `toolchain` and `go`
directives in `go.mod`; an older module language minimum uses the maintained 1.26 default. A selected
version cannot be older than the module requires. Resolved image digests, selected Go version and the
generated Dockerfile are recorded in build evidence. `GOTOOLCHAIN=local` prevents an unrecorded automatic
toolchain download inside the build. See the [Go toolchain rules](https://go.dev/doc/toolchain).

The default build compiles the one detected main package to `/out/app`. A custom build command executes
exactly as configured and must produce an executable there; this also allows code generation and an
explicit package choice for a repository with several mains. The historical detected `go build ./...`
command still executes, followed by the default output-producing build for compatibility. A custom start
command replaces the default `/app` entrypoint and runs inside the unprivileged runtime container.

This recipe uses `CGO_ENABLED=0`. Local non-test source importing `C`, unsupported source versions and
explicit CGO-enabling commands produce actionable planning/preparation refusals. Dependencies needing
CGO or more complex native-library/workspace arrangements require a Dockerfile; source scanning does
not certify all transitive dependencies. The dashboard's own required Go toolchain remains 1.26.8.

## Rust

`Cargo.toml` names the package (or the first `[[bin]]`) whose release binary becomes `/app` on an
`alpine` runtime as an unprivileged user; the build runs on `rust:1-alpine` (or `rust:<version>-alpine`
when `rust-toolchain(.toml)` pins a stable release — nightly and beta need a Dockerfile) with
`musl-dev`, `pkgconfig` and static OpenSSL installed so the usual crates link. `cargo fetch` is its own
layer under the install secret mount; the build is `cargo build --release`, `--locked` when `Cargo.lock`
is committed (its absence is the unpinned warning). A custom build command runs in its place and must
still leave `target/release/<binary>`. `axum` (3000), `actix-web` (8080), `rocket` (8000), `warp`
(3030), `poem` (3000) and `salvo` (5800) mark a web service with the framework's conventional port and
ask to confirm it; a crate with none is a worker that asks. A workspace without a root package is a
`recipe_unsupported` finding: set the root directory to the member crate.

When `package.default-run` is declared, it selects the served binary ahead of that fallback, including
automatically discovered `src/bin` targets. This keeps a maintenance command from being launched in
place of the HTTP service just because its `[[bin]]` appears first.

## Java and Kotlin

A `pom.xml` builds with `maven:3-eclipse-temurin-<release>` (`mvn -q -B -DskipTests package`); a
`build.gradle(.kts)` with `gradle:8-jdk<release>` (`./gradlew --no-daemon -q build -x test` through
the committed wrapper, or the image's own Gradle). The one executable jar — not `-sources`, `-javadoc`,
Spring Boot's `-plain` or a shade plugin's `original-` — becomes `/app/app.jar` on
`eclipse-temurin:<release>-jre-alpine`, run by `java -jar` as an unprivileged user with
`-XX:MaxRAMPercentage=75` so the heap follows the container's limit. The release comes from
`.java-version`, the pom's `java.version`/`maven.compiler.release` properties, or Gradle's toolchain and
compatibility settings; 11, 17, 21 and 25 are in the catalogue and 21 is the default. Spring Boot,
Quarkus, Micronaut, Javalin, Ktor, Helidon and Vert.x mark a web service (8080; Javalin 7070); a plain
project is a worker that asks. A multi-module Maven project is a `recipe_unsupported` finding: set the
root directory to the module that builds the application.

## .NET

The one `.csproj` at the root — or, among several, the one `Microsoft.NET.Sdk.Web` project — is restored
(its own layer under the install secret mount) and published with `mcr.microsoft.com/dotnet/sdk:<target>`
into `/out`, then run on `mcr.microsoft.com/dotnet/aspnet:<target>` (a console project on
`runtime:<target>`) as the image's `app` user. The target comes from `<TargetFramework>` or the newest
supported portable entry in `<TargetFrameworks>`; restore and publish explicitly select that same
framework. Platform-specific-only target lists require a Dockerfile. net8.0, net9.0
and net10.0 are in the catalogue. Kestrel reads its port from `ASPNETCORE_HTTP_PORTS`, so the default
start command bridges the `PORT` the runtime injects: `ASPNETCORE_HTTP_PORTS=${PORT:-8080} dotnet
/app/<Assembly>.dll`. A web project listens on 8080; a console program asks whether it serves.

## Deno

Configuration parsing accepts JSONC comments and trailing commas without rewriting URLs, escaped
quotes or comment-like text inside task strings. Invalid configuration does not contribute partially
parsed tasks.

`deno.json`/`deno.jsonc` (comments and trailing commas tolerated) supplies the `build` and `start`
tasks; without a start task the conventional entry file (`main.ts`, `server.ts`, `mod.ts`, …) is run
with `--allow-all`. `deno install` (`--frozen` with a `deno.lock`) caches the imports on
`denoland/deno:alpine` before the build task runs. A `fresh` import names the framework. `Deno.serve`'s
default of 8000 is the port, and the candidate asks to confirm it.

## PHP

A `composer.json`, or an `index.php` at the root or under `public/`, is a PHP application; an `index.php`
deeper in the tree (WordPress's `wp-admin/`, a theme) names nothing on its own. `laravel/framework`
with an `artisan` file is Laravel, `symfony/framework-bundle` with `bin/console` is Symfony, `slim/slim` is
Slim; anything else is plain PHP served from the directory that holds `index.php`. The image is
`dunglas/frankenphp:1-php<version>-alpine` — FrankenPHP serves on 80 with its own worker, so no nginx
or php-fpm pair — and the version comes from Composer's `php` constraint with Composer's own semantics
(`^8.2`, `~8.3.0`, `>=8.2 <8.4`, `8.2.*`, `||` unions); 8.2, 8.3 and 8.4 are in the catalogue, 8.3 is the
default, and a constraint the catalogue cannot satisfy (`^7.4`) is a `recipe_unsupported` finding.
`ext-*` requirements install through `install-php-extensions` (built-ins such as `mbstring` are skipped;
`pdo_mysql`, `pdo_pgsql` and `opcache` are always present, so a linked database works without asking).
Composer runs from the `composer:2` image with `--no-dev --optimize-autoloader`; a `package.json` whose
build script names Vite, `laravel-vite-plugin` or Encore gets an asset stage on the manifest's own
runtime whose `public/build` is copied in. That asset stage installs through the same plan as the Node
recipe ([JavaScript installs](#javascript-installs), toolchain stage `assets-toolchain`), the same
`packageManager` build setting applies to a `php` recipe, and its lockfile findings — competing lockfiles,
none (`assets_dependencies_unpinned`), a stale one — reach preflight before Deploy. Laravel's start runs `php artisan migrate --force` first;
Symfony's runs `doctrine:migrations:migrate` when the migrations bundle is present. `storage/`,
`bootstrap/cache` and `database/` are created writable so a first start on an empty volume works. The
configure form mints `APP_KEY` for a Laravel import (`base64:` over 32 random bytes) and offers a
**Generate** button on any variable whose name ends in `SECRET`, `KEY` or `SECRET_KEY_BASE`. Laravel's
`.env.example` names its engine in `DB_CONNECTION`, which becomes a database suggestion on `DB_URL`.

## Verification

`TestLiveDetectedFrameworkBuildAndServing` builds locked Next.js (Bun, and pnpm started through `pnpm run
start`), an Express server installed and started by Yarn 1, Vite, SvelteKit Node/static, plain HTML,
Containerfile and Go fixtures through detection and the real artifact/runtime owners, and the catalogue's
own starters: Astro 7 (static), Nuxt 4 and React Router 8 (servers), FastAPI on an unpinned
`requirements.txt` with no server declared, a Flask factory on a bare `pyproject.toml`, a Django project
whose first request reads its migrated table, a Streamlit script (its health endpoint and a file served
through its own static-serving setting) and a Gradio app (the value in the page's embedded config), an
axum service, a Maven jar and a Gradle jar, an ASP.NET Core minimal API, a Deno server, a minimal Laravel
12 application (migrated, with the form's generated `APP_KEY`) and a plain `index.php`. It checks
readiness and served values without supplying build values at runtime; recipe fixtures also inspect logs,
metadata and saved image layers for private install credentials. The Go fixture proves generated code,
custom startup and the selected toolchain through its HTTP response. These local adapter journeys
complement the production-build browser gate; they do not constitute public provider/DNS/TLS or clean-VM
acceptance. The remaining catalogue entries are covered by rendered-Dockerfile and detection tests
(`frameworks_*_test.go`, `build_recipes_test.go`). JavaScript installs are covered by
`build_node_lockfile_test.go` (each manager's lockfile shapes), `build_node_install_test.go` (resolution
and every rendered install), `preflight_node_test.go` and `build_node_incident_test.go`, which keeps the
incident that motivated them fixed: a Next.js 16 + Prisma 7 repository with an in-sync `bun.lock` beside a
`package-lock.json` fifteen dependencies behind now resolves to Bun with no decision, and a forced npm
raises `lockfile_out_of_sync` before Deploy and installs unfrozen instead of failing `npm ci`.
