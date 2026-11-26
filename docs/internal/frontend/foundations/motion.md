# Motion

## Reference

Motion says one of four things, and nothing moves to decorate.

| Meaning | Animation | Utility | For |
| --- | --- | --- | --- |
| live | `jd-breathe` | `animate-breathe` | a mark fed by a live source; replaces `animate-pulse` on a status |
| arrived | `jd-rise` | `animate-rise`, `Page rise` | a fetch settled: the page once, each block once, a figure once |
| indeterminate | `jd-sweep` | `animate-sweep`, drawn by one `Sweep` (`components/loading.tsx`) | a wait that cannot say how far it has got |
| level crossed | `jd-drill` | `animate-drill` | the rail drilling into a section and back |

The library marks (`jd-beam`, `jd-beam-back`, `jd-wire`, `jd-shimmer`, `agent-caret`,
`agent-attention`, `BorderBeam`, `NumberTicker`) say *live* and *arrived* at the scale of a word or
a wire. `animate-pulse` is a skeleton's and nothing else's.

| Token | Value | Utility | For |
| --- | --- | --- | --- |
| `--ease-arrive` | `cubic-bezier(0.16, 1, 0.3, 1)` | `ease-arrive` | rise, drill, a beam's pass |
| `--ease-standard` | `cubic-bezier(0.4, 0, 0.6, 1)` | `ease-standard` | breathe, sweep, colour |
| `--duration-fast` | 150ms | `duration-fast` | hover colour, a row dimming, a chevron's turn |
| `--duration-base` | 200ms | `duration-base` | an overlay entering, drill, rise |
| `--duration-slow` | 300ms | `duration-slow` | a sheet sliding, the assistant's colour cross-fade |
| `--duration-fill` | 500ms | `transition-fill` | every meter or bar fill |

