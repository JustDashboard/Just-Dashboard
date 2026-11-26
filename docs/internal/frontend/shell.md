# The shell

Routing, the shell around every page, the rail, sign-in, the hub link, command search and the
editor. The component map that used to close this page is in
[`components/README.md`](components/README.md), and the chart notes are in
[`components/readings.md`](components/readings.md).

The App Router's `page.tsx` entry points (`find frontend/src/app -name page.tsx` counts them) include
nested database, Docker, proxy, security, and deployment workflows plus `/login` and `/welcome`. Most page modules are client components; the three
deployment detail/new wrappers remain server components and hand interaction to client components under
`components/deploy/`.

**A route imports its page from a module that is that page's alone.** The bundler keeps a module
whole: it drops neither the exports a route never names nor what they import. So a folder index that
re-exports several pages, or one file that holds several, hands every one of them to each route that
names one. The database section did both until 0.8.6 (`ops/index.tsx` over thirteen pages, the eight
settings pages in one `ops/settings.tsx`, `mongo/index.tsx` and `redis/index.tsx` over the engines'
pages): a settings page loaded 2.2 MB of entry JavaScript, half of it other pages and recharts. Each
route now names its page's own file (`ops/settings-version-page.tsx`), what the settings pages share
is `ops/settings-frame.tsx`, and `ops/page-modules.test.js` walks every database route's imports and
fails when two routes share a page module or one reaches another's. An index over small shared parts
(`database/kit`) is not this: every page that imports it uses most of it.

## The shell

`(dashboard)/layout.tsx` owns `CommandPaletteProvider`, `SelfUpdateProvider`, `NavScopeProvider`,
`SidebarProvider` + `AppSidebar`, `MetricsStream` — which renders nothing and exists to hold the
metrics socket open for the whole shell, so Overview's charts keep filling from other pages — and
`SavedFolderColours` around the page, which reads the folder labels from `/files/places` and again on
each navigation, so a folder coloured in Files is that colour in every tree and browser, and
`HeldSessionsProvider` with `HeldSessionsHost` after the page, which draw Remote Desktop's sessions
so they stay connected across pages (the session page only names which one is shown). Its
redirect to `/login` is convenience, not a control; every API call behind it is authenticated server-side.
The authenticated workspace is keyed by account ID. Session recovery for a different account remounts
its providers and pages after account working-state cleanup, removing mounted editor buffers and held
sessions. Recovery for the same account keeps those mounts and their drafts.

**The scroll container is on the `SidebarInset`, not the document.** That is what lets a page ask for
the remaining height (`<Page fill>`) instead of growing past the viewport.

**The first Tab on every page is "Skip to content"** (`SkipToContent` in
`(dashboard)/layout.tsx`). It stands above the window until it has the focus. Enter moves the
focus to the page's `h1` rather than the address, so a screen reader says which page this is and
the next Tab is the page's first control. The heading holds a `tabindex` only until it loses the
focus. `<main>` is never focusable: a click anywhere in a focusable `<main>` would move the focus
to it, and the browser scrolls with the keyboard from the focused element outwards, which would
take Page Down away from the pane that was clicked.

**A table comes fully on screen before its rows scroll.** A capped table (`max-h-[calc(100svh-22rem)]`
and the like) is sized to fit under the page's header, so with the page anywhere but its top the
wheel would scroll a grid half below the fold. Every `ui/table.tsx` container listens through
`hooks/use-reveal-on-wheel.ts`: when the wheel would scroll its rows while the shell's scroll (or a
dialog body's) clips part of it, the page first moves the smallest distance that shows the whole
container — `lib/scroll-reveal.ts`, 12px clear of the edge, its top when it cannot fit — and holds the
wheel while it glides, so the rows stay still under the reader. A wheel past the table's end already
scrolls the page and is left alone, as are a table with nothing to scroll, sideways and Ctrl-zoom
wheels; reduced motion jumps rather than glides. The database `DataGrid` fills its frame and has no
page around it to move.

The navigation sidebar is always expanded on desktop: the shell disables desktop collapse in
`SidebarProvider` and does not read the old `shell.sidebar` preference. Desktop has no toggle
and Ctrl/Cmd+B does not collapse it. Below `md` the rail is a sheet. The strip across the top opens
it and the sheet's own head closes it; Ctrl/Cmd+B still toggles it. A page opened from the sheet
puts it away: `SidebarProvider` closes it when the pathname changes, and a page's row closes it on
the press, so the row of the page you are already on closes it too. A row that only drills into a
panel (the way back out, a group) leaves it open. Below `md` the sheet's rows, the way back out,
Search and the close control are 44px, and so are the strip's three controls (sidebar, Ask,
Search) in its 48px. The strip is a `<header>` beside `<main>`, so it is the phone's banner, as
the rail's head is the desk's. `shell-phone.spec.ts` holds all of it.

### When there is no page to show

An address no page claims goes through `(dashboard)/[...missing]` to `(dashboard)/not-found.tsx`, inside
the shell with the rail still there, and someone signed out is sent to sign in first. It answers HTTP 200:
the shell draws its splash until the session probe returns, so the server never renders the page that
throws the 404. The probe is started by an inline script in the document's head, so it is in flight
while the shell's JavaScript downloads. A probe that fails for any reason but the server saying nobody is signed in keeps the
splash and is asked again; from the first such failure the splash carries one sentence saying why it is
still there ([data layer](patterns/data-flow.md), "The first probe of a page load"). `app/not-found.tsx` is the same page without a shell, for what the login and welcome pages
throw.

A render that throws under `(dashboard)` keeps the shell (`(dashboard)/error.tsx`). One from the dashboard's
own layout, or the login and welcome pages, takes the window (`app/error.tsx`); one from the root layout
replaces it (`app/global-error.tsx`), which brings its own document, stylesheet and font and has no inline
script, because a client component cannot read the CSP nonce. The two share `RenderFailure`, whose way out
is a full load rather than a router link back into what threw.

### The document proxy and the build configuration

