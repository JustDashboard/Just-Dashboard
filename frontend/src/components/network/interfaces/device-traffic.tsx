"use client"

import { useMemo } from "react"
import { ArrowDown, ArrowUp, Warning } from "@/components/icons"
import { bytes, plural, rate } from "@/lib/format"
import type { NetworkLink, NetworkLivePoint } from "@/lib/types"
import { WireMark } from "@/components/deploy/wire"
import { ChartPanel } from "@/components/metrics/chart-panel"
import { TileTrend } from "@/components/metrics/sparkline"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ReadingLine, ReadingLines } from "@/components/reading-line"
import { Notice } from "@/components/state"
import { RX, TX } from "@/components/network/rate-pair"
import { DeviceErrors, useDeviceDetail } from "./device-detail"
import type { DeviceTab } from "./device-facts"
import { DevicePicture } from "./device-picture"
import { DeviceReadiness, READINESS_KINDS } from "./device-readiness"

const SERIES = [
  { key: "rx", label: "In", color: RX, kind: "area" as const },
  { key: "tx", label: "Out", color: TX, kind: "area" as const },
]
const formatRate = (value: number) => rate(value)
const axisRate = (value: number) => `${bytes(value, 0)}/s`

/**
 * A device's first view: what it is wired to, what it carried over the last
 * fifteen minutes, what it has carried since it was made, and what the kernel
 * counted going wrong on it. A tunnel or a virtual device adds what this host
 * can establish about whether it can carry anything at all.
 */
export function DeviceTraffic({
  link,
  links,
  points,
  onOpenNamespace,
  onTab,
}: {
  link: NetworkLink
  links: NetworkLink[]
  points?: NetworkLivePoint[]
  onOpenNamespace: (kind: "named" | "container", name: string) => void
  onTab: (tab: DeviceTab) => void
}) {
  const rows = useMemo(
    () => (points ?? []).map((p) => ({ ts: p.t * 1000, rx: p.rx, tx: p.tx })),
    [points],
  )
  const faults = useMemo(() => (points ?? []).map((p) => (p.err ?? 0) + (p.drop ?? 0)), [points])
  const c = link.counters
  const errors = c.rxErrors + c.txErrors
  const dropped = c.rxDropped + c.txDropped
  return (
    <div className="flex min-w-0 flex-col gap-8 pb-6">
      {link.dockerJoin && (
        <Notice tone="warning" title="Docker join incomplete">
          {link.dockerJoinReason}
        </Notice>
      )}
      <DevicePicture link={link} links={links} onOpenNamespace={onOpenNamespace} onTab={onTab} />
      <ChartPanel
        plain
        title="Last 15 minutes"
        rows={rows}
        series={SERIES}
        format={formatRate}
        axisFormat={axisRate}
        showPeaks={false}
        height={180}
        note="Collecting: the first readings arrive within a few seconds."
      />
      <Panel plain>
        <PanelHeader title="Since it was created" />
        <PanelBody className="pt-1">
          <ReadingLines aria-label="Since it was created">
            <ReadingLine
              mark={
                <WireMark size="sm" hue={RX}>
                  <ArrowDown aria-hidden />
                </WireMark>
              }
              name="Received"
              hint={`${c.rxPackets.toLocaleString()} packets`}
            >
              <span className="numeric">{bytes(c.rxBytes)}</span>
            </ReadingLine>
            <ReadingLine
              mark={
                <WireMark size="sm" hue={TX}>
                  <ArrowUp aria-hidden />
                </WireMark>
              }
              name="Sent"
              hint={`${c.txPackets.toLocaleString()} packets`}
            >
              <span className="numeric">{bytes(c.txBytes)}</span>
            </ReadingLine>
            <ReadingLine
              mark={
                <WireMark size="sm" tone={errors + dropped > 0 ? "warning" : "neutral"}>
                  <Warning aria-hidden />
                </WireMark>
              }
              name="Faults"
              hint={`${dropped.toLocaleString()} dropped`}
              trend={
                <TileTrend
                  values={faults}
                  color="var(--chart-3)"
                  label="Errors and drops over the last fifteen minutes"
                />
              }
            >
              <span className={errors + dropped > 0 ? "numeric text-warning" : "numeric"}>
                {errors === 0 ? "No errors" : plural(errors, "error")}
              </span>
            </ReadingLine>
          </ReadingLines>
        </PanelBody>
      </Panel>
      {link.role !== "loopback" && <LinkErrors name={link.name} />}
      {READINESS_KINDS.has(link.kind) && <DeviceReadiness link={link} />}
    </div>
  )
}

function LinkErrors({ name }: { name: string }) {
  const detail = useDeviceDetail(name)
  return <DeviceErrors detail={detail} />
}
