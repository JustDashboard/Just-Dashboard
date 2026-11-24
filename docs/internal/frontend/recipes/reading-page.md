# Reading page

A page that reports: the reader arrives to find out what is true. Reference: a project's Overview,
`/deploy/[id]`.

**Reference:** `components/deploy/project-shell.tsx` (the header every page of a project opens on)
and `project-overview.tsx`. The shot is [`docs/readme/deployment.png`](../../../readme/deployment.png).
Its siblings are the host Overview, the proxy overview, the Network overview, Security, Backups and
a database's Home.

**Components:** `Page`, `PageContext`, `PageState` (`components/page.tsx`); `IdentityHeader`,
`IdentityVerbs` and `Fact` ([`../components/page-header.md`](../components/page-header.md));
`WireFlow` ([`../components/wiring.md`](../components/wiring.md)); `ReadingLine`, `BarReading`,
`RankList`, `ChartPanel` ([`../components/readings.md`](../components/readings.md)); `Panel plain`
with `PanelHeader` ([`../components/section-head.md`](../components/section-head.md)); `ChoiceList`
and `RowList` for what the sections hold.

## The regions, in order

1. **The back line**, only where the page sits under a list: the way back at the start, the walk
   and the shortcuts' `?` at the end (`PageContext back`, `actions`).
2. **The identity line**: the thing's mark, its name, its state as a dot and a word, one line of
   live facts, then one brand-faced command and a `⋯` ([`../patterns/identity-line.md`](../patterns/identity-line.md)).
3. **What needs a look**, when something does: one-line notices under the line (`NoticeLines`), or
   a `Notice`. Nothing is drawn when all is well.
4. **A picture of the thing** ([`../patterns/picture-first.md`](../patterns/picture-first.md)).
5. **Readings in words**: `ReadingLines`, a band of `BarReading`s, or both
   ([`../patterns/readings-in-words.md`](../patterns/readings-in-words.md)). No row of tiles.
6. **Sections**, each a title and a hairline ([`../patterns/sections.md`](../patterns/sections.md)):
   lists of things you open as cards, a table only for readings, a chart with its summary line.
7. **A log at the page's foot**, where the thing has one (`EmbeddedLog` in a `LogSection`).

## Skeleton

```tsx
export default function ThingPage() {
  const thing = usePoll(readThing, 5000)
  if (thing.error && !thing.data) {
    return <PageState title="Thing" error={thing.error} onRetry={thing.refresh} />
  }
  const data = thing.data
  return (
    <Page rise>
      <PageContext title="Thing" />
      <IdentityHeader
        label="About thing"
        loading={!data}
        mark="nginx"
        title={data?.name}
        state={data ? <Status tone={toneOf(data)} label={wordOf(data)} /> : true}
        facts={data && <ThingFacts thing={data} />}
        {...(data ? IdentityVerbs({ verbs: verbsOf(data), name: data.name }) : { command: true })}
      />
      <WireFlow phone="marks" loading={!data} start={callers(data)} hub={hub(data)} end={served(data)} />
      <ReadingLines>
        <ReadingLine mark={<ReadingMark hue={HUE.net} glyph={Globe} />} name="Traffic" trend={trend}>
          {data ? `${rate(data)} requests a minute` : <NoReading />}
        </ReadingLine>
      </ReadingLines>
      <Panel plain>
        <PanelHeader title="Sites" count={data?.sites.length} link={{ href: "/proxy/sites", label: "All sites" }} />
        <PanelBody>{data ? <SiteCards sites={data.sites} /> : <ChoiceListSkeleton rows={3} />}</PanelBody>
      </Panel>
    </Page>
  )
}
```

`Page` declares the reading register by default. A page that offers refresh, row movement or a
search wraps itself in a `Workspace` ([`../patterns/keyboard-and-place.md`](../patterns/keyboard-and-place.md)).

## The four states

