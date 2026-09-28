"use client"

import { memo, useCallback, useEffect, useId, useMemo, useRef, useState } from "react"
import { motion, useReducedMotion } from "motion/react"
import { Cross } from "@/components/icons"
import { get } from "@/lib/api"
import { plural, relativeTime, timestamp } from "@/lib/format"
import {
  GRAPH_PAD,
  GRAPH_ROW,
  graphEdges,
  graphLanes,
  graphLineage,
  laneX,
  rowY,
  type GraphEdge,
} from "@/lib/git-graph"
import type { GitBranch, GitGraph, GitGraphCommit } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import type { GitPreview } from "@/components/git/preview-panel"
import { SourceFork } from "@/components/git/glyphs"
import { parseRefs, RefTags } from "@/components/git/ref-tags"
import { SearchInput } from "@/components/page"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { PaneHeader } from "@/components/panel"
import { BlurFade } from "@/components/ui/blur-fade"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/**
 * The branch topology a hosted forge draws and a working copy hides: every
 * local and remote tip laid out in lanes, so "which branch came off which, and
 * where has it got to since" is one glance rather than a mental reconstruction
 * from `git log`.
 *
 * The lanes are assigned on the server (gitx.Graph) — the same rule the docker
 * and proxy renderers follow, one implementation of what the shape means — and
 * how a line travels between two lanes is `lib/git-graph`. Here we only draw:
 * a lane is a coloured column, a commit is a dot on its lane, and pointing at a
 * row lights the line of history that commit is on and steps the rest back.
 *
 * The whole history is one scroll. Pages arrive as the reader nears the foot of
 * what has loaded, down to the server's depth limit, and only the rows in view
 * — with the dots and edges beside them — are rendered. The log consoles lean
 * on `content-visibility` instead, but their rows wrap to any height and these
 * are all one; and a canvas of five thousand dots re-rendered in full on every
 * page that arrived was a second-long stall at a time the browser skipped none
 * of it.
 */

const PAGE = 500
// Rows rendered past each edge of the viewport, so a flick of the wheel lands
// on rows that are already there.
const OVERSCAN = 12
// Rows that stagger in when a graph first lands — about a screen of them. The
// rest are below the fold and arrive without being seen to.
const ARRIVE = 24
const BEAT = 0.02

// Enough hues to tell adjacent lanes apart, each legible on the near-black and
// near-white surfaces this panel renders against. Lanes past the end wrap.
//
// The `--tag-*` tokens, which exist for exactly this: eight fixed hues, held at
// one lightness that holds up against both cards, and deliberately *not*
// computed from the palette — a lane's colour is an identity, and an identity
// that shifted with the theme would stop being the same lane. This file used to
// carry its own eight hex literals, which were the same idea arrived at twice.
const LANES = [
  "var(--tag-blue)",
  "var(--tag-green)",
  "var(--tag-amber)",
  "var(--tag-violet)",
  "var(--tag-red)",
  "var(--tag-cyan)",
  "var(--tag-pink)",
  "var(--tag-slate)",
]

const laneColour = (col: number) => LANES[col % LANES.length]

type Loaded = {
  query: string
  page?: GitGraph
  rows: GitGraphCommit[]
  lanes: number
  total?: number
  hasMore: boolean
}

