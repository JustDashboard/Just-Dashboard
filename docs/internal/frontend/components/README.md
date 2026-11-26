# Components

The catalogue: every shared export, so a page can find what to use in five minutes. Shared means
`components/*.tsx`, `components/ui/`, `components/wire/`, `components/settings/`,
`components/shell/` and `hooks/`; a component of one area (`components/docker/`, …) is that area's
and is found from [`../features/`](../features/). A few exports outside those folders have rows
too: the ones another area is meant to reuse (`account/one-time-code.tsx`,
`onboarding/purpose-card.tsx`, `lib/password.ts`), and the area copies a shared export
replaced, as deprecated rows. Each family's own page carries the detail:
anatomy, when to use it and when not, props, states, the phone and accessibility.

How to read it:

- **One row per export**, except where several are the parts of one thing: a Radix primitive's
  parts, a model file's helpers, a component and its run (`RowList`, `Row`). Those share a row and
  every name in its first cell is an export. A file's exported types sit in the row of what they
  describe, marked *(type)*. The icons are listed under the table.
- **Status** is where the export stands. *keep*: use it. *deprecated (use X)*: still exported so
  its callers keep working, and deleted once they have moved; a new page takes X. *retired*: gone;
  the row says what does its job. *new (…)*: written ahead of the change that adds it, and the
  only status besides *retired* whose names need not be exports yet; it becomes *keep* when the
  change lands.
- `src/docs-catalogue.test.js` fails when a shared export has no row, when a row names something
  that is not an export, and when any page here names a component the code does not have. Its
  baseline (`src/docs-catalogue.baseline.json`) is empty and stays empty.

Looking for a shape rather than a name? Start from the families below, or from the recipe for the
page you are building ([`../recipes/`](../recipes/README.md)).

## Catalogue

