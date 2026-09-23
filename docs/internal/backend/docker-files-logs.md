# Docker, files, and logs

## Docker

`internal/dockerx` uses the official SDK over the socket. It shells out in exactly three places —
compose, the streaming compose runner, and `Build` — because the Engine API has no equivalent; all three
build argv explicitly.

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
  because limits genuinely can change in place.
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
  `health_status: unhealthy` justify the feature alone.
- **`CheckUpdate` compares the registry's current digest against the pulled one** — a more useful question
  than "is there a newer tag", because it catches a moving tag that moved. Cached 30 min, four-worker
  pool, because Docker Hub rate-limits by address. Unreachable or credentialed registries are `unknown`
  with the reason; a locally built image is `local`, a digest reference is `pinned`, and neither is a
  failure — reporting "not checked" for something that cannot change reads as a broken check.
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
  Volumes are always listed and never recommended, and the route additionally demands a typed phrase when
  they are in the selection.
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
  already has them, and "what uses this volume" for every volume at once is otherwise an inspect per
  container per poll); `ListStacks` builds on `ListContainers` so it inherits resolved health and uptime;
  `Diagnose` inspects each container once and runs every rule against that payload.
  `ListContainersWithLabels` applies exact label filters in the Engine list call before health/uptime
  enrichment, so a deployment detail read inspects only its matching running containers. The uptime pass
  also collects limits, health-check presence and restart policy from the inspect it was already making,
  and marks the rows it did not inspect (`Inspected`) so the UI never renders an absence as an answer.
  Writable-layer sizes ride along on the stats sampler from the **cached** disk walk — a sampler must
  never trigger one — which is what makes "grew 6.4 GB today" a measurement rather than a guess.
- **`httpx.URLParam`, not `chi.URLParam`.** chi routes on `r.URL.RawPath` whenever a request carried one
  and slices the parameter out of the same string, so a handler receives the percent-escapes the browser
  sent. Every Docker route uses the decoding wrapper; `urlparam_test.go` pins it.

## Files

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
parents). Uploads go through `Upload`, which writes to a temporary sibling and renames into place the
way `Write` does: an interrupted transfer never leaves a truncated file, an existing file keeps its
owner and mode across the replacement (which is what lets the image editor save over a picture the
web server owns), and `?overwrite=true` is what permits the replacement at all. The handler sends one
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
  offsets**. Bounded three ways (time, visits, matches) and it *says* when it stopped early — a fuzzy
  search that quietly answers from a third of the disk is worse than one that admits it.
- **`places.go`**: `Home` prefers `$HOME`, then `/root`, then a single account under `/home`, then the
  first configured root — every candidate checked through `Resolve`, because a shortcut landing outside
  the roots is worse than no shortcut. `Complete` treats a trailing separator as "inside this directory"
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
graphite), returned with the places as `colours`. `PUT /files/colours` (`file.write`, audited as
`file.colour`) sets one path per request, or clears it with an empty colour, so two tabs labelling two
folders cannot undo each other; a name outside the closed set is refused, and so is a path the roots
refuse. The label follows its folder: a move (a rename is one) re-keys it and everything labelled under
it, using `files.MoveEnds` to learn where the entry actually landed, and a delete drops it, so a new
folder of the same name starts blue. Both are best-effort after the filesystem operation has succeeded.

