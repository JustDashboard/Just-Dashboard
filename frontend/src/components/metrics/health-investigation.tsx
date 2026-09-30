"use client"

import Link from "next/link"
import { useState } from "react"
import { get } from "@/lib/api"
import { percent, timestamp } from "@/lib/format"
import { healthInvestigation } from "@/lib/server-advisor"
import type { HealthFinding, Snapshot } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { Panel, PanelHeader, PanelBody } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { StorageAdvisor } from "@/components/metrics/storage-advisor"
import { WorkloadAdvisor } from "@/components/metrics/workload-advisor"

export function HealthInvestigation({
  finding,
  onOpenChange,
  onChanged,
}: {
  finding: HealthFinding | null
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  const target = finding ? healthInvestigation(finding.id) : undefined
  return (
    <SidePanel
      open={!!finding}
      onOpenChange={onOpenChange}
      title={finding?.title ?? "Health investigation"}
      description="Local evidence and reviewed actions"
      width="lg"
      initialFocus="body"
    >
      {finding && target && (
        <div className="space-y-5">
          <p className="text-body text-muted-foreground">{finding.detail}</p>
          {target.kind === "storage" && (
            <StorageAdvisor
              key={finding.id}
              path={target.path}
              inodes={target.inodes}
              onChanged={onChanged}
            />
          )}
          {target.kind === "workloads" && (
            <WorkloadAdvisor key={finding.id} sort={target.sort} onChanged={onChanged} />
          )}
          {(target.kind === "external" || target.kind === "network") && (
            <OtherEvidence key={finding.id} finding={finding} />
          )}
        </div>
      )}
    </SidePanel>
  )
}

function OtherEvidence({ finding }: { finding: HealthFinding }) {
  const target = healthInvestigation(finding.id)
  const [frames, setFrames] = useState<Snapshot[]>([])
  const report = usePoll(
    (signal) =>
      get<Snapshot>("/system/metrics", undefined, signal).then((snapshot) => {
        if (!signal.aborted) setFrames((current) => [...current.slice(-1), snapshot])
        return snapshot
      }),
    5000,
    [finding.id],
  )
  const current = frames.at(-1)
  const previous = frames.length === 2 ? frames[0] : undefined
  const networkInterface = target?.kind === "network" ? target.networkInterface : undefined
  const network = current?.net.find((item) => item.interface === networkInterface)
  const priorNetwork = previous?.net.find((item) => item.interface === networkInterface)
  const elapsed =
    current && previous ? (Date.parse(current.ts) - Date.parse(previous.ts)) / 1000 : 0
  const drops = network ? network.dropIn + network.dropOut : undefined
  const priorDrops = priorNetwork ? priorNetwork.dropIn + priorNetwork.dropOut : undefined
  const delta =
    drops !== undefined && priorDrops !== undefined && drops >= priorDrops && elapsed > 0
      ? drops - priorDrops
      : undefined
  return (
    <div className="space-y-4">
      {report.error && (
        <p role="alert" className="text-body text-destructive">
          {report.error.message} Readings below may be stale.
        </p>
      )}
      {target?.kind === "external" && (
        <p className="text-body leading-relaxed text-muted-foreground">{target.reason}</p>
      )}
      {current && finding.id === "steal" && (
        <StatGrid columns={2}>
          <StatTile label="Current CPU steal" value={percent(current.cpu.modes.steal)} />
          <StatTile
            label="Reported assessment"
            value={percent(finding.value)}
            hint="May use recorded history"
          />
        </StatGrid>
      )}
      {current && finding.id.startsWith("temp:") && (
        <Panel plain>
          <PanelHeader title="Sensor readings" />
          <PanelBody>
            <div className="space-y-2">
              {current.sensors
                ?.filter((sensor) => sensor.name === finding.id.slice(5))
                .map((sensor) => (
                  <p key={sensor.name} className="text-body">
                    {sensor.name}: {sensor.tempC}°C · high {sensor.high || "unavailable"} · critical{" "}
                    {sensor.critical || "unavailable"}
                  </p>
                ))}
            </div>
          </PanelBody>
        </Panel>
      )}
      {networkInterface && (
        <>
          <StatGrid columns={2}>
            <StatTile label="Drops since boot" value={drops?.toLocaleString() ?? "Unavailable"} />
            <StatTile
              label="Drops during observation"
              value={delta?.toLocaleString() ?? "Waiting for two readings"}
              hint={
                delta !== undefined
                  ? `${elapsed.toFixed(1)} seconds`
                  : "Counters reset with the interface"
              }
            />
          </StatGrid>
          <p className="text-body text-muted-foreground">
            Cumulative drops describe the interface’s history. A new delta indicates drops during
            this observation; it does not identify a responsible process. Inspect the interface,
            routes and diagnostic tools before changing its configuration.
          </p>
        </>
      )}
      {current && finding.id === "timewait" && (
        <>
          <StatGrid columns={2}>
            <StatTile
              label="TIME_WAIT sockets"
              value={current.sockets.tcpTimeWait.toLocaleString()}
            />
            <StatTile label="TCP in use" value={current.sockets.tcpInUse.toLocaleString()} />
          </StatGrid>
          <p className="text-body text-muted-foreground">
            TIME_WAIT records remain after a socket closes and often have no live process owner.
            Inspect active connections and client workloads. Review connection pooling; avoid
            blindly shortening kernel TCP timers.
          </p>
        </>
      )}
      {current && (
        <p className="text-hint text-muted-foreground">Measured {timestamp(current.ts)}</p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" size="sm" onClick={report.refresh}>
          Measure again
        </Button>
        <Button variant="outline" size="sm" asChild>
          <Link href="/metrics">Recorded history</Link>
        </Button>
        {target?.kind === "network" && (
          <>
            <Button variant="outline" size="sm" asChild>
              <Link href={networkInterface ? "/security/network" : "/security/connections"}>
                {networkInterface ? "Inspect network" : "Inspect connections"}
              </Link>
            </Button>
            <Button variant="outline" size="sm" asChild>
              <Link href="/security/tools">Network diagnostics</Link>
            </Button>
          </>
        )}
        {target?.kind === "external" && finding.id.startsWith("temp:") && (
          <Button variant="outline" size="sm" asChild>
            <Link href="/processes">Inspect workloads</Link>
          </Button>
        )}
      </div>
    </div>
  )
}
