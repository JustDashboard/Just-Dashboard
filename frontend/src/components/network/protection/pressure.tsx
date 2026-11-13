"use client"

import { useMemo } from "react"
import { relativeTime } from "@/lib/format"
import type { Conntrack, PressureCount, ProtectionPressure } from "@/lib/types"
import type { PollState } from "@/hooks/use-poll"
import { Warning } from "@/components/icons"
import { BarReading, type ReadingPart } from "@/components/bar-reading"
import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { RankList, RankSkeleton, type RankItem } from "@/components/rank-list"
import { ErrorState, Notice } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { ChartPanel } from "@/components/metrics/chart-panel"
import type { Series } from "@/components/metrics/metric-chart"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { levelTone, seriesRows } from "@/components/network/protection/reading"

const TABLE_SERIES: Series[] = [
  { key: "conntrack:count", label: "Tracked", color: "var(--chart-1)", kind: "area" },
  { key: "conntrack:max", label: "Maximum", color: "var(--chart-5)" },
]
const DROP_SERIES: Series[] = [
  { key: "conntrack:drop", label: "Dropped", color: "var(--chart-3)" },
  { key: "conntrack:early_drop", label: "Early-dropped", color: "var(--chart-4)" },
  { key: "conntrack:insert_failed", label: "Failed inserts", color: "var(--chart-2)" },
]
/** The states the band draws, each on a chart hue of its own; the rest are one muted span. */
const STATE_HUES = [
  "var(--chart-1)",
  "var(--chart-3)",
  "var(--chart-2)",
  "var(--chart-4)",
  "var(--chart-5)",
] as const
const whole = (v: number) => Math.round(v).toLocaleString()

/**
 * A breakdown's rows as figures in columns: the count, and the share of the
 * entries read that the server worked out, which is the whole each is part of.
 */
function ranks(counts: PressureCount[], mono = true): RankItem[] {
  return counts.map((c) => ({
    key: c.key,
    label: c.key,
    mono,
    value: c.count.toLocaleString(),
    share: c.share,
  }))
}

const RANKS: {
  title: string
  label: string
  of: (b: ProtectionPressure["breakdown"]) => PressureCount[]
  mono: boolean
}[] = [
  { title: "By state", label: "By state", of: (b) => b.byState, mono: false },
  { title: "Busiest sources", label: "By source", of: (b) => b.topSources, mono: true },
  { title: "Busiest destination ports", label: "By port", of: (b) => b.topPorts, mono: true },
  { title: "By protocol", label: "By protocol", of: (b) => b.byProtocol, mono: false },
]

/**
 * Why the connection table is filling, not only how full it is.
 *
 * It opens on the table as a band: what it tracks against what it can, split
 * by the states that fill it (a table half full of `SYN_RECV` reads as one
 * before any sentence says so), in its level's colour when it runs short.
 * Then the last day of its count against its maximum and the kernel's own
 * refusals when it was full, recorded every minute; what it holds now by
 * state, source, destination port and protocol, read over netlink and
 * bounded; and last what those shares point at. The causes are indications:
 * a SYN flood and a crowd of slow clients look alike from here.
 *
 * It ended on a paragraph of four sentences. Its figures are the line under
 * the band, the basis is behind the ⓘ, and a read that came up short is a
 * notice.
 *
 * Every block has its height before the read lands: the band is drawn from
 * the page's own reading of the table, and the charts, lists and findings
 * stand grey where they will be.
 */
