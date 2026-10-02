"use client"

import { useMemo, useState } from "react"
import { CodeBracket, Copy } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { usePoll } from "@/hooks/use-poll"
import { BarList, type BarListItem } from "@/components/bar-list"
import { Modal } from "@/components/modal"
import { Metric, MetricStrip } from "@/components/page"
import { Panel, PanelHeader, Well } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { BarsSkeleton, CouldNotRead, NotUpdating } from "@/components/database/home/blocks"
import { statementVerb } from "@/components/database/home/kinds"
import { compact } from "@/components/database/home/readings"
import { readStatements } from "@/components/database/ops/performance-api"
import { millis, shareWords } from "@/components/database/ops/performance-figures"
import type { DbStatement } from "@/components/database/ops/performance-types"
import { oneLine } from "@/components/database/ops/queries"

/** How many statements the list holds. */
const TOP = 15

/**
 * Which statements cost the most, summed over every time they ran.
 *
 * A session list says what is running now; this says what has been running
 * all week, which is the other question — a query that takes ten
 * milliseconds and runs ten thousand times an hour is never caught running
 * and is the top row here. Drawn as a ranked list with a bar, the honest way
 * to show a top fifteen, in the shape the request log's facets take. It is
 * the third reading of a server's Queries view, beside the statements the
 * server logged one by one — on the database's own page and on the host's
 * Logs page alike, which is why it asks for nothing but the connection's id.
 * The whole list, in any order, with a statement's plan, is the database's
 * Performance page.
 *
 * A read that fails says so and can be tried again: it used to draw nothing,
 * and a failure read as a server with no statements.
 */
export function StatementsPanel({
  id,
  title = "Top statements",
  onQuery,
}: {
  /** The connection's id. */
  id: number
  title?: string
  /** Where a statement is opened to be run, for a role that may. */
  onQuery?: (sql: string) => void
}) {
  const [detail, setDetail] = useState<DbStatement | null>(null)
  const statements = usePoll(
    (signal) => readStatements(id, { sort: "total", limit: TOP }, signal),
    30_000,
    [id],
  )
  const data = statements.data
  const items = useMemo<BarListItem[]>(() => {
    const list = data?.statements ?? []
    const longest = Math.max(...list.map((statement) => statement.share), 0)
    return list.map((statement) => {
      const line = oneLine(statement.query)
      const verb = statementVerb(line)
      const share = shareWords(statement.share)
      return {
        key: statement.id || statement.query,
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
        share: longest > 0 ? statement.share / longest : 0,
        hint: `${compact(statement.calls)} ${statement.calls === 1 ? "call" : "calls"} · ${millis(statement.meanMs)} each`,
        // Reads that miss the cache are the part of the bar that is trouble.
        signal: statement.hitRatio >= 0 && statement.hitRatio < 0.9 ? 1 - statement.hitRatio : 0,
        tone: "warning",
        onClick: () => setDetail(statement),
      }
    })
  }, [data])

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
            <BarsSkeleton rows={8} />
          )
        ) : !data.supported ? (
          <p className="text-body leading-relaxed wrap-anywhere text-muted-foreground">
            {data.enable?.note ?? data.reason ?? "This engine keeps no per-statement statistics."}
          </p>
        ) : (
          <div className="animate-rise">
            <BarList items={items} emptyLabel="Nothing has been counted yet." />
          </div>
        )}
      </div>
      {detail && (
        <Modal
          open
          onOpenChange={(open) => !open && setDetail(null)}
          size="lg"
          initialFocus="body"
          title="Statement"
          description="A statement's figures and its text."
          footer={
            <>
              <Button
                variant="ghost"
                onClick={() => void copyText(detail.query, "Statement copied")}
              >
                <Copy />
                Copy
              </Button>
              {onQuery && (
                <Button variant="outline" onClick={() => onQuery(detail.query)}>
                  <CodeBracket />
                  Open in Query
                </Button>
              )}
              <Button variant="outline" onClick={() => setDetail(null)}>
                Close
              </Button>
            </>
          }
        >
          <div className="space-y-4">
            <MetricStrip>
              <Metric label="Of runtime" value={shareWords(detail.share)} />
              <Metric label="Calls" value={detail.calls.toLocaleString()} />
              <Metric label="Total" value={millis(detail.totalMs)} />
              <Metric label="Mean" value={millis(detail.meanMs)} />
              {detail.maxMs !== undefined && detail.maxMs > 0 && (
                <Metric label="Slowest run" value={millis(detail.maxMs)} />
              )}
              <Metric label="Rows" value={detail.rows.toLocaleString()} />
              {detail.hitRatio >= 0 && (
                <Metric label="From cache" value={`${(detail.hitRatio * 100).toFixed(1)}%`} />
              )}
            </MetricStrip>
            <Well className="max-h-80 overflow-auto text-hint leading-relaxed whitespace-pre-wrap">
              {detail.query}
            </Well>
          </div>
        </Modal>
      )}
    </Panel>
  )
}
