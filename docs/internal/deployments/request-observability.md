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
| **Insights** | What does the window add up to — which page fails, who is scanning, where visitors come from? | The same record, faceted |
| **Events** | What happened to the container? | Docker's own event stream |

The container's own output is no longer a view of this page. For a modern framework it is a startup
banner and then silence, and a tab that never moves teaches the reader to ignore the page. The lines
stay one press away from a failing request — "Container output around this moment" opens the host
Logs page on the live container for the minute either side (the same handoff a run's own logs use) —
which is the only time anybody wanted them. The run page keeps its runtime-logs view.

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

A route written before this existed carries no block, and nothing would rewrite it until its next
deploy — so the ingress lifecycle worker's 30-second pass (`reconcileRoutes`) adds it in place through
the same write, validate, reload and restore-on-failure a network repair takes, recorded as a
reconcile. `renderAccessLogDirective` is the one spelling of the block, so an upgraded route and a
freshly activated one are byte-identical and the next pass reads the upgraded one as recording.

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

- **Page views** (`Summary.Pages`, `Filter.PagesOnly`) are documents a person opened: `IsPage` drops
  framework prefetches (`_rsc=`, `__nextDataReq`), build-output prefixes (`/_next/`, `/_nuxt/`,
  `/assets/`, …), asset extensions, `HEAD` and `OPTIONS`; a refused scanner probe is not a visit
  either. The "Pages only" chip filters on it; the readings tile says "N page views" beside the rate.
- **Probes and scanners** (`IsProbe`, `Summary.Probes`, `Summary.Scanners`): a refused request for
  one of the paths every scanner tries — `/.env`, `/wp-login.php`, `/xmlrpc.php`, `/.git`,
  `/phpmyadmin`, … — or for a `.php`/`.asp`/`.sql` file the site does not serve. A client with three
  or more is named a scanner. Every client facet carries `Refused` and `Probes`, so a visitor who
  followed a dead link and a script trying doors are told apart on the row.
- **Referers** are grouped by host, the site's own host excluded: a link followed within the site is
  navigation, not a source.
- **Per-path p95**: a bounded sample per path (128 samples, 2,000 paths) so the top-paths table says
  which route the slow tenth belongs to.

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
  p95 and the bytes its answers sent (`bytes`, which is what lets the Served reading carry its hour as
  a line). A long window widens the column rather than adding columns, and `Summary.bucketSeconds` is
  that width: it used to say 60 whatever the window, so a day's chart captioned each of its 24-minute
  points as one minute. A window a whole number of columns long touches one column more than the
  chart has — "the last hour" asked at 12:00:40 runs from 11:00:40, so it spans 11:00 through 12:00 —
  and the column that goes is the oldest, which the window only clips: dropping the newest, as the
  histogram first did, drew a 5xx the readings had already counted nowhere on the chart or on the
  minute strip. A window asked for whole, both ends on the minute, keeps its own start.
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
| `GET /deploy/{id}/lifecycle` | Docker events for this environment's containers and networks, filtered by `kinds`, `search`, `since` and `limit`, each correlated against the audit log |
| `GET /deploy/{id}/lifecycle/stream` | WebSocket: this environment's buffered past, then every arriving event that carries its label |
| `GET /deploy/{id}/requests/export?…` | The window as CSV, every matching request oldest first (up to 50k) — the page's own query, not the whole record |
| `GET /deploy/{id}/runs/{run}/traffic` | Requests/min, 5xx share, p95 and page views over the half hour before and after the run's activation; the after-window is cut at now |
| `GET /deploy/traffic` | Every project's last hour — rate, 5xx share, page views and one point per minute — read from the held record when it is under a minute old, so a fleet of forty cards is one answer rather than forty file reads |
| `GET/POST /deploy/{id}/alerts`, `PUT/DELETE …/{alert}`, `POST …/{alert}/test` | Traffic alert rules (below); writes need `system.admin` |

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

## Traffic alerts

A rule watches one environment's record: `error_rate` (share of requests answered 5xx, as a
percentage), `latency` (the window's p95, milliseconds) or `silence` (a record that has held traffic
holds none for the window — at least five minutes, or a quiet minute is an outage). Rows live in
`deploy_traffic_alerts` (additive, `CREATE TABLE IF NOT EXISTS`); `AutomationStore` owns them
(`ListTrafficAlerts`, `CreateTrafficAlert`, `UpdateTrafficAlert`, `DeleteTrafficAlert`,
`EnabledTrafficAlerts`), and a rule naming a channel that does not exist is refused at the form.

