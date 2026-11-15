# Decisions: Proxy and Security

Why these pages look as they do. The stories were told in `design-system.md` §14 and §15 until 0.9.0
and are moved here verbatim, under a heading per page. They are history: a rule they state is in
[`../standard.md`](../standard.md) or the page it links to, and where a story and the standard
disagree, the standard wins.

## Security draws what it watches (from §14)

**The Security section draws what it watches.** All eight pages use the reading register, and
since 0.7.1 the overview, Firewall and SSH each draw a wiring picture on the page's own ground,
in the `WireNode`/`AnimatedBeam` vocabulary the deployment and Configuration pictures
speak, with the line as the state (dashed where a layer is missing, red where one is off, amber
where it works but should not be relied on, moving where it carries). The overview's
(`perimeter.tsx`) is the two ways onto the machine — the internet's through the firewall, fail2ban
and sshd, and this browser's through the allowlist to the dashboard — and it describes the layers
and their states, not the reach of every port. The strip of seven posture checks above it
(`posture-strip.tsx`) and the findings it narrowed went with the automatic posture findings in
0.7.5. Firewall's (`network/firewall-picture.tsx`) folds the rules by where they lead — in from the
internet, through the firewall, out to each admitted port drawn as the product that answers there,
or into the default (`firewall-reading.ts`, unit-tested) — and its rule table names each action in
a `--tag-*` hue down the row's edge, ending on the default as its last row. SSH's
(`ssh-picture.tsx`) draws the port, password authentication, root login and the keyed accounts as
the doors a login can take, following the draft as it is edited; it replaced four tiles that said
the same facts one at a time. Firewall's defaults are a settings section under its rule table,
saved by the page's save bar (0.8.7, below); SSH
sets its directives in one centred column of `SettingSection`s behind the product's save bar, and
Intrusion's jail choices carry their watched service's mark, state and comparable readings (both
are described in the next paragraph). SSH, Firewall and
Intrusion each end on their own log — the auth log, the firewall's log, fail2ban's Activity — read
through its lens in one `Pane` under a title across the page, since a log needs the width a rail or a
half row does not have; the last day's counts are on Firewall's picture's nodes and are `ReadingLine`s
on SSH and Intrusion (0.8.7), and an attacker's address is blocked from the line it is on. Repeat offenders
therefore take Intrusion's row alone. Logins and Network combine related facts into fewer,
richer columns: an account and its terminal, an
interface and its kind. Their action columns are always drawn. Connections lists each address as a
one-line card that opens the address's own page (`network/connections/`). Tools is a two-pane working surface,
with a searchable choice rail and one visible form/result, retaining every other probe's work.

