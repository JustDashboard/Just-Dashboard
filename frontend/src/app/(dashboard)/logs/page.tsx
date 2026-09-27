"use client"

import { useEffect, useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Logs, SidebarLeftClose, SidebarLeftOpen } from "@/components/icons"
import { get } from "@/lib/api"
import type { LogSource, LogSourceIndex } from "@/lib/types"
import {
  EMPTY_FILTER,
  fieldsFromParams,
  filterQuery,
  levelsFromParam,
  readLogWindow,
} from "@/lib/log-filter"
import { lensFor, withLensDefaults } from "@/lib/log-lenses"
import { journalSource } from "@/lib/log-sources"
import type { LogFilterState, LogMode, LogTimeRange } from "@/components/logs/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { usePanelSize } from "@/lib/panel-size"
import { useSessionState, useViewState } from "@/lib/view-state"
import { Page, PageContext } from "@/components/page"
import { EmptyState } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { ResizeHandle } from "@/components/resize-handle"
import { Button } from "@/components/ui/button"
import { SourceRail, railSources } from "@/components/logs/source-rail"
import { SourceFacts } from "@/components/logs/source-facts"
import { ExportDialog } from "@/components/logs/export-dialog"
import { LogWorkspace } from "@/components/logs/log-workspace"

const RAIL = { min: 208, max: 420, base: 272 }

/** The readings a link may open on; a page view's id joins these when `/logs` offers one. */
const MODES = ["live", "search", "insights"]

function validLens(id: string | null) {
  return id === "none" || lensFor(id ?? undefined) ? (id as string) : ""
}

