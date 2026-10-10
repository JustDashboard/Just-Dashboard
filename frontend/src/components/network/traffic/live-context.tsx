"use client"

import type { NetworkLink } from "@/lib/types"
import {
  millis,
  observationAge,
  packetRate,
  retransmitShare,
  shareLabel,
} from "@/lib/network-traffic"
import { plural, relativeTime } from "@/lib/format"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { TileTrend } from "@/components/metrics/sparkline"
import { useNow } from "@/components/deploy/vocabulary"
import type { LiveTraffic } from "@/components/network/use-live-traffic"

/**
 * What the live byte rates cannot say on their own, over the same two-second
 * steps: how old the newest reading is, the uplink's packets, how much of
 * what TCP sent had to be sent again, and TCP's own round-trip estimate on
 * the host's live sockets. Every figure is something the kernel measured on
 * real traffic; nothing here is a probe.
 *
 * Age is the first figure because a chart that keeps drawing its last answer
 * looks exactly like a quiet link: past three missed steps it says stale.
 */
export function LiveContext({
  live,
  links,
}: {
  live: LiveTraffic
  links: NetworkLink[] | undefined
}) {
  const now = useNow(1000) / 1000
  const age = observationAge(live.sampledAt, now, live.stepSeconds ?? 2)
  const stale = age.stale || Boolean(live.error)
  const uplink = links?.find((l) => l.role === "uplink")?.name
  const points = uplink ? live.series[uplink] : undefined
  const last = points?.at(-1)
  const share = retransmitShare(live.tcp, 30)
  const recent = live.tcp.slice(-30)
  const resets = recent.reduce((n, p) => n + p.resets, 0) * (live.stepSeconds ?? 2)
  const latency = live.latency
  return (
    <StatGrid columns={4} aria-label="Live context">
      <StatTile
        label="Newest reading"
        value={live.sampledAt ? `${age.seconds}s old` : "—"}
        tone={stale ? "warning" : "default"}
        hint={
          live.error
            ? "the last poll failed; figures are the last answer"
            : age.stale
              ? "the sampler has not stepped; figures are stale"
              : `sampled every ${live.stepSeconds ?? 2}s`
        }
      />
      <StatTile
        label={uplink ? `Packets on ${uplink}` : "Packets"}
        value={last?.rxp !== undefined ? packetRate((last.rxp ?? 0) + (last.txp ?? 0)) : "—"}
        trend={
          <TileTrend
            values={(points ?? []).slice(-150).map((p) => (p.rxp ?? 0) + (p.txp ?? 0))}
            color="var(--chart-1)"
            label="Uplink packets a second over the last five minutes"
          />
        }
        hint={
          last?.rxp !== undefined
            ? `${packetRate(last.rxp)} in · ${packetRate(last.txp)} out`
            : "no uplink reading yet"
        }
      />
      <StatTile
        label="TCP resent"
        value={shareLabel(share)}
        tone={share !== undefined && share > 0.02 ? "warning" : "default"}
        trend={
          <TileTrend
            values={live.tcp
              .slice(-150)
              .map((p) => (p.outSegs > 0 ? (p.retrans / p.outSegs) * 100 : 0))}
            color="var(--chart-3)"
            label="Share of TCP segments sent again over the last five minutes"
          />
        }
        hint={
          live.tcpError
            ? `TCP counters unreadable: ${live.tcpError}`
            : `of segments in the last minute · ${plural(Math.round(resets), "reset")}`
        }
      />
      <StatTile
        label="TCP round trip"
        value={latency && !latency.error && latency.sockets > 0 ? millis(latency.medianMs) : "—"}
        hint={
          !latency
            ? "measuring…"
            : latency.error
              ? latency.error
              : latency.sockets === 0
                ? "no non-loopback TCP socket to measure"
                : `median of ${plural(latency.sockets, "socket")} · p90 ${millis(latency.p90Ms)} · read ${relativeTime(latency.at)}`
        }
      />
    </StatGrid>
  )
}
