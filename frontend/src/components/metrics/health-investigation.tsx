"use client"

import Link from "next/link"
import { useState } from "react"
import { get } from "@/lib/api"
import { percent, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import { healthInvestigation } from "@/lib/server-advisor"
import type { HealthFinding, Snapshot } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { useNow } from "@/components/deploy/vocabulary"
import {
  AREA,
  LEVEL_TEXT,
  LEVEL_WASH,
  LEVEL_WORD,
  findingArea,
  heldFor,
} from "@/components/metrics/health-vocabulary"
import { ContainerFixes, ServiceFixes } from "@/components/metrics/health-fixes"
import { StorageAdvisor } from "@/components/metrics/storage-advisor"
import { WorkloadAdvisor } from "@/components/metrics/workload-advisor"

/**
 * One finding, opened: what is happening and why it matters on a tinted
 * ground, the measured facts behind the verdict, and then the fix — the named
 * services, containers, files or workloads with the controls that change
 * them, and a fresh reading after each one so the reader sees it worked.
 */
export function HealthInvestigation({
  finding,
  onOpenChange,
  onChanged,
  onFixed,
}: {
  finding: HealthFinding | null
  onOpenChange: (open: boolean) => void
  onChanged: () => void
  /** A control here succeeded against this finding; the list may mark it resolved. */
  onFixed?: (finding: HealthFinding) => void
}) {
  const target = finding ? healthInvestigation(finding.id) : undefined
  const Icon = finding ? AREA[findingArea(finding)].icon : undefined
  const changed = () => {
    onChanged()
    if (finding) onFixed?.(finding)
  }
  return (
    <SidePanel
      open={!!finding}
      onOpenChange={onOpenChange}
      title={
        finding && Icon ? (
          <>
            <Icon className={cn("size-4 shrink-0", LEVEL_TEXT[finding.level])} />
            <span className="min-w-0 truncate">{finding.title}</span>
          </>
        ) : (
          "Health investigation"
        )
      }
      description="Local evidence and the controls that fix it"
      width="lg"
      initialFocus="body"
    >
      {finding && target && (
        <div className="space-y-6">
          <Diagnosis finding={finding} />
          {target.kind === "storage" && (
            <StorageAdvisor
              key={finding.id}
              path={target.path}
              inodes={target.inodes}
              onChanged={changed}
            />
          )}
          {target.kind === "workloads" && (
            <WorkloadAdvisor key={finding.id} sort={target.sort} onChanged={changed} />
          )}
          {target.kind === "services" && (
            <ServiceFixes key={finding.id} subjects={finding.subjects} onChanged={changed} />
          )}
          {target.kind === "containers" && (
            <ContainerFixes key={finding.id} subjects={finding.subjects} onChanged={changed} />
          )}
          {(target.kind === "external" || target.kind === "network") && (
            <OtherEvidence key={finding.id} finding={finding} />
          )}
        </div>
      )}
    </SidePanel>
  )
}

/**
 * The verdict and its reasons, on the level's own ground: the measured sentence
 * first, the opinion under it, and the facts the server judged on as figures.
 */
function Diagnosis({ finding }: { finding: HealthFinding }) {
  const now = useNow(30_000)
  const held = heldFor(finding.since, now)
  return (
    <section
      aria-label="Diagnosis"
      className={cn("animate-rise space-y-3 rounded-xl border p-4", LEVEL_WASH[finding.level])}
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <Status verdict={finding.level} label={LEVEL_WORD[finding.level]} />
        {held && <span className="numeric text-hint text-muted-foreground">for {held}</span>}
      </div>
      <p className="text-body leading-relaxed font-medium">{finding.detail}</p>
      {finding.advice && (
        <p className="text-body leading-relaxed text-muted-foreground">{finding.advice}</p>
      )}
      {!!finding.evidence?.length && (
        <dl className="grid grid-cols-2 gap-x-4 gap-y-3 border-t border-hairline pt-3 sm:grid-cols-4">
          {finding.evidence.map((fact) => (
            <div key={fact.label} className="min-w-0">
              <dt className="eyebrow truncate">{fact.label}</dt>
              <dd className="numeric truncate text-title font-semibold">{fact.value}</dd>
            </div>
          ))}
        </dl>
      )}
    </section>
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
    <section aria-label="Live evidence" className="space-y-4">
      <h3 className="text-title font-semibold">
        {target?.kind === "external" ? "Where the fix is" : "Live readings"}
      </h3>
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
          <StatTile
            label="CPU steal now"
            value={percent(current.cpu.modes.steal)}
            tone={current.cpu.modes.steal >= 5 ? "warning" : "default"}
          />
          <StatTile label="Judged on" value={percent(finding.value)} hint="Recorded hour" />
        </StatGrid>
      )}
      {current && finding.id.startsWith("temp:") && (
        <StatGrid columns={3}>
          {current.sensors
            ?.filter((sensor) => sensor.name === finding.id.slice(5))
            .flatMap((sensor) => [
              <StatTile
                key="now"
                label="Reading"
                value={`${sensor.tempC.toFixed(0)}°C`}
                tone={sensor.critical && sensor.tempC >= sensor.critical ? "danger" : "warning"}
              />,
              <StatTile key="high" label="High" value={sensor.high ? `${sensor.high}°C` : "—"} />,
              <StatTile
                key="critical"
                label="Critical"
                value={sensor.critical ? `${sensor.critical}°C` : "—"}
              />,
            ])}
        </StatGrid>
      )}
      {networkInterface && (
        <>
          <StatGrid columns={2}>
            <StatTile label="Drops since boot" value={drops?.toLocaleString() ?? "Unavailable"} />
            <StatTile
              label="While watching"
              value={delta?.toLocaleString() ?? "…"}
              tone={delta ? "warning" : delta === 0 ? "success" : "default"}
              hint={
                delta !== undefined
                  ? `over ${elapsed.toFixed(0)} seconds`
                  : "Waiting for a second reading"
              }
            />
          </StatGrid>
          <p className="text-body leading-relaxed text-muted-foreground">
            The second figure is what matters: drops happening now. If it stays at zero the
            interface is healthy and the history is an old incident. If it climbs, compare the
            interface’s throughput and errors, then its MTU and queue settings.
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
          <p className="text-body leading-relaxed text-muted-foreground">
            These remain after a connection closes and usually have no live owner. The fix is in the
            client that opens them: reuse connections (keep-alive or a pool) instead of opening one
            per request. Shortening kernel timers hides the symptom.
          </p>
        </>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="outline" size="sm" onClick={report.refresh}>
          Measure again
        </Button>
        <Button variant="outline" size="sm" asChild>
          <Link href="/metrics">Recorded history</Link>
        </Button>
        {target?.kind === "network" && (
          <>
            <Button variant="outline" size="sm" asChild>
              <Link href={networkInterface ? "/network/interfaces" : "/network/connections"}>
                {networkInterface ? "Open interface" : "Open connections"}
              </Link>
            </Button>
            <Button variant="outline" size="sm" asChild>
              <Link href="/network/tools">Network diagnostics</Link>
            </Button>
          </>
        )}
        {target?.kind === "external" && finding.id.startsWith("temp:") && (
          <Button variant="outline" size="sm" asChild>
            <Link href="/processes">Busiest processes</Link>
          </Button>
        )}
        {current && (
          <span className="numeric ml-auto text-hint text-muted-foreground">
            Measured {timestamp(current.ts)}
          </span>
        )}
      </div>
    </section>
  )
}
