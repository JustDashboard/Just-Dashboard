"use client"

import { useViewState } from "@/lib/view-state"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentInsights } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { BarList } from "@/components/bar-list"
import { TileTrend } from "@/components/metrics/sparkline"
import { formatDuration, sentence } from "@/components/deploy/vocabulary"
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

/**
 * How a day's releases ended, in the outcome strip's colours: the cancelled
 * share is its quiet grey — present, so a day of cancelled runs is not drawn
 * as a day of nothing.
 */
const FILL = {
  succeeded: "bg-success/80",
  failed: "bg-destructive",
  cancelled: "bg-muted-foreground/40",
} as const

type Outcome = keyof typeof FILL

// Top to bottom: what shipped at the foot of the bar, where the eye measures
// from, and the red at the top, where it lands.
const STACK: Outcome[] = ["failed", "cancelled", "succeeded"]

// Under an hour in the runs' own "1m 35s", so a median never reads longer
// than every run listed under it — "2m" for 95 seconds did.
function seconds(value: number) {
  if (!value) return "—"
  if (value < 3600) return formatDuration(value)
  if (value < 86400) return `${(value / 3600).toFixed(1)}h`
  return `${(value / 86400).toFixed(1)}d`
}

/** "Aug 25" for a `YYYY-MM-DD` day, read as that calendar day wherever the reader is. */
function dayLabel(date: string) {
  return new Date(`${date}T00:00:00`).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  })
}

/**
 * Delivery figures computed by the server from persisted runs: frequency,
 * success rate, release duration and time to recover from a failure. Nothing
 * here is estimated by the browser; each tile names its own basis so a figure
 * cannot be read as more than it is. The caller hides this for legacy
 * projects, which the engine never scored.
 *
 * Four figures, not five: the failure streak was a tile of its own that sat
 * alone on a row at most widths, and it is a fact about the success rate —
 * its count is that tile's trailing reading now, and when the last failure
 * happened is said beside the chart that draws it. The release time carries
 * the window's shape in its trend; how often the project ships already has
 * its shape in the chart of releases per day under the tiles; the rate
 * fills, and keeps a meter.
 *
 * `onFailureClick` lets a page that lists the runs narrow to the failed ones
 * from a reason's row.
 */
