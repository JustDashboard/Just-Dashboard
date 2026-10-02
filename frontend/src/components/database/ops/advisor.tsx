"use client"

import type { SectionId, SectionParams } from "@/components/database/engine"
import { read } from "@/components/database/home/read"
import { EngineGlyph, SectionFrame } from "@/components/database/kit"
import { ApplyDialog, type ApplyRequest } from "@/components/database/ops/advisor-apply"
import { FindingBody } from "@/components/database/ops/advisor-finding"
import {
  CATEGORIES,
  LEVELS,
  aboutWords,
  countCategories,
  findingKeys,
  isCategory,
  isLevel,
  keptFindings,
  type DbAdvice,
  type DbAdviseReport,
} from "@/components/database/ops/advisor-fix"
import { runScript, timed } from "@/components/database/ops/performance-api"
import { useMaintenance } from "@/components/database/ops/performance-maintenance"
import {
  RETURNS_FOCUS,
  ServerDown,
  Stale,
  ViewRead,
  isDown,
  useReturnFocus,
} from "@/components/database/ops/performance-parts"
import { useDatabase } from "@/components/database/shell/database-context"
import { databasePlace } from "@/components/database/shell/routes"
import { FindingList, type Finding } from "@/components/finding-list"
import { RefreshClockwise } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { usePoll } from "@/hooks/use-poll"
import { calendarDate, plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useCallback, useMemo, useState } from "react"

export function Advisor() {
  const { status } = useDatabase()
  return (
    <SectionFrame section="advisor">
      {isDown(status.state) ? <ServerDown what="The advisor's report" /> : <Report />}
    </SectionFrame>
  )
}

/**
 * How many findings a report opens on arrival. A report this short arrives
 * open from top to bottom; a longer one with its worst few open and the rest
 * as a list to choose from, so that the page is never only a list of titles
 * and never a wall either.
 */
const OPEN_ALL_UP_TO = 6
const OPEN_WORST = 3

