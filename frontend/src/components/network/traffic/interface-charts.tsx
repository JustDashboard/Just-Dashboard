"use client"

import { useMemo, useState } from "react"
import { get } from "@/lib/api"
import { bytes, rate } from "@/lib/format"
import type { NetworkHistory, NetworkLink, NetworkLivePoint } from "@/lib/types"
import {
  localInput,
  localInstant,
  packetRate,
  rangeProblem,
  type Percentiles,
  type TrafficAnnotation,
} from "@/lib/network-traffic"
import { usePoll } from "@/hooks/use-poll"
import { useNow } from "@/components/deploy/vocabulary"
import { useAuth } from "@/hooks/use-auth"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Field } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { LinkGlyph, ROLE_LABEL } from "@/components/network/marks"
import { RX, TX } from "@/components/network/rate-pair"

export type TrafficWindow = "live" | "1h" | "6h" | "24h" | "7d"
/** An explicit recorded range, in unix seconds: typed, or dragged on a chart. */
export type TrafficRange = { from: number; to: number }
export type TrafficSpan = TrafficWindow | TrafficRange

export const WINDOWS: { key: TrafficWindow; label: string }[] = [
  { key: "live", label: "Live" },
  { key: "1h", label: "1h" },
  { key: "6h", label: "6h" },
  { key: "24h", label: "24h" },
  { key: "7d", label: "7d" },
]

const WINDOW_SECONDS: Record<Exclude<TrafficWindow, "live">, number> = {
  "1h": 3600,
  "6h": 6 * 3600,
  "24h": 86400,
  "7d": 7 * 86400,
}

export const isRange = (span: TrafficSpan): span is TrafficRange => typeof span === "object"

/** The span as an explicit window ending now (or at its own end), in unix seconds. */
export function spanBounds(span: TrafficSpan, now: number): TrafficRange {
  if (isRange(span)) return span
  if (span === "live") return { from: now - 15 * 60, to: now }
  return { from: now - WINDOW_SECONDS[span], to: now }
}

/** The named window that covers a span back from now, for readings that only take one. */
export function coveringWindow(span: TrafficSpan, now: number): Exclude<TrafficWindow, "live"> {
  if (!isRange(span)) return span === "live" ? "1h" : span
  const back = now - span.from
  return (["1h", "6h", "24h", "7d"] as const).find((w) => WINDOW_SECONDS[w] >= back) ?? "7d"
}

const rangeLabel = (r: TrafficRange) =>
  `${new Date(r.from * 1000).toLocaleString(undefined, { dateStyle: "short", timeStyle: "short" })} – ${new Date(r.to * 1000).toLocaleString(undefined, { dateStyle: "short", timeStyle: "short" })}`

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

/**
 * The window every chart on the page follows: the live ring, a named recorded
 * window, or a range typed here or dragged on a chart. A range stays until
 * another window is chosen, and says what it is in the picker.
 */
export function WindowPicker({
  value,
  onChange,
}: {
  value: TrafficSpan
  onChange: (next: TrafficSpan) => void
}) {
  const [typing, setTyping] = useState(false)
  const selected = isRange(value) || typing ? "custom" : value
  return (
    <span className="flex flex-col items-end gap-2">
      <ToggleGroup
        type="single"
        value={selected}
        onValueChange={(next) => {
          // Pressing the chosen Range again reopens its form to change it.
          if (!next) {
            if (isRange(value)) setTyping(true)
            return
          }
          if (next === "custom") {
            setTyping(true)
            return
          }
          setTyping(false)
          onChange(next as TrafficWindow)
        }}
        variant="outline"
        size="sm"
        aria-label="How far back the charts reach"
      >
        {WINDOWS.map((w) => (
          <ToggleGroupItem key={w.key} value={w.key} className="px-2.5 text-hint">
            {w.label}
          </ToggleGroupItem>
        ))}
        <ToggleGroupItem value="custom" className="px-2.5 text-hint">
          Range
        </ToggleGroupItem>
      </ToggleGroup>
      {isRange(value) && !typing && (
        <span className="numeric text-hint text-muted-foreground" aria-label="Chosen range">
          {rangeLabel(value)}
        </span>
      )}
      {typing && (
        <RangeForm
          initial={isRange(value) ? value : undefined}
          onApply={(range) => {
            setTyping(false)
            onChange(range)
          }}
          onCancel={() => setTyping(false)}
        />
      )}
    </span>
  )
}

