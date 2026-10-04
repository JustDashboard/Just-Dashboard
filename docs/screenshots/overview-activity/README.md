# Overview activity comparison

The before and after images show the same eight mocked events on the Overview's existing mocked
host. Before is the unchanged `patch/0.7.1` page at `fb9500b0`; after is the implementation in this
change. The browser clock is fixed at 2026-10-04 14:30 UTC and reduced motion is enabled. The names,
identifiers and targets are fixture data; these captures contain no live server records.

| Viewport | Before | After |
| --- | --- | --- |
| 1720px | [Before](before-1720.png) | [After](after-1720.png) |
| 1280px | [Before](before-1280.png) | [After](after-1280.png) |
| 390px | — | [After](after-390.png) |

Both desktop panels retain their original 421px height. The list scrolls to the remaining backup and
reboot events. Full-page captures were also inspected at both desktop widths; the processes and
activity headers remain aligned, and the page and activity panel have no horizontal overflow.

The browser coverage in `frontend/tests/browser/overview-ui.spec.ts` checks the product marks, original
action/target tooltip, exact timestamp, audit-log navigation, empty state and running-run outcome at
1280px and 390px. Run it against a production frontend built from this worktree:

```bash
JD_BROWSER_BASE_URL=http://127.0.0.1:43121 scripts/test-changed.sh patch/0.7.1
```
