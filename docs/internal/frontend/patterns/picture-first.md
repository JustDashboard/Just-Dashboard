# A picture first

The first block under the identity line is a picture of the thing (D2).

A page that reports on something connected (a proxy, a network, a backup job, an account) opens on
what is wired to what, drawn as the products they are. A page about one thing that has a face (a
deployed site) opens on the thing itself. A row of figures or a table is never the first block.

## What draws it

| The picture is | Use |
| --- | --- |
| Things on one side, one hub, things on the other, a line from each | `WireFlow` (`components/wire/wire-flow.tsx`), from data |
| Tiers, a chain, many to many, faces on a node | laid by hand on `WireNode`, `WireMark` and `AnimatedBeam`, each column a `WireLane` |
| A settings section with a shape (a source, a schedule, a route) | `SettingPicture`, two or three marks and the caller's lines |
| The thing itself | its own preview (a project's site preview), in the picture's place |

The parts and their props are in [`../components/wiring.md`](../components/wiring.md).

## The rules

1. **It says something a list cannot.** A node is a mark, a name and one line of detail. A line
   carries a state: still, moving while it carries traffic, dashed when it is down or not in use,
   amber or red when it needs a look. A picture that only repeats the list under it is cut.
2. **Real marks.** A node is the product's own logo (`ProductLogo`, `WireMark tone="logo"`), this
   server is `WireHost`, and a thing not added yet is a dashed ring (`WirePlaceholder`) that a
   press adds. See [`drawn-as-itself.md`](drawn-as-itself.md).
3. **It waits at its loaded shape.** `loading` draws the hub and rows where the nodes will be, so
   nothing under the picture moves when the data lands ([`page-holds-still.md`](page-holds-still.md)).
4. **A node leads somewhere.** A node with a page is a link to it; a node that narrows the list
   under the picture is a toggle (`aria-pressed`). Pointing at a node steps the rest back.
5. **It holds still when asked.** The lines stop under reduced motion, under Pause animations and
   while the tab is hidden (`useMotionHeld`).
6. **On a phone it shrinks to its marks**, or keeps its words and drops its lines when nothing else
   on the page names its nodes. One rule, in [`pictures-on-a-phone.md`](pictures-on-a-phone.md).
7. **Not on a workbench, and not twice.** A workbench opens on its work. A page with views draws
   the picture once: over the views when it describes the whole thing, inside the first view when
   it describes only that view (an image, a volume, an egress group's Members).

## As wide as the page, as short as it can be

A hub picture fills its page's width before it grows down. `WireFlow` lays each side as rows of
nodes on level lines into the hub wherever a side has room for three across, and keeps one column
a side only where it has not ([`../components/wiring.md`](../components/wiring.md#wireflow)). A
picture that stands tall in the middle of a wide page with nothing on either side of it is a
finding: the operator's words for it were "too big, the hierarchy is not good, too much white space
on the sides while it is super tall" (20 Nov 2026), with the Settings page's request path, one line
across the page, as the picture that was right.

## Accessibility

Each column is a `section` named by its title and its nodes an ordered list. A node's link or button
is named by the node in words. The lines are `aria-hidden`: what they say is in each node's state.

## Held by

`wire-flow-model.test.js` (line states, columns, the loading rows) and the page specs that read
`[data-slot=wire-flow]`. Whether a page should have a picture is a review question.

## Related

[`../components/wiring.md`](../components/wiring.md),
[`pictures-on-a-phone.md`](pictures-on-a-phone.md), [`identity-line.md`](identity-line.md),
[`../recipes/reading-page.md`](../recipes/reading-page.md).
