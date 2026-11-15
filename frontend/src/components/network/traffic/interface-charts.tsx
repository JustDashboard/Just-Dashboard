"use client"

import Link from "next/link"
import { useEffect, useMemo, useState } from "react"
import { get } from "@/lib/api"
import { bytes, dateFormatter, rate } from "@/lib/format"
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
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { ChartActivity } from "@/components/icons"
import { Field } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { LinkGlyph, ROLE_LABEL } from "@/components/network/marks"
import { RX, TX } from "@/components/network/rate-pair"
import { Skeleton } from "@/components/ui/skeleton"
import { useViewState } from "@/lib/view-state"
import { cn } from "@/lib/utils"
import {
  WINDOWS,
  isRange,
  spanBounds,
  type TrafficRange,
  type TrafficSpan,
  type TrafficWindow,
} from "./span"
import { thresholdWords, throughputWords } from "./words"

const rangeEnd = dateFormatter({ dateStyle: "short", timeStyle: "short" })
const rangeLabel = (r: TrafficRange) => `${rangeEnd(r.from * 1000)} – ${rangeEnd(r.to * 1000)}`

const LIVE_SERIES = [
  { key: "rx", label: "In", color: RX, kind: "area" as const },
  { key: "tx", label: "Out", color: TX, kind: "area" as const },
]
const RECORDED_SERIES = [
  { key: "rx", label: "In", color: RX, kind: "area" as const, peakKey: "rxPeak" },
  { key: "tx", label: "Out", color: TX, kind: "area" as const, peakKey: "txPeak" },
]
const formatRate = (value: number) => rate(value)

type DeviceRow = { ts: number; rx: number; tx: number; rxPeak?: number; txPeak?: number }
const NO_ROWS: DeviceRow[] = []
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
 * bucket its mean with its busiest two seconds behind it, the rate each
 * direction stayed under nineteen times in twenty as a line across the chart,
 * and — for an administrator — the network changes and saved incidents of the
 * window as marks on the time axis. Dragging across a recorded chart narrows
 * every chart to that span. Docker's veths and the kernel's own devices are
 * left out — every container's traffic is the Containers block's, and
 * loopback's is this machine talking to itself.
 *
 * It holds still (§11 of the rubric: D11). Before the devices are known it
 * draws as many blocks as it drew last time, and a block is one height
 * whether it is waiting, collecting or drawn: the page was a grey panel that
 * became nine charts, and each chart grew by its legend when its first
 * reading landed.
 */
