# Automatic recipes and serving defaults

`just-dashboard-recipes-v3` prepares immutable Dockerfiles using digest-pinned catalogue bases. Build
commands execute inside the build container; source inspection never executes repository configuration
on the host. Generated Dockerfiles use root-relative, exclusive writes so a checkout symlink cannot
redirect output outside the build context. Detection ignores this generated directory.

Detection reads manifests as data — `package.json`, `angular.json`, `requirements.txt`, `pyproject.toml`,
`uv.lock`, `poetry.lock`, `Cargo.toml`, `pom.xml`, `build.gradle(.kts)`, `*.csproj`, `deno.json(c)` and
`deno.lock` with Deno's version files, `composer.json` and `composer.lock`, a `Procfile`, and for a
JavaScript package its lockfiles, `.npmrc`, `.yarnrc.yml`, `bunfig.toml`,
`pnpm-workspace.yaml` and the Node and Bun version files (`.nvmrc`, `.node-version`, `.tool-versions`,
`.bun-version`), its framework's configuration (`next.config`, `svelte.config`, `astro.config`,
`nuxt.config`, `vite.config`, `nest-cli.json`, an Nx workspace's `nx.json` and `project.json`, …, read as
text for literals), and the site generators' configuration (`hugo.toml`, Zola's `config.toml`, `book.toml`,
Jekyll's `_config.yml` and `Gemfile`, `mkdocs.yml`, Sphinx's `conf.py`, `pelicanconf.py`, Lume's
`_config.ts`) — and names a candidate per root with the framework, the build and start commands, the port,
static output, the interpreter or toolchain release, and the environment variables and databases the
source reads. Every default is a plan field the configure form and the Build settings can change. A
detected framework that the recipe cannot serve automatically (an Astro provider adapter, a Meteor
application, a workspace without a root package, an unsupported interpreter release) is a low-confidence
candidate with a decision or a `recipe_unsupported` preflight finding, never a silent guess. Which of a repository's roots is the
application, and what it is when it is not a service, is [repository shape](#repository-shape-and-candidate-selection).

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
  `SESSION_SECRET`/`COOKIE_SECRET`/`JWT_SECRET` beside the session or JWT library that signs with them,
  and a name another platform's file generates (render.yaml `generateValue`, app.json
  `generator: "secret"`) as 64 hex characters.
  Every rule is an exact name gated by the dependency that issues it to itself, so a provider's
  credential — `STRIPE_SECRET_KEY`, `CLERK_SECRET_KEY`, `SUPABASE_JWT_SECRET`, `AUTH_GITHUB_SECRET` —
  is never generated. A secret the framework reads internally (`SECRET_KEY_BASE` for Rails, Auth.js's
  secret) is added even when no line of code names it.
- `domain` — a self-URL bound to the planned domain through `domainTemplate`: `AUTH_URL`,
  `NEXTAUTH_URL`, `BETTER_AUTH_URL`, SvelteKit adapter-node's `ORIGIN`, Medusa's `ADMIN_CORS`, `AUTH_CORS`
  and (when documented) `STORE_CORS`, Laravel's `APP_URL`, Phoenix's
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

