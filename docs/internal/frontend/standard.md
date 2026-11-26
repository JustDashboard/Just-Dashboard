# The standard

What every page is held to. The operator's decisions come first, then the deploy standard (D1 to
D14), then the answers to the ten tensions the 0.9.0 analysis raised. Where any other page in these
docs disagrees with this one, this one wins, and the other page is the bug.

The deployment pages are the reference. The operator calls them "how I would like everything else":
a project's Overview (`/deploy/[id]`) for a reading page, its Runtime for a picture, its General and
Domains settings for settings, the fleet (`/deploy`) for a list, and a process's page
(`/processes/[pid]`) and a container's (`/docker/containers/[id]`) for a detail page. A flow page
takes `/deploy/new`.

Each rule says how it is held:

- **lint** `id`: an ESLint rule in `frontend/eslint-rules/design-system.mjs` (the plugin is
  `design-system/`; `tokens` rule ids are in brackets in its messages), or `jsx-a11y`.
- **spec** `file`: a browser spec under `frontend/tests/browser/`.
- **unit** `file`: a `bun test` file under `frontend/src/`.
- **type**: the type checker refuses it, because the prop does not exist.
- **convention**: a person or an agent holds it on review. Nothing checks it yet.

"Coming" names the 0.9.0 package that adds the check.

## The operator's decisions

These are requirements. Never undo one. If best practice argues with one, write a tension down (as
below) and let the operator decide; it is never a defect.

