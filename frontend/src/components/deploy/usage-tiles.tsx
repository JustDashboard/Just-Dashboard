"use client"

import { useCallback, useState } from "react"
import { get } from "@/lib/api"
import { bytes, percent, rate } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import type { ContainerHistory, ContainerStats } from "@/lib/types"
import { utilisationTone } from "@/components/meter"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { StatusDot } from "@/components/status-dot"
import { TileTrend } from "@/components/metrics/sparkline"
import { NumberTicker } from "@/components/ui/number-ticker"

type Reading = {
  containerId: string
  stats: ContainerStats
  /** Bytes a second, from two frames; there is no rate until the second arrives. */
  network?: { rx: number; tx: number }
}

/** The receive and send rates between two frames of the stats socket. */
function throughput(previous: ContainerStats, next: ContainerStats) {
  const seconds = (Date.parse(next.ts) - Date.parse(previous.ts)) / 1000
  if (!(seconds > 0)) return undefined
  return {
    rx: Math.max(0, next.netRx - previous.netRx) / seconds,
    tx: Math.max(0, next.netTx - previous.netTx) / seconds,
  }
}

/**
 * What the project's container is using, from the live stats socket — the
 * reading Docker's own page shows — with the last hour, from the recorded
 * history, in each tile's trend slot, so "1.2%" also says whether it was 40% a
 * moment ago.
 *
 * `columns={2}` is the Overview's glance: processor and memory. `columns={4}`
 * is Runtime's reading, which adds the processes inside and the network. They
 * were two implementations over one socket, and the Runtime page's four were
 * the flatter of the two.
 *
 * A figure that fills against a limit — memory under a cap, the processor
 * under a quota — keeps a meter; one with nothing to fill against carries its
 * hour instead (§15's checklist). The dot beside the processor figure
 * breathes only while the socket is open, because that is the one reading
 * here that is live (§11).
 *
 * With no container — the application has not started — the tiles stay, with
 * a dash and `reason` under the first, so the row never reflows when one
 * appears. `href` makes each tile a way to the page that has the rest.
 *
 * `initial` is a polled reading of the same container, shown until the first
 * frame lands, so a slow socket leaves the tiles with a figure rather than a
 * dash; `onStats` hands each frame on, so a card elsewhere on the page can
 * show the same figure the tiles do rather than an older one beside them.
 */
