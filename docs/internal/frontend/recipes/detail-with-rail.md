# Detail with a rail

An item's detail is a page of its own (D9). It has its own route with a way back, its views in
the address as `?tab=`, and from `xl` a rail of its siblings beside it. A sheet is only for a small
add or edit form.

**Reference:** `/processes/[pid]` (`procs/process-page.tsx`, its rail `process-rail.tsx`). Open
it against the fixtures of `tests/browser/processes-ui.spec.ts`, at 1440 and on a 390 touch
device. Its siblings are the container, stack, image, volume and network
pages under `/docker`, a service and a PM2 application under `/processes`, an account and a group,
a package, a site, a stream and a certificate, the seven Network detail pages, a port and a jail, a schedule, a backup job, and a
database's account, statement, table and session.

**Components:** `DetailPage`, `StepArrows`, `useSiblingSteps`, `stepCommands` and `useDetailTab`
in `components/detail-page.tsx`, and `BackLink` in `components/page.tsx`
([`../components/page-header.md`](../components/page-header.md)). The rail is `Rail` in `components/rail.tsx`
([`../components/rail.md`](../components/rail.md)). The view strip is `ViewTabs`
([`../components/tabs-chips-segments.md`](../components/tabs-chips-segments.md)).

## The regions, in order

1. **The rail**, from `xl`: the list the item was opened from, in that list's own search, filters
   and order.
2. **The context line**: the way back to the list (`back`, drawn as a `BackLink`) at the start, and
   the verbs and the two step arrows with the shortcuts' `?` at the end.
3. **The identity line**: the item's mark, its name, its state in words and its facts
   ([`../components/page-header.md`](../components/page-header.md)).
4. **The views**: a `ViewTabs` strip and the open view's pane. From `xl` the head stays put and
   the views scroll under it. Narrower, the page scrolls as a whole. The body that scrolls from
   `xl` is marked `data-detail-scroll`, so a field reached by Tab stops short of a floating save
   bar there as it does in the shell's scroller; a pane with a scroller of its own inside it (an
   account's) takes the mark too.

That is the order on screen. In the document the item comes first and the rail last (`DetailPage`
places the rail with `order` from `xl`), so a keyboard reaches the way back, the views and the
controls before the rail's search and rows.

The thing's verbs are on the identity line, one command and a `⋯` (`IdentityVerbs`), never a row
of buttons beside the way back. The shortcuts' `?` shares the context line with the back link.

## Skeleton

```tsx
export function ThingPage() {
  const router = useRouter()
  const [tab, setTab] = useDetailTab(TABS, { remember: "things.thing.tab" })
  const rail = useThingRail() // the list page's own search, filters and order
  const open = (key: string) => router.push(thingHref(key, tab))
  const steps = useSiblingSteps(rail.keys, current, open)
  return (
    <DetailPage
      name="Thing"
      what="thing"
      stateKey="thing.page"
      refresh={reading.refresh}
      steps={steps}
      rail={<ThingRail {...rail} current={current} />}
      back={{ href: "/things", label: "Things" }}
      title={name}
      identity={<ThingIdentity thing={reading.data} />}
    >
      <Tabs value={tab} onValueChange={setTab}>
        <ViewTabs label={`Views of ${name}`} views={VIEWS} shown={tab} />
        {/* the panes */}
      </Tabs>
    </DetailPage>
  )
}
```

Keep a page's Workspace `name` and `stateKey` when it moves onto `DetailPage`. The help sheet is
titled by the name, and the remembered place is kept under the key. `escape` is passed to the
Workspace too: it returns true when the page used the key itself (letting go of a picked run). The arrows' labels are
"Previous <what>" and "Next <what>", and specs press them by those names.

## The view in the address

`useDetailTab(tabs, { param, remember, keepMounted })` returns `[tab, setTab, visited]`.

- The first view is the bare address.
- A view is written to the history at once with `replaceState`. It is never pushed, because Back
  does not step through views. It is never debounced either: a debounced write cancels a
  navigation the router has pending, which shows up as CI-only "URL did not change" failures.
- An address that changes under the page is followed, by the rules it opens on: one that names a
  view opens it, and one with no `?tab=` (a link to the bare page, Back past a view) opens the
  remembered view or the first, never the view that was on screen.
- With `remember`, a page opened without `?tab=` opens on the view last left.
- `visited` holds the views opened so far, so their panes can stay mounted.

## The four states

| State | What the page draws |
|---|---|
| Loading | The context line and the identity line at their loaded height, with the name the rail already knows; the rail's ghosts at the 48px row. |
| Empty | A view with nothing in it says so in its pane (`EmptyState`). The frame does not change. |
| Unavailable or missing | The item is gone or never was: `MissingThing` or `EmptyState` in the body, with the way back. The rail stays, so the next item is still a press away. |
| Failed | The item's read failed: `ErrorState` in the body with **Try again**. The rail's own failure is its own `ErrorState`, in place of its rows. |

## The phone

One pane: the context line with the way back and the two arrows, the identity line, the view strip
and the open view, scrolling as a whole. There is no rail and no stacked scrolling boxes
([`../patterns/README.md`](../patterns/README.md), one pane on a phone).

## Checklist

- [ ] The route is the item's own, and every view is in `?tab=`.
- [ ] `DetailPage` is the frame. There is no hand-written Workspace with Alt+↑ commands,
      `Page fill="xl"` or a hand-made back link.
- [ ] The rail is a `Rail`. It keeps the list page's `data-slot`, search, filters and order, and
      its rows are links on the view on screen.
- [ ] The rail has a designed loading state (`RailGhosts`), an empty line and an error state.
- [ ] Alt+↑ and Alt+↓ and the two arrows walk the rail's order, and the view goes with them.
- [ ] The head holds still: the name shows at once from what the rail knows.
- [ ] There is no description under the title. A verb is one word.
- [ ] The identity line follows its rules: one command and a `⋯`; no destructive verb in the
      brand face (Terminate, Kill, Delete and Drop go behind the `⋯`, and a page whose only verbs
      are destructive draws no command); on a phone one row and one line of facts, the command
      its icon, with danger and warning facts first.
- [ ] The item comes before the rail in the document.
- [ ] On a phone it is one pane with no rail.

## The spec to add

A page moving onto this shape adds a browser spec with these four checks:

1. At 1440 the rail is visible by its `data-slot`. The row on screen is a link with
   `aria-current="page"`. Every row is one height.
2. Alt+↓ moves to the next sibling with `?tab=` carried (`toHaveURL`), and the button named
   "Previous <what>" comes back.
3. A filter the list page was left on narrows the rail, the item on screen stays first, and
   **Clear filters** brings the rest back.
4. At 390 there is no rail, and the page does not scroll sideways.

`tests/browser/container-page.spec.ts` and `processes-ui.spec.ts` are the models.
