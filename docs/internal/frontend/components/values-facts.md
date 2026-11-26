# Facts lines and label lists

How a page says a few facts about a thing: in a line of words under its name, inline as
"label value", or stacked as a list of labels and values.

**Members:** `FactLine`, `Fact`, `facts` (`components/fact-line.tsx`); `DetailList`, `Detail`
(`components/detail-list.tsx`); `FormFacts`, `FormFact` (`components/form.tsx`, the forms family).
**Deprecated, kept until their callers move:** `FactsLine` (`database/workbench/facts-line.tsx`,
now drawn through `FactLine`), the re-export in `deploy/fact-line.tsx`, and `Metric`/`MetricStrip`,
which now draw as `FormFact`/`FormFacts`.

## Which one

| The facts are | Use | Looks like |
| --- | --- | --- |
| a sentence about a thing, under its name | `FactLine` of `Fact`s | 3 sites · 2 on TLS · via tailscale0 |
| figures that need their names, inline | `FormFacts` of `FormFact`s | Entries 1,204   Pending 3 |
| a record read down a column | `DetailList` of `Detail`s | Image   docker.io/n8n:1.98 |

There are two shapes only: inline and stacked (PRM-28). A figure under a caption (`Metric`) was a
tile without its border, so it now reads inline. `StatTile` is the readings family's
([`readings.md`](readings.md)).

## FactLine and Fact

```tsx
<FactLine>
  <Fact mono>{tunnel.subnet}</Fact>
  <Fact>UDP {tunnel.listenPort}</Fact>
  {moved > 0 && <Fact className="numeric">moved {bytes(moved)}</Fact>}
</FactLine>
```

**Anatomy.** Each fact carries its separating dot in front of it, in the gap, and the line is drawn
one gap to the left of a frame that clips it. The dot before a line's first fact falls outside the
frame, whichever fact the wrap makes first. So a wrapped line never ends on a dot. A dot drawn as an
item of its own was left at the end of a line whenever the next fact wrapped ("Cloudflare
credentials saved ·", PGP-01).

- The dot is `--faint`, never a faded `muted/40` (no text ink takes an opacity).
- The dot is real text, `" · "`, so `textContent` reads "3 sites · 2 on TLS". Specs that read a
  header's facts read them this way. It is `aria-hidden`, so a screen reader says the facts and not
  the punctuation. The first fact of a line has no dot at all.
- `Fact`'s `title` goes on its own words, so a tooltip (and a test reading by title) gets the fact
  without the dot. Its `className` is the whole fact's, so a fact hidden on a phone
  (`className="max-sm:hidden"`) takes its dot with it.

**Props.**

- `FactLine({ nowrap?, className, children })`: `nowrap` keeps a single-line row on one line, with
  its facts truncating rather than wrapping (a device's line, a row's second line).
- `Fact({ product?, mono?, title?, className, children })`: `product` draws that product's glyph
  before the words, and `mono` sets a literal from the host (a path, an address) in mono.
- `facts(list)` turns data into `Fact`s, leaving out the empty entries:
  `<FactLine>{facts([count && \`${count} sites\`, tls])}</FactLine>`.

**In an identity header.** `IdentityHeader`'s `facts` written as `Fact`s are drawn through a
`FactLine`. Whatever stands between two `Fact`s and is not one becomes one fact, with its parts 8px
apart. Facts written without a `Fact` are drawn as given. Facts that a component of the page's own
returns cannot be seen from outside it, so that component returns `Fact`s, or the page calls it as
a function (`hubFacts(status)` on the Hub page).

**The phone.** The line wraps between facts and never inside one, unless a single fact is wider than
the line. A row that must hold one line passes `nowrap`.

## DetailList and Detail

```tsx
<DetailList ruled labelWidth="fixed">
  <Detail label="Image" mono after={<CopyButton label="Copy the image" text={image} />}>
    {image}
  </Detail>
  <Detail label="Started">{timestamp(startedAt)}</Detail>
</DetailList>
```

- `DetailList({ ruled?, labelWidth?, size? })`:
  - `ruled` puts a hairline between rows, for a list read as a record.
  - `labelWidth` is `"auto"` (as wide as the longest label; the default) or `"fixed"` (8rem, so
    two lists one over the other line up).
  - `size` is `"xs"` (the default, in a card or a pane) or `"body"` (a list that is the page's
    subject).
- `Detail({ label, mono?, icon?, after?, className, children })`: `icon` is a glyph before the label,
  and `after` sits at the value's end (a copy button, a link) while the value truncates before it.

The local lists that were this with another label width (the database home's fact rows, the map
inspector's and the file preview's facts, the log lens's reading list) are on it now: a ruled list
is `DetailList ruled`.

## Accessibility

- A `DetailList` is a `dl`: label and value are a term and its definition.
- A `FactLine`'s dots are `aria-hidden`. Where a fact's meaning needs more words than it shows, give
  them in `title`.

## What enforces it

- `src/components/fact-line.test.js`: the dot is never first, `textContent` keeps `" · "`, and the
  dot is hidden, in `--faint`, and outside the title.
- `tests/browser/database-data.spec.ts` and `database-schema.spec.ts` read `FactsLine`;
  `deploy-history.spec.ts` and `deploy-deployments-page.spec.ts` read a `FactLine` by its facts'
  titles.
- `eslint-rules/retired-apis.mjs` (`fact-dot`) refuses an import of the old separate dot and of the
  old product fact. `Metric` and `MetricStrip` are not linted; they go when their one caller
  (`network/tools/probe-evidence.tsx`) moves.

## Related

- [`page-header.md`](page-header.md): the identity header whose facts these are.
- [`forms.md`](forms.md): `FormFacts`.
- [`../patterns/readings-in-words.md`](../patterns/readings-in-words.md): why facts are words.
