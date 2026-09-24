# Automatic recipes and serving defaults

`just-dashboard-recipes-v2` prepares immutable Dockerfiles using digest-pinned catalogue bases. Build
commands execute inside the build container; source inspection never executes repository configuration
on the host. Generated Dockerfiles use root-relative, exclusive writes so a checkout symlink cannot
redirect output outside the build context. Detection ignores this generated directory.

Detection reads manifests as data — `package.json`, `angular.json`, `requirements.txt`, `pyproject.toml`,
`uv.lock`, `poetry.lock`, `Cargo.toml`, `pom.xml`, `build.gradle(.kts)`, `*.csproj`, `deno.json(c)`, a
`Procfile` — and names a candidate per root with the framework, the build and start commands, the port,
static output, the interpreter or toolchain release, and the environment variables and databases the
source reads. Every default is a plan field the configure form and the Build settings can change. A
detected framework that the recipe cannot serve automatically (a provider adapter, a workspace without a
root package, an unsupported interpreter release) is a low-confidence candidate with a decision or a
`recipe_unsupported` preflight finding, never a silent guess.

## Build values

Every `build`-scoped variable automatically reaches the recipe's build command through a required
BuildKit environment secret mount. Frozen run inputs supply the names at preparation and values at
build time; the bindings must agree. A variable's explicit `build.secrets` mapping to `install` limits
it to dependency installation instead. Build settings expose this choice for each build-scoped value.
Runtime and release-task scopes remain separate. Custom Dockerfiles do not gain automatic values or
secret mappings; they retain their existing refusal of requested secrets. The one exception is a value
the Dockerfile itself asks for and that is public by design: a **plain** build-scoped variable with a
browser-public prefix (`NEXT_PUBLIC_`, `VITE_`, `PUBLIC_`, `NUXT_PUBLIC_`, `REACT_APP_`, `EXPO_PUBLIC_`)
that the Dockerfile declares with `ARG` is passed as `--build-arg NAME`, the value only in buildx's
process environment (`deploy/build_dockerfile_args.go`). A value typed into a new project's environment
is stored secret, except one with a browser-public name that is not declared secret: the page's
JavaScript carries it by design, and only a plain value can become a build argument. Preflight lists
what is passed (`dockerfile_build_args`) and warns about every other declared argument that will be
empty (`dockerfile_arg_not_passed`, naming a browser-public one marked secret); a secret, or a name that
would replace the builder's own environment (`DOCKER_*`, `PATH`, …), never qualifies. A build argument
stays in the image's history, which is why a secret is never one.

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
build is still served by nginx. A command that carries credential material is ignored.

## Release commands

