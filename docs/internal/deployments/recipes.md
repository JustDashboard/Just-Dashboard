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

## What the recipe refuses is said before Deploy

A recipe refuses from what the tree holds — which lockfiles sit at the root, what a manifest declares,
which packages are main — so a refusal knowable from the tree is never left for a build slot to say.
Preflight asks the recipe itself: `dryRunBuild` (`deploy/recipe_preflight.go`) runs the real
`ArtifactBuilder.Prepare` — recipe selection and Dockerfile rendering — over the checkout with every
reviewed base resolved to a placeholder digest and the generated Dockerfile never written. Nothing is
pulled, built or executed, so it keeps detection's model: repository files read as bounded data. Its
error is exactly the one `prepare_context` would return, and it becomes `recipe_unsupported` (blocked,
pointing at the setting it names), `build_root_missing` when the root directory is not in the commit, or
`recipe_check_incomplete` (warning) when a file the recipe needs could not be read. The dry run runs:

- at detection, for every recipe candidate with the settings detection proposes, so a candidate the
  recipe would refuse carries `recipeIssue` at low confidence and loses selection to a buildable one —
  except for what the operator supplies (a start command) or chooses (a package manager among competing
  lockfiles, while any one of them prepares), which preflight asks for instead;
- at Review, over the commit the draft's detection read, with the draft's own settings;
- in the advisory check and in `analyze_plan`, over the commit a deployment builds, with the saved plan.

Where it ran, it replaces the candidate's `recipeIssue`, which was decided with detection's settings
rather than the plan's; a refusal already named by a more precise finding (`package_manager_*`,
`go_version_unsupported`, `go_main_*`, `python_version_unsupported`, `start_command_missing`) is not
reported twice. Without a tree — an image, a pasted Compose file, a commit that could not be fetched —
the same checks run from the candidate's stored facts.

## Build values

Every `build`-scoped variable automatically reaches the recipe's build command through a required
BuildKit environment secret mount. Frozen run inputs supply the names at preparation and values at
build time; the bindings must agree. A variable's explicit `build.secrets` mapping to `install` limits
it to dependency installation instead. Build settings expose this choice for each build-scoped value.
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

A variable is `required` only when it is read in a form that fails without a value: `os.environ["X"]`
(not an assignment), `ENV.fetch("X")` with no default argument or block, TypeScript's
`process.env.X!`, an `if (!process.env.X) throw` guard, a key of a t3-env/zod schema with no
`.optional()`, `.default()`, `.nullish()` or `.nullable()` in its chain, a name imported from
SvelteKit's `$env/static/*`, Prisma's `env("X")` in a schema or `prisma.config.*`, and a pydantic
`BaseSettings` field with no default (when the class sets no `env_prefix`). Preflight warns once per
required name the plan does not set — no variable row, staged value, generated secret or database link
— as `variable_detected_required_<name>`, naming the database to link when one is suggested for it, and
lists the optional remainder in one `variables_detected_unset` pass. They are warnings rather than
decisions: a heuristic must not make a project uncreatable.

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

The lockfile selects npm, pnpm, Yarn or Bun. When lockfiles for more than one manager are committed,
the build setting `packageManager` chooses, then `packageManager` in `package.json`; with neither, the
recipe and preflight refuse rather than install from a lockfile the project may have abandoned. A chosen
manager must have its own lockfile. Plain HTML uses the selected source directory as
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

The candidate keeps pyproject's declared range (`pythonRequires`) and the manifest the recipe installs
from (`pythonInstall`). A family the plan selects — or the one detection chose, from `.python-version`
say — outside that range is `python_version_unsupported`: blocked on the `uv.lock` and `poetry.lock`
paths, which refuse the interpreter, and a warning on pip, which installs anyway. The action names the
newest family the range allows.

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
for, and preflight says so first: `start_command_missing` (blocked, on the start command) for the
Python, Deno and PHP recipes and a JavaScript server with no static output.

## Go

The Go recipe supports stable 1.25 and 1.26 toolchains. `build.goVersion` optionally pins a language
family or patch version. Without it, `.go-version` takes precedence over the `toolchain` and `go`
directives in `go.mod`; an older module language minimum uses the maintained 1.26 default. A selected
version cannot be older than the module requires. Resolved image digests, selected Go version and the
generated Dockerfile are recorded in build evidence. `GOTOOLCHAIN=local` prevents an unrecorded automatic
toolchain download inside the build. See the [Go toolchain rules](https://go.dev/doc/toolchain).

Which toolchain builds the module is a setting, so detection keeps the facts — `goMinimumVersion`, the
`toolchain` line (`goToolchain`) and the `.go-version` pin (`goVersionFile`) — rather than a refusal.
Preflight runs `chooseGoRecipeVersion` with the plan's own `build.goVersion` over them and raises
`go_version_unsupported` (blocked, naming the pins) only when that fails: pinning 1.26 for a module
whose `.go-version` says 1.24 clears it, as it lets the recipe build.

The main package is read the way the go command reads it, from package clauses and build constraints
and never by compiling (`deploy/go_packages.go`, shared by detection and the recipe): a file counts for
linux on the host's architecture with cgo disabled and no extra tags, judged on its `//go:build` line
(or legacy `+build` lines) and its `_GOOS`/`_GOARCH` file-name suffix, so `ignore`, `tools` and mage
files drop out; `testdata/`, `_*` and `.*` directories and nested modules (a directory with its own
`go.mod`) are not part of the module. Detection records `goMainPackages` and chooses `goPackage` by
the layout's own convention: the module root, then `cmd/<module name>`, then the one of
`cmd/{server,api,web,app}` that exists, then the only main that imports `net/http` or a known router or
RPC server. A tie asks — a `NeedsDecision` and the `go_main_ambiguous` decision on
`build.goPackage`, which a run cannot go past — and a module with no main package is a low-confidence
`Go library` candidate (`goLibrary`) with the blocked `go_main_missing`. `build.goPackage` names the
package the recipe builds (`go build … ./<goPackage>`); it must be a buildable main, or the recipe and
preflight refuse it. Without it the recipe makes the same choice detection did, and refuses an
undecided module with its main packages named.

A custom build command executes exactly as configured and must produce an executable at `/out/app`; it
decides what it compiles, so no main package is asked for. The historical detected `go build ./...`
command still executes, followed by the default output-producing build for compatibility. A custom start
command replaces the default `/app` entrypoint and runs inside the unprivileged runtime container.

This recipe uses `CGO_ENABLED=0`. A local file importing `C` that the build would compile — one whose
constraints do not already restrict it to cgo builds, beside a `!cgo` fallback — produces the refusal,
as do unsupported source versions and explicit CGO-enabling commands. Dependencies needing CGO or more
complex native-library/workspace arrangements require a Dockerfile; source scanning does not certify
all transitive dependencies. The dashboard's own required Go toolchain remains 1.26.8.

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
