"use client"

import { get } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import type { ErrorGroup, ErrorReport } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { upstreamEndpoint } from "@/components/proxy/site-apps"
import { portsHref } from "@/components/proxy/ports-list"
import { FindingList, type Finding } from "@/components/finding-list"
import { Well } from "@/components/panel"

/** nginx's levels as the finding list's three: what stops a request, what warns, the rest. */
function findingLevel(level: string): Finding["level"] {
  if (level === "emerg" || level === "alert" || level === "crit" || level === "error") {
    return level === "error" ? "warning" : "critical"
  }
  if (level === "warn") return "warning"
  return "notice"
}

export function isActionableProxyError(group: Pick<ErrorGroup, "level">) {
  return findingLevel(group.level) !== "notice"
}

/**
 * One group as a finding: the failure in words when nginx's line is one the
 * server recognises, and the pattern it was grouped by when it is not, so an
 * unexplained line is still shown as nginx wrote it.
 */
export function errorFinding(
  group: ErrorGroup,
  context: { site?: string; mayConfigure?: boolean } = {},
): Finding {
  const where = group.upstream ? ` · ${group.upstream}` : ""
  const endpoint = group.upstream ? upstreamEndpoint(group.upstream) : undefined
  const local =
    endpoint &&
    (endpoint.host === "localhost" ||
      endpoint.host.startsWith("127.") ||
      endpoint.host === "[::1]" ||
      endpoint.host === "0.0.0.0")
  const action = context.site
    ? { label: "Inspect site", href: `/proxy/sites/${encodeURIComponent(context.site)}` }
    : local
      ? { label: "Inspect upstream owner", href: portsHref({ q: `port:${endpoint.port}` }) }
      : context.mayConfigure
        ? { label: "Inspect nginx configuration", href: "/proxy/config" }
        : undefined
  return {
    id: `${group.level}:${group.pattern}:${group.upstream ?? ""}`,
    level: findingLevel(group.level),
    title: group.title || group.pattern,
    detail: `${plural(group.count, "time")}, last ${relativeTime(group.last)}${where}`,
    advice: group.advice,
    meta: <span className="numeric">{group.count.toLocaleString()}</span>,
    extra: <Well className="break-all whitespace-pre-wrap">{group.sample}</Well>,
    action,
  }
}

/** What the counts leave out, when anything: a cut read or an ungrouped tail. */
function readNotes(report: ErrorReport): string[] {
  return [
    !report.complete && "Only the log's last 8 MB was read, so each count is a floor.",
    (report.ungrouped ?? 0) > 0 &&
      `${plural(report.ungrouped ?? 0, "line")} of rarer failures are not grouped.`,
    report.note,
  ].filter((note): note is string => Boolean(note))
}

/**
 * The error log over the window, grouped: a site's own log, or nginx's
 * narrowed to its names; with no site, nginx's whole. Read by the page rather
 * than by the list, so the view's tab can carry the count its rows add up to.
 */
export function useErrorReport(site: string | undefined, range: string, enabled = true) {
  return usePoll(
    (signal) => get<ErrorReport>("/proxy/errors", { window: range, site }, signal),
    30_000,
    [site ?? "", range],
    { enabled },
  )
}

/** The failures a report holds that somebody can act on: what the view lists and its tab counts. */
export function actionableCount(report: ErrorReport | undefined): number | undefined {
  if (!report) return undefined
  return report.groups.filter(isActionableProxyError).reduce((n, group) => n + group.count, 0)
}

/**
 * The groups as findings, worst first as the list orders them, with the file
 * they were read from and what the counts leave out on one quiet line under
 * them. A log that was not read has nothing to be clean about, so its reason
 * is said plainly rather than beside a tick.
 */
export function ErrorLog({
  report,
  site,
}: {
  report: ErrorReport
  /** The site the read was narrowed to, which its findings point back at. */
  site?: string
}) {
  const { can } = useAuth()
  const notes = readNotes(report)
  const where = report.scope === "shared" ? `${report.path}, ${site}'s lines` : report.path
  return (
    <div className="animate-rise space-y-3">
      {report.groups.length === 0 && (report.note || !report.exists) ? (
        <p className="text-body text-muted-foreground">
          {report.note ?? "The error log has not been written yet."}
        </p>
      ) : (
        <FindingList
          findings={report.groups
            .filter(isActionableProxyError)
            .map((group) => errorFinding(group, { site, mayConfigure: can("system.admin") }))}
          emptyLabel="nginx logged no errors in this window"
        />
      )}
      <p className="flex min-w-0 flex-wrap gap-x-2 text-hint text-muted-foreground">
        <span className="truncate font-mono" title={report.path}>
          {where}
        </span>
        {report.groups.length > 0 && notes.length > 0 && <span>{notes.join(" ")}</span>}
      </p>
    </div>
  )
}
