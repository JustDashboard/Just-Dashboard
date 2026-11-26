# Smart stacks

A smart stack is several applications deployed as one Compose template and then set up and connected
to each other by the dashboard, so the person does not open six web interfaces to copy API keys
between them. The first is the media server stack (Jellyfin, Seerr, Radarr, Sonarr, Prowlarr,
qBittorrent, Bazarr, Recyclarr). It is offered on the **Stacks** tab of `/deploy/new` and reported
on a project's **Stack** page (`/deploy/[id]/stack`).

## What is new and what is not

Nothing about deploying is new. A stack's preset is a reviewed built-in Compose template
(`blueprint/builtin/media-complete.json`, `media-essential.json`) marked `"stack": "media"`, and it
deploys as `kind: compose, mode: blueprint` through the draft, commit and run path every Compose
template uses ([Multi-service templates](implementation.md)). Building, starting, networks, volumes,
the domain, logs, backups, alerts and rollback are the deployment's.

What is new is what happens after a release goes live: `internal/stacks` runs a reviewed graph of
configuration steps against the live release's containers and records each step's outcome.

| Piece | Where |
|---|---|
| Step, result, action and environment contracts | `stacks/stacks.go` |
| Manifest (presets, services, options, credentials, steps) and its validation | `stacks/manifest.go` |
| The registry of shipped stacks, held to their templates by a test | `stacks/catalog/` |
| The media stack: constants, presets and the whole step graph | `stacks/media/media.go` |
| The application adapters | `stacks/media/<application>.go` |
| The runner (order, concurrency, retries, actions, redaction) | `stacks/runner.go` |
| When a pass runs; the environment it runs in | `stacks/service.go`, `stacks/dockerenv.go` |
| Tables | `store/schema_deploy_stacks.go`, `stacks/store.go` |
| Routes and the state the page reads | `api/handlers_deploy_stacks.go` |
| Test helpers (fake over httptest; DockerEnv over a test's own containers) | `stacks/stackstest/` |

## The templates

- **Complete** (eight services, the Compose template cap) and **Essential** (Jellyfin and Seerr).
  Images are pinned to exact upstream releases (linuxserver.io builds for the managers, the
  download client and Bazarr; Jellyfin's and Seerr's own images; Recyclarr's from Docker Hub).
- **Jellyfin is the public service**: the deployment's domain routes to it, as any template's
  public service. Every other web interface uses the `loopback` port exposure, which only a stack
  template may declare: published on `127.0.0.1` at a preference derived from the template, service
  and port (`blueprint.LoopbackHostPort`), moved by the Compose port recovery every published port
  already has (`dockerx/compose_ports.go`) when taken, so two stacks on one server do not collide.
  Nothing but Caddy binds a routable address (invariant 1). A service with a loopback port must
  declare `egress`: Docker publishes no port of a container that is only on an internal network.
  To reach one from elsewhere the person
  uses an SSH tunnel or publishes it through a site of their own.
- **One shared volume, `media`, at `/data`**: downloads and the library on one filesystem so a
  finished download is renamed into the library, not copied. Writers (qBittorrent, Radarr, Sonarr,
  Bazarr) mount it read-write; Jellyfin mounts it **read-only** in Complete. The validator allows a
  read-only mount of a data volume only when another service of the stack writes it.
- The linuxserver.io images run their application as `PUID`/`PGID` 1000; the folders the stack makes
  on the volume are given to that account and writes are checked as it.
- Every private service declares `egress`: the managers, Seerr and Recyclarr reach metadata services
  and the person's sources.
- **API keys** for Radarr, Sonarr and Prowlarr are template secrets (`RADARR_API_KEY`, …), generated
  at commit and handed to the applications through their `<APP>__AUTH__APIKEY` environment, and to
  Recyclarr. No key is ever copied by hand or by the person.
- **Options** are template inputs that become deployment variables: `MEDIA_QUALITY` (720p, 1080p,
  2160p), `MEDIA_SUBTITLE_LANGUAGES`, `MEDIA_REQUEST_APPROVAL` (`admin` or `auto`). Changing one is
  changing a variable and redeploying; the pass after the new release applies it.
- An added template may not claim `stack` (`validateUserRules`): the adapters trust the services to
  be the images they were written and tested against.

## When a pass runs

`stacks.Service` looks at every stack every ten seconds and at once after a commit or a Retry
(`Kick`). A pass runs when the environment's live release is a release of one of the stack's preset
templates and either the release is not the one last configured, the manifest version changed, or
a pass was requested. At most two stacks are configured at once, one pass per stack. A pass for a
release that stopped being live is cancelled, and so is one for a deployment that was removed; one a
restart cut short is queued again (`ResetInterrupted`). Changing the source to the other preset's template moves the stack to that
preset. A deployment whose source is no longer a preset is left alone, and Retry on it is refused
(`stack_not_preset`) rather than recorded for a pass that would never run.

The stack is registered in the commit request (`registerStack`, after the blueprint schedules), keyed
by project; its rows cascade with the project's.

## The runner

- A step runs when every step in its `After` is done, or skipped because its service is not in the
  preset (such steps are left out of the page). A step after one that failed, needs the person, or
  was itself held back is skipped with a sentence naming which.
- Three steps at once. Each attempt is bounded by the step's timeout; up to its attempts with
  5, 10, 20, 40, 60 s between them. `stacks.Permanent` stops retrying; `stacks.NeedsAction` records
  `action_required` with an action the page lists. A panic in an adapter fails that step only.
- Every secret the environment handed out (variables, credentials) is masked out of each recorded
  message and error before it is written, so an application echoing a request back cannot put a key
  in the step's record.
- Steps converge: each reads what is there, changes only what differs from what the stack wants,
  reads it back, and finds its own entries by a fixed name. Running a pass twice makes no second
  change, so Retry and every redeploy simply run the whole graph again.

## Reaching the containers

The backend runs on the host's network and reaches each container by its address on
`<compose project>_internal`, read from Docker on every call (`DockerEnv.BaseURL`) — the way the
data-rollback copy is reached. Addresses written into one application for another are Compose DNS
names (`http://radarr:7878`). The HTTP client takes no proxy from the environment and follows no
redirect to another host. Commands inside a container (`ExecCapture`) are closed argv built by the
adapters, never a shell string from a request.

## Credentials

Accounts the stack creates (Jellyfin's administrator, which is also Seerr's owner; qBittorrent;
Radarr, Sonarr, Prowlarr, Bazarr) are generated on first use (24 letters and digits) and sealed in
`deploy_stack_credentials` with the dashboard's sealer. Tokens an application hands back (Jellyfin's
and Seerr's API keys, Bazarr's) are kept there too and are never revealed. The state lists the
accounts with `stored`; `GET /deploy/{id}/stack/credentials/{name}/reveal` returns one on the terms a
variable reveal has — an administrator, in a session, not through Just Hub, audited without the
value. Nothing in a response, a step record or a log carries a value otherwise.

## Routes

| Route | Who | What |
|---|---|---|
| `GET /deploy/stacks` | any signed-in account, any key | the catalogue: presets with their template, memory and disk estimate, limitations, services |
| `GET /deploy/{id}/stack` | whoever reads the project | state, readiness, services with container state, a private one's loopback address and, once live, the public one's URL (the deployment's endpoint), steps, actions, accounts; `404 stack_not_found` otherwise |
| `POST /deploy/{id}/stack/configure` | the deploy control capability | run a pass now; `409 stack_waiting` before a live release, `409 stack_busy` during a pass, `409 stack_not_preset` when the live release is not one of the stack's presets (no pass would ever run); audited |
| `GET /deploy/{id}/stack/credentials/{name}/reveal` | administrator, session, not Hub | one account's password; audited |

Blueprint summaries and details carry `stack`; the Template tab leaves those templates out.

## What the page may claim

`status` is `waiting` (no live release), `configuring`, `ready`, `action_required` (done but for what
only the person can do) or `failed`. `readiness` separates installed, healthy (every container
running and healthy), connected (every link step done), able to play (Jellyfin set up with its
libraries) and able to acquire (Complete, connected, the download link verified and at least one
source in Prowlarr). A Complete stack with no source is `action_required: Add a media source` — never
"set up" — and still plays what is in its library.

## What the manager, subtitle and quality steps make

What these steps own in each application, by the name they find it again with. Anything under
another name is the person's and is not read past its name.

| Application | The stack's own | Left alone |
|---|---|---|
| Radarr, Sonarr, Prowlarr | the sign-in: a form on every address, user `admin`, the stack's password (`config/host`; the whole resource goes back with four fields changed) | every other host setting |
| Radarr, Sonarr | the root folder `/data/media/movies` or `/data/media/tv` | other root folders |
| Radarr, Sonarr | the download client **qBittorrent (Just Dashboard)**: host `qbittorrent`, port, user, password, category `movies` or `tv`, enabled | other clients; every other setting of the stack's own client (priority, removal, tags) |
| Prowlarr | the applications **Radarr (Just Dashboard)** and **Sonarr (Just Dashboard)**: URLs, key, full sync | other applications; the sync categories, which come from Prowlarr's own schema |
| Bazarr | its sign-in; Radarr and Sonarr switched on with host, port and key; the languages profile **Just Dashboard** | other profiles and enabled languages (saving either replaces the list, so the person's go back with the stack's); a default profile the person chose; subtitle providers, which need the person's own account and are never added |
| Recyclarr | `/config/recyclarr.yml`, written again on every pass | files under `/config/configs/` |

- A new download client or application starts from the application's own schema, so a setting the
  stack does not name keeps the application's default. The application's own test runs before every
  save. A secret is never read back (the applications answer `********`), so the only way to know a
  stored one is still right is that same test, run on the entry as stored.
- An account whose password was changed in the application is given the stack's password back: it is
  the one the dashboard reveals for that application.
- **Quality profiles.** Recyclarr creates the TRaSH Guides' profile for the chosen quality under a
  name that carries the stack's mark, because Recyclarr 8 takes over any profile or custom format
  that already has the name it is about to use and has no setting to stop it. A person's own profile
  never has the marked name; a person's own custom format that has exactly a guide format's name
  would be taken over.

  | `MEDIA_QUALITY` | Radarr | Sonarr |
  |---|---|---|
  | `1080p` | HD Bluray + WEB (Just Dashboard) | WEB-1080p (Just Dashboard) |
  | `2160p` | UHD Bluray + WEB (Just Dashboard) | WEB-2160p (Just Dashboard) |
  | `720p` | the built-in HD-720p | the built-in HD-720p |

  The guides have no 720p profile. For 720p Recyclarr keeps only the quality sizes and the
  applications' own HD-720p profile is used as it is; the step says so. Changing the quality leaves
  the earlier profile and its custom formats in place (`delete_old_custom_formats` is off, and a
  sync deletes no profile). `reset_unmatched_scores` is off, so a score the person gives a custom
  format of their own in the stack's profile stays. The API keys are not in the file: Recyclarr
  reads them from its environment (`!env_var`).
- `DockerEnv.Exec` gives a command what is left of its step's attempt. The sync is still run for
  Radarr and Sonarr separately, and an attempt first waits for any sync already running in the
  container (the image's daily one, or an earlier attempt cut short) rather than starting a second.
- `downloads.verify` has Radarr and Sonarr run their health checks now (the `CheckHealth` command)
  before reading them, since `GET health` otherwise answers from the last scheduled run. It fails on
  an error about the download client or its folders, names anything else, and leaves the checks
  about indexers to `prowlarr.sources`.
- `prowlarr.sources` counts the sources that are switched on. None is the blocking action
  `media-source`; the step never adds one.

## What Jellyfin, qBittorrent and Seerr steps make

- **Folders** (`storage.folders`): the four library and download folders and their two parents are
  made as root in qBittorrent's container (Jellyfin's in Essential) and given to 1000:1000 only when
  missing or root-owned, never recursively; every writer then proves it can create a file there as
  the account it runs as, and Jellyfin in Complete that it can read the library.
- **Jellyfin**: the first-run wizard is finished through its Startup API with `admin` and the stack's
  password, and an API key named "Just Dashboard" is made once and kept. Libraries are matched by
  path, since Jellyfin 12.4 makes "Movies2" rather than refusing a taken name. A Jellyfin whose
  wizard someone else finished, and which does not let the stack's account in, is the blocking action
  `jellyfin-owner`; nothing is reset.
- **qBittorrent**: the temporary password the linuxserver.io image prints on first start is read
  from its log once and replaced with the stack's; the stored one is then checked offline against
  the PBKDF2 value in its settings file before a single sign-in, because five refused sign-ins lock
  the address out for an hour. A password that is not the stack's is the action
  `qbittorrent-password`, tried no further. Categories `movies` and `tv` with their folders need
  automatic torrent management, which is turned on with the save path.
- **Seerr** is signed in to the stack's Jellyfin as `admin`, which makes that account Seerr's owner,
  and its Movies and Shows libraries are turned on by Jellyfin's ids. The stack's password is sent
  only to a Seerr the stack set up itself: it records that it began Seerr's setup before the request
  that does it, and a Seerr already set up by anyone else is the action `seerr-owner` with no
  sign-in, since which Jellyfin that Seerr would pass the password to cannot be read without it.
  Radarr and Sonarr are added as "Radarr (Just Dashboard)" and "Sonarr (Just Dashboard)" with the
  quality profile Recyclarr reported and the library folder; an update carries the rest of the entry
  as the person left it. The request policy changes only the auto-approve bits of the default
  permissions, for accounts made from then on.

## Verified end to end

On 26 Nov 2026 both presets were deployed through the API exactly as the Stacks tab does (draft,
source, detect, configuration, preflight, commit, run) on a dashboard running inside its own Docker
daemon (Docker-in-Docker, the backend in its network namespace), with nothing touching the host's
containers:

- Essential: the run went live and the pass finished `ready`, 6 of 6 steps on the first attempt;
  the revealed Jellyfin account signed in to Jellyfin 12.4.0 directly as an administrator.
- Complete: all eight containers healthy, every private interface published on loopback (each
  service is also on the `egress` network, which the validator requires of a loopback port: Docker
  publishes nothing from a network declared internal, checked on Docker 29.9), 21 of 22 steps done
  on the first attempt and `action_required: Add a media source`. Read directly from the
  applications afterwards: one "qBittorrent (Just Dashboard)" client on host `qbittorrent` with the
  `movies` category, the library root accessible, the synced profile, Prowlarr's two applications on
  full sync, and Radarr refusing an unauthenticated request.
- Retry on the configured Complete stack: a second pass changed nothing and made no second entry;
  a second press during it answered `stack_busy`.

Not exercised end to end: a request fetched through a real source, imported and played. That needs a
source the tester is allowed to use; none ships, by design.

## Not done yet

Said here so nobody builds on it by mistake:

- Adopting an existing Jellyfin or Seerr deployment into a stack. The Stacks tab offers fresh
  installs only.
- Mounting an existing library folder from the host; media goes into the stack's own volume.
- Hardware transcoding, the Ultimate preset's optional modules, and a guided form for adding a media
  source (it is added in Prowlarr itself, and subtitle providers in Bazarr).
- Jellyfin is reachable at the deployment's domain from the moment the release is live, and its
  wizard is finished by the stack within seconds of that, as the single Jellyfin template has always
  left it to the first visitor. A deployment without a domain has no such window.
- Stack-level backup and restore beyond the deployment's own volume coverage, and update actions of
  the stack's own: an update is a source change to a newer preset version and a redeploy, after
  which the pass checks every link again.

## Tests

- `go test ./internal/stacks/...` — runner semantics, manifest validation, the catalogue held to its
  templates, each adapter against recorded upstream answers (first run, a second run with no
  writes, upstream errors, the actions).
- `go test ./internal/blueprint/` — the templates render to their recorded digests; loopback ports
  only on loopback and only in a stack.
- `TestStackRoutesRegisterAWaitingStackAndRevealOnlyItsAccounts` (`api`) — commit registers, the
  page waits, Retry refuses in words, reveal is by name and audited, no secret in the state.
- Live (`JD_STACKS_LIVE=1`, as root): each adapter lane's test brings its pinned images up on a
  network of its own and runs its steps twice. See the header of each `*_live_test.go`.
