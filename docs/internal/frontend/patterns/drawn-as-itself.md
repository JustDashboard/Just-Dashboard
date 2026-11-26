# Things drawn as themselves

A product, a host, a person and a file are drawn as what they are (D3).

## The rule

A thing the reader already knows by its mark is drawn with that mark, in its own colours, wherever
it is named: on an identity line's tile, in a picture's node, before a fact, in a card, in a menu.

| The thing | Drawn with | File |
| --- | --- | --- |
| A product (an image, a unit, a port's program, an engine, a channel, a certificate's issuer) | `ProductLogo` on a tile, `ProductGlyph` bare in a line, `ProductLogos` as a run; which product a thing is comes from the `…Product` helpers | `components/product-logo.tsx` |
| A product with a state | `StateMark`: the tile with its status dot in the corner | `components/product-logo.tsx` |
| A deployed project | `ProjectMark`, its own website's icon | `components/project-mark.tsx` |
| This server, in a picture | `WireHost` | `components/wire/wire.tsx` |
| A person or an account | `UserAvatar`, `InitialsMark`, `AvatarRun` | `components/user-avatar.tsx` |
| What signed a session in | `ClientMark` | `components/client-mark.tsx` |
| A repository's language | `LanguageMark` | `components/language-icon.tsx` |
| A file or a folder | `FileIcon`, `FolderIcon` | `components/file-icon.tsx` |
| The dashboard itself, as one thing among others | `DashboardMark` | `components/logo.tsx` |

- **The artwork is the product's own**, from `public/logos/`, with its source and licence recorded
  in `public/logos/NOTICE`. No GPL artwork. A logo is never tinted, greyed or redrawn.
- **A thing with no logo keeps a glyph** (`fallback`) and its name. A mark never replaces the word:
  it stands in front of it.
- **A mark is wayfinding, not decoration.** A header, a section, a tile and a dialog carry no
  icon of their own. The marks that stay are the thing itself, the rail's entries, and the ones
  that are the message (a status, a notice, a verb on a button).
- **A new product is added once**: its file, its line in the notice, its id in the helper that
  recognises it.

What follows is the text that was in `design-system.md` §14: why, and the cases. The dated stories
it tells are also why the pages in [`../decisions/`](../decisions/README.md) look as they do.

## 14. A header is its title: things drawn as themselves

