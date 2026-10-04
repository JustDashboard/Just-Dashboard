# Terminal pane detachment and window docking proof

Split panes now share their original window's tab. Every pane offers **Open as separate window**
beside Close, which detaches that existing shell into a standalone terminal tab. Dragging a window
tab onto any visible pane previews the left, right, upper or lower half nearest the pointer. Dropping
moves that existing window into the split and focuses it. An existing split group can also be moved
as a whole. The Split terminal menu offers the same placement without dragging.

## Recorded proof

The [28-second recording](native-drag-proof.webm) uses the production frontend and real held Linux
PTYs running the isolated resize-aware test program. It shows two panes with one window tab, the
detach button restoring a second tab, a single held drag moving through all four overlay directions,
a drop below, detaching the original group owner, and a drop to the right. The final screens show
different input received by the two shells. No agent command or operator workload is involved.

| Verified behavior | Screenshot |
| --- | --- |
| Two panes, one tab, detach and close controls on each pane | [Split panes](native-split-hidden-from-windows.png) |
| Detached pane appears as its own selected window tab | [Separate window](native-detached-window.png) |
| Live placement preview follows one held drag | [Left](native-drop-overlay-left.png), [Above](native-drop-overlay-up.png), [Right](native-drop-overlay-right.png), [Below](native-drop-overlay-down.png) |
| Dropped shells resize and receive input | [Below](native-dropped-below.png), [Right](native-dropped-right.png) |

For comparison, the [previous split capture](../2026-10-04-terminal-splits/native-three-pty-splits.png)
shows each split leaf in the window strip and only Close in its pane header.

The live browser assertions compare each visible xterm grid to kernel PTY rows/columns, require one
WebSocket attachment per terminal throughout detach/dock, and verify that typed input reaches only
the focused shell. Mocked browser checks exercise completed drops in all four directions in both
DOM and WebGL renderers, nested placement, owner detachment, overlay cancellation/exit, same-group
and undersized drops, unrelated drag types, and the non-drag menu alternative. Pure layout tests
cover legacy tab migration, immutable membership changes and whole-group minimum sizes.

## Reproducing the recording

Build and start the frontend from the task worktree:

```sh
cd frontend
bun run build
bun run start --hostname 127.0.0.1 --port 43130
```

Start the isolated existing backend harness in another terminal:

```sh
cd backend
JD_TERMINAL_BROWSER_EVIDENCE_DIR=/tmp/jd-terminal-drag-live \
  go test ./internal/api -run '^TestTerminalBrowserEvidenceServer$' -count=1 -v -timeout=20m
```

Once its `ready.json` exists, run the focused proof:

```sh
cd frontend
JD_BROWSER_BASE_URL=http://127.0.0.1:43130 \
  JD_TERMINAL_LIVE_READY=/tmp/jd-terminal-drag-live/ready.json \
  JD_TERMINAL_EVIDENCE=/tmp/jd-terminal-drag-native \
  bunx playwright test tests/browser/terminal-live.spec.ts \
    --grep 'native panes detach' --workers=1
```

Screenshots go to the evidence directory; Playwright writes the video under `test-results/`.
Create `/tmp/jd-terminal-drag-live/stop` to stop the owned harness. It uses temporary HOME/data,
loopback listeners and native PTYs, and cleans up only its own shells. Its browser context bypasses
CSP to reach the fixture's second loopback port; production authentication, CSP and routes retain
their existing contracts. The normal changed-file gate skips the opt-in live tests.

## Local validation

The final production build passed. `JD_BROWSER_BASE_URL=http://127.0.0.1:43130
scripts/test-changed.sh patch/0.7.1` passed Prettier, ESLint, TypeScript, all 2,919 unit tests and
84 selected browser tests. Its two opt-in live tests were skipped there and passed separately.
The native detach/dock proof passed. The existing live terminal regression also passed,
covering directory inheritance, agent launch, nested resize, focused input, fullscreen and phone
containment. New shell commands wait for the shell prompt; a socket can attach before rc startup
finishes. The final isolated backend harness exited cleanly after its source shell exited normally.
Documentation review updated the operator README and terminal feature guide; architecture,
security boundaries, dependencies, contributor commands and CI are unchanged.
