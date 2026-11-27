# Features: Workspace

The route areas of sign-in, the boards, Remote Desktop, the dashboard's settings, the account, the
audit log, the assistant, Files, Local Git, Logs and the terminal: what each page is responsible for,
then, under **Implementation**, the files that own it and the specs that check it.

## `/hub/enter`

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

## `/login`

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
shared logo/state/controls; [shell](../shell.md#signing-in); checked by
`tests/browser/sign-in.spec.ts`

## `/welcome`

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
`server-picture.tsx`, `lib/sidebar-sections.ts`; [shell](../shell.md#the-first-run-question);
checked by `tests/browser/onboarding.spec.ts`

## `/excalidraw`

Routes: `/excalidraw`, `/excalidraw/[id]`

Workspace Excalidraw drawings (`/boards` redirects here). The list opens on Excalidraw's logo and a
line of facts over the drawings as lit cards, each drawn from the server's preview of its scene and
sketched in on arrival, with the server items it links drawn as themselves. The editor is one
workbench surface held to the window, running to the sheet's edges with no frame of its own, under
a head bar: back, Excalidraw's mark, the name edited in place and the save state as a dot
and its words, then Add server item as the one command (Reload in its place while another tab's
change stops saving) and a menu holding Save now, Reload and Delete drawing; a failed save offers
Try again beside its words. The page names Excalidraw's menu trigger and takes the footer role off
its zoom strip (`board-menus.tsx`); the library offers no prop for either. While something is
selected and the drawing can be edited, four Move buttons in Excalidraw's foot carry it a step a press,
for a pointer that cannot drag (`MoveBar`, made of Excalidraw's own arrows so bound text and arrows
follow). A drawing that is not there, or an address that is no drawing's, is an
empty state with Excalidraw's mark and Back to drawings. The editor embeds
Excalidraw and autosaves to server SQLite with a revision guard, or by merging once it has joined
the drawing's live room, where everyone with it open sees the others' changes, pointers and names as
they happen; images are stored apart from the scene; the insert panel adds live cards for the host,
deployment projects and saved databases, each reading its resource now; YouTube, Vimeo and Figma
embed; the shape library is kept on the server and packs from libraries.excalidraw.com are
downloaded by it; Describe a diagram writes Mermaid through the assistant's provider; the menu and
the empty canvas are the dashboard's

**Implementation:** `components/boards/`, `lib/board/boards.ts`, `lib/board/*.ts`,
`lib/drawing-preview.ts`; [drawing contract](../../boards.md)

## `/remote-desktop`

Routes: `/remote-desktop`, `/remote-desktop/new`, `/remote-desktop/[id]`

Workspace remote desktop. The overview opens on the computers' systems as their own marks and a line
of facts (how many, how many ready, open sessions, the tailnet) with the verdict beside the name, Add
a computer as the one command and Check every computer again in the menu after it, drawn while the
page loads;
then the picture of a session's path — this browser, this server running guacd, each computer wired
by whether its port answers and pulsing while a session is open (`picture.tsx`, a `WireFlow` since
0.9.1: the computers are rows on level lines into the server and the browser's line runs level into
it from the other side, each computer its name and one line under its mark, which opens its
screen); the computers as one-line rows (the
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
contract](../../backend/remote-desktop.md)

## `/dashboard`

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

## `/dashboard/configuration`

The panel's own settings. An identity line first, laid out as a project's header: the address it
answers at (scheme, host and port each in their hue) with Running or the restart's phase beside it,
the checkout with Compose's mark, its compose and `.env` files and the last restart as the facts,
and Restart and Rebuild behind its `⋯`, each with the `docker compose` command it runs and what it
costs. Then the stack as the path a request takes — this browser, the tailnet (or loopback, or the
network), Caddy on its port with the certificate's issuer, and Next.js and Go on their loopback
ports with a state dot — wired with pulsing beams that go still and amber during a restart, and the
run record under it with its stages and whole transcript, followed across the restart it describes.
Then the settings as one centred column of sections, the shape a project's settings have
(`components/settings/settings-column.tsx` around `SettingSection`): Certificate (three cards with their
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
change old to new, and sends the same request. Then the [usage count](../../backend/usage-ping.md) as a
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
`components/config/usage-ping.tsx`, `components/settings/settings-column.tsx`,
`components/run-transcript.tsx`, `components/run-phases.tsx`, `hooks/use-self-config.tsx`,
`hooks/use-self-uninstall.tsx`, `lib/uninstall.ts` (`system.admin` only)

## `/dashboard/link`

Settings → Connect: linking this server to a site account. The sidebar names what the page does
rather than the site's domain, under the `Link` glyph (it wore Version's cloud). An identity line
first, laid out as the Hub's — the site's J on a 40px tile, the site as its name and a link to it,
the link's state beside the name (said once, not again among the facts), who the server is linked
to, at which scope and when and by whom (or its own key fingerprint while it is not), and once
linked Sync now and Unlink at its far end. The two Activity sections share their marks
(`account-link/marks.tsx`: `commandMark`, `COMMAND_STATUS`, `ACTIVITY_ROW`); the keys, the policy
and the shared fields are `DetailList`s. Under it the link drawn as what it is (`connect-picture.tsx`, a `WireFlow`, its kinds stacked
under their heading on a phone; the deploy overview's wiring turned to face out): System (the distribution's logo), Readings, Containers
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
The last section is **just-dashboard.com address** (named after the zone `GET
/dashboard/link/domain` reports; [app domains](../../backend/app-domains.md)), one state at a time:
not linked, a sentence on what it is and that the link above comes first; linked but not offered,
the site's reason; site unreachable, a warning with the reason; ready, a **Handle** field whose four
rules (`lib/app-domain.ts`, the protocol's section 3) are `FieldCheck`s lit while typing, a live
preview `app.<handle>.<zone>` with the zone from the answer, one line that claiming publishes this
server's IP address in public DNS, then **Claim**; held, a ruled `DetailList` of the address, the app
names, the address it points at and the wildcard certificate's `Status` with **Issue** or **Renew**
(issuing polls every three seconds until it settles), the *New deployments get this address* switch,
which applies at once, **Used by** as rows leading to each deployment's Domains settings, and
**Release** on the title line, destructive, confirmed, and off while anything uses the address
(the line under the rows says why). Suspended and detached are a notice with the reason, what it
means and the deployments it leaves without a name; detached offers the claim again with the old
handle. Every refusal (protocol section 5, plus `domain_in_use`, `session_required`, `not_linked`)
is a sentence from `refusalWords`, `address_mismatch` naming the address the site saw. Unlink's
confirmation names the app names that stop resolving when the address is in use. A backend without
the route (404) draws no section at all. `JD_APP_DOMAINS=false` is one line naming the switch.
An address the account chose when it approved the link (`offer`, claimed by the server itself;
[app domains](../../backend/app-domains.md)) is one line led by a live dot, *Claiming
acme.just-dashboard.com, the address chosen when this server's link was approved…*, while the page
asks every three seconds until it is held; a failed automatic claim is the backend's sentence in a
notice over the claim form, filled in with the offered handle. While app domains are on, the link
card before linking (choosing and waiting) ends with one line: when you approve the link on the
site, you can give this server an address for its apps there.
Configuration carries one line pointing here

**Implementation:** `app/(dashboard)/dashboard/link/page.tsx`, `components/account-link/`
(`connect-picture.tsx`, whose `LinkPicture` Settings → Hub draws too, `link-panel.tsx`,
`status-panel.tsx`, `scope-cards.tsx`, `shared-fields.tsx`, `marks.tsx`, `policy.tsx`,
`app-domain-section.tsx`), `hooks/use-account-link.ts`, `hooks/use-app-domain.ts`,
`lib/app-domain.ts` (unit-tested), `tests/browser/app-domain-link.spec.ts`, `hooks/use-now.ts`, `lib/account-link.ts` (unit-tested; the scope
wording is the contract's table, the command kinds' words are `commandWords`); checked by
`tests/browser/account-link.spec.ts` (`system.admin` only)

## `/dashboard/hub`

Settings → Hub: this server's link to a Just Hub ([protocol](../../backend/hub-link-protocol.md) §8).
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
**What this server will send** appears once it is, with what only a hub is sent after Connect's own items:
package updates and sign-in counts at every scope, certificate names and fixable image-scan findings from
`operate` up, `sharedItems(scope, "hub")`, [protocol](../../backend/hub-link-protocol.md) §5.4), **Connect** and `sudo ./scripts/manage.sh hub
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

## `/account`

Routes: `/account`, `/account/security`, `/account/sessions`, `/account/keys`, `/account/workspace`,
`/account/users`

The signed-in account as six pages under one strip. Workspace: the first-run answer after the fact —
one line that says where it stands (*15 of 15 shown · 6 purposes*) with *Ask me again* back to
`/welcome` at its end, then one centred column: the question's purposes, Route the network marked
experimental, as a grid of lit cards that throw their sections, over the rail's entries in their
groups as full-width switch rows on hairlines, one draft saved through the floating Save bar. No page opens on tiles: each
figure the old readings rows gave is said by a picture, a chip or a verdict, and every picture
speaks the deployment pages' and Configuration's wiring vocabulary (`components/wire/wire`, `AnimatedBeam`) —
a wire pulses while what it joins is in use, goes amber where something should be looked at, and is
dashed where it has never carried anything. The reads behind every wire, chip and verdict are
`components/account/access.ts` (unit-tested). Profile: the display name in the account's identity
line (`IdentityHeader` with the picture as its mark — the sign-in name and role, member since, last
sign-in, the browser and network this session came in through drawn as themselves, for
`system.admin` the accounts as their faces linking to Users, and the two-factor verdict beside the
name); *Signed in as you* (`access-picture.tsx`): every session on the left as its browser or program with its system
in the corner, what it is doing over its name and its network and address under it, the account in
the middle as its face in the brand's ring with a green or amber shield for what guards it, and the
account's own usable keys on the right as the products that hold them with their role over their
name, drawn through `WireFlow` (marks on a phone, with no line crossing the account's name), each
side's heading counting it and linking to its page; then one
centred column of two sections: the profile (picture cropped and shrunk in the browser before
upload, display name and sign-in name as a draft on the floating Save bar with *Edited* in the
section head, whose line of facts is the rail's own account card drawn from the draft, and what each
field is for behind its ⓘ); and what the role can do as a ladder (`role-ladder.tsx`) — read-only,
limited, administrator, each rung's bar lit in its hue up to the account's role, each listing the
powers it adds as a glyph on the rung's tint, a name and the products it reaches (Docker, systemd
and PM2 for Start and stop), powers past the role dimmed, and the sentence each rung and power had
as its name's tooltip. Security: an identity line as its siblings open on (a shield, the second
factor as its state, then *required on this install* while it is off and required, the passkeys and
the sessions as facts), then what a sign-in passes as
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

**Implementation:** `components/account/` (`access.ts` and its test; `role.tsx`, a role's name, mark
and hue and `RoleTag`, the one way a role is shown — the picker, a key's card, the profile's header
and the ladder all say *Read-only*, *Limited*, *Administrator*, and the key stays in the API and the
address; `marks.tsx`, the session, key and account marks the pictures share; `user-avatar.tsx` is also the rail's picture and holds
`InitialsMark`, the face of anyone drawn from a bare name, and `AvatarRun`; `totp-secret.tsx` is
also the sign-in page's enrolment; `role-choice.tsx`), `components/client-mark.tsx` (a session's
client drawn as itself, shared with a deployment's requests), `lib/clients.ts`, `lib/password.ts`,
account layout/pages, auth hook, confirmation primitives; checked by
`tests/browser/account-ui.spec.ts`, `account-passkeys.spec.ts` (Chrome's virtual authenticator at
localhost) and `api-keys.spec.ts`; key filtering, ordering and replacement roles are unit-tested in
`components/account/key-list.test.js`

## `/audit`

An identity line saying how the last day went: what the server refused as its state ("1 of 10
failed", and "1 sign-in refused" in amber), then this server, the changes in the last day, the
people behind them as faces with the automated changes, and the sign-ins with where the last came
from; `?` and a `⋯` menu at its end holding Export as CSV and Refresh. Under it the day as one
strip, an hour to a stretch in the colour of how its changes went (green, amber where some were
refused, red where all were, the track where nothing changed), with the busiest sections on the
line under it and the pointed hour in their place, said on the 24-hour clock (`minuteAt`) as every
other time is (`components/audit/day-strip.tsx`, the model in the unit-tested `day.ts`). Both are read from their own `since` query, on the hour, so a filter
does not narrow them, and both stand at their final height while the day is read. Then the
filtered, paginated trail as a framed table shelved by day, one line to a row at every width: the
time, the actor as a face (a key in the corner for an API key, with the key named — "· key
ci-deploy", its id in the tooltip — when the row recorded it; "· by the assistant" for the
assistant; "· through Just Hub as <hub user>" for a session a hub sign-in ticket opened, the
session's id in the tooltip, which is the `sessionId` of the `hub.signin` row that opened it; the
webhook's and the dashboard's own marks for machines) with its address as the network it is on, the
action with the mark of the section it touched and its namespace in a lane hue that is never green
(a refused change must not read as one that went through), the target with any detail after it, and the
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

## `/assistant`

A chat about the whole server beside every chat the person has had, grouped by what each is about
(the server, each deployment, each connection), and behind a second tab who the assistant asks, for
every page, as one settings column (`SettingSection`s on hairlines): Model (the provider, the model
and who set it up as a line of facts, the picker at its end), Providers (a `ChoiceGrid` of cards
with their real logos, the one in use lit), Default mode (three cards; written the moment one is
pressed, one write at a time) and Where it works, then Ask about a deployment ([the deployment
assistant](../../deployments/operator.md#providers)). `?about=`
and `?title=`, from **Ask** beside Search on any page (`assistant/ask-about.tsx`, `agent/about.ts`,
unit-tested), begin a new chat told which page it came from, with a chip back to it;
`tests/browser/assistant.spec.ts`, `tests/browser/assistant-settings.spec.ts` and
`tests/browser/database-assistant.spec.ts`.

## `/files`

The host's files as a workbench: a sidebar of places and starred folders (`files-sidebar.tsx`,
`places-menu.tsx`), the path bar, list and grid views with selection, drag and drop and uploads,
name and content search (`quick-open.tsx`, `search.ts`) and a preview and details panel
(`preview-panel.tsx`). Below `lg` it is one pane at a time (Places, Folder, Details) with a bar at the
foot (`phone-panes.tsx`) under a one-row strip: back, the path, find, New, and the rest as words in
`more-menu.tsx`. The folder follows the disk over a socket (`live-folder.ts`, `live-patches.ts`), with
the rows that changed laid over the listing so selection, scroll and dialogs stay put. The uploads panel
(`uploads.tsx`) holds uploads (in pieces over 32 MiB, with pause, resume and cancel, carrying on after a
dropped connection) and links being fetched by the server (New → *From a link*), each one line with
how far it is in words (`transfer-words.ts`). A worker stops for good once its upload is paused, and
Resume pressed while that worker still waits on a request leaves the upload queued until it has
exited, so one upload never has two workers sending its pieces

Each row says the most telling thing the server knows about the entry after its name, with its logo
("Postgres volume of shop-db", "Served at example.com by nginx"); a tile carries the logo in its
corner; the details pane's *On this server* says everything known as linked sentences, including
what the entry is inside and "Not backed up" for a folder that matters (`server-context.tsx`,
`server-context-words.ts`, `GET /files/context`, checked by `tests/browser/files-context.spec.ts`).

A text file reads as what it is in the details pane and the viewer: Markdown rendered with a press
back to the source, a CSV or TSV as a table, one-line JSON indented (`rendered-text.tsx`). Pictures
list as the server's small copies (`/files/thumbnail`); the viewer walks a folder as a gallery with
a strip, swipe and a slideshow; audio plays as a queue whose player is one line in the listing's
foot and keeps playing across folders (`audio-queue.tsx`); a video the browser cannot play says so
and offers Download (`playable-video.tsx`). Checked by `tests/browser/files-viewing.spec.ts`

**Implementation:** files page and `components/files/`, the page's keys decided in the unit-tested
`keymap.ts`; checked by `tests/browser/files-ui.spec.ts` and `tests/browser/files-transfer.spec.ts`;
backend contract in [`../backend/docker-files-logs.md`](../../backend/docker-files-logs.md#files)

A delete moves to the Trash (`delete-flow.tsx`): before the confirmation opens it asks
`GET /files/trash/check` for each entry, so the sentence says where things go ("It goes to the Trash,
kept 30 days") or, for what the Trash cannot take, why it is deleted permanently instead. Shift+Delete
and "Delete permanently" in the menu skip the Trash. Every delete, move, rename, copy and duplicate is
recorded as data (`undo.ts`: the steps that undo it, in order) on this visit's stack
(`use-file-undo.ts`); its notice carries Undo, and Ctrl/Cmd+Z undoes the newest, then the one before,
while text fields, dialogs and menus keep their own. Undos run one after another in the order asked
for: a press while the last one is still with the server waits its turn rather than being dropped,
and an operation asked for twice runs once. A move or copy answers the identity of what it left
(`identity`), and its undo sends it back as `expect`: an entry that took the name while the undo waited
is refused (`409 entry_changed`, "Something else has taken its name since, and was left alone") rather
than moved or put in the Trash. A save in the editor sheet writes a new file over the name, so the
write answers the identity it replaced and the one it left, and an undo still to run that names the
saved file follows it (`carryIdentity`); a rename, then a save, then Ctrl+Z still undoes the rename,
while a step expecting something else at that name is left as it was. An operation that cannot be
undone (a permanent
delete, a copy by an account that may not delete) says so on its notice instead. Name search passes
the folder on screen as `here` and says the index's age beside the count when the index answered.

## `/files/trash`

The Trash (`trash-view.tsx`) in the Files workbench's frame: the places sidebar with Trash as its last
row (`trash-place.tsx`, also the places menu's last item), a table of what was deleted (name, the
folder it came from, how long ago and by whom, how long it is still kept, its size; one 44px line
each) and the chosen item's details (`?item=` in the address). Restore asks through the conflict
dialog when the place is taken; Delete permanently and Empty Trash confirm and need the destructive
capability. Below `lg` it is one pane at a time (Places, Trash, Details) with the bar at the foot

**Implementation:** `app/(dashboard)/files/trash/page.tsx` and `components/files/trash*.ts*`; checked
by `tests/browser/files-trash.spec.ts` and `trash.test.js`

## `/files/editor`

A file opened as a page: a folder tree (`file-tree.tsx`) beside the text editor (`file-editor.tsx`)
or the image editor (`image-editor.tsx`), with name and content search. The editor's commands are
grouped (`editor-toolbar.tsx`), with text size, indentation and the command palette in a menu. Below
`lg` the tree and the editor are one pane at a time with a bar at the foot, both kept mounted so an
unsaved draft outlives a look at the tree

**History** in the strip opens the file's versions as a view of the same page
(`?view=history&version=e12&against=previous`, `version-history.tsx`): the earlier contents as 48px
rail rows (`RailRow`) beside Monaco's diff editor (`version-diff.tsx`), against the file as it is now
or the version before it, with Restore and download on the version. A picture's versions are the two
pictures side by side; any other binary is download and restore only. A backup's copy on another
machine is read only after a press that says it is fetching. The editor stays mounted, hidden, and is
keyed without the history's keys (`editorKey` in `versions.ts`), so a draft outlives the history; the
page's leave guards let the same file's history addresses by (`sameEditorFile`). Below `lg` the bar's
stops become Files, Versions and Compare. The details pane's **Versions** line (`versions-detail.tsx`)
says how many there are and when the file last changed, or why none are kept, and links here

**Implementation:** files/editor page and shared `components/files/` editors; checked by
`tests/browser/files-ui.spec.ts` and `tests/browser/files-versions.spec.ts`; the words and addresses by
`components/files/versions.test.js`

## `/git`

**Local Git** in the rail and the tab (it was "Git" until 0.9.1, when GitHub became a page of its
own: [`github.md`](github.md)). The checkouts under the git roots (`JD_GIT_ROOTS`) shelved by the account their remote belongs to
(`lib/git-repos.ts`), as repository cards under the chips All, Uncommitted, Behind, Unpushed,
Detached, Pull requests and Worktrees, with worktrees in a section of their own; Clone for
`service.control` and the GitHub account beside them. A selected checkout takes the whole page as
its workspace (`repo-workspace.tsx`). Its middle column opens 424px wide, the width its four views
(Changes, History, Branches, GitHub, with their counts) need whole on the shared strip; the strip
holds only the views, the branch graph's toggle sits in the head beside the branch, and Alt+↑ and
Alt+↓ walk the changed files

**Implementation:** `app/(dashboard)/git/page.tsx`, `components/git/`, `lib/git-repos.ts`; checked
by `tests/browser/git-ui.spec.ts`, `git-panel.spec.ts` and `git-features.spec.ts`; backend contract
in [`../backend/git-backups-users.md`](../../backend/git-backups-users.md#git-working-copies)

## `/logs`

Every log source in one place: a source rail (`source-rail.tsx`) beside one pane with Live, History
and Insights (`log-workspace.tsx`), the same readings and views a service's own page reads through
`service-logs.tsx`. Over a container's or a stack's logs a lens's readings are reading lines
(`readings="lines"`, `logs/lens-reading-line.tsx`, the lines Security draws the same readings as),
not a row of tiles; a database's and a scheduled job's put them on the lens row's chips. In
Insights a breakdown of one or two values is a ranked line each, not a framed table. The whole journal's identity line carries the systemd logo, how many units
write to it, its size on disk and its oldest line (`GET /logs/source?source=journal:`, asked once
the journal is the source on screen; the two facts keep their room while they are asked). A kind
of source that is absent is one line in the rail with the thing's mark and the page that says
more (`MissingLine`, worded by `logKindMissing`): "Docker is not running" to `/docker`, "PM2 is
not installed" to `/processes/pm2`, "No systemd on this server" to `/processes/services`

**Implementation:** logs page, `components/logs/`, `lib/log/filter.ts`, `lib/log/lenses.ts`,
`lib/log/sources.ts`; checked by `tests/browser/logs-ui.spec.ts`, `logs-lens.spec.ts`,
`logs-views.spec.ts`, `logs-handoff.spec.ts`, `logs-buffer.spec.ts` and `missing-states.spec.ts`;
backend contract in
[`../backend/docker-files-logs.md`](../../backend/docker-files-logs.md#logs)

## `/terminal`

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
[`workspace.md`](workspace.md#the-terminal-panel)

## The terminal panel

`components/terminal/` is the session rail and window strip; `components/xterm-pane.tsx` is the emulator. The
split matters — the pane is reused by the compose runner and knows nothing about sessions.

- **It can be read.** Screen reader mode (xterm's `screenReaderMode`) is a switch in the settings
  and a button that is the stop before each terminal, drawn only under the keyboard. It is off by
  default because the mirror costs a busy terminal time, and a browser cannot tell that a screen
  reader is running. Bright black is `--faint` and `minimumContrastRatio` is 4.5.
- The page is **one workbench surface**, run to the sheet's edges with no border of its own (the
  sheet is its frame; 12px inside it, the `rounded-xl border` it had until 0.9.1 was a frame in a
  frame). The rail, the emulator and the Files/Git column are `Pane flush`
  inside it, separated by a hairline each (drawn on the rail's right
  edge and the tools column's left edge). Three framed panes with a gutter between them read as three
  boxes floating on the page; the screen is one working surface. The three columns' top strips are all
  40px (the rail's and the tools column's `PaneHeader` are pinned to `h-10`, the emulator's strip is
  `min-h-10`), so their hairlines meet as one rule across the frame. Immersive mode drops the wrapper's
  frame along with the page. Below `lg` the frame is the screen, edge to edge as the Git workspace's
  is, and shows **one pane at a time** over the shared bar at its foot (`phone-panes.tsx`): Sessions,
  Terminal, Files and Git, the last two being the tools column's halves, with the count of changed
  files on Git. The page opens on the emulator. The rail and the column used to cover the emulator
  there, each with a hide button of its own; covered, the emulator was still laid out and followed
  the window, so where a phone's keyboard shrinks the window, typing in the session filter fitted the
  shell to a few rows and told it so. Now a pane
  that is not shown is `display: none` and the emulators are handed `visible={false}`
  (`XtermPane`'s own state for a window that is not on screen): they keep their sockets and their
  grids, the page's measure of the pane grid ignores a size of nothing, and showing the pane again
  fits each emulator and says its size once. Picking a session, or opening one, shows the emulator
  and gives it the keyboard. The strip's two toggles are not drawn on a phone and their chords move
  between the panes instead; beside the emulator they remain the one way to hide either panel.
  Which half of the column is open is held by the page (`terminal.tools.tab`), since the bar picks it
  as well as the column's own strip, which a phone does not draw.
- `session-rail.tsx` is a plain column: a "Sessions" strip with the new-session button, then the
  list, newest first. A row is one line — the program mark, the session's label and, at the end, the
  activity mark (below), with the close button after it under the pointer (`rowReveal`). That is
  everything a row offers: the operator asked for folders, renaming, pinning and the row's `⋯` menu to
  go, because sessions that name themselves after what they run need no filing system — a session is
  opened and closed. Rows carry no directory line under the title (the label carries the directory at
  a prompt). Rows are plain rounded rows with no fence or divider between them; the active one is
  `bg-accent` and the others take the row hover, and that is the whole difference — no brand bar or
  other colour on the active row. The filter box appears only once there are more than five
  sessions — a filter over one session is a box with nothing to do. The backend's folder, pin and
  rename routes remain; the page no longer calls them.
- **Names follow the shell.** An unnamed session or window is "Terminal" (numbered from 2 when that is
  taken), and that default is only a fallback. A tab is labelled the way a desktop terminal's title
  bar is: the title the foreground program set through OSC 0/2, the program's name when it set none,
  and the directory at a prompt (the bundled prompts set that title). The glyph an agent animates in
  front of its title — Claude Code's asterisk, Codex's half-moon — is dropped from the label
  (`plainTitle`): saying "working" is the activity mark's job, and a label that carried both said it
  twice. A session is labelled after its
  *current* window — the one last on screen in any browser (the pane sends a `focus` frame), or
  failing that the newest. The page offers no renaming; a name given through the API (`named`) is
  still shown as given. `lib/terminal/activity.ts` holds the rule
  once for the strip and the rail. The state arrives two ways: polled with the window list and the
  session listing (every five seconds), and pushed over each visited window's attach socket as a
  `state` frame the moment it changes, which is what makes a tab's mark and title move with the
  shell rather than with the poll. The server side is in
  [`processes-terminal-github.md`](../../backend/processes-terminal-github.md#the-terminal).
- **The activity mark** (`activity-mark.tsx`) is one still `StatusDot` whose colour is the window's
  state, in this order: **red** when this browser's socket to the window has dropped, **green** while
  it is *working*, and **orange** when it is idle. Nothing on it moves and there is no "finished"
  state: a breathing dot while working and a green finish held for a while after made an agent at its
  prompt, whose odd redraw the server used to read as work, cycle orange, green and back all day. It
  is drawn at the end of every window tab and, for the session, at the end of its row in the rail —
  the same place in both, beside the close; a row is red when any window of it that this browser
  attached has dropped. The mark's box is one fixed size, so a tab does not change width as its state changes.
  Working is the server's word for "output has been arriving for a second and still is, or the job is
  burning CPU": an agent streaming an answer, a build, a test run. A program that is merely open — an
  editor, Claude Code or Codex waiting for the next message — holds the terminal and is idle, because
  nothing is happening; that is exactly when an agent's own title glyph goes still, and the tab
  follows the same signal. Nothing is announced for a command over in a blink (`ls`), so the label
  never flickers, and one redraw is not a run of work (see
  [`processes-terminal-github.md`](../../backend/processes-terminal-github.md#the-terminal)).
  Disconnected is known only in the browser — to the
  server the PTY is alive — so `page.tsx` sets it when a window's socket closes and clears it on the
  first `state` frame a reattached socket receives, which the server sends on every attach. Idle is
  the resting state and is hidden from screen readers; the other two are named (`role="img"`), and
  the mark carries its state as `data-activity`.
- **The page remembers where you were.** Which session was open, and within each session which
  window, live in `view-state` (`terminal.session`, `terminal.windows`) rather than in component
  state, so leaving for another page and coming back lands on the same window rather than on the
  first session's first one. It is the one selection that store keeps: a terminal's selection is the
  tab you had open, which is furniture. Entries for sessions that have ended are dropped as the
  listing arrives.
- `window-strip.tsx` places compact, horizontally scrolling direct-PTY tabs between exactly two workspace
  toggles: sessions on the left and Files/Git on the right. Each draws the side its panel is on and
  the way pressing it moves the panel (`SidebarLeftOpen`/`Close`, `SidebarRightOpen`/`Close`). The strip
  lives in the shared terminal title bar above the pane canvas; there is no separate workspace bar or
  working-directory/shell title.
  A tab reads as a rail row does: the program mark, the label, then the activity mark beside the close.
  Close appears under the pointer (`rowReveal`), the way a browser's does; the active tab keeps it
  visible. There is no rename (double-click only selects) or colour action. Each standalone window or
  split group has one tab, owned by the group's original window; split children do not add tabs.
  The active tab remains selected whichever pane has focus. Selecting another group restores it;
  selecting the current tab retains its focused pane. Window shortcuts traverse these same tabs,
  while pane shortcuts traverse the visible leaves.
  Closing the last window closes its session through the session endpoint.
  **Overflow:** tabs give up width first, the way a browser's do, but only to 128px (`min-w-32`), so
  a label still reads a word rather than "cl…". Past that the tabs scroll inside their own scroller,
  which draws **no scrollbar** — a ten-pixel bar across the labels in a 40px title bar was the
  loudest thing on the page — and fades whichever edge has tabs past it (a `mask-image` driven by the
  scroll position, `data-overflow-start`/`-end`). While anything is out of view, a chevron sits at
  each end of the strip (disabled at the end already reached) and scrolls it by most of its width —
  the fade alone read as a gap rather than as more tabs. A vertical mouse wheel scrolls the strip sideways,
  the active tab scrolls itself into view, and **New window** sits outside the scroller, right after
  the last visible tab, so it never scrolls away with the tabs it adds to.
- **Directional splits** use `lib/terminal/layout.ts`'s binary layout trees, grouped per session in
  `terminal.layouts`. Each new split adds a direct-PTY window above/below/left/right of the focused
  leaf; a new window or agent launch creates an independent tab. Closed leaves collapse their parent,
  and a listing from another browser reconciles missing/new windows. Each group's optional `tab`
  stores its owner; older saved trees recover their oldest live window as the owner. Removing the
  owner promotes a surviving pane. Each split pane has close and **Open as separate window** controls;
  detaching removes that leaf, collapses its divider and selects its new standalone tab without a
  PTY mutation. Dragging a window tab over a visible terminal previews the half nearest the pointer's
  normalized edge (left/right/above/below). A drop moves the existing window or entire source group
  beside that target leaf. The target group retains its tab and the moved window receives focus.
  `split-drop-overlay.tsx` renders the preview; insufficient room shows a blocked preview. Drops on
  the same group are ignored; leaving the pane, cancelling or ending the drag clears the overlay.
  Only the internal window drag type is intercepted, preserving terminal image drops. The split menu's
  **Move window into split** submenus provide a non-drag alternative with the same size checks.
  Pane rectangles and dividers are
  computed separately from the stable flat keyed collection of emulators: changing the layout never
  reparents xterm or reconnects its socket. Every visible pane fits and sends changed rows/columns.
  Controlled pane dimensions fit in a layout effect before composition, rather than clearing WebGL
  in an application animation frame after xterm has painted. The observer still covers font and
  internal-header changes.
  Only the focused pane auto-focuses and sends the `focus` frame. Pointer and keyboard focus capture
  selects a pane before xterm can stop propagation. An active inset rule and pane title identify where
  typing goes; Files/Git follows that pane. Hidden groups continue parsing at their last visible size.
  `split-divider.tsx` supports pointer capture, arrows (Shift for larger steps), Home and double-click
  balance. Pointer moves stay in component state; the final ratio is remembered. Nested minimum sizes
  bound each divider. On narrower viewports existing groups shrink proportionally within the canvas;
  splits that cannot give both new panes usable space are disabled. The split canvas clips output and
  all hosts remain absolutely inset, so an emulator cannot expand its parent.
  A successful create inserts the returned window immediately; a failed follow-up listing cannot
  discard it. Each session's listing revision prevents a pre-create/pre-close poll from replacing
  newer windows. Creation also merges into the latest in-progress divider layout, so releasing a
  pointer after an asynchronous split cannot remove the new pane. The split menu returns keyboard
  focus to the terminal instead of its trigger; the new pane takes focus when its socket attaches.
- The control-key row under the emulator is a run of monospace words on the footer strip, not framed
  keycaps.
- `workspace-tools.tsx` is the Files/Git companion. Its header is two section tabs (`tabClasses`, the
  brand underline, no glyphs) that **split the strip between them** — each `flex-1`, label centred —
  with the changed-file count beside "Git". Two small words at the left edge of a 336px column read as
  a label rather than as the panel's switch. Under that, the Files half is a
  strip with the root path (middle-truncated), **new file** and **refresh** inline, and hidden files /
  new folder / open in Files behind one menu — `file-tree.tsx` draws that strip, and each entry in the
  same folders and pages the Files page draws (`components/file-icon.tsx`), in the colours folders were
  labelled with there. (The Files page's own sidebar is no longer this tree but a fixed list of
  places, `files-sidebar.tsx`.) A file opens over the panel body (`InlineFile`) with no Save in its
  header: a Save on every file opened to be read was a command with nothing to do. The first edit
  brings up a floating bar at the foot of the text — *Unsaved changes*, **Discard**, **Save**, on the
  popover surface and shadow the settings save bar uses (`design-system.md` §2) — and it leaves once
  the file on disk matches again. Ctrl/⌘+S in the editor is the same Save.
  The second tab is **Git** (`git-tools.tsx`): the repository the shell is in. A strip names the
  branch (with the git mark, and ahead/behind) as a button that opens the branch list, and links
  **Open in Git** to `/git?repo=<path>`; under it three chips choose the view (`terminal.tools.git`):
  - **Changes** (`diff-tools.tsx`) is the diff, and only that — *what have I changed?* A strip says
    how many files and how many lines added and removed, with fold-all and unfold-all; then every
    changed file under its name — the status letter in its colour (M amber, A and untracked green, D
    red, R cyan), the directory muted before the file name, the file's own +/− — with its diff below
    it, staged and unstaged halves both shown and labelled when a file has both. Long diffs start
    folded. The list is the status the panel already polls; the diffs are read again when that list
    changes, as the whole unstaged and the whole staged diff plus one request per untracked file (the
    first twenty), split per file by `lib/diff-files.ts`.
  - **Branches** lists local branches (current one ticked, a branch another worktree holds says
    where and offers nothing) with **Switch**, and remote branches with no local counterpart with
    **Check out**, which makes the tracking branch (`POST /git/checkout` with `local`). A filter box
    appears past six branches.
  - **Pull requests** lists the open pull requests through gh (`/git/github/pulls`), each with its
    number, head branch, author, age, checks dot and draft tag, and **Check out**
    (`/git/github/pulls/{n}/checkout`, i.e. `gh pr checkout`) to try it in this checkout; the one whose
    branch is checked out says so instead. gh is asked only while the Git tab is on screen, which is
    also what gives the chip its count. Without gh, or signed out, the view says so and links to the
    Git page, where signing in lives.
  Switching and checking out need `service.control`; a refusal is shown in git's own words. Staging,
  committing, pushing and the rest stay on the Git page: these are the two moves made *from* a
  shell's directory, and the shell's prompt shows the result. A browser that had the earlier Diff tab
  open (`terminal.tools.tab` stored as `diff`) comes back to Git.
  Each rail row and each window tab also carries **the program it is running** as that program's own
  mark (`ProgramMark` in `activity-mark.tsx`, `programProduct` in `product-logo.tsx`: Claude, Neovim,
  Vim, Node, Bun, Python, Go, git, Docker, psql, redis-cli, kubectl and the rest), read off the
  foreground process the backend reports while a window is busy — Codex as OpenAI's mark, the same one
  its toolbar button carries. A shell at its prompt, or a program
  with no mark of its own (`htop`, OpenCode), is drawn as a terminal
  (`public/logos/terminal.svg`, drawn for this product): left empty, an agent with no logo read as a
  window with nothing in it.
  The shared toolbar offers **Codex** and **Claude** (each drawn as its maker's mark — OpenAI's and
  Claude's — with the name as its accessible label and the exact command in its tooltip), **Split
  terminal** (two panes in a window, `terminal/split-glyph.tsx`, with a tooltip: the bar's `+`
  stays New window and New session), Terminal actions and fullscreen.
  Search, snippets and terminal behaviour buttons are absent from the terminal page; search remains
  available through its shortcut. Terminal actions apply to the focused pane (copy, export, working
  folder, shortcuts and clear). Docker consoles keep their own emulator controls; deployment console
  toolbars show only Terminal actions and fullscreen, with search available through its shortcut.
  Codex/Claude **run in the focused terminal**: the exact `codex --yolo` or
  `claude --dangerously-skip-permissions` line is typed into that pane's shell with Enter
  (`XtermActions.run`), as if you had typed it, so it starts in whatever directory the shell is in
  and opens no tab. The one exception is a focused terminal already holding a program (`busy` — an
  editor, another agent, a TUI): typed there, the command would be that program's input, so the page
  falls back to the window API and opens a sibling window with the agent, after the native Bash/Zsh
  configuration loads. The backend reads the focused `sourceWindowId`'s directory at creation time,
  including a recent `cd`; the existing pane's running program is unaffected. Missing tools print the
  shell error and leave the shell usable. Unsupported shells report the launch limitation rather than
  silently opening an ordinary terminal.
  Input stays in the shell: there is no separate composer or Workspace/Focus mode. Bundled Bash and
  Zsh startup files install a compact directory/chevron prompt — which also sets the window title to
  the directory (`\W`, `%1~`), the title the tab shows at a prompt — and native Tab completion in new
  windows.
  The Zsh startup also loads the host's `zsh-syntax-highlighting` and `zsh-autosuggestions` packages
  when present (Debian's `/usr/share/<plugin>` or Arch's `/usr/share/zsh/plugins/<plugin>`), guarded so
  an account rc that already loaded one is not wrapped twice, and sets a history file, `HISTSIZE` and
  `SAVEHIST` only when the account left them unset — a bash-by-default account has no `.zshrc`, and
  suggestions drawn from history need one. `install.sh` installs those packages and writes
  `JD_TERMINAL_SHELL` so a fresh install has the ghost text without touching the account's login shell.
  Account profiles and interactive configuration still load; account dotfiles are never edited. The
  login half runs the account's own shell from `/etc/passwd` when it is a POSIX one (bash, sh, dash,
  zsh, ksh, mksh), as ssh does, and then execs the window's shell: a bash account's `~/.profile` —
  where Debian and Ubuntu put `~/.local/bin` — used to go unread by the zsh `JD_TERMINAL_SHELL`
  selects, so a command ssh ran was "not found" in the dashboard. The bundled `.zshenv` also adds
  `~/bin` and `~/.local/bin` when they exist and are missing, as Debian's profile does, for root (whose
  profile does not) and for a PATH line an installer wrote to `~/.bashrc`, which zsh never reads.
  `term.SetupShell` atomically installs readable scripts in the process-owned shared terminal root's
  `.shell` directory, rejecting symlink or foreign-owned directories. A constant login bootstrap passes
  shell and startup paths as positional arguments; unsupported shells retain their ordinary startup.
  Existing running shells are not modified.
  The terminal host is absolutely inset into its output region so its own rows cannot grow its parent.
- `ResizeHandle` is an invisible eight-pixel hit target over each panel's own border. The border is the
  visual affordance, so the layout draws no extra divider. Arrow keys and double-click reset remain the
  non-drag alternatives.
- `lib/terminal/settings.ts` keeps scrollback and behaviour in localStorage — on the screen, not the
  account, because it belongs to the machine you are sitting at. Font metrics are deliberately fixed: user-selectable line height,
  spacing and fonts made the emulator grid cease to be a stable terminal grid.
- `lib/terminal/keymap.ts` is every shortcut, all rebindable. A chord must get past the browser, the page
  and the shell, and no default suits everybody. Ctrl+Alt is the default family (neither browser nor shell
  wants it); Ctrl+Shift is the emulator's own. Matching is on `event.code`, the **physical** key, so a binding recorded on QWERTY
  survives a Romanian layout. Actions carry a **scope** — `navigation` is the page's (it alone knows the
  sessions), `terminal` is the pane's (the compose runner needs copy/paste/search with no session at all)
  — and that split is what stops one keydown being handled twice. `shortcuts-dialog.tsx` is both cheatsheet
  and editor, because a read-only list is opened once and a hidden settings page never. Split creation
  defaults to Ctrl+Alt+Shift plus a direction arrow; pane focus defaults to Ctrl+Alt+H/L/I/K
  (left/right/up/down), and Ctrl+Alt+P cycles visible panes. These appear in that same editable dialog.
  Ctrl/Cmd+K remains the shell's global command search: the window capture listener opens it before
  xterm receives the keystroke, so opening search does not also alter the shell's current line.

In `xterm-pane.tsx` and the page, load-bearing and easy to undo:

- **New session always opens a direct PTY, held on the host.** New window creates a sibling direct PTY
  and the browser connects each visited window's emulator to that window's opaque id. Each PTY must
  be owned by a host holder rather than by the dashboard
  ([`processes-terminal-github.md`](../../backend/processes-terminal-github.md#sessions-outlive-the-dashboard)),
  so a session keeps running — an agent included — with every browser closed and across dashboard
  restarts, rebuilds and upgrades, until it is closed or its shell exits. There is no idle timeout.
  The listing's `persistent` says whether new holders can be started; `persistenceError` supplies the
  reason when they cannot, shown in a danger notice while existing windows remain usable. The empty
  state disables Open session until protection is available, and the API refuses all new terminals
  and windows with HTTP 503 rather than falling back to a terminal that would end on restart. A host
  reboot ends running terminals. Splits are a browser layout of independent window PTYs; there is
  no multiplexer.
- **A dropped socket reconnects by itself.** A terminal-page pane whose socket closes (the dashboard
  restarting, a laptop waking, the network) retries on its own, backing off from one second to ten,
  and the banner says the session is still running and that it is reconnecting; Reconnect only skips
  the wait. A window that has really ended leaves the next listing, which unmounts the pane and ends the
  retrying. The compose runner, which shares the pane, never retries: re-issuing its GET runs the
  command again. A failing listing poll no longer replaces the workbench with an error once it has
  loaded, so a restart does not take the panes down with it.
- **Visited windows keep their emulator and socket while hidden.** A TUI updates the screen incrementally;
  remounting xterm on each window/session switch loses its parser, alternate buffer and unchanged cells.
  The server's last 128 KiB of raw output is not a screen snapshot and may start inside an escape sequence.
  The page retains visited windows until their window/session is removed from the live lists or the page
  is left. Window lists are cached per session so switching sessions never borrows the previous session's
  windows while its request is pending. Hidden panes keep parsing bytes and answering live terminal
  queries, but remain inert and retain their last visible grid size. Selection fits and refreshes every
  visible pane and focuses only the selected one; browser visibility/focus changes refresh it even if the dimensions have not changed.
  Showing a pane, returning to the browser tab and focusing the browser window also *resend* its size even
  when unchanged: `sent` is what this tab last said, not the PTY's size, and another browser attached to the
  same window resizes the PTY too. The server ignores a size the PTY already has.
- **`clipboardKey`**: Ctrl+C copies **only when something is selected** and clears the selection as it
  goes, so the interrupt is never more than one keypress away. Ctrl+V returns false *without*
  `preventDefault`, so xterm leaves the key alone instead of sending ^V and the browser's own paste runs —
  arriving through `onData`, where the multi-line confirmation still sees it. Reading the clipboard there
  instead needs a permission Firefox does not grant at all.
- **Clipboard images never enter the PTY.** A capture-phase paste listener on xterm's actual host leaves
  text-only events completely alone, but sends PNG/JPEG/WebP files to
  `POST /terminal/{id}/clipboard`. The handler binds the upload to the authenticated dashboard owner of
  the live session, verifies the declared MIME against the bytes, and chooses the destination under
  `/tmp/just-dashboard/<session-id>` itself. Only the returned absolute path goes through the existing
  terminal socket, with no Enter. The backend container bind-mounts that temporary root at the same path
  on the host; session directories are removed when their PTY ends and old files expire after seven days.
- **Multi-line paste is confirmed, and the guard lives in `onData`.** A pasted block runs every line but
  the last immediately, and Ctrl+V, the context menu and the X11 middle click all arrive as one `onData`
  call — guarding only the Ctrl+Shift+V handler guarded the one route nobody uses. That handler must call
  `preventDefault`: returning false from `attachCustomKeyEventHandler` stops xterm, not the browser, so
  without it the confirmation opened *and* the native paste went through.
- **Direct PTY reconnect uses best-effort shell-history replay.** A direct PTY has no independent screen
  model, so the handler subscribes before resizing and then sends its bounded output suffix — after a
  dashboard restart, the suffix the holder kept, including what was printed while nobody was attached.
  The replay protocol below prevents terminal capability replies from being typed into the current
  prompt.
- **Replies are suppressed while direct-PTY scrollback is replayed.** `CSI c` and friends are the shell asking the
  terminal a question, and xterm answers down the channel a keystroke uses — so replaying a buffer
  containing one typed `1;2c0;276` at whatever prompt exists now and left a column of "command not found".
  The server announces the replay with a `scrollback` frame before the binary snapshot (from the browser's
  side the bytes are identical either way) and the client drops its own output until xterm's write callback
  says the replay is parsed.
- `allowProposedApi` is on because the search addon's match count and highlight-all use xterm's decoration
  API, which is not frozen; without it `findNext` throws and the counter reads "none" over a scrollback
  full of matches.
- **The xterm-6-matched WebGL renderer is the default.** xterm 6's DOM renderer explicitly omits custom
  glyph support, so enabling `customGlyphs` there still leaves box-drawing and block-element characters
  to browser font fallback; that produced disconnected Codex borders and gaps in Claude's block artwork.
  `@xterm/addon-webgl` 0.19 matches xterm 6.0 and paints those structural glyphs to the full cell. Every
  alternate-buffer change schedules a complete refresh to prevent the stale/blank rows seen in the old
  WebGL integration. Context loss disposes WebGL and **fits** the DOM fallback: WebGL floors the cell width
  to whole device pixels and DOM keeps the fraction, so the WebGL-fitted grid ran off the pane under DOM,
  and the box had not changed for any observer to notice. A terminal whose context was lost stays on DOM
  until it remounts. Contexts are rationed by `WEBGL_BUDGET` (8): Chrome keeps sixteen per page and
  destroys the oldest — possibly the pane on screen — and every visited window stays mounted. Each fit of
  a visible pane claims WebGL and moves it to the recent end; past the budget, the least recently fitted
  *hidden* pane drops to DOM and claims WebGL again, before fitting, when next shown. Setting
  `jd.terminal.renderer=dom` in local storage is the diagnostic A/B override. The Canvas addon remains
  absent because its stable release targets xterm 5 internals. Font metrics remain fixed at unit line
  height and zero letter spacing. DOM columns are fitted from the measured font width and the
  actual scrollbar reservation, because xterm's rounded screen width makes the addon's cell width
  depend on the previous column count; returning from a split must restore the same grid at the
  same pane width. WebGL uses the fit addon's renderer metrics. `@xterm/addon-unicode11` keeps
  cursor arithmetic aligned with the Unicode-width rules used by modern TUIs.
- **PTY output and input are binary WebSocket frames.** JSON text frames are controls only. Raw PTY chunks
  go straight to `terminal.write(Uint8Array)` (whose streaming decoder preserves a UTF-8 character split
  across chunks); keyboard and paste strings are encoded once with `TextEncoder`. The backend neither
  decodes nor rewrites terminal bytes.
- **The navigation listener runs in the capture phase and must not skip the terminal.** Bubbling lands after
  xterm has forwarded the keystroke, so Ctrl+Alt+→ would switch the window *and* type an escape sequence.
  The usual "ignore keys while a text field has focus" guard needs an exception for `.xterm`, since xterm
  receives keystrokes through a hidden `.xterm-helper-textarea` — the plain form disables every shortcut
  exactly when the terminal has focus.
- **Shortcuts fire only where the shell has the keyboard.** These chords move sessions and close windows;
  anywhere-in-the-workspace was too wide and could close a window while the operator clicked around the
  file tree. The target must be inside `.xterm`, or nothing focused at all (`document.body` on a
  fresh load, which is the difference between "new session" having a shortcut and not). Any open dialog
  vetoes the lot, because focus sits on the body while one closes. The other half: **every switch hands the
  keyboard back** — window tabs are buttons and keep the focus they were given, so `XtermPane` takes a
  `focusRef` owned only by the active pane, which fits and focuses itself when selected. Background
  socket connections and pending image uploads must not steal that focus.

`tests/browser/terminal-ui.spec.ts` exercises both DOM and WebGL renderers with output exceeding the
server's replay limit, ANSI/UTF-8 split across messages, background terminal replies, resize while hidden,
size resent on every return, a lost WebGL context refitting the DOM fallback inside its pane,
window/session switching, screen preservation, input routing and cleanup when windows/sessions close.
It also checks all four split directions, mixed nested splits, pointer/keyboard divider resizing,
focused-only input, pane shortcuts, reconnect focus, saved layouts, fullscreen and narrow viewport
containment. It also checks pane detachment, one-tab groups, actual tab drags in all four directions,
live overlay movement/cancellation, nested drops and menu placement with unchanged socket attachments.
`terminal-layout.test.js` covers tab ownership, detachment, group docking, drop direction and size
constraints alongside tree reconciliation, geometry and directional focus
without a browser.
`tests/browser/terminal-live.spec.ts` is optional proof against the isolated backend PTY harness:
set `JD_TERMINAL_LIVE_READY` to that harness's `ready.json` and `JD_TERMINAL_EVIDENCE` to a temporary
output directory, then run that spec against a production frontend build with `JD_BROWSER_BASE_URL`.
It compares each xterm grid with kernel PTY dimensions after splitting and resizing, checks actual
input routing and the launch command/directory, and records screenshots and video. Its separate
detach/dock test uses real mouse drags, captures each overlay direction and checks native input after
dropping below and beside the target, with one attachment per PTY. Without the
readiness variable it is skipped. `JD_TERMINAL_LIVE_RENDERER=dom` selects the DOM renderer for a
comparison recording; the default is WebGL. The harness setup is documented in
[`../backend/processes-terminal-github.md`](../../backend/processes-terminal-github.md#the-terminal).

**Open-shell links are consumed once.** The page removes `cwd` from the current history
entry before creating the session, preserving other query parameters and the hash. A refresh cannot
replay a launch or recreate a closed session; a later explicit Open shell link can still launch anew.
The link gives the session no title: the prompt names the directory, and a title would have pinned the
row to the folder the shell started in.

**The page has no separate header or workspace bar.** A terminal is the one screen whose content *is* the
viewport. "New session" sits alone in the rail's strip; the emulator title bar contains the two
panel toggles and window tabs, with no shell, user or working-directory title. The one banner that stays
is a missing login account — a broken feature rather than information.
