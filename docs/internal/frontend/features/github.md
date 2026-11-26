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

What joins them: a clone from GitHub lands in Local Git, and a GitHub card carries a mark when a
checkout of it is on this server — matched by the checkout's remote (`githubRemoteKey`,
`localCheckoutsOf` in `lib/github.ts`), never by folder name.

## `/github`

A reading page drawn as GitHub draws a person's own home, because that is the picture of it the
reader already has. It opens on its identity line in every state: the GitHub mark, "GitHub", and
facts. Connected, the facts are `@login · 9 repositories · 4 private · 1 archived` (`accountFacts`,
from `repositoryCounts`), a warning "Sign-in ended" first when the account's sign-in is over; with
several connections the login is the account picker (a menu of every connection with its state,
written to `?account=`; the default is the first active one, `pickConnection`). The command is
**New** (a repository; `service.control`), its icon alone on a phone. `⋯` holds Connect another
account, Sign in with a code, Refresh, Install on another account, App settings (administrator),
Disconnect @login (ordinary confirmation) and Remove GitHub setup (administrator, ordinary
confirmation; `s.destructive` on the server), as words without glyphs (**Menus are words**). `?`
ends the line.

Under the line, the one thing the state needs:

| State | What the page draws |
| --- | --- |
| Loading | The identity line's loading form and the Overview's shape (`HomeSkeleton`: the views' strip, the profile column, the calendar's height, four card ghosts); the route's Suspense fallback is the same (`GitHubListSkeleton`) |
| No App, administrator | `EmptyState` with the mark, one sentence and **Set up** (the setup dialog) |
| No App, anyone else | The same, saying an administrator sets it up once |
| No connection | `EmptyState` **Connect GitHub**; where this address is not a registered callback, the connect help in its place (below) |
| Sign-in ended (`expired`, `revoked`, or the list answering `github_reconnect`) | A warning `Notice` with GitHub's words and **Reconnect** |
| Connected | Any partial or truncated warning, then the four views |

### The views

Connected, the page has four views in the address (`?tab=`, `useDetailTab(HOME_TABS)`; Overview is
the bare address), drawn as the product's quiet tab strip with the repository count on
Repositories.

**Overview** (`Overview` in `list-page.tsx`): two columns from `lg` (a 16.5rem profile column and
the main one), one on a phone.

