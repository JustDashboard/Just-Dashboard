"use client"

import { useCallback, useMemo } from "react"
import { RefreshClockwise } from "@/components/icons"
import { calendarDate, plural, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import { notify } from "@/lib/toast"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { FindingList, type Finding } from "@/components/finding-list"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatButton, StatGrid, StatTile } from "@/components/stat-tile"
import { LoadingPanel, Notice } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { CouldNotRead } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import type { SectionId, SectionParams } from "@/components/database/engine"
import { SectionFrame } from "@/components/database/kit"
import { FindingBody } from "@/components/database/ops/advisor-finding"
import {
  CATEGORIES,
  LEVELS,
  aboutWords,
  countCategories,
  countLevels,
  findingKeys,
  isCategory,
  isLevel,
  keptFindings,
  type DbAdvice,
  type DbAdviseReport,
} from "@/components/database/ops/advisor-fix"
import { runScript } from "@/components/database/ops/performance-api"
import { useMaintenance } from "@/components/database/ops/performance-maintenance"
import { ServerDown, Stale, isDown } from "@/components/database/ops/performance-parts"
import { useDatabase } from "@/components/database/shell/database-context"
import { databasePlace } from "@/components/database/shell/routes"

const LEVEL_TONE = { critical: "danger", warning: "warning", notice: "default" } as const

/**
 * What is worth fixing on this database, each finding with the object it is
 * about, why it matters, and the statement that fixes it.
 *
 * The page opens on how many findings there are of each severity — the
 * figures are the filters, so pressing "Warnings" is the list of them — and
 * then on the categories with their counts. A finding is one line until it is
 * opened; opened, it names its objects and shows its fix as the statement
 * itself. The statement can be taken to the Query page, and it is applied
 * from here only where the server marks it safe to (`advisor-fix.ts`).
 *
 * The report is never silent about its own limits: what it could not assess
 * is listed under the findings, in the server's sentences, and a schema too
 * large to walk in full says how far the walk got.
 *
 * The report is the whole database's, whatever schema the reader last had
 * open elsewhere: nothing here is narrowed by a place the page does not show.
 */
export function Advisor() {
  const { status } = useDatabase()
  return (
    <SectionFrame section="advisor">
      {isDown(status.state) ? <ServerDown what="The advisor's report" /> : <Report />}
    </SectionFrame>
  )
}

