"use client"

import { useMemo, useState } from "react"
import { CodeBracket } from "@/components/icons"
import { cn } from "@/lib/utils"
import { percent, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Segments } from "@/components/deploy/settings/segments"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { FormFact, FormNote } from "@/components/form"
import { Metric, MetricStrip, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, LoadingPanel, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { compact } from "@/components/database/home/readings"
import { CodeView, EngineMark } from "@/components/database/kit"
import { oneLine } from "@/components/database/ops/queries"
import {
  enableExtension,
  readStatements,
  resetStatements,
} from "@/components/database/ops/performance-api"
import {
  isStatementSort,
  millis,
  planFor,
  rowsPerCall,
  shareWords,
  statementKeys,
  statementSorts,
} from "@/components/database/ops/performance-figures"
import {
  NoFigure,
  NotAvailable,
  Stale,
  StatementLine,
  ViewRead,
  useAsk,
  useReturnFocus,
  RETURNS_FOCUS,
} from "@/components/database/ops/performance-parts"
import { StatementPlan } from "@/components/database/ops/performance-plan"
import type {
  DbStatement,
  DbStatementSort,
  DbStatements,
} from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

/** How many statements the list asks for, and how many when the reader wants more. The server's cap is 200. */
const TOP = 50
const MORE = 200

/**
 * Which statements cost the most, summed over every time they ran.
 *
 * A session list says what is running now; this says what has been running
 * all week, which is the other question — a statement that takes ten
 * milliseconds and runs ten thousand times an hour is never caught running
 * and is the top row here. Each row carries its share of everything the
 * server counted as a bar, so the few statements that are most of the work
 * are seen before a figure is read; the list is asked for in the order the
 * reader chooses, since "slowest on average" and "called most" are different
 * lists and the server ranks each over everything it tracked.
 *
 * The text is a shape — the server replaces every value before it leaves — so
 * a row is something to recognise. It opens on the whole statement and its
 * figures, and on its plan: asked as it stands where it has no placeholder,
 * and with the values the reader puts in where it has.
 *
 * Where the statistics are off, the view says what turns them on, and offers
 * it where one press does.
 */
export function StatementsView() {
  const { id, conn, engine, readOnly, param, select } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useAsk()
  const focus = useReturnFocus()
  const [frame, width] = useColumnWidth<HTMLDivElement>()
  const [filter, setFilter] = useState("")
  const [limit, setLimit] = useState(TOP)
  const [enabling, setEnabling] = useState(false)
  const asked = param("sort")
  const sort: DbStatementSort = isStatementSort(asked) ? asked : "total"
  const statements = usePoll((signal) => readStatements(id, { sort, limit }, signal), 30_000, [
    id,
    sort,
    limit,
  ])
  const data = statements.data
  // A row is addressed by a key no other row has: the engine's digest names
  // a statement twice when it ran in two schemas.
  const keyed = useMemo(() => {
    const list = data?.statements ?? []
    const keys = statementKeys(list)
    return list.map((statement, index) => ({ key: keys[index], statement }))
  }, [data])
  const openKey = param("statement")
  const open = openKey ? keyed.find((row) => row.key === openKey)?.statement : undefined
  // Held as last seen: a statement that drops out of the top of the list
  // while its panel is open is still the one being read.
  const [kept, setKept] = useState<{ key: string; statement: DbStatement } | null>(null)
  if (open && (kept?.statement !== open || kept.key !== openKey)) {
    setKept({ key: openKey, statement: open })
  }
  const shown = openKey ? (open ?? (kept?.key === openKey ? kept.statement : undefined)) : undefined

  const rows = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return needle
      ? keyed.filter((row) => oneLine(row.statement.query).toLowerCase().includes(needle))
      : keyed
  }, [keyed, filter])
  const longest = Math.max(...keyed.map((row) => row.statement.share), 0)
  // The columns the view has room for, by its own width: the figures a
  // reader ranks by stay, and the ones that qualify them go first.
  const slowest = width >= 620 && keyed.some((row) => row.statement.maxMs !== undefined)
  const counted = width >= 520
  const cached = width >= 760 && keyed.some((row) => row.statement.hitRatio >= 0)

  const enable = async (extension: string) => {
    setEnabling(true)
    try {
      await enableExtension(id, extension)
      notify.success("Statement statistics are on", {
        description: "They count from now; the list fills as statements run.",
      })
      statements.refresh()
    } catch (err) {
      notify.error("Could not turn on statement statistics", err)
    } finally {
      setEnabling(false)
    }
  }

  const reset = () =>
    confirm({
      title: "Reset statement statistics",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: conn.name,
        facts: <FormFact label="Clears">every statement&apos;s figures</FormFact>,
      },
      description: (
        <p>
          Every figure in this list starts again from zero — for the whole server, not only this
          database. No data changes; what is lost is the record of which statements have cost the
          most until now.
        </p>
      ),
      confirmLabel: "Reset statistics",
      action: async () => {
        await resetStatements(id)
      },
      onDone: statements.refresh,
    })

  const mayReset =
    Boolean(data?.supported && data.resettable) &&
    engine.can("statementsReset") &&
    can("destructive") &&
    !readOnly

  return (
    <Panel plain aria-label="Statements" ref={frame} className="focus-ring" {...RETURNS_FOCUS}>
      {dialog}
      <PanelHeader
        title="Statements"
        actions={
          <>
            <Stale poll={statements} />
            {data?.supported && (
              <SearchInput
                dense
                aria-label="Filter the statements"
                placeholder="Filter by text"
                value={filter}
                containerClassName="sm:w-56"
                onChange={(event) => setFilter(event.target.value)}
              />
            )}
            {mayReset && (
              <Button size="sm" variant="outline" onClick={reset}>
                Reset statistics
              </Button>
            )}
          </>
        }
      />
      <ViewRead poll={statements} what="the statements" skeleton={<LoadingPanel plain rows={8} />}>
        {(answer) =>
          !answer.supported ? (
            <div className="pt-4">
              <NotCounting
                data={answer}
                enabling={enabling}
                onEnable={can("system.admin") && !readOnly ? enable : undefined}
              />
            </div>
          ) : (
            <div className="animate-rise space-y-3 pt-3">
              <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
                <Segments
                  label="Order the statements by"
                  value={sort}
                  options={statementSorts(answer.statements).map((entry) => ({
                    value: entry.id,
                    label: entry.label,
                  }))}
                  onChange={(next) => select({ sort: next === "total" ? null : next })}
                />
                <p className="min-w-48 flex-1 text-hint text-muted-foreground sm:text-right">
                  <span className="numeric font-medium text-foreground">
                    {millis(answer.totalMs)}
                  </span>{" "}
                  of statement time counted
                  {answer.since && (
                    <span title={timestamp(answer.since)}> since {relativeTime(answer.since)}</span>
                  )}
                </p>
              </div>
              <PanelBody flush className="group-data-[plain]/panel:-mx-4">
                {answer.statements.length === 0 ? (
                  <EmptyNote className="px-4">
                    No {engine.nouns.statement} has been counted yet. The list fills as they run.
                  </EmptyNote>
                ) : rows.length === 0 ? (
                  <EmptyNote className="px-4">No statement matches {filter.trim()}.</EmptyNote>
                ) : (
                  <Table>
                    <TableHeader>
                      <TableRow className="hover:bg-transparent">
                        <TableHead>Statement</TableHead>
                        <TableHead className="px-2 text-right">Of runtime</TableHead>
                        <TableHead className="px-2 text-right">Calls</TableHead>
                        <TableHead className="px-2 text-right">Mean</TableHead>
                        {slowest && <TableHead className="px-2 text-right">Slowest</TableHead>}
                        {counted && <TableHead className="px-2 text-right">Rows</TableHead>}
                        {cached && <TableHead className="text-right">From cache</TableHead>}
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map(({ key, statement }) => (
                        <TableRow
                          key={key}
                          data-state={key === openKey ? "selected" : undefined}
                          onActivate={() => {
                            focus.remember()
                            select({ statement: key })
                          }}
                          aria-label={`Open the statement: ${oneLine(statement.query).slice(0, 80)}`}
                        >
                          <TableCell className="w-full max-w-0 py-2">
                            <StatementLine text={statement.query} />
                            <span
                              aria-hidden
                              className="mt-1.5 block h-1 overflow-hidden rounded-full bg-meter-track"
                            >
                              <span
                                className="block h-full rounded-full bg-primary"
                                style={{
                                  width: `${Math.max(longest > 0 ? (statement.share / longest) * 100 : 0, 1)}%`,
                                }}
                              />
                            </span>
                          </TableCell>
                          <TableCell className="numeric px-2 py-2 text-right font-medium">
                            {shareWords(statement.share)}
                          </TableCell>
                          <TableCell className="numeric px-2 py-2 text-right">
                            {compact(statement.calls)}
                          </TableCell>
                          <TableCell className="numeric px-2 py-2 text-right">
                            {millis(statement.meanMs)}
                          </TableCell>
                          {slowest && (
                            <TableCell className="numeric px-2 py-2 text-right text-muted-foreground">
                              {statement.maxMs === undefined ? (
                                <NoFigure />
                              ) : (
                                millis(statement.maxMs)
                              )}
                            </TableCell>
                          )}
                          {counted && (
                            <TableCell className="numeric px-2 py-2 text-right text-muted-foreground">
                              {compact(statement.rows)}
                            </TableCell>
                          )}
                          {cached && (
                            <TableCell
                              className={cn(
                                "numeric py-2 text-right",
                                statement.hitRatio >= 0 && statement.hitRatio < 0.9
                                  ? "font-medium text-warning"
                                  : "text-muted-foreground",
                              )}
                            >
                              {statement.hitRatio < 0 ? (
                                <NoFigure />
                              ) : (
                                percent(statement.hitRatio * 100, 1)
                              )}
                            </TableCell>
                          )}
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </PanelBody>
              <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2">
                <FormNote>
                  The bar is a statement&apos;s share of all the time counted. The text is a shape:
                  the server replaces every value before it leaves, and a row&apos;s plan is asked
                  for with values put back in.
                </FormNote>
                {answer.statements.length >= limit && limit < MORE && (
                  <Button size="xs" variant="ghost" onClick={() => setLimit(MORE)}>
                    Show the top {MORE}
                  </Button>
                )}
              </div>
            </div>
          )
        }
      </ViewRead>

      {shown && (
        <StatementPanel
          // A panel is one statement's: the values typed for one shape are
          // not carried into the next.
          key={openKey}
          statement={shown}
          gone={!open && Boolean(data)}
          onClose={() => {
            select({ statement: null })
            focus.restore()
          }}
        />
      )}
    </Panel>
  )
}

/** A reason in the server's words, closed as the sentence it is. */
function sentence(text: string | undefined): string {
  const trimmed = (text ?? "").trim()
  return !trimmed || /[.!?]$/.test(trimmed) ? trimmed : `${trimmed}.`
}

/**
 * The statistics are off, or closed to this account. What would turn them on
 * is said in the server's own words, and where one statement is enough — an
 * extension to create — an administrator is offered it.
 */
function NotCounting({
  data,
  enabling,
  onEnable,
}: {
  data: DbStatements
  enabling: boolean
  onEnable?: (extension: string) => void
}) {
  const { engine } = useDatabase()
  const enable = data.enable
  const extension = enable?.sql ? enable.extension : undefined
  return (
    <NotAvailable
      title={
        enable
          ? `This server is not counting its ${engine.nouns.statements}`
          : `The ${engine.nouns.statements} this server has run cannot be read`
      }
      reason={sentence(enable?.note ?? data.reason)}
      action={
        extension &&
        onEnable && (
          <Button size="sm" pending={enabling} onClick={() => onEnable(extension)}>
            Turn on statement statistics
          </Button>
        )
      }
    />
  )
}

/**
 * One statement, opened: its figures, the whole of its text, and its plan
 * (`StatementPlan`).
 */
function StatementPanel({
  statement,
  gone,
  onClose,
}: {
  statement: DbStatement
  /** It is no longer among the statements the list holds. */
  gone: boolean
  onClose: () => void
}) {
  const { engine, goto } = useDatabase()
  const { can } = useAuth()
  const shape = planFor(statement.query) === "shape"
  const lines = statement.query.split("\n").length
  const each = rowsPerCall(statement)
  const mayQuery = engine.has("query") && can("service.control")
  const toQuery = (sql: string) => goto("query", { sql })

  return (
    <SidePanel
      open
      onOpenChange={(open) => !open && onClose()}
      width="lg"
      initialFocus="body"
      title="Statement"
      description="A statement's figures, its text and its plan."
    >
      <div className="space-y-5">
        {gone && (
          <Notice title="No longer among the top statements">
            <p>The list has moved on. These are its figures as they were last read.</p>
          </Notice>
        )}
        <MetricStrip>
          <Metric label="Of runtime" value={shareWords(statement.share)} />
          <Metric label="Calls" value={statement.calls.toLocaleString()} />
          <Metric label="Total" value={millis(statement.totalMs)} />
          <Metric label="Mean" value={millis(statement.meanMs)} />
          {statement.maxMs !== undefined && (
            <Metric label="Slowest run" value={millis(statement.maxMs)} />
          )}
          <Metric
            label="Rows"
            value={statement.rows.toLocaleString()}
            hint={
              each === undefined
                ? undefined
                : `${each >= 10 ? compact(each) : each.toFixed(1)} a call`
            }
          />
          {statement.hitRatio >= 0 && (
            <Metric label="From cache" value={percent(statement.hitRatio * 100, 1)} />
          )}
        </MetricStrip>

        <CodeView
          code={statement.query}
          language={engine.editor}
          label={shape ? "The statement, as a shape" : "The statement"}
          // As tall as the statement, up to what leaves the plan in sight.
          className={lines <= 2 ? "h-28" : lines <= 5 ? "h-44" : lines <= 12 ? "h-72" : "h-96"}
          actions={
            mayQuery && (
              <Button size="xs" variant="ghost" onClick={() => toQuery(statement.query)}>
                <CodeBracket />
                Open in Query
              </Button>
            )
          }
        />

        <StatementPlan text={statement.query} onQuery={mayQuery ? toQuery : undefined} />
      </div>
    </SidePanel>
  )
}
