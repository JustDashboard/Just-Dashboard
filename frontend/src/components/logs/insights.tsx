"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { LineChart, MagnifyingGlassMinus } from "@/components/icons"
import { cn } from "@/lib/utils"
import { errorMessage, get } from "@/lib/api"
import { bytes, clock, plural, relativeTime, timestamp } from "@/lib/format"
import { hueFor, LANES } from "@/lib/hue"
import { latency, type StatusClass } from "@/lib/requests"
import type { LogBucket, LogFacet, LogFacetValue, LogSearchResult } from "@/lib/types"
import type { LogFilterState, LogTimeRange } from "@/components/logs/types"
import {
  LEVEL_LABEL,
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
import { eventLabel, eventMeta, type LensGroup, type LogLens } from "@/lib/log-lenses"
import { RankList, SplitBar, percent, type RankItem } from "@/components/rank-list"
import { FAMILY_HUE, Facet } from "@/components/deploy/traffic-facets"
import { LatencyReadings } from "@/components/deploy/latency-readings"
import { FieldMark, FieldValue } from "@/components/logs/field-value"
import { Histogram, SERIES_HUES } from "@/components/logs/histogram"
import { TileTrend } from "@/components/metrics/sparkline"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { StatButton, StatGrid, StatTile, TileBand } from "@/components/stat-tile"
import { NumberTicker } from "@/components/ui/number-ticker"
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

/**
 * The keys every line has exactly one of, so their values are parts of one
 * whole: drawn as one strip over the list, in the colours the chart above
 * draws them in, the way the request record draws its status codes.
 */
const WHOLE = new Set(["level", "event", "class"])

/** Each level in the chart's swatch (`LEVEL_DOT`), as a colour a strip can take. */
const LEVEL_HUE: Record<LogLevel, string> = {
  critical: "var(--destructive)",
  error: "color-mix(in oklab, var(--destructive) 70%, transparent)",
  warn: "var(--warning)",
  info: "var(--chart-2)",
  debug: "color-mix(in oklab, var(--muted-foreground) 60%, transparent)",
  unknown: "color-mix(in oklab, var(--muted-foreground) 30%, transparent)",
}

const TONE_HUE = {
  danger: "var(--destructive)",
  warning: "var(--warning)",
  success: "var(--success)",
} as const

type InsightsProps = {
  /** False leaves out the row of figures over the chart. */
  tiles?: false
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
  onZoom: (since: Date, until: Date) => void
  /** The overview's answer, for the lens row's counts. */
  onOverview?: (result: LogSearchResult | null) => void
}

type Answer = {
  /** The source the answer is about: another source's never stands in for this one's. */
  source: string
  overview: LogSearchResult
  /** Each group's answer, or null for a group the reader's filter rules out. */
  groups: Record<string, LogSearchResult | null>
}

/**
 * What the log adds up to over the window and the filter on screen.
 *
 * The request log's Insights, for any log a lens reads, and drawn the way the
 * request record's is: the window's figures, the events over time, the
 * measure's figures where there is one, each of the lens's keys as a ranked
 * list of figures and shares (a press narrows to the value and stays here,
 * as the request lists do), each of the lens's groups as a ranked table — the
 * slow statements by their shape, the attackers by address — and the patterns
 * every line falls into, which a log with no lens has too.
 *
 * One search for the overview and one per group, in parallel: the groups
 * each rank one key under their own predicates and measure, which one search
 * cannot. They run when the view opens and again when a chip, a field or the
 * window changes; the words in the search box wait for Enter, as History's do.
 * While a new question is being answered the last answer stays, slightly
 * dimmed, and only its figures move when the new one lands: nothing remounts
 * and nothing drops to a skeleton, the rule the request record keeps.
 */
export function Insights(props: InsightsProps) {
  const { lens, lensId, filter, onFilterChange } = props
  const { answer: held, error, loading } = useInsights(props)
  // Held over a change of question, never over a change of source.
  const answer = held?.source === props.sourceId ? held : null
  const overview = answer?.overview

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
  const timed = Boolean(measure?.format === "ms" && overview?.measure && overview.measure.count > 0)
  const ranks = overviewFacets(lens).filter(
    (key) => key !== "pattern" && !BY_SAMPLE_ONLY.has(key) && telling(key, overview?.facets?.[key]),
  )
  // The levels on their own are what the tiles, the chart's legend and the
  // level chips already count, three times over; as the only list they were a
  // fourth saying of it, in half the pane beside a hole.
  const lists = ranks.length === 1 && ranks[0] === "level" ? [] : ranks
  const columns = [lists.filter((_, i) => i % 2 === 0), lists.filter((_, i) => i % 2 === 1)]
  const patterns = overview?.facets?.pattern
  const groups = lens?.groups ?? []
  const ranked = lists.length > 0 || groups.length > 0 || (patterns?.values.length ?? 0) > 0

  return (
    <div className="flex min-h-0 flex-1 flex-col">
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
        <div
          aria-busy={loading}
          className={cn("flex flex-col transition-opacity duration-150", loading && "opacity-70")}
        >
          {props.tiles !== false && (
            <Readings
              overview={answer.overview}
              lens={lens}
              lensId={lensId}
              loading={loading}
              filter={filter}
              onFilterChange={onFilterChange}
            />
          )}
          {answer.overview.histogram.length > 0 && (
            <Histogram
              heading
              className="animate-rise"
              title={lens ? "Events over time" : "Lines over time"}
              buckets={answer.overview.histogram}
              bucketSeconds={answer.overview.bucketSeconds ?? 60}
              by={answer.overview.histogramBy}
              lens={lensId}
              onZoom={props.onZoom}
            />
          )}

          <div className="flex flex-col gap-8 px-5 py-5">
            {timed && measure && (
              <Facet
                title={measure.label}
                reading={`${plural(answer.overview.measure!.count, "timed line")} · press a figure to see those lines`}
                index={0}
              >
                <LatencyReadings
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
                        <FacetList
                          name={key}
                          facet={answer.overview.facets![key]}
                          matched={answer.overview.matched}
                          lensId={lensId}
                          narrow={narrow}
                        />
                      </Facet>
                    ))}
                  </div>
                ))}
              </div>
            )}

            {groups.map((group, i) => (
              <Facet
                key={group.id}
                title={group.label}
                reading={
                  answer.groups[group.id]?.facets?.[group.by]?.values.length
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
                index={lists.length + groups.length + 1}
              >
                <PatternTable
                  facet={patterns}
                  onPick={(pattern) =>
                    props.onShowLines({ ...filter, q: patternRegex(pattern), regex: true })
                  }
                />
              </Facet>
            )}

            {!ranked && !timed && (
              <EmptyState
                icon={LineChart}
                title="Nothing to rank here"
                description={
                  lens
                    ? `These lines carry none of the values a ${lens.label} log is ranked by. History has them as they were written.`
                    : "This log is read as plain text, so its lines carry no fields to rank. Read it through a lens under Options to rank its events and values."
                }
              />
            )}
          </div>
        </div>
      )}
    </div>
  )
}

