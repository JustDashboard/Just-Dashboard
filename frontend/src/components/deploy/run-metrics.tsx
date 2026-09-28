"use client"

import { Fragment } from "react"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Sparkline } from "@/components/metrics/sparkline"
import { ProductGlyph } from "@/components/product-logo"
import { serviceProduct } from "@/components/deploy/service-product"
import { bytes, percent } from "@/lib/format"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentSourceKind } from "@/lib/types"
import { RunTrafficPanel } from "@/components/deploy/run-traffic"

type MetricPoint = {
  ts?: string
  samples: number
  cpu: number
  cpuPeak: number
  mem: number
  memPeak: number
  memBytes?: number
  memBytesPeak?: number
}
type MetricSeries = { containerId: string; points: MetricPoint[] }
type MetricWindow = {
  releaseId: number
  status: "available" | "partial" | "unavailable"
  reason?: string
  history?: { series: MetricSeries[] }
  /** Which container each series is, for a single-container release; a Compose release has none. */
  sources?: { containerId: string; name?: string; image?: string }[]
}
type MetricComparison = {
  status: string
  reason?: string
  before: MetricWindow
  after: MetricWindow
  hostBefore?: { points: MetricPoint[] }
  hostAfter?: { points: MetricPoint[] }
  hostReason?: string
}

type Summary = { samples: number; cpu: number; cpuPeak: number; mem: number; memPeak: number }

function summarize(points: MetricPoint[]): Summary | undefined {
  const samples = points.reduce((sum, point) => sum + point.samples, 0)
  if (!samples) return undefined
  return {
    samples,
    cpu: points.reduce((sum, point) => sum + point.cpu * point.samples, 0) / samples,
    cpuPeak: Math.max(...points.map((point) => point.cpuPeak)),
    mem: points.reduce((sum, point) => sum + point.mem * point.samples, 0) / samples,
    memPeak: Math.max(...points.map((point) => point.memBytesPeak ?? 0)),
  }
}

function seriesOf(window: MetricWindow) {
  return window.history?.series ?? []
}

/** The container a series is, where the release recorded it: its name and its image. */
function sourceOf(window: MetricWindow, containerId: string) {
  return window.sources?.find((source) => source.containerId === containerId)
}

/**
 * What the release looked like in the ten minutes before it took over and
 * the ten after: the figures, and the shape of them.
 *
 * The figures are one row of comparisons — each reading before, an arrow,
 * then after — rather than a row of three tiles per window that the reader
 * had to hold side by side and subtract, which is what the sentence under
 * them used to do for them. A reading goes amber when it is half again what
 * it was, the traffic panel's own rule for "slower".
 *
 * The shape is one strip per measure: the window before and the window after
 * at equal widths (both are ten minutes) on one shared scale, with a brand
 * rule between them where the release went live — the instant being compared
 * — so a line that sits higher after it is higher in fact and not just drawn
 * taller. The comparison is made here rather than on the Metrics page, which
 * cannot know which minute the release went live.
 */
