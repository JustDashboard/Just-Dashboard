# Registers

Each page declares one register, reading or flow, through `Page` (`data-register`), and
`design-system.spec.ts` checks that every page declares one and only a flow page has a foreground.

## The rule

- **Reading** (the default): the reader arrives to find out what is true. Readings on the page,
  not boxes on the page; nothing lifts. Its recipes are
  [`reading-page`](../recipes/reading-page.md), [`list-page`](../recipes/list-page.md),
  [`detail-with-rail`](../recipes/detail-with-rail.md) and
  [`settings-page`](../recipes/settings-page.md). A workbench is a layout of this register, one
  frame held to the window ([`workbench`](../recipes/workbench.md)), not a third register.
- **Flow** (`<Page register="flow">`): a sequence with an outcome at the end. The `h1` is the
  question, a spine says how far through, one surface carries depth (`FlowPanel`) and one command
  advances. Its recipe is [`flow-page`](../recipes/flow-page.md).
- **The test is what the reader came to do, not what the page contains.** A page with a form on it
  is not a flow page: settings are a reading of a configuration that happens to be editable. If
  you cannot name the sequence and its outcome in one sentence, the page is a reading page.
- **A page picks a register; a section does not.** Making a project is a flow and reading one is
  not.
- **The lit edge is not the flow register's property.** A row or a card you take lights under the
  pointer on any page (`ChoiceRow`, `ChoiceCard`).

The table of which page is in which register, and the reasoning, are in §16 below, as
`design-system.md` held it.

## 16. Two registers

Everything above §15 describes a page that **reports**. The Overview, the metrics page, the Docker
lists, Security, the proxy tables, Processes, the audit log: the reader arrives to find out what is
true, and the system is tuned for exactly that. It removes decoration and hands back three loud
things in exchange — a 24px figure, a tone on a reading, a `Status` dot — and every one of them
requires the page to have numbers on it.

A page where the reader is **deciding** has almost none, and spends what it has at the wrong rung.
The 2026-09-20 pass on `/deploy/new` applied §15 and produced a screen on which the 24px step fired
exactly once — the two-word title — against fourteen times on the Overview; both buttons were
`variant="outline"`, so the only brand ink on the page was a 14px icon on the active tab; and
twenty-two repository rows were hand-laid without the arrow `Row` draws through `rowReveal`, so a
page of click targets looked like a read-only listing. The complaint that arrived — "no life,
doesn't look good" — was right.

**But not because the page had no readings.** It had four: the draft count, the App's repositories,
the identity count, and "N of M repositories". Every one was rendered at `text-hint` inside a
`PanelHeader`'s actions. §15 pass 2 names counts explicitly, and `deploy/credentials-page.tsx` — the
same section, also a page you configure rather than read — ran its page heading straight into a
`StatGrid` of `StatTile`s. So the first half of the failure was §15 pass 2 skipped, not §15 being
inapplicable; the HEAD commit that ran the pass audited itself against passes 3, 7 and 9 and never
mentioned 2.

What the second half needed is what this register adds. A flow page keeps §15 pass 2 wherever it has
a genuine set of headline figures — being in register B is not an exemption from readings — and adds
the things a sequence needs that no amount of correctly-applied §15 would have produced: a question,
a spine, a foreground, and a command.

So there are two registers, and a page declares which it is in.

```tsx
<Page register="flow">   // the reader is deciding
<Page>                   // the reader is reading — the default, and most of the product
```

It is stamped on the page element as `data-register` rather than inferred, so which register a page
is in is one grep, a page cannot be half in each by accident, and a browser test can assert that only
the flow pages carry the flow affordances.

### Which register a page is in

The test is what the reader came to do, not what the page contains. A page with a form on it is not a
flow page; Settings is a reading of a project's configuration that happens to be editable. A flow
page is one where there is a **sequence with an outcome at the end**, and the screen's job is to get
the reader through it.

