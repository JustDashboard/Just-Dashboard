# Image vulnerabilities

A deployment is rebuilt when its code changes. Its base image changes without it: the distribution
publishes a fixed OpenSSL, the tag the recipe names (`node:22-alpine`) moves to an image with the fix,
and the release keeps serving from the digest it was pinned to on the day it was built. The watch reads
each release's image for known vulnerabilities, after the build and every day while it is live, and
puts two facts side by side — **a finding with a published fix**, and **a base whose tag now names a
newer image** — because together they say a rebuild is likely to fix something, which is the one thing
the reader can do about it.

A scan counts everything Trivy finds, and most of it is nobody's to fix from the dashboard. One
Next.js release on the Node recipe reported 183 findings, 67 of them high or worse "with a fix": 148 were
Go's standard library inside two copies of esbuild's binary (a bundler, brought in by `drizzle-kit`, a
devDependency, because the recipe ships the whole `node_modules`), 34 were in npm's own packages and
Alpine's in a `node:22-alpine` that was already the newest, and none were in anything that served a
request. So every finding is read for **whose it is** (the application's, a development tool's, a
base's) and **what the reader can do** (upgrade it, rebuild, or nothing), and the overview, a release's
row and the alert say only what can be done.

The scanning is `internal/imagescan`; what is kept of it, and when, is `deploy/image_scan.go`; the runs
are `deploy/image_scan_watcher.go`; the routes, the host alert and the wiring are
`api/handlers_deploy_image_scans.go` and `api/host_alert_readings.go`.

## The scanner

Trivy ([`imagescan.Image`](../../../backend/internal/imagescan/scanner.go), `aquasec/trivy:0.76.1` pinned
by tag **and** index digest, so the tag cannot be moved under it), run by the daemon as a one-shot
container (`dockerx.RunToCompletion`, removed whatever happens):

- **No network at all** (`NetworkMode: none`), no capabilities (`CapDrop: ALL`), a read-only root, no
  new privileges, as nobody (`65534:65534`), with a memory, CPU and process limit and capped logging.
- It sees four things: the database volume (`jd-image-scan-db`) read-only at `/cache`; the image as
  the archive `docker save` writes, read-only at `/scan/image.tar`; a folder for its report at `/out`
  and one for its scratch at `/work`. The three folders are one scan's workspace under
  `<JD_DATA_DIR>/image-scans/scan-<random>/`, removed when it ends. It never sees the Docker socket — an
  image reaches it as an archive, which is why the archive is written rather than the scanner asking
  the daemon.
- On containerd's image store a release's own layers are zstd and its bases' layers the gzip they were
  pulled with (`dockerx/build_export.go`), and the archive carries them as they are. Trivy 0.76.1 reads
  both: a zstd image and a gzip image of the same build gave the same 929 packages and 43 findings.
- `trivy image --input /scan/image.tar --offline-scan --skip-db-update --skip-java-db-update
  --skip-vex-repo-update --skip-version-check --disable-telemetry --scanners vuln --cache-backend memory
  --list-all-pkgs=false --format json --output /out/report.json`. Its report is read into the
  findings (`ParseReport`): a report of another schema version is refused, never read as clean.
- An archive larger than 8 GiB is not written (`MaxImageBytes`), and a report larger than 64 MiB is not
  read.
- Each language package keeps **where it is in the image** (`Finding.Path`): Trivy's `PkgPath` (the
  package.json or metadata file it read), or for a Go or Rust binary, the binary. Two copies at two
  paths are two findings: npm's own `glob` and the application's are different things to fix.

### Which Node packages the application runs

