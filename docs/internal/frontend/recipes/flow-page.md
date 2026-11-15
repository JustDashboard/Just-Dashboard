# Flow page

A page where the reader decides a sequence. Reference: `/deploy/new`.

**Status: moved text.** WP-41 gives this page the recipe shape (the reference shot, the regions, a
skeleton, the four states, the phone, a checklist and the spec). Until then it holds the passes that
were in `design-system.md`, below, and they are what a redesign runs.

## 17. Redesigning a flow page

§15 is the reading register's ordered passes. This is the other one. The reference is `/deploy/new`.

1. **Declare it.** `<Page register="flow">`. If you cannot name the sequence and its outcome in one
   sentence, the page is a reading page and §15 is what you want.
2. **Ask the question.** The `h1` becomes what the screen is asking. The page's name moves to the
   eyebrow beside the back link. No sentence under it — §5 holds.
3. **Draw the spine.** `FlowSteps`, with the steps taken from the state machine underneath rather
   than invented: `/deploy/new`'s three are the draft's own `source` → `configuration` → commit, so
   the spine cannot drift from what the server thinks is happening.
4. **Pick the one focused surface.** The thing being decided on this screen becomes a `FlowPanel`.
   Everything else on the page stays a `Panel plain` — supporting facts do not get depth, and a
   second `FlowPanel` is the pass failing.
5. **Find the command.** One brand-faced button in `FlowActions`, or choices whose edges light. If
   the screen has neither, the reader cannot tell what advances it.
6. **Choices become choices.** Kinds → `ChoiceGrid` of `ChoiceCard`. Instances → `ChoiceList` of
   `ChoiceRow`. A list of things you can pick is never a `RowList`. A run of kinds longer than a
   screen is shelved the way the reader looks for one — `FilterChip`s with counts over the shelves,
   the Git page's filter strip — and laid out as `ChoiceGrid columns="fill"`, whose rows are equal,
   with the card's hint clamped to two lines beside a `logo`: sixty cards at three heights read as a
   grid that failed to load. The strip that picks between kinds of source is a `role="group"` of
   pressed buttons, never a tablist — a tablist must own tabs, and these are four toggles for one
   answer — and on a phone it runs to the screen's edge, where the source cut off is the cue that
   there are more, as a `ChipStrip` does; the scroll shade is drawn in the page's own ground and
   cannot show on it.
7. **Motion on the state change, not only on arrival.** §11's four still apply and no fifth is added:
   the spine's current segment, a `BorderBeam` on a card while its work is in flight, a
   `NumberTicker` on a figure that settled, `Confetti` once when the outcome lands in front of the
   reader. On `/deploy/new` the beam is the repository or image row being inspected
   (`ChoiceRow busy`), beside its *Importing…*, so the row that was pressed is the row that answers.
8. **Hold it to the window.** A flow screen is decided in one view: `<Page fill="xl">` holds it to the
   window at `xl`, the question, the spine and any strip stay put, and what scrolls is the one list or
   form longer than the space left — inside its own surface, under its own toolbar and above its own
   command. A surface with no inner scroll is a surface the page now clips, so each one is capped
   (`max-h-full`) and scrolls itself. A surface that scrolls also clips whatever its contents draw
   outside themselves — a field's focus ring four pixels out, a quiet fold's wash twelve — so a
   scrolling column of fields pays that bleed and takes it back (`-mx-3 -my-1 px-3 py-1`), as a
   scrolling list pays its rows'. `/deploy/new` is the reference: every source is the same two
   columns (the focused surface, capped at the window's height with `max-h-full self-start`, and a 22rem
   column beside it), unfinished setups moved from a block above the strip into a counted button beside
   the question, and a Configure step with more settings than fit scrolls its fields between the heading
   and Continue. The Database tab shares `/databases/new`'s shelved engine catalogue and settings
   panel: each column scrolls independently, with the Create command held at the panel's foot. Below
   `xl` the columns stack and the page scrolls as every other page does — a phone is not a window to hold. `deploy-new.spec.ts` asserts the shell does not scroll at 1280×800 on every
   source and every Configure step.
9. **Verify.** `scripts/test-changed.sh`, then screenshots at 1280 and
   1720 — and look at them. The failure this register exists to catch is one no assertion sees.
