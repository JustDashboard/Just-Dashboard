# Live data and arrivals

What is live says so, what arrived rises once, and a poll never claims to be a socket.

## Three kinds of reading

| The reading | How it is fed | What it may say |
| --- | --- | --- |
| **Live** | an open socket (`useSocket`, the metrics stream) | "Live", a breathing dot, a figure that glides to each frame (`LiveFigure`, `LiveBytes`), `StreamState` in the block's head |
| **Polled** | `usePoll` on an interval, shared between blocks that read the same thing (`sharedPoll`) | its value, and how long ago it was read where that matters (`Ago`) |
| **Read once** | a fetch when the page opens or a verb is pressed | its value; a Refresh command (F5, Ctrl/⌘+R) reads it again |

The rule the three exist for: **only a block fed by an open socket says Live.** A poll every five
seconds is not live, and a dot that breathes over it is a claim the page cannot keep. While a
socket is down, its figures are the last frame and the head says so.

## Motion says one of four things

`animate-breathe`: this reading is live. `animate-rise`: this was not here a moment ago.
`animate-sweep`: this is working and cannot say how far along. `animate-drill`: you crossed a
level. Nothing else moves ([`../foundations/motion.md`](../foundations/motion.md)).

## Arrivals

1. **The page rises once**, when its first read lands (`Page rise`). Each block rises once when its
   own read settles.
2. **A row that is new since the page opened rises** (`useArrivals`). Forty rows that came with the
   page are not forty arrivals, and a re-render is not one.
3. **A figure counts up once** when it lands (`NumberTicker`, `LandedFigures`). A figure that
   follows a draft as it is typed does not count.
4. **A burst is let out a few lines a frame** (`useTranscriptDrip`), so a console does not jump,
   and only while the reader is at the end and not searching.
5. **New things never move what is being read.** A list the reader has scrolled, a log they are
   searching and a row they are on stay put; what arrived is counted on a control that brings it
   into view.
6. **Nothing moves under reduced motion or Pause animations**, and a picture's wires hold while
   the tab is hidden. The readings still update.

## When a read stops

| What happened | What the block does |
| --- | --- |
| The read after a good one failed | keeps the last reading and says *Not updating* beside it (`Stale`); a page says `StaleNotice` with its age and one Refresh |
| The first read failed | `ErrorState` in the block's place, with Try again |
| The thing is not there | `MissingThing`: an absence, not a failure |
| The socket dropped | it reconnects with backoff; the head says the figures are the last frame |

A failed poll backs off and keeps asking. It does not clear the block and does not raise a toast
every interval.

## Cost

A block reads only what it draws. A read for something below the fold can wait until it is near
(`useNearViewport`), a hidden tab stops polling, and two blocks that need one thing share one read.

## Accessibility

A state that changes while the reader is elsewhere is said in a polite live region: a `Status`
word, or `StatusWords` where the state is not drawn as one. A console is a `log`. A figure that
ticks every two seconds is not announced, and a breathing dot is decoration: its word carries the
state.

## Held by

`design-system.spec.ts` (*reduced motion is honoured globally*, *a wire pulses …*), the unit tests
`poll-share.test.js` and `use-poll.test.js`, and the page specs that fail a second read and
expect the first reading to stay. Whether a block may say Live is review.

## Related

[`data-flow.md`](data-flow.md) (the reads, the sockets, the metrics state),
[`../foundations/motion.md`](../foundations/motion.md),
[`../components/states.md`](../components/states.md),
[`../components/feedback.md`](../components/feedback.md), [`page-holds-still.md`](page-holds-still.md).