`src/proxy.ts` runs for every document request and nothing under `/api`, `/_next/static` or the files
`public/` serves by the dozen (`/monaco/`, `/logos/`, `/brand-logo/`, Excalidraw's fonts: its
`matcher`). It mints the per-request nonce and the Content-Security-Policy that goes with it, and it
is where the build's routing choices in `next.config.ts` are completed.

**A trailing slash costs an API call nothing, and a page still loses it.** About 140 calls spell a
list endpoint with its slash (`/jobs/`, `/deploy/`), which the backend's router accepts. Next answers
any such path with a 308 to the path without the slash before it consults a rewrite, so wherever Next
fronts the API (`bun dev`, the standalone server under the browser specs) each of those calls was two round
trips. `skipTrailingSlashRedirect` turns that redirect off and the request reaches the `/api` rewrite as
sent; `withoutTrailingSlash` in the proxy does the redirect for pages, so `/docker/` is still `/docker`
and the rail's `pathname === href` still matches. In production Caddy routes `/api` to the backend and
neither rule is met. The spelling of the calls is unchanged: the specs' mocks match it exactly.

### The tab's title

The dashboard's layout renders a React `<title>` from `tabTitle(pathname, host, name)` in
`components/shell/nav.ts`: "Files · Just Dashboard", the product's alone for an address under no section.

**The item leads.** The address of a detail page names only its section, so every page of every database
read "Databases", every page of every project "Deployments" and a container "Docker", and four tabs or
the Back menu's entries could not be told apart. The page knows the item, so the page says it
(`components/shell/tab-title.tsx`, a small store outside React that only the `<title>` reads):