export function ConnectionTable({
  table,
  read,
}: {
  /** The page's own reading of the table, there before the breakdown is. */
  table: Conntrack
  read: PollState<ProtectionPressure>
}) {
  const pressure = read.data
  const tableRows = useMemo(
    () => seriesRows(pressure?.series ?? {}, ["conntrack:count", "conntrack:max"]),
    [pressure?.series],
  )
  const dropRows = useMemo(
    () =>
      seriesRows(pressure?.series ?? {}, [
        "conntrack:drop",
        "conntrack:early_drop",
        "conntrack:insert_failed",
      ]),
    [pressure?.series],
  )
  const b = pressure?.breakdown
  const states = (b?.byState ?? []).slice(0, STATE_HUES.length)
  const named = states.reduce((n, s) => n + s.count, 0)
  const parts: ReadingPart[] = states.map((s, place) => ({
    key: s.key,
    value: s.count,
    color: STATE_HUES[place],
    mark: (
      <span aria-hidden className="size-2 rounded-full" style={{ background: STATE_HUES[place] }} />
    ),
    name: s.key,
    figure: `${s.count.toLocaleString()} · ${Math.round(s.share * 100)}%`,
    label: `${s.key} ${s.count.toLocaleString()}`,
  }))
  const tone = levelTone(table.level)

  if (!pressure && read.error)
    return (
      <div aria-label="Connection table pressure">
        <ErrorState error={read.error} onRetry={read.refresh} />
      </div>
    )

  return (
    <div className="flex min-w-0 flex-col gap-8" aria-label="Connection table pressure">
      <div className="min-w-0 space-y-2">
        <BarReading
          title="Connection table"
          label="How full the connection table is"
          list="States that fill the table"
          fixed
          parts={parts}
          capacity={table.available ? table.max : undefined}
          rest={table.available ? Math.max(table.count - named, 0) : 0}
          restLabel="Other states"
          free="Free"
          format={whole}
          figure={
            <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
              {table.available ? (
                <>
                  <span
                    className={
                      tone === "danger"
                        ? "font-medium text-destructive"
                        : tone === "warning"
                          ? "font-medium text-warning"
                          : "font-medium text-foreground"
                    }
                  >
                    {table.percent}% full
                  </span>
                  · {table.count.toLocaleString()} of {table.max.toLocaleString()}
                </>
              ) : (
                "connection tracking is not loaded"
              )}
            </span>
          }
          message={
            !pressure ? (
              <TextShimmer>Reading the table…</TextShimmer>
            ) : parts.length === 0 ? (
              "Nothing is tracked."
            ) : undefined
          }
        />
        <p className="numeric flex min-h-5 min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
          {pressure && b ? (
            <>
              <span>
                {b.read.toLocaleString()} entries read{b.truncated ? " (the first ones only)" : ""}
              </span>
              <span>{b.unreplied.toLocaleString()} never answered</span>
              <span>{b.assured.toLocaleString()} assured</span>
              <span>read {relativeTime(pressure.checkedAt)}</span>
              <InfoTip label="How the table is read">{pressure.basis}</InfoTip>
            </>
          ) : (
            <Skeleton className="h-3 w-72 max-w-full" />
          )}
        </p>
        {pressure?.statsError && (
          <Notice tone="warning" icon={Warning} title="The kernel's statistics could not be read">
            {pressure.statsError}
          </Notice>
        )}
        {b?.error && (
          <Notice tone="warning" icon={Warning} title="The table could not be read in full">
            {b.error}
          </Notice>
        )}
      </div>

      <div className="grid gap-8 lg:grid-cols-2 [&>*]:min-h-[17rem] [&>*]:min-w-0">
        <ChartPanel
          plain
          title="Tracked connections, last day"
          rows={tableRows}
          series={TABLE_SERIES}
          format={whole}
          showPeaks={false}
          note={
            pressure
              ? "Nothing recorded yet: the recorder reads the table every minute."
              : "Reading…"
          }
          height={160}
        />
        <ChartPanel
          plain
          title="Refused by a full table"
          actions={
            pressure?.stats && (
              <span className="numeric text-hint text-muted-foreground">
                since boot {pressure.stats.drop.toLocaleString()} dropped ·{" "}
                {pressure.stats.earlyDrop.toLocaleString()} early-dropped
              </span>
            )
          }
          rows={dropRows}
          series={DROP_SERIES}
          format={whole}
          showPeaks={false}
          note={
            pressure ? "The kernel refused nothing for a full table in the last day." : "Reading…"
          }
          height={160}
        />
      </div>

      <div className="grid gap-6 md:grid-cols-2 xl:grid-cols-4 [&>*]:min-w-0">
        {RANKS.map((rank) => (
          <section key={rank.label} aria-label={rank.label} className="space-y-1.5">
            <p className="text-hint font-medium">{rank.title}</p>
            {b ? (
              <RankList items={ranks(rank.of(b), rank.mono)} emptyLabel="Nothing tracked" />
            ) : (
              <RankSkeleton rows={3} />
            )}
          </section>
        ))}
      </div>

      <Panel plain aria-label="What the shares point at">
        <PanelHeader
          title="What the shares point at"
          actions={
            pressure && (
              <span className="numeric text-hint text-muted-foreground">
                {pressure.causes.length}
              </span>
            )
          }
        />
        <PanelBody flush>
          {!pressure ? (
            <Skeleton className="my-3 h-9 w-full rounded-md" />
          ) : pressure.causes.length === 0 ? (
            <p className="py-3 text-body text-muted-foreground">
              Nothing in the table&rsquo;s shares points at a cause of pressure.
            </p>
          ) : (
            <ul className="divide-y divide-hairline">
              {pressure.causes.map((c) => (
                <li key={c.kind} className="flex min-w-0 items-start gap-3 py-3">
                  <StatusDot
                    tone={c.severity === "warning" ? "warning" : "unknown"}
                    className="mt-1.5"
                  />
                  <div className="min-w-0 flex-1 space-y-0.5">
                    <p className="text-body font-medium">{c.evidence}.</p>
                    <p className="text-hint leading-relaxed text-muted-foreground">{c.message}</p>
                  </div>
                  <span className="eyebrow shrink-0 pt-1">{c.kind}</span>
                </li>
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}
