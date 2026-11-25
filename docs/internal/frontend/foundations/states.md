# Selected, hover and focus

## Reference

| State | Mark | Utility |
| --- | --- | --- |
| Selected | a filled ground, a property of the data | `bg-accent`; a switcher's choice (a segment, a filter chip) `bg-chosen` |
| Hover | a ground on a row, a menu or a control; a border step on a framed destination | `bg-row-hover`, `bg-menu-hover`, `hover:bg-control-hover`, `border-border-strong` |
| Focus | the one outline ring | `focus-ring`, `focus-ring-inset` (no room outside), `focus-ring-within` |

| Rule | Held by |
| --- | --- |
| The three never look alike, and a control in all three at once stays readable. | spec `design-system.spec.ts` (*hover, selection and focus are three different marks on a row*) |
| One focus ring. No hand-written `focus-visible:ring` or outline. | lint `focus-ring`; widened to `focus` and bare `outline-none` (WP-39) |
| Selection never borrows the ring's colour. | convention |
| A chosen tab is a control face drawn inside the tab, and a chosen segment or filter chip the chosen fill (`bg-chosen`) with no outline; never an underline ([`../components/tabs-chips-segments.md`](../components/tabs-chips-segments.md)). | unit `components/tabs.test.js` |
| On the rail the current row is the selection fill, with no bar on its edge, and hover is the menu's fill. | spec `shell-phone.spec.ts` (*the current page is said, and the pointer's fill is not the selection's*) |
| A row's actions appear through `RowActions`, `IconAction reveal` or `rowReveal()`, are visible on touch and on keyboard focus, and are laid out, never overlaid. | lint `row-reveal`; spec `design-system.spec.ts` (*row actions … are visible without a pointer*) |

## 6. Three states, three mechanisms

Selection, hover and focus were once drawn the same way, so a keyboard user could not tell which
filters were applied and a mouse user tabbing away found the selection had apparently moved.

- **Selected** — a filled surface. A property of the data.
- **Hover** — a faint background. A property of the pointer.
- **Focus** — a ring drawn outside the control. A property of the keyboard.

A control can be all three at once and stay readable, which is the test.

The ring is `focus-ring`, or `focus-ring-inset` where there is no room outside the control — one
appearance, two placements. It is an `outline`, not Tailwind's `ring`: an outline follows
`border-radius`, cannot be overwritten by a `shadow-*` utility landing on the same element, and works
on a `<tr>`. Being the outline, it is undone by any class in the same list that sets the outline's style
to none: `outline-hidden` and `outline-none` win over it. The sidebar's five class lists carried
`outline-hidden` from the component library's own focus styling, so every row of the rail took the
keyboard's focus and showed nothing. `components/ui/focus-ring.test.js` fails on a class list that has
both, and `design-system.spec.ts` walks the rail's own rows.

A row's actions appear through `RowActions`, `IconAction reveal`, or `rowReveal()`. Never by hand: the
four mechanisms that sentence hides — pointer, keyboard, open menu, touch — are how five action
clusters ended up permanently unreachable on a phone.

**An action on a row is laid out, never overlaid.** The draft list positioned its discard button
`absolute right-2` against the `<li>` while the row reserved space for it with `pr-12` against a box
that `ROW_BLEED` had made twelve pixels wider on each side — so the button sat on top of the last
reading in the row. The arithmetic is not the lesson: two boxes measured against different origins
will drift again the moment either one's padding changes. A second action belongs in a slot in the
row's own flow (`ChoiceRow`'s `actions`), where there is nothing to mis-measure, and it must stop the
press from reaching the row around it — discarding a draft used to navigate to the draft it had just
deleted.

Reveal only where the controls share their space with something else. Where they have a column of
their own — a container card's actions slot — use `DimActions` instead: always drawn, and quiet until
the pointer is on the row. Quiet is the `row-dim` utility: the glyphs at 55% and the words in the faint
ink, so a resting verb still reads at 4.5:1 (the whole cluster at 55% put a red "Sign out" at 2.3:1).
A verb takes its own colour back with the row's hover, focus inside it, or an open menu; a filled
button in the cluster is not dimmed, and nothing is where there is no hover. A reserved column left empty reads as a layout bug, not as an affordance.
The Data page's rail is the shared case: a row's menu takes the place of its row count under the
pointer, in the same grid cell, and the count's slot is at least the menu's width so the menu never
covers the name. With a column of its own, every count stood a button's width short of the edge the
rail's headings end on.
