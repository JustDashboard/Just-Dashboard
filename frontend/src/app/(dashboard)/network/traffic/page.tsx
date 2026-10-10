"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import type { ShapingView } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { Page, PageContext, Section } from "@/components/page"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Bandwidth } from "@/components/network/traffic/bandwidth"
import type { TrafficSpan } from "@/components/network/traffic/interface-charts"
import { TrafficWorkloads } from "@/components/network/traffic/workloads"
import { Shaping } from "@/components/network/traffic/shaping"
import { EBPFSection } from "@/components/network/traffic/ebpf"

/**
 * What is moving bytes, and how fast it may.
 *
 * It opens on each device's own chart — the uplink across the page, then the
 * tunnels, bridges and cards — with the window the whole page follows in the
 * head of the section: Live is the two-second ring the Overview draws, the
 * rest are the recorded samples, and a range typed there or dragged across a
 * chart narrows every chart to it. Then who is moving them, programs by what
 * they send right now and containers by what they sent over that same window,
 * as the Processes page's workload bar turned to traffic. Then shaping, which
 * is what bounds it, and last the eBPF programs the kernel has loaded, which
 * is what watches and filters it.
 */
export default function NetworkTrafficPage() {
  const [span, setSpan] = useState<TrafficSpan>("live")
  const shaping = usePoll<ShapingView>(
    (signal) => get("/network/shaping", undefined, signal),
    15_000,
  )

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Traffic" />
      {shaping.data && (
        <NetworkReadWarning
          error={shaping.error}
          refresh={shaping.refresh}
          lastSuccess={shaping.lastSuccess}
        />
      )}

      <Bandwidth span={span} onSpan={setSpan} />

      <Section title="Who is moving them">
        <TrafficWorkloads span={span} />
      </Section>

      {shaping.data ? (
        <Shaping view={shaping.data} onChanged={shaping.refresh} stale={Boolean(shaping.error)} />
      ) : (
        <Section title="Shaping">
          {shaping.error ? (
            <ErrorState error={shaping.error} onRetry={shaping.refresh} />
          ) : (
            <LoadingPanel />
          )}
        </Section>
      )}

      <EBPFSection />
    </Page>
  )
}
