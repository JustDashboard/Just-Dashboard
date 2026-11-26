# Workbench

One surface held to the window, the sheet its frame: the panes it is made of, one pane at a time on a phone, the handles
between them, the page's keys and place, the terminal, the editor and the canvases.

**Members:** `Pane`, `PaneHeader`, `PaneFooter` (`panel.tsx`); `PaneBar`, `PaneBack`, `paneShown`
(`phone-panes.tsx`); `ResizeHandle` (`resize-handle.tsx`); `Workspace`, `WorkspaceHelp`,
`useWorkspaceFocus` (`components/workspace/`); `XtermPane` (`xterm-pane.tsx`); `CodeEditor`
(`code-editor.tsx`); `CANVAS_MOVE`, `zoomKey`, `zoomAbout`, `useCanvasPinch` (`canvas-move.ts`);
`useIsPhone`, `useMediaQuery` (`hooks/use-mobile.ts`); `WorkbenchHeader` (`workbench-header.tsx`);
`CanvasZoom` (`canvas-zoom.tsx`) and `useFullscreen` (`hooks/use-fullscreen.ts`). The page frame
is `Page fill="workspace"` (`components/page.tsx`), with no inset at any width: the working surface
runs to the sheet's edges and draws no border of its own, since the sheet is its frame
([`../foundations/surfaces.md`](../foundations/surfaces.md)). `WORKBENCH_WAITING` is its loading
state's class list.

**Register.** A workbench is a page the reader works in for minutes (the terminal, Files, Git, a
database's Data and Query, the boards, the map), not one they read. Its recipe is
[`../recipes/workbench.md`](../recipes/workbench.md); the page frame is `Page fill`, whose content
is the viewport.

## Which one

| You need | Use | Not |
| --- | --- | --- |
| A working region of the page: a rail, a tree, a console | `Pane` | a `Panel` (that is a block of content) |
| The strip a workbench opens on: back, what is open, its verbs | `WorkbenchHeader` | a `PaneHeader` of your own, a text "← Files", a visible `h1` |
| Several panes on a phone | `paneShown` on each, `PaneBar` at the foot, `PaneBack` in an opened page | stacked short boxes inside a scrolling page |
| A side column the reader can widen | `ResizeHandle` beside it | a fixed width |
| A list page's keys (rows, search, refresh, help) and the place it returns to | `Workspace` | key handlers on `window` |
| A shell | `XtermPane` | anything else; it owns the socket and the PTY's size |
| Text or code to edit | `CodeEditor` | a `Textarea` for more than a few lines |
| A canvas (React Flow) | spread `CANVAS_MOVE` and call `useCanvasPinch` | the library's default gestures |

## Pane

**Purpose.** The same frame as a `Panel` for a working region rather than a block of content; its
header and footer strips keep a faint tint, and it does not lift. **Not** a block on a reading page.

## WorkbenchHeader

**Purpose.** The strip a workbench that opens one thing starts on: the board, Git's repository and
the editor. Back, what is open, how it is, and what can be done to it, read left to right. They
drew this three ways (an icon back, the words "← Files", a name as a field, two lines or a visible
heading on a second row), so moving between workbenches read as moving between products. A remote
screen's session bar and the terminal's window strip are other controls and keep their own; Files,
the terminal and the map keep their toolbars and carry their mark in them (the terminal's rail head,
the map's rail).

**Props.** `back: { label, hint?, onClick | href }`, always an arrow (`PaneBack`'s look, at every
width), named by `label` ("Back to repositories", "Back to Excalidraw", "Back to Files") with
`hint` as its tooltip. `heading` is the page's `h1`, said and not drawn, and the tab's name: it is
drawn beside the strip, so a phone that hides the strip while a pane is read keeps the page's
name. `mark` (the product or the thing as itself), `name` (words, or the field that renames it),
`state` (beside the name where the strip has room, under it where it does not), `subline` (the
remote, the folder), `tools` (the workbench's own controls; a row of their own when narrow),
`command` and `menu` (the one command and the `⋯`, at the end). The strip measures itself, not the
window: narrower than one row it folds on purpose, the back, the name and the verbs over the tools.

**Accessibility.** One `h1` per page, here; the back is a named button with a tooltip.

**Example.** `components/git/repo-workspace.tsx`: the repository, its remote as the subline, HEAD
and the sync verbs as tools, the `⋯` and the GitHub account as the menu.

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
break up it measures nothing. A settings save bar on the same page stands above it, not on it: a bar
of a higher order (`useStackOccupant(el, "bar", { order: 1 })`) is lifted by the bars under it. `PaneBack` in the head of an opened page. The page
keeps its own state for which pane is shown, and holds its frame to the window below the break
(`Page fill`) so the pane has a height to be the whole of.

**Accessibility.** The bar is a `nav` named by `label` holding a button for each stop, the shown
one `aria-current="page"` and its count as a description; `PaneBack` is named "Back to the list"
unless told otherwise. A row that opens a page in its place, and `PaneBack` that closes it, each
hide the control that has the keyboard, so the page moves the keyboard with the switch: onto the
opened page's heading (`tabIndex={-1}`), and back onto the row it came from. A backup job's runs
are the reference (`components/backups/job-detail.tsx`, spec `backups-ui.spec.ts`, *on a phone
the keyboard goes with a run*).

**Example.** The pages on it are listed in
[`../patterns/keyboard-and-place.md`](../patterns/keyboard-and-place.md): the Git workspace, the
assistant, Logs, the network tools, Files, the editor, a project's logs, the terminal, a Remote
Desktop session and the databases map; since 0.9.0 also the server map (Systems, Map, Inspector),
a deployment run (Logs, Steps, Step) and a database's Logs (its readings). A backup job shows its
runs as rows, and a run picked in their place with `PaneBack`. The assistant's page puts Settings
on its bar, so one strip picks what is shown.

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
own keys. `WorkspaceHelp` is the `?` cap. On a page with a way back it ends the back line
(`PageContext actions`, or `FlowHeader actions` on a flow); on a page with none it ends the
identity line, after the `⋯`, and never has a row to itself
([`page-header.md`](page-header.md#the-identity-lines-rules)). It is not drawn on a coarse pointer. Its sheet lists the keys
with `ShortcutList` and ends with the single-key switch (`SingleKeySwitch`). The rules are in [`../patterns/keyboard-and-place.md`](../patterns/keyboard-and-place.md).

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
that does neither. Arrow keys slide the map, the ER diagram and the boards, and move what Enter picked (T10; the
row in [`../patterns/keyboard-and-place.md`](../patterns/keyboard-and-place.md)).

## Related

[Surfaces](surfaces.md) (`Panel` against `Pane`), [Tabs, chips, segments](tabs-chips-segments.md)
(a pane's views), [Feedback](feedback.md) (consoles), [Rail](rail.md) (a detail page's sibling
list), [`../foundations/layout-phone.md`](../foundations/layout-phone.md).
