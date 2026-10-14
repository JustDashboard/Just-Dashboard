# Stickyboard

Stickyboard is a Workspace page at `/stickyboard`, with one board at `/stickyboard/{id}`: an endless
wall of sticky notes that can be written on, moved, resized, coloured, grouped into sections and linked
to each other, zoomed out to see everything or in to read one. It sits beside Boards rather than inside
it. Boards is a freehand drawing (Excalidraw) whose scene the server stores as an opaque file; a
stickyboard is structured — notes, links between notes by id — and the server validates every field
of it, so that a link can never be left pointing at a note that is gone.

## Data and API

The `sticky_boards` table in `internal/store/store.go` holds each board's name, its document, the
number of sticky notes and links in it, a thumbnail preview, a revision and timestamps, in the
dashboard's SQLite database under `JD_DATA_DIR`. A new table is an additive schema change; existing
installations acquire it when the store opens. Backing up `JD_DATA_DIR` backs up every board.

A document is version 1: a `background` (`dots`, `lines` or `none`), `notes` in paint order and
`links`. A note has an `id`, a `kind` (`sticky`, `text` or `section`), a rectangle (`x`, `y`, `w`,
`h` in board units), its Markdown `text`, a `color` from one palette of nine (`yellow`, `orange`,
`red`, `pink`, `violet`, `blue`, `teal`, `green`, `graphite`), a `style` (`paper` or `tint`), a
`size` (`s`–`xl`), an `align`, a `font` (`sans` or `mono`) and `locked`. A link has an `id`, `from`
and `to` note ids, a `label`, and its own `color`, `style` (`solid`, `dashed`), `arrow` (`none`,
`end`, `both`) and `path` (`curved`, `straight`, `elbow`).

`handlers_stickyboards.go` decodes a document with unknown fields refused and checks it before it is
stored: at most 5,000 notes and 10,000 links; ids of letters, digits, `_` and `-`, unique within notes
and within links; coordinates within ten million units and sides between 24 and 20,000; text up to
20,000 characters and labels up to 200; every enumerated field one the editor can draw; and every link
joining two different notes that are on the board. An empty enumerated field takes the editor's
default, so the stored and served document is always complete. The whole request is capped at 4 MiB
and a larger one is refused as `413 stickyboard_too_large`, not as malformed JSON.

| Route | Capability | Does |
| --- | --- | --- |
| `GET /api/v1/stickyboards/` | `read` | Every board's summary: name, revision, counts, preview, timestamps. Never a document. |
| `GET /api/v1/stickyboards/{id}` | `read` | One board with its document. |
| `POST /api/v1/stickyboards/` | `service.control` | Creates a board from a name and, optionally, a starting document (a template), checked as a save is. |
| `PUT /api/v1/stickyboards/{id}` | `service.control` | Saves a document against the revision it was read at; `409 stickyboard_conflict` if another tab saved first. |
| `PATCH /api/v1/stickyboards/{id}` | `service.control` | Renames a board. The name is not part of the document, so a rename leaves the revision alone and never puts a tab editing the board into a conflict. |
| `POST /api/v1/stickyboards/{id}/duplicate` | `service.control` | Copies a board's document under a new name, at revision 1. |
| `DELETE /api/v1/stickyboards/{id}` | `destructive` through `s.destructive` | Deletes a board: the destructive rate limit and audit, and ordinary confirmation in the UI. |

Every mutation passes the shared CSRF and audit middleware and names itself in the audit log
(`stickyboard.create`, `.save`, `.rename`, `.duplicate`, `.delete`) with the board's name as the
target. A missing board is `404 stickyboard_not_found`. The document is data: no field of it invokes a
host command or grants a capability.

The counts and the preview are derived from the document on every write. The preview is the board's
shape for the list's thumbnail — each note's rectangle, kind, colour, style and words, and the links
between them as index pairs — capped at 300 notes, the largest kept and returned to paint order, so the
list never reads a whole board to draw it. A note's words are its Markdown reduced to plain text by
`stickyPlainText`: at most four lines and 120 characters, with headings, list and task markers, quotes,
emphasis, code and link syntax taken out. A board saved before the words were kept has none in its
preview until its next save.

## The list

`/stickyboard` is a reading page. It opens on Stickyboard's own mark — three notes fanned out in the
board's paper colours (`StickyboardMark`), since the feature is this dashboard's own and has no logo to
borrow — and a line of facts (boards, sticky notes, links, the last edit and on which board) in the
identity shape the Overview gives the machine, where four stat tiles used to stand. Under it are the
boards as cards drawn as the boards themselves: `BoardPreview` draws the server's preview on the
editor's dot grid in the notes' own colours, with each note's words written on it wherever they are
legible at that size, and the notes land on the grid one after another on arrival (reduced motion, or a
board of more than 60 notes, simply draws them). A card names the colours its notes are written in, most
used first. Every card is a board to enter, so it carries the lit edge of a thing you take (§16) and a
menu with Rename, Duplicate and Delete by capability. A name filter appears from seven boards. Under the
boards, for the `service.control` capability, **Start from** shows the templates — Blank, Kanban,
Retrospective, Mind map — as `ChoiceCard`s, each drawn as the board it will make; taking one opens New
board on it. New board asks for a name and a starting point, and the board is created from that document
in one request.

**New board**, available with `service.control`, sits at the right end of the identity line beside
the page title and facts. The identity line wraps on narrow screens.

## The board

