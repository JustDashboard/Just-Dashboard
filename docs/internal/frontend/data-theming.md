# Frontend data flow and theming

- `src/lib/api.ts` is the only fetch layer: `get/post/put/patch/del`, `credentials: "include"`,
  `X-JD-CSRF` on every mutation, URI-encoded exact `X-Confirm` with
  `X-Confirm-Encoding: uri` (including Unicode and surrounding whitespace), `ApiError` with
  `needsConfirmation`/`isAuthProblem`/`needsTotp`; `wsUrl()` and
  `downloadUrl()` build the non-JSON URLs.
- `usePoll` schedules the next request only after the previous one settles and pauses scheduled
  requests on hidden tabs. Its fixed-length dependency list identifies the resource: changing it
  immediately hides the previous resource's data and resets loading. Refreshes and cadence changes
  retain the same resource's data. Cleanup aborts the request and ignores late responses.
- `useSocket` — reconnect with backoff (these sockets ride a tunnel that drops routinely), handlers in a
  ref so a fresh closure does not rebuild the socket.
- `lib/view-state.ts` is what a page remembers about itself, in three stores drawn by how long the
  thing should live. `useViewState` is **how the page is arranged** — a hidden panel, a chosen tab, a
  sort order, a toggle — in localStorage, so a reload keeps it. `useSessionState` is **what you were
  doing** — the filter in the box, the chip narrowed to, the page of results, the row whose detail is
  open, the SQL in the editor, a form half filled in — in sessionStorage, so it survives moving between
  pages and a reload and is gone when the tab closes. `useMemoryState` is the same for a value that must
  never be written down by the browser (a secret in an unsaved form: a credential, a database password,
  a backup destination's keys, an environment value): it lives as long as the page's JavaScript does,
  across navigation but not a reload. All three have `useState`'s shape; a dotted key names the page and
  the thing, and a third argument to `useSessionState` is the value the address bar handed over, which
  wins on arrival and is remembered from then on. A form draft is keyed under the revision or name it
  was read from, so a save from anywhere starts it again from the server's copy; a dialog's fields are
  forgotten (`forgetSessionState`/`forgetMemoryState` by prefix) when it is closed by hand, never by
  navigation; and `forgetWorkingState` empties both working stores on sign-out. `useQuerySelection`
  keeps a sheet's selection in the address bar and, per page and key, in the session store, so
  arriving on the rail's bare link puts the last selection back with `replaceState`; the databases
  layout does the same for `?conn=`, `?schema=` and `?table=`. Every route area was reviewed for this:
  filters, chips, facets, pagination, chosen sub-tabs, open detail rows, in-progress forms and the
  whole new-project flow are remembered; a search box is no longer the exception it used to be.
- `useMetricsWindow` — the charts' window as a **stack**: zooming is exploratory, so the way out of five
  minutes is the hour it was inside, not the day you started from. Deliberately component state — a named
  range is a standing choice, a zoom is a question being asked now, and restoring yesterday's zoom shows an
  empty window with no obvious way out. `useMetricEvents`/`useHealth` poll on much slower cadences.
- `src/lib/types.ts` mirrors the backend's JSON by hand, including the `Capability` union — it drifts if
  backend types change without it. `useAuth`'s `can("capability")` hides controls a role cannot use:
  **affordance only**, the server re-decides every request.
- An account that owes a password change remains unauthenticated in the UI after completing any
  required second factor. `/login` presents the current/new password form, then returns to credentials
  after the server revokes sessions. `password_change_required` responses also return the shell to this
  flow; they never grant access to ordinary feature controls.
- Database selection belongs to a specific query and result snapshot. Sorting, filtering, paging,
  refreshing, and changing tables require a fresh selection. Row editors are bound to their original
  connection and table. Exact integer/decimal SQL values and Redis scan cursors travel as strings;
  `lib/db-values.ts` preserves precision and rejects non-finite ordinary numeric input. CSV exports
  escape column names and carriage returns with the same rules as cell values.
- PM2 actions, deletes, and log sockets send both the trusted `daemonId` as `user` and numeric `id`.
  The application name alone cannot identify a process across multiple account-owned daemons.
  A false `logsAvailable` shows the server's `logsUnavailableReason` instead of opening a rejected
  socket. Available log streams show their connection state, including reconnects.
- Blueprint catalogue entries expose `deploymentSupported` and `unavailableReason`. Unsupported
  entries remain visible with their reason but cannot be selected or inspected for deployment.
  Template selection keeps a stable details column while fetching, preserves per-template edits, and
  ignores obsolete inspection responses. The new-project flow keeps secret-bearing inputs in memory;
  saved environment values resume as masked names from the encrypted server draft. Hostname changes
  update only unchanged template-derived URL defaults, and retain visitor password protection.
- Compose stack creation, file edits, validation, and execution require `system.admin` alongside each
  action's existing capability. Stack pages hide those controls from limited accounts, explain the
  restriction, and retain stack/config/log read views. Direct container controls retain their separate
  capability checks.
- `ConfirmDialog` collects the typed phrase and the server re-checks it. Its `phrase` is optional and the
  absence is meaningful: a request without one is reversible but still deserves a pause (deleting a
  terminal folder loses a grouping and nothing else), and asking somebody to type "delete folder" teaches
  them to type phrases without reading — the one habit the typed confirmation exists to prevent.
- `lib/metrics-store.ts` keeps the live series **outside React**: owning five minutes of history in a route
  component threw it away on navigation, and pushing a 2 s frame through a context above the router
  re-rendered the terminal and log tail twice a second. Mirrored to sessionStorage so a reload keeps its
  chart, with points older than `STALE_MS` dropped on the way back in — a graph silently stitching this
  minute onto one from an hour ago is worse than one that starts empty.
- `lib/metrics-range.ts` defines the windows (`live`, `1h`, `6h`, `24h`, `7d`), their buckets and cadence,
  and the `MetricsWindow` a dragged span becomes (fixed in the past, fetched once, never re-polled).
  **Live and recorded data are never spliced into one line** — the cadences differ by two orders of
  magnitude, and a chart drawing twenty coarse points and a hundred fine ones at equal spacing lies about
  when things happened. Container charts offer only recorded ranges. `hooks/use-metrics.ts` and
  `hooks/use-metrics-history.ts` are the React surface over those two.
- `hooks/use-self-update.tsx` is one poll for the whole shell, and its gotcha is the feature's design
  problem: **the API goes away in the middle of the thing it is watching**. A failed poll during a run
  renders as "restarting", never an error, and the cadence is set from the fetch rather than an effect so a
  failure does not drop back to the five-minute interval. `components/update/` is the sidebar-footer notice
  (which renders *nothing* when there is nothing to say), the release-notes sheet, and the panel on
  `/dashboard` — that page is the dashboard's own version and nothing else, because the server's packages
  and the tool you look at it through are updated by completely different machinery.
- `hooks/use-self-config.tsx` is the same shape for the dashboard's own settings, with one problem the
  update flow does not have: a change to the port or the address means the dashboard **does not come back
  here**. Nothing on the client can follow it, so the run record carries the new endpoint and
  `components/config/restart-progress.tsx` states it as a URL to open rather than spinning on an address
  that is now answering nothing. It also renders a fourth outcome — `rolled_back`, the configuration that
  did not come up and was undone — because showing that as either success or failure would misreport it.
  The form on `/dashboard/configuration` **derives** its draft from the poll rather than mirroring it into
  state: a copy refreshed every two seconds would wipe half-typed input during a restart.
- Both runs' transcripts are drawn by `components/run-transcript.tsx` over `lib/transcript.ts`. The polled
  report carries only the file's last 64 KB; when that tail starts with the server's trimmed marker the
  console reads the whole file once (`getText` on `…/update/log` or `…/config/log`) and from then on
  extends it with each tail, finding where the tail begins in the whole copy (`extendTranscript`) and
  reading the file again only when the two stop overlapping. A read that fails during the restart leaves
  the tail on screen. While a run is live and the reader is at the end, the lines a poll brought are let
  out a few a frame rather than landing at once; scrolled up, searching or with reduced motion they are
  drawn outright. `lib/transcript.test.js` covers the line shapes and the merge.
- **There is one theme.** The light palette was removed: every tinted surface, status hue, chart colour
  and terminal ANSI slot had to be chosen twice and verified twice, and the second set was seen by almost
  nobody. Colours live on `:root` in `globals.css`. `lib/themes.ts` is now one function —
  `themeBootstrapScript()`, inlined in `<head>` with the request's CSP nonce — which puts `.dark` on the
  root element before first paint so the generated shadcn primitives' `dark:` variants resolve, sets
  `color-scheme`, and clears the stored light/dark preference from anyone upgrading. `hooks/use-theme.tsx`,
  the top bar's toggle, the palette's theme commands and `/appearance` are all gone; `<html>` keeps
  `suppressHydrationWarning` because the class is still applied by script.
