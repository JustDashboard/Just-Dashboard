"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import { bytes, rate } from "@/lib/format"
import type { NetworkHistory, NetworkLink, NetworkLivePoint } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { Notice } from "@/components/state"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { LinkGlyph, ROLE_LABEL } from "@/components/network/marks"
import { RX, TX } from "@/components/network/rate-pair"

export type TrafficWindow = "live" | "1h" | "6h" | "24h" | "7d"

export const WINDOWS: { key: TrafficWindow; label: string }[] = [
  { key: "live", label: "Live" },
  { key: "1h", label: "1h" },
  { key: "6h", label: "6h" },
  { key: "24h", label: "24h" },
  { key: "7d", label: "7d" },
]

const LIVE_SERIES = [
  { key: "rx", label: "In", color: RX, kind: "area" as const },
  { key: "tx", label: "Out", color: TX, kind: "area" as const },
]
const RECORDED_SERIES = [
  { key: "rx", label: "In", color: RX, kind: "area" as const, peakKey: "rxPeak" },
  { key: "tx", label: "Out", color: TX, kind: "area" as const, peakKey: "txPeak" },
]
const formatRate = (value: number) => rate(value)
const axisRate = (value: number) => `${bytes(value, 0)}/s`

export function WindowPicker({
  value,
  onChange,
}: {
  value: TrafficWindow
  onChange: (next: TrafficWindow) => void
}) {
  return (
    <ToggleGroup
      type="single"
      value={value}
      onValueChange={(next) => next && onChange(next as TrafficWindow)}
      variant="outline"
      size="sm"
      aria-label="How far back the charts reach"
    >
      {WINDOWS.map((w) => (
        <ToggleGroupItem key={w.key} value={w.key} className="px-2.5 text-hint">
          {w.label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}

/**
 * Every device that carries traffic of its own, each its own chart of in and
 * out: the uplink across the page, then the tunnels, the bridges and the
 * cards two to a row. Live is the two-second ring the page already holds;
 * the longer windows are the recorded samples, each bucket its mean with its
 * busiest two seconds behind it, so a burst inside a ten-minute bucket is
 * still drawn. Docker's veths and the kernel's own devices are left out —
 * every container's traffic is the Containers block's, and loopback's is
 * this machine talking to itself.
 */
export function InterfaceCharts({
  links,
  live,
  span,
}: {
  links: NetworkLink[]
  live: Record<string, NetworkLivePoint[]>
  span: TrafficWindow
}) {
  const devices = links.filter((l) => l.role !== "container" && l.owner !== "kernel" && l.adminUp)
  const history = usePoll<NetworkHistory>(
    (signal) => get("/network/traffic/history", { window: span, points: 240 }, signal),
    60_000,
    [span],
    { enabled: span !== "live" },
  )

  const rowsFor = useMemo(() => {
    return (name: string) => {
      if (span === "live") {
        return (live[name] ?? []).map((p) => ({ ts: p.t * 1000, rx: p.rx, tx: p.tx }))
      }
      return (history.data?.interfaces[name] ?? []).map((p) => ({
        ts: p.t * 1000,
        rx: p.rx,
        tx: p.tx,
        rxPeak: p.rxPeak,
        txPeak: p.txPeak,
      }))
    }
  }, [span, live, history.data])

  if (span !== "live" && history.data && !history.data.recording) {
    return (
      <Notice title="Nothing is recorded">
        The metrics retention is zero (JD_METRICS_RETENTION), so the server keeps no history of
        any device. Live still shows the last fifteen minutes.
      </Notice>
    )
  }
  const [first, ...rest] = devices
  if (!first) return null
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <DeviceChart link={first} rows={rowsFor(first.name)} span={span} height={220} />
      {rest.length > 0 && (
        <div className="grid min-w-0 gap-x-10 gap-y-8 lg:grid-cols-2">
          {rest.map((link) => (
            <DeviceChart key={link.name} link={link} rows={rowsFor(link.name)} span={span} />
          ))}
        </div>
      )}
    </div>
  )
}

function DeviceChart({
  link,
  rows,
  span,
  height = 160,
}: {
  link: NetworkLink
  rows: { ts: number; rx: number; tx: number }[]
  span: TrafficWindow
  height?: number
}) {
  const title = link.dockerNetwork ? `${link.name} · ${link.dockerNetwork}` : link.name
  return (
    <ChartPanel
      plain
      title={title}
      actions={
        <span className="inline-flex items-center gap-1.5 text-hint text-muted-foreground">
          <LinkGlyph kind={link.kind} className="size-3.5" />
          {ROLE_LABEL[link.role]}
        </span>
      }
      rows={rows}
      series={span === "live" ? LIVE_SERIES : RECORDED_SERIES}
      format={formatRate}
      axisFormat={axisRate}
      showPeaks={span !== "live"}
      height={height}
      note={
        span === "live"
          ? "Collecting — the first readings arrive within a few seconds."
          : "Nothing recorded for this device in this window yet."
      }
    />
  )
}
