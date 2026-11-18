# Features: Proxy and Security

The route areas of `/proxy` and `/security`: what each page is responsible for, then, under
**Implementation**, the files that own it and the specs that check it.

## `/proxy/*`

Proxy overview, Sites and site details, Certificates, Streams, Ports, Configuration, TLS tools and
Traffic show observed inventories and records. Certificates keeps its views in the address
(`?tab=`: certificates, renewal, DNS challenge, ACME accounts, local CA, signing requests, watched
domains), and a certificate is a page of its own, `/proxy/certificates/<name>` with `?path=` where
two share a name (`certificate-page.tsx`, its rail and picture; the shared console, forms and verbs
in `certificate-work.tsx`, the certificates layout); the sheet's old `?cert=<file>` lands there. Alerts (`/proxy/alerts`, administrators) is its own
page: what the rules watch wired to the channels told, what fires with its mute, the rules edited in
a sheet, and a test per channel (the parts that do not know the proxy's kinds are the alerts kit,
`components/alerts/kit.tsx`, `kinds.ts` and `alert-parts.tsx`, shared with `/metrics/alerts`); the overview's line says what
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
column, one save bar, a reload switch; Unknown hosts, the catch-all, is one of its sections), Lint and History as `?tab=` views; a file's revisions and
restore are the config editor's History view (`?view=history`, the old `?history=` link lands
there). Tunnel (`/proxy/tunnel`) opens on the tunnel's identity line with Check now, the picture of
hostname → Cloudflare's edge → connector → this server, and the hostnames as one-line cards; not
connected, the dashed picture, a choice of token or API and one form (administrators only).

