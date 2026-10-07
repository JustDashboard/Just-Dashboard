# Packages overhaul

The `/packages` redesign follows the reading register's ordered passes in
[`design-system.md`](../../internal/frontend/design-system.md), using the recent Processes overhaul
([PR #163](https://github.com/JustDashboard/Just-Dashboard/pull/163)) as the visual reference.

## What changed

- Removed the four summary tiles. Installed is in the identity and view count; by-hand and dependency
  counts are filters; updates have their queue, view count and security filter; disk size heads the
  software band.
- The whole inventory is grouped by software product, or archive section for unnamed software. The
  largest five are shares of one installed-size bar, with everything else drawn muted. Each group
  filters Installed, including dependencies. Figures glide when readings arrive and spans ease to a
  new size; reduced motion is respected.
- The update queue puts security fixes first, shows changed version fields, and carries confirmed
  security/all-upgrade actions. Failed reads and unavailable advisory data are explicit.
- Fixed responsive inventory/update columns, summaries beneath names, per-row size bars, and identity
  controls that wrap without hiding the host at narrower desktop widths.
- Catalogue suggestions are software choices; search results use the shared lit edge with a separate
  Install action. A pending query hides obsolete results.
- One package readout replaces About/How to use it tabs: versions and size, commands, registered
  services, configuration and documentation links, manual, dependencies and metadata. Commands copy;
  service/file links open the existing inspectors; dependency navigation supports Back.
- A failed usage read reports its reason and Retry instead of leaving a loader. Existing protection,
  capability checks, ordinary confirmations and resumable jobs remain in effect.

## Visual evidence

Screenshots use the mocked Ubuntu host in
[`packages-fixture.ts`](../../../frontend/tests/browser/packages-fixture.ts), with 14 packages,
including the Linux kernel, Docker and its runtime, Python and its library, development tools, Node.js,
PostgreSQL and Redis. Three packages have updates, one a security fix. The before shots use an untouched
production build of `5cdb03e8`; the after shots use this task branch's production build. No host package
was changed during validation.

| Surface | Before | After |
| --- | --- | --- |
| Page, 1440 | [Before](before-page-1440.png) | [After](after-page-1440.png) |
| Page, 1280 | [Before](before-page-1280.png) | [After](after-page-1280.png) |
| Page, phone | [Before](before-page-390.png) | [After](after-page-390.png) |
| Package sheet, 1440 | [Before](before-sheet-1440.png) | [After](after-sheet-1440.png) |
| Package sheet, phone | [Before](before-sheet-390.png) | [After](after-sheet-390.png) |
| Updates | [Before](before-updates.png) | [After](after-updates.png) |
| Catalogue | [Before](before-catalogue.png) | [After](after-catalogue.png) |
| Search results | [Before](before-search.png) | [After](after-search.png) |

Also recorded: [1720-wide page](after-page-1720.png), [software filter](after-software-filter.png),
[phone inventory](after-table-phone.png), [removal confirmation](after-confirmation.png), and
[workspace interaction recording](workspace.webm).

Capture screenshots and the recording against a running production build:

```bash
JD_BROWSER_BASE_URL=http://127.0.0.1:43371 bun docs/audits/2026-10-07-packages-overhaul/capture.ts after
```

## Verification

`bun run build` passed. The required gate is
`JD_BROWSER_BASE_URL=http://127.0.0.1:43371 scripts/test-changed.sh patch/0.7.1`, after `bun run build`.
It selects the Packages and design-system browser specs, the frontend lint/type checks and `bun test src`.
Prettier, ESLint, TypeScript and all 2,989 unit tests passed. All 88 selected browser checks passed
across the gate and its focused rerun: 83 passed initially; four timeouts during shared machine load
and an Audit number-animation alignment check passed when rerun with one worker. The rerun command was:

```bash
cd frontend
JD_BROWSER_BASE_URL=http://127.0.0.1:43371 bunx playwright test tests/browser/design-system.spec.ts tests/browser/packages-ui.spec.ts --last-failed --workers=1 --timeout=90000
```

The focused browser coverage includes group filters and dependencies, failed update/usage reads,
unknown size/advisory coverage, read-only controls, pending searches, confirmations, keyboard/history
navigation and table/control containment at 390, 768, 1024, 1280 and 1720.

## Documentation review

Updated the frontend feature guide, feature map, workspace interactions and design-system passes, and
indexed this audit. Compared the complete diff with `docs/internal/`, `AGENTS.md`, `README.md` and
`CONTRIBUTING.md`. The root guides need no change: no backend route, security boundary, configuration,
dependency, build command or contributor workflow changed.
