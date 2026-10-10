import { useState } from "react"
import { driftMoves, type DriftMove, type DriftRow, type DriftStatus } from "@/lib/network-drift"

const KEEP = 8

const statuses = (rows: DriftRow[]) =>
  new Map<string, DriftStatus>(rows.map((r) => [r.id, r.status]))

/**
 * What changed under the reader between inspections: the rows whose status is
 * not the one the previous inspection saw, newest first, and how many
 * inspections have been made since the page opened. Kept as state adjusted
 * during render when a new inspection arrives (as `useArrivals` does) rather
 * than in an effect, and timed by the inspection's own clock so the render
 * stays pure.
 */
export function useDriftMoves(rows: DriftRow[], inspection: string) {
  const [seen, setSeen] = useState<{
    inspection: string
    statuses: Map<string, DriftStatus>
    moves: DriftMove[]
    inspections: number
  }>(() => ({ inspection, statuses: statuses(rows), moves: [], inspections: 1 }))
  if (seen.inspection === inspection) return seen
  const next = {
    inspection,
    statuses: statuses(rows),
    moves: [
      ...driftMoves(seen.statuses, rows, new Date(inspection).getTime()),
      ...seen.moves,
    ].slice(0, KEEP),
    inspections: seen.inspections + 1,
  }
  setSeen(next)
  return next
}
