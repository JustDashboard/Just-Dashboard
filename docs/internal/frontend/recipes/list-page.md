# List page

A list of things you open, with its search, filters and one command. Reference: the fleet,
`/deploy`, and `/docker/containers`.

**Reference:** `components/deploy/projects-page.tsx` (cards) and
`app/(dashboard)/docker/containers/page.tsx` (a table of readings whose rows open a page). The shot
is [`docs/readme/deployments.png`](../../../readme/deployments.png). Its siblings are the database
fleet, the proxy's Sites and Streams, Ports, Docker's stacks, images, volumes and networks,
Services, PM2, Packages, System users and the Network devices.

**Components:** `Page`, `PageContext`, `IdentityHeader` ([`../components/page-header.md`](../components/page-header.md));
`ListToolbar`, `SearchInput`, `FilterChip`, `ChipStrip`, `ChipCount`
([`../components/search-toolbar.md`](../components/search-toolbar.md));
`ChoiceList`, `ChoiceRow`, `GroupRule`, `ChoiceListSkeleton`
([`../components/card-row.md`](../components/card-row.md)); `Table` with `RowLink` and `SortHead`
where rows are read down their columns ([`../components/table.md`](../components/table.md));
`EmptyState`, `ErrorState`, `Stale` ([`../components/states.md`](../components/states.md));
`Workspace` for the list's keys and place.

## Cards or a table

Things you open are cards (D5): one bordered row a thing, its mark, its name over a mono line, its
state in aligned columns, an arrow, a `⋯`. A table is for rows read down their columns (D7): a
container's processor beside the next one's. If the reader compares a column, it is a table whose
name cell is the link (`RowLink`); if they find one and open it, it is a `ChoiceList`. Sorting a
card list is a `Select` in its toolbar, not column heads (T4 was not adopted).

## The regions, in order

1. **The identity line** of the section: its mark, its name, what the list adds up to as facts
   ("9 running, 2 stopped"), and one command, the verb that adds one (New site). A section that is
   about one thing (ufw, nginx) names that thing; otherwise it names the section.
2. **What needs a look**, only when something does.
3. **A picture**, where the list is of things wired together (routes, ports). Not for a plain
   inventory.
4. **The toolbar** (`ListToolbar`): the search first, then the chips with their counts, the sort,
   and the list's own small actions. One order, and one layout on a phone.
5. **The list**: cards in a `ChoiceList`, a `GroupRule` over each group where it is grouped, or a
   framed table. Its count is in the section's head or on the All chip.
6. **Related lists** as titled sections under it, each a title and a hairline.

## Skeleton

```tsx
export default function ThingsPage() {
  const things = usePoll(readThings, 5000)
  const [search, setSearch] = useState("")
  const [state, setState] = useState<"all" | "failed">("all")
  const shown = narrow(things.data, search, state)
  return (
    <Workspace name="Things" stateKey="things.list" refresh={things.refresh}>
      <Page rise>
        <PageContext title="Things" />
        <IdentityHeader
          label="About things"
          loading={!things.data}
          mark="docker"
          title="Things"
          facts={things.data && <ThingCounts things={things.data} />}
          command={<Button size="sm" asChild><Link href="/things/new"><Plus />New thing</Link></Button>}
        />
        <ListToolbar
          search={<SearchInput placeholder="Search things" value={search} onChange={(e) => setSearch(e.target.value)} clearable />}
          chips={
            <ChipStrip role="group" aria-label="Which things are listed">
              <FilterChip selected={state === "all"} onClick={() => setState("all")}>All <ChipCount>{things.data?.length}</ChipCount></FilterChip>
              <FilterChip selected={state === "failed"} onClick={() => setState("failed")}>Failed <ChipCount>{failed}</ChipCount></FilterChip>
            </ChipStrip>
          }
        />
        {things.error && !things.data ? (
          <ErrorState error={things.error} what="the things" onRetry={things.refresh} />
        ) : !shown ? (
          <ChoiceListSkeleton rows={6} lines={2} />
        ) : shown.length === 0 ? (
          <NoThings filtered={search !== "" || state !== "all"} onClear={clear} />
        ) : (
          <ChoiceList>
            {shown.map((thing, index) => (
              {/* The verb holds every word the title shows (label in name, A11Y-05). */}
              <ChoiceRow key={thing.id} index={index} href={`/things/${thing.id}`} verb={`Open ${thing.name}`}
                workspaceItem={{ id: thing.id, name: thing.name }} leading={<ThingMark thing={thing} />}
                title={thing.name} description={thing.address} trailing={<ThingState thing={thing} />} />
            ))}
          </ChoiceList>
        )}
      </Page>
    </Workspace>
  )
}
```

## The four states

| State | What the page draws |
| --- | --- |
| Loading | The identity line's loading form, the toolbar at its size (`SearchInputSkeleton`, or the controls disabled) and `ChoiceListSkeleton` at the cards' own box, so the list is as tall loading as loaded. |
| Empty | Two different things. **Nothing yet**: `EmptyState` with what the list would hold as its mark, one sentence and the command that adds the first; the toolbar is not drawn. **Nothing matches**: one line under the toolbar with the way out ("Clear the search"), and the toolbar stays. |
| Unavailable | The thing the list is of is not there (Docker is not installed, the module is off): `MissingThing` under the identity line. A list only an administrator opens is `NeedsAdmin`. |
| Failed | The first read failed: `ErrorState` with `what` and Try again, in the list's place. A later read failed: the list stays, with `Stale` in its head. |

## The phone

The identity line is one row and one line of facts, its command an icon. The search is a line of
its own, 44px tall, and the chips scroll sideways under it. Cards keep their name, their one line
and their state; columns that do not fit are dropped, not wrapped into a taller card. A table keeps
the columns that say what a row is and how it is (`secondary` drops the rest). Rows and cards are
at least 44px to press, and a row's verbs are its `⋯`.

## Checklist

- [ ] The page opens on the section's identity line, with the count as facts and one command. No
      destructive verb is that command.
- [ ] One toolbar, in `ListToolbar`'s order. The search is not `dense`.
- [ ] Rows that are destinations are `ChoiceRow`s; a table only where columns are compared.
- [ ] Every row is one height in every state. Nothing expands under a row: a detail is a page, a
      popover or a `⋯` ([`../patterns/rows-never-grow.md`](../patterns/rows-never-grow.md)).
- [ ] An item opens on its own page with this list as its rail
      ([`detail-with-rail.md`](detail-with-rail.md)), and the search and filters are kept in the
      session so the rail and a return find them.
- [ ] Filters that should survive a link are in the address.
- [ ] Empty-yet and empty-by-filter are told apart. Loading is the list's own skeleton.
- [ ] The list's keys work: arrows, first letters, Enter, Ctrl/⌘+F, F5, `?`.
- [ ] A row's name is its link, said by `verb`; the row is the pointer's larger target.
- [ ] At 390: search on its own line, chips scrolling, no sideways scroll, 44px press boxes.

## The spec to add

1. The identity line says the counts, and its command leads where it says.
2. Typing in the search narrows the list; a chip narrows it and shows its count; Clear brings the
   rest back.
3. Every card is one height (measure each), and a landmark under the list sits at the same `top`
   before and after the read lands.
4. With no things, the empty state names the first step; with a filter that matches nothing, the
   toolbar is still there.
5. Enter on a row opens its page, and Back returns to the list with its search and its place.
6. At 390 the document is no wider than the window, and the search is a line of its own.

`tests/browser/deploy-projects.spec.ts`, `container-page.spec.ts` and `processes-ui.spec.ts` are
the models.
