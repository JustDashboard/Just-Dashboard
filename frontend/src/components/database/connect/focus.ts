/**
 * Hands the keyboard back to the control a dialog was opened from.
 *
 * A dialog returns focus by itself to whatever had it when it opened. Two of
 * the ways these pages open one leave nothing to return to: a choice in a
 * menu (the item is gone by the time the dialog closes) and a row that redraws
 * itself as busy while the request that ends in the dialog is in flight. Then
 * the keyboard is left on the document, at the top of the page, after every
 * cancelled confirmation.
 *
 * So the caller says which control stands for the thing the dialog was about
 * — by its accessible name, which is how the control is found again whatever
 * was redrawn in between — and once the dialog has had its own turn, focus
 * goes there if it landed nowhere.
 */
export function returnFocus(label: string | undefined) {
  if (!label) return
  // The dialog keeps the keyboard while it leaves, and makes its own attempt
  // as it unmounts; this waits for that to be over, for at most a second.
  let turns = 0
  const timer = window.setInterval(() => {
    turns += 1
    const active = document.activeElement
    const leaving = active?.closest("[role=dialog], [role=alertdialog], [role=menu]")
    if (active && active !== document.body && !leaving) {
      // It went somewhere of its own accord.
      window.clearInterval(timer)
      return
    }
    if (!leaving) {
      window.clearInterval(timer)
      for (const control of document.querySelectorAll<HTMLElement>(
        "button[aria-label], a[aria-label]",
      )) {
        if (control.getAttribute("aria-label") !== label) continue
        control.focus()
        return
      }
    }
    if (turns >= 20) window.clearInterval(timer)
  }, 50)
}
