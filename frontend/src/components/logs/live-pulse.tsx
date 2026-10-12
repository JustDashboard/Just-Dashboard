"use client"

import { useEffect, useRef, useState } from "react"
import { cn } from "@/lib/utils"
import type { LogLine } from "@/lib/types"
import { PULSE_SLOT_MS, clockSkew, pulseOf } from "@/components/logs/logs-model"

/**
 * How busy the stream is, beside its Live dot: a strip of the last minute's
 * columns and the pace in words. It moves while the socket is open — a column
 * slides off the left every two seconds whether or not anything arrived — so
 * a quiet log reads as quiet and alive rather than as a frozen pane. Errors
 * are the red at the top of their column, the histogram's stacking.
 */
export function LivePulse({ lines, paused }: { lines: readonly LogLine[]; paused?: boolean }) {
  // The host's clock, read off lines that arrived while the pane was open:
  // the tail replayed on connect was written before it was sent, and its age
  // is not the clocks' difference.
  const newest = newestStamp(lines)
  const replayed = useRef(newest)
  const skew = useRef<number | undefined>(undefined)
  useEffect(() => {
    if (newest && newest !== replayed.current) {
      skew.current = clockSkew(skew.current, Date.now(), newest)
    }
  }, [newest])

  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (paused) return
    const timer = setInterval(() => setNow(Date.now() - (skew.current ?? 0)), PULSE_SLOT_MS)
    return () => clearInterval(timer)
  }, [paused])

  const slots = pulseOf(lines, now)
  if (!slots) return null
  const total = slots.reduce((n, s) => n + s.total, 0)
  const peak = Math.max(1, ...slots.map((s) => s.total))
  const pace = total === 0 ? "quiet" : `${total.toLocaleString()} a minute`

  return (
    <span
      className="flex shrink-0 items-center gap-2"
      title={`${total.toLocaleString()} ${total === 1 ? "line" : "lines"} written in the last minute`}
    >
      <span aria-hidden className="flex h-4 w-[90px] items-end gap-px">
        {slots.map((slot, i) => (
          <span
            key={i}
            className={cn(
              "flex min-w-0 flex-1 flex-col justify-end overflow-hidden rounded-[1px]",
              slot.total === 0 && "bg-meter-track/60",
            )}
            style={{ height: slot.total ? `${Math.max(18, (slot.total / peak) * 100)}%` : "2px" }}
          >
            {slot.errors > 0 && (
              <span
                className="w-full shrink-0 bg-destructive"
                style={{ height: `${(slot.errors / slot.total) * 100}%` }}
              />
            )}
            {slot.total > slot.errors && <span className="w-full flex-1 bg-chart-2" />}
          </span>
        ))}
      </span>
      {/* In a narrow console the strip carries the pace and the controls
          keep their room; the title still says it. */}
      <span className="numeric text-hint whitespace-nowrap text-muted-foreground @max-md:hidden">
        {pace}
      </span>
    </span>
  )
}

function newestStamp(lines: readonly LogLine[]) {
  for (let i = lines.length - 1; i >= 0; i--) {
    if (lines[i].timestamp) return lines[i].timestamp
  }
  return undefined
}
