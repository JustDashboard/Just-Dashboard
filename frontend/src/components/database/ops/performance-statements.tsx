"use client"

import { useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { cn } from "@/lib/utils"
import { percent, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useSessionState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Segments } from "@/components/deploy/settings/segments"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { FormFact, InfoTip } from "@/components/form"
import { SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, LoadingPanel } from "@/components/state"
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
import { EngineMark } from "@/components/database/kit"
import { oneLine } from "@/components/database/ops/queries"
import { statementHref } from "@/components/database/ops/performance-address"
import {
  enableExtension,
  readStatements,
  resetStatements,
} from "@/components/database/ops/performance-api"
import {
  isStatementSort,
  millis,
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
import { statementsFilterKey } from "@/components/database/ops/performance-lists"
import { Fig, TitleFact } from "@/components/database/ops/performance-words"
import { useStatementsSetup } from "@/components/database/ops/statements-setup"
import { setupStart } from "@/components/database/ops/statements-setup-model"
import type { DbStatementSort, DbStatements } from "@/components/database/ops/performance-types"
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
 * and is the top row here. Each row says its share of everything the server
 * counted as the first of its figures, in a column that lines up down the
 * list: it was a bar under the statement until 0.8.7, a row twice the height
 * for a length the figure beside it already said. The list is asked for in
 * the order the reader chooses, since "slowest on average" and "called most"
 * are different lists and the server ranks each over everything it tracked.
 *
 * The text is a shape — every value in it is replaced before it leaves the
 * server, by the engine or by the dashboard where the engine keeps a statement
 * as it was sent — so a row is something to recognise. It opens the
 * statement's own page (`performance-statement`): its figures, the whole of
 * its text, its plan and its history.
 *
 * Where the statistics are off, the view says what turns them on, and offers
 * it where one press does.
 */
export function StatementsView() {
  const { id, conn, engine, readOnly, param, select } = useDatabase()
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useAsk()
  const focus = useReturnFocus()
  const [frame, width] = useColumnWidth<HTMLDivElement>()
  // Kept for the tab, under the key a statement's page reads: the rail beside
  // a statement is this list as it was left.
  const [filter, setFilter] = useSessionState(statementsFilterKey(id), "")
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
  const openSetup = useStatementsSetup(statements.refresh)
  // A row is addressed by a key no other row has: the engine's digest names
  // a statement twice when it ran in two schemas.
  const keyed = useMemo(() => {
    const list = data?.statements ?? []
    const keys = statementKeys(list)
    return list.map((statement, index) => ({ key: keys[index], statement }))
  }, [data])
  const rows = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return needle
      ? keyed.filter((row) => oneLine(row.statement.query).toLowerCase().includes(needle))
      : keyed
  }, [keyed, filter])
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
  // Where the header's controls wrap under its title, the filter is a row of
  // its own as wide as the list it filters, below the title and the reset.
  const narrow = width < 520
  const filterBox = data?.supported && (
    <SearchInput
      dense
      aria-label="Filter the statements"
      placeholder="Filter by text"
      value={filter}
      containerClassName={narrow ? "order-last basis-full" : "sm:w-56"}
      onChange={(event) => setFilter(event.target.value)}
    />
  )

  return (
    <Panel plain aria-label="Statements" ref={frame} className="focus-ring" {...RETURNS_FOCUS}>
      {dialog}
      <PanelHeader
        title="Statements"
        actions={
          <>
            {data?.supported && (
              <>
                <TitleFact>
                  <Fig>{millis(data.totalMs)}</Fig> of statement time counted
                  {data.since && (
                    <span title={timestamp(data.since)}> from {relativeTime(data.since)}</span>
                  )}
                </TitleFact>
                <InfoTip label="What a statement's text is">
                  The text is a shape: every value in it is replaced before it leaves the server,
                  and a statement&apos;s plan is asked for with values put back in.
                </InfoTip>
              </>
            )}
            <Stale poll={statements} />
            {!narrow && filterBox}
            {mayReset && (
              <Button size="sm" variant="outline" onClick={reset}>
                Reset statistics
              </Button>
            )}
          </>
        }
      >
        {narrow && filterBox}
      </PanelHeader>
      <ViewRead poll={statements} what="the statements" skeleton={<LoadingPanel plain rows={8} />}>
        {(answer) =>
          !answer.supported ? (
            <div className="pt-4">
              <NotCounting
                data={answer}
                enabling={enabling}
                onEnable={can("system.admin") && !readOnly ? enable : undefined}
                onSetup={
                  openSetup && engine.can("statementsSetup") && can("system.admin") && !readOnly
                    ? () => {
                        if (!answer.enable) return
                        focus.remember()
                        openSetup({ enable: answer.enable, onClose: focus.restore })
                      }
                    : undefined
                }
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
                {answer.statements.length >= limit && limit < MORE && (
                  <Button
                    size="xs"
                    variant="ghost"
                    className="ml-auto"
                    onClick={() => setLimit(MORE)}
                  >
                    Show the top {MORE}
                  </Button>
                )}
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
                          onActivate={() =>
                            router.push(
                              statementHref(id, key, { sort: sort === "total" ? null : sort }),
                            )
                          }
                          aria-label={`Open the statement: ${oneLine(statement.query).slice(0, 80)}`}
                        >
                          <TableCell className="w-full max-w-0 py-2">
                            <StatementLine text={statement.query} />
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
            </div>
          )
        }
      </ViewRead>
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
 * extension to create — an administrator is offered it. Where the module is
 * not loaded yet, the same words open the guided setup (`StatementsSetup`):
 * the library, the restart, the extension. Where the library list cannot be
 * changed from here, the server's reason is said beside what to do.
 */
function NotCounting({
  data,
  enabling,
  onEnable,
  onSetup,
}: {
  data: DbStatements
  enabling: boolean
  onEnable?: (extension: string) => void
  onSetup?: () => void
}) {
  const { engine } = useDatabase()
  const enable = data.enable
  const extension = enable?.sql ? enable.extension : undefined
  const guided = setupStart(enable) !== undefined
  const reason = [
    sentence(enable?.note ?? data.reason),
    enable?.blocked ? sentence(enable.blocked) : "",
  ]
    .filter(Boolean)
    .join(" ")
  return (
    <NotAvailable
      title={
        enable
          ? `This server is not counting its ${engine.nouns.statements}`
          : `The ${engine.nouns.statements} this server has run cannot be read`
      }
      reason={reason}
      action={
        extension && onEnable ? (
          <Button size="sm" pending={enabling} onClick={() => onEnable(extension)}>
            Turn on statement statistics
          </Button>
        ) : (
          guided &&
          onSetup && (
            <Button size="sm" onClick={onSetup}>
              Turn on statement statistics
            </Button>
          )
        )
      }
    />
  )
}
