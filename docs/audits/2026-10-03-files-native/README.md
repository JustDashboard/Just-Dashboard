# Files navigation and native interactions

Folder visits now have browser history entries and shareable URLs. Back/Forward, the mouse's history
buttons and the Files controls traverse folders; Parent/Backspace/Alt+Up go up within the allowed
roots. A new visit after Back discards forward history, and browser Back can leave Files when its
folder history is exhausted. Selection, active item, scroll and keyboard focus return with a folder.

Files owns local Find and Refresh, name typing, list/tile arrows, new-folder and hidden-file shortcuts,
selection toggling and staged Escape cancellation. Text fields, composition, dialogs, menus and the
dashboard rail retain their own keys. A footer control opens the shortcut reference. New mutation
shortcuts call the existing operations and capability guards; backend routes are unchanged.

## Proof

The [recording](interactions.webm) shows keyboard name navigation and Enter, Back into the previous
folder, tile navigation/selection, local Find, shortcut help and phone Back/Forward.

- [Desktop](desktop.png) — 1280×960.
- [Phone](phone.png) — 390×844, reduced motion.
- [Shortcut reference](shortcuts.png).

Captures use the task worktree's production build with synthetic API fixtures. The transparent image
and pending video poster are fixtures, not a real server's media. The recording contains no host data.
Animations are settled for the screenshots. Browser verification is Chromium on Linux; physical
mouse side buttons and native Safari trackpad gestures are not separately exercised. Browser history
traversal is tested directly with Playwright Back/Forward and keyboard history chords.

## Verification

`bun run build` passed. The final required gate passed formatting, ESLint, TypeScript, 2,821 logic
tests and all 188 selected browser cases:

```sh
JD_BROWSER_BASE_URL=http://127.0.0.1:43221 scripts/test-changed.sh patch/0.7.1
```

The selected specs were `design-system`, `docker-ui`, `files-ui`, `navigation` and `server-advisor`.
The nine new Files scenarios cover reload, branching history, deep links in a fresh tab, leaving
Files, a long listing's state/focus restoration, local refresh without remounting, text/control/menu
ownership, list/tile keyboard behavior, restricted roots, a read-only account and small-phone/landscape
layout. Existing editor, search, preview, selection, drag/drop and upload scenarios also passed.

The proof recording passed in a separate run against the same build:

```sh
cd frontend
JD_FILES_INTERACTIONS_VIDEO=1 JD_BROWSER_BASE_URL=http://127.0.0.1:43221 \
  bunx playwright test tests/browser/files-ui.spec.ts --grep 'record the Files workflow' --workers=1
```

Documentation review covered the complete diff against `docs/internal/`, `README.md`, `AGENTS.md` and
`CONTRIBUTING.md`. The Files implementation guide, feature map and operator instructions were updated.
No contributor workflow, dependency, backend security, deployment or CI documentation changes were
needed.
