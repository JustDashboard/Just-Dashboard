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
to reload, so it never silently overwrites the other tab. A board scene must be a JSON object with
`elements`, `appState`, and `files`; one scene is capped at 16 MiB. Excalidraw image files live inside
the saved scene. The UI debounces saves and shows pending, saving, saved, failed, and conflict states.

`DELETE /boards/{id}` uses `s.destructive`: the `destructive` capability, tighter rate limit and audit.
It requires the board name as a server-checked `X-Confirm` phrase because the drawing cannot be recovered
after deletion. All mutations also pass the shared CSRF and audit middleware. The backend treats scene
JSON as data; no scene field invokes host commands or grants a capability.

## Server cards

The insert panel reads the deployment list and database fleet through their existing authenticated
APIs. It can place a card for this host, an unarchived deployment project, or a saved database
connection. Each card stores the resource kind and id in Excalidraw `customData`, and its link opens
the resource's dashboard page. The visible label records the name and status at insertion time; the
destination page supplies current state when opened. These cards navigate to existing feature pages;
they do not run a deployment or database mutation from a drawing.
