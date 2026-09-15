"use client"

import { useCallback, useState } from "react"
import { ContainerUsage } from "@/components/docker/container-usage"
import { Metric, MetricStrip } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { bytes, percent } from "@/lib/format"
import type { ContainerStats, DeploymentRuntimeServices } from "@/lib/types"

export function DeploymentMetrics({ runtime }: { runtime?: DeploymentRuntimeServices }) {
  const [picked, setPicked] = useState<string | null>(null)
  const services = runtime?.status === "available" ? runtime.services : []
  const selected = picked
    ? services.find((service) => service.containerId === picked)
    : (services.find((service) => service.liveRelease) ?? services[0])
  return (
    <div className="space-y-4">
      <Panel>
        <PanelHeader
          title="Resource usage"
          actions={
            services.length > 0 && (
              <Select value={selected?.containerId ?? ""} onValueChange={setPicked}>
                <SelectTrigger className="w-56" aria-label="Metrics service">
                  <SelectValue placeholder="Choose a service" />
                </SelectTrigger>
                <SelectContent>
                  {services.map((service) => (
                    <SelectItem key={service.containerId} value={service.containerId}>
                      {service.name || service.containerId}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )
          }
        />
        <PanelBody>
          {selected ? (
            <LiveUsage key={selected.containerId} containerId={selected.containerId} />
          ) : (
            <EmptyNote>
              {runtime?.status !== "available"
                ? runtime?.reason || "Runtime metrics are unavailable."
                : picked
                  ? "This service is no longer running. Choose another service."
                  : "Metrics appear when a managed service starts."}
            </EmptyNote>
          )}
        </PanelBody>
      </Panel>
      {selected && (
        <ContainerUsage
          key={selected.containerId}
          containerId={selected.containerId}
          name={selected.name || selected.containerId}
        />
      )}
    </div>
  )
}

function LiveUsage({ containerId }: { containerId: string }) {
  const [stats, setStats] = useState<ContainerStats | null>(null)
  const [error, setError] = useState<string | null>(null)
  const onMessage = useCallback((message: Envelope) => {
    if (message.error) {
      setError(message.error)
      return
    }
    if (message.type === "stats") {
      setStats(message.data as ContainerStats)
      setError(null)
    }
  }, [])
  const { state } = useSocket(
    `/docker/containers/${encodeURIComponent(containerId)}/stats/stream`,
    { onMessage },
  )
  const current = state === "open" && !error
  return (
    <div className="space-y-3">
      <Status
        tone={current && stats ? "running" : "unknown"}
        label={
          error
            ? "Unavailable"
            : state === "connecting"
              ? "Connecting"
              : current
                ? stats
                  ? "Live"
                  : "Waiting for a sample"
                : "Disconnected · last sample"
        }
      />
      {error && <ErrorState error={new Error(error)} />}
      <MetricStrip>
        <Metric label="CPU · 100% = 1 core" value={stats ? percent(stats.cpuPercent) : "—"} />
        <Metric
          label="Memory"
          value={stats ? bytes(stats.memUsage) : "—"}
          hint={stats?.memLimited ? `of ${bytes(stats.memLimit)}` : "No container limit"}
        />
        <Metric label="Processes" value={stats ? String(stats.pids) : "—"} />
        <Metric label="Last sample" value={stats ? new Date(stats.ts).toLocaleTimeString() : "—"} />
      </MetricStrip>
    </div>
  )
}
