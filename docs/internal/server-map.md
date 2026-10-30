# The server map

`/map` draws every system the server runs as its own diagram: each deployment, each Compose stack
nobody deployed, each PM2 app, a service something is wired to, a container or a database on its own —
and this dashboard itself — with what it is made of and what it is wired to, left to right the way a
request travels and the data settles. It answers "what does this deployment touch, and what touches
it" without opening every section in turn, and every box on it is a way to the page that manages that
thing.

The backend is `api/handlers_map.go` (the routes, the reads, the per-account filter),
`api/map_sources.go` (the held answers), `api/map_build.go` (the pure join: the kinds and the lines a
kind draws as it is read) and `api/map_links.go` (the lines between kinds, and one line per pair); the
store holds `map_layouts`. The frontend is `app/(dashboard)/map/`, `components/map/`,
`lib/server-systems.ts` (which systems there are, and where every box in one goes) and
`lib/server-map.ts` (what each kind is called, and the arrangement's document).

## What the map is not

- **Not a place to change the server.** Nothing on the map mutates anything. A box opens its own page,
  which keeps its own capability checks, destructive guards and typed confirmations; *Deploy a
  project*, on a server with nothing to draw, goes to the page that does that. The only write the map
  makes is the account's own arrangement.
- **Not a diagnosis.** A box's state is what that thing's own page measures — a container's state and
  health, a backup's last run, a certificate's days left, whether a database answered the fleet's
  dial — cut to one word and one dot. The map reaches no verdict of its own and carries no attention
  list (0.7.5's rule). A source that could not be read says so, with its reason, and is never drawn
  as an empty system (security invariants: an unavailable reading is neither healthy nor a problem).
- **Not a guess.** A line joins two things only where the server knows they are joined: a label, a
  route the proxy holds and the socket's owner behind it, a binding, a variable's typed reference, an
  address the databases topology found in a running container's environment, a mount, a folder a
  backup takes. No relation is inferred from a name.

## What is on it

Only what a system is made of: deployments, this dashboard, Compose stacks, containers, databases,
volumes, domains and their certificates, streams, the checkouts things are built from or run in,
backups, the services and PM2 apps something is wired to, and the tailnet. Images, networks,
accounts, API keys, shells, drawings, schedules, alert rules and the firewall are on their own pages
and not here; the backend still sends them (`GET /map` is one reading for every page that wants one),
and `SYSTEM_KINDS` is what the map takes of it. A systemd unit nothing is wired to is not drawn: a
thousand units are the services page's to list.

## Systems

`detectSystems` (unit-tested in `lib/server-systems.test.js`) finds them in this order, which is the
rail's: this dashboard (`dashboard:self`), the deployments, the stacks no deployment runs, the PM2
apps, the containers on their own, the databases nothing above took, the services something is wired
to, and last one system — *Everything else* — for what nothing claims (a domain whose upstream is not
on this machine, an unused volume, a folder backup, a checkout nobody builds from). Within each kind,
worst first and then by name.

A system grows from its anchor along the links the server sent, and only along them (`CLAIMS`): a
deployment takes its release containers and stack, the databases it is bound to, names or depends on,
the domains routed to it and the checkout it is built from; a stack takes its members; a container
takes the volumes it mounts, the databases it serves, pools for or names, the domains and streams
routed to it, and the backups that pause it; a database takes the container or unit it runs in, its
pooler and the backups that dump it; a domain takes its certificate; a volume or a checkout its
backups; a PM2 app its working folder's checkout and its domains; this dashboard its own stack, the
domains routed to it, its address, its store and the tailnet its address is on. Claims run one way,
from what a system is towards what it keeps — a container never takes the stack it is in — so a
database container in somebody else's stack does not pull that stack into a deployment's diagram.

What two systems both reach is drawn in each and marked shared (`SystemNode.shared`): one Postgres
three deployments use is in all three, with its container, its volume and its dump, so each diagram is
whole on its own; the inspector lists *Also drawn in* and flies to the others.

## The shape

Each system is laid out in columns (`place`): the tailnet, then where traffic arrives (domains,
streams, the dashboard's address), then what the system is (the deployment, this dashboard, a stack's
own card), then what it runs (containers, services, PM2 apps), then what that keeps (databases,
volumes, the dashboard's store), then what takes a copy (backups). A column a system has nothing in is
not drawn. Lines leave a box on its right and arrive on the next one's left, a workflow editor's
wiring. Within a column the boxes are ordered to cross as few lines as the column before allows (two
sweeps of barycentres), then set level with what they are wired to where there is room and pushed down
where there is not. The same reading lays out the same diagram.

- **A stack a deployment or this dashboard runs is a frame** round its containers, with one line from
  the deployment to the frame rather than one to each member. A stack that is a system of its own is a
  card at the head of its members, wired to each.
- **What hangs under a box rather than in the flow is a satellite** — a round sub-node on a dashed
  line from a labelled port on the box's foot, n8n's tools under its agent: the checkout a deployment
  is built from or a PM2 app runs in (*Source*), a domain's certificate (*TLS*, one under each name it
  covers), and the logs a deployment, this dashboard, a container on its own or a PM2 app opens on
  (*Logs*, a box the frontend adds that opens that thing's own logs).
- **Every kind is its own shape** (`Face`), so a domain is told from a database before either name is
  read: the deployment and this dashboard are the large card (`hero`); where traffic arrives is
  rounded on its left like a trigger (`entry`); a database is a cylinder; a volume a disk; a backup a
  card with its schedule along its foot; a service or a PM2 app a terminal's window; a container is
  drawn by its role (`containerRole`) — a web container wears a browser's bar, a proxy is cut to a
  gateway's point, a database container is a cylinder, a cache and a queue are smaller and rounder,
  an API, a worker, storage, search and monitoring say so in the eyebrow. The role is read from the
  image's product (Postgres is a database wherever it runs) and, for a language's image, from the
  container's name (`shop-worker-1`, `just-dashboard-backend-1`). It decides how a box looks and
  nothing else: no line is drawn from it.
- **The atlas** (`layoutAtlas`) puts every system's frame on one canvas, in rows that read in the
  rail's order, about one and a half times as wide as the square their areas make. A frame's head is
  the system's mark, name, kind, what it holds and its anchor's state.

## Moving it

The canvas moves the way a whiteboard's does (Excalidraw's), set once in `MOVE` in `canvas.tsx`:

- **On a trackpad** two fingers slide it (`panOnScroll`, at the fingers' own pace) and a pinch zooms it
  about the fingers — a pinch arrives as a wheel with Ctrl held (`zoomOnPinch`). Safari sends its
  pinch as `gesture*` events instead, and would zoom the page with it: the canvas takes those itself,
  zooming about where the pointer last was, and lets go of any Ctrl-wheel Safari also sends during
  one so a pinch zooms once.
- **With a mouse** a drag on open ground slides it — inside a system's frame as well as between frames,
  since a frame is not draggable and so not `nopan` — and so does a middle-button drag anywhere. Its
  wheel slides it too (Shift for sideways); Ctrl or ⌘ with the wheel zooms. Space held makes any drag
  a slide. Double-clicking the canvas does not zoom.
- **Keys**, wherever the page has the keyboard and not a field: Ctrl/⌘ `+` and `−` zoom, Ctrl/⌘ `0` is
  the real size, Shift+1 fits every system and Shift+2 what is chosen. The zoom buttons show the
  percentage, which goes back to 100% when pressed.
- **Nothing changes size but by the zoom.** Every word, box and line is drawn once at its own size and
  the zoom scales the picture; from far out the boxes and their names are smaller, never re-lettered,
  faded or swapped for a title.

A box can be dragged where the reader wants it inside its system: the place is kept against the
account, by node id and relative to its system (`system:project:1/db:3`), so a reading that adds a
container lays that one out and moves nothing the reader put. A satellite nobody moved follows the box
it hangs under, a stack's frame is drawn round wherever its containers are, and the system's frame
grows round whatever left it (`settle`) without moving the frames beside it.

A whole system is carried by its frame's head (`map-frame-grip`, a grab cursor): the canvas picks it
up on the press, moves it once the pointer has gone a few pixels — less is a click, which only makes
it the system being read, without moving the camera — and saves the frame's corner by system id when
it is put down. A system nobody carried keeps the place the atlas gives it.

**Tidy up** forgets every box's position and every system's place (with *Undo* in its toast);
**Forget this layout** in Options throws away the whole arrangement, the system last read included,
behind a confirmation.

## Reading it

- **The rail** lists every system, grouped by what anchors it, with what it holds in a few words and
  its anchor's dot; a row flies the canvas to its system, which the next visit opens on. *Show all*
  and the canvas's *All* fit every frame. `/map?system=<id>` opens on one system.
- **Choosing a box** lights what it is wired to in its system, draws those lines in the brand's blue
  with how each is known written on them — a pulse travelling the line where both ends are up — and
  steps the rest of the system back. Double-clicking a box, or Enter, opens its page; double-clicking
  a frame opens its anchor's.
- **The inspector** has one shape: a head naming the kind; the thing itself with *Open*, its logs
  where it has them, *show it with what it is wired to* and *copy the name*; then *Details* (the facts
  the server sends with it), *Wired to* (each line as a `Tag`, a pair the server knows several ways
  named by the surest, `mapViaOrder`, the others in its title) and *Also drawn in*. With a system and
  no box chosen it is the system's account, box by box; with neither, the list of systems. Pointing
  at a row rings its box on the canvas.
- **Search** (`/`) keeps the systems in the rail and the frames on the canvas that hold a match — by
  name, state, detail, product or role — rings each matching box, and Enter chooses the first and
  frames it with its neighbours.
- **No box spills.** A box's name and state are cut short with an ellipsis rather than wrapping or
  running past its outline, so the sizes `server-systems.ts` lays out are the sizes drawn.
- **A source that could not be read says so** over the canvas, with its reason, and is never drawn as
  an empty system (security invariants: an unavailable reading is neither healthy nor a problem).
- **Keys**: `/` search, `f` fit the system being read (or every system), `i` the inspector, Enter
  opens the chosen box's page, Escape lets it go; the zoom keys are under *Moving it*.

## What keeps it light

- **One request.** `GET /map` answers for the whole server, and is polled every 20 s (every 3 s while a
  source is still being read on arrival, so the map fills in seconds). The last answer seeds the next
  visit.
- **Each source is held** (`mapSources`): its answer stays true for as long as that kind of thing does,
  only one read of a source is ever in flight however many maps are open, and a request waits a short
  budget for a fresh answer and otherwise draws the last one. A read that fails keeps the last good
  answer beside its error.

  | Spec | Sources | Holds | Waits |
  | --- | --- | --- | --- |
  | fast | containers, projects | 10 s | 2.5 s |
  | steady | images, volumes, networks, databases (the fleet's own dial, shared through its cache), bindings, the deployments' references, dependencies and poolers (`relations`), the listening sockets and their owners (`listeners`, as `GET /proxy/ports` reads them), sites, backups, PM2, channels, drawings, API keys, alert rules, accounts, computers | 30 s | 2.5 s |
  | slow | the databases topology (`uses`: every running container inspected for the addresses it names, every engine asked for its sessions), certificates, streams, timers, systemd units, the system's cron files, VPN, firewall | 2 min | 1.2 s |
  | rare | repositories (a walk of the Git roots), Linux accounts, this dashboard's version | 5 min | 1.2 s |
  | holding | open shells (in memory) | 3 s | 1 s |

  A failed source is asked again after 30 s at most. Every account's crontab (`crontab -u` per account)
  and the package manager are not read at all: their pages are where those costs belong.
- **Each kind is capped** at 300 things, worst first, with the true total.
- **The canvas draws what is on screen** (`onlyRenderVisibleElements`), and a box's data is the same
  object while nothing it draws has changed (`faceData` in `canvas.tsx`), so a click re-renders the
  boxes it lit or dimmed rather than all of them; zooming or sliding re-renders none (it is the
  viewport's transform), and carrying a system moves only its own boxes.
- **Every node carries `measured`.** React Flow keeps a node's handles — where its lines attach — only
  while the node it is handed says it was measured; a reading or a click that hands it fresh nodes
  without that drops every line on the canvas. The layout knows each box's size, so it hands it over
  measured.

## Links

Each line is read from the source that knows it. Where one pair is known several ways it is drawn once
(`mergeLinks`), as the surest way in its direction, the rest in `also`.

| `via` | From → to | Known by |
| --- | --- | --- |
| `binding` | project → database | `deploy_database_bindings` |
| `reference` | project → database, domain | a `${{database.…}}` or `${{domain.…}}` among the environment's current variables, its target read as the runtime reads it (`databaseReferenceTarget`) |
| `dependency` | project → database, volume, domain, backup job | the environment's planned dependencies (`deploy_dependencies`, `release_id = 0`); a bind path draws nothing |
| `release` | project → container, stack | the container's `io.just-dashboard.environment-id` label, and its Compose project |
| `route` | domain → project | the site's owner (`markDeploymentRoutes`) |
| `route` | domain, stream → container, service, PM2 app, this dashboard | the port the site's or stream's upstream names on this machine (`SitesForUpstreamPort`, `StreamsForUpstreamPort`) and who holds it (`AttributeOwners`, as the ports page reads it); a container publishing a loopback port the site forwards to |
| `serves` | container, service → database | the fleet's placement of the connection's server: its container, or the systemd unit a native server runs under |
| `pooler` | container → database | `db_poolers` |
| `source` | project → repository | the checkout a local source is read from (the deepest repository holding it), or every checkout whose remote is the one a Git source is fetched from (`remoteKey`: `git@host:a/b.git` and `https://host/a/b` are one) |
| `workdir` | PM2 app → repository | the repository holding the app's working folder |
| `member` | stack → container, volume, network | the Compose project label |
| `self` | this dashboard → stack | the Compose project of the container mounting the dashboard's data directory (`SelfContainer`) |
| `route` | the dashboard's address → this dashboard | its own configuration (`JD_SITE`): `dashboard:site`, the name its Caddy answers on, with the certificate `JD_TLS` says it serves |
| `tailnet` | the dashboard's address → Tailscale | `JD_TLS=tailscale` (a certificate issued to the machine's MagicDNS name) or a `*.ts.net` address |
| `mount` | container → the dashboard's store | the container mounting the data directory, which keeps `dashboard:store` (`vpsd.db`, its size from a stat, never its folder) |
| `mount` | container → volume | the container's mounts |
| `certificate` | domain → certificate | the certificate's `UsedBy` naming the site |
| `backup` | database, container, volume, repository → backup job | the job's dumps, the containers it pauses, and its source folders holding the volume's mount point or the checkout (or held by it), less an absolute exclude |
| `starts` | timer → service | the unit the timer activates |
| `access` | API key → database | the databases an MCP key reaches |
| `owner` | API key → account | the key's account |
| `notifies` | alert rule → channel | the channels the rule names, or every enabled one where it names none |
| `user` | terminal, PM2 app, cron job → Linux account | the account the shell, the app or the job runs as |
| `image` | container, project → image | the container's image id; the live release's image references |
| `network` | container → network | the container's user networks (not `bridge`, `host`, `none`) |
| `env` | project, container → database | the databases topology: a running container's environment holding one of the server's addresses (the `db-N.jd.internal` alias, the container's name or service, a published address) — a `DATABASE_URL` written by hand, which no binding records |
| `session` | project, container → database | the databases topology: the engine reporting a session from the container's address |

Lines to a thing that is not on the map, or not this role's to see, are dropped (`linksBetween`).

## Who sees what

`GET /map` is open to every authenticated account (scoped and MCP keys are refused as on every other
route); what it answers is what that account's own pages would show it (`filterServerMap`):

- Terminals need `terminal`; Computers, Accounts, System users, Alert rules and VPN need
  `system.admin`. Those kinds are neither read for nor sent to anybody else.
- API keys are the account's own unless it administers the server — the line `GET /tokens` draws —
  and the kind says *yours only* (`state: "limited"`). The map itself draws no keys; the reading
  keeps them for the page that does.
- No value the list routes keep back is sent: no environment, no DSN, no channel URL, no secret.

## The arrangement

`GET`/`PUT`/`DELETE /map/layout` keep one JSON document per account in `map_layouts` (goes with the
account). They need a browser session (`RequireSession`): arranging your view of the server is open to
every role and is no business of an API key. The server checks only that the body is a JSON object under
512 KiB; `decodeDocument` reads it back defensively. A save is debounced (900 ms), reported in the
toolbar (*Saving…*, *Saved*, *Not saved*), sent on the way out of the page on a request the browser
keeps alive, and audited as `map.layout.save` / `map.layout.reset`. The document (`version: 2`) holds
the positions of the boxes dragged, by node id inside their system; the corners of the systems
carried, by system id (`frames`, absent from a document saved before systems could be carried); the
system last read; the view; and the minimap switch. A version-1 document — the mind map this replaced, whose positions placed
shelves round the server — is read as a fresh arrangement, keeping only its minimap switch.

## Tests

- `lib/server-systems.test.js`: a container's role from its image and its name; a deployment taking
  what it runs, keeps and answers to and not what other pages own; its release stack drawn as a frame
  with one line to it; the satellites under their boxes; lines running left to right; no two boxes
  overlapping and every box inside its system; determinism; a database two systems use drawn in each
  and marked shared; a stack of its own as a card wired to its members; this dashboard as a system
  (its tailnet, address, stack and store); services drawn only when wired, a native database named by
  itself; what nothing claims as one system last; absent and unread kinds drawing nothing; the atlas's
  rows; dragging (a box, its satellites following, a stack's frame redrawn); search; neighbours; and
  the document decoder reading a version-1 document as fresh.
- `api/map_build_test.go`: the joins, the measured states (exit codes, project states), unread and
  absent sources, worst-first capping, a box's facts and the per-role filter; `map_links_test.go`: every
  line between kinds in the table above, what must not be joined (a rule's disabled channel, an
  excluded folder, another remote, a shallower checkout, a revoked key, a socket nothing forwards to),
  one line per pair, the remote and folder readings, and the store read of references, dependencies and
  poolers; `map_sources_test.go`: one read while fresh, the budget, the last answer through a failure;
  `TestMapDrawsTheDashboardsAddressAndStore` and its tailnet table: this dashboard's address, the
  tailnet line only where its configuration puts it there, and its store in the container mounting
  the data directory, without the folder;
  `handlers_map_test.go`: the arrangement per account, refused to a key, and the map on a server with
  nothing running; `proxysvc/ports_routes_test.go`: the sites and streams forwarding to a port;
  `store/schema_server_map_test.go`: the table on fresh and upgraded installs.
- `api/map_live_test.go` (`JD_DEPLOY_LIVE=1`): a real daemon's running container wired to its
  volume and a container that exited 3 drawn as down; an application whose `DATABASE_URL` names a
  Postgres container joined to that database by `env`, and the database to its container by `serves`.
- `tests/browser/server-map.spec.ts` (fixture `server-map-fixture.ts`, a server with every kind of
  system on it): the rail's groups and a frame per system; a deployment's columns, its stack's frame,
  its satellites and its shapes, with images and keys left off; this dashboard's tailnet, address,
  stack, roles and store; choosing a box (its lines named in the inspector, the rest stepped back,
  Escape); a shared database's *Also drawn in* flying to the stack that runs it; the rail flying the
  canvas and a search keeping what holds a match; a drag saved by node id, kept across a reload and
  tidied away; a version-1 arrangement read as fresh; every word the same size far out; a trackpad's
  slide, its pinch and the zoom keys; a mouse sliding the canvas from inside a frame; a system carried
  by its head with everything in it, not taken for a click, kept across a reload and tidied away; no
  box's words running out of it with every name and state stretched; and a fresh server's offer to
  deploy with what it could not read. `/map` is one of `design-system.spec.ts`'s surfaces.
