# Wiring pictures

A picture of what is connected to what, drawn first (D2).

**Members:** `WireFlow` (`wire/wire-flow.tsx`, its arithmetic in `wire/wire-flow-model.ts`),
`WireLane`, `WireLaneTitle` (`wire/wire-lane.tsx`), `WireMarks`, `MoreTile`
(`wire/wire-marks.tsx`), `WireNode`, `WireMark`, `WireHost`, `WirePlaceholder`, `WireLink`,
`WireLabel` (`wire/wire.tsx`), `MonoValue` (`wire/mono-value.tsx`), `useMotionHeld`
(`wire/use-motion-held.ts`), `AnimatedBeam` (`ui/animated-beam.tsx`), and `SettingPicture`
(`settings/setting-picture.tsx`), a settings section's row of two or three marks.

## Which one

| The picture is | Use |
| --- | --- |
| Things on the left, one hub, things on the right, a line from each to the hub | `WireFlow` |
| One node a side, in a settings section, the lines the caller's | `SettingPicture` |
| Tiers, a chain, two lanes a side, many to many, faces on a node | laid by hand on `WireNode`, `WireMark` and `AnimatedBeam`, each column a `WireLane`; below `lg` it draws `WireMarks` |

A picture that is the hub shape uses `WireFlow` and lays its nodes by hand only when it is not that
shape. In 0.9.0 the account's access picture and the link picture (Settings → Connect, Settings →
Hub) moved onto it, and in 0.9.1 the pictures that had been one column a side by hand: the alerts
pictures (`alerts/kit.tsx`), the proxy's route picture (`proxy/route-picture.tsx`), the stream
picture (`proxy/stream-picture.tsx`) and Remote Desktop's (`remote-desktop/picture.tsx`). The
others the 0.9.0 pass looked at stay laid by hand on purpose: a container's
picture (ways in, the container, what it keeps, with its faces), the procs wiring picture, the stack
map (many to many), a project's runtime lanes, the routing decision's two columns, the ports
picture (networks to programs). Porting those would either lose what they show or grow `WireFlow`
into a layout engine.

## WireFlow

**Purpose.** The picture several sections open on, from data: what reaches a thing and what it
reaches, with each line saying its state.