`TrafficAlertEvaluator` takes every enabled rule's reading once a minute from the held record
(`Window` with `Since: now − window`), decides `ok` or `firing`, and announces only the transition:
`traffic.firing` on the way over, `traffic.recovered` on the way back — a rule that stays crossed for
an hour is one message, not sixty. A route with no record is new, not silent, and is skipped; a
latency rule over nginx's duration-less record has nothing to say and is skipped; an unreadable record
changes nothing. The state, the reading and when it entered the state are written back so the page
can say "firing since 03:12, 4.2%" without waiting a minute.

Delivery is the notification channels' own: the two events are rendered by `renderTrafficAlert` into
the same provider payloads a run's outcome takes (Discord, Slack, Telegram, e-mail; a webhook gets the
envelope with an `alert` block verbatim). The rule's channels, or every enabled channel when it names
none. "Send test" delivers the rule as if it had just fired, at twice its limit, with `[test]` in the
title so nobody wakes up for it.

The Logs page carries one line under its readings — no alerts, all quiet with the rules as sentences,
or what is firing and since when — naming the channels each rule tells by their logos, and saying
so in amber when a rule tells no channel at all. With no rule yet, the first is written in a sheet
on the page itself, opening on the reading as it stands so the line is chosen against it, and the
form in it is Automation's own (`AlertForm`, drawn in `add-alert-sheet.tsx`), so one rule has one
form wherever it opens. Automation lists the rules as sentences with their state and a meter with a
tick at the line; its form speaks the rule back before it is saved, and removing one asks first.

## Security intel and blocking

The Insights view's Clients list names scanners and, for `system.admin`, offers **Block** on each
address — `POST /firewall/rules` with a source-only deny, exactly what the Security pages' address
verbs do, so a scanner seen here is stopped at the firewall without changing pages. Loopback and
private addresses never get the verb: a deny rule against something already inside the network the
firewall stands at the edge of does nothing.

"Private" is read from the actual reservations rather than from a prefix. The first version tested
`172.` and so hid the verb for the whole of `172.32`–`172.255`, which is public space — 172.217 is
Google — while RFC 1918 reserves only `172.16`–`172.31`. `isPrivate` in `request-marks.tsx` (the
Insights list and the opened request both ask it) is `networkOf` in `lib/clients.ts` asked one
question — is this the internet — so it covers `0/8`, `10/8`, `127/8`, `172.16/12`, `192.168/16`,
`169.254/16`, Tailscale's `100.64/10` and `fd7a:115c:a1e0::/48`, `::1`, `fc00::/7` and `fe80::/10`,
and unwraps an IPv4-mapped address first. It was a second classifier once, and the two disagreed: a
row drawn with Tailscale's mark offered a firewall deny against a tailnet peer.

## Lifecycle

`dockerx.Event` gained `Owner`, the dashboard's own labels off the actor's attributes with the
`io.just-dashboard.` prefix stripped. Docker puts every label there; keeping this one namespace is
what lets a project page show its own restarts and OOM kills without inspecting a container that is
by then already gone. Only the dashboard's prefix is kept — an arbitrary image's label set is
unbounded and none of it is ours to render.

The buffer is host-wide and bounded (2000), so `handleDeploymentLifecycle` asks for a wide slice and
narrows by `Owner["environment-id"]` in `ownedByEnvironment`: asking for only `limit` events would
return a hundred belonging to other containers and none belonging to this one. The `since` bound is
served here rather than computed in the browser — a reading like "restarts in the last hour" worked
out during render reads the clock on every re-render, so the figure would depend on when React
happened to paint. `kinds` defaults to **containers and networks** rather than containers alone: a
network disappearing under a running release is exactly what this feed is for.

A network is not recognised the way a container is. Docker puts an object's labels in the event's
actor attributes for a container, which is what the owner filter reads; for a network it sends `name`
and `type` and nothing else, whatever the network was created with — so the label test silently drops
every network event and adding the kind would achieve nothing. `ownsEvent` falls back to the name for
that one kind, which this dashboard chose and which carries the environment in it: `jd-e7-db-…` for a
database network (`deployment_database_network.go`), `jd-preview-e7` and `jd-preview-e7-…` for a
preview's. The trailing separator is what stops environment 7 claiming environment 70's. This was
found by the live test rather than by reading the API reference, which is the argument for having
one.

