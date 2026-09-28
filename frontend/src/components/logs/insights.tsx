"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { LineChart, MagnifyingGlassMinus } from "@/components/icons"
import { cn } from "@/lib/utils"
import { errorMessage, get } from "@/lib/api"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import { latency } from "@/lib/requests"
import type { LogFacet, LogFacetValue, LogSearchResult } from "@/lib/types"
import type { LogFilterState, LogTimeRange } from "@/components/logs/types"
import {
  fieldPredicates,
  fieldsOf,
  filterQuery,
  onlyField,
  resolveRange,
  toggleField,
  type LogLevel,
} from "@/lib/log-filter"
import { fieldOf, isFilterable } from "@/lib/log-fields"
import { groupScope, overviewFacets, overviewSplit, patternRegex } from "@/lib/log-insights"
import type { LensGroup, LogLens } from "@/lib/log-lenses"
import { BarList, type BarListItem } from "@/components/bar-list"
import { Facet } from "@/components/deploy/traffic-facets"
import { LatencyLadder } from "@/components/deploy/latency-ladder"
import { FieldMark, FieldValue } from "@/components/logs/field-value"
import { LevelChips } from "@/components/logs/filter-bar"
import { Histogram } from "@/components/logs/histogram"
import { LensReadings, useLensReadings } from "@/components/logs/lens-readings"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/** How many values each list draws. */
const SHOWN = 8

/** How many rows a group's table ranks. */
const GROUP_ROWS = 20

/**
 * A query's shape is a hash, read by the statement it stands for — which
 * only a group's sample carries. As a list of its own it is twelve hex
 * strings, so the overview ranks it and the lists leave it to the groups.
 */
const BY_SAMPLE_ONLY = new Set(["fp"])

type InsightsProps = {
  sourceId: string
  lens: LogLens | undefined
  /** The lens the lines are read through, which names event values. */
  lensId: string | undefined
  /** The lens the reader forced, sent as `lens=`. */
  forcedLens: string
  filter: LogFilterState
  onFilterChange: (filter: LogFilterState) => void
  /** A pattern's lines, in History. */
  onShowLines: (filter: LogFilterState) => void
  range: LogTimeRange
  since: string
  until: string
  archives: boolean
  boot: boolean
  /**
   * Bumped by Enter, Search and a zoom: the words in the search box and a
   * typed window are asked only then, as History asks them.
   */
  ask: number
  /** The lens's readings at the top — the logs page; a service page draws its own. */
  readings?: boolean
  onZoom: (since: Date, until: Date) => void
  /** The overview's answer, for the lens row's counts. */
  onOverview?: (result: LogSearchResult | null) => void
}

type Answer = {
  overview: LogSearchResult
  /** Each group's answer, or null for a group the reader's filter rules out. */
  groups: Record<string, LogSearchResult | null>
}

/**
 * What the log adds up to over the window and the filter on screen.
 *
 * The request log's Insights, for any log a lens reads: the lens's readings,
 * the events over time, each of the lens's keys ranked (a press narrows to
 * the value and stays here, as the request lists do), the measure's
 * distribution where there is one, each of the lens's groups as a ranked
 * table — the slow statements by their shape, the attackers by address —
 * and the patterns every line falls into, which a log with no lens has too.
 *
 * One search for the overview and one per group, in parallel: the groups
 * each rank one key under their own predicates and measure, which one search
 * cannot. They run when the view opens and again when a chip, a field or the
 * window changes; the words in the search box wait for Enter, as History's do.
 */
