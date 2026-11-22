# Features: GitHub

The GitHub page and its callback: what each is responsible for, then, under **Implementation**, the
files that own it and the specs that check it. A repository's own page (`/github/[owner]/[repo]`)
is described by the lane that owns it, in its own section of this file.

## Local Git and GitHub

Two pages under the rail's Workspace group, side by side since 0.9.1:

| | **Local Git** (`/git`, `GitBranch`) | **GitHub** (`/github`, `GitHubMark`) |
| --- | --- | --- |
| What it lists | The checkouts on this server, under the git roots | The repositories a connected GitHub account reaches |
| Who answers | This server's own git, as each checkout's owner | GitHub's API, as the person who connected the account |
| Its GitHub sign-in | `gh` for the host account that owns a checkout (`GitHubAccountControl`), used by push and pull | A connection of this dashboard account, through the dashboard's repository App |
| Needs the other | No | No |

The route `/git` did not move; only its name did ("Git" became "Local Git" in the rail, the tab and
its `h1`). `github` is a section key of its own (`lib/sidebar-sections.ts`), in the "Work on the
server" purpose beside `git`, so an account that hid Git before 0.9.1 still sees GitHub until it
hides it. Command search finds both by "git" and "github" (the pages' addresses are keywords).

What joins them: a clone from GitHub lands in Local Git, and a GitHub row carries a mark when a
checkout of it is on this server — matched by the checkout's remote (`githubRemoteKey`,
`localCheckoutsOf` in `lib/github.ts`), never by folder name.

## `/github`

A list page (`recipes/list-page.md`), reading register. It opens on its identity line in every
state: the GitHub mark, "GitHub", and facts. Connected, the facts are `@login · 9 repositories ·
4 private · 1 archived` (`accountFacts`, from `repositoryCounts`), a warning "Sign-in ended" first
when the account's sign-in is over; with several connections the login is the account picker (a
menu of every connection with its state, written to `?account=`; the default is the first active
one, `pickConnection`). The command is **New** (a repository; `service.control`), its icon alone on
a phone. `⋯` holds Connect another account, Sign in with a code, Refresh, Install on another
account, App settings (administrator), Disconnect @login (ordinary confirmation) and Remove GitHub
setup (administrator, ordinary confirmation; `s.destructive` on the server). `?` ends the line.

Under the line, the one thing the state needs:

| State | What the page draws |
| --- | --- |
| Loading | The identity line's loading form and `ChoiceListSkeleton` at the rows' box; the route's Suspense fallback is the same (`GitHubListSkeleton`) |
| No App, administrator | `EmptyState` with the mark, one sentence and **Set up** (the setup dialog) |
| No App, anyone else | The same, saying an administrator sets it up once |
| No connection | `EmptyState` **Connect GitHub**; where this address is not a registered callback, the connect help in its place (below) |
| Sign-in ended (`expired`, `revoked`, or the list answering `github_reconnect`) | A warning `Notice` with GitHub's words and **Reconnect** |
| Connected | The access strip, any partial or truncated warning, the toolbar and the list |
| No repositories | `EmptyState` "No repositories yet" with **New** |
| Nothing matches | One line, **Clear filters**; the toolbar stays |
| First read failed | `ErrorState` with Try again |
| Rate limit spent (`github_rate_limited`) | "GitHub asks to wait": GitHub's sentence and the local time it lifts, from `Retry-After` (`listFailure`, `clockTime`) |

