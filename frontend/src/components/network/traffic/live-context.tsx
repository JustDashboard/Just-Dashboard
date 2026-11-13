"use client"

import { ArrowUpDown, Monitoring } from "@/components/icons"
import { WireMark } from "@/components/deploy/wire"
import { TileTrend } from "@/components/metrics/sparkline"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { TextShimmer } from "@/components/ui/text-shimmer"
import type { LiveTraffic } from "@/components/network/use-live-traffic"
import { packetWords, tcpWords } from "./words"

/**
 * What the byte rates cannot say on their own, over the same two-second
 * steps: the uplink's packets, and how TCP is faring — the share of what it
 * sent that had to be sent again, and its own round-trip estimate on the
 * host's live sockets. Every figure is something the kernel measured on real
 * traffic; nothing here is a probe.
 *
 * These were four tiles. How old the newest reading is went to the identity
 * line, where the state of the page's readings is said; the other three are
 * two lines here, each with its last five minutes at its end. A line is one
 * height in every state, so a first reading landing moves nothing under it.
 */
export function LiveContext({ live, uplink }: { live: LiveTraffic; uplink: string | undefined }) {
  const points = uplink ? live.series[uplink] : undefined
  const packets = packetWords(points?.at(-1))
  const tcp = tcpWords({
    tcp: live.tcp,
    tcpError: live.tcpError,
    latency: live.latency,
    stepSeconds: live.stepSeconds,
  })
  const waiting = live.now === 0 && !live.error
  return (
    <ReadingLines aria-label="Live context" role="group">
      <ReadingLine
        mark={
          <WireMark size="md" hue="var(--chart-1)">
            <ArrowUpDown />
          </WireMark>
        }
        name={uplink ? <>Packets on {uplink}</> : "Packets"}
        hint={waiting ? undefined : packets.hint}
        trend={
          <TileTrend
            values={(points ?? []).slice(-150).map((p) => (p.rxp ?? 0) + (p.txp ?? 0))}
            color="var(--chart-1)"
            label="Uplink packets a second over the last five minutes"
          />
        }
      >
        {waiting ? (
          <TextShimmer>Reading the uplink…</TextShimmer>
        ) : (
          (packets.reading ?? <NoReading>No reading yet</NoReading>)
        )}
      </ReadingLine>
      <ReadingLine
        mark={
          tcp.warning ? (
            <WireMark size="md" tone="warning">
              <Monitoring />
            </WireMark>
          ) : (
            <WireMark size="md" hue="var(--chart-3)">
              <Monitoring />
            </WireMark>
          )
        }
        name="TCP health"
        hint={waiting ? undefined : tcp.hint}
        trend={
          <TileTrend
            values={live.tcp
              .slice(-150)
              .map((p) => (p.outSegs > 0 ? (p.retrans / p.outSegs) * 100 : 0))}
            color="var(--chart-3)"
            label="Share of TCP segments sent again over the last five minutes"
          />
        }
      >
        {waiting ? (
          <TextShimmer>Reading TCP&rsquo;s counters…</TextShimmer>
        ) : (
          (tcp.reading ?? <NoReading>Nothing sent yet</NoReading>)
        )}
      </ReadingLine>
    </ReadingLines>
  )
}