function Report() {
  const { id, engine, param, select, goto } = useDatabase()
  const report = usePoll(
    (signal) =>
      timed(
        read<DbAdviseReport>(
          `/databases/${id}/advisor`,
          (answer) => Array.isArray(answer.findings),
          undefined,
          signal,
        ),
      ),
    300_000,
    [id],
  )
  const refresh = report.refresh
  const maintenance = useMaintenance(refresh)
  const [applying, setApplying] = useState<ApplyRequest | null>(null)
  const focus = useReturnFocus()

  const askedLevel = param("level")
  const level = isLevel(askedLevel) ? askedLevel : null
  const askedCategory = param("category")
  const category = isCategory(askedCategory) ? askedCategory : null

  const data = report.data
  // The chips count what the severity leaves, so a chip never promises rows
  // the severity filter has hidden.
  const categories = useMemo(
    () => countCategories(keptFindings(data?.findings ?? [], { level })),
    [data, level],
  )

  const applyStatements = useCallback(
    async (sql: string) => {
      try {
        const result = await runScript(id, sql)
        const ran = result.statements.filter((step) => step.status === "ok").length
        if (result.failed >= 0) {
          const step = result.statements[result.failed]
          notify.error(
            ran > 0
              ? `The fix stopped after ${plural(ran, "statement")}`
              : "The fix was not applied",
            step?.error ?? "The server refused the statement.",
          )
        } else {
          notify.success(`Applied: ${plural(ran, "statement")} ran`, {
            description: "The report is being read again.",
          })
        }
      } catch (err) {
        notify.error("The fix was not applied", err)
      }
      refresh()
    },
    [id, refresh],
  )

  const ask = useCallback(
    (request: ApplyRequest) => {
      focus.remember()
      setApplying(request)
    },
    [focus],
  )

  const all = data?.findings ?? []
  const keys = findingKeys(all)
  const kept = new Set(keptFindings(all, { level, category }))
  const findings: Finding[] = all.flatMap((advice, index) =>
    kept.has(advice)
      ? [
          {
            id: keys[index],
            level: advice.level,
            title: advice.title,
            detail: advice.detail,
            advice: advice.advice,
            meta: <About advice={advice} />,
            extra: <FindingBody advice={advice} maintenance={maintenance} onApply={ask} />,
            action: linkAction(advice, {
              id,
              title: (section) => engine.section(section)?.title,
              open: goto,
            }),
          },
        ]
      : [],
  )

  // What the page opens on. A report is read for what each finding is about
  // and what fixes it, and a page of closed one-line rows says neither: a
  // short report arrives open, a long one with its worst finding open — the
  // server lists them worst first — and a link to one finding opens that one.
  const asked = param("finding")
  const arrival = asked ? [asked] : keys.length <= OPEN_ALL_UP_TO ? keys : keys.slice(0, OPEN_WORST)
  const rests = Boolean(data && (data.silences.length > 0 || data.endOfLife))

  return (
    <>
      {maintenance.dialogs}
      {applying && (
        <ApplyDialog
          request={applying}
          onCancel={() => {
            setApplying(null)
            focus.restore()
          }}
          onStatements={async (sql) => {
            await applyStatements(sql)
            setApplying(null)
            focus.restore()
          }}
          onMaintenance={(fix) => {
            setApplying(null)
            // The run's own dialog takes over, and hands the keyboard back
            // to the button that asked when it closes.
            maintenance.run(
              {
                key: fix.action.id,
                label: fix.action.label,
                action: fix.action,
                request: fix.request,
              },
              focus.from.current,
            )
          }}
        />
      )}
      {!data ? (
        // The checks read every table's catalogue entry, and wait behind a
        // lock like any other read of it: a wait says so, and so does a
        // failure that followed one.
        <ViewRead
          poll={report}
          what="the advisor's report"
          locking={engine.can("locks")}
          skeleton={
            <>
              <LoadingPanel plain rows={5} />
            </>
          }
        >
          {() => null}
        </ViewRead>
      ) : (
        <>
          {(data.truncated || data.tablesOmitted > 0) && (
            <Notice tone="warning" title="Part of this database was not checked">
              <p>
                {data.truncated
                  ? `It holds more ${engine.nouns.objects} than the advisor walks in one report: the structure checks covered the first ${data.tablesChecked.toLocaleString()}.`
                  : `${plural(data.tablesOmitted, engine.nouns.object)} ${data.tablesOmitted === 1 ? "was" : "were"} left out of the structure checks.`}{" "}
                A finding about one of the others would not be listed here.
              </p>
            </Notice>
          )}

          {/* The findings, and beside them — where the window has the width —
              what the report rests on: both plain, the gap between them is
              what separates them. */}
          <div
            className={cn(
              "grid items-start gap-8 [&>*]:min-w-0",
              rests && "2xl:grid-cols-[minmax(0,1fr)_20rem]",
            )}
          >
            <Panel plain aria-label="Findings" className="focus-ring" {...RETURNS_FOCUS}>
              <PanelHeader
                title="Findings"
                actions={
                  <>
                    <Stale poll={report} />
                    <Button size="sm" variant="outline" pending={report.loading} onClick={refresh}>
                      <RefreshClockwise />
                      Check again
                    </Button>
                  </>
                }
              />
              <PanelBody className="animate-rise space-y-3">
                <ChipStrip role="group" aria-label="Findings by severity">
                  <FilterChip selected={!level} onClick={() => select({ level: null })}>
                    All severities
                  </FilterChip>
                  {LEVELS.map((entry) => (
                    <FilterChip
                      key={entry.id}
                      selected={level === entry.id}
                      onClick={() => select({ level: level === entry.id ? null : entry.id })}
                    >
                      {entry.label}
                    </FilterChip>
                  ))}
                </ChipStrip>

                {data.findings.length > 0 && (
                  <ChipStrip role="group" aria-label="Findings by category">
                    <FilterChip selected={!category} onClick={() => select({ category: null })}>
                      All
                      <ChipCount>
                        {keptFindings(data.findings, { level }).length.toLocaleString()}
                      </ChipCount>
                    </FilterChip>
                    {CATEGORIES.filter(
                      (entry) => categories[entry.id] > 0 || entry.id === category,
                    ).map((entry) => (
                      <FilterChip
                        key={entry.id}
                        selected={category === entry.id}
                        onClick={() =>
                          select({ category: category === entry.id ? null : entry.id })
                        }
                      >
                        {entry.label}
                        <ChipCount>{categories[entry.id].toLocaleString()}</ChipCount>
                      </FilterChip>
                    ))}
                  </ChipStrip>
                )}
                {/* Not keyed on the filter: a finding the reader opened is
                    still open when a filter that hid it is taken off. */}
                <FindingList
                  findings={findings}
                  defaultOpen={arrival}
                  anchor={(key) => `finding-${key}`}
                  emptyLabel={
                    data.findings.length === 0
                      ? `The advisor checked ${plural(data.tablesChecked, engine.nouns.object)}${data.engineChecks ? " and the server's settings" : ""}, and found nothing to fix`
                      : "No finding of that kind"
                  }
                />
              </PanelBody>
            </Panel>

            {rests && (
              <Panel plain aria-label="What the report rests on">
                <PanelHeader
                  title="What the report rests on"
                  actions={
                    // As tall as the command in the header beside it, so the
                    // two hairlines are one line across the page.
                    <span
                      className="flex h-8 items-center text-hint text-muted-foreground"
                      title={timestamp(data.checkedAt)}
                    >
                      read {relativeTime(data.checkedAt)}
                    </span>
                  }
                />
                <PanelBody className="animate-rise space-y-4 text-body">
                  {data.endOfLife && (
                    <div className="space-y-1">
                      <p className="eyebrow">Release</p>
                      <p className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                        <span className="flex min-w-0 items-center gap-2 font-medium">
                          <EngineGlyph engine={engine} />
                          {data.endOfLife.product} {data.endOfLife.release}
                        </span>
                        <Status
                          tone={
                            data.endOfLife.past
                              ? "danger"
                              : data.endOfLife.daysLeft <= 180
                                ? "warning"
                                : "running"
                          }
                          label={data.endOfLife.past ? "Out of maintenance" : "Maintained"}
                        />
                      </p>
                      <p className="text-xs leading-relaxed text-muted-foreground">
                        {data.endOfLife.past
                          ? `Its maintenance ended on ${calendarDate(data.endOfLife.date)}: it gets no more fixes.`
                          : `Until ${calendarDate(data.endOfLife.date)}, ${plural(data.endOfLife.daysLeft, "day")} from now.`}
                      </p>
                    </div>
                  )}
                  {data.silences.length > 0 && (
                    <div className="space-y-1.5">
                      <p className="eyebrow">Not assessed</p>
                      <ul className="space-y-1.5 text-xs leading-relaxed text-muted-foreground">
                        {data.silences.map((silence) => (
                          <li key={silence} className="wrap-anywhere">
                            {silence}
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                </PanelBody>
              </Panel>
            )}
          </div>
        </>
      )}
    </>
  )
}

/** What a finding is about and which kind of concern it is, at its row's edge. */
function About({ advice }: { advice: DbAdvice }) {
  const about = aboutWords(advice)
  // A name is a literal; "3 tables" is words.
  const named = (advice.targets?.length ?? advice.objects?.length ?? 0) === 1
  return (
    <>
      {about && <span className={cn("text-foreground/80", named && "font-mono")}>{about}</span>}
      {about && <span aria-hidden> · </span>}
      {advice.category}
    </>
  )
}

/**
 * The page of this database a finding points at, as its one action. The
 * advisor links by path; only a path that is a page of this database is
 * followed, and by the page's name rather than the path's text — it came from
 * the server, and a press here must not leave for somewhere it merely
 * spelled.
 */
function linkAction(
  advice: DbAdvice,
  tools: {
    id: number
    title: (section: SectionId) => string | undefined
    open: (section: SectionId, params?: SectionParams) => void
  },
): Finding["action"] {
  const place = advice.link ? databasePlace(advice.link) : null
  if (!place || place.id !== tools.id) return undefined
  const title = tools.title(place.section)
  if (!title) return undefined
  // Performance is several readings: the finding is opened on the one that
  // lists what it is about.
  const kinds = new Set((advice.targets ?? []).map((target) => target.kind))
  const view =
    place.section !== "performance"
      ? undefined
      : kinds.has("slot")
        ? "replication"
        : kinds.has("index")
          ? "indexes"
          : kinds.has("table")
            ? "tables"
            : undefined
  return {
    label: `Open ${title}`,
    onClick: () => tools.open(place.section, view ? { view } : undefined),
  }
}