/** What the server reads a source as: the unit's lens for one unit of the journal. */
function detectedLensOf(
  source: LogSource | null,
  unit: string,
  units: LogSourceIndex["units"],
): string | undefined {
  if (!source) return undefined
  if (source.kind === "journal" && unit) return units.find((u) => u.name === unit)?.lens
  return source.lens
}

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
  const { host } = useMetrics()
  const { can } = useAuth()
  const admin = can("system.admin")
  const sources = usePoll(
    (signal) => get<LogSourceIndex>("/logs/sources", undefined, signal),
    60000,
  )
  const listed = useMemo(() => railSources(sources.data, admin), [sources.data, admin])

  // Keep exact instants from shared links; converting them to local input values
  // before searching would lose the offset during a repeated daylight-saving hour.
  const [initialWindow] = useState(() => readLogWindow(params))
  const [windowError, setWindowError] = useState(initialWindow.error)
  // A link into the page is a complete question and sets the whole window;
  // arriving bare — the rail's own link — reopens the window this tab had,
  // which the effect below then writes back into the address bar.
  const linked = ["source", "mode", "q", "unit", "since", "until", "f", "levels", "lens"].some(
    (key) => params.has(key),
  )
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
      initialWindow.since || initialWindow.until
        ? "search"
        : MODES.includes(params.get("mode") ?? "")
          ? (params.get("mode") as LogMode)
          : "live",
    ),
  )
  // A link carries the whole question — its fields and levels too — with
  // anything the server would refuse dropped rather than sent.
  const [filter, setFilter] = useSessionState<LogFilterState>(
    "logs.filter",
    EMPTY_FILTER,
    arrival({
      ...EMPTY_FILTER,
      q: params.get("q") ?? "",
      levels: levelsFromParam(params.get("levels")),
      fields: fieldsFromParams(params.getAll("f")),
    }),
  )
  // The lens is in the address only when the reader forced one: a detected
  // lens is the source's own and would only go stale in a link.
  const [lens, setLens] = useSessionState("logs.lens", "", arrival(validLens(params.get("lens"))))
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
    const list = listed
    if (!picked) {
      return (
        list.find((s) => s.id === "file:/var/log/syslog") ??
        list.find((s) => s.kind === "system" && (s.label === "syslog" || s.label === "messages")) ??
        list.find((s) => s.kind === "journal") ??
        list[0] ??
        null
      )
    }
    return (
      list.find(
        (s) => s.id === picked || (picked.startsWith("journal:") && s.kind === "journal"),
      ) ?? null
    )
  }, [listed, picked])

  // The journal is one source with a thousand faces, so the unit rides on the
  // id rather than filling the rail with systemd's inventory.
  const sourceId = useMemo(() => {
    if (!selected) return ""
    if (selected.kind === "journal" && unit) return journalSource(unit)
    return selected.id
  }, [selected, unit])
  const units = sources.data?.units
  const detectedLens = useMemo(
    () => detectedLensOf(selected, unit, units ?? []),
    [selected, unit, units],
  )

  // A source read through another lens is a different vocabulary: its fields
  // would silently empty the new one (`event:slow` on auth.log), so they go,
  // and the new lens's own defaults take their place as chips. A lens the
  // reader forced was forced on the source they left.
  const switchSource = (source: LogSource, nextUnit: string) => {
    const next = detectedLensOf(source, nextUnit, units ?? [])
    const current = lens === "none" ? undefined : lens || detectedLens
    if (lens) setLens("")
    if (next !== current) {
      setFilter((f) => withLensDefaults({ ...f, fields: {} }, lensFor(next)))
    }
  }

  const predicates = filterQuery(filter).f
  const predicatesKey = JSON.stringify(predicates ?? [])
  useEffect(() => {
    if (!sourceId || windowError) return
    const url = new URL(window.location.href)
    url.searchParams.set("source", sourceId)
    if (mode !== "live") url.searchParams.set("mode", mode)
    else url.searchParams.delete("mode")
    if (filter.q) url.searchParams.set("q", filter.q)
    else url.searchParams.delete("q")
    url.searchParams.delete("f")
    for (const predicate of JSON.parse(predicatesKey) as string[]) {
      url.searchParams.append("f", predicate)
    }
    if (filter.levels.length) url.searchParams.set("levels", filter.levels.join(","))
    else url.searchParams.delete("levels")
    if (lens) url.searchParams.set("lens", lens)
    else url.searchParams.delete("lens")
    for (const [key, value] of Object.entries(
      range === "custom" ? { since, until } : { since: "", until: "" },
    )) {
      if (value && Number.isFinite(Date.parse(value)))
        url.searchParams.set(key, new Date(value).toISOString())
      else url.searchParams.delete(key)
    }
    window.history.replaceState(null, "", url)
  }, [
    sourceId,
    mode,
    filter.q,
    filter.levels,
    predicatesKey,
    lens,
    range,
    since,
    until,
    windowError,
  ])

  const railToggle = (
    <IconAction
      label={showRail ? "Hide the sources" : "Show the sources"}
      aria-pressed={showRail}
      className="size-7 shrink-0"
      onClick={() => setShowRail((value) => !value)}
    >
      {showRail ? <SidebarLeftClose /> : <SidebarLeftOpen />}
    </IconAction>
  )

  return (
    <Page fill className="gap-4 md:gap-5">
      <PageContext eyebrow="Server" title="Logs" />

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
              sources={listed}
              loading={sources.loading}
              error={sources.error}
              selectedId={selected?.id ?? null}
              onSelect={(source) => {
                switchSource(source, source.kind === "journal" ? unit : "")
                setPicked(source.id)
                if (source.kind !== "journal") setUnit("")
              }}
              onRescan={() => sources.refresh()}
              platform={host?.platform}
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
            actions={
              <ExportDialog
                sourceId={sourceId}
                source={selected}
                filter={filter}
                boot={boot}
                lens={lens}
              />
            }
            source={selected}
            sourceId={sourceId}
            units={sources.data?.units ?? []}
            mode={mode}
            onModeChange={setMode}
            filter={filter}
            onFilterChange={setFilter}
            unit={unit}
            onUnitChange={(next) => {
              switchSource(selected, next)
              setUnit(next)
            }}
            lens={lens}
            onLensChange={(next) => {
              setLens(next)
              setFilter((f) => ({ ...f, fields: {} }))
            }}
            detectedLens={detectedLens}
            insightReadings
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
