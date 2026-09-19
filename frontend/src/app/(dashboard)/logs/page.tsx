"use client"

import { useEffect, useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Logs, SidebarLeft } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import type { LogSource, LogSourceIndex } from "@/lib/types"
import { EMPTY_FILTER, readLogWindow } from "@/lib/log-filter"
import type { LogFilterState, LogMode, LogTimeRange } from "@/components/logs/types"
import { usePoll } from "@/hooks/use-poll"
import { usePanelSize } from "@/lib/panel-size"
import { useSessionState, useViewState } from "@/lib/view-state"
import { Metric, MetricStrip, Page, PageHeader } from "@/components/page"
import { EmptyState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { IconAction } from "@/components/icon-action"
import { ResizeHandle } from "@/components/resize-handle"
import { Button } from "@/components/ui/button"
import { KIND_TAG, SourceRail } from "@/components/logs/source-rail"
import { ExportDialog } from "@/components/logs/export-dialog"
import { LogWorkspace } from "@/components/logs/log-workspace"

const RAIL = { min: 208, max: 420, base: 272 }

function toLocalInput(date: Date) {
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 23)
}

/**
 * The logs page is a workbench, like the terminal: one frame around the whole
 * screen, the sources down the left and the lines on the right, a hairline
 * between them. The rail hides and resizes the way the terminal's session
 * rail does, and remembers both.
 */
export default function LogsPage() {
  const params = useSearchParams()
  const sources = usePoll(
    (signal) => get<LogSourceIndex>("/logs/sources", undefined, signal),
    60000,
  )

  // Keep exact instants from shared links; converting them to local input values
  // before searching would lose the offset during a repeated daylight-saving hour.
  const [initialWindow] = useState(() => readLogWindow(params))
  const [windowError, setWindowError] = useState(initialWindow.error)
  // A link into the page is a complete question and sets the whole window;
  // arriving bare — the rail's own link — reopens the window this tab had,
  // which the effect below then writes back into the address bar.
  const linked = ["source", "mode", "q", "unit", "since", "until"].some((key) => params.has(key))
  const arrival = <T,>(value: T) => (linked ? value : undefined)
  const [picked, setPicked] = useSessionState(
    "logs.source",
    "",
    arrival(params.get("source") ?? ""),
  )
  const [mode, setMode] = useSessionState<LogMode>(
    "logs.mode",
    "live",
    arrival(
      params.get("mode") === "search" || initialWindow.since || initialWindow.until
        ? "search"
        : "live",
    ),
  )
  const [filter, setFilter] = useSessionState<LogFilterState>(
    "logs.filter",
    EMPTY_FILTER,
    arrival({ ...EMPTY_FILTER, q: params.get("q") ?? "" }),
  )
  const [unit, setUnit] = useSessionState(
    "logs.unit",
    "",
    arrival(
      params.get("unit") ??
        (params.get("source")?.startsWith("journal:")
          ? params.get("source")!.slice("journal:".length)
          : ""),
    ),
  )
  const [range, setRange] = useSessionState<LogTimeRange>(
    "logs.range",
    "24h",
    arrival(initialWindow.since || initialWindow.until ? "custom" : "24h"),
  )
  const [since, setSince] = useSessionState("logs.since", "", arrival(initialWindow.since))
  const [until, setUntil] = useSessionState("logs.until", "", arrival(initialWindow.until))
  const [context, setContext] = useSessionState("logs.context", 0)
  const [archives, setArchives] = useSessionState("logs.archives", false)
  const [boot, setBoot] = useSessionState("logs.boot", false)
  const [showRail, setShowRail] = useViewState("logs.rail", true)
  const [railWidth, setRailWidth, resetRailWidth] = usePanelSize("logs.rail", RAIL.base)
  const railPx = Math.max(RAIL.min, Math.min(RAIL.max, railWidth))

  // The first source is streaming before you choose one. Landing on an empty
  // pane and a "pick something" sign wastes the visit: nine times out of ten
  // the answer is in syslog, and the operator can switch in one click if it is
  // not. Derived rather than stored, so no effect has to sync it.
  const selected: LogSource | null = useMemo(() => {
    const list = sources.data?.sources ?? []
    if (!picked) return list[0] ?? null
    return (
      list.find(
        (s) => s.id === picked || (picked.startsWith("journal:") && s.kind === "journal"),
      ) ?? null
    )
  }, [sources.data, picked])

  // The journal is one source with a thousand faces, so the unit rides on the
  // id rather than filling the rail with systemd's inventory.
  const sourceId = useMemo(() => {
    if (!selected) return ""
    if (selected.kind === "journal" && unit) return `journal:${unit}`
    return selected.id
  }, [selected, unit])

  useEffect(() => {
    if (!sourceId || windowError) return
    const url = new URL(window.location.href)
    url.searchParams.set("source", sourceId)
    if (mode === "search") url.searchParams.set("mode", "search")
    else url.searchParams.delete("mode")
    if (filter.q) url.searchParams.set("q", filter.q)
    else url.searchParams.delete("q")
    for (const [key, value] of Object.entries(
      range === "custom" ? { since, until } : { since: "", until: "" },
    )) {
      if (value && Number.isFinite(Date.parse(value)))
        url.searchParams.set(key, new Date(value).toISOString())
      else url.searchParams.delete(key)
    }
    window.history.replaceState(null, "", url)
  }, [sourceId, mode, filter.q, range, since, until, windowError])

  const archiveCount = useMemo(
    () => (sources.data?.sources ?? []).reduce((n, s) => n + (s.archives ?? 0), 0),
    [sources.data],
  )
  const archiveBytes = useMemo(
    () => (sources.data?.sources ?? []).reduce((n, s) => n + (s.archiveBytes ?? 0), 0),
    [sources.data],
  )

  const railToggle = (
    <IconAction
      label={showRail ? "Hide the sources" : "Show the sources"}
      aria-pressed={showRail}
      className="size-7 shrink-0"
      onClick={() => setShowRail((value) => !value)}
    >
      <SidebarLeft />
    </IconAction>
  )

  return (
    <Page fill className="gap-4 md:gap-5">
      <PageHeader
        eyebrow="Server"
        title="Logs"
        actions={
          <>
            {sources.data && (
              <MetricStrip className="animate-rise">
                <Metric label="Sources" value={sources.data.sources.length} />
                {sources.data.units.length > 0 && (
                  <Metric label="Units" value={sources.data.units.length} />
                )}
                {archiveCount > 0 && (
                  <Metric label="Archives" value={archiveCount} hint={bytes(archiveBytes)} />
                )}
              </MetricStrip>
            )}
            {sourceId && (
              <ExportDialog sourceId={sourceId} source={selected} filter={filter} boot={boot} />
            )}
          </>
        }
      />

      {/* One frame around the whole workbench. The rail and the lines are
          separated by a hairline rather than by a gutter and two borders: two
          framed panes with a gap between them read as two boxes floating on
          the page, and the screen is one working surface. */}
      <div
        style={{ "--jd-rail": `${railPx}px` } as React.CSSProperties}
        className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-xl border bg-card lg:flex-row"
      >
        {showRail && (
          <div className="relative flex max-h-64 shrink-0 border-b border-hairline lg:max-h-none lg:w-(--jd-rail) lg:border-r lg:border-b-0">
            <SourceRail
              index={sources.data}
              loading={sources.loading}
              error={sources.error}
              selectedId={selected?.id ?? null}
              onSelect={(source) => {
                setPicked(source.id)
                if (source.kind !== "journal") setUnit("")
              }}
              onRescan={() => sources.refresh()}
            />
            <ResizeHandle
              side="left"
              label="Sources panel width"
              value={railPx}
              min={RAIL.min}
              max={RAIL.max}
              onChange={(px, commit) => setRailWidth(px, commit)}
              onReset={resetRailWidth}
              className="absolute inset-y-0 -right-1 z-20"
            />
          </div>
        )}

        {windowError ? (
          <Blank leading={railToggle}>
            <EmptyState
              icon={Logs}
              title="Invalid log window"
              description={windowError}
              action={
                <Button
                  variant="outline"
                  onClick={() => {
                    setWindowError(undefined)
                    setRange("24h")
                    setSince("")
                    setUntil("")
                  }}
                >
                  Use last 24 hours
                </Button>
              }
            />
          </Blank>
        ) : selected ? (
          <LogWorkspace
            key={sourceId}
            flush
            leading={railToggle}
            facts={<SourceFacts source={selected} />}
            source={selected}
            sourceId={sourceId}
            units={sources.data?.units ?? []}
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
              setSince(toLocalInput(from))
              setUntil(toLocalInput(to))
            }}
            context={context}
            onContextChange={setContext}
            archives={archives}
            onArchivesChange={setArchives}
            boot={boot}
            onBootChange={setBoot}
          />
        ) : (
          <Blank leading={railToggle}>
            <EmptyState
              icon={Logs}
              title={
                sources.loading
                  ? "Looking for logs…"
                  : picked
                    ? "Requested log source unavailable"
                    : "No log sources on this host"
              }
              description={
                sources.loading
                  ? undefined
                  : picked
                    ? `The requested source (${picked}) is not in the current inventory. It may have been removed or its owner may be unavailable. Rescan or choose another source.`
                    : `Nothing readable was found under ${(sources.data?.roots ?? []).join(", ") || "the configured log roots"}. Containers, PM2 processes and the journal appear here too when they are present.`
              }
            />
          </Blank>
        )}
      </div>
    </Page>
  )
}

