"use client"

import { bytes, rate } from "@/lib/format"
import { flowBytes } from "@/lib/network/flows"
import { cn } from "@/lib/utils"
import { Sparkline } from "@/components/sparkline"
import type { NetworkLivePoint } from "@/lib/types"

/**
 * Received, in every picture and figure of it: the network chart's cyan.
 * Sent is `TX`. Written once so a pair is never re-spelled as an arbitrary
 * `text-[var(--chart-5)]` beside one that uses the constant.
 */
export const RX = "var(--chart-5)"
/** Sent: the network chart's second colour. Received is `RX`. */
export const TX = "var(--chart-2)"

/** A rate as a chart's pointer reads it: "1.2 MB/s". */
export function formatRate(value: number): string {
  return rate(value)
}

/** A rate as a chart's axis reads it, whole units: "12 MB/s". */
export function axisRate(value: number): string {
  return `${bytes(value, 0)}/s`
}

/**
 * The two areas a chart of in and out draws, in their colours. The words are
 * the legend's: "In" and "Out" for a device, "From it" and "To it" for a
 * tunnel's peer, whose in is the peer's out.
 */
export function inOutSeries(words: { rx: string; tx: string } = { rx: "In", tx: "Out" }) {
  return [
    { key: "rx", label: words.rx, color: RX, kind: "area" as const },
    { key: "tx", label: words.tx, color: TX, kind: "area" as const },
  ]
}

/**
 * What a figure of `InOut` is, by its unit: a rate a second, a byte count, or
 * a conntrack counter (a decimal string or a bigint, since a flow's bytes can
 * pass what a number holds exactly).
 */
type Figure = number | string | bigint | null | undefined

function said(value: Figure, unit: "rate" | "bytes" | "flow"): string {
  if (unit === "flow") return flowBytes(typeof value === "number" ? String(value) : value)
  const n = typeof value === "number" ? value : Number(value ?? Number.NaN)
  return unit === "rate" ? rate(n) : bytes(n)
}

/**
 * Received and sent as one reading: "↓ 1.2 MB/s ↑ 80 KB/s", down first, each
 * in its direction's colour.
 *
 * `inline` is the pair on one line, as a row's trailing figure; `stacked` puts
 * them one over the other, right-aligned, as a device list's column, with the
 * last two minutes drawn beside them when `trend` holds them (from `sm`: on a
 * phone the row has no room for a picture). `plain` drops the colour, for a
 * pair inside a sentence or a fact whose other figures are not coloured.
 */
export function InOut({
  rx,
  tx,
  unit,
  layout = "inline",
  trend,
  plain,
  className,
}: {
  rx: Figure
  tx: Figure
  unit: "rate" | "bytes" | "flow"
  layout?: "inline" | "stacked"
  /** The live points behind a stacked pair; the last sixty are drawn. */
  trend?: NetworkLivePoint[]
  plain?: boolean
  className?: string
}) {
  const down = <span style={plain ? undefined : { color: RX }}>↓ {said(rx, unit)}</span>
  const up = <span style={plain ? undefined : { color: TX }}>↑ {said(tx, unit)}</span>
  if (layout === "inline") {
    return (
      <span className={cn("numeric font-mono text-hint whitespace-nowrap", className)}>
        {down} {up}
      </span>
    )
  }
  const tail = (trend ?? []).slice(-60)
  return (
    <span className={cn("flex items-center gap-3", className)}>
      {tail.length > 1 && (
        <span className="relative hidden h-6 w-20 sm:block">
          <Sparkline
            values={tail.map((p) => p.rx)}
            color={RX}
            width={80}
            height={24}
            className="absolute inset-0 h-6 w-20"
            label="Received over the last two minutes"
          />
          <Sparkline
            values={tail.map((p) => p.tx)}
            color={TX}
            width={80}
            height={24}
            className="absolute inset-0 h-6 w-20 opacity-80"
            label="Sent over the last two minutes"
          />
        </span>
      )}
      <span className="numeric grid min-w-[5.5rem] text-right font-mono text-micro leading-tight">
        {down}
        {up}
      </span>
    </span>
  )
}
