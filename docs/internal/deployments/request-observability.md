# What a deployment served: requests, output and lifecycle

## The problem

`Deploy › Project › Logs` showed one thing: the live container's standard output, through the same
`LogWorkspace` the host-wide Logs page uses, with the source pinned to `docker:<containerId>`.

For a modern framework that is a startup banner and then silence. A Next.js, Rails or Django
production server prints nothing per request, so a healthy deployment serving a thousand requests a
minute showed thirty-nine lines, ending at `Ready in 236ms`, and never changed again. The page was
not broken; it was answering a question nobody had.

Three defects sat on top of that, all visible on the first deployment anybody opened:

- **stderr was read as a level.** An unclassified stderr line was filed as `error`, which is true of
  a crash and false of npm's notices, Prisma's "Update available" banner and every CLI that treats
  stderr as a second stdout. A freshly deployed, healthy project reported *13 errors*, every one of
  them a version notice inside an ASCII box.
- **Terminal control was rendered as text.** A build tool's progress spinner arrived as
  `B[2KB[1AB[2KB[G Generated Prisma Client`, and the escape bytes were part of the string the level
  scan and the operator's search then ran over.
- **Structured output was unreadable.** An application logging `{"level":"error","msg":…}` got its
  level from a word scan that found either nothing or a key name.

And nothing anywhere recorded a single request to any deployment. `renderDockerCaddyRoute` emitted
`reverse_proxy` with no `log` directive, and Caddy's per-site access log is off by default.

## The decision

"Logs" is one word covering three questions. The page answers all three, in one pane, over one
timeline:

| View | Question | Source |
| --- | --- | --- |
| **Requests** | Is it serving traffic, and how well? | The ingress's access log |
| **Output** | What did the application print? | The container's stdout/stderr, as before |
| **Events** | What happened to the container? | Docker's own event stream |

The ingress is the right layer for the first because it is the one place every request passes
through regardless of what the application chose to write about itself — and it works for every
framework without asking the operator to add request logging to their app.

## Requests

### Recording

`DeploymentRoute` gained `AccessLog`, set by `deploymentRoute` in `activation_executor.go`. The two
ingress drivers already differed and still do:

| Driver | Where | Format | Latency |
| --- | --- | --- | --- |
| Docker Caddy | `/config/just-dashboard/access/<route>.log`, inside the ingress's own `/config` volume | Caddy JSON | yes, `duration` |
| Host nginx | `/var/log/nginx/<route>.access.log` | stock `combined` | **no** — `combined` has no `$request_time` |

nginx has recorded these routes since `deploymentSiteSpec` first set `AccessLog: true`; only the
Caddy driver was silent. The rendered block is:

```
log {
  output file "/config/just-dashboard/access/just-dashboard-env-7.log" {
    roll_size 16MiB
    roll_keep 4
    roll_keep_for 336h
    roll_uncompressed
  }
  format json
}
```

Rotation is Caddy's own: the ingress volume is on no logrotate schedule this dashboard controls, and
an access log is the fastest-growing file a deployment produces. Rolled generations stay uncompressed
so the reader can pick one up from an offset — the tail of a file that rolled between two reads is
recovered from wherever the roller put it, and a gzip has no offsets. The log path goes through
`dockerCaddyAccessLogPath`, which reuses `dockerCaddyRoutePath`'s check — a name that cannot be
trusted to build a config path cannot be trusted to build a log path either, and this one is handed
to Caddy, which creates whatever it is pointed at.

The file lives **inside** the container rather than on the host because the ingress may be one the
operator already owned; adding a bind mount would mean recreating their running web server to turn
on a log. Reads go through one `docker exec` per read (`caddyReadScript` in
`deployment_access_log.go`), the same mechanism route files already use.

`RemoveDeploymentRoute` deletes the Caddy record and its rolled generations with the route. It holds
client addresses, and leaving it behind after the deployment it described is gone would keep personal
data on the host with nothing left to read it. nginx's file is left alone — it is in the host's log
tree under logrotate's management, which the existing code has never touched.

### Reading

