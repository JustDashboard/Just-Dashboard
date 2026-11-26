# Accessibility

What a keyboard and a screen reader get from every page: landmarks, heading levels, names, live
regions, keyboard models and target sizes. A page built from the catalogue gets most of this from
its components. This page says what each one gives and what the page still owes.

The target is WCAG 2.2 AA. The numbers in brackets are its success criteria.

## What a page owes

1. **One `h1`, and no level skipped.** It comes from `PageContext` (drawn for a screen reader only;
   the rail names the page for the eye), from `FlowHeader`'s question, or from `WorkbenchHeader
   heading`. Sections are `h2`, their parts `h3`.
2. **Every control has a name, and the name starts with the words it shows** (2.5.3).
3. **Everything a pointer can do, a keyboard can do**, in the model the control's role promises.
4. **A change the reader did not make is said**, politely, once.
5. **A press box of 24px, and 44px under a finger** (2.5.8).
6. **Nothing is told by colour, position or motion alone.**

## Landmarks

| Landmark | What it is | From |
| --- | --- | --- |
| `banner` | the rail's head; on a phone the strip across the top | the shell |
| `navigation` "Sidebar" | the rail | the shell |
| `main` | the page. One on every screen, sign-in, the welcome page, `StandaloneState` and the starting splash included. It is never focusable | the shell |
| `navigation` named by its list | a detail page's `Rail`; a phone's `PaneBar` | the component |
| `region` named "About …" | the identity line, when it is given a `label` | `IdentityHeader label` |
| `region` | the floating save bar | `FloatingBar` |
| `section` named by its title | a `Panel`, a `PaneSection`, a picture's column | the component |

- **Bypass** (2.4.1). The first Tab on every page is "Skip to content", which lands on the page's
  `h1`.
- **Order.** On a detail page the item comes before its rail in the document, so a keyboard reaches
  the way back, the views and the controls before the rail's search and rows. `DetailPage` places
  the rail on screen with `order`.
- **Where you are.** `aria-current="page"` on the rail's lit row, in the account menu, on a
  `RailRow`, on a `PaneBar` stop and on a route-level link strip. A section or group you are inside,
  seen from a level above, carries `aria-current="true"`.

## Headings

| Level | Who draws it |
| --- | --- |
| `h1` | `PageContext title` (screen-reader only), `FlowHeader question`, `WorkbenchHeader heading`, `PageState` |
| `h2` | `PanelHeader` (default), `Section`, `FormSection`, `SettingSection`, `FlowPanelHeader`, a `PaneSection level={2}` at the top of a page |
| `h3` | `PanelHeader level={3}` (a section inside a section or a dialog), `PaneSection` (default), a `DangerRow`'s title |
| `h4` | `PaneSection level={4}`, under a pane whose own head is an `h3` |

A dialog's title is its name, not a heading in the page's outline. A rail's group name labels its
list (`aria-labelledby`) and is never a heading, because the rail stands before the page's own. An
eyebrow, a tag and a count are not headings. A card's name in a list is not a heading either: forty
`h3`s are noise in the outline, and the list is already a list.

## Names

- **Visible words are the name.** A control that shows words is named by them, or by a name that
  starts with them: "Operator, admin, account menu". An `aria-label` that says something else
  breaks speech input, which presses what it reads (2.5.3).
- **A shortened word keeps the long one.** An identity line's command drawn with its `short` word
  ("Logs") keeps "Open logs" as its accessible name, and on a phone, where it is its icon alone,
  the tip says that name.
- **An icon-only control is named**: `IconAction label`, a `Button` with `aria-label`, and a tooltip
  that says the same words. A `⋯` says whose actions it holds ("More actions for api").
- **Keycaps go beside a button, not inside it.** A shortcut is said with `aria-keyshortcuts`. Axe
  reads visible text and ignores `aria-hidden`, so caps inside a button become part of its name.
- **A field has a label.** `Field` gives its control the id, the label, the hint and the error
  (`aria-describedby`, `aria-invalid`). A group of controls is named by `aria-labelledby`, because
  a `<label for>` cannot name a group: a field of several controls is `Field group`, a
  `role="group"` its label names and its hint describes, each control in it named by its own words.
- **A link says where it goes.** The way on is "All metrics →", not "More". A link inside a
  sentence is underlined at rest (`TextLink`), never told apart by colour alone (1.4.1).
- **A thing drawn as itself still has words.** A product's logo carries the product's name; a mark
  in a phone's picture carries its node's name; a status dot always stands beside its word.
- **A table's columns are named**, the ones with no visible title too (`TableHead label`).

## Live regions

| What changes | How it is said | From |
| --- | --- | --- |
| A state (running, failed) while the reader is elsewhere | polite, once per change, not on first render | `Status announce`, `StatusWords` |
| A failed read | `role="alert"` | `ErrorState` |
| A reading that stopped updating | `role="status"` | `Stale` |
| A fact the reader needs before acting | not announced unless asked: `live="status"`, or `live="alert"` for a danger | `Notice` |
| A field's error | `role="alert"`, tied to the field | `Field` |
| A rule a value has to meet, as it is met | polite, never an alert | `FieldChecks` |
| The save bar's count | polite | `FloatingBar` |
| A console's new lines | `role="log"`, polite | `ConsoleView` |
| A finished action | a polite toast; a failure stays until dismissed | `notify` |
| A dialog's work in flight | the name and the count, not the timer | `OperationProgress` |
| A block that is loading | `aria-busy` on the block | the skeletons |

A figure that ticks every two seconds is not announced. A toast is never the only place a failure
the reader must act on is said: that is a `Notice` or an `ErrorState` on the page.

## Keyboard models

One mark, one model. The strips that looked like tabs once had four.

| Control | Role | Keys | Component |
| --- | --- | --- | --- |
| Views of one page | `tablist`, `tab`, `tabpanel` | one tab stop; ←/→ move and show, Home and End jump | `ViewTabs`, `ViewTabList` |
| Pages of a section | links in a `nav`, `aria-current="page"` | Tab to each, Enter follows | a link strip, never a tablist |
| Filters, several at once | toggle buttons, `aria-pressed` | Tab to each, Space or Enter toggles | `FilterChip` in a `ChipStrip` |
| One of a few short words | a radio group | one tab stop; arrows move and choose | `Segments`, `WindowPicker` |
| A menu of verbs | `menu`, `menuitem`, `menuitemcheckbox` | ↑/↓, first letters, Enter, Escape back to the trigger; a choice closes it | `DropdownMenu`, `VerbMenu` |
| A phone's panes | buttons in a `nav`, `aria-current="page"` | Tab, Enter | `PaneBar` |
| A rail of siblings | links in a `nav`, the one on screen `aria-current="page"` | Tab into the search and the rows; Alt+↑ and Alt+↓ walk it from anywhere on the page | `Rail`, `DetailPage` |
| A list a page owns | rows marked for the page's keys | arrows, Home and End, first letters, Enter opens; Ctrl/⌘+F to its search; `?` for help | `Workspace` |
| A card you open | a link (or a button) | Tab, Enter; inner controls are their own stops | `ChoiceRow` |
| A table row that opens | the row's name is the control (`ROW_NAME`); the row is no stop of its own | Tab to the name, Enter | `TableRow onActivate`, `RowLink` |
| A sortable column | a button in a `th` with `aria-sort` | Enter or Space | `SortHead` |
| A dialog or a sheet | `dialog`; `alertdialog` for a question | focus trapped inside, Escape closes, focus returns to what opened it | `Modal`, `SidePanel`, `ConfirmDialog` |
| An explanation | a button; `tooltip` on hover or focus, `dialog` when pinned | focus opens it, Enter or Space pins it, Escape closes | `Explain` |
| A side column's edge | `separator` with a value | arrows nudge, Home resets | `ResizeHandle` |
| A canvas | the library's own, plus the page's keys | arrows slide it; Enter picks the box or frame the focus ring is on, and the arrows then move it (T10); zoom is buttons too; on the map, the inspector's Move buttons carry the chosen box or the system it reads | `CanvasZoom`, `CANVAS_MOVE`, `MapInspector` |
| A terminal, an editor | theirs | they own their keys; Escape leaves where the page says so; a terminal has a screen reader mode | `XtermPane`, `CodeEditor` |

- **Single keys can be turned off** (2.1.4). One-character shortcuts (`?`, `n`, a name's first
  letters from outside the list) wait for the single-key preference (`SingleKeySwitch`, in every
  help sheet). Chords with Ctrl, ⌘ or Alt, and a list's letters while focus is in the list, keep
  working. Page keys go through `useShortcuts`, which skips them while the reader types or a
  dialog or menu owns the keyboard.
- **Focus is always visible, and one ring draws it** (`focus-ring`). On a canvas the ring is drawn
  on the box or frame itself (`globals.css`, `[data-slot="server-map"] .react-flow__node:focus-visible`),
  so a keyboard reader sees what Enter would pick. Selection never borrows the
  ring's colour, and hover, selection and focus are three different marks
  ([`../foundations/states.md`](../foundations/states.md)).
- **What the eye cannot see is not a Tab stop.** A fact the phone's identity line clips is `inert`
  until the line has room for it ([`../components/page-header.md`](../components/page-header.md)),
  and a detail page's own scroller stops a focused field short of the save bar
  (`data-detail-scroll`).
- **Focus goes somewhere on purpose.** A dialog returns it to its opener; a removed row hands it to
  its neighbour (`useFocusTrail`); a view strip keeps it on the tab.
- **A hover reveal is also a focus reveal**, and on touch the controls are simply drawn
  (`RowActions`, `rowReveal`).
- **What a drag does has two other ways, and they are two criteria.** Keys are the keyboard's
  way (2.1.1): a handle answers arrows, a canvas slides and moves what is picked with them. They
  do not meet 2.5.7, which asks for a way with one pointer and no drag: a click or a tap at a
  time. That is an order changed from a menu, a canvas slid by its zoom buttons, Fit and (on the
  map) a system picked from the rail, and a box or a system on the server map or the databases
  map moved by the inspector's four Move buttons, one longer step a press (`MoveBar` in
  `components/map/inspector.tsx`, spec `server-map.spec.ts`, *a single pointer moves a box and a
  system*). Still keys or a drag only: a table on a database's Diagram, a shape in a drawing, and
  a side column's edge beyond its double-click reset.

## Tooltips and toggletips

A tooltip names a control that has no words. It never holds something the reader needs, because it
does not open on a phone. An explanation is an `Explain`: it opens on hover, on focus and on a tap,
stays while the pointer moves onto it (1.4.13), and Escape closes it.

## Target sizes

- A press box is at least 24px under a mouse and 44px under a finger. Buttons take their height
  from the pointer; small controls (`Switch`, `Checkbox`, dense buttons, an inline text button)
  carry `hit-area`, a larger box that does not grow the row.
- Two press boxes never overlap, and a box never reaches past its scroller
  ([`../foundations/layout-phone.md`](../foundations/layout-phone.md)).
- A field keeps 16px text on a touch screen, whatever class it was given: iOS zooms on less.

## Colour, text and motion

- **Three inks, none faded.** `--faint` keeps 4.5:1 on every ground, and text is never dimmed with
  opacity (1.4.3). A disabled control is the exception the standard allows.
- **A control's edge is 3:1** (T3): inputs, selects, checkboxes and switches (1.4.11). A glyph
  that carries meaning keeps 3:1.
- **A state is a word as well as a colour.** A dot stands beside its word, a failed line is washed
  and says so, in and out are named as well as coloured (1.4.1).
- **Small text grows under a finger** (T1): 12px and 11px where a desk has 11 and 10.
- **Motion can be stopped** (2.2.2, 2.3.3). The system's reduced-motion setting and Pause animations
  in the account menu stop every animation; nothing depends on having seen one
  ([`../foundations/motion.md`](../foundations/motion.md)).
- **The page reflows.** No page scrolls sideways at 390px, and a table is never scrolled sideways
  for its main reading (1.4.10).

## What holds it

| Check | Holds |
| --- | --- |
| `eslint.config.mjs`, `jsx-a11y` recommended, as errors | a field has a label, what a pointer presses a keyboard reaches, no positive `tabindex` |
| `design-system/primitives` (`field-group`) | a `Field` of several controls is a `group`, so no two of them share its id |
| `tests/browser/shell-phone.spec.ts` | the landmarks, the skip link, the current page, label in name in the shell, Pause animations, the phone strip's sizes |
| `tests/browser/field-a11y.spec.ts` | a field's label, hint and error reach its control |
| `tests/browser/touch-targets.spec.ts`, `touch-targets-pages.spec.ts` | press boxes, and that none is cut or leaves its scroller |
| `tests/browser/design-system.spec.ts` | the inks' contrast, hover, selection and focus as three marks, row actions without a pointer, reduced motion, one register a page, a settings page's heading levels |
| unit tests of the components | `SortHead`'s `aria-sort`, `PaneBar`'s roles, the single-key guard (`lib/keys`) |
| `tests/browser/axe-gate.spec.ts` | no serious or critical axe violation on every fixture route, at 1440 and on a touch phone, less the elements `axe-known.ts` lists by route and rule, each named by its tag and `aria-label` with how many of it there are, and each rule with its finding. Its rule set is axe's defaults with AAA contrast and 2.5.8 target size off: target size is held as the 44px press box by the touch-target specs. Every node axe reports is compared, so a new element failing a rule a route already has in the list fails the gate, and an element fixed while still listed fails it until its line comes out or its count comes down. |

What no check sees is review: whether a name says what the control does, whether a change is worth
announcing, and whether the order a keyboard meets things in is the order they are read in.

## Related

[`keyboard-and-place.md`](keyboard-and-place.md), [`identity-line.md`](identity-line.md) (*The
outline a screen reader walks*), [`../foundations/states.md`](../foundations/states.md),
[`../foundations/layout-phone.md`](../foundations/layout-phone.md),
[`../foundations/motion.md`](../foundations/motion.md), [`../shell.md`](../shell.md), and the
Accessibility section of each family page in [`../components/`](../components/README.md).