| Page | Register | Why |
| --- | --- | --- |
| Host Overview, metrics, Docker, Security, Network, proxy, Processes, System, Backups, Packages, audit, Git, files, terminal | Reading | The reader arrives to find out what is true. |
| Deployments list, a project's overview, runtime, logs, deployments, requests | Reading | A project that exists is a thing you read. |
| A stack project's Stack page (`/deploy/[id]/stack`) | Reading | The reader arrives to open the stack's apps and to find out how far its own setup is: the server's sentence and state, the five facts each on its own (installed, healthy, connected, able to fetch, able to play), the one next step when something is left (a media source to add, the apps' addresses to give), every app with what it is for, its address and Open, the one login beside them with Show, Copy and Change password, and the setup as a checklist by stage. Retry is a verb on the lead's title line, and each next step one command that leaves for its own page or confirms in a dialog, not a sequence. |
| A stack's media sources (`/deploy/[id]/stack/sources`) | Reading | The reader arrives to find out what the stack searches and to add or remove a source: the sources it has, then the catalogue, searched and filtered by Public or Private. A source opens on the page's own address (`?definition=`) as its fields and Add, with the outcome said in words, the catalogue beside it as a rail from `xl` and one pane on a phone. One form and one verb is an edit of a reading, not a sequence with a spine. |
| `/deploy/new` — the source chooser | **Flow** | Step one of three, and the screen is asking a question. |
| Databases — the control center, a database's home, Search, Generate, Performance, Access, Backups, Alerts, Settings | Reading | The reader arrives to find out what is true of a server and of everything on it. |
| `/databases/[id]/import` — bring a database over from another server | **Flow** | A sequence with an outcome: which server it is on now, how it should come over, and — for a replication — when to cut over, ending in a database here. The spine is taken from the import the server keeps, so a reload, or the next day, finds it where it was. |
| A database's Data, Query, Schema, Diagram and Logs | Reading, as a workbench | The reader works rather than scrolls, so the page is one surface held to the window, on the sheet's edges with no frame of its own (`<Page fill>` through `SectionFrame`, which takes the fact from the engine registry). A workbench is a layout of this register (§2), not a third one: same grounds, same type ladder, no flow panel. |
| The server map (`/map`), and the databases map (`/databases/map`) on the same canvas with one diagram for each database | Reading, as a workbench | The reader arrives to find out what each thing the server runs is made of and wired to. One surface held to the window (`<Page fill="workspace">`), the rail of systems, a canvas and its inspector side by side on the sheet's edges with hairlines between them, the way a database's Diagram is. Each system is its own diagram in a hairlined frame on a ground a step up from the canvas's, and its boxes are the product's own vocabulary — a `ProductLogo` tile, the name, a `Status` dot and word, the eyebrow voice for the kind — given, at the operator's request in 0.8.2, a workflow editor's wiring and an outline per kind: the deployment is the large card, where traffic arrives is rounded on its left like a trigger, a database is a cylinder, a volume a disk, a web container wears a browser's bar, a proxy is cut to a gateway's point, and what hangs under a box (its checkout, its logs, a certificate) is a round sub-node on a dashed line from a labelled port. No shadow, glow or gradient comes with them: the outline is a border and a step of ground, a state is a `rule-*` edge and the dot, and the chosen box's lines are the brand's blue with the §2 pulse travelling them where both ends are up. *Open* in the inspector is the surface's one command face, as is the button a tap floats on a phone, where the canvas is alone and edge to edge, and *Deploy a project* on a server with nothing to draw. No toolbar or minimap: the inspector's toggle and full screen float on the canvas, beside the panel they work. |
| `/databases/new` — add a database | **Flow** | A question with an outcome: which database, started here or connected, ending in the one command that does it. The section's other flow page is Import. |
| Any page with a run of *choices* on it | either | The register is about the page; the lit choice is about the thing. A reading page with a picker on it — the generators on a database's Generate page — gets the edge on that picker and changes in no other way. |
| `/deploy/new` — Configure | **Flow** | Step two of three, ending in the one command that creates the project. Each of its four screens is the run page's rail and inspector before the run: the plan read down a rail beside the one focused surface holding the step's fields. |
| A run in progress (`/deploy/[id]/runs/[run]`) | Reading | You are *watching*, not deciding. The page opens on the project header's shape saying what the run is, its verbs — Cancel, Retry, Redeploy, Visit, the release's menu — at the far end beside its state, and nothing above it: the sequence on `/deploy/new` ended when the project was created, so a first run is read the same way as the fortieth, with no spine claiming the screens before it. |
| Deploy settings, credentials, notifications, templates | Reading | Editable readings of state, not a sequence with an end. |
| Sign-in, first-run setup, `/welcome` — the first-run question | **Flow** | A sequence with an outcome: the account's sidebar. |
| Remote Desktop — the computers (`/remote-desktop`) | Reading | The reader arrives to find out which computers can be opened now. |
| `/system-users/new` — add an account | **Flow** | What it is for, then who it is and how they get in, ending in the one command that creates it. An account nobody can sign in to has to be asked for by name, which is the failure this replaced: a dialog that took a name and left the way in optional made an administrator nobody could ever sign in as. The form is the one focused surface and the column beside it draws what the answers add up to (the way in, the account, how far it reaches). An account's own page, the groups, a group's page and the defaults are reading pages. |
| A Remote Desktop session (`/remote-desktop/[id]`) | Reading, as a workbench | The terminal's columns held to the window around the live screen, but edge to edge: no measure, gutter or frame, since every pixel of those was taken from the screen. It was the first workbench to keep full bleed at every width, because it is a screen; since 0.9.1 every workbench does, as the sheet is their frame (the terminal, Files, a Git working copy, a drawing and a database's views stood in a 12px gutter inside a rounded frame of their own from `lg`, a frame in a frame once the page became a sheet). On a phone it is one pane at a time (Screen, Tools, Computers) over the shared bar; a stream's frame is the computer's own shape, so the space left round it is the workspace's ground rather than black bars; drawn by guacd (Control) or streamed by Sunshine (Stream); what a stream waits on (its bridge, Sunshine, the PIN) is said in the screen's place, with Sunshine's own mark. |
| `/remote-desktop/new` — add a computer | **Flow** | Which computer, is its remote desktop on, and the sign-in: ending in the one command that saves it and opens the screen. |

