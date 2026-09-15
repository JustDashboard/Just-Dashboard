"use client"

import { useState } from "react"
import { useSearchParams } from "next/navigation"
import { LogWorkspace } from "@/components/logs/log-workspace"
import type { LogFilterState, LogMode, LogTimeRange } from "@/components/logs/types"
import { Button } from "@/components/ui/button"
import { EMPTY_FILTER } from "@/lib/log-filter"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type { DeploymentRuntimeServices } from "@/lib/types"

export type DeploymentLogSource = { containerId: string; name: string; activationUrl?: string }

export function DeploymentLogs({ runtime }: { runtime?: DeploymentRuntimeServices }) {
  if (runtime?.status !== "available") {
    return (
      <Panel>
        <PanelHeader title="Runtime logs" />
        <PanelBody>
          <EmptyNote>{runtime?.reason || "Runtime services are unavailable."}</EmptyNote>
        </PanelBody>
      </Panel>
    )
  }
  return <DeploymentLogSources sources={runtime.services} />
}

export function DeploymentLogSources({ sources }: { sources: DeploymentLogSource[] }) {
  const search = useSearchParams()
  const [activation, setActivation] = useState(false)
  const [picked, setPicked] = useState<string | null>(() => search.get("service"))
  const selected = picked ? sources.find((source) => source.containerId === picked) : sources[0]
  return (
    <Panel>
      <PanelHeader
        title="Runtime logs"
        actions={
          sources.length > 0 && (
            <Select
              value={selected?.containerId ?? ""}
              onValueChange={(value) => {
                setPicked(value)
                setActivation(false)
              }}
            >
              <SelectTrigger className="w-56" aria-label="Runtime log source">
                <SelectValue placeholder="Choose a service" />
              </SelectTrigger>
              <SelectContent>
                {sources.map((source) => (
                  <SelectItem key={source.containerId} value={source.containerId}>
                    {source.name || source.containerId}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )
        }
      />
      <PanelBody>
        {selected ? (
          <div className="space-y-3">
            {selected.activationUrl && (
              <Button size="sm" variant="outline" onClick={() => setActivation(!activation)}>
                {activation ? "Return to live logs" : "Around activation"}
              </Button>
            )}
            <ScopedLogWorkspace
              key={`${selected.containerId}:${activation}`}
              source={selected}
              activation={activation}
            />
          </div>
        ) : (
          <EmptyNote>
            {picked
              ? "This service is no longer available. Choose another service to view its logs."
              : "No managed runtime logs yet. Logs appear here when a container is created."}
          </EmptyNote>
        )}
      </PanelBody>
    </Panel>
  )
}

function ScopedLogWorkspace({
  source,
  activation,
}: {
  source: DeploymentLogSource
  activation: boolean
}) {
  const window = new URL(source.activationUrl || "/", "http://localhost").searchParams
  const [mode, setMode] = useState<LogMode>(activation ? "search" : "live")
  const [filter, setFilter] = useState<LogFilterState>(EMPTY_FILTER)
  const [range, setRange] = useState<LogTimeRange>(activation ? "custom" : "1h")
  const [since, setSince] = useState(activation ? window.get("since") || "" : "")
  const [until, setUntil] = useState(activation ? window.get("until") || "" : "")
  const [context, setContext] = useState(0)
  const [archives, setArchives] = useState(false)
  const [boot, setBoot] = useState(false)
  const [unit, setUnit] = useState("")
  const sourceId = `docker:${source.containerId}`
  return (
    <div className="flex h-[40rem] min-w-0 flex-col">
      <LogWorkspace
        source={{ id: sourceId, label: source.name, kind: "docker", rotated: false }}
        sourceId={sourceId}
        units={[]}
        mode={mode}
        onModeChange={setMode}
        filter={filter}
        onFilterChange={setFilter}
        unit={unit}
        onUnitChange={setUnit}
        range={range}
        onRangeChange={setRange}
        since={since}
        until={until}
        onSinceChange={setSince}
        onUntilChange={setUntil}
        onCustomRange={(from, to) => {
          setRange("custom")
          setSince(from.toISOString())
          setUntil(to.toISOString())
        }}
        context={context}
        onContextChange={setContext}
        archives={archives}
        onArchivesChange={setArchives}
        boot={boot}
        onBootChange={setBoot}
      />
    </div>
  )
}
