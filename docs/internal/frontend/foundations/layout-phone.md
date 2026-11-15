# Layout and the phone

## Breakpoints

Viewport breakpoints are for the page's frame only: the shell, the rail, the bottom bar and whether
a pane is shown.

| Name | Width | Means |
| --- | --- | --- |
| `sm` | 640 | a field row stops stacking; a toolbar fits one line |
| `md` | 768 | the sidebar appears; the page gutter is 32; `useIsMobile` turns false |
| `lg` | 1024 | a workbench shows its side column beside the work |
| `xl` | 1280 | a detail page shows its sibling rail (D9) |
| `2xl` | 1536 | rarely: a second column of readings |

No other viewport widths. In code, `lib/breakpoints.ts` (WP-01) exports `BREAKPOINTS` and
`useBreakpoint("lg")`, so nothing writes `useMediaQuery("(min-width: …px)")` by hand.

## Container queries

A block that can render at more than one width at one viewport width (a detail column beside a
rail, a band's half, a sheet, a card grid) answers to its container, not to the viewport. So does
any rule that would otherwise have to subtract a rail's width.

| Container | Declared on | Sizes |
| --- | --- | --- |
| `@container/page` | `Page` | `@3xl` 48rem, `@5xl` 64rem, `@6xl` 72rem |
| `@container/detail` | the detail column beside a rail | the same scale |
| `@container` | a row, a band's half, a form card | `@xl` 36rem, `@2xl` 42rem, and `--container-stack` 38rem for "this row stacks" |

## Layers

Named utilities, never a raw `z-` number.

| Layer | Utility | Value | For |
| --- | --- | --- | --- |
| raised | `z-raised` | 10 | a sticky cell, a chip over a chart, a pinned column |
| sticky | `z-sticky` | 20 | the top bar, sticky table and grid heads, resize handles |
| pane overlay | `z-pane` | 30 | a workbench's side column laid over the work below `lg`, a drop target |
| float | `z-float` | 40 | the floating save bar, the assistant dock, held sessions: one bottom stack |
| immersive | `z-immersive` | 45 | the full-screen map, ER diagram, terminal and remote screen |
| overlay | `z-overlay` | 50 | dialog, sheet, popover, menu, hover card, lightbox |
| tooltip | `z-tooltip` | 55 | a tooltip, above the dialog it may sit in |

## Control sizes

One height per density. A touch screen (`pointer: coarse`) gets the taller box from the token, so a
call site never writes `h-11 sm:h-9`.

| Density | Fine | Touch | Button | Field | Icon | Label |
| --- | --- | --- | --- | --- | --- | --- |
| dense | 24 | 32, with a 44 hit area | `xs`, `icon-xs` | `SearchInput dense` | `size-3` | `text-xs` |
| compact | 32 | 40, with a 44 hit area | `sm`, `icon-sm`, `IconAction` | `SearchInput`, `Select size="sm"` | `size-3.5` | `text-body` |
| regular | 36 | 44 | `default`, `icon` | `Input`, `Select`, `Textarea` | `size-4` | `text-body` |
| large | 40 | 48 | `lg`, `icon-lg` | — | `size-4` | `text-body` |

`hit-area` gives an inline text button or a link in a row a 24px press box, 44px on touch, without
growing the row.

## Named layout values

`grid-cols-main-end` (`minmax(0,1fr) auto`), `grid-cols-start-main`, `grid-cols-start-main-end`,
`grid-rows-fill`, `w-rail` (18rem), `w-inspector` (22rem), `h-workbench` (`min(75svh, 40rem)`),
`max-h-rail`, `glyph-inline` and `code-inline`. A grid template that appears once and a table's
column percentages stay arbitrary: they are layout.

## The phone

| Rule | Held by |
| --- | --- |
| No page spills sideways at 390. | page specs at 390 (`network-ui.spec.ts`: no sideways scroll on a phone); convention |
| A page of several panels is one full-height pane over a bar at the foot (`PaneBar` and `paneShown` in `components/phone-panes.tsx`). Never stacked scrolling boxes. | unit `phone-panes.test.js` (no hand-drawn foot bar) |
| An item's details are a page, not a stack of sheets. | D9 |
| Lists become cards; nothing is cut off; a table is not scrolled sideways for its main reading. | convention |
| A field keeps 16px text on a touch screen (iOS zooms on less), whatever class its caller passes. | coming: spec `touch-targets.spec.ts` (WP-02) |
| A pressable target is at least 24px, and 44px on touch. | coming: spec `touch-targets.spec.ts` (WP-02) |
| On touch, `text-hint` is 12px and `text-micro` 11px (T1). | coming: WP-01 |
| Hover reveals are for real pointers only; on touch the controls are drawn. | spec `design-system.spec.ts` (*row actions … are visible without a pointer*) |
| The page clears the home indicator and the notch (`viewportFit: cover`, safe-area utilities). | coming: WP-01 |
| Canvases (the server map, the ER diagram, the boards) pan and pinch like Excalidraw, and arrow keys pan and move on them (T10, coming in WP-44). | unit `canvas-move.test.js` |
