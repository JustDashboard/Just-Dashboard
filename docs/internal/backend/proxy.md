# Proxy

- Public-address discovery excludes CGNAT (`100.64.0.0/10`) as well as private, loopback and link-local
  addresses, so Tailscale interfaces cannot produce a public deployment hostname or a false public DNS
  match. Deployment HTTP-01 issuance checks DNS destinations before ordering; existing certificates
  remain usable for private names. Certbot error summaries prefer ACME validation details and skip the
  generic community-help footer.
- Deployment certificate orders use the running nginx listener's webroot instead of trying to bind
  standalone Certbot over occupied port 80. A temporary, validated challenge-only route and a file probe
  establish host/container filesystem visibility before ordering, with snapshot restoration on every
  exit. The fixed `/srv/just-dashboard-acme` root survives deployment route activation and site form
  round trips through `managedAcme`; ordinary site's ACME roots remain `/var/www/html`. Docker Caddy
  ownership uses native automatic HTTPS and shared routes; unsupported owners are reported explicitly.
  See [the ingress decision](../deployments/caddy-ingress.md) for provisioning, recovery and live tests.
- **Listening ports** (`ports.go`, `ports_owner.go`) are read from the kernel's own tables,
  `/proc/net/{tcp,tcp6,udp,udp6}` (under `HOST_PROC` when set, the variable gopsutil reads for the
  process details), not from gopsutil's connection list: that list keeps one holder per socket, and its
  `/proc` walk meets PID 1 first, so a socket-activated sshd, whose port systemd holds too, was listed as
  systemd, `/sbin/init`. `socketHolders` keeps every PID holding a listening inode and `ownerOf` sets init
  aside, then names the holder whose parent (`/proc/<pid>/stat`) is not itself a holder: a prefork
  server's master, not one of the workers that inherited the socket. Not the lowest PID, because once the
  PID counter wraps a reload's respawned workers are numbered below their master. The lowest PID decides
  only between unrelated holders, and init only when it holds the socket alone.
  `Listener.Scope` is `loopback`, `interface` (one specific address) or `all`; `Exposed` is every scope but
  loopback. It used to mean "bound to a wildcard", which drew caddy on the tailnet address as loopback and
  failed to identify a database on a public IP as exposed. `Listener.Reach` is
  netsec's grade of the address (`loopback`, `host` for a bridge, `network`, `public`, `all`, see
  observability-security.md), and `Listener.Network` and `Listener.Interface` name what it is made of
  (`tailnet` on `tailscale0`, `docker` on `docker0` or a `br-<network id>`, `public` on `ens3`, `vpn`,
  `uplink` for a private address on the default-route interface, `private`, another `bridge`,
  `link-local`, `loopback`, `all`); all three come from one `HostNetwork.Place`, which needs the
  interfaces, so `GET /ports` fills them and `ListListeners` leaves them empty. `GET /ports` also fills
  `Listener.Level` and `InboundDefault` from `netsec.GradePort`, with the firewall status read beside the
  walk: the service catalogue's reach level for that socket and the firewall's
  inbound default when that is what holds it to a warning, or `PastFirewall` — `docker` for a port
  docker-proxy holds, `rule` with `FirewallRule` for one a rule admits from anywhere — when the default
  does not hold it (see observability-security.md). The ports page words a socket by its network
  ("Tailnet only · tailscale0") and colours a database by the same `Level`. `Listener.Family`
  (`ipv4`/`ipv6`) is the kernel table the socket is in, and `Listener.PPID` the owner's parent; the page
  folds a service's two families on one network into one row and one count when one process holds both,
  or one program run by one account was started by one parent (not init) or with the same command line
  but for its addresses — Docker holds a published port's two families in two docker-proxy processes,
  dockerd's children, each told its own `-host-ip`. The overview's internet-facing fact counts the same
  folded rows. A UDP socket on port 0 is not listed, and `GET /ports` gives the walk ten seconds before
  a retryable 504, which the page offers to try again. `GET /ports/meta` answers `{ephemeralRange: {low,
  high}}` from `EphemeralPorts`, `net.ipv4.ip_local_port_range` read under the same process table
  (`null` where it cannot be read), apart from the list so `/ports` stays the plain array other pages
  read: the page sets aside loopback sockets inside it on request, and the Connections page links a
  connection's local port to the ports page only outside it, where somebody chose the port.
  Every exposed socket carries `firewall` (`netsec.JudgeFirewall` in `reach.go`): `allowed`, `restricted`
  (a rule admits one source ahead of a refusing default), `blocked`, `off`, `docker` (published past ufw's
  or iptables' input chain; firewalld is not claimed) or `unknown`, with the deciding rule's number and
  action, read in first-match order and passing over rules whose target the listing cannot say.
  `GET /ports/firewall` answers `{backend, available, enabled, editable, incoming, orphanRules}`:
  `netsec.OrphanRules` lists the inbound allows on single ports or lists that no socket and no Docker
  publication answers (ranges, every-port, profile and interface rules and ufw's IPv6 twins are left out).
  It reads the firewall afresh each call, uncached, because the page deletes an orphan by number through
  the existing destructive `DELETE /firewall/rules/{n}` and ufw renumbers on every delete; the page also
  re-reads `/firewall/` and refuses when the number now names another rule. Any signed-in account may
  read it, as it may read `/firewall/`.
  `GET /ports` also says what the proxy does with each socket (`proxysvc.AttachProxy` in
  `ports_routes.go`): `routes` are the enabled sites whose upstream reaches the TCP socket
  (`SitesForUpstreamPort`, shared with the container routes view: `localhost`, a loopback, `0.0.0.0` or one
  of the host's own addresses at that exact port — `:30000` is not `:3000` — and only the loopback spellings
  for a loopback socket; a container or other host name never matches; `StreamsForUpstreamPort` reads a
  stream's upstream and pool the same way, a range forwarded port for port included, for the server
  map's line from a stream to what holds its port), `stream` the nginx stream on an
  nginx-held socket when nginx includes the stream directory, and `servedSites` the enabled nginx sites
  listening on an nginx-held port. `GET /ports/free?protocol=&address=&from=&count=` (system.admin: it binds
  each candidate for a moment on the address named) finds up to 20 ports with `portalloc.Select`, searching
  up from `from` and passing over every host port a container publishes or, stopped, keeps in its
  `PortBindings` (`dockerx.HostPortBindings`, ranges expanded); it answers `{ports, skipped,
  containersChecked}` so the page does not claim to have avoided containers when Docker did not answer.
  It also passes over claims a bind cannot see (`api.freePortPolicy`, `ports_reservations.go`), each
  read beside the search under the owner-source bound and consulted in a fixed order: unexpired
  deployment candidate leases (`deploy_port_leases`, matched by address overlap), the tailnet preview
  range `selfcfg.TailnetPortMin`–`TailnetPortMax` (for a wildcard or tailnet address only), the kernel's
  ephemeral range (outgoing connections take ports from it), an enabled firewall's inbound rule already
  naming the port (admitting it from where the rule says, or refusing it — whatever binds there meets
  that policy at once; read through `netsec.InboundRuleFor`, so an interface-limited or profile rule is
  not taken for one covering every address), and an enabled gateway forward translating it away. A
  loopback search skips the firewall and gateway, which decide only what other hosts reach.
  `JD_PORT_POLICY_LIVE=1` (`TestLiveFreePortPolicyOnThisHost`, run as root from a compiled test binary)
  reads every source on the actual host for a wildcard search and lists what each would pass over,
  binding nothing. `reservations` names each
  passed-over port (at most 32) and why; `sources` lists every owner consulted with `checked`,
  `unavailable` (with the reason) or `not_supplied` — provider reservations, for which no adapter
  exists, always say so rather than being left out.
  `GET /ports/external` (system.admin, like the external checks it reads) joins the external-check
  owner's retained checks to their enrolled sources (`ports_external.go`): per check the port, family,
  source, placement, measured address, whether that address is on this host, lifecycle status and the
  TCP stage's state and basis — an expired or cancelled check stays `unknown`, never a failed
  measurement — plus each enrolled, unrevoked source's scopes, so the page can offer a check for a
  port a scope names. It starts nothing; a check is created through `POST /network/external/checks`.
  An unavailable owner answers 200 with `error` rather than an empty reading.
  Each listener carries `pids`, every process holding the socket, and on TCP `clients` — the ESTABLISHED
  connections in the same socket tables (`countClients`: same family and local port, the exact address
  before a wildcard listener), with the five busiest remote addresses. `POST /ports/identify`
  `{protocol, address, port, serverName?}` (system.admin, audited as `proxy.ports.identify`: it sends
  traffic the dashboard originates) answers 404 `not_listening` unless a TCP socket listens on exactly that
  address and port now (`proxysvc.ListeningAt`), so it cannot be aimed elsewhere; a wildcard is dialled on
  its family's loopback. Within 5 seconds it reads a banner (1.5 s), and only if none came, a TLS handshake
  (certificate read, not verified; ALPN offered h2 and http/1.1; a refusal alert still means TLS) and a
  `HEAD /` over TLS or plain, reporting status, `Server` and `Location`. `serverName` must be a DNS name and
  is sent as SNI and Host.
  `TestListListenersNamesTheDaemonNotInitOnThisHost` checks the owner on the real host and runs only as
  root; it reads `/proc` and changes nothing.
  **Owners.** `ListListeners` also reads, once per holder, its start (`StartedAt`, the processes page's
  own `CreateTime`, so a signal sent from the page can refuse a reused PID), its supervisor from its
  cgroup under the same process table (`procs.ManagerOf`: `Manager`/`ManagerName`, `systemd` and a unit,
  `container` and a short ID, `session`), and `DisplayName` — the executable's base name, or the
  command line's first word where another account's `exe` is unreadable — where the kernel's name is a
  thread's (node names its main thread `MainThread`); `Process` stays the raw comm, which
  `dbx.DetectHost` and the HTTP-01 planner match on. `GET /ports` then runs `AttributeOwners` with what
  `ownerInput` read beside the walk, each source under four seconds and left out when it fails: the
  running containers from `dockerx.ListRunning` (the Engine's list, no inspects), `systemctl list-sockets
  --all --show-types --output=json` through `procs.Systemd.Sockets` (read with `SocketUnitAt`: inet
  stream and datagram addresses only), and — after the walk, and only when some owner's parent is a PM2
  daemon (`UnderPM2`), since `pm2 jlist` starts a daemon where none runs — PM2's app PIDs. A socket systemd listens on for a service
  carries `SocketUnit` and `Activates` (`ssh.socket` → `ssh.service`); a PM2 app's PID becomes `pm2` and
  its name; a socket whose protocol, host address and port a container's published binding names — the
  kernel lets one socket hold them, so whatever holds it is Docker's (docker-proxy, dockerd, or a holder
  this account cannot see) — carries `Container` with `Published`, as does a wildcard docker-proxy or
  unseen socket whose other family's binding names it (a Docker before 20.10 published every interface
  as one proxy on `::`); a container on the host's network is joined by its cgroup's ID. A published
  binding no socket stands for — the userland proxy off, NAT rules alone — becomes a row of its own with
  `Source: "docker-nat"` and no PID. `Container.Deployment` names the project and environment its
  `io.just-dashboard.environment-id` label (on a container also labelled `io.just-dashboard.managed`)
  points at, looked up in `deploy_environments`. `Self` marks the dashboard's own sockets: its own PID,
  and every container of the compose project whose container mounts `JD_DATA_DIR` (the `backend`
  service's where several do); the ingress the dashboard creates serves deployments and is not its own.
- **The ports history** (`ports_history.go`). The kernel keeps no record of what listened before, so
  `PortRecorder`, started from `startProxyExtras` and stopped from `stopProxyExtras`, walks
  `ListListeners` at once and then every minute and compares the result with the stretches still open
  in `listener_observations` (`store/schema_proxy.go`): one row per stretch of time one socket listened
  with one owner, `opened_after`/`first_seen` the samples either side of its opening and
  `gone_after`/`gone_at` of its closing, Unix seconds. `listener_history` is one row, when recording
  began and the latest sample, which is what a change is dated after — across a minute or across a
  restart, so a socket that went while the dashboard was stopped is dated to the whole unsampled span.
  `diffListeners` is pure: a socket is its protocol, family, address and port, and stays the same
  socket while the same program run by the same account holds it; a new PID alone (a restart between
  samples) only updates the row, an owner this account could not read matches any rather than
  recording a close and reopen, and a different program or account closes the stretch and opens
  another. The first sample ever taken is the baseline (`opened_after` NULL: listening when recording
  began, never reported as opened); a walk that fails records nothing rather than every socket
  closing; loopback sockets inside `net.ipv4.ip_local_port_range` are not recorded, as the page sets
  them aside. A partial unique index holds one open stretch per socket. Stretches that closed more than
  30 days ago are pruned at start and hourly; a failing walk is logged once until it recovers.
  `GET /ports/history?hours=&limit=` (any signed-in account, as `/ports`; hours 1–720, default 24;
  limit 1–5000, default 1000, anything else a 400) answers `{recordingSince, lastSample, stalled,
  intervalSeconds, retentionDays, since, events, truncated}`: events newest first and by port within a
  sample, a socket's closing before its opening, each the socket as recorded plus `kind`, `at`, `after`,
  `since` (first seen) and `baseline`, placed and graded by `placeListeners` against the host's network
  and firewall as they are now; `stalled` says the latest sample is over three intervals old by the
  server's clock. `GET /ports` adds `firstSeen` to a socket the history saw open while it was recording
  and whose recorded owner still holds it (`PortRecorder.FirstSeen`), and lists the sockets undated,
  with a warning in the log, if the history cannot be read. Tests: `TestDiffListeners`,
  `TestPortRecorderKeepsWhatOpenedAndClosed` (a fake clock and lister through a restart gap, a failed
  walk, the window, the limit, pruning and `stalled`), `TestPortRecorderRecordsARealSocket` (the real
  kernel tables), `TestListenerObservationsKeepOneOpenStretchPerSocket`, and the API's
  `handlers_ports_history_test.go`.
- **HTTPS from the site form** (`site_certs.go`, `site-certificate.tsx`). `GET /certificates/covering?domains=`
  (system.admin, since it returns the remembered contact email) lists unexpired certificates covering
  every domain (a wildcard covers one label), each paired with its key the way deployment resolution
  pairs one, plus `certbot.email` from settings and the managed webroot. `POST /certificates/issue` stores
  the email it was started with under `certbot.email` and takes `dryRun` (`certbot --dry-run`). A site
  saved with `managedAcme` that answers plain HTTP itself (not TLS-forced, not a redirect, whose
  server-level `return` runs before location selection) renders `location ^~ /.well-known/acme-challenge/`
  rooted at the managed webroot with `allow all; auth_basic off;`, so neither an allow list, a password
  nor the exploit regexes block the validator. "Get a certificate" saves the site first when its file
  does not serve that path (as it is live for an edit, over plain HTTP for a new site), issues over
  webroot (optionally after a dry run), then saves it with HTTPS on; when no save was needed it only
  fills the paths. Wildcards, redirect sites and sites nginx does not read are not offered issuance.
- **Site builder** (`sites.go`, `sites_render.go`, `sites_parse.go`, `sites_apply.go`). `SiteSpec` is our
  shape, not nginx's, for the reason `ContainerSpec` is not `container.Config`; rendering happens **on the
  server** so a spec has one meaning, and the output is hand-written rather than templated because order
  carries meaning to whoever maintains the file after this dashboard is gone. `SaveSite` (`ApplySite` is
  its positional form) puts the **symlink in before `nginx -t`** — a new file in `sites-available` is not
  in the include tree, so the test has nothing to say about it — and undoes both together on failure.
  What a save does, and says, beyond that:
  - **The link is left as it was unless the save asks to enable.** The site form used to post
    `enable: true` for every save, so fixing one field of a disabled site put it back on the internet.
    `POST /proxy/sites/` takes `enable: "enable" | "keep"`; the old `true`/`false` still parse, and
    `false` always meant keep (it declined to make a link and never removed one). A site that has a link
    keeps it, relinked to the file being saved, and `SiteResult.enabled` says which it is.
    `GET /proxy/sites/{name}` and every preview say whether nginx reads the file (`enabled`, from
    `SiteFile`: linked into sites-enabled under its own name or any other — a site linked as
    `sites-enabled/010-app` is enabled, and a save neither stages nor makes a second link for it, which
    had nginx read the file twice and report it conflicting with itself — or in conf.d with a name ending
    in `.conf`) and whether it is in conf.d (`confd`). The form's footer follows them: a disabled site
    offers **Save (stays disabled)** (`keep`, no reload) and **Save and enable** (`enable`, reload)
    instead of a "Save and reload" that did neither, and only the first when its name's link is another
    file's or the site is in conf.d. The listing (`nginxVHosts`) likewise calls a site enabled only when
    its name's link names its own file (`enabledElsewhere`), so it no longer says "serving" beside a form
    that says the link is another site's; a site enabled only under another name is still listed as
    disabled there, which is the listing's and the toggle's to change. A `sites-enabled/<name>` that is a
    file of its own (a `cp` where a link was meant) is listed as serving, since nginx serves it
    (`servedCopy()`). `SiteFile.ServedCopy` reaches the form as `servedCopy`, and the form says nginx
    serves that file and not this one, and offers only **Save** (`keep`, no reload). The save's result
    carries `servedCopy` and a note that it changed nothing nginx serves and was not tested, where it
    used to say "It stays disabled" of a name nginx answers (`TestSaveSiteSaysNginxServesAFileOfItsOwn`).
  - **A conf.d site is saved where the listing found it.** A conf.d file is switched off by a name that
    does not end in `.conf` (`app.conf.disabled`, or `app`). `siteTarget` used to add the suffix to
    every name, so saving that site as "stays disabled" wrote `app.conf.disabled.conf` beside it — a copy
    nginx reads. `confdSite` now targets an existing file of exactly the name, `ReadSiteSpec` keeps the
    `.conf` in a spec name when a file of the shorter name exists beside it (so `app` and `app.conf` are
    two sites, each saved to itself), and `DeleteSite` removes the file the listing names. A save that
    asks to enable such a file is refused: enabling it is renaming it.
  - **A site saved disabled is tested as it would be enabled, in a copy of the configuration**
    (`site_trial.go`). Without that `nginx -t` never read the file, so a disabled site was saved untested
    and failed on the day somebody enabled it. The live tree is never touched: `stageTrial` writes
    `<nginx dir>/.jd-trial-*.conf`, nginx.conf byte for byte except that its include of `sites-enabled`
    (or of `conf.d` for a conf.d site) points at `.jd-trial-*.d`, which links every entry of the real
    directory plus the site under the name enabling it would give it (`<name>`, or `<file>.conf` in
    conf.d), so nginx sorts it where enabling would put it. `nginx -t -c` and, for a conflict's order,
    `nginx -T -c` run on the copy; the copy sits beside nginx.conf so relative includes resolve the same,
    and its paths are renamed back to nginx.conf and the real directory in the result. Linking the site
    into the live `sites-enabled` for the length of the test, as the save first did, raced everything that
    runs nginx without the service lock — `POST /proxy/reload` and `/proxy/test`, the vhost toggle, a site
    delete, a stream save, certbot's hook, systemctl: a reload in the window served the disabled site until
    the next reload, and a broken one failed unrelated reloads with 422
    (`TestLiveReloadsBesideADisabledSaveNeverReadIt` runs reloads beside disabled saves, and
    `TestADisabledSaveNeverPutsTheSiteInTheLiveConfiguration` records what the live directory held during
    every nginx the save ran). Nothing the test finds refuses the save — nginx does not read the file —
    and the result is what enabling it would meet (`testedAsEnabled`): `validation` with nginx's objection
    placed at its file and line, `conflicts` worded as what enabling would do, and `testWarnings`. A reload
    follows only a test that passed. The copy assumes nginx loads `<nginx dir>/nginx.conf`, as the
    htpasswd and stream checks do. It is untested, with `validation.note` saying why, when its name's link
    is another file's, when nginx.conf does not include the directory itself or its pattern would not read
    the name, or when nginx.conf cannot be copied.
  - **What still races an outside reload.** An enabling save, an edit to an enabled site, `Validate` and
    the config editor's write put the file (and an enabling save its link) in the live tree for the length
    of their test and take it back if the test fails: nginx has nothing else to test a file it reads. The
    reload and test endpoints do not take the service lock, so one landing in that window reads the
    candidate — refused over a broken one, or loading one the save is about to take back until the next
    reload.
  - **A name another server block also claims is refused when the save changes who answers it**, 409
    `name_conflict` with `ConflictSummary`'s sentences. `nginx -t` passes a second claim on a name with only
    `[warn] conflicting server name … ignored` and answers from the **first** block it reads, so the save
    could as easily take a working site's domain as leave its own unreachable. The conflicts are nginx's own
    warnings for this site's names on the addresses its file listens on — nginx already knows a wildcard
    `:80` and `127.0.0.1:80` are separate. `orderConflicts` then reads which claim wins from `nginx -T`
    (`dumpNginx` under the held lock, or the trial's dump for a disabled site, then `NginxTree`, so
    `sites-enabled/*` sorted, a `conf.d` include before or after it and a block inline in `nginx.conf` all
    fall where nginx reads them),
    names the other claim's site from that tree, and sets `effect`: `ignored` (the other keeps the name:
    "nginx answers app.example.com on 0.0.0.0:80 from legacy, which it reads first, and ignores this site's
    claim."), `takes` (this site sorts first and takes it: "Saving anyway takes … from legacy, since nginx
    reads this site first.") or `keeps` (this site already answered the name before the save — read from the
    file as nginx loaded it — and still does). A `keeps` conflict alone is **not** refused: an edit to the
    site that is serving a name was refused in the name of the site nginx ignores. When the order cannot be
    read the conflict has no `effect` and says only that both claim the name. The file and link are taken
    back on a refusal; `allowConflict: true` saves anyway and the result lists `conflicts`, which the toast
    words by `effect` (checked against a running nginx in `TestLiveNameConflictSaysWhichSiteNginxAnswers`).
    `ValidateSpec` refuses a domain listed twice, which nginx warns about the same way. Deployment cutovers
    (`applySiteLocked`) are not refused over a conflict.
  - **A reload that fails after a clean test is a saved site**, 200 with `reloaded: false` and
    `reloadError`, where it was a 400 "Not applied" over a file that was written and linked. The toast
    says nginx did not pick it up and to start or reload it, never what nginx is serving: the usual cause
    is an nginx that is not running (`open() "/run/nginx.pid" failed`), which serves nothing. The
    deployment cutovers still get the error, since their recovery is built on it.
  - **A reload is proven from the master's side** (`reload_proof.go`). `nginx -s reload` returning 0
    says only that the signal was delivered; the master then reads the files and either opens what they
    listen on and replaces every worker, or logs `[emerg]` and keeps serving its old configuration. The
    case that matters is a listen address another process holds — `nginx -t` passes it, because its test
    ignores `EADDRINUSE` — where the master logs `bind() … failed (98: Address already in use)` five
    times, half a second apart, then `still could not bind()` (checked against nginx 1.26.3). Every
    reload a save, a switch, a link removal, a bulk change or the engine's own **Reload** asks for now
    goes through `reloadProven`: `markLoad` notes the running master (`markReload`: found by the
    configuration it was started on, as the stream watch finds it), its workers, the end of its error
    log and the SHA-256 of each file the change wrote (the site's file and its `sites-enabled` link);
    `awaitLoad` then watches for `loadWait` (3 s). `LoadProof.state` is `loaded` when every worker
    serving is one that was not there before (`replacedAll`: one new worker beside old ones is a
    crashed worker replaced on the same load, not a load), each file still holds the digest the change
    wrote (`held`), and nginx holds every socket the site's `listen` lines ask for (`siteBinds`, read
    as the stream watch reads them; `listening`); `refused` with nginx's first `[emerg]` line (`error`)
    when the master logged one after the signal — except that a `bind()` failure is a refusal only
    once the master logs `still could not bind()`: the watch reads its attempts on until it does
    (`awaitBindAttempts`, so its later attempts are never read as the next reload's), and a socket
    freed between attempts is bound on the next and the reload read as any other load, `unconfirmed`
    with the first failure in its note when neither is seen; `unconfirmed` with a `note` when none of
    that was seen in time, a file was written again before the load, or nginx
    loaded without the site's socket; `unchecked` when the running nginx could not be read. A save
    whose reload the master gave up **over one of the site's own sockets** is put back exactly as a
    refused test puts it back (file, link and `.bak`) and answered 409 `load_refused` with
    `SiteLoadRefusedError` naming the program holding the port from the host's listener list (audited
    `result: rolled-back`): left in place, the file would fail every later reload on the host. Any
    other refusal keeps the valid file and is `reloaded: false` with `reloadError` ("nginx did not take
    the reload up: …"). The engine's reload answers a refused load as 502 `load_refused`
    (`ErrLoadRefused`, audited `result: refused`) and carries `loadProof` otherwise; every reload
    toast reads it (`reloadToast`: the command palette, the served-certificate and TLS report reloads,
    the import outcome), so none says "nginx reloaded" of a load the master was not seen taking up. The site form says
    "is live" only for `loaded` (or a backend that sends no proof), "saved and reloaded" with the note
    otherwise, and "nginx refused the reload" with nginx's words (`load-proof.ts`, `site-save.ts`); the
    engine's Reload toast and the pending strip read the same. `TestSaveSiteKeepsASiteNginxBoundOnALaterAttempt`
    keeps a site whose port was freed between the master's attempts. `TestLiveReloadIsProvenFromTheMaster`
    runs the host's nginx binary on a private prefix: a loaded save is proven from the real master's
    workers and sockets, a save onto a port the test holds is refused by the master, put back, named as
    held by the test process, and the previous site still answers.
  - `testWarnings` are the test's warnings placed in the site's own file, so "is live" is said only when
    nginx had nothing to say about it: "Saved with 2 warnings" lists them by line, and alongside a
    conflict they are listed after it rather than dropped. A conflict warning names no file, so it is
    never among them.

  Renderer details:
  - The ACME challenge location goes **above** the catch-all redirect, or renewal silently stops and
    nobody finds out for sixty days.
  - `http2 on;` is a directive, not a `listen` parameter (nginx 1.25 warns on every reload).
  - **`proxy_pass` is the upstream as typed, less a path that is the location's own.** Its path is how
    nginx is told to replace the location's prefix: `/api/` to `http://127.0.0.1:4000/` sends `/api/users`
    as `/users`. Trimming its slash sent `/page` to `http://…/app/` as `/apppage`. When the path and the
    upstream disagree about a trailing slash, `SpecWarnings` says what a request becomes. nginx forwards
    the request exactly as sent **only to an upstream without a path**; to one with a path it sends the
    path decoded, so `%2F` arrives as `/` (npm/Verdaccio scoped packages, GitLab project paths). An
    upstream path equal to the location's prefix — `http://x/` pasted on `/`, `http://x/pkg/` on `/pkg/`
    — swaps nothing, so it is left off and the request goes through raw; any other path is kept and
    `SpecWarnings` says `/a%2Fb` reaches the application decoded (both checked against a running nginx).
    A `unix:` upstream is written `http://unix:…`, the only spelling nginx accepts, and read back as
    `unix:`.
  - **A folder is served at its path**: `alias <folder>/;` in `location <path>/`, both ending in a slash
    so a neighbour such as `/assets-private` is never read through `/assets` (checked against a running
    nginx). `root` appended the path, so `/assets` with `/var/www/assets` looked in
    `/var/www/assets/assets`. A location read back from a file that used `root` keeps it
    (`SiteLocation.RootMode = "root"`), since rewriting it as the folder itself would move every file it
    serves; a hand-written `alias` is read back as a folder.
  - **A static site can be a single-page app** (`SiteSpec.SPA`): its `location /` answers a path with no
    file of its own with `/index.html` (`try_files $uri $uri/ /index.html`) instead of 404, so a deep link
    or a reload reaches the app's router; the probe blocks still refuse `/.env` (checked against a running
    nginx). It is read back from that `try_files` and only for a static site.
  - **Compression off is written `gzip off;`**: Debian's `nginx.conf` turns gzip on for the whole http
    block, and a site saying nothing inherits it. A managed file with no gzip line predates this and was
    written with the switch off; a hand-written one reads back as on.
  - **A site file owns its http-level objects** (`renderHTTPBlock`, above the first `server`).
    `sites-enabled/*` and `conf.d/*.conf` are both included *inside* `http {}`, so the top of a site
    file is http context: `map`, `upstream`, `limit_req_zone`, `limit_conn_zone`, `proxy_cache_path`,
    `geo` and `log_format` written there pass `nginx -t` (checked on nginx 1.26.3) and are deleted with
    the site. Their names are global, so each is `NginxIdent(site)` plus a suffix: a second
    `log_format` of one name fails the reload, and a `map` variable defined twice is decided by the
    last file read. `ParseSiteSpec` steps over these statements (and whatever is inside their blocks)
    rather than reading a map entry or a geo range as a server directive.
  - **A variable name is at most 46 characters** (`maxNginxVarLen`). nginx's default
    `variables_hash_bucket_size 64` takes no longer one on a 64-bit build, and one longer name fails
    `nginx -t` for the whole configuration ("could not build variables_hash"), so every site stops
    reloading. `NginxIdent` plus a suffix passed that for a site name with a dot from 21 characters (26 with
    only WebSockets on), and for every deployment route from environment 10 on (`just-dashboard-env-10.conf` + `_connection` is 47).
    `RenderNginx` therefore renders once under `NginxIdent` and, only when one of the dashboard's own
    `$jd_…` variables comes out too long, renders again with every variable and limit zone named from
    `NginxVarIdent`: the start of the ident and an FNV-32a hash of the whole name, short enough for the
    longest suffix (`_asset_immutable`). A file that already loads keeps every name it has, so nothing
    reads as edited by hand after an upgrade. All of a site's variables move together because the
    readers find a partner by swapping the suffix (`_maint` → `_maint_ip`, a zone's `_req` →
    `_limit_exempt`); the limit zones move with them because a zone's key is a variable named after it,
    and nginx refuses a reload that finds a live zone counting by another key. Upstream, cache-zone and
    `log_format` names are not hashed by length and keep `NginxIdent`. The forward-auth reader takes its
    variables under either ident. `TestEveryVariableFitsNginxsDefaultHash` renders every variable the
    form can name for the longest site name, and `TestLiveNginxTakesTheVariablesOfLongNames` passes
    them and environment 10's route through the real `nginx -t`.
  - **WebSockets use the standard upgrade map**, per site: `map $http_upgrade $jd_<id>_connection
    { default upgrade; '' close; }`, with `Connection $jd_<id>_connection` in each upgrading location.
    It replaces passing the client's `$http_connection` through, whose comment wrongly said a map had
    to be shared by every site. Both forms read back as WebSockets on (the `Upgrade` header is what
    the parser keys on), and the map is written only when a rendered proxy location upgrades.
  - **Access logs are timed by default** (`SiteSpec.logFormat`: `timed`, the default when empty, or
    `combined`). Timed is `log_format jd_<id>_timed` — combined followed by `rt=$request_time
    urt="$upstream_response_time" host=$host cs=$upstream_cache_status` — so a stock combined reader
    still reads every line and `accesslog.parseCombined` takes latency from `rt=`. An `access_log`
    naming any `jd_*_timed` format reads back as timed; a file with none, or with another format,
    reads back as `combined`, which the drift list already reports as the form writing its own
    `access_log`. The form offers the choice under Hardening beside the access-log switch.
  - **An `allow` list is fenced with `deny all`.** nginx stops at the first match and otherwise permits,
    so a site restricted to `10.0.0.0/8` was reachable from anywhere; expecting the operator to write the
    fence themselves into a box labelled "Deny" fails too, since `0.0.0.0/0` lets in every IPv6 client.
    Explicit denials render *above* the allows for the same first-match reason. One case cannot be fenced
    and is said rather than rendered anyway: nginx answers a `return` in the rewrite phase, before access,
    so an allow list does not restrict a redirect site.

  **`ParseSiteSpec` must round-trip every field the form writes**, or an edit deletes what it cannot read
  back — `Custom` and `BlockExploits` were the two that did not, so opening a site and pressing save
  removed the operator's directives and turned the probe blocks off. `customMarker` and
  `exploitDotLocation` are shared with the renderer and
  `TestCustomConfigurationSurvivesARoundTrip` renders/parses/re-renders; that test is what a new field
  needs. It skips the generated ACME location (reading it back emits it twice), reports whether the file
  carries our marker so the UI can say a hand-written file may not survive, and leaves an `allow`/`deny`
  inside a location where it is — hoisting it into the site-wide list applied one path's restriction to
  the whole site on the next save. A save over a file without the marker first keeps it as
  `<file>.bak`, which the form's notice promised while nothing wrote it. The save then returns that
  path as `SiteResult.backup`, and the toast names it. A later save of the now-managed file leaves that
  `.bak` alone. A save that is refused puts back the `.bak` that was there before, or none
  (`TestSaveSiteKeepsAHandWrittenFileAsBak`). New backups take the original file's permissions;
  replacing an existing backup cannot make its permissions broader (`TestSiteBackupsKeepRestrictedPermissions`).

  **What a save drops is listed before it happens** (`site_drift.go`). A save writes the form's reading
  of the file, so a statement the form has no field for — a `proxy_set_header` added by hand, a second
  `listen`, a location it cannot express, certbot's `include` — went with no word, from a managed file as
  much as from a hand-written one. `DroppedLines` parses the file and what saving a spec writes with
  `ParseNginxFile` and compares them statement by statement where they sit: each server block of the
  file against the written block it shares a name and the most statements with (several may share one:
  a site written as a `:80` block and a `:443` block is one block listening on both in the form, and
  loses nothing), each block against its twin by name and arguments, and the statements in it as a
  multiset. Spacing, quoting and comments do not count, and the spellings the form writes differently
  with the same effect are the same statement: `listen 443 ssl http2` (it writes `http2 on;`),
  `listen *:80`, a `proxy_pass` whose path is the location's own (see the renderer below), a folder
  `alias` and its location without their trailing slashes, and a `server_name` split over several lines.
  What is left is every statement not written back, with its line and its text — its own lines of the
  file, comment and all, or the statement written out on one line where it shares one — and where it
  sits. `SiteDrift` is the form's view of that: the statements the form's own reading of the file leaves
  out, less those the current spec writes back, so an edit made in the form is a change rather than a
  dropped line and a line moved into the extra configuration stops being one. `GET /proxy/sites/{name}`
  carries it for the file as read (`dropped`, `lossless`), and every preview for the spec it is given
  when that site's file exists; both are left out when the form cannot write the file at all, which its
  preview then says.

  A dropped line is **movable** when it sits directly in the server block the form writes and the extra
  configuration, which goes there, keeps it as it is. Not one inside a location (a `proxy_set_header` in
  the server block is not inherited by a location that sets its own), in another server block or outside
  them all, and not one of a name the form writes there itself: nginx refuses most of those twice
  (`http2`, `ssl_session_timeout`, `ssl_session_tickets`, `ssl_prefer_server_ciphers`, `gzip`,
  `gzip_vary`, `client_max_body_size`, `root` and the `auth_basic` pair, checked against nginx 1.26.3 as
  `refusedTwice`) and applies the rest twice — a second access log, a second `X-Frame-Options`.
  `add_header` is compared by header and `listen` by address. An `include` is read for the names of the
  directives it sets (`includedNames`: relative to the nginx directory, a glob without its dotfiles,
  regular files under 1 MiB, and only the names leave, only as far as the reason), since certbot's
  `options-ssl-nginx.conf` sets `ssl_session_timeout`, which the form writes, and moving it fails
  `nginx -t` with "is duplicate" (`TestLiveAnIncludeThatRepeatsTheFormsTLSStaysOut`); its `reason` says
  so. The same statement movable from two places moves once. `TestLiveMovedLinesKeepWhatTheyDid` has a
  real nginx answer on a second port with a header added by hand, moves both into the extra
  configuration, saves and reloads, and checks both still hold — and that the `proxy_set_header` it could
  not move is no longer sent, as it said.

  **A draft is of one version of the file.** `GET /proxy/sites/{name}` and every preview of an existing
  file carry `digest`, the file's sha256 (`ContentDigest`). The form keeps it with the draft, and the
  save sends it back as `baseDigest`: `SaveSite` refuses a file that is no longer that version, or is
  gone, with 409 `site_changed` (audited as `changed_on_disk`) before it writes anything, so another
  tab's edit or the raw editor's is never silently written over
  (`TestSaveRefusesAFileThatChangedSinceTheFormReadIt`, `TestSiteSpecSaysWhatASaveDropsAndWhichVersionItRead`).
  Deployment cutovers send none and are not checked.

  **`POST /proxy/sites/preflight {spec}` (system.admin) makes the checks `nginx -t` cannot**
  (`proxysvc/site_preflight.go`), answering `{checks:[{id,level,title,detail,blocking,domain?}]}`:
  certificate and key exist, parse and match (`keyMatchesCertificate`), the certificate covers every
  domain (`certificateCoversAll`) and its days left; each password file exists, has users and is
  readable by nginx's worker group (`nginxWorkerGID`, mode/owner from the host); a static root and
  location folders exist, with an `index.html` where one is expected; each of the spec's own upstreams
  accepts a TCP connection and answers a HEAD within 2s (never any other target, since it is outbound
  traffic the caller aims); and DNS for every domain (`CheckDomainDNS`). Existence and mode come from
  one `stat -L -c` argv run through `hostexec.CommandOnHost`, because `/var/www` is not shared with the
  container; content (certificate, key, htpasswd) is read in the container and never returned. Each
  input is held to its own ValidateSpec pattern and skipped if it fails it, so a half-filled form still
  gets answers. Only files that break the site as saved are `blocking` (missing/mismatched/expired
  certificate or key, uncovered domain, missing/empty/unreadable-by-root-owned password file, missing
  folder, SPA without index.html); a closed upstream and DNS elsewhere are warnings, since a site is
  often saved before its app or its record exists. The form runs it 800ms after typing stops, shows it
  in a Checks tab and as per-domain DNS lines under Domains, and holds Save while a blocking check fails
  until the operator allows it; a new blocking check holds it again.

  **`POST /proxy/sites/preview` also says where the file goes**: `path` is what a save writes
  (`SiteFile`, through the same `siteTarget` the save uses: `sites-available/<name>`, or
  `conf.d/<name>.conf` on a conf.d host) and `exists` whether a file is there. Both are left out when the
  host has neither directory. A new site's name follows its domain, so it can land on an existing site
  without the operator typing it; the form refuses that at the field before the save refuses it.
  `enabledElsewhere` is the other way a name is taken: a `sites-enabled/<name>` that enables a different
  file (a hand-written site linked under its domain rather than its file name, or a link to a file that is
  gone) or is a file of its own, said as a sentence (`enabledElsewhere()`, which follows relative and
  chained links, so a link naming this very file, even before it is written, is not in the way). The save
  refuses a new site there, and an edit asked to enable itself there, before `linkEnabled` runs: it used to
  replace the link before `nginx -t`, so the test never saw the two sites claim one name and the other
  site silently stopped being served. An edit that keeps its link state leaves such a link alone and
  reports itself not enabled (`TestSaveSiteLeavesAnotherSitesLinkAlone`,
  `TestSiteFileSaysWhatHoldsTheNamesLink`). `DeleteSite` had the same bug: it removed
  `sites-enabled/<name>` wherever it pointed. Now it removes the link only when the link names the
  file being deleted, or points at nothing (a dangling link enables nothing and fails the next reload).
  It deletes the file and leaves any other link where it is. A file of its own in sites-enabled makes
  deletion fail before changing either file: nginx serves that copy and deletion cannot silently remove
  it without a backup (`TestDeleteSiteRefusesToRemoveServedCopy`). A name held only by another site's
  link is "no such site", and the error says what holds the name
  (`TestDeleteSiteLeavesAnotherSitesLinkAlone`). Every other sites-enabled entry that resolves to the
  deleted file goes with it (`linksTo`, skipping dotfiles as nginx's include does). Before, a site
  linked as `010-app` lost only the `sites-enabled/<name>` it never had, and the dangling `010-app`
  failed every later `nginx -t` and reload (`TestDeleteSiteRemovesALinkOfAnotherName`, and the real
  nginx run in `TestLiveDeletingASiteLinkedUnderAnotherNameLeavesNginxValid`).

  The form's side of this lives in pure modules beside `site-form.tsx`, tested with bun.
  `site-draft.ts` holds the draft's rules. A draft survives a trip to another page while its file is
  still the version it started from — the server's copy used to replace it on every open — gives way to
  the file when nothing in it was changed, and otherwise the form asks, with **Reload from disk** or
  **Keep my draft**, as it does after a save refused with `site_changed` (saving waits for the answer).
  Unsaved changes are the draft against the spec it started from, compared as sent: they draw an
  "unsaved" tag, and closing the sheet with them — Escape, the overlay, the close button, one funnel —
  asks **Discard changes?**; a trip to another page keeps them instead. Ctrl/Cmd+S runs the footer's own
  command. The dropped lines are drawn as a diff at their own line numbers, with the reason beneath for
  a line of the server block that cannot move, **Move N lines into Extra configuration** and **Edit the
  raw file** (the config editor over the form, at the first dropped line; saving there reads the file
  again). A hand-written file the form reads whole says so and that it is kept as `<file>.bak`. A
  **Changes** tab diffs the file on disk against the preview, which is what the save writes over it.

  `site-identity.ts` works a new site's file name and certificate paths out of its whole first domain on
  every change, for each part not typed by hand (keeping the first value named a site typed as
  app.example.com "a", after one keystroke), and checks a typed name against the server's rule; the form
  shows the name as its own **File name** field (read-only for an existing site), with **Match the
  domain** and **Use the domain's certificate** to follow the domain again. `site-save.ts` builds the
  request — `keep` for an existing site unless the operator chose **Save and enable**, HSTS only with
  TLS, since the switch is drawn only under HTTPS
  and its hidden default warned on every plain-HTTP site, and `spa` and `permanent` only with the kind
  that draws them — and turns the result into what the toast says, a disabled site's included ("saved;
  enabling it would fail nginx's test" with the line, "saved with a name conflict once enabled"). A
  refused conflict is a Notice in the form with **Save anyway**, which repeats the save that was refused,
  enabling included. A new form opens with the keyboard in **Domains**, and the presets come
  after the file name, so the one field every site needs is in view on a phone.

  A new site can **start from a preset** (`site-presets.ts`: Node.js app, single-page app, Grafana, Home
  Assistant, Docker registry, MinIO, Jellyfin, redirect), a `SiteSpec` partial laid over the form. It
  keeps the operator's name, domains, certificate, access settings and any upstream they typed, and
  swaps only its own lines in the extra configuration (the registry and MinIO turn
  `proxy_request_buffering` off there and lift the upload limit with `client_max_body_size 0`). A
  preset's card stays chosen, with its note about the application's own settings, until the operator
  picks another kind by hand: `leavePreset` then takes out the preset's extra lines and puts back the
  default for each field it set that still holds its value. Each preset is checked in as `proxysvc/testdata/presets/<id>.json`, which `site-presets.test.js` holds equal
  to the TypeScript; `TestPresetsRenderAndReadBackAsThemselves` renders every one plain and over HTTPS
  and reads it back unchanged, `TestLivePresetsPassNginxTest` puts all of them through the host's
  `nginx -t` with no warning, and two more live tests prove the single-page fallback and a 64 MB upload
  through the large-upload presets (which the Node.js preset refuses with 413).

  **Send it to** is an `UpstreamPicker` (`upstream-picker.tsx`) over `upstream-options.ts`: free text,
  plus what `GET /ports` and `GET /docker/containers/?all=false` show — running containers by the port
  they publish (a port they do not publish is listed, cannot be picked, and says to publish it on
  127.0.0.1, since its bridge address changes with every recreate), then loopback and wildcard sockets;
  443 and 8443 are offered as `https://`.
  nginx's own sockets, UDP, sockets bound to one other interface and ports that answer something other
  than HTTP (SSH, mail, databases, Docker's API) are left out. A loopback upstream nothing listens on is
  warned about under the field, not refused: nginx answers 502 until the application starts. The blank
  form's own default (`127.0.0.1:3000`) is not warned about until somebody sets the upstream — by
  typing, picking, a preset or a link — or the site already had it. Both lists are polled while the form
  is open, so an application started half-way through appears.

  **`/proxy/sites?new=1&upstream=<url>&domain=<name>`** opens the form on a new site from elsewhere in
  the dashboard (`site-link.ts`, called from the Sites page). It is read once, taken off the address, and
  only for `system.admin` on a host with nginx; the values go in as the link spelled them, so the form and
  preview say what is wrong with either. `tests/browser/proxy-site-builder.spec.ts` checks each state
  against mocks from `tests/browser/fixtures/proxy/siteform.ts`.

  **Maintenance and error pages** (`site_pages.go`, form section "Pages & files",
  `page-editor.tsx`). `SiteSpec.Maintenance{On, RetryAfter, BypassFrom}`, `ErrorPages` (404, 502, 503,
  504) and `InterceptErrors` (proxy sites: `proxy_intercept_errors on`, so the application's own
  responses with those codes get the pages too). Pages are files at `<nginxDir>/jd-pages/<site>/<page>.html`
  (0644; the embedded `proxysvc/pages/*.html` are written for any page a save needs and the site lacks),
  served from exact `internal` locations `= /__jd/<page>.html` by `alias`, reached with `error_page` to
  that path — a path, not a named location, because nginx turns the request into a GET only on the
  way to a path, and a POST to a static page is 405. `PagesDir` is set by the service, never read from a
  request. Maintenance writes a `geo $jd_<id>_maint_ip` above the server (the bypass list) whenever it is
  configured, and while on a server-level `set`/`if` pair that returns 503 except for the ACME challenge
  and the maintenance page itself: a variable `set` on every pass rather than a `map`, whose value nginx
  keeps across the error page's internal redirect. The maintenance location carries `Retry-After` and
  repeats the server's security headers (its `add_header` stops them inheriting), and every page
  location turns `auth_basic` off. While maintenance holds 503 the site's own 503 page is not routed,
  but its location stays written, which is how the parser reads `ErrorPages` back. `VHost.maintenance`
  reports the switch on the Sites card. Routes: `GET /proxy/sites/{name}/pages/{page}` (any signed-in
  account; the default when the site has no file), `PUT` the same (`system.admin`, at most 256 KiB,
  no reload, audited `proxy.site.page`), and `POST /proxy/sites/{name}/maintenance`
  `{on, retryAfter?, bypassFrom?}` (destructive gate, like disabling; audited
  `proxy.site.maintenance`), which saves the form's reading of the file with the switch changed and
  reloads — refused (409 `not_managed`) for a file the form did not write or would drop lines of. Page
  paths are refused unless the site folder resolves directly under `jd-pages` inside the nginx directory
  and the file is not a link.
  `SiteMaintenance.Switch` is the same maintenance behind a file instead of `On`, which is how a
  deployment's route carries it: the check is always written, behind `if (-f
  <pages>/maintenance.on)`, and a request it holds is rewritten to an internal `= /__jd/maintenance`
  location that returns 503 with the maintenance page as its own `error_page`, so the server's
  `error_page 503` stays the site's 503 page whichever way the file has it. nginx tests the file on every
  request, so turning it on or off is the file (`SetDeploymentMaintenance`) and needs no reload. The
  parser reads the switch back with `On` false (the file decides), `VHost.maintenance` reads the file
  (a Docker Caddy route's card reads its file inside the ingress the same way),
  and the access explainer takes no maintenance layer while the file is absent and the site's own
  judgement, owned by the switch, while it is there. `ReadDeploymentPage`/`WriteDeploymentPage` keep a
  deployment's pages the same way under its route's name, or in the Docker Caddy ingress
  ([implementation](../deployments/implementation.md), [Caddy ingress](../deployments/caddy-ingress.md#maintenance-and-error-pages)).

  **Limits** (`sites_limits.go`, form section "Limits", a per-path override under the extra paths).
  `SiteSpec.Limits{Request{Rate, Burst, NoDelay, Key}, ConnPerIP, ExemptFrom, DryRun}` and
  `SiteLocation.RateLimit`. Rate is `^[1-9]\d{0,5}r/[sm]$`, Key is empty (per address) or `ip_path`
  (`$binary_remote_addr$uri`), exemptions are IPs/CIDRs. Above the server: `geo $jd_<id>_limit_exempt`
  (listed addresses 1), and per zone a `map` of it to `$<zone>_key` (`""` for exempt, which nginx does
  not count) plus `limit_req_zone`/`limit_conn_zone` — zones `jd_<id>_req`, `jd_<id>_conn` and
  `jd_<id>_req_p<n>` for the n-th extra path. The zone key is always that map variable because nginx
  refuses a reload that finds a live zone counting by a different key. Server level: `limit_req`,
  `limit_req_status 429`, `limit_conn`, `limit_conn_status 429`, and with DryRun `limit_req_dry_run on`
  and `limit_conn_dry_run on` (logged as "dry run"); a path's own `limit_req` replaces the site's there,
  as nginx inherits it. Refused for redirect sites (`return` runs before limits apply) and exemptions or
  log-only with nothing limited. Every limit counts the address nginx sees, so a warning says so unless
  the extra configuration sets `real_ip_header`; the form cannot tell whether a CDN is in front.
  `VHost.rateLimited` puts "rate limited" on the Sites card. The private-prefix nginx test loads the
  rendered zones and confirms that a third request over a `1r/m` limit answers 429.

  **Caching** (`sites_cache.go`, form section "Caching", verb "Purge cache"). `SiteSpec.StaticCache{MaxAge,
  Immutable}` (not for redirects) renders `map $sent_http_content_type $jd_<id>_asset_expires` (CSS, JS,
  images, fonts, woff/wasm → MaxAge, default `off`) with server-level `expires` on it, so pages are never
  kept; Immutable adds a second map and `add_header Cache-Control $jd_<id>_asset_immutable` (empty, so
  unsent, for everything else). `SiteSpec.ProxyCache{MaxSize, Valid, ServeStale, CacheCookies}` (proxy
  sites only, refused unless `Buffering` is on — nginx never caches an unbuffered response; a path with
  buffering off is warned about as uncached) renders `proxy_cache_path /var/lib/just-dashboard/nginx-cache/
  <name> levels=1:2 keys_zone=jd_<id>_cache:10m max_size=… inactive=7d use_temp_path=off` and at server
  level `proxy_cache`, key `$scheme$host$request_uri` (so domains never share entries), `proxy_cache_valid
  200 301 302`, `proxy_cache_lock`, and with ServeStale `proxy_cache_use_stale error timeout updating
  http_5xx` + `proxy_cache_background_update`. `proxy_cache_bypass`/`proxy_no_cache` always carry
  `$http_authorization` (not a toggle: nginx would otherwise store one user's authorised page) and
  `$http_cookie` unless CacheCookies (which warns). `add_header X-Cache-Status $upstream_cache_status
  always` sits with the site's other headers (`writesHeaders`), which the maintenance page repeats.
  `SaveSite` makes the cache root (0755); nginx makes the site's folder at load and chowns it to its
  worker. `GET /proxy/sites/{name}/cache` (the read gate; size only) walks it for blocks on disk and
  files; `DELETE` (system.admin + destructive, audited `proxy.site.cache.purge`) empties it through an
  `os.Root` and keeps the folder, refusing a linked root or site folder. There is no cache_purge module:
  nginx takes a missing file as a MISS, but its shared-memory size accounting only catches up as the
  cache manager evicts. `VHost.cached` shows the verb. A real-nginx test confirms MISS then HIT,
  Authorization and Cookie bypasses, and a MISS after purge.

  **A site's controls as its service policy, and their measured effect** (`site_controls.go`).
  `SitePolicy` reads the site's file (by its listed name) for the request limit (`limit_req` with its
  zone's rate from the file or the loaded tree, burst, `nodelay`), the connection limit, the response
  cache (`proxy_cache`), browser caching of assets (`expires`), HTTP/2 (`http2 on` or a `listen … http2`)
  and HTTP/3 (`http3 on` or a `quic` listen), each with paths that set their own, and says per control
  whether the build nginx runs has what it needs (`nginx -V`: the standard limit, proxy modules unless
  configure left them out with `--without-`, now kept as `streamNginxBuild.without`;
  `--with-http_v2_module` and `--with-http_v3_module`): `built-in`, `module`, `missing` or `unknown`.
  `GET /proxy/sites/{name}/policy` serves it to every account (the site read already shows the file).
  The connection investigator reuses it as the **service policy** of each site that forwards to the
  destination port (`netpath/policy.go`), with the limitation that a direct connection to the port meets
  none of it. `POST /proxy/sites/{name}/controls/verify {path, asset}` (`system.admin`, audited
  `proxy.site.controls.verify` with the request count) measures them against this nginx on loopback —
  or, for a site whose listens on the port all name one address, at that address (`dialAddress`), so
  nginx answers from the block that address picks rather than a catch-all on the wildcard, and only
  where it is one of this host's own (`hostAddress`; otherwise nothing is sent) — naming the site in
  SNI and Host as the request tester does (`localTarget`: only an enabled nginx site
  that takes its first exact name on its first TLS port, else its first plain one, and refused when
  another enabled site wins that name on the port, whose controls would be measured instead): HTTP/2 from a
  browser's ALPN offer (`verified`, `not-effective` when the site asks for it and nginx does not
  negotiate it, or `not-configured` noting that another server block on the address turned it on);
  HTTP/3 from a QUIC Version Negotiation answer on the UDP port (`probeQUIC`) and an `h3` in the HTTPS
  answer's Alt-Svc; the response cache from two GETs of the path and `X-Cache-Status` (`HIT` second, or
  why not: a cookie, `Cache-Control` private/no-store/no-cache, a bypass, an uncacheable status;
  `not-measured` without the header); browser caching from the asset path's `Cache-Control` max-age
  (`not-measured` without one); and, last, since every other check's requests count against it, the
  request limit from burst + 2 requests at once, at most 40, counting the site's `limit_req_status`
  (a dry run is verified by refusing nothing; `not-measured` when the burst needs more than 40, when
  without `nodelay` nginx would hold the check over five seconds, or when the address the requests
  come from — loopback, or the site's own address — is on the site's exempt list). The connection limit is `not-measured`: `limit_conn` counts requests still being
  answered, which a short request does not hold. The requests reach the application. The site page's
  **Controls** panel (`site-controls-panel.tsx`, `site-controls.ts`) lists the policy and, for an
  administrator, measures it with a path and an optional asset path. Tests:
  `TestSitePolicyReadsTheFormsControlsAndTheBuild`, `TestVerifySiteControlsRefusesBeforeSending`,
  `TestControlHelpers`, `TestProxyLayerCarriesTheSitesServicePolicy`, and `TestLiveSiteControlsAreMeasured`,
  where the host's nginx binary on a private prefix serves a TLS site with HTTP/2, a QUIC listen
  advertised by Alt-Svc, a response cache and CSS caching, and a site limited to one request a minute
  with a burst of one, and every control is verified from nginx's answers. The site is written for the
  installed build: HTTP/2 goes in the listen where nginx predates `http2 on;` (1.25.1), and a build
  without `http_v3_module` gets no QUIC listen, so HTTP/3 must read as not configured there.

  **Visitor address** (`site_realip.go`, form "Visitor address"). `SiteSpec.RealIP{Source, Trusted,
  Header, CloudflareOnly}`: `cloudflare` renders `include <nginxDir>/jd-realip/cloudflare.conf;` +
  `real_ip_header CF-Connecting-IP` at server level; `proxies` renders `set_real_ip_from` per `Trusted`
  entry (IP/CIDR, a `/0` refused), `real_ip_header <Header>` (a header name) and, for X-Forwarded-For,
  `real_ip_recursive on`. It sits before the maintenance check, so address lists, limits, maintenance
  bypass and the log all see the visitor. `CloudflareOnly` (Cloudflare only, warns) adds an http-level
  `geo $realip_remote_addr $jd_<id>_edge {default 0; include …/cloudflare.geo;}` and `if ($jd_<id>_edge
  = 0) { return 444; }` in the site server and its redirect server. PROXY protocol is not offered: nginx
  ORs `proxy_protocol` across every server on one address:port, so one site would switch it on for all
  of :443. The two files are owned (`# source=cloudflare|built-in fetched=<RFC3339>` header); `SaveSite`
  writes the built-in list when a Cloudflare site needs them and they are missing. `realIPFiles` refuses
  a `jd-realip` that does not resolve directly under the nginx directory and a non-regular file.
  `GET /proxy/realip/cloudflare` (any signed-in account; public ranges) reads the list;
  `POST /proxy/realip/cloudflare/refresh` (system.admin, audited `proxy.realip.refresh`) downloads
  cloudflare.com/ips-v4 and ips-v6 (10s, 64 KiB each, every line a masked CIDR of its family, v4 ≥ /8,
  v6 ≥ /16), writes both files, runs `nginx -t` and restores both on failure (422), then reloads.

  **Headers** (`sites_headers.go`, form "Headers"). `SiteSpec.Headers{Request, Response, Hide,
  FrameOptions, CSP, Permissions, CORS}`. Every value is rendered in double quotes and refuses `" ' \ ;
  { } $` and control characters; names match `^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`. A request header value may
  instead be one whole `$var`; it is written into every forwarding location after the fixed
  `proxy_set_header` lines (a location's list replaces the server's) and read back from `location /`.
  Request and Hide are proxy-only; Hide refuses what nginx already drops (Server, Date, X-Accel-*). CSP
  and Permissions-Policy are kept as parts (keywords unquoted: `self`, `none`, `nonce-…`), so the stored
  scalars never carry `;` or quotes; the renderer adds them. Custom response headers may not name one a
  control writes (X-Frame-Options, CSP, Permissions-Policy, HSTS, `Access-Control-*`, and whatever the
  security-headers switch or caches send; a deployment route's refusal of `Strict-Transport-Security`
  names its own HSTS control under Access and headers instead). All response headers go through `renderHeaders`, so the
  maintenance page's location repeats them. CORS (not on redirects): an http-level
  `map $http_origin $jd_<id>_cors` (exact origins → `$http_origin`, else empty, so no ACAO is sent) or a
  literal `*` (refused with credentials), a `map "$request_method:$http_access_control_request_method"
  $jd_<id>_preflight`, and a server-level `if ($jd_<id>_preflight) { return 204; }` — only real preflights
  are answered, before auth and limits; other OPTIONS reach the application. A proxy site hides the
  application's own `Access-Control-Allow-*` headers so browsers never see two.

  **Access options** (`sites_access.go`, form "Who may reach it", "Crawlers & hotlinking", "Pages &
  files"). `BasicAuthRealm` is now in the form. `SatisfyAny` writes `satisfy any;` and needs both an
  allow list and a password file; it is inherited by every location, so the exploit blocks then say
  `satisfy all;` (a password must not open them) and the internal page locations say `allow all;`.
  `AccessList` writes `include <nginxDir>/jd-access/<name>.conf;` (the access lists feature's
  contract; `SetAccessListDir` sets the folder, never a request) and is refused beside the site's own
  allow/deny/password/satisfy, since nginx refuses a second `auth_basic` and merges allow lists. A
  missing list fails `nginx -t`. `ClientCert{CAPath, Mode, PassSubject}` writes
  `ssl_client_certificate` + `ssl_verify_client on|optional` and is refused unless TLS with HTTP
  redirected (port 80 would serve without a certificate); `optional` needs `PassSubject`, which sets
  `X-Client-Verify`/`X-Client-Subject` in each forwarding location (proxy only; both names reserved from
  custom request headers). `BlockBots{AI, Scanners, Custom}` renders an http-level
  `map $http_user_agent $jd_<id>_bot` of `"~*<name>"` entries (custom names: letters, digits, space
  `. _ / -`, dots escaped) and a server-level `if` returning 403; presets are read back when every
  member is present. `Hotlink{Allow}` (not redirects) renders `map $uri $jd_<id>_hotlink_type` for
  media extensions and `valid_referers none blocked server_names …` with a `set`/`if` returning 403
  only for media with a foreign Referer. `SecurityTxt`/`RobotsTxt` (not redirects) serve
  `jd-pages/<site>/security.txt` and `robots.txt` at `= /.well-known/security.txt` and `= /robots.txt`
  (`default_type text/plain`), edited as pages `security` and `robots`; the security.txt default names
  `security@<first domain>` and expires in a year, and the form warns within 30 days of `Expires`.
  Private-prefix nginx tests confirm the crawler and hotlink refusals, same-site referrals, and
  single sign-on's 200, 401 redirect and 500 refusal before the application.

  **Single sign-on** (`sites_forward_auth.go`, form "Who may reach it" and each path's options).
  `SiteSpec.ForwardAuth{Provider, Verify, SignIn, PathsOnly}` writes `auth_request /__jd/auth;` at
  server level (or, with `PathsOnly`, in each location whose `SiteLocation.ForwardAuth` is `on`; a
  site-wide one is skipped where a location says `off`), `auth_request_set $jd_<id>_sso_user|email` from
  the provider's headers (Authelia/custom `Remote-User`/`Remote-Email`, Authentik `X-authentik-*`,
  oauth2-proxy `X-Auth-Request-*`), `error_page 401 =302 <signIn>?rd=$scheme://$http_host$request_uri`
  and an internal `location = /__jd/auth` that proxies to `Verify` without the body, with
  `X-Original-URL`/`X-Original-Method`/`X-Forwarded-*` (and `proxy_cache off` under the proxy cache).
  Only 2xx passes; a 403, 5xx or unreachable server refuses the request (auth_request's own rule). Each
  forwarding location sets `Remote-User`/`Remote-Email` from those variables, so a visitor's own are
  replaced (empty where no check ran). The page and ACME locations say `auth_request off`. Refused on
  a redirect, beside a site or path password, access list or `satisfy any` (a password prompt's 401 would become
  the sign-in redirect; satisfy any would bypass it), with `InterceptErrors` (the application's own
  401s would redirect), and with a custom `Remote-User`/`Remote-Email` request header. The provider
  round-trips through the `# Single sign-on through <provider>.` comment. Session cookies the auth
  server sets on the check response are not relayed, so the sign-in portal must set its own.
  The provider, check-address and sign-in-page rules are exported (`ValidForwardAuthProvider`,
  `ValidateForwardAuthCheck`, `ValidateForwardAuthSignIn`) because a deployment route's sign-in
  (`DeploymentRoute.ForwardAuth`) is held to the same ones; on nginx that route is this site
  feature on every path, and on the Docker Caddy ingress it is the equivalent `forward_auth`
  ([Caddy ingress](../deployments/caddy-ingress.md#sign-in)). The form's four presets live in
  `frontend/src/lib/sso-providers.ts`, shared with the deployment Domains settings.

  **Plain-HTTP names on a TLS site** (`sites_tls.go`). `PlainDomains` are names among `Domains` served
  over plain HTTP alone — a server of their own on port 80 after the site's own, with no certificate,
  redirect or HSTS, each marked `# Plain HTTP: <name> is served without TLS.` — and `HostRedirect.Plain`
  a redirecting name answering on port 80 alone, marked `… over plain HTTP`; a redirecting HTTPS name
  on a site whose served names are all plain is marked `… over HTTPS`, from which the parser reads the
  site's TLS, and its certificate and HTTP/2 from that skipped server. `validPlainDomains` refuses a
  plain name the site does not have, or any on a site without TLS. A deployment route's domains each
  choose their scheme ([deployments](../deployments/implementation.md)); the form does not offer it.
  **HSTS and TLS versions** (`sites_tls.go`, form "Encryption"). `HSTSMaxAge` (0 = 15552000, else
  300..63072000), `HSTSOwnNameOnly` (drops `includeSubDomains`) and `HSTSPreload` (refused below a year
  or without subdomains; the form asks for the first domain typed) build the one
  `Strict-Transport-Security` value; `readHSTS` reads it back, six months as 0, so older files save
  unchanged. `TLSProfile` is empty (`ssl_protocols TLSv1.2 TLSv1.3`) or `modern` (`TLSv1.3` only, read
  back from that exact value, with a SpecWarnings line). There is deliberately no cipher/curve profile:
  nginx takes `ssl_ciphers` and `ssl_ecdh_curve` from the socket's default server before SNI, so in a
  named site they do nothing; protocol options do follow SNI. HTTP/3 is not offered: it needs one
  `listen 443 quic reuseport` owner, and the default site does not claim QUIC.

  **Site kinds** (`sites_kinds.go`, form "What it serves"). A redirect's `RedirectCode` is 301, 302,
  307 or 308; 0 falls back to `Permanent` (301/302), and the parser writes back only 307/308 into it so
  pre-existing files round-trip unchanged. `RedirectDropPath` writes `return <code> <to>;` without
  `$request_uri`. `Canonical` (`www`/`apex`, any kind) adds, per non-wildcard domain, a server for its
  other form marked `# Canonical host (<mode>): …` that the parser skips whole; it listens on 80 (and
  443 with the site's certificate when TLS), keeps the ACME path, and 301s to the site's name; a name
  that is already a domain is refused, and a TLS site gets a warning that the certificate must cover
  the other names. `HostRedirects` (`from`, `to`, `code`, at most 16) name redirecting servers outright
  rather than deriving them: each `from` is a name the site does not serve, sent to one it does, with
  308 when `code` is 0, in a server marked `# Host redirect (<code>): <from> is sent to <to>.` that
  keeps the ACME path. The marker carries the whole redirect, so the parser skips the server and still
  reads it back. Deployment routes use it for a plan's redirecting domains. Static sites gain `Autoindex` (warned), `GzipStatic` and `IndexFiles` (also PHP;
  empty = the kind's order, a default read back as empty). Kind `php` needs `Root` and `PHPSocket`
  and renders `location ~ \.php$ { try_files $uri =404; include fastcgi_params; fastcgi_param
  SCRIPT_FILENAME …; fastcgi_pass unix:<socket>; }` plus `location ~ /\.ht { deny all; }`, with
  `PHPFrontController` ending `location /` in `/index.php?$query_string`. fastcgi_params is used
  always rather than Debian's `snippets/fastcgi-php.conf`: the render is pure (it cannot look at the
  host) and the snippet only adds PATH_INFO. Preflight checks a PHP site's root and socket.
  `TestLivePHPSiteRoutesThroughFPM` uses a cached PHP-FPM Docker image and a private nginx prefix to
  verify scripts, front-controller paths, missing scripts and blocked Apache files; it skips without
  the cached image.
  `GET /proxy/php-sockets` (system.admin; runs `find /run/php -maxdepth 1 -type s` on the host via
  hostexec argv) lists sockets for the form.

  **Path routing** (`sites.go` `validLocation`/`validUpstreamTLS`, `sites_render.go` `renderLocation`/
  `renderUpstreamTLS`, form "Paths that go somewhere else" + `site-routes.ts` preview table).
  `SiteLocation.Match` is empty (prefix), `=`, `^~`, `~` or `~*`; `locationPathRe` refuses `; ' " $ ( )`
  in a non-regex path and a regex refuses whitespace, `; { } " ' #`. A regex path may not forward to an
  upstream with a path (nginx refuses it); a folder at an exact/regex path is written with `root`
  (`RootMode root`). `StripPrefix` (prefix paths only) ends both the path and the upstream in `/` and is
  read back from the `# Strips the path before forwarding` comment; otherwise `proxy_pass` is written as
  typed. Folders render `alias <root>/;` and `SPA` falls back to `<path>index.html`. Per path:
  `BodyLimit`, `Timeout`, `Buffering`/`RequestBuffering` (`on`/`off`, empty = the site's — the parser
  folds a value equal to the site's back to empty), `BasicAuthFile`, `AllowFrom`/`DenyFrom` (a path's list
  replaces the site's, as nginx inherits it; a warning says so when both exist). Site level: `Buffering`
  (default off, as before), `StreamUploads` (`proxy_request_buffering off`; named so its zero value is
  nginx's default), `HostHeader` (`$host`, `upstream` = `$proxy_host`, `custom` = `HostHeaderValue`), and
  at server level `proxy_ssl_server_name on` (`UpstreamSNI`), `proxy_ssl_name` (`UpstreamTLSName`, only
  with SNI or verify), `proxy_ssl_verify on` + `proxy_ssl_verify_depth 3` + `proxy_ssl_trusted_certificate`
  (`UpstreamCA`, else `/etc/ssl/certs/ca-certificates.crt`). Password-file paths are absolute-path
  checked like the site's; the route stays system.admin.
- **`sites_pool.go` — several servers.** `SiteSpec.Pool` (`Method` empty/`least_conn`/`ip_hash`/`hash`
  = `hash $request_uri consistent`/`random`, `Scheme` empty = http or `https`, `Servers[]` with `Weight`,
  `MaxFails`, `FailTimeout` seconds, `Backup`, `Down`, zero = nginx's default; `Keepalive`, `RetryOn`
  = `proxy_next_upstream` conditions or `["off"]`, `Tries`) replaces `Upstream` (both set is refused) and
  renders `upstream jd_<ident>_pool { … }` above the servers, which the catch-all forwards to with its
  `proxy_next_upstream`/`_tries`. Keepalive clears `Connection` (`proxy_set_header Connection ""`, or the
  site's upgrade map answers `""` instead of `close`). Refused: backup with `ip_hash`/`hash`/`random`
  (nginx refuses it), a pool of only backups, a scheme or path in a server address (host:port,
  `[v6]:port` or `unix:/path`), Host "upstream's" (`$proxy_host` would be the block name) and an HTTPS
  pool with SNI/verify but no `UpstreamTLSName`. The parser reads any upstream block the catch-all's
  `proxy_pass` names back as the pool, so a Duplicate gets the new name's block. Health checking is
  passive only (`max_fails`/`fail_timeout`); stock nginx has no active checks and the form says so. A
  private-prefix nginx test confirms a request uses the primary and then its backup after the primary
  stops.
- **`tlsscan.go` — what the domain actually serves.** Everything else on the page reads files, which
  cannot see a certificate renewed and never reloaded, a proxy still offering TLS 1.0, or a redirect that
  quietly stopped. Each version is probed on a connection pinned to exactly that version, offering
  every cipher suite Go implements (`probeSuites`: Go leaves RSA key exchange, 3DES and RC4 out by
  default, and a server taking TLS 1.0 only with AES128-SHA read as refusing it). Only the server's own
  answer is `refused` — an alert (recognised by its `remote error` type: OpenSSL's protocol_version
  alert reads "protocol version not supported", and matching those words once filed every correct
  refusal as this client's), a different version picked, or the connection closed on the ClientHello —
  with its words in `detail`. handshake_failure and insufficient_security are the exception and read
  `unknown`: OpenSSL sends them when the version is fine and no suite is shared (a suite Go lacks, such
  as finite-field DHE), Java 8 when it refuses a version, and the alert cannot say which. A version this
  client will not ask for, a probe that could not connect, or one that timed out is `unknown`, **never
  `refused`**, since reporting it absent would be false reassurance about the versions that matter most;
  `TestLiveNginxRefusalsAreReportedAsRefused` and
  `TestLiveNginxLegacyVersionsWithOnlyRSAKeyExchangeAreOffered` hold this against a private real nginx.
  The report's own handshake (`handshake`) offers what a current client does, since that is what it
  calls negotiated. A server answering it with an alert is asked again offering every version and
  suite (`tlsOffer` with TLS 1.0–1.3), then once more as a current client so a passing fault is not
  reported as policy: taken only by the full offer, the scan is reachable and `legacyOnly`, graded F with
  `tls.legacy-only` naming what was negotiated, and its HTTPS request makes the full offer too (a
  server taking TLS 1.2 only with RSA key exchange was reported as nothing answering, with DNS advice).
  Refused by both, it is `tls.refused` with the alert — no certificate for the name
  (`ssl_reject_handshake`, Caddy), a client certificate wanted, or a suite Go lacks;
  `TestLiveNginxWithOnlyRSAKeyExchangeIsReachable` and `TestLiveNginxRejectedHandshakeIsARefusal`.
  **A scan that never completes a handshake says how far it got** (`failure`, `tlsscan_diagnosis.go`):
  `classifyDialError` names the stage — `dns` (no record, or the lookup failed), `connect` (refused,
  timed out, no route) or `handshake` (the server's alert, `plain-http` when the ClientHello is answered
  with `HTTP/` — nginx's `listen 443;` without `ssl` — `not-tls` with the first bytes another protocol
  sent, closed, or no answer). `dialTLS` connects and then handshakes as two steps so a handshake failure
  carries the address it reached (`handshakeError`). `where` says whose answer it was: `here` (loopback or
  an address on this machine's interfaces, private ones included), `cloudflare`, `elsewhere` only when
  this server has a public address of its own in that family, or `unknown` when the provider maps it
  in front of it — a dual-stack VM with its IPv6 on the interface cannot tell its mapped IPv4 from
  another host's (`familyKnown`). Every connect and handshake finding's advice follows it
  (`hereOrThere`): `tcp.refused` here is "nothing here listens on 443", elsewhere it is that host's
  refusal; a handshake with Cloudflare is its edge's, so plain HTTP on 8080 names its plain-HTTP and
  HTTPS ports rather than nginx's `listen … ssl`; plain HTTP on port 80, from any host, is HTTP's own
  port answering as it should (`plainHTTPPort`), never told to add `ssl`, which would break http://,
  the redirect and HTTP-01, and is sent to 443; and an address scanned as itself is never told to
  point a record. `dns` carries `CheckDomainDNS`. `TestLiveNginxListenWithoutSSLIsDiagnosed` holds the plain-HTTP
  case against a real nginx. **Only an HTTP answer is graded on HTTP** (`service`): a port registered to
  a protocol that speaks TLS from its first byte and is not HTTP (`implicitTLSServices`: 465, 993, 995,
  636, 853, 8883 and the rest) is sent no web request at all (`service: other`, `serviceName`); a
  service that answers in its own protocol is `other` with its first line (`banner`, read off the
  connection by `firstBytes`: net/http's error quotes only a fragment of a line that is not HTTP, and
  nothing of a greeting sent on connect before the request, as SMTP and IMAP send theirs); a request
  with no answer is `unknown` with `httpsError` and the `http.https-error` notice. In none of them is an
  HSTS, header or redirect finding made, or port 80 asked. Otherwise the plain-HTTP side is followed by
  hand up to five hops (`redirectChain`), passes when it reaches `https://` on any host, and says where
  it ended (`redirectVerdict`: `same-host`, `other-host`, `stays-http`, `loop`, `too-many`, `dead-end`,
  `internal`); `plainErrorKind` says whether port 80 refused, timed out or did not resolve. The HTTPS
  answer's own redirect is `location`. Every hop after the first goes where the remote site's
  `Location` says, so its dial (checked on the resolved address, in a `net.Dialer` `Control` hook) may
  reach only the address the first request reached or a public one (`hopAllowed`, `IsPublicAddress`):
  a redirect to loopback, a private or link-local network or CGNAT is recorded as an `internal` hop and
  not requested, and graded as a notice (`http.redirect-internal`) rather than a missing redirect,
  because where the chain ends is not known. The serial is colon hex as openssl prints it, and the
  unstapled-OCSP notice needs a responder in the leaf (`ocspServers`; Let's Encrypt names none);
  `crlUrls` and `spkiPin` (base64 SHA-256 of the public key, what `curl --pinnedpubkey` takes) are
  reported beside them. **Chain and revocation** (`tlsscan_chain.go`, `revocation.go`): each `chain`
  link carries its validity, serial, SHA-256, signature algorithm, names, extended key usage and
  `issuedByNext`; `trustPath` is the path `x509.Verify` built to a root in the system store (without
  the name, so a mismatch does not hide it), each step marked sent or supplied by the store.
  `chainAudit` feeds grade rules: `tls.no-server-auth` (EKU set without serverAuth, F), `tls.sha1`
  (a non-root link signed with SHA-1, F), `tls.must-staple` (TLS Feature status_request with no
  staple, F), and for a chain trusted here only, `tls.long-validity` (over 398 days, issued since
  2020-09-01, B) and `tls.no-sct` (no SCT in the certificate, the TLS extension or the stapled OCSP
  answer, B) — a private root in the system store is indistinguishable from a public one, and the
  findings say so; `tls.chain-order` and `tls.root-sent` are notices. `revocation` reads the stapled
  OCSP answer, then each responder (`x/crypto/ocsp`, POST), then each CRL (at most 20 MB,
  `CheckSignatureFrom` the issuer, current by NextUpdate); an answer is believed only when signed by
  the issuer or its delegated responder. Those URLs are the certificate issuer's to write, so the
  fetch dials public addresses only (checked after DNS in a `Control` hook, like redirect hops),
  follows no redirects and fetches `http`/`https` only. Good and revoked answers are cached in memory
  per issuer key and serial until their NextUpdate. A revoked leaf is `tls.revoked` (F); an answer
  that could not be had is the `tls.revocation-unchecked` notice when the leaf names a source.
  **Expiry is judged against the certificate's term** (`tlsscan_lifetime.go`):
  renewal is due in the last third of it, the last half for a term of ten days or less — certbot's rule
  since 4.0, and Caddy's — and never more than 30 days out. `summarise` (`certs.go`) sets `expiring`
  by it, so the certificate inventory, the watch list and the report agree; a 6-day certificate is no
  longer expiring for its whole life. Not everything reads it yet: certbot's lineage cards and an import's warning still count 30 days (`certbot-panel.tsx`,
  `import.go`), and the Certificates page's expiring reading counts by the window under a 30-day label
  (`certs-panel.tsx`). The report notes a certificate inside that window (`tls.renewal-due`) and grades B once
  half of it has passed with the certificate still served (`tls.expiring`); `lifetimeHours` and
  `renewalWindowHours` are in hours because a short-lived term is 160 of them. **HSTS preload** is
  measured for a name on 443 that answered HTTP (`preload`, `tlsscan_preload.go`) against
  hstspreload.org's rules, each with what was seen: a registrable domain by the Public Suffix List (a
  subdomain gets only that rule, naming the parent to scan), a trusted certificate, a first plain-HTTP
  redirect to HTTPS on the same host (a refused port 80 passes), an HTTPS redirect that stays on HTTPS,
  `max-age` of a year, `includeSubDomains`, `preload`, and `www.` serving a trusted certificate when it
  has a record — that handshake runs beside the probes. Six months stays the A+ threshold, as SSL
  Labs'; the list asks for a year, which the report now says. `TestLiveNginxPreloadRules` checks the
  rules against a real nginx. `grade` is a pure function of the scan. The live certificate, TLS and DNS probes require
  `system.admin`: each emits traffic to a caller-chosen destination, the same scanner boundary as
  `/network/probe`. What reaches them is `ParseScanTarget` (`scantarget.go`): a URL, host:port, a
  bracketed IPv6 address or a name in its own script become a host and port, and anything else is a 400
  with the reason, quoting the host as typed rather than the whole pasted URL;
  `frontend/src/lib/scan-target.ts` is the same parser, and both are tested against
  `frontend/src/lib/scan-target-cases.json`, whose `says` holds the two to the same words. A port,
  written after the host or given as `?port=` text (`ParseScanQuery`), must be 1–65535 in digits, so
  `0`, `+993` or `host:+993` is a 400 rather than 443 or 993.
  A scan runs on its request's context, so a caller that leaves (the report's Cancel) ends its
  handshakes and probes at once.
- **The deep scan** (`GET /certificates/scan/deep?domain=&port=`, `system.admin`, handler in
  `handlers_tls_deep.go`) is its own request, which the report makes after the quick scan when `?deep=1`
  is in its address. Go's TLS client offers only the suites Go implements and in TLS 1.3 all of them
  whatever it is told, so the suites are asked with a ClientHello of the scan's own (`tlshello.go`): it
  reads the ServerHello's version, suite and TLS 1.3 group (and a HelloRetryRequest's), and for TLS 1.2
  the ServerKeyExchange's curve or DH prime size, then hangs up; no key is ever derived. A hello between
  256 and 511 bytes is padded to 512 as browsers do (F5), SSL 3.0 carries the renegotiation SCSV and no
  extensions, and signature_algorithms goes only in TLS 1.2 and 1.3 hellos. Each of SSL 3.0 to TLS 1.3 is
  listed by elimination (`tlsciphers.go`): offer the whole catalogue (every ECDHE/DHE/RSA/static-ECDH
  suite with AES, ChaCha20, CCM, ARIA, Camellia, SEED, IDEA, 3DES, RC4 or DES, and the export,
  anonymous and NULL ones; PSK, SRP and GOST need a secret or certificate no scan has), note the pick,
  offer the rest, until a refusal — about one connection per accepted suite — then offer the accepted
  ones reversed to say whose `order` it is. A version stopped short by a probe that heard nothing says
  so (`complete: false`). Suites rate `insecure` (NULL, export, DES, RC4, anonymous), `weak` (no forward
  secrecy, CBC, a 64-bit block, CCM_8) or `strong`. TLS 1.3 groups are asked one at a time — real key
  shares for X25519, P-256/384/521 and the three ML-KEM hybrids (X25519MLKEM768 puts the ML-KEM key
  first), none for X448 and ffdhe, which a server answers with a retry — and then as a browser offers
  them, whose pick is `browserGroup`; without TLS 1.3 that is TLS 1.2's curve for a browser's offer.
  TLS 1.2 curves are not listed one by one: there the offered curves also bound an ECDSA certificate's,
  so one at a time refuses the certificate, not the curve. Every probe goes to the one address the first
  handshake reached, eight at a time, five seconds each, under a 45-second budget (`tlsdeep.go`). That
  first handshake is a browser's (ALPN h2/http/1.1, a session cache): its protocol is held against the
  site form's HTTP/2 switch for the enabled nginx site that claims the name with a TLS listen on the
  scanned port (`SiteForName`: exact, then the longest leading wildcard, then trailing; a name no site
  claims is not compared), only when the connection reached this server. Resumption offers its session
  back (TLS 1.3 tickets arrive after the handshake, so it reads for up to a second, asking a website for
  its headers), plus a TLS 1.2 pair; a TLS 1.2 server that issues no ticket is reported untested, since
  Go cannot resume by session ID. Handshakes naming no site and a `.invalid` name show what a scanner
  gets; a certificate there is `tls.sni.default-certificate`, whose fix is a default server with
  `ssl_reject_handshake on`. Alt-Svc is read from an HTTPS GET, and QUIC asked on the advertised UDP port
  of the same host (or, unadvertised, the scanned port) with one padded packet in a reserved version,
  which a QUIC server must answer with Version Negotiation (`tlsquic.go`); another host's alternative is
  never sent anything. `deepFindings` is pure; version findings reuse the quick scan's ids so the page
  can drop the ones the quick report already lists, and advice for an answer from another machine says
  so. `TestLiveDeepScanOfNginx`, `TestLiveNginxRejectingUnknownNamesLeaksNothing`,
  `TestLiveNginxWithOnlyRSAKeyExchangeHasNoForwardSecrecy` and `TestLiveNginxSmallDHGroup` hold it
  against a private real nginx; Go servers cover ML-KEM, TLS 1.3 suites, ALPN and resumption.
- **A scan with a certificate is traced to the nginx site behind it** (`origin`, `tlsorigin.go`,
  added by `handleTLSScan`): `TraceOrigin` picks among enabled nginx sites listening on the port by
  nginx's order (exact `server_name`, longest leading then trailing wildcard, first regex RE2 can
  compile, then the `default_server` or else the first site by name) at file granularity, finds the
  site's `ssl_certificate` through the inventory's `usedBy`, and compares fingerprints: `current`,
  `stale` (the file is a later certificate for the same names — a renewal nginx has not reloaded),
  `other` (another certificate on this host answered), `foreign` (none on this host did), `unserved`
  or `unknown` (no `ssl_certificate` in the site's own file, or unreadable). It is nil for an
  address, a name Caddy claims, or a host without nginx sites. `stale` is a heuristic: a second
  machine serving an older copy for the same names reads the same way.
  **Every scan says which address it reached** (`address`, and `addressKind` from `whereConnected`:
  `here`, `cloudflare`, `elsewhere`, `unknown`). `?connect=<ip>` (`ScanOptions.ConnectTo`, read by
  `ParseConnectTo`: an IP only, for a name only, a written port must be the scan's) dials that address
  for the handshakes, the HTTPS request and the plain-HTTP chain's requests to the name, with SNI and
  Host still the name's — curl `--resolve` — so the origin behind a CDN or a server before cutover can
  be graded; the scan is a GET, which `AuditMutations` skips, so the handler records it itself
  (`httpx.AuditRead`, action `certificates.scan.connect`). `?all=1` (not with `connect`) handshakes
  with each A and AAAA record beside the scan, eight at a time (`tlsscan_addresses.go`, resolver
  injectable), returns them as `addresses`, and raises the warning `tls.address-mismatch` when
  reachable addresses serve different leaf certificates; it caps no grade, since the grade is of the
  answer this scan got.
- **STARTTLS services are scanned through their own dialogue** (`starttls.go`). `?proto=` is `auto`
  (the default: SMTP on 25 and 587, IMAP 143, POP3 110, FTP 21, PostgreSQL 5432, TLS from the first
  byte elsewhere), `tls`, `smtp`, `imap`, `pop3`, `ftp` or `postgres` (`ParseStartTLS`, anything else a
  400). `dialTLS` holds the dialogue on every connection of the scan — the handshake, the four version
  probes and each `?all=1` address — before its ClientHello, bounded by the handshake's deadline, the
  request's context, 4 KiB a line and 100 lines a reply; EHLO names `localhost`. It is strict: an answer
  the protocol does not define ends it, and so does any byte buffered after the server agreed (the
  STARTTLS injection class). The scan carries `starttls`, sends no web request (`http.service` is
  `other`) and skips the preload check; a dialogue that ends early is a failed handshake with reason
  `starttls-refused`, `starttls-unexpected`, `starttls-closed`, `starttls-timeout` or
  `starttls-error` and what the server said in `answer`, and a version probe it stops is `unknown`.
  `CheckDomain` and `CheckEndpoint` use Auto; a watched endpoint has no per-endpoint protocol.
- **The HTTPS request can be shaped, and its answer is audited.** `?method=` (GET, HEAD or OPTIONS
  only: a scan is a read; POST belongs to the audited request tester), `?path=` and `?host=` go
  through `proxysvc.ParseRequestShape` and change only the HTTPS request — SNI stays the scanned
  name, and the plain-HTTP chain still asks `/` with the name as Host. A custom Host is recorded
  with `httpx.AuditRead`, action `certificates.scan.host`. `http.responseHeaders` lists every header
  of that answer (150 at most, values capped at 2 KiB); Set-Cookie values are replaced with
  `<redacted>` inside the scan and the raw headers are never kept or stored. `httpaudit.go` holds
  the pure rules over those headers — versioned `Server` (with `fix: "server-tokens"`, which the
  page answers by locating `server_tokens` through `/proxy/tools/directive`), `X-Powered-By` and
  kin, cookies without Secure/HttpOnly/SameSite, CSP `'unsafe-inline'` without nonce or hash,
  `'unsafe-eval'`, wildcard script sources, a policy with no script limit or only Report-Only,
  uncompressed text on GET (the request sends `Accept-Encoding: gzip, br` itself so net/http does
  not strip `Content-Encoding`), and `Access-Control-Allow-Origin: *` with credentials. They add
  findings and cap no grade.
- **The watch list is endpoints.** `watched_endpoints` (lane G in `proxySchema`) is a name, a port and
  an address, unique together, so a mail server can be watched on 443 and 993; `watched_domains` held
  one row per name and a second port replaced the first. Its rows are copied in on every boot with
  `INSERT OR IGNORE`, which brings nothing back because an unwatch deletes the `watched_domains` row as
  well. Each endpoint keeps its last check (`checked_at`, `certificate` as JSON) and may name an `ip` to
  reach the name at (an origin behind a CDN): the handshake then goes there and still asks for, and is
  verified against, the name (`CheckEndpointAt`). `GET /certificates/watched` only reads; no visit
  handshakes. The server checks on its own schedule: `proxysvc.TLSMonitor` (`tlsmonitor.go`, started in
  `startProxyExtras`, stopped in `stopProxyExtras`) looks every minute for endpoints whose last check is
  older than the interval (setting `certificates.watch.interval`, default 300 s, 60–86400 s; read by any
  account at `GET /certificates/watch-schedule`, set by `system.admin` at `PUT`), and
  `POST /certificates/watched/check` (`system.admin`, audited) checks all now under a 30-second budget and
  answers with the list. Either way eight run at a time, stalest first, through `CheckEndpoint`, whose dial
  ends with the context and holds the STARTTLS dialogue Auto picks by port, so a watched mail server on
  587 is read as a scan reads it; one still in flight or not started when the budget ends keeps its stored result
  and time. One pass at a time: a caller waiting for a scheduled pass waits only as long as its own budget.
  Every check also goes into `watched_checks` (days left, fingerprint, error; 2000 per endpoint and 90
  days, cascading with the endpoint), read at `GET /certificates/watched/{id}/history`.
  **Network probes are watched endpoints of their own kind**, not a second monitor:
  `watched_endpoints.kind` is `tls` (a handshake and its certificate, every row before this) or `tcp`
  (`POST /certificates/watched {domain, port, ip?, kind: "tcp"}`), checked by the same `TLSMonitor` pass
  on the same interval through `CheckTCP` — one connection, at the pinned address where there is one,
  nothing sent, closed — kept as `ProbeCheck` (`ok`, `state`: connected, refused, timeout, unresolvable,
  error; the address dialled; connect time) in `watched_endpoints.probe`, and in `watched_checks` with
  its error and `ms`. The two tables moved from `proxySchema` into `schema` because they gained columns
  after shipping (`kind`, `probe`, `ms`, brought to older installs by `addedColumns`; every existing row
  reads `tls`). A probe where a TLS watch already exists is 409 `already_watched` (the handshake checks
  the connection too); a TLS watch where a probe exists takes it over, drops the probe's connect times
  from its history in the same transaction, and is checked on the next pass,
  and a probe result still in flight then is not saved onto the row (`… AND kind = 'tcp'`).
  The `watch_unreachable` alert judges a probe by whether it connected ("No TCP connection (refused):
  …"); the untrusted and grade rules and the fleet scan leave probes out. The list (`watched-domains.tsx`)
  draws a probe with its connection and connect-time trend and no TLS report; the Network Tools page's
  TCP port check offers **Watch on a schedule**, and the Network runs page lists **Watched probes**
  (`watched-probes.tsx`, `lib/watched-probes.ts`) with check-now and stop. Tests:
  `TestWatchedEndpointsGainProbeColumns`, `TestTheWatchMonitorProbesNetworkEndpoints`,
  `TestCheckTCPConnectsOrSaysWhyNot`, `TestNetworkProbesAreWatchedEndpoints` (a real loopback listener
  watched, checked, its history, the unreachable alert after it closes, and the kind rules).
- **TLS report history.** Every quick scan that runs to its end (not one its caller cancelled) is stored in
  `tls_scans` (grade, days left, fingerprint, reachability and the report as JSON; 100 per target and 180
  days). `GET /certificates/reports?domain=&port=` lists a target's reports newest first and
  `GET /certificates/reports/{id}` returns one with `changes` against the report before it
  (`proxysvc.DiffScans`, `tlsdiff.go`: reachability, grade, certificate replaced, trust, key, negotiated
  and per-protocol status, HSTS, each security header and the port-80 redirect). Both only read, so every
  account may; `DELETE /certificates/reports?domain=&port=` is destructive and audited. Deep scans are not
  stored, and a successful scan records no connected address, so an address change is not diffed.
- **`dnsdeep.go` — the TLS page's DNS panel** (`GET /certificates/dns?deep=1`, `system.admin` like the
  rest of the probes). `Query` asks one resolver one question on the wire (`x/net/dns/dnsmessage`, UDP
  with EDNS 1232, TCP when the answer is truncated), because Go's resolver hides TTLs and cannot ask
  for CAA. The report reads A/AAAA with TTLs, each tagged this-server, cloudflare, other, or unknown
  when the host has no public address; the CNAME chain; the CAA walk of RFC 8659 from the name up to
  the TLD, judged for `letsencrypt.org` (issue, issuewild falling back to issue, `;` naming nobody, a
  critical unknown tag forbidding everyone, a failed lookup refusing issuance as Let's Encrypt does);
  `_acme-challenge` TXT/CNAME; and A+AAAA from the first `resolv.conf` nameserver beside each compared
  resolver, each marked against the first that answered. Records come from that same first resolver.
  The comparison list is the `proxy.dns.resolvers` setting (IP literals only, at most six, empty
  restores 1.1.1.1, 8.8.8.8, 9.9.9.9), written by `PUT /certificates/dns/resolvers` (`system.admin`,
  audited as `certificates.dns.resolvers`). A name only: an address is refused with 400.
- **Scan every site** (`tlsfleet.go`, `handlers_tls_fleet.go`). `proxysvc.FleetTargets` (pure) is each
  enabled TLS site's names on each port its nginx `listen … ssl` directives name (443 when none does; a
  Caddyfile address unless it says `http://` or is a bare port) plus every watched endpoint, once per
  host and port; wildcards, regex names and `_` are left out, and a watched endpoint pinned to an address
  is scanned by name. `POST /certificates/scan-all` (`system.admin`, audited, 409 while one runs, 400 with
  nothing to scan) starts job `tls.scan-all`: `ScanFleet` runs the quick report four at a time, a minute
  each, streams `[n/total] host:port grade — summary`, and stores each finished report in `tls_scans`;
  cancelling the job starts nothing more and keeps nothing cut short. `GET /certificates/scans/latest`
  (any account; it only reads) is the fleet with each target's latest stored report (grade, days left,
  expiry flags, negotiated version, count of critical and warning findings) and the running job, if any.
  Both take the sites that could be read (`ReadVHosts`): a Docker ingress that cannot be looked up
  leaves nginx's sites, the host Caddyfile's and the watched endpoints in the fleet, and one that is
  found and cannot be read answers 503 `ingress_unreadable` rather than 500
  ([runtime boundaries](../architecture/runtime-boundaries.md#server-modules-degradation)).
- **`probe.go` — the request tester** (`POST /proxy/tools/request {url, method, headers, connectTo}`,
  `system.admin` with the rest of `/proxy/tools`, audited as `proxy.tools.request` with the method and
  URL). It dials only `127.0.0.1` or `::1` on the URL's port, whatever DNS says, and puts the name in
  SNI and Host; the URL's host must be a name an enabled nginx site listening on that port takes by
  `serverFor` (the same order `TraceOrigin` uses), so an address, another engine's name or a name no
  site claims is a 400 before anything is sent. Methods are a closed set, there is no request body, and
  headers are tokens with one-line values, never Host, Content-Length, Transfer-Encoding or Connection.
  Each hop reports status, protocol (HTTP/2 when nginx offers h2), headers, the first 64 KiB of the body
  (none if not UTF-8 text), connect, TLS, first-byte and total times from `httptrace`, and the leaf
  certificate with its trust verdict and `TraceOrigin` against the site's file. Redirects are followed
  by hand, at most five, each Location re-checked as the first URL was: one that names no local site
  ends the chain as `elsewhere`, so the tester never reaches anything but this nginx. Authorization and
  Cookie are dropped when a redirect changes host.
- **`tlsscan_fixes.go` — remedies for TLS findings.** Each `ScanFinding` carries a `fix` hint
  (`renew`, `issue`, `force-https`, `hsts`, `security-headers`, `fullchain`, `protocols`); it is a hint
  only, and every remedy runs through an endpoint that already exists (site preview and apply,
  `/certificates/renew`, `/proxy/config`, test-then-reload). The one new route is
  `GET /proxy/tools/directive?name=` (`system.admin` with the rest of `/proxy/tools`, read-only, not
  audited): the name must match `^[a-z_][a-z0-9_]{0,63}$`, and the answer is every place it is set in
  the tree `NginxTree` builds from `EffectiveConfig` — path, line, arguments, block context and the
  enclosing server's `server_name`s. Files `EffectiveConfig` leaves out (outside the proxy directory,
  such as certbot's `options-ssl-nginx.conf`, or password files) are not searched.
- **`dns01.go` — wildcards and CDN-fronted domains**, which between them are most of the certificates
  people want: Let's Encrypt signs `*.example.com` only against DNS-01, and a Cloudflare-proxied domain
  never receives an HTTP challenge. Eight certbot plugins as a closed set (each names credentials and
  propagation after itself; route53 has neither), credentials 0600 inside certbot's tree. A wildcard over
  HTTP is refused here with what to do instead, rather than relaying certbot's accurate and useless
  "wildcard domains are not supported by the HTTP-01 challenge".
  Route 53 credentials are normalized to an AWS `[default]` profile and atomically saved at
  `/etc/letsencrypt/jd-dns/route53.ini`. Previously saved bare key/value files are validated and
  atomically normalized on the next dashboard certbot operation; invalid files fail without falling back
  to a different AWS identity. Dashboard issuance and renewal set
  `AWS_SHARED_CREDENTIALS_FILE` to this path and `AWS_PROFILE=default`, clearing competing inherited
  AWS credentials; values never appear in command arguments or job logs. If host certbot timers or
  cron jobs renew these certificates, their environment must set the same two variables. Saving a
  dashboard credential does not reconfigure an external timer; an IAM-role-only installation can leave
  the credential form empty.
  Deployment runs use the same saved files for a plan that serves a wildcard
  (`deployment_certificate_dns.go`, see the deployments [implementation guide](../deployments/implementation.md)):
  each provider carries a pattern for the nameservers it gives a zone it hosts, the run walks up from the
  wildcard's base to the zone's NS records and uses the provider they name, failing that the single
  provider with saved credentials and an installed plugin, and otherwise refuses with what to save here.
  It passes `dnsIssueArgs`'s argv — the file's path, never its contents — through the page's certbot
  runtime, so the lineage renews like one issued here; an IAM-role-only Route 53 (no saved file) is not
  chosen. `DeploymentDNSProvider` answers the same question without ordering, for preflight and the
  deployment Domains sheet. Removing a provider's credentials also stops runs issuing wildcards in its
  zones; the next one is refused, naming this page.
  Site-validation rollback restores a replaced `sites-enabled` symlink to its exact previous target.
- **Raw configuration edits carry a content revision** (`config_checked.go`). `GET /proxy/config`
  returns `content` and its SHA-256 `revision` under the proxy writer lock, so it cannot expose
  another operator's transient nginx validation candidate. `PUT /proxy/config` requires that
  `expectedRevision` (400 when missing); `WriteConfigChecked` compares the original bytes under the
  same lock before any staging or native validation. A stale or removed file answers 409
  `config_conflict`, audited as a refused conflict, without replacing it. Caddy validates a private
  copy and checks the revision again immediately before atomic publication. nginx stages the checked
  candidate once, runs its native validator and verifies the candidate is still on disk: a native
  writer that replaced it is preserved and causes a conflict, rather than being overwritten by a
  rollback. Invalid or interrupted candidates are restored only while their own bytes still occupy
  the file. Successful responses, including `reload_failed` after a completed save, carry the saved
  revision. Internal generated writes and history restore retain their existing `WriteConfig` owner.
  The shared raw editor keeps its dirty buffer and original revision in RAM per engine/path across
  SPA navigation and Back/Forward. Conflict UI pauses saving, preserves the draft, shows a diff
  against the current file and requires an explicit review against that version before retrying.
  Discard rereads a changed file; a removed/unreadable file retains the draft with a retry action.
  Raw Test config uses the same conflict display if a native writer changes the candidate during
  validation. The config editor's nginx dry runs restore only their own candidate's bytes, including
  prior absence, and refuse to erase a newer native edit. Raw reads, writes and restoration pin the approved parent
  directory through `os.Root`; temporary contents, FD-based permissions, publication and cleanup stay
  on that captured directory. A renamed, replaced or outwardly retargeted parent causes a conflict;
  a candidate still held by the original directory is restored there while replacement directories
  and their files stay untouched. `files.Resolve` rejects outward symlink ancestors even when the
  requested suffix does not exist. Raw file reads are nonblocking and require a regular file, so a
  native FIFO replacement cannot hold the writer lock forever. The native check-to-publish interval
  cannot atomically compare bytes against independent host writers; descriptor confinement prevents
  that remaining interval from redirecting metadata, writes or cleanup into a replacement ancestor.
  Its unload warning remains active even while the editor is closed. Explicit discard and account
  working-state cleanup clear the buffer; no config text is written to Web Storage. Service/API
  regressions cover both nginx and Caddy, simultaneous stale writers, native edits during validation,
  refused-candidate restoration and missing preconditions; `proxy-engine-overview.spec.ts` covers
  retained history drafts and conflict review.
- **The editor's read route is not a password reader.** `ReadConfig` refuses the dashboard's own
  `jd-auth` directory and any `.ht*`/`htpasswd` file with `ErrProtectedFile` (403 `protected_file`):
  `GET /proxy/config` is held by every signed-in account and a list of bcrypt hashes is not
  configuration. The protected-directory comparison uses its absolute and resolved path, so
  relative configured roots and internal `jd-auth` directory aliases retain the same refusal. A file inside the proxy's directories that is not on disk (removed or renamed
  after the list naming it was read) is a 404 `not_found` marked retryable, which the editor shows
  as an error with Try again; a missing path outside them is still 403 `outside_root`. `Validate` now carries a `Note` when the file is outside nginx's include tree —
  no `sites-enabled` link, a `conf.d` file without `.conf`, a stream with no include — because
  `nginx -t` passes a file it never reads, and a dry run that says "valid" about a disabled site is
  a false reassurance. `POST /proxy/test` runs the engine's own test against what is on disk without
  staging or reloading anything (admin, audited as `proxy.config.test`); the overview's Test config
  and Reload buttons post it and `/proxy/reload` with the engine's kind, and a kind the service does
  not know is a 400 rather than nginx. A reload the test refuses is a 422 whose body carries the test
  beside the error, as a refused start's does (below). For the shared Docker Caddy that kind is `caddy-ingress`
  (`docker_caddy_engine.go`): `caddy validate` and `caddy reload` run inside the running container
  against its own `/etc/caddy/Caddyfile`, never the host's caddy, and with no ingress running both
  answer 409 `no_ingress`. The editor's `POST /proxy/validate` and `PUT /proxy/config` take the same
  kinds (none named is nginx, an unknown one is a 400) and refuse `caddy-ingress` with a 400 before
  any file is touched: `WriteConfig` treats every kind but `caddy` as nginx, so the ingress kind wrote
  an nginx file and then reloaded the container, and the ingress's Caddyfile is the deployments' to
  write. `Availability` names an ingress only once it runs (`ingressState:
  "running"`); one the first deployment would start is `ingressState: "provisionable"` with neither
  `caddy` nor `ingressContainer` set, and deploy preflight counts either state as a proxy that can
  serve and certify the domain. A lookup that could not say either way sets neither state and
  `ingressProblem: {code, message, reason, raw, retryable}` (`proxysvc.IngressNotice`): Docker not
  answering, the named container not running, more than one public Caddy, or a Caddy that cannot be
  shared as it runs. `GET /proxy/vhosts` still answers nginx's and the host Caddyfile's sites then
  (`ReadVHosts`), so this field is where the Sites page learns that the deployment routes are missing
  from its list and why; the codes and their statuses on the routes that do fail are in
  [runtime boundaries](../architecture/runtime-boundaries.md#server-modules-degradation). A running ingress also carries `ingressId` and `ingressStartedAt` (Docker's
  `State.StartedAt`), so the overview reads "running as a container, up 3d" and offers Restart
  container (`POST /docker/containers/{id}/restart`, destructive, audited as
  `docker.container.restart`) and Container logs (the container page's logs tab) through the Docker
  routes and their own gates; the proxy routes add no container control of their own. Start, restart, stop, enable at boot and clearing a failed state go
  through `POST /proxy/engine/{start|restart|stop|enable|reset-failed}`, which resolves the unit
  itself (`Service.Engine`: `nginx.service` where nginx is installed, else `caddy.service`; 409
  `no_engine_unit` otherwise) and runs start and restart through `WithTestedConfig` — the config
  test under the service lock, then systemctl only if it passed. This host's `nginx.service` runs
  `nginx -t` before it starts, so a restart over a broken file used to stop nginx and leave every
  site down; now it is a 422 whose body is `{error: {code: "invalid_config", message}, validation}`
  — the message is the sentence plus nginx's output, and `validation` is the test itself with its
  parsed `diagnostics`, which the overview places at their file and line — and systemctl never runs.
  Stop, enable and reset-failed start nothing and run no test, so an engine whose file is broken can
  still be set to start at boot or have a hand-fixed failure cleared. A systemctl that refuses (a
  masked unit, a start that fails) is a 502 `command_failed` in systemd's words. Start, enable and
  reset-failed need `system.admin`; restart and stop are destructive as well; each is audited as
  `proxy.engine.<action>`, a refusal with `result: refused` and the 422. There is no disable: taking
  the proxy out of the boot sequence is the Services page's. The unit's state, `UnitFileState`,
  `Result` and `NRestarts` are still read from `/systemd/{unit}`, and a failed unit's last 30 lines
  from `GET /logs/search` on its `journal:` source (`engine-failure.tsx`), so the Services page
  and the overview never disagree about it.
- **`ParseSiteSpec` reads what hand-written files actually look like.** A line holding a whole
  block — `location / { proxy_pass http://x; }` — is split into statements before it is read
  (`splitInline`, quote-aware, since a Content-Security-Policy value carries semicolons of its
  own); the line reader used to take the opener and drop everything after the brace, so the site
  came back with no upstream. `listen` is read as nginx reads it — the address's port and an `ssl`
  parameter — where `4430` and `127.0.0.1:8443` used to count as TLS, and the pre-1.25
  `listen 443 ssl http2` reads back as HTTP/2 on. A file with no `access_log` directive is logging
  to nginx's default, so an unmanaged file round-trips with logging on rather than the first save
  writing `access_log off;`. Only a server-level `access_log`/`error_log` is the site's —
  `access_log off` under `/favicon.ico` silences one path, and read as the site's it told the form a
  site logging to nginx's shared file kept no log at all — and the first that names a file is `SiteSpec.AccessLogPath`
  / `ErrorLogPath` (`logFile`: `off`, `syslog:`, `stderr`, `memory:`, anything under `/dev/`, a path
  with a variable and a relative one are not a file this can read). There is no fallback to the managed
  names: nginx writes nothing there once the directive is gone. `VHost` carries both, read the same way
  in the listing, so a list of sites says which keep a request record of their own without a read per
  site; the renderer and every reader spell the managed paths through `nginxAccessLogPath` /
  `nginxErrorLogPath`.
- **A site's requests are read the way a deployment's are** (`site_access_log.go`,
  `handlers_proxy_site_requests.go`). `GET /proxy/sites/{name}/requests`, `/requests/stream`
  (WebSocket) and `/requests/export` (CSV) are reads, like the site itself, and share
  `requestFilterFrom`, the window, the cursor tail and the export with the deployment routes (one
  `followRequests`/`exportRequests` for both), over the one `accesslog.Store` — two stores would hold
  two caps, and the cap bounds what the whole process keeps. `SiteRequestRoute` resolves the record
  **from the site's file on every request**, so an edit that moves `access_log` — the form, the raw
  sheet or an editor over SSH — reads the new file from the next poll: an nginx site is the record
  `file:<its access log>`, refused unless `logs.Allow` accepts it; a `just-dashboard-*` site whose file
  logs where the renderer puts it, and a Docker Caddy route with no file on the host, are the
  deployment's own route, the record its Logs page already holds — the latter only while the ingress
  holds that route's file, since any reader can type a `just-dashboard-*` name and each would start a
  record and a lookup of its own. An export neutralises a cell a client wrote (`csvText`): an agent, a
  referer, a path or a host that starts with `=`, `+`, `-`, `@`, a tab or a return gets a leading `'`,
  so a spreadsheet reads it as text rather than running it. `SiteRecordReader` asks `logs.Allow`
  again before every open of the live file and of each rotated generation, since a generation beside
  the file can be a link to anywhere, and generations are the names logrotate gives (`logsx.Archives`:
  `.N` and dated), compressed ones skipped — for deployments' nginx files too. A site with no file, no
  access log of its own (off, syslog, or nginx's shared log, whose combined lines do not say which site
  answered) or one outside the roots answers `unavailable` with that sentence, and the stream and export
  refuse it with a 400. The name in the URL is unescaped, so a Caddy host with a colon works. A
  deployment's window carries `ingress` (the Caddy container) or `errorLog` (the nginx site's
  `.error.log`), where the proxy says why it failed a request. The rest of the proxy's logs are read on
  the pages they are about, through the `/logs` routes: a site's own page (its access and error files,
  nginx's shared error log narrowed to its names, the ingress container or `journal:caddy.service`),
  the overview's Engine log (nginx's two files and unit, a host Caddy's unit, and the Docker Caddy
  ingress once `GET /logs/source` finds its container) and Certificates' Renewals (`letsencrypt.log`
  and the renewal unit's journal, through the `certbot` lens).
- **`SetVHostEnabled` changes a link only if nginx still loads.** Enabling a site whose `sites-enabled`
  entry already existed but pointed elsewhere returned success and changed nothing; it goes through
  `linkEnabled` now, so the switch saying on means nginx reads the file. Enabling also used to make the
  link and return: a site nginx could not load was then in the include tree, the reload after it was
  refused, and every later reload — deployment cutovers included — was refused with it until the link
  was removed by hand. Now the link goes in, `nginx -t` runs, and a refusal undoes it (or restores the
  link it replaced, target text and all) and returns a `*RefusedError` carrying the test, which the
  switch answers as 422 `invalid_config` with nginx's first error, file and line
  (`FailureHeadline`) as the message and the test output as `raw`. A disable is tested the same way
  and put back when it breaks a configuration nginx was loading (another site used its upstream or
  zone); out of a configuration nginx was already refusing it stands, because switching sites off is
  how a broken configuration gets fixed. nginx's first error names a file other than the one being
  switched in three cases, and `RefusedError.Lead` says which, so `Reason()` — the 422's message and
  the audit entry's `reason` — does not read as that file's fault: a refused disable or link removal
  is "nginx refuses the configuration without <name>: …" (the error is in the site that needed it);
  a refused enable is tested again with the link undone, and when the same error is still the first
  one it is "nginx already refuses the configuration without <name>: …"; and when it is not, but the
  error is outside the site's own (resolved) file or names no file — a second `default_server` on
  the same address, an upstream name both sites define, which nginx reports in whichever of the two
  it reads second — it is "nginx refuses the configuration with <name>: …". An enable refused in its
  own file carries no lead. A reload that fails after a passing test is a 200 with
  `reloaded: false` and `reloadError` rather than a 502, since the link is in place and correct.
  A file sitting where a link belongs in `sites-enabled` is never removed as a "disable". A link
  under another name to the site's file (`00-default -> ../sites-available/default`) serves it as
  surely as its own: the disable takes every such link out (and puts them all back on a refusal),
  and an enable of a site served that way changes nothing rather than loading it twice.
  `RemoveVHostLink` (`DELETE /proxy/vhosts/{name}/link`, destructive, audited as `proxy.vhost.unlink`)
  takes out a link no site's switch owns — one to nothing, or to a file outside sites-available —
  under the same test-and-restore rule, and refuses a link to any sites-available site and any real
  file. The switch and the removal reload nginx through `ToggleVHost` and `RemoveVHostLink(…, reload)`
  inside the same hold of the service lock (`reloadLocked`): run after it, the reload's test could see
  another request's candidate link and report a change that had worked as "not reloaded". Both routes
  read `{name}` through `httpx.URLParam`, since chi hands back the escaped segment (`vb%3A8080`).
  `DeleteSite` refuses a regular copy at `sites-enabled/<name>`, keeps another site's link there,
  and removes every alias to the deleted site's file, so no dangling link blocks the next reload. The bulk
  deletion path still uses `checkSiteDelete` to refuse cases it cannot remove safely.
  `parseCaddyfile` tracks brace depth so only top-level blocks are site addresses — `handle`,
  `header` and `tls` blocks were listed as server names — and says whether every site address is
  served over HTTPS: an address with a host is unless it is `http://` or on port 80 (localhost and IP
  addresses included, which caddy:2 serves from its local authority), one with no host is not, and
  under `auto_https off` only a block with its own `tls` counts. Caddy's one-site form without braces
  is read (the first line is the address), an `http://` block whose only directives redirect to
  `https://` is not a plain site — as an nginx port-80 block that only redirects is not — and a block
  opened and closed on one line counts. An address list carried onto the next lines by trailing
  commas is joined first; read line by line, its first line began the one-site form and every later
  site was taken for one of its directives. One plain site makes the Caddyfile's single entry plain.
  Names are listed once, without their scheme.
- **What the running nginx has not loaded.** `Pending` (`pending.go`, `GET /proxy/pending`, a read
  every account holds) compares the files nginx reads with the load it is running. nginx replaces all
  of its workers each time it loads its configuration and keeps them when a reload fails in the master
  — a port another process holds is the common case, which `nginx -t` does not check — so the start of
  the oldest worker that is not "shutting down" is when the running configuration was read, and `nginx
  -s reload` exiting 0 says only that the signal went out. The master is found in `/proc` (the
  dashboard's container shares the host's processes): its title names its `-c` and `-p`, or it reads
  the compiled-in configuration `nginx -V` gives, and only a master reading the main file `nginx -T`
  names counts — the host's nginx, one in another container and the live harness's all show up
  beside each other. Two that read the same path are told apart by the `pid` file that main file (or
  the build) names, read under `/host` first. A start in `/proc` counts clock ticks since boot and is
  placed on the wall clock through `CLOCK_BOOTTIME`, not `/proc/stat`'s whole-second `btime`, and
  rounded up to the tick, because a file saved and reloaded by a script lands within a hundredth of a
  second of the new workers. Each file `EffectiveConfig` returns (so only what `ReadConfig` shows) is
  judged by its change time — through its link, and the link's own — not its modification time, so a
  file moved or copied in with an old time still counts. `pendingTracker` (per `Service`) remembers,
  for the running load, the SHA-256 of every file unchanged since it: a file whose content is back to
  what nginx loaded is not pending however new it is (a dry-run test stages its candidate at the live
  path and writes the original back; a switch refused and undone re-creates its link — each of
  these calls `keepLoaded` before it writes, which notes the digest of a file still unchanged since
  the load, so this holds even when nothing asked what is pending first), and a file
  nginx loaded that it no longer reads is "removed" — a site disabled without a reload is still
  served. A file saved since the load that the tracker never saw loaded is "changed", or "added" for a
  link made since; the tracker is in memory, so after the dashboard restarts, a file changed and
  changed back before it first read the load reads as changed until the next reload. Each file names
  the Sites entry it belongs to (`Site`, `Layout`: a link in sites-enabled belongs to the
  sites-available file it serves, a conf.d file to itself). `Generation` (`<master pid>-<ticks>`) names
  the load; `?after=<generation>` waits up to five seconds, reading `/proc` every 100 ms, for a newer
  one, which is how the Sites page reads nginx again after a reload it asked for (a save without a
  reload reads at once). Workers all replaced without a reload — a crash or an OOM kill — keep the
  load when the master's error log says each one that went was killed by a signal (`exited on
  signal`, at alert level) and the new ones started right after; with no error log file to read,
  or one in another time zone, they are taken for a load. The cached `nginx -T` is dropped before a
  read when a file it names, or a directory its glob includes read, changed since it was taken, so a
  link made over SSH shows at once. When `nginx -T` refuses the configuration, `Problem` is its first
  error and the files are found by following the includes on disk from the main file (as nginx
  globs them), so a site linked since the load is still "added"; when the dump waits more than five
  seconds behind the service lock, only the files known to be loaded are judged. `Running` is
  false, with `Reason`, where nginx is not installed, no master reads the configuration, or two do and
  the pid file names neither; a master with no worker is running with no `LastReload`. A certificate
  nginx has not reloaded is not a configuration file, and is left to the served-certificate check.
  writing `access_log off;`. Statements sharing a line without a brace are split too — `gzip on;
  gzip_vary on;` read whole was gzip set to "on; gzip_vary on", which is not on, and the next save wrote
  `gzip off;` — and a comment after a statement is cut off where nginx would read one (outside quotes,
  where a word could start): certbot ends every line it adds with `# managed by Certbot`, which made
  the certificate path `…/fullchain.pem; # managed by Certbot`, so the form could not save a site
  certbot had touched.
- **`SetVHostEnabled` replaces a stale link.** Enabling a site whose `sites-enabled` entry already
  existed but pointed elsewhere returned success and changed nothing; it goes through `linkEnabled`
  now, so the switch saying on means nginx reads the file. `parseCaddyfile` tracks brace depth so
  only top-level blocks are site addresses — `handle`, `header` and `tls` blocks were listed as
  server names.
- **Site rename.** `POST /proxy/sites/{name}/rename` (`system.admin` + destructive, audited
  `proxy.site.rename` with the new name, path and whether it was re-rendered) takes `{to, reload}`.
  `RenameSite` in `sites_ops.go`, under the service lock, finds the file in sites-available or conf.d
  (a conf.d file keeps its `.conf`), refuses a link rather than a file, an existing target name or
  `sites-enabled/<to>`, a `just-dashboard-env-*` deployment route on either side and a `jd-*` owned
  file, then moves the file, moves its own `sites-enabled` link to the new name and re-points links under
  other names, runs `nginx -t` and puts file, content and links back on a refusal (422). A form-written
  file that still renders byte-for-byte is re-rendered under the new name (header and log paths — the
  renderer derives nothing else from the name); a hand-edited managed file moves unchanged and the
  answer carries a warning. History records a delete of the old path and a `rename` write of the new.
- **Bulk site changes.** `POST /proxy/sites/bulk` (`system.admin` + destructive, audited
  `proxy.sites.bulk` with the action, the names and what changed) takes `{action: enable|disable|delete,
  names}` (at most 200, each once) and `BulkSites` in `sites_ops.go` applies it under the service lock
  as one change: every site's link or file is changed with the same checks a single toggle or delete
  makes, `nginx -t` runs once over the result, and a refusal (422, as a link change) or any site the
  action cannot apply to (400 naming it) puts every site back and changes nothing. Unlike a single
  disable it does not go ahead over a configuration nginx already refuses. Deletes keep `<file>.bak`
  only once the test passed, with the original file's permissions; a backup that cannot be read or
  written refuses the whole change (`TestBulkDeleteKeepsRestrictedBackupsAndRefusesAnUnreadableBackup`).
  One reload follows. Sites already as asked come back as `unchanged`.
  The listing's `roots` and `redirects` (from `root` and `return 30x URL`) feed the list's search and
  its Static and Redirect chips.
- **Site files.** `sites_files.go`, all `system.admin` and audited. `GET /proxy/sites/{name}/download`
  (`proxy.site.download`) hands over one site's file, looked up in sites-available, conf.d, then
  sites-enabled, through `allowedPath` and never a password file. `GET /proxy/sites-export`
  (`proxy.sites.export`; under `/proxy`, since `/proxy/sites/export` would shadow reading a site called
  `export`) is a tar.gz of every listed nginx site as `<layout>/<name>` plus `manifest.json` (layout,
  enabled, aliases, server names, sha256); a file outside the nginx directory or a password file is
  listed as skipped, never packed. `POST /proxy/sites/import/preview` and `/import` take
  `{name, content, enable, reload}` (site name rules, no deployment-route name, text up to 256 KiB,
  refused when the name exists in either layout or in sites-enabled): the file is written into
  sites-available (conf.d where there is none) and linked even when it is to stay disabled, `nginx -t`
  runs, and a preview or a refusal (422) takes both back out; warnings name hostnames another site
  already serves and a file with no server block. `GET /proxy/site-backups` lists backup-suffixed
  plain files in sites-available and conf.d with the site each restores to; `POST
  /proxy/site-backups/{dir}/{file}/restore` (`{as, enable, reload}`) places the copy through the same
  test and keeps the backup; `DELETE /proxy/site-backups/{dir}/{file}` (destructive,
  `proxy.site.backup.purge`) removes it.
- **Import from Nginx Proxy Manager.** `npm_import.go`, `system.admin`, under `/proxy`. `POST
  /proxy/import/npm` (multipart `file`, at most 50 MB, audited as `proxy.import.npm.preview`) copies
  NPM's `database.sqlite` to a private temp file under `$TMPDIR/just-dashboard`, opens it
  `mode=ro&immutable=1` (tables only, 2000 rows each, 20 s), deletes the copy, and returns the items it
  maps to: proxy and redirection hosts as `SiteSpec`s (rendered and `ValidateSpec`ed), streams as one
  `StreamSpec` per protocol, access lists with users as password files under `jd-auth/npm-*` (NPM's
  plain passwords bcrypt-hashed at preview and never kept or returned). TLS is on only when a local,
  unexpired certificate and key cover every domain (NPM's own stay in its container); an access list
  the form cannot write faithfully, a disabled stream, a TLS-terminating stream and a disabled host on
  a conf.d host are skipped rather than imported weaker or live. Advanced configuration is shown,
  never written. Redirect hosts keep NPM's 301, 302, 307 or 308 and its preserve-path choice. An
  imported access list keeps `satisfy any` when it has an allowed address and a password, and clears
  `Authorization` to the upstream when NPM's `pass_auth` is off. A local TLS certificate also keeps
  NPM's HSTS subdomain choice. NPM's asset cache policy is reported for manual setup because its
  settings do not map to the site's cache options. A representative SQLite export is covered by a
  preview, apply and real-nginx request test, including automatic password-file dependency import;
  `JD_NPM_AUDIT_DB` can point that test at a private copy of a database initialized by NPM with its
  fixture rows 7–9. Both paths were run against real nginx, and soft-deleted NPM rows stayed out.
  Conflicts (name taken, hostname served, stream port forwarded, password file present)
  are rechecked at apply. The plan lives in memory for 15 minutes behind a token bound to the actor.
  `POST /proxy/import/npm/apply` `{token, ids}` (`proxy.import.npm`) adds required password files,
  writes every file and links every site, runs `nginx -t` once (422 and everything taken back out on a
  refusal), unlinks the sites NPM had off, records the site and stream writes, and reloads once; a
  spent or foreign token is 410.
- **Catch-all default site.** `default_site.go` keeps one owned file, `jd-default` (sites-available plus
  its link, or `conf.d/jd-default.conf`, first line carrying `OwnedMarker`, so the Sites list leaves it
  out). `GET /proxy/default-site` (a read every account holds: listen lines of files the listing already
  shows, no probe) reads who answers an unknown Host on each socket today from `EffectiveConfig` +
  `NginxTree` — the `default_server` claimant, else the first server nginx reads — and the plan: it
  always claims `*:80`, and `[::]:80`, `*:443`, `[::]:443` only where some site already listens there,
  because a reload that has to bind a new port fails where `nginx -t` (which does not bind) passed;
  443 is skipped when a site serves plain HTTP on it, since `ssl` on one listen line turns the socket
  to TLS for every server on it. Sockets on a named address are reported as not covered. `PUT`
  (`system.admin`, `{choice: close|not_found|redirect|page, redirectTo}`, audited
  `proxy.default_site.apply`) answers 409 `other_default` naming each other file's `default_server`
  on a socket it would claim, before writing anything; otherwise it writes the file (and, for
  `page`, a plain `<nginxDir>/jd-pages/default/index.html` when none exists), links it, runs
  `nginx -t` and undoes all of it on a refusal (422, as a link change), then reloads. The choice sits
  in `location /` rather than the server so the ACME location (`/srv/just-dashboard-acme`) still
  answers: a server-level `return` runs before locations are matched. On 443 the second server is
  `ssl_reject_handshake on` with no certificate. A redirect target is a scheme and host only
  (`$request_uri` is appended; `$`, quotes, `;`, braces refused). `DELETE` (destructive, audited
  `proxy.default_site.remove`) removes the file and its link and reloads; the page directory stays.
  QUIC is not claimed: `quic` listen lines are left to the site that has them, since taking
  `reuseport` means rewriting that site's file.
- **Certificates say who uses them.** `listCertificates` joins the sites' `ssl_certificate` paths
  onto the certificate list (`UsedBy`), through symlinks, so a certbot lineage and the site naming
  its `live/` path are one entry; `certificateName` names a file in a generic directory
  (`/etc/nginx/ssl/site.crt`) after itself rather than after "ssl". `CertbotState.RenewUnit` is a
  certbot timer systemd knows but is not running, which the Certificates page enables and starts
  through `/systemd/{unit}/enable` and `/start`. `DNSProvider.HasCredentials` reports a saved
  token without reading it, and `DELETE /certificates/dns-credentials/{provider}` removes one
  (destructive, ordinary confirmation, audited as `certificates.dns.credentials.remove`).
- **Deleting a certificate revokes nothing.** `POST /certificates/delete {names, force}` runs
  `certbot delete --non-interactive --cert-name` (`DeleteArgs`) for each lineage in turn in one
  `certbot.delete` job; `DELETE /certificates/imported/{name}?force=1` removes
  `importedDir/<name>` (a real directory only, named by `importNameRe`; Caddy release copies are
  refused and stay with the evidence prune). Both are destructive with an ordinary confirmation,
  audited as `certificates.delete` / `certificates.imported.delete`, and answer 409 `in_use`, the
  message naming the sites, while an enabled nginx site's `ssl_certificate` resolves inside the
  lineage or import — unless `force`, which the page sends only after its dialog has named those
  sites. Host-Caddy `tls` file references are not detected. The page's "Delete expired and unused"
  checklist offers expired certbot/imported certificates whose `UsedBy` is empty and never forces.
- **Where an issuance or a renewal failed, and whose it is to fix** (`issue_diagnosis.go`).
  `DiagnoseIssuance` reads a certbot run's output — the authority's per-domain report (`Domain:`,
  `Type:`, `Detail:`), or for a run that failed before or beside validation its meaningful lines, never
  progress such as "Account registered." — into problems, each with a stage and that stage's owner:
  `dns` (NXDOMAIN: the DNS provider or registrar; SERVFAIL, timeouts, refusals: the authoritative
  nameservers), `caa` (the zone's CAA records), `connect` (a timeout: a firewall in front of port 80,
  this host's or the provider's; refused on this host's address: whatever answers port 80 here; any
  failure at an address that is not this host's: whoever holds it — the record points there, or a
  provider maps it here), `challenge` (an invalid response: the web server answering the name on
  port 80), `dns-plugin` (the plugin's credentials or zone), `dns-01` (a TXT record not found or wrong:
  the provider and the propagation wait), `rate-limit`, `account`, `local-port` (certbot's own server
  on an occupied port 80), `installer` (the nginx plugin) or `unknown`. The address the authority
  reached is taken from its words, and `here` says whether it is one of this host's interfaces' —
  set only where that can be said: a host with no public address of its own in that family may be
  behind the provider's NAT. Each problem carries an action and links to the page holding the owner's
  evidence: the Network DNS lookup and delegation tools, the TLS page's DNS and CAA panel, the host
  firewall, external checks, the listening ports on 80, the Sites page's URL resolver, the HTTP tool
  on the challenge path, the rate-limit and account panels. `GET /certificates/jobs/{id}/diagnosis`
  (`system.admin`) reads a failed `certbot.*` job's held output (404 for another kind; a job that did
  not fail has none), and `RenewalHealth.problems` carries the last failed renewal run's — none once
  the run is `recovered` — which the `renewal_failed` alert appends as "domain at stage — owner". The Certificates page's issuance
  console and a site form's certificate step show **Where it failed** under a failed job
  (`issuance-problems.tsx`), and the renewal notice shows the renewal's. Tests:
  `TestDiagnoseIssuanceReadsEachDomainsStageAndOwner`, `TestDiagnoseIssuanceReadsRunFailures`,
  `TestFailedRenewalRunCarriesItsProblems`, `TestCertJobDiagnosisReadsTheFailedRun`, and
  `TestLiveCertbotFailureIsReadByStage`, where the host's certbot orders over webroot, into private
  directories, from a certificate authority on loopback that fails the challenge as a real one does
  (connection, DNS, unauthorized and CAA problems), and its real output is read into each stage —
  no request leaves the host and no certificate exists.
- **Issuing (`certbot_issue.go`, `PlanIssue`).** `IssueRequest` takes `keyType` (`ecdsa`/`rsa`),
  `rsaKeySize` (2048/3072/4096) and `certName` (`certNameRe`, passed as `--cert-name`: names are
  added to a lineage by resending its whole list under its name, which avoids `--expand`). A key of
  another type or size than an exact-names lineage's adds that lineage's `--cert-name` (certbot
  refuses the change non-interactively otherwise) and `--force-renewal` on a real run. The email may
  be empty only when `accounts/<host+path>` under certbot's directory holds an account for the server
  this run orders from (staging for a test run). A webroot must pass `test -d` on the side certbot
  runs on. `POST /certificates/issue/preview` (system.admin) answers the same checks with the argv and
  runs nothing; DNS tokens appear only as their file path, and pasted credentials are checked there
  and saved by the issuance job only. `GET /certificates/account` (system.admin) runs `certbot
  show_account` when an account exists (it asks the authority, briefly holding certbot's lock) and
  returns its server, URL, thumbprint and first email contact.
  **Authorities and accounts** (`acme_accounts.go`): `IssueRequest.ca` picks `letsencrypt`, `zerossl`,
  `google` (Google Trust Services) or `custom` with an https `directory`; empty keeps
  `JD_ACME_DIRECTORY`/Let's Encrypt. The choice drives `--server`, the account check, the CAA
  identifier the preflight judges against (`sectigo.com`, `pki.goog`) and whether Let's Encrypt's
  limits apply. Buypass is not offered: it stopped issuing ACME certificates in October 2025 (its
  directory still works as `custom` if that changes). ZeroSSL and Google need External Account
  Binding to register: `eabKeyId`/`eabHmacKey` on the request are checked, the HMAC key saved by the
  job sealed with `auth.Sealer` in `acme_eab` (keyed by directory), and, only when certbot has no
  account there, written with `mktemp` + `tee` (stdin) on certbot's side as a 0600 file passed as
  `--config` and removed when the job ends — never argv. `GET /certificates/accounts` (system.admin)
  walks `accounts/**/regr.json` (never `private_key.json`), asks `certbot show_account --server
  --account` for each (at most 8, sequentially, for the lock) and lists the offered authorities with
  `eabSaved`/`account` flags, never the key. `POST /certificates/accounts/email` (system.admin,
  audited) runs `certbot update_account --no-eff-email -m` for an account found on disk, as an
  exclusive certbot job.
  `POST /certificates/issue/preflight` (system.admin, `issue_preflight.go`) checks the same request
  before a run and changes nothing it leaves behind: each name's resolution (`CheckDomainDNS`), its
  CAA set (a raw CAA query to `/etc/resolv.conf`'s nameservers via `x/net/dns/dnsmessage`, climbing
  to the first non-empty set, judged against `letsencrypt.org`; another authority's identifier is
  unknown, so a restricting set is a warning), port 80 for the method (standalone needs it free,
  nginx held by nginx; the nginx plugin's own challenge block is not probed), and for webroot a
  `mktemp` token written on certbot's side under `<webroot>/.well-known/acme-challenge/`, fetched over
  loopback with each name as Host, then removed with any folder it had to create. The lineage relation
  (identical / replaces / expands — which certbot refuses under `--non-interactive` — / new) comes from
  the renewal confs. `GET /certificates/rate-limits?domains=` (system.admin, `ratelimits.go`) counts
  production certificates in `archive/*/cert*.pem` by notBefore within seven days (duplicates of the
  exact set of 5, per registered domain by the public suffix list of 50) and failed non-test
  `certbot.issue` jobs this process still holds within an hour (5); the preflight turns a reached limit
  into a critical finding for a real run. No audit entry: nothing persists. The Issue dialog runs the
  preflight as its fields settle (and on Re-check), lists what did not pass, draws the limits as
  meters, and holds a real run on a critical finding until "Issue anyway" is switched on for that
  exact request; the server does not enforce it.
- **Two layouts, and files that are not sites.** `nginxVHosts` (`vhosts.go`, with the rest of the
  listing and `SetVHostEnabled`) lists every place nginx takes a site from, each entry carrying its
  `Layout`: sites-available where it exists, conf.d always (a Debian nginx.conf includes it too; on
  every RPM distro, Alpine and Arch it is the only one), and on a Debian host whatever is only in
  sites-enabled — a copied file, a link to a file kept elsewhere, a link to nothing. nginx reads all of
  sites-enabled, so a backup-named file there is listed. A site's link is judged by identity
  (`readEnabledLink`, `os.SameFile`), not by its text: serving the file is `Enabled`, a link to nothing
  is `Broken: "dangling"` — nginx refuses every reload while it is there, and Lstat used to report it as
  serving — and a link to, or a copy of, another file is `Broken: "stale"`, with `LinkTarget` saying
  where it points and `TargetServedElsewhere` whether nginx also reads that file through another name
  in sites-enabled or through conf.d — when it does not, the site's Enable, which points the link at
  the site's own file, takes it out of nginx, and the page asks before it; the other names in sites-enabled that link to a sites-available file are its
  `LinkedAs`, make it `Enabled`, and are not listed as sites of their own. `ResolvesTo` is the real
  path of a file outside the proxy's directories, which the editor refuses to open while its switch
  still works. A conf.d file is listed only when it declares a `server` block (read with
  `ParseNginxFile`; one it cannot parse is listed): a file holding only a `log_format`, `map` or zone
  is configuration other sites depend on, and listed as a site it read "Default host" with a Delete.
  conf.d has no symlink, which reaches the UI as an empty `EnabledPath`, because a switch that can only
  error is worse than none. `FormEditable` says the site form reads the file and
  saves it back to the same place (sites-available, or a `.conf` in conf.d where there is no
  sites-available); a conf.d file on a Debian host or a file only in sites-enabled would be saved beside
  itself under the same names, so it gets the raw editor. `confdPath` stops `app.conf` becoming
  `app.conf.conf`. Names, listens and upstreams are listed once each (a forced-HTTPS site is two server
  blocks with the same names); `CertPaths` holds every `ssl_certificate`, unquoted, including one in a
  snippet the site includes (one level, only files the editor would show), with `CertPath` the first;
  and a `listen … ssl` or `quic` is TLS whether or not the certificate is named in the file. The
  listing **skips backups** in sites-available and conf.d (`isBackupFile`: `.bak`, `~`, `.dpkg-old`,
  `.rpmsave`) — nginx reads none of them there, and since delete keeps `<name>.bak`, without the filter
  deleting a site produced a second site.
- **What a site does, who wrote it, and what is not a site.** `siteDetails` (`site_discovery.go`)
  reads each site file with `ParseNginxFile`. `Pools` are its `upstream` blocks with their servers, so a
  `proxy_pass http://<pool>` reads as the servers behind it. `Features`, in `siteFeatureOrder`, are
  `auth` (`auth_basic`), `sso` (`auth_request`), `allow` (an `allow` anywhere, or a `deny` at the
  server's own level — the form's fences around dotfiles and backups deny inside a location and restrict
  nobody), `ratelimit` (`limit_req`, `limit_conn`), `cache` (`proxy_cache`), `ws` (an `Upgrade` header
  to the upstream), `h2` and `h3` (`http2`/`http3 on`, or `http2`/`quic` on a `listen`), and
  `maintenance` — an `if ($jd_<site>_maint)` in a server block, which the site form must write only
  while maintenance is on. An `include` the site file itself writes is followed one level, for files
  the editor would open, and never into the site's own file: a site that includes itself — by name,
  through its link, or through a glob such as `sites-available/*` in a server block — would otherwise
  be expanded into itself without end, and the stack overflow took the whole process down on every
  listing. `AccessLog` and `ErrorLog` are the files the site writes to: a log one of its
  server blocks names comes first (a forced-HTTPS site logs where its serving block says, not where its
  redirect block inherits), then the one nginx.conf sets — its `http` block's, or for errors the main
  context's before that (`inheritedLogs`; that file only, not what it includes). Both are empty for
  `off`, `syslog:`, `stderr`, a device, and a relative or variable path. The Logs page opens only the
  files it lists, so the API then clears either one it does not (`keepOpenableLogs`,
  `handlers_proxy_vhosts.go`, against `logsx.Discover`) — a file outside `JD_LOG_ROOTS`, one nginx
  has not created yet, a name the inventory skips — and the Sites page offers Access log and Error log
  only for a file the Logs page opens. A file nginx cannot parse gets none of these fields. `Package` is the dpkg package that installed the file, set only while the file still
  matches the md5 dpkg recorded for that conffile (`/var/lib/dpkg/status`, and
  `/host/var/lib/dpkg/status` from the dashboard's container; read again when either changes; obsolete
  entries ignored). That is Ubuntu's untouched `sites-available/default`, which the Sites findings no
  longer call "on disk but not serving" and the page does not sort among the sites needing attention.
  The recorded paths are absolute, so it is set only with `JD_NGINX_DIR=/etc/nginx`, and never on a host
  without dpkg. A file named `jd-*` whose first line contains `OwnedMarker` ("Just Dashboard owned") is
  the dashboard's own plumbing — the catch-all default site, a shared log format — and is left out of the
  listing in sites-available and conf.d, and so is any link in sites-enabled to one — but not a link
  under such a file's name that points at nothing or at another file: nginx refuses every reload over
  the first and serves the second, so it is listed from sites-enabled like any other link (a link to
  nothing with its Remove link); a feature that writes such a file puts the marker on its first line
  and shows the file itself. `Owner` is filled in by the API, not proxysvc (`markDeploymentRoutes`,
  `handlers_proxy_vhosts.go`): a name that `deploy.RouteNameFor` spells (`just-dashboard-env-<id>.conf`,
  on nginx and in the Docker Caddy ingress alike) is joined to `deploy_environments` and
  `deploy_projects` for the project and environment ids and names — an archived project's own name
  from `archived_name`, as the deploy store reads it, since its `name` column holds an
  `__jd_archived_<id>_<hex>` tombstone — with `archived` when either is archived; a name whose
  environment no longer exists has none. The Sites page leads such a route to its deployment in place
  of Edit, Disable, Duplicate and Delete, opens it read-only (its card and a `?site=` link alike), and
  keeps Edit anyway behind a confirmation; an archived owner's route is the operator's again. The backend does not refuse
  those verbs on a route, since deployments call proxysvc directly and an administrator may override.
  A deployment route nothing deploys any more — no owner (its project deleted) or an archived one — and
  that Delete does not apply to (every Docker Caddy route, which has no host file) has **Remove route**,
  on its card and its page: `DELETE /proxy/vhosts/{name}/deployment-route` (system.admin, destructive,
  audited as `proxy.deployment_route.remove`) accepts only a name `deploy.RouteNameFor` spells, answers
  409 `deployment_active` while an unarchived environment still owns it, and runs
  `proxysvc.RetireDeploymentRoute`: `RemoveDeploymentRoute`'s snapshot/restore removal, then on the
  Docker ingress `rm -rf <caddy certificates>/*/<name>` for each plain domain (checked against
  `certDomainRe`; wildcards are left) that only that route served, since Caddy goes on renewing and the
  Certificates page goes on listing a certificate it holds whether or not a route asks for it. A
  certificate it could not remove is reported as `certificateError` beside a removal that succeeded.
  Without this a route outlived its project, serving its domain, with nothing on any page to remove it.
  The overview draws such a route's application as its owner when no socket names its upstream — a
  deployment's container is dialled at its network address (`10.0.0.3:3000`), which no listener holds —
  so the route list, the route map and the traffic band carry the project's name and website icon
  (`ownedBackend`, `route-map.ts`), as the Sites cards already did.
  listing and `SetVHostEnabled`) reads sites-available where it exists and conf.d where it does not
  (every RPM distro, Alpine, Arch — most of the servers this runs on); the difference reaches the UI as an
  empty `EnabledPath`, because conf.d has no symlink and a switch that can only error is worse than none. `confdPath` stops `app.conf` becoming `app.conf.conf`, and `ReadSiteSpec` reads the listing's `app.conf` back as a spec called `app`, the name a save writes to — the spec kept `.conf` once, and the next save renamed the site's logs to `app.conf.access.log`. The listing
  **skips backups** (`isBackupFile`: `.bak`, `~`, `.dpkg-old`, `.rpmsave`) — nginx reads none of them, and
  since a delete, and a save over a hand-written file, keep `<name>.bak`, without the filter deleting a
  site produced a second site.
- **A password file must be readable by the account that reads it.** nginx opens `auth_basic_user_file` in
  a *worker* (www-data/nginx/http), not as the root that wrote it, so a 0640 root:root file is a 403 for
  every visitor and "Permission denied" in the log — which reads exactly like a wrong password.
  `nginxWorkerGID` takes the account from this host's `nginx.conf`, falling back to the three defaults;
  where none resolves the file is 0644, which is what `htpasswd` itself produces. `authDir`/`streamDir`
  hang off `JD_NGINX_DIR`, which exists precisely for hosts whose nginx is elsewhere. A missing file is
  not an nginx error: `nginx -t` passes, reloads succeed, and the site answers every login with 403 and
  an error line per request (checked on 1.26.3), so deleting a password file a site uses fails silently.
  That is why `ListAuthFiles` carries `usedBy` — every nginx site (sites-available, sites-enabled,
  conf.d, enabled or not, one include level deep, relative paths from the nginx directory) whose blocks
  name the file — and why `DELETE /proxy/auth-files/{file}` answers 409 `in_use` for such a file unless
  `?force=1`, auditing the sites it named. Caddy's `basic_auth` inlines its hashes and is not counted.
  Removing a file's last login keeps it empty, which asks for credentials again like a wrong password.
- **`import.go`** checks the key against the certificate **before** writing either: a mismatched pair is
  accepted by every text editor and refused by nginx at reload, which on a live server means finding out
  during an outage. Imports live in `/etc/ssl/just-dashboard`, so a renewal run can never prune a
  certificate it did not issue. The key is found among the blocks pasted (an `EC PARAMETERS` block in
  front of it is skipped; an encrypted key without a password is refused); the leaf is the
  certificate that key belongs to, wherever it sits in the bundle; the chain is followed by signature and
  saved leaf first without the root; `ChainComplete` means the chain reaches a root (the bundle's own,
  one the system trusts, or `JD_ACME_CA_ROOT`), not that more than one block was pasted. A name already
  imported is a 409 `certificate_exists` naming what is there; `replace: true` overwrites it and keeps
  the previous pair as `.bak`, so replacing is a write rather than a destructive act.
  **`import_formats.go`** turns what authorities send into that PEM first (`Service.Import`, and
  `Service.InspectImport` behind `POST /certificates/import/inspect`, system.admin, which runs every
  check with `dryRun` and writes nothing — it returns the chain, a name suggested from the CN, the import
  it would replace and the enabled sites serving it). A PFX (`pfx`, base64) is read by
  `x/crypto/pkcs12`, falling back to the host's `openssl pkcs12` for AES/PBES2 files, with the password
  in the child's environment, never argv; an encrypted key (legacy `Proc-Type` or PKCS#8 via
  `youmark/pkcs8`) is decrypted with `password` and saved decrypted, 0600, since nginx cannot prompt.
  With `fetchIssuer` (the operator's consent on the Inspect step) a chain that stops short follows the top
  certificate's AIA http(s) address, up to three hops: public addresses only on every dial including
  redirects, no proxy, 5 s, 64 KB, and a fetched certificate is kept only if it signed the one below.
  The password is never audited. Caddy's release
  copies (`caddy-<24 hex>`, `docker_caddy_certs.go`) share the directory and are refreshed through
  `keepCaddyEvidence`. `ListCertificates` keeps them: deployment activation
  (`ResolveDeploymentCertificate`), preflight, the route summary and the hostname suggestions find a
  release's certificate there, and on a Docker Caddy host a copy is the only pair covering its domains
  (the route summary names it with `certificateRenewedBy: "caddy"` and neither days left, a link to the
  Certificates page, which does not list it, nor an expiry finding: the copy is refreshed only when a
  release runs, and graded by its own expiry it warned about a certificate Caddy had already renewed).
  `CertificateInventory` — `GET /certificates/` — leaves out the copies no
  nginx site names, since Caddy renews what it serves and never these, and listed they raised an expiry
  finding apiece; their names are refused to an operator's import. On a Docker Caddy host it lists
  what Caddy serves instead, read from `/data/caddy/certificates` in one `docker exec` (a fixed `sh -c`
  script, the directory its argument): source `caddy`, named by domain, `usedBy` the Caddy routes whose
  names it covers, `expiring` always false (Caddy renews a third of the term early; expired stays true),
  and each release copy for its domain folded under it as `evidence`. When Caddy cannot be read the list
  carries one unreadable `caddy` entry saying why rather than silently listing none.
  `GET /certificates/evidence` (system.admin) lists the copies no Caddy route serves and no release names
  (`OrchestrationStore.ReleasedHostnames`, every hostname in any release's runtime snapshot, retired ones
  included, since any can be rolled back to); `DELETE /certificates/evidence` (destructive, audited as
  `certificates.evidence.prune`) takes the confirmed `names`, recomputes the orphans and removes only
  those still orphaned, answering `{removed, kept}`. An unreadable snapshot or Caddy route list refuses
  the prune rather than treating the release as naming nothing.
- **Certificates made here** (`csr.go`, `localca.go`, routes in `api/handlers_certificates_private.go`,
  mounted from `mountCertificateRoutes`). A **signing request** (`POST /certificates/csr`, system.admin)
  makes the key on this server — ECDSA P-256/P-384 or RSA 2048/3072/4096, PKCS#8, 0600 — and a request
  for host names and addresses with optional organisation fields, kept in
  `/etc/ssl/just-dashboard-private/requests/<name>/` (the directory, 0700, is the claim on the name: a
  second request under it is a 409 `request_exists`). `GET /certificates/csr` lists what waits, with the
  PEM request, never the key, and the certificate already kept under the name (`Replaces`), since
  renewing a bought certificate is a new request under the same name. `POST /certificates/csr/{name}/complete`
  takes only the authority's certificate: the leaf is found by the waiting key, a certificate for another
  key is a 400 naming it, and the pair goes through `importCertificate` — ordering, chain verdict,
  `replace` as for an import (409 `certificate_exists` without it) — then the request is removed; a name
  the certificate leaves out of the request is a warning. `DELETE /certificates/csr/{name}` (destructive,
  no phrase) deletes a request and its key. **Self-signed** (`POST /certificates/self-signed`) and
  **local CA** certificates (`POST /certificates/local-ca` makes the root, a 409 `local_ca_exists` after;
  `POST /certificates/local-ca/issue`) are 397-day server certificates for names or addresses, written
  beside the imports by `installPair` (key 0600 then certificate 0644, staged and renamed, a replaced pair
  kept as `.bak`) so sites and the inventory read them as imports. The root is ECDSA P-256,
  ten years, path length zero, in `/etc/ssl/just-dashboard-private/ca/` (0700; `root-key.pem` 0600): no
  route reads or exports its key, and `GET /certificates/local-ca/root.pem` hands every signed-in account
  the certificate alone as `application/x-x509-ca-cert`, since a root is made to be installed. The private
  directory is apart from `importedDir` because every directory there is listed as a certificate.
  A certificate is the local CA's when the root verifies it (`localCALeaves`), not by a note beside it;
  `CertificateInventory` marks those `LocalCA`, and `summarise` now lists a certificate's addresses among
  its `Domains`. `GET /certificates/local-ca` (every signed-in account) is the root, each certificate it
  signed with the sites naming it and when it renews, and the daily check's last pass and next run.
  **The daily check** (`localCARenewal` in `api/modules_proxy.go`'s lane F sections: five minutes after
  start, then every 24 hours) runs `RenewLocalCALeaves`: every certificate within 45 days of expiry — well
  before the page's 30-day warning — is signed again for 397 days on the key it has, so the key a site
  names never changes, the previous certificate kept as `.bak`; nginx is then reloaded once if an enabled
  site names one, and a failed test is the pass's error ("renewed, but nginx was not reloaded …"). A pass
  that renewed or failed anything is audited as the system (`certificates.local-ca.renew`). Self-signed
  certificates are never renewed: a new one is a certificate every device has to be told about again.
  Writes are serialised by `privateMu`, never the service lock. Tests: `csr_test.go`, `localca_test.go`
  (the sweep with a fake clock, openssl cross-checks) and `TestLiveNginxServesALocalCACertificateAndItsRenewal`
  (the host's nginx serving an issued certificate that a client trusting only the root accepts by name
  and address, then serving the renewal the check reloaded it for).
- **`ct.go`** — the Certificate Transparency monitor, off until an administrator switches it on, since
  every check sends this host's domain names to crt.sh (setting `proxy.ct_monitor`; `PUT
  /certificates/transparency` on, `DELETE` off, destructive with no phrase; both audited as
  `certificates.transparency.enable`/`.disable`). `GET /certificates/transparency` (system.admin) is
  `{enabled:false}` while off and asks nothing; on, it reduces every vhost server name and certificate
  name to its registered domain (`RegisteredDomains`, `golang.org/x/net/publicsuffix`; names under no
  public suffix are skipped), asks crt.sh for the first ten (`q=<domain>` and `q=%.<domain>`,
  `exclude=expired`, three at a time, 45s each, answers over 8 MiB refused rather than listed in part)
  and keeps each domain's rows for six hours (`CTMonitor` in `modules_proxy.go`'s lane F sections;
  failures are not cached, one query per domain is shared between readers). Verdicts are made on each
  read: a certificate is `ours` when its serial is one held live, imported or in certbot's `archive/`,
  and `unexpectedIssuer` when it is not ours and its issuer's organisation signed none of this host's
  publicly trusted certificates (never set when there are none to compare with). A precertificate and its
  certificate share a serial and are listed once. The names come from the whole site list
  (`ListVHosts`), so while the Docker ingress cannot be looked up the report answers the lookup's own
  code with its reason (503 `ingress_unreachable`, 409 `ingress_ambiguous`, …; `proxyReadError`), not
  500 and not a report on the names that could be read as if they were all.
- **`streams.go`** — nginx's `stream` is a sibling of `http`, so a stream cannot live under
  sites-available. They go in `<JD_NGINX_DIR>/stream.d`, and the page says plainly when `nginx.conf` does not
  include it. nginx.conf is never edited silently: the one change the dashboard makes to connect the
  directory (below) is shown before it is made and asked for.
  - **The module comes first.** Debian and Ubuntu build the stream module dynamic and ship it as
    libnginx-mod-stream, which a plain `apt install nginx` leaves out; there a `stream` block is an unknown
    directive, `nginx -t` fails and every reload is refused. `nginx_modules.go` (`StreamModule`) reads
    `nginx -V` word by word (`--with-stream`, `=dynamic`; `--with-stream_ssl_module` is not the module), the
    `load_module` lines of a `nginx -T` dump, nginx's own "unknown directive" when a block is already there,
    and the `.so` on the host, into static / loaded / not-loaded / not-installed / absent / unknown; a `.so`
    that is absent is not-installed even when the dump fails. For not-installed the list handler names the
    package (`ModulePackage`: `libnginx-mod-<name>` for apt, `nginx-mod-<name>` for dnf/yum/apk,
    `http-xslt-filter` for XSLT) only when the package manager has it (`updates.Describe`, remembered ten
    minutes in `proxyExtras.modulePackages`, asked through its `catalogue` — the updates service, which
    tests replace with a fake: an `ErrUnknownPackage` names nothing, any other failure names it for the
    install to explain), and the page installs it through `POST /packages/install` as a job — Debian's
    package links `modules-enabled/50-mod-stream.conf` itself. Unknown is never reported as missing.
  - **The module report** (`NginxModules`, `GET /proxy/modules`, every signed-in account) lists each
    `--with-` module `nginx -V` names — not threads, compat or the compiler flags — with `version`, `openssl`
    and `modulesPath`, each static / loaded / not-loaded / not-installed / unknown (the configuration could
    not be read, `detail` says why) and, for not-installed, the package as above. Kept a minute per service
    (`?fresh=1` asks again); 503 `not_installed` without nginx. Lane A's engine line reads it.
  - **Connecting the directory** (`main_dropin.go`). `GET /proxy/streams/include/plan` (system.admin)
    works out the change without writing: a drop-in `zz-just-dashboard-stream.conf` holding
    `stream { include <dir>/*.conf; }` in the first directory nginx.conf includes at its top level by a
    glob it matches (Ubuntu's `modules-enabled/*.conf`, so nginx.conf stays the conffile its package
    shipped; `zz-` sorts after the modules' own files because nginx refuses a `load_module` after a block —
    a load_module read after the drop-in sends it to the next mode); else the block appended to nginx.conf
    between `# Just Dashboard: streams begin.`/`end.` markers in the file's own line endings; else, where a
    top-level stream block exists (a second is "duplicate"), one `include …; # added by Just Dashboard`
    line before its closing brace, found by an offset scanner that reads comments and quotes as nginx does.
    An nginx.conf edit carries `reason` — `no-directory`, `load-module-after`, `directory-elsewhere` (the
    directory leads outside `JD_NGINX_DIR`) or `name-taken` (a file of the drop-in's name that is not the
    dashboard's), the first such directory's — and `dropIn`, where the drop-in would have gone, so the
    sheet never gives another mode's reason. The plan is checked with `NginxTree` (the changed
    configuration must read the directory in a stream block), refuses a file outside `JD_NGINX_DIR` as the
    connect would, and lists the staged stream files and their sockets that another program or another
    staged stream holds. For an edited file it decides the copy then: `keepsCopy` is false, with a
    warning, where an include in the changed configuration would read `<file>.jd-stream-<unix>.bak` by the
    file's configured name or its resolved one (beside a stream block, a second one), and the connect
    keeps exactly the copy the plan named. Refusals are 409s with codes: `module_missing`, `already_included`, `include_misplaced`,
    `stream_block_elsewhere` (outside `JD_NGINX_DIR`), `config_unreadable`. `POST /proxy/streams/include
    {mode, path, reload}` re-plans under `s.mu`, refuses a plan that changed (`plan_changed`) or has
    conflicts (`port_in_use`: `nginx -t` does not bind, and the failed bind would poison every later
    reload), keeps the plan's copy of an edited file where it named one, writes
    through the resolved path, runs `nginx -t`, and on failure puts the bytes back (mode and CRLF kept, the
    copy removed; 422 `invalid_config`). `POST /proxy/streams/include/remove {reload}` (destructive — a
    POST, since `DELETE /{name}` would take a stream called "include") takes out exactly what a connect
    wrote — the unchanged drop-in, the marked block, the marked line — and nothing written by hand
    (`not_connected`), with the same test and restore. The listing's `connection` names the dashboard's
    own include, and `streamBlock` the file whose stream block the manual snippet (then an `include` line)
    goes into. Audited `proxy.stream.include.add` / `.remove` with mode, file, copy, streams and reload.
  - **Pausing a stream** (`stream_pause.go`): `POST /proxy/streams/{name}/enabled {enabled}` (system.admin,
    destructive, audited `proxy.stream.toggle`) moves `stream.d/<name>.conf` to and from `stream.d/paused/`,
    which the `*.conf` include glob does not reach. Both directions run `nginx -t` when the directory is
    read and move the file back on failure (422 `invalid_config` — a pause fails only under a hand-written
    glob that takes the `paused/` directory itself). Resume refuses a taken name (`stream_exists`) or a
    socket another stream, site or program holds (`port_in_use`), and a reload nginx fails on the stream's
    own port puts it back in `paused/`, as a save does. A symbolic link is not paused (`stream_linked`):
    deleting the link stops it. The listing returns paused files in `paused`, apart from `streams`, with
    state `paused`; a delete takes a paused name too, without a reload, and a new stream or a rename onto
    a paused name is `stream_exists`.
  - **Where stream.d is read** (`stream_state.go`) is `NginxTree` over the files on disk, with a probe file
    placed where a new stream would go: an include inside `http` is named as misplaced, a `stream` block in
    its own file and a relative include count, and `upstream` or `other-stream.d` no longer do. It reads
    from disk because it is asked with the lock held (`includeNote`) and while the configuration fails.
  - **Hand-written files** are parsed token by token. `ParseStreamSpec` returns what the form cannot express
    (an upstream server option other than weight/max_fails/fail_timeout/backup/down, TLS, a port range,
    an unknown directive) and the save refuses to
    overwrite such a file (`HandwrittenStreamError`, 409 `stream_handwritten`); the raw editor is the way
    in. A bind address, `10m`-style times, a direct `proxy_pass`, `unix:` upstreams and the UDP mode are
    read and written back, so a loopback forward is never saved onto every interface. A hand-written file
    the form rewrites is kept as `.bak`.
  - **Access and limits.** `StreamSpec.Rules` (`{action: allow|deny, source}`) with `DefaultAllow` is the
    ordered access list: rendered in order, then `allow all;` or `deny all;`, and read back with the first
    rule for `all` as the default (nothing after it is reachable, so it is dropped). Rules that only allow,
    with everyone else denied, are folded into `AllowFrom` on save and on read, so one file reads back one
    way; `allowFrom` and `rules` together, `all` as a rule's source, and denying everyone with no rule are
    refused. `MaxConnPerIP`/`MaxConnTotal` render `limit_conn_zone $binary_remote_addr` /
    `$server_port zone=<NginxIdent>_conn_ip|_conn_all:1m` at the top of the file (zone names are global to
    nginx, http included) and `limit_conn` in the server; `UploadRate`/`DownloadRate` (KiB/s, per
    connection) render `proxy_upload_rate`/`proxy_download_rate`. Only zones declared under the stream's own
    names, each used once, read back; any other zone or cap is hand-written.
  - **Pools.** `StreamSpec.Servers` (`{address, weight?, maxFails?, failTimeout?, backup?, down?}`) with
    `Balance` (empty round robin, `least-conn`, `client-ip` = `hash $remote_addr consistent`, `random`) and
    `NoRetry` (`proxy_next_upstream off;`) render into the stream's upstream block, the method first. nginx
    defaults are folded (weight 1, max_fails 1, fail_timeout 10s), and a pool of one folds into `Upstream`
    with its method, retry and options dropped, since nginx counts, weighs and retries nothing for a lone
    server; `Upstream` is always the first server. Refused: backup under client-ip or random (nginx has none
    there), every server down, no up server that is not a backup, a duplicate address, more than 32. The
    upstream Test dials every server not marked down.
  - **TLS.** `TLS` with `CertPath`/`KeyPath` puts `ssl` on every listen and writes `ssl_certificate` and
    `ssl_certificate_key`; `UpstreamTLS` writes `proxy_ssl on`, `UpstreamName` adds `proxy_ssl_name` with
    `proxy_ssl_server_name on` (without a name nginx would check against the generated upstream name), and
    `UpstreamVerify` adds `proxy_ssl_verify on` with `proxy_ssl_trusted_certificate UpstreamCA` and needs
    the name. TCP only (no DTLS); paths match `absPathRe`, the name `domainRe` without a wildcard; an end
    that is off drops its fields. `StreamModule.SSL` reads `--with-stream_ssl_module` from `nginx -V`, and
    `ApplyStream` refuses TLS with `ErrNoStreamSSL` (409 `stream_ssl_missing`) when the build lacks it — a
    file saved for later would otherwise fail the reload that connects the directory. `Certificate.UsedByStreams`
    lists the unpaused streams serving a certificate (source `stream:<name>` when only a stream names it).
    `proxy_ssl_verify_depth`, protocols, ciphers and client certificates are hand-written.
  - **Routes by TLS name** (`stream_sni.go`). `Routes []{name, upstream}` writes `map
    $ssl_preread_server_name $<ident>_sni { hostnames; <name> <ident>_sniN; default <ident>_backend; }`, one
    `upstream <ident>_sniN` per route (a host name resolves at load, no resolver needed), and `ssl_preread on;
    proxy_pass $<ident>_sni;` in the server. The pool is the default for any other name, no SNI, or non-TLS.
    TCP only, refused with `TLS` or `UpstreamTLS` (the TLS passes through unopened); names are lowercased,
    must match `domainRe` and hold a dot (so no map keyword like `default` or `include`), at most 64, no
    duplicates; route upstreams go through `validStreamUpstream`. `StreamModule.Preread` reads
    `--with-stream_ssl_preread_module`, and `ApplyStream` refuses routes with `ErrNoStreamPreread` (409
    `stream_preread_missing`). The parser reads the map, the route blocks and `ssl_preread` back and names
    any other shape. The stream module's variables hash has the same 46-character limit, so a stream
    whose `<ident>_sni` would pass it names the variable from `NginxVarIdent`; the parser takes either
    spelling. A site already on the port (443) is refused by the port check like any other holder.
    The upstream Test dials the pool only, not the routes' backends. Audited as `routes` in `proxy.stream.apply`.
  - **Listen options** (`stream_listen.go`). `ListenEnd` makes `listen <first>-<last>` (at most 100 ports;
    `streamBinds`, `freePort`, the session list and `configClaims` via `listenBinds` all cover every port of a
    range). A range refuses `MaxConnTotal`, whose zone is keyed on `$server_port` and would cap each port
    apart. `SamePort` (range only) writes `proxy_pass <ip>:$server_port` with no upstream block: `Upstream` is
    then an IP address alone (a name would need a `resolver`), one server, no routes. `AcceptProxy` with
    `TrustedProxies` (TCP only, 1–32 addresses/CIDRs, never `all` or a /0) puts `proxy_protocol` on every
    listen and writes `set_real_ip_from` per entry; `StreamModule.RealIP` reads `--with-stream_realip_module`
    and `ApplyStream` refuses without it (`ErrNoStreamRealIP`, 409 `stream_realip_missing`). All three parse
    back; a proxy_protocol listen without `set_real_ip_from`, or on some listens only, is named. `Address` is
    checked against the host's interfaces (`StreamAddressError`; the backend shares the host network
    namespace) unless `ip_nonlocal_bind` is on — refused with 422 `address_not_local` on `spec.address`, and a
    warning in the preview. `GET /proxy/streams` returns the addresses as `addresses [{address, interface}]`
    for the form's picker (link-local IPv6 left out: a listen line cannot name its zone). Audited as
    `listenEnd`/`samePort`, `address` and `trustedProxies`.
  - **Testing a stream** (`stream_dial.go`). `POST /proxy/streams/test {target, port, protocol, mode,
    query?}` (system.admin — it dials an address the caller chose — and audited `proxy.stream.test`) dials
    from the host's network namespace inside 5 s: `mode: upstream` the forward's target, `mode: nginx` the
    stream's own port (the page sends 127.0.0.1 or ::1 for a wildcard listen). TCP waits up to 2 s for a
    banner; `query: dns` (default for port 53) sends a root NS query, over TCP with its length prefix, and
    `ntp` (default for 123, UDP only) a client packet; any other UDP port is answered `silent` without
    sending, since a datagram the service does not understand proves nothing. Outcomes: connected,
    answered, closed (nginx accepted and dropped — an access rule, a cap or an unreachable upstream),
    silent, refused, timeout, dns, unreachable, error. A hostname upstream carries a warning that nginx
    resolves it once per reload. `unix:` upstreams are not tested (the socket path may not be in the
    container). netsec's PortCheck/BannerGrab are not reused: their own 6 s timeouts and a read that
    ignores the context would overrun the 5 s cap, and they answer in prose rather than an outcome.
  - **Traffic** (`stream_traffic.go`). `LogConnections` (on by default for a new stream in the form)
    renders `log_format jd_<ident>_log '$msec $remote_addr $protocol $status $bytes_sent $bytes_received
    $session_time "$upstream_addr" $server_port';` at the top of the stream's own file and
    `access_log /var/log/nginx/stream-<name>.log jd_<ident>_log;` in its server; the parser reads back exactly
    that pair (and `access_log off`), anything else is unsupported. The format lives in each file rather than
    a shared `_jd-*.conf`, so a paused or copied stream carries it and there is no second managed file.
    `GET /proxy/streams/{name}/traffic?window=1h|24h|7d` reads at most 8 MiB from the end of the log and its
    `.1` rotation (a `.2.gz` left unread makes `complete` false): sessions by bucket (by the time they
    ended), status mix, bytes each way, session-length percentiles and the 12 busiest clients.
    `GET /proxy/streams/traffic` is the last hour of every logging stream for the list (1 MiB each), and
    `GET /proxy/streams/{name}/sessions` lists the ESTABLISHED TCP sockets on the stream's port whose owner
    is nginx or unknown (gopsutil; a UDP-only stream has no per-client socket and says so). All three are
    readable by every account, like the nginx access logs in the log viewer; a Deny or Allow on a client
    is an ordinary save of the stream (system.admin, audited `proxy.stream.apply`). The log line's
    quoted `$upstream_addr` is read too (`streamLogLine.upstream`), and `GET /proxy/streams/{name}/path`
    (`stream_path.go`, every account) joins the forward, its access list, the client sessions, the
    connections nginx holds to each backend now and the last hour by backend, for the connection
    investigator's stream evidence ([network-investigator.md](network-investigator.md#native-streams-on-the-path)).
  - **A save** (`ApplyStream(spec, previous, reload)`) refuses a new name, or a rename, onto a taken one
    (409 `stream_exists`) and a port another stream, a site or another program holds (`PortInUseError`,
    409 `port_in_use` on `spec.listen` with the next free port): `nginx -t` passes all three, and the
    reload then fails inside the master while `nginx -s reload` exits 0. What holds a port
    (`portClaims`) is read from the stream directory's files, the stream and http servers of the
    configuration nginx reads (`configClaims`: a site by its first server name, else its Sites-page name;
    an http `quic` listen is UDP, a listen-less server is `*:80`), and the running nginx's sockets; nginx's
    own sockets are no conflict (its configuration names what it asks for, and one it holds for a server
    that is gone is let go at the reload). `POST /proxy/streams/preview {spec, previous}` answers the same
    refusal as `conflict` (`PortOwner` plus `suggest` and `message`), so the form says it while the port is
    typed. A reload is then **watched** (`stream_probe.go`, `awaitListening`, up to 3 s, only when nginx
    reads the file as a stream): done when a worker that was not there before runs and nginx holds every
    socket (`listening: true`); a `bind()` failure nginx logs for the stream's own address puts the stream
    back as it was (new file removed, edit restored, rename undone) and is the same 409 with nginx's words
    in `BindError` — a stream nginx cannot bind makes every later reload on the host fail, so it is never
    left behind — audited `result: rolled-back`; any other `[emerg]` is `reloaded: false` with
    `reloadError` ("nginx did not take the reload up: …") and the valid file stays; a wait that runs out is
    `listening: false` with `listenNote`; a running nginx that cannot be read is `listenNote` alone. A
    rename moves the old file to `.bak` before
    one test and puts both back if it fails. It reloads only when asked ("Save for later" does not), and a
    reload that fails after a clean test is a 200 with `reloadError`, not "not applied". The upstream block
    is `NginxIdent(name)_backend`, so `a-b` and `a_b` no longer declare one upstream; UDP writes
    `proxy_responses 1` only in the one-reply mode (it made every datagram a new session for a game server
    or WireGuard); protocol `both` writes a TCP and a UDP listen on the same addresses, the port check asks
    for both sockets and names the one that clashed (`514/udp`), and `proxy_responses` touches its UDP side
    only; the idle (`timeout`) and connect timeouts are separate and written as nginx time (`10m`, `1h30m`).
    A file's zero timeout (`proxy_timeout 0`: nginx takes it and drops every connection) is `unsupported`, not unset.
    Delete reloads only when nginx read the directory, and returns a failed reload as `reloadError`. A
    stream directory that cannot be read is a retryable 500 `stream_dir_unreadable` ("Could not read the
    stream directory"), never an empty list.
  - **Each stream's state** (`fillStreamStates`, `state` / `stateReason` / `blocker` / `bindError` on
    every listed stream): `not-read` where the file is not in a top-level stream block of the tree (no
    module first, then the include missing or misplaced, a file that does not parse — every reload refused
    — or an include that does not take it), `shadowed` where a stream read before it has one of its exact
    addresses (nginx warns "conflicting server name" and gives the first every connection; a wildcard and
    one address on a port are not a shadow), `not-listening` where a site nginx reads has its port (nginx
    fails every reload on the second bind, verified on 1.27) or where the running nginx holds no socket
    for a bind — named with the program holding it, nginx's last logged `bind()` failure for that address,
    or "it last loaded its configuration before this file changed" (newest worker's start against the
    file's mtime), `live` where nginx holds a socket taking every bind (its wildcard serves one address;
    a live file changed since it was loaded says so), and `unknown` with why where the running nginx could
    not be read. **The running nginx** (`stream_probe.go`) is the master process whose title names this
    `nginx.conf` (`-c`, `-p`, else the build's `--conf-path`; two that do are told apart by the pid file,
    read on the host where the dashboard runs in a container), its sockets are its network namespace's
    `/proc/<pid>/net/{tcp,tcp6,udp,udp6}` — readable by any user, which is what lets a dashboard see an
    nginx in another namespace — held by the master's descriptors, or, where those cannot be read (the
    dashboard runs as another user), owned by nginx's uid; the host's listener list names another
    program only in nginx's own namespace. The error log is the configuration's first top-level
    `error_log` file, else the build's, read only under `/var/log` or `JD_NGINX_DIR` — the states are
    shown to every account and the dashboard runs as root. `nginx -V` is kept a minute per service.
  - **Links and unreadable files.** The listing names a symbolic link (`link`, and "a symbolic link" in
    `unsupported`, so the form opens read-only; a save over one is `stream_handwritten`) and says a link to
    nothing links to nothing. Delete (`StreamDeletion`) removes a link as a link, leaving its target, and
    removes a file it cannot read without a `.bak`, answering `link` or `unread` in place of `backup`: a
    dangling link fails `nginx -t` for the whole host and was the one file the page could not remove. The
    route unescapes the name (`httpx.URLParam`), since the page encodes it and the listing shows any
    `*.conf`. A directory included in the wrong block counts as read for the validation note
    (`streamDirRead`): nginx reads it there, and its test refuses every stream in it.
- **`htpasswd.go`** does bcrypt in process — `htpasswd` lives in apache2-utils, is not installed on a host
  running nginx, and would put the password in a world-readable argv. Changing a password is the same
  POST as adding a login (it replaces the user's line); the panel's 20-character generator runs in the
  browser, so the password crosses the wire once, in that POST body, and is never stored or audited.
- **`access_lists.go` — one list, every site that includes it.** An office range or a VPN in front of ten
  sites was ten copies of the same lines and ten edits when it changed. A list is
  `<JD_NGINX_DIR>/jd-access/<name>.conf` (name `[a-z0-9][a-z0-9_-]{0,62}`), holding only `satisfy`, the
  `auth_basic` pair and `allow`/`deny` — legal in http, server and location context — and a site takes it
  in with `include <path>;` wherever it wants the restriction. `ValidateAccessList` writes every address
  in `netip`'s canonical spelling, refuses `all` (a non-empty allow list is always closed with `deny all`,
  denials written first, as the site form does), a range with bits past its length (nginx would take
  `10.0.0.5/8` as `10.0.0.0/8` with a warning), a repeat (`10.0.0.1` and `10.0.0.1/32` are one), a zone,
  more than 512 entries a side, a prompt with quotes, `$` (nginx expands variables there), control
  characters or the word `off` (which turns the password off), and `satisfy any` without both an allow
  list and a password file (with no allow list every address passes and the password would be moot).
  `satisfy` is written only when a list has both addresses and a password, so a list adds no directive a
  site might already set. The password file is a `jd-auth` file by name and must exist when saved. Read
  back, a file with anything else — another directive, a block, a password file outside `jd-auth`, rules
  the form would reorder (compared directive by directive with its own rendering) — carries
  `HandWritten` with why, and a named password file that has gone is `AuthFileMissing`. `UsedBy` is every
  site in the listing (`nginxVHosts`, enabled or not) whose file includes the list at any depth, directly
  or through a file it includes (one level, as the listing follows a snippet), by absolute or relative
  path, `./`/`../` or glob. `SaveAccessList` writes, runs `nginx -t` with the list in place — so it is
  tested with every site that includes it — and on a refusal puts the previous bytes back (or removes a
  new list) and returns a `*RefusedError` whose lead says whose error it is: "nginx refuses <name> where
  a site includes it" (the error is in the list's own file: a site that already sets `auth_basic`, an
  include where the directives are not allowed), "nginx refuses the configuration with this change to
  <name>" (in a site's file), or "nginx already refuses the configuration without this change"; a passing
  save is recorded and reloads nginx inside the same hold of the service lock. `DeleteAccessList` is
  refused while any site includes the list (`AccessListInUseError`, naming them and which are disabled —
  a disabled site would fail its next enable), removes it, tests, and puts it back if nginx still reads it
  from somewhere the listing does not follow (nginx.conf, a snippet of a snippet); it keeps
  `<name>.conf.bak`, which the listing and nginx's `*.conf` skip, and reloads nothing, since nothing nginx
  loaded changed. Routes (`api/handlers_proxy_access_lists.go`, `mountAccessListRoutes`, mounted from
  `mountVHostRoutes` at `/proxy/access-lists`, all `system.admin` like the password files they name):
  `GET /` → `{dir, clientAddress, lists}`, where `clientAddress` is `httpx.ClientIP` for the page to check
  each list against; `PUT /{name}` with the spec and `overwrite` (false for a new list, 409 `exists` if
  one has the name) → `{list, validation, reloaded, reloadError?, reload?}`, 422 `invalid_config` with the
  lead and nginx's first error as the message and the test as `raw`, 400 for a spec it refuses; `DELETE
  /{name}` (destructive, ordinary confirmation) → 204, 409 `in_use`, 404. Audited as
  `proxy.accesslist.save` (entry counts, password file, satisfy, created, the sites it reached, reloaded)
  and `proxy.accesslist.delete`, refusals included. `TestLiveAccessListDecidesWhoGetsIn` drives a running
  nginx through each save: an allowed address, a denied one, a password, an IPv4 client against an
  IPv6-only list — and that under `satisfy any` a correct password also opens a location the site closes
  with `deny all`, which the form says.
- **`route_resolve.go`** answers "which site answers this URL": `ResolveRoute(tree, url)` replays nginx's
  precedence over `NginxTree(EffectiveConfig())` — the address:port group (a block on a specific address
  takes every request arriving there; a server without `listen` is `*:80`), `ssl` on that group, then
  server_name (exact, `.name` also exact, longest `*.` wildcard, longest `.*` wildcard, first regex,
  `default_server`, first block), server-level `return`, then locations the way
  `ngx_http_core_find_location` recurses (`=`, longest prefix, its nested locations, `^~` stopping that
  level's regexes, regexes in order, the 301 to a trailing slash for a `*_pass` prefix), then what answers
  (`return`, a `*_pass`, or files under `root`/`alias` with `try_files`/`index`). Each step carries its
  file:line; a `rewrite`, an `if`, or a regex Go's RE2 cannot compile marks the answer `certain: false`
  rather than guessing. `GET /proxy/resolve?url=` (`handlers_proxy_resolve.go`, mounted in
  `mountVHostRoutes`, `system.admin`, read-only, nothing is sent to the URL) → `RouteResolution`; 400 for
  an unusable URL, 409 `config_unreadable` when `nginx -T` fails.
- **Who may reach a URL from one address** (`effective_access.go`, `GET /proxy/resolve/access?url=&source=`,
  `system.admin` beside `/resolve`, read-only, nothing sent). `ExplainAccess` resolves the route, finds the
  server block and location chain again by file and line, and judges each layer for `source` (one IP
  address, as nginx sees the visitor; a range or a zone is a 400) in nginx's order: **Outside this host**
  (provider firewall, security group, CDN, NAT) is always `unknown`; then the host firewall, added by the
  handler with `netsec.JudgeFirewallFrom` (`netsec/reach_source.go`: JudgeFirewall's rule order for one
  source — the first inbound rule whose source holds the address and whose target covers the route's
  port decides, else the inbound default), judged for the source as the connecting address. A rule
  that may concern the port but cannot be judged for an address from its listing is passed over and
  returned rather than dropped, as `JudgeFirewall` drops it: a source that is not an address or a
  range, a ufw rule
  limited to an interface (`443/tcp on tailscale0`, as `ufw allow in on tailscale0` prints it) or
  written as a profile the host does not define (a defined one is read through `ufw app info`), an
  iptables rule limited to an input interface other than loopback, carrying a match other than its
  ports, a connection state, a comment and a REJECT's answer, or jumping to a chain that is not read
  (a state match without `NEW` never meets a new connection and is not one). When any of them acts
  otherwise than the verdict (`netsec.FirewallUndecided`), the layer is `unknown`, naming the rules and
  the verdict past them; otherwise the verdict stands and says how many were passed over
  (`TestJudgeFirewallFromOneSource`, `…ReadsUFWProfiles`, `…ReadsIPTablesListing`, on ufw's and
  iptables' own listings). Then the server block's rewrite-phase checks the site form writes — maintenance (`$jd_<id>_maint` with its `geo $jd_<id>_maint_ip` bypass list and the
  always-exempt ACME and page paths: 503 unless bypassed), Cloudflare only (`$jd_<id>_edge` over the
  Cloudflare geo file: 444 unless the address is one of Cloudflare's), the crawler block (by User-Agent,
  so an ordinary browser is let through); CORS preflight and hotlink checks answer particular requests
  and are not access; any other server-level `if` is a layer of its own that is `unknown`, never a pass.
  Then **Allowed addresses**: the allow and deny lines of the innermost level that sets any (a path's
  replace the site's, an included access list's are read where nginx includes them), first match,
  naming the deciding line and, for a line in `jd-access/`, the shared list as owner
  (`/proxy/sites#access-lists`); a `return` answers before addresses are checked, so the layer is
  `skipped` (an allow list does not restrict a redirect site). Then **Sign-in**: `auth_basic` and its
  user file, `auth_request` (single sign-on), each innermost and turned off by `off`; then the request
  limit in effect (its zone's rate, or the site's exempt list), and `ssl_verify_client` (`on` requires a
  certificate, `optional` lets the application decide). `satisfy any` is applied as nginx applies it: an
  address the allow lines refuse can still sign in, one an `allow` line matches is not asked, and one
  no line names is declined rather than allowed, so it is still asked (an allow list without `deny all`
  under `satisfy any` lets nobody past the password but the listed addresses). The verdict is
  `refused` (naming the refusing layers), `unknown` (a layer, or a route `certain: false` for anything
  but the judged server-level ifs), `credentials` or `admitted`; `no-route` where nothing answers the
  URL. The Sites page's **Which site answers a URL** takes an optional **From address** and draws each
  layer with its verdict, deciding lines and owner link above the route (`route-resolver.tsx`,
  `access-explain.ts`). Tests: `TestExplainAccessFollowsNginxsOrderAndInheritance`,
  `TestExplainAccessJudgesTheFormsOwnChecks` (rendered maintenance, Cloudflare-only, redirect and SSO
  sites), `TestExplainAccessEdges`, `TestExplainAccessDoesNotGuessAnIf`, `TestJudgeFirewallFromOneSource`,
  `TestRouteAccessExplainsLayersForAdmins`, and `TestLiveAccessExplanationAgreesWithNginx`, where the
  host's nginx binary on a private prefix answers 127.0.0.1 exactly as the explanation predicted for
  an open path, a deny ahead of an allow, and `satisfy any` with and without the address listed.
- **`certbot.go`** issues, renews and revokes. `renewalScheduled` has its own field because it is the real
  story behind almost every expired certificate: not a forgotten renewal, a timer that stopped months ago;
  `CertbotState` answers it before reading the lineages, whatever they say. A cron file whose command
  tests `! -d /run/systemd/system` (Debian's and Ubuntu's `/etc/cron.d/certbot`) is no schedule where
  that directory exists: it stands aside for the timer, so a stopped timer there reads as nothing
  renewing, with the timer to turn on. Issuance defaults to a test
  run in the UI (the real limit is five failures an hour), and a test run is `certonly --dry-run` — the
  whole exchange, nothing saved (`--staging` used to write a lineage holding an untrusted certificate and
  make the real issuance that followed a "no action taken" no-op). A real issuance whose names already
  hold a test certificate (a lineage renewed from a staging authority, or one a staging authority
  signed) adds `--force-renewal`, unless the configured directory is itself a staging one; with
  `JD_ACME_DIRECTORY` both name `--server`, except Let's Encrypt's production directory, which is
  certbot's default and is left unnamed (certbot swaps a `--dry-run` to staging only when the server
  is its default spelled exactly); a deployment run's own orders, over HTTP-01 and DNS-01, name it the
  same way and check for an account under it (`deployments/implementation.md`). The page offers that
  issuance on a test lineage in place of Renew, since certbot renews from the authority the lineage's
  configuration names. Such a job is titled "Replacing the test certificate for …", audited with
  `replacesTestCertificate`, reloads nginx for the enabled sites naming the certificate (see
  reloading after renewal, below) and ends by saying what each answered. `ServedCertificates` (`cert_served.go`,
  `GET /certificates/served?path=`, system.admin) asks each such site over a TLS handshake on its
  first `listen … ssl` — a wildcard address on loopback, a named one only when it is this host's, a
  PROXY header first behind `proxy_protocol`, with a server name the certificate covers — and
  compares the leaf with the file's. Certificate details (`cert_detail.go`): `GET
  /certificates/detail?path=` (read) answers 403 for a path `listCertificates` does not list, and
  returns the file's chain in file order (subject/issuer DNs, SANs, serial, SHA-256/SHA-1, key type,
  AIA/CRL, embedded SCT count) with a verdict judged offline against the system roots (`complete`,
  `wrong-order`, `missing-intermediate` when the top issuer publishes an AIA URL, else `private-ca`,
  `self-signed`, `invalid`), plus the key's path (the site's `ssl_certificate_key`, certbot's
  `privkey`, or a sibling `privkey.pem`), mode, owner and whether it matches — never the key.
  `GET /certificates/history?name=` (system.admin: it reads the audit trail) merges certbot's
  `archive/<name>/certN.pem` versions (only for a name with a renewal conf), `certificates.*` audit
  entries whose target names it, and renewal failures from the renewal record. `POST
  /certificates/decode` (read, 256 KiB) decodes a pasted PEM chain or CSR in memory and refuses to
  read a private-key block. Downloads and export (`cert_export.go`): `GET
  /certificates/download?path=&part=` (read) answers `fullchain`, `cert` (the leaf) or `chain` (the
  rest) of a listed file as PEM, 403 for an unlisted path; `POST /certificates/export` (system.admin,
  ordinary export confirmation, `{path, format: key|pfx, password, legacy}`) returns the matching key as PEM
  or a PFX (OpenSSL 3's AES-256/PBKDF2 default, or 3DES/SHA-1 with `legacy`; password at least 8
  characters) — see invariants.md for the confirmation policy and how the password stays out of argv. Certificate coverage (`cert_coverage.go`) maps served names to their certificates through
  `GET /certificates/coverage`. The hygiene findings endpoint and its recommendation rules are removed
  in 0.7.5. Existing certificate issuance, renewal and lineage controls retain their own review and
  permission boundaries.
- **Reloading after renewal** (`renewal_hooks.go`). nginx serves the certificate it read at its last
  reload, and certbot reloads it after a renewal only for a lineage its nginx plugin installed. `PUT
  /certificates/renewal-hook` (system.admin, audited as `certificates.renewal-hook.install`) writes one
  managed deploy hook, `/etc/letsencrypt/renewal-hooks/deploy/50-just-dashboard-reload-nginx`, which
  reloads nginx only after `nginx -t` passes; `DELETE` (inside `s.destructive`, no phrase) removes it.
  A file at that name without the `# Managed by Just Dashboard` marker is `foreign` and is left alone
  (409 `hook_foreign`). `CertbotState.reloadHook` carries the state (`installed`, `missing`,
  `modified`, `foreign`) and the other deploy and post hooks certbot runs. The dashboard's own
  issuance and renewal jobs reload nginx themselves when an enabled site serves a lineage they
  changed (`reloadAfterRenewal`, `SitesServingLineages`),
  since certbot runs no deploy hook for a new lineage. `GET /certificates/renewal/log` reads the
  renewal schedule's record (the timer's service journal, or certbot's log where cron renews), and
  `POST /certificates/renewal/run` (system.admin, audited as `certificates.renewal.run`) starts that
  service as a `certbot.renewal` job, 409 `renewal_not_systemd` where cron renews. `GET
  /certificates/certbot` is `CertbotState` (503 `certbot_unavailable` without certbot); `POST
  /certificates/certbot/install` (system.admin, `certificates.certbot.install`) installs certbot, its
  nginx plugin or one DNS plugin through the host's package manager; `GET /certificates/dns-providers` lists the DNS
  plugins with whether each is installed and has saved credentials; and `POST /certificates/revoke`
  (inside `s.destructive`, `certificates.revoke`) runs `certbot revoke --delete-after-revoke` as a job.

- **The proxy routes are mounted per area**, each from its own file beside its handlers, and composed in
  `mountProxyRoutes` (`api/handlers_proxy.go`): `mountEngineRoutes` (status, the raw config editor,
  validate, test, reload; `handlers_proxy_engine.go`), `mountVHostRoutes` (the listing, what nginx has not
  loaded, the enable switch and the removal of a stray link; `handlers_proxy_vhosts.go`, which also mounts
  `mountAccessListRoutes` at `/proxy/access-lists`) and `mountProxyInsightRoutes` (`handlers_proxy_insights.go`, which
  mounts the live metrics from `handlers_proxy_metrics.go`) inside `/proxy`; `mountSiteBuilderRoutes` (`handlers_proxy_sites.go`) and `mountSiteOpsRoutes`
  (`handlers_proxy_siteops.go`) inside `/proxy/sites`; `mountStreamRoutes` (`handlers_proxy_streams.go`),
  `mountAuthFileRoutes` (`handlers_proxy_auth.go`) and `mountProxyToolRoutes` (`handlers_tls.go`, where
  the whole subtree is `system.admin` because every tool probes a caller-chosen destination) at
  `/proxy/streams`, `/proxy/auth-files` and `/proxy/tools`; `mountCertificateRoutes`
  (`handlers_certificates.go`) and `mountTLSRoutes` (`handlers_tls.go`; the watch list's handlers stay in
  `handlers_domains.go`) inside `/certificates`; and `mountPortRoutes` (`handlers_ports.go`) at `/ports`,
  which chi serves with and without the trailing slash, with `/ports/meta`, `/ports/firewall` and
  `/ports/history` (`handlers_ports_history.go`) beside it.
  `TestProxyRoutesKeepTheirPaths` pins every path, method and gate as they stood before the split. The whole group runs `withProxyActor`, which puts the
  signed-in account on the context for the change record below. Background work and state the proxy
  pages keep beyond `proxysvc.Service` go in `api/modules_proxy.go` (`initProxyExtras`, run last in
  `initModules`; `startProxyExtras` from `Start`; `stopProxyExtras` from `Shutdown`, which also runs for a
  server never started), and their tables in `store/schema_proxy.go`'s `proxySchema`, which `Open` applies
  after `applyAddedColumns` and `TestProxySchemaIsAdditive` holds to `CREATE … IF NOT EXISTS`. A table
  created there that later gains a column through `addedColumns` has to move into `schema` in the same
  change, since `applyAddedColumns` runs first on a fresh install.
- **What the engine's own test says, not only whether it passed.** `runTest` (`testrun.go`) fills
  `ValidationResult.Diagnostics` (`diagnostics.go`: level, message, and file and line where nginx names
  them) and `Warnings`, the warn-level count — nginx exits 0 through a conflicting server name it is
  "ignoring", and that site then never serves. nginx writes a test's messages as `nginx: [warn] … in
  /path:12` when it can open its startup error log (root on the host) and as the timestamped error-log
  line when it cannot; both are read, the path whole from the last ` in /` outside the quotes nginx
  puts round what it repeats, so a file named with a space keeps its place and a message naming two
  places (`used in /a.conf:1 and in /a.conf:2`) opens the second, where nginx stopped.
  `ParseCaddyDiagnostics` reads `caddy validate`'s JSON warnings and its `Error:` line, best effort,
  taking the position after `, at ` before any other path-like text so an upstream URL is never read
  as one. A diagnostic's file is named with its symlinks resolved, the form
  `allowedPath` gives the file being edited, so a Debian site's error names its sites-available file
  rather than the sites-enabled link nginx included. Caddy is validated against a copy in a private
  directory under `/tmp/just-dashboard`, the one temporary directory docker-compose mounts at the same
  path in the container and on the host, where caddy runs through nsenter — a copy in the container's
  own /tmp was invisible to it, so every Caddyfile check failed. That root must be a real directory the
  dashboard's user owns and nobody else can write to, or nothing is written or run. `validateCaddy`
  replaces the copy's name with the file's throughout the result; `Validate` holds a Caddy path to
  the proxy's directories as it does an nginx one.
- **A test that gives no verdict is not a refusal.** `runTest` tells the engine's answer (exit 0, or
  the 1 both engines refuse with) from a run that never gave one: out of its 30 seconds, stopped with
  its caller's context, ended by a signal, not started, or exit 126/127 from nsenter or `docker exec`
  failing to reach the binary; for the ingress, also docker's own "Error response from daemon".
  Those are an `*UnfinishedTestError` (`ErrTestUnfinished`, unwrapping to the context's or exec's
  error), which `mapProxyError` answers as `test_unfinished` — 504 when time ran out, 502 otherwise —
  retryable, with the output as `raw` and no test beside it; a reload adds "so nothing was reloaded"
  and a start or restart "so nginx was not started". It is never kept, and a reload, start or save it
  guards does nothing (a staged candidate is put back). `Test` and `Reload` run their test under
  `context.WithoutCancel`, so a tab closed mid-test no longer cuts it off; `WaitDelay` stops the wait
  for a grandchild (nginx under nsenter, a shim's docker) that holds the output open after the kill.
  `runValidator` is `runTest` for the other callers (sites, streams, deployment routes), where a test
  that did not finish still reads as a failure and refuses what it guards.
- **The engine's last test outlives the toast.** Every test of the files on disk is kept per engine
  kind as a `TestRecord` (`lasttest.go`: kind, `checkedAt` — when the test began — and the result):
  `Test`, the test `Reload` runs first (passed or refused, for nginx, a host Caddy and the ingress),
  the one a start or restart runs under `WithTestedConfig`, and the second test `WriteConfig` runs
  with an nginx file in place when it passes (one it refuses is put back, so the files are those the
  last record describes). A candidate `Validate` stages and restores is not a test of the files on
  disk and is not kept. A test begun before the kept one never replaces it, and every reader gets its
  own copy. `GET /proxy/test/last?kind=` (admin, as the test itself — its output quotes the
  configuration) answers the record or 204 when none has run since the dashboard started; it is held
  in memory, so a restart forgets it rather than vouching for files it did not see. Test config
  opens this retained result without running the test again. The automatic overview finding was
  removed in 0.7.5.
- **A conflicting server name is placed at the sites that claim it.** nginx names no file for its
  commonest warning (`conflicting server name "a.test" on 0.0.0.0:80, ignored`).
  `PlaceNameConflicts` (`nameclaims.go`) reads the effective configuration and gives such a warning
  `claims`: every http server block whose `server_name` names it on that address, in nginx's reading
  order, at its `server_name` line with symlinks resolved — the first serves the name, and each after
  it is `ignored`. A `server_name` in a file the block includes (a snippet several sites share, whose
  one line would name every site) places the claim at the block's own `server` line instead, with the
  snippet's line as `nameFile`/`nameLine`; claims that still land on one file and line (one file
  enabled under two names, or included twice) cannot say which site is served, and are not given. A listen's address is read as nginx prints it (`80`, `*:80` and `0.0.0.0:80` are
  one; no listen is `*:80`, or `*:8000` for an unprivileged nginx). nginx warns once for each block
  after the first, so claims are given only when there are exactly one more than the warnings about
  that name and address, and not at all when a claimant listens on a host name: a block the tree
  cannot see would put the wrong site first. `POST /proxy/test`, `GET /proxy/test/last`,
  `POST /proxy/reload` (passed, which its toast's Show opens, or refused) and a refused start or
  restart place against the configuration as it is now, within three seconds (the dump waits for the
  service lock, which the start's test has released by then); the record itself is kept as nginx
  wrote it.
- **The Configuration page reads the nginx directory as files.** `GET /proxy/files` (every
  signed-in account, as `GET /proxy/config`) is `ConfigFiles` (`config_files.go`): every file three
  folders deep under the nginx directory with its size, modification time and kind (`main`, `link`,
  `password`, `file`), editor and package-manager backups left out unless an include reaches them.
  Whether nginx reads a file is worked out from the disk by following nginx.conf's includes as nginx
  does (relative to the main file, glob(3) order, inside any block), not from `nginx -T`, which prints
  nothing for a configuration that fails its test; a file that does not parse stops the walk and
  `includesKnown` is false with the `problem` placed. A password file is listed as `protected` and
  never read; a file the dashboard wrote carries its marker and is `managed`. `GET /proxy/effective`
  (admin, gated with the test because it runs the host's nginx) answers `nginx -T` file by file with
  `checkedAt`, each link's `target`, and every directive placed with its enclosing blocks
  (`PlaceDirectives`) for a search by directive; a configuration nginx refuses is a 422 carrying the
  test (`DumpRefusedError`), and one held behind the service lock past 20 s is a 503 `busy`. A
  refused `PUT /proxy/config` now carries its test beside the error, as a refused reload's does.
- **Server-wide settings and the config linter** (`settings.go`, `directives.go`, `lint.go`,
  `api/handlers_proxy_settings.go`), all `system.admin` because they run `nginx -T`. `GET /proxy/settings`
  reads server_tokens, client_max_body_size, keepalive_timeout, gzip, ssl_protocols and
  server_names_hash_bucket_size from http and worker_connections from events in the effective tree: value,
  the file:line that sets it or nginx's default (ssl_protocols' default follows `nginx -v`: TLSv1.2 TLSv1.3
  from 1.23.4, older or unreadable versions TLSv1 TLSv1.1 TLSv1.2), how many blocks inside http set it
  again, a level, advice and a recommended value where there is one. worker_connections is judged against
  `worker_rlimit_nofile` or the running master's `Max open files` from `/proc/<pid>/limits` (pid file from
  the `pid` directive or `/run/nginx.pid`). `POST /proxy/settings/preview {changes}` and `PUT
  /proxy/settings {changes, reload}` (audited `proxy.settings.write`) take a closed list of names, each value
  checked by a strict per-directive pattern; `SetDirective` replaces only the words between the name and
  its `;` (tokenised as nginx does, a trailing comment kept, a comment between the words or a directive set
  twice in one block refused) in the file that sets it — found again in the file on disk by line, refused if
  it moved — or adds it to `conf.d/jd-http.conf` (created with the dashboard marker) when nginx.conf's http
  includes that path, otherwise to the main file's http or events block. Each file goes through
  `WriteConfig`; an edit to a file a `/var/lib/dpkg/info/nginx*.conffiles` lists is flagged `conffile`.
  `GET /proxy/lint` answers `Lint(tree)`: add_header dropping outer headers, a prefix location without `/`
  aliased to a directory with one, proxy_pass with a variable and no resolver in scope (IPs, unix sockets and
  upstream names excepted), `$http_host`, allow without `deny all`, return where deny/auth_basic/auth_request
  apply, stub_status neither guarded nor on a loopback-only server, `listen … http2`, SSLv2/3 or TLSv1/1.1,
  two servers with the same name on the same address, and `if` in a location holding anything but return or
  rewrite … last. Files `EffectiveConfig` leaves out (certbot's options, modules) are not linted.
- **The configuration nginx actually loads.** `EffectiveConfig` (`effective.go`) runs `nginx -T` through
  `hostexec` under the service lock — so it never dumps a candidate `Validate` has staged — splits it into
  `ConfigFile`s byte for byte (`ParseEffective`, which takes a `# configuration file` line as a file only
  when it names an absolute path), keeps only the files `ReadConfig` would show — no password file, and
  nothing that resolves outside the proxy's directories, so certbot's options file or a module's
  `load_module` file is left out with its directives — and caches the result for ten seconds; every
  change the service makes and every reload forgets it at once. A main configuration outside
  `JD_NGINX_DIR` is an error rather than a tree rooted at the wrong file. The service lock can be held
  for minutes by a certificate order, so a reader waits only as long as its own context, and readers
  that queue behind one pending dump share it. `NginxTree` (`nginxconf.go`) parses those files the way nginx tokenises them
  (quotes and their escapes, comments, `${var}`, nested blocks) into `Directive`s carrying their file,
  line and enclosing contexts, with each `include` replaced in place by the files its glob matched, sorted
  and without dotfiles as glob(3) would, `[!…]` included. nginx prints a file under the path it was
  included by, `./` and `../` left in, so includes and printed paths are compared cleaned; the files are
  indexed once by path and directory, which keeps thousands of sites linear.
- **Every change to a configuration file is offered to a `ChangeRecorder`** (`changes.go`) once it is
  committed — `WriteConfig`, `SaveSite` and deployment cutovers through `applySiteLocked`, `DeleteSite`,
  `SetVHostEnabled`/`ToggleVHost` (which now take the service lock like every other change, resolve the
  site file as the writes do, and record nothing for a toggle that leaves the links as they were or that
  nginx refused), `RemoveVHostLink` (recorded as a disable of the file behind the link, or of the link
  itself when it points at nothing), `ApplyStream`,
  `DeleteStream`, the live metrics' status server going in and out, and a deployment route's restore —
  with its prior content and the actor
  `WithActor` put on the context (empty for a deployment or a background loop). Password files are never
  recorded, a site file that resolves outside the proxy's directories is recorded without content, and
  the htpasswd writers do not call it; a recorder that fails is logged and the change stands.
- **Config history** (`api/proxy_revisions.go`) is that recorder: `proxy_config_revisions` keeps each
  file as a change left it (`existed = 0` for a removal), the newest 50 per path, nothing over 1 MiB.
  When the before-state differs from the newest revision kept, it is stored first as `outside` (changed
  outside the dashboard); a file with no history keeps its before-state as `baseline`. Toggles record
  the site file's content under `enable`/`disable`; the recorder cannot tell the site form from the raw
  editor, since both reach it as a `write`. Routes, all `system.admin` because a revision is a file's
  full content: `GET /proxy/history/files` (each file's newest revision, and `drift` against disk),
  `GET /proxy/history?path=`, `GET /proxy/history/{id}` (with the previous revision and the file now),
  and `POST /proxy/history/{id}/restore {reload}`, which goes through `WriteConfig` — tested in place,
  a 422 with the test on refusal, the file untouched — after `ReadConfig` refuses a password file or a
  path outside the roots; audited `proxy.history.restore`, recorded as `restore`. A removed site's file
  is restorable from the revision before its removal; its sites-enabled link is not recreated.
- **Served against disk certificates** (`drift.go`, `api/handlers_proxy_drift.go`). `GET /proxy/tls/drift`
  is readable by every signed-in account, like the sites list it is drawn from. For each TLS `server`
  block of an enabled nginx site (parsed with `ParseNginxFile`) it handshakes with the block's first
  `ssl` listen, sending its first exact `server_name` as SNI with `InsecureSkipVerify`, and compares the
  leaf's SHA-256 with every `ssl_certificate` the block names: `ok`; `stale` (another certificate that
  covers the name: renewed, not reloaded); `mismatch` (another site's file, named in `servedBy`, or one
  that does not cover the name); `unreachable`; `skipped` with the reason (no exact name, a certificate
  chosen by a variable, a unix or named listen, an unreadable file). Only this host's addresses are
  dialled — a wildcard listen becomes `127.0.0.1`/`::1`, a specific address must be loopback or one of
  the host's interface addresses — and never anything from the request. The result is cached five
  minutes and dropped on any configuration change or reload; `?refresh=1` runs it now and needs
  `system.admin`. The overview polls it every five minutes, shows stale and mismatch as findings, and
  gives an administrator's stale finding Reload nginx, after which it asks for a fresh check.
- **Live request metrics come from nginx's stub_status** (`stubstatus.go`, `api/handlers_proxy_metrics.go`).
  `GET /proxy/metrics` is open to every signed-in account — nginx's counters are no secret, and the
  sampler reads only the address in the dashboard's own file, so no caller can aim it — and
  `PUT /proxy/metrics {enabled}` is the switch (`system.admin`, audited as `proxy.metrics.enable` and
  `proxy.metrics.disable`). Switching off is not destructive: it stops no site and removes only the
  dashboard's own file. The switch's state is that file, `conf.d/jd-status.conf`, whose first line says
  `Just Dashboard owned` (the mark the site listing leaves `jd-*` files out by once site discovery
  checks it; until that change lands, a conf.d-only host lists the file as a site): one `server` on
  `127.0.0.1:<port>` with `location = /jd-status { stub_status; allow 127.0.0.1; deny all; }`,
  `access_log off`, `keepalive_timeout 0` and a 404 for every other path. The port is the first from
  19081 that `portalloc` can bind on 127.0.0.1 — the backend shares the host's network with nginx — and
  a file already in place keeps its own. Switching on writes the file, runs `nginx -t`, checks with
  `nginx -T` that nginx loads conf.d at all (a test passes over a file nginx never reads), reloads, and
  waits up to ten seconds for the first reading, because nginx binds after the reload command has
  returned and a port it cannot bind (taken since, or refused by SELinux) is only in its error log. A
  step that fails puts the file back, and after a reload reloads again without it, so the switch is on
  and answering or off: 422 `invalid_config` with the test beside it, 409 `metrics_unavailable` (no
  conf.d, conf.d not included, a file at the path without the marker, which both directions leave
  alone), 502 `reload_failed` or `no_answer`. The change runs to its end if the browser goes away.
  Switching off removes the file only while the configuration tests clean without it; a reload that then
  fails leaves it removed, and the 502 says, from whether the address still answers, either that nginx
  serves the status server until its next reload or that nothing answers there (nginx is not running);
  the client reads that as switched off with a warning. A dashboard-owned file whose listen names no
  `127.0.0.1` port the sampler can read (parameters after the port are fine) reports as on with that
  reason as its error rather than as on with nothing read. The
  `StatusSampler` (started from `startProxyExtras`) reads the file's address every five seconds into an
  in-memory hour — the file is the switch, so one removed by hand stops the series — trimmed on every
  poll, failed ones included, and counted only back to an hour before the report's `at`, so readings that
  fail for longer than an hour leave no hour behind — and
  `Report(epoch, after)` sends a client only the readings after its cursor, one a poll rather than 720; a
  new epoch (switched off and on, a moved port, a restarted dashboard) sends the whole series. Rates come
  from counter deltas: none for the first reading, after nginx restarted (its counters fall) or across a
  gap of more than four intervals, and never negative. The sampler's own connection and request are left
  out of every figure — keep-alive is off on both sides, so they are exactly one of each — and `dropped`
  is accepted minus handled, the connections nginx turned away at `worker_connections`.
  `TestLiveStatusServer` runs the whole switch against the host's nginx binary on a private prefix.
- **Upstream health** (`proxysvc/upstreams.go`, `api/handlers_proxy_insights.go`): `UpstreamTargets`
  reads every `proxy_pass`, `grpc_pass`, `fastcgi_pass`, `uwsgi_pass` and `scgi_pass` from the
  effective tree (http and stream), expanding a named upstream block server by server (`down` servers
  left out, unix sockets included); a target with a `$variable` is `dynamic` and not checked.
  `CheckUpstreams` connects to each distinct address once, eight at a time, 1.5 s each, then sends
  `HEAD /` to http/https ones (certificate unverified, as nginx does by default). A unix socket is
  connected to through `hostexec.HostPath` (`/proc/1/root` + path), since the host's `/run` is not
  mounted in the container. States: up, refused, timeout, unresolvable, missing, error, dynamic; a local
  TCP port that answers names its process from `ListListeners`. `GET /proxy/upstreams` is open to every
  signed-in account because destinations come only from the config on disk, and it serves the last check
  for 15 s with concurrent readers sharing one, so polling cannot amplify outbound traffic.
  `POST /proxy/upstreams/check` (system.admin, audited `proxy.upstreams.check`) skips the cache. 503
  `not_installed` on a host without nginx (the one answer of every nginx-only route, see `nginxOnly`
  in the request-lifecycle document), 503 `invalid_config` when `nginx -T` refuses.
  **Pools** (`upstream_pools.go`): the same report carries `pools`, the destinations grouped as nginx
  spreads them — one per upstream block a route forwards to (its method: round-robin, `least_conn`,
  `ip_hash`, `hash <key>`, `random`; `keepalive`; every `server` with weight, `max_fails`,
  `fail_timeout`, `backup` and `down`, the down ones listed but never dialled) and one per direct
  address — each with the routes and site files that use it. `balancing` says who spreads the requests:
  `native` for a block of several servers nginx chooses among, `native-dns` for one server named by a
  host name that resolves to several addresses from here now (nginx resolves it when it loads and
  balances across every address; what it resolved then may differ), and `single` for one address,
  whose spreading — a provider's load balancer, a floating address, a Kubernetes Service — nginx
  cannot see and whose failure is the route's; `provider` names the managed balancer a single
  endpoint's host-name suffix suggests (`.elb.amazonaws.com`, `.run.app`, `.svc.cluster.local` and a
  dozen more), as a hint and never proof. Each member carries the check's state and, from nginx's own
  error logs over the last hour (`requestErrorLogs`: every `error_log` the loaded tree names, main,
  http and server, confined to `/var/log/nginx` like the site logs, or the default there), what nginx
  met at it by kind — `refused`, `timeout`, `reset`, `closed`, `disabled` (nginx setting the server aside
  after `max_fails`, logged at warn, so absent where the log level is error), `other` — and when it
  last did; a member written as a name is matched under each address it resolves to, and nginx's
  `no live upstreams` (every server set aside) is counted on the block (`noLive`). `verdict` is what a
  visitor meets: `serving` (every primary up), `degraded` (some), `on-backup` (no primary, a backup),
  `down`, or `unknown` (nothing checkable). `evidence` names the logs read, the window's start and
  whether a log's 8 MB bound cut into it. A site's page draws each of its pools as a plain
  **Balancing** panel (`site-balancing.tsx`, `upstream-pools.ts`): who balances and how, the verdict,
  `no live upstreams`, and per server its role, check and what nginx logged. Tests:
  `TestUpstreamPoolsSayWhoBalances`, `TestUpstreamPoolVerdicts`, `TestUpstreamPoolsCountWhatNginxLogged`,
  `TestRequestErrorLogsStayInsideTheLogDirectory`, and `TestLivePoolOutcomesComeFromNginxItself`, where
  the host's nginx binary balances a private pool with a refusing member and the report reads the
  refusals from nginx's own log.
- **Site traffic and error log** (`proxysvc/site_logs.go`, `proxysvc/site_errors.go`,
  `api/handlers_proxy_traffic.go`): `SiteLogsFor` finds an nginx site by its listed name (never a
  joined path) and reads the `access_log`/`error_log` its file sets at file or server level;
  `confineLog` refuses a relative path, a `$variable`, and anything that is not inside `/var/log/nginx`
  both as written and after symlinks, and `syslog:`/`off` are reported as reasons. A second
  `accesslog.Store` (`proxyExtras.siteTraffic`) is keyed by that log path, its opener re-confining it.
  `parseCombined` also reads trailing `rt=` (latency) and `host=` when a `log_format` appends them; the
  stock `combined` the site form writes has no timing, so p95 shows only for such formats. All reads,
  open to every signed-in account like a deployment's request record: `GET /proxy/traffic` (each site's
  last hour with its minutes as `points` — every nginx site from the log its file names, then every
  route on the Docker Caddy ingress, `engine: "caddy-ingress"`, from the shared request record its
  deployment's Logs page reads, listed by `IngressRouteNames` in one `find` inside the container;
  each record refreshed at most every fifteen seconds, which costs only the bytes appended since), `GET /proxy/traffic/{name}?window=15m|1h|6h|24h|7d`
  plus the deployment filter params (a `RequestWindow` with `logs`), `GET /proxy/traffic/{name}/tail?after=`
  (polled tail from the window's cursor), `GET /proxy/traffic/{name}/export` (CSV), and
  `GET /proxy/errors?site=&window=`: the last 8 MB of the site's error log, or of nginx's (the http
  block's `error_log`, then main's, then `/var/log/nginx/error.log`) narrowed to the site's
  `server_name`s by each line's `server:`/`host:`, grouped by level, message with quoted values and
  numbers replaced, and upstream; recognised lines carry a plain title and advice (refused upstream,
  timeouts, too-large bodies with the size to raise `client_max_body_size` to, missing files, TLS
  handshakes and so on).
- **Proxy alerts** (`api/proxy_alerts.go`, `api/handlers_proxy_alerts.go`; tables `proxy_alert_rules`,
  `proxy_alert_state`, `proxy_alert_events` in `store/schema_proxy.go`): rules of kind `cert_expiring`
  (`params.days` from 21/7/3/1), `cert_expired`, `renewal_failed` (the last certbot run or its reload
  hook failed, with an unreadable or running renewal left unjudged), `served_drift` (a TLS site serves a
  stale or mismatched certificate; unreachable and skipped sites are left unjudged), `engine_down`
  (the engine's systemd unit not active/reloading/activating), `upstream_down` (`params.minutes`, 5–1440;
  the `/proxy/upstreams` check,
  so only addresses nginx's own config names are dialled), `watch_unreachable` / `watch_untrusted`
  (`CheckEndpointAt` on each current `watched_endpoints` row, using its pinned IP when set, with the IP
  in the alert subject so two origins of one name stay distinct), `watch_grade_below` (`params.grade`
  defaults to A; A+, A, B or C allowed; a full `ScanTLSWith` of each current watched endpoint runs
  only while the rule is enabled, up to four at once with a 60 s per-endpoint budget, following pinned
  addresses and STARTTLS ports; watch lists over 12 endpoints rotate in two-pass batches so the hold
  can mature while each pass stays bounded; an unreachable or unfinished scan is unjudged, and a grade
  below the chosen minimum must persist through two checks and a minute) and `site_errors`
  (`threshold` %, `minutes`, `minRequests` over the site's access log through
  `proxyExtras.siteTraffic`). A pass runs every 5 min (`proxyAlerts.Start`
  from `startProxyExtras`; clock, observer and deliverer are fields for tests). State is one row per
  (rule, subject, level); a transition is told once — firing when the subject first holds (engine and
  unreachable: two passes and a minute; upstream: two passes and the rule's minutes), recovered once the
  reading no longer finds it — one message per subject at its most severe level; a source that cannot be
  read, or a subject it cannot judge, keeps its state. Messages go through `DeliverNotification` as
  `proxy.alert.firing` / `proxy.alert.recovered` with `NotificationEnvelope.Proxy`, rendered by
  `renderProxyAlert`; a rule with no channels tells every enabled one, a paused channel gets nothing, a
  muted subject is followed but not told, and every transition is kept in the newest 500 events. Routes,
  all `system.admin` and audited (`proxy.alerts.*`): `GET /proxy/alerts` (rules, firing/pending/muted
  subjects, last 100 events), `POST /proxy/alerts/rules`, `POST /proxy/alerts/test {channelId}` (a
  `[test]` message to one existing channel), `POST /proxy/alerts/evaluate` (a pass now; holds still
  apply); behind `destructive`: `PUT /proxy/alerts/rules/{id}` (can pause; new terms or a pause clear the
  rule's state except mutes), `DELETE /proxy/alerts/rules/{id}`, `PUT /proxy/alerts/mutes {ruleId, subject,
  muted}`. The ordinary watched-check schedule still records a handshake and certificate; only an
  enabled grade rule runs the full grader. The operator chooses the minimum expected grade. The
  rules are read and edited on their own page, `/proxy/alerts` (`components/proxy/alerts-page.tsx`,
  with the parts that do not know its kinds in `components/alerts/alert-parts.tsx`, which the host's
  Alerts page shares), and the overview's identity bar links there with the firing count from the same
  `GET /proxy/alerts`.
  `TestProxyAlertsRequireAdminAndTellEachTransitionOnce` covers the API gate, audit, firing, unreadable
  source, and recovery; `TestProxyAlertReadingsForRenewalAndServedCertificates` covers the two additional
  readings; `TestProxyAlertsCheckCurrentPinnedWatchEndpoints` and
  `TestWatchedGradeRuleScansPinnedTargetsAndJudgesOnlyCompletedGrades` cover current watch rows,
  pinned origins, STARTTLS and grade decisions; `TestWatchedGradeRuleRotatesLargeWatchListsAfterTwoPasses`
  covers the bounded schedule.
The proxy overview retains native source read errors and retry controls, including a status read that
fails after answering. It no longer folds observations into an attention list or suggests domain/configuration
changes. The finding snooze routes are removed. Existing `proxy_finding_snoozes` schema/migration entries
remain so upgrades preserve the database. Certificate hygiene findings and `/certificates/findings`
are removed; certificate inventory, issuance, renewal, coverage and requested diagnostic tools remain.
nginx Settings returns current values and constraints without levels, advice or recommended values.
