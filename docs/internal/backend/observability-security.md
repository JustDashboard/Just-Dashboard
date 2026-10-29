# Observability, host security, and packages

## Metrics and saturation

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
  was unavailable when the sample was taken. The anomaly advisor was removed in 0.7.5; native
  usage history still retains the sample.
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
- Samples also keep Docker's cumulative CFS counters, `cpu_periods` and `cpu_throttled` (additive,
  nullable): written only for a container with a CPU quota, so a NULL is "no limit or not yet kept",
  never "never throttled". [Right-sizing](../deployments/right-sizing.md) differences them per
  container into the share of periods a limit throttled.
- **Volume sizes** of the volumes deployments own go to `metric_volume_samples` every quarter hour
  (`metrics.VolumeInterval`, `Recorder.WithVolumes`), on a loop of their own so Docker's disk walk never
  delays a host sample, and are pruned with the rest. The sampler (`api.sampleDeploymentVolumes`) reads
  the cached walk the Docker pages share and skips a size the walk did not reach.
- `GET /system/metrics/capacity` fits a least-squares line through hourly means of each filesystem's
  used bytes and each deployment volume's size and says when each disk fills at that rate, with the
  fit's R² and a confidence; see [right-sizing and the capacity forecast](../deployments/right-sizing.md).

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

The recorder also keeps how connections fared. `sysinfo.ReadTCPCounters` reads `/proc/net/snmp`
and `/proc/net/netstat` (TCP's MIB is shared by both families) and the collector turns them into
per-second rates; `ReadTCPLatency` asks `sock_diag` (`NETLINK_INET_DIAG`, `INET_DIAG_INFO`) for every
established socket's `tcpi_rtt` and keeps the median and 90th percentile of those whose peer is off
this host's networks (loopback, link-local, RFC 1918 and ULA peers are left out; the tailnet's
100.64/10 is kept). It sends nothing. The rates and the RTT live in additive nullable columns
(`tcp_out_segs`, `tcp_retrans`, `tcp_attempt_fails`, `tcp_estab_resets`, `tcp_listen_drops`,
`tcp_rtt_ms`, `tcp_rtt_p90_ms`), so an hour from before they were sampled reads as unmeasured, not as
clean. `Range` returns the bucket's resent share (`retransPct`) with the worst sample's as its peak,
failed attempts and accept drops with peaks, and the mean median RTT with the highest 90th percentile
as its peak; the metrics page charts them as Resent segments, Connection RTT and Failed connections.
They are recorded and charted; with Health removed, nothing judges them into findings.
`metrics.Events` (`GET /system/metrics/events`) is the annotation layer, answered from `deploy_runs`,
`backup_runs` and `audit_log` — this dashboard *is* the thing that ran the deploy. A deploy event
carries its project's id, so the Overview's activity row draws the project's own icon. Reboots need no
storage: a sample whose `uptime_seconds` dropped means the machine went down, which also catches
restarts nobody initiated here. Works with `JD_METRICS_RETENTION=0`; only reboot markers go quiet.

