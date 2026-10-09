# Observability, host security, and packages

## Metrics, saturation, health

The frontend can pin a shared chart instant, inspect adjacent samples and link to the surrounding
journal History window. Audit/Security filters and package inspectors also preserve URL questions,
while counted incoming audit, connection and login rows wait for explicit reveal. These client
interactions use the existing reads described here; see
[workspace interactions](../frontend/workspace-interactions.md).

`internal/metrics` samples on the server's own timer into SQLite: a live socket only describes the time
since a tab was opened, and charts that start empty every visit cannot show last night's spike.

- `GET /system/metrics/history` buckets **in SQL**, and every series carries its bucket's **peak** beside
  its mean — a 100% second inside a ten-minute bucket averages away to nothing.
- Capacity is **per filesystem** (`metric_mount_samples`, `GET /system/metrics/storage`), never a
  worst-of line: when the fullest mount stops being the fullest, one line drops to the runner-up and
  reads as freed space on a disk that never changed. Pseudo filesystems are filtered before the write.
- Containers are sampled into `metric_container_samples`, keyed by **name, not id** — a compose redeploy
  replaces the container and seeing across the restart is the point. Docker being absent is logged once,
  not an error. `/docker/containers/stats/history` serves a sparkline per table row in one query.
- The sample carries `size_rw`, the writable layer, taken from the **cached** disk walk rather than by
  asking the daemon for sizes per container: a sampler running every fifteen seconds shares the bounded
  disk cache and its refresh instead of starting a separate walk per sample. It is what makes
  "grew 6.4 GB today" a measurement — a static 38.7 GB cannot tell a container that has held it for six
  months from one whose disk has two days left. Zero means the walk
  was unavailable when the sample was taken, and `dockerx.DetectAnomalies` treats that as an absent
  measurement rather than as an empty layer.
- Samples also retain the observed container ID for release attribution. The additive `container_id`
  column defaults to empty for old rows, which stay visible in name-continuous charts but cannot be
  attributed to a release. `ContainerIdentityRange` reads 1..64 distinct IDs in one aggregation with
  CPU/memory means, peaks and sample counts; its half-open window is limited to 24 hours and requested
  resolution to 600 buckets per container. Empty series remain empty, and retention-disabled reads
  return no samples. An `(container_id, ts)` index is installed after the column migration.
- Container network/block totals are stored as Docker's **cumulative counters** at Docker's sample
  timestamp (`sample_time` retains fractional seconds; legacy rows use their integer timestamp).
  `container_usage.go` differences consecutive samples **before** bucketing, with a bounded
  look-behind for the first visible sample. Means are weighted by measured elapsed time; peaks are the
  highest measured interval rate. Single-sample buckets retain the interval from their predecessor.
  Missing counters, identity changes, backwards CPU/I/O counters and gaps over three sample intervals
  return null rates, not zeroes or spikes. Additive nullable availability/limit/counter columns leave old
  rows unknown; positive legacy counters can still establish availability. Only explicitly configured
  memory limits become historical limit lines, rather than Docker's default host RAM ceiling.