Trivy reads every `package.json` under `node_modules` but cannot say whether the application runs a
package. When a finding is under `node_modules`, the **dashboard reads the same archive itself**
(`imagescan.ReadNodeGraph`, `nodegraph.go`), as data: the layers in the manifest's order, gzip, zstd or
plain, keeping only the `package.json` files (at most 1 MiB each, 200,000 in all) and the links under
`node_modules`, with whiteouts and opaque folders applied. Nothing is extracted to disk and nothing in
the image runs; the layers may decompress to at most four times the archive bound, and an archive past
any bound is left unclassified. From each application folder (a `package.json` outside `node_modules`)
and each package at the top of an ownerless `node_modules` (a global install such as
`/usr/local/lib/node_modules/npm`), `dependencies`, `optionalDependencies` and the peers that are not
optional are followed the way Node resolves them: from the package's real path (links followed, as
pnpm's and Bun's stores need), the nearest `node_modules/<name>` first. What that reaches is
`direct` or `indirect`, with the dependency the application names that brought it (`Via`). The
`devDependencies` are followed afterwards, and what only they reach, or nothing reaches, is
`development`. A binary is the package's it sits in: esbuild's Go is `development`, via `drizzle-kit`.
On that release the reading took about 8 seconds over a 340 MB archive, beside Trivy's own.

An archive that cannot be read leaves the findings unclassified, which count as the application's:
a reading that fails says too much, never too little.

At start-up the server removes any scanner container a stopped process left behind (`DockerDaemon.Sweep`,
by the `io.just-dashboard.image-scan` label) and puts a scan that was in flight back in the queue.

## The database

Trivy would fetch its database itself, but only with a network, and a container with a network can
reach whatever the host's bridge reaches. So the **dashboard** fetches it (`imagescan/database.go`), as it
already talks to registries for the deployments' update checks:

1. The artifact's manifest from `mirror.gcr.io/aquasec/trivy-db:2`, else `ghcr.io/aquasecurity/trivy-db:2`
   (Trivy's own order), answering a registry's bearer challenge with an anonymous pull token. A
   challenge whose realm is not https is refused, so it cannot send the download anywhere over plain
   HTTP.
2. The one layer of type `application/vnd.aquasec.trivy.db.layer.v1.tar+gzip`, at most 2 GiB, written to
   `<JD_DATA_DIR>/image-scans/database-layer.tar.gz` while it is counted and hashed, and removed unless
   it is exactly the size and the sha256 the manifest names.
3. Only then is the volume touched: the layer is repacked so that **only** `db/trivy.db` and
   `db/metadata.json` reach it — any other entry, a path that climbs out, a link, is left out, and both
   files must be there — owned by root and readable by the scanner, and copied in through a container of
   the scanner's image that is created and never started (`DockerDaemon.CopyIntoVolume`, then removed).
   The metadata must be schema 2.

The state (`<JD_DATA_DIR>/image-scans/database.json`) records when the database was **built**
(`UpdatedAt`, which its age is counted from), when its publisher says the next one is due
(`NextUpdate`, about a day later), the digest and the source, and the last attempt and its error. A
download is attempted when there is no database or `NextUpdate` has passed — so about once a day — and a
failed one waits an hour (`DatabaseRetry`) before the next, rather than being retried before every scan.
**A database that cannot be replaced is still read**: the scan goes on with it and states its age
(`databaseAgeHours` on the scan, the age on the panel), and Build settings show the last error. A copy
that fails part of the way marks the volume as holding none until a download succeeds.

The footprint: the scanner image (about 260 MB), the database (about 1.6 GB in the volume, 140 MB
downloaded a day) and, for each scan, the image's archive for as long as the scan runs.

## What is scanned, and when

`deploy.OrchestrationStore.ImageScanTargets` reads a release's targets:

- **Its own images**: every `image` artifact — one per Compose service — by the local image id the build
  recorded (`metadata.configDigest`), else its reference. An image no longer on this server is not
  pulled (it may never have been in a registry) and that target fails with the reason.
- **The bases it was pinned on**: `provenance.builder.baseImages`, the reviewed bases the recipe and
  static builds resolve to digests before rendering their Dockerfile (`build_artifacts.go`). Each is read
  by repository and digest (`node@sha256:…`) — the tag has moved since, which is the point — pulled
  first when the daemon does not hold it (BuildKit fetched it into its own store) and removed again
  afterwards. A custom Dockerfile's `FROM` lines are not pinned by the dashboard, so such a release has
  no bases here.

`ImageScanWatcher` runs one scan at a time, never inside a run: it is a run observer, and a run that
succeeds with a release not yet scanned queues one (`build`); `POST …/vulnerabilities/scan` queues one
(`manual`); and every live release whose latest scan is a day old (or a failed one an hour old) is due
(`schedule`). The bases are read after a build and when a person asks; **the daily scan carries their
findings forward** from the scan before, since a base is pinned by digest and only the database it is
read against moves. Previews are never scanned: a preview is production's code at a pull request's head,
and lives for an afternoon.

A release's own findings that a base carries too (the same vulnerability, package and installed
version, at the same path for a language package) are marked `fromBase`, with the pinned digest of each
base that carries it (`baseDigests`): a newer image of that base may fix those; the rest came with the
application. A base read before paths were kept matches on the package alone.

