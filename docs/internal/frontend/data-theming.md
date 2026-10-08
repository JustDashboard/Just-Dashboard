# Frontend data flow and theming

Network pages retain useful readings when a poll fails and mark them with
`components/network/read-warning.tsx`; a failed initial read stays an error or loading state.
Historical traffic, namespace and BGP read errors are shown explicitly. Network write controls follow
the administrator capability, with destructive controls also following the destructive capability;
backend route checks remain authoritative. VPN peer reads are disabled for a reader without the
administrator capability. Interface sheets are keyed by device, and address/MTU drafts clear only
after a successful write. Their bridge selector reaches the existing guarded membership route, and
the create dialog includes the IPv6 GRE/GRETAP kinds the backend supports.

Network diagnostics retain independent inputs/results/history and reject malformed TCP ports before
submitting. Host support, route lookup, path MTU, packet snapshots and Wake-on-LAN use the same probe
surface; Wake-on-LAN reports that a packet was sent rather than claiming the target is awake. The
subnet calculator uses pure IPv4/IPv6 arithmetic with exact IPv6 counts. DNS comparison sends a name
only to configured resolvers by default; the reader explicitly opts into public presets through
`includePublic`. Resolver changes may carry a private `verificationName` for a network that cannot
resolve public names. This verification input is a check for that apply, not saved resolver state.
The managed WireGuard full-tunnel label states its IPv4 egress and the blocking of IPv6 to prevent
leaks until dual-stack egress is configured.