**Intrusion and SSH read like a project's pages (0.8.7).** Intrusion stacked a merged table and three
engines, fifteen tiles and four framed tables, into one column nearly six thousand pixels long. It
opens on an identity line (`HostIdentity` laid out as a project's header) whose state is the worst
thing true of any engine (`posture` in `security/intrusion-model.ts`, unit-tested) and whose one
command bans by hand through either engine (`ban-dialog.tsx`), then on a `WireFlow` picture
(`intrusion-picture.tsx`): who knocks, this server with how many addresses it refuses, and each
engine on the line it reads along with what it holds. The picture has one hub and the hub is the
machine, since no engine is in the packet's path; an engine that is not installed keeps its place
as a dashed ring, so the picture is one height in every state. Under it one engine is on screen at
a time as a view in the address (`?tab=fail2ban|crowdsec|suricata`, the first installed engine
without one), and the addresses every engine and the firewall refuse are the fourth view,
`?tab=blocked`. Each engine's view opens on its own line (`engine-head.tsx`). Its tiles are
`ReadingLine`s, a log lens's reading among them through `lens-reading-line.tsx`, which keeps the
press that narrows the log (`ReadingLine`'s own `onPress`: a button laid over the line, named by
`label`; `BarReading`'s line under a band has its own slot, `bar-reading-line`); Suricata's severities are one `BarReading`; repeat offenders are a
`RankList`. A blocked address, a decision and an alert are one line a row at one height
(`line-list.tsx`: a grid every row shares where the list has 42rem of its own width, two fixed
lines where it has not, measured on the list and not the window). A jail's detail is a page of its
own, `/security/intrusion/jails/[jail]`, with the other jails as a rail from `xl` and Alt+↑ and
Alt+↓ to walk them; tuning a jail stays a dialog, because it is a form. A fail2ban that is installed
and silent is a state with **Start** (its unit is asked after first, as Docker's is), its client's
error under a fold, and an absent one hands off to its package as CrowdSec and Suricata do.
SSH's four readings are lines on a hue each (`--tag-green` for a login, amber for a failure, red
for an unknown account, violet for the addresses behind them), and its directives are one centred
column of `SettingSection`s: a switch for a yes or no, `Segments` for three short words, a list
otherwise, each row one height with *sshd runs …* under a staged value. While the draft changes
something an *Edited N* chip stands at the end of the first section's title line and narrows the
page to those rows; no chip stands over the settings when nothing is edited. The floating save bar
(`save-bar.tsx`) replaced an apply bar of the page's own; **Save** opens a review that lists each old
and new value, what the change does to the dashboard's access boundary and which recovery stands
behind it, and a change that would cut the way in holds the review's button.
The diagram and the workbench have edges for §7's reasons; tables keep theirs, and forms and
findings remain plain. `components/security/marks.tsx` supplies the inline address, `PeerIdentity`,
process and interface marks. A product is named only when it can be identified; no logo is guessed
for ufw, sshd or an unknown interface. Source choices in the firewall dialog use the same lit
`ChoiceCard` as deployment choices, with Tailscale's own mark for the tailnet.

**Since 0.8.7 the overview and Logins open on a line and a picture, with no tile row.** The
overview's five linked tiles stood between its identity line and the picture, and four of them said
what a node already said. The picture (`perimeter.tsx`) stands directly under the line now and
carries each reading on its node: the peers and sockets on the internet's, the policy, the rule
count and the open ports on the firewall's, the jails and bans on fail2ban's, how a login gets in
(`ssh-reading.ts`) on sshd's, and a fifth node for who is signed in. A node's mark is the way to its
page (Connections, Firewall, Intrusion, SSH, Logins), so the row of links is the picture. A layer
whose read is in flight or failed is said to be unread, never drawn as a missing layer. Logins
(`logins-panels.tsx`) opens on `HostIdentity` in the project's layout — this server, how many are
signed in and how many addresses are trying as its state, the last login and the failed attempts as
its facts, the page's keys and Refresh at the far end — then a `WireFlow` picture
(`logins-picture.tsx`): where sessions are held from and who is attacking on the left, sshd in the
middle, the accounts on the right. A session's line moves; an attacker is in the danger colour and
moves while an attempt is under a quarter of an hour old; one the firewall already turns away
(`refusedBy`, read from the rules the layout polls) is grey and dashed. The picture is four rows in
every state and while it waits (`drawnFirst` shares the rows between who is in and who is trying,
the last row counting the rest), so the lists under it do not move. Under it a session is a
`ChoiceRow` with End session in its `⋯`, offered only with `system.admin` and `destructive` as the
route requires; attackers are a `RankList` (attempts and their share in columns, no bar per row)
with Block and the lookups in each row's `⋯` and "blocked" where the firewall already refuses the
address; the login record is the page's one framed table, a line to a row, and a two-line list
below `sm`. Empty, each list says so on one line at a row's height; a host without `last` or
`lastb` says which package is missing and links Packages.

## The proxy draws routes, engines and authorities (from §14)

**The proxy section draws routes, engines and authorities.** All seven pages stay in the reading
register. Sites opens on its identity line and the site band (§15, *The proxy's Sites page*); what
the reader has to act on first — a failed read, nginx not running, changes on disk nginx has not
loaded, with Test config and Reload nginx — stands under the line as a `Notice`. No page of the section
begins with a row of tiles since 0.8.7; each opens on its identity line. The overview took §15's exit
in 0.7.1 (pass 2 has the paragraph): it opens on the engine identity, its facts ending on the sites,
certificates, streams and internet-facing ports, each a link, the routes' verdict beside the name
with the engine's state, and its one command at the end with the rest behind `⋯` (pass 2's fifth
pass); then the route picture, Live traffic, the band of traffic, certificates
and recent errors, and the routes in the main column with attention in a narrower one. Live traffic
is the edge's hour added up from every site's access record — nginx's files and the Docker Caddy
ingress's routes alike, so a host where the ingress holds 80 and 443 is not drawn as idle — as four
`StatTile`s in a framed `StatGrid` (the last whole minute, the hour, the 5xx share, bytes served,
each with its `TileTrend`) over a `ChartPanel` of requests a minute with 4xx and 5xx lines. nginx's
own stub_status counters are a one-line strip under the chart, because they count only what nginx
answers: its switch is a `Switch` beside a `Status` rather than a button, because whether nginx is
counting is a state of the engine; a switch in flight reads "Switching on…", readings that stop lose
their figures and say why, and nothing on it breathes as live, since it is a poll. The nginx modules
a reverse proxy is asked for sit beside the version in the engine line's title, said only when some are missing ("2 modules
missing" in warning), whose popover lists each, a module a host package provides with its own
Install (and Install all when there are several) that runs through the overview's one package console
under the bar; they were a row of six status dots that read as findings, then the last fact of the
line, the one item that wrapped onto a row of its own. Recent changes in the rail is five two-line rows, so the rail ends near the routes it
stands beside. A source the overview could not read is never drawn as an empty or
healthy one: its fact in the engine line reads "couldn't read sites" with the reason as its tooltip,
its panel shows the `ErrorState`, the picture is not drawn from sites it cannot read, and attention
carries it as a finding whose button is Try again, so the all-clear line cannot appear over it. The
overview's Refresh is in the line's `⋯`, the page's age beside it ("updated 14s ago", the
oldest reading on the page, or "reading…" while a refresh is out, when it cannot be pressed); only a source that has not
answered after twenty seconds is said on the line ("No answer from sites"), and Refresh can be pressed
again. A status that fails after answering keeps its age, since the engine
identity still draws it. The engine line's one brand command follows the unit: Reload while it
runs, Start once it is stopped or failed, with Reload greyed in the menu and the state beside the
name saying why (a masked unit's Start keeps its reason on a focusable wrapper, since a disabled
button takes no hover). Whether it starts at
boot is said only when it does not: a warning with an inline Start at boot where `systemctl enable`
would fix it. A failed unit is the one `Notice` the overview draws under the line — the reader has to act on it
— holding systemd's reason in words, a fold that reads the journal when opened and opens scrolled to
its newest line, and its two verbs. A start or restart the config test refuses keeps its dialog
open on the test's diagnostics as rows (level as a `Status` verdict, file:line in mono, Open at line
N), not a toast of nginx's output; the editor a row opens closes back into that dialog, with the
keyboard on the row's button. A clean Test config is a success toast ("nginx config is valid", what it means, and Show output),
because a window-high panel holding one green line was all ground; a warning, a failure or a test that
gives no verdict opens the panel, and a test run from the open panel answers there. That panel is a
`SidePanel` of the same rows under a verdict `Notice`
(success, warning or danger: "Valid", "Valid with 1 warning", "Fails"), with the output in a quiet
fold (open when there are no rows to place), Copy output and Test again in the footer beside how long ago it ran, and a conflicting server
name's claimants as rows indented under it behind a rule, "served by" and "ignored in" each with its
own button (a name taken from a shared snippet adds "server_name in" the snippet's line under the
site's); while it runs it says "Testing…" rather than keep the last verdict under a new run, and a
test that gives no verdict is the panel's `ErrorState` with Try again, never a "Fails" `Notice`. The
last test's warnings and failure stay in attention as one finding whose button, Open test, shows it.
Its routes are ordered worst first like the Sites cards, eight with "Showing 8 of N". An
administrator's route opens the site on Sites and a Docker ingress route its live TLS report; a
reader's route opens its file read-only in place rather than the site form — skeleton rows while it
is read and an `ErrorState` with Try again when it cannot be, never an empty editor — and a route with
nothing a role may open is a disabled row. A site's and a stream's card separates identity, route and named
actions into three bands: `components/proxy/route-path.tsx` gives the source and destination their
own labelled columns (stacked on phones), so a hostname and its upstream do not compete for the same
truncated line. Sites and streams use a two-column grid on wide screens and a single column on
smaller ones. The nginx or Caddy mark names a site's engine, the stream's port names its product
where known, and the TLS reading carries Let's Encrypt's mark only where the certificate path
supports it. Unknown products keep a glyph. Until streams can forward, the Streams page puts a plain
block of two numbered steps above its list — the stream module, then connecting the directory — each
with its `Status`, a line of meaning and the button that does it where the page can; the step that
needs doing carries the page's one brand command, so "Prepare a stream" stays outline, and the
connect opens a sheet showing the file before anything is written. The install step keeps the recent
installs beside its button, the list its job console says a run is reopened from. A stream's Status is nginx's own state for it — live, not listening, shadowed, not read — on its row and its page, and its page says why one is not live (below).

**Streams took the deploy pages' shape in 0.8.7**, and the grid of cards and the stream half of the
route bands above went with it. The page opens on an identity line (the forwards as the products
their ports are, the module's state, the counts and the hour's sessions as facts, the verdict that
the Live tile was at its end and New stream beside it), then a wiring picture — the host's ports on
the left drawn as their products, nginx in the middle, the addresses they are handed to on the
right, a forward open to anyone in the warning colour and a database port open to anyone in the
danger one, a line moving only while nginx holds the port and bytes crossed it in the hour — and
then the forwards as one-line `ChoiceRow`s, one height in every state: the name over where it
listens and where that goes, the protocol, who may connect and the hour's sessions in aligned
columns, its state, and a menu. A test's outcome takes the hour's place on the row until the next
test. Why a stream is not live, nginx's bind error and the way to what holds the port are a
`Notice` on its page, which is a destination like a site's: the identity line, the views as tabs
in the address, the list as a rail from `xl`. Its traffic is `ReadingLine`s with each window's
shape at their end, the session lengths in words ("half inside 2.4 s"), the outcomes and the
clients `RankList`s; its form is a settings column with the one floating save bar.
Every site card, the overview's route rows and a certificate's links to the sites using it open the
site's own page (`/proxy/sites/<name>`): the same marks as an identity line, the way a request
reaches it on the page's ground, and its requests and errors read there in one log `Pane`, so a
site's Logs verb goes to that page rather than to a file on the Logs page. Editing stayed with the
card's verbs, as a sheet over the list, until 0.8.7; it is the site page's Settings view now (below). The overview
ends on the engine's own log, a `Pane` across the page under the two columns.

The TLS page (`/proxy/tls`) opens each of its three views on an identity line, with the views as
an underlined strip under it in the address (`?view=fleet`, `?tool=request`). A report's line is the
target — host and port as its heading, the grade as a word in its verdict's colour, the protocol,
what answered (its nginx site, which opens the site's page) and how old the report is — with the
scan's summary at the right and a `⋯` of Scan again, Copy as Markdown, the JSON and `chain.pem`,
Copy link and a request to the same name in the tester. Under the scan form the report draws the way
the scan reached the name (`tls-chain-picture.tsx`, its verdicts in `lib/tls-chain.ts`): this server
as the visitor, the domain and the address it reached, what answered, the leaf, the intermediate (or
the one missing, on a dashed amber line) and the root, each step on a tile in its verdict's colour,
the leaf's term as a meter under its days. A failed scan is the same row cut short at the name, the
port and the handshake, the failed step red and the rest not reached. The row is drawn unjudged
before anything is asked and while the first answer is on its way, at its loaded height, with a
pulse down its lines while a scan is in flight and once when the answer lands. The four tiles that
opened the report ("—" before a scan) are gone: each reading stands on its step. Every site is an
identity line saying the names, how many are scanned, the worst of the reports as its state and how old the newest
report is (how many are below A, expiring or unreachable are the chips over the list), with Scan all sites and an Export CSV menu, over one-line cards (the engine's mark with
the report's verdict in its corner, the name, its days, protocol, issues and age in aligned columns
and the grade last) that open the report. The request tester's line is the request: the URL, then
the answer's status in its colour, protocol, first byte, total and bytes read; its redirect chain
is cards that pick a hop, and a host with no nginx gets the section's empty card with Install.

Certificates (overhauled in 0.8.7) opens on its identity line rather than four figure tiles: the
issuers' marks, certbot, how many certificates, how many expire inside the renewal window and in how
many days, how many browsers refuse (expired, test or unreadable, the split in its title) and the
renewal reading as a link to the Renewal view, with the verdict and the split Issue command at the far
end and Import and Paste a certificate in its `⋯` menu. The command is split rather than doubled:
"Issue certificate" is Let's Encrypt on the brand face, and a chevron joined to it opens every other
way to get one — the local CA, self-signed, a signing request — as words in a menu; without certbot
the menu is the whole command. Under it the page's jobs are views in the
address (`?tab=`): Certificates, then what issues and renews them — Renewal, DNS challenge, ACME
accounts (administrators), Local CA, Signing requests — then Watched domains, with the transparency
logs. Each certificate is one `ChoiceRow` read down its columns — issuer mark, name over its names in
mono, where it came from, who names it, the expiry date and its status — with every verb in its row's
menu and the slot held where there is none, so a Caddy, test, unreadable or busy certificate is the
height of the rest; the grid/table toggle went with the four-line cards. The line, the strip and the
list's toolbar are drawn at once and rows of the list's height stand in while it loads, so nothing
moves when the reads land (the page used to grow as six reads arrived). A certificate opens on its
own page, `/proxy/certificates/<name>` (`?path=` where two share a name), in the process page's
shape: the way back with its one command on the brand face (the real issuance for a test
certificate, a renewal for certbot's, a new pair for an import) and the rest in `⋯`, its identity
line with its state in the tile's corner, a picture of who signed it, the certificate with its time
left and the sites and streams that serve it (a line carries only where the certificate is accepted
and the site is on), its facts and identity, then Key, Chain, Files and Timeline as views, and from
`xl` the list as a rail walked with Alt+↑ and Alt+↓. The sheet's address, `?cert=<file>`, lands
there. An unreadable certificate's error and a test certificate's sentence with its verb are Notices
on its page. What the server makes itself is said where it is read: the local CA's certificates read
`local CA` where the source is, and the page of one nothing trusts by default says where it is
trusted in a hint line. The Renewal view opens on the schedule — its recent certbot runs, what the
timer's last run did (a failed run is a danger Notice naming each certificate and certbot's reason, a
passing run whose hook failed a warning Notice with the hook's own words, each with Run now and Show
log; otherwise a Status and the next run), the "Reload nginx after every renewal" OptionRow — then
certbot's lineages, each run as certbot printed it (Show log brings it into view; it was a sheet),
and the renewal service's journal as a log `Pane`. A lineage's last failure is a line in its row, its
webroot folders wrapping mono paths rather than tags, and a certain next failure a warning Notice.
A watched domain opens a live report and preserves its nonstandard port. Password files, access
lists and watched-domain setup use the deployment settings' sections, each head over its fields; an
access list is a row you read (its addresses as mono tags, its sites as links, its include line with
Copy) with Edit inline and Delete in its menu. DNS providers remain readings with their own
actions. The providers are one list of choices, each a row of one height (mark, name, verbs): a plugin that is not
installed and credentials nobody saved are the expected answer for a provider nobody chose, so only a
provider with a token saved, or one a lineage renews through, says where it stands (*credentials
saved*, *plugin missing*, *no credentials*), and those stand first. The name keeps its width before
anything beside it; the plugin's id, the propagation wait and why a plugin cannot be installed from
here are behind the name. Until 0.8.6 all eight carried an amber *plugin missing* line. The TLS
report keeps findings, protocol checks and HTTP readings beside the live certificate and its
vertical chain; long header values wrap instead of hiding the verdict. Its scan field is one input
group, the address and its port, with a hint naming what will be scanned and a native datalist of
known names; a scan in flight is a line with its elapsed time and an outline Cancel. The Ports page (0.8.7) opens on
its identity line — "9 listening · 5 internet-facing", the count's make-up its tooltip, the dangerous
exposure ("2 dangerous services exposed") beside the name as the page's state, then how old the list
is (the protocols and the private networks are the chips over the list, which networks the chip's
tooltip), with Find a free port as the line's command and
Refresh, Pause and the two exports in its `⋯` — where four tiles stood; then
`ports-picture.tsx`, who reaches what: the networks a socket answers on (the internet, the tailnet,
a VPN, a private network, Docker's networks, only this server) wired to the programs listening
there, a wire red where the posture calls the exposure critical and moving only while somebody is
connected; a network is a button that narrows the list like its chip, and each program's ports open
their pages. Below `lg` the networks are not drawn and each program says who reaches it. Listening
sockets stay a framed table read down its columns, one line per row at one height (`h-12`): the
port is the link to its page with the protocol, the `New` tag and the addresses after it; the
application is its product, its name and how it runs (or the command), the account and PIDs its
tooltip; reach is one coloured word with the interface, a database's firewall default its tooltip;
the firewall's verdict is one word whose conclusion is its tooltip; clients a count. The three named
columns sort from their headings with `aria-sort`, and a phone gets the same orders as one Sort menu
over rows of two lines at one height (`h-16`). Grouped by application, one owner's sockets are a line
whose name is the disclosure button. A failed poll is a warning `Notice` over the rows it kept. A
port's page (`/proxy/ports/tcp-443`, the address added where two rows share a protocol and port;
`port-page.tsx`) is built as a process's: the way back, the socket's verbs, its identity line (the
owner as its product, the port, addresses, account, PIDs, how it runs, and the reach as its state),
then Reach (`port-path.tsx`: who can reach it → the firewall → the port → the program, then where it
answers, from outside, the Docker path and Identify), Owner, Clients and History as an underlined
strip in `?tab=`, and the list as its rail from `xl` with Alt+↑ and Alt+↓; the side sheet's
`?socket=` address, in either spelling, opens it. Below the table, Changes is a plain panel of lines
grouped under day headings — the minute, the change as a `Status` word coloured as the table colours
that socket, the port, its addresses, the program and where it answered — with its window as
`Segments` in the header and the reach chips under it; on a port's page it is that port's. Tables retain their scrolling
boundary; forms and sections remain plain.
`tests/browser/proxy-ui.spec.ts` covers all six populated pages at 390, 1280 and 1720, detail
navigation, site-kind choices and read-only access; `proxy-engine-overview.spec.ts` covers the
overview's failure states, its freshness and Refresh, its routes by role and the engine controls,
including a stopped, failed, masked and boot-disabled unit, a refused start, restart or reload, and
the config test panel; `proxy-insights.spec.ts` covers Live traffic, the ingress's routes in it, Recent errors, the nginx counters' switch (pressed on the card as well as the switch) and a phone;
`proxy-alerts.spec.ts` covers the Alerts page — its picture, what fires, the rule form — and the
overview bar's firing count.

**Traffic reads the edge's hour as a project's Logs page reads one deployment** (`/proxy/traffic`,
`site-traffic-view.tsx`, 0.8.7). It opens on its identity line — the engine's mark, "Traffic", the
hour, the sites (a link to Sites), how many keep no access log, whether the counts are floors and how
old the read is, then the hour's verdict ("Answering", "3.2% failing") and a `⋯` with Refresh, Open
Sites and, for administrators, Alert rules — and then four `ReadingLine`s in place of the four bare
tiles it had: requests a minute, the share that failed in the Logs page's words ("1.0% of requests
failed", "12 of 1,260 answered 5xx · 30 refused with a 4xx"), the slowest site's time in words ("19
in 20 inside 412ms") and what was sent, each with its hour as a trend in its own hue. Under them the
hour minute by minute, every site added up, in the request chart's series with its legend on one
line. Sites and Errors are tabs in the address (`?tab=errors`; `?view=errors` still opens it): Sites
is one `SplitBar` shared out by site in `LANES` hues over a `RankList`, busiest first, a site with no
readable log last saying so, each row opening the site's page; Errors is nginx's error log grouped and
explained (`ErrorLog`), over a window as `Segments` and narrowed to one site by a select, its tab
counting what it lists. A host with no site, or none whose log can be read, says so in one
`OverviewEmpty` card with the press that changes it (New site, the one site's page where its access
log is turned on, or Sites) rather than four zeros; a host with no reverse proxy draws the section's
`EngineSetup`. One site's requests were a second, older copy of that site's own page here
(`?site=`); that address now lands on the site's page, and `?site=…&view=errors` is the Errors view
narrowed to it. `proxy-traffic.spec.ts` covers every state at 1280 and 390.

**Configuration and Tunnel open on what they are about.** Neither draws a way back over its line,
as no page of the section does. Configuration (`/proxy/config`) is an
identity line — nginx's mark, its directory in mono, how many files in how many folders, how many
nginx reads, and for an administrator the last config test as a
coloured word beside the directory (who wrote the files is the chips over the list) — with Test config as the line's brand command and a `⋯` of Read again, the main
file, the last test and the engine overview. Under it the views are an underlined strip in the
address (`?tab=effective`, `settings`, `lint`, `history`; Files has none), administrators only as
before. Files opens on the directory as nginx walks it (`config-picture.tsx`, its lanes from
`includeMap` in `config-tree.ts`): the main file, the folders it includes, the folders their files
include — sites-enabled's links into sites-available, a site into snippets — and, closing the last
lane unwired and dashed, the folders nginx reads nothing in; a folder's line is red where a link in
it points at nothing, and pressing a folder narrows the list under it. Settings is the deploy
settings' column (`SettingSection`s by what the directives decide, each a field with the file and
line that sets it at its right, the line a change writes previewed under it, an Applying switch for
the reload, one save bar); its four tiles are the identity line's facts. A file's history is the
editor sheet's History view beside its File view (`?view=history`), restore where it was; the old
`?history=` address lands there. Without nginx the page is the section's install screen. Tunnel
(`/proxy/tunnel`) is Cloudflare's mark and the tunnel's name, its edge connections as a state, how
it was made, the connector and how old the reading is, with Check now as the command and Disconnect
in the `⋯`; then the way a visitor arrives — each hostname into Cloudflare's edge, through the
connector, to this server's proxy, a line moving only where both ends are up and red and dashed
where one is not — and the hostnames as one-line cards with their route and record in aligned
columns, a refused record's reason taking the project's place on the line. Not connected, the same
line says so, the same picture is drawn dashed, one sentence, `Segments` between pasting a token and
creating one with the API, and that one form; a reader gets the picture and a Notice, no form.

**A Sites list that could not be read says which read failed, why, and can be asked again.** The list
is read from nginx, a Caddy on the host and the Caddy container that holds the public ports, and the
last can fail on a server where the first two are fine (Docker not answering, two public Caddy
containers and no word on which is the ingress, a Caddy that publishes 80 without 443). The page was
two words in a red box. `proxy/sites-read-failure.ts` names the block for what was being read
(*Could not read the sites*), draws the answer's `reason` under its message and its `raw` behind
*What the server said* where the server sent them, points a bare 500 at the log line with its request
id where it did not, and always offers **Try again**, since the read changes nothing. The notice over
rows kept from the last good read carries the same reason and disclosure. `proxy-sites-list.spec.ts`
holds both at 1280 and 390, with and without a reason.

## The proxy overview (§15 pass 2)

The proxy overview took the exit in 0.7.1, at the operator's request, because it had no life:
four grey tiles (sites, certificates, streams, internet-facing ports) over a column of route cards
that each drew the engine's own logo, so a host fronting Grafana, n8n and Gitea was nine nginx
marks, and nothing on the page moved but the Live traffic sparklines. Each figure went where it
is said better. All four are facts at the end of the engine's identity line — *9 sites, 8 on
TLS*, *9 certificates, 1 needs attention* in its tone, *3 streams*, *3 ports internet-facing* —
each a link to its page, and each "couldn't read …" with the reason as its tooltip when its
source fails; the sites are also the picture's Domains lane and the Routes head's *Showing 8 of
9*, the certificates the Certificates block, and the front door under the engine in the picture.
The line's right end carries a verdict beside the service commands: how many routes are down,
or partly down, or that all of them answer, from the upstream check (`routeVerdict`), and nothing
until that check has answered. Under it the proxy is drawn as what it does
(`components/proxy/route-picture.tsx`, over the pure, unit-tested `route-map.ts`): the domains
on the left, each a lock in green for TLS or amber for plain HTTP with its hour and 5xx share,
wired through the engine in the middle — its front door under it — to the applications on the
right, each the product that answers on its address (`backendOf`: a loopback port read against
the sockets this host holds, a container as its image, a program as itself, a name on Docker's
network as its container, a socket as its program, and a glyph where nothing names it), with
the upstream check's state in the tile's corner. Down routes lead, then the busiest, up to
eight. A wire is the route's last hour from its access log: it pulses while the route served
more than a request a minute, quicker the busier (`requestPulse`, the Network section's curve
over requests), red where it fails or the upstream refuses (dashed then), amber in plain HTTP
or where the upstream times out; pointing at a domain lights the application it reaches and the
other way round. The band under it (`overview-band.tsx`) is three bar readings (§10): Traffic, the
hour's requests shared out over the busiest five, each rank a `LANES` hue, its 5xx share and its
trail on the line; Certificates, the five that run out first as ticks in the colour of their state
on one ninety-day axis, the rest counted at the strip's end, each opening its certificate; and Recent
errors, the hour's 5xx by site from the same access records over nginx's error-log groups where
nginx is on the host, each opening the site's requests or the error log. Until 0.8.5 these were
rows under each bar: Traffic's a `ShareBar` over what each site reaches, Certificates every
certificate as a dot on a runway with certbot's renewal stretch marked over the five soonest as the
expiry `BarList`, each block opening on its figure at the 24px reading size. Each head holds its
one figure now (requests this hour, days to the next expiry or how many need attention, server
errors this hour), and a site opens its own page, where its requests are read (for an ingress
route, from the deployment's record). The
route cards lead with the application's own mark and the engine in the tile's corner
(`RouteMark`), and are one line: what serves the route and the way a request goes (its names,
only where they differ from the route's own, then the upstream or folder) under the name, and
the hour's traffic, the ports said once each (`listenPorts`: `80` and `[::]:80` are one), TLS
and the serving state at the far end. nginx's `_` reads "any host" (`routeNames`). They were
two lines with the listen addresses spelled out at the far edge, twice the height of what they
said.

The overview took a second pass on 2026-10-23, at the operator's request, for its quiet
states. A host with no reverse proxy opens, under the identity line, on `EngineSetup`
(`components/proxy/engine-setup.tsx`): the question at the 24px rung ("Put nginx in front of
this server"), the brand command at `size="lg"` with the exact package-manager line beside it,
certbot riding along on an `OptionRow` that is on by default, the job streaming under it, and
a rail of what the page draws once nginx runs. It was two `xs` outline buttons over an empty
Certificates block, an empty Routes list and a Recent changes rail; none of those are drawn
without an engine, and the identity line drops its zero counts (the open ports stay). Where a
block had nothing to read it said so in one shape, `OverviewEmpty` (an icon, what is missing,
why, and the one way forward as an outline button where there is one), rather than a grey
sentence at whichever size each block had chosen; the band's blocks say it in their own `message`
since 0.8.5 (§10), and the card is Live traffic's. With no access log read anywhere, Live
traffic says why and opens the site (or Sites), the band's Traffic block says so on its one line,
where it used to drop out and move the routes under it (only a host whose engines keep no access
records draws neither it nor Recent errors), and Recent errors leads on nginx's error-log lines, since a 5xx count
with no log to count from is unknown, not zero; an error log whose every group is a notice no
longer draws an empty finding list's "No problems found" under the block's own all-clear.

A fourth pass on 2026-10-24 cut the engine line to what is not the expected answer, because
the bar still ran to a second row on a laptop: "starts at boot" is gone (only "won't start at
boot" is said); the config directory is the name's tooltip, certbot's version and renewal timer
the certbot fact's, and "Updated 14s ago" Refresh's; the uptime reads *up 2h* beside the green
dot rather than *running for 2h 1s*; and a build with every module says nothing.

A third pass on 2026-10-24, from an operator's screenshot of a one-site host, took out what
the page said twice. The title is *nginx 1.24.0*, not `nginx -v`'s "nginx nginx/1.24.0"; who
renews rides on the certbot fact (*certbot 2.9.0, renews via certbot.timer*), with only an
unscheduled renewal standing apart in warning; a zero count of certificates or streams leaves
the line, since the Certificates block says it with the way forward; a path in the line is set
at `0.9em` so the mono face does not stand taller than the words round it; and the modules
fact carries a chevron, because it opens a list. The command is *Reload nginx*, which beside
the page's Refresh had read as reloading the page. In the picture the lanes' headings hang
above their lists and the engine's caption under its tile, each column padded evenly to hold
them, so every mark sits on one line and a one-to-one route is a straight wire rather than an
S bent to meet marks the headings had pushed down; the front door names the engine's own port
*open* (`frontDoorLine(doors, self)`) rather than "nginx" a third time. Live traffic leads on
the server's reason where it gives one instead of a sentence of ours it contradicted, and its
button says it opens a site; the stub_status strip is *nginx counters* and drops the "On"/"Off"
word where the switch beside it already says it; the empty Certificates block drew the open
lock the picture uses for plain HTTP rather than a shield with a tick, until its card became the
block's `message` in 0.8.5; and Recent errors,
without an access log, gives the error log's zero for the hour once rather than again under it.

A fourth pass on 2026-10-24, at the operator's request, made the page read as one product with
the Routes cards they liked. The header is one bar: the identity line (`HostIdentity bar`, a
40px tile on a framed `bg-card` bar rather than a 48px tile over a hairline) now carries the
page's own controls — how old the page is, Refresh as an icon button, and **Alerts** as a link
whose firing count stands beside it in red (holding in amber) — before the engine's commands,
where they had been a row of their own over a band of empty ground. Every quiet block became
`OverviewEmpty`'s card, the Routes row's shape: a tile drawn as the product the block is
about with what is missing in its corner (`EmptyMark` — nginx with a log for no access log,
Let's Encrypt with an open lock for no certificates), the title and its reason beside it
on one line, and the way forward at the card's far end where the card is wide enough
(a container query, since it stood full-width under Live traffic and in half the band). The
16px glyph in a column of its own over 12px grey text left a gutter down each block. No
certificates was amber-edged, with **Issue a certificate** at its end for an administrator; an hour in which nothing failed a green-edged card with a tick on nginx. The band's blocks gave those
cards up in 0.8.5, because a block keeps one height (§10): they are its `message` now, *Nothing
failed in the last hour* where an access log counts 5xx, *nginx logged no errors this hour* where
none does, since a 5xx count with no log is still unknown, not zero, and Issue a certificate
stands in the block's head. Live traffic's empty is still the card. The nginx
counters are a card the same shape whose whole surface is the switch's `<label>`, so a press
anywhere flips it, with the readings at the body size; Live traffic's requests draw in
`--chart-2` rather than the near-white brand. Alerts left the sheet for a page of its own,
`/proxy/alerts` (`components/proxy/alerts-page.tsx`, in the rail for administrators), drawn the
way Notifications is: a picture of what the rules watch — certificates as Let's Encrypt, the
engine as itself, upstreams, sites and watched endpoints, each wire red while something there
fires, amber while one holds, dashed and pressable where no rule watches it — wired through
this server to each channel as its own service; under it what is firing as cards in their
state's edge with Mute, the rules as `ChoiceRow`s (each kind's product with its glyph in the
corner) that open their form in a sheet, and beside them what was told lately and a test per
channel. The same day the cards were brought down to the size of a `ChoiceRow` and a
notification channel's card, because the operator found them too big with their verb not part
of them: a 12px inset and a 56px floor, the title over a hint-sized reason, and the verb a small
outline button at the far end (under the words, never the tile, where the card is narrow), the
amber edge rather than a filled button carrying that no certificate was a problem. The alerts
page's all-clear and firing cards take the same size. The host's Alerts page under Metrics
(`/metrics/alerts`, 2026-10-25) is the same page over the machine's own readings — CPU, memory
and filesystems as their glyphs, containers as Docker, backups as an archive — and draws its
channels, all-clear, subject cards and history from the parts the two share
(`components/alerts/alert-parts.tsx`) rather than a second copy of them. Since 0.8.7 it opens on
an identity line in a project's layout: what the rules are doing as the state, *Add rule* as the
command and *Check now* behind the menu, where the two stood on two section lines; a rule's row
keeps one height by saying why it cannot be judged on its own line; and the page is drawn in its
shape, picture included, while the rules are read.

A fifth pass in 0.8.7 made the section's pages open alike. The overview's bar held five controls
of one weight (a refresh glyph, Alerts, Test config, Reload, a menu) in a frame; six pages
overhauled after it kept their verdict and commands in a cluster at the line's far end; and two
of those stood under a "Proxy & TLS" link the others did not draw. Every one is
`HostIdentity`'s project layout now (`state`, `command`, `menu`): a 40px tile, the name with its
state beside it, the facts under them, one brand command at the end and the rest behind `⋯`, on
the page's own ground with a rule under it — and no rule where a view strip follows, which draws
its own. The overview's command is the one the service calls for; its menu (`EngineMenu`) leads
with Test config and Alerts, then Refresh with the page's age beside it, counted only while the
menu is open, then Service details and, last, Restart and Stop. What fires is a red link on the
line ("1 alert firing"), since a way to the Alerts page with nothing beside it said nothing. A
route is one line in every state: its upstream's reading is a word on the line (`UpstreamWord`,
every address in its tooltip, shown at a phone's width only when something is down) where a
list hung under the row; the rows are a plain `ChoiceRow`, the size Streams, Sites and
Certificates draw; and the engine's log is the height every service's log takes. Each line has
a state in every reading (reading, not read, nothing yet), so it is one height on a phone, where
the state has a row of its own, before the read lands and after. `/proxy/alerts` opens on such a
line — how many rules are on, what fires or holds, who is told — with Add rule as its command;
the server in its picture says how often the rules are checked, which a paragraph under Rules
used to; a catalogue card is a name over a few words with the sentence in its tooltip; and an
account that only reads is told the page is an administrator's rather than shown the refusal.
The Streams form's field help is behind each label (`Field`'s `info`), a line staying under a
field only where it says what is wrong with what was entered. `proxy-opening-lines.spec.ts`
walks the section at 1280 and 390.

## Security's second pass (§15 pass 2)

The Security section took a second pass in 0.7.1, because the operator found its pages grey and
still. The overview's figures count up as they land (`NumberTicker`) and Intrusion's carries the
bans per day as its `TileTrend`; over them, the posture is its seven checks as a coloured strip;
under them, the ways onto the machine are a wiring picture. SSH took the exit for four of its
eight tiles — the port, passwords, root login and the keyed accounts went into the picture of
sshd's doors, which reads them as one answer — and kept the auth log's four (lines since 0.8.7). Firewall kept its
tiles and gained the picture of its inbound path over them. The three pictures are described
under *The Security section draws what it watches*.

## A proxy site, and Sites (§15 pass 2)

A proxy site's own page took the same exit in 0.7.1, at the operator's request, because it was
four grey figures — requests a minute, server errors, refused, upstream failures — over a
two-column route line and its logs, with nothing on it that moved. Each went where it is said
better: the rate and the probes refused are the first node of the route, the server errors the
identity line's verdict — a press of which narrows Requests to them and brings the pane into
view, as the tile's did — and the upstream failures the Errors tab's count, which is the day the
view holds. The line keeps the Sites card's facts and adds when the file was edited; its verdict
(`components/proxy/site-overview.ts`, unit-tested) is the hour's failed requests, else a pool's
servers failing, else the certificate, else how much it answered. Under it
`components/proxy/site-route-map.tsx` draws the way a request reaches the site in the wiring
vocabulary the dashboard's own request path speaks: who asked as the browsers and bots they were
with their share, the names with whose certificate answers them and its term on a meter, the
engine with what the file switches on in the hue of its kind (`--tag-green` for what guards,
`--tag-blue` for protocols, `--tag-cyan` for what saves work), and what answers behind it — a
pool's servers with the check's reading, or the program holding a local upstream's socket drawn
as its product, and *nothing listens* in red where a loopback port has none. Its wires carry the
readings: a pulse quicker on a busy site, red to a server that refuses, amber through a
certificate in its renewal window, still on a site that is off. The logs became the deployment
Logs page's `Pane` — Requests, Insights, Errors and Log files in one strip, the service logs
`flush` under the last as Output sits under the deployment's — and Controls three to a row, each
setting in its kind's hue with on, off or *no module* beside it.

In 0.8.7 the page took the shape of a process's and a stream's. Its line is laid out as a
project's header: whether it is serving and its verdict beside its name, one command at the far
end (the deployment that writes the route, else Edit, else the file) and the rest behind `⋯`,
where the verbs stood on a row of their own above it. Its views are an underlined strip with the
view in the address — Overview (the picture, what its link or owner makes of it, Balancing,
Controls), Logs (the pane, under one line where the site keeps no log of its own) and Settings —
and the Sites list stands beside it from `xl` with the list's own search, chip and order
(`site-rail.tsx`), Alt+↑ and Alt+↓ walking it. **Settings is the site form**, which was a sheet
4,280 lines long: one centred column of `SettingSection`s split across parts (`?view=`), what a
field is for behind its ⓘ rather than under it, the file a save would write with its changes,
checks and notes as the last part, a `PendingStrip` naming the parts that hold unsaved changes,
and one floating bar. The bar is the form's own (`site-settings.tsx`) rather than the
deployments' `SaveBar`, because a site has two saves — and reload, or only save; for a disabled
site as it is, or enabled — and drawing one Save would have hidden which. It is up only while
there is something to save: an edit, a file the form would write differently, or a disabled site
to enable. Leaving Settings with unsaved changes asks what closing the sheet asked; a link inside
the form is a trip the draft is kept across. The form's fields are written once and stood in
either layout (`SiteFormBody`): a new site is still made in the sheet. The audit
log, which had no figures at all, gained four readings of
the last day read from their own query so a filter narrows the trail without narrowing them:
the changes with their hours as a trend, the failures, the people as their faces and the
sign-ins with the refused ones.

The proxy's Sites page took the exit in 0.7.1, at the operator's request, because it had no
life: four grey counts — sites, on TLS, plain HTTP, disabled — over a grid of cards that each
drew the same nginx mark, so a Grafana and a blog read alike. Each figure went where it is said
better. Sites is the identity line's *12 sites* and the All chip; On TLS the line's fact and the
Certificates block's head; Plain HTTP a chip, a cause in the verdict and a press under the
Certificates block that narrows to it; Disabled a fact in the line — with the sites nginx still
serves until it reloads said beside it in amber — and a chip. The line is `HostIdentity`: the
host's sites drawn as the applications they front, the commonest first, then the host's name,
the engines with their versions, how many sites serve (none of a stopped engine's, none nginx
has not loaded), how many are on TLS and the hour's requests; at its right end the verdict —
how many sites need attention, said by the worst thing about each (a broken link or an
application refusing connections red, plain HTTP, a parked site or a change not live amber), a
press of which narrows the cards to exactly them through a *Needs attention* chip — beside
Export all, Import and New site. Under it `components/proxy/site-band.tsx` answers what the
tiles never did, in three bar readings (§10): **Traffic**, the hour's requests as spans of one bar
in each site's hue with the busiest five named, a 5xx share on the line in amber from 1% and red
from 5%; **Certificates**, the enabled TLS sites' certificates counted expired, due for renewal and
valid on one bar, worst first, the line naming the soonest to end in that standing with its days
left; and **Applications**, what the sites stand in front of drawn as itself, those refusing
connections first, the line carrying the health check's answer. A press opens its site. Until
0.8.5 Traffic's five and Certificates' five soonest were rows under their bars, the second on a
`StandingBar`. What a site fronts is read off the Ports page's
sockets (`components/proxy/site-apps.ts`, unit-tested): the container's image, else the program,
else its unit — never the port alone, so an upstream nothing on the list names keeps the
engine's mark. A site's hue is a lane hue without slate, the bar's muted "everything else"; the
card's edge carries none (0.7.5, at the operator's request: the stripe read as noise). The card leads with the application tucked over its engine
(`ProductLogos`), names it in its second line and at the route's far end beside the address, and
draws a static site's directory as *Files* and a redirect's target as *Redirects to* where both
read "Served by configuration"; its hour is a short bar against the busiest site's. A site
whose application refuses connections joins Needs attention, since its visitors get a 502.
Chips that name something wrong carry its tone in their word, and a site new since the last
read rises (`useArrivals`).

In 0.8.7 the card became a row. A site was about 190px of card — its route between two rules,
its notes, and Edit, Raw config and a menu at its foot — two to a line, each as tall as the
tallest thing it held. It is one `ChoiceRow` now, one height in every state: the overview's
`RouteMark` (what it fronts, the engine in the corner), its name over its kind and
`names → destination` in mono, then its hour, ports, TLS, certificate and state in aligned
columns, and every verb in its menu. The state is one word: an upstream that refuses takes the
place of *serving*, since that is what its visitors meet. The notes and the feature words are on
the site's page; the Cards/Table switch went, since a row read down its columns is the table.
The verdict moved beside the host's name, New site is the line's command, and Import and Export
all are behind its menu. Password files, Access lists and Deleted & backups are titled sections
of one-line rows with no sentence under the title: a file's logins, and a list's addresses,
sites and include line, are in the sheet its row opens, which is also where a list is edited.
