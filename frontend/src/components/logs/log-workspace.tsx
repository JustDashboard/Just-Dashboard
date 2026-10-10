"use client"

import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react"
import {
  ChartActivity,
  ClockRewind,
  MagnifyingGlass,
  MagnifyingGlassMinus,
  Pause,
  Play,
  RefreshClockwise,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { errorMessage, get } from "@/lib/api"
import { plural } from "@/lib/format"
import type {
  LogLine,
  LogRetention,
  LogSearchResult,
  LogSource,
  LogSourceIndex,
  LogStreamMeta,
} from "@/lib/types"
import {
  EMPTY_FILTER,
  fieldPredicates,
  fieldsEqual,
  fieldsOf,
  filterQuery,
  isFilterActive,
  resolveRange,
} from "@/lib/log-filter"
import { LOG_FIELDS } from "@/lib/log-fields"
import { STACK_LENS, lensFor, type LogLens } from "@/lib/log-lenses"
import type { LogFilterState, LogMode, LogTimeRange } from "@/components/logs/types"
import type { Tone } from "@/components/tone"
import type { Verb } from "@/components/verbs"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import { EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { FilterBar, LevelChips, claimPane } from "@/components/logs/filter-bar"
import { Histogram } from "@/components/logs/histogram"
import { Insights } from "@/components/logs/insights"
import { LensBar } from "@/components/logs/lens-bar"
import type { LensReadingsState } from "@/components/logs/lens-readings"
import { LineDetail } from "@/components/logs/line-detail"
import { LogConsole } from "@/components/logs/log-console"
import { oneService, unitJournalLens } from "@/components/logs/logs-model"
import { RetentionNote } from "@/components/logs/retention-note"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Pane } from "@/components/panel"

/** How many lines the live pane holds before the oldest fall off the top. */
const LIVE_BUFFER = 4000

/** The opening window a live tail asks for. */
const TAIL_LINES = 500

/** The most keys one search may rank (`facets=`), the server's limit. */
const FACET_LIMIT = 12

/** datetime-local wants the browser's own clock, not an ISO string in UTC. */
function toLocalInput(date: Date) {
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 23)
}

const MODE_LABEL: Record<"live" | "search" | "insights", string> = {
  live: "Live",
  search: "History",
  insights: "Insights",
}

/** A page's own reading of the source, beside Live, History and Insights. */
export type WorkspaceView = {
  id: string
  label: string
  count?: number
  countTone?: Tone
  /** Keep the filter and the lens row above it: the view reads the same filter. */
  filtered?: boolean
  render: () => React.ReactNode
}

type WorkspaceProps = {
  onSubmitQuestion?: () => void
  refreshToken?: number
  source: LogSource
  sourceId: string
  units: LogSourceIndex["units"]
  mode: LogMode
  onModeChange: (mode: LogMode) => void
  filter: LogFilterState
  onFilterChange: (filter: LogFilterState) => void
  unit: string
  onUnitChange: (unit: string) => void
  range: LogTimeRange
  onRangeChange: (range: LogTimeRange) => void
  since: string
  until: string
  onSinceChange: (value: string) => void
  onUntilChange: (value: string) => void
  onCustomRange: (since: Date, until: Date) => void
  context: number
  onContextChange: (value: number) => void
  archives: boolean
  onArchivesChange: (value: boolean) => void
  boot: boolean
  onBootChange: (value: boolean) => void
  /**
   * The lens the reader asked for, sent as `lens=` on every request: "" reads
   * the source as detected, "none" as plain text.
   */
  lens?: string
  /** Offers "Read as" in the filter's More row. */
  onLensChange?: (lens: string) => void
  /** The lens the page named for its source: where the pane starts, not a setting the reader changed. */
  pageLens?: string
  /** What the source was detected as, where the page knows better than `source.lens` (a unit). */
  detectedLens?: string
  /** Which of the three readings are offered; all of them unless a page says otherwise. */
  modes?: ("live" | "search" | "insights")[]
  /** A page's own views of the source, after the three. */
  views?: WorkspaceView[]
  /** The lens's readings as the figures on the lens row's chips, for a page that draws no tiles. */
  readings?: LensReadingsState
  /**
   * The readings the page draws as tiles of its own: a quick view one of
   * them answers carries no count, since the tile says it over its window.
   */
  answered?: LensReadingsState
  /** A page's verbs for one line — "Block this address" on an auth line. */
  lineVerbs?: (line: LogLine) => Verb[]
  /** One column of a workbench that draws the frame: no frame of its own. */
  flush?: boolean
  /** A sheet's narrow column: the lens's value columns stay in the detail. */
  compact?: boolean
  /** What sits before the source's name in the top strip — the rail toggle. */
  leading?: React.ReactNode
  /**
   * The source's name in the strip, where it is a control: the picker of a
   * page's sources. `null` where the page names the source above the pane —
   * the strip is then its views alone, from its leading edge.
   */
  name?: React.ReactNode
  /** The source's facts, beside its name: its kind, path, size, state. */
  facts?: React.ReactNode
  /**
   * Commands for the source, after the views' tabs at the strip's end, as
   * glyphs: a boxed button between the name and the tabs split the strip in
   * two and read as one more tab.
   */
  actions?: React.ReactNode
  className?: string
}

