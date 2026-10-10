# Feature panels and terminal workspace

## Feature panels

**Docker** (`components/docker/`) is where the product's opinion about newcomers lives.

- `explain.tsx` is the teaching layer — `Hint`, `Term` (a dotted underline, definition one hover away),
  `Field`, `GLOSSARY` — written for somebody who has never run a container and phrased around the decision
  rather than the mechanism. The rule it enforces is that **explanation is quiet**: a form that shouts
  every caveat is as unusable as one that explains nothing.
- `create-container.tsx`'s three entry points matter more than the form, and they are the *first* thing
  on the panel rather than three stacked sections: three cards on one line, and only the route you picked
  spends any height. **Paste a command** covers the common case: `lib/docker-run.ts` parses leniently and
  returns three things — the spec, warnings about what it could not read, and `unsupported`, the flags it
  *understood* and the form has nowhere to put. The last is kept separate on purpose. `--gpus all`
  dropped without a word produces a container that starts and then has no GPU, and the operator finds out
  from the application failing; the panel says so and keeps the original command beside the form.
  **Start from a template** is `lib/docker-templates.ts` — images almost every server runs across
  five categories, every one bound to 127.0.0.1; a set of starting points, not an app store, which is
  what goes stale and becomes the maintenance burden in Yacht and CasaOS. A template's setup
  requirement is one word on its card and a full notice at the top of the form the moment it is picked,
  which is the only place the instruction can actually be followed. **Custom** is for people who
  know what they want. The Command tab shows the server-rendered `docker run` and compose, live. Port
  bindings are offered as *who should reach this* rather than as an address, populated from the machine's
  real interfaces so the LAN option names one rather than gesturing at the idea.

  The form's explanations are `?` hover cards on the section headings, the field labels and the
  behaviour switches, not paragraphs printed beside them. Six sections of three lines each is eighteen
  lines of prose around twelve controls: an expert scrolls past all of it every visit and a newcomer
  reads it once. Prose survives in exactly two places — a warning that depends on what you just typed
  (a missing image tag, a port on every interface, an unnamed volume) and the `privileged` switch, which
  can hand the server away and should not need a hover to say so.
- `run-console.tsx` deliberately **does not** let `useSocket` reconnect: reconnecting re-issues the GET,
  and re-issuing the GET runs the command again — a redeploy fired twice because a VPN blinked is not a
  re-render. A dropped socket ends the run and says so.
- `attention.tsx` renders the half of the diagnosis that is not runtime, through the product's one
  `FindingList` (`components/finding-list.tsx`) rather than through a parallel component of its own.
  Docker had grown a second one — a two-line title-and-detail row with a glyph, a tag and a chevron —
  while Metrics and Security shared a one-line accordion for the identical idea, so a page showing both
  read as two products. What is left here is the Docker-specific part: repeats of one kind collapse to a
  single row ("9 containers have no memory limit") whose body states the shared reasoning once and names
  the containers as chips that run that container's own remedy. The severity filter strip, the "N
  distinct" counter and the Hide button are gone — four chips and two counters framing a list capped at
  five rows. `ContainerFindings` filters a diagnosis pass for one container; the containers page passes its own,
  and the container's page asks for one.
  The other half — what is *fine* as well as what is not — was `RuntimeHealthPanel`, a bar of the
  health-check counts over the containers list, because a list of problems can never say "eight
  healthy, four with no health check at all" and that last number is what stops "all healthy" meaning
  "nothing is being watched". The 0.7.1 containers overhaul moved those counts to where they are read:
  the page's identity line says how many running containers a passing health check vouches for, and
  every running row says its own verdict, "no health check" included.
