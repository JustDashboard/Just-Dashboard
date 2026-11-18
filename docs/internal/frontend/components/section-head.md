# Section head

A section is a title and a hairline (D6). This page is the head: the title, the hairline, and what
sits at the end of its line (a count, the section's verbs, the way on to its page).

**Members:** `PanelHeader` (`components/panel.tsx`), `Section` (`components/section.tsx`),
`SectionLink` (`components/section-link.tsx`). `FormSection` (`components/form.tsx`) draws the same
head; its form rules are in [`forms.md`](forms.md). `PaneSection`, the head of a group inside an
inspector pane, is in [`surfaces.md`](surfaces.md#panesection).

## One head

There is one section head: `PanelHeader` in a plain panel. The title is `text-title`, 15/22 at
600, `tracking-tight`, in the foreground, with a hairline 12px under it (`pb-3`) and 12 to 16px
more before the body.

Before 0.9.0 there were two. `Section` was 16px semibold with no rule, and `Panel plain` with
`PanelHeader` was 15px medium over a hairline. The host Overview put them side by side, so half its
heads were ruled and half were not. A service's page and a container's page, one click apart, were
headed two ways (HST-01). Now `Section` and `FormSection` render `PanelHeader plain`, and the weight
is 600 everywhere a surface names itself (FND-03).

```
Resources                                    4   [Refresh]   All metrics →
─────────────────────────────────────────────────────────────────────────
rows…
```

From left to right: the title (with `Advanced` after it when set), then any `children` (a
`ChipStrip` that belongs on the head's line), then the end of the line. The end holds, in this
order, the count, the actions, then the link. The end never grows wider than the head, and on a
narrow screen it wraps under the title rather than pushing the page sideways.

## Which one

| The block is | Use | Not |
| --- | --- | --- |
| one block of content on a page: a list, a reading, a table | `Panel plain` + `PanelHeader` | a `Section` around one panel |
| a group of several panels or blocks that belong together (account, a certificate's facts, a unit's views) | `Section` | a `Panel` holding panels |
| a part of a dialog's or sheet's form | `FormSection` | a bold `<p>` over the fields |
| a part of a page that is a form (settings) | `FormSection aside` in `FormSections`. The rule between sections is the one `FormSections` draws, so this head draws none under itself | `Section` |
| a titled group inside an inspector pane | `PaneSection` | `PanelHeader` (too loud for a column of facts) |
| the strip of a workspace pane | `PaneHeader` | `PanelHeader` |

A `Section` over panels that have their own heads gives those panels `level={3}`, so the outline
keeps its order. Metrics is one such page. Each resource is a `Section`, and its panels sit
under it.

## PanelHeader

| Prop | What it does |
| --- | --- |
| `title` | The section's name. Truncates on one line. |
| `level` | `2` (default) or `3`. Only the element changes: an h3 looks exactly like an h2. |
| `count` | How many there are: a number or a short node. It is drawn tabular at `text-hint` in the muted ink, at the end before the actions. It sits outside the heading, so the heading's accessible name is the name alone. `0` is drawn; `undefined`, `null` and `false` are not. |
| `link` | `{ href, label }`, the way on to the page the block stands for, drawn with `SectionLink`, last on the line. |
| `actions` | The section's verbs. One verb is a small outline button (D6); a brand button belongs to the page header, not to a section. |
| `eyebrow` | A caps label above the title. Rare: a section's name is its title. |
| `advanced` | Marks a panel that assumes the reader administers the thing directly (`caps`, after the title). |
| `plain` | For a head drawn outside a `Panel` (`Section`, `FormSection`). Inside a panel, the panel decides. |
| `children` | Content on the head's own line between the title and the end: chips, a segment. |
| `hint` | Deprecated. `FormSection`'s old line under the title, drawn in the title's column so it holds still when the actions land. WP-39 deletes it with `FormSection hint`. |

A head carries no description (§5, `standard.md`) and no icon (§14).

```tsx
<Panel plain>
  <PanelHeader
    title="Containers"
    count={containers.length}
    link={{ href: "/docker/containers", label: "All containers" }}
  />
  <PanelBody flush>…</PanelBody>
</Panel>
```

## Section

`Section({ title, count?, link?, actions?, level?, className?, children })` is a `<section>`
holding `PanelHeader plain` and its children 16px under the hairline. It is not a panel. It carries
no `data-slot="panel"`, so a spec that finds a panel by its heading finds the panel and not the
group around it.

## SectionLink

The way on: "All metrics →", "Audit log →". The label is `text-hint` medium in the muted ink, and
the foreground on hover, with `ArrowRight` at `size-3`, `whitespace-nowrap` and the focus ring
(`rounded-md`). The line is 17px, too small to press, so it takes `hit-area`. That gives a 24px
target under a mouse and 44px under a finger, without growing the head.

Use `PanelHeader`'s `link` where the link ends a head. Use `SectionLink` alone only where the head
is not a `PanelHeader` (a picture's lane, a band). Never type the arrow link by hand. About thirty
hand copies, some without the focus ring, are why this exists; the databases' and the proxy's own
head links were two of them and are gone.

It is `TextLink`'s onward face (`components/inline-link.tsx`). A link inside a sentence is
`TextLink`'s inline face, underlined at rest; never `hover:underline` alone.

## States

- **Loading.** The head is drawn with the page, because the title is known before the data. A
  count that is not known yet is left out rather than drawn as a bone.
- **Empty.** The head stays and the body says so ([`states.md`](states.md)). A count of `0` is drawn.
- **Hover and focus.** Only the link and the actions take them.

## The phone

The head wraps: the end of the line goes under the title when both do not fit, and the actions
wrap inside it. The title truncates rather than wrapping. The link's press target is 44px under a
finger.

## Accessibility

- `level` keeps the heading order where a head nests under another head (A11Y-12). The default is
  2, the level under the page's `h1`.
- The count is outside the heading, so "Routes" is announced as "Routes", not "Routes 11".
- `SectionLink`'s name is its label. The arrow is decorative.

## What enforces it

- `src/components/panel.test.js` holds the contract: same classes at both levels, the count's
  place and look, the link, and `Section` and `FormSection` on the one head.
- `tests/browser/metrics-ui.spec.ts` reads the Metrics sections' heads as
  `[data-slot=page] > section > [data-slot=panel-header] h2`.
- Planned for WP-39: lint rules against a hand-typed `ArrowRight size-3` link outside
  `section-link.tsx`, and against a hand-typed `border-b border-hairline pb-2 text-title` head.

## Related

[`surfaces.md`](surfaces.md) for the panel the head sits in, [`forms.md`](forms.md) for
`FormSection`, [`page-header.md`](page-header.md) for the page's own head,
[`../patterns/sections.md`](../patterns/sections.md), and [`../foundations/type.md`](../foundations/type.md)
for the ladder.
