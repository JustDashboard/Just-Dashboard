# Frontend feature map

The App Router keeps route entry files thin where a feature has reusable panels and colocates page-only
orchestration where splitting it would hide the workflow. Backend capability checks remain authoritative;
hiding a control through `useAuth().can()` is affordance only.

## Route areas

One section per route area: what its pages are responsible for, then, under **Implementation**,
the files that own it and the specs that check it.

### `/hub/enter`

Where Just Hub opens this server with a sign-in ticket in the fragment (§9.5). Not a page of the
app: a document written by hand with its style and mark inline, on the sign-in page's dark ground,
whose one script takes the ticket out of the address first. It reads *Signing you in*, asks
*Continue through Just Hub?* with **Continue** and **Stay** when the browser is signed in as another
account, and the same without a name when it cannot tell who is (the session read answered neither
`200` nor `401`), sending `replace` only after **Continue**; on a refusal says *Could not sign you
in* with a sentence of its own for the refusal's code (`HUB_ENTER_REFUSALS`, naming **Sign in on
this server** rather than the API's "sign in here") and **Sign in on this server** (`/login`); it
never retries

**Implementation:** `app/hub/enter/route.ts`, `lib/hub-enter.ts` (unit-tested); checked by
`tests/browser/hub-signin.spec.ts`

### `/login`

