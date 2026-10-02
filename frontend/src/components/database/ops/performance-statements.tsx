"use client"

import { useMemo, useState } from "react"
import { CodeBracket, Route } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { cn } from "@/lib/utils"
import { percent, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import { FormFact, FormNote } from "@/components/form"
import { Metric, MetricStrip, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
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
  explainStatement,
  readStatements,
  resetStatements,
} from "@/components/database/ops/performance-api"
import {
  isStatementSort,
  millis,
  planFor,
  rowsPerCall,
  shareWords,
  statementSorts,
} from "@/components/database/ops/performance-figures"
import {
  NoFigure,
  NotAvailable,
  Stale,
  StatementLine,
  ViewRead,
} from "@/components/database/ops/performance-parts"
import type {
  DbExplainResponse,
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
 * figures, with the plan where the engine can plan it and the Query page for
 * the rest.
 *
 * Where the statistics are off, the view says what turns them on, and offers
 * it where one press does.
 */
export function StatementsView() {
  const { id, conn, engine, readOnly, param, select } = useDatabase()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
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
  const openId = param("statement")
  const open = openId ? data?.statements.find((statement) => statement.id === openId) : undefined
  // Held as last seen: a statement that drops out of the top of the list
  // while its panel is open is still the one being read.
  const [kept, setKept] = useState<DbStatement | null>(null)
  if (open && open !== kept) setKept(open)
  const shown = openId ? (open ?? (kept?.id === openId ? kept : undefined)) : undefined

  const rows = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    const list = data?.statements ?? []
    return needle ? list.filter((s) => oneLine(s.query).toLowerCase().includes(needle)) : list
  }, [data, filter])
  const longest = Math.max(...(data?.statements ?? []).map((statement) => statement.share), 0)
  const slowest = (data?.statements ?? []).some((statement) => statement.maxMs !== undefined)
  const cached = (data?.statements ?? []).some((statement) => statement.hitRatio >= 0)

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
    <Panel plain aria-label="Statements">
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
                        {slowest && (
                          <TableHead className="px-2 text-right max-md:hidden">Slowest</TableHead>
                        )}
                        <TableHead className="px-2 text-right max-sm:hidden">Rows</TableHead>
                        {cached && (
                          <TableHead className="text-right max-lg:hidden">From cache</TableHead>
                        )}
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {rows.map((statement) => (
                        <TableRow
                          key={statement.id}
                          data-state={statement.id === openId ? "selected" : undefined}
                          onActivate={() => select({ statement: statement.id })}
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
                            <TableCell className="numeric px-2 py-2 text-right text-muted-foreground max-md:hidden">
                              {statement.maxMs === undefined ? (
                                <NoFigure />
                              ) : (
                                millis(statement.maxMs)
                              )}
                            </TableCell>
                          )}
                          <TableCell className="numeric px-2 py-2 text-right text-muted-foreground max-sm:hidden">
                            {compact(statement.rows)}
                          </TableCell>
                          {cached && (
                            <TableCell
                              className={cn(
                                "numeric py-2 text-right max-lg:hidden",
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
                  the server replaces every value before it leaves.
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
          statement={shown}
          gone={!open && Boolean(data)}
          onClose={() => select({ statement: null })}
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
 * One statement, opened: its figures, the whole of its text, and its plan.
 *
 * The plan is asked of the engine only for a statement that can be planned as
 * it stands. Most cannot: the text is a shape with `$1` where a value was, and
 * an engine plans values. Those are handed to the Query page, where the
 * reader puts values in and explains the result.
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
  const { id, engine, goto } = useDatabase()
  const { can } = useAuth()
  const [plan, setPlan] = useState<{
    for: string
    busy?: boolean
    answer?: DbExplainResponse
    error?: string
  } | null>(null)
  const own = plan?.for === statement.id ? plan : null
  const plans = planFor(statement.query)
  const each = rowsPerCall(statement)
  const mayQuery = engine.has("query") && can("service.control")

  const explain = async () => {
    setPlan({ for: statement.id, busy: true })
    try {
      const answer = await explainStatement(id, statement.query)
      setPlan({ for: statement.id, answer })
    } catch (err) {
      setPlan({ for: statement.id, error: errorMessage(err) })
    }
  }

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
          label={plans === "shape" ? "The statement, as a shape" : "The statement"}
          className="h-44"
          actions={
            mayQuery && (
              <Button
                size="xs"
                variant="ghost"
                onClick={() => goto("query", { sql: statement.query })}
              >
                <CodeBracket />
                Open in Query
              </Button>
            )
          }
        />

        <div className="space-y-2">
          <div className="flex min-h-7 items-center justify-between gap-3">
            <p className="eyebrow">Plan</p>
            {plans === "plan" && (
              <Button
                size="xs"
                variant="outline"
                pending={own?.busy}
                onClick={() => void explain()}
              >
                <Route />
                {own?.answer || own?.error ? "Explain again" : "Explain"}
              </Button>
            )}
          </div>
          {plans === "none" ? (
            <FormNote>
              Only a statement that reads or changes rows has a plan; this one does neither.
            </FormNote>
          ) : plans === "shape" ? (
            <FormNote>
              This text is a shape: the server put a placeholder where each value was, and an engine
              cannot plan a statement without its values.{" "}
              {mayQuery
                ? "Open it in Query, put values in, and explain it there."
                : "It can be explained on the Query page, with values put in."}
            </FormNote>
          ) : own?.error ? (
            <Notice tone="warning" title="The engine did not plan it">
              <p className="wrap-anywhere">{own.error}</p>
            </Notice>
          ) : own?.answer ? (
            <Plan answer={own.answer} />
          ) : (
            <FormNote>
              Explain asks the engine how it would run this statement. Nothing is executed.
            </FormNote>
          )}
        </div>
      </div>
    </SidePanel>
  )
}

/** A plan as the engine printed it: one column is lines of text, several are a table. */
function Plan({ answer }: { answer: DbExplainResponse }) {
  const { columns, rows } = answer.result
  if (columns.length <= 1) {
    return (
      <Well className="max-h-96 overflow-auto text-hint leading-relaxed whitespace-pre">
        {rows.map((row) => row[0] ?? "").join("\n")}
      </Well>
    )
  }
  return (
    // Framed: the plan is a table with a scroll of its own.
    <div className="overflow-hidden rounded-lg border">
      <Table containerClassName="max-h-96">
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            {columns.map((column) => (
              <TableHead key={column} className="h-8 px-2.5">
                {column}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row, index) => (
            <TableRow key={index}>
              {row.map((cell, at) => (
                <TableCell key={at} className="px-2.5 py-1.5 font-mono whitespace-pre">
                  {cell === null ? <span className="text-muted-foreground/60">NULL</span> : cell}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
