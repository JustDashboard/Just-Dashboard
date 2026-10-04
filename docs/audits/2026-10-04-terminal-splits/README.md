# Terminal splits and agent launch proof

The Terminal toolbar now offers Codex, Claude, directional splits, terminal actions and fullscreen.
Search remains a keyboard command; the old search, quick-action and terminal-behaviour buttons are
removed from this page. Codex runs `codex --yolo`, and Claude runs
`claude --dangerously-skip-permissions`, in a fresh held PTY whose directory is read from the focused
window at creation time.

Splits arrange independent windows above, below, left or right. The emulators stay mounted while
layouts change; every visible pane resizes, and only the selected pane takes input. Dragging or using
the divider's arrow keys changes its size; Home or double-click balances it. Nested groups, focused
pane shortcuts, tab switching, fullscreen and a narrow viewport are covered by browser checks.

## Recorded proof

The [2-minute-40-second recording](native-pty-proof.webm) uses the production frontend and real held
PTYs with fixture agent commands. It shows a live `cd`, both exact launch commands, nested splits,
pointer and keyboard resizing, pane focus and typed input, fullscreen and a narrow viewport.
Kernel PTY rows/columns must match every visible emulator after each completed resize, and every
terminal keeps one WebSocket attachment throughout the layout changes. The commands in these
captures are fixtures; they do not show an authenticated Codex or Claude coding task.

| Check | Capture |
| --- | --- |
| Supplied original toolbar | [Before](before-toolbar.png) |
| Launch flags and live directory | [Codex](native-codex-fixture-agent.png), [Claude](native-claude-fixture-agent.png) |
| Three independent PTYs | [Initial split](native-three-pty-splits.png), [Resized](native-resized-splits.png) |
| Display during a held pointer drag | [Width](native-drag-width.png), [Height](native-drag-height.png) |
| Input sent to the selected pane | [Focused input](native-focused-input.png) |
| Different viewport sizes | [Fullscreen](native-fullscreen.png), [Phone](native-mobile.png) |

Controlled pane fitting now runs in a layout effect: the separate fit animation frame could clear
WebGL after xterm painted during dragging. Recorded samples at 45/60 seconds during width changes
and 85/95 seconds during height changes retain text in all three panes as their kernel grids change.

## Reproducing the live recording

Start a production frontend from the tree under test:

```sh
cd frontend
bun run build
bun run start --hostname 127.0.0.1 --port 43127
```

In another terminal, start the isolated real-PTY fixture:

```sh
cd backend
JD_TERMINAL_BROWSER_EVIDENCE_DIR=/tmp/jd-terminal-live \
  go test ./internal/api -run '^TestTerminalBrowserEvidenceServer$' -count=1 -v -timeout=20m
```

Once `/tmp/jd-terminal-live/ready.json` exists, run:

```sh
cd frontend
JD_BROWSER_BASE_URL=http://127.0.0.1:43127 \
  JD_TERMINAL_LIVE_READY=/tmp/jd-terminal-live/ready.json \
  JD_TERMINAL_EVIDENCE=/tmp/jd-terminal-proof \
  bunx playwright test tests/browser/terminal-live.spec.ts --workers=1
```

The fixture uses an isolated HOME, real held PTYs and native WebSockets. Stub commands report their
actual argv and directory and run a resize-aware Unicode TUI; the browser compares its xterm grid
with the kernel PTY dimensions after layout changes. The recorded input checks distinguish the panes.
The optional browser context bypasses CSP to reach the fixture on its separate loopback port;
the application continues using its existing CSP and same-origin WebSocket route.
No authenticated agent task is submitted. The harness expires after fifteen minutes or when its
recorded stop file is created. Normal local checks skip this opt-in fixture and browser test.

## Local validation

The production frontend build passed. The final `scripts/test-changed.sh patch/0.7.1` run passed
Prettier, ESLint, TypeScript, all 2,857 frontend unit tests, Go build/vet, the selected API tests and
all 94 selected browser checks. Browser checks used one worker against port 43127; the optional live
test was skipped there and passed separately against the native PTY fixture, producing this recording.

That script returned nonzero because a new Go directory fixture removed its temporary HOME before
a shell history write finished. Cleanup now stops and waits for its owned holder. All four directory
regressions subsequently passed 30 repetitions each (120 cases), with their assertions unchanged.
The shell fixture likewise requests a normal shell exit and drains asynchronous editor helpers before
removing HOME; all six Bash/Zsh launch and missing-tool cases passed 20 repetitions each (120 cases).
These cleanup changes affect test fixtures only. The targeted terminal directory/agent/activity race
tests also passed. After the cleanup fixes, `go test ./internal/term -count=1` passed the complete
affected terminal package in 24.216 seconds. The already passing browser and frontend checks were
not repeated for these Go test-only changes.