One card on arrival's ground: the form (password, TOTP challenge, enrolment where the install
requires it, recovery codes, a forced password change that signs straight back in, and
partial-session states) beside the product orbit from `lg`, a strip of it on a phone; the spine
names only the steps the account has; *Forgot it?* gives the host's reset command; where the page's
Permissions-Policy forbids passkeys, the reason in place of the passkey button; a signed-out browser
the shell sent here carries the page it asked for as `?next=` and goes back to it once signed in, so
a link from an alert still ends on its page the morning after the session expired;
`returnAfterSignIn` in `lib/sign-in-return.ts` rebuilds that address from checked parts (a plain
path whose first segment is a page behind the sign-in, `tab` anywhere, `source` on Logs, `system` on
the map, and `connect` on the hub's Connect door `/dashboard/hub?connect=<hub host>`) and drops
every other key, so a `next=` someone else wrote cannot prefill anything; anything that fails a
check, a sign-out, and a session that ended under the reader (`?reason=session`) sign in to the
Overview as before

**Implementation:** `src/app/login/page.tsx`, `components/onboarding/product-orbit.tsx`, auth hooks,
shared logo/state/controls; [shell](shell-design.md#signing-in); checked by
`tests/browser/sign-in.spec.ts`

### `/welcome`

The first-run question, outside the shell, which sends a new account here after sign-in until it
answers. Two screens: what the server is for, as big `ChoiceCard`s drawn as the products they mean,
the five stable ones chosen in advance (Ship apps, Run databases, Work on the server, Manage
containers, Secure the server) and Route the network last, marked *Experimental — working, still
being tested*, every section belonging to one purpose only, with a count on the cards of what the
server already runs, and *Continue* and *Show me everything*; then the answer — this server's rack
in the middle of the screen, each chosen purpose a card around it with a beam to it — and a centred
*Go to dashboard*. *Continue* saves the sidebar the purposes keep (`PUT /account/workspace`); *Show
me everything* hides nothing. Every answer keeps Packages, System users and the audit log. Either
ends the question for good; Account → Workspace changes the sections afterwards

**Implementation:** `src/app/welcome/page.tsx`, `components/onboarding/welcome-flow.tsx`,
`server-picture.tsx`, `lib/sidebar-sections.ts`; [shell](shell-design.md#the-first-run-question);
checked by `tests/browser/onboarding.spec.ts`

### `/excalidraw`

Routes: `/excalidraw`, `/excalidraw/[id]`

Workspace Excalidraw drawings (`/boards` redirects here). The list opens on Excalidraw's logo and a
line of facts over the drawings as lit cards, each drawn from the server's preview of its scene and
sketched in on arrival, with the server items it links drawn as themselves. The editor is one
workbench frame held to the window (rounded and in the page's gutter from `lg`, the screen below
it) under a head bar: back, Excalidraw's mark, the name edited in place and the save state as a dot
and its words, then Add server item as the one command (Reload in its place while another tab's
change stops saving) and a menu holding Save now, Reload and Delete drawing; a failed save offers
Try again beside its words. A drawing that is not there, or an address that is no drawing's, is an
empty state with Excalidraw's mark and Back to drawings. The editor embeds
Excalidraw and autosaves to server SQLite with a revision guard, or by merging once it has joined
the drawing's live room, where everyone with it open sees the others' changes, pointers and names as
they happen; images are stored apart from the scene; the insert panel adds live cards for the host,
deployment projects and saved databases, each reading its resource now; YouTube, Vimeo and Figma
embed; the shape library is kept on the server and packs from libraries.excalidraw.com are
downloaded by it; Describe a diagram writes Mermaid through the assistant's provider; the menu and
the empty canvas are the dashboard's

**Implementation:** `components/boards/`, `lib/boards.ts`, `lib/board-*.ts`,
`lib/drawing-preview.ts`; [drawing contract](../boards.md)

### `/remote-desktop`

Routes: `/remote-desktop`, `/remote-desktop/new`, `/remote-desktop/[id]`

Workspace remote desktop. The overview opens on the computers' systems as their own marks and a line
of facts (how many, how many ready, open sessions, the tailnet) with the verdict beside the name, Add
a computer as the one command and Check every computer again in the menu after it, drawn while the
page loads;
then the picture of a session's path — this browser, this server running guacd, each computer wired
by whether its port answers and pulsing while a session is open; the computers as one-line rows (the
system's mark, the name over the server on it and `account@address:port` in mono, a state dot and
word, "43 ms direct · used 11m ago" as one line, Stream once paired, Connect and a menu; on a phone
the mark, the name over its state and reading, and the menu), and the
tailnet's desktops not added yet. A server without the engine opens on its install, run as a job.
An engine that is stopped, removed, shadowed by another container or unread is the missing-thing
screen as a block with its step (Start, Install, Open Containers, Check again), and Docker not
answering is Docker's own screen there, never the client's sentence.
The add flow asks which computer (the tailnet's desktops drawn as their systems, or an address),
says where Remote Desktop or Screen Sharing is switched on for that system and watches the port
until it answers, then saves the sign-in. A session is the terminal's workspace around the live
screen, edge to edge across the shell's content area with no measure, gutter or frame: the computers
down the left (put away from the top bar), a top bar of only the computer, how it is drawn, its
round trip, the keys a browser keeps, fullscreen with the keyboard and Disconnect, and the session's
column — the computer with its round trip coloured by how it will feel, the two ways to draw the
screen as their own logos (Control through guacd, or Stream, `?mode=stream`), the route the picture
takes as its three stops, and that mode's controls: the clipboard both ways, files into the Windows
drive and back as downloads and key combinations for Control; quality, the pointer and pairing for
Stream; the keyboard for both. Stream installs its bridge as a job, says how Sunshine is turned on
for that system while it watches port 47989, pairs and shows the PIN (held over the Control screen
while it is typed into Sunshine there), then frames the bridge's stream page in a frame of the
computer's screen's own shape (read off the black padding Sunshine adds round it in the 16:9 stream
and remembered per computer), with the padding drawn past the frame's edges rather than shown and
the stream never loaded again for a new shape, with the quality (Light, Smooth, Sharp, Max at
1440p120 and 80 Mb/s) and unpairing in the tools and the transport on its route (WebRTC on the
tailnet, else the WebSocket); a paired computer's row has Stream beside Connect. Below `lg` a
session is one pane at a time over the shared bar (Screen, Tools, Computers), the screen keeping
its connection and its size while another pane is up; from `lg` to `xl` the computers are a rail
and the tools a sheet; from `xl` all three stand side by side. Sessions are held
by the shell, so another page and back finds them still connected, with a dock at the corner listing
them (each as its computer's mark, how it is drawn and its round trip) while elsewhere, and the
rail's Remote Desktop leading back into the last one; the stream hides this computer's pointer over
the picture unless asked, and fullscreen locks the keyboard so Alt+Tab and the Windows key reach the
computer

**Implementation:** `components/remote-desktop/`, `lib/remote-desktop.ts`; [Remote Desktop
contract](../backend/remote-desktop.md)

### `/`

Host overview. While the dashboard itself has a problem (`GET /dashboard/health`: a full disk under
its data, a database that refuses writes or reads) a danger `Notice` says so first, to every role,
with the free space or the store's own error; a background task that panicked in the last half hour
is a warning `Notice` naming the worst one and counting the rest. For an administrator, until one
answers, the [usage count](../backend/usage-ping.md)'s one question comes first, with the report
printed in it. Then the machine's identity line, laid out as a project's header (its distribution,
processor and hypervisor drawn as their marks, kernel, uptime, processes; *Online* while the socket
is open, and *Restart needed* beside it while the packages owe one; **Open terminal** as its command
for an account that has the terminal, and behind `⋯` *Update to …* (the sidebar notice's dialog,
while there is a version an administrator can take), *Update N packages* and *Restart the dashboard*,
which goes to Configuration where the restart is confirmed and followed: there is no route that
restarts the machine); Shortcuts — the four things a visit starts, as lit cards
two by two and four across once the content is 42rem wide, each counting what it adds to and ending
in a glyph for whether it makes something or goes somewhere: New project, New terminal (in the
account's home), New drawing (made in place and opened) and Browse files, every one gated on its
section being in the sidebar and on the capability its destination needs, and those with a
destination real links; a Resources section headed by an All metrics link, holding five live
readings as `ReadingLine`s, each a link to Metrics (CPU with a bar per core, memory, load, network
and disk I/O: a glyph on the measurement's hue, which takes amber or red past its line, the reading
as words, what it is made of under it, its last hour as a sparkline from `sm`, each figure gliding
to every two-second frame; two to a row from `xl`) and then each real filesystem, up to four, as a
capacity bar with its free space and total (one disk beside the disk I/O line, more two to a row
under the lines). The page carries no `StatTile`. Until the socket's first frame the identity line
and the readings are bones at their heights and every block under them is already reading, so
nothing moves when it lands. Then the Deployments section
(up to six of the fleet's own project cards, three below `lg`, worst first, with each project's
product, address, traffic, run strip and a light round the edge while one deploys, a count of those
left out, New project and All projects, or the first-project empty state); the shells open on the
server (worst-first by work: working, holding a program, idle, ended; each drawn as the program in
it, its directory and windows, opening `/terminal?session=`) beside the drawings edited last, up to
four, each its own preview; the databases (the control center's fleet read, worst first, engine and
version, size and objects, environment and whether it answers, opening its home) beside every backup
job (a failed last run first, then running, then by next run, with its destination's mark, next run,
last run and Back up now for `service.control`); the top processes (the Metrics page's `RankList`,
by CPU, memory or I/O, each drawn as its product and opening `/processes/[pid]`) beside the last 24
hours of recorded events (shared audit
product marks, coloured source names, common action labels with literal targets, explicit outcomes,
exact/elapsed times and an administrator-only link to the full audit log); and one `ChoiceRow` per
module, two to a line from `lg`, each the product it is, one sentence and a state (unit-tested in
`service-words.ts`: *9 containers running, 2 stopped*) — Docker (with the products its running
containers are), proxy certificates (amber while one is late to renew, red once one expired),
security exposure, package updates and reboot state, System services (a failed unit leads the
sentence, in red), Git (its checkouts, what is uncommitted or behind, and the forges they push to),
Remote Desktop (its computers drawn as their systems and how many are open, for administrators) and
the dashboard's own version. A row is one height in every state: a bone while its read is out, *Not
on this server* for a module that is absent, *Its reading did not arrive* for a read that failed.
Each block, row and shortcut — with the read behind it — is left out for a
section the account keeps out of its sidebar, and two blocks that share a row take the whole of it
alone

**Implementation:** dashboard root page plus `components/overview/` (`shortcuts.tsx`,
`workspace.tsx`, `data.tsx`, `resources.tsx`, `services.tsx` with `service-words.ts`,
`section-link.tsx`, the unit-tested orders in `order.ts`, `activity.tsx` and `activity-event.ts`
(unit-tested), `readings.tsx` and `storage.tsx`,
`health-notice.tsx` with its unit-tested wording in `lib/dashboard-health.ts`),
`components/metrics/` (`host-identity.tsx`, `top-processes.tsx`),
`components/deploy/fleet-card.tsx`, `components/database/fleet/read.ts`,
`components/product-logo.tsx`

### `/map`

The server map (Server → Map, beside the Overview and, like it, never hidden): every system the
server runs drawn as its own diagram — this dashboard (its tailnet, address, stack, store and
backup), each deployment, each Compose stack nobody deployed, each PM2 app, a service something is
wired to, a container or a database on its own, and *Everything else* (what nothing claims, in rows
by kind) — found from the links the server knows and laid out left to right: where traffic arrives,
the thing itself, what it runs, what that keeps, what backs it up. Every kind is its own shape (the
deployment a large card, a domain rounded like a trigger, a database a cylinder, a volume a disk, a
container by its role — a web container's browser bar, a proxy's point), a release stack a frame
round its containers, a checkout, a certificate and the logs round sub-nodes under what they belong
to; what two systems share is drawn in each and marked. A rail lists the systems by what anchors
them and flies the canvas to one (`?system=` opens on it); choosing a box lights its wiring and the
inspector names each line; boxes drag and stay where they were put, and a whole system is carried by
its head, per account; the canvas moves like a whiteboard's (two fingers slide it, a pinch zooms, a
mouse drags open ground) and nothing on it changes size but by the zoom. No bar runs over the
canvas. Below `lg` it is the canvas alone, and a tap floats one *Open* button to the box's page.
Read-only: every box opens its own page. With no place left behind and every system at once too
small to read, it opens on the dashboard's own system; what a source did not answer for is one
line per cause in the inspector ("Docker is not answering: containers, stacks and volumes not
drawn", **Open Docker**); and the inspector's *On this server* is a row per kind that lights that
kind's boxes

**Implementation:** `app/(dashboard)/map/`, `components/map/`, `lib/server-systems.ts` and
`lib/server-map.ts` (unit-tested); [server map](../server-map.md); checked by
`tests/browser/server-map.spec.ts`

### `/dashboard`

The panel's own version: one identity line laid out as a project's header (the mark and the
installed version with the check's verdict beside it, the GitHub repository and branch it follows
and how far behind it is as the facts, *Check now* and a brand-faced *Update to …* at the far end),
what the update needs said first as a line each under it (that it wants something done by hand, with
*Read the notes* going to that release's own note; that the checkout carries uncommitted edits the
update keeps, with *Show* listing them in a popover), the upgrade while one runs, and the release
history as a searchable timeline. The sidebar entry is "Settings" — inside a product called
Dashboard, an entry called Dashboard said nothing — and this page is its "Version". It is also where
the sidebar's update notice leads: *View patch notes*, *View progress*, *See what's new* and *See
what went wrong* all open this page. *Update to …* here and *Update now* on the notice open the same
dialog, which asks and does not explain: the installed version wired to the newest, *Not now*, and a
brand-faced *Update*

**Implementation:** `components/update/` (`release-timeline.tsx`, `update-progress.tsx`,
`update-notice.tsx`, `update-dialog.tsx`, `update-art.tsx`), `lib/release-kinds.ts` (unit-tested),
`components/run-transcript.tsx`, self-update provider; checked by
`tests/browser/dashboard-ui.spec.ts`

### `/dashboard/configuration`

The panel's own settings. An identity line first, laid out as a project's header: the address it
answers at (scheme, host and port each in their hue) with Running or the restart's phase beside it,
the checkout with Compose's mark, its compose and `.env` files and the last restart as the facts,
and Restart and Rebuild behind its `⋯`, each with the `docker compose` command it runs and what it
costs. Then the stack as the path a request takes — this browser, the tailnet (or loopback, or the
network), Caddy on its port with the certificate's issuer, and Next.js and Go on their loopback
ports with a state dot — wired with pulsing beams that go still and amber during a restart, and the
run record under it with its stages and whole transcript, followed across the restart it describes.
Then the settings as one centred column of sections, the shape a project's settings have
(`components/settings-column.tsx` around `SettingSection`): Certificate (three cards with their
products' logos; picking one carries the address, listening interface and allowlist with it, filled
from the machine's own tailnet identity), Address, Access, Sessions, Features. Each head carries
what the section currently is and *Edited* while it holds an unapplied change; no sentence stands
under a field or a switch, the reason is behind the ⓘ beside its name, and a switch is one full-width
line. What `.env` holds that the running stack does not is the strip at the column's head
(`PendingStrip`), with the restart that adopts it. The allowlist is a list of networks drawn as
Tailscale, this server, local or public, with add and remove (the only loopback entry cannot be
removed); session durations are read back in words in the section's head and a value that is not a
duration is flagged. An edit raises the settings pages' floating bar, which names the sections the
edits are in and whose command is *Apply and restart*: it opens the same confirmation, listing every
change old to new, and sends the same request. Then the [usage count](../backend/usage-ping.md) as a
section of its own that says it applies at once, with no Apply or restart: its switch, the report as
exact bytes, any failure, a link to the totals and the pointer to Settings → Connect. Last, a Danger
zone in the danger rule with Uninstall, whose dialog asks whether the deployments and data are kept
for a reinstall or wiped — what each removes and leaves is read off the server, a wipe asks for the
host name and whether the backups go too — and whose run takes the top of the page, hiding the stack
and settings, until the dashboard stops and the record says what is left to do. Apply takes a
session this server opened: a session Just Hub opened is answered `403 hub_session_not_allowed`,
read as *Sign in on this server itself to do this* (`HUB_SESSION_REFUSED`), and an API key `403
session_required`

**Implementation:** `components/config/request-path.tsx`, `components/config/allowlist.tsx`,
`components/config/restart-progress.tsx`, `components/config/uninstall.tsx`,
`components/config/usage-ping.tsx`, `components/settings-column.tsx`,
`components/run-transcript.tsx`, `components/run-phases.tsx`, `hooks/use-self-config.tsx`,
`hooks/use-self-uninstall.tsx`, `lib/uninstall.ts` (`system.admin` only)

### `/dashboard/link`

Settings → Connect: linking this server to a site account. The sidebar names what the page does
rather than the site's domain, under the `Link` glyph (it wore Version's cloud). An identity line
first — the site as its name, a link to it, who the server is linked to, at which scope and when and
by whom (or its own key fingerprint while it is not), and at its far end the link's state with, once
linked, Sync now and Unlink. Under it the link drawn as what it is (`connect-picture.tsx`, the
deploy overview's wiring turned to face out): System (the distribution's logo), Readings, Containers
(Docker) and Backups wired into this server and out to the site's J with the account under it, each
kind's line saying what the last heartbeat read (*Ubuntu 24.04*, *CPU 23% · memory 61%*, *12
running*) or, before one, what the chosen scope would send (*Counts only* / *Counts and names*).
Every line pulses while heartbeats go out, the site's line holds still in amber when the link
stopped reaching the site, only the server's line pulses while a request waits, and every line is
dashed while nothing is linked. Then one section at a time under hairlines with 64px between heads,
the deploy settings rhythm. Not linked: **Choose what the site can do**, three `ChoiceCard` scopes
each with a mark of what it touches (an eye in its lane hue, Docker, the J) and the one line of what
the site may then do, Observe chosen, then **Link this server** at `size="lg"` with `sudo
./scripts/manage.sh link` beside it; then **What this server will send** at the chosen scope, one
thing to a line with its mark (the names Operate adds arrive as a line of their own), and **Never
sent** as a list. A request waiting for approval shows the code large and copyable inside a
`BorderBeam`, the minutes left over a `Meter` draining across the request's fifteen minutes,
**Approve on just-dashboard.com** (a new tab, `noopener noreferrer`) and **Cancel**, and the page
asks every five seconds so approving in the other tab flips it to linked. Linked: **Activity** — the
heartbeat and the last command as rows with their `Status` at the far end (the command in words,
drawn as what it acted on), the last failed exchange as a warning above them; the scope as the same
cards with an explicit **Change to …** command (a pick is not applied until it is pressed); what was
actually sent read back from the last heartbeat in the groups it was sent in, each group headed by
its product's mark and set in columns so a short group sits under the one above it, each list of
records (containers, stacks, alerts firing, backup jobs) a line per record led by its name with the
rest truncated, folded after five behind **Show all**; the automatic-update policy as a read-only
mirror of what the site set beside this server's key and the pinned site keys. No stat tile anywhere
(§15 pass 2 dropped as `/git` drops it; the page's doc comment names where each figure went). Unlink
asks first. A link request that ended without a link is said once, and dismissing it is remembered
for the tab. With `JD_ACCOUNT_LINK=false` the page says so and offers nothing, picture included.
Configuration carries one line pointing here

**Implementation:** `app/(dashboard)/dashboard/link/page.tsx`, `components/account-link/`
(`connect-picture.tsx`, whose `LinkPicture` Settings → Hub draws too, `link-panel.tsx`,
`status-panel.tsx`, `scope-cards.tsx`, `shared-fields.tsx`, `marks.tsx`, `policy.tsx`),
`hooks/use-account-link.ts`, `hooks/use-now.ts`, `lib/account-link.ts` (unit-tested; the scope
wording is the contract's table, the command kinds' words are `commandWords`); checked by
`tests/browser/account-link.spec.ts` (`system.admin` only)

### `/dashboard/hub`

Settings → Hub: this server's link to a Just Hub ([protocol](../backend/hub-link-protocol.md) §8).
In the rail beside Connect, under the `Hub` rack, only while this server is linked to a hub or while
you are on the page (`hubLink` in `nav.ts`), so an unlinked rail is what it was; the palette finds
it by name either way, and the hub's Connect button opens it as `/dashboard/hub?connect=<hub host>`.
Connect's page pointed at the hub, in the same reading register: an identity line (the hub's address
as a link, as text while the link is idle, its name said as the hub's, *Just Hub (<name>)*, the
scope, how many servers it holds once the hub has answered a report, when and by whom it was
connected — or this server's fingerprint while it is not — the link's state beside the name, said
there and nowhere else on the line, and at its far end, once connected, **Sync now** and
**Disconnect**);
Connect's picture with the hub at its end (`hub-picture.tsx` over `LinkPicture`, "Reports every 15s"
in the hub's own interval, the status's `heartbeatSeconds`); then one section at a time. Not
connected: **Connect this server to Just Hub at <host>** — the address field filled from `?connect=`
(kept through a sign-in when the hub's new tab is signed out) only when it is a bare host or, with
`JD_HUB_LINK_TEST_ORIGIN` set, that exact origin or its host (`connectTarget`; anything else fills
nothing), a notice that the address came from a link, under the field whose machine it is (`GET
/dashboard/hub/check` as it is typed or filled: *The hub's machine: hub.…, a machine of <owner>*, or
*tagged …*, *not checked*, the test origin, or the rule's refusal as a sentence, said there once:
while the check refuses the address — a look-alike name off this tailnet — Connect stays disabled
and the terminal's command is not offered), the three scope cards with `maintain` chosen when the
address is typed and none chosen after a `?connect=` link (Connect stays disabled until one is, and
**What this server will send** appears once it is), **Connect** and `sudo ./scripts/manage.sh hub
connect <url>` beside it, and one line on turning key expiry off with its link to the Tailscale
admin console; nothing is sent to the hub until Connect is pressed. No sentence stands under the
field or the cards: what the address has to be, that no scope gives the hub a shell, files or
accounts, and why key expiry matters are each behind the ⓘ beside what they explain. Connect's own refusal is *Could not connect* with the
backend's reason as a sentence; a hub that gave it no answer (`502 hub_unreachable`) is said as the
Activity section says a failed exchange, *Could not reach <host>: the connection was refused.*, with
Go's words behind *What this server said* (`exchangeFailure`). Waiting: the code inside a
`BorderBeam` to type into the hub (which never shows it), this server's fingerprint beside it (the
hub's approval screen shows it), the hub's machine (*not checked yet* when Tailscale could not be
asked, and at the test origin that none is checked), the minutes left over a `Meter`, **Approve in
Just Hub** (the request's `approveUrl`, a new tab) and **Cancel**; the page asks every three
seconds, so approving in the hub flips it and refreshes the session, which turns the shell
hub-aware. Approved, before the first report has gone, the backend answers `reachable: false` with
no `lastHeartbeatAt`; for an administrator with no `admin.link.lastError` that is the report being
due, not the hub out of reach (`firstReportDue`), so the line reads *Connected, first report due*,
the picture *First report within <interval>*, the report *Not yet*, the server count is left out and
the switcher shows no amber dot, and the page and the shell keep asking every three seconds until
the report has gone; without `admin` nothing tells the two apart and the link reads as unreachable.
Connected: **Activity** (the report and the last command the hub ran here, asked by whom, with a
link to every `hub.command.*` entry in the audit log; a failed last exchange as a sentence, *Could
not reach <host>: the connection was refused.* for a request that got no answer, with Go's own words
behind *What this server recorded*, `exchangeFailure`, and **Sync now** says *Report not sent to
<host>* with the same sentence), **From Just Hub** (`#notices`, every notice the hub carries, each
headed *Just Hub (<name>) says*, its link naming its host), the scope as the same cards with
**Change to …**, **What this server shares** (the last report, exactly as sent), and the
automatic-update policy beside this server's key and the pinned hub keys, with the line to compare
the hub key's fingerprint with the hub's Settings page and the pinned machine
(`admin.link.hubMachine`). Disconnect asks first and says the key is kept; afterwards it says
whether the hub was told (`DELETE /dashboard/hub`'s `hubNotified`): *Disconnected from <host>*, or
*Disconnected here* and that the hub could not be told and still lists this server until it is
removed there. An idle link — one this server stopped using because the hub's address fails the URL
rule now or another machine answers at its name, `idle` on the status (`isIdle`), its reason read
back from `admin.link.lastError` by `idleReason` — reads *Idle* in red at the identity line's far
end, says why in a danger notice at the top (*The link is idle*, the reason, and that nothing is
sent or run until a disconnect and a new connect), shows the report as *Not sent* and the picture's
hub as *Idle, nothing is sent*, offers no **Sync now**, and its Disconnect says the hub is not told;
without `system.admin` it reads *Idle* with no reason. While it is idle nothing on the page or in
the shell links to the hub's address, which may now answer on a machine this server refused: notices
keep their words and lose their links. A request that ended without a link, or a link the hub
removed (*Removed in Just Hub*, §8.9), is said once, a failed one with its reason as a sentence
(*The hub's machine changed: …* when another machine answered at the hub's name before the approval
was collected); `JD_HUB_LINK=false` explains the switch and offers nothing;
`JD_HUB_LINK_TEST_ORIGIN` is a warning banner. Without `system.admin` the status has no `admin` part
and the page shows the hub, the scope and the notices, with no control. Connected, every account
also finds **Sign in from Just Hub** (§9): off on this server (it names `JD_HUB_SIGNIN=true`), not
offered by the hub, or the account's own choice — what it costs said in full first (whoever takes
over the hub could open a session as this account; the Files page reads every file it reaches, and
with file changes or the terminal such a session is as good as root; what the dashboard's own forms
refuse it; that ending it does not undo what was done), then the hub user (shown when the hub offers
one, chosen when it offers several), the password and, with an authenticator, the code, and **Let
Just Hub sign me in**, an outline button; once chosen, the hub user and since which day, and **Stop
letting Just Hub sign me in**. The hub is named there as everywhere else on the page, *Just Hub
(<name>)*. On an idle link, for every account (`isIdle`), it offers no form and says sign-in is
paused until a disconnect, keeping **Stop letting Just Hub sign me in**. An administrator also sees
every account's choice as rows with **Remove**, which asks first. A `403 hub_session_not_allowed`
anywhere reads *Sign in on this server itself to do this* (`HUB_SESSION_REFUSED`, `lib/api.ts`)

**Implementation:** `app/(dashboard)/dashboard/hub/page.tsx`, `components/hub-link/`
(`connect-panel.tsx`, `hub-picture.tsx`, `status-panel.tsx`, `sign-in-panel.tsx`), Connect's
`scope-cards.tsx`, `shared-fields.tsx`, `policy.tsx` and `Keys`, `hooks/use-hub-link.tsx`
(`useHubLink`), `lib/hub-link.ts` (unit-tested); checked by `tests/browser/hub-link.spec.ts` and,
for sign-in, `tests/browser/hub-signin.spec.ts`

### `/account`

Routes: `/account`, `/account/security`, `/account/sessions`, `/account/keys`, `/account/workspace`,
`/account/users`

The signed-in account as six pages under one strip. Workspace: the first-run answer after the fact —
one line that says where it stands (*15 of 15 shown · 6 purposes*) with *Ask me again* back to
`/welcome` at its end, then one centred column: the question's purposes, Route the network marked
experimental, as a grid of lit cards that throw their sections, over the rail's entries in their
groups as full-width switch rows on hairlines, one draft saved through the floating Save bar. No page opens on tiles: each
figure the old readings rows gave is said by a picture, a chip or a verdict, and every picture
speaks the deployment pages' and Configuration's wiring vocabulary (`deploy/wire`, `AnimatedBeam`) —
a wire pulses while what it joins is in use, goes amber where something should be looked at, and is
dashed where it has never carried anything. The reads behind every wire, chip and verdict are
`components/account/access.ts` (unit-tested). Profile: the display name in the account's identity
line (`HostIdentity` with the picture as its mark — the sign-in name and role, member since, last
sign-in, the browser and network this session came in through drawn as themselves, for
`system.admin` the accounts as their faces linking to Users, and the two-factor verdict beside the
name); *Signed in as you* (`access-picture.tsx`): every session on the left as its browser or program with its system
in the corner, what it is doing over its name and its network and address under it, the account in
the middle as its face in the brand's ring with a green or amber shield for what guards it, and the
account's own usable keys on the right as the products that hold them with their role over their
name, every node at Configuration's scale, each side counted and linking to its page; then one
centred column of two sections: the profile (picture cropped and shrunk in the browser before
upload, display name and sign-in name as a draft on the floating Save bar with *Edited* in the
section head, whose line of facts is the rail's own account card drawn from the draft, and what each
field is for behind its ⓘ); and what the role can do as a ladder (`role-ladder.tsx`) — read-only,
limited, administrator, each rung's bar lit in its hue up to the account's role, each listing the
powers it adds as a glyph on the rung's tint, a name and the products it reaches (Docker, systemd
and PM2 for Start and stop), powers past the role dimmed, and the sentence each rung and power had
as its name's tooltip. Security: what a sign-in passes as
a picture (`sign-in-path.tsx`: the account, its password, the second factor and the server with the
sessions' browsers; without a second factor the factor is a dashed ring; an optional factor has an
amber bypass from password to server (dashed wires carry the amber on a phone), while a required
factor says enrollment is needed before completing sign-in and has no bypass), then one centred
column of four sections, each a title, one line of facts and its verbs — two-factor (*on · asked at
every sign-in · optional on this install*; enrol with the grouped secret, copy and
open-in-authenticator, recovery codes shown once with copy-all, regenerate after the current
password, turn off, why behind the ⓘ beside the verbs), then passkeys (`passkeys.tsx`: each a row
with when it was added and last signed in, whether it is synced, and the name it signs in at when
that is not the one open; Add a passkey on the title line asks for the password and, with an
authenticator, its code before the browser's prompt; rename and remove from each row's menu; at an
IP address, or where the page's Permissions-Policy forbids passkeys, the reason instead of the
button), then password (three empty fields, the server's rule lit as it is met, `lib/password.ts`;
typing a new one raises the floating bar, whose command is *Change password* and whose line says
what is still missing; the current password alone does not raise it), then where the account is
signed in, each session a row drawn as its client with its network and what let it in, with the
way to Sessions on its title line. Sessions: this device as an identity line; every session as the path it
takes (`session-map.tsx`: the browsers, the networks they came in on — Tailscale by its mark, a
local network, this server, the internet — and the server, a network's wire as busy as its busiest
session, a session owing its code dashed and one on a password alone amber; every node drawn at
Configuration's scale, a session with what it is doing over its name and its address and what let it
in under it, a network with what it is to the server); every other session as a row drawn as its
browser with its system in the tile's corner, the network its address is on (`lib/clients.ts`),
whether it passed a code and when it was last seen; sign out one or every other. API keys: one
identity line that counts the usable keys, by role in the role's hue and by how soon the first
stops, with New key at its end; *Where the keys reach* (`key-map.tsx`: every usable key as the product its name names — `keyProduct` — with
its role over its name, wired through the role it carries into this server's API, a role with no key
dashed, every node at Configuration's scale), then the usable keys as cards of one height, as Users
draws an account (a key opens nothing, so a card carries no arrow and its verbs are behind its `⋯`),
under chips that count and narrow (All, Used this week, Never used and Expiring soon in amber, each
while it has a match or is selected) and the revoked or expired ones as the same cards behind a
folded history, with the owner's face on
a key an administrator reads that is not theirs and the role in its rung's hue, the request that
uses a key coloured as a command, and a create dialog whose roles are cards and whose name field
shows the mark the key will carry. The list has search over name, prefix, role and owner, an
administrator's Yours filter, newest/last-used/expiry/name order, and chips for use older than 30
days or no expiry. The shared verb menu distinguishes Revoke from Delete record (an administrator's
inactive-only deletion, audited separately); Replace key is offered only for the signed-in owner's
keys, opens the create form prefilled at an equal or narrower role capped by the current account
role (an MCP key, drawn through an *MCP only* door, is replaced with the same databases and the same
reach over the deployments, its chosen deployments and generated tools included; a deploy key with
the same deployments), and keeps the previous key working until explicitly revoked after the new
secret is copied into the tool. An administrator's create dialog also offers the kind as two cards,
*API key* and *Deploy key*: a deploy key takes every deployment or a checklist of the fleet, says in
two short lists what it can and can never do, and after minting shows the `jd login` line, the jd
download and a link to the deployment API's OpenAPI description (`/api/v1/deploy/openapi.json`, for
generating a client, opened in a new tab so the secret stays on screen); its row says which
deployments it reaches, and the key picture draws it through a *Deploy only* door
(`components/account/deploy-keys.ts`, unit-tested). Seven-day expiry joins the existing presets with
a concrete expiry-date preview; the secret dialog stays mounted through the first-key refresh and
cannot close while creation is pending. With no keys the empty state says what one is for. Users:
the accounts as an identity line (their faces overlapping on the tile, how many, enabled accounts,
the administrators, the last sign-in; the verdict names missing enrollment under required two-factor
or password-only accounts under optional two-factor — amber for required enrollment or a
password-only administrator — a press narrowing the cards to exactly those — beside New user), then
the accounts as cards drawn by their faces that open the editor under chips for each role,
Enrollment required or Password only according to policy, Never signed in and Disabled, with role
and sign-in inline, policy-aware factor status and 2FA reset guidance and ordinary confirmation for
deletion in the card's menu; a new account's role is picked from cards

**Implementation:** `components/account/` (`access.ts` and its test; `marks.tsx`, the session, key
and account marks the pictures share; `user-avatar.tsx` is also the rail's picture and holds
`InitialsMark`, the face of anyone drawn from a bare name, and `AvatarRun`; `totp-secret.tsx` is
also the sign-in page's enrolment; `role-choice.tsx`), `components/client-mark.tsx` (a session's
client drawn as itself, shared with a deployment's requests), `lib/clients.ts`, `lib/password.ts`,
account layout/pages, auth hook, confirmation primitives; checked by
`tests/browser/account-ui.spec.ts`, `account-passkeys.spec.ts` (Chrome's virtual authenticator at
localhost) and `api-keys.spec.ts`; key filtering, ordering and replacement roles are unit-tested in
`components/account/key-list.test.js`

### `/audit`

An identity line saying how the last day went: what the server refused as its state ("1 of 10
failed", and "1 sign-in refused" in amber), then this server, the changes in the last day, the
people behind them as faces with the automated changes, and the sign-ins with where the last came
from; `?` and a `⋯` menu at its end holding Export as CSV and Refresh. Under it the day as one
strip, an hour to a stretch in the colour of how its changes went (green, amber where some were
refused, red where all were, the track where nothing changed), with the busiest sections on the
line under it and the pointed hour in their place (`components/audit/day-strip.tsx`, the model in
the unit-tested `day.ts`). Both are read from their own `since` query, on the hour, so a filter
does not narrow them, and both stand at their final height while the day is read. Then the
filtered, paginated trail as a framed table shelved by day, one line to a row at every width: the
time, the actor as a face (a key in the corner for an API key, with the key named — "· key
ci-deploy", its id in the tooltip — when the row recorded it; "· by the assistant" for the
assistant; "· through Just Hub as <hub user>" for a session a hub sign-in ticket opened, the
session's id in the tooltip, which is the `sessionId` of the `hub.signin` row that opened it; the
webhook's and the dashboard's own marks for machines) with its address as the network it is on, the
action with the mark of the section it touched, the target with any detail after it, and the
outcome's code in its family's colour. The action is the row's button and the keyboard's place on
it: it opens the entry whole in a popover (`entry-detail.tsx`), with the method and path in the
request log's hues, the key's record or the session's id, and whatever a one-line cell cut short.
Below `2xl` a row that came through a key or a session says that in place of its address's digits.
The user and action filters are on the frame's title line and one row of chips is under it
(Failures only, then the sections with their marks, which set the action filter); entries a poll
brings wait behind "N new entries · Show" beside the count and rise into place. Export as CSV
downloads what the filters match, every page of it up to 20,000 rows and none newer than the press
(`export.ts`, unit-tested; cells a spreadsheet would run as a formula are made text). An account
without `system.admin`, or one the server refuses, is told the log is for administrators and is
offered no filter

**Implementation:** audit page, `components/audit/` (`marks.tsx`, `day.ts` and `export.ts`
unit-tested); checked by `tests/browser/audit-ui.spec.ts` (fixture in `audit-fixture.ts`) and
`tests/browser/system-ui.spec.ts`

### `/backups`

Routes: `/backups`, `/backups/[job]`

Backup coverage, jobs, destinations, schedules, next runs and recorded outcomes. The page opens on
an identity line: how the jobs stand as its state (a job that failed, one being taken, every job
backed up, with paused ones after), the jobs, what they hold and the next backup as its facts
(`components/backups/readings.ts`, unit-tested), New backup as its command for an administrator and
a `⋯` menu holding Import from a destination… and Refresh. Manual runs,
restore, retention and job configuration keep their confirmed controls. A job may encrypt its
archives (on by default for S3 and B2): its card and identity line say so, an encrypted run carries
a lock on the rail and an Encryption block in its inspector, and the Backup key panel under the jobs
shows the server's key, what depends on it and whether a copy has been exported, with a confirmed
Export key… for an administrator (`components/backups/backup-key.tsx`, `encryption.ts`), and Import
key… for one exported elsewhere, listed beside it. The menu's Import opens Import from a destination
(`components/backups/import-dialog.tsx`, words in the unit-tested `import-model.ts`): this server's
destinations as choices or another typed in with its keys, then the archives it did not write
grouped by server and job, each with what it covers, its size, time and whether a key here opens it,
imported one at a time or a job's whole history, a missing key brought in place, and an optional
copy kept here; an imported job's card and page say where it came from rather than when it next
runs, offer only Test destination and Remove imported job, and its runs restore through the same
dialogs, the in-place one saying per source what it overwrites and what it makes first (a volume
through Docker, a directory) (`tests/browser/backup-import.spec.ts`). A job opens on its own page
(`/backups/[job]`, `components/backups/job-detail.tsx`): its identity line, its picture narrowed to
the one job (its middle the schedule over the next run), one reading as a sentence (what it holds
over how much is kept, with the archive sizes as its trend), and its runs as a rail beside the run
they show, each run one height with its archive's size as a figure, with each run's log, archive
and restores. Automatic overdue/attention recommendations were removed in 0.7.5

**Implementation:** `components/backups/`; backend in
[`../backend/git-backups-users.md`](../backend/git-backups-users.md)

### `/databases`

Routes: `/databases`, `/databases/map`, `/databases/new`, `/databases/[id]` (+ `/assistant`,
`/data`, `/query`, `/search`, `/schema`, `/diagram`, `/generate`, `/performance`,
`/performance/statements/[key]`, `/performance/tables/[name]`, `/performance/sessions/[pid]`, `/logs`,
`/access`, `/access/[account]`, `/backups`, `/alerts`, `/import`, `/settings`, `/settings/network`,
`/settings/resources`, `/settings/pooling`, `/settings/parameters`, `/settings/extensions`,
`/settings/version`, `/settings/danger`)

Every page under `/databases/[id]` opens on the database's header, as a project's pages open on the
project's (`shell/database-header.tsx`, drawn by `shell/database-shell.tsx`): the engine's mark, the
name as the switcher, whether it answers, one line of facts, *Open data* as its one command, Connect
and the verb menu. The exceptions are decided in one place (`shell/header-rule.ts`): Data, Query,
Schema and the Diagram carry that line as their own frame's head, and the page of one item (a
statement, a table, a session, an account) keeps a one-line strip over the item's own identity line.

The control center draws every saved database as a card, shelved by where it runs, its engine or its
environment (the table it could be laid across as, and its picture of what the databases feed, went
in 0.8.6). The map (`/databases/map`) is the server map's canvas with one diagram for each database
— what reads it, the database, the container it runs in, the volume that keeps it and the backups
that take it — its rail by engine, its arrangement kept per account under its own name,
`?database=<id>` opening on one ([server map](../server-map.md#the-databases-map);
`lib/database-systems.ts`, unit-tested). On a phone the map is one pane at a time, the databases, the
canvas and the inspector, picked from a bar at the foot (`MapCanvas panes`; the server map keeps the
canvas alone). The control center opens on the server's line with *Add a database* as its one
command; a card reads who is connected with its last hour and its newest dumps as a strip; and the
`⋯` menu at the line's end opens a Metrics export dialog (the
Prometheus URL, a metrics-only API key, a scrape config and how long activity is kept) and an MCP
server dialog (`fleet/mcp-server-dialog.tsx`: the MCP endpoint, the account's MCP keys with what
each reaches, a key made for chosen databases each to read or change — protected ones and non-SQL
engines listed with why — and for the deployments to read or deploy, every deployment or the ones
ticked (the deploy key dialog's `DeploymentChoice`) and whether it is offered every deployment route
as a tool (on for a new key, off for one made before the generated tools), shown once with Claude
Code's, Cursor's and VS Code's configuration, what a key reaches edited without a new secret, and
revoking; `tests/browser/database-mcp-server.spec.ts`). Engine-specific inventory and Home readings,
data/query/schema tools (Schema also writes, edits and drops functions, procedures and triggers on
the engines that have them, with the exact statement shown and the destructive capability the Query
tab asks for the same CREATE), generators, performance, logs, access, backups and settings.
Generate's *Seed data* card (`generate/seed.tsx`, gated on `seed`) says first what it does — made-up
test rows inserted beside the ones already there, amber on a connection labelled production — then
plans them for every table or the ones ticked — a tab per table in the order they are inserted with
its rows now and after, its first rows under two-line column heads naming each column with its type
and the generator filling it (changed there), and the statement a row is inserted with — and inserts
them behind `SqlReview` for a role that may change data on an unprotected connection; the preview is
every role's, and what was written, refused or left out is said table by table with a link to each
table's Data. Add a database (`/databases/new`) draws what is about to be made beside its catalogue
until an engine is picked (`connect/picture.tsx`): the engine as a mark not added yet, wired to the
container or the address and on to this dashboard. Home draws how the database is used (readers, what it holds, its dumps), one line each
for what it runs as, where it is reachable from and its dumps, every activity view as a line of
words with the chosen window's shape (the charts are Performance's; a stopped server keeps them),
and its server log followed live. Performance opens on who is connected
right now (each client as its program's logo, wired to what its sessions are doing) over the
recorded activity charts; Redis and MongoDB charts open on the recorder's last hour. Where
PostgreSQL never loaded pg_stat_statements, *Turn on statement statistics* (Performance →
Statements, and Home's busiest statements) is one dialog of three asked-for steps — the library
added to what the server loads at start, the restart, the extension — and a PostgreSQL server made
on `/databases/new` counts its statements from its first start, with slow-statement plans
(auto_explain) as an option. Logs' Queries view (`ops/queries-view.tsx`) says when the server
records no slow statements — Postgres's `log_min_duration_statement` off, MySQL's slow log off or
kept only in a file — with the statement that changes it to copy, and for an administrator a *Turn
on* that sets the same parameters through `PUT /databases/{id}/settings` (Postgres's reload
included) and reads the log again; a Postgres role that is not a superuser is told so rather than
offered a change it would be refused. Performance's Statement history view
(`ops/performance-history.tsx`) lists what each statement cost over the Overview's window, each with
its time drawn across it and ordered by the column pressed, and opens that statement's page on its
History: its time, calls, time per call (how long half, 19 in 20 and 99 in 100 took, where the
engine keeps run times) and rows. Access
opens on who signs in (each account wired to the server, pulsing while it holds sessions) over the
accounts with counted filters, one card to an account and one height in every state. **An account
is a page of its own** (`/databases/[id]/access/[account]`, `ops/account-page.tsx` and
`account-frame.tsx`; the sheet's `?account=` address redirects there): its line (mark, name, whether
a session is open as it, its tags, what its grants add up to), one command (a new password) with
the drop in the `⋯` menu, its views in the address (`?tab=`), and from `xl` the list beside it as a
rail in the filter the list was left in, walked by Alt+↑ and Alt+↓ with the view carried. A SQL
account has Grants (memberships and the privilege matrix, staged and reviewed as the server's
statements), Sessions (Performance's sessions narrowed to it, each a way to its panel there) and
Settings (its attributes on one save bar, sending only what changed); a key–value user has Rule
(the rule as the server holds it, and its commands, keys and channels) and Settings (the form that
rewrites it); a document user has its roles, with no strip. The name is one segment of the path,
written once and read back from the path (`ops/account-address.ts`), and a MySQL host or a document
user's database is `?host=`. The page asks before it is left over unsaved work, and a reader who
may not change an account is shown its attributes as words rather than as switches. Backups opens
on where the dumps go (the database and its schedule, this
server's dump tool, the directory) over the dumps table, then, for a PostgreSQL server the dashboard
runs, its point-in-time recovery: the moments it can be recovered to, how archiving is going, its
base backups and restore points, setup with the command line the server restarts with, a recovery
into a new server planned before it starts, and the recovered copies kept beside the server or
switched in for it. Import is a flow page (§16) for the engines that can restore under another name:
it asks where the database is now (the source's address as fields or a pasted string, kept in the
tab and never drawn back), says what stands in the way of a replication before its command, and then
follows the import by the state the server keeps — each table's copy, how far behind the target is,
the cutover behind a statement that the writes have stopped, or a streamed copy's progress — with
the earlier imports as `ChoiceRow`s beside it. Backups' Copy dialog can send a copy to another saved
connection of the same engine. Alerts opens on who is told (the database, this server reading it
every thirty seconds, the notification channels its rules tell, red while one fires) over the rules
as cards of one height (`ops/alerts-rows.tsx`: the kind's tile, the rule's name over its condition, and
where it stands in a column of its own with the reading against its line as a small mark; the status
opens what the server read, the card opens the rule's form) and what was
told lately; an administrator adds a rule for this database or every database from a sheet of the
kinds its engine can be watched for (server down, disk, connections, replication, a backup failed or
missed), with its line, how long it must hold, a cooldown and whether its recovery is told, and
sends a test from the rule's menu. Settings is a group of pages in the rail, as a project's are
(`shell/settings-pages.ts`), each one centred column under the page's one save bar, headed by a
strip of what the server has stored and not yet read (a parameter or a library waiting for a
restart, each named as the way to where it is changed, with Restart beside them for the role that
may interrupt the server; `ops/settings-pending.tsx`; unsaved edits join the strip while it is drawn
and never make it arrive): General (the address as the way the dashboard reaches it, then its
fields in place — host, port, account, password, database, or the whole string — the sealed string
read only once a field changes, every option it carries kept, and Save dialling the new string
before it writes it, `ops/settings-address.tsx`; the labels and protection), Network (who can reach
the port, through its container, drawn with what the firewall and the gateway do about it and amber
for a port only a password guards, and the four answers a container the dashboard owns takes — this
server, one of this machine's tailnet, VPN or local-network addresses, a list of allowed addresses
the gateway table keeps it to, anywhere — as segments with the chosen one's value under them, saved
by the bar and asked for first, the two that take a value with what the server says it will run,
the gateway's rules among it; whether the server speaks TLS with a certificate from the dashboard's
own authority, drawn as the connection with its lock, plain text allowed or refused as a field of
the same bar, with the CA download, a client connection string and the renewal;
`tests/browser/database-reach.spec.ts`, `database-tls.spec.ts`), Resources (its container's memory
and processor limits beside their peaks, the machine and the engine's own buffer, changed in place
or — to lift one — by replacing the container after asking), Pooling (PostgreSQL's PgBouncer: its
settings, its figures on the picture of the way a connection goes — clients and the ones waiting on
the line in, server connections on the line out, the wait for one at the pooler — where it is
reached, and its pools one a line; Connect shows the pooled string beside the direct one),
Parameters (every one under the engine's headings, changed in its row — a yes/no and a closed set
as their words, the rest a field — several at once by the one Save, one request each, with what ran
shown afterwards; `ops/settings-parameters-edit.ts`), Extensions or Modules (each drawn as the
product it belongs to, on one line of one height with Update as its button and Disable behind its
menu; a SQL engine's extensions by category with what each needs, searched and filtered, and on
PostgreSQL enabled with a version, a schema and CASCADE, updated along a shipped path, disabled
after listing what depends on it, and — for one whose library has to be loaded at start — load,
restart and create in one dialog, `ops/extension-preload.tsx`), Version (an administrator's, for a
PostgreSQL server Databases started: the release it runs with its image, container and volume as
its captions, the four steps of an upgrade as marks on the way to the newer release with the line
moving through the one being taken, a check of the newer one listed line by line, the upgrade
followed as a job, and the old server kept until it is finished or reverted) and Danger zone; what
else the server holds is Home's *Databases on this server* block, not a Settings page. A page the
engine lacks is not listed, and an address from the one-page Settings (`#danger`, `?q=`) is sent on
to the page that holds it, `#databases` to Home. Every connection has an Assistant beside its Home
(`database/assistant.tsx`, the deployment's page through the shared
`assistant/agent/subject-page.tsx`), for a role with `service.control`: a chat about this connection
on `POST /databases/{id}/assistant`, filling the window under the header with the connection's chats
and what the open one did in the rail, suggestions in the engine's own words (keys and commands on
Redis), and saved chats opened by `?chat=`. Query keeps the same chat — the same session — in a
column beside the editor (`query/query-assistant.tsx`), for a role that may run statements on an
engine the capability table offers it to: set up from the column by an administrator, the tab's
statement, last result (a small text table), error and plan attached by toggles and sent as
`attached` with their labels drawn as tags, a finished answer's statements opened in a tab or at the
cursor, and a failed statement or a plan asked about from where it is shown (`query/attachments.ts`,
unit-tested). A protected connection says on both that nothing about it is sent while it is
protected. Engine capabilities decide which operations appear; protected connections keep their
write restrictions. A PostgreSQL table's Schema view has a *Row security* reading (`rowSecurity`):
the switches and policies with a template for each common shape and the server's statement before
every change, and a *Try it as a role* test that runs one SELECT as another role in a transaction
that is rolled back; an account's grants on Access list Functions and Columns beside Tables. The
Query page's Plan view carries two index tools the reader presses for (`query/index-advice.tsx`,
`index-advice-model.ts`, the Advisor's opposite: asked for, never shown unasked): **What if?** on a
PostgreSQL plan, which proposes indexes for the plan's filters, joins and sorts, tries one as a
hypothetical index (HypoPG) and shows the cost before and after, and offers the real `CREATE INDEX
CONCURRENTLY` through the schema change dialog, and **Missing indexes** on SQL Server, over its
`sys.dm_db_missing_index_*` views. Automatic Home findings and the Advisor route/API were removed in
0.7.5

A statement, a table and a session each open as a page of their own under Performance
(`/databases/[id]/performance/statements/[key]`, `/tables/[name]`, `/sessions/[pid]`), on the
process page's shape (`ops/performance-detail.tsx`): the way back to the list, an identity line with
one command and a `⋯` menu, the views as an underlined strip with the open one in `?tab=`, and from
`xl` the list it came from as a rail with that list's own filter, order and narrowing
(`ops/performance-lists.ts` holds the keys both read), Alt+↑ and Alt+↓ walking it with the view
kept; on a phone the page is the one pane and its two arrows step. A statement
(`ops/performance-statement.tsx`) has Figures as `ReadingLine`s with the recorded window at each
line's end, Text, Plan and History; it is the one place the Statements view, Statement history and
the top statements on Logs open. A table (`ops/performance-table.tsx`) has its size as one strip,
its rows and how it is read, and its housekeeping and maintenance. A session
(`ops/performance-session.tsx`) reads the session list at the list's cadence, has its statement,
its timing and who it waits on or holds up, and its stop verbs; one that ends while open is said to
have ended and stays as last seen. The address is one path segment with no percent sign
(`ops/performance-address.ts`, unit-tested): what would be escaped is written with `~`, and a dot
inside a name is escaped so the dot left is the one between schema and name. The addresses that
opened the sheets these were (`?statement=`, `?object=`, `?session=`) are sent on to the pages. No
list of Performance draws a bar per row: a share is a figure in its own column, and what a view's
footnote said is a fact on its title line (`ops/performance-words.tsx`);
`tests/browser/database-performance.spec.ts`.

Data, Query, Schema and Diagram are four workbenches under one head
(`components/database/workbench/chrome.tsx`): the database's identity and the open thing's verbs in
the frame's own bar, the glyph-only controls as words in its one menu, and on a phone one pane with
a bar at the foot — Tables, Rows and Details on Data, where a row is a card and its full row the
Details pane (`data/row-cards.tsx`); Editor, Results and Saved on Query, where Run's menu holds the
row limit and the head's menu Format and Save. A schema's and a table's figures are one line of
facts, a table's Statistics are sentences, and the Diagram is zoomed and fitted from the maps' bar
at the foot of its canvas (no minimap).

Backups' dumps are one line each in a framed table (a card of two lines on a phone): when, the
file's name with its tag and note, what it holds, its size, and Download, Restore and Delete. The
name opens who took it, the tool that wrote it and how long that took. *Back up now* is the one
command on the title line; *Upload a dump* and *Copy database* are in the menu beside it. Where a
server's log is not archived, point-in-time recovery draws the path it would take
(`ops/pitr-picture.tsx`: the server, its write-ahead log, a directory, S3 or Backblaze B2) over one
sentence and *Set up point-in-time recovery*, or over the server's reason where it cannot be set
up. Import's plan names the source beside its logo with what it holds as one line, and its copy
draws the replication (`import/picture.tsx`, words in the unit-tested `import/model.ts`): the
source, the subscription and the database here, a line that moves while rows or changes are on
their way with how far behind the target is written on it. Generate draws each generator as the
project it writes for (`generate/options.ts` `logoKeys`; its language's mark where the project has
none), and before anything is written its place is that mark, "Nothing written yet" and Generate.
Search says a limit only when one was hit, as the tail of its summary sentence, and each table's
*Every match* is a small verb on its title line.

**Implementation:** `components/database/`; backend in
[`../backend/databases.md`](../backend/databases.md) Adding a database can limit its container from
the start.

### `/assistant`

A chat about the whole server beside every chat the person has had, grouped by what each is about
(the server, each deployment, each connection), and behind a second tab who the assistant asks, for
every page, as one settings column (`SettingSection`s on hairlines): Model (the provider, the model
and who set it up as a line of facts, the picker at its end), Providers (a `ChoiceGrid` of cards
with their real logos, the one in use lit), Default mode (three cards; written the moment one is
pressed, one write at a time) and Where it works, then Ask about a deployment ([the deployment
assistant](../deployments/operator.md#providers)). `?about=`
and `?title=`, from **Ask** beside Search on any page (`assistant/ask-about.tsx`, `agent/about.ts`,
unit-tested), begin a new chat told which page it came from, with a chip back to it;
`tests/browser/assistant.spec.ts`, `tests/browser/assistant-settings.spec.ts` and
`tests/browser/database-assistant.spec.ts`.

### An address no page claims

`app/(dashboard)/[...missing]` hands every unclaimed address to `(dashboard)/not-found`, inside the
shell. It draws `components/missing-page.tsx`: the address asked for on a dashed ring, wired to this
server by a line nothing travels, and beyond the server the pages that are there. The nearest of
them is the page of the navigation whose own address the path starts with and the account's role
reaches (`missing-page-model.ts`, unit-tested), and is the page's command; *Back to the Overview*
is the second way, and the only one for an address under no page. `tests/browser/missing-page.spec.ts`.

### `/deploy`

Routes: `/deploy`, `/deploy/notifications`, `/deploy/credentials`, `/deploy/variables`,
`/deploy/drains`, `/deploy/templates`, `/deploy/new`, `/deploy/[id]` and project subpages

Projects, in-progress runs and native state filters; project Overview, Assistant ([the deployment
assistant](../deployments/operator.md)), Deployments, Logs, Runtime, Console and settings, read for
production or — through the header's environment switch — staging, with Add staging, Promote,
promotion-only and, on a pull request's row of the Overview, Promote to staging for its preview's
image ([staging and promotion](../deployments/staging-promotion.md)). Operations show observed
runtime, domains, storage, backups and dependencies. Deploy/version/retry queue work directly;
source detection and actual execution preflight/readiness checks remain. New-project planning, Git
automation, credentials, shared variables and notifications retain their controls. Automatic
fleet/project attention panels and optional Before you deploy reports were removed in 0.7.5. Build
settings carries the migration rehearsal's terms under the release tasks (`settings/rehearsal.tsx`):
the switch, the database to restore when the tasks reach several, whether a release that cannot be
rehearsed stops or continues with a warning, and the restore and size limits, with a note naming a
task the rehearsal cannot run; the run page reads a `rehearse_migration` step's report as one
sentence on the step's mark with a line of facts (`run-verdict.tsx`, its words in the unit-tested
`run-verdicts.ts`) — whether the tasks passed, the restore and its source, the longest lock, the copy
removed — over each
task's exit and every lock with what production will wait for (`run-rehearsal.tsx`, its words in
`rehearsal-model.ts`, unit-tested). While a release just cut over to is on watch, the project
Overview opens on its window (`release-watch.tsx`), in the Overview's block shape: the proxy's mark
with the reading as one sentence (how much of the new release fails and how long 19 in 20 of its
requests take), what it was judged as and the window's countdown under it, then the previous release
and the new one as two strips of one length with their figures beside them, with *Keep release #N*
and *Roll back instantly*; a window that rolled back stays an hour with the reading that decided it,
and one that closed clean is not drawn. Deployments marks the rolled-back release and the warm one,
and the one Live row is the run whose outcome is live now; Runtime settings edits the terms under
*After a release goes live* (`settings/rollback-watch.tsx`, its words in `rollback-watch-model.ts`,
unit-tested) — skew protection too, *Keep each visitor on the release that answered them*, which is
the plan's `runtime.skewProtection` saved as a one-field change of the plan and said back with the
window it lasts; with it on, the Overview's window says how many of its visitors' requests the warm
release, running rather than paused, answered —, and the Releases section offers one to eight
replicas, only to a stateless plan and with the reason when not; the Overview's runtime line reads a
replicated release as *2 of 3 replicas healthy* and the Runtime page's cards say which replica each
is (`replicas-model.ts`, unit-tested). The Releases section also offers Canary beside Blue / green
and Stop first, with its share, window and *Promote on its own*, and the run page draws a canary's
trial while its step runs — the window's countdown, the candidate and the live release side by side
(requests, 5xx share, p95) and *Promote now* / *Abort* (`run-canary.tsx`, its words in
`canary-model.ts`, unit-tested; `tests/browser/deploy-canary.spec.ts`). Runtime settings also
carries the traffic replay's terms under *Traffic replay*, beside the release strategy and saved
with the runtime plan (`settings/replay.tsx`): the switch, the requests replayed, the status drift
allowed, the slowdown factor, the time limit and the path prefixes, each refused under its own field
before the save when the server would refuse it; the run page reads a `replay_traffic` step in the
Verify stage as one sentence on the step's mark with a line of facts (`run-verdict.tsx`) — how many
replayed requests matched, then those drifted against the share
allowed, how long 19 in 20 of the candidate's answers took against the recorded ones, the time taken
and how many were recorded — over each request the candidate answered differently by method, path, recorded status and its
answer (`run-replay.tsx`, its words in `replay-model.ts`, unit-tested). Deployments also carries
each release's data point as a tag, *Restore data as of this release…* under the release's verbs,
and the *Data copies* panel — a copy's address revealed on request, *Compare with live*, *Keep 1
more hour*, *Restore in place…* (a link to the database's Backups page or the backup run's restore,
prefilled by `?recover=` and `?run=&restore=`) and *Remove now*; the rollback dialog's side-copy
checkbox and the run page's link to the copy it asked for (`data-copies.tsx`, its words in the
unit-tested `data-rollback-model.ts`; [data-aware rollback](../deployments/data-aware-rollback.md)).
Under Production the Overview's **Right-size** card reads the environment's suggested memory and CPU
limits from its retained samples — the verdict and *Apply suggested limits* in its head, each
suggestion beside its p95, peak and limit — and the fleet lists the environments whose limits are
off under the projects, each with *Apply*; both save through the configuration PUT
([right-sizing](../deployments/right-sizing.md); `right-size.tsx`, words in `sizing-model.ts`,
unit-tested). The Overview's production panel carries *Diagnose* for a role with `service.control`:
a link to the project's Assistant page (`/deploy/[id]/assistant?ask=diagnose`,
`project-assistant.tsx` over `components/assistant/agent/`), which asks the assistant the diagnostic
question about the environment ([the deployment assistant](../deployments/operator.md)). The
Overview's *Vulnerabilities* panel reads the live release's latest image scan
(`vulnerabilities.tsx`, its words in `vulnerabilities-model.ts`, unit-tested): a figure per severity
with how many have a fix, the moved base that makes a rebuild worth it, an end-of-life distribution,
the fixable findings at the floor, and *Rebuild* — the runs route's own `force_build` of the live
commit — and *Scan now*; Deployments marks a release whose scan found something at the floor, and
Build settings carries the scanning terms (`settings/vulnerability-scan.tsx`) ([image
vulnerabilities](../deployments/image-vulnerabilities.md)). Templates added on this dashboard sit
beside the reviewed catalogue ([implementation](../deployments/implementation.md)): the picker on
`/deploy/new` opens on *Your templates* above the shelves, with a *Yours* chip, each a card like a
built-in's drawn by its public image's product, and its empty state saying the two ways to make one
with a link to Templates; a chosen one says *Added … on this dashboard* where a built-in says
*Reviewed*. `/deploy/templates` (Deployments → Templates, `templates-page.tsx`) draws them as the
picker does, a card each (the product its image is, what it runs, Compose stack or image, *Used by
N*), and the chosen card (the first until another is pressed) as a picture over the grid: what it
was made from, by whom and when; the template under its id and version with how many deployments
use it, or why this dashboard cannot deploy it; and each service it runs with its image, the public
one marked. A card's own verbs are *Deploy* (into the picker with it chosen, not offered for a
template that cannot be deployed), *Export as JSON* and *Remove template* (whose confirmation says
those deployments keep running from their own copy); the head counts them (*3 templates · 1 in
use*) and carries *Import…* for an exported file and *Add from Compose file*, a sheet whose *Check*
shows what the template will ask before it is added (`template-preview.tsx`) and says the
catalogue's refusal under the form; empty, it offers the three ways as cards, each with a product's
mark. Settings → General carries *Save as template…* on a deployment that runs published
images (`settings/save-as-template.tsx`): a dialog that reads the live release's plan as it opens
and says what the template will run, ask when it is chosen and ask on the Variables screen, or why
it cannot be one. The words are in `templates.ts` (unit-tested). The new-project build step and
Build settings offer Railpack beside the recipes, a Dockerfile and a static site, with its one-line
description, its provider (Railpack's detection unless one is chosen) and its configuration file,
and the run page reads a Railpack prepare step as what Railpack decided — the release, the provider,
the packages it resolved, its plan's steps and start, and the images it builds from — with
Railpack's own errors first when it refused (`run-railpack.tsx`, its words in the unit-tested
`railpack-model.ts`; [Railpack](../deployments/recipes.md#railpack)). Runtime settings carries the
release container's process flags under *Inside the container* — the user it runs as, a read-only
root with its writable paths (started from /tmp, and for a static site where nginx writes), and
behind a fold the ulimits and sysctls, each one per line as `docker run` writes them — and its GPUs
under *Container access* (none, every one, a number or named, and whether previews get them), read
and refused line by line in the unit-tested `runtime-flags-model.ts` before a save and under the
field the server's refusal names after one (`settings/runtime.tsx`,
`tests/browser/deploy-runtime-flags.spec.ts`). Domains settings give each saved domain *Open the DNS
record*: a sheet that reads the dry run of `GET /deploy/{id}/domains/{domain}/dns-record` once as it
opens — where the record stands, each set as the line to copy beside what the zone holds now, the
zone and provider whose saved DNS credentials write it, and who created what is managed here and
when — with one button in the dry run's words, *Create the record*, *Correct the record* or *Replace
the record* (which sends `replace`), and a refusal as a notice naming what stands in the way, under
the records to create by hand, leading to Certificates where a saved token is the answer; a run's
`dns_record_writable` finding offers *Create the record*, which opens the page on that sheet by
`?record=` (`settings/dns-record-panel.tsx`, its words in the unit-tested `dns-record-model.ts`;
[DNS records](../deployments/dns-records.md)).

Five of these pages took the deploy standard's last steps in 0.8.7. A project's Logs reads the hour in
the Overview's `ReadingLine`s where four tiles stood and is one pane with a bottom bar on a phone
([request observability](../deployments/request-observability.md)). Deployments opens Delivery on the
Overview's sentence on the source's mark with the rate, the frequency, the usual time and the recovery
as one line of facts (`deliveryFacts` in `delivery-model.ts`, each fact's basis its title), its waiting
shape the lead's and the charts' own, and a run's row says its time once, in amber when it ran long,
with no bar under it. A run at work that has written nothing shows the step it is at in the console,
its name lit (`build-console.tsx`). On `/deploy/new` the GitHub App that is not connected is the
Credentials picture, small (`new-project/app-picture.tsx`), with Connect on the section's title line,
the Git column ends on Import, and a shelf of one template stands beside its neighbours in the cards'
columns. A subject's assistant whose settings cannot be read is the off state with *Try again*
(`assistant/off.tsx`), a database's included. A line of facts that wraps is `deploy/fact-line.tsx`,
which never leaves a separator at a line's end.

**Implementation:** `components/deploy/`; see
[`../deployments/implementation.md`](../deployments/implementation.md); checked by
`tests/browser/deploy-data-rollback.spec.ts`, `tests/browser/deploy-rollback-watch.spec.ts`,
`tests/browser/deploy-rehearsal.spec.ts`, `tests/browser/deploy-replay.spec.ts`,
`tests/browser/right-sizing.spec.ts`, `tests/browser/assistant.spec.ts`,
`tests/browser/deploy-vulnerabilities.spec.ts`, `tests/browser/deploy-dns-record.spec.ts` and
`tests/browser/deploy-templates.spec.ts`

### `/docker/*`

Native inventory, container state/health/events, live usage, logs, inspect, shell and storage;
images, volumes, networks and Compose projects with their existing ownership guards and confirmed
changes. Configure retains the administrator-reviewed replacement specification for standalone
containers and opens the owning Compose file for Compose containers. Automatic overview findings,
issue badges, failure/anomaly suggestions and recommendation cards were removed in 0.7.5

The overview, Containers, Events and Stacks open on one line (`docker-identity.tsx`, `HostIdentity`
laid out as a project's header): Docker's mark, "Docker" and its version, the page's verdict beside
the name, the server as the first fact, and the page's one command at the far end (Open Deployments,
Create stack, Events' window). The overview (`app/(dashboard)/docker/page.tsx`) reads: that line; the
band of the busiest containers and what last happened to each; the engine as a picture
(`engine-picture.tsx` on `WireFlow`, its nodes worked out in `engine-picture-model.ts`, unit-tested)
with the compose projects and the containers started by hand on one side of Docker and the networks
and volumes it keeps on the other, a line red where a project has a failing container, amber where it
is partly up or a volume has nothing mounting it, dashed where a thing is stopped or never deployed,
six on the left and three of each kind on the right before the rest fold into a count that opens
their page, pointing at a project lighting what is its own; then the compose projects as one-line
cards beside the disk. The table of every container is `/docker/containers` only: the section's
title links to it and the verdict opens it narrowed to what is failing. The page is drawn before
anything is read, and the picture waits at the height it had on the last visit
(`docker.overview.picture.rows`). Stacks (`stacks-tab.tsx`) lists each stack as a one-line card
(`stack-row.tsx`): logos, name and directory, then a strip of its services' states, its state with
how much of it is up, summed CPU and memory and published ports in columns that line up, Deploy for
a stack that is down, an arrow and a menu (View, Files, Open shell); nothing expands in place, and a
stack found by a service's name or image says which matched. A stack's page reads in a container
page's order (the band, the ports-and-networks picture, the services table), and both detail pages
keep their list beside them from `xl` (`stack-rail.tsx`, `container-rail.tsx`, copies of
`procs/process-rail.tsx`): the list's own search, chips and order from its session keys, the item
on screen filled (and drawn first when the filter does not hold it), Alt+↑ and Alt+↓ and the two
arrows walking it with `?tab=` carried. Beside the rail the blocks that went side by side at 1280px
and 1536px do so at 1600px and 1856px (`beside-rail.ts`)

An image is a page of its own, `/docker/images/[id]` (`image-page.tsx`; the old `?image=` address
redirects there): its identity line with Pull or Tag and the rest in a menu, a picture of its
registry, the image and the containers made from it, the views Layers, Containers, History and
Configuration in `?tab=`, and from `xl` the images as a rail with the table's own filters.
`tests/browser/docker-image-page.spec.ts`

A volume is a page of its own (`/docker/volumes/[name]`, `components/docker/volume-page.tsx`): its
identity line with one command, a picture of the containers that mount it and the backup jobs that
cover it, and its views in the address as `?tab=` — Files, Mounts, Backups, Configuration — with
the Volumes table as a rail beside it from `xl`. `/docker/volumes?volume=<name>`, which opened the
sheet this replaced, redirects to it. Specs: `tests/browser/docker-volumes.spec.ts`,
`tests/browser/docker-volume-page.spec.ts`.

**Implementation:** `components/docker/`; checked by `tests/browser/docker-overview.spec.ts`,
`stacks-ui.spec.ts`, `stack-detail.spec.ts`, `container-page.spec.ts`, `docker-events.spec.ts` and
`docker-ui.spec.ts`; see
[`features-terminal.md`](features-terminal.md#feature-panels) and
[`../backend/docker-files-logs.md`](../backend/docker-files-logs.md)

### `/metrics`

Redesigned per design-system §15, last in 0.7.1 to read as the Overview does: the identity line (the
processor drawn as its vendor's mark, cores, memory, swap, hypervisor, distribution, recording
cadence, then the pause, export, range and shortcuts controls); a Resources section headed by the
window the trends cover and the socket's Live state, holding five live readings — CPU with a bar per
core, memory, load, network and disk I/O — each figure gliding to every frame, keyed by its line's
colour, carrying the window on screen as its trend and, on a recorded range, a delta against the
previous window; a notable-moments list that groups the peaks, stalls, failed deploys and reboots
that arrived together into incidents — each led by its heaviest signal (an event outweighs any
spike) with the rest as keyed signals beside it, the window drawn above them as a timeline of marks
— where pressing an incident pins its instant on every chart (pressing again releases it), hovering
one previews it as the charts' crosshair, and a zoom glyph narrows the charts to it and keeps the
pin, beside top processes by CPU, memory or I/O as a `RankList`, each drawn as its product and
opening its own page; then one section per
resource, each reading what it is now beside what it did: Processor (utilisation, total or broken
down, beside every core as a column and the temperatures, a sensor carrying its vendor's mark),
Memory (Allocation, a `BarReading` of what programs hold and what the cache holds on a track that is
all of it, its head saying what is available, and Swap as a second one under it, beside the memory
chart), Network (throughput beside sockets, whose head carries TCP, TIME_WAIT and open file
handles; then how connections fared as three recorded charts — resent segments, whose head carries
the current resent share and median RTT, connection RTT and failed connections — and the interfaces
— the host's real devices by default with Docker's veth pairs and bridges behind an Everything
toggle, as on the Network section's Interfaces page, each its owner's mark and a bar of its in and
out, two to a row on the page's own ground as the filesystems are), Storage (the Overview's
filesystem rows with their scan beside capacity; disk throughput, operations, latency and inodes)
and Saturation (pressure, its head the three stalls now, beside load, its head the processes it
counts). Every series takes its measurement's hue; live and recorded windows, annotations, a
synchronized cursor and pinned moments run across every chart, the pinned moment's strip holding at
the top of the scroll. A dragged span is mirrored into the URL as `from`/`to` so a window is a link,
the live feed can be paused, and the window on screen exports as CSV. Under Storage, the **Capacity
forecast** says when each filesystem fills at the rate the record shows, with the fit's confidence,
beside the five deployments whose volumes grow fastest ([capacity
forecast](../deployments/right-sizing.md#capacity-forecast))

**Implementation:** metrics page, `components/metrics/` (`notable-moments.tsx` with
`lib/metrics-moments.ts`, `top-processes.tsx`, `hardware-panels.tsx`, `moment-inspector.tsx`,
`capacity-forecast.tsx` with `lib/capacity.ts`), `components/overview/readings.tsx` and
`storage.tsx` (shared with the Overview), `lib/metrics-summary.ts`, `lib/metrics-export.ts`

### `/metrics/alerts`

The host's Alerts (administrators; under Monitoring beside Metrics, whose identity line links here
with the firing count): the proxy Alerts page's layout over the machine's own readings — CPU,
memory, filesystems, Docker's containers and backups wired through this server to the channels told
— under an identity line laid out as a project's header: what the rules are doing now as its state
(*N firing*, *N holding*, *Watching*, *No rules*), how many are on, how many channels they tell and
when they were last checked as its facts, **Add rule** as its command and *Check now* behind `⋯`.
Then what fires or holds with its mute, the rules as rows of one height that open their form in a
sheet (each kind's line and minutes checked against the server's ranges before it is sent), a rule
that cannot be judged saying why on its own line, and beside them what was told lately and a test
per channel; a rule's menu also sends its own test. With no rule the kinds are offered as cards, a
name and one fact each; with no channel the section shows the services a channel can be and the way
to add one. The page has its shape while the rules are read. An account that is not an administrator
is told the page is one's, and the server is not asked

**Implementation:** `app/(dashboard)/metrics/alerts/`, `components/metrics/alerts-page.tsx` and
`alerts-link.tsx`, `components/alerts/alert-parts.tsx` (shared with `/proxy/alerts`),
`lib/host-alerts.ts`; backend in
[`../backend/observability-security.md`](../backend/observability-security.md#host-alerts-told-when-the-machine-itself-goes-wrong)

### `/docker/events`

What the Docker daemon did and who did it, over a 1h / 6h / 24h / All window: Docker's identity line
(the one every Docker section page opens on) with the verdict beside its name (a restart loop named,
the containers that failed, or nothing), the socket's state and, at its end, the window; four readings (failures, restarts, created or removed, external), each counting up
with its trend across the window and narrowing the page to what it counts; the containers the record
names as a table, worst first, each with its state now from Docker's listing, its window as a lane
of outcome-coloured marks (a loop a red band), its failures, comebacks and who last acted on it, a
row narrowing the feed and its name opening the container; and the feed, a plain section (a title,
a hairline, rows on hairlines, scrolling with the page; framed with a scroll of its own until 0.8.7),
restart loops and a container's events moments apart folded into one row each, every row saying who
did it. The buffer
is read whole once and its newest hundred every fifteen seconds, so the polled copy's audit-log
match replaces the socket's; an exit right after a kill is read as the stop it was

**Implementation:** `components/docker/events-page.tsx`, `event-activity.tsx`, `event-feed.tsx`,
`event-marks.tsx`; `lib/docker-events.ts` (unit-tested)

### `/docker/networks`

Docker's networks, who is on each and what they carry. The engine's identity line, laid out as a
project's header (Docker as its mark and version, how many networks and how many were made here, the
containers on them; from `sm` the host and how many join two or more) with the verdict beside the
name: the address pool nearly or wholly spent, else — where the container listing failed — that who
is on them is unread, since then no network is called unused or in use; else the unused networks — a
press narrows the table to them — else every network in use. Create network is the line's one
command. A band of bar readings (`network-band.tsx`, design system §10): **Traffic**, the
five busiest networks by their bridge's two-second counters (`useLiveTraffic`) as spans of one bar
in each network's colour, with in and out gliding on its line; **Addresses**, Docker's address pools
(`default-address-pools` from `/docker/info`, else the built-in thirty-one) as a span per network in
its colour sized by the blocks it holds, the first free block at the strip's end and any subnet
outside the pools in the figure's tooltip; **Recent**, Docker's network events followed live as
ticks on the last day, each naming the container that joined or left. A press in Traffic or Recent
opens that network's page. Then the networks as a framed
table — the network's lane colour down its edge, who made it as counted chips that narrow (compose
project, this dashboard, standalone, Docker's own; the backend's reading of the labels where it has
one, so a deployment's network and a database link are the dashboard's and name the deployment,
otherwise the labels themselves), its subnet and gateway, its members as their products with how
many run (**members unread**, not "nothing attached", where the container listing failed, with a
notice over the page; a failed refresh of the list keeps it with a dated warning), its traffic with
two minutes of shape, and Attach and Remove drawn disabled with the reason where they would fail
(Docker's own, in use, members unread, a swarm network without `--attachable`, the dashboard's own
private network) — and every container as a second table: each network it is on in that network's
colour with its address there, the names it answers to (none on the default bridge, in amber), its
traffic, chips for the containers that join networks, the default bridge and the stopped ones, and a
network's chip narrowing the table to it. A network's name is the link to its own page
(`/docker/networks/[name]`, below) and the row is the pointer's larger target for it;
`/docker/networks?network=<id>`, the address of the sheet that page replaced, redirects there on the
server. A row is one height in both tables and in the lists they become below `xl`: a container on
more than two networks names the first and counts the rest, its names stay on one line with the rest
behind a count, and a network's traffic below `xl` has its place whether or not it carries any. On a
phone the two strips of chips wrap rather than run off the panel's edge. Remove
unused sits in the networks table's toolbar. Attach a container, Remove and Remove unused
each read what they would disturb first (`network-dialogs.tsx`, `network-conflicts.tsx`; the
previews' types and ordering in `lib/docker-networks.ts`, unit-tested): refusals are the server's
too and leave the confirmation off, warnings are confirmed, the attach dialog keeps its container
and extra name when the candidates or the preview cannot be read, and the reviewed prune removes
only the networks nothing names and lists the rest as kept with why; see [guarded
changes](../backend/docker-files-logs.md#network-ownership-dependencies-and-guarded-changes). Create
network (`network-create.tsx`, with `network-create-reading.ts`, unit-tested) keeps its draft after
Engine refusals or a stale inventory and offers advanced gateway and allocation ranges, dual-stack
pools, attachable, labels and, to an administrator, drivers and options read from the Engine's
catalogue (a missing or refused driver and option keys Docker would ignore are named);
`?ipamReservation=` opens it on a shared address plan's reservation; see [network
creation](../backend/docker-files-logs.md#network-creation)

**Implementation:** `components/docker/networks-page.tsx`, `network-band.tsx`, `network-table.tsx`,
`network-members.tsx`, `network-live.ts`, `network-dialogs.tsx`, `network-conflicts.tsx`,
`network-create.tsx`, `networks.ts` (unit-tested), `lib/docker-networks.ts` (unit-tested); checked
by `tests/browser/docker-networks.spec.ts`, `docker-network-maturity.spec.ts` and
`docker-network-create.spec.ts`

### `/docker/networks/[name]`

One network, as a place of its own; it was a sheet over the Networks table until 0.8.7. The address
names the network, and an id opens it too (the Engine resolves either, and a deployment's removal
plan names one by id): the address is then put right to the name. A way back and Previous / Next;
the identity line as a project's header — Docker's mark for a bridge or an overlay, the kind of
device otherwise, the name behind the network's own colour, where it stands beside it (**in use**,
**N of M running**, **unused** in amber for a network somebody made that nothing is on, **nothing
attached** for Docker's own, **members unread**) with **no internet** and **IPv6**, then who made it
(a compose project's is a link to its stack), its driver and device on this server, its subnet and
how many are on it. **Attach a container** is its one command, offered where Docker would take one;
the menu holds Open the stack, Copy name, Copy ID and Remove, which is drawn disabled with the
reason where Docker would refuse, as is the reason the dashboard's own network or a swarm network
without `--attachable` takes no container. Removing goes back to the list.

Views in the address as `?tab=` (Overview carries none), remembered between networks:

- **Overview**: the wiring picture (`network-picture.tsx`, on `WireFlow`) — where it leads on the
  left, the network in the middle (its bridge device and subnet), its members on the right as the
  products they run with their address and what each is moving. Where it leads is how its members
  leave it (out through this server at its gateway address; straight onto a network card for a
  macvlan or ipvlan, a link to that card on Interfaces; no way out for an internal one; nothing for
  `host` and `none`, said in a sentence) and then the other networks its members are also on, each
  a link to that network and saying through whom, since a container on two networks is the only
  thing that joins them. A member's line pulses while it moves more than a kilobyte a second, faster
  the busier, and is dashed while it is stopped; pointing at a member lights the networks it joins
  this one to. Five members a side and three leads are drawn and the rest counted, the count opening
  Members. Under it four reading lines: received and sent on its bridge with their last two minutes,
  addresses taken of the subnet with its gateway, and whether its members answer by name (not on the
  default bridge, in amber).
- **Members**: every member as a one-line card (`ChoiceRow`) that opens the container — its product,
  name, **This dashboard** / **Shared ingress**, its address (both families from a wide column) and
  the other networks it is on, the names the others reach it by (two and a count, all in the tooltip;
  **aliases unread** where its inspect failed; where the list is too narrow for the column they are
  the row's third line, on every row), its state and rate — one height whatever it has. Detach reads what it disturbs first; the dashboard's own
  containers and the shared ingress get a locked control with the reason. Nothing attached is an
  empty state with Attach a container. The notices for members unread, members' details unread and
  the default bridge's lack of names are here.
- **Configuration**: subnets, gateway and IPv6; who made it and what acts on it next (Compose
  recreates it, its deployment reconciles it, nothing recreates one made by hand), driver and
  device, whether it reaches the internet and accepts new members, scope, created, id; then the
  driver's options and the labels as written.

From `xl` the networks stand beside it as a rail (`network-rail.tsx`): the table's own search, owner
chip and order (the same session keys), a caption ("11 networks", "3 of 11 · Compose"), Clear
filters, the network on screen filled and kept in view and drawn first when the filter does not hold
it. Alt+↑ / Alt+↓ and the two arrows walk it and the open view goes along. Below `xl` the page is
one pane that scrolls as a whole, the picture's columns stacked with every word kept.

The list is read every ten seconds, for the rail and for who is on the network now; the inspect once,
and again when the list says its members changed (it inspects every member for its aliases). So the
page draws from the list before the inspect lands, and when the inspect fails. It has its shape
while both are on their way (the identity line with the name from the address, the three views, the
picture waiting); a name that is no network is an empty state with the way back and the rail still
listing the rest; a network removed while its page is open goes back to the list.

**Implementation:** `components/docker/network-page.tsx`, `network-picture.tsx`, `network-views.tsx`,
`network-rail.tsx`, `network-live.ts` (the containers socket's listing and rates, shared with the
table), `networks.ts` (the page's address, standing, members, joined networks and way out,
unit-tested); checked by `tests/browser/docker-network-page.spec.ts` and
`docker-network-maturity.spec.ts`

### `/files`

The host's files as a workbench: a sidebar of places and starred folders (`files-sidebar.tsx`,
`places-menu.tsx`), the path bar, list and grid views with selection, drag and drop and uploads,
name and content search (`quick-open.tsx`, `search.ts`) and a preview and details panel
(`preview-panel.tsx`). Below `lg` it is one pane at a time (Places, Folder, Details) with a bar at the
foot (`phone-panes.tsx`) under a one-row strip: back, the path, find, New, and the rest as words in
`more-menu.tsx`

**Implementation:** files page and `components/files/`; checked by `tests/browser/files-ui.spec.ts`;
backend contract in [`../backend/docker-files-logs.md`](../backend/docker-files-logs.md#files)

### `/files/editor`

A file opened as a page: a folder tree (`file-tree.tsx`) beside the text editor (`file-editor.tsx`)
or the image editor (`image-editor.tsx`), with name and content search. The editor's commands are
grouped (`editor-toolbar.tsx`), with text size, indentation and the command palette in a menu. Below
`lg` the tree and the editor are one pane at a time with a bar at the foot, both kept mounted so an
unsaved draft outlives a look at the tree

**Implementation:** files/editor page and shared `components/files/` editors; checked by
`tests/browser/files-ui.spec.ts`

### `/git`

The checkouts under the git roots (`JD_GIT_ROOTS`) shelved by the account their remote belongs to
(`lib/git-repos.ts`), as repository cards under the chips All, Uncommitted, Behind, Unpushed,
Detached, Pull requests and Worktrees, with worktrees in a section of their own; Clone for
`service.control` and the GitHub account beside them. A selected checkout takes the whole page as
its workspace (`repo-workspace.tsx`)

**Implementation:** `app/(dashboard)/git/page.tsx`, `components/git/`, `lib/git-repos.ts`; checked
by `tests/browser/git-ui.spec.ts`, `git-panel.spec.ts` and `git-features.spec.ts`; backend contract
in [`../backend/git-backups-users.md`](../backend/git-backups-users.md#git-working-copies)

### `/logs`

Every log source in one place: a source rail (`source-rail.tsx`) beside one pane with Live, History
and Insights (`log-workspace.tsx`), the same readings and views a service's own page reads through
`service-logs.tsx`. The whole journal's identity line carries the systemd logo, how many units
write to it, its size on disk and its oldest line (`GET /logs/source?source=journal:`, asked once
the journal is the source on screen; the two facts keep their room while they are asked). A kind
of source that is absent is one line in the rail with the thing's mark and the page that says
more (`MissingLine`, worded by `logKindMissing`): "Docker is not running" to `/docker`, "PM2 is
not installed" to `/processes/pm2`, "No systemd on this server" to `/processes/services`

**Implementation:** logs page, `components/logs/`, `lib/log-filter.ts`, `lib/log-lenses.ts`,
`lib/log-sources.ts`; checked by `tests/browser/logs-ui.spec.ts`, `logs-lens.spec.ts`,
`logs-views.spec.ts`, `logs-handoff.spec.ts`, `logs-buffer.spec.ts` and `missing-states.spec.ts`;
backend contract in
[`../backend/docker-files-logs.md`](../backend/docker-files-logs.md#logs)

### `/packages`

Reading register without summary tiles: the host identity laid out as a project's header, with the
manager beside the distribution, what is owed as its state, the inventory count and index age as
facts, **Refresh index** as its one command and Re-read and the upgrades behind its menu; a band of
two bar readings (`software-band.tsx`, design system §10): the whole inventory's size (products or
archive sections, largest five and other packages) that filters Installed, beside Updates waiting, a
span per pending package, security first, the first five named and each opening its package's page,
with confirmed upgrade actions at the strip's end; count-bearing views, drawn while the inventory is
still out, and by-hand/everything/dependencies/security/behind chips; from 1024px framed tables of
one line a row (the package as the software it is and its name as the link to its page, the
description as the row's title, version, the changed part of a pending one, the size as a figure in
its column, security/essential at the edge), below it a card a package of one height; catalogue
product choices and lit search-result rows that open the package's page, with separate install
verbs, pending searches hiding obsolete results, and `?q=` opening Add software on a search handed
over from another page; **Log** reads the manager's logs through its lens and stays available after
an inventory failure; unreadable updates report errors, missing sizes/advisory coverage say so. The
list's search, scope, software group and order are one filter (`use-package-filter.ts`, the pure
part in `inventory.ts`) shared with the rail beside a package

**Implementation:** packages page, `components/packages/` (`marks.tsx` and `inventory.ts`
unit-tested), shared job console; checked by `tests/browser/packages-ui.spec.ts`, mocks in
`packages-fixture.ts`

### `/packages/[name]`

A package's own page (`package-page.tsx`; the old `?package=` address that opened its sheet
redirects there, and the name is one encoded path segment, so `g++` and `libc6:amd64` open): a way
back and two arrows, then the identity line, two rows in every state: the package as the software it
is, its name and its state (installed or not, an update or a security update waiting, or what the
manager is doing to it), then the version it is at, the one an update lands on with its changed part
in ink and amber for a security fix, its size and its description, **Install** or **Update** as the
one command and Remove and Remove and purge behind the menu and their confirmations (for an account
with both `system.admin` and `destructive`, and never for a protected package). The job console
opens under the line, a run started elsewhere for the same package is attached rather than offered
again, and the package is read again when the run ends. Views in the address (`?tab=`), carried to
the next package (`package-views.tsx`): **Overview**, the picture of what it needs, the package and
what it gives the server (`WireFlow`: dependencies as links to their pages, dashed where one is not
installed; its commands, its service or services, its configuration and its manual as ways into the
views or the unit's page; every line pulsing while a run is in flight; held to one height for every
package and while loading), why it cannot be removed where it cannot, and About (description,
installed by hand or as a dependency, essential, repository, section, architecture, licence,
maintainer, homepage); **Files**, what its file list put
on the server (commands that copy, services opening the unit with `systemctl start` to copy,
configuration and documentation opening in Files); **Manual**, its man page and its other pages;
**Depends**, everything it needs one line each with the version on the host or "not installed" and
what was asked of it; **Log**, the package manager's logs searched for its name, dpkg's per-package
record first, kept apart from the list's own Log view (`log-view.tsx`'s `about`). No changelog view:
no manager is asked for one. From `xl` the installed list is a rail beside it (`package-rail.tsx`:
the list's own search, scope and order, the first 300 drawn, the package on screen filled and drawn
first when the filter does not hold it, a dot in a logo's corner for one that is behind), walked
with Alt+↑/↓; on a phone the page is the one pane. Not installed, a library with nothing to run, a
name nothing knows (with a search for it) and a failed read are each said in place

**Implementation:** `app/(dashboard)/packages/[name]/page.tsx`, `components/packages/package-page.tsx`,
`package-rail.tsx`, `package-views.tsx`, `use-package-filter.ts`; checked by
`tests/browser/package-page.spec.ts`

### `/processes/*`

Four routed pages listed on the rail's Processes panel, every row drawn as the product it is where
it is one (`product-logo.tsx`: `processProduct`, `unitProduct`, `pm2Product`, `programProduct`) and
a glyph where it is not. Live: the machine's identity line (its distribution as its mark, CPU, load,
available memory and uptime as facts gliding to each socket frame, the process count, and the
table's refresh cadence and row cap, Pause and the shortcuts at its right end), then who is using
the machine (`procs/workloads.tsx`: the five heaviest workloads by processor and by memory — a
supervisor's processes, or the copies of one program started by hand, summed over the whole host
with shared pages counted once — as spans of one bar the size of the machine that ease to each poll,
one of them on the line beneath it (a bar reading, design system §10) and a press narrowing the
table to its workload), over the inventory table whose head counts running, sleeping, blocked and
zombie as state chips beside the ranking as five chips (Automatic, which says why it chose, CPU,
Memory, Disk I/O, Uptime), with owner chips (PM2 and Docker drawn with their marks), the accounts as
chips beside them (the three with the most processes, the rest behind one more chip, the one in
force always drawn) and the chosen workload as a chip under it; fixed columns,
so a long command line never pushes the readings or the row menu past the panel's edge, CPU and
memory as a figure beside a short bar, a container owner by its name, a kernel thread, a zombie and
an unreadable command line each saying which it is, and a process that started since the last poll
rising into place; a row's verbs (open, terminate, kill, pause/resume, hang up, interrupt,
SIGUSR1/SIGUSR2, open its supervisor, copy) in one menu, none of the signals offered to a kernel
thread or a zombie, whose menu opens its parent instead; a row opens the process's own page. **A
process's page** (`/processes/[pid]`, `process-page.tsx`; the old `?pid=` address redirects there):
from `xl` the table folded into a rail beside it (`process-rail.tsx`: the table's own filters,
search and ranking, held while the pointer is in it, the process on screen filled), Alt+↑/↓ and two
arrows to walk it, the view on screen carried to the next process; the identity line (the process as
its product with its state in the corner, the PID to copy, its user, its supervisor with that
supervisor's mark as a link, uptime ticking, its parent, its threads, and its state and wait channel
at the end) under Terminate, Kill and a menu of every other signal and "with its children"; then the
views as the underlined strip, `?tab=` in the address — **Overview** (`process-overview.tsx`:
notices for an exit or a reused PID, a zombie, a kernel thread, a paused process, a binary or
library an upgrade replaced, descriptors near their limit, deleted files held open and a waiting
signal; four live readings over the recent windows; *Where it runs*, a wired picture of what
supervises it and its parent on the left and what it started — copies of one program as one node —
its listening sockets and its open files on the right, each node opening what it is; the command;
who it runs as and how it is scheduled; and its memory as one strip and a list that does not grow),
**Threads**, **Files** (descriptors by kind and the libraries it loaded), **Network**,
**Environment** (the container's table, credentials hidden), **Limits and privileges** (its cgroup's
limits, every rlimit, capabilities, seccomp, and the signals it handles, ignores and holds),
**Control** (`process-views.tsx`: its supervisor's own verbs — the unit's, the PM2 application's or
the container's — every signal with what it will do to *this* process from its signal masks and a
switch to send it to its children too, the priority stepper and the out-of-memory bias as five
presets) and **Logs** (its supervisor's log through the service logs) — and it stops reading and
says so when the process exits or its PID is reused; below `xl` the page is the one pane and scrolls
as a whole. PM2: PM2's own identity line laid out as a project's header (whether it resurrects on
boot as the verdict beside its name, read against the saved list's own names so an application
started since the save turns it amber and offers **Save list**; the account, the Node every
application runs, the boot hook, the last save and the application count as facts; **Start
application** as the line's one command and the daemon-wide save/reload/restart/stop in its menu;
several accounts as rows under it), a band of what PM2
takes of the machine (`pm2-band.tsx`, bar readings: the five heaviest applications by processor and
by memory, a cluster summed into one, as spans of one bar the size of the host beside everything
else in use, easing to each poll, a press narrowing the table to the application with a chip to let
it go), state chips (online, stopped, errored) in the table's head that count and narrow, and a
table of every instance — crashing applications first, each drawn as its interpreter, its uptime
ticking, CPU and memory as a figure beside a short bar (memory against the limit PM2 restarts it at,
where it has one), its restart count rising when it moves with the unstable ones in red, *not saved*
where the saved list lacks it, fixed columns from `xl` with the mode folded into the name line below
`2xl`, every row one height — with start/reload/restart/stop inline and logs, scale, reset counters,
flush, delete behind the menu; a row opens the application's own page. The line and the band are
drawn while the list is read; a server without PM2, one where PM2 is installed and no daemon runs,
and a PM2 that does not answer each say so under PM2's mark with the next step
(`pm2-missing.tsx`). **An application's page** (`/processes/pm2/[app]`, `pm2-app-page.tsx`; the
name is the address, `?account=` only where two accounts run the same name, and the old `?app=`
address — an exact `account:id` or a bare name — redirects there): from `xl` the list folded into
a rail beside it (`pm2-app-rail.tsx`: the list's own search, state chip and order, a cluster one
row, held while the pointer is in it), Alt+↑/↓ and two arrows to walk it with the view carried
along; the identity line (the application as its interpreter, its name and state, its script, fork
or cluster of n, restarts and unstable restarts, uptime ticking) with one command — Start where a
worker is down, otherwise Reload — and the rest in a menu; then the views as the underlined strip,
`?tab=` in the address — **Overview** (`pm2-app-overview.tsx`: notices where PM2 gave up, where
some workers are down and where a reboot would not bring it back; *Where it runs*, the process
page's wired picture with the account's daemon and its boot hook on the left and each worker as a
process, the port they answer on, the log and the working directory on the right; *What it
takes*, four reading lines — CPU and memory summed over its workers from the process table's read of
each PID, memory against the limit PM2 restarts a worker at, disk rates and restarts — that hold
their place while it is not running; the command; how PM2 runs it; and whether it comes back
after a reboot), **Instances** (a cluster only: each worker a one-line card that opens its
process's page, with its own verbs in its menu and Scale on the title line), **Logs** (the service
logs on each worker's `pm2:` source under the record's own facts — restarts, unstable restarts,
uptime — with an Around the last start window only where PM2 stamps its lines, and the server's
reason instead of a socket where the logs are outside the log roots), **Environment** (read from
the running process, credentials masked as on a process's page) and **Control** (every verb as a
row that says what it does, and the startup list). A verb pressed on the page reaches every worker
in turn through the per-process routes (`pm2-app-actions.tsx`); a worker's own verbs reach it
alone. Below `xl` the page is the one pane and scrolls as a whole. Dialogs: a scale dialog
(`pm2-scale-dialog.tsx`) that is a stepper over the workers, those to start outlined and those to
stop struck, with one per core a press away, opened from a row, the Instances view or the menu; and
a start-application dialog whose interpreter and mode are
cards with their marks and which shows the `pm2 start` command it will run, coloured. Services: the machine's identity line (systemd's version, the
active and boot-enabled counts and when it booted as facts, the failed count as the verdict that
narrows the table to them, Reload unit files and the shortcuts at its right end), then
`procs/service-band.tsx`, bar readings — the five services using the most processor and memory as
spans of Live's bar the size of the machine, read from each unit's cgroup, and the last units to
start, stop, finish or fail since boot as ticks on the last day, the line saying when and why — over
the table, whose head counts active, failed, starting and inactive as state chips and whose toolbar
has the startup chips; failed units first, each drawn as what it runs with how long it has been in
its state (up for, failed since and why, its restarts) and its CPU and memory as a figure beside a
bar, a unit no file defines counted in the footer rather than listed, and new units rising while
existing units update state in place; clear-failed and enable/disable on a row, and a row opens the
unit's own page. **A unit's page** (`/processes/services/[unit]`, `service-page.tsx`; the old
`?unit=` address redirects there): from `xl` the table folded into a rail beside it
(`service-rail.tsx`: the table's own filters and search, failed first, held while the pointer is in
it, the unit on screen filled, each row its state in words — up for, why it failed, restarting — and
while it runs its CPU and memory), Alt+↑/↓ and two arrows to walk it, the view on screen carried to
the next unit; the identity line (the unit as what it runs with its state in the corner, its name to
copy, its description, who it runs as, whether it starts on boot, uptime ticking, its main PID as a
link to that process's page and its tasks, and at the end its state and its exposure score in its
tone, opening Limits and security) under Start/Restart/Stop and a menu of reload, clear failed,
freeze or thaw, enable/disable, mask or unmask, reload unit files when this one changed on disk, its
main process, its unit file and copy; then the views as the underlined strip, `?tab=` in the address
— **Overview** (`service-overview.tsx`: a failure, a restart loop, a mask, a unit file changed on
disk with Reload unit files, a frozen cgroup, a skipped condition and memory at its ceiling as
Notices; four live readings over the unit's recent windows, or its last run's counters; *Where it
runs*, the wired picture the process page draws (`wiring-picture.tsx`, shared): what starts it — a
timer or socket, the units it needs with a down one in red, the targets that want it — on the left,
and its main process, its other processes by program, what it listens on and where it writes on the
right, every node opening what it is; every command systemd runs for it, from ExecCondition to
ExecStopPost; how it runs — runs as, startup, type, restart policy and start limit, timeouts, how it
is stopped, watchdog, working and root directory, cgroup, unit file and overrides; and what it has
used since it started from its cgroup's counters), **Processes** (every process in its cgroup,
busiest first, the main one marked, each opening its page), **Journal** (the service logs on the
unit's journal with the **Runs** view — `unit-runs.tsx` over `lib/unit-runs.ts`: a week of the
manager's own lines about the unit, grouped by invocation into runs, how each ended, when, how long,
its exit, systemd's result, the restart count, CPU and peak memory, identical runs folded, a crash
loop said as a Notice, a run opening History on its own invocation — and sshd's unit refused to
anyone but an administrator in a Notice rather than a socket), **Dependencies**
(`service-views.tsx`: every unit it names under the question each answers — what starts it, what it
needs, what it starts, start order, what it cannot run with, reloads — each with its state from one
`systemctl show` of them all, a down requirement in red, a service opening its page and a timer its
schedule), **Unit file** (the file and each drop-in as systemd reads them, coloured as a unit file,
each opening in Files, with Reload unit files when they changed on disk), **Environment** (its
`Environment=` in the container's table, credentials hidden, then the files it reads, what it passes
through or removes, and a link to its main process's live environment), **Limits and security**
(`systemd-analyze security`'s exposure score and verdict in its tone, each setting most exposed
first, then what its cgroup lets it use against what it uses and each process's limits) and
**Control** (every verb as a row that says what it does — now, and on boot — every signal sent
through `systemctl kill` with what it will do to its main process from that process's signal masks
and a switch for every process in it, and its memory, processor and task limits set through
`systemctl set-property` until the next boot or kept); below `xl` the page is the one pane and
scrolls as a whole. Reload unit files (`daemon-reload`) is also on the table's identity line.
Scheduled: the machine's identity line (its distribution as its mark, the account's jobs and how
many are disabled, timers armed, the system cron lines, and the server's zone when it is not the
browser's — every time on the page is on the server's clock, the one its schedules are written in)
with what fires next across cron and the timers, its clock time and a countdown to the second at its
end, then **Next 24 hours** (`procs/schedule-band.tsx` over `lib/schedule.ts`): a chart of the next
24 hours an hour to a column, each schedule that runs in an hour one block in its kind's hue,
stacked, the hours named under the columns from Now, and one fixed line under it that says the day
in a sentence at rest and reads out the hour or block pointed at; under it **Up next**, the
schedules in the order they run, each its clock time, mark, name, schedule in words, kind and
countdown, six until Show all, pointing at a row lighting its blocks across the day; the kinds as
chips that count and narrow, and under the list what the chart cannot draw — work later in the week,
at boot only, switched off, and package cron lines left to systemd. A timer's later runs are read
off its calendar only where it has no random delay. Then one account's cron jobs as a table of the
job (the program's mark, the note or program as its name, the command under it), the schedule in
words over its expression, and the next run as a countdown over its clock time, per-job
enable/disable/edit/remove and the enabled and disabled counted as chips, an empty crontab saying
what a job is for with Add a job; the timers as a table of the timer (its unit's product, its name
and what its unit says it is for), its schedule in words with any random delay, next and last run,
and its state, saying "off after a reboot" only where it is; the read-only **System cron files** as
one table grouped under a row per file, `/etc/crontab`'s worked example left out, `run-parts` lines
named Hourly, Daily, Weekly and Monthly scripts, and a line guarded on `/run/systemd/system` said to
be "left to systemd" with the timer that does its work; and a **Cron log** (the service logs on the
cron daemon's unit journal, else a cron file, else the journal's `CRON` lines, through the cron lens
with its sessions hidden as a chip and its runs and lost output counted on its chips). Every
schedule opens a sheet built from one set of parts (`procs/schedule-sheet.tsx`): the schedule in a
sentence over the expression it was written as, its figures as tiles, **When it runs** — the next
five runs listed beside the week drawn as seven days with a mark per run and a count per day, a
timer's random delay a wash after each mark — then Command, Details and Runs. A job's (`?job=`) has
its next run and runs this week, and the cron log narrowed to its command; a package line's opens
the same sheet without verbs, and one left to systemd says so first and links the timer; a timer's
(`?timer=`) has its next and last run and how long that took, the service it starts, and the
service's Runs with Live and History. The job editor sheet asks what runs, then when — its frequency
a grid of toggles — over a preview of the schedule in words, its week and its next three runs

**Where there is no systemd** (a container, a host started by another init, or the dashboard's own
container without `/run/systemd`), the Services list, a unit's page and Scheduled's Timers draw
one screen (`procs/no-systemd.tsx` over `MissingThing`): the systemd logo, why, **Open Processes**
and the mount to copy. A unit's page is that screen whole, with no rail, whether the list answered
`available: false` or the unit's read answered `systemd_not_running`, and systemctl's own refusal
is behind *What the server said*; on Scheduled it is one block under Timers and the cron jobs
above it are drawn as usual

**Implementation:** processes layout and pages, `components/procs/` (`process-page.tsx`,
`process-rail.tsx`, `process-overview.tsx`, `process-views.tsx`, `process-detail.tsx`,
`process-facts.ts`, `wiring-picture.tsx`, `services.tsx`, `service-page.tsx`, `service-rail.tsx`,
`service-overview.tsx`, `service-views.tsx`, `service-facts.ts`, `service-band.tsx`, `pm2-band.tsx`,
`pm2-shared.ts`, `pm2-facts.ts`, `pm2-app-page.tsx`, `pm2-app-rail.tsx`, `pm2-app-overview.tsx`,
`pm2-app-views.tsx`, `pm2-app-actions.tsx`, `pm2-scale-dialog.tsx`, `pm2-start-dialog.tsx`,
`pm2-missing.tsx`, `unit-runs.tsx`, `units.ts`, `schedule-band.tsx`, `cron-job-sheet.tsx`,
`timer-sheet.tsx`), `components/verbs.tsx`, `lib/cron.ts`, `lib/crontab.ts`, `lib/schedule.ts`,
`lib/unit-runs.ts` (`units.ts`, `pm2-shared.ts`, `pm2-facts.ts`, `process-facts.ts`,
`service-facts.ts` and these unit-tested); checked by `tests/browser/processes-ui.spec.ts`,
`tests/browser/pm2-application.spec.ts` and
`tests/browser/services-ui.spec.ts`

### `/proxy/*`

Proxy overview, Sites and site details, Certificates, Streams, Ports, Configuration, TLS tools and
Traffic show observed inventories and records. Certificates keeps its views in the address
(`?tab=`: certificates, renewal, DNS challenge, ACME accounts, local CA, signing requests, watched
domains), and a certificate is a page of its own, `/proxy/certificates/<name>` with `?path=` where
two share a name (`certificate-page.tsx`, its rail and picture; the shared console, forms and verbs
in `certificate-work.tsx`, the certificates layout); the sheet's old `?cert=<file>` lands there. Alerts (`/proxy/alerts`, administrators) is its own
page: what the rules watch wired to the channels told, what fires with its mute, the rules edited in
a sheet, and a test per channel (the parts that do not know the proxy's kinds are
`components/alerts/alert-parts.tsx`, shared with `/metrics/alerts`); the overview's line says what
fires and leads there, and its menu has Alerts. Ports (`/proxy/ports`) opens on its counts, a picture of which
networks reach which listening programs and the list of sockets; each listening port has its own page
(`/proxy/ports/[port]`, keyed `tcp-443` or `udp-53-127.0.0.53` where two rows share a port) with its
reach, owner, clients and history, and the list as a rail; the old `?socket=` sheet address opens
it. Site/stream configuration, certificate issuance/renewal,
requested TLS scans, nginx tests/lint and traffic diagnostics keep their own controls and
authorization. The overview has no automatic attention list or domain suggestions. Certificate
hygiene and finding snoozes are removed; nginx Settings exposes current values and edit constraints
without advice or recommended values

Streams (`/proxy/streams`, 0.8.7) opens on its identity line — the forwards as the services their
ports are, the stream module's state, how many forward and how many are open to anyone, the hour's
sessions, the verdict (a press narrows the list to what it counts) and New stream — then the wiring
picture of the host's ports through nginx to where they go (`stream-picture.tsx`), the module and
include steps where they are still to do (`stream-setup.tsx`), and the forwards as one-line rows,
each a way to its own page. Without nginx it is the overview's install screen (`engine-setup.tsx`).
A stream's page (`/proxy/streams/<name>`, `stream-page.tsx`, with the list as a rail from `xl`,
`stream-rail.tsx`) holds what the traffic sheet and the edit form did: Overview (the forward drawn,
what it carried as reading lines and its sessions and outcomes), Clients (with Deny or Allow),
Connected now, and Settings (the form as a settings column with one save bar, administrators only),
`?tab=` in the address. New stream stays a sheet (`stream-form.tsx`); the verbs both places share are
`stream-verbs.tsx`, and what they say is `stream-facts.ts` (unit-tested). `?stream=<name>` lands on
the stream's page; checked by `tests/browser/proxy-streams.spec.ts`

Configuration (`/proxy/config`) opens on nginx's directory as an identity line with Test config, the
include picture over the files by folder, and for administrators What nginx loads, Settings (one
column, one save bar, a reload switch), Lint and History as `?tab=` views; a file's revisions and
restore are the config editor's History view (`?view=history`, the old `?history=` link lands
there). Tunnel (`/proxy/tunnel`) opens on the tunnel's identity line with Check now, the picture of
hostname → Cloudflare's edge → connector → this server, and the hostnames as one-line cards; not
connected, the dashed picture, a choice of token or API and one form (administrators only).

Sites (`/proxy/sites`, 0.8.7) lists every site as a one-line row (`SiteRow` in `vhosts-panel.tsx`):
what it fronts with its engine in the tile's corner, its name over where its requests go, then its
hour, ports, TLS, certificate and state in aligned columns, every verb in its menu. New site is the
line's command, Import and Export all are behind its menu, and under the list Unknown hosts,
Password files (`auth-files-panel.tsx`), Access lists (`access-lists-panel.tsx`), the URL resolver
and Deleted & backups (`site-backups.tsx`) are titled sections of one-line rows; a password file's
logins and an access list's addresses, sites and include line are in the sheet its row opens. A
site's page (`/proxy/sites/<name>`, `site-page.tsx`, with the list as a rail from `xl`,
`site-rail.tsx`) keeps its views in the address (`site-tabs.ts`, unit-tested): Overview, Logs
(`?tab=logs`) and, for an administrator and a file the form saves back, Settings (`?tab=settings`).
Settings is the site form (`SiteSettings` in `site-form.tsx`) as one column of sections split across
parts (`&view=` general, https, access, hardening, headers, paths, file), with a strip naming the
parts that hold unsaved changes (`site-sections.ts`, unit-tested) and one floating bar holding the
form's saves (`site-settings.tsx`); leaving it with unsaved changes asks first. A new site, or a copy
of one, is still made in a sheet over the list (`SiteForm`, the same fields). `?site=<name>` lands on
the site's Settings for an administrator and a file the form owns, and opens the config viewer
otherwise. Checked by `tests/browser/proxy-site-page.spec.ts`, `proxy-site-builder.spec.ts`,
`proxy-sites-list.spec.ts` and `proxy-sites-tools.spec.ts`.

Every page of the section opens on an identity line laid out as a project's header (0.8.7): the
name with its state beside it, the facts under them, one command at the end and the rest behind
`⋯`. The overview's command is Reload (Start while the engine is stopped), and Test config, Alerts,
Refresh, Service details, Restart and Stop are in its menu (`useEngineCommands` and `EngineMenu` in
`engine.tsx`); Alerts' is Add rule, Streams' New stream, Ports' Find a free port, Certificates'
Issue certificate, the TLS fleet's Scan all sites, Configuration's Test config and Tunnel's Check
now. Traffic's old `?site=<name>` lands on that site's page at its Logs view; checked by
`tests/browser/proxy-opening-lines.spec.ts`

**Implementation:** `app/(dashboard)/proxy/`, `components/proxy/`; backend in
[`../backend/proxy.md`](../backend/proxy.md#proxy)

Traffic (`/proxy/traffic`, every account) is the last hour across every site from
`GET /proxy/traffic`: requests a minute, the failing share, the slowest site's response time in
words and bytes sent, the hour by the minute, the sites busiest first (each opening its site page,
where its requests are read), and nginx's error log grouped from `GET /proxy/errors` over a window,
for every site or one (`?tab=errors&site=`). The old one-site address `?site=<name>` redirects to
`/proxy/sites/<name>`. Checked by `tests/browser/proxy-traffic.spec.ts`; the logic is
`components/proxy/site-traffic.ts` (unit-tested)

### `/network/*`

Eighteen reporting pages: topology/overview, interfaces/namespaces, IPv4/IPv6 routes and policy
rules, monitored egress groups, firewall, gateway/NAT, protections/blocklists,
WireGuard/Tailscale/Headscale, resolver/hosts/lookup and private retained native DNS policy/trust
evidence, traffic/shaping/eBPF, connections and tools. Connection path (a tool of the Tools
workbench since 0.8.7; `/network/investigate` redirects to it, and the command palette finds it by
its name through `TOOL_ALIASES` in `components/command-search/model.ts`) adds source/container
evidence with explicit basis and scope, the native nginx stream on the destination port as observed
evidence with the backend leg nginx logged for a measured connection, and each forwarding site's
service policy; Saved runs adds bounded durable lifecycle, retention, export and compatible
comparison, each run a page of its own (`/network/runs/[id]`, `components/network/run-page.tsx`:
Result, Compare, History and Details, with the list as a rail), and lists the **Watched probes**
under them: TCP endpoints the proxy watch list's own
monitor connects to on its schedule, with each connect time, check-now and stop for administrators
(`components/network/watched-probes.tsx`, `lib/watched-probes.ts`), started from the TCP port
check's **Watch on a schedule**; Packet captures adds bounded immutable scope, dated native
progress/cleanup, original private PCAP access and separately redacted support export, each capture
a page of its own (`/network/captures/[id]`, `components/network/capture-page.tsx`: the interface,
filter and file as a picture, the record on a second view); reload never starts capture. Drift adds desired/render/runtime identities, measured activation and reviewed
selected owned repair with mandatory reconnection. Egress groups (`components/network/egress/`) are
a list and a page a group: `/network/egress` opens on an identity line (how many of the six slots
are used, how many carry traffic, how many switch on their own, members proven) over one card a
group (its carrying member as the state, how it switches, its verbs in a menu), and
`/network/egress/[name]` is the group: the carrying member as its state, **Run simulation** as the
command, a `WireFlow` picture of the selected traffic, the group and its members (the carrying wire
moves, a failed member is dashed), then views in the address (`?tab=`): Members (state, round trip,
loss and a strip of recent samples a member, the rules' current advice, readiness, what each member
is bound to and how the group decides), Decisions (a timeline whose evidence opens on the routes
before and after, the measurements and the connections moved, pinned and flushed) and Simulation
(the last run's expectations and phases). From `xl` the list stands beside it as a rail; Alt+↑/↓
walk it with the view kept. Destructive confirmations guard fail over/fail back now, enable,
disable, removal and turning automation on, which stays disabled until a passed simulation of the
current configuration; an account that cannot use a verb is not shown it.
Address planning joins held reservations with fresh overlap coverage and native owner
handoffs; External checks retain scoped signed source evidence; Socket history defaults off and
separates native TCP deltas from explicitly reviewed kernel TCP/UDP payload subtotals, retained
ownership and coverage gaps. Traffic explicitly distinguishes download policing from owned IFB/CAKE
SQM. It opens on the uplink's identity line (what it carries in words, the newest reading's age as
its state, whether history is kept) over four views in the address (`?tab=`: Bandwidth, Budgets,
Shaping, and the eBPF programs, each of which is a page, `/network/traffic/ebpf/[id]`, with the
list as a rail). Uplink packets and TCP's resent share and round trip are two reading lines, the
window is in the address (`?window=`, or a typed or dragged `?from=&to=`) and a recorded one says
its throughput spread in words with (admin) change/incident marks, devices carry alert-only
transfer budgets set in a sheet, programs say what a read could not see and list UDP peers without
bytes, every container opens its own page (`/network/traffic/containers/[name]`: chart, service,
attributed peers, the other containers as a rail), each queue is a one-line card whose sheet holds
its evidence for any account and the form for an administrator: what the next change does with it
(kernel default, preserved fq_codel, refused foreign tree) and the parameters drift changed in
place, CAKE shows its measured per-class delay and takes
an upload profile, congestion control is compared on live sockets before and after a switch, eBPF
programs open into maps, attachments and cost with the dashboard's observer marked, and an install
is followed by configured/active/verified phases (`components/network/traffic/`, `install.tsx`);
Connections opens on its identity line and picture, lists each address as a one-line card, gives an
address a page of its own (`/network/connections/[address]`: its sockets, closes and layers, a past
hour from socket history), and blocks with a reason, an end and an incident
(`components/network/connections/`); VPN
opens on its own line and shows one kind at a time as a view in the address (`?tab=wireguard`, the
default, `tailscale`, `headscale` where this server runs one, `log`; `components/network/vpn/views.ts`),
each kind on its own identity line with no tile row, opts in to IPv6 addressing and separate
egress with per-family evidence and translated-connection counts on the row that switches that
family's exit; each tunnel's picture marks stale always-on handshakes, captured transports and
passed budgets on the peer they are about, a tunnel has a page of its own (`/network/vpn/<name>`:
endpoint evidence, trend and what each peer moved, lifecycle with outcomes, kept for a removed
tunnel beside its restore), and each peer's sheet adds its trend, endpoint history, alert-only
budget, editing (a device's regenerated configuration), site verification and a Linux kill-switch
download, with archived tunnels restorable, tailnet machines and Headscale nodes as one-line cards,
the exit-node offer and subnet routes as settings on one save bar, a Tailscale that waits for
sign-in offering the address it waits at, and Headscale approval and key expiry read-only
(`components/network/vpn/`: `tunnel-page.tsx`, `tunnel-reading.ts`, `tailscale-offers.tsx`,
`peer-actions.tsx`, `archive.tsx`). A session opened through Just Hub is not offered what the server
refuses it there (adding or editing a peer, a peer's configuration, restoring a tunnel) nor, on DNS,
connecting an engine or changing its connection: `components/network/hub-session.ts` reads the
account's own sessions once and finds the one in use. Twenty-six admin-only server diagnostics, each rendering one
shared structured result (verdict, checks, findings with owners, facts with their basis, tables with
in-product row links, metrics and limits) with raw output behind a disclosure, a server-held quick
result that saves without rerunning, trusted SSH fingerprints and saved Wake-on-LAN devices; an
exact IPv4/IPv6 subnet calculator available to read users with local prefix comparison and an
admin-only shared-IPAM overlap check; five new host/LAN tools, IPv6 GRE with tunnel keys and packet
limits, VXLAN multicast and bridge membership. Backend permissions/ownership/path guards govern
mutations; read-only users retain inventory; failed reads retain data with a dated retry warning.
The routing diagram switches address family and labels inferred highlights; a read-only kernel
lookup explains target/source/mark selection. Tailscale route drafts survive rejected writes and
retry against current preferences. Docker attachment candidates distinguish failed reads from an
empty list. The overview adds per-family ways out (NIC source apart from the unobserved provider
identity, forwarding per family), connection-tracking paths in use that light the nodes a pointed
node trades with, the live ring's observation age, chart selections that drill into the devices that
carried them, explicit errors for failed readings. Interfaces flag incomplete Docker joins, read a
device's driver, offloads and error counters, show a bridge as a switch (VLANs by port, FDB) with a
native/tagged VLAN policy for managed ports, preview a bridge move before it is offered, check
tunnel/macvlan/dummy readiness without sending traffic, edit a unicast VXLAN's flood ends and open a
namespace as its own network (routes, resolver, listeners and a kernel route question) with handoffs
between veths, namespaces, the investigator and the routing form. DNS lookup follows effective
resolver policy; direct comparison requires named destinations and disclosure acknowledgment.
Gateway separates installed readiness from measured reachability (a target check from this server
and, for administrators, external evidence after the last change), previews each forward or NAT
change's impacts and modeled policy layers while the form is edited, offers one-to-one, IPv6 prefix
and destination-scoped NAT, flags a drifted auto source-translation decision with its re-decide, and
labels live counters by table generation beside totals carried across reloads. Protection
(`components/network/protection/`) opens on an identity line (what the lists dropped and the limits
refused, New blocklist as its command) and a `WireFlow` picture of who is refused, this server's
filter and what gets through, and has three views in the address (`?tab=`): the lists, limits,
exceptions and trusted addresses as one-height rows, the connection table (administrators), and the
kernel's protections as a settings column on the shared save bar. It adds
service-profile limits with a ceiling for everyone, meter occupancy and per-limit refusal trends,
scoped expiring exceptions, trusted-address notes with staleness review, feed schedules, signatures,
diffs and provenance (a list's standing and enforcement evidence head its editor), a list preview
naming local networks and open sessions, explicit session revocation, workload kernel profiles
staged beside hand edits with per-interface effective values, and the connection table's band,
charts, shares and what they point at. Private verification is available,
and IPv4 WireGuard full tunnels explain IPv6 leak blocking. DNS and VPN each keep their service's log
as a view and Interfaces ends on its own — the resolvers, tunnels and link daemons the page lists, read in the shared
`ServiceLogs` pane ([`docker-files-logs.md`](../backend/docker-files-logs.md)). The [complete
inventory](../../audits/2026-10-08-network-audit/README.md) distinguishes UI, API and inspection
limits

**The three evidence pages share one layout (0.8.7).** External checks, Address planning and Socket
history each opened on four tiles over a paragraph, a form and a framed table, with their forms folded
into disclosures. Each now opens on an identity line (`HostIdentity` in its project layout, its facts
through `IdentityFacts`) whose state is the page's verdict and whose one command is its main verb, and
none has a `StatGrid`:

- **External checks** (`components/network/external/`): the picture is the enrolled sources, the
  internet and this server's approved services, each line the last check's outcome (`reading.ts`:
  reached, a measured stage refused, partly unknown, in flight, or no measurement, which is dashed and
  never red). Checks are one framed table, a line each, the chosen one read out under it; sources are
  `ChoiceRow`s whose press runs a check from them. Run a check, Enroll a source (the one-use credential
  is shown in the sheet that made it and stays reachable from the source's menu until it expires) and
  Compare are sheets. Revoke needs `destructive` as well as `system.admin`.
- **Address planning** (`components/network/ipam/`): named as the rail names it. A pool is a card
  holding one `BarReading` of planned, observed and free blocks with the server's exact counts and its
  owners as logos (`reading.ts` joins a pool's held plans with the native observations inside its
  network); the prefix preview is a field over the bands; reservations are `ChoiceRow`s that link to
  the Docker or WireGuard creation form while they are only reserved (`?ipamReservation=`); coverage is
  a framed table. New pool and Reserve are sheets. Retire and Release need `destructive`.
- **Socket history** (`components/network/flows/`, `components/network/kernel-observer.tsx`): with the
  recorder off and nothing kept, the page is the picture of what would be sampled and kept, one
  sentence and Start recording. With a record, the picture is the latest sample's sources, the
  recorder and what it keeps; the UTC day and the two filters are the table's toolbar; a socket hour
  is one line and the chosen one is read out under the table; the totals of the rows shown are the
  table's foot, native counters and kernel payload apart; the day's twenty-four hours are a strip
  that reads one hour on a fixed line. Sampling policy is a sheet, Erase is in the menu, and the
  kernel observer keeps its own reviewed activation. Under the picture it is three views in the
  address (`?tab=`, `flows/views.ts`): the day's socket hours with its recorded hours (the default),
  the kernel observer, and the latest sample with the measurement boundaries. A node of the picture
  opens the view it stands for.

Every reading these pages gave as "Unknown" is still given as "Unknown": a check that expired or was
cancelled measured nothing, a UDP row has no byte counters, and an hour with no block was not sampled.

**The overview, Firewall, Gateway and Drift open on a project's line (0.8.7).** None keeps a row of
tiles. The overview's line says whether the uplink is up and has *Run a check* as its command; In
and Out are two reading lines, the connections are on this server's node, and Ways out is a line a
family. Firewall draws its backend as netfilter, the filter it drives, says what its log counted on
the picture's nodes (each a press that narrows the log), keeps its defaults as one settings section
under the rules saved by the page's save bar with the inbound confirmation and preflight unchanged,
opens a recorded change's request and a rule's history as popovers, and offers ufw's install where
there is no firewall. Gateway's line carries the forwarding families, the forwards and NAT in force
and firewall admission as a word with the policy evidence behind it; each row carries its own state
word. Drift's boot unit is one line with its activation behind a press, and its repair plan is
one-line rows with a checkbox column. Controls on destructive routes are drawn only for an account
with that capability, and a reader is drawn none it cannot use. Checked by
`tests/browser/network-overview-firewall.spec.ts`.

**Implementation:** network pages, `components/network/`, shared security firewall/connection
components; checked by `network-*.spec.ts`, `security-ui.spec.ts` and `design-system.spec.ts`

### `/security/*`

Security overview, SSH, Intrusion and Logins show the observed access arrangement, firewall/service
state, sessions, login records and enforcement evidence. The access-boundary report remains
available. SSH edits are reviewed and protected by lockout guards; intrusion controls keep their
capabilities, confirmation and audit. Automatic posture findings and settings recommendation
comparisons were removed in 0.7.5

Since 0.8.7 the overview is its identity line, the picture of the ways in and the access-boundary
report: the picture's nodes carry the readings (connections, firewall, fail2ban, sshd, who is signed
in) and their marks link to Connections, Firewall, Intrusion, SSH and Logins. Logins
(`/security/logins`) opens on this server's identity line and a picture of where sessions are held
from, who is attacking and which accounts are in; then open sessions (End session, `system.admin`
and `destructive`), attackers ranked by attempts (Block at the firewall and the address lookups in
each row's menu; an address the firewall already denies reads "blocked"), and the login record
(Successful / Failed for administrators as `?failed=1`, a filter as `?q=`, new records held behind
"N new logins · Show"). Checked by `security-logins.spec.ts` and `security-ui.spec.ts`; captures of
every state with `JD_SECURITY_SHOTS=<folder>` and `security-logins-shots.spec.ts`

Intrusion (0.8.7) is an identity line, a picture of the internet, this server and the three
engines, and one engine at a time as a view in the address: `?tab=fail2ban`, `crowdsec`, `suricata`
or `blocked` (every engine's refused addresses folded by address). Without `tab` it opens on the
first engine that is installed. A jail is a page, `/security/intrusion/jails/[jail]`: what it holds
with Unban and Never ban again on each address, its policy, and Ban an address, Tune and Release
every ban as its verbs. One form bans through CrowdSec or a jail. A stopped fail2ban is started
from the page (`POST /systemd/fail2ban.service/start`, `service.control`) and an absent one is
installed from it. SSH (0.8.7) stages its directives in one column and applies them through the
shared save bar and a review dialog; `?setting=<key>` still lands on a directive.

**Implementation:** `app/(dashboard)/security/`, `components/security/`; backend in
[`../backend/observability-security.md`](../backend/observability-security.md)

### `/system-users`

Host accounts, under the host's identity line (how many can sign in as its state, New account as its
command, Groups and Account defaults in its menu) and, only when there is something to change, a list
of findings each with where to change it. Then the ways to root as one picture (`RootPicture`, a
`WireFlow`): the accounts that become root on the left, by sudo or by a group that is root by another
name (`docker`, `lxd`, `libvirt`, `disk`), each line in the tone of how strong it is; root in the
middle; and on the right whether root itself signs in by password and by key. Under it every account
is a one-line card (`ChoiceRow`) that opens the account: who can sign in and how (password, keys, or
the one thing closing them), how far each reaches root (read from the sudoers files), and when each
last signed in, in columns from `xl` and on two lines of their own under the name below it, so no
account is taller than another. Root is in the list however the system-accounts chip is set, and
daemons are a shelf of their own drawn as their products. A row's checkbox selects it for lock,
disable, enable, end sessions and delete (one request at a time); its menu goes to that account's
terminal, files, settings and history. The page has its shape while it waits: the line, the picture
and the rows are drawn at the height the last read gave them.

The whole section is `system.admin`, reads included. An account without the role is shown a closed
door on every page of it (`read-state.tsx`) rather than controls that fail.

**Implementation:** `app/(dashboard)/system-users/page.tsx`, `components/system-users/access.ts`,
`marks.tsx`, `root-picture.tsx`, `wire-marks.tsx` and `read-state.tsx`

### `/system-users/user/[name]`

One account, on the shape a service's page has: a way back, its identity line (sign-in state beside
the name; a terminal as that account as the command, `/terminal?as=<name>`, which `/terminal` passes
on as `asUser`, or its files where it has no shell; Files, Disable or Enable, End sessions and a
delete that reads what would go first in the menu), and three views as an underlined strip with the
one on screen in the address (`?tab=`). **Overview**: its password and every SSH key as rows of one
list (set or generate a password, require a change, lock, unlock only when there is a password
behind the lock, remove; add a key by paste, GitHub import or generation, with label, restrictions,
last use and a weak-key warning, and give it to other accounts); its routes to root with the file
each rule is in, its own sudo rule, and its groups; what it is running right now, drawn only when it
is running something. **Settings**: profile, password rules and limits as one centred column of
sections under one floating save bar; they are still three writes, and the bar sends each form that
holds an edit. A disabled account's expiry is not offered and not sent, since an empty one is how an
account is enabled. **History**: sign-ins, refusals, sudo commands and dashboard changes on one line
of time, narrowed by chips; an account with nothing on record is drawn as itself with one sentence.
From `xl` the accounts stand in a column beside it (`AccountRail`) with the list's own search, filter
and order; Alt+↑ and Alt+↓ walk them and the view on screen goes along. A system account is read-only
until `Allow changes` is pressed.

**Implementation:** `components/system-users/account-page.tsx`, `account-rail.tsx`, `detail-shell.tsx`
and the `account-*.tsx` views

### `/system-users/new`

A flow page: what the account is for, as templates (the operator's own beside the built-in ones),
then its name, how it signs in, its root access, groups and shell in the one focused surface, ending
in Create with Save as template beside it. The column beside the form (`NewAccountPicture`; under it
below `xl`) draws what the answers add up to: the way in, the account, how far it will reach. An
account nobody can sign in to has to be asked for by name.

**Implementation:** `components/system-users/new-account.tsx`, `new-account-picture.tsx`

### `/system-users/groups`

The host's groups under an identity line that says how many reach root (a press narrows the rows to
those) with New group as its command. Each group is a one-line card: its mark, its name over its
number, what it hands over in its tone, and who is in it; on a phone what it hands over takes the
line under the name. What reaches root comes first; a private group is hidden and system groups are
behind a chip. A row opens the group's page; `?group=<name>` lands there too.

**Implementation:** `app/(dashboard)/system-users/groups/page.tsx`, `components/system-users/groups.ts`,
`group-parts.tsx`

### `/system-users/groups/[name]`

One group, on the same shape as an account: its identity line (what it hands over as its state, its
number, how many are in it, whether a package made it), three views in the address and from `xl` the
groups in a column beside it (`GroupRail`) with the list's search and switch, walked by Alt+↑ and
Alt+↓. **Overview**: who is in it, the group, and what being in it reaches as a picture, then the
sudo rules that say so, each with its file and line. **Members**: a switch per account (those already
in it that the people's list does not hold as well); an account it is the main group of is said, not
offered. **Settings**: name and number, held for a package's group until `Allow changes` and never
offered for one the system depends on, and deletion. Membership, name and number are one write, saved
by the one floating bar whichever view the edit was made in. It was a sheet over the list until
0.8.7.

**Implementation:** `components/system-users/group-page.tsx`, `group-rail.tsx`, `detail-shell.tsx`

### `/system-users/defaults`

What an account is born under, as one centred column: New accounts (default shell, grace after a
password expires, what `/etc/skel` holds with a link to it) and Passwords (how long one lasts, how
soon it can change, the warning, the shortest allowed and how many kinds of character), each head
naming the file it writes; one floating save bar; and the operator's own templates, one line each.

**Implementation:** `app/(dashboard)/system-users/defaults/page.tsx`, `components/system-users/fields.ts`

### `/terminal`

Direct PTY sessions held on the host so they outlive the dashboard (opened and closed, nothing else
— no folders, renaming or pinning), windows with retained screens and connections while switching,
directional nested splits with pointer/keyboard divider resizing and focused input, Codex/Claude run
in the focused shell (a sibling window only when that terminal is holding a program), a window strip
that scrolls without a scrollbar behind edge fades and chevrons, tabs and rows named after what the
shell is doing (without the agent's own title glyph) with a working mark and the running program's
own logo (a terminal's where it has none), a Files and Git companion column (full-width tabs; files
open with a floating Save that appears only once edited; the Git tab is the repository's changes
read file by file, branch switching and checking out an open pull request, with a link to the Git
page for everything else), the last-open session and window remembered across navigation, side
tools, replay, a dropped socket reconnecting by itself, clipboard upload, renderer, and keyboard
customization. Below `lg` the frame is the screen and shows one pane at a time over the shared bar
(Sessions, Terminal, Files, Git with the count of changed files), opening on the emulator; an
emulator that is put away keeps its socket and its grid and is fitted again when shown. Turned off
in Configuration, the page is the missing-thing screen with the terminal's mark and Open
Configuration for an administrator

**Implementation:** terminal page, `components/terminal/`, `components/xterm-pane.tsx`; see
[`features-terminal.md`](features-terminal.md#the-terminal-panel)

## The Network pages in more detail

The DNS page (`components/network/dns/dns-page.tsx`) opens on the resolver's identity line: its name
(systemd-resolved, or `/etc/resolv.conf` where programs do not ask the stub), the address programs
ask, where a lookup goes in words ("Cloudflare over opportunistic TLS") and the share answered from
the cache (`reading.ts`, unit-tested), with **Resolve a name** as the command and the engines' two
verbs in the menu. Under it are five views named in the address (`?tab=`, `views.ts`); the anchors
older links carry (`#dns-links`, `#dns-upstreams`) open the view their section moved to.

- **Resolver**: the chain on `WireFlow` (`resolver-chain.tsx`), with the cache share and the lookup
  count on the resolver's node and the failures and DNSSEC verdicts as two `ReadingLine`s under it;
  what answers on port 53; each link's scope as a one-line card that, for an administrator, opens the
  link's split DNS through its native profile as a temporary apply (`link-scopes.tsx`, `split-dns.ts`,
  unit-tested); `tls-check.tsx`, each configured DoT server's certificate; and `resolver-owner.tsx`:
  who writes `/etc/resolv.conf`, what programs ask, whether the page's resolved drop-in reaches them,
  the owner's own place to change DNS and the evidence and conflicts behind the verdict.
- **Lookups** (`lookups.tsx`, `?tool=`): one name shared by three lookups, each keeping its answer.
  The lookup compares named resolvers over DNS over TLS with the DO bit and asks before sending a
  private name to unseen forwarding, an acknowledgement that holds only for the name it was given
  for; the policy investigation and `dnssec-chain.tsx` are an administrator's and are not offered to
  anyone else.
- **Settings** (`settings-view.tsx`): one column of `SettingSection`s on the page's one save bar.
  `upstreams.tsx` is the resolver's form (public resolvers, servers, fallback, domains, clearing each
  list on purpose, DNS over TLS, DNSSEC and the cache as `Segments`, up to eight verification names);
  Save shows the server's verification plan in the confirmation, and every check's result, or the
  failed check of a change that was put back, lands at the head of the column (`verification.tsx`).
  It needs `system.admin` and `destructive`, as its routes do. The host records editor is the second
  form: it previews overlaps before saving and checks local NSS resolution after.
- **Services**: the ad-blocking cards hand a detected AdGuard Home, Pi-hole or Technitium to its
  native connection (the card opens it) or to a seeded connection form (Connect), over the connected
  engines and the owned setups. Connection sheets add `service-dhcp.tsx`, the engine's read-only DHCP
  ranges and leases.
- **Log**: resolved's unit, the resolver programs and the DNS containers (`log-sources.ts`).

Every block waits for the first read at the height it had on the last visit (`ghost.tsx`,
`shapeOf`). Checked by `tests/browser/network-dns.spec.ts`, `network-dns-evidence.spec.ts` and
`network-dns-services.spec.ts`.

The DNS page's Services view mounts `components/network/dns/services.tsx` for administrators, including while
the host resolver reading is unavailable. Native connections default read-only; replacement requires
the complete native credential and selected custom CA again. A connected engine is a page of its own,
`/network/dns/services/<id>` (`components/network/dns/engine-page.tsx`, `engine-rail.tsx`,
`engine-views.ts`): the engine as the product it is on the identity line, Review a change as its
command, updating the connection and disconnecting it in the menu, and its views in the address
(`?tab=`): Overview (owner, runtime, listeners, upstreams, limitations), Clients (access, client
settings, native views, named networks/translations, filtering groups), Records (authoritative zones,
local overrides), Queries (the bounded native history), Filters, DHCP and Changes. Each keeps its
native evidence basis and unknowns. A retained native change's review is part of Changes
(`?change=<id>`). From `xl` the connected engines stand beside it as a rail, and Alt+↑/↓ walk them
with the view kept. The connection's form, the owned Docker setup's form and an owned setup's review
stay sheets.
Apply requires a fresh matching owner/policy, unchanged reviewed intent and explicit confirmation;
an attempted apply stays held across navigation/reload through account-scoped browser attempt IDs.
Only explicit native `verified` state reports verification. Owned removal separately names the
container, bridge and both data volumes and refuses changed identities. This UI exposes the backend's
closed supported actions; broader zone/view/client editing remains outside this first handoff.
Record/client controls in `service-policy-form.tsx` extend that scope with A/AAAA local overrides,
explicit unsigned Primary-zone records and TTL, and existing Pi-hole client group assignments.
The closed actions are `override_add`, `override_remove`, `record_add`, `record_remove` and
`client_groups`, validated by `lib/network-dns-service-policy.ts` and the retained DTO parser.
Authoritative zone reads show unsupported RR types without offering edits. Empty group membership
is an explicit request. Drafts survive field/native refusals; error summaries focus and link to the
associated controls. Retained review polls `/changes/{id}/current` for the selected native policy,
holding stale or changed confirmations and showing human record/client details before and after.
The [record/client acceptance](../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-service-policy-ui-acceptance.md)
keeps its final source, selected browser checks and actual native engine/current-read proofs separate
from the earlier handoff below. Explicit null and empty client comments have distinct labels.
Custom-domain controls in the same policy form add `filter_add` and `filter_remove` for AdGuard and
Pi-hole. The engine fixes the match: AdGuard covers the domain and its subdomains, while Pi-hole
matches the exact domain. Pi-hole additions use explicit existing native memberships, including
`[]` and ID zero; removals omit replacement groups. Unknown native group inventory holds additions.
A selected group absent from a configured refresh stays in the draft until explicitly removed;
the removal returns focus to the membership fieldset and preserves the domain and disposition.
Retained reviews distinguish absent, ambiguous and other-target policy with owner/exact counts,
and show native enabled/groups/comment metadata and configured
evidence before/readback. Full selected fingerprints and metadata participate in current-read and
open-confirmation checks; configured rules do not establish precedence or measured client filtering.
Technitium custom-domain controls remain unsupported. The
[assembled custom-domain acceptance](../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-service-domain-filter-ui-acceptance.md)
records the fresh production build, all 24 interactions, current-read holds, twelve captures and
separate exact-source AdGuard/Pi-hole configuration/restart proofs. Broader P17 acceptance remains open.
`service-filters.tsx` adds a separate native filter reading on read-only or managed connections:
redacted subscription origins, IDs, counts/update metadata, custom-rule fingerprints and explicit
native group memberships. Configured inventory, native status, unavailable/manual/app scope and
unmeasured client decisions stay separate. Failed reads retain dated same-owner data; changed
connection identity clears it. Unknown sections carry no zero-policy count or healthy verdict.
The [filter UI acceptance](../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-service-filter-ui-acceptance.md)
keeps its production build, strict decoder, browser captures and actual native metadata proofs
separately attributed.
Its [source-matched build, 13 interaction cases and phone/desktop inspection](../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-services-ui-acceptance.md)
pass, as do the [combined reachable checks](../../audits/2026-10-08-network-capability-report/implementation-evidence/native-dns-integrated-acceptance.md).
Broader P17 acceptance remains open.

**Interfaces** (`app/(dashboard)/network/interfaces/`, `components/network/interfaces/`) opens on one
line (how many devices, which is the uplink, how many were made here, **New device**), lists the
devices by what they are for as one-line cards, then the namespaces as cards with their device count
(a namespace's routes, resolver and listeners stay its sheet) and the link log. A device is a page,
`/network/interfaces/[name]` (`device-page.tsx`; the old `?device=` address redirects there), on the
process page's shape: its line with its state and guard, **Set up** as the command of a device that
is down, **Set down** and **Delete** in the menu; the views **Traffic** (a `WireFlow` picture of what
it is wired to, the last fifteen minutes, what it carried since it was made, link errors, a tunnel's
readiness), **Addresses**, **Ports** (a bridge's ports as cards, the switch and its forwarding
database), **Settings** (MTU, bridge membership, VLANs and flood ends as sections under one save bar,
each its own write and a destructive one confirmed; the driver) and, for an administrator,
**History** (the audit trail's `network.link.`, `network.address.` and `network.native.` entries for
that device, narrowed in the page) with `?tab=` in the address; and from `xl` the list as a rail
(`device-rail.tsx`) with the list's own search and Everything switch, Alt+↑/↓ to walk it. The pure
part (`device-facts.ts`: grouping, state, views, the picture's two sides, a change in words) is
tested in `bun test src`.

A device's Settings view adds the administrator-only native persistent profile editor
(`components/network/interfaces/native-profile.tsx`). It reads the supported existing owner, including
standalone veth profiles verified by the backend's private reciprocal-peer fences, and retains
typed IPv4/IPv6 address, DNS/domain and route drafts after refusals, and requires a reviewed generation
rebase when the native baseline changes. VRF routes inherit the observed table; edits to bond
mode/membership and VRF table/membership remain outside this L3 editor. Native writes always use
temporary apply and reconnection confirmation. The global
`components/network/change-confirmation.tsx` keeps incomplete native cleanup visible after the network
decision and offers an explicit owned cleanup retry. It is mounted in the shell for administrators and
reads `/network/changes/current` every 2 s while a change is unfinished, every 15 s otherwise, at once on
the `jd:network-change` event a change's own response raises, and again whenever the tab is shown. With
nothing pending it draws nothing, so a Network page opens on its own title: the switch that turns the
ordinary preference off was the first line of all nineteen Network pages until 0.8.6 and is one row,
*Confirm managed changes after reconnecting*, in the Network overview's last panel, *Managed changes*
(`components/network/reconnect-guard.tsx`, a store the notice and the row share, kept for the tab's life
so a reload arms the guard again; `tests/browser/network-confirmation.spec.ts`). These interactions
are checked by `network-native-profile.spec.ts`; the native manager's separate evidence and limits are
documented in [the backend contract](../backend/network-native-managers.md).

The Routing page (`app/(dashboard)/network/routing/page.tsx`, `components/network/routing/`) opens on
its identity line (`identity.tsx`): the default way out, the counts of tables, routes and rules,
whether the server forwards as its state, *Add route* as the command and *Add rule* and *Refresh*
behind the `⋯`. Under it are four views named in the address (`?tab=history`, `bgp`, `forwarding`;
the first has no parameter). Overview is the decision picture (`decision-map.tsx`, hand-laid: two
columns wired to each other) with its question in its head: the family chips and one target field
(`route-lookup.tsx`). An address or network typed there narrows the routes to those covering it
and the history to the changes touching it; one literal address is also put to the kernel
(`GET /network/routing/lookup`, when the typing pauses and every fifteen seconds after), with the
packet's source and mark behind the field's sliders, and the table the kernel names is lit in the
picture, with the rule where one rule alone leads to that table. The line under the head is the
answer; with nothing asked it is this browser's reply path and how far the kernel vouches for the
highlight, the long form behind an `ⓘ`. Every route is then one line of one framed table
(`route-tables.tsx`), the routing table a column and a chip, the device and its source on one mono
line, Edit and Remove on the dashboard's own rows and why a guarded row stays behind an `ⓘ` a
keyboard reaches; the policy rules are a second framed table with *Add rule* on its title line.
Below 1100px a route is two lines of one height, and a rule below 768px. History
(`route-history.tsx`) is a line a change with the observer's limits behind an `ⓘ`; BGP (`bgp.tsx`)
is FRR's neighbours, OSPF adjacencies and the route browser as framed tables, or a designed empty
state that links to Packages. Forwarding (`forwarding.tsx`) is a setting: a `WireFlow` of each
family, this server and what needs it, then a switch row a family, changed as a draft and written
by the page's save bar (`SaveBarProvider`), which asks before it stops a family; the view stays
mounted so a draft stays on the bar while another view is read. An account that cannot change
anything is shown no command, no row verb and no switch. The page is drawn before its first read
at the size it had last time (`useViewState("network.routing.shape")`). Checked by
`tests/browser/network-routing.spec.ts`, with what the forms send in
`network-routing-firewall.spec.ts` and `network-audit.spec.ts`; the pure readings are
`route-reading.test.js`.

## A deployment's project pages in more detail

The project identity line and Settings → General show read-only production Git monitoring status,
including repository access failures. Branch deployments are automatic after the first deployment;
additional webhooks remain separate integrations. Run labels use a per-project sequence starting at 1,
while permanent run URLs retain their global identifiers. A run that failed or rolled back offers *Ask
why it failed* under its failure (`components/deploy/run-diagnosis.tsx`): the assistant's provider is
asked only on the press, its answer — cause, quoted lines that open the build logs at themselves, steps,
and the exact masked text that was sent — is kept with the run, and an administrator sets the assistant
up from there (`deploy-run-diagnosis.spec.ts`). `/deploy/notifications`
(`components/deploy/notifications-page.tsx`) manages Discord, Slack, Microsoft Teams, Mattermost,
Telegram, ntfy, Gotify, Pushover, e-mail and signed webhook channels with event selection, pause/resume, test delivery and delivery history.
`/deploy/variables` (`components/deploy/shared-variables-page.tsx` over the list in
`shared-variables.tsx`, Deployments → Shared variables) lists the
server's shared variables and Settings → Variables a project's own under *Project variables*, the same
list in both; the server's page opens on a picture of what they reach (each variable, this server, the
projects whose variables read one, a dashed line to a project still on an earlier value;
`shared-variables-reading.ts`) and counts them on its head (*4 variables · 2 not live yet*): each row the name, the `${{server.NAME}}` or `${{project.NAME}}` it is read as, who changed
it when, how many variables read it and, under it, those readers by project and environment (each a link
to that project's Variables page), with *Saved · not live yet* on the row and *(not live yet)* on a reader
whose live release started before the value changed. Copy reference, Reveal value (the audited reveal),
Edit and Remove are its verbs; the editor sheet takes a name (shown as the reference it will be read as),
the Secret/Config type and the value, which an edit may leave empty to keep the sealed one. An
environment variable's editor, with *Reference* on, offers the project's and then the server's shared
variables by name beside the linked databases (`SharedReferencePicker`), and choosing a secret one makes
the variable secret too; a variable reading one draws `server.NAME` or `project.NAME` as a link to where it
is defined. Checked by `deploy-shared-variables.spec.ts`; the Automatic
deployment section on General carries the GitHub commit-status switch. `/deploy/drains`
(`components/deploy/log-drains-page.tsx`, its sentences in `log-drains.ts`, its state word, figure and
line in `log-drain-state.ts`) reads as Notifications does: a picture of the projects, this server and
each drain as the service it sends to (OpenTelemetry, Grafana, the webhook mark for JSON lines; the
formats not added as dashed rings an administrator presses), then one line per drain: the host and
what it carries, a dot and a word (*Shipping*, *Failing 3m*, *Paused*, *Nothing sent*) and one figure
(what waits, what was dropped, else what was shipped), the count on the head (*2 drains · 1 failing*).
The destination's own words and the totals open from the state word. It adds or edits one in a side panel: format, endpoint, sealed
headers, streams — container output, requests and, for an OpenTelemetry drain, traces with the link
its service opens one at — and projects. A request opened in a project's Logs names its trace and
links to it through the first drain that exports the project's traces with a link (`traceLinkFor`). `/deploy/secret-stores` (`components/deploy/secret-stores-page.tsx`, its
sentences and the reference grammar in `secret-stores.ts`) lists the Vault, OpenBao, Infisical, Doppler, AWS
Secrets Manager and Azure Key Vault connections under a picture of the stores, this server and the projects
whose variables read them (`secret-store-state.ts`; the kinds not connected as dashed rings): each store one
line with the product's mark, the name, the endpoint (derived
from the region or the vault name for the last two) with the mount, namespace and sign-in (or the Infisical
environment, the region, the tenant), a dot and a word for the newest test or run's read (*Answering*,
*Refused 2m ago*, *Not tested*) that opens on the store's own words, and *Read by N*, which opens on every
variable that reads it by project, each project a link to its Variables; the head counts them (*3 stores · 1 refused*). The side panel takes the store, the name (shown as the reference it is read as, fixed once
saved), the address, the kind's settings, an optional CA, the sealed credentials and whether previews may read
it; an edit adds Test with an optional path and key, which shows the version found and never the value. An
environment variable's editor, with *Reference* on, offers the connected stores with path, key and an
optional version (`StoreReferencePicker`, `components/deploy/secret-store-picker.tsx`), which makes the
variable secret; a variable reading one draws the store's mark and name, linked here, and where in it the
key is (`StoreReferenceLabel`). Checked by `deploy-secret-stores.spec.ts`. Settings → Runtime exposes memory,
CPU and process limits, the restart policy, a Shutdown section (stop signal, grace and drain
periods — `settings/shutdown.ts`) and the health-check editor; Settings → Storage names each
protected volume's backup standing (`settings/volume-backup.ts`). The Runtime page's service cards carry the
container's own Start, Stop, Restart and Pause/Resume verbs, every published port with its scope
(`runtime-ports.tsx`) and a Details panel read from `GET /docker/containers/{id}` on open
(`runtime-details.tsx`). The Console tab
(`components/deploy/project-console.tsx`) embeds the Docker exec pane on the live release container
for every non-game deployment, using automatic shell detection and the image's user, without
`cmd` or `user` overrides (`console-session.ts`). Its toolbar contains only Terminal actions and
fullscreen; shell, user, container, search, snippet and terminal settings controls are absent.
Runtime's Console action still selects a service through `?service=`. The
`docker.container.exec.open` audit entry records the exec command and user. A console with nothing
to open (no capability, no answer from Docker, no running container; a game server that is not
running, on its Console, Players and Server settings alike) is `ProjectEmpty`
(`components/deploy/project-empty.tsx`): the project's mark, one sentence, the backend's reason
under it and the next page as a button, in the height the pane takes. A game's Players
(`game/players.tsx`) draws each player as `PlayerFace` (`game/player-face.tsx`, the game's
eight-pixel head drawn from the name, since the image policy admits no skin server) and says since
when the page has seen them on (`game/players-model.ts`: the game reports who is online and not
when they joined, so "joined 3m ago" is only for a player the page saw arrive). The shared vocabulary
(`components/deploy/vocabulary.tsx`) is the one place a run state, a project state, a source line or a
release path is put into words, and the one
place a project is resolved to the product it is drawn as (`projectProduct`, `sourceProduct`); a
project's verbs are declared once in `project-verbs.tsx` and a run's in `run-verbs.tsx`, for every
surface that draws them. What a pull request and its preview say about themselves — the tone of the
folded check state, whether the head moved past the built revision, the one word for where the preview
stands, why a project cannot have one at all, what each service of a Compose stack gets in its preview
how a Compose preview's services stand, and how a preview's database branches read, where their data
came from and what a preview of a fork gets — is decided in `lib/pull-requests.ts` so the Git page's
card, the project Overview and the fleet card cannot disagree, and the verbs a deploy project offers a pull request are declared once in
`deploy/pull-request-verbs.tsx`.

## Navigation between features and shared interactions

Cross-feature navigation is intentional: Files and Docker can open Git or a terminal in context; Git can
open GitHub authentication; deployments embed scoped runtime logs, service metrics, and automatic live
website previews while retaining links to owning Docker, proxy, backup, files, and terminal surfaces.
A page that shows a service's log — a database's, a container's or stack's, a unit's, a PM2
application's, a site's, the proxy engine's, certbot's, the Security pages' and Packages' — reads it in
place through `components/logs/service-logs.tsx` and hands nothing off to `/logs`, which gathers the
same readings and views in one place.
Query parameters that trigger a one-time terminal/file context are consumed
so refresh does not repeat an action.

Files supports `entry=` alongside a parent `path=` to open a file's inspector without trying to
list a regular file as a directory. Container Configure is an administrator-only manual editor in
`docker/container-configuration.tsx`; it preserves reviewed credentials and mounts, previews the
specification and confirms replacement. Compose containers open their owning stack's Compose file.
Services links honor `?state=failed`. Failed or stale readings retain the existing operation controls.

Page-owned keyboard commands, history, restoration and polling interactions are specified in
[`workspace-interactions.md`](workspace-interactions.md). Their shared owner is `components/workspace/`;
page-specific navigation, confirmations and API capabilities stay with the feature components above.

Connection-path **Run and save** retains a typed source-sensitive report through the existing saved
run lifecycle. Both quick and saved reports use `network/path-report.tsx`; the exact-scoped comparison
includes source identity/address, protocol/family/port, selected address/mark and measurement choice.

## Requested diagnostics and operation errors

`components/finding-list.tsx` still renders requested TLS/network diagnostics, nginx lint and
certificate issuance reports. The requested operation keeps its measured result, limits and named
action; extended evidence folds away. Actual deployment validation errors retain links to the
matching settings field. These reports use the operation's capabilities and confirmation flow.
The [2026-10-12 actionable-findings audit](../../audits/2026-10-12-actionable-findings/README.md)
records the historical system before its automatic advice features were removed.

## Advisory removal (0.7.5)

The current routes omit automatic Attention/Needs attention/What to change panels, related filters,
issue badges, settings comparisons and navigation marks. The database Advisor route is removed.
Docker, Security, project/fleet, proxy, Backups and Network overview no longer request advisory
endpoints. PM2 details no longer add a heuristic attention panel. Editable settings, updates,
certificate operations and requested diagnostics remain. Network read failures remain visible as
errors; Gateway retains manual admission-rule repair, behind the admission word's Policy evidence and in the
page's menu. See the owning backend guides for retired
routes and retained operation checks; the 2026-10-12 actionable-findings audit is historical.

## Non-network audit repairs

The [2026-10-14 repair matrix](../../audits/2026-10-14-non-network-fixes/README.md) records the 34 findings
from the 94-route audit, their owning features, regression coverage and remaining integration limits.
Route inventory and capability ownership remain unchanged; the fixes correct existing save, handoff,
status, loading, error-recovery and authentication behaviors. `/network` pages are excluded.