The command a repository declares runs once before each release — a Procfile `release:` process,
`fly.toml`'s `[deploy] release_command`, `render.yaml`'s `preDeployCommand` (when one service declares
it), or a Phoenix release's `rel/overlays/bin/migrate` — is the candidate's `releaseCommand`. The form
plans it as a release task named `release` that runs **in the release image** (`runner: "image"`): one
throwaway container of the candidate release's image, with the runtime variables the release starts
with plus the task's own `release_task`-scoped ones, on the project's database networks — or on the
host's network when the release runs there — removed when it exits. A command with shell syntax runs
through the image's `/bin/sh`; a plain one is executed directly, so an image without a shell still runs
`bin/migrate`. Its container is labelled as a release task, so it never counts as one of the release's
services; one a stopped dashboard left behind is removed when the dashboard starts and before the task
runs again, and the run records whether removal succeeded. Without such a task preflight warns
`release_command_unmapped`. A new task defaults to the release image whenever the build makes one (not
for `none` or legacy Compose, which refuse it). A Compose release's image task runs the primary
service's image on its own, outside the stack — not on its network and without the service's
`environment:` entries — which preflight names as `release_task_outside_compose_stack`: blocked when the
stack runs its own database or cache, a warning otherwise; a migration against the stack's own database
belongs in the Compose file (a one-off service the application `depends_on` with
`condition: service_completed_successfully`, or the service's command). A task with no runner is the historical shell over the unbuilt checkout in
the dashboard's own container; preflight refuses one that runs a tool only installed dependencies
provide (`prisma`, `knex`, `alembic`, anything under `node_modules/.bin` or `.venv`) or a program the
dashboard does not have — in its own image that includes `npx`, `python` and `bundle` — as
`release_task_tool_missing`, and a host task that runs the application's code no longer counts as the
schema step.

## JavaScript and static output

The lockfile selects npm, pnpm, Yarn or Bun. When lockfiles for more than one manager are committed,
the build setting `packageManager` chooses, then `packageManager` in `package.json`; with neither, the
recipe and preflight refuse rather than install from a lockfile the project may have abandoned. A chosen
manager must have its own lockfile. Plain HTML uses the selected source directory as
its public root; its output directory is empty, not the source directory repeated a second time.
Packaged static output always serves on nginx port 80, regardless of a repository's development/start
script port. nginx refuses every dot-path except `.well-known/` (`location ~ /\.(?!well-known/)`), so a
stray `.git/`, `.env` or `.htaccess` in the published directory is never served. Quick setup and the wizard generate required HTTP readiness checks for that serving port.

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

A server framework's entry file (`.output/server/index.mjs`, `build/server/index.js`, `dist/main.js`,
…) is checked in the generated Dockerfile after the build when the start command is still the
framework's own, so a wrong output path fails the build with the framework's name instead of failing the
readiness gate. A custom start command skips the check. `main` without a start script is a low-confidence
worker unless the manifest names an HTTP library.

A site whose client owns its routes — Vite, Create React App, Vue CLI, Ember, Parcel and Angular
detections — carries `build.spaFallback`, which makes nginx answer any path with no file behind it with
`index.html`; multi-page generators (Astro, Gatsby, Docusaurus, VitePress, Eleventy, SvelteKit static)
serve files as they are, with the dot-path rule and no fallback. The switch is on the configure form and
Build settings whenever there is static output, and is valid only with a static site or a recipe with an
output directory.

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

## Repository Dockerfiles and Compose files

