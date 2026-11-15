"use client"

import { useEffect, useState } from "react"
import { ago, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"

/**
 * How long ago something happened, kept true while the page stays open:
 * "checked 3m ago", "Updated just now". The words are lib/format's `ago`
 * (largest unit, "just now" for the first five seconds) and the exact time is
 * in the title, for the reader who needs the second.
 *
 * It owns the ticking, so every age on a page moves on one rule instead of
 * each wrapper's own interval: every second while the age is in seconds, then
 * every half minute, then every minute (every second for the first hour when
 * two units show the seconds). `live={false}` stops it, for an age
 * in a panel that is closed. `verb` is written as given, so a caller at the
 * start of a sentence passes "Checked" and one inside it "checked".
 */
export function Ago({
  at,
  verb,
  units = 1,
  tick,
  live = true,
  className,
}: {
  /** Epoch milliseconds, or an ISO string as the API sends it. */
  at: number | string
  verb?: string
  units?: 1 | 2
  /** A fixed interval in milliseconds, in place of the one the age's unit asks for. */
  tick?: number
  live?: boolean
  className?: string
}) {
  const time = typeof at === "number" ? at : Date.parse(at)
  const now = useAgeClock(time, tick, live, units === 2)
  // An instant stamped a moment after this tab's clock last read is now, not a
  // negative age, until the next tick.
  const words = ago(time, Math.max(now, time), { units })
  return (
    <time
      dateTime={Number.isFinite(time) ? new Date(time).toISOString() : undefined}
      title={Number.isFinite(time) ? timestamp(new Date(time).toISOString()) : undefined}
      className={cn("numeric", className)}
    >
      {verb ? `${verb} ${words}` : words}
    </time>
  )
}

/**
 * The time, read again as often as the age's own unit needs: `useNow` with an
 * interval that follows the age, which a fixed `setInterval` cannot. Each
 * reading schedules the next, so an age that crosses into minutes slows its
 * clock from then on.
 */
function useAgeClock(
  at: number,
  tick: number | undefined,
  live: boolean,
  seconds: boolean,
): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!live) return
    const age = now - at
    // Two units say the seconds for the first hour ("2m 30s ago").
    const fast = age < 60_000 || (seconds && age < 3_600_000)
    const wait = tick ?? (fast ? 1000 : age < 3_600_000 ? 30_000 : 60_000)
    const timer = setTimeout(() => setNow(Date.now()), wait)
    return () => clearTimeout(timer)
  }, [now, at, tick, live, seconds])
  return now
}
