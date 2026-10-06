"use client"

import { useState } from "react"
import { cn } from "@/lib/utils"
import { byteParts } from "@/lib/format"
import { NumberTicker } from "@/components/ui/number-ticker"

/**
 * A figure fed by the metrics socket, gliding to each frame's value rather
 * than jumping to it, so a reading that changes every two seconds is seen to
 * move. It counts up once when it lands, as every figure that arrives does.
 *
 * The unit stays outside the count: 980 KB/s becoming 1.0 MB/s is a new scale,
 * not a count from 980 down to 1, so a change of unit swaps the figure for the
 * new value without counting.
 */
export function LiveFigure({
  value,
  decimals = 0,
  unit,
}: {
  value: number
  decimals?: number
  /** Set after the number as written — "%" against it, " GB" with its space. */
  unit?: string
}) {
  // The count from zero belongs to the first unit only; a later one starts
  // where the figure already is.
  const [scale, setScale] = useState({ unit, from: 0 })
  if (scale.unit !== unit) setScale({ unit, from: value })
  return (
    <>
      <NumberTicker key={unit} value={value} startValue={scale.from} decimalPlaces={decimals} />
      {unit}
    </>
  )
}

/** `LiveFigure` for a size or a rate in bytes, on the same ladder `bytes` uses. */
export function LiveBytes({ value, suffix = "" }: { value: number; suffix?: string }) {
  const parts = byteParts(value)
  return (
    <LiveFigure value={parts.value} decimals={parts.decimals} unit={` ${parts.unit}${suffix}`} />
  )
}

/** Wider than this, neighbouring cores share a bar and it shows the busier of them. */
const MOST_BARS = 32

/**
 * Every core's share beside the CPU figure, from the same frame. The total
 * says how busy the machine is; this says whether that is eight cores at 90%
 * or one pinned and seven idle, which is a different fix. A shared bar keeps
 * the busiest of its cores so a pinned one is never averaged away.
 */
export function CoreBars({ cores, color }: { cores: number[]; color: string }) {
  if (cores.length < 2) return null
  const size = Math.ceil(cores.length / MOST_BARS)
  const bars: number[] = []
  for (let i = 0; i < cores.length; i += size) bars.push(Math.max(...cores.slice(i, i + size)))
  const busiest = Math.max(...cores)
  return (
    <span
      role="img"
      aria-label={`${cores.length} cores, the busiest at ${busiest.toFixed(0)}%`}
      className="inline-flex h-5 items-end gap-0.5 self-center"
    >
      {bars.map((share, index) => (
        <span
          key={index}
          className={cn(
            "relative h-full overflow-hidden rounded-sm bg-meter-track",
            bars.length <= 8 ? "w-1.5" : bars.length <= 16 ? "w-1" : "w-0.5",
          )}
        >
          <span
            className="absolute inset-x-0 bottom-0 transition-[height] duration-700 ease-out"
            style={{ height: `${Math.min(Math.max(share, 0), 100)}%`, background: color }}
          />
        </span>
      ))}
    </span>
  )
}

/**
 * The key to a tile's hour, before its name: the 2×10 bar a chart's legend
 * draws, so a reading and the line under it are one colour and the four lines
 * read apart before a label is read.
 */
export function SeriesKey({ color }: { color: string }) {
  return (
    <span
      aria-hidden
      className="mr-1.5 inline-block h-2.5 w-0.5 rounded-full align-[-1px]"
      style={{ background: color }}
    />
  )
}
