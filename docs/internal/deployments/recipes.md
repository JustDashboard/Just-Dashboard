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
are public, even if encrypted in the dashboard. Quick setup labels such rows `public`, the variable
editor gives a new browser-prefixed name Build and Runtime scope as a plain value, and preflight warns
when one is secret-shaped and refuses a live Stripe secret key or a Supabase service_role token.
See the upstream [Next.js environment contract](https://nextjs.org/docs/app/guides/environment-variables)
and [Vite environment contract](https://vite.dev/guide/env-and-mode).

## Environment discovery and database suggestions

Detection lists the variables a source expects so the configure form opens with them as rows, and says
how each is supplied so an imported project arrives configured rather than as blank rows. Names come
from a documented template (`.env.example`, `.env.sample`, `.env.template`, `.env.dist`, `example.env`
and the like) with the template's own value kept as the row's placeholder when it is not
credential-shaped; from the committed files a framework loads with real values (`.env`, `.env.local`,
`.env.production`, `.env.production.local`); from configuration templates (Spring and Quarkus
`application*.properties|yml` placeholders `${X}` and `${X:default}`, HOCON `application.conf`
`${X}`/`${?X}`, Rails ERB in `config/*.yml`, .NET `appsettings*.json` connection strings and empty
leaves as `CONNECTIONSTRINGS__NAME` and `SECTION__KEY`); and from the code's own reads of its
environment — `process.env.X`, `import.meta.env.X` (Vite's own `MODE`, `DEV`, `PROD`, `SSR`,
`BASE_URL` excepted), `Bun.env.X`, `Deno.env.get("X")`, `os.environ["X"]`, `os.getenv("X")`,
`env("X")`, `config("X")`, `os.Getenv("X")`, `getenv("X")`, `ENV["X"]`, `System.getenv("X")`, Spring
`@Value("${X}")`, Scala `sys.env`, Rust `std::env::var`, `dotenvy::var`, `env!`/`option_env!` and clap
`env = "X"`, .NET `Environment.GetEnvironmentVariable`, `Configuration["A:B"]` and
`GetConnectionString("N")`, Elixir `System.get_env`/`fetch_env!`, Dart `Platform.environment`, Swift
`Environment.get`, Haskell `getEnv`/`lookupEnv`, Gleam `envoy.get`, Go `env:"X"`/`envconfig:"X"`
struct tags, pydantic-settings `BaseSettings` fields (upper-cased, behind `env_prefix` unless an alias
names them), django-environ's `env.db()`/`env.cache()` and `dj_database_url.config()` (implicit
`DATABASE_URL`/`CACHE_URL`), the bare `environ` imported from `os`, SvelteKit's `$env/static/*` imports
and `$env/dynamic/*` reads, t3 `createEnv` schemas, Astro `envField` schemas, AdonisJS `Env.schema`
and Nuxt `runtimeConfig` keys (`NUXT_API_SECRET`, `NUXT_PUBLIC_API_BASE`). Tests, fixtures,
documentation, migrations and generated files are not read, and names the platform supplies (`PORT`,
`NODE_ENV`, …) are left out; `DEBUG` is left out of JavaScript, where it is the `debug` package's, and
kept from Python, where it is the framework's debug mode. The scan has its own budget (400 files,
3 MiB) apart from detection's limits: a large repository stops contributing names quietly and is never
reported truncated for it. The fact files classification reads whole — `Gemfile.lock`, `mix.exs`,
Rails' credentials, `config/puma.rb`, `production.rb` and `database.yml`, Phoenix's migrate overlay,
codegen configs — have a budget of their own (64 files, 1 MiB), so an `app/` tree walked first cannot
crowd them out. The root's own template comes first in file order, then a committed `.env`,
then what the repository documents above the root, then code references by name. Each row carries where
it was read; a detected row left empty and set up by nothing is skipped at submit, not set to an empty
value, and the form says how many are unset.

A committed real env file contributes its names and two facts derived from a value, never the value:
whether it points at loopback (which inside the container is the app itself) and, for a database URL,
the engine its scheme names.

Each variable carries how it was read. `phase: build` marks a value read while the build runs — a
framework config file (`next.config.*`, `vite.config.*`, `prisma.config.*`, …), a static env import, a
compile-time macro, a browser prefix. `browserInlined` marks a value the framework compiles into the
JavaScript every visitor downloads: the prefix the root's own framework inlines (`NEXT_PUBLIC_`,
`VITE_`, SvelteKit's and Astro's `PUBLIC_`, `REACT_APP_`, `NUXT_PUBLIC_`, `EXPO_PUBLIC_`, `GATSBY_`,
`VUE_APP_`, listed on the candidate as `browserPrefixes`), or a Vite `define` / `next.config` `env`
block. `required` marks a read with no default where the application starts or builds — a Python
subscript or default-less `env()`/`config()` at module level of a settings, config or entry module, a
`BaseSettings` field with no default, Elixir `fetch_env!` or `|| raise` in `config/`, Rails
`ENV.fetch` without a default in `config/`, a Spring or HOCON placeholder without a default, a Go
`required` tag, a static env import, Prisma 7's `env()` in `prisma.config.*` (which `prisma generate`
loads during the build), a t3, Astro or AdonisJS schema entry without `optional`. A Python default may
be positional: django-environ's typed readers (`env.bool("X", False)`) and python-decouple's
`config("X", 30)` take it second, while a bare `env()`, `env.list()` and Starlette's `config()` take a
cast there, so a type name in that position is a cast and anything else is a default. Only Spring's
base `application.*` and `application-prod*` decide requiredness: a `dev`, `local`, `test` or `ci`
profile is not read at all, and any other profile is listed without it. Mix's `config/dev.exs` and
`config/test.exs` are not read, since a release never loads them.
`requiredRead` is the same form somewhere that may only run on one path. `localhostIn` names the
committed file whose value points at loopback.

