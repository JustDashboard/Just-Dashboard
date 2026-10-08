# Docker, files, and logs

## Docker

`internal/dockerx` uses the official SDK over the socket. It shells out in exactly three places —
compose, the streaming compose runner, and `Build` — because the Engine API has no equivalent; all three
build argv explicitly.

Container-table WebSockets share one two-second inventory/stats sampler while viewers are connected.
Each cycle still reads Docker, including changes made outside the dashboard, and sends inventory before
stats in the existing envelopes. New viewers receive the current sample; a slow viewer retains only
the latest pair and cannot block collection. The final unsubscribe cancels the sampler and drops the
snapshot. Direct reads and mutation checks remain fresh. Read-only database discovery can separately
opt into a request-scoped inventory/inspection snapshot; it never survives that request.

- **`ContainerSpec` is the dashboard's shape, not `container.Config` + `HostConfig`.** Those are split on
  the historical accident of which fields the daemon could change after creation, and rendering them as a
  form is how Portainer's create page became twelve accordions. `toEngine` translates and warns about
  what is legal but probably unintended (a port on every interface, no memory limit, an anonymous volume,
  a writable bind mount of a sensitive path). `SpecOf` reads a container back into that shape, which is
  what makes duplicate, edit-and-recreate and "save as a stack" possible.
- **`Recreate` is the verb Docker lacks.** Editing means destroy-and-recreate, which is fine until the
  create fails and the operator has nothing where their service was — so the old container is renamed
  aside (`<name>_jd_replaced_<random>`), restored if anything later fails, and removed only once the replacement
  runs. Compose-managed containers are refused with `ErrComposeManaged`. `UpdateResources` is separate
  because limits genuinely can change in place. `UpdateRestartPolicy` also uses an Engine update,
  preserves the running process, and refuses Compose ownership and incompatible auto-remove settings.
  The service-control route is `PATCH /docker/containers/{id}/restart-policy`; mutations are audited.
- **`render.go` keeps the form from being a black box**: a spec back into the `docker run` line and the
  compose service, rendered **on the server** so "what does this spec mean" has one implementation. The
  YAML is hand-written, not marshalled — key order carries meaning and a marshaller would sort it into
  something correct and unreadable.
- **`diagnose.go` is what nothing else in this class has.** Every panel shows a state, an exit code and a
  restart count and leaves you to read them; this says what 137 means and what the limit was, that a
  container restarted twelve times in a minute, that a health check is failing and what it last said,
  that a port is published in front of the firewall, that an unrotated json-file log has reached 800 MB,
  that data is being written into the container rather than a volume. Deliberately conservative — a panel
  that cries wolf is ignored wholesale — and `diagnose_test.go` pins the claims, including the
  **silences** (a finished one-shot job, a loopback-bound port, a well-configured container).
- **`attention.go` splits the two questions one word was answering.** `Diagnosis.Status` was the worst of
  a list mixing "this container is not running" with "this container is more exposed than it needs to
  be", and the overview labelled it *Health* — so a server whose containers were all up and one of which
  mounted the Docker socket read "All good" above a page of warnings. Runtime health is Docker's own
  report, counted from the container list so it can say how many are *fine*; Attention is posture,
  storage, configuration and exposure, which never clears itself. Every finding carries a four-level
  `Severity` (a recommendation is not a warning) and a `Class` deciding which summary it belongs to;
  `Level` is derived from `Severity` by `normalizeFinding` so the two cannot drift.
- **`exposure.go` is the one definition of what a published port means.** `0.0.0.0:5432` and
  `127.0.0.1:5432` differ by a character and by everything else, and three places computed "is this
  public" with three slightly different rules. The vocabulary is about *binding*, never reachability:
  "bound to every interface" is a fact, and the conclusion that needs the firewall is drawn once, in
  `handleContainerRoutes`, where the proxy and firewall are also in reach.
- **`imageref.go` says what an image string is.** `nginx:1.27`, `nginx@sha256:…`, a bare `sha256:…` id and
  `<none>:<none>` arrive through one field and mean four different things. Normalising blindly appended
  `:latest` to an id, so the daemon read it as the repository `sha256` with a hex tag and answered
  *invalid reference format: repository name (library/sha256…) must be lowercase* — which made every
  dangling image unopenable, since an id is its only handle.
- **`failure.go`, `writable.go` and `anomaly.go` are the derived layer.** `DiagnoseFailure` assembles exit
  code, OOM flag, health history and restart cadence into a cause and labels it *likely*, because it is
  an inference; the cadence comes from the event log, since `RestartCount` cannot tell 17 restarts in
  twelve minutes from 40 across a year. `AnalyzeWritableLayer` runs one `du` inside the container through
  `ExecCheck` with explicit argv, reports which directories are not backed by a mount, and states its own
  failure modes rather than returning an empty result. `PlanMigration` writes the move to a volume out as
  steps and commands and **never performs it**. `DetectAnomalies` reads the recorded history for a level
  *held* rather than a spike, a memory floor that only rises, and a writable layer growing by the day.