**Anatomy.** Wide, five columns: the start side, a gap the lines bend across, the hub, the gap,
the end side (`hubShare` scales the hub's column, 10rem at the default). The sides share what is
left, so the picture is as wide as its page. A node is a mark, an eyebrow, a name and a line of
detail. The hub's words hang under its mark out of the flow, and the picture holds the room they
need under itself (`useHubRoom`).

The wide picture takes one of two forms, chosen from the room a side has (`flowForm`):

- **Rows**, wherever a side holds its nodes in one row or has room for three across. Each side is
  rows of nodes, as many to a row as fit (`perRow`, evened out: five are three and two), a node's
  words centred under its mark. A row is one level line into the hub that its marks stand on: a
  node's own line runs to the mark between it and the hub (`nearerInRow`), and the nearest node's
  runs on, bends in the gap and arrives level (`AnimatedBeam`'s `acrossRef`). One row a side is a
  single line across the page. With more, the sides are dropped until the hub stands on the middle
  of their rows (`useLevel`), under headings that stay on one line at the top. A short last row is
  pushed against the hub. The rows of a side share the last of their way in, where the line that
  needs a look is painted last (`paintOrder`); every line is drawn from the hub's end out, which
  puts their dashes in the same places.
- **Column**, where a side has no room for three across (beside a rail, on a page with a narrow
  measure). Each side is one column, the start side's words before their marks and the end's after,
  and a line fans from each node to the hub. Words under a mark cost a row twice the height of
  words beside it, so rows of one or two would stand taller than the column.

Until 0.9.1 the column was the only form: eight nodes a side stood 480px tall in the middle third
of a wide page with nothing on either side. `size="sm"` draws small marks, which stand closer
(`CELL`), for a dense picture whose nodes say a name and a word: the alerts pictures.

A page writes a node's `title` and `hint` for both forms: no `lg:text-left`, `lg:justify-start`
or fixed width on them, since in rows the words are centred and a node is about 120px wide. A
line that must stay whole is a `block truncate` with its `title`. Several facts on one line are
inline blocks, not a flex row, so the node's own alignment sets them and the line breaks between
two facts and never inside one (`Facts` in `proxy/route-picture.tsx`). A fact that is the expected
state (TLS on a domain whose lock is closed, a forward's protocol) goes to the `title`. A mono
value set inline in a sans line stands 2px taller than the line: make it a `block`, or the hub
lands taller than the grey line that waited for it.

The line at a mark says that mark's state and no other's: a failing node at the far end of a row
is red from its mark to the next, and the working nodes nearer the hub keep their own lines.
Pointing at a node keeps its way to the hub lit (`beyondInRow`) and steps the rest back.

**Props.**

- `start`, `end`: a `WireFlowColumn` — `title` (its landmark's name and its heading), `nodes`,
  `count` beside the heading, `heading` (`narrow`: only where the columns stack; `always`: hung
  above the column wide too), `href` (the heading is the way to the page that lists the column, on a
  phone too), `max` and `more` (past `max` the rest are one "N more" tile, and a column with room
  for one more draws it rather than "1 more"), `empty`, and the loading shape (`loadingRows`,
  `loadingLines`). A picture whose data is cut before it reaches the column (the route map's
  eight domains, a stream picture's eight ports) adds the tile as a node of its own: `placeholder`,
  a `MoreTile` for its mark, and the count in its words.
- A node (`WireFlowNode`): `id`, `mark` (or a function of the size it is drawn at), `eyebrow`,
  `title`, `hint`, `name` (the node in words: the link's name and the phone's caption), `href` or
  `onPress` with `label`, `line`, `placeholder` (a dashed ring where a thing would go).
- `line` (`WireLine`): `state` — `up` (still, or carrying a pulse when `moving`), `down` (dashed,
  still), `warning` (amber), `danger` (red), `idle` (configured, not in use: dashed, grey) — plus
  `dashed`, `reverse`, `duration`, `delay`.
- `hub`: `mark`, `eyebrow`, `title`, `hint`, `loadingLines`, and `className` on its cell in the
  column form and stacked (room held open around the mark; left off in rows).
- `related(focus, id)`: what stays lit with a pointed node besides itself.
- `phone`: `stack` or `marks` (below); `loading`; `size` of the nodes' marks wide (`md`, `sm`).

**States.** `loading` draws the final shape — the hub, and rows where the nodes will be, each the
shape its column gives it — so nothing under the picture moves when the data lands. Pointing at a
node steps every other node and line back. A column with no nodes says its `empty`. The lines hold
still under reduced motion, while the tab is hidden and while the reader has paused motion
(`data-motion="paused"`, `useMotionHeld`).

**Phone.**

- `stack` (the default): the columns stack under their headings with every word kept and no lines,
  the hub one row among them at the lanes' size, for a picture that is the only place its nodes
  are named (the Network overview, the link picture).
- `marks`: `WireMarks` (below), a row of the start column's marks, the hub with its words under it, and a row of the end
  column's marks, the lines running down between them, for a picture whose nodes a list under it
  names again (Notifications, Backups, the proxy's alerts, routes and streams, Remote Desktop, the
  account). The lines to the end row
  leave from under the hub's words, so none runs through them. Until 0.9.0 the words were printed
  over the first row, which named that row (the not-found page's "?" read "This server"), and the
  account picture's own phone layout ran its lines through the account's name. A column with
  `href` keeps its heading as a link above or below its row.

**Accessibility.** Each column is a `section` named by its title, its nodes an `ol`. A node's link or
button is named by `label`, else `name`, else a string `title`; on a phone the marks carry their
`name` as screen-reader text. The lines are `aria-hidden`.

**Example.** `components/account/access-picture.tsx`:

```tsx
<WireFlow
  phone="marks"
  loading={sessions === undefined}
  start={{
    title: "Sessions",
    count: sessions?.length,
    href: "/account/sessions",
    heading: "always",
    nodes: signedIn,
    max: 4,
    more: { href: "/account/sessions", words: (hidden) => `${hidden} more sessions` },
  }}
  hub={{ mark: <AccountWireMark user={user} />, eyebrow: "You", title: name, hint: guard }}
  end={{ title: "API keys", count: usable.length, href: "/account/keys", heading: "always", nodes: keyring }}
/>
```

**Enforced by** `wire-flow-model.test.js` (line states, the form, rows and level, columns,
stagger, loading rows), `ui/beam-path.test.js` (the level line) and the
specs of the pages that draw one (`[data-slot=wire-flow]`), among them `missing-page.spec.ts`,
`account-ui.spec.ts`, `account-link.spec.ts`, `proxy-ui.spec.ts`, `proxy-streams.spec.ts` and
`remote-desktop.spec.ts` (a column is a `region` named by its title). A picture that waits as
itself is held to its place by `design-system.spec.ts` ("stands where it lands").

## WireLane

**Purpose.** One column of a hand-laid picture: its heading, how many it holds, and the list of its
nodes. Seven pictures carried this as a local `Lane`, four byte for byte; the count was the muted ink
at 70%, a fade the palette has no name for, and is the faint ink now.

**Props.** `title` (also the list's accessible name), `count`, `float` (hang the heading above the
list from `lg`, with room for it above and below, so the nodes centre on the hub and the wires run
level), `dense` (a 12px gap for a lane of small marks that outnumbers the other side), `align="end"`
(the heading against the right edge from `lg`, for the start side), `className` on the lane.
`WireLaneTitle` is the heading alone, and with `href` it is a link with an arrow; `WireFlow`'s
columns draw their headings with it.

**Example.** `components/proxy/ports-picture.tsx`:

```tsx
<WireLane title="Who can reach it" count={model.lanes.length} align="end" float>
  {model.lanes.map(({ lane }) => <li key={lane}>…</li>)}
</WireLane>
```

Its users: `docker/container-picture.tsx`, `docker/stack-map.tsx`, `procs/wiring-picture.tsx` and
`proxy/ports-picture.tsx`; a project's runtime map still carries its own copy. Until 0.9.1 the
proxy's route and stream pictures were two more, which are `WireFlow`s now.

## WireMarks

**Purpose.** The phone form of every picture: the one shape a wiring picture takes below `lg`
([`../patterns/pictures-on-a-phone.md`](../patterns/pictures-on-a-phone.md)). The picture shrinks
to its marks. Each lane is a centred row of small marks, the hub stands between the rows at its
own size with its words under it, and the lines run down from centre to centre. The names are not
drawn, because a list under the picture says them.

**Props.**

- `tiers`, top to bottom. A row is `{ title, nodes, empty?, heading?, href?, count? }`: `title` is
  its landmark's name, and `heading` (`above` or `below`) draws it only where no hub between rows
  says what the row is. A hub is `{ id, mark, eyebrow?, title?, hint? }`. A row, a hub, a row is
  things wired through one place; two rows and no hub is things wired to each other (Ports); a
  row and a chain of hubs is a path (a tunnel).
- A node (`WireMarksNode`): `id`, `mark` at the `sm` size, `name` for a screen reader, `href` or
  `onPress` (with `pressed` for a toggle) and `label`; `round` for a dashed ring or a count tile,
  `anonymous` for a mark that is not a node of its own.
- `wires`: `{ from, to }` by node id, each with what the wide line says (`still`, `dashed`, `tone`,
  `reverse`, `duration`, `delay`).
- `MoreTile({ count, size })` is the nodes a row has no room for, as a `+N` tile of the marks' own
  shape.

**Use.** `WireFlow phone="marks"` draws it for a hub-shaped picture. A hand-laid picture keeps its
wide layout and draws `WireMarks` below `lg`, choosing between the two with `useBreakpoint("lg")`,
not with `max-lg:hidden` on a second copy: the specs and a screen reader would find every node
twice. Its users: the ports and tunnel pictures and a project's databases; the route, stream and
Remote Desktop pictures drew it themselves until 0.9.1 and reach it through `WireFlow` now.

## The parts

- `WireNode`: a mark with an eyebrow, a name and a line of detail; `align` puts the words before the
  mark (`end`), after it (`start`) or under it (`center`) wide, and to its right narrow. `aside` is a
  node's readings at the far end of a wide column from `xl`.
- `WireMark`: a filled square tile. `tone` is `neutral` (a thing that is off), `brand` (this
  product), `logo` (a thing's own mark or glyph), or a signal tone; `hue` a measurement's colour
  (§10). The only circle left in a picture is `WirePlaceholder`'s dashed ring, a thing not added yet.
- `WireHost`: this server, the rack on the brand's tint in the brand's ring; its outer edge is a
  `WireMark`'s of the same size, so a line meets it where it would meet any mark.
- `WireLink` is a mark that is a link when there is somewhere to go; `WireLabel` a caption along a
  line.
- `MonoValue({ value })`: a path, an address or a unit's name that wraps only at its separators
  (`monoBreaks`), never inside a word, and copies whole. It is not specific to pictures: use it
  wherever a mono value must wrap, in place of `break-all`.
- `AnimatedBeam`: the line between two marks, measured from their positions and redrawn when the
  container resizes (or `redrawOn` changes). `shape="s"` leaves and arrives level, as a node
  editor's wires do, and with `acrossRef` bends only across that element's width (`levelPath`); the
  arc is for a path down a phone. A still line is `--wire`, the strong border's grey on the page's
  ground, so lines that lie on each other do not add up. `still`, `dashed`, `tone` and `once` say what the line carries. The pulse
  is a CSS animation, still under reduced motion and paused motion.

## What enforces it

Lint has no rule for pictures yet. A new hub-shaped picture drawn by hand is a review finding
against [`../foundations/surfaces.md`](../foundations/surfaces.md#2-nothing-lifts), and the page
specs read `[data-slot=wire-flow]`.

## Related

[`../patterns/picture-first.md`](../patterns/picture-first.md),
[`../patterns/pictures-on-a-phone.md`](../patterns/pictures-on-a-phone.md),
[`../foundations/motion.md`](../foundations/motion.md) (the beam is one of motion's four meanings),
[Readings](readings.md) (a node's detail is a reading in words), [Status and tags](status-tag.md).