The deployments have a Prometheus exposition of their own, `GET /api/v1/metrics/deployments`
(`api/deployment_metrics_export.go`), behind the same `metrics.read` key as the databases': per
environment, the live release's age, its containers' CPU seconds and memory from the newest
`metric_container_samples` row, restarts, OOM kills and exits from the persisted container events
(`deploy_container_events`, kept at least thirty days whatever `JD_METRICS_RETENTION` says), run
outcomes, the last hour's requests by status class and latency quantiles, the warm-release window's
state, the maintenance switch and the availability figure over 24 h, 7 d and 30 d. The series and
what each reads are tabled in
[request observability](../deployments/request-observability.md#the-prometheus-exposition).

## Host alerts: told when the machine itself goes wrong

The host's **Alerts** page (`/metrics/alerts`, under Monitoring beside Metrics, for administrators;
Metrics' identity line links there with the firing count) holds rules that tell the deployments'
notification channels — through `deploy.AutomationStore.DeliverNotification`, so a channel is set up
once on `/deploy/notifications` — when the machine is in trouble, and when that is over. It is the
proxy alerts' shape ([Proxy alerts](databases-proxy-platform.md#proxy)): rules, subjects, mutes and a
bounded history, a picture of what is watched wired to the channels, and a test per channel and per
rule. Source: `api/host_alerts.go` (rules, the pass, `stepHostAlert`, history), `api/host_alert_readings.go`
(each kind's source and judge), `api/handlers_host_alerts.go` (routes); tables `host_alert_rules`,
`host_alert_state`, `host_alert_events` in `store/schema_host_alerts.go`; the page is
`components/metrics/alerts-page.tsx` over `lib/host-alerts.ts`, with the parts it shares with the
proxy's page in `components/alerts/alert-parts.tsx`.

- **Six kinds, a closed vocabulary** (`hostAlertSpecs`). `cpu_high` and `memory_high` take a line
  (default 90%, 10–100) and a window (CPU 10 min, memory 5 — the OOM killer does not wait ten; 1–1440):
  the **mean of the recorded samples** in `metric_samples` over the window, with its peak, so a spike
  inside a quiet window is a high peak and not an alert (Netdata's stock CPU alarm averages ten minutes
  the same way). A window the record does not cover yet — fewer than two samples, a newest sample older
  than three intervals (at least two minutes), or a first sample later than the window's start plus
  two intervals — is not judged. A firing message ends with the container that used the most over the
  window from `metric_container_samples` ("1.8 cores on average", Docker counting a core as 100%, or
  bytes for memory). `disk_filling` (default 90%, 10–99) reads the recorder's newest
  `metric_mount_samples` batch and fires a mount on its space **or** its inodes, since a filesystem out
  of inodes reports free space and refuses to create a file; a mount missing from the newest batch has
  been unmounted and leaves. `container_down` (minutes 2, 1–1440), `backup_failed` and `tunnel_down`
  (minutes 2, 1–1440) are below.
- **One pass a minute** (`hostAlerts.Start` from `Server.Start`, clock, observer and deliverer fields
  for tests); each source is read once a pass and only when an enabled rule needs it. CPU, memory and
  disk are queries of what the recorder already keeps; with `JD_METRICS_RETENTION=0` those rules are
  unjudged and say so.
- **A container is down** when it is running with a health check reporting unhealthy, Docker restarted
  it by its restart policy three times in ten minutes (a loop, counted from `RestartCount` across passes;
  a deploy's or an operator's restart is not counted by Docker at all), it is `restarting`, or it
  **stopped without being asked to**. The listing is `ListContainers(all)`, which inspects running
  containers for health and restart count as the Docker page's listing does; a stopped container is
  inspected once per stop (`dockerx.LastExit`, cached by id and the exit code its status prints) for its
  start, finish, exit code and OOM verdict. A stop counts only when it finished after the rule began
  watching on its current terms (`watch_from`), so containers stopped long ago, or while the rule was
  paused, never page. Then `judgeExit`: `dead` and an OOM kill are down; a `kill` or `stop` Docker
  recorded for the container between its start and its finish (`judgeStop`, from the event log — docker
  stop's SIGTERM, compose, a deploy's cutover and this dashboard all send one; a crash or the kernel gets
  only a `die`) is on purpose; exit 0 finished rather than failed; 130 and 143 were signals asking it to
  stop; 137 is down only when the event log was listening and still holds the ten minutes before the
  stop (`EventLog.Covers`) and saw no kill — otherwise it is read as a stop that outlasted its grace
  period, because a false page is worse than a late one; any other code is a crash. A deploy's release
  task and a `compose run` container are one-shot by design and never subjects, a `paused` container
  (a backup freezing its volumes) is fine, and a removed container leaves. A down container whose image
  is MongoDB 8 (by its tag), on a kernel MongoDB 8 refuses (Linux 6.19 to 7.0.13) and without the
  dashboard's mitigation label, has why added to its detail: MongoDB SERVER-121912 and that the
  Databases page applies the fix (`mongoKernelAlertLine`; *MongoDB 8 and the kernel* in
  [Databases](databases-proxy-platform.md)). It is the same kind, not a new one.
- **Every backup job** — files, volumes and database dumps alike — is a `backup_failed` subject
  (`job:<id>`), judged by its newest finished run in the database alerts' own words (`judgeBackupJob`
  over `readAlertBackupJobs`, the reader both share): it stays failed until a run succeeds. A job an
  enabled database `backup_failed` rule already follows (one for every database, or one for a database
  the job dumps) is **left to that rule** (`databaseBackupRules`), so one failed run is told once and
  keeps the database page's link; a job leaving the host rule that way is told it is over if it was
  firing.
- **The Cloudflare Tunnel is down** (`tunnel_down`, `tunnelAlertReading`) when its connector container
  is not running, or is running and its metrics say it holds no connection to Cloudflare's edge; the
  detail carries cloudflared's own words where it said why (the manager's last reading,
  [Cloudflare Tunnel](../deployments/cloudflare-tunnel.md)). One subject, `tunnel:<id>`, linking to
  Proxy › Tunnel; with no tunnel connected the rule is unjudged rather than quiet. It holds two passes
  and the rule's minutes, as a container does, because a connector reconnects by itself after a blip of
  the edge.
