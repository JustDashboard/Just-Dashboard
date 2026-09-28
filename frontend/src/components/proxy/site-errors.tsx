"use client"

import { get } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import type { ErrorGroup, ErrorReport } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { FindingList, type Finding } from "@/components/finding-list"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ErrorState, LoadingPanel } from "@/components/state"

/** nginx's levels as the finding list's three: what stops a request, what warns, the rest. */
function findingLevel(level: string): Finding["level"] {
  if (level === "emerg" || level === "alert" || level === "crit" || level === "error") {
    return level === "error" ? "warning" : "critical"
  }
  return "notice"
}

/**
 * One group as a finding: the failure in words when nginx's line is one the
 * server recognises, and the pattern it was grouped by when it is not, so an
 * unexplained line is still shown as nginx wrote it.
 */
export function errorFinding(group: ErrorGroup): Finding {
  const where = group.upstream ? ` · ${group.upstream}` : ""
  return {
    id: `${group.level}:${group.pattern}:${group.upstream ?? ""}`,
    level: findingLevel(group.level),
    title: group.title || group.pattern,
    detail: `${plural(group.count, "time")}, last ${relativeTime(group.last)}${where}`,
    advice: group.advice,
    meta: <span className="numeric">{group.count.toLocaleString()}</span>,
    extra: <Well className="break-all whitespace-pre-wrap">{group.sample}</Well>,
  }
}

/** Where the groups were read from: a shared log says whose lines were kept. */
function scopeLabel(report: ErrorReport): string {
  return report.scope === "shared" ? `${report.path}, this site's lines` : report.path
}

/** What the counts leave out, when anything: a cut read or an ungrouped tail. */
function ReadNotes({ report }: { report: ErrorReport }) {
  const notes = [
    !report.complete && "Only the log's last 8 MB was read, so each count is a floor.",
    (report.ungrouped ?? 0) > 0 &&
      `${plural(report.ungrouped ?? 0, "line")} of rarer failures are not grouped.`,
    report.note,
  ].filter(Boolean)
  if (notes.length === 0) return null
  return <p className="text-hint text-muted-foreground">{notes.join(" ")}</p>
}

/**
 * The error log over the window, grouped and explained. A site reads its own
 * log, or nginx's narrowed to its names; with no site it reads nginx's whole.
 */
export function SiteErrors({ site, range }: { site?: string; range: string }) {
  const errors = usePoll(
    (signal) => get<ErrorReport>("/proxy/errors", { window: range, site }, signal),
    30_000,
    [site ?? "", range],
  )
  const report = errors.error ? undefined : errors.data

  return (
    <Panel plain>
      <PanelHeader
        title="Error log"
        actions={
          report && (
            <span
              className="truncate font-mono text-hint text-muted-foreground"
              title={report.path}
            >
              {scopeLabel(report)}
            </span>
          )
        }
      />
      <PanelBody>
        {errors.error ? (
          <ErrorState error={errors.error} onRetry={errors.refresh} />
        ) : !report ? (
          <LoadingPanel plain rows={4} />
        ) : (
          <div className="animate-rise space-y-3">
            {/* A log that was not read has nothing to be clean about, so its
                reason is said plainly rather than beside a tick. */}
            {report.groups.length === 0 && (report.note || !report.exists) ? (
              <p className="text-body text-muted-foreground">
                {report.note ?? "The error log has not been written yet."}
              </p>
            ) : (
              <FindingList
                findings={report.groups.map(errorFinding)}
                emptyLabel="nginx logged no errors in this window"
              />
            )}
            {report.groups.length > 0 && <ReadNotes report={report} />}
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}