`handleDeploymentLifecycleStream` follows the same slice live. Without it the feed learned about a
restart up to ten seconds after the chart beside it had drawn the 502s, which is not one timeline.
The socket sends the buffered past on connect and then every arriving event carrying this
environment's label, and correlates none of them: a single event would cost its own audit query, and
the poll beside it re-reads the same event with its trigger a moment later — which is why the client
dedupes with the polled copy first, so a row stops saying "docker itself" once the poll knows better
rather than flickering back to it.

### Who did it

`correlateEvents` on the host feed matches an audit entry naming the same container, image or compose
stack within a minute either side. Run against a deployment it finds nothing, because a release is
audited against the **project** (`deploy.run` with target `lampino`) and never against the container
it goes on to create — so `handleDeploymentLifecycle` did not call it at all, and every row said
"docker itself" or nothing however the container had been started. The tag the frontend drew for a
correlated event was unreachable code.

`correlateDeploymentEvents` runs both passes over one batch. The container-level match goes first and
is kept, because an entry naming this exact container is a better answer than one naming the project
it belongs to. The deployment pass then matches the project's name across a window that is
**directional**: the entry must precede the event by no more than `deployCorrelationWindow` (15
minutes, because a release's containers appear after the build rather than with the button) and never
follow it — a symmetric window would let a deploy at 10:05 explain a container that died at 10:01,
which reads as "the deploy broke it" when the truth is the reverse.

Three actions are never a release's doing however well they line up. `die`, `oom` and
`health_status:*` are the daemon and the kernel reporting something that happened *to* a container,
and filing those under "this dashboard" sends an operator to the audit log for an answer that is not
there. `die` is on the list even though a release genuinely causes one when it recreates a container:
the host feed is right to attribute that, because it has an audit entry naming the container seconds
away, while this pass has a project's name and a fifteen-minute window — which a container that
crashed of its own accord a minute after an unrelated deploy fits perfectly well. An exit is what the
reader came to the page to explain, so a confident wrong answer about it is the expensive one.

`correlateEventsWith` is the shared walk both passes use, and it skips an event that already carries a
trigger, which is what makes "first match wins" true rather than "last one seen wins".

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

`ProjectLogs` is a `StatGrid` of five readings (requests/min with page views, failing share, p95,
bytes served, container events — the figures count up on arrival through `NumberTicker`) and the
alerts line over a `Pane` with the three views. Each reading carries its last hour in the tile, as
the host Overview's do: the request rate, the p95 and the bytes as lines from the buckets, the
browsers, programs and crawlers that asked as their logos beside the tile's name, the failing share
as a strip of the hour's sixty minutes (green where a minute went fine, red where it had a 5xx, grey
where nothing came in), and the container as its newest disruption with a rail of a tick per exit,
restart and start (`traffic-strip.tsx`, the Backups run strip's shape; two strips on one scale, so a
red minute and an exit at the same place are one incident read twice), two to a row on a phone. An
exit with status 0 is not a disruption: it is a routine stop or the old container of a release
swap, which the server already logs as a notice. The rail draws it as a muted tick, never the
failure's red; the reading counts it as a stop, reading "Stopped" in no tone when nothing started
after it and "Steady" after a swap; and the Events tab does not count it. A kill is not counted
either, because Docker reports the exit that follows it and one stop would read as two. The chart
carries marks through the house chart's `events`: a release going live (`releases[].activatedAt`,
the Metrics page's deploy colour), a container exit that was not clean or an OOM kill (danger), a
restart or a start (warning) — a clean exit is the other half of a release going live or of somebody
stopping it, and is not marked. A spike of red with a deploy mark at its foot is a different
afternoon from the same spike with none.
A failing request's detail opens onto the container's output around that minute (host Logs page) and
onto Events scoped to two minutes either side. The view and that instant are written back to the URL
(`?view=events&moment=…`, through `history.replaceState` as the host Logs page does): they were read
on arrival and never written, so pressing the tab changed nothing in the address bar and a reload
landed back on Requests with the scoping gone — and a link to the minute a deployment broke is
exactly the thing somebody pastes into a chat.

