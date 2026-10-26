# The server map

`/map` draws the whole server as one canvas: this server in the middle, seven branches leaving it, and
on each branch a shelf per kind of thing the dashboard manages, holding the things themselves. It is
the page that answers "what is on this machine, and what is wired to what" without opening every
section in turn, and every box on it is a way to the page that manages that thing.

The backend is `api/handlers_map.go` (the routes, the reads, the per-account filter),
`api/map_sources.go` (the held answers), `api/map_build.go` (the pure join: the shelves and the lines a
shelf draws as it is read) and `api/map_links.go` (the lines between kinds, and one line per pair); the
store holds `map_layouts`. The frontend is `app/(dashboard)/map/`, `components/map/` and `lib/server-map.ts`.

## What the map is not

- **Not a place to change the server.** Nothing on the map mutates anything. A box opens its own page,
  which keeps its own capability checks, destructive guards and typed confirmations; *Add* goes to the
  page or dialog that already adds that kind of thing. The only write the map makes is the account's
  own arrangement.
- **Not a diagnosis.** A box's state is what that thing's own page measures — a container's state and
  health, a backup's last run, a certificate's days left, whether a database answered the fleet's
  dial — cut to one word and one dot. The map reaches no verdict of its own and carries no attention
  list (0.7.5's rule). A source that could not be read says so, with its reason, and is never drawn
  as an empty shelf (security invariants: an unavailable reading is neither healthy nor a problem).
- **Not a guess.** A line joins two things only where the server knows they are joined: a label, a
  route the proxy holds and the socket's owner behind it, a binding, a variable's typed reference, an
  address the databases topology found in a running container's environment, a mount, a folder a
  backup takes. No relation is inferred from a name.

## The shape

A mind map rather than a graph (`layoutMap`, unit-tested in `lib/server-map.test.js`). A force layout
puts the same server somewhere different on every visit, and a free graph of three hundred boxes is a
hairball; a tree with fixed sides is a shape somebody learns, as the ER diagram's dagre layout is.

| Branch | Side | Shelves |
| --- | --- | --- |
| Deployed | right | Projects, Databases, Stacks, Containers, Volumes, Networks, Images |
| Web | right | Domains, Certificates, Streams |
| Host | right | Services (systemd), PM2, Dashboard (this dashboard's version), Packages (a tool) |
| Workspace | left | Terminals, Drawings, Repositories, Computers, Files (a tool) |
| Jobs | left | Backups, Scheduled (timers and the system's cron files), Alert rules, Notifications |
| People | left | Accounts, API keys, System users |
| Protection | left | Security (the firewall), VPN (WireGuard, Tailscale) |

The server's middle is the origin. Each side's branches stack centred on the server's line, each
branch level with the middle of its shelves; every shelf on a branch takes the branch's widest shelf's
width so the column has straight edges. A shelf draws its things in a grid that reads left to right
whichever side it is on, two rows of it before "+N more", worst first (the server's order: down, then
needing a look, then by name). An empty shelf the account may fill holds the dashed ring of a thing not
added yet with the verb (*Add a database*); one it may not fill says *No databases yet*. A *tool*
(Files, Packages) is one box that opens its page: every file on the disk is not a box, and asking the
package manager on every read is the costliest thing the map could do.

Every thing is one box: its mark on a 40px tile, its name, and its state in a word with its dot
(`SIZE.item`, 270 × 76). What a server is for — its projects, its databases and the names it answers
to — is a *card* (`face: "card"` in `KINDS`, `SIZE.card`, 364 × 96): the same box with room, its name
a step up and the fact that says which one it is (a project's address, a database's engine, where a
domain goes) on a line of its own under the state rather than cut short beside it. Three cards span
what four of anything else do, gaps and all, so a branch's shelves still end at one edge; what stands
in a card shelf's grid ("+N more", the first one's *Add*) is a card too.

A kind the role cannot open is not drawn (its `capability` in `KINDS`, and the backend does not send
it); a module the server does not run (no PM2, no Docker) is `absent` and has no shelf. Shelves in a
section the account keeps out of its sidebar start folded (`foldedByDefault`), and opening one is
remembered apart from the reader's own folds (`opened`), so a section shown again later is not still
folded by a choice nobody made.

## Moving it

The layout is the same every time, and what the reader moved outranks it, as in the ER diagram.

- **Dragging carries what branches off.** The server takes the whole map, a branch takes its shelves
  and whatever was pulled off them (`followersOf`), and a shelf carries its grid as its children.
  Holding Alt moves one box alone. The positions of everything moved are saved on drop.
- **A thing pulled off its shelf stays where it is put**, hanging from its shelf by a dashed line in the
  muted ink, so which shelf it still belongs to is seen. While it is in the hand its shelf turns into
  the drop target — a dashed brand edge and *Drop here to put it back*, *Let go to put it back* once
  letting go would — and let go over anything else it slides to the nearest clear ground
  (`freeSpot`, unit-tested: searched ring by ring outward, nearest first), so it is never left
  covering another shelf's things. It goes back by being dropped on its shelf, by the ↺ on its own
  corner, or from the inspector (*Put it back on its shelf*). A thing on its shelf has no saved
  position — its place is its slot — so a server that gains a container puts it on the shelf without
  moving anything the reader arranged.
- **Tidy up** forgets every position (with *Undo* in its toast); **Forget this layout** in Options
  throws away the whole arrangement, folds included, behind a confirmation.

## Reading it

- **Choosing a box** lights it and what it is wired to, draws those links in the brand's blue with how
  each is known written on them, and steps the rest back. A shelf chosen lights what is on it; the
  server is what the inspector shows with nothing chosen.
- **The inspector** has one shape for all three: a head naming the kind; the thing itself — its mark,
  its name, its state with the kind, and its actions directly under the name (*Open* as the command
  face, then show it on the map with its links, put it back on its shelf, copy its name); then
  sections, each an eyebrow over a hairline. A thing's *Details* are the facts the server sends with it
  (`facts`: a container's image, stack, published ports and uptime; a database's engine, where it runs
  and how fast it answered; a backup's destination, schedule and runs …) and *Wired to* names each
  link as a `Tag`, read from the chosen box to the row (a project *refers to* its database, a database
  is *referred to by* the project); a pair the server knows several ways is one line, named by the
  surest (`mapViaOrder`: declared before seen), with the others in the tag's title; a shelf lists its
  things; the server shows CPU and memory live from the shell's
  metrics stream, four readings, and every kind on it by branch. Every row is a way to its box, and
  pointing at a row rings that box on the canvas (the `signal` ring a search hit takes), so the list
  and the picture are read together. The keys are written along its foot. Links run over the shelves' ground and under the boxes (`zIndex` 1 between
  the shelves' 0 and the boxes' 2), and two boxes stacked in one column are joined round the column's
  left rather than through it.
- **Search** (`/`) lights what it finds by name, by what a box says of itself, by product or by kind;
  Enter chooses the first and frames it with its links — unfolding its branch and shelf and opening
  the shelf where it sat behind "+N more".
- **Three distances, and no name left to shrink.** `data-zoom` on the canvas is `near` (every word,
  from 0.75), `mid` (a thing is its mark, its name and its dot, from 0.35) or `far` (its name and its
  dot, the name given the box's width). Past near every name is held so it still reads — a thing's and
  a shelf's at 11px on the screen, the server's and the branches' at four fifths of their size — so
  the eye goes heart, branch, shelf, thing at any distance, and the first view takes in the whole
  server down to a zoom of 0.2.
  - *Names in boxes grow by their font size* (`--map-name`, `--map-label`, from `heldScale`), so they
    are laid out and cut short inside their box rather than spilling over its buttons or the next
    box. They grow up to what a line of the box holds (a thing's name four times, so down to about
    0.2) and shrink with the map past that. The canvas writes them in steps of a twelfth of a
    doubling, and only when the step changes, so a zoom lays the names out a few times rather than on
    every frame: measured on a headless VM, zooming a map of about a thousand things with every shelf
    opened showed no difference between the names held and the names fixed (a median frame of
    16.7 ms).
  - *Names with open ground round them are scaled* — the server's, the branches', a link's label —
    against `--map-unzoom`, written every frame and costing no layout, so a long hostname is read
    whole; they let the pointer through to the fold they come to lie over.
  - *Room is given to the names.* A shelf is at least 460 wide, so the longest shelf name, held, fits
    beside its fold at the zoom an empty server first opens at. A head's fold, add and open are not
    drawn far out, where they would be specks, and at mid its add and open show while it is pointed at
    or focused. From mid on, a shelf's offer to add the first one and a tool are their words alone,
    without the ring or the glyph.
- **Keys**: `/` search, `f` fit, `i` the inspector, Enter opens the chosen thing's page, Escape lets
  it go. They are heard inside the map and while the focus is on the page's body (a press in the
  inspector that redraws it leaves it there), never from the sidebar or a menu.

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
- **Shelves are capped** at 300 things each, with the true total; "+N more" counts both what the shelf
  holds back and what the server did.
- **The canvas draws what is on screen** (`onlyRenderVisibleElements`), and a box's data is the same
  object while nothing it draws has changed (`flaggedData`), so a click re-renders the boxes it lit or
  dimmed rather than all of them; zooming re-renders none (the distance is CSS). Measured on a headless
  software-rendered VM: about a hundred boxes paint in a second in 14 MB; 965 with every shelf opened
  hold a median frame of 16.7 ms while zooming and panning, with about two hundred in the DOM.
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
| `self` | this dashboard → stack | the Compose project of the container mounting the dashboard's data directory (`SelfProject`) |
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
  and the shelf says *yours only* (`state: "limited"`).
- No value the list routes keep back is sent: no environment, no DSN, no channel URL, no secret.

## The arrangement

`GET`/`PUT`/`DELETE /map/layout` keep one JSON document per account in `map_layouts` (goes with the
account). They need a browser session (`RequireSession`): arranging your view of the server is open to
every role and is no business of an API key. The server checks only that the body is a JSON object under
512 KiB; `decodeDocument` reads it back defensively. A save is debounced (900 ms), reported in the
toolbar (*Saving…*, *Saved*, *Not saved*), sent on the way out of the page on a request the browser
keeps alive, and audited as `map.layout.save` / `map.layout.reset`. The document holds positions,
folds, opened shelves, the view, and the two switches (minimap; drawing the chosen box's links).

## Tests

- `lib/server-map.test.js`: the registry, the layout (sides, no overlaps, determinism, caps, folds,
  hand-placed boxes, branches carrying shelves, the cards and the four rows they span), dragging's followers, where a box let go lands,
  folding defaults, the document decoder, links, search and the census.
- `api/map_build_test.go`: the joins, the measured states (exit codes, project states), unread and
  absent sources, worst-first capping, a box's facts and the per-role filter; `map_links_test.go`: every
  line between kinds in the table above, what must not be joined (a rule's disabled channel, an
  excluded folder, another remote, a shallower checkout, a revoked key, a socket nothing forwards to),
  one line per pair, the remote and folder readings, and the store read of references, dependencies and
  poolers; `map_sources_test.go`: one read while fresh, the budget, the last answer through a failure;
  `handlers_map_test.go`: the arrangement per account, refused to a key, and the map on a server with
  nothing running; `proxysvc/ports_routes_test.go`: the sites and streams forwarding to a port;
  `store/schema_server_map_test.go`: the table on fresh and upgraded installs.
- `api/map_live_test.go` (`JD_DEPLOY_LIVE=1`): a real daemon's running container wired to its
  volume and a container that exited 3 drawn as down; an application whose `DATABASE_URL` names a
  Postgres container joined to that database by `env`, and the database to its container by `serves`.
- `tests/browser/server-map.spec.ts` (fixture `server-map-fixture.ts`): the sides, "+N more", folding
  remembered across a reload, a branch carrying its shelves and Alt moving it alone, pulling a box off
  its shelf (its shelf the drop target, landing clear of the next shelf, the line it hangs by, back by
  its own ↺), links and the inspector (its details, a row ringing its box), a deployment wired to the
  database its environment names and the checkout it is built from, a pair known several ways drawn
  once, revealing a thing behind "+N more", Add, a reading role
  offered only its own API key, a fresh server, the names held at 11px from mid and far (a thing's
  and a shelf's), and a database drawn as a card with its engine on its own line; `/map` is one of
  `design-system.spec.ts`'s surfaces.
