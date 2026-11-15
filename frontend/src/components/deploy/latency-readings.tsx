"use client"

import { Fragment } from "react"
import type { RequestLatency } from "@/lib/types"
import { Stopwatch } from "@/components/icons"
import { LATENCY_SHARE } from "@/lib/latency-words"
import { latency, latencyTone } from "@/lib/requests"
import { cn } from "@/lib/utils"
import { HUE } from "@/components/overview/readings"
import { WireMark } from "@/components/deploy/wire"

/**
 * How long things took, as the sentence a person would say: half were inside
 * this, nearly all inside this, and the slowest took this long.
 *
 * This was a logarithmic rule with ticks named p50, p95 and p99, which said the
 * same numbers in notation and left the reader to learn it; then four tiles, a
 * figure under a name each, which was the sentence cut into a row of boxes.
 * Now it is the sentence, on the measure's mark. Each figure in it is still a
 * button, and narrows the window to what took at least that long, as the tick
 * and the tile did (§15 pass 3): the dotted rule under a figure says so before
 * a pointer does.
 */
const READINGS: { key: keyof RequestLatency; name: string; lead: string; words: string }[] = [
  { key: "p50", name: LATENCY_SHARE.p50, lead: "", words: "Half inside" },
  { key: "p95", name: LATENCY_SHARE.p95, lead: ", ", words: "19 in 20 inside" },
  { key: "p99", name: LATENCY_SHARE.p99, lead: ", ", words: "99 in 100 inside" },
  { key: "max", name: "Slowest", lead: "; ", words: "the slowest took" },
]

export function LatencyReadings({
  latency: reading,
  threshold,
  onPick,
}: {
  latency: RequestLatency
  /** The enabled latency alert's line: a figure past it is amber even under a second. */
  threshold?: number
  onPick: (ms: number) => void
}) {
  const slowAt = (ms: number) =>
    latencyTone(ms) === "warning" || (threshold !== undefined && ms >= threshold)
  // The mark answers for nearly all of them: one slow request in a hundred
  // does not make the window slow, nineteen in twenty past the line does.
  const slow = slowAt(reading.p95)
  return (
    <div data-slot="latency-readings" className="flex min-w-0 items-center gap-3.5">
      {slow ? (
        <WireMark size="md" tone="warning">
          <Stopwatch aria-hidden />
        </WireMark>
      ) : (
        <WireMark size="md" hue={HUE.load}>
          <Stopwatch aria-hidden />
        </WireMark>
      )}
      <p className="min-w-0 text-body leading-relaxed text-muted-foreground">
        {READINGS.map(({ key, name, lead, words }) => {
          const ms = reading[key]
          return (
            <Fragment key={key}>
              {lead}
              {words}{" "}
              <button
                type="button"
                aria-label={`${name}: ${latency(ms)}. Show what took at least this long`}
                title="Show what took at least this long"
                onClick={() => onPick(ms)}
                className={cn(
                  "numeric rounded-sm font-medium underline decoration-dotted underline-offset-4 focus-ring transition-colors hover:bg-row-hover",
                  slowAt(ms) ? "text-warning" : "text-foreground",
                )}
              >
                {latency(ms)}
              </button>
            </Fragment>
          )
        })}
        .
      </p>
    </div>
  )
}
