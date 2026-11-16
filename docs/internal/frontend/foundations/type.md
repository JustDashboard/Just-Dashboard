# Type

## Reference

The face is Source Sans 3. Every step has its own line height, so a line's box never comes from
an ancestor.

| Role | Utility | Size / line (px) | On a touch screen | Weight | Ink | For |
| --- | --- | --- | --- | --- | --- | --- |
| micro | `text-micro` | 10 / 14 | 11 / 15 | 500 (600 in `.eyebrow`) | muted, faint for axes | eyebrow, caps tag, axis tick, chip count |
| hint | `text-hint` | 11 / 16 | 12 / 16 | 400 (500 for a table head) | muted | a row's second line, a field hint, a facts line, a table head, a caption |
| xs | `text-xs` | 12 / 16 | same | 400 | foreground or muted | a table cell, a status word, a tooltip |
| body | `text-body` | 13 / 20 | same | 400 reading, 500 a name | foreground | a row title, a field, a select, a menu item, a button label |
| sm | `text-sm` | 14 / 20 | same | 500 | foreground | an option row's title, and nothing else |
| title | `text-title` | 15 / 22 | same | 600 | foreground | a surface's title and a page section's head (with its hairline) |
| identity | `text-base` | 16 / 24 | same | 600 | foreground | the identity header's name |
| display | `text-2xl` | 24 / 32 | same | 600 | foreground | a flow page's question, a stat figure, a one-time code |

"On a touch screen" is `pointer: coarse` (T1, adopted 15 Nov 2026): `text-hint` reads 12px and
`text-micro` 11px there, through the tokens, so no call site writes it. Desktop is unchanged.

**Weights.** Nothing is bold.

| Weight | Utility | Is |
| --- | --- | --- |
| 400 | `font-normal` | reading text, secondary text, values |
| 500 | `font-medium` | a thing's name: a row title, a field label, a button, a tab, a table head |
| 600 | `font-semibold` | a head (surface, section, identity, display) and the eyebrow |

A `font-normal` is a reset only where a primitive bakes a weight in (`Button` is 500, so a select
trigger that composes it resets to 400). Semibold on `text-hint`, `text-micro` (outside
`.eyebrow`), `text-xs` or `text-body` emphasis is off the rule: a head is a head's size, and
emphasis in a sentence is the name's 500.

**Caps labels.** `.eyebrow` (10, 600, 0.14em, muted) opens a section, a panel header or a tile.
`.caps` (10, 500, 0.06em, uppercase, faint) is a `Tag`'s face and every copy of it.

**Figures.** `.numeric` wherever figures are compared (tabular figures), and by default in table
cells. `tracking-code` spaces a one-time code.

| Rule | Held by |
| --- | --- |
| Only the steps above; no `text-[Npx]`. | lint `type-scale`; coming: `text-lg`, `text-xl` refused outside their role (WP-39) |
| A field is 16px on a touch screen whatever its caller passes (iOS zooms on a smaller one). | spec `touch-targets.spec.ts` |
| No text ink faded with opacity. | coming: lint `ink-opacity` (WP-39) |
| No sentence under a title. | type |

## 8. Type

