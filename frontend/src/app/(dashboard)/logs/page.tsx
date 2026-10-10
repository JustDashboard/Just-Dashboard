"use client"

import { useEffect, useMemo, useState } from "react"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useQuestionHistory, useHistoryVisit } from "@/components/workspace/history"
import { useRouter, useSearchParams } from "next/navigation"
import { Logs, SidebarLeftClose, SidebarLeftOpen } from "@/components/icons"
import { cn } from "@/lib/utils"
import { get } from "@/lib/api"
import type { DbFleetEntry, LogSource, LogSourceIndex } from "@/lib/types"
import {
  EMPTY_FILTER,
  fieldsFromParams,
  filterQuery,
  levelsFromParam,
  readLogWindow,
  resolveRange,
} from "@/lib/log-filter"
import { STACK_LENS, lensFor, withLensDefaults } from "@/lib/log-lenses"
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
import { SourceRail, railSources, sourceProduct } from "@/components/logs/source-rail"
import { SourceIdentity } from "@/components/logs/source-facts"
import { ExportDialog } from "@/components/logs/export-dialog"
import { askOf, withAsk } from "@/components/logs/logs-model"
import { LogWorkspace } from "@/components/logs/log-workspace"
import { useLensReadings } from "@/components/logs/lens-readings"
import type {
  LogWindow,
  ServiceLogSource,
  ServiceLogsContext,
} from "@/components/logs/service-logs"
import { serviceViews, useServiceFinds } from "@/components/logs/service-views"
import {
  RecordColumn,
  RecordIdentity,
  recordQueryKey,
  useRequestRecords,
} from "@/components/logs/request-records"
import { railSourceFor } from "@/components/logs/service-views-model"
import { sectionHref } from "@/components/database/engine"
import type { RequestsView } from "@/components/deploy/requests-workspace"
import {
  EMPTY_REQUEST_QUERY,
  QUERY_PARAMS,
  completeQuery,
  queryFromParams,
  queryParams,
  type RequestQuery,
} from "@/components/deploy/logs-model"

const RAIL = { min: 208, max: 420, base: 272 }

/** The readings every source offers; a page view's id — events, runs, queries — joins them where the source has it. */
const MODES = ["live", "search", "insights"]

/** The lines' column where it stacks under the rail: a window's height, not what is left of one. */
const COLUMN = "max-lg:h-[max(32rem,calc(100dvh-5rem))] max-lg:flex-none"

/** A page view's id as a link may name it, before the views it could name are known. */
const VIEW_ID = /^[a-z][a-z-]{0,31}$/

/** History on a stretch of time, narrowed, on this source or the one a view names. */
type OpenAt = LogWindow & { source?: string }

/** The words a link into a source says; a record's link says its own in their place. */
const SOURCE_PARAMS = ["source", "mode", "q", "unit", "since", "until", "f", "levels", "lens"]

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
 *
 * Over the frame, the page reads the way a deployment's Logs page does: the
 * chosen source as its identity line, with Export and the shortcuts at its
 * end, then the frame, whose lens row counts what the log adds up to.
 *
 * It is every service page's reading in one place. A source is offered the
 * views its own page has beside Live, History and Insights — a container's
 * Events, a unit's Runs, a saved database's Queries, a site's Requests — and
 * the rail lists the request records beside the logs they are read from,
 * each read in the same column under the same strip.
 */
export default function LogsPage() {
  const visit = useHistoryVisit()
  return <LogsScreen key={visit} />
}

