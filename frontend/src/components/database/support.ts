import type { DbEndOfLife } from "@/lib/types"

/**
 * A release's support status, said as a plain fact beside its version
 * wherever the version is said: "Supported until Nov 2030", "End of life
 * since Apr 30, 2026". A day still ahead is said to the month, which is how
 * far ahead anybody plans; one gone by is said to the day it was.
 *
 * In `calendarDate`'s words, so it reads like the "added Sep 1, 2026" beside
 * it, but on the UTC calendar: the date is a day, not a moment, and read in
 * the browser's own zone the last day of 8.0 was the 29th west of Greenwich.
 */
export function supportWords(eol: DbEndOfLife): string {
  const date = new Date(eol.date)
  return eol.past
    ? `End of life since ${date.toLocaleDateString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
        timeZone: "UTC",
      })}`
    : `Supported until ${date.toLocaleDateString(undefined, {
        year: "numeric",
        month: "short",
        timeZone: "UTC",
      })}`
}

/** The same, as one fact in a line of them: "supported until Nov 2030". */
export function supportFact(eol: DbEndOfLife): string {
  const words = supportWords(eol)
  return words.charAt(0).toLowerCase() + words.slice(1)
}