- **Hold and state** (`stepHostAlert`, one pure function). A subject is told once when it has been over
  the line for its kind's passes and minutes — one pass for CPU, memory (their window is the hold) and
  backups, two for disks, two and the rule's minutes for containers — and once when it comes back,
  never in between; one that comes back before it was told ends without a word. A reading that could
  not be taken, or a percentage between the line and five points under it, keeps the subject where it
  was. A subject the reading no longer finds is cleared, told it is over if it had fired. State rows
  exist only while a subject fires, holds or is muted; a rule's `revision` moves on every edit and a
  pass writes a subject only while the revision it read still holds. New terms, a pause or a resume
  start the rule over (mutes are kept, `watch_from` moves on); new channels alone do not. The newest 500
  events are kept, a test marked as one.
- **Messages.** `host.alert.firing` / `host.alert.recovered` with `NotificationEnvelope.Host`, rendered
  by `renderHostAlert` ([notifications](../deployments/notifications.md)); a rule with no channels tells
  every enabled one, a paused channel gets nothing, a muted subject is followed but not told.
- **Routes**, all `system.admin` and audited: `GET /system/alerts` (rules with why the last pass could
  not judge each, `unjudged`; firing, holding and muted subjects; the last 100 events; `metrics`, whether
  history is recorded), `POST /system/alerts/rules` (`host.alerts.rule.create`),
  `POST /system/alerts/rules/{id}/test` (the rule's message, `[test]`, to its channels;
  `host.alerts.rule.test`; 409 when no enabled channel is told), `POST /system/alerts/test {channelId}`
  (`host.alerts.test`), `POST /system/alerts/evaluate` (a pass now; holds still apply;
  `host.alerts.evaluate`); behind `destructive`, because each stops somebody being told:
  `PUT /system/alerts/rules/{id}` (`.rule.update`, can pause), `DELETE /system/alerts/rules/{id}`
  (`.rule.delete`), `PUT /system/alerts/mutes {ruleId, subject, muted}` (`host.alerts.mute`/`unmute`).