function LogsScreen() {
  const [question, setQuestion] = useState(0)
  const {
    editing,
    commit: commitQuestion,
    begin: beginQuestion,
  } = useQuestionHistory(() => setQuestion((value) => value + 1))
  const params = useSearchParams()
  const { host } = useMetrics()
  const { can } = useAuth()
  const admin = can("system.admin")
  const sources = usePoll(
    (signal) => get<LogSourceIndex>("/logs/sources", undefined, signal),
    60000,
  )
  const listed = useMemo(() => railSources(sources.data, admin), [sources.data, admin])

  // A link to a request record names the record and its question, in the
  // request log's own words — whose `since` is a request window, not a log's.
  const recordLink = params.get("requests") ?? ""
  // Keep exact instants from shared links; converting them to local input values
  // before searching would lose the offset during a repeated daylight-saving hour.
  const [initialWindow] = useState(() =>
    recordLink ? { since: "", until: "", error: undefined } : readLogWindow(params),
  )
  const [windowError, setWindowError] = useState(initialWindow.error)
  // A link into the page is a complete question and sets the whole window;
  // arriving bare — the rail's own link — reopens the window this tab had,
  // which the effect below then writes back into the address bar.
  const linked = !recordLink && SOURCE_PARAMS.some((key) => params.has(key))
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
        : VIEW_ID.test(params.get("mode") ?? "")
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
  // The request record on screen in place of a source, when one is.
  const [record, setRecord] = useSessionState(
    "logs.requests",
    "",
    recordLink || (linked ? "" : undefined),
  )
  const [recordView, setRecordView] = useSessionState<RequestsView>(
    "logs.requests.view",
    "requests",
    recordLink ? (params.get("mode") === "insights" ? "insights" : "requests") : undefined,
  )
  // Kept under the record's owner's key, so the question on a deployment's
  // requests is the same here and on its own Logs page.
  const [storedQuery, setRecordQuery] = useSessionState<RequestQuery>(
    recordQueryKey(record),
    EMPTY_REQUEST_QUERY,
    recordLink ? queryFromParams(params) : undefined,
  )
  const recordQuery = useMemo(() => completeQuery(storedQuery), [storedQuery])
  const [context, setContext] = useSessionState("logs.context", 0)
  const [archives, setArchives] = useSessionState("logs.archives", false)
  const [boot, setBoot] = useSessionState("logs.boot", false)
  const [showRail, setShowRail] = useViewState("logs.rail", true)
  const [railWidth, setRailWidth, resetRailWidth] = usePanelSize("logs.rail", RAIL.base)
  const railPx = Math.max(RAIL.min, Math.min(RAIL.max, railWidth))
  // A jump to a moment — a view's "open the log around this" — is a History
  // run of its own, which a pane already on History would not make.
  const [jump, setJump] = useState(0)
  const router = useRouter()
  const requests = useRequestRecords()

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
      setFilter((f) => withLensDefaults({ ...f, fields: {} }, lensFor(next), lensFor(current)))
    }
  }

  // The source as its own page hands it to a view: the unit rather than the
  // whole journal, and the product and state the rail draws it with.
  const readLens = lens === "none" ? undefined : lens || detectedLens
  const viewSource = useMemo<ServiceLogSource | undefined>(() => {
    if (!selected) return undefined
    const oneUnit = selected.kind === "journal" && unit !== ""
    return {
      ...selected,
      id: sourceId,
      label: oneUnit ? unit : selected.label,
      status: oneUnit ? units?.find((u) => u.name === unit)?.active : selected.status,
      // What the whole journal holds is not what one unit's does.
      detail: oneUnit ? undefined : selected.detail,
      lens: detectedLens,
      product: sourceProduct(selected, host?.platform),
    }
  }, [selected, sourceId, unit, units, detectedLens, host?.platform])
  const finds = useServiceFinds(record ? undefined : viewSource, readLens, requests.sites)

  // The lens's readings are the counts on the lens row's chips, as a
  // deployment's output carries them: tiles over the frame took a laptop's
  // live tail down to nine lines and said again what the chips count.
  const readings = useLensReadings(
    sourceId,
    lensFor(
      lens === "none"
        ? undefined
        : (readLens ?? (selected?.kind === "stack" ? STACK_LENS : undefined)),
    ),
    { forcedLens: lens, enabled: !record && !windowError },
  )

  // Another log opened on a stretch of time, from a view or a request —
  // History on it, narrowed where the asker knows how. A source the rail
  // does not list is said to be gone, as a link to one is, rather than
  // swapped for another.
  const openLog = (id: string | undefined, at: OpenAt) => {
    commitQuestion()
    setRecord("")
    if (id !== undefined && id !== sourceId) {
      const target = railSourceFor(listed, id)
      if (target) switchSource(target.source, target.unit)
      setPicked(target?.source.id ?? id)
      setUnit(target?.unit ?? "")
    }
    setRange("custom")
    setSince(at.since)
    setUntil(at.until)
    const ask = askOf(at)
    if (ask) setFilter((f) => withAsk(f, ask))
    setMode("search")
    setJump((n) => n + 1)
  }
  // Not while the link's window is refused: its bounds are not dates, and the
  // page draws the refusal instead of any view.
  const ctx: ServiceLogsContext | undefined =
    viewSource && !windowError
      ? {
          source: viewSource,
          sourceId,
          lens: lensFor(readLens),
          filter,
          window: resolveRange(range, since, until),
          setFilter,
          // A view that names another source is heard: a database's statement
          // opens its server's own log.
          openHistory: (at: OpenAt) => openLog(at.source, at),
          openLive: () => setMode("live"),
        }
      : undefined
  const views = viewSource
    ? serviceViews(viewSource, finds, {
        openLog,
        onQuery: (conn: DbFleetEntry, sql: string) =>
          router.push(sectionHref(conn.id, "query", { sql })),
      })
    : []
  // A view the source does not have — Events kept from a container, now on
  // syslog — reads as Live until a source that has it is chosen again.
  const shownMode = MODES.includes(mode) || views.some((v) => v.id === mode) ? mode : "live"
  const shownView = views.find((v) => v.id === shownMode)
  const found = requests.records.find((r) => r.id === record)

  const predicates = filterQuery(filter).f
  const predicatesKey = JSON.stringify(predicates ?? [])
  const recordWords = JSON.stringify(record ? queryParams(recordQuery) : [])
  useEffect(() => {
    if (windowError || editing.current) return
    const url = new URL(window.location.href)
    if (record) {
      // A record's address is the record and its question, in the request
      // log's own words, so a link opens on the same rows.
      for (const key of [...SOURCE_PARAMS, ...QUERY_PARAMS]) url.searchParams.delete(key)
      url.searchParams.set("requests", record)
      if (recordView === "insights") url.searchParams.set("mode", "insights")
      for (const [key, value] of JSON.parse(recordWords) as [string, string][]) {
        url.searchParams.set(key, value)
      }
      window.history.replaceState(null, "", url)
      return
    }
    if (!sourceId) return
    url.searchParams.delete("requests")
    for (const key of QUERY_PARAMS) {
      if (!SOURCE_PARAMS.includes(key)) url.searchParams.delete(key)
    }
    url.searchParams.set("source", sourceId)
    if (shownMode !== "live") url.searchParams.set("mode", shownMode)
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
    question,
    editing,
    record,
    recordView,
    recordWords,
    sourceId,
    shownMode,
    filter.q,
    filter.levels,
    predicatesKey,
    lens,
    range,
    since,
    until,
    windowError,
  ])

  const onFilterChange = (next: LogFilterState) => {
    beginQuestion()
    setFilter(next)
  }
  const identity = record
    ? found && <RecordIdentity key={record} record={found} aside={<WorkspaceHelp compact />} />
    : viewSource &&
      selected &&
      !windowError && (
        <SourceIdentity
          key={sourceId}
          source={viewSource}
          aside={
            <div className="flex items-center gap-2">
              {/* A page view with its own export — a site's requests — is not
                the log's lines, and two Export buttons would be two answers. */}
              {(!shownView || shownView.filtered) && (
                <ExportDialog
                  sourceId={sourceId}
                  source={selected}
                  filter={filter}
                  boot={boot}
                  lens={lens}
                />
              )}
              <WorkspaceHelp compact />
            </div>
          }
        />
      )

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
  // Equivalent URLs can reorder fields when a restored question is written back.
  const questionParams = new URLSearchParams(params.toString())
  questionParams.sort()

  return (
    // Below `lg` the rail stacks over the lines, and the two shared the
    // window: a request record's chart and filters left its rows no height
    // at all. There the page scrolls, and the column under the rail keeps a
    // window's height of its own.
    <Workspace
      name="Logs"
      rows={false}
      memory
      stateKey={`logs.${sourceId}.${shownMode}.${questionParams.toString()}`}
      refresh={() => {
        sources.refresh()
        setJump((value) => value + 1)
      }}
    >
      <Page fill className="gap-4 max-lg:h-auto max-lg:overflow-visible md:gap-5">
        <PageContext
          eyebrow="Server"
          title="Logs"
          actions={identity ? undefined : <WorkspaceHelp />}
        />
        {identity}

        {/* One frame around the whole workbench. The rail and the lines are
          separated by a hairline rather than by a gutter and two borders: two
          framed panes with a gap between them read as two boxes floating on
          the page, and the screen is one working surface. */}
        <div
          style={{ "--jd-rail": `${railPx}px` } as React.CSSProperties}
          className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-xl border bg-card max-lg:flex-none lg:flex-row"
        >
          {showRail && (
            <div className="relative flex max-h-64 shrink-0 border-b border-hairline lg:max-h-none lg:w-(--jd-rail) lg:border-r lg:border-b-0">
              <SourceRail
                index={sources.data}
                sources={listed}
                loading={sources.loading}
                error={sources.error}
                selectedId={record ? null : (selected?.id ?? null)}
                onSelect={(source) => {
                  if (record || source.id !== selected?.id) commitQuestion()
                  setRecord("")
                  switchSource(source, source.kind === "journal" ? unit : "")
                  setPicked(source.id)
                  if (source.kind !== "journal") setUnit("")
                }}
                records={requests.records}
                selectedRecord={record || null}
                onSelectRecord={(next) => {
                  if (next.id !== record) commitQuestion()
                  setRecord(next.id)
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
          ) : record ? (
            found ? (
              <RecordColumn
                className={COLUMN}
                key={record}
                record={found}
                leading={railToggle}
                view={recordView}
                onViewChange={setRecordView}
                query={recordQuery}
                onQueryChange={setRecordQuery}
                openLog={openLog}
              />
            ) : (
              <Blank leading={railToggle}>
                <EmptyState
                  icon={Logs}
                  title={
                    requests.settled ? "Request record unavailable" : "Looking for request records…"
                  }
                  description={
                    requests.settled
                      ? `The requested record (${record}) is not among this host's deployments and sites with one. The deployment may have been removed, or the site may no longer write an access log of its own.`
                      : undefined
                  }
                />
              </Blank>
            )
          ) : selected ? (
            <LogWorkspace
              key={sourceId}
              refreshToken={jump}
              className={COLUMN}
              flush
              leading={railToggle}
              // The identity line over the frame names it — a unit's journal
              // as the unit — so the strip is the views alone.
              name={null}
              source={selected}
              sourceId={sourceId}
              units={sources.data?.units ?? []}
              views={ctx && views.map((view) => ({ ...view, render: () => view.render(ctx) }))}
              mode={shownMode}
              onModeChange={(next) => {
                if (next !== mode) commitQuestion()
                setMode(next)
              }}
              filter={filter}
              onFilterChange={onFilterChange}
              onSubmitQuestion={() => {
                if (editing.current) commitQuestion()
              }}
              unit={unit}
              onUnitChange={(next) => {
                if (next !== unit) commitQuestion()
                switchSource(selected, next)
                setUnit(next)
              }}
              lens={lens}
              onLensChange={(next) => {
                setLens(next)
                setFilter((f) => ({ ...f, fields: {} }))
              }}
              detectedLens={detectedLens}
              readings={readings}
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
    </Workspace>
  )
}

/** The lines column with nothing to show: the rail toggle stays reachable. */
function Blank({ leading, children }: { leading: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className={cn("flex min-h-0 min-w-0 flex-1 flex-col", COLUMN)}>
      <div className="flex min-h-10 shrink-0 items-center border-b border-hairline px-2">
        {leading}
      </div>
      <div className="flex min-h-0 flex-1 items-center justify-center overflow-auto p-6">
        {children}
      </div>
    </div>
  )
}
