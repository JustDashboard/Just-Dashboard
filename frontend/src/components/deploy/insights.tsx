"use client"

import { useViewState } from "@/lib/view-state"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentInsights } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { humanize } from "@/components/deploy/vocabulary"
import { NumberTicker } from "@/components/ui/number-ticker"
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
 * success rate, release duration and time to recover from a failure. Nothing
 * here is estimated by the browser; each tile names its own basis so a figure
 * cannot be read as more than it is. The caller hides this for legacy
 * projects, which the engine never scored.
 */
export function Insights({ projectId }: { projectId: number }) {
  const [range, setRange] = useViewState<(typeof WINDOWS)[number][0]>("deploy.insights.range", "30")
  const insights = usePoll(
    (signal) => get<DeploymentInsights>(`/deploy/${projectId}/insights`, { days: range }, signal),
    60000,
    [projectId, range],
  )
  const data = insights.data
  const decided = (data?.succeeded ?? 0) + (data?.failed ?? 0)
  const peak = Math.max(1, ...(data?.daily ?? []).map((day) => day.succeeded + day.failed))

  return (
    <Panel plain>
      <PanelHeader
        title="Delivery"
        actions={
          <Select value={range} onValueChange={(value) => setRange(value as typeof range)}>
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
            <StatGrid columns={5}>
              <StatTile
                label="Success rate"
                value={
                  decided ? (
                    <>
                      <NumberTicker value={Math.round(data.successRate * 100)} />%
                    </>
                  ) : (
                    "—"
                  )
                }
                hint={`${data.succeeded} of ${decided} decided release${decided === 1 ? "" : "s"}`}
                tone={data.failureStreak > 0 ? "warning" : "default"}
              />
              <StatTile
                label="Deploys per week"
                value={<NumberTicker value={data.deploysPerWeek} decimalPlaces={1} />}
                hint={`${data.succeeded} successful over ${data.windowDays} days`}
              />
              <StatTile
                label="Median release"
                value={seconds(data.medianDurationSeconds)}
                hint={`p95 ${seconds(data.p95DurationSeconds)} · claim to finish`}
              />
              <StatTile
                label="Recovery time"
                value={data.recoveredFailures ? seconds(data.meanRecoverySeconds) : "—"}
                hint={
                  data.recoveredFailures
                    ? `mean over ${data.recoveredFailures} recovered failure${data.recoveredFailures === 1 ? "" : "s"}`
                    : data.failed
                      ? "no failure followed by a success yet"
                      : "no failures in this window"
                }
              />
              <StatTile
                label="Failure streak"
                value={<NumberTicker value={data.failureStreak} />}
                hint={
                  data.lastFailureAt
                    ? `last failure ${relativeTime(data.lastFailureAt)}`
                    : "no failures in this window"
                }
                tone={data.failureStreak > 0 ? "danger" : "default"}
              />
            </StatGrid>
            <div>
              <p className="mb-1.5 text-hint text-muted-foreground">
                Releases per day · green succeeded, red failed
              </p>
              <ol
                className="flex h-16 items-end gap-px"
                aria-label="Releases per day"
                data-testid="insights-daily"
              >
                {data.daily.map((day, index) => {
                  const total = day.succeeded + day.failed
                  const height = total ? Math.max(8, Math.round((total / peak) * 100)) : 2
                  const failedShare = total ? (day.failed / total) * 100 : 0
                  return (
                    <li
                      // Dates repeat when a window is longer than the recorded
                      // history; the position is what makes a bar unique.
                      key={`${day.date}-${index}`}
                      // Full height, or the bar's percentage has nothing to
                      // resolve against and every day draws at zero.
                      className="flex h-full min-w-0 flex-1 flex-col justify-end"
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
              <p className="flex flex-wrap gap-x-4 gap-y-1 text-hint text-muted-foreground">
                <span>Why releases failed:</span>
                {data.topFailures.map((failure) => (
                  <span key={failure.code}>
                    <span className="font-medium text-foreground">{humanize(failure.code)}</span> ×
                    {failure.count}
                  </span>
                ))}
              </p>
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
