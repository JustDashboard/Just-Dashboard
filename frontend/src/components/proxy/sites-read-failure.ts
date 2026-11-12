import { ApiError, errorMessage } from "@/lib/api"

/**
 * A read of the sites that failed, in the parts the page draws.
 *
 * The list is read from nginx, a Caddy on the host and the Caddy container
 * that holds the server's public ports. The last of those can fail on a
 * server where nothing is wrong with the first two: Docker does not answer,
 * two public Caddy containers and no word on which one is the ingress, or a
 * Caddy that publishes 80 without 443. The server has a sentence for each,
 * and the page showed "internal error" and nothing to press.
 */
export type SitesReadFailure = {
  /** What the server answered, or what the browser said when nothing did. */
  message: string
  /** Why it failed and what to do about it, where the server said. */
  reason?: string
  /** The words of whatever was asked underneath, for the disclosure. */
  raw?: string
}

/**
 * The codes `ErrorState` draws as a module this host does not have. Those
 * keep their own title: "Could not read the sites" over a server with no
 * proxy on it would be a failure where there is only an absence.
 */
const ABSENT = new Set(["docker_unavailable", "not_installed", "no_proxy", "terminal_disabled"])

/**
 * What to say of a failed read. A server that sends no reason — an older
 * one, or a failure it has no sentence for — still gets one where there is a
 * request id to look up, because a 500 hides its cause on purpose.
 */
export function sitesReadFailure(error: unknown): SitesReadFailure {
  const message = errorMessage(error)
  if (!(error instanceof ApiError)) return { message }
  const reason = said(error.reason, message)
  const raw = said(error.raw, message, reason)
  if (reason) return { message, reason, raw }
  if (error.code === "internal" && error.requestId) {
    return {
      message,
      reason: "The server did not say why. Its log has the cause on the line with that request id.",
      raw,
    }
  }
  return { message, raw }
}

/**
 * The error a first read that failed is drawn from: the same one, named for
 * what was being read, so the block opens on "Could not read the sites"
 * rather than on the two words a 500 carries. An answer that names its own
 * operation, and one that says the module is absent, is left as it came.
 */
export function sitesReadError(error: Error): Error {
  const api = error instanceof ApiError ? error : undefined
  if (api && (ABSENT.has(api.code) || (api.operation && api.resource))) return api
  const { message, reason, raw } = sitesReadFailure(error)
  return new ApiError(api?.status ?? 0, api?.code ?? "unreachable", message, undefined, {
    operation: "read",
    resource: "the sites",
    reason,
    raw,
    requestId: api?.requestId,
    // Reading the list changes nothing, so asking again is always safe.
    retryable: true,
  })
}

/** A sentence the server sent, unless it only repeats one already shown. */
function said(text: string | undefined, ...shown: (string | undefined)[]): string | undefined {
  const trimmed = text?.trim()
  if (!trimmed || shown.some((other) => other?.trim() === trimmed)) return undefined
  return trimmed
}
