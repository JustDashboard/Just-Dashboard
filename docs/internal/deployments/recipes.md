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
task. A command that carries credential material is ignored. Its port and bind flags are read like any
start command's (next section): `--port 5000` makes 5000 the candidate's port, `-b 0.0.0.0:$PORT` follows
PORT, and a gunicorn line with no `--bind` takes its bind from `gunicorn.conf.py` (or the `-c` file).

## Where the server listens, and whom it trusts

The runtime injects `PORT` and publishes the container's port to the managed proxy, which reaches it over
a Docker network address and forwards `X-Forwarded-Proto`, `X-Forwarded-Host` and `X-Forwarded-For`. Two
things break that silently: a server that ignores PORT and listens elsewhere, and one bound to
`127.0.0.1`/`localhost`/`::1`, which nothing outside its own container reaches. Both used to end as a
readiness timeout that named neither. Detection now reads, as bounded text and never by running anything
(`deploy/detect_listen.go`, `deploy/detect_network.go`):

- **The start command**, following package scripts: `-p`/`--port N`, `--port=N`, a `PORT=N` prefix,
  `--bind host:N`, `--server.port N`, `-Dserver.port=N`, `--urls URL`, `runserver [host:]N`, `$PORT` and
  `${PORT:-N}` (which follow PORT), and the defaults of servers that bind loopback or ignore PORT when
  told nothing: `vite preview` (4173), `astro preview` (4321), `uvicorn`, `hypercorn`, `daphne`,
  `flask run`, `fastapi dev`, `manage.py runserver`, `next start -H localhost`.
- **The served code**, when the command runs a file: `app.listen(4000)`, `listen(port, '127.0.0.1')`,
  `listen({ port, host })` (shorthand followed to its constant), `Bun.serve`/`Deno.serve` options, Hono's
  `serve`, a PORT read and its `|| 5000` fallback, `process.env.HOST || 'localhost'`, Fastify and Nest's
  Fastify adapter (both bind localhost when given no host); Go main-package `Run(":8080")`,
  `ListenAndServe`, `Serve`, `net.Listen`, the `Addr:` of an `http.Server`/`fasthttp.Server` literal (a
  client's options, such as go-redis's, are not listeners) and gin's argument-less `Run()` (PORT, else
  8080) — test files excluded; Rust `bind("127.0.0.1:3000")`, `bind(("0.0.0.0", 8080))`,
  `bind(&"…".parse()…)`, `SocketAddr::from(([0,0,0,0], N))`, warp's `run` (never `String::from` or a
  client's `new`); Python `app.run(...)`, `socketio.run(...)` and `uvicorn.run(...)` (all 127.0.0.1
  without `host=`, and 5000, 5000 and 8000 without `port=`, since none reads PORT); Vert.x `listen(8888)`,
  Javalin `start(7070)`, Ktor `embeddedServer(port = …)`; `app.Run("http://localhost:5000")`, `UseUrls`,
  `ListenLocalhost` in `Program.cs`.
- **Configuration**: `server.port`/`server.address` (Spring, Helidon), `quarkus.http.port`,
  `micronaut.server.port` in `application.properties`/`.yml`, Ktor's `application.conf`, `Rocket.toml`,
  `appsettings(.Production).json` Kestrel endpoints and `Urls`, `gunicorn.conf.py`, a Dockerfile's final
  `ENV PORT=`/`ARG PORT=` when it has no single `EXPOSE`, and Phoenix's `config/runtime.exs` PORT default.

A listen address is read only when it is one whole string literal, or an identifier assigned exactly
one: `":" + port` names neither a port nor a host. A listen call whose address the scan cannot read —
`http.Server{Addr: ":" + port}` with `srv.ListenAndServe()`, `cfg.Addr`, an address from a variable — may
well be on every interface, so it counts as an open listener. A listener on loopback beside such a call,
or beside a PORT read while it fixes a port of its own, is a side listener (a debug or admin endpoint):
its port is never the candidate's, and its loopback is a warning, never certain. A loopback
`http.ListenAndServe(…, nil)` in a main package that imports `net/http/pprof` is the profiling endpoint
and is not read at all.