- The containers page (`app/(dashboard)/docker/containers/page.tsx`) is three parts under the engine's
  identity line. `container-band.tsx` draws the five containers using the most processor and memory
  as spans of one bar the size of the host (the Processes band's `ShareBar`), figures gliding to each
  two-second frame, and **Recent**: Docker's event log over `/docker/events/stream?kinds=container`,
  a restart loop folded to one line and an OOM kill said on the exit it caused (`foldRestarts`).
  `container-table.tsx` is the table — sortable headings, a ticking uptime under each state, CPU and
  memory as figures beside short bars (memory against its limit, amber past 85%), the processor's
  hour and the network rate from `2xl`, the compose project in its `hueFor(…, LANES)` hue as a filter
  — drawn down the row below `xl`. `containers.ts` (unit-tested) holds what the rows say: the five
  buckets the state chips count (failing, starting, running, paused, stopped — failing being a restart
  loop, a dead container, a failed health check, a crash exit or an exit the event log says the OOM
  killer caused, since `docker stop` also leaves 137), the exit words, the sort, and the network rate
  from two frames of Docker's cumulative counters.
- `tabs.tsx` gives selection, hover and focus three different mechanisms. They had two: the accent
  outline meant both "this filter is on" and "the keyboard is here", so a keyboard user could not tell
  which filters were applied and tabbing looked like the selection moving.
- `exposure.tsx` draws a published port so the two cases that matter look different, and `RouteRow`
  shows the full trace — binding, reverse proxy, firewall — with the reasoning behind each verdict and
  an explicit *inferred* where one was worked out rather than read.
- `container-cells.tsx` holds the three cells that were saying something other than what they meant:
  memory showing host RAM as a limit nobody set, CPU with no denominator stated (Docker counts one core
  as 100%), and a Status column carrying the worst security finding underneath the runtime state.
- `container-band.tsx`, `container-table.tsx` and `overview.ts` are the overview's. The band and the
  table read the containers socket, so the overview's figures move with every frame where the four
  tiles it replaced were a poll. `overview.ts` holds the words: a container is *failing* only when
  its check fails, its restart policy is cycling, or it exited with anything but 0 or 143 — a host
  of one-shot jobs is otherwise a page of red — and Recent reads Docker's events one line per
  container, a stop's kill, die and stop as one stop and an OOM kill folded into the exit it caused.
  The table's figures are plain text: a counting figure starts only once scrolled into view, and a
  row below the fold read 0.0% until it was.
- `cleanup.tsx` and `stack-preview.tsx` are the two "before you press it" panels: what each category of
  removable object costs, and what a compose deploy is expected to change — including the sentence about
  volumes, stated whether or not any are affected. The preview opens on that decision as one sentence
  (*Deploy will recreate 2 services and create 1*) over a bar of the stack's services, each in the
  colour of what happens to it, and lists them as a framed table, most consequential first: the change,
  why (the keys that changed as tags over the server's reason), the image and the tag it moves to, and
  what the service is doing now — a running service about to be recreated says it goes down while it
  is replaced. The unchanged rest stays in the table, quiet, rather than five copies of "its
  configuration is identical" each with a heading. The file's diff is grouped by the service each hunk
  changes (`stack-diff.tsx`, the same view the compose editor's review and the history use), beside
  the volumes, each kept or destroyed, and the "compose makes the final call" caveat is stated once.
  Deploy and a fresh read sit at the top, where the reader who has just read what will happen can act
  on it. A cleanup category is one line — name, size, count, in
  fixed columns — with the cost sentence and the example names behind its `?`. It used to be a block
  whose height depended on how far the cost sentence wrapped and whether that category had examples, so
  the six rows came out at three different heights and the size and count, top-aligned against the
  tallest, never lined up with each other; four of the six were "Nothing in this category" at half
  opacity and were the tallest thing on screen.

Panel headers carry no icon plot. A tinted square in front of every title is chrome repeated once per
panel, and on a page that is eight stacked panels it reads as a column of brand marks rather than as
eight headings. It started here and now holds product-wide — the prop is gone from `PanelHeader`,
`Modal`, `SidePanel` and `Section`, and the title sits at `text-title` instead
(`docs/internal/frontend/design-system.md` §14). The `?` stays: it is the one mark on those headers
that does something.
- The Images page is `app/(dashboard)/docker/images/page.tsx` over `image-band.tsx`, `image-table.tsx`,
  `image-sheet.tsx` and `image-pull.tsx`, with the pure parts — a reference split as a registry reads
  it, one registry answer per image, the table's filters and order, a pull's stream folded by layer
  and a history line read as its instruction — in `images.ts` (unit-tested). Its disk lines reclaim
  their own kind: unused images through `POST /docker/images/prune?all=true`, the build cache through
  `POST /docker/build-cache/prune` and stopped containers through `POST /docker/containers/prune`;
  only Reclaim beside the total runs the sweep, and says it reaches stopped containers and networks.
  The pull socket is enabled only while a pull is in flight: the server closes it when the pull
  ends, and `useSocket` reconnects whatever is still enabled, so a finished pull used to open a new
  one about once a second until the page was left. Tag names the same image again, so the copy a
  container runs can keep a name across the next pull of its tag.
- `stacks-tab.tsx` is the Stacks page, read the way Services is (`design-system.md` §15 pass 2): the
  server's identity line (the Compose mark, Docker's version, stacks deployed, services running, ports
  published, and the attention verdict that presses the Needs attention chip), then `stack-band.tsx`
  (the five stacks using the most processor and memory as spans of one bar the size of the machine,
  summed over each stack's containers from the containers socket, a press narrowing the table to that
  stack; and Recent, the daemon's last starts, exits with their codes, out-of-memory kills folded into
  the exit they caused and failed checks, each opening its stack), then `stack-table.tsx`: the stacks as
  one framed table of their containers. A stack is a row with its state, how many declared services are
  up, its summed CPU and memory and the one action that belongs there — deploy when the application is
  down — and a lane in its name's hue down its containers; each container is a row of the containers
  page's readings (state with uptime, health or exit code, CPU with its last hour, memory against its
  limit, published ports) and its verbs, opening the container. Stacks fold their containers away for
  the session. Rows are worst first; below 1280px the same rows are drawn down rather than across.
  The joins, buckets and change words are pure (`stack-readings.ts`, unit-tested). A stack's own page follows the same rule as a container's: the compose verbs that are pressed
  daily (deploy, restart) sit inline, and the rest are behind one overflow menu, one word to a line,
  drawn the same way as the container menu. Its services and the deploy preview's services are framed
  tables (below), its deployment history a rail beside the record it reads; the network panel's shape
  diagram is the one `Group tinted` fence the section keeps, and a diff sits in a `Well`.
- `stack-detail.tsx` is a stack as the application it is. It opens on the stack's identity line — its
  services' products, the compose file, the directory and its checkout (uncommitted changes, how far
  behind, a link to the Git page), how many services run and how many ports it publishes — with the
  verdict at its end (*1 service failing*, *1 not created*, *All 5 running*), which narrows the table
  to those services. Its Services view is a framed table of every service (`stack-services-table.tsx`)
  fed by the containers socket: the service as its image's product with its log lane down the row,
  its state read with the event log (out of memory, crashed or stopped by the exit status, a restart
  loop counted) and how long it has been in it, its processor's last hour beside this second's share,
  memory against its limit, network rates from 1536 and its published ports, under state chips that
  count and narrow; each row opens its container and carries the compose verbs for that one service
  (`docker compose restart|stop|start|up --force-recreate|pull <service>`, each confirmed with its
  command). Under it are the Services page's band for the stack (`stack-usage-band.tsx`: its share of
  the processor and memory, and Recent from `/docker/events?stack=`) and a picture of its published
  ports, services and networks whose wires pulse with each service's traffic (`stack-map.tsx`); the
  joins and words are `stack-service-readings.ts`, unit-tested. Every other view names a service the
  same way — its lane, its product, its name (`ServiceLabel`):
  - the deploy preview (`stack-preview.tsx`, above);
  - the compose file (`stack-compose.tsx`), editable in place with an outline beside it — each section
    and the names under it a press from their line, each service with its live state — a state that
    says whether the screen is the disk and whether compose took it, a status line that says where the
    cursor is in the stack's terms (*services · api*), Ctrl+S, and a review of an unsaved edit as a diff
    against the file on disk. An edit survives a look at another view (kept in memory, never written
    down: a compose file holds passwords). Validation jumps to the line compose names, and saving says
    it is *not* deploying and offers the preview and Deploy;
  - the stack's directory (`stack-files.tsx`), read first as what compose takes from it: the compose
    file, the root `.env`, each build context, bind mount and env file the services name, with the
    services that read each in their lanes, git's word for any change, and a path that is missing said
    with what it costs (a build or env file compose cannot start without in red, a mount Docker will
    create empty in amber). The same words follow the names into the browser under it, which takes the
    tab's height;
  - the deployment history (`stack-history.tsx`): a strip from the first record to now, each stretch
    in the colour of the compose file that was live through it, over the records as a rail (the backup
    job's runs' shape) and the one picked read whole — who did what from which commit, whether its
    images are still here, how its file differs from the one on disk now, the digests running just
    before, and *Edit from this version*, which puts that file in the editor unsaved;
  - its logs as one source (`stack-logs.tsx`, `stack:<name>`: every container merged by time, each line
    under its service in that service's hue, the stack's readings over it, and an Events view of what
    Docker did to the project), under a strip of the services writing into it — each with its state,
    its lines and errors in the last hour and their shape — where a press narrows the log to one.

  The outline, the line diff, what each service reads from the directory and the history's spans are
  `stack-views.ts`, unit-tested. The stack's menu opens a shell in its directory. `container-detail.tsx` adds
  the reachability join (published port + the proxy site pointing at it turns "running on 3000" into a
  URL), the writable-layer investigator, the failure diagnosis (whose window the Logs tab opens as a
  Crash window chip, and the Overview's notice as Read those lines), its logs through the lens its
  image names with an Events view beside them (`container-events.tsx`: the health check's probes,
  Docker's events for the container with crash loops folded and an exit's last lines under it),
  editable limits, raw inspect, every verb `container-actions.tsx` declares (the lifecycle as words
  beside the way back; Pause, Update, Copy id and Remove behind its menu) and Rename — behind a
  statement of consequence when compose owns the container, because the next deploy silently undoes
  it days later. The page opens on the container's identity line (`container-identity.tsx`: its
  product with its state in the tile's corner, image, project, ticking uptime, restart policy in
  words, the id, and the verdict from `container.ts`, which reads a crash loop and a failing health
  check as failing where Docker says "restarting" and "running"), and its Overview on the failure
  notice, four live readings off its stats socket with their last hour (`container-readings.tsx`),
  its findings, a picture of how it is reached and what it keeps (`container-picture.tsx`), the
  containers of its compose project — or of its own networks — as a live table
  (`container-company.tsx`), its recent events with loops folded (`container-recent.tsx`), how it
  runs, and its ports and networks as tables (`container-tables.tsx`, which also draws the
  Environment tab: each name's namespace in its lane hue, each value in its kind's code hue, and
  the kinds — credentials, addresses, paths, switches, numbers — as chips that count and narrow, beside
  a filter that never searches a hidden value). Each other view is the overhauled page it stands in
  for. **Usage** (`container-usage-tab.tsx`) is a project Runtime's `deploy/runtime-usage.tsx` — every
  chart headed by its reading now, a Live range off the stats socket beside the recorded ones, the
  processes chart and what it has done since it started — with the container's own exits, OOM kills
  and folded restart loops as the charts' markers (`usage-markers.ts`), then the Metrics page's
  breakdowns read off the same frame: the memory allocation as one bar of programs, active files,
  reclaimable cache and headroom, the quota, host share, throttling and task count as tiles, the
  memory and CPU limits drawn beside the change that sets them, and the interfaces as a framed table
  with an in/out bar per row. `RuntimeUsage` takes `running={false}` for a stopped container: no
  socket, the recorded hour, and "Not running" rather than a socket waiting forever. The old Pause
  button went with the old tiles; a recorded range holds the charts still. **Storage**
  (`container-storage-tab.tsx`) is the file manager's workbench: one frame around a rail of the mounts
  — each the path *inside* the container, drawn as its kind in that kind's hue (a database's volume as
  its engine, Docker's socket in red) with where it really lives under it, a tmpfs marked *not kept* —
  and beside it the picked mount open in `files/inline-browser.tsx`, flush, at the tab's height; the
  rail's foot counts how many mounts outlive the container, and the writable-layer investigator stays
  under the frame. With more than one to look in, the rows are the switch; a tmpfs row never is.
  Storage that looks like a database's own files is named as such above it while the container runs
  (`docker/shared.tsx`). **Logs** is the logs page's own `ServiceLogs`, read through the lens the image
  names, the lens's readings as tiles over the pane where it has them. **Inspect**
  (`container-inspect-tab.tsx`, logic in `inspect.ts`) is the run page's Details shape: one frame
  around a rail of the inspect document's sections and the picked one as coloured JSON in the `CODE`
  hues, folding, filterable, credentials still masked and never matched by the filter.
  **Configuration** (`configuration-remedy.tsx`) is Health's: each finding a lit card on its level's
  wash that prepares its change (or, compose-owned, opens the service with its YAML coloured), the
  settings those findings touch as rows in their tone, and the replacement specification as one framed
  editor whose head names the edited fields in `--git-modified`, a `BorderBeam` and "Replacing…" while
  it runs. **Shell** (`container-shell-tab.tsx`) is the deploy Console's: one pane, its strip saying
  `account@container` (root in amber) and the working directory, the run-as switch where the image
  declares a user, and read-only first commands (`id`, `ls -la`, `ps aux`, `df -h`, the OS release)
  that type themselves into the session — no `env`, which would print every credential the
  Environment tab hides. `build-dialog.tsx` is where the
  git panel and Docker stop being two products: a repository we already pull is a build context.

