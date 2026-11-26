# Table

A table for readings read down their columns, framed, one line per row (D7).

**Members:** `Table`, `TableHeader`, `TableBody`, `TableFooter`, `TableHead`, `SortHead`,
`TableRow`, `TableCell`, `TableCaption`, `stickyTableHeader`, `ROW_NAME`, `SECONDARY_COLUMN`
(`components/ui/table.tsx`); `TABLE_BLEED` (`components/row-list.tsx`); `RowLink`
(`components/row-link.tsx`).

## Purpose

A table is for things compared on three or more readings, read down a column: which process holds
the memory, which volume is largest and unbacked, which statement takes the runtime. A row may open
its thing's page, and the table is still a table of readings with a destination on its name
(Processes, Packages, Services, Volumes). It is not for a list of things you open and do not
compare: that is a `ChoiceList` of cards ([`card-row.md`](card-row.md), D5). A few things each with
a name and a state is a `RowList` ([`list-row.md`](list-row.md)).

## Anatomy

```
 Name ▾          State        CPU      Memory   [ verbs ]     ← TableHead / SortHead, h-10, 12px muted
 ───────────────────────────────────────────────────────────
 [mark] web      ● Running    12%      180 MB   ⋯             ← TableRow, a hairline under it
        image    for 6d                                       ← the name cell's second line
```

- The cells are one step denser than body text (`text-xs`) and their figures tabular (`.numeric`)
  by default. The first column takes the row's name, as a `RowLink` or a link, under a mark.
- A column of verbs, a mark or a spacer has no visible title. It still has a name:
  `<TableHead label="Actions" />` draws it for a screen reader only.
- Inside a plain `Panel`, `TABLE_BLEED` on the table's `containerClassName` (or a flush
  `PanelBody`) lines the first column's words up with the section title. Never type the `-mx-4`.
- A capped table (`containerClassName="max-h-…"`) keeps its head with `stickyTableHeader`.

## Use when, and when not

| Use a table when | Use instead |
| --- | --- |
| Many things compared on three or more readings | |
| Rows that open a page and are read down their columns | |
| Things you open and do not compare | `ChoiceList` of `ChoiceRow` cards |
| A few things with a name and a state | `RowList` |
| Label and value pairs about one thing | `DetailList` ([`values-facts.md`](values-facts.md)) |

## Props

| Part | Prop | Does |
| --- | --- | --- |
| `Table` | `containerClassName` | On the scroll container: a height cap, `TABLE_BLEED`. A container that scrolls and holds nothing focusable becomes a named region and a tab stop, so its rows can be reached. |
| `TableHead` | `label` | The column's name for a screen reader where it has no visible title. Drawn only without children. |
| `TableHead`, `TableCell` | `secondary` | Not drawn below `sm` (`SECONDARY_COLUMN`). Put it on the head and every cell of the column. |
| `TableRow` | `onActivate` | The whole row is the pointer's target for its primary action. A press on a control inside the row is left to that control. Enter and Space work on a row that is a stop itself. |
| `SortHead` | `label`, `active`, `descending`, `onSort` | A head that orders the rows: `aria-sort` on the `th` (`none` on the columns not in use), the words a `type="button"` with the focus ring, one chevron on the column in use. |
| `SortHead` | `align` | `"right"` for a column of figures: the chevron goes before the words, so the words keep the figures' edge. |
| `SortHead` | `after`, `secondary`, `className` | Something beside the button and not in it (an `InfoTip`); the phone rule; the `th`'s width. |

```tsx
const by = (column: SortKey) => ({
  active: sort.key === column,
  descending: sort.dir === "desc",
  onSort: () => onSort(column),
})

<TableHeader className={stickyTableHeader}>
  <TableRow>
    <SortHead label="Container" {...by("name")} />
    <SortHead label="CPU" {...by("cpu")} align="right" className="w-32" />
    <TableHead secondary className="w-36">Ports</TableHead>
    <TableHead label="Actions" className="w-28" />
  </TableRow>
</TableHeader>
```

## States

- **Loading.** The table's own shape with bones in its cells, or the page's skeleton for the
  section, so nothing under it moves when the rows land (D11).
- **Empty.** `EmptyNote` in the section for a filter that matches nothing; `EmptyState` when the
  table is the page's subject ([`states.md`](states.md)).
- **Selected.** `data-state="selected"` on the row (the selection fill); `aria-current` where the
  row is the page being shown, as the compose table's own container is.
