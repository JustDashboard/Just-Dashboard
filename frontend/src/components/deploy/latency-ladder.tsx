"use client"

import { useCallback, useState } from "react"
import { cn } from "@/lib/utils"
import type { RequestLatency } from "@/lib/types"
import { latency, latencyTone } from "@/lib/requests"
import { useMediaQuery } from "@/hooks/use-mobile"

/**
 * The window's response times as the distribution the API returns, drawn on
 * one rule: where half the requests were answered, where the slow tenth
 * begins, the slowest hundredth, and the one slowest of all.
 *
 * The same numbers used to be a line of footer text — "p50 24ms · p95 412ms ·
 * p99 2.84s" — which states the distribution and shows none of it. On a
 * logarithmic rule the gap between the median and the tail is the picture: a
 * p99 a hundred times the p50 is a long reach to the right, and the span past
 * a second (or past the line a latency alert watches) is washed amber, so
 * where the tail lands against it is read rather than worked out.
 *
 * Each mark narrows the rows to the requests at least that slow — the API's
 * own `minMs` — so "p99 is 2.84 s" and the requests that make it so are one
 * press apart (§15 pass 3).
 */

const MARKS: { key: keyof RequestLatency; name: string; phone: boolean }[] = [
  { key: "p50", name: "p50", phone: true },
  { key: "p75", name: "p75", phone: false },
  { key: "p90", name: "p90", phone: false },
  { key: "p95", name: "p95", phone: true },
  { key: "p99", name: "p99", phone: true },
  { key: "max", name: "max", phone: true },
]

/**
 * A label's width, near enough: the figure line is 12px tabular digits and the
 * name line 10px, so the longer line at about 6.5px a character. Labels whose
 * boxes would come within `GAP_PX` of each other share one, drawn as pairs on
 * one line — "p99 2.84s · max 3.12s" — rather than printing over each other.
 * A shared label cannot stand over each of its ticks, so each figure keeps its
 * own name beside it: a line of figures over a line of names, each centred
 * to a different width, put 412ms over the p90's tick and no name under its
 * figure.
 */
const CHAR_PX = 6.5
const GAP_PX = 8

export function LatencyLadder({
  latency: reading,
  threshold,
  onPick,
}: {
  latency: RequestLatency
  /** The enabled latency alert's line, in milliseconds, when there is one. */
  threshold?: number
  onPick: (ms: number) => void
}) {
  const [width, setWidth] = useState(0)
  // A ref callback rather than an effect: the rule is measured when it mounts
  // and whenever the pane resizes, and never read during render.
  const measure = useCallback((node: HTMLDivElement | null) => {
    if (!node) return
    const observer = new ResizeObserver(([entry]) => setWidth(entry.contentRect.width))
    observer.observe(node)
    return () => observer.disconnect()
  }, [])
  const wide = useMediaQuery("(min-width: 640px)")

  const ceiling = Math.max(reading.max, threshold ?? 0, 10) * 1.15
  const scale = Math.log10(ceiling)
  const at = (ms: number) => (Math.log10(Math.max(ms, 1)) / scale) * width
  const line = threshold ?? 1000

  const marks = MARKS.map((mark) => ({ ...mark, ms: reading[mark.key], x: at(reading[mark.key]) }))
  const labelled = marks.filter((mark) => wide || mark.phone)
  // Where a group's label would sit: centred over its marks, and moved in
  // from either end rather than past it.
  const extent = (group: typeof marks) => {
    const chars =
      group.length > 1
        ? group.map((mark) => `${mark.name} ${latency(mark.ms)}`).join(" · ").length
        : Math.max(latency(group[0].ms).length, group[0].name.length)
    const span = chars * CHAR_PX
    const center = (group[0].x + group[group.length - 1].x) / 2
    const left = Math.max(0, Math.min(center - span / 2, width - span))
    return {
      left,
      right: left + span,
      center,
      pinned: left === 0 ? "start" : left === width - span ? "end" : undefined,
    }
  }
  const groups: (typeof marks)[] = []
  for (const mark of labelled) {
    groups.push([mark])
    while (
      groups.length > 1 &&
      extent(groups[groups.length - 1]).left < extent(groups[groups.length - 2]).right + GAP_PX
    ) {
      const last = groups.pop()!
      groups[groups.length - 1].push(...last)
    }
  }

  return (
    <div ref={measure} className="relative h-16 w-full min-w-0 animate-rise">
      {width > 0 && (
        <>
          {groups.map((group) => {
            const { center, pinned } = extent(group)
            return (
              <div
                key={group[0].key}
                aria-hidden
                style={
                  pinned === "start"
                    ? { left: 0 }
                    : pinned === "end"
                      ? { right: 0 }
                      : { left: center, transform: "translateX(-50%)" }
                }
                className={cn(
                  "pointer-events-none absolute inset-y-0 flex flex-col justify-between whitespace-nowrap",
                  pinned === "start"
                    ? "items-start"
                    : pinned === "end"
                      ? "items-end"
                      : "items-center",
                )}
              >
                <span className="numeric text-xs font-medium">
                  {group.map((mark, i) => (
                    <span key={mark.key}>
                      {i > 0 && <span className="text-muted-foreground/50"> · </span>}
                      {group.length > 1 && (
                        <span className="font-normal text-muted-foreground">{mark.name} </span>
                      )}
                      <span
                        className={latencyTone(mark.ms) === "warning" ? "text-warning" : undefined}
                      >
                        {latency(mark.ms)}
                      </span>
                    </span>
                  ))}
                </span>
                {group.length === 1 && (
                  <span className="text-micro text-muted-foreground">{group[0].name}</span>
                )}
              </div>
            )
          })}

          <div className="absolute inset-x-0 top-1/2 h-2 -translate-y-1/2 overflow-hidden rounded-sm bg-meter-track">
            {line < ceiling && (
              <span
                aria-hidden
                className="absolute inset-y-0 right-0 bg-wash-warning"
                style={{ left: at(line) }}
              />
            )}
          </div>

          {marks.map((mark) => (
            <button
              key={mark.key}
              type="button"
              onClick={() => onPick(mark.ms)}
              aria-label={
                mark.key === "max"
                  ? `Show the slowest requests (${latency(mark.ms)})`
                  : `Show the requests slower than ${mark.name} (${latency(mark.ms)})`
              }
              title={
                mark.key === "max"
                  ? `The slowest: ${latency(mark.ms)}`
                  : `${mark.name} ${latency(mark.ms)} — show the requests at least this slow`
              }
              className="group absolute top-1/2 flex h-6 w-4 -translate-x-1/2 -translate-y-1/2 justify-center rounded-sm focus-ring"
              style={{ left: Math.min(Math.max(mark.x, 2), width - 2) }}
            >
              <span className="h-full w-px bg-[var(--chart-3)] transition-colors group-hover:bg-foreground" />
            </button>
          ))}
        </>
      )}
    </div>
  )
}