- `PageContext` hands the words of its `h1` to the tab as well as to a screen reader. The shell uses a
  heading only where the nav has no row for the address, so a list page keeps its rail entry's word and
  a detail page reads "nginx · Containers", "orders · Query · Databases", "shop · Logs · Deployments"
  (a project's page is named from `PROJECT_NAV`), "New project · Deployments", "Page not found".
- `useTabTitle("Deployment #42", "shop")` is for a page that wants more than its heading, most specific
  first; it outranks a heading and is used on any address. The run page uses it.

Two rail rows that share a word carry their section: "Alerts · Monitoring" and "Alerts · Proxy & TLS",
and likewise Traffic, Map, Security and Configuration. `nav.test.js` fails when two rows of the rail
would give their tabs one title. A word said twice is said once. `/login` is "Sign in · Just Dashboard"
and `/welcome` "Welcome · Just Dashboard".

On a server linked to a Just Hub, `host` is this server's own host name (`useHostName` in
`hooks/use-metrics.ts`, the Overview's title from `/system/host`) and goes after the page — "Docker · atlas · Just
Dashboard" — so five dashboard tabs can be told apart; unlinked, or before the name is read, the title is exactly
what it was. It is never the name the hub lists the server under: that is the hub's text, which a hub that is taken
over could make read "Session expired - sign in", and a title has no room to say whose
([protocol](../backend/hub-link-protocol.md) §8.10). The root layout declares no title, and `login/` and
`welcome/` have small layouts that do. Next's metadata writes over `document.title` when it resolves, and
beside a second `<title>` it would come first.

### The rail drills in

**The sidebar shows one list at a time: the list for where you are.** Opening Docker replaces the list
of everything with the list of Docker's seven pages, under Docker's own name and a control back out.
Until 0.6.7 the rail was one list whose section rows unfolded a second list beneath them — Docker and
Security open together was thirty rows deep — and because that rail could not be relied on to show a
section's pages, six section layouts also carried a strip of route tabs across the top of every page in
them. **Those strips are gone. There is no route-level tab anywhere in the product**; a switcher that
remains switches between views of one page, never between pages (see `components/tabs.tsx` below).

Which panel is showing is derived from the route, not remembered, so a pasted link opens with the rail
already inside the right section. The single piece of state is a look elsewhere without leaving the
page you are on — the step back out, or a group opened from the list — and it is dropped on the next
navigation. Three levels exist: the top-level list, a section, and one deployment inside Deployments
or one database inside Databases — or, inside a group, the group, a section in it, and that section's
pages (Monitoring → Processes → PM2; Administration → Proxy & TLS → Sites).

`components/shell/nav.ts` is the nav registry — `NAV`, `PERSONAL_NAV`, `ACCOUNT_SECTION`, `PROJECT_NAV`,
`PROJECT_SETTINGS_NAV`, `navMatches`, `navOwns`, `navCurrent`, `sectionsFor`, `navLocation`, and for the sections an
account hides `shownFor`, `hideableSections` and `sectionEntry` — so `shell/app-sidebar.tsx` and
`shell/command-palette.tsx` walk the same lists without a second copy, and neither imports the other. Items
may carry a `capability`, and every reader hides what the role cannot use. ⌘K covers every page and is
the one surface that still sees them all at once.

A section's `children` are its pages **including its own landing page, first and named for what it
shows** — "Browse", "Live", "Version", otherwise "Overview" — so every destination in a panel is a leaf
and "where am I" points at exactly one row. The palette skips the child whose href is the section's own,
which is how one array serves both.

**Which row is lit** is `navCurrent` in `nav.ts`, for a section's panel and for a database's or a
project's own alike: a row is current on its page and on the pages opened from it (a volume under
Volumes, a jail under Intrusion, a statement under a database's Performance), so an item's page two
levels down still lights one row. The exception keeps a parent and its child from both being lit: a row
other rows of the panel hang under (Docker's Overview at `/docker`, a database's Home, Metrics above its
Alerts) is the landing page and is current on its own address alone, unless it is the list its items
open under (`LIST_LANDINGS`: Live for `/processes/<pid>`, Projects for a project and `/deploy/new`,
Databases for a database), which keeps the addresses no other row owns. A run is one of its
project's Deployments. An address under a section that no row owns lights nothing. The lit row is
drawn two ways: the selection fill (`bg-accent`) and the medium label, with no bar on its edge
(removed in 0.9.1, [*No colour bars*](standard.md#the-operators-decisions)). The pointer gets the menu's hover fill (`bg-menu-hover`), so the page you are on and
the row under the pointer never look alike. It is said, too: a page's row carries
`aria-current="page"`, and a section or group you are inside, seen from a level above, carries
`aria-current="true"`.

A section with more pages than a reader scans as one list puts them under **headings**, as a project's
rail does with *Settings*: a `NavChild` names its `group`, `groupedChildren` (`nav.ts`) turns the list
into the panel's groups, and a section that names none is the single list it always was. Network is the
one that does, since 0.8.6: Overview above every heading, then *Setup* (Interfaces, Routing, Egress
groups, Firewall, Gateway, Protection, VPN, DNS, Address planning), *Activity* (Traffic, Socket history,
Connections, Drift) and *Diagnostics* (Tools, Packet captures, External checks, Saved
runs; Connection path left the rail in 0.8.7, when it became a tool of the Tools workbench). It was
nineteen siblings with four glyphs each used twice. No two pages of one section share a
glyph, which `nav.test.js` holds for every section: the glyph is what a row is found by before it is
read.

The group that holds Packages, System users and the Audit log is **Administration**. Until 0.8.6 it was
"Server configuration", a synonym of Settings → Configuration two rows under it for a group whose audit
log configures nothing. A button that opens a section says the rail's name for it in sentence case
(*Open Deployments*, *Open Proxy & TLS*, *Open the database's Home*), never a shorter word the rail
does not use. Excalidraw stays the page's name, the tool's own, and a document made there is a
*drawing* everywhere; "board" is only the API's path. Pages of different sections that share a name
(Traffic, Alerts, Map, Configuration, Security) are told apart by the panel they stand in and by the
trail the palette prints beside each.

A **group** (`NavGroup`) is a row with a chevron and no page: Monitoring holds Metrics, its Alerts
(administrators only), Processes and Logs, and Administration holds Proxy & TLS, Packages, System users and Audit log. There is nothing
that is "Monitoring" as such, so pressing the row opens its panel over the page you are on rather than
navigating, and a group owns a path only through its entries (`navOwns`) — Metrics, Processes and Logs
share no prefix. `sectionsFor` returns the chain of groups and sections a path sits inside, and the rail
draws one panel per link of it; the breadcrumb names every link (`navLocation().parents`).

### Sections an account keeps out of its sidebar

**Hiding a section is about the rail, never about access.** Every account made from 0.7.3 on is asked
once, straight after sign-in, what it will use the server for (`/welcome`, below), and the answer is
a list of sections kept out of its sidebar — stored on the account (`users.hidden_sections`, served as
`user.hiddenSections` on the session and written by `PUT /account/workspace`), so it follows the person
to every browser rather than living in this one's storage. `lib/sidebar-sections.ts` names the
sections that can be hidden — the first segment of their path: Deployments, Databases, Docker, the
six Workspace tools, Network, Proxy & TLS, Security, Backups, Packages, System users and the audit
log — and the purposes the question offers, each the sections it brings. Every section belongs to
exactly one purpose or to the base, so every card turns on something no other card does: Ship apps
is Deployments and Proxy & TLS, Run databases is Databases and Backups, Work on the server is the six
Workspace tools, Manage containers is Docker, Secure the server is Security and Route the network is
Network. Until this was a partition, Manage containers brought nothing the other purposes had not,
and Route the network and Secure the server brought the same two rows. Overview, the server map,
Monitoring, Settings and the account cannot be hidden: a rail without them has no way to say how the
machine is, or back to the switch — and the map is about every section at once, so no answer is
without it. Packages, System users and the audit log can be, but every answer keeps them
(`BASE_SECTIONS`): they are how the machine itself is changed and checked, whatever it is for.

What a hidden section loses is exactly three things, each read through `useHiddenSections()`:

- **Its row in the rail.** `shownFor` in `nav.ts` decides every row (and asks the role, so a group
  whose remaining entries the role cannot open goes with them — Administration with Packages
  hidden, to an account without `system.admin`). A section's root is hidden, never a page inside it.
  **While you are inside a hidden section it is drawn anyway**: a deployment's domain links to
  `/proxy/sites/<name>`, about 150 such links cross sections, and the rail must still say where you
  are. It says why the row is there, too — `HiddenHere` in `app-sidebar.tsx` puts one line under the
  panel's head, "Proxy & TLS is hidden from your sidebar. Show it", and Show it removes the section
  from the list. The line is in the rail rather than over the page so a `fill` page (the terminal, a
  database workbench) keeps its exact height; on a phone it is in the sheet.
- **Its tiles, blocks, shortcuts and reads on the Overview.** Each Services tile, each block (Deployments,
  Terminal, Drawings & boards, Databases, Backups), each shortcut and the read behind it are gated on the
  section, so the page asks the server only about what it draws.
- **Its pages in the palette's empty list.** They are `explicit`, listed as soon as a word is typed:
  search is how a hidden page is reached on purpose.

No route, link, embedded pane or permission changes. The links into a hidden section are left as they
are on purpose — wrapping them in "can I go here?" checks would make every missed one a dead end.

### The first-run question

`app/welcome/page.tsx` sits outside the shell — the rail is what it decides — and the shell's layout
sends an account whose session says `needsOnboarding` there from any address, after sign-in, two-factor
and any forced password change; answering or skipping clears the flag server-side, and it is never
asked again. It is a flow page (design system §16, `components/onboarding/welcome-flow.tsx`) of two
screens, centred on arrival's ground (`.auth-backdrop`, design system §16), the one sign-in stands on:

1. **The question**: the purposes as big `ChoiceCard`s and nothing beside them — any number, each
   drawn as the three products it means (`PURPOSES[].products` in `lib/sidebar-sections.ts`) and its
   name. Five are stable — finished and tested — and come first, chosen to begin with
   (`STABLE_PURPOSES`). Route the network works but is still being tested (`stage: "experimental"`),
   so it comes last, carries `ExperimentalMark` (an amber *Experimental* tag and *Working, still
   being tested*) and is taken only by somebody who presses it; stable is the rule and carries no
   mark, so there is no shelf of one card. On a server that already has projects or saved databases,
   those cards carry the count. *Continue* saves the sidebar they keep (`hiddenFor`, which always
   keeps `BASE_SECTIONS`), so the defaults leave out only Network; *Show me everything* hides
   nothing and goes straight to the dashboard.
2. **The answer**: `ServerPicture` (`components/onboarding/server-picture.tsx`) filling the screen —
   this server's rack in the middle, named and marked with its distribution, each purpose chosen a
   card of its products around it on an ellipse, with an `AnimatedBeam` running out to each — and one
   centred command, *Go to dashboard*, over a line saying how many sections the sidebar keeps and
   that Account → Workspace changes them. The picture is capped by the window's height so the command
   is on screen at 800px; no card sits straight below the server, where its name hangs. On a phone
   the cards sit in two columns under the server.

Until 0.7.4 it was three steps: the purposes beside a small picture of the server, then the sections
as fifteen tiles beside a preview of the rail, then the outcome. Choosing sections one by one is
Account → Workspace's job, after the fact, and the first sign-in no longer asks for it. On a phone the question's cards are rows with the commands under them, as on a
wide screen: a bar holding them at the foot of the window drew a dark strip the wide screen does not
have.

Nothing is saved until *Continue* or *Show me everything*. `/account/workspace` is the answer after
the fact (`components/account/workspace.tsx`), and nothing on it is framed: one line that says
where the answer stands (*15 of 15 shown · 6 purposes*), then one centred settings column (design
system §7). *What you use this server for* is the question's purposes in its order as a grid of lit cards
(`PurposePicker`), each its three products, its name — with the same mark on Route the network —
and the sections it brings with the ones out of the rail dimmed; *Ask me again* goes back to `/welcome`. Only sections are stored, so a
purpose is chosen when every section of it the role is offered is shown (`purposeShown`); pressing a
chosen one takes its sections out and pressing any other brings them back whole (`pressPurpose`).
Purposes share no section, so a press never moves another card. Under it, *Sidebar* is the rail's entries (`SidebarEditor`), walked
from the same registry in its groups and order as full-width switch rows on hairlines, one for every row the role may hide;
Overview, Map, Monitoring and Settings say *Always* instead, and Administration heads the three
sections it holds. A list of sections beside a separate picture of the rail was the same list twice.
Both edit one draft, and an edit raises the settings pages' floating Save (`SaveBarProvider`,
design system §7) rather than a disabled button sitting under the page. Accounts that predate the
question default to answered with nothing hidden, so an upgrade changes nobody's rail.

### Signing in

The dashboard Users page derives missing-factor descriptions from the same `require2fa` policy.
On an optional install, an enabled unenrolled account is password-only. On a required install,
its card and the coverage reading say enrollment is required before completing sign-in, and a
two-factor reset explains that requirement. Enabled/disabled account counts describe account state;
they do not promise that an unenrolled account can bypass the sign-in enrollment step.

`app/login/page.tsx` is one `FlowPanel` card: the form, and from `lg` a picture half drawing
`ProductOrbit` (`components/onboarding/product-orbit.tsx`) — the same server rack, ringed by the
products the dashboard drives with a beam running out to each, so sign-in and the first-run question
are one drawing. Nothing in it is read from the server, because nobody has signed in. On a phone the
picture is a strip across the top of the card. The spine names only the steps the account has, from
the server's answer (`needsTotp`, `needsEnrollment`, `user.totpEnabled`, `user.mustChangePassword`), and
is not drawn before that answer: it used to promise *Verify* to every account. *Forgot it?* shows
`scripts/reset-password.sh` with the typed name filled in, since recovery happens on the host. A forced
password change does not ask again for the current password the page already holds, reads the server's
rule as the reader types (`lib/password.ts`), and signs straight back in with the new password once the
change has ended the session. *Sign in with a passkey* sits under Continue, and the username field
(`autocomplete="username webauthn"`) also lists this dashboard's passkeys in the browser's autofill where
conditional mediation is available (`lib/passkeys.ts`); the autofill's request is withdrawn when the
button asks out loud. Either way the server's answer moves the page on exactly as a password does, so
an account with an authenticator lands on the code step. Opened at an IP address, at plain http off
localhost, or in a browser without WebAuthn, the button is replaced by the reason
(`passkeyPlace`), since a passkey needs the dashboard's name.

`components/shell/nav-scope.tsx` is for the two panels the route cannot describe. A section calls
`useNavScope(...)` and the rail draws what it publishes: one database's layout
(`app/(dashboard)/databases/[id]/layout.tsx`, through `database/shell/nav-scope.tsx`), because the
connection's name and which pages it has depend on its engine — the engine registry
(`database/engine.ts`) says Redis has Keys and a Console where a SQL server has Data, Query, Schema and
a Diagram — and its rows carry the reader's place (`?schema=&table=`) from page to page; and
`deploy/project-shell.tsx`, because a project's name and its mark (its favicon or product), its
game-only pages and which of its settings pages hold a saved change that is not live yet
(`PENDING_KIND_PAGE`) are not in the URL. Both sit a level *below* their section (`/databases/<id>`
under Databases, `/deploy/<id>` under Deployments), so the back control leads to the section. The rail
draws a project's rows from the route alone until that registration lands, so the panel does not reflow —
only the name fills in. It draws a database's from the route and what the tab remembers of it
(`useSessionState("databases.known")`: by id, the name from the last connection list and — once its pages
have been open — the flavour and capabilities its summary answered with), so a database opened before in
this tab is its own panel at once, with exactly the pages that then register. One the tab has not opened,
or an id the list never held, gets none until its layout registers one: a saved row knows only its
driver, and a rail of MySQL's pages for a MariaDB server, or of any pages for a database that does not
exist, is worse than the section's panel standing a moment longer. The memory is the tab's and not the
browser's because connection names are the server's data and must not outlive a sign-out. The
alternative was the rail polling the driver catalogue and a project on every page in the
product to draw a list the page beneath it already holds. A project's run page keeps the project's
panel, with Deployments marked as where you are, because a run is opened from there.

**Which database is the path, and only the path.** The section's static children in `nav.ts` are the
three pages about every database at once — Databases (`/databases`), Map (`/databases/map`) and
Add a database (`/databases/new`, `system.admin`) — and one database's pages are
`/databases/<id>` and `/databases/<id>/<section>`, built by `sectionHref` in the engine registry and
never by hand. `database/shell/routes.ts` holds the one spelling of an id (`DATABASE_ID`, digits with
no leading zero), which the layout, the rail and the palette all read, so `/databases/007` is not
database 7 to one of them and an unknown address to another. An id no saved connection has is
"Database not found" with a way back, never some other database; any other path under a database is
"Page not found" inside its shell (`[id]/[...missing]/page.tsx`), rail and header kept, drawn with
the database's engine. A database's
panel has Home alone and then Work, Schema, Insights and Operate (`database/shell/nav-groups.ts`),
each holding only what the engine has, in the engine's words. The connection used to be a query
parameter (`/databases/browse?conn=3`), and those addresses are still out there — a board stores the
link of every database card drawn on it, and a bookmark outlives every release — so a segment that is
not a number is read by `legacyDatabasesHref` and replaced, not pushed: `overview` is the home,
`browse` Data, `structure` Schema, `find` Search, `monitor` Performance, `server` Access,
`connection` Settings, `diagram`, `query`, `backups`, `logs` and `generate` themselves,
with `schema`, `table`, `sql`, `source` and `view` carried over, and `/databases/topology` is the
map. An old page with no valid `conn` opens the control center rather than a guess.

A scope's head is not a section's. A section's name is one of this product's words and is drawn as an
eyebrow, the rail's label voice; a project or a connection is a name somebody typed, and small caps
turned `api-production` into API-PRODUCTION, so a scope's head (`named` in `app-sidebar.tsx`) is printed
as written at `text-title` semibold — a step above the rows, so the name reads as what the rows belong
to rather than as one of them — after the scope's `mark`, the thing drawn as itself at the rows' icon
size, a project's favicon or product (`ProjectMark size="xs"`), a connection's engine
(`ProductGlyph`). The mark rides along with the rest of the scope but is not what decides a republish,
so it is derived from the same data as the title. A scope carries no `icon`: a glyph in a
row's slot made the heading read as one more row to press.

The groups run in the order a day on the server runs, and the rail names each group:
**Server** (Overview, and Monitoring: Metrics, Processes, Logs), **Apps** (Deployments first,
then Databases, Docker), **Workspace** (Excalidraw, Terminal, Files, Git, Remote Desktop), **Protection** (Security, Backups),
**Advanced** (Administration: Proxy & TLS, Packages, System users, Audit log) and **System**
(Settings). The top-level list is twelve rows rather than seventeen: the three monitoring pages answer
one question, and the four configuration pages are opened to change the server rather than to use it.
Deployments open the second group because shipping something is the reason most visits happen; until
0.6.7 it sat fourth in a group called Operations, between Packages and Backups.

Above the account card sits the update notice (`components/update/update-notice.tsx`), and only when
there is something to say: a newer version, an update running, or one that finished and has not been
dismissed. The rail imports only its slot (`update-notice-slot.tsx`), which reads the shell's one update
poll and fetches the notice's code when `updateHasNews` says there is something to draw, so the notice,
its progress and its confetti are in no route's chunks until then. The deployment vocabulary it draws
with still loads with the shell, through the command palette's inventory
(`command-search/inventory.ts`). It is the account card's container (the same edge and ground, tinted green or red only
once a run has finished) with the dashboard's J,
an eyebrow in the notice's tone, the installed version wired to the newest with a pulse running
along the wire, the newest release's title and a band of what the releases bring by kind. An admin on
an install that updates in place gets *Update now*, which opens the update's own dialog
(`update-dialog.tsx`: the two versions, *Not now* and *Update*, nothing else); everyone gets a link to the Version page (`/dashboard`) for the notes. While an
update runs the card carries the border beam, the stage it is at and *View progress*; afterwards it
turns green with *See what's new*, or red with the updater's error and *See what went wrong*, and only
then can it be dismissed. In the icon rail it is one tile in the same tone that links to the Version
page.

### A server linked to a Just Hub

A dashboard linked to a Just Hub ([protocol](../backend/hub-link-protocol.md)) is one of several, and the
shell says so in four places. All four are drawn from `HubLinkProvider` (`hooks/use-hub-link.tsx`), which
the dashboard layout puts above the tab's title and the shell, and **only while `/auth/session` carries a
`hub` field** (§8.3) — present only for a fully signed-in session on a server with a stored hub link. With
no field the provider asks nothing and `useShellHub()` is `null`, so an unlinked server's rail, strip,
palette and title are what they were, request for request. Linked, it reads `GET /dashboard/hub` every
fifteen seconds (the hub's default heartbeat) for the fleet, the notices and the freshest reachability, and a
status that says the link is gone (a disconnect from the terminal, a removal in the hub) takes the hub away
again. The browser never talks to the hub: everything is this server's cached copy of the hub's last
reply, so the CSP is unchanged. `lib/hub-link.ts` holds the pure half and its Bun tests.

An **idle** link (the session's or the status's `idle`, §8.2, §8.3) keeps the hub's name, fleet and notices, but
`shellHub` drops the hub's address and every notice's link: after "the hub's machine changed" that address answers
on a machine this server refused, and a look-alike there could ask for the hub's password. So the switcher has no
**Open Just Hub** and says *Link idle* with a red dot, the palette's hub opens nothing and says why, and no notice
offers *Open in Just Hub*.

- **The server switcher** (`components/hub-link/server-switcher.tsx`) takes the logo row's place: the J,
  this server's own host name where the wordmark was (as in the tab's title, never the hub's name for it,
  which the menu shows under its attribution), and a chevron. Its menu says under its label who named the
  servers, *Listed by Just Hub (hub)*, since every name in it is the hub's text
  ([protocol](../backend/hub-link-protocol.md) §8.10), then lists every server the hub holds, this one
  first with the installed version the wordmark carried and marked *this one* (a link to the Overview),
  the rest by name with a `StatusDot` in the hub's state (online green, late amber, offline red,
  updating grey; the word is in the row's accessible name) and an arrow (the hub sends no version for
  another server, §6.1), then **Open Just Hub**. Another server is a real link to its origin, and it and
  the hub open **in a new tab** with `noopener noreferrer` (§8.10): the address is one the hub handed
  over, so it never takes this dashboard's tab and nothing of this dashboard's goes with it. A server
  with no reported address is listed, disabled, as *no address*. While the hub cannot be reached the
  trigger carries an amber dot, the menu says *Hub unreachable*, and the others' dots go grey
  (*Unknown*), since what the hub said last may be minutes old. A link just approved whose first report
  has not gone yet is not unreachable, though the backend's `reachable` is `false` until one has: for an
  administrator with nothing failed (`firstReportDue`, `ShellHub.due`) there is no amber dot, this
  server's row reads *First report due*, and the shell reads the status every three seconds until the
  report has gone rather than every fifteen. Every address is checked again in the
  browser (`openable`: `https`, or loopback `http` for the test origin) before it becomes a link. On a
  phone the strip's wordmark is the same trigger and menu.
- **Servers in the palette** — a scope after Recent, offered only on a linked server
  (`searchScopes(hubLinked)`; unlinked, `server:` is plain text again). Each server is a result with its
  state and where it opens, found by name or address, its preview saying who listed it (*Just Hub
  (hub)*) and that it opens in a new tab; this one opens the Overview, another is opened with
  `window.open(…, "_blank", "noopener,noreferrer")` (the palette's own navigation refuses another
  origin, `localDestination`), and **Just Hub (hub)** is the last entry.
- **The host in the tab's title**, above.
- **The hub notice** (`components/hub-link/hub-notice.tsx`), between the update notice and the account
  card, built the way the update notice is and rendering nothing when there is nothing to say: the newest
  notice this browser has not dismissed: who says it, *Just Hub (hub) says*, and when, then its title and
  its body as plain text (it came from another machine, so it is never markup and never reads as the
  dashboard's own words, [protocol](../backend/hub-link-protocol.md) §8.10), *Open in Just Hub* with the
  host it opens on a line of its own, in a new tab with `noopener noreferrer`, when its link is on the hub's
  own origin, and
  *N more from Just Hub* leading to Settings → Hub's `#notices`. The account card's container, tinted
  amber or red only for a warning or critical notice. The rail is narrow and a hub's name may be 64
  characters, so the attribution wraps, the time going to a second line, and only the name is ever cut,
  with the whole name as its title: *says* is what marks the words below as the hub's and is never the
  part that is lost. Dismissals are kept in `localStorage` per browser and forgotten once the hub stops
  carrying the notice.

Settings → Hub (`/dashboard/hub`) is a `hubLink` entry in `nav.ts`: `shownFor` draws it only while the
server is linked, or while you are on it, and the palette lists it once a word is typed either way. A page's
preview in the palette lists its neighbours as the rail does, so an unlinked server's Settings pages do not name
Hub beside Connect, except in Hub's own preview.

`PERSONAL_NAV` is a flat list of leaves — Profile (`/account`), Security, Sessions, API keys, Workspace
and, for `system.admin`, Users — drawn three times from the one array: `ACCOUNT_SECTION`, which is that list as a
panel the rail drills into once you are inside `/account`; the palette's Account group; and the menu on
the rail's foot. Its button is a neutral control with a circular picture, display name and role,
with no chevron. Above the card the menu is exactly the card's width (Radix's trigger width),
so it reads as the card opening rather than a panel overhanging the rail. It
opens with a small picture, display name, sign-in name and a role `Tag`, then the six pages
(Security carries a `Status` for two-factor) and a separate neutral Sign out row. It grows out of the
card on the product's ease, and its rows `rise` in an 18ms stagger starting from the row nearest the
card; the stagger is `motion-safe` because the reduced-motion rule collapses durations, not delays.
Menu rows are 32px on desktop and 44px on a phone, with the current page marked by `aria-current`, an
accent wash and a brand glyph. The card is named by the words it shows and then by what it opens:
"Operator, admin, account menu" (WCAG 2.5.3). Between the pages and Sign out is **Pause
animations**, a checked menu row that keeps the menu open when pressed: it is this browser's, not
the account's (`components/shell/motion-pause.ts`,
[`foundations/motion.md`](foundations/motion.md)). The picture is `components/user-avatar.tsx`: the stored image when
there is one, otherwise the display name's initials in a hue taken from the username (`lib/hue.ts`'s
`LANES`, so the same person keeps one colour in the rail, the users list and their profile). Pictures and
initials are circular at every account-avatar size and carry `data-slot="user-avatar"`, the design
system's identity exception to the pill ban. `InitialsMark` for a person drawn from a bare name
retains the compact mark radius used in commit lines and forge faces.