/**
 * Whether a key's list says anything. A level list of one level is every line
 * at that level, which the tiles and the level chips already say — and on a
 * log that writes no levels at all it was one bar the width of the pane
 * reading "other".
 */
function telling(key: string, facet: LogFacet | undefined) {
  const values = facet?.values.length ?? 0
  if (key === "level") return values > 1
  return values > 0
}

const ERROR_LEVELS: LogLevel[] = ["critical", "error"]
const WARN_LEVELS: LogLevel[] = ["warn"]

function sameLevels(a: readonly string[], b: readonly string[]) {
  return a.length === b.length && a.every((level) => b.includes(level))
}

/**
 * Each column's count of the lines in a tone: the levels themselves where the
 * chart is counted by level, and the events the lens marks in that tone where
 * it is counted by event — a deadlock and an auth failure are the errors of a
 * Postgres log's chart.
 */
function toneSeries(
  buckets: LogBucket[],
  by: string | undefined,
  lensId: string | undefined,
  levels: LogLevel[],
  tone: "danger" | "warning",
) {
  if (!by || by === "level") {
    return buckets.map((b) => levels.reduce((n, level) => n + (b.counts[level] ?? 0), 0))
  }
  if (by !== "event") return []
  return buckets.map((b) =>
    Object.entries(b.counts).reduce(
      (n, [event, count]) => (eventMeta(lensId, event)?.tone === tone ? n + count : n),
      0,
    ),
  )
}

/** A trend on its own peak, so a quiet window still draws its line rather than a hole. */
function Trend({ values, label, color }: { values: number[]; label: string; color: string }) {
  return <TileTrend values={values} label={label} color={color} max={Math.max(...values, 1)} />
}

