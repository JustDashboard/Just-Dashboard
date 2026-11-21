# Pictures on a phone

How a wiring picture reads at 390: one form, the picture shrunk to its marks, with the hub named.

Until 0.9.0 the same idea was drawn six ways below `lg`: a vertical chain with lines, a chain
without them, one lane with the other dropped, nothing at all, a second copy of the list under it,
and a cluster of unlabelled icons. Each hand-laid picture carried its own rule. There is one rule
now, and one component draws it.

## The rule

1. **The picture shrinks to its marks.** Each lane of the wide picture becomes a centred row of
   small (`sm`) marks. A mark keeps its state in its corner, its link or press, and its name for a
   screen reader. Its words are not drawn.
2. **The hub is named.** The thing in the middle stands between the rows at its wide size, with
   its eyebrow, name and line of detail under it. A chain of middle things (a tunnel's edge,
   connector and server) is a column of named hubs.
3. **The lines run down.** Row to hub to row, straight from centre to centre. A line leaving a hub
   leaves from under its words, so none runs through them. The lines hold still under reduced
   motion, paused motion and a hidden tab, as they do wide.
4. **A list only when the page has none.** The marks lose their words because a list on the same
   page names them: the proxy's Routes, the Hostnames, the Computers cards, a stream's rows. Where
   the picture is the only place its nodes are named, it keeps the words instead: the lanes stack
   under their headings with every word, the hub one row among them at the lanes' size, and no lines
   (`stack`). Never draw the marks and then the same names again under them.
5. **More than fit.** A row that the wide picture cut at its "N more" ends in a `+N` tile
   (`MoreTile`), which links to the list where the list has a page.

## Which form a picture takes

| Picture | Phone | Why |
| --- | --- | --- |
| Proxy overview, `RoutePicture` | marks (`WireFlow phone="marks"`): domains, the engine named, applications | Routes lists every domain and its upstream |
| Streams and a stream, `StreamPicture` | marks (`WireFlow phone="marks"`): ports, nginx named, addresses | the stream rows name each port and address |
| Ports, `PortsPicture` | marks, two rows and no hub: networks over programs, each row's heading shown | the socket list names every program and port; a network mark still narrows the list |
| Tunnel, `TunnelPicture` | marks: hostnames, then the edge, the connector and this server, each named | Hostnames lists the hostnames; the three middle things are named nowhere else |
| Remote Desktop, `ComputerPicture` | marks (`WireFlow phone="marks"`): this browser, the server named, the computers fanned under it | the Computers cards are the list |
| Notifications, Backups, the account | marks (`WireFlow phone="marks"`) | a list under each names the nodes |
| The proxy's alerts, `AlertsPicture` | marks (its default) | the kind cards and rules name the areas |
| Host alerts, `AlertsPicture phone="stack"` | stack | five of its eight sources are named nowhere else until a rule watches them |
| A database's How it is used and Right now | stack, the database at the lanes' size | its readers are named nowhere else on the page |
| Network overview, the gateway, the link picture | stack (`WireFlow`'s default) | the picture is the only list |

## How to build one

- A hub-shaped picture is a `WireFlow`; pass `phone="marks"` when a list on the page names its
  nodes. Its `stack` default is for the pictures in the table that keep their words.
- A hand-laid picture (tiers, many to many, a chain) keeps its wide layout and draws `WireMarks`
  (`components/wire/wire-marks.tsx`) below `lg`. It passes `tiers` top to bottom — a row is
  `{ title, nodes }`, a hub `{ id, mark, eyebrow, title, hint }` — and `wires` between node ids,
  each with what the wide line says (`still`, `dashed`, `tone`, `duration`). Choose between the two
  layouts with `useBreakpoint("lg")`, not with `max-lg:hidden` on a second copy: the specs and a
  screen reader would find every node twice.
- A row's heading is drawn (`heading: "above"` or `"below"`) only where the row is not said by the
  hub between rows: two rows and no hub (Ports), or a heading that links to its list.
- `WireMarks` is `WireFlow`'s `marks` form, so a change to the phone form is made once.

## Mono values in a picture and on a phone

A path, an address or a unit's name breaks only at its separators, never mid-word. `MonoValue`
(`components/wire/mono-value.tsx`) puts a `<wbr>` after each `/`, `.` and `:`; the browser breaks
after a hyphen on its own; `break-words` is the last resort for one word longer than the line.
Never `break-all`: it cut `links.batch` as `link|s.batch` and an IPv6 address mid-group. Inside a
picture's node, where a second line would push the marks off the hub's line, the value is one line,
truncated, with the whole value as its `title` (the tunnel's origin, a stream's upstream).

## Related

[`../components/wiring.md`](../components/wiring.md) (`WireFlow`, `WireLane` and the parts),
[`picture-first.md`](picture-first.md), [`../foundations/layout-phone.md`](../foundations/layout-phone.md),
[`accessibility.md`](accessibility.md).