export function UsageTiles({
  containerId,
  name,
  reason,
  columns = 2,
  href,
  initial,
  onStats,
}: {
  containerId?: string
  /** The service the container is, said under the processor figure. */
  name?: string
  /** Why there is no container to read. */
  reason?: string
  columns?: 2 | 4
  href?: string
  initial?: ContainerStats
  onStats?: (stats: ContainerStats) => void
}) {
  const [reading, setReading] = useState<Reading>()
  const onMessage = useCallback(
    (message: Envelope) => {
      if (message.type !== "stats" || !containerId) return
      const stats = message.data as ContainerStats
      setReading((previous) => ({
        containerId,
        stats,
        network:
          previous?.containerId === containerId
            ? (throughput(previous.stats, stats) ?? previous.network)
            : undefined,
      }))
      onStats?.(stats)
    },
    [containerId, onStats],
  )
  const socket = useSocket(`/docker/containers/${containerId}/stats/stream`, {
    onMessage,
    enabled: Boolean(containerId),
  })
  const history = usePoll(
    (signal) =>
      get<ContainerHistory>(
        `/docker/containers/${encodeURIComponent(containerId ?? "")}/stats/history`,
        { points: 60 },
        signal,
      ),
    60000,
    [containerId],
    { enabled: Boolean(containerId) },
  )
  // A reading of the container this was showing a moment ago is not a
  // reading of this one.
  const current = reading?.containerId === containerId ? reading : undefined
  const stats = current?.stats ?? (initial?.id === containerId ? initial : undefined)
  const points = history.data?.points ?? []
  // With no frame yet — a proxy that does not pass the socket, a stream that
  // stalled — the last recorded minute is the figure, said as that and
  // never drawn as live (§11), rather than a dash over a line of values.
  const recorded = stats ? undefined : points.at(-1)
  const trend = (values: number[], label: string, color: string, max?: number) => (
    <TileTrend values={values} label={`${label} over the last hour`} color={color} max={max} />
  )

  const cpuShare = stats?.cpuLimit ? stats.cpuPercent / stats.cpuLimit : undefined
  const cpu = stats?.cpuPercent ?? recorded?.cpu
  const memory = stats?.memUsage ?? recorded?.memBytes
  const mib = memory !== undefined ? memory / 1024 / 1024 : 0
  const gib = mib >= 1024

  // Labels are words, because each one also names its meter and its link.
  const tiles: (React.ComponentProps<typeof StatTile> & { label: string })[] = [
    {
      label: "CPU",
      value:
        cpu !== undefined ? (
          <Arrived>
            <NumberTicker value={cpu} decimalPlaces={1} />
          </Arrived>
        ) : (
          "—"
        ),
      trailing: cpu !== undefined && (
        <span className="inline-flex items-center gap-2">
          %{current && socket.state === "open" && <StatusDot tone="running" live />}
        </span>
      ),
      meter: cpuShare,
      tone: cpuShare === undefined ? "default" : utilisationTone(cpuShare),
      trend:
        cpuShare === undefined &&
        trend(
          points.map((point) => point.cpuPeak),
          "CPU",
          "var(--chart-1)",
        ),
      hint: !containerId
        ? reason
        : recorded
          ? "last minute · not live"
          : name
            ? `100% is one core · ${name}`
            : stats?.hostCpus
              ? `100% is one core · ${stats.hostCpus} cores here`
              : "100% is one core",
    },
    {
      label: "Memory",
      value:
        memory !== undefined ? (
          <Arrived>
            {/* Keyed by the unit, so crossing a gibibyte counts up in the new
                unit rather than springing from 1,023 down to 1.00. */}
            <NumberTicker
              key={gib ? "gib" : "mib"}
              value={gib ? mib / 1024 : mib}
              decimalPlaces={gib ? 2 : 0}
            />
          </Arrived>
        ) : (
          "—"
        ),
      // The unit `bytes()` gives every other figure on the page: 1024-based,
      // written MB and GB.
      trailing: memory !== undefined && (gib ? "GB" : "MB"),
      meter: stats?.memLimited ? stats.memPercent : undefined,
      tone: stats?.memLimited ? utilisationTone(stats.memPercent) : "default",
      trend:
        !stats?.memLimited &&
        trend(
          points.map((point) => point.memBytesPeak),
          "Memory",
          "var(--chart-2)",
        ),
      hint: !stats
        ? containerId &&
          (recorded ? "last minute · not live" : "Waiting for Docker's first reading")
        : stats.memLimited
          ? `of ${bytes(stats.memLimit)} limit`
          : stats.memHostPercent !== undefined
            ? `no limit · ${percent(stats.memHostPercent)} of the host`
            : "no limit",
    },
  ]
  if (columns === 4) {
    tiles.push(
      {
        label: "Processes",
        value: stats ? (
          <Arrived>
            <NumberTicker value={stats.pids} />
          </Arrived>
        ) : (
          "—"
        ),
        // Headroom over the hour's highest count: a steady count is the
        // usual case, and scaled to its own maximum it filled the whole slot
        // as a bar rather than drawing a line.
        trend: trend(
          points.map((point) => point.pids),
          "Processes",
          "var(--chart-3)",
          Math.max(0, ...points.map((point) => point.pids)) * 1.5,
        ),
        hint: "inside the container",
      },
      {
        label: "Network",
        value: current?.network ? <Arrived>{rate(current.network.rx)}</Arrived> : "—",
        trailing: current?.network && "in",
        trend: trend(
          points.map((point) => point.netRx + point.netTx),
          "Network",
          "var(--chart-5)",
        ),
        hint: current?.network && `${rate(current.network.tx)} out`,
      },
    )
  }

  return (
    <StatGrid columns={columns} dense>
      {tiles.map((tile) =>
        href ? (
          <StatLink key={tile.label} href={href} label={`${tile.label} on Runtime`}>
            <StatTile {...tile} className="h-full transition-colors group-hover:bg-row-hover" />
          </StatLink>
        ) : (
          <StatTile key={tile.label} {...tile} />
        ),
      )}
    </StatGrid>
  )
}

/** A figure that rises once when the socket's first frame lands (§11 *arrived*). */
function Arrived({ children }: { children: React.ReactNode }) {
  return <span className="inline-block animate-rise">{children}</span>
}