/**
 * What the window adds up to, before any list: how many lines, how many of
 * them failed or warned, when it was busiest, and — where the lens reads one
 * — how many distinct clients spoke. Each figure counts up as it lands and
 * keeps one picture line, as the request record's tiles do, so the names, the
 * figures and the hints stand in rows: the lines' shape across the window,
 * the errors' and the warnings' (flat when there were none), where the
 * busiest column fell, and how the clients split. The errors and the warnings
 * narrow the lines to themselves, as the level chips do, and press again to
 * let go.
 *
 * The measure's p95 used to be a fifth tile here, with the same figure in the
 * block of four under the chart; the block says it in words and narrows on a
 * press, so the tile went.
 */
function Readings({
  overview,
  lens,
  lensId,
  loading,
  filter,
  onFilterChange,
}: {
  overview: LogSearchResult
  lens: LogLens | undefined
  lensId: string | undefined
  loading: boolean
  filter: LogFilterState
  onFilterChange: (filter: LogFilterState) => void
}) {
  const levels = Object.fromEntries(
    (overview.facets?.level?.values ?? []).map((v) => [v.value, v.count] as const),
  )
  const errors = (levels.critical ?? 0) + (levels.error ?? 0)
  const warnings = levels.warn ?? 0
  const buckets = overview.histogram
  const totals = buckets.map((b) => b.total)
  // A figure of none draws none: by event, the chart's amber is the lens's
  // warning-toned events (a slow statement), which a level count of no
  // warnings would otherwise sit over as a rising line.
  const quiet = buckets.map(() => 0)
  const peakAt = totals.indexOf(Math.max(0, ...totals))
  const peak = peakAt >= 0 ? buckets[peakAt] : undefined
  const width = overview.bucketSeconds ?? 60
  const perMinute = peak ? Math.round((peak.total * 60) / width) : 0
  const share = (n: number) =>
    overview.matched
      ? `${((n / overview.matched) * 100).toFixed(n / overview.matched < 0.1 ? 1 : 0)}% of lines`
      : ""
  // The lens's own "who": the first key it reads as an address, counted.
  const who = lens?.facets.find((key) => fieldOf(key).kind === "address")
  const whoFacet = who ? overview.facets?.[who] : undefined
  // Every tile keeps a picture line while any of them has a picture to draw;
  // over lines that carry no time none does, and five empty bands were a
  // row of tiles a band taller than anything in them.
  const banded = buckets.length > 1 || Boolean(whoFacet)
  const press = (wanted: LogLevel[]) =>
    onFilterChange({ ...filter, levels: sameLevels(filter.levels, wanted) ? [] : wanted })
  // The overview is asked with the level chips on screen, so a level they
  // leave out reads as none: "nothing failed" under a Warnings chip was the
  // tile saying something the window did not.
  const leftOut = (levels: LogLevel[]) =>
    filter.levels.length > 0 && !levels.some((level) => filter.levels.includes(level))
  const errorsOut = leftOut(ERROR_LEVELS)
  const warningsOut = leftOut(WARN_LEVELS)

  return (
    <StatGrid columns={whoFacet ? 5 : 4} dense className="shrink-0 border-b border-hairline">
      <StatTile
        label="Lines"
        value={<NumberTicker value={overview.matched} />}
        trend={
          banded && (
            <TileBand>
              <Trend values={totals} label="Lines across the window" color="var(--chart-2)" />
            </TileBand>
          )
        }
        hint={
          loading
            ? "Reading the window…"
            : `of ${overview.scanned.toLocaleString()} read · ${overview.tookMillis}ms`
        }
      />
      <StatButton
        label="Show only the errors"
        pressed={sameLevels(filter.levels, ERROR_LEVELS)}
        onClick={() => press(ERROR_LEVELS)}
      >
        <StatTile
          label="Errors"
          tone={errorsOut ? "default" : errors > 0 ? "danger" : "success"}
          value={errorsOut ? "—" : errors > 0 ? <NumberTicker value={errors} /> : "None"}
          trend={
            banded && (
              <TileBand>
                {!errorsOut && (
                  <Trend
                    values={
                      errors > 0
                        ? toneSeries(buckets, overview.histogramBy, lensId, ERROR_LEVELS, "danger")
                        : quiet
                    }
                    label="Errors across the window"
                    color={errors > 0 ? "var(--destructive)" : "var(--success)"}
                  />
                )}
              </TileBand>
            )
          }
          hint={
            errorsOut
              ? "left out by the level chips"
              : errors > 0
                ? share(errors)
                : "nothing failed in this window"
          }
          className="h-full group-hover:bg-row-hover"
        />
      </StatButton>
      <StatButton
        label="Show only the warnings"
        pressed={sameLevels(filter.levels, WARN_LEVELS)}
        onClick={() => press(WARN_LEVELS)}
      >
        <StatTile
          label="Warnings"
          tone={!warningsOut && warnings > 0 ? "warning" : "default"}
          value={warningsOut ? "—" : warnings > 0 ? <NumberTicker value={warnings} /> : "None"}
          trend={
            banded && (
              <TileBand>
                {!warningsOut && (
                  <Trend
                    values={
                      warnings > 0
                        ? toneSeries(buckets, overview.histogramBy, lensId, WARN_LEVELS, "warning")
                        : quiet
                    }
                    label="Warnings across the window"
                    color={warnings > 0 ? "var(--warning)" : "var(--muted-foreground)"}
                  />
                )}
              </TileBand>
            )
          }
          hint={
            warningsOut
              ? "left out by the level chips"
              : warnings > 0
                ? share(warnings)
                : "nothing to look at"
          }
          className="h-full group-hover:bg-row-hover"
        />
      </StatButton>
      <StatTile
        label="Busiest"
        value={peak && peak.total > 0 ? <NumberTicker value={perMinute} /> : "—"}
        trailing={peak && peak.total > 0 ? "a minute" : undefined}
        trend={
          banded && (
            <TileBand>
              {peak && peak.total > 0 && buckets.length > 1 && (
                <PeakStrip buckets={buckets} at={peakAt} />
              )}
            </TileBand>
          )
        }
        hint={
          peak && peak.total > 0
            ? `at ${clock(peak.start)}`
            : // The chart counts the lines that carry a time; a log whose lines
              // carry none has matches and no column to be busiest.
              overview.matched > 0
              ? "these lines carry no time"
              : "nothing in the window"
        }
      />
      {whoFacet && who && (
        <StatTile
          label={titleOf(who)}
          value={
            <>
              <NumberTicker value={whoFacet.distinct} />
              {whoFacet.distinctCapped ? "+" : ""}
            </>
          }
          trend={
            <TileBand center>
              <SplitBar
                parts={[
                  ...whoFacet.values.slice(0, 5).map((v) => ({
                    key: v.value,
                    count: v.count,
                    color: hueFor(v.value, LANES),
                  })),
                  {
                    key: "rest",
                    count:
                      whoFacet.other + whoFacet.values.slice(5).reduce((n, v) => n + v.count, 0),
                    color: "var(--tag-slate)",
                  },
                ]}
                label={whoFacet.values
                  .slice(0, 5)
                  .map((v) => `${v.value} ${plural(v.count, "line")}`)
                  .join(", ")}
              />
            </TileBand>
          }
          hint={
            whoFacet.values[0]
              ? `busiest ${whoFacet.values[0].value} · ${plural(whoFacet.values[0].count, "line")}`
              : `no ${fieldOf(who).label} in these lines`
          }
        />
      )}
    </StatGrid>
  )
}