Four deep links are worth preserving: `/files?path=`, `/git?repo=`, `/terminal?cwd=`, `/audit?action=`.
Files and Git selections follow the address bar, while the terminal opens a session exactly once per
mount because a shell is a process. Audit's settled filters are history entries; Back restores the
previous question. The audit link is how the Docker event feed hands off a correlated event.

Packages use `?package=` for their inspector, and backup job pages use `?run=` for their selected run.
These, the Docker container inventory, Git and the Audit/Security lists participate in the shared
keyboard/place controller; [`workspace-interactions.md`](workspace-interactions.md) describes their
shortcuts, held arrivals and focus restoration.

A container and a stack are their own destinations — `/docker/containers/<id>` and
`/docker/stacks/<name>` — since 2026-09-21: each holds its logs, and the container a shell and
the stack a compose editor, which is the test for a page rather than a sheet (`shell-design.md`). The
container takes `?tab=` so a link can ask its question — "show me the logs" lands on the logs. The
`?container=` and `?stack=` addresses those pages replaced still resolve, by redirect. Deployment
runtime rows use these handoffs, including when a container disappears between the observation and
the click: the page reports it, and a container removed from its own page returns to the list.

`useQuerySelection` still keeps the remaining sheets' selections in browser history so reload and
back/forward restore them; closing clears only that selection parameter.