## Workspace commands and place

### Global command search

`components/shell/command-palette.tsx` opens with Ctrl/Cmd+K, the rail's search field (drawn as a framed field with
the shortcut in keycaps for the reader's platform — ⌘K on Apple keyboards, Ctrl K elsewhere, via
`lib/platform.ts`; the field is a well with the button and the keycaps side by side, not a button
holding both: a control is named by the words it shows, and inside the button the caps were words
it showed. The button's `::after` covers the well, so a press on the caps is a press on Search,
and the shortcut is said by `aria-keyshortcuts`), or the search button in the top strip a phone gets in place of the rail. An
empty search puts the
previous distinct destination first, then the other recent destinations, current page commands
and permission-visible navigation outside the sections the account keeps out of its sidebar, whose
pages are listed as soon as a word is typed. Enter on that first result switches back to the previous
place without a browser history traversal; native Back/Forward remain available. The bounded
12-destination history observes pathname and query changes from every navigation entry point,
keeps resource selections, and uses `useMemoryState` per account. Filter and tab edits update the
current place without displacing the previous page; switching a query-selected resource creates
a distinct destination. Reload and sign-out discard it.

`components/command-search/inventory.ts` projects explicitly selected metadata from eleven existing
authenticated GET routes: the deployment fleet, proxy vhosts, saved database connections, Docker
containers and stacks, Git repositories, systemd services, PM2 applications, backup jobs and Excalidraw
drawings.
The dialog mounts its index only while open, with at most four concurrent inventory requests and
an eight-second timeout per source. Closing aborts reads; reopening reads fresh inventory rather
than retaining deleted resources. Reads settle independently; a failed source contributes no
stale rows and is named as incomplete search with a retry control. Retry refreshes all inventories.
Backend route permissions remain authoritative. No aggregate backend route or database migration
is introduced. Database rows, arbitrary files, logs, credentials, environment values and board
scenes are outside this index; URL userinfo, queries and fragments are excluded from addresses.