A section does not pick a register — a *page* does. The deployment section has pages in both, which
is correct: making a project is a flow and reading one is not.

### What register B adds

Each of these is bought against a specific failure, and each is the smallest thing that answers it.

- **The question is the `h1`.** A flow screen asks something — "What are you deploying?", "How should
  it run?" — and the ask is at the page's own rank. It is *not* a sentence under the page's name:
  that is the caption §5 removed from every page in the product, and putting one back at a rank
  invented for it is how the ladder grew thirteen sizes the first time. The step count lives in the
  eyebrow or in the spine, never in a new size between 24 and 15.
- **A spine.** `FlowSteps` says which step this is, what is behind it and what is left, as segments of
  a rule with their names under them — the way `deploy/run-pipeline.tsx` draws the seven stages of a
  release, so the screen where a project is planned and the screen where it is built agree about how
  "where we are" is drawn. **Never numbered circles**: a filled circle under 32px with a character in
  it is the pill §4 deleted, and `tests/browser/design-system.spec.ts` fails any page that renders
  one. Credentials was the last to draw them, for the GitHub App's three setup steps; they are
  three equal segments of the release path's own bar now — green and ticked when done, amber while
  the App waits to be installed, the brand for the step that is simply next — and the spec checks
  that page and Notifications with the showcase's App, accounts and channels in them. Where a spine
  is drawn it *is* the head rule, and `--flow-rule` yields to it.
- **Exactly one surface with depth.** §2 says nothing lifts, and the reason is that forty-nine
  surfaces all claiming the foreground is a page with no foreground. One does not have that problem.
  `FlowPanel` is the thing being decided right now: a step of ground above the card, `border-strong`,
  and `shadow-sm` — the smallest step, and the only place outside a popover, a dropdown and a dialog
  that takes any. **One per screen.** A second is two foregrounds, which is none.
- **One unmistakable advancing gesture.** Either a brand-faced command — `Button` with no variant,
  drawn in `FlowActions` at the foot of the focused surface — or, on a screen where *the choice is
  the advance*, the choices themselves, whose edges light under the pointer. What a flow screen may
  never have is what the Git tab shipped with: a primary action wearing `variant="outline"` while
  nothing else on the page carries the brand either.

  **One, not two.** The Git and Docker tabs each pair a list with a fallback field — paste a
  URL, name a registry image — and the fallback carries a button. While the list has rows in it the
  rows are the advance, so that button is `outline`: a brand face there is the only blue on the
  screen pointing at the secondary path. When the list is *empty* there is nothing to choose, the
  fallback becomes the way forward, and it takes the command face. So the variant is a function of
  whether there is anything to pick —
  `variant={pickable.length > 0 ? "outline" : "default"}` — which is the rule stated in code rather
  than a colour chosen once and left to be wrong half the time.
- **A choice is something you pick.** `ChoiceCard` in a `ChoiceGrid` for the *kinds* of thing — the
  five sources, the templates, the databases. `ChoiceRow` in a `ChoiceList` for *instances* of one
  kind — twenty-two repositories, a page of image tags. The split is load-bearing: a three-column
  grid of twenty-two 13px names is a wall, and a flat row is the listing this register exists to stop
  a decision from looking like. Both keep §12's shape — the title is a real `<button>` carrying the
  verb in its accessible name, the surrounding press is a convenience for the pointer — and add the
  two things a reading row does not have: an arrow that is always drawn, and an edge that answers the
  pointer.