- **Tests.** `host_alerts_test.go`: validation and defaults, the state machine's holds and transitions,
  each kind's reading with made-up readings (the window's mean, coverage and band; space and inodes per
  mount; a crash told from a stop, a loop, a one-shot and a stop before the rule; every job but those a
  database rule follows), the restart window, and the store queries.
  `handlers_host_alerts_test.go`: the admin gate on every route, audit, firing once after the hold,
  unknown and unjudged passes, a muted recovery, both tests, a pause clearing state, an edit during a
  pass, and the server's own observer on staged backup jobs and a CPU rule with no samples.
  `events_test.go` covers `Covers`, `notifications_test.go` the message, `schema_host_alerts_test.go` the
  tables on fresh and upgraded installs, `lib/host-alerts.test.js` the page's words and form ranges, and
  `tests/browser/host-alerts.spec.ts` the page. Not verified here: a real container crashing under a
  live daemon end to end and a real channel's delivery (the readings and the delivery path are the ones
  the tests drive).

## netsec: exposure and login history

The Security overview's Intrusion reading describes fail2ban only: its current bans and jails,
whether it is installed or running, or a status read that failed. It never infers that no engine
blocks repeated failures from a missing fail2ban. CrowdSec and the combined engine evidence remain
on Intrusion; a failed overview read stays unknown rather than being described as no protection.

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
has a log of hundreds of megabytes, and the repeat offenders (`/fail2ban/offenders`) read it when requested; the tail holds the events every reader wants. The Intrusion
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

Security pages display exposure, firewall, SSH, intrusion and connection records directly. The
security posture collector, findings UI and `GET /security/posture` were removed in 0.7.5. Exposure
returns the observed access arrangement without a recommendation. `port_reach.go` retains the binding
and firewall attribution used by listener inventory and requested connection diagnostics. The SSH
editor exposes current values, options and legal constraints without `recommended` or `secure`
judgements. Its lockout guards, preview, confirmation, audit and recovery still apply.

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
- `Port` is bounded by `LegalMin`/`LegalMax`. `guardSSHPort` refuses a move to a port a
  default-deny firewall has no rule for.
- `AllowUsers`/`DenyUsers` are `kind: "list"`: the value is explicitly checked for a newline (one would
  write a directive of the caller's choosing on the next line) and normalised through `strings.Fields`.
  An emptied list is commented out — sshd refuses to start behind a bare keyword.
- The bastion directives (`allowtcpforwarding`, `gatewayports`, `allowagentforwarding`, `permittunnel`,
  `maxsessions`) are in the closed list for the SSH page's jump-host section. Forwarding choices
  do not enter `guardSSHLockout`: forwarding cannot cost access to SSH itself. They can cost access
  to the dashboard, though: a request arriving on loopback came through an SSH tunnel,
  which rides on TCP forwarding, so `handleSSHApply` refuses `allowtcpforwarding no` or `remote` from
  one with `409 would_lock_you_out` (the current tunnel would stay up and the next would be refused).
- `permitrootlogin` folds `without-password` onto `prohibit-password`, because `sshd -T` still prints the
  deprecated spelling distributions ship as default and a dropdown missing it renders empty.
- `reloadSSH` tries systemd units, then `rc-service`, then `service`.
- **A pending apply is kept only once this session returns a fresh dashboard response.** With
  `X-JD-Network-Apply: pending` (the SSH page sends it wherever `GET /network/changes/current` says the
  independent watchdog is available, behind a "Restore unless confirmed" switch), `handleSSHApply`
  plans as before, then `netx.BeginSSHChange` snapshots every file the plan writes
  (`SSHApplyPlan.Files`: the managed file and, for a socket port move, the socket drop-in) into the
  network journal with subsystem `sshd`, arms the ninety-second `systemd-run` timer and only then
  starts the job. The journal accepts only `sshd_config`, `sshd_config.d/99-just-dashboard.conf` and
  `<ssh|sshd>.socket.d/10-just-dashboard.conf` beside the boot unit, and an undo vocabulary of
  `reload` (`systemctl reload-or-restart ssh`, then `sshd`) and `socket <unit>` (`daemon-reload`,
  `restart`); any other file, command or subsystem is refused before recovery touches anything. A
  reload or socket failure, which an immediate apply reports as partial success
  (`SSHApplyResult.Failure`), restores the snapshots at once in pending mode. The confirmation is the
  network one — verify, then confirm within thirty seconds, from the same account, session and
  source — and the global notice words it as an SSH change. Unconfirmed, the standalone
  `--network-recover` helper restores the files and reloads sshd without the backend; at boot it
  uses `--no-block` and `try-reload-or-restart`, since the recovery unit is not ordered against sshd.
  A journal is one change at a time: a pending SSH change blocks network changes until it is
  confirmed or recovered, and the other way round.

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
  address" from the Connections, Intrusion and Logins pages writes (the Connections page's as a
  recorded block, below), and appended after `allow 22` it
  never saw the SSH traffic it was written to refuse. The positions are ufw's, checked with
  `--dry-run` against a real dual-stack host: an IPv4 block at 1, an IPv6 block at one past the last
  IPv4 rule (ufw numbers the v6 rules after the v4 ones and refuses an insert outside the family's own
  range), and an empty family appended to, because ufw refuses `insert 1` into nothing. An explicit
  `position` is kept.
