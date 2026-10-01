"use client"

import { PaneFooter } from "@/components/panel"
import type { ChangeCounts } from "./change-set"
import { describeAggregate, describeStatus, type GridStatus } from "./status"

/**
 * The strip under the grid: how many rows, what is selected, what the numbers
 * in the selection come to, and how much is staged.
 *
 * Counts are plain tabular text beside their words (a count is not a badge),
 * and the three change counts wear the same three hues the rows do, with the
 * same signs, so the strip and the gutter read as one legend. The owner's own
 * controls — pagination, a commit bar — go on the right.
 */
export function GridStatusBar({
  status,
  counts,
  message,
  children,
}: {
  /** Null while there is nothing true to say: the rows are loading, or could not be read. */
  status: GridStatus | null
  counts: ChangeCounts | null
  /** What just happened, said once: "Copied 24 cells". Read out by a screen reader. */
  message: string
  children?: React.ReactNode
}) {
  const words = status ? describeStatus(status) : []
  const figures = status?.aggregate ? describeAggregate(status.aggregate) : []
  return (
    <PaneFooter data-slot="data-grid-status" className="gap-x-4 gap-y-1">
      <div className="numeric flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
        {words.map((word) => (
          <span key={word} className="whitespace-nowrap">
            {word}
          </span>
        ))}
        {figures.map(([label, figure]) => (
          <span key={label} className="whitespace-nowrap">
            {label} <span className="font-mono text-foreground">{figure}</span>
          </span>
        ))}
        {status?.aggregate && !status.aggregate.exact && (
          <span className="whitespace-nowrap">approximate</span>
        )}
        {counts && counts.total > 0 && (
          <span className="flex items-center gap-2 font-mono whitespace-nowrap">
            {counts.inserts > 0 && (
              <span className="text-(--git-added)">
                +{counts.inserts}
                <span className="sr-only"> new</span>
              </span>
            )}
            {counts.updates > 0 && (
              <span className="text-(--git-modified)">
                ~{counts.updates}
                <span className="sr-only"> edited</span>
              </span>
            )}
            {counts.deletes > 0 && (
              <span className="text-(--git-deleted)">
                −{counts.deletes}
                <span className="sr-only"> to delete</span>
              </span>
            )}
          </span>
        )}
        <span aria-live="polite" className="min-w-0 truncate text-foreground">
          {message}
        </span>
      </div>
      {children && <div className="ml-auto flex min-w-0 items-center gap-2">{children}</div>}
    </PaneFooter>
  )
}