A repository's own container definitions are read as data, never built during detection
(`deploy/dockerfile_parse.go`, `dockerfile_facts.go`, `detector_dockerfile.go`, `detector_compose.go`).
The parser follows BuildKit: parser directives (`# escape=`), `\` continuations with comment lines
inside them, and heredoc bodies.

- **Names and places.** `Dockerfile`, `Containerfile`, and their variants (`Dockerfile.prod`,
  `Dockerfile-dev`, `api.Dockerfile`, `Containerfile.worker`) are candidates, including under `build/`
  (Go's `build/package/Dockerfile`) and `deploy/`. Dev-container and GitHub Action Dockerfiles, and
  Compose files under `.devcontainer/`, are not. A name saying `prod`/`production`/`release` is a
  production Dockerfile; one saying `dev`/`local`/`test`/`debug`/`ci` is a development one that is
  listed but never chosen on its own; any other variant is medium confidence.
- **Build context.** A Dockerfile is built from the directory a Compose service names for it, else the
  nearest directory from its own up to the repository root that holds the paths its `COPY`/`ADD` lines
  read — `docker/Dockerfile` written for `docker build -f docker/Dockerfile .` builds from the root — and
  `build.dockerfile` is its path relative to that context.
- **Stage and port.** When the last stage is `dev`/`development`/`test`/`debug`, the candidate builds
  `production`/`prod`/`release`/`runner`/`runtime` (`build.target`, passed as `--target`; a Compose
  service's own `target` wins). The stage is edited beside the Dockerfile path and cleared when the path
  changes; detection records the file's stage names (`dockerfileStages`), and preflight refuses a stage
  the file does not have (`dockerfile_target_missing`). The port is the one TCP `EXPOSE` of the built stage and the stages it
  derives from, resolving `$PORT`/`${PORT}` through the file's own `ARG`/`ENV` defaults and setting aside
  debugger and metrics ports (9229, 5005, 9464, 9090); anything else still asks.
- **What the tree shows will fail** is read only from the stages the build runs — the target, the
  stages it is built `FROM`, and those it copies or mounts from; BuildKit skips every other one — except
  that every `FROM` must resolve, used or not. It is recorded as the candidate's `imageBuildIssues` and
  becomes a preflight finding of the same code: `dockerfile_refused` (a secret-named `ENV`/`ARG`, shell assignment
  or flag given a literal — not empty values, `$VAR` references, switches, numbers, the placeholder words
  Rails and Django use, or `*_FILE` paths; `SECRET_KEY_BASE_DUMMY=1` builds), `dockerfile_copy_source_missing`,
  `dockerfile_copy_ignored` (the context's `.dockerignore`, or `<Dockerfile>.dockerignore`, with BuildKit's
  pattern rules), `dockerfile_arg_required` (an `ARG` without default in `FROM`, unless the expression
  has its own, `${X:-d}`), `dockerfile_ssh_mount`,
  `dockerfile_standalone_missing` (a Next.js Dockerfile copying `.next/standalone` without
  `output: 'standalone'`), `dockerfile_dev_server` (warning), `script_crlf` and `script_not_executable`
  (the file an exec-form `ENTRYPOINT`/`CMD` or `RUN ./x` executes, or a recipe's start script, committed
  with Windows line endings or without its executable bit, unless the Dockerfile fixes it: a `chmod` of
  it, a directory or a wildcard covering it, a separator after the operand included, or a `dos2unix` or
  `sed` over it, a wildcard such as the Rails Windows template's `bin/*`, or its directory). A
  `FROM --platform=` for another architecture is `foreign_architecture_build`, blocked when this host has
  no binfmt emulator for it.
- **Choosing.** Candidates for the same root — two ways to build one application — are ranked by
  whether they may be chosen on their own, whether they build as detected (no blocking issue, no recipe
  issue, no unresolved package manager), confidence, and intent: a production-named Dockerfile, then the
  plain Dockerfile, then the recipe; a Dockerfile that runs a dev server, or declares no `ARG` for a
  browser-public variable the source reads, ranks below the recipe. Different roots are different things,
  so their best candidates compare only on whether they may be chosen and on confidence: a helper image
  in `docker/db/` that builds does not beat the application whose recipe needs one decision. A Dockerfile
  built on a database, cache or other backing image is low confidence. The winner is listed first with
  the reason (`selectionReason`, the `detection_selected` finding's measured text); only a true tie leaves
  the choice to the operator. A buildable Dockerfile beside a recipe blocked by competing lockfiles wins;
  preflight names a skipped Dockerfile's problem as `dockerfile_not_selected`.
- **Compose files in a repository** are classified before they become candidates. One that runs only
  backing images (Postgres, MySQL, Redis, Mongo, Mailpit, MinIO, …) is not a candidate: its databases
  become suggestions on the candidates beside it — unless the repository has nothing else to deploy, when
  it is a low-confidence Compose candidate. One whose builds all come from paths the checkout lacks
  (Laravel Sail's `vendor/laravel/sail/runtimes`) is a low-confidence development stack. Any other is a
  Compose candidate that is chosen only when nothing else is there, because a Compose file in a Git
  source is analysed only as a Compose source: preflight blocks it as `compose_analysis_missing`, and the
  project step offers to re-read the same repository — a Git URL, a connected repository with its
  credential, or a local checkout — as one.
- **A Compose source** records each service's build `target` and `args`, `platform`, `env_file` entries,
  and the platforms each image is published for (read from the registry four lookups at a time, under one
  15-second budget). Build arguments and targets are resolved at build time with Compose's own
  `${X:-default}` rules from the **plain** runtime and build variables together — Compose reads one
  environment for the whole file, and the form plans a Compose file's variables for runtime — and passed
  like Dockerfile build arguments. An argument or target that reads a secret is refused, at preflight
  (`compose_build_arg_secret`) and at the build, because a build argument stays in the image's history;
  one whose variable is planned only for release tasks is empty (`compose_build_arg_unscoped`, blocked
  when the file marks it `${X:?}`). A
  browser-public Compose variable is planned plain. Variables interpolated with a default are optional
  rows with the default as the example, never required secrets. Preflight blocks a build context the
  checkout lacks (`compose_build_context_missing`), a required `env_file` it lacks
  (`compose_env_file_missing`), a refused service Dockerfile, and an image with no build for this host's
  architecture (`compose_image_platform_missing`, whose action suggests pinning `platform:` when an
  emulator is installed; a service pinned to a platform its image offers is left to the emulation
  check). The primary service — the one readiness, the release's container and its image release tasks
  follow — builds or publishes a port, prefers `web`, `app`, `frontend`, `server` or `api`, and is never a
  database; the operator can choose another (`build.primaryService`), and preflight refuses one the stack
  no longer has (`compose_primary_service_missing`). A pasted or uploaded Compose file arrives without
  the files around it, so a service it builds is refused before Deploy (`compose_build_without_checkout`)
  instead of failing to find its Dockerfile.
- **Languages without a recipe.** A Vapor or Hummingbird `Package.swift` with no Dockerfile is a
  low-confidence candidate whose `dockerfile_missing` finding says to commit the template's Dockerfile;
  with one, the Dockerfile is chosen over the template's Compose file. `Environment.get("X")` reads are
  listed as variables. Rails, Phoenix and Swift Dockerfiles carry their framework's name.

## Build context

Every recipe and static build writes `.just-dashboard/Dockerfile.dockerignore`, which BuildKit reads in
place of the repository's own `.dockerignore` (`deploy/build_dockerignore.go`). It keeps the
repository's rules except any that would leave out a file the recipe reads by name — a manifest,
lockfile, framework configuration, Prisma schema — which is `dockerignore_drops_recipe_input`, a warning
that the rule is set aside; then it excludes `**/node_modules`, `.dockerignore` and the dashboard's own
files. The static, PHP, Node and Deno images, which are served or copied whole, also exclude `.git`;
recipes whose toolchains stamp or version builds from Git (Go, Python's setuptools-scm, Maven's
git-commit-id, SourceLink) keep it. A static site also excludes `.env` and `.env.*`. Committed `.env`
files are otherwise left in: Next.js and Vite read public build values from them.

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
the committed wrapper, its Windows line endings stripped first, or the image's own Gradle). The one executable jar — not `-sources`, `-javadoc`,
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
FrankenPHP does not read `.htaccess`, so one with access or rewrite rules is a `php_htaccess_ignored`
warning, and a plain PHP application served from the repository root (`--root /app`) is a
`php_docroot_is_repository_root` warning: dependencies, lockfiles and logs under it are reachable.
Composer runs from the `composer:2` image with `--no-dev --optimize-autoloader`; a `package.json` whose
build script names Vite, `laravel-vite-plugin` or Encore gets an asset stage on the manifest's own
runtime whose `public/build` is copied in. Laravel's start runs `php artisan migrate --force` first;
Symfony's runs `doctrine:migrations:migrate` when the migrations bundle is present. `storage/`,
`bootstrap/cache` and `database/` are created writable so a first start on an empty volume works. The
configure form mints `APP_KEY` for a Laravel import (`base64:` over 32 random bytes) and offers a
**Generate** button on any variable whose name ends in `SECRET`, `KEY` or `SECRET_KEY_BASE`. Laravel's
`.env.example` names its engine in `DB_CONNECTION`, which becomes a database suggestion on `DB_URL`.

## Verification

`TestLiveDetectedFrameworkBuildAndServing` builds locked Next.js, Vite, SvelteKit Node/static, plain HTML,
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
(`frameworks_*_test.go`, `build_recipes_test.go`).
