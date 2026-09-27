import { duration } from "@/lib/format"

/**
 * How old what the overview shows is, and whether a refresh it asked for has
 * come back.
 *
 * The overview reads six endpoints on four cadences — the sites and ports
 * every thirty seconds, the certificates every five minutes — and said
 * nothing about when any of them was read. A certificate renewed over ssh
 * stayed "expiring" for up to five minutes with no sign the page was that old
 * and no way to ask again short of reloading it.
 */

/** A poll's answer and the moment it arrived. */
export type Reading<T> = { value: T; at: number }

/**
 * Stamps a fetch with when it answered. The clock is read here, inside the
 * poll, rather than while rendering, which has to stay pure.
 */
export async function reading<T>(value: Promise<T>): Promise<Reading<T>> {
  const settled = await value
  return { value: settled, at: Date.now() }
}

/**
 * The oldest of the readings on show, which is the age the page can vouch
 * for: everything on it is at least this fresh. A source with no reading —
 * still loading, failed, or not asked — has none to count.
 */
export function oldestReading(times: (number | undefined)[]): number | undefined {
  const known = times.filter((at): at is number => at !== undefined)
  return known.length > 0 ? Math.min(...known) : undefined
}

/** "Updated 14s ago". The first seconds read as now rather than as a counter starting at 0s. */
export function updatedLabel(at: number, now: number): string {
  const age = Math.max(0, (now - at) / 1000)
  return age < 5 ? "Updated just now" : `Updated ${duration(age)} ago`
}

/**
 * What a poll last said: when its answer arrived, and its error, which is a
 * new object every time a read fails.
 */
export type Answer = { at: number | undefined; error: unknown }

/**
 * Whether a source asked to refresh has not answered since it was asked.
 *
 * A read that succeeds is stamped later than the one before it, and one that
 * fails leaves a new error beside the last answer, so a source whose stamp
 * and error are both the ones it had when Refresh was pressed has not come
 * back yet. A source whose poll has been switched off since is not waited for.
 */
export function stillRefreshing(
  asked: Record<string, Answer>,
  current: Record<string, Answer>,
): boolean {
  return Object.entries(asked).some(([source, before]) => {
    const now = current[source]
    return now !== undefined && now.at === before.at && now.error === before.error
  })
}
