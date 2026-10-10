"use client"

import { ArrowRight, Box } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { useNow } from "@/components/deploy/vocabulary"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, percent, plural } from "@/lib/format"
import type { Snapshot } from "@/lib/types"
import { cn } from "@/lib/utils"
import { cores } from "@/components/procs/shared"
import { ago } from "@/components/procs/units"
import { ShareBar, shade } from "@/components/procs/workloads"
import {
  serviceLane,
  type ServiceChange,
  type ServiceReading,
} from "@/components/docker/stack-service-readings"

/** How many lines Recent keeps; the stack's Events view holds the rest. */
const RECENT = 6

/**
 * What the stack takes of the machine and what just happened to it: its
 * services' processor and memory as spans of one bar the size of the server,
 * beside everything else the server is doing, and the last starts, exits,
 * restart loops and failed checks.
 *
 * It is the Services and PM2 pages' band, drawn for one stack's services:
 * the same bar, the same rank of the measurement's hue, figures that glide
 * to each frame of the containers socket. Recent names each service in its
 * lane, the hue its lines take in the stack's log. A row opens the service's
 * container.
 */
export function StackUsageBand({
  readings,
  changes,
  snapshot,
  eventsRead,
  productOf,
  onOpen,
}: {
  readings: ServiceReading[]
  changes: ServiceChange[]
  snapshot?: Snapshot
  /** Whether the stack's events have been read yet. */
  eventsRead: boolean
  productOf: (service: string) => string | undefined
  onOpen: (service: string) => void
}) {
  const now = useNow(15_000)
  const measured = readings.filter((r) => r.stat)
  const byCPU = measured
    .filter((r) => r.stat!.cpuReady !== false && r.stat!.cpuPercent > 0)
    .sort((a, b) => b.stat!.cpuPercent - a.stat!.cpuPercent)
  const byMemory = measured
    .filter((r) => r.stat!.memUsage > 0)
    .sort((a, b) => b.stat!.memUsage - a.stat!.memUsage)
  const cpuSum = byCPU.reduce((total, r) => total + r.stat!.cpuPercent, 0)
  const memSum = byMemory.reduce((total, r) => total + r.stat!.memUsage, 0)

  // The machine's own readings size the bars: a core is a hundred points of
  // the processor bar, and the memory bar is the host's total with what the
  // rest of the server uses drawn muted beside the stack.
  const coreCount =
    snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || measured[0]?.stat?.hostCpus || 0
  const busy = snapshot?.cpu ? snapshot.cpu.totalPercent * coreCount : undefined
  const memTotal = snapshot?.memory?.total ?? 0
  const memUsed = snapshot?.memory?.used ?? 0
  const running = readings.some((r) => r.state === "running")

  return (
    <div
      data-slot="stack-usage"
      className="grid min-w-0 gap-x-10 gap-y-8 lg:grid-cols-2 xl:grid-cols-3"
    >
      <Panel plain aria-label="Processor by service">
        <PanelHeader
          title="Processor"
          actions={
            coreCount > 0 && (
              <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
                <span className="font-medium text-foreground">
                  <LiveFigure value={cpuSum / 100} decimals={2} />
                </span>
                of {plural(coreCount, "core")}
              </span>
            )
          }
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Processor"
            capacity={coreCount > 0 ? coreCount * 100 : cpuSum}
            rest={busy !== undefined ? Math.max(busy - cpuSum, 0) : 0}
            parts={byCPU.map((r, rank) => ({
              key: r.key,
              value: r.stat!.cpuPercent,
              color: shade(HUE.cpu, rank),
              label: `${r.key} ${cores(r.stat!.cpuPercent)}`,
            }))}
            format={cores}
          />
          {!running ? (
            <Quiet>Nothing in this stack is running.</Quiet>
          ) : measured.length === 0 ? (
            <p className="py-2 text-body">
              <TextShimmer>Waiting for the first reading…</TextShimmer>
            </p>
          ) : byCPU.length === 0 ? (
            <Quiet>No service is using the processor.</Quiet>
          ) : (
            <ul className="-mx-2">
              {byCPU.map((r, rank) => (
                <Line
                  key={r.key}
                  service={r.key}
                  product={productOf(r.key)}
                  onOpen={onOpen}
                  lead={<RankKey color={shade(HUE.cpu, rank)} />}
                  title={`${percent(r.stat!.cpuPercent)} of one core`}
                  figure={
                    r.stat!.cpuPercent / 100 >= 0.005 ? (
                      <LiveFigure
                        value={r.stat!.cpuPercent / 100}
                        decimals={r.stat!.cpuPercent >= 99.5 ? 1 : 2}
                        unit=" cores"
                      />
                    ) : (
                      "idle"
                    )
                  }
                />
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Memory by service">
        <PanelHeader
          title="Memory"
          actions={
            memTotal > 0 && (
              <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
                <span className="font-medium text-foreground">
                  <LiveBytes value={memSum} />
                </span>
                of {bytes(memTotal, 0)}
              </span>
            )
          }
        />
        <PanelBody className="space-y-3 pt-4">
          <ShareBar
            label="Memory"
            capacity={memTotal > 0 ? memTotal : memSum}
            rest={Math.max(memUsed - memSum, 0)}
            parts={byMemory.map((r, rank) => ({
              key: r.key,
              value: r.stat!.memUsage,
              color: shade(HUE.mem, rank),
              label: `${r.key} ${bytes(r.stat!.memUsage)}`,
            }))}
            format={(v) => bytes(v)}
          />
          {byMemory.length === 0 ? (
            <Quiet>
              {running ? "No memory is reported yet." : "Nothing in this stack is running."}
            </Quiet>
          ) : (
            <ul className="-mx-2">
              {byMemory.map((r, rank) => {
                const limited = r.stat!.memLimited || (r.container?.memoryLimit ?? 0) > 0
                return (
                  <Line
                    key={r.key}
                    service={r.key}
                    product={productOf(r.key)}
                    onOpen={onOpen}
                    lead={<RankKey color={shade(HUE.mem, rank)} />}
                    detail={
                      limited ? (
                        <span className={cn(r.stat!.memPercent >= 85 && "text-warning")}>
                          {percent(r.stat!.memPercent, 0)} of its limit
                        </span>
                      ) : undefined
                    }
                    title={
                      memTotal > 0
                        ? `${bytes(r.stat!.memUsage)} — ${percent((r.stat!.memUsage / memTotal) * 100)} of the server`
                        : bytes(r.stat!.memUsage)
                    }
                    figure={<LiveBytes value={r.stat!.memUsage} />}
                  />
                )
              })}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Recent changes" className="lg:col-span-2 xl:col-span-1">
        <PanelHeader
          title="Recent"
          actions={
            changes.length > 0 && (
              <span className="numeric flex h-7 items-center text-hint text-muted-foreground">
                {plural(changes.length, "change")}
              </span>
            )
          }
        />
        <PanelBody className="pt-3">
          {!eventsRead ? (
            <p className="py-2 text-body">
              <TextShimmer>Reading what Docker did…</TextShimmer>
            </p>
          ) : changes.length === 0 ? (
            <Quiet>
              Nothing has started, stopped or failed since Docker&apos;s events were kept.
            </Quiet>
          ) : (
            <ul className="-mx-2">
              {changes.slice(0, RECENT).map((change) => (
                <Line
                  key={change.key}
                  service={change.service}
                  product={productOf(change.service)}
                  onOpen={onOpen}
                  lane
                  lead={<StatusDot tone={change.tone} className="mx-px" />}
                  title={new Date(change.at).toLocaleString()}
                  detail={
                    <span
                      className={cn(
                        change.tone === "danger" && "text-destructive",
                        change.tone === "warning" && "text-warning",
                      )}
                    >
                      {change.verb}
                    </span>
                  }
                  figure={
                    <span className="text-hint font-normal text-muted-foreground">
                      {ago(change.at / 1000, now)}
                    </span>
                  }
                />
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}

function Quiet({ children }: { children: React.ReactNode }) {
  return <p className="py-2 text-body text-muted-foreground">{children}</p>
}

/** The span's key: the rank's step of its measurement's hue, so a row finds itself on the bar. */
function RankKey({ color }: { color: string }) {
  return (
    <span aria-hidden className="h-2.5 w-0.5 shrink-0 rounded-full" style={{ background: color }} />
  )
}

/**
 * One service on a line: what finds it on the bar or in time, its mark and
 * name, a detail, and its figure. A press opens the service's container; the
 * arrow is what says so, revealed under the pointer and always drawn on a
 * touch screen.
 */
function Line({
  service,
  product,
  lead,
  detail,
  figure,
  title,
  lane,
  onOpen,
}: {
  service: string
  product?: string
  lead: React.ReactNode
  detail?: React.ReactNode
  figure: React.ReactNode
  title?: string
  /** Draws the name in its lane, as the stack's log does. */
  lane?: boolean
  onOpen: (service: string) => void
}) {
  return (
    <li>
      <button
        type="button"
        aria-label={`Open ${service}`}
        title={title}
        onClick={() => onOpen(service)}
        className="group flex h-8 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors hover:bg-row-hover"
      >
        {lead}
        <span className="flex size-4 shrink-0 items-center justify-center">
          {hasProductLogo(product) ? (
            <ProductGlyph id={product} className="size-3.5" />
          ) : (
            <Box aria-hidden className="size-3.5 text-muted-foreground" />
          )}
        </span>
        <span
          className="min-w-0 truncate font-medium"
          style={lane ? { color: serviceLane(service) } : undefined}
        >
          {service}
        </span>
        {detail && (
          <span className="numeric min-w-0 shrink truncate text-hint text-muted-foreground">
            {detail}
          </span>
        )}
        <span className="numeric ml-auto shrink-0 font-medium text-foreground">{figure}</span>
        <ArrowRight
          aria-hidden
          className={cn("size-3.5 shrink-0 text-muted-foreground", rowReveal())}
        />
      </button>
    </li>
  )
}
