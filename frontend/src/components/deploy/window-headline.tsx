"use client"

import { bytes, dayMinuteAt, percent } from "@/lib/format"
import type { DeploymentRequests } from "@/lib/types"
import { LATENCY_SHARE } from "@/lib/latency-words"
import { latency, latencyTone, type RequestRange } from "@/lib/requests"
import { cn } from "@/lib/utils"
import { StatusDot } from "@/components/status-dot"
import { Skeleton } from "@/components/ui/skeleton"
import { failingTone } from "@/components/deploy/fleet"
import {
  coverageNote,
  historyOf,
  keptWords,
  periodWords,
} from "@/components/deploy/window-headline-model"

/**
 * The window in one sentence, over the chart that draws it.
 *
 * Four tiles stood here — a rate, a failure strip, a latency line and the
 * bytes — each taken over the last hour whatever the window below them
 * said, in a box of their own above the box of requests. The reader had to
 * assemble the sentence, and to notice that "48.8 a minute" was not about
 * the seven days they had just picked. This is that sentence, written out
 * and about the window on screen: how many requests, from how many people,
 * how many of them pages, how many failed, how long they took and what was
 * sent, then which stretch of time that was. The figures carry the weight
 * (the name's 500, tabular), the words between them recede, and a failure
 * takes its colour where it is said.
 *
 * The second line is about the record rather than the traffic: the stretch,
 * how far back the dashboard has kept it when that is less than the window,
 * and at its end whatever the page puts there (a deployment's alerts).
 *
 * Both lines are one height with a reading and without one, so the chart and
 * the rows under them are where they will be before the window is read.
 */
export function WindowHeadline({
  data,
  range,
  since,
  until,
  asked,
  aside,
  dense,
}: {
  data?: DeploymentRequests
  range: RequestRange
  since?: string
  until?: string
  /** The instant the window asked from, to tell a short record from a quiet one. */
  asked?: number
  /** The second line's end: the alerts that watch this record. */
  aside?: React.ReactNode
  /**
   * Over a phone's rows: the count, the people and the failures, one line,
   * so the rows keep the screen. The rest is Insights' reading, a stop away.
   */
  dense?: boolean
}) {
  const period = periodWords(range, since, until, atMinute)
  return (
    <div
      data-slot="window-headline"
      className="flex shrink-0 flex-col gap-1 border-b border-hairline px-4 py-3"
    >
      {data?.status === "available" ? (
        <Sentence data={data} dense={dense} />
      ) : (
        <div className="flex h-5 items-center">
          <Skeleton className="h-3.5 w-96 max-w-full" />
        </div>
      )}
      <div
        className={cn(
          "flex min-h-5 min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground",
          dense && "max-md:hidden",
        )}
      >
        <span className="first-letter:uppercase">{period}</span>
        {data && <RecordWords data={data} asked={asked} />}
        {aside && <span className="ml-auto flex min-w-0 items-center">{aside}</span>}
      </div>
    </div>
  )
}

/** "Nov 20, 18:32": where a stretch of the record begins or ends. */
function atMinute(iso: string) {
  return dayMinuteAt(new Date(iso))
}

function Sentence({ data, dense }: { data: DeploymentRequests; dense?: boolean }) {
  const summary = data.summary
  const failed = summary.classes["5xx"] ?? 0
  const tone = failingTone(summary.errorRate)
  const audience = summary.audience
  const p95 = summary.latency?.p95
  const figure = "numeric font-medium text-foreground"
  // What a phone's dense line leaves to Insights.
  const extra = dense ? "max-md:hidden" : undefined
  return (
    <p className="flex min-h-5 min-w-0 flex-wrap items-baseline gap-x-1 text-body text-muted-foreground">
      <StatusDot
        tone={
          summary.total === 0
            ? "unknown"
            : tone === "danger"
              ? "danger"
              : tone === "warning"
                ? "warning"
                : "running"
        }
        className="mr-1 self-center"
      />
      <span>
        <span className={figure}>{summary.total.toLocaleString()}</span>{" "}
        {summary.total === 1 ? "request" : "requests"}
      </span>
      {audience && (
        <span>
          from <span className={figure}>{audience.visitors.toLocaleString()}</span>{" "}
          {audience.visitors === 1 ? "person" : "people"}
          {audience.bots > 0 && (
            <span className={extra}>
              {" "}
              and <span className={figure}>{audience.bots.toLocaleString()}</span>{" "}
              {audience.bots === 1 ? "bot" : "bots"}
            </span>
          )}
        </span>
      )}
      <span aria-hidden className={cn("text-faint", extra)}>
        ·
      </span>
      <span className={extra}>
        <span className={figure}>{summary.pages.toLocaleString()}</span>{" "}
        {summary.pages === 1 ? "page view" : "page views"}
      </span>
      <span aria-hidden className="text-faint">
        ·
      </span>
      {failed > 0 ? (
        <span className={tone === "danger" ? "text-destructive" : "text-warning"}>
          <span className="numeric font-medium">{failed.toLocaleString()}</span> failed
          <span className="numeric"> ({percent(summary.errorRate * 100, 1)})</span>
        </span>
      ) : (
        <span>
          <span className={figure}>none</span> failed
        </span>
      )}
      {p95 !== undefined && (
        <>
          <span aria-hidden className={cn("text-faint", extra)}>
            ·
          </span>
          <span className={extra}>
            {LATENCY_SHARE.p95.toLowerCase()} inside{" "}
            <span
              className={cn(
                "numeric font-medium",
                latencyTone(p95) === "warning" ? "text-warning" : "text-foreground",
              )}
            >
              {latency(p95)}
            </span>
          </span>
        </>
      )}
      <span aria-hidden className={cn("text-faint", extra)}>
        ·
      </span>
      <span className={extra}>
        <span className={figure}>{bytes(summary.bytes)}</span> sent
      </span>
    </p>
  )
}

function RecordWords({ data, asked }: { data: DeploymentRequests; asked?: number }) {
  const note = coverageNote(data, asked, atMinute)
  const kept = keptWords(historyOf(data))
  return (
    <>
      {note && <span className="text-warning">{note}</span>}
      {!note && kept && <span>{kept}</span>}
      {data.coverage.stale && (
        <span className="text-warning">The proxy did not answer just now</span>
      )}
    </>
  )
}
