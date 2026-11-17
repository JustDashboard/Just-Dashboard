# Logs and records read by their shapes

A log line and a step's record are coloured by the shapes in them.

## The rule

- **A log line is cut into its shapes** (a time, a host, a program, an address, a status code, a
  path, a quoted string) by `lib/log/tokens.ts`, and each shape takes its hue from the `--tag-*`
  rung. `LogText` draws it. No shape takes a status hue unless it is a status: a string is not a
  success.
- **A level is a wash on the line**, not a coloured tag in front of it: a failure is found by
  where it is, and says so in words.
- **Text that was written is kept as written**, in a `CodeBlock` with its name, its length and a
  copy; a command is `ShellWords`, a record `JsonCode`, a Dockerfile its pieces in the same palette
  (`components/code.tsx`).
- **A transcript is numbered lines** (`TranscriptRow`), a console is a `ConsoleView`, and a diff is
  a `DiffView`. One renderer each, so the same line reads the same on every page.
- **A record is drawn as what its values are**: a digest cut to twelve characters with a copy, a
  commit as the Git page draws one, an image beside its product's mark.
- **A log pane's filters narrow in place** and are kept in the address.

What follows is the text that was in `design-system.md` §14: why, and the cases. The dated stories
it tells are also why the pages in [`../decisions/`](../decisions/README.md) look as they do.

## 14. A header is its title: logs and records

**A log line is read by its shapes, and coloured by the same rules as the rest of the product.**
The logs console used to draw every line as one grey-white string with a 10px level tag in front of it,
so a failed password, the address it came from and a 502 were found only by reading every line.
`lib/log/tokens.ts` cuts a line into its shapes — syslog's `time host proc[pid]:`, the common log
format, logfmt pairs, a JSON object, bracketed and shouted levels, addresses, request lines, statuses,
paths, ids — as spans over the original text, so the search's match ranges still land; and
`components/logs/log-text.tsx` colours them from one map. The **status hues** go only to what is a
reading of state: a level, an HTTP status by its class (2xx success, 4xx warning, 5xx destructive), a
word that says something failed or succeeded. A word is read whole, digits and all — `fail2ban` is a
name, not "fail" — and a firewall's `BLOCK`, `DROP` or `REJECT` is the firewall working, on every line
of its log, so it is not a failure word. Every other kind takes a `--tag-*` hue, which sit at one
lightness so no kind outshouts another, and what the eye should skip goes muted — the line's own
timestamp (not drawn at all while the time column shows it), this host's name on a syslog line (not
drawn either), the pid, the punctuation, the keys. The message stays in the foreground. A program's
name takes a hue by name from `LANES` (`lib/hue.ts`: the tag hues without red and amber, which on this
console would read as a process that failed), so one process can be followed down a busy page — the
same argument as `AuthorMark`, whose hash now lives there too. An error or a critical row is washed the
way the build console washes a failing step, a warning row in amber; the level column is the level's
word at the line's size. Where a lens named what the line records, **its event word takes that
column** — "auth failed", "deadlock", "upstream refused" say more than "err" — at the line's size in
the event's tone (`EVENT_WORD`: `font-medium text-destructive` for a failure, `text-warning`,
`text-success`, muted otherwise), and the level stays as the edge and the wash; the column is as wide as
the longest word on screen, within bounds, rather than a fixed width that cut "restart scheduled" short.
An event a busy log says constantly and neutrally — a request, a connection, a cron session — keeps the
level's word (`mark: false`), or the column would be a wall of words saying nothing. A value the lens
read out of the text is drawn as what it is (`components/logs/field-value.tsx`: an address as its
network, a status in its family's colour, a method as its word, a duration in its latency tone, a unit,
a jail, a package or an image beside its product), the same drawing a request's client gets, so a
Postgres client and a visitor are one mark; at most three such columns stand before the message, only in
colour and only while a line on screen fills one, a column drawn as its text as wide as its longest
value on screen (an upstream cut to "http://1…" identified nothing). A record's continuation lines stay
under their head, the first three shown and the rest one fold away; a run's start (a unit started, an
application came up) is a hairline rule across the pane with its word and the unit's lane hue, so a
service's runs read as runs — in one service's stream only, and never for a stop or a failure, whose
word and wash already mark them: a whole host's journal ruled off every timer that fired; and in the
live tail a run of identical lines is one row with a muted `×N`. `stderr` is a muted word, never a danger tag — Postgres writes every line there, and a red tag on
each said a failure that was not there. None of these is a pill or a badge: a `Tag` carries the word
only with Colour off. A structured line is drawn as its message and fields in the logfmt shape the
tokenizer reads, most telling field first. The "Colour" toggle beside Wrap and Time turns all of it
off and shows each line exactly as it was written; in a pane too narrow for a switch each (a phone, a
sheet), Wrap, Time, Colour and Repeats are one **View** menu, so the level chips are not squeezed to one.

A deployment's request log is a log, and is drawn by the same rules: the request console, a request
opened in place, the Insights lists and the scanners notice all take their parts from
`deploy/request-marks.tsx`, which is `log-text.tsx`'s token classes over `lib/requests.ts`'s one
status map. A status is coloured by its family — 2xx success, 3xx the path hue, 4xx amber, 5xx red —
a write takes the method hue while a read stays muted (a `DELETE` is a change, not a danger), the
path and query are tokens, and an address takes the address hue beside the client drawn as itself.
Its Colour switch is the console's own; turned off it keeps a failure, a refusal and an answer
slower than a second, because those are readings of state (§3) rather than decoration.

