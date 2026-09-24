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
`recipe_unsupported` preflight finding, never a silent guess. Which of a repository's roots is the
application, and what it is when it is not a service, is [repository shape](#repository-shape-and-candidate-selection).

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
a start script, another platform's file and a framework default alike; it is only ever a server command,
so a site framework's build is still served by nginx. Every other process line is recorded as one of the
candidate's [processes](#background-processes): `release:` is not run automatically (preflight's
`release_process_not_run` says so unless the start command already migrates), and a worker, clock or
beat line is a process this project does not run. A command that carries credential material is ignored.

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

Candidates are ranked, and the one that outranks every other is selected (`detect_repo_shape.go`). The
order is: whether it is a service at all; whether something ranks it below the application; its
confidence; between two nested roots, `apps/`, `services/` or `web/` above `packages/`, `libs/` or
`tools/`; and, on a tie, the shallower root — unless that is plain static files, which say least about
what a repository is for. A tie is still the operator's choice. A candidate ranked down is chosen on its
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
| health check path | evidence and `healthPath`; the readiness check does not use it yet |
| `generateValue`, `generator: "secret"` | a variable the configure form generates |
| secret env names, `sync: false`, `required` | a variable row that must be set |
| accessories, add-ons, `fromDatabase`, `databases:` | a database suggestion on the declared variable |
| worker, cron, Kamal roles, `[processes]` | [processes](#background-processes) |
| release command (`release_command`, `preDeployCommand`, `PRE_DEPLOY` job) | a `release` process |
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
project. Octane is recorded as an alternative start
command, not a failure.

## Ecosystems without a recipe

A root the builder has no recipe for is still named (`detect_ecosystems.go`), with a low-confidence
candidate whose framework is the language or framework and whose `recipeIssue` says what to commit —
which preflight's `recipe_unsupported` shows instead of "No deployable plan was detected". Rails
(`rails new` has generated a production Dockerfile since 7.1; older applications use `dockerfile-rails`),
Hanami, Sinatra and Rack, Jekyll and Middleman; Phoenix (`mix phx.gen.release --docker`) and Elixir;
Crystal, Haskell, Zig, Swift, Scala, Clojure, Gleam, F#, OCaml, Nim, Perl, Erlang, Dart, R Shiny and
Plumber, C/C++ (CMake, Meson), Elm, Hugo, MkDocs and a Flutter web app. A web framework among the
manifest's dependencies makes it a web service on its conventional port. A Rails, Hanami or Phoenix
application owns its `package.json` (and `assets/package.json`): that asset pipeline is set aside rather
than offered as a Node service — a CocoaPods or fastlane Gemfile beside a React Native app owns nothing.
A site generator (Hugo, MkDocs, Jekyll, Middleman, Elm) owns a tooling-only `package.json` at its root
the same way: a Hugo site with a Tailwind build used to be only a Node worker asking for a start command.
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
runtime whose `public/build` is copied in. Laravel's start runs `php artisan migrate --force` first;
Symfony's runs `doctrine:migrations:migrate` when the migrations bundle is present. `storage/`,
`bootstrap/cache` and `database/` are created writable so a first start on an empty volume works. With
`laravel/horizon` the image also carries `pcntl` and `redis`, which Horizon's own manifest requires. The
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
(`frameworks_*_test.go`, `build_recipes_test.go`). Repository shape — ranking, decoys, static roots,
split repositories, shapes that are not services, ecosystems without a recipe, processes, other
platforms' files, submodules and LFS, case-mismatched imports and the preflight findings they raise — is
covered by `detect_*_test.go` and `preflight_repo_shape_test.go` against written fixtures.
