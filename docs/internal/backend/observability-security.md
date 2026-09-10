# Observability, host security, and packages

## Metrics, saturation, health

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
- Samples also retain the observed container ID for release attribution. The additive `container_id`
  column defaults to empty for old rows, which stay visible in name-continuous charts but cannot be
  attributed to a release. `ContainerIdentityRange` reads 1..64 distinct IDs in one aggregation with
  CPU/memory means, peaks and sample counts; its half-open window is limited to 24 hours and requested
  resolution to 600 buckets per container. Empty series remain empty, and retention-disabled reads
  return no samples. An `(container_id, ts)` index is installed after the column migration.
- Container network/block totals are stored as Docker's **cumulative counters** and differenced in SQL
  (`MAX - MIN` over the bucket). A total can be re-bucketed later; a rate recorded against one interval
  cannot.
- The recorder keeps its **own** `sysinfo.Collector` and `dockerx.StatsSampler`: rates are deltas, and
  sharing with request handlers would let a one-shot `GET /system/metrics` shorten the next interval.

**Saturation series** answer "is work waiting", which utilisation cannot: CPU **by mode** including
`steal` (on a VPS the one whose fix is outside the machine); PSI pressure (`Supported: false` renders as
"cannot tell", never three reassuring zeroes); disk IOPS/service time/%util as the **worst device** (a
disk saturated by small random writes moves almost no bytes); socket totals from `/proc/net/sockstat`
(enumerating connections is thousands of lines a sample); `load.Misc.Blocked`; and inodes per mount,
where a build server hits the ceiling first on a filesystem every capacity chart calls half empty.

`metrics.Assess` (`GET /system/health`) turns those into findings — measured / means / do — ranked
worst-first. It runs on the server because the thresholds are a claim the product makes, and because
each check reads an hour of history to tell a spike from a trend. **Memory is judged on available, never
on "used"**: Linux counts page cache there and judging by it is a permanent meaningless warning.

`metrics.Events` (`GET /system/metrics/events`) is the annotation layer, answered from `deploy_runs`,
`backup_runs` and `audit_log` — this dashboard *is* the thing that ran the deploy. Reboots need no
storage: a sample whose `uptime_seconds` dropped means the machine went down, which also catches
restarts nobody initiated here. Works with `JD_METRICS_RETENTION=0`; only reboot markers go quiet.

## netsec: exposure, posture, login history

`netsec` reads records the host already keeps rather than polling: wtmp (`GET /logins`), btmp
(`/logins/failed`, behind `system.admin` — it holds whatever was typed at a login prompt, sometimes a
password in the username field) and fail2ban's log (`/fail2ban/history`). Polling a jail would invent
events between samples and miss every ban shorter than the interval.

`netsec.Exposure` grades who can reach this panel (`tailscale`, `tunnel`, `private`, `public`, `open`)
from the allowlist and the host's interfaces. The setting lives in an env file nobody re-reads after
install day, which is exactly why it belongs on screen.

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
- **A check that could not run is not a pass.** `Posture.Skipped` says which is which, because a zero and
  an unanswerable question look identical: `SecurityFiltering` is false on Alpine/Arch (no advisory
  data), `LoginRecordRead` false wherever `last`/`lastb` are missing (util-linux-extra, absent from
  minimal cloud images). Each is reported as a finding — silence in a security verdict reads as
  "checked, nothing outstanding".

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

## Firewall: one page, three backends

`netsec/firewall.go` dispatches to ufw, firewalld or iptables (`firewall_{ufw,firewalld,iptables}.go`).
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
  nothing at all, which looks exactly like a deny that works.
- **A ban is a deny rule wearing another name**: `netsec.Ban` refuses the caller's own address, the same
  guard the firewall route has. `IgnoreIP` writes through to the jail.d drop-in — `addignoreip` changes
  only the running server.

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
no reason is worse than one that is not there.

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
- `SetDefaultPolicy` refuses an inbound deny on a host admitting nobody; ambiguous cases go to the typed
  confirmation, since a rule list admitting *something* cannot be judged without knowing which port the
  browser arrived on.
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

Reboot detection: Debian's flag file, then `needs-restarting -r`, then "cannot tell". **Exactly exit 1**
means yes — every other non-zero is the tool failing, and reading those as yes puts a permanent reboot
warning on a host that never asked for one.

`manager` embeds `catalogue` (`ListInstalled`, `Search`, `Info`, `Files`, `InstallCommand`,
`RemoveCommand`) — embedded rather than optional because all six can do all of it, and a compiler error
beats a page that renders empty.

- **The installed set comes from the local database, never the front end** (dpkg, `rpm -qa`). Asking dnf
  needs a metadata cache present to answer a question about this disk.
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
