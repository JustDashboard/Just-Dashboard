# Flow page

A page where the reader decides a sequence and something exists at the end of it. Reference:
`/deploy/new`.

**Reference:** `components/deploy/new-project.tsx` and the screens under
`components/deploy/new-project/`. The shot is [`docs/readme/deploy-new.png`](../../../readme/deploy-new.png).
Its siblings are `/databases/new`, a database's Connect, adding a Remote Desktop computer and a new
system account.

**Components:** `Page register="flow"`; `FlowHeader`, `FlowSteps`, `FlowPanel`, `FlowPanelHeader`,
`FlowPanelBody`, `FlowActions` (`components/flow.tsx`); `ChoiceGrid` and `ChoiceCard` for kinds
([`../components/choice-card.md`](../components/choice-card.md)); `ChoiceList` and `ChoiceRow` for
instances ([`../components/card-row.md`](../components/card-row.md)); the form kit
([`../components/forms.md`](../components/forms.md)). Which pages are flows is in
[`../patterns/registers.md`](../patterns/registers.md): if you cannot name the sequence and its
outcome in one sentence, the page is a reading page.

## The regions, in order

1. **The head** (`FlowHeader`): the way out to the list the new thing will join (`back`), the
   question as the `h1`, the shortcuts' `?` at the end (`actions`), and the spine (`steps`).
2. **The spine** (`FlowSteps`): the steps of the state machine underneath, as segments of a rule
   with their names. Not numbered circles.
3. **The one focused surface** (`FlowPanel`): the thing being decided on this screen. It is the
   only surface that takes depth, and there is one a screen.
4. **Supporting facts** beside or under it, as plain panels: what was chosen so far, what the
   server found.
5. **The foot** (`FlowActions`): one brand-faced command that advances, the ways out before it,
   quieter, and a note where the command's word does not say what it is about to do.

## Skeleton

```tsx
export function NewThing() {
  const [step, setStep] = useStepInAddress(STEPS) // `?step=`, so Back walks the steps
  return (
    <Workspace name="New thing" stateKey="things.new">
      <Page register="flow" fill="xl">
        <FlowHeader
          back={{ href: "/things", label: "Things" }}
          question={QUESTIONS[step]}
          actions={<WorkspaceHelp />}
          steps={<FlowSteps steps={STEPS} current={STEPS.findIndex((s) => s.key === step)} />}
        />
        <div className="grid min-h-0 gap-6 xl:grid-cols-main-end">
          <FlowPanel className="max-h-full self-start">
            <FlowPanelHeader title="Where is it?" />
            <FlowPanelBody>
              <ChoiceGrid mode="single" label="Source">
                {SOURCES.map((source, index) => (
                  <ChoiceCard
                    key={source.id}
                    index={index}
                    logo={<ProductLogo id={source.product} />}
                    title={source.name}
                    description={source.line}
                    selected={source.id === draft.source}
                    onClick={() => pick(source)}
                  />
                ))}
              </ChoiceGrid>
            </FlowPanelBody>
          </FlowPanel>
          <Panel plain>
            <PanelHeader title="So far" />
            <PanelBody><ChosenFacts draft={draft} /></PanelBody>
          </Panel>
        </div>
        <FlowActions secondary={<Button variant="ghost" onClick={back}>Back</Button>}>
          <Button disabled={!ready} onClick={next}>Continue</Button>
        </FlowActions>
      </Page>
    </Workspace>
  )
}
```

The step hook is the page's own. Take the steps from the server's draft, not from the page, so the
spine cannot drift from what the server thinks is happening.

## The four states

| State | What the page draws |
| --- | --- |
| Loading | The head, the spine and the focused surface at their sizes; a list being inspected waits as `ChoiceListSkeleton`, and the row that was pressed carries the beam (`ChoiceRow busy`) beside its words. The command is disabled, not hidden. |
| Empty | Nothing to choose from (no repositories, no images): `EmptyState` inside the focused surface with what would be there and the step that fills it (connect an account). |
| Unavailable | The flow cannot run here (no Docker, no rights): `MissingThing`, or `NeedsAdmin`, in place of the focused surface, with the head kept so the way out is still there. |
| Failed | A step the server refused keeps everything typed, marks the field it named and says its words by the command. A read that failed is `ErrorState` in the surface with Try again. Nothing made so far is lost: an unfinished setup can be resumed. |

## The phone

Below `xl` the columns stack and the page scrolls as any page does: a phone is not a window to
hold. The spine keeps its segments and shows the current step's name. A strip of sources runs to
the screen's edge and scrolls sideways (`ChipStrip`). A grid of kinds is one column, each card one
height. The command is the full width at the foot of the surface.

## Checklist

- [ ] `Page register="flow"`, and only a flow page has a foreground surface.
- [ ] The `h1` is the question. No sentence under it.
- [ ] One `FlowPanel` a screen, and one brand-faced command in `FlowActions`.
- [ ] Kinds are `ChoiceCard`s and instances are `ChoiceRow`s; nothing to pick is a plain row.
- [ ] The steps are in the address, so Back and Forward walk them, and a refresh keeps the place.
- [ ] At `xl` the screen is decided in one view: the shell does not scroll, and the one long list
      or form scrolls inside its surface.
- [ ] Motion only on a state change: the spine's segment, a beam on work in flight, one arrival
      when the outcome lands.
- [ ] Leaving with something typed asks first.

## The spec to add

1. The page declares `data-register="flow"`, its `h1` is the question, and the spine marks the
   current step (`aria-current`).
2. Each step is reached from the one before by the command, and Back returns with what was typed.
3. At 1280×800 the shell does not scroll on any step.
4. A refused step keeps the draft and shows the server's words.
5. At 390 the page scrolls as one column and is no wider than the window.

`tests/browser/deploy-new.spec.ts` and `deploy-new-flow.spec.ts` are the models.

## 17. Redesigning a flow page

§15 is the reading register's ordered passes. This is the other one. The reference is `/deploy/new`.

1. **Declare it.** `<Page register="flow">`. If you cannot name the sequence and its outcome in one
   sentence, the page is a reading page and §15 is what you want.
2. **Ask the question.** The `h1` becomes what the screen is asking. The way out is `back`, drawn as
   every page's back link above the question. No sentence under it — §5 holds.
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
   pressed buttons, never a tablist — a tablist must own tabs, and these are five toggles for one
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
