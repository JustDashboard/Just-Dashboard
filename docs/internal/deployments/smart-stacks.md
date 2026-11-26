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
  Nothing but Caddy binds a routable address (invariant 1). To reach one from elsewhere the person
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
release that stopped being live is cancelled; one a restart cut short is queued again
(`ResetInterrupted`). Changing the source to the other preset's template moves the stack to that
preset. A deployment whose source is no longer a preset is left alone.

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
| `GET /deploy/{id}/stack` | whoever reads the project | state, readiness, services with container state and loopback address, steps, actions, accounts; `404 stack_not_found` otherwise |
| `POST /deploy/{id}/stack/configure` | the deploy control capability | run a pass now; `409 stack_waiting` before a live release, `409 stack_busy` during a pass; audited |
| `GET /deploy/{id}/stack/credentials/{name}/reveal` | administrator, session, not Hub | one account's password; audited |

Blueprint summaries and details carry `stack`; the Template tab leaves those templates out.

## What the page may claim

`status` is `waiting` (no live release), `configuring`, `ready`, `action_required` (done but for what
only the person can do) or `failed`. `readiness` separates installed, healthy (every container
running and healthy), connected (every link step done), able to play (Jellyfin set up with its
libraries) and able to acquire (Complete, connected, the download link verified and at least one
source in Prowlarr). A Complete stack with no source is `action_required: Add a media source` — never
"set up" — and still plays what is in its library.

## Not done yet

Said here so nobody builds on it by mistake:

- Adopting an existing Jellyfin or Seerr deployment into a stack. The Stacks tab offers fresh
  installs only.
- Mounting an existing library folder from the host; media goes into the stack's own volume.
- Hardware transcoding, the Ultimate preset's optional modules, and a guided form for adding a media
  source (it is added in Prowlarr itself).
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
