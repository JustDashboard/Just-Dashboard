/**
 * How long each player has been on, as far as this page can truthfully say.
 *
 * The game reports who is online and nothing about when they joined, so the
 * reading is what the page itself observed: a name that was not in the last
 * answer and is in this one joined between the two, and a name that was
 * already there when the page first asked has been on since some time before
 * that. The two are said differently ("joined 3m ago", "on before 14:02")
 * rather than giving the second a join time nobody saw.
 *
 * The record is kept for the tab, so a visit to the Console and back does not
 * forget it. A gap in the observations is a gap in what the page knows — a
 * player could have left and come back inside it — so a record older than
 * `STALE_AFTER_MS` is started again from the next answer.
 */

export type PlayerSighting = {
  /** When the page first had this player in an answer. */
  at: string
  /** The page saw them arrive, rather than finding them already on. */
  arrived: boolean
}

export type PlayerRecord = {
  /** The answer this record was last brought up to. */
  observedAt: string
  players: Record<string, PlayerSighting>
}

/** Three of the page's twenty-second polls: one missed answer is not a gap. */
export const STALE_AFTER_MS = 60_000

/**
 * The record brought up to one more answer. Players who are gone leave it, so
 * one who returns is seen to arrive again.
 */
export function observePlayers(
  previous: PlayerRecord | undefined,
  names: string[],
  observedAt: string,
): PlayerRecord {
  const now = Date.parse(observedAt)
  const last = previous ? Date.parse(previous.observedAt) : NaN
  const continuous = Number.isFinite(now) && Number.isFinite(last) && now - last <= STALE_AFTER_MS
  // An answer older than the record (a slow response overtaken by a newer
  // one) has nothing to add to it.
  if (previous && continuous && now < last) return previous
  const players: Record<string, PlayerSighting> = {}
  for (const name of names) {
    players[name] =
      (continuous ? previous?.players[name] : undefined) ??
      ({ at: observedAt, arrived: continuous } satisfies PlayerSighting)
  }
  return { observedAt, players }
}

/** Whether an answer would change the record, so the page stores it only then. */
export function sameRecord(a: PlayerRecord | undefined, b: PlayerRecord): boolean {
  if (!a || a.observedAt !== b.observedAt) return false
  const names = Object.keys(b.players)
  return (
    names.length === Object.keys(a.players).length &&
    names.every(
      (name) =>
        a.players[name]?.at === b.players[name].at &&
        a.players[name]?.arrived === b.players[name].arrived,
    )
  )
}
