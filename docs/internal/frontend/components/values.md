# Values

How a value is said and drawn: counts, ages, days, digests, certificates, in and out, copy, files
to save, and route segments. One helper per value, so the same figure reads the same on every page.

**Members:** `lib/format.ts` (`grouped`, `compactCount`, `plural`, `percent`, `sharePercent`,
`bytes`, `rate`, `duration`, `ago`, `relativeTime`, `dayHeading`, `timestamp`, `calendarDate`,
`clock`, `localInputValue`, `shortDigest`, `shortSha`, `truncateMiddle`, `sentenceCase`,
`asSentence`, `dateFormatter`), `Ago` (`components/ago.tsx`), `lib/certificates.ts`
(`certificateWords`, `certificateTone`, `timeLeftWords`, `certState`), `InOut`, `RX`, `TX`,
`formatRate`, `axisRate`, `inOutSeries` (`components/in-out.tsx`), `CopyButton`, `CopyAction`,
`CopyValue` (`components/copy.tsx`), `useCopy`, `copyText`, `saveText`, `saveBlob`
(`lib/download.ts`), `decodeSegment` (`lib/route-params.ts`), `useNow` (`hooks/use-now.ts`, the one
clock: `useNow(intervalMs = 1000, enabled = true)`), `useElapsed`, `useDebounced`, `ProductLogo`,
`ProductGlyph`, `ProductLogos`, `Logo`, `LogoGlyph`, `icons.tsx`.

Products, logos and icons are drawn as themselves; their rules are in
[`../patterns/drawn-as-itself.md`](../patterns/drawn-as-itself.md) and the brand paragraphs of
[`README.md`](README.md).

## Purpose

A figure the product says in many places is said by one function. Before 0.9.0 a request count
read "1.9k" on the proxy's Overview and "1.9K" on Sites, a certificate with ten hours left read "0d
left" on one page and "10 hours left" on another, and "how long ago" was phrased eight ways in one
area. The rule now: if a value has a helper here, a page calls it; if a page needs a value that two
areas say, the helper goes here, not in the area.

## Figures

**One locale for every figure: en-US.** `grouped(1204)` is "1,204" in every browser. A dashboard's
figures are read beside a terminal's, and a German browser used to read "1.204" keys beside "1,204"
collections. `plural` groups the same way. Dates and times keep the reader's locale and zone
(`dateFormatter`); only figures are pinned.

| Helper | Says | Use for |
| --- | --- | --- |
| `grouped(n)` | "1,204" | a count where every digit fits |
| `compactCount(n)` | "1.2k", "60k", "16.1M" | a count in a chip, a tile or a row's trailing figure |
| `compactCount(n, { exactBelow: 10_000 })` | "9,999", then "28.6k" | a tile that has room for four digits |
| `plural(n, one, many?)` | "1 row", "1,204 rows", "2 indexes" | a count and its noun |
| `percent(v, precision = 1)` | "42.5%" | a percentage the API sends as 0–100 |
| `sharePercent(share)` | "<1%", "42%", ">99%" | a share of 0–1 in whole percent, as a ranked list's column |
| `bytes(v)`, `rate(v)` | "1.2 MB", "1.2 MB/s" | sizes and throughput |

`compactCount` writes thousands as a lower-case **k** (the SI prefix) and the larger units as
capitals (**M**, **B**, **T**), since "m" is a minute. One decimal while the figure is under a
hundred of its unit, none past it ("123k"), and a figure that rounds to a thousand of its unit is
the next unit ("1M", not "1000k"). `exactBelow` never goes under 1,000: below it a compact figure
would read "0.5k".

**Not here, on purpose.** `flowBytes` (`lib/network/flows.ts`) keeps a conntrack counter exact as a
bigint and in binary units. The database's `millis` and `micros` encode each page's own rounding
choices. A latency is said in words (`lib/latency-words.ts`), not as a figure.

## Ages and days

`ago(at, now, { units })` is how long ago an instant was, both as numbers: "just now" for the first
five seconds, then the largest unit ("14s ago", "3m ago", "2d ago"). Two units ("2m 30s ago") only
where the seconds matter, because the second unit ticks while it is read. An instant a moment ahead
of `now` (two clocks) is "just now", never "-2s".

`relativeTime(iso)` is the ISO version for a moment either side of now ("from now" too), and stays
for the call sites that hold a string.