## Whose a finding is, and what can be done

`imagescan.FindingOrigin` reads the marks; `ImageScan.read` adds what can be done
(`ImageScan.actionOf`) when a scan is read, so it follows the base check and the build method:

| Origin | What it is | Action |
| --- | --- | --- |
| `base` | `fromBase` | `rebuild` while the tag of a base that carries it names a newer image; nothing otherwise — it is fixed when that base's maintainers publish one. In a Node and Bun build, a newer Bun is no rebuild for what Node carries. A finding marked before `baseDigests` were kept reads any base that moved as its own until the next scan marks it |
| `development` | `dependency: development` | nothing: a development tool's fix is its makers' (esbuild's Go), and nothing the application runs needs it |
| `app` | everything else: what the application runs, and packages its build installed | `upgrade` for a language package; `rebuild` for a distribution package the build installed, when the build is one the dashboard runs (`Rebuildable`), since a build without the cache installs the fixed one |

Only a fixable finding has an action. `ImageScan.Actionable` counts those with one, and `Origins`
counts every finding by origin (kept in `counts_json` beside `all` and `fixable`).

## Whether a newer base exists

`ReleaseBaseUpdates` asks the registry what each base's tag names now — the same distribution read
`ReleaseUpdate` makes for the release comparison, through the artifact builder's backend — and compares
it with **the digest the release pinned**, not with whatever the tag names on this server today. For a
release that is an upstream image (an image source), its own tag is compared the same way; an image that
exists only on this server is `local`, with no tag to have moved. Each base is `current`, `outdated` or
`unknown` with the registry's reason. The read goes to the registry every time: the ten-minute memory and
the outage fallback a build's preparation resolves bases through (`build_base_resolution.go`) are the
builder's, not this check's, so a build that reused an answer never hides a tag that has moved since. It is read at scan time and stored with the scan, so the alert,
which reads every minute, never asks a registry anything.

## What is kept

One row per release (`deploy_image_scans`, its latest scan; it goes with its release): the state
(`queued`, `scanning`, `scanned`, `failed`), the trigger, the scanner's version, when the database it
read was built, the counts of every finding by severity, of the fixable ones and by origin, the
release's own findings worst first (at most a thousand), each target read (with a base's findings, for
the next day's carry-over), the base check, and `reading`: how the findings were read
(`imageScanReading`, 1 since paths and dependencies were kept). A live release whose scan has an older
reading is **due again at once**, its bases read again rather than carried; until then a binary under
`node_modules` in it reads as a development tool's. **A scan that fails keeps the findings of the last one that read the
image**, so a Docker or a registry that was away for a minute does not read as an image with nothing
wrong with it. The summary — the fixable findings and the unfixed ones at or above the floor — is
computed when it is read, so changing the floor needs no rescan. Each of its lists holds at most 200
(`imagescan.MaxListed`); a list cut at that bound keeps the findings with an action before the rest, and
the verdicts are read before the cut, from every finding the row kept: the alert's `BaseFixable` and the
overview's count, `Actionable`. A development tool's criticals filling the list cannot clear the alert or
make the overview say there is nothing to fix.

## The terms

Per environment (`deploy_image_scan_policies`; no row is the default): scanning **on**, counting from
**HIGH**. The floor (`CRITICAL`, `HIGH`, `MEDIUM` or `LOW`; an unrated finding is never at a floor) is
the lowest severity the panel lists, the releases list marks and the alert is raised at; every finding
is still counted. Turning scanning off stops the scans and takes the environment out of the alert;
turning it back on asks for the live release at once. Set in Build settings → Vulnerability scanning
(`settings/vulnerability-scan.tsx`), by an administrator in a session, and they apply at once.

## The alert

`image_vulnerable`, a host alert kind with nothing to set ([host alerts](../backend/observability-security.md)),
reads `LiveImageScans`: for each scanning environment's live release, firing when its scan found a
fixable vulnerability at or above the floor **that a base carries** **and** the tag of that base now
names a newer image (`ImageScan.Vulnerable`, over `ImageScan.NewerBaseFixes`, the part of
`ImageScan.BaseFixable` whose own base moved). A development tool's finding, or the application's own,
is never the alert: a newer base fixes neither. Nor is a base's finding when only another base moved.
It is told once when it starts and once when it ends, as every host alert is, through the deployments'
notification channels: "Release 12 has 1 critical and 2 high findings with a fix (CVE-… in zlib
1.2.12-r0, fixed in 1.2.12-r2), and node:22-alpine now names a newer image than the one it was built
on." A scan not run yet, in flight or failed keeps the subject where it was. A fixable finding with no
newer base is not the alert — a rebuild would pin the same digest — though the report still lists it.

