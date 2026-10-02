/** How long a closing dialog is waited for before the keyboard is left where it is. */
const WAIT_MS = 1500

/**
 * Hands the keyboard back once the dialog over the page has gone.
 *
 * A dialog returns focus to the control that opened it, and the dialogs of
 * this page are opened by none: a shortcut, the address moving, a menu that
 * has already closed, a button that left with the bar it stood in. Closing
 * one therefore dropped focus on the document, and the next Tab started from
 * the top of the dashboard. `focus` is where the reader was working.
 *
 * It waits for the dialog to leave the page — while it is closing it still
 * holds the keyboard — and gives way if the reader has put focus somewhere
 * themselves or another dialog has opened in its place.
 */
export function focusAfterDialog(focus: () => void) {
  const deadline = performance.now() + WAIT_MS
  const look = () => {
    if (document.querySelector("[data-slot=dialog-content]")) {
      if (performance.now() < deadline) requestAnimationFrame(look)
      return
    }
    const active = document.activeElement
    if (active === null || active === document.body) focus()
  }
  requestAnimationFrame(look)
}