`setup` says how the dashboard supplies the value when nothing is typed (`detect_variables.go`):

- `generate` — a self-issued secret, minted at commit on the server in its framework's shape and stored
  sealed like any secret variable: Laravel's `APP_KEY` (`base64:` over 32 bytes), Auth.js
  `AUTH_SECRET`/`NEXTAUTH_SECRET`, Better Auth, Payload, Rails' and Phoenix's `SECRET_KEY_BASE`,
  Django's `SECRET_KEY`/`DJANGO_SECRET_KEY` (only when its settings read that exact name), Flask's,
  Strapi's `APP_KEYS` (four keys) and salts, Directus `KEY`/`SECRET`, Medusa, AdonisJS, and
  `SESSION_SECRET`/`COOKIE_SECRET`/`JWT_SECRET` beside the session or JWT library that signs with them.
  Every rule is an exact name gated by the dependency that issues it to itself, so a provider's
  credential — `STRIPE_SECRET_KEY`, `CLERK_SECRET_KEY`, `SUPABASE_JWT_SECRET`, `AUTH_GITHUB_SECRET` —
  is never generated. A secret the framework reads internally (`SECRET_KEY_BASE` for Rails, Auth.js's
  secret) is added even when no line of code names it.
- `domain` — a self-URL bound to the planned domain through `domainTemplate`: `AUTH_URL`,
  `NEXTAUTH_URL`, `BETTER_AUTH_URL`, SvelteKit adapter-node's `ORIGIN`, Laravel's `APP_URL`, Phoenix's
  `PHX_HOST` (the bare hostname), Django's `CSRF_TRUSTED_ORIGINS` and the variable its settings read
  `ALLOWED_HOSTS` from (with `localhost` and `127.0.0.1` kept so the readiness probe answers), joined
  with the separator the settings split it on — commas for `env.list`, `Csv()` or `.split(",")`, spaces
  for `.split(" ")` or `.split()`. A list joined the other way is one host Django never matches, so
  when the settings do not say, the variable is left unbound and noted. `SITE_URL`, `PUBLIC_URL`, `BASE_URL` and
  `NEXT_PUBLIC_APP_URL`/`SITE_URL`/`BASE_URL`, and any variable whose example is an http(s) URL on
  loopback at the application's own port. Another port or a non-http scheme — `API_URL` on 8000,
  `DATABASE_URL` — is never rebound.
- `default` — a harmless documented setting: `LOG_LEVEL` (pinned to `info`), `SESSION_DRIVER`,
  `DB_CONNECTION`, `DB_CLIENT`, `DATABASE_CLIENT` from the template; `HOST=0.0.0.0` where `HOST` is read
  within a few lines of a listen or bind call, or falls back to `0.0.0.0` (reading `PORT` beside it
  proves nothing — links are built from both); Django or Flask debug off (`False`, or `0` for an
  `int()` read) on whichever variable the settings read it from (`DJANGO_DEBUG`) where they turn debug
  on by default — a settings module named `local`, `dev`, `test` and the like is a developer's and is
  not read for debug or a committed key; `SOLID_QUEUE_IN_PUMA=true` where Puma loads Solid Queue's plugin;
  `AUTH_TRUST_HOST=true` for Auth.js v5, whose only ingress is the managed proxy. `NODE_ENV`, `PORT`
  and loopback URLs are never filled.
- `paste` — a secret only the operator holds: Rails' `RAILS_MASTER_KEY` beside committed credentials,
  required when production sets `require_master_key`.

The configure form declares each set-up variable in the plan (a generated secret, a plain value that
follows the domain until edited, a default, a required name) and the environment row says so; a typed
value wins at commit, and removing the row removes the declaration.

