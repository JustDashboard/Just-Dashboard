"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentInsights } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { humanize } from "@/components/deploy/deployment-ui"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const WINDOWS = [
  ["7", "Last 7 days"],
  ["30", "Last 30 days"],
  ["90", "Last 90 days"],
] as const

function seconds(value: number) {
  if (!value) return "—"
  if (value < 60) return `${value}s`
  if (value < 3600) return `${Math.round(value / 60)}m`
  if (value < 86400) return `${(value / 3600).toFixed(1)}h`
  return `${(value / 86400).toFixed(1)}d`
}

/**
 * Delivery figures computed by the server from persisted runs: frequency,
 * success rate, release duration and time to recover from a failure. The
 * numbers describe what the engine did; nothing here is estimated by the
 * browser. Each tile names its own basis so a figure cannot be read as more
 * than it is.
 */
export function DeploymentInsightsPanel({ projectID }: { projectID: number }) {
  const [window, setWindow] = useState<(typeof WINDOWS)[number][0]>("30")
  const insights = usePoll(
    (signal) => get<DeploymentInsights>(`/deploy/${projectID}/insights`, { days: window }, signal),
    60000,
    [projectID, window],
  )
  const data = insights.data
  const decided = (data?.succeeded ?? 0) + (data?.failed ?? 0)
  const peak = Math.max(1, ...(data?.daily ?? []).map((day) => day.succeeded + day.failed))
  return (
    <Panel>
      <PanelHeader
        title="Delivery"
        actions={
          <Select value={window} onValueChange={(value) => setWindow(value as typeof window)}>
            <SelectTrigger size="sm" className="w-40" aria-label="Insights window">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {WINDOWS.map(([value, label]) => (
                <SelectItem key={value} value={value}>
                  {label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        }
      />
      <PanelBody className="space-y-4">
        {insights.error && <ErrorState error={insights.error} />}
        {insights.loading && !data && <LoadingRows rows={2} />}
        {data && data.runs === 0 && (
          <EmptyNote>
            No release finished in this window. Figures appear after the first deployment ends.
          </EmptyNote>
        )}
        {data && data.runs > 0 && (
          <>
            <dl className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
              <Figure
                label="Success rate"
                value={decided ? `${Math.round(data.successRate * 100)}%` : "—"}
                basis={`${data.succeeded} of ${decided} decided release${decided === 1 ? "" : "s"}`}
                tone={data.failureStreak > 0 ? "warning" : undefined}
              />
              <Figure
                label="Deploys per week"
                value={data.deploysPerWeek.toFixed(1)}
                basis={`${data.succeeded} successful over ${data.windowDays} days`}
              />
              <Figure
                label="Median release"
                value={seconds(data.medianDurationSeconds)}
                basis={`p95 ${seconds(data.p95DurationSeconds)} · claim to finish`}
              />
              <Figure
                label="Recovery time"
                value={data.recoveredFailures ? seconds(data.meanRecoverySeconds) : "—"}
                basis={
                  data.recoveredFailures
                    ? `mean over ${data.recoveredFailures} recovered failure${data.recoveredFailures === 1 ? "" : "s"}`
                    : data.failed
                      ? "no failure followed by a success yet"
                      : "no failures in this window"
                }
              />
              <Figure
                label="Failure streak"
                value={String(data.failureStreak)}
                basis={
                  data.lastFailureAt
                    ? `last failure ${relativeTime(data.lastFailureAt)}`
                    : "no failures in this window"
                }
                tone={data.failureStreak > 0 ? "danger" : undefined}
              />
            </dl>
            <div>
              <p className="mb-1.5 text-hint text-muted-foreground">
                Releases per day · green succeeded, red failed
              </p>
              <ol
                className="flex h-16 items-end gap-px"
                aria-label="Releases per day"
                data-testid="insights-daily"
              >
                {data.daily.map((day) => {
                  const total = day.succeeded + day.failed
                  const height = total ? Math.max(8, Math.round((total / peak) * 100)) : 2
                  const failedShare = total ? (day.failed / total) * 100 : 0
                  return (
                    <li
                      key={day.date}
                      className="flex min-w-0 flex-1 flex-col justify-end"
                      title={`${day.date}: ${day.succeeded} succeeded, ${day.failed} failed${day.cancelled ? `, ${day.cancelled} cancelled` : ""}`}
                    >
                      <span
                        className="block w-full overflow-hidden rounded-sm bg-success/70"
                        style={{ height: `${height}%`, opacity: total ? 1 : 0.35 }}
                      >
                        <span
                          className="block w-full bg-destructive/80"
                          style={{ height: `${failedShare}%` }}
                        />
                      </span>
                    </li>
                  )
                })}
              </ol>
            </div>
            {data.topFailures.length > 0 && (
              <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                <span>Why releases failed:</span>
                {data.topFailures.map((failure) => (
                  <span key={failure.code}>
                    <span className="font-medium text-foreground">{humanize(failure.code)}</span> ×
                    {failure.count}
                  </span>
                ))}
              </div>
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

function Figure({
  label,
  value,
  basis,
  tone,
}: {
  label: string
  value: string
  basis: string
  tone?: "warning" | "danger"
}) {
  return (
    <div className="min-w-0 rounded-md border border-hairline p-3">
      <dt className="text-hint text-muted-foreground">{label}</dt>
      <dd
        className={`numeric text-xl font-semibold tabular-nums ${
          tone === "danger" ? "text-destructive" : tone === "warning" ? "text-warning" : ""
        }`}
      >
        {value}
      </dd>
      <dd className="text-hint text-muted-foreground">{basis}</dd>
    </div>
  )
}
