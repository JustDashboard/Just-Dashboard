# Decisions: Network

Why these pages look as they do. The stories were told in `design-system.md` §14 and §15 until 0.9.0
and are moved here verbatim, under a heading per page. They are history: a rule they state is in
[`../standard.md`](../standard.md) or the page it links to, and where a story and the standard
disagree, the standard wins.

## A network device (§15 pass 2)

A network device took the same exit in 0.8.7 (`network/interfaces/device-page.tsx`): its sheet
held a chart, eight blocks of facts and five forms. It is `/network/interfaces/[name]` with the
Interfaces list as the rail, its relations (what it rides on, its ports, the namespace it leads
into, where it answers) drawn as one `WireFlow` picture rather than listed as facts, and its
forms as settings sections under one save bar. The namespace cards beside the list gave up the
devices listed under each row for a count on the line; the devices are the namespace's sheet's.

## Interfaces (§15)

**The interfaces gave up their table in 0.8**, at the operator's request, because it was the one
framed block on the page and read as another product's: a box with a toolbar strip, column heads and
a scroll of its own under ten unframed charts. Nothing in it needed the table's reason for an edge
(§2 — a grid that scrolls sideways and clips a row's actions): an interface has no actions, and the
cap that made it scroll was there only because "Everything" ran to thirty rows. It is a plain block
now, the Real devices / Everything toggle in its head beside the up count as Top processes keeps its
order, and each interface is drawn the way Filesystems below it draws a disk — its mark, the name
and kind with the two rates keyed by the bar's colours, the bar, then its addresses and its errors
and drops, amber when there are any. Two to a row from `lg`, like the charts above them, so
Everything is half the length it was and needs no scroll. Metrics now has no framed block at all.

## The Network section (§15)

**The Network section was made in 0.7.1 out of four Security pages**, at the operator's request: the
firewall, the connections, the interface list and the tools left Security, which kept what is about
who may get in, and joined new pages that change the network as well as read it. Every page is a
reading page, and what makes them alive is the sanctioned five, held hard:

- **Things drawn as themselves.** A device is the product that made it (`network/marks.tsx`:
  Docker's bridge, Tailscale's tunnel, WireGuard's, a container's veth as the product its image runs)
  or a glyph for its kernel kind; an upstream resolver is its provider; a tailnet peer its system.
  Ten marks arrived for it (WireGuard, AdGuard, Pi-hole, CrowdSec, Headscale, Mullvad, Quad9,
  Unbound, OpenVPN, ZeroTier), each lifted to the L 0.72 rung where its own colour vanished on the
  ground (`public/logos/NOTICE`).
- **Pictures whose wires are traffic.** Six pictures stand unframed on the page's own ground in the
  deployment section's vocabulary: the Overview's topology (`network/topology.tsx`: the outside —
  the internet through the uplink, the tailnet, each tunnel — this server with its firewall and
  gateway as facts, and the inside — each Docker network with its containers' products, each bridge
  made here), the Connections page's callers and what they reached (`connections/picture.tsx`), Routing's
  decision (`routing/decision-map.tsx`: the policy rules in the order they are asked, each wired to
  the table it looks in, the one answering this browser lit), each WireGuard tunnel's devices and
  sites (`vpn/tunnel-picture.tsx`), the gateway's forwards, and DNS's resolver chain. A wire carries
  a pulse while its device moves more than a kilobyte a second, quicker the busier it is
  (`pulseDuration`, logarithmic over eight orders of magnitude), running the way most of the bytes
  go; still while idle, dashed where the thing is down or not set up, amber where the internet
  reaches a server no firewall filters. Where nothing is set up a dashed ring stands where it would
  go, and is the way to set it up.
- **Figures that move.** The live ring (`use-live-traffic.ts`, two-second points topped up with only
  what the page has not drawn) feeds `LiveBytes` figures, each tile's fifteen minutes as its
  `TileTrend`, the device rows' two-minute sparklines and every chart's Live window.
- **Colour that names a kind.** A device's role takes a lane hue (`ROLE_HUE`, from the palette that
  cannot be read as a state), an address's prefix the muted ink (`Cidr`, `components/address.tsx`), in and out
  their chart colours everywhere they are drawn.
- **The lit edge on what is taken.** Peers, forwards and lists open their sheets and a device its
  page (0.8.7), and all are lit rows; route tables, rule lists and shaping are readings and stay framed tables. (The tailnet's
  machines were a framed table until 0.8.7; they are cards now, below.)

