# Status, tone and tags

A live state is a dot and a word (`Status`). A fixed property is a caps word (`Tag`). Both take
their colours from one module, `components/tone.ts`.

**Members:**

- `Status`, `StatusDot` and `StatusWords` in `components/status-dot.tsx`.
- `Tag` in `components/tag.tsx`.
- `Tone`, `DotTone`, `OutcomeTone`, `Verdict`, `toneFor`, and the fill and ink maps, in
  `components/tone.ts`.
- `OutcomeStrip` in `components/outcome-strip.tsx`.
- `StateMark` and `StateCorner` in `components/product-logo.tsx`.

## Which one

| The thing to say | Use | Not |
| --- | --- | --- |
| The state of something now: running, stopped, failed, syncing | `Status` | a `Tag`, a pill, a coloured word on its own |
| A property that does not change while you look: `ed25519`, `read-only`, `self-signed` | `Tag` | a `Status`, a bordered chip |
| A literal string from the host: a cipher, a port map, a hash | `Tag mono` | caps, which would corrupt it |
| How the last few attempts ended | `OutcomeStrip` | a row of dots |
| What a thing is plus its state, on a list or a header | `StateMark` | a tile with a dot typed by hand |
| A count | `.numeric` text beside its label | any of these |
| More than three words | prose, in the row's second line or a `Notice` | a long `Tag` |

**There is no badge and no pill in this product.** `design-system.spec.ts` fails a page that renders
a filled pill.

## The tone vocabularies

`components/tone.ts` holds three unions. Each answers a different question.

| Union | Values | Answers |
| --- | --- | --- |
| `Tone` | `default`, `success`, `warning`, `danger` | The colour of a word or a figure. `Tag` and the readings take it. |
| `DotTone` | `running`, `stopped`, `warning`, `danger`, `notice`, `unknown` | The state of a thing. A status dot is drawn in it. |
| `OutcomeTone` | `success`, `danger`, `warning`, `running`, `muted` | How one attempt ended, for `OutcomeStrip`. |

Map from one to another with the tables, never with a copy of them:

- `toneFor(state)` reads Docker's, systemd's and pm2's state words.
- `VERDICT_TONE` reads the four-value verdict an assessment sends (`ok` … `critical`).
- `TONE_DOT` turns a `Tone` into a `DotTone`, for a reading that holds a `Tone` and draws a dot.

The fills and inks live next to the unions:

- `DOT_TONE` is the dot's fill.
- `DOT_COLOR` is the same fill as a CSS colour, for a line or a chart.
- `TEXT_TONE` is the word's ink. `running` is plain foreground, because a column of green
  "running" is as hard to scan as a column with no signal.
- `ICON_TONE` is `TEXT_TONE`, except that `running` is green. A lone icon carries the whole signal.
- `OUTCOME_FILL` is an attempt's fill. Its three answers are the dot's own fills.

`toneFor` maps these states:

| State words | Tone |
| --- | --- |
| running, active, online, enabled, success, open, connected | `running` |
| paused, restarting, activating, deactivating, launching, stopping, connecting, reconnecting | `warning` |
| failed, errored, dead, unreachable | `danger` |
| exited, stopped, inactive, created, closed, disconnected | `stopped` |
| anything else | `unknown` |

A failed thing reads red. It is the state a reader opens the page to find, and it must not share
amber with a thing that is only starting. `unknown` is a hollow ring rather than the grey `stopped`
dot, so a reading nobody has made yet does not look like one that says the thing is off.

## Status

**Anatomy.** A 6px dot (or an icon the caller passes), a 6px gap, and the word in `TEXT_TONE`.
There is no border and no ground.

**Props.**

- `state`: a state word, read by `toneFor`.
- `verdict`: an assessment's verdict, read by `VERDICT_TONE`.
- `tone`: the `DotTone` directly, for a caller that already holds one. Of the three, `tone` wins,
  then `verdict`, then `state`.
- `label`: the word to show. It defaults to `state`, then "unknown".
- `live`: the dot breathes. Only for a row fed by an open socket. A polled figure is not live.
- `announce`: the word is said in a polite live region when it changes. A string names what the
  state belongs to ("Container state"). Only for the one subject of a page; see Accessibility.
- `icon`: an icon in place of the dot, in `ICON_TONE`.
- `className` and the other span props reach the element, which carries `data-slot="status"`.

```tsx
<Status state={unit.activeState} />
<Status verdict={posture.status} label={POSTURE_WORD[posture.status]} />
<Status tone={tone} label={words} live={streaming} announce="Container state" />
```

## StatusDot

The dot alone. Use it where the word stands somewhere else: a table's state column whose header
names it, or a mark's corner.

**Props.** `state` or `tone`, `live`, `corner`, `label`, `className` and span props.