`LifecycleFeed` carries the toolbar the other two views have — a search box, kind chips with counts,
and a Live chip over the socket — because it was the only one of the three that could be neither
searched nor followed. Filtering is applied in the browser rather than on the wire, for the reason
the host feed gives: re-subscribing on every keystroke would drop the connection four times a word.
Its empty state names the boundary instead of asserting steadiness. "Watching since 07:28" is the
backend's own start, so a few minutes after a restart the old copy — "which for a running deployment
is the reading you want" — was a claim the page could not make; under an hour it now says the record
begins with the dashboard and nothing from before was kept. A row's release links to the run that put
it there and a correlated trigger links to `/audit?action=…`, the same hand-off the host feed makes.
Its rows are grouped under hour rules, each event drawn on its project's or image's tile with what
happened as a toned badge in the corner, and the rows a poll or the socket brings rise as they
arrive. The Overview carries two of this page's readings among its own four (requests a minute with
the hour's line, and the failing share), and each fleet card a sparkline with the rate, both from
`/deploy/traffic`; the run page's Metrics view leads with `RunTrafficPanel` — requests/min, failing
share and p95 before → after activation. The readings are
the page's own, over a fixed last hour, so they hold still while the reader narrows the rows beneath
them — which is what lets the error rate be the thing that sent them to Output in the first place.
Each is a rate or a share rather than a count: a figure whose meaning depends on a control somewhere
else is a figure people learn to ignore.

`lib/requests.ts` holds the vocabulary. A request log is read by status **family**, not by code —
nobody scans for 418, they scan for "is anything 5xx" — and the four families are the chips, the
chart's stack and the colour of the code in the row. It is the one status map (`CLASS_TEXT`,
`CLASS_DOT`), and the host log console reads its in-line statuses from it too, so a code is one
colour on both pages: 2xx success, 3xx the path hue, 4xx amber, 5xx red. A method is a word, not a
colour: a read stays muted and a write steps forward in the log's method hue, a `DELETE` included —
it is a change, not a danger. The rows, the opened request, the Insights lists and the scanners
notice take their parts from `request-marks.tsx`, over the log console's token classes, so a path,
a query and an address read the same wherever they are drawn; its Colour switch is the log
console's, and turned off it keeps only what is a reading of state — a 5xx, a 4xx and an answer over
a second.

The socket opens with the window's cursor (`after=`), and rows are keyed by sequence, so a live
prepend neither remounts the list nor shows a request twice.

`TrafficFacets` (Insights) draws each reading as a `BarList` — Tremor's BarList pattern rewritten
onto the tokens as a house primitive in `components/bar-list.tsx`: the meter's own track behind a
name, the figure at the right in `.numeric`, and a signal segment inside the bar for the failing or
refused share, so "the busiest" and "the failing" are one row read two ways. A path row narrows the
rows to it; a client row narrows to the client and, for an admin, blocks it. Insights opens on a
response-time ladder (`latency-ladder.tsx`): the window's distribution from the median to the
slowest on one logarithmic rule, where the gap between the median and the tail is the picture — a
line of footer text had stated the same numbers and shown none of it — with the span past a second,
or past the line a latency alert watches, washed amber, and each mark narrowing the rows to the
requests at least that slow. Every narrowing the page makes is one of the API's own filters —
`host`, exact `status` codes, `minMs` — and the poll, the socket and the CSV export are asked the
same query, so what is exported is what is on screen.