**A product is not a kind, and it is drawn as itself.** The template catalogue is sixty-two products
the reader already knows by their marks — n8n, Grafana, Redis — and set as sixty-two names in one grey
face it was a wall of words, with the language marks on the Git tab the only colour anywhere in the
flow. `components/product-logo.tsx` draws each product's *own* logo, in its own colours, on the recessed
tile `ProjectMark` uses for a deployment's favicon, so a template and the project it becomes are drawn
the same way: every template card, each database as the product that answered — its driver's mark, a
flavour's own where the server is one (TimescaleDB, CockroachDB, TiDB, FerretDB), and the mark of a
server the inventory sees and nothing here opens (Kafka, Cassandra, etcd) —, the images on the Images
tab (by the last segment of the reference, Docker's whale for the rest), and the settings panel's
header. The
colour lives in the artwork rather than in a token, which is the same argument as the language marks
— the hue is not this product's to choose — taken one step further: the files are bundled in
`public/logos/` (the page's `img-src` is its own origin, and §8's locked-down networks cannot reach a
CDN), picked in the variant drawn for a dark ground, with their licences in `public/logos/NOTICE`. The
one file whose own colour failed that ground, MySQL's navy dolphin, was lifted to the L 0.72 rung the
`--language-*` tokens sit on. The tile is not an icon plate — it carries no tint of this product's and
sits beside a card's words rather than in front of a header's title — and the source strip still
stays muted, because four *kinds* are not four products. A repository row on the Git tab carries its
owner's picture on the same reasoning: the face is the account, which a glyph could only guess at.

An extension or a module on a database's Settings is drawn as the product it belongs to, on the
same tile: what PostgreSQL ships in its contrib package and its own `plpgsql` as the elephant, a
server's plugin catalogue as that server, a key–value server's loaded modules as it, TimescaleDB as
its own mark, and anything else as a plain puzzle piece rather than a mark guessed from its name.
They had no mark at all, on the argument that one glyph down sixty rows says nothing sixty times;
drawn as their products they tell what came with the engine from what somebody added to it.

The same marks carry into Docker and Databases, because they are the same products. A container,
an image and a stack's service are drawn as the product their image is (`imageProduct` reads the
last segment of the reference; anything it cannot name is Docker's whale). A container whose reference
names nothing — a deployment's is a bare image id — is what its image's OCI title or source label says
(`containerProduct`), since the publisher wrote those and Docker copies them onto the container; the
reference wins when it names a product, because labels are inherited from a base. A stack is drawn as its services'
products overlapping (`ProductLogos`, the way a group of avatars overlaps; Compose's own mark when
none has a logo), a volume as the product of the container that keeps its data there, and a
database connection as the product that answered, not as its driver: `EngineMark` and `EngineGlyph`
(`components/database/kit/engine-mark.tsx`) ask the engine registry, so MariaDB is not the MySQL
dolphin and Valkey is not Redis, on a fleet card, a home's identity tile, the strip's switcher, the
rail's head and every engine picker, which are one `EngineCard` (`choice-card.tsx`) rather than three
shapes that had already drifted. A flavour with no artwork of its own draws the database glyph on
the same tile and never borrows its driver's logo with its own name beside it.
A network has no product of its own. Its row carries its members' products (`ProductLogos`), and its
page draws its bridge as Docker's mark.

**What a backup covers is a product, and so is what a terminal runs.** A coverage row is drawn as
the thing it protects — a saved database as its engine, the proxy's configuration as nginx or Caddy,
a repository as git, a volume as the product of the container that keeps its data there (joined
through the container list, since the report names containers rather than images; a database
container run from a bare image id is its connection's engine), a stack as its services overlapping,
the dashboard as its own mark — and a job as the products of what it covers, with Backblaze drawn as
itself where it writes. S3 is a protocol a dozen providers speak, so the protocol alone is no one
company's, but a bucket is the provider its endpoint names — Amazon's when it names none, because
that is where the SDK sends it, Cloudflare's on `r2.cloudflarestorage.com`, MinIO's on a host
carrying the word (`destinationProduct` in `backups/marks.tsx`) — and keeps a cloud glyph when the
host names nothing; a directory on this server is a server glyph. A run's database dump is drawn as
its driver's engine, and an entry in a run's archive as its file (`FileIcon`). The terminal
draws the program in each window's foreground the same way. A thing none of these can name keeps its
kind's glyph on the same tile.

**The host is a product too.** The Overview, Metrics and the logs rail draw the machine as what it
reports itself to be — its distribution (`platformProduct`, from `/etc/os-release`'s id), its processor
(`cpuProduct`, from the model string: AMD, Intel, Arm), its hypervisor (`virtualizationProduct`: QEMU for
a KVM guest) — and a running process as the product it is (`processProduct`: `postgres` is Postgres,
`dockerd` is Docker, `node` is Node.js) in the top processes the Overview and the Metrics page share
and in every row of the live process table. Metrics draws two more of the machine's parts by
whose they are: an interface by the name its owner gives it (`interfaceProduct`: `tailscale0` is
Tailscale's, `docker0`, a `br-` bridge and a `veth` pair Docker's, an `eth0` its kind's glyph), and
a temperature by the hwmon driver that reads it (`sensorProduct`: `coretemp` is Intel's, `k10temp`,
`zenpower` and `amdgpu` AMD's, an NVMe drive or an ACPI zone nobody's). The other three Processes pages read their rows the same way: a systemd unit as
the product it runs (`unitProduct` — `postgresql@16-main.service` is Postgres, `pm2-deploy.service`
PM2, `certbot.timer` Let's Encrypt's renewal, by the unit's name with its suffix and instance
dropped, then by its first word), a PM2 application as its interpreter (`pm2Product`: Node unless
the ecosystem file says Bun or Python, a glyph for a binary), and a cron line as the program its
command starts (`programProduct`, the terminal's reader: `docker system prune` is Docker's, a
script of the operator's own keeps the clock). Each returns nothing for a name it does not know,
and the tile keeps a glyph: a Tux on an unrecognised distribution, or a guessed logo on `bash` or
`apt-daily.timer`, would be the drawing lying about the row. A reading that counts products carries
them after its words (`ProductGlyphs`): the Overview's Docker tile draws the images its running
containers are, Databases the engines its connections speak, Git the forges its checkouts push to, and the live table's Processes tile what
the machine is running. The Services page draws each busy service and each recent change as its
product beside its name, which is where its Active and Failed tiles carried their marks.
The Live page opens on the machine's identity line — the same one, with the table's cadence and cap
at its right end where Metrics keeps its range — and PM2 on PM2's own: its mark, the account, the
Node it runs, the boot hook and the last save as facts, and whether it resurrects as the verdict
beside its name, with Start application as the line's one command and the daemon's verbs in its menu.

And the dashboard's own two pages draw what it is made of and reached through: the stack's three
services as Caddy, Next.js and Go, its checkout as Compose, the certificate modes as Tailscale and
Caddy (and a lock glyph for plain HTTP, which is no product), the trusted certificate's issuer as
Let's Encrypt, and the repository it updates from as GitHub. Inside a line of text a tile is a box
in the middle of a sentence, so `ProductGlyph` draws the artwork bare at the line's height — the
GitHub mark before the repository's name, Let's Encrypt's before "through Tailscale".

**What signs in is a product too, and a person is drawn as their face.** The account pages draw a
session as its browser's own mark with the system it runs on as a badge in the tile's corner — the
browser is what the reader recognises, so it is not a `ProductLogos` pair in which the second tile
covers the first — and a program that signed in (curl, Go, Python) as itself; its address as the
network it is on, with Tailscale's ranges drawn by Tailscale's mark (`lib/clients.ts`: `parseAgent`,
`networkOf`). A key is drawn as the service its name says holds it: minting one asks where it will
live, so `github-actions` is GitHub's, and `backup-cron`, whose name says nothing, keeps the key glyph
(`keyProduct`). An account is its picture, and without one its initials take a hue by the username
from `LANES` — `AuthorMark`'s argument: a users list of eight brand-blue squares was a texture, and
the same person now keeps one colour in the rail, the list and their own profile. The profile opens on
`IdentityHeader` with that picture where the tile would be, which makes it the fourth page that
describes a thing the same way; a project's header is the fifth, and a deployment's run page
opens on that same header saying what the run is (`run-header.tsx`) — it had its own identity line,
a 48px tile and the duration as a 24px figure, until the operator asked for the project's compact
one. A game server's Players and Console pages add one line under that header (`GameIdentity`; its
Server settings keeps to the settings column and leaves it off) with what only
the game can say — the address a player types, the edition, how full it is — and draw neither the
game nor its name again.

**What a host has installed, who is on it and what they changed are products too.** Packages
draws a package as the software its name says it is (`packageProduct`, `components/packages/marks.tsx`:
`postgresql-16` and `libpq5` are PostgreSQL, `python3-requests` Python, `linux-image-*` the kernel's
Tux, `python3-certbot-nginx` Let's Encrypt) and a name that says nothing as its archive section's
glyph on the same tile (`sectionGlyph`: a library as layers, a tool as a wrench), so `libc6` is never
drawn as a guess. The page opens on the host's identity line with its distribution as the mark and the
manager beside the name, an upgrade's origin is the archive that published it (`originProduct`:
Ubuntu's security pocket, `apt.postgresql.org`, a `pgdg16` repository), and the version an upgrade
lands on keeps the part it shares with the installed one stepped back and the part it changes in ink
(`VersionTo`, cut at the last field both versions end), amber only for a security fix. System users
draws a person as their initials in the hue their name has everywhere else — the host's `ion` and the
dashboard's `ion` are one colour — and an account a package made for its daemon as the product that
runs under it (`accountProduct`, `components/system-users/marks.tsx`: `postgres`, `redis`,
`gitlab-runner`), with `www-data`, which is nginx's on one host and Apache's on the next, left as a
glyph; a group that reaches root is drawn behind a shield or as its product (`docker`, `lxd`), and a
login shell as its own (`shellProduct`), under ids of their own so a process list still draws no
mark for a shell. The audit log draws each entry with the mark of the part
of the product it touched (`auditSection`, `components/audit/marks.tsx`: Docker's whale, Git's and
GitHub's marks, PM2's, fail2ban's, the terminal's, Let's Encrypt's for an issued or renewed
certificate, the dashboard's own for its settings, and the sidebar's glyph for every section that is
no product), the action's first word in its `LANES` hue the way the log console draws a program, the
route's method and path in the request log's hues (in the entry a row's action opens, since 0.8.7
made a row one line), and who acted as their face — an API key's use
with a key in the tile's corner and the key's name after the account's, a webhook as the webhook's
mark and the dashboard's reconcilers as the dashboard.

**A project is drawn as its website, else as what it is.** `ProjectMark` tries three things in
order, all on `ProductLogo`'s tile so a card does not change shape when an icon arrives: the icon
the site declares (read through the dashboard's origin), then the product the project is
(`projectProduct` — the template, the image's product, Compose, the framework detection recognised,
else the recipe's language, Docker for a Dockerfile, nginx for a static site), then its workload's
glyph (`WORKLOAD_GLYPH`: a globe for a site, a box for an image or a service, layers for a stack,
servers for a game). A repository's own site says what it is and not what it runs on, so there the
favicon carries the framework or language as a badge in the corner, the session list's
browser-over-system shape; a template's or an image's favicon already is its product, and the badge
would be one logo twice. It is one tile rather than `ProductLogos`, because a column of titles has
to line up — a stack's services are drawn after its words instead. The fleet card, the rail's
scope head, the project identity line and the notification picture's source all draw it; the archive
draws the product alone, at a step of opacity — a project at rest, the way Backups dims a paused
job.

**A project is drawn as itself wherever it appears, not only on Deploy's pages.** A database's
reader was a cloud glyph and a site's application its container's image, so the one project read as
three things across the product. Another section's payload names a project by its id alone, and
`useProjects` reads the fleet once for every mark on the page. Where the row draws its own tile,
`ProjectMarkOf` draws `ProjectMark` over the tile it had before; where the tile belongs to the
picture — a `WireMark` with its state's tint, a 16px glyph inside a line, the palette's result mark
— `ProjectFavicon` (or `ProductLogo`'s `favicon`) puts the icon in it, with the product or glyph it
drew before wherever the project has no icon to serve. A site's application leads the engine's tile
in its stack (`ProductLogos`' `lead`), and a backup job leads its products with the project it
covers. A reader without Deploy's read keeps the old marks: the fleet is refused, not guessed.

**Where a project comes from, and what builds it, are products too.** A Git remote, a clone URL, a
registry host or an image reference is drawn as the forge or registry it names (`hostProduct`:
GitHub, `ghcr.io` included, GitLab, Bitbucket, Codeberg, Gitea, Forgejo, Docker Hub, Quay, Harbor,
Azure, AWS and Google Cloud's registries, and a self-hosted host whose name carries one of those
words), falling back to git's or Docker's own mark — on a fleet card's source line, the run page's
header, and as the field is typed on `/deploy/new`'s Clone URL and Image reference and the
credential sheet's Host. A saved credential is the host it signs in to, an SSH key
with a key in the tile's corner and a GitHub App credential as the installed account's face with
GitHub's there. What is pasted into a credential's secret is read for what it says about itself
(`lib/secrets.ts`): providers prefix their tokens so that secret scanners can find them, so
`glpat-` is named a GitLab token the moment it is pasted and a mismatch with the host is said
before it is saved, and a key's first line names its format — which is how the public half of a key
pair, pasted where the private half belongs, is caught. A build is its framework
(`frameworkProduct` over detection's ids — Next.js, Django, Laravel, Spring Boot), else its
recipe's language (`recipeProduct`; Bun rather than Node.js when that is what runs it), else what
its method is (`buildMethodProduct`), and a package manager is itself. A GitHub App that is not
connected is GitHub's own tile, and a GitHub CLI that is not signed in the terminal's tile with
GitHub's mark in its corner, `ClientMark`'s shape.

**Where an outcome goes, and who came asking, are products.** A notification channel is its service
(`channelProduct`): Discord, Slack, Teams, Mattermost, Telegram, ntfy, Gotify and Pushover as
themselves, a signed webhook as the receiver its
URL's host names (`webhookProduct`: n8n, ntfy, Gotify, Home Assistant, Healthchecks, Uptime Kuma)
and the webhook's own mark otherwise, e-mail as an envelope or as the mail service its server's host
names with an envelope in the corner; a paused channel is drawn greyed. A request's client is
`ClientMark` again, now shared with the account pages (`components/client-mark.tsx`), and a crawler
is the search engine it belongs to — Googlebot is Google, bingbot is Bing — or a bug glyph when
nothing names it (`agentProduct`); a referrer is the site it came from (`refererProduct`: Google in
every country domain, GitHub, Hacker News, Reddit, X, LinkedIn); an address is the network it is on
(`NETWORK_GLYPH`: Tailscale's mark, this server, the local network, the internet).

**A variable is the service that holds it, and a certificate its issuer.** `variableProduct` reads a
service word anywhere in an environment name — `STRIPE_SECRET_KEY` is Stripe's, `SENTRY_DSN`
Sentry's — and otherwise the first word, which is a framework's prefix (`NEXT_PUBLIC_`, `VITE_`);
`DATABASE_URL` names nothing and keeps the key glyph, and a typed reference is drawn as the database
it points at, in its engine's colours. A prefix two names share takes a `LANES` hue, so a family of
`STRIPE_*` variables is found as one. A domain's certificate is drawn as its issuer — Let's Encrypt
from the intermediate's name (`issuerProduct`), read off the certificate the route actually matched
and never guessed from how the domain is owned. A running service is its image's product, else the
project's (`deploy/service-product.ts`); a mount is the file manager's folder in the colour the
operator gave it, or its volume's product; and a Docker event on a project's Logs page is drawn on
its project's or image's tile with what happened as a toned badge in the corner.

**And a person is their face, or their initials in their hue.** `RunActorMark` draws who started a
run: you, as your own picture; anyone else as `InitialsMark` in the `LANES` hue their name has in
the rail — another administrator's picture would cost a users request per list, and the hue is the
same colour they are everywhere else; a push as the host its remote names, a pull request's preview
as its forge, and a schedule, an API call or the system as a glyph on the same tile. `ForgeFace`
draws a pull request's or a commit's author, GitHub's picture where the forge has one and
`AuthorMark` everywhere else, and the connected identities and repository owners on `/deploy/new`
are the same squares. A game's players are initials in the hue their names take in the console's
replies, and whoever set a variable is drawn beside it the same way.

The families above brought 57 files into `public/logos/`, keyed in `product-logo.tsx` and each
recorded in `NOTICE`: the dashboard-icons set (Apache-2.0) in the variant drawn for a dark ground,
Simple Icons' framework marks (CC0) filled with their brand colour — the black ones, Express,
Fastify, Remix, Symfony and Koa, filled white — with Azure's, Django's, .NET's and Solid's colours
lifted to the L 0.72 rung the way MySQL's navy was. Rust's and pnpm's originals, dark marks drawn
for the file manager's paper page, stay, while the tile draws light copies (`rust-light.svg`,
`pnpm-light.svg`), and two marks already keyed, Valkey's and FreshRSS's, were lifted after the
contact sheet showed them failing the ground. The notification kinds added in 0.8.2 brought three
more from the same set — Pushover's, Teams' and Mattermost's, the last with its navy lifted to the
L 0.72 rung — and draw ntfy and Gotify with the files the webhook receivers already had. The
multi-service templates brought thirteen more from the same set: WordPress, Ghost (its light variant,
rasterised to 96px like Compose's), Plausible, Matomo, listmonk, Paperless-ngx (its leaf lifted to the
L 0.72 rung), Miniflux (its light variant), Wiki.js, authentik, Keycloak, Docmost (rasterised), Firefly
III and Planka; a template that pairs an application already drawn with its database
(`nextcloud-mariadb`, `n8n-postgres`, …) is keyed to that application's file. The second batch of
templates brought thirteen more: Activepieces, Flowise, LibreChat, LobeChat, Chroma, Budibase, Grist,
Documenso, Rallly, Wakapi, Duplicati, AnythingLLM (its light variant) and GlitchTip (its purple lifted
to the L 0.72 rung); Langflow, LiteLLM, Appsmith, GoatCounter, SFTPGo and Apprise have no vector mark
in the collection and keep their category glyph.
The third batch of one-click templates brought eighteen more from the same set — Homarr, Karakeep, Kimai,
Mailpit, MeTube, PrivateBin, Yamtrack and BentoPDF unchanged, LibreSpeed's light variant,
changedetection.io's blue and Homebox's black outlines lifted to the L 0.72 rung, LibreTranslate's
black glyph drawn white, and Dashy, Gotenberg, Linkwarden, PairDrop and ConvertX rasterised to
96px like Compose's — and reuses Excalidraw's; Onetime Secret has no mark in any collection and
Zipline's is a wordmark, so both keep the plain glyph.

The Databases section brought sixteen more, from the same three collections under the same rules:
dashboard-icons' etcd, CouchDB, Cassandra, OpenSearch, FerretDB and Kafka (its white variant);
devicon's Memcached, NATS, YugabyteDB and Elasticsearch — dashboard-icons ships Elastic's company
mark under that name, and a product is drawn as itself —; and Simple Icons' TimescaleDB, TiDB,
ScyllaDB, DuckDB, CockroachDB and Neo4j, whose dashboard-icons file is the disc that product retired.
Seven colours were lifted to the L 0.72 rung after the contact sheet: CockroachDB's violet,
FerretDB's navy, both of OpenSearch's blues, Cassandra's lashes, Elasticsearch's charcoal band and
Neo4j's blue. SQL Server's file became devicon's silver-and-red mark, its dark red lifted, in place of
a red outline that covered half a per cent of a 14px glyph; Azure SQL Edge, which no collection
draws, is keyed to it. Percona Server, KeyDB and Dragonfly are in none of the three and keep the
database glyph rather than MySQL's or Redis's mark: a product with no licensed artwork has none, and
is never drawn from memory. ScyllaDB's only licensed rendition is a hairline outline, faint at 14px
on a 1x screen and left as drawn.

**The same argument buys the git surface its own glyph set.** Heroicons draws no branch, no commit
and no pull request, so `icons.tsx` maps those words onto the share, hash and chat-bubble marks —
near enough on any other page, and wrong on the one screen where the reader identifies the thing *by*
the drawing. `components/git/glyphs.tsx` takes seven from Material Design Icons — the six git marks
and the ringed dot a forge prints in front of an open issue — and is the only file that imports
that set. Nothing else is imported from MDI there: a glyph that exists
in both sets stays Heroicons, or the git pages grow a second icon weight. `AuthorMark` in `git/marks.tsx` is the coloured-wayfinding rule again — a column
of commits where mine and the bot's are two hues is scanned, one where they are the same grey is read
— drawn as the account face without a picture (`InitialsMark`): the initial on a wash of the `LANES`
hue the name is given in the rail and as a run's actor, hashed without its case, so one person is one
colour in a commit line, a run row and a forge's face alike. A square rather than a circle, because a
filled 16px round mark with a character in it is the pill §4 deleted. The one forge face that is
round is the account the dashboard is signed in as (`ForgeFace account`, in the workspace strip's
account control and beside the comment box), because that face is an account and §4 draws every
account round, with `data-slot="user-avatar"` so the pill check knows it.

**A pull request's state is its forge's glyph in its forge's colour.** A list of requests whose
state was a word in the same grey as everything else is read; GitHub prints the state as the mark in
front of the title — green while open, violet once merged, red when closed without it, grey as a
draft — and that is the index the reader already has. `git/pull-state.tsx` draws it
(`PullStateMark`, `PullStateWord`) in `--pull-open`, `--pull-merged`, `--pull-closed` and
`--pull-draft`: GitHub's hues at one lightness, kept apart from the status hues so an open request
never reads as a healthy service, and so merged has a hue none of them owns. How the checks and the
reviews stand are readings of state, so they keep the status hues (`ChecksMark`, `ReviewMark`, an
Actions run's tick, cross or clock). A merge commit in History and a merged branch in Branches take
the merged glyph and hue for the same reason.

**A checkout is drawn as what it is written in.** `/git` cards and the workspace strip open on
`RepoMark` — the logo of the checkout's largest language on the product tile, git's own mark where the
server could not read one — with the language strip under the name: the share of tracked bytes per
language (`languages` on the repository summary) as one bar in the `--language-*` hues, and the
largest three by their own logos (`git/languages.tsx`, over `lib/git-languages.ts`). The owner's
face stays on the shelf rule, said once for every card under it.

**The workspace strip folds, it does not wrap.** It is one row from a 56rem strip (`@4xl` on its own
container, not the viewport, since the sidebar decides the width): who, then where HEAD is, the sync
verbs, the menu, and the account across a rule. Below that it is exactly two full rows — the checkout
with the menu, the account and help at its end, then the branch stretched beside fetch, pull and push
— because a free `flex-wrap` stacked it four rows deep on a phone with half of each row empty. Folded,
the path gives way to the forge slug (it stays on the title's tooltip), the sync verbs drop their
words below 32rem, and the account is its avatar alone below 36rem (`compact="fit"`).

**Text a person wrote on a forge is drawn as the Markdown it is.** A pull request's description and
each comment on it are rendered by `components/markdown.tsx` from the tree `lib/markdown.ts` reads, on this
product's type ladder rather than GitHub's, with a `#123` and an `@mention` linking to the forge. It is
drawn with elements, never `innerHTML`; raw HTML is dropped where it is layout and shown as text
otherwise, and an image becomes a link to itself, because the page's `img-src` is its own origin.