| Export | Family | File | One line | Status |
| --- | --- | --- | --- | --- |
| `HostIdentity` | [Page header](page-header.md) | `components/identity-header.tsx` | The identity line: a mark, the title, its state and a line of facts. | deprecated (use `IdentityHeader`) |
| `IdentityHeader` | [Page header](page-header.md) | `components/identity-header.tsx` | `HostIdentity` under its own name, with a `loading` form that stands at the loaded shape. | keep |
| `platformName` | [Page header](page-header.md) | `components/identity-header.tsx` | The distribution as its own name spells it, for an identity line. | keep |
| `PageContext` | [Page header](page-header.md) | `components/page.tsx` | A page's accessible name, its way back (`back={{href, label}}`), the walk and the help, with no visual title; a thing's verbs are on its identity line. | keep |
| `BackLink`, `Back` (type) | [Page header](page-header.md) | `components/page.tsx` | The one way back to the list a page sits under ("← Containers"), drawn by `PageContext back`, `FlowHeader`, `DetailPage` and, outside them, on its own. | keep |
| `IdentityVerbs` | [Page header](page-header.md) | `components/identity-header.tsx` | Splits a thing's verbs into its identity line's one command (its `short` word where given) and the `⋯` menu after it: `{...IdentityVerbs({ verbs, name })}`. | keep |
| `Metric`, `MetricStrip` | [Facts lines](values-facts.md) | `components/detail-list.tsx` | One figure with its name, and a run of them; they draw as `FormFact` and `FormFacts` now. | deprecated (use `FormFact`, `FormFacts`) |
| `DetailList`, `Detail` | [Facts lines](values-facts.md) | `components/detail-list.tsx` | Label and value pairs in a stacked list; `ruled` draws a rule between pairs, `labelWidth` sets the label column and `size` the type. | keep |
| `FactLine`, `Fact`, `facts`, `hasFacts` | [Facts lines](values-facts.md) | `components/fact-line.tsx` | A line of facts with the dot between them drawn once, in the faint ink; `facts` makes one from data, and `hasFacts` says whether children are written as facts. | keep |
| `HostFact` | [Facts lines](values-facts.md) | `components/identity-header.tsx` | A fact in the identity line with the product it names drawn before it. | deprecated (use `Fact`) |
| `FactDot` | [Facts lines](values-facts.md) | `components/fact-line.tsx` | The dot between two facts of a line, as an item of its own (`identity-header.tsx` re-exports it). | deprecated (use `FactLine`) |
| `FactsLine` | [Facts lines](values-facts.md) | `components/settings/facts-line.tsx` | A settings section's facts line, with the paragraph it used to print behind a mark. | keep |
| `SectionLink` | [Section head](section-head.md) | `components/section-link.tsx` | The way on to the page a block stands for, at the end of its head. | keep |
| `Section` | [Section head](section-head.md) | `components/section.tsx` | A titled group of blocks inside a page: the title and a hairline. | keep |
| `Page` | [Surfaces](surfaces.md) | `components/page.tsx` | The frame every page renders into: one measure, gutter and rhythm; `fill` for a page whose content is the viewport. | keep |
| `WORKBENCH_WAITING` | [Workbench](workbench.md) | `components/page.tsx` | A workbench page's loading state, as the class list of a `LoadingPanel plain`: the surface's ground with rows, no frame against the sheet's edge. | keep |
| `Panel`, `PanelHeader`, `PanelToolbar`, `PanelBody`, `PanelFooter` | [Surfaces](surfaces.md) | `components/panel.tsx` | The content block: a header on its own ground and a hairline; `plain` drops the frame. `PanelHeader` takes a `count`, a heading `level` and a `link` slot. | keep |
| `Pane`, `PaneHeader`, `PaneFooter` | [Surfaces](surfaces.md) | `components/panel.tsx` | The framed working region of a page (a rail, a tree, a console), its strips tinted. | keep |
| `PaneSection` | [Surfaces](surfaces.md) | `components/panel.tsx` | An eyebrow-titled section inside a pane, a hairline under each. | keep |
| `Well` | [Surfaces](surfaces.md) | `components/panel.tsx` | A recessed ground inside a body, for a value set apart. | keep |
| `Group` | [Surfaces](surfaces.md) | `components/panel.tsx` | A fenced group of fields inside a body. | keep |
| `Separator` | [Surfaces](surfaces.md) | `components/ui/separator.tsx` | A hairline between two things. | keep |
| `SearchInput` | [Search and toolbar](search-toolbar.md) | `components/search-input.tsx` | The one filter box: 44px and its own line on a phone; with a deferred apply (`onApply`) and a clear. | keep |
| `SearchInputSkeleton` | [Search and toolbar](search-toolbar.md) | `components/search-input.tsx` | The search box's bone at the box's own size. | keep |
| `Toolbar` | [Search and toolbar](search-toolbar.md) | `components/toolbar.tsx` | A row of filters and actions standing on its own, outside a panel. | keep |
| `ListToolbar` | [Search and toolbar](search-toolbar.md) | `components/toolbar.tsx` | A list's search, sort, actions and chips in one order with one phone layout. | keep |
| `useDeferredSearch`, `receiveApplied` | [Search and toolbar](search-toolbar.md) | `hooks/use-deferred-search.ts` | Applies a typed search after a pause, without echoing the applied value back; `receiveApplied` is the echo guard as a pure step. | keep |
| `FindingList`, `FindingAction` (type), `Finding` (type) | [List row](list-row.md) | `components/finding-list.tsx` | Results of a diagnostic tool: the problem, its consequence and the next step, the evidence in a popover off the title (rows never grow). | keep |
| `RowLink` | [List row](list-row.md) | `components/row-link.tsx` | A table cell's primary column as the row's way into its detail page. | keep |
| `RowList`, `Row` | [List row](list-row.md) | `components/row-list.tsx` | The list of rows you read: a mark, a title, a second line and the right edge, one height in every state. | keep |
| `ROW_BLEED` | [List row](list-row.md) | `components/row-list.tsx` | The bleed a row takes inside a plain panel, for a list laid out by hand. | keep |
| `RowsSkeleton` | [List row](list-row.md) | `components/row-list.tsx` | Rows that have not arrived, at a `Row`'s own pitch, so a list is as tall loading as loaded. | keep |
| `TABLE_BLEED` | [List row](list-row.md) | `components/row-list.tsx` | The bleed a table takes inside a plain panel, so its first column starts on the panel title's edge. | keep |
| `useColumnWidth` | [List row](list-row.md) | `hooks/use-column-width.ts` | The width a list is drawn in, for a row that sets its readings beside its name or under it. | keep |
| `ChoiceList`, `ChoiceRow` | [Card row](card-row.md) | `components/choice-row.tsx` | The card row you take: a project to enter, a site to open; the arrow always drawn. | keep |
| `GroupRule` | [Card row](card-row.md) | `components/choice-row.tsx` | What the next run of cards is and how many, as a rule over them. | keep |
| `CONTROL` | [Card row](card-row.md) | `components/choice-row.tsx` | The selector for anything inside a card that owns its own press. | keep |
| `ChoiceListSkeleton` | [Card row](card-row.md) | `components/choice-row.tsx` | Card rows that have not arrived, at the card's own box. | keep |
| `useChoicePress`, `choicePress`, `ChoicePress` (type) | [Card row](card-row.md) | `components/choice-row.tsx` | A card's press: skips inner controls and a text selection, and opens a new tab on a modified click; `choicePress` is the rule as a pure step. | keep |
| `CHOICE_ROW_BOX` | [Card row](card-row.md) | `components/choice-row.tsx` | A `ChoiceRow`'s box inside its edge, shared with its skeleton so both are 58px. | keep |
| `ChoiceGrid`, `ChoiceCard`, `ChoiceCardTitle`, `ChoiceCardHint` | [Choice card](choice-card.md) | `components/choice-card.tsx` | A block you pick one of, in a grid of choices. | keep |
| `ProductCard` | [Choice card](choice-card.md) | `components/choice-card.tsx` | One product as a picker of products draws it: its logo, name and a line. | keep |
| `PurposeCard`, `ExperimentalMark` | [Choice card](choice-card.md) | `components/onboarding/purpose-card.tsx` | One purpose the server is for, as a card pressed to take or leave it, and what an experimental one says on it. | keep |
| `OpenCard` | [Card row](card-row.md) | `components/open-card.tsx` | The press-anywhere card shell under a repository, a drawing or a fleet card. | keep |
| `DetailPage`, `StepArrows`, `useSiblingSteps`, `siblingSteps`, `stepCommands`, `useDetailTab`, `Step` (type) | [Rail](rail.md) | `components/detail-page.tsx` | An item's detail page: the rail, the way back, the steps to its neighbours (Alt+↑ and Alt+↓, the arrows on a phone) and the `?tab=` views. | keep |
| `BackLink` | [Page header](page-header.md) | `components/detail-page.tsx` | The re-export where a copy was drawn; `BackLink` from `components/page.tsx` is the one. | retired (0.9.0) |
| `Rail`, `RailColumn`, `RailRow`, `RailName`, `RailLine`, `RailFigures`, `RailFigure`, `RailAction`, `RailNote`, `RailGhosts` | [Rail](rail.md) | `components/rail.tsx` | The sibling list beside a detail page from `xl`: its column, a row and its two lines and readings, the caption line's action, the line in place of rows, and the rows it waits on. | keep |
| `railRowClass`, `railInk` | [Rail](rail.md) | `components/rail.tsx` | A rail row's one 48px class, and a reading's tone as its ink, for a row drawn by hand. | keep |
| `DockerRail`, `DockerRailRow` | [Rail](rail.md) | `components/docker/item-rail.tsx` | The Docker item pages' rail; `Rail` and `RailRow` draw it. | retired (0.9.0) |
| `DetailShell`, `DetailRail`, `RailRow`, `steps` | [Rail](rail.md) | `components/network/traffic/detail-shell.tsx` | The network detail pages' old frame; `DetailPage`, `Rail`, `RailRow` and `useSiblingSteps` do its job. | retired (0.9.0) |
| `FilterChip`, `ChipStrip`, `ChipCount` | [Tabs, chips, segments](tabs-chips-segments.md) | `components/chips.tsx` | A filter chip, the run chips sit in (scrolling on a phone), and a count beside a label. | keep |
| `Segments`, `SegmentOption` (type), `SegmentsSize` (type) | [Tabs, chips, segments](tabs-chips-segments.md) | `components/segments.tsx` | A closed choice of two to five short words as one segmented control. | keep |
| `tabClasses` | [Tabs, chips, segments](tabs-chips-segments.md) | `components/tabs.tsx` | The quiet tab's look (Just Hub's: the chosen one on a control face); the query editor's tabs keep it for a control of their own. | deprecated (use `ViewTabs`) |
| `ViewTabs`, `TabView` (type) | [Tabs, chips, segments](tabs-chips-segments.md) | `components/tabs.tsx` | The one strip of views: Radix tabs that scroll on a phone, with counts and dots. | keep |
| `ViewTabList` | [Tabs, chips, segments](tabs-chips-segments.md) | `components/tabs.tsx` | The strip alone, inside a `Tabs` the caller holds, whose `TabsContent` are the panels. | keep |
| `Tabs`, `TabsList`, `TabsTrigger`, `TabsContent`, `tabsListVariants` | [Tabs, chips, segments](tabs-chips-segments.md) | `components/ui/tabs.tsx` | Radix tabs; `TabsList` is the Hub's segmented track inside a form, and a page's views are `ViewTabs`. | keep (under `ViewTabs`) |
| `ToggleGroup`, `ToggleGroupItem` | [Tabs, chips, segments](tabs-chips-segments.md) | `components/ui/toggle-group.tsx` | A Radix toggle group; a single choice is a `Segments`, which draws with it. | keep (inside `Segments` only) |
| `Toggle`, `toggleVariants` | [Tabs, chips, segments](tabs-chips-segments.md) | `components/ui/toggle.tsx` | A Radix toggle; a single choice is a `Segments`. | keep |
| `WindowPicker` | [Tabs, chips, segments](tabs-chips-segments.md) | `components/window-picker.tsx` | The time window over a chart, on `Segments`. | keep |
| `useViewTab`, `pickView`, `viewSearch`, `ViewTabOptions` (type) | [Tabs, chips, segments](tabs-chips-segments.md) | `hooks/use-view-tab.ts` | The view a page shows, held in `?tab=`; `pickView` and `viewSearch` are its rules as pure steps. | keep |
| `Bone` | [States](states.md) | `components/bone.tsx` | A bone in a line of words that keeps the height the words will give it. | keep |
| `Spinner` | [States](states.md) | `components/loading.tsx` | A small turning mark for a wait too short for a skeleton. | keep |
| `LoadingRows`, `LoadingPanel` | [States](states.md) | `components/loading.tsx` | Skeleton rows for a list whose shape is not known yet, and a panel's skeleton at the panel's height. | keep |
| `Hold`, `HOLD_AT_MOST_MS` | [States](states.md) | `components/loading.tsx` | Keeps what is under a block off the page until the read above it answers, so nothing moves. | keep |
| `nearestPage`, `addressTail`, `NearestPage` (type) | [States](states.md) | `components/missing-page-model.ts` | The not-found page's nearest page and the address as its picture says it. | keep |
| `MissingPage` | [States](states.md) | `components/missing-page.tsx` | An address no page claims, as a picture of the address and the pages that are there. | keep |
| `noSystemd`, `moduleOff`, `missingFromError`, `absentFromError`, `unreadLine`, `saysDockerUnreachable`, `logKindMissing`, `SYSTEMD_RUN_DIR`, `SYSTEMD_MOUNT`, `MissingStep` (type), `Missing` (type), `MissingReason` (type), `SystemdReader` (type), `Refusal` (type), `MissingLineWords` (type) | [States](states.md) | `components/missing-thing-model.ts` | The words of every absence, so a missing systemd or a module switched off reads the same everywhere. | keep |
| `MissingThing`, `MissingScreen`, `MissingOrError`, `MissingLine`, `MissingGain` (type) | [States](states.md) | `components/missing-thing.tsx` | A thing that is not there, drawn as itself with the next step; whole, from a model, from an error, or as one line. | keep |
| `PageState` | [States](states.md) | `components/page.tsx` | A page that has not got its data yet, or cannot get it, in the shell's frame. | keep |
| `InstallHandoff`, `InstallFollowUp`, `HandoffPhases`, `usePackageKnown`, `PackageKnown` (type) | [States](states.md) | `components/install-handoff.tsx` | A section whose tool is not installed: what it would do, drawn through `MissingThing`, its install streaming under the button, and the phases after. | keep |
| `Stale`, `agoFrom` | [States](states.md) | `components/stale.tsx` | A reading that stopped updating: a mark beside it or a strip with Try again; `agoFrom` says how long ago. | keep |
| `StaleNotice` | [States](states.md) | `components/stale.tsx` | The last good reading's age and a refresh, over a page whose read now fails. | keep |
| `StandaloneState`, `RenderFailure` | [States](states.md) | `components/standalone-state.tsx` | A whole window with no shell: sign-in's failures and a render error above the shell. | keep |
| `EmptyState` | [States](states.md) | `components/state.tsx` | An empty list, drawn as what it would hold, with the next step. | keep |
| `EmptyNote` | [States](states.md) | `components/state.tsx` | "Nothing here" inside something already: a rail, a filtered body; takes an `action` and `dense`. | keep |
| `ErrorState` | [States](states.md) | `components/state.tsx` | A failed read, said calmly when it is an absence; `what` names the thing, `every` the cadence, and the retry shows it is pending. | keep |
| `Notice` | [States](states.md) | `components/state.tsx` | A short banner for a fact the reader needs before acting on the page. | keep |
| `NeedsAdmin`, `needsVerb` | [States](states.md) | `components/state.tsx` | The closed door under a page's identity line for a page only an administrator opens. | keep |
| `Sweep`, `SWEEP_STILL` | [States](states.md) | `components/loading.tsx` | The one indeterminate bar: a third of a track sweeping across, and the one way it holds still. | keep |
| `Skeleton` | [States](states.md) | `components/ui/skeleton.tsx` | The one skeleton fill. | keep |
| `OutcomeStrip`, `OUTCOME_FILL`, `OutcomeTone` (type), `Outcome` (type) | [Status and tags](status-tag.md) | `components/outcome-strip.tsx` | The last few attempts at something as a square each in how it ended, oldest first. | keep |
| `StateMark`, `StateCorner` | [Status and tags](status-tag.md) | `components/product-logo.tsx` | A product's tile with its status dot in the corner, on one size ladder. | keep |
| `Status`, `DotTone` (type), `Verdict` (type) | [Status and tags](status-tag.md) | `components/status-dot.tsx` | The one live-state indicator: a coloured dot and its word. | keep |
| `StatusDot`, `CornerSize` (type) | [Status and tags](status-tag.md) | `components/status-dot.tsx` | The dot alone, for a place that already says the state; `corner` sets it in a mark's corner. | keep |
| `StatusWords` | [Status and tags](status-tag.md) | `components/status-dot.tsx` | A state in words in a polite live region that is not drawn, for a header or a console whose state is not a `Status`. | keep |
| `toneFor`, `DOT_COLOR` | [Status and tags](status-tag.md) | `components/status-dot.tsx` | Maps docker, systemd and pm2 states onto one tone, and each tone's fill as a colour; re-exported from `tone.ts`. | keep |
| `Tag` | [Status and tags](status-tag.md) | `components/tag.tsx` | Small caps for a fixed property of a row; no chip, no border; `hue` colours it. | keep |
| `Tone` | [Status and tags](status-tag.md) | `components/tone.ts` | The four signal tones (default, success, warning, danger) a coloured state is said in. | keep |
| `DotTone` (type), `Verdict` (type), `OutcomeTone` (type), `toneFor`, `DOT_TONE`, `DOT_COLOR`, `TEXT_TONE`, `ICON_TONE`, `TONE_DOT`, `VERDICT_TONE`, `OUTCOME_FILL` | [Status and tags](status-tag.md) | `components/tone.ts` | Every state's dot, word, icon and outcome colour, and the maps between tones and states, in one file; `status-dot.tsx` and `outcome-strip.tsx` re-export theirs. | keep |
| `StateTile` | [Status and tags](status-tag.md) | `components/network/diagnostic-marks.tsx` | A network tile with its state in the corner; `StateMark` in `product-logo.tsx` is it. | retired (0.9.0) |
| `DOT` | [Status and tags](status-tag.md) | `components/network/egress/marks.tsx` | The egress marks' tone-to-dot map. | deprecated (use `TONE_DOT`) |
| `Ago` | [Values](values.md) | `components/ago.tsx` | A relative time that keeps itself current ("checked 4 min ago"). | keep |
| `ClientMark`, `BadgedLogo` | [Values](values.md) | `components/client-mark.tsx` | What signed a session in as the product it is, and a tile with a second product in its corner. | keep |
| `NetworkFact`, `NETWORK_GLYPH` | [Values](values.md) | `components/client-mark.tsx` | Where an address is (the tailnet drawn as Tailscale) and the address itself. | keep |
| `CodeBlock`, `CodeLines`, `gutterCh` | [Values](values.md) | `components/code.tsx` | Text kept as it was written, in a recessed block with its name, line count and copy; numbered lines in the faint gutter; and the gutter's width in `ch`, as wide as the last number. | keep |
| `CODE`, `ShellWords`, `shellWordHue`, `JsonCode` | [Values](values.md) | `components/code.tsx` | The one code palette (the `--tag-*` rung, none of the status hues), a command and a record coloured in it, and the hue of one word of a command. | keep |
| `DOCKERFILE_HUE`, `dockerfileLines`, `tokenDockerfile`, `DockerfilePieces` | [Values](values.md) | `components/code.tsx` | A Dockerfile as lines of pieces (the one tokeniser, which keeps a continued line in its instruction), and a line's pieces drawn in the code palette. | keep |
| `Digest` | [Values](values.md) | `components/code.tsx` | An image or content digest as twelve hex characters in mono, the whole of it on hover and a press away. | keep |
| `Cidr`, `AddressLine` | [Values](values.md) | `components/address.tsx` | An address in CIDR form with its prefix in the port hue (a host route's prefix left off: /32 for IPv4, /128 for IPv6; an IPv6 /32 is a network and keeps it), and a device's addresses on one line, global ones first. | keep |
| `Cidr`, `AddressLine` | [Values](values.md) | `components/network/address.tsx` | The re-export from where they were written; they live in `components/address.tsx`. | retired (0.9.0) |
| `DiffView`, `DIFF_WASH` | [Values](values.md) | `components/diff-view.tsx` | A unified diff in git's colours, the one renderer for the git page, the terminal's companion and the editor's review; `lineNumbers` adds the two gutters, `selection` ticks lines to stage. `DIFF_WASH` is the faint wash behind an added or a removed line, for anything else that shows a change as a diff does (a release's changed settings). | keep |
| `FileIcon`, `FolderIcon`, `fileKind`, `folderKind`, `kindOfEntry`, `FileKind` (type) | [Values](values.md) | `components/file-icon.tsx` | A file or a folder drawn as what it is: the icon for one entry, a folder alone, and the kind a name reads as (the whole name, then the extension, then the extension under a backup suffix). | keep |
| `FolderColourProvider`, `useFolderColour`, `FolderSwatch`, `folderColourOf`, `defaultFolderColour`, `validFolderColour`, `FOLDER_COLOURS`, `FOLDER_COLOUR_NAMES`, `FolderColour` (type) | [Values](values.md) | `components/file-icon.tsx` | A folder's label colour: the labels a tree is drawn under, the colour one folder takes (its own, else the global choice, its kind, then blue), and the swatch a colour menu offers. | keep |
| `Markdown`, `MarkdownFiles` (type) | [Values](values.md) | `components/markdown.tsx` | A description or a comment drawn as the Markdown it was written in, on this product's type ladder; `repoUrl` links `#123` and `@name` to the forge, `code` draws a fenced block the caller's way, and `files` reads a file on this server, whose scheme-less references are the files beside it. | keep |
| `MediaImage`, `MediaGrid`, `MediaLightbox` | [Values](values.md) | `components/markdown-media.tsx` | A picture or video from a Markdown body, served through this server; pictures alone in a paragraph side by side; and the lightbox that steps through a body's media. | keep |
| `CopyButton`, `CopyAction`, `CopyValue` | [Values](values.md) | `components/copy.tsx` | Copying: a button that says Copied, an icon action that ticks, a mono value with its copy. | keep |
| `DiffCount` | [Values](values.md) | `components/diff-count.tsx` | Lines added and removed, in git's two colours. | keep |
| `InOut`, `RX`, `TX`, `formatRate`, `axisRate`, `inOutSeries` | [Values](values.md) | `components/in-out.tsx` | In and out as one pair in their two hues, and the rate formatters every network figure takes. | keep |
| `Keycaps`, `ShortcutList`, `SingleKeySwitch`, `KEYCAP`, `keyWays`, `useLauncherKeys`, `KeyPiece` (type), `ShortcutGroup` (type) | [Values](values.md) | `components/keycaps.tsx` | A shortcut as one cap per key, Ctrl or ⌘ by platform and hidden on touch unless `touch="show"`; a help sheet's list; the single-key switch; the cap's class, a written shortcut as what to draw, and the palette's own shortcut. | keep |
| `Keycaps`, `KEYCAP`, `useLauncherKeys` | [Values](values.md) | `components/command-search/keycaps.tsx` | The palette's first copy of the caps. | deprecated (use `components/keycaps.tsx`) |
| `TextLink`, `INLINE_LINK`, `ONWARD_LINK` | [Section head](section-head.md) | `components/inline-link.tsx` | A link drawn as text: `inline`, underlined at rest in the sentence's ink, and `onward`, with its arrow (what `SectionLink` draws); the two classes are for a Radix `asChild` link that cannot be a `TextLink`. | keep |
| `LanguageMark` | [Values](values.md) | `components/language-icon.tsx` | A repository's language as its logo and its name. | keep |
| `Logo`, `LogoGlyph`, `LogoStacked`, `LogoMark` | [Values](values.md) | `components/logo.tsx` | The product's own lockups and mark, inline on `currentColor`. | keep |
| `DashboardMark` | [Values](values.md) | `components/logo.tsx` | The dashboard drawn as one thing among others: the mark on a product tile. | keep |
| `ProductLogo`, `ProductGlyph`, `ProductGlyphs`, `ProductLogos` | [Values](values.md) | `components/product-logo.tsx` | A product drawn as itself: on its tile, bare in a line, a row after words, an overlapping run. | keep |
| `hasProductLogo` | [Values](values.md) | `components/product-logo.tsx` | Whether a product has a logo file, before choosing between it and a glyph. | keep |
| `imageProduct`, `containerProduct`, `imageProducts`, `containerProducts`, `processProduct`, `unitProduct`, `pm2Product`, `portProduct`, `programProduct`, `platformProduct`, `cpuProduct`, `virtualizationProduct`, `interfaceProduct`, `sensorProduct`, `hostProduct`, `gitProviderProduct`, `recipeProduct`, `packageManagerProduct`, `frameworkProduct`, `buildMethodProduct`, `channelProduct`, `webhookProduct`, `issuerProduct`, `variableProduct` | [Values](values.md) | `components/product-logo.tsx` | Which product a thing is (an image, a unit, a port, a channel, a certificate's issuer), by whatever names it. | keep |
| `ProjectMark`, `ProjectMarkOf`, `ProjectFavicon`, `projectFavicon`, `useProjectFavicon` | [Values](values.md) | `components/project-mark.tsx` | A deployed project drawn as its own website's icon, on a tile or bare. | keep |
| `Avatar`, `AvatarImage`, `AvatarFallback`, `AvatarBadge`, `AvatarGroup`, `AvatarGroupCount` | [Values](values.md) | `components/ui/avatar.tsx` | The avatar primitive under `UserAvatar`. | keep |
| `UserAvatar`, `InitialsMark`, `AvatarRun` | [Values](values.md) | `components/user-avatar.tsx` | An account's picture or silhouette, a person from a name alone, and a run of them. | keep |
| `displayNameOf`, `avatarSrc` | [Values](values.md) | `components/user-avatar.tsx` | The name an account shows as, and where its picture is served from. | keep |
| `useCopy` | [Values](values.md) | `hooks/use-copy.ts` | Copying with an inline tick for 1.6 s instead of a toast. | keep |
| `useDebounced` | [Values](values.md) | `hooks/use-debounced.ts` | A value that follows another after it has held still for a delay. | keep |
| `useElapsed` | [Values](values.md) | `hooks/use-elapsed.ts` | Milliseconds since a start, ticking, for a wait or a run's time. | keep |
| `useNow` | [Values](values.md) | `hooks/use-now.ts` | The one clock: the time, advancing every `intervalMs`, for a figure that is a distance from now. | keep |
| `useShortcuts`, `useSingleKeyShortcuts`, `ShortcutMap` (type), `ShortcutHandler` (type) | [Keyboard and place](../patterns/keyboard-and-place.md) | `hooks/use-shortcuts.ts` | A page's keys written as the help prints them (`n`, `Shift+R`, `Ctrl/⌘+K`, `g s`), skipped while typing or while a dialog or menu has them; one-character keys wait for the single-key preference unless `scope` keeps them to focus inside a list. | keep |
| `isTypingTarget`, `keyOwnedElsewhere`, `isCharacterShortcut`, `shortcutSteps`, `shortcutName`, `eventNames`, `TYPING_SELECTOR`, `OWNER_SELECTOR` | [Keyboard and place](../patterns/keyboard-and-place.md) | `lib/keys.ts` | The guards every key handler needs: whether a key is somebody typing, whether a dialog or menu owns the keyboard, whether a shortcut is one character, and a written shortcut as the names a key press answers to. | keep |
| `ConfirmDialog`, `useConfirm`, `ConfirmRequest` (type) | [Overlays](overlays-confirm.md) | `components/confirm-dialog.tsx` | The one confirmation: a dialog driven from anywhere in a page. | keep |
| `Dock` | [Overlays](overlays-confirm.md) | `components/dock.tsx` | A floating dock at the bottom right, stacked over the ones below it. | keep |
| `useStackOccupant`, `useBottomStack`, `stackOffsets`, `STACK_EDGE`, `STACK_GAP`, `StackLayer` (type), `StackOccupant` (type) | [Overlays](overlays-confirm.md) | `components/dock.tsx` | The bottom stack: what floats at the window's foot, measured, so a bar, a dock and a toast never cover one another. | keep |
| `Explain`, `ExplainEntry` (type), `ExplainProps` (type) | [Overlays](overlays-confirm.md) | `components/explain.tsx` | The one toggletip for an explanation: an ⓘ, a dotted term or a wrapped control. | keep |
| `ImageLightbox`, `LightboxImage` (type) | [Overlays](overlays-confirm.md) | `components/image-lightbox.tsx` | A picture as large as the window allows, over a dark ground. | keep |
| `Modal`, `PaletteModal`, `OverlaySize` (type) | [Overlays](overlays-confirm.md) | `components/modal.tsx` | The centred task surface, and the search overlay whose input is its header; `size` takes the overlay size names. | keep |
| `ModalDescription` | [Overlays](overlays-confirm.md) | `components/modal.tsx` | An alert dialog's sentence, drawn in its body and read as its description. | keep |
| `StageModal`, `STAGE_CONTROL` | [Overlays](overlays-confirm.md) | `components/modal.tsx` | A viewer that takes the whole window on a dark ground (a picture), and a control in its strip. | keep |
| `DialogSubject` | [Overlays](overlays-confirm.md) | `components/modal.tsx` | What a dialog acts on, as a mark, a name and its facts. | keep |
| `PromptDialog`, `usePrompt`, `PromptOptions` (type), `PromptFact` (type) | [Overlays](overlays-confirm.md) | `components/prompt-dialog.tsx` | Asks for one value (a name) in a dialog, from a component or imperatively. | keep |
| `RailNotice`, `RailNoticeHead`, `RailNoticeTone` (type) | [Overlays](overlays-confirm.md) | `components/rail-notice.tsx` | A notice card at the sidebar's foot that folds to a tile on the collapsed rail, and its first line with the dismiss. | keep |
| `SidePanel`, `SidePanelFooter`, `useInSidePanel` | [Overlays](overlays-confirm.md) | `components/side-panel.tsx` | The sheet, for a small add or edit form; a command the body brings to its footer. `size` takes the overlay size names; `width` is deprecated. | keep |
| `Command`, `CommandDialog`, `CommandInput`, `CommandList`, `CommandEmpty`, `CommandGroup`, `CommandItem`, `CommandShortcut`, `CommandSeparator` | [Overlays](overlays-confirm.md) | `components/ui/command.tsx` | cmdk's command list, under the command palette. | keep |
| `Dialog`, `DialogClose`, `DialogContent`, `DialogDescription`, `DialogFooter`, `DialogHeader`, `DialogOverlay`, `DialogPortal`, `DialogTitle`, `DialogTrigger` | [Overlays](overlays-confirm.md) | `components/ui/dialog.tsx` | Radix dialog, assembled only in `modal.tsx` (and `ui/command`); a page opens a `Modal`. | keep |
| `DialogBareContent`, `OVERLAY_CLOSE`, `OVERLAY_GROUND` | [Overlays](overlays-confirm.md) | `components/ui/dialog.tsx` | The dialog's content with no face (for `StageModal`), the one close button, and the dim ground under every overlay that takes the page. | keep |
| `HoverCard`, `HoverCardTrigger`, `HoverCardContent` | [Overlays](overlays-confirm.md) | `components/ui/hover-card.tsx` | A card shown on hover, for a preview a pointer asks for. | keep |
| `Popover`, `PopoverTrigger`, `PopoverContent`, `PopoverAnchor`, `PopoverHeader`, `PopoverTitle`, `PopoverDescription`, `keepMovedFocus` | [Overlays](overlays-confirm.md) | `components/ui/popover.tsx` | A floating panel, one of the three things that may cast a shadow; `keepMovedFocus` hands the focus back to the trigger only when it has nowhere else to be. | keep |
| `Sheet`, `SheetTrigger`, `SheetClose`, `SheetContent`, `SheetHeader`, `SheetFooter`, `SheetTitle`, `SheetDescription` | [Overlays](overlays-confirm.md) | `components/ui/sheet.tsx` | Radix sheet, assembled only in `SidePanel` (and the sidebar's phone drawer). | keep |
| `Tooltip`, `TooltipTrigger`, `TooltipContent`, `TooltipProvider` | [Overlays](overlays-confirm.md) | `components/ui/tooltip.tsx` | A label for an icon-only control; an explanation is an `Explain`. | keep |
| `useFocusReturn`, `firstReachable`, `claimFocusReturn` | [Overlays](overlays-confirm.md) | `hooks/use-focus-return.ts` | Where the keyboard goes when a dialog closes. | keep |
| `NameDialog` | [Overlays](overlays-confirm.md) | `components/git/name-dialog.tsx` | Git's dialog asking for a name. | deprecated (use `PromptDialog`) |
| `usePrompt`, `PromptRequest` (type) | [Overlays](overlays-confirm.md) | `components/files/prompt-dialog.tsx` | Files' own prompt. | deprecated (use `usePrompt` from `prompt-dialog.tsx`) |
| `menuOpener`, `openerOf` | [Overlays](overlays-confirm.md) | `hooks/use-focus-return.ts` | The one walk from a menu item back to the control that opened its menu, and the control a press came from. | keep |
| `useFocusTrail` | [Overlays](overlays-confirm.md) | `hooks/use-focus-return.ts` | A page's fallback for the keyboard when a dialog's opener went with what it did. | keep |
| `IconAction`, `RevealGroup` (type) | [Menus](menus.md) | `components/icon-action.tsx` | An icon-only control with a real tooltip label. | keep |
| `RowActions`, `DimActions`, `rowReveal`, `ROW_REVEAL`, `ROW_DIM` | [Menus](menus.md) | `components/icon-action.tsx` | Where a row's controls are: revealed on hover and focus, or always drawn and dimmed. | keep |
| `ContextMenu`, `ContextMenuTrigger`, `ContextMenuContent`, `ContextMenuItem`, `ContextMenuCheckboxItem`, `ContextMenuRadioItem`, `ContextMenuLabel`, `ContextMenuSeparator`, `ContextMenuShortcut`, `ContextMenuGroup`, `ContextMenuPortal`, `ContextMenuSub`, `ContextMenuSubContent`, `ContextMenuSubTrigger`, `ContextMenuRadioGroup` | [Menus](menus.md) | `components/ui/context-menu.tsx` | The right-click menu, drawn as the dropdown is. | keep |
| `DropdownMenu`, `DropdownMenuPortal`, `DropdownMenuTrigger`, `DropdownMenuContent`, `DropdownMenuGroup`, `DropdownMenuLabel`, `DropdownMenuItem`, `DropdownMenuCheckboxItem`, `DropdownMenuRadioGroup`, `DropdownMenuRadioItem`, `DropdownMenuSeparator`, `DropdownMenuShortcut`, `DropdownMenuSub`, `DropdownMenuSubTrigger`, `DropdownMenuSubContent` | [Menus](menus.md) | `components/ui/dropdown-menu.tsx` | The dropdown, drawn from `menu-styles`; a row's or a page's verbs are a `VerbMenu`. | keep |
| `MenuItemText` | [Menus](menus.md) | `components/ui/menu-item-text.tsx` | An option's name and its metadata in one text column. | keep |
| `menuSurfaceClasses`, `menuItemClasses`, `menuDangerClasses`, `menuLabelClasses`, `menuIndicatorClasses` | [Menus](menus.md) | `components/ui/menu-styles.ts` | The one look of a menu's surface, rows, labels and marks, for select, dropdown and context menu. | keep |
| `Select`, `SelectContent`, `SelectGroup`, `SelectItem`, `SelectLabel`, `SelectScrollDownButton`, `SelectScrollUpButton`, `SelectSeparator`, `SelectTrigger`, `SelectValue` | [Menus](menus.md) | `components/ui/select.tsx` | The select, its trigger a `Button` and its rows the menu rows. | keep |
| `VerbActions`, `VerbBar`, `VerbMenu`, `VerbActionsMode` (type) | [Menus](menus.md) | `components/verbs.tsx` | A row's icon verbs, a detail's verb bar, and the overflow menu where a verb is a word; `VerbActions` takes `mode` (`reveal`, `dim` or `plain`); its `reveal` and `dim` booleans are deprecated. | keep |
| `VerbSpec` (type), `Verb` (type), `DANGER_AT_REST` | [Menus](menus.md) | `components/verbs.tsx` | A verb declared once as data (its label, a `short` word for a bar, its capability and confirmation), and the ink a danger verb keeps at rest. | keep |
| `VerbItems`, `VerbItemList`, `VerbMenuParts` (type) | [Menus](menus.md) | `components/verbs.tsx` | One list of verbs drawn into a dropdown or a context menu, or with parts of the caller's. | keep |
| `FloatingBar`, `SaveKey`, `useFloatingBarHost`, `saveShortcut` | [Forms and settings](forms.md) | `components/floating-bar.tsx` | The floating bar a save or a pending change stands in, the keycap at its command's end, where it is drawn, and the save shortcut's words. | keep |
| `Field`, `FieldRow`, `useField`, `useFieldControl`, `FieldControl` (type) | [Forms and settings](forms.md) | `components/form.tsx` | A label, a control and one line under it; two or three on a line; what a control reads from its field. | keep |
| `FieldChecks`, `FieldCheck` | [Forms and settings](forms.md) | `components/form.tsx` | The rules a value has to meet, lit as they are met and spoken as they change. | keep |
| `InfoTip` | [Forms and settings](forms.md) | `components/form.tsx` | The ⓘ beside a label. | deprecated (use `Explain`) |
| `FormSection`, `FormSections` | [Forms and settings](forms.md) | `components/form.tsx` | A titled part of a longer form, and a run of them held to the fields' width. | keep |
| `Disclosure` | [Forms and settings](forms.md) | `components/form.tsx` | A part of a form folded away until it is wanted. | keep |
| `OptionList`, `OptionRow` | [Forms and settings](forms.md) | `components/form.tsx` | A switch with its sentence, in a run ruled apart. | keep |
| `FormFacts`, `FormFact` | [Forms and settings](forms.md) | `components/form.tsx` | What a form operates on, as data under its title. | keep |
| `Statement` | [Forms and settings](forms.md) | `components/form.tsx` | The statement a form will run, shown before it runs. | keep |
| `FormNote` | [Forms and settings](forms.md) | `components/form.tsx` | A short piece of guidance under a form, quieter than a `Notice`. | keep |
| `ListField`, `ListEntry` (type) | [Forms and settings](forms.md) | `components/list-field.tsx` | A field that holds a list of entries (addresses, headers), each checked as typed. | keep |
| `ScheduleBuilder`, `SchedulePreset` (type), `ScheduleFields` (type) | [Forms and settings](forms.md) | `components/schedule-builder.tsx` | A schedule as a sentence with the moments it fires under it. | keep |
| `scheduleFields`, `scheduleExpression`, `scheduleValid`, `schedulePreview`, `SCHEDULE_PRESETS`, `WEEKDAYS` | [Forms and settings](forms.md) | `components/schedule-builder.tsx` | A schedule's fields, its cron expression and its next firings, both ways. | keep |
| `SecretField` | [Forms and settings](forms.md) | `components/secret-field.tsx` | A password or token field with show, generate and copy. | keep |
| `generatePassword`, `PASSWORD_ALPHABET` | [Forms and settings](forms.md) | `lib/password.ts` | The one generator behind every Generate: thirty characters by default, from an alphabet with no look-alikes. | keep |
| `OneTimeCodeInput`, `RecoveryCodes` | [Forms and settings](forms.md) | `components/account/one-time-code.tsx` | The six-digit code from an authenticator, and the recovery codes two-factor hands out once. | keep |
| `DangerRow`, `DangerRows` | [Forms and settings](forms.md) | `components/settings/danger-row.tsx` | One destructive verb in a settings page's danger zone, with its sentence, and the one edge around them all. | keep |
| `PendingStrip`, `PendingItem` (type) | [Forms and settings](forms.md) | `components/settings/pending-strip.tsx` | What is saved and not in force yet, at the head of a settings column. | keep |
| `appliesLine`, `namesLine`, `pendingLine`, `SettingApplies` (type) | [Forms and settings](forms.md) | `components/settings/save-bar-words.ts` | The save bar's second line in words. | keep |
| `SaveBarProvider`, `useSaveBarEntry`, `SettingApplies` (type) | [Forms and settings](forms.md) | `components/settings/save-bar.tsx` | The page's one floating save bar and the forms on it; its shortcut's words are `saveShortcut` in `floating-bar.tsx`. | keep |
| `useSaveBarHost` | [Forms and settings](forms.md) | `components/settings/save-bar.tsx` | Where the save bar is drawn; `useFloatingBarHost` is. | retired (0.9.0) |
| `SettingsPage` | [Forms and settings](forms.md) | `components/settings/settings-page.tsx` | A deployment's settings page: its configuration read, what is saved but not live, and its forms, in the one settings column. | keep |
| `SettingForm`, `SettingSection`, `settingStatus` | [Forms and settings](forms.md) | `components/settings/setting-card.tsx` | One form of a settings page, one section, and where a section stands against what is live. | keep |
| `SettingsPage` | [Forms and settings](forms.md) | `components/settings/setting-card.tsx` | The settings page's old home, re-exported; `SettingsPage` from `settings/settings-page.tsx` is the one. | retired (0.9.0) |
| `SettingsColumn` | [Forms and settings](forms.md) | `components/settings/settings-column.tsx` | The one centred settings column for a page that is not a deployment's. | keep |
| `SwitchRows`, `SwitchRow` | [Forms and settings](forms.md) | `components/settings/settings-column.tsx` | A switch as a full-width row, in a run ruled apart. | keep |
| `EditedTag` | [Forms and settings](forms.md) | `components/settings/settings-column.tsx` | A section holding a change not applied yet, in git's modified colour. | keep |
| `NoticeLines`, `NoticeLine` | [Forms and settings](forms.md) | `components/settings/settings-column.tsx` | One-line notices under a page's identity line, ruled apart. | keep |
| `Accordion`, `AccordionItem`, `AccordionTrigger`, `AccordionContent` | [Forms and settings](forms.md) | `components/ui/accordion.tsx` | Folding sections; a form's fold is a `Disclosure`, and a finding's evidence a popover. | retired (0.9.0) |
| `Button`, `buttonVariants`, `hitAreaClasses` | [Forms and settings](forms.md) | `components/ui/button.tsx` | The button: faces and sizes, touch heights, and the 24px hit area for a small one. | keep |
| `Checkbox` | [Forms and settings](forms.md) | `components/ui/checkbox.tsx` | A 16px box with a 24px press box (44px under a finger) and the control edge. | keep |
| `Collapsible`, `CollapsibleTrigger`, `CollapsibleContent` | [Forms and settings](forms.md) | `components/ui/collapsible.tsx` | A part that folds away; the primitive under `Disclosure`. | keep |
| `InputGroup`, `InputGroupAddon`, `InputGroupButton`, `InputGroupInput`, `InputGroupText`, `InputGroupToggle` | [Forms and settings](forms.md) | `components/ui/input-group.tsx` | A field with things inside its edge: a unit, a prefix, a button. | keep |
| `Input` | [Forms and settings](forms.md) | `components/ui/input.tsx` | A text field, 44px under a finger and 36px under a pointer, with the control edge. | keep |
| `Label` | [Forms and settings](forms.md) | `components/ui/label.tsx` | A field's label. | keep |
| `Slider` | [Forms and settings](forms.md) | `components/ui/slider.tsx` | A range slider. | keep |
| `Switch` | [Forms and settings](forms.md) | `components/ui/switch.tsx` | An on/off switch; in a form it sits in an `OptionRow` or `SwitchRow`. | keep |
| `Textarea` | [Forms and settings](forms.md) | `components/ui/textarea.tsx` | A multi-line text field. | keep |
| `settleOrder`, `sameOrder`, `trailSignature`, `pushTrails`, `trailMoves`, `spanPercent`, `tickPercent`, `timeAt`, `OVERTAKE`, `TRAIL_LENGTH` | [Readings](readings.md) | `components/bar-reading-model.ts` | A band's arithmetic: the order that holds between polls, the trails, and where a span or tick falls. | keep |
| `BarReading`, `ReadingPart` (type) | [Readings](readings.md) | `components/bar-reading.tsx` | A band's block: a head, one bar of parts, one line and a strip of marks, one height in every state. | keep |
| `shade` | [Readings](readings.md) | `components/bar-reading.tsx` | A part's colour at a step of its own lightness, for neighbours of one hue. | keep |
| `ChartPanel`, `ChartPlaceholder` | [Readings](readings.md) | `components/chart-panel.tsx` | A chart's head, plot and legend, and its empty state. | keep |
| `LiveFigure`, `LiveBytes`, `LandedFigures` | [Readings](readings.md) | `components/live-figure.tsx` | A socket-fed figure that glides to each value, its unit kept beside it. | keep |
| `HUE` | [Readings](readings.md) | `components/live-figure.tsx` | Each measure's colour, wherever it is drawn. | keep |
| `CoreBars`, `SeriesKey`, `StreamState` | [Readings](readings.md) | `components/live-figure.tsx` | Every core's share, the key before a reading's name, and whether the socket is live. | keep |
| `utilisationTone` | [Readings](readings.md) | `components/meter.tsx` | The thresholds that colour a utilisation figure and its bar alike. | keep |
| `Meter` | [Readings](readings.md) | `components/meter.tsx` | A bar for a figure between 0 and 100, with a threshold mark; `color`, `size="cell"` and `decorative`. | keep |
| `RankList`, `RankSkeleton`, `RankItem` (type), `SplitPart` (type) | [Readings](readings.md) | `components/rank-list.tsx` | A ranked list read in columns (a name, a figure, its share), and its rows waiting. | keep |
| `percent` | [Readings](readings.md) | `components/rank-list.tsx` | A share in whole percent; `sharePercent` from `lib/format.ts` is it. | retired (0.9.0) |
| `SplitBar`, `splitWidths` | [Readings](readings.md) | `components/rank-list.tsx` | One whole as one strip of coloured parts, with an optional legend that names or narrows. | keep |
| `ReadingLine`, `ReadingLines`, `readingLineHeight` | [Readings](readings.md) | `components/reading-line.tsx` | One reading as a sentence on a mark with its last hour, or the verb that answers it, at the end; two columns from `xl`; and the one height every state of a line keeps, for a line drawn by hand. | keep |
| `ReadingMark` | [Readings](readings.md) | `components/reading-line.tsx` | A reading's glyph on its measure's hue, or on the tone of what is wrong with it. | keep |
| `NoReading` | [Readings](readings.md) | `components/reading-line.tsx` | A reading that has not arrived, in the reading's own place and height. | keep |
| `Sparkline` | [Readings](readings.md) | `components/sparkline.tsx` | A trend the width of a table cell, as one SVG path. | keep |
| `TileTrend`, `isSteady`, `buildPath` | [Readings](readings.md) | `components/sparkline.tsx` | A reading's last hour at a tile's or a reading line's end; a steady series is a level line. | keep |
| `StatTile`, `StatGrid` | [Readings](readings.md) | `components/stat-tile.tsx` | A headline figure, and a run of them as a table; only in a sanctioned headline set. | keep |
| `StatButton` | [Readings](readings.md) | `components/stat-tile.tsx` | A tile whose press narrows what is under it (a lens's counts). | keep |
| `TileBand` | [Readings](readings.md) | `components/stat-tile.tsx` | A tile's picture on the one line every tile of a row keeps for it. | keep |
| `StatLink` | [Readings](readings.md) | `components/stat-tile.tsx` | A tile that was a link; a `Link` around a `StatTile` is. | retired (0.9.0) |
| `ChartContainer`, `ChartTooltip`, `ChartTooltipContent`, `ChartLegend`, `ChartLegendContent`, `ChartStyle`, `ChartConfig` (type) | [Readings](readings.md) | `components/ui/chart.tsx` | shadcn's recharts frame; a page's chart is a `MetricChart` or `ChartPanel`. | keep |
| `SettingPicture` | [Wiring pictures](wiring.md) | `components/settings/setting-picture.tsx` | A settings section's picture of what its fields decide: two or three marks in a row. | keep |
| `AnimatedBeam` | [Wiring pictures](wiring.md) | `components/ui/animated-beam.tsx` | A line between two marks with a pulse along it; still, dashed or toned by what it carries. | keep |
| `levelPath`, `Point` (type) | [Wiring pictures](wiring.md) | `components/ui/beam-path.ts` | The beam's arithmetic: a line that runs level from each end and bends only across a gap between them. | keep |
| `useMotionHeld` | [Wiring pictures](wiring.md) | `components/wire/use-motion-held.ts` | Whether the wires hold still: the tab is hidden, the system asks for less motion, or this browser paused the animations. | keep |
| `useMotionPaused`, `setMotionPaused` | [Motion](../foundations/motion.md) | `components/shell/motion-pause.ts` | Whether this browser paused the product's animations, and the switch for it (Pause animations, in the account menu). | keep |
| `reducedMotion`, `motionMs`, `scrollIntoViewRespectingMotion` | [Motion](../foundations/motion.md) | `lib/motion.ts` | What script asks before it moves anything: both answers to reduced motion, a canvas duration, and the one way to scroll to a section. | keep |
| `beamOf`, `staggerOf`, `fitColumn`, `isLit`, `PORT`, `flowTemplate`, `CELL`, `fitsRow`, `flowForm`, `perRow`, `columnStart`, `middleRows`, `levelDrops`, `nearerInRow`, `beyondInRow`, `carriedBeam`, `paintOrder`, `loadingRows`, `waitingLines`, `WireState` (type), `WireLine` (type), `BeamProps` (type), `WaitingHint` (type), `WaitingLine` (type) | [Wiring pictures](wiring.md) | `components/wire/wire-flow-model.ts` | WireFlow's arithmetic: what a line's state draws, what a column has room for, what stays lit, and the wide picture's form, rows and level. | keep |
| `WireFlow`, `WireFlowNode` (type), `WireFlowWaiting` (type), `WireFlowColumn` (type), `WireFlowHub` (type), `WaitingHint` (type), `WireLine` (type), `WireState` (type) | [Wiring pictures](wiring.md) | `components/wire/wire-flow.tsx` | A picture of things wired through one hub, from data: columns, the hub, lines by state, loading and phone forms. | keep |
| `WireLane`, `WireLaneTitle` | [Wiring pictures](wiring.md) | `components/wire/wire-lane.tsx` | One column of a hand-laid picture (its heading, count and list), and the heading alone. | keep |
| `WireMarks`, `MoreTile`, `WireMarksTier` (type), `WireMarksRow` (type), `WireMarksHub` (type), `WireMarksNode` (type), `WireMarksWire` (type) | [Wiring pictures](wiring.md) | `components/wire/wire-marks.tsx` | A picture on a phone, the one form every picture takes below `lg`: rows of small marks, the hub between them at its own size, lines from centre to centre; and the count of what a row has no room for. | keep |
| `MonoValue` | [Wiring pictures](wiring.md) | `components/wire/mono-value.tsx` | A path, an address or a unit's name that wraps only at its separators, never inside a word; copied whole. | keep |
| `monoBreaks` | [Wiring pictures](wiring.md) | `components/wire/mono-breaks.ts` | Where a mono value may break, as the pieces `MonoValue` draws a `<wbr>` between. | keep |
| `WireNode`, `WireMark`, `WireHost`, `WirePlaceholder`, `WireLink`, `WireLabel` | [Wiring pictures](wiring.md) | `components/wire/wire.tsx` | A picture's parts: a node, a filled mark, this server, a mark not there yet, a linked mark, a caption on a line. | keep |
| `CANVAS_MOVE`, `zoomKey`, `zoomAbout`, `useCanvasPinch` | [Workbench](workbench.md) | `components/canvas-move.ts` | How a canvas moves, Excalidraw's way: trackpad pans, pinch and keys zoom about a point. | keep |
| `CanvasZoom`, `CanvasZoomWord`, `FLOAT` | [Workbench](workbench.md) | `components/canvas-zoom.tsx` | A canvas's zoom out, percent, in, Fit and Tidy, a word beside Fit, and the ground of a control floating on a canvas. | keep |
| `ZoomBar`, `FLOAT` | [Workbench](workbench.md) | `components/database/diagram/chrome.tsx` | The ER diagram's own zoom bar and its ground; `CanvasZoom` and `FLOAT` from `canvas-zoom.tsx` draw them. | retired (0.9.0) |
| `CodeEditor`, `EditorCommands` (type) | [Workbench](workbench.md) | `components/code-editor.tsx` | Monaco, wired to the dashboard's palette and keys. | keep |
| `PaneBar`, `PaneBack`, `paneShown`, `PaneBreak` (type), `PaneStop` (type) | [Workbench](workbench.md) | `components/phone-panes.tsx` | One pane at a time on a phone: the bar at the foot (a bar in the bottom stack), the way back, and which pane shows. | keep |
| `ResizeHandle` | [Workbench](workbench.md) | `components/resize-handle.tsx` | The grab strip between a side column and the work, by pointer or by keys. | keep |
| `WorkbenchHeader`, `WorkbenchBack` (type) | [Workbench](workbench.md) | `components/workbench-header.tsx` | A workbench's top strip: the way back (always an arrow), the mark, the name with its state, the tools, one command and the `⋯`. | keep |
| `XtermPane`, `XtermActions` (type) | [Workbench](workbench.md) | `components/xterm-pane.tsx` | An xterm.js terminal wired to a PTY over a socket. | keep |
| `useFullscreen` | [Workbench](workbench.md) | `hooks/use-fullscreen.ts` | Full screen, or an immersive overlay where the browser refuses it, with Escape and focus return. | keep |
| `useIsPhone`, `useIsMobile`, `useMediaQuery` | [Workbench](workbench.md) | `hooks/use-mobile.ts` | A phone, the drawer's width, and any media query, as state. | keep |
| `useRevealOnWheel` | [Workbench](workbench.md) | `hooks/use-reveal-on-wheel.ts` | The page moves first to bring a partly shown region on screen before the wheel scrolls it. | keep |
| `ConsoleView`, `ConsoleState` (type), `ConsoleLine` (type) | [Feedback](feedback.md) | `components/console-view.tsx` | A command's output as it runs, for a job or a compose run: the title, the state as a `Status` word spoken when it changes, Stop or Dismiss, and one `log` well that follows the newest line. | keep |
| `JobConsole`, `useJobConsole`, `RecentJobs` | [Feedback](feedback.md) | `components/job-console.tsx` | Watching an operation that outlives the page, drawn through `ConsoleView`, and the ones that ran recently. | keep |
| `OperationProgress`, `OperationReading` (type) | [Feedback](feedback.md) | `components/operation-progress.tsx` | A dialog's action in flight, as part of its footer. | keep |
| `RunPhases`, `phaseStates`, `RunPhase` (type) | [Feedback](feedback.md) | `components/run-phases.tsx` | Where a restart or an upgrade is, as a deployment's release path draws it. | keep |
| `RunTranscript` | [Feedback](feedback.md) | `components/run-transcript.tsx` | A restart's or an upgrade's transcript, read line by line. | keep |
| `TranscriptRow`, `useTranscriptDrip`, `dripRemaining`, `DRIP_LAG_MS` | [Feedback](feedback.md) | `components/transcript-line.tsx` | One line of a transcript (`gutter` takes `gutterCh` of the last number; `inBlock` inside a build console's block), and the drip that lets lines arrive readably, each within 300 ms. | keep |
| `jobLineToTranscript`, `streamKind` | [Feedback](feedback.md) | `components/transcript-line.tsx` | A console stream's line as a transcript line: status a heading, stderr a washed warning, the number the job's own sequence. | keep |
| `EmbeddedLog`, `logsPageHref`, `sourceSummary`, `EmbeddedLogProps` (type) | [Feedback](feedback.md) | `components/logs/embedded-log.tsx` | A service's log at a page's foot; on a phone a card per source that opens the Logs page on it, never a console inside the page. | keep |
| `LogSection` | [Feedback](feedback.md) | `components/logs/log-section.tsx` | A page-foot log's title, hairline and held skeleton. | keep |
| `LOG_PANE_SIZE`, `LogPaneSize` (type) | [Feedback](feedback.md) | `components/logs/service-logs.tsx` | The three heights of a service's log: section, sheet and fill. | keep |
| `Toaster` | [Feedback](feedback.md) | `components/ui/sonner.tsx` | The one place a toast is drawn; raise one with `notify` from `lib/toast.ts`. | keep |
| `Table`, `TableHeader`, `TableBody`, `TableFooter`, `TableHead`, `TableRow`, `TableCell`, `TableCaption`, `stickyTableHeader`, `ROW_NAME`, `SECONDARY_COLUMN` | [Table](table.md) | `components/ui/table.tsx` | Tables, for readings only: hairlines, a row wash, a header that can stay put. `ROW_NAME` marks a row's name control, so the row is no tab stop; `SECONDARY_COLUMN` (`secondary` on `TableHead` and `TableCell`) drops a column below `sm`. | keep |
| `SortHead` | [Table](table.md) | `components/ui/table.tsx` | A sortable column head with `aria-sort` and one chevron. | keep |
| `FlowHeader`, `FlowSteps`, `FlowPanel`, `FlowPanelHeader`, `FlowPanelBody`, `FlowActions` | [Flow pages](../recipes/flow-page.md) | `components/flow.tsx` | A flow page's head, its spine of steps, the one focused surface and its foot. | keep |
| `BlurFade` | [Motion](../foundations/motion.md) | `components/ui/blur-fade.tsx` | A block arriving by fading in from a blur, staggered by its place. | keep |
| `BorderBeam` | [Motion](../foundations/motion.md) | `components/ui/border-beam.tsx` | A light travelling round a frame: work is happening inside it. | keep |
| `Confetti`, `ConfettiRef` (type) | [Motion](../foundations/motion.md) | `components/ui/confetti.tsx` | A burst of paper for a release that went live while it was watched, and the first run's landing. | keep |
| `NumberTicker` | [Motion](../foundations/motion.md) | `components/ui/number-ticker.tsx` | A figure that counts up to its value when it arrives. | keep |
| `SpotlightBorder` | [Motion](../foundations/motion.md) | `components/ui/spotlight-border.tsx` | A border that lights while the pointer is on the card. | keep |
| `TextShimmer` | [Motion](../foundations/motion.md) | `components/ui/text-shimmer.tsx` | Words with a light passing over them while something waits. | keep |
| `AppSidebar` | [Shell](../shell.md) | `components/shell/app-sidebar.tsx` | The rail: groups, sections drilled into, and the account at its foot. | keep |
| `CommandPaletteProvider`, `useCommandPalette` | [Shell](../shell.md) | `components/shell/command-palette.tsx` | The command palette and the way to open it. | keep |
| `NavScopeProvider`, `useNavScope`, `useNavScopeValue`, `NavScopeEntry` (type), `NavScope` (type) | [Shell](../shell.md) | `components/shell/nav-scope.tsx` | A section handing the rail its own panel while it is mounted. | keep |
| `NAV`, `PERSONAL_NAV`, `ACCOUNT_SECTION`, `PROJECT_NAV`, `PROJECT_SETTINGS_NAV`, `APP_NAME`, `NavChild` (type), `NavItem` (type), `NavGroup` (type), `NavEntry` (type), `ProjectNavEntry` (type), `TabName` (type) | [Shell](../shell.md) | `components/shell/nav.ts` | The rail's entries, the account's pages and a project's, in their order. | keep |
| `groupedChildren`, `navMatches`, `navOwns`, `navCurrent`, `shownFor`, `hideableSections`, `sectionEntry`, `sectionsFor`, `navLocation`, `tabTitle` | [Shell](../shell.md) | `components/shell/nav.ts` | Where a path sits in the rail, what the rail shows for it, and what the tab says. | keep |
| `useTabTitle`, `TabHeading`, `useTabName` | [Shell](../shell.md) | `components/shell/tab-title.tsx` | Names the browser tab from the page on screen. | keep |
| `Sidebar`, `SidebarContent`, `SidebarFooter`, `SidebarGroup`, `SidebarGroupAction`, `SidebarGroupContent`, `SidebarGroupLabel`, `SidebarHeader`, `SidebarInput`, `SidebarInset`, `SidebarMenu`, `SidebarMenuAction`, `SidebarMenuBadge`, `SidebarMenuButton`, `SidebarMenuItem`, `SidebarMenuSkeleton`, `SidebarMenuSub`, `SidebarMenuSubButton`, `SidebarMenuSubItem`, `SidebarProvider`, `SidebarRail`, `SidebarSeparator`, `SidebarTrigger`, `useSidebar` | [Shell](../shell.md) | `components/ui/sidebar.tsx` | The sidebar primitive the shell's rail is built on. | keep |
| `useHiddenSections` | [Shell](../shell.md) | `hooks/use-hidden-sections.ts` | The sections an account keeps out of its sidebar, and the way to change them. | keep |
| `pollLoop`, `PollPage` (type), `PollFailure` (type), `PollHandle` (type) | [Data and reads](../patterns/data-flow.md) | `hooks/poll-loop.ts` | Fetches, then schedules the next fetch once the last settled. | keep |
| `sharedPoll`, `createPollShare`, `forgetSharedPolls`, `releaseSharedPolls`, `FRESH_MS`, `KEEP_MS`, `NO_SHARED_ANSWER`, `SharedSnapshot` (type), `SharedReader` (type) | [Data and reads](../patterns/data-flow.md) | `hooks/poll-share.ts` | One answer for every reader of a key, kept a while for whoever opens the page again. | keep |
| `useAccountLink` | [Data and reads](../patterns/data-flow.md) | `hooks/use-account-link.ts` | This server's link to a just-dashboard.com account. | keep |
| `useAppDomain`, `AppDomainControls` (type) | [Data and reads](../patterns/data-flow.md) | `hooks/use-app-domain.ts` | This server's just-dashboard.com address, and claiming, releasing and issuing it. | keep |
| `useArrivals` | [Data and reads](../patterns/data-flow.md) | `hooks/use-arrivals.ts` | The rows a poll just brought, for them to rise in (§11). | keep |
| `useAuth`, `AuthProvider` | [Data and reads](../patterns/data-flow.md) | `hooks/use-auth.tsx` | The signed-in account and what it can do. | keep |
| `useContainerLive`, `useContainerFrame`, `ContainerLive` (type) | [Data and reads](../patterns/data-flow.md) | `hooks/use-container-live.ts` | One container's stats socket as a reading and a rolling window, and its newest frame. | keep |
| `useGitHubAccount`, `useGitHubApp`, `githubAppStage`, `forgeMediaUrl`, `githubAvatarUrl`, `GitHubAppStage` (type) | [Data and reads](../patterns/data-flow.md) | `hooks/use-github.ts` | Who the dashboard is to GitHub, its GitHub App, and media through this server. | keep |
| `useHubLink`, `HubLinkProvider`, `useShellHub`, `outdated`, `ShellHubValue` (type) | [Data and reads](../patterns/data-flow.md) | `hooks/use-hub-link.tsx` | This server's link to a Just Hub, and the hub the shell draws. | keep |
| `useMetricsHistory`, `useStorageHistory`, `useMetricEvents`, `useRangePreference`, `HistoryState` (type), `StorageState` (type) | [Data and reads](../patterns/data-flow.md) | `hooks/use-metrics-history.ts` | The recorded history over a window, its filesystems, its events and the chosen window. | keep |
| `useMetricsWindow`, `WindowControls` (type) | [Data and reads](../patterns/data-flow.md) | `hooks/use-metrics-window.ts` | The window charts are drawn over, and how it was reached. | keep |
| `useMetrics`, `useMetricsValue`, `useHost`, `useHostName`, `MetricsStream` | [Data and reads](../patterns/data-flow.md) | `hooks/use-metrics.ts` | The live metrics store, one value of it, the host's description and its name, and the stream that feeds it. | keep |
| `useNearViewport` | [Data and reads](../patterns/data-flow.md) | `hooks/use-near-viewport.ts` | Whether a block is within reach of the screen, so a chart draws when it is about to be seen. | keep |
| `usePoll`, `PollState` (type) | [Data and reads](../patterns/data-flow.md) | `hooks/use-poll.ts` | A read that asks again on an interval after each answer, shared between readers of one key. | keep |
| `useProjects` | [Data and reads](../patterns/data-flow.md) | `hooks/use-projects.ts` | The fleet's project summaries by id. | keep |
| `useQuerySelection`, `openRepoPull`, `closeRepo`, `workspaceTab` | [Data and reads](../patterns/data-flow.md) | `hooks/use-query-selection.ts` | A selection held in the address, and the git workspace's own. | keep |
| `useSelfConfig`, `configPhaseLabel` | [Data and reads](../patterns/data-flow.md) | `hooks/use-self-config.tsx` | The dashboard's own settings and the restart that applies a change, its phases in words. | keep |
| `useSelfUninstall` | [Data and reads](../patterns/data-flow.md) | `hooks/use-self-uninstall.tsx` | Removing the dashboard, watched from the page it removes. | keep |
| `useSelfUpdate`, `SelfUpdateProvider`, `updateHasNews`, `phaseLabel` | [Data and reads](../patterns/data-flow.md) | `hooks/use-self-update.tsx` | The dashboard's own upgrade: its state, whether the rail has news, and its phases in words. | keep |
| `useSocket`, `retryDelay`, `Envelope` (type), `SocketState` (type) | [Data and reads](../patterns/data-flow.md) | `hooks/use-socket.ts` | One of the dashboard's push sockets, reconnecting with jittered backoff. | keep |
| `useUpdateHistory` | [Data and reads](../patterns/data-flow.md) | `hooks/use-update-history.ts` | The release notes this build was compiled with, newest first. | keep |
| `useUsagePing` | [Data and reads](../patterns/data-flow.md) | `hooks/use-usage-ping.ts` | The optional daily count of servers, and this server's answer to it. | keep |

### Icons

`components/icons.tsx` is the icon vocabulary: Heroicons, re-exported under the names the product
uses, plus the panel toggles drawn inline (Heroicons has none). A glyph not on this list is added
there rather than imported from `@heroicons/react` in a feature. The names:

`AcronymCsv`, `AcronymJson`, `AcronymMarkdown`, `Archive`, `ArrowCircleUp`, `ArrowDown`, `ArrowLeft`, `ArrowLeftRight`, `ArrowMove`, `ArrowRight`, `ArrowUp`, `ArrowUpDown`, `ArrowUpRight`, `Backspace`, `Bell`, `BlendMode`, `BookOpen`, `Box`, `BranchPlus`, `Bridge`, `Bug`, `Building`, `Calculator`, `ChartActivity`, `ChatBubble`, `Check`, `CheckCircle`, `ChevronDoubleDown`, `ChevronDoubleUp`, `ChevronDown`, `ChevronLeft`, `ChevronRight`, `ChevronUp`, `Clipboard`, `Clock`, `ClockRewind`, `CloudDownload`, `CloudUpload`, `Code`, `CodeBracket`, `CodeWrap`, `Command`, `Connection`, `Copy`, `CornerDownLeft`, `CornerUpLeft`, `CornerUpRight`, `Cpu`, `Crop`, `Cross`, `CrossCircle`, `Crosshair`, `Database`, `DesktopDevice`, `DotMark`, `Download`, `Envelope`, `External`, `Eye`, `EyeOff`, `File`, `FileText`, `FileZip`, `Filter`, `Fingerprint`, `FirewallCheck`, `FloppyDisk`, `FolderClosed`, `FolderOpen`, `FolderPlus`, `Fullscreen`, `FullscreenClose`, `Gauge`, `GitBranch`, `GitCommit`, `GitHubMark`, `GitMerge`, `GitPullRequest`, `GitTag`, `Globe`, `GridMasonry`, `GridSquare`, `Hash`, `Heart`, `Home`, `Hub`, `Icon`, `IconProps`, `Image`, `Inbox`, `Information`, `Inspect`, `Key`, `Layers`, `Layout`, `Lifebuoy`, `Lightning`, `LineChart`, `Link`, `Linked`, `ListOrdered`, `ListUnordered`, `LoaderCircle`, `Location`, `LockClosed`, `LockOpen`, `Logout`, `Logs`, `MagnifyingGlass`, `MagnifyingGlassMinus`, `MagnifyingGlassPlus`, `Minus`, `Monitoring`, `Monorepo`, `MoreHorizontal`, `Music`, `NetworkDevice`, `Notes`, `PaperAirplane`, `Pause`, `Pencil`, `PhoneDevice`, `Pin`, `Play`, `Plus`, `PlusSquareSmall`, `PreviewDocument`, `Puzzle`, `Question`, `RefreshClockwise`, `Rocket`, `RotateClockwise`, `RotateCounterClockwise`, `Route`, `Router`, `Rss`, `SecureConnection`, `ServerMap`, `Servers`, `SettingsGear`, `SettingsSliders`, `Shield`, `ShieldCheck`, `ShieldOff`, `SidebarLeftClose`, `SidebarLeftOpen`, `SidebarRightClose`, `SidebarRightOpen`, `SignIn`, `Slash`, `SlashForward`, `Sparkles`, `Star`, `StarFill`, `Stop`, `StopCircle`, `Stopwatch`, `Table`, `TabletDevice`, `Terminal`, `TerminalWindow`, `TextFormat`, `TextTitle`, `TextUppercase`, `Topology`, `TrackNext`, `TrackPrevious`, `Trash`, `UserMinus`, `UserPlus`, `UserSettings`, `Users`, `Video`, `Warning`, `WarningFill`, `Wrench`

## Area kits

Exports outside the shared folders that more than one page of an area builds from, or that a
shared family names. They are their area's (the area's page in [`../features/`](../features/) says
where they are used); a page in that area takes them instead of drawing a copy.

| Export | Family or area | File | One line | Status |
| --- | --- | --- | --- | --- |
| `KindTile`, `KindCard`, `KindCards`, `AlertsPicture`, `RuleSheet`, `RuleForm`, `PAIRED_HEAD` | [Alerts](../features/host.md) | `components/alerts/kit.tsx` | The alerts pages' kit: a kind as a tile and as a card, the picture of what is watched and who is told (a `WireFlow`; `phone` picks marks or stack), and a rule's sheet and form. | keep |
| `AlertKinds` (type), `AlertKindSpec` (type), `AlertArea` (type), `areaOf`, `kindProduct`, `reaches`, `alertCounts`, `isAlertKind` | [Alerts](../features/host.md) | `components/alerts/kinds.ts` | What a page's alert kinds are, the area and product each belongs to, which channels a rule reaches, and the counts the line says. | keep |
| `AllClear`, `SubjectCard`, `HistoryRow`, `NothingTold`, `ChannelTests`, `channelNode` | [Alerts](../features/host.md) | `components/alerts/alert-parts.tsx` | The parts that do not know a page's kinds: the all-clear, a subject's card, a line of history, nobody told, the channels with their tests, a channel as the picture's node. | keep |
| `ConnectionString` | [Databases](../features/databases.md) | `components/database/kit/connection-string.tsx` | A connection string in its shapes, with Copy and the password's eye: the Connect popover and an account's snippet. | keep |
| `EnvironmentTag`, `ProtectedTag` | [Databases](../features/databases.md) | `components/database/kit/environment-tag.tsx` | What the operator said a database is for, as a tag in its one hue, and that a connection is protected (`compact` is the lock alone). | keep |
| `DatabaseMissingPage` | [States](states.md) | `components/database/shell/missing-page.tsx` | An address under a database that no page claims, its rail the nearest pages. | keep |
| `WorkbenchFrame`, `ObjectHead`, `WorkbenchSide`, `useSideColumn`, `NOTICE_ON_A_WORKBENCH` | [Workbench](workbench.md) | `components/database/workbench/chrome.tsx` | The database workbenches' one head: the surface (run to the sheet's edges, no frame of its own), the open-object strip, the side column (256px opening width), the one rule for when it stands beside the work, and the inset of a notice above it. | keep |
| `RailHead`, `RailFind`, `RailGroupHead`, `RailRow`, `RailSkeleton`, `RailNoMatch` | [Workbench](workbench.md) | `components/database/workbench/rail.tsx` | The database workbenches' one rail kit: the head, the find box, a group's head, a row, the skeleton and no match. | keep |
| `ContainerPicker` | [Databases](../features/databases.md) | `components/database/kit/container-picker.tsx` | Which container a workbench is looking at (a schema of a SQL database, a MongoDB database) and the way to another. | keep |
| `NameMark` | [Databases](../features/databases.md) | `components/database/kit/marks.tsx` | A container's own colour, stable for its name, as a small square before it. | keep |
| `SchemaChips` | [Databases](../features/databases.md) | `components/database/kit/schema-chips.tsx` | The schemas a page reads, as chips with the rest in a menu; `mode` is a choice of one or a tick of several. | keep |
| `SchemaHead` | [Databases](../features/databases.md) | `components/database/kit/schema-head.tsx` | The head of a schema where an object would stand before one is chosen: the engine's mark, the name, one line of what it holds, its verbs. | keep |
| `ColumnsTable` | [Table](table.md) | `components/database/schema/columns-table.tsx` | A table's columns with the marks of a key and each column's verbs; it measures the width it is given and reads down on a narrow one. | keep |
| `parseJsonDoc`, `printJsonDoc`, `respaceJson`, `nodeAt`, `nodeSize` | [Databases](../features/databases.md) | `components/database/kit/json-doc.ts` | The one lossless JSON parser, and its printer. | keep |
| `CHANGE_STATE` | [Databases](../features/databases.md) | `components/database/grid/legend.ts` | A staged change's sign, words and hue. | keep |
| `PageControls` | [Databases](../features/databases.md) | `components/database/data/foot.tsx` | A pager's size picker and arrows. | keep |
| `FileDrop` | [Databases](../features/databases.md) | `components/database/data/file-drop.tsx` | An import's file picker and drop zone. | keep |
| `RunningCommand` | [Databases](../features/databases.md) | `components/database/query/running-command.tsx` | Stop, for a read that is out. | keep |
| `COMMAND_CLASS`, `CommandClassTag` | [Databases](../features/databases.md) | `components/database/query/command-class.tsx` | What a console command does: reads, writes, removes or is refused. | keep |
| `streamExport`, `settledExport`, `exportBlob`, `exportName`, `saveExport` | [Databases](../features/databases.md) | `lib/stream-export.ts` | The database exports' stream, record and save. | keep |
| `exitWords`, `changeWords`, `crashed` | [Docker](../features/host.md) | `components/docker/words.ts` | What an exit code and a file change read as, and whether an exit was a crash. | keep |
| `EventBand` | [Docker](../features/host.md) | `components/docker/event-band.tsx` | The band a Docker page opens on: what happened as one bar of four kinds, and the newest of them on the window's axis. | keep |
| `Definition`, `GLOSSARY` | [Docker](../features/host.md) | `components/docker/explain.tsx` | A Docker term as a dotted word or a `?` on the shared `Explain` toggletip, and the terms it knows. | keep |
| `cpuModes` | [Metrics](../features/host.md) | `components/metrics/cpu-modes.ts` | Where the processor's time went, as the one line said under CPU: "12% user · 4% sys · 1% wait". | keep |
| `ScaledPair` | [Readings](readings.md) | `components/metrics/hardware-panels.tsx` | Two sides of one quantity as one bar against the largest beside it, for a table cell: the shared `SplitBar` with `total`. | keep |
| `RoleTag`, `ROLE_NAME` | [Workspace](../features/workspace.md) | `components/account/role.tsx` | An account's role as a tag, and the name each role is said by. | keep |
| `LensReadingLine` | [Readings](readings.md) | `components/logs/lens-reading-line.tsx` | A log lens's reading as a `ReadingLine` whose press narrows the log to the lines it counts. | keep |
| `lineDiff`, `diffCounts`, `unifiedBody` | [Values](values.md) | `lib/line-diff.ts` | Two texts as a line diff, its added and removed counts, and a unified body `DiffView` can draw. | keep |
| `useAddressTab`, `ViewTabs`, `StepArrows`, `View` (type) | [Rail](rail.md) | `components/system-users/detail-shell.tsx` | System users' first copy of the detail frame's parts; `useDetailTab`, `ViewTabList`, `StepArrows` from `detail-page.tsx` and `TabView` are them. | retired (0.9.0) |

## Families

| Family | Page |
| --- | --- |
| Page header and identity header | [`page-header.md`](page-header.md) |
| Facts lines | [`values-facts.md`](values-facts.md) |
| Section head | [`section-head.md`](section-head.md) |
| Surfaces | [`surfaces.md`](surfaces.md) |
| Search and toolbar | [`search-toolbar.md`](search-toolbar.md) |
| List row | [`list-row.md`](list-row.md) |
| Card row | [`card-row.md`](card-row.md) |
| Choice card | [`choice-card.md`](choice-card.md) |
| Rail | [`rail.md`](rail.md) |
| Tabs, chips and segments | [`tabs-chips-segments.md`](tabs-chips-segments.md) |
| States | [`states.md`](states.md) |
| Status, tone and tags | [`status-tag.md`](status-tag.md) |
| Values | [`values.md`](values.md) |
| Overlays and confirmations | [`overlays-confirm.md`](overlays-confirm.md) |
| Menus | [`menus.md`](menus.md) |
| Forms and settings | [`forms.md`](forms.md) |
| Readings | [`readings.md`](readings.md) |
| Wiring pictures | [`wiring.md`](wiring.md) |
| Workbench | [`workbench.md`](workbench.md) |
| Feedback | [`feedback.md`](feedback.md) |
| Table | [`table.md`](table.md) |

Adding one: [`../contributing-a-component.md`](../contributing-a-component.md).

The map below is the one that closed `shell.md`, kept for the rules and the history it carries.
The catalogue above is the index; where the two differ, the catalogue and the family page are
current.

## The map carried over from the shell page

A small set of files defines the visual language, and pages compose them rather than hand-rolling
layout. The rules are in [`../standard.md`](../standard.md) and [`../foundations/`](../foundations/README.md); this is the map.

- `components/page.tsx` — `Page` (one measure, gutter and rhythm; `fill` for terminal and logs, whose
  content *is* the viewport), `PageContext` (an accessible page name, the way back as `back`, and the
  walk and help, with no visual page title; a thing's verbs are on its identity line, and list
  commands live in their section or workbench),
  `PageState`; and beside it, each in a file named after it, `section.tsx` (`Section`),
  `toolbar.tsx` (`Toolbar`), `search-input.tsx` (`SearchInput`), `detail-list.tsx`
  (`Metric`/`MetricStrip`, `DetailList`/`Detail`) and `row-link.tsx` (`RowLink`) (`SearchInput` is
  44px and a line of its own below `sm`; `Metric`/`MetricStrip` are deprecated and draw as
  `FormFact`/`FormFacts`). **None of the heading primitives takes a
  `description`** — see rule 5 in [`design-system.md`](../design-system.md).
- `components/panel.tsx` — `Panel`, `PanelHeader`, `PanelToolbar`, `PanelBody`, `PanelFooter`; `Pane`,
  `PaneHeader`, `PaneFooter`; `Well`. A **panel** is *the* content block: a framed surface with a header
  on its own ground and a hairline under it, so a toolbar or full-bleed table sits flush beneath
  without a second edge. `Panel plain` is the same anatomy with no frame, for a list that is the
  whole of a section — but **not for a table**, whose panel keeps its frame because the grid owns a
  scroll region and a boundary you cannot see is one that lies about where the data ends (§2);
  `interactive` is the hover a panel-as-link takes. A **pane** is the same frame,
  for a working region of the page rather than a block of content on it — a session rail, a file
  tree, a log console — and its strips keep a faint tint. Neither lifts; the distinction is semantic
  and shows in how the two are composed and in their header heights.
- `components/row-list.tsx` — `RowList` and `Row`: the list-of-rows you *read*. A leading mark, a
  title, an optional second line and whatever sits at the right edge; a row with `href` is a link
  with a revealed arrow, with `onClick` a button, with neither inert.
  Its counterpart is `ChoiceList`/`ChoiceRow` in `components/choice-row.tsx`, for a row you *take* — a
  project to enter, a site to open, a stack to look inside. Same anatomy, plus an arrow that is
  always drawn and a border that lights under the pointer. Which one a list gets is decided by
  whether its rows are destinations, not by which register the page is in: see §15 pass 3 and the
  last subsection of §16 in `design-system.md`.
- `components/modal.tsx` — `Modal` (the centred task surface) and `PaletteModal` (a search overlay whose
  input is its own header, hung near the top of the window rather than centred). `components/side-panel.tsx` — `SidePanel`, the right-hand detail surface.

  **Which detail views are sheets: a small add or edit form, and nothing else** (D9). An item's
  detail is a page: a route of its own, its views named in `?tab=`, and from `xl` a rail of its
  siblings ([`../recipes/detail-with-rail.md`](../recipes/detail-with-rail.md)); the address that
  opened a sheet redirects to the page. What follows is how the product got there. A sheet was for a
  *glance*: you open it with the list still showing, read it, and close it. A detail that holds a
  **stream, a terminal, or an editor** is not that. You stay in it for minutes, the list behind it is
  dead weight, and a sheet's `sm:max-w-3xl` is about ninety columns of terminal. Those are their own
  destination with a breadcrumb back: `PageContext` with `back` and the verbs on the identity line
  (`IdentityVerbs`), then the resource name and state among the page's facts — an identity line
  (`HostIdentity`) on a container's page and on a stack's, with the verdict at its end. The run page
  (`deploy/run-page.tsx`) goes one step further: since 2026-10-05 it opens on the header its
  project's pages open on (`run-header.tsx`, the project's tile, the commit, the run's state and how
  long it took, one line of provenance),
  because a run is one of the project's Deployments, and that header is the first thing on the
  page — its verbs sit at its far end beside the run's state, and the way back is the rail's panel
  and the menu's Open project rather than an eyebrow. (From 2026-09-24 it opened on the
  `HostIdentity` line the host Overview opens on instead.) A container,
  a compose stack and a backup job went that way on 2026-09-21, and a proxy site
  (`/proxy/sites/<name>`) on 2026-09-27, since it now holds its logs — the way back is a "Sites" link
  beside its verbs, the form and the raw file staying sheets it opens. A process followed in 0.8.6 and a network
  device and a volume in 0.8.7, and since 0.8.7 every item's detail is a page. A sheet that shows a log — a unit's journal, a PM2
  application's output — draws the service logs in their `layout="sheet"` form (no readings, no value
  columns, no facts beside the name), because the unit or the process is still the glance and the log
  one tab of it.

  Their tabs stay **in** the page — they are views of one thing, which is what `ViewTabs` is for.
  Do not reintroduce a route-level strip for them (see the note on route-level strips below), and do not give
  one a `useNavScope`: the rail drills into a *section* with many pages, not into a leaf.

  A sheet's `onOpenChange` is the funnel for guarding unsaved work. Files keeps that quick-edit
  sheet and also offers `/files/editor`, a destination with a collapsible file tree.
  `files/editor-surface.tsx` keeps the text editor's controls identical in both surfaces.
  Opening the destination transfers text/image drafts in memory; file contents never become a browser
  storage preference. The destination guards file switches and links with ordinary confirmation,
  browser unload with `beforeunload`, and cancelable history traversals with the Navigation API.
  Older browsers without cancelable traversal retain the link and unload guards.
  `Modal` and `SidePanel` share one anatomy: title, tinted strip, a body that is the only
  part that scrolls, a footer strip. Both take `actions` in the title strip; `Modal`'s
  `size="full"` is the whole viewport with that anatomy intact, for the one task that is looking
  at a thing rather than filling in a form (the file viewer). Their `description` is rendered `sr-only` — Radix wants an
  accessible description and nothing is drawn. **Raw `Dialog`/`Sheet` are assembled only in those three
  components** — a page or a feature panel never opens one itself.
