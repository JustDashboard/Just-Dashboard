# Files search palette verification

The Find and Search inside files commands share `quick-open.tsx`. This change removes the input's
active border/outline and fixes the frame at 34rem, capped to the viewport with a 16px margin.
The results scroll independently; loading controls and footer notices reserve space. Result rows
fade in and out, with departing rows immediately inert and hidden from screen readers. Reduced
motion shows the next state immediately. The search body remains visible during dialog dismissal.

## Evidence

All captures use the production frontend with mocked Files API responses, without a live host or
private files. The baseline screenshot is from `2fe48306`, the active `patch/0.7.1` branch at task start.

- [Before](before.png) and [after with the same query](after.png).
- Names: [30 results](names-many.png), [one result](names-single.png), [complete video](names.webm).
- Contents: [30 results](contents-many.png), [one result](contents-single.png), [complete video](contents.webm).
- [Phone](phone.png) and [short landscape window](landscape.png).
- [Animation preview](interaction.gif), cropped from the original Names video; the full videos preserve
  the complete sequence and timings.

The six state-sequence tests exercise both modes at 1280×960, 375×812 and 640×375. On every animation
frame they measure the dialog, input, results viewport and footer. Every coordinate and dimension
stays within 0.6px throughout typing, debouncing/loading, 30/one/no results, partial results,
unreadable-file notices, retryable errors, case/regex/hidden toggles, clearing and switching modes.
They also observe intermediate opacity on arrival and departure, verify no input outline/border,
check viewport margins, scroll to the last result with the keyboard, and dismiss with Escape.

The seventh test uses reduced motion on a phone and verifies immediate opacity/position, unchanged
frame size, and reachable folder/home scope controls. Existing tests retain immediate local matches,
opening results and their matching lines, stale-response cancellation, and usable local results after
remote failures.

## Reproduction

Build and start this worktree's frontend on a free loopback port:

```sh
cd frontend
bun run build
bun run start --hostname 127.0.0.1 --port 43129
```

Record the focused interaction tests (screenshots and WebM videos land in `frontend/test-results`):

```sh
cd frontend
JD_FILES_SEARCH_VIDEO=1 JD_BROWSER_BASE_URL=http://127.0.0.1:43129 \
  bunx playwright test tests/browser/files-ui.spec.ts -g 'fixed search palette' --workers=2
```

Run the required diff-selected gate from the repository root:

```sh
JD_BROWSER_BASE_URL=http://127.0.0.1:43129 scripts/test-changed.sh patch/0.7.1
```

## Results and local timing limits

The production build, Prettier, ESLint, TypeScript and all 2,812 unit tests pass. All seven focused
search tests pass at the normal assertion deadline, including their animation-frame measurements.

The required diff-selected gate ran 153 browser scenarios. It initially passed 141 and failed 12 on
loading/wait deadlines on this shared machine. A one-worker rerun with a 90-second test deadline
passed nine of those twelve. The remaining volume-editor, 500-file menu-focus and initial-Health-error
cases pass with a temporary local configuration using a 20-second assertion deadline. All 153 selected
scenarios therefore passed; the default-timeout gate itself was not green. No assertions, application
behavior, committed Playwright configuration or CI were changed to address these timing limits.

Hosted verification exposed an ambiguous existing file-opening assertion: the closing search dialog
briefly overlaps the new editor during dismissal. The name and content search tests now target the
`notes.md` editor explicitly and verify that the search dialog finishes closing. Both tests pass
locally at the normal deadlines; the screenshots and recordings remain current because this
follow-up changes only the tests. The follow-up gate against `bb2e60be` passes formatting, lint,
types and all 2,812 unit tests. Of its 27 Files browser scenarios, 25 pass at the default deadlines;
the large-list focus and full-editor screenshot cases exceed the 30-second test deadline and pass
unchanged with the temporary local configuration above. All seven search animation cases and both
corrected file-opening cases pass in the default-deadline run.

[Verification output](verification.txt) preserves the commands, summaries and temporary timeout
configuration. The captures are from the task worktree's final production build.

## Documentation review

Reviewed the complete diff against `docs/internal/`, `AGENTS.md`, `README.md` and `CONTRIBUTING.md`.
Updated the Files behavior in `docs/internal/backend/docker-files-logs.md` and the Files palette
rules in `docs/internal/frontend/design-system.md`. The other documents still describe the resulting
behavior and workflow; no updates are needed. No backend, dependencies, configuration or CI changed.