**Security and proxy** (`components/security/`, `components/proxy/`) follow the same rule — teaching next
to the control, not in a banner above it. `posture-panel.tsx` turns a finding's `fix` into a button and
maps it to the request plus the confirmation it deserves. `rule-form.tsx` is why the catalogue lives on
the server: picking "Redis" fills in 6379 *and* raises the warning at the moment of choosing, not in a
report afterwards; the `ufw` line it would run is in the footer. `ssh-panel.tsx` stages every change and
applies them together, and its dialog says to keep the session open and check a second terminal — the one
piece of advice no error message can give afterwards. `site-form.tsx` shows the server-rendered nginx live
beside the form (same renderer that writes the file, so the form is not a black box), with `DNSCheck`
under the domain field because "the name does not point here yet" causes most certificate failures and
certbot reports it as "challenge failed". `tls-report.tsx` says out loud what `unknown` means for a
protocol row.

The security section has four reading pages (§15–16): the overview, SSH, Intrusion and Logins.
The firewall, connections, interfaces and tools moved to the Network section in 0.7.1 and are
described with it below; the overview's Firewall and Connections tiles open them there. The overview opens on the exposure identity
line, then the posture's seven checks as a coloured strip (`posture-strip.tsx`; a dashed segment is
a check that could not run, and a segment narrows the findings to its area), then five area
readings, then the ways onto the machine as a wiring picture (`perimeter.tsx`: the internet through
the firewall, fail2ban and sshd, and this browser through the allowlist to the dashboard) in the
deployment section's `WireNode` and `AnimatedBeam` vocabulary. It describes the layers and their
states, not the reach of every port on the host. Under it the **Access boundary** lists the five
boundaries every change is judged against — Caddy-only ingress, the allowlist, the tailnet, SSH for
a tunnel, tailnet-only previews — each held, broken or unknown (`boundary-view.tsx`). The finding
severity counts sit in the Findings head.

