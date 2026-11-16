# Words

## Reference

| Rule | Held by |
| --- | --- |
| No sentence under a title. What the reader needs before acting is a `Notice`; what the page is is its own row or fact. | type (`PageContext`, `Section`, `PanelHeader`, `ChartPanel` take no `description`) |
| A verb is one word on a button: Deploy, Restart, Delete. The object goes in the title or the confirmation. | convention |
| A count is `.numeric` text beside its label. A state is a `Status` (a dot and a word). A fixed property is a `Tag`. Anything longer than three words is prose. | convention |
| There is no badge and no pill. | spec `design-system.spec.ts` (*no page renders a filled pill*) |
| How long something took is said in words: "19 in 20 inside 412ms", never p50, p95 or p99. | lint `design-system/latency-words` |
| A failure is said in a sentence, in one place: `ErrorState` (not found, unavailable, throttled, failed). | convention |
| Names, addresses and paths are mono. | convention |
| One way to say a value: bytes, rates, how long ago, a certificate's time left. | convention; coming: the format helpers (WP-14) |
| When there is too much on a page, cut words; do not rearrange them. | convention |

## 4. One vocabulary per idea

**There is no badge and no pill in this product.** The badge primitive was deleted so the decision cannot
come back by accident, and a Playwright test asserts no filled, fully-rounded label renders on any
page.

**An account's avatar is circular.** `UserAvatar` draws a picture or initials and carries
`data-slot="user-avatar"`. Account avatars are the one identity exception to the filled-circle ban;
labels, counts and numbered steps remain subject to it.

- A **count** is `.numeric` text next to its label.
- A **state** is `Status` — a coloured dot (or an icon) and a word. No border, no fill.
- A fixed **property** of a row is `Tag` — **small caps text, and nothing else**. No border, no
  ground, no padding. The chip is gone: 10px caps inside a hairline box is a container three times
  the height of the word it holds, and beside the 13px title where most of the 117 of these sit, the
  box read as the louder of the two. A `mono` tag keeps a quiet recessed ground because its contents
  are literal strings from the host — a cipher, a port map, a config hash — which run together
  otherwise and which small caps would corrupt.
- Anything longer than three words is **prose**, and belongs in the row's secondary line or a `Notice`.
- A **choice** is a `ChoiceCard` or a `ChoiceRow`, from `components/choice-card.tsx` and
  `components/choice-row.tsx`. Two shapes, one look: a choice that is *a name* is the whole card, and a
  choice carrying *a sentence* puts the control on its title and takes a `verb` — an ARIA button
  names itself from everything inside it, so a card holding a title, a paragraph and three tags
  announces all of it as the name of one control (§12). A run of the same *kind* of thing is rows;
  a choice between *kinds* is a grid.

  This one has already gone wrong once, which is why it is written here. `components/choice-card.tsx`
  existed to stop six places building the same tile six ways; the flow register then shipped a
  **second** `ChoiceCard` in `components/flow.tsx`, with a lit border the first one did not have — so
  the deploy pages had the new treatment and the database dialogs, the credential picker and the
  engine picker kept the old one. There is one again. A component whose name already exists in this
  product is not a new component — and the rule holds for a shape as well as a name. `ProductCard`
  is `ChoiceCard`'s compact form, a product's logo, its name and one line of what exactly would be
  used; it began as the database engine picker's card, and when the build settings needed the
  same card for ten builders and five package managers it became the shared one rather than a
  second, with `EngineCard` now rendering through it. A run of them sits in
  `ChoiceGrid columns="compact"`, two to a row on a phone and five across at `xl`.

A tag that annotates a **row** belongs at the row's edge, not against its title: ten of them
interrupt ten sentences at ten different points, and in a column they are a column. And a tag is a
property, not a state: on a run card *Live* is a `Status`, because a release serving is a reading
that will change, and *Pinned* is a `Tag` with a pin, because somebody set it and it stays.

`components/tone.ts` exports the one severity union: `"default" | "success" | "warning" | "danger"`.
`Verdict` keeps `critical` because that is what the backend sends and `Button` keeps `destructive`
because that is what the control does — but anything choosing a *colour* chooses from the shared
union. A component that declares its own tone names is the drift this file exists to stop.