- `corner` puts the dot on a mark's bottom-right, in a ring of the page's ground. The mark is the
  positioned parent (`relative`). `true` is the small size. `"md"` and `"lg"` grow the ring and the
  dot with the mark:

  | Size          | Ring | Dot | For a tile of |
  | ------------- | ---- | --- | ------------- |
  | `sm` (`true`) | 10px | 6px | 28 to 32px    |
  | `md`          | 12px | 8px | 36px          |
  | `lg`          | 16px | 8px | 48px          |

- `label` adds the state as sr-only words. Pass it only where the row does not already say the
  state in words, for example the dirty dot on a view link or the map rail's system dot. Where the
  word is beside the dot, a label would say it twice.

```tsx
<span className="relative flex shrink-0">
  <ProjectMark project={project} size="sm" />
  <StatusDot tone="warning" corner label="Unsaved" />
</span>
```

## StateMark and StateCorner

`StateMark` is a thing's tile with its state in the corner. The glyph, or the product's logo where
one made it, says what kind of thing it is. The dot says whether it is still working. A list of them
reads green, grey and red before a name is read. It has one size ladder:

| Size | Tile | For |
| --- | --- | --- |
| `sm` | 28px | a rail's line, a sheet's |
| `md` | 36px | a list row |
| `lg` | 48px | an identity header |

`StateCorner` is the corner alone, for a tile that is not a `ProductLogo`, such as a project's mark.

```tsx
<StateMark
  product={unitProduct(unit.name)}
  glyph={Cog}
  tone={toneFor(unit.activeState)}
/>
```

`network/diagnostic-marks.tsx`'s `StateTile` is now `StateMark`, and `EgressMark` draws through it.

## Tag

**Anatomy.** The `.caps` class from `globals.css`: 10px (11 on touch), weight 500, tracked
0.06em, uppercase, in `--faint`. A `tone` takes the full-strength signal ink instead. There is no
border, no ground and no padding.

**Props.**

- `tone`: one of the four `Tone`s. `default` is `--faint`. The other three are the signal inks.
- `hue`: a colour that is not a signal, as a CSS variable: a change's (`var(--git-modified)`) or a
  label's (`var(--tag-blue)`). It wins over `tone`. Pass it instead of a `style` object.
- `mono`: for a literal string from the host. It drops the caps and draws a quiet sunken ground, so a
  run of them is still a list.
- `icon`: a 10px icon before the word.
- `asChild`: for the few tags that are also links, such as a published port.

```tsx
<Tag>read-only</Tag>
<Tag tone="danger">expired</Tag>
<Tag hue="var(--git-added)">new</Tag>
<Tag mono>TLS_AES_128_GCM_SHA256</Tag>
```

A caps label outside a tag uses the same `.caps` class, not a hand-typed copy of it.

## OutcomeStrip

The last few attempts at something, oldest first, as a 6 by 12px square each in the colour of how
it ended. The caller writes the strip's `label` ("Last 14 runs: 2 failed"). Each square says what it
was in its `title`. A running attempt is the brand blue, because it has no outcome yet. One that
ended without an answer (cancelled, superseded) is a quiet grey.

## The phone

None of these change on a phone. Tag's text is 11px on touch, through the type tokens. A row's state
word that has no room at its end moves to the head of the second line (`PhoneState` on the
diagnostic pages).

## Accessibility

- The dot is `aria-hidden`. The word is what is read. A dot with no word beside it takes `label`.
- Colour is never the only signal. Every `Status` has a word, and `unknown` and `stopped` differ
  in shape as well as in name.
- `announce` speaks a state when it changes, once, and not on the first render. The visible word is
  then hidden from the screen reader, so it is not read twice. `StatusWords` is the same live region
  on its own, for a state that is not drawn as a `Status`, such as a run's header or a job's
  console. Use either only for the subject of the page. A list whose rows each announce their own
  change is noise.

## What enforces it

- `src/components/status-dot.test.js` holds the `toneFor` mapping, the fills, the corner, the
  label, `announce`, `StateMark`'s ladder, Tag's face and hue, and `LanguageMark`.
- `tests/browser/design-system.spec.ts` fails a page that renders a filled pill.
- From WP-39, lint refuses a hand-typed status dot (`rounded-full size-1.5 bg-*`).

## Related

- [`states.md`](states.md): empty, error and loading states.
- [`readings.md`](readings.md): figures and their tones.
- [`../foundations/words.md`](../foundations/words.md#4-one-vocabulary-per-idea): one vocabulary per
  idea.
- The product's own marks: `DashboardMark` (`components/logo.tsx`) is the dashboard as one thing
  among others, on a product's tile. `LanguageMark` (`components/language-icon.tsx`) is a
  repository's language as its real logo and its name.