- **SSH:** the page opens on sshd's doors as a picture (`ssh-picture.tsx`) — the port, public keys
  with the keyed accounts' faces, passwords and root — drawn from the draft, so a staged change
  shows its effect before it is applied. Authentication, access, session limits and other
  directives are `FormSection aside` groups, two to a row from `xl`, each marked *Edited* while it
  holds a staged change. Every control occupies the same column; recommendations sit with the
  setting, explanatory detail is available beside its label, and a sticky apply bar naming what
  changed applies the values together. Reverting a draft to its effective value removes it from the change set. The apply
  dialog still requires `change ssh`; the backend tests and reloads through its existing job. For an
  administrator the page ends on the Auth log (`auth.log` or `secure`, else the journal's sshd, sudo,
  su and logind lines); anyone else is told the page needs an administrator and no log is asked for.
  The **Jump host** section reads the five forwarding directives the closed list gained in 0.7.1
  (`AllowTcpForwarding`, `GatewayPorts`, `AllowAgentForwarding`, `PermitTunnel`, `MaxSessions`), stages
  a safe bastion profile into the same draft, and writes a ProxyJump snippet for the hosts behind this
  server (`bastion.tsx`). Where the host can run the independent recovery watchdog, the apply bar
  carries a "Restore unless confirmed (90 s)" switch, on by default: the apply is sent pending, and
  the global network confirmation notice, worded for SSH, keeps it only after this session verifies
  a fresh response. A staged change is judged against the access boundary as it is made
  (`useBoundaryCheck`); the dialog shows what it does to tunnels, and confirming it acknowledges it.
