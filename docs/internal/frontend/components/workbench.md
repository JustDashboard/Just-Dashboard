# Workbench

One frame held to the window: the panes it is made of, one pane at a time on a phone, the handles
between them, the page's keys and place, the terminal, the editor and the canvases.

**Members:** `Pane`, `PaneHeader`, `PaneFooter` (`panel.tsx`); `PaneBar`, `PaneBack`, `paneShown`
(`phone-panes.tsx`); `ResizeHandle` (`resize-handle.tsx`); `Workspace`, `WorkspaceHelp`,
`useWorkspaceFocus` (`components/workspace/`); `XtermPane` (`xterm-pane.tsx`); `CodeEditor`
(`code-editor.tsx`); `CANVAS_MOVE`, `zoomKey`, `zoomAbout`, `useCanvasPinch` (`canvas-move.ts`);
`useIsPhone`, `useMediaQuery` (`hooks/use-mobile.ts`). Coming in 0.9.0: `WorkbenchHeader` (WP-33),
`CanvasZoom` and `useFullscreen` (WP-15), `Page fill="workspace"` (WP-09).

**Register.** A workbench is a page the reader works in for minutes (the terminal, Files, Git, a
database's Data and Query, the boards, the map), not one they read. Its recipe is
[`../recipes/workbench.md`](../recipes/workbench.md); the page frame is `Page fill`, whose content
is the viewport.

## Which one

| You need | Use | Not |
| --- | --- | --- |
| A working region of the page: a rail, a tree, a console | `Pane` | a `Panel` (that is a block of content) |
| Several panes on a phone | `paneShown` on each, `PaneBar` at the foot, `PaneBack` in an opened page | stacked short boxes inside a scrolling page |
| A side column the reader can widen | `ResizeHandle` beside it | a fixed width |
| A list page's keys (rows, search, refresh, help) and the place it returns to | `Workspace` | key handlers on `window` |
| A shell | `XtermPane` | anything else; it owns the socket and the PTY's size |
| Text or code to edit | `CodeEditor` | a `Textarea` for more than a few lines |
| A canvas (React Flow) | spread `CANVAS_MOVE` and call `useCanvasPinch` | the library's default gestures |

## Pane

**Purpose.** The same frame as a `Panel` for a working region rather than a block of content; its
header and footer strips keep a faint tint, and it does not lift. **Not** a block on a reading page.

## One pane on a phone: PaneBar, PaneBack, paneShown

**Purpose.** Side-by-side columns are not smaller columns on a phone. A page with several panes
shows one, the whole height of the screen, and a bar along the foot picks which; what a row opens
is a page of its own with the bar out of the way and a back arrow in its head (the operator's rule:
one pane, a bottom bar, details as pages).

**Props.** `paneShown(shown, until)` on each pane: hidden below the break (`md` or `lg`) while it is
not the one picked, untouched from the break up. `PaneBar({ label, stops, active, onPick, until })`:
one 56px stop per pane, the glyph over its word, a `count` on the glyph's shoulder, clear of the
home indicator, not drawn from the break up. It is a bar in the bottom stack
([`overlays-confirm.md`](overlays-confirm.md)), so a dock opened over the page (held remote
sessions, the assistant's conversations) rises above its stops instead of covering them; from the
break up it measures nothing. `PaneBack` in the head of an opened page. The page
keeps its own state for which pane is shown, and holds its frame to the window below the break
(`Page fill`) so the pane has a height to be the whole of.

**Accessibility.** The bar is a `nav` named by `label` holding a button for each stop, the shown
one `aria-current="page"` and its count as a description; `PaneBack` is named "Back to the list"
unless told otherwise.

**Example.** The ten pages on it are listed in
[`../patterns/keyboard-and-place.md`](../patterns/keyboard-and-place.md): the Git workspace, the
assistant, Logs, the network tools, Files, the editor, a project's logs, the terminal, a Remote
Desktop session and the databases map.

**Enforced by** `phone-panes.test.js` and the specs of those pages at 390.

## ResizeHandle

**Purpose.** The grab strip between a side column and the work. Pointer capture rather than a
window listener, so a drag over the terminal or an iframe still ends; the page keeps the width and
decides its limits; arrows nudge it and Home resets (it is a `separator` with a value); a
double-click resets. `side` says which side of the handle the column is on.

## Workspace

**Purpose.** Page-owned keyboard commands and place memory for a list page: its read commands in the
palette's *This page* group, Ctrl/Cmd+F to the page's search, F5 and Ctrl/Cmd+R to refresh its
reads, arrows, Home/End and typing to move between rows, Enter to open, `?` for help
(`WorkspaceHelp`), and the row and scroll it returns to. Editors and portalled controls own their
own keys. The rules are in [`../patterns/keyboard-and-place.md`](../patterns/keyboard-and-place.md).

## XtermPane and CodeEditor

`XtermPane` is an xterm.js terminal on a PTY over a socket: raw bytes both ways, resize as a control
message, the palette read from the theme's tokens through a probe (xterm understands neither
`oklch()` nor `var()`), and WebGL contexts rationed so a hidden pane falls back to the DOM renderer.
`CodeEditor` is Monaco on the dashboard's palette, its ground the container's (`.monaco-host`), with
one schema-aware completion provider per language.

## Canvases

Every canvas moves as a whiteboard (Excalidraw) does: two fingers on a trackpad slide it, a pinch
zooms about the fingers, a mouse drags it by open ground or its middle button, Ctrl or ⌘ with the
wheel zooms, Space held makes any drag a slide, and nothing on it changes size but by the zoom. Each
`<ReactFlow>` spreads `CANVAS_MOVE` and calls `useCanvasPinch`; `canvas-move.test.js` fails for one
that does neither. Arrow keys pan and move on the map, the ER diagram and the boards (T10, WP-44).

## Related

[Surfaces](surfaces.md) (`Panel` against `Pane`), [Tabs, chips, segments](tabs-chips-segments.md)
(a pane's views), [Feedback](feedback.md) (consoles), [Rail](rail.md) (a detail page's sibling
list), [`../foundations/layout-phone.md`](../foundations/layout-phone.md).