## The rebuild

There is no rebuild route. The panel offers the run the runs route already takes —
`POST /deploy/{id}/environments/{env}/runs` with `force_build` and, for a remote Git source, the live
release's commit as `sourceRevision` — so it is queued, gated, audited and rolled out as every build
is. Its preparation resolves each base's tag to the digest the registry names now (`resolveBases`) and
builds with no cache, which is what re-pins the base; it builds the environment's current configuration,
as any build does. It is offered for the live release of a recipe, static, Dockerfile or Compose build
(`ImageScan.Rebuildable`) to a principal with `service.control`, when a fixable finding at the floor has
the `rebuild` action: a base whose tag names a newer image, or a distribution package the build
installs. For an upstream image the route gives no rebuild and says to check the source again in
Settings, since the image's digest is pinned when its source is detected, not when it is built.

## Routes

| Route | Who | What |
| --- | --- | --- |
| `GET /deploy/{id}/environments/{env}/vulnerabilities[?release=]` | whoever reads the environment; a deploy key | The terms, whether there is a scanner, the database's state, the live (or named) release's scan, the rebuild to offer, and every release's mark |
| `PUT /deploy/{id}/environments/{env}/vulnerabilities` | `system.admin`, session | The terms; audited `deploy.vulnerabilities.update` |
| `POST /deploy/{id}/environments/{env}/vulnerabilities/scan` | `service.control`; a deploy key | Queue a scan of the live release (or `releaseId`) with its bases; audited `deploy.vulnerabilities.scan`; 409 without Docker, without a release, with scanning off, or for a preview |

Each has its line in `deployOperations` (`api/deploy_openapi.go`), so the three are in the deploy
API's description, `docs/api/deploy-openapi.json`.

## The pages

- The project overview's **Vulnerabilities** panel (`deploy/vulnerabilities.tsx`, its words in
  `vulnerabilities-model.ts`) says **only what can be done**. With something to do: "2 high or worse to
  fix in release 2" on Trivy's mark, the count in the header (both from `actionable`, counted before the
  list was cut), and one row per action (`scanActions`) — a
  rebuild first ("Rebuild on the newer node:22-alpine", what it fixes), then one upgrade per package
  ("Upgrade next to 16.4.7", the version now, and whether package.json names it or what brought it),
  with **Rebuild** in the header. With nothing to do it is one sentence and no verdict in the header:
  "Nothing high or worse to fix in release 1 — 183 known: 149 in development tools shipped with it and
  34 in the node:22-alpine base image", and an own package at the floor with no fix yet is said, not
  counted. No total in the title, no bar of severities, no advisories: a total read as that many
  things wrong with the application when none of them were. An end-of-life distribution and a scan that
  did not finish are called out. **Report** in the head leads to the rest; it is there as soon as the
  live release can be scanned, before its first scan too, since **Scan now** is the report's verb
  (`reportReachable`).