export function Insights({
  projectId,
  onFailureClick,
}: {
  projectId: number
  onFailureClick?: (code: string) => void
}) {
  const [range, setRange] = useViewState<(typeof WINDOWS)[number][0]>("deploy.insights.range", "30")
  const insights = usePoll(
    (signal) => get<DeploymentInsights>(`/deploy/${projectId}/insights`, { days: range }, signal),
    60000,
    [projectId, range],
  )
  const data = insights.data
  const decided = (data?.succeeded ?? 0) + (data?.failed ?? 0)
  const daily = data?.daily ?? []
  const total = (day: DeploymentInsights["daily"][number]) =>
    day.succeeded + day.failed + day.cancelled
  const peak = Math.max(1, ...daily.map(total))
  const topFailure = Math.max(1, ...(data?.topFailures ?? []).map((failure) => failure.count))

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
      <PanelBody className="space-y-6">
        {insights.error && <ErrorState error={insights.error} />}
        {insights.loading && !data && <LoadingRows rows={2} />}
        {data && data.runs === 0 && (
          <EmptyNote>
            No release finished in this window. Figures appear after the first deployment ends.
          </EmptyNote>
        )}
        {data && data.runs > 0 && (
          <>
            <StatGrid columns={4} dense>
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
                meter={decided ? data.successRate * 100 : undefined}
                tone={data.failureStreak > 0 ? "warning" : "default"}
                trailing={
                  data.failureStreak > 0 && (
                    <span className="text-destructive">{data.failureStreak} failed in a row</span>
                  )
                }
                hint={`${data.succeeded} of ${decided} decided release${decided === 1 ? "" : "s"}`}
              />
              {/* No trend of its own: its shape is the Releases per day chart
                  under the tiles, and a day's zero or one drew as noise. */}
              <StatTile
                label="Deploys per week"
                value={<NumberTicker value={data.deploysPerWeek} decimalPlaces={1} />}
                hint={`${data.succeeded} successful over ${data.windowDays} days`}
              />
              <StatTile
                label="Median release"
                value={seconds(data.medianDurationSeconds)}
                trend={
                  <TileTrend
                    // Only the days something shipped: a day with no release
                    // has no duration, and drawing it at zero would say the
                    // releases got faster.
                    values={daily
                      .map((day) => day.medianDurationSeconds)
                      .filter((value) => value > 0)}
                    label={`Median release time per day over the last ${data.windowDays} days`}
                    color="var(--chart-2)"
                  />
                }
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
            </StatGrid>

            <div
              className={cn(
                "grid min-w-0 gap-x-10 gap-y-6",
                data.topFailures.length > 0 && "lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]",
              )}
            >
              <div className="min-w-0 space-y-2">
                <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
                  <p className="eyebrow">Releases per day</p>
                  <p className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
                    {(Object.keys(FILL) as Outcome[]).map((outcome) => (
                      <span key={outcome} className="inline-flex items-center gap-1.5">
                        <span aria-hidden className={cn("size-2 rounded-[2px]", FILL[outcome])} />
                        {outcome}
                      </span>
                    ))}
                    {data.lastFailureAt && (
                      <span>last failure {relativeTime(data.lastFailureAt)}</span>
                    )}
                  </p>
                </div>
                <ol
                  // Keyed by the window so a new range arrives rather than
                  // the old bars stretching into it.
                  key={range}
                  className="flex h-20 animate-rise items-end gap-px border-b border-hairline"
                  aria-label="Releases per day"
                  data-testid="insights-daily"
                >
                  {daily.map((day, index) => {
                    const count = total(day)
                    return (
                      <li
                        // Dates repeat when a window is longer than the recorded
                        // history; the position is what makes a bar unique.
                        key={`${day.date}-${index}`}
                        // Full height, or the bar's percentage has nothing to
                        // resolve against and every day draws at zero.
                        className="flex h-full min-w-0 flex-1 flex-col items-center justify-end"
                        title={`${day.date}: ${day.succeeded} succeeded, ${day.failed} failed${day.cancelled ? `, ${day.cancelled} cancelled` : ""}`}
                      >
                        {count ? (
                          <span
                            // A thin bar centred in its day, so a week is
                            // eight marks rather than eight slabs.
                            className="flex w-full max-w-3 flex-col overflow-hidden rounded-t-sm transition-[height] duration-500 ease-out"
                            style={{ height: `${Math.max(8, Math.round((count / peak) * 100))}%` }}
                          >
                            {STACK.map((outcome) =>
                              day[outcome] ? (
                                <span
                                  key={outcome}
                                  className={cn(
                                    "block w-full shrink-0 transition-[height] duration-500 ease-out",
                                    FILL[outcome],
                                  )}
                                  style={{ height: `${(day[outcome] / count) * 100}%` }}
                                />
                              ) : null,
                            )}
                          </span>
                        ) : (
                          // Nothing ran: a tick on the baseline, never a
                          // faint success.
                          <span className="block h-0.5 w-full max-w-3 bg-meter-track" />
                        )}
                      </li>
                    )
                  })}
                </ol>
                {daily.length > 0 && (
                  <p className="numeric flex justify-between gap-3 text-micro text-muted-foreground">
                    <span>{dayLabel(daily[0].date)}</span>
                    <span>today</span>
                  </p>
                )}
              </div>

              {data.topFailures.length > 0 && (
                <div className="min-w-0 space-y-2">
                  <p className="eyebrow">Why releases failed</p>
                  <BarList
                    items={data.topFailures.map((failure) => ({
                      key: failure.code,
                      label: sentence(failure.code),
                      mono: false,
                      value: `×${failure.count}`,
                      share: failure.count / topFailure,
                      signal: 1,
                      tone: "danger",
                      title: onFailureClick ? "Show the failed deployments" : undefined,
                      onClick: onFailureClick && (() => onFailureClick(failure.code)),
                    }))}
                  />
                </div>
              )}
            </div>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
