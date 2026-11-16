import { toast as sonner } from "sonner"
import { errorMessage } from "@/lib/api"
import { asSentence } from "@/lib/format"

/**
 * The app's toasts, in one place.
 *
 * Every call site was reaching for sonner directly and deciding for itself how
 * to turn a failure into words, which produced three different habits across
 * one page: some passed `String(err)`, which prefixes the name of the exception
 * class and put "ApiError:" in front of a message meant for a person; some
 * passed `err.message`; some passed nothing and left the reader with "Could not
 * save" and no reason. `notify.error` takes the error itself and unwraps it, so
 * getting that right is not something each caller has to remember.
 *
 * It is a thin layer over sonner rather than a replacement for it. Sonner is
 * the shadcn default and renders through `components/ui/sonner`, where the
 * theming and the layout live; this is only about what is *said* and how the
 * arguments are shaped, so anything sonner can do is still reachable through
 * `notify.raw`.
 */

type Options = {
  /** Longer explanation under the title. */
  description?: string
  /** Milliseconds; omit for the default, `Infinity` to require dismissal. */
  duration?: number
  action?: { label: string; onClick: () => void }
}

/**
 * How long a toast that is not a failure stays. Sonner's own four seconds is
 * under the five a short line needs to be found and read by somebody whose
 * eyes were on the form, and a success often carries a second line.
 */
export const TOAST_MS = 6_000

/** A passing toast: the caller's duration, or the default. */
function passing(options?: Options) {
  return { ...options, duration: options?.duration ?? TOAST_MS }
}

/**
 * The failures on screen, by what they say. A failure stays until dismissed,
 * so the same one raised again (a second press on a dead button, a poll that
 * keeps failing) would otherwise pile up copies of one sentence. The earlier
 * copy leaves and the new one arrives, which also says it happened again.
 */
const standing = new Map<string, string | number>()

/** What a failure says under a toast's title: the error's message, as a sentence. */
export function failureReason(cause: unknown): string {
  return asSentence(errorMessage(cause))
}

export const notify = {
  success(title: string, options?: Options) {
    return sonner.success(title, passing(options))
  },

  info(title: string, options?: Options) {
    return sonner.info(title, passing(options))
  },

  warning(title: string, options?: Options) {
    return sonner.warning(title, passing(options))
  },

  /**
   * A failure, with the reason underneath it.
   *
   * The second argument is the error itself rather than a string, because the
   * unwrapping is the part that kept being done differently. An error message
   * can be long — a `pg_dump` refusal names two versions and two Debian build
   * strings — so these stay up until dismissed rather than sliding away while
   * somebody is still reading. That holds for a failure with no reason too:
   * "Not connected" explains why a press did nothing, and a reader who looked
   * away for four seconds was left with a button that silently does not work.
   * The toaster draws a close button on every toast, and a caller that knows
   * its failure is passing gives a `duration`.
   *
   * The reason is the API's message, which is worded to sit inside other text:
   * "not found", "too many requests, slow down". Under a title it stands
   * alone, so it is given its capital and its full stop here, once, and not by
   * each caller (`asSentence`, which leaves a tool's own output as it came).
   */
  error(title: string, cause?: unknown, options?: Options) {
    const description = cause === undefined ? options?.description : failureReason(cause)
    const said = `${title}\n${description ?? ""}`
    const earlier = standing.get(said)
    if (earlier !== undefined) sonner.dismiss(earlier)
    const forget = () => {
      if (standing.get(said) === id) standing.delete(said)
    }
    const id = sonner.error(title, {
      ...options,
      description,
      duration: options?.duration ?? Infinity,
      onDismiss: forget,
      onAutoClose: forget,
    })
    standing.set(said, id)
    return id
  },

  /** A toast that becomes a result. Returns sonner's id so it can be updated. */
  loading(title: string, options?: Options) {
    return sonner.loading(title, options)
  },

  dismiss(id?: string | number) {
    return sonner.dismiss(id)
  },

  /** Everything else sonner offers, for the cases this shape does not cover. */
  raw: sonner,
}