The engines a source connects to are read from its dependencies (`pg`, `mysql2`, `mongoose`, `ioredis`,
`psycopg`, `asyncpg`, `pymongo`, `redis`, `github.com/jackc/pgx`, …), from `Gemfile.lock`, `mix.exs`,
`Cargo.toml` (sqlx, diesel and sea-orm features included), Maven and Gradle coordinates, `*.csproj`
package references, `Package.swift`, `pubspec.yaml` and Composer (`predis/predis`, `ext-pdo_pgsql`,
…), from a Prisma datasource provider, from variable names and example URLs (`REDIS_URL`,
`MONGODB_URI`, `postgres://…`), and from a committed real env file's URL scheme. Each engine quick
setup can provision — `postgres`, `mysql`, `mariadb`, `redis`, `mongodb` — is offered as one button
that opens the database sheet on that engine and the variable the connection belongs in (`DATABASE_URL`
for a relational engine the dependencies name, or the documented name). SQLite names no engine; a
Python application whose SQLite default is only the fallback of a variable it reads (`dj-database-url`,
`os.environ.get("DATABASE_URL", "sqlite:///…")`, a pydantic setting) is offered PostgreSQL on that
variable, which takes the file out of use. A suggestion also carries:

- `format` — the connection shape its consumer parses: `jdbc` for Spring (`SPRING_DATASOURCE_URL`, or
  the placeholder the `spring.datasource.url` key reads — by its full path, never another `url:`),
  Quarkus (`quarkus.datasource.jdbc.url`, `%prod.` included) and Micronaut, `jdbc-mariadb` for the
  same over MariaDB Connector/J, which refuses `jdbc:mysql://`, `adonet` for .NET
  (`CONNECTIONSTRINGS__<NAME>` from `GetConnectionString`), `mysql2` for Rails before 7.2 on MySQL. The
  link requests that shape, and the typed reference records it (`${{database.5.jdbc}}`). Relinking a
  variable from Settings → Databases keeps the shape its previous reference asked for.
- `extensions` — `vector` or `postgis`, read from a Prisma `extensions` list or `Unsupported` type, the
  first migrations' `CREATE EXTENSION`, Rails `enable_extension`, a drizzle `vector()` column, or a
  library that needs one (pgvector, langchain-postgres, GeoAlchemy2, neighbor, PostGIS adapters). Quick
  setup then preselects PostgreSQL with pgvector or PostGIS, since the official image ships neither.
- `hosted` — a driver that only speaks its provider's protocol (Neon's HTTP or WebSocket driver,
  `@vercel/postgres`, PlanetScale's HTTP driver, Prisma Accelerate, Upstash REST). An import seen in the
  source is conclusive; a dependency alone is when no TCP driver is also declared. The form asks for the
  provider's connection string and offers no local engine.
- `alsoVariables` — Rails 8's `CACHE_`, `QUEUE_` and `CABLE_DATABASE_URL` when `config/database.yml`
  declares those production databases; the link sets each to the same server under
  `<database>_<name>`.

Laravel's `DB_CONNECTION` suggestion targets `DB_URL` from Laravel 11 and `DATABASE_URL` before it,
whichever the application's own `config/database.php` reads, else the framework constraint.

Preflight answers all of this before the first build (`preflight_variables.go`): a cleared generated
secret (`secret_unset_*`) and the generated ones (`secrets_generated`); a self-URL with no domain,
bound to the planned one, or naming another host (`public_url_unbound_*`, `public_url_bound_*`,
`public_url_mismatch_*`, and `phoenix_host_*` for `PHX_HOST`); a static env import or compile-time read
with no build-scoped value (`build_variable_missing_*`, blocked) and browser variables that would build
as undefined (`browser_variables_unbuilt`); a value, or a committed file's fallback, that points at
loopback (`variable_points_to_localhost_*`, `build_inlines_localhost_*`, `host_variable_loopback_*`,
which gives way to `listen_loopback` when that finding already names the variable); a Django host list
left unbound because its separator is unknown (`allowed_hosts_unbound_*`); a secret compiled into the
browser bundle (`public_variable_secret_*`, blocked for a live Stripe secret
key or a Supabase service_role token); documented or required-form variables left unset
(`documented_variables_unset`, `variable_likely_required`); Rails without its secret or master key
(`rails_secret_missing`, `rails_master_key_missing`); Django debug or a committed secret key
(`django_insecure_settings`); build scripts that fetch from localhost (`build_fetches_localhost`); a
hosted driver linked to a local database (`hosted_driver_local_database`); a URL where JDBC or ADO.NET
is read (`database_url_format`, whose action names the shaped reference, `${{database.5.jdbc}}`); Rails'
extra databases on quick-setup MySQL
(`rails_multidb_create_denied`); a linked PostgreSQL without the schema's extension
(`database_extension_missing`, blocked; the linked server is asked only when the schema needs an
extension); MongoDB 5+ on a CPU without AVX or ARMv8.2 atomics
(`database_cpu_unsupported`); a Laravel or Symfony start that migrates a database nobody linked
(`database_required_for_start`, blocked); the callback, authorized-domain and webhook addresses to
register with Auth.js providers, Better Auth, OmniAuth, django-allauth, Passport (named by the strategy
constructed around its `callbackURL`), Auth0, Clerk, Firebase, Supabase and Stripe
(`external_callback_registration`); a Clerk production key issued for another host
(`clerk_key_domain_mismatch`: a warning on the same registrable domain, blocked on another); and a
Phoenix release whose migrate overlay nothing runs
(`migrations_not_run`). No finding repeats a secret value.

