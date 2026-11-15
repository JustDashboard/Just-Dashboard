# Tabs, chips and segments

Views of one page, filters over a list, a closed set of short words, a chart's window, and the
phone's bar between panes. Five controls that look alike in a screenshot and do different jobs.

**Members:** `ViewTabs`, `ViewTabList`, `useViewTab` (`components/tabs.tsx`,
`hooks/use-view-tab.ts`); `FilterChip`, `ChipStrip`, `ChipCount` (`components/chips.tsx`);
`Segments` (`components/segments.tsx`); `WindowPicker` (`components/window-picker.tsx`); `PaneBar`
(`components/phone-panes.tsx`, whose frame is in [`workbench.md`](workbench.md)). `tabClasses` is
deprecated (below).

## Which one

| The reader is choosing | Use | Role a screen reader hears | Not |
| --- | --- | --- | --- |
| which view of this page is on screen (Overview, Journal, Settings) | `ViewTabs` | tab list, tabs, a panel each | a route change: that is a link |
| which subset of the rows is shown, often several at once, with counts | `FilterChip` in a `ChipStrip` | toggle buttons, pressed | a view of the page |
| one answer of two to five short words (a Python release, a method) | `Segments` | radio group | a `Select` for a closed set; two switches for one row's binaries |
| how far back a chart reaches (1h, 6h, 24h, 7d) | `WindowPicker` | radio group | a popover form with dates: that is `TimeWindow` |
| which pane fills a phone's screen | `PaneBar` | navigation of buttons, one current | a view strip; a tab list |