The face is **Source Sans 3**, self-hosted from Adobe's
[3.052R release](https://github.com/adobe-fonts/source-sans/releases/tag/3.052R). The upright and
italic variable WOFF2 files sit in `src/app/fonts/` with their SIL Open Font License. `next/font/local`
loads both in `app/layout.tsx` at weights 200–900 and emits `--font-source-sans-3`; `--font-sans` in
`globals.css` puts it ahead of the system fallback. Nothing at build or run time reaches out to a
font CDN. **Only the upright is preloaded.** The two faces are declared apart (`app/font.ts`,
`app/font-italic.ts`) because one `localFont` call preloads every file it names, and the italic's
138 KB then raced the page's JavaScript on each first visit for a face most screens never draw; it is
fetched when italic text first appears. Both declarations assign to a constant named `sourceSans3`,
which is what the build names the family after, so they are one family and `font-style: italic` picks
the italic file with nothing else said; `app-states.spec.ts` checks that, and that the response preloads
the upright alone. The files are whole, not subset to Latin: the licence reserves the name "Source", so
a subset is a modified font that could not keep it. `--font-mono` stays fixed-width for terminals, source code, and technical identifiers.

**Glyphs are laid out at their real widths.** Chromium on Linux hints a web font and rounds every
glyph's advance to a whole pixel, and against the body's −0.02em tracking that broke the 11–13px
lines into uneven pieces — "No tes", "Emp ty", a hint reading "Howitis listed" — at any device
scale. `body` sets `text-rendering: geometricPrecision`, which lays glyphs out at their true
advances there; macOS and Windows already did.

The ladder is `text-micro` (10) → `text-hint` (11) → `text-xs` (12) → `text-body` (13) →
`text-sm` (14) → `text-title` (15). An arbitrary `text-[Npx]` is a departure from it, and the linter
says so.

**`text-title` is what a surface calls itself**: a `PanelHeader`, a `Section`, a `Modal`, a
`SidePanel`. It was `text-body` on the first two, which put a panel's name at the size of the rows
underneath it — legible only because a tinted icon square was sitting in front of it doing the
separating. With the square gone (§14) the title has to be the thing that reads as a heading, and two
pixels is the whole of what that takes: `text-title` is a step the ladder already had, so the visible
ranks run a flow question (24, `text-2xl`) or a reading (24) → section and surface (15,
`text-title` 600, a section with a hairline) → body (13) with nothing invented in between. The reading page title went from 20 to 24 in
0.6.7, then left the visual layout when the rail became the visible page location. A `StatTile`'s
figure stays at 24 because a headline number is what a reader finds without reading.

Weight carries hierarchy where size cannot. The sidebar is the one surface dense enough to need
three: group labels in the eyebrow's small caps, resting entries `font-normal` so the column reads as
a list rather than as forty-nine headings, and the current entry `font-medium` alongside its accent
fill and brand-blue icon. A panel's head speaks in the eyebrow when it names one of this product's
sections, and not when it names something the reader called — a project, a database connection:
small caps turned `api-production` into API-PRODUCTION, which is the corruption §4 keeps literal
strings out of caps for, so a scope's head is printed as written at `text-hint` semibold, after its
own mark (the project's favicon or product, the connection's engine) at the line's height.

A menu reads the same way at a smaller size: a `DropdownMenuLabel` or `SelectLabel` is an
eyebrow over the group it names, and every option and action is `text-body`. Selects, action menus,
right-click menus and form popovers share the surface and opening motion in `ui/menu-styles.ts`;
menu rows are at least 32px on desktop and 44px on a phone, with the same hover wash, disabled state and
right-hand check for a selected option. Reduced motion removes the opening animation.

`SelectTrigger` composes the shared outline `Button`, so its border, focus ring and press feedback
are the button's. Its chevron turns on the trigger's own `data-state`: it keyed off
`in-data-[state=open]`, which matches any open ancestor, so every select inside a dialog or a sheet
pointed up while it was shut. A select opens below its trigger with a 4px gap, aligned to its starting edge;
Radix can flip it above or constrain it to the viewport when space runs out. Its width accommodates
the trigger and its options, without an ownership-specific minimum width. Arrow keys, typeahead,
Home/End, Escape, focus restoration and scrolling remain the primitive's responsibility.

**No option's value is `""`.** A choice of nothing has its own word — `none`, `auto`, `prefix` —
mapped back to empty by the caller. `Select` never passes `""` to `onValueChange`. Inside a form,
Radix keeps a hidden native `<select>`. When the value is set to one it has no `<option>` for, it
reads that value back as `""` and reports it as a choice. Before 0.8, Discard on a deploy settings
page restored the Source's GitHub App credential, which the Git credential picker does not offer.
The picker turned the reported `""` into credential 0, and the form stayed dirty however often
Discard was pressed.

**An option has one text column.** `MenuItemText` draws an option's name and any useful metadata
beneath it, never at the far end of a second column. `SelectItem` uses this for its `hint`, outside
Radix's item text, so metadata is excluded from the option's accessible name and the value copied into the
closed trigger. `CredentialSelect` keeps the host's glyph beside the name and the host and kind
below it. File locations, terminal snippets, database switchers, saved queries and export menus use
the same text component. Keyboard shortcut hints retain their end alignment. A one-word property
stays on the option's own line instead: a column picker's option (the Data page's Sort and Columns
menus) is the column as its grid header names it, the name and then its type as a mono `Tag`, and a
schema picker's option ends on the schema's count where the rail under it ends its rows' figures.
Beneath the name, each was a few characters on a line of their own, which made every option twice
the height of the rows it was chosen beside.

`OwnershipSelect`, shared by deployment domains, mounts and volume dependencies, offers only
Managed, Linked and (where supported) Observed in the menu. Its button shows the chosen word;
the selected removal consequence stays beside it and is connected with `aria-describedby`.
Search scope and database activity history also use the shared select, rather than native controls.

**A field is 16px on a phone and under a finger.** `Input`, `Textarea` and `SelectTrigger` write
`max-sm:text-base pointer-coarse:text-base` after the caller's classes, so a caller's
`font-mono text-xs` or `text-body` sizes the field only under a mouse from `sm`: iOS zooms the
page into any field under 16px that takes focus and does not zoom back out. `SearchInput`
follows it — the search box had pinned `text-body` over it. From `sm` the select's text is `text-body`, where it
was 14px throughout, so an `Input` and a `Select` in one `FieldRow` read at one size. The search box
is 40px on a phone, beside the 40px compact `Select` it usually shares a toolbar with, and takes a
wrapping toolbar's first line to itself: a call site's `flex-1` had squeezed the projects search to
95px beside its chips. The same pass sized the rest of a phone for a finger. A dialog's or a sheet's
close button is 40px below `sm` and 32px from it, and answers the pointer with a wash rather than an
opacity step; it was a bare 16px glyph, the smallest thing to hit on a surface whose fields are
44px. A run of figures with their names (`FormFacts`, which the deprecated `MetricStrip` draws
as now) is two columns on a phone rather than a wrapping row, because wrapped, the first figure of
each new line kept the rule that belonged beside its neighbour on the line above. And
`IdentityHeader` sets its tile and title on one row with one line of facts beneath them, where
they had run down a narrow column beside the tile. These are primitives, so every page that
draws them — Docker, Security, Audit, the account pages and the rest — changed with the deployment
section, not only `/deploy`.

A **view strip** — the underlined tabs that switch between two readings of the *same* page — is
`text-body`. It was `text-xs` while the page title was 20px and the strip sat within two pixels of both
the title and the panel titles; the earlier 24px title gave the strip a rank of its own, and 12px
chrome under it read as an afterthought. There is no route-level strip to size: since 0.6.7
the sidebar drills into a section and lists its pages, and a tab that changes the URL is not a thing
this product has.

A **form runs four ranks**: section (15, semibold) → option (14, medium) → field label (13, medium)
→ hint (11, muted). `FormSection` opened with the eyebrow's 10px muted small caps, so a Configure
screen of nine sections set every head *quieter than the 13px field labels it opened* and the
loudest line in each block was a label — the same argument the paragraph above makes about a panel's
name, one rung down and one degree worse. The head went to 14 first and 14 was not enough: an
`OptionRow`'s title is 14 so that it outranks the fields it governs, which left "Public address" and
"Publish on a public hostname" two lines apart at one size with a weight step between them and
nothing else. At 15 the four steps are visible and every one of them is a rung the ladder already
had. A `Disclosure` takes the section's rank, because a fold is a section (§7). A page section's
head names a part of the page rather than of a dialog: it is the same 15/600 head, with no rule
of its own because `FormSections` draws the one between sections; its state (the deprecated
`hint`) is 12 (§7).

A **table header** is `text-hint`, medium weight, muted — not the eyebrow's small caps. At 10px
tracked-out caps a nine-column header was the loudest line in the table, above rows it exists only to
name. It is opaque, so rows scroll *under* a sticky header rather than through it, and the ground it
is opaque with is `--panel-ground` — declared by the panel (`--card` framed, `--background` plain),
never assumed by the table. A card that frames a table without being a `Panel` (the Files browser)
declares `--panel-ground` itself; left undeclared, the header is transparent. Reading `--card` there put a faint unexplained band across every table on
a plain panel, overhanging the header hairline by the table's own `-mx-4` bleed.

**A row's text shares one centre line.** A bare `span`, `div` or `Link` placed in a flex row is a
block, and a block keeps the line box it inherits — the page's 16px, 24px-tall one where nothing
set another. A 12px `Status` or `Tag` inside it rests on that box's baseline, three pixels below the
button beside it: the database strip's "connected", a deployment's route and certificate columns,
a container card's state, a release note's kind and the identity line's aside all shipped that way.
A wrapper around an inline status is a flex box (`flex`, or `flex flex-col items-start` for a state
over its detail), or it carries its content's own type size. A glyph beside a title is nudged by
the title's line box, not by habit: `mt-0.5` centres a 16px glyph on a 20px line and drops it two
pixels below a `leading-tight` one. A field and its button in one row are `items-center`, since the
field is 44px on a phone and a compact button is 40. A view strip's tabs carry `pt-0.5` against their
2px underline, so their labels sit on the strip's centre line with whatever shares the strip.
`design-system.spec.ts` checks the first of these on every surface it opens.

`.eyebrow` is the small-caps label that opens a section, a panel header or a stat tile. `.numeric` is
any figure meant to be compared with the one above it — tabular digits stop a polling table from
shimmering.

**`cn` has to be told about these names, and `lib/utils.ts` is where that happens.** tailwind-merge
resolves conflicts by looking each class up in a table of groups built from stock Tailwind. The four
custom steps are not in it, so `text-micro` fell through to the generic `text-{colour}` matcher and
was treated as a colour — which meant any element carrying both a size and a colour silently lost the
size and rendered at the browser's default 16px. Every `Tag` in the product did exactly that, which is
why they looked oversized while the markup was correct. Adding a step to the scale means adding its
name to the `font-size` group in `extendTailwindMerge`, or it will not survive a `cn()`.