An access log has no index. It is append-only and time-ordered, so the only way to answer "the last
hour" from the file is to read from somewhere before the last hour to the end — and with nothing to
say where that is, the first version of this page read the last 24 MB on every poll. Two pollers on
one page made that two scans a minute of the same bytes for the same figures, and a busy
deployment's hour did not fit in the window anyway.

`accesslog.Store` makes the decision the metrics recorder and the Docker event log already made:
this process is long-running and already attached to the thing producing the record, so it reads
each byte once and keeps what it parsed.

- **One record per route, seeded then advanced.** The first question about a route reads the live
  file and then as many rolled generations as a 48 MB budget allows, newest first, in the order they
  were written. Every later question reads only what was appended since — a stat and a short read
  through the same `exec` — at most once per 400 ms however many pollers and sockets are open on the
  route. A quiet deployment's whole retained record fits; a busy one holds its newest 48 MB and says
  so.
- **A time index in memory.** Entries are held in file order with a running high-water mark of their
  instants. Requests land in the file in completion order, so two that overlapped can be a few
  milliseconds out of time order; the running maximum is monotone regardless, and a window's start
  is found in it by binary search — exact, because everything before the index found is older than
  the window and the filter decides the rest.
- **Rotation, by identity.** A reader addresses a generation by inode, not by path. A read of the
  live file names the inode it expects; when the file has rolled, the container-side script finds the
  old generation with `find -inum` wherever the roller put it, opens it and holds it — an open file
  keeps its inode however it is renamed — reads the tail past the cursor, then the new live file from
  its start. Intermediate generations rolled between two reads are read too, by modification time.
  The host-file reader for nginx does the same in Go against logrotate's `.1`. Truncation in place
  (`copytruncate`) is recognised by the same identity at a shorter size, and a generation that is
  gone before its tail was read is reported as a hole rather than silently skipped: `Coverage.Complete`
  goes false, and the page says every figure is a floor.
- **A cursor, not a timestamp, for the tail.** Every request gets a sequence that only rises within
  the process's record of its route. The window hands the page its cursor; the socket asks for what
  comes after it. That is exact where "newer than the last time I saw" is not — two requests in one
  second are two, which in nginx's format is every request.
- **Bounded, and gone when nobody is looking.** 150k requests per route, 300k across routes (the
  least recently asked-about route is dropped whole), nothing older than eight days, and a route
  unasked-about for half an hour is dropped. Strings that repeat across a record — path, user agent,
  client address — are interned, so a hundred thousand rows from one browser share one copy of its
  name; at roughly 180 bytes a request a full route is under 30 MB.
- **Stale rather than refused.** A read that fails against a record already held serves the previous
  answer with `Coverage.Stale` set; the page says the ingress did not answer and when it last did.
  Nothing held and the read failing is the one case that is an error.

The container-side script was validated against `caddy:2-alpine` (busybox 1.37): `stat -L` through
`/proc/self/fd`, `find -inum`, `tail -c +N` on a held descriptor, and `caddy validate` of the rendered
block. `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveCaddyAccessLogReader` runs it against
the image: a roll under the reader, the rolled generation found by inode with its tail recovered, the
new live file, a vanished inode reading as absent.

`accesslog.Collector` folds the window into one `Result` in a single pass:

- **Rows** are the newest `Limit`, and the readings are computed over the *whole* window — a table
  showing the last 500 requests must not report a p95 of only those 500.
- **Latency** is a distribution (`p50/p75/p90/p95/p99/max/mean`), nearest-rank rather than
  interpolated, so a reported percentile is a duration some request actually took and can be found in
  the rows. A mean alone is the one number that hides the problem.
- **Facets** carry their own error counts, so "the busiest path" and "the path that is failing" are
  one table read two ways.
- **Slowest** is a running top-5 during the scan, not a sort of the row buffer: the slowest request
  of the day is almost never in the last 500.
- **Buckets** are 60 columns over the requested window, counted by status family, each with its own
  p95.
- Bounds: `latencyCap` 250k samples, `facetCap` 20k distinct values per dimension. A path with a
  request id in it produces a new key per request; without a bound the top-paths table is a memory
  leak that happens to render. Hitting either sets `Truncated`, and the page says the figures are a
  floor.

