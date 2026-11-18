# Search and toolbar

The filter box over a list, and the row of controls it stands in.

**Members:** `SearchInput`, `SearchInputSkeleton` (`components/search-input.tsx`),
`useDeferredSearch` (`hooks/use-deferred-search.ts`), `Toolbar`, `ListToolbar`
(`components/toolbar.tsx`). The chips a toolbar holds are in
[`tabs-chips-segments.md`](tabs-chips-segments.md).

## SearchInput

The one filter box: a magnifying glass, a field, and controls pinned inside its end.

**Anatomy.** The glass sits 10px in, and the text starts after it (`pl-8`). The field is `Input`
with its control edge and focus ring. A clear button and any `trailing` controls sit at the right
end, inside the box.

**Sizes.** The page's box and the pane's box:

| | Under a mouse from `sm` | On a phone, or under a finger |
| --- | --- | --- |
| page (default) | 32px, `sm:w-72`, the body size | 44px, the whole first line of its toolbar, 16px text |
| `dense` (a pane's own chrome) | 28px, 12px text | 44px, 16px text: `dense` is ignored under a finger |

The 16px is not a type choice. iOS zooms the page when a field under 16px takes focus, and the
zoom does not come back out. `Input` writes it on variants that no caller class can remove. `dense`
is for a filter inside a pane's chrome, such as the terminal's session rail or a key rail. A page's
own list filter is never dense: at page level dense skipped the phone size and `/` did not focus it
(PGN-07).

**A page's search is what `/` focuses.** The page box carries `data-page-search`. A dense box does
not.

| Prop | What it does |
| --- | --- |
| `value`, `onChange`, … | An `Input`'s props, including `ref`. |
| `dense` | The pane-chrome size, above. |
| `trailing` | Controls inside the box's end: a regex toggle, an info mark. The caller pads the text for them. |
| `clearable` | A clear button (`Cross`, a 24px hit area) while the box holds text. Escape empties a box that holds text, too. Without `onApply`, the box empties through the caller's own `onChange`, as if the reader had deleted the text. |
| `stopEscape` | Escape that empties the box goes no further, so the sheet or dialog around it stays open. Escape on an empty box still closes it. |
| `onApply` | The box runs a scan per value (a keyspace, a key's members). `value` is then the value in force. The box keeps what is typed and applies it `debounceMs` after the last key, at once on Enter, and empty on Escape or clear. Clearable by default. |
| `debounceMs` | The pause before a typed value applies. 400 by default. |
| `clearLabel` | The clear button's name. "Clear the search" by default; say what is cleared ("Clear the pattern"). |
| `containerClassName` | The box's wrapper: its width in a toolbar. |

```tsx
// A plain list filter.
<SearchInput value={filter} onChange={(e) => setFilter(e.target.value)} clearable
  placeholder="Name, domain or port" aria-label="Search sites" aria-keyshortcuts="/" />

// A scan per value, applied after a pause, following the address.
<SearchInput dense value={pattern} onApply={setPattern} debounceMs={450}
  aria-label="Key pattern" placeholder="Pattern — user:*" clearLabel="Clear the pattern"
  className="font-mono" />
```

## useDeferredSearch

`useDeferredSearch(applied, onApply, ms = 400)` returns `{ draft, setDraft, apply, clear }`.
`SearchInput` uses it with `onApply`; a box that draws its own field can use it directly.

The echo guard is the part that matters. When the box applies "user", the address catches up a
moment later and hands `applied = "user"` back. By then the reader may have typed "user:se". That
returning value is the box's own echo, and it never replaces the draft. A value the box did not send
does replace the draft, because something else changed the search (a namespace narrowed to, Back).
`receiveApplied` is that rule as a pure function, held by `hooks/use-deferred-search.test.js`.

## SearchInputSkeleton

`SearchInputSkeleton({ dense?, className? })` is the box's place while its page loads, drawn from
the box's own size constants. Hand copies had drifted: the proxy certificates bone was 32px where
the field it held the place of is 44px on a phone, so the page moved when the field arrived (D11,
DEP-17).

## Toolbar and ListToolbar

`Toolbar` is a wrapping row of controls with an 8px gap, outside a panel. Inside a panel, the row
under the header is `PanelToolbar` ([`surfaces.md`](surfaces.md)).

`ListToolbar({ search, sort?, actions?, chips?, className? })` is the controls over a list, in one
order on every list:

```
[ search …………………… ]                      [ Sort ▾ ] [ Verb ]
[ All 12 ] [ Enabled 9 ] [ Errors 1 ] …
```

- The **search** opens the first line. It is a `SearchInput`, not dense.
- The **sort** (a `Select size="sm"`) and the **actions** close the first line, at its right end.
- The **chips** take a line of their own under it: a `ChipStrip` of `FilterChip`s, or a
  `Segments` for a two or three way view switch.

Before it, six lists had six layouts. Sites put the sort at the far right and the chips below.
Certificates sorted with a segment, Streams and Config set chips beside the search, Ports had a
third row, and Logins put its search on the right (PGP-07).

**The phone.** The search is the whole first line at 44px. The sort and the verbs make the next
line, and the chips are a strip that scrolls sideways and bleeds to the page's edge. A sort
`Select` inside a `ListToolbar` is 44px on a phone and under a finger, the search's height, rather
than the compact select's 40. A shorter search above a taller select made the select read as the
main field (PGP-08). Chips press as 44px under a finger through their own `hit-area`.

```tsx
<PanelToolbar>
  <ListToolbar
    search={<SearchInput value={filter} onChange={…} aria-label="Search sites" clearable />}
    sort={<Select …><SelectTrigger size="sm" className="w-40" aria-label="Sort sites">…</Select>}
    chips={<ChipStrip aria-label="Filter sites">…</ChipStrip>}
  />
</PanelToolbar>
```

**Not this.** A view switch of the whole page is `ViewTabs`. A window over time is `WindowPicker`. A
toolbar of a workbench (the query editor, the grid) is that workbench's own bar
([`workbench.md`](workbench.md)).

## Accessibility

- Every `SearchInput` takes an `aria-label` that says what it searches ("Search sites"), and
  `aria-keyshortcuts="/"` on the page's own box.
- The clear button is a real button with a name, reachable by Tab, and the focus returns to the
  field after it.
- Escape inside a sheet empties the box first and closes the sheet on the next press only where
  `stopEscape` is set. Set it in sheets and dialogs.

## What enforces it

- `src/hooks/use-deferred-search.test.js` holds the echo guard.
- The phone sizes come from `Input`'s `max-sm:` and `pointer-coarse:` variants, which no caller
  class removes, and from the size constants the box and its skeleton share.
- Planned for WP-39: a lint rule against `dense` on a page-level search and against hand-typed
  search bones.

## Related

[`tabs-chips-segments.md`](tabs-chips-segments.md) for chips and segments,
[`surfaces.md`](surfaces.md) for `PanelToolbar`, [`forms.md`](forms.md) for `Input` and `Select`,
[`../patterns/page-holds-still.md`](../patterns/page-holds-still.md).