What the guard refuses is drawn, not hidden: a control the server would refuse stays, disabled,
with the guard's sentence beside it (a device's *Set down* on the uplink, forwarding that Docker
needs), because a control that disappears says nothing and one greyed out with no reason says less.
Each page that reads a tool this host may not have opens on its install where it is missing
(`network/install.tsx`: the package's name, what it would do here drawn as the product, and the
Packages page's install job streaming under the button). The Git page and Tailscale on the VPN page do
the same, without the after-install phases the server keeps only for the network and security tools
(`phases={false}`); Tailscale is offered only where the package manager carries it
(`usePackageKnown`, one `GET /packages/tailscale`), since Debian and Ubuntu do not until its own
repository is added, and is pointed at otherwise. PM2 is an npm package for a host account, not
something the package manager installs, so its page names the command and opens the terminal
(`procs/pm2-missing.tsx`). The same file draws PM2's two other absences under its mark: installed
with no account's daemon running (it starts with the first application, so the block names
`pm2 start`), and not answering (what the page asked for, PM2's own words under that sentence
rather than as the headline, and Try again). What the Docker pages say when the daemon cannot be
asked is which fault it is (`docker/unreachable.ts`): a socket that refused the dashboard, a `docker.service` systemd says
is not running (with **Start Docker** for `service.control`), a daemon that runs but not at the
address asked, no `docker.service` at all (not installed: **Install Docker** for `system.admin` where
the package manager carries `docker.io` or `docker`, Docker's own instructions otherwise), or no
answer where systemd cannot be asked. The client's own sentence is behind *What the server said*.

## Drift, the overview, Firewall, Gateway, Egress, VPN and Protection (§15)

**Network drift took the exit on 2026-10-10**, at the operator's request, because it was dead:
four grey figures (known differences, incomplete readings, matching observations, owned repair
proposals) over six panels of mono paths that each said one state word, a route named by the number
the saved configuration gave it, and nothing on the page that moved. Where each figure went, and
what took their place (`components/network/drift/`):

- **The identity line.** `HostIdentity` with the dashboard's own mark, when the host was inspected
  (ticking), how many comparisons, the saved spec's digest and the journal's phase; at its end the
  verdict (`driftVerdict`): a failed read before a configuration that changed under the inspection
  before any difference, the differences counted in red while another owner holds one, its dot
  breathing only while an inspection is in flight. A press narrows the table to them. The other
  three figures are the table's chips, and the proposals the plan's own review button.
- **A picture of where the saved configuration goes** (`drift-picture.tsx`), the backups map's
  shape: `spec.json` wired to the rendered files, the kernel objects, the boot unit and the
  blocklists, each drawn as the product that reads it — netfilter's flame for the nftables files and
  admission chains, systemd's brackets for the units, Linux for the kernel's devices, routes and
  settings, WireGuard for a tunnel, Spamhaus and FireHOL for their lists (four marks arrived for it,
  each the project's own artwork, `public/logos/NOTICE`). A wire takes the worst state in its domain
  (red, amber, green, plain, dashed for nothing to compare), every comparison is a block in its
  state's colour beside the domain, the wires pulse while an inspection is in flight and once as
  each lands, and pressing a domain narrows the table to it.
- **Since this page opened** (`drift-rhythm.tsx`): each inspection a beat on fifteen minutes that end
  at this second, split into its states and sliding left as the clock runs, the countdown to the
  next one under it, and each comparison whose state moved between two of them, newest first. It is
  the page's memory and nothing else; a host that has not drifted says how many inspections it has
  stood through. The three configuration identities sit under it.
- **One framed table of every comparison**, worst first, each as its product, named the way a
  reader knows it (a route by its destination, an address by its prefix and device), its kind in
  its lane hue, and its evidence read in place — expected beside observed, a digest as seven
  characters, amber where they disagree. A row whose state changes rises.
- **The boot unit as one line** (`drift-boot.tsx`, 0.8.7): systemd's mark, the unit over its four
  facts, its last activation and how long ago, and its state with the reason under it; the caveat
  about reboot attribution is the `ⓘ` beside the state, where a framed notice stood. Its activation
  as its run (each `ExecStart` as the tool it is over a rule in the colour of its exit) and the
  evidence open from the line as a popover. **Each blocklist** is its publisher with its cache,
  render and kernel set as a chain, a link amber where the later one has fallen behind. **The
  repair plan** (`drift-repairs.tsx`, 0.8.7) is one sentence over one-line rows with a checkbox
  column: the resource over why it is proposed, what the repair does and how much changes, and
  *Can apply* or *Review only* with the reason behind an `ⓘ`; the excluded resources open from the
  title line. It was a framed notice over half-width cards of four lines each. The review shows
  each change as a diff of its before and after. The identity line is laid out as a project's
  header since 0.8.7, the verdict beside the name and *Inspect again* its command, and stands
  before the first inspection lands.

**The Network overview, Firewall and Gateway lost their tile rows in 0.8.7** (2026-11-14), in the
same run. Each was built in the current language and kept a row of tiles under its picture; each
figure went where it is said better, and each page opens on `HostIdentity` laid out as a project's
header.

- **The overview** (`app/(dashboard)/network/page.tsx`): whether the uplink is up is the line's
  state, *Run a check* its command (to Tools), and the refresh and an administrator's two other
  diagnostics its menu. *In* and *Out* are two `ReadingLine`s with their last fifteen minutes; the
  VPN and Refused tiles went, the topology's own nodes carrying them, and the connections went onto
  this server's node (`topology.tsx`). *Ways out* (`egress-identity.tsx`) is a line a family, a
  mark and a sentence ("Leaves ens3 via … from …, public") over what it establishes and whether
  the family is forwarded, the explanation behind an `ⓘ`; it was a framed table of two rows with a
  paragraph in a cell. The page is drawn before its read with the picture grey at the rows it drew
  last time and the line of facts at the height it took (`useViewState("network.overview.shape")`).
- **Firewall** (`network/firewall-panel.tsx`): the backend is drawn as the product it is in the
  identity tile and the picture's centre node (`firewall-words.ts`: ufw, firewalld, iptables and
  nftables each as netfilter, the filter it drives; firewalld's own icon is GPL artwork and is not
  carried). *Active* is the
  line's state; the switch and *Add rule* are at its end, *Reset* and *Refresh* in the menu. The
  seven tiles went: the rule count is the line's allow and deny, and what the log says the rules
  did is on the picture's nodes (`firewall-picture.tsx`, a `WireFlow`): what was refused and from
  how many addresses under *Everything else*, what a rate limit turned away under the one port it
  limits (with two limited ports, or none, it joins the default's line, since the log cannot say
  which port), each a press that narrows the log. **Defaults** are one `SettingSection` under the
  rules, a row a default with `Segments`, staged and saved by the page's one save bar; each
  default sends the request it always sent, and an inbound default that turns traffic away is
  still confirmed first with the server's preflight, the bar's Save waiting on the answer. A
  reader is shown the defaults in words. The table's footers are `ⓘ` notes on its titles; a
  recorded change is one line whose request opens in a popover, and a rule's history is a popover
  on its row where it was a sheet (`firewall-changes.tsx`). Controls on the server's destructive
  routes (the switch, a default, a removal, a plan, Reset) are drawn only for an account holding
  that capability. With no firewall on the host, ufw is installed from the page where the package
  manager carries it (`InstallHandoff`); it goes in switched off.
- **Gateway** (`app/(dashboard)/network/gateway/page.tsx`): the line's mark is the firewall the
  gateway writes into, its state whether the entries are in force, its facts the families the
  kernel forwards, the forwards and NAT entries in force, and firewall admission as a dot and a
  word; *Forward a port* is its command. The block of sentences headed *Policy evidence* that
  opened the page is behind that word as a popover (`PolicyEvidence`), with *Repair owned rules*;
  each forward and NAT row carries its own part as a state word in a column of its own
  (`gateway/evidence.ts`, unit-tested: the worst thing known, what is installed before what is
  modelled before what was measured), with that entry's evidence behind a press (`EntryEvidence`).
  The four tiles went to the line's facts and each section's own total at its head, and the
  paragraph about counters is the `ⓘ` beside those totals. An account that cannot make or move an
  entry is drawn no command and no switch.

Checked by `tests/browser/network-overview-firewall.spec.ts` at 1280 and 390, with the pages' older
specs; the pure readings are `firewall-words.test.js` and `gateway/evidence.test.js`.

**Egress groups became a list and a page in 0.8.7** (2026-11-13), in the run that brought the older
pages to the deploy pages' look. The page had never had a design pass: four grey tiles (groups,
carrying traffic, automated, members proven) over every group laid out in full, about a thousand
pixels each, under a row of six equal buttons and a switch. Where each went
(`components/network/egress/`):

