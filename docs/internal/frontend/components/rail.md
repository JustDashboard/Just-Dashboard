# Rail

The list a detail page was opened from, folded into a column beside it from `xl` (D9). The next
item is one press away, Alt+↑ and Alt+↓ walk it, and the view on screen goes with you.

**Members:** `Rail`, `RailRow`, `RailName`, `RailLine`, `RailFigure`, `RailFigures`, `RailGhosts`,
`RailAction`, `RailNote`, `RailColumn`, `railRowClass` and `railInk`, all in
`components/rail.tsx`. The frame a rail stands in is `DetailPage`
([`../recipes/detail-with-rail.md`](../recipes/detail-with-rail.md)).

**Retired (0.9.0):** the Docker pages' `DockerRail` and `DockerRailRow`, and the network detail
pages' old frame, are gone. Each Docker item page draws its rail with `Rail` and `RailRow`
(`docker/container-rail.tsx` and its siblings), and the network section's run and capture pages
with `ItemRail` (`network/item-rail.tsx`), which is the same parts.

## Purpose

Twenty-two rails were written by hand, with the same column, the same 48px row and the same scroll
into view. They drifted in what they did not share. Half the rows were buttons calling
`router.push`, so they could not open in a new tab and were not announced as links. Six loaded
with `LoadingRows`, 36px bars in a column of 48px rows, so the column changed shape when the list
landed. A list that failed said "No stack matches." The width was `w-64`, `w-72` or `w-80`, and
each rail captioned and filtered itself differently. `Rail` is the one column. A page's rail file
keeps only where its list comes from and what its row says.

## Anatomy

From the top:

1. **Search** (`search`): one `SearchInput`, labelled "Search the <label>" unless `search.label`
   says otherwise. Give it to a list that is searched on its own page.
2. **Filters** (`filters`): the list's chips or a segmented switch, under the search. Only the
   filters the list page has.
3. **The caption line**: what the list holds and how it is ordered ("12 containers · failing
   first", "1 of 5 · running"), then at its end either **Clear filters** (when `filtered` and
   `onClearFilters`) or one `action`, a `RailAction` switch such as "System groups" or
   "Everything (+8)". The caption is words, not a label with a number after it.
4. **The rows**, in the list's own order:
   - the item on screen first, set apart by a rule, when the list's filter leaves it out
     (`pinned`);
   - a rule or a group's name over a row where the list has shelves (`divideBefore`);
   - each row a `RailRow`.
5. **What stands in for rows**: `RailGhosts` while the list is read, a compact `ErrorState` when
   it failed (`error`, `onRetry`), and `empty` ("No volume matches.") when nothing matches, said
   under the item on screen too.
6. **A foot** (`foot`) for a capped list: "The first 300 of 2,114. Search to reach the rest."

The column is `w-rail` (18rem). It is hidden below `xl` and its width is forced after the
caller's classes, so a page that still passes `w-64` gets the rail's width.

### The row

`RailRow` is one 48px row in every state: the item's mark (a `ProductLogo` or the area's own mark
at `size-7`), its state as a dot in the mark's corner (`dot`), two lines, and a reading at its
end (`end`).

- First line: `RailName`, the name, `font-medium`, `font-semibold` on the one on screen. Use
  `mono` for a name the machine spells: a device, an account, a volume, a package.
- Second line: `RailLine`, in the muted ink unless `ink` gives it a tone (`railInk("danger")`, a
  hue for a stack).
- End: `RailFigure` for one figure (a size, a count with its noun, the days left), `RailFigures`
  for two over each other (CPU over memory).
- `quiet` draws a switched-off item's words in the muted ink. It never fades with opacity.

## Use when, and when not

- **Use it** for the sibling list beside any item's detail page: a process, a unit, a container, a
  site, an account, a device.
- **Not** for a workbench's own panes (the terminal's sessions, the schema rail, the logs
  sources, the map). They have heads, actions and grouped rows of their own: `WorkbenchSide` and
  the workbench rail parts.
- **Not** on a phone. Below `xl` the page is one pane with the way back and the two arrows.

## Props

`Rail<T>`:

| Prop | What it is |
|---|---|
| `slot` | The rail's `data-slot`, which specs find it by. Keep the one a page had. |
| `label` | What the list is of, in the plural: the `nav`'s name and the search's. |
| `listLabel` | The list's name, where "<label> to open" would not read. |
| `rows`, `keyOf`, `current` | The list in its own order, a row's key, and which row is on screen. |
| `row(row, current)` | Draws one `RailRow`. |
| `pinned` | The item on screen, drawn first when `rows` does not hold it. |
| `loading`, `loadingRows` | Ghost rows (6 by default) while the list is read. |
| `error`, `onRetry` | The list could not be read. Never folded into the caption. |
| `empty` | "No <thing> matches." |
| `search`, `filters`, `caption`, `action` | The head, as above. |
| `filtered`, `onClearFilters` | Draws **Clear filters** at the caption line's end. |
| `divideBefore(row, index)` | `true` for a rule, a string for a group's name. |
| `foot`, `bindings`, `className` | A last line; `useInspectionOrder`'s bindings; extra classes. |

`RailRow`: `item` (the `data-workspace-item` the keys walk by), `name`, `label` (the accessible
name, where it is not the name), `title`, `current`, `href`, `onOpen`, `mark`, `dot`, `end`,
`quiet`, and its two lines as children.

`href` is the row's address on the view on screen. Read `?tab=` with `useSearchParams` and build it
with the area's own helper (`processHref(pid, tab)`). `onOpen` takes an unmodified press instead
of the link, for a page that carries state the address does not hold. It returns `false` to let
the link follow after all, which is how a page with unsaved work asks first. A row without `href`
is a button. That is deprecated, because every item has an address.

## States

| State | What the rail draws |
|---|---|
| Loading | The head with "Reading the <things>…", and `RailGhosts` at the 48px row. |
| Loaded | The rows; the one on screen filled (`bg-accent`) and scrolled into view. |
| Filtered to nothing | The item on screen, a rule, then `empty`. |
| Failed | No caption, and a compact `ErrorState` with **Try again** in place of the rows. |
| Stale | A refresh that fails keeps the rows it has. |

## The phone

There is no rail on a phone. The page is one pane with the way back and the two arrows.

## Accessibility

- The column is a `nav` named by `label`. The list is an `ol` named "<label> to open", with
  `aria-busy` while it is read.
- The row on screen carries `aria-current="page"`.
- Rows are links, so a screen reader announces them as links and a middle click opens a new tab.
- The ghosts are `aria-hidden`. The failure is the `ErrorState`'s `role="alert"`.

## Example

```tsx
<Rail
  slot="process-rail"
  label="Processes"
  search={{ placeholder: "Name, PID, user or owner", query, onQuery }}
  rows={rows}
  keyOf={processKey}
  current={(row) => row.pid === pid}
  pinned={currentRow}
  row={(row, on) => (
    <RailRow
      item={processKey(row)}
      name={row.name}
      current={on}
      href={processHref(row.pid, tab)}
      mark={<ProductLogo id={processProduct(row.name)} size="sm" fallback={Cpu} />}
      dot={<StatusDot state={processStateTone(row.state)} className="size-1.5" />}
      end={<RailFigures top={percent(row.cpuPercent)} bottom={bytes(row.rss)} />}
    >
      <RailName current={on}>{row.name}</RailName>
      <RailLine>{ownerName(row)}</RailLine>
    </RailRow>
  )}
  loading={loading}
  error={error}
  onRetry={refresh}
  empty="No process matches."
  filtered={filtered}
  onClearFilters={clearFilters}
  caption={caption}
/>
```

## What enforces it

- `components/rail.test.js` covers several things:
  - the row on screen is a link with `aria-current="page"`;
  - the ghosts stand at the 48px row;
  - a failed list draws `ErrorState` and never "No x matches";
  - the width is `w-rail` whatever a caller asks;
  - no `*-rail.tsx` file draws its own 48px row.
- The browser specs find each rail by its `data-slot` (`container-rail`, `process-rail`,
  `account-rail` and others) and its rows by role `link`.

## Related

- [`../recipes/detail-with-rail.md`](../recipes/detail-with-rail.md): the page a rail stands in.
- [`states.md`](states.md): `ErrorState`.
- [`search-toolbar.md`](search-toolbar.md): `SearchInput`.
- [`list-row.md`](list-row.md): the list page's own rows, which a rail row folds.
