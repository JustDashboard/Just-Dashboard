# Automatic recipes and serving defaults

`just-dashboard-recipes-v4` prepares immutable Dockerfiles using digest-pinned catalogue bases. Build
commands execute inside the build container; source inspection never executes repository configuration
on the host. Generated Dockerfiles use root-relative, exclusive writes so a checkout symlink cannot
redirect output outside the build context. Detection ignores this generated directory.

Detection reads manifests as data — `package.json`, `angular.json`, requirement files, `pyproject.toml`,
`uv.lock`, `poetry.lock`, `pdm.lock`, `Pipfile(.lock)`, `setup.cfg`, `environment.yml`, `go.mod`, `go.sum`,
`go.work`, `Cargo.toml`, `Cargo.lock`, `.cargo/config.toml`, `pom.xml` (and the in-repository parents and
reactor it belongs to), `build.gradle(.kts)`, `settings.gradle(.kts)` and `gradle/libs.versions.toml`,
`*.csproj`, `*.fsproj`, `*.vbproj` with the `Directory.Build.props`, `Directory.Packages.props`,
`NuGet.config` and `global.json` that apply to them, `deno.json(c)`, a `Procfile`, the JDK and .NET SDK
version files (`.java-version`, `.sdkmanrc`, `.tool-versions`, `mise.toml`, `system.properties`), and for
a JavaScript package its lockfiles, `.npmrc`, `.yarnrc.yml`, `bunfig.toml`,
`pnpm-workspace.yaml` and the Node and Bun version files (`.nvmrc`, `.node-version`, `.tool-versions`,
`.bun-version`), `Gemfile`/`Gemfile.lock`/`.ruby-version`, `mix.exs`/`mix.lock`, `build.sbt` and
`project/*.sbt`, `project.clj`, `deps.edn`/`build.clj`, `pubspec.yaml`/`pubspec.lock` and
`gleam.toml`/`manifest.toml` — and names a candidate per root with the framework, the build and start commands, the port,
static output, the interpreter or toolchain release, and the environment variables and databases the
source reads. Every default is a plan field the configure form and the Build settings can change. A
detected framework that the recipe cannot serve automatically (a provider adapter, a workspace without a
root package, an unsupported interpreter release) is a low-confidence candidate with a decision or a
`recipe_unsupported` preflight finding, never a silent guess. Which of a repository's roots is the
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
`go_version_unsupported`, `go_main_*`, `go_cgo_library_unknown`, `go_embed_missing`,
`go_local_replace_outside_root`, `rust_binary_*`, `python_version_unsupported`, `java_version_unsupported`,
`gradle_wrapper_incompatible`, `dotnet_version_unsupported`, `start_command_missing`) is not reported
twice, and neither is a candidate's `recipeIssue` without a tree. A JDK or .NET release the recipe cannot
build with is a setting, not something wrong with the source, so detection never freezes it into
`recipeIssue` (the dry run's `toolchainVersionError`); preflight judges the plan's own `build.javaVersion`
or `build.dotnetVersion` against the facts detection kept. Without a tree — an image, a pasted Compose file, a commit that could not be fetched —
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
[JavaScript installs](#javascript-installs)); so is a credential a Maven `settings.xml` (`${env.X}`), a
Gradle repository's `credentials { … System.getenv("X") }` or `credentials(PasswordCredentials::class)`,
a `NuGet.config`'s `packageSourceCredentials` (`%X%`) or a `.cargo/config.toml` registry
(`CARGO_REGISTRIES_<NAME>_TOKEN`) names (see [Java and Kotlin](#java-and-kotlin), [.NET](#net) and
[Rust](#rust)). Maven and Gradle resolve dependencies as the build runs rather than in a step of their own,
so a variable mapped to install is mounted on their build step; .NET restores in its own step. Nothing maps a database URL to the install on its own:
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
  `SESSION_SECRET`/`COOKIE_SECRET`/`JWT_SECRET` beside the session or JWT library that signs with them
  (Sinatra's `SESSION_SECRET` as 128 hex characters, the 64 bytes Rack's cookie session wants), Play's
  `APPLICATION_SECRET` (64 hex characters) whether or not the code names it,
  and a name another platform's file generates (render.yaml `generateValue`, app.json
  `generator: "secret"`) as 64 hex characters.
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
  `RAILS_LOG_TO_STDOUT=1` where Rails 7.0 and earlier's `production.rb` otherwise logs to a file nothing
  reads, so a failed request leaves nothing in the container's output;
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
`Cargo.toml` (sqlx, diesel and sea-orm features included), Maven, Gradle, sbt, Leiningen and `deps.edn` coordinates, `*.csproj`
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
(`database_cpu_unsupported`); a Laravel, Symfony, Rails, Hanami or Phoenix start that migrates a
database nobody linked (`database_required_for_start`, blocked; read with the same pattern as the
readiness budget's migrating starts, Ecto's inline migrator included); the callback, authorized-domain
and webhook addresses to register with Auth.js providers, Better Auth, OmniAuth, django-allauth,
Passport (named by the strategy constructed around its `callbackURL`), Auth0, Clerk, Firebase, Supabase
and Stripe (`external_callback_registration`); a Clerk production key issued for another host
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

- **The start command**, following package scripts and `bundle exec`: `-p`/`--port N` (`-p` for the
  servers whose flag it is — Next.js, Vite, Rails, Puma, rackup, Unicorn and the like), `--port=N`, a `PORT=N` prefix,
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
candidate listening only on loopback gets the cause `runtime_loopback_bind` with the listener as its
subject, named in the failure message even when the application printed nothing. A server whose startup
line names the port it took — Puma, Bandit and Cowboy under Phoenix or Plug, Play's `Listening for HTTP
on`, Jetty's connector under Ring, and the Node, Python, Go and JVM servers before them — and names
another than the plan's gets `runtime_port_mismatch` with that port as the fix.

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
`serverless_functions_dropped`. The static server's single-page fallback is unchanged, so those routes
answer with `index.html`.

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
| redirect rules other than the SPA rewrite | `static_redirects_unsupported` (warning) |

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
which preflight's `recipe_unsupported` shows instead of "No deployable plan was detected": Jekyll and
Middleman; Crystal, Haskell, Zig, Swift, F#, OCaml, Nim, Perl, Erlang, R Shiny and Plumber, C/C++
(CMake, Meson), Elm, Hugo, MkDocs and a Flutter web app. The same pass recognises Ruby, Elixir, Scala,
Clojure, Dart and Gleam, which have recipes of their own ([Ruby](#ruby), [Elixir](#elixir),
[Scala and Clojure](#scala-and-clojure), [Dart and Gleam](#dart-and-gleam)): their candidate is built
from the recipe's own reading of the root (`detect_languages.go`), beside a Dockerfile when the root has
one. A web framework among the manifest's dependencies makes it a web service on its conventional port.
A Rails, Hanami or Phoenix application owns its `package.json` (and `assets/package.json`): that asset
pipeline is set aside rather than offered as a Node service, and the Ruby or Elixir recipe installs and
builds it — a CocoaPods or fastlane Gemfile beside a React Native app owns nothing.
A site generator (Hugo, MkDocs, Jekyll, Middleman, Elm) owns a tooling-only `package.json` at its root
the same way: a Hugo site with a Tailwind build used to be only a Node worker asking for a start command.
With a Dockerfile at the same root the Dockerfile is the candidate — beside the recipe candidate for
a language that has one — and it takes the framework, the processes and the database drivers. Every
other language is named only when nothing else at its root is a candidate, and C/C++ and MkDocs never
inside another candidate's root, where they are that application's vendored code or manual. Each entry
is removed when its recipe lands.

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
source. Evidence recorded before this (no `submodulesChecked`/`lfsChecked`) keeps the old decisions.

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

Every command the table proposes uses the resolved manager's runner (`bun`/`npm`/`pnpm`/`yarn run`,
`bunx`/`npx`/`pnpm exec`/`yarn` for a binary), and detection records the commands it would propose for
each of the four managers, so choosing another manager swaps whole commands.

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
framework default alike. Prisma (`*.prisma` at the root or under `prisma/`, or where its configuration
declares its schema) runs `prisma migrate deploy` when a `migration.sql` is committed and `prisma db push`
otherwise; Drizzle (`drizzle.config.*`) runs `drizzle-kit migrate` with a
`_journal.json` and `drizzle-kit push` otherwise; Knex (`knexfile.*`) runs `knex migrate:latest`;
Sequelize CLI runs `sequelize-cli db:migrate`; MikroORM migrations run `mikro-orm migration:up`. TypeORM
is recognised but needs an operator's command. A start script that already runs the tool is left as it
is, and static output never gains a start command. Prisma's push refuses destructive changes without an
explicit flag and Drizzle's stops to ask a question nobody can answer, so a schema that would lose data
fails the start instead of dropping it. That is why a push is never a pass: preflight raises
`schema_push_unversioned` (a warning in place of `schema_step`, linked database or not) whenever the start
command, a release task or the package's own start script pushes, with the command that commits
migrations instead (`prisma migrate dev`, `drizzle-kit generate`). When a push does refuse, the failed
readiness gate names it — `runtime_schema_push_refused`, from Prisma's `--accept-data-loss` message or Drizzle's
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
that the rule is set aside. Names only one recipe reads (Gradle's `gradle.properties` and build logic, the
MSBuild and NuGet files and `global.json`, the JDK and SDK version files such as `.tool-versions`, sbt's
`project/`, a Gemfile or `manifest.toml`) are set aside for that recipe's builds alone, so a static site
or a Node image keeps the repository's rule instead of copying or serving the file. A JavaScript install
input below the root is brought back by an exception
instead ([JavaScript installs](#javascript-installs)), and the run log names each rule set aside or
overridden. Then it excludes `**/node_modules`, `.dockerignore` and the dashboard's own files. The static, PHP, Node, Deno and Ruby images, which are served or copied whole, also exclude `.git`;
recipes whose toolchains stamp or version builds from Git (Go, Python's setuptools-scm, Maven's
git-commit-id, SourceLink) keep it. A static site also excludes `.env` and `.env.*`. Committed `.env`
files are otherwise left in: Next.js and Vite read public build values from them.

The context is the build root unless the build reads above it: a JavaScript workspace member installs
from its workspace root ([JavaScript installs](#javascript-installs)), a Go module that a `go.work` above it
uses, or whose local `replace` targets sit beside it, builds from the directory that holds them all, a
Cargo workspace member builds from its workspace root ([Go](#go), [Rust](#rust)), a Maven module from its
reactor, a Gradle project from its settings root, and a .NET project from the directory that holds the
projects it references and the MSBuild, NuGet and SDK files that apply to it. Preparation names the wider
directory (`prepared.contextDirectory`), the build uses it, the generated Dockerfile and its ignore file
are written there, and the member's own build file is kept in the context even when an ignore rule would
drop it.

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
is how npm and Yarn 1 workspaces refer to one (`"@acme/shared": "*"`). The member's directory is written
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
base). The first declaration that can be read decides: the nearest version file, looking from the
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

## Python

A Python root is a directory with a dependency manifest: `requirements.txt` or a requirement file
beside it (`requirements-prod.txt`, a `requirements/` folder), `pyproject.toml`, `Pipfile`, a uv,
Poetry or PDM lock, `setup.cfg` or a literal `install_requires=[…]` in `setup.py`, or a conda
`environment.yml`. The last two count only when they install something that serves (a framework or
`gunicorn`/`uvicorn`), since vendored libraries and documentation environments carry them too; a
`requirements-dev.txt` alone never does. Every manifest is read as bounded data
(`detect_python_manifests.go`, `detect_python_lock.go`, `detect_python_project.go`): `setup.py` is
searched for a literal list and never executed, a requirement file's `-r`/`-c` includes are followed
inside the root (three deep, sixteen files), UTF-16 files written by PowerShell's `pip freeze >` are
decoded, pyproject arrays are read quote-aware (`celery[redis]` no longer ends the dependency list), and a
lock too large for the walk's budget is streamed once and kept without other platforms' wheels and
hashes. A `manage.py` directory that `django-admin startproject` made inside the repository is folded into
the root above it rather than becoming a root of its own.

**What is installed, and how.** The recipe installs from the manifest that pins most:

| Manifest | Install |
| --- | --- |
| `uv.lock` | `pip install uv==<pin> && uv sync --locked --no-dev --python /usr/local/bin/python` into `/app/.venv` (first on `PATH`, `VIRTUAL_ENV` set, `UV_PYTHON_DOWNLOADS=never`), so the interpreter is always the digest-pinned image's |
| `poetry.lock` or `[tool.poetry]` | `pip install poetry==<pin> && poetry install --only main --no-root --no-interaction` into the interpreter (`POETRY_VIRTUALENVS_CREATE=false`) |
| `pdm.lock` | `pip install pdm==<pin> && pdm export --prod` to a hashed requirement file, installed with pip |
| `Pipfile.lock` | `pip install pipenv==<pin> && pipenv install --system --deploy`; `--ignore-pipfile` when the build's Python is not the Pipfile's `python_version` (`--deploy` would refuse it); a `Pipfile` without a lock installs `--skip-lock` |
| requirement file | `pip install --requirement <file>`: `requirements.txt`, else `requirements/production.txt`, `requirements/prod.txt`, `requirements-prod.txt`, `requirements/base.txt`, … or the only file that is not for development |
| bare `pyproject.toml` | `tomllib` reads `[project].dependencies` into a requirement file (3.10 brings `tomli`) rather than `pip install .`, which needs a build backend an application never set up |
| `setup.py`/`setup.cfg` | `pip install .` |
| `environment.yml` | the conda packages a reviewed table maps to PyPI (`pandas=2.1` read as `pandas==2.1.*`) and its `pip:` list as conda hands it to pip — markers, VCS references, index options and `-r` includes — written into `.jd-conda-requirements.txt` beside it, so includes resolve as they do under conda; a requirement file the list includes is the environment's, not an install of its own, unless the environment needs a conda-only package, when that file is installed instead. A conda-only package, a line the Dockerfile cannot carry inside single quotes (a quote outside a marker, a backslash, a literal credential, non-ASCII), or an include that leaves the root or is missing is `recipe_unsupported` naming it, never dropped |

The tools are pinned (`build_python.go`), so two builds of one commit run the same resolver.
Unpinned requirements are accepted — refusing them turned the most ordinary Python repository there is
into a manual Dockerfile — and preflight raises `dependencies_unpinned` as a warning. A process manager
the start command runs that no manifest declares is installed at the pinned release into the same
environment: `gunicorn`, and `uvicorn[standard]`, whose websocket library FastAPI's websocket routes need
(`python_websocket_library_missing` warns when a declared bare `uvicorn` would refuse them).

A lock is compared with the manifest it was generated from, the Python shape of the npm incident: uv's
root package `requires-dist` (names and specifiers) and dependency groups against pyproject, Poetry's and
PDM's package names against the declared dependencies, `Pipfile.lock`'s `default` against the Pipfile.
A stale lock is resolved again inside the build (`uv sync --no-dev`, `poetry lock &&`, `pdm lock
--update-reuse &&`, `pipenv install --skip-lock`) with the reason in the run log, and preflight warns
`python_lock_stale` (missing and changed names; run `uv lock` and commit). A lock detection judges in
sync installs `--locked`, so drift it could not see fails loudly and is classified. A requirement file
that omits dependencies a PEP 621 pyproject declares is the older description: the build installs from
pyproject and `python_manifests_disagree` says so. A pyproject that only configures tools installs
nothing, and `python_dependencies_empty` warns — blocked when the code imports a framework or has a
`manage.py`.

`pip freeze` leftovers are rewritten inside the image before pip reads them, by generic `sed`
expressions that name no line of the file. A conda package build's `name @ file:///croot/…` (Anaconda's
`/croot/` and `/tmp/build/`, conda-build's `/opt/conda/conda-bld/`, conda-forge's builders) becomes
`name`: conda repackaged that PyPI distribution. Windows or macOS packages (`pywin32`, `pywinpty`,
`pyobjc-*`) are commented out with their `--hash` continuation lines (`python_requirements_local_artifacts`).
Any other `name @ file:` — a developer's own package — and a path outside the checkout (`-e C:\…`) are
commented out too, never installed by name: an index could publish an unrelated or hostile package under
it (`python_requirements_local_paths` names them; the application stops with `ModuleNotFoundError` if it
imports one). A `file:` URL pip expands from a variable (PDM's `${PROJECT_ROOT}`) is left alone. A
requirement file the install names is written unquoted into the install command, so one whose name a
shell would split is `recipe_unsupported`. A pip install that pulls
PyTorch in (`torch`, `sentence-transformers`, `ultralytics`, `openai-whisper`, …) with no index of its own
adds PyTorch's CPU index to `PIP_EXTRA_INDEX_URL` for that one command: a deployment container is never
given a GPU, and PyPI's Linux x86_64 torch brings gigabytes of CUDA libraries (`python_cpu_torch_selected`;
a lock that pins them is `python_gpu_wheels`).

A private index becomes install-scoped variables with the names each tool reads — `PIP_EXTRA_INDEX_URL`
or `PIP_INDEX_URL` for a requirement file's index, `POETRY_HTTP_BASIC_<SOURCE>_USERNAME/PASSWORD`,
`UV_INDEX_<NAME>_USERNAME/PASSWORD`, and every `${NAME}` a requirement file or Pipfile source expands
(required, since the file names it) — with `python_private_index` warning. A credential committed in an
index or dependency URL is `credential_in_manifest`; a `git+ssh` dependency is the decision
`python_private_git_dependency`, since the build has no SSH key.

A uv workspace member (a directory a parent pyproject's `[tool.uv.workspace]` members cover, with the
workspace's `uv.lock`) installs from the workspace root, which is ranked below its members when it
declares no dependencies of its own (uv's own root has a `[project]` table with an empty list): the build
context widens to it
(`ContextDirectory`), `uv sync --locked --no-dev --package <member>` installs the member with its sibling
packages, and the server runs from the member's directory. Both are written unquoted into the
Dockerfile, so a member whose directory is not letters, digits and `. _ @ + - /`, or whose name is not a
normalized distribution name, is `recipe_unsupported`. A `[tool.uv.sources]` workspace or path
dependency built without its workspace is `recipe_unsupported`: pip would fetch an unrelated package of
the same name from PyPI.

**System packages.** The image is `python:<family>-slim-trixie`, named by its Debian release so the
package names cannot drift, and a dependency that builds or loads against a system library gets it from
Debian in one layer before the source is copied: `psycopg2` (gcc, libc6-dev, libpq-dev), `psycopg`
without its binary or C extra (libpq5), `mysqlclient`, `mariadb`, `python-ldap`, `uwsgi`, `pycairo`,
`GDAL`, `opencv-python` (libgl1, libglib2.0-0t64), `weasyprint` (Pango), `python-magic`, `pdf2image`
(poppler), `pytesseract`, `pydub`/`moviepy`/`openai-whisper` (ffmpeg), `pyodbc`, `pyzbar`, `pyvips`,
GeoDjango (`django.contrib.gis`: gdal-bin), and `git` for a VCS requirement. The recipe installs these
itself on every build (`candidate.systemPackages`, `automatic`); `build.systemPackages` adds others, at
most 32 Debian names, and is seeded from another platform's `Aptfile` or `nixpacks.toml` under trixie's
names (`libgl1-mesa-glx` → `libgl1`), which answers `platform_system_packages_ignored`.
`python_system_packages` lists what the image installs and why. A build that still fails on a missing
library is classified with the packages to add (`pg_config` → gcc libc6-dev libpq-dev, `libGL.so.1` →
libgl1) as its fix.

**The interpreter.** The catalogue carries 3.10 to 3.14; 3.13 stays the default for a project that
declares nothing. The family comes from `build.pythonVersion`, then the first file that pins one —
`.python-version` (the root's, or the nearest directory above it in a monorepo), `runtime.txt`,
`.tool-versions`, `mise.toml`, the Pipfile's `python_version`, `environment.yml`'s `python=` — then the
range `requires-python` (either TOML quote style), Poetry's `python`, `uv.lock`'s `requires-python` or a
setup file's `python_requires` allows. A range picks the newest family at or below the default, and a
floor above it (`>=3.14`) picks the family it asks for; a range the catalogue cannot meet is
`recipe_unsupported` naming it, never the default. A patch pin (`3.13.1`) is served by the family's image,
said in the evidence. A declared 3.8 or 3.9 is raised to 3.10 (`python_version_raised`) when its pins
publish 3.10 wheels, and refused naming them otherwise.

Pins decide too: a 2024 `pip freeze` pins numpy 1.26 and pydantic-core 2.14, which publish no wheel for
3.13, and on an image with no compiler the build fails. A lock is read exactly — a locked package with
Linux wheels supports a family when one of them is tagged for it (`cp3N`, `abi3` from an earlier `cp3M`,
`py3-none` compiled code such as the NVIDIA libraries, or pure) for the build's architecture, and each
family is judged by the versions the lock installs on it: the lock is walked from the project's own
dependencies, following the extras each edge asks for, and an edge or a version whose environment marker
rules out a Linux build of that family (`python_full_version < '3.12'`, uv's `resolution-markers`,
Poetry's and PDM's per-package markers, `sys_platform == 'win32'`) is skipped; what a marker asks that the
build does not settle, such as an extra, counts as installed (`detect_python_markers.go`). A requirement
file's `==` pins and upper bounds, under their own markers, are checked against a reviewed table of the
first release with Linux wheels for each family, for the compiled distributions freezes commonly pin
(`build_python_wheels.go`, generated from PyPI's JSON). A few releases
import a module a later Python removed (python-telegram-bot before 20, Django before 4.1, pydub without
`audioop-lts`). An undeclared version then stays below the family those pins cannot use, with the pins as
evidence (`python_version_limited`, and the run log's note) — the configure form seeds
`build.pythonVersion` from detection, so a setting equal to the detected family is still the pins' choice,
not an override; a chosen one they cannot use is `python_version_wheels_missing`
(naming a family that works) or `python_native_build_unmapped` when none does. A family outside the
declared range is `python_version_unsupported`: blocked on the uv and Poetry paths, which refuse it, a
warning on pip.

**Frameworks.** `deploy/frameworks_python.go` recognises a framework from what the project itself
declares — not from everything its lock installs, since Gradio locks FastAPI and Dash locks Flask — and
from the entry that builds its application: the conventional entry files the walk reads, the root's other
scripts (at most 32), and the module a `run.py`, `wsgi.py`, `main.py` or `app.py` imports its factory from.
Frameworks built on another come first, and one that only appears in the manifests yields to one whose
application an entry builds (a FastAPI app that mounts Gradio is FastAPI). aiohttp, Tornado and Starlette
count only with an entry that builds their application, since half the catalogue depends on them.

| Framework | Start | Port |
| --- | --- | --- |
| Django (`manage.py` at the root, or the only one two levels down) | `python [dir/]manage.py migrate --noinput && [collectstatic &&] gunicorn --bind 0.0.0.0:${PORT:-8000} --access-logfile - [--chdir dir] <project>.wsgi:application`; Channels (`ProtocolTypeRouter` in asgi.py) runs `daphne` when declared, else `uvicorn <project>.asgi:application` | 8000 |
| FastAPI, Litestar, Starlette | `uvicorn <module>:<object> --host 0.0.0.0 --port ${PORT:-8000}`; `--factory` for a factory with no required parameters | 8000 |
| Flask, Bottle, Falcon (WSGI) | `gunicorn --bind 0.0.0.0:${PORT:-8000} --access-logfile - <module>:<object>`, or `'<module>:create_app()'`; Flask-SocketIO runs one worker with threads (or eventlet/gevent-websocket) | 8000 |
| Falcon ASGI | uvicorn | 8000 |
| Sanic | `sanic <module>:<object> --host 0.0.0.0 --port ${PORT:-8000}` | 8000 |
| Quart | `hypercorn --bind 0.0.0.0:${PORT:-8000} '<module>:<object>'` | 8000 |
| aiohttp | `python <script>` for `web.run_app` (its `port=`), else gunicorn with `aiohttp.GunicornWebWorker` | 8080 |
| Tornado | `python <script>` | its `.listen(N)` |
| Streamlit | `streamlit run <script> --server.port ${PORT:-8501} --server.address 0.0.0.0 --server.headless true`, the script beside `pages/` for a multipage app | 8501 |
| Gradio | `python <script>` with `GRADIO_SERVER_NAME=0.0.0.0` (`gradio_bind_loopback` warns about a literal `server_name="127.0.0.1"`) | its `server_port`, else 7860 |
| Dash | `gunicorn … <module>:server` when the script exposes `server = app.server`, else `python <script>` with `HOST=0.0.0.0` | 8050 |
| Panel | `panel serve <script> --address 0.0.0.0 --port ${PORT:-5006}`, with `BOKEH_ALLOW_WS_ORIGIN` bound to the domain | 5006 |
| Chainlit | `chainlit run <script> --host 0.0.0.0 --port ${PORT:-8000} --headless` | 8000 |
| NiceGUI | `python <script>` | its `ui.run(port=)`, else 8080 |
| Mesop | `gunicorn --bind 0.0.0.0:${PORT:-8080} --access-logfile - <module>:me` | 8080 |
| Reflex | `recipe_unsupported`: it compiles a Node frontend and runs a separate backend | — |

An application object is found as applications keep it: `app = FastAPI()` or `fastapi.FastAPI()`, a
module-level `app = create_app()` whose factory builds or is annotated with the framework's type, a Flask
factory that takes a configuration used through the object a `wsgi.py` builds from it. A `src/` layout
imports by package name with `--app-dir src` (uvicorn) or `--pythonpath src` (gunicorn), and installs the
project itself (`pip install .`, Poetry without `--no-root`, `pip install --no-deps .` after a requirement
file or a PDM export) when it declares a build system — as it does when the start command runs one of
the project's console scripts. The entry files of the root's packages (`<pkg>/`, `src/<pkg>/`) are read
after the walk whatever their depth, since a uv workspace member made by `uv init --package` keeps its
application at `apps/api/src/api/main.py`; an
application folder whose modules import their siblings by bare name (`app/main.py` importing `routers`)
runs from inside it. A script or `manage.py` directory whose path a shell would split or expand (a space,
a quote, a `$`, a leading dash) is never written into a proposed start command. `start_module_unresolved`
warns when a start command's module is none of the root's importable names.

A start command the repository declares outranks every guess: a `Procfile` web process, then a task
runner's `start`, `serve`, `server`, `web` or `prod` task (`[tool.pdm.scripts]`, `[tool.poe.tasks]`,
`[tool.taskipy.tasks]`, Hatch's default scripts), with `pdm run`/`hatch run`/`poetry run`/`uv run`
stripped; a composite task is not a command, and `run` is not read. A task is as often what a developer
runs, so one that starts a development server (`runserver`, `flask run`, `fastapi dev`, `--reload`),
binds loopback where the recipe's environment does not move it (`hypercorn app:app`), or runs a script
where the framework has a production server (Flask's `app.run()` in `python app.py`) leaves the
framework's command in place; the Procfile, which a platform ran in production, is taken as written. A
declared start answers the framework's questions (confidence high), and keeps what the
platform it was written for would have done: Heroku's buildpack collects Django's static files at build
time, so a Django web process gets `collectstatic` before it when WhiteNoise has a `STATIC_ROOT`. Its
`release:` line is planned as described under [release commands](#release-commands).

Django's settings are read as text. The settings module is the one the WSGI (or ASGI) module names;
when that is a development module (`…dev`, `…local`) with a `production.py` beside it that sets a
`SECRET_KEY`, the production one. When `manage.py` names another (cookiecutter-django's `local`), the
chosen module is seeded as a plain `DJANGO_SETTINGS_MODULE` variable, so `migrate`, `collectstatic` and
the server load the same settings. A development module with no usable production sibling (Wagtail's
template, whose `production.py` sets no key) keeps running and `django_development_settings` warns.
`collectstatic` runs at container start, never at build (settings need runtime secrets), and only with
WhiteNoise and a `STATIC_ROOT`: WhiteNoise without one is `django_static_root_missing`, static files
without WhiteNoise `django_static_unserved`. With DEBUG off and no `LOGGING`, Django sends request errors
to the admins' email, not to the output the dashboard shows: `django_errors_unlogged` warns with a
console `LOGGING` snippet, the detected gunicorn commands write their access log (`--access-logfile -`),
and a readiness failure that answered a 5xx with no error in the output is named
`runtime_errors_hidden` (a 400 is Django's host allowlist, `runtime_host_disallowed`; a 401, 403 or 404
is an answer the readiness classification names).

The detected commands read `${PORT:-N}` rather than a fixed port, so changing the application port in
Build settings moves the server with it. A Django project answers only the hosts its settings allow; the
variable `ALLOWED_HOSTS` is read from is bound to the planned domain in the separator the settings split
it on, and `CSRF_TRUSTED_ORIGINS` to its origin (see [environment
discovery](#environment-discovery-and-database-suggestions)); a literal list is checked against the
planned domain by `readiness_host_allowlist`. `st.secrets` reads only `.streamlit/secrets.toml`, so a
Streamlit start command whose sources read `st.secrets["KEY"]` first writes that file from the variables
of the same names (each value a JSON string, which is a TOML string; values never enter the image), unless
the repository commits one; a table such as `st.secrets["connections"]` cannot be a variable
(`streamlit_secrets_nested`). A plain `main.py`/`app.py` is a low-confidence worker that asks whether it
serves. The recipe refuses a plan with no start command, naming the frameworks detection proposes one
for, and preflight says so first: `start_command_missing` (blocked, on the start command) for the Python,
Deno, PHP and Ruby recipes and a JavaScript server with no static output. The configure form's first step
refuses to go on without one for the same plans (`needsStartCommand`).

A Django or Flask application whose `package.json` builds its CSS or JavaScript (a `build` script with
Tailwind, Vite, webpack, esbuild, PostCSS or Sass, and no server of its own — a `start` script that only
watches, as django-tailwind's `"start": "npm run dev"` does, is not one) at the root or in
django-tailwind's `theme/static_src` gets a Node stage: the same install planner as the JavaScript recipe
installs and runs the build over a copy of the application, drops `node_modules`, and the Python image
copies the result in place of the source (`python_assets_built`). The package is not offered as a site
of its own, and `build.packageManager` chooses its manager among competing lockfiles.

gunicorn and uvicorn run one worker unless told otherwise, and both read `WEB_CONCURRENCY`. When the
start command leaves the count to it and the application loads no machine-learning model, the build
records that (`PreparedBuild.webConcurrency`) and the runtime sets it for the container
(`runtime_concurrency.go`): 2×CPU+1 within 256 MiB of the memory limit per worker, and 2 when the plan
sets no limit; an explicit variable always wins, and the start step's log states the value and how it
was sized. A websocket application keeps its connections, and usually the list it broadcasts to, in the
process, so a generated uvicorn command for one (a websocket route in an entry, Channels'
`ProtocolTypeRouter`) runs `--workers 1`.

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

The Go recipe builds with the families in `goRecipeFamilies` (`deploy/build_go.go`): Go 1.26 and 1.27,
which upstream supports, and 1.25, which it no longer does. That one table is the version pattern plans
are validated against, the default (the newest), the refusal text, and the Build settings field
(`GO_VERSIONS` in `deployment-defaults.ts`, kept equal by `TestGoRecipeCatalogueMatchesTheBuildSettings`);
a release joins it when `golang:<family>-alpine` is published, and `TestLiveGoRecipeCatalogueResolves`
resolves every family's image. `build.goVersion` pins a family or an exact patch, and so does
`.go-version`; both are built exactly, and a 1.25 pin builds with the warning `go_version_eol`. What
`go.mod` says is a minimum: `go 1.26.0` builds on `golang:1.26-alpine`, the family's newest patch with the
security fixes the .0 lacks, never on the unpatched release it names (the pass finding
`go_version_family` and the run log say so). The `toolchain` line is a preference: a maintained family it names is followed, one
newer than the catalogue builds with the newest family and the warning `go_toolchain_downgraded`
(`GOTOOLCHAIN=local` keeps a module that really needs the newer release a build error, "requires go >="),
and a `go` line newer than every family is refused. A minimum older than the maintained families builds
with the default. Resolved image digests, the chosen version, why it was chosen and the generated
Dockerfile are recorded in build evidence. See the [Go toolchain rules](https://go.dev/doc/toolchain).

Which toolchain builds the module is a setting, so detection keeps the facts — `goMinimumVersion`, the
`toolchain` line (`goToolchain`), the `.go-version` pin (`goVersionFile`) and, for a module a `go.work`
uses, the go.work's own `go` and `toolchain` lines (`workGo` and `workToolchain` in the candidate's `go`
facts), which the recipe folds in (the higher `go` minimum, the go.work's `toolchain` first) — rather than
a refusal.
Preflight runs `chooseGoRecipeVersion` with the plan's own `build.goVersion` over them and raises
`go_version_unsupported` (blocked, naming the pins) only when that fails: pinning 1.26 for a module
whose `.go-version` says 1.24 clears it, as it lets the recipe build.

The main package is read the way the go command reads it, from package clauses and build constraints
and never by compiling (`deploy/go_packages.go`, shared by detection and the recipe): a file counts for
linux on the host's architecture with cgo disabled and no extra tags, judged on its `//go:build` line
(or legacy `+build` lines) and its `_GOOS`/`_GOARCH` file-name suffix, so `ignore`, `tools` and mage
files drop out. A file importing `"C"` counts when it builds under cgo and has no pure-Go twin, which is
exactly when the recipe turns cgo on for it, so a command written in cgo is a command, not a library; `testdata/`, `_*` and `.*` directories and nested modules (a directory with its own
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

What the module needs beyond its main package is read by `planGoBuild` (`deploy/build_go_recipe.go`),
which detection and the recipe both run, so the candidate's `go` facts are what the build does and each
refusal is a named finding before Deploy (`preflight_go.go`):

- **cgo.** The recipe compiles C when a dependency is a cgo binding with no pure-Go path —
  `github.com/mattn/go-sqlite3` (which `gorm.io/driver/sqlite` pulls in), `mutecomm/go-sqlcipher`,
  `confluentinc/confluent-kafka-go` (built with `-tags musl`, its bundled librdkafka), `h2non/bimg` and
  `davidbyttow/govips` (libvips), `gographics/imagick.v3` (ImageMagick 7), `otiai10/gosseract`
  (Tesseract) — when `go.mod` requires it directly or indirectly, when a local file imports `"C"` with no
  pure-Go twin beside it (a file built only under `!cgo`; a twinned file builds pure Go), or when the
  build command sets `CGO_ENABLED=1`. Built with `CGO_ENABLED=0`, go-sqlite3 compiles a stub whose first
  query fails ("Binary was compiled with 'CGO_ENABLED=0'"). The build stage installs `gcc musl-dev`
  and the Alpine packages mapped from the libraries the bindings and the local files' `#cgo pkg-config:`
  and `#cgo LDFLAGS: -l` lines name for linux; the binary is linked statically
  (`-linkmode external -extldflags "-static"`) onto the usual `alpine:3.22` runtime unless a library
  is only a shared object (libvips, ImageMagick, Tesseract, libpq, librdkafka, libzmq, libpcap, libwebp,
  libheif), in which case it links dynamically and both stages are Alpine `3.24`
  (`golang:<family>-alpine3.24`, `alpine:3.24` with the runtime packages), because a binary must run on
  the release it was linked against. `go_cgo_enabled` (warning) names why cgo is on, and for a binding
  with a pure-Go replacement (modernc.org/sqlite, glebarez/sqlite for GORM, franz-go) suggests it; a
  library no package is known for is `go_cgo_library_unknown` (blocked) and the recipe's refusal.
- **Where dependencies come from.** A committed `vendor/modules.txt` builds with `GOFLAGS=-mod=vendor`
  and downloads nothing, so a vendored private module still builds (`go_vendored`). Without vendoring,
  `go.sum` is compared with every requirement of `go.mod` (a replacement's own version for a replaced
  module, none for a directory): an absent `go.sum` is `go_sum_missing` (warning) and one without an
  entry `go.mod` requires is `lockfile_out_of_sync` (warning); both build with `-mod=mod`, recording the
  checksums of what they download instead of stopping at "missing go.sum entry". Requirements from the
  module's own account (`github.com/acme/…` for `github.com/acme/app`) are `go_private_module` when the
  source is fetched with a credential: proxy.golang.org cannot see a private module. A module a local
  replacement or the `go.work` provides is in the checkout and is not named. Detection compares at most
  16 MiB of `go.sum` across all its modules (and 16 MiB for any one); past that a module's `go.sum` is
  left to the go command, which checks it during the build anyway. With a build
  variable `GIT_TOKEN` mapped to the install step, the recipe installs git, sets
  `GOPRIVATE=<host>/<owner>` and runs `go mod download` with git's URL for that host rewritten through
  `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_0`/`GIT_CONFIG_VALUE_0` in that one command's environment — the
  value lives in the step's secret mount and nowhere in the Dockerfile, a layer or git's configuration.
- **Workspaces and local replacements.** A module a `go.work` above it `use`s builds from the go.work's
  directory (the build context widens there, `WORKDIR` is the module); a module whose `replace`
  directives point at local directories builds from the nearest directory holding it and every target,
  with `GOWORK=off`. The runtime files are copied from the module's own directory. In a workspace the
  install step is `go list -e -deps ./... >/dev/null` rather than `go mod download`, which fetches every
  requirement — a sibling the go.work provides (`require example.com/shared v0.0.0`) included, which no
  proxy has ("unrecognized import path") — while listing the packages' imports fetches only what the
  build compiles; `-e` leaves a package not written yet (templ output, an embed the web stage builds) to
  the build. A replacement
  outside the checkout, or at an absolute path, is `go_local_replace_outside_root` (blocked) and the
  recipe's refusal; a module that is only a library is already `Go library` at low confidence, so the
  service in the workspace is selected.
- **Generated code.** `.templ` components without their `_templ.go` are generated before the build:
  `go tool templ generate` when `go.mod` has the `tool github.com/a-h/templ/cmd/templ` directive, else
  `go run github.com/a-h/templ/cmd/templ@<the required version> generate`, so the generator and the
  runtime agree. Files the repository's own scripts write and the recipe does not — the CSS a
  `tailwindcss … -o <file>` in a Makefile, Taskfile, justfile or package.json writes, sqlc's `out`
  directory — are `go_codegen_missing` (warning) when the commit lacks them.
- **Embedded front ends.** The embeds are those of the package being built and every package of the
  module it imports (read from the files' import lists), so another command's embed never blocks the
  service; with no command chosen yet, or a build command, every embed in the module counts. A
  `//go:embed` pattern whose directory the commit lacks, or holds only
  dotfiles (`dist/.gitkeep`), is built first when a package.json with a build script contains it, or
  its Vite `outDir` writes it or a directory above it (then the embedded directory itself is copied): a Node stage runs that package's lockfile-driven install and `build`
  script with the JavaScript recipe's own planner (`go_embed_frontend`, pass), and the output is copied
  into the Go stage before `go build`; the package's own candidate is demoted, so the server is selected
  rather than a site without its API. Nothing builds it and no build command is set:
  `go_embed_missing` (blocked).
- **Command-line applications.** A module on cobra (whose command tree something executes) or
  urfave/cli whose commands are named `serve`, `server` or `start` (`Use: "serve [flags]"` is serve,
  `Use: "server-status"` is not) starts `/app <that subcommand>` (`go_start_subcommand`, a warning to confirm it),
  since the binary run bare prints its help and exits. PocketBase's start command, port and data volume
  are the persistent-state defaults below; its readiness is `/api/health`.

The default build is `go build -trimpath -tags timetzdata -ldflags='-s -w' -o /out/app ./<package>`:
`timetzdata` embeds the zone database, so `time.LoadLocation` works on any runtime image. The runtime
stage installs `tzdata` as well (alpine carries none), for a `TZ` variable and anything that reads
`/usr/share/zoneinfo`; `ca-certificates-bundle` is already in the image. Source scanning does not
certify every transitive dependency: a cgo package outside the catalogue still builds without cgo, and
its failure names the compiler (`build_native_toolchain_missing`). The dashboard's own required Go
toolchain remains 1.26.8.

## Rust

`Cargo.toml` is read as data (`readCargoFile`, `deploy/detect_rust.go`), dotted and inline keys alike:
`axum.workspace = true` and `web = { package = "axum" }` are axum. The build runs on `rust:1-alpine`
(or `rust:<version>-alpine` when `rust-toolchain(.toml)` pins a stable release — nightly and beta need a
Dockerfile) and the binary runs on `alpine:3.22` as an unprivileged user. A custom build command runs in
place of the default one and must still leave the binary where Cargo writes it.

**Workspaces.** A crate that a `[workspace]` above it lists (a `members` glob it matches and no `exclude`
covers, or a path dependency of the root package) builds within that workspace: the build context is the
workspace root, where its `Cargo.lock`, `rust-toolchain` and `.cargo/config.toml` are, and the build is
`cargo build --release --locked -p <package> --bin <binary>`; `x.workspace = true` dependencies resolve
through `[workspace.dependencies]`, so a member's framework and port are read. Each member with a package
is a candidate (`cargo_workspace_member`, pass) and a workspace root with no package of its own is none
once the walk found its members, so the ranking chooses among the members — a service over a library,
a tie as the usual choice between candidates. A crate that an ancestor workspace does not list builds
on its own, as it always did.

**The served binary** is chosen from Cargo's own targets: declared `[[bin]]`s whose `required-features`
the default features enable, and, unless `autobins = false`, `src/main.rs` as the package's binary and
each `src/bin/*.rs` and `src/bin/*/main.rs`. `package.default-run` wins, then the only binary, then the
only one whose source starts a server (`axum::serve`, `HttpServer::new`, `rocket::build`/`#[launch]`,
`warp::serve`, poem, salvo, Loco's CLI), then the one named after the package or `server`/`api`/`web`/
`app`/`serve`. A tie asks — the decision `rust_binary_ambiguous` on `build.cargoBin`, which a run cannot go
past — and `build.cargoBin` names the binary to serve (`rust_binary_missing` when it is not a target).
The recipe always builds `--bin <binary>`, and reads it from `target/release`, from
`target/<triple>/release` when `.cargo/config.toml` sets `[build] target`, or from its `target-dir`.

**Native crates.** The build stage, which the runtime image leaves behind, always installs `musl-dev
pkgconfig openssl-dev openssl-libs-static perl make` (perl and make are what OpenSSL's vendored build and
jemalloc need). `Cargo.lock`'s packages (the lock resolves every feature of the workspace) add: `cmake`
→ `cmake g++ linux-headers`; `cxx`/`link-cplusplus` → `g++`; `bindgen`/`clang-sys` → `clang-dev`, and a dynamic link (below);
`prost-build`/`tonic-build`/`protobuf-codegen` without `protoc-bin-vendored`/`protobuf-src` → `protoc
protobuf-dev`; `pq-sys` without `pq-src` → `libpq-dev`, linked statically (`PQ_LIB_STATIC=1` and the
archives libpq needs after it on the link line, through `RUSTFLAGS`, which replaces a
`.cargo/config` `build.rustflags`); `mysqlclient-sys` without `mysqlclient-src` → `mariadb-connector-c-dev
mariadb-static zlib-static zstd-static` with `MYSQLCLIENT_STATIC=1 PKG_CONFIG_ALL_STATIC=1`;
`libsqlite3-sys` → `sqlite-dev sqlite-static`; `libz-sys` → `zlib-dev zlib-static`; `rdkafka-sys` → `bash
g++ linux-headers`. The binary is static, except with bindgen: its build script loads libclang, which a
build script linked statically against musl cannot ("Dynamic loading not supported"), so the build sets
`RUSTFLAGS="-C target-feature=-crt-static"` — every artifact, build scripts included, links musl
dynamically — libpq links as a shared library instead of `PQ_LIB_STATIC`, and the runtime stage installs
`libgcc` (and `libpq`) beside `tzdata` (`nativeRuntime`). With `[build] target` in `.cargo/config`,
Cargo keeps `RUSTFLAGS` from build scripts, so such a build still needs a Dockerfile. The classifier names
that failure (`build_system_library_missing`, detail `rust-static`) apart from a missing libclang.
`rust_native_dependency` (pass) lists what was added and why; a GUI or hardware binding the lock resolves (gtk, webkit2gtk, alsa, udev) is
`rust_native_dependency_unmapped` (warning).

**sqlx.** The query macros are looked for in the crate's sources and in those of the crates inside the
checkout it depends on by path (directly or through each other, at most 16, a workspace dependency
resolved from the workspace root) that depend on sqlx, where a workspace usually keeps them. Committed
offline data — `.sqlx` at the crate, at a library whose macros it compiles or at the workspace root, or the
`sqlx-data.json` sqlx 0.5 and 0.6 read (`sqlxOfflineData` names which) — builds with `SQLX_OFFLINE=true`, so a
`DATABASE_URL` in a committed `.env` or the build's variables never sends the query macros to a database
the build cannot reach (`sqlx_offline`, pass). The query macros (`query!`, `query_as!`, …) with no
`.sqlx` are `sqlx_offline_data_missing` — blocked, with `cargo sqlx prepare`; a warning when a build-scoped
`DATABASE_URL` is configured, since that compiles against a database the build can reach only if it is
not a linked one. `sqlx::migrate!()` is `sqlx_migrations`: the application applies them at start.

**The lockfile.** `Cargo.lock` is compared with the manifests: a dependency it does not resolve, or
resolves at no version the requirement accepts (Cargo's caret, tilde, exact and comparator rules), is
`lockfile_out_of_sync` (warning) and the build runs without `--locked` rather than stopping at "the lock
file needs to be updated". A `version = 4` lock with a toolchain pinned before 1.78, which cannot read it,
builds on `rust:1-alpine` (`rust_lock_newer_than_toolchain`). Whenever a `rust-toolchain` file is in the
context the build sets `RUSTUP_TOOLCHAIN=${RUST_VERSION}`: rustup obeys the file over the image's own
toolchain and would otherwise install the pin a second time — or, here, the old release the lock
outgrew. A missing lock is the unpinned warning, whose action is `cargo generate-lockfile`. Detection
charges each `Cargo.lock` (read once per workspace, at most 8 MiB) to the same 16 MiB budget as the
sources it reads; past it a lock is present but unread, and only the recipe, which reads it whole, acts on
it.

**Memory.** The build runs as many compile jobs as whole gigabytes are free when it starts
(`CARGO_BUILD_JOBS`, read from `/proc/meminfo` inside the step, so the Dockerfile is the same on every
host), and `build_memory_low` estimates 2 GiB for a release build and 3 GiB with fat LTO, one codegen
unit, or Leptos.

**Frameworks.** `loco-rs` (5150, recognised before the axum it is built on), `axum` (3000), `actix-web`
(8080), `rocket` (8000), `warp` (3030), `poem` (3000) and `salvo` (5800) mark a web service with the
framework's conventional port. The served binary's source and `Rocket.toml` are read for the port and
bind; the "confirm the port" decision stays only when none of them names one. Rocket, whose default
address is 127.0.0.1, runs as `exec env ROCKET_PORT=${PORT:-8000} /app` with `ROCKET_ADDRESS=0.0.0.0`
(both outrank `Rocket.toml`); Loco starts with `/app start --binding 0.0.0.0 --port ${PORT:-5150}` from
its `<app>-cli` default-run binary. A crate with no framework is a worker that asks. Three build a
browser side:

- **Leptos** (`[package.metadata.leptos]`, or the `[[workspace.metadata.leptos]]` project whose
  `bin-package` the crate is) builds on `rust:<version>-bookworm`, because cargo-leptos downloads glibc
  helpers (wasm-bindgen, dart-sass): `rustup target add wasm32-unknown-unknown`, `cargo install
  cargo-leptos --locked --version 0.3.9`, then `cargo leptos build --release` (`--project` for a
  workspace member). The server binary and `target/site` (its `site-root`) run on `debian:bookworm-slim`
  with `LEPTOS_SITE_ROOT=site`, `LEPTOS_ENV=PROD` and `LEPTOS_OUTPUT_NAME` (its `output-name`, else the
  project's name). With `hash-files = true` the bundle's file names carry content hashes that the server
  reads from the `hash.txt` (or `hash-file`) the build writes beside the binary: the recipe copies it to
  `/hash.txt`, beside `/app`, and sets `LEPTOS_HASH_FILES=true` (and `LEPTOS_HASH_FILE_NAME`), since
  without it the page links unhashed names that 404 and never hydrates. The server is started as `exec env LEPTOS_SITE_ADDR=0.0.0.0:${PORT:-<site-addr port>} /app`, since
  `site-addr` is a development address; the port is the `site-addr`'s.
- **Trunk** (a `Trunk.toml`, or an `index.html` with `data-trunk` links, beside yew, leptos, dioxus-web,
  sycamore or seed) is a static site: `cargo install trunk --locked --version 0.21.14`, `trunk build
  --release --dist /out/dist`, served by nginx with the single-page fallback. The `index.html` is its
  source, so it is not a static-site candidate of its own.
- **Dioxus** (it bundles with `dx`) and **Shuttle** (`shuttle-runtime` starts `main` on Shuttle's own
  runtime) are `recipe_unsupported` with that reason: a Dockerfile builds them.

A binary whose `src/main.rs` or `src/bin` entry calls `dotenvy::dotenv()` with `.expect`, `.unwrap()` or
`?` gets the same empty `.env` as a Go one.

A private registry a `.cargo/config.toml` (or `.cargo/config`) at or above the crate declares
(`[registries.<name>]`) is read with `CARGO_REGISTRIES_<NAME>_TOKEN`, a detected variable with step
`install` that reaches `cargo fetch` through the install's secret mount. It is required, and
`registry_token_missing` blocks without it, when a dependency names that registry, it is the default
registry, or it replaces crates.io.

## Java and Kotlin

A root with a `pom.xml` or a `build.gradle(.kts)` is read the way Maven and Gradle read it, as data
(`deploy/detect_jvm.go`, `detect_maven.go` and `detect_gradle.go`, shared by detection and the recipe in
`build_java.go`):

- **Maven** POMs are parsed as XML. A module's in-repository parents (`<parent>` with its default
  `../pom.xml` relative path, checked by artifactId) contribute their properties, dependencies and build
  plugins, and the reactor that aggregates it (`<modules>`, profiles' included, nested aggregators followed
  to the top) is where it builds: `mvn -B -ntp -DskipTests -pl :<artifactId> -am package` from the
  reactor, with `-f` when the reactor sits below the directory that holds every parent. A POM-packaged
  aggregator is never the candidate once one of its modules builds an application, and set as the root
  directory it names the module to choose instead.
- **Gradle** reads `settings.gradle(.kts)` — `include(…)` lists, `:a:b` meaning `a/b`, and
  `project(":x").projectDir` — and builds from that settings root by project path
  (`./gradlew :app:bootJar`), wherever in the checkout it is. A composite build's `includeBuild(…)`
  targets are read from the settings root too, so one outside it widens the build context to hold both,
  and Gradle runs from the settings root with `-p` (`./backend/gradlew -p backend :app:installDist`).
  `gradle/libs.versions.toml` resolves version-catalog aliases (`libs.spring.boot.starter.web`,
  `alias(libs.plugins.ktor)`, bundles) to their coordinates and plugin ids before anything matches a
  framework, and plugins applied through `buildSrc`, `build-logic` or an included build's precompiled
  script plugins are followed to what they apply. `apply false` applies nothing.
- The build's own logic is set aside as `tooling`, never a candidate: `buildSrc`, a `build-logic` build, a
  build `pluginManagement { includeBuild(…) }` names, and any project applying `kotlin-dsl`,
  `java-gradle-plugin` or `groovy-gradle-plugin` — a convention project that puts the Spring Boot plugin on
  its classpath is not a Spring application.
- A module or project that packages nothing — a library, a `java-library`, a POM aggregator — is set
  aside (`library`) when its build has an application, whose evidence names what it is built with; with
  several applications each is a candidate and the ranking decides. `java_module_selected` and
  `gradle_subproject_selected` name the choice and where it builds. A build none of whose modules packages
  anything keeps the aggregator, with a `recipeIssue` naming what would.
- A Kotlin Multiplatform server that depends on a project applying the Android Gradle plugin cannot even
  be configured without the Android SDK, which is its `recipeIssue` before Deploy — and since Gradle
  configures every project of a build, so is any other project that applies it, unless
  `gradle.properties` sets `org.gradle.configureondemand=true`. Android modules themselves are set aside as
  mobile apps.

The artifact follows the packaging plugin, and the task is the one that produces it — never `build`,
which also runs checks and linters: Spring Boot `bootJar`/`bootWar` (Maven's `package` with
spring-boot-maven-plugin; an executable war runs as `java -jar /app/app.war`), Quarkus `quarkusBuild`
(the `quarkus-app/` directory, run as `java -jar /app/quarkus-app/quarkus-run.jar`, or the `*-runner.jar`
when `application.properties`, `gradle.properties` or a POM property sets
`quarkus.package.jar.type=uber-jar`), Ktor's `buildFatJar` and Shadow's `shadowJar` (`*-all.jar`),
the application plugin's (and Micronaut's) `installDist` (the distribution in `/app`, started by
`/app/bin/app`), Maven shade, assembly's `jar-with-dependencies`, Micronaut and Vert.x fat jars, and a
Helidon jar with its `libs/`. Jars are tried by name in that order (`-sources`, `-javadoc`, `-tests`,
`-plain` and `original-` never), and only one whose manifest names a `Main-Class` (read with the JDK's
`jar` tool) is copied, with the `lib/` or `libs/` beside it that a thin jar's `Class-Path` points at.
Nothing runnable fails the guard, `build produced no executable jar in …`. Preflight warns
`java_artifact_not_runnable` when no build file packages anything, and blocks a servlet WAR. Vaadin builds
its `production` profile and JHipster its `prod` (`-P`, or `-Pvaadin.productionMode=true` for Gradle);
Vaadin with neither is `vaadin_dev_mode`. A Spring Boot root with `application-prod.*` or
`application-production.*` gets an empty `SPRING_PROFILES_ACTIVE` row whose example is that profile:
running with it is the operator's call, since a production profile usually needs variables nobody has set
yet.

**The Java release.** The catalogue carries 8, 11, 17, 21 and 25. The build files declare the release —
the POM's `maven.compiler.release`, `java.version`, `maven.compiler.target`/`source` or Kotlin's
`jvmTarget`, then maven-compiler-plugin's `<release>` (properties resolved, parents included); Gradle's
toolchain (`JavaLanguageVersion.of`, `jvmToolchain`, through the catalog too) or source/target
compatibility, in the project's script, the root script, then convention plugins — and a version file pins
it: `.java-version`, `.sdkmanrc`, `.tool-versions`, `mise.toml` or Heroku's `system.properties`, the nearest
first. `build.javaVersion` overrides both. A release the catalogue lacks maps up to the next one
(`java_version_mapped`: a newer JDK compiles for it and its JRE runs the result), and a version file older
than what the build compiles for gives way to the build. A **Gradle toolchain is exact**: when Gradle runs
on another JDK, the declared one is copied beside it from `eclipse-temurin:<N>-jdk` into `/opt/jdk-<N>` and
passed as `-Porg.gradle.java.installations.paths`; one the catalogue lacks needs the foojay toolchain
resolver in the settings, or it is `java_version_unsupported`. The wrapper's Gradle
(`gradle-wrapper.properties`) must run on the JDK it builds with — Java 17 needs Gradle 7.3, 21 needs 8.5,
25 needs 9.1, and Gradle 9 needs 17 — so a wrapper that cannot runs on the newest JDK it supports (compiling
for the older release, or with the toolchain provided), and when no JDK works `gradle_wrapper_incompatible`
names the `./gradlew wrapper --gradle-version` to run. With nothing declared the release is 21, or the
newest one the wrapper runs on.

Builds run on `maven:3-eclipse-temurin-<N>`, on `eclipse-temurin:<N>-jdk` with the committed wrapper (its
Windows line endings stripped, made executable), or on `gradle:8-jdk<N>` (`gradle:9-jdk25` for 25) without
one — also when `gradlew` is committed without `gradle/wrapper/gradle-wrapper.jar`, a `*.jar` ignore rule's
doing, which is `gradle_wrapper_jar_missing`. A Maven wrapper pinning Maven 4 runs as `./mvnw`; one pinning
Maven 3 builds with the image's Maven 3.9 instead, the same major without a download. The
artifact runs on `eclipse-temurin:<N>-jre` — Ubuntu, published for amd64 and arm64 for every release (the
Alpine JREs of 8, 11 and 17 are amd64-only, and an arm64 server failed every such build after Deploy) — as
the unprivileged `app` user with `-XX:MaxRAMPercentage=75`, so the heap follows the container's limit. A
build for an explicit target platform refuses a base that does not publish it, naming both, and
`TestLiveRecipeBaseCatalogueRunsOnAmd64AndArm64` keeps every catalogue image on both architectures.

Spring Boot, Quarkus, Micronaut, Javalin, Ktor, Helidon and Vert.x mark a web service (8080; Javalin
7070); a plain project is a worker that asks. These frameworks do not read PORT, so the default start
command bridges it into the variable that outranks `application.properties`/`.yml`: `exec env
SERVER_PORT=${PORT:-8080} java -jar /app/app.jar` for Spring Boot and Helidon, `QUARKUS_HTTP_PORT` for
Quarkus, `MICRONAUT_SERVER_PORT` for Micronaut, around whichever launch the packaging needs (`exec` keeps
the JVM as PID 1). A configured `server.port` becomes the suggested port. Vert.x, Javalin and Ktor's
embedded server fix their port in code, which detection reads (`listen(8888)`, `start(7070)`,
`embeddedServer(port = …)`); Ktor's `application.conf` with `port = ${?PORT}` follows PORT.

A private repository's credentials come from the build's own configuration: the `settings.xml` that
`.mvn/maven.config` passes with `-s` (or a committed `.mvn/settings.xml` or `settings.xml` naming `${env.…}`,
which the recipe then passes itself), whose servers' `${env.X}` are required when a repository or mirror of
that id is used; and the `credentials` of a Gradle `repositories` block in the scripts, the settings or a
convention plugin (`System.getenv`, `providers.environmentVariable`, and typed
`credentials(PasswordCredentials::class)` as `ORG_GRADLE_PROJECT_<name>Username`/`Password`) — a
`publishing` block's credentials only publish. Each is a detected variable with step `install`, and
`registry_token_missing` asks for the ones the plan does not give.

## .NET

A `*.csproj`, `*.fsproj` or `*.vbproj` is read the way MSBuild and the dotnet CLI read it, as XML data
(`deploy/detect_dotnet.go`, shared by detection and `build_dotnet.go`): the `Directory.Build.props` chain
that applies (the nearest, and the next one up when it imports it) is merged under the project's own
properties, `$(Property)` references resolve (`$(MSBuildProjectName)` included), and the SDK comes from the
`Sdk` attribute, `<Sdk Name=… />` or an `<Import Sdk=…>`. What it builds decides the rest:

| Kind | From | Built and run |
|------|------|---------------|
| web | `Microsoft.NET.Sdk.Web`, or an executable with a `Microsoft.AspNetCore.App` framework reference (a console host running Kestrel) — C#, F# (Giraffe, Saturn, Falco) or Visual Basic | published, run on `aspnet:<target>`, port 8080 |
| worker | `Microsoft.NET.Sdk.Worker` | `runtime:<target>`, a background service with no port question |
| exe | `OutputType` Exe | `runtime:<target>`, asking whether it serves HTTP |
| blazor-wasm | `Microsoft.NET.Sdk.BlazorWebAssembly` | published, its `wwwroot` served by nginx with the single-page fallback (static, port 80) |
| library, test, apphost | anything else; `IsTestProject`, `Microsoft.NET.Test.Sdk`, xunit, NUnit, MSTest or `MSTest.Sdk`; `Aspire.AppHost.Sdk` or `IsAspireHost` | not deployed |

Among several projects in one directory the one web project, else the one program, is published. Across
a solution, test projects and an Aspire AppHost are set aside (`tooling`), a library a runnable project
references is built with it (`library`), a Blazor WebAssembly client its ASP.NET Core host references is
served by that host, and a static candidate in a .NET project's `wwwroot` is not a site of its own. The
`.sln` or `.slnx` is not needed for any of it: the walk finds every project, and `ProjectReference` paths
say what builds with what.

**Where it builds.** A web project in a solution's `src/` references its siblings and inherits the
solution root's `Directory.Build.props`, `Directory.Packages.props` (central package management) and
`NuGet.config`, so the build context is the directory that holds all of them: the project, every project
it references transitively, each props and targets file that applies, every `NuGet.config` at or above it
(NuGet merges them all) and the `global.json` above it. The Dockerfile copies that context and runs
`dotnet restore <project>` (its own layer under the install secret mount) and `dotnet publish <project> -c
Release --no-restore -o /out` in the project's own directory — where the dotnet CLI resolves `global.json`
for a developer too — then checks `/out/<AssemblyName>.dll`, the assembly name resolved through the props.
`dotnet_project_selected` names the project and the context.

**The release and the SDK.** The target comes from `<TargetFramework>` or the newest supported portable
entry of `<TargetFrameworks>` (net8.0, net9.0 and net10.0); publish names it (`--framework`) when there are
several. `build.dotnetVersion` chooses the release instead, and retargets a project that declares another,
which rescues a net6.0 or net7.0 project or one whose target the recipe cannot read; without it such a
project is `dotnet_version_unsupported`. Restore resolves every framework the project declares, which are
the frameworks its references are built for. Only a retarget, or a sibling the SDK image cannot restore —
one needing a workload (`net9.0-android`, iOS, Mac Catalyst) or a newer SDK; Windows targets restore
anywhere — holds restore to the published framework (`-p:TargetFramework`), and restore hands that
framework to every project the published one references, which would then build for its own and fail
with NETSDK1005. So a referenced project that does not declare it is `dotnet_version_unsupported` before
Deploy, naming it: a net7.0 API that references a net7.0 library is fixed by its own
`<TargetFramework>` — a net9.0 project references a net7.0 library, and restore then resolves each
project's own — not by the setting. A `global.json` pin decides the SDK image
by its `rollForward`: `latestPatch` (the default), `patch` and `disable` build with `sdk:<exact version>`,
`feature` and `latestFeature` with the pin's own `sdk:<major.minor>`, and the minor and major policies with
the newer of the pin's release and the target's. A pin that cannot build the target is
`dotnet_version_unsupported` before Deploy. Microsoft publishes an exact SDK image only for each patch of
the feature band that was newest when it shipped — `sdk:8.0.100` and `sdk:8.0.414`, never `sdk:8.0.119` —
so a patch-level pin to an older band has none, and prepare_context ends the run as
`dotnet_sdk_pin_unavailable`, naming the pin, its policy and the fix (`rollForward` `latestFeature`, or a
pin that has an image); the check before Deploy resolves no image, so it cannot say so earlier.
`.tool-versions` (`dotnet`, `dotnet-core`) and `mise.toml` pin the SDK the same way when there is no
`global.json`. The runtime image always follows the target.

`PublishAot`, `PublishSingleFile`, `PublishReadyToRun`, `SelfContained` and `PublishTrimmed`, in the
project or its props, make publish need the AOT SDK image and write a native executable instead of the dll;
the recipe passes `-p:PublishAot=false … -p:PublishTrimmed=false` to restore and publish alike (they must
agree) and runs the JIT-compiled, framework-dependent dll (`dotnet_aot_disabled`). A project whose publish
builds a single-page application with npm — `<SpaRoot>`, an `Exec` running npm, yarn or pnpm, or a
referenced `*.esproj` — gets Node in the SDK image: the Node recipe's install planner reads the front end's
lockfile, Node is copied from the catalogue's Debian image of that release (the SDK images are glibc; the
plan's Corepack releases and Bun come with it), and the lockfile's install runs in the front end's
directory before publish (`dotnet_spa_node`); a Node candidate in that directory ranks below the project.
A front end whose install the planner refuses (competing lockfiles) still gets the Node release it asks
for, and the project's own publish runs npm; a `package.json` the recipe cannot read — larger than 512
KiB — is refused by name.
An Aspire AppHost's `WithReference` wiring — another project's `services__<name>__http__0`, a resource's
`ConnectionStrings__<name>` — is named on each service it starts as `dotnet_aspire_orchestration`:
deployed on its own, nothing sets them.

A `NuGet.config`'s `packageSourceCredentials` (`%NUGET_TOKEN%`) are detected variables with step `install`,
required when their source is enabled; mapped to the install, they reach `dotnet restore` alone.

Kestrel reads its port from `ASPNETCORE_HTTP_PORTS`, so the default start command bridges the `PORT` the
runtime injects: `ASPNETCORE_HTTP_PORTS=${PORT:-8080} dotnet /app/<Assembly>.dll`. An
`appsettings.json`/`appsettings.Production.json` that names Kestrel endpoints or `Urls` outranks that
variable, so the bridge also sets the one endpoint's `Kestrel__Endpoints__<Name>__Url=http://+:${PORT:-8080}`,
or `URLS=http://+:${PORT:-8080}`; several endpoints, or an HTTPS one, cannot all move to one port and are
named in `listen_endpoints_unbridged`. A URL passed in `Program.cs` (`app.Run("http://localhost:5000")`,
`UseUrls`) outranks everything and is read as a fixed port and, on loopback, a blocker. `/app`, `/app/data`
and the Data Protection key ring `/home/app/.aspnet/DataProtection-Keys` belong to the `app` user, so a
relative `app.db` and a volume on either directory are writable.

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
sign-in fails on. Later releases find the volume filled and copy nothing. A file the build context's
`.dockerignore` leaves out, or one reached through a symlink, is not copied.

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
runtime whose `public/build` is copied in. That asset stage installs through the same plan as the Node
recipe ([JavaScript installs](#javascript-installs), toolchain stage `assets-toolchain`), the same
`packageManager` build setting applies to a `php` recipe, and its lockfile findings — competing lockfiles,
none (`assets_dependencies_unpinned`), a stale one — reach preflight before Deploy. Laravel's start runs `php artisan migrate --force` first;
Symfony's runs `doctrine:migrations:migrate` when the migrations bundle is present. `storage/`,
`bootstrap/cache` and `database/` are created writable so a first start on an empty volume works. The
recipe links the public disk the way `php artisan storage:link` would (`public/storage` →
`/app/storage/app/public`, unless the repository commits one), without booting the application in
the build. With `laravel/horizon` the image also carries `pcntl` and `redis`, which Horizon's own
manifest requires. A Laravel import's `APP_KEY` is generated on the server at commit (`base64:` over 32 random
bytes), and the **Generate** button is offered on a self-issued name the operator types by hand — never
on a provider's (`STRIPE_SECRET_KEY`, `AUTH_GITHUB_SECRET`, a client or webhook secret). Laravel's
`.env.example` names its engine in `DB_CONNECTION`, which becomes a database suggestion on `DB_URL`
(Laravel 11 and later) or `DATABASE_URL` (Laravel 10 and earlier).

## Ruby

A `Gemfile` whose lock (or, without one, whose `gem` lines) names `railties`, `hanami`, `sinatra`,
`roda`, `grape` or `rack`, or a root with `config.ru`, is built by the Ruby recipe
(`deploy/frameworks_ruby.go`, `build_ruby.go`) on the official `ruby:<release>-slim` image. Detection and
the build read the same files through the same functions — `Gemfile`, `Gemfile.lock` (its GEM, GIT and
PATH specs, PLATFORMS, RUBY VERSION and BUNDLED WITH), `.ruby-version`, `.tool-versions`, `config.ru`,
`config/application.rb`, `config/database.yml`, `bin/rails` and the `Procfile` — as bounded data.

The release comes from `.ruby-version` (a leading `ruby-` dropped), then `.tool-versions`, then the
Gemfile's `ruby` line (`file: ".ruby-version"` defers to the file), then the lock's RUBY VERSION as a
family. An exact release builds on its own image (`ruby:3.4.7-slim`), which the build resolves first and
replaces with the family's newest release, saying so, when the registry has no image for it (3.4.0 has
none); anything else builds on the newest catalogue family the Gemfile's requirement allows, 3.4 first.
3.3, 3.4 and 4.0 are in the catalogue; another release, JRuby or TruffleRuby, or a `.ruby-version` the
Gemfile's requirement refuses, is a `recipe_unsupported` finding. RubyGems runs the Bundler the lock was
written with on its own, so none is installed; a lock written by Bundler 1, which does not run on Ruby
3.2 and later, is `ruby_bundler_outdated` (warning).

A committed lock installs frozen (`BUNDLE_DEPLOYMENT=1`, into `/usr/local/bundle`, without the
development and test groups); without one Bundler resolves, and preflight's `dependencies_unpinned` says
so. A lock resolved on a Mac or on the other architecture has no platform for this server, and a frozen
install refuses it ("Your bundle only supports platforms …"): the recipe runs `bundle lock --add-platform
<x86_64|aarch64>-linux` first, and preflight warns `ruby_lock_platform_added` with the command that
fixes the lock. A lock with `ruby` or the server's Linux platform needs nothing. A repository's own
Dockerfile beside a Ruby root is judged the same way: `ruby_lock_platform_missing` is blocked when the
Dockerfile freezes the bundle (`BUNDLE_DEPLOYMENT`, `BUNDLE_FROZEN`, `--deployment`, `--frozen`) and a
warning otherwise, and not raised when the Dockerfile's own `bundle lock --add-platform` names the
server's platform before it installs. The build stage installs the headers the locked gems compile
against (`libpq-dev` for `pg`, `default-libmysqlclient-dev` for `mysql2`, `libssl-dev` for `trilogy`,
`libsqlite3-dev`, `libmagickwand-dev`, `libffi-dev`, always `build-essential`, `pkg-config` and
`libyaml-dev`, and `git` for Git gems) and the runtime only the libraries they load (`libpq5`,
`libmariadb3`, the virtual `libvips` for `ruby-vips`, `imagemagick`); the names hold on bookworm and
trixie. A Rails runtime also installs `libjemalloc2` and preloads it (`LD_PRELOAD=libjemalloc.so.2`), as
Rails' own Dockerfile does, which keeps a long-running server's memory from fragmenting. The Gemfile's
other gem servers (`source "https://gems.contribsys.com/"`) are `BUNDLE_<HOST>` variables mapped to the
install, and a Git gem's HTTPS host one the install may use; a Git gem over SSH, which the build has no
key for, is `ruby_git_gem_ssh` (warning).

| Framework | Start | Port |
| --- | --- | --- |
| Rails | `bundle exec rails db:prepare && exec bundle exec rails server --binding 0.0.0.0 --port ${PORT:-3000}`, without `db:prepare` when Active Record is not loaded or there is no `config/database.yml`; `bin/rails` must be committed | 3000 |
| Hanami | `exec bundle exec puma --port ${PORT:-2300}`, after `bundle exec hanami db migrate` when `hanami-db` has migrations | 2300 |
| Sinatra, Roda, Grape, Rack | `exec bundle exec puma --port ${PORT:-N}` (else the locked `unicorn`, `thin` or `rackup`); a classic Sinatra script with no `config.ru` runs `exec bundle exec ruby app.rb -o 0.0.0.0 -p ${PORT:-4567}` | 4567 (Sinatra), 9292 |

Each start `exec`s its server, so the server is the container's first process and a stop's SIGTERM
reaches it rather than a shell that ignores it until the grace period ends. A `Procfile` web process
outranks all of these, and its `release:` line is the candidate's release command, run in the release
image. With no server gem and no `config.ru` the candidate asks for the
start command, and preflight's `start_command_missing` names what is missing. The image sets the
framework's environment (`RAILS_ENV`, `HANAMI_ENV`, `RACK_ENV`, `APP_ENV` to `production`) and for Rails
`RAILS_LOG_TO_STDOUT=1` and `RAILS_SERVE_STATIC_FILES=1`, which Rails 7.0 and earlier need before they
log where the dashboard reads and serve their own precompiled assets. It runs as root from `/app`, so
Rails' `storage/` takes the volume [persistent state](#persistent-state) plans at `/app/storage`.

Rails precompiles its assets when an asset pipeline is locked (Propshaft, Sprockets, jsbundling,
cssbundling, tailwindcss-rails, dartsass-rails, Shakapacker, Webpacker, Vite Ruby, importmap):
Rails 7.1 and later with `SECRET_KEY_BASE_DUMMY=1`, as the Dockerfile `rails new` writes does, and older
releases with a throwaway key made inside the step, never a value from the plan. Hanami with
`hanami-assets` runs `hanami assets compile`. A Rails or Hanami application whose `package.json` is its
asset pipeline borrows Node: the install is planned by the [JavaScript installs](#javascript-installs)
planner on the Debian Node image (the lockfile's manager, pinned releases through Corepack, Bun copied
in), whose `/usr/local` and `/opt` are copied into the Ruby build stage under `/opt/node`, and the install
runs there before the precompile, which calls `yarn build` or `npm run build` itself. tailwindcss-rails
and dartsass-rails need no Node: their compilers are platform gems, which is one more reason the lock
has to carry the server's platform. ExecJS without `mini_racer` gets Node alone. The runtime stage has no Node, and `node_modules` stays in the build.
Build values reach the precompile the way they reach any recipe's build (a build-scoped
`RAILS_MASTER_KEY` among them).

## Elixir

A `mix.exs` is built by the Elixir recipe (`deploy/frameworks_elixir.go`, `build_elixir.go`) into a mix
release on the official `elixir:<release>-otp-<otp>-slim` image, and the release runs on that same
image, pinned to the same digest: a release carries the ERTS it was built with, linked against the
builder's libcrypto and ncurses, so running it on a Debian base of another release is how a working
build fails at start. The release comes from `.tool-versions` (`elixir 1.17.3-otp-27`, with `erlang
27.1.2` naming the OTP), then `.elixir-version`, then `elixir_buildpack.config`, with `mix.exs`'s
`elixir:` requirement as the floor; without a declaration it is 1.19 on OTP 28, or the newest family the
requirement allows. 1.17, 1.18, 1.19 and 1.20 are in the catalogue on the OTP majors the official image
publishes for each; anything else is `recipe_unsupported`. An exact release without an image builds on
its family's newest, as Ruby's does.

The build follows `mix phx.gen.release`: `mix local.hex` and `local.rebar`, `mix deps.get --only prod`
(`--check-locked` with a committed `mix.lock`, under the install secret mount, where a `HEX_API_KEY` for
a private organisation's packages belongs), `deps.compile`, `mix assets.setup` when the alias exists,
the npm install of `assets/package.json` when there is one (the same borrowed Node as Rails), `mix
compile`, `mix assets.deploy` when the alias exists, and `mix release` — named when `mix.exs` configures
releases (`default_release`, else the first of `releases:`), plain otherwise. An umbrella without a
release is `recipe_unsupported`. The slim image has no CA certificates, which Hex needs, so both stages
install `ca-certificates`. The runtime copies the release to `/app` and runs it as the unprivileged
`app` user (uid 10001) with `LANG=C.UTF-8`, and for Phoenix `PHX_SERVER=true`, which a release otherwise
needs its `bin/server` overlay for.

The start is `/app/bin/<release> start`, or `/app/bin/server` when the overlay exists. Ecto migrations
(`ecto_sql` with `priv/*/migrations`) run in front of it: the project's own `Release.migrate`
(`/app/bin/shop eval "Shop.Release.migrate" && exec /app/bin/shop start`), else the same loop
`phx.gen.release` writes, evaluated inline; a `rel/overlays/bin/migrate` overlay is the release command
instead, run in the release image before each release. An umbrella's migrations are its applications'
(`apps/*/priv/*/migrations`, and a `Release` module under `apps/*/lib`): the inline loop loads each of
those applications by the name its `mix.exs` gives it and migrates the repositories it configures, and
one the release leaves out has none to migrate. Phoenix's `force_ssl:` in `config/prod.exs` or
`config/runtime.exs` redirects every plain-HTTP request to HTTPS, which readiness (any answer) passes and
visitors do not: without an HTTPS domain the plan gets `readiness_redirects_to_https`, and with one but no
`rewrite_on: [:x_forwarded_proto]` (Phoenix 1.8's generator writes it) the same finding names the
redirect loop behind the proxy. Phoenix listens on PORT, 4000 by default; a Plug or Bandit service on
the literal port its application module names. `SECRET_KEY_BASE`, `PHX_HOST` and
`DATABASE_URL` are set up by [environment discovery](#environment-discovery-and-database-suggestions),
and an `ecto_sqlite3` file named by `DATABASE_PATH` moves to `/app/data`, the directory the image
creates for the `app` user.

## Scala and Clojure

Scala (`build.sbt`) and Clojure (`project.clj`, or `deps.edn` with a tools.build `:build` alias) build on
the JVM the way the Java recipe does (`deploy/frameworks_jvm_languages.go`): the heap follows the
container's limit (`JAVA_TOOL_OPTIONS=-XX:MaxRAMPercentage=75`), the program runs as the `app` user from
`/app`, and the release comes from the root's version file — read as the Java recipe reads it:
`.java-version`, `.sdkmanrc`, `.tool-versions`, `mise.toml` or `system.properties` — or a `--release` the
build compiles for, 17, 21 or 25, 21 by default. A `--release` (or `-release:`) below 17 is only the
bytecode the build targets, which a 17 compiler still writes: it builds and runs on 17. A version file
naming another JDK is `recipe_unsupported`. The builders are the sbt project's
`sbtscala/scala-sbt:eclipse-temurin-<jdk>_<1|2>.x` (the launcher series `project/build.properties`
names; it fetches the sbt and Scala releases the project pins) and the official
`clojure:temurin-<jdk>-lein` and `-tools-deps`.

- **sbt-native-packager** (`enablePlugins(JavaAppPackaging)`, or Play, which brings it): `sbt -batch
  update`, `sbt -batch stage`, and the staged directory runs on `eclipse-temurin:<jdk>-jre` (Ubuntu,
  since the start script is bash) as `/app/bin/<script>` — the project's `executableScriptName`, else
  sbt's normalised `name`, top-level settings counting for the root project. A multi-project build
  stages the project whose settings enable the packager. Both names are written into the Dockerfile, so
  a directory that is not a plain relative path (`[A-Za-z0-9._/-]`, nothing above the root) or a script
  that is not a plain file name is `recipe_unsupported`, and the renderer refuses any instruction that
  would span lines.
- **sbt-assembly**: `sbt -batch assembly`; the largest jar under `target/scala-*` is the fat jar and runs
  as `java -jar` on the Alpine JRE.
- **Leiningen**: `lein deps`, `lein uberjar`; `project.clj` must name `:main`, and a `^:skip-aot` main is
  refused unless a profile compiles `:aot :all`. **tools.build**: `clojure -P`, `clojure -T:build uber`
  (or `uberjar`, whichever `build.clj` defines). Both run the largest jar under `target/`.

With neither plugin, or a `deps.edn` with no build, the finding says which to add. Play listens on
`http.port` and writes `RUNNING_PID` where the next container would find it, and its host filter
answers only localhost: the start is `exec /app/bin/<script> -Dconfig.file=/app/conf/just-dashboard.conf
-Dhttp.port=${PORT:-9000} -Dpidfile.path=/dev/null -Dplay.filters.hosts.allowed.0=.`, every flag a plain
system property. Play refuses to start in production while `play.http.secret.key` is its default
`changeme`, and neither its `reference.conf` nor a stock `application.conf` reads any variable for it.
The image therefore writes `/app/conf/just-dashboard.conf` — `include "application"` (the application's
own configuration, from beside it or the classpath) and `play.http.secret.key =
${?APPLICATION_SECRET}` — and the start loads it, so `APPLICATION_SECRET`, generated on the server (64 hex
characters, the 256 bits Play 3 asks for), reaches Play through the environment, never the command line.
It takes precedence over a secret `application.conf` reads from another variable. http4s, ZIO HTTP,
Akka and Pekko HTTP, Ring, Compojure, http-kit and Pedestal mark a web service on their conventional
port. The drivers sbt, Leiningen and `deps.edn` name are database
suggestions like a pom's. The readiness budget is a JVM's.

## Dart and Gleam

A `pubspec.yaml` without Flutter is compiled ahead of time by the Dart recipe
(`deploy/frameworks_dart.go`) on the official `dart:<release>` image — the newest of 3.9 to 3.13 that
both the pubspec's `environment: sdk:` and the lock's resolved SDK range allow — and the executable runs
on `debian:trixie-slim` (with `ca-certificates`) as the `app` user: an AOT executable needs only glibc,
and a shell keeps the start command and the release tasks working, which the official image's
scratch-based `/runtime` would not. The install is `dart pub get --enforce-lockfile` with a committed
`pubspec.lock`. A Dart Frog project (`dart_frog` and a `routes/` directory) is built with
`dart_frog_cli` 1.2.14 (`dart_frog build`, then `dart compile exe` of the server it writes, its
`public/` copied beside it); a shelf or `dart:io` server compiles `bin/server.dart`, `bin/<name>.dart`
or `bin/main.dart`. Both listen on PORT, 8080 by default. A pub mirror given as a `PUB_HOSTED_URL` build
value mapped to the install keeps the locked versions without `--enforce-lockfile`, which refuses a
pub.dev lock resolved against another host. A custom build command must write `/out/server`.

A `gleam.toml` is built by the Gleam recipe (`deploy/frameworks_gleam.go`) into an Erlang shipment
(`gleam deps download`, `gleam export erlang-shipment`) with the Gleam project's own
`ghcr.io/gleam-lang/gleam:v1.18.1-erlang-alpine`, and the shipment runs on that same image, whose
Erlang it was compiled for, as `/app/entrypoint.sh run`. A `gleam = "…"` requirement that 1.18.1 does not
meet, or a JavaScript target, is `recipe_unsupported`. A Mist or Wisp service listens on the literal
`mist.port(N)` its source names, on the PORT it reads and passes to `mist.port` (its `unwrap` default,
else 8000), or on mist's own 4000 when nothing calls `mist.port`. mist 3 and later listen on
`localhost` unless the builder calls `mist.bind`, and the Wisp examples do not: a module under `src/`
that builds a mist server with no `mist.bind` anywhere is a certain loopback listen fact, so preflight's
`listen_loopback` blocks with the fix (`|> mist.bind("0.0.0.0")`) before Deploy.

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
   routes, `@GetMapping("/health")`, `MapGet("/health")`). PocketBase's `/api/health` is declared by
   the framework, so it is probed expecting a 2xx. A router can mount it under a prefix
   detection cannot see, so it is probed accepting any answer; a JVM route is put under the context
   path the configuration sets.
5. The convention: a page framework (Next.js, Nuxt, SvelteKit, Astro, Remix, React Router, Angular SSR,
   Solid/TanStack Start, Streamlit, Gradio, Flask routing `/`, Laravel, Fresh, plain PHP) is asked for a
   2xx at `/`; everything else — Express/Fastify/Hono/Koa/Elysia/hapi and unknown Node servers, Nest,
   FastAPI, Go, Rust, JVM (Scala and Clojure included), .NET, Deno, Symfony, Slim, a Rails API or a Rails
   application without `/up`, Hanami, Sinatra and Rack, Phoenix and Plug, Dart and Gleam — accepts any
   answer from `/`, and so
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

Budgets follow the start: 20 attempts 3 s apart by default; 40 for a JVM service (Java, Scala, Clojure) or a start command
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
`/data` for the recipes that run as root (Node, Python, PHP, Ruby), `/home/app/data` for Go and Rust and
`/app/data` for .NET, Elixir, Gleam, Dart, Scala and Clojure, which those recipes create owned by their
unprivileged user. An image built by its
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
| A Phoenix release with `ecto_sqlite3`, built by the Elixir recipe or its own Dockerfile | the file `runtime.exs` reads from `DATABASE_PATH` | `/app/data/<app>.db` through it for the recipe; `/data/<app>.db` for a Dockerfile image that runs as root; reported under `USER nobody` |
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
`deno install`, `bundle install` and its `bundle lock --add-platform`, `mix deps.get` and
`deps.compile`, `dart pub get`, `gleam deps download`, `sbt -batch update`, `lein deps`, `clojure -P`,
an `apk add`) is **install**; the plan's build command, or the recipe's default build
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
| `build_lockfile_out_of_sync` | npm `EUSAGE … are in sync` (subjects from `Missing:`/`Invalid:`), Bun `lockfile had changes, but lockfile is frozen`, `ERR_PNPM_OUTDATED_LOCKFILE`, Yarn `YN0028` and Yarn 1 `--frozen-lockfile`, Poetry `changed significantly`, uv `--locked`, Pipenv `Your Pipfile.lock (…) is out of date`, Cargo `--locked was passed`, Go `missing go.sum entry` / `updates to go.mod needed` / `inconsistent vendoring`, Composer lock errors, Deno `The lockfile is out of date`, Bundler deployment mode | the package manager whose lockfile the detected candidate reads as in sync |
| `build_lockfile_incompatible` | pnpm `ERR_PNPM_LOCKFILE_BREAKING_CHANGE`/`BROKEN_LOCKFILE`, Cargo lock version, Poetry/uv lock format, Bun lockfile version | — |
| `build_package_manager_mismatch` | corepack `This project is configured to use X`, `ERR_PNPM_BAD_PM_VERSION` | the manager `packageManager` declares |
| `build_lifecycle_script_blocked` | `ERR_PNPM_IGNORED_BUILDS`, Bun `Blocked N postinstalls` with a consequence | — |
| `build_runtime_version` | EBADENGINE, `ERR_PNPM_UNSUPPORTED_ENGINE`, Yarn/Next engine lines, Go `GOTOOLCHAIN=local`, rustc `or newer`, Maven release, Gradle class version, a Gradle toolchain no JDK matches, NETSDK1045, `A compatible .NET SDK was not found`, NETSDK1005 (a referenced project restored for another framework than it builds for), Composer `requires php`, pip `requires a different Python`, uv/Poetry Python requirement, Ruby/Elixir/Hugo versions | a Go, Python, Java or .NET release the recipe offers |
| `build_env_missing` | PrismaConfigEnvError, P1012, t3-env, SvelteKit `$env/static`, Astro, Rails `secret_key_base`, Phoenix, Django, `KeyError` on the environment | the variable, or its build scope (recipes only) |
| `build_sqlx_offline` | sqlx `set DATABASE_URL to use query macros` / no cached data | `SQLX_OFFLINE=true` for the build |
| `build_database_unreachable`, `build_prerender_failed` | Next prerender/collect-page-data errors, with or without a database error; `Can't reach database server`; a `*.jd.internal` address that does not resolve (a linked database is reachable only on the project network, which a build is not on); Django `OperationalError` | — |
| `build_prisma_client_missing` | `@prisma/client did not initialize yet` | `prisma generate` before the build command, unless the recipe already runs it (the `prisma` CLI is a dependency); otherwise the sentence says to add the CLI |
| `build_platform_binary_missing` | rollup/esbuild/SWC/lightningcss/oxide/sharp Linux binaries missing | — |
| `build_legacy_openssl` | `0308010C`, `ERR_OSSL_EVP_UNSUPPORTED` | `NODE_OPTIONS=--openssl-legacy-provider` |
| `build_system_library_missing`, `build_native_toolchain_missing` | `pg_config`, `mysql_config`, pkg-config, `cannot find -l`, `*-sys` crates, bindgen's `Unable to find libclang`, headers, Prisma libssl, glibc on musl, Gradle's `SDK location not found` (an Android module); `gyp ERR!`, a missing compiler (a shell's `make: not found` only with exit 127 or a wrapper reporting 127), `Failed building wheel`, cgo the recipe did not detect, `linking with cc`, `protoc`, `` is `cmake` not installed ``, perl, NativeAOT's clang | for the Python recipe, the Debian packages to add to `build.systemPackages` (`pg_config` → gcc libc6-dev libpq-dev, a compiler → build-essential) |
| `build_install_script_failed` | npm `error path /app/node_modules/X` with `command failed`, Yarn `YN0009` | — |
| `build_php_extension_missing` | `requires ext-X … it is missing from your system` | — |
| `build_dependency_conflict`, `build_dependency_unavailable`, `build_dependency_local_path`, `build_dependency_os_only`, `build_dependency_advisory_blocked` | ERESOLVE, `ResolutionImpossible`, Composer/uv/Cargo/NuGet conflicts; ETARGET/E404, `No matching distribution`, NU1101, Maven artifacts, Go revisions and the proxy's or checksum database's 404/410 for a private module (the sentence names `GIT_TOKEN`), gems; conda `/croot/` paths, Go's `replacement directory … does not exist`; a Windows or macOS package from a `pip freeze` (`pywin32`, `pywinpty`, `pyobjc-*`); Composer advisories | — |
| `build_registry_auth`, `build_registry_rate_limited`, `build_network` | E401/E403, `YN0041`, `terminal prompts disabled` (from the go command's git fetch: a private module without `GIT_TOKEN`), NuGet `NU1301` with 401/403, a Maven or Gradle repository's `status code: 401` (read before the dependency it could not resolve); `toomanyrequests`; DNS, TLS and connection failures | — |
| `build_command_not_found`, `build_script_missing` | `sh: X: not found` when the step exited 127 or a wrapper reports that status (`exit code 127`, `exited (127)`) — a caught probe prints the same line and carries on —, `executable file not found`, pip's `Cannot find command 'git'`, Laravel Wayfinder's `php artisan wayfinder:generate` in an asset stage without PHP; npm/pnpm/Bun/Yarn missing script | the build command with its runner moved to the image's package manager (the install planner's own rewrite, `nodeRunnerFor`), read from the install the build recorded |
| `build_module_not_found`, `build_type_error`, `build_compile_error` | `Cannot find module`, `Can't resolve`, `No module named`, `no required module provides`; `Type error:`, `error TS…`; rustc, C#, javac/Kotlin, Go, Maven, Gradle, bundler and framework compile errors | — |
| `build_output_missing`, `build_copy_source_missing`, `build_embed_source_missing`, `build_wrong_root` | the recipe's own guard, a missing `COPY` source, `go:embed` without files, a manifest the build cannot find, a module a `go.work` does not list | the detected output directory, else the field to review |
| `build_out_of_memory`, `build_disk_full`, `build_timeout` | heap limits, exit 137 (which points at no line: the step's output only shows where it was), `OutOfMemoryError`; `no space left on device`; the 30-minute limit | `NODE_OPTIONS=--max-old-space-size=` three quarters of the server's memory (from 1 GiB, at most 8 GiB) |
| `build_permission`, `build_script_crlf`, `build_wrapper_missing`, `build_dev_dependency_in_production`, `build_bundle_platform_missing`, `build_hugo_extended_required`, `build_base_image_missing`, `build_platform_unsupported`, `build_dockerfile_invalid` | exit 126, `\r` interpreters, the Gradle/Maven wrapper, Symfony dev bundles and Telescope, a Gemfile.lock without Linux, Hugo Pipes' Sass, a FROM that does not resolve, a manifest for another platform, a Dockerfile that does not parse | — |

The language recipes' own failures take the same codes: `mix.lock` refused by `--check-locked` and a
`pubspec.lock` `--enforce-lockfile` cannot satisfy are `build_lockfile_out_of_sync`; a Dart SDK or Gleam
release the project requires is `build_runtime_version`; Rails' `Missing encryption key to decrypt file
with` during the precompile is `build_env_missing` naming `RAILS_MASTER_KEY`; a gem's native extension
that Bundler cannot build (`An error occurred while installing pg …`, mkmf's missing header) is
`build_install_script_failed` or `build_system_library_missing`; Bundler's, Hex's and Gleam's
resolution failures are `build_dependency_conflict`, a Hex package, a pub package or an sbt artifact
that does not exist `build_dependency_unavailable`; and Elixir's `== Compilation error in file …`,
scalac's `[error] …scala:N`, Clojure's `Syntax error compiling at (…clj:N)`, Dart's `file.dart:N:M:
Error:` and Gleam's `┌─ file.gleam:N` are `build_compile_error` naming the file. A command of a language
another recipe builds (`bundle: not found` in a Node image) names that recipe as the builder to choose.

Nothing matched is `build_failed`, still with the phase, command and exit code. A Dockerfile build
never gets a variable fix, because a custom Dockerfile cannot take build secrets.

## Verification

`TestLiveDetectedFrameworkBuildAndServing` builds locked Next.js (Bun, and pnpm started through `pnpm run
start`), an Express server installed and started by Yarn 1, Vite, SvelteKit Node/static, plain HTML,
Containerfile and Go fixtures through detection and the real artifact/runtime owners, and the catalogue's
own starters: Astro 7 (static), Nuxt 4 and React Router 8 (servers), FastAPI on an unpinned
`requirements.txt` with no server declared, a Flask factory on a bare `pyproject.toml`, a Django project
whose first request reads its migrated table, a Streamlit script (its health endpoint and a file served
through its own static-serving setting) and a Gradio app (the value in the page's embedded config), the
Python install shapes (a Litestar app on a `pdm.lock` started by its `[tool.pdm.scripts]` task, Flask on a
`Pipfile.lock`, FastAPI on a `uv.lock` that asks for Python 3.14 and answers from the image's own
interpreter, a Django project created inside the repository with a `requirements/` folder, split settings
run through the seeded `DJANGO_SETTINGS_MODULE` and psycopg2 compiled against the libpq the recipe
installs, and a Flask app whose JavaScript a Node stage bundles), an
axum service, a Maven jar and a Gradle jar, a Spring Boot module of a Maven reactor on Java 17 and an
application-plugin project of a multi-project Gradle build with a version catalog (each built from its
build's root), a Gradle build whose settings root is below the checkout's and includes a build beside it,
an ASP.NET Core minimal API, a web project in a solution's `src/` with shared props, central package
versions and a `global.json` pin, a web project with two target frameworks that references a library with
one, a Blazor WebAssembly app served as static files, an F# minimal API,
an ASP.NET Core project whose publish runs npm for its front end, a Deno server, a minimal Laravel
12 application (migrated, with the form's generated `APP_KEY`), a plain `index.php`, a Rails 8
application whose `Gemfile.lock` was resolved on a Mac (the recipe adds the Linux platform, precompiles
with Propshaft and answers through the table `db:prepare` migrated, with a Propshaft asset fetched), a
Sinatra app behind Puma, a Phoenix 1.8 release, a Play 3 application staged by sbt with the seed's stock
`application.conf` (its secret reaching Play only through the configuration the image writes), a
Leiningen uberjar on Ring and a Gleam Mist server. The JVM and .NET
fixtures are also detected and prepared without Docker (`TestCompiledLiveFixturesDetectAndPrepare`). The
Go and Rust shapes beyond one module or crate are built from their own fixtures too: a `go.work` member
serving a template it reads at runtime with a value from its sibling module, which it requires at `v0.0.0`
as a workspace member does; a Go server embedding its Vite build,
answering with cgo SQLite's version, a `Europe/Bucharest` zone and a generated templ component; a Cargo
workspace member beside a maintenance binary; a Leptos (cargo-leptos) application with `hash-files`, its
site's hashed scripts fetched; and a Trunk (yew) site, its WebAssembly module fetched. The WebAssembly builds install their tool
from source and take several minutes each, so run them with `-timeout 90m`.
`TestLiveGoRecipeCatalogueResolves` resolves every Go family's image, the Alpine pair a dynamically linked
Go build uses, and the Debian images of the Leptos and Trunk builds, and
`TestLiveRecipeBaseCatalogueRunsOnAmd64AndArm64` every catalogue image with the Java and .NET releases'
own, each for amd64 and arm64. It checks
readiness and served values without supplying build values at runtime; recipe fixtures also inspect logs,
metadata and saved image layers for private install credentials. The Go fixture proves generated code,
custom startup and the selected toolchain through its HTTP response. These local adapter journeys
complement the production-build browser gate; they do not constitute public provider/DNS/TLS or clean-VM
acceptance. The remaining catalogue entries are covered by rendered-Dockerfile and detection tests
(`frameworks_*_test.go`, `build_recipes_test.go`); for Python, `frameworks_python_catalogue_test.go`,
`build_python_test.go`, `detect_python_manifests_test.go` and `preflight_python_test.go`, and every other
framework's detected start command (Starlette, Sanic, Quart, Falcon, Bottle, aiohttp, Tornado, Dash, Panel,
Chainlit, NiceGUI, Mesop, Channels under Daphne), the setup.py, conda, freeze-leftover and uv workspace
installs, and the Streamlit secrets step were built and served locally when they were written; the Go and
Rust builds beyond a single module or crate — toolchain families, cgo, vendoring, `go.sum`, private modules,
templ, embedded front ends, workspaces and replacements, command-line subcommands, Cargo workspaces, binary
selection, native crates, sqlx, the lockfile, Leptos, Trunk, Dioxus and Shuttle — by `build_go_test.go`,
`build_go_recipe_test.go` and `build_rust_test.go`, and their failure signatures by
`build_output_cause_compiled_test.go`; the JVM and .NET readers, toolchain plans, packagings, layouts and
registry credentials in `frameworks_jvm_test.go`, `frameworks_dotnet_solution_test.go` and
`detect_registry_credentials_test.go` — a Gradle 8.4 wrapper with a Java 21 toolchain (Gradle on JDK 17,
the toolchain provided beside it, a CRLF `gradlew`), a Quarkus fast-jar on a bridged PORT and a
`PublishAot` web API published as the JIT dll were built and served locally when they were written; the
language recipes' detection, versions, rendered Dockerfiles, preflight findings and failure signatures are
in `frameworks_languages_test.go` and `preflight_languages_test.go`. Rails with jsbundling on Yarn, Hanami
with its npm assets, a Phoenix release migrating `ecto_sqlite3` at start, a plain Plug service on an exact
Elixir and OTP pin, a tools.build uberjar, an sbt-assembly jar, and Dart Frog and shelf servers were built
and served through the same owners when the recipes were written; the Dart ones are not live fixtures
because pub.dev refuses this repository's CI host, and were built there through a pub mirror given as
`PUB_HOSTED_URL`.
Persistent state, schema tools, seeds and their
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
from every declaration, and the system packages each dependency adds), `build_node_prisma_test.go`
(`prisma generate` and the `env()` placeholders, the incident repository with Prisma 7's
`prisma.config.ts` included), `build_node_build_test.go` (legacy OpenSSL, T3 Env, `--env-file`)
and `preflight_build_test.go`; the rendered Dockerfiles for canvas on Alpine, a GitHub dependency,
onnxruntime-node on Debian slim, Puppeteer with Alpine's Chromium, Prisma 7 on npm and on Bun with Node
24, and a webpack-4-era build were built and run locally when they were written.