/**
 * Where in the window the busiest column fell: one cell per column of the
 * chart below, as bright as it was busy, the busiest lit whole and a column
 * with no lines the empty track — the request record's strip of the hour,
 * the pattern rather than a second line chart beside the Lines tile's.
 */
function PeakStrip({ buckets, at }: { buckets: LogBucket[]; at: number }) {
  const peak = Math.max(buckets[at]?.total ?? 0, 1)
  return (
    <div
      role="img"
      aria-label={`Busiest at ${clock(buckets[at].start)}, ${plural(buckets[at].total, "line")}`}
      className="flex h-9 min-w-0 items-end gap-px overflow-hidden pb-0.5"
    >
      {buckets.map((bucket, i) => (
        <span
          key={bucket.start}
          title={`${clock(bucket.start)} · ${plural(bucket.total, "line")}`}
          className={cn(
            "h-5 min-w-px flex-1 rounded-sm",
            bucket.total === 0 ? "bg-meter-track" : "bg-chart-2",
          )}
          style={
            bucket.total === 0 || i === at
              ? undefined
              : { opacity: 0.2 + 0.5 * (bucket.total / peak) }
          }
        />
      ))}
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
      if (window.error) throw new Error(window.error)
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
        source: p.sourceId,
        overview,
        groups: Object.fromEntries(groups.map((group, i) => [group.id, ranked[i]])),
      })
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

  return { answer, error, loading }
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

/**
 * Each value of a whole in the colour the chart draws it in: a level in its
 * chip's swatch, a status class in its family's, an event in its tone or —
 * one with no verdict — in the chart's hues by rank, busiest first.
 */