| State | What the page draws |
| --- | --- |
| Loading | Its final shape: the identity line as `IdentityHeader loading` with what is already known, the picture's `loading` form, each reading as `NoReading`, each list as its skeleton. Nothing moves when the reads land ([`../patterns/page-holds-still.md`](../patterns/page-holds-still.md)). |
| Empty | Each section says so in its own body (`EmptyState` with what it would hold and the verb that fills it, or `EmptyNote` in a small block). The page's frame does not change. |
| Unavailable | The thing the page is about is not there (Docker, a proxy engine, a module): `MissingThing size="page"` under the identity line, drawn as the thing with the next step. A page only an administrator opens is `NeedsAdmin`. |
| Failed | A first read that fails is `PageState` with the error and Try again. A later read that fails keeps the last reading: `Stale` beside the block, or one `StaleNotice` for the page. One block's failure is its own `ErrorState`, in its place. |

## The phone

One column, the same order. The identity line is one row and one line of facts, its command an
icon. The picture shrinks to its marks ([`../patterns/pictures-on-a-phone.md`](../patterns/pictures-on-a-phone.md)).
`ReadingLines` is one column. Cards keep one line of state, and a table drops its secondary columns.
A log at the foot is a card per source into the Logs page. Nothing scrolls sideways.

## Checklist

- [ ] The page opens on an identity line, not a text title, and the first block under it is a
      picture or the thing itself.
- [ ] The identity line follows its rules: one row and one line of facts on a phone, with danger
      and warning facts first; the command an icon on a phone; no destructive verb in the brand
      face; `?` at the line's end, or on the back line where there is a back link.
- [ ] No row of tiles opens the page. Every figure is a fact, a node, a reading line, a band, a
      rank or a chart.
- [ ] Latency is in words, and every value goes through its one helper.
- [ ] Every section is a title and a hairline. A framed block says why.
- [ ] Things you open are `ChoiceRow`s, one height in every state. Nothing expands under a row.
- [ ] No sentence under a title. A verb is one word.
- [ ] Loading, empty, unavailable and failed are designed, and the page holds still through all
      four.
- [ ] One `h1`, sections as `h2`, every control named ([`../patterns/accessibility.md`](../patterns/accessibility.md)).
- [ ] At 390 on a touch device: one column, no sideways scroll, press boxes of 44px.

## The spec to add

A browser spec against fixtures, with these checks:

1. The identity line is found by its name ("About …") and says the thing's name, its state and its
   command.
2. The picture is there (`[data-slot=wire-flow]`), and the page has no `[data-slot=stat-tile]`
   where the standard gives it none.
3. A landmark below the first block sits at the same `top` before and after the reads land, at
   1440 and at 390.
4. A failed second read leaves the first reading on screen with *Not updating*.
5. At 390 the document is no wider than the window.

`tests/browser/deploy-project.spec.ts`, `deploy-header.spec.ts` and `docker-overview.spec.ts` are
the models. `design-system.spec.ts` runs over every page for the shared rules.

## 15. Redesigning a page

What "redesign this page with the design system" means, in the order to do it. The passes were settled
on the host Overview in 0.6.7. Since 0.9.0 the reference is a project's Overview (`/deploy/[id]`)
and the yardstick is [`../standard.md`](../standard.md), which wins wherever a pass below disagrees:
open the reference beside the page being redesigned and make the second one read like the first.

**The look in one sentence: readings on the page, not boxes on the page.** One dark ground. A hairline
where two things meet. Type doing the hierarchy. One blue. Motion only to say something arrived.
"Modern and clean" here means *fewer edges*, never more decoration — no gradients, no glow, no glass,
no shadows, no icon plates, no badges, no rounded cards floating over the ground.

**What the 0.8.7 run settled.** The run that brought every page to the deploy pages' look closed
questions these passes had left open. Each is written once, where the list points, and a page
redesigned now starts from them:

- **A page opens on an identity line in the project's layout**: `IdentityHeader` with a `state`, one
  `command` and a `menu` from `IdentityVerbs` (§14, *Any page can open as a project does*).
