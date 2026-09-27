"use client"

import { useEffect, useMemo, useState } from "react"
import { Logs } from "@/components/icons"
import { cn } from "@/lib/utils"
import { get } from "@/lib/api"
import { minuteSpan } from "@/lib/format"
import type { LogLine, LogSource } from "@/lib/types"
import { EMPTY_FILTER, fieldsOf, resolveRange, type LogLevel } from "@/lib/log-filter"
import { STACK_LENS, lensFor, withLensDefaults, type LogLens } from "@/lib/log-lenses"
import { useSessionState } from "@/lib/view-state"
import type { LogFields, LogFilterState, LogMode, LogTimeRange } from "@/components/logs/types"
import type { Tone } from "@/components/tone"
import type { Verb } from "@/components/verbs"
import { usePoll } from "@/hooks/use-poll"
import { ExportDialog } from "@/components/logs/export-dialog"
import { LensReadings, useLensReadings } from "@/components/logs/lens-readings"
import { LogWorkspace } from "@/components/logs/log-workspace"
import { SourceFacts } from "@/components/logs/source-facts"
import { Pane } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { EmptyState, LoadingRows } from "@/components/state"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/** A source a page offers: what `/logs/sources` would list, and the product it is. */
export type ServiceLogSource = Omit<LogSource, "rotated"> & { rotated?: boolean; product?: string }

/** A stretch of time a page opens History on: a run's activation, a request's moment. */
export type LogWindow = { since: string; until: string; label?: string }

/** A page's own reading of the source — a database's queries, a unit's runs — beside the three. */
export type ServiceLogsView = {
  id: string
  label: string
  count?: number
  countTone?: Tone
  /** Keep the filter and the lens row above it; it reads the same filter. */
  filtered?: boolean
  /** Drawn in a body that owns its scroll. */
  render: (ctx: ServiceLogsContext) => React.ReactNode
}

/** What a page view is handed: the source as read, the question on screen, and the ways back to the lines. */
export type ServiceLogsContext = {
  source: ServiceLogSource
  sourceId: string
  lens: LogLens | undefined
  filter: LogFilterState
  window: { since?: string; until?: string }
  setFilter(filter: LogFilterState): void
  /** History on a stretch of time — "the server log around this query" — narrowed if asked. */
  openHistory(at: LogWindow & { fields?: LogFields; levels?: LogLevel[]; q?: string }): void
  openLive(): void
}

export type ServiceLogsProps = {
  /** At least one. Compared by id, so a page polling them does not reset anything. */
  sources: ServiceLogSource[]
  /**
   * What the page's address bar says to open, as `useSessionState`'s arrival:
   * set, it opens and is remembered; null or absent, the remembered one
   * stands. An id that is not among `sources` is said to be gone, never
   * swapped for another.
   */
  source?: string | null
  onSourceChange?(id: string): void
  /** The same, for the reading on screen: "live", "search", "insights" or a view's id. */
  view?: string | null
  onViewChange?(id: string): void
  /** Where the state is kept for the tab; absent, it lives as long as the component. Never "logs.*". */
  storageKey?: string
  /** Which of Live, History and Insights are offered; all three unless the page says otherwise. */
  modes?: ("live" | "search" | "insights")[]
  views?: ServiceLogsView[]
  /** History on this window, each time it changes; gone again, back to Live. */
  window?: LogWindow
  /** Live pressed while a window is set: the page lets go of it. */
  onLeaveWindow?(): void
  /** The lens's readings above the pane, for a page with no figures of its own. */
  readings?: boolean
  /** A sheet: no readings, no value columns, no facts beside the name. */
  layout?: "page" | "sheet"
  facts?: React.ReactNode
  actions?: React.ReactNode
  /** The accessible name of the picker a page with several sources gets. */
  pickerLabel?: string
  lineVerbs?(line: LogLine): Verb[]
  /** Inside a pane the page draws: no frame of its own. */
  flush?: boolean
  /** The column the readings and the pane stand in. */
  className?: string
  /** The pane's own size — a page sets its floor here. */
  paneClassName?: string
}

const DEFAULT_RANGE: LogTimeRange = "24h"

/** The kept values a page's arrival overrides until the kept state holds them. */
type Handed = {
  picked?: string
  mode?: LogMode
  range?: LogTimeRange
  since?: string
  until?: string
}
const ALL_MODES: ("live" | "search" | "insights")[] = ["live", "search", "insights"]

