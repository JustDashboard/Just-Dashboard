# Table

A table for readings read down their columns, framed, one line per row (D7).

**Members:** `ui/table` (`Table`, `TableHeader`, `TableRow`, `TableCell`).

**Status: moved text.** WP-34 rewrites this page in wave 1 as purpose, anatomy, use when and when
not (and what instead), props, states, the phone, accessibility, an example call site, what enforces
it, and related families. Until then it holds the rules that were in `design-system.md`, below.

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
window is the wrong thing to ask at all: `settings/use-column-width.ts` measures the column a list
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
