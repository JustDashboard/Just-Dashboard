"use client"

import { useMemo } from "react"
import { relativeTime } from "@/lib/format"
import type { PressureCount, ProtectionPressure } from "@/lib/types"
import { BarList, type BarListItem } from "@/components/bar-list"
import { ChartPanel } from "@/components/metrics/chart-panel"
import type { Series } from "@/components/metrics/metric-chart"
import { Tag } from "@/components/tag"
import { seriesRows } from "@/components/network/protection/reading"

const TABLE_SERIES: Series[] = [
  { key: "conntrack:count", label: "Tracked", color: "var(--chart-1)", kind: "area" },
  { key: "conntrack:max", label: "Maximum", color: "var(--chart-5)" },
]
const DROP_SERIES: Series[] = [
  { key: "conntrack:drop", label: "Dropped", color: "var(--chart-3)" },
  { key: "conntrack:early_drop", label: "Early-dropped", color: "var(--chart-4)" },
  { key: "conntrack:insert_failed", label: "Failed inserts", color: "var(--chart-2)" },
]
const whole = (v: number) => Math.round(v).toLocaleString()

function bars(counts: PressureCount[], mono = true): BarListItem[] {
  const top = counts[0]?.count || 1
  return counts.map((c) => ({
    key: c.key,
    label: c.key,
    mono,
    value: (
      <span className="numeric">{`${c.count.toLocaleString()} · ${Math.round(c.share * 100)}%`}</span>
    ),
    share: c.count / top,
  }))
}

/**
 * Why the connection table is filling, not only how full it is.
 *
 * The last day of its count against its maximum, recorded every minute, and
 * the kernel's own refusals when it was full; then what it holds now — by
 * state, protocol, source and destination port — read over netlink and
 * bounded, with the shares that point at a cause said in a sentence each. The
 * causes are indications from those shares: a SYN flood and a crowd of slow
 * clients look alike from here.
 */
export function PressurePanel({ pressure }: { pressure: ProtectionPressure }) {
  const tableRows = useMemo(
    () => seriesRows(pressure.series, ["conntrack:count", "conntrack:max"]),
    [pressure.series],
  )
  const dropRows = useMemo(
    () =>
      seriesRows(pressure.series, [
        "conntrack:drop",
        "conntrack:early_drop",
        "conntrack:insert_failed",
      ]),
    [pressure.series],
  )
  const b = pressure.breakdown
  return (
    <div className="flex min-w-0 flex-col gap-6" aria-label="Connection table pressure">
      {pressure.causes.length > 0 ? (
        <ul className="space-y-2">
          {pressure.causes.map((c) => (
            <li key={c.kind} className="flex min-w-0 items-baseline gap-2 text-hint">
              <Tag tone={c.severity === "warning" ? "warning" : "default"} className="shrink-0">
                {c.kind}
              </Tag>
              <span className="min-w-0">
                {c.message} <span className="text-muted-foreground">{c.evidence}.</span>
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-hint text-muted-foreground">
          Nothing in the table&rsquo;s shares points at a cause of pressure.
        </p>
      )}

      <div className="grid gap-8 lg:grid-cols-2 [&>*]:min-w-0">
        <ChartPanel
          plain
          title="Tracked connections, last day"
          rows={tableRows}
          series={TABLE_SERIES}
          format={whole}
          showPeaks={false}
          note="Nothing recorded yet: the recorder reads the table every minute."
          height={160}
        />
        <ChartPanel
          plain
          title="Refused by a full table"
          rows={dropRows}
          series={DROP_SERIES}
          format={whole}
          showPeaks={false}
          note="The kernel refused nothing for a full table in the last day."
          height={160}
        />
      </div>

      <div className="grid gap-6 md:grid-cols-2 xl:grid-cols-4 [&>*]:min-w-0">
        <section aria-label="By state" className="space-y-1.5">
          <p className="text-hint font-medium">By state</p>
          <BarList items={bars(b.byState, false)} emptyLabel="Nothing tracked" />
        </section>
        <section aria-label="By source" className="space-y-1.5">
          <p className="text-hint font-medium">Busiest sources</p>
          <BarList items={bars(b.topSources)} emptyLabel="Nothing tracked" />
        </section>
        <section aria-label="By port" className="space-y-1.5">
          <p className="text-hint font-medium">Busiest destination ports</p>
          <BarList items={bars(b.topPorts)} emptyLabel="Nothing tracked" />
        </section>
        <section aria-label="By protocol" className="space-y-1.5">
          <p className="text-hint font-medium">By protocol</p>
          <BarList items={bars(b.byProtocol, false)} emptyLabel="Nothing tracked" />
        </section>
      </div>

      <p className="text-hint text-muted-foreground">
        {b.read.toLocaleString()} entries read
        {b.truncated ? " (the first ones only: the table is larger than the read's bound)" : ""}
        {", "}
        {b.unreplied.toLocaleString()} never answered, {b.assured.toLocaleString()} assured.{" "}
        {pressure.stats
          ? `Since boot the kernel dropped ${pressure.stats.drop.toLocaleString()} new connections for a full table and early-dropped ${pressure.stats.earlyDrop.toLocaleString()}.`
          : pressure.statsError
            ? `The kernel's statistics could not be read: ${pressure.statsError}.`
            : ""}{" "}
        {b.error ? `The table could not be read in full: ${b.error}. ` : ""}
        {pressure.basis} Read {relativeTime(pressure.checkedAt)}.
      </p>
    </div>
  )
}