Editor tabs that close and rename (the query editor's `TabStrip`) and the pill switcher inside a form
(`ui/tabs` `TabsList`, in the rule and site forms) are their own controls and stay apart.

## ViewTabs

The underlined strip of a page's views, on Radix Tabs: the arrow keys move along it, Home and End
reach its ends, the focus roves, and each tab names the panel it shows.

**Anatomy.** A strip with a bottom hairline. Each tab is the label at the body size (13px), medium,
the muted ink at rest and the foreground chosen, with a 2px underline in the brand blue under the
chosen one. A count sits after the label (`ChipCount`) and a dot marks a view that wants a look. A
`trailing` slot at the right end holds a search or a verb, outside the tab list.

```tsx
const VIEWS = [
  { key: "overview", label: "Overview" },
  { key: "journal", label: "Journal", count: errors },
  { key: "settings", label: "Settings", dot: unsaved ? { tone: "warning", label: "not saved" } : undefined },
] satisfies TabView<View>[]

const [view, setView] = useViewTab(VIEWS)

<ViewTabs label={`Views of ${unit.name}`} views={VIEWS} value={view} onValueChange={setView}
  rootClassName="min-h-0 flex-1">
  <TabsContent value="overview">…</TabsContent>
  <TabsContent value="journal">…</TabsContent>
  <TabsContent value="settings">…</TabsContent>
</ViewTabs>
```

| Prop | What it does |
| --- | --- |
| `label` | The tab list's name, required: "Views of nginx.service". Keep the `Views of <name>` form. |
| `views` | `{ key, label, count?, dot?, disabled? }[]`. A count of 0 is not drawn. `dot` is `{ tone, label }`; the label is read after the tab's name. |
| `value` | The view on screen. |
| `onValueChange` | Given, `ViewTabs` holds the `Tabs` and its `children` are the panels (`TabsContent` from `ui/tabs`). Left out, it is the strip alone and stands inside the caller's `Tabs` (`ViewTabList` is that strip by name). |
| `size` | `md`, 40px, a page's strip (the default); `sm`, 36px, a pane's or a workbench's. |
| `mono` | The labels are names a machine wrote: units, keys, files. |
| `sticky` | Holds the strip at the top of its scroller, on the panel's ground. |
| `trailing` | Controls at the right end, outside the tab list. |
| `hideWhenSingle` | Draws nothing with one view: one tab is a title, not a choice. |
| `className`, `rootClassName` | The strip's box; the `Tabs` it holds. |

**States.** Chosen: foreground ink and the blue underline, a count in the label's ink. At rest: the
muted ink, a count in `--faint`. Hover: a grey underline and the foreground. Focus: the ring, inset,
because the strip scrolls and a scroller clips a ring drawn outside it. Disabled: `--faint`, no hover.

**The phone.** The strip never wraps. When the views run past the edge it scrolls sideways with no
scrollbar, and the edge they run under is shaded (a mask on the list, 32px), so the cut tab reads as
more rather than as a word cut short. The chosen tab is scrolled into view, clear of the shade, when
the strip mounts and whenever the view changes, so a `?tab=` link to the fifth view opens with it on
screen. The strip scrolls sideways only: a strip below the fold does not move the page.

**The address.** `useViewTab(views, { param, remember })` returns `[view, setView]`:

- `param` is `tab` (the default) for an item's pages and `view` for a workbench's views.
- The first view is the bare address; choosing it takes the parameter out.
- A view is replaced in the history, never pushed, so Back leaves the page.
- A value the page does not have opens the first view.
- An address that changes under the page is followed.
- `remember: "<page>.<thing>"` reopens a bare address on the view last chosen on this screen
  (`useViewState`), and a link that names a view is remembered in its turn.

`pickView` and `viewSearch`, the hook's two rules as pure functions, are held by
`hooks/use-view-tab.test.js`.

**Not this.** A strip whose tabs change the route is links in a `nav` with `aria-current="page"`;
since 0.6.7 the rail lists a section's pages and the product has no route-level strip. A switch
between two modes of a control (a command palette's scope, a log's Live and History inside one pane)
that shows no panel is `Segments`.

**`tabClasses` is deprecated.** It is the look without the roles, and the 75 strips built on it gave
the same mark four keyboard models (A11Y-02). New code uses `ViewTabs`; the wave-2 passes move the
callers and WP-39 bans `TabsPrimitive.List` and `tabClasses` outside `ui/tabs` and `ViewTabs`. The
editor's `TabStrip` keeps the look for its own control.

## FilterChip, ChipStrip and ChipCount

A chip narrows a list to a subset. It is a toggle button (`aria-pressed`), so several can be on.

**Anatomy.** 28px, squared (`rounded-md`), the hint size (11px, 12 under a finger), an edge at rest
(`--input`) and, chosen, the accent ground under `--border-strong`. It presses as 44px under a finger
(`hit-area`) without growing the row. Pill shapes are reserved for nothing.

`ChipCount` is the count after a chip's or a tab's label: tabular, the micro size, in `--faint`
(4.5:1 on every ground a chip sits on) and in the label's own ink inside a chosen chip, tab or
segment, which set `--count-ink`. It used to be the label's ink at 80% opacity, which the one-mode
rule forbids. A chip that is a legend passes its colour as a class (`text-warning`), so "5xx 13" says
which thirteen. The fleet's rule is the one to copy: a chip whose count is 0 is not drawn, and a
non-zero failed count wears the danger ink.

`ChipStrip` is the run the chips sit in. On a phone it scrolls sideways and bleeds to the page's
gutter (`--page-inset`, 20px until the page names it), so the chip cut at the edge is cut by the
screen; from `sm` it wraps. The bleed is sideways only. It used to step out above and below as room
for the focus ring, and inside a `space-y` stack that negative margin replaced the gap, so the
certificate sources sat flush on the first certificate (PGP-06). On a phone the ring is drawn inside
the chip instead.

| Prop | What it does |
| --- | --- |
| `bleed` | `true` (the default) reaches the page's gutter on a phone; `false` keeps the strip in its box, for a strip inside a form, a dialog, a pane or a rail. |
| `role="group"` and `aria-label` | Name the run when the chips are a question of their own ("Which extensions are listed"). |

```tsx
<ChipStrip role="group" aria-label="Source">
  {SOURCES.map((source) =>
    counts[source.key] > 0 ? (
      <FilterChip key={source.key} selected={shown.has(source.key)} onClick={() => toggle(source.key)}>
        {source.label} <ChipCount>{counts[source.key]}</ChipCount>
      </FilterChip>
    ) : null,
  )}
</ChipStrip>
```

**Not this.** One answer of a few words is `Segments`. A filter is chips first, then the search, in a
`Toolbar`, in lists and rails alike (NET-13).

## Segments

A closed choice of two to five short words as one segmented control, the only single-choice
`ToggleGroup`. A second press on the chosen segment does not clear it: there is always an answer.

| Prop | What it does |
| --- | --- |
| `label` | The group's name. Inside a `Field` it may be left out: the field's visible label names the group (`aria-labelledby`), because a `<label for>` cannot name a group. One or the other is required. |
| `value` | The choice; a value that is none of the options (a dragged chart span) shows none chosen. |
| `options` | `{ value, label, icon?, mono?, disabled? }[]`. A disabled option is shown and refused. |
| `onChange` | Hears a new choice only. |
| `onReselect` | Hears the chosen segment pressed again, for one that opens something (a typed range). |
| `size` | `inline`, 24px (32 under a finger, pressing as 44), in a toolbar or a row; `sm`, 32/40, beside a section title or over a chart (the default); `field`, 36/44, on a form's line beside an input. |
| `fill` | Shares the whole width between the segments. |

Inside a `Field` it is also described by the field's hint and error. Labels are `text-xs`; the
segments not chosen step back to the muted ink, because `bg-accent` alone is a step of ground the
reader has to compare rather than a mark.

```tsx
<Field label="Python version" hint="The release the build image runs.">
  <Segments value={version} options={VERSIONS} onChange={setVersion} size="field" />
</Field>
```

**Not this.** A group that may be emptied on purpose is a raw `ToggleGroup` with a lint exemption.
A multi-select is chips. More than five or long words is a `Select`.

## WindowPicker

How far back a chart reaches, as one `Segments` at `sm`, named "How far back the charts reach"
unless a page with one chart says "How far back the trend reaches".

```tsx
<WindowPicker windows={WINDOWS} value={zoomed ? "" : window} onChange={setWindow} />
```

What a page adds around it stays the page's: Metrics' pan and zoom-out once a span is dragged
(`value=""` while it is), the traffic page's typed range (`onReselect` reopens its form). The log
console's `TimeWindow` is a popover form, not this.

## PaneBar

The bar at the foot of a phone's frame: one 56px stop per pane, the glyph over its word at the hint
size, a count on the glyph's shoulder, clear of the home indicator, not drawn from the break up. The
frame and its panes are in [`workbench.md`](workbench.md).

It is a `nav` of buttons, and the stop on screen is `aria-current="page"`: each pane is a page of its
own on a phone, the way an app's bottom bar moves between them. Each stop is a Tab stop. It is not a
tab list: it used to claim `role=tablist` with `aria-selected` and had no arrow keys and no panels it
named, so a screen reader announced keys it did not answer (PRM-37). A spec finds a stop with
`getByRole("navigation", { name }).getByRole("button", { name })` and asks which is shown with
`toHaveAttribute("aria-current", "page")`. A count is the stop's `aria-description`, not part of its
name.

## What enforces it

- `hooks/use-view-tab.test.js`: the address parameter, the remembered view, the fallback to the first.
- `components/phone-panes.test.js`: no page draws a foot bar by hand; the bar is a nav of buttons
  with the shown stop current and its words at the hint size.
- Browser specs that drive these controls: `phone-panes`, `logs-views`, `database-performance`,
  `proxy-site-page`, `network-device-page`, `deploy-settings-a`.
- Planned (WP-39): a lint ban on `TabsPrimitive.List` and `tabClasses` outside `ui/tabs` and
  `ViewTabs`, and on a raw `type="single"` `ToggleGroup` outside `Segments`.

## Related

[`forms.md`](forms.md) (a closed set of short words, a run of filter chips),
[`search-toolbar.md`](search-toolbar.md) (chips before the search), [`workbench.md`](workbench.md)
(the phone's panes), [`../foundations/type.md`](../foundations/type.md#8-type) (the view strip's
size).
