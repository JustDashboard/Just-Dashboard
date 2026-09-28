"use client"

import { useEffect, useMemo, useState } from "react"
import { LockClosed, Logs } from "@/components/icons"
import { cn } from "@/lib/utils"
import { ApiError, get } from "@/lib/api"
import { minuteSpan } from "@/lib/format"
import type { LogLine, LogSource } from "@/lib/types"
import { EMPTY_FILTER, filterEquals, resolveRange } from "@/lib/log-filter"
import { STACK_LENS, lensFor, withLensDefaults, type LogLens } from "@/lib/log-lenses"
import { useSessionState } from "@/lib/view-state"
import type { LogFilterState, LogMode, LogTimeRange } from "@/components/logs/types"
import type { Tone } from "@/components/tone"
import type { Verb } from "@/components/verbs"
import { usePoll } from "@/hooks/use-poll"
import { useMediaQuery } from "@/hooks/use-mobile"
import { ExportDialog } from "@/components/logs/export-dialog"
import {
  LensReadings,
  useLensReadings,
  type LensReadingsState,
} from "@/components/logs/lens-readings"
import { LogWorkspace } from "@/components/logs/log-workspace"
import { askOf, emptiedFile, withAsk, type LogAsk } from "@/components/logs/logs-model"
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

export type { LogAsk }

/**
 * A source a page offers: what `/logs/sources` would list, and the product it is.
 *
 * `described` says the page asked `GET /logs/source` itself and this is the
 * answer — the pane does not ask a second time, and its `lens` is the one
 * the server detects rather than one the page reads it through. A page that
 * reads a source through a lens of its own leaves it off, so Auto under
 * "Read as" still knows what the server would have detected.
 */
export type ServiceLogSource = Omit<LogSource, "rotated"> & {
  rotated?: boolean
  product?: string
  described?: boolean
}

/**
 * A stretch of time a page opens History on — a run's activation, a
 * request's moment — narrowed there when the page knows to: the minute
 * around a failed request, in a log other sites share, to this site's names.
 */
export type LogWindow = LogAsk & { since: string; until: string; label?: string }

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
  /**
   * History on a stretch of time — "the server log around this query" — on
   * another of the page's sources when it names one, narrowed if asked.
   */
  openHistory(at: LogWindow & { source?: string }): void
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
  /**
   * The reader moved off the window — Live, another range, a zoom, History
   * on another moment: the page lets go of it. The move stands, so letting
   * go must withdraw `window`.
   */
  onLeaveWindow?(): void
  /**
   * The lens's readings: as tiles above the pane (`true`) for a page with no
   * figures of its own, from `sm` up — on a phone they were the whole first
   * screen; or as the figures on the lens row's chips (`"chips"`) where the
   * section draws no tiles.
   */
  readings?: boolean | "chips"
  /**
   * The lens's readings where the page draws them in a grid of its own: a
   * quick view one of them answers then carries no second count of its own.
   */
  answeredBy?: LensReadingsState
  /** The window History opens on until the reader picks another: a log written weekly reads empty over a day. */
  initialRange?: LogTimeRange
  /**
   * A narrowing from outside the pane — a reading pressed on the page's own
   * grid — applied each time its key changes, in the first reading that
   * shows the filter.
   */
  ask?: LogAsk & { key: string }
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
  /** The pane's own size — its height, or the floor it fills the column from. */
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
  /** The window the kept range was handed for, or "" once it is the reader's own. */
  windowed?: string
  archives?: boolean
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

