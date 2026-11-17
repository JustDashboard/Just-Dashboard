# Features: Network

The route areas of `/network`: what each page is responsible for, then, under **Implementation**,
the files that own it and the specs that check it.

## `/network/*`

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
command, then views in the address (`?tab=`) directly under the line: Members, which opens on a
`WireFlow` picture of the selected traffic, the group and its members (the carrying wire moves, a
failed member is dashed), then its measuring cadence as a line and the members' table (state, round trip,
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
SQM. It opens on its identity line, titled Traffic, with the uplink as a fact (what it carries in words, the newest reading's age as
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
`ServiceLogs` pane ([`docker-files-logs.md`](../../backend/docker-files-logs.md)). The [complete
inventory](../../../audits/2026-10-08-network-audit/README.md) distinguishes UI, API and inspection
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
  kernel observer keeps its own reviewed activation. Directly under the line it is three views in the
  address (`?tab=`, `flows/views.ts`), and the picture opens each: the day's socket hours with its
  recorded hours (the default), the kernel observer, and the latest sample with the measurement
  boundaries. A node of the picture opens the view it stands for.

Every reading these pages gave as "Unknown" is still given as "Unknown": a check that expired or was
cancelled measured nothing, a UDP row has no byte counters, and an hour with no block was not sampled.

**The overview, Firewall, Gateway and Drift open on a project's line (0.8.7).** None keeps a row of
tiles. The overview's line says whether the uplink is up and has *Run a check* as its command; In
and Out are two reading lines, the connections are on this server's node, and Ways out is a line a
family. Firewall has no way back, so its shortcuts' `?` closes its identity line after the `⋯`
rather than taking a row above it, and its header starts where its siblings' do. It draws its
backend as netfilter, the filter it drives, says what its log counted on the picture's nodes (each
a press that narrows the log), keeps its defaults as one settings section under the rules saved by
the page's save bar with the inbound confirmation and preflight unchanged,
opens a recorded change's request and a rule's history as popovers, and offers ufw's install where
there is no firewall. Gateway's line carries the forwarding families, the forwards and NAT in force
and firewall admission as a word with the policy evidence behind it; each row carries its own state
word. Drift's boot unit is one line with its activation behind a press, and its repair plan is
one-line rows with a checkbox column. Controls on destructive routes are drawn only for an account
with that capability, and a reader is drawn none it cannot use. Checked by
`tests/browser/network-overview-firewall.spec.ts`.

