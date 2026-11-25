# One GitHub repository's page

`/github/<owner>/<name>?account=<connection>&tab=<view>`: one repository on GitHub, read and changed
through one connected GitHub account, on the detail-with-rail frame
([`../recipes/detail-with-rail.md`](../recipes/detail-with-rail.md)). The API is
`frontend/src/lib/github-types.ts`; every path is built by `githubRepoApi` and every address by
`githubRepoHref` (`lib/github.ts`).

**Files:** `app/(dashboard)/github/[owner]/[repo]/page.tsx` → `components/github/repo-page.tsx` (the
frame, the account, the read, the failure states) and one file per part under
`components/github/repo/`: `rail.tsx`, `identity.tsx`, `acts.tsx` (archive, visibility, delete),
`overview.tsx`, `branches.tsx`, `commits.tsx`, `pulls.tsx`, `issues.tsx`, `actions.tsx`,
`collaborators.tsx`, `settings.tsx`, `shared.tsx` (the repository context, gated buttons, paging) and
`model.ts` (the pure logic, unit-tested in `model.test.js`).

## The frame

- `DetailPage` named "Repository"; the way back is "GitHub" to `/github?account=<connection>`.
- **The account.** `?account=` names the connection. Without it the page reads `/github/connections`
  and the active connections' lists one at a time, and uses the first that lists the repository. None
  does: `MissingThing` "… is not reachable" with **Back to GitHub**.
- **The read.** The repository (`GitHubRepositoryDetail`) is read once and again after every change,
  never on a timer. Each view reads its own lists when it opens.
- **The rail** (`data-slot="github-repo-rail"`, from `xl`) is the list page's list for this account:
  `useRepositoryList` (once a minute, keyed so it is shared) narrowed and ordered by
  `narrowRepositories` with the filters in `useSessionState(GITHUB_LIST_FILTERS_KEY)`, which the list
  page writes. Its search writes the same filters; **Clear filters** resets all but the sort. Rows are
  links on the view on screen; Alt+↑ and Alt+↓ walk them with `?tab=` carried.
- **Failure states** (`RepoUnavailable`): `github_not_found` or `github_connection` →
  `MissingThing` with the way back; `github_reconnect` → a **Reconnect** link to the list page, which
  owns signing in; `github_rate_limited` → when the limit lifts (from `Retry-After`) and **Try
  again**; anything else → `ErrorState`.
- **Loading.** The identity line in its loaded shape with the owner's face and the name the address
  gives, its command's place held.

## The identity line

`data-slot="github-repo-identity"`, labelled "About <owner>/<name>". The owner's `ForgeFace`, the name
(the owner before it from `sm`), and the facts: archived first in the warning ink, visibility, the
default branch, the role when below admin, then language, stars and last update (desk only).

The command is **Clone** (opens `CloneToServerDialog` from `components/github/clone-dialog.tsx`), or
**Open** in Local Git when `localCheckoutsOf` finds a checkout of it in `/git/` (matched by remote).
The `⋯` (`VerbMenu`): Open on GitHub, Clone to server, Copy clone URL, Archive or Unarchive, Make
public or private, Delete. A verb its ability holds back stays in the menu `aria-disabled` with the
reason in its tooltip (`VerbSpec.reason`).

## The views