/**
 * What decides whether History's answer is stale: everything a chip or a
 * popover changes. The words in the search box are not here — they wait for
 * Enter, because this scans the file and a keystroke-driven re-run would
 * queue a pass over gigabytes per character.
 */
function rerunKeyOf(filter: LogFilterState, lens: string | undefined) {
  return JSON.stringify({
    levels: [...filter.levels].sort(),
    fields: fieldPredicates(fieldsOf(filter)),
    regex: filter.regex,
    ignoreCase: filter.ignoreCase,
    lens: lens || "",
  })
}

/**
 * One source, read several ways.
 *
 * A single pane: a strip naming the source and switching between the stream,
 * its history, what it adds up to and the page's own views, the filter under
 * it, the lens's row when the source has a lens, the histogram when there is
 * one, and the lines. It used to be three framed surfaces stacked with
 * gutters between them — a filter panel, a histogram box, a console pane —
 * which was three boxes for one question. The hairlines between the rows are
 * the only edges now, and the whole thing takes one frame (or none, when it
 * is a column of the logs page's workbench).
 */
export function LogWorkspace(props: WorkspaceProps) {
  const { source, sourceId, mode, filter, onFilterChange, onModeChange } = props
  const paneId = useId()
  const forced = props.lens ?? ""
  const modes = props.modes ?? ["live", "search", "insights"]

  // The live filter trails what is being typed. Reconnecting the socket per
  // keystroke would restart the tail four times a word; waiting for a button
  // is the friction that made the old page's filter go unused. The pause is
  // short enough to read as instant and long enough to type through.
  const applied = useDebounced(filter, 400)
  const live = useLiveTail(sourceId, mode === "live" ? applied : null, props.boot, forced)

  // The lens the lines are drawn through: the one the reader forced, else the
  // one the server says it read them with, else what the source was detected
  // as. A stack whose containers disagree is read by its services.
  const served = mode === "live" ? live.meta?.lens : undefined
  const detected = props.detectedLens ?? source.lens
  const [searchedLens, setSearchedLens] = useState<string | undefined>()
  // What History's last answer was read through is that run's lens: once the
  // reader forces another, or goes back to Auto, it names nothing on screen
  // until History runs again, and Insights drew one source's figures in
  // another's vocabulary.
  const [searchedFor, setSearchedFor] = useState(forced)
  if (searchedFor !== forced) {
    setSearchedFor(forced)
    setSearchedLens(undefined)
  }
  const lensId =
    forced === "none"
      ? undefined
      : forced ||
        served ||
        searchedLens ||
        detected ||
        (source.kind === "stack" ? STACK_LENS : undefined)
  const lens = lensFor(lensId)
  const barLens = useMemo(
    () => unitJournalLens(lens, source.kind, sourceId),
    [lens, source.kind, sourceId],
  )

  const search = useHistorySearch(props, lens, setSearchedLens)

  // Insights re-reads on its own when a chip, a field or the window changes;
  // Enter, Search and a zoom are the asks that carry the words in the box.
  const [insightAsk, setInsightAsk] = useState(0)
  const [insightFacets, setInsightFacets] = useState<LogSearchResult["facets"]>()

  const refreshToken = props.refreshToken ?? 0
  useEffect(() => {
    if (!refreshToken) return
    if (mode === "live") live.reconnect()
    else if (mode === "search") search.run()
    // The token is an explicit refresh, not a change to the current question.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshToken])

  // Arriving on a shared link that already says "history, this term" should
  // show the answer, not a form with the question typed into it. The workspace
  // is keyed on the source, so this fires once per source rather than on every
  // change to the filter.
  const started = useRef(false)
  const runSearch = search.run
  useEffect(() => {
    if (mode !== "search" || started.current) return
    started.current = true
    runSearch()
  }, [mode, runSearch])

  // A chip, a field or a switch changes the question History answered, and
  // the answer on screen must not disagree with the chips above it. Keyed on
  // what those change, and compared against the question last asked, so the
  // run a mode switch or Enter already made is not made twice.
  const rerunKey = rerunKeyOf(filter, forced)
  const rerun = search.rerunIfStale
  useEffect(() => {
    if (mode === "search") rerun()
  }, [mode, rerunKey, rerun])

  const lines = mode === "live" ? live.lines : (search.result?.lines ?? [])
  const counts = useMemo(() => {
    if (mode === "search") {
      // The level facet counts every match; the histogram only the ones with
      // a timestamp, which on a log without them was zero beside a screen of lines.
      const facet = search.result?.facets?.level
      if (facet) return Object.fromEntries(facet.values.map((v) => [v.value, v.count]))
      const totals: Record<string, number> = {}
      for (const bucket of search.result?.histogram ?? []) {
        for (const [level, n] of Object.entries(bucket.counts)) {
          totals[level] = (totals[level] ?? 0) + n
        }
      }
      return totals
    }
    const totals: Record<string, number> = {}
    for (const line of live.lines) {
      if (line.cont) continue
      const level = line.level || "unknown"
      totals[level] = (totals[level] ?? 0) + 1
    }
    return totals
  }, [mode, live.lines, search.result])

  const retention = usePoll(
    (signal) => get<LogRetention>("/logs/retention", { source: sourceId }, signal),
    0,
    [sourceId],
    { enabled: Boolean(source.path) || source.kind === "pm2" },
  )

  // The lane a line already draws — a journal line's unit, a stack line's
  // service — is not a column as well.
  const laneKeys = useMemo(
    () =>
      source.kind === "stack"
        ? ["service"]
        : source.kind === "journal" || source.kind === "journal-id"
          ? ["unit", "program"]
          : [],
    [source.kind],
  )
  const columns = useMemo(
    () => (props.compact ? undefined : lens?.columns?.filter((key) => !laneKeys.includes(key))),
    [lens, laneKeys, props.compact],
  )
  // The words a `key:value` in the search box may name: what the lens reads,
  // and nothing at all on a source without one, where `error:` is prose.
  const fieldKeys = useMemo(
    () =>
      lens
        ? new Set(
            [
              ...Object.keys(LOG_FIELDS).filter((key) => LOG_FIELDS[key].filterable !== false),
              ...lens.facets,
            ].filter((key) => key !== "level"),
          )
        : undefined,
    [lens],
  )

  const switchMode = (next: LogMode, withFilter?: LogFilterState) => {
    if (next === mode) return
    onModeChange(next)
    if (next === "search") {
      // The arrival run above is for a link that opens on History; a switch
      // made here runs its own, and the two used to send the scan twice.
      started.current = true
      search.run({ filter: withFilter })
    }
  }

  const view = props.views?.find((v) => v.id === mode)
  const filtered = !view || view.filtered
  const named = props.name !== null
  const requirement = requirementOf(lens, filter)

  const renderDetail = (line: LogLine, head: LogLine | undefined) => (
    <LineDetail
      line={line}
      head={head}
      lens={lensId}
      forcedLens={forced || undefined}
      sourceId={sourceId}
      fields={fieldsOf(filter)}
      onFieldsChange={(fields) => onFilterChange({ ...filter, fields })}
      lineVerbs={props.lineVerbs}
    />
  )

  return (
    <Pane
      flush={props.flush}
      // Its rows' scroll edges cover with the pane's own card, not the plain
      // section around it: that ground showed as a dark block at the end of
      // every chip row on a Security page.
      className={cn("min-h-0 flex-1 [--panel-ground:var(--card)]", props.className)}
      onPointerDownCapture={() => claimPane(paneId)}
      onFocusCapture={() => claimPane(paneId)}
    >
      {/* The strip names what is being read and which question is being
          asked of it. The tabs are the section-tab underline because Live and
          History are two places within the source, not two commands. Where
          the pane is too narrow for the name beside every tab — a phone, a
          sheet — the tabs take a line of their own under it rather than
          squeezing the name to its chevron. Where the page names the source
          above the pane, the tabs lead the strip, as a deployment's do. */}
      <div className="flex min-h-10 shrink-0 flex-wrap items-stretch border-b border-hairline pr-1 pl-2">
        {named ? (
          <div className="@container flex min-w-[min(10rem,100%)] flex-1 items-center gap-2 py-1.5">
            {props.leading}
            {props.name ?? <span className="truncate text-body font-medium">{source.label}</span>}
            {props.facts}
          </div>
        ) : (
          props.leading && <div className="flex shrink-0 items-center pr-1">{props.leading}</div>
        )}
        <nav
          aria-label="Log mode"
          className={cn(
            "flex max-w-full items-stretch overflow-x-auto",
            named ? "shrink-0" : "min-w-0 flex-1",
          )}
        >
          {modes.map((id) => (
            <button
              key={id}
              type="button"
              aria-pressed={mode === id}
              className={cn(tabClasses(mode === id, "h-10"), "max-sm:px-2")}
              onClick={() => switchMode(id)}
            >
              {MODE_LABEL[id]}
            </button>
          ))}
          {props.views?.map((v) => (
            <button
              key={v.id}
              type="button"
              aria-pressed={mode === v.id}
              className={cn(tabClasses(mode === v.id, "h-10"), "max-sm:px-2")}
              onClick={() => switchMode(v.id)}
            >
              {v.label}
              {v.count !== undefined && v.count > 0 && (
                <ChipCount
                  className={cn(
                    v.countTone === "danger" && "text-destructive opacity-100",
                    v.countTone === "warning" && "text-warning opacity-100",
                  )}
                >
                  {v.count.toLocaleString()}
                </ChipCount>
              )}
            </button>
          ))}
        </nav>
        {props.actions && (
          <div className="flex shrink-0 items-center gap-1 pl-1">{props.actions}</div>
        )}
      </div>

      {filtered && (
        <FilterBar
          mode={mode}
          filter={filter}
          onFilterChange={onFilterChange}
          onSubmit={(next) => {
            props.onSubmitQuestion?.()
            if (mode === "search") search.run({ filter: next })
            else if (mode === "insights") setInsightAsk((n) => n + 1)
            else if (mode === "live") switchMode("search", next)
          }}
          searching={mode === "search" && search.loading}
          source={source}
          units={props.units}
          unit={props.unit}
          onUnitChange={props.onUnitChange}
          range={props.range}
          onRangeChange={(next) => {
            props.onRangeChange(next)
            if (mode === "search" && next !== "custom") search.run({ range: next })
          }}
          since={props.since}
          until={props.until}
          onSinceChange={props.onSinceChange}
          onUntilChange={props.onUntilChange}
          context={props.context}
          onContextChange={(next) => {
            props.onContextChange(next)
            if (mode === "search") search.run({ context: next })
          }}
          archives={props.archives}
          onArchivesChange={(next) => {
            props.onArchivesChange(next)
            if (mode === "search") search.run({ archives: next })
          }}
          boot={props.boot}
          onBootChange={(next) => {
            props.onBootChange(next)
            if (mode === "search") search.run({ boot: next })
          }}
          paneId={paneId}
          fieldKeys={fieldKeys}
          lens={forced}
          pageLens={props.pageLens}
          detectedLens={detected}
          onLensChange={props.onLensChange}
        />
      )}

      {filtered && (
        <LensBar
          lens={barLens}
          lensId={lensId}
          filter={filter}
          onFilterChange={onFilterChange}
          lines={mode === "live" ? live.lines : undefined}
          facets={
            mode === "search"
              ? search.result?.facets
              : mode === "insights"
                ? insightFacets
                : undefined
          }
          readings={props.readings}
          answered={props.answered}
        />
      )}

      {view ? (
        <div key={refreshToken} className="flex min-h-0 flex-1 flex-col overflow-auto">
          {view.render()}
        </div>
      ) : mode === "insights" ? (
        <div className="flex min-h-0 flex-1 flex-col overflow-auto">
          <Insights
            sourceId={sourceId}
            lens={lens}
            lensId={lensId}
            forcedLens={forced}
            filter={filter}
            onFilterChange={onFilterChange}
            onShowLines={(next) => {
              onFilterChange(next)
              switchMode("search", next)
            }}
            range={props.range}
            since={props.since}
            until={props.until}
            archives={props.archives}
            boot={props.boot}
            ask={insightAsk + refreshToken}
            onZoom={(from, to) => {
              props.onCustomRange(from, to)
              setInsightAsk((n) => n + 1)
            }}
            onOverview={(result) => setInsightFacets(result?.facets)}
          />
        </div>
      ) : (
        <>
          {mode === "search" && (search.result?.histogram.length ?? 0) > 0 && (
            <Histogram
              key={search.runId}
              className="animate-rise"
              buckets={search.result!.histogram}
              bucketSeconds={search.result!.bucketSeconds ?? 60}
              by={search.result!.histogramBy}
              lens={lensId}
              onZoom={(from, to) => {
                props.onCustomRange(from, to)
                search.run({ range: "custom", since: toLocalInput(from), until: toLocalInput(to) })
              }}
            />
          )}

          <LogConsole
            // The place is the question's as asked, not as the live tail
            // trails it: keyed on the debounced filter, a run's question
            // opened on an empty filter for 400ms, and a record opened in
            // that window was filed where Back and Forward never look.
            stateKey={`${sourceId}.${mode}.${JSON.stringify({ filter, range: props.range, since: props.since, until: props.until, boot: props.boot, lens: forced })}`}
            lines={lines}
            filter={applied}
            leading={<LevelChips filter={filter} onFilterChange={onFilterChange} counts={counts} />}
            showLineNumbers={mode === "search"}
            // A file's "source" is the file already chosen in the rail, repeated
            // on every line. The journal's is which unit spoke, and a stack's
            // which service, which is the point.
            showSource={source.kind === "journal" || source.kind === "stack"}
            showFile={mode === "search" && (search.result?.files.length ?? 0) > 1}
            paused={mode === "live" ? live.paused : undefined}
            onPausedChange={mode === "live" ? live.setPaused : undefined}
            held={live.held}
            onClear={mode === "live" ? live.clear : undefined}
            lens={lensId}
            columns={columns}
            collapseRepeats={mode === "live"}
            dividers={oneService(source.kind, sourceId)}
            renderDetail={renderDetail}
            status={
              mode === "live" ? (
                live.ended ? (
                  <>
                    <Status state="stopped" label="Stopped" className="text-hint" />
                    <Button
                      size="xs"
                      variant="ghost"
                      className="h-5 px-1.5"
                      onClick={live.reconnect}
                    >
                      <RefreshClockwise className="size-3" />
                      Reconnect
                    </Button>
                  </>
                ) : (
                  <>
                    <Status
                      state={
                        live.state === "open"
                          ? "running"
                          : live.state === "connecting"
                            ? "restarting"
                            : "stopped"
                      }
                      label={
                        live.state === "open"
                          ? "Live"
                          : live.state === "connecting"
                            ? "Connecting"
                            : "Disconnected"
                      }
                      live={live.state === "open"}
                      className="text-hint"
                    />
                    {live.meta?.prefill && !live.meta.prefill.complete && (
                      <Tag title={live.meta.note}>partial history</Tag>
                    )}
                  </>
                )
              ) : (
                <SearchSummary result={search.result} loading={search.loading} />
              )
            }
            footer={
              <>
                <LevelSummary counts={counts} />
                {mode === "search" && search.result && <SearchNotes result={search.result} />}
                {retention.data && (
                  <span className="ml-auto flex max-w-full min-w-0">
                    <RetentionNote retention={retention.data} />
                  </span>
                )}
              </>
            }
            empty={
              mode === "live" ? (
                <LiveEmpty
                  state={live.state}
                  ended={live.ended}
                  error={live.error}
                  held={live.held}
                  onResume={() => live.setPaused(false)}
                  filter={applied}
                  requirement={requirement}
                  onReconnect={live.reconnect}
                  onSearchHistory={() => switchMode("search")}
                  onClearFilter={() => onFilterChange(EMPTY_FILTER)}
                />
              ) : (
                <SearchEmpty
                  result={search.result}
                  loading={search.loading}
                  error={search.error}
                  source={source}
                  archives={props.archives}
                  requirement={requirement}
                  onIncludeArchives={() => {
                    props.onArchivesChange(true)
                    search.run({ archives: true })
                  }}
                  onRun={() => search.run()}
                />
              )
            }
          />
        </>
      )}
    </Pane>
  )
}

/**
 * The server setting a quick view's lines depend on, when that view is the
 * question and nothing came back: Postgres writes no slow statements until
 * `log_min_duration_statement` says how slow is slow, and "no matches" alone
 * reads as "no slow statements".
 */
function requirementOf(lens: LogLens | undefined, filter: LogFilterState) {
  const fields = fieldsOf(filter)
  return lens?.views.find(
    (view) =>
      view.requires && Object.keys(fields).length > 0 && fieldsEqual(fields, view.fields ?? {}),
  )?.requires
}

function useDebounced<T>(value: T, delay: number): T {
  const [settled, setSettled] = useState(value)
  const key = JSON.stringify(value)
  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delay)
    return () => clearTimeout(timer)
    // The value is an object rebuilt on every keystroke, so the identity is
    // useless as a dependency and its content is what actually changed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, delay])
  return settled
}

/**
 * Where a reconnect resumes: the newest instant already on screen and the
 * texts of the window the server is about to replay. The server opens every
 * connection with the last few hundred lines, so a tunnel that dropped for a
 * second used to append those hundreds again under themselves.
 */
type Resume = { stamp: number; texts: Set<string> }

function resumeFrom(lines: LogLine[]): Resume | null {
  if (lines.length === 0) return null
  let stamp = -Infinity
  for (let i = lines.length - 1; i >= 0; i--) {
    const at = lines[i].timestamp ? Date.parse(lines[i].timestamp!) : NaN
    if (!Number.isNaN(at)) {
      stamp = at
      break
    }
  }
  return { stamp, texts: new Set(lines.slice(-TAIL_LINES).map((l) => l.text)) }
}

/**
 * The lines of a replayed batch that are new. Once one line is new, every
 * line after it is too: the replay is contiguous, and a line that merely
 * repeats an old text after that point is a line that was written again.
 */
function unseen(batch: LogLine[], resume: Resume): { lines: LogLine[]; caughtUp: boolean } {
  for (let i = 0; i < batch.length; i++) {
    const line = batch[i]
    const at = line.timestamp ? Date.parse(line.timestamp) : NaN
    const stamped = !Number.isNaN(at)
    const old = stamped
      ? at < resume.stamp || (at === resume.stamp && resume.texts.has(line.text))
      : resume.texts.has(line.text)
    if (!old) return { lines: batch.slice(i), caughtUp: true }
  }
  return { lines: [], caughtUp: false }
}

/**
 * The live tail.
 *
 * Pausing holds what arrives rather than dropping it, which is the difference
 * between reading a busy log and choosing between reading and keeping. The
 * held lines are counted and appended on resume.
 */
function useLiveTail(sourceId: string, filter: LogFilterState | null, boot: boolean, lens: string) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [meta, setMeta] = useState<LogStreamMeta | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [paused, setPaused] = useState(false)
  // The stream said it was over — a container that stopped, a file that went.
  // Reconnecting would replay its last lines once a second for ever.
  const [ended, setEnded] = useState(false)
  // Held lines are state rather than a ref because the reset happens during
  // render, and a ref written there is a value React never sees.
  const [heldLines, setHeldLines] = useState<LogLine[]>([])
  const pausedRef = useRef(false)
  const onScreen = useRef<LogLine[]>([])
  const resume = useRef<Resume | null>(null)

  useEffect(() => {
    pausedRef.current = paused
    onScreen.current = heldLines.length ? [...lines, ...heldLines] : lines
  }, [paused, lines, heldLines])

  const query = useMemo(
    () =>
      filter
        ? {
            source: sourceId,
            lines: TAIL_LINES,
            boot: boot ? "true" : undefined,
            lens: lens || undefined,
            ...filterQuery(filter),
          }
        : { source: "" },
    [sourceId, filter, boot, lens],
  )
  const queryKey = JSON.stringify(query)

  // A new filter is a new question: the socket restarts with a window that
  // matches it, so mixing the answers would put lines the filter rejects above
  // the ones it keeps. Reset during render rather than in an effect — the two
  // differ by one frame, and that frame is the old log's tail drawn under the
  // new question.
  const [lastQuery, setLastQuery] = useState(queryKey)
  if (lastQuery !== queryKey) {
    setLastQuery(queryKey)
    setLines([])
    setMeta(null)
    setError(null)
    setHeldLines([])
    setEnded(false)
  }

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.error) {
      setError(envelope.error)
      return
    }
    if (envelope.type === "meta") {
      setMeta(envelope.data as LogStreamMeta)
      // The same question on a new connection: keep what is on screen and
      // take only what is newer from the replay that follows.
      resume.current = resumeFrom(onScreen.current)
      return
    }
    if (envelope.type === "eof") {
      setEnded(true)
      return
    }
    if (envelope.type !== "logs") return
    let batch = envelope.data as LogLine[]
    if (resume.current) {
      const next = unseen(batch, resume.current)
      batch = next.lines
      if (next.caughtUp) resume.current = null
      if (batch.length === 0) return
    }
    if (pausedRef.current) {
      setHeldLines((prev) => capLines([...prev, ...batch]))
      return
    }
    setLines((prev) => capLines([...prev, ...batch]))
  }, [])

  const { state } = useSocket("/logs/stream", {
    onMessage,
    query,
    enabled: Boolean(filter && sourceId) && !ended,
  })

  const setPausedAndFlush = useCallback(
    (next: boolean) => {
      setPaused(next)
      if (next) return
      if (heldLines.length) setLines((prev) => capLines([...prev, ...heldLines]))
      setHeldLines([])
    },
    [heldLines],
  )

  const clear = useCallback(() => {
    setLines([])
    setHeldLines([])
  }, [])

  const reconnect = useCallback(() => {
    setError(null)
    setEnded(false)
  }, [])

  return {
    lines,
    meta,
    error,
    state,
    ended,
    reconnect,
    paused,
    setPaused: setPausedAndFlush,
    held: heldLines.length,
    clear,
  }
}

