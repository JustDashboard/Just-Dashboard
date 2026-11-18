# Menus

A thing's verbs, declared once as data and drawn as icons, buttons or menu items; the dropdown and
context menus they are drawn into; and the selects, from the same menu styles.

**Members:** `Verb`, `VerbSpec`, `VerbActions`, `VerbBar`, `VerbMenu`, `VerbItems`, `VerbItemList`,
`DANGER_AT_REST` (`components/verbs.tsx`); `ui/dropdown-menu`, `ui/context-menu`, `ui/select`,
`ui/menu-styles.ts`, `MenuItemText`.

## A verb is a word

A row's verbs are declared once, as data: the label, the capability already applied, the
confirmation already attached. A surface decides only how many it has room to draw. Two or three go
inline as icons — the ones pressed daily, whose glyphs are conventional — and everything else goes
behind one menu where each verb is its word, one to a line. A verb that needs a sentence to be
understood needs a better word, or a confirmation that carries the sentence
([`../foundations/words.md`](../foundations/words.md#13-a-verb-is-a-word)).

## The verb

`VerbSpec` is a verb as data. `Verb` is a `VerbSpec` with `run`, the form a caller holds when it
presses a verb itself (`verb.run()`); every component takes the wider `VerbSpec`.

| Field | What it is |
| --- | --- |
| `key`, `label`, `icon` | its id, its word, its glyph |
| `short` | a shorter word for a named button in a narrow bar (`VerbBar`, the identity line's command from `IdentityVerbs`), which keeps `label` as its accessible name; the menu says `label` |
| `run` | what pressing it does; a verb with `href` or `submenu` leaves it out |
| `href`, `internal`, `download` | a verb that opens something is a link: through the router when `internal`, saved when `download` (a file name, or `true`) |
| `inline` | drawn as a control of its own in a row or bar; the rest go behind the menu |
| `progressive` | the word a row says while it runs: "Restarting" |
| `danger`, `disabled` | |
| `group` | the part of a long menu it belongs to; a group opens under its name |
| `shortcut` | the key that does the same, said at the item's end |
| `checked` | the current one of a set of choices: a tick, and `menuitemcheckbox` to a screen reader |
| `mark` | a drawing in place of the glyph: a colour's swatch |
| `submenu` | verbs one level down |

A verb that navigates is a link, never a handler calling `router.push`: it can be opened in a new
tab, copied, and middle-clicked.

## The surfaces

- **`VerbActions({ verbs, mode, menuLabel })`** — a row's controls: the inline verbs as
  `IconAction`s, the rest behind `VerbMenu`. `mode` is `plain` (always there), `reveal` (for a row
  whose last column is shared: they appear with the pointer, and are always there with no hover) or
  `dim` (for controls that own their column: drawn, quiet until the pointer is on the row). The
  booleans `reveal` and `dim` are the old spelling, deprecated.
- **`VerbBar({ verbs, menuLabel })`** — a detail surface's controls: the inline verbs as named
  `xs` outline buttons (`short` where given), the rest behind the menu.
- **`VerbMenu({ verbs, align, label, trigger })`** — the overflow menu: the ellipsis, or a named
  `trigger` for a menu that stands alone in a header. A choice closes the menu.
- **`VerbItems({ verbs, kind })`** — the items alone, for a menu whose root is the caller's:
  `kind="dropdown"` or `kind="context"` (one right-click root for a whole listing, say).
  `VerbItemList({ verbs, parts })` is the same with parts of the caller's.

## Danger

A danger verb in a menu is the destructive item, under a hairline from the rest. A danger verb drawn
as a control of its own is in the muted ink at rest at every width and turns destructive only under
the pointer or the keyboard (`DANGER_AT_REST`): a phone has no hover to keep a row's reveal grey,
and a list of dumps showed a red bin on every card. The red is for the moment of pressing and for
the confirmation that follows ([`overlays-confirm.md`](overlays-confirm.md)).

## The phone

Inline verbs keep their 44px press box under a coarse pointer (`IconAction`'s `hit-area`). Reveal
rows show their controls at rest, since there is no hover. A submenu opens beside its item; under
Playwright at phone width the pointer has to move to it in steps, or Radix closes it on the jump.

## Accessibility

The ellipsis is named (`label`, "More actions" by default; `menuLabel` names it after its row where a
surface draws several). Items are Radix menu items: arrows move, Enter chooses, Escape closes and
the keyboard goes back to the trigger. A link item is handed the item's role and keys through
`asChild`. A checked verb is `menuitemcheckbox` with `aria-checked`.

```tsx
const verbs: VerbSpec[] = [
  { key: "logs", label: "Logs", icon: Terminal, href: `/docker/${id}/logs`, internal: true, inline: true },
  { key: "restart", label: "Restart", icon: Refresh, run: restart, inline: true, progressive: "Restarting" },
  { key: "rm", label: "Remove", icon: Trash, run: () => confirm(removal), danger: true },
]
<VerbActions verbs={verbs} mode="reveal" menuLabel={`Actions for ${name}`} />
```

## Not yet on it

`docker/container-actions.tsx` still carries its own copy of the verb and the menu (whose items do
not close the menu on a choice), and Files has a second model of a verb with its own renderer
(`files/file-actions.tsx`); seventeen pages still assemble a `DropdownMenu` and an ellipsis by hand.
They are the copies still to move onto `VerbSpec` and `VerbItems`; a new page starts there.

## What enforces it

- `components/verbs.test.js`: links, checks, submenus, groups, and the danger ink at rest.
- `tests/browser/shared-dropdowns.spec.ts`: the open menus.

**Related:** [`overlays-confirm.md`](overlays-confirm.md), [`list-row.md`](list-row.md) (where a
row's verbs sit), [`../foundations/type.md`](../foundations/type.md#8-type) (menus and selects).