- **A block from the Connections page has a reason and an end** (`netsec/blocks.go`,
  `network_address_blocks`). `POST /firewall/blocks` (admin, audited) validates one remote address
  (no loopback, unspecified or multicast), a one-line reason, a length of five minutes to ninety days
  or none, and an optional saved diagnostic run as its incident (checked to exist), then writes the
  same source-only deny through `AddRule` with the caller's address — so the lockout guard refuses
  the operator's own address — commented `jd-block <id>`. The rule goes in before the record; a
  record that cannot be written takes its rule back. An address already blocked, or already denied by
  a plain rule somebody wrote, is refused rather than shadowed. A loop started with the server lifts
  ended blocks every minute, removing exactly the block's rule — found by its comment, or on a
  firewall that keeps no comments by its exact shape, which creation refused to duplicate — reading
  the list again before each delete; a failed removal leaves the block active with its error and is
  retried, and each outcome is a `system` audit entry (`firewall.block.expire`). Lifting only ever
  opens the firewall, so the loop can cut nobody off. `GET /firewall/blocks` (the rules' standing)
  lists them with whether the firewall still lists each rule; `DELETE /firewall/blocks/{id}` (admin,
  destructive) lifts one now. The Intrusion and Logins pages keep the plain permanent deny.
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
verbatim). The structured policy is compared against `"allow"` by the exposed-port
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
- **A published container's port is not these rules' to refuse.** Docker forwards it before the input
  chain they belong to (`PastFirewallDocker` in the port grading), so a database's *Allowed addresses*
  are kept by the gateway table's prerouting drop ([kept ports](network.md#gateway-and-protection)).
  The rules the database reach route writes here — one from anywhere, or one per listed network,
  commented `just-dashboard database <port>`, as firewall plans inside `protectFirewall` and filed in the
  history — are for this page's own listing and the traffic the firewall does see: docker-proxy's, and a
  native listener's ([databases](databases-proxy-platform.md)).
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
- **A jail's policy is explained, not only shown.** `GET /fail2ban/{jail}/policy` (`JailPolicy`, read
  like the config) reads the running values, `logpath`, `journalmatch`, `ignoreself`, the actions and
  each action's `port` and `type`, and `ExplainJailPolicy` turns them into the rule as a sentence
  ("5 failures within 10 minutes earn a 2-hour ban"), what the jail reads, what each action does
  with a ban (firewall, blackhole route, Cloudflare edge, report only, unknown — classified by the
  action's name; `allports`, `type=allports` or `0:65535` cover every port), and findings: no action
  that blocks is critical, a watch with neither file nor journal match is a warning, and for the
  `sshd` jail a ban whose ports do not cover a port sshd actually listens on (service names from
  `/etc/services`, ranges included) is critical. Values in `jail.d/99-just-dashboard.local` that differ
  from the running server are marked as what a restart will load — the dashboard's drop-in only; the
  distribution's files are not merged. `get <jail> actions` prints several actions on one
  comma-separated line, which the address-list parser read as prose; `parseActionList` reads it.
- **Every engine's refused addresses are folded by address.** `GET /security/blocks` reads fail2ban's
  banned addresses, CrowdSec's `ban` decisions on an address or range (`CrowdSecDecisions`, the
  decision list alone) and the firewall's inbound `DENY`/`REJECT`/`DROP` sources concurrently, and
  `MergeBlocks` keys them by canonical prefix: each entry names every source holding it and the
  broader blocks (any engine) that already contain it. Community decisions no local source touches
  are counted, not listed; an engine that was not read is reported as such. The Intrusion page draws
  it first, and both ban forms say which engine already holds the address being typed. The posture verdict used `len()`
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

