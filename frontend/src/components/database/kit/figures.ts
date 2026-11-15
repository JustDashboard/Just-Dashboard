import { duration } from "@/lib/format"

/**
 * The figures a database's reading pages print, written once.
 *
 * Home and Performance had a `millis` each, and they disagreed: one statement
 * that ran in 0.004 ms read "0.00 ms" on the Home and "<0.01 ms" on its own
 * page, and a statement of 75 seconds read "75 s" on one and "1m 15s" on the
 * other. These are the Performance rules, which the Home now prints too.
 */

/** Milliseconds at the precision a reader compares them at. */
export function millis(value: number): string {
  if (value >= 60_000) return duration(value / 1000)
  if (value >= 1000) return `${(value / 1000).toFixed(value >= 10_000 ? 0 : 1)} s`
  if (value >= 10) return `${Math.round(value)} ms`
  // Something that ran took some time: under a hundredth is said as that,
  // never as a round nought.
  if (value > 0 && value < 0.005) return "<0.01 ms"
  return `${value.toFixed(value >= 1 ? 1 : 2)} ms`
}

/** A share of the whole as a percentage: whole numbers from ten up, a decimal under it, never "0%" for something that ran. */
export function shareWords(share: number): string {
  const value = share * 100
  if (value <= 0) return "0%"
  if (value < 0.1) return "<0.1%"
  return `${value.toFixed(value >= 10 ? 0 : 1)}%`
}

/**
 * The word with its first letter in capitals: "Tables" from an engine's
 * "tables", at the start of a title. The rest is left as it is, so "pgBouncer"
 * stays itself after the first letter. Fourteen places wrote this by hand.
 */
export function capitalize(word: string): string {
  return word ? word[0].toUpperCase() + word.slice(1) : word
}
