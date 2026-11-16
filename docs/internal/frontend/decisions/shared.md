# Decisions: Across the product

Why these pages look as they do. The stories were told in `design-system.md` §14 and §15 until 0.9.0
and are moved here verbatim, under a heading per page. They are history: a rule they state is in
[`../standard.md`](../standard.md) or the page it links to, and where a story and the standard
disagree, the standard wins.

## Names that no longer exist

Reference pages do not use these. They appear in the stories here and in the other files of this
folder, because that is when they existed.

| Name | What it was | Gone |
| --- | --- | --- |
| `StatusBadge` (removed), `ui/badge.tsx`, `card.tsx` | the badge and the card primitives | the September 2026 unification pass |
| `SectionNav` (removed), `TabLink` (removed) | route-level tab strips | 0.6.7, when the rail began to drill into sections |
| `hooks/use-theme.tsx`, `/appearance` | the light theme and its switch | when the product became one mode |
| `lib/docker-templates.ts`, `create-container.tsx` | Docker's own create flow | 2026-09-12 and 2026-09-15; `/deploy/new` creates |
| `AccountCard` (removed) | System users' account card | 0.8.2, when the list became a table again and an account a page |
| `TopBar` (removed; `components/top-bar.tsx`) | a top bar nothing rendered | 0.9.0, with the file moves |
| `StatLink` (removed) | a stat tile that was also a link | 0.9.0, with no caller left; a `Link` around a `StatTile` |
| `ShareBar` (removed) | a bar with the rows it shared out under it | after 0.8.5, for `BarReading` (§10) |
| `StandingBar` (removed) | a bar of standings with rows under it | after 0.8.5, for `BarReading` |
| `container-table.tsx` | the Docker overview's table of every container | 0.8.7 |
| `stack-table.tsx` | a stack's containers as rows under it | 0.8.7, for a stack's own page |
| `posture-strip.tsx` | Security's strip of seven posture checks | 0.7.5, with the automatic findings |

## Attention sections removed (0.7.5)

Since 0.7.5, automatic Attention/Needs attention sections, recommendation comparisons, Advisor and
navigation attention marks have been removed. Historical layout examples
below describe the earlier pages; their attention blocks are no longer part of those page passes.
Requested diagnostic reports and operational validation remain.
