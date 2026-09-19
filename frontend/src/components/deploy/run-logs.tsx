"use client"

import { useState } from "react"
import { LogWorkspace } from "@/components/logs/log-workspace"
import type { LogFilterState, LogMode, LogTimeRange } from "@/components/logs/types"
import { EMPTY_FILTER } from "@/lib/log-filter"
import { Panel, PanelBody } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { FilterChip } from "@/components/tabs"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"

type RunLogSource = { containerId: string; name: string; liveUrl: string; activationUrl?: string }

type RunLogHandoff = {
  status: "available" | "unavailable"
  reason?: string
  windowReason?: string
  activationCompletedAt?: string
  sources: RunLogSource[]
}

/**
 * The application's own runtime logs for this run's containers — separate
 * from the build transcript, which is the engine's own narration.
 */
export function RunLogs({ projectId, runId }: { projectId: number; runId: number }) {
  const result = usePoll(
    (signal) => get<RunLogHandoff>(`/deploy/${projectId}/runs/${runId}/logs`, undefined, signal),
    5000,
    [projectId, runId],
  )
  const [picked, setPicked] = useState<string>()
  const [activation, setActivation] = useState(false)
  const sources = result.data?.sources ?? []
  const selected = picked ? sources.find((source) => source.containerId === picked) : sources[0]

  return (
    <Panel plain>
      {/* No title: the tab already says "Runtime logs". What a reader needs
          told is what these are — the application's own output, not the
          build's — and where the two views of it look. */}
      <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-2">
        <p className="max-w-prose text-hint text-muted-foreground">
          {selected
            ? `What ${selected.name || selected.containerId.slice(0, 12)} prints while it runs — the container this release started, separate from the build transcript. Live follows it now; Around activation shows the five minutes either side of the release going live.`
            : "The application's own output from the containers this release started, separate from the build transcript."}
        </p>
        {sources.length > 0 && (
          <Select
            value={selected?.containerId ?? ""}
            onValueChange={(value) => {
              setPicked(value)
              setActivation(false)
            }}
          >
            <SelectTrigger size="sm" aria-label="Runtime log source" className="w-56">
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
        )}
      </div>
      <PanelBody flush className="space-y-3 pt-3">
        {result.error ? (
          <ErrorState error={result.error} />
        ) : !result.data ? (
          <LoadingRows />
        ) : result.data.status === "unavailable" ? (
          <EmptyNote>{result.data.reason}</EmptyNote>
        ) : (
          <>
            {result.data.windowReason && (
              <p className="text-hint text-muted-foreground">{result.data.windowReason}</p>
            )}
            {!selected ? (
              <EmptyNote>
                {picked
                  ? "This service is no longer available. Choose another service to view its logs."
                  : "No managed runtime logs yet. Logs appear here when a container is created."}
              </EmptyNote>
            ) : (
              <>
                {selected.activationUrl && (
                  <FilterChip selected={activation} onClick={() => setActivation(!activation)}>
                    {activation ? "Return to live logs" : "Around activation"}
                  </FilterChip>
                )}
                <ScopedLogWorkspace
                  key={`${selected.containerId}:${activation}`}
                  source={selected}
                  activation={activation}
                />
              </>
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * One source, opened either at the live tail or at the server-computed window
 * around this run's activation — ported from the old `deployment-logs.tsx` so
 * this file owns every behaviour of a run's own log handoff.
 */
function ScopedLogWorkspace({ source, activation }: { source: RunLogSource; activation: boolean }) {
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