- **Intrusion:** jail destination rows carry the watched service, current state, counts and a meter
  of bans still held. The jail sheet retains manual ban and release actions. Its tuning form shows
  the subject, groups the three policy numbers, and offers the browser's address for the allowlist.
  Draft values use fail2ban's lowercase parameter names while the saved configuration uses camel
  case. Repeat offenders take their row alone, and Activity — fail2ban's own log, every strike as
  well as every ban — follows across the page; where fail2ban writes only to its journal, the
  offenders' fold says Activity reads it instead rather than "nothing has been banned". CrowdSec
  and Suricata follow, each opening on its install where it is missing (`network/install.tsx`):
  CrowdSec's decisions with release and a ban form that refuses the reader's own address, its alerts
  and bouncers (`crowdsec-panel.tsx`); Suricata's mode, rules and alerts by severity, read from
  `eve.json` through the log roots (`suricata-panel.tsx`). The page opens on **Blocked across
  engines** (`blocks-panel.tsx`): every refused address once, with each engine holding it and the
  broader blocks it already sits inside. Both ban forms say which engine already holds the typed
  address (`blocks.ts`) and what the ban does to the access boundary, and send the acknowledgement
  only after showing it. The jail sheet adds the jail's **Policy** — the rule as a sentence, what it
  reads, what each action does with a ban, whether the sshd ban covers sshd's actual port, and values
  a restart would change. CrowdSec's head, tile and notice say the server's enforcement verdict
  (`enforcement.ts`) rather than whether the engine runs, with each bouncer's pull age and the kernel
  sets under the bouncers. Suricata adds **Setup** (`suricata-setup.tsx`: capture interface with a
  tested move, whether the capture sees packets, rules with an update job, start) and the read-only
  **Inline queue**, which says whether each queue rule fails open.
- **Each area reads its own log in place.** SSH, Firewall and Intrusion read the file an operator
  would open first, each asked after with `GET /logs/source` rather than out of the whole log index,
  and fall back to the journal's reading of the same program, saying in the pane's facts whether the
  file is missing or outside `JD_LOG_ROOTS` (`host-logs.ts`, `log-section.tsx`). The day's counts
  from the log are a second run of the page's one grid, each a press that narrows the log under it.
  The client of a line takes the same address verbs as a peer anywhere else in the section, but the
  block only where the line is an attack a deny answers — a failed or invalid login, a strike or a
  ban, a rate-limited packet — never an accepted login, an `ignoreip` match or an allowed packet,
  which can be the operator's own; an outbound packet's address is this host's, and gets no verbs.
- **Logins** groups account/terminal, origin and session age, then places attackers and login
  history beside one another when there is room. Addresses use `PeerIdentity` or the inline
  `Address`; address block and lookup actions remain in `address-verbs.tsx`.

**The Network section** (`app/(dashboard)/network/`, `components/network/`) is eleven reading pages
over the network module; [`feature-map.md`](feature-map.md) lists each and
[`design-system.md`](design-system.md) §15 records how they are drawn. Its layout renders the same
`SecurityState` the Security section does, so the moved firewall keeps its posture findings and every
page knows the reader's address. The pages that moved keep their behaviour:

