import { notify } from "@/lib/toast"

/**
 * The failure, in one sentence.
 *
 * Seventeen files had written their own copy-to-clipboard, and between them they
 * had five different words for the same refusal: "Could not copy", "Could not
 * reach the clipboard", "The browser refused clipboard access", the first with
 * "Select the code and copy it by hand." underneath, and the same pair again
 * with the description in lower case. A failure that reads differently
 * depending on which panel you were in is one the reader cannot learn.
 */
const REFUSED =
  "The browser would not give the page clipboard access. Select the text and copy it by hand."

/**
 * Puts text on the clipboard and says what happened.
 *
 * The clipboard needs a user gesture and a secure context. Over plain HTTP on a
 * LAN address — which is how this dashboard is reached before its certificate is
 * set up — `navigator.clipboard` is simply absent, so it is checked rather than
 * assumed: the call sites that omitted the guard threw a TypeError into an
 * unhandled rejection and reported nothing at all.
 *
 * `announce` is what the toast says. Passing nothing means no toast — for the
 * callers that show an inline tick instead (see `useCopy`) and for
 * copy-on-select in the terminal, where a toast on every drag would be
 * unusable.
 */
export async function copyText(text: string, announce?: string): Promise<boolean> {
  try {
    if (!navigator.clipboard) throw new Error("no clipboard")
    await navigator.clipboard.writeText(text)
  } catch {
    notify.error("Could not copy", undefined, { description: REFUSED })
    return false
  }
  if (announce) notify.success(announce)
  return true
}

/** The same, for the paths where a failure is not worth interrupting anybody. */
export async function copyTextQuietly(text: string): Promise<boolean> {
  try {
    if (!navigator.clipboard) return false
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

/**
 * Text that is still being made (a request's answer), promised to the
 * clipboard inside the gesture: a write made after the request came back is
 * one Safari refuses. Throws when the browser refuses, for the caller to say
 * what was not copied.
 */
export async function copyPending(text: Promise<string>): Promise<void> {
  if (!navigator.clipboard?.write) throw new Error("no clipboard")
  await navigator.clipboard.write([
    new ClipboardItem({
      "text/plain": text.then((value) => new Blob([value], { type: "text/plain" })),
    }),
  ])
}

/** Whether the page may read the clipboard at all (a secure context that offers it). */
export function canPaste(): boolean {
  return typeof navigator !== "undefined" && navigator.clipboard?.readText !== undefined
}

/** The clipboard's text. Throws when the browser will not let the page read it. */
export async function pasteText(): Promise<string> {
  if (!canPaste()) throw new Error("no clipboard")
  return navigator.clipboard.readText()
}
