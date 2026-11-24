# Page header and identity header

The page's opening: the way back, the page's name for assistive technology, and the identity line a
page about one thing opens on (D1).

**Members:** `Page`, `PageContext`, `BackLink`, `PageState` (`components/page.tsx`);
`IdentityHeader`, `IdentityVerbs`, `platformName` (`components/identity-header.tsx`).
**Deprecated, kept until WP-39 bans them:** `HostIdentity` (an alias of `IdentityHeader`),
`HostFact` (use `Fact` with `product`), `FactDot` (see [`values-facts.md`](values-facts.md)),
`PageContext`'s and `PageState`'s `eyebrow` (use `back`), and the `BackLink` export of
`components/detail-page.tsx` (import it from `components/page.tsx`).

## Anatomy

```
┌ PageContext ──────────────────────────────────────────────────────────────┐
│ ← Containers                                         ⌃ ⌄  ?  (walk, help) │  back line
└───────────────────────────────────────────────────────────────────────────┘
┌ IdentityHeader ───────────────────────────────────────────────────────────┐
│ [tile]  name  ● Running                                  [Restart]  ⋯     │  identity line
│         facts · facts · facts                                             │
└──────────────────────────────────────────────── hairline ─────────────────┘
```

- **`Page`** is the frame: one measure, gutter and rhythm. `fill` holds it to the window (`true` for
  terminal and logs, `"xl"` for a flow decided in one view, `"workspace"` for a workbench); `rise`
  is the arrival animation; `register` is `"reading"` or `"flow"` (§16).
- **`PageContext`** renders the page's `h1` (screen-reader only; the rail names the page visually),
  names the browser tab, and draws the back line when there is something on it.
- **`IdentityHeader`** is the line a page about one thing opens on: a tile (`mark`, a product-logo
  id, or `logo`, a picture of its own), the name, its state, the facts under the name, and the verbs
  at the far end.

## The header placement rule

Every detail page places the same things in the same places.

| What | Where | How |
| --- | --- | --- |
| The way back to the list | the back line, left | `PageContext back={{ href, label }}` |
| Walk arrows (Alt+Up/Down) and the shortcuts' help | the back line, right | `PageContext actions`, hidden below `sm` |
| The shortcuts' help on a page with no way back | the identity line's end, after `⋯` | `IdentityHeader menu`, hidden below `sm`; never a row of its own |
| Mark, name | the identity line | `IdentityHeader mark`/`logo`, `title` |
| State word | beside the name | `IdentityHeader state` (a `Status`) |
| Facts | under the name | `IdentityHeader facts`, written as `Fact`s |
| The one command | the identity line's far end | `IdentityHeader command`, brand face; never a destructive verb |
| Every other verb, and every destructive one | `⋯` after the command | `IdentityHeader menu` |

`IdentityVerbs({ verbs, name })` turns a thing's `Verb[]` into that `command` and `menu`: the first
inline verb is the command (the first verb when none is inline), everything else goes in a menu
named "More actions for `<name>`".
The command says the verb's `short` word where it has one ("Logs" for "Open logs") and keeps `label`
as its accessible name. On a phone the command is its icon alone, and the tip on hover or focus
says that name, the words a screen reader hears, never the shorter ones. Spread it in:

```tsx
<IdentityHeader
  label={`About ${site.name}`}
  mark="nginx"
  title={site.name}
  state={<Status tone={tone} label={word} />}
  facts={
    <>
      <Fact mono>{site.root}</Fact>
      <Fact>{plural(site.domains.length, "domain")}</Fact>
    </>
  }
  {...IdentityVerbs({ verbs, name: site.name })}
/>
```

Verbs never go on the back line as a row of buttons. That row wrapped to two lines above the name
on a phone (MOB-12), and it put the page's command in a second place.

**Back links.** An item page one level under a list carries `back`, its one way back. A pane that a
row opened inside a workspace uses `PaneBack` (`components/phone-panes.tsx`), which switches panes
rather than following a link; the two are different jobs and stay separate. Section pages reached
from the rail (Groups, Defaults, Alerts) carry a back line all or none. A flow's way out is `FlowHeader back={{ href, label }}`, drawn with the same
`BackLink`, not an eyebrow in capitals.

