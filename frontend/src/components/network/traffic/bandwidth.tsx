"use client"

import { get } from "@/lib/api"
import type { NetworkLink } from "@/lib/types"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { usePoll } from "@/hooks/use-poll"
import { Section } from "@/components/page"
import { ErrorState, LoadingPanel } from "@/components/state"
import { StreamState } from "@/components/overview/readings"
import { useLiveTraffic } from "@/components/network/use-live-traffic"
import {
  InterfaceCharts,
  WindowPicker,
  type TrafficSpan,
} from "@/components/network/traffic/interface-charts"
import { LiveContext } from "@/components/network/traffic/live-context"
import { TrafficBudgets } from "@/components/network/traffic/budgets"

/**
 * Each device's own chart, with the window the whole page follows in the
 * section's head. The two-second ring lives here rather than on the page: it
 * replaces its arrays every poll, and a page that held it would re-render the
 * shaping table and the eBPF inventory under it every two seconds for figures
 * that change every thirty or not at all.
 *
 * Live is claimed only on the Live window, because that is the only one fed by
 * the ring; a recorded window is a read of history and does not say it is live.
 * On the live window the context the rates cannot carry comes first — how old
 * the newest reading is, packets, TCP's resent share and round trip — and the
 * transfer budgets, which are measured against the recorded history, close the
 * section on every window.
 */
export function Bandwidth({
  span,
  onSpan,
}: {
  span: TrafficSpan
  onSpan: (next: TrafficSpan) => void
}) {
  const live = useLiveTraffic(true, 2000, true)
  const links = usePoll<NetworkLink[]>((signal) => get("/network/links", undefined, signal), 30_000)
  return (
    <Section
      title="Bandwidth by device"
      actions={
        <span className="flex flex-wrap items-start gap-x-4 gap-y-2">
          {span === "live" && live.now > 0 && (
            <StreamState connection={live.error ? "closed" : "open"} />
          )}
          <WindowPicker value={span} onChange={onSpan} />
        </span>
      }
    >
      {links.data && (
        <NetworkReadWarning
          error={links.error}
          refresh={links.refresh}
          lastSuccess={links.lastSuccess}
          reading="link inventory"
        />
      )}
      {span === "live" && <LiveContext live={live} links={links.data} />}
      {links.data ? (
        <InterfaceCharts links={links.data} live={live.series} span={span} onSpan={onSpan} />
      ) : links.error ? (
        <ErrorState error={links.error} onRetry={links.refresh} />
      ) : (
        <LoadingPanel />
      )}
      <TrafficBudgets links={links.data} />
    </Section>
  )
}