/**
 * One piece of the state: kept for the tab under the page's key, or for the
 * component's life when the page gave none — a run's logs, a sheet. Both
 * hooks run on every render, as hooks must; the unused slot is inert, since
 * a session key that is never written only ever reads its fallback.
 */
function useKept<T>(storageKey: string | undefined, name: string, fallback: T) {
  const local = useState<T>(fallback)
  const stored = useSessionState<T>(storageKey ? `${storageKey}.${name}` : "", fallback)
  return storageKey ? stored : local
}

/** The page's facts over what the server describes: the page named it, the server measured it. */
function describedOver(given: ServiceLogSource, described: unknown): ServiceLogSource {
  if (!described || typeof described !== "object" || Array.isArray(described)) return given
  const out: Record<string, unknown> = { ...given }
  for (const [key, value] of Object.entries(described)) {
    if (out[key] === undefined && value !== undefined && value !== null) out[key] = value
  }
  return out as ServiceLogSource
}

function productOf(source: ServiceLogSource) {
  return source.product ?? lensFor(source.lens)?.product
}

/**
 * A service's logs, on the service's own page.
 *
 * The logs page's reading of one source — Live, History, Insights, the
 * lens's quick views and fields, a line opened in place — as the one thing
 * every page that shows a service's log embeds, so a database's log, a
 * container's output and a site's access log are read the same way where
 * they are rather than on a page the reader is sent to.
 *
 * The page hands over what it knows: its sources (a picker appears when
 * there are several), the lens where it knows better than detection, its own
 * views of the same lines, a window to open History on. The rest is
 * described by the server (`GET /logs/source`: the file's size, its rotated
 * set, the lens it detects) and merged under what the page said — a page
 * that answers `{}`, or a server without the route, loses the facts and
 * nothing else. Nothing is searched on arrival but History's own run.
 *
 * A lens's defaults (the auth log's scans hidden) are applied the first time
 * a source is read through it, as chips the reader can press away; a source
 * read in another vocabulary starts without the last one's fields, since
 * `event:slow` on an auth log is an empty pane that looks like an answer.
 */