Frontend `components/files/`: the page is **one framed workbench** with no page header above it — a
strip across the top carrying where you are (the folder in its colour, which opens the colour menu, the
path and its star) and every page command (Find, content search, refresh, the view, arrange, the GitHub
account, New, Upload, and the toggles for the two side columns), then a sidebar, the listing and an
inspector as three flush columns with a hairline between each, resizable through `panel-size.ts`. The
sidebar (`files-sidebar.tsx`) has no header of its own and is a fixed list the way a desktop file
manager's is, not a folder tree:
the server's places (home, the roots, the accounts, the notable directories), then the starred folders,
then the recent ones, each a drop target, with the browsed folder marked when it is one of them. It does
not change as the listing walks into folders — the walking happens in the listing. `destinations` in
`places-menu.tsx` builds that list once for the sidebar and for the phone's menu alike, and draws each
place as what it is (`PlaceMark`: `/` as the host's distribution, a home as a folder with a house in it).
`file-icon.tsx` is the vocabulary (~200 extensions, the files with none — Dockerfile, authorized_keys,
lockfiles — and ~90 folders whose name says what they hold) and draws it itself rather than from an icon
set: a folder is a two-tone folder in its colour (its label from `FolderColourProvider`, else graphite for
build output and installed dependencies, else blue) with a product's Simple Icons mark
(`public/logos/mono/`) or a Heroicons glyph pressed into its face; a file is a page with its corner
folded, the format's own logo on it (`public/logos/`, devicon's language marks among them), and a band
along its foot in the format's colour (`--language-*`, `--tag-*`) carrying the extension where the icon
is large enough. `folder-colour.tsx` is the picker — swatches in the inspector, a menu behind the strip's
folder — and `file-actions.tsx` offers the same choice as a submenu on every folder's menu and on the
background menu for the folder being browsed. The inspector (`preview-panel.tsx`) opens on the thing
large — the picture, the video, or its folder or page — with its name, kind and colour under it, and
describes the folder being browsed while nothing in it is chosen. `thumbnail.tsx` draws a picture as itself and a video as its first frame on a
row and a tile alike (images lazily, a video only once it scrolls into view, and playing muted under the
pointer on a tile). `file-actions.tsx` declares every verb **once, as data**, and renders it into the
row's overflow button, the tile's, and the right-click menu (`ui/context-menu.tsx`, one root over the
listing that reads the row from `data-entry-path`); the space between rows gets the folder's verbs.
`dnd.ts` makes folder rows, sidebar places, crumbs and starred folders drop targets for paths dragged from
the listing (Ctrl or Alt copies) and for files from the desktop. `uploads.tsx` is the queue — one
`XMLHttpRequest` per file for progress, three at a time, folders walked through the entries API so a
dropped folder is its contents rather than an empty file named after it — and `conflict-dialog.tsx`
asks once per operation (replace, keep both, skip) before an upload, move or paste touches a name that
is taken; "keep both" is `photo (2).jpg` for a transfer and `photo copy.jpg` for a duplicate.
`media-viewer.tsx` is the full-screen look (a `Modal` at `size="full"`): pictures fit or 1:1, video,
audio, PDF through a blob, and the same head or archive listing the inspector shows for anything else;
Space in the listing opens it, the arrows walk the folder's files. The editor gains a full-screen toggle
and a diff review of the draft against the disk (`diff.ts`, a prefix/suffix-trimmed LCS capped at a few
million cells) drawn by the git page's `DiffView`. The listing polls every twenty seconds and refetches
hidden-file flips in place; the parent row is offered only where the parent is inside the roots; a bulk
delete that includes a folder is typed for like a single one. Two layout rules are easy to undo: **the
listing body does not scroll** (a sticky table header sticks to its nearest scrolling ancestor), and
**the sidebar's tree waits for `/files/places`** before mounting, since it caches and would keep showing
the refusal from listing a root it cannot. The image editor commits each operation to a **new canvas**
rather than a live parameter pipeline — that is what makes undo a stack of bitmaps and why "rotate, crop,
rotate again" behaves the way it looks; saving goes through the ordinary upload route with
`overwrite=true`, so owner and mode survive.

## Logs

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
- **Nothing is offered that cannot be opened**: `Discover` runs `Allow` over the well-known paths, or an
  install that narrowed `JD_LOG_ROOTS` gets a rail of files that refuse to open. Source kinds that cannot
  be queried return an explanation in `missing`; an installed PM2 with no managed processes reports that
  empty state explicitly rather than disappearing from the rail.
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

Frontend `components/logs/`: the page is a workbench like the terminal — one frame, the source rail
(`source-rail.tsx`, hideable and resizable, remembered through `view-state` and `panel-size`) beside
`log-workspace.tsx`, a hairline between them. The workspace is one pane: a strip naming the source with
its kind, path, size and state and the Live/History tabs; `filter-bar.tsx` under it with the one filter,
the window and the journal unit inline and the exclusion, context, archives and boot behind "More";
the histogram; the lines; a footer carrying the stream's state, the line and error counts, the search's
notes and the retention verdict. One filter, because "these errors are scrolling past, when did they
start" is one thought. Live applies as you type (debounced; the socket restarts, which is what makes the
prefill meaningful); History runs on Enter, because a keystroke-triggered full scan would queue a pass
over gigabytes per character. `log-console.tsx` draws each line as columns — a level edge, the line
number, the clock, the level's word in its colour (`LEVEL_MARK` in `lib/log-filter.ts`, `LEVEL_WORD` in
`log-text.tsx`), the journal unit in its lane's hue, then the message drawn by its shapes: `lib/log-tokens.ts`
cuts the text into spans (time, host, program, pid, level, key, string, number, address, URL, path,
method, status, id, failure and success words) over the original string, so the server's match ranges
still intersect them, and `log-text.tsx` colours them from one map (design-system §14). While the time
column is on, the line's own leading timestamp is not drawn, and neither is a syslog hostname that is
this host's. Error and critical rows are washed, warnings too. A line the server parsed as structured
(`message` and `fields` on the wire) is drawn as its message and fields in logfmt order, most telling
field first — except in a History result, whose match ranges are over the raw JSON. Tokens are cached by
text, because a server's log repeats itself, and each row is memoised, because the live tail appends. A
"Colour" toggle, persisted with Wrap and Time in `lib/log-view.ts`, shows every line exactly as written.
The source rail draws each source as its product (a container as its image, nginx, PM2, the system files
as the host's distribution). The console uses `content-visibility` rather than a virtualiser: off-screen rows skip layout while the scrollbar
stays honest, wrapped rows keep real heights, and the browser's own find still works. The level chips on
the strip above the lines carry the on-screen counts and share their swatches (`LEVEL_DOT`) with the
level column and the histogram. **Pausing holds incoming lines instead of dropping them.**
`histogram.tsx` is matches by level over time; clicking a column narrows the window to it. The
deployment pages embed the same workspace framed on its own.

Logs accepts `source`, `since` and `until` URL parameters for deployment handoffs. Time bounds must be
explicit ISO instants with a timezone; they select History and remain exact through search and reload,
including during a repeated daylight-saving hour. The controls display browser-local time without
replacing the original instants. Invalid/reversed link bounds require choosing a new window before any
search. An explicitly requested source missing from discovery stays unavailable; only an unselected
visit defaults to the first source. Rescan or choosing a source provides recovery.

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