When a candidate still fails its checks, its own output names the environment cause beside a missing
table (`runtime_variable_cause.go`): a secret it stops without (Rails' `secret_key_base`, Django's
`SECRET_KEY`, Auth.js's `MissingSecret`), Rails credentials it cannot decrypt, Auth.js's
`UntrustedHost`, Phoenix refusing a socket origin or a `runtime.exs` variable, a missing `.env`, and a
connection refused on loopback, by port — never the line it was read from.

## Procfile

A `web:` process in a Heroku-style `Procfile` is the repository declaring how it is served, and outranks
a start script and a framework default alike; it is only ever a server command, so a site framework's
build is still served by nginx. A `release:` process is the candidate's release command
([Release commands](#release-commands)). A command that carries credential material is ignored. Its port and bind flags are read like any
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
  `appsettings(.Production).json` Kestrel endpoints and `Urls`, `gunicorn.conf.py`, the built Dockerfile
  stage's `ENV PORT=`/`ARG PORT=` when it has no single `EXPOSE`, and Phoenix's `config/runtime.exs` PORT
  default.

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
| `public_url_variable_missing` | warning | `NEXTAUTH_URL` (next-auth 4) is missing or empty; the action names the value the primary domain gives. Only for a detection whose environment classification did not set the variable up, which otherwise answers with `public_url_unbound_*` |
| `public_url_variable_stale` | warning | `NEXTAUTH_URL` names another host than the primary domain (only the host is echoed); likewise left to `public_url_mismatch_*` when the classification set it up |
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
  build method, including a Dockerfile beside the package, rather than image settings; a name the
  environment classification also set up is declared once, in the classification's shape. A plan with no
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
worker unless the manifest names an HTTP library; a bot or queue library answers the question, making
it a worker without one, and a start script beside such a library no longer makes it a web service on
3000 (see "Readiness, workers and start commands").

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
fails the start instead of dropping it. That is why a push is never a pass: preflight raises
`schema_push_unversioned` (a warning in place of `schema_step`, linked database or not) whenever the start
command, a release task or the package's own start script pushes, with the command that commits
migrations instead (`prisma migrate dev`, `drizzle-kit generate`). When a push does refuse, the failed
readiness gate names it — `schema_push_refused`, from Prisma's `--accept-data-loss` message or Drizzle's
rename and data-loss prompts — rather than a bare timeout. The tool must be installed by the lockfile; the
runtime stage copies the build's `node_modules`, so a devDependency is available to the start command.

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
  debugger and metrics ports (9229, 5005, 9464, 9090). Without one, the built stage's `ENV PORT=N`/`ARG
  PORT=N`, then what the source at that context says about its listener, then a Phoenix
  `config/runtime.exs` PORT default (else Phoenix's own 4000) supply it; otherwise it still asks. A base
  image's unrecorded exposed port is not guessed.
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
| Django (`manage.py`) | `python manage.py migrate --noinput && gunicorn <project>.wsgi:application --bind 0.0.0.0:${PORT:-8000}`, with `collectstatic` before it when WhiteNoise is installed; `uvicorn <project>.asgi:application` when only an ASGI module exists and uvicorn is declared | 8000 |
| FastAPI | `uvicorn <module>:<object> --host 0.0.0.0 --port ${PORT:-8000}` for the `= FastAPI(` object found | 8000 |
| Flask | `gunicorn --bind 0.0.0.0:${PORT:-8000} <module>:<object>`, or `'<package>:create_app()'` for a factory | 8000 |
| Streamlit | `streamlit run <script> --server.port ${PORT:-8501} --server.address 0.0.0.0 --server.headless true` | 8501 |
| Gradio | `python <script>` with `GRADIO_SERVER_NAME=0.0.0.0` in the image | 7860 |

The detected commands read `${PORT:-N}` rather than a fixed port, so changing the application port in
Build settings moves the server with it. A framework whose application object is not in an entry file
keeps the port and asks for the module. A Django project answers only the hosts its settings allow; the
variable `ALLOWED_HOSTS` is read from is bound to the planned domain in the separator the settings split
it on (see [environment discovery](#environment-discovery-and-database-suggestions)), and a literal list
is checked against the planned domain by `readiness_host_allowlist`. A plain `main.py`/`app.py` is a
low-confidence worker that asks whether it serves. The recipe refuses a plan with no start command,
naming the frameworks detection proposes one for.

Python migration tools are recognised the way the Node ones are, and share their preflight findings:
Django (`python manage.py migrate --noinput`, already the default start's first step), Alembic
(`alembic.ini` beside the manifest or one directory down, with the `alembic` dependency: `alembic upgrade
head`, `-c <ini>` when it is not at the root), Flask-Migrate (`migrations/alembic.ini` with
`flask-migrate`: `flask --app <module> db upgrade`) and Aerich (`[tool.aerich]`: `aerich upgrade`). The
command is chained before a detected start; a `Procfile` web process is the repository's own and is not
rewritten, so a linked database without the step raises `schema_step_missing` instead. A start command
that runs a committed `prestart.sh` (or `scripts/prestart.sh`) which applies the migrations counts as the
step. Alembic is chained only when the `env.py` beside its script location takes the database from the
running environment — it overrides `sqlalchemy.url`, reads a variable or the application's settings, or
imports the application's engine. An `env.py` that connects to the URL `alembic.ini` commits would reach
a developer's database, fail, and keep the server from ever starting; it is recorded without a command,
and a linked database raises `schema_step_missing` with the change `env.py` needs.

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

The runtime stage runs the binary at `/app` as the unprivileged `app` user from its own home,
`/home/app`, so a relative path the service opens resolves somewhere it can write. The root-level files a
service reads at runtime are copied there, owned by that user: `templates`, `views`, `static`, `public`,
`assets`, `migrations`, `locales`, `i18n`, `config` and `config*.{yaml,yml,toml,json}` when they exist,
plus the directory named by any literal path the sources hand to `LoadHTMLGlob`, `ParseGlob`,
`http.Dir`, `Static`/`StaticFile`, `os.DirFS` or a `file://` migration source. The sources are read as
text under a fixed budget, and the walk stops after 20,000 entries. Symlinks are never copied, nor is a
name the repository's `.dockerignore` keeps out of the build context, because copying a file the context
lacks would fail the build. `/home/app/data` is created and owned by `app`, so a volume mounted there
starts writable. Rust uses the same stage, reading `ServeDir`, `ServeFile`, `Tera::new`, `NamedFile` and
actix `Files` literals.

The working directory used to be `/`. A relative path in a saved custom start command now resolves
against `/home/app`, so the stage links `/home/app/app` to the binary: a command such as `./app serve`
still starts it. The link is left out when the repository has a root-level `app` directory the stage
copies instead.

A binary that loads `.env` and treats a missing file as fatal — `godotenv.Load()` followed by
`log.Fatal`, `panic` or `os.Exit` — gets an empty `.env` in the runtime stage's working directory,
`/home/app`, created before the stage drops to its user. It carries no value, and godotenv never overrides a variable
already in the environment, so the dashboard's values still win.

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

A binary whose `src/main.rs` or `src/bin` entry calls `dotenvy::dotenv()` with `.expect`, `.unwrap()` or
`?` gets the same empty `.env` as a Go one.

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
whether it serves. `/app`, `/app/data` and the Data Protection key ring
`/home/app/.aspnet/DataProtection-Keys` belong to the `app` user, so a relative `app.db` and a volume on
either directory are writable.

EF Core migrations (a `Microsoft.EntityFrameworkCore.Design` or `.Tools` reference and a committed
`*ModelSnapshot.cs`) are recorded as the `ef-core` schema tool. Source that calls `Database.Migrate()`,
`MigrateAsync()` or `EnsureCreated()` applies them itself; otherwise a linked database, or a SQLite
database moved onto a volume, raises `schema_step_missing`, whose action is to call `Database.Migrate()`
at startup — the EF command-line tools and the design-time project are not in the runtime image a
release task runs in.

A SQLite file an `appsettings` connection string names, committed to the repository, is copied into
`/app/data` owned by `app`: Docker fills an empty named volume from the image the first time it is
mounted, so the volume detection plans there starts from the committed database — the ASP.NET Core
Identity template ships `app.db` with its schema and never migrates — rather than from an empty file every
sign-in fails on. Later releases find the volume filled and copy nothing. A file the repository's
`.dockerignore` leaves out of the build context, or one reached through a symlink, is not copied.

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
FrankenPHP does not read `.htaccess`, so one with access or rewrite rules is a `php_htaccess_ignored`
warning, and a plain PHP application served from the repository root (`--root /app`) is a
`php_docroot_is_repository_root` warning: dependencies, lockfiles and logs under it are reachable.
Composer runs from the `composer:2` image with `--no-dev --optimize-autoloader`; a `package.json` whose
build script names Vite, `laravel-vite-plugin` or Encore gets an asset stage on the manifest's own
runtime whose `public/build` is copied in. Laravel's start runs `php artisan migrate --force` first;
Symfony's runs `doctrine:migrations:migrate` when the migrations bundle is present. `storage/`,
`bootstrap/cache` and `database/` are created writable so a first start on an empty volume works. The
recipe links the public disk the way `php artisan storage:link` would (`public/storage` →
`/app/storage/app/public`, unless the repository commits one), without booting the application in
the build. A Laravel import's `APP_KEY` is generated on the server at commit (`base64:` over 32 random
bytes), and the **Generate** button is offered on a self-issued name the operator types by hand — never
on a provider's (`STRIPE_SECRET_KEY`, `AUTH_GITHUB_SECRET`, a client or webhook secret). Laravel's
`.env.example` names its engine in `DB_CONNECTION`, which becomes a database suggestion on `DB_URL`
(Laravel 11 and later) or `DATABASE_URL` (Laravel 10 and earlier).

## Readiness, workers and start commands

A release takes traffic only after its readiness check passes, and the check a detected plan carries is
the one the source declares (`detect_readiness.go`), strongest first:

1. The final stage's Dockerfile `HEALTHCHECK`. A `curl`/`wget` of `localhost` on the served port (or
   `$PORT`) becomes an HTTP check of that path; any other command becomes a `docker_health` check that
   waits for Docker's own verdict, with a budget covering the healthcheck's start period and retries.
2. A health path a previous platform's file names: `fly.toml` HTTP checks, `render.yaml`
   `healthCheckPath`, `railway.json`/`railway.toml` `healthcheckPath`, Kamal's `proxy.healthcheck.path`.
3. A health endpoint the framework declares, probed expecting a 2xx: Rails `get "up" =>
   "rails/health#show"`, Laravel's `health: '/up'`, Spring Boot Actuator (`/actuator/health`, under
   `server.servlet.context-path` or `spring.webflux.base-path` and `management.endpoints.web.base-path`;
   skipped when `management.server.port` moves it), Quarkus SmallRye Health (`/q/health/ready`), Micronaut
   management (`/health`), ASP.NET `MapHealthChecks("…")`, Strapi `/_health`, Medusa `/health`,
   Directus `/server/health`, Django's root URLconf (a health route, django-health-check's include, a
   view at `/`, else the admin login page), and file-routed health endpoints — Next.js
   `app/**/health/route.ts` and `pages/api/health.ts` (under a literal `basePath`), Nuxt/Nitro
   `server/api|routes/health.*`, SvelteKit `src/routes/**/health/+server.ts`, Remix resource routes,
   Astro endpoints.
