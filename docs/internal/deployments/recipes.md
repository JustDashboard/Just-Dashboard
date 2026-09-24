# Automatic recipes and serving defaults

`just-dashboard-recipes-v2` prepares immutable Dockerfiles using digest-pinned catalogue bases. Build
commands execute inside the build container; source inspection never executes repository configuration
on the host. Generated Dockerfiles use root-relative, exclusive writes so a checkout symlink cannot
redirect output outside the build context. Detection ignores this generated directory.

Detection reads manifests as data — `package.json`, `angular.json`, `requirements.txt`, `pyproject.toml`,
`uv.lock`, `poetry.lock`, `Cargo.toml`, `pom.xml`, `build.gradle(.kts)`, `*.csproj`, `deno.json(c)`, a
`Procfile`, and for a JavaScript package its lockfiles, `.npmrc`, `.yarnrc.yml`, `bunfig.toml`,
`pnpm-workspace.yaml` and the Node and Bun version files (`.nvmrc`, `.node-version`, `.tool-versions`,
`.bun-version`) — and names a candidate per root with the framework, the build and start commands, the port,
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
framework default alike. Prisma (`*.prisma` at the root or under `prisma/`, or where its configuration
declares its schema) runs `prisma migrate deploy` when a `migration.sql` is committed and `prisma db push`
otherwise; Drizzle (`drizzle.config.*`) runs `drizzle-kit migrate` with a
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
(`build_node_lockfile.go`): a dependency missing from the lock is drift for every manager; one removed
from `package.json` but still locked is drift for Bun, pnpm and Yarn, while `npm ci` leaves it out and
installs, so npm's reading stays `in_sync` and lists it under `extra`; pnpm and Yarn also compare the range
text, while npm and Bun accept a changed range the locked version still satisfies (npm's range grammar is
evaluated as data, `node_semver.go`). Yarn 1 is read from its entry headers, Berry from its workspace
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
script — unless the variable is mapped to `install_and_build` (`prisma_config_env`).

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
| `build_env_validation_skipped` (pass; warning when a skipped variable has no value at all) | the schema's required server variables have no build value and the schema honours `SKIP_ENV_VALIDATION` |
| `build_env_missing` (warning) | the same, and the schema cannot be skipped, so the build stops with "Invalid environment variables" |
| `build_env_client_missing` (warning) | a `client` variable has no build value; the browser bundle gets `undefined` |
| `build_database_unreachable` (warning) | Next.js, Nuxt, Astro, SvelteKit or Gatsby with a database client, and a build-scoped URL pointing at `db-N.jd.internal`, at loopback, or a linked database reference: the build runs apart from the environment's network, so a prerendered page that queries it fails; the action is rendering those pages on request |
| `port_variable_mismatch` (warning) | a `PORT` variable differs from the internal port the proxy and readiness check use |
| `node_env_not_production` (warning) | `NODE_ENV` other than `production` reaches the build or the server |
| `host_variable_loopback` (warning) | `HOST` or `HOSTNAME` on loopback, which frameworks bind to |

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
raises `lockfile_out_of_sync` before Deploy and installs unfrozen instead of failing `npm ci`. The image
and the build around the install are covered by `build_node_runtime_test.go` (the Node and Bun release
from every declaration, and the system packages each dependency adds), `build_node_prisma_test.go`
(`prisma generate` and the `env()` placeholders, the incident repository with Prisma 7's
`prisma.config.ts` included), `build_node_build_test.go` (legacy OpenSSL, T3 Env, `--env-file`)
and `preflight_build_test.go`; the rendered Dockerfiles for canvas on Alpine, a GitHub dependency,
onnxruntime-node on Debian slim, Puppeteer with Alpine's Chromium, Prisma 7 on npm and on Bun with Node
24, and a webpack-4-era build were built and run locally when they were written.
