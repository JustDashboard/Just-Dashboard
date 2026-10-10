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
import { formatDuration } from "@/components/deploy/vocabulary"
import { causeTitle } from "@/components/deploy/failure-cause"
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
 * fills, and keeps a meter. On the Overview, recent runs sit between these
 * readings and the historical charts, so its shorter tiles leave room for
 * the records that explain them.
 *
 * `onFailureClick` lets a page that lists the runs narrow to the failed ones
 * from a reason's row.
 *
 * History puts each pair of readings over the chart it explains, rather than
 * in a second row of tiles above the deployment records (§15 pass 2): success
 * and frequency over the releases per day, duration and recovery over the
 * release time per day. The two charts share their days, so the day a release
 * failed and the day one ran long are found at the same place in each. Why
 * the releases failed runs under both, since it explains the first chart's
 * red and the second's recovery.
 */
export function Insights({
  projectId,
  onFailureClick,
  children,
  presentation = "tiles",
}: {
  projectId: number
  onFailureClick?: (code: string) => void
  /** Overview records placed between the delivery figures and their history. */
  children?: React.ReactNode
  presentation?: "tiles" | "history"
}) {
  const history = presentation === "history"
  const [range, setRange] = useViewState<(typeof WINDOWS)[number][0]>("deploy.insights.range", "30")
  const insights = usePoll(
    (signal) => get<DeploymentInsights>(`/deploy/${projectId}/insights`, { days: range }, signal),
    60000,
    [projectId, range],
  )
  const data = insights.data
  const decided = (data?.succeeded ?? 0) + (data?.failed ?? 0)
  const daily = data?.daily ?? []
  const durationValues = daily.map((day) => day.medianDurationSeconds).filter((value) => value > 0)
  // The window's median is drawn across the days, so it has to be on their scale.
  const slowest = Math.max(data?.medianDurationSeconds ?? 0, ...durationValues)
  const total = (day: DeploymentInsights["daily"][number]) =>
    day.succeeded + day.failed + day.cancelled
  const peak = Math.max(1, ...daily.map(total))
  const reading = "space-y-1"
  const figure = "numeric text-2xl font-semibold tracking-tight"
  const axis = daily.length > 0 && (
    <p className="numeric flex justify-between gap-3 text-micro text-muted-foreground">
      <span>{dayLabel(daily[0].date)}</span>
      <span>today</span>
    </p>
  )
  // One cause has nothing to be compared with, so it is a line and no bar.
  const OnlyFailure = onFailureClick ? "button" : "p"
  const failureColumns = history ? "grid gap-x-10 sm:grid-cols-2 xl:grid-cols-3" : undefined

  const failures = data && data.topFailures.length > 0 && (
    <div className="space-y-2">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h3 className="text-title font-medium">Why releases failed</h3>
        <span className="text-hint text-muted-foreground">
          {data.failed} failed release{data.failed === 1 ? "" : "s"}
          {data.lastFailureAt && ` · last ${relativeTime(data.lastFailureAt)}`}
        </span>
      </div>
      {data.topFailures.length === 1 ? (
        <div className={cn("-ml-2", failureColumns)}>
          <OnlyFailure
            type={onFailureClick ? "button" : undefined}
            onClick={onFailureClick && (() => onFailureClick(data.topFailures[0].code))}
            title={onFailureClick ? "Show the failed deployments" : undefined}
            aria-label={onFailureClick ? "Show the failed deployments" : undefined}
            className={cn(
              "flex min-w-0 items-baseline gap-2 rounded-sm px-2 py-1.5 text-left text-body",
              onFailureClick && "focus-ring-inset transition-colors hover:bg-row-hover",
            )}
          >
            <span className="truncate font-medium">{causeTitle(data.topFailures[0].code)}</span>
            <span className="numeric shrink-0 text-hint text-destructive">
              ×{data.topFailures[0].count}
            </span>
          </OnlyFailure>
        </div>
      ) : (
        <BarList
          className={cn("-ml-2", failureColumns)}
          items={data.topFailures.map((failure) => ({
            key: failure.code,
            label: causeTitle(failure.code),
            mono: false,
            value: `×${failure.count}`,
            // Of every failed release, not of the commonest cause: the bars
            // then read as shares, whichever columns they land in.
            share: failure.count / data.failed,
            signal: 1,
            tone: "danger",
            title: onFailureClick ? "Show the failed deployments" : undefined,
            onClick: onFailureClick && (() => onFailureClick(failure.code)),
          }))}
        />
      )}
    </div>
  )

  const charts = data && data.runs > 0 && (
    <div
      className={cn(
        "grid min-w-0 gap-x-10",
        history ? "gap-y-8" : "gap-y-6",
        (history || failures) &&
          (children
            ? "2xl:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]"
            : "lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]"),
      )}
    >
      <div className="min-w-0 space-y-2">
        {history && (
          <dl className="mb-5 grid grid-cols-2 gap-x-8">
            <div className={reading}>
              <dt className="text-hint text-muted-foreground">Success rate</dt>
              <dd className={cn(figure, data.failureStreak > 0 && "text-warning")}>
                {decided ? `${Math.round(data.successRate * 100)}%` : "—"}
              </dd>
              <dd className="text-hint text-muted-foreground">
                <span>
                  {data.succeeded} of {decided} decided release{decided === 1 ? "" : "s"}
                </span>
                {data.failureStreak > 0 && (
                  <span className="ml-2 text-destructive">
                    {data.failureStreak} failed in a row
                  </span>
                )}
              </dd>
            </div>
            <div className={reading}>
              <dt className="text-hint text-muted-foreground">Deploys per week</dt>
              <dd className={figure}>{data.deploysPerWeek.toFixed(1)}</dd>
              <dd className="text-hint text-muted-foreground">
                {data.succeeded} successful over {data.windowDays} days
              </dd>
            </div>
          </dl>
        )}
        <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
          <h3 className="text-title font-medium">Releases per day</h3>
          <p className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
            {(Object.keys(FILL) as Outcome[]).map((outcome) => (
              <span key={outcome} className="inline-flex items-center gap-1.5">
                <span aria-hidden className={cn("size-2 rounded-[2px]", FILL[outcome])} />
                {outcome}
              </span>
            ))}
          </p>
        </div>
        <ol
          // Keyed by the window so a new range arrives rather than
          // the old bars stretching into it.
          key={range}
          className={cn(
            "flex animate-rise items-end gap-px border-b border-hairline",
            history ? "h-28" : "h-20",
          )}
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
        {axis}
      </div>

      {history && (
        <div className="min-w-0 space-y-2">
          <dl className="mb-5 grid grid-cols-2 gap-x-8">
            <div className={reading}>
              <dt className="text-hint text-muted-foreground">Median release</dt>
              <dd className={figure}>{seconds(data.medianDurationSeconds)}</dd>
              <dd className="text-hint text-muted-foreground">
                {data.medianDurationSeconds
                  ? `p95 ${seconds(data.p95DurationSeconds)} · claim to finish`
                  : "no release succeeded in this window"}
              </dd>
            </div>
            <div className={reading}>
              <dt className="text-hint text-muted-foreground">Recovery time</dt>
              <dd className={figure}>
                {data.recoveredFailures ? seconds(data.meanRecoverySeconds) : "—"}
              </dd>
              <dd className="text-hint text-muted-foreground">
                {data.recoveredFailures
                  ? `mean over ${data.recoveredFailures} recovered failure${data.recoveredFailures === 1 ? "" : "s"}`
                  : data.failed
                    ? "no failure followed by a success yet"
                    : "no failures in this window"}
              </dd>
            </div>
          </dl>
          {/* A window nothing succeeded in has no release time, and a row of
              empty days would draw one as zero (§10). */}
          {durationValues.length > 0 && (
            <>
              <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
                <h3 className="text-title font-medium">Release time per day</h3>
                <p className="inline-flex items-center gap-1.5 text-hint text-muted-foreground">
                  <span
                    aria-hidden
                    className="w-3 border-t border-dashed border-muted-foreground"
                  />
                  median {seconds(data.medianDurationSeconds)}
                </p>
              </div>
              {/* The same days at the same height as the releases beside it,
                  which a line through the days that shipped was not: it
                  joined four releases a week apart into one slope with no
                  scale. The first line of the plot is kept for the scale's
                  top, so the slowest day never runs under its own label. */}
              <div key={range} className="relative animate-rise">
                <ol
                  className="flex h-28 items-end gap-px border-b border-hairline pt-4"
                  aria-label="Median release time per day"
                  data-testid="insights-durations"
                >
                  {daily.map((day, index) => (
                    <li
                      key={`${day.date}-${index}`}
                      className="flex h-full min-w-0 flex-1 flex-col items-center justify-end"
                      title={
                        day.medianDurationSeconds
                          ? `${day.date}: ${seconds(day.medianDurationSeconds)}, median of ${day.succeeded} successful release${day.succeeded === 1 ? "" : "s"}`
                          : `${day.date}: no successful release`
                      }
                    >
                      {day.medianDurationSeconds ? (
                        <span
                          className="block w-full max-w-3 rounded-t-sm bg-chart-2"
                          style={{
                            height: `${Math.max(4, (day.medianDurationSeconds / slowest) * 100)}%`,
                          }}
                        />
                      ) : (
                        <span className="block h-0.5 w-full max-w-3 bg-meter-track" />
                      )}
                    </li>
                  ))}
                </ol>
                <span className="numeric absolute top-0 left-0 text-micro leading-4 text-muted-foreground">
                  {seconds(slowest)}
                </span>
                <span
                  aria-hidden
                  className="pointer-events-none absolute inset-x-0 top-4 border-t border-hairline"
                />
                <span
                  aria-hidden
                  className="pointer-events-none absolute inset-x-0 border-t border-dashed border-muted-foreground"
                  style={{
                    bottom: `calc(1px + (100% - 1rem - 1px) * ${data.medianDurationSeconds / slowest})`,
                  }}
                />
              </div>
              {axis}
            </>
          )}
        </div>
      )}
      {failures && <div className={cn("min-w-0", history && "lg:col-span-2")}>{failures}</div>}
    </div>
  )

  return (
    <div className={children ? "space-y-8" : undefined}>
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
        <PanelBody className={children ? undefined : "space-y-6"}>
          {insights.error && <ErrorState error={insights.error} />}
          {insights.loading && !data && <LoadingRows rows={2} />}
          {data && data.runs === 0 && (
            <EmptyNote>
              No release finished in this window. Figures appear after the first deployment ends.
            </EmptyNote>
          )}
          {!history && data && data.runs > 0 && (
            <>
              <StatGrid
                columns={4}
                dense
                className={children ? "[&_[data-slot=stat-tile]]:py-3" : undefined}
              >
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
                <StatTile
                  label="Median release"
                  value={seconds(data.medianDurationSeconds)}
                  trend={
                    !children && (
                      <TileTrend
                        // Only the days something shipped: a day with no release
                        // has no duration, and drawing it at zero would say the
                        // releases got faster.
                        values={durationValues}
                        label={`Median release time per day over the last ${data.windowDays} days`}
                        color="var(--chart-2)"
                      />
                    )
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
                {/* No trend of its own: its shape is the Releases per day chart
                  below the records, and a day's zero or one drew as noise. */}
                <StatTile
                  label="Deploys per week"
                  value={<NumberTicker value={data.deploysPerWeek} decimalPlaces={1} />}
                  hint={`${data.succeeded} successful over ${data.windowDays} days`}
                />
              </StatGrid>
            </>
          )}
          {!children && charts}
        </PanelBody>
      </Panel>
      {children}
      {children && charts && (
        <Panel plain>
          <PanelHeader
            title="Release history"
            actions={
              <span className="text-hint text-muted-foreground">
                {WINDOWS.find(([value]) => value === range)?.[1]}
              </span>
            }
          />
          <PanelBody>{charts}</PanelBody>
        </Panel>
      )}
    </div>
  )
}
