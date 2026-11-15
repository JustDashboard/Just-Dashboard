# Page header and identity header

The page's opening: the way back, the page's name for assistive technology, and the identity line a
page about one thing opens on (D1).

**Members:** `Page`, `PageContext`, `BackLink`, `PageState` (`components/page.tsx`);
`IdentityHeader`, `IdentityVerbs`, `platformName` (`components/identity-header.tsx`).
**Deprecated, kept until WP-39 bans them:** `HostIdentity` (an alias of `IdentityHeader`),
`HostFact` (use `Fact` with `product`), `FactDot` (see [`values-facts.md`](values-facts.md)),
`PageContext`'s and `PageState`'s `eyebrow` (use `back`).

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

Every detail page places the same things in the same places (PGH-02, PRX-01, PGB-02, PGD-27). The
page passes move the pages that do otherwise.

| What | Where | How |
| --- | --- | --- |
| The way back to the list | the back line, left | `PageContext back={{ href, label }}` |
| Walk arrows (Alt+Up/Down) and the shortcuts' help | the back line, right | `PageContext actions`, hidden below `sm` |
| Mark, name | the identity line | `IdentityHeader mark`/`logo`, `title` |
| State word | beside the name | `IdentityHeader state` (a `Status`) |
| Facts | under the name | `IdentityHeader facts`, written as `Fact`s |
| The one command | the identity line's far end | `IdentityHeader command`, brand face |
| Every other verb | `⋯` after the command | `IdentityHeader menu` |

`IdentityVerbs({ verbs, name })` turns a thing's `Verb[]` into that `command` and `menu`: the first
inline verb is the command, everything else goes in a menu named "More actions for `<name>`".
Spread it in:

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
from the rail (Groups, Defaults, Alerts) carry a back line all or none: that is a content decision
for the page passes. A flow's way out is `FlowHeader back={{ href, label }}`, drawn with the same
`BackLink`, not an eyebrow in capitals.

`eyebrow` on `PageContext` is deprecated. A link passed there is still drawn. A string is not, as
before, but in development it now logs that it was dropped, so the page pass removes it or turns it
into `back`.

## Props

`PageContext({ title, back?, actions?, className? })`: `back` is `{ href, label }`.

`BackLink({ href, children })`: the 12px `ArrowLeft` link, for a way back outside `PageContext`.

`PageState({ title, back?, error?, onRetry?, skeleton? })`: a page that has not got its data, or
cannot get it. Give it `skeleton` in the page's final shape. A page that opens on an identity line
passes `<IdentityHeader loading … />`, not the default `LoadingPanel`.

`Page({ fill?, bleed?, rise?, register? })`: `fill="workspace"` is `h-full`, the whole width, no gap,
and an inset of 12px only from `lg`; `bleed` drops that inset (a remote screen).

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
| `foot` | the command row at the foot wherever the line is narrower than 42rem, not only on a phone: for a line beside a rail |
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
- The identity line gives the name its own row: the tile and the title share the first row, the
  state takes the next, the facts run the full width, and the command and its `⋯` are the foot. Before
  this, "View deployment" beside the address cut it to eleven characters on all 16 deploy project
  pages (MOB-13). A line with only `⋯` keeps it beside the title, because an icon costs the name
  nothing.
- Facts wrap between facts, never inside one, and no dot is left at a line's end (`FactLine`).

## Accessibility

- One `h1` per page, from `PageContext` (or `FlowHeader`'s question on a flow).
- `label` makes the identity line a named region ("About api"); specs and screen readers find it by
  that name.
- The dots between facts are `aria-hidden`; the facts are read as words.
- The command keeps its word on a phone; `⋯`'s name says whose actions it holds.

## What enforces it

- `src/components/identity-header.test.js`: the loading form, the phone foot, the identity rung,
  `IdentityVerbs`.
- `tests/browser/deploy-header.spec.ts`, `deploy-run.spec.ts`, `network-vpn-page.spec.ts`,
  `network-device-page.spec.ts`, `account-ui.spec.ts`: the headers as pages draw them.
- WP-39 adds the lint rules that ban `HostIdentity`, `HostFact`, `FactDot` and string eyebrows.

## Related

- [`values-facts.md`](values-facts.md): the facts line.
- [`rail.md`](rail.md): the sibling rail beside a detail page.
- [`../patterns/identity-line.md`](../patterns/identity-line.md): why a page opens on what it is.
- [`../recipes/detail-with-rail.md`](../recipes/detail-with-rail.md): the detail page these headers
  open.