`<Ago at verb units live tick className />` is `ago` kept true while the page is open. It owns the
ticking: every second while the age is in seconds (or for the first hour with two units), every half
minute, then every minute. `live={false}` stops it, for a closed panel. The exact time is the
`title`, and it renders a `<time dateTime>`. `verb` is written as given, so it follows the sentence:
"Checked 3m ago" at the start of a line, "checked 3m ago" inside one.

```tsx
<Ago at={scan.checkedAt} verb="checked" />
<Ago at={result.checkedAt} verb="tested" live={open} className="text-hint text-muted-foreground" />
```

`dayHeading(iso, now)` is the heading over a day's group in a feed: "Today", "Yesterday", then
"Oct 28" within this year and "Oct 28, 2025" before it. One ladder, so a project's runs and its
webhook deliveries name the same day the same way.

`localInputValue(date)` is a date as a `datetime-local` input holds it: wall-clock time, no zone.

`useElapsed(since, tick = 250)` is the milliseconds since `since` while something runs, 0 when
`since` is null (and the clock stops). A boolean caller records the time it turned true and passes
that.

## Digests

`shortDigest(value)` is the one way a digest is shortened: twelve hex characters, no "sha256:" in
front and no ellipsis, as `docker images` prints an image id. A value that is not a digest (a tag, a
release name) is cut to the same width. `shortSha(sha, length = 7)` is for commits.

## Code

`components/code.tsx` is how code is drawn. `CodeBlock({ title, mark?, copy, lines?, children })` is
the recessed well with its caption, line count and copy. `CodeLines({ lines, render?, wrap? })`
numbers lines in the faint ink with a gutter as wide as the last number (`gutterCh`). `CODE` is the
one palette: the `--tag-*` rung the log console's tokens sit on and none of the status hues, since
a string is not a success. `DOCKERFILE_HUE` maps a Dockerfile's pieces onto it
(`dockerfileLines(source)` for a whole file, `tokenDockerfile(line)` for one line,
`DockerfilePieces` to draw them). `Digest({ value, label })` is a digest as twelve hex characters
with its copy. `ShellWords` and `JsonCode` colour a command and a record. `deploy/run-evidence`
re-exports `CODE`, `CodeBlock`, `JsonCode` and `ShellWords` as deprecated names for its older
callers.

A diff is `DiffView` (`components/diff-view.tsx`), text a person wrote on a forge is `Markdown`
(`components/markdown.tsx`), a file or folder is `FileIcon` (`components/file-icon.tsx`), and a
path or address that must wrap is `MonoValue` (`components/wire/mono-value.tsx`), which breaks at
its separators and never inside a word.

## Certificates

Every certificate word and tone is built on `certState` in `lib/certificates.ts`, which is the one
order of a certificate's states, worst first: unreadable, test, expired, expiring, valid.

`certificateWords(cert, { form, now })`:

| State | Short (a status word) | Long (a sentence) |
| --- | --- | --- |
| unreadable | Unreadable | Unreadable |
| test | Test certificate | Test certificate |
| expired | Expired | Expired |
| 29 days | 29d left | 29 days left |
| last day and a half | 10h left | 10 hours left |
| under an hour | <1h left | Under an hour left |

