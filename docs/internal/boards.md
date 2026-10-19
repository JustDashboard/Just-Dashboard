# Excalidraw drawings

The Workspace page is named after the editor it is: **Excalidraw**, at `/excalidraw`, with one drawing
open at `/excalidraw/{id}`. It was called Boards, at `/boards`, until 0.7.1; `next.config.ts` redirects
`/boards` and `/boards/{id}` to the new addresses so a bookmark still lands. The API, the table and the
audit names keep the `board` word (`/api/v1/boards/`, `boards`, `board.save`): renaming a stored
contract to match a page title would break every client for no reader's benefit. The editor embeds the
MIT-licensed `@excalidraw/excalidraw` package directly in the dashboard shell. Its fonts are copied
from the pinned package into `public/excalidraw/fonts` by `scripts/sync-excalidraw.mjs` before `dev`
and `build`, and served locally. The package and lockfile are managed with Bun.

## Data and API

The `boards` table in `internal/store/store.go` stores each board's name, Excalidraw scene JSON,
revision, and timestamps in the dashboard's SQLite database under `JD_DATA_DIR`; `board_files` holds the
images placed on each board, apart from its scene (below), and `board_library` the install's one shape
library. New tables are additive schema changes; existing installations acquire them when the store
opens. Backing up `JD_DATA_DIR` backs up the boards, their images and the library.

| Route | Capability | Does |
| --- | --- | --- |
| `GET /api/v1/boards/` | `read` | Names, timestamps and each drawing's preview (below), never a scene. |
| `GET /api/v1/boards/{id}` | `read` | The scene, and `fileIds`: the images stored for it. |
| `POST /api/v1/boards/` | `service.control` | Creates an empty drawing. |
| `PUT /api/v1/boards/{id}` | `service.control` | Saves name and scene against a revision, or merges (`merge: true`). |
| `DELETE /api/v1/boards/{id}` | `destructive` through `s.destructive` | Deletes the drawing and its images, and tells its live room. |
| `GET /api/v1/boards/{id}/files/{fileId}` | `read` | One image's bytes. |
| `PUT /api/v1/boards/{id}/files/{fileId}` | `service.control` | Stores one image, the body its bytes and `Content-Type` its type. |
| `GET /api/v1/boards/{id}/live` | `read` | The drawing's live room, a WebSocket. |
| `GET /api/v1/boards/library` | `read` | The library and its revision. |
| `PUT /api/v1/boards/library` | `service.control` | Saves the library against its revision; `409 library_conflict` if it moved. |
| `POST /api/v1/boards/library/fetch` | `service.control` | Downloads a shared library for the editor (below). |
| `POST /api/v1/boards/diagram` | `service.control` | Writes Mermaid for a description (below). |

A save carries the previous revision and updates only that revision, returning `409 board_conflict` if
another tab saved first. The editor then stops autosaving and asks the operator to reload, so it never
silently overwrites the other tab. A save that finds the board gone (`404 board_not_found`) stops
autosave the same way. A board scene must be a JSON object with `elements`, `appState`, and `files`;
one scene is capped at 16 MiB, and a larger scene or request body is refused with
`413 board_too_large` rather than as a malformed request. Every save reads and writes the stored board
in one write transaction (the store opens SQLite with `_txlock=immediate`), so two saves cannot both
read one revision.

