# Wiring pictures

A picture of what is connected to what, drawn first (D2).

**Members:** `WireFlow` (`wire/wire-flow.tsx`, its arithmetic in `wire/wire-flow-model.ts`),
`WireLane`, `WireLaneTitle` (`wire/wire-lane.tsx`), `WireNode`, `WireMark`, `WireHost`,
`WirePlaceholder`, `WireLink`, `WireLabel` (`wire/wire.tsx`), `useMotionHeld`
(`wire/use-motion-held.ts`), `AnimatedBeam` (`ui/animated-beam.tsx`), and `SettingPicture`
(`settings/setting-picture.tsx`), a settings section's row of two or three marks.

## Which one

| The picture is | Use |
| --- | --- |
| Things on the left, one hub, things on the right, a line from each to the hub | `WireFlow` |
| One node a side, in a settings section, the lines the caller's | `SettingPicture` |
| Tiers, a chain, two lanes a side, many to many, faces on a node | laid by hand on `WireNode`, `WireMark` and `AnimatedBeam`, each column a `WireLane` |

A picture that is the hub shape uses `WireFlow` and lays its nodes by hand only when it is not that
shape. In 0.9.0 the account's access picture and the link picture (Settings → Connect, Settings →
Hub) moved onto it; the others the 0.9.0 pass looked at stay laid by hand on purpose: a container's
picture (ways in, the container, what it keeps, with its faces), the procs wiring picture, the stack
map (many to many), a project's runtime lanes, the routing decision's two columns, the ports
picture (networks to programs). Porting those would either lose what they show or grow `WireFlow`
into a layout engine.

## WireFlow

**Purpose.** The picture several sections open on, from data: what reaches a thing and what it
reaches, with each line saying its state.

**Anatomy.** Wide, five columns: the start column, a gap the lines bend across, the hub, the gap,
the end column (`hubShare` sets the hub's width against one for each side). A node is a mark, an
eyebrow, a name and a line of detail; the start column's words stand before their marks and the
end's after, so every line leaves a mark over nothing. The hub's words hang under its mark out of
the flow, and the picture holds the room they need under itself (`useHubRoom`).

**Props.**

- `start`, `end`: a `WireFlowColumn` — `title` (its landmark's name and its heading), `nodes`,
  `count` beside the heading, `heading` (`narrow`: only where the columns stack; `always`: hung
  above the column wide too), `href` (the heading is the way to the page that lists the column, on a
  phone too), `max` and `more` (past `max` the rest are one "N more" tile, and a column with room
  for one more draws it rather than "1 more"), `empty`, and the loading shape (`loadingRows`,
  `loadingLines`).
- A node (`WireFlowNode`): `id`, `mark` (or a function of the size it is drawn at), `eyebrow`,
  `title`, `hint`, `name` (the node in words: the link's name and the phone's caption), `href` or
  `onPress` with `label`, `line`, `placeholder` (a dashed ring where a thing would go).
- `line` (`WireLine`): `state` — `up` (still, or carrying a pulse when `moving`), `down` (dashed,
  still), `warning` (amber), `danger` (red), `idle` (configured, not in use: dashed, grey) — plus
  `dashed`, `reverse`, `duration`, `delay`.
- `hub`: `mark`, `eyebrow`, `title`, `hint`, `loadingLines`.
- `related(focus, id)`: what stays lit with a pointed node besides itself.
- `phone`: `stack` or `marks` (below); `loading`; `size` of the nodes' marks wide (`md`, `sm`).

**States.** `loading` draws the final shape — the hub, and rows where the nodes will be, each the
shape its column gives it — so nothing under the picture moves when the data lands. Pointing at a
node steps every other node and line back. A column with no nodes says its `empty`. The lines hold
still under reduced motion, while the tab is hidden and while the reader has paused motion
(`data-motion="paused"`, `useMotionHeld`).

**Phone.**

- `stack` (the default): the columns stack under their headings with every word kept and no lines,
  for a picture that is the only place its nodes are named (the Network overview, the link
  picture).
- `marks`: a row of the start column's marks, the hub with its words beside it, and a row of the end
  column's marks, the lines running down between them, for a picture whose nodes a list under it
  names again (Notifications, Backups, the proxy's alerts, the account). The hub's words stand to
  the right of its mark, as a narrow `WireNode`'s do, clear of the lines running down through it.
  Until 0.9.0 they were printed over the first row, which named that row (the not-found page's "?"
  read "This server"), and the account picture's own phone layout ran its lines through the
  account's name. A column with `href` keeps its heading as a link above or below its row.

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

**Enforced by** `wire-flow-model.test.js` (line states, columns, stagger, loading rows) and the
specs of the pages that draw one (`[data-slot=wire-flow]`), among them `missing-page.spec.ts`,
`account-ui.spec.ts` and `account-link.spec.ts`.

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

**Example.** `components/proxy/route-picture.tsx`:

```tsx
<WireLane title="Domains" count={map.total} align="end" float>
  {map.domains.map((domain) => <li key={domain.id}>…</li>)}
</WireLane>
```

Its users: `docker/container-picture.tsx`, `procs/wiring-picture.tsx`, `proxy/ports-picture.tsx`,
`proxy/route-picture.tsx`; the stack map, a project's runtime map and the stream picture still carry
their own copy until their page passes (WP-28, WP-18, WP-26).

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
- `AnimatedBeam`: the line between two marks, measured from their positions and redrawn when the
  container resizes. `shape="s"` leaves and arrives level, as a node editor's wires do; the arc is
  for a path down a phone. `still`, `dashed`, `tone` and `once` say what the line carries. The pulse
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
