# Table scroll reveal

A capped table sized to fit under its page's header is half below the fold whenever the page is not
at its top. The wheel over such a table now moves the page first, bringing the whole table container
on screen, and only then scrolls its rows. Every `Table` in the product gets this through
`ui/table.tsx`; see the shell notes in
[`docs/internal/frontend/shell-design.md`](../../internal/frontend/shell-design.md).

[Recording (MP4, 28 s)](processes-table-reveal.mp4) · [Inline GIF](processes-table-reveal.gif)

The recording runs on `/processes` at 1280×800 against this branch's production build with the
labelled API fixtures from `frontend/tests/browser/processes-ui.spec.ts` (80 mocked processes). The
cursor, wheel badge and captions are overlays drawn by the recording script. It shows, in order: the
table cut off at the bottom; one wheel step over the table gliding the page until the table is fully
visible, with the rows unmoved; the wheel then scrolling the rows down and back; the page scrolled
back up from outside the table; and a fast burst of wheel over the table that reveals first and
then scrolls the rows.

`frontend/tests/browser/processes-ui.spec.ts` ("the wheel over a half-shown table…") asserts the
reveal downward and upward, that the rows hold still while the page moves, and that a table already
in view keeps the wheel. `frontend/src/lib/scroll-reveal.test.js` covers the geometry: no move when
in view, the nearest move either way, a region taller than the port, clamping, and that applying the
answer settles to 0.