- **Never open.** A row is one line, one height in every state. Nothing expands under it
  ([`../patterns/rows-never-grow.md`](../patterns/rows-never-grow.md)).

## The phone

A table has one phone answer, chosen by what its rows are (MOB-11):

1. **A table of readings** keeps its table and drops the columns a phone can do without:
   `secondary` on the head and every cell, so what is left is the name, its state and one value.
   It never scrolls sideways for its main reading.
2. **A table whose rows open something** becomes rows below `sm`: the name over one line of its
   state and one or two readings, the way the containers list draws its phone rows. The section
   around it loses its frame (`Panel framed={!phone}`), a title and a hairline, because a frame
   around a list of rows on a phone is padding that cuts the rows short. Audit's trail and a
   container's compose neighbours are the references.
3. Which shape is drawn is chosen once with `useBreakpoint`, never as a hidden twin (§12 below).

## Accessibility

- **A row is not a second, nameless stop.** `onActivate` is the pointer's convenience. The keyboard
  reaches a row by its name control, marked `{...ROW_NAME}` (the `data-workspace-primary` a
  `Workspace` already puts the keyboard back on). A row holding one is no tab stop itself. A row
  that holds none stays a stop so it can be reached, and should get a name control, or at least an
  `aria-label`. Before 0.9.0 every activatable row was a stop beside its own name button: two
  presses of Tab a row, the first reading every cell as one name (A11Y-10). `RowLink` passes
  `ROW_NAME` and any other attribute on to its button; until 0.9.1 it dropped them, and the
  Docker containers table stayed two stops a row (spec `docker-ui.spec.ts`, *a container's row
  is one Tab stop*).
- **Every header cell has a name**: a visible title, or `label` (axe `empty-table-header`).
- **A sortable head says its order**: `aria-sort` on the `th`, and its words are a real button with
  the focus ring. There is one `SortHead`; it replaced five copies, one of which said nothing to a
  screen reader (DBO-07, A11Y-19).
- A name control takes the focus ring (`focus-ring`) and `rounded-sm`, so the ring has corners.

## Example

```tsx
<Table className="table-fixed" containerClassName={cn("max-h-[calc(100svh-22rem)]", TABLE_BLEED)}>
  <TableHeader className={stickyTableHeader}>
    <TableRow>
      <TableHead>Timer</TableHead>
      <TableHead secondary className="w-36">Next run</TableHead>
      <TableHead label="Actions" className="w-24" />
    </TableRow>
  </TableHeader>
  <TableBody>
    {timers.map((timer) => (
      <TableRow key={timer.unit} onActivate={() => open(timer)}>
        <TableCell>
          <button type="button" {...ROW_NAME} onClick={() => open(timer)}
            className="truncate rounded-sm text-body font-medium focus-ring hover:underline">
            {timer.name}
          </button>
        </TableCell>
        <TableCell secondary><RunAt at={timer.next} /></TableCell>
        <TableCell><VerbActions dim verbs={verbs(timer)} /></TableCell>
      </TableRow>
    ))}
  </TableBody>
</Table>
```

## What enforces it

- `src/components/ui/table.test.js`: `SortHead` puts `aria-sort` on the `th` and draws a
  `type="button"` with the focus ring and one chevron; `TableHead label` and `secondary`; a row
  that opens something starts as a stop until its name is found.
- The axe probe in the captures (`empty-table-header`), and the browser specs of the pages that
  draw these tables (`audit-ui`, `container-page`, `processes-ui`, `packages-ui`,
  `database-performance`, `files-ui`).
- `tests/browser/row-name-stop.ts` (`expectRowIsOneStop`): a row whose name is its control is one
  Tab stop, and Tab goes from the name straight to the row's next control. The Processes, PM2,
  Services and stack services tables run it (`processes-ui`, `services-ui`, `stack-detail`); each
  marks its `RowLink` with `ROW_NAME`.

## Related

[`list-row.md`](list-row.md) for `RowList`, `RowLink` and `TABLE_BLEED`; [`card-row.md`](card-row.md)
for things you open; [`surfaces.md`](surfaces.md) for when a section takes a frame;
[`search-toolbar.md`](search-toolbar.md) for the toolbar over a table.

## 12. A table is a layout, not a contract

A wide table narrows by dropping columns, and that is the right answer until it is not. Past roughly
half of them the reader is no longer looking at a table — they are looking at the remains of one: a
wide first cell, a wedge of empty space, and two stubs where seven columns used to be.