`deploy.ObserveRequests` maps an environment to `deploymentRouteName(environmentID)` and asks the
store. `RequestRecord` is an interface so the read model is testable without a Caddy container and
a Docker socket. Transport errors never reach the client: they carry socket paths and daemon
addresses, so the reason shown is the operator's next move.

### API

| Route | Answers |
| --- | --- |
| `GET /deploy/{id}/requests` | The window: rows, readings, facets, chart columns |
| `GET /deploy/{id}/requests/stream?after=<cursor>` | WebSocket: every 400ms, what the store holds past the cursor, batched — a deployment under load writes thousands of lines a second and one frame each would spend the budget on envelopes |
| `GET /deploy/{id}/lifecycle` | Docker events for this environment's containers |

An unbounded read walks the whole retained record to answer a question about "now", so a missing
window defaults to the last 24 hours and is reported back — an empty answer is never read as "nothing
ever happened".

A window costs one refresh of the held record — a stat and a read of what was appended since —
plus a pass over the entries in the window, which for an hour of a busy deployment is milliseconds.
The page's four readings and the Requests view's own poll share that refresh, and the live socket
is another reader of the same record rather than a second follower of the file: one process reads
the log, however many pages are open on it.

These sit on the same authenticated group as every other deployment read, with no extra capability.
That matches the existing boundary rather than widening it: `/logs` already serves
`/var/log/nginx/access.log`, which is the same data for the same host.

## Lifecycle

`dockerx.Event` gained `Owner`, the dashboard's own labels off the actor's attributes with the
`io.just-dashboard.` prefix stripped. Docker puts every label there; keeping this one namespace is
what lets a project page show its own restarts and OOM kills without inspecting a container that is
by then already gone. Only the dashboard's prefix is kept — an arbitrary image's label set is
unbounded and none of it is ours to render.

The buffer is host-wide and bounded (2000), so `handleDeploymentLifecycle` asks for a wide slice and
narrows by `Owner["environment-id"]`: asking for only `limit` events would return a hundred belonging
to other containers and none belonging to this one. The `since` bound is served here rather than
computed in the browser — a reading like "restarts in the last hour" worked out during render reads
the clock on every re-render, so the figure would depend on when React happened to paint.

## The three output fixes

- **`StripANSI`** (`logsx/structured.go`) resolves CSI, OSC, DCS/SOS/PM/APC, backspace, and carriage
  returns — a progress bar's whole animation arrives as one line, and the last frame is what the
  terminal would have shown. It runs in `ParseLine`, before the level scan and before the text the
  operator searches is decided.
- **`parseStructured`** reads JSON log lines: `level`/`severity`/`lvl`/`levelname`,
  `msg`/`message`/`event`, `time`/`ts`/`timestamp`, numeric levels on both pino's scale (10–60,
  upward) and syslog's (0–7, downward). The remaining keys become `Fields`, capped at 24 keys and 512
  bytes each. The raw line is kept in `Text`, so a search for a request id still finds it. A line
  that merely starts with `{` is not enough — a pretty-printed fragment inside a stack trace does too.
- **stderr is no longer promoted to a level**, in `dockerLine`, `followPM2` and `logsx/search.go`.
  The stream is recorded on the line and the viewer marks it; a level the line does not claim is the
  page inventing a reading.

`LogLine` gained `message` and `fields` on the wire. Both are absent for the plain text that is most
of a host's logs.

## Frontend

`ProjectLogs` is a `StatGrid` of four readings over a `Pane` with the three views. The readings are
the page's own, over a fixed last hour, so they hold still while the reader narrows the rows beneath
them — which is what lets the error rate be the thing that sent them to Output in the first place.
Each is a rate or a share rather than a count: a figure whose meaning depends on a control somewhere
else is a figure people learn to ignore.

