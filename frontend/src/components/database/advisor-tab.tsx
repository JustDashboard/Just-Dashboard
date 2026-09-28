"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { CodeBracket, Copy, Shield } from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural, relativeTime } from "@/lib/format"
import type { DbAdvice, DbAdviseReport, DbConnection } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChipStrip, ChipCount, FilterChip } from "@/components/tabs"
import { FindingList, type Finding } from "@/components/finding-list"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Well } from "@/components/panel"
import { Panel, PanelHeader } from "@/components/panel"

type Category = "all" | DbAdvice["category"]

function stamp() {
  return new Date().toISOString()
}

const CATEGORY_WORD: Record<DbAdvice["category"], string> = {
  performance: "Performance",
  schema: "Schema",
  security: "Security",
  maintenance: "Maintenance",
}

/**
 * The advisor: what the engine's own catalogue says is wrong, as findings
 * with the fix attached.
 *
 * The findings as the same list every verdict in the product is drawn as
 * (`FindingList`), each carrying the objects it is about and, where one
 * statement fixes it, that statement with a copy button and a link to run it
 * in the console. The four readings that stood over the list as tiles — how
 * many findings, how many critical, what was checked, when — are the list's
 * own header now (§15 pass 2): a count is loudest as the title of the thing
 * it counts. Nothing here executes on its own: the advisor points, the
 * operator decides.
 */
export function AdvisorTab({
  conn,
  schema,
  onQuery,
}: {
  conn: DbConnection
  schema: string
  /** Open the console with a statement in it. */
  onQuery: (sql: string) => void
}) {
  const report = usePoll(
    (signal) => get<DbAdviseReport>(`/databases/${conn.id}/advisor`, { schema }, signal),
    120_000,
    [conn.id, schema],
  )
  const [category, setCategory] = useSessionState<Category>(
    `databases.${conn.id}.advisor.category`,
    "all",
  )
  // When the report in hand was fetched: stamped as it lands, so "checked 4
  // minutes ago" is about the findings on screen rather than the render.
  const [checkedAt, setCheckedAt] = useState(stamp)
  const lastReport = useRef(report.data)
  useEffect(() => {
    if (report.data !== lastReport.current) {
      lastReport.current = report.data
      setCheckedAt(stamp())
    }
  }, [report.data])

  const counts = useMemo(() => {
    const list = report.data?.findings ?? []
    const by: Record<Category, number> = {
      all: list.length,
      performance: 0,
      schema: 0,
      security: 0,
      maintenance: 0,
    }
    for (const f of list) by[f.category]++
    return by
  }, [report.data])

  const findings = useMemo<Finding[]>(
    () =>
      (report.data?.findings ?? [])
        .filter((f) => category === "all" || f.category === category)
        .map((f) => ({
          id: f.id,
          level: f.level,
          title: f.title,
          detail: f.detail,
          advice: f.advice,
          meta: CATEGORY_WORD[f.category],
          extra: (
            <>
              {f.objects && f.objects.length > 0 && (
                <ul className="flex flex-wrap gap-x-3 gap-y-1 font-mono text-hint text-foreground/80">
                  {f.objects.slice(0, 40).map((o) => (
                    <li key={o}>{o}</li>
                  ))}
                  {f.objects.length > 40 && (
                    <li className="text-muted-foreground">and {f.objects.length - 40} more</li>
                  )}
                </ul>
              )}
              {f.sql && (
                <div className="space-y-1.5">
                  <Well className="max-h-40 text-hint whitespace-pre-wrap">{f.sql}</Well>
                  <div className="flex flex-wrap gap-1.5">
                    <Button size="xs" variant="outline" onClick={() => onQuery(f.sql!)}>
                      <CodeBracket className="size-3" />
                      Open in the console
                    </Button>
                    <Button
                      size="xs"
                      variant="ghost"
                      onClick={() => void copyText(f.sql!, "Statement copied")}
                    >
                      <Copy className="size-3" />
                      Copy
                    </Button>
                  </div>
                </div>
              )}
            </>
          ),
        })),
    [report.data, category, onQuery],
  )

  if (report.loading && !report.data) return <LoadingPanel />
  if (report.error && !report.data) return <ErrorState error={report.error} />
  if (!report.data) return null

  const critical = report.data.findings.filter((f) => f.level === "critical").length
  const warnings = report.data.findings.filter((f) => f.level === "warning").length

  return (
    <div className="flex min-w-0 animate-rise flex-col gap-6">
      <Panel plain>
        <PanelHeader
          title={
            report.data.findings.length === 0
              ? "Nothing to fix"
              : `${plural(report.data.findings.length, "finding")}${
                  critical > 0 ? ` · ${critical} critical` : ""
                }${warnings > 0 ? ` · ${plural(warnings, "warning")}` : ""}`
          }
          eyebrow={
            <span className="flex flex-wrap items-center gap-x-1.5">
              <span>
                {plural(report.data.tablesChecked, "table")} checked
                {schema ? ` in ${schema}` : ""} ·{" "}
                {report.data.engineChecks
                  ? "statistics, indexes and settings read"
                  : "structure only, this engine keeps no statistics"}{" "}
                · {relativeTime(checkedAt)}
              </span>
              <Button
                size="xs"
                variant="ghost"
                className="-my-1 h-5 px-1.5"
                onClick={report.refresh}
                pending={report.loading}
              >
                Check again
              </Button>
            </span>
          }
          actions={
            <ChipStrip>
              {(["all", "performance", "schema", "security", "maintenance"] as Category[]).map(
                (c) =>
                  c === "all" || counts[c] > 0 ? (
                    <FilterChip key={c} selected={category === c} onClick={() => setCategory(c)}>
                      {c === "all" ? "All" : CATEGORY_WORD[c]}
                      <ChipCount>{counts[c]}</ChipCount>
                    </FilterChip>
                  ) : null,
              )}
            </ChipStrip>
          }
        />
        <FindingList
          findings={findings}
          emptyLabel={
            report.data.findings.length === 0
              ? `Nothing found across ${plural(report.data.tablesChecked, "table")}`
              : "Nothing in this category"
          }
        />
      </Panel>
      <p className="sr-only">
        <Shield className="inline size-3" /> Advisor
      </p>
    </div>
  )
}
