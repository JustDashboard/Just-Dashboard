"use client"

import { plural } from "@/lib/format"
import type { ContainerTraffic, NetworkLink } from "@/lib/types"
import type { PollState } from "@/hooks/use-poll"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState } from "@/components/state"
import type { LiveTraffic } from "@/components/network/use-live-traffic"
import { InterfaceCharts, WindowPicker } from "./interface-charts"
import { LiveContext } from "./live-context"
import type { RecordedWindow, TrafficSpan } from "./span"
import { TrafficWorkloads } from "./workloads"

/**
 * The page's first view: what is moving, on which device, and who moves it.
 *
 * Two readings the byte rates cannot carry — the uplink's packets and TCP's
 * health — then each device's own chart, with the window every chart follows
 * on the section's title line, then the programs and the containers as spans
 * of what moved. Live is the two-second ring the page holds; the rest are the
 * recorded samples, and a range typed in the picker or dragged across a chart
 * narrows every chart to it.
 */
export function Bandwidth({
  live,
  links,
  containers,
  range,
  span,
  onSpan,
}: {
  live: LiveTraffic
  links: PollState<NetworkLink[]>
  containers: PollState<ContainerTraffic>
  range: RecordedWindow
  span: TrafficSpan
  onSpan: (next: TrafficSpan) => void
}) {
  const uplink = links.data?.find((l) => l.role === "uplink")?.name
  const shown = links.data?.filter(
    (l) => l.role !== "container" && l.owner !== "kernel" && l.adminUp,
  ).length
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <LiveContext live={live} uplink={uplink} />

      <Panel plain aria-label="Bandwidth by device">
        <PanelHeader
          title="Devices"
          actions={
            <span className="flex flex-wrap items-start justify-end gap-x-4 gap-y-2">
              {shown !== undefined && (
                <span className="numeric flex h-8 items-center text-hint text-muted-foreground">
                  {plural(shown, "device")}
                </span>
              )}
              <WindowPicker value={span} onChange={onSpan} />
            </span>
          }
        />
        <PanelBody className="flex flex-col gap-6">
          {links.data && (
            <NetworkReadWarning
              error={links.error}
              refresh={links.refresh}
              lastSuccess={links.lastSuccess}
              reading="link inventory"
            />
          )}
          {span === "live" && live.error && live.now > 0 && (
            <NetworkReadWarning
              error={live.error}
              refresh={live.refresh}
              lastSuccess={live.lastSuccess}
              reading="live throughput"
            />
          )}
          {!links.data && links.error ? (
            <ErrorState error={links.error} onRetry={links.refresh} />
          ) : (
            <InterfaceCharts links={links.data} live={live.series} span={span} onSpan={onSpan} />
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Who is moving them">
        <PanelHeader title="Who is moving them" />
        <PanelBody>
          <TrafficWorkloads span={span} range={range} containers={containers} />
        </PanelBody>
      </Panel>
    </div>
  )
}