function RangeForm({
  initial,
  onApply,
  onCancel,
}: {
  initial?: TrafficRange
  onApply: (range: TrafficRange) => void
  onCancel: () => void
}) {
  const now = Math.floor(useNow(60_000) / 1000)
  const [from, setFrom] = useState(() =>
    localInput(initial?.from ?? Math.floor(Date.now() / 1000) - 3 * 3600),
  )
  const [to, setTo] = useState(() => localInput(initial?.to ?? Math.floor(Date.now() / 1000)))
  const start = localInstant(from)
  const end = localInstant(to)
  const problem = rangeProblem(start, end, now)
  return (
    <form
      className="flex flex-wrap items-end justify-end gap-2"
      aria-label="Recorded range"
      onSubmit={(event) => {
        event.preventDefault()
        if (!problem && start !== undefined && end !== undefined) {
          onApply({ from: start, to: Math.min(end, now) })
        }
      }}
    >
      <Field label="From" htmlFor="traffic-range-from">
        <Input
          id="traffic-range-from"
          type="datetime-local"
          value={from}
          onChange={(event) => setFrom(event.target.value)}
          className="w-52 font-mono"
        />
      </Field>
      <Field label="To" htmlFor="traffic-range-to" error={problem}>
        <Input
          id="traffic-range-to"
          type="datetime-local"
          value={to}
          onChange={(event) => setTo(event.target.value)}
          className="w-52 font-mono"
        />
      </Field>
      <Button type="button" size="sm" variant="outline" onClick={onCancel}>
        Cancel
      </Button>
      <Button type="submit" size="sm" disabled={Boolean(problem)}>
        Show range
      </Button>
    </form>
  )
}

/**
 * Every device that carries traffic of its own, each its own chart of in and
 * out: the uplink across the page, then the tunnels, the bridges and the
 * cards two to a row. Live is the two-second ring the page already holds,
 * with each device's packets a second and the faults of the last fifteen
 * minutes under it; the recorded windows are the recorded samples, each
 * bucket its mean with its busiest two seconds behind it, the window's 95th
 * percentile as a line across the chart, and — for an administrator — the
 * network changes and saved incidents of the window as marks on the time
 * axis. Dragging across a recorded chart narrows every chart to that span.
 * Docker's veths and the kernel's own devices are left out — every
 * container's traffic is the Containers block's, and loopback's is this
 * machine talking to itself.
 */