- **The lit edge.** `ui/spotlight-border.tsx` turns a card's whole one-pixel border from its own
  `--border` to `--flow-lit` while the pointer is on the card, with `transition-colors` and nothing
  else; a selected card keeps its full `--brand` edge. It began as Magic UI's `magic-card` with the
  glow taken out, a radial gradient centred on the pointer — until 0.7.1, when that lit one side of a
  wide card and left the rest dark, so the edge that says the *card* is takeable said it about a
  corner. Depth and response; no decoration.

### Three grounds, one ladder

The focused surface began two steps above the card and read as a grey slab laid over the page rather
than as the page's own foreground — at L 0.226 against a 0.145 ground it was the lightest thing in
the product. The border and the lit edge do the separating, so the ground only has to be *a* step:

| Token | L | Is |
| --- | --- | --- |
| `--background` | 0.16 | the page |
| `--choice-surface` | 0.183 | a card you pick — recessed *into* the surface holding it |
| `--flow-surface` | 0.2 | the one focused surface on a flow screen |

The order matters and is easy to get backwards: choices sit **below** the panel that holds them, not
above it. Painting both from one token — which is what shipped first — made every card inside a
`FlowPanel` invisible against it.

### The lit edge is not register B's property

This is the part that decides how the rest of the product changes. The lit edge belongs to **things
you pick**, wherever they are — the deploy chooser, a database engine on Add a database, a credential
kind on a settings page. It is not a flow-page decoration, and `ChoiceCard` carries it for every caller
rather than the deploy pages having a better-looking version of a shared component.

What it does **not** belong to is a row you *read*. A reading page answers the pointer with
`bg-row-hover` and nothing else (§6), and that stays true, because the edge means *this is takeable*
and a table of forty readings where every line glows is a page that means nothing by it. So when a
reading page is revamped:

- a list whose rows are **destinations or choices** — a run to open, a project to enter, an engine to
  start — becomes a `ChoiceList` of `ChoiceRow`s and gets the edge;
- a table of **readings** — the audit log, a metrics grid, a process table — keeps its hairlines, its
  density and its wash. Twelve columns of readings do not become cards. The containers table was the
  example here until 2026-09-23 and is the case that shows where the line is: its cells were live
  readings, but every row opened the container's own page, so the row was a destination with
  readings on it — which is the Git card's shape, not a table's (§12). The operator reversed it in
  0.7.1, and the reason moves the line rather than erasing it: thirty containers are read down their
  columns far more often than one is entered, and Live, PM2 and Services had by then all become
  tables whose rows open something. A table of readings whose row also opens stays a table — the
  name is the button, the row takes Enter, and there is no lit edge; the edge stays with lists whose
  rows are only ways in. The Docker overview drew its containers as a table again on 2026-10-08, at
  the operator's request, and that is the other side of the same line: there the containers are read
  down their columns before any one is taken, so the overview's table keeps hairlines and a wash and
  its rows open by a press anywhere on them;
- a **figure** is still a `StatTile` (§15 pass 2) in either register, with two pages excepted: a
  project's Overview, below, and since 0.8.7 the host's (§15).

A page that is mostly readings with one run of choices in it takes the edge on that one run. That is
the honest answer to "use it everywhere": everywhere something is picked.

The deployment section is the widest application of it so far, and it draws the line in both
directions on one page. Everything there that opens something is a lit card — a project, a run, a
credential or a channel that opens its sheet, a service that opens its container, a domain on
Runtime that opens the proxy site serving it, a webhook, a schedule, an approval or a preview, a
variable, a linked database — and so is every run of choices inside a sheet or a dialog: a provider,
a channel's service, an alert's measure, a schedule's action, the commits a version can be deployed
from, whether a database is created or an existing one used, the saved connections to take. The
traffic-alert rules on Automation are the counter-example on the same page: a rule is read against
its threshold, with a `Meter` carrying a tick where the line is, and opens nothing, so its rows stay
rows you read.

### A project's Overview reads in sentences

At the operator's request in 0.8.5 the Overview carries no `StatTile`: "I don't like them", "clean text
and easy to understand", and sections added later that "feel like they don't belong". The top of that
page already says everything in the wiring's words, so what follows it now takes the wiring's shape, and
a new block on this page takes it too:

- **A block opens on a `WireNode`:** a real mark (`WireMark tone="logo"` and a `ProductGlyph`, with the
  block's state as the `StatusDot` in its corner, as Runtime's node has it), then the reading as a sentence
  and its detail. What the runtime is on Right-size, Trivy on Vulnerabilities, Docker on Availability and
  the source's forge on Delivery: each mark is where the block's evidence comes from.
- **Then its one coloured picture:** each limit's gauge at the end of its reading line, the
  severities' bar, the ribbon of days, the releases per day. The hues are the existing ones (§3, §10), and the reading it replaces is said in the
  words beside it, so nothing is colour alone.
- **The live readings are lines,** `ReadingLine` in `components/reading-line.tsx`, drawn for this page by
  `deploy/live-readings.tsx`: a measurement's glyph on its own tint (`WireMark hue`, the `HUE` map's
  colours), a name, the reading as words ("0.1% of one core"), a detail and its last hour at the line's
  end. Each is one height in every state and opens the page that has the rest. The component is shared
  (0.8.7): `ReadingLines` lays a run of them in two columns, `NoReading` holds a reading's place until it
  arrives, and a line without an `href` is a reading that opens nothing. It is what a tile row becomes on
  a page being brought to this one's shape.

- **A block that shows only in one state takes the shape too.** The release watch and the sleeping
  block were missed in 0.8.5 because each is drawn only while a release is on watch or the environment
  is asleep, and stayed two rows of four tiles until 0.8.6. The watch opens on the proxy that wrote the
  record (Caddy, or nginx where the readings carry no durations) with *Release #12 fails 0.4% of its
  requests; 19 in 20 finish inside 180ms*, the judgement and the countdown under it, and its picture is
  the release before and the release now as two `ReadingLine`s, each the release's number as its mark,
  the share failed, how many it answered and how fast, and at its end one strip of the same length,
  what it answered and what it failed. A release that has answered nothing says *No requests* in the
  line's place and draws no strip: two empty tracks across the block read as a picture that had not
  loaded (0.9.1). The sleeping block opens on Docker's mark with *Asleep
  by design for 42 min*, and its picture is the last seven days as one strip with each stretch asleep on
  it. Their verbs stay in the head: *Keep release #N* and *Roll back instantly*, *Wake now*. The watch's
  lines and the sleeping block's strip keep one height and one place whether there is a reading or not
  (`deploy-rollback-watch.spec.ts` and `deploy-sleep.spec.ts` measure both at 1280 and 390).

The Deployments tab's history and every other page but the host Overview keep `StatTile`: this is
the two Overviews' register of speech, not a retirement of the figure.

### What register B does not get

The bans are not relaxed here. No glass, no glow, no gradient ground, no hover-transform, no shadow
on anything that is not the one focused surface, no pill, no badge, no icon plate, no off-ladder type
or radius, no colour that is not a token. §2 §4 §8 §9 §13 §14 hold in both registers without
exception. What §16 buys is depth on one surface, a mandatory command face, and an edge that responds
— and nothing else. A flow page that also wants a gradient behind its hero has misread this section.

`--flow-surface`, `--flow-lit`, `--flow-lit-soft` and the `flow-rule` utility are register B's whole
palette, and a reading page must not grow a use for them.

**One ground is not flat: arrival's.** Sign-in, `/welcome` and the shell's splash between them are the
only full-bleed screens in the product and the first a new account sees, and they stand on
`.auth-backdrop` — three washes, the signal from the top edge and the chart ramp's blue and violet from
the lower corners (never `--brand`, which is near white and washes to grey). The sign-in card's picture
half is the same lights closer (`.auth-art`), pooled behind the server it draws. The operator asked for
these screens to have colour and life, and for the line grid that used to sit under the washes to go;
the exception is the ground of those screens and nothing else — no page in either register takes a
wash, and nothing on these screens takes a glow, a gradient fill or a pattern.

The update notice above the account card was tried on `.auth-art`'s light at card scale and taken
back off at the operator's request: in the shell the washes read as the sign-in screen pasted into a
dashboard. It is the account card's own container (`border-sidebar-border`, `bg-card`), tinted with
`bg-wash-success` or `bg-wash-danger` only once a run has finished, and its life is the wire, the
kind band and the beam (§11) rather than a ground. Arrival's light stays on arrival's screens.
The hub notice beside it (`hub-link/hub-notice.tsx`) is the same container, tinted with
`bg-wash-warning` or `bg-wash-danger` only for a notice the hub marked as a warning or critical, and
has no motion of its own beyond arriving: it reports what another machine said.