The editor saves the scene in Excalidraw's file format with the deleted elements kept and the images
left out (`sceneBody` in `board-workspace.tsx`). A deletion is an element at a newer version with
`isDeleted` set; dropped from the save, as Excalidraw's own export drops it, a merge with an editor that
had not seen it yet put the element back. The server prunes a deleted element a day after it was
deleted (`updated`, Excalidraw's own `DELETED_ELEMENT_TIMEOUT`) on every save; one with no `updated` is
never pruned. Changes are detected from element versions and the appState fields a file keeps, compared
with the scene as Excalidraw first restored it, so opening, panning, zooming or selecting never saves or
bumps the revision. The whole scene is serialized only when it is saved. The name is saved trimmed; a blank name
is never sent and is restored to the saved name when the field loses focus. The UI debounces saves,
saves at once on Ctrl/Cmd+S (Excalidraw's own save-to-file shortcut is disabled), and shows pending,
saving, saved, failed, unnamed, conflict and deleted states. When changes cannot be saved, Back and
in-board links ask before leaving without saving instead of doing nothing.

A flush waits for every edit, including changes made while an earlier request was in flight.
`lib/board-save.ts` drains successive snapshots against their acknowledged revisions, and editor-owned
Back and resource-link navigation checks that the drawing is still clean before routing. A failed later save keeps the editor open with
the leave choice. Reloading or closing the browser tab also warns while changes remain unsaved,
including after a conflict or missing-drawing response; an explicit choice to leave without saving or
delete the drawing suppresses that warning.

Every dirty drawing also retains its name, scene, saved signature and original revision in this tab's
memory (`lib/editor-drafts.ts`). Sidebar, command-palette and browser history navigation can unmount
the editor without discarding that snapshot. Returning waits for the previous instance's in-flight
saves to settle before restoring the draft and its acknowledged revision; no best-effort unmount
write stands in for acknowledgement. A failed or conflicted save retains the draft, and the tab's
unload warning stays active even on another page. Explicit Leave without saving, successful deletion
or sign-out clears it. Buffers are never placed in Web Storage and do not survive a confirmed reload.
Account cleanup fences late save responses with a generation, so they cannot restore a previous
account's draft. Regression coverage includes failed sidebar/palette exits, Back and save completion
while the editor is unmounted in `boards-ui.spec.ts`.

`DELETE /boards/{id}` uses `s.destructive`: the `destructive` capability, tighter rate limit and
audit. The editor uses ordinary confirmation before deleting the drawing. A failed save does not block
deletion. All mutations
also pass the shared CSRF and audit middleware. The backend treats scene JSON as data; no scene field
invokes host commands or grants a capability.

### Images

An image placed on a drawing is stored once in `board_files` (`internal/api/boards_files.go`), not in
the scene: a scene carrying images as data URLs reached the 16 MiB limit after a few screenshots, and
every save sent all of them again. The editor sends each image as soon as it is placed
(`use-board-files.ts`), and before any save of a scene that names it, as its raw bytes with its type;
`fileId` is Excalidraw's own (a SHA-1 of the content, or a random id where `crypto.subtle` is missing),
so a second upload under one id is the same picture. A file is SVG, PNG, JPEG, GIF, WebP, BMP, ICO or
AVIF (`415 board_file_type`), at most 8 MiB (`413 board_file_too_large`), and a board holds at most
2000 files and 512 MiB (`413 board_files_too_large`). A file is served with its stored type,
`nosniff`, an immutable private cache and `Content-Security-Policy: sandbox; default-src 'none'`, so an
SVG opened at its own address runs nothing in the dashboard's origin. The editor reads the images a
scene shows and it does not hold through `fetch` (the page's `connect-src` allows nothing else) and
gives them to Excalidraw as data URLs.

A save moves any image still embedded in `scene.files` — every drawing saved before this, and the first
save of one opened since — into `board_files`, keeping in the scene only an entry it will not store (an
unknown type, or a board without room), so an old drawing never loses a picture. After a save the
server deletes the board's files that no element names, deleted elements included (an undo can bring
one back), once they are an hour old: the grace covers an image one editor sent just before another
editor's save that does not show it yet. A repeated upload restarts that hour. The save answers
`fileIds`, the files left, and the editor sends again any it shows that are missing.

### The live room

Everyone with a drawing open is in its live room (`internal/api/boards_live.go`,
`use-board-live.ts`): a relay that keeps nothing, so saving stays the audited PUT and a room that ends
loses no more than an unsaved tab. Joining needs `read`; the socket is upgraded through `wsx` with its
origin check, and the drawing must exist (`404 board_not_found` before the upgrade). A room holds 32
people. Frames are JSON text:

- the server says `hello` (who you are and who is there), `joined`, `left`, `error`, and `deleted`
  when the drawing is deleted, then closes the room;
- an editor with `service.control` sends `scene` (the elements that changed since its last frame, every
  50 ms at most; with `to`, to one peer) and `files` (images it has just stored); a reader sending
  either gets an `error` frame and is not relayed;
- anyone sends `pointer` (position, button, selection, at most every 40 ms while moving) and `idle`
  (`active`, `idle`, `away`, from the tab's visibility).

The server forwards each frame with `from` set, raw, without decoding an element array. A scene frame
may be 16 MiB and any other 64 KiB; a peer with 256 frames or 64 MiB queued is dropped rather than
holding the room back. Because the relay keeps no scene, a newcomer has the last save and is sent the
rest by everyone already there (`joined` → a scene frame `to` the newcomer).

The editor applies another editor's elements with Excalidraw's `reconcileElements` (the higher version
wins, the lower nonce on a tie) without an undo step, and reads any image they name that it lacks. A
drawing that had nothing unsaved before them still has nothing: they are their author's to save, or
every editor in the room would save every change. Other editors appear as Excalidraw's collaborators
(pointer, name, selection), and the header says who is there ("Live with Ana and Ion").

Once an editor has joined its room, its saves send `merge: true`: the server merges the incoming
elements with the stored ones by the same rule instead of refusing a revision another editor moved on,
orders them by Excalidraw's fractional `index`, and answers `remoteElements` — the merged elements the
saver does not hold as they now are — which the editor applies the same way, so it converges even if
it missed a frame. In a merge save the request's revision is not compared and every element must have
an `id`. An editor that never joined (the socket down, or an older tab) saves as before, against its
revision, and meets `409 board_conflict` if the room moved on.

## The preview and the list

A drawing's thumbnail is derived by the server from the scene on every write (`boardPreviewOf` in
`internal/api/boards_preview.go`), stored in the `boards.preview` column beside the scene by the same
`UPDATE`, and returned by the list, so the list never reads a scene — which can be megabytes of embedded
images — to draw it. The preview decodes only `elements` and `appState.viewBackgroundColor`, never
`files`. It holds the canvas colour; the count of live elements; up to 400 of them (the largest by
bounding area when there are more, back in paint order), each as its type, box, rotation, stroke and fill
colours, fill and stroke style, rounding, opacity, up to 64 points for a line, arrow or freehand stroke,
its text or frame name (200 characters), font and arrowheads; and the server cards on it, as each card's
`customData.justDashboard` kind and resource id (up to 24). A colour is kept only when it is a hex
literal, because it lands in an SVG attribute on other operators' screens. A scene whose elements cannot
be read into that shape still saves; it gets an empty preview, because the scene is the data and the
preview a convenience. The column arrived after boards had shipped (`store.addedColumns`); a board saved
before it has an empty preview, which the list derives from its scene once and stores without touching
its revision or `updated_at`, so the backfill never puts an open editor into a conflict.

`/excalidraw` (`components/boards/drawing-list.tsx`) is a reading page. It opens on Excalidraw's own
logo and a line of facts — drawings, shapes, linked server items, the last edit — in the identity shape
the Overview gives the machine (`HostIdentity`), where a row of stat tiles would have counted what the
cards below already show. Each drawing is a lit card (`SpotlightBorder`) whose picture is the drawing:
`components/boards/drawing-preview.tsx` draws the preview's shapes in the scene's own colours and its
words in Excalidraw's Virgil (served from the synced fonts as `--font-drawing`), inverted with the
editor's own dark-theme filter so the thumbnail is the canvas the operator left. On arrival each stroke
sketches itself in once and the fills follow (`motion`'s `pathLength`); reduced motion, or a drawing of
more than 120 shapes, is simply drawn. Under a card that holds server cards, the things it links are
drawn as themselves — this server as the dashboard's J, a project as its mark, a database as its engine —
from the deployment fleet and the database fleet, read only when some drawing links one. A card's menu
holds Delete for the `destructive` capability. A name filter appears from seven drawings. The empty page
shows a small drawing of what the editor is for here instead of a glyph.

**New drawing**, available with `service.control`, sits at the right end of the identity line beside
the page title and facts. The identity line wraps on narrow screens.

## Server cards and embeds

The insert panel reads the deployment list and database fleet through their existing authenticated
APIs. It can place a card for this host, an unarchived deployment project, or a saved database
connection, centred in the visible canvas at the current zoom. A card is an Excalidraw embed whose link
is the resource's dashboard page (`/`, `/deploy/{id}`, `/databases/{id}`), with the resource kind and id
in `customData`; Excalidraw draws it through `renderEmbeddable` as a live card (`live-card.tsx`): this
server's hostname, CPU, memory and uptime, a project's mark, state and address, or a database's engine,
whether it answers and its version, each read now rather than written in when it was placed. The reads
are made once for every card on the drawing and only while one is there (`/system/host` once,
`/system/metrics` every 10 s, the deployment fleet every 15 s, the database fleet every 30 s); a card
whose resource is gone says so. A card is opened by clicking into it and pressing Open, which leaves
through the editor's own unsaved-changes check. The shape under the card keeps the old cards' colours,
which is what the list's thumbnail and an exported image draw. Cards placed before 0.8 are rectangles
with the name and state they were placed with; they still open their pages. Cards navigate to existing
feature pages; they do not run a deployment or database mutation from a drawing.

Excalidraw's own embed list includes tweets, gists and Reddit, which it draws with the provider's
script inside the frame; this page's `script-src` admits nonce-bearing scripts alone, so those frames
were always blank. `validateEmbeddable` (`lib/board-embeds.ts`) allows the dashboard's own resource
links and YouTube, Vimeo and Figma over https, and `frame-src` in `src/proxy.ts` names their players
(`https://www.youtube.com`, `https://player.vimeo.com`, `https://www.figma.com`) from the same list.
Excalidraw sandboxes those frames; they are other origins and cannot reach the dashboard.

## Library

Excalidraw keeps its shape library in memory and empties it when the editor closes, so a pack added
to one drawing was gone by the next. The library is stored on the server instead, one per install,
since drawings have no owner (`use-board-library.ts`, `internal/api/boards_library.go`). The editor
reads it when it opens and saves it when it changes, against its revision; on `409 library_conflict`
it reads the stored library again, keeps both sets of items (`mergeLibraryItems`) and saves that. A
library is a JSON array of objects, at most 16 MiB. A reader without `service.control` sees the library
and saves nothing.

**Browse libraries** opens libraries.excalidraw.com, whose *Add to Excalidraw* sends the pack back to
the tab named in its link, as `#addLibrary=<url>&token=<editor id>` on the drawing's address. The editor
names its tab (`window.name`) so the pack lands in the editor that asked, reads the hand-off from the
address on load and on `hashchange`, removes it from the address, and asks the server to download the
pack: Excalidraw's own installer fetches it from the browser, which `connect-src 'self'` refuses.
`POST /boards/library/fetch` accepts only https URLs on `libraries.excalidraw.com` under
`/libraries/<author>/<name>.excalidrawlib`, or `raw.githubusercontent.com` under
`/excalidraw/excalidraw-libraries/`, the two Excalidraw itself accepts — no user info, port, query,
encoded or dot segments, the request rebuilt from the checked parts (`400 library_url_refused`). It
follows no redirect, gives up after 15 s, reads at most 10 MiB (`413 library_too_large`), and answers
only an `excalidrawlib` file (`502 library_fetch_failed` otherwise). It is audited as
`board.library.fetch` with the URL. When the token is not this editor's, Excalidraw asks before adding
the shapes. A `.excalidrawlib` file opened from the library menu needs no download and is saved the
same way.

## Text to diagram and the menu

Excalidraw's Mermaid dialog has always been in its More tools menu. **Describe a diagram** — the same
dialog's Text to diagram tab, behind the More tools menu and on the empty canvas — sends the
description to `POST /boards/diagram` (`internal/api/boards_diagram.go`), which asks the provider an
administrator set up for the SQL editor's assistant for one Mermaid diagram, strips any code fence and
answers it; the dialog previews it and inserts it as shapes. Only the description is sent: no drawing,
server or database data. It shares the assistant's limits (20 a minute and 4 at once for the install),
takes at most 90 s, and a description is 1–4000 characters. Without a provider it answers
`409 assistant_not_configured`, naming where an administrator sets one up. The dialog's preview runs
under the page's CSP.

The main menu is the dashboard's (`board-menus.tsx`): Save to this server first, then Excalidraw's
open, export, image, command palette, find, help, reset and background items, without its links to
GitHub, X and Discord; a reader sees no save, open or reset. An empty drawing shows Excalidraw's
welcome screen in this dashboard's terms, with Add a project, database or this server, Describe a
diagram, Open and Help.

## Tests

The server's rules are held by `handlers_boards_test.go` and `handlers_boards_{files,library,merge,
diagram,live}_test.go`: saves and conflicts, image storage, extraction and collection, the merge rule
and tombstones, the library and its fetch's URL table, text to diagram against a fake provider, and the
relay with real sockets. `lib/board-files.test.js`, `lib/board-embeds.test.js` and
`lib/board-live.test.js` hold the editor's pure rules, and `tests/browser/boards-ui.spec.ts` drives the
editor against a mock of the handlers, the live room included through a routed WebSocket.
