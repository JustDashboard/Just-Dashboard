"use client"

import { useState } from "react"
import { useSearchParams } from "next/navigation"
import { LogWorkspace } from "@/components/logs/log-workspace"
import type { LogFilterState, LogMode, LogTimeRange } from "@/components/logs/types"
import { EMPTY_FILTER } from "@/lib/log-filter"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { FilterChip } from "@/components/tabs"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useProject } from "@/components/deploy/project-context"

type LogSource = { containerId: string; name: string; activationUrl?: string }

// `GET .../runs/{run}/logs` — the same handoff `run-logs.tsx` reads for a
// single run, kept local (types.ts has no shape for it) rather than guessed
// from `detail.runtime.services`, which carries no `activationUrl` at all.
type RunLogs = {
  status: "available" | "unavailable"
  reason?: string
  sources: LogSource[]
}

/**
 * The live release's own logs, scoped to one service at a time. Ported from
 * the pre-rebuild `DeploymentLogSources`/`ScopedLogWorkspace` — same log
 * workspace, same activation-window behaviour — inside a `Pane` rather than a
 * bare div, since a log console is a working region with its own scrolling.
 */
export function ProjectLogs() {
  const project = useProject()
  const { runtime } = project.detail
  const liveRun = project.liveRun
  // The runtime snapshot names the containers but never their activation
  // window; the live run's own log handoff (`run-logs.tsx`'s endpoint) is
  // the one place `activationUrl` is computed, so it is read here too.
  const runLogs = usePoll(
    (signal) =>
      liveRun
        ? get<RunLogs>(`/deploy/${project.projectId}/runs/${liveRun.id}/logs`, undefined, signal)
        : Promise.resolve(undefined),
    5000,
    [project.projectId, liveRun?.id],
    { enabled: Boolean(liveRun) },
  )

  if (runtime?.status !== "available") {
    return (
      <Panel plain>
        <PanelHeader title="Runtime logs" />
        <PanelBody>
          <EmptyNote>{runtime?.reason || "Runtime services are unavailable."}</EmptyNote>
        </PanelBody>
      </Panel>
    )
  }
  const sources =
    runLogs.data?.sources ??
    runtime.services.map((service) => ({ containerId: service.containerId, name: service.name }))
  return <LogSources sources={sources} />
}

function LogSources({ sources }: { sources: LogSource[] }) {
  const search = useSearchParams()
  const [activation, setActivation] = useState(false)
  const [picked, setPicked] = useState<string | null>(() => search.get("service"))
  const selected = picked ? sources.find((source) => source.containerId === picked) : sources[0]

  return (
    <Panel plain>
      <PanelHeader
        title="Runtime logs"
        actions={
          sources.length > 0 && (
            <>
              {selected?.activationUrl && (
                <FilterChip selected={activation} onClick={() => setActivation(!activation)}>
                  Around activation
                </FilterChip>
              )}
              <Select
                value={selected?.containerId ?? ""}
                onValueChange={(value) => {
                  setPicked(value)
                  setActivation(false)
                }}
              >
                <SelectTrigger size="sm" className="w-56" aria-label="Runtime log source">
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
            </>
          )
        }
      />
      <PanelBody>
        {selected ? (
          <ScopedLogWorkspace
            key={`${selected.containerId}:${activation}`}
            source={selected}
            activation={activation}
          />
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

function ScopedLogWorkspace({ source, activation }: { source: LogSource; activation: boolean }) {
  const activationParams = new URL(source.activationUrl || "/", "http://localhost").searchParams
  const [mode, setMode] = useState<LogMode>(activation ? "search" : "live")
  const [filter, setFilter] = useState<LogFilterState>(EMPTY_FILTER)
  const [range, setRange] = useState<LogTimeRange>(activation ? "custom" : "1h")
  const [since, setSince] = useState(activation ? activationParams.get("since") || "" : "")
  const [until, setUntil] = useState(activation ? activationParams.get("until") || "" : "")
  const [context, setContext] = useState(0)
  const [archives, setArchives] = useState(false)
  const [boot, setBoot] = useState(false)
  const [unit, setUnit] = useState("")
  const sourceId = `docker:${source.containerId}`
  return (
    <LogWorkspace
      className="h-[40rem]"
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
  )
}
