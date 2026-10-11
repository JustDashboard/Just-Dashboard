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
revision, and timestamps in the dashboard's SQLite database under `JD_DATA_DIR`. A new table is an
additive schema change; existing installations acquire it when the store opens. Backing up `JD_DATA_DIR`
backs up the boards and embedded image data too.

`GET /api/v1/boards/` lists names, timestamps and each drawing's preview (below), never a scene, and
`GET /api/v1/boards/{id}` reads the scene; both
require the authenticated `read` surface. `POST /boards/` and `PUT /boards/{id}` require
`service.control`. The save carries the previous revision and updates only that revision, returning
`409 board_conflict` if another tab saved first. The editor then stops autosaving and asks the operator
to reload, so it never silently overwrites the other tab. A save that finds the board gone
(`404 board_not_found`) stops autosave the same way. A board scene must be a JSON object with
`elements`, `appState`, and `files`; one scene is capped at 16 MiB, and a larger scene or request body
is refused with `413 board_too_large` rather than as a malformed request.

The editor saves the scene in Excalidraw's file format (`serializeAsJSON(..., "local")`), which is the
one that keeps `files`, so embedded images live inside the saved scene. The `"database"` format drops
`files` for a separate image store this server does not have; the backend refuses such a scene. Changes
are detected from element versions and the appState fields a file keeps, compared with the scene as
Excalidraw first restored it, so opening, panning, zooming or selecting never saves or bumps the
revision. The whole scene is serialized only when it is saved. The name is saved trimmed; a blank name
is never sent and is restored to the saved name when the field loses focus. The UI debounces saves,
saves at once on Ctrl/Cmd+S (Excalidraw's own save-to-file shortcut is disabled), and shows pending,
saving, saved, failed, unnamed, conflict and deleted states. When changes cannot be saved, Back and
in-board links ask before leaving without saving instead of doing nothing.

`DELETE /boards/{id}` uses `s.destructive`: the `destructive` capability, tighter rate limit and
audit. The editor uses ordinary confirmation before deleting the drawing. A failed save does not block
deletion. All mutations
also pass the shared CSRF and audit middleware. The backend treats scene JSON as data; no scene field
invokes host commands or grants a capability.

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

## Server cards

The insert panel reads the deployment list and database fleet through their existing authenticated
APIs. It can place a card for this host, an unarchived deployment project, or a saved database
connection, centred in the visible canvas at the current zoom. Each card stores the resource kind and
id in Excalidraw `customData`, and its link opens the resource's dashboard page. The visible label
records the name and status at insertion time; the destination page supplies current state when
opened. These cards navigate to existing feature pages; they do not run a deployment or database
mutation from a drawing.