- **The list** (`egress-list.tsx`) opens on `HostIdentity` laid out as a project's header: the four
  figures are its facts, whether any member is down is its state, **New group** its one command. A
  group is a `ChoiceRow`: its mark with the carrying state in the corner, its name over
  "sticky · 2 members", the carrying member in that state's colour, how it switches, and its verbs
  in the `⋯`. One height whatever the group holds; a running simulation replaces the automation
  word and lights the card's edge.
- **A group's page** (`egress-page.tsx`, `/network/egress/[name]`) is the process page's shape: the
  way back, the identity line with the carrying member as the state, **Run simulation** as the
  command and fail over, fail back, enable or disable, automation, edit and remove in the menu
  (`egress-verbs.tsx`, shared with the list's cards, each confirmation as it was), then the views
  as an underlined strip with `?tab=` in the address, and from `xl` the list as a rail with Alt+↑
  and Alt+↓. An account sees only the verbs it can use.
- **The picture** (`egress-picture.tsx`) is a `WireFlow`: the selected traffic, the group, its
  members by tier, each drawn as what carries it (a tunnel's product, else a router). Only the
  carrying member's wire moves, and only while the group is on; a member that is down is dashed,
  red where it is the one carrying. The group in the middle is its own mark on a logo tile, as the
  list and the rail draw it (it was the filled brand tile, the command's face, unlike every other
  picture's hub). Every node says the same number of lines, and the loading picture is `WireFlow`'s
  own with those lines given to it (`loadingLines`) and as many members as the group had on the
  last visit, so the views under it do not move when a group lands.
- **Members** stays a framed table, because its rows are read down their columns (state, round
  trip, loss, the strip of recent samples), one line a member. The second line of each row and the
  sixth column (path, tier, mark, table, probe source) were a member's fixed identity rather than
  readings: they are the **Bound to** list under it, with **How it decides** (probes, thresholds,
  holds, connections, protected addresses), which only the form showed before. What the rules would
  do now (`advice`) is a notice above the table when they would act.

**The VPN page is one kind of VPN at a time** since 2026-11-14: it opens on its own line (VPN, how
many tunnels are up and whether the server is on its tailnet, then each kind in a few words under its
mark), and under it WireGuard, Tailscale, Headscale (where this server runs one) and the log are views
named in the address (`?tab=`, `vpn/views.ts`), WireGuard first as it always was. Each view still opens
on its own identity line. The tailnet's view stays in the document once opened, so a draft of its
offers under the save bar outlives a look at a tunnel. A session opened through Just Hub is refused a
new peer, a change to one, a peer's configuration and restoring a tunnel
(`httpx.RefuseHubSession`); `components/network/hub-session.ts` finds that out from the account's own
list of sessions, and none of those controls is drawn for it.

**The VPN page lost its tiles on 2026-11-13**, in the run that brought the older pages to the deploy
pages' look. It opened on four figures (WireGuard 2/5, Tailnet 4/6, Exit, Moved), each said again a
screen lower, over a tunnel whose head was a row of small grey buttons, a box of alerts, a list of
family facts and a paragraph. Where each went (`components/network/vpn/`):

- **Each VPN opens on its own line** (`HostIdentity` in the project's layout, `tunnel-identity.tsx`):
  WireGuard's mark, the tunnel's name with who is online and how many things need a look, then its
  network, its port and what it has moved; **Add a device** is the one command and Join a site,
  History, Take down or Bring up, Create another tunnel and Remove are its menu. The tailnet's line is
  Tailscale's mark, this server's name there, the daemon's state in words (`tailnetState`: "Needs
  sign-in", never `NeedsLogin`) and its facts; while it waits to be signed in, the address it waits
  at is the line's command. A Headscale this server runs opens on a third. The "Moved" and "Exit"
  tiles are a fact on the tunnel's line and the hub of its picture.
- **The picture is straight under the line, on `WireFlow`** (`tunnel-picture.tsx`, 250 hand-laid
  lines before): devices, the tunnel with where its clients dial, sites. What the alert box listed is
  a mark on the peer it is about (an amber corner on its tile and the finding in a few words,
  `findingWords`), with the sentence in that peer's sheet. The VPN page draws six peers a side and
  sends the rest to the tunnel's page, the ones that need a look first (`drawnPeers`).
- **Internet exit is a row per address family** (`tunnel-exit.tsx`): the family's network, where
  its exit leaves, the connections it translated, whether its rules were verified, and the switch
  that turns that family's exit on or off. Two lines in every state. What the readings do not prove
  is behind the title's ⓘ.
- **A tunnel is a page** (`tunnel-page.tsx`, `/network/vpn/<name>`), on the process page's shape:
  the way back, the same line, Overview, Traffic and History as the underlined strip (`?tab=`), the
  other tunnels in a rail from `xl`, Alt+↑ and Alt+↓ carrying the view. It replaced a sheet opened
  by a "History" button. A removed tunnel keeps its page: its record outlives it, and **Restore** is
  that page's command while its file is in the archive.
- **Machines are cards of one height** (`tailnet-machines.tsx`): the system's mark, the name over
  its address in mono, how it is reached, what it moved and its state in columns that line up; under
  1180px the readings take a second line on every card alike. The list's count and its All, Online
  and Offline chips say what the "4 / 6" tile said. Headscale's nodes are the same card.
- **What this server offers the tailnet is a settings form** (`tailscale-offers.tsx`):
  `SettingSection`s in the fields' column, the exit node as `Segments`, the subnet routes as a list
  with a field under it, the helper sentences behind an ⓘ, and the page's one floating save bar. A
  save is worked out against the preferences read at that moment (`offerRequest`), so a retry after
  a lost answer sends only what still differs.
- **A verb the session would be refused is not drawn**: taking a tunnel down, removing it, removing a
  peer, forgetting its saved copy, clearing a budget and withdrawing an offer are destructive routes,
  and are shown to an account that holds `destructive`.

**Protection took the exit in 0.8.7** (2026-11-13). It opened on four tiles with no header over
five sections, the last of them fifteen kernel settings in two columns, and a `LoadingPanel` stood
where all of it would land. Where each figure went (`components/network/protection/`):

- **The identity line** (`HostIdentity` as a project's header): in force, not loaded or nothing in
  force as its state, a list whose fetch failed beside it in red, then how many lists and limits
  and what they dropped and refused since the table was loaded. New blocklist is its command; New
  limit, New exception and Refresh are its menu. The bytes and the totals since a date are each
  section's own fact on its title line.
- **A picture of what is refused and what gets through** (`picture.tsx`, on `WireFlow`): each
  blocklist as its flag or its kind with its networks and what it dropped, this server with whether
  its filter is loaded and how full the connection table is (the old tile's meter, in its level's
  colour), then each limited port as the product that listens there, the exceptions and the
  addresses never refused. A wire pulses when its counter grew since the last read and is dashed
  while its entry is off. It keeps one height: three lists and "N more", one or two limits, in a
  frame with room for four rows, so the lists under it never move. Its waiting state is
  `WireFlow`'s own `loading`, with both lines of the hub's caption named so a phone's rows of marks
  stand where the drawn picture's will.
- **Rows at one height.** A blocklist's coverage is on the line under its name; when it was fetched
  and when it next is, its networks and drops, and its one state (`listState`: a failed fetch in
  red before a stale copy before a verified set) are columns; the error's words, the enforcement
  evidence and the rest are the head of its editor (`ListStanding`). An exception's or a trusted
  address's standing is a state word in a column, and a row with no second line is as tall as one
  with. A row's other verbs are its `⋯`.
- **Views in the address.** `?tab=table` is the connection table for an administrator: a
  `BarReading` of what it tracks against its size, by state, drawn from the page's own reading
  before the breakdown lands, then the charts, the shares as `RankList`s and what they point at;
  its closing paragraph became the line under the band and an `InfoTip`. `?tab=kernel` is the
  kernel's protections as §7's settings column: `SettingSection`s of full-width rows (a switch for
  an off/on setting, `Segments`, a number field), a line under each control that says one thing at
  a time (the range, what is running under a staged value, kept for boot) so no state changes a
  row's height, and the product's one save bar, which asks for the confirmation that lists each
  change. The draft is the page's, so it outlives a look at another view and the tab counts it.
- **Its shape before its read.** The line, the tabs, the picture and each section's head stand
  where they will, with as many grey rows as each list held at the last visit (`useViewState`).
- **A reader is shown no control**: no command, switch, menu or field; a row says in force or off
  in words and still opens, to be read.

## Packet captures and Saved runs (§15)

**Packet captures and Saved runs took the exit on 2026-11-13.** Both opened on four grey tiles over a
list beside a key-and-value inspector. The tiles went where each figure is said better: on the
identity line (what is capturing or running as its state; how many are kept, the packet data in
bytes, the retention as its facts) and on the list's filter chips, which count the run states the
tiles counted. The inspector became a page for one item on the process page's shape
(`components/network/capture-page.tsx`, `run-page.tsx`, the rail in `item-rail.tsx`): a capture is
drawn as the interface it listens on, with its interface, filter and file as a `WireFlow` picture
and its seventeen facts on a second view; a run's result is drawn by the tool's own renderer, with
Compare, History and Details as views. A row's verbs are words behind its menu, its state stands in
one column wide and leads its second line on a phone, and every row is one height.
