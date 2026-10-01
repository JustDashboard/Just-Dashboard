"use client"

import { useState } from "react"
import { Copy } from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { duration } from "@/lib/format"
import type { DbConnection, DbStatement, DbStatements } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { BarList } from "@/components/bar-list"
import { Modal } from "@/components/modal"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelHeader, Well } from "@/components/panel"
import { Button } from "@/components/ui/button"

/**
 * Which statements cost the most, summed over every time they ran.
 *
 * A session list says what is running now; this says what has been running
 * all week, which is the other question — a query that takes ten
 * milliseconds and runs ten thousand times an hour is never caught running
 * and is the top row here. Drawn as a ranked list with a bar, the honest way
 * to show a top ten, in the shape the request log's facets take. The Queries
 * view of a server's log shows it beside the statements the server logged
 * one by one, under its own title.
 */
export function StatementsPanel({
  conn,
  title = "Slowest statements",
}: {
  conn: DbConnection
  title?: string
}) {
  const [detail, setDetail] = useState<DbStatement | null>(null)
  const statements = usePoll(
    (signal) => get<DbStatements>(`/databases/${conn.id}/statements`, { limit: 15 }, signal),
    30_000,
    [conn.id],
  )
  const data = statements.data
  if (!data) return null
  if (!data.supported) {
    return (
      <Panel plain>
        <PanelHeader title={title} />
        <p className="text-hint text-muted-foreground">
          {data.reason ?? "This engine keeps no per-statement statistics."}
        </p>
      </Panel>
    )
  }
  const longest = data.statements.reduce((m, s) => Math.max(m, s.totalMs), 0)
  return (
    <Panel plain className="animate-rise">
      <PanelHeader
        title={title}
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {duration(data.totalMs / 1000)} of query time tracked
          </span>
        }
      />
      <BarList
        emptyLabel="Nothing recorded yet."
        items={data.statements.map((s) => ({
          key: s.id || s.query,
          label: s.query.replace(/\s+/g, " ").trim(),
          value: (
            <span title={`${s.calls.toLocaleString()} calls · ${s.meanMs.toFixed(1)} ms each`}>
              {duration(s.totalMs / 1000)}
              <span className="text-muted-foreground"> · {s.calls.toLocaleString()}×</span>
            </span>
          ),
          share: longest > 0 ? s.totalMs / longest : 0,
          signal: s.hitRatio >= 0 && s.hitRatio < 0.9 ? 1 - s.hitRatio : 0,
          onClick: () => setDetail(s),
          title: "Open the statement",
        }))}
      />
      {detail && (
        <Modal
          open
          onOpenChange={(o) => !o && setDetail(null)}
          size="lg"
          title="Statement"
          footer={
            <>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => void copyText(detail.query, "Statement copied")}
              >
                <Copy className="size-3.5" />
                Copy
              </Button>
              <Button variant="ghost" onClick={() => setDetail(null)}>
                Close
              </Button>
            </>
          }
        >
          <div className="grid gap-4">
            <DetailList>
              <Detail label="Calls">{detail.calls.toLocaleString()}</Detail>
              <Detail label="Total">{duration(detail.totalMs / 1000)}</Detail>
              <Detail label="Mean">{detail.meanMs.toFixed(2)} ms</Detail>
              {detail.maxMs !== undefined && detail.maxMs > 0 && (
                <Detail label="Slowest">{detail.maxMs.toFixed(2)} ms</Detail>
              )}
              <Detail label="Rows">{detail.rows.toLocaleString()}</Detail>
              {detail.hitRatio >= 0 && (
                <Detail label="From cache">{(detail.hitRatio * 100).toFixed(1)}%</Detail>
              )}
            </DetailList>
            <Well className="max-h-80 text-hint whitespace-pre-wrap">{detail.query}</Well>
          </div>
        </Modal>
      )}
    </Panel>
  )
}