export function ServiceLogs(props: ServiceLogsProps) {
  const { sources, storageKey } = props
  const sheet = props.layout === "sheet"
  const modes = props.modes ?? ALL_MODES

  const [storedPicked, setPicked] = useKept(storageKey, "source", "")
  const [storedMode, setMode] = useKept<LogMode>(storageKey, "mode", modes[0])
  const [filter, setFilter] = useKept<LogFilterState>(storageKey, "filter", EMPTY_FILTER)
  // The lens the filter's fields were written in, or null before the first.
  const [filterLens, setFilterLens] = useKept<string | null>(storageKey, "filterLens", null)
  const [readAs, setReadAs] = useKept(storageKey, "lens", "")
  const [storedRange, setRange] = useKept<LogTimeRange>(storageKey, "range", DEFAULT_RANGE)
  const [storedSince, setSince] = useKept(storageKey, "since", "")
  const [storedUntil, setUntil] = useKept(storageKey, "until", "")
  const [context, setContext] = useKept(storageKey, "context", 0)
  const [archives, setArchives] = useKept(storageKey, "archives", false)
  const [boot, setBoot] = useKept(storageKey, "boot", false)
  // A jump to a moment is a History run of its own, which a pane already on
  // History would not make: the pane starts again on it.
  const [jump, setJump] = useState(0)

  // What the page hands over — its source, its view, a window — is applied
  // each time it changes, the way `useSessionState`'s arrival is: noticed
  // during render, so the first frame is already the right one, and written
  // to the kept state after it, since that may be the session store and a
  // store written mid-render updates its other readers mid-render. Until the
  // store holds a handed value, the handed value is what is read.
  const sourceArrival = props.source ?? null
  const viewArrival = props.view ?? null
  const windowKey = props.window ? `${props.window.since}|${props.window.until}` : ""
  const [prev, setPrev] = useState<{ source: string | null; view: string | null; window: string }>({
    source: null,
    view: null,
    window: "",
  })
  const [handed, setHanded] = useState<Handed>({})
  const stored: Required<Handed> = {
    picked: storedPicked,
    mode: storedMode,
    range: storedRange,
    since: storedSince,
    until: storedUntil,
  }
  if (prev.source !== sourceArrival || prev.view !== viewArrival || prev.window !== windowKey) {
    const next: Handed = { ...handed }
    if (sourceArrival !== null && sourceArrival !== prev.source) next.picked = sourceArrival
    if (viewArrival !== null && viewArrival !== prev.view) next.mode = viewArrival
    if (windowKey !== prev.window) {
      if (props.window) {
        next.mode = "search"
        next.range = "custom"
        next.since = props.window.since
        next.until = props.window.until
      } else {
        // Out of the window: History on it goes back to Live, and the
        // window's bounds go, so the next History is not the old moment.
        if ((next.mode ?? storedMode) === "search") next.mode = "live"
        next.range = DEFAULT_RANGE
        next.since = ""
        next.until = ""
      }
    }
    setPrev({ source: sourceArrival, view: viewArrival, window: windowKey })
    setHanded(next)
  } else {
    // What the kept state has caught up with is read from it again.
    const caught = (Object.keys(handed) as (keyof Handed)[]).filter(
      (key) => handed[key] === stored[key],
    )
    if (caught.length > 0) {
      const rest = { ...handed }
      for (const key of caught) delete rest[key]
      setHanded(rest)
    }
  }
  useEffect(() => {
    if (handed.picked !== undefined) setPicked(handed.picked)
    if (handed.mode !== undefined) setMode(handed.mode)
    if (handed.range !== undefined) setRange(handed.range)
    if (handed.since !== undefined) setSince(handed.since)
    if (handed.until !== undefined) setUntil(handed.until)
  }, [handed, setPicked, setMode, setRange, setSince, setUntil])

  const picked = handed.picked ?? storedPicked
  const offered = [...modes, ...(props.views ?? []).map((v) => v.id)]
  const wantedMode = handed.mode ?? storedMode
  const mode = offered.includes(wantedMode) ? wantedMode : offered[0]
  const range = handed.range ?? storedRange
  const since = handed.since ?? storedSince
  const until = handed.until ?? storedUntil

  // A remembered id that went is quietly the first source again; one the
  // page asked for by name is said to be gone, because opening another in
  // its place would answer a question nobody asked.
  const wanted = picked
  const found = sources.find((s) => s.id === wanted)
  const missing = !found && wanted !== "" && wanted === sourceArrival
  const given = found ?? (missing ? undefined : sources[0])
  const sourceId = given?.id ?? ""

  const described = usePoll(
    (signal) => get<unknown>("/logs/source", { source: sourceId }, signal),
    60_000,
    [sourceId],
    { enabled: Boolean(sourceId) },
  )
  const source = useMemo(
    () => (given ? describedOver(given, described.data) : undefined),
    [given, described.data],
  )

  // The lens the page named is the one asked for; the reader's "Read as"
  // overrides it. Until the server has said what it detects — when the page
  // named none — the pane waits, so the first socket is already the right
  // question, defaults and all.
  const forced = readAs || given?.lens || ""
  const settled = described.data !== undefined || described.error !== undefined
  const lensKnown = Boolean(readAs || given?.lens) || settled
  const lensId =
    readAs === "none"
      ? undefined
      : readAs || source?.lens || (source?.kind === "stack" ? STACK_LENS : undefined)
  const lens = lensFor(lensId)

  const lensKey = lensKnown ? (lensId ?? "") : null
  const relens = lensKey !== null && filterLens !== lensKey
  const shown = useMemo(
    () =>
      relens
        ? withLensDefaults(filterLens === null ? filter : { ...filter, fields: {} }, lens)
        : filter,
    [relens, filterLens, filter, lens],
  )
  useEffect(() => {
    if (!relens) return
    setFilter(shown)
    setFilterLens(lensKey)
  }, [relens, shown, lensKey, setFilter, setFilterLens])

  const readings = useLensReadings(sourceId, lens, {
    forcedLens: forced,
    enabled: Boolean(props.readings) && !sheet && lensKnown,
  })

  const changeMode = (next: LogMode) => {
    if (next === "live" && props.window && props.onLeaveWindow) {
      props.onLeaveWindow()
      return
    }
    setMode(next)
    props.onViewChange?.(next)
  }

  const openHistory: ServiceLogsContext["openHistory"] = (at) => {
    setMode("search")
    setRange("custom")
    setSince(at.since)
    setUntil(at.until)
    if (at.fields || at.levels || at.q !== undefined) {
      setFilter({
        ...shown,
        fields: at.fields ?? fieldsOf(shown),
        levels: at.levels ?? shown.levels,
        q: at.q ?? shown.q,
      })
    }
    setJump((n) => n + 1)
    props.onViewChange?.("search")
  }

  const choose = (id: string) => {
    setPicked(id)
    props.onSourceChange?.(id)
  }

  const picker =
    sources.length > 1 || missing ? (
      <Select value={given?.id ?? ""} onValueChange={choose}>
        <SelectTrigger
          size="sm"
          aria-label={props.pickerLabel ?? "Log source"}
          className="h-7 max-w-72 min-w-0 gap-1.5 border-transparent bg-transparent px-1.5 font-medium shadow-none max-sm:h-8"
        >
          <SelectValue placeholder="Choose a source" />
        </SelectTrigger>
        <SelectContent>
          {sources.map((s) => {
            const product = productOf(s)
            return (
              <SelectItem key={s.id} value={s.id}>
                {product && <ProductGlyph id={product} />}
                {s.label}
              </SelectItem>
            )
          })}
        </SelectContent>
      </Select>
    ) : undefined

  const paneClass = cn("min-h-0 flex-1", props.paneClassName)

  if (!source) {
    if (!missing) return null
    return (
      <div className={cn("flex min-h-0 min-w-0 flex-col", props.className)}>
        <Pane flush={props.flush} className={paneClass}>
          <div className="flex min-h-10 shrink-0 items-center border-b border-hairline px-2">
            {picker}
          </div>
          <div className="flex flex-1 items-center justify-center p-6">
            <EmptyState
              icon={Logs}
              title="This log source is no longer available"
              description={`${wanted} is not among this page's sources any more — it may have been removed or replaced. Choose another to read its logs.`}
            />
          </div>
        </Pane>
      </div>
    )
  }

  const window = resolveRange(range, since, until)
  const ctx: ServiceLogsContext = {
    source,
    sourceId,
    lens,
    filter: shown,
    window,
    setFilter,
    openHistory,
    openLive: () => changeMode("live"),
  }
  const product = productOf(source)
  const facts = sheet
    ? undefined
    : (props.facts ??
      (props.window?.label ? (
        <span className="numeric shrink-0 text-hint text-muted-foreground">
          {props.window.label} · {minuteSpan(props.window.since, props.window.until)}
        </span>
      ) : (
        <SourceFacts source={source} />
      )))

  return (
    <div className={cn("flex min-h-0 min-w-0 flex-col gap-4", props.className)}>
      {props.readings && !sheet && readings.tiles.length > 0 && (
        <LensReadings readings={readings} filter={shown} onFilterChange={setFilter} />
      )}
      {!lensKnown ? (
        <Pane flush={props.flush} className={paneClass}>
          <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-hairline px-2">
            {picker ?? <span className="truncate text-body font-medium">{source.label}</span>}
          </div>
          <LoadingRows rows={6} className="p-3" />
        </Pane>
      ) : (
        <LogWorkspace
          key={`${sourceId}|${windowKey}|${jump}`}
          className={paneClass}
          flush={props.flush}
          compact={sheet}
          leading={!picker && product ? <ProductGlyph id={product} /> : undefined}
          name={picker}
          facts={facts}
          actions={
            (props.actions || !sheet) && (
              <>
                {props.actions}
                {!sheet && (
                  <ExportDialog
                    sourceId={sourceId}
                    source={{ ...source, rotated: source.rotated ?? false }}
                    filter={shown}
                    boot={boot}
                    lens={forced}
                  />
                )}
              </>
            )
          }
          source={{ ...source, rotated: source.rotated ?? false }}
          sourceId={sourceId}
          units={[]}
          unit=""
          onUnitChange={() => {}}
          modes={modes}
          views={props.views?.map((view) => ({ ...view, render: () => view.render(ctx) }))}
          mode={mode}
          onModeChange={changeMode}
          filter={shown}
          onFilterChange={setFilter}
          lens={forced}
          onLensChange={setReadAs}
          detectedLens={source.lens}
          lineVerbs={props.lineVerbs}
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
      )}
    </div>
  )
}
