"use client"

import { bytes, percent } from "@/lib/format"
import { containerRateLabel } from "@/lib/container-usage"
import type { ContainerStats } from "@/lib/types"
import { useContainerLive } from "@/hooks/use-container-live"
import { utilisationTone } from "@/components/meter"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { NumberTicker } from "@/components/ui/number-ticker"
import { RangePicker } from "@/components/metrics/range-picker"
import { ContainerCharts, useContainerUsage } from "@/components/docker/container-usage"

/**
 * What the selected service is using now, and the charts of how it got there.
 *
 * Five readings off the stats socket — processor, memory, what it is receiving
 * and sending this second, and the processes inside — over the same charts
 * Docker's own page draws. The Live range draws the socket's last five minutes
 * a frame a second; the others read the recorded history, so the same panels
 * answer "what is it doing" and "what did it do overnight".
 *
 * The readings carry no trend of their own: each one's shape is in the chart
 * below it, at a scale with an axis to read it against, where a 36px band in
 * the tile could say only that something moved. Network is two readings
 * because in and out are different questions — a service under a flood and a
 * service serving a large download share a sum.
 *
 * `initial` is a polled reading of the same container, shown until the first
 * frame lands; `onStats` hands each frame on, so the service's card shows the
 * figure these do rather than an older one beside them. `picker` is the
 * service selector, when there is more than one to choose from.
 */
export function RuntimeUsage({
  containerId,
  name,
  product,
  initial,
  picker,
  onStats,
}: {
  containerId: string
  name: string
  product?: string
  initial?: ContainerStats
  picker?: React.ReactNode
  onStats?: (stats: ContainerStats) => void
}) {
  const feed = useContainerLive(containerId, onStats)
  const stats = feed.stats ?? (initial?.id === containerId ? initial : undefined)
  const usage = useContainerUsage(containerId, {
    rows: feed.rows,
    memoryLimit: stats?.memLimited ? stats.memLimit : 0,
  })

  const cpu = stats?.cpuReady ? stats.cpuPercent : undefined
  const cpuShare = cpu !== undefined && stats?.cpuLimit ? cpu / stats.cpuLimit : undefined
  const memory = stats?.memUsage
  const mib = memory !== undefined ? memory / 1024 / 1024 : 0
  const gib = mib >= 1024
  const counted = stats?.networkAvailable === true
  const received = rateParts(feed.now?.netRx)
  const sent = rateParts(feed.now?.netTx)
  const label = feed.live
    ? "Live"
    : feed.stale
      ? "Readings stale"
      : feed.state === "open"
        ? "Waiting for Docker"
        : "Connecting"

  // The eyebrow keeps its word as the meter's name; the mark in front of it
  // says whose figures these are when several services share the selector.
  const eyebrow = (word: string) =>
    product ? (
      <span className="flex min-w-0 items-center gap-1.5">
        <ProductGlyph id={product} className="size-3" />
        <span className="truncate">{word}</span>
      </span>
    ) : (
      word
    )

  return (
    <Panel plain>
      <PanelHeader
        title="Resource usage"
        actions={
          <>
            <Status
              tone={feed.live ? "running" : "notice"}
              live={feed.live}
              label={label}
              className="mr-2"
            />
            <RangePicker controls={usage.controls} ranges={usage.ranges} />
          </>
        }
      >
        {picker}
      </PanelHeader>
      <PanelBody className="space-y-6">
        <StatGrid columns={5} dense>
          <StatTile
            label={eyebrow("CPU")}
            meterLabel="CPU"
            value={
              cpu !== undefined ? (
                <Arrived>
                  <NumberTicker value={cpu} decimalPlaces={1} />
                </Arrived>
              ) : (
                "—"
              )
            }
            trailing={cpu !== undefined && "%"}
            meter={cpuShare}
            tone={cpuShare === undefined ? "default" : utilisationTone(cpuShare)}
            hint={
              stats?.cpuLimit
                ? `of a ${stats.cpuLimit}-core quota`
                : stats?.hostCpus
                  ? `100% is one core · ${stats.hostCpus} here`
                  : "100% is one core"
            }
          />
          <StatTile
            label={eyebrow("Memory")}
            meterLabel="Memory"
            value={
              memory !== undefined ? (
                <Arrived>
                  {/* Keyed by the unit, so crossing a gibibyte counts up in the
                      new unit rather than springing from 1,023 down to 1.00. */}
                  <NumberTicker
                    key={gib ? "gib" : "mib"}
                    value={gib ? mib / 1024 : mib}
                    decimalPlaces={gib ? 2 : 0}
                  />
                </Arrived>
              ) : (
                "—"
              )
            }
            trailing={memory !== undefined && (gib ? "GB" : "MB")}
            meter={stats?.memLimited ? stats.memPercent : undefined}
            tone={stats?.memLimited ? utilisationTone(stats.memPercent) : "default"}
            hint={
              !stats
                ? "Waiting for Docker's first reading"
                : stats.memLimited
                  ? `of ${bytes(stats.memLimit)} limit`
                  : stats.memHostPercent !== undefined
                    ? `no limit · ${percent(stats.memHostPercent)} of the host`
                    : "no limit"
            }
          />
          <StatTile
            label={eyebrow("Received")}
            value={received.figure}
            trailing={received.unit}
            hint={
              !stats
                ? undefined
                : counted
                  ? `${bytes(stats.netRx)} since it started`
                  : "no interface to count"
            }
          />
          <StatTile
            label={eyebrow("Sent")}
            value={sent.figure}
            trailing={sent.unit}
            hint={
              !stats
                ? undefined
                : counted
                  ? `${bytes(stats.netTx)} since it started`
                  : "no interface to count"
            }
          />
          <StatTile
            label={eyebrow("Processes")}
            value={
              stats ? (
                <Arrived>
                  <NumberTicker value={stats.pids} />
                </Arrived>
              ) : (
                "—"
              )
            }
            hint="inside the container"
          />
        </StatGrid>
        <ContainerCharts usage={usage} containerId={containerId} name={name} plain />
      </PanelBody>
    </Panel>
  )
}

/** A rate as a tile's figure and its unit, set apart the way the processor's % is. */
function rateParts(value: number | null | undefined) {
  const label = containerRateLabel(value)
  const space = label.lastIndexOf(" ")
  return space < 0
    ? { figure: label, unit: undefined }
    : { figure: label.slice(0, space), unit: label.slice(space + 1) }
}

/** A figure that rises once when the socket's first frame lands (§11 *arrived*). */
function Arrived({ children }: { children: React.ReactNode }) {
  return <span className="inline-block animate-rise">{children}</span>
}
