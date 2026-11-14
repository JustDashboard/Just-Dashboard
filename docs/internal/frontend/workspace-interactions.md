# Workspace interactions

`components/workspace/` supplies page-owned keyboard commands and place memory to Processes,
Services, Git, Logs, the Docker overview and containers, Docker images, Docker stacks, Docker volumes, Docker networks, Packages, Backups, deployment setup, Audit and the Security
Connections, Logins and Firewall lists. Files retains its existing interaction controller.

A workspace registers its read commands in the command palette's **This page** group. Ctrl/Cmd+F
focuses the page's search field where one exists. F5 and Ctrl/Cmd+R refresh the current reads;
Shift+Refresh retains the browser's reload behavior. `?` and the named shortcuts button open help.
Arrow keys, Home/End and name typing move between marked visible rows; Enter uses the row's
existing inspect or navigation action. Read-only rows advertise movement rather than an open action.
Inputs, Monaco, xterm, contenteditable elements and open dialogs, menus or listboxes own their keys.
An Escape in a page search clears its filter where supported and returns to the list.
Audit keeps its shortcuts and new-arrival controls in the trail toolbar, preserving the reading
page's opening on its figures rather than adding a context header.

| Surface | Behavior |
| --- | --- |
| Processes | Pause stops scheduled inventory reads; changing the question (a search, a chip, a workload) and explicit refresh still read it. Ranking and row-count changes retain the live table, its scroll and its last readings while fetching; its footer describes the ranking that actually answered. Pointer inspection, row focus and open menus or details hold the returned order across Live, PM2, Services and timers while readings update. Exited identities disappear and new ones follow the held rows. Leaving inspection releases the ranking; explicit Live refresh or a changed filter/focus choice also releases it. Live identities include PID and creation time; signals retain the existing confirmation and identity checks. Escape clears the search, then the workload, then the held order. A row new since the last read rises once; a changed filter is a new list, not arrivals. Cron row identities exclude disabled state and line position, so enable/disable updates the same row and toggle without losing focus. |
| PM2 | The application table has the shared row navigation and place memory; Enter opens a row's sheet. Escape clears the search, then the application chosen on the band, then the state chip. A row that arrives with a poll rises into place, and a restart count that moves rises with it. |
| Services | The unit list keeps reading every five seconds; a row is a unit by name, so focus and place survive a read that reorders it. Escape clears the search, then the startup chip, then the state chip. The palette's **This page** group toggles the failed units. A new unit rises once; an existing unit updates its state in place without replaying its arrival. A changed filter is a new list, not arrivals. |
| Docker networks | Both tables have the shared row navigation; Enter on a network row opens its page (`/docker/networks/[name]`; `?network=<id>`, the address of the sheet it replaced, redirects there) and on a container row its page. Escape clears the search, then the network the containers table is narrowed to, then the containers chip, then the owner chip. The palette's **This page** group toggles the unused networks. A new network or container rises once. A network's page keeps its view in `?tab=` (replaced, not pushed, so Back leaves the network), and Alt+↑ / Alt+↓ walk the rail's networks with the view carried along; the rail's search and owner chip are the table's. |
| Git | Commit messages are session drafts per checkout and clear after a successful commit. Alt+Up/Down and the adjacent-file buttons walk changed files. Escape closes the preview and returns focus to its file. The repository list has the shared row navigation and place memory. Find targets History's existing search. |
| Logs | Source and mode changes, run handoffs and settled filters become history entries. Typing settles after 400ms; submission commits immediately. Back/Forward rebuild the question from the URL. Consoles remember the opened and active record and following state in memory. Opaque content/position identities match fresh responses, distinguish repeated records and retain no raw log text in Web Storage. F3/Shift+F3, n/N and visible match buttons cycle search hits; opening or walking a line stops following. Refresh reconnects or searches in place. |
| Docker overview | The container table has the shared row navigation and place memory; Enter opens a row's container, and the table's order is remembered for that page's adjacent-container buttons as the containers page's is. Escape clears the search, then the project chip, then the state chip. The palette's **This page** group toggles the failing containers. A container that appears rises once; one that changes state updates in place. A changed filter is a new list, not arrivals. |
| Docker containers | Returning from a detail restores the list's focused container and scroll position. Name typing selects a container. Adjacent-container buttons and Alt+Up/Down use the first 500 rows of the last visible inventory, retained for the tab, and preserve the detail tab. A direct link without a remembered inventory disables unavailable adjacent navigation. |
| Docker volumes | A row opens the volume's page (`/docker/volumes/<name>`); `?volume=` redirects to it. Rows support keyboard movement and name typing; Enter opens the page. Escape clears the holder chosen on the band, then the mount standing, then the chip, then the search. A volume new since the last read rises once; container state changes update the rows in place from the containers socket. On a volume's page the table is a rail from `xl` with the table's search, filters and order, kept for the tab; Alt+Up/Down and the adjacent-volume buttons walk it and preserve the open view, which is in the address as `?tab=` (replaced, not pushed). A direct link to a volume the rail's filter does not hold draws it first, above the rest. |
| Database account | `/databases/<id>/access/<account>` has `?tab=` for its view, replaced rather than pushed. The adjacent-account buttons and Alt+Up/Down walk the Access list in the filter and search it was left in (kept for the tab), and preserve the view. The list's chip is in the list's own address and is carried by the way back. While grants are staged or a setting is edited, a link out, another account and a reload ask first; the browser's Back cannot be asked. |
| Docker volumes | `?volume=` is shareable and uses the query-selection history contract: Back closes the sheet before leaving the page. Rows support keyboard movement and name typing; Enter opens the sheet. Escape clears the holder chosen on the band, then the mount standing, then the chip, then the search. A volume new since the last read rises once; container state changes update the rows in place from the containers socket. |
| Docker stacks | A stack and each of its containers are rows; name typing selects either. The stack list is read again the moment the containers socket reports a container whose state or health changed, so a stack row updates in place rather than up to fifteen seconds later. Escape clears the search, then the stack the band narrowed to, then the state chip. The palette's **This page** group toggles the stacks that need attention. Folded stacks stay folded for the session. A stack or container new since the last read rises once. |
| Docker images | The image table has the shared row navigation and place memory; a row's name is a link to the image's page (`/docker/images/<id>`, the id's 64 hex digits) and Enter on a row opens it. `?image=` from an older link lands on that page. Escape in the search clears the registry answer chosen on the band or the verdict first, then the search. Refresh reads the images, the disk figures and the containers; it does not ask the registries again, which Check now does. A pulled image rises once into the table. On an image's page `?tab=layers\|containers\|history\|configuration` names the open view (Layers is not said) and is replaced, not pushed; the rail from 1280px is the table's list with its search, chips, registry filter and order, read from the same session; Alt+Up/Down and the adjacent-image buttons walk it and keep the view. A reference in the address (`ghcr.io%2Facme%2Fapi%3Amain`, one escaped segment) is resolved and replaced by the image's own address. |
| Packages | `?package=` is shareable and uses the existing query-selection history contract: Back closes the sheet before leaving the page. Closing a sheet returns to the originating row. Installed/update rows and Add software results support keyboard movement; Enter inspects a result. Refresh reads inventory and updates; it does not refresh the package-manager index or start an install. The session-scoped software-group filter opens Installed, clears search and selects Everything to include dependencies. Escape clears that group before the search. The readout follows dependencies through `?package=` and Back; commands copy only, while service and file choices link to their existing inspectors. |
| Docker images | `?image=` is shareable and uses the query-selection history contract: Back closes the sheet before leaving the page. The image table has the shared row navigation and place memory; Enter opens a row's sheet. Escape in the search clears the registry answer chosen on the band or the verdict first, then the search. Refresh reads the images, the disk figures and the containers; it does not ask the registries again, which Check now does. A pulled image rises once into the table. |
| Packages | A package is a page (`/packages/[name]`), so Back returns to the list and its place; `?package=`, which opened the sheet, redirects there. Installed/update rows and cards and Add software results support keyboard movement; Enter opens the package's page and never starts an install. On the page Alt+Up/Down and the two arrows walk the list as it was left (its search, scope, software group and order), keeping the view on screen; a dependency opens on the same view and Back returns from it. Refresh reads inventory and updates on the list, and the package and the inventory on its page; it does not refresh the package-manager index or start an install. The session-scoped software-group filter opens Installed, clears search and selects Everything to include dependencies. Escape clears that group before the search. Commands copy only, while service and file choices link to their existing pages. |
| Backups | `?run=` selects a shareable, remembered run on the job page. Each archive remembers its filter, opened state and selected paths for the tab. Shift+click and Shift+Up/Down extend a range; Ctrl/Cmd+A selects visible entries. Escape clears the archive selection before closing the run. Restore retains its existing review and confirmation. |
| Deployment setup | `?step=source\|project\|runtime\|variables\|review` lets browser history traverse the source and configure steps. Back to source keeps the inspected flow; explicitly changing the source retains the existing discard behavior. Step visits restore field focus and scroll without storing field values. Existing flow persistence and memory-only unsaved secrets remain authoritative. |
| Audit | Username, action, failed-only and offset are URL questions. Settled edits create one history entry; pagination commits immediately. Polls update current rows in place, while new entries wait behind a counted Show button. |
| Security lists | Connections (`q`, `scope`), Logins (`q`, `failed`) and Firewall (`q`) expose URL filters and restore them on Back. Connections and login history hold newly arrived identities until Show is pressed; existing readings update and removed rows disappear. Login row/place memory stays in RAM because a failed username can contain a mistyped password. Firewall row numbers remain readings rather than mutation identities. |
| Metrics | Clicking a chart or pressing Enter/Space pins the shared instant. Left/Right, Home/End and the host page's adjacent-sample buttons move it. Each chart shows pinned values and ignores pointer movement until released with Escape or Release moment. The host page links to the journal's History view for the minute on either side, from a strip that appears only while a moment is pinned and holds at the top of the scroll. Drag zoom and the existing live pause remain available. |

Place memory stores row identifiers, field indices and scroll offsets, never field values or server
row payloads. It is session-scoped except for the Logs and Login workspaces, which use RAM.
The default workspace path is captured on arrival so an inactive cached route cannot adopt another
page's URL as its storage key. Deployment steps and log questions supply their own keys; log query
parameters are sorted so rewriting the same question cannot reset its place. Explicit refresh rereads
specialized log views in their own pane, while live/history consoles keep their place memory. Restoration
waits for visible rows and scroll regions, retries cached-route reveals and late hydration replacements,
and stops after two seconds or any pointer, keyboard or wheel interaction. It runs after Next's route
focus, and a SidePanel inside a workspace returns
focus through the workspace's remembered row when its original trigger is no longer mounted.
Cleanup saves the last observed scroll offsets rather than rereading DOM that a route or step commit
may already have replaced. A long container inventory verifies both scroll and row focus on Back.

`useFilterHistory` owns only its named URL parameters and leaves other handoffs intact. Native
history writes pass application state rather than copying Next's private history markers, so
`useSearchParams` stays synchronized. Files also preserves its own folder marker when Next rewrites
the same entry, so a router update cannot reset the Back/Forward trail; a different address or
an explicitly supplied marker keeps its own state. `useHeldList` keeps only identities in React state;
it never stores audit, connection or login responses. The shared crosshair remains an external store;
only its overlays subscribe to moving instants, while charts subscribe to pin status.

Verification lives in the existing page browser specs (tests prefixed `workspace:`) and pure tests
for row navigation, held ordering, range selection, log identity and pinning. The Files history
spec waits for each destination address before the next traversal, because retained listing rows
can still describe the previous folder while native history is moving. Run
`scripts/test-changed.sh <base>` against a production frontend built from the task worktree;
`JD_BROWSER_BASE_URL` selects that worktree's loopback server.

## Drafts and navigation failures

Editor-owned board Back and resource-link navigation drains every dirty snapshot through
acknowledgement; further edits during a save must also be acknowledged before those controls leave. Failed save/duplicate attempts retain the workspace and
its leave warning. Drawing documents also retain dirty snapshots and their original
revision in RAM across every SPA exit, including sidebar, palette and native history navigation.
Returning waits for the previous editor's outstanding save to settle before restoring the retained
snapshot; later acknowledgements update the saved revision without clearing newer edits. Raw proxy
editors retain each dirty buffer and its original content revision per engine/path, so Back closing
a query-selected editor and Forward reopening it preserves the draft. The shared unload guard
stays active even while these editors are closed or their pages are unmounted. Explicit discard,
successful save/deletion and `jd-working-state-cleared` clear retained contents. Cleanup increments
a generation so an old request cannot recreate or remove a new account's draft. These buffers are
never persisted in Web Storage and a confirmed reload discards them.
Below `lg` the Git workspace shows one pane at a time, the full height of the screen. A bottom bar picks
Changes, History, Branches, GitHub or Files; pressing a row opens its preview as a full page with the strip
and the bar hidden and a back arrow in `PreviewHeader`. `pick` closes an open preview there, so anything
that asks for a list shows it. The branch graph has no tab strip to live in on a phone and is also in the
More menu. The hardware back button still leaves the page rather than closing a preview.

That layout is `components/phone-panes.tsx`, and a page with several panels uses its parts rather than
stacking them: `paneShown(shown, until)` on each pane (hidden below the break while it is not the one
picked, untouched from the break up), `PaneBar` at the foot of the frame (one 56px stop for each pane, a
count on a glyph's shoulder, clear of the home indicator, not drawn from the break up) and `PaneBack` in the
head of a page a row opened. The page keeps its own state for which pane is shown, and the frame is held
to the window below the break so the pane has a height to be the whole of. Six pages are on it:

to the window below the break so the pane has a height to be the whole of. Five pages are on it:

| Page | Break | Stops | Opens on |
| --- | --- | --- | --- |
| Git workspace | `lg` | Changes, History, Branches, GitHub, Files | Changes |
| Assistant chat | `lg` | Chat, Chats, What it did | Chat |
| `/logs` | `md` | Log, Sources | the log |
| `/network/tools` | `md` | Tool, Every tool | the tool, the one a link asked for |
| `/files` | `lg` | Places, Folder, Details | the folder |
| `/files/editor` | `lg` | Files, Editor | the editor |
| `/deploy/[id]/logs` | `md` | Requests, Insights, Output, Builds, Events | the view the address or the project asks for |

Logs and the tools run to the screen's edges below `md` and open on the thing the page is for; picking a
source or a tool returns to it. Logs keeps its tablet layout from `md` to `lg` (the rail stacked over the
lines, the page scrolling), which was a choice made for a request record's chart. *Hide the sources* is a
wider screen's toggle and is not drawn on a phone, where the bar reaches the sources whether or not they
were hidden at a desk. `tests/browser/phone-panes.spec.ts` holds both at 390×844 (the working control in
view without scrolling, the bar at the foot, every row reachable), a tablet's stack and a desk's columns;
`git-panel.spec.ts` and `database-assistant.spec.ts` hold the two that came first. A deployment's Logs
is the pane under the project's header, which the shell holds to the window below `md`; its readings and
alerts are not a stop but a page behind the pane's first line, with `PaneBack` and the bar out of its way
(`deploy-logs-page.spec.ts`). The database
workbenches and the container, process and stack detail pages are not on it yet.

Files keeps its pane against the folder it was picked in, so arriving at a folder by any route (a place,
a crumb, Back, a search result) shows the listing without an effect to put it there, and a place that is
already open is still shown when picked. Its strip is one row below `lg` (back, the path scrolled to the
open folder, find, New) and the rest of the wide strip is a `MoreMenu` of words. A touch has no drag, so
moving is Cut or Copy from the selection bar or a long press and Paste from the bar or More. The
inspector hidden at a desk and the sidebar hidden at a desk are both still on the bar. The editor keeps
the tree and the editor mounted while the other is shown, so an unsaved draft outlives a look at the
tree; choosing the open file returns to it without asking, another file asks first. `files-ui.spec.ts`
holds both at 390×844, the strip at 800px and the columns at 1280px.

page and the container, process and stack detail pages are not on it yet.

A database's four workbenches are on it too, through `components/database/workbench/chrome.tsx`
(`WorkbenchFrame` takes the bar as `bar` and draws it at the foot of the frame, under the head every one
of them shares). Their break is not a window width: each shows one pane exactly where its side column
already lay over the work instead of beside it (the table editor and the SQL editor decide that from the
frame's measured width, the rest at `lg`), so the bar names states the page already had and adds none.

| Workbench | Stops | Opens on |
| --- | --- | --- |
| Data, SQL | Tables, Rows, Details | the tables with none open, else the rows; a row's card opens Details |
| Data, MongoDB | Collections, Documents | the collections with none open |
| Data, Redis | Keys, Key (Keyspace with none open) | the keys with none open |
| Query, SQL | Editor, Results, Saved, Assistant where it is offered | the editor; running or explaining shows Results |
| Query, MongoDB | Collections, Aggregations | the collections with none open, unless the view needs none |
| Query, Redis | Console, Commands | the console |
| Schema | Objects (Collections on MongoDB), then Table, Object or Schema | the tree with nothing chosen |
| Diagram | Diagram, then Table, Relation or Tables | the canvas |

Under 480px of frame the table editor's rows are cards (`data/row-cards.tsx`: a row's first two columns
over a mono line of the rest, one 52px height in every state) and the row panel is their detail, so
nothing is read by scrolling sideways; a new row is staged and opened in the panel, since there is no
grid to move to it. `database-data.spec.ts`, `database-query.spec.ts`, `database-schema.spec.ts`,
`database-mongo.spec.ts` and `database-redis.spec.ts` each hold their own at 390×844.

Git file drafts stay in memory per checkout/path across SPA previews, with an unload guard across
repositories; cron builder drafts keep their captured crontab revision in memory per account. Their
explicit Close/discard and sign-out paths clear the retained content. File Cut clipboard entries are
removed only for successfully moved sources, preserving canceled, skipped and failed items.