- `src/lib/api.ts` is the only fetch layer: `get/post/put/patch/del`, `credentials: "include"`,
  `X-JD-CSRF` on every mutation, URI-encoded exact `X-Confirm` with
  `X-Confirm-Encoding: uri` only when a typed phrase is supplied (including Unicode and surrounding
  whitespace), `ApiError` with
  `needsConfirmation`/`isAuthProblem`/`needsTotp` and the whole parsed `body`, for a refusal that
  carries more than the error (the proxy engine's config-test refusal carries the test); `wsUrl()` and
  `downloadUrl()` build the non-JSON URLs. A `Query` value may be an array, which is a repeated
  parameter (`f=a&f=b`, the log routes' field predicates): joining on a character and splitting it again
  on the server breaks on a value that holds it, and an IPv6 address is all colons. `useSocket` takes the
  same `Query`.
- A log source is asked for by one id, built only in `lib/log-sources.ts` (`dockerSource`,
  `fileSource` → `file:<path>`, `journalSource`, `journalIdSource`, `kernelSource`, `stackSource`,
  `pm2Source`): an id spelled by hand — a bare path here, `file:` there — was a different session key for
  the same file and opened on the wrong remembered filter.
- `usePoll` schedules the next request only after the previous one settles and pauses scheduled
  requests on hidden tabs. Its fixed-length dependency list identifies the resource: changing it
  immediately hides the previous resource's data and resets loading. Refreshes and cadence changes
  retain the same resource's data. Cleanup aborts the request and ignores late responses.
  Live Processes treats its search and filters as resource identity, and ranking/row-count
  changes as refreshes of that inventory. Automatic focus therefore keeps the table and scroll
  mounted while fetching its new ranking; response-local focus metadata keeps the footer truthful
  during that read. `components/procs/inspection-order.ts` holds row order during pointer or focus
  inspection across the Processes lists without freezing values or retaining exited processes.
- Database activity (`components/database/home/use-samples.ts`) reads retained snapshots from
  `GET /databases/{id}/stats/history` every 30 seconds, independently of the live snapshot read.
  The selected 1h, 6h, 24h or 7d range is view state per connection; the samples live in the backend
  store, rather than a browser-owned rolling buffer. Failed refreshes retain the last result, and a
  stopped database can show history without dialing its engine. Counter resets and recording gaps
  longer than 90 seconds break chart lines and rates; missing snapshots are ignored. Historical
  working-session counts are the recorded active count minus the recorded waiting count, while live
  session filters use the current session list. SQLite has no activity chart or recording poll.
- `useSocket` — reconnect with backoff (these sockets ride a tunnel that drops routinely), handlers in a
  ref so a fresh closure does not rebuild the socket.
- Persisted view/session values use individual Web Storage entries, so changing a small filter does not
  serialize unrelated editor drafts. Writes remain synchronous for reload persistence. Existing single
  documents migrate on first read, with restoration and the old persistence path if splitting exceeds
  quota. Prefix deletion and sign-out remove the same values; the memory store never writes to storage.
- `components/workspace/` keeps row identifiers, field indices and scroll offsets per page/step/
  log question. Logs and Login place memory use RAM; field values and server row payloads never
  enter these snapshots. `useFilterHistory` pushes settled Audit/Security filter questions and
  restores native Back/Forward. `useHeldList` preserves existing row order in React state while
  polling updates values, and exposes counted new arrivals for explicit reveal. Git commit messages
  and per-run backup archive selections are session drafts. See
  [`workspace-interactions.md`](workspace-interactions.md) for the complete contract.
- `lib/view-state.ts` is what a page remembers about itself, in three stores drawn by how long the
  thing should live. `useViewState` is **how the page is arranged** — a hidden panel, a chosen tab, a
  sort order, a toggle — in localStorage, so a reload keeps it. `useSessionState` is **what you were
  doing** — the filter in the box, the chip narrowed to, the page of results, the row whose detail is
  open, the SQL in the editor, a form half filled in — in sessionStorage, so it survives moving between
  pages and a reload and is gone when the tab closes. `useMemoryState` is the same for a value that must
  never be written down by the browser (a secret in an unsaved form: a credential, a database password,
  a backup destination's keys, an environment value): it lives as long as the page's JavaScript does,
  across navigation but not a reload. All three have `useState`'s shape; a dotted key names the page and
  the thing, and a third argument to `useSessionState` is the value the address bar handed over, which
  wins on arrival and is remembered from then on. A form draft is keyed under the revision or name it
  was read from, so a save from anywhere starts it again from the server's copy — except where one
  revision covers several forms: every deployment settings form saves a new revision of the whole
  configuration, so each keys its draft on a digest of its *own* saved value (`useSettingDraft`), and
  a save of the form beside it leaves the draft alone while its own save still restarts it. A
  dialog's fields are
  forgotten (`forgetSessionState`/`forgetMemoryState` by prefix) when it is closed by hand, never by
  navigation; and `forgetWorkingState` empties both working stores on sign-out. `useQuerySelection`
  keeps a sheet's selection in the address bar and, per page and key, in the session store, so
  arriving on the rail's bare link puts the last selection back with `replaceState`. A database's
  layout does the same for the reader's place inside it — `?schema=&table=`, `?db=&collection=` — per
  database (`databases.<id>.place`, `database/shell/place.ts`): a page's address holds only what that
  page can use, so the place the last address stated is kept, every link the shell builds
  (`useDatabase().href`) carries the part its target can hold, and a bare address is completed from it.
  Which database is the path (`/databases/<id>`), never remembered. Inside a database a page writes its
  address through the layout's context and never with a `router.replace` of its own, which the
  context's next write would undo: `useDatabase().select` replaces, with the writes made in one press
  batched into one change and read back at once, and `goto` pushes. The shared keys are the selection
  (`schema`, `table`; `db`, `collection`; `db`, `key`); what a page adds is its own view of it — a
  filter list, a sort and a page on Data, an object and a view on Schema, a view strip's choice, an
  open session or statement on Performance — so a pasted link opens on what was being looked at, and
  opening a table, a key or a view is a history entry while narrowing one is not. A statement handed
  to Query (`?sql=`, `?saved=`) is read once, opened in a tab of its own and taken out of the address.
  The stores follow the same three lifetimes under one naming: arrangement is `useViewState` under
  `databases.<area>.…` where it is the area's (a rail shown, a split's share, the last generator) and
  `databases.<id>.<area>.…` where it is one database's (a page size, a column layout per table, saved
  pipelines, the diagram's copy of an arrangement the server also keeps); work in progress is
  `useSessionState` (`databases.<id>.query.tabs`, a new-table draft, the place, and the rail's memory
  of the databases this tab has opened, `databases.known`, which holds names and so must not outlive
  a sign-out); and whatever holds rows of data or a secret — a staged change set, what a tab ran,
  unsaved Redis values, a console's transcript, staged document edits, a connect form's password — is
  `useMemoryState` and never reaches Web Storage. One thing is kept beside the stores on purpose: a
  change of power in flight is written to `sessionStorage` and told over a `BroadcastChannel`
  (`jd.databases.power`, `database/home/power.ts`), so a reload, or another tab, shows a start or a
  stop that is under way. Every route area was reviewed for this:
  filters, chips, facets, pagination, chosen sub-tabs, open detail rows, in-progress forms and the
  whole new-project flow are remembered; a search box is no longer the exception it used to be. A
  service's logs keep their reading — source, view, filter, lens, range, window — under the embedding
  page's `storageKey` (`databases.<id>.logs`, `docker.container.<id>.logs`, `docker.stack.<name>.logs`,
  `deploy.<id>.output`, `proxy.site.<name>`, `security.ssh.log`, `packages.log`, …), never under
  `logs.*`, which is the host Logs page's own; a sheet's live only as long as it is open. A request
  record's question is kept under its owner's key (`deploy.<id>.requests`,
  `proxy.site.<name>.requests`), so a deployment's or a site's requests read the same question on its
  own page and on `/logs`.
- `useMetricsWindow` — the charts' window as a **stack**: zooming is exploratory, so the way out of five
  minutes is the hour it was inside, not the day you started from. Deliberately component state — a named
  range is a standing choice, a zoom is a question being asked now, and restoring yesterday's zoom shows an
  empty window with no obvious way out. `useMetricEvents`/`useHealth` poll on much slower cadences.
- `src/lib/types.ts` mirrors the backend's JSON by hand, including the `Capability` union — it drifts if
  backend types change without it. The proxy pages' shapes live in `src/lib/proxy/types-*.ts`, one file
  per area of those pages, and `types.ts` re-exports them, so an import from `@/lib/types` finds them. `useAuth`'s `can("capability")` hides controls a role cannot use:
  **affordance only**, the server re-decides every request.
- An account that owes a password change remains unauthenticated in the UI after completing any
  required second factor. `/login` presents the current/new password form, then returns to credentials
  after the server revokes sessions. `password_change_required` responses also return the shell to this
  flow; they never grant access to ordinary feature controls.
- Database selection belongs to a specific query and result snapshot. Sorting, filtering, paging,
  refreshing, and changing tables require a fresh selection. A staged change set is scoped to one
  connection and table (`useChangeSet({ scope })` in `database/grid/change-set.ts`): leaving the table
  takes the set and its undo history with it, and the page asks before a dirty one is left. Exact
  integer/decimal SQL values and Redis scan cursors travel as strings; `lib/db-values.ts` preserves
  precision and rejects non-finite ordinary numeric input, the grid computes on them as strings
  (`database/grid/decimal.ts`), and a JSON value is laid out and compared as text, token by token,
  never through `JSON.parse`, so a `9007199254740993` goes back as it came. NULL is never drawn or
  written as an empty string. CSV exports escape column names and carriage returns with the same
  rules as cell values.
- What a database engine is, the frontend knows in one module: `components/database/engine.ts`. It holds
  how an engine is presented — its name and logo by flavour, the words it uses for its objects, which
  page a capability opens, how a program connects to it — and states no capability: every flag comes from
  the server's catalogue (`GET /databases/drivers`, per driver and per flavour) and, for one connection,
  from its summary (`GET /databases/{id}`), resolved for the product that answered. A page asks
  `engine.kind`, `engine.can(flag)`, `engine.has(section)` and `engine.nouns`; no other file decides from
  a driver's name what an engine can do. What stays beside the pages is how an engine is *written*, which
  no flag says: each dialect's quoting, keywords and diagnostic snippets for the editor
  (`database/query/dialect.ts`, `keywords.ts`, `snippets.ts`), each engine's index methods and
  foreign-key actions for the schema forms (`database/schema/engine-notes.ts`), and how each driver's
  connection string spells TLS (`database/connect/dsn.ts`, beside `lib/db-dsn.ts`). The registry's tests
  and the browser fixture read the catalogue from the file a Go test holds to the route
  (`backend/internal/api/testdata/database-drivers.json`), so a capability given to an engine or taken
  from one reaches the frontend's tests as the table the server really serves.
- PM2 actions and deletes send both the trusted `daemonId` as `user` and numeric `id`, and a PM2
  application's logs are the `pm2:<daemon>/<id>/<name>` source on `/logs/stream` (`pm2Source`, the
  account and name escaped): the application name alone cannot identify a process across multiple
  account-owned daemons. A false `logsAvailable` shows the server's `logsUnavailableReason` instead of
  opening a rejected socket. Available log streams show their connection state, including reconnects,
  and a stream that ended says Stopped rather than reconnecting for ever.