4. A health route registered in code (`app.get('/healthz')`, `@app.get("/health")`, a NestJS
   `@Controller('health')` under a literal global prefix, `HandleFunc("/healthz")`, axum/actix/warp
   routes, `@GetMapping("/health")`, `MapGet("/health")`). A router can mount it under a prefix
   detection cannot see, so it is probed accepting any answer; a JVM route is put under the context
   path the configuration sets.
5. The convention: a page framework (Next.js, Nuxt, SvelteKit, Astro, Remix, React Router, Angular SSR,
   Solid/TanStack Start, Streamlit, Gradio, Flask routing `/`, Laravel, Fresh, plain PHP) is asked for a
   2xx at `/`; everything else — Express/Fastify/Hono/Koa/Elysia/hapi and unknown Node servers, Nest,
   FastAPI, Go, Rust, JVM, .NET, Deno, Symfony, Slim, a Rails API — accepts any answer from `/`, and so
   does a page framework whose authentication SDK (Clerk, Auth0, Kinde, WorkOS, Logto, Descope) sends an
   anonymous visitor to a sign-in page on the provider's site.

JVM settings are read from the first document of `application.properties`/`application.yml` only (a
later `---` or `#---` document is a profile the default run does not activate), and a `${NAME:default}`
placeholder counts as its default. A placeholder with no default leaves the path unknown until deploy, so
the check falls back to any answer rather than guessing a framework path the service may answer 404.