function swatchesOf(name: string, values: LogFacetValue[], lensId: string | undefined) {
  let neutral = 0
  return Object.fromEntries(
    values.map((v) => {
      if (name === "level") return [v.value, LEVEL_HUE[(v.value || "unknown") as LogLevel]]
      if (name === "class") {
        return [v.value, FAMILY_HUE[v.value as StatusClass] ?? FAMILY_HUE.other]
      }
      const tone = eventMeta(lensId, v.value)?.tone
      if (tone && tone !== "default") return [v.value, TONE_HUE[tone]]
      return [v.value, SERIES_HUES[neutral++ % SERIES_HUES.length]]
    }),
  )
}

/**
 * One key's values, ranked: the name drawn as what it is, the lines it is on
 * and its share of the window's, in columns that end on the head's edge —
 * the request record's lists, which dropped a bar per row because a page of
 * bars of different lengths left every figure floating away from its name.
 * A key every line has one of is a whole, so it gets one strip over the list
 * in the chart's colours, and each row its swatch as the strip's legend.
 */
function FacetList({
  name,
  facet,
  matched,
  lensId,
  narrow,
}: {
  name: string
  facet: LogFacet
  matched: number
  lensId: string | undefined
  narrow: (key: string, value: string) => void
}) {
  const values = facet.values.slice(0, SHOWN)
  const field = fieldOf(name)
  const verdict = name === "event" || name === "level"
  const whole = WHOLE.has(name)
  const swatches = whole ? swatchesOf(name, values, lensId) : undefined
  const marked = field.kind === "address" || field.kind === "product"
  const pressable = name === "level" || isFilterable(name)
  const named = (value: string) =>
    name === "level"
      ? (LEVEL_LABEL[(value || "unknown") as LogLevel] ?? value)
      : name === "event"
        ? eventLabel(lensId, value)
        : value

  const items: RankItem[] = values.map((v) => ({
    key: v.value,
    mark: marked ? <FieldMark name={name} value={v.value} /> : undefined,
    swatch: swatches?.[v.value],
    mono: !(verdict || field.kind === "product" || field.kind === "lane"),
    label: <FieldValue name={name} value={v.value} lens={lensId} glyph={false} />,
    value: v.count.toLocaleString(),
    share: matched > 0 ? v.count / matched : undefined,
    // An event and a level are the verdict already, in their own colour; a
    // client or a path is where the share of errors is the news.
    hint:
      !verdict && v.errors > 0 ? (
        <span className="text-destructive">{plural(v.errors, "error")}</span>
      ) : v.sum !== undefined && v.count > 0 && name !== "level" ? (
        `mean ${latency(v.sum / v.count)}`
      ) : undefined,
    title: pressable
      ? name === "level"
        ? `Only ${named(v.value)} lines`
        : `Only lines where ${field.label} is ${v.value}`
      : undefined,
    onClick: pressable ? () => narrow(name, v.value) : undefined,
  }))

  return (
    <>
      {swatches && (
        <SplitBar
          className="mb-1.5"
          parts={values.map((v) => ({ key: v.value, count: v.count, color: swatches[v.value] }))}
          label={values
            .map((v) => `${percent(matched > 0 ? v.count / matched : 0)} ${named(v.value)}`)
            .join(", ")}
        />
      )}
      <RankList items={items} />
    </>
  )
}

function measured(key: string | undefined) {
  const kind = key ? fieldOf(key).kind : undefined
  return (n: number) =>
    kind === "duration" ? latency(n) : kind === "bytes" ? bytes(n) : n.toLocaleString()
}

/**
 * A table's head and cells, the request record's Slowest table's: a quiet
 * row of names over rows of one height, the figures in columns at the right.
 */
const HEAD = "h-6 px-2 text-micro font-normal"
const CELL = "h-8 px-2 py-0"
const FIGURE = cn(CELL, "numeric text-right text-muted-foreground")