export function Insights(props: InsightsProps) {
  const { lens, lensId, filter, onFilterChange } = props
  const { answer, error, loading, runId } = useInsights(props)
  const readings = useLensReadings(props.sourceId, lens, {
    forcedLens: props.forcedLens,
    enabled: Boolean(props.readings),
    range: { range: props.range, since: props.since, until: props.until },
  })

  const overview = answer?.overview
  const levelCounts = useMemo(
    () =>
      Object.fromEntries(
        (overview?.facets?.level?.values ?? []).map((v) => [v.value, v.count] as const),
      ),
    [overview],
  )

  const narrow = (key: string, value: string) => {
    if (key === "level") {
      const level = value as LogLevel
      onFilterChange({
        ...filter,
        levels: filter.levels.includes(level)
          ? filter.levels.filter((l) => l !== level)
          : [...filter.levels, level],
      })
      return
    }
    onFilterChange({ ...filter, fields: toggleField(fieldsOf(filter), key, value) })
  }

  const measure = lens?.measure
  const ladder = measure?.format === "ms" && overview?.measure && overview.measure.count > 0
  const lists = overviewFacets(lens).filter(
    (key) =>
      key !== "pattern" &&
      !BY_SAMPLE_ONLY.has(key) &&
      (overview?.facets?.[key]?.values.length ?? 0),
  )
  const columns = [lists.filter((_, i) => i % 2 === 0), lists.filter((_, i) => i % 2 === 1)]
  const patterns = overview?.facets?.pattern

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-10 shrink-0 items-center gap-2 overflow-x-auto border-b border-hairline px-2 py-1">
        <LevelChips filter={filter} onFilterChange={onFilterChange} counts={levelCounts} />
        <span className="numeric ml-auto shrink-0 pr-1 text-hint whitespace-nowrap text-muted-foreground">
          {loading
            ? "Reading the window…"
            : overview
              ? `${plural(overview.matched, "match", "matches")} in ${overview.scanned.toLocaleString()} lines · ${overview.tookMillis}ms`
              : null}
        </span>
      </div>

      {props.readings && readings.tiles.length > 0 && (
        <div className="shrink-0 border-b border-hairline px-5">
          <LensReadings readings={readings} filter={filter} onFilterChange={onFilterChange} />
        </div>
      )}

      {error ? (
        <div className="flex flex-1 items-center justify-center p-6">
          <ErrorState error={new Error(error)} className="max-w-lg" />
        </div>
      ) : !answer ? (
        <LoadingRows rows={6} className="p-5" />
      ) : answer.overview.matched === 0 ? (
        <div className="flex flex-1 items-center justify-center p-6">
          <EmptyState
            icon={MagnifyingGlassMinus}
            title="Nothing in this window"
            description={`Scanned ${answer.overview.scanned.toLocaleString()} lines and none matched. Widen the window, or let a chip or a field go.`}
          />
        </div>
      ) : (
        <div key={runId} className={cn("flex flex-col", loading && "opacity-60")}>
          {answer.overview.histogram.length > 0 && (
            <Histogram
              className="animate-rise"
              title={lens ? "Events over time" : "Matches over time"}
              buckets={answer.overview.histogram}
              bucketSeconds={answer.overview.bucketSeconds ?? 60}
              by={answer.overview.histogramBy}
              lens={lensId}
              onZoom={props.onZoom}
            />
          )}

          <div className="flex flex-col gap-8 px-5 py-5">
            {ladder && measure && (
              <Facet
                title={measure.label}
                reading={`mean ${latency(answer.overview.measure!.mean)} · ${plural(answer.overview.measure!.count, "timed line")}`}
                index={0}
              >
                <LatencyLadder
                  latency={answer.overview.measure!}
                  onPick={(ms) =>
                    onFilterChange({
                      ...filter,
                      fields: atLeast(fieldsOf(filter), measure.key, ms),
                    })
                  }
                />
              </Facet>
            )}

            {lists.length > 0 && (
              <div className="grid grid-cols-1 gap-x-10 gap-y-8 lg:grid-cols-2">
                {columns.map((keys, c) => (
                  <div key={c} className="flex min-w-0 flex-col gap-8">
                    {keys.map((key, i) => (
                      <Facet
                        key={key}
                        title={titleOf(key)}
                        reading={tallyOf(answer.overview.facets![key])}
                        index={i * 2 + c + 1}
                      >
                        <BarList
                          items={facetItems(key, answer.overview.facets![key], lensId, narrow)}
                        />
                      </Facet>
                    ))}
                  </div>
                ))}
              </div>
            )}

            {lens?.groups?.map((group, i) => (
              <Facet
                key={group.id}
                title={group.label}
                reading={
                  answer.groups[group.id]?.facets?.[group.by]
                    ? tallyOf(answer.groups[group.id]!.facets![group.by], GROUP_ROWS)
                    : undefined
                }
                index={lists.length + i + 1}
              >
                <GroupTable
                  group={group}
                  result={answer.groups[group.id]}
                  lensId={lensId}
                  onPick={(value) => {
                    const scope = groupScope(group, filter)
                    if (!scope) return
                    onFilterChange({
                      ...scope,
                      fields: onlyField(fieldsOf(scope), group.by, value),
                    })
                  }}
                />
              </Facet>
            ))}

            {patterns && patterns.values.length > 0 && (
              <Facet
                title="Patterns"
                reading={tallyOf(patterns)}
                index={lists.length + (lens?.groups?.length ?? 0) + 1}
              >
                <PatternTable
                  facet={patterns}
                  onPick={(pattern) =>
                    props.onShowLines({ ...filter, q: patternRegex(pattern), regex: true })
                  }
                />
              </Facet>
            )}

            {!lens && lists.length <= 1 && (!patterns || patterns.values.length === 0) && (
              <EmptyState
                icon={LineChart}
                title="Nothing to rank here"
                description="This log is read as plain text, so its lines carry no fields to rank. Read it through a lens under More to rank its events and values."
              />
            )}
          </div>
        </div>
      )}
    </div>
  )
}