- *The profile column* (`home/profile.tsx`): the account's face, round, 112px on a desk and 64px
  on a phone where it shares a row with the name; the name over the login; the bio; followers and
  following; company, location and website, each by its glyph, only when GitHub has one (the
  website through `blogHref`, which adds a scheme and refuses anything but http and https). On a
  phone that part is one block of one height (`h-17`): the followers and the first fact on one line,
  the bio in at most two under it, so the calendar does not move when the profile lands. Under a
  hairline (at the page's foot on a phone), **Reaches**: one line per installation, the account's face, its login, All or Selected
  (or Suspended), and **Change** (that installation's settings on GitHub); then **Install** and
  **Refresh access** (`POST …/connections/{id}/refresh`, then the list read again past the
  server's cache). Arriving with `?installed=1` refreshes every active connection once and takes
  the flag off the address.
- *The year* (`home/contributions.tsx`, the page's picture, D2): the number of contributions
  counting up, the longest and current streaks, and GitHub's calendar as an SVG, a week a column in
  five `--contribution-*` greens. It draws as many of the latest weeks as its width holds
  (`weeksThatFit`; the year on a desk, the last months on a phone, never a sideways scroll) at one
  height in every state. Under it, the day under the pointer, or the busiest day, and the key.
  Where GitHub did not share the calendar (`contributionsMessage`), its sentence stands in the
  calendar's box.
- *Recently pushed*: the four repositories pushed to last, archived ones left out
  (`recentRepositories`), as cards, with **All repositories** to the Repositories view.
- *Your pull requests*, *Review requests* and *Assigned issues* (`InboxPreview`): each the first
  five open of one filter as rows, its section head counting GitHub's total and leading to the
  view. Nothing open is one quiet line.
- *Public activity* (`home/activity.tsx`): the last ten of `GET …/events` (public events only:
  the App asks for no account permission, so GitHub hands over nothing private), one fixed-height line
  each: the event's mark in GitHub's colour (merged violet, opened green, closed red, a star
  amber), the sentence (`eventWords`) with its object linked to GitHub, the repository (here when
  the page lists it, else GitHub) and how long ago. On a phone the repository and the time take a
  second line under the sentence, at one height for every event.

**Repositories**: the toolbar and every repository as a card.

- *The toolbar* (`ListToolbar`): the search, then Owner and Language selects (only when the list
  has more than one of either), the sort (`GITHUB_SORT_LABEL`), and a refresh that asks GitHub
  again (`?refresh=1`). The chips are All, Public, Private and Archived with counts; a state
  nothing is in gets no chip unless it is chosen. The filters live in
  `useSessionState(GITHUB_LIST_FILTERS_KEY)` and go through `narrowRepositories`, which the
  repository page's rail reads too.
- *The cards* (`home/repo-card.tsx`, `RepoCard`), one, two then three to a row
  (`REPO_CARD_GRID`), each `h-40` whatever it says: the language's logo on the tile (`RepoMark`),
  the name (the link, `Open owner/name`) with Private or Internal, the owner's face and login with
  the kind tags (`rowTags`: Archived, Fork, Template, "Shared with you", and a role below
  maintain); the description in two lines, or the topics, or "No description"; and a floor of the
  language by its logo, stars, forks, open issues, the "On this server" mark when a checkout
  matched, and when it was pushed. No arrow (**No arrow on a card**); the card and its name open
  the repository's page (`githubRepoHref(fullName, { account })`), and its `⋯` holds Open on
  GitHub, Clone to server…, Open in Local Git (when a checkout matched) and Copy clone URL, as
  words. Thirty cards, then **Show more** adds thirty and the cards already drawn hold still.
- No repositories: `EmptyState` "No repositories yet" with **New**. Nothing matches: one line and
  **Clear filters**. First read failed: `ErrorState`. Rate limit spent (`github_rate_limited`):
  "GitHub asks to wait", GitHub's sentence and the local time it lifts, from `Retry-After`
  (`listFailure`, `clockTime`).

**Pull requests** and **Issues** (`InboxView`): GitHub's own lists of the account's, across every
repository it reaches (`GET …/pulls` and `…/issues`, GitHub's issue search). Chips for which part
— Created, Review requests (pull requests only), Assigned, Mentioned — with GitHub's total on the
chosen one; Open or Closed as segments; a refresh. Rows (`InboxRow`, `ChoiceRow` without an arrow,
one height): the state as GitHub's glyph in its colour (a request open, draft, merged or closed; an
issue open, closed as completed, or closed as not planned), the title, `owner/name #n · opened 2
days ago by login`, and from a tablet two labels in their colours, the comment count and the
author's face. A row in a repository the page lists opens on that repository's page with the
request or issue open (`?tab=pulls&pull=n`, `?tab=issues&issue=n`, `inboxHref`); any other opens on
GitHub. Thirty a page, **Show more**. An `incomplete` search is a warning over the list.

Nothing here polls: GitHub's rate limit is the reader's own. The App and the connections are read
on arrival; the list, the profile and the events on arrival and on Refresh (each past the server's
cache then); the pull requests and issues when their view or preview is drawn (the server keeps
each answer a minute); Local Git's list once, for the marks.

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
`components/github/list-page.tsx` (the page, the connect help, the views, the Overview, the
Repositories toolbar), `home/` (`profile.tsx`, `contributions.tsx`, `repo-card.tsx`, `inbox.tsx`,
`activity.tsx`, and `model.ts`: the views, the calendar's fit, months and streaks, the recent
repositories, where a request or issue opens, the events in words; unit-tested in
`home/model.test.js`), `list-model.ts` (names, facts, failures, tags, permission words; unit-tested
in `list-model.test.js`), `callback.ts` (unit-tested in `callback.test.js`), `setup-dialog.tsx`,
`device-dialog.tsx`, `create-dialog.tsx`, `clone-dialog.tsx`; shared with the repository page:
`lib/github-types.ts` (the API), `lib/github.ts`, `lib/github-list.ts`. The rail rows are in
`components/shell/nav.ts`, the section key in `lib/sidebar-sections.ts`, the palette's logo for the
page in `components/command-search/marks.tsx`.

Checked by `tests/browser/github-ui.spec.ts` over `github-fixture.ts` (this page's routes, through
the shared `github-fixture-data.ts`, which also answers the profile with a seeded year
(`contributionYear`), the pull requests and issues by filter and state, the events, a README and
languages): the rail's two pages and `/git` as Local Git, search for both, setup as an administrator
and as read-only, Connect posting the origin, the code sign-in through to the new account and with
device flow off, the callback posting once and a cancel explained, the identity line's counts,
search, chips, owner, language and sort, Show more holding still, every card one height at 1440 and
390, the server mark on exactly the two cloned repositories and a menu of words, the expired
account's Reconnect and the account picker, the rate limit's time, a partial installation, the
access list and `?installed=1`, Disconnect; the Overview's profile, calendar (53 weeks on a desk,
fewer at one height on a phone, the day under the pointer), recent cards, previews and activity, and
the main column holding still while the profile lands; the pull requests' and issues' filters,
states, glyphs, where a row opens, and rows of one height; the create dialog's rule, refusal and
unreachable result, the clone streaming to Open in Local Git and a failed clone, and no sideways
scroll on any view at a phone's width with the icon command. `design-system.spec.ts` holds `/github` to the item-page rules, the identity line's rules
and the layout-shift budget at both widths.