function capLines(lines: LogLine[]) {
  return lines.length > LIVE_BUFFER ? lines.slice(lines.length - LIVE_BUFFER) : lines
}

/**
 * The history search.
 *
 * It runs on Enter rather than as you type, and that is deliberate: this scans
 * the file — and its rotated archives when asked — so a keystroke-triggered
 * version would queue a full pass over gigabytes per character. The live mode
 * next to it is the one that answers instantly. What a chip changes re-runs
 * it (`rerunIfStale`), because a chip is one deliberate press.
 */
function useHistorySearch(
  props: WorkspaceProps,
  lens: LogLens | undefined,
  onLens: (lens: string | undefined) => void,
) {
  const [result, setResult] = useState<LogSearchResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // Which run the result on screen came from, so the histogram can rise once
  // per answer rather than once per mount.
  const [runId, setRunId] = useState(0)
  const abort = useRef<AbortController | null>(null)
  // Which answer is still wanted. A sequence number rather than the abort
  // signal, because "this request was superseded" and "this request was
  // cancelled" are different questions and only the first should decide
  // whether to render — reading it off the signal left the pane stuck on
  // "Searching…" forever the first time anything cancelled a live request.
  const seq = useRef(0)
  // The question the last run asked, or null before the first.
  const asked = useRef<string | null>(null)
  // The runner is called from event handlers that already know what changed,
  // so the current values are read from a ref rather than from a closure that
  // was built before the change landed.
  const latest = useRef({ props, lens })
  useEffect(() => {
    latest.current = { props, lens }
  })

  const run = useCallback(
    async (
      overrides: Partial<{
        range: LogTimeRange
        since: string
        until: string
        context: number
        archives: boolean
        boot: boolean
        filter: LogFilterState
      }> = {},
    ) => {
      const { props: p, lens: l } = latest.current
      const filter = overrides.filter ?? p.filter
      const forced = p.lens ?? ""
      const range = overrides.range ?? p.range
      const id = ++seq.current
      asked.current = rerunKeyOf(filter, forced)
      abort.current?.abort()
      const controller = new AbortController()
      abort.current = controller
      setLoading(true)
      setError(null)
      try {
        const window = resolveRange(range, overrides.since ?? p.since, overrides.until ?? p.until)
        if (window.since && window.until && Date.parse(window.since) >= Date.parse(window.until)) {
          throw new Error("The start of the log window must be before its end.")
        }
        // The level facet counts the chips; the lens's facets rank the
        // Fields popover's values over every match, not only those on screen.
        const facets = [...new Set(["level", ...(l?.facets ?? [])])].slice(0, FACET_LIMIT)
        const res = await get<LogSearchResult>(
          "/logs/search",
          {
            source: p.sourceId,
            ...filterQuery(filter),
            ...window,
            lens: forced || undefined,
            facets: facets.join(","),
            before: (overrides.context ?? p.context) || undefined,
            after: (overrides.context ?? p.context) || undefined,
            archives: (overrides.archives ?? p.archives) ? "true" : undefined,
            boot: (overrides.boot ?? p.boot) ? "true" : undefined,
            limit: 3000,
          },
          controller.signal,
        )
        if (id !== seq.current) return
        setResult(res)
        setRunId(id)
        onLens(res.lens || undefined)
      } catch (err) {
        if (id !== seq.current) return
        setError(errorMessage(err))
        setResult(null)
      } finally {
        if (id === seq.current) setLoading(false)
      }
    },
    [onLens],
  )

  const rerunIfStale = useCallback(() => {
    const { props: p } = latest.current
    if (asked.current !== null && asked.current !== rerunKeyOf(p.filter, p.lens)) void run()
  }, [run])

  return { result, loading, error, run, rerunIfStale, runId }
}

