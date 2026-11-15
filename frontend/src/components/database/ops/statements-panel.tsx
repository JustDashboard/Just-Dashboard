"use client"

import { useMemo } from "react"
import { useRouter } from "next/navigation"
import { usePoll } from "@/hooks/use-poll"
import { RankList, RankSkeleton, type RankItem } from "@/components/rank-list"
import { Panel, PanelHeader } from "@/components/panel"
import { CouldNotRead, NotUpdating } from "@/components/database/home/blocks"
import { statementVerb } from "@/components/database/home/kinds"
import { compact } from "@/components/database/home/readings"
import { statementHref } from "@/components/database/ops/performance-address"
import { readStatements } from "@/components/database/ops/performance-api"
import { millis, shareWords, statementKeys } from "@/components/database/ops/performance-figures"
import { oneLine } from "@/components/database/ops/queries"

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
  const router = useRouter()
  const statements = usePoll(
    (signal) => readStatements(id, { sort: "total", limit: TOP }, signal),
    30_000,
    [id],
  )
  const data = statements.data
  const items = useMemo<RankItem[]>(() => {
    const list = data?.statements ?? []
    // A row's key is its place in the address: one digest listed twice is
    // two statements, and one with no digest still has a page.
    const keys = statementKeys(list)
    return list.map((statement, index) => {
      const line = oneLine(statement.query)
      const verb = statementVerb(line)
      const share = shareWords(statement.share)
      // Reads that miss the cache are the part of a statement that is
      // trouble: said in amber beside its calls, where the bar used to carry it.
      const missed =
        statement.hitRatio >= 0 && statement.hitRatio < 0.9 ? 1 - statement.hitRatio : 0
      return {
        key: keys[index] || `no-id:${index}`,
        label: verb ? (
          <>
            <span style={{ color: verb.color }}>{verb.word}</span>
            {verb.rest}
          </>
        ) : (
          line
        ),
        title: `${line.slice(0, 60)} — ${share} of runtime. Open the statement`,
        value: share,
        hint: (
          <>
            {`${compact(statement.calls)} ${statement.calls === 1 ? "call" : "calls"} · ${millis(statement.meanMs)} each`}
            {missed > 0 && (
              <span className="text-warning">
                {" "}
                · {shareWords(missed)} of reads missed the cache
              </span>
            )}
          </>
        ),
        onClick: () => router.push(statementHref(id, keys[index])),
      }
    })
  }, [data, id, router])

  return (
    <Panel plain aria-label={title}>
      <PanelHeader
        title={title}
        actions={
          <>
            {data && statements.error && <NotUpdating error={statements.error} />}
            {data?.supported && (
              <span className="numeric text-hint text-muted-foreground">
                {millis(data.totalMs)} of statement time counted
              </span>
            )}
          </>
        }
      />
      <div className="pt-3">
        {!data ? (
          statements.error ? (
            <CouldNotRead
              what="the statement statistics"
              error={statements.error}
              onRetry={statements.refresh}
            />
          ) : (
            <RankSkeleton rows={8} />
          )
        ) : !data.supported ? (
          <p className="text-body leading-relaxed wrap-anywhere text-muted-foreground">
            {data.enable?.note ?? data.reason ?? "This engine keeps no per-statement statistics."}
          </p>
        ) : (
          <div className="animate-rise">
            <RankList items={items} emptyLabel="Nothing has been counted yet." />
          </div>
        )}
      </div>
    </Panel>
  )
}