"Any answer" is the check's `acceptAnyAnswer`: every status below 500 passes except 400 and 421, which
are how host allowlists refuse a request, and a redirect is itself the answer. It proves the server
serves without requiring a page at the path; preflight names it (`readiness_root_unverified`, a pass)
with the remedy of a real health route. Both check editors have an **Any answer counts** switch, so a
detected any-answer check can be made strict once the application has a health route.

The probe dials only the candidate, but when the release has a domain it introduces itself the way the
proxy does: `Host` and `X-Forwarded-Host` are the first concrete domain (a wildcard names no host a
visitor sends, and Django refuses a `Host` with `*` in it) and `X-Forwarded-Proto` its scheme, so Django's
`ALLOWED_HOSTS`, Rails' `force_ssl` and host authorization, and anything else that checks the request
answer as they will for visitors. A redirect to the candidate's own address, or to one of the release's
own domains (a locale prefix, a login page, the https form of the same URL, a name under a wildcard
domain), is followed on the candidate — at most four times; a redirect anywhere else is never requested
and is reported with its origin.

Budgets follow the start: 20 attempts 3 s apart by default; 40 for a JVM service or a start command
that applies migrations; 60 attempts 5 s apart for Wagtail's first migrations; 60 attempts 10 s apart
when a Python application loads a model while it starts (`from_pretrained(`, `pipeline(`,
`SentenceTransformer(`, `whisper.load_model(`, `YOLO(` and the like at module level, under `__main__`,
or in a lifespan/startup hook, with a model library among the dependencies). Preflight then warns
`python_model_download_at_start` until a volume keeps the cache (`/root/.cache/huggingface`, or
`/root/.cache`), since the download is otherwise part of every release's container.

Preflight also reads what the source says about how it answers: `readiness_host_allowlist` when a literal
`ALLOWED_HOSTS` or `config.hosts` refuses a planned domain, or, without one, the loopback address the
probe sends as `Host` (`127.0.0.1`, `[::1]` for an IPv6 bind; `localhost` does not match it);
`readiness_redirects_to_https` when `config.force_ssl` without `config.assume_ssl`, or
`SECURE_SSL_REDIRECT`, meets a plan with no HTTPS domain — or, for Django, runs without
`SECURE_PROXY_SSL_HEADER`, which loops behind any proxy — and `readiness_path_unrouted` when a strict
check asks for `/` of a Python application whose router serves nothing there. `readiness_path_detected`
names the declared source the check came from. Django's settings are read from the module `wsgi.py` or
`asgi.py` (else `manage.py`) names, falling back to the base module a split settings package imports
(`base.py`, `common.py`, `settings.py`…); an empty `ALLOWED_HOSTS` under a literal `DEBUG = True` is
Django's local names (`.localhost`, `127.0.0.1`, `[::1]`).