/**
 * The searches. Keyed on what a chip, a field and the window change — never
 * on the words in the box, which wait for `ask` — and read from a ref at run
 * time, so a run never answers the question from a render ago.
 */
function useInsights(props: InsightsProps) {
  const [answer, setAnswer] = useState<Answer | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [runId, setRunId] = useState(0)
  const latest = useRef(props)
  useEffect(() => {
    latest.current = props
  })
  const seq = useRef(0)

  const { filter, forcedLens, range, archives, boot, ask, sourceId } = props
  const key = JSON.stringify({
    levels: [...filter.levels].sort(),
    fields: fieldPredicates(fieldsOf(filter)),
    regex: filter.regex,
    ignoreCase: filter.ignoreCase,
    lens: forcedLens,
    range,
    archives,
    boot,
    ask,
    sourceId,
  })

  const run = useCallback(async (signal: AbortSignal) => {
    const p = latest.current
    // A custom window with nothing typed yet is a question not asked: the
    // answer on screen stays until Enter says what the window is.
    if (p.range === "custom" && !p.since && !p.until && seq.current > 0) return
    const id = ++seq.current
    setLoading(true)
    setError(null)
    try {
      const window = resolveRange(p.range, p.since, p.until)
      if (window.since && window.until && Date.parse(window.since) >= Date.parse(window.until)) {
        throw new Error("The start of the log window must be before its end.")
      }
      const common = {
        source: p.sourceId,
        lens: p.forcedLens || undefined,
        ...window,
        archives: p.archives ? "true" : undefined,
        boot: p.boot ? "true" : undefined,
        limit: 1,
      }
      const groups = p.lens?.groups ?? []
      const [overview, ...ranked] = await Promise.all([
        get<LogSearchResult>(
          "/logs/search",
          {
            ...common,
            ...filterQuery(p.filter),
            facets: overviewFacets(p.lens).join(","),
            measure: p.lens?.measure?.key,
            histogramBy: overviewSplit(p.lens),
          },
          signal,
        ),
        ...groups.map((group) => {
          const scope = groupScope(group, p.filter)
          return scope
            ? get<LogSearchResult>(
                "/logs/search",
                {
                  ...common,
                  ...filterQuery(scope),
                  facets: group.by,
                  facetLimit: GROUP_ROWS,
                  measure: group.measure,
                  sample: group.sample?.join(","),
                },
                signal,
              )
            : Promise.resolve(null)
        }),
      ])
      if (id !== seq.current) return
      setAnswer({
        overview,
        groups: Object.fromEntries(groups.map((group, i) => [group.id, ranked[i]])),
      })
      setRunId(id)
      p.onOverview?.(overview)
    } catch (err) {
      if (id !== seq.current || signal.aborted) return
      setError(errorMessage(err))
      p.onOverview?.(null)
    } finally {
      if (id === seq.current) setLoading(false)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void run(controller.signal)
    return () => controller.abort()
  }, [key, run])

  return { answer, error, loading, runId }
}

/** The measure's floor replaced, so "at least this slow" is one condition, not a pile of them. */
function atLeast(fields: Record<string, string[]>, key: string, ms: number) {
  const kept = (fields[key] ?? []).filter((value) => !value.startsWith(">"))
  return { ...fields, [key]: [...kept, `>=${Math.floor(ms)}`] }
}

function titleOf(key: string) {
  if (key === "event") return "Events"
  if (key === "level") return "Levels"
  const label = fieldOf(key).label
  return label.charAt(0).toUpperCase() + label.slice(1)
}

/**
 * A list's reading says what is known: the server ranks a key's values
 * past the few drawn and counts how many there were, so a full list is the
 * top of a longer one — "top 8 of 312" — and a count past its budget says so.
 */
function tallyOf(facet: LogFacet, shown = SHOWN) {
  const distinct = `${facet.distinct.toLocaleString()}${facet.distinctCapped ? "+" : ""}`
  return facet.values.length > shown || facet.distinct > shown
    ? `top ${Math.min(shown, facet.values.length)} of ${distinct}`
    : plural(facet.distinct, "value")
}

function facetItems(
  key: string,
  facet: LogFacet,
  lensId: string | undefined,
  narrow: (key: string, value: string) => void,
): BarListItem[] {
  const values = facet.values.slice(0, SHOWN)
  const peak = Math.max(...values.map((v) => v.count), 1)
  const field = fieldOf(key)
  const verdict = key === "event" || key === "level"
  const marked = field.kind === "address" || field.kind === "product"
  const pressable = key === "level" || isFilterable(key)
  return values.map((v) => ({
    key: v.value,
    mark: marked ? <FieldMark name={key} value={v.value} /> : undefined,
    mono: !(verdict || field.kind === "product" || field.kind === "lane"),
    label: <FieldValue name={key} value={v.value} lens={lensId} glyph={false} />,
    value: v.count.toLocaleString(),
    share: v.count / peak,
    // An event and a level are the verdict already, in their own colour; a
    // client or a path is where the share of errors is the news.
    signal: !verdict && v.count ? v.errors / v.count : undefined,
    hint:
      !verdict && v.errors > 0 ? (
        <span className="text-destructive">{plural(v.errors, "error")}</span>
      ) : v.sum !== undefined && v.count > 0 && key !== "level" ? (
        `mean ${latency(v.sum / v.count)}`
      ) : undefined,
    title: pressable
      ? key === "level"
        ? `Only ${v.value} lines`
        : `Only lines where ${field.label} is ${v.value}`
      : undefined,
    onClick: pressable ? () => narrow(key, v.value) : undefined,
  }))
}

function measured(key: string | undefined) {
  const kind = key ? fieldOf(key).kind : undefined
  return (n: number) =>
    kind === "duration" ? latency(n) : kind === "bytes" ? bytes(n) : n.toLocaleString()
}

/**
 * A group, ranked: what its key's values add up to under the group's own
 * predicates — how many lines, how many of them errors, how long they took
 * in all and at worst, when they were last seen — with the last value of the
 * group's sample keys beside each, so a statement's shape reads as the
 * statement and an attacking address as the usernames it tried. A press
 * narrows everything above to that value.
 */
function GroupTable({
  group,
  result,
  lensId,
  onPick,
}: {
  group: LensGroup
  result: LogSearchResult | null | undefined
  lensId: string | undefined
  onPick: (value: string) => void
}) {
  if (result === null) {
    return (
      <p className="py-3 text-hint text-muted-foreground">
        The filter on screen rules these lines out.
      </p>
    )
  }
  const facet = result?.facets?.[group.by]
  if (!facet || facet.values.length === 0) {
    return <p className="py-3 text-hint text-muted-foreground">None in this window.</p>
  }
  const values = facet.values.slice(0, GROUP_ROWS)
  const errors = values.some((v) => v.errors > 0)
  const timed = Boolean(group.measure) && values.some((v) => v.sum !== undefined)
  const format = measured(group.measure)
  const byQuery = group.by === "fp" && values.some((v) => v.samples?.query)
  const cell = "px-2 py-1.5"
  const figure = cn(cell, "numeric text-right text-muted-foreground")

  return (
    <Table containerClassName="-mx-2 w-auto">
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead className="h-8 px-2">{byQuery ? "Query" : titleOf(group.by)}</TableHead>
          <TableHead className="h-8 px-2 text-right">Lines</TableHead>
          {errors && <TableHead className="h-8 px-2 text-right">Errors</TableHead>}
          {timed && (
            <>
              <TableHead className="h-8 px-2 text-right max-sm:hidden">Total</TableHead>
              <TableHead className="h-8 px-2 text-right max-md:hidden">Mean</TableHead>
              <TableHead className="h-8 px-2 text-right">Max</TableHead>
            </>
          )}
          <TableHead className="h-8 px-2 text-right max-sm:hidden">Last seen</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {values.map((v) => (
          <TableRow
            key={v.value}
            onActivate={() => onPick(v.value)}
            title={`Only lines where ${fieldOf(group.by).label} is ${v.value}`}
          >
            <TableCell className={cn(cell, "w-full max-w-0")}>
              <GroupValue value={v} by={group.by} byQuery={byQuery} lensId={lensId} />
            </TableCell>
            <TableCell className={figure}>{v.count.toLocaleString()}</TableCell>
            {errors && (
              <TableCell className={cn(figure, v.errors > 0 && "text-destructive")}>
                {v.errors > 0 ? v.errors.toLocaleString() : "—"}
              </TableCell>
            )}
            {timed && (
              <>
                <TableCell className={cn(figure, "max-sm:hidden")}>
                  {v.sum !== undefined ? format(v.sum) : "—"}
                </TableCell>
                <TableCell className={cn(figure, "max-md:hidden")}>
                  {v.sum !== undefined && v.count > 0 ? format(v.sum / v.count) : "—"}
                </TableCell>
                <TableCell className={figure}>
                  {v.max !== undefined ? format(v.max) : "—"}
                </TableCell>
              </>
            )}
            <TableCell className={cn(figure, "max-sm:hidden")} title={seenTitle(v)}>
              {v.last ? relativeTime(v.last) : "—"}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function seenTitle(v: LogFacetValue) {
  if (!v.first && !v.last) return undefined
  return [v.first && `first ${timestamp(v.first)}`, v.last && `last ${timestamp(v.last)}`]
    .filter(Boolean)
    .join(" · ")
}

function GroupValue({
  value,
  by,
  byQuery,
  lensId,
}: {
  value: LogFacetValue
  by: string
  byQuery: boolean
  lensId: string | undefined
}) {
  const samples = Object.entries(value.samples ?? {}).filter(
    ([key, sample]) => sample !== "" && !(byQuery && key === "query"),
  )
  return (
    <span className="flex min-w-0 flex-col gap-0.5">
      {byQuery ? (
        <span className="truncate font-mono text-xs" title={value.samples?.query}>
          {value.samples?.query ?? value.value}
        </span>
      ) : (
        <span className="flex min-w-0 items-center text-xs">
          <FieldValue name={by} value={value.value} lens={lensId} />
        </span>
      )}
      {samples.length > 0 && (
        <span className="flex min-w-0 items-center gap-3 text-hint text-muted-foreground">
          {samples.map(([key, sample]) => (
            <span key={key} className="flex min-w-0 items-center gap-1.5">
              <span className="shrink-0">{fieldOf(key).label}</span>
              <FieldValue name={key} value={sample} compact lens={lensId} className="min-w-0" />
            </span>
          ))}
        </span>
      )}
    </span>
  )
}

/**
 * The shapes the lines fall into, most frequent first, with the variable
 * parts — numbers, addresses, ids, quoted values — as `<*>`. A press opens
 * History on the lines of that shape.
 */
function PatternTable({ facet, onPick }: { facet: LogFacet; onPick: (pattern: string) => void }) {
  const values = facet.values.slice(0, GROUP_ROWS)
  const errors = values.some((v) => v.errors > 0)
  const cell = "px-2 py-1.5"
  const figure = cn(cell, "numeric text-right text-muted-foreground")
  return (
    <Table containerClassName="-mx-2 w-auto">
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead className="h-8 px-2">Pattern</TableHead>
          <TableHead className="h-8 px-2 text-right">Lines</TableHead>
          {errors && <TableHead className="h-8 px-2 text-right">Errors</TableHead>}
          <TableHead className="h-8 px-2 text-right max-sm:hidden">Last seen</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {values.map((v) => (
          <TableRow
            key={v.value}
            onActivate={() => onPick(v.value)}
            title="Show the lines of this pattern in History"
          >
            <TableCell className={cn(cell, "w-full max-w-0")}>
              <span className="block truncate font-mono text-xs" title={v.value}>
                {v.value.split("<*>").map((part, i) => (
                  <span key={i}>
                    {i > 0 && <span className="text-muted-foreground/60">&lt;*&gt;</span>}
                    {part}
                  </span>
                ))}
              </span>
            </TableCell>
            <TableCell className={figure}>{v.count.toLocaleString()}</TableCell>
            {errors && (
              <TableCell className={cn(figure, v.errors > 0 && "text-destructive")}>
                {v.errors > 0 ? v.errors.toLocaleString() : "—"}
              </TableCell>
            )}
            <TableCell className={cn(figure, "max-sm:hidden")} title={seenTitle(v)}>
              {v.last ? relativeTime(v.last) : "—"}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
