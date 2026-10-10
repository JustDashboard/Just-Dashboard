# Docker Images overhaul

The `/docker/images` redesign follows the reading register's ordered passes in
[`design-system.md`](../../internal/frontend/design-system.md), with the Packages overhaul
([PR #164](https://github.com/JustDashboard/Just-Dashboard/pull/164)) as the visual reference and the
Processes, PM2, Services and Health overhauls (#163, #165, #167, #172) for the band, the arrivals and
work in flight.

## What changed

- **Identity line.** The page opens on Docker as its mark, the engine's version, and as facts the
  image count, the layers' size, how many are in use and how many are untagged. The registry's
  verdict sits at the right end; pressing *2 updates available* narrows the table to those images.
  Pull (the page's one command) and Build sit beside it.
- **On disk.** One bar of what Docker holds: images, build cache, containers and volumes, each in the
  colour the Docker overview uses for that kind, with the part a prune would give back hatched inside
  each span. Each line keeps its own Reclaim, and Reclaim beside the total runs the safe sweep.
- **Registry.** One bar of every image by what its registry says: update available, current, pinned
  by digest, built here, not checked, not in use, untagged. Each answer is a line drawn as the
  products in it, and pressing it narrows the table. *Check now* asks the registries again, and the
  bar sweeps while it does.
- **The table.** The images are a framed table again, at the operator's request (the Processes,
  Packages and Services tables are the precedent). Each row shows the image as its product, with
  the registry and tag set a step back from the repository. Then come the containers running it, by
  name with their state's dot and each linking to its page, then the registry's answer in the band's
  colours, its age, and its size over a bar against the largest. Pull and Remove are inline; Tag,
  Copy name and Copy ID are in the row's menu. It filters by use (All, In use, Not in use,
  Untagged), searches name, version and id, and sorts largest first, newest first or by name. Below
  `xl` the containers and the answer move to the name's second line. A pulled image rises into place,
  and its row says *Pulling…* while the pull runs.
- **The sheet** (`?image=`). It opens on four readings (on disk, layers, built, version), then the
  reference and what follows from it, then the containers using the image as lit rows. *Where the
  size went* draws each layer in build order as a bar beside the instruction that wrote it, with the
  instruction's verb coloured and a RUN's command highlighted. The metadata-only steps fold away.
  The sheet ends with how the image runs, its OCI labels and its environment.
- **Pull.** A row per layer, folded from Docker's stream, shows its bytes as Docker printed them and a
  bar coloured by what is happening: received in the network colour, written in the disk colour. The
  raw output is one fold away and now keeps its line breaks.
- **Tag.** The `POST /docker/images/{id}/tag` route existed with no caller. Tag gives an image a
  second name, so the copy a container runs keeps a name when its tag is pulled again.

## Defects fixed

| Severity | Defect | Fix |
| --- | --- | --- |
| High | **Reclaim beside Images and beside Build cache ran the whole sweep** (`POST /docker/prune` without `imagesAndCacheOnly`). That sweep also deletes stopped containers, with their writable layers, and unused networks, and neither confirmation said so. | Each line calls its own route: `POST /docker/images/prune?all=true` and `POST /docker/build-cache/prune` (both existed, unused). Only Reclaim beside the total runs the sweep, and its confirmation names everything it reaches. |
| High | **A finished pull started again about once a second.** The server closes the pull socket when a pull ends, and `useSocket` reconnects whatever is still enabled. The old hook stayed enabled after `done`, and Close did not reset it, so every reconnection was another audited pull for as long as the page was open. | The socket is enabled only while the pull is in flight, and closing the dialog resets it. `docker-images.spec.ts` counts socket openings: 1 with the fix, 3 within 2.5 s with the old condition restored. |
| Low | The pull output and the sheet's environment joined their lines with `\n` inside a `Well` that collapses whitespace, so a pull's transcript read as one run-on paragraph. | `whitespace-pre-wrap` on both. |
| Low | The disk panel said "48 entrys". | Plural is `entries`. |

## Visual evidence

Screenshots use the mocked host in
[`docker-images-fixture.ts`](../../../frontend/tests/browser/docker-images-fixture.ts). It has fourteen
images: services running from registry tags (two of them behind), the previous Postgres left by an
upgrade, a build base, an application built here with a second tag, a digest-pinned proxy, a registry
that answered 429, a stopped workflow engine and two untagged layers. The before shots are a
production build of `7be11ba0`; the after shots are this branch's production build. No live Docker
was touched.

| Surface | Before | After |
| --- | --- | --- |
| Page, 1440 | [Before](before-page-1440.png) | [After](after-page-1440.png) |
| Page, 1280 | [Before](before-page-1280.png) | [After](after-page-1280.png) |
| Page, 1720 | [Before](before-page-1720.png) | [After](after-page-1720.png) |
| Page, phone | [Before](before-page-390.png) | [After](after-page-390.png) |
| Image sheet, 1440 | [Before](before-sheet-1440.png) | [After](after-sheet-1440.png) |
| Image sheet, phone | [Before](before-sheet-390.png) | [After](after-sheet-390.png) |
| Pull in progress | [Before](before-pull-1440.png) | [After](after-pull-1440.png) |

Also recorded: the table at [1440](after-table-1440.png), [1280](after-table-1280.png) and on a
[phone](after-table-390.png), the [registry filter](after-filter-1440.png) and the
[Tag dialog](after-tag-1440.png).

Capture them against a running production build:

```bash
JD_BROWSER_BASE_URL=http://127.0.0.1:43190 bun docs/audits/2026-10-08-docker-images-overhaul/capture.ts after
```

## Verification

`bun run build` passed. `JD_BROWSER_BASE_URL=http://127.0.0.1:43190 scripts/test-changed.sh patch/0.7.1`
ran Prettier, ESLint, TypeScript and all 3,073 unit tests (passed). It selected nine browser specs
(301 tests): the Docker, design-system and navigation specs, plus the database, deployment,
command-search and Health specs, because `finding-actions.ts` imports `docker-prune.ts`. The shared
machine's load average was 180 during the run. The run passed 136 tests and timed out on 37 before it
was stopped; none of the timeouts was on the Images page. Every test was then run to completion in
smaller batches:

```bash
cd frontend
JD_BROWSER_BASE_URL=http://127.0.0.1:43190 bunx playwright test tests/browser/docker-images.spec.ts tests/browser/docker-ui.spec.ts --workers=2 --timeout=120000
JD_BROWSER_BASE_URL=http://127.0.0.1:43190 bunx playwright test tests/browser/design-system.spec.ts tests/browser/navigation.spec.ts tests/browser/server-advisor.spec.ts --workers=2 --timeout=120000
JD_BROWSER_BASE_URL=http://127.0.0.1:43190 bunx playwright test <each test that timed out> --workers=1 --timeout=150000
```

All 301 passed, with no source or test change between runs. The thirteen `docker-images.spec.ts`
tests cover:

- the identity line, both bars, the columns and a row's containers;
- the band, verdict and use filters, and the order;
- the sheet's layers and who runs the image;
- the pull's layer rows and that a finished pull opens the socket only once;
- each disk line calling its own route;
- removing an image nothing runs, and tagging;
- a read-only reader;
- the phone table;
- containment at 390, 768, 1024, 1280 and 1720.

Restoring the old socket condition made the pull test fail with three pulls in 2.5 s.

## Documentation review

Updated the design-system passes (§2's plain blocks, §12's cards-and-tables argument and a §15
paragraph for this page), the feature map, the Docker feature panels, workspace interactions, and the
index of audits. No backend route, security boundary, configuration, dependency, command or
contributor workflow changed, so `AGENTS.md`, `README.md` and `CONTRIBUTING.md` need no update.
