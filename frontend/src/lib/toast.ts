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

/** What a failure says under a toast's title: the error's message, as a sentence. */
export function failureReason(cause: unknown): string {
  return asSentence(errorMessage(cause))
}

export const notify = {
  success(title: string, options?: Options) {
    return sonner.success(title, options)
  },

  info(title: string, options?: Options) {
    return sonner.info(title, options)
  },

  warning(title: string, options?: Options) {
    return sonner.warning(title, options)
  },

  /**
   * A failure, with the reason underneath it.
   *
   * The second argument is the error itself rather than a string, because the
   * unwrapping is the part that kept being done differently. An error message
   * can be long — a `pg_dump` refusal names two versions and two Debian build
   * strings — so these stay up until dismissed rather than sliding away while
   * somebody is still reading.
   *
   * The reason is the API's message, which is worded to sit inside other text:
   * "not found", "too many requests, slow down". Under a title it stands
   * alone, so it is given its capital and its full stop here, once, and not by
   * each caller (`asSentence`, which leaves a tool's own output as it came).
   */
  error(title: string, cause?: unknown, options?: Options) {
    const description = cause === undefined ? options?.description : failureReason(cause)
    return sonner.error(title, {
      ...options,
      description,
      duration: options?.duration ?? (description ? 12_000 : undefined),
    })
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