**Reduced motion and the pause.** One rule at the root of `globals.css` stops every animation and
transition under `prefers-reduced-motion: reduce`, and the same rule applies under
`:root[data-motion="paused"]` (T2, adopted 15 Nov 2026). That one attribute on `<html>` pauses
every beam, breathe, sweep and shimmer for anyone. The control is **Pause animations** in the
account menu (`components/shell/motion-pause.ts`: `useMotionPaused`, `setMotionPaused`). It is kept
per browser under `just-dashboard.motion` (`lib/themes.ts`), and the document's bootstrap script
puts the attribute back before the first paint, so a paused page never starts moving. Everything
that asks about reduced motion gets the same answer from it: Tailwind's `motion-reduce:` and
`motion-safe:` are custom variants in `globals.css` that answer to the media query and to the
attribute; a picture reads `useMotionHeld`; a component that renders differently when motion is
reduced reads `useReducedMotion` from `motion-pause.ts`, which follows both answers while it is on
screen (the motion library's hook of that name reads once at mount, and ESLint refuses it); and the
motion library's own answer is written from `motion-pause.ts` (paused, or the system setting). The
library's elements read that when each mounts, so a layout or exit glide of one already on screen
keeps what it read. Such a component keeps one element whichever the answer, so the switch
changes how it moves and never remounts what it holds. Script that scrolls or
animates asks `lib/motion.ts`: `reducedMotion()` is both answers,
`scrollIntoViewRespectingMotion(el, { block, focus })` is the one way to scroll to a section,
`scrollBehavior()` is the behaviour for any other scroll run from script (a strip's chevrons, a
page pulled along by the wheel), and `motionMs(ms)` is a canvas duration. An explicit
`behavior: "smooth"` overrides the CSS rule, so it is never written by hand, and the media query
alone is only the system's half of the answer, so script never asks it directly either. `Sweep` owns the one reduced-motion face of an indeterminate bar: a
still track in the faint ink.

| Rule | Held by |
| --- | --- |
| Reduced motion is honoured globally. | spec `design-system.spec.ts` (*reduced motion is honoured globally*) |
| A wire pulses as the browser's animation, and its page asks for no frames. | spec `design-system.spec.ts` (*a wire pulses …*) |
| No hand-written `ease-[cubic-bezier(…)]` or `duration-[…]`. | convention; coming: WP-39 |
| `data-motion="paused"` pauses what reduced motion pauses, from before the first paint. | `globals.css` (the rule and the two variants); spec `shell-phone.spec.ts` (*Pause animations*); unit `components/shell/motion-pause.test.js` |
| A component already on screen follows the switch without a reload. | `useReducedMotion` in `motion-pause.ts`; ESLint `no-restricted-imports`; spec `files-ui.spec.ts` (*Pause animations reaches a page already open*) |
| `behavior: "smooth"` and the reduced-motion media query only in `lib/motion.ts` (and `motion-pause.ts`). | unit `motion.test.js` (*script that scrolls*, which reads every source file); spec `proxy-certificates.spec.ts` (*with animations paused, Show log …*) |

## 11. Motion says one of four things

Before 0.6.7 the only shared motion in the product was whatever Radix and `tw-animate-css` happened
to ship: a `transition-colors` here, an `animate-pulse` there, and no answer to "what should this
look like while it is happening". Four names in `@theme`, and no more — three of them are about a
reading, and the fourth exists because 0.6.7 gave the product one navigation that moves between
levels rather than between pages:

| Token | Says | Spent on |
| --- | --- | --- |
| `animate-breathe` | this reading is live | A halo breathing out of a `StatusDot`, at an amplitude low enough to read as "still arriving" and never as an alarm, and the cap on a live bar reading (§10). |
| `animate-rise` | this was not here a moment ago | Opacity and four pixels, once, on arrival: a panel that appears, a disclosure that opens, a sparkline whose data landed. |
| `animate-sweep` | this is working, and cannot say how far along | An indeterminate bar for a pull or a prune, where a spinner in the corner of a wide panel is too small to be the answer. |
| `animate-drill` | you crossed a level | `rise` turned sideways, for the one place where a list is replaced by a list: the sidebar going into a section and back out. The direction *is* the message — in arrives from the right, out from the left — so the distance is `--drill-from` at the call site, not in the keyframe, and a panel that merely re-rendered does not move at all. |

`OperationProgress` is a dialog's reading for an action in flight: its name, elapsed time and one
thin track. It exists only inside `Modal`, which draws it as part of its footer rather than as a
surface of its own — no border, ground or shadow (§2). The name and a hint line (elapsed time, a
measured count, the latest detail) take the footer's free side beside the buttons, each cut to one
line, and the footer's top hairline becomes the track. The footer keeps its height and the dialog its
frame when work starts; where the buttons leave the words too little room, as on a phone, the words
take the footer's first line. With no measured total the track spends `animate-sweep`; a reported
item or byte count fills it instead. An upload reaching its total switches to an indeterminate
processing reading until the server answers. Elapsed time is not a completion estimate or a server
heartbeat. The timer is excluded from the live region, the track has a named `progressbar` role, and
reduced motion leaves a static full track. Pages, workbenches and job consoles draw no working bar of
their own; a bar reading already on the page sweeps its own bar while work on what it draws runs (§10).

**`rise` is expected wherever a fetch settles**, not reserved for special occasions. The Overview is
the reference: the page rises once when its first snapshot lands; each sparkline when its hour of
history arrives; the activity list when its events arrive; each service figure when its own poll
settles (swap the figure's `key` between skeleton and value so it remounts). Hover is colour only —
`transition-colors` on a wash or a border — and a meter eases its width. Nothing translates, scales,
glows or casts a shadow to say it was hovered or pressed; the one `group-hover:translate` the product
had was removed from the Overview's service arrow in 0.6.7. A bar reading's line (§10) rises when
pointing changes its part, because that part was not on the line a moment ago, and the spans it fades
are the reading answering, not a hover's wash.

`animate-pulse` is not one of them. It dims the element itself, which on a table of twenty running
containers reads as twenty things blinking for attention — `breathe` leaves the dot constant and
moves only the air around it, so the column still scans as "all green" at a glance.

**Live is a claim about the data, not a decoration.** `StatusDot`'s `live` prop belongs to a row fed
by an open socket, and `BarReading`'s to a bar fed by one. A polled figure is not live, and marking it
so is the interface lying about how fresh its numbers are.

Nothing here needs a `motion-reduce:` guard. The rule lives once at the root of `globals.css` and
collapses every animation's *duration* rather than cancelling it, so a keyframe that would otherwise
never reach its final frame still ends up there. A *delay* is the one exception, because the rule does
not touch it: the account menu's rows `rise` in a stagger from the row nearest its card, and that
stagger is `motion-safe:[animation-delay:…]` so a reduced-motion reader gets every row at once rather
than rows popping in one after another.

### Motion that arrived with a library

The deployment section brings in registry components — Magic UI's `ui/animated-beam`,
`ui/border-beam`, `ui/blur-fade`, `ui/number-ticker` and `ui/confetti`, and Motion
Primitives' `ui/text-shimmer` — each rewritten onto the tokens and each saying one of the four things
above:

- *arrived* — `BlurFade` staggers the fleet's cards by a beat each, the Overview's blocks by
  0.04s, the Insights facets, and the first screen of the Git branch graph's rows while its lanes
  draw down beside them; `ChoiceRow` given its `index` staggers every lit list the same way, capped
  at twelve so a long list does not spend a second arriving, and `ChoiceCard` staggers a grid by its
  `index`, a beat each and uncapped. `NumberTicker` counts a figure up to its value once it lands:
  the fleet's live and build-slot figures, the
  Overview's requests, the delivery insights, Automation's
  revisions awaiting review and alerts firing, the live usage tiles, the readings on
  Packages, and every lens's readings (`LensReadingLine`) — the logs
  page's, a service page's and Security's alike. A figure that follows a
  draft as it is typed — Build's and Runtime's settings readings — does not count, because it would
  count again on every keystroke; it rises once when it lands instead;
- *live* — `AnimatedBeam`'s pulse on a line, `BorderBeam` running around anything whose work is in
  flight, and `TextShimmer` lighting the word for what it is doing, are `breathe` for a link, a
  frame and a word: a reading that is happening now. The beam reaches a row through
  `ChoiceRow busy` and a card through `ProductCard working`, so it is one mark for one meaning
  wherever it lands — a project card, a run row and the build console while a run is going (the
  fleet's in-progress rows carry the sweeping release path and the lit stage instead); the
  in-flight card on the Overview; a channel while its test is out and a
  credential while its probe runs; a candidate service during a release, a backup job taking a
  backup (on /backups as well as on a deployment's pages, with the protection picture's wires
  pulsing and its middle naming the job), a game command waiting on its reply; a webhook whose delivery's run is building, a
  schedule firing, a preview environment building; a variable being rotated, a linked database
  being tested, an engine being started from quick setup; and, on `/deploy/new`, a repository or
  an image being inspected. The shimmer lights the stage a release is at and the present
  participles that stand for a wait — *Sending test…*, *Testing…*, *Pausing…*, *Candidate for
  release #14*, *deploying #14*, *running now*. All three are the browser's own animations, never a
  script's frame loop, because *live* lasts as long as the page is open: the beam's pulse is a band
  moved by `transform` behind a mask cut to the line (`jd-beam`), `BorderBeam`'s lap is the frame's
  edge written as transform keyframes (`lib/beam-lap.ts`) and handed to the Web Animations API
  once, the shimmer is `jd-shimmer`, the update notice's wire `jd-wire`, and the release path's
  running `Segment` is `animate-sweep`. Reduced motion stills each of them in CSS. A new *live*
  mark is written the same way: an endless `motion` animation keeps a page that looks still busy
  sixty times a second (`design-system.spec.ts` counts the frames a page with wires asks for).
  The element a `BorderBeam` is put in clips (`overflow-hidden`) when it is not the frame itself:
  the light is a square centred on the edge it lights, so half of it lies outside the frame, and on
  a phone that half reached past the page and scrolled it sideways each lap. Since 0.8.7 the
  light's own box clips it (`ui/border-beam.tsx`), so a busy `ChoiceRow` and a working
  `ProductCard` need nothing of their holder;
- *once* — `Confetti` fires only when a release goes live in front of the reader — on the run page,
  on the Overview when a run watched there from start to finish ends in success, and from the
  sidebar's update notice when an update of the dashboard itself finishes while the tab is open —
  never on arrival, and never for a Stop, which takes the release down.

`animate-sweep` is spent the same way, on the three waits in the section that cannot say how far
along they are: a credential's test while the server tries it, the website preview while the site
paints behind an invisible frame, and the build console before its first line. The preview's rise
sits on a wrapper round the frame, because the keyframe ends on `transform: none` and cancelled the
scale that shrinks a desktop-width page into the tile.

**`hooks/use-arrivals.ts` is *arrived* for a polled list, in the tokens rather than a library.** A
new request, a container event, a delivery or a player who has just joined used to appear in its
list with nothing to say it had not been there a moment ago, so a list that gained a row read
exactly like one that had merely re-rendered; only the run transcript let its new lines rise.
`useArrivals(keys)` returns the keys that were not in the list the last time it changed, and those
rows take `animate-rise` once — the live request rows, the lifecycle feed's events, a channel's and
a webhook's deliveries, a game's players, the audit trail's entries. It is empty on the first render, because the page's own
rise covers what arrived with it and forty rows rising at once are not forty arrivals, and its
answer is held until the keys change again, so a re-render halfway through a rise does not cut it
short. A list whose rows already stagger in on mount — the runs, keyed by id — does not take it as
well, which would draw one arrival twice.

The dashboard's own restarts and upgrades use the same three and nothing new: `BorderBeam` runs
around the transcript console and around the Restart or Rebuild card while that run is in flight,
the request path's wires pulse while this tab's own requests go down them and stand still and
amber while a restart replaces the containers,
`TextShimmer` lights the stage it is at (`components/run-phases.tsx`, drawn with the release path's
own `Segment`), and the transcript's new lines *arrive* — a poll's forty lines are let out over the
next frames, all within 300 ms, each taking `animate-rise` once, so a live log reads line by line instead of jumping; scrolled
up, searching or with reduced motion they are simply drawn. A deployment's build console draws its
lines through the same painter and the same drip (`components/transcript-line.tsx`), so the two
consoles cannot drift into two answers to what a live log looks like. The version history's
timeline took its layout from Aceternity's and Magic UI's timelines — the sticky label, the rail —
and not their scroll-driven gradient beam, which is a motion this section does not have.

The sidebar's update notice (`components/update/update-notice.tsx`) says the same things at the
rail's scale. Its row of two versions is a wire (`UpdateWire` in `components/update/update-art.tsx`)
with `AnimatedBeam`'s pulse running from the installed version to the newest, drawn as a flex item
rather than measured between two elements, so a dialog zooming in as it opens cannot leave it
pointing at where its ends were; it goes still once the run has finished or failed. Under it, what
the releases bring is a band of the change kinds' own hues whose spans draw out one after another
as the notice lands (*arrived*, once). While an update runs, `BorderBeam` runs round the notice, the
stage it is at is the update's own three `Segment`s with its name in `TextShimmer`, and an update
that succeeds while the tab watches fires the confetti above.

Magic UI's `animated-circular-progress-bar` was tried beside the release path and removed: a ring
saying "100%" next to a header saying "Ready" was the same fact twice, and the timeline now shows how
far a run is by how much of the bar has coloured. Its `safari` device mock was evaluated for the
website preview and not adopted — its chrome is drawn for a hero, and at tile size the address bar's
text is too small to read — so the preview draws its own strip and shrinks a desktop-width frame.

The redesign of the whole section in September 2026 measured it against Magic UI's registry again,
fetched live a component at a time, and took nothing new. `animated-list` reveals items it already
holds on a timer, which is an arrival made up — its real meaning is `useArrivals`. `code-comparison`
needs shiki and next-themes, and there is no theme to switch (§1); a release comparison keeps its
field list in the git hues, and a text diff is the file manager's `diff-view`. `file-tree` brings
Radix's accordion and lucide into a product that already draws a file as what it is
(`components/file-icon.tsx`). `terminal` types scripted lines that claim a liveness they do not have,
beside a real console. `animated-circular-progress-bar` stays out for the reason above, and every
proportion in the section is already a figure and a `Meter`. `progressive-blur` is stacked
`backdrop-filter`, which is glass (§15), where `.scroll-affordance` already says "more past here"
with ground; `scroll-progress` is a gradient bar tied to the window's scroll in a shell that
scrolls an inner container, and reading progress is none of the four meanings. `avatar-circles` is
round faces in white rings with a filled "+N" circle; an account's circular avatar is an identity
exception, but the filled count still violates §4. `pulsating-button` glows a command at rest, and §3
never lets a command be a state. `dock` magnifies on hover and blurs behind itself;
`orbiting-circles` and `ripple` are perpetual decoration, beside wires that draw the real mechanism and a `StatusDot` that
already breathes. `shine-border` and `magic-card` are `BorderBeam` and `SpotlightBorder` already,
and `animated-shiny-text` is `TextShimmer`. The marquee, the globe and the dotted map (which would
need a GeoIP database and would fabricate the rest), the device mocks, the lens, the highlighter,
the glyph matrix, the flickering grid, particles, patterns and the aurora, sparkle, hyper and
morphing texts each sell a gradient, a glow or a movement this system does not have. What the pass
took instead was more of what was already here, on more of the section: the ticker, the border
beam, the fade, and wire marks that draw the products they connect.

Every one of them honours `prefers-reduced-motion` in JavaScript, because the root CSS rule cannot
reach a JavaScript-driven animation.

The assistant's chat is the one surface that spends `motion` itself at the scale of a word or a row, because an
answer arriving is a sequence of small arrivals and a person watches every one. Each is still one of the four
meanings, each runs 150–250ms on the product's rise curve with nothing springing, and a subtree under
`MotionConfig reducedMotion="user"` drops every transform for a reader who asked for less: a turn rises in as it
is asked (one read back from the history does not); the suggestions of an empty chat arrive a beat apart; a step's
badge crosses from the spinner to a tick or a cross; a step and a long run of steps unfold to their height
(`animate-collapsible-*`), and opening one lets go of the bottom first, so the row stays under the pointer instead of
being pushed up by the column following the newest line; a call waiting on the person rises and lights its edge once
in its tone (`.agent-attention`, the arrival highlight's outline run once on arrival), and so does a change's
shortcut, in green; *Ask* becomes *Stop* in place — the face eases from the command's blue to a control's while the
word and glyph cross — rather than one button replacing another; the composer's and the logo's `BorderBeam` fade up
and away; the answer still arriving ends in a soft caret that breathes (`.agent-streaming`, drawn by the column
after the last word, never typed into the Markdown); *Thinking* is the shimmer with the seconds counted beside it;
**Jump to latest** rises in once the reader has scrolled up and sinks away when they are back; the rail's changes
slide in; and the dock rises in and sinks away, its rows easing their height as they join and leave.


Database creation on `/databases/new` and `/deploy/new?source=database` uses one shared catalogue,
settings `FlowPanel`, and startup sequence (`database/connect/start.tsx`). While a container is being
provisioned, adopted or verified, the panel carries `BorderBeam` and `DatabaseProgress` draws the
release path's own sweeping `Segment`s, stage marks and `TextShimmer` through `RunPhases`. The stages
advance on API results, including a fresh ping after adoption, and a failed stage stays red for retry;
there is no estimated percentage or simulated build output. The Databases page opens the verified
connection's home; Deploy shows the masked connection string through `deploy/database-ready.tsx`.

`ui/bento-grid` was a seventh, and is gone. It drew "Start with something ready" on New project's Git
tab as cells of unequal size that were buttons; the 2026-09-20 pass removed that grid because the
source strip above it already listed the same six ways in, and the file then sat unimported for a
release while this paragraph went on describing it as shipped. A registry component with no caller is
not a component the product has — the next reader takes a sentence like that as permission to rebuild
what was deliberately deleted.