Sites (`/proxy/sites`, 0.8.7) lists every site as a one-line row (`SiteRow` in `vhosts-panel.tsx`):
what it fronts with its engine in the tile's corner, its name over where its requests go, then its
hour in words (`HourWords`: requests an hour and the 5xx share), ports, TLS, certificate and state
in aligned columns, every verb in its menu. New site is the line's command, Import and Export all
are behind its menu, and under the list a line saying what an unknown host gets
(`UnknownHostsFact`, leading to Configuration → Settings, where the catch-all is a section saved
by the page's bar), then Password files (`auth-files-panel.tsx`), Access lists (`access-lists-panel.tsx`), the URL resolver
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
otherwise. On Settings the identity line does not offer Edit, as its command or in its menu: the
form is what is on screen. Checked by `tests/browser/proxy-site-page.spec.ts`, `proxy-site-builder.spec.ts`,
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
[`../backend/proxy.md`](../../backend/proxy.md#proxy)

Traffic (`/proxy/traffic`, every account) is the last hour across every site from
`GET /proxy/traffic`: requests a minute, the failing share, the slowest site's response time in
words and bytes sent, the hour by the minute, the sites busiest first (each opening its site page,
where its requests are read), and nginx's error log grouped from `GET /proxy/errors` over a window,
for every site or one (`?tab=errors&site=`). The old one-site address `?site=<name>` redirects to
`/proxy/sites/<name>`. Checked by `tests/browser/proxy-traffic.spec.ts`; the logic is
`components/proxy/site-traffic.ts` (unit-tested)

## `/security/*`

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
[`../backend/observability-security.md`](../../backend/observability-security.md)

## Security and proxy panels

**Security and proxy** (`components/security/`, `components/proxy/`) follow the same rule — teaching next
to the control, not in a banner above it. Security pages open their owning controls directly;
SSH fixes focus their directive and retain the full access preview, confirmation and recovery flow.
Firewall findings open enforcement review, and allowlist findings open Access in configuration.
`rule-form.tsx` is why the catalogue lives on
the server: picking "Redis" fills in 6379 *and* raises the warning at the moment of choosing, not in a
report afterwards; the `ufw` line it would run is in the footer. `ssh-panel.tsx` stages every change and
applies them together, and its dialog says to keep the session open and check a second terminal — the one
piece of advice no error message can give afterwards. `site-form.tsx` shows the server-rendered nginx as
the form's last part (same renderer that writes the file, so the form is not a black box), with the DNS checks
(`DomainChecks`) under the domain field because "the name does not point here yet" causes most certificate failures and
certbot reports it as "challenge failed". `tls-report.tsx` says out loud what `unknown` means for a
protocol row.

The security section has four reading pages (§15–16): the overview, SSH, Intrusion and Logins.
The firewall, connections, interfaces and tools moved to the Network section in 0.7.1 and are
described with it below; the overview's Firewall and Connections nodes open them there. The overview
opens on the observed access identity line and then the ways onto the machine as a
wiring picture whose nodes carry the area readings (`perimeter.tsx`: internet through firewall, fail2ban and sshd, and this browser
through the allowlist). The **Access boundary** lists Caddy-only ingress, allowlist, tailnet, SSH
tunnels and preview boundaries as held, broken or unknown. Automatic posture findings and their
strip were removed in 0.7.5.

- **SSH:** the page opens on sshd's doors as a picture (`ssh-picture.tsx`) — the port, public keys
  with the keyed accounts' faces, passwords and root — drawn from the draft, so a staged change
  shows its effect before it is applied. The auth log's four readings are `ReadingLine`s
  under it, each a press that narrows the log. Authentication, access, session limits and other
  directives are one centred column of `SettingSection`s (0.8.7): a row is a directive's name with
  why behind its ⓘ and its control at the far end (a switch, `Segments`, a list or a field), marked
  while it holds a staged change with *sshd runs …* under the control, and the product's floating
  save bar names what changed. Reverting a draft to its effective value removes it from the change
  set. **Save** opens the review, which lists each old and new value; the backend tests and reloads
  through its existing job. `?setting=<key>` lands on a directive. For an
  administrator the page ends on the Auth log (`auth.log` or `secure`, else the journal's sshd, sudo,
  su and logind lines); anyone else is told the page needs an administrator and no log is asked for.
  The **Jump host** section reads the five forwarding directives the closed list gained in 0.7.1
  (`AllowTcpForwarding`, `GatewayPorts`, `AllowAgentForwarding`, `PermitTunnel`, `MaxSessions`), stages
  a safe bastion profile into the same draft, and writes a ProxyJump snippet for the hosts behind this
  server (`bastion.tsx`). Where the host can run the independent recovery watchdog, the review
  carries a "Restore unless confirmed within 90 seconds" switch, on by default: the apply is sent pending, and
  the global network confirmation notice, worded for SSH, keeps it only after this session verifies
  a fresh response. A staged change is judged against the access boundary as it is made
  (`useBoundaryCheck`); the review shows what it does to tunnels, holds its button while a change
  would cut the way in, and confirming it acknowledges it.
- **Intrusion** (0.8.7) opens on an identity line whose state is the worst thing true of any engine
  and whose command bans an address through either engine (`ban-dialog.tsx`), then a picture of the
  internet, this server and the three engines (`intrusion-picture.tsx`). One engine is on screen at a
  time as a view in the address (`?tab=fail2ban`, `crowdsec`, `suricata`; the first installed engine
  without one), and `?tab=blocked` is every refused address once, with each engine holding it and the
  broader blocks it already sits inside (`blocks-panel.tsx`). fail2ban's view is its readings as
  lines, the jails as one-line cards that carry the watched service, current state and counts, repeat
  offenders as a `RankList`, and Activity: fail2ban's own log, every strike as well as every ban;
  where fail2ban writes only to its journal, the offenders' fold says Activity reads it instead
  rather than "nothing has been banned". A jail is a page (`/security/intrusion/jails/[jail]`,
  `jail-page.tsx`, the other jails as a rail from `xl`): what it holds with Unban and Never ban again
  on each address, its **Policy** (the rule as a sentence, what it reads, what each action does with
  a ban, whether the sshd ban covers sshd's actual port, and values a restart would change), and Ban
  an address, Tune and Release every ban as its verbs. The tuning form is a dialog: it shows the
  subject, groups the three policy numbers, and offers the browser's address for the allowlist; draft
  values use fail2ban's lowercase parameter names while the saved configuration uses camel case.
  CrowdSec and Suricata each open on their install where they are missing (`components/install-handoff.tsx`):
  CrowdSec's decisions with release and a ban form that refuses the reader's own address, its alerts
  and bouncers (`crowdsec-panel.tsx`); Suricata's mode, rules and alerts by severity, read from
  `eve.json` through the log roots (`suricata-panel.tsx`). Both ban forms say which engine already
  holds the typed address (`blocks.ts`) and what the ban does to the access boundary, and send the
  acknowledgement only after showing it. CrowdSec's head, readings and notice say the server's
  enforcement verdict (`enforcement.ts`) rather than whether the engine runs, with each bouncer's
  pull age and the kernel sets under the bouncers. Suricata adds **Setup** (`suricata-setup.tsx`:
  capture interface with a tested move, whether the capture sees packets, rules with an update job,
  start) and the read-only **Inline queue**, which says whether each queue rule fails open.
- **Each area reads its own log in place.** SSH, Firewall and Intrusion read the file an operator
  would open first, each asked after with `GET /logs/source` rather than out of the whole log index,
  and fall back to the journal's reading of the same program, saying in the pane's facts whether the
  file is missing or outside `JD_LOG_ROOTS` (`host-logs.ts`, `log-section.tsx`). The day's counts
  from the log are reading lines on SSH and Intrusion and stand on the nodes of Firewall's picture,
  each a press that narrows the log under it.
  The client of a line takes the same address verbs as a peer anywhere else in the section, but the
  block only where the line is an attack a deny answers — a failed or invalid login, a strike or a
  ban, a rate-limited packet — never an accepted login, an `ignoreip` match or an allowed packet,
  which can be the operator's own; an outbound packet's address is this host's, and gets no verbs.
- **Logins** (0.8.7) opens on this server's identity line and a picture of where sessions are held
  from, who is attacking and which accounts are in (`logins-picture.tsx`); then the open sessions as
  one-line cards, attackers as a `RankList` and the login record as the page's one framed table.
  Addresses use `PeerIdentity` or the inline
  `Address`; address block and lookup actions remain in `address-verbs.tsx`.
