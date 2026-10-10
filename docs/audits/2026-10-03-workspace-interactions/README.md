# Workspace interactions across the dashboard

Processes, Git, Logs, Docker containers, Packages, Backups, deployment setup, Audit/Security and
Metrics now preserve the context of reading, inspecting and navigating. The complete behavior and
storage rules are in [the interaction contract](../../internal/frontend/workspace-interactions.md).

## Proof

- [Packages recording](packages.webm): arrows and Enter inspect a package; Back closes its sheet and
  restores the row; local Find, Escape, shortcut help and the command palette keep the page's context.
- [Metrics recording](metrics.webm): keyboard pinning, adjacent samples, a journal handoff, mouse
  pinning that survives pointer leave, and explicit release.
- [Phone Audit recording](audit-phone.webm): visible row navigation, help, dismissal, local Find and
  typing in the filter without invoking page shortcuts.
- [Package inspector](package-inspector.png) and [shortcut reference](package-shortcuts.png).
- [Shared pinned readings](metrics-pinned.png).
- [Phone Audit row focus](audit-phone.png), 390×844 with reduced motion.

Captures use synthetic API fixtures against the task worktree's production build. They contain no
operator data. Chromium on Linux exercises browser history directly; physical mouse side buttons and
Safari trackpad gestures were not separately tested. The recording covers interaction changes rather
than a page redesign.

To reproduce the recordings and screenshots:

```sh
cd frontend
JD_WORKSPACE_VIDEO=1 JD_BROWSER_BASE_URL=http://127.0.0.1:43603 \
  bunx playwright test tests/browser/packages-ui.spec.ts tests/browser/metrics-ui.spec.ts \
  tests/browser/system-ui.spec.ts --grep 'workspace: (package links|a pinned moment|mobile audit)' \
  --workers=1 --output=/tmp/jd-workspace-proof
```

## Verification

The production build and all fifteen new `workspace:` browser scenarios passed. The focused Git
package race checks also passed. The Docker and deployment history scenarios passed eighteen repeated
runs, including long-list scroll/focus and reload restoration:

```sh
cd backend
go test -race ./internal/gitx ./internal/ghx ./internal/forgex -count=1
```

The final required gate ran against the same production build. Prettier, ESLint, TypeScript and
all 2,828 unit tests passed. Its 47 selected browser specs returned **1,055 passed, 28 skipped and
one failed** in 18.6 minutes; the command exited 1 for the existing Files history case described
below. All fifteen new interaction scenarios and the design-system checks passed in this run:

```sh
JD_BROWSER_BASE_URL=http://127.0.0.1:43603 scripts/test-changed.sh patch/0.7.1
```

During the first gate, the unchanged Files browser-history scenario failed intermittently after a
reload and toolbar Forward. The same test failed three of six repeats on an independently built,
untouched checkout of the starting commit `daa3008544391c026d72c88417b54489d0d750c9`; three passed.
The task build also returned three failures and three passes. The failed assertions expected the
photos folder after Alt+Left or Cmd+[ but the address remained on its parent. This is an existing
Files history-state race, outside the requested non-Files interactions. Files source and tests are
unchanged in this PR. The baseline reproduction used:

```sh
cd frontend
JD_BROWSER_BASE_URL=http://127.0.0.1:43604 bunx playwright test tests/browser/files-ui.spec.ts \
  --grep 'browser and toolbar history traverse folders' --repeat-each=6 --workers=3
```

Documentation review covers the complete diff against `docs/internal/`, `README.md`, `AGENTS.md` and
`CONTRIBUTING.md`. The frontend, feature and deployment setup contracts and operator instructions are
updated. No changes to contributor workflow, dependencies, backend routes, security invariants or CI
configuration require additional documentation.