**Workers.** A package that never listens is a worker: no port, no route, no readiness gate. Node:
`discord.js`, `telegraf`, `grammy`, `node-telegram-bot-api`, `@slack/bolt` in socket mode,
`whatsapp-web.js`, Baileys, `mineflayer`, `tmi.js`, BullMQ/Bull/Bee-Queue, Agenda, pg-boss, Graphile
Worker and the cron schedulers, when no framework or HTTP library is in the manifest and nothing in the
package calls `.listen(`/`createServer(`/`Bun.serve(` (a keep-alive server keeps it web). Python, for a
root no web framework or `web:` process claimed: a `worker:` process, a root script (or package
`__main__`) that imports a bot library — discord.py and its forks, python-telegram-bot, pyTelegramBotAPI,
aiogram, Pyrogram, Telethon, Slack Bolt with `SocketModeHandler`, TwitchIO — and runs it (`python
bot.py`), Celery (`celery -A <module> worker`), RQ (`rq worker --url "$REDIS_URL"`), Dramatiq and arq.
A script that serves HTTP itself (`web.run_app`, `uvicorn.run`, a webhook server) is left as it was.
"Nothing listens" is concluded only from a complete read: when the scanner's budget, a file over
128 KiB or a truncated walk left one of the root's source files unread, a Node bot stays web and asks
whether it serves HTTP, and a Python root keeps its previous plan. A worker is at most medium
confidence, so a web application in another root of the same repository still wins the selection (two
high candidates tie and ask the operator to choose). Planned as web anyway, such a package gets
`web_profile_without_listener`.

**Start commands.** A container lives as long as its main process, so a start command that backgrounds
the server makes it exit. Detection rewrites the certain cases into their foreground form — `pm2 start`
(with pm2 installed) into `<runner> pm2-runtime start …`, `forever start` into `forever …`, `gunicorn
--daemon` without the flag, a lone command's trailing `&` removed — looking through the package script
the start command runs. A script whose body is rewritten no longer runs through the package manager, so
the replacement starts with `<manager> run pre<name>` when the package has that hook (every manager the
recipes install runs it, except a `packageManager`-pinned Yarn 2+); a body that reads `$npm_*`
variables, which only the package manager sets, is left as it is and recorded instead. Anything else (`pm2` not installed, `nohup … > log &`, `uwsgi --daemonize`,
`celery multi`, a detached `screen`/`tmux`) is `start_command_daemonizes`, blocked for a recipe and a
warning for a Dockerfile's own `CMD`; `a & b` runs a second, unsupervised process and is
`start_command_backgrounds` (one process per project: deploy the other as its own worker). When a
single container nonetheless exits with code 0 while readiness waits, the run's diagnosis is
`start_command_exited`.

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
| `better-sqlite3`, `sqlite3`, `@libsql/client`, Payload's SQLite adapter, Drizzle's `sqlite` dialect | a file named by a variable, or a literal in the source | volume and variable in the example's own shape (`file:` for libSQL); a literal only in its own directory |
| Drizzle's `turso` dialect, a `TURSO_*` variable, or any example naming a hosted database (`libsql://`, `https://`) | nothing local | none: the data lives in the hosted database, which a local file must not replace |
| `lowdb` `JSONFile`/`JSONFilePreset` | the JSON database | its own directory, or a warning |
| Strapi | `.tmp/data.db`, `public/uploads` | volumes on both |
| `multer` `dest`/`destination`, `UPLOAD_DIR`-style variables | uploads | the directory, or `/data/uploads` through the variable |
| Django `django.db.backends.sqlite3`, `MEDIA_ROOT` | the database, media | the file's own directory when it has one; `NAME` or `MEDIA_ROOT` read from a variable moves to `/data` |
| SQLAlchemy `sqlite:///…` (Flask-SQLAlchemy 3 resolves relative paths in `instance/`) | the database | its own directory, or a warning |
| Laravel with `DB_CONNECTION=sqlite` (or none, from Laravel 11) | `database/database.sqlite`, sessions, cache, queue | volume at `/app/storage`, `DB_DATABASE=/app/storage/database.sqlite` when `config/database.php` reads it; a planned `DB_CONNECTION` other than `sqlite`, or a `DB_URL`, takes the file out of use |
| Laravel with Filament, Media Library or `FILESYSTEM_DISK=public` | uploads on the local disks | volume at `/app/storage` |
| Statamic | `content/`, `users/` | reported: they are the repository's own files |
| Rails `database.yml` production `sqlite3`, Active Storage `Disk` + `:local`, Kamal `volumes:` | `storage/` | volume at `<final WORKDIR>/storage` |
| Go `modernc.org/sqlite`, `mattn/go-sqlite3`, … and Rust `rusqlite`, `sqlx`/`diesel` with SQLite | a file named by a variable, or a literal | `/home/app/data` through the variable (keeping `sqlite://`, `file:` and the query), a literal only under `data/` |
| PocketBase (the Go recipe with no start command of its own) | its databases and uploads | start `/app serve --http=0.0.0.0:${PORT:-8090} --dir=/home/app/data` on port 8090, volume at `/home/app/data`; with a start command of its own, reported |
| `Microsoft.EntityFrameworkCore.Sqlite` with an `appsettings.json` `Data Source=` | the database | `/app/data` through `ConnectionStrings__<name>` (only `Cache`, `Mode`, `Foreign Keys`, `Pooling` and `Default Timeout` options are kept), seeded from a committed copy |
| A Phoenix release built by its own Dockerfile with `ecto_sqlite3` | the file `runtime.exs` reads from `DATABASE_PATH` | `/data/<app>.db` through it when the image runs as root; reported under `USER nobody` |
| ASP.NET cookie auth, Identity, Razor Pages, MVC views, antiforgery or Blazor without `PersistKeysTo*` | the Data Protection key ring | volume at `/home/app/.aspnet/DataProtection-Keys` |
| Dockerfile `VOLUME` (final stage, inherited from an earlier stage), a local image's declared volumes | the declared path | a volume on it; scratch space (`/tmp`, `/var/tmp`, `/run`, `/var/run`, `/var/cache`, `/dev/shm`) is left anonymous |