`eyebrow` on `PageContext` is deprecated. A link passed there is still drawn. A string is not, and
in development it logs that it was dropped: remove it, or turn it into `back`.

## The identity line's rules

The operator's decisions of 16 Nov 2026. They hold on every page that opens on an identity line,
whatever its shape, and each recipe's checklist carries them.

1. **A phone's header is one row.** The tile, the name and the verbs are one row, and the facts are
   one line under it. The line clips, so danger and warning facts come first: write them first in
   `facts`. Never add a row to fix a truncated name; cut a fact instead.
2. **A command on a phone is always its icon**, with a tooltip and its words as its accessible
   name. The words return from `sm` up. That covers a split command (a button with a menu on its
   side) too: both halves are icons on a phone.
3. **No destructive verb takes the brand face.** Terminate, Kill, Delete, Drop, Remove and the like
   go behind the `⋯`, in the destructive ink. A page whose only verbs are destructive draws no
   command: pass `menu` alone. `IdentityVerbs` holds this for you: a `danger` verb never becomes
   its `command`, and nothing is promoted in its place. When the inline verb is destructive, or
   every verb is, it returns `menu` alone (unit `identity-header.test.js`).
4. **`?` sits at the end of the identity line**, after the `⋯` (in `menu`, as Packages does, or as
   `aside` on a line with no verbs), hidden on a phone. It gets a row of its own only where it
   shares that row with a back link: on a page with `back`, it is on the back line
   (`PageContext actions`, which `DetailPage` fills).
5. **A section page names the one thing it is about when there is one** (ufw, systemd-resolved,
   nginx), otherwise the section: "Traffic", with the uplink as a fact, not the uplink's name.

## Props

`PageContext({ title, back?, actions?, className? })`: `back` is `{ href, label }`.

`BackLink({ href, children })`: the 12px `ArrowLeft` link, for a way back outside `PageContext`; a
long list name ends in an ellipsis. It is the one way back: `PageContext`, `PageState`, `FlowHeader`
and `DetailPage` all draw it. The `BackLink` export of `components/detail-page.tsx` is a deprecated
name for this one, so import it from `components/page.tsx`.

`PageState({ title, back?, error?, onRetry?, skeleton? })`: a page that has not got its data, or
cannot get it. Give it `skeleton` in the page's final shape. A page that opens on an identity line
passes `<IdentityHeader loading … />`, not the default `LoadingPanel`.

`Page({ fill?, rise?, register? })`: `fill="workspace"` is `h-full`, the whole width, no gap and no
inset at any width, because the sheet is a workbench's frame and the workbench draws none of its own
([`../foundations/surfaces.md`](../foundations/surfaces.md)). `bleed` is gone with the 12px inset it
dropped (0.9.1).

`IdentityHeader`:

| Prop | Use |
| --- | --- |
| `mark`, `fallback` | a product-logo id, and the glyph a thing with no product keeps. The glyph draws at a logo's size on the tile (20px on a 40px tile, 24px on 48px) |
| `logo` | a picture of its own (an account's avatar, a project's mark) in place of the tile |
| `title` | the name, at the identity rung: `text-base` 16/24, weight 600 |
| `state` | the `Status` beside the name. With `state`, `command` or `menu` the line takes the project layout |
| `facts` | the line under the name. `Fact` children are drawn through a `FactLine`; anything else as given |
| `command`, `menu` | from `IdentityVerbs` |
| `aside` | the far end of a line with no state: a health verdict |
| `bar` | the compact framed bar (Proxy overview) |
| `foot` | the command on a row of its own, at the line's foot, wherever the line is narrower than 42rem from `sm` up: for a line beside a rail. On a phone the verbs keep the name's row |
| `label` | the landmark name ("About api"); set, the line is a region |
| `slot` | the `data-slot`, default `host-identity`, for specs that know a header by another name |
| `loading` | bones in the final boxes (below) |

## States

**Loading.** `loading` draws every part not given as a bone in the box the words will take: the
title as a `Bone` in its own line height, the facts as a 16rem bone, a `Skeleton` tile when no
`mark` or `logo` is known. `state` and `command` passed as `true` hold their places too. A title or
a mark that is already known is drawn as itself. The line is `aria-busy`. The page has its final
shape while it loads and nothing under the line moves when the words land (D11).