function SearchSummary({ result, loading }: { result: LogSearchResult | null; loading: boolean }) {
  if (loading) {
    return <span className="text-hint text-muted-foreground">Searching…</span>
  }
  if (!result) return <span className="text-hint text-muted-foreground">History</span>
  return (
    <span className="numeric text-hint text-muted-foreground">
      {plural(result.matched, "match", "matches")} in {result.scanned.toLocaleString()} lines ·{" "}
      {result.tookMillis}ms
    </span>
  )
}

/**
 * How many of what is on screen needs attention. Two figures, each a reading
 * in its own colour, and nothing when there is nothing to say.
 */
function LevelSummary({ counts }: { counts: Record<string, number> }) {
  const errors = (counts.critical ?? 0) + (counts.error ?? 0)
  const warnings = counts.warn ?? 0
  if (errors === 0 && warnings === 0) return null
  return (
    <span className="flex items-center gap-x-2">
      {errors > 0 && (
        <span className="numeric font-medium text-destructive">{plural(errors, "error")}</span>
      )}
      {warnings > 0 && (
        <span className="numeric font-medium text-warning">{plural(warnings, "warning")}</span>
      )}
    </span>
  )
}

/**
 * What was actually read. A search that spans a rotated set and finds
 * everything in yesterday's file is saying something a merged total hides, and
 * a truncated answer that does not admit it is worse than no answer.
 */
