"use client"

import { useState } from "react"
import {
  ChartActivity,
  Copy,
  CrossCircle,
  Database,
  Gauge,
  Layout,
  LockClosed,
  Slash,
  Stopwatch,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { bytes, duration, plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  DbActivity,
  DbActivityResponse,
  DbConnection,
  DbOverview,
  DbStatement,
  DbStatements,
  DbTableSize,
} from "@/lib/types"
import { BarList } from "@/components/bar-list"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Detail, DetailList } from "@/components/page"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Meter } from "@/components/meter"
import { copyText } from "@/lib/clipboard"

/**
 * The Monitor tab answers the two questions asked at three in the morning, and
 * only those: what is the server doing right now, and what is using the disk.
 *
 * Both are things every database can be asked and almost no dashboard shows.
 * The application has gone slow and the developer has no idea whether the
 * database is the cause; or the disk alert fired and nobody knows which table
 * grew. The alternative to this panel is remembering `pg_stat_activity` and
 * `pg_total_relation_size` — and their four different spellings on the other
 * engines — under exactly the pressure that makes people mistype them.
 */
export function MonitorTab({
  conn,
  schema,
  confirm,
  onOpenTable,
}: {
  conn: DbConnection
  schema: string
  confirm: ReturnType<typeof useConfirm>["confirm"]
  onOpenTable?: (schema: string, table: string) => void
}) {
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <ActivityPanel conn={conn} confirm={confirm} />
      <StatementsPanel conn={conn} />
      <StoragePanel conn={conn} schema={schema} onOpenTable={onOpenTable} />
    </div>
  )
}