`lib/requests.ts` holds the vocabulary. A request log is read by status **family**, not by code —
nobody scans for 418, they scan for "is anything 5xx" — and the four families are the chips, the
chart's stack and the colour of the code in the row. Only 5xx and 4xx take a hue; the rest are steps
of ink, exactly as the log histogram beside them is, because brand blue is a command's face or a
location mark and a chart of served requests is neither.

The socket opens with the window's cursor (`after=`), and rows are keyed by sequence, so a live
prepend neither remounts the list nor shows a request twice.

`RequestConsole` draws rows in the log console's own anatomy rather than a `<table>`: a request record
is read the way a log is read, and a nine-column table at this density spends its width on cell
padding and its maintenance on breakpoint rules. Following holds the *top* — newest first — and
pausing holds what arrives rather than dropping it, both carried over from the log console next door.
`content-visibility` rather than a virtualiser, for the same three reasons: an honest scrollbar, real
row heights, and the browser's own find.

`RequestChart` stacks the families and rides the p95 over them as a row of marks. The two readings are
one chart deliberately: a wall of red with a flat p95 is a deployment refusing requests, and the same
red with the p95 climbing is one falling over. Those are different afternoons, and two charts side by
side make the reader correlate them by eye.

### On component libraries

The registry ecosystem has close prior art — openstatus's `data-table-filters` (TanStack Table, nuqs,
dnd-kit; faceted counts, drag-to-zoom on a timeline, a live toggle that prepends rows) and several
shadcn access-log blocks. The **patterns** were taken: status-family colouring, a latency bar scaled
against p99, faceted counts on the chips, drag-to-narrow on the chart, live-as-a-toggle rather than a
tab, and row detail opened in place. The **dependencies** were not. This product already has its own
`Pane`, `StatTile`, `FilterChip` and log console; adding TanStack Table and three state adapters to
render rows those primitives already render would have put a second design system in one page. The
registry blocks also draw a filled colour chip per HTTP verb, which turns a column of GETs into a
column of rectangles competing with the status for attention — the method is a word here, and only
the verbs that change something step forward in weight.

## Boundaries

- A deployment with no public route has no request record, and the page says so rather than showing
  an empty table.
- nginx-served deployments have no latency column at all. The page names the reason instead of
  drawing dashes.
- What is held is a tail of the retained record when the seed budget, the cap or a roll cut it, and
  every figure taken from one is reported as a floor with the instant it begins.
- The record is in memory, per process. A backend restart re-seeds each route on its first question
  and sequences start again; a page holding an older cursor gets everything held rather than nothing,
  and dedups by sequence.
- The Docker event buffer is memory-resident and bounded; it is an hour of a busy host, not an audit
  trail. What the dashboard itself did is in `audit_log`.
- Access logs hold client addresses. They are removed with the route, and they sit behind the same
  authentication as every other host log this dashboard serves.

## Verification

```bash
cd backend && go build ./... && go vet ./... && go test ./...
cd ../frontend && bun run lint && bun run build && bun run test:browser
```

`internal/accesslog` covers both formats, the junk that is not either, the filter, nearest-rank
percentiles, facet error counts, histogram coverage of the requested window, that the row limit does
not cap the readings, and the store against a fake disk: that only appended bytes are read, that a
half-written line waits for its newline, a roll with and without the old generation surviving,
truncation in place, the seed budget and its ordering of generations, eviction past the cap without
renumbering, `After` from a cursor, `FromNow` and a stale cursor, out-of-order instants inside a
window, the idle sweep, an absent record that later appears, stale-on-failure, and a removed route
emptying. `internal/proxysvc` covers the rendered log block, its rotation and `roll_uncompressed`,
the framing of the container script's two reports, the host-file reader across appends, a rename and
a truncation, and — live — the script against `caddy:2-alpine`. `internal/logsx` covers ANSI, carriage-return frames, structured levels on
both numeric scales, and that a brace alone is not JSON. `internal/proxysvc` covers the rendered log
block, its rotation, and that the reader and `sites_render` agree about nginx's path.
`internal/deploy` covers the observer against a fake ingress. `tests/browser/deploy-requests.spec.ts`
covers the three views, the readings, chip narrowing, the opened row, the absent-record sentence, and
that the page fits at 390 and 1280.