- `cpu_percent` is `NOT NULL`, so a sample with no interval to measure is stored as 0. `ContainerRange`
  treats those as absent — the first sample after a container starts (no predecessor under its id), a
  counter reset, or a gap — and `cpu`/`cpuPeak` are `null` for a bucket with no measured interval, so a
  deploy does not draw an idle dip. Rows from before `container_id` existed keep their stored value. A
  recorder restart under an unchanged container id is not distinguishable and still stores a 0.
  Each point also carries `pidsPeak` (the bucket's maximum) beside the mean `pids`.
- The recorder keeps its **own** `sysinfo.Collector` and `dockerx.StatsSampler`: rates are deltas, and
  sharing with request handlers would let a one-shot `GET /system/metrics` shorten the next interval.

**Saturation series** answer "is work waiting", which utilisation cannot: CPU **by mode** including
`steal` (on a VPS the one whose fix is outside the machine); PSI pressure (`Supported: false` renders as
"cannot tell", never three reassuring zeroes); disk IOPS/service time/%util as the **worst device** (a
disk saturated by small random writes moves almost no bytes); socket totals from `/proc/net/sockstat`
(enumerating connections is thousands of lines a sample); `load.Misc.Blocked`; and inodes per mount,
where a build server hits the ceiling first on a filesystem every capacity chart calls half empty.

Two **live-only hardware readings** ride on the snapshot and are never recorded: `files` is
`/proc/sys/fs/file-nr` (open handles against the kernel's ceiling, with `max` reported as 0 where a
container runtime hands out the 64-bit maximum, so nothing divides by it), and `sensors` is every hwmon
or thermal-zone temperature gopsutil can read, hottest first, each with the driver's own high and critical
marks. A VPS usually reports no sensors, and the UI shows none rather than a cold machine.

Each of the snapshot's `net` rows carries a `kind`
(`netsec.ClassifyInterface`: physical, tunnel, bridge, virtual) and they arrive in that order, so the
metrics page can open on the host's own devices and set Docker's veth pairs and bridges aside — a host
running a dozen containers otherwise lists thirty interfaces with the uplink among them.

`metrics.Assess` (`GET /system/health`) turns those into findings — measured / means / do — ranked
worst-first. It runs on the server because the thresholds are a claim the product makes, and because
the CPU steal check uses an hour of recorded history where available. **A finding is only raised for
a condition that is costing the machine something now**; occupancy that costs nothing is a reading,
and a list padded with readings teaches the operator to ignore it. So:

- **Pressure is judged on the minute and five-minute averages together** (`sysinfo.ReadPressure`
  parses `avg10`, `avg60` and `avg300`; the recorder keeps sampling `avg10`). CPU warns at 25% of the
  minute with 15% of the five, and is critical at 60/40; memory warns at 10/5 and is critical at 40%
  of the minute or 10% *full* (every task stalled at once); storage warns at 30/20 and is critical at
  25% full with 25% of the five. A ten-second spike — a build finishing — is not a finding.
- **Load and I/O wait stand in only where PSI is missing.** Load counts tasks blocked on a disk as
  well as tasks waiting for a core, so beside pressure readings it told one problem twice.
- **Memory is judged on available, never on "used"**: Linux counts page cache there. Tight memory
  (≤10% available) escalates to critical when swap is also ≥90% full, because there is no headroom
  left anywhere. **Swap occupancy alone is never a finding**: full swap beside free memory is idle
  pages. Only on a kernel without PSI does swap ≥80% beside ≤20% available stand in for thrashing.
- **Packet loss is judged over a window, never on since-boot counters.** The recorder keeps each
  physical and tunnel interface's counters in memory between checks; a window closes once its
  baseline is a minute old, its verdict stands until the next one closes, counters that go backwards
  or a baseline older than ten minutes start a fresh window, and the first check after a start has
  none ("measuring"). A physical link warns at ≥100 drops that are ≥1% of the window's packets, and
  at ≥10 errors (`neterr:<iface>`); a tunnel is a notice at ≥500 drops and ≥5%. Docker's veths and
  bridges are not judged — the host's own links carry the same traffic.
- File handles are judged only against a real ceiling (80% of `max`), and a sensor only against its
  own thresholds — critical past `critical`, warning past `high`, nothing where the driver reports
  neither.

Each finding carries its `area` (cpu, memory, storage, network, services, containers, hardware), its
`evidence` — the two to four measurements behind the verdict, already worded — and, where a remedy
acts on named things, its `subjects`: the mount, the interface, each failed unit or each container.
The response's `areas` is one verdict per area, always all seven in that order, each the worst
finding in it or `ok`, with a few words of current reading ("23% stalled", "6.5 GB free", "/ at 78%");
an area whose evidence could not be read is `unknown` rather than passing. `Health.Settle` derives the
area statuses and the host verdict from the findings, so the badge, the coverage and the list cannot
disagree.

The handler also reads service and container runtime concurrently with a four-second bound. Failed
units become one `systemd.failed` finding whose subjects carry each unit's description, its last
`Result` (`exit-code`, `oom-kill`, `start-limit-hit`…) and when it failed — `Systemd.DescribeFailed`
reads those in one `systemctl show` over the failed units, which `List` leaves empty for speed.
Unhealthy, dead, restarting and paused containers are grouped by state with each container's id as a
subject. Exited one-shot containers are not assumed failed. Overview, Metrics and the top bar use this
same response; the browser no longer folds its own additional verdict. Missing managers, unread
container checks and missing PSI appear in `silences`; a virtual machine's missing temperature sensors
do not — there is nothing to read, so the hardware area says "no sensors" instead. A clean partial
assessment names its missing evidence instead of claiming every check passed. Initial and subsequent
failed Health reads stay visible on Overview and Metrics; Try again refreshes the mounted Health
consumers.

`HealthPanel` draws the response in three layers on Overview and Metrics alike. The `areas` are a
strip of the release path's segments — green, amber, red, dashed where unread, sweeping while a
manual check is in flight — each with its summary, so a clean host reads as checked rather than empty.
Critical and warning findings are lit cards on their level's wash carrying the figure they were judged
on against the line it crossed and how long the condition has held; notices fold under one line.
Each card opens the [local server advisor](server-advisor.md): the diagnosis with the server's
`evidence`, then the fix. Storage investigation is requested only when opened, and copy hashing is an
explicit extra read. CPU, memory, swap, disk I/O and handle findings open grouped process attribution
with group controls; failed services and containers are fixed in the sheet from their `subjects`;
network findings distinguish observed deltas from since-boot counters; steal and sensor findings
state the provider or hardware remedy. Successful controls dispatch `jd:health-changed` so all mounted
health polls refresh, and a finding fixed from the sheet that is gone from the next verdict stays on
the list as *Resolved* for a minute and a half.

`metrics.Events` (`GET /system/metrics/events`) is the annotation layer, answered from `deploy_runs`,
`backup_runs` and `audit_log` — this dashboard *is* the thing that ran the deploy. Reboots need no
storage: a sample whose `uptime_seconds` dropped means the machine went down, which also catches
restarts nobody initiated here. Works with `JD_METRICS_RETENTION=0`; only reboot markers go quiet.

## netsec: exposure, posture, login history

`netsec` reads records the host already keeps rather than polling: wtmp (`GET /logins`), btmp
(`/logins/failed`, behind `system.admin` — it holds whatever was typed at a login prompt, sometimes a
password in the username field) and fail2ban's log (`/fail2ban/history`). Polling a jail would invent
events between samples and miss every ban shorter than the interval. `GET /logins/attackers` (admin,
for the same reason as the listing) folds the same btmp sample by address — `SummariseFailedLogins`
is a pure function over `LoginRecord`s: attempts, the account names tried most, first and last seen,
most persistent first, with `Capped` reported by the arithmetic `countWithin` uses so a sample that
ran out inside the window is a floor rather than a total. A console attempt has no address; it is
counted and not listed.

`netsec.Exposure` grades who can reach this panel (`tailscale`, `tunnel`, `private`, `public`, `open`)
from the allowlist and the host's interfaces. The setting lives in an env file nobody re-reads after
install day, which is exactly why it belongs on screen. The handler adds `Client`, the address the
request arrived from — `DescribeExposure` stays pure — because every lockout guard on the Security
pages compares against that address and the pages should say what it is; the jail tuning offers it to
fail2ban's allowlist by name. The firewall-rule and ban guards are handed `networkClient(r)` instead,
which for a request on loopback is the address of the SSH session carrying the tunnel
([`OperatorAddress`](network.md#three-rules)): a rule that refuses that address cuts the tunnel, and
the browser with it.

`BanHistory` reads at most the last `banLogTailBytes` (8 MB) of each fail2ban log and drops the line
the seek tears in half. fail2ban writes a "Found" line per failed attempt, so a host under a campaign
has a log of hundreds of megabytes, and the repeat offenders (`/fail2ban/offenders`) and the posture
check each read it every minute or two; the tail holds the events every reader wants. The Intrusion
page no longer calls `/fail2ban/history`: its Activity section reads fail2ban's log itself, through
the `fail2ban` lens on the `/logs` routes, and — unlike the ban table it replaced — a host where
fail2ban writes only to the journal is read from `journal:fail2ban.service` rather than said to have
nothing.

**Each area's own log is read on its page, and login records stay the administrator's.** SSH reads
`auth.log`, else `secure`, else the journal's `sshd`, `sshd-session`, `sshd-auth`, `sudo`, `su` and
`systemd-logind` lines (`journal-id:`), through the `auth` lens; Firewall reads `ufw.log`, else
`kern.log`, else the kernel ring (`kernel:`), forced through the `firewall` lens since kern.log is
mostly other things; Intrusion reads `fail2ban.log`, else its unit's journal. Each page asks after its
own files with `GET /logs/source` rather than the whole log index, and names in the pane why it fell
back — the file is missing (404) or outside `JD_LOG_ROOTS` (400). The auth lens leaves a failed or
invalid login at info — its tone in the UI carries it, and a public SSH log must not be a sea of amber —
and raises only a failed `sudo` or `su` and sshd's too-many-attempts to warn. The same reasoning that
puts `/logins/failed` behind `system.admin` applies to these lines, and the log routes enforce it on the
source rather than on the page: auth.log, secure, their numbered generations and anything resolving to
them — including a link among another file's generations, and a PM2 process's out or error file, which
its owner names — the `ssh`/`sshd` units, and a `journal-id:` naming any of those programs or `login`
are refused to anyone else on every `/logs` route and left out of `/logs/sources`
([Logs](docker-files-logs.md#logs)).
The SSH page makes no log request for a non-administrator. **The whole journal (`journal:`) is still
readable at `read`, as it was before the gate** — narrowing it to sshd is what is gated — so a determined
reader without the capability can find those lines in the unfiltered journal; that is a known gap, not
the boundary.

`netsec.Assess` (`GET /security/posture`) is to security what `metrics.Assess` is to load: every panel
in this class shows facts and leaves the reading to somebody who already knows how; the ones that take a
position sell a score out of a hundred, which is a number to optimise rather than a thing to fix.

- A `SecurityFinding` carries measured / means / do as three fields, plus a `Fix` naming a remedy the
  dashboard can perform.
- `Assess` is a **pure function of its inputs** (the handler gathers exposure, firewall, fail2ban, sshd,
  listeners, certificates, failed logins and updates concurrently), which is what makes it testable with
  no firewall or network. `posture_test.go` pins each claim, including the two easy to get backwards: an
  exposed database behind a default-deny firewall is a **warning**, not a critical (otherwise it cries
  wolf), and "turn off password authentication" stops being offered when no account has a key — there it
  is a lockout, not advice.
- `ExposedPort` and `CertSummary` are declared *in* netsec rather than imported from `proxysvc`, so the
  audit has no dependency on how ports or certificates are discovered.
- A database or control port is judged by the **address it is bound to and the interface that address
  is on** (`reach.go`), since `ExposedPort.Exposed` is any bind but loopback. `ReadHostNetwork` places
  each address on its interface (Go's interface list) and marks the interfaces carrying a default route
  (`/proc/net/{route,ipv6_route}`, the namespace the sockets are read in). A bridge is told by the
  kernel's link table (an `RTM_GETLINK` dump: each device's kind and master, `classifyLinks`), not by
  its name: a Linux bridge whose every port is a veth, tap or dummy is `bridge` whatever it is called
  (Docker, libvirt, LXD's `lxdbr0`, Incus's `incusbr0`, Podman's `podman1`, a Proxmox internal
  `vmbr1`), and one that enslaves a NIC, bond, VLAN or VXLAN is that network and `physical`, as is a
  device named like a bridge that the kernel does not call one (Open vSwitch). `NetworkInfo` classifies
  the Network page's devices the same way; names (`classifyInterface`) are only the fallback when the
  table cannot be read. `HostNetwork.Reach` grades a bind: `all` and `public` are critical; `network` (a
  tailnet — Tailscale's ranges or a `tailscale*` tunnel —, another tunnel, a link-local address on the
  uplink, or a private address on a physical or default-route interface) and `host` (an address,
  link-local included, on such a bridge or a veth that carries no default route) are warnings. Only the private address
  on the uplink, or one on no interface the host listed, gets the clause that a provider mapping a public
  address onto it makes it the internet's too; a bridge or link-local address cannot be mapped. Every
  level keeps the catalogue's `Danger` in the advice. A preset marked `InternetOnly` (DNS as an open
  resolver, RDP, VNC — their danger is strangers, their advice a VPN) is not a finding at a reach the
  internet cannot share, so libvirt's dnsmasq on `virbr0` raises nothing. A port bound to several
  addresses is one finding at its widest, as its ID is per port, and its detail names the interface the
  address is on ("TCP/6379 is bound to 100.110.34.31 on tailscale0"). `HostNetwork.Place` returns the
  grade with the network it is made of — `tailnet`, `vpn`, `uplink` (a private address on an interface
  carrying a default route), `private` (one on another NIC, or on no interface the host listed),
  `docker` (Docker's `docker0` or `br-<network id>`), another `bridge`, `link-local` — and the interface,
  and `GET /ports` returns all three as `Listener.Reach`, `Network` and `Interface`, so the ports page
  words a socket as the posture judges it. A firewall that is on and does not allow inbound by default
  holds a port finding to a warning and is named in its detail — unless the default is not what the
  socket's traffic meets. A socket `docker-proxy` holds, or one `ExposedPort.Published` marks (a
  container's published binding names it, whatever holds it, or no socket at all where Docker's NAT
  alone publishes it), is Docker's published port, forwarded by its NAT rules before the input chain
  the default belongs to, so it keeps its reach's level and the detail says it is "published by Docker
  past the firewall's inbound default". A socket an inbound rule admits from
  anywhere keeps it too, and the detail names the rule ("firewall rule 10 admits it from anywhere"):
  `admittingRule` walks the rules in order, first match deciding as ufw and iptables do — ufw's and
  firewalld's `ALLOW`/`LIMIT` or iptables' `ACCEPT` in `INPUT` (its port read from the match text by
  `iptablesPort`), by a port, list or range, a firewalld service the catalogue names (`Firewalld`), or
  no port at all; a refusal from anywhere to any address met first ends the walk (not for iptables,
  whose interface column is not read). A rule limited to an interface, to a tailnet's or bridge's
  destination address, or naming a ufw application profile is not weighed, so an allow on
  `tailscale0` still leaves the internet to the default. One whose inbound default could not be read
  is not counted on. `GradePort` levels one socket by exactly these rules — the same `dangerAt` and
  `portLevel` `assessPorts` uses — and `GET /ports` returns its result as `Listener.Level`,
  `InboundDefault`, `PastFirewall` (`docker` or `rule`) and `FirewallRule`, reading the firewall status
  the posture reads, so the ports page and the proxy overview colour a database as the posture levels
  it.
- **One dangerous-service catalogue.** `netsec.ServiceOf` names a socket from `ServiceCatalogue` on
  port *and* protocol (DNS is 53/udp; a TCP socket on 53 is not flagged), and `GET /ports` returns it
  as `Listener.Service` and `Danger`. The frontend no longer keeps its own port list for sockets: the
  ports page, its "Dangerous services exposed" tile and the proxy overview's finding flag whatever
  carries `danger` and a posture `level`, so RDP, VNC, FTP and an open resolver are flagged there as
  the posture flags them. (The streams page still judges a stream's listen port by
  `DANGEROUS_PORTS`, since a stream has no socket to read `danger` from.)
- **The dashboard's own sockets stay on loopback.** `ExposedPort.Dashboard` marks a `Self` socket
  other than the dashboard's Caddy (process `caddy`, or compose service `proxy`); `ServiceOf` names it
  "Just Dashboard", an `InternetOnly` danger, so a backend or web app bound where the internet or the
  private uplink reaches raises `ports.self.<proto>.<port>` — critical, or a warning where the
  firewall's inbound default holds it, by the same `portLevel` — and a tailnet or bridge bind raises
  nothing.
- **A check that could not run is not a pass.** `Posture.Skipped` says which is which, because a zero and
  an unanswerable question look identical: `SecurityFiltering` is false on Alpine/Arch (no advisory
  data), `LoginRecordRead` false wherever `last`/`lastb` are missing (util-linux-extra, absent from
  minimal cloud images). Each is reported as a finding — silence in a security verdict reads as
  "checked, nothing outstanding".
- **A layer no check can see is named, not passed.** `Posture.Unknowns` (`posture_unknowns.go`) is
  neither a finding nor a pass. Provider policy is always unknown — no provider adapter exists — and its
  detail says whether this host has a public address on an interface (`publicHostAddress`) or is only
  reached through a provider's translation. Other nftables tables come from the gateway's own reading of
  the ruleset (`GatewayCapability().Layers`, handed in as `AssessInput.Policy`, so the ruleset is not
  parsed a second way): a base chain at the input, forward or prerouting hook that is not an iptables-nft
  table, not firewalld's and not the dashboard's own gateway, whose decision is more than an
  unconditional accept (`blocked` or `unknown`), is listed as a foreign decision this check does not
  evaluate; an unread or unreadable ruleset is itself an unknown. The Security page lists them under the
  findings as "Not seen by these checks", and a clean list beside them reads "No findings in the layers
  these checks can see". The ports sheet says the same of an exposed socket's provider, and the
  container reachability verdicts end with it: "reachable from outside" is unproven until an external
  check measures it. `JD_POSTURE_LIVE=1` (`TestLivePostureUnknownsOnThisHost`, run as root from a
  compiled test binary) grades the actual host read-only and lists its unknowns.

`netsec.Disconnect` ends an interactive login: the PID is matched against the live session list first,
or the route is a "kill any process on this host" primitive wearing a sensible name. SIGHUP, not
SIGKILL, so the login is recorded as ended.

## sshd

`netsec/sshd.go` reads the **effective** config (`sshd -T`, falling back to parsing) — a file setting
`PasswordAuthentication` twice does not behave the way it reads. Two sshd semantics are load-bearing and
get read backwards by anyone treating the file as an ini: the **first** value wins, and everything after
a `Match` is conditional. So the parser never overwrites an earlier value, a `Match` ends the file for
it, and its existence is reported rather than dropped.

`sshd_apply.go` writes in the proxy editor's order: refuse certain lockouts → write → `sshd -t` →
restore on failure → reload only then.

- The write target is a drop-in under `sshd_config.d` **only if the main file includes that directory
  before setting anything itself**. First-value-wins means a drop-in included at the bottom is a file we
  wrote and sshd ignored — the worst outcome for a security setting. Otherwise the directive is replaced
  in place and later duplicates commented out.
- `guardSSHLockout` refuses only the certain cases: passwords off with no key anywhere, both passwords
  and keys off, root the only keyed account with passwords off. Disabling passwords where somebody has a
  key is correct and must never be blocked.
- The directive list is **closed** — an open set makes this a config editor that can take the machine off
  the network.
- `Port` is bounded by `LegalMin`/`LegalMax`, not the `Min`/`Max` carrying the recommendation (they were
  one pair of fields, so the range was treated as advice). `guardSSHPort` refuses a move to a port a
  default-deny firewall has no rule for.
- `AllowUsers`/`DenyUsers` are `kind: "list"`: the value is explicitly checked for a newline (one would
  write a directive of the caller's choosing on the next line) and normalised through `strings.Fields`.
  An emptied list is commented out — sshd refuses to start behind a bare keyword.
- The bastion directives (`allowtcpforwarding`, `gatewayports`, `allowagentforwarding`, `permittunnel`,
  `maxsessions`) are in the closed list for the SSH page's jump-host section. `allowtcpforwarding` and
  `maxsessions` are `AlwaysAcceptable` — a jump host exists to forward TCP, so grading "yes" as insecure
  would put a permanent warning on a legitimate bastion — with the recommendation ("no, unless this
  server is a jump host") carried as text. The other three are graded against "no". The posture reads
  none of them, and `guardSSHLockout` does not either: forwarding cannot cost access to SSH itself. It
  can cost access to the dashboard, though: a request arriving on loopback came through an SSH tunnel,
  which rides on TCP forwarding, so `handleSSHApply` refuses `allowtcpforwarding no` or `remote` from
  one with `409 would_lock_you_out` (the current tunnel would stay up and the next would be refused).
- `permitrootlogin` folds `without-password` onto `prohibit-password`, because `sshd -T` still prints the
  deprecated spelling distributions ship as default and a dropdown missing it renders empty.
- `reloadSSH` tries systemd units, then `rc-service`, then `service`.

**Where sshd listens is not always sshd's decision.** Ubuntu ships socket-activated SSH by default on
24.04+: `ssh.socket` holds the listener, `sshd_config`'s `Port` is read, reported by `sshd -T`, and
ignored — which made the port control the one setting that reported success and did nothing.
`sshd_socket.go` reads and writes the unit alongside the daemon; `SSHDConfig.Socket` carries which unit
holds the port and which port that actually is. A move writes a drop-in and **restarts** (systemd
rebinds addresses only on restart; `daemon-reload` leaves the old port bound while reporting success).
Three drop-in details, each found against real systemd:

- An empty `ListenStream=` before the new one, because systemd *appends* — without it a socket moved to
  2222 still answers on 22.
- Both families named explicitly: a bare `ListenStream=2222` binds the IPv6 wildcard, and under
  `BindIPv6Only=ipv6-only` that takes IPv4 SSH off the machine.
- `BindIPv6Only=ipv6-only` repeated in the drop-in, since naming both addresses is only legal with it.

Addresses are rewritten rather than replaced (a socket bound to one interface stays bound to it), with
wildcards as fallback. Refusing the move would have been the safe-looking choice and would leave the
control broken on the commonest server distribution.

Network diagnostics, managed networking and the complete probe inventory are documented in
[the network module guide](network.md#diagnostics-and-host-support). Diagnostics remain administrator-only;
packet summaries can contain sensitive decoded fields and have bounded time/output.

## Firewall: one page, three backends

The page is `/network/firewall` since 0.7.1, in the Network section beside the gateway table the
[network module](network.md) owns; the routes below are unchanged. A port forward or NAT entry made there
admits its own connections past these rules by a connection mark, so it needs no rule here
([forward admission](network.md#gateway-and-protection)).

`netsec/firewall.go` dispatches to ufw, firewalld, the network module's owned nftables table or
iptables (`firewall_{ufw,firewalld,nft,iptables}.go`).
**Validation and both lockout guards live in the dispatcher**, so a fourth backend cannot be added
without them — that placement is the reason the refactor was worth doing. The shared `run` is a
**variable** so recorded transcripts can stand behind it; the fixtures in the three test files are copied
from the tools themselves, since a host running one backend is no way to check the other two.

ufw's grammar has shapes that are accepted and mean something else, checked against a real ufw with
`--dry-run` (`TestUFWAppProfileGoesInATargetClause`):

- **An app profile is a destination, and only a `to` clause reads it as one.** `allow in app OpenSSH` is
  refused; `allow in from 10.0.0.0/8 app OpenSSH` is accepted and binds the profile to the *source* port.
  Every rule names both ends: `from <src|any> to <dst|any>`, `app` inside `to`.
- **`ALLOW FWD` is a third direction** (ufw-docker writes many). An unrecognised direction leaks into the
  source address, and a rule parsed with no direction reads as inbound — which let a host whose only
  rules were forwarding rules pass `admitsAnything` and switch its inbound default to deny. They are
  refused by `replaceRule` and hidden from the edit control.
- **The destination column carries an address when the rule names one** (`10.0.0.5 5432/tcp`), so the
  port is its last field; `presetForRulePort` expands the lists and ranges the form itself writes
  (`6379,6380`, `8000:8010`) before consulting the catalogue. Both were silent: the rule parsed and the
  catalogue warning never fired.
- **ufw writes every rule twice and deletes one of them.** The page folds "(v6)" duplicates away, which
  is right for reading and was catastrophic for deleting: closing a port removed the IPv4 line and *hid*
  the standing IPv6 one. `DeleteRule` reads the rule first and finds its twin **afterwards** by shared
  fields (`sameRule`) — afterwards, because a delete renumbers everything below it.
- **ufw answers a duplicate by doing nothing and exiting zero.** "Skipping adding existing rule" read as
  success, and the `number+1` delete that followed removed the rule *below* the one being edited.
  `AddRule` reports `errRuleExists`, and only when *nothing* was written (a v4 accepted with its v6 twin
  skipped is a real add).
- `AddRule` has `insert` because ufw stops at the first match — a deny added after a broad allow does
  nothing at all, which looks exactly like a deny that works. **A source-only deny or reject with no
  position goes in front on its own** (`blocksASource`, `frontPosition`): that is what "block this
  address" from the Connections, Intrusion and Logins pages writes, and appended after `allow 22` it
  never saw the SSH traffic it was written to refuse. The positions are ufw's, checked with
  `--dry-run` against a real dual-stack host: an IPv4 block at 1, an IPv6 block at one past the last
  IPv4 rule (ufw numbers the v6 rules after the v4 ones and refuses an insert outside the family's own
  range), and an empty family appended to, because ufw refuses `insert 1` into nothing. An explicit
  `position` is kept.
- **A ban is a deny rule wearing another name**: `netsec.Ban` refuses the caller's own address, the same
  guard the firewall route has, and the jail sheet now reaches it. `IgnoreIP` writes through to the
  jail.d drop-in — `addignoreip` changes only the running server.

firewalld differs in ways that are the work: no rule numbers (a zone holds services, ports and rich
rules, each removed by handing back exactly what was added — numbers are positional from
`Service.Status`, and `Rule.Handle` is never serialised or accepted from a client), everything written
`--permanent` and reloaded (a runtime rule dies at reboot), ports spelled `8000-8010` with no multiport
at all (validation accepts the ufw spelling and `firewalldPorts` translates, one rule per port in a
single call rather than a partly-applied rule), and `AppProfiles` returning predefined services by name
only, since resolving several hundred would be a subprocess each — which is why the picker is searchable.

**iptables is read-only on purpose**: it has no persistence of its own, so a rule added here works until
reboot and leaves a page saying protected in front of a host that is not. `FirewallCapabilities` lets
each backend declare what it can do and `ReadOnlyReason` explains the absence — a greyed-out button with
no reason is worse than one that is not there. Its chain verdict arrives in the shared words
(`iptablesPolicyWord`: ACCEPT → allow, DROP → deny, REJECT → reject; `Default` keeps the verdict
verbatim). The structured policy is compared against `"allow"` by the posture rules, the exposed-port
grading and `guardSSHPort`, and `"accept"` matched none of them: an iptables host with an ACCEPT
policy was never told its inbound default was allow, had every exposed database graded as though a
firewall were refusing it, and was refused an SSH port move as a lockout the policy could not cause.

Cross-cutting:

- `FirewallStatus` carries the three default policies structured plus the logging level: allows in front
  of a default of *allow* are decoration, and a firewall that drops silently leaves no record.
  `ufw status numbered verbose` is rejected by ufw outright — it takes exactly one of the two words — so
  the old single call returned no rules and reported every firewall as inactive. Two calls now:
  `numbered` for rules, `verbose` for policy (soft failure).
- **`ReplaceRule` order is the safety property.** Neither ufw nor firewalld has an edit, so the
  replacement goes in **first**; deleting first and failing to add leaves a hole in the firewall, the one
  outcome an edit must never produce. The rule is read before anything is added and found again by what
  it says rather than where it sat. Ordering lives in `replaceRule`, separate from backend detection.
- `SetDefaultPolicy` refuses an inbound deny on a host admitting nobody. With the request's access
  context (below) it also refuses a default that would refuse a required way in.
- `ServiceCatalogue` (`GET /security/services`) is the rule form's teaching layer, served from the server
  so the form's warning and the audit's finding are the same claim. `annotateRule` attaches it centrally
  so firewalld's rules read like ufw's — and `parseUFWRule` must find the port, since `ufw allow 6379`
  writes a destination with no slash and a portless rule dropped the catalogue's warning entirely. It
  warns only when the source is unrestricted; the same port on a private range is the recommendation.
- **fail2ban jail tuning writes `jail.d/99-just-dashboard.local` as well as the running server** —
  fail2ban reads that directory last and a runtime change is gone at the next restart.
  `mergeJailOverrides` rewrites one section and leaves every other jail, and any hand-written line inside
  its own section, alone.
- **fail2ban-client does not print a list.** 1.x draws a tree under a heading and answers an empty set in
  a sentence (`No IP address/network is ignored`); `parseClientList` knew only the two shapes 0.x
  printed, so the allowlist panel showed entries reading "These", "IP", "addresses/networks". All four
  shapes parse now; the ambiguous bare line is settled by what the words look like — every real value is
  an address, a network or a path, and prose is not.
- **No start/stop for a jail**: `fail2ban-client status` lists only running jails, so one stopped from the
  UI would vanish with nothing left to start it. A control usable once is a trap.
- `FailedLoginVolume` counts inside a **window** and reports `Capped`. The posture verdict used `len()`
  of a 500-record btmp listing, which made the 2000-attempt threshold unreachable and the 200-attempt
  notice permanent on every host with a public SSH port.

### Which firewall, which rules, and who can still get in

- **Selection is by activity, not presence** (`firewall_detect.go`). Each candidate reports
  `active`, `inactive`, `unknown` or `not_checked`: `ufw status`, `firewall-cmd --state` (read even
  from its non-zero exit), the owned table's own state and `iptables -S` holding rules or a non-accept
  policy. The first active front end is in charge; otherwise the first installed one that can be
  switched on; then the owned table; then raw iptables for reading. Both front ends are always asked,
  and two active at once is a conflict: `capabilitiesFor` withdraws every write with the reason. Past
  an active front end the rest are not probed. `availableOnHost` is a variable for tests.
- **An inactive ufw lists nothing** — `ufw status numbered` prints only its status line — though it
  holds rules it loads the moment it is enabled. `show added` is read instead (`parseUFWAdded`, the
  command grammar ufw echoes), `rulesFrom: "configured"` says so, and the rules stay **unnumbered**:
  ufw numbers its IPv6 twins only once it loads them, and a number-based delete then (checked against a
  real ufw in a sandbox) leaves the twin behind. Its defaults come from `/etc/default/ufw` the same way.
  `(out)` closes an outbound source column, an IPv6 source carries no `(v6)` marker, and `on <device>`
  sits in either column, before or after `(v6)`; all three are parsed. An interface-scoped rule cannot
  be edited from the form (it would widen to every device) and is refused like a route rule.
- **Every rule has a stable identity** (`assignRuleIDs`, `fw-` and twelve hex digits): a digest of
  what a ufw rule says, or of firewalld's handle and zone. Edit and removal routes take `?id=`; a stale
  identity is `409 rule_changed` instead of whichever rule took the number. **Ordering findings**
  (`analyzeRules`) mark a ufw or owned-table rule shadowed or redundant when an earlier rule selects
  everything it does (family, direction, interface, protocol, source and destination containment,
  port ranges); firewalld evaluates denials before allowances whatever the listing shows, so it gets a
  note instead of invented findings.
- **The access guard** (`firewall_analysis.go`, `firewall_change.go`). The API puts an
  `AccessContext` on the request: the operator's address and arrival device, the port the dashboard was
  reached on, sshd's ports, the uplink, and Caddy's public ingress on 80 and 443 in both families.
  Every rule add, replace and delete, enable/disable, default and reset is first simulated on a copy of
  the status and each required check evaluated before and after — first match for ufw and the owned
  table, the landing zone (source binding, then interface, then default) with denials before
  allowances and the zone target for firewalld. A required way in that is admitted, limited or
  unfiltered now and refused afterwards is `AccessRefusal` (`409 would_lock_you_out`, with the check
  and the deciding rule). A rule the evaluator cannot read (a destination address, an unresolved
  profile, a device when the arrival device is unknown) is followed both ways, up to four deep: when
  both reach the same decision that is the verdict, so an allow that may or may not apply in front of
  one that admits anyway changes nothing. Otherwise the verdict is `unknown`, never admitted, and a
  required way in that is admitted now and `unknown` afterwards is refused like one that is refused.
  A status that cannot be read refuses a guarded change (`ErrUnreadable`, `503 firewall_unreadable`).
  A browser that reached the dashboard through an SSH tunnel is judged by the SSH session's address
  (`netx.OperatorAddress`), so its SSH check is kept.
  `GET /firewall/preflight?op=` returns the same comparison for the confirm dialogs, and the page shows
  the current verdicts as "Preserved access" (`GET /firewall/access`).
- **Staged verification and timed recovery.** ufw and firewalld changes run inside the network
  journal (`netx.ProtectFirewallChange`). Under its lock the change runs first with
  `netsec.Scoped(…, checkOnly)`, which stops every mutation at `ErrChecked` after its validation and
  access guard: a refused or invalid request opens no journal and sets off no recovery that would
  rewrite and reload an unchanged firewall. Both passes are bound to the firewall the journal was
  prepared for; one that changed hands in between is `409 firewall_changed`, never written through
  another path. A rule named by identity is resolved inside the protected change. The tool's own files
  (`/etc/ufw/*.rules`, `ufw.conf`, `/etc/default/ufw`; firewalld's `firewalld.conf` and the zone file)
  and whether it ran are snapshotted first; firewalld's boot unit is restored only from a plain
  `enabled` or `disabled` (`systemctl is-enabled`), any other answer leaving it alone. A firewall whose
  state cannot be read is not changed at all, since neither the guard nor the recovery can be prepared. After the change the firewall is read back and the access comparison repeated
  (`VerifyAccessAfter`); a failure restores the snapshot at once. With `X-JD-Network-Apply: pending` the
  change waits for the same reconnection confirmation as a network change and the independent host
  helper restores it at the deadline. Recovery is a closed vocabulary: those files and `ufw --force
  enable|disable`, `ufw reload`, `systemctl start|stop|enable|disable firewalld`, `firewall-cmd
  --reload`. ufw's enable starts a stopped firewall but does nothing to a running one, so an enabled
  state is restored by enable then reload — found by the real-ufw sandbox test.
- **Plans** (`firewall_plan.go`, `POST /firewall/plans/preview` and `/plans`): up to twenty adds,
  replacements and removals named by identity, validated and guarded together, run adds first and
  removals last, each re-resolved by identity just before it runs. A failure takes back the steps
  already made in reverse and the review says which steps applied, failed, were skipped or compensated.
  A device-scoped or forwarding rule cannot be removed in a plan: the form could not write it back as it
  was if a later step failed.
- **History** (`firewall_history.go`, `firewall_rule_events`): every dashboard rule change, refusal
  and failure is filed under the rule's identity, a replacement linking new to previous so a rule's
  history follows its edits; `GET /firewall/history?rule=` (administrators, as the audit log). Edits made
  with the tools directly leave no event, and the response says so.
- **firewalld zones and reset.** Status lists the active zones with their interfaces, sources, target
  and rules (read-only beyond the default zone), and the effective policy per family and interface.
  Reset reloads the default zone's shipped definition (`--load-zone-defaults`), only for a zone
  firewalld ships under `/usr/lib/firewalld/zones` (read through `/host`); the result is simulated
  from that file and judged by the access guard first.
- **The owned nftables table** (`BackendNFTOwned`, `netx/firewall_owned.go`): where no ufw or firewalld
  runs, table `inet jd_firewall` filters input for both families from the network spec, rendered to
  `firewall.nft`, checked with `nft -c`, loaded, read back and restored by the boot unit (never removed
  by its stop). Before its rules it admits established and related traffic, loopback, ICMP and ICMPv6,
  DHCP client replies, the gateway's translated connections by mark and the trusted operator sets.
  Changes go through the ordinary commit, journal and pending confirmation. Any other change that alters
  its render, such as a trusted address added or revoked on the Protection page, loads the table beside
  its own runtime change (`withFirewallLoad`) and takes both back together; otherwise a revoked address
  would stay admitted ahead of every rule until the next boot. No other table is read as
  its own or changed; their names are listed, since an accept here cannot override a drop there.

Routes under `/firewall`: `GET /`, `/apps` and `/access` (the requester's checks, kept off the status
several pages poll) for any reader;
for administrators `GET /history` and `GET /preflight`, `POST /plans/preview`, `POST /rules`,
`PUT /rules/{n}?id=`, `POST /logging`, and inside `s.destructive` `POST /enabled`, `/policy`, `/reset`,
`/plans` and `DELETE /rules/{n}?id=`. Every change is audited; the covered ones accept pending apply.

## Packages: six managers, one interface

`internal/updates` was apt-only, so every RPM, Alpine and Arch host reported *no package manager* —
which renders as "nothing to update" rather than "never checked", leaving the posture audit's patch
check silently dead on half the servers this runs on. It is a `manager` interface now (apt, dnf, yum,
zypper, pacman, apk), each a listing command parsed by a pure function plus an upgrade argv. Four
details were each confirmed by running the tool in a container of its distribution:

- **`dnf check-update` exits 100 when there is something to do.** Treating non-zero as failure is exactly
  backwards; it is read as a *code*, because guessing from the output's shape passed a real failure
  through as an empty package list.
- **dnf5 takes `--security` only after the subcommand**, so `dnf -y --security upgrade` failed on every
  Fedora from 41. Command first, which dnf4 also accepts.
- **zypper reserves 100–106 for informational exits** — one stale repo is not a failure, and returning
  early on those reported an error and no packages.
- **Alpine and Arch publish no advisory data**: `SupportsSecurityOnly` false, `SecurityFiltering` tells
  the UI "cannot tell", and `guardSecurityOnly` refuses a narrowed upgrade rather than quietly applying
  everything.
- **APT security-only upgrades pin exact installed package versions from security candidates.** The
  service simulates that explicit `install --only-upgrade --no-remove` command before returning it.
  A solver proposal that adds/removes packages, changes an unselected package or selects a different
  version is refused. `apt-get -t <security-suite> upgrade` is not used: a preferred suite does not
  restrict the upgrade set to security fixes. With no eligible candidates the request is refused, never
  broadened to a full upgrade.

Reboot detection: Debian's flag file, then `needs-restarting -r`, then "cannot tell". **Exactly exit 1**
means yes — every other non-zero is the tool failing, and reading those as yes puts a permanent reboot
warning on a host that never asked for one.

`manager` embeds `catalogue` (`ListInstalled`, `Search`, `Info`, `Files`, `InstallCommand`,
`RemoveCommand`) — embedded rather than optional because all six can do all of it, and a compiler error
beats a page that renders empty.

- **The installed set comes from the local database, never the front end** (dpkg, `rpm -qa`). Asking dnf
  needs a metadata cache present to answer a question about this disk.
- The installed inventory is reused for up to 30 seconds, with concurrent misses sharing one read.
  Cached and uncached answers preserve every architecture and return independent slices. Package jobs
  invalidate at start and completion, including failure or cancellation, and suppress cache fills while
  any job is active. A generation check rejects reads begun before invalidation. External changes are
  picked up after expiry; pending upgrades are still read separately on each inventory request. The
  cache belongs to this backend instance, and cancelling a waiting reader does not cancel a shared
  package-manager read, which has its own two-minute deadline.
- **"Installed on purpose" is a different question on each** and is what makes two thousand rows
  readable: `apt-mark showmanual`, `dnf repoquery --userinstalled`, pacman's `Install Reason`, Alpine's
  `/etc/apk/world`. zypper has no supported query, so `Explicit` stays false and the filter is hidden.
- **Ranking happens here** (`rankResults`: exact, prefix, contained, summary-only), identically for all
  six, so results do not reorder when the operator moves from Fedora to Debian. `apt-cache search git`
  otherwise puts `git` around row four hundred. The cap applies *after* ranking, and on apt before the
  `apt-cache policy` call — one subprocess rather than sixty.
- **A thin name-only search widens to descriptions.** Matching names is why `nginx` returns nginx and not
  the four hundred packages mentioning it; it is also why "web server" returned nothing, since no package
  is called that. Somebody who knows the name types the name; somebody who does not types what the
  software does. That widened bucket needs its own tie-break (ordering on name length put `nd` and `h2o`
  above nginx), so it ranks by how many typed words the summary carries.
- **The index has an age and it is on screen.** Every read answers from the on-disk database (a search
  that refreshed first would take a minute per keystroke), so the page is only as current as the last
  `apt update` — and on a server nobody logs into, that timer is the first thing to stop. `IndexAge`
  reads the manager's own cache, skipping lock files (touched by operations that fetched nothing);
  `RefreshCommand` fixes it. pacman returns false: a refresh without an upgrade is what turns the next
  `pacman -S` into a partial upgrade.
- **Names are validated before they are arguments** — not for quoting (nothing goes through a shell) but
  because every one of these tools reads a leading dash as a flag and several accept a path to a package
  *file* in the same position.
- `protectedReason` guards removal and is deliberately narrow, in the spirit of `guardSSHLockout`: the
  package manager, init, libc, the shell, sshd, docker, a kernel, plus dpkg's own `Essential`. Everything
  else goes through ordinary confirmation — a guard that second-guesses every risky removal is one nobody
  can work with.

`usage.go` answers "it is installed, now what", the part nothing else in this class has: a version and a
dependency list do not tell you `postgresql-client-16` gave you `psql`. The file list is read into
commands on the path, manual pages, systemd units, /etc entries and the README, and the primary man page
is *rendered*. **Nothing here executes the package's own binaries** — `foo --help` would run an arbitrary
host binary as root on a route needing only `read`. Three details, each a bug found by running it: a
command is a file whose **parent** is a bin directory (`/usr/bin` is in every file list; a binary in
`/usr/lib/postgresql/16/bin` is on nobody's path); a page is recognised by a `manN` **component**, not
`/man/man` (translated pages live under `de/man1`); and the page to render is **ranked**, or coreutils
shows TEST(1) and openssh-client shows scp. `stripOverstrike` undoes nroff bold in four lines rather than
shelling to `col -b`, which lives in the same package whose absence already costs the login records.

Security findings without an authorized direct remedy open their owning controls: dashboard network
allowlist, firewall, SSH, intrusion, listener ownership, certificates or host packages. Firewall and SSH
remedies retain their administrator/destructive gates and existing confirmations. Inspection remains
available to limited roles without advertising a mutation they cannot carry out.