- **Firewall** (`/network/firewall`): the page opens on its inbound path as a picture (`firewall-picture.tsx`, reading the
  rules through `firewall-reading.ts`): each port the rules admit, drawn as the product that answers
  there with who may reach it, and the default for everything else. The rules are the working
  column, with default-policy and logging controls beside them on wide screens. Each row carries its
  action in a `--tag-*` hue down its edge and groups its destination service, port and comment;
  actions stay visible, and the table ends on the default as a row of its own. The rule dialog groups policy, destination and source, using source choice cards
  (Tailscale's own mark for a tailnet source) and service marks where the port identifies a product.
  Address-only deny/reject rules can be edited without inventing a destination port. Existing
  ordinary confirmations for toggle, reset and inbound-deny policy remain. The page ends on the Firewall
  log (`ufw.log`, else `kern.log`, else the kernel ring, read as the firewall lens); while ufw or
  firewalld says logging is off, the section says so and its button brings the logging control
  beside the rules into view instead of drawing an empty pane.
- **Connections** (`/network/connections`): opens on four live readings, each carrying every read
  since the page opened as its trend, and a picture of who is connected (`connections-map.tsx`: the
  callers by network, this server, the programs they reached), then the peers as before — address and
  network in one column, process and ports in another, a socket count and a comparative meter — with
  each peer's transports, far-end ports and socket states kept under its ports, and a footer saying
  what the read could not see (time since the last read, closes noticed, unconnected UDP). An
  administrator opens an address (`connections/peer-sheet.tsx`, `GET /connections/{address}`) into
  its live tuples with how long each has been seen, TCP bytes and RTT, the tuples seen closing in the
  last fifteen minutes, and the layers it crosses: the firewall rules naming it or its network (rules
  for any source are counted, not listed) and its block, the kernel route that answers it, and the
  socket history's last day, with the tools, investigator, captures and history as next steps. "A past
  hour" reads the socket history for a chosen UTC hour in the same shape (`connections/recorded.tsx`).
  "Block at the firewall" opens a dialog asking why, for how long (an hour to thirty days, or until
  lifted) and which incident — an existing saved run, or a new one that starts by recording who owns
  the address (`connections/block-dialog.tsx`); the blocks made here are listed under the table with
  their end, their incident and whether the rule is still in the firewall, and an administrator with
  the destructive capability lifts one after confirmation (`connections/blocks-panel.tsx`).
- **Tools** (`/network/tools`): 26 probes plus the browser-only IPv4/IPv6 subnet calculator occupy a two-pane
  workbench (the four counts that stood over it are gone; where probes are sent from is a fact in the
  result pane's head). The searchable chooser selects one labelled form and result area. Other probes
  stay mounted while hidden, preserving drafts, results and in-flight requests when switching. A new
  query-string arrival reseeds only the requested probe. Arriving with `?tool=asn&target=…` never runs
  it; sending traffic remains an explicit Run action.
- The tools now include route/path-MTU checks, host prerequisites, bounded packet summaries and
  local Wake-on-LAN. All server probes remain administrator-only; decoded packet fields may be
  sensitive. Network forms expose IPv6 GRE/bridge membership, preferred route source, rule outgoing
  interface/notes and DNS fallback/cache settings. The [complete networking audit](../../audits/2026-10-08-network-audit/README.md)
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
findings, the device sheet's guards, the create forms' bodies, the routing picture and a peer's QR
code; `JD_NETWORK_SHOTS` writes review screenshots at 390 and 1440.

**Packages.** `install-panel.tsx` updates as you type, which is not decoration: the reason people open a
terminal instead of a package page is that they do not know the name (`postgresql-client`, not `psql`;
`build-essential`, not `gcc`), and a form where you type a guess and press a button to find out you were
wrong is a form you use once. Install is one press on the row — there was a tray, and it cost a click and
a concept on every single-package install while protecting against an interruption a job survives anyway.
The command each install button runs is its `title`. The empty search offers eight software choices
with product marks; results are `ChoiceRow`s with their install action, so opening a result and starting
an install remain separate gestures. A pending query hides the previous query's results and says it is
searching; failed reads say so. A started install is a `Status`, not a disabled button.
The page is drawn per design-system §15 in the reading register, without summary tiles. Installed and
by-hand/dependency counts live in the identity, view strip and scope chips. `software-band.tsx` draws the
five largest software groups as shares of the installed size, in the disk measurement's colour, with
other packages as one muted span. Products are grouped by `packageProduct`; unnamed packages use the
archive's section. These sums cover the whole inventory, not the 400-row rendered table. Pressing a
group opens Installed, clears its search and selects Everything so dependencies appear too; Escape or
the selected chip clears the group. Beside it is a security-first update queue, version changes,
advisory coverage and confirmed security/all-upgrade actions. Missing sizes and advisory data are stated;
a failed update read is an error, never an empty all-clear.
`package-sheet.tsx` is one scroll: version changes and size, copyable commands coloured as shell words,
registered services with links to their systemd inspector, configuration and documentation links into
Files, the manual in a scrollable well, inspectable dependencies, and package metadata. Dependency
navigation uses `?package=` and browser Back. A usage read that fails reports its reason with Retry,
instead of leaving a loader. The sheet keeps package protection, install/update/remove/purge actions,
ordinary confirmations, and the page's resumable jobs. The installed table uses compact columns below 1280 pixels and fixed columns above,
a size bar per row and a 400-row cap with the count said plainly underneath. The fourth view, **Log**
(`components/packages/log-view.tsx`), reads what the package manager did from its own logs — apt's
history with each transaction's command and who asked, dpkg's record, the unattended runs, dnf's — in
History and Insights only, opening on everything on disk, since a package log is written a few times
a week and a live tail of it is an empty pane; it is named Log because the pane's own first tab is
History.

## The terminal panel

`components/terminal/` is the session rail and window strip; `components/xterm-pane.tsx` is the emulator. The
split matters — the pane is reused by the compose runner and knows nothing about sessions.

- The page is **one framed workbench**. The rail, the emulator and the Files/Git column are `Pane flush`
  inside a single `rounded-xl border` wrapper, separated by a hairline each (drawn on the rail's right
  edge and the tools column's left edge). Three framed panes with a gutter between them read as three
  boxes floating on the page; the screen is one working surface. The three columns' top strips are all
  40px (the rail's and the tools column's `PaneHeader` are pinned to `h-10`, the emulator's strip is
  `min-h-10`), so their hairlines meet as one rule across the frame. Immersive mode drops the wrapper's
  frame along with the page. Below `lg` the rail and the tools column **cover the emulator** inside the
  frame instead of sitting beside it — stacked over and under it they left a phone's terminal one line
  tall — so only one of the two is up at a time (`useMediaQuery` in `page.tsx` decides), picking a
  session puts the rail away, and each carries its own hide button while it is an overlay — and only
  then: beside the emulator, the strip's two toggles are the one way to hide either panel.
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
  still shown as given. `lib/terminal-activity.ts` holds the rule
  once for the strip and the rail. The state arrives two ways: polled with the window list and the
  session listing (every five seconds), and pushed over each visited window's attach socket as a
  `state` frame the moment it changes, which is what makes a tab's mark and title move with the
  shell rather than with the poll. The server side is in
  [`processes-terminal-github.md`](../backend/processes-terminal-github.md#the-terminal).
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
  [`processes-terminal-github.md`](../backend/processes-terminal-github.md#the-terminal)).
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
- **Directional splits** use `lib/terminal-layout.ts`'s binary layout trees, grouped per session in
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
  same folders and pages the Files page draws (`files/file-icon.tsx`), in the colours folders were
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
  terminal**, Terminal actions and fullscreen.
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
  Account profiles and interactive configuration still load; account dotfiles are never edited.
  `term.SetupShell` atomically installs readable scripts in the process-owned shared terminal root's
  `.shell` directory, rejecting symlink or foreign-owned directories. A constant login bootstrap passes
  shell and startup paths as positional arguments; unsupported shells retain their ordinary startup.
  Existing running shells are not modified.
  The terminal host is absolutely inset into its output region so its own rows cannot grow its parent.
- `ResizeHandle` is an invisible eight-pixel hit target over each panel's own border. The border is the
  visual affordance, so the layout draws no extra divider. Arrow keys and double-click reset remain the
  non-drag alternatives.
- `lib/terminal-settings.ts` keeps scrollback and behaviour in localStorage — on the screen, not the
  account, because it belongs to the machine you are sitting at. Font metrics are deliberately fixed: user-selectable line height,
  spacing and fonts made the emulator grid cease to be a stable terminal grid.
- `lib/terminal-keymap.ts` is every shortcut, all rebindable. A chord must get past the browser, the page
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
  ([`processes-terminal-github.md`](../backend/processes-terminal-github.md#sessions-outlive-the-dashboard)),
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
[`../backend/processes-terminal-github.md`](../backend/processes-terminal-github.md#the-terminal).

**Open-shell links are consumed once.** The page removes `cwd` from the current history
entry before creating the session, preserving other query parameters and the hash. A refresh cannot
replay a launch or recreate a closed session; a later explicit Open shell link can still launch anew.
The link gives the session no title: the prompt names the directory, and a title would have pinned the
row to the folder the shell started in.

**The page has no separate header or workspace bar.** A terminal is the one screen whose content *is* the
viewport. "New session" sits alone in the rail's strip; the emulator title bar contains the two
panel toggles and window tabs, with no shell, user or working-directory title. The one banner that stays
is a missing login account — a broken feature rather than information.
