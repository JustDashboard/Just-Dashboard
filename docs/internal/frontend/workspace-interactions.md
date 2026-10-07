# Workspace interactions

`components/workspace/` supplies page-owned keyboard commands and place memory to Processes,
Services, Git, Logs, Docker containers, Packages, Backups, deployment setup, Audit and the Security
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
| Processes | Pause stops scheduled inventory reads; changing the question (a search, a chip, a workload) and explicit refresh still read it. Focusing a process holds the returned row order while readings update, keyed by PID and creation time. Leaving the table releases the order; signals retain the existing confirmation and identity checks. Escape clears the search, then the workload, then the held order. A row new since the last read rises once; a changed filter is a new list, not arrivals. |
| Services | The unit list keeps reading every five seconds; a row is a unit by name, so focus and place survive a read that reorders it. Escape clears the search, then the startup chip, then the state chip. The palette's **This page** group toggles the failed units. A unit whose state changed since the last read rises once; a changed filter is a new list, not arrivals. |
| Git | Commit messages are session drafts per checkout and clear after a successful commit. Alt+Up/Down and the adjacent-file buttons walk changed files. Escape closes the preview and returns focus to its file. The repository list has the shared row navigation and place memory. Find targets History's existing search. |
| Logs | Source and mode changes, run handoffs and settled filters become history entries. Typing settles after 400ms; submission commits immediately. Back/Forward rebuild the question from the URL. Consoles remember the opened and active record and following state in memory. Opaque content/position identities match fresh responses, distinguish repeated records and retain no raw log text in Web Storage. F3/Shift+F3, n/N and visible match buttons cycle search hits; opening or walking a line stops following. Refresh reconnects or searches in place. |
| Docker containers | Returning from a detail restores the list's focused container and scroll position. Name typing selects a container. Adjacent-container buttons and Alt+Up/Down use the first 500 rows of the last visible inventory, retained for the tab, and preserve the detail tab. A direct link without a remembered inventory disables unavailable adjacent navigation. |
| Packages | `?package=` is shareable and uses the existing query-selection history contract: Back closes the sheet before leaving the page. Closing a sheet returns to the originating row. Installed/update rows and Add software results support keyboard movement; Enter inspects a result. Refresh reads inventory and updates; it does not refresh the package-manager index or start an install. |
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
`useSearchParams` stays synchronized. `useHeldList` keeps only identities in React state; it never
stores audit, connection or login responses. The shared crosshair remains an external store;
only its overlays subscribe to moving instants, while charts subscribe to pin status.

Verification lives in the existing page browser specs (tests prefixed `workspace:`) and pure tests
for row navigation, held ordering, range selection, log identity and pinning. Run
`scripts/test-changed.sh <base>` against a production frontend built from the task worktree;
`JD_BROWSER_BASE_URL` selects that worktree's loopback server.