**How long something took is said in words, never as `p50` / `p95` / `p99`.** The operator found the
notation unclear ("notation asks the reader to learn it") and Insights' readings became *Half*,
*19 in 20* and *99 in 100*; in 0.8.6 every other rendered label, legend, column head and sentence
followed, so one figure has one name on every page. The words live in `lib/latency-words.ts`:
`LATENCY_SHARE` names a figure, `latencyHead` is a column head, a legend or a tile's label
("19 in 20 within"), `latencyWords` is the reading inside a sentence ("19 in 20 inside 180ms"), and
`latencyTerm` is the `title` that keeps the term for a reader who looks for it. A threshold reads the
other way round, by the share past it ("more than 1 in 20 requests slower than 800ms"), and a factor
between two readings is a *slowdown*. The lint rule `design-system/latency-words` refuses the
notation in JSX text, in a string with other words around it and in a `label`, `title`, `name` or
`hint`; a bare `"p95"` elsewhere is a key of the server's answer and stays. The interface charts on
`/network/traffic` were outside it until 0.8.7, because their percentiles are of throughput, not of
how long anything took; the notation asked the same learning of the reader either way, so they say
their shares in the same words ("19 in 20 under ↓ 2.0 MB/s", `network/traffic/words.ts`) and the
rule covers that file too. Sentences the server writes (a rollback watch's verdict, a canary's, a replay's, a traffic
alert's notification) still say p95 and are shown as the server wrote them.

## 5. No descriptions under titles

**`PageContext`, `Section`, `PanelHeader` and `ChartPanel` have no `description`.** They have no `icon`
either — that is §14.

Every page carried a sentence under its heading explaining what the page was — a caption for a title
the reader had already read and understood. It pushed the first real row of every page a line and a
half down the screen, and it was never read twice.

The rule and its three exceptions:

- What the reader genuinely needs before acting goes in a **`Notice`**, where it is a fact rather
  than a subtitle.
- Where the slot was carrying **data** rather than prose — a row count, how many volumes are unused,
  what platform and kernel a host runs — the data stays, moved to where it belongs: the header's
  actions, a `Tag`, or its own row.
- `Modal` and `SidePanel` keep a `description` prop, rendered `sr-only`. Radix wants an accessible
  description and a screen reader is the one audience that cannot see the rest of the box.
- `Modal` and `SidePanel` give the keyboard back to what opened them (`hooks/use-focus-return.ts`).
  Radix returns focus to a `DialogTrigger`, and almost every dialog here is opened by state and has
  none, so closing a confirmation or an editor left the keyboard on `body` and the next Tab began at the
  top of the sidebar. The opener is whatever held the keyboard when the dialog opened; for a verb in a
  row's menu that item is gone by the time the dialog closes, so the menu's own button is remembered
  beside it (through the menu's `aria-labelledby`) and gets the keyboard instead. A dialog that knows
  better passes `onCloseAutoFocus` and calls `preventDefault()`. A page that hands the keyboard back
  itself, for every dialog it opens, calls `claimFocusReturn` as it closes one: the database's data page
  does (`focusAfterDialog`), because the question it asks before unapplied changes are left is opened by
  the link that was pressed, and the keyboard belongs back on the rows. Open dialogs through these two
  components, not through a bare `DialogContent`, or the keyboard is dropped again.

`EmptyState`, `ErrorState` and `Notice` keep their body text: there, the sentence *is* the content.

**A failure is said in sentences, in one place.** The API words its messages to sit inside other text:
lower case, no full stop ("not found", "too many requests, slow down"). `ErrorState` is where they
stand alone, so it gives the headline its capital and a reason its full stop (`sentenceCase` and
`asSentence` in `lib/format.ts`), and `notify.error` does the same for the reason under a toast. A
message that opens on a program's name, a path or an identifier ("nginx: [emerg] …", "eth0 is down")
is somebody else's text and is left as written; so is a command that is the sentence's subject ("nginx
refuses the configuration", "gh did not answer": the `COMMANDS` list beside `sentenceCase`, for names
that are only ever a command) and a command line ("wget --spider …"). No full stop goes after a path,
an address or a quoted value. A page does not reword an API message itself.

`ErrorState` draws four kinds (`data-kind`), and only one of them is red:

| Kind | When | Drawn as |
| --- | --- | --- |
| `failed` | anything else | the danger wash and rule, the message as the headline |
| `unavailable` | this host has no Docker, nginx, terminal | neutral: "Not available on this host" |
| `missing` | 404 | neutral: the message, then "It may have been removed or renamed since this link was made." An old link to something deleted is a normal event |
| `throttled` | 429 | neutral: the message, "The server asked for a pause", **Try again** whenever `onRetry` is given, and `onRetry` called by itself once the answer's `Retry-After` (ten seconds without one) and a short grace have passed |

Pass `onRetry` wherever the read can be repeated: it is what makes a throttled page come back without
a press on a read that does not poll.

## 13. A verb is a word

An icon-only control is legible when its shape is universal and it is pressed constantly — play,
pause, stop, restart. It is not legible the first time, and `ArrowCircleUp` has never meant "pull a
newer image and rebuild this container with the same settings" to anybody who did not already know.

The rule that fell out of the Docker pass, and which generalises:

- **Two or three verbs inline, as icons.** The ones pressed daily, whose glyphs are conventional.
- **Everything else behind one overflow menu**, one word to a line, with no sentence under it. The
  menu is not where things are hidden — it is where a verb gets its name, which is the only form
  most of them are usable in. A verb that needs a sentence to be understood needs a better word, or
  a confirmation that carries the sentence; a menu of two-line items read as a wall of captions
  (§5), and every item cost twice the height it earned. Rows in a menu that carry a *fact* beside
  the word — a place's path, a saved command, a connection's host — keep it on the same line, muted
  and truncated, never underneath.
- **The verbs themselves are declared once**, as data, and a surface decides only how many it has
  room to draw. `components/docker/container-actions.tsx` is the pattern: three surfaces used to hold
  three different answers to "what can I do to this container", and they disagreed about which
  capability each needed. `components/verbs.tsx` is that pattern made shareable — a `Verb`, drawn
  by `VerbActions` in a row (inline icons and one menu), `VerbBar` in a sheet or a detail page's
  header (named buttons and
  the same menu) and `VerbMenu` alone in a header — and the process, PM2, unit, timer and cron rows
  all draw theirs through it, so four kinds of row did not arrive at four menus. A process row keeps
  nothing inline: the daily verb on a process table is reading it, and a stop glyph beside four
  hundred rows is four hundred invitations to end something by mis-click, so Terminate and Kill are
  words in the row's menu. On the process's page they stay behind its `⋯` too, in the destructive
  ink: no destructive verb takes an identity line's command (`IdentityVerbs` passes over a `danger`
  verb, and a thing whose only verbs are destructive draws no command).

The deployment section declares its two sets the same way. A project's verbs are declared once in
`deploy/project-verbs.tsx` and drawn by the project context row — whose one brand command,
`projectCommand`, is the first of View, Start, Deploy and Redeploy the list holds — by a fleet
card's menu and by a fleet row, so a card and the context row cannot disagree about what can be done to
a project. A run's and its release's are declared once in `deploy/run-verbs.tsx` and drawn by a
Deployments row's menu and by the run page's header, which is what keeps a finished run from
being a dead end. A long menu is grouped: a `Verb` may name its `group` — Running, Building, Project,
*Release #4* — and `VerbMenu` draws an eyebrow and a separator where the group changes, because
eleven words in a row are a wall and the same eleven under three names are three short lists.
Cancelling a run is not a danger verb, any more than Stop is: it is undone by deploying again, and
the danger rule in front of it would have cut its group in two.

A choice **closes the menu**. `VerbMenu` lets Radix's default close run from 0.6.7: the item's
`preventDefault`, copied from the Docker actions menu, had kept every menu drawn through it open after
a choice, so the verb ran behind a menu that was still asking. Where a surface draws more than one
menu — a sheet with its own verbs above a table of rows that each have theirs — `menuLabel` names
each ellipsis after its row, so a screen reader and a test can tell them apart.

A control that changes state should also **say that it is changing**. A Docker stop takes ten seconds
to honour while the socket keeps reporting the old state, so the row answers the press by sitting
still and then jumping — indistinguishable from a button that did not work, and the reason anybody
presses restart twice. The row carries the present participle (`Stopping…`) until the change lands.