TypeScript's `process.env.X!` and an `if (!process.env.X) throw` guard are `requiredRead`, and a
Prisma schema's datasource `env("X")` is `required` (the client cannot connect, nor a start-time
migration run, without it); a t3/zod key is optional when `.optional()`, `.default()`, `.nullish()`,
`.nullable()` or `.catch()` appears anywhere in its chain, including the lines that continue it.
Preflight lists the `required` names the plan does not declare, and the `requiredRead` ones it does not
set, in one `variable_likely_required` warning — never a decision: a heuristic must not make a project
uncreatable. A finding named after a variable (`build_variable_missing_<name>`, `secret_unset_<name>`,
`compose_variable_<name>`, …) keeps its code within the 128 characters a saved preflight allows: a
longer one is cut and ends in a digest of the whole name, which the finding's `fieldId` carries.

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
a start script, another platform's file and a framework default alike; it is only ever a server command,
so a site framework's build is still served by nginx. A `release:` line is the candidate's release
command ([Release commands](#release-commands)), and every other process line is recorded as one of the
candidate's [processes](#background-processes): a worker, clock or beat line is a process this project
does not run. A command that carries credential material is ignored. Its port and bind flags are read
like any start command's (next section): `--port 5000` makes 5000 the candidate's port,
`-b 0.0.0.0:$PORT` follows PORT, and a gunicorn line with no `--bind` takes its bind from
`gunicorn.conf.py` (or the `-c` file).

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
- **PHP** images load a prepend file that, while `PHP_FORWARDED_TRUST=private`, takes `HTTPS` from
  `X-Forwarded-Proto` and the client from the last `X-Forwarded-For` hop, for requests whose peer is a
  loopback or private address only ([PHP](#php)), so Laravel, Symfony and WordPress see https without
  trusting a proxy themselves.
- A server that reads `HOST` with a loopback fallback (`process.env.HOST || 'localhost'`,
  `env::var("HOST").unwrap_or("127.0.0.1")`) in a build whose image does not set HOST — a Dockerfile, a
  Go or Rust recipe — gets a `HOST=0.0.0.0` plan variable instead. HOST is never injected globally: some
  applications use it as their public hostname.

Trusting forwarded headers is safe only while the proxy fronts the release alone: it replaces whatever a
client sent. The release records which trust settings its recipe image sets. When the release has no
route (nothing adds the headers), or its port is reachable directly (a `0.0.0.0`/`::` bind, host
networking, or the application's port published again on every interface), the runtime writes the
withdrawn value of each recorded setting (`FORWARDED_ALLOW_IPS=127.0.0.1`,
`ASPNETCORE_FORWARDEDHEADERS_ENABLED=false`, `SERVER_FORWARD_HEADERS_STRATEGY=none`, `PHP_FORWARDED_TRUST=none`, the Quarkus switches
`false`, SvelteKit's `PROTOCOL_HEADER` and `ADDRESS_HEADER` empty and `HOST_HEADER=host`, adapter-node's
own default — before 5.5 an empty one made every origin `https://undefined`) unless the plan sets the
variable itself, and preflight says so. A repository's own Dockerfile, a pulled image and an adopted
container record nothing, so what their authors bake in is never overridden.

When a candidate fails its readiness gate anyway, the diagnosis also reads its listening sockets: the
runtime owner runs `cat /proc/net/tcp /proc/net/tcp6` inside the candidate's own container (closed
argv, bounded output, only the parsed addresses kept; an image without `cat` reports nothing). A
candidate listening only on loopback gets the cause `runtime_loopback_bind` with the listener as its
subject, named in the failure message even when the application printed nothing.

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
schema step. `release_process_not_run` is not raised for a process the release command or a planned task
already runs; `fly.toml`'s `/app/bin/migrate` is the overlay's `bin/migrate`, run from the image's
working directory.

## Repository shape and candidate selection

Detection reads a checkout in two passes (`detect_walk.go`). The first lists directories breadth-first
to the depth bound and reads only the files whose names say what a directory is — the manifests above,
Dockerfiles and Compose files, and the repository-shape files below — so ten thousand images under
`assets/` or megabytes of generated Go under `api/` can no longer spend the bound before the root's
`package.json` or `go.mod` is read. The second walks everything else, reusing the first pass's
judgement of each directory, so a pruned directory is recorded once and never entered. The file bound
(10,000) counts files detection opens, not files the repository holds. Go source is read head-first (64 KiB, enough for
the package clause, the imports and the cgo check) under a budget of its own (3,000 files, 24 MiB), and
files marked `// Code generated … DO NOT EDIT.` and `*.pb.go` are skipped; a module whose scan stopped
early says so in its evidence. A manifest the depth bound kept detection from reading sets `truncated`,
and every candidate found before a bound carries that as evidence. A verdict drawn from a file the walk
did not find — no `src/main.rs`, no main package, no script at a Python project's top, no `web/` in a
Flutter app — is drawn only when the walk saw everything under that root: after a bound stopped it, or
when the depth bound pruned a directory under the root, the candidate keeps its ordinary plan instead
of being called a library. Editor, CI and tool directories
(`.devcontainer`, `.github`, `.vscode`, `.idea`, caches and build outputs such as `.svelte-kit`,
`.nuxt`, `coverage`) are never walked; a development container's Dockerfile is recorded as set aside,
not offered. A directory holding `pyvenv.cfg` or `conda-meta/` is a committed virtualenv whatever it is
called: it is skipped, and preflight's `committed_virtualenv` warns that the build context carries it.
A manifest that starts with a UTF-8 byte-order mark (PowerShell 5.1 writes one), or is UTF-16 with a
mark, is read as npm, Composer and Deno read it; the recipe accepts the same file.

Candidates are ranked, and the one that outranks every other is selected (`detect_ranking.go`, with each
candidate's standing read here). Two candidates for one directory are two ways to build one application
and compare as [Repository Dockerfiles and Compose files](#repository-dockerfiles-and-compose-files)
says. Between directories the order is: whether it may be chosen on its own at all (a service, not a
development Dockerfile); whether something ranks it below the application; its confidence; between two
nested roots, `apps/`, `services/` or `web/` above `packages/`, `libs/` or `tools/`; and, on a tie, the
shallower root — unless that is plain static files, which say least about what a repository is for.
Which of two directories builds as detected never decides between them. A tie is still the operator's
choice. A candidate ranked down is chosen on its
own only when it is the only deployable thing and nothing in the repository is a library it could be
the example of; a candidate that is not a service is never chosen on its own. At most the 64
best-ranked candidates are listed, and the rest are counted under "Not offered", so a repository of
fixtures stays a result the chooser can show. Ranked down
(`demotion`, said beside it in the candidate chooser and by `selected_candidate_demoted` when it is
picked anyway):

- roots under `examples/`, `demo(s)/`, `sample(s)/`, `fixtures/`, `test(s)/`, `e2e/`, `playground/`,
  `sandbox/`, `benchmark(s)/`, `templates/` or a Storybook;
- a documentation site (Docusaurus, VitePress, Starlight, Nextra, MkDocs, Hugo, Jekyll, or plain HTML
  under `docs/`) beside an application;
- a workspace root whose applications are its member packages;
- an `index.html` root nested inside a code root outside the folders a framework serves
  (`static_candidate_nested` when selected);
- a `package.json` that only runs tooling — no framework, server library, start script beyond a
  static preview server, runtime library (a bot SDK, a database driver, a queue) or main file that
  exists — when anything else is deployable: a root of prettier and husky used to win on depth over
  the GitHub Pages site in `public/` or `site/`;
- the frontend of a split repository's API, and a desktop shell's frontend (below).

An `index.html` is not a site when it sits under `templates/`, `layouts/`, `_includes/`, `partials/`,
`views/`, `themes/`, a coverage or test report, or when its first 4 KiB start with front matter or hold
`{%`, `<?php` or `<%=` — a Flask or Jinja template used to become a site serving raw template source. An
`index.html` nested inside another plain static root is a page of that site ("2 more pages under ."). One
at or under an application's root, in a folder the framework serves (`public/`, `static/`, `assets/`,
`templates/`, `views/`, `wwwroot/`, `priv/`), is that application's static files and is set aside with
evidence on the application — an Express API with `public/index.html` used to deploy the folder on nginx
and not the API. A tooling-only `package.json` is no application and owns no static files. A plain HTML
site whose `package.json` only runs tooling (the Tailwind CLI, PostCSS, a formatter, a `live-server` or
`http-server` preview) is a static site: with a build script it is "Static site with a build step", the
Node recipe with output directory `.`, whose image serves the package root after the build without
`node_modules`, the package manifests, the lockfiles or dotfiles, and whose nested `index.html` files are
its pages; without one it is a plain static site. `"main": "index.js"`, which `npm init` writes whether
or not the file exists, counts only when the file is there. A bot or script with a landing page — a
main file that exists, or a runtime library such as `discord.js` — stays the program it is, so its code
and configuration are never served as files.

A repository split into a frontend and an API (`detect_split_repository.go`) is one deployment when the
server's own scripts build the frontend (`npm --prefix client run build`, `cd client`, `--workspace`,
`--filter`): the frontend's candidate folds into the server's, which becomes high confidence and, when
the build lives in `heroku-postbuild` or `render-build`, builds with that script. When the two build
separately but talk to each other — the frontend's Vite `server.proxy` points at the API's port, it
reads an `*_API_URL`, or the API reads `CORS_ORIGIN`, `FRONTEND_URL` or `CLIENT_URL` — they are
companions: the API is selected, the frontend is ranked down, and preflight's
`companion_service_not_deployed` names the root the second project deploys from.

Not a service (`notDeployable`, `detect_not_deployable.go`): a package that publishes a library
(`exports`, `types`, `files`, `peerDependencies`) and starts nothing, a `bin`-only command-line tool, a
VS Code extension (`engines.vscode`), a browser extension (a `manifest_version` manifest, WXT, Plasmo,
CRXJS), Electron and React Native or Expo apps without `react-native-web`, a GitHub Action
(`action.yml`), a Python project with no framework and no entry point (`[project.scripts]` makes it a
tool; a package's `__main__.py`, a Procfile or Dockerfile, or a dependency only a program has — a
Discord or Telegram SDK, Celery, RQ, APScheduler, a WSGI or ASGI server — makes it a program), a
notebook-only root, a Rust library crate, a Windows-only .NET program (.NET Framework,
`-windows`, WinForms, WPF), a Go module with no main package outside its examples, and a Wails or Tauri
shell. Android modules (`com.android.*` in a Gradle script), a Flutter app's native folders and the
functions directories Supabase, Netlify and Firebase host are set aside rather than offered. Preflight's
`source_not_a_service` is blocked when every candidate is one, or when the selected one is, while the
plan is still detection's; a start command of the operator's own, or another build method (a Dockerfile,
a static site), makes it a warning either way, because detection can be wrong about what a source is. A
Tauri or Wails frontend is ranked down and warned (`desktop_frontend_only`): its calls into the shell
have nothing to answer them in a browser. Expo with `react-native-web` builds its web target
(`expo export --platform web` into `dist`, single-page fallback on); `expo.web.output: "server"` is a
`recipe_unsupported` finding.

Serverless and edge code (`detect_serverless.go`) is named rather than dropped: a Wrangler `main` is a
Worker entry, which a container never runs. `edge_runtime_code_not_deployed` is blocked when the
application has no server of its own — a static site, a Worker-only package, or a start command that
runs the Worker entry itself — and a warning beside an application the recipe starts in Node. A `main`
a framework's Cloudflare adapter writes (`.open-next/`, `dist/_worker.js`, `.svelte-kit/cloudflare/`,
`.output/server/`) is evidence only: Next.js on OpenNext still builds and serves with `next start`. A
Vite app with `@cloudflare/vite-plugin` serves `dist/client`, where the plugin writes the client; and
Vercel `api/`,
`netlify/functions` or Cloudflare Pages `functions/` beside a static site are listed by route in
`serverless_functions_dropped`. The static server's single-page fallback never answers `/api` or a path
under it: those requests get a 404 rather than the application's page with a 200, so a client calling a
function that did not deploy fails where it calls.

When the Git remote is the upstream repository of an application the template catalogue packages —
n8n, Gitea, Uptime Kuma — detection offers the reviewed template, and a workflow that publishes the
project's own image to `ghcr.io/<owner>/…` or `docker.io/<owner>/…` offers that image
(`alternatives`; `source_has_packaged_release` or `source_publishes_image`, a warning). The project step
links to `/deploy/new?source=template&template=<id>` or `?source=image&image=<ref>`. Everything detection
recognised and deliberately did not offer is listed as `setAside` and shown under "Not offered", so an
empty result can say what the repository is instead of "No deployable plan was detected".

## Other platforms' deployment files

A repository that ran somewhere else declares how it runs there, and detection reads those files as
bounded data (64 KiB each, `detect_platform_manifests.go`; TOML through a line reader of the shapes
these files use, YAML and JSON parsed): `fly.toml`, `render.yaml`, `railway.json`/`railway.toml`, Heroku
`app.json` and `heroku.yml`, `nixpacks.toml`, `netlify.toml` and `_redirects`, `vercel.json`,
DigitalOcean `.do/app.yaml`, Kamal `config/deploy.yml` (ERB tags stripped), `Aptfile`,
`.readthedocs.yaml`, `firebase.json` hosting and a Hugging Face Space's README front matter. What they
declare ranks below the operator's input and the Procfile and above a framework's defaults, and each
candidate records the file, the facts and which it took (`platformManifests`):

| Declared | Taken as |
| --- | --- |
| start command (`startCommand`, `[start] cmd`, `run_command`, fly `app` process, Space `app_file`) | the start command of a recipe candidate that did not take the Procfile's, unless it runs a package manager the lockfile does not build with, with the detected schema step kept in front of it unless it applies the schema itself; it answers the start-command decisions |
| build command | the build command when detection had none, with its dependency installs (`npm ci`, `pip install`, `bundle install`, …) removed — the recipe installs from the lockfile |
| port (`internal_port`, `http_port`, Kamal `proxy.app_port`, Space `app_port`) | the port when the start command came from the same file, or when nothing named one |
| publish directory (`publish`, `outputDirectory`, `staticPublishPath`, `output_dir`, hosting `public`) and a rewrite of every path to `/index.html` | the output directory and the single-page fallback of a static candidate |
| health check path | evidence and `healthPath`; readiness detection reads the same files for the check ([Readiness](#readiness-workers-and-start-commands)) |
| `generateValue`, `generator: "secret"` | a variable set up to be generated on the server when the project is created (`setup: generate`), unless the environment classification knows the framework's own shape for it |
| secret env names, `sync: false`, `required` | a variable row that must be set |
| accessories, add-ons, `fromDatabase`, `databases:` | a database suggestion on the declared variable |
| worker, cron, Kamal roles, `[processes]` | [processes](#background-processes) |
| release command (`release_command`, `preDeployCommand`, `PRE_DEPLOY` job) | a `release` process; `fly.toml`'s and `render.yaml`'s are also the candidate's release command ([Release commands](#release-commands)), and one no release task runs is `release_process_not_run` |
| volumes and `[[mounts]]` | evidence and `volumes`; no mount is created from them yet |
| `Aptfile`, `aptPkgs` | `platform_system_packages_ignored` (warning) for a recipe build |
| redirects, rewrites and headers (`_redirects`, `_headers`, `netlify.toml`, `vercel.json`) | rules the static server applies ([Static sites](#static-sites-and-site-generators)); `static_redirects_unsupported` (warning) counts those it leaves out |
| `HUGO_VERSION`, `ZOLA_VERSION`, `NODE_VERSION` in a build environment | the Hugo or Zola release the site recipe builds with, and the Node release after the version files |

Values are never imported from these files: a secret is a name to fill in or generate, and plain values
become examples only when they are not shaped like a host, an address or a URL — `PHX_HOST` and a Kamal
accessory's IP belong to the old host. Kamal's `env.clear` brings examples only for framework toggles
(`SOLID_QUEUE_IN_PUMA`, `WEB_CONCURRENCY`, `JOB_CONCURRENCY`, `RAILS_LOG_LEVEL`, …). Every command passes
the same credential screen as the rest of detection. The TOML reader gathers a multi-line string or
array line by line and stops at 256 lines, so an unterminated value ends the document instead of being
rescanned to its end, and these files are read only within detection's time bound. A fact the result's
validation would refuse — a process named with a space, a path with a newline in it — is dropped before
the result is saved, so one odd file costs that fact and never the import.

## Background processes

A project runs one process. Detection records every other process the source declares or its framework
implies on the candidate (`processes`, `detect_processes.go`), each with the command a second project
from the same repository would start: the Procfile's lines; Celery's worker and, with a beat schedule or
`django-celery-beat`, its beat (`celery -A <module> worker`, the module found where `Celery(` is
assigned); an RQ worker on `REDIS_URL`; Dramatiq and arq workers by name; a Laravel queue worker when a
job, mailable, notification or listener implements `ShouldQueue` and `QUEUE_CONNECTION` is not `sync`
(`php artisan horizon` with Horizon, whose image then carries `pcntl` and `redis`), its scheduler when
`routes/console.php` or the console kernel schedules anything, and Reverb's WebSocket server; a Symfony
Messenger consumer; Sidekiq, Solid Queue, GoodJob, Resque and Delayed Job for Ruby; a BullMQ worker file
the start command does not run; and the workers, cron jobs and roles of the platform files above.
A process the Procfile declares replaces the framework's conventional one of the same kind (a Gemfile's
Sidekiq beside `worker: bundle exec sidekiq` is one worker), two processes with the same command are
one, and a Procfile command is held to the same 1,024-character bound and credential screen as a
command from any other platform's file. Preflight raises `secondary_process_not_deployed_<name>`
(warning) for each, unless the plan's start command is that process's own, which is the second
project, or the process is Solid Queue's `bin/jobs` while the plan sets `SOLID_QUEUE_IN_PUMA` for a
`puma.rb` that reads it — the web server then runs the jobs itself, and the plan declares it by default;
a release process is not a project of its own but a [release task](#release-commands), and the
project step lists only the others. Octane is recorded as an alternative start command, not a failure.

## Ecosystems without a recipe

A root the builder has no recipe for is still named (`detect_ecosystems.go`), with a low-confidence
candidate whose framework is the language or framework and whose `recipeIssue` says what to commit —
which preflight's `recipe_unsupported` shows instead of "No deployable plan was detected". Rails
(`rails new` has generated a production Dockerfile since 7.1; older applications use `dockerfile-rails`),
Hanami, Sinatra and Rack, Jekyll and Middleman; Phoenix (`mix phx.gen.release --docker`) and Elixir;
Crystal, Haskell, Zig, Swift, Scala, Clojure, Gleam, F#, OCaml, Nim, Perl, Erlang, Dart, R Shiny and
Plumber, C/C++ (CMake, Meson), Elm and a Flutter web app. Hugo, Zola, mdBook, Jekyll, MkDocs, Sphinx,
Pelican and Lume have recipes ([Static sites](#static-sites-and-site-generators)). A web framework among the
manifest's dependencies makes it a web service on its conventional port. A Rails, Hanami or Phoenix
application owns its `package.json` (and `assets/package.json`): that asset pipeline is set aside rather
than offered as a Node service — a CocoaPods or fastlane Gemfile beside a React Native app owns nothing.
A site generator (Middleman, Elm, and the generators with a recipe) owns a tooling-only `package.json` at
its root the same way: a Hugo site with a Tailwind build used to be only a Node worker asking for a start
command. A Flutter app's `web/` folder is a template `flutter build web` fills in, never a site: served
raw it was a blank page that passed readiness, so it is set aside as the app's own files and the app is
named with its build. There is no Flutter recipe: Flutter publishes no official image, and its SDK and
packages are fetched from hosts a build cannot be proven against here.
With a Dockerfile at the same root the Dockerfile is the candidate, and it takes the framework, the
processes and the database drivers. Every other language is named only when nothing else at its root
is a candidate, and C/C++ and MkDocs never inside another candidate's root, where they are that
application's vendored code or manual. Recipes for some of these are planned; each entry is removed when
its recipe lands.

## Submodules and Git LFS

Detection parses `.gitmodules` and the LFS patterns of `.gitattributes` (`detect_git_extras.go`): each
submodule's path and whether its URL is relative or on the repository's own host (`submoduleList`), and
the files under the root that LFS tracks (`lfsFiles`, `lfsPaths`, which lists at most 256). Only what
the build root holds matters: a submodule inside it, or the one it lies inside. A nested root's LFS count
comes from the listed paths; when the repository tracks more files than are listed, a count of zero is
unknown, and preflight warns that LFS files may deploy as pointer files instead of passing. The configure screen turns submodules on when every one under the root is on the source's own
host, and LFS when files under the root are tracked, and both switches sit in the Source section beside
the branch. Preflight's `git_submodules` is a decision only for a submodule on another host, a warning
when a same-host one is left off, and a pass when none is under the root; `git_lfs` warns that
LFS-tracked files deploy as pointer files when LFS is off, and `git_lfs_unavailable` is blocked when LFS
is on and this host has no `git-lfs`, which is where the release would otherwise stop at acquiring the
source. Evidence recorded before this (no `submodulesChecked`/`lfsChecked`) keeps the old decisions. A
site generator whose configured theme is a submodule (`themes/<theme>` for Hugo and Zola) cannot build
without it: `site_theme_in_submodule` is blocked while submodules are off, whichever host it is on.

## Case-mismatched imports

The source detection already reads for its environment is also read for relative import specifiers
(`import … from './x'`, `require('./x')`, dynamic `import()`), resolved against the file list with the
bundlers' extension and `index` rules (a `.js` specifier may name a `.ts` file). An import with no exact
match and exactly one file that differs only in letter case builds on macOS and Windows and fails on the
Linux build: `import_case_mismatch` names the file, line and real name, blocked for a Node recipe and a
warning for a Dockerfile. A PHP class whose file differs from Composer's PSR-4 expectation only in case
is a warning: the optimized autoloader skips it. An alias, a generated file or a typo that matches
nothing says nothing about case and is not reported.

## JavaScript and static output

How dependencies install — which package manager, from which lockfile, frozen or not, with which
release — is [JavaScript installs](#javascript-installs) below. Plain HTML uses the selected source directory as
its public root; its output directory is empty, not the source directory repeated a second time.
Packaged static output always serves on nginx port 80, regardless of a repository's development/start
script port, with a configuration of the platform's own (`build_static_serving.go`) — nginx's stock one
answered `/about` with 404 for every generator that writes `about.html`, redirected a directory to
`http://` behind the TLS proxy and ignored the site's `404.html`:

- `try_files $uri $uri.html $uri/`: `/about` is `about.html` (SvelteKit's adapter-static, VitePress's
  clean URLs, Next's export, Astro's `build.format: 'file'`) before it is the directory `about/`;
- `absolute_redirect off`, so the slash nginx adds to a directory is a relative `Location` that keeps
  the scheme and host the browser used;
- `error_page 404 /404.html`, answered from the site's own `404.html` when it has one and nginx's page
  when it does not — never through the single-page fallback;
- gzip for text, CSS, JavaScript, JSON, XML and SVG;
- every dot-path except `.well-known/` refused (`location ~ /\.(?!well-known/)`), so a stray `.git/`,
  `.env` or `.htaccess` in the published directory is never served, and a host's `_redirects` and
  `_headers` files answer 404.

Quick setup and the wizard generate required HTTP readiness checks for that serving port.

The catalogue in `deploy/frameworks_node.go` is ordered and the first match wins, so a meta-framework
built on Vite is recognised before Vite itself — every one of them lists `vite`, and reading that alone
made a Remix server a static site — and a CMS or commerce server (Strapi, Medusa, Directus, KeystoneJS,
AdonisJS) before the Next.js or Vite its admin is built with. A framework's `start` script (Nest:
`start:prod`; Angular SSR: the `serve:ssr:<project>` script the CLI writes, when it runs the entry)
wins over its default start command, unless it starts a development server; a site framework ignores
start scripts. A framework whose build script is missing is a low-confidence candidate that asks for one,
except where the framework has a build command of its own (KeystoneJS, RedwoodJS).

| Framework | Recognised by | Serves as | Default |
| --- | --- | --- | --- |
| Strapi | `@strapi/strapi` | server, 1337 | the `start` script (`strapi start`); the build runs with `NODE_ENV=production` so the admin is built for production |
| Medusa v2 | `@medusajs/medusa` in `dependencies` (v2 by its major or `@medusajs/framework`) | server, 9000 | `cd .medusa/server && medusa db:migrate && medusa start` (what `medusa build` writes, on the root's `node_modules`); entry `.medusa/server/package.json`; Medusa 1 keeps its `start` script with a decision to confirm it and its `medusa migrations run` |
| Directus | `directus` | server, 8055 | `<runner> directus bootstrap && <runner> directus start` |
| KeystoneJS 6 | `@keystone-6/core` | server, 3000 | `<runner> keystone start --with-migrations`; `<runner> keystone build` without a build script |
| AdonisJS 6 (5) | `@adonisjs/core` | server, 3333 | `node build/bin/server.js` (5: `build/server.js`), after `node build/ace.js migration:run --force` (5: `build/ace`) with `@adonisjs/lucid` and `database/migrations`; the template's `start` runs inside `build/` and is not used |
| RedwoodJS | `@redwoodjs/core` | server, 8910 | `<runner> rw serve` (after `rw prisma migrate deploy` with `api/db/migrations`); `<runner> rw build` |
| Next.js | `next` | server, 3000 / site `out` | `<manager> run start` (`<runner> next start` without a script); `output: 'export'` is a static site in `out` (or its `distDir`) under its `basePath`; Payload is Next.js |
| SvelteKit | `@sveltejs/kit` | server, 3000 / site `build` | the adapter `svelte.config.js` imports: `node build` (Bun: `bun ./build/index.js`), or adapter-node's `out`, for adapter-node, and for adapter-auto or a provider adapter, built with a pinned adapter-node; adapter-static's `pages` and `fallback` |
| Astro | `astro` (+ `@astrojs/node`) | site `dist` / server, 4321 | `node ./dist/server/entry.mjs` (every Node server's runtime stage sets `HOST=0.0.0.0`); a provider adapter, middleware mode, or `output: 'server'` without an adapter is a decision |
| Nuxt 3/4 | `nuxt` | server, 3000 / site `.output/public` for `nuxt generate` or the `static` preset | `node .output/server/index.mjs`; a provider preset builds with `NITRO_PRESET=node-server`; Nuxt 2 runs `nuxt start` |
| Remix | `@remix-run/dev` (+ `@remix-run/serve`) | server, 3000 / site `build/client` | `remix-serve ./build/server/index.js` (`build/index.js` with `remix.config.js`); `ssr: false` is a single-page site |
| React Router (framework mode) | `@react-router/dev` (+ `@react-router/serve`) | server, 3000 / site `build/client` | `react-router-serve ./build/server/index.js`; `ssr: false` in `react-router.config` is a single-page site, answering from `__spa-fallback.html` when prerendering wrote `/` to `index.html`; `react-router` alone is a Vite site |
| SolidStart, TanStack Start, Nitro | `@solidjs/start`, `@tanstack/*-start`, `nitropack` | server, 3000 | `node .output/server/index.mjs`; TanStack Start is certain of it with the Nitro Vite plugin or a vinxi `app.config`, and otherwise asks to confirm the entry |
| Qwik City | `@builder.io/qwik-city` | server, 3000 / site `dist` | the adapter `build.server` builds (`node server/entry.express`, `.fastify`, `.node-server`, or its `serve` script); the static adapter's site; no adapter is a decision |
| Analog | `@analogjs/platform` | server, 3000 | `node dist/analog/server/index.mjs`; `ssr: false` is a single-page site in `dist/analog/public` |
| Angular | `@angular/core` (+ `@angular/ssr`) | site from `angular.json` (`dist/<app>/browser` with the application builder) / server, 4000 | `node dist/<app>/server/server.mjs`, or the `serve:ssr:<app>` script; `outputMode: "static"` is a site |
| NestJS | `@nestjs/core` | server, 3000 | `start:prod` script, else `node dist/main` — or `node dist/src/main` when a TypeScript file outside `src/` makes tsc write there, `dist/apps/<app>/main` in monorepo mode |
| Vike | `vike` | server, 3000 / site `dist/client` | the `start`/`prod`/`serve`/`production`/`preview` script that runs its server (following the scripts it runs; Bati names it `preview`), never one that builds first; prerendering is a site; otherwise a decision |
| Waku | `waku` | server, 8080 | `<runner> waku start` |
| Gatsby, Docusaurus, VitePress, Rspress, Eleventy | their packages | site `public`, `build`, `<docs>/.vitepress/dist`, `doc_build`, `_site` | VitePress reads the docs directory from its build script (`docs:build`); Eleventy's output is its build script's `--output`, else `dir.output` in its configuration, and with no build script it builds with `<runner> eleventy` (the binary's name, which `pnpm exec` and `yarn` run; neither resolves a package name) |
| Hexo, VuePress, Slidev | `hexo`, `vuepress`/`vuepress-vite`/`vuepress-webpack`/`@vuepress/cli`, `@slidev/cli` | site `_config.yml`'s `public_dir` (default `public`), `<docs>/.vuepress/dist`, `dist` (single-page) | Hexo builds with `<runner> hexo generate` and Slidev with `<runner> slidev build` when there is no build script; VuePress reads the docs directory from `vuepress build <dir>` (or `vuepress-vite`/`vuepress-webpack build`) |
| Create React App, Vue CLI, Ember, Parcel, Rsbuild, Rspack, Farm, webpack, Vite | their packages (webpack only when the build script runs it) | site `build` / `dist`, or the directory the bundler's configuration names (Vite `outDir` under its `root`, webpack/Rspack `output.path`, Rsbuild `distPath.root`, Farm `output.path`) | single-page fallback on by default |
| Express, Fastify, Hono, Koa, Elysia, hapi, h3, Polka, restify, Apollo Server, GraphQL Yoga, tRPC, Socket.IO, ws | their packages | server, 3000 | the `start` script, else `node <main>` (`bun <main>` with a Bun lockfile), else the dev script without its watcher or the conventional entry file |

A Meteor application (`.meteor/release`) builds with its own toolchain, which the recipe does not
install: it is a low-confidence candidate the recipe refuses (`recipe_unsupported`: use a Dockerfile).

Every command the table proposes uses the resolved manager's runner (`bun`/`npm`/`pnpm`/`yarn run`,
`bunx`/`npx`/`pnpm exec`/`yarn` for a binary), and detection records the commands it would propose for
each of the four managers, so choosing another manager swaps whole commands.

**Framework configuration is read as data** (`frameworks_node_config.go`), from the package's own
directory, through the install's `os.Root`, on a read budget of their own (16 MiB a detection or build,
apart from the lockfiles', so a monorepo's configuration and `next/image` scans never spend what a later
package's lockfile comparison needs): `next.config.*`, `svelte.config.*`,
`astro.config.*`, `react-router.config.*`, `nuxt.config.*`, `vite.config.*`, the webpack, Rsbuild,
Rspack and Farm configurations, a vinxi `app.config.*`, `nest-cli.json`, `tsconfig(.build).json` and
`nx.json`, each at most 64 KiB, as text with its comments blanked so a commented-out
`output: 'export'` says nothing. Only a quoted literal where the key is settles a question:
`output: 'export' | 'standalone'`, `distDir`, `images.unoptimized`, the adapter
`svelte.config` passes to `kit.adapter` (the import its call names, or the only one imported), its
`pages` and `fallback`, Astro's `output`, adapter and `mode`, `ssr: false`, a Nitro `preset`, Vite's
`root` and `outDir` (`path.resolve(__dirname, …)` read from the package), a bundler's output
directory, and Nest's `sourceRoot`, `entryFile`, `monorepo`, `root`, `outDir`, `rootDir` and `include`.
The sub-path a static output is built for (`basePath`, `base`, …) is read only from the exported object,
by the static server's reader (`detect_js_config.go`, "Sub-paths" below).
A key given an expression keeps the catalogue's default with a decision — `output: process.env.X ?
'export' : undefined` asks whether the site is exported, and so do Astro's `output` and Vite's `root`
(other than `__dirname`) and `outDir` given one; an `svelte.config` that imports several adapters and
picks one by an expression asks which. Detection and the recipe read through the same
function, so what detection proposes is what the recipe renders. NestJS writes `dist/src/main.js`
instead of `dist/main.js` when tsc's common root is the package: a TypeScript file outside `src/` and
`test/` — at the root (`prisma.config.ts`, `drizzle.config.ts`) or one directory down (`prisma/seed.ts`)
— with no `rootDir` and an `include` not limited to `src`; a `start:prod` that still runs `node dist/main`
is then not used (`nest_output_layout`, a warning naming the `"include": ["src"]` fix).

**Start scripts that run a development server** (`frameworks_node_scripts.go`) are passed over. A
script is a development server when a command it runs, following the scripts it calls, is `ng serve`,
`vite` (bare, `dev` or `serve`), `astro dev` (or bare `astro`), `next dev`, `nuxt`/`nuxi dev`, `remix
dev`/`vite:dev`, `react-router dev`, `vue-cli-service serve`, `react-scripts start`, `gatsby develop`,
`webpack serve`/`webpack-dev-server`, `rsbuild dev`, `rspack serve`, `farm start`, `docusaurus start`,
`vitepress`/`vuepress`/`rspress dev`, `parcel <entry>`, `strapi develop`, `medusa develop`, `node ace serve`,
`rw dev` and the like, or a watcher: `nodemon`, `tsx watch`, `node --watch`, `ts-node-dev`, `bun --hot`/`--watch`,
`nest start --watch`. A framework then starts from its own entry (evidence: "the start script runs ng
serve, the development server; the start command runs node dist/app/server/server.mjs"). A package
without a framework uses another script that serves a build (`start:prod`, `start:production`, `prod`,
`production`, `serve:prod`, `serve`), else the watcher's own command without the watcher — `nodemon
index.js` is `node index.js`, `tsx watch src/index.ts` is `tsx src/index.ts` (with `tsx` installed),
`node --watch server.js` is `node server.js`, `bun --hot src/index.ts` is `bun src/index.ts` — run after
`<manager> run prestart` when the package has that hook. A `start` that only runs another script
(`"start": "npm run dev"`, with no arguments passed on) is followed to that script's watcher, after each
script's pre hook. A development server nothing can replace is
kept, and preflight warns while the plan still runs one (`start_command_dev_server`, judged against the
configured command through the scripts detection recorded as `nodeBuild.devScripts`).

**A server without a start script.** A server library's starter often ships only `dev` (`bun create
hono`, `bun create elysia`): its start is the dev script without the watcher, else the conventional entry
file (`src/index.ts`, `src/server.ts`, `index.ts`, `server.js`, … — the first that exists), on Bun with
a Bun lockfile, through `tsx` for TypeScript when installed, else Node — then with a decision, since
`node <file>.ts` runs only through Node's own type stripping (22.6 and later, no enums, decorators or
extensionless imports). Bun's `export default app` (a fetch handler, Hono's Bun template) serves only on
Bun, as does Elysia: with another package manager the candidate asks to choose Bun or a Node adapter.

**A site builder beside a server.** Vite, Parcel, Create React App, Vue CLI and the bundlers only build
a site; a package that also runs a server library from `dependencies` and whose start command (or
Procfile) runs a file — `NODE_ENV=production node dist/index.js`, the Replit full-stack template, where
`vite build` writes the client into what Express serves — is that server, started by its script
(evidence: "vite builds the client; the start command serves it with express"). A package that lists
Vite beside a server library with no `index.html` and no `vite.config` at its root (Vite for its tests)
is the server too, with a decision to confirm its start command unless `vitest` says what Vite is for. A
`vite preview` or `serve` start keeps the site.

**A framework's own fallback page** (`build_node_frameworks.go`). adapter-static's `fallback:
'200.html'` and React Router's `__spa-fallback.html` are the single-page fallback when the plan serves
the framework's own output directory: nginx tries the page as a file before `index.html`
(`try_files $uri $uri.html $uri/ /200.html /index.html`), since a framework writes it only in some modes
(React Router only when prerendering wrote `/` to `index.html`). Another output directory may not hold it
and falls back to `index.html`, and a host's `/* /app.html 200` rule is what the site was served with
there, so its page wins. The sub-path, the clean URLs, the 404 page and the host rules are the static
server's own for every static output (see "Sub-paths" and "Other hosts' rules" below): the base path is a
property of the build — its pages link under it wherever the files are copied — so it is applied whichever
output directory the plan serves.

**What the recipe adds around the build** for the plan's own commands:

- SvelteKit on adapter-auto or a provider adapter: after the install, `<manager> add` of
  `@sveltejs/adapter-node` at a reviewed release (5.5.7 for Kit 2.4 and later, 3.0.3 before, 1.3.1 for
  Kit 1; npm installs with `--no-save --legacy-peer-deps`), unless it is installed, and
  `svelte.config.js` moved to `svelte.config.user.js` (a `svelte.config.mjs` in a `"type": "module"`
  package to `svelte.config.user.mjs`) behind a `svelte.config.js`, the file SvelteKit loads, that imports
  it and sets `kit.adapter` to adapter-node — every other option is the repository's. The repository is
  not changed; `sveltekit_adapter_substituted` (a warning) gives the one-line change that makes it build
  the same way everywhere. A `svelte.config.ts`, an `.mjs` in a CommonJS package, or a Kit major without a
  reviewed adapter, stays a decision.
- A Nitro provider preset (Nuxt, SolidStart, TanStack Start): the build command's RUN sets
  `NITRO_PRESET="${NITRO_PRESET:-node-server}"`, which outranks the configuration's preset
  (`nitro_preset_overridden`).
- Next.js standalone started from `.next/standalone/…/server.js`: after the build,
  `.next/static` and `public` are copied beside the server (Next.js's own Docker example does the same),
  the runtime stage sets `HOSTNAME=0.0.0.0`, which the server binds, and the server file is the entry
  checked.
- The entry check: a server framework's entry (`.output/server/index.mjs`, `build/index.js`,
  `dist/src/main.js`, `build/bin/server.js`, …) is checked after the build when the start command is the
  framework's own or runs the entry, directly or through a package script (`npm run serve:ssr:app`), so a
  wrong output path fails the build with the framework's name instead of failing the readiness gate.
  Another start command skips the check.
- A command the build, the install's lifecycle scripts or the start reach that calls `bash` gets it in
  that stage on Alpine (`bash_installed`); one that calls another language's toolchain — `python`,
  `php`, `ruby`, `java`, `go`, `cargo`, `dotnet`, `deno` and their package managers — is refused before
  the build on the field that runs it (`command_runner_missing`); `python3` is in the build stage when a
  native addon brings the compilers, and the PHP recipe's asset stage, built on its vendor stage, has
  `php` and `composer` for a script that runs `php artisan`.

`main` without a start script is a low-confidence worker unless the manifest names an HTTP library; a
bot or queue library answers the question, making it a worker without one, and a start script beside
such a library no longer makes it a web service on 3000 (see "Readiness, workers and start commands").

A site whose client owns its routes — Vite, Create React App, Vue CLI, Ember, Parcel, the bundlers,
Angular, Slidev and SPA modes (`ssr: false`) detections, a SvelteKit adapter-static whose configuration
names a `fallback` page, and a host rule that rewrites every path to `/index.html` — carries
`build.spaFallback`, which makes nginx answer any path with no file behind it with `index.html` (the
framework's fallback page first, as above, or the page a `/* /app.html 200` rule names). Paths that
belonged to code another host ran answer 404 instead: `/api` and the paths under it when the site keeps
functions there (Vercel's `api/`, Cloudflare Pages' `functions/api/`, read from the commit when the build
is prepared), and the paths a rule sends to a Netlify function or another host. A site with no functions
keeps `/api` for its client's own routes (a developer portal's `/api/reference`). Multi-page generators
(Astro, Gatsby, Docusaurus, VitePress, Eleventy, Hexo, Next.js export, SvelteKit static without a
fallback) serve files by their clean URLs with no fallback. The switch is on the configure form and Build
settings whenever there is static output, and is valid only with a static site or a recipe with an output
directory.

**Before Deploy** (`preflight_node_frameworks.go`), what the framework's configuration made the recipe do
is judged against the plan as it stands, from what detection recorded (`nodeBuild.findings`):

| Finding | When |
| --- | --- |
| `sveltekit_adapter_substituted` (warning) | SvelteKit's configuration names adapter-auto or a provider adapter, and the plan serves the server |
| `nitro_preset_overridden` (warning) | a Nitro provider preset is replaced by `node-server` |
| `next_export_images` (warning) | the plan exports a Next.js site, a page imports `next/image` and `next.config` sets neither `images.unoptimized` nor a custom loader; the build would stop with "Image Optimization using the default loader is not compatible with export" |
| `nest_output_layout` (warning) | the plan runs the start detection proposed past a `start:prod` that looks for `dist/main` |
| `start_command_dev_server` (warning) | the configured start command runs a development server or a watcher, directly or through a package script |
| `node_decision_open` (warning) | a question detection left open about the framework (an output set by an expression, Astro's middleware mode, a Vike or TanStack Start entry, an entry only Bun serves, TypeScript on plain Node, an Nx executor it does not know, …) while the setting that answers it — named by the finding's field, the start command, output directory or package manager, else the whole build — still holds detection's guess; the questions one setting answers are one finding |
| `command_runner_missing` (blocked) | a saved build or start command calls another language's toolchain |

A service whose manifest depends on a recognised migration tool starts by applying its schema: the
detected start command becomes `<runner> <schema command> && <start>`, in front of a start script or a
framework default alike. Prisma (`*.prisma` at the root or under `prisma/`, or where its configuration
declares its schema) runs `prisma migrate deploy` when a `migration.sql` is committed and `prisma db push`
otherwise; Drizzle (`drizzle.config.*`) runs `drizzle-kit migrate` with a
`_journal.json` and `drizzle-kit push` otherwise; Knex (`knexfile.*`) runs `knex migrate:latest`;
Sequelize CLI runs `sequelize-cli db:migrate`; MikroORM migrations run `mikro-orm migration:up`. TypeORM
is recognised but needs an operator's command. A framework that owns its migrations names its tool from
its catalogue entry instead of a dependency: AdonisJS's Lucid (`node build/ace.js migration:run --force`,
run as written), Medusa (`(cd .medusa/server && medusa db:migrate)`, already in its start), KeystoneJS
(`keystone prisma migrate deploy`, applied by its start's `--with-migrations`) and RedwoodJS
(`rw prisma migrate deploy`). Their steps are chained, judged (`schema_step`, `schema_step_missing`) and
put in front of a Procfile's or platform file's start like any package's own tool; a start script that
migrates does not count for them, since the framework's start is not that script. A start script that already runs the tool is left as it
is, and static output never gains a start command. Prisma's push refuses destructive changes without an
explicit flag and Drizzle's stops to ask a question nobody can answer, so a schema that would lose data
fails the start instead of dropping it. That is why a push is never a pass: preflight raises
`schema_push_unversioned` (a warning in place of `schema_step`, linked database or not) whenever the start
command, a release task or the package's own start script pushes, with the command that commits
migrations instead (`prisma migrate dev`, `drizzle-kit generate`). When a push does refuse, the failed
readiness gate names it — `runtime_schema_push_refused`, from Prisma's `--accept-data-loss` message or Drizzle's
rename and data-loss prompts — rather than a bare timeout. The tool must be installed by the lockfile; the
runtime stage copies the build's `node_modules`, so a devDependency is available to the start command.

## Static sites and site generators

A site generator's root used to be read as something else: Hugo's `layouts/` a site serving
`{{ .Content }}`, a Hugo Modules `go.mod` a Go service with no main package, MkDocs a Python service
asking for a start command, a Jekyll `index.html` raw Liquid, Zola's `templates/` raw Tera, mdBook
nothing at all. Detection now recognises each generator from its configuration, read as bounded data
(`frameworks_site.go`, `detect_site_generators.go`), and the root becomes one candidate the generator
builds and nginx serves. What stood in for it at that root is set aside with the reason: the templates
and committed output (`index.html` under the root), a `go.mod` with no Go code beside it, a docs
`requirements.txt` or a library's manifest with no server of its own, a `package.json` of PostCSS or
Tailwind. Which generator builds is read again from the commit when the build is prepared, never from
the plan.

| Generator | Recognised by | Recipe and image | Build | Output |
| --- | --- | --- | --- | --- |
| Hugo | `hugo.toml`/`.yaml`/`.json`, `config/_default/`, or `config.toml` with `baseURL` or a theme beside `content/`, `layouts/`, `archetypes/` or `themes/` | `site`, `ghcr.io/gohugoio/hugo` (extended, as its own `hugo` user) | `hugo --gc --minify --baseURL "${HUGO_BASEURL:-/}"` | `publishDir`, default `public` |
| Zola | `config.toml` with `base_url` and Zola's own shape: `content/` beside `templates/`, a committed theme, or a setting only Zola has (`compile_sass`, `taxonomies`, `[markdown]`, `[extra]`, …); beside an application that runs its own server from the same directory it ranks below it | `site`, the binary of `ghcr.io/getzola/zola` on Alpine (0.23 and later, musl) or Debian slim (earlier, glibc) | `zola build --base-url "${ZOLA_BASE_URL:-/}"` | `output_dir`, default `public` |
| mdBook | `book.toml` | `site`, the project's release archive on Alpine, checked against its digest | `mdbook build` | `build-dir`, default `book` (`book/html` with more than one renderer) |
| Jekyll | `_config.yml` with a Gemfile naming `jekyll` or `github-pages`, or with `_posts/`, `_layouts/`, `_includes/` or a theme; never beside `.nojekyll` or Hexo's `package.json` | `site`, `ruby:<3.1–3.4>-slim` with `build-essential` | `bundle exec jekyll build --baseurl ""` | `destination`, default `_site` |
| MkDocs, Zensical | `mkdocs.yml`, `zensical.toml` | `python` with an output directory | `mkdocs build`, `zensical build` | `site_dir`, default `site` |
| Sphinx | `conf.py` in `docs/`, `doc/`, `docs/source/`… beside an `index.rst`, or where `.readthedocs.yaml` says | `python`, built from the project above its docs, where autodoc's code is | `sphinx-build -b html <docs> <docs>/_build/html` | `<docs>/_build/html` |
| Pelican | `pelicanconf.py` | `python` | `pelican <PATH> -o <OUTPUT_PATH> -s publishconf.py` (`pelicanconf.py` without one) | `OUTPUT_PATH`, default `output` |
| Lume | a `lume/` import in `deno.json` | `deno` with an output directory | `deno task build` | `_config.ts`'s `dest`, default `_site` |

The Python and Deno recipes with an output directory build, then serve that directory from nginx the way
the JavaScript recipe always has; they need no start command, and preflight asks for none. A Python site
installs what `.readthedocs.yaml` lists for the documentation build, else a docs requirements file
(`docs/requirements.txt`, `requirements-docs.txt`, …), else the root's manifest as any Python project does
(with the generator's pinned release beside it when the manifest does not name it), else — nothing
declared — the pinned releases its configuration needs: MkDocs and Material for MkDocs (PyMdown
Extensions for a `pymdownx.*` extension on MkDocs' own theme), the Sphinx theme and extensions `conf.py`
names, Pelican with Markdown, and the plugins in `pythonSitePackages`. A library usually declares its
documentation tool in a dependency group (`[dependency-groups] docs`, `[tool.poetry.group.docs]`), which an
application's `uv sync --no-dev` or `poetry install --only main` leaves out: when the generator is not
among the project's own dependencies but its lock names it, the site's build installs every group
(`uv sync --frozen --all-groups`, `poetry install --all-groups`). Plugins and extensions are read from the
list items directly under `plugins:` and `markdown_extensions:` (or a flow list on the key's line); the
lists inside an item's options — a blog's categories, a fence's names — are not plugins. A plugin
outside that table has no package the recipe can be sure of, so the plan is refused with the plugin named
and a requirements file asked for. A plugin that dates pages from their commits brings `git` into the
image.

**Releases.** Hugo's release comes from `.hvm`, `HUGO_VERSION` in `netlify.toml`, `.tool-versions`, a
workflow's `hugo-version:` or `HUGO_VERSION:`, or `hugo-extended` in `package.json`; Zola's from
`ZOLA_VERSION`, `.tool-versions` or a workflow; mdBook's line (0.4 or 0.5) from a workflow or
`.tool-versions`, else 0.4 for a book whose `book.toml` uses a setting 0.5 removed or whose `theme/`
overrides the templates. A pin with an official image builds with that release; one older than any image
(Hugo's start at 0.141.0) builds with the reviewed default and says so (`site_generator_version`,
warning); a minimum (`module.hugoVersion.min`, a theme's `min_version`) is a floor, not a pin, and an
unpinned Hugo site is `hugo_version_unpinned` (warning). A pinned release whose image the registry does
not have when the build is prepared falls back to the default with a note in the run log. Jekyll builds
on the Ruby `.ruby-version`, the Gemfile's `ruby` line or `Gemfile.lock` asks for, from 3.1 to 3.4
(default 3.3). A Gemfile that pins one exact patch release (`ruby "3.3.0"`, or `ruby file: ".ruby-version"`
reading one) builds on that release's own image, `ruby:3.3.0-slim`, since Bundler refuses any other; a
patch release with no image builds on its line's newest with a note in the run log. Another release line is
`jekyll_ruby_version` (warning).
A `Gemfile.lock` installs frozen; one that lists no Linux platform gains `x86_64-linux` and
`aarch64-linux` first. A site with no Gemfile gets one naming the `github-pages` gem, as GitHub Pages
builds it, and `dependencies_unpinned` says how to lock it.

**Building for the root it is served from.** Hugo, Zola, Jekyll and mdBook are built for `/`: a GitHub
Pages project site's `baseURL`, `base_url`, `baseurl` or `site-url` names a sub-path this server does not
serve it under. Hugo's and Zola's base URLs follow the planned domain through `HUGO_BASEURL` and `ZOLA_BASE_URL`, build
variables detection declares with the domain templates `{{scheme}}://{{hostname}}/` and
`{{scheme}}://{{hostname}}`, so their sitemaps and feeds name the site's own address rather than relative
paths. The GitHub Pages gem derives `url` from the repository's GitHub name, which a
checkout here does not carry, and stops the build; a site whose `_config.yml` sets no `url` builds with a
one-line override (`url: ""`) the recipe writes, and `PAGES_DISABLE_NETWORK=1` keeps the gem from asking
GitHub's API while it builds.

**Sub-paths.** A framework whose output is built for a sub-path — a literal `baseUrl` in
`docusaurus.config.*`, `base` in `vite.config.*`, `astro.config.*`, `.vitepress/config.*` or
`.vuepress/config.*`, SvelteKit's `kit.paths.base`, Angular's `baseHref` or `--base-href`, Create React App's
`homepage`, Vue CLI's `publicPath`, Gatsby's `pathPrefix` with `--prefix-paths`, Nuxt's `app.baseURL`,
Next's `basePath` for a static export an operator serves from `out/`, a `--base` flag in the build script —
is served under that path, with `/` redirecting there (302, relative)
and the fallback and 404 pages under it, and preflight's `static_base_path` (warning) says how to serve it
at the root instead. A base the configuration computes (`base: process.env.BASE`) is served at the root
and named (`static_base_path_computed`), since what it evaluates to here is not something reading can
know. The key is read from the object the configuration file exports (`export default`, `module.exports`,
through `defineConfig(…)`, an arrow function or a variable; `detect_js_config.go`) and never from inside a
nested object or a comment: VitePress's sidebar groups carry a `base` of their own, Nuxt's
`runtimeConfig.public.baseURL` is an API's, and a commented-out `// base: '/repo/'` is not configuration.
Only a plain path of letters, digits and `._~-` segments is ever written into the configuration.

**Other hosts' rules.** `_redirects` and `_headers` (Netlify and Cloudflare Pages, at the root or in
`public/` or `static/`, where a framework copies them into its output, and first in the directory a
committed site serves), `netlify.toml`'s `[[redirects]]`
and `[[headers]]` (from the checkout's top too, when its `[build] base` is the site's root) and
`vercel.json`'s `redirects`, `rewrites` and `headers` become nginx rules when the build is prepared:

- an exact path or a `/dir/*` splat (Vercel's `:path*` and `(.*)` tails too), to a path or an http(s)
  URL, with 301, 302, 303, 307 or 308, as a `return` that keeps the query string;
- a 200 rewrite to a page of the site, served from its target in the rule's own location as the host
  serves it, and `/dir/* /dir/index.html 200` as a single-page application in that directory that keeps
  its own page under the site-wide fallback; `/* /index.html 200` is the fallback switch, and
  `/* /app.html 200` the fallback to that page;
- a header for every path, a `/dir/*` prefix or one exact path, as `add_header … always`, repeated in a
  location with headers of its own because nginx inherits none into it.

Each path has one location block: nginx refuses to start on a second `location = /old` or
`location /app/`, so the first rule for a path is the one kept, as the hosts apply the first rule that
matches; the same rule declared twice (in `_redirects` and again in `netlify.toml`, as a migration leaves
it) is one rule, and a header rule for a path a redirect, a directory's application or the 404 page
already answers joins that block. Placeholders (`:slug`), conditions (country, language, role, query,
`has`), a proxy to another host or to a Netlify function, a redirect or rewrite into its own prefix (it
would match itself again), a second rule for a path, a redirect from `/404.html`, any path with a dot
segment other than `.well-known` or naming `_redirects` or `_headers` (an exact location would end nginx's
search before the dot-path refusal and serve `/.env`), `Basic-Auth` (a password, not a header), framing and
transport headers and any value with a `"`, `\`, `$` or control character are left out and counted
(`static_redirects_unsupported`, warning); what is applied is `static_hosting_rules` (pass). A platform
file the server does not read for the site (one outside its root) keeps its own count, and the same
finding says it is not read. At most 100
redirects and 64 header rules are written, and on a site built for a sub-path each rule's paths move
under it. A rule applies whether or not a file exists at its path, as Netlify's forced rules do.

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
  so their best candidates compare on whether they may be chosen, their standing and confidence, then
  where they sit ([Repository shape](#repository-shape-and-candidate-selection)), never on which one
  builds: a helper image in `docker/db/` or `tools/worker/` that builds does not beat the application
  whose recipe needs one decision. A Dockerfile
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
that the rule is set aside; a JavaScript install input below the root is brought back by an exception
instead ([JavaScript installs](#javascript-installs)), and the run log names each rule set aside or
overridden. Then it excludes `**/node_modules`, `.dockerignore` and the dashboard's own files. The static, PHP, Node and Deno images, which are served or copied whole, also exclude `.git`;
recipes whose toolchains stamp or version builds from Git (Go, Python's setuptools-scm, Maven's
git-commit-id, SourceLink, Hugo's `enableGitInfo`, the MkDocs and Jekyll plugins that date pages from
their commits) keep it; the site recipe's nginx stage copies only the output directory. A static site also excludes `.env` and `.env.*`. Committed `.env`
files are otherwise left in: Next.js and Vite read public build values from them.

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
(`build_node_lockfile.go`): a dependency missing from the lock is drift for every manager; one removed
from `package.json` but still locked is drift for Bun, pnpm and Yarn, while `npm ci` leaves it out and
installs, so npm's reading stays `in_sync` and lists it under `extra`; pnpm and Yarn also compare the range
text, while npm and Bun accept a changed range the locked version still satisfies (npm's range grammar is
evaluated as data, `node_semver.go`). npm and Bun record a package's peer dependencies too, and so does
pnpm when the lock's `settings.autoInstallPeers` is on (pnpm 8's default): a peer the package does not
also depend on is then expected among its locked dependencies, as in a Turborepo UI package that
declares `react` as a peer. Yarn 1 is read from its entry headers, Berry from its workspace
blocks, pnpm up to its package list (only the bytes read are charged to the budget), npm entry by entry,
Bun's text lock as JSONC; `bun.lockb` is binary and can only prove a name missing. A lockfile and the
workspace manifests it is compared with are parsed once per detection, whichever member is compared, so a
monorepo's members never exhaust the budget re-reading the same root lockfile. Notes and listed names are
bounded to what a saved draft validates. Each reading on the
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
logged note when that release has no image (other Bun declarations:
[JavaScript runtime and toolchain](#javascript-runtime-and-toolchain)). `package_manager_version` (a pass) and the build evidence's
`toolchain` name the release and what chose it.

**One toolchain for the build and the server.** The pinned releases are installed through Corepack into a
`toolchain` stage (`COREPACK_HOME=/opt/corepack`), and both the build stage and a server's runtime stage
start from it, with `COREPACK_ENABLE_NETWORK=0` at run time: a start command that runs `pnpm`, `yarn` or
`bunx` finds the exact release offline, where the runtime stage used to have no pnpm at all; the pass
`runtime_runner_available` names each manager program the start command (or a package script it runs)
reaches and where the server image gets it. The runtime and build stages put `node_modules/.bin` on
`PATH`. Bun is copied from its digest-pinned image beside
Node, with `bunx` linked to it: Bun installs and runs `bun` and `bunx` commands, and anything started
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
the locked tree (`peer_dependencies_legacy`). A lock that lacks the image's own platform binaries
(npm/cli#4828 — Rollup, lightningcss, Tailwind's oxide, SWC, sharp) gets them added at the exact version
the parent names, after `npm ci` (`optional_binary_missing`): the `-linux-<arch>-musl` (or `linuxmusl`)
packages on Alpine, the `-linux-<arch>-gnu` (or `-glibc`) ones on the Debian image — never the other C
library's, which npm refuses outright when asked for by name (`EBADPLATFORM`). A lock resolving packages
from an intranet host is `registry_host_private`. A workspace package or `file:` directory is a `link`
entry whose `resolved` is a path in the checkout: it is neither a download nor a Git dependency, and a
lockfile's `resolved` is read as a Git source only when it is a URL (`git+…`, `git://`, `github:`, a
`.git` address), never by the `owner/repo` shorthand a `package.json` range may use.

**Registry credentials.** `.npmrc` (`${NAME}`), `.yarnrc.yml` (`${NAME}`, with or without a default) and
`bunfig.toml` (`$NAME`) are read as data for the variables their credentials name. Each becomes a detected
variable marked for the install step (`step: "install"`), required when a dependency's scope installs from
that registry, or when Yarn would abort without it (Berry fails every install on an unset variable with no
default). A value given in the draft for a variable detected only in those files is declared with build
scope alone — the running application never receives the token — and mapped to the install step
automatically; preflight's `registry_token_missing` is blocked when a required credential cannot reach
the install and a warning otherwise, for the Node recipe and the PHP recipe's asset stage alike, and a
literal token committed to a configuration file is `registry_token_committed` (the value is never
echoed).

**Workspaces.** A package with no lockfile of its own installs from the nearest ancestor whose lockfile
and `workspaces` (or `pnpm-workspace.yaml` `packages`) include it; a `pnpm-workspace.yaml` of settings
alone is not a workspace. The build context widens to that root (`prepared.contextDirectory`), the
install runs there, the build and the server run in the member's directory, and every workspace the
lockfile records is compared; preflight shows it as the pass `workspace_lockfile` ("Installed from the
workspace lockfile at ."). A member of a Turborepo that depends on workspace packages builds with
`<runner> turbo run build --filter=<name>...`: a dependency is a workspace package when its range uses
`workspace:` (pnpm, Bun, Berry) or when it names another member detection found under the same root, which
is how npm and Yarn 1 workspaces refer to one (`"@acme/shared": "*"`). Without Turborepo, a member whose
workspace dependencies build themselves (transitively, those with a `build` script) builds them first
through the manager's own workspace commands, which find the workspace root from the member's directory:
`pnpm --filter <name>... run build` (pnpm orders the dependencies itself), and dependencies first for the
others — `npm run build --workspace=<dependency> && … && npm run build`, `yarn workspace <dependency> run
build`, `bun run --filter <dependency> build`. The root that only declares the workspace (package.json
`workspaces` or `pnpm-workspace.yaml` `packages`, with nothing of its own to start) is offered but never
chosen over its members. An Nx integrated repository keeps its applications in `project.json` files
under `apps/` (or nx.json's `workspaceLayout.appsDir`, one or two levels down) with no `package.json` of
their own: each application project whose build target's executor is known (`@nx/next:build`,
`@nx/vite:build`, `@nx/esbuild:esbuild`, `@nx/webpack:webpack`, `@nx/node:*`, the Angular builders), or
whose build `@nx/next/plugin` or `@nx/vite/plugin` infers from its `next.config`/`vite.config`, is a
candidate at the workspace root that builds with `NX_DAEMON=false NX_NO_CLOUD=true <runner> nx run
<project>:build` (with `--configuration=production` when the target has one) and serves what that build
writes: `next start <outputPath>` (inferred: `next start <project root>`), the Vite or Angular site, an
Angular application with `ssr` from `node <outputPath>/server/server.mjs` on 4000, or `node
<outputPath>/main.js` (for esbuild, the file its `outputFileName`, else its `main`, names). The root
package itself is not offered, and an unknown executor keeps a decision. The applications share the
workspace root and the build method, so a saved plan, which carries no selection, is matched to its
application by the build and start commands it runs, then by its build command alone, before preflight,
`analyze_plan` or the check before Deploy judge it. The member's directory is written
unquoted into `WORKDIR`, `ENV PATH` and `RUN` lines, so one outside letters, digits and `. _ @ + - /` is
refused before Deploy and at build (`workspace_member_path_unsupported`, blocked) rather than rendered
into a Dockerfile BuildKit cannot parse. A Yarn 1 member that depends on a sibling by a plain range reads
as stale, since Yarn 1 records no workspace in its lock; the install then runs unfrozen with a warning.

**The repository's `.dockerignore`.** The generated `.just-dashboard/Dockerfile.dockerignore`
([Build context](#build-context)) is written at the install's context — the workspace root for a
member — and keeps every install input the recipe reads: a root-level one (`package.json`, the lockfile,
`.npmrc`, `.yarnrc.yml`) by setting aside the rule that would leave it out, and one below the root or a
directory (a member's `package.json`, `bunfig.toml`, `.yarn/releases`, `patches`) by an exception after
the repository's rules, so its other exclusions under that directory stand. The run log names each input
a repository rule had excluded.

**Commands follow the manager.** Choosing a manager in the configure form or Build settings swaps a
command that is still one detection proposed for the one it proposes for the new manager (`nodeInstalls`),
and "From the lockfile" means the manager detection resolved; a command the operator wrote keeps its
words with only its plain runner segments moved. At build time a saved command whose plain runner names
another manager (`npm run build` after a commit replaced `package-lock.json` with `bun.lock`) runs through
the resolved manager's runner; the run log says so and preflight raises `runner_mismatch`. A second
install in the build command is `install_in_build_command`. Review lists the install, build and start
commands the Dockerfile runs (`build_commands`).

## JavaScript runtime and toolchain

`deploy/build_node_runtime.go` decides the image a JavaScript build runs on inside the same install plan,
from the same files read as data, so detection (`nodeVersion` on the candidate, the plan's findings under
`nodeInstalls`), preflight and the recipe agree on it.

**Which Node.** The catalogue builds on Node 20, 22 and 24 (`node:<major>-alpine`, digest-pinned like every
base). The Build setting `nodeVersion` (20, 22 or 24; the new-project form and Build settings offer it,
for a JavaScript recipe and for a PHP recipe's asset stage) outranks everything the repository
declares — preflight then names it (`node_version_selected`, "22 (Build settings)"), a build log's
engine mismatch offers the newest major its range allows as the fix, and Yarn 1's or an engine-strict install's refusal of it is the recipe's `recipe_unsupported`
before Deploy. Without it, the first declaration that can be read decides: the nearest version file, looking from the
package's directory up to the top of the checkout the way nvm, fnm and asdf do (`.nvmrc`, then
`.node-version`, then `.tool-versions`' `nodejs`/`node` line, in one directory), then `volta.node`,
`devEngines.runtime` and `engines.node` in `package.json` (the package's, then its workspace root's).
A version file names a major (`22`, `v22.11.0`, `lts/jod`; `lts/*` and `node` are 24). A `package.json`
range keeps the default 22 when it allows it — most `engines` fields are a floor — and otherwise takes the
newest major it allows. Without a declaration the build runs on 22, which stays the default until a
recipe version bump re-verifies the live fixtures on 24. A declaration outside the catalogue (`18`,
`18.x`) runs on the nearest major with `node_version_unsupported` (a warning); it is refused only where
the install is certain to stop on it — Yarn 1, or npm and pnpm with `engine-strict=true` in `.npmrc`,
check the root package's `engines.node` against the Node they run on. Node 20 is past end of life
(`node_version_eol`). `node-sass` has no binary for Node 22 and later and does not compile against them,
so a package that installs it and declares nothing newer builds on Node 20; one that pins a newer Node
keeps it with `node_sass_unsupported`. `node_version_selected` (a pass) names the release and its
source, the candidate records it as `nodeVersion` (`"22 (.nvmrc)"`), and the build evidence as
`prepared.nodeVersion`. Node 25 and later images ship without Corepack, which installs the pinned pnpm
and Yarn releases, so they are not in the catalogue.

**Which Bun.** Bun is copied beside Node from `oven/bun:<release>-alpine`: the release `packageManager`
names, else `.bun-version` or `.tool-versions`' `bun` line (an exact release or a `major.minor` line),
else an `engines.bun` range the newest 1.x does not satisfy (its minor line), else the newest 1.x image.
A release without an image falls back to the newest 1.x with a logged note.

**What the dependencies need from the image.** The image stays Alpine unless a package the application
loads ships glibc binaries only — `onnxruntime-node` (and `@huggingface/transformers` or
`@xenova/transformers`, which depend on it), `@tensorflow/tfjs-node`, or `playwright` as a runtime
dependency — which install on musl and then fail to load; those build and run on the same major's
`node:<major>-bookworm-slim` (`glibc_image_selected`), with Bun copied from `oven/bun:<release>-slim`.
System packages go where they are needed, installed before the source is copied so they are cached apart
from it (`apk add --no-cache`, or `apt-get install --no-install-recommends` on Debian), and each has a
finding:

| Dependency | Build stage | Runtime stage | Finding |
| --- | --- | --- | --- |
| A native addon that compiles when no prebuilt binary matches the platform, Node release and C library (`better-sqlite3`, `sqlite3`, `bcrypt`, `argon2`, `canvas`, `node-sass`, `re2`, `isolated-vm`, `node-pty`, …, from `package.json` or the lockfile) | `python3 make g++` | — | `native_addon_toolchain` |
| `canvas` on Alpine, which publishes no musl binary | cairo, pango, jpeg, gif, rsvg and pixman headers, `pkgconf` | their libraries | `native_addon_toolchain` |
| A Git dependency (`github:`, `owner/repo` or `git+https:` in `package.json`, a `.git` URL, or a lockfile entry whose `resolved` is a Git URL — not a workspace or `file:` link) | `git` (`openssh-client` for SSH) | — | `git_dependencies`; `git_dependency_credentials` (warning) over SSH, which has no key |
| Prisma (`prisma` or `@prisma/client`) | `openssl` | `openssl` | — (logged) |
| `puppeteer`/`puppeteer-core` in `dependencies` | install skips the Chrome download (`PUPPETEER_SKIP_DOWNLOAD`) | Chromium, fonts, `PUPPETEER_EXECUTABLE_PATH` | `headless_browser` (warning) |
| `playwright`/`playwright-core` in `dependencies` (Debian) | `PLAYWRIGHT_BROWSERS_PATH=/app/.cache/ms-playwright`, `<runner> playwright install chromium` | the same path, `<runner> playwright install-deps chromium` | `headless_browser` (warning) |

Compilers never reach the runtime image. Puppeteer's and Playwright's own downloads land in the build
stage's home directory, which the runtime stage never copied, so the recipe keeps them out of it or
inside the application. A test runner's Playwright in `devDependencies` changes nothing.

**Prisma.** When the `prisma` CLI is a dependency and a schema exists where Prisma looks for one —
`prisma/schema.prisma`, `schema.prisma`, a `prisma/schema/` directory, or the path `prisma.config.*`'s
`schema:` or `package.json`'s `prisma.schema` declares — the build runs `<runner> prisma generate` after
the install and before the build command (`prisma_generate_added`). The install cannot be trusted to
have done it: pnpm 10 and Bun skip `@prisma/client`'s install script, and Prisma 7 generates into a
directory the repository ignores. No `--schema` flag is passed; Prisma resolves its configuration
itself. With only `@prisma/client` declared nothing runs, since `npx` would download an unpinned CLI. A
schema at a declared path also moves the schema tool's lookup (migrations beside it, or at `migrations:
{ path }`), so `prisma migrate deploy` is still chained into the start command.

Prisma 7's `prisma.config.ts` reads its datasource URL through `env("DATABASE_URL")`, which throws when
the variable is unset even for `generate`, which never connects. The names `env()` reads (from
`prisma.config.*` or `.config/prisma.*`, read as text) get a placeholder on every step that may run
`generate` — the install, whose root `postinstall` or `@prisma/client` script can run it, the recipe's
generate step, and the build command when it runs `prisma generate` itself — written as
`export DATABASE_URL="${DATABASE_URL:-postgresql://127.0.0.1:5432/prisma-generate}" && …`. The value is a
recipe constant shaped like the schema's provider (`mysql://…`, `sqlserver://…`, `file:./prisma-generate.db`,
`mongodb://…`; a name without URL, URI or DSN gets `prisma-generate`), never a credential or an operator
value, and it points at nothing. The RUN's shell expands it, not the Dockerfile parser, so a value the
build mounts for that step wins, and it ends with the RUN: the runtime never sees it. A build command that
runs `prisma migrate` or `prisma db` gets no placeholder, since it needs the real database. The real
value is never widened to the install on its own — the install also runs every dependency's install
script — unless the variable is mapped to `install_and_build` (`prisma_config_env`). Environment
discovery reads the same `env()` calls as build-time reads; detection records the names the placeholder
covers as `nodeBuild.prismaEnv` (none when the detected build command connects), and for a Node recipe
build the environment check does not refuse them as `build_variable_missing_*`, since the build runs
without them. A repository Dockerfile gets no placeholder, so there they stay build-time reads.

**The build command's RUN** (`build_node_build.go`) leaves V8's heap at its own default (a quarter of
physical memory, at most about 4 GiB). `NODE_OPTIONS` is inherited by every `node` process a build starts —
Next.js's page workers, a bundler's minifier workers — so a raised limit is a raised ceiling for each of
them at once, and on a small host it turns a "JavaScript heap out of memory" into swapping and the
kernel's OOM killer choosing among this server's services; Turbopack's memory is native and not bounded
by the flag at all. Preflight's `build_memory_low` warns before Deploy when the host's free memory and swap
are below the selected build's estimated peak, and an operator who has the memory sets `NODE_OPTIONS`
(`--max-old-space-size=…`) as a build variable, which reaches the RUN as is. The RUN adds only what the
toolchain needs, below; the PHP recipe's asset stage builds the same way.

- A webpack 4 toolchain — `react-scripts` before 5, `@vue/cli-service` before 5, `webpack` before 5.61,
  Nuxt before 2.16, `@angular-devkit/build-angular` before 13, `laravel-mix` before 6,
  `@symfony/webpack-encore` before 1, as a direct dependency of the package, by the installed lockfile's
  version while the range still allows it, else the range's floor — hashes with MD4, which OpenSSL 3
  refuses (`error:0308010C`). A webpack 4 that only another dependency pulls in (Storybook 6 beside a
  Turbopack build) is not the build's toolchain, and a competing lockfile says nothing. Its build runs with
  `NODE_OPTIONS="--openssl-legacy-provider${NODE_OPTIONS:+ $NODE_OPTIONS}"`, keeping any `NODE_OPTIONS` the
  build has (`legacy_openssl_provider`, a warning that names the upgrade).
- T3 Env (`@t3-oss/env-nextjs`, `-core`, `-nuxt`) validates its schema when `next build` imports it. The
  schema file (`src/env.js` and the usual places) is read as text for its `server` and `client` keys,
  leaving out those whose schema is optional or has a default. When a required server key is not mounted
  in the build and the schema honours `SKIP_ENV_VALIDATION` (create-t3-app's does), the build runs with
  `SKIP_ENV_VALIDATION="${SKIP_ENV_VALIDATION:-1}"`; validation still runs when the server starts. The
  decision follows the build's mounts, so the Dockerfile changes when the variables do.
- A build or start command, or a package script it runs, that passes `node --env-file=<path>` (or `tsx`
  or Bun) exits when the file is missing, and an env file is rarely committed. The build stage (before
  the build) or the runtime stage (after copying the application) runs `[ -e <path> ] || : > <path>`
  (`[ -e <path> ] || { mkdir -p <dir> && : > <path>; }` when the path has a directory, which may be
  ignored or never committed), and the process environment takes precedence over the empty file
  (`env_file_placeholder`). Only a plain path inside the package is written; `--env-file-if-exists` needs
  nothing.

**Before Deploy.** Preflight (`preflight_build.go`) judges what the build will meet from the candidate's
`nodeBuild` record, the configuration's values and the host, and names each case before a build runs:

| Finding | When |
| --- | --- |
| `build_memory_low` (warning) | the host's free memory and swap are below the build's estimated peak (~2 GiB for Next.js, Nuxt, Angular, Gatsby, Docusaurus, Strapi, Payload; ~1 GiB for other frameworks) |
| `build_env_validation_skipped` (pass; warning when a skipped variable has no value at all) | the schema's required server variables have no build value and the schema honours `SKIP_ENV_VALIDATION`; the environment check then does not refuse them as `build_variable_missing_*` |
| `build_env_missing` (warning) | the same, and the schema cannot be skipped, so the build stops with "Invalid environment variables"; it lists only what the environment check did not already refuse per field as `build_variable_missing_*` |
| `build_env_client_missing` (warning) | a `client` variable has no build value; the browser bundle gets `undefined` |
| `build_database_unreachable` (warning) | Next.js, Nuxt, Astro, SvelteKit or Gatsby with a database client, and a build-scoped URL pointing at `db-N.jd.internal`, at loopback, or a linked database reference: the build runs apart from the environment's network, so a prerendered page that queries it fails; the action is rendering those pages on request |
| `port_variable_mismatch` (warning) | a `PORT` variable differs from the internal port the proxy and readiness check use |
| `node_env_not_production` (warning) | `NODE_ENV` other than `production` reaches the build or the server |
| `host_variable_loopback_hostname` (warning) | `HOSTNAME` on loopback, which Next.js standalone binds to (a loopback `HOST` is the environment check's `host_variable_loopback_host`) |

A pasted local `.env` is where `PORT` and a development `NODE_ENV` usually come from, so both are left
out of it before they reach the plan: the new-project form drops their lines from the pasted block
(saying so under it), and the Settings import sends them as `skip` — the import and its dry run leave
them out, the preview's verdict is `skipped` ("left out · set by the deployment") — unless the operator
keeps them. `NODE_ENV=production` is kept, whatever the build method: the recipe sets it anyway, and an
image a repository's own Dockerfile builds may rely on it being passed. A Compose stack keeps both by
default, since its file may interpolate `${PORT}` itself. A variable added one at a time is deliberate
and is kept; the warnings above still name it.

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
With an output directory the recipe builds a site instead — MkDocs, Zensical, Sphinx, Pelican, or any
build command that writes files — and nginx serves what it wrote
([Static sites](#static-sites-and-site-generators)). `dependencies_unpinned` names each recipe's own way
to pin: `composer.lock` for PHP, `deno.lock` for Deno, `Cargo.lock` for Rust, `Gemfile.lock` for Jekyll
and a requirements file for a Python site that declared none.

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
naming the frameworks detection proposes one for, and preflight says so first: `start_command_missing`
(blocked, on the start command) for the Python, Deno and PHP recipes and a JavaScript server with no
static output. The configure form's first step refuses to go on without one for the same plans
(`needsStartCommand`).

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
`go.mod`) are not part of the module, and neither are files whose names start with `_` or `.`.
Detection runs the recipe's own scan (`scanGoModule`: file headers only, at most 10,000 files and 32 MiB
per module) at each Go candidate's root, apart from its walk's shared read budget, so a module whose
generated or internal sources outweigh that budget is still read whole and never mistaken for a
library; a module past the scan's own bound carries the recipe's refusal as its `RecipeIssue`.
Detection records `goMainPackages` (the first 64, with `goMainPackagesOmitted` counting the rest, so
a package chosen past the list is not called missing) and chooses `goPackage` by
the layout's own convention: the module root, then `cmd/<module name>`, then the one of
`cmd/{server,api,web,app}` that exists, then the only main that imports `net/http` or a known router or
RPC server. A tie asks — a `NeedsDecision` and the `go_main_ambiguous` decision on
`build.goPackage`, which a run cannot go past; the packages it names are one bounded line, the first
few then "and N more" — and a module with no main package is a low-confidence
`Go library` candidate (`goLibrary`) with the blocked `go_main_missing`. `build.goPackage` names the
package the recipe builds (`go build … ./<goPackage>`); it must be a buildable main, or the recipe and
preflight refuse it. Without it the recipe makes the same choice detection did, and refuses an
undecided module with its main packages named. When preflight has the tree, a dry run that prepares
the plan outvotes these findings — `go_main_missing`, `go_main_ambiguous`, `go_version_unsupported`,
like `package_manager_lockfile_missing` and `package_manager_ambiguous` — because the recipe decided
each of them against the commit itself.

A custom build command executes exactly as configured and must produce an executable at `/out/app`; it
decides what it compiles, so no main package is asked for. The historical detected `go build ./...`
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
`--allow-all`. A `fresh` import names the framework. A Deno 2 project that keeps its dependencies and
scripts in `package.json` — its `start` script runs `deno`, or `deno.lock` is its only lockfile — is a
Deno candidate too, started with `deno task start` (Deno runs package.json scripts as tasks); the Node
candidate for the same package is low confidence, since its scripts call a `deno` the Node image lacks.
A start task that runs a development watcher (`--watch`, Fresh 1's `dev.ts`) is replaced by the
`preview` task, else a non-watching `serve` task, else `deno run -A main.ts`; with none of them it stays
and preflight says so (`deno_start_watch_mode`). The port is read from the start task (`deno serve
--port 3000`, else `deno serve`'s 8000) or the served file (`Deno.serve({ port: 3000 })`, Oak's `listen({
port })`, a `Deno.env.get("PORT")` read); with nothing readable it is `Deno.serve`'s default of 8000,
stated as evidence rather than asked as a question.

The release is the repository's own declaration, read as data: `.dvmrc` and `.tool-versions` from the
root up to the checkout, then `deno-version` in the checkout's `.github/workflows/*.yml` (setup-deno).
An exact release builds on `denoland/deno:alpine-<release>` (a release with no image falls back to its
major's reviewed release, with a note in the run); a major, `v2.x` or nothing builds on the catalogue's
reviewed release of that major — `denoland/deno:alpine-2.9.7`, and `alpine-1.46.3` for Deno 1 —
because Docker Hub publishes no major-only Alpine tag and the floating `alpine` tag moved every rebuild
onto the newest major. A major the catalogue lacks builds on Deno 2 with `deno_version_mismatch`. The
Toolchain evidence names the release and where it was declared (`deno 1.46.3 (.dvmrc)`).

Deno 2 installs with `deno install`, `--frozen` when `deno.lock` records everything the workspace
asks for. Detection compares the lock's `workspace.dependencies` and `workspace.packageJson.dependencies`
(version 3 and later) with the `jsr:` and `npm:` imports of `deno.json` and the dependencies of
`package.json`, written the way Deno writes them (`^X.0.0` as `X`, `~X.Y.0` as `X.Y`, anything else
verbatim, a subpath dropped). A specifier the lock lacks is `deno_lock_outdated` (warning), and the
install runs without `--frozen` — the Deno twin of a stale `package-lock.json` beside the lockfile that
is in sync, resolved again rather than stopping on "The lockfile is out of date". The build task runs
next. The file the start command runs (through `deno task`, `deno run … <file>` or `deno serve …
<file>`) is then cached with its whole module graph, `deno install --entrypoint <file>`, so URL and `npm:`
imports written only in code are fetched at build rather than at every container start; the lock still
verifies every module it records. It runs after the build because the build may write a module the
entry imports, and only for an entry the checkout has: a start that serves what the build writes
(Fresh 2's `deno serve -A _fresh/server.js`, an adapter's `dist/server.js`) has nothing to cache before
the build and imports what `deno.json` already names. Deno 1, whose `install` is a script installer,
caches the entry with `deno cache <file>` at the same point. With an output directory — Lume, whose
`lume/` import names it — the build task's output is served by nginx, there is no start command and no
entry to cache ([Static sites](#static-sites-and-site-generators)).

## PHP

A `composer.json`, or an `index.php` at the root, under `public/`, or under a conventional document root
(`web/`, `webroot/`, `htdocs/`, `public_html/`, `www/`) where nothing else owns the directory above it,
is a PHP application; an `index.php` deeper in the tree (WordPress's `wp-admin/`, a theme) names
nothing on its own. The framework decides where the front controller is served from, and what the start
runs first: `laravel/framework` (Laravel, `public/`, `php artisan migrate --force`), `symfony/framework-
bundle` (Symfony, `public/`, `doctrine:migrations:migrate` with the migrations bundle), `slim/slim`,
`mezzio/mezzio` and `laminas/laminas-mvc` (`public/`), `cakephp/cakephp` (CakePHP, `webroot/`, `php
bin/cake.php migrations migrate` with `cakephp/migrations` and files in `config/Migrations`),
`codeigniter4/framework` (CodeIgniter, `public/`, `php spark migrate --all` when
`app/Database/Migrations` has files), `yiisoft/yii2` (Yii, `web/`, `php yii migrate --interactive=0`
when `migrations/` has files; the image sets `YII_ENV=prod YII_DEBUG=0`, which the prepend below defines
before the front controller's own development defaults) and `drupal/core(-recommended)` (Drupal, the
`extra.drupal-scaffold.locations.web-root` of composer.json, `web/` by default; `vendor/bin/drush deploy
-y` is named as the release task to add once the site is installed). A migration step runs only where
there are migrations, since a migrate with nothing to migrate still needs a database. Anything else is
plain PHP served from the directory that holds `index.php`. A Heroku `web: heroku-php-apache2 public/`
(or `heroku-php-nginx`) is translated rather than run — the buildpack's server is not in the image and
`--no-dev` does not install its scripts — into FrankenPHP serving the document root it names after the
framework's migrations; `-C`/`-F`/`-i` server configurations are `php_procfile_server_config_ignored`
(`-p`'s port and `-l`'s log are skipped as values), and a `release:` line is the release task.

The image is `dunglas/frankenphp:1-php<release>-alpine`, one catalogue entry per release — FrankenPHP
serves on 80 with its own worker, so no nginx or php-fpm pair. The release satisfies composer.json's
`php` and every production package composer.lock installs, read with Composer's semantics
(`php_constraints.go`: `^`, `~`, wildcards, `>=`/`<` ranges with or without a space, hyphen ranges,
`||` and a single `|`, stability flags; a branch or alias is never a verdict). `config.platform.php`,
the release the lock was resolved for, wins when it qualifies; otherwise the newest release up to 8.4.
8.2 to 8.5 are in the catalogue and 8.3 is what an application that names nothing gets; 8.5 is built
only when the constraints require it or the Build settings choose it, so adding a release does not move
every PHP deployment onto it at their next build. The evidence names the locked package that narrowed
the choice (`php ^8.1; laminas/laminas-diactoros requires ~8.1.0 || ~8.2.0 || ~8.3.0 (composer.lock) →
PHP 8.3`). A requirement no catalogue release satisfies is `recipe_unsupported`, naming the package; a
release chosen in the Build settings (`build.phpVersion`) that one of them refuses is
`php_version_unsupported` (blocked), and a build that stops on "your php version does not satisfy that
requirement" proposes the release that does.

The extensions are installed with `install-php-extensions` before the source is copied, so the layer is
cached. They are, with the reason each is recorded (`php_extensions`, a pass that lists them):
`pdo_mysql`, `pdo_pgsql`, `mysqli` and `opcache` always, so a linked database works without asking (a
default the application needs for a reason of its own is listed with that reason instead); every
`ext-` requirement of composer.json and of composer.lock's production packages (read under a budget of
its own, 4 MiB, keeping only names, versions, requirements, `provide`/`replace` and whether a package has
an archive — `require-dev` packages are not checked by a `--no-dev` install and are skipped); without a
lock, a table of the popular packages whose own requirements stand in (Filament `intl`, Horizon `pcntl`
and `redis`, PhpSpreadsheet and Laravel Excel `gd` and `zip`, CodeIgniter and CakePHP `intl`, Drupal
`gd`, MongoDB `mongodb`, Media Library `exif`, Intervention `gd`); the calls the application's own code
makes, read breadth first from up to 400 files and 3 MiB (`mysqli_*`, `image*`, `ZipArchive`,
`NumberFormatter` and Laravel's `Number::`, `bc*`, `exif_read_data`, `new Redis`, `pcntl_*`, `gmp_*`,
`Imagick`, `SoapClient`, `ftp_connect`, …, each recorded as the call and the file it was seen in:
`mysqli_connect in includes/db.php → mysqli`); Laravel's phpredis client when `.env.example` makes Redis a
cache store, queue, session or broadcast driver (`CACHE_STORE=redis`, `QUEUE_CONNECTION=redis`, …) and
`predis/predis` is absent — the stock `.env.example`'s `REDIS_HOST` and `REDIS_CLIENT=phpredis` configure a
client nothing uses, so they do not cost every Laravel build the extension's compile, and an application
that switches a driver to Redis only in its deployment variables is told `Class "Redis" not found`
(`runtime_php_extension_missing`), whose fix is `"ext-redis": "*"` in composer.json's require; and
WordPress's `mysqli gd exif intl zip`. Built-ins such as `mbstring` are skipped, Composer's other
spellings (`zend-opcache`) are read as the extension, and a name `install-php-extensions` cannot build for
every catalogue release on Alpine (its `data/supported-extensions`; `memcache` stops at 8.4 and is left out
on 8.5) is left out with `php_extension_unsupported`; a lock too large or not written by Composer is
`php_extensions_unverified`.

Every image copies PHP's own `php.ini-production` (errors logged, never shown; deprecations no longer
print before headers) and adds uploads as large as the managed proxy lets through (64 MB),
`memory_limit=256M`, `expose_php=Off` and an `auto_prepend_file`. Behind the proxy the connection is plain
HTTP, so FrankenPHP never sets `HTTPS`; the prepend — constant text written with `printf`, with no plan
value in it — sets `HTTPS=on`, `REQUEST_SCHEME=https` and `SERVER_PORT=443` from `X-Forwarded-Proto` and
`REMOTE_ADDR` from the last `X-Forwarded-For` hop (the proxy appends the peer it saw — nginx's
`$proxy_add_x_forwarded_for`, the same address it sends as `X-Real-IP` — so the last hop is the visitor
whatever earlier hops a client forged), and only for a request whose own peer is a loopback, private or
reserved address, which the proxy's always is and a visitor reaching a public port directly is not. It
acts only while `PHP_FORWARDED_TRUST=private`, which the runtime withdraws like every recipe's proxy trust
(see [Where the server listens](#where-the-server-listens-and-whom-it-trusts)). Laravel's `@vite` and
`url()`, Symfony's `isSecure()` and WordPress's `is_ssl()` then produce https without the application
trusting any proxy.
Laravel's `APP_URL`, Symfony's `DEFAULT_URI` and Bedrock's `WP_HOME`/`WP_SITEURL` follow the planned
domain; Laravel's `APP_KEY`, Symfony's `APP_SECRET` (64 hex characters) and Bedrock's keys and salts are
generated on the server at commit.

Composer runs from the `composer:2` image. The build is staged: `php-base` (the release, extensions,
php.ini and prepend), `vendor` (the source and `composer install --no-dev --optimize-autoloader`), an
optional `assets` stage, and the final stage from `php-base` with `vendor`'s `/app` copied in. Laravel's
`storage/`, `bootstrap/cache` and `database/` are created writable in `vendor`, before Composer's
`package:discover` and the asset build boot the application, and the public disk is linked the way `php
artisan storage:link` would (`public/storage` → `/app/storage/app/public`, unless the repository commits
one). Symfony builds and runs with `APP_ENV=prod APP_DEBUG=0` whatever the committed `.env` says — its
dev bundles are `require-dev`, which `--no-dev` leaves out — and a runtime `APP_ENV` from the plan still
wins; with `symfony/asset-mapper` (or `importmap.php`) the build runs `importmap:install` and
`asset-map:compile`, after `tailwind:build --minify` or `sass:build` for the SymfonyCasts bundles.

composer.lock is compared with composer.json as data. A `require` package the lock lacks (or that no
locked package provides or replaces) is `composer_lock_missing_packages`, and one whose locked version no
longer satisfies its constraint is `composer_lock_outdated` — both warnings, the Composer twin of the
npm incident: instead of stopping on "Required package … is not present in the lock file", the build
runs `composer update --no-dev … --with-all-dependencies <those packages>`, keeping every other locked
version. A `require-dev` mismatch is `composer_lock_dev_outdated`, which this build does not meet. No lock
at all is `dependencies_unpinned`, whose action says to commit composer.lock and whose means say that
Composer refuses to resolve a version with a security advisory. A Laravel application that registers a
`require-dev` package's provider for every environment (`bootstrap/providers.php`, or the `providers`
of `config/app.php` — never its `aliases`, facades resolved only when called — with an `App\Providers`
class followed through what it `extends`, resolved by its `use` imports, to the package's provider —
Telescope, IDE Helper, Debugbar, Dusk, …) is `laravel_dev_provider_registered` (blocked):
`package:discover` boots it and the build stops on "Class … not found". A registration guarded by
`environment(`/`isLocal(`/`class_exists(` is not counted, nor is what a provider's body mentions:
Telescope's local-only installation registers the package inside `AppServiceProvider::register`, which
extends Laravel's own provider. A Composer repository needs credentials by its host: a paid or private
store (Nova, Spark, Private Packagist, Magento's marketplace, Flux Pro, Spatie, ACF Pro, Anystack's
`*.composer.sh`, Repman) adds `COMPOSER_AUTH` as an install-scoped variable that `registry_token_missing`
blocks on while it is missing; a public one (Packagist and its mirrors, WPackagist, `packages.drupal.org`,
Asset Packagist, Firegento) — the Bedrock, Drupal recommended-project and Yii templates list them — or a
URL that carries its own user and password adds nothing; any other host, and a VCS repository, adds the
same variable with a warning, since it may be public. A committed `auth.json` supplies them all. A VCS
repository off GitHub, or a locked package without an archive, installs `git` first.
FrankenPHP does not read `.htaccess`, so one with access or rewrite rules is a `php_htaccess_ignored`
warning, and a plain PHP application served from the repository root (`--root /app`) is a
`php_docroot_is_repository_root` warning: dependencies, lockfiles and logs under it are reachable
(WordPress, whose root is its own design, is exempt).

A `package.json` whose build the recipe recognises gets the `assets` stage: Vite (with or without
`laravel-vite-plugin`), Encore, Laravel Mix, or `@wordpress/scripts` (`build/`, or its `--output-path`).
That stage starts from `vendor`, so the build finds PHP —
Wayfinder's Vite plugin runs `php artisan wayfinder:generate` — and `vendor/`, which the official
starter kits import from (Flux's CSS, Ziggy); Node, npm and Corepack, with the manager releases the
toolchain stage pinned (and Bun beside them), are copied from the reviewed Node image into `/opt/node`.
When `vite.config` imports a `require-dev` package from `vendor/`, the stage installs Composer's
development packages first; none of them reaches the final image. The stage installs through the same
plan as the Node recipe ([JavaScript installs](#javascript-installs), toolchain stage
`assets-toolchain`), the same `packageManager` build setting applies to a `php` recipe, and its lockfile
findings — competing lockfiles, none (`assets_dependencies_unpinned`), a stale one — reach preflight
before Deploy. Exactly the directory the build writes is copied into the final stage: `public/build`
for `laravel-vite-plugin` (its `publicDirectory`/`buildDirectory` when set) and Encore (its
`setOutputPath`), Vite's `build.outDir` (`dist` by default) otherwise, and `public/` for Mix, which
writes `public/js`, `public/css` and `mix-manifest.json`. Output outside the served directory is
`php_assets_outside_docroot`. Mix builds with its `production` (or `prod`) script unless
`public/mix-manifest.json` is committed; without either, `mix()` fails every page and preflight says so
(`laravel_mix_unbuilt`). Any PHP application with such a build — not only a known framework — owns its
`package.json`, which is no longer offered as a static site of its own.

WordPress is recognised in every shape a repository holds it. **Core** (`wp-settings.php` and
`wp-includes/version.php`, whose `$wp_version` is read as data) is served from its own root. A
**wp-content** tree (`themes/`, `plugins/` or `mu-plugins/` in a root no Composer framework owns, with
WordPress's own evidence: an `index.php` that is "Silence is golden", or, with no `index.php`, a
`themes/*/style.css` or `plugins/*` file carrying a theme's or plugin's header — a Laravel themer's
`themes/` or a Slim application's `plugins/` is the application's own), a **theme** (`Theme Name:` in the
first 8 KiB of `style.css`) and a **plugin** (`Plugin Name:` in a root PHP file's header) are copied into
the WordPress release of the reviewed `wordpress:6-php8.4-fpm-alpine` image — only its files are taken —
at `wp-content/`, `wp-content/themes/<text domain>` or `wp-content/plugins/<text domain>`, to be activated
in wp-admin. The walk reads those headers itself (at most 32 files: the top of the checkout's
`style.css` and PHP files, and `themes/*/style.css`), so a block theme (`theme.json`, `templates/`, no
`index.php`) and a `@wordpress/create-block` plugin are WordPress roots too. A theme's or plugin's
`package.json` is its asset build, never a Node service: a `wp-scripts build` (or a Vite build) runs in the
`assets` stage from the directory the theme or plugin sits in inside WordPress, and exactly its output is
copied, so the blocks a create-block plugin registers from `build/` exist. When no `wp-config.php` is
committed the recipe writes one, constant text again, that reads the database from the `DATABASE_URL` a
linked MySQL or MariaDB supplies (or the official image's `WORDPRESS_DB_*` names) and keys and salts from
`WORDPRESS_*` when set, else WordPress generates and stores its own. **Bedrock** (`roots/wordpress`) is
served from `web/` (the parent of its `wordpress-install-dir`) and reads its own configuration. Every shape
installs WordPress's extensions, suggests MySQL (`wordpress_database_required`, a decision, while no
database is linked or configured) and keeps `wp-content/uploads` (Bedrock's `web/app/uploads`) as
state.

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
   Solid/TanStack Start, Qwik City, Analog, Vike, Waku, RedwoodJS, Streamlit, Gradio, Flask routing `/`,
   Laravel, Fresh, plain PHP) is asked for a
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
that applies migrations (a detected schema step counts only when the start runs it, not beside a
Procfile's web process that leaves it to a release task); 60 attempts 5 s apart for Wagtail's first migrations; 60 attempts 10 s apart
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
high candidates in different roots are told apart by where they sit, and only a true tie asks the
operator). Planned as web anyway, such a package gets
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
`runtime_start_exited`.

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
by its user — fails its readiness gate with `runtime_sqlite_not_writable` named.

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

## Failure diagnosis

A failed build is named from its own output, never by running anything more (see
[implementation](implementation.md) for the collector and the evidence). The phase comes from which
rendered instruction failed: a recipe's dependency install (`npm ci`, `bun install`, `pip install`,
`uv sync`, `poetry install`, `go mod download`, `cargo fetch`, `dotnet restore`, `composer install`,
`deno install`, an `apk add`) is **install**; the plan's build command, or the recipe's default build
when it has none, is **build**; the `test -f … || (echo '… must produce …' >&2; exit 1)` guard and a
`COPY --from=build` of an output directory are **output_check**; any other generated line is
**setup**, and every step of a custom Dockerfile or Compose build is **dockerfile**. A recipe step the
JavaScript recipe prefixes with the defaults it exports (`export NAME="${NAME:-…}" && …`: Prisma's
placeholders, `SKIP_ENV_VALIDATION`, the legacy OpenSSL provider) is read, and named, by the command
after them.

The signature table (`build_output_signatures.go`) is ordered most specific first; the failed step's
own lines are read before the stream's, which keeps only the steps that have not finished and
BuildKit's closing replay — a step that passed printed nothing that explains a later failure. What it
names, with the remedy the evidence supports:

| Code | Recognised from | Fix computed |
|------|-----------------|--------------|
| `build_lockfile_out_of_sync` | npm `EUSAGE … are in sync` (subjects from `Missing:`/`Invalid:`), Bun `lockfile had changes, but lockfile is frozen`, `ERR_PNPM_OUTDATED_LOCKFILE`, Yarn `YN0028` and Yarn 1 `--frozen-lockfile`, Poetry `changed significantly`, uv `--locked`, Cargo `--locked was passed`, Go `missing go.sum entry` / `updates to go.mod needed`, Composer lock errors, Deno `The lockfile is out of date`, Bundler deployment mode | the package manager whose lockfile the detected candidate reads as in sync |
| `build_lockfile_incompatible` | pnpm `ERR_PNPM_LOCKFILE_BREAKING_CHANGE`/`BROKEN_LOCKFILE`, Cargo lock version, Poetry/uv lock format, Bun lockfile version, Deno `Unsupported lockfile version` | — |
| `build_package_manager_mismatch` | corepack `This project is configured to use X`, `ERR_PNPM_BAD_PM_VERSION` | the manager `packageManager` declares |
| `build_lifecycle_script_blocked` | `ERR_PNPM_IGNORED_BUILDS`, Bun `Blocked N postinstalls` with a consequence | — |
| `build_runtime_version` | EBADENGINE, `ERR_PNPM_UNSUPPORTED_ENGINE`, Yarn/Next engine lines, Go `GOTOOLCHAIN=local`, rustc `or newer`, Maven release, Gradle class version, NETSDK1045, Composer `requires php`, pip `requires a different Python`, uv/Poetry Python requirement, Ruby/Elixir/Hugo versions | a Go, Python or PHP release the recipe offers |
| `build_env_missing` | PrismaConfigEnvError, P1012, t3-env, SvelteKit `$env/static`, Astro, Rails `secret_key_base`, Phoenix, Django, `KeyError` on the environment | the variable, or its build scope (recipes only) |
| `build_sqlx_offline` | sqlx `set DATABASE_URL to use query macros` / no cached data | `SQLX_OFFLINE=true` for the build |
| `build_database_unreachable`, `build_prerender_failed` | Next prerender/collect-page-data errors, with or without a database error; `Can't reach database server`; a `*.jd.internal` address that does not resolve (a linked database is reachable only on the project network, which a build is not on); Django `OperationalError` | — |
| `build_prisma_client_missing` | `@prisma/client did not initialize yet` | `prisma generate` before the build command, unless the recipe already runs it (the `prisma` CLI is a dependency); otherwise the sentence says to add the CLI |
| `build_platform_binary_missing` | rollup/esbuild/SWC/lightningcss/oxide/sharp Linux binaries missing | — |
| `build_legacy_openssl` | `0308010C`, `ERR_OSSL_EVP_UNSUPPORTED` | `NODE_OPTIONS=--openssl-legacy-provider` |
| `build_system_library_missing`, `build_native_toolchain_missing` | `pg_config`, `mysql_config`, pkg-config, `cannot find -l`, `*-sys` crates, headers, Prisma libssl, glibc on musl; `gyp ERR!`, a missing compiler (a shell's `make: not found` only with exit 127 or a wrapper reporting 127), `Failed building wheel`, cgo, `linking with cc`, `protoc`, perl, NativeAOT's clang | — |
| `build_install_script_failed` | npm `error path /app/node_modules/X` with `command failed`, Yarn `YN0009` | — |
| `build_php_extension_missing` | `requires ext-X … it is missing from your system` | — |
| `build_dependency_conflict`, `build_dependency_unavailable`, `build_dependency_local_path`, `build_dependency_advisory_blocked` | ERESOLVE, `ResolutionImpossible`, Composer/uv/Cargo/NuGet conflicts; ETARGET/E404, `No matching distribution`, NU1101, Maven artifacts, Go revisions, gems; conda `/croot/` paths; Composer advisories | — |
| `build_registry_auth`, `build_registry_rate_limited`, `build_network` | E401/E403, `YN0041`, `terminal prompts disabled`, npm's `Failed to replace env in config: ${X}` (naming the variable `.npmrc` authenticates with), Composer's `URL required authentication` (whose remedy is `COMPOSER_AUTH` for the install); `toomanyrequests`; DNS, TLS and connection failures | — |
| `build_next_image_export` | `Image Optimization using the default loader is not compatible with` a static export | — |
| `build_command_not_found`, `build_script_missing` | `sh: X: not found` when the step exited 127 or a wrapper reports that status (`exit code 127`, `exited (127)`) — a caught probe prints the same line and carries on —, `executable file not found`, pip's `Cannot find command 'git'`, Composer's `git was not found in your PATH`, Laravel Wayfinder's `php artisan wayfinder:generate` in an asset stage without PHP, an mdBook preprocessor or renderer that is not installed; npm/pnpm/Bun/Yarn missing script | the build command with its runner moved to the image's package manager (the install planner's own rewrite, `nodeRunnerFor`), read from the install the build recorded |
| `build_module_not_found`, `build_type_error`, `build_compile_error` | `Cannot find module`, `Can't resolve`, `No module named`, `no required module provides`, a Sphinx extension that does not import, an MkDocs plugin that is not installed; `Type error:`, `error TS…`; rustc, C#, javac/Kotlin, Go, Maven, Gradle, bundler and framework compile errors | — |
| `build_theme_missing`, `build_site_render_failed` | a Hugo theme `module … not found in …/themes/`, Zola `Failed to load theme`, a Jekyll theme gem, MkDocs `Unrecognised theme name`, Sphinx `no theme named`; Hugo `error building site` and `execute of template failed`, Jekyll `Liquid Exception … in <file>`, Zola `Failed to build the site`, Eleventy `Problem writing Eleventy templates`, Hexo `Template render error`, MkDocs strict mode, Sphinx `-W` | — (a Hugo or Zola theme names the Source section's submodules switch) |
| `build_output_missing`, `build_copy_source_missing`, `build_embed_source_missing`, `build_wrong_root` | the recipe's own guard, a missing `COPY` source, `go:embed` without files, a manifest the build cannot find | the detected output directory, else the field to review |
| `build_out_of_memory`, `build_disk_full`, `build_timeout` | heap limits, exit 137 (which points at no line: the step's output only shows where it was), `OutOfMemoryError`; `no space left on device`; the 30-minute limit | `NODE_OPTIONS=--max-old-space-size=` three quarters of the server's memory (from 1 GiB, at most 8 GiB) |
| `build_permission`, `build_script_crlf`, `build_wrapper_missing`, `build_dev_dependency_in_production`, `build_bundle_platform_missing`, `build_hugo_extended_required`, `build_base_image_missing`, `build_platform_unsupported`, `build_dockerfile_invalid` | exit 126, `\r` interpreters, the Gradle/Maven wrapper, Symfony dev bundles and Telescope, any provider class `package:discover` cannot load, a Gemfile.lock without Linux, Hugo Pipes' Sass, a FROM that does not resolve, a manifest for another platform, a Dockerfile that does not parse | — |

Nothing matched is `build_failed`, still with the phase, command and exit code. A Dockerfile build
never gets a variable fix, because a custom Dockerfile cannot take build secrets.

## Verification

`TestLiveDetectedFrameworkBuildAndServing` builds locked Next.js (Bun, and pnpm started through `pnpm run
start`), an Express server installed and started by Yarn 1, Vite, SvelteKit Node/static, a Next.js static
export under a `basePath` (a second page served from its `.html` file), a Next.js standalone server
started from its `server.js` (serving `public/` and its static assets), a SvelteKit app on adapter-auto
built with adapter-node, Express serving the Vite client its build writes (the Replit shape), a Hono
starter with only a `bun --hot` dev script, on Bun, React Router 8 in SPA mode with a prerendered home
(another path answered by its `__spa-fallback.html` shell, not the home page), plain HTML,
Containerfile and Go fixtures through detection and the real artifact/runtime owners, and the catalogue's
own starters: Astro 7 (static), Nuxt 4 and React Router 8 (servers), FastAPI on an unpinned
`requirements.txt` with no server declared, a Flask factory on a bare `pyproject.toml`, a Django project
whose first request reads its migrated table, a Streamlit script (its health endpoint and a file served
through its own static-serving setting) and a Gradio app (the value in the page's embedded config), an
axum service, a Maven jar and a Gradle jar, an ASP.NET Core minimal API, a Deno server, a minimal Laravel
12 application (migrated, with the form's generated `APP_KEY`) and a plain `index.php`, and the site
generators: Hugo (Sass through Hugo Pipes, the build value read with `getenv`), Zola, mdBook (two
renderers, the title from `MDBOOK_BOOK__TITLE`), Jekyll 4 from its `Gemfile.lock` (a plugin reading the
build value), MkDocs with Material, Lume and Eleventy with no build script and its output in its
configuration, each also fetched by a clean URL and answering a missing page with the site's own
`404.html`; the SvelteKit static fixture serves a prerendered `/about` as `about.html` and falls back to
its `200.html`. It checks readiness and served values without supplying build values at runtime; recipe fixtures also inspect logs,
metadata and saved image layers for private install credentials. The Go fixture proves generated code,
custom startup and the selected toolchain through its HTTP response. These local adapter journeys
complement the production-build browser gate; they do not constitute public provider/DNS/TLS or clean-VM
acceptance. The remaining catalogue entries are covered by rendered-Dockerfile and detection tests
(`frameworks_*_test.go`, `build_recipes_test.go`). Persistent state, schema tools, seeds and their
findings are table-tested per stack in `detect_state_test.go`, `detect_schema_test.go`,
`preflight_state_test.go` and `recipe_runtime_files_test.go`. Repository shape — ranking, decoys, static
roots, split repositories, shapes that are not services, ecosystems without a recipe, processes, other
platforms' files, submodules and LFS, case-mismatched imports and the preflight findings they raise — is
covered by `detect_*_test.go` and `preflight_repo_shape_test.go` against written fixtures. JavaScript installs are covered by
`build_node_lockfile_test.go` (each manager's lockfile shapes), `build_node_install_test.go` (resolution
and every rendered install), `preflight_node_test.go` and `build_node_incident_test.go`, which keeps the
incident that motivated them fixed: a Next.js 16 + Prisma 7 repository with an in-sync `bun.lock` beside a
`package-lock.json` fifteen dependencies behind now resolves to Bun with no decision, and a forced npm
raises `lockfile_out_of_sync` before Deploy and installs unfrozen instead of failing `npm ci`. The image
and the build around the install are covered by `build_node_runtime_test.go` (the Node and Bun release
from every declaration, and the system packages each dependency adds), the framework catalogue by
`frameworks_node_catalogue_test.go` (each framework's configuration, the long tail, development-server
start scripts), `frameworks_node_config_test.go` and `frameworks_node_scripts_test.go` (the readers and
their read budget), `frameworks_node_schema_test.go` (the migrations a framework owns as schema steps),
`build_node_frameworks_test.go` (what the recipe renders around each framework's build, and the runners
it refuses), `detect_node_monorepo_test.go` (every manager's workspace build, and Nx, its plans matched
to their application),
`preflight_node_frameworks_test.go` and `build_output_node_frameworks_test.go`, `build_node_prisma_test.go`
(`prisma generate` and the `env()` placeholders, the incident repository with Prisma 7's
`prisma.config.ts` included), `build_node_build_test.go` (legacy OpenSSL, T3 Env, `--env-file`)
and `preflight_build_test.go`; the rendered Dockerfiles for canvas on Alpine, a GitHub dependency,
onnxruntime-node on Debian slim, Puppeteer with Alpine's Chromium, Prisma 7 on npm and on Bun with Node
24, and a webpack-4-era build were built and run locally when they were written. Static sites are covered
by `build_static_serving_test.go` (the nginx configuration, hosting rules and the shell that writes
them), `build_node_static_serving_test.go` (a framework's static output from detection through the
recipe to the static server: a Next.js export under its `basePath` with host rules, SvelteKit's
adapter-static fallback under its base, React Router's SPA shell, Eleventy and Hexo built by their own
binaries), `detect_site_generators_test.go`, `detect_static_site_test.go`, `build_site_test.go`,
`preflight_static_site_test.go` and `build_output_cause_site_test.go`; beyond the live fixtures, Hugo
Modules and Hugo with PostCSS, Zola 0.19 on Debian, mdBook 0.4, Jekyll on the github-pages gem with no
Gemfile and with a macOS-only lock, Sphinx with autodoc, Pelican, Zensical, Hexo, VuePress 2 under a
base path, Slidev, a Vite site with `_redirects`, `_headers` and a Vercel function, and a multi-page HTML
site were built and served locally when the recipes were written.