A test certificate says so before anything about its days: browsers refuse it whatever they are.
The last day and a half is counted in hours, which "1d" and "0d" hid. A certificate known only by
its days (a project's domain) says "1 day" and "under a day" there instead. `timeLeftWords` is the
same without "left", for a figure ("29d", "10h").

`certificateTone(cert)` is the `Verdict`: critical once refused (unreadable, test, expired), warning
inside the renewal window, ok otherwise.

The components stay adapters, because they take different inputs and draw different things:
`ExpiryStatus` (`proxy/expiry-status.tsx`) is the short word on a `Status`; deploy's
`CertificateReading` (`deploy/vocabulary.tsx`) keeps the issuer's logo and the domain-level state
word ("Certificate valid") and adds the long form beside it, through `domainCertificate`.

## In and out

`InOut({ rx, tx, unit, layout, trend, plain })` draws received and sent as one reading,
"↓ 1.2 MB/s ↑ 80 KB/s": down first, a space between, each in its direction's colour, `RX`
(`--chart-5`) and `TX` (`--chart-2`). Never re-spell the colour as `text-[var(--chart-5)]`.

- `unit`: `rate` (`rate()`), `bytes` (`bytes()`) or `flow` (a conntrack counter, `flowBytes`).
- `layout`: `inline` for a row's trailing figure; `stacked` for a device list's column, with the
  last two minutes beside it when `trend` holds them (from `sm`).
- `plain` drops the colour, for a pair inside a sentence whose other figures are not coloured.

A chart of in and out takes `inOutSeries()` for its two areas ("In", "Out"; a tunnel's peer passes
`{ rx: "From it", tx: "To it" }`), `formatRate` for its pointer and `axisRate` for its axis.

`RatePair` (`network/rate-pair.tsx`) is `InOut` stacked and is deprecated, as are its `RX` and `TX`
re-exports.

## Copy

Two kinds of feedback, because they answer different questions. A tick where the control stands
says *that* it worked without moving the reader's eye: a value about to be pasted into a terminal,
or a set of recovery codes a toast would sit on. That is `useCopy` and the three components below.
A toast says *what* was copied, after a bulk copy ("340 rows as SQL"): that is `copyText`
(`lib/clipboard.ts`), called directly.

- `CopyButton({ text, label, size })`: an outline button, the word Copy turning into Copied with a
  tick. For a command, a code or a URL a dialog hands the reader, where the button is the next
  thing to press.
- `CopyAction({ text, label, reveal })`: the copy icon as an `IconAction`, whose icon becomes a tick.
  For a row's or a value's action.
- `CopyValue({ value, label, framed, wrap })`: a mono value with its `CopyAction`. `framed` draws the
  hairline card box, for a value that is the block's subject. `wrap="all"` breaks a hash anywhere;
  `"anywhere"` breaks a path or a URL at a break opportunity first.

A label is "Copy" and the thing, with no article: "Copy command", "Copy fingerprint".

## Files to save

`saveText(filename, text, type = "text/plain")` and `saveBlob(filename, blob)` hand the browser a
file. The name always comes first, as in the save dialog: the three helpers this replaced took
three argument orders, and swapped strings type-check. A CSV passes `"text/csv"`.

## Route segments

`decodeSegment(segment)` is a dynamic route segment as the name it encodes. A segment that does not
decode (`/docker/stacks/50%off`) is the name as typed, so the page says it is not there instead of
throwing a URIError onto the error page; a missing one is "". Every detail page reads its `[name]`
through it.

## The phone

Nothing here changes on a phone except `InOut layout="stacked"`, which drops its trend picture
below `sm`. `compactCount` is the width a phone row has room for; use it on a row's trailing figure
rather than `grouped`.

## Accessibility

- `Ago` renders a `<time>` with `dateTime` and the exact time as its `title`.
- `CopyButton` keeps its `aria-label` ("Copy command") while its visible word changes; `CopyAction`
  says "Copied" in its label once it has.
- `InOut`'s arrows are text and come before their figures, so the colour is never the only sign of
  the direction.

## What enforces it

- Unit tests: `lib/format.test.js`, `lib/route-params.test.js`, `lib/certificates.test.js` (the
  short and long forms and the last day).
- Browser specs that assert the words: `proxy-certificates`, `proxy-certificate-page`, `proxy-tls`,
  `proxy-config`, `proxy-ports-history`, `deploy-project`, `network-traffic-page`, `database-home`.
- WP-40 adds lint for `decodeURIComponent` outside `lib/route-params`, and for `navigator.clipboard`
  and hand copy buttons outside `copy.tsx` and `lib/clipboard`.

## Still to move

The deprecated names keep their callers working until the wave-2 page passes move them; WP-39
deletes them: `compact` and `ago` (`database/home/readings`), `grouped` and `compactCount`
(`database/data/view`), `compactCount` (`proxy/site-traffic`), `compact`
(`network/gateway/reading`), `updatedLabel`, `testedLabel`, `readLabel`, `sinceWords` (proxy),
`certificateLeft` (`lib/tls/report`), `shortIdentity` (`deploy/vocabulary`), `RatePair`, `RX`, `TX`
(`network/rate-pair`), `CopyButton` (`account-link/link-panel`), `downloadText` and `downloadBlob`
(`database/diagram/export`).

## Related

[`readings.md`](readings.md) (figures in tiles and bands), [`status-tag.md`](status-tag.md) (the
`Status` a certificate word sits on), [`values-facts.md`](values-facts.md) (a value inside a facts
line), [`../foundations/words.md`](../foundations/words.md).