**When more than half the columns would go, replace the layout instead.** The containers table was
nine columns wide and kept three on a phone, so below `xl` the same rows were drawn down the row
instead of across it. The test of that replacement is that **nothing was dropped to achieve it**:
the image, the ports, both live readings and the issue count are all present, because a phone is
where somebody checks whether the thing they just deployed is alive and every one of those is part
of that answer.

**The breakpoint is where the table stops fitting, not where the viewport stops being wide.** That
one swapped at `xl` (1280) rather than `lg` (1024), because the sidebar takes 256px of it: at 1024 the
table appeared already scrolling inside its own panel, with Issues and the row's actions past the
right edge — a table that arrives broken.

**And a table whose every row is a place to go is not a table at all.** Since 2026-09-23 the
networks lists are cards at every width (the stacks, containers, images and volumes lists were made tables of readings in 0.7.1 and after — §15 pass 2) — the argument
`git/repo-card.tsx` made for checkouts, and §16's for anything you take: each row opens a page or a
panel, so it carries the lit edge. The containers list was the first of them and went back to a
table in 0.7.1 (§16 says where that leaves the line). `components/docker/containers-table.tsx` (the
Containers page's; the overview's own copy went in 0.8.7 with the table it drew) keeps
both halves of the paragraphs above: from `xl` a table of fixed columns, each heading naming and
sorting its reading; below, the same readings beneath the name, nothing dropped. Which shape is drawn
is chosen once (`useMediaQuery`) rather than by `hidden`/`xl:block` twins, because a reading that
exists in a hidden copy is two answers to every query a test or a screen reader makes. The list around the cards is a plain panel — a frame around framed cards is two nested
frames, which is the stacking this section refuses. System users took the same argument in 0.7.0:
every account opened its keys, so the eight-column table became cards,
their groups, last sign-in, keys and state beside the name from `lg` and beneath it below. It became
a table again in 0.8.2 at the operator's request, when an account came to open a whole page rather
than a sheet: the readings on a card had no names on them, a key count beside an arrow and a padlock
that read as a state rather than a verb, and the four questions the page exists for are each asked
of a column. In 0.8.7 the rows are cards once more (`ChoiceRow`) and keep what the table was for:
each reading has a column of its own from `xl` (how it gets in, how far it reaches, when it last
did), a fixed width so they line up down the list, and narrower each takes a line of its own under
the name, so every account is one height at every width. The audit
log is the counter-example on the next page of the same section: an entry opens nothing, so its
trail stays a table. The volumes were cards from 2026-09-23 and became a table again on 2026-10-08
at the operator's request: a volume's card said "1 container" and not which, so the question every
volume is opened with took a click, and "which is largest, unmounted or not backed up" is asked of a
column. A row that opens a page but is read down its columns is a table of readings with a
destination on its name, as Processes, Packages and Services are, and it keeps its frame (§2).
The images were cards from 2026-09-23 and became a table again on 2026-10-08
at the operator's request, on the Processes, Packages and Services tables' precedent: a row that
opens a sheet but is read down its columns — which image is largest, oldest, unused or behind — is
a table of readings with a destination on its name, and it keeps its frame (§2).

The networks list left the cards on 2026-10-08, at the operator's request, by the argument the
containers made: eleven networks are compared down their columns — which is busy, which has nothing
on it, which subnet each holds — far more often than one is opened, and the cards were eleven grey
copies of one line. It is a table of fixed columns from `xl` whose name is the button and whose row
takes Enter, without the lit edge, drawn down the row below `xl` (`components/docker/network-table.tsx`).
Since 0.8.7 the name is a link to the network's own page (`/docker/networks/[name]`) rather than the
button of a sheet, and a row is one height in the table and in the list it becomes: what a network
or a container has more or less of (a second network, a second name, traffic) is drawn inside the
row's two lines or counted, never added under them.

**The deployment section's rows took the same rule, and it moved the breakpoint twice more.** A run
(`deploy/run-row.tsx`), a runtime service and a channel set their readings beside the name in fixed
measures where there is room and under it where there is not, chosen once with a media query — and
the width they need is measured inside the project's shell, not the window: at 1280 the content
column beside the project's navigation is about 968px, which left a service's name 150px beside
five readings, so the wide shape of the Overview's runs-beside-previews starts at `2xl`, as does
the build console's rail of stages beside the transcript. Inside a settings page the column is
narrower still, because the fields are held to 48rem however wide the window is, so there the
window is the wrong thing to ask at all: `hooks/use-column-width.ts` measures the column a list
is drawn in, before its first paint, and the variables and linked databases (from 600px), the
storage page's mounts (from 480px) and Automation's webhooks, schedules, approvals and previews
(from 560px) choose beside-or-under from that — still once, still drawn once.