export function RunMetrics({
  projectId,
  runId,
  releaseNumbers,
  kind,
  product,
}: {
  projectId: number
  runId: number
  /** Release ids to the numbers a reader knows them by, for the window heads. */
  releaseNumbers?: Map<number, number>
  /**
   * Where the project's source comes from and what the project is, so a
   * container of its own build is drawn as the project, as Runtime draws it.
   */
  kind?: DeploymentSourceKind
  product?: string
}) {
  const productOf = (image?: string) => serviceProduct(image, kind, product)
  const result = usePoll(
    (signal) =>
      get<MetricComparison>(`/deploy/${projectId}/runs/${runId}/metrics`, undefined, signal),
    30000,
    [projectId, runId],
  )
  const data = result.data

  return (
    <div className="space-y-8">
      <RunTrafficPanel projectId={projectId} runId={runId} />
      <Panel plain>
        <PanelHeader title="Metrics around activation" />
        <PanelBody flush className="pt-3">
          {result.error && !data ? (
            <ErrorState error={result.error} />
          ) : !data ? (
            <LoadingRows rows={3} />
          ) : data.status !== "available" ? (
            <EmptyNote className="px-0 text-left">
              {data.reason || "No metrics comparison for this run."}
            </EmptyNote>
          ) : (
            <Comparison data={data} releaseNumbers={releaseNumbers} productOf={productOf} />
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}

function Comparison({
  data,
  releaseNumbers,
  productOf,
}: {
  data: MetricComparison
  releaseNumbers?: Map<number, number>
  productOf: (image?: string) => string
}) {
  const before = seriesOf(data.before)
  const after = seriesOf(data.after)
  const everyPoint = [...before, ...after].flatMap((series) => series.points)
  const scale = {
    cpu: Math.max(1, ...everyPoint.map((point) => point.cpuPeak)),
    mem: Math.max(1, ...everyPoint.map((point) => point.memBytesPeak ?? point.memBytes ?? 0)),
  }
  // A release's containers are new containers, so the two windows share no
  // ids: they are paired in the order the release recorded them.
  const pairs = Array.from({ length: Math.max(before.length, after.length) }, (_, index) => {
    const id = after[index]?.containerId ?? before[index]?.containerId ?? String(index)
    const source = sourceOf(data.after, id)
    return {
      key: id,
      name: source?.name || id.slice(0, 12),
      image: source?.image,
      before: before[index],
      after: after[index],
    }
  })
  // Several containers — a Compose release — each get named; one needs no name.
  const named = pairs.length > 1
  const rows = pairs.length * 2 + 2

  return (
    <div className="animate-rise space-y-6">
      {pairs.map((pair) => (
        <section
          key={pair.key}
          aria-label={named ? pair.name : "Readings around activation"}
          className="min-w-0 space-y-2"
        >
          {named && (
            <p className="flex min-w-0 items-center gap-1.5 text-xs font-medium">
              <ProductGlyph id={productOf(pair.image)} />
              <span className="truncate font-mono">{pair.name}</span>
            </p>
          )}
          <Readings before={pair.before} after={pair.after} />
        </section>
      ))}

      <div className="grid min-w-0 grid-cols-[3.5rem_minmax(0,1fr)_1px_minmax(0,1fr)] items-start gap-x-3 gap-y-2 sm:grid-cols-[4.5rem_minmax(0,1fr)_1px_minmax(0,1fr)] sm:gap-x-4">
        <span />
        <WindowHead
          title="Before activation"
          window={data.before}
          release={releaseNumbers?.get(data.before.releaseId)}
          productOf={productOf}
        />
        {/* The instant being compared: where the release went live. */}
        <span
          aria-hidden
          className="relative h-full w-px bg-brand"
          style={{ gridRow: `1 / span ${rows}`, gridColumn: 3 }}
        >
          <span className="absolute -top-0.5 left-1/2 -translate-x-1/2 -translate-y-full text-micro font-medium text-brand">
            live
          </span>
        </span>
        <WindowHead
          title="After activation"
          window={data.after}
          release={releaseNumbers?.get(data.after.releaseId)}
          productOf={productOf}
        />
        {pairs.map((pair) => (
          <Fragment key={pair.key}>
            <Strip
              label="CPU"
              name={named ? pair.name : undefined}
              color="var(--chart-1)"
              max={scale.cpu}
              before={pair.before?.points.map((point) => point.cpu)}
              after={pair.after?.points.map((point) => point.cpu)}
            />
            <Strip
              label="Memory"
              name={named ? pair.name : undefined}
              color="var(--chart-2)"
              max={scale.mem}
              before={pair.before && memoryOf(pair.before.points)}
              after={pair.after && memoryOf(pair.after.points)}
              missing="No memory samples"
            />
          </Fragment>
        ))}
        <span className="border-t border-hairline pt-2 text-hint text-muted-foreground">Host</span>
        <HostLine summary={summarize(data.hostBefore?.points ?? [])} />
        <HostLine summary={summarize(data.hostAfter?.points ?? [])} />
      </div>

      {data.hostReason && <p className="text-hint text-muted-foreground">{data.hostReason}</p>}
      <p className="text-hint text-muted-foreground">
        These observations do not establish that the release caused a change. Missing samples are
        not zero utilization.
      </p>
    </div>
  )
}

/** One container's three readings, each before and after. */
function Readings({ before, after }: { before?: MetricSeries; after?: MetricSeries }) {
  const was = summarize(before?.points ?? [])
  const now = summarize(after?.points ?? [])
  return (
    <StatGrid columns={3} dense>
      <StatTile
        label="CPU mean"
        value={<Change before={was && percent(was.cpu, 0)} after={now && percent(now.cpu, 0)} />}
        tone={grew(was?.cpu, now?.cpu) ? "warning" : "default"}
        hint={now && `${now.samples} samples`}
      />
      <StatTile
        label="CPU peak"
        value={
          <Change before={was && percent(was.cpuPeak, 0)} after={now && percent(now.cpuPeak, 0)} />
        }
        tone={grew(was?.cpuPeak, now?.cpuPeak) ? "warning" : "default"}
      />
      <StatTile
        label="Memory peak"
        value={<Change before={was && bytes(was.memPeak)} after={now && bytes(now.memPeak)} />}
        tone={grew(was?.memPeak, now?.memPeak) ? "warning" : "default"}
      />
    </StatGrid>
  )
}

/** Half again what it was: the traffic panel's "slower" rule, for a cost. */
function grew(before: number | undefined, after: number | undefined) {
  return before !== undefined && after !== undefined && before > 0 && after >= before * 1.5
}

/** Before → after, at the figure's own size, so the arrow is the reading. */
function Change({ before, after }: { before?: string; after?: string }) {
  return (
    // One line at every width: on a tile two to a row on a phone the before
    // half steps down to the body size, so the after figure keeps the tile's
    // and never drops under the arrow.
    <span className="numeric inline-flex items-baseline gap-x-1.5 whitespace-nowrap">
      <span className="max-sm:text-body">
        <span className="text-muted-foreground">{before ?? "—"}</span>
        <span className="ml-1.5 text-muted-foreground/60">→</span>
      </span>
      <span className="whitespace-nowrap">{after ?? "—"}</span>
    </span>
  )
}

/** A window's name, the release it covers, the container it watched, and why it is short. */
function WindowHead({
  title,
  window,
  release,
  productOf,
}: {
  title: string
  window: MetricWindow
  release?: number
  productOf: (image?: string) => string
}) {
  const first = seriesOf(window)[0]
  const source = first && sourceOf(window, first.containerId)
  return (
    <div className="min-w-0 space-y-1 pb-1">
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <p className="eyebrow">{title}</p>
        {release !== undefined && (
          <span className="numeric text-hint text-muted-foreground">release #{release}</span>
        )}
        {window.status !== "available" && (
          <Status
            tone={window.status === "unavailable" ? "stopped" : "warning"}
            label={window.status === "unavailable" ? "Unavailable" : "Partial history"}
          />
        )}
      </div>
      {first && (
        <p
          className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground"
          title={first.containerId}
        >
          <ProductGlyph id={productOf(source?.image)} className="size-3" />
          <span className="truncate font-mono">
            {source?.name || first.containerId.slice(0, 12)}
          </span>
        </p>
      )}
      {window.reason && <p className="text-hint text-muted-foreground">{window.reason}</p>}
    </div>
  )
}

/**
 * A window's memory in bytes, or nothing when no sample in it carried a byte
 * count: a series with no numbers is dropped, not drawn flat at zero (§10).
 */
function memoryOf(points: MetricPoint[]) {
  const known = points.flatMap((point) => (point.memBytes ? [point.memBytes] : []))
  return known.length > 0 ? known : undefined
}

/** One measure across the two windows: its name, then the line before and the line after. */
function Strip({
  label,
  name,
  color,
  max,
  before,
  after,
  missing,
}: {
  label: string
  /** The container, where there are several. */
  name?: string
  color: string
  max: number
  before?: number[]
  after?: number[]
  /** What a window with no series at all says, where that is not "too few". */
  missing?: string
}) {
  const line = (values: number[] | undefined, when: string) =>
    !values && missing ? (
      <span className="flex h-9 items-center text-hint text-muted-foreground/60">{missing}</span>
    ) : values && values.length > 1 ? (
      <Sparkline
        values={values}
        max={max}
        color={color}
        width={240}
        height={36}
        className="h-9 w-full"
        label={`${label} ${when} activation`}
      />
    ) : (
      <span className="flex h-9 items-center text-hint text-muted-foreground/60">
        Too few samples to draw
      </span>
    )
  return (
    <>
      <span className="flex h-9 min-w-0 flex-col justify-center text-hint text-muted-foreground">
        <span className="flex items-center gap-1.5">
          <span
            aria-hidden
            className="size-2 shrink-0 rounded-[2px]"
            style={{ background: color }}
          />
          {label}
        </span>
        {name && <span className="truncate font-mono text-micro">{name}</span>}
      </span>
      {line(before, "before")}
      {line(after, "after")}
    </>
  )
}

/** The host around the container: what else the machine was doing in that window. */
function HostLine({ summary }: { summary: Summary | undefined }) {
  return (
    <p className="min-w-0 border-t border-hairline pt-2 text-hint text-muted-foreground">
      {summary
        ? `Host CPU mean ${percent(summary.cpu, 0)} · memory mean ${percent(summary.mem, 0)} · ${summary.samples} samples`
        : "Host history unavailable for this window."}
    </p>
  )
}
