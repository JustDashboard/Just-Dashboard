# Spacing

The 4px Tailwind scale stays. A call site picks a role, and the role gives the value. A value
outside its role's column is noise: fold it into the nearest role when the page is next touched.

| Context | Role | Value (px) | Spelled | Home |
| --- | --- | --- | --- | --- |
| Page | gutter | 20, 32 from `md` | `Page` (`px-5 md:px-8`) | `components/page.tsx` |
| Page | section gap | 24, 32 from `md` | `Page` (`gap-6 md:gap-8`) | `components/page.tsx` |
| Page | workbench | 0, 12 from `lg`, no gap | `<Page fill="workspace">` (coming, WP-09; hand-spelled today) | `components/page.tsx` |
| Section | head to body | 12 under the hairline | `PanelHeader plain` (`pb-3`) | `components/panel.tsx` |
| Settings | rhythm | 32 between sections, 16 head to fields, 20 between fields | `SettingsColumn`, `FormSection` | `components/settings/settings-column.tsx`, `components/form.tsx` |
| Surface | gutter | 20 | `px-5` | `Panel`, `Row`, `StatTile` |
| Surface | overlay padding | 16 | `p-4` | sheet, popover, a modal's body, a pane |
| Surface | dialog padding | 24 | `p-6` | `ui/dialog.tsx` |
| Row | height | 44 (`py-3` with a 20 line) | `Row`, `ChoiceRow` | `components/row-list.tsx`, `components/flow.tsx` |
| Row | internal gap | 12 | `gap-3` | `components/row-list.tsx` |
| Row | bleed in a plain panel | 12 | `ROW_BLEED` | `components/row-list.tsx` |
| Table | bleed in a plain panel | the surface gutter | `TABLE_BLEED` (coming, beside `ROW_BLEED`), not `-mx-4`, `-mx-3` or `-mx-2` by hand | `components/row-list.tsx` |
| Inline | glyph to word | 6 | `gap-1.5` | everywhere |
| Inline | word to word in a facts line | 4 | `gap-1` | `FactsLine` |
| Inline | stacked lines (a title over its hint) | 2 | `gap-0.5`, `space-y-0.5` | everywhere |
| Controls | between controls in a toolbar | 8 | `Toolbar` (`gap-2`) | `components/page.tsx` |
| Controls | a field's label to the field | 6 | `space-y-1.5` | `components/form.tsx` |
| Grid | readings, cards | 12, cards 16 | `gap-3`, `gap-4` | per page |

`gap-5` and `px-5` are not noise: 20 is the surface gutter. The noise is the half steps that no role
asks for (`gap-2.5`, `gap-3.5`, `space-y-2.5`), `gap-7` and `space-y-7`, and arbitrary
`p-[2px]`, `p-[3px]`, `gap-[3px]`. About 230 sites carry one; each page pass reviews its own by eye,
because 2.5 goes to 2 or to 3 depending on the row.

| Rule | Held by |
| --- | --- |
| A page's gutter and rhythm come from `Page`, never a page's own padding. | convention |
| A row is 44px in every state. | spec `design-system.spec.ts` (*nothing on … moves*); convention |
| Settings keep the 32 / 16 / 20 rhythm. | spec `design-system.spec.ts` (*a settings page's sections are the level under its title*) |