export function InterfaceCharts({
  links,
  live,
  span,
  onSpan,
}: {
  /** Undefined until the first inventory lands. */
  links: NetworkLink[] | undefined
  live: Record<string, NetworkLivePoint[]>
  span: TrafficSpan
  onSpan: (next: TrafficSpan) => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const devices = links?.filter((l) => l.role !== "container" && l.owner !== "kernel" && l.adminUp)
  const [remembered, remember] = useViewState("network.traffic.devices", 5)
  useEffect(() => {
    if (devices && devices.length !== remembered) remember(devices.length)
  }, [devices, remembered, remember])
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

  // One array per device per reading, not per render: a chart is redrawn when
  // its rows are another array (`ChartPanel` is memoised), and this page
  // renders on every frame of the live ring whichever window is on screen. On
  // a recorded window the rows change once a minute, with the record.
  const liveRows = useMemo(() => {
    const out = new Map<string, DeviceRow[]>()
    if (recorded) return out
    for (const [name, points] of Object.entries(live)) {
      out.set(
        name,
        points.map((p) => ({ ts: p.t * 1000, rx: p.rx, tx: p.tx })),
      )
    }
    return out
  }, [recorded, live])
  const recordedRows = useMemo(() => {
    const out = new Map<string, DeviceRow[]>()
    if (!recorded) return out
    for (const [name, points] of Object.entries(history.data?.interfaces ?? {})) {
      out.set(
        name,
        points.map((p) => ({
          ts: p.t * 1000,
          rx: p.rx,
          tx: p.tx,
          rxPeak: p.rxPeak,
          txPeak: p.txPeak,
        })),
      )
    }
    return out
  }, [recorded, history.data])
  const rowsFor = (name: string) => (recorded ? recordedRows : liveRows).get(name) ?? NO_ROWS

  if (!devices) {
    return (
      <ChartGrid>
        {Array.from({ length: Math.max(1, remembered) }, (_, i) => (
          <ChartGhost key={i} lead={i === 0} recorded={recorded} />
        ))}
      </ChartGrid>
    )
  }
  if (recorded && !history.data && history.error) {
    return <ErrorState error={history.error} onRetry={history.refresh} retryable />
  }
  if (recorded && history.data && !history.data.recording) {
    return (
      <EmptyState
        icon={ChartActivity}
        title="Nothing is recorded"
        description={
          <>
            The metrics retention is zero (<span className="font-mono">JD_METRICS_RETENTION</span>),
            so the server keeps no history of any device. Live still shows the last fifteen minutes.
          </>
        }
        action={
          <Button size="sm" onClick={() => onSpan("live")}>
            Show live
          </Button>
        }
      />
    )
  }
  if (devices.length === 0) {
    return (
      <EmptyState
        icon={ChartActivity}
        title="No device is up"
        description="Every device that carries traffic of its own is down, or belongs to a container."
        action={
          <Link
            href="/network/interfaces"
            className="text-body font-medium text-brand hover:underline"
          >
            Interfaces
          </Link>
        }
      />
    )
  }
  const events = annotations.data
  const zoom = recorded
    ? (from: number, to: number) =>
        onSpan({ from: Math.floor(from / 1000), to: Math.ceil(to / 1000) })
    : undefined
  const retained = history.data?.retainedFrom
  const reading = recorded && !history.data
  return (
    <div className="flex min-w-0 flex-col gap-8">
      {history.data && history.error && (
        <ErrorState error={history.error} onRetry={history.refresh} retryable />
      )}
      {isRange(span) && retained && span.from < retained && (
        <Notice title="The start of this range is no longer kept">
          The metrics retention keeps interface history back to{" "}
          {new Date(retained * 1000).toLocaleString()}; the range before that is empty because it
          was pruned, not because nothing moved.
        </Notice>
      )}
      <ChartGrid>
        {devices.map((link, i) => (
          <DeviceChart
            key={link.name}
            link={link}
            rows={rowsFor(link.name)}
            span={span}
            reading={reading}
            lead={i === 0}
            livePoints={live[link.name]}
            percentiles={history.data?.percentiles?.[link.name]}
            events={events}
            onZoom={zoom}
          />
        ))}
      </ChartGrid>
    </div>
  )
}

const LEAD_HEIGHT = 220
const CHART_HEIGHT = 160

/**
 * What a block stands on besides its plot (`--plot`), and so how tall it is
 * held in every state: the title line, the legend's three lines and the line
 * under it, which is one line on the live window and the window's spread on
 * a recorded one. The spread wraps between its four parts and never inside
 * one: a phone's column takes one part to a line, half the page two, the
 * whole page all four, and each is given that room before it has anything to
 * say, so a first reading fills its block instead of stretching it.
 */
function blockHeight(lead: boolean, recorded: boolean) {
  if (!recorded) return "min-h-[calc(var(--plot)+157px)]"
  return lead
    ? "min-h-[calc(var(--plot)+207px)] sm:min-h-[calc(var(--plot)+174px)] xl:min-h-[calc(var(--plot)+157px)]"
    : "min-h-[calc(var(--plot)+207px)] xl:min-h-[calc(var(--plot)+174px)]"
}
const spreadHeight = (lead: boolean) =>
  lead ? "min-h-[4lh] sm:min-h-[2lh] xl:min-h-[1lh]" : "min-h-[4lh] xl:min-h-[2lh]"
const plot = (lead: boolean) =>
  ({ "--plot": `${lead ? LEAD_HEIGHT : CHART_HEIGHT}px` }) as React.CSSProperties

/** The uplink across the page, then two to a row. */
function ChartGrid({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid min-w-0 gap-x-10 gap-y-8 lg:grid-cols-2 lg:*:first:col-span-2">
      {children}
    </div>
  )
}

/** A device's block before the devices are known. */
function ChartGhost({ lead, recorded }: { lead: boolean; recorded: boolean }) {
  return (
    <div
      data-slot="device-chart"
      aria-hidden
      className={cn("flex min-w-0 flex-col", blockHeight(lead, recorded))}
      style={plot(lead)}
    >
      <div className="flex items-center justify-between border-b border-hairline pb-3">
        <Skeleton className="h-4 w-28" />
        <Skeleton className="h-3.5 w-16" />
      </div>
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
  reading,
  lead,
  livePoints,
  percentiles,
  events,
  onZoom,
}: {
  link: NetworkLink
  rows: { ts: number; rx: number; tx: number }[]
  span: TrafficSpan
  /** The recorded window has not answered yet. */
  reading: boolean
  /** The first device, drawn across the page and taller. */
  lead: boolean
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
            { value: percentiles.rxP95, label: thresholdWords("In", percentiles.rxP95) },
            { value: percentiles.txP95, label: thresholdWords("Out", percentiles.txP95) },
          ]
        : undefined,
    [live, percentiles],
  )
  const actions = useMemo(
    () => (
      <span className="inline-flex items-center gap-1.5 text-hint text-muted-foreground">
        <LinkGlyph kind={link.kind} className="size-3.5" />
        {ROLE_LABEL[link.role]}
      </span>
    ),
    [link.kind, link.role],
  )
  const last = livePoints?.at(-1)
  const faults = faultsOf(livePoints)
  return (
    <div
      data-slot="device-chart"
      className={cn("flex min-w-0 flex-col", blockHeight(lead, !live))}
      style={plot(lead)}
    >
      <ChartPanel
        plain
        title={title}
        actions={actions}
        rows={rows}
        series={live ? LIVE_SERIES : RECORDED_SERIES}
        format={formatRate}
        axisFormat={axisRate}
        showPeaks={!live}
        height={lead ? LEAD_HEIGHT : CHART_HEIGHT}
        thresholds={thresholds}
        events={live ? undefined : events}
        onZoom={onZoom}
        note={
          live
            ? "Collecting — the first readings arrive within a few seconds."
            : reading
              ? "Reading the recorded samples…"
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
              className={cn(
                "numeric flex flex-wrap content-start gap-x-4 text-hint text-muted-foreground",
                spreadHeight(lead),
              )}
              aria-label={`${link.name} throughput spread`}
            >
              {throughputWords(percentiles).map((part) => (
                <span key={part} className="whitespace-nowrap">
                  {part}
                </span>
              ))}
            </span>
          ) : undefined
        }
      />
    </div>
  )
}