```tsx
<IdentityHeader loading slot="project-header" state command />   // a project, before its read
<IdentityHeader loading mark="wireguard" state />                // a tunnel
<IdentityHeader loading title={<span className="font-mono">{name}</span>} state />
```

**Empty and unavailable** pages are `PageState` with an error, or the states family
([`states.md`](states.md)).

## The phone

- The back line keeps the back link. The walk and the help are desk tools: put them in a span with
  `max-sm:hidden`, as the container page does. The thing's verbs are on the identity line, so
  nothing on the back line wraps.
- The identity line is one row and one line of facts. The row holds the tile a step smaller (32px),
  the name, and the verbs at the far end as icon buttons: the command drawn as its icon alone, its
  words kept as its accessible name and said in a tooltip, then `⋯`. Each verb is a 32px square
  with a 44px press box (`hit-area`), 12px apart so no two boxes overlap, and the row does not grow
  for them. The name stays whole when it fits; it is the last thing to give way, and truncates only
  when it is too long for the row on its own (MOB-13: "View deployment" beside the address cut it to
  eleven characters on every deploy project page).
- The state stands after the name when both fit, and otherwise leads the facts line. The line
  measures that before it paints, so it never shows the state in one place and then the other.
- The facts are one line that never wraps. A fact that does not fit is left off whole, at a fact's
  edge, never cut inside a word. A fact that only states the expected answer ("Auto-deploy on" when
  it is on) is not on a phone's line at all: it takes `max-sm:hidden` at its call site, so the line
  keeps the facts that differ.
- The row has a control's height and the facts a line's whatever is in them, so a loading line is
  as tall as the line it becomes, with nothing reserved. The verbs' cell is 32px tall even with no
  verb in it, so a tile smaller than 32px (an account's mark) does not leave a loading row short of
  the loaded one. `menu={false}`, `command` passed as `true`
  while `loading`, and `foot` still work; on a phone none of them adds a row.
- Off a phone, facts wrap between facts, never inside one, and no dot is left at a line's end
  (`FactLine`).

## Accessibility

- One `h1` per page, from `PageContext` (or `FlowHeader`'s question on a flow).
- `label` makes the identity line a named region ("About api"); specs and screen readers find it by
  that name.
- The dots between facts are `aria-hidden`; the facts are read as words.
- On a phone the command is drawn as its icon, and its words stay in the button as its accessible
  name and come up in a tooltip on hover and focus; `⋯`'s name says whose actions it holds. The
  tooltip stands beside the command, never around it, and listens on the command's own box in the
  page: a command that is a popover (Connect) opens, closes on Escape and returns the focus exactly
  as it does anywhere else, and nothing pressed inside the popover reaches the tooltip.

## What enforces it

- `src/components/identity-header.test.js`: the loading form, the phone's one row, the identity rung,
  `IdentityVerbs`.
- `tests/browser/deploy-header.spec.ts`: on a phone the header is one row over one line of
  facts, no taller than before the verbs had a row, still while it loads, with the name whole where
  it fits.
- `tests/browser/deploy-header.spec.ts`, `deploy-run.spec.ts`, `network-vpn-page.spec.ts`,
  `network-device-page.spec.ts`, `account-ui.spec.ts`: the headers as pages draw them.
- `tests/browser/design-system.spec.ts` (*… keeps the identity line's rules*, at 1440 and on a touch
  phone): no destructive verb in the brand face, and on a phone a command that is its icon with a
  name. `tests/browser/axe-gate.spec.ts` reads every route's header with the rest of the page.
- WP-39 adds the lint rules that ban `HostIdentity`, `HostFact`, `FactDot` and string eyebrows.

## Related

- [`values-facts.md`](values-facts.md): the facts line.
- [`rail.md`](rail.md): the sibling rail beside a detail page.
- [`../patterns/identity-line.md`](../patterns/identity-line.md): why a page opens on what it is.
- [`../recipes/detail-with-rail.md`](../recipes/detail-with-rail.md): the detail page these headers
  open.