**Wide, a lit card is one line** — the Notifications channel card is the reference. The mark, the
name over one truncating line of its secondary facts joined with " · ", and at its far end the state
word and the outcome strip, the strip in a fixed `w-27.5` column so that down a list the strips are
one column and the states end on one edge, then the arrow and the verbs on the card's middle. A
second band under the name — a strip, a preview address, a run's trigger — left the card two or
three lines tall with the verbs level with the name over an empty corner, and 0.7.1 took it off
every deployment card that had one: webhooks, schedules, approvals and previews; linked databases,
the gate evidence, the live mounts and a variable's linked database; a backup job (`JobCard`, which
measures its own width because it is drawn on /backups as well); a runtime service (one line from
1280, its ports from 1536), a domain (from 1024), a mount and a dependency; the fleet's list rows
(from `lg`, in columns two lines tall so the row keeps the height of its name and source line with
both lines spent, traffic joining at `xl`), the runs in flight (from `sm`) and the archive (from `sm`).
Narrow, each keeps one short second line, as the channel card does on a phone; a fact that fitted
neither line and is not needed to recognise or judge the card went to the page or sheet it opens.
An isolation note on a preview keeps its line, because it is the thing to do. A row that
only reflows rather than rearranging, a domain or a mount editor, uses a container query
(`@container`, `@min-[40rem]` for a domain and `@min-[36rem]` for a mount) and draws nothing twice
by construction.

**A variable is read down its columns, and a value is revealed in place.** At the operator's request
in 0.8.5 an environment's and a shared variable's card is `ChoiceRow columns`, one 44px line from
600px: the name, the value, then an edge of one width on every row, with a state in the value's
column so it cannot move the columns. Reveal swaps the mask for `RevealedValue` in the same cell, one
line that scrolls sideways however long the value is. It was a framed band under the row, which
pushed the rest of the list down for the length of a look. Nothing hangs under the row at any width:
a shared variable's readers open from its *Read by N*, and on a phone the row is its name over its
value, where a variable reaches being on the filter chips and in its editor. A band left empty room
under the mark. Reveal and copy are inline in both states,
when a variable was set is on the face and in its editor, and no strip of the variables' products
stands under the section's title: each row wears its own mark, and one logo there read as a stray
icon.

A row drawn this way is a **click target, not a control**. `role="button"` on the wrapper is the
obvious way to make a whole card pressable and is wrong: an ARIA button takes its accessible name
from its contents, so a screen reader announces the entire row — readings, ports, and the label of
the menu button nested inside it — as one control's name. The row's title is a real `<button>`; the
surrounding click handler is a convenience for the pointer that skips any press landing on a control
of its own, exactly as `TableRow`'s `onActivate` does.

**A title column caps its width, and the title binds itself to that cap.** `truncate` on the title
is not enough on its own: a `<button>` is inline-block, so it sizes to its text and a process whose
name is the whole of a headless Chrome's argv paints across Owner, State and CPU rather than
ellipsing. `RowLink` carries `max-w-full min-w-0` for that reason — the first binds it to the
`max-w-[Nrem] min-w-0` wrapper every title cell is built from, the second lets it shrink where the
title is laid out with flex beside a tag. Anything else that sizes to its content in a capped cell
needs the same pair.

**A stack's services went back to a table on 2026-10-08, and the argument above is why.** Each
service on `/docker/stacks/<name>` opens its container, so it had been a lit card in a
`ChoiceList`. But a stack is six things read against each other — which one is restarting, which
one holds the memory — and six cards put each reading in a different place and offered nothing to
read down. So the services are one framed table (`docker/stack-services-table.tsx`): fixed columns
for state, CPU (the last hour beside this second's share), memory (against the limit, amber past
85%), network from 1536 and ports, the service taking what is left, and the compose verbs in a
`DimActions` column. The row still opens its container through `onActivate` and the name's
`RowLink`; what it gave up is the lit edge, because a table of readings answers the pointer with
the row wash (§16). Below 1280 the same row is drawn down instead of across with nothing dropped,
chosen once by `useMediaQuery`.