- Blueprint catalogue entries expose `deploymentSupported` and `unavailableReason`. Unsupported
  entries remain visible with their reason but cannot be selected or inspected for deployment.
  Template selection keeps a stable details column while fetching, preserves per-template edits, and
  ignores obsolete inspection responses. The new-project flow keeps secret-bearing inputs in memory;
  saved environment values resume as masked names from the encrypted server draft. Hostname changes
  update only unchanged template-derived URL defaults, and retain visitor password protection.
- Compose stack creation, file edits, validation, and execution require `system.admin` alongside each
  action's existing capability. Stack pages hide those controls from limited accounts, explain the
  restriction, and retain stack/config/log read views. Direct container controls retain their separate
  capability checks.
- `ConfirmDialog` collects a phrase only for permanent deletion of an archived deployment project,
  deletion of an entire database, or Docker stack removal. The server checks those phrases too
  ([`invariants.md`](../security/invariants.md),
  [`permanent-deletion.md`](../deployments/permanent-deletion.md)). Other destructive actions use the
  ordinary dialog; `phrase` stays optional.
  The dialog toasts "<title> completed" when the action resolves, unless the action resolves to
  `"reported"`: an outcome that is only partly what the title promises — a site deleted but nginx not
  reloaded — is announced by the action itself, and a bare "completed" beside it would be the untrue
  half.