| Decision | What it means | Held by |
| --- | --- | --- |
| **One mode** | Dark only. No light palette, no theme switch. | convention |
| **Three inks** | `text-foreground`, `text-muted-foreground` and `text-faint`. Faint has a 4.5:1 floor. Text is never faded with opacity. | spec `design-system.spec.ts` (*faded secondary text keeps 4.5:1*, *no page fades its words with opacity*); coming: lint `ink-opacity` (WP-39) |
| **Colours are tokens** | Every colour is named once, on `:root` in `app/globals.css`. | lint `hardcoded-palette`, `tone-tint`; coming: lint `colour-var` (WP-39) |
| **Nothing lifts** | A surface separates by a step of ground and a border. Shadows only on popovers, menus, dialogs and the floating bars. A block is plain unless it can say why it needs an edge. A control's face carries one lit pixel along its top (`--edge`), no drop shadow. | convention |
| **The sheet is the frame** (24 Nov 2026) | From `md` up every page is a sheet with its own edge. Nothing inside it draws a frame along that edge: a workbench runs to the sheet's edges with hairlines between its panes, and a pane on a reading page keeps the page's gutter. A frame 12px inside the sheet is a frame in a frame. | spec `design-system.spec.ts` (*nothing on … draws a frame inside the sheet*); [`foundations/surfaces.md`](foundations/surfaces.md) |
| **One blue, three jobs** | The brand blue is a command's face, where you are, and a location mark. Status has its own three hues. | convention |
| **No descriptions under titles** | `PageContext`, `Section`, `PanelHeader` and `ChartPanel` have no `description`. | type |
| **A verb is one word. Few words.** | D14. When the operator says "too much whitespace", cut content; do not rearrange it. | convention |
| **No colour bars** (19 Nov 2026) | No thin vertical bar or rule in a colour beside a name, down a row's edge, or on the current row: not the rail's current page, not a band's line, not a stack, service or network hue, not a staged change, a log level or an error. The fill, the word, the coloured figure or a wash says it. A chart whose lines are told apart only by colour keys them with a round dot. | spec `shell-phone.spec.ts` (the rail's current row has no `::before` bar); convention for the rest |
| **Menus are words** (24 Nov 2026) | A `⋯` menu says each verb as its word, without a glyph: "use icons only for the super necessary menus". A colour's swatch, a tick and a danger verb's ink stay, being what the item says. Done on the Databases control center (`VerbMenu glyphs={false}`: a card's menu, the identity line's, a found row's) and the GitHub page (`/github`: the identity line's menu and a repository card's, 25 Nov 2026); the product's other menus still draw glyphs until their pages are passed. | spec `database-control-center.spec.ts` (a card's menu draws no glyph), `github-ui.spec.ts` (a repository card's menu draws no glyph); convention for the rest |
| **No arrow on a card** (24 Nov 2026) | The Databases control center's cards and its found rows carry no arrow at their end, which D5 asks for elsewhere: the card and its name open it, and a found row's verb is drawn as a small button's face (`ChoiceRow arrow={false}`). The fleet's project cards (`/deploy` and the Overview) dropped theirs on 25 Nov 2026, and so did the GitHub page's repository cards and its pull request and issue rows when it was redrawn that day. D5's arrow stands on the other pages until the operator says otherwise. | spec `deploy-projects.spec.ts` (a project card draws no arrow); convention for the rest |
| **List rows never grow** | A row is one height in every state. Nothing expands under it. Details go in a popover or a page. | spec `design-system.spec.ts` (*nothing on … moves while its reads land*, *… first row stands where it lands*); convention |
| **The identity line** (16 Nov 2026) | A phone's header is one row and one line of facts, danger and warning facts first. A command on a phone is its icon. No destructive verb takes the brand face: it goes behind the `⋯`. `?` ends the identity line, and has a row only where a back link shares it. | [`components/page-header.md`](components/page-header.md#the-identity-lines-rules); spec `deploy-header.spec.ts` (one row on a phone); unit `identity-header.test.js`; convention for the rest |
| **No metric cards on a project's Overview** | That page is a real logo, a sentence and one coloured picture. | spec `deploy-project.spec.ts` (no `stat-tile` on the Overview) |
| **Canvases move like Excalidraw** | Trackpad pans, pinch zooms, a mouse drags, frames drag. Nothing changes size with zoom. | unit `canvas-move.test.js` |
| **The assistant feels like a chat** | Answers like a chat, tools only when needed, a round arrow send, full pages over sidebars. | convention |
| **Each page declares one register** | Reading or flow ([`patterns/registers.md`](patterns/registers.md)). | spec `design-system.spec.ts` (*every page declares one register*) |
| **Bun only, Prettier, comments say why** | Prettier with no semicolons, double quotes, 100 columns. New dependencies need a licence check. | `scripts/test-changed.sh` (Prettier, ESLint); convention for the licence |

## The deploy standard, D1 to D14

Copied from the 13 Nov 2026 UI map (`tools/rubric.md`), which rated every page against it. A
workbench (the terminal, Files, a database's Data, Query, Schema and Diagram) is one surface held
to the window, the sheet its frame: judge it on D3, D5, D11, D12, D13 and D14, and on whether its chrome matches the Git
workspace, the server map and the assistant. Say which traits do not apply rather than failing them.

| Id | Trait | At the standard | Not | Held by |
| --- | --- | --- | --- | --- |
| D1 | **Identity header** | The page opens on what it is about: a logo tile, the name, one line of live facts (source, branch, release, a state dot and word), verbs at the far right (one brand-faced command, the rest in a `⋯` menu). | A bare text title, no header, or a toolbar of equal grey buttons. | spec `design-system.spec.ts` (*… keeps the identity line's rules*, at 1440 and on a touch phone); page specs check their own line (`network-overview-firewall.spec.ts`, `docker-overview.spec.ts`) |
| D2 | **A picture first** | The first block is a picture of the thing: a wiring picture (`WireFlow`, `WireNode` and `WireMark` with beams) of what is connected to what, or the thing itself (the site preview). | A row of figures or a table first. | unit `wire-flow-model.test.js` (the picture's shape); convention |
| D3 | **Colour and life** | Real product logos (`ProductLogo`, `ProductGlyph`), a status dot with its word, a hue per measure with a sparkline, strips of recent runs, a beam or shimmer on work in flight. | A grey page of text, hairlines and numbers. | unit `product-logo.test.js`; convention |
| D4 | **Readings in words** | A reading is a sentence beside a mark ("16 known vulnerabilities in release 2"), or a `ReadingLine`: a glyph, a name, the reading and its last hour. Where a figure stays a tile it carries a sparkline or a strip and a colour. | A `StatGrid` of bare grey `StatTile`s opening the page. | spec `deploy-project.spec.ts` and the deploy page specs (no `stat-tile`); convention elsewhere |
| D5 | **Things you open are cards** | One bordered, rounded row per thing (`ChoiceRow` in a `ChoiceList`): logo tile, name over a mono line, state in aligned columns, an arrow at the end, a `⋯` menu. One height in every state. | A plain `RowList` or table row that happens to be clickable; a row that expands in place. | convention |
| D6 | **Sections are a title and a hairline** | Title left, a count or small fact right, the section's one verb as a small outline button on the title line. No box around the section. | Framed `Panel`s around ordinary sections; panels inside panels. Frames are for tables, panes, wells and the danger zone. | spec `design-system.spec.ts` (*a settings page's sections are the level under its title*); convention |
| D7 | **Tables only for readings** | A table where rows are read down their columns; one line per row; framed; the name is the button. | A table for three items; rows that are really destinations; wrapped two-line cells. | convention |
| D8 | **Settings as a form you read** | One centred column (about 770px), sections with a title, a facts line with marks, then fields; switches as full-width rows on hairlines; `Segments` for small choices; a picture where the setting has a shape; one floating save bar; a strip of what is pending. | Framed cards side by side, a Save button per card, explanatory paragraphs. | spec `deploy-settings-a.spec.ts`, `deploy-settings-b.spec.ts`; convention |
| D9 | **Detail is a page** | An item's detail is its own route: header, views in the address (`?tab=`), a rail of its siblings from `xl`. A sheet is fine for a small add or edit form. | Rich detail in a `SidePanel` over the list. | spec `processes-ui.spec.ts`, `network-device-page.spec.ts`, `container-page.spec.ts` (the old address lands on the page, the rail); unit `rail.test.js`, `detail-page.test.js` (the one `Rail` and `DetailPage`) |
| D10 | **Bands, ranks and charts** | A band is a `BarReading`; a ranked list is a `RankList` with figures in aligned columns; latency in words ("19 in 20 inside 412ms"); a chart has a Min / Mean / Max / Now line under it and a hue per series. | `BarList`, rows under a bar, p50 / p95, an unlabelled grey chart. | lint `design-system/latency-words`, `design-system/primitives` (`[bar-list]`); unit `bar-reading-model.test.js` |
| D11 | **Holds still** | The page has its final shape while loading; rows and bands keep one height in every state. | Blocks that grow as data lands, spinners that collapse, layout shift. | spec `design-system.spec.ts` (*nothing on … moves while its reads land*, layout shift under 0.02, *… first band stands where it lands*) |
| D12 | **One pane on a phone** | At 390px a page of several panels shows one full-height pane over a bar at the foot; lists become cards; nothing is cut off. | Panels stacked into small scrolling boxes; a table scrolled sideways for its main reading. | unit `phone-panes.test.js` (no hand-drawn foot bar); convention |
| D13 | **Designed empty and unavailable states** | A picture or logos, one sentence, the next step as a button. | A grey sentence, a raw daemon error. | unit `missing-thing-model.test.js`; convention |
| D14 | **Few words** | No sentence under a title, a verb is a word, mono for names and addresses. | Paragraphs under headings, helper text on every field. | type (no `description` props); convention |

## Tensions, and the operator's answers (15 Nov 2026)

Each is a best-practice change that touches a decision above. The operator answered all ten.

| Id | Best practice asked for | Answer | Where it lives |
| --- | --- | --- | --- |
| T1 | Raise 10px and 11px text on touch screens | **Adopted, then folded into the scale** (0.9.1): every step went one pixel up on every pointer, so `text-hint` is 12px and `text-micro` 11px on a desk too. | [`foundations/type.md`](foundations/type.md) |
| T2 | A way to pause endless live motion (WCAG 2.2.2) | **Adopted.** `data-motion="paused"` on `<html>` pauses every animation, as `prefers-reduced-motion` does. The control is Pause animations in the account menu, kept per browser. | [`foundations/motion.md`](foundations/motion.md) |
| T3 | 3:1 edges on controls (WCAG 1.4.11) | **Adopted.** Inputs, selects, checkboxes and switches draw a 3:1 edge in the control-edge colour. Other borders are unchanged. | [`foundations/colour.md`](foundations/colour.md) |
| T4 | Sortable tables for compare-and-sort lists | **Not adopted.** Things you open are cards. Ports becomes a `ChoiceList` with sort in its toolbar. | D5, D7 |
| T5 | Expandable rows | **Not adopted.** List rows never grow. | the decision above |
| T6 | Percentiles for latency | **Not adopted.** Latency stays in words; the exact figure can sit in an `Explain`. | D10 |
| T7 | Help text under risky settings | **Not adopted.** No help text under titles. The reasoning goes in an `Explain`; a confirmation names its object. | D14 |
| T8 | Autosave for single toggles | **Not adopted.** Settings keep one save bar. | D8 |
| T9 | Move `--chart-5` off the success hue | **Adopted.** `--chart-5`, the RX colour of every in/out pair, moves to a cyan near hue 200. | [`foundations/colour.md`](foundations/colour.md) |
| T10 | Keyboard alternatives on canvases (WCAG 2.5.7) | **Adopted.** Arrow keys slide the server map, the databases map, a database's Diagram and a drawing, and move what Enter or a click picked on them. | [`foundations/layout-phone.md`](foundations/layout-phone.md) |

## What the linter refuses today

There is one checkbox and one spinner: `Checkbox` (`components/ui/checkbox`) and `Spinner`
(`components/loading.tsx`). `design-system/primitives` refuses a native `<input type="checkbox">`
and a hand-spun `LoaderCircle` outside `components/ui`, `state.tsx`, `loading.tsx` and `icons.tsx`, an import
of `BarList` anywhere but the Query page's Chart view, and a `Field` of several controls that is not
a `group` (`field-group`), whose controls would each take its one id. `design-system/tokens` refuses
`type-scale` (a `text-[Npx]`), `tone-tint`, `radius-bare`, `radius-scale`, `focus-ring` (a
hand-written focus ring), `row-reveal` (a hand-written hover reveal) and `hardcoded-palette`.
`design-system/latency-words` refuses p50, p95 and p99 in text. `eslint.config.mjs` holds every
`.tsx` to `jsx-a11y`'s recommended rules and lets only `Modal`, `PaletteModal` and `SidePanel`
import the raw `Dialog` and `Sheet`.