/** The lens the server says it reads a source through, from its description. */
function detectedOf(described: unknown): string | undefined {
  if (!described || typeof described !== "object") return undefined
  const lens = (described as { lens?: unknown }).lens
  return typeof lens === "string" && lens ? lens : undefined
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
 * nothing else; one the server refuses is said to be refused, and nothing
 * is read. Nothing is searched on arrival but History's own run.
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
  const opening = props.initialRange ?? DEFAULT_RANGE
  const wide = useMediaQuery("(min-width: 640px)")

  const [storedPicked, setPicked] = useKept(storageKey, "source", "")
  const [storedMode, setMode] = useKept<LogMode>(storageKey, "mode", modes[0])
  const [filter, setFilter] = useKept<LogFilterState>(storageKey, "filter", EMPTY_FILTER)
  // The lens the filter's fields were written in, or null before the first.
  const [filterLens, setFilterLens] = useKept<string | null>(storageKey, "filterLens", null)
  // "" reads the source as the page named it; "auto" as the server detects
  // it, over a lens the page named; "none" as plain text; else that lens —
  // on the source it was chosen on. A lens forced on one source was a
  // statement about that one: carried to the next, nginx-error read an access
  // log and None took a container's journal out of its lens.
  const [readAsOn, setReadAsOn] = useKept(storageKey, "readAs", { source: "", lens: "" })
  const [storedRange, setRange] = useKept<LogTimeRange>(storageKey, "range", opening)
  const [storedSince, setSince] = useKept(storageKey, "since", "")
  const [storedUntil, setUntil] = useKept(storageKey, "until", "")
  const [storedWindowed, setWindowed] = useKept(storageKey, "windowed", "")
  const [context, setContext] = useKept(storageKey, "context", 0)
  const [storedArchives, setArchives] = useKept(storageKey, "archives", false)
  const [boot, setBoot] = useKept(storageKey, "boot", false)
  // A jump to a moment is a History run of its own, which a pane already on
  // History would not make: the pane starts again on it.
  const [jump, setJump] = useState(0)
  // A narrowing handed in — with a window, from `ask`, from `openHistory` —
  // waiting for the lens it is read in to be known, so a source switched to
  // with it does not drop it as the last vocabulary's fields.
  const [asking, setAsking] = useState<LogAsk | null>(null)

  // What the page hands over — its source, its view, a window, an ask — is
  // applied each time it changes, the way `useSessionState`'s arrival is:
  // noticed during render, so the first frame is already the right one, and
  // written to the kept state after it, since that may be the session store
  // and a store written mid-render updates its other readers mid-render.
  // Until the store holds a handed value, the handed value is what is read.
  const sourceArrival = props.source ?? null
  const viewArrival = props.view ?? null
  const windowKey = props.window ? `${props.window.since}|${props.window.until}` : ""
  const askKey = props.ask?.key ?? ""
  const [prev, setPrev] = useState<{
    source: string | null
    view: string | null
    window: string
    ask: string
  }>({ source: null, view: null, window: "", ask: "" })
  const [handed, setHanded] = useState<Handed>({})
  const stored: Required<Handed> = {
    picked: storedPicked,
    mode: storedMode,
    range: storedRange,
    since: storedSince,
    until: storedUntil,
    windowed: storedWindowed,
    archives: storedArchives,
  }
  const windowed = handed.windowed ?? storedWindowed
  const offered = [...modes, ...(props.views ?? []).map((v) => v.id)]
  // Whether a reading shows the filter: a page view that reads none would
  // take a narrowing out of sight.
  const shows = (mode: LogMode) =>
    (modes as LogMode[]).includes(mode) ||
    Boolean(props.views?.find((view) => view.id === mode)?.filtered)
  // Out of the window: History on it goes back to Live, and the window's
  // bounds go, so the next History is not the old moment.
  const outOfWindow = (next: Handed) => {
    if ((next.mode ?? storedMode) === "search") next.mode = "live"
    next.range = opening
    next.since = ""
    next.until = ""
    next.windowed = ""
  }
  if (
    prev.source !== sourceArrival ||
    prev.view !== viewArrival ||
    prev.window !== windowKey ||
    prev.ask !== askKey
  ) {
    const next: Handed = { ...handed }
    if (sourceArrival !== null && sourceArrival !== prev.source) next.picked = sourceArrival
    if (viewArrival !== null && viewArrival !== prev.view) next.mode = viewArrival
    if (windowKey !== prev.window) {
      if (props.window) {
        next.mode = "search"
        next.range = "custom"
        next.since = props.window.since
        next.until = props.window.until
        next.windowed = windowKey
        // A new window is a History run of its own, whatever the pane was on.
        setJump((n) => n + 1)
        const ask = askOf(props.window)
        if (ask) setAsking(ask)
      } else {
        outOfWindow(next)
      }
    }
    if (askKey !== prev.ask && props.ask) {
      const ask = askOf(props.ask)
      if (ask) {
        setAsking(ask)
        setJump((n) => n + 1)
        const mode = next.mode ?? storedMode
        if (!shows(offered.includes(mode) ? mode : offered[0])) next.mode = modes[0]
      }
    }
    setPrev({ source: sourceArrival, view: viewArrival, window: windowKey, ask: askKey })
    setHanded(next)
  } else if (!props.window && windowed !== "") {
    // Mounted without the window the kept range was handed for — the page
    // let go of it while this pane was not on screen — so the pane does not
    // come back on an old moment with nothing to say what it was. A reading
    // the address asks for by name is still the one opened.
    const next: Handed = { ...handed, range: opening, since: "", until: "", windowed: "" }
    if (next.mode === undefined && storedMode === "search") next.mode = "live"
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
    if (handed.windowed !== undefined) setWindowed(handed.windowed)
    if (handed.archives !== undefined) setArchives(handed.archives)
  }, [handed, setPicked, setMode, setRange, setSince, setUntil, setWindowed, setArchives])

  const picked = handed.picked ?? storedPicked
  const wantedMode = handed.mode ?? storedMode
  const mode = offered.includes(wantedMode) ? wantedMode : offered[0]
  const range = handed.range ?? storedRange
  const since = handed.since ?? storedSince
  const until = handed.until ?? storedUntil
  const archives = handed.archives ?? storedArchives

  // A remembered id that went is quietly the first source again; one the
  // page asked for by name is said to be gone, because opening another in
  // its place would answer a question nobody asked.
  const found = sources.find((s) => s.id === picked)
  const missing = !found && picked !== "" && picked === sourceArrival
  const given = found ?? (missing ? undefined : sources[0])
  const sourceId = given?.id ?? ""

  const described = usePoll(
    (signal) => get<unknown>("/logs/source", { source: sourceId }, signal),
    60_000,
    [sourceId],
    { enabled: Boolean(sourceId) && !given?.described },
  )
  const source = useMemo(
    () => (given ? describedOver(given, described.data) : undefined),
    [given, described.data],
  )
  // A read the server refuses — login records, for anyone but an
  // administrator — is said to be refused, in the server's words, and
  // nothing is opened: a socket retrying a refusal says the tunnel dropped.
  const refused =
    described.error instanceof ApiError && described.error.status === 403
      ? described.error
      : undefined

  // Nothing is read before the server has described the source: whether
  // this reader may read it at all, and the lens it detects where nothing
  // named one, so the first socket is already the right question, defaults
  // and all. The lens the page named is the one asked for; the reader's
  // "Read as" overrides it, Auto included.
  const ready =
    Boolean(given?.described) || described.data !== undefined || described.error !== undefined
  const pageLens = given?.described ? "" : (given?.lens ?? "")
  const readAs = readAsOn.source === sourceId ? readAsOn.lens : ""
  const forced = readAs === "auto" ? "" : readAs || pageLens
  const detected = given?.described ? given.lens : detectedOf(described.data)
  const lensId =
    readAs === "none"
      ? undefined
      : forced || detected || (source?.kind === "stack" ? STACK_LENS : undefined)
  const lens = lensFor(lensId)

  const lensKey = ready ? (lensId ?? "") : null
  const relens = lensKey !== null && filterLens !== lensKey
  const applying = asking !== null && ready
  const shown = useMemo(() => {
    const base = relens
      ? filterLens === null
        ? withLensDefaults(filter, lens)
        : withLensDefaults({ ...filter, fields: {} }, lens, lensFor(filterLens || undefined))
      : filter
    return applying ? withAsk(base, asking) : base
  }, [relens, filterLens, filter, lens, applying, asking])
  // The narrowing is let go once the kept filter holds it.
  if (applying && !relens && filterEquals(filter, shown)) setAsking(null)
  useEffect(() => {
    if (!relens && !applying) return
    setFilter(shown)
    if (relens) setFilterLens(lensKey)
  }, [relens, applying, shown, lensKey, setFilter, setFilterLens])

  // A file logrotate has just emptied is read with its rotated set: the
  // server's last lines are in yesterday's file, and a History of the empty
  // one answered "no matches" about a busy night. Once per source a visit,
  // so switching the archives off again sticks.
  const [archivesFor, setArchivesFor] = useState<string | null>(null)
  if (source && archivesFor !== sourceId && emptiedFile(source)) {
    setArchivesFor(sourceId)
    setHanded((h) => ({ ...h, archives: true }))
  }

  const asTiles = props.readings === true && wide && !sheet
  const asChips = props.readings === "chips" && !sheet
  const readings = useLensReadings(sourceId, lens, {
    forcedLens: forced,
    enabled: (asTiles || asChips) && ready && !refused,
  })

  // The reader moved off the window the page handed. The page lets go of
  // it, and the move stands: the arrival its letting go would otherwise be
  // — back to Live, the window's bounds cleared — has already been seen.
  const leaveWindow = () => {
    if (!props.window || !props.onLeaveWindow) return false
    props.onLeaveWindow()
    setPrev((p) => ({ ...p, window: "" }))
    setHanded((h) => ({ ...h, windowed: "" }))
    return true
  }

  const changeMode = (next: LogMode) => {
    if (next === "live" && leaveWindow()) {
      setRange(opening)
      setSince("")
      setUntil("")
    }
    setMode(next)
    props.onViewChange?.(next)
  }

  const openHistory: ServiceLogsContext["openHistory"] = (at) => {
    leaveWindow()
    if (at.source && at.source !== sourceId) {
      setPicked(at.source)
      props.onSourceChange?.(at.source)
    }
    setMode("search")
    setRange("custom")
    setSince(at.since)
    setUntil(at.until)
    const ask = askOf(at)
    if (ask) setAsking(ask)
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

  // The pane's own height, where the page gives one, is its height: a flex
  // basis of zero made it whatever the column was, or the whole log tall.
  const paneClass = cn("min-h-0 flex-auto", props.paneClassName)

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
              description={`${picked} is not among this page's sources any more — it may have been removed or replaced. Choose another to read its logs.`}
            />
          </div>
        </Pane>
      </div>
    )
  }

  const product = productOf(source)
  const name = picker ?? <span className="truncate text-body font-medium">{source.label}</span>

  if (refused) {
    return (
      <div className={cn("flex min-h-0 min-w-0 flex-col", props.className)}>
        <Pane flush={props.flush} className={paneClass}>
          <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-hairline px-2">
            {!picker && product && <ProductGlyph id={product} />}
            {name}
          </div>
          <div className="flex flex-1 items-center justify-center p-6">
            <EmptyState
              icon={LockClosed}
              title="You cannot read this log"
              description={refused.message}
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
  // The window's name only while the pane is on it: a range picked since,
  // or bounds typed over it, are not the moment the page named.
  const onWindow =
    props.window !== undefined &&
    range === "custom" &&
    since === props.window.since &&
    until === props.window.until
  const facts = sheet
    ? undefined
    : (props.facts ??
      (onWindow && props.window?.label ? (
        <span className="numeric min-w-0 truncate text-hint text-muted-foreground max-sm:hidden">
          {props.window.label} · {minuteSpan(props.window.since, props.window.until)}
        </span>
      ) : (
        <SourceFacts source={source} />
      )))
  // A page view that reads no filter has its own commands — a request log
  // exports its own rows — and the log's export beside them was a second
  // button saying the same word about other lines.
  const view = props.views?.find((v) => v.id === mode)
  const exportable = !sheet && (!view || view.filtered)

  return (
    <div className={cn("flex min-h-0 min-w-0 flex-col gap-4", props.className)}>
      {asTiles && readings.tiles.length > 0 && (
        <LensReadings readings={readings} filter={shown} onFilterChange={setFilter} />
      )}
      {!ready ? (
        <Pane flush={props.flush} className={paneClass}>
          <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-hairline px-2">
            {name}
          </div>
          <LoadingRows rows={6} className="p-3" />
        </Pane>
      ) : (
        <LogWorkspace
          key={`${sourceId}|${jump}`}
          className={paneClass}
          flush={props.flush}
          compact={sheet}
          leading={!picker && product ? <ProductGlyph id={product} /> : undefined}
          name={picker}
          facts={facts}
          actions={
            (props.actions || exportable) && (
              <>
                {props.actions}
                {exportable && (
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
          pageLens={pageLens}
          onLensChange={(next) =>
            // Auto over a lens the page named is a choice of its own; the
            // page's lens chosen again is the page's reading back.
            setReadAsOn({
              source: sourceId,
              lens: next === "" && pageLens ? "auto" : next === pageLens ? "" : next,
            })
          }
          detectedLens={detected}
          readings={asChips ? readings : undefined}
          answered={asTiles ? readings : props.answeredBy}
          lineVerbs={props.lineVerbs}
          range={range}
          onRangeChange={(next) => {
            leaveWindow()
            setRange(next)
          }}
          since={since}
          until={until}
          onSinceChange={(value) => {
            leaveWindow()
            setSince(value)
          }}
          onUntilChange={(value) => {
            leaveWindow()
            setUntil(value)
          }}
          onCustomRange={(from, to) => {
            leaveWindow()
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
