# Tokens

Every token lives in `frontend/src/app/globals.css`, on `:root` or in an `@theme` block. That is the
only place a colour, a shadow or a scale step is named. A call site writes the utility, never the
value. WP-40 replaces this page with one generated from the stylesheet and a test that it is
current; until then it lists the families.

## Colour

| Family | Tokens | Utility | For |
| --- | --- | --- | --- |
| Grounds | `--background`, `--card`, `--popover`, `--control`, `--surface-header`, `--surface-sunken`, `--muted`, `--secondary` | `bg-background`, `bg-card`, `bg-control`, `bg-surface-header`, `bg-surface-sunken` | the ladder a surface steps up ([`surfaces.md`](surfaces.md)) |
| Inks | `--foreground`, `--muted-foreground`, `--faint` | `text-foreground`, `text-muted-foreground`, `text-faint` | the three inks; faint keeps 4.5:1 |
| Edges | `--border`, `--input`, `--hairline`, `--border-strong`, `--control-edge` | `border-border`, `border-input`, `border-hairline`, `border-border-strong`, `border-control-edge` | a frame, a field, where a header meets a body, a destination's hover, a control's 3:1 edge (T3) |
| Brand | `--brand`, `--brand-hover`, `--brand-active`, `--brand-foreground`, `--signal`, `--ring` | `bg-brand`, `text-brand`, `text-signal` | a command's face, where you are, a location mark, the focus ring |
| Neutral command | `--primary`, `--primary-foreground` | `bg-primary` | white as a fill, not the command face |
| Status | `--success`, `--warning`, `--destructive` | `text-success`, `text-warning`, `text-destructive` | a reading's state, never decoration |
| Danger face | `--destructive-face`, `--destructive-active`, `--destructive-foreground` | `bg-destructive-face`, `bg-destructive-active`, `text-destructive-foreground` | the danger button at rest and pressed, and its label ([`colour.md`](colour.md)) |
| Tints | `--plot-*`, `--wash-*`, `--rule-*`, `--mark`, `--meter-track` | `bg-plot-*`, `bg-wash-*`, `border-rule-*` | an icon plot, a tinted banner, a tinted rule, a match mark, a meter's track |
| Interaction | `--row-hover`, `--menu-hover`, `--control-hover`, `--control-active`, `--accent` | `bg-row-hover`, `bg-menu-hover`, `bg-accent` | hover's three answers, press feedback, selection |
| Flow | `--flow-surface`, `--flow-lit`, `--flow-lit-soft`, `--choice-surface` | | register B's focused surface and the lit edge |
| Charts | `--chart-1` to `--chart-5` | `text-chart-*`, `fill-chart-*` | a series' hue; `--chart-5` is the RX colour of every in/out pair, near hue 200 (T9) |
| Categorical | `--tag-*` (slate, red, amber, green, cyan, blue, violet, pink) | `text-tag-*` | a kind: a Redis key type, a BSON type, a folder label, a lane |
| Git | `--git-*`, `--pull-*` | `text-git-*`, `text-pull-*` | a pending change, a pull request's state; aliased onto the tag ramp |
| Languages, folders, documents | `--language-*`, `--folder-*`, `--doc-*` | | a checkout's language, a folder's colour, a file preview |
| Sidebar | `--sidebar-*` | `bg-sidebar` | the rail |

## Scales

| Scale | Tokens | Page |
| --- | --- | --- |
| Type | `--text-micro`, `--text-hint`, `--text-body`, `--text-title`, each with `--line-height`; Tailwind's `xs`, `sm`, `base`, `2xl` | [`type.md`](type.md) |
| Tracking | `--tracking-normal`, `--tracking-code` | [`type.md`](type.md) |
| Radius | `--radius` (8px), `--radius-xs`, `--radius-tile-md`, `--radius-tile-lg` | [`surfaces.md`](surfaces.md) |
| Shadow | `--shadow-xs`, `--shadow-sm`, `--shadow-md`, `--shadow-lg`, `--shadow-xl`; no control carries one | [`surfaces.md`](surfaces.md) |
| Motion | `--ease-arrive`, `--ease-standard`, `--duration-fast`, `--duration-base`, `--duration-slow`, `--duration-fill` | [`motion.md`](motion.md) |
| Control sizes | `--control-dense`, `--control-compact`, `--control-regular`, `--control-large` | [`layout-phone.md`](layout-phone.md) |
| Layers | `z-raised`, `z-sticky`, `z-pane`, `z-float`, `z-immersive`, `z-overlay`, `z-tooltip` | [`layout-phone.md`](layout-phone.md) |
| Layout | `--width-rail`, `--width-inspector`, `--container-stack` | [`layout-phone.md`](layout-phone.md) |

## Custom classes

`.eyebrow`, `.caps`, `.numeric`, `focus-ring`, `focus-ring-inset`, `focus-ring-within`,
`hit-area`, `transition-fill`, `glyph-inline`, `code-inline`, `grid-cols-main-end`,
`grid-cols-start-main`, `grid-cols-start-main-end`, `grid-rows-fill`, `h-workbench`, `max-h-rail`,
and the animations `animate-breathe`, `animate-rise`, `animate-sweep`, `animate-drill`. `cn` in
`lib/utils.ts` is told about each one, or tailwind-merge drops it ([`type.md`](type.md)). The
safe-area utilities `pt-safe`, `pb-safe`, `pl-safe`, `pr-safe`, `px-safe` and `mb-safe` pad by the
device's insets ([`layout-phone.md`](layout-phone.md)).

## Removed in 0.9.0

Nothing read them: `--warning-foreground`, `--signal-foreground`, `--sidebar-primary`,
`--sidebar-primary-foreground`, `--sidebar-ring`, `--plot-signal`, `--plot-primary-hover`,
`--shadow-color`, `--shadow-2xs`, `--shadow` and `--shadow-2xl`, and the phantom `mark-*` names
`cn` had been told about. `--destructive-foreground` stays: the danger face's label reads it.
