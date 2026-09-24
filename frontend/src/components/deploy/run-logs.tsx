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
import { ProductGlyph } from "@/components/product-logo"
import { serviceProduct } from "@/components/deploy/service-product"
import { get } from "@/lib/api"
import { clockMinute, minuteSpan } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentSourceKind } from "@/lib/types"

type RunLogSource = {
  containerId: string
  name: string
  image?: string
  liveUrl: string
  activationUrl?: string
}

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
 *
 * No title and no caption: the view strip already says "Runtime logs". The
 * workspace's own strip names the container, drawn as the product its image
 * is, and switches it between the live tail and its history; a picker above
 * it appears only when the run has more than one container to choose. The
 * window the server computed around the release going live is one more way
 * to read the same container — a chip that opens the history there, with the
 * window's times as the strip's facts while it is open — rather than a
 * second Live beside the workspace's own.
 */
export function RunLogs({
  projectId,
  runId,
  kind,
  product,
}: {
  projectId: number
  runId: number
  /**
   * Where the project's source comes from and what the project is, so a
   * container of its own build is drawn as the project, as Runtime draws it.
   */
  kind?: DeploymentSourceKind
  product?: string
}) {
  const result = usePoll(
    (signal) => get<RunLogHandoff>(`/deploy/${projectId}/runs/${runId}/logs`, undefined, signal),
    5000,
    [projectId, runId],
  )
  const [picked, setPicked] = useState<string>()
  const [activation, setActivation] = useState(false)
  const sources = result.data?.sources ?? []
  const selected = picked ? sources.find((source) => source.containerId === picked) : sources[0]
  const around =
    activation && selected?.activationUrl
      ? new URL(selected.activationUrl, "http://localhost").searchParams
      : undefined
  const since = around?.get("since")
  const until = around?.get("until")
  const wentLive = result.data?.activationCompletedAt

  return (
    <Panel plain>
      {(sources.length > 1 || selected?.activationUrl) && (
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          {sources.length > 1 && (
            <Select
              value={selected?.containerId ?? ""}
              onValueChange={(value) => {
                setPicked(value)
                setActivation(false)
              }}
            >
              <SelectTrigger
                size="sm"
                aria-label="Runtime log source"
                className="w-full max-sm:basis-full sm:w-56"
              >
                <SelectValue placeholder="Choose a service" />
              </SelectTrigger>
              <SelectContent>
                {sources.map((source) => (
                  <SelectItem key={source.containerId} value={source.containerId}>
                    <ProductGlyph id={serviceProduct(source.image, kind, product)} />
                    {source.name || source.containerId}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          {selected?.activationUrl && (
            <FilterChip selected={activation} onClick={() => setActivation(!activation)}>
              Around activation
            </FilterChip>
          )}
        </div>
      )}
      <PanelBody flush className="space-y-3 pt-3">
        {result.error ? (
          <ErrorState error={result.error} />
        ) : !result.data ? (
          <LoadingRows />
        ) : result.data.status === "unavailable" ? (
          <EmptyNote className="px-0 text-left">{result.data.reason}</EmptyNote>
        ) : (
          <>
            {result.data.windowReason && (
              <p className="text-hint text-muted-foreground">{result.data.windowReason}</p>
            )}
            {!selected ? (
              <EmptyNote className="px-0 text-left">
                {picked
                  ? "This service is no longer available. Choose another service to view its logs."
                  : "No managed runtime logs yet. Logs appear here when a container is created."}
              </EmptyNote>
            ) : (
              <ScopedLogWorkspace
                key={`${selected.containerId}:${activation}`}
                source={selected}
                product={serviceProduct(selected.image, kind, product)}
                activation={activation}
                // Live is the workspace's own tab; pressed from the window,
                // it leaves the window.
                onLive={() => setActivation(false)}
                facts={
                  since &&
                  until && (
                    <span className="numeric shrink-0 text-hint text-muted-foreground">
                      {minuteSpan(since, until)}
                      {wentLive && (
                        <span className="max-sm:hidden"> · went live {clockMinute(wentLive)}</span>
                      )}
                    </span>
                  )
                }
              />
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
function ScopedLogWorkspace({
  source,
  product,
  activation,
  onLive,
  facts,
}: {
  source: RunLogSource
  product: string
  activation: boolean
  onLive: () => void
  facts?: React.ReactNode
}) {
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
      className="h-[min(75vh,40rem)] min-h-80"
      source={{ id: sourceId, label: source.name, kind: "docker", rotated: false }}
      sourceId={sourceId}
      units={[]}
      leading={<ProductGlyph id={product} />}
      facts={facts}
      mode={mode}
      onModeChange={(next) => {
        if (activation && next === "live") onLive()
        else setMode(next)
      }}
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
