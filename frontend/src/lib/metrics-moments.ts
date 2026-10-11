/**
 * Moments that happened together, read as one incident.
 *
 * A spike rarely arrives alone: the deploy that failed is also the minute CPU
 * hit 97%, load passed the core count and the disk queued — and listed one
 * per row, that read as five unrelated rows all stamped 00:30:22, with the
 * one that explained the others somewhere in the middle. Grouped by time, it
 * is one incident led by whatever stood furthest out, with the rest as the
 * signals it came with.
 */

export type Signal = {
  id: string
  ts: number
  /**
   * How far past its own threshold the signal went — a peak of twice the
   * line is 2. Events take a fixed weight by severity, so a failure always
   * leads the spikes it caused.
   */
  weight: number
}

export type Incident<T extends Signal> = {
  id: string
  /** The lead signal's instant: where pinning and zooming land. */
  ts: number
  from: number
  to: number
  lead: T
  /** The other signals, heaviest first. */
  rest: T[]
}

/**
 * How close two signals have to be to count as one incident: a fortieth of
 * the window, and never under two minutes — the width a marker takes on the
 * window's own timeline, so two incidents never draw as one mark.
 */
export function incidentReach(span: number): number {
  return Math.max(span / 40, 120_000)
}

/**
 * Groups the signals into incidents, keeps the `limit` heaviest, and returns
 * them newest first.
 */
export function groupIncidents<T extends Signal>(
  signals: T[],
  span: number,
  limit = 8,
): Incident<T>[] {
  const reach = incidentReach(span)
  const sorted = [...signals].sort((a, b) => a.ts - b.ts)
  const groups: T[][] = []
  let last = -Infinity
  for (const signal of sorted) {
    if (groups.length === 0 || signal.ts - last > reach) groups.push([])
    groups[groups.length - 1].push(signal)
    last = signal.ts
  }
  const incidents = groups.map((group) => {
    const byWeight = [...group].sort((a, b) => b.weight - a.weight)
    const lead = byWeight[0]
    return {
      id: lead.id,
      ts: lead.ts,
      from: group[0].ts,
      to: group[group.length - 1].ts,
      lead,
      rest: byWeight.slice(1),
    }
  })
  return incidents
    .sort((a, b) => b.lead.weight - a.lead.weight)
    .slice(0, limit)
    .sort((a, b) => b.ts - a.ts)
}

/** Whether a pinned instant belongs to an incident — it may have been pinned on a chart. */
export function holds(incident: Incident<Signal>, ts: number, span: number): boolean {
  const reach = incidentReach(span) / 2
  return ts >= incident.from - reach && ts <= incident.to + reach
}
