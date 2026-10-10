"use client"

import type { ReactNode } from "react"
import type { DbFleetEntry } from "@/lib/types"
import { GroupRule } from "@/components/flow"
import type { FleetGroup } from "@/components/database/fleet/fleet"
import { useColumns } from "@/components/database/fleet/use-columns"

/** The narrowest a card is drawn, and the gap between two: 19rem and `gap-3`. */
const CARD_MIN = 304
const CARD_GAP = 12

/**
 * The fleet's cards on their shelves: each shelf named by a rule, its cards
 * under it.
 *
 * A shelf is as wide as its cards. A server usually has most of its databases
 * in one place and one or two elsewhere — a file, a managed server — and with
 * every shelf a row of its own those were rows of one card and three columns
 * of nothing. Here a shelf takes one column for each card it has, up to the
 * whole row, so short shelves stand side by side under their own rules and a
 * long one still runs the page's width and wraps.
 */
export function Shelves({
  groups,
  leading,
  card,
}: {
  groups: FleetGroup[]
  /** A mark before a shelf's name: the engine's, when the shelves are engines. */
  leading?: (group: FleetGroup) => ReactNode
  card: (entry: DbFleetEntry) => ReactNode
}) {
  const [grid, columns] = useColumns<HTMLDivElement>(CARD_MIN, CARD_GAP)
  return (
    <div
      ref={grid}
      className="grid min-w-0 gap-x-3 gap-y-5"
      style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}
    >
      {groups.map((group) => {
        const span = Math.min(group.entries.length, columns)
        return (
          <div
            key={group.key}
            className="flex min-w-0 flex-col gap-3"
            style={{ gridColumn: `span ${span}` }}
          >
            <GroupRule
              label={group.label}
              count={group.entries.length}
              leading={leading?.(group)}
            />
            <ul
              aria-label={group.label}
              className="grid min-w-0 gap-3"
              style={{ gridTemplateColumns: `repeat(${span}, minmax(0, 1fr))` }}
            >
              {group.entries.map(card)}
            </ul>
          </div>
        )
      })}
    </div>
  )
}