export function InterfaceCharts({
  links,
  live,
  span,
  onSpan,
}: {
  links: NetworkLink[]
  live: Record<string, NetworkLivePoint[]>
  span: TrafficSpan
  onSpan: (next: TrafficSpan) => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const devices = links.filter((l) => l.role !== "container" && l.owner !== "kernel" && l.adminUp)
  const recorded = span !== "live"
  const query = isRange(span)
    ? { from: span.from, to: span.to, points: 240 }
    : { window: span, points: 240 }
  const spanKey = isRange(span) ? `${span.from}-${span.to}` : span
  const history = usePoll<NetworkHistory>(
    (signal) => get("/network/traffic/history", query, signal),
    60_000,
    [spanKey],
    { enabled: recorded },
  )
  const annotations = usePoll<TrafficAnnotation[]>(
    (signal) => {
      const now = Math.floor(Date.now() / 1000)
      const bounds = spanBounds(span, now)
      return get("/network/traffic/annotations", { from: bounds.from, to: bounds.to }, signal)
    },
    60_000,
    [spanKey],
    { enabled: admin },
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

  if (recorded && !history.data) {
    return history.error ? (
      <ErrorState error={history.error} onRetry={history.refresh} />
    ) : (
      <LoadingPanel />
    )
  }

  if (recorded && history.data && !history.data.recording) {
    return (
      <Notice title="Nothing is recorded">
        The metrics retention is zero (JD_METRICS_RETENTION), so the server keeps no history of any
        device. Live still shows the last fifteen minutes.
      </Notice>
    )
  }
  const [first, ...rest] = devices
  if (!first) return null
  const events = annotations.data
  const zoom = recorded
    ? (from: number, to: number) =>
        onSpan({ from: Math.floor(from / 1000), to: Math.ceil(to / 1000) })
    : undefined
  const retained = history.data?.retainedFrom
  const chartFor = (link: NetworkLink, height?: number) => (
    <DeviceChart
      key={link.name}
      link={link}
      rows={rowsFor(link.name)}
      span={span}
      height={height}
      livePoints={live[link.name]}
      percentiles={history.data?.percentiles?.[link.name]}
      events={events}
      onZoom={zoom}
    />
  )
  return (
    <div className="flex min-w-0 flex-col gap-8">
      {history.error && <ErrorState error={history.error} onRetry={history.refresh} />}
      {isRange(span) && retained && span.from < retained && (
        <Notice title="The start of this range is no longer kept">
          The metrics retention keeps interface history back to{" "}
          {new Date(retained * 1000).toLocaleString()}; the range before that is empty because it
          was pruned, not because nothing moved.
        </Notice>
      )}
      {chartFor(first, 220)}
      {rest.length > 0 && (
        <div className="grid min-w-0 gap-x-10 gap-y-8 lg:grid-cols-2">
          {rest.map((link) => chartFor(link))}
        </div>
      )}
    </div>
  )
}

/** The last fifteen minutes of a device's faults, both directions. */
function faultsOf(points: NetworkLivePoint[] | undefined) {
  let errors = 0
  let drops = 0
  for (const p of points ?? []) {
    errors += p.err ?? 0
    drops += p.drop ?? 0
  }
  return { errors, drops }
}

function DeviceChart({
  link,
  rows,
  span,
  height = 160,
  livePoints,
  percentiles,
  events,
  onZoom,
}: {
  link: NetworkLink
  rows: { ts: number; rx: number; tx: number }[]
  span: TrafficSpan
  height?: number
  livePoints?: NetworkLivePoint[]
  percentiles?: Percentiles
  events?: TrafficAnnotation[]
  onZoom?: (from: number, to: number) => void
}) {
  const title = link.dockerNetwork ? `${link.name} · ${link.dockerNetwork}` : link.name
  const live = span === "live"
  const thresholds = useMemo(
    () =>
      !live && percentiles && percentiles.samples > 1
        ? [
            { value: percentiles.rxP95, label: `p95 in ${rate(percentiles.rxP95)}` },
            { value: percentiles.txP95, label: `p95 out ${rate(percentiles.txP95)}` },
          ]
        : undefined,
    [live, percentiles],
  )
  const last = livePoints?.at(-1)
  const faults = faultsOf(livePoints)
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
      series={live ? LIVE_SERIES : RECORDED_SERIES}
      format={formatRate}
      axisFormat={axisRate}
      showPeaks={!live}
      height={height}
      thresholds={thresholds}
      events={live ? undefined : events}
      onZoom={onZoom}
      note={
        live
          ? "Collecting — the first readings arrive within a few seconds."
          : "Nothing recorded for this device in this window yet."
      }
      footer={
        live ? (
          last?.rxp !== undefined ? (
            <span
              className="numeric text-hint text-muted-foreground"
              aria-label={`${link.name} packets`}
            >
              {packetRate(last.rxp)} packets in · {packetRate(last.txp)} out
              {faults.errors + faults.drops > 0
                ? ` · ${faults.errors} errors, ${faults.drops} drops in 15 min`
                : " · no errors or drops in 15 min"}
            </span>
          ) : undefined
        ) : percentiles && percentiles.samples > 0 ? (
          <span
            className="numeric text-hint text-muted-foreground"
            aria-label={`${link.name} percentiles`}
          >
            p50 ↓ {rate(percentiles.rxP50)} ↑ {rate(percentiles.txP50)} · p95 ↓{" "}
            {rate(percentiles.rxP95)} ↑ {rate(percentiles.txP95)} · p99 ↓ {rate(percentiles.rxP99)}{" "}
            ↑ {rate(percentiles.txP99)} — over {percentiles.samples} {percentiles.basisSeconds}s
            intervals
          </span>
        ) : undefined
      }
    />
  )
}