**The access strip** says what the App reaches for this account, one entry per installation: the
account's face, its login, "All repositories" or "Selected repositories" (or "Suspended"), and
**Change**, a link to that installation's settings on GitHub. At its end **Install** (the App's
install page) and **Refresh access** (`POST …/connections/{id}/refresh`, then the list read again
past the server's cache). An installation whose listing failed is a warning `Notice` per entry of
`partial`; `truncated` is one more. Arriving with `?installed=1` (GitHub's setup redirect after an
installation changed) refreshes every active connection once and takes the flag off the address.

**The toolbar** (`ListToolbar`): the search, then Owner and Language selects (only when the list
has more than one of either), the sort (`GITHUB_SORT_LABEL`), and a refresh that asks GitHub again
(`?refresh=1`). The chips are All, Public, Private and Archived with counts; a state nothing is in
gets no chip unless it is chosen. The filters live in `useSessionState(GITHUB_LIST_FILTERS_KEY)` and
go through `narrowRepositories`, which the repository page's rail reads too.

**The list** is a `ChoiceList` of `ChoiceRow`s, one height in every state and at every width: the
owner's face, the name over a line of `owner/name · default branch · description` (the branch goes
on a phone; the description is the line's tooltip too), then aligned columns from wide to narrow —
tags (Archived, Fork, Template, "Shared with you" for a collaborator's repository, and the role when
it is below maintain: Read only, Triage, Write; `rowTags`), the "On this server" mark, visibility
(a lock for private and internal), language, stars, forks and when it last moved. A phone keeps the
name, its line, a private lock and `⋯`. The row opens the repository's page
(`githubRepoHref(fullName, { account })`); its `⋯` holds Open on GitHub, Clone to server…, Open in
Local Git (when a checkout matched) and Copy clone URL. Thirty rows, then **Show more** adds thirty
and the rows already drawn hold still. Rows are keyed by full name.

Nothing here polls: GitHub's rate limit is the reader's own. The App and the connections are read
on arrival, the list on arrival, on Refresh and after a change; Local Git's list once, for the marks.

### Setting up the App

**Set up** opens a `Modal`: an optional App name, the owner (my account, or an organisation by
login, checked against GitHub's login rule), "Let organisations and other accounts install it"
(on by default; an `Explain` says nothing reaches this dashboard without a person's own sign-in
here), and what the App will ask for, in words (`permissionWords(MANIFEST_PERMISSIONS)`).
**Continue on GitHub** posts `/github/app/manifest` with `origin: window.location.origin` and then
posts the manifest to GitHub as a form, as the deploy App's card does. GitHub sends the browser back
to `/github/callback`.

### Signing in

Two of GitHub's own flows, both from the page:

- **The web sign-in** (the default): **Connect GitHub** posts `/github/connections/authorize`
  `{ origin }` and goes to the address it answers. GitHub only redirects to a callback URL
  registered on the App, so the page checks first (`githubCallbackRegistered`); where this address
  is not one (a dashboard opened at an IP or another name), and where the server answers
  `github_callback_unregistered`, the **connect help** says so, lists the registered addresses and
  offers **Sign in with a code** — and for an administrator **App settings** and **Add this
  address** (`POST /github/app/callback-urls`, after adding `<origin>/github/callback` on GitHub).
- **Sign in with a code** (the device flow): a dialog with the code large and copyable, **Open
  GitHub** (the verification address) and a countdown. It asks the server how the flow stands
  (`GET /github/connections/device/{flow}`) no faster than GitHub's `interval`, one ask at a time;
  `complete` lands on the new account, `denied`/`expired`/`failed` say so with Try again. An App
  without "Enable Device Flow" (`github_device_flow_disabled`) says where to tick it, with a link
  to the App's settings. The device code never reaches the page; it holds an opaque flow id.

## `/github/callback`

Where GitHub returns after creating the App or a web sign-in. It reads `code` and `state`, or
`error` and `error_description` (Cancel on GitHub), takes them off the address at once, and posts
`/github/callback` exactly once per page load (`takeGitHubReturn` holds the one promise, so React's
strict mode and a remount never post twice). `kind: "connection"` goes to
`/github?account=<id>`, `kind: "app"` to `/github` with a toast. A refusal is an `ErrorState` with
the server's reason and **Back to GitHub**; a cancel or an empty return is a sentence
(`deniedWords`) and the same way back. It holds one shape while it works.

## New repository

`CreateRepositoryDialog` (`components/github/create-dialog.tsx`), a `Modal` from the command:
the owner (`create-options`; an owner that cannot take one is listed disabled with its note, and a
selected-repositories installation's note is the field's hint), the name with GitHub's rule held as
it is typed and the name GitHub will use said when it differs (`githubRepoName`), a description,
visibility (`Segments`, Private first, with an `Explain`), Add a README (on), a `.gitignore`
template and a licence. A `github_validation` refusal about the name lands on the field; anything
else is a `Notice`. On success the list is read again, and the page opens the new repository —
unless `reachable` is false (an installation on selected repositories), when the dialog stays and
says why.

## Clone to server

`CloneToServerDialog` (`components/github/clone-dialog.tsx`; its props are the contract the
repository page opens it with): into one of Local Git's roots (`/git/roots`; only the roots, not a
sub-folder of one), a folder named by the server's rule (`cloneFolderProblem`: one segment, not
hidden, no leading dash, ref characters only; the repository's name by default), the branch
(default branch prefilled) and "Latest commit only" (depth 1). It says where the checkout lands, and
for a private repository that it is fetched with a one-time read-only token and that later pulls use
this server's own git sign-in. **Clone** posts `…/clone`, opens the job it answers
(`useJobConsole().open`) and streams `git clone --progress` in a `JobConsole` inside the dialog.
After a success Local Git's list is read again; **Open in Local Git** is offered only when that list
has the target, and `onCloned(path)` is called then. A refusal is a `Notice` and a failed job keeps
its console and the form for another try.

## Implementation

`app/(dashboard)/github/page.tsx` (Suspense around the list), `app/(dashboard)/github/callback/page.tsx`;
`components/github/list-page.tsx` (the page, the connect help, the access strip, the rows),
`list-model.ts` (names, facts, failures, tags, permission words; unit-tested in
`list-model.test.js`), `callback.ts` (unit-tested in `callback.test.js`), `setup-dialog.tsx`,
`device-dialog.tsx`, `create-dialog.tsx`, `clone-dialog.tsx`; shared with the repository page:
`lib/github-types.ts` (the API), `lib/github.ts`, `lib/github-list.ts`. The rail rows are in
`components/shell/nav.ts`, the section key in `lib/sidebar-sections.ts`, the palette's logo for the
page in `components/command-search/marks.tsx`.

Checked by `tests/browser/github-ui.spec.ts` over `github-fixture.ts` (this page's routes, through
the shared `github-fixture-data.ts`): the rail's two pages and `/git` as Local Git, search for both,
setup as an administrator and as read-only, Connect posting the origin, the code sign-in through to
the new account and with device flow off, the callback posting once and a cancel explained, the
identity line's counts, search, chips, owner, language and sort, Show more holding still, every row
one height at 1440 and 390, the server mark on exactly the two cloned repositories, the expired
account's Reconnect and the account picker, the rate limit's time, a partial installation, the
access strip and `?installed=1`, Disconnect, the create dialog's rule, refusal and unreachable
result, the clone streaming to Open in Local Git and a failed clone, and the phone's width and icon
command. `design-system.spec.ts` holds `/github` to the item-page rules, the identity line's rules
and the layout-shift budget at both widths.
