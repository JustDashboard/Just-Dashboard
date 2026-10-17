import type { DbEndOfLife } from "@/lib/types"

/**
 * A release's support status, said as a plain fact beside its version
 * wherever the version is said: "Supported until 2030-11", "End of life since
 * 2026-04-30". A day still ahead is said to the month, which is how far ahead
 * anybody plans; one gone by is said to the day it was.
 */
export function supportWords(eol: DbEndOfLife): string {
  return eol.past
    ? `End of life since ${eol.date.slice(0, 10)}`
    : `Supported until ${eol.date.slice(0, 7)}`
}

/** The same, as one fact in a line of them: "supported until 2030-11". */
export function supportFact(eol: DbEndOfLife): string {
  const words = supportWords(eol)
  return words.charAt(0).toLowerCase() + words.slice(1)
}