`RequestConsole` draws rows in the log console's own anatomy rather than a `<table>`: a request record
is read the way a log is read, and a nine-column table at this density spends its width on cell
padding and its maintenance on breakpoint rules. Following holds the *top* — newest first — and
pausing holds what arrives rather than dropping it, both carried over from the log console next door.
`content-visibility` rather than a virtualiser, for the same three reasons: an honest scrollbar, real
row heights, and the browser's own find. A request that arrives live rises into place
(`hooks/use-arrivals.ts`). A row opened in place leads with who asked — the client drawn as itself,
its network and address, a scanner tag when it is one — then typed facts (the code and its word, the
time and where it sits in the window's distribution, TLS or plaintext, the referrer as its site), and
its menu carries filter by client, copy as JSON, copy as curl and — for an administrator and an
address that is not private — Block.

`RequestChart` goes through `components/metrics/` like every chart (§10): a `ChartPanel` of request
volume as the chart's ramped area with 4xx and 5xx as lines that sit on the floor until they don't,
and a second, shorter `ChartPanel` of the p95 on the same time axis — a wall of red with a flat p95 is
a deployment refusing requests, the same red with the p95 climbing is one falling over. Numeric time
axis, synced crosshair readout, drag-to-narrow and `events` for the marks come with the house chart;
the legend is a one-line one, because here the chart is a row of a workspace, not the page. Its
header reads the window's own count and failures, and after a drag the span with a way back to the
last hour; the p95 chart draws each enabled latency alert as a line at its threshold. A first
version hand-drew stacked columns to mirror the log histogram and stacked the families solid; on a
working deployment the ok share is ninety-five per cent of every column, and a pale slab that size
swallowed the red sliver that is the only thing anyone looks for.

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
  trail. What the dashboard itself did is in `audit_log`. A restart of the backend — a self-update
  included — empties it, and the Events view, the Container reading and the chart's failure marks all
  forget together. The empty state and the feed's own footer say so rather than leaving a reader to
  infer steadiness from a record that starts three minutes ago.
- Access logs hold client addresses. They are removed with the route, and they sit behind the same
  authentication as every other host log this dashboard serves.

## Verification

```bash
cd backend && go build ./... && go vet ./... && go test ./...
cd ../frontend && bun run lint && bun run build && bun run test:browser
```

`internal/accesslog` covers both formats, the junk that is not either, the filter, nearest-rank
percentiles, facet error counts, histogram coverage of the requested window, each column's bytes
and its real width, the minute happening now kept on the chart, that the row limit does
not cap the readings, and the store against a fake disk: that only appended bytes are read, that a
half-written line waits for its newline, a roll with and without the old generation surviving,
truncation in place, the seed budget and its ordering of generations, eviction past the cap without
renumbering, `After` from a cursor, `FromNow` and a stale cursor, out-of-order instants inside a
window, the idle sweep, an absent record that later appears, stale-on-failure, and a removed route
emptying. `internal/proxysvc` covers the rendered log block, its rotation and `roll_uncompressed`, the
in-place upgrade of a pre-recording route (byte-identical to activation, credentials and TLS kept,
idempotent, foreign content refused),
the framing of the container script's two reports, the host-file reader across appends, a rename and
a truncation, and — live — the script against `caddy:2-alpine`. `internal/logsx` covers ANSI, carriage-return frames, structured levels on
both numeric scales, and that a brace alone is not JSON. `internal/proxysvc` covers the rendered log
block, its rotation, and that the reader and `sites_render` agree about nginx's path.
`internal/deploy` covers the observer against a fake ingress. `internal/api` covers the lifecycle feed
itself: that it shows one environment's events and not the host's, that the window and the limit are
the caller's, that an environment with no events reads as an empty list rather than a null, that the
default kinds are containers and networks and a junk `since` is ignored, and — for the correlation —
that a release names itself as the cause of its own containers, that it never explains what happened
before it or outside its window, that an OOM kill and a health flip are never the dashboard's doing,
that another project's release and a refused action explain nothing, and that an entry naming the
container itself wins over the one naming its project. `TestLiveDeploymentEventsAreReadFromDockerAndNamedWithTheirCause`
drives the whole path against a real daemon — a labelled container exiting 137 and a named network
destroyed, read back through the real routes and the real socket — because the one thing the unit
tests cannot hold is whether Docker still carries an object's labels on its events.
`tests/browser/deploy-requests.spec.ts`
covers the three views, the readings, chip narrowing, the opened row, the absent-record sentence,
the Events toolbar (kind chips, search, the no-match sentence, the socket), the audit and run links
on a correlated row, the restart-empty sentence, `?view=`/`?moment=` surviving a reload, which
addresses are offered a Block, and that the page fits at 390 and 1280 — and that a code, a latency
and a client on Insights each narrow the window through the API's own filters, that a chosen method
keeps its chip as the way back, that a row opens from the keyboard, that a clean exit reads as a
stop rather than a failure, and that a role which cannot add an alert rule is pointed at Automation
instead of a form. `tests/browser/deploy-alerts.spec.ts` covers the first rule written from the
sheet on the Logs page.
