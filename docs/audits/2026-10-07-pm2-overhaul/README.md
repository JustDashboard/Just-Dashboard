# PM2 overhaul and merge verification

The screenshots in this directory record the PM2 redesign in PR #165, including
[the page](after-page-1440.png), [an application](after-sheet-api-1440.png), and
[the start dialog](after-start-1440.png).

## Files history regression

Integration verification also exposed a Files history failure. After a native traversal, Next
occasionally replaced the current entry's state without `jdFiles`, so returning forward to that
folder reset its trail and disabled Back. Files now retains only its folder marker when the same
entry is rewritten without an explicit marker. The incoming router state stays intact; another
address or an explicit marker keeps its own state. The wrapper becomes inactive on cleanup.

The browser test waits for the destination address between traversals, because retained listing
rows can belong to the previous folder. Pure tests cover router rewrites, another entry, explicit
marker replacement and clearing. The production build and five consecutive runs of the existing
browser history test passed.

[Before interaction replay](files-history-before.trace.zip) ·
[After interaction replay](files-history-after.trace.zip)

Both recordings use the mocked Files API and its fictitious `/home/operator` inventory. They cover
opening a folder, its parent, native Back, reload, and toolbar and keyboard Back/Forward. Open a replay
from `frontend/`:

```bash
bunx playwright show-trace ../docs/audits/2026-10-07-pm2-overhaul/files-history-after.trace.zip
```

The targeted gate for this follow-up is `scripts/test-changed.sh HEAD` against the worktree's newly
built frontend. Browser checks use one worker and a 90-second timeout during shared machine load.
The broader PM2 integration checks passed 3,011 unit tests, Go build/vet and the adjacent PM2 tests,
and 218 browser checks with one existing skipped case. The history follow-up passed 3,013 unit tests.
