# Command search

## Plan

The current Ctrl/Cmd+K menu lists navigation and reads projects, saved database connections and
proxy sites. It has no recent destinations, inventory feedback or way to narrow resource types.
The intended outcome is a keyboard route back to the previous page and from a resource name or
domain to its existing detail view, anywhere in the dashboard.

This is a shell picker, not a flow page: it has no multi-step outcome. Keep `PaletteModal`, cmdk's
combobox/listbox behavior and the existing design tokens. Use the reading register's spacing,
type, selection and colour rules; the dialog remains a framed floating surface.

1. Put the previous destination first when the input is empty, then recent destinations and
   the current page's commands. Preserve query selections in memory and bound history to 12
   distinct destinations. Record navigation from every entry point, including browser Back.
2. Read inventory only while the palette is open, using existing authenticated GET routes.
   Add containers, Compose stacks, Git repositories, systemd services, PM2 applications, backup
   jobs and boards to projects, proxy domains and saved database connections. Index explicitly
   selected metadata rather than entire API payloads. Do not query database rows, logs, files,
   environment values or credentials. Do not add an unprivileged aggregate endpoint.
3. Match names, identifiers and useful metadata; rank exact names before prefixes, substrings and
   one-edit typos. Support named scopes (`domain:`, `db:`, `container:`, `repo:`, `service:`, etc.)
   and a visible scope control. Bound visible results and keep the selected identity stable as
   independent requests finish.
4. Show loading and partial failures with retry. A failed inventory must not masquerade as an
   exhaustive search. Preserve permission filtering, page commands, database engine pages and
   existing proxy commands. Searching or walking results must perform no mutation.
5. Verify ranking, scope parsing and history in Bun; keyboard, focus, deep links, refresh,
   partial errors, delayed responses, mobile layout and permission visibility in Playwright.
   Run the repository's changed-file gate against `patch/0.7.1` on this worktree's production
   frontend. Record an actual browser interaction with labelled fixture data and publish the
   recording with the PR. Review documentation before pushing. Leave the PR unmerged.

## Research

- [Linear Search](https://linear.app/docs/search): recent resources on opening search, global
  resource search, scoped types, and a separate current-view search. Applied here as recents,
  typed scopes and preservation of page-owned Find commands.
- [Raycast Quickstart](https://manual.raycast.com/quickstart): one entry point for destinations
  and contextual commands. Applied as one palette retaining the current page's commands.
- [W3C Combobox pattern](https://www.w3.org/WAI/ARIA/apg/patterns/combobox/): keep input focus while
  arrows move the active option, Enter accepts it and Escape dismisses the popup. Reuse cmdk and
  the existing Radix dialog rather than implementing a second focus model.
- `ui-ux-pro-max` query `keyboard focus modal`, UX domain: visible focus and unobscured selection.
  Apply to the scope control, retry control, input and scrolling results.

## Verification

Verified on branch `improve/smart-command-search`, based on the active `patch/0.7.1` branch,
with Bun 1.3.11 and a production frontend built from this worktree.

- `cd frontend && bun run build` — passed, including Next's production type-check pass.
- `JD_BROWSER_BASE_URL=http://127.0.0.1:43167 scripts/test-changed.sh patch/0.7.1` — passed:
  changed-file Prettier and ESLint, `tsc --noEmit`, 2,847 Bun tests, and 536 Chromium checks
  across the 21 browser specs selected by the gate. Shared shell/dialog reachability includes
  the design-system, navigation, editor, terminal and feature-page coverage.
- Ten search model/projection tests cover ranking, scopes, typo matching, duplicate identities,
  history bounds, filter changes, local destination validation and credential exclusion.
- Eleven command-search browser scenarios cover domain/container/previous-place navigation,
  both shortcut modifiers, arrows/Home/End, repeated keys, Escape and focus restoration,
  the native scope control, all ten inventories, partial failure/retry, delayed selection,
  fresh additions/deletions, read-only visibility, current database engine pages, page-owned
  Find, Monaco keyboard focus and mobile bounds/touch targets.
- `git diff --check` and local documentation-link validation — passed.

The frontend was served with:

```bash
cd frontend
bun run start --hostname 127.0.0.1 --port 43167
```

### Recorded workflow

[Watch the keyboard workflow](evidence/keyboard-workflow.webm) (24 seconds, 1280 × 800, WebM).
From the account page, keyboard events open search, type `domain: shop.example.com`,
open the site's inspection page, type `container: shop-web`, open its real detail view,
return to the exact previous site with Ctrl+K → Enter, and search `shop` across related resources.
The recorder checks the resulting addresses/rendered details and asserts zero mutation requests.

The browser tests and recording use explicitly labelled API fixtures against the production UI.
They verify rendering, keyboard behavior and navigation; live-host/backend integration was not
exercised. No private host inventory appears in the recording.

- [Scoped domain search, 1280px](evidence/domain-search-1280.png)
- [Global results, 1280px](evidence/global-search-1280.png)
- [Global results, 1720px](evidence/global-search-1720.png)
- [Global results, mobile](evidence/global-search-mobile.png)

To reproduce the evidence against an already built/running frontend:

```bash
cd frontend
JD_BROWSER_BASE_URL=http://127.0.0.1:43167 bun scripts/record-command-search.ts
```

### Documentation review

Compared the complete change with `docs/internal/`, `AGENTS.md`, `README.md` and
`CONTRIBUTING.md` before pushing. Updated the operator tour and screenshot, global shell search
behavior, terminal shortcut ownership, repository map, documentation index and this audit.
`AGENTS.md` and `CONTRIBUTING.md` require no update: workflow, checks, licensing and dependencies
remain unchanged. Backend route permissions, security invariants, configuration and database schema
also remain unchanged; their owning documents require no update.