export function GraphPanel({
  repoPath,
  onClose,
  onSelect,
}: {
  repoPath: string
  onClose: () => void
  onSelect: (p: GitPreview) => void
}) {
  // A commit opens in the same column, replacing the graph — the same move
  // the history list makes, and the graph is one button away again.
  const show = useCallback(
    (c: GitGraphCommit) => onSelect({ kind: "commit", sha: c.sha, subject: c.subject }),
    [onSelect],
  )

  const [search, setSearch] = useState("")
  const [term, setTerm] = useState("")
  const [ref, setRef] = useState("all")
  useEffect(() => {
    const timer = setTimeout(() => setTerm(search.trim()), 300)
    return () => clearTimeout(timer)
  }, [search])
  const branches = usePoll(
    (signal) => get<GitBranch[]>("/git/branches", { path: repoPath }, signal),
    0,
    [repoPath],
  )

  // How far down the reader has asked for belongs to one query: a new search,
  // branch or repository starts again from the top.
  const query = [repoPath, term, ref].join("\0")
  const [want, setWant] = useState({ query, skip: 0 })
  const skip = want.query === query ? want.skip : 0

  const page = usePoll(
    (signal) =>
      get<GitGraph>(
        "/git/graph",
        {
          path: repoPath,
          limit: PAGE,
          skip,
          search: term || undefined,
          ref: ref === "all" ? undefined : ref,
        },
        signal,
      ),
    0,
    [repoPath, skip, term, ref],
  )

  // Each page is laid out on the server from the top of the history, so its
  // lanes carry on from the page above and appending is all it takes to join
  // them. Folded in during render rather than in an effect, so a page never
  // paints once on its own before it is part of the graph.
  const [loaded, setLoaded] = useState<Loaded>({ query, rows: [], lanes: 0, hasMore: false })
  let view = loaded
  if (view.query !== query) view = { query, rows: [], lanes: 0, hasMore: false }
  if (page.data && page.data !== view.page && (page.data.skip ?? 0) === view.rows.length) {
    view = {
      query,
      page: page.data,
      rows: view.rows.concat(page.data.commits),
      lanes: Math.max(view.lanes, page.data.lanes),
      total: page.data.total ?? view.total,
      hasMore: !!page.data.hasMore,
    }
  }
  if (view !== loaded) setLoaded(view)

  const rows = view.rows
  const searching = term !== ""
  const older = view.hasMore && !page.loading
  const more = useCallback(() => setWant({ query, skip: rows.length }), [query, rows.length])

  const reduced = useReducedMotion()
  const [drawnFor, setDrawnFor] = useState<string>()
  const drawn = reduced || drawnFor === query
  const onDrawn = useCallback(() => setDrawnFor(query), [query])

  // The window is kept in rows, not pixels, so scrolling within a row renders
  // nothing at all.
  const scroller = useRef<HTMLDivElement>(null)
  const sentinel = useRef<HTMLDivElement>(null)
  const [scroll, setScroll] = useState({ query, first: 0 })
  const [visible, setVisible] = useState(32)
  const first = scroll.query === query ? scroll.first : 0
  const start = Math.max(0, first - OVERSCAN)
  const end = Math.min(rows.length, first + visible + OVERSCAN)
  const hasRows = rows.length > 0
  useEffect(() => {
    const el = scroller.current
    if (!el) return
    const observer = new ResizeObserver(() => setVisible(Math.ceil(el.clientHeight / GRAPH_ROW)))
    observer.observe(el)
    return () => observer.disconnect()
  }, [hasRows])
  useEffect(() => {
    if (!older || !sentinel.current) return
    const observer = new IntersectionObserver(
      (entries) => entries.some((e) => e.isIntersecting) && more(),
      { root: scroller.current, rootMargin: "0px 0px 1200px 0px" },
    )
    observer.observe(sentinel.current)
    return () => observer.disconnect()
  }, [older, more])

  const [focus, setFocus] = useState<{ query: string; row: number }>()
  const focused = focus?.query === query && focus.row < rows.length ? focus.row : undefined
  const pointAt = useCallback((row: number) => setFocus({ query, row }), [query])
  const line = useMemo(
    () => (focused === undefined || searching ? undefined : graphLineage(rows, focused)),
    [rows, focused, searching],
  )

  const lanes = searching ? 1 : graphLanes(rows, view.lanes)
  const gutter = laneX(lanes - 1) + GRAPH_PAD
  const height = rows.length * GRAPH_ROW
  const edges = useMemo(() => graphEdges(rows, height), [rows, height])
  const total = view.total ?? rows.length
  const count = (n: number) => n.toLocaleString()
  const noun = (n: number) => (searching ? plural(n, "match", "matches") : plural(n, "commit"))
  const status = view.hasMore
    ? page.loading
      ? `Loading older commits… ${count(rows.length)} of ${count(total)}`
      : `${count(rows.length)} of ${noun(total)}`
    : rows.length < total
      ? `The newest ${count(rows.length)} of ${noun(total)} — search or pick a branch to reach older ones`
      : `All ${noun(total)}`

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PaneHeader className="gap-2 px-3">
        <div className="min-w-0 flex-1">
          <p className="truncate text-body font-medium">Branch graph</p>
          <p className="truncate text-hint text-muted-foreground">
            {noun(total)} · {ref === "all" ? "all branches and tags" : ref}
          </p>
        </div>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              size="sm"
              variant="ghost"
              className="size-7 shrink-0 p-0 text-muted-foreground hover:text-foreground"
              aria-label="Close"
              onClick={onClose}
            >
              <Cross className="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>Close the graph</TooltipContent>
        </Tooltip>
      </PaneHeader>

      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-hairline px-3 py-2">
        <SearchInput
          dense
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          aria-label="Search graph commits"
          placeholder="Search messages…"
          containerClassName="min-w-0 flex-1 sm:w-auto"
        />
        <Select value={ref} onValueChange={setRef}>
          <SelectTrigger size="sm" aria-label="Graph branch" className="max-w-48">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All branches and tags</SelectItem>
            {branches.data?.map((branch) => (
              <SelectItem key={branch.name} value={branch.name}>
                {branch.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      {page.error && <ErrorState error={page.error} className="m-3" />}

      {rows.length === 0 ? (
        page.loading ? (
          <LoadingRows className="p-3" rows={10} />
        ) : (
          !page.error && (
            <EmptyState
              className="m-3"
              icon={SourceFork}
              title={searching ? "No commit messages match" : "No commits yet"}
            />
          )
        )
      ) : (
        <div
          ref={scroller}
          className="@container min-h-0 flex-1 overflow-auto"
          onScroll={(e) => {
            const top = Math.floor(e.currentTarget.scrollTop / GRAPH_ROW)
            setScroll((p) => (p.query === query && p.first === top ? p : { query, first: top }))
          }}
        >
          <div
            className="group/graph relative"
            data-focus={line ? "" : undefined}
            style={{ height }}
            onPointerLeave={() => setFocus(undefined)}
          >
            <GraphCanvas
              key={query}
              rows={rows}
              edges={edges}
              start={start}
              end={end}
              gutter={gutter}
              height={height}
              searching={searching}
              drawn={drawn}
              onDrawn={onDrawn}
            />
            {line && (
              <LineOverlay
                rows={rows}
                edges={edges}
                line={line}
                start={start}
                end={end}
                gutter={gutter}
                height={height}
              />
            )}
            {rows.slice(start, end).map((c, k) => (
              <GraphRow
                key={c.sha}
                commit={c}
                row={start + k}
                gutter={gutter}
                arrive={!drawn && start + k < ARRIVE ? (start + k) * BEAT : undefined}
                onOpen={show}
                onPoint={pointAt}
              />
            ))}
            <div ref={sentinel} className="absolute inset-x-0 bottom-0 h-px" aria-hidden />
          </div>
        </div>
      )}
      {rows.length > 0 && (
        <div className="flex shrink-0 items-center gap-2 border-t border-hairline px-3 py-2">
          <span className="numeric min-w-0 flex-1 truncate text-hint text-muted-foreground">
            {status}
          </span>
          {view.hasMore && (
            <Button size="xs" variant="outline" disabled={page.loading} onClick={more}>
              Load older
            </Button>
          )}
        </div>
      )}
    </div>
  )
}

/**
 * The lanes and the dots for the rows in the window, and every edge that passes
 * through it. Memoised, so pointing at a row — which only changes the overlay
 * above it — redraws nothing here.
 */
const GraphCanvas = memo(function GraphCanvas({
  rows,
  edges,
  start,
  end,
  gutter,
  height,
  searching,
  drawn,
  onDrawn,
}: {
  rows: GitGraphCommit[]
  edges: GraphEdge[]
  start: number
  end: number
  gutter: number
  height: number
  searching: boolean
  drawn: boolean
  onDrawn: () => void
}) {
  // The first screen of lanes draws itself down the page in step with the rows
  // arriving beside it, then the clip comes off for good.
  const reveal = Math.min(height, ARRIVE * GRAPH_ROW)
  const clip = useId()

  return (
    <svg
      className="pointer-events-none absolute top-0 left-0 z-[1] transition-opacity duration-200 group-data-[focus]/graph:opacity-30"
      width={gutter}
      height={height}
      aria-hidden
    >
      {!drawn && (
        <defs>
          <clipPath id={clip}>
            <motion.rect
              x={0}
              y={0}
              width={gutter}
              initial={{ height: 0 }}
              animate={{ height: reveal }}
              transition={{ duration: ARRIVE * BEAT + 0.34, ease: [0.16, 1, 0.3, 1] }}
              onAnimationComplete={onDrawn}
            />
          </clipPath>
        </defs>
      )}
      <g clipPath={drawn ? undefined : `url(#${clip})`}>
        {searching ? (
          <line
            x1={laneX(0)}
            y1={rowY(0)}
            x2={laneX(0)}
            y2={rowY(rows.length - 1)}
            stroke={laneColour(0)}
            strokeWidth={2}
            strokeLinecap="round"
            strokeDasharray="0 6"
          />
        ) : (
          edges
            .filter(within(start, end))
            .map((e) => (
              <path
                key={e.key}
                d={e.d}
                fill="none"
                stroke={laneColour(e.lane)}
                strokeWidth={2}
                strokeLinecap="round"
              />
            ))
        )}
        {rows.slice(start, end).map((c, k) => (
          <Dot key={c.sha} commit={c} row={start + k} gutter={searching ? undefined : gutter} />
        ))}
      </g>
    </svg>
  )
})

/** The pointed-at commit's line, drawn again at full strength over the dimmed canvas. */
function LineOverlay({
  rows,
  edges,
  line,
  start,
  end,
  gutter,
  height,
}: {
  rows: GitGraphCommit[]
  edges: GraphEdge[]
  line: Set<number>
  start: number
  end: number
  gutter: number
  height: number
}) {
  const near = within(start, end)
  return (
    <svg
      className="pointer-events-none absolute top-0 left-0 z-[2]"
      width={gutter}
      height={height}
      aria-hidden
    >
      {edges
        .filter((e) => e.first && near(e) && line.has(e.from) && (e.to === -1 || line.has(e.to)))
        .map((e) => (
          <path
            key={e.key}
            d={e.d}
            fill="none"
            stroke={laneColour(e.lane)}
            strokeWidth={2.5}
            strokeLinecap="round"
          />
        ))}
      {[...line]
        .filter((i) => i >= start && i < end)
        .map((i) => (
          <Dot key={rows[i].sha} commit={rows[i]} row={i} gutter={gutter} />
        ))}
    </svg>
  )
}

/** Whether an edge passes through rows [start, end) — one still waiting on its parent always does. */
const within = (start: number, end: number) => (e: GraphEdge) =>
  e.from < end && (e.to === -1 || e.to >= start)

/**
 * A commit's mark. The checked-out commit is the one place the brand appears —
 * `--brand` is "where you are" — ringed so it is found at a glance in a column
 * of dots; a merge is hollow, because it joins lines rather than adding work;
 * and a commit something points at is a step larger, with a thread out to the
 * names beside it so a tip in lane 4 is not read as belonging to lane 0.
 */
function Dot({ commit, row, gutter }: { commit: GitGraphCommit; row: number; gutter?: number }) {
  const cx = laneX(commit.col)
  const cy = rowY(row)
  const colour = laneColour(commit.col)
  const refs = parseRefs(commit.refs)
  const head = refs.some((r) => r.kind === "head")
  return (
    <g>
      {gutter !== undefined && refs.length > 0 && gutter - cx > 16 && (
        <line
          x1={cx + 7}
          y1={cy}
          x2={gutter - 4}
          y2={cy}
          stroke={head ? "var(--brand)" : colour}
          strokeOpacity={0.5}
          strokeDasharray="2 3"
        />
      )}
      {head ? (
        <>
          <circle cx={cx} cy={cy} r={8} fill="none" stroke="var(--brand)" strokeOpacity={0.4} />
          <circle cx={cx} cy={cy} r={4.5} fill="var(--brand)" />
        </>
      ) : commit.isMerge ? (
        <circle cx={cx} cy={cy} r={3.75} fill="var(--card)" stroke={colour} strokeWidth={2} />
      ) : (
        <circle cx={cx} cy={cy} r={refs.length > 0 ? 5 : 4} fill={colour} />
      )}
    </g>
  )
}

const GraphRow = memo(function GraphRow({
  commit,
  row,
  gutter,
  arrive,
  onOpen,
  onPoint,
}: {
  commit: GitGraphCommit
  row: number
  gutter: number
  arrive?: number
  onOpen: (c: GitGraphCommit) => void
  onPoint: (row: number) => void
}) {
  const button = (
    <button
      onClick={() => onOpen(commit)}
      onPointerEnter={() => onPoint(row)}
      onFocus={() => onPoint(row)}
      className="flex w-full items-center gap-3 pr-3 text-left focus-ring-inset transition-colors hover:bg-row-hover"
      style={{ height: GRAPH_ROW, paddingLeft: gutter }}
    >
      <span className="flex min-w-0 flex-1 items-center gap-1.5">
        <RefTags refs={commit.refs} max={2} className="flex shrink-0 items-center gap-1" />
        <span className="truncate text-body">{commit.subject}</span>
      </span>
      <span className="hidden w-28 shrink-0 truncate text-right text-hint text-muted-foreground @xl:block">
        {commit.author}
      </span>
      <span className="w-16 shrink-0 text-right font-mono text-hint text-muted-foreground">
        {commit.short}
      </span>
      <span
        className="hidden w-20 shrink-0 text-right text-hint whitespace-nowrap text-muted-foreground @md:block"
        title={timestamp(commit.at)}
      >
        {relativeTime(commit.at)}
      </span>
    </button>
  )
  return (
    <div className="absolute inset-x-0" style={{ top: row * GRAPH_ROW }}>
      {arrive === undefined ? button : <BlurFade delay={arrive}>{button}</BlurFade>}
    </div>
  )
})