The port precedence is the command's explicit port, then a configured port (a suggestion when the
recipe bridges it onto PORT, fixed otherwise), then a code literal when the code never reads PORT, then
a PORT read's own fallback, then the framework default. The candidate carries the result as `listen`
(`port`/`portFrom` for a port the source fixes, `readsPort`, `loopback`/`loopbackFrom`,
`loopbackCertain`, `loopbackRecipeFix`, `loopbackVariable`, `unbridged`), each fact naming its file and
line, and evidence lines say the same. Preflight re-checks it against the plan — a replaced start command
is read on its own — and raises the findings below. A loopback read from code is certain only for the
code the detected plan runs: a start command that no longer runs the same package script (a
package-manager switch keeps it), a different build method, and every Dockerfile candidate (whose image
runs its own `CMD`, and whose code loopback on a port other than its `EXPOSE` is dropped as fronted) turn
the blocker into a warning, which is also how an operator stops a detection they know to be wrong from
blocking.

| Code | Severity | When |
| --- | --- | --- |
| `listen_loopback` | blocked when the loopback bind is certain (a literal or a served default with no other listener), else warning | the server binds loopback and nothing in the plan moves it |
| `listen_loopback_moved` | pass | the recipe (`HOST`, `UVICORN_HOST`, `FLASK_RUN_HOST`, `ROCKET_ADDRESS`, a Kestrel bridge) or a plan variable moves it |
| `port_hardcoded` | warning | the source fixes a port the plan's internal port differs from |
| `port_from_source` | pass | the plan's port is the one the source fixes, or the server follows PORT |
| `listen_endpoints_unbridged` | warning | several Kestrel endpoints, or an HTTPS one, keep their own addresses |
| `start_command_dev_server` | warning | a Python recipe runs `runserver`, `flask run`, `fastapi dev` or `--reload` |
| `proxy_headers_trusted` | pass | the recipe or a plan variable makes the app believe the proxy's forwarded headers |
| `forwarded_headers_untrusted` | warning | the port is reachable without the proxy (a `0.0.0.0`/`::` bind, host networking, or the application's port published again on every interface): the recipe's trust is withdrawn, and a plan variable such as `AUTH_TRUST_HOST` is named as still trusted |
| `proxy_trust_variable_missing` | warning | a variable detection proposed for proxy trust (`AUTH_TRUST_HOST`) was removed |
| `public_url_variable_missing` | warning | `NEXTAUTH_URL` (next-auth 4) is missing or empty; the action names the value the primary domain gives |
| `public_url_variable_stale` | warning | `NEXTAUTH_URL` names another host than the primary domain (only the host is echoed) |
| `request_body_limit` | pass | the plan has a route: the largest upload the proxy lets through, zero being 64 MB on nginx and no limit on Caddy |

What the recipes write so the server listens where the proxy reaches it and believes the headers it
sends, without asking:

- **Every Node server** gets `ENV HOST=0.0.0.0` in its runtime stage — Nuxt 2, Nitro, adapter-node,
  Adonis and `process.env.HOST || 'localhost'` bind it, and it is inert where unused (Next.js does not
  read `HOST`). **SvelteKit adapter-node** also gets `PROTOCOL_HEADER=x-forwarded-proto`,
  `HOST_HEADER=x-forwarded-host`, `ADDRESS_HEADER=x-forwarded-for` and `XFF_DEPTH=1`, so form actions are
  not refused as cross-site and `getClientAddress` is the visitor's. adapter-node's `getClientAddress`
  throws when `ADDRESS_HEADER` names a header a request lacks, so the setting is only kept where every
  request carries it: the runtime withdraws it (below) from a release with no route, and a readiness
  probe that connects to the candidate directly must send `X-Forwarded-For`, or an app that calls
  `getClientAddress` in its hooks answers the probe with 500. A start whose script is a bare
  `vite preview`/`astro preview` becomes `<runner> vite preview … --host 0.0.0.0`.
- **Auth.js**: `next-auth` 5 (or `beta`) and `@auth/*` get a plain plan variable `AUTH_TRUST_HOST=true`,
  without which every request is refused as an untrusted host; `next-auth` 4 gets `NEXTAUTH_URL` with the
  domain template `{{scheme}}://{{hostname}}`, which follows the primary domain, the suggested one
  included. Both are visible, removable plan variables (`networkVariables` on the candidate) for every
  build method, including a Dockerfile beside the package, rather than image settings. A plan with no
  domain saves no `NEXTAUTH_URL` at all — next-auth 4 fails every sign-in request on an empty one — and
  its environment row stays askable; preflight warns while it is missing. A domain changed later in the
  project's settings does not rewrite it; `public_url_variable_stale` says so.
- **Python** images set `FORWARDED_ALLOW_IPS=*` (gunicorn, uvicorn and the uvicorn inside Gradio,
  Chainlit and NiceGUI read it; their default trusts only 127.0.0.1, and the proxy is not 127.0.0.1),
  `UVICORN_HOST=0.0.0.0` and `FLASK_RUN_HOST=0.0.0.0` (both default to 127.0.0.1; an explicit `--host`
  wins).
- **ASP.NET Core** sets `ASPNETCORE_FORWARDEDHEADERS_ENABLED=true`; **Spring Boot** sets
  `SERVER_FORWARD_HEADERS_STRATEGY=framework`; **Quarkus** sets `QUARKUS_HTTP_PROXY_PROXY_ADDRESS_FORWARDING`
  and `QUARKUS_HTTP_PROXY_ALLOW_X_FORWARDED`. OAuth/OIDC redirect URIs and secure cookies then use
  https; detection names the sign-in packages (`Microsoft.AspNetCore.Authentication.*`,
  `Microsoft.Identity.Web`, `spring-boot-starter-oauth2-client`/`-security`) that depend on it.
- A server that reads `HOST` with a loopback fallback (`process.env.HOST || 'localhost'`,
  `env::var("HOST").unwrap_or("127.0.0.1")`) in a build whose image does not set HOST — a Dockerfile, a
  Go or Rust recipe — gets a `HOST=0.0.0.0` plan variable instead. HOST is never injected globally: some
  applications use it as their public hostname.

Trusting forwarded headers is safe only while the proxy fronts the release alone: it replaces whatever a
client sent. The release records which trust settings its recipe image sets. When the release has no
route (nothing adds the headers), or its port is reachable directly (a `0.0.0.0`/`::` bind, host
networking, or the application's port published again on every interface), the runtime writes the
withdrawn value of each recorded setting (`FORWARDED_ALLOW_IPS=127.0.0.1`,
`ASPNETCORE_FORWARDEDHEADERS_ENABLED=false`, `SERVER_FORWARD_HEADERS_STRATEGY=none`, the Quarkus switches
`false`, SvelteKit's `PROTOCOL_HEADER` and `ADDRESS_HEADER` empty and `HOST_HEADER=host`, adapter-node's
own default — before 5.5 an empty one made every origin `https://undefined`) unless the plan sets the
variable itself, and preflight says so. A repository's own Dockerfile, a pulled image and an adopted
container record nothing, so what their authors bake in is never overridden.

When a candidate fails its readiness gate anyway, the diagnosis also reads its listening sockets: the
runtime owner runs `cat /proc/net/tcp /proc/net/tcp6` inside the candidate's own container (closed
argv, bounded output, only the parsed addresses kept; an image without `cat` reports nothing). A
candidate listening only on loopback gets the cause `loopback_only`, named in the failure message even
when the application printed nothing.

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
| Astro | `astro` (+ `@astrojs/node`) | site `dist` / server, 4321 | `node ./dist/server/entry.mjs` (every Node server's runtime stage sets `HOST=0.0.0.0`); a provider adapter is a decision |
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
literal TCP `EXPOSE` in the final stage supplies the suggested port. Without one, the final stage's
`ENV PORT=N`/`ARG PORT=N`, then what the source at that root says about its listener, then a Phoenix
`config/runtime.exs` PORT default supply it; otherwise the port stays unset for the operator. A
Dockerfile inherited base's unrecorded exposed port is not guessed.

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
| Django (`manage.py`) | `python manage.py migrate --noinput && gunicorn <project>.wsgi:application --bind 0.0.0.0:${PORT:-8000}`, with `collectstatic` before it when WhiteNoise is installed; `uvicorn <project>.asgi:application` when only an ASGI module exists and uvicorn is declared | 8000 |
| FastAPI | `uvicorn <module>:<object> --host 0.0.0.0 --port ${PORT:-8000}` for the `= FastAPI(` object found | 8000 |
| Flask | `gunicorn --bind 0.0.0.0:${PORT:-8000} <module>:<object>`, or `'<package>:create_app()'` for a factory | 8000 |
| Streamlit | `streamlit run <script> --server.port ${PORT:-8501} --server.address 0.0.0.0 --server.headless true` | 8501 |
| Gradio | `python <script>` with `GRADIO_SERVER_NAME=0.0.0.0` in the image | 7860 |

The detected commands read `${PORT:-N}` rather than a fixed port, so changing the application port in
Build settings moves the server with it. A framework whose application object is not in an entry file
keeps the port and asks for the module. A Django project answers only the hosts its settings allow; `ALLOWED_HOSTS` read from the environment is
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

A module whose main package serves HTTP — `gin`, `echo`, `fiber`, `chi`, `gorilla/mux` or connect in
`go.mod`, or `net/http` in the main package — is a web candidate with the framework and the port its
main package names (`Run(":8081")`, `ListenAndServe(":9000", …)`), or 8080 with the evidence "follows
PORT" when it reads PORT, so it gets a readiness gate and a suggested hostname instead of an unset port.
That gate is the detected readiness check ("Readiness, workers and start commands"), which counts any
answer from an API with no page at `/`; a 2xx-only check on `/` would fail it. A `net/http` main package
(no web framework in `go.mod`) whose every registered route is `/metrics`, `/debug/…` or a health
endpoint is a worker with a side port and keeps the service profile, as does a gRPC or other listener,
each with its port. Several main packages are the recipe's own question and name no port.

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
still leave `target/release/<binary>`. `loco-rs` (5150, recognised before the axum it
is built on), `axum` (3000), `actix-web` (8080), `rocket` (8000), `warp` (3030), `poem` (3000) and `salvo`
(5800) mark a web service with the framework's conventional port. The served binary's `src/main.rs` (or
`src/bin/<binary>.rs`) and `Rocket.toml` are read for the port and bind; the "confirm the port" decision
stays only when none of them names one. Rocket, whose default address is 127.0.0.1, runs as
`exec env ROCKET_PORT=${PORT:-8000} /app` with `ROCKET_ADDRESS=0.0.0.0` (both outrank `Rocket.toml`);
Loco starts with `/app start --binding 0.0.0.0 --port ${PORT:-5150}`. A crate with no framework is a
worker that asks. A workspace without a root package is a
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
project is a worker that asks. These frameworks do not read PORT, so the default start command bridges
it into the variable that outranks `application.properties`/`.yml`: `exec env SERVER_PORT=${PORT:-8080}
java -jar /app/app.jar` for Spring Boot and Helidon, `QUARKUS_HTTP_PORT` for Quarkus,
`MICRONAUT_SERVER_PORT` for Micronaut (`exec` keeps java as PID 1). A configured `server.port` becomes the
suggested port. Vert.x, Javalin and Ktor's embedded server fix their port in code, which detection reads
(`listen(8888)`, `start(7070)`, `embeddedServer(port = …)`); Ktor's `application.conf` with
`port = ${?PORT}` follows PORT. A multi-module Maven project is a `recipe_unsupported` finding: set the
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
/app/<Assembly>.dll`. An `appsettings.json`/`appsettings.Production.json` that names Kestrel endpoints or
`Urls` outranks that variable, so the bridge also sets the one endpoint's
`Kestrel__Endpoints__<Name>__Url=http://+:${PORT:-8080}`, or `URLS=http://+:${PORT:-8080}`; several
endpoints, or an HTTPS one, cannot all move to one port and are named in `listen_endpoints_unbridged`. A
URL passed in `Program.cs` (`app.Run("http://localhost:5000")`, `UseUrls`) outranks everything and is
read as a fixed port and, on loopback, a blocker. A web project listens on 8080; a console program asks
whether it serves.

## Deno

Configuration parsing accepts JSONC comments and trailing commas without rewriting URLs, escaped
quotes or comment-like text inside task strings. Invalid configuration does not contribute partially
parsed tasks.

`deno.json`/`deno.jsonc` (comments and trailing commas tolerated) supplies the `build` and `start`
tasks; without a start task the conventional entry file (`main.ts`, `server.ts`, `mod.ts`, `main.js`,
`server.js`, `main.tsx`, `index.ts`, `app.ts`, `src/main.ts`, `src/server.ts`, `src/index.ts`) is run with
`--allow-all`. `deno install` (`--frozen` with a `deno.lock`) caches the imports on `denoland/deno:alpine`
before the build task runs. A `fresh` import names the framework. The port is read from the start task
(`deno serve --port 3000`, else `deno serve`'s 8000) or the served file (`Deno.serve({ port: 3000 })`, Oak's
`listen({ port })`, a `Deno.env.get("PORT")` read); with nothing readable it is `Deno.serve`'s default of
8000, stated as evidence rather than asked as a question.

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
