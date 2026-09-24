# Automatic recipes and serving defaults

`just-dashboard-recipes-v3` prepares immutable Dockerfiles using digest-pinned catalogue bases. Build
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
engine; a Python application whose SQLite default is only the fallback of a variable it reads
(`dj-database-url`, `os.environ.get("DATABASE_URL", "sqlite:///…")`, a pydantic setting) is offered
PostgreSQL on that variable, which takes the file out of use.

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
fails the start instead of dropping it. That is why a push is never a pass: preflight raises
`schema_push_unversioned` (a warning in place of `schema_step`, linked database or not) whenever the start
command, a release task or the package's own start script pushes, with the command that commits
migrations instead (`prisma migrate dev`, `drizzle-kit generate`). When a push does refuse, the failed
readiness gate names it — `schema_push_refused`, from Prisma's `--accept-data-loss` message or Drizzle's
rename and data-loss prompts — rather than a bare timeout. The tool must be installed by the lockfile; the
runtime stage copies the build's `node_modules`, so a devDependency is available to the start command.

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

A framework whose application object is not in an entry file keeps the port and asks for the module.

Python migration tools are recognised the way the Node ones are, and share their preflight findings:
Django (`python manage.py migrate --noinput`, already the default start's first step), Alembic
(`alembic.ini` beside the manifest or one directory down, with the `alembic` dependency: `alembic upgrade
head`, `-c <ini>` when it is not at the root), Flask-Migrate (`migrations/alembic.ini` with
`flask-migrate`: `flask --app <module> db upgrade`) and Aerich (`[tool.aerich]`: `aerich upgrade`). The
command is chained before a detected start; a `Procfile` web process is the repository's own and is not
rewritten, so a linked database without the step raises `schema_step_missing` instead. A start command
that runs a committed `prestart.sh` (or `scripts/prestart.sh`) which applies the migrations counts as the
step. A
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

The runtime stage runs the binary at `/app` as the unprivileged `app` user from its own home,
`/home/app`, so a relative path the service opens resolves somewhere it can write. The root-level files a
service reads at runtime are copied there, owned by that user: `templates`, `views`, `static`, `public`,
`assets`, `migrations`, `locales`, `i18n`, `config` and `config*.{yaml,yml,toml,json}` when they exist,
plus the directory named by any literal path the sources hand to `LoadHTMLGlob`, `ParseGlob`,
`http.Dir`, `Static`/`StaticFile`, `os.DirFS` or a `file://` migration source (read as text under a
fixed budget; symlinks are never copied). `/home/app/data` is created and owned by `app`, so a volume
mounted there starts writable. Rust uses the same stage, reading `ServeDir`, `ServeFile`, `Tera::new`,
`NamedFile` and actix `Files` literals.

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
/app/<Assembly>.dll`. A web project listens on 8080; a console program asks whether it serves. `/app`,
`/app/data` and the Data Protection key ring `/home/app/.aspnet/DataProtection-Keys` belong to the `app`
user, so a relative `app.db` and a volume on either directory are writable.

EF Core migrations (a `Microsoft.EntityFrameworkCore.Design` or `.Tools` reference and a committed
`*ModelSnapshot.cs`) are recorded as the `ef-core` schema tool. Source that calls `Database.Migrate()`,
`MigrateAsync()` or `EnsureCreated()` applies them itself; otherwise a linked database raises
`schema_step_missing`, whose action is to call `Database.Migrate()` at startup — the EF command-line
tools and the design-time project are not in the runtime image, and a release task runs on the host.

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
recipe links the public disk the way `php artisan storage:link` would (`public/storage` →
`/app/storage/app/public`, unless the repository commits one), without booting the application in
the build. The
configure form mints `APP_KEY` for a Laravel import (`base64:` over 32 random bytes) and offers a
**Generate** button on any variable whose name ends in `SECRET`, `KEY` or `SECRET_KEY_BASE`. Laravel's
`.env.example` names its engine in `DB_CONNECTION`, which becomes a database suggestion on `DB_URL`.

## Persistent state

An application that writes to its own filesystem loses it when the next release replaces the container:
a SQLite database starts empty, uploads disappear, a key ring is regenerated and signs everyone out.
Detection (`deploy/detect_state.go`) reads, as bounded text and under its own budget, where that state
lives, and records it on the candidate as `persistentPaths`: the kind (`sqlite`, `uploads`, `storage`,
`volume`, `keys`), where it is written today, the directory a managed volume can stand on (`target`), and
— when the location is read from a variable — the variable and the value that moves it there.

A volume target never hides what the image ships. It is a directory the framework owns (Rails'
`storage/`, Laravel's `storage/`, Strapi's `.tmp` and `public/uploads`), a conventional data directory
(`data`, `uploads`, `media`, `instance`, `pb_data`, …) the repository fills with nothing but
placeholders, a path a `VOLUME` declares, or the image's own data directory reached through a variable:
`/data` for the recipes that run as root (Node, Python, PHP), `/home/app/data` for Go and Rust and
`/app/data` for .NET, which those recipes create owned by their unprivileged user. An image built by its
own Dockerfile as a non-root user is only given directories the repository commits, because a volume
over a directory the image lacks is owned by root. A file that sits beside code (`/app/db.sqlite3`,
Prisma's `prisma/dev.db`) gets no volume — mounting there would hide the code or the migrations — and is
reported instead.

| Source | State | Plan |
| --- | --- | --- |
| Prisma `provider = "sqlite"`, `url = env("X")` (or Prisma 7's `prisma.config`) | the database | volume at `/data`, `X=file:/data/<file>` |
| `better-sqlite3`, `sqlite3`, `@libsql/client`, Payload's SQLite adapter, Drizzle's `sqlite`/`turso` dialect | a file named by a variable, or a literal in the source | volume and variable in the example's own shape (`file:` for libSQL); a literal only in its own directory |
| Strapi | `.tmp/data.db`, `public/uploads` | volumes on both |
| `multer` `dest`/`destination`, `UPLOAD_DIR`-style variables | uploads | the directory, or `/data/uploads` through the variable |
| Django `django.db.backends.sqlite3`, `MEDIA_ROOT` | the database, media | the file's own directory when it has one; `NAME` or `MEDIA_ROOT` read from a variable moves to `/data` |
| SQLAlchemy `sqlite:///…` (Flask-SQLAlchemy 3 resolves relative paths in `instance/`) | the database | its own directory, or a warning |
| Laravel with `DB_CONNECTION=sqlite` (or none, from Laravel 11) | `database/database.sqlite`, sessions, cache, queue | volume at `/app/storage`, `DB_DATABASE=/app/storage/database.sqlite` when `config/database.php` reads it |
| Laravel with Filament, Media Library or `FILESYSTEM_DISK=public` | uploads on the local disks | volume at `/app/storage` |
| Statamic | `content/`, `users/` | reported: they are the repository's own files |
| Rails `database.yml` production `sqlite3`, Active Storage `Disk` + `:local`, Kamal `volumes:` | `storage/` | volume at `<final WORKDIR>/storage` |
| Go `modernc.org/sqlite`, `mattn/go-sqlite3`, … and Rust `rusqlite`, `sqlx`/`diesel` with SQLite | a file named by a variable, or a literal | `/home/app/data` through the variable (keeping `sqlite://`, `file:` and the query), a literal only under `data/` |
| PocketBase | `pb_data` beside the binary | reported, with `serve --dir=/home/app/data` |
| `Microsoft.EntityFrameworkCore.Sqlite` with an `appsettings.json` `Data Source=` | the database | `/app/data` through `ConnectionStrings__<name>` (only `Cache`, `Mode`, `Foreign Keys`, `Pooling` and `Default Timeout` options are kept) |
| ASP.NET cookie auth, Identity, Razor Pages, MVC views, antiforgery or Blazor without `PersistKeysTo*` | the Data Protection key ring | volume at `/home/app/.aspnet/DataProtection-Keys` |
| Dockerfile `VOLUME` (final stage, inherited from an earlier stage), a local image's declared volumes | the declared path | a volume on it |

The configure form turns every target into a managed named volume (`<slug>-<hash>-<purpose>`, the shape a
template's volumes have), with a storage dependency whose purpose Review shows, fills the moving
variable's row (`Keeps it on the volume at …`), and releases stop-first, because preflight refuses
candidate-first activation with a writable mount. A variable the operator changes or empties is read
again by preflight, never echoed: it compares where the state would be written with the plan's writable
mounts. State no mount keeps is a warning before deploy — `sqlite_ephemeral`, `uploads_ephemeral`,
`persistent_path_unmounted`, `declared_volume_unmounted`, `dotnet_data_protection_ephemeral` — whose
action is the volume and variable detection proposed, a server database through the variable a SQLite
fallback is read from, or the change the source needs. State that is kept passes as
`persistent_state_kept`, and `backup_policy_missing` then asks for a backup. A linked server database in
the variable a SQLite default falls back from takes the file out of use and raises nothing. A container
that cannot write its SQLite file fails its readiness gate with `sqlite_not_writable` named.

Limits: an image's declared volumes are read only when the image is already on this host (the registry
manifest does not carry its configuration), and a base image's own `VOLUME` behind a Dockerfile is not
seen. Retired containers still keep their anonymous volumes, because one may hold the only copy of data a
plan without a mount wrote.

## Seed data

A project that creates its first administrator or lookup rows in a seed records the command
(`seedCommand`, and `seedResets` when the seed deletes or truncates first): Prisma's `prisma.seed` or
Prisma 7's `migrations.seed` (`<runner> prisma db seed`), a `db:seed`, `seed` or `prisma:seed` script,
Laravel's `DatabaseSeeder` when it does more than the skeleton's test user (`php artisan db:seed
--force`), and a `db/seeds.rb` with code (`bin/rails db:seed`). When the plan links a database or keeps
SQLite on a new volume, and neither the start command nor a release task seeds, preflight raises
`seed_available` (a warning) naming the command to run once from the project's console after the first
release. It is never run automatically: a release task runs on the host, not in the application image,
and a seed that clears tables must not run on every release.

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
(`frameworks_*_test.go`, `build_recipes_test.go`). Persistent state, schema tools, seeds and their
findings are table-tested per stack in `detect_state_test.go`, `detect_schema_test.go`,
`preflight_state_test.go` and `recipe_runtime_files_test.go`.