## CrowdSec: enforcement is verified, not assumed

`CrowdSecView.enforcement` (`AssessEnforcement`, pure) decides whether the decisions are dropping
anything. A bouncer counts only if its key is valid, it pulled within three minutes and, for the
firewall bouncer, `crowdsec-firewall-bouncer` is active. A fresh firewall bouncer is `enforcing` only
when the kernel holds a CrowdSec set with a drop or reject rule in a chain attached to a netfilter
hook — read from `nft -j list tables` and `nft -j list table <family> <crowdsec…>`, or from
`ipset list -t` with the `iptables -S`/`ip6tables -S` rule matching the set — and the sets are not
empty while ban decisions are in force; otherwise `degraded`. Proxy bouncers alone are `partial`
(HTTP only; their drop cannot be read here); an unreadable kernel or an unknown bouncer kind is
`unverified`; no fresh pull is `stale`; none registered is `unenforced`; a stopped engine is
`stopped`. An unreadable bouncer list is `unverified`, never `unenforced`. Every read is a listing.
The panel claims protection only for `enforcing` and `partial`. Historical posture rules raised
`intrusion.crowdsec-unenforced` (warning), `-partial` or `-unverified` (notices) from the same
verdict. The nft fixtures are nft's own JSON from a throwaway namespace; the ipset one follows
`ipset list -t`'s format.

## Suricata: setup after installing

The view carries the setup evidence: the newest `stats` event in the eve.json tail (kernel packets,
kernel drops, decoder packets, uptime — absent where there is none), the af-packet interfaces in
`suricata.yaml` with the host's up, non-loopback interfaces as candidates, the rule file's age and
the enabled `suricata-update` sources, and every NFQUEUE rule (`iptables-save`, `ip6tables-save`,
`nft -j list ruleset`) with whether it bypasses when nothing reads the queue. The mode is read from
the service's command line first: Debian ships `LISTENMODE=nfqueue` beside a unit that runs
`--af-packet` and never reads that file, so the defaults file decides only where the command line
does not say.

Three admin jobs, each a fixed argv: `POST /security/suricata/interface` rewrites the first
non-default af-packet `interface:` line in place (every other line kept), tests with
`suricata -T`, restores the file on failure, and restarts a running Suricata — restoring the
previous interface and restarting again if it does not come back; it is refused in inline mode, when
the unit names its interface on its command line, and for an interface that is not up.
`POST /security/suricata/rules/update` runs `suricata-update` (which tests the rule set with Suricata
itself) and reloads a running Suricata; `POST /security/suricata/start` runs
`systemctl enable --now suricata` and checks it stayed up. Inline queue rules are read and never
changed: a queue without bypass drops what it queues while Suricata is not reading, the dashboard's
own traffic included if it is queued.

## The access boundary

`netsec.DescribeBoundary` (pure; `GET /security/boundary`, readable by every role) reads five checks
from evidence the API gathers concurrently: **ingress** (Caddy holds the configured port and no
other dashboard socket answers on a routable address — the listener walk with owners), **allowlist**
(this request's address is inside the running allowlist; a loopback request names the SSH session
carrying it), **tailnet** (an allowlist that admits 100.64/10 has a tailscale interface up),
**ssh** (a socket listens on sshd's port, for a tunnel) and **previews** (every served port in
21000–21999 maps to a loopback upstream and is not funnelled, from `ServedTailnetPorts`). Each is
`held`, `broken` or `unknown` — unread evidence is never held.