function Report() {
  const { id, engine, param, select, goto } = useDatabase()
  const { confirm, dialog } = useConfirm()
  const report = usePoll(
    (signal) =>
      read<DbAdviseReport>(
        `/databases/${id}/advisor`,
        (answer) => Array.isArray(answer.findings),
        undefined,
        signal,
      ),
    300_000,
    [id],
  )
  const refresh = report.refresh
  const maintenance = useMaintenance(refresh)

  const askedLevel = param("level")
  const level = isLevel(askedLevel) ? askedLevel : null
  const askedCategory = param("category")
  const category = isCategory(askedCategory) ? askedCategory : null

  const data = report.data
  const levels = useMemo(() => countLevels(data?.findings ?? []), [data])
  // The chips count what the severity leaves, so a chip never promises rows
  // the tiles have filtered away.
  const categories = useMemo(
    () => countCategories(keptFindings(data?.findings ?? [], { level })),
    [data, level],
  )

  const applyStatements = useCallback(
    async (sql: string) => {
      const result = await runScript(id, sql)
      const ran = result.statements.filter((step) => step.status === "ok").length
      if (result.failed >= 0) {
        const step = result.statements[result.failed]
        notify.error(
          ran > 0 ? `The fix stopped after ${plural(ran, "statement")}` : "The fix was not applied",
          step?.error ?? "The server refused the statement.",
        )
      } else {
        notify.success(`Applied: ${plural(ran, "statement")} ran`, {
          description: "The report is being read again.",
        })
      }
      refresh()
    },
    [id, refresh],
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
            extra: (
              <FindingBody
                advice={advice}
                maintenance={maintenance}
                confirm={confirm}
                onApplyStatements={applyStatements}
              />
            ),
            action: linkAction(advice, {
              id,
              title: (section) => engine.section(section)?.title,
              open: goto,
            }),
          },
        ]
      : [],
  )

  const asked = param("finding")

  return (
    <>
      {dialog}
      {maintenance.dialogs}
      {!data ? (
        report.error ? (
          <CouldNotRead what="the advisor's report" error={report.error} onRetry={refresh} />
        ) : (
          <>
            <StatGrid columns={4} dense aria-hidden>
              {Array.from({ length: 4 }, (_, index) => (
                <div key={index} className="space-y-2.5 px-5 py-4">
                  <Skeleton className="h-2.5 w-16" />
                  <Skeleton className="h-7 w-12" />
                  <Skeleton className="h-3 w-28" />
                </div>
              ))}
            </StatGrid>
            <LoadingPanel plain rows={5} />
          </>
        )
      ) : (
        <>
          <StatGrid columns={4} dense className="animate-rise">
            {LEVELS.map((entry) => {
              const count = levels[entry.id]
              return (
                <StatButton
                  key={entry.id}
                  label={`Show only the ${count === 1 ? entry.one : `${entry.one}s`}`}
                  pressed={level === entry.id}
                  onClick={() => select({ level: level === entry.id ? null : entry.id })}
                >
                  <StatTile
                    label={entry.label}
                    value={count.toLocaleString()}
                    tone={count > 0 ? LEVEL_TONE[entry.id] : "default"}
                    hint={LEVEL_HINT[entry.id]}
                    className="h-full transition-colors group-hover:bg-row-hover"
                  />
                </StatButton>
              )
            })}
            <StatTile
              label="Checked"
              value={data.tablesChecked.toLocaleString()}
              trailing={data.tablesChecked === 1 ? engine.nouns.object : engine.nouns.objects}
              hint={
                <span title={timestamp(data.checkedAt)}>
                  {data.engineChecks ? "and the server's settings" : "structure only"} ·{" "}
                  {relativeTime(data.checkedAt)}
                </span>
              }
            />
          </StatGrid>

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

          <Panel plain aria-label="Findings">
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
                      onClick={() => select({ category: category === entry.id ? null : entry.id })}
                    >
                      {entry.label}
                      <ChipCount>{categories[entry.id].toLocaleString()}</ChipCount>
                    </FilterChip>
                  ))}
                </ChipStrip>
              )}
              <FindingList
                // Keyed on the filter: the list's open rows are its own, and
                // a row the filter brings back starts closed.
                key={`${level}:${category}`}
                findings={findings}
                defaultOpen={asked ? [asked] : undefined}
                anchor={(key) => `finding-${key}`}
                emptyLabel={
                  data.findings.length === 0
                    ? `The advisor checked ${plural(data.tablesChecked, engine.nouns.object)}${data.engineChecks ? " and the server's settings" : ""}, and found nothing to fix`
                    : "No finding of that kind"
                }
              />
            </PanelBody>
          </Panel>

          {(data.silences.length > 0 || data.endOfLife) && (
            <Panel plain aria-label="What the report rests on">
              <PanelHeader title="What the report rests on" />
              <PanelBody className="space-y-3 text-body">
                {data.endOfLife && (
                  <p>
                    <span className="font-medium">
                      {data.endOfLife.product} {data.endOfLife.release}
                    </span>{" "}
                    <span className="text-muted-foreground">
                      {data.endOfLife.past
                        ? `has been out of maintenance since ${calendarDate(data.endOfLife.date)}.`
                        : `is maintained until ${calendarDate(data.endOfLife.date)} — ${plural(data.endOfLife.daysLeft, "day")} from now.`}
                    </span>
                  </p>
                )}
                {data.silences.length > 0 && (
                  <div className="space-y-1.5">
                    <p className="eyebrow">Not assessed</p>
                    <ul className="space-y-1 text-xs leading-relaxed text-muted-foreground">
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
        </>
      )}
    </>
  )
}

const LEVEL_HINT = {
  critical: "to deal with now",
  warning: "worth fixing",
  notice: "worth knowing",
} as const

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
