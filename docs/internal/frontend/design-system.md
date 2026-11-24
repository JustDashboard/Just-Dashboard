# The design system

This page is an index. Until 0.9.0 it held every rule in one 4,852-line file; the rules now live in
the pages below, and the section numbers stay as their ids. About 720 comments in `frontend/src`
cite a section as `§n` (`§14`, `§15 pass 2`, `§16's table`): find the number here and follow it.

Start from [`README.md`](README.md) to build a page, and from [`standard.md`](standard.md) for what
every page is held to. `eslint-rules/design-system.mjs` and `tests/browser/design-system.spec.ts`
hold the parts a machine can check; the rest is judgement, written down in these pages.

## 1. One mode

Dark only, three inks, colours named once in `globals.css`.
[`foundations/colour.md`](foundations/colour.md#1-one-mode).

## 2. Nothing lifts

Grounds, borders and hairlines instead of shadows; the frame (the rail on the window's ground, the
page a sheet on it); a control face's one lit top pixel; which blocks are framed (tables, panes, wells);
`WireFlow` and its loaded shape; the database workbench's one head; press feedback.
[`foundations/surfaces.md`](foundations/surfaces.md#2-nothing-lifts).

## 3. One blue, three jobs

The brand blue's three roles, the status hues, selection as `bg-accent`, the tone tokens, hover's
three answers. [`foundations/colour.md`](foundations/colour.md#3-one-blue-three-jobs).

## 4. One vocabulary per idea

No badge and no pill; avatars; counts, states, tags and tone; latency in words.
[`foundations/words.md`](foundations/words.md#4-one-vocabulary-per-idea).

## 5. No descriptions under titles

The rule and its exceptions, and how a failure is said (`ErrorState`'s kinds).
[`foundations/words.md`](foundations/words.md#5-no-descriptions-under-titles).

## 6. Three states, three mechanisms

Selected, hover and focus; the focus ring; row actions laid out, never overlaid.
[`foundations/states.md`](foundations/states.md#6-three-states-three-mechanisms).

## 7. Which surface

The surface table (`Panel`, `RowList`, `Pane`, `Well`, `Group`, `StatTile`, `ChoiceCard`,
`ChoiceRow`, `RankList`, `BarReading`, `MissingThing`) and the assistant's conversation column:
[`components/surfaces.md`](components/surfaces.md#7-which-surface).
What goes inside a task surface (fields, settings columns, the save bar, sheets, input groups,
`Segments`, `ChipStrip`, confirmations, folds, options):
[`components/forms.md`](components/forms.md#7-which-surface-what-goes-inside-a-task-surface).

## 8. Type

The face, the ladder, the weights, menus and selects, a field at 16px on a phone, view strips, a
form's four ranks, table heads, one centre line, `cn` and the custom classes.
[`foundations/type.md`](foundations/type.md#8-type).

## 9. Radius

[`foundations/surfaces.md`](foundations/surfaces.md#9-radius).

## 10. Charts

`components/metrics/`, chart panels and their summary line, and *The bar reading*.
[`components/readings.md`](components/readings.md#10-charts).

## 11. Motion says one of four things

Live, arrived, indeterminate and level crossed; *Motion that arrived with a library*.
[`foundations/motion.md`](foundations/motion.md#11-motion-says-one-of-four-things).

## 12. A table is a layout, not a contract

When a table becomes cards, where its breakpoint is, rows that are places to go, a lit card on one
line, variables read down their columns.
[`components/table.md`](components/table.md#12-a-table-is-a-layout-not-a-contract).

## 13. A verb is a word

Icon-only controls, declared verb sets, a choice closes the menu, a control says it is changing.
[`foundations/words.md`](foundations/words.md#13-a-verb-is-a-word).

## 14. A header is its title

- The header carries its name and its actions; one `h1`; no icon plots; wayfinding marks; *Any
  page can open as a project does*:
[`patterns/identity-line.md`](patterns/identity-line.md#14-a-header-is-its-title). The identity
  line's rules of 16 Nov 2026 (one row on a phone, the command an icon, no destructive verb in the
  brand face, where `?` goes) are in
  [`components/page-header.md`](components/page-header.md#the-identity-lines-rules).
- A product is drawn as itself (templates, databases, the host, accounts, packages, projects,
  sources, channels, variables, people, the logo files, git glyphs):
  [`patterns/drawn-as-itself.md`](patterns/drawn-as-itself.md).
- A log line and a step's record are read by their shapes; the log panes' filters:
  [`patterns/read-by-shape.md`](patterns/read-by-shape.md).
- The section stories (Security, the proxy, Files):
  [`decisions/proxy-security.md`](decisions/proxy-security.md),
  [`decisions/workspace.md`](decisions/workspace.md).

## 15. Redesigning a page

The reading page's recipe, then its passes, in order, and what the 0.8.7 run settled:
[`recipes/reading-page.md`](recipes/reading-page.md#15-redesigning-a-page). Pass 2 is *Figures are
readings* since 0.9.0. The per-page stories that ran under pass 2 and after the passes are in
[`decisions/`](decisions/README.md), one file per section. *A thing that is not there has one
screen* is in
[`components/states.md`](components/states.md#a-thing-that-is-not-there-has-one-screen);
*Every page of one database opens on the database's header* is in
[`decisions/databases.md`](decisions/databases.md#every-page-of-one-database-opens-on-the-databases-header).

## 16. Two registers

Reading and flow, which register a page is in, what register B adds, the grounds, the lit edge, a
project's Overview in sentences. [`patterns/registers.md`](patterns/registers.md#16-two-registers).

## 17. Redesigning a flow page

The flow page's recipe, then its passes:
[`recipes/flow-page.md`](recipes/flow-page.md#17-redesigning-a-flow-page).

## 18. What a keyboard and a screen reader get

Landmarks, heading levels, names, live regions, a keyboard model for each control, toggletips and
press sizes, and the checks that hold them (lint, the shell and field specs, the axe gate):
[`patterns/accessibility.md`](patterns/accessibility.md).

## The other pages

| Page | What it holds |
| --- | --- |
| [`foundations/`](foundations/README.md) | Tokens and scales: colour, surfaces, type, spacing, motion, words, layout and the phone |
| [`components/`](components/README.md) | The component catalogue, one page per family |
| [`patterns/`](patterns/README.md) | Rules that span components: identity line, picture first, readings in words, sections, the page holds still, rows never grow, settings, confirmations, live data, registers, data flow, keyboard and place, accessibility |
| [`recipes/`](recipes/README.md) | One recipe per page shape: reading, list, detail with a rail, settings, flow, workbench |
| [`decisions/`](decisions/README.md) | Why each page looks as it does: the dated stories, by section |
| [`shell.md`](shell.md) | The shell: routing, the rail, sign-in, the hub link, command search, the editor |
| [`features/`](features/README.md) | Every route area, its files and its specs |
