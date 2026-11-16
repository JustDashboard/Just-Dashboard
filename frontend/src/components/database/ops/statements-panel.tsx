"use client"

import { RankList, RankSkeleton } from "@/components/rank-list"
import { Panel, PanelHeader } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { millis } from "@/components/database/kit/figures"
import { Read, Stale } from "@/components/database/kit/read"
import { useTopStatements } from "@/components/database/kit/top-statements"
import { NotAvailable } from "@/components/database/ops/performance-parts"

/** How many statements the list holds. */
const TOP = 15

/**
 * Which statements cost the most, summed over every time they ran.
 *
 * A session list says what is running now; this says what has been running
 * all week, which is the other question — a query that takes ten
 * milliseconds and runs ten thousand times an hour is never caught running
 * and is the top row here. Drawn as a ranked list of figures, its share of the
 * runtime at the right, in the shape the request log's facets take. It is
 * the third reading of a server's Queries view, beside the statements the
 * server logged one by one — on the database's own page and on the host's
 * Logs page alike, which is why it asks for nothing but the connection's id.
 * The whole list, in any order, is the database's Performance page, and a
 * row opens the statement's own page there (`performance-statement`): its
 * figures, its text, its plan and its history. Until 0.8.7 a row opened a
 * dialog of this panel's own, a second detail of the same statement with a
 * strip of tiles and no plan.
 *
 * A read that fails says so and can be tried again: it used to draw nothing,
 * and a failure read as a server with no statements.
 */
export function StatementsPanel({
  id,
  title = "Top statements",
}: {
  /** The connection's id. */
  id: number
  title?: string
  /**
   * Where a statement was opened to be run, when this panel had a dialog of
   * its own. The statement's page offers it now; the callers that still pass
   * it are not broken by it.
   */
  onQuery?: (sql: string) => void
}) {
  const { items, state } = useTopStatements(id, { limit: TOP })
  const data = state.data
  return (
    <Panel plain aria-label={title}>
      <PanelHeader
        title={title}
        count={data?.supported ? `${millis(data.totalMs)} of statement time counted` : undefined}
        actions={<Stale poll={state} />}
      />
      <div className="pt-3">
        <Read
          poll={state}
          what="the statement statistics"
          skeleton={<RankSkeleton rows={8} />}
          rise
        >
          {(answer) =>
            !answer.supported ? (
              <NotAvailable
                title="No statement statistics"
                reason={
                  answer.enable?.note ??
                  answer.reason ??
                  "This engine keeps no per-statement statistics."
                }
              />
            ) : items.length === 0 ? (
              <EmptyNote>Nothing has been counted yet. The list fills as statements run.</EmptyNote>
            ) : (
              <RankList items={items} />
            )
          }
        </Read>
      </div>
    </Panel>
  )
}