**A step's record is read by its shapes, and code is coloured by the same rules.** The run page's
Details drew a step's evidence as a well of grey JSON under an accordion row: the commit a build
checked out, the Dockerfile it wrote and the image it made were all found by reading braces.
`deploy/run-evidence.tsx` reads the record by shape instead — a value by its key, a list by what its
items carry — and draws each the way the product draws it elsewhere: a toolchain, a Node version, a
platform or an image beside its product's mark, a digest cut to twelve characters with a copy, a
commit as the Git page draws one, a list of images as rows of their products with size and platform,
a list of health checks as rows of their outcomes. What is text — a Dockerfile, a command line, a
record nested past two levels — is a `CodeBlock` (`components/code.tsx`): the `Well`'s ground with a strip naming it, its
length and a copy, and its tokens in the `--tag-*` hues the log console's tokens sit on (keys blue,
strings green, numbers pink, literals and a Dockerfile's instructions violet, paths and flags cyan,
digests slate, punctuation and comments stepped back). None of the status hues: a string is not a
success. A step is drawn on the tile of the product it works with (`StepTile`, its state in the
tile's corner the way `ProjectMark` carries a framework) — the forge for the source, the toolchain
for the build context, Docker for the build and the runtime, Let's Encrypt for a certificate, the
authority certbot asks — and a step that is the dashboard's own bookkeeping keeps a glyph.

`/deploy/new` reads its plan the same way since 2026-10-05, because a plan is the run before it
happens. The drawing beside Configure's fields — four nodes on beams — is a rail
(`new-project/plan-rail.tsx`, over the pure `plan-reading.ts`): each of the four steps under its
segment of the spine, each part of the plan the step decides on the tile of the product it is (the
forge, the framework, the runtime, Let's Encrypt for a name served over HTTPS, a glyph for a health
check, the limits, the storage, the environment and the server's check), what it currently says
under its name, a build or start command, a check's request, the paths kept between releases or the
variables still owed as code in the same hues (`ShellWords` in `components/code.tsx`, the inline form of the run page's
`CommandBlock`), and a part that wants a look before Deploy marked in its tile's corner. Every row
opens the fields that decide it. Review opens on what the server said about the plan the way the
run page opens on how a run ended, and draws what the release does to this server — a mount, a
generated secret, a value the plan sets, a readiness request, the cutover — as rows of what each
is. Detection's evidence is its files drawn as files (`FileIcon`), and an image tag is its
registry, its repository and its tag.

### The log panes' filters

**The log panes' filters took one shape on 2026-10-11**, at the operator's request, everywhere a log
is read — `/logs`, every service page's `ServiceLogs` and both request workspaces — because the bar
over the lines was four kinds of control in a row: a brand Search standing alone, a filled select
reading "Custom range" with its two date fields a row lower, a ghost More, bare chips, and two glyphs at
the end of the field that read as the `/` shortcut and a letter "B". Now:

- **One face for the bar.** The search field carries its `.*` and `Aa` switches and, where the words
  wait for Enter, Search — the one command, on the brand face — inside its own edge (`InputGroup`); the
  unit, the window, Options, and on a request record Took, Live and Export, are outline controls at the
  field's height, a pressed one on the accent ground.
- **The window is one control** (`logs/time-window.tsx`): its trigger always names what is on screen —
  *Live* with the socket's dot, a preset, or the span — and its popover holds the tail, six presets one
  press each, and a span typed as two times with one Apply. A preset picked on the stream opens History
  over it.
- **Every narrowing is in one row**, and stays in sight: Options' settings as chips that clear them, the
  level chips (a dot in each level's swatch, an error count red and a warning amber), Fields, the quick
  views and the readings, with **Clear filters** at the row's end. The levels had sat on the strip above
  the lines, the chart between the two rows of narrowings.
- **The strip over the lines says what is being read**: *Live* with the last minute's pace — thirty
  two-second columns by the lines' own times, errors red at their tops, sliding while the socket is
  open, so a quiet tail reads as quiet and alive rather than frozen (§11 *live*, nothing new) — or
  History's matches. How the lines are drawn is one segmented group (Wrap, Time, Colour, Repeats), each
  segment filled while on; Pause, Copy and Clear are glyphs.
- **Colour that names a level**: info is the chart blue rather than ink, so a healthy day's histogram
  is a blue field with amber and red where they happened, not a grey wall. The histogram is taller,
  reads a column out by series under the pointer, and takes a drag as well as a press.
- **Insights says the window in words first.** A log's opens on its lines, errors, warnings (each
  narrowing to itself), busiest minute and its distinct addresses, every figure with its picture on
  one line across the row. Since 2026-11-09 it is drawn as the request record's is: `RankList` and
  `SplitBar` rather than a `BarList` per key, group tables of one-line rows, the chart under a block's
  head, and level chips for the levels present only. A request record's opens on who came: **Visitors** (distinct people —
  an address and a browser that opened a page; never a crawler, a script or a probe), page views, bots
  and scripts, the site that sent them as itself and the devices as one bar in their colours, then
  the people as a list — the browser on its system, the device, the address, their trail of pages —
  above the address list (renamed **Addresses**) that keeps Block. `BarList` takes a `color` for a run
  of kinds (devices in the `--tag-*` hues, browsers and systems by `hueFor(name, LANES)`), which then
  reads as a legend instead of one grey column. The p95 chart and tile are **Response time**, captioned
  in words.