`BoundaryImpacts` judges a proposal (`ban`, `firewall.rule`, `firewall.policy`, `ssh`) against it:
a ban or a deny covering this session's address or tunnel peer on the dashboard's or sshd's port
`cuts`; one overlapping an allowlisted network other than loopback or `0.0.0.0/0`, or the tailnet
previews are served to, `affects`; a deny of UDP 41641 from everywhere sends tailnet peers through
DERP; an inbound deny default leaves the dashboard and sshd to their rules; an SSH port move,
`AllowTcpForwarding no|remote` (a cut from a tunnel session) and `AllowUsers` affect who can open a
tunnel. `GET /security/boundary/check` answers it for the forms; `api.boundaryGate` enforces it on
the fail2ban ban, CrowdSec decision and SSH routes. A pending network change (any route that takes
`X-JD-Network-Apply: pending`, and a pending SSH apply) has the boundary read before it is applied;
the verification returns the boundary now beside that picture (`CompareBoundaries`, `lost` for a
boundary that held and does not), and the confirmation notice lists it and words its button
"Confirm anyway" when one was lost. The before-picture is held in memory for ten minutes; after a
backend restart the verification says it has none. Firewall, gateway and other Network forms can
ask the check route; wiring their own forms to it belongs with those owners.

## Packages: six managers, one interface

`internal/updates` was apt-only, so every RPM, Alpine and Arch host reported *no package manager* —
which renders as "nothing to update" rather than "never checked", leaving the historical posture audit's patch
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

**Package commands survive the dashboard and repair what an earlier run left.** Install, upgrade and
removal (and certbot's install) go through `api.runPackageCommand`, which runs them with
`jobs.RunDetached` as host systemd units: a docker-ce upgrade restarts Docker, which used to kill the
backend container and, with it, the dpkg it had started — the host was then stuck on "dpkg was
interrupted, you must manually run 'dpkg --configure -a'" for every later apt command. When
`updates.DpkgInterrupted` sees apt's own sign of that state (a numbered entry in
`/var/lib/dpkg/updates`, read through `hostexec.HostPath`) while neither `lock-frontend` nor `lock` is
held, the command is wrapped by `FinishingDpkgFirst` in a constant script that runs
`dpkg --force-confold --configure -a` and then execs the command, in one unit so the Docker restart the
repair usually causes cannot separate the two. Every mutating apt-get also carries
`-o DPkg::Lock::Timeout=600`, so one started while unattended-upgrades holds the lock waits instead of
refusing.

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

## Truthful empty blocks and package outcomes

The merged intrusion block list's empty wording states that no blocks were returned in this view;
it does not infer that every engine blocks nobody. Engine availability and community decisions remain
separate evidence. The allowlist editor's IPv4 loopback helper performs prefix membership, so a CIDR
such as `127.0.0.0/8` correctly covers `127.0.0.1` without accepting unrelated prefixes.

Package installation outcomes are tracked by accepted package/job identity, independently of the
currently viewed console and retained across tab changes. A failed or canceled attempt can be retried;
a terminal job poll wins over stale running console status, and old attempts cannot overwrite the latest one.
Completion re-reads catalogue installed flags, installed searches and inventory/update readings.
Upgrade confirmations describe the native manager: ordinary APT holds back dependency additions and
removals, security-only APT retains its simulated exact candidate contract, dnf/yum and zypper may add
or replace dependencies, pacman may also remove replacements, and apk may add/replace dependencies.
The UI does not promise that affected services cannot restart. No live package upgrade is needed for
these regressions: unit outcome tests and browser fixtures cover retries, refreshes and six managers.
