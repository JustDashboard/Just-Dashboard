# Frontend data flow and theming

- `src/lib/api.ts` is the only fetch layer: `get/post/put/patch/del`, `credentials: "include"`,
  `X-JD-CSRF` on every mutation, `X-Confirm` passthrough, `ApiError` with
  `needsConfirmation`/`isAuthProblem`/`needsTotp`; `wsUrl()` and
  `downloadUrl()` build the non-JSON URLs.
- `usePoll` — abort-per-run so a slow endpoint cannot stack requests, paused on a hidden tab.
- `useSocket` — reconnect with backoff (these sockets ride a tunnel that drops routinely), handlers in a
  ref so a fresh closure does not rebuild the socket.
- `useMetricsWindow` — the charts' window as a **stack**: zooming is exploratory, so the way out of five
  minutes is the hour it was inside, not the day you started from. Deliberately component state — a named
  range is a standing choice, a zoom is a question being asked now, and restoring yesterday's zoom shows an
  empty window with no obvious way out. `useMetricEvents`/`useHealth` poll on much slower cadences.
- `src/lib/types.ts` mirrors the backend's JSON by hand, including the `Capability` union — it drifts if
  backend types change without it. `useAuth`'s `can("capability")` hides controls a role cannot use:
  **affordance only**, the server re-decides every request.
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
- **There is one theme.** The light palette was removed: every tinted surface, status hue, chart colour
  and terminal ANSI slot had to be chosen twice and verified twice, and the second set was seen by almost
  nobody. Colours live on `:root` in `globals.css`. `lib/themes.ts` is now one function —
  `themeBootstrapScript()`, inlined in `<head>` with the request's CSP nonce — which puts `.dark` on the
  root element before first paint so the generated shadcn primitives' `dark:` variants resolve, sets
  `color-scheme`, and clears the stored light/dark preference from anyone upgrading. `hooks/use-theme.tsx`,
  the top bar's toggle, the palette's theme commands and `/appearance` are all gone; `<html>` keeps
  `suppressHydrationWarning` because the class is still applied by script.