- `components/tabs.tsx` — the switchers that remain, all of which switch between *views of one page*:
  `ViewTabs` (the one strip of views, for a log pane's Live, History and Insights and the page's own
  views beside them, the packages page's installed, updates, search and log, the deploy wizard's source
  kinds), `ViewTabList` (the strip alone, in a `Tabs` the caller holds) and `useViewTab`
  (`hooks/use-view-tab.ts`, the view held in `?tab=`); `tabClasses`, the quiet tab's look, is
  deprecated; beside it, `components/chips.tsx` holds `FilterChip`, `ChipCount` and
  `ChipStrip`, the run every set of chips sits in (sideways-scrolling on a phone, wrapping from `sm`).
  The route-level strips were deleted in 0.6.7 when the rail started
  drilling into sections; do not reintroduce a strip that changes the URL.
- `components/form.tsx` — what goes inside a task surface: `Field` (a label, a control, one line under
  it — a hint, or the error while there is one), `FieldRow`, `FormSection` (a title and a hairline
  opening part of a longer form), `FormSections` (a run of `FormSection aside`s, each head over its
  fields, held with the hairlines between them to the fields' 48rem),
  `FieldCheck` (one rule a value has to meet, lit as it is met and saying "met" or "not yet" to a
  screen reader) and `FieldChecks` (the polite live region a run of them sits in),
  `useFieldControl` (what a control reads from the `Field` it sits in), `OptionList`/`OptionRow` (a switch with its sentence), `FormFacts`
  (what the form operates on, as data under the title), `Statement` (the SQL a schema-editing form is
  about to run, with a copy) and `FormNote`. The databases section's dialogs are built from these and
  nothing else, and the statement one shows is the server's own plan of the change (`?preview=1`),
  never a page's approximation of it.
- `components/explain.tsx` — `Explain`, the one toggletip for an explanation (ⓘ, dotted term, or
  wrapped control), opened by hover, focus, press or tap. `InfoTip` in `form.tsx` is a deprecated
  alias of it.
- `components/stat-tile.tsx` — `StatTile` (a small name over a 24px figure, an optional meter and one
  hint) and `StatGrid`, which runs them across the page with a hairline between cells and no frame
  around them, every tile the same inset and a lone last tile taking its row. `framed` restores the
  box. A tile that is also a destination is a `Link` around a `StatTile` (`StatLink`, which did
  that, had no caller left and went in 0.9.0); `StatButton` wraps one whose press
  narrows what is under it (a lens's readings wherever a page draws them, a site's request figures)
  with a revealed funnel and `aria-pressed`. `dense` sets the tiles two to a row on a phone.
- `components/outcome-strip.tsx` — `OutcomeStrip`, the last few attempts at something as a square per
  attempt in the colour of how it ended, oldest first: a backup job's runs, a project's runs, a
  channel's messages, a webhook's deliveries, a schedule's firings. One drawing, so a strip means the
  same thing on every page it appears on.
- `components/status-dot.tsx` — `Status`, the one live-state indicator: a coloured dot and a word.
  `components/tag.tsx` — `Tag`, small-caps text marking a *fixed property* of a row. No chip, no border.
  **There is no badge and no pill in this product**, and `design-system.spec.ts` checks that no page
  renders a filled pill. A count is `.numeric` text, a state is a `Status`, a property is a `Tag`, and
  anything longer than three words is prose.
- `components/icon-action.tsx` — `IconAction` (an icon-only row control with a real tooltip label),
  `RowActions` (the cluster of them that appears on hover, stays on keyboard focus and on an open menu,
  and is always visible on touch) and `DimActions`, the same cluster for a row whose controls have a
  column to themselves: always drawn, dimmed until the pointer arrives.
  `rowReveal(group, "control" | "cluster")` is the one reveal; `RowActions` is its cluster form.
- Copying has two kinds of feedback. `copyText` (`lib/clipboard.ts`), called directly with the words
  to announce, raises a toast that says *what* was copied (340 rows as SQL); `useCopy`
  (`hooks/use-copy.ts`) sets a `copied` flag for 1.6 s, for an inline tick that says *that* it worked
  without moving the reader's eye (a chip about to be pasted) or where a toast would sit on what was
  copied (a set of recovery codes). The tick controls are `CopyButton`, `CopyAction` and
  `CopyValue` in `components/copy.tsx` ([values](values.md#copy)).
- `components/finding-list.tsx` — `FindingList`, a `RowList` of findings whose evidence opens in a
  popover off the title (rows never grow). Automatic attention panels, routine settings
  recommendations and navigation attention marks were removed in 0.7.5. Pending-edit marks still
  represent saved changes. Operational error messages and diagnostic evidence remain visible.

**Reach for `Panel`/`Pane`/`Page`, not raw `Card`**, and add a variant there rather than a one-off in a
feature page — before these existed, fourteen pages read as fourteen products.
`components/loading.tsx` (`Spinner`, `LoadingRows`, `LoadingPanel`, `Hold`) and
`components/state.tsx` (`EmptyState`, `EmptyNote`, `ErrorState`, `Notice`) do the same for the
non-happy paths; `LoadingPanel plain` is the same silhouette unframed, for a page
whose blocks arrive plain, and `EmptyState`'s `mark` draws what the list would hold (`ProductLogos`) in
place of the glyph. `min-w-0` on the frame and its children is load-bearing: a wide
table's intrinsic width would otherwise widen the flex column and take the whole shell sideways instead
of scrolling inside its panel.

**A page arrives from the top.** A block whose answer is taller than its skeleton, or that is not on
the page until its read answers, pushes everything under it down when it lands; eight pages moved that
way while they loaded, past a quarter of the window on Certificates. `Hold` (in `loading.tsx`) is the
answer where the loaded height cannot be reserved: `<Hold until={!read.loading} />` after the block
leaves a marker until the read's first answer, and a rule in `globals.css` keeps what follows the marker,
and every block of the `Page` under the one the marker is in, off the page until then. What is held is
mounted, so its own reads start with the page; it is only not drawn, so when it appears nothing that
was already there moves. Its children are what stands there meanwhile; `LoadingRows hold` and
`LoadingPanel hold` are the same for a skeleton that already stands in the block. A hold lets go once
and for good (a filter that makes the read load again takes nothing away) and after four seconds
whatever the read is doing, so a read that hangs does not keep the page from its reader. A band is not
a case for it: a band is drawn during load at its one height (design-system §10). `design-system.spec.ts`
makes the reads of those eight pages land apart, in both orders, at 1280 and at 390, and holds what the
browser's `layout-shift` observer reports under 0.02 (`tests/browser/layout-shift.ts`).

**Nothing lifts.** Cards, buttons, tabs, tiles and the sign-in panel used to carry a `raised` utility —
a gradient gloss, a light hairline on the top edge, a dark lip below, a drop shadow, and an inversion on
`:active`. It is gone. Forty-nine surfaces all claiming to stand in front of the page is a page with no
foreground, and five CSS properties were saying what a one-pixel border already says.

A surface separates by a step of ground (`--background` → `--card` → `--control`), a border, and a
`--hairline` where a header meets a body. `shadow-*` is reserved for the three things that genuinely
float above the page: popover, dropdown, dialog. Press feedback is colour — `active:bg-control-active`
on a control with a face, the accent wash on a ghost — so nothing translates and nothing casts a shadow
to say it was pressed. Since 0.9.1 a control's face carries one lit pixel along its top (`--edge`) in
place of its border, which is the whole of the height it claims ([`../foundations/surfaces.md`](../foundations/surfaces.md#2-nothing-lifts)).

The nav's own treatment did not change with it, because it never used the lift: the current destination
takes the sidebar accent fill, a brand-blue icon and a `font-medium` label against the `font-normal` of
the rest. The rail is the one surface dense enough to need three weights, and the group labels above the
entries are the third. `SidebarMenu`/`SidebarMenuSub` ship at `gap-0.5`.

The brand ships as files in `public/brand-logo/`: the J mark (`just-mark*.svg`, with a `-small` cut
for 16px and under), the horizontal and stacked lockups of the mark with "Just Dashboard"
(`just-dashboard-*.svg`), each in ink, white and brand blue, and the app-icon tiles
(`just-tile-*.svg`). The `just-hub-*` files are the same system for Just Hub and are not drawn by the
dashboard. Anything outside the app — the README, a slide — uses those files as they are.

`components/logo.tsx` is how the app draws them, and nothing else does. `Logo` is the horizontal
lockup with the version as small muted text beside it: the lettering is the brand's own, outlined, not
"Dashboard" set in Source Sans beside a glyph, because the lockup fixes how large the mark is against
the words. It is one colour, `text-brand`, as `just-dashboard-on-dark.svg` draws it on this ground.
`LogoStacked` is the stacked lockup, for the start-up splash where the composition is centred.
`LogoGlyph` is the mark alone and `LogoMark` the mark as the collapsed rail's home link. All of them
are inline `<svg>` on `currentColor` — the same paths as the files, no raster — so they tint with the
palette and stay crisp in the sidebar and on a retina sign-in alike, and the glyph takes whatever
tone the surface it sits on gives it. `LogoGlyph small` draws the `-small` cut: at 16px and under,
the regular mark's gap between tittle and stem antialiases shut, so every call site at `h-4` or
below passes it. This is the only rendering of the product's name, so sidebar, sign-in and splash
agree and a rename is one file.

Where the UI draws the dashboard *itself* as one thing among others — the `just-dashboard` Compose
stack, its containers on a network, the sockets it listens on, the routes and rules it made, the
audit log's machine actors, a backup of its own store — it takes the mark (`DashboardMark` on a
product tile, in `components/logo.tsx`; `LogoGlyph` bare), not the Docker or Caddy logo its images would otherwise resolve to.

The browser icons are `just-tile-ink.svg`: `app/icon.svg` is the file itself, and `favicon.ico`
(16–128px) is rasterised from it, an ink tile rather than the bare mark because the pale mark would
vanish on a light tab strip. `apple-icon.png` is the tile's mark on a full-bleed ink square, because
iOS rounds the corners itself and fills transparency with black.

**Selection is `bg-accent`, everywhere.** The active session in the terminal rail, a pressed
`ToggleGroupItem`, an applied `FilterChip`, a highlighted command row, a selected table row, the current
file in the tree and the current sidebar entry all take the same neutral wash. Neither the primary tint
nor the brand hue is ever the mark for "this one is chosen": the brand is the *command* face and *where
you are*, and a filter borrowing either reads as the page's main action.

`components/ui/*` originated as shadcn/ui (new-york, zinc), with its icons rewired to
the Heroicons vocabulary in `components/icons.tsx`. Compose these shared primitives in features;
product-wide control changes belong in the primitives. Select triggers compose `Button`, while
select, dropdown and context-menu rows share `ui/menu-styles.ts`. `ui/menu-item-text.tsx` keeps
option names and metadata in one text column. The shared select replaces native selectors for
search scope and database activity history. Every side-panel toggle — the mobile navigation sheet's trigger,
the terminal's rail and Files/Diff, the Files sidebar and details, the logs sources, the ER diagram's inspector — draws its panel's side and
state with `SidebarLeftOpen`/`Close` or `SidebarRightOpen`/`Close` (drawn inline in `icons.tsx`,
since Heroicons has no sidebar): a rounded window with a bar inset on the panel's side, solid while
the panel shows and an empty outline of the same bar while it is hidden. They are drawn one pixel
wide on a 16px grid and render at 16px, where every line lands on a pixel. They mean a panel toggle and nothing else, which is why the Files page's
Places menu is a map pin. `ui/context-menu.tsx` is the right-click menu, drawn from `ui/menu-styles.ts`
alongside the dropdown and select so all three read as one menu; a feature that needs both (the file listing) renders one verb list into whichever
opened. Feature pieces live in `components/<feature>/`: `database/`, `docker/`, `files/`, `git/`, `logs/`,
`metrics/`, `packages/`, `procs/`, `proxy/`, `security/`, `terminal/`, `update/`.