/**
 * What the chosen source is: its kind, where it lives, how big it is and
 * whether its writer is running. The facts sit beside the name in the
 * workspace's strip rather than under a title as a caption, and truncate
 * rather than wrap so the strip stays one line.
 */
function SourceFacts({ source }: { source: LogSource }) {
  const hasSize = source.size !== undefined && source.size > 0
  return (
    <span className="hidden min-w-0 items-center gap-x-3 text-hint text-muted-foreground md:flex">
      <Tag>{KIND_TAG[source.kind]}</Tag>
      {source.status && <Status state={source.status} className="text-hint" />}
      {source.path && (
        <span className="truncate font-mono" title={source.path}>
          {source.path}
        </span>
      )}
      {hasSize ? (
        <span className="numeric whitespace-nowrap">
          {bytes(source.size)} · {relativeTime(source.modified)}
        </span>
      ) : (
        source.detail && <span className="truncate">{source.detail}</span>
      )}
      {(source.archives ?? 0) > 0 && (
        <span className="numeric whitespace-nowrap">
          {source.archives} rotated · {bytes(source.archiveBytes)}
        </span>
      )}
    </span>
  )
}

/** The lines column with nothing to show: the rail toggle stays reachable. */
function Blank({ leading, children }: { leading: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex min-h-10 shrink-0 items-center border-b border-hairline px-2">
        {leading}
      </div>
      <div className="flex min-h-0 flex-1 items-center justify-center overflow-auto p-6">
        {children}
      </div>
    </div>
  )
}