The canvas is React Flow (`@xyflow/react`, already the database diagram's), controlled by the document:
nodes and edges are derived from it, and a node is rebuilt only when its note, selection, edit or find
state changed, so dragging one note across a large board re-renders one note. Each node declares its
size and its four link handles up front, because a note off screen is never drawn (`onlyRenderVisibleElements`)
and so never measured, and it still has to count towards the first fit and the minimap and to be the
end of a link.

- **Moving around.** Scroll pans, Ctrl/⌘+scroll or a pinch zooms (5%–400%), Space or the middle button
  pans while held, and the pointer/hand tools decide what a drag on bare board does — a selection box or
  a pan. Touch starts on the hand. The zoom bar resets to 100% and fits the board; the minimap pans and
  zooms. Where the reader was is remembered per board in this browser (`useViewState`), and a remembered
  place that shows none of the board — a desktop's, opened on a phone — is replaced by a fit.
- **Adding.** A double-click on bare board writes a sticky note there; N, T and S add a sticky, text or
  a section at the pointer, and S with notes selected draws a section around them. The tool rail arms
  a kind for the next press on the board, and holds the colour new stickies take.
- **Notes.** A sticky is paper in its colour, or tinted onto the board; text is ink with no paper; a
  section is a faint wash of its colour that the notes stand on. A section's body lets the pointer
  through, so a selection or a double-click starts inside it as on bare board; its title, which stands on
  its top edge and grows when zoomed out until it reads at twelve pixels, is what takes it, and moving it
  carries every unlocked note wholly inside. Text is Markdown, drawn by the pull-request reader
  (`lib/markdown.ts`, which produces no markup): headings, lists, quotes, code, tables, links, and tasks
  whose boxes tick the text and whose count shows in the note's corner. Editing grows a note to fit
  what is typed. Text left empty is removed when editing ends; a sticky left empty stays.
- **Links.** A ring off each side of the note under the pointer or selected is dragged onto another note
  to link the two, with that note outlined before the drop; dropped on bare board it makes a new note
  there, already linked. A second drag between the same two notes selects the link they have. A link is
  drawn edge to edge between the notes' rectangles (`linkGeometry`), so it follows them to whichever
  sides face each other, with its arrowheads in its own colour.
- **Formatting.** The bar over a selection holds colour, paper or tint, size, alignment, typeface, lock,
  and a menu for order, duplicate, copy and delete; a link's bar holds colour, line, shape, arrows,
  label, direction and delete. It is drawn in screen space and slid along the board's edge, clear of the
  tool rail, rather than cut off. The keyboard does the same: 1–9 colour, arrows nudge, `]`/`[` order,
  Ctrl/⌘+D duplicates, Delete removes (a note's links with it), Enter edits; `?` lists them all.
- **Clipboard.** Copy and cut put the selected notes and the links among them on the clipboard as
  marked JSON, so a paste here or on another board recreates them at the pointer with new ids; plain
  text pasted on a board becomes a sticky.
- **History and find.** Every change is a new document, and undo/redo walk up to 200 of them; a gesture
  — a drag, a resize, an editing session — is one step, and a run of nudges merges into one. Ctrl/⌘+F
  outlines every note holding the words and brings the current one to the middle.

## Saving

`components/stickyboard/use-board.ts` saves 700 ms after the last change, at once on Ctrl/⌘+S and
before leaving, and compares the document with the object last saved, so undoing back to it leaves
nothing to save and opening, panning, zooming or selecting never writes a revision. The status says
saved, unsaved, saving, failed, conflict or deleted. A conflict or a deleted board stops saving and
editing and says so, with Reload on a conflict, rather than overwriting another tab; Back with changes
the server does not have asks before leaving without them. A reader without `service.control` sees the
board read-only: no tools, no format bar, no editing, and nothing that would save.

Each flush drains every snapshot changed during the requests already in flight (`lib/board-save.ts`,
also used by Excalidraw). Navigation checks that the latest document is clean before routing, and a
failed later request retains the editor and its leave choice. The browser's reload/close warning
remains active for a dirty document after a conflict or missing-board response.

Duplicate first requires a successful flush: a failed or conflicted save leaves the operator with the
original document instead of copying an older server document and navigating away. If edits arrive
while the duplicate request itself is pending, the copy is created but the editor stays with those
newer edits. The name field saves on blur or Enter; Escape restores the saved name and suppresses the
blur's rename request.

## Design

The nine colours are tokens on `:root` (`--note-*`), labels the operator applied, like a folder's
colour, at a lightness chosen so a board of paper notes is paper rather than a wall of lamps;
`[data-note-color]`, `[data-note-style]` and `[data-note-kind]` in `globals.css` mix every ground, ink
and edge a note draws from them. Note type sizes are board units the canvas scales, not the interface's
ladder. Selection is drawn in the brand and a find result in the signal (§3). A note being carried is the
one thing on the board above it and the only one that casts a shadow, and the format bar, which floats
over the board, takes a popover's ground and shadow (§2). The canvas controls sit on the card's ground
with a border, as the database diagram's do. React Flow's own node wrappers are styled from
`.stickyboard-canvas` in `globals.css`.

`tests/browser/stickyboard-ui.spec.ts` drives the list, templates, editing, links, the format bar,
conflicts, rename cancellation, failed duplication, saves made during navigation and the read-only
board against a mock of the handler's rules; the Go rules are held by
`handlers_stickyboards_test.go` and the pure model by `lib/stickyboard.test.js`.
