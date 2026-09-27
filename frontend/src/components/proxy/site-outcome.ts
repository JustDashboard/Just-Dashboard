import type { ProxyValidation, SiteDeleteResult, VHostLinkResult } from "@/lib/types"

/**
 * What a change to a site actually did, in words.
 *
 * The site verbs used to read nothing back: a delete whose reload failed and a
 * disable nginx could not load both came up as "completed", and a refused
 * enable said only "configuration failed validation". These turn the answers
 * into the one line a toast can carry and the output behind it.
 */

const ERROR_LEVELS = new Set(["emerg", "alert", "crit", "error", "fatal", "panic"])

/** nginx's first error in a failed test, with the file and line it names. */
export function failureHeadline(validation: ProxyValidation): string | undefined {
  const error = validation.diagnostics?.find((d) => ERROR_LEVELS.has(d.level))
  if (error)
    return error.file && error.line
      ? `${error.message} in ${error.file}:${error.line}`
      : error.message
  return validation.output
    .split("\n")
    .map((line) => line.trim())
    .find(Boolean)
}

/**
 * Why nginx is not running a change that landed, or undefined when it is (or
 * was not asked to reload). A delete answers "configuration failed
 * validation" when the test refused its reload; the reason is in the test it
 * sends beside that, and is what the operator needs.
 */
export function reloadFailure(
  result: Pick<SiteDeleteResult | VHostLinkResult, "reload" | "reloadError">,
): string | undefined {
  if (!result.reloadError) return undefined
  const validation = result.reload?.validation
  const headline = validation && !validation.valid ? failureHeadline(validation) : undefined
  if (headline && !result.reloadError.includes(headline)) return `nginx -t failed: ${headline}`
  return result.reloadError
}

/** The whole of what nginx printed about a failed reload, for the operator who wants it. */
export function reloadOutput(
  result: Pick<SiteDeleteResult | VHostLinkResult, "reload">,
): string | undefined {
  const reload = result.reload
  if (!reload) return undefined
  if (!reload.validation.valid) return reload.validation.output || undefined
  return reload.output || undefined
}