function SearchNotes({ result }: { result: LogSearchResult }) {
  const notes: string[] = []
  if (result.truncated) {
    notes.push(
      `Showing the most recent ${result.lines.length.toLocaleString()} of ${result.matched.toLocaleString()} matches — narrow the window to see the rest.`,
    )
  }
  if (!result.complete) {
    notes.push("The scan hit its time limit before reaching the end.")
  }
  const files = result.files.filter((f) => f.matched > 0 || f.error)
  if (notes.length === 0 && files.length <= 1) return null

  return (
    <>
      {notes.map((note) => (
        <span key={note}>{note}</span>
      ))}
      {files.length > 1 &&
        files.map((file) => (
          <span key={file.path} className="numeric" title={file.path}>
            {file.name}
            {file.archive && " (archive)"}:{" "}
            {file.error ?? `${file.matched.toLocaleString()} matched`}
          </span>
        ))}
    </>
  )
}

function LiveEmpty({
  state,
  ended,
  error,
  held,
  onResume,
  filter,
  requirement,
  onReconnect,
  onSearchHistory,
  onClearFilter,
}: {
  state: string
  ended: boolean
  error: string | null
  /** Lines that arrived while paused, which an empty pane is not the absence of. */
  held: number
  onResume: () => void
  filter: LogFilterState
  requirement?: string
  onReconnect: () => void
  onSearchHistory: () => void
  onClearFilter: () => void
}) {
  if (error) {
    return <ErrorState error={new Error(error)} className="max-w-lg" />
  }
  if (ended) {
    return (
      <EmptyState
        icon={ChartActivity}
        title="The stream ended"
        description="The source stopped writing — a container that exited, a file that was removed. Its history is still searchable."
        action={
          <div className="flex flex-wrap justify-center gap-2">
            <Button size="sm" onClick={onSearchHistory}>
              <ClockRewind className="size-3.5" />
              Search this log&apos;s history
            </Button>
            <Button size="sm" variant="outline" onClick={onReconnect}>
              <RefreshClockwise className="size-3.5" />
              Reconnect
            </Button>
          </div>
        }
      />
    )
  }
  // Paused, a new question's lines are held like any others: the pane is
  // empty because of the pause, not because nothing matches.
  if (held > 0) {
    return (
      <EmptyState
        icon={Pause}
        title={`${plural(held, "line")} arrived while paused`}
        description="The stream holds what arrives while you read. Resume to see them."
        action={
          <Button size="sm" onClick={onResume}>
            <Play className="size-3.5" />
            Resume
          </Button>
        }
      />
    )
  }
  if (state !== "open") {
    return (
      <EmptyState
        icon={ChartActivity}
        title={state === "connecting" ? "Connecting…" : "Not connected"}
        description={
          state === "connecting"
            ? "Opening the stream."
            : "The stream closed. It reconnects on its own; this page rides a tunnel that drops routinely."
        }
      />
    )
  }
  if (isFilterActive(filter)) {
    return (
      <EmptyState
        icon={MagnifyingGlassMinus}
        title="Nothing in the recent window matches"
        description={
          requirement
            ? `These lines are written only when ${requirement} is set on the server — check it before reading the silence as an answer. History goes further back than a live tail can.`
            : "The filter was applied on the server, so this really is every matching line in the opening window — not a slice of it. History goes further back than a live tail can."
        }
        action={
          <div className="flex flex-wrap justify-center gap-2">
            <Button size="sm" onClick={onSearchHistory}>
              <ClockRewind className="size-3.5" />
              Search this log&apos;s history
            </Button>
            <Button size="sm" variant="outline" onClick={onClearFilter}>
              Clear the filter
            </Button>
          </div>
        }
      />
    )
  }
  return (
    <EmptyState
      icon={ChartActivity}
      title="Nothing new yet"
      description="This log is quiet. New lines appear here the moment they are written."
    />
  )
}