- **`events.go` keeps what Docker throws away**, so "why did this restart at 04:00" has an answer. An
  in-memory ring: an event log worth keeping across restarts belongs in the audit table. `oom` and
  `health_status: unhealthy` justify the feature alone. `EventFilter{Kinds, Search, Container, Stack}`
  is the one question every reader asks of it, and `Find` applies it **while walking the ring** rather
  than to a slice of its newest entries — one container's events are a sliver of a busy host's, and a
  limit taken first answered a quiet container with two hundred of everyone else's. A container matches
  by its whole id, an id prefix of twelve characters or more, or its name; a stack by the compose
  project label, which network events do not carry. `GET /docker/events` and `WS /docker/events/stream`
  take `container=` and `stack=` (a name Docker would accept, and `stackProject`), refused with a 400
  before the upgrade; asked nothing they are the host's feed as before. The socket subscribes **before**
  it reads the buffered past, so an event recorded between the two is sent rather than lost — one
  recorded in that instant arrives twice, and every reader drops the copy. `Event.Service` is the compose
  service (`db`, where the event's name is `shop-db-1`), which is what a stack's log calls a container.
  `Event.Container` is the container a network's `connect` or `disconnect` moved, by id — Docker puts
  nothing else of it in the event, so the Networks page names it from the container listing.
- **A container's and a stack's output are log sources**, `docker:<id>` and `stack:<project>` on the
  `/logs/*` routes (see [Logs](#logs)). `GET /docker/containers/{id}/logs`, its `/logs/stream` and
  `GET /docker/stacks/{name}/logs/stream` are gone: a second reader of the same lines with no lens and no
  filter, and nothing called them once both pages embedded the service logs.
- **`CheckUpdate` compares the registry's current digest against the pulled one** — a more useful question
  than "is there a newer tag", because it catches a moving tag that moved. Only successful registry
  digests are cached for 30 min, with a four-worker pool because Docker Hub rate-limits by address.
  The bounded cache belongs to one Docker client and keys on the normalized reference and local digest;
  local state is inspected on every check, so an external pull, tag change or removal cannot reuse an
  old verdict. A changed local digest also refreshes the remote answer. Concurrent misses share a read;
  forced checks and ordinary image pulls invalidate the reference and reject older in-flight fills.
  `checkedAt` retains the registry read's timestamp on a hit. Unreachable or credentialed registries are
  `unknown` with the reason; a locally built image is `local`, a digest reference is `pinned`, and neither is a
  failure — reporting "not checked" for something that cannot change reads as a broken check. Errors
  are not cached. These are anonymous registry reads; adding credentials requires a credential-scoped key.
- **`preview.go` and `deployments.go` make a compose deploy predictable and reversible.** Docker keeps no
  history: `up` replaces what was running and the previous configuration is gone, so "what changed" and
  "roll back" have no answer. `SnapshotStack` records the compose file, the digests each service was
  *actually* running and the git commit immediately before every state-changing action, into
  `docker_stack_deployments` (append-only, 50 per project); environment values are **hashed, never
  stored**, because an .env file beside a compose file holds every password the stack uses.
  `PreviewDeploy` diffs the current file against that record and says which services are expected to be
  recreated — labelled inferred, because compose makes the final call — and states explicitly that no
  volume is removed, which is the commonest fear about the button.
- **`cleanup.go` replaces one word covering five sweeps.** Each category reports what it holds, what
  removing it reclaims (Docker's own figure, which counts a shared layer once) and what that costs.
  Volumes are always listed and never recommended; selecting them still uses ordinary confirmation.
- **Authorization uses effective container resources.** Creation and recreation validate the selected
  spec, including a spec reused from an existing container. Limited accounts may use plain local
  volumes; references to existing named volumes are inspected first. Custom drivers or driver options
  require `system.admin`, as do host/shared network namespaces and device-backed network drivers.
  Inspection failures refuse creation. Local filesystem volume `device` paths must be absolute and
  remain subject to `JD_FILE_ROOTS` for administrators; NFS/CIFS remote names retain their syntax.
  Compose create, config-write, validation and every execution endpoint (including
  the WebSocket runner) require `system.admin` until authorization covers Docker's complete resolved
  Compose model, including includes, substitution and plugins. Ordinary stack reads remain available;
  non-administrator detail requests retain static YAML service names and never execute Compose to
  resolve includes or environment substitutions.
- **Compose.** `RunComposeStream` forwards output line by line, because a request that hangs for minutes
  is indistinguishable from a broken dashboard. `composeSteps` maps actions to commands; `update` is a
  pull **then** an up, so a registry that is down leaves the running stack alone. `ValidateCompose` feeds
  the candidate to the parser on **stdin** so a syntax error never touches disk; `WriteComposeFile` goes
  through a temp file in the same directory and keeps `<name>.bak`, guarding what validation cannot catch
  — a correct file that says the wrong thing. `DeclaredServices` costs a subprocess per stack and is read
  on demand for administrators; other users receive the static declared-service list from discovery.
- **`Build` drives the `docker` binary** because BuildKit is a separate builder the classic API path never
  reaches, and silently building with the legacy one produces images differing from the same Dockerfile
  from a shell.
- **Efficiency rules that are load-bearing**: `ListContainers` carries `Mounts` (the Engine summary
  already has them); membership joins for volumes, networks and images, and image-reference discovery
  use the summary without fetching unused inspection fields. The network listing carries each member's
  address from the same summary — `Network.Endpoints` (container id and name, IPv4 and IPv6 with their
  prefix, MAC) and `Network.Gateway` — kept off the containers socket (`Container.Endpoints` is
  `json:"-"`); the summary has no aliases, so the names a member answers to beyond its own are
  `NetworkDetail`'s, which inspects. The history recorder reads the enriched
  listing because it persists explicit memory budgets, including a limit equal to host RAM, which the
  stats response alone cannot distinguish from an unlimited container. The shared live table sampler
  reuses the inventory it already collected and samples those IDs without another listing.
  `ListStacks` builds on `ListContainers` so it inherits resolved health and uptime;
  `Diagnose` inspects each container once, reusing that payload for enrichment and every rule.
  already has them, and "what uses this volume" for every volume at once is otherwise an inspect per
  container per poll); `ListStacks` builds on `ListContainers` so it inherits resolved health and uptime;
  `Diagnose` inspects each container once and runs every rule against that payload.
  `ListRunning` is the Engine's list of running containers mapped with no inspect at all, for the ports
  page, which asks every fifteen seconds only which container a published port belongs to.
  `ListContainersWithLabels` applies exact label filters in the Engine list call before health/uptime
  enrichment, so a deployment detail read inspects only its matching running containers.
  `ListContainersWithLastRun` is the same listing that also inspects the matching stopped ones, for the
  exit code, OOM verdict and restart count the Runtime services carry (`Container.Restarts`, `Exited`,
  `WasOOMKilled`, none of them on the listing's wire); only the runtime services read asks for it, so the
  cleanup and recovery paths that share the labelled listing pay nothing for it. The uptime pass
  also collects limits, health-check presence and restart policy from the inspect it was already making,
  and marks the rows it did not inspect (`Inspected`) so the UI never renders an absence as an answer.
  It also inspects any container, stopped or not, that the Engine lists by bare `sha256:…` id — which it
  does once the container's tag has moved on to a newer pull — and reports the name from the container's
  own config instead, as `Inspect` does. An id named no product, so every page drew such a container as
  Docker's whale and database discovery (which reads the engine off the name) skipped it.
  `GET /docker/containers/stats` reads `ListRunning` rather than the enriched listing and samples through
  the shared `StatsSampler`, which is **not** the recorder's. Its `WithMaxAge(30s)` drops a previous
  sample older than the bound, so CPU is a recent interval or `cpuReady: false` (the first call, or one
  after a gap), never an average over however long ago anyone last asked. A call that names only some
  containers keeps the others' baselines until they pass the bound, so two pages polling different
  projects do not erase each other's. The optional `ids` query
  (comma-separated, full container ids or prefixes of at least 12 hex characters, at most 64; a bad
  value is a 400) restricts the stats reads to those running containers; the response shape is unchanged.
  Writable-layer sizes ride along on the stats sampler from the shared disk cache, rather than a
  separate layer walk per sample, so "grew 6.4 GB today" is a measurement rather than a guess.
- **Disk accounting has bounded staleness.** One client shares a disk walk between concurrent cold
  readers. After 60 seconds it refreshes in the background; the previous snapshot may be reused for at
  most three minutes from the start of its source read. Older snapshots wait for a refresh, and a failed
  refresh then reports unavailable instead of serving old figures indefinitely. Cancelling one waiting
  request does not cancel the shared walk, which has its own two-minute deadline. Pulls, builds,
  container lifecycle/create/recreate/removal, volume creation/removal, prunes and Compose execution
  invalidate on completion, including partial failures. A generation check prevents an older read from
  restoring invalidated data. These caches are local to one backend; independent instances do not share
  invalidation. Authorization and destructive preconditions remain outside the caches.
- **`httpx.URLParam`, not `chi.URLParam`.** chi routes on `r.URL.RawPath` whenever a request carried one
  and slices the parameter out of the same string, so a handler receives the percent-escapes the browser
  sent. Every Docker route uses the decoding wrapper; `urlparam_test.go` pins it.

### Container usage measurements

`stats.go` retains the Engine's sample timestamp and raw counters. Memory working set subtracts
`total_inactive_file` on cgroup v1 or `inactive_file` on v2, in Docker CLI precedence order; raw usage,
excluded cache, anonymous memory and swap (when reported) are separate readings. CPU counts 100% per
core and carries a readiness flag, quota, host core count and throttling counters. Streaming limits
refresh every 15 seconds because Docker permits in-place resource updates.

Network availability is determined by reported interfaces, never positive traffic. The stream carries
each interface's bytes, packets, errors and drops; the Usage tab derives bytes/second and packets/second
from successive Docker timestamps. First samples, resets, topology changes and reconnects establish a
new baseline. Host networking has no isolated container traffic attribution; `container:` networking
reports a shared namespace. These are interface totals including local/container/LAN traffic, not an
internet billing meter. Block I/O is device traffic, not filesystem occupancy or cached application I/O.

The definitions follow [Docker stats](https://docs.docker.com/reference/cli/docker/container/stats/),
the [CLI calculations](https://github.com/docker/cli/blob/master/cli/command/container/stats_helpers.go),
[runtime metrics](https://docs.docker.com/engine/containers/runmetrics/) and
[resource constraints](https://docs.docker.com/engine/containers/resource_constraints/).
`stats_test.go`, `metrics/container_usage_test.go`, the store migration test and
`frontend/src/lib/container-usage.test.js` pin conversion, availability and rate boundaries. Browser
coverage lives in `docker-ui.spec.ts`, including pause/reconnect, stale data, disabled retention,
host networking, stopped containers and desktop/phone layouts.
For read-only acceptance against a running container, run from `backend/`:
`JD_DOCKER_STATS_CONTAINER=<id> go test ./internal/dockerx -run '^TestLiveContainerUsageStream$' -count=1 -v`.
The test opens the existing stats stream, reconciles totals and memory, and checks cancellation;
it creates or changes no containers.

## Files

Owner and group names are memoised only within a listing or completion request. The next request
performs NSS lookups again, so account renames and reused IDs are not hidden until backend restart.
User and group IDs have separate maps, and numeric IDs remain the filesystem identity.

Copy and move pin configured roots with Go's `os.Root` while traversing entries. Copy resolves a
destination that is intentionally followed, rejects existing symlink children and same-inode or
descendant copies, and writes regular-file replacements to an exclusive sibling temporary file before
rename. Existing destination ownership, mode bits and POSIX access ACLs survive replacement;
ACL read or preservation errors fail before replacing it. Move replaces final file
symlinks as entries; directory destinations are resolved and contained before appending the source
basename. A copy error leaves an existing regular destination intact.

**Nothing clobbers unless told to.** `Move` and `Copy` take an `overwrite` flag (the `/files/move` and
`/files/copy` bodies carry it) and answer an occupied destination with `fs.ErrExist` — a 409
`already_exists` — without it; rename(2) used to replace silently, which was how "move a.txt here" ate
the a.txt already there. `Move` also refuses a directory into its own subtree with a sentence rather
than rename's EINVAL, and treats a move onto itself as a no-op. `Touch` and `Mkdir` are the "New file"
and "New folder" verbs and refuse an existing path the same way (`Mkdir` still creates missing
parents). Uploads go through `Upload`, which writes to a temporary sibling and publishes it atomically:
without overwrite, a no-replace rename (or a hard link where unsupported) claims the destination only
if it is still free after the transfer; with overwrite, a rename replaces it. An interrupted transfer
never leaves a truncated file. An existing file keeps its owner and mode across the replacement
(which is what lets the image editor save over a picture the web server owns), and `?overwrite=true`
is what permits the replacement at all. The handler sends one
request per file from the page, maps a body over `maxUploadBytes` (2 GiB per request) to 413
`too_large`, and drops any directory part a client put in the filename. Empty `from`/`to`/`path`
fields are 400s rather than "the first root", which is what an empty path resolves to.

`ResolveArchive` checks an archive download before a header goes out — the base directory, and each
member through `ResolveEntry` so that a selected symlink is archived as the link it is — and refuses a
member that is not under the base. Resolving members through `Resolve` used to put a link's *target*
into the archive under a name computed from wherever the target lived, which for a link out of the base
was an entry beginning `../`: an archive this server writes must never carry the traversal its own
extractor refuses. `Compress` then streams what was resolved and checks nothing itself.

`internal/files` used to be a listing, a reader and a writer, with a page that answered every click by
loading the file into Monaco — right for a config file, wrong for a picture, a tarball, a video and a
two-gigabyte log.

- **`preview.go`** reports what a thing *is* without loading it: a trimmed head for text (whole lines, no
  rune cut in half), image dimensions, the first 200 entries of a zip or tar without unpacking, child
  counts for a directory. `Editable` is separate from `Kind` on purpose — a preview of a huge log must not
  offer a Save that writes those hundred lines over it. Extensions decide media kinds, **bytes** decide
  the rest (a `.log` is sometimes a rotated binary; a file with no extension is usually a script).
  `imageSize` parses webp and svg by hand: half of what a web root holds, and neither has a stdlib decoder.
- **`MediaType` is a security boundary.** `GET /files/raw` returns a file's bytes with a content type the
  browser acts on, on the same origin as a session that drives the Docker socket — so it is a closed
  allowlist (images, video, audio, PDF) and everything else is refused rather than sniffed. **HTML is not
  on it and must not be**: served inline it runs as this dashboard. The route tightens CSP to `sandbox`
  (which neuters a directly opened SVG) and is the one route with a short `Cache-Control` instead of
  `no-store` — forty thumbnails otherwise re-read every JPEG on every scroll; callers append mtime so a
  saved image is a new URL. The page's `media.ts` mirrors the allowlist by extension: a name not on it
  never gets a thumbnail or a viewer, because the request would be refused. A video thumbnail is the
  browser's own first frame, fetched with the range requests `http.ServeContent` honours, so a poster
  for a two-gigabyte recording costs a few hundred kilobytes.
- **`find.go`** is the fuzzy finder (`search.go` is the literal/regex one, optionally grepping contents).
  Subsequence matching scored so the basename beats directories, a run beats scattered characters, a
  boundary beats mid-word and a shallow path beats a deep one; terms ANDed; positions as **UTF-16
  offsets**. Where the typed term occurs as a run in the name, that run is scored too and the better
  kept, with a bonus, so `promo` finds `…-Promo.mp4` above `proxy-tls-monitor.spec.ts`. The walk is
  **breadth first**: depth first spent its budget inside the first large directory it met and never
  reached a shallow match beside it. Bounded three ways (time, visits, matches) and it *says* when it
  stopped early (`truncated`) — a fuzzy search that quietly answers from a third of the disk is worse
  than one that admits it — while `total` counts the matches before the best `limit` were kept, which
  is a ranking rather than a partial walk.
- **`search.go`** pins the search directory inside an allowed `os.Root`, walks without following
  symlinks, and opens only regular files with a nonblocking flag and an opened-file stat. Content
  scans are limited to 4 MiB per file, 100,000 visits, twelve directory levels and the requested hit
  limit; binary files and generated/system directories are skipped. The palette requests
  `detailed=true`, which returns every matching line plus `hits`, `truncated`, `visited`, `unreadable`
  and `elapsedMs`, under a three-second deadline. Snippets centre on the first match and carry UTF-16
  ranges for literal and regex highlighting. `hidden=false` skips dotfiles; invalid regex is a 400.
  Callers without `detailed=true` retain the hit array and first matching line per file.
- **`places.go`**: `Home` prefers `$HOME`, then `/root`, then a single account under `/home`, then the
  first configured root — every candidate checked through `Resolve`, because a shortcut landing outside
  the roots is worse than no shortcut. A notable place's `name` is what it holds (*Configuration*,
  *Websites*, *Logs*, *Apps*, *Served files*, *Local software*, *Temporary*); its `hint` keeps the
  longer description. `Complete` treats a trailing separator as "inside this directory"
  and anything else as a component being typed; dotfiles appear only once a dot is typed.
- **`Usage`** accumulates per-child totals in the *same* bounded walk (forty children would otherwise be
  forty-one walks) and reports `Truncated` rather than quoting a partial total. A symlink counts as the
  link, or a tree with links into /usr reports the size of the operating system.
- **`GET /system/disk-usage` is a file operation.** Its requested mount passes through `files.Resolve`,
  recursive visits and top-level children are capped, and only two scans run concurrently. A result limit
  controls the response size; it is not mistaken for a bound on the work needed to rank those results.

Bookmarks live in the `settings` table (`handlers_files_browse.go`), not the browser — which directory
matters is a fact about the server and should be there from a phone. Recent folders are the opposite and
stay in `useViewState`. The bookmark list is saved whole, so an add, a removal and a reorder cannot
disagree about order; every path is resolved before storing.

A folder's **colour** is the same kind of fact and lives beside them (`files.colours`, a map from the
entry's resolved path to one of nine names — blue, teal, green, yellow, orange, red, pink, purple,
graphite), returned with the places as `colours`. The strip's global choice is stored as
`files.defaultColour` and returned as `defaultColour`; `PUT /files/colours/default` (`file.write`,
audited as `file.colour.default`) sets it and clears individual labels in one transaction so every
folder changes at once. New individual labels take precedence until the next global choice.
`PUT /files/colours` (`file.write`, audited as
`file.colour`) sets one path per request, or clears it with an empty colour, so two tabs labelling two
folders cannot undo each other; a name outside the closed set is refused, and so is a path the roots
refuse. The label follows its folder: a move (a rename is one) re-keys it and everything labelled under
it, using `files.MoveEnds` to learn where the entry actually landed, and a delete drops it, so a new
folder of the same name starts with the global colour, or blue if none was chosen. Both are best-effort
after the filesystem operation has succeeded.

Frontend `components/files/`: the page is **one framed workbench** with no page header above it — a
strip across the top carrying where you are (a compact folder button for the global colour, the
path and its star) and every page command (Find, content search, refresh, the view, arrange, the GitHub
account, New, Upload, and the toggles for the two side columns), grouped into outlined boxes
(design-system §14), then a sidebar, the listing and an
inspector as three flush columns with a hairline between each, resizable through `panel-size.ts`. The
sidebar (`files-sidebar.tsx`) has no header of its own and is a fixed list the way a desktop file
manager's is, not a folder tree, in four sections that fold and stay folded (`files.sidebar.folded`):
**Home** (the dashboard's home and the accounts), **Starred**, **This server** (`/` as *File system*,
then the notable directories) and **Recent**, each row one line and a drop target, with the browsed
folder marked when it is one of them. It does not change as the listing walks into folders — the
walking happens in the listing. `placeSections` in `places-menu.tsx` builds that list once for the
sidebar and for the phone's menu alike, and draws each place as what it is (`PlaceMark`: `/` as the
host's distribution, a home as a folder with a house in it).

The search palette (`quick-open.tsx`) searches *This folder*, *Home* or *Everywhere*. Home is
`homeFor` the folder (`search.ts`): the account home it is inside, else the one account's home on a
one-account server, else the dashboard's own — the same home the strip's house button goes to. It used
to be the dashboard's `$HOME` alone, so on the usual install "From home" searched `/root` while the
operator was browsing `/home/ubuntu`.

Folder navigation uses native browser history (`use-folder-navigation.ts`, `navigation.ts`). Each
visit pushes a `?path=` address, so browser Back/Forward and mouse history buttons traverse folders;
the toolbar and Alt+Left/Right or Cmd+brackets use the same history. The initial home or remembered
folder replaces its address without creating an extra visit. History survives reload, a new visit
after Back drops the forward branch, and exhausted folder history lets browser Back leave Files.
The Parent control, Backspace, Alt+Up and Left in details view use a parent within the server's roots;
Right in details view enters a folder. During loading, the places response supplies the root limits.
The tab remembers up to 40 folder visits: available selections, the active item, view and scroll.
State is saved as interaction happens; a stable history listener restores listing keyboard focus
without stealing focus from a toolbar control. Removed entries cannot remain selected or inspected.

`keyboard.ts` scopes the page and path-bar shortcuts to Files, leaving text fields, composition,
open dialogs/menus/listboxes and the dashboard rail in charge of their own keys. F5/Ctrl/Cmd+R
refreshes only the folder; a modified hard refresh keeps the browser's behavior. Ctrl/Cmd+F opens
name search; Ctrl/Cmd+Shift+N creates a folder, Ctrl/Cmd+Shift+. toggles hidden files and Ctrl/Cmd+Space
toggles the active item. Name typing jumps through entries, repeated letters cycle matches, and
tile arrows follow the actual rendered columns. Escape clears the selection/active item before
canceling the internal clipboard. The footer's Files shortcuts control (also `?`) opens
`shortcuts-dialog.tsx`. New keyboard mutation paths invoke the existing guarded operations.

`file-icon.tsx` is the vocabulary (~200 extensions, the files with none — Dockerfile, authorized_keys,
lockfiles — and ~90 folders whose name says what they hold) and draws it itself rather than from an icon
set: a folder is a two-tone folder in its colour (its label from `FolderColourProvider`, else the
chosen global colour, else graphite for build output and installed dependencies, else blue) with a product's Simple Icons mark
(`public/logos/mono/`) or a Heroicons glyph pressed into its face; a file is a page with its corner
folded, the format's own logo on it (`public/logos/`, devicon's language marks among them), and a band
along its foot in the format's colour (`--language-*`, `--tag-*`) carrying the extension where the icon
is large enough. `folder-colour.tsx` is the picker — swatches in the inspector for one folder, a menu
behind the strip's small folder button for all folders — and `file-actions.tsx` offers the individual
choice as a submenu on every folder's menu and on the
background menu for the folder being browsed. The inspector (`preview-panel.tsx`) opens on the thing
large — the picture or video on a stage, or its folder or page — with its name, its extension in the
format's colour, its kind and size, a folder's colour swatches, and its verbs: the one most people
want named and wide (Edit, View, Open folder, or Download for a binary or archive) beside glyphs for
the rest (View, Crop, Download, Checksum). Under that, grouped sections: what is inside (the text
head, the PDF, the archive, a folder's folder and file counts with Measure size and its largest
entries as a `BarList`), Details (modified with the date, size with the byte count, lines, language,
owner and group, a link's target), Access (the mode as an owner/group/everyone grid and in one
sentence, `access.ts`), and Location (the path as crumbs to walk up, with Copy path). It describes the
folder being browsed while nothing in it is chosen. `thumbnail.tsx` draws a picture as itself and a video as its first frame on a
row and a tile alike (images lazily, a video only once it scrolls into view, and playing muted under the
pointer on a tile). `file-actions.tsx` declares every verb **once, as data**, and renders it into the
listing's context menu (`ui/context-menu.tsx`, one root over the
listing that reads the row from `data-entry-path`); the space between rows gets the folder's verbs.
`dnd.ts` makes folder rows, sidebar places, crumbs and starred folders drop targets for paths dragged from
the listing (Ctrl or Alt copies) and for files from the desktop. The listing itself has no overflow
dots: right-click, Shift+F10 on an entry's name or touch long-press opens its menu. A plain click
inspects, double-click or Enter opens, and after the first checkbox/modifier selection a plain click
anywhere on an entry toggles it. Checkbox clicks also establish the Shift-range anchor.
`selection.ts` holds range and rectangle hit testing; `use-marquee.ts` captures background mouse
gestures in scroll-content coordinates, selects intersecting entries in either direction, preserves
the initial selection with Ctrl/Cmd/Shift and scrolls at the listing's edges. Release commits; Escape,
pointer cancellation or loss restores the snapshot; a background click clears. Touch scrolling and
native entry dragging remain available. A drag carries the selected group (or just an unselected
source), draws a compact count preview, fades its sources and highlights accepting folders; a folder
never advertises a drop into itself or its descendants. After a successful move, the inspector clears
an entry moved away (including a descendant of a moved folder); copies and failed moves retain it.
Selection and clipboard actions are animated
foot overlays with reduced-motion support, leaving the listing's layout unchanged. The grid leaves
metadata to the inspector and details view; all tile sizes use compact fixed columns and 4px gaps.
`uploads.tsx` is the queue — one
`XMLHttpRequest` per file for progress, three at a time, folders walked through the entries API so a
dropped folder is its contents rather than an empty file named after it — and `conflict-dialog.tsx`
asks once per operation (replace, keep both, skip) before an upload, move or paste touches a name that
is taken; "keep both" is `photo (2).jpg` for a transfer and `photo copy.jpg` for a duplicate.
`media-viewer.tsx` is the full-screen look (a `Modal` at `size="full"`): pictures fit or 1:1, video,
audio, PDF through a blob, and the same head or archive listing the inspector shows for anything else;
Space in the listing opens it, the arrows walk the folder's files. Text and image editors open beside
that listing. **Open full editor** transfers the current draft in memory to `/files/editor`, which
shares the same editor controls beside a collapsible `FileTree`, which unfolds the current file's
ancestors; on a phone the tree opens over the editor. Contents are never put in browser storage. Monaco exposes Find, Replace, commands, undo/redo,
formatting, language, indentation, wrap, minimap and font size, with a cursor/UTF-8 size status and a
diff review (`diff.ts`, a prefix/suffix-trimmed LCS capped at a few million cells) drawn by `DiffView`.
Content-search results reveal their matching line. `quick-open.tsx` shares one keyboard palette for
names (Ctrl/Cmd+P) and contents (Ctrl/Cmd+Shift+F), with immediate current-listing name matches,
cancelled stale requests, hidden/case/regex controls, a wider scope and explicit partial/error states.
The palette keeps a fixed viewport-bounded frame, with independently scrolling results and reserved
footer space for partial/unreadable notices. The search input has no active border or outline; the
caret, selected result and keyboard navigation carry its state. Results fade in and out without
resizing the frame; exiting rows immediately become inert and hidden from assistive technology, and
reduced motion shows each state immediately. A failed disk request leaves current-listing matches
available. The listing polls every twenty seconds and refetches
hidden-file flips in place; the parent row is offered only where the parent is inside the roots; a bulk
delete that includes a folder uses ordinary confirmation like a single one. Two layout rules are easy to undo: **the
listing body does not scroll** (a sticky table header sticks to its nearest scrolling ancestor), and
**the sidebar's tree waits for `/files/places`** before mounting, since it caches and would keep showing
the refusal from listing a root it cannot. The image editor commits each operation to a **new canvas**
rather than a live parameter pipeline, with bounded undo/redo, original comparison, zoom, resizing,
rotation, flips and live brightness/contrast/saturation. `react-image-crop` supplies touch and keyboard
crop handles and aspect-ratio constraints; native canvas operations stay local to the browser. Save
bakes live adjustments into PNG/JPEG/WebP, preserving `.jpeg` when applicable. The ordinary upload
route gets `overwrite=true` only for the source path, preserving owner and mode; a copy refuses an
occupied name. Both editors guard closing a dirty sheet. The full workspace guards file changes,
links, unload and cancelable browser-history traversal (see `frontend/shell-design.md`).

Large listings keep every filename and metadata cell mounted: native find, sorting, filtering, range
selection and select-all still address the complete directory. One shared intersection observer defers
offscreen thumbnails and heavier row controls; keyboard focus keeps its current control mounted.
The row/tile checkbox is a controlled two-state button, while select-all keeps its mixed state.
Overflow menus instantiate on first activation and remain mounted afterwards for focus restoration.
The listing API still returns the complete directory; these rendering savings do not reduce its disk
reads or response bytes.

## Logs

The host Logs frontend records sources, modes, run handoffs and settled searches in browser history.
Back/Forward restores the question and its in-memory record/following state; match controls and local
read refresh operate in the current pane. [Workspace interactions](../frontend/workspace-interactions.md)
defines identity, storage and keyboard rules; discovery, search and stream routes retain their guards.

`logsx` + `handlers_logs.go` were three products wearing one page: the grep box and level chips applied
to *file* tails only, `/logs/search` and `/logs/logrotate` had no caller, export ignored the filter, and
rotated archives were unreachable — so "when did this start" could not be asked past last night's
logrotate run, which is the question that sent people back to ssh and zgrep.

- **One filter, compiled once, applied to every kind.** `logsx.Filter` is the single description of what
  the operator wants; `handleLogStream` turns a file, a container, a PM2 process and the journal into the
  same `logsx.Line` before filtering. A bad regex is refused **before** the socket upgrades, so it is a
  form error rather than a stream that opens and stays empty. `logsx.Collector` does the same for history.
- **A filtered tail opens on n *matches*, not n lines.** `TailLines` scans backwards through the last
  32 MB collecting matches, then follows from the byte the scan stopped at — a byte earlier duplicates a
  line, a byte later loses one. `Prefill` distinguishes "few matches" from "we only looked so far back".
- **Archives are part of the log.** `Archives` orders rotated generations by **mtime**: the two schemes
  count in opposite directions (`syslog.1` is newer than `syslog.2`; `syslog-20240612` is older than
  `-20240613`), so ordering by name reports an incident running backwards. gzip and bzip2 read
  transparently; xz and zstd are skipped rather than offered and then refused.
- **`unknown` is a level.** Most lines carry no level word, so a filter without that chip hid every
  unclassified line — including the continuation lines of the stack trace being hunted.
- **The journal's numbers and a text log's words are one vocabulary** (`LevelFromPriority`).
  `maxJournalPriority` pushes only the *maximum* down to `journalctl -p 0..n`, since the chips are a set
  and `-p` takes a range; the exact test is still done here. A text filter is deliberately **not** pushed
  down — `journalctl -g` needs a PCRE2 build nobody can assume — so the window is widened instead.
- **A tail search reads the journal newest first.** Read in the journal's own order, a window the
  60-second limit cut short lost its newest end — the lines the search was for — and a crash-looping
  unit writes a hundred thousand lines a day. `searchJournalTail` runs `journalctl --reverse`, holds the
  newest 50 000 records, reads anything older forward up to the oldest held (stopping at its cursor),
  and feeds the collector oldest first, since a lens and a record are only readable in that order. When
  the newest end took so long that the rest cannot fit in the time left, the rest is not started and
  the answer is one stretch up to the window's end, marked incomplete. `order=asc` keeps the forward
  read. A container's window goes to Docker as RFC 3339 with nanoseconds: in whole seconds an `until`
  was cut back to the start of its second, and "the minute before the crash" lost the crash line. The
  journal's goes to `journalctl` to the microsecond and spelled `… UTC` (`journalTimeSpec`): it runs on
  the host, reads a stamp with no zone in the host's local time, and moved every window by the host's
  offset — leaving a hole before the newest records a tail search held.
- **Nothing is offered that cannot be opened**: `Discover` runs `Allow` over the well-known paths, or an
  install that narrowed `JD_LOG_ROOTS` gets a rail of files that refuse to open. Source kinds that cannot
  be queried return an explanation in `missing`; an installed PM2 with no managed processes reports that
  empty state explicitly rather than disappearing from the rail. `/logs/sources` does not list the
  journal's readings by program (`journal-id:sshd,…`, `journal-id:CRON,crond`, `kernel:`): any host with
  a journal can open them, so the rail adds them client-side (`railSources` in `source-rail.tsx`) where
  no listed file already holds the same lines. `GET /logs/source` describes one source and answers under
  the id it was asked by — `file:<path>` stays `file:<path>`, a container asked by name keeps its name.
- **Retention is a verdict, not a rule list.** `MatchRetention` finds the file no rule governs — precisely
  the entry a rule list cannot show. Two parser details, both found against a real host: a stanza's paths
  may be listed **one per line before the brace** (exactly how Debian ships rsyslog's, so reading only the
  brace line reported syslog, auth.log and kern.log as governed by nothing), and the globals at the top of
  `logrotate.conf` sit at the same indentation and must not be mistaken for paths. A rule that has not run
  in a fortnight is a warning — that is the failure this panel is for.
- **Parsing performance is the difference between usable and not** (a five-file auth.log scan: 19 s → 2 s):
  a word scan with a map lookup (`detectLevel`) instead of a regex, timestamp layouts chosen from the
  first byte rather than tried in turn, and case-insensitive substring folding in place. The ISO timestamp
  is read as a **token**, not a fixed width — slicing 25 characters chopped the zone off
  `2026-08-28T23:03:24.804642+02:00` and filed the line an hour wrong outside UTC. `Filter.Highlights`
  returns **UTF-16** offsets, because Go counts bytes and the browser slices by code unit.
- **A lens is what the dashboard knows about one kind of log** (`lens.go`, one `lens_<id>.go` each):
  how Postgres spells a failed login, what nginx means by `*42 upstream timed out`, which sshd line is a
  scanner. Its `Reader` runs on every line **after `ParseLine` and before any filter test** and names
  what the line records (`Line.Event`) and the values in it (`Line.Attrs`), so the live tail, the
  history, the histogram, the facets and the export agree — they all read the line through the same
  lens before anything judges it. Twenty-one are registered: `postgres`, `mysql`, `redis`, `mongodb`,
  `clickhouse`, `mssql`; `http-access`, `nginx-error`, `nginx` (a container's access and error lines by
  shape) and `caddy`; `auth`, `firewall`, `kernel`, `fail2ban`, `systemd`, `cron`, `certbot`,
  `packages`; `app` and `pm2` for application output (requests, exceptions with their stack frames,
  starts, and the failures deployments die of); and `syslog`, a composite that hands each line to the
  lens its program has (`ProgramLens`, one table for the composite and the journal alike) and records
  which on the line (`Line.Lens`). Readers are single-pass and forward-only — a value is carried onto
  later lines, an emitted line is never edited — allocate attrs only for a line they recognise, and gate
  every expression behind a cheap prefix or substring test: the budget is twice the no-lens search, and
  `BenchmarkSearchLens` measures the shipped lenses against it over a million mixed lines (the worst was
  about 1.6× when they landed). Parsing lives only here; the browser reads `event`/`attrs` off the
  wire and never re-derives them. **Level precedence** is a lens's `SetLevel`
  (the format's own severity token, or a status or crash rule), then a JSON or logfmt level key, then the
  journal's priority, then the word scan — which files, Docker and PM2 get and journal lines never do.
- **A record is kept or dropped whole.** A Postgres `ERROR` is followed by its `DETAIL` and `STATEMENT`,
  a Java exception by forty frames; judged line by line, a search for "deadlock" matched the ERROR and
  lost the DETAIL naming the processes, and a level filter dropped the STATEMENT, which has no level of
  its own. A lens marks such lines `Cont`, and `Stream` (`stream.go`) gives each the verdict of the head
  it follows: kept iff its head was, drawn in its head's level, never counted in `matched`, the
  histogram, the facets or the measure. Every reader runs the same four steps — `Skip` before parsing,
  `ParseLine`, `Read`, `Keep` — and the text pre-reject may skip a line only while no lens reads the
  stream and no kept record is open: a lens carries values forward (a transaction's Start-Date onto its
  Upgrade line, a connection's address onto the FATAL that ends it), so a line it never saw would read
  one way with a query and another without, and lose the stamp that keeps it out of a window. The gate resets per file and per stream, and a continuation line at the top of a window, whose
  head nobody saw, stands on its own. Docker's and the journal's own stamps are re-applied after the
  reader, so no lens can move a line in time.
- **Detection runs in the handlers, never in the search or the tail** (`DetectLens`), so those read
  `Filter.Lens` and nothing else and the package's own tests still describe what they always did. A file
  by its directory or basename, never a substring of the name (`/var/log/postgresql/` is Postgres,
  `auth.log`/`secure` auth, `ufw.log` firewall; `/var/log/myapp/cronjobs.log` is an application's); a
  container by its image's last path segment, where a list that reports the image as a bare
  `sha256:`/hex id is looked up through inspect's `Config.Image` and kept per image id
  (`Server.logImageRefs`) — a Postgres container read as an application is the first thing a database
  page would get wrong; a unit by its own name; a `journal-id:` source by its first identifier; the whole
  journal as `syslog`; PM2 as `pm2`; a stack container by container. `lens=<id>` forces one, `lens=none`
  reads no lens, and an unknown id is a 400 from `NewFilter`, before a socket upgrades.
- **A field predicate narrows by what was read out of a line, not by its text** (`predicate.go`):
  `f=user:postgres` finds the lines where postgres is the user, where a search for "postgres" also finds
  the database, the path and the comment. `f=<key>:<value>` repeats, at most 32, each value at most 256
  bytes, split at the **first** colon (`client:2001:db8::1` is an address); keys are
  `[A-Za-z0-9_.@-]{1,64}` and resolve through `Line.Value` — `event`, `stream`, `source`, then the attrs,
  then a structured line's fields. `level` is not a key: the level chips are. A bare value is exact and
  case-insensitive, `=lit` is the escape for a literal that starts with an operator, `!lit` not equal,
  `*` present, `!*` absent, `~sub`/`!~sub` substring and not, `>n` `>=n` `<n` `<=n` numeric (a
  non-numeric value never matches; a non-finite bound is refused). A line without the key satisfies only
  the negations — hiding the lines where user is postgres keeps the ones with no user — and the virtual
  `pattern` (below) is a key too, so a pattern pressed in Insights narrows. Within one key the exact and
  substring forms are alternatives and the negations and comparisons constraints on them —
  "status ≥ 500 and not 503" is the range meant — and different keys AND. `Match` is text, level and
  fields; the sites that test the text before parsing call `MatchParsed`. A predicate-only filter is not
  `Empty()`, so it takes the filtered prefill and `meta.filtered` is true. The browser evaluates the same
  grammar over a live tail (`matchFields` in `lib/log-filter.ts`), and `testdata/predicates.json` is the
  one set of vectors both are held to.
- **A search can answer "who" as well as "which lines"** (`facets.go`), counted in the same pass that
  collects the matches, from the same lens reading, so a bar in Insights and the lines under it cannot
  disagree. `facets=` (≤ 12 keys: any field key, `level`, and the virtual `pattern` — the message with its
  digits, hex, addresses, UUIDs and quoted strings replaced by `<*>`, computed only when asked) returns
  each key's top `facetLimit` values (12, at most 50) with their error count, first and last stamp,
  `distinct`, `other` and `missing`; past 20 000 distinct values or 4 MB of value bytes per key the rest
  folds into `other` and `distinctCapped` says the ranking is approximate. `measure=<key>` adds `sum` and
  `max` per value and the key's p50–p99, max and mean from a fixed-seed reservoir of 100 000, so asking
  twice gives one answer; `sample=` keeps the last value of up to three keys beside each value of the
  first facet (the statement beside its fingerprint, the usernames an attacking address tried).
  `histogramBy=<key>` splits the columns by a key's values instead of by level — interned to 64 during
  the scan, the top eight kept, the rest `*` — and `histogramValues=` pins exact series, which is what a
  page's readings ask. Readings ask about the last hour on every page load, so a search with `since` on
  a live uncompressed file first bisects on line stamps (`since.go`: at most 16 probes of 64 KB, from
  2 MB up, backing off to the start whenever stamps are missing or run backwards) and skips an archive
  whose mtime is older than the window.
- **The journal's lines carry what the manager knew** (`journalLine`, `procs.ParseJournalLine`). The
  manager's "Started …" and "Failed with result 'exit-code'" are written by PID 1, so their
  `_SYSTEMD_UNIT` is `init.scope`; the unit and the run they are about are in `UNIT` and
  `INVOCATION_ID`. The kept attrs are `unit` (`UNIT` ‖ `USER_UNIT` ‖ `_SYSTEMD_UNIT`), `program`, `pid`,
  `invocation` — the named unit's run for a manager line, preferred over the writer's own, or every run
  of a user unit folds into `user@1000.service`'s one — `message_id`, `result`, `exit_code` and
  `exit_status`. `MESSAGE` goes through `ParseLine`. One journalctl run holds two kinds of line, so
  `journalRoute` hands the manager's to the `systemd` lens and, where the lens was detected rather than
  forced, every other program's to its own (`ssh.service`'s lines come from `sshd-session`); a forced
  `systemd` lens leaves other programs' lines unnamed. With a lens reading, `-p` is never pushed below
  6 — a lens raises lines a program filed at info, and Postgres writes its FATAL to stderr — and a
  predicate or that clamp widens a tail's `-n` twenty-five times, as a text filter does.
- **Three kinds of source were added.** `stack:<project>` (`handlers_logs_stack.go`, a compose project
  name) is every container of the project from `ListContainers(all)` — replicas and exited ones, whose
  history is the stack's too — merged by Docker's stamps, each line's `source` the service and its attrs
  carrying `service` and the short `container`, each container read through its own lens with its own
  record gate. The tail opens on each container's last n merged by stamp and then follows each running
  one from its own newest stamp (the newest, not the last read: stdout and stderr arrive through two
  pipes, out of order). Docker ends a follow when its container stops, and restarts, crash loops and
  redeploys are stops while the rest of the stack runs on, so every `stackRejoin` (2 s) the project's
  containers are listed again and one that runs and is not followed is — the same container from its own
  newest line, a redeploy's replacement from the newest line its service sent, one stopped when the
  socket opened from then; the socket sends `eof` once nothing in the stack runs. History and export
  merge into one collector, so a stack's search has one limit, one histogram and one set of facets. `journal-id:<ident>[,<ident>…]` is `journalctl -t` per
  identifier (1–16, each `procs.ValidateName`'d) and `kernel:` is `journalctl -k`; both are followable and
  searchable like `journal:`. `/logs/sources` lists one stack per compose project with its `images` and
  a status; `Source` and `LogJournalUnit` carry `lens`. `GET /logs/source?source=<id>` describes one
  source — its lens, path, size, modified, archives, archive bytes and status — for a page that embeds
  one log and should not walk every root and ask Docker, PM2 and systemd about everything else; it has
  `/logs/sources`' capability and answers 404 for a file that is not there and 400 for one outside the
  roots. The stream's `meta` carries the lens, and `eof` is a frame.
- **Auth data is `system.admin`'s** (`logTargetFor`). Every `/logs` route that reads a source — the
  stream, the search, the export, the retention verdict and `/logs/source` — parses the id through it,
  so there is no second door to the same lines. It refuses, with a 403 for anyone else, `auth.log` and
  `secure` (with their numbered generations, `auth.log.1` and `secure-20240612`, and any file resolving
  to one of them), the `ssh`/`sshd` units and their `ssh@`/`sshd@` instances, and a `journal-id:` naming
  `sshd`, `sshd-session`, `sshd-auth`, `sudo`, `su`, `login` or `systemd-logind`; `/logs/sources` leaves
  those files and units out for a non-admin, so nothing is offered that cannot be opened. A file is also
  gated when one of its rotated generations resolves to auth data, since a search with archives reads
  them, and so is a PM2 process whose out or error file does: its owner names them. Whatever the gate,
  a generation is read only when it resolves inside the roots itself (`searchPaths` runs `Allow` on
  each), since a name beside the live file can be a link to anywhere. The reason is
  `/logins/failed`'s: "Invalid user <what was typed>" is sometimes a password typed into the username
  prompt, and a sudo line carries the command that was run. **The whole journal (`journal:`) stays
  readable at `read`**, as it was before the gate existed — narrowing it to sshd is what is gated — and
  that is a known gap rather than a boundary. `TestAuthLogsNeedAnAdministrator` pins each door.

Frontend `components/logs/`: the page is a workbench like the terminal — one frame, the source rail
(`source-rail.tsx`, hideable and resizable, remembered through `view-state` and `panel-size`) beside
`log-workspace.tsx`, a hairline between them. Over the frame the page reads as a deployment's Logs page
does: the chosen source as an identity line (`SourceIdentity` in `source-facts.tsx`, the Overview's
`HostIdentity` shape: the source drawn as its product, its name, and its kind, state, path, size and
rotated set as facts), with Export and the shortcuts key at its end — a request record takes the same
line (`RecordIdentity`) — and under it the lens's readings as tiles (`LensReadings`) where five fit
across and the console keeps its height, a window at least 1280 by 800; on a smaller one they are the
counts on the lens row's chips instead. The workspace is one pane, and its chrome is at most three
rows above the lines: a strip of the views as `tabClasses` buttons with `aria-pressed` — **Live**,
**History**, **Insights**, then the page's own views — which on the logs page (`name={null}`) is the
rail toggle and the views from its leading edge, and on a service page names the source with its facts
beside the name (`SourceFacts`, giving way by the strip's own width rather than the window's) and the
page's actions, Export among them, before the views; `filter-bar.tsx` with the one
filter, the window and the journal unit inline and the exclusion, context, archives, boot and a
**Read as** select (Auto, naming the detected lens; each lens; None) behind "More"; and `lens-bar.tsx`
only when the lens has something to offer or a predicate is on. The histogram sits over History's lines,
and a footer carries the stream's state, the counts, the search's notes and the retention verdict. One
filter, because "these errors are scrolling past, when did they start" is one thought. Live applies as
you type (debounced; the socket restarts, which is what makes the prefill meaningful); History re-runs
when a chip, a field, the lens or the regex switch changes, and the words in the box wait for Enter,
because a keystroke-triggered full scan would queue a pass over gigabytes per character. A `key:value`
word typed in the box, for a key the lens reads, becomes a field chip on Enter (`12:30` stays text). `/`
focuses the filter of the pane last pressed or focused, not the first on the page. A reconnect drops the
lines the server replays that are already on screen rather than appending them again, and an `eof` frame
stops the reconnecting: "Stopped", with a Reconnect button, since a container that exited would otherwise
replay its last lines once a second.

`log-console.tsx` draws each line as columns — a level edge, the line number, the time (`lib/log-view.ts`:
off, clock, date, UTC, or the delta since the line above), then the **event word the lens named** in the
level's column (`eventMeta` in `lib/log-lenses.ts`: "auth failed" says more than "err"), sized by
`eventColumnFor` to the longest word on screen within 6–17 characters, or the level's word where no line
names an event (`LEVEL_MARK` in `lib/log-filter.ts`, `LEVEL_WORD` in `log-text.tsx`); with Colour on, up
to three of the lens's columns for values its text buries (a Postgres line's duration, user and
database, drawn by `field-value.tsx`), shown only while some line on screen has one and only from 900 px
of the console's own width — one drawn as its text (an upstream, a jail, a namespace) as wide as its
longest value on screen within 6–24 characters (`columnWidthFor`), an address, a duration or a status by
its kind; the journal unit or a stack's service in its lane's hue; then the message
drawn by its shapes: `lib/log-tokens.ts` cuts the text into spans (time, host, program, pid, level, key,
string, number, address, URL, path, method, status, id, failure and success words) over the original
string, so the server's match ranges still intersect them, and `log-text.tsx` colours them from one map
(design-system §14). In a narrow pane the message moves to a line of its own under the event word and
the lane rather than starting past the edge. While the time column is on, the line's own leading
timestamp is not drawn, and neither is a syslog hostname that is this host's. Error and critical rows are
washed, warnings too; `stderr` is a muted mark, not a verdict, since Postgres writes everything there. A
line the server parsed as structured (`message` and `fields` on the wire) is drawn as its message and
fields in logfmt order, most telling field first — except in a History result, whose match ranges are
over the raw JSON. A record's continuation lines stay under their head, three inline and the rest behind
an "N more lines" fold that a search hit inside forces open; a run's start (`divider`: a unit started,
an application came up) draws a rule across the pane of one service's stream — a container, a process,
a stack, an application's file, a unit's journal (`oneService`) — and never across a whole host's, where
it ruled off every timer that fired; and in Live, consecutive repeats — same source, level, event and
words, the leading time aside — are one row with `×N` (the Repeats toggle), keeping the first line's key
so an opened row stays open as the run grows.
Tokens are cached by text, because a server's log repeats itself, and each row is memoised and keyed by
its arrival (`lib/log-line-key.ts`, a `WeakMap` sequence on the line object) rather than its buffer
position, because the live tail appends and trims: evicting old lines at the 4,000-line cap reuses the
retained rows and keeps the opened line attached to its text, the weak keys hold no evicted history, and
identical messages from separate arrivals keep separate keys. A "Colour" toggle, persisted with Wrap, Time and Repeats in `lib/log-view.ts`, shows every line exactly as
written. Below a console width of 28rem those four are one **View** menu beside Pause, Copy and Clear:
seven buttons left the level chips room for one. A press on a line — only when nothing is selected, so a drag still copies — opens it **in place**
(`line-detail.tsx`, the request row's shape): the event as its title, its values as facts drawn as what
they are, each with "Only lines where …" and "Hide lines where …", the raw text in a `Well` (indented
when it is JSON), and "Lines around this", two searches either side of the line's instant with nothing
narrowing them, fetched only on the press. `j`/`k` walk the lines and Enter opens one while the
console (`aria-label="Log lines"`) has focus. The source rail draws each source as its product (a
container as its image, nginx, PM2, the system files as the host's distribution, a lens's product where
it names one). The console uses `content-visibility` rather than a virtualiser: off-screen rows skip
layout while the scrollbar stays honest, wrapped rows keep real heights, and the browser's own find
still works. The level chips carry the on-screen counts and share their swatches (`LEVEL_DOT`) with the
level column and the histogram. **Pausing holds incoming lines instead of dropping them**, a new
question's opening window included — the empty pane then says how many are held and offers Resume,
rather than that nothing matches.
`histogram.tsx` is matches over time by level, or by the key a search split them by (a status class in
its family's colour, events by their tone); clicking a column narrows the window to it.

**A lens's presentation is data** (`lib/log-lenses.ts`, types only from `lib/log-fields.ts`, which names
each attr's label and kind): per lens, its event labels and tones (`mark: false` for the high-volume
neutral ones — a request, a connection — which keep the level word; `divider` for a run's start:
systemd's `started` and an application's `startup`; a hint under 25 characters, one line of a tile),
its **quick views** (a question as fields and levels: Postgres's Slow, Auth, Locks, Maintenance), its
**readings** (figures over the lens's window), its **groups** (a key ranked under the group's own
predicates, with a sample and a measure: slow statements by their shape with the query beside it,
attackers by address with the usernames they tried), its facets, measure, columns and **defaults** (the
auth log's scans hidden), applied on first open as chips the reader can press away. `log-lenses.test.js`
reads the Go golden (`testdata/lenses.golden.json`) and holds the registry to what the parsers emit, both
ways. `lens-bar.tsx` is the lens's one row, scrolling sideways: what is narrowed first, as chips that go
on a press; one **Fields** chip opening a popover of each facet's top values (a press includes, an icon
excludes); then the quick views with their counts — from History's facets, or tallied over the lines on
screen in Live — except a view a tile the page draws already answers (`answered`), which carries no
count of its own under the tile's. A unit's journal adds systemd's Failures to its own lens's views,
since most of a failing unit's journal is the manager's lines about it. Applying a view replaces the
question's fields and levels; pressing it again lets them go. A lens whose defaults are levels alone
(ClickHouse's "Info and above") shows them as the defaults chip too, and they go with it when the pane
moves to a source in another lens, unless the reader changed them. `insights.tsx` is **Insights**, any lens's version of the request log's: one overview search
(`facets`, the measure, `histogramBy=event`) and one per group, in parallel, over the window and the
filter on screen, drawn as the events over time, a `BarList` per key (a press narrows and stays),
the measure's ladder where it is milliseconds, each group as a ranked table and the log's patterns last.
`lens-readings.tsx` is the readings: those on one key share a search, those on levels another, and a
distinct count asks on its own (`lib/log-insights.ts`), read again each minute over the lens's window
and independent of the filter on screen and of the view, so they hold still while the reader moves
between Live, History and Insights; each figure counts up as it lands (`NumberTicker`) and glides to the
next minute's. Insights draws none of its own, which would be a second answer over another window. A
page with a `StatGrid` of its own takes the tiles from `useLensReadings` (`only` names the ones it
draws, and the rest are not searched for) and hands them to the pane (`answeredBy`).

**`service-logs.tsx` is the one thing a page that shows a service's log embeds** — a database's server
log, a container's output, a site's access log, a unit's journal — so no page sends the reader to
`/logs`. The page hands over its sources (a picker appears for several), the lens where it knows better
than detection, its own views (`ServiceLogsView`: a database's Queries, a container's Events, a unit's
Runs, handed a context with the source, the filter and `openHistory` — History on a stretch of time, on
another of the page's sources if it names one, narrowed if asked), a `window` to open History on (a crash
window, a request's moment, narrowed where the page knows how) and `onLeaveWindow` for when the reader
moves off it, an `ask` for a reading pressed on the page's own grid, an `initialRange`, `readings`
(`true` for tiles from `sm` up, `"chips"` for the counts on the lens row's chips), and `layout="sheet"`
for a detail sheet. It merges `GET /logs/source` under what the page said — a page that answers `{}`,
or a server without the route, loses the facts and nothing else; `described` skips the second read for a
source the page already asked about; a 403 is a refusal said as the server's sentence, with no socket
and no search — keeps its state for the tab under the page's `storageKey` (never `logs.*`, which is the
host page's) or in React state without one, opens an emptied file with its rotated set, clears the
fields when the source's lens changes, keeps a **Read as** on the source it was chosen on, and issues
no search on mount but History's own run.
`service-views.tsx` is where a source's page views are defined once, for the owning page and `/logs`
alike: `docker:` and `stack:` get Events, a unit's journal Runs, a container or a host log that is a saved
connection's server Queries (a container through `/databases/fleet`, a file or unit by asking each of
the host's connections of that engine for its log sources), and an nginx site's access or error log
Requests.

The page (`app/(dashboard)/logs/page.tsx`) is every service page's reading in one place. The rail groups
sources as System, the Systemd journal (with the journal's readings by program: SSH log for an
administrator, Kernel ring, Cron — each only where no file holds the same lines), Containers, **Stacks**,
**Requests** (`request-records.tsx`: each deployment whose route has a record, from `/deploy/traffic`,
and each nginx site with an access log of its own, from the vhost listing, read in the workspace column
through the same `RequestsWorkspace` their own pages use, under the question those pages keep), the web
server, PM2 and applications. A visit with nothing chosen opens syslog (or messages, or the journal),
because nine times out of ten the answer is there. A source switch that changes the lens drops the
fields and applies the new lens's defaults, and what a view or a request opens — "the server log around
this statement", a failed request's error log — lands here, on History around the moment.

The address carries `source`, `mode` (`live`, `search`, `insights` or a view's id), `q`, `unit`, `since`,
`until`, `f` (repeated), `levels`, and `lens` only when the reader forced one; a request record is
`?requests=deploy:<id>` or `site:<name>`, with `mode=insights` for its Insights and the request query in
the request log's own words. Time bounds must be explicit ISO instants with a timezone; they select
History and remain exact through search and reload, including during a repeated daylight-saving hour.
The controls display browser-local time without replacing the original instants. Invalid/reversed link
bounds require choosing a new window before any search. Field predicates and levels a link carries that
the server would refuse are dropped in the browser rather than sent. An explicitly requested source
missing from discovery stays unavailable; only an unselected visit defaults. Rescan or choosing a source
provides recovery. The old `components/log-viewer.tsx` and `lib/log-format.ts` are gone with their last
callers.

Published port conflicts during container creation and Compose startup trigger bounded retries with
available concrete host ports. Existing port owners stay running; bind addresses, protocols, container
ports and unpublished services keep their meaning. `CreateResult.ports` reports actual bindings and is
included in the create audit. Compose keeps generated bindings in `.just-dashboard-ports.yml` (deployment
releases use `<release override>.ports.yml`), invalidating them when source ports change. This file has
mode 0600, contains no resolved environment values, and uses Compose 2.24.4+ `!override` semantics.
Direct host-network applications still require application-specific port configuration.

Compose checks whether saved automatic port mappings still match the source using a pure fingerprint of
network mode and port fields. This check does not bind sockets, so stopping a stack does not depend on
its former listening interfaces still being available. Environment and image edits do not invalidate
saved port choices.

Container recreation parks the inspected container under a random name using Docker's atomic rename.
A parking-name conflict is retried without deleting its occupant. All subsequent mutations use the
inspected container ID. If replacement fails, only the returned candidate ID is cleaned up, then the
original name and running state are restored with a cancellation-independent cleanup timeout. Cleanup
failures report the retained original's parking name.

PM2 discovery cannot expand log roots. Its file paths must pass the configured `JD_LOG_ROOTS` check,
including symlink resolution; custom PM2 log directories require explicit administrator configuration.

## Actionable findings and storage investigation

Docker Overview, the container list and container details all use `components/docker/finding-actions.ts`.
Restart-policy remedies update a standalone container in place; log-driver remedies review replacement,
interruption and loss of logs/writable-layer data. Compose configuration remedies open the owning stack
so the source configuration can be changed durably. Storage and reachability actions focus the relevant
container tab; findings without an automatic remedy can still open configuration evidence. Permission
fallbacks name inspection instead of claiming an unavailable mutation. Group and singleton dismissals
use the same finding-kind key; Rescan restores them without changing the server diagnosis.

The resource editor uses the backend's **PATCH** route. RAM-only updates preserve existing swap
headroom (including explicitly unlimited swap); previously unset swap gets Docker's equal-RAM swap
allowance. Explicit combined limits are kept. Empty, negative and overflowing updates are refused;
Engine warnings remain visible. A real Docker fixture checks that restart policy, RAM and CPU changes
keep the running PID and start timestamp.

Host storage investigation and selected cleanup are documented in [server advisor](server-advisor.md).
An advisor file link opens `/files?path=<parent>&entry=<absolute-file>`: the inspector selects only an
entry already returned by that validated directory listing. An explicit click or deselection overrides
the URL's initial selection. Paths still pass through the existing file service boundary.

Standalone Configuration offers local preparation for readiness commands, image version/digest pins,
port bindings, privileged mode and Docker socket mounts, plus the full supported replacement spec.
Only administrators edit it because it includes credentials and host settings. Preview renders the
reviewed spec; application readiness is not promised. Applying requires destructive capability and
reviewed confirmation, reports Engine warnings and navigates to the returned replacement ID. Auto-remove
containers cannot use this replacement path. Supported settings are carried through; Inspect remains the
source for additional Engine options. Compose remedies open `?tab=compose&remedy=` with a service-level
example, then use the existing validation/save/deploy workflow. Live limits link the owning file too.
Removing Just Dashboard's required socket/host access is explicitly described as breaking its controls.

Docker diagnosis reports unread inspections, disk accounting and log files in `silences`; running
containers with unread inspections increment runtime `unknown`, rather than `noHealthcheck`. The
Attention reclaim action uses `imagesAndCacheOnly=true` on `/docker/prune`, removing only unused images
and build cache. It leaves containers, networks and volumes untouched. The older broad sweep retains
its original scope; pairing images-only scope with volume removal is refused. Every scope is audited.