### Ask about this page

Beside the rail's search field, and in the phone's strip beside its search button, **Ask**
(`components/assistant/ask-about.tsx`) begins a new chat on `/assistant` about the page it was
pressed on: `/assistant?about=<path and query>&title=<the page's own h1>`, which the page reads
once, begins the chat with, and lets go of (`agent/about.ts` bounds both as the server does and
refuses an address that is not the dashboard's own, since it is drawn as a link back). It is not
drawn on the assistant's own pages, for a role without `service.control`, or — except to an
administrator, who is asked there to turn it on — while the assistant is off. The palette offers
the same as *Ask the assistant about this page* once something is typed. There is no keyboard
shortcut for it: the shell has no registry of global keys beyond the palette's own.

`model.ts` supplies scope aliases, AND matching of query terms, accent/punctuation normalization,
ranking (exact name/address, prefix, substring, one-edit typo), result limits and recent-history
logic. `domain:`/`site:`, `db:`/`database:`, `container:`, `stack:`, `project:`, `repo:`, `service:`,
`pm2:`/`app:`, `backup:`, `board:`, `page:`, `command:`, `recent:` and — on a server linked to a Just Hub
only — `server:` narrow the same input; a
strip of scopes under the input — words as the Files search's quiet tabs (`tabClasses`), no
glyphs, `role="group"`, one tab stop with arrow keys between them — makes the types discoverable,
presses the scope a typed prefix names, and counts each kind's matches for the words typed. Global searches omit duplicate recent
entries; the Recent scope searches history explicitly. At most 60 results are shown with a
matching count and a suggestion to narrow when there are more. A narrowed search with no matches
offers to search everything for the same words. A section's landing page is listed once under the
section's name and also answers to the name the rail gives it inside the section (Deployments'
"Projects", Processes' "Live").

Every result is one line — its mark, its name, its second line in the hint size after it, and at
the far end its state, when it was visited, *Previous*, *Current* or its shortcut — so a screen holds
twice the rows the two-line list did. The highlighted result takes the menus' hover fill and nothing
else; it is where the keyboard is, not a selection. The mark is drawn as what the result is
(`command-search/marks.tsx`). An inventory row carries the product it is as a `ProductLogo` key — a
container its image's, a database its flavour or driver, a site nginx or Caddy, a repository its forge
or Git, a service the unit's program, a PM2 app its interpreter, a stack Compose's, a project
`projectProduct`'s, a B2 backup Backblaze's — drawn bare (`ProductGlyph`), and the pages that are one
product's own (Docker, Stacks, Git, PM2, Terminal) carry theirs. Everything else is its glyph as the
rail draws it, grey at rest and ink on the selected row. There is no hue per kind: the rows used to
stand on tiles tinted in seven `--tag-*` lanes, with the same hue on the group heading and the scope
chip, and a column of faded squares said nothing the heading did not — the icon plate §14 removed
everywhere else. The colour in the list is now the products' own. A recent destination is drawn as
the rail entry it is under, or as the resource it was opened as, with how long ago it was visited. A
container, service or PM2 app shows its running state as a `Status`; the words a title matched are
drawn in `--signal`, the search-hit colour (`highlight` in `model.ts`); page commands show their
shortcut as keycaps for the reader's platform.

From `lg` the dialog widens and the selected result is said in full beside the list
(`command-search/preview.tsx`), following the selection as the arrows move it: its mark on the
product tile, its kind, its name and state, then what the row had no room for. An inventory row
projects a few `facts` from the same explicitly chosen fields as the rest of its metadata — a
container's image, status, health and Compose service; a site's server and every domain; a
connection's engine, host, database and whether it is protected; a repository's branch and upstream,
folder, last commit and working tree; a backup's destination, schedule and folders — and never a
payload's secrets, environment, notes, database users or remote addresses. Each also names its
`links`: the domains, container names, stacks and folders other resources can know it by. A resource
sharing one exactly is listed as *Connected* — the site serving a project's domain, the container a
connection's host names, the stack and repository in one folder and the backup covering it — and a
relation that would need a guess is not drawn. A page shows the rail list it sits in with itself lit; a command
says what it does; a recent destination opened from search keeps the readings of the resource it
was. The pages and resources listed there open on a click and are out of the tab order, because the
combobox owns the keyboard and each is a result the same words reach. The preview's body takes
`animate-rise` keyed by the result, and rows that arrive as the words change rise the same way
(`useArrivals`); the list and the preview hold one height while typing so the footer stays put. A
phone keeps the whole width for the list.

Each resource result uses its existing detail address. Sites open `/proxy/sites/<name>` for
inspection rather than the legacy `?site=` editing link. Containers and stacks open their dedicated
pages, and a PM2 application opens its own (`/processes/pm2/<name>`, with `?account=` where two
accounts run one of that name), as one result per application (`pm2Apps`), never one per cluster
worker; Git and systemd retain query-selection links. Nothing in the product
writes an address that opened a sheet any more (the server map, the palette, a project's deep links,
the investigator's evidence and the pictures all name the item's page); every such address still
redirects, for the links already out there.
Names with the same spelling retain separate identities. The current database keeps the existing
engine-dependent pages and broken-connection restriction; saved databases also retain the
`open <name>` search phrase. Page commands, proxy commands and
account navigation retain capability filtering. Reload nginx and Sign out require an explicit
search or the Commands scope, so neither becomes the blank menu's default action.

cmdk keeps combobox focus, arrow movement, Home/End, wraparound and active-option announcements;
the selected identity survives independently arriving results. Its Ctrl+K vim binding is disabled
so the global shortcut also closes the menu. Escape first clears a non-empty search through
`PaletteModal.onEscapeKeyDown`, then dismisses the dialog and restores focus. The scope strip and
the retry control own their keys. `PaletteModal` hangs from a fixed line near the top of the window
rather than centring, so the input does not move as the results grow and shrink. The shortcut ignores composition, repeats and additional modifiers.
Ctrl/Cmd+K is reserved in window capture so Monaco's chord and the terminal do not consume the
same keystroke before the launcher opens.

Pure ranking/history and metadata-projection contracts are tested with Bun in
`components/command-search/`; `tests/browser/command-search.spec.ts` verifies keyboard navigation,
real detail rendering with API fixtures, live refresh, partial failures, delayed reads, permissions,
database engine navigation and mobile bounds. The design and recording are in the
[command-search audit](../../audits/2026-10-04-command-search/README.md).

`components/workspace/` registers the active page's Find, read refresh, shortcuts and contextual
actions in the existing command palette. The shell marks its scroll region, and participating pages
restore row/field focus and scroll after navigation. Editors, terminals and portalled controls keep
their own keys. `SidePanel` returns focus through a workspace when the sheet's trigger is gone, and
otherwise, like `Modal`, to whatever opened it (`hooks/use-focus-return.ts`).
See [`patterns/keyboard-and-place.md`](patterns/keyboard-and-place.md) for the page contracts and storage rules.

## The editor is served from here, not from a CDN

`components/code-editor.tsx` wraps Monaco, and the line that matters is
`loader.config({ paths: { vs: MONACO_VS } })`, this origin's `/monaco/<version>/vs`. `@monaco-editor/react` otherwise fetches the editor from
`cdn.jsdelivr.net` at runtime — third-party JavaScript in the same origin as a session that drives the
Docker socket and a root shell, and a permanent spinner for an operator whose workstation has no egress,
which is the workstation this is meant to be reached from. `scripts/sync-monaco.mjs` copies
`monaco-editor/min/vs` into `public/monaco/<version>/vs` from the Bun `dev`/`build` scripts and explicitly in the
Dockerfile. The copy is gitignored and excluded from eslint.

**What `public/` serves is cached, and is not a page.** A file in `public/` has no hash in its name, so
Next answers it with `max-age=0`, and the proxy gave each one a nonce and a policy it never reads:
opening an editor was dozens of conditional round trips over the tunnel this product is used through.
The two copied runtimes therefore sit under the version of the package they came from
(`/monaco/<version>/vs`, `/excalidraw/<version>/fonts`), which `next.config.ts` reads from the
installed package exactly as the sync scripts do; it writes the two addresses into the build
(`lib/vendored.ts`) and answers those paths with `public, max-age=31536000, immutable`. An update that
brings another Monaco asks under another path. The logos (`/logos`, `/brand-logo`) keep their names
when redrawn, so they get a day and a week of `stale-while-revalidate`. `src/proxy.ts` leaves all four
prefixes alone (a drawing's own page, `/excalidraw/<id>`, still comes through), and
`static-assets.spec.ts` reads the headers off the built server.

**What the proxy leaves alone is still answered with a policy.** Where one of those prefixes names no
file, the shell answers with its own page, and a page the proxy never saw would go out with no policy
at all, free to be framed. `next.config.ts` therefore answers everything under the four prefixes with
the policy of something that is not a page (`default-src 'none'; … frame-ancestors 'none'`), which is
the one Caddy adds to an answer that has none, so the app answers the same with or without the proxy
in front and nothing in `deploy/Caddyfile` changes. A script, a stylesheet, a font or an image is not
governed by the policy it arrives with; Monaco starts its workers from blob URLs, which take the
page's policy, and the spec opens an editor to hold that. A new prefix in the matcher needs its line
in `headers()`.

Files exposes Monaco's Find, Replace, Undo, Redo, Format and Commands actions, indentation, word wrap,
minimap and font size. Models use the file path, search results reveal their matching line, and
`bun.lock` is recognised as JSON. The image editor bundles ISC-licensed `react-image-crop` 11.1.2
for resizable, keyboard-accessible crop handles and aspect presets; rotate, flip, resize, adjustments,
undo/redo, zoom and export remain local canvas operations. No editor loads a runtime from a CDN.

### Bundled sanitizer advisory review (2026-09-15)

Monaco 0.56.0, the current stable release at review time, includes DOMPurify 3.4.8 inside its prebuilt
AMD bundle. Updating only the transitive package does not change the JavaScript served to browsers.
Four advisories remain in `bun audit`; an exploitable path was not established in this integration:

- [Persistent configuration pollution](https://github.com/cure53/DOMPurify/security/advisories/GHSA-cmwh-pvxp-8882)
  requires `setConfig()` and a hook mutating its allowed-attribute map. Monaco uses per-call configuration
  and does not write that map.
- [Custom-element hook bypass](https://github.com/advisories/GHSA-c2j3-45gr-mqc4) requires
  `CUSTOM_ELEMENT_HANDLING` and an `afterSanitizeElements` enforcement hook. Neither is configured.
- [Trusted Types policy persistence](https://github.com/advisories/GHSA-vxr8-fq34-vvx9) requires a custom
  policy followed by `clearConfig()`. Monaco requests trusted output, but supplies no custom policy and
  does not call `clearConfig()`.
- [Detached subtree with in-place sanitization](https://github.com/cure53/DOMPurify/security/advisories/GHSA-55q2-fjhq-7xh7)
  requires `IN_PLACE`. Monaco does not enable it.

The application wrapper exposes no sanitizer configuration to API or editor content. Database result
cells render as React text, and SQL schema completions supply string labels and insertion text, without
trusted Markdown or HTML. Preserve these boundaries when extending editor features. Upgrade Monaco when
its shipped bundle contains the patched sanitizer, then recheck the copied runtime and `bun audit`.