function SearchEmpty({
  result,
  loading,
  error,
  source,
  archives,
  requirement,
  onIncludeArchives,
  onRun,
}: {
  result: LogSearchResult | null
  loading: boolean
  error: string | null
  source: LogSource
  archives: boolean
  requirement?: string
  onIncludeArchives: () => void
  onRun: () => void
}) {
  if (loading) {
    return (
      <EmptyState
        icon={MagnifyingGlass}
        title="Searching…"
        description="Reading the file server-side."
      />
    )
  }
  if (error) {
    return <ErrorState error={new Error(error)} className="max-w-lg" />
  }
  if (!result) {
    return (
      <EmptyState
        icon={ClockRewind}
        title="Search the whole log, not just the tail"
        description="Type a term and press Enter. The scan happens on the server, so this works on a file far too large to send to a browser — and it reads the rotated archives too, which is where last night's answer usually is."
        action={
          <Button size="sm" onClick={onRun}>
            <MagnifyingGlass className="size-3.5" />
            Search
          </Button>
        }
      />
    )
  }
  const hasArchives = (source.archives ?? 0) > 0
  return (
    <EmptyState
      icon={MagnifyingGlassMinus}
      title="No matches in that window"
      description={
        requirement
          ? `Scanned ${result.scanned.toLocaleString()} lines. These lines are written only when ${requirement} is set on the server.`
          : `Scanned ${result.scanned.toLocaleString()} lines in ${result.tookMillis}ms. Widen the window, relax the level filter, or look further back.`
      }
      action={
        hasArchives && !archives ? (
          <Button size="sm" onClick={onIncludeArchives}>
            Include the {source.archives} rotated {source.archives === 1 ? "archive" : "archives"}
          </Button>
        ) : undefined
      }
    />
  )
}