function ActivityPanel({
  conn,
  confirm,
}: {
  conn: DbConnection
  confirm: ReturnType<typeof useConfirm>["confirm"]
}) {
  const { can } = useAuth()
  const [detail, setDetail] = useState<DbActivity | null>(null)
  // Five seconds: fast enough that a query you are watching does not vanish
  // between refreshes, slow enough that the panel is not itself a load.
  const activity = usePoll(
    (signal) => get<DbActivityResponse>(`/databases/${conn.id}/activity`, undefined, signal),
    5000,
    [conn.id],
  )

  if (activity.loading && !activity.data) return <LoadingPanel />
  if (activity.error) return <ErrorState error={activity.error} />
  if (!activity.data) return null

  if (!activity.data.supported) {
    return (
      <Notice tone="default" icon={Slash} title="No session list on this engine">
        {activity.data.reason ??
          "This engine has no server-side session concept, so there is nothing running to list."}
      </Notice>
    )
  }

  const sessions = activity.data.sessions
  const blocked = sessions.filter((s) => s.blockedBy)
  // A session named as a blocker is the one worth finding in a list of forty.
  const blockers = new Set(sessions.flatMap((s) => (s.blockedBy ?? "").split(",")).filter(Boolean))
  const slowest = sessions.reduce((m, s) => Math.max(m, s.seconds), 0)

  return (
    <Panel className="animate-rise">
      <PanelHeader
        title="Running now"
        actions={
          <>
            {blocked.length > 0 && (
              <Status verdict="critical" icon={LockClosed} label={`${blocked.length} blocked`} />
            )}
            {slowest > 60 && (
              <Status verdict="warning" icon={Stopwatch} label={`longest ${duration(slowest)}`} />
            )}
          </>
        }
      />
      <PanelBody flush>
        {sessions.length === 0 ? (
          <EmptyState
            icon={ChartActivity}
            title="Nothing running"
            description="The server reports no active sessions."
          />
        ) : (
          <div className="min-w-0 overflow-x-auto group-data-[plain]/panel:-mx-4">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-24">Session</TableHead>
                  <TableHead className="w-40">User</TableHead>
                  <TableHead className="w-24">State</TableHead>
                  <TableHead className="w-24 text-right">For</TableHead>
                  <TableHead>Statement</TableHead>
                  <TableHead className="w-32">Waiting on</TableHead>
                  <TableHead className="w-20" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {sessions.map((s) => (
                  <TableRow
                    key={s.pid}
                    onClick={() => setDetail(s)}
                    className={cn(
                      "cursor-pointer",
                      s.blockedBy && "bg-wash-danger",
                      blockers.has(s.pid) && "bg-wash-warning",
                    )}
                  >
                    <TableCell className="font-mono">
                      {s.pid}
                      {s.self && (
                        <span className="ml-1.5 text-micro text-muted-foreground">
                          this dashboard
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="truncate text-muted-foreground">
                      {s.user || "—"}
                      {s.client ? ` · ${s.client}` : ""}
                    </TableCell>
                    <TableCell>{s.state || "—"}</TableCell>
                    <TableCell
                      className={cn(
                        "text-right font-mono text-xs tabular-nums",
                        s.seconds > 60 && "text-warning",
                      )}
                    >
                      {duration(s.seconds)}
                    </TableCell>
                    <TableCell className="max-w-0">
                      <code className="block truncate font-mono text-xs" title={s.query}>
                        {s.query || "—"}
                      </code>
                    </TableCell>
                    <TableCell>
                      {s.blockedBy ? (
                        <span className="text-destructive">blocked by {s.blockedBy}</span>
                      ) : (
                        <span className="text-muted-foreground">{s.wait || "—"}</span>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      {can("destructive") && !s.self && (
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-destructive"
                          // The row opens the detail; this must not do both.
                          onClick={(e) => {
                            e.stopPropagation()
                            confirm({
                              title: "Stop this session",
                              // No phrase to type: this button is pressed
                              // repeatedly under exactly the time pressure
                              // that makes a typing exercise harmful, and
                              // nothing is lost that was not already going to
                              // roll back. The dialog names the session and
                              // shows its statement, which is what identifies
                              // the right one.
                              confirmLabel: "Stop it",
                              description: (
                                <>
                                  <p>
                                    Terminates session <b>{s.pid}</b> on the server. Whatever it has
                                    done so far rolls back, and the application holding that
                                    connection will see it drop.
                                  </p>
                                  {s.query && (
                                    <Well className="mt-2 max-h-32 text-hint whitespace-pre-wrap">
                                      {s.query}
                                    </Well>
                                  )}
                                </>
                              ),
                              action: async (c) => {
                                await post(
                                  `/databases/${conn.id}/activity/kill`,
                                  { pid: s.pid },
                                  { confirm: c },
                                )
                                activity.refresh()
                              },
                            })
                          }}
                        >
                          <CrossCircle className="size-3.5" />
                          Stop
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </PanelBody>

      {detail && (
        <SessionDialog
          session={detail}
          onClose={() => setDetail(null)}
          onStop={
            can("destructive") && !detail.self
              ? () => {
                  const s = detail
                  setDetail(null)
                  confirm({
                    title: "Stop this session",
                    confirmLabel: "Stop it",
                    description: (
                      <p>
                        Terminates session <b>{s.pid}</b> on the server. Whatever it has done so far
                        rolls back, and the application holding that connection will see it drop.
                      </p>
                    ),
                    action: async (c) => {
                      await post(
                        `/databases/${conn.id}/activity/kill`,
                        { pid: s.pid },
                        { confirm: c },
                      )
                      activity.refresh()
                    },
                  })
                }
              : undefined
          }
        />
      )}
    </Panel>
  )
}

/**
 * One session, in full.
 *
 * The row can only ever show the first eighty characters of a statement, which
 * is exactly the part that is the same for every slow query in a workload — the
 * `SELECT` and the first column. Deciding whether *this* is the session to stop
 * means reading the whole thing, and everything the engine reported about it.
 */
function SessionDialog({
  session,
  onClose,
  onStop,
}: {
  session: DbActivity
  onClose: () => void
  onStop?: () => void
}) {
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      size="lg"
      title={
        <>
          Session {session.pid}
          {session.self && <Tag>this dashboard</Tag>}
        </>
      }
      footer={
        <>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              void copyText(session.query ?? "", "Statement copied")
            }}
            disabled={!session.query}
          >
            <Copy className="size-3.5" />
            Copy statement
          </Button>
          <div className="flex gap-2">
            <Button variant="ghost" onClick={onClose}>
              Close
            </Button>
            {onStop && (
              <Button variant="destructive" onClick={onStop}>
                <CrossCircle className="size-3.5" />
                Stop session
              </Button>
            )}
          </div>
        </>
      }
    >
      <DetailList className="gap-y-2">
        <Detail label="User">{session.user || "—"}</Detail>
        <Detail label="Database">{session.database || "—"}</Detail>
        <Detail label="State">{session.state || "—"}</Detail>
        <Detail label="Running for">{duration(session.seconds)}</Detail>
        <Detail label="Client">{session.client || "—"}</Detail>
        <Detail label="Waiting on">{session.wait || "nothing"}</Detail>
        {session.blockedBy && <Detail label="Blocked by">{`session ${session.blockedBy}`}</Detail>}
      </DetailList>

      <div className="space-y-1.5">
        <p className="eyebrow">Statement</p>
        <Well className="max-h-72 text-hint whitespace-pre-wrap">
          {session.query || (
            <span className="text-muted-foreground italic">
              No statement reported — this session is idle.
            </span>
          )}
        </Well>
      </div>
    </Modal>
  )
}

/**
 * One table's storage, broken down.
 *
 * The row answers "which table is the big one". This answers the question that
 * follows it, which is always "is that the data or the indexes" — a table whose
 * indexes outweigh its rows is a different problem from one that simply has a
 * lot of rows, and the row in the table cannot say which it is.
 */
function TableSizeDialog({
  size,
  totalBytes,
  onClose,
  onOpen,
}: {
  size: DbTableSize
  totalBytes: number
  onClose: () => void
  onOpen?: () => void
}) {
  const indexRatio = size.dataBytes > 0 ? size.indexBytes / size.dataBytes : 0
  const perRow = size.rows > 0 ? size.bytes / size.rows : 0
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={size.table}
      footer={
        <>
          {onOpen ? (
            <Button size="sm" variant="ghost" onClick={onOpen}>
              <Layout className="size-3.5" />
              Browse this table
            </Button>
          ) : (
            <span />
          )}
          <Button variant="ghost" onClick={onClose}>
            Close
          </Button>
        </>
      }
    >
      <DetailList>
        <Detail label="Schema">{size.schema || "—"}</Detail>
        <Detail label="Rows">
          <span title="The engine's own estimate, not an exact count">
            {size.rows.toLocaleString()}
          </span>
        </Detail>
        <Detail label="Data">{size.dataBytes ? bytes(size.dataBytes) : "—"}</Detail>
        <Detail label="Indexes">{size.indexBytes ? bytes(size.indexBytes) : "—"}</Detail>
        <Detail label="Total">{size.bytes ? bytes(size.bytes) : "—"}</Detail>
        {totalBytes > 0 && (
          <Detail label="Share of schema">{`${((size.bytes / totalBytes) * 100).toFixed(1)}%`}</Detail>
        )}
        {perRow > 0 && (
          <Detail label="Average row">
            <span title="Total bytes divided by the estimated row count">{bytes(perRow)}</span>
          </Detail>
        )}
        {indexRatio > 0 && (
          <Detail label="Index to data">
            <span
              title={
                indexRatio > 1
                  ? "The indexes on this table are larger than the rows they index"
                  : undefined
              }
            >{`${indexRatio.toFixed(2)}×`}</span>
          </Detail>
        )}
      </DetailList>

      {indexRatio > 1 && (
        <Notice tone="warning" title="More index than data">
          Every write to this table maintains more index than row. That is sometimes right — a
          read-heavy lookup table — and sometimes an index nobody uses.
        </Notice>
      )}
    </Modal>
  )
}

/**
 * Which statements cost the most, summed over every time they ran.
 *
 * The activity list says what is running now; this says what has been
 * running all week, which is the other question — a query that takes ten
 * milliseconds and runs ten thousand times an hour never appears above and
 * is the top row here. Drawn as a ranked list with a bar, the honest way to
 * show a top ten, in the shape the request log's facets take.
 */
function StatementsPanel({ conn }: { conn: DbConnection }) {
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
        <PanelHeader title="Slowest statements" />
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
        title="Slowest statements"
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

function StoragePanel({
  conn,
  schema,
  onOpenTable,
}: {
  conn: DbConnection
  schema: string
  onOpenTable?: (schema: string, table: string) => void
}) {
  const [detail, setDetail] = useState<DbTableSize | null>(null)
  const [expanded, setExpanded] = useState(false)
  // Sizes move on the scale of a deploy, not a request, so this is polled far
  // more slowly than the session list beside it.
  const overview = usePoll(
    (signal) => get<DbOverview>(`/databases/${conn.id}/overview`, { schema }, signal),
    120000,
    [conn.id, schema],
  )

  if (overview.loading && !overview.data) return <LoadingPanel />
  if (overview.error) return <ErrorState error={overview.error} />
  if (!overview.data) return null

  const o = overview.data
  const shown = expanded ? o.tables : o.tables.slice(0, 10)
  // The engine lists by size, but the bar is drawn against the largest table
  // wherever it sits, so an engine that orders differently still compares.
  const largest = o.tables.reduce((m, t) => Math.max(m, t.bytes), 0)
  const pool = o.pool

  return (
    <Panel className="animate-rise">
      <PanelHeader
        title="Storage"
        actions={
          <span
            className="numeric inline-flex items-center gap-1.5 text-xs text-muted-foreground"
            title="The dashboard's own connection pool"
          >
            <Gauge className="size-3 shrink-0" />
            pool {pool.inUse}/{pool.open}
            {pool.waitCount > 0 ? ` · ${pool.waitCount} waits` : ""}
          </span>
        }
      />
      <PanelBody flush>
        {!o.sizesKnown && (
          <Notice tone="default" className="mb-3" title="Sizes unavailable on this engine">
            This build of the engine does not report per-table bytes, so only row counts are shown.
            They are still the fastest way to find the table that grew.
          </Notice>
        )}
        {o.tables.length === 0 ? (
          <EmptyState icon={Database} title="No tables in this schema" />
        ) : (
          <div className="min-w-0 overflow-x-auto group-data-[plain]/panel:-mx-4">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Table</TableHead>
                  <TableHead className="w-28 text-right">Rows</TableHead>
                  <TableHead className="w-24 text-right">Data</TableHead>
                  <TableHead className="w-24 text-right">Indexes</TableHead>
                  <TableHead className="w-28 text-right">Total</TableHead>
                  <TableHead className="w-40">Share</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((t) => (
                  <TableRow
                    key={`${t.schema}.${t.table}`}
                    onClick={() => setDetail(t)}
                    className="cursor-pointer"
                  >
                    <TableCell className="font-mono">{t.table}</TableCell>
                    <TableCell
                      className="text-right font-mono text-muted-foreground tabular-nums"
                      title="The engine's own estimate, not an exact count"
                    >
                      {t.rows.toLocaleString()}
                    </TableCell>
                    <TableCell className="text-right font-mono text-muted-foreground tabular-nums">
                      {t.dataBytes ? bytes(t.dataBytes) : "—"}
                    </TableCell>
                    <TableCell className="text-right font-mono text-muted-foreground tabular-nums">
                      {t.indexBytes ? bytes(t.indexBytes) : "—"}
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {t.bytes ? bytes(t.bytes) : "—"}
                    </TableCell>
                    <TableCell>
                      {/* A bar against the largest table rather than against
                          the total: the question is "which one is the big one",
                          and against a total every row in a wide schema is a
                          sliver. */}
                      <Meter
                        value={largest ? (t.bytes / largest) * 100 : 0}
                        label={`${t.table} relative to the largest table`}
                      />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {o.tables.length > 10 && (
              <div className="border-t border-hairline p-2 text-center">
                <Button size="sm" variant="ghost" onClick={() => setExpanded(!expanded)}>
                  {expanded ? "Show top 10" : `Show all ${plural(o.tables.length, "table")}`}
                </Button>
              </div>
            )}
          </div>
        )}
      </PanelBody>

      {detail && (
        <TableSizeDialog
          size={detail}
          totalBytes={o.totalBytes}
          onClose={() => setDetail(null)}
          onOpen={
            onOpenTable
              ? () => {
                  onOpenTable(detail.schema || o.schema, detail.table)
                  setDetail(null)
                }
              : undefined
          }
        />
      )}
    </Panel>
  )
}
