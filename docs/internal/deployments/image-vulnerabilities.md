# Image vulnerabilities

A deployment is rebuilt when its code changes. Its base image changes without it: the distribution
publishes a fixed OpenSSL, the tag the recipe names (`node:22-alpine`) moves to an image with the fix,
and the release keeps serving from the digest it was pinned to on the day it was built. The watch reads
each release's image for known vulnerabilities, after the build and every day while it is live, and
puts two facts side by side — **a finding with a published fix**, and **a base whose tag now names a
newer image** — because together they say a rebuild is likely to fix something, which is the one thing
the reader can do about it.

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
- `trivy image --input /scan/image.tar --offline-scan --skip-db-update --skip-java-db-update
  --skip-vex-repo-update --skip-version-check --disable-telemetry --scanners vuln --cache-backend memory
  --list-all-pkgs=false --format json --output /out/report.json`. Its report is read into the
  findings (`ParseReport`): a report of another schema version is refused, never read as clean.
- An archive larger than 8 GiB is not written (`MaxImageBytes`), and a report larger than 64 MiB is not
  read.

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
version) are marked `fromBase`: a newer base may fix those; the rest came with the application.

## Whether a newer base exists

`ReleaseBaseUpdates` asks the registry what each base's tag names now — the same distribution read
`ReleaseUpdate` makes for the release comparison, through the artifact builder's backend — and compares
it with **the digest the release pinned**, not with whatever the tag names on this server today. For a
release that is an upstream image (an image source), its own tag is compared the same way; an image that
exists only on this server is `local`, with no tag to have moved. Each base is `current`, `outdated` or
`unknown` with the registry's reason. It is read at scan time and stored with the scan, so the alert,
which reads every minute, never asks a registry anything.

## What is kept

One row per release (`deploy_image_scans`, its latest scan; it goes with its release): the state
(`queued`, `scanning`, `scanned`, `failed`), the trigger, the scanner's version, when the database it
read was built, the counts of every finding by severity and of the fixable ones, the release's own
findings worst first (at most a thousand), each target read (with a base's findings, for the next day's
carry-over) and the base check. **A scan that fails keeps the findings of the last one that read the
image**, so a Docker or a registry that was away for a minute does not read as an image with nothing
wrong with it. The summary — the fixable findings and the unfixed ones at or above the floor — is
computed when it is read, so changing the floor needs no rescan.

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
fixable vulnerability at or above the floor **and** a base now names a newer image (`ImageScan.Vulnerable`).
It is told once when it starts and once when it ends, as every host alert is, through the deployments'
notification channels: "Release 12 has 1 critical and 2 high findings with a fix (CVE-… in zlib
1.2.12-r0, fixed in 1.2.12-r2), and node:22-alpine now names a newer image than the one it was built
on." A scan not run yet, in flight or failed keeps the subject where it was. A fixable finding with no
newer base is not the alert — a rebuild would pin the same digest — though the panel still lists it.

## The rebuild

There is no rebuild route. The panel offers the run the runs route already takes —
`POST /deploy/{id}/environments/{env}/runs` with `force_build` and, for a remote Git source, the live
release's commit as `sourceRevision` — so it is queued, gated, audited and rolled out as every build
is. Its preparation resolves each base's tag to the digest the registry names now (`resolveBases`) and
builds with no cache, which is what re-pins the base; it builds the environment's current configuration,
as any build does. It is offered for the live release of a recipe, static, Dockerfile or Compose build
(`ImageScan.Rebuildable`) to a principal with `service.control`: as the panel's command when the alert's
condition holds, and as a plain verb for a build whose bases the dashboard does not pin (a Dockerfile,
Compose) when it has a fixable finding at the floor, since that build pulls its bases again. For an
upstream image the panel says to check the source again in Settings, since the image's digest is pinned
when its source is detected, not when it is built.

## Routes

| Route | Who | What |
| --- | --- | --- |
| `GET /deploy/{id}/environments/{env}/vulnerabilities[?release=]` | whoever reads the environment; a deploy key | The terms, whether there is a scanner, the database's state, the live (or named) release's scan, the rebuild to offer, and every release's mark |
| `PUT /deploy/{id}/environments/{env}/vulnerabilities` | `system.admin`, session | The terms; audited `deploy.vulnerabilities.update` |
| `POST /deploy/{id}/environments/{env}/vulnerabilities/scan` | `service.control`; a deploy key | Queue a scan of the live release (or `releaseId`) with its bases; audited `deploy.vulnerabilities.scan`; 409 without Docker, without a release, with scanning off, or for a preview |

## The pages

- The project overview's **Vulnerabilities** panel (`deploy/vulnerabilities.tsx`, its words in
  `vulnerabilities-model.ts`): the verdict in the header, one figure per severity with how many have a
  fix, the rebuild's reason when the alert's condition holds (each moved base, pinned and current
  digest), an end-of-life distribution called out, the fixable findings at the floor with their advisory
  link and `installed → fixed`, what could not be read, and when the scan ran against how old a
  database. **Rebuild** and **Scan now** are its verbs; the terms are a link away.
- The deployments list marks a release whose scan found something at the floor — a shield and
  "2 fixable" or "3 unfixed", a `Status`, since it changes with the next scan — with the counts in its
  title.

## Tests

- `imagescan/report_test.go` reads Trivy 0.76.1's own reports of `alpine:3.12` and `alpine:3.22`
  (`testdata/`), the fixable and floor logic, the bounds and the base marks; `scanner_test.go` the
  registry flow against a fake registry (bearer token, digest check, a tampered layer, a layer with
  other entries, the retry interval, a stale database still read) and the scanner's confinement.
- `deploy/image_scan_test.go`: the terms, the queue and the daily schedule, previews, the floor at
  read time, a failed scan keeping its findings, the base comparison, the watch's base carry-over and the
  run observer.
- `api/handlers_deploy_image_scans_test.go`: the alert's reading, once on the way in and once on the way
  out through the server's own observer, and the routes; `deploy_keys_test.go` holds the deploy key's
  answers.
- `TestLiveImageScanStoresFindingsAndRaisesTheAlert` (`JD_DEPLOY_LIVE=1`, as root, about 40 seconds;
  [CONTRIBUTING](../../../CONTRIBUTING.md)) runs the real scanner image against a real database fetched
  into a volume of its own: a release on `alpine:3.12` pinned on a moved `alpine:3.22` stores zlib's
  CVE-2022-37434 as a fixable critical from its base, is found vulnerable and is told; one on today's
  `alpine:3.22` has nothing at the floor and is not.
- `tests/browser/deploy-vulnerabilities.spec.ts`: the panel, its Rebuild and Scan now, the release
  mark and the terms.

## Limits

- Only what Trivy's database knows is found, and only in what Trivy reads in an image: OS packages and
  the lock files and installed packages of the languages it supports. A vulnerability in code the
  application wrote is not a finding.
- A custom Dockerfile's bases are not pinned by the dashboard, so a Dockerfile release has no newer-base
  reading and is never the alert, though its findings are listed and its rebuild offered.
- The java index database is not downloaded, so a JAR is matched only by what the vulnerability
  database knows of it offline.
- A rebuild re-pins a base; whether the newer base actually contains the fix is known only when the
  rebuilt release is scanned, which happens after its run.