- **No row of tiles opens a page.** A figure that is a reading is a fact on that line, a node of the
  picture or a `ReadingLine` (§16, *A project's Overview reads in sentences*); pass 2 below says
  where a tile still stands.
- **A picture of things wired through one hub is `WireFlow`**, and it waits at its loaded shape
  (§2, the two paragraphs on it).
- **An item's detail is a page with a rail, not a sheet**: a route of its own on `DetailPage`, which
  is a way back, the identity line with its one command, the views as an underlined strip named in
  `?tab=`, and from `xl` the list it came from as a `Rail` with that list's own filter, Alt+↑ and
  Alt+↓ walking it with the view kept. The address that opened the sheet redirects to the page. A
  sheet stays for a small add or edit form ([`detail-with-rail.md`](detail-with-rail.md)).
- **Settings are one centred column** under one save bar, with a `PendingStrip` for what is stored
  and not in force; the same bar serves a setting on a reading page and a column beside a rail
  ([`settings-page.md`](settings-page.md)).
- **A thing that is not there has one screen**, `components/missing-thing.tsx`
  ([`../components/states.md`](../components/states.md#a-thing-that-is-not-there-has-one-screen)).
  The proxy's `EngineSetup` is that screen (`MissingThing size="page"` with the install as its
  step, the certbot option and the job console); a new page takes `MissingThing`.
- **On a phone a page of several panels is one pane at a time** over a bar at the foot
  (`components/phone-panes.tsx`; the pages on it are listed in `patterns/keyboard-and-place.md`).
- **A workbench runs to the sheet's edges and draws no frame**, the sheet being its frame; a
  Remote Desktop session was the first to (§16's table, [`../foundations/surfaces.md`](../foundations/surfaces.md)).
- **Every page of one database opens on the database's header**, with the exceptions decided in
  `shell/header-rule.ts`
  ([`../decisions/databases.md`](../decisions/databases.md#every-page-of-one-database-opens-on-the-databases-header)).

The passes, in order. Each one is a diff you can review on its own.

1. **Remove the frames.** Every `Panel` becomes `Panel plain` unless it is one of the exceptions in §7:
   a `Pane` (a working region with its own scrolling), a **table** (a region with its own scrolling
   that happens to sit in the page's flow — §2), a `Well` (output you read), a `Group` (a fence
   inside a body), or something that genuinely floats (popover, dropdown, dialog). "It is a card" is
   not a reason. A block that is the whole of a section is a title and a hairline. Two blocks side by
   side are both plain; the gap between them is the separation. A framed block that survives this pass
   has a sentence in a comment saying why.
2. **Figures are readings.** A figure the reader opens the page for is said, not tiled: a fact on
   the identity line (*9 containers running, 2 stopped*), a node of the picture, or a `ReadingLine`
   (a glyph on its measure's hue, the reading in words, its last hour at the line's end). No row of
   tiles opens a page (D4), and a project's Overview carries none at all. Before a figure moves, name
   where it goes and what now does the job it did, and say so in the page's doc comment, the way a
   surviving frame carries its sentence in pass 1.

   A `StatTile` stays only where a run of figures is read as a table (Metrics' five readings, the
   Docker overview's), and there each tile carries a sparkline or a strip and its tone: a `StatGrid`
   with hairlines between, nothing around, every tile the same inset. A tile that is a destination
   is a `Link` around the `StatTile` (`StatLink`, which drew that, went in 0.9.0 with no caller
   left), and the tile takes `className="h-full transition-colors group-hover:bg-row-hover"`. A tile whose figure is a question about what is under it — a
   log's "Auth failures 12" — is a `StatButton` instead: a press narrows the pane below rather than
   leaving the page, its revealed mark is the funnel every "only lines like this" carries, and
   `pressed` says the figure's filter is the one on screen (pressing it again lets it go). The figure
   is 24px (`text-2xl`): `text-xl` is not on the ladder. A state colours the figure through `tone`,
   never through a mark beside it.

   Where each page's figures went, page by page, is in [`../decisions/`](../decisions/README.md).

3. **Lists are rows — and a row you *take* is not a row you read.** `RowList`/`Row` for things with
   a title and a second line, `FindingList` for verdicts, a table for columns. Never a grid of framed
   cards standing in for rows. A scroll container that holds plain rows pads by the rows' bleed
   (`-mx-3 px-3`), or it grows a sideways scrollbar.

   **Then ask of every list on the page whether its rows are destinations.** A row that carries an
   `href` — a project to enter, a site to open, a stack to look inside — is a choice, and the last
   subsection of §16 gives it a `ChoiceList` of `ChoiceRow`s and the lit edge. A row that is a
   *reading* keeps its hairlines and its wash, and a table of readings is never touched.

   This is the pass that was missed everywhere, and the 2026-09-21 revamp exists because of it. The
   lit edge shipped with `/deploy/new` and stayed there: `ChoiceRow` appeared in three files, all of
   them under `new-project/`, so the git import panel was the only surface in the product that
   answered the pointer with an edge — and the complaint that arrived, that one screen had depth and
   the rest were flat, was a correct reading of exactly that. The proxy overview's sites, the Docker
   overview's compose projects and its idle containers were all `Row`s with an `href` on them. It is
   a numbered pass here rather than a sentence in §16 because a sentence in §16 is read by somebody
   redesigning a **flow** page, which is precisely the reader who does not need it.
4. **No decorative glyphs.** Headers, sections, tiles and modals carry no icon (§14). The glyphs that
   stay are wayfinding — the sidebar entry and a module tile's mark, drawn as a 12px `text-brand`
   glyph inline before the tile's eyebrow — and the ones that *are* the message: a `Status`, a
   `Notice`, an `EmptyState`, a verb on a button.
5. **Type on the ladder.** A flow question or headline figure 24 → identity name 16 → section head
   and surface title 15 → body 13 → hint 11 → micro 10 ([`../foundations/type.md`](../foundations/type.md)). Reading page names are accessible but not drawn. Anything in between is
   deleted, not rounded to the nearest.
6. **Colour by role.** Brand blue is a command's face or a location mark. Amber, red and green arrive
   only attached to a reading — a figure's `tone`, a `Status` dot and word, a `Notice`. Selection is
   `bg-accent`; hover is `bg-row-hover` on a row or tile and a border step on a framed destination;
   never both, never movement.
7. **Motion says one of four things** (§11). Add `animate-rise` where a fetch settles: the page once,
   each block once, a figure once (swap its `key`). `transition-colors` on hover. Nothing else moves.
8. **Data, not captions.** No sentence under a title (§5). What the reader needs is a `Tag`, a
   `Status`, a hint on a tile, or a `Notice`. What the page *is* — a hostname, a kernel, a platform —
   is its own row.

   A finding's consequence is measured data, so `FindingList` shows it below the problem's title
   before the evidence disclosure. Expanding shows the full evidence once rather than repeating
   the same text in the header and body. Its action stays visible beside the disclosure button,
   never nested inside it. Titles wrap; narrow panels put the action below the text using the
   container's width, including paired panels on a desktop. Attention lists retain critical and
   warning findings, with the first offered remedy using the command face (`emphasize`); diagnostic
   reports keep secondary actions. Routine preferences stay in their owning configuration or
   inventory. Findings rise when they arrive, and a poll never claims to be a live socket reading.
   Name the action and land on the actual resource or control; a review never silently executes a
   fix. Viewers get inspection or explicit administrator guidance, and async actions show progress
   and prevent repeat submission.
9. **Alignment.** Tiles top-align so a row of names is a row; hints truncate rather than wrap; every
   tile in a row, the first included, keeps the same inset.
10. **Verify.** `scripts/test-changed.sh` (it runs `tests/browser/design-system.spec.ts` for any
    UI change), then a screenshot at 1280 and 1720 wide against a mocked
    API in the pattern `mockShell` uses, and look at it: a scrollbar where none belongs, a label a
    line lower than its neighbours, a figure at the wrong size, are things the checks do not catch.

### Where the page's name and its controls go

Reading pages now keep their page name in a screen-reader-only `h1` through `PageContext`. The rail
provides the visible location. A linked parent remains as a compact way back (`back`), while pages
without one begin directly with their content. Put a control beside the data it changes: list commands in
their section header or filter bar, workbench commands in the workbench strip, and empty-list commands
in the empty state. The Metrics range, pause and export controls sit in the machine identity line;
the proxy service verbs sit with the engine facts. A detail page's verbs are on its identity line
(one command, the rest behind `⋯`), never beside the way back. The deployment fleet puts its related
pages and create command with the list filters; an empty fleet has its create command in the empty
state.