| View | What it draws | States |
| --- | --- | --- |
| Overview | Two columns from `lg`, as GitHub opens a repository: the main one and a 17rem side column, which comes first on a phone. Main: the picture first, the repository on GitHub wired through this server to its checkouts (`WireFlow`), or one sentence and **Clone**; then the README pane, headed by the last commit on the default branch (author's face, subject linked to GitHub, verified mark, short sha, time; `GET …/commits?sha=<default>&page=1`) and the README's path, its Markdown drawn by `Markdown` after `absoluteReadme` made its relative pictures GitHub `raw` addresses (read through the forge media reader) and its relative links GitHub `blob` pages (`GET …/readme`); then *What you can do here*: the role, the installation's selection, the App's grant in words, and every ability with its reason. Side: About (description, homepage, topics, stars, forks, open issues, visibility and default branch, template, fork parent, each by its glyph) and Languages, GitHub's bar in Linguist's colours with each language's logo and share (`LanguageBar`, `LanguageList`, from `GET …/languages` through `languageShares`). | Checkouts loading: the picture's loading shape. The README pane keeps a 16rem floor while read; no README is one line; a failed read is one line with Try again. Languages GitHub measured none of draw nothing. |
| Branches | Rows, the default first; `default` and `protected` marks; the protection and rulesets of a protected branch in a popover (`GET …/branches/protection`), the unreadable half saying why. **New branch** (name, from a branch). Delete per row (ordinary confirm). 100 a page, **Show more**. | `ListBody`: skeleton rows, `ErrorState`, one line when empty. |
| Commits | A branch select (default first); each commit's first line, author (`ForgeFace` with a login), relative time, verified mark, short sha, open on GitHub. **Show older**. | as above |
| Pull requests | Chips Open, Closed, Merged, All; `ChoiceRow` cards (state mark, number, title, author, head → base, draft, two labels, updated). A card opens the request in place (`&pull=<n>`, pushed so Back returns): body as `Markdown`, size and mergeability, checks, reviews, **Merge**, **Close** or **Reopen**, GitHub. | the request's own read: `LoadingRows`, `ErrorState` |
| Issues | Chips Open, Closed, All and a label select; cards (state mark, number, title, author, labels, comments). **New issue** (title, description, labels). An issue opens in place (`&issue=<n>`): labels, body, **Edit**, **Close** (as completed or not planned) or **Reopen**. | Issues switched off: one line saying so. |
| Actions | Workflows (name, path, state) with **Run** only where `dispatch.available`; one without says "on its triggers" with GitHub's reason in a tooltip. Runs (state dot and word from `runState`, title, workflow, branch, event, actor, time) with **Re-run** (all, or failed jobs, for a failed run) once finished and **Cancel** while queued or running. | Workflows unreadable: GitHub's message. |
| Collaborators | People with their role; invitations. | `readable: false`: an `EmptyState` with GitHub's message and no controls. |
| Settings | `SettingsColumn` under one save bar (D8): General (name, default branch, description, homepage, topics, template), Features (issues, wiki, projects, discussions), Pull requests and Commits only when GitHub stated those settings. One `SettingForm` sends only the changed fields (`settingsChanges`). A rename lands on the new address (`router.replace`). The danger zone: Archive or Unarchive, Make public or private, Delete. | A refused save keeps the draft and shows GitHub's words. Without the right, every value is read in place, disabled, with the reason as the form's note. |

The dialogs: **Merge** offers the methods in `mergeMethods` and sends the head `sha` the page showed
(GitHub refuses if it moved; its sentence is shown in the dialog). **Run** has a branch select and the
inputs by type: a switch for a boolean, a select for a choice, a box for the rest, required ones
marked and holding **Run** until filled. Archive says it makes the repository read-only and deletes
nothing. Make public says the code and whole history become visible and anyone can fork it. Make
private says others' stars and watchers go and public forks may be detached. Delete names the
repository, says it cannot be undone from here, and asks for `<owner>/<name>`, sent as `X-Confirm`;
done, the page goes to `/github?account=<connection>`.

## What gates which control

A control that changes something is drawn only where the operation exists, and is disabled with the
reason where it may not be used (`AbilityButton`, `Blocked`, `useActReason`). The reason is the first
of: the dashboard account's own rights (`service.control` for any change, `destructive` too for the
destructive ones), then the repository's ability with GitHub's reason (`can.reasons`). The backend
checks all of it again.

| Ability | Controls |
| --- | --- |
| `administer` | Settings form; Archive/Unarchive; visibility (plus `destructive`); collaborator roles, **Invite**, remove and cancel an invitation (removing plus `destructive`) |
| `delete` | Delete (plus `destructive`) |
| `push` | **New branch**; delete a branch (plus `destructive`; the default branch never) |
| `merge` | **Merge** (also held for a draft, a conflict, or no allowed method) |
| `pulls` | **Close** / **Reopen** a pull request |
| `createIssues` | **New issue**, **Edit** an issue (GitHub decides an author's own edit) |
| `issues` | Labels in the issue form; **Close** / **Reopen** an issue |
| `actions` | **Run**, **Re-run**, **Cancel** |

An archived repository's settings are read-only until it is unarchived; only Unarchive is offered.

## Specs

- `frontend/src/components/github/repo/model.test.js` (`bun test`): the settings diff (only changed,
  only stated), name, topic and branch rules, the run word and its verbs, reasons, the grant in words,
  the failure kinds.
- `frontend/src/components/github/repo/readme-model.test.js` (`bun test`): a README's relative
  pictures and links made whole against its own folder and branch, absolute addresses, fragments and
  paths that climb out of the repository left alone; bytes per language as shares.
- `frontend/tests/browser/github-repo.spec.ts` with `github-repo-fixture.ts` (every route of the
  views through `mockGitHub`'s `handle`, writes recorded in `calls`): the four detail-with-rail checks;
  each view against its fixture; reasons on held controls (`acme-labs/design-system`, role read;
  `acme-labs/legacy-portal`, archived without the administration grant); creating and deleting a
  branch with the default refused; the merge sending the sha shown; dispatch only for a dispatchable
  workflow, with its inputs; the collaborators' unreadable state; a settings save sending only what
  changed and a rename landing on the new address; visibility's confirmation; the typed delete sending
  `X-Confirm` and returning to the list; every list row one height in whole pixels.
- `frontend/tests/browser/design-system.spec.ts` lists `/github/northwind/api?account=1` among the
  item pages and the identity lines.