/**
 * A group, ranked: what its key's values add up to under the group's own
 * predicates — how many lines, how many of them errors, how long they took
 * in all and at worst, when they were last seen — with the last value of the
 * group's sample keys beside each, so a statement's shape reads as the
 * statement and an attacking address as the usernames it tried. A press
 * narrows everything above to that value.
 *
 * Every row is one line. The sample used to be a second line under the
 * value, so a row with one stood taller than a row without, and the samples
 * now sit beside the value and give way before it does.
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

  return (
    <Table containerClassName="-mx-2 w-auto">
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead className={HEAD}>{byQuery ? "Query" : titleOf(group.by)}</TableHead>
          <TableHead className={cn(HEAD, "text-right")}>Lines</TableHead>
          {errors && <TableHead className={cn(HEAD, "text-right")}>Errors</TableHead>}
          {timed && (
            <>
              <TableHead className={cn(HEAD, "text-right max-sm:hidden")}>Total</TableHead>
              <TableHead className={cn(HEAD, "text-right max-md:hidden")}>Mean</TableHead>
              <TableHead className={cn(HEAD, "text-right")}>Max</TableHead>
            </>
          )}
          <TableHead className={cn(HEAD, "text-right max-sm:hidden")}>Last seen</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {values.map((v) => (
          <TableRow
            key={v.value}
            onActivate={() => onPick(v.value)}
            title={`Only lines where ${fieldOf(group.by).label} is ${v.value}`}
          >
            <TableCell className={cn(CELL, "w-full max-w-0")}>
              <GroupValue value={v} by={group.by} byQuery={byQuery} lensId={lensId} />
            </TableCell>
            <TableCell className={cn(FIGURE, "font-medium text-foreground")}>
              {v.count.toLocaleString()}
            </TableCell>
            {errors && (
              <TableCell className={cn(FIGURE, v.errors > 0 && "text-destructive")}>
                {v.errors > 0 ? v.errors.toLocaleString() : "—"}
              </TableCell>
            )}
            {timed && (
              <>
                <TableCell className={cn(FIGURE, "max-sm:hidden")}>
                  {v.sum !== undefined ? format(v.sum) : "—"}
                </TableCell>
                <TableCell className={cn(FIGURE, "max-md:hidden")}>
                  {v.sum !== undefined && v.count > 0 ? format(v.sum / v.count) : "—"}
                </TableCell>
                <TableCell className={FIGURE}>
                  {v.max !== undefined ? format(v.max) : "—"}
                </TableCell>
              </>
            )}
            <TableCell className={cn(FIGURE, "max-sm:hidden")} title={seenTitle(v)}>
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

/**
 * A group's value and its samples on one line. The value keeps its width and
 * the samples give way first, all of them, before the value loses a
 * character: they wrap onto a line the row does not show rather than
 * standing as a stub, as `RankList`'s hints do.
 */
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
    <span className="flex h-4 min-w-0 flex-wrap items-baseline gap-x-3 overflow-hidden text-xs leading-4">
      {byQuery ? (
        <span className="max-w-full min-w-0 shrink truncate font-mono" title={value.samples?.query}>
          {value.samples?.query ?? value.value}
        </span>
      ) : (
        <span className="flex max-w-full min-w-0 shrink-0 items-center">
          <FieldValue name={by} value={value.value} lens={lensId} />
        </span>
      )}
      {samples.length > 0 && (
        <span className="flex min-w-[6ch] flex-[1_1_6ch] items-baseline gap-3 overflow-hidden text-micro leading-4 text-muted-foreground">
          {samples.map(([key, sample]) => (
            <span key={key} className="flex min-w-0 items-baseline gap-1.5">
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
  return (
    <Table containerClassName="-mx-2 w-auto">
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead className={HEAD}>Pattern</TableHead>
          <TableHead className={cn(HEAD, "text-right")}>Lines</TableHead>
          {errors && <TableHead className={cn(HEAD, "text-right")}>Errors</TableHead>}
          <TableHead className={cn(HEAD, "text-right max-sm:hidden")}>Last seen</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {values.map((v) => (
          <TableRow
            key={v.value}
            onActivate={() => onPick(v.value)}
            title="Show the lines of this pattern in History"
          >
            <TableCell className={cn(CELL, "w-full max-w-0")}>
              <span className="block truncate font-mono text-xs" title={v.value}>
                {v.value.split("<*>").map((part, i) => (
                  <span key={i}>
                    {i > 0 && <span className="text-muted-foreground/60">&lt;*&gt;</span>}
                    {part}
                  </span>
                ))}
              </span>
            </TableCell>
            <TableCell className={cn(FIGURE, "font-medium text-foreground")}>
              {v.count.toLocaleString()}
            </TableCell>
            {errors && (
              <TableCell className={cn(FIGURE, v.errors > 0 && "text-destructive")}>
                {v.errors > 0 ? v.errors.toLocaleString() : "—"}
              </TableCell>
            )}
            <TableCell className={cn(FIGURE, "max-sm:hidden")} title={seenTitle(v)}>
              {v.last ? relativeTime(v.last) : "—"}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