- `lib/metrics-store.ts` keeps the live series **outside React**: owning five minutes of history in a route
  component threw it away on navigation, and pushing a 2 s frame through a context above the router
  re-rendered the terminal and log tail twice a second. Mirrored to sessionStorage so a reload keeps its
  chart, with points older than `STALE_MS` dropped on the way back in — a graph silently stitching this
  minute onto one from an hour ago is worse than one that starts empty.
- `lib/metrics-range.ts` defines the windows (`live`, `1h`, `6h`, `24h`, `7d`), their buckets and cadence,
  and the `MetricsWindow` a dragged span becomes (fixed in the past, fetched once, never re-polled).
  **Live and recorded data are never spliced into one line** — the cadences differ by two orders of
  magnitude, and a chart drawing twenty coarse points and a hundred fine ones at equal spacing lies about
  when things happened. Container charts offer only recorded ranges; the container Usage tab also has
  a separate `container-live-usage.tsx` reading section on its existing stats WebSocket. Its rate helper
  differences Docker timestamps and per-interface counters, rejects resets and gaps, and establishes a
  new baseline after reconnects. Pausing freezes labelled readings; a disconnected or ten-second-stale
  feed clears current figures without hiding recorded history. `hooks/use-metrics.ts` and
  `hooks/use-metrics-history.ts` are the React surface over those two.
- `hooks/use-self-update.tsx` is one poll for the whole shell, and its gotcha is the feature's design
  problem: **the API goes away in the middle of the thing it is watching**. A failed poll during a run
  renders as "restarting", never an error, and the cadence is set from the fetch rather than an effect so a
  failure does not drop back to the five-minute interval. `components/update/` is the sidebar-footer notice
  (which renders *nothing* when there is nothing to say), the release-notes sheet, and the panel on
  `/dashboard` — that page is the dashboard's own version and nothing else, because the server's packages
  and the tool you look at it through are updated by completely different machinery.
- `hooks/use-self-config.tsx` is the same shape for the dashboard's own settings, with one problem the
  update flow does not have: a change to the port or the address means the dashboard **does not come back
  here**. Nothing on the client can follow it, so the run record carries the new endpoint and
  `components/config/restart-progress.tsx` states it as a URL to open rather than spinning on an address
  that is now answering nothing. It also renders a fourth outcome — `rolled_back`, the configuration that
  did not come up and was undone — because showing that as either success or failure would misreport it.
  The form on `/dashboard/configuration` **derives** its draft from the poll rather than mirroring it into
  state: a copy refreshed every two seconds would wipe half-typed input during a restart.
- `hooks/use-arrivals.ts` answers which rows of a polled or streamed list were not in it the last time
  it changed, so those rows alone take `animate-rise` (request rows, container events, deliveries,
  players). It is empty on the first render and holds its answer until the keys change again, kept as
  state adjusted during render rather than in an effect, so an arrival costs no second paint and a
  re-render mid-rise does not cut it short.
- Both runs' transcripts are drawn by `components/run-transcript.tsx` over `lib/transcript.ts`, and a
  deployment's build console paints its lines with the same row and the same drip
  (`components/transcript-line.tsx`, which both import). The polled
  report carries only the file's last 64 KB; when that tail starts with the server's trimmed marker the
  console reads the whole file once (`getText` on `…/update/log` or `…/config/log`) and from then on
  extends it with each tail, finding where the tail begins in the whole copy (`extendTranscript`) and
  reading the file again only when the two stop overlapping. A read that fails during the restart leaves
  the tail on screen. While a run is live and the reader is at the end, the lines a poll brought are let
  out a few a frame rather than landing at once; scrolled up, searching or with reduced motion they are
  drawn outright. `lib/transcript.test.js` covers the line shapes and the merge.
- **There is one theme.** The light palette was removed: every tinted surface, status hue, chart colour
  and terminal ANSI slot had to be chosen twice and verified twice, and the second set was seen by almost
  nobody. Colours live on `:root` in `globals.css`. `lib/themes.ts` is now one function —
  `themeBootstrapScript()`, inlined in `<head>` with the request's CSP nonce — which puts `.dark` on the
  root element before first paint so the generated shadcn primitives' `dark:` variants resolve, sets
  `color-scheme`, and clears the stored light/dark preference from anyone upgrading. `hooks/use-theme.tsx`,
  the top bar's toggle, the palette's theme commands and `/appearance` are all gone; `<html>` keeps
  `suppressHydrationWarning` because the class is still applied by script.
