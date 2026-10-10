# Files workspace review — 2026-10-02

Files and its full editor use the reading register. The workbench frame owns scrolling panes;
counts stay in status strips, file identities use the existing format/product marks, and page
commands share a 32px height.

The before images are the operator-supplied tile and toolbar references. After images use mocked
API fixtures from `frontend/tests/browser/files-ui.spec.ts`, with a signed-in fictional operator,
`notes.md` and a generated sample image. They contain no live server files or credentials.

| Surface | Evidence |
| --- | --- |
| Original folder tile and command strip | [Tile](before-tile.png), [toolbar](before-toolbar.png) |
| README gallery, using an expanded sample project and Compose preview | [1720px](../../files.png) |
| Compact tiles and normalized commands | [1280px](after-tiles-1280.png), [1720px](after-tiles-1720.png) |
| Immediate fuzzy name results | [Search palette](after-find-1720.png) |
| Full text workspace and collapsible tree | [1280px](after-editor-1280.png), [1720px](after-editor-1720.png), [375px](after-editor-phone.png) |
| Shared image editor | [1720px](after-image-editor-1720.png), [375px](after-image-editor-phone.png) |

Browser coverage opens a fuzzy result from the keyboard, rejects a delayed stale content response,
reveals a matching line, transfers unsaved text and image drafts to the full editor, saves text,
navigates and collapses the tree, cancels dirty-file exits (including history), applies crop/undo/redo,
and decodes an exported PNG to verify live brightness changes were saved. A saved copy retains the
source's dirty guard; saving over the source clears it.

Reproduce the review captures with a production build from this tree and:

```bash
JD_BROWSER_BASE_URL=http://127.0.0.1:43127 bunx playwright test tests/browser/files-ui.spec.ts
```

The screenshots land in `frontend/test-results/`. `scripts/test-changed.sh patch/0.7.1` also runs the
selected design-system, navigation and embedding-surface checks, frontend logic tests, and backend
search/API checks. History traversal confirmation depends on the browser exposing a cancelable
Navigation API event; link and unload guards remain available in older browsers.
