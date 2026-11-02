"use client"

import type { RequestLatency } from "@/lib/types"
import { latency, latencyTone } from "@/lib/requests"
import { StatButton, StatGrid, StatTile } from "@/components/stat-tile"

/**
 * How long things took, in the words a person would ask it in: half were this
 * fast, nearly all were this fast, and the slowest took this long.
 *
 * This was a logarithmic rule with ticks named p50, p95 and p99, which said the
 * same numbers in notation and left the reader to learn it, and whose ticks
 * crowded into one label whenever the tail bunched up. Each figure narrows the
 * window to what took at least that long, as the tick did (§15 pass 3).
 */
const READINGS: { key: keyof RequestLatency; name: string; hint: string }[] = [
  { key: "p50", name: "Half", hint: "finished within this" },
  { key: "p95", name: "19 in 20", hint: "finished within this" },
  { key: "p99", name: "99 in 100", hint: "finished within this" },
  { key: "max", name: "Slowest", hint: "the single longest" },
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
  return (
    <StatGrid columns={4} dense framed>
      {READINGS.map(({ key, name, hint }) => {
        const ms = reading[key]
        const slow = latencyTone(ms) === "warning" || (threshold !== undefined && ms >= threshold)
        return (
          <StatButton
            key={key}
            label={`${name}: ${latency(ms)}. Show what took at least this long`}
            onClick={() => onPick(ms)}
          >
            <StatTile
              label={name}
              value={latency(ms)}
              tone={slow ? "warning" : "default"}
              hint={hint}
              className="group-hover:bg-row-hover"
            />
          </StatButton>
        )
      })}
    </StatGrid>
  )
}