**Implementation:** network pages, `components/network/`, shared security firewall/connection
components; checked by `network-*.spec.ts`, `security-ui.spec.ts` and `design-system.spec.ts`

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
(`?tab=`): Overview (owner and transport as lines, each reading's basis behind a tip; listeners;
upstreams; limitations), Clients (access, client
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
`client_groups`, validated by `lib/network/dns-service-policy.ts` and the retained DTO parser.
Authoritative zone reads show unsupported RR types without offering edits. Empty group membership
is an explicit request. Drafts survive field/native refusals; error summaries focus and link to the
associated controls. Retained review polls `/changes/{id}/current` for the selected native policy,
holding stale or changed confirmations and showing human record/client details before and after.
The [record/client acceptance](../../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-service-policy-ui-acceptance.md)
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
[assembled custom-domain acceptance](../../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-service-domain-filter-ui-acceptance.md)
records the fresh production build, all 24 interactions, current-read holds, twelve captures and
separate exact-source AdGuard/Pi-hole configuration/restart proofs. Broader P17 acceptance remains open.
`service-filters.tsx` adds a separate native filter reading on read-only or managed connections:
redacted subscription origins, IDs, counts/update metadata, custom-rule fingerprints and explicit
native group memberships. Configured inventory, native status, unavailable/manual/app scope and
unmeasured client decisions stay separate. Failed reads retain dated same-owner data; changed
connection identity clears it. Unknown sections carry no zero-policy count or healthy verdict.
The [filter UI acceptance](../../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-service-filter-ui-acceptance.md)
keeps its production build, strict decoder, browser captures and actual native metadata proofs
separately attributed.
Its [source-matched build, 13 interaction cases and phone/desktop inspection](../../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-services-ui-acceptance.md)
pass, as do the [combined reachable checks](../../../audits/2026-10-08-network-capability-report/implementation-evidence/native-dns-integrated-acceptance.md).
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
documented in [the backend contract](../../backend/network-native-managers.md).

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

## Network panels

**The Network section** (`app/(dashboard)/network/`, `components/network/`) is eighteen reading pages
over the network module; [``](README.md) lists each and
[`design-system.md`](../design-system.md) §15 records how they are drawn. Its layout renders the same
`SecurityState` the Security section does, so every page knows the reader's address and observed
service state. The pages that moved keep their behaviour:

- **Firewall** (`/network/firewall`): the page opens on its inbound path as a picture (`firewall-picture.tsx`, reading the
  rules through `firewall-reading.ts`): each port the rules admit, drawn as the product that answers
  there with who may reach it, and the default for everything else. The firewall turns on and off
  from the line's `⋯` menu (Enable or Disable), and the shortcuts' `?` is on the row above the
  line. The rules are the working column, and the defaults are one settings section under them at
  the page's width, staged and saved by the page's save bar (0.8.7). Each row carries its
  action in a `--tag-*` hue down its edge and groups its destination service, port and comment;
  actions stay visible, and the table ends on the default as a row of its own. The rule dialog groups policy, destination and source, using source choice cards
  (Tailscale's own mark for a tailnet source) and service marks where the port identifies a product.
  Address-only deny/reject rules can be edited without inventing a destination port. Existing
  ordinary confirmations for toggle, reset and inbound-deny policy remain. The page ends on the Firewall
  log (`ufw.log`, else `kern.log`, else the kernel ring, read as the firewall lens); while ufw or
  firewalld says logging is off, the section says so and its button brings the logging default
  under the rules into view instead of drawing an empty pane.
- **Connections** (`/network/connections`): opens on its identity line — the sockets, the remote
  addresses and how many are from the internet — and a picture of who is connected
  (`connections/picture.tsx`, on `WireFlow`: the callers by network, this server with what listens and
  what never left the machine, the programs they reached), which opens the Live view. "Live" and
  "A past hour" are a `ViewTabs` strip directly under the line (`?when=recorded`, `?hour=`). The
  three places are always drawn, a quiet
  one with a still dashed wire; each node carries every read since the page opened as its trend;
  "Everything" and "From the internet" are filter chips with counts beside the search, and a
  program narrows the list to its callers. Four
  tiles stood over the picture until 0.8.7, every figure of theirs already on the page. Under it each
  address is a one-line card of one height (`connections/rows.tsx`): its network's mark, the address
  over the network, the program it reached as that product with its ports, sockets and active ones as
  figures in columns of their own, and its verbs — the block among them — in the `⋯` menu. The footer
  is a count of the addresses shown, and what the read could not see (time since the last read,
  closes noticed, unconnected UDP) is behind its tip, "What this read cannot see". The past hour reads the
  socket history for a chosen UTC hour in the same cards (`connections/recorded.tsx`), and with the
  history off it offers the page that turns it on.
  **An address is a page of its own** (`/network/connections/[address]`, `connections/address-page.tsx`),
  in place of the peer sheet, on the process page's shape: a way back, the identity line with the block
  (or lifting it) as its one command and the lookups in the menu, "Live" and "A past hour" as an
  underlined strip (`?tab=past`, `?hour=`), and from `xl` a rail of the live list with the list's own
  search and chip (`connections/address-rail.tsx`; Alt+↑ and Alt+↓ walk it and carry the open view).
  `?peer=<address>` on the list lands on it. The live view (`connections/address-views.tsx`) is a
  picture of the address, this server's ports and the programs behind them — what the summary folded:
  transports, far-end ports, socket states — then, for an administrator (`GET /connections/{address}`),
  every live tuple as one line with how long it has been seen, TCP's round trip, its state and its
  bytes, the tuples seen closing in the last fifteen minutes, and the layers it crosses as cards that
  open the page that owns each: the firewall rules naming it or its network (rules for any source are
  counted, not listed) and its block, the kernel route that answers it, and the socket history's last
  day. A role without the detail's capability reads the picture alone. A long IPv6 address keeps both
  of its ends in a phone's card and in the rail (`shortAddress`), and is whole on its page.
  "Block at the firewall" opens a dialog asking why, for how long (an hour to thirty days, or until
  lifted) and which incident — an existing saved run, or a new one that starts by recording who owns
  the address (`connections/block-dialog.tsx`); it is never offered for an address on the tailnet, a
  local network or this machine. The blocks made here are listed under the list, two lines each, with
  their end, their incident and whether the rule is still in the firewall; each opens its address, and
  an administrator with the destructive capability lifts one after confirmation
  (`connections/blocks-panel.tsx`).
- **Tools** (`/network/tools`): 26 probes, the connection path investigator and the browser-only
  IPv4/IPv6 subnet calculator occupy one workbench held to the window (`<Page fill>`), drawn as the Git
  workspace is: a strip across the top of the frame (the server's mark, "sent from this server", Saved
  runs as a quiet link, Every tool), a searchable rail and the chosen tool's pane, each scrolling
  inside the frame. Every diagnostic has a mark, its glyph on its group's hue (`tools/tool-marks.tsx`:
  the glyphs the DNS, Certificates, SSH and Ports pages already wear). A tool with nothing run opens on
  its mark, what it does in one sentence and an example target as a button, which fills the field and
  sends nothing (`tools/tool-frame.tsx`); the sentence that stood under every tool's title is that
  empty pane and the title's tip. Below `md` the rail and the pane are one full-height pane at a time
  with a bar at the foot (`phone-panes.tsx`), opening on the tool. Other tools stay mounted while
  hidden, preserving drafts, results and in-flight requests when switching, but only the tool in view
  has its form fields in the page. A new query-string arrival reseeds only the requested tool. Arriving
  with `?tool=asn&target=…` never runs it; sending traffic remains an explicit Run action.
- **Connection path** is the rail's `path` tool (`tools/path-tool.tsx`), among the reachability tools
  after Route lookup. It was a page of its own: `/network/investigate` now redirects to
  `/network/tools?tool=path` with every parameter it carried (`target`, `port`, `protocol`, `family`,
  `measure`, `container`; `tools/tool-links.ts`), and the Network rail no longer lists it. Its report
  (`network/path-report.tsx`, also drawn by Saved runs and a published port's trace) is a chain of
  wired hops, one per layer, each mark in the colour of what the layer rests on (a snapshot, the
  model's prediction, a measurement; no colour for unknown, and none of them success's green), with
  the wire dashed where either end is unknown. The four counts that were a tile row are the key to
  those colours.
- The tools now include route/path-MTU checks, host prerequisites, bounded packet summaries and
  local Wake-on-LAN. All server probes remain administrator-only; decoded packet fields may be
  sensitive. Network forms expose IPv6 GRE/bridge membership, preferred route source, rule outgoing
  interface/notes and DNS fallback/cache settings. The [complete networking audit](../../../audits/2026-10-08-network-audit/README.md)
  records API-only controls, validation, permission handling and provider/hardware boundaries.
- The interface list that was `/security/network` is the Interfaces page, rebuilt over
  `GET /network/links`; `/security/network` redirects there.

Capability-gated controls, unavailable modules, backend paths and audit/confirmation boundaries
retain their contracts. Tables keep their frames because they own scroll regions; plain findings
and forms do not. Phone tables scroll inside those frames without expanding the page.

`tests/browser/security-ui.spec.ts` checks the Security pages and the three that moved, their mutations and lookup handoffs,
probe draft/request preservation, jail policy edits, SSH draft reversion, source-only rules,
ordinary confirmations, limited roles and unavailable modules, and each area's log — its lens, its
readings, its fallbacks and which lines offer a block — against the lines the Go lenses read
(`host-logs-fixture.ts`). It checks the viewport at 390, 1280 and 1720, and requires desktop tables to
fit their action columns. `JD_SECURITY_SHOTS` writes
review screenshots at those three widths, including scrolled content and rule/jail dialogs.
The changed-file gate also runs the design-system checks. `tests/browser/network-ui.spec.ts` (with
`network-fixture.ts`) checks every Network page's structure — no unnamed control, no pill, no frame
that is not a table, no sideways scroll on a phone — the redirects, the overview's picture and
findings, a device page's guards, the create forms' bodies, the routing picture and a peer's QR
code; `JD_NETWORK_SHOTS` writes review screenshots at 390 and 1440. `network-device-page.spec.ts`
holds the Interfaces list and a device's page to what their overhaul promised, at 1280 and 390: one
height for every card and row, the opening line in one place loading and loaded, the old `?device=`
address landing on the page, every verb's request, the rail, and nothing a read-only account cannot
use.
