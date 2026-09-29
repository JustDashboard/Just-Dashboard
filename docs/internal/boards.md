# Boards

Boards are a Workspace page at `/boards`, with one editor at `/boards/{id}`. The editor embeds the
MIT-licensed `@excalidraw/excalidraw` package directly in the dashboard shell. Its fonts are copied
from the pinned package into `public/excalidraw/fonts` by `scripts/sync-excalidraw.mjs` before `dev`
and `build`, and served locally. The package and lockfile are managed with Bun.

## Data and API

The `boards` table in `internal/store/store.go` stores each board's name, Excalidraw scene JSON,
revision, and timestamps in the dashboard's SQLite database under `JD_DATA_DIR`. A new table is an
additive schema change; existing installations acquire it when the store opens. Backing up `JD_DATA_DIR`
backs up the boards and embedded image data too.

`GET /api/v1/boards/` lists names and timestamps, and `GET /api/v1/boards/{id}` reads the scene; both
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

## Server cards

The insert panel reads the deployment list and database fleet through their existing authenticated
APIs. It can place a card for this host, an unarchived deployment project, or a saved database
connection, centred in the visible canvas at the current zoom. Each card stores the resource kind and
id in Excalidraw `customData`, and its link opens the resource's dashboard page. The visible label
records the name and status at insertion time; the destination page supplies current state when
opened. These cards navigate to existing feature pages; they do not run a deployment or database
mutation from a drawing.
