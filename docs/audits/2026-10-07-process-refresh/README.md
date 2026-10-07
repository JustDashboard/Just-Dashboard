# Process refresh continuity

The Live table treated Automatic focus and maximum row count as a new polling resource. A change
cleared the inventory, unmounted its scroll container and reset the reader's place. Background
rankings also moved rows under the pointer, Services replayed row arrivals on state changes, and
cron toggles changed both the row and button identities.

Ranking and row-count changes now refresh the mounted Live inventory. The footer describes the
ranking that answered while the next read is pending. Search and filter changes still identify
a different question and hide obsolete rows.

Live, PM2, Services and timers hold row order during pointer or keyboard inspection, including menus
and detail sheets. Readings continue to update, exited identities disappear and new identities follow
the held rows. Leaving inspection releases the ranking. Existing services and cron jobs update state
in place; new rows retain their arrival animation. Cron toggle controls keep keyboard focus.

## Interaction recordings

These are production frontend browser runs against the mocked host in
[`processes-ui.spec.ts`](../../../frontend/tests/browser/processes-ui.spec.ts).

- [Automatic focus](automatic-focus.webm): a deliberately delayed ranking retains the same table
  and scroll position, with the previous ranking named until its replacement answers.
- [Services](services.webm): background state changes and failed polls retain inspected row order
  and the open menu; leaving inspection applies the latest ranking.
- [Cron toggle](cron-toggle.webm): disabling a job updates the same row and button with focus intact.

## Verification

The regression spec covers slow Automatic focus, background updates and failed reads on all four
lists at 390px and 1720px, reduced motion on narrow screens, PM2 and service start/stop, cron focus,
and a timer sheet opened by deep link before the list loads. Pure tests cover changed readings,
exits, arrivals, PID reuse and duplicate cron commands.

Run the required checks against the release branch with a production build from the task worktree:

```sh
cd frontend
bun run build
bun run start --hostname 127.0.0.1 --port 43127
```

In another terminal at the worktree root:

```sh
JD_BROWSER_BASE_URL=http://127.0.0.1:43127 scripts/test-changed.sh origin/patch/0.7.1
```

The API fixtures exercise frontend refresh behavior; they do not operate real host services.
