"use client"

import { get } from "@/lib/api"
import { bytes, percent } from "@/lib/format"
import { useContainerLive } from "@/hooks/use-container-live"
import { usePoll } from "@/hooks/use-poll"
import type { ContainerHistory } from "@/lib/types"
import { utilisationTone } from "@/components/meter"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { StatusDot } from "@/components/status-dot"
import { TileTrend } from "@/components/metrics/sparkline"
import { NumberTicker } from "@/components/ui/number-ticker"

/**
 * What the project's container is using, from the live stats socket — the
 * reading Docker's own page shows — with the last hour, from the recorded
 * history, in each tile's trend slot, so "1.2%" also says whether it was 40% a
 * moment ago. The Overview's glance: processor and memory, each a way to the
 * Runtime page that has the rest.
 *
 * A figure that fills against a limit — memory under a cap, the processor
 * under a quota — keeps a meter; one with nothing to fill against carries its
 * hour instead (§15's checklist). The dot beside the processor figure
 * breathes only while frames are arriving, because that is the one reading
 * here that is live (§11).
 *
 * The socket is `useContainerLive`'s, so the minutes read here are already on
 * the Runtime page's live charts when the reader follows the link.
 *
 * With no container — the application has not started — the tiles stay, with
 * a dash and `reason` under the first, so the row never reflows when one
 * appears. `href` makes each tile a way to the page that has the rest.
 */
export function UsageTiles({
  containerId,
  name,
  reason,
  href,
}: {
  containerId?: string
  /** The service the container is, said under the processor figure. */
  name?: string
  /** Why there is no container to read. */
  reason?: string
  href?: string
}) {
  const feed = useContainerLive(containerId)
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
  const stats = feed.stats
  const points = history.data?.points ?? []
  // With no frame yet — a proxy that does not pass the socket, a stream that
  // stalled — the last recorded minute is the figure, said as that and
  // never drawn as live (§11), rather than a dash over a line of values.
  const recorded = stats ? undefined : points.at(-1)
  const trend = (values: number[], label: string, color: string) => (
    <TileTrend values={values} label={`${label} over the last hour`} color={color} />
  )

  const streamedCpu = stats?.cpuReady ? stats.cpuPercent : undefined
  const cpuShare =
    streamedCpu !== undefined && stats?.cpuLimit ? streamedCpu / stats.cpuLimit : undefined
  const cpu = streamedCpu ?? recorded?.cpu
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
          %{feed.live && <StatusDot tone="running" live />}
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

  return (
    <StatGrid columns={2} dense>
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