A variable holds one value, so detection keeps one entry per variable — the reader that read it more
closely wins (Django's `MEDIA_ROOT` from the settings, not from the generic upload-variable list).

The configure form turns every target into a managed named volume (`<slug>-<hash>-<purpose>`, the shape a
template's volumes have, the hash covering the project's name and its draft, so one repository imported
twice never shares a volume), with a storage dependency whose purpose Review shows. The value that moves
the state is declared by the plan — plain, scoped to the runtime and release tasks, not the build, which
has no volume mounted and may open the committed default instead, unless detection saw the build read
that variable (`phase: build`, such as Prisma 7's `prisma.config`), where an unset one fails the build
and `build_variable_missing_*` blocks — and its environment row shows it (`Keeps it on the volume at …`); the row is sent only when the operator changes it. Releases are
stop-first, because preflight refuses candidate-first activation with a writable mount. A variable the
operator changes or empties is read again by preflight, never echoed: it compares where the state would
be written with the plan's writable mounts. A managed volume another project's plan already manages is
`storage_owned_by_other_project` (blocked, naming the owner): both would share the data and removing
either would offer to delete it. The draft's preflight raises it and commit refuses it again, for a
project created in between; a mount marked Linked shares a volume on purpose. State no mount keeps is a
warning before deploy — `sqlite_ephemeral`, `uploads_ephemeral`, `persistent_path_unmounted`,
`declared_volume_unmounted`, `dotnet_data_protection_ephemeral` — whose action is the volume and variable
detection proposed, a server database through the variable a SQLite fallback is read from, or the change
the source needs. State that is kept passes as `persistent_state_kept`, and `backup_policy_missing` then
asks for a backup. A linked server database in the variable a SQLite default falls back from takes the
file out of use and raises nothing. SQLite moved onto a volume starts empty the way a new server database
does, so the detected schema tool's step is checked for it too (`schema_step_missing`, titled for the
volume); only the start command or the application itself counts there, because a release task, on the
host or in the release image, runs without the plan's volumes. A container that cannot open or write its SQLite file — the directory is missing or not writable
by its user — fails its readiness gate with `sqlite_not_writable` named.

Limits: an image's declared volumes are read only when the image is already on this host (the registry
manifest does not carry its configuration), and a base image's own `VOLUME` behind a Dockerfile is not
seen. Retired containers still keep their anonymous volumes, because one may hold the only copy of data a
plan without a mount wrote.

## Seed data

A project that creates its first administrator or lookup rows in a seed records the command
(`seedCommand`, and `seedResets` when the seed deletes or truncates first): Prisma's `prisma.seed` or
Prisma 7's `migrations.seed` (`<runner> prisma db seed`), a `db:seed`, `seed` or `prisma:seed` script,
Laravel's `DatabaseSeeder` when it does more than the skeleton's test user (`php artisan db:seed
--force`), a `db/seeds.rb` with code (`bin/rails db:seed`), and the fixtures a Django application commits
in its `fixtures/` directories, outside any test directory (`python manage.py loaddata <names>`). When
the plan links a database or keeps SQLite on a new volume, and neither the start command nor a release
task seeds — a release task reaches a linked server, never a volume — preflight raises `seed_available` (a
warning) naming the command to run once from the project's console after the first release. Only a
first release does: a deployment that replaces a live runtime has a database that already holds whatever
was seeded. It is never planned as a release task, which runs before every release, because a seed that
clears tables must not run on every release.

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
