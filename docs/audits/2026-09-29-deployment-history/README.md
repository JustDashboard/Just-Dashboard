# Deployment history layout

`/deploy/[id]/deployments` puts success rate and weekly frequency with the daily chart, and release
and recovery times in a compact timing list. Their calculation basis and window remain visible.
The separate four-tile strip is gone. Status filters use the shared underlined appearance, retain
counts and keyboard focus, and allow an empty result with Clear filters.

Switching status or environment reserves the height of the history and its pagination for the page
visit. Shorter results leave space below them rather than clamping the shell's scroll and moving the
controls. History rows skip the arrival stagger when filtering.

`frontend/tests/browser/deploy-history.spec.ts` checks All, Ready, Failed, an empty Cancelled filter,
Clear filters and an environment with one run at 390, 1280 and 1720 pixels. It checks the scroll
position and filter geometry, the delivery figures and their basis, keyboard selection, and page
overflow. The phone uses reduced motion; desktop uses normal motion. Against the original page, the
1280-pixel scroll regression moved from 473px to 0 when Failed was selected.

| Capture | Image |
| --- | --- |
| Desktop before (1280) | [Before](before-desktop.png) |
| Phone before (390) | [Before](before-phone.png) |
| Desktop after (1280) | [After](after-desktop.png) |
| Wide desktop after (1720) | [After](after-wide.png) |
| Phone delivery (390) | [After](after-phone.png) |
| Phone filters (390) | [After](after-phone-filters.png) |

Captures use mocked API responses and a 24-run production history. Local validation passed:
`cd frontend && bun run build` and
`JD_BROWSER_BASE_URL=http://127.0.0.1:43127 scripts/test-changed.sh patch/0.7.1`.
The scoped gate passed formatting, lint and types, 1,255 logic tests and 340 browser checks.
This change affects presentation and local filtering; deployment execution and backend boundaries
are unchanged, so it does not require the live Docker or Go race lanes.
