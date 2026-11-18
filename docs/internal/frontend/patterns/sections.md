# Sections

A section is a title and a hairline, never a box (D6).

```
Title                  4  Verb
───────────────────────────────
body
```

The title is at the left, a count or a small fact beside it, and the section's one verb as a small
outline button on the title line. The gap between two sections is the separation. A frame has to
say why it is there.

## What draws it

| Where | Use |
| --- | --- |
| A block on a page | `Panel plain` with a `PanelHeader` (`title`, `count`, `link`, `actions`) and a `PanelBody` |
| A group of blocks under one name | `Section` |
| A part of a form or a settings page | `FormSection` in `FormSections`; on a settings page `SettingSection` |
| A group inside a pane (an inspector, a side column) | `PaneSection`, an eyebrow head |
| A section that folds | `Disclosure`: a section whose head is a button |

All of them draw the same head: `text-title`, 15px, weight 600. The props are in
[`../components/section-head.md`](../components/section-head.md) and
[`../components/surfaces.md`](../components/surfaces.md).

## The rules

1. **No box around an ordinary section.** A frame is for a table (its container scrolls), a `Pane`
   (a working region), a `Well` (output you read), a `Group` (a fence inside a body), the danger
   zone (`DangerRows`) and a panel that is itself a destination (`interactive`). A panel that keeps
   its frame passes `framed` and says why in a comment. Never a panel inside a panel.
2. **No sentence under the title** (D14). What the section is right now is data on the title line
   or its first row: a count, a `Status`, a facts line. The reasoning goes behind an `Explain`.
3. **One verb on the title line**, a small outline button; a second goes behind a `⋯`. The way on
   to the page a section stands for is `PanelHeader link` ("All metrics →"), drawn by
   `SectionLink`; never an arrow link typed by hand.
4. **The heading level follows the page.** A section's head is an `h2` under the page's one `h1`.
   A section inside a section, or inside a dialog, passes `level={3}`. A `PaneSection` is an `h3`
   by default. No level is skipped ([`accessibility.md`](accessibility.md)).
5. **A section holds still.** Its head stands at its loaded height while its body waits, and a
   status that arrives sits at the head's end, where it pushes nothing down.
6. **An empty section says so in its body** (`EmptyNote`, or `EmptyState` when the section is the
   page's subject) with the verb that fills it. The head stays.

## Held by

`design-system.spec.ts` (*a settings page's sections are the level under its title*),
`src/components/panel.test.js` (`framed` and `plain`, `PaneSection`'s anatomy). The rest is review.

## Related

[`../components/section-head.md`](../components/section-head.md),
[`../components/surfaces.md`](../components/surfaces.md),
[`../foundations/surfaces.md`](../foundations/surfaces.md#2-nothing-lifts),
[`settings-save-and-pending.md`](settings-save-and-pending.md).
