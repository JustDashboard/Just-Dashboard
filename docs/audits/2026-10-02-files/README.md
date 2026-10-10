# File browser spacing and interactions

The reading-register workbench keeps its panes and colour tokens. Tile columns shrink from
96/128/160px to 80/104/128px, with 4px gaps and icon/name captions; details view and the inspector
still show metadata. Entries have no overflow dots. Right-click, Shift+F10, the context-menu key and
touch long-press open their menus; closing a keyboard menu returns focus to its original entry.

Selection and clipboard commands float above the listing's foot. Their 160ms opacity/four-pixel
entrance and exit become immediate with reduced motion. They never insert a toolbar row. A plain
click inspects, double-click or Enter opens, and once selection starts a plain click anywhere on an
entry toggles it. Checkbox clicks establish the Shift-range anchor; Ctrl/Cmd toggles and Shift
selects a range. Background dragging selects intersecting entries in either direction, preserves
the initial selection with Ctrl/Cmd/Shift, scrolls at the edges and restores the snapshot on Escape
or pointer cancellation. Background clicking clears the selection. Native entry dragging retains
file/folder group moves, Ctrl/Alt copying, desktop uploads and the existing conflict decisions,
with a compact count preview, faded sources and highlighted destinations. A folder cannot advertise
a drop into itself or its descendants. The inspector returns to the current folder when its entry
was successfully moved away; copies and failed moves retain their previews. Backend routes, capability
checks and filesystem guards are unchanged.

## Rendered evidence

All captures use synthetic API fixtures and a production build from the task worktree. Desktop is
1440×900; phone is 390×844 with reduced motion. The image fixture is a transparent pixel and the
video fixture deliberately leaves its poster waiting; neither represents a real server's media.

- [Compact desktop listing](desktop.png)
- [Selection without moving the entries](selection.png)
- [Background marquee](marquee.png)
- [Phone selection actions](phone.png)
- [Selection, marquee and native group-drag recording](interactions.webm)

## Verification

`bun run build` passes. The required local gate ran against a separately managed frontend built
from this worktree:

```sh
JD_BROWSER_BASE_URL=http://127.0.0.1:43138 scripts/test-changed.sh patch/0.7.1
```

Formatting, ESLint, TypeScript and all 2,816 logic tests passed. The default four-worker browser
run passed 165 of 172 cases, including all 33 Files cases. Five Docker nine-page width sweeps hit
their 30-second case limit; the Docker volume editor and first Health-error read hit their
five-second visibility limit. The command therefore exited nonzero. All seven passed with the
same assertions in a serial rerun (the width sweeps took 20–23 seconds):

```sh
cd frontend
JD_BROWSER_BASE_URL=http://127.0.0.1:43138 bunx playwright test --last-failed --workers=1 --timeout=90000
```

All 172 reachable browser cases are verified across these runs. The gate covers changed-file
formatting/lint, TypeScript, the fast logic suite and reachable browser specs.
`selection.test.js` checks forward/reverse intersections, partial overlaps, shrinking additive
selection, boundaries and visible-order ranges. `files-ui.spec.ts` checks both views for stable
selection/clipboard geometry, whole-entry toggling, modifiers, opening, keyboard menu focus,
marquee selection/shrinking/cancellation and native file/folder group moves and Alt-copying. It also
checks edge scrolling/release, phone layout, reduced motion, touch long-press, desktop file drops,
large-directory find/keyboard selection and the existing search/editor/colour/upload interactions.
The native drag checks inspect the count preview and source/target feedback as well as the API
requests, with overwrite disabled. Successful moves clear the old inspector preview; copies and
failed moves retain it.
An additional keyboard check opens Rename from Shift+F10 with Enter, verifies that its name input
receives focus and keeps the original filename, and cancels without leaving the dialog open.

The integration follow-up waits for the listing's `animate-rise` entrance to finish before recording
the selection geometry baseline. All three selection/clipboard comparisons remain exact; this keeps
the entrance animation's subpixel movement out of the selection measurements.

Browser verification is Chromium against mocked API routes; no real server files are moved or
uploaded by these fixtures. WebKit installation was attempted, but launching it is unavailable on
this host because its GTK, ICU, GStreamer and other runtime libraries are absent. A real macOS/Safari
run is not claimed.

Documentation review covers `docs/internal/`, `README.md`, `AGENTS.md` and `CONTRIBUTING.md`.
The Files feature map, owning implementation guide, design-system workbench description and operator
instructions are updated; no contribution, security, dependency, deployment or CI workflow changes
are needed.