- The **report** (`/deploy/[id]/vulnerabilities`, `deploy/vulnerability-report.tsx`): how many were
  found and whose they are, when and against how old a database, each base with whether it is the newest,
  and one section per origin — *Your app's packages*, *Development tools shipped with it* (naming the
  devDependencies that brought them), *The node:22-alpine base image* (which of its bases names a newer
  image that a rebuild picks up, with the fixed version shown only on a finding whose own base moved) — each with its counts by severity, a sentence on what can be done about it, and its findings at
  the floor as one row per package copy (`groupFindings`), on the mark of its distribution or ecosystem
  (Go's for a binary's standard library), each advisory under it. The version that fixes a package is
  shown where the fix is the reader's to take — their own packages, and a base's once a newer image is
  out — never for a development tool's. Trivy lists every release line with a fix ("6.29.1, 7.30.1,
  8.12.2"), so `nearestFix` takes the one on the line installed, and `cleanTitle` drops the package it
  repeats in front of its titles. **Scan now** and **Rebuild** are its verbs; the terms are a link away.
- The deployments list marks a release whose scan found something at the floor that an upgrade or a
  rebuild fixes — a shield and "2 to fix", a `Status`, since it changes with the next scan — and
  nothing otherwise (`ImageScanBadge.Actionable`).

## Tests

- `imagescan/report_test.go` reads Trivy 0.76.1's own reports of `alpine:3.12` and `alpine:3.22`
  (`testdata/`), the fixable and floor logic, the bounds, the paths and the base marks;
  `nodegraph_test.go` builds `docker save` archives (gzip, zstd, plain; whiteouts, hard links, a pnpm
  store of links, a link loop) and reads which packages the application runs; `scanner_test.go` the
  registry flow against a fake registry (bearer token, digest check, a tampered layer, a layer with
  other entries, the retry interval, a stale database still read) and the scanner's confinement.
- `deploy/image_scan_test.go`: the terms, the queue and the daily schedule, previews, the floor at
  read time, a failed scan keeping its findings, whose each finding is and what can be done, the
  verdicts read before a full list is cut, a newer image of one base read only for what it carries, an
  older reading due again, the base comparison, the watch's base carry-over and the run observer.
- `api/handlers_deploy_image_scans_test.go`: the alert's reading, once on the way in and once on the way
  out through the server's own observer, and the routes; `deploy_keys_test.go` holds the deploy key's
  answers.
- `TestLiveImageScanStoresFindingsAndRaisesTheAlert` (`JD_DEPLOY_LIVE=1`, as root, about 40 seconds;
  [CONTRIBUTING](../../../CONTRIBUTING.md)) runs the real scanner image against a real database fetched
  into a volume of its own: a release on `alpine:3.12` pinned on a moved `alpine:3.22` stores zlib's
  CVE-2022-37434 as a fixable critical from its base, is found vulnerable and is told; one on today's
  `alpine:3.22` has nothing at the floor and is not.
- `tests/browser/deploy-vulnerabilities.spec.ts`: the panel with something to do and with nothing
  (the 183-finding release), its Rebuild, the report's sections and Scan now, the release mark and the
  terms.

## Limits

- Only what Trivy's database knows is found, and only in what Trivy reads in an image: OS packages and
  the lock files and installed packages of the languages it supports. A vulnerability in code the
  application wrote is not a finding.
- Whether the application runs a package is read for Node only. A Python, Ruby or Java image's
  language packages are all the application's.
- "Runs" is what `dependencies` reach. A development tool the start command runs anyway
  (`bunx drizzle-kit migrate`) is still `development`: its findings are in a tool, not in what serves
  a request.
- The recipe still ships development dependencies, which is why they are here to be counted; an image
  without them would not carry them at all.
- A custom Dockerfile's bases are not pinned by the dashboard, so a Dockerfile release has no newer-base
  reading and is never the alert, though its findings are listed and its rebuild offered.
- The java index database is not downloaded, so a JAR is matched only by what the vulnerability
  database knows of it offline.
- A rebuild re-pins a base; whether the newer base actually contains the fix is known only when the
  rebuilt release is scanned, which happens after its run.
